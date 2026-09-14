#!/usr/bin/env bash
# CI 依赖方向硬门禁（M6 · T-evergreen.s1_main_flow-158614-074）。
#
# 判据来源：docs/specs/2026-08-31-evergreen-s1-tech-design.md §13（依赖方向）
#           + 对账合同 §1.2 / ADR-20 文件级隔离 + §7.1「写口唯一」。
#
# §13 三条禁令（任一命中即非零退出；本脚本接入 make lint，与 go test 的 arch/store 护栏互为冗余）：
#   ① internal/index 不得依赖 internal/store —— 索引只吃调用方读好的中性快照，绝不自己回捞库；
#   ② internal/rules 与 internal/query 的 rank / relations / review 子域不得（经依赖闭包）依赖
#      internal/query/filter —— ADR-20：`updated_at > reviewed_at` 受限信号只许出现在筛选条件里，
#      排序 / 关系分析 / 收敛判定 / 综述取材一律不得依赖它（子包未拆出时退到当前宿主断言）；
#      且 filter 不得反向依赖 internal/query（隔离双向，否则闭包判据被绕过）；
#   ③ 状态写口 store.SetStatus / SetReplacedBy / SetDeleted 的**非测试调用点**必须全部落在
#      internal/store/ 内（写口唯一，§7.1）——plan / cli / reconcile 等任何其他包零命中。
#
# 约束：离线、零交互、可重复；只用 bash / coreutils / go；不写盘、不碰任何仓库工作区。
# 用法：cd evergreen && bash tests/contract/dep-direction/dep_direction_gate.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
bad()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

# deps <pkg>：某包的**完整依赖闭包**（含传递），每行一个 import path。
deps() { go list -deps "$1" 2>/dev/null; }
# pkg_exists <pkg>：包是否可被 go list 解析（子包尚未拆出时为假）。
pkg_exists() { go list "$1" >/dev/null 2>&1; }

# ------------------------------------------------------------------ ① index 不依赖 store
sec "禁令①：internal/index 不得依赖 internal/store"
if deps ./internal/index | grep -qx 'github.com/ikaqiu-Lemon/EverGreen/internal/store'; then
  bad "internal/index 的依赖闭包含 internal/store（索引必须只吃中性快照，不得回捞库）"
else
  pass "internal/index 依赖闭包不含 internal/store"
fi

# ------------------------------------------------------------------ ② ADR-20：filter 文件级隔离
sec "禁令②：rank / relations / review / rules(converge) 不得依赖 query/filter，且 filter 不反依赖 query"
FILTER='github.com/ikaqiu-Lemon/EverGreen/internal/query/filter'
# 合同按目标形态点名四个包；子包未拆出时退到「当前宿主」断言，判据不因拆包与否失效。
#   contract|host
FORBIDDEN=(
  'github.com/ikaqiu-Lemon/EverGreen/internal/query/rank|github.com/ikaqiu-Lemon/EverGreen/internal/query'
  'github.com/ikaqiu-Lemon/EverGreen/internal/query/relations|github.com/ikaqiu-Lemon/EverGreen/internal/query'
  'github.com/ikaqiu-Lemon/EverGreen/internal/query/review|github.com/ikaqiu-Lemon/EverGreen/internal/query'
  'github.com/ikaqiu-Lemon/EverGreen/internal/rules/converge|github.com/ikaqiu-Lemon/EverGreen/internal/rules'
)
for pair in "${FORBIDDEN[@]}"; do
  contract="${pair%%|*}"; host="${pair##*|}"
  target="${contract}"
  pkg_exists "${target}" || target="${host}"
  if deps "./${target#evergreen/}" | grep -qx "${FILTER}"; then
    bad "${target} 的依赖闭包含 ${FILTER}（ADR-20：受限信号只许出现在筛选条件里）"
  else
    pass "${target}（合同名 ${contract}）依赖闭包不含 query/filter"
  fi
done
# 隔离双向：filter 不得反向依赖 internal/query 本体。
if deps ./internal/query/filter | grep -qx 'github.com/ikaqiu-Lemon/EverGreen/internal/query'; then
  bad "internal/query/filter 反向依赖 internal/query（隔离必须双向，否则闭包判据被绕过）"
else
  pass "internal/query/filter 不反向依赖 internal/query"
fi

# ------------------------------------------------------------------ ③ 写口唯一：状态写口调用点全在 store
sec "禁令③：store.SetStatus / SetReplacedBy / SetDeleted 非测试调用点必须全部落在 internal/store/"
# 只匹配「方法调用」形态 `.SetXxx(`，与既有常量 plan.OpSetReplacedBy（`.OpSetReplacedBy`）不冲突。
OUTSIDE="$(grep -rnE '\.(SetStatus|SetReplacedBy|SetDeleted)\(' internal/ --include='*.go' \
  | grep -v '_test\.go' | grep -v '^internal/store/' || true)"
if [ -n "${OUTSIDE}" ]; then
  printf '%s\n' "${OUTSIDE}" >&2
  bad "状态写口调用出现在 store 之外（写口唯一：plan / cli / reconcile 等必须零命中）"
else
  pass "三个状态写口的非测试调用点全部落在 internal/store/ 内（写口唯一）"
fi

# ------------------------------------------------------------------ 汇总
echo
if [ "${FAIL}" -ne 0 ]; then
  echo "[FAIL] dep_direction_gate.sh：§13 依赖方向门禁存在违规（见上）" >&2
  exit 1
fi
echo "[PASS] dep_direction_gate.sh：§13 三条禁令全绿"
