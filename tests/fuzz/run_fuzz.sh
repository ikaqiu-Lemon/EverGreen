#!/usr/bin/env bash
# Go 原生 fuzz 的短时执行器（system_assurance · T-…-005 · 合同 D5 `fuzz` profile）。
#
# 语义边界（先说清楚，免得把它当成"覆盖率"用）
# ------------------------------------------------
# fuzz 是**性质防线**，不是回归清单：它证明"任意输入下这条性质没被破"，
# 而不是"某个用例通过"。因此：
#   · 默认 `-fuzztime` 很短（EG_FUZZTIME，默认 10s/target），CI 里也只跑短时；
#     短时的意义是**守住 seed 语料 + 抓住立刻能触发的反例**，长跑是显式动作，不进 core。
#   · 失败语料由 Go 自动落到 `<pkg>/testdata/fuzz/<Target>/`。物化树是临时的，
#     所以本脚本在失败时把语料**回捞**到 tests/_report/fuzz-crashers/ 并原样打印，
#     否则崩溃输入随沙箱一起消失，等于白跑。
#
# target 清单从**盘面派生**，不手写
# --------------------------------
# 扫 `tests/_staged/**` 里的 `func Fuzz…(f *testing.F)`，一个都不许漏；也因此新增 target
# 不需要改本脚本或任何清单。最少 3 个（合同 D5 / T-…-005 A4）：少于 3 直接失败，
# 防止"删到只剩一个还一路绿"。
#
# 为什么必须在物化树里跑：白盒测试的权威存放位是 tests/_staged/（ADR-T1），
# 真实产品树零 _test.go，`go test -fuzz` 在真实仓根本找不到 target。
#
# 用法：
#   cd evergreen && bash tests/fuzz/run_fuzz.sh              # 每个 target 10s
#   EG_FUZZTIME=60s bash tests/fuzz/run_fuzz.sh              # 加长
#   EG_FUZZ_ONLY=FuzzRoundTrip bash tests/fuzz/run_fuzz.sh   # 只跑一个（排障）

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

FUZZTIME="${EG_FUZZTIME:-10s}"
ONLY="${EG_FUZZ_ONLY:-}"
MIN_TARGETS="${EG_FUZZ_MIN_TARGETS:-3}"
CRASH_DIR="${REPO_ROOT}/tests/_report/fuzz-crashers"

RAN=0      # 实际跑过的 target 数
PASS=0     # 无反例的 target 数
FAIL=0     # 有反例 / 执行失败的 target 数
step() { printf '\n=== %s ===\n' "$1"; }
note() { printf '  [ok] %s\n' "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
bad()  { FAIL=$((FAIL + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
die()  { printf '[FAIL] %s\n' "$1" >&2; exit 1; }

eg_require_disk 512

# ---------------------------------------------------------------- 1. 派生 target 清单
step "从 tests/_staged/** 派生 fuzz target（pkg dir + 函数名，不手写清单）"
TARGETS="$(grep -rn --include='*_test.go' -E '^func (Fuzz[A-Za-z0-9_]*)\(f \*testing\.F\)' \
  "${EG_STAGED_TESTS}" | sed -E 's#^'"${EG_STAGED_TESTS}"'/##; s#/[^/]*\.go:[0-9]+:func #\t#; s#\(f \*testing\.F\).*##' \
  | sort -u)"
[ -n "${TARGETS}" ] || die "tests/_staged/ 下零 fuzz target：fuzz profile 无内容可跑"
N_TARGETS="$(printf '%s\n' "${TARGETS}" | grep -c .)"
printf '%s\n' "${TARGETS}" | awk -F'\t' '{printf "  · %-28s %s\n", $2, $1}'
[ "${N_TARGETS}" -ge "${MIN_TARGETS}" ] ||
  die "fuzz target ${N_TARGETS} 个 < 下限 ${MIN_TARGETS}（合同 D5 / A4）"
note "${N_TARGETS} 个 fuzz target 在盘（下限 ${MIN_TARGETS}）"

# ---------------------------------------------------------------- 2. 物化后逐个短时跑
step "物化测试树后逐 target 跑 -fuzztime=${FUZZTIME}（CGO_ENABLED=0）"
STAGED="$(eg_staged_root)" || die "物化失败"
trap 'eg_staged_cleanup' EXIT
printf '  物化树：%s\n' "${STAGED}"

while IFS=$'\t' read -r pkgdir fn; do
  [ -n "${fn}" ] || continue
  if [ -n "${ONLY}" ] && [ "${fn}" != "${ONLY}" ]; then
    continue
  fi
  RAN=$((RAN + 1))
  log="${TMPDIR:-/tmp}/eg-fuzz-${fn}.log"
  if (cd "${STAGED}" && CGO_ENABLED=0 go test "./${pkgdir}" -run '^$' \
        -fuzz "^${fn}\$" -fuzztime "${FUZZTIME}" >"${log}" 2>&1); then
    execs="$( { grep -oE 'execs: [0-9]+' "${log}" | tail -1 || true; } | sed 's/[^0-9]//g' )"
    ok "${fn}（${pkgdir}）无反例${execs:+（execs ${execs}）}"
  else
    bad "${fn}（${pkgdir}）发现反例或执行失败 —— 见下方输出与回捞语料"
    tail -40 "${log}" >&2
    # 回捞崩溃语料：物化树随后即删，不捞就永久丢失。
    if [ -d "${STAGED}/${pkgdir}/testdata/fuzz/${fn}" ]; then
      mkdir -p "${CRASH_DIR}/${fn}"
      cp -a "${STAGED}/${pkgdir}/testdata/fuzz/${fn}/." "${CRASH_DIR}/${fn}/" || true
      printf '  [note] 崩溃语料已回捞到 %s/%s/\n' "${CRASH_DIR}" "${fn}" >&2
    fi
  fi
done <<< "${TARGETS}"

# ---------------------------------------------------------------- 3. 汇总
printf '\n=== 汇总 ===\n'
printf '  在盘 target=%s｜实跑=%s｜无反例=%s｜有反例=%s｜fuzztime=%s（每 target）\n' \
  "${N_TARGETS}" "${RAN}" "${PASS}" "${FAIL}" "${FUZZTIME}"
[ "${RAN}" != "0" ] || die "零 target 实跑（EG_FUZZ_ONLY 过滤掉了全部 target？）"
if [ "${FAIL}" != "0" ]; then
  printf '[FAIL] fuzz profile 未通过：%s 个 target 有反例\n' "${FAIL}" >&2
  exit 1
fi
printf '[PASS] fuzz profile 通过（短时；长跑属显式动作，结论不外推）\n'
