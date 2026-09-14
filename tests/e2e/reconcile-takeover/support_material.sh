#!/usr/bin/env bash
# R7 材料支撑不足实时判定（`support_insufficient` / W20 / warning，只报告 + 零落盘）的
# 端到端脚本（T-evergreen.s1_main_flow-158614-056）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §10（R7 全文：有效 support 数
# 为 0 即判定；有效 = 对端存在且未逻辑删除；**永不改 `status`**；**零落盘标记**——那句方括号
# 视图提示只由命令层渲染、只进 stdout；与 M3 删除路径那张建议清单路径分治）、§1.1（对账包
# 只读三条零）、§1.3 写口归属表第 4 行（R3 / R4 / R5 / R7 无人写 → 零 RepairSpec）、
# §2（Finding 四键）、§3 单射表（`support_insufficient` ↔ `W20` ↔ warning；`W20` 是 W 段
# **末位**，`W21` 不再分配）、§6.2（`card.sources[].note` 缺失归 `dangling_ref` / E12，
# 一件事不许两码重复计）、§15（明确不做）；
# `docs/specs/2026-11-15-m4-prestart-adjudication.md` 的 **A-31**（有 error 级 finding 退 2
# —— W20 是 warning，因此单有 W20 时退出码恒 0）；
# `docs/specs/2026-10-13-m3-prestart-adjudication.md` 的 **A-28**（路径分治）；
# `milestones/M-004-m4.md` 完成判据 9。
#
# 九段断言（task deliverable 逐字要求：造一张零 support 卡 + 一张 support 对端已逻辑删除的卡
# → 跑只读检查 → 恰 2 条 W20 / 卡 status 字节不变 / vault 内提示命中 0 次）：
#   ① 造一个**干净**的小 vault（2 原文 / 2 笔记 / 3 张互连卡，全部由 `eg` 口径的提交入库）；
#   ② 只读检查：**恰 2 条 W20**（零 support 的 alpha + support 对端已逻辑删除的 beta），
#      severity 全是 warning、`targets` 元数恒 1、有效 support 卡（gamma）零命中、
#      退出码 0、**零 RepairSpec**；
#   ③ **永不改 `status`**：检查前后三张卡的 `status` / `deprecated` / 删除维度事实逐字不变，
#      文件字节 sha256 不变，`status: active` 行数恒 3，没有任何卡被写成 `deprecated`；
#   ④ **零落盘标记**：`grep -rn '<视图提示>' "$VAULT" | wc -l` → `0`；vault 内同样零
#      `support_insufficient` / `W20` / `deleted_at` 新增；`git status --porcelain` → `0`；
#      commit 数不变、`sources[]` 条目数不变；
#   ⑤ **提示只在渲染层**：事实里的人类可读行由 `internal/cli/reconcile_render.go` 产出且
#      逐行以提示开头；该字面量在仓库内的**非测试**落点恰 1 个文件（渲染层），
#      对账包与落盘层恒 0；
#   ⑥ 幂等可复算：同一 vault 连跑两次，事实 JSON 逐字节相同；
#   ⑦ 判定可逆：把 beta 的对端笔记「恢复」（去掉 `deleted_at`）→ W20 降到 1；再给 alpha 补一条
#      有效 support → W20 归零、退出码仍 0（判定条件双向可逆，且全程零落盘标记）；
#   ⑧ 与 R1 / R2 / R3 / R4 / R5 零重复计数：其余七项条数恒 0；对端**缺失**（而非删除）时
#      让位给 E12 —— W20 不重复记这一件事；
#   ⑨ 源码级边界反证（对账包三条零 / 状态写口与状态字段零命中 / 提示字面量分域计数 /
#      `W2[1-9]` 恒 0 / 命令本体零注册 / checkers 恰 6 / op 恒 16 / `content_hash` 不减 /
#      M3 那张建议清单一字未动）。
#
# 为什么用 `go test` 驱动而不是 `eg check`：
#   `eg check` / `eg reconcile` 命令本体、信封与退出码属 T-…-059 / T-…-058，本 task 明确
#   不注册命令，只交付 R7 检查器与渲染函数。脚本因此在真实临时 vault 上用
#   `test/e2e/m4_r7_support_test.go` 这一层驱动壳调检查器（同 `m4_r4_structure.sh` /
#   `m4_r5_domain_moved.sh` 的先例），事实一律回读驱动壳落盘的 JSON、vault 自己与 git 自己，
#   不看实现自报。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go）；
# **一切写操作只发生在 mktemp -d 目录内**；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/reconcile-takeover/support_material.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-r7.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

