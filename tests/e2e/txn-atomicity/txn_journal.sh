#!/usr/bin/env bash
# 事务日志（txn_id / intent / Scan 三分类 / marker 互斥 / Prune）端到端脚本
# （M6 · T-evergreen.s1_main_flow-158614-071）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §4（txn_id 格式 ^t[0-9a-f]{16}$、单调分配；intent.json 单次发布屏障；
#      commit / abort 零字节标记互斥且各自单次；Scan 全量只读三分类 open/corrupt/residue +
#      committed/aborted；Prune 阻断优先、保留策略）
#   §4.2（Scan 不提前 break、全程零写盘；intent 明确缺席才是 residue，stat 成功但解析失败判 Corrupt）
#
# 反证清单：
#   ① txn_id：格式合规、跨进程分配严格单调递增；
#   ② intent + Scan：写 intent 后判 open；未写 intent 的空目录判 residue；
#   ③ marker 互斥：commit 后再 abort 得 E15（不制造双标记），Scan 仍 committed；
#   ④ marker 幂等：同侧 commit 二次为幂等成功，Scan 仍 committed；
#   ⑤ 非零字节 marker：被塞内容的 commit 标记令该事务判 Corrupt；
#   ⑥ 损坏 intent：写坏 intent.json 判 Corrupt；
#   ⑦ Prune：干净结果下清 residue、留 committed；存在 open / corrupt 时阻断（E15）且零删除。
#
# 不钉具体退出码：语义判定读 helper 的 stdout 标记（txn_id= / STATE / code=E15 / removed= / blocked=）。
#
# 约束：离线、零交互、可重复；无 jq / sqlite3 外部依赖（只用 bash/coreutils/go）；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/txn_journal.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-txn-journal.XXXXXX")"
TXNCTL="${WORK}/txnctl"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

ctl() { "${TXNCTL}" "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt"; cat "${WORK}/out.txt"; }
# new_vault：造一个带 .index/txn/ 的干净 vault，回显其路径。
new_vault() { local v; v="$(mktemp -d "${WORK}/vault.XXXXXX")"; mkdir -p "${v}/.index/txn"; echo "${v}"; }
# txn_dir <vault> <id>：事务目录路径。
txn_dir() { echo "$1/.index/txn/$2"; }
# state_of <id>：从 out.txt 的 STATE 行取某 txn 的分类。
state_of() { grep -E "^STATE $1 " "${WORK}/out.txt" | awk '{print $3}'; }

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 helper
step "构建 txnctl（test-only helper）"
(cd "${REPO_ROOT}" && go build -o "${TXNCTL}" ./tests/lib/txnctl) || die "txnctl 构建失败"
ok "helper 就绪"

# ---------------------------------------------------------------- 2. txn_id 格式 + 单调递增
step "txn_id：格式 ^t[0-9a-f]{16}$、连续分配严格单调递增"
V="$(new_vault)"
ID1="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
ID2="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
ID3="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
for id in "${ID1}" "${ID2}" "${ID3}"; do
  printf '%s' "${id}" | grep -qE '^t[0-9a-f]{16}$' || die "txn_id 格式不合规：${id}"
done
[ "${ID1}" \< "${ID2}" ] && [ "${ID2}" \< "${ID3}" ] ||
  die "txn_id 未严格单调递增：${ID1} ${ID2} ${ID3}"
ok "三次分配 ${ID1} < ${ID2} < ${ID3}，格式均合规"

# ---------------------------------------------------------------- 3. intent + Scan（open / residue）
step "写 intent 判 open；未写 intent 的空事务目录判 residue"
V="$(new_vault)"
IDO="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
ctl write-intent --vault "${V}" --txn "${IDO}" >/dev/null
grep -q "intent_written=1" "${WORK}/out.txt" || die "write-intent 应成功"
# 手工造一个只有目录、无 intent.json 的 residue（用合法命名的 txn 目录）。
IDR="t00000000deadbeef"
mkdir -p "$(txn_dir "${V}" "${IDR}")"
ctl scan --vault "${V}"
[ "$(state_of "${IDO}")" = "open" ] || die "${IDO} 应判 open"
[ "$(state_of "${IDR}")" = "residue" ] || die "${IDR} 应判 residue"
ok "有 intent → open；无 intent 空目录 → residue"

