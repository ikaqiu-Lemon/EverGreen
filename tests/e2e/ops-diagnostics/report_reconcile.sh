#!/usr/bin/env bash
# 报告 `reconcile` 字段的端到端脚本（T-evergreen.s1_main_flow-158614-057）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §11（恰三键 `ran` / `commit` /
# `findings`；`ran` bool 必填、`commit` 是串或 null、`findings` 数组非 null；`reconcile` 是
# **S3 阶段键**不进 §4.6 的 S1 必填 11 项；非对账命令路径下恒为「键在值空」的占位形态；
# **不新增 `affected` 字段级定义**；`skipped[].kind` 仍恰两值）、§2（Finding 四键）；
# `2026-11-12-m4-visibility-and-execution-contract.md` §1.3 / §1.4（报告体 `git.commit` 与
# `proposals[].execution.git_commit` 两处投影键**都不许动**）；`milestones/M-004-m4.md`
# 完成判据 8 与风险 R-14。
#
# 七段断言（task deliverable 逐字要求）：
#   ① 任取**真实的非对账写命令**跑 `--json`：报告体含 `reconcile` 键，且三键值恰
#      `false` / `null` / `[]`（键在、值为空，不得整键缺席）；
#   ② 换第二条非对账写命令 + `eg report --last` 复现路径：占位形态逐字相同（不随命令变形）；
#   ③ 驱动壳跑「带对账」的那条路径：`ran` 为 `true`、`commit` 是 40 位 sha、
#      `findings` 恰 2 条且元素恰四键（键序即合同 §2）；
#   ④ **两条路径的顶层键序完全一致**（键集合不随路径变化，下游不必分支处理）；
#   ⑤ S1 必填 11 项一字不动：键数恒 11、`git.commit` 仍是嵌套键、顶层无自创 `commit` 键；
#   ⑥ `affected` 仍是**值为 null 的占位键**（M4 不给字段级定义，A-36），
#      `reconcile` 内**无第四键**，`skipped[].kind` 未新增第三种；
#   ⑦ 源码级边界反证：`internal/report` 零 import 对账包（依赖方向单向）、
#      `reconcile.go` 三键计数恰 3、`report.go` 四处投影键计数恒 1、无 `affected` 具名类型、
#      `eg reconcile` / `eg check` 命令仍零注册（本 task 只做报告投影）。
#
# 为什么「带对账的那条路径」用 `go test` 驱动而不是 `eg reconcile`：
#   `eg reconcile` / `eg check` 命令本体属 T-…-058 / T-…-059，本 task 明确不注册命令。
#   照 `m4_r1_takeover.sh` 的先例，脚本用 `test/e2e/m4_report_reconcile_test.go` 这一层
#   驱动壳产出对账路径的报告体，并把**真实 eg 命令输出**的报告体一并回读比对 ——
#   事实一律来自 eg 自己的 `--json` 与驱动壳落盘的 JSON，不看实现自报。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / jq）；
# 一切写操作只发生在 mktemp -d 目录内；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/ops-diagnostics/report_reconcile.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-report.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
CARD='k-20261201-attention'
NOTE='n-20261201-attention'
SRC='s-20261201-attention'
CARD_REL="${KDIR_REL}/${CARD}.md"
NOTE_REL="${NDIR_REL}/${NOTE}.md"
SHA40='0ed1181a7aa214ac81382491025c9137fcd3a14c'
PLACEHOLDER='{"ran":false,"commit":null,"findings":[]}'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}";  eg_staged_cleanup; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
porcelain_count() { gitv status --porcelain | wc -l | tr -d ' '; }
rawhash() { sha256sum "${VAULT}/$1" | cut -d' ' -f1; }
chash() { printf 'sha256:%s\n' "$(rawhash "$1")"; }
# rc_of <信封文件>：取报告体里 reconcile 那一块的紧凑原文。
rc_of() { jq -cr '.data.report.reconcile' "$1"; }
# keys_of <信封文件>：取报告体顶层键序（逐位，不排序 —— 键序也是合同的一部分）。
keys_of() { jq -cr '.data.report | keys_unsorted | join(",")' "$1"; }
fact() { jq -cr "$1" "${FACTS}"; }
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读 .data.report"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库与语料
step "eg init + 一份原文 / 一篇材料笔记 / 一张卡（都由 eg 口径入库，工作区干净）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/${NDIR_REL}" "${VAULT}/sources"
cat >"${VAULT}/sources/${SRC}.md" <<SRC_EOF
---
id: ${SRC}
url: https://example.com/attention
title: 注意力机制原文
saved_at: '2026-12-01T09:00:00+08:00'
---

