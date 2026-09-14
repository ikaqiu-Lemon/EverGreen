#!/usr/bin/env bash
# R1 外部编辑纳管的端到端脚本（T-evergreen.s1_main_flow-158614-050）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §4（R1 全文：触发 / 检查 /
# 判定 / 动作 / 「零改动即零 commit」/ 恰一次 commit 的理由）、§1.3 写口归属表第 1 行、
# §11.2（`reconcile.commit` 是**单值**字段）；`docs/specs/2026-11-15-m4-prestart-adjudication.md`
# 的 A-30（commit verb = `reconcile`）与 A-35（R1 / R2 / R6 的写入合并为**恰一次** commit）；
# `milestones/M-004-m4.md` 完成判据 3 与风险 R-15（`git add -A` 口径继承 R-13 不得偷改）。
#
# 四段断言（task deliverable 逐字要求）：
#   ① 人为造 2 个外部编辑 → 只读检查恰 1 条 `git_uncommitted`（W13 / warning，targets 两条
#      且去重升序），且**检查阶段零写入零提交**（git log 条数与 status 逐字不变）；
#   ② 跑纳管 → `git log --oneline | wc -l` 相对纳管前**恰 +1**、`git status --porcelain` 为空、
#      提交主题的 verb 恰 `reconcile`、正文带 Reason: / Requirement: 行；
#   ③ 干净工作区再跑一次 → commit 数**不变**、`git_uncommitted` 条数为 `0`（零改动即零 commit）；
#   ④ dry-run 造脏再跑 → 零提交零写入（工作区状态逐字保持脏）。
# 另加源码级边界反证（检查侧三条零 / 写口侧不碰知识数据写盘层 / `add -A` 口径未改窄）。
#
# 为什么用 `go test` 驱动而不是 `eg reconcile`：
#   `eg reconcile` 命令本体、信封与退出码属 T-…-058，本 task 明确不注册命令，只交付
#   「R1 检查器 + 恰一次纳管 commit 的写口」两项能力。脚本因此在真实临时 vault 上用
#   `test/e2e/m4_r1_takeover_test.go` 这一层驱动壳调这两项能力（同 `m3_superseded.sh`
#   的先例），事实一律回读 **git 自己**与驱动壳落盘的 JSON，不看实现自报。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/reconcile-takeover/takeover.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-r1.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

EDIT_TRACKED='unprocessed.md'                              # 已跟踪文件的外部修改
EDIT_UNTRACKED='domains/ai-infra/knowledge/k-20261120-manual.md'  # 用户手放的未跟踪文件

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
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain; }
porcelain_count() { porcelain | wc -l | tr -d ' '; }
tree_sum() { find "${VAULT}" -path "${VAULT}/.git" -prune -o -type f -print0 |
  xargs -0 sha256sum | sort; }

# drive <mode> [ours]：在真实 vault 上跑驱动壳，事实落 ${FACTS}。
drive() {
  local mode="$1" ours="${2:-}"
  rm -f "${FACTS}"
  ( cd "$(eg_staged_root)" &&
    EG_M4_R1_VAULT="${VAULT}" EG_M4_R1_MODE="${mode}" EG_M4_R1_OUT="${FACTS}" \
    EG_M4_R1_DOMAIN='ai-infra' EG_M4_R1_OURS="${ours}" \
    go test ./test/e2e/ -run TestM4R1TakeoverHarness -count=1 -v </dev/null ) \
    >"${WORK}/drive-${mode}.log" 2>&1 ||
    { tail -30 "${WORK}/drive-${mode}.log"; die "驱动壳（${mode}）未全绿"; }
  grep -Fq -- "--- PASS: TestM4R1TakeoverHarness" "${WORK}/drive-${mode}.log" ||
    { tail -30 "${WORK}/drive-${mode}.log"; die "驱动壳（${mode}）未 PASS（可能被 skip）"; }
  [ -s "${FACTS}" ] || die "驱动壳（${mode}）未落事实文件"
}

# fact_str <键>：取 JSON 里的字符串值（驱动壳的输出是紧凑 JSON，键名固定）。
fact_str() { sed -E "s/.*\"$1\":\"([^\"]*)\".*/\1/" "${FACTS}"; }
# fact_num <键>：取 JSON 里的数字值。
fact_num() { sed -E "s/.*\"$1\":([0-9]+).*/\1/" "${FACTS}"; }
# fact_count <片段>：数 JSON 里某片段出现次数。
fact_count() { grep -o -- "$1" "${FACTS}" | wc -l | tr -d ' '; }
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库
step "eg init 建库（工作区应干净，作为「零改动」的基线）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "eg init 后工作区应干净"; }
BASE_COMMITS="$(commits)"
[ "${BASE_COMMITS}" -ge 1 ] || die "eg init 应留下至少一条 commit"
ok "vault 就绪：commit 数 ${BASE_COMMITS}，工作区干净"