DOM="ai-infra"
KDIR="${VAULT}/domains/${DOM}/knowledge"
NDIR="${VAULT}/domains/${DOM}/notes"

CARD_A="k-20261127-alpha"  # 零 support 条目（只有 against / context）→ 命中 W20
CARD_B="k-20261127-beta"   # 唯一 support 的对端笔记**已逻辑删除** → 命中 W20
CARD_C="k-20261127-gamma"  # 一条有效 support → 零命中
NOTE_LIVE="n-20261127-attention" # 未删除的材料笔记
NOTE_DEL="n-20261127-second"     # 已逻辑删除的材料笔记
SRC_1="s-20261127-attention"
SRC_2="s-20261127-second"

# HINT 是 R7 的视图提示逐字字面量：本脚本内**只在这一行**出现，
# 其余断言一律引用变量（提示的唯一实现落点是 internal/cli/reconcile_render.go）。
HINT='[材料支持不足]'

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
cards_sum() { find "${KDIR}" -name '*.md' -type f -print0 | xargs -0 sha256sum | sort; }
# support_entries：全库 sources[] 条目数（`- source:` 行数），只报告的字节级反证。
support_entries() { find "${VAULT}/domains" -name '*.md' -type f -print0 |
  xargs -0 grep -c '^  - source:' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}'; }
seal() { gitv add -A >/dev/null && gitv -c user.name=e2e -c user.email=e2e@local \
  commit -q -m "$1: $2" >/dev/null; }

# drive <标签>：在真实 vault 上跑驱动壳，事实落 ${FACTS}（另存一份带标签的副本）。
drive() {
  local tag="$1"
  rm -f "${FACTS}"
  ( cd "$(eg_staged_root)" &&
    EG_M4_R7_VAULT="${VAULT}" EG_M4_R7_OUT="${FACTS}" \
    go test ./test/e2e/ -run TestM4R7SupportHarness -count=1 -v </dev/null ) \
    >"${WORK}/drive-${tag}.log" 2>&1 ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未全绿"; }
  grep -Fq -- "--- PASS: TestM4R7SupportHarness" "${WORK}/drive-${tag}.log" ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未 PASS（可能被 skip）"; }
  [ -s "${FACTS}" ] || die "驱动壳（${tag}）未落事实文件"
  cp "${FACTS}" "${WORK}/facts-${tag}.json"
}

fact_num() { sed -E "s/.*\"$1\":([0-9]+).*/\1/" "${FACTS}"; }
fact_count() { grep -o -- "$1" "${FACTS}" | wc -l | tr -d ' '; }
want_num() { # want_num <键> <期望值>
  [ "$(fact_num "$1")" = "$2" ] ||
    { cat "${FACTS}"; die "$1 应为 $2，实得 $(fact_num "$1")"; }
}
want_lit() { # want_lit <说明> <应逐字出现的片段>
  grep -Fq -- "$2" "${FACTS}" || { cat "${FACTS}"; die "$1：事实里未出现 $2"; }
}
no_lit() { # no_lit <说明> <不得出现的片段>
  grep -Fq -- "$2" "${FACTS}" && { cat "${FACTS}"; die "$1：事实里出现了 $2"; }
  return 0
}
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# source_file <ID> <标题>：写一份原文（正文收录后不因加工改写）。
source_file() {
  cat >"${VAULT}/sources/$1.md" <<SRC
---
id: $1
url: https://example.com/$1
title: $2
saved_at: '2026-11-27T09:00:00+08:00'
---

# $2

正文占位（收录后不因加工改写）。
SRC
}

# note_file <ID> <原文 ID> <live|deleted>：写一篇材料笔记；deleted 即带 deleted_at 两键
# （删除维度逐字落盘，是 R7「有效性」的输入之一，不是 R7 自己写的）。
note_file() {
  local id="$1" src="$2" state="$3" del=""
  if [ "${state}" = "deleted" ]; then
    del="deleted_at: '2026-11-27T11:00:00+08:00'
deleted_reason: 原文撤稿，材料整篇作废"
  fi
  cat >"${NDIR}/${id}.md" <<NOTE
---
id: ${id}
source: ${src}
created_at: '2026-11-27'
updated_at: '2026-11-27T10:00:00+08:00'
reviewed_at: '2026-11-27T10:00:00+08:00'
${del}
---

# 材料笔记 ${id}

## 材料提炼

提炼占位（本笔记正文不参与任何判定）。
NOTE
}