# 注意力机制原文

正文占位（收录后不因加工改写）。
SRC_EOF
cat >"${VAULT}/${NOTE_REL}" <<NOTE_EOF
---
id: ${NOTE}
source: ${SRC}
created_at: '2026-12-01'
updated_at: '2026-12-01T10:00:00+08:00'
---

# 注意力机制材料笔记

## 材料提炼

提炼占位。

## Agent 分析

分析占位。

## 用户补充

## 存疑与待验证

## 产出知识卡

- ${CARD}
NOTE_EOF
cat >"${VAULT}/${CARD_REL}" <<CARD_EOF
---
id: ${CARD}
title: 注意力机制
status: active
created_at: '2026-12-01'
updated_at: '2026-12-01T10:00:00+08:00'
tags:
  - llm
sources:
  - source: ${SRC}
    note: ${NOTE}
    rel: support
    reason: 该结论由这份材料支持
---

# 注意力机制

## 知识内容

正文占位。

## 解释与依据

依据占位。

## 条件与边界

边界占位。

## 用户补充

## 理解自检

- 自检问题占位？
CARD_EOF
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m4 报告 reconcile 字段语料"
[ "$(porcelain_count)" = "0" ] || die "前置条件：工作区必须干净"
ok "库就绪：1 原文 / 1 笔记 / 1 卡，工作区干净"

# ------------------------------------------- 2. 非对账写命令 ①：键在、值为空
step "非对账写命令 ① eg apply --json：reconcile 键在且三键值恰 false / null / []"
cat >"${WORK}/plan.json" <<JSON
{
  "plan_version": 1, "verb": "process", "domain": "ai-infra",
  "reason": "追加一条依据，用于观察非对账路径的报告 reconcile 形态",
  "requirement_ids": ["EG-EDIT-05"],
  "base": { "${CARD_REL}": "$(chash "${CARD_REL}")" },
  "ops": [
    { "op": "append_card", "card": "${CARD}",
      "sections": { "解释与依据": "- 非对账路径追加的一条依据。\n" } }
  ]
}
JSON
[ "$(eg_code apply --plan "${WORK}/plan.json" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/out.txt"; die "eg apply 未退 0"; }
cp "${WORK}/out.txt" "${WORK}/apply.json"
jq -e '.data.report | has("reconcile")' "${WORK}/apply.json" >/dev/null ||
  { cat "${WORK}/apply.json"; die "报告体缺 reconcile 键：S3 阶段键必须「键在」，不得整键缺席"; }
[ "$(rc_of "${WORK}/apply.json")" = "${PLACEHOLDER}" ] ||
  { rc_of "${WORK}/apply.json"; die "非对账路径的 reconcile 必须逐字为 ${PLACEHOLDER}"; }
[ "$(jq -cr '.data.report.reconcile | keys_unsorted | join(",")' "${WORK}/apply.json")" = "ran,commit,findings" ] ||
  die "reconcile 键序必须逐字为 ran,commit,findings（合同 §11）"
[ "$(jq -cr '.data.report.reconcile | length' "${WORK}/apply.json")" = "3" ] ||
  die "reconcile 必须恰 3 键（不增不减）"
[ "$(jq -cr '.data.report.reconcile.findings | type' "${WORK}/apply.json")" = "array" ] ||
  die "findings 必须是数组（空时为 []，永不为 null）"
[ "$(jq -cr '.data.report.reconcile.commit' "${WORK}/apply.json")" = "null" ] ||
  die "非对账路径的 commit 键必须为 null（不是空串）"
KEYS_APPLY="$(keys_of "${WORK}/apply.json")"
ok "eg apply：reconcile 三键 = ${PLACEHOLDER}"