# ---------------------------------------------------------------- 2. 干净工作区先跑一次：零改动即零 commit
step "干净工作区跑纳管：零 commit、零写入、git_uncommitted 恰 0（合同 §4「零改动即零 commit」）"
CLEAN_SUM="$(tree_sum)"
drive takeover
[ "$(fact_num git_uncommitted)" = "0" ] || { cat "${FACTS}"; die "干净工作区不得报 git_uncommitted"; }
[ "$(fact_str commit)" = "" ] || { cat "${FACTS}"; die "零改动必须零 commit（不产生空 commit）"; }
[ "$(fact_num commits)" = "0" ] || { cat "${FACTS}"; die "零改动时写口提交条数必须为 0"; }
grep -Fq '"ran":true' "${FACTS}" || { cat "${FACTS}"; die "零改动分支的 reconcile.ran 仍应为 true" ; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "零改动却产生了 commit"
[ "${CLEAN_SUM}" = "$(tree_sum)" ] || die "零改动纳管写盘了（应零写入）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "零改动纳管后工作区应仍干净"; }
ok "零改动 → 零 commit、零写入、ran=true、commit 字段为空（§11.2 单值可表达 null）"

# ---------------------------------------------------------------- 3. 造 2 个外部编辑
step "人为造 2 个外部编辑（改 1 个已跟踪文件 + 手放 1 个未跟踪文件），只写盘不提交"
printf '# 未处理清单\n\n- 用户在编辑器里手写的一行（外部编辑 1）\n' >"${VAULT}/${EDIT_TRACKED}"
mkdir -p "$(dirname "${VAULT}/${EDIT_UNTRACKED}")"
cat >"${VAULT}/${EDIT_UNTRACKED}" <<'CARD'
---
id: k-20261120-manual
title: 用户手写的卡（外部编辑 2）
domain: ai-infra
---

## 结论

用户直接在编辑器里放进 vault 的内容，纳管不改写它一个字节。
CARD
[ "$(porcelain_count)" = "2" ] ||
  { porcelain; die "外部编辑后 git status 应恰 2 条，实得 $(porcelain_count)"; }
DIRTY_BEFORE="$(porcelain)"
DIRTY_SUM="$(tree_sum)"
ok "工作区恰 2 处未提交改动：${EDIT_TRACKED} + ${EDIT_UNTRACKED}"

# ---------------------------------------------------------------- 4. 只读检查：恰 1 条 finding，零写入零提交
step "只读检查（probe）：恰 1 条 git_uncommitted（W13 / warning），检查阶段零写入零提交"
drive probe
[ "$(fact_num git_uncommitted)" = "1" ] ||
  { cat "${FACTS}"; die "外部编辑应产出恰 1 条 git_uncommitted，实得 $(fact_num git_uncommitted)"; }
[ "$(fact_count '"check":"git_uncommitted"')" = "1" ] || { cat "${FACTS}"; die "finding 条数不为 1"; }
[ "$(fact_count '"severity":"warning"')" = "1" ] || { cat "${FACTS}"; die "R1 的 severity 应为 warning"; }
[ "$(fact_count '"W13"')" = "1" ] || { cat "${FACTS}"; die "R1 的诊断码应为 W13（合同 §3 单射）"; }
grep -Fq "\"targets\":[\"${EDIT_UNTRACKED}\",\"${EDIT_TRACKED}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "targets 应为两条 vault 相对路径且字典序升序"; }
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" ||
  { cat "${FACTS}"; die "R1 不产 RepairSpec（Git 纳管不是知识数据修改）"; }
[ "$(fact_str commit)" = "" ] || { cat "${FACTS}"; die "只读检查不得产生 commit"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "只读检查改变了 commit 数"
[ "${DIRTY_BEFORE}" = "$(porcelain)" ] || die "只读检查改变了工作区状态"
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
ok "检查侧：1 条 W13 / warning、targets 去重升序、零 RepairSpec、零写入零提交"

# ---------------------------------------------------------------- 5. dry-run：零提交零写入
step "dry-run 纳管：清单照报，零 commit、零写入，工作区仍脏"
drive dryrun
[ "$(fact_str commit)" = "" ] || { cat "${FACTS}"; die "dry-run 必须零 commit"; }
[ "$(fact_num commits)" = "0" ] || { cat "${FACTS}"; die "dry-run 的提交条数必须为 0"; }
grep -Fq "${EDIT_TRACKED}" "${FACTS}" || { cat "${FACTS}"; die "dry-run 必须如实回报待纳管清单"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "dry-run 产生了 commit"
[ "${DIRTY_BEFORE}" = "$(porcelain)" ] || die "dry-run 改变了工作区状态"
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "dry-run 写盘了"
ok "dry-run：只报不写（commit 空、工作区状态逐字不变）"

# ---------------------------------------------------------------- 6. 纳管：恰 +1 条 commit
step "纳管：git log 恰 +1、status 为空、verb = reconcile（A-30）、正文带 Reason / Requirement"
BEFORE_LOG="$(commits)"
drive takeover "${EDIT_UNTRACKED}"
SHA="$(fact_str commit)"
[ -n "${SHA}" ] || { cat "${FACTS}"; die "有改动时必须产生恰一条 commit"; }
[ "$(fact_num commits)" = "1" ] || { cat "${FACTS}"; die "写口提交条数应恰 1（A-35）"; }
AFTER_LOG="$(commits)"
[ "${AFTER_LOG}" = "$((BEFORE_LOG + 1))" ] ||
  die "git log 条数 ${BEFORE_LOG} → ${AFTER_LOG}，应恰 +1"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "纳管后 git status --porcelain 应为空"; }
SUBJECT="$(gitv log -1 --pretty=%s)"
case "${SUBJECT}" in
  "reconcile(ai-infra): "*) ;;
  *) die "提交主题 = ${SUBJECT}，应以 reconcile(<domain>): 开头（A-30 / KnownVerbs 恒 8）" ;;
