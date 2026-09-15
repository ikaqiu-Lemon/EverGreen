#!/usr/bin/env bash
# CI 流水线口径（system_assurance · T-…-006，合同 D4 / D6 / D9）。
#
# 这是"CI 会做什么"的**唯一可执行定义**：任何人 clone 之后跑 `bash tests/ci/pipeline.sh`
# 得到的结论必须与 CI 一致。流水线本身不含任何 suite 列表——执行计划一律由
# tests/manifest/suites.yaml 经 tests/run.sh 派生（D4 唯一清单）。
#
# 阶段：
#   [1] 环境前置：go / python3 / PyYAML 在场，Go 模块在 `GOPROXY=off` 下可解析，且**全程离线**（不装依赖、不联网）；
#   [2] 工作树干净门禁：CI 上跑测试前工作树必须干净（脏树意味着测的不是被审对象）；
#   [3] 跟踪面卫生门禁：构建产物 / 运行期产物不得被 git 跟踪；
#   [4] make lint：gofmt + vet + 写路径守卫 + §13 依赖方向；
#   [5] 清单自检 profile=manifest：清单 ↔ 盘面零漂移（幽灵 / 孤儿都判红）；
#   [6] 主验收 profile=${EG_CI_PROFILE:-core}；EG_CI_FULL=1 时追加 full 与 race；
#   [7] 收尾零污染自查：跑完测试后工作树必须与 [2] 逐字一致（运行期产物都在 .gitignore 内）。
#
# 退出码：0 全绿；1 判据不成立；3 环境不足（缺工具链/依赖，不算失败但不能声明通过）。
#
# 环境变量：
#   EG_CI_PROFILE     主验收 profile（默认 core）
#   EG_CI_FULL=1      追加 full + race（发布 / 阶段验收口径，耗时数十分钟）
#   EG_CI_ALLOW_DIRTY=1  仅供本地调试：把 [2] 从"必须干净"降级为"记录基线、只查新增污染"，
#                        CI 上禁止设置（脚本会在报告里显式标注降级，避免拿降级结果冒充 CI 绿）。
#
# 用法：cd evergreen && bash tests/ci/pipeline.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

# 支持源码分发包直接运行 CI 脚本：无 .git 时先建立本地测试基线，供后续
# git status / ls-files 型门禁使用；真实仓库不会触发，且不访问网络。
if [ "$(git rev-parse --show-toplevel 2>/dev/null || true)" != "${REPO_ROOT}" ]; then
  git init -q .
  git -c user.name='EverGreen Tests' -c user.email='evergreen-tests@example.invalid' add -A
  git -c user.name='EverGreen Tests' -c user.email='evergreen-tests@example.invalid' commit -qm 'source package ci baseline'
fi

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }
env_die() { printf '  [ENV] %s\n' "$1" >&2; exit 3; }

PROFILE="${EG_CI_PROFILE:-core}"

# ---------------------------------------------------------------- 1. 环境前置
step "环境前置：go / python3 / PyYAML / Go 模块缓存或 vendor 在场（离线，不安装任何东西）"
command -v go >/dev/null 2>&1 || env_die "缺 go 工具链"
command -v python3 >/dev/null 2>&1 || env_die "缺 python3"
python3 -c 'import yaml' 2>/dev/null || env_die "缺 PyYAML（离线环境请预装；CI 镜像应内置）"
# go.mod 的 go 指令可能要求比镜像内置更新的工具链；GOTOOLCHAIN=auto 下 go
# 会尝试从 GOMODCACHE（golang.org/toolchain）取用。离线且缓存缺失时这一步会
# 失败——同样属"环境未就绪"，不能让 set -e 在此处以裸退出码 1 中断，必须落到
# 下面统一的 env_die（退 3 + 指引）。故容错取版本号，真正的可用性由 go list 判定。
GOVER="$(go env GOVERSION 2>/dev/null || true)"
# 离线硬化：即使镜像里带了代理配置，本流水线也不允许联网取模块。
export GOFLAGS="${GOFLAGS:-} -mod=mod"
export GOPROXY=off
export GONOSUMDB='*'
export GOFLAGS="${GOFLAGS} -buildvcs=false"
go list -deps ./cmd/eg >/dev/null 2>"${TMPDIR:-/tmp}/eg-ci-go-modules.err" || {
  sed 's/^/    /' "${TMPDIR:-/tmp}/eg-ci-go-modules.err" >&2 || true
  env_die "Go 工具链或模块在 GOPROXY=off 下不可离线解析（缺 go.mod 所需 Go 工具链或依赖模块）：请预热 GOMODCACHE 或提交 vendor/ 后重跑 CI"
}
ok "go=${GOVER}；python3+PyYAML 在场；GOPROXY=off；Go 模块离线可解析"