# ------------------------------------------- 3. 非对账写命令 ② + 复现路径
step "非对账写命令 ② eg mark-reviewed 与复现路径 eg report --last：占位形态逐字相同"
[ "$(eg_code mark-reviewed --target "${CARD}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg mark-reviewed 未退 0"; }
cp "${WORK}/out.txt" "${WORK}/marked.json"
[ "$(rc_of "${WORK}/marked.json")" = "${PLACEHOLDER}" ] ||
  { rc_of "${WORK}/marked.json"; die "第二条非对账写命令的占位形态不一致"; }
[ "$(keys_of "${WORK}/marked.json")" = "${KEYS_APPLY}" ] ||
  die "两条写命令的报告体顶层键序不一致（键集合不得随命令变化）"
[ "$(eg_code report --last --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg report --last 未退 0"; }
cp "${WORK}/out.txt" "${WORK}/last.json"
[ "$(rc_of "${WORK}/last.json")" = "${PLACEHOLDER}" ] ||
  { rc_of "${WORK}/last.json"; die "复现路径的占位形态不一致（apply 与 report --last 必须逐字可比）"; }
[ "$(keys_of "${WORK}/last.json")" = "${KEYS_APPLY}" ] || die "复现路径的顶层键序不一致"
ok "三条非对账路径（apply / mark-reviewed / report --last）占位形态与键序逐字一致"

# ------------------------------------------- 4. 带对账的路径：ran 为 true
step "驱动壳跑带对账的路径：ran=true、commit 为 40 位 sha、findings 恰 2 条且元素恰四键"
rm -f "${FACTS}"
( cd "$(eg_staged_root)" &&
  EG_M4_RPT_ENVELOPE="${WORK}/apply.json" EG_M4_RPT_OUT="${FACTS}" EG_M4_RPT_SHA="${SHA40}" \
  go test ./test/e2e/ -run TestM4ReportReconcileHarness -count=1 -v </dev/null ) \
  >"${WORK}/drive.log" 2>&1 || { tail -30 "${WORK}/drive.log"; die "驱动壳未全绿"; }
grep -Fq -- "--- PASS: TestM4ReportReconcileHarness" "${WORK}/drive.log" ||
  { tail -30 "${WORK}/drive.log"; die "驱动壳未 PASS（可能被 skip）"; }
[ -s "${FACTS}" ] || die "驱动壳未落事实文件"
[ "$(fact '.ran_reconciled')" = "true" ] || { cat "${FACTS}"; die "带对账路径的 ran 必须为 true"; }
[ "$(fact '.ran_real')" = "true" ] ||
  { cat "${FACTS}"; die "真实命令输出的 ran 应为 false（本字段回报的是「是否命中 false」）"; }
[ "$(fact '.commit_reconcile')" = "${SHA40}" ] ||
  { cat "${FACTS}"; die "带对账路径的 commit 键必须是那条真实 sha"; }
[ "$(fact '.findings_count')" = "2" ] || { cat "${FACTS}"; die "findings 应恰 2 条（数量守恒）"; }
[ "$(fact '.finding_keys | join(",")')" = "check,severity,targets,detail" ] ||
  { cat "${FACTS}"; die "findings 元素必须恰四键且键序逐字同合同 §2"; }
[ "$(fact '.mirror_keys | join(",")')" = "$(fact '.source_keys | join(",")')" ] ||
  { cat "${FACTS}"; die "报告镜像键集合与对账包真源不等（等号锁在 internal/cli 侧）"; }
[ "$(fact '.stable_twice')" = "true" ] || die "同一报告两次序列化必须逐字节相同"
ok "带对账路径：ran=true、commit=${SHA40:0:8}…、findings 2 条、四键逐字同名"

# ------------------------------------------- 5. 两条路径顶层键序完全一致
step "两条路径的报告体顶层键序完全一致（键集合不随路径变化）"
[ "$(fact '.key_set_equal')" = "true" ] ||
  { cat "${FACTS}"; die "两条路径的顶层键序不同：下游脚本不得因路径分支处理键集合"; }
[ "$(fact '.real_keys | join(",")')" = "${KEYS_APPLY}" ] ||
  { cat "${FACTS}"; die "驱动壳回读的键序与 jq 读到的不一致"; }
[ "$(fact '.real_reconcile')" = "${PLACEHOLDER}" ] || { cat "${FACTS}"; die "占位形态回读不一致"; }
[ "$(fact '.placeholder')" = "${PLACEHOLDER}" ] ||
  { cat "${FACTS}"; die "report 包的占位常量与本脚本的期望串不一致（唯一真源必须同步）"; }
ok "顶层键序两路径逐位相等：${KEYS_APPLY}"

# ------------------------------------------- 6. 冻结面：S1 必填 11 项 / affected / kind
step "冻结面反证：S1 必填恰 11 项、git.commit 仍嵌套、affected 恒 null、kind 未新增第三种"
[ "$(fact '.s1_required')" = "11" ] || { cat "${FACTS}"; die "S1 必填必须恒 11 项（§4.6）"; }
[ "$(fact '.affected_raw')" = "null" ] ||
  { cat "${FACTS}"; die "affected 必须仍是值为 null 的占位键（M4 不给字段级定义，A-36）"; }
[ "$(jq -cr '.data.report | keys_unsorted[0:11] | join(",")' "${WORK}/apply.json")" = \
  "source,note,cards,relations,open_questions,links,git,skipped,default_domain_fallback,high_impact,warnings" ] ||
  die "报告体前 11 个键必须逐字是 §4.6 的 S1 必填清单（S3 阶段键排在其后）"
jq -e '.data.report.git | has("commit")' "${WORK}/apply.json" >/dev/null ||
  die "git.commit 必须仍是嵌套键（§4.6）"
jq -e '.data.report | has("commit") | not' "${WORK}/apply.json" >/dev/null ||
  die "顶层不得出现自创的 commit 键"
jq -e '.data.report | has("affected")' "${WORK}/apply.json" >/dev/null ||
  die "affected 占位键不得被删（阶段字段要求「键在、值不造假」）"
jq -e '[.data.report.reconcile | keys_unsorted[]] - ["ran","commit","findings"] | length == 0' \
  "${WORK}/apply.json" >/dev/null || die "reconcile 出现第四键"
[ "$(jq -cr '[.data.report.skipped[]?.kind] | unique | length' "${WORK}/apply.json")" -le 2 ] ||
  die "skipped[].kind 超过两值（M4 不新增第三种 kind）"
# kind 的**枚举真源**只有一份：internal/store 的 SkipReason（合同 §8 的封闭两值）。
# 这里直接数真源里的非空取值，而不是满仓库 grep 裸词 —— 后者会把 high_impact 的
# `Kind:` 一并数进来（那是另一个结构体的另一个封闭表，与 skipped 无关）。
KINDS="$( { grep -oE 'SkipReason = "[a-z_]+"' "${REPO_ROOT}/internal/store/receipt.go" || true; } |
  sort -u | wc -l | tr -d ' ')"
