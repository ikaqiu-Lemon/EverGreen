# Agent Harness 真实 coding-agent 会话留痕

## 会话标识

| 项 | 值 |
|-|-|
| 环境 | `agent.example.invalid` Agent Harness coding-agent Linux 沙箱 |
| 会话 ID | `56cc0392-ffa1-41ee-9b89-21d7ae5c0199` |
| 会话链接 | `https://agent.example.invalid/chat/56cc0392-ffa1-41ee-9b89-21d7ae5c0199` |
| 起止时间（UTC） | `2026-09-01T14:54:40Z` ~ `2026-09-01T15:26:03Z` |
| Agent | Agent Harness |

## 来源登记

| 文件 | 来源 | 是否 Agent 原件 |
|-|-|-|
| `plan.json` | Agent Harness Agent 读取本版本 `SKILL.md`、真实文章与查询结果后从零生成 | 是，未经人工编辑 |
| `commands.log` | 从真实会话 `command_log.txt` 提取的九条核心链路命令、UTC 时间与退出码 | 是，仅做格式归一 |
| `query-before.txt` | 写入前 `eg rel k-20260918-scaling-compute` 完整输出 | 是 |
| `query-after.txt` | 写入后同一命令的完整输出 | 是 |
| `git-log.txt` | 真实 vault 的业务提交记录；环境初始化 commit 未列入 | 是 |
| `seed-k-20260918-scaling-compute.md` | 会话前提供的既有卡 | 否，环境输入 |

`plan.json` 是真实 Agent Harness Agent 自主生成且未经人工编辑的原件。人未提供 ChangePlan
模板、op 组合、参数修正、收敛判断结论或倾向。

## 人工干预记录

| # | 时间（UTC） | 动作 | 边界 |
|-|-|-|-|
| 1 | `2026-09-01T15:03:44Z` | 提供源码包、同版本 `SKILL.md`、既有卡和文章正文备用副本 | 仅补齐环境资源；未提供计划、op 组合、参数修正或语义结论 |

其余网络代理选择、源码构建、vault 初始化、查询、语义判断、计划生成、写入、关系选择与复核均由 Agent 自主完成。

## 脱敏登记

| # | 原文形态 | 替换后形态 | 理由 |
|-|-|-|-|
| - | 无 | 无 | 留痕不含凭证、Cookie、token 或个人敏感标识 |

## 结果摘要

- `eg 0.2.0-m2` 构建成功。
- `capture` / `context` / `apply --dry-run` / `apply` / `rel add` 均退出 `0`。
- Agent 判定 `k-20260918-scaling-compute` 与新知识三维度均为 `different`，
  选择 `independent_new`，新建 `k-20260901-verification-principle`。
- 新关系为
  `k-20260901-verification-principle --limits--> k-20260918-scaling-compute`。
- 写入后同一关系查询由反向 `0` 条变为 `1` 条；业务提交动词序列为
  `capture` → `process` → `relate`。
- Agent Harness 原始证据包 SHA-256：
  `57c6544d5b198c39ca6720c6cd71b3e4191217eefa57092fe081fb366a83da43`。
