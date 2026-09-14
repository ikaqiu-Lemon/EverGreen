#!/usr/bin/env bash
# CI 依赖方向门禁的端到端脚本（M6 · T-evergreen.s1_main_flow-158614-074）。
#
# 判据来源：docs/specs/2026-08-31-evergreen-s1-tech-design.md §13（依赖方向）
#           + 对账合同 §1.2 / ADR-20 文件级隔离 + §7.1 写口唯一。
#
# 被测对象是 tests/contract/dep-direction/dep_direction_gate.sh 本体。一个门禁只有同时满足「真绿」与「能红」
# 才有价值：只跑一次绿灯无法排除「脚本永远返回 0」的假绿。本脚本因此两面都验：
#   ① 真绿：在**真实仓库**上跑门禁 → 退 0，输出含 [PASS]、三条禁令逐条 [ok]；
#   ② 能红（禁令①）：在仓库的**只读快照副本**里注入 internal/index → internal/store 依赖，
#      门禁必须非零退出且点名禁令①；
#   ③ 能红（禁令②）：在另一份副本里注入 internal/query → internal/query/filter 依赖，
#      门禁必须非零退出且点名禁令②；
#   ④ 能红（禁令③）：在再一份副本里于 internal/plan 注入 store.SetStatus 调用，
#      门禁必须非零退出且点名禁令③（写口唯一）。
#
# 副本一律经 tar 从**当前工作树**（含尚未提交的门禁脚本）落到 mktemp -d 沙箱，注入只改副本，
# **真实仓库工作区一个字节都不碰**（末尾自查）。
#
# 约束：离线、零交互、可重复；只用 bash/coreutils/git/go/tar；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/contract/dep-direction/dep_gate_double_sided.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-depgate.XXXXXX")"
GATE_REL='tests/contract/dep-direction/dep_direction_gate.sh'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

# snapshot <dst>：把**当前工作树**（含未提交改动、被测门禁脚本本体）复制到 dst。
# 排除项与"目标目录落在源树内"的自包含递归统一由 eg_snapshot_worktree 处理
# （见 tests/lib/common.sh：`.tests-staging/` / `tests/_report/` 必须排除，
#  否则进 runner 后会把物化副本反复打包进去，实测跑满 900s 超时）。
snapshot() {
  local dst="$1"
  eg_snapshot_worktree "${dst}"
  [ -f "${dst}/${GATE_REL}" ] || die "快照缺 ${GATE_REL}（门禁脚本未随工作树复制？）"
}
# run_gate <dir>：在副本 dir 里跑门禁，stdout+stderr 落 out.txt，回显退出码。
run_gate() {
  local dir="$1" c=0
  ( cd "${dir}" && bash "${GATE_REL}" ) >"${WORK}/out.txt" 2>&1 || c=$?
  echo "${c}"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 真绿：真实仓库
step "真绿：真实仓库上跑门禁 → 退 0，输出含 [PASS] 且三条禁令逐条 [ok]"
C="$(run_gate "${REPO_ROOT}")"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "真实仓库门禁应退 0，实得 ${C}"; }
grep -Fq '[PASS]' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "真绿输出应含 [PASS]"; }
[ "$(grep -c '\[ok\]' "${WORK}/out.txt")" -ge 3 ] || { cat "${WORK}/out.txt"; die "三条禁令应各至少一条 [ok]"; }
ok "真实仓库门禁退 0；[PASS] 且 $(grep -c '\[ok\]' "${WORK}/out.txt") 条 [ok]"

# ---------------------------------------------------------------- 2. 能红：禁令① index→store
step "能红①：副本注入 internal/index → internal/store 依赖，门禁必须非零退出并点名禁令①"
S1="${WORK}/s1"
snapshot "${S1}"
cat >"${S1}/internal/index/zzz_gate_probe.go" <<'GO'
package index

// zzz_gate_probe.go：**故意违规**探针，仅供 m6_ci_dep_gate.sh 反证门禁「能红」，不进主干。
import _ "github.com/ikaqiu-Lemon/EverGreen/internal/store"
GO
C="$(run_gate "${S1}")"
[ "${C}" != "0" ] || { cat "${WORK}/out.txt"; die "注入 index→store 后门禁必须非零退出"; }
grep -Fq '禁令①' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "门禁应点名禁令①"; }
grep -Eq '\[FAIL\].*internal/store' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "门禁应报 index 依赖 internal/store"; }
ok "index→store 注入 → 门禁退 ${C}（非零）且点名禁令①"

# ---------------------------------------------------------------- 3. 能红：禁令② query→filter
step "能红②：副本注入 internal/query → internal/query/filter 依赖，门禁必须非零退出并点名禁令②"
S2="${WORK}/s2"
snapshot "${S2}"
cat >"${S2}/internal/query/zzz_gate_probe.go" <<'GO'
package query

// zzz_gate_probe.go：**故意违规**探针，仅供 m6_ci_dep_gate.sh 反证门禁「能红」，不进主干。
import _ "github.com/ikaqiu-Lemon/EverGreen/internal/query/filter"
GO
C="$(run_gate "${S2}")"
[ "${C}" != "0" ] || { cat "${WORK}/out.txt"; die "注入 query→filter 后门禁必须非零退出"; }
grep -Fq '禁令②' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "门禁应点名禁令②"; }
grep -Eq '\[FAIL\].*query/filter' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "门禁应报依赖闭包含 query/filter"; }
ok "query→filter 注入 → 门禁退 ${C}（非零）且点名禁令②"

# ---------------------------------------------------------------- 4. 能红：禁令③ 写口外调 SetStatus
step "能红③：副本在 internal/plan 注入 store.SetStatus 调用，门禁必须非零退出并点名禁令③"
S3="${WORK}/s3"
snapshot "${S3}"
cat >"${S3}/internal/plan/zzz_gate_probe.go" <<'GO'
package plan

// zzz_gate_probe.go：**故意违规**探针，仅供 m6_ci_dep_gate.sh 反证门禁「能红」，不进主干。
// 在 store 之外的包直接调用状态写口，违反「写口唯一」（§7.1）。
import (
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

func zzzGateProbe(s *store.Store, rel, hash string) {
	_, _ = s.SetStatus(rel, hash, model.StatusActive)
}
GO
C="$(run_gate "${S3}")"
[ "${C}" != "0" ] || { cat "${WORK}/out.txt"; die "注入 store.SetStatus 调用后门禁必须非零退出"; }
grep -Fq '禁令③' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "门禁应点名禁令③"; }
grep -Eq 'internal/plan/zzz_gate_probe\.go.*SetStatus' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "门禁应指出 plan 里的越界 SetStatus 调用点"; }
ok "plan 越界调用 SetStatus → 门禁退 ${C}（非零）且点名禁令③、指出具体调用点"

# ---------------------------------------------------------------- 5. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变（注入只落在只读快照副本里）"

printf '\n[PASS] m6_ci_dep_gate.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
