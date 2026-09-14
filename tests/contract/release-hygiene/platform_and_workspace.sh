#!/usr/bin/env bash
# Release support-scope and workspace-hygiene contract (defect_zeroing · T-005/T-006).
#
# This gate keeps three public promises executable:
#   1. The only native platform with in-repo execution evidence is linux/amd64.
#   2. linux/arm64 and darwin/* are cross-compiled artifacts, not claimed as native-validated.
#   3. Python caches and generated runtime trees stay outside the tracked source surface.
# It also exercises the source-package bootstrap path without recursing into `make test`.

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
cd "${REPO_ROOT}"

WORK="$(eg_scratch eg-release-hygiene)"
trap 'rm -rf "${WORK}"' EXIT

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

step "平台证据：当前仓内原生执行证据只对应 linux/amd64"
GO_NATIVE="$(go env GOOS)/$(go env GOARCH)"
UNAME="$(uname -srm)"
[ "${GO_NATIVE}" = "linux/amd64" ] || die "本 suite 当前只登记 linux/amd64 原生证据，实测 go env=${GO_NATIVE}"
printf '  uname=%s; go-native=%s\n' "${UNAME}" "${GO_NATIVE}"
ok "原生执行环境与文档已验证平台集合一致：linux/amd64"

step "支持范围文档：三支非本机目标必须标为未原生验证"
grep -qF '| `linux/amd64` | Fully exercised by the original acceptance suite |' INSTALL.md \
  || die "INSTALL.md 未把 linux/amd64 登记为唯一已完整执行平台"
grep -qF '| `linux/arm64` | 仅交叉编译，原生运行验证未做 |' INSTALL.md \
  || die "INSTALL.md 未把 linux/arm64 登记为未原生验证"
grep -qF '| `eg_darwin_amd64` | 仅交叉编译产出，未经真机运行验证 |' INSTALL.md \
  || die "INSTALL.md 未把 darwin/amd64 登记为未原生验证"
grep -qF '| `eg_darwin_arm64` | 仅交叉编译产出，未经真机运行验证 |' INSTALL.md \
  || die "INSTALL.md 未把 darwin/arm64 登记为未原生验证"
grep -qF 'Linux/amd64 is the original fully exercised platform' README.md \
  || die "README.md 未声明 linux/amd64 是已完整执行平台"
grep -qF 'Linux/arm64,' README.md \
  || die "README.md 未点名 linux/arm64"
grep -qF 'Darwin/amd64, and Darwin/arm64 release binaries are cross-compiled' README.md \
  || die "README.md 未把 darwin 两支限定为交叉编译产物"
grep -qF '未经真机运行验证' README.md \
  || die "README.md 未登记非本机目标未经真机运行验证"
ok "README/INSTALL 的已验证集合为 {linux/amd64}，其余三支为交叉编译/未原生验证"

step "反向措辞：不得把交叉编译目标冒充为原生已验证"
if grep -nE 'linux/arm64.*(原生)?(已验证|验证通过|通过验收)|(eg_)?darwin_(amd64|arm64).*(原生)?(已验证|验证通过|通过验收)|darwin/(amd64|arm64).*(原生)?(已验证|验证通过|通过验收)|macOS.*(已验证|验证通过)|四平台.*(原生|运行).*(已验证|验证通过|通过验收)' README.md INSTALL.md; then
  die "公开文档出现无证据平台已验证表述"
fi
ok "公开文档没有把 linux/arm64 或 darwin/* 写成已原生验证"

step "反证：把 darwin/arm64 写成已验证时，本判据表达式必须命中"
cp INSTALL.md "${WORK}/INSTALL.bad.md"
printf '\n| `eg_darwin_arm64` | 原生已验证通过 |\n' >> "${WORK}/INSTALL.bad.md"
grep -nE '(eg_)?darwin_(amd64|arm64).*(原生)?(已验证|验证通过|通过验收)|darwin/(amd64|arm64).*(原生)?(已验证|验证通过|通过验收)|macOS.*(已验证|验证通过)' "${WORK}/INSTALL.bad.md" >/dev/null \
  || die "合成 darwin/arm64 已验证表述未被反向表达式命中"
ok "无证据平台误标已验证会转红"

step "Python/测试运行期垃圾：ignore 规则覆盖 __pycache__、*.pyc、staging 与报告目录"
for p in 'tools/teamwork/vendor/click/__pycache__/core.cpython-311.pyc' 'pkg/__pycache__/x.pyc' '.tests-staging/run-x/file' 'tests/_report/run-x/summary.json'; do
  git check-ignore -q "${p}" || die "${p} 未被 .gitignore 覆盖"
done
tracked_junk="$(git ls-files | grep -E '(^|/)__pycache__/|\.pyc$|^\.tests-staging/|^tests/_report/' || true)"
[ -z "${tracked_junk}" ] || { printf '%s\n' "${tracked_junk}" >&2; die "运行期/缓存垃圾进入跟踪面"; }
ok "运行期与 Python 缓存垃圾被忽略，且零跟踪"

step "源码分发包无 .git 时，tests/run.sh 可建立本地测试基线并生成执行计划"
SRC_PKG="${WORK}/source-package"
eg_snapshot_worktree "${SRC_PKG}"
rm -rf "${SRC_PKG}/.git"
(cd "${SRC_PKG}" && bash tests/run.sh --profile manifest --plan-only >"${WORK}/srcpkg-plan.log" 2>&1) \
  || { tail -20 "${WORK}/srcpkg-plan.log" >&2; die "无 .git 源码包 tests/run.sh --plan-only 失败"; }
grep -q 'manifest.inventory' "${WORK}/srcpkg-plan.log" || die "源码包 plan 未包含 manifest.inventory"
[ -d "${SRC_PKG}/.git" ] || die "源码包未建立本地测试 Git 基线"
ok "无 .git 源码包可自举测试基线，清单执行计划可生成"

printf '\n[PASS] release-hygiene/platform_and_workspace：%d 项断言全绿（步骤 %d）\n' "${PASS}" "${STEP}"
