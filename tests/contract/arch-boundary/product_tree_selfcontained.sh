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

# ------------------------------------------------------------------ ⑤ 用户可见串不得指向测试树
# 判据来源：I-evergreen.system_assurance-158614-027（`eg bench` 提示 `go run ./test/perf/corpus_gen.go`，
# 而 `test/` 已整体删除 → 一条**指向不存在文件的可执行建议**）。
# 边界：只排除整行注释（注释里引用测试树位置属开发者上下文）；已删除的 `test/` 根由 ⑥
# 连注释一并禁止。产品二进制的诊断建议不得把用户导向测试树路径 ——
# 测试树可能不随二进制分发，任何 tests/ 路径在用户机器上都不保证存在。
sec "断言⑤：产品树非注释行不得出现测试树路径（tests?/…）"
# 判据实现：只排除**整行注释**（`^\s*//`）。行内的双引号串、反引号 raw string（help 文本就是
# raw string —— I-…-027 的第二处漂移正躲在这里）与代码本体都在覆盖面内。
lit_viol="$(git ls-files -c -o --exclude-standard -- $(printf '%s ' "${PRODUCT_DIRS[@]}") | grep '\.go$' \
  | xargs -r grep -nE '\btests?/[A-Za-z0-9_.-]' \
  | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' || true)"
if [ -n "${lit_viol}" ]; then
  bad "用户可见串/代码指向测试树路径（提示必须现态可执行或改中性表述）：$(printf '%s' "${lit_viol}" | head -3 | tr '\n' ' ')"
else
  pass "cmd/ internal/ skill/ 的非注释行零测试树路径"
fi

# ------------------------------------------------------------------ ⑥ 全文不得引用已删除的 test/ 根
# 批次 A 把测试树外置后 `test/`（单数）整棵目录已删除。产品树里任何对它的引用（含注释）
# 都是失效指针：开发者按注释找文件必然落空。`tests/`（复数，现态）在注释中允许出现。
sec "断言⑥：产品树零引用已删除的 test/ 根（含注释）"
if [ -e "${REPO_ROOT}/test" ]; then
  bad "仓内重新出现 test/ 目录（现态唯一测试根是 tests/）"
else
  pass "仓内无 test/ 目录（唯一测试根 tests/）"
fi
stale="$(git ls-files -c -o --exclude-standard -- $(printf '%s ' "${PRODUCT_DIRS[@]}") | grep '\.go$' \
  | xargs -r grep -nE '(^|[^a-zA-Z0-9_/])(\./)?test/' || true)"
if [ -n "${stale}" ]; then
  bad "产品树引用了已删除的 test/ 根：$(printf '%s' "${stale}" | head -3 | tr '\n' ' ')"
else
  pass "产品树零 test/ 失效路径引用"
fi

printf '\n'
if [ "${FAIL}" -ne 0 ]; then
  printf '[FAIL] 产品树自足门禁未通过\n' >&2
  exit 1
fi
printf '[PASS] 产品树自足门禁通过（零 _test.go / 零测试树依赖 / 未物化可构建 / 用户可见串零测试树路径）\n'
