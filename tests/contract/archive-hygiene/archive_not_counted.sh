#!/usr/bin/env bash
# 归档隔离门禁：历史治理材料不得以任何方式进入执行面或统计面
# （system_assurance · T-…-005 · A5；合同 D5 执行面定义 / D9 归档语义）。
#
# H7（e2e-hygiene）已经守住"归档脚本不在 tests/e2e/ 下、不被 e2e 当入口"。
# 但那只是**路径隔离**。真正会骗人的是**统计面**：只要归档材料拿到一个 suite_id、
# 或者被 runner 的执行计划选中、或者被物化回运行期目录，它就会以"测试"的身份
# 出现在 PASS 数、用例数基线和覆盖矩阵里 —— 那等于用历史治理材料给当前系统背书。
# 本门禁守的就是这一层，并且**双侧**：不仅要求"当前不计入"，还要求
# "把它记成计入时判据必须变红"（合成反例，见 A5 括号里那句"计入即失败"）。
#
# 判据
#   A1 README 在场且明确"不是当前产品测试"（归档区的语义声明是必需件，不是可选文档）；
#   A2 清单里 tests/archive/** 恒 layer=archive；
#   A3 且恒 suite_id=`-`（suite 是追溯单位；归档材料不得成为追溯单位）；
#   A4 不出现在 coverage.tsv（能力 × 层级矩阵只统计能力层）；
#   A5 不出现在 runtime_map.tsv（不物化 → 运行期根本看不见它）；
#   A6 归档脚本不可执行位可有可无，但**不得**被任何 profile 计划文件 / runner 清单引用
#      （当前 runner 尚未落地，故检"清单侧零 suite"，T-…-006 落地后由 profile 自检续接）；
#   A7 反例三连：把归档行改成有 suite_id / 改 layer / 塞进 runtime_map，
#      对应判据必须逐个变红（否则本门禁自己是假的）。
#
# 只读：所有反例都在 mktemp 副本上做，真实清单一个字节不动。
#
# 用法：cd evergreen && bash tests/contract/archive-hygiene/archive_not_counted.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

MANIFEST="${EG_TESTS_ROOT}/manifest"
INV="${MANIFEST}/inventory.tsv"
COV="${MANIFEST}/coverage.tsv"
RMAP="${MANIFEST}/runtime_map.tsv"
README="${EG_TESTS_ROOT}/archive/history/README.md"

WORK="$(eg_scratch eg-archive-gate)"
trap 'rm -rf "${WORK}"' EXIT

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

for f in "${INV}" "${COV}" "${RMAP}"; do
  [ -f "${f}" ] || die "清单缺失：${f}"
done

# ---------------------------------------------------------------- 1. 归档区语义声明
step "A1 归档区 README 在场，且明确声明「不是当前产品测试」"
[ -f "${README}" ] || die "缺 tests/archive/history/README.md（A5 要求归档语义显式声明）"
grep -Fq "不是当前产品测试" "${README}" ||
  die "README 未出现「不是当前产品测试」字样：归档语义必须写死，不能靠读者意会"
grep -q "PASS" "${README}" ||
  die "README 未交代与 PASS 统计的关系（应写明不计入 PASS/FAIL 统计）"
N_ARCH_FILES="$(awk -F'\t' 'NR>1 && $1 ~ /^tests\/archive\//' "${INV}" | wc -l | tr -d ' ')"
[ "${N_ARCH_FILES}" -gt 0 ] || die "清单里零 tests/archive/ 资产：归档区是空的还是漏盘点了？"
ok "README 在场且语义明确；清单登记了 ${N_ARCH_FILES} 个归档资产"

# ---------------------------------------------------------------- 2. 层级与 suite
step "A2/A3 归档资产恒 layer=archive 且 suite_id 为 -"
badlayer="$(awk -F'\t' 'NR>1 && $1 ~ /^tests\/archive\// && $2 != "archive" {print $1" layer="$2}' "${INV}")"
[ -z "${badlayer}" ] || { printf '%s\n' "${badlayer}" >&2; die "A2 归档资产 layer 不是 archive"; }
badsuite="$(awk -F'\t' 'NR>1 && $1 ~ /^tests\/archive\// && $5 != "-" {print $1" suite="$5}' "${INV}")"
[ -z "${badsuite}" ] || { printf '%s\n' "${badsuite}" >&2
  die "A3 归档资产拿到了 suite_id —— 它会以一个 suite 的身份进追溯与统计"; }
ok "${N_ARCH_FILES} 个归档资产全部 layer=archive、suite_id=-"

# ---------------------------------------------------------------- 3. 统计面与运行期
step "A4/A5 归档不进 coverage.tsv、不进 runtime_map.tsv"
# coverage 的行是 capability；归档资产 capability 恒 `-`，因此矩阵里不应有 `-` 行。
if awk -F'\t' 'NR>1 && $1 == "-"' "${COV}" | grep -q .; then
  die "A4 coverage.tsv 出现 capability 为 - 的行：元层 / 归档层漏进了能力覆盖矩阵"
