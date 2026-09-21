#!/usr/bin/env bash
# R5 手工跨领域移动检测（`domain_moved` / W18 / warning，只报告）的端到端脚本
# （T-evergreen.s1_main_flow-158614-054）。
#
# 判据来源：`docs/specs/2026-11-12-m4-reconcile-contract.md` §8（R5 全文：两条判定、
# 「只报告，不移动文件、不改领域字段、不补关系」、`targets[]` = `[对象 ID, 旧领域, 新领域]`
# 恰三元顺序固定）、§1.1（对账包只读三条零）、§1.3 写口归属表第 4 行（R3 / R4 / R5 / R7 无人写）、
# §2（Finding 四键）、§3 单射表第 10 行（`domain_moved` ↔ `W18` ↔ warning）、§15（明确不做）；
# `docs/specs/2026-11-15-m4-prestart-adjudication.md` 的 **A-31**（有 error 级 finding 退 2 ——
# W18 是 warning，因此单有 W18 时退出码恒 0）；`milestones/M-004-m4.md` 完成判据 7。
#
# 九段断言（task deliverable 逐字要求）：
#   ① 造一个**干净**的小 vault（1 原文 / 1 笔记 / 3 张互连卡，全部由 `eg` 口径的提交入库）
#      → 只读检查零 finding、零 rename 事实、退出码 0；
#   ② 用 `git mv` 把一张卡**跨领域**移动并以**非 `eg` verb** 提交 → 只读检查
#      **恰 1 条 W18**、severity=warning、`targets` 长度恰 3 且第 2 / 3 元为旧 / 新领域、
#      固定三元 ≠ 字典序（真的走了有序例外）、退出码 0、零 RepairSpec；
#   ③ **只报告**：检查前后文件路径与 frontmatter 字节 sha256 逐字不变、
#      `git status --porcelain` 恒空、commit 数不变、`relations[]` 条目数不变、
#      被移动的卡里**没有**被补出 `domain:` 键；
#   ④ 幂等可复算：同一 vault 连跑两次，事实 JSON 逐字节相同；
#   ⑤ **`eg` 自身产生的跨领域 rename 零命中**：另造一次以 `eg` 已知 verb 提交的跨领域移动
#      → W18 条数**一字不变**（仍恰 1 条，且新卡不在移动清单里）；
#   ⑥ 同领域内改名零命中：以非 `eg` verb 在同一领域内改名 → W18 条数一字不变；
#   ⑦ 与 R1 / R2 / R3 / R4 零重复计数：同一份数据里 `git_uncommitted` / `reviewed_at_missing` /
#      `duplicate_id` / `dangling_ref` / `orphan` / 四个 relation 码恒 0；
#   ⑧ 判定可逆：用 `eg` 口径的提交把卡归位 → W18 归零、退出码回 0；
#   ⑨ 源码级边界反证（对账包三条零 / 零关系写 API / 零 verb 字面量 / `internal/git` 零写方法 /
#      命令本体零注册 / op 数恒 16 / `content_hash` grep 不减）。
#
# 为什么用 `go test` 驱动而不是 `eg check`：
#   `eg check` / `eg reconcile` 命令本体、信封与退出码属 T-…-059 / T-…-058，本 task 明确
#   不注册命令，只交付 R5 检查器。脚本因此在真实临时 vault 上用
#   `test/e2e/m4_r5_domain_moved_test.go` 这一层驱动壳调检查器（同 `m4_r1_takeover.sh` /
#   `m4_r3_relation.sh` 的先例），事实一律回读驱动壳落盘的 JSON 与 git 自己，不看实现自报。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go）；
# **一切写操作只发生在 mktemp -d 目录内**；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/reconcile-takeover/domain_moved.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-r5.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
FACTS="${WORK}/facts.json"

DOM_A="ai-infra"   # 卡的原领域
DOM_B="platform"   # 手工移动后的新领域
KDIR="${VAULT}/domains/${DOM_A}/knowledge"
NDIR="${VAULT}/domains/${DOM_A}/notes"
KDIR2="${VAULT}/domains/${DOM_B}/knowledge"

