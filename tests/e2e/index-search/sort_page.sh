#!/usr/bin/env bash
# 关系四级排序 + 截断 / 分页 端到端脚本（M5 · T-evergreen.s1_main_flow-158614-068 · 阶段 A）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §7.4（排序必须能在内存里复算，FTS5 的 `rank` 一律不用；索引与扫描逐字等价）
#   §8.2 A-47（`--limit` / `--offset` 封闭两参数、默认 50、`--limit 0` == 不限量、
#             offset 超界返回空 + 退 0、负数 / 非整数退 1（退出码口径经 I-…-008 修正，
#             详见该 issue 与 §8.2 修订行）、截断产恰一条 `W25`、翻页无重无漏）
#   §8.3（data 键集合不扩张：分页事实只经诊断区与文本摘要承载）
# 以及 milestone `M-005` 判据 11（关系四级排序）与判据 12（分页与 W25）。
#
# 本脚本守四件事，其中第 ① 条是本 task 的纠偏核心：
#   ① **`--limit` 是一次读的全局上限**：`card show` / `rel` 的 data 里有正反两个关系列表，
#      合同写的是「最多返回的条数」，因此 `--limit L` 的返回**总数**恒为 min(L, total)，
#      绝不允许两个列表各自分页而给到 2L 条（逐个 L ∈ [1,7] 全覆盖反证）；
#   ② 分页边界逐行对齐 §8.2：`--limit 0` 不限量且零 W25、offset 超界空结果仍退 0、
#      负数 / 非整数退 1、截断恰一条 W25、逐页拼接 == 一次性全量（无重无漏）；
#   ③ 排序确定性**不依赖输入顺序**：同一张卡里两条「同型、同对端、只有 reason 不同」的
#      重复关系，在 frontmatter 里正序写与逆序写，输出必须逐字相同（第 ④ 级排序键是
#      条目输出全等标识 `from|type|target|reason`，不是 `from|target`）；
#   ④ 索引在位与索引删除两条后端的关系列表逐字相等（§7.4），且全程零写权威、零 commit。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；只用 bash / coreutils / awk / git / go，
#   无 jq / sqlite3 依赖；一切写只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/sort_page.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-sort-page.XXXXXX")"
VAULT="${WORK}/vault"
VAULT2="${WORK}/vault2"
EG="${WORK}/eg"

HUB='k-20261201-hub'
PEERS='k-20261201-p1 k-20261201-p2 k-20261201-p3'
SRCS='k-20261201-s1 k-20261201-s2 k-20261201-s3'
DUP='k-20261201-dup'
TOTAL=6   # 正向 3 + 反向 3（对端全 active，默认视图一条不隐藏）

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
eg_at() { local v="$1"; shift; "${EG}" --vault "${v}" "$@" </dev/null; }
gitv() { git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg "$@"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain | sort; }
authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals -type f 2>/dev/null || true; } | sort |
    xargs -r sha256sum )
}

# —— JSON 取值（只用 grep/sed；键序由合同固定，故切片是确定的）——
# 关系条目**只**出现在 relations_out / relations_in 里，且每条恰有一格 `"from":`，
# 因此「本次返回的关系条数」= 全文 `"from":"` 的出现次数（一次读的全局返回量）。
# 计数用的 grep 一律加 `|| true`：零命中是**合法结果**（空页 / 无诊断），不是脚本失败。
edges_total() { { grep -o '"from":"' "$1" || true; } | wc -l | tr -d ' '; }
# out_slice / in_slice：两个数组的原文（data 键序固定：id → relations_out → relations_in → …）。
out_slice() { sed 's/.*"relations_out":\[//; s/\],"relations_in".*//' "$1"; }
in_slice()  { sed 's/.*"relations_in":\[//; s/\],"scanned_files".*//; s/\],"deleted".*//' "$1"; }
count_in_slice() { printf '%s' "$1" | { grep -o '"from":"' || true; } | wc -l | tr -d ' '; }
# edge_keys <文件>：按输出顺序给出每条条目的 reason（本语料里 reason 互不相同，可当条目身份）。
edge_keys() { { grep -o '"reason":"[^"]*"' "$1" || true; } | sed 's/"reason":"//; s/"$//'; }
code_count() { { grep -o "\"code\":\"$2\"" "$1" || true; } | wc -l | tr -d ' '; }

