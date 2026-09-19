#!/usr/bin/env bash
# `eg check` 命令的端到端脚本（T-evergreen.s1_main_flow-158614-059 · 阶段 3）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §13（`eg check [--json]`：
# **只读**、只跑 R3 / R4 两组结构检查（恰 8 个 `check`，A-62 起含 opinion_unsupported_validated / W29）、**恒 0 次 commit**、
# 三档退出码 `0`（无 error 级 finding，含只有 warning）/ `1`（参数非法）/ `2`（有 error 级
# finding，**零写入**）、与 `eg reconcile --dry-run` 的**真子集**关系、五个 `check` **永不产出**）、
# **§12**（父命令口径，用于真子集对照）、**§0.1 第 3 条**（对账**不作为任何写命令的前置**）；
# `2026-11-12-m4-visibility-and-execution-contract.md` **§3.4**（`eg check` 属
# `--include-deprecated` 的**不作用面**，传入即退 `1`）+ **§4.4 G7**；
# `milestones/M-004-m4.md` 完成判据 12 与「强校验属 S5」这条明确不做。
#
# 断言组（task deliverable 逐字要求；脚本共 10 步 = 1 步构建 + 9 组断言，编号 02–10）：
#   ① 干净 vault 跑 `eg check` → 退 **0**、`git log` 条数 +0、`git status --porcelain` 逐字不变；
#      信封恒五键且键序固定、`data` 首键是 `check`、`scope[]` 恰 8 值、`excluded[]` 恰 5 值；
#   ② 造**重复 ID**（E11，error 级）→ 退 **2**，且 `git log` 条数 **不变**
#      （与 `eg reconcile` 的关键差：后者会先完成 R1 纳管 commit 再退 2）、脏文件仍在工作区；
#   ③ 造**关系异常**（外部编辑写入 `relations[]`：target 查无此对象 + 同一条边写两遍）
#      → 退 **2**，且 findings 里逐字出现 `relation_target_missing` 与 `relation_duplicate`；
#   ④ findings 的 `check` 集合 ⊆ R3 / R4 八值，且**五个被排除值**在 findings 里恒 0 命中；
#      同一份库上 `eg reconcile --dry-run` **确实**产出被排除值（非空对照，杜绝空判）；
#   ⑤ **真子集**：`eg check` 的 finding 条数 < `eg reconcile --dry-run` 的条数，
#      且前者的每个 `check` 值都在后者的集合里（`comm -23` 恒空）；
#   ⑥ 参数面：`--include-deprecated` / `--target` / `--domain` / `--fix` / 位置参数**各退 1**，
#      且 commit 数与 `porcelain` 逐字不变（零写入）；
#   ⑦ 只读到底：`eg check` 跑前后 `eg report --last` 输出**逐字节**相同（不改写最近一次报告），
#      且 `eg check` 自己输出里的报告 `reconcile` 三键恒是占位 `{"ran":false,"commit":null,"findings":[]}`；
#   ⑧ **不作为写命令的前置**：真实跑写命令（`eg mark-reviewed` / `eg capture`）各 commit **+1**，
#      且它们的输出里**没有** `data.check` 这一格（`eg check` 没被挂到任何写命令前）；
#   ⑨ 越界反证（grep）：`runCheck` 在 `internal/cli/*.go` 里除 `check*.go` 外恒 0；
#      注册形态 `Name: "check"` 恰 1 且唯一落点是 `internal/cli/check.go`；
#      `check.go` 内计划层 / 提交面 / 直写 API / 五个被排除 `check` 字面量恒 0；
#      `--fix` / `--repair` / `--include-deprecated` 三个参数名在 `check.go` 内恒 0。
#
# 为什么本脚本直接跑 `eg check` 而不像 R1–R7 那些脚本那样用 `go test` 驱动壳：
# 那些 task 明确**不注册命令**，只能驱动能力层；本 task 交付的**就是命令本体**，
# 因此一切事实都从**真实 eg 二进制的退出码与 `--json` 输出**以及 **git 自己**回读。
#
# 约束：离线、零交互、可重复执行；**无 `jq` 依赖**（用紧凑 JSON 的 grep 正则 + awk 状态机取值，
# 口径逐字沿用 `m4_cmd_reconcile.sh`）；只依赖 bash / coreutils / awk / git / go；
# **一切写操作只发生在 `mktemp -d` 目录内**，仓库工作区一个字节都不碰；
# 任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/ops-diagnostics/cmd_check.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-cmd-check.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
CARD='k-20261201-attention'
CARD2='k-20261201-context'
NOTE='n-20261201-attention'
SRC='s-20261201-attention'
CARD_REL="${KDIR_REL}/${CARD}.md"
CARD2_REL="${KDIR_REL}/${CARD2}.md"
NOTE_REL="${NDIR_REL}/${NOTE}.md"
DUP_REL="${KDIR_REL}/${CARD}-dup.md"
MISSING='k-20991231-missing'