CARD_A="k-20261126-alpha"  # 被**手工**跨领域移动的卡（→ 恰 1 条 W18）
CARD_B="k-20261126-beta"   # 被 **eg 自己**跨领域移动的卡（→ 零命中）
CARD_C="k-20261126-gamma"  # 只在同领域内改名的卡（→ 零命中）
NOTE_ID="n-20261126-attention"
SRC_ID="s-20261126-attention"

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
# cards_sum：全部知识卡文件的 sha256（frontmatter 字节的逐字反证面）。
cards_sum() { find "${VAULT}/domains" -name '*.md' -type f -print0 | xargs -0 sha256sum | sort; }
# rel_entries：全库 relations[] 条目数（`- type:` 行数），只报告的字节级反证。
rel_entries() { find "${VAULT}/domains" -name '*.md' -type f -print0 |
  xargs -0 grep -c '^  - type:' 2>/dev/null | awk -F: '{s+=$2} END {print s+0}'; }
# seal <verb> <说明>：以指定 verb 的主题行提交（verb 是 R5 条件② 的判定输入）。
seal() { gitv add -A >/dev/null && gitv -c user.name=e2e -c user.email=e2e@local \
  commit -q -m "$1: $2" >/dev/null; }

# drive <标签>：在真实 vault 上跑驱动壳，事实落 ${FACTS}（另存一份带标签的副本）。
drive() {
  local tag="$1"
  rm -f "${FACTS}"
  ( cd "$(eg_staged_root)" &&
    EG_M4_R5_VAULT="${VAULT}" EG_M4_R5_OUT="${FACTS}" \
    go test ./test/e2e/ -run TestM4R5DomainMovedHarness -count=1 -v </dev/null ) \
    >"${WORK}/drive-${tag}.log" 2>&1 ||
    { tail -30 "${WORK}/drive-${tag}.log"; die "驱动壳（${tag}）未全绿"; }
  grep -Fq -- "--- PASS: TestM4R5DomainMovedHarness" "${WORK}/drive-${tag}.log" ||
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
cnt0() { # cnt0 <说明> <grep 参数...>：期望 0 命中
  local name="$1"; shift
  local n; n="$( { grep "$@" || true; } | wc -l | tr -d ' ')"
  [ "${n}" = "0" ] || { grep "$@" || true; die "${name}：期望 0 命中，实得 ${n}"; }
}

# card_file <目录> <ID> [relations 条目段]：写一张知识卡（sources 四要素齐备，
# 避免掺入 R4 的判定；**不含 `domain` 键**——领域由目录唯一决定，EG-DOM-01）。
card_file() {
  local dir="$1" id="$2" rels="${3-}"
  if [ -z "${rels}" ]; then rels="relations: []"; else rels="relations:
${rels}"; fi
  cat >"${dir}/${id}.md" <<CARD
---
id: ${id}
title: 跨领域移动检测用卡 ${id}
status: active
created_at: '2026-11-26'
updated_at: '2026-11-26T10:00:00+08:00'
reviewed_at: '2026-11-26T10:00:00+08:00'
sources:
  - source: ${SRC_ID}
    note: ${NOTE_ID}
    rel: support
    reason: 该结论由这份材料支持
${rels}
---

## 知识内容

R5 手工跨领域移动检测的端到端数据（本卡正文不参与任何判定）。
CARD
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 干净数据
step "建库并造干净数据：3 张互连卡 + 1 原文 + 1 笔记，全部以 eg 口径的提交入库"
[ "$(eg_code init --domain "${DOM_A}")" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain "${DOM_A}")" = "0" ] || die "config set 失败"
mkdir -p "${KDIR}" "${NDIR}" "${KDIR2}" "${VAULT}/domains/${DOM_B}/notes" "${VAULT}/sources"
cat >"${VAULT}/sources/${SRC_ID}.md" <<SRC
---
id: ${SRC_ID}
url: https://example.com/domain-move
title: 跨领域移动原文
saved_at: '2026-11-26T09:00:00+08:00'
---

# 跨领域移动原文

正文占位（收录后不因加工改写）。
SRC
cat >"${NDIR}/${NOTE_ID}.md" <<NOTE
---
id: ${NOTE_ID}
source: ${SRC_ID}
created_at: '2026-11-26'
updated_at: '2026-11-26T10:00:00+08:00'
reviewed_at: '2026-11-26T10:00:00+08:00'
---

# 跨领域移动材料笔记

## 材料提炼

提炼占位。
NOTE
# alpha 出边指向 beta 与 gamma → 三张卡都不是零关系卡（避免掺入 R4 的 W17）。
card_file "${KDIR}" "${CARD_A}" "  - type: supports
    target: ${CARD_B}
    reason: 支持 beta 那张卡的结论
  - type: derives
    target: ${CARD_C}
    reason: 由 gamma 那张卡推导而来"
card_file "${KDIR}" "${CARD_B}"
card_file "${KDIR}" "${CARD_C}"
seal "process(${DOM_A})" "收录三张卡与一篇笔记"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "造数后应已入库（工作区为空）"; }
CLEAN_COMMITS="$(commits)"
CLEAN_ENTRIES="$(rel_entries)"
[ "${CLEAN_ENTRIES}" = "2" ] || die "造数后关系条目应为 2 条，实得 ${CLEAN_ENTRIES}"
ok "干净 vault 就绪：commit 数 ${CLEAN_COMMITS}，工作区为空，关系条目 ${CLEAN_ENTRIES} 条"

# ---------------------------------------------------------------- 2. 干净库：零 finding
step "干净库跑只读检查：零 finding、零 rename 事实、退出码 0（没移动过就不该报）"
CLEAN_SUM="$(tree_sum)"
drive clean
want_num cards 3
want_num notes 1
want_num domain_moved 0
want_num renames_sampled 0
want_num cross_domain_renames 0
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "干净库不得有 finding"; }
want_lit "干净库不得有 error 级 finding" '"has_error":false'
want_num exit_code 0
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R5 恒零 RepairSpec"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后工作区应仍为空"; }
[ "${CLEAN_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了"
ok "干净库：0 finding / 0 repair / exit 0 / 零写入"

# ---------------------------------------------------------------- 3. 手工跨领域移动
step "用 git mv 把 alpha 从 ${DOM_A} 挪到 ${DOM_B} 并以**非 eg verb** 提交（外部编辑）"
gitv mv "domains/${DOM_A}/knowledge/${CARD_A}.md" "domains/${DOM_B}/knowledge/${CARD_A}.md"
seal "mv(${DOM_B})" "手工把知识卡挪到别的领域（外部编辑，非 eg 产生）"
[ -f "${KDIR2}/${CARD_A}.md" ] || die "git mv 未生效"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "移动后应已入库"; }
MOVED_COMMITS="$(commits)"
MOVED_SUM="$(tree_sum)"
MOVED_CARDS="$(cards_sum)"
MOVED_ENTRIES="$(rel_entries)"
ok "手工跨领域移动已入库：commit 数 ${MOVED_COMMITS}，关系条目 ${MOVED_ENTRIES} 条"