# card_file <ID> <sources 段> <relations 段>：写一张知识卡（**不含 `domain` 键**——
# 领域由目录唯一决定，EG-DOM-01；`status` 恒 active，是「永不改 status」的比对基准）。
card_file() {
  local id="$1" srcs="$2" rels="${3-}"
  if [ -z "${rels}" ]; then rels="relations: []"; else rels="relations:
${rels}"; fi
  cat >"${KDIR}/${id}.md" <<CARD
---
id: ${id}
title: 材料支撑判定用卡 ${id}
status: active
created_at: '2026-11-27'
updated_at: '2026-11-27T10:00:00+08:00'
reviewed_at: '2026-11-27T10:00:00+08:00'
sources:
${srcs}
${rels}
---

## 知识内容

R7 实时判定的端到端数据（本卡正文不参与任何判定）。
CARD
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 造数
step "建库并造数：2 原文 / 2 笔记（其一已逻辑删除）/ 3 张互连卡，以 eg 口径的提交入库"
[ "$(eg_code init --domain "${DOM}")" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain "${DOM}")" = "0" ] || die "config set 失败"
mkdir -p "${KDIR}" "${NDIR}" "${VAULT}/sources"
source_file "${SRC_1}" "材料支撑判定原文一"
source_file "${SRC_2}" "材料支撑判定原文二"
note_file "${NOTE_LIVE}" "${SRC_1}" live
note_file "${NOTE_DEL}" "${SRC_2}" deleted
# alpha：两条材料关系，但**没有一条是 support**（against / context 不是支持）→ 命中 W20。
card_file "${CARD_A}" "  - source: ${SRC_1}
    note: ${NOTE_LIVE}
    rel: against
    reason: 这份材料与该结论相反
  - source: ${SRC_2}
    note: ${NOTE_LIVE}
    rel: context
    reason: 这份材料只提供背景" "  - type: supports
    target: ${CARD_C}
    reason: 支持 gamma 那张卡的结论"
# beta：唯一一条 support 的对端笔记**已逻辑删除**（对端存在，但不再有效）→ 命中 W20。
card_file "${CARD_B}" "  - source: ${SRC_2}
    note: ${NOTE_DEL}
    rel: support
    reason: 该结论曾由这份已作废材料支持" "  - type: derives
    target: ${CARD_C}
    reason: 由 gamma 那张卡推导而来"
# gamma：一条对端齐备且未删除的 support → 零命中（入边来自 alpha / beta 的关系条目，
# 因此它不是「零关系卡」，不掺入 R4 的 W17；论证关系取值只有恰 4 值，本卡不自设出边）。
card_file "${CARD_C}" "  - source: ${SRC_1}
    note: ${NOTE_LIVE}
    rel: support
    reason: 该结论由这份材料支持"
seal "process(${DOM})" "收录三张卡、两篇笔记与两份原文"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库（工作区为空）"; }
BASE_COMMITS="$(commits)"
BASE_SUM="$(tree_sum)"
BASE_CARDS="$(cards_sum)"
BASE_ENTRIES="$(support_entries)"
[ "${BASE_ENTRIES}" = "4" ] || die "造数后 sources[] 条目应为 4 条，实得 ${BASE_ENTRIES}"
ok "干净 vault 就绪：commit 数 ${BASE_COMMITS}，工作区为空，sources[] 条目 ${BASE_ENTRIES} 条"

# ---------------------------------------------------------------- 2. 恰 2 条 W20
step "只读检查：恰 2 条 W20 / warning，targets 恒一元，有效 support 卡零命中，退出码 0"
drive base
want_num cards 3
want_num notes 2
want_num sources 2
want_num support_insufficient 2
[ "$(fact_count '"check":')" = "2" ] || { cat "${FACTS}"; die "finding 总数应恰 2"; }
[ "$(fact_count '"check":"support_insufficient"')" = "2" ] ||
  { cat "${FACTS}"; die "两条 finding 的 check 都必须逐字是 support_insufficient"; }
[ "$(fact_count '"W20"')" = "2" ] || { cat "${FACTS}"; die "W20 应恰 2 次（合同 §3 单射）"; }
[ "$(fact_count '"severity":"warning"')" = "2" ] ||
  { cat "${FACTS}"; die "W20 的 severity 必须是 warning"; }
cnt0 'W20 被误判成 error 级' -F '"severity":"error"' "${FACTS}"
want_lit "targets 必须恰是 [知识卡 ID]（一元）" "\"targets\":[\"${CARD_A}\"]"
want_lit "targets 必须恰是 [知识卡 ID]（一元）" "\"targets\":[\"${CARD_B}\"]"
want_lit "targets 元数必须恒 1" '"targets_arity":[1,1]'
no_lit "有效 support 的卡不得命中" "\"targets\":[\"${CARD_C}\"]"
# 支持面事实逐字可复算：声明 / 有效条数 + 无效原因。
want_lit "alpha：零 support 条目" "\"${CARD_A}|0|0\""
want_lit "beta：声明 1 条但对端已逻辑删除" "\"${CARD_B}|1|0|note_deleted\""
want_lit "gamma：声明 1 条且有效" "\"${CARD_C}|1|1\""
want_lit "detail 必须写明只报告 / 零落盘边界" '永不改该卡状态'
want_lit "detail 必须写明有效性口径" '有效 = 对端存在且未逻辑删除'
want_lit "W20 是 warning，has_error 必须为 false" '"has_error":false'
want_num exit_code 0
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R7 只报告，恒零 RepairSpec"; }
ok "恰 2 条 W20 / warning、targets 恒一元、gamma 零命中、exit 0、零 RepairSpec"

# ---------------------------------------------------------------- 3. 永不改 status
step "永不改 status：三张卡的状态维度事实与文件字节逐字不变，没有任何卡被写成 deprecated"
want_lit "alpha 状态维度逐字不变" "\"${CARD_A}|active|false|false\""
want_lit "beta 状态维度逐字不变" "\"${CARD_B}|active|false|false\""
want_lit "gamma 状态维度逐字不变" "\"${CARD_C}|active|false|false\""
[ "${BASE_CARDS}" = "$(cards_sum)" ] || die "卡文件字节被改动（R7 永不改 status、零落盘）"
[ "$(grep -c '^status: active' "${KDIR}/${CARD_A}.md" "${KDIR}/${CARD_B}.md" \
  "${KDIR}/${CARD_C}.md" | awk -F: '{s+=$2} END {print s+0}')" = "3" ] ||
  die "三张卡的 status 行必须仍是 active"
cnt0 '卡被写成 deprecated' -rn '^status: deprecated' "${KDIR}/"
cnt0 '卡被补出删除维度键' -rn '^deleted_at:' "${KDIR}/"
ok "状态维度事实逐字不变 / 卡文件 sha256 不变 / 零 deprecated / 零 deleted_at"

# ---------------------------------------------------------------- 4. 零落盘标记
step "零落盘标记：vault 内提示命中 0 次，零写盘 / 零 commit / sources[] 条目不变"
[ "$(grep -rn -F -- "${HINT}" "${VAULT}" | wc -l | tr -d ' ')" = "0" ] ||
  { grep -rn -F -- "${HINT}" "${VAULT}"; die "视图提示被写进了 vault：R7 必须零落盘标记"; }
cnt0 'check 名被写进 vault' -rn 'support_insufficient' "${VAULT}/domains" "${VAULT}/sources"
cnt0 '诊断码被写进 vault' -rn 'W20' "${VAULT}/domains" "${VAULT}/sources"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后 git status --porcelain 应为空"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "只读检查改变了 commit 数（R7 零 commit）"
[ "${BASE_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了（R7 零写盘）"
[ "${BASE_ENTRIES}" = "$(support_entries)" ] ||
  die "sources[] 条目数被改动：${BASE_ENTRIES} → $(support_entries)（R7 不补材料关系）"
want_num support_entries 4
ok "vault 内提示 0 命中 / 零写盘 / 零 commit / sources[] 条目 ${BASE_ENTRIES} 条不变"

# ---------------------------------------------------------------- 5. 提示只在渲染层
step "提示只在渲染层：事实里的人类可读行由渲染函数产出，仓库非测试落点恰 1 个文件"
want_num hint_count 1
[ "$(fact_count "$(printf '%s' "${HINT}")")" -ge 3 ] ||
  { cat "${FACTS}"; die "渲染行与摘要都应带提示（≥ 3 次：2 行 + 1 摘要）"; }
want_lit "渲染行逐字以提示开头且带码与卡 ID" "${HINT} W20 warning ${CARD_A}"
want_lit "渲染行逐字以提示开头且带码与卡 ID" "${HINT} W20 warning ${CARD_B}"
want_lit "渲染摘要必须写明只提示、未改产物" '仅提示，未改任何产物、未写任何标记'
RENDER="${REPO_ROOT}/internal/cli/reconcile_render.go"
[ "$(grep -c -F -- "${HINT}" "${RENDER}")" -ge 1 ] || die "提示字面量必须在渲染层在册"
# 分域计数：提示字面量的**非测试**落点恰 1 个文件（渲染层），对账包与落盘层恒 0。
HINT_FILES="$( { grep -rl -F -- "${HINT}" "${REPO_ROOT}/internal/" || true; } |
  { grep -v '_test.go' || true; } | sort)"
[ "$(printf '%s\n' "${HINT_FILES}" | { grep -c . || true; } )" = "1" ] ||
  { printf '%s\n' "${HINT_FILES}"; die "提示字面量的非测试落点必须恰 1 个文件"; }
[ "${HINT_FILES}" = "${RENDER}" ] ||
  { printf '%s\n' "${HINT_FILES}"; die "提示的唯一非测试落点必须是渲染层"; }
cnt0 '对账包 / 落盘层出现提示字面量' -rn -F -- "${HINT}" \
  "${REPO_ROOT}/internal/reconcile/" "${REPO_ROOT}/internal/store/"
ok "提示只在渲染层（唯一非测试落点 ${RENDER##*/}），对账包与落盘层恒 0"

# ---------------------------------------------------------------- 6. 幂等可复算
step "幂等可复算：同一 vault 连跑两次，事实 JSON 逐字节相同"
drive base2
cmp -s "${WORK}/facts-base.json" "${WORK}/facts-base2.json" ||
  { diff "${WORK}/facts-base.json" "${WORK}/facts-base2.json" || true;
    die "两次检查的事实不同：R7 必须可逐字复算"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "第二次检查后工作区应仍为空"; }
[ "${BASE_SUM}" = "$(tree_sum)" ] || die "第二次检查写盘了"
ok "两次事实逐字节相同 + 第二次仍零写入"

# ---------------------------------------------------------------- 7. 与 E12 不重复计数
step "与 R4 的 dangling_ref 不重复计数：对端**缺失**（非删除）时让位 E12，W20 不再记一次"
card_file "${CARD_B}" "  - source: ${SRC_2}
    note: n-20269999-gone
    rel: support
    reason: 该结论由一篇不存在的材料支持" "  - type: derives
    target: ${CARD_C}
    reason: 由 gamma 那张卡推导而来"
seal "process(${DOM})" "把 beta 的 support 对端换成一个不存在的笔记"
drive missing
want_num dangling_ref 1
want_num support_insufficient 1
want_lit "唯一命中的 W20 仍是零 support 的 alpha" "\"targets\":[\"${CARD_A}\"]"
want_lit "beta 的支持面事实如实记为对端缺失" "\"${CARD_B}|1|0|note_missing\""
want_lit "E12 是 error 级，has_error 必须为 true" '"has_error":true'
want_num exit_code 2
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R7 / R4 恒零 RepairSpec"; }
ok "对端缺失这一件事只被 E12 记一次（W20 让位），且让位不影响 alpha 的判定"

# ---------------------------------------------------------------- 8. 判定可逆
step "判定可逆：恢复 beta 的对端笔记 → W20 仍 1；给 alpha 补一条有效 support → W20 归零"
card_file "${CARD_B}" "  - source: ${SRC_2}
    note: ${NOTE_DEL}
    rel: support
    reason: 该结论由这份材料支持" "  - type: derives
    target: ${CARD_C}
    reason: 由 gamma 那张卡推导而来"
note_file "${NOTE_DEL}" "${SRC_2}" live
seal "process(${DOM})" "把作废的材料笔记恢复（去掉删除维度两键）"
cnt0 '恢复后仍残留删除维度键' -n '^deleted_at:' "${NDIR}/${NOTE_DEL}.md"
drive restored
want_num support_insufficient 1
want_num dangling_ref 0
want_lit "唯一命中的仍是零 support 的 alpha" "\"targets\":[\"${CARD_A}\"]"
want_lit "beta 的 support 恢复有效" "\"${CARD_B}|1|1\""
want_num exit_code 0
card_file "${CARD_A}" "  - source: ${SRC_1}
    note: ${NOTE_LIVE}
    rel: against
    reason: 这份材料与该结论相反
  - source: ${SRC_1}
    note: ${NOTE_LIVE}
    rel: support
    reason: 补一条真正的材料支撑" "  - type: supports
    target: ${CARD_C}
    reason: 支持 gamma 那张卡的结论"
seal "process(${DOM})" "给 alpha 补一条有效的材料支撑"
drive fixed
want_num support_insufficient 0
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "补齐材料支撑后应零 finding"; }
want_lit "alpha 现在有 1 条有效 support（另有 1 条非 support）" "\"${CARD_A}|1|1\""
[ "$(fact_count "$(printf '%s' "${HINT}")")" = "0" ] ||
  { cat "${FACTS}"; die "零命中时不得渲染任何提示行"; }