seed_card() { # <vault> <id> <title> <status> <relations-block-or-empty>
  local v="$1" id="$2" title="$3" status="$4" rels="$5"
  local dir="${v}/domains/ai-infra/knowledge"
  mkdir -p "${dir}"
  {
    printf '%s\n' '---' "id: ${id}" "status: ${status}" "created_at: '2026-12-01'" \
      "updated_at: '2026-12-01T10:00:00+08:00'" "title: ${title}" 'sources: []'
    if [ -n "${rels}" ]; then printf '%s\n' 'relations:'; printf '%b' "${rels}"; fi
    printf '%s\n' '---' '' '## 知识内容' '' "正文占位：${title}。" ''
  } >"${dir}/${id}.md"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 + seed
step "CGO_ENABLED=0 构建 + seed 语料（枢纽卡：正向 3 条 / 反向 3 条，对端全 active）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"

HUB_RELS=''
for p in ${PEERS}; do
  HUB_RELS="${HUB_RELS}  - type: supports\n    target: ${p}\n    reason: hub-out-${p}\n"
done
seed_card "${VAULT}" "${HUB}" "枢纽卡" active "${HUB_RELS}"
for p in ${PEERS}; do seed_card "${VAULT}" "${p}" "对端 ${p}" active ''; done
for s in ${SRCS}; do
  seed_card "${VAULT}" "${s}" "来源 ${s}" active \
    "  - type: derives\n    target: ${HUB}\n    reason: hub-in-${s}\n"
done
# 重复关系：同型、同对端，**只有 reason 不同**（第 ④ 级排序键的观察点）。
seed_card "${VAULT}" "${DUP}" "重复关系卡" active \
  "  - type: supports\n    target: ${HUB}\n    reason: dup-b\n  - type: supports\n    target: ${HUB}\n    reason: dup-a\n"
gitv add -A >/dev/null && gitv commit -q -m "seed: m5 排序与分页语料"
[ "$(porcelain)" = "" ] || die "前置条件失败：工作区应干净"
BASE_COMMITS="$(commits)"
BASE_STATUS="$(porcelain)"
authority_sha >"${WORK}/authority.before.txt"
[ -s "${WORK}/authority.before.txt" ] || die "权威快照为空：语料没建起来"
ok "语料就绪：枢纽卡正反各 3 条 + 一张重复关系卡，工作区干净"

# ---------------------------------------------------------------- 2. 全量口径（--limit 0）
step "--limit 0 == 不限量：返回全部、零 W25、total 为分页前总数"
[ "$(eg_code rel "${HUB}" --json --limit 0)" = "0" ] || { cat "${WORK}/err.txt"; die "rel --limit 0 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/full.json"
FULL_EDGES="$(edges_total "${WORK}/full.json")"
# 反向多了重复关系卡的两条：3 + 2 = 5，正向 3 → 共 8。
[ "${FULL_EDGES}" = "8" ] || die "全量应返回 8 条条目（正向 3 + 反向 5），实际 ${FULL_EDGES}"
[ "$(code_count "${WORK}/full.json" W25)" = "0" ] || die "--limit 0 不许产 W25"
edge_keys "${WORK}/full.json" >"${WORK}/full.keys"
[ "$(wc -l <"${WORK}/full.keys" | tr -d ' ')" = "8" ] || die "条目身份清单条数不对"
ok "--limit 0 返回全部 8 条、零 W25"

# ---------------------------------------------------------------- 3. limit 是全局上限（纠偏核心）
step "--limit L 的返回总数恒为 min(L, total)：绝不允许正反列表各自分页给到 2L"
for L in 1 2 3 4 5 6 7 8 9; do
  WANT=$(( L > 8 ? 8 : L ))
  [ "$(eg_code rel "${HUB}" --json --limit "${L}")" = "0" ] || die "rel --limit ${L} 应退 0"
  GOT="$(edges_total "${WORK}/out.txt")"
  [ "${GOT}" = "${WANT}" ] ||
    die "rel --limit ${L}：返回 ${GOT} 条，期望 min(L,8)=${WANT}（limit 被当成了每个列表各自的上限）"
  OUT_N="$(count_in_slice "$(out_slice "${WORK}/out.txt")")"
  IN_N="$(count_in_slice "$(in_slice "${WORK}/out.txt")")"
  [ "$((OUT_N + IN_N))" = "${GOT}" ] || die "两段条数之和 ${OUT_N}+${IN_N} 与总数 ${GOT} 不符"
  # card show 必须给出同一套语义（同一份分页实现，不许两条读路径两种口径）。
  [ "$(eg_code card show "${HUB}" --json --limit "${L}")" = "0" ] || die "card show --limit ${L} 应退 0"
  CGOT="$(edges_total "${WORK}/out.txt")"
  [ "${CGOT}" = "${WANT}" ] || die "card show --limit ${L}：返回 ${CGOT} 条，期望 ${WANT}"
done
ok "rel / card show 在 L ∈ [1,9] 上均满足「返回总数 == min(L, total)」"

# ---------------------------------------------------------------- 4. 截断恰一条 W25
step "截断产恰一条 W25（正反两个列表同时被截断也只有一条）；total == offset+limit 不算截断"
[ "$(eg_code rel "${HUB}" --json --limit 2)" = "0" ] || die "截断路径必须仍退 0"
[ "$(code_count "${WORK}/out.txt" W25)" = "1" ] ||
  { cat "${WORK}/out.txt"; die "截断应产恰一条 W25，实际 $(code_count "${WORK}/out.txt" W25) 条"; }
grep -Fq '共 8 条' "${WORK}/out.txt" || die "W25 文案必须说出截断前的总数"
grep -Fq '返回 2 条' "${WORK}/out.txt" || die "W25 文案必须说出本页条数"
[ "$(eg_code card show "${HUB}" --json --limit 2)" = "0" ] || die "card show 截断应退 0"
[ "$(code_count "${WORK}/out.txt" W25)" = "1" ] || die "card show 截断也必须恰一条 W25"
[ "$(eg_code rel "${HUB}" --json --limit 6 --offset 2)" = "0" ] || die "边界用例应退 0"
[ "$(code_count "${WORK}/out.txt" W25)" = "0" ] || die "total == offset+limit 不应判截断"
ok "截断恰一条 W25、文案说出「共 8 条 / 返回 2 条」；等号边界不判截断"

# ---------------------------------------------------------------- 5. 翻页无重无漏
step "逐页拼接 == 一次性全量（无重无漏），且每页条数不超过 limit"
for SIZE in 1 2 3 5; do
  : >"${WORK}/paged.keys"
  OFF=0
  while :; do
    [ "$(eg_code rel "${HUB}" --json --limit "${SIZE}" --offset "${OFF}")" = "0" ] ||
      die "rel --limit ${SIZE} --offset ${OFF} 应退 0"
    N="$(edges_total "${WORK}/out.txt")"
    [ "${N}" -le "${SIZE}" ] || die "limit=${SIZE} 却返回了 ${N} 条"
    if [ "${N}" = "0" ]; then break; fi
    edge_keys "${WORK}/out.txt" >>"${WORK}/paged.keys"
    OFF=$((OFF + SIZE))
    if [ "${OFF}" -gt 64 ]; then die "分页未推进"; fi
  done
  diff "${WORK}/full.keys" "${WORK}/paged.keys" >/dev/null ||
    { diff "${WORK}/full.keys" "${WORK}/paged.keys" || true; die "limit=${SIZE} 的逐页拼接 != 全量"; }
done
ok "limit ∈ {1,2,3,5} 的逐页拼接逐行等于一次性全量"

# ---------------------------------------------------------------- 6. offset 超界 / 非法参数
step "offset 超界返回空结果且退 0；负数 / 非整数退 1 且零写入"
[ "$(eg_code rel "${HUB}" --json --limit 3 --offset 99)" = "0" ] || die "offset 超界必须退 0（不是错误）"
[ "$(edges_total "${WORK}/out.txt")" = "0" ] || die "offset 超界应返回空结果"
grep -Fq '"relations_out":[]' "${WORK}/out.txt" || die "offset 超界时 relations_out 应为空数组"
grep -Fq '"relations_in":[]' "${WORK}/out.txt" || die "offset 超界时 relations_in 应为空数组"
[ "$(eg_code card show "${HUB}" --json --offset 99)" = "0" ] || die "card show offset 超界必须退 0"
for BAD in "--limit -1" "--limit abc" "--offset -1" "--offset 1.5"; do
  # shellcheck disable=SC2086
  # I-…-008：只读命令退出码闭集恰 {0,1}，参数非法归 1（原判据锁的 4 已判定为错误行为）。
  [ "$(eg_code rel "${HUB}" --json ${BAD})" = "1" ] || die "rel ${BAD} 必须退 1（参数非法）"
  # shellcheck disable=SC2086
  [ "$(eg_code card show "${HUB}" --json ${BAD})" = "1" ] || die "card show ${BAD} 必须退 1"
  # shellcheck disable=SC2086
  [ "$(eg_code search 枢纽 --json ${BAD})" = "1" ] || die "search ${BAD} 必须退 1"
done
# 只读 flag 不作用于写路径：rel add / rel remove 传入即退 1。
[ "$(eg_code rel add "${HUB}" supports "k-20261201-p1" --reason x --limit 1)" = "1" ] ||
  die "rel add --limit 必须退 1（只读参数不作用于写路径）"
[ "$(eg_code rel remove "${HUB}" supports "k-20261201-p1" --reason x --offset 1)" = "1" ] ||
  die "rel remove --offset 必须退 1"
[ "$(porcelain)" = "${BASE_STATUS}" ] || die "非法参数路径污染了工作区"
[ "$(commits)" = "${BASE_COMMITS}" ] || die "读路径与参数错路径不许产生 commit"
ok "offset 超界空结果退 0；四类非法参数退 1；写路径拒收只读参数退 1；零写入零 commit"

# ---------------------------------------------------------------- 7. 排序不依赖输入顺序（第 ④ 级键）
step "同型同对端、只差 reason 的重复关系：frontmatter 正序 / 逆序写，输出逐字相同"
[ "$(eg_code rel "${DUP}" --json --limit 0)" = "0" ] || die "rel dup 应退 0"
cp "${WORK}/out.txt" "${WORK}/dup.a.json"
DUP_ORDER_A="$(edge_keys "${WORK}/dup.a.json" | paste -sd, -)"
# 同一张卡，把两条关系的**书写顺序颠倒**后重放（另一个沙箱 vault，语料其余部分一致）。
mkdir -p "${VAULT2}"
eg_at "${VAULT2}" init --domain ai-infra >/dev/null 2>&1 || die "vault2 init 失败"
eg_at "${VAULT2}" config set default_domain ai-infra >/dev/null 2>&1 || die "vault2 config 失败"
seed_card "${VAULT2}" "${HUB}" "枢纽卡" active "${HUB_RELS}"
seed_card "${VAULT2}" "${DUP}" "重复关系卡" active \
  "  - type: supports\n    target: ${HUB}\n    reason: dup-a\n  - type: supports\n    target: ${HUB}\n    reason: dup-b\n"
for p in ${PEERS}; do seed_card "${VAULT2}" "${p}" "对端 ${p}" active ''; done
eg_at "${VAULT2}" rel "${DUP}" --json --limit 0 >"${WORK}/dup.b.json" </dev/null ||
  die "vault2 rel dup 应退 0"
DUP_ORDER_B="$(edge_keys "${WORK}/dup.b.json" | paste -sd, -)"
[ "${DUP_ORDER_A}" = "${DUP_ORDER_B}" ] ||
  die "重复关系的输出顺序依赖 frontmatter 书写序：${DUP_ORDER_A} vs ${DUP_ORDER_B}"
[ "${DUP_ORDER_A}" = "dup-a,dup-b" ] ||
  die "同键兜底次序应为确定的 reason 升序（dup-a,dup-b），实际 ${DUP_ORDER_A}"
# 同一份语料连跑两次，字节逐字相同（确定性）。
eg rel "${HUB}" --json --limit 0 >"${WORK}/rep1.json" </dev/null
eg rel "${HUB}" --json --limit 0 >"${WORK}/rep2.json" </dev/null
cmp -s "${WORK}/rep1.json" "${WORK}/rep2.json" || die "同一语料两次执行输出不逐字相同"
ok "输入顺序不影响输出（dup-a,dup-b），两次执行逐字相同"

# ---------------------------------------------------------------- 8. 索引后端 == 扫描后端
step "索引在位与索引删除：关系列表逐字相等（§7.4），且分页语义一致"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg index build 应退 0"; }
for ARGS in "--limit 0" "--limit 3" "--limit 2 --offset 3"; do
  # shellcheck disable=SC2086
  eg rel "${HUB}" --json ${ARGS} >"${WORK}/idx.json" </dev/null || die "索引态 rel 应退 0"
  rm -rf "${VAULT}/.index"
  # shellcheck disable=SC2086
  eg rel "${HUB}" --json ${ARGS} >"${WORK}/scan.json" </dev/null || die "扫描态 rel 应退 0"
  [ "$(eg_code index build --json)" = "0" ] || die "复位索引失败"
  # 两条后端的**关系列表**必须逐字相等（降级诊断 W23/Q5 只出现在扫描态，故只比数组切片）。
  [ "$(out_slice "${WORK}/idx.json")" = "$(out_slice "${WORK}/scan.json")" ] ||
    die "${ARGS}：正向关系两条后端不逐字相等"
  [ "$(in_slice "${WORK}/idx.json")" = "$(in_slice "${WORK}/scan.json")" ] ||
    die "${ARGS}：反向关系两条后端不逐字相等"
done
ok "三组分页参数下，索引后端与扫描后端的正反关系列表逐字相等"

# ---------------------------------------------------------------- 9. 权威零改动 / 零 commit
step "全程零写权威 Markdown、零 commit、工作区不脏"
authority_sha >"${WORK}/authority.after.txt"
diff "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" >/dev/null ||
  { diff "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" || true
    die "权威 Markdown 被读路径改写（Markdown 是唯一权威来源）"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "commit 数变了：读路径恒零 commit"
[ "$(porcelain)" = "${BASE_STATUS}" ] || die "工作区被污染（.index/ 必须在 .gitignore 内）"
ok "权威字节清单逐行不变、commit +0、git status 逐字不变"

# ---------------------------------------------------------------- 10. 仓库零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] sort_page.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
