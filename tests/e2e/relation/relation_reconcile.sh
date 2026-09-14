#!/usr/bin/env bash
# R3 关系校验四子检查（E13 target 缺失 / E14 前缀非法 / W15 opposing 方向不对称 /
# W16 重复关系对）的端到端脚本（T-evergreen.s1_main_flow-158614-053）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §7（R3 全文：四子检查判定表、
# 只报告、不改 F4、与 A-24 不冲突）、§6.2 末段（`E12` 与 `E13` 的分工：关系条目的 target
# 缺失只走 R3）、§2（Finding 四键 + targets 去重升序）、§3（check ↔ 诊断码单射 + severity）、
# §13（`eg check` 只跑 R3 / R4）；`docs/specs/2026-11-15-m4-prestart-adjudication.md` 的
# **A-31**（有 error 级 finding 退 2）；`docs/specs/2026-10-13-m3-prestart-adjudication.md`
# 的 **A-24**（`opposing` 先按两端 ID 字典序规范化、单向存储恰一条、不留墓碑）；
# `milestones/M-004-m4.md` 完成判据 6 与风险 R-17。
#
# 六段断言（task deliverable 逐字要求）：
#   ① 造一个**干净**的小 vault（1 原文 / 1 笔记 / 4 张互连卡，含一条**规范方向**的 opposing）
#      → 只读检查零 finding、按 A-31 换算的退出码 0（规范的单向 opposing 不得被误报 W15）；
#   ② 外部编辑造四处关系异常（一条 target 不存在、一条 target 前缀非法、一条**非规范方向**的
#      单向 opposing、一条重复三元组）→ 只读检查 **E13 / E14 / W15 / W16 各恰 1 条**、
#      severity 分别 error / error / warning / warning、targets 逐字可复算、
#      按 A-31 换算的退出码 **2**、零 RepairSpec；
#   ③ **零自动修**：检查前后 `git status --porcelain` 恒为空、commit 数不变、
#      每张卡的 `relations[]` 条目数逐卡不变、四张卡的 frontmatter 字节 sha256 逐字不变、
#      vault 文件树 sha256 逐字不变；
#   ④ 与 R4 零重复计数：同一份异常数据里 `dangling_ref`（E12）/ `duplicate_id`（E11）/
#      `orphan`（W17）恒 0，关系 target 缺失只被 E13 记一次；
#   ⑤ 幂等可复算：同一 vault 连跑两次，事实 JSON 逐字节相同；
#   ⑥ 判定可逆 + 存在性看落盘事实：四处修好（含把 opposing 搬回规范方向）→ finding 归零、
#      退出码回 0；再把 target 卡标成 `deprecated` → 事实 JSON 逐字节不变（不做可见性过滤）。
#   末段再加一组源码级反证（零写盘 / 零 commit / 零子进程 / 零关系写 API / 零新造 ID 规则 /
#   四个 check 值恰 4 / 命令本体零注册 / F4 仍恰 8 值）。
#
# 为什么用 `go test` 驱动而不是 `eg check`：
#   `eg check` / `eg reconcile` 命令本体、信封与退出码属 T-…-059 / T-…-058，本 task 明确
#   不注册命令，只交付 R3 四项检查器。脚本因此在真实临时 vault 上用
#   `test/e2e/m4_r3_relation_test.go` 这一层驱动壳调检查器（同 `m4_r1_takeover.sh` /
#   `m4_r4_structure.sh` 的先例），事实一律回读驱动壳落盘的 JSON 与 git 自己，
#   不看实现自报。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / python3）；
# **一切写操作只发生在 mktemp -d 目录内**；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/relation/relation_reconcile.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-r3.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

KDIR="${VAULT}/domains/ai-infra/knowledge"
NDIR="${VAULT}/domains/ai-infra/notes"