# ---------------------------------------------------------------- 2. 工作树干净门禁
step "工作树干净门禁：CI 检出后跑测试前，git status 必须为空"
BASE_STATUS="$(git status --porcelain | sort)"
if [ -n "${BASE_STATUS}" ]; then
  if [ "${EG_CI_ALLOW_DIRTY:-0}" = "1" ]; then
    printf '  [降级] EG_CI_ALLOW_DIRTY=1：工作树非干净，本次只查"新增污染"，\n'
    printf '         结论**不得**当作 CI 绿引用（脏项 %s 条）\n' "$(printf '%s\n' "${BASE_STATUS}" | grep -c .)"
  else
    printf '%s\n' "${BASE_STATUS}" >&2
    die "工作树不干净：CI 必须在干净检出上执行（本地调试可设 EG_CI_ALLOW_DIRTY=1）"
  fi
else
  ok "git status --porcelain 为空"
fi

# ---------------------------------------------------------------- 3. 跟踪面卫生
step "跟踪面卫生：构建产物与运行期产物不得被 git 跟踪"
DIRTY_TRACKED="$(git ls-files | grep -E '^(bin/|dist/|\.tests-staging/|tests/_report/)|\.test$|\.prof$' || true)"
[ -z "${DIRTY_TRACKED}" ] || { printf '%s\n' "${DIRTY_TRACKED}" >&2; die "以下产物被 git 跟踪，必须移出跟踪面"; }
for d in .tests-staging tests/_report; do
  git check-ignore -q "${d}" || die "${d} 必须在 .gitignore 内（运行期产物）"
done
ok "无被跟踪的构建/运行期产物；.tests-staging/、tests/_report/ 均已忽略"

# ---------------------------------------------------------------- 4. 静态门禁
step "make lint：gofmt + go vet + 写路径守卫 + §13 依赖方向"
make --no-print-directory lint >/dev/null || die "make lint 未通过（单独跑 make lint 看详情）"
ok "make lint 通过"

# ---------------------------------------------------------------- 5. 清单自检
step "清单自检 profile=manifest：清单 ↔ 盘面零漂移"
bash tests/run.sh --profile manifest || die "清单自检未通过"
ok "manifest profile 全绿"

# ---------------------------------------------------------------- 6. 主验收
step "主验收 profile=${PROFILE}"
bash tests/run.sh --profile "${PROFILE}" || die "profile=${PROFILE} 未通过"
ok "profile=${PROFILE} 全绿（NOT_RUN 不计 PASS 分子，见报告 gate_a）"

if [ "${EG_CI_FULL:-0}" = "1" ]; then
  step "发布口径追加：profile=full"
  bash tests/run.sh --profile full || die "profile=full 未通过"
  ok "profile=full 全绿"
  step "发布口径追加：profile=race"
  bash tests/run.sh --profile race || die "profile=race 未通过"
  ok "profile=race 全绿"
else
  printf '\n[注] 未设 EG_CI_FULL=1：本次只跑 manifest + %s。全量（full/race）由阶段验收执行。\n' "${PROFILE}"
fi

# ---------------------------------------------------------------- 7. 收尾零污染
step "收尾零污染自查：跑完测试后工作树与 [2] 基线逐字一致"
AFTER_STATUS="$(git status --porcelain | sort)"
if [ "${BASE_STATUS}" != "${AFTER_STATUS}" ]; then
  diff <(printf '%s\n' "${BASE_STATUS}") <(printf '%s\n' "${AFTER_STATUS}") >&2 || true
  die "测试执行污染了工作树（运行期产物必须落在 .gitignore 覆盖的目录内）"
fi
ok "工作树逐字未变（物化副本与报告都在忽略面内）"

printf '\n[PASS] CI 流水线全部 %d 组门禁通过（共 %d 阶段；profile=%s，full=%s）\n' \
  "${PASS}" "${STEP}" "${PROFILE}" "${EG_CI_FULL:-0}"