want_lit "零 finding 时 has_error 应为 false" '"has_error":false'
want_num exit_code 0
[ "$(grep -rn -F -- "${HINT}" "${VAULT}" | wc -l | tr -d ' ')" = "0" ] ||
  die "全程 vault 内不得出现提示字面量"
ok "判定双向可逆（2 → 1 → 0）、退出码回 0、全程零落盘标记"

# ---------------------------------------------------------------- 9. 不重复计数 + 源码反证
step "与 R1 / R2 / R3 / R5 零重复计数：其余各项条数恒 0（本驱动壳只驱动 R7）"
want_num git_uncommitted 0
want_num reviewed_at_missing 0
want_num duplicate_id 0
want_num orphan 0
want_num relation_findings 0
want_num domain_moved 0
cnt0 '越界产出 R1 / R2 / R3 / R5 / R6 的 check' -E \
  '"(git_uncommitted|reviewed_at_missing|duplicate_id|orphan|relation_[a-z_]*|domain_moved|recap_stale)"' \
  <(grep -o '"check":"[a-z_]*"' "${FACTS}" || true)
ok "六项恒 0；材料支撑这件事只被 W20 记一次"

step "边界反证：对账包三条零 / 状态写口零命中 / W2[1-9] 恒 0 / 命令零注册 / 计数面不动"
R7SRC="${REPO_ROOT}/internal/reconcile/r7_support.go"
cnt0 '检查侧写盘或提交' -rnE 'os\.WriteFile|os\.Create|os\.Remove|os\.Rename|os\.OpenFile|Commit\(' "${R7SRC}"
cnt0 '检查侧起子进程' -rnE 'os/exec|exec\.Command' "${R7SRC}"
cnt0 '检查侧越界依赖' -rnE 'internal/(store|plan|cli|report|proposal|rules)' "${R7SRC}"
# 状态写口的口径与 Acceptance 逐字一致：`grep -v _test.go`（用例文件里出现的是**反证 grep
# 的判据串本身**，不是状态写口的调用）。
STATUS_WRITES="$( { grep -rn 'SetStatus\|StatusDeprecated' "${REPO_ROOT}/internal/reconcile/" ||
  true; } | { grep -v '_test.go' || true; } | wc -l | tr -d ' ')"