# 四张卡的稳定 ID（字典序关系是 W15 判定的前提：ALPHA < BETA < GAMMA < ZETA）。
CARD_A="k-20261123-alpha"
CARD_B="k-20261123-beta"
CARD_C="k-20261124-gamma"
CARD_Z="k-20261125-zeta"
GONE="k-20261199-gone"      # 形态合法但库内不存在 → E13
BAD_PREFIX="n-20261123-attention" # 笔记 ID 当关系 target → E14（前缀非法优先于存在性）

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
# cards_sum：四张卡文件的 sha256（frontmatter 字节的逐字反证面）。
cards_sum() { sha256sum "${KDIR}"/*.md | sort; }
# rel_entries：全库 relations[] 条目数（`- type:` 行数），零自动修的字节级反证。
rel_entries() { grep -rc '^  - type:' "${KDIR}"/*.md | awk -F: '{s+=$2} END {print s+0}'; }
# seal <说明>：把造好的数据提交进 vault 自己的仓库，使「检查前工作区为空」成立。
seal() { gitv add -A >/dev/null && gitv -c user.name=e2e -c user.email=e2e@local \
  commit -q -m "e2e(r3): $1" >/dev/null; }

# drive <标签>：在真实 vault 上跑驱动壳，事实落 ${FACTS}（另存一份带标签的副本）。
drive() {
  local tag="$1"
  rm -f "${FACTS}"
  ( cd "$(eg_staged_root)" &&
    EG_M4_R3_VAULT="${VAULT}" EG_M4_R3_OUT="${FACTS}" \
    go test ./test/e2e/ -run TestM4R3RelationHarness -count=1 -v </dev/null ) \
    >"${WORK}/drive-${tag}.log" 2>&1 ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未全绿"; }
  grep -Fq -- "--- PASS: TestM4R3RelationHarness" "${WORK}/drive-${tag}.log" ||
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
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# card_file <ID> [relations 条目段]：写一张知识卡（sources 四要素齐备，避免掺入 R4 的判定）。
# 第二参数省略或为空 → 落 `relations: []`（空序列写成行内形式，与既有 e2e 造数逐字同形）。
card_file() {
  local id="$1" rels="${2-}"
  if [ -z "${rels}" ]; then rels="relations: []"; else rels="relations:
${rels}"; fi
  cat >"${KDIR}/${id}.md" <<CARD
---
id: ${id}
title: 关系校验用卡 ${id}
status: active
created_at: '2026-11-23'
updated_at: '2026-11-23T10:00:00+08:00'
sources:
  - source: s-20261123-attention
    note: n-20261123-attention
    rel: support
    reason: 该结论由这份材料支持
${rels}
---

## 知识内容

R3 只读关系校验的端到端数据（本卡正文不参与任何判定）。
CARD
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 干净数据
step "建库并造干净数据：4 张互连卡（含一条**规范方向**的 opposing）+ 1 原文 + 1 笔记"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${KDIR}" "${NDIR}" "${VAULT}/sources"
cat >"${VAULT}/sources/s-20261123-attention.md" <<'SRC'
---
id: s-20261123-attention
url: https://example.com/relations
title: 关系校验原文
saved_at: '2026-11-23T09:00:00+08:00'
---

# 关系校验原文

正文占位（收录后不因加工改写）。
SRC
cat >"${NDIR}/n-20261123-attention.md" <<'NOTE'
---
id: n-20261123-attention
source: s-20261123-attention
created_at: '2026-11-23'
updated_at: '2026-11-23T10:00:00+08:00'
---

# 关系校验材料笔记

## 材料提炼

提炼占位。
NOTE
card_file "${CARD_A}" "  - type: supports
    target: ${CARD_B}
    reason: 支持 beta 那张卡的结论"
# opposing 落在两端 ID 字典序**较小**的一端（BETA < GAMMA）→ 规范方向，**不得**报 W15。
card_file "${CARD_B}" "  - type: opposing
    target: ${CARD_C}
    reason: 与 gamma 那张卡的结论对立（规范方向：写在字典序较小的一端）"
card_file "${CARD_C}" "  - type: derives
    target: ${CARD_Z}
    reason: 由 zeta 那张卡推导而来"
card_file "${CARD_Z}"
seal "干净数据"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库（工作区为空）"; }
CLEAN_COMMITS="$(commits)"
ok "干净 vault 就绪：commit 数 ${CLEAN_COMMITS}，工作区为空，关系条目 $(rel_entries) 条"

# ---------------------------------------------------------------- 2. 干净库：零 finding
step "干净库跑只读检查：零 finding、零 RepairSpec、退出码 0（规范单向 opposing 不误报 W15）"
CLEAN_SUM="$(tree_sum)"
drive clean
want_num cards 4
want_num relation_entries 3
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "干净库不得有 finding"; }
for k in relation_target_missing relation_prefix_invalid relation_opposing_asymmetric \
  relation_duplicate duplicate_id dangling_ref orphan; do want_num "${k}" 0; done
grep -Fq '"has_error":false' "${FACTS}" || { cat "${FACTS}"; die "干净库不得有 error 级 finding"; }
want_num exit_code 0
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R3 恒零 RepairSpec"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后工作区应仍为空"; }
[ "${CLEAN_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
ok "干净库：0 finding / 0 repair / exit 0 / 零写入（单向 opposing 是规范形态）"

# ---------------------------------------------------------------- 3. 外部编辑造四处关系异常
step "外部编辑造四处异常：target 不存在 / target 前缀非法 / 非规范方向 opposing / 重复三元组"
card_file "${CARD_A}" "  - type: supports
    target: ${CARD_B}
    reason: 支持 beta 那张卡的结论
  - type: supports
    target: ${GONE}
    reason: 外部编辑绕过写侧校验，指向一张不存在的卡（→ E13）
  - type: derives
    target: ${BAD_PREFIX}
    reason: 外部编辑把笔记 ID 当成了关系 target（→ E14）"
# opposing 写在字典序**较大**的一端（ZETA > ALPHA）且规范方向缺失 → W15。
card_file "${CARD_Z}" "  - type: opposing
    target: ${CARD_A}
    reason: 外部编辑把 opposing 写在了非规范的一端（→ W15）"
# 同一 (from,type,target) 写了两条 → W16（写侧幂等去重只在写侧生效）。
card_file "${CARD_C}" "  - type: derives
    target: ${CARD_Z}
    reason: 由 zeta 那张卡推导而来
  - type: derives
    target: ${CARD_Z}
    reason: 外部编辑重复写了同一条边（→ W16）"
seal "四处关系异常"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库"; }
DIRTY_COMMITS="$(commits)"
DIRTY_SUM="$(tree_sum)"
DIRTY_CARDS="$(cards_sum)"
DIRTY_ENTRIES="$(rel_entries)"
[ "${DIRTY_ENTRIES}" = "7" ] || die "造数后关系条目应为 7 条，实得 ${DIRTY_ENTRIES}"
ok "异常数据入库：commit 数 ${DIRTY_COMMITS}，关系条目 ${DIRTY_ENTRIES} 条，工作区为空"

# ---------------------------------------------------------------- 4. 四码各恰 1 条
step "只读检查：E13 / E14 / W15 / W16 各恰 1 条、单射码与 severity 正确、退出码 2（A-31）"
drive dirty
want_num relation_target_missing 1
want_num relation_prefix_invalid 1
want_num relation_opposing_asymmetric 1
want_num relation_duplicate 1
[ "$(fact_count '"check":')" = "4" ] || { cat "${FACTS}"; die "finding 总数应恰 4"; }
for code in E13 E14 W15 W16; do
  [ "$(fact_count "\"${code}\"")" = "1" ] ||
    { cat "${FACTS}"; die "${code} 应恰 1 次（合同 §3 单射）"; }
done
[ "$(fact_count '"severity":"error"')" = "2" ] ||
  { cat "${FACTS}"; die "error 级应恰 2 条（E13 + E14）"; }
[ "$(fact_count '"severity":"warning"')" = "2" ] ||
  { cat "${FACTS}"; die "warning 级应恰 2 条（W15 + W16）"; }
# 四条 finding 的 targets 逐字可复算（合同 §2：去重 + 字典序升序）。
grep -Fq "\"targets\":[\"${CARD_A}\",\"${GONE}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "E13 的 targets 应为 [引用方, 缺失目标] 的升序对"; }
grep -Fq "\"targets\":[\"${CARD_A}\",\"${BAD_PREFIX}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "E14 的 targets 应为 [引用方, 非法 target] 的升序对"; }
grep -Fq "\"targets\":[\"${CARD_A}\",\"${CARD_Z}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "W15 的 targets 应为规范化后的 [较小端, 较大端]"; }
grep -Fq "\"targets\":[\"${CARD_C}\",\"${CARD_Z}\"]" "${FACTS}" ||
  { cat "${FACTS}"; die "W16 的 targets 应为规范化后的 [from, target]"; }
# W15 的 detail 必须写清「规范方向缺失」这一可复算事实（A-24 口径）。
grep -Fq '规范方向' "${FACTS}" || { cat "${FACTS}"; die "W15 的 detail 未写明规范方向口径"; }
grep -Fq '出现 2 条记录' "${FACTS}" || { cat "${FACTS}"; die "W16 的 detail 未写明重复条目数"; }
grep -Fq '"has_error":true' "${FACTS}" || { cat "${FACTS}"; die "E13 / E14 存在时 has_error 应为 true"; }
want_num exit_code 2
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R3 只报告，恒零 RepairSpec"; }
ok "四码各恰 1 条、targets 逐字可复算、exit 2、零 RepairSpec"

# ---------------------------------------------------------------- 5. 与 R4 零重复计数
step "与 R4 分工：dangling_ref（E12）/ duplicate_id（E11）/ orphan（W17）恒 0，一件事只报一码"
want_num dangling_ref 0
want_num duplicate_id 0
want_num orphan 0
cnt0 '关系 target 缺失被 E12 重复计一次' -F '"E12"' "${FACTS}"
cnt0 '越界产出 R4 / R5 / R6 / R7 的 check' -E \
  '"(duplicate_id|dangling_ref|orphan|domain_moved|recap_stale|support_insufficient|git_uncommitted|reviewed_at_missing)"' \
  <(grep -o '"check":"[a-z_]*"' "${FACTS}")
ok "E11 / E12 / W17 恒 0；关系 target 缺失只被 E13 记一次（合同 §6.2 末句）"

# ---------------------------------------------------------------- 6. 零自动修 + 幂等
step "零自动修与幂等：git status 空、commit 数不变、条目数逐卡不变、frontmatter 字节不变"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后 git status --porcelain 应为空"; }
[ "$(commits)" = "${DIRTY_COMMITS}" ] || die "只读检查改变了 commit 数（R3 零 commit）"
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了（R3 零写盘、零去重、零移除）"
[ "${DIRTY_CARDS}" = "$(cards_sum)" ] || die "四张卡的 frontmatter 字节被改动（R3 零自动修）"
[ "${DIRTY_ENTRIES}" = "$(rel_entries)" ] ||
  die "relations[] 条目数被改动：${DIRTY_ENTRIES} → $(rel_entries)（不补反向、不去重、不移除）"
want_num relation_entries 7
grep -Fq "\"relations_by_card\":[\"${CARD_A}=3\",\"${CARD_B}=1\",\"${CARD_C}=2\",\"${CARD_Z}=1\"]" \
  "${FACTS}" || { cat "${FACTS}"; die "逐卡关系条目数与落盘事实不符"; }
drive dirty2
cmp -s "${WORK}/facts-dirty.json" "${WORK}/facts-dirty2.json" ||
  { diff "${WORK}/facts-dirty.json" "${WORK}/facts-dirty2.json" || true;
    die "两次检查的事实不同：R3 必须可逐字复算"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "第二次检查后工作区应仍为空"; }
[ "${DIRTY_SUM}" = "$(tree_sum)" ] || die "第二次检查写盘了"
[ "${DIRTY_ENTRIES}" = "$(rel_entries)" ] || die "第二次检查改动了关系条目数"
ok "零写盘零 commit（sha256 逐字不变）+ 条目数逐卡不变 + 两次事实逐字节相同"

# ---------------------------------------------------------------- 7. 修好即归零
step "四处修好（含把 opposing 搬回规范方向）→ finding 归零、退出码回 0（判定条件可逆）"
card_file "${CARD_A}" "  - type: supports
    target: ${CARD_B}
    reason: 支持 beta 那张卡的结论
  - type: opposing
    target: ${CARD_Z}
    reason: 把 opposing 搬回规范方向（写在字典序较小的一端）"
card_file "${CARD_Z}"
card_file "${CARD_C}" "  - type: derives
    target: ${CARD_Z}
    reason: 由 zeta 那张卡推导而来"
seal "四处关系异常修好"
drive fixed
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "四处修好后应零 finding"; }
grep -Fq '"has_error":false' "${FACTS}" || { cat "${FACTS}"; die "零 finding 时 has_error 应为 false"; }
want_num exit_code 0
# 修法是「删 3 条脏条目 + 把 opposing 搬到规范端」：7 - 3 = 4 条，逐卡分布同样可复算。
want_num relation_entries 4
grep -Fq "\"relations_by_card\":[\"${CARD_A}=2\",\"${CARD_B}=1\",\"${CARD_C}=1\",\"${CARD_Z}=0\"]" \
  "${FACTS}" || { cat "${FACTS}"; die "修好后逐卡关系条目数与落盘事实不符"; }
ok "四处修好 → 0 finding / exit 0（E13 / E14 / W15 / W16 的判定条件均可逆）"

# ---------------------------------------------------------------- 8. 存在性看落盘事实
step "存在性解耦可见性：把 opposing 对端标成 deprecated → 事实 JSON 逐字节不变"
python3 - "${KDIR}/${CARD_Z}.md" <<'PY'
import sys
p = sys.argv[1]
s = open(p, encoding='utf-8').read()
# 只改 status 一个键：把 alpha 的 opposing 对端标成失效（deleted_at 保持不存在）。
s = s.replace("status: active", "status: deprecated", 1)
open(p, 'w', encoding='utf-8').write(s)
PY
grep -Fq 'status: deprecated' "${KDIR}/${CARD_Z}.md" || die "未能把对端标成 deprecated"
cnt0 '对端被误加删除标记' -F 'deleted_at' "${KDIR}/${CARD_Z}.md"
seal "把 opposing 对端标成 deprecated"
drive deprecated
cmp -s "${WORK}/facts-fixed.json" "${WORK}/facts-deprecated.json" ||
  { diff "${WORK}/facts-fixed.json" "${WORK}/facts-deprecated.json" || true;
    die "端点失效改变了 R3 判定：存在性必须只看落盘事实，不做可见性过滤"; }
ok "端点 deprecated 不改变四码判定（存在性只看落盘事实）"

# ---------------------------------------------------------------- 9. 源码级边界反证
step "边界反证：检查侧三条零 / 零关系写 API / 零新造 ID 规则 / 四 check 恰 4 / 命令零注册"
R3SRC="${REPO_ROOT}/internal/reconcile/r3_relation.go"
cnt0 '检查侧写盘或提交' -rnE 'os\.WriteFile|os\.Create|os\.Remove|os\.Rename|os\.OpenFile|Commit\(' "${R3SRC}"
cnt0 '检查侧起子进程' -rnE 'os/exec|exec\.Command' "${R3SRC}"
cnt0 '检查侧调关系写 API' -rnE 'AddRelation|RemoveRelation|SetRelation' "${R3SRC}"
cnt0 '检查侧越界依赖' -rnE 'internal/(store|plan|cli|report|proposal|rules)' "${R3SRC}"
cnt0 '检查侧自造 ID 前缀规则' -rnE 'regexp\.MustCompile|regexp\.' "${R3SRC}"
cnt0 '检查侧出现越界能力' -rnE '\.index/|FTS5|[Ss][Qq][Ll]ite|flock|P95' "${REPO_ROOT}/internal/reconcile/"
[ "$(grep -oE 'relation_target_missing|relation_prefix_invalid|relation_opposing_asymmetric|relation_duplicate' \
  "${R3SRC}" | sort -u | wc -l | tr -d ' ')" = "4" ] || die "四个 check 值应恰 4 个不同取值"
[ "$(grep -cE 'CheckRelationTargetMissing|CheckRelationPrefixInvalid|CheckRelationOpposingAsymmetric|CheckRelationDuplicate' \
  "${R3SRC}")" -ge 4 ] || die "四项 check 必须引用 check.go 的枚举真源（不得自写字面量）"
[ "$(grep -cE 'internal/model' "${R3SRC}")" -ge 1 ] ||
  die "前缀校验必须复用 internal/model 的现成规则（零新造规则）"
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
# F4 仍恰 8 值封闭：材料 3 + 论证 4 + 生命周期 1（R3 不新增关系类型、不新增关系字段）。
[ "$(grep -c 'F4RelationValueCount = 3 + 4 + 1' "${R3SRC}")" = "1" ] ||
  die "F4 的 8 值构成必须逐字在册（材料 3 + 论证 4 + 生命周期 1）"
cnt0 'F4 集合外的关系类型残留' -rnE 'refines|contradicts|depends_on|removed_at|RelationID' "${R3SRC}"
ok "检查侧零写盘 / 零 commit / 零子进程 / 零关系写 API / 零正则；四 check 恰 4；命令零注册"

printf '\n=== relation_reconcile.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
