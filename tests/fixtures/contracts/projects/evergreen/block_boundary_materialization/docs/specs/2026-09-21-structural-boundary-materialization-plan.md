# EverGreen 结构内生分块边界与确定性物化 — 开发方案（计划期）

- 日期：2026-09-21
- 状态：**计划**（本文件只定方案与切分，不含实现；契约真源由 T-…-001 单独产出）
- 产品仓：`evergreen`
- 关联 Epic：`projects/evergreen/block_boundary_materialization`

## 1. 目标（一句话）

候选块的边界不再写进正文，而是由 Markdown 的标题结构内生推导；物化 Knowledge /
Opinion 从「模型再生成」变成「程序确定性截取原文字节」。

用户的职责收窄为**只改正文**；边界、日期、ID、落盘路径全部由程序负责。

## 2. 代码基线核实（2026-09-21 审计）

两条线已分叉，方案必须按 integration 分支的事实来写：

| 项 | `origin/main` = `1cc6532` | `origin/feature/knowledge_opinion_split/integration` = `9a0d1d6` |
| --- | --- | --- |
| 版本 | `0.6.0-m6` | `0.6.0-m6`（Schema v2 代码已落，版本待 T-…-010 提为 `0.7.0-m7`） |
| plan op 全集 | 17（7+8+1+1） | **19**（主链路 9 + M3 8 + `edit_section` + `set_stale`） |
| index | 6 表 | 6 表，`IndexSchemaVersion = 2`、`kind` / validation 列已加 |
| 命令名册 | 22 | **23**（T-…-008 文档同步中） |
| 存储文档 | `docs/STORAGE_DESIGN.md`（325 行，技术方案） | `docs/BLOCK_BOUNDARY_STORAGE.md`（578 行，选型调研） |

`main` **不是** integration 的祖先，差异 229 文件 / +34122 −1801。

与本 Epic 直接相关的实现现状（均为读码核实）：

- `internal/mdfile/doc_index.go`：`indexSections` 只切 **H2 分区**，`h2Name`
  明确排除 `### `；`Span{Name,Start,Body,End}`；`inFence` 状态机目前只用来
  屏蔽围栏代码块内的 `##`。**没有 H3 索引，没有块区间推导。**
- `internal/mdfile/block.go`：`BlockKind` 是封闭 5 值集
  `paragraph / list_item / fence / table / heading3`，**没有容器块**。
- `internal/mdfile/mutate.go`：已有 `ReplaceSectionBody`（H2 级）与 `ReplaceBlock`，
  API 形态可复用，但只能按 H2 分区名定位。
- `internal/mdfile/frontmatter.go`：保字节契约，本包任何路径不出现序列化 API；
  `make guard` 禁 `yaml.Marshal`。
- `go.mod`：**零 Markdown AST 依赖**，仅 `yaml.v3` + `modernc.org/sqlite`；
  所有 Markdown 处理都是行扫描。
- `internal/plan`：op 计数写成"加法等式"注释并有契约测试；新增一个 op 要同时动
  常量、`opKnownKeys`、注册函数、validate 链、执行器分发与测试基线。
- `internal/txn`：`run.lock` + intent 文件 + 原子 rename 两阶段提交已完备，
  `SupportedJournalVersion = 1`；跨文件写集需要新增阶段语义。
- `internal/index/schema.go`：`表名常量（恰 6 张，本 task 不得增删）`、
  `index_meta` 恰 6 键；`Rebuild` 在 `rebuild.go`。
- `internal/cli/root.go`：命令注册在 `wireImplemented`；
  `tests/_staged/internal/cli/root_help_contract_test.go` 做 `--help` 双向恰等。
- **哨兵注释从未实现**：`<!-- kb:begin -->` 类模式在 `internal/` 下 grep 零命中，
  只出现在文档与测试辅助里。因此「哨兵退役」= 契约层声明不采用，
  **不需要写哨兵迁移器**，范围据此收窄。
- `skill/SKILL.md` 现状是让 Agent 在 ChangePlan JSON 的 `sections{}` 里直接递交
  正文，与"在 Note 原位提议候选"的目标态不同 —— 目标态尚未落地任何代码。