[ "${STATUS_WRITES}" = "0" ] ||
  { grep -rn 'SetStatus\|StatusDeprecated' "${REPO_ROOT}/internal/reconcile/" | grep -v '_test.go';
    die "对账包非测试源出现状态写口：期望 0 命中，实得 ${STATUS_WRITES}"; }
cnt0 '检查侧读写卡的状态字段' -rnE '\.Status|Status =|Deprecated =' "${R7SRC}"
cnt0 '检查侧调材料关系写 API' -rnE 'AddSource|RemoveSource|SetSource|AddRelation' "${R7SRC}"
# check 值只许引用 check.go 的枚举真源：源码里不得出现带引号的 `"support_insufficient"`。
cnt0 'check 值自写字面量' -nE '"support_insufficient"' "${R7SRC}"
[ "$(grep -c 'CheckSupportInsufficient' "${R7SRC}")" -ge 1 ] ||
  die "必须引用 CheckSupportInsufficient 枚举"
[ "$(grep -c 'model.MaterialSupport' "${R7SRC}")" -ge 1 ] ||
  die "support 取值必须复用 internal/model 的封闭三值（不自写字面量）"
cnt0 '自写材料关系字面量' -nE '"(support|against|context)"' "${R7SRC}"
[ "$(grep -cE 'x\.Has\(|NewStructureIndex' "${R7SRC}")" -ge 1 ] ||
  die "「对端存在」必须复用 T-…-052 的 ID 索引（不另写存在性判断）"