esac
BODY="$(gitv log -1 --pretty=%b)"
printf '%s' "${BODY}" | grep -Fq 'Reason:' || die "提交正文缺 Reason: 行"
printf '%s' "${BODY}" | grep -Fq 'Requirement:' || die "提交正文缺 Requirement: 行"
# 纳管只把既有事实记进历史：两个文件的内容逐字不变（不改写用户改好的文件）。
gitv show --stat --oneline HEAD | grep -Fq "${EDIT_TRACKED}" || die "本次 commit 未含 ${EDIT_TRACKED}"
gitv show --stat --oneline HEAD | grep -Fq "${EDIT_UNTRACKED}" || die "本次 commit 未含 ${EDIT_UNTRACKED}"
grep -Fq '用户直接在编辑器里放进 vault 的内容' "${VAULT}/${EDIT_UNTRACKED}" ||
  die "纳管改写了用户的文件内容（R1 只做 Git 纳管，不改一个字节）"
grep -Fq '"existing":' "${FACTS}" || { cat "${FACTS}"; die "回执应如实区分既有改动"; }
ok "恰 +1 条 commit（${SHA:0:8}）、工作区干净、verb=reconcile、两处外部编辑同进一条 commit"

# ---------------------------------------------------------------- 7. 再跑一次：commit 数不变
step "干净工作区再跑一次：commit 数不变、git_uncommitted 恰 0（幂等）"
AFTER_SUM="$(tree_sum)"
drive takeover
[ "$(fact_num git_uncommitted)" = "0" ] ||
  { cat "${FACTS}"; die "纳管后应无 git_uncommitted，实得 $(fact_num git_uncommitted)"; }
[ "$(fact_str commit)" = "" ] || { cat "${FACTS}"; die "第二次纳管不得产生 commit"; }
[ "$(fact_num commits)" = "0" ] || { cat "${FACTS}"; die "第二次纳管的提交条数必须为 0"; }
[ "$(commits)" = "${AFTER_LOG}" ] || die "第二次纳管改变了 commit 数（应幂等）"
[ "${AFTER_SUM}" = "$(tree_sum)" ] || die "第二次纳管写盘了"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "第二次纳管后工作区应仍干净"; }
ok "幂等：commit 数不变（${AFTER_LOG}）、findings 归零、零写入"