step "只读检查：恰 1 条 W18 / warning，targets 恰三元 [ID, 旧领域, 新领域] 顺序固定，退出码 0"
drive moved
want_num domain_moved 1
[ "$(fact_count '"check":')" = "1" ] || { cat "${FACTS}"; die "finding 总数应恰 1"; }
want_lit "check 值必须逐字是 domain_moved" '"check":"domain_moved"'
[ "$(fact_count '"W18"')" = "1" ] || { cat "${FACTS}"; die "W18 应恰 1 次（合同 §3 单射）"; }
[ "$(fact_count '"severity":"warning"')" = "1" ] ||
  { cat "${FACTS}"; die "W18 的 severity 必须是 warning"; }
cnt0 'W18 被误判成 error 级' -F '"severity":"error"' "${FACTS}"
# 三元顺序固定：逐字断言 [对象 ID, 旧领域, 新领域]（脚本按位取值的根据）。
want_lit "targets 必须是固定三元 [ID, 旧领域, 新领域]" \
  "\"targets\":[\"${CARD_A}\",\"${DOM_A}\",\"${DOM_B}\"]"
want_lit "targets 元数必须恰 3" '"targets_arity":[3]'
want_lit "固定三元必须 ≠ 字典序（否则钉不住顺序）" '"ordered_not_sorted":true'
want_lit "移动事实与 finding 同源，且证据是跨领域 rename" \
  "\"${CARD_A}|${DOM_A}|${DOM_B}|cross_domain_rename\""