[ "${KINDS}" = "2" ] || { grep -oE 'SkipReason = "[a-z_]+"' "${REPO_ROOT}/internal/store/receipt.go" || true;
  die "skipped kind 的枚举真源必须恰两值（file_changed / user_block_unsafe），实得 ${KINDS}"; }
for k in file_changed user_block_unsafe; do
  grep -q "SkipReason = \"${k}\"" "${REPO_ROOT}/internal/store/receipt.go" ||
    die "kind 真源缺既有取值 ${k}（M4 不改既有两值）"
done
ok "S1 必填 11 项一字未动；affected 恒 null；kind 恰两值"

# ------------------------------------------- 7. 源码级边界反证
step "源码级边界：report 零依赖对账包 / 三键计数恰 3 / 四处投影键恒 1 / 命令仍零注册"
# 依赖方向按**导入形态 + 选择器形态**双格判定（比裸词 grep 更贴合同、且不误伤说明行）：
# reconcile.go 的文件头必须逐字讲清「为什么不能引对账包」，因此包路径会作为**文字**出现；
# 真正会成环的是 import 那一行与 `reconcile.X` 这种跨包选择器，两者恒 0。
cnt0 'report 包 import 对账包' -rn '"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"' "${REPO_ROOT}/internal/report/"
cnt0 'report 包使用对账包导出符号' -rnE 'reconcile\.[A-Z]' \
  "${REPO_ROOT}/internal/report/reconcile.go" "${REPO_ROOT}/internal/report/report.go"
[ "$(grep -cE 'json:"(ran|commit|findings)"' "${REPO_ROOT}/internal/report/reconcile.go")" = "3" ] ||
  die "reconcile.go 的三键计数必须恰 3"
for key in 'json:"ran"' 'json:"findings"'; do
  [ "$(grep -c "${key}" "${REPO_ROOT}/internal/report/reconcile.go")" = "1" ] ||
    die "reconcile.go 里 ${key} 必须恰 1 处"
done
[ "$(grep -c 'json:"commit"' "${REPO_ROOT}/internal/report/reconcile.go")" = "1" ] ||
  die "reconcile.go 里 commit 键必须恰 1 处"
[ "$(grep -c 'Commit \*string `json:"commit"`' "${REPO_ROOT}/internal/report/report.go")" = "1" ] ||
  die "report.go 的 git.commit 投影键必须恰 1 处且一字不动（§4.6）"