# ---------------------------------------------------------------- 8. 源码级边界反证
step "边界反证：检查侧三条零 / 写口侧不碰知识数据写盘层 / add -A 口径未改窄"
cnt0 '检查侧写盘或提交' -rnE 'os\.WriteFile|os\.Create|Commit\(' "${REPO_ROOT}/internal/reconcile/r1_git.go"
cnt0 '检查侧起子进程' -rnE 'os/exec|exec\.Command' "${REPO_ROOT}/internal/reconcile/r1_git.go"
cnt0 '写口 import 知识数据写盘层' -rn 'internal/store' "${REPO_ROOT}/internal/cli/reconcile_commit.go"
cnt0 '写口出现选择性暂存' -rnE 'add --|add -u|add -p' "${REPO_ROOT}/internal/cli/reconcile_commit.go"
[ "$(grep -cE 'add -A|AddAll' "${REPO_ROOT}/internal/cli/reconcile_commit.go")" -ge 1 ] ||
  die "写口必须逐字登记 add -A 口径（R-15）"
[ "$(grep -c 'internal/git' "${REPO_ROOT}/internal/cli/reconcile_commit.go")" -ge 1 ] ||
  die "写口必须直连 internal/git"
# `eg reconcile` 命令本体属 T-…-058：本 task 不注册命令。裸词 grep 会命中 M1 起就有的
# `config set` 的 commit verb 说明行（实测 2 处，全在 commands.go 的说明里），故按
# **注册形态** `Name: "reconcile"` 判定；用法行 `eg reconcile` 同样零命中（消歧行除外）。
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
# 2026-09-06 随 M4 · T-…-056 **按实测重钉判据形态**（本体一格不放宽）：R7 的视图提示落在
# `internal/cli/reconcile_render.go`（合同 §10 要求的唯一渲染落点，只进 stdout），该文件的
# 两处说明行逐字写着「命令本体属 T-…-058 / T-…-059」——它们是**边界声明**，不是用法行。
# 故用法行 grep 排除该文件，并**新增**一格加严：该文件内零命令注册形态（Name / Usage / Run /
# Flags / Command{ 全零），命令注册形态 `Name: "reconcile"` 的全包恒 0 判据逐字未动。
RENDER_ONLY="reconcile_render.go"
# 2026-09-07（T-…-063 收口，K-063-02）：命令本体落地后，T-…-059 的 `eg check` 帮助文案
# （check.go：「修复走 eg reconcile」「未判面…请跑 eg reconcile」）与命令注册表注释
# （commands.go）会**合法交叉引用** `eg reconcile`——它们与 reconcile_render.go 同属**边界
# 声明 / 交叉引用**，不是 reconcile 命令本体的用法行。故一并豁免。本 task 的真实守护目标
# ——写口 `reconcile_commit.go` 不得冒充命令本体——保持逐字生效：下方额外一格钉死写口零用法行。
XREF_CHECK="internal/cli/check.go"
XREF_CMDS="internal/cli/commands.go"
# 2026-09-07 随 M4 · T-…-058 阶段 3 按实测重钉（同上，本体一格未放宽）：命令本体落地后
# `eg reconcile` 用法行必然非 0（实测 6 行，**全部**在 `internal/cli/reconcile.go` 内：
# 文件头、注册说明、`Usage:` 串与两处 report 说明）。故把「用法行恒 0」重钉为
# 「落点**恰 1 个文件**且集合逐字 == {`internal/cli/reconcile.go`}」——
# 本 task 的写口 `reconcile_commit.go` 若写用法行，落点集合立刻 ≠ 单元素，判红。
T058_CMD_SET="$( { grep -rn 'eg reconcile' "${REPO_ROOT}/internal/cli/" --include='*.go' |
  grep -v _test.go | grep -v '≠' | grep -v "${RENDER_ONLY}" |
  grep -v "${XREF_CHECK}" | grep -v "${XREF_CMDS}" || true; } |
  sed "s#${REPO_ROOT}/##" | cut -d: -f1 | sort -u | tr '\n' ' ')"
[ "${T058_CMD_SET}" = "internal/cli/reconcile.go " ] ||
  die "eg reconcile 用法行的落点集合应恰 {internal/cli/reconcile.go}，实得 {${T058_CMD_SET}}"
# 写口守护逐字生效：上面的**集合恰等**已钉死——若写口 reconcile_commit.go 出现任何非消歧
# （无 `≠`）的 `eg reconcile` 用法行，集合立刻 ≠ 单元素判红；其现有含 `≠` 的边界声明注释
# （「本文件 ≠ eg reconcile 命令本体」）被 `grep -v '≠'` 合法排除，不算用法行。
cnt0 '渲染层出现命令注册形态' -nE '(Name|Usage|Run|Flags):|Command\{' \
  "${REPO_ROOT}/internal/cli/${RENDER_ONLY}"
ok "检查侧零写盘 / 零 commit / 零 os-exec；写口只连 internal/git；add -A 未改窄；命令零注册"

printf '\n=== takeover.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