want_lit "detail 必须写明只报告边界" '只报告'
want_lit "detail 必须写明事实口径" 'git log --follow --name-status'
want_lit "W18 是 warning，has_error 必须为 false" '"has_error":false'
want_num exit_code 0
grep -Eq '"repairs":(\[\]|null)' "${FACTS}" || { cat "${FACTS}"; die "R5 只报告，恒零 RepairSpec"; }
[ "$(fact_num cross_domain_renames)" -ge 1 ] ||
  { cat "${FACTS}"; die "应至少采到 1 条跨领域 rename 事实"; }
want_lit "rename 事实必须逐字可复算（旧路径|新路径|verb）" \
  "\"domains/${DOM_A}/knowledge/${CARD_A}.md|domains/${DOM_B}/knowledge/${CARD_A}.md|mv\""
ok "恰 1 条 W18 / warning、targets 三元顺序固定且非字典序、exit 0、零 RepairSpec"

# ---------------------------------------------------------------- 4. 只报告（零动作）
step "只报告：零写盘 / 零 commit / 不搬回文件 / 不改领域字段 / 不补关系"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "只读检查后 git status --porcelain 应为空"; }
[ "$(commits)" = "${MOVED_COMMITS}" ] || die "只读检查改变了 commit 数（R5 零 commit）"
[ "${MOVED_SUM}" = "$(tree_sum)" ] || die "只读检查写盘了（R5 零写盘、零移动）"
[ "${MOVED_CARDS}" = "$(cards_sum)" ] || die "卡文件字节被改动（R5 不改领域字段）"
[ -f "${KDIR2}/${CARD_A}.md" ] || die "卡被搬回原领域：R5 不移动文件"
[ ! -f "${KDIR}/${CARD_A}.md" ] || die "卡被复制回原领域：R5 不移动文件"
[ "${MOVED_ENTRIES}" = "$(rel_entries)" ] ||
  die "relations[] 条目数被改动：${MOVED_ENTRIES} → $(rel_entries)（R5 不补关系）"
want_num relation_entries 2
cnt0 'frontmatter 被补出 domain 键' -n '^domain:' "${KDIR2}/${CARD_A}.md"
ok "零写盘 / 零 commit / 文件仍在新领域 / 无 domain 键 / 关系条目不变"

step "幂等可复算：同一 vault 连跑两次，事实 JSON 逐字节相同"
drive moved2
cmp -s "${WORK}/facts-moved.json" "${WORK}/facts-moved2.json" ||
  { diff "${WORK}/facts-moved.json" "${WORK}/facts-moved2.json" || true;
    die "两次检查的事实不同：R5 必须可逐字复算"; }
[ "$(porcelain_count)" = "0" ] || { porcelain; die "第二次检查后工作区应仍为空"; }
[ "${MOVED_SUM}" = "$(tree_sum)" ] || die "第二次检查写盘了"
ok "两次事实逐字节相同 + 第二次仍零写入"

# ---------------------------------------------------------------- 5. eg 自身 rename 零命中
step "eg 自身产生的跨领域 rename：以 eg 已知 verb 提交 → 零命中（W18 条数一字不变）"
gitv mv "domains/${DOM_A}/knowledge/${CARD_B}.md" "domains/${DOM_B}/knowledge/${CARD_B}.md"
seal "reconcile(${DOM_B})" "eg 自身产生的跨领域 rename（已知 verb）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "移动后应已入库"; }
drive egown
want_num domain_moved 1
cnt0 'eg 自身移动的卡被误报' -F "\"${CARD_B}|" "${FACTS}"
want_lit "只有手工移动的那张卡在移动清单里" "\"${CARD_A}|${DOM_A}|${DOM_B}|cross_domain_rename\""
[ "$(fact_num cross_domain_renames)" -ge 2 ] ||
  { cat "${FACTS}"; die "两次跨领域 rename 都该被采到（判定差别只在 verb 归属）"; }