# 检查面（R3 / R4 恰 8 值，A-62 起含 opinion_unsupported_validated / W29）与被排除面（恰 5 值）：
# **独立**写死一份，与产品代码从 `reconcile.Specs()` 派生的那一份互为对照（任一侧漂移即红）。
SCOPE_KEYS='duplicate_id,dangling_ref,orphan,relation_target_missing,relation_prefix_invalid,relation_opposing_asymmetric,relation_duplicate,opinion_unsupported_validated'
EXCLUDED_KEYS='git_uncommitted,reviewed_at_missing,domain_moved,recap_stale,support_insufficient'
PLACEHOLDER='{"ran":false,"commit":null,"findings":[]}'
ENVELOPE_KEYS='ok,data,warnings,exit_code,status'
# `data.check` 的键序：产品侧这一格是 `map[string]interface{}`，Go 的 `encoding/json`
# 对 map **按键名升序**输出，因此实测键序恒为 `excluded,findings,scope`（确定性、可回归）。
# 这里按**实测事实**钉，而不是按人的书写顺序想象；合同 §13 只要求这三个键齐全、不要求顺序。
CHECK_KEYS='excluded,findings,scope'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
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
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# ─────────────────────────────── §JSON 工具（零 jq 依赖）───────────────────────────────
# 与 m4_cmd_reconcile.sh 同源：本仓 `--json` 是**紧凑单行** JSON，键序由 DataOrder /
# 结构体字段序钉死，因此用 awk 字符串状态机取值即可，既不引第三方依赖也不会在嵌套上失真。
JSON_KEYS_AWK='
{
  s = $0; n = length(s); depth = 0; instr = 0; esc = 0; pc = ""; grab = 0; key = ""; out = ""
  for (i = 1; i <= n; i++) {
    c = substr(s, i, 1)
    if (instr) {
      if (esc) { esc = 0; if (grab) key = key c; continue }
      if (c == "\\") { esc = 1; if (grab) key = key c; continue }
      if (c == "\"") { instr = 0; pc = "\""
        if (grab) { out = (out == "" ? key : out "," key); grab = 0 }
        continue }
      if (grab) key = key c
      continue
    }
    if (c == " " || c == "\t" || c == "\n" || c == "\r") continue
    if (c == "\"") { instr = 1; key = ""
      grab = (depth == DEPTH && st[depth] == "{" && (pc == "{" || pc == ",")) ? 1 : 0
      continue }
    if (c == "{" || c == "[") { depth++; st[depth] = c; pc = c; continue }
    if (c == "}" || c == "]") { delete st[depth]; depth--; pc = c; continue }
    pc = c
  }
  print out
}
'
JSON_SLICE_AWK='
{
  p = index($0, PFX); if (p == 0) { exit 1 }
  s = substr($0, p + length(PFX)); n = length(s)
  depth = 0; instr = 0; esc = 0; out = ""
  for (i = 1; i <= n; i++) {
    c = substr(s, i, 1); out = out c
    if (instr) {
      if (esc) { esc = 0; continue }
      if (c == "\\") { esc = 1; continue }
      if (c == "\"") instr = 0
      continue
    }
    if (c == "\"") { instr = 1; continue }
    if (c == "{") depth++
    else if (c == "}") { depth--; if (depth == 0) { print out; exit 0 } }
  }
  exit 1
}
'
# env_keys <信封文件>：信封顶层键序（应恒 `ok,data,warnings,exit_code,status`）。
env_keys() { awk -v DEPTH=1 "${JSON_KEYS_AWK}" "$1"; }
# chk_obj <信封文件>：`data.check` 那一整个对象的紧凑原文。
# 前缀写成 `"data":{"check":`，因此这个切片顺带断言了「data 的首键就是 check」。
chk_obj() { awk -v PFX='"data":{"check":' "${JSON_SLICE_AWK}" "$1"; }
# chk_keys <信封文件>：`data.check` 的键序（应恒 ${CHECK_KEYS}，即升序 excluded,findings,scope）。
chk_keys() { chk_obj "$1" >"${WORK}/chk.json" && awk -v DEPTH=1 "${JSON_KEYS_AWK}" "${WORK}/chk.json"; }
# JSON_ARR_AWK：从一行紧凑 JSON 里取出 `PFX` 之后那个**括号配平**的数组原文。
# 不能用 `sed 's/.*"findings":\(\[.*\]\)}$/\1/'`：`data.check` 的键是升序输出的，
# `findings` 落在**中间**（后面还有 `scope`），末尾锚定会取歪；detail 文本里也可能出现括号。
JSON_ARR_AWK='
{
  p = index($0, PFX); if (p == 0) { exit 1 }
  s = substr($0, p + length(PFX)); n = length(s)
  depth = 0; instr = 0; esc = 0; out = ""
  for (i = 1; i <= n; i++) {
    c = substr(s, i, 1); out = out c
    if (instr) {
      if (esc) { esc = 0; continue }
      if (c == "\\") { esc = 1; continue }
      if (c == "\"") instr = 0
      continue
    }
    if (c == "\"") { instr = 1; continue }
    if (c == "[") depth++
    else if (c == "]") { depth--; if (depth == 0) { print out; exit 0 } }
  }
  exit 1
}
'
# chk_findings <信封文件>：`data.check.findings` 数组原文（含首尾方括号）。
chk_findings() { chk_obj "$1" >"${WORK}/chk.arr.json" &&
  awk -v PFX='"findings":' "${JSON_ARR_AWK}" "${WORK}/chk.arr.json"; }
