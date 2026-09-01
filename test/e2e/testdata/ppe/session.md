# PPE 会话留痕（T-evergreen.s1_main_flow-158614-028）

> **真实 AIME PPE 会话未执行。**
> 本目录里的文件**不是** AIME PPE 真实 coding-agent 会话的产物，而是「离线回放基线」：
> 用它可以在沙箱内逐字复算五步链路（收录 → 查询 → 判断结论落盘 → 写入 → 再查询验证），
> 但它**不能**证明「计划由真实 Agent 自主生成」。该事实的结论见证据报告
> `teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-10-06-m2-ppe-agent-e2e-evidence.md`
> 第 ⑦ 节：**M2 完成判据 8 记为未达成**。

## 会话标识

| 项 | 值 |
|-|-|
| PPE 环境名 | 无（未创建环境；可达性核实见下表） |
| 会话 ID | 无（未创建会话） |
| 会话链接 | 无（未创建会话） |
| 离线回放执行窗口（UTC） | `2026-09-01T10:55:25Z` ~ `2026-09-01T10:55:26Z`（取自 `commands.log` 首末行） |
| 执行者 | 周航（zhouhang.26），本地沙箱 |

## 环境可达性核实（2026-09-01，逐条命令 + 实测输出）

| # | 命令 | 实测输出（摘录） | 结论 |
|-|-|-|-|
| 1 | `curl -s -o /dev/null -w '%{http_code}' https://aime-ppe.bytedance.net/` | `000` | PPE 域名不可解析 / 不可达 |
| 2 | `curl -s -o /dev/null -w '%{http_code}' https://ppe-aime.bytedance.net/` | `000` | 同上 |
| 3 | `curl -s -o /dev/null -w '%{http_code}' https://aime.bytedance.net/` | `200` | 只有生产域可达，非 PPE |
| 4 | `aime --help` | 子命令仅 `app / diagnose / document / environment / mcp / oncall / ports / prism / skill / space / template / trigger / version / webide / workflow` | CLI **无**「创建会话 / 起 coding agent」入口 |
| 5 | `claude -p "Reply with exactly: PONG"` | `Not logged in · Please run /login` | 本地 coding-agent CLI 未登录，无法起真实会话 |

**结论**：沙箱内既无法访问 AIME PPE，也无法真实调起外部 coding agent。
按 T-…-028 Acceptance C 段的「无法达成时的诚实出口」执行：不伪造会话证据，
任务置 `blocked`，完成判据 8 记为未达成。

## 来源登记（本目录每个文件的真实来源）

| 文件 | 来源 | 是否 Agent 原件 |
|-|-|-|
| `plan.json` | **离线回放基线**：由本任务实施者按 `evergreen/skill/SKILL.md` 的规程与 `2026-09-08-changeplan-contract.md` 手工编写，用于把语义判断凝固成可离线复算的输入 | **否** |
| `seed-k-20260918-scaling-compute.md` | 离线回放基线：会话前 vault 里的既有卡（人工编写的语料） | 否 |
| `commands.log` | 真实执行记录：离线回放实跑九条命令的 UTC 时间戳 / 命令 / 退出码 | 否（非会话产物，是回放产物） |
| `query-before.txt` | 真实执行输出：写入前 `eg rel k-20261006-verification-first` 的完整 stdout | 否（回放产物） |
| `query-after.txt` | 真实执行输出：写入后同一条查询的完整 stdout | 否（回放产物） |
| `git-log.txt` | 真实执行输出：回放 vault 的 `git log --oneline` | 否（回放产物） |
| `session.md` | 本文件：登记「未执行」事实与全部来源 | — |

**未经人工编辑声明**：不适用——`plan.json` 本身即人工编写的回放基线，
因此本文件**不作**「计划未经人工编辑」的声明。真实会话落地后，需用 Agent 原件整体替换
`plan.json` 并把本节改写为逐字声明，同时删去首段的「未执行」登记；
`ppe_test.go` 的 `TestPPEProvenanceRegisteredHonestly` 会强制这两处同步。

## 脱敏登记

| # | 原文形态 | 替换后形态 | 替换理由 |
|-|-|-|-|
| — | 无 | 无 | 未执行真实会话，无会话文本需要脱敏；本目录全部内容为沙箱内自产，无敏感标识 |

## 语料与 ID

| 项 | 值 |
|-|-|
| 文章 | Verification, The Key to AI（Rich Sutton, 2001），正文取自 `test/e2e/testdata/verification-key-to-ai.txt` |
| 原文 ID | `s-20261006-verification-the-key-to-ai` |
| 既有卡 | `k-20260918-scaling-compute` |
| 新建卡 | `k-20261006-verification-first` |
| 新建关系 | `k-20261006-verification-first --limits--> k-20260918-scaling-compute` |
| `plan.json` sha256 | `d29b1cfb92bfcae8a4d1979bee90e17a49cc831f465998a26b6c9e70235b9b46` |

## 解除条件（真实会话何时能补跑）

1. 可访问的 AIME PPE 环境（可创建会话、会话可留痕导出）；
2. PPE 环境内可按 `evergreen/INSTALL.md` 安装并执行 `eg`（`0.2.0-m2`）；
3. 阻塞人：AIME PPE 环境 owner（环境开通）+ 本 Epic owner `ikaqiu`（会话执行与留痕入库）。