[ "$(grep -c 'json:"git_commit"' "${REPO_ROOT}/internal/report/report.go")" -ge "1" ] ||
  die "proposals[].execution.git_commit 投影键不得被删（可见性合同 §1.3 / §1.4）"
[ "$(grep -c 'json:"affected"' "${REPO_ROOT}/internal/report/report.go")" = "1" ] ||
  die "affected 占位挂点必须恰 1 处（不增不删）"
# 只看**非测试源**：report_test.go 里那三个词是反证用例自己的词表（它就是来判红这件事的）。
cnt0 'affected 具名类型（字段级定义）' -rnE 'type Affected|AffectedEntry|AffectedItem' \
  "${REPO_ROOT}/internal/report/report.go" "${REPO_ROOT}/internal/report/reconcile.go" \
  "${REPO_ROOT}/internal/report/support_check.go"
# 2026-09-07 随 M4 · T-…-058 阶段 3 按实测重钉判据**形态**（本体一格未放宽，反而多一格）：
# T-…-058 按技术方案 §7.1 / 对账合同 §12 **合法注册** S3 命令 `eg reconcile`（命令数 18 → 19），
# 注册形态 `Name: "reconcile"` 在 `internal/cli/reconcile.go` 落**恰 1** 次，故旧的「全包恒 0」
# 按实测重钉为两格：① 全 `internal/cli/` 恰 1（= M3 期 0 + M4 T-…-058 新增 1，加法等式，
# M3 侧加数 0 逐字保留）；② 唯一落点必须是 `internal/cli/reconcile.go` —— 本 task 的实现文件
# 与其它任何文件内恒 0。「本 task 不注册命令」这一本体由第 ② 格逐字钉死，比旧的一格更严。
T058_REG_ALL="$( { grep -rnE 'Name:[[:space:]]*"reconcile"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' || true; } | wc -l | tr -d ' ')"
T058_REG_OUT="$( { grep -rnE 'Name:[[:space:]]*"reconcile"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' | grep -v '/reconcile\.go:' || true; } | wc -l | tr -d ' ')"
[ "${T058_REG_ALL}" = "1" ] ||
  die "reconcile 命令注册形态应恰 1（M3 期 0 + M4 T-…-058 新增 1），实得 ${T058_REG_ALL}"
[ "${T058_REG_OUT}" = "0" ] ||
  die "reconcile 命令注册形态的唯一落点必须是 internal/cli/reconcile.go，别处实得 ${T058_REG_OUT}"
# 2026-09-07 随 M4 · T-…-059 阶段 3 按实测重钉判据**形态**（本体一格未放宽，反而多一格）：
# T-…-059 按技术方案 §7.1 / 对账合同 §13 **合法注册**只读结构体检命令 `eg check`
# （命令数 19 → 20，M4 收口值），注册形态 `Name: "check"` 在 `internal/cli/check.go` 落**恰 1** 次，
# 故旧的「全包恒 0」按实测重钉为两格：① 全 `internal/cli/` 恰 1（= M3 期 0 + M4 T-…-059 新增 1，
# 加法等式，M3 侧加数 0 逐字保留）；② 唯一落点必须是 `internal/cli/check.go` —— 本 task 的实现文件
# 与其它任何文件内恒 0。「本 task 不注册命令」这一本体由第 ② 格逐字钉死，比旧的一格更严。
T059_REG_ALL="$( { grep -rnE 'Name:[[:space:]]*"check"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' || true; } | wc -l | tr -d ' ')"
T059_REG_OUT="$( { grep -rnE 'Name:[[:space:]]*"check"' "${REPO_ROOT}/internal/cli/" \
  --include='*.go' | grep -v '/check\.go:' || true; } | wc -l | tr -d ' ')"
[ "${T059_REG_ALL}" = "1" ] ||
  die "check 命令注册形态应恰 1（M3 期 0 + M4 T-…-059 新增 1），实得 ${T059_REG_ALL}"
[ "${T059_REG_OUT}" = "0" ] ||
  die "check 命令注册形态的唯一落点必须是 internal/cli/check.go，别处实得 ${T059_REG_OUT}"
cnt0 '报告层出现落盘或子进程' -rnE 'os\.WriteFile|os\.Create|exec\.Command' \
  "${REPO_ROOT}/internal/report/reconcile.go"
ok "依赖方向单向、三键计数恰 3、两处投影键与 affected 占位恒 1、命令零注册"

printf '\n=== report_reconcile.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