# rc_obj_of <信封文件>：`data.reconcile` 那一整个对象（跑 eg reconcile 时用）。
rc_obj_of() { awk -v PFX='"data":{"reconcile":' "${JSON_SLICE_AWK}" "$1"; }
# rc_findings <信封文件>：`data.reconcile.findings` 数组原文。
rc_findings() { rc_obj_of "$1" >"${WORK}/rc.arr.json" &&
  awk -v PFX='"findings":' "${JSON_ARR_AWK}" "${WORK}/rc.arr.json"; }
# names_of <数组原文文件>：把 findings 数组里的 check 值抽成去重升序清单。
names_of() { { grep -o '"check":"[a-z_]*"' "$1" || true; } |
  sed -E 's/^"check":"(.*)"$/\1/' | sort -u; }
# find_count <数组原文文件>：finding 条数（按 `"check":` 出现次数）。
find_count() { { grep -o '"check":"' "$1" || true; } | wc -l | tr -d ' '; }
# csv_of <清单文件>：把逐行清单折成逗号分隔（便于与 SCOPE_KEYS / EXCLUDED_KEYS 逐字比对）。
csv_of() { paste -sd, "$1"; }
# json_list <信封文件> <键名>：取 data.check 下某个字符串数组的值（逗号分隔，保持原序）。
json_list() {
  chk_obj "$1" | sed -E 's/^.*"'"$2"'":\[([^]]*)\].*$/\1/' | tr -d '"'
}
# assert_chk_shape <信封文件> <说明>：信封五键 + data 首键 check + data.check 恰三键且键序固定
# + findings 恒非 null + scope / excluded 两份名单逐字相等。
assert_chk_shape() {
  local f="$1" what="$2" obj keys
  obj="$(chk_obj "${f}")" || { cat "${f}"; die "${what}：data 首键必须是 check（切片前缀不命中）"; }
  keys="$(chk_keys "${f}")"
  [ "${keys}" = "${CHECK_KEYS}" ] ||
    { printf '%s\n' "${obj}"; die "${what}：data.check 键序必须逐字为 ${CHECK_KEYS}，实得 ${keys}"; }
  printf '%s\n' "${obj}" | grep -Fq '"findings":null' &&
    { printf '%s\n' "${obj}"; die "${what}：findings 恒非 null（空时为 []）"; }
  [ "$(env_keys "${f}")" = "${ENVELOPE_KEYS}" ] ||
    die "${what}：信封键序必须逐字为 ${ENVELOPE_KEYS}，实得 $(env_keys "${f}")"
  [ "$(json_list "${f}" scope)" = "${SCOPE_KEYS}" ] ||
    die "${what}：data.check.scope 必须逐字为 ${SCOPE_KEYS}，实得 $(json_list "${f}" scope)"
  [ "$(json_list "${f}" excluded)" = "${EXCLUDED_KEYS}" ] ||
    die "${what}：data.check.excluded 必须逐字为 ${EXCLUDED_KEYS}，实得 $(json_list "${f}" excluded)"
  # 报告 `reconcile` 三键恒占位（A-41：eg check 是只读子集，不是全库对账）。
  grep -Fq "\"reconcile\":${PLACEHOLDER}" "${f}" ||
    { cat "${f}"; die "${what}：报告 reconcile 三键必须逐字是占位 ${PLACEHOLDER}"; }
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库与语料
step "eg init + 一份原文 / 一篇材料笔记 / 两张卡（都由 eg 口径入库，工作区干净）"
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
write_card() { # write_card <文件> <id> [relations 段]
  local path="$1" id="$2" rels="${3:-}"
  {
    printf -- '---\n'
    printf 'id: %s\n' "${id}"
    printf 'title: 注意力机制\n'
    printf 'status: active\n'
    printf "created_at: '2026-12-01'\n"
    printf "updated_at: '2026-12-01T10:00:00+08:00'\n"
    [ -n "${rels}" ] && printf '%s\n' "${rels}"
    printf 'sources:\n'
    printf '  - source: %s\n' "${SRC}"
    printf '    note: %s\n' "${NOTE}"
    printf '    rel: support\n'
    printf '    reason: 该结论由这份材料支持\n'
    printf -- '---\n\n'
    printf '# 注意力机制\n\n## 知识内容\n\n正文占位。\n\n'
    printf '## 解释与依据\n\n依据占位。\n\n## 条件与边界\n\n边界占位。\n\n'
    printf '## 用户补充\n\n## 理解自检\n\n- 自检问题占位？\n'
  } >"${path}"
}
write_card "${VAULT}/${CARD_REL}" "${CARD}"
write_card "${VAULT}/${CARD2_REL}" "${CARD2}"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m4 eg check 命令语料"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "前置条件：工作区必须干净"; }
BASE_COMMITS="$(commits)"
ok "库就绪：1 原文 / 1 笔记 / 2 卡，工作区干净，commit 数 ${BASE_COMMITS}"

# --------------------------------------- 2. 干净 vault：退 0、commit +0、工作区逐字不变
step "干净 vault 跑 eg check：退 0（无 error 级 finding）+ commit 恰 +0 + porcelain 逐字不变"
porcelain >"${WORK}/porcelain.before"
CODE="$(eg_code check --json)"
cp "${WORK}/out.txt" "${WORK}/clean.json"
[ "${CODE}" = "0" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/clean.json"; die "干净 vault 的结构体检必须退 0（含只有 warning），实得 ${CODE}"; }
[ "$(commits)" = "${BASE_COMMITS}" ] ||
  die "eg check 产生了 commit：${BASE_COMMITS} → $(commits)（合同 §13：恒 0 次 commit）"
porcelain >"${WORK}/porcelain.after"
diff -q "${WORK}/porcelain.before" "${WORK}/porcelain.after" >/dev/null ||
  { diff "${WORK}/porcelain.before" "${WORK}/porcelain.after" || true; die "eg check 改动了工作区（必须零写入）"; }
assert_chk_shape "${WORK}/clean.json" "干净 vault"
chk_findings "${WORK}/clean.json" >"${WORK}/clean.findings"
cnt0 '干净库出现 error 级 finding' -F '"severity":"error"' "${WORK}/clean.findings"
ok "退 0 / commit ${BASE_COMMITS} → $(commits)（+0）/ porcelain 逐字不变 / 信封五键 + data.check 三键 + 两份名单逐字相等"

# --------------------------------------- 3. 重复 ID：退 2 且 commit 数**不变**
step "造重复 ID（E11，error 级）跑 eg check：退 2 + commit 恰 +0（与 eg reconcile 的关键差）"
cp "${VAULT}/${CARD_REL}" "${VAULT}/${DUP_REL}"   # 逐字节复制 → 同一个 id 落两份文件
[ "$(porcelain_count)" = "1" ] ||
  { porcelain; die "造重复 ID 后 git status 应恰 1 条（未跟踪文件），实得 $(porcelain_count)"; }
BEFORE_LOG="$(commits)"
porcelain >"${WORK}/porcelain.dup.before"
CODE="$(eg_code check --json)"
cp "${WORK}/out.txt" "${WORK}/dup.json"
[ "${CODE}" = "2" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/dup.json"; die "有 error 级 finding 时 eg check 必须退 2，实得 ${CODE}"; }
[ "$(commits)" = "${BEFORE_LOG}" ] ||
  die "eg check 退 2 之前发生了提交：${BEFORE_LOG} → $(commits)（eg reconcile 才纳管；check 恒 0 次 commit）"
porcelain >"${WORK}/porcelain.dup.after"
diff -q "${WORK}/porcelain.dup.before" "${WORK}/porcelain.dup.after" >/dev/null ||
  die "eg check 退 2 时动了工作区（脏文件必须原样留着，等用户或 eg reconcile 处理）"
assert_chk_shape "${WORK}/dup.json" "重复 ID"
chk_findings "${WORK}/dup.json" >"${WORK}/dup.findings"
grep -Fq '"check":"duplicate_id","severity":"error"' "${WORK}/dup.findings" ||
  { cat "${WORK}/dup.findings"; die "应产出 duplicate_id（error 级）"; }
ok "退 2 / commit ${BEFORE_LOG} → $(commits)（+0）/ 工作区逐字不变 / duplicate_id 逐字在册"
rm -f "${VAULT}/${DUP_REL}"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "清理重复文件后工作区应回到干净"; }

# --------------------------------------- 4. 关系异常：退 2 + 两个 R3 check 逐字在册
step "造关系异常（外部编辑写 relations[]：target 查无此对象 + 同一条边写两遍）跑 eg check：退 2"
RELS="relations:
  - type: supports
    target: ${CARD2}
    reason: 重复条目其一
  - type: supports
    target: ${CARD2}
    reason: 重复条目其二
  - type: supports
    target: ${MISSING}
    reason: 指向不存在的卡"
write_card "${VAULT}/${CARD_REL}" "${CARD}" "${RELS}"
[ "$(porcelain_count)" = "1" ] ||
  { porcelain; die "外部编辑后 git status 应恰 1 条，实得 $(porcelain_count)"; }
BEFORE_LOG="$(commits)"
CODE="$(eg_code check --json)"
cp "${WORK}/out.txt" "${WORK}/rel.json"
[ "${CODE}" = "2" ] ||
  { cat "${WORK}/err.txt"; cat "${WORK}/rel.json"; die "关系 target 缺失（E13）应让 eg check 退 2，实得 ${CODE}"; }
[ "$(commits)" = "${BEFORE_LOG}" ] ||
  die "eg check 纳管了外部编辑：${BEFORE_LOG} → $(commits)（纳管是 R1 / eg reconcile 的职责）"
[ "$(porcelain_count)" = "1" ] ||
  { porcelain; die "外部编辑必须仍留在工作区（eg check 不纳管、不提交）"; }
assert_chk_shape "${WORK}/rel.json" "关系异常"
chk_findings "${WORK}/rel.json" >"${WORK}/rel.findings"
grep -Fq '"check":"relation_target_missing","severity":"error"' "${WORK}/rel.findings" ||
  { cat "${WORK}/rel.findings"; die "应产出 relation_target_missing（E13，error 级）"; }
grep -Fq '"check":"relation_duplicate","severity":"warning"' "${WORK}/rel.findings" ||
  { cat "${WORK}/rel.findings"; die "应产出 relation_duplicate（W16，warning 级）"; }
ok "退 2 / commit +0 / 外部编辑仍在工作区 / E13 + W16 两条逐字在册"

# --------------------------------------- 5. 检查面闭合 + 五个被排除值恒 0（含非空对照）
step "findings 的 check 集合 ⊆ R3 / R4 八值，五个被排除值恒 0；同库 eg reconcile --dry-run 确有产出"
names_of "${WORK}/rel.findings" >"${WORK}/names.check"
printf '%s\n' "${SCOPE_KEYS}" | tr ',' '\n' | sort -u >"${WORK}/names.scope"
OUTSIDE="$(comm -23 "${WORK}/names.check" "${WORK}/names.scope" | tr -d ' ')"
[ -z "${OUTSIDE}" ] || { cat "${WORK}/rel.findings"; die "findings 里出现检查面外的 check：${OUTSIDE}"; }
for name in $(printf '%s\n' "${EXCLUDED_KEYS}" | tr ',' ' '); do
  cnt0 "被排除的 check ${name} 出现在 eg check 的 findings 里" -F "\"check\":\"${name}\"" "${WORK}/rel.findings"
done
# 非空对照：同一份库上 `eg reconcile --dry-run`（同样零写入零 commit）**确实**产出被排除值。
BEFORE_LOG="$(commits)"
CODE="$(eg_code reconcile --dry-run --json)"
cp "${WORK}/out.txt" "${WORK}/rc_dry.json"
[ "${CODE}" = "2" ] ||
  { cat "${WORK}/err.txt"; die "对照组（eg reconcile --dry-run）在同一份库上也应退 2，实得 ${CODE}"; }
[ "$(commits)" = "${BEFORE_LOG}" ] || die "对照组 --dry-run 产生了 commit：${BEFORE_LOG} → $(commits)"
rc_findings "${WORK}/rc_dry.json" >"${WORK}/rc.findings"
HIT=0
for name in $(printf '%s\n' "${EXCLUDED_KEYS}" | tr ',' ' '); do
  if grep -Fq "\"check\":\"${name}\"" "${WORK}/rc.findings"; then HIT=$((HIT + 1)); fi
done
[ "${HIT}" -ge 1 ] ||
  { cat "${WORK}/rc.findings"; die "对照组也没产出任何被排除值 → 上面那组「恒 0」是空判"; }
ok "检查面闭合（面外 0 命中）/ 五个被排除值在 check 侧恒 0 / 对照组命中 ${HIT} 个（非空对照成立）"

# --------------------------------------- 6. 真子集关系
step "真子集：eg check 的 finding 条数 < eg reconcile --dry-run，且 check 值集合被真包含"
N_CHK="$(find_count "${WORK}/rel.findings")"
N_RC="$(find_count "${WORK}/rc.findings")"
[ "${N_CHK}" -ge 1 ] || die "check 侧零 finding：真子集是空判"
[ "${N_CHK}" -lt "${N_RC}" ] ||
  { cat "${WORK}/rel.findings"; cat "${WORK}/rc.findings";
    die "check 侧 ${N_CHK} 条不小于 reconcile 侧 ${N_RC} 条：真子集关系不成立"; }
names_of "${WORK}/rc.findings" >"${WORK}/names.rc"
NOT_IN_RC="$(comm -23 "${WORK}/names.check" "${WORK}/names.rc" | tr -d ' ')"
[ -z "${NOT_IN_RC}" ] ||
  die "check 侧的 ${NOT_IN_RC} 不在 reconcile 侧集合内：子集关系不成立"
[ "$(csv_of "${WORK}/names.check")" != "$(csv_of "${WORK}/names.rc")" ] ||
  die "两侧 check 集合完全相同（$(csv_of "${WORK}/names.check")）：应是**真**子集"
# 逐条同源：check 侧的每一条 finding 原文都能在 reconcile 侧原文里逐字找到（不改写事实）。
python3 - "${WORK}/rel.findings" "${WORK}/rc.findings" <<'PY' || die "逐条同源比对失败"
import json, sys
a = json.load(open(sys.argv[1]))
b = json.load(open(sys.argv[2]))
key = lambda f: (f["check"], f["severity"], tuple(f["targets"]), f["detail"])
missing = [f for f in a if key(f) not in {key(x) for x in b}]
if missing:
    print("check 侧独有的 finding（应逐字出现在 reconcile 侧）：", missing)
    sys.exit(1)
PY
ok "check ${N_CHK} 条 ⊊ reconcile ${N_RC} 条 / 集合真包含 / 逐条四键同源"

# --------------------------------------- 7. 参数面：五种一律退 1 且零写入
step "参数面：--include-deprecated / --target / --domain / --fix / 位置参数 各退 1 且零写入"
BEFORE_LOG="$(commits)"
porcelain >"${WORK}/porcelain.args.before"
set -- '--include-deprecated' '--target k-x' '--domain ai-infra' '--fix' "${CARD}"
for arg in "$@"; do
  # shellcheck disable=SC2086
  CODE="$(eg_code check ${arg} --json)"
  [ "${CODE}" = "1" ] ||
    { cat "${WORK}/out.txt"; cat "${WORK}/err.txt";
      die "eg check ${arg} 应退 1（参数非法 / 不作用面），实得 ${CODE}"; }
done
[ "$(commits)" = "${BEFORE_LOG}" ] || die "参数非法路径产生了 commit：${BEFORE_LOG} → $(commits)"
porcelain >"${WORK}/porcelain.args.after"
diff -q "${WORK}/porcelain.args.before" "${WORK}/porcelain.args.after" >/dev/null ||
  die "参数非法路径动了工作区（必须零写入）"
ok "五种参数各退 1 / commit +0 / 工作区逐字不变"

# --------------------------------------- 8. 只读到底：不改写 eg report --last
step "写命令落一份最近报告 → eg check 跑前后 eg report --last 输出逐字节相同（不改写最近报告）"
# 前置：`eg report --last` 回放的是 `.eg/last-report.json`，只有**写命令**才落这份记录；
# 而 `eg check` 恰恰不落（A-41）。所以先用一条真实写命令把记录立起来：
#   ① `eg reconcile` 把上一步的外部编辑纳管掉（R1 的职责，不是 check 的），工作区回到干净；
#   ② `eg mark-reviewed`（M3 写命令）落一份最近报告 —— 它的报告 `reconcile` 三键是**占位**，
#      正好用来验证 `eg check` 之后这份记录一个字节都没变、也没被改成「跑过对账」。
[ "$(eg_code reconcile --json)" = "2" ] ||
  { cat "${WORK}/err.txt"; die "纳管用的 eg reconcile 应退 2（库里仍有 E13）"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "纳管后工作区应干净"; }
BEFORE_LOG="$(commits)"
[ "$(eg_code mark-reviewed --target "${CARD2}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg mark-reviewed 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/mark.json"
[ "$(commits)" = "$((BEFORE_LOG + 1))" ] ||
  die "eg mark-reviewed 的 commit 数应恰 +1（对账不作为写命令前置，不产生第二次提交）"
cnt0 '写命令输出里出现 data.check（说明 check 被挂到写命令前）' -F '"check":{"scope":' "${WORK}/mark.json"
grep -Fq "\"reconcile\":${PLACEHOLDER}" "${WORK}/mark.json" ||
  { cat "${WORK}/mark.json"; die "写命令的报告 reconcile 三键必须是占位（写命令不跑对账）"; }
[ "$(eg_code report --last --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg report --last 失败"; }
cp "${WORK}/out.txt" "${WORK}/last.before"
CODE="$(eg_code check --json)"
[ "${CODE}" = "2" ] || die "前置：本语料应退 2，实得 ${CODE}"
[ "$(eg_code report --last --json)" = "0" ] || die "eg report --last 失败（check 之后）"
cp "${WORK}/out.txt" "${WORK}/last.after"
diff -q "${WORK}/last.before" "${WORK}/last.after" >/dev/null ||
  { diff "${WORK}/last.before" "${WORK}/last.after" || true; die "eg check 改写了最近一次报告"; }
grep -Fq "\"reconcile\":${PLACEHOLDER}" "${WORK}/last.after" ||
  { cat "${WORK}/last.after"; die "最近一次报告里的 reconcile 三键应仍是占位（eg check 不改它）"; }
ok "report --last 前后逐字节相同 / 报告 reconcile 仍是占位"

# --------------------------------------- 9. 不作为写命令的前置 + 越界反证
step "再来一条写命令（capture）commit 恰 +1 且输出无 data.check；grep 反证 runCheck / 注册形态 / 禁用面"
# 第 8 步已经用 `eg mark-reviewed` 验过「M3 写路径 +1 次 commit 且不带 data.check」；
# 这里换一条**新建产物**的写命令（capture 走收件区 + 原文落盘），确认结论不是单命令的偶然。
BEFORE_LOG="$(commits)"
printf '第二份原文正文（越界反证用，长度足够通过正文下限校验）。%s\n' \
  '注意力机制之外的一段占位正文，用来确认 capture 在本库能正常退 0。' >"${WORK}/body.txt"
[ "$(eg_code capture --title 第二份原文 --url https://example.com/second --reason 越界反证 \
  --body-file "${WORK}/body.txt" --captured-at '2026-12-01T11:00:00+08:00' --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg capture 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/capture.json"
[ "$(commits)" = "$((BEFORE_LOG + 1))" ] || die "eg capture 的 commit 数应恰 +1"
cnt0 '写命令输出里出现 data.check' -F '"check":{"scope":' "${WORK}/capture.json"

# grep 反证 ①：runCheck 的引用面严格限于 check.go / check_test.go（写命令一律不以它为前置）。
# ── C2a·M6 现态重钉（§17.2 R7 S3 写前强校验 wiring 授权；保留历史事实 + 新增现态双侧锁，加严非放宽）──
# 历史事实一格不放宽：`runCheck`**方法**（eg check 唯一入口）在 internal/cli/ 里除 check*.go 外恒 0。
# M6·§17 的写前强校验 wiring 新增测试助手 `runCheckCLI`（precheck_wire_test.go，跑 CLI 取退出码），
# 其标识符前缀恰为 `runCheck`——是**测试专用**、非生产写路径前置。故按标识符把该 M6 助手从「runCheck 方法」
# 越界计数里剔除（历史 runCheck 方法判据本体不动），并新增双侧锁：runCheckCLI 只许落在 *_test.go。
RUNCHECK_OUT="$( { grep -rn 'runCheck' "${REPO_ROOT}/internal/cli/" --include='*.go' |
  grep -vE '/check(_test)?\.go:' || true; } | { grep -v 'runCheckCLI' || true; } | wc -l | tr -d ' ')"
[ "${RUNCHECK_OUT}" = "0" ] ||
  { grep -rn 'runCheck' "${REPO_ROOT}/internal/cli/" --include='*.go' | grep -vE '/check(_test)?\.go:' | grep -v 'runCheckCLI' || true;
    die "runCheck 方法只允许出现在 check.go / check_test.go，别处实得 ${RUNCHECK_OUT}"; }
# M6 现态双侧锁：新增助手 runCheckCLI 只许出现在测试文件（*_test.go），生产代码零引用。
RUNCHECKCLI_PROD="$( { grep -rn 'runCheckCLI' "${REPO_ROOT}/internal/cli/" --include='*.go' || true; } |
  { grep -vE '_test\.go:' || true; } | wc -l | tr -d ' ')"
[ "${RUNCHECKCLI_PROD}" = "0" ] ||
  die "M6 测试助手 runCheckCLI 泄漏到生产代码（${RUNCHECKCLI_PROD} 处）：它只许在 *_test.go"
# grep 反证 ②：注册形态恰 1 且唯一落点是 internal/cli/check.go。
REG_ALL="$( { grep -rnE 'Name:[[:space:]]*"check"' "${REPO_ROOT}/internal/cli/" --include='*.go' || true; } |
  wc -l | tr -d ' ')"