want_lit "eg 自身那条 rename 事实同样被采到（采样不挑 verb）" \
  "\"domains/${DOM_A}/knowledge/${CARD_B}.md|domains/${DOM_B}/knowledge/${CARD_B}.md|reconcile\""
want_num exit_code 0
ok "eg 自身 rename 零命中（verb 归属复用 KnownVerbs 单一真源），W18 仍恰 1 条"

# ---------------------------------------------------------------- 6. 同领域改名零命中
step "同领域内改名（非 eg verb）：零命中 —— 改名不是跨领域移动"
gitv mv "domains/${DOM_A}/knowledge/${CARD_C}.md" "domains/${DOM_A}/knowledge/${CARD_C}-v2.md"
seal "chore(${DOM_A})" "手工在同一领域内改名（不跨领域）"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "改名后应已入库"; }
drive samedomain
want_num domain_moved 1
cnt0 '同领域改名被误报' -F "\"${CARD_C}|" "${FACTS}"
want_lit "同领域 rename 事实被采到但不命中" \
  "\"domains/${DOM_A}/knowledge/${CARD_C}.md|domains/${DOM_A}/knowledge/${CARD_C}-v2.md|chore\""
ok "同领域改名零命中（判定看的是领域段是否变化，不是路径是否变化）"

# ---------------------------------------------------------------- 7. 不重复计数
step "与 R1 / R2 / R3 / R4 零重复计数：其余七项条数恒 0，一件事只被 W18 记一次"
want_num git_uncommitted 0
want_num reviewed_at_missing 0
want_num duplicate_id 0
want_num dangling_ref 0
want_num orphan 0
want_num relation_findings 0
cnt0 '越界产出 R1 / R2 / R3 / R4 / R6 / R7 的 check' -E \
  '"(git_uncommitted|reviewed_at_missing|duplicate_id|dangling_ref|orphan|relation_[a-z_]*|recap_stale|support_insufficient)"' \
  <(grep -o '"check":"[a-z_]*"' "${FACTS}")
[ "$(fact_count '"check":')" = "1" ] ||
  { cat "${FACTS}"; die "finding 总数应仍恰 1（三次移动 / 改名只产 1 条 W18）"; }
ok "七项恒 0；跨领域移动这件事只被 W18 记一次"

# ---------------------------------------------------------------- 8. 判定可逆
step "判定可逆：用 eg 口径的提交把 alpha 归位 → W18 归零、退出码回 0"
gitv mv "domains/${DOM_B}/knowledge/${CARD_A}.md" "domains/${DOM_A}/knowledge/${CARD_A}.md"
seal "reconcile(${DOM_A})" "eg 把手工移动的卡归位"
[ "$(porcelain_count)" = "0" ] || { porcelain; die "归位后应已入库"; }
drive fixed
want_num domain_moved 0
[ "$(fact_count '"check":')" = "0" ] || { cat "${FACTS}"; die "归位后应零 finding"; }
want_lit "零 finding 时 has_error 应为 false" '"has_error":false'
want_num exit_code 0
[ "$(fact_num cross_domain_renames)" -ge 3 ] ||
  { cat "${FACTS}"; die "历史里的跨领域 rename 事实不该消失（判定变了，事实没变）"; }
ok "归位后 W18 归零 / exit 0（判定条件可逆，且历史事实仍如实采到）"