## 3. 差距清单（现状 → 目标）

| # | 现状 | 目标 | 落点 |
| --- | --- | --- | --- |
| G1 | 只有 H2 分区索引 | H3 小节可寻址，Span 可推导 | `mdfile/doc_index.go` |
| G2 | 无标题属性概念 | `{#k-slug .knowledge}` 可解析、可剥离 | `mdfile/` 新增 |
| G3 | 无容器块 | `:::` fenced div 最小子集作兜底边界 | `mdfile/block.go`（封闭值集 5→6） |
| G4 | 无物化写口 | `materialize_card` op（19→20） | `plan/schema.go`、`ops_*.go`、validate |
| G5 | 事务无"先目标后源"阶段 | journal v2 + 跨文件写集 + 崩溃两态 | `internal/txn` |
| G6 | 只能按 H2 名替换正文 | 按 H3/容器定位并改源、留物化链接 | `mdfile/mutate.go`、执行器 |
| G7 | 候选块无派生视图 | 可重建 block sidecar，**不加表** | `internal/index` |
| G8 | 无 `eg materialize` / 无 `--plain` 导出 | 命令名册 23→25 + 文档恰等 | `internal/cli`、四份文档 |
| G9 | Agent 在 JSON 里递交提炼正文 | Agent 在 Note 写候选 H3 小节 | `skill/SKILL.md` |

## 4. 工作单元（9 项）

每项写清：改哪里 / 是否触及封闭合同 / 前置 / 验收怎么测。

### T-…-001 Storage v3 契约定稿 — Contract（无代码）

- 改哪里：本 Epic `docs/specs/2026-09-21-structural-boundary-materialization-design.md`。
- 内容：L1 标题小节与属性语法定稿；L2 `:::` 最小子集（是否允许嵌套、与代码块的
  优先级）；逻辑名字符集与冲突规则；日期与 ID 补齐时机；定位失败的错误码；
  `materialize_card` 字段表；journal 阶段枚举；七条验收断言编号；
  「不引入 goldmark」的默认决策与推翻条件。
- 同时收敛双文档：`docs/STORAGE_DESIGN.md` 与 `docs/BLOCK_BOUNDARY_STORAGE.md`
  合并为「一份契约 + 一份调研留档」，交叉引用单向。
- 封闭合同：不触及（只定义）。前置：无。
- 验收：无悬置决策、无模板占位；002/003 能仅凭本文件开工。

### T-…-002 mdfile 基座：H3 索引 + 标题属性 + 容器块

- 改哪里：`doc_index.go`（H3 子索引与 Span 推导）、`block.go`（`BlockKind` 5→6）、
  新增属性解析文件、`inFence` 状态机扩展（容器与围栏的互斥优先级）。
- 封闭合同：**是**（`BlockKind` 值集与其契约测试计数）。
- 前置：001。
- 验收：H3 小节 Span 稳定；属性可解析且可剥离；`SelfCheck` 字节保真；
  fuzz 覆盖"标题在围栏内 / 容器未闭合 / 属性畸形"三类畸形输入。

### T-…-003 `materialize_card` op 契约与注册

- 改哪里：`plan/schema.go`（常量、`opKnownKeys`）、`ops_*.go`（注册与计数等式
  19→20）、`validate_*.go`（字段校验、`initiator=user` 授权口径、错误码）。
- 封闭合同：**是**（op 全集计数与契约测试）。
- 前置：001。可与 002 并行。
- 验收：`AllOpNames()` 计数等式与注释同步；未知 op 仍报 E5；缺 `initiator=user`
  的处置与授权合同一致；plan 解析用例覆盖字段缺失/多余。

### T-…-004 txn 跨文件写集与 journal v2

- 改哪里：`internal/txn/journal.go`（阶段枚举、`SupportedJournalVersion` 1→2）、
  `commit.go` / `recover.go`、CLI recover 钩子。