REG_OUT="$( { grep -rnE 'Name:[[:space:]]*"check"' "${REPO_ROOT}/internal/cli/" --include='*.go' |
  grep -v '/check\.go:' || true; } | wc -l | tr -d ' ')"
[ "${REG_ALL}" = "1" ] || die "check 命令注册形态应恰 1，实得 ${REG_ALL}"
[ "${REG_OUT}" = "0" ] || die "check 命令注册形态的唯一落点必须是 internal/cli/check.go，别处实得 ${REG_OUT}"
# grep 反证 ③：check.go 内的禁用面（计划层 / 提交面 / 直写 API / 五个被排除 check 字面量 /
# 修复开关 / 可见性参数 / 不启用的退出码常量）一律 0 命中 —— 「只读子集」由文件内容结构性
# 保证，不靠自律。这里用 `grep -c`（命中**行数**）直接读数，不走 cnt0（那个函数按输出行数计，
# 与 `-c` 的语义会打架）。
CHK_BAN="$( { grep -cE 'runPlan|internal/plan|Commit\(|os\.WriteFile|os\.Create|exec\.Command' \
  "${REPO_ROOT}/internal/cli/check.go" || true; } | tr -d ' ')"
[ "${CHK_BAN}" = "0" ] || die "check.go 出现计划层 / 提交面 / 直写 / 子进程调用：${CHK_BAN} 处"
CHK_EXCL="$( { grep -cE 'git_uncommitted|reviewed_at_missing|domain_moved|recap_stale|support_insufficient' \
  "${REPO_ROOT}/internal/cli/check.go" || true; } | tr -d ' ')"
