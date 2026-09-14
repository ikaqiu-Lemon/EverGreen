#!/usr/bin/env bash
# staging 隔离反证门禁（system_assurance · T-…-003 / 合同 D3 + D3.5）。
#
# 判据来源：docs/specs/2027-03-01-system-test-architecture-contract.md
#   D3「materialize 默认独立 copy，可选 reflink/COW；**禁止** hardlink / symlink」
#   D3.5「隔离以反证方式证明：在 stage 内做破坏性写入，真实仓内容与 git 状态必须逐字节不变」。
#
# 反证流程：
#   0. 快照真实仓：全部 tracked 文件的 SHA256 清单 + `git status --porcelain` + 关键文件 inode/链接数；
#   1. materialize 一个一次性 run（copy 模式）；
#   2. 断言物理隔离：抽样文件的 inode 与真实仓**不同**、真实仓侧硬链接数为 1（无 inode 共享）；
#   3. 在 stage 内执行五类破坏性写入：覆写产品源码 / 截断语料 / chmod / 删除文件 / 新建文件；
#   4. 再跑一次真实仓快照，断言 **SHA 清单、git 状态、链接数三者逐项不变**；
#   5. 断言 stage 内确实发生了变化（否则「不变」是假绿：证明破坏动作真的落了盘）。
#
# 约束：离线、零交互；只写 .tests-staging/<run> 与 TMPDIR，退出前清理自身 run。
# 用法：cd evergreen && bash tests/contract/staging-isolation/isolation.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"
eg_require_disk 1024

FAIL=0
pass() { printf '  [ok] %s\n' "$1"; }
bad()  { printf '  [FAIL] %s\n' "$1" >&2; FAIL=1; }
sec()  { printf '\n=== %s ===\n' "$1"; }

SCRATCH="$(eg_scratch eg-isolation)"
RUN_ID="isolation-$$"
STAGE="${REPO_ROOT}/.tests-staging/${RUN_ID}"
cleanup() {
  rm -rf "${SCRATCH}"
  chmod -R u+w "${STAGE}" 2>/dev/null || true
  rm -rf "${STAGE}"
}
trap cleanup EXIT

# snapshot <输出前缀>：真实仓的三份指纹。
snapshot() {
  local out="$1" file
  git ls-files -z | xargs -0 sha256sum > "${out}.sha256"
  git status --porcelain > "${out}.status"
  : >"${out}.stat"
  while IFS= read -r file; do
    eg_stat_fingerprint "${file}" >>"${out}.stat"
  done < <(git ls-files)
}

# ------------------------------------------------------------------ 0. 前置快照
sec "步骤0：真实仓基线指纹"
snapshot "${SCRATCH}/before"
# 三个被破坏目标的字节副本：步骤 4 用 cmp 逐字节回比，不依赖任何内容常量，
# 避免哨兵字符串随产品重构漂移而把"隔离生效"误判成"污染"。
mkdir -p "${SCRATCH}/bytes/internal/cli"
cp -p internal/cli/bench.go "${SCRATCH}/bytes/internal/cli/bench.go"
cp -p go.mod "${SCRATCH}/bytes/go.mod"
cp -p README.md "${SCRATCH}/bytes/README.md"
printf '  tracked=%s 行指纹\n' "$(wc -l < "${SCRATCH}/before.sha256")"
pass "基线指纹已采集（SHA 清单 / git 状态 / inode 链接数）"

# ------------------------------------------------------------------ 1. materialize
sec "步骤1：materialize（copy 模式）"
if ! meta="$(python3 tests/runner/materialize.py --run-id "${RUN_ID}" --json 2>"${SCRATCH}/mat.err")"; then
  printf '  [FAIL] materialize 失败：%s\n' "$(head -3 "${SCRATCH}/mat.err" | tr '\n' ' ')" >&2
  exit 1
fi
mode="$(printf '%s' "${meta}" | python3 -c 'import json,sys;print(",".join(json.load(sys.stdin)["mode_effective"]))')"
hardlink="$(printf '%s' "${meta}" | python3 -c 'import json,sys;print(json.load(sys.stdin)["hardlink_used"])')"
printf '  mode_effective=%s hardlink_used=%s\n' "${mode}" "${hardlink}"
[ "${hardlink}" = "False" ] && pass "materializer 自报未使用 hardlink" || bad "materializer 使用了 hardlink（D3 禁止）"
[ -d "${STAGE}/evergreen" ] && pass "stage 已生成：.tests-staging/${RUN_ID}/evergreen" || bad "stage 目录缺失"