- 封闭合同：**是**（journal 版本与恢复用例基线）。
- 前置：003。
- 验收：每个阶段注入中断后恢复只出现两态；旧版本 journal 的处置明确
  （拒绝或升级，由 001 定）；`race` 档全绿。

### T-…-005 物化执行器（核心）

- 改哪里：执行器新增 `materialize_card` 分支、`mdfile/mutate.go` 定位扩到
  H3/容器、源 Note 改写（留物化链接/标记）、`store` 落盘路径复用 `CardRel`。
- 封闭合同：不触及。前置：002、004。
- 验收：五步流程端到端；产物正文与源字节 `cmp` 等价；重复执行幂等；
  定位失败显式报错不猜测。

### T-…-006 派生层：block sidecar 与重建

- 改哪里：sidecar 写入与读取、`internal/index/rebuild.go` 纳管、reconcile 规则。
- 封闭合同：不触及（**明确不加表、不加列、不动 6 键**）。前置：005（软依赖 002）。
- 验收：删除 `.index/` 与 sidecar 后可完全重建；对账与 Markdown 全集一致；
  表数量仍恰 6。

### T-…-007 CLI 与逃生舱

- 改哪里：`internal/cli` 新增 `eg materialize`、`eg export --plain`，`root.go`
  注册，`--help` 契约测试与四份文档（README / README.zh-CN / INSTALL / SKILL）。
- 封闭合同：不触及（名册是加法 23→25）。前置：005。
- 验收：`--help` 双向恰等门禁全绿；`--plain` 输出零专有语法且正文可逐字节比对。

### T-…-008 Agent 契约与 SKILL 重写

- 改哪里：`skill/SKILL.md`：Agent 改为在 Note 中写候选 H3 小节（含属性），
  不再在 plan JSON 里递交提炼正文；与 Schema v2 的 `extraction_coverage[]` 对齐。
- 封闭合同：**是**（Agent 侧对外合同）。前置：001、007。
- 验收：SKILL 与 `--help`、契约文档三方一致；示例 plan 可被当前校验链接受。

### T-…-009 存量迁移 + 门禁收口 + 交付

- 改哪里：现存 Vault 的 Note 补候选小节结构；版本提到 `0.8.0-m8`；r7 交付包。
- 封闭合同：不触及。前置：006、008。
- 验收：迁移后 `eg check` / `eg reconcile` 全绿；七档门禁在同一 commit 全绿；
  skip 数不上升；公开仓卫生脚本通过。

## 5. 排期与依赖

- 关键路径：`001 → 003 → 004 → 005 → 006 → 009`。
- 可并行：002 与 003/004；007 与 006。
- 接口型（必须先冻结）：001 / 002 / 003。未 `done` 前 005–007 一律 STOP。
- 里程碑：M-A 2026-10-09 契约冻结；M-B 2026-11-20 基座；M-C 2026-12-18 闭环；
  M-D 2027-01-15 交付。
- **前置闸门**：代码期开工的唯一信号是上游 Epic `knowledge_opinion_split`
  `shipped` 且已合入 `main`；契约期（001）不等待。

## 6. 非目标

- 不更换权威格式（不引入 `.sy`、不引入块数据库、不引入 CRDT 权威层）。
- 不实现 HTML 注释哨兵，也不写哨兵迁移器（从未上线）。
- 不改 Schema v2 已定的 Note 批注分类、Opinion 生命周期与 `extraction_coverage[]` 语义。
- 不新增索引表 / 列 / `index_meta` 键。
- 不引入外部前端（SilverBullet / SiYuan 等）；前端适配是后续独立 Epic 的事。

## 7. 待决策（T-…-001 必须收口）

1. L2 容器是否允许嵌套；容器与围栏代码块同时出现时的优先级。
2. 定位失败的处置：硬失败（推荐）还是产出可修复报告后退出非零。
3. 旧版本 journal 的兼容口径：拒绝执行还是自动升级。
4. 逻辑名唯一性范围：单文件内唯一，还是全 Vault 唯一。
5. 物化后源 Note 留什么痕迹：链接、属性标记还是两者。
6. `main` 与 integration 的收敛顺序：先合上游再开工（推荐），还是双线并行。