[ "${CHK_EXCL}" = "0" ] ||
  die "check.go 出现被排除 check 的字面量：${CHK_EXCL} 处（排除集合必须由真源取补集）"
CHK_FLAGS="$( { grep -cE '"fix"|"repair"|"include-deprecated"|"target"|"domain"' \
  "${REPO_ROOT}/internal/cli/check.go" || true; } | tr -d ' ')"
[ "${CHK_FLAGS}" = "0" ] ||
  die "check.go 声明了修复开关 / 可见性参数 / 收窄参数：${CHK_FLAGS} 处"
CHK_EXITS="$( { grep -cE 'ExitPartialWrite|ExitCommitFailed|ExitNeedConfirm|ExitLockBusy' \
  "${REPO_ROOT}/internal/cli/check.go" || true; } | tr -d ' ')"
[ "${CHK_EXITS}" = "0" ] || die "check.go 引用了不启用的退出码常量：${CHK_EXITS} 处"
ok "写命令各 +1 次 commit 且输出无 data.check / runCheck 引用面 0 / 注册形态恰 1 且唯一落点 / 五类禁用面全 0"

printf '\n=== cmd_check.sh 全部通过（%d 步 = 1 构建 + 9 组断言 / %d 组全绿）===\n' \
  "${STEP}" "${PASS}"
