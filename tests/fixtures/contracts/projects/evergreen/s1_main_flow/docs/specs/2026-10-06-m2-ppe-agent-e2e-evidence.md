# M2 Agent Harness 真实 coding-agent 端到端验证证据

**结论：通过。** Agent Harness Agent 独立完成
`capture → context/查询 → 语义判断 → ChangePlan → apply → rel add → 再查询验证`。

## ① 会话标识与起止 UTC 时间

| 项 | 值 |
|-|-|
| 环境 | `agent.example.invalid` Agent Harness coding-agent Linux 沙箱 |
| 会话 ID | `56cc0392-ffa1-41ee-9b89-21d7ae5c0199` |
| 会话链接 | `https://agent.example.invalid/chat/56cc0392-ffa1-41ee-9b89-21d7ae5c0199` |
| 起（UTC） | `2026-09-01T14:54:40Z` |
| 止（UTC） | `2026-09-01T15:26:03Z` |

## ② 环境与 `eg` 版本号

- Linux x86_64，Go 1.24.13，Git 2.39.5。
- `eg --version` 实测：
  `eg 0.2.0-m2 (commit unknown, built 2026-09-01T15:04:47Z, go1.24.13 X:cacheprog,testenhance)`。
- `make build` 与 `make lint` 退出 `0`。
- `make test` 因分发包未包含仓库外的 `../teamwork/**` 合同文档而退出 `2`；
  失败限于 4 个文档契约测试，产品功能包均通过。该打包缺口不改写为成功。

## ③ 逐条命令序列

下列九条是 `commands.log` 的归一化核心链路，均来自真实会话原始 `command_log.txt`。

| # | 命令 | 退出码 | 留痕 |
|-|-|-|-|
| 1 | `eg capture ...` | 0 | `commands.log:1` |
| 2 | `eg context --source s-20260901-verification-the-key-to-ai --json` | 0 | `commands.log:2` |
| 3 | `eg search 自验证` | 0 | `commands.log:3` |
| 4 | `eg search 算力` | 0 | `commands.log:4` |
| 5 | `eg card show k-20260918-scaling-compute` | 0 | `commands.log:5` |
| 6 | `eg rel k-20260918-scaling-compute` | 0 | `commands.log:6` |
| 7 | `eg apply --plan plan.json` | 0 | `commands.log:7` |
| 8 | `eg rel add k-20260901-verification-principle limits k-20260918-scaling-compute ...` | 0 | `commands.log:8` |
| 9 | `eg rel k-20260918-scaling-compute` | 0 | `commands.log:9` |

完整原始执行共 41 条带退出码记录，其中 38 条为 `0`。三条非零均在准备阶段：
一次代理抖动、一次确认源码缺失、一次因分发包缺少外部合同文档导致的 `make test` 失败。
核心九条链路命令全部一次成功。

## ④ 计划原件与自主生成声明

- 原件：`evergreen/test/e2e/testdata/ppe/plan.json`
- SHA-256：`990e9685b05eadb0680b40ff3b303315c41db5ab3260e0064b89adb0950d3c05`
- 大小：10398 字节；顶层恰 8 键；4 个 op。
- 收敛结论：对 `k-20260918-scaling-compute` 判定 `independent_new`，
  `core_knowledge` / `conditions` / `reuse_purpose` 均为 `different`。
- op 序列：`write_note` → `create_card` → `add_material_rel` → `add_open_question`。

**计划由 Agent 自主生成，人未提供计划模板与 op 组合。** `plan.json` 从生成到
`eg apply --plan` 消费均未经人工编辑。唯一人工干预是补充缺失的源码、同版本
`SKILL.md`、会话前既有卡和文章正文备用副本。

## ⑤ vault Git 动词序列

真实 vault 含初始化 commit，完整顺序为：

```text
init(aaef516) → capture(92de348) → process(9e82cb3) → relate(cdd3081)
```

按 M2 判据统计业务写入时，动词序列逐字为：

```text
capture( → process( → relate(
```

`git status --porcelain` 为空。既有卡只出现在初始化 commit，写入阶段逐字节未改。

## ⑥ 写入前后同一查询差异

查询均为 `eg rel k-20260918-scaling-compute`，模式一致。

> [query-before.txt] rel：k-20260918-scaling-compute 的正向 0 条 / 反向 0 条（扫描 1 个 .md，跳过 0 个；只读，零写入零 commit）
> [query-after.txt] rel：k-20260918-scaling-compute 的正向 0 条 / 反向 1 条（扫描 2 个 .md，跳过 0 个；只读，零写入零 commit）
> [query-after.txt] 反向关系：k-20260901-verification-principle --limits--> k-20260918-scaling-compute  理由：验证原则限定了既有卡结论的适用前提：可随算力扩展的通用方法要把算力真正转化为知识规模，前提是这些知识能被系统自身验证；原文指出若错误只能由人发现和纠正，知识系统的规模就受限于人所能监控与理解的范围并长期脆弱，因此算力增长本身不足以保证知识规模增长（来源卡：domains/ai-infra/knowledge/k-20260901-verification-principle.md）

补充查询证据：`eg search 自验证` 从 0 命中变为 1 命中新卡；
`eg search 算力` 始终只命中既有卡，反向印证三维度 `different`。

## ⑦ 完成判据 8 与人工干预

| 判据 | 结论 | 证据文件:行号 |
|-|-|-|
| Agent Harness 真实会话存在并可追溯 | 达成 | `session.md:5-10` |
| Agent 自行抓取清洗真实文章 | 达成 | `session.md:35-42`、Agent Harness 原始报告 |
| `capture` 与 `context` 成功 | 达成 | `commands.log:1-2` |
| `search` / `card show` / `rel` 完成判断前复核 | 达成 | `commands.log:3-6` |
| Agent 自主生成且未人工编辑 ChangePlan | 达成 | `plan.json:1`、`session.md:14-23` |
| `apply` 与 `rel add` 成功 | 达成 | `commands.log:7-8` |
| 再查询可见新增关系 | 达成 | `query-before.txt:2-4`、`query-after.txt:2-8` |
| 业务 commit 动词为 `capture → process → relate` | 达成 | `git-log.txt:1-3` |

**M2 完成判据 8：达成。**

### 人工干预记录

| # | 时间 | 动作 | 影响 |
|-|-|-|-|
| 1 | 会话中段 | 首轮发现环境缺源码后，补充源码包、同版本 `SKILL.md`、会话前既有卡与正文备用副本 | 仅补资源；未提供计划、op 组合、参数修正或语义结论 |

### 测试中发现但不否定主链路的缺口

| 项 | 事实 |
|-|-|
| `eg rel add` 与 W5 | 会话时固定产生 W5；已由 I-001 经 T-030/T-031 修复：专用 `relate` 单关系计划不再误报，普通加工计划规则不变（`evergreen@f456b0e`） |
| `apply --dry-run` 计数 | `planned[]` 正确列出新卡分区，但报告体仍显示“新建 0 张” |
| 分发包自检 | 源码包未包含 `teamwork` 合同文档，导致 4 个文档契约测试失败 |
| 规程样例偏置 | `SKILL.md` 示例与验收文章相同，存在诱导 Agent 复用示例结论的风险；本次 Agent 明确未套用 |