# ---------------------------------------------------------------- 4. marker 互斥（commit 后 abort → E15）
step "marker 互斥：commit 后再 abort 得 E15（不制造双标记），Scan 仍 committed"
V="$(new_vault)"
IDC="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
ctl write-intent --vault "${V}" --txn "${IDC}" >/dev/null
ctl mark --vault "${V}" --txn "${IDC}" --kind commit
grep -q "marked=commit" "${WORK}/out.txt" || die "首个 commit 应成功"
ctl mark --vault "${V}" --txn "${IDC}" --kind abort || true
grep -q "code=E15" "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "commit 在场时 abort 应得 E15"; }
[ ! -e "$(txn_dir "${V}" "${IDC}")/abort" ] || die "abort 标记绝不该被创建"
ctl scan --vault "${V}"
[ "$(state_of "${IDC}")" = "committed" ] || die "${IDC} 应仍 committed"
ok "commit 后 abort 得 E15、abort 未落盘、Scan 仍 committed"

# ---------------------------------------------------------------- 5. marker 幂等（commit 二次）
step "marker 幂等：同侧 commit 二次为幂等成功，Scan 仍 committed"
ctl mark --vault "${V}" --txn "${IDC}" --kind commit
grep -q "marked=commit" "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "二次 commit 应幂等成功"; }
[ ! -e "$(txn_dir "${V}" "${IDC}")/commit.tmp" ] || die "幂等 commit 不应残留 commit.tmp"
ctl scan --vault "${V}"
[ "$(state_of "${IDC}")" = "committed" ] || die "幂等后 ${IDC} 应仍 committed"
ok "二次 commit 幂等成功、无 .tmp 残留、Scan 稳定 committed"

# ---------------------------------------------------------------- 6. 非零字节 marker → Corrupt
step "非零字节 commit 标记（被塞内容）令事务判 Corrupt"
V="$(new_vault)"
IDN="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
ctl write-intent --vault "${V}" --txn "${IDN}" >/dev/null
printf 'garbage' >"$(txn_dir "${V}" "${IDN}")/commit"   # 非零字节，违反零字节合同
ctl scan --vault "${V}"
[ "$(state_of "${IDN}")" = "corrupt" ] || die "非零字节 commit 标记应判 Corrupt"
ok "非零字节 commit 标记 → Corrupt"

# ---------------------------------------------------------------- 7. 损坏 intent → Corrupt
step "写坏 intent.json 判 Corrupt（stat 成功但解析失败绝不降级为 residue）"
V="$(new_vault)"
IDB="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
mkdir -p "$(txn_dir "${V}" "${IDB}")"
printf '{ not valid json ' >"$(txn_dir "${V}" "${IDB}")/intent.json"
ctl scan --vault "${V}"
[ "$(state_of "${IDB}")" = "corrupt" ] || die "损坏 intent.json 应判 Corrupt"
grep -q "blocked=1" "${WORK}/out.txt" || die "存在 Corrupt 时 Scan 应报 blocked=1"
ok "损坏 intent.json → Corrupt 且 blocked=1"

# ---------------------------------------------------------------- 8. Prune：清 residue、留 committed；阻断优先
step "Prune：干净结果清 residue 留 committed；存在 open 时阻断（E15）且零删除"
V="$(new_vault)"
# committed 事务。
IDK="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
ctl write-intent --vault "${V}" --txn "${IDK}" >/dev/null
ctl mark --vault "${V}" --txn "${IDK}" --kind commit >/dev/null
# residue（无 intent 的空目录）。
IDRES="t0000000000cafe01"
mkdir -p "$(txn_dir "${V}" "${IDRES}")"
# 干净态（无 open / corrupt）Prune：应删 residue、留 committed。
ctl prune --vault "${V}"
grep -q "REMOVED ${IDRES}" "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "干净 Prune 应移除 residue ${IDRES}"; }
[ -e "$(txn_dir "${V}" "${IDK}")/commit" ] || die "committed 事务不应被 Prune 删除"
[ ! -e "$(txn_dir "${V}" "${IDRES}")" ] || die "residue 目录应已被删除"
# 现在制造一个 open 事务，再 Prune：应阻断（E15）且不删任何东西。
IDOPEN="$(ctl alloc-id --vault "${V}" | sed -n 's/^txn_id=//p')"
ctl write-intent --vault "${V}" --txn "${IDOPEN}" >/dev/null
IDRES2="t0000000000cafe02"
mkdir -p "$(txn_dir "${V}" "${IDRES2}")"
ctl prune --vault "${V}" || true
grep -q "code=E15" "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "存在 open 时 Prune 应阻断得 E15"; }
[ -e "$(txn_dir "${V}" "${IDRES2}")" ] ||
  die "阻断优先：存在 open 时 Prune 必须零删除（residue ${IDRES2} 应仍在）"
ok "干净 Prune 清 residue 留 committed；存在 open 时阻断 E15 且零删除"

# ---------------------------------------------------------------- 9. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] txn_journal.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