fi
cov_total="$(awk -F'\t' 'NR>1 {s+=$7} END {print s+0}' "${COV}")"
cov_cols="$(awk -F'\t' 'NR>1 {s+=$2+$3+$4+$5+$6} END {print s+0}' "${COV}")"
[ "${cov_total}" = "${cov_cols}" ] ||
  die "A4 coverage.tsv 自相矛盾：total 合计 ${cov_total} ≠ 五列合计 ${cov_cols}（有层级被算进 total 却没有列）"
if awk -F'\t' 'NR>1 && ($1 ~ /^tests\/archive\// || $2 ~ /^tests\/archive\//)' "${RMAP}" | grep -q .; then
  die "A5 runtime_map.tsv 登记了归档资产：它会被物化进运行期目录"
fi
ok "coverage.tsv 无 capability=- 行且 total 与五列自洽（合计 ${cov_total}）；runtime_map 零归档条目"

# ---------------------------------------------------------------- 4. 执行面引用
step "A6 归档材料不被执行面引用（e2e / contract / perf / fuzz / mutation 脚本零引用）"
refs="$(grep -rnE '(bash|source|\. )[[:space:]]+[^#]*tests/archive/' \
  "${EG_TESTS_ROOT}/e2e" "${EG_TESTS_ROOT}/contract" "${EG_TESTS_ROOT}/perf" \
  "${EG_TESTS_ROOT}/fuzz" "${EG_TESTS_ROOT}/mutation" 2>/dev/null \
  | grep -vE ':[0-9]+:[[:space:]]*#' || true)"
[ -z "${refs}" ] || { printf '%s\n' "${refs}" >&2; die "A6 执行面把归档材料当入口"; }
ok "执行面五个目录零归档引用"

# ---------------------------------------------------------------- 5. 反例三连（双侧）
step "A7 反例：给归档行加 suite / 改 layer / 塞进 runtime_map，判据必须逐个变红"
ARCH_ROW="$(awk -F'\t' 'NR>1 && $1 ~ /^tests\/archive\// {print $1; exit}' "${INV}")"
[ -n "${ARCH_ROW}" ] || die "取不到归档样本行"

# ①「给归档资产一个 suite_id」→ A3 必须命中
awk -F'\t' -v OFS='\t' -v target="${ARCH_ROW}" \
  '$1 == target { $5 = "archive.m2_acceptance" } { print }' \
  "${INV}" >"${WORK}/inv.suite.tsv"
grep -Fq "archive.m2_acceptance" "${WORK}/inv.suite.tsv" || die "反例①构造失败（sed 未命中）"
if [ -z "$(awk -F'\t' 'NR>1 && $1 ~ /^tests\/archive\// && $5 != "-" {print $1}' "${WORK}/inv.suite.tsv")" ]; then
  die "反例① A3 判据未命中：归档行拿到 suite_id 也判绿 —— 判据是假的"
fi

# ②「把归档层改成 e2e」→ A2 必须命中
awk -F'\t' -v OFS='\t' -v target="${ARCH_ROW}" \
  '$1 == target { $2 = "e2e" } { print }' \
  "${INV}" >"${WORK}/inv.layer.tsv"
grep -Fq "${ARCH_ROW}	e2e	" "${WORK}/inv.layer.tsv" || die "反例②构造失败（sed 未命中）"
if [ -z "$(awk -F'\t' 'NR>1 && $1 ~ /^tests\/archive\// && $2 != "archive" {print $1}' "${WORK}/inv.layer.tsv")" ]; then
  die "反例② A2 判据未命中：归档行改成 e2e 层也判绿"
fi

# ③「把归档脚本塞进 runtime_map」→ A5 必须命中
cp "${RMAP}" "${WORK}/rmap.ghost.tsv"
printf '%s\ttest/archive/m2_acceptance.sh\tevergreen\tarchive\tshell\n' "${ARCH_ROW}" \
  >>"${WORK}/rmap.ghost.tsv"
if ! awk -F'\t' 'NR>1 && ($1 ~ /^tests\/archive\// || $2 ~ /^tests\/archive\//)' \
     "${WORK}/rmap.ghost.tsv" | grep -q .; then
  die "反例③ A5 判据未命中：runtime_map 里塞进归档条目也判绿"
fi

# ④「coverage 里出现元层空行」→ A4 必须命中
{ cat "${COV}"; printf -- '-\t0\t0\t0\t0\t0\t2\n'; } >"${WORK}/cov.dash.tsv"
if ! awk -F'\t' 'NR>1 && $1 == "-"' "${WORK}/cov.dash.tsv" | grep -q .; then
  die "反例④ A4 判据未命中：coverage 出现 capability=- 行也判绿"
fi

# 真实清单零改动（反例只在副本上做）
git -C "${REPO_ROOT}" diff --quiet -- tests/manifest/inventory.tsv tests/manifest/coverage.tsv \
  tests/manifest/runtime_map.tsv 2>/dev/null ||
  printf '  [note] 清单在本次会话中已有未提交改动（非本门禁所为，反例仅在 %s 内进行）\n' "${WORK}"
ok "四个反例逐个被对应判据命中；反例全部只在临时副本上构造"

printf '\n[PASS] 归档隔离门禁通过（A1~A6 + 4 组反例；共 %d 步 / %d 条断言）\n' "${STEP}" "${PASS}"