# ------------------------------------------------------------------ 2. 物理隔离：inode 不共享
sec "步骤2：物理隔离（inode 不共享、链接数为 1）"
SAMPLES=(go.mod internal/cli/bench.go skill/SKILL.md)
for rel in "${SAMPLES[@]}"; do
  [ -f "${rel}" ] || { printf '  [skip] %s 不存在\n' "${rel}"; continue; }
  [ -f "${STAGE}/evergreen/${rel}" ] || { bad "stage 缺少 ${rel}"; continue; }
  ri="$(eg_stat_inode "${rel}")"; si="$(eg_stat_inode "${STAGE}/evergreen/${rel}")"
  rl="$(eg_stat_links "${rel}")"
  if [ "${ri}" = "${si}" ]; then
    bad "${rel} 与 stage 共享 inode（${ri}）——这是 hardlink，不是隔离"
  elif [ "${rl}" != "1" ]; then
    bad "${rel} 真实仓侧链接数为 ${rl}（>1 说明有外部硬链接引用）"
  else
    pass "${rel} inode 独立（真实 ${ri} / stage ${si}），链接数 1"
  fi
  if [ -L "${STAGE}/evergreen/${rel}" ]; then
    bad "${rel} 在 stage 内是 symlink（D3 禁止；go:embed 亦不支持）"
  fi
done
symcnt="$(find "${STAGE}" -type l | wc -l)"
[ "${symcnt}" -eq 0 ] && pass "stage 内零 symlink" || bad "stage 内存在 ${symcnt} 个 symlink"

# ------------------------------------------------------------------ 3. 破坏性写入
sec "步骤3：在 stage 内执行五类破坏性写入"
S="${STAGE}/evergreen"
printf '// CORRUPTED BY isolation.sh\n' > "${S}/internal/cli/bench.go"; pass "覆写产品源码 internal/cli/bench.go"
: > "${S}/go.mod";                                                     pass "截断 go.mod"
chmod 400 "${S}/skill/SKILL.md";                                       pass "chmod 400 skill/SKILL.md"
rm -f "${S}/README.md" 2>/dev/null || true;                            pass "删除 README.md"
printf 'garbage\n' > "${S}/ISOLATION_PROBE.txt";                       pass "新建 ISOLATION_PROBE.txt"
# 让写入穿过 Go 测试路径：staged 测试也在 stage 内写盘
mkdir -p "${S}/test/e2e/testdata/_probe" && printf 'x\n' > "${S}/test/e2e/testdata/_probe/x"
pass "在 stage 的运行期语料目录内写入探针文件"

# 反向自证：stage 确实变了（否则第4步的「不变」毫无意义）
if [ -s "${S}/go.mod" ] || [ ! -f "${S}/ISOLATION_PROBE.txt" ]; then
  bad "破坏动作未真正落盘，反证无效"
else
  pass "stage 侧确认已被破坏（go.mod 空 + 探针文件存在）"
fi

# ------------------------------------------------------------------ 4. 真实仓逐项不变
sec "步骤4：真实仓三项指纹必须逐字节不变"
snapshot "${SCRATCH}/after"
for kind in sha256 status stat; do
  if diff -q "${SCRATCH}/before.${kind}" "${SCRATCH}/after.${kind}" >/dev/null; then
    pass "真实仓 ${kind} 指纹不变"
  else
    bad "真实仓 ${kind} 指纹发生变化：$(diff "${SCRATCH}/before.${kind}" "${SCRATCH}/after.${kind}" | head -4 | tr '\n' ' ')"
  fi
done
# 额外直查：被破坏的三个文件在真实仓仍逐字节等于步骤 0 的字节副本
for f in internal/cli/bench.go go.mod README.md; do
  if cmp -s "${SCRATCH}/bytes/${f}" "${f}"; then
    pass "真实仓 ${f} 逐字节未变"
  else
    bad "真实仓 ${f} 被 stage 侧写入污染"
  fi
done
[ -s go.mod ] && pass "真实仓 go.mod 非空" || bad "真实仓 go.mod 被截断"
[ -f README.md ] && pass "真实仓 README.md 未被删除" || bad "真实仓 README.md 被删除"
[ ! -e ISOLATION_PROBE.txt ] && pass "真实仓无探针文件泄漏" || bad "探针文件泄漏到真实仓"
[ ! -e test/e2e ] && pass "真实仓未出现运行期路径 test/e2e（回填只发生在 stage）" \
  || bad "真实仓出现了 test/e2e —— 回填污染了权威树"

printf '\n'
if [ "${FAIL}" -ne 0 ]; then
  printf '[FAIL] staging 隔离反证未通过（D3/D3.5）\n' >&2
  exit 1
fi
printf '[PASS] staging 隔离反证通过：stage 可任意破坏，真实仓 SHA/git 状态/链接数三项不变\n'