# W20 是 **M4 收口时**的 W 段末位。M5（S4）起 W2x 段被继续分配，因此这里改成
# **分域 + 复算**，M4 的历史结论一个字不放宽：
#   ① 摘掉 M5 唯一落地面 `internal/index/` 之后，W2[1-9] 在 internal 全库恒 0
#      —— 这就是 M4 结论「W20 是末位」的原样复算；
#   ② M5 已落地的 W22 / W23 / W24 只许出现在 `internal/index/`（越界即红）；
#   ③ W21 / W25–W29 全库恒 0：W25+ 属后续 task，未做不许提前出现。
#      （2026-09-08 随 M5 · T-…-066 阶段 B 精确重钉：W22 = index_stale 已按索引合同 §9 正式启用，
#       故它从「全库恒 0」这一格移到「只许落在 internal/index/」那一格 —— 分域口径不变，
#       M4 结论「摘掉 M5 落地面后 W2x 全库恒 0」原样复算。）
#      （2026-09-09 随 M5 · T-…-068 收口后再次精确重钉：W25 = result_truncated 已按合同 §7.5
#       正式启用，唯一字面量落点是 `internal/query/page.go`。故 M5 落地面从「internal/index/」
#       扩为「internal/index/ ∪ internal/query/page.go」，W25 从「全库恒 0」那一格移到
#       「只许落在 page.go 且非注释面恰 1」那一格 —— 分域口径不变、格数只增不减，
#       M4 结论「摘掉 M5 落地面后 W2x 全库恒 0」仍原样复算。）
M5_LANDED='/internal/index/|/internal/query/page\.go'
# ── C2a·M6 现态重钉（§16.3 M6 诊断码分域 + §16.4 现态重钉授权；保留历史事实 + 新增现态双侧锁，加严非放宽）──
# 沿用本脚本既有「W 段随里程碑逐步分配、按包分域」的原样口径：M4 收口时 W20 是 W 段末位；M5 把 W22–W25
# 发放给 internal/index/ 与 internal/query/page.go（上方两格）；M6·§12/§16.3 再把 **W26/W28 发放给
# internal/txn/、W27 发放给 internal/mdfile/**。故把 M6 落地面并入越位剔除集合（历史事实一格不放宽：
# 摘掉 M5∪M6 落地面后 W2[1-9] 全库仍恒 0），并在下方新增封闭双侧锁把三个 M6 码逐字钉在各自专属包。
M6_LANDED='/internal/txn/|/internal/mdfile/'
W2X_OUTSIDE="$( { grep -rnE '"W2[1-9]"' "${REPO_ROOT}/internal/" --include='*.go' || true; } |
  { grep -vE "${M5_LANDED}|${M6_LANDED}" || true; } | wc -l | tr -d ' ')"