# ---------------------------------------------------------------- 9. 源码级边界反证
step "边界反证：对账包三条零 / 零关系写 API / 零 verb 字面量 / git 包零写方法 / 命令零注册"
R5SRC="${REPO_ROOT}/internal/reconcile/r5_domain.go"
GITSRC="${REPO_ROOT}/internal/git/log.go"
cnt0 '检查侧写盘或提交' -rnE 'os\.WriteFile|os\.Create|os\.Remove|os\.Rename|os\.OpenFile|Commit\(' "${R5SRC}"
cnt0 '检查侧起子进程' -rnE 'os/exec|exec\.Command' "${R5SRC}"
cnt0 '检查侧调关系写 API' -rnE 'AddRelation|RemoveRelation|SetRelation' "${R5SRC}"
cnt0 '检查侧越界依赖' -rnE 'internal/(store|plan|cli|report|proposal|rules)' "${R5SRC}"
cnt0 '检查侧自写 verb 字面量清单' -rnE '"(init|reconcile|capture|process|reprocess|relate|proposal|delete)"' "${R5SRC}"
[ "$(grep -cE 'KnownVerbs|IsForeignVerb' "${R5SRC}")" -ge 1 ] ||
  die "verb 归属判定必须复用单一真源（KnownVerbs / IsForeignVerb）"
[ "$(grep -cE 'log --follow|--name-status' "${R5SRC}")" -ge 1 ] ||
  die "条件② 的事实口径必须逐字在册"
# check 值只许引用 check.go 的枚举真源：源码里不得出现带引号的 `"domain_moved"` 字面量
# （注释里逐字提到 check 名是文档需要，不是判定输入）。
cnt0 'check 值自写字面量' -nE '"domain_moved"' "${R5SRC}"
[ "$(grep -c 'CheckDomainMoved' "${R5SRC}")" -ge 1 ] || die "必须引用 CheckDomainMoved 枚举"
# internal/git 只扩只读参数面：零写方法、零撤销类能力。
cnt0 'git 包新增写方法' -rnE 'func .*\b(Commit|Add|Push|Reset|Checkout)\(' "${GITSRC}"
[ "$(grep -cE '\-\-follow|\-\-name-status' "${GITSRC}")" -ge 2 ] ||
  die "internal/git 必须提供 --follow / --name-status 只读参数面"
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
# 计数面不动：op 全集不缩、content_hash 面不减、有序 targets 例外恰 1 项。
# op 全集加法等式（本 task 不新增 op）：判据沿用 internal/plan 侧的既有等式行。
# 重钉链（判据本体一格不放宽，反而更严）：
#   · M4 · T-…-055 `set_stale`（A-33）把等式钉成 `m3AllOps, m4NewOps = 16, 1`（全集 17）；
#   · knowledge_opinion_split（Schema v2，4b36712）新增两个 Opinion 写口，主链路 7→9，
#     全集 17→19，等式随之重钉为 `mainOps, m3Ops, editOps, m4NewOps = 9, 8, 1, 1`
#     （逐项写死，任一项漂移即判红；见 internal/plan/m3_test.go 的加法等式与 t.Fatalf）。
# 锚定当前这条加法等式：它把「主链路 9 / M3 8 / 编辑 1 / M4 新增 1」四项逐字钉死——
# 本 task（跨领域移动，reconcile 侧）若偷偷新增 op，任一加数必然被迫改动，本格立刻红。
OPS="$(grep -c 'mainOps, m3Ops, editOps, m4NewOps = 9, 8, 1, 1' "$(eg_test_path internal/plan/m3_test.go)")"
[ "${OPS}" = "1" ] || die "AllOpNames 等式必须仍是「主链路 9 + M3 8 + 编辑 1 + M4 新增 1 = 19」（本 task 不新增 op）"
[ "$(grep -c 'OrderedTargetsCheckCount = 1' "${REPO_ROOT}/internal/reconcile/check.go")" = "1" ] ||
  die "有序 targets 例外必须恰 1 项（封闭例外面）"
[ "$(grep -rn 'content_hash' "${REPO_ROOT}" --include='*.go' --include='*.md' --include='*.sh' |
  wc -l | tr -d ' ')" -ge 42 ] || die "content_hash 事实面不得减少（≥ 42）"
ok "三条零 / 零关系写 API / 零 verb 字面量 / git 包零写方法 / 命令零注册 / 计数面不动（AllOpNames 等式行 ${OPS} 条）"

printf '\n=== domain_moved.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
