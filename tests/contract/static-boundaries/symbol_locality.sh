#!/usr/bin/env bash
# 符号落点封闭性门禁（system_assurance · T-…-002 / 合同 D5 + D7）。
#
# 来源与形态转换（migration.tsv 中 5 条 merge 行的落点）：
#   · m2_acceptance.sh L172-181 —— internal/query 内 `.index` 非测试落点封闭 + scan.go 排除分支在场
#   · m3_acceptance.sh L245-272 / L380-395 —— reconcile 命令注册与用法行落点封闭
#   · m4_acceptance.sh L161 / L190-196 —— internal/reconcile 零索引符号 + cli 非对账族零 reconcile 引用
#   · m5_acceptance.sh L134-178 / m6_acceptance.sh L96-104 —— 锁与事务原语的授权本位
#
# **形态转换的原因（合同 D5）**：历史脚本把这些判据写成「冻结计数等式」（如 6+7+1=14 → (6+2)+7+1=16），
# 每次合法演进都要回改等式，等式本身没有语义。本脚本一律改写为**集合封闭性**与**存在性**断言：
# 「实现只许落在这些文件里」「该锚点至少在场一次」。语义等价、抗合法演进、不冻结数字。
# 历史等式的差异登记在 I-…-009（阶段冻结算术未迁移），历史脚本原样留档在 tests/archive/。
#
# 约束：离线、只读、零副作用（不写盘、不构建、只 grep + go list）。
# 用法：cd evergreen && bash tests/contract/static-boundaries/symbol_locality.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
bad()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

# files_with <pattern> <路径...>：命中该模式的**非测试**文件集合（去重、排序、空格分隔）。
files_with() {
  local pat="$1"; shift
  { grep -rlE "${pat}" "$@" --include='*.go' 2>/dev/null || true; } \
    | grep -v '_test\.go$' | sort -u | tr '\n' ' ' | sed -e 's/ *$//'
}
# hits <pattern> <路径...>：非测试、非注释行的命中条数。
hits() {
  local pat="$1"; shift
  { grep -rnE "${pat}" "$@" --include='*.go' 2>/dev/null || true; } \
    | grep -v '_test\.go:' | grep -vE ':[0-9]+:[[:space:]]*//' | grep -c . || true
}
# set_eq <期望集合> <实际集合> <说明>
set_eq() {
  if [ "$2" = "$1" ]; then pass "$3：落点集合逐字封闭 = [$2]"; else
    bad "$3：落点集合越界（期望 [$1]，实际 [$2]）"; fi
}
# subset_of <允许集合正则> <实际集合> <说明>
subset_of() {
  local allow="$1" got="$2" desc="$3" out=""
  for f in ${got}; do printf '%s' "${f}" | grep -qE "${allow}" || out+="${f} "; done
  if [ -n "${out}" ]; then bad "${desc}：越界落点 [${out%% }]（允许模式 ${allow}）"
  else pass "${desc}：落点集合 ⊆ 允许模式（实际 [${got}]）"; fi
}

# ---------------------------------------------------- ① 索引读路径落点（源 m2 L172-181）
sec "①internal/query 内 .index 落点封闭 + scan.go 排除分支在场"
set_eq "internal/query/backend.go internal/query/index_backed.go internal/query/scan.go" \
       "$(files_with '\.index' internal/query/)" \
       "内 .index 符号（索引读路径只许在 backend/index_backed/scan 三处）"
if grep -q 'name == "\.git" || name == "\.index"' internal/query/scan.go; then
  pass "scan.go walker 排除分支在场（.index/ 永不进 Markdown 扫描面）"
else
  bad "scan.go 缺少 .git/.index walker 排除分支 —— 索引产物会被当作权威 Markdown 扫进来"
fi

# ---------------------------------------------------- ② reconcile 注册与用法行（源 m3）
sec "②reconcile 命令注册形态与用法行落点封闭"
reg="$(files_with 'Name:[[:space:]]*"reconcile"' internal/cli/)"
set_eq "internal/cli/reconcile.go" "${reg}" "命令注册形态 Name: \"reconcile\"（唯一实现处）"
wire="$(files_with 'r\.Wire\("reconcile"' internal/cli/)"
wire_count="$(printf '%s\n' "${wire}" | wc -w | tr -d ' ')"
if [ "${wire_count}" = "1" ]; then
  pass "r.Wire(\"reconcile\") 装配点唯一（实际 ${wire}）"
else
  bad "r.Wire(\"reconcile\") 装配点不唯一：[${wire}]"
fi
# 用法文案（`eg reconcile`）允许出现在对账族与两处合同要求的引导文案里；
# 历史冻结集合 {reconcile,check,commands}.go 在 M6 强事务后扩为对账族，改判集合封闭。
subset_of '^internal/cli/(reconcile[a-z_]*\.go|check\.go|commands\.go)$' \
          "$(files_with 'eg reconcile' internal/cli/)" \
          "eg reconcile 用法文案（限对账族 + check/commands 引导文案）"

# ---------------------------------------------------- ③ 对账域零索引符号（源 m4 L161/L190）
sec "③internal/reconcile 零索引符号 + cli 非对账族零 reconcile 代码引用"
n="$(hits 'FTS5|sqlite|\.index/' internal/reconcile/)"
[ "${n}" = "0" ] && pass "internal/reconcile 零索引符号（对账只吃 Markdown 真源）" \
  || bad "internal/reconcile 出现索引符号 ${n} 处（对账不得依赖派生索引）"
subset_of '^internal/cli/(reconcile[a-z_]*\.go|check\.go|root\.go|commands\.go)$' \
          "$(files_with '"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"' internal/cli/)" \
          "internal/cli 内 import github.com/ikaqiu-Lemon/EverGreen/internal/reconcile（限对账族入口）"

# ---------------------------------------------------- ④ 锁与事务原语授权本位（源 m5/m6）
sec "④锁 / 事务原语实现的授权本位"
set_eq "internal/txn/lock.go" "$(files_with 'syscall\.Flock|LOCK_EX|LOCK_NB' internal/ cmd/)" \
       "文件锁系统调用（flock 原语只许在 internal/txn/lock.go 实现）"
set_eq "internal/index/reserved.go internal/txn/lock.go" "$(files_with '"run\.lock"' internal/ cmd/)" \
       "run.lock 字面量（锁文件名定义 + 保留名登记两处）"
[ "$(hits 'syscall\.Flock' internal/txn/lock.go)" != "0" ] \
  && pass "授权本位内锁原语确实在场（≥1 处，防止「删干净也算绿」）" \
  || bad "internal/txn/lock.go 内无 flock 调用 —— 锁实现被掏空"
subset_of '^internal/(txn|index)/.*\.go$' "$(files_with 'journal|Journal' internal/ | grep -v '^internal/cli/' | tr '\n' ' ')" \
          "事务日志实现（限 internal/txn 与 internal/index）"

printf '\n'
[ "${FAIL}" -eq 0 ] || { printf '[FAIL] 符号落点封闭性门禁未通过\n' >&2; exit 1; }
printf '[PASS] 符号落点封闭性门禁通过（集合封闭 + 锚点在场，无冻结计数）\n'