[ "${W2X_OUTSIDE}" = "0" ] ||
  { grep -rnE '"W2[1-9]"' "${REPO_ROOT}/internal/" --include='*.go' | grep -vE "${M5_LANDED}|${M6_LANDED}"
    die "W 段越位（摘掉 M5∪M6 落地面后仍有 ${W2X_OUTSIDE} 处）：W20 是 M4 末位、W2x 只许落在已分配落地面"; }
# W21 / W29 仍未分配 ⇒ 全库恒 0（历史事实原样复算，一格不放宽）。
for code in W21 W29; do
  cnt0 "未分配 / 未落地的 ${code}" -rnE "\"${code}\"" "${REPO_ROOT}/internal/" --include='*.go'
done
# M6 码封闭双侧锁：W26/W28 只许落 internal/txn/、W27 只许落 internal/mdfile/，且各自至少在场 1 处（少一处 / 挪窝都红）。
for code in W26 W28; do
  [ "$(grep -rlE "\"${code}\"" "${REPO_ROOT}/internal/" --include='*.go' |
      { grep -v '/internal/txn/' || true; } | wc -l | tr -d ' ')" = "0" ] ||
    die "${code} 出现在 internal/txn/ 之外：M6 事务域诊断码只许落在 txn 包（§16.3）"
  [ "$(grep -rlE "\"${code}\"" "${REPO_ROOT}/internal/txn/" --include='*.go' | wc -l | tr -d ' ')" -ge 1 ] ||
    die "${code} 未在 internal/txn/ 落地：M6·§12 已分配该码"
done
[ "$(grep -rlE '"W27"' "${REPO_ROOT}/internal/" --include='*.go' |
    { grep -v '/internal/mdfile/' || true; } | wc -l | tr -d ' ')" = "0" ] ||
  die "W27 出现在 internal/mdfile/ 之外：M6 块级合并码只许落在 mdfile 包（§16.3）"
