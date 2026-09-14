#!/usr/bin/env bash
# Evergreen 全系统测试的**唯一**执行入口（system_assurance · T-…-006，合同 D5）。
#
# 存在理由：在此之前，"跑测试"有 6 种写法（go test ./...、逐支 bash tests/e2e/**、
# m*_acceptance.sh 阶段聚合、单独的 perf/mutation 脚本、CI 里另抄一份命令），
# 每种覆盖面都不同，于是"测试过了"这句话没有唯一含义。本文件把入口收敛成一个：
#   bash tests/run.sh --profile <profile>
# 全部语义（清单驱动、profile 矩阵、零执行/隐式 skip 判红、NOT_RUN 分级、报告）
# 由 tests/runner/run.py 实现，本 wrapper 只做三件事：
#   1. 把 cwd 固定到仓库根（脚本与清单里的相对路径以此为基准，避免"从哪跑结果不同"）；
#   2. 前置检查 python3 与 PyYAML —— 缺依赖必须是**明确的环境错误（退出码 3）**，
#      不能表现成"清单解析失败"这种误导性的红；
#   3. 逐字转发参数，不注入任何默认 profile：入口不替使用者决定跑什么。
#
# 用法：
#   bash tests/run.sh --profile manifest        # 清单自检（CI 第一道）
#   bash tests/run.sh --profile core            # 迁移 / 修复期快验
#   bash tests/run.sh --profile full            # 全系统一次遍历（去重）
#   bash tests/run.sh --profile race            # Go 全包 -race
#   bash tests/run.sh --profile full --plan-only
set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

if ! command -v python3 >/dev/null 2>&1; then
  printf '[ENV] 缺少 python3：runner 与 materializer 均以 python3 实现（环境约束，不是产品缺陷）\n' >&2
  exit 3
fi
if ! python3 -c 'import yaml' >/dev/null 2>&1; then
  printf '[ENV] 缺少 PyYAML：suites.yaml 是唯一套件清单（pip install pyyaml）\n' >&2
  exit 3
fi

exec python3 tests/runner/run.py "$@"
