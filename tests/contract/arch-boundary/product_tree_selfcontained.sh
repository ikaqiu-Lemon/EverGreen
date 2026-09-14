#!/usr/bin/env bash
# 产品树自足门禁（system_assurance · T-…-003 / 合同 D1+D2）。
#
# 判据来源：docs/specs/2027-03-01-system-test-architecture-contract.md
#   D1「tests/ 是唯一权威测试根」+ D2「产品树零 _test.go、Go 白盒测试外置到 tests/_staged/」。
#
# 四条硬断言（任一命中即非零）：
#   ① 产品树（cmd/ internal/ skill/）**零** `_test.go`；
#   ② 产品树任何 .go 文件不得 import 本模块的 `test/...` 或 `tests/...`
#      —— 被产品 import 的包按定义是产品代码，必须归位产品树（历史破窗见
#         I-evergreen.system_assurance-158614-001：internal/cli/bench.go → test/perf/queryset）；
#   ③ `go build ./...` 与 `go vet ./cmd/... ./internal/... ./skill/` 在**未物化**的真实仓直接通过
#      —— 即产品树不依赖任何 materialize 步骤即可独立构建；
#   ④ `go list -deps ./cmd/eg` 的依赖闭包里不出现测试树包。
#
# 约束：离线、零交互、只读（不写工作区、不 materialize）。
# 用法：cd evergreen && bash tests/contract/arch-boundary/product_tree_selfcontained.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"
eg_require_disk 256

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
bad()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

PRODUCT_DIRS=(cmd internal skill)
MODULE_PATH="$(go list -m)"

# ------------------------------------------------------------------ ① 产品树零 _test.go
sec "断言①：产品树零 _test.go"
stray="$(git ls-files -c -o --exclude-standard -- $(printf '%s ' "${PRODUCT_DIRS[@]}") | grep '_test\.go$' || true)"
if [ -n "${stray}" ]; then
  bad "产品树仍有 _test.go（必须外置到 tests/_staged/）：$(echo "${stray}" | tr '\n' ' ')"
else
  pass "cmd/ internal/ skill/ 下零 _test.go"
fi

# ------------------------------------------------------------------ ② 产品树不 import 测试树
sec "断言②：产品树不得 import 测试树包"
viol=""
while IFS= read -r f; do
  [ -n "${f}" ] || continue
  if grep -nE "\"${MODULE_PATH}/tests?(/|\")" "${f}" >/dev/null 2>&1; then
    viol+="${f}:$(grep -nE "\"${MODULE_PATH}/tests?(/|\")" "${f}" | head -1 | cut -d: -f1) "
  fi
done < <(git ls-files -c -o --exclude-standard -- $(printf '%s ' "${PRODUCT_DIRS[@]}") | grep '\.go$' || true)
if [ -n "${viol}" ]; then
  bad "产品代码 import 了测试树包（应归位产品树）：${viol}"
else
  pass "产品树所有 .go 均不 import 本模块的 test 或 tests"
fi

# ------------------------------------------------------------------ ③ 未物化即可构建
sec "断言③：未物化的真实仓可直接 build / vet"
if [ -d "${REPO_ROOT}/.tests-staging" ] && [ -n "$(ls -A "${REPO_ROOT}/.tests-staging" 2>/dev/null)" ]; then
  printf '  [note] .tests-staging 存在但本断言只在真实仓内执行，不受其影响\n'
fi
if out="$(CGO_ENABLED=0 go build ./... 2>&1)"; then
  pass "go build ./... 通过（产品树自足）"
else
  bad "go build ./... 失败：$(printf '%s' "${out}" | head -3 | tr '\n' ' ')"
fi
if out="$(CGO_ENABLED=0 go vet ./cmd/... ./internal/... ./skill/ 2>&1 | grep -vE '^(testcache|\[bits_ut_info\])' || true)"; then
  if [ -n "${out}" ]; then
    bad "go vet 报告问题：$(printf '%s' "${out}" | head -3 | tr '\n' ' ')"
  else
    pass "go vet ./cmd/... ./internal/... ./skill/ 干净"
  fi
fi

# ------------------------------------------------------------------ ④ 依赖闭包不含测试树
sec "断言④：cmd/eg 依赖闭包不含测试树包"
if deps="$(CGO_ENABLED=0 go list -deps ./cmd/eg 2>/dev/null)"; then
  if printf '%s\n' "${deps}" | grep -E "^${MODULE_PATH}/tests?(/|$)" >/dev/null; then
    bad "cmd/eg 闭包含测试树包：$(printf '%s\n' "${deps}" | grep -E "^${MODULE_PATH}/tests?(/|$)" | tr '\n' ' ')"
  else
    pass "cmd/eg 依赖闭包零测试树包"
  fi
else
  bad "go list -deps ./cmd/eg 执行失败"
fi

printf '\n'
if [ "${FAIL}" -ne 0 ]; then
  printf '[FAIL] 产品树自足门禁未通过\n' >&2
  exit 1
fi
printf '[PASS] 产品树自足门禁通过（零 _test.go / 零测试树依赖 / 未物化可构建）\n'