[ "$(grep -rlE '"W27"' "${REPO_ROOT}/internal/mdfile/" --include='*.go' | wc -l | tr -d ' ')" -ge 1 ] ||
  die "W27 未在 internal/mdfile/ 落地：M6·§7 已分配该码"
for code in W22 W23 W24; do
  [ "$(grep -rlE "\"${code}\"" "${REPO_ROOT}/internal/" --include='*.go' |
      { grep -v '/internal/index/' || true; } | wc -l | tr -d ' ')" = "0" ] ||
    die "${code} 出现在 internal/index/ 之外：M5 索引域诊断码只许落在索引包"
done
# W25 属查询域（分页截断）：唯一落点 internal/query/page.go，且**非注释面恰 1**（常量声明一处）。
[ "$(grep -rlE '"W25"' "${REPO_ROOT}/internal/" --include='*.go' |
    { grep -v '/internal/query/page\.go$' || true; } | wc -l | tr -d ' ')" = "0" ] ||
  die "W25 出现在 internal/query/page.go 之外：分页截断码只许落在分页实现文件"
W25_CODE="$( { grep -nE '"W25"' "${REPO_ROOT}/internal/query/page.go" || true; } |
  { grep -vE '^[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
[ "${W25_CODE}" = "1" ] ||
  die "W25 在 page.go 的非注释面应恰 1 处（唯一常量声明），实得 ${W25_CODE}"
# `eg check` / `eg reconcile` 命令本体属 T-…-059 / T-…-058：本 task 不注册命令。
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
cnt0 '检查侧出现越界能力' -rnE '\.index/|FTS5|[Ss][Qq][Ll]ite|flock|P95' "${REPO_ROOT}/internal/reconcile/"
# 计数面：checkers 恰 6（R1 / R2 / R4 / R3 / R5 / R7）、op 恒 16、content_hash 面不减。
# 2026-09-07 随 M4 · T-…-055 阶段 3 **按实测重钉 op 计数锚点**（本体一格不放宽，反而更严）：
# T-…-055 阶段 1 的 `set_stale`（A-33）把 `internal/plan` 的等式行改写成加法等式
# `m3AllOps, m4NewOps = 16, 1`，旧字面 `!= 16` 锚点漂移（grep 计数 0 → 本行 `set -e` 崩）。
# 重钉后锚定加法等式：同时钉住「M3 期 16 逐字未改写」与「M4 期新增恰 1（不属本 task）」。
CHK="$(grep -c 'landedR7 = 1' "$(eg_test_path internal/reconcile/r1_git_test.go)")"
[ "${CHK}" = "1" ] || die "checkers 数量断言必须已把 R7 计入（landedR7 = 1）"
OPS="$(grep -c 'm3AllOps, m4NewOps = 16, 1' "$(eg_test_path internal/plan/m3_test.go)")"
[ "${OPS}" = "1" ] || die "AllOpNames 必须仍是「M3 期 16 + M4 新增 1」（本 task 不新增 op）"
[ "$(grep -c 'OrderedTargetsCheckCount = 1' "${REPO_ROOT}/internal/reconcile/check.go")" = "1" ] ||
  die "有序 targets 例外必须恰 1 项（W20 的 targets 是一元，不进例外面）"
[ "$(grep -rn 'content_hash' "${REPO_ROOT}" --include='*.go' --include='*.md' --include='*.sh' |
  wc -l | tr -d ' ')" -ge 42 ] || die "content_hash 事实面不得减少（≥ 42）"
# A-28 路径分治：M3 删除路径那张建议清单一字未动（结构体键与两条建议文案仍在原处）。
DELSRC="${REPO_ROOT}/internal/report/support_check.go"
for lit in 'support_check' 'recommendation' '建议标记' '建议重新检查材料关系'; do
  [ "$(grep -c -F -- "${lit}" "${DELSRC}")" -ge 1 ] ||
    die "M3 删除路径的 ${lit} 不在原处：本 task 不得改写它"
done
cnt0 '对账包出现删除路径字段' -rn 'support_check\|recommendation' "${REPO_ROOT}/internal/reconcile/"
ok "三条零 / 状态写口与状态字段零命中 / W21+ 恒 0 / 命令零注册 / checkers 与 op 计数面在册"

printf '\n=== support_material.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
