---
topic: M3 提案四态合同（status 状态机 / execution 正交 / 逻辑删除全时序 / 新增 op 与诊断码）
stage: S2
milestone: M-003
task: 未分配（本合同先于 M3 拆分冻结，后续 task 反向引用本文）
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》§1.1 / §3 / §3.1 / §4.2 / §4.4 / §4.5 / §4.5.1 / §4.6 / §5.1 / §5.2 / §5.3 / §7.1 / §7.2 / §7.3 / §9.1 / §10 / §10.1 / §10.2 / §10.3 / §14 / §15.1 / §16.1
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
authority_extracted_at: '2026-10-10'
authority_mutation: 只读，未修改
baseline: docs/specs/2026-08-31-evergreen-s1-tech-design.md#ch7
created: '2026-10-10'
updated: '2026-10-10'
updated_by: 项目维护者
---

# M3 提案四态 — Contract（S2 · 里程碑 M3）

本文件把 §16.1 **M3 完成判据**的前两句——「**提案 `status` 四态 + `superseded` 两个触发全通过**」
与「**`execution=failed` 时报告逐路径列出已写 / 未写**」——展开成可施工、可机器校验的合同：
四态状态机（合法边 / 非法边 / 终态）、`execution` 与 `status` 的正交可达矩阵、
逻辑删除全时序、`reviewed_at` 与标记输出口径、提案的 Markdown 落盘形态、
M3 新增 op 与诊断码编号。

下游 M3 task 一律对着本文写 Acceptance；实现与本文冲突时**先回本文补合同**。

## 0. 依据与抽取声明

| 项 | 内容 |
|-|-|
| 权威原文 | 飞书《[技术方案]常青（Evergreen）v1 技术方案》 |
| 抽取日期 | **2026-10-10** |
| 抽取方式 | 全文本地快照 `feishu_tech_design_full.md`（1839 行）逐章定位 |
| 对原文的改动 | **飞书原文只读，未修改**。所有「与原文冲突 / 原文缺失」一律只登记（§10），不回改权威文档 |
| 引用的原文章节 | §1.1 S2 定义、§1.2 F1–F6、§3 目录布局、§3.1 文件形态与命名、§4.2 分区与块、§4.4 提案 schema 与十项必备、§4.5 op 表、§4.5.1 校验分级、§4.6 报告 schema、§5.1 四象限真值表、§5.2 逻辑删除四类影响面、§5.3 恢复规则、§7.1 命令表、§7.2 退出码、§7.3 显著标记、§9.1 B1–B4、**§10 提案子系统（状态图）**、§10.1 两个字段的实现口径、§10.2 逻辑删除完整执行时序、§10.3 提案隔离性、§14 追溯矩阵、§15.1 ADR-11 / ADR-15 / ADR-18 / ADR-20、§16.1 M3 行 |

**姊妹合同**（本文不重复其内容，只引用）

- `docs/specs/2026-10-10-m3-user-authorization-contract.md`：两条路径（P-A / P-U）定义、
  43 行写权限矩阵、4 种授权表达、退出码 `6` 启用边界、13 条不可授权项。
  **本文不重复定义「谁能改什么」**。
- `docs/specs/2026-09-08-changeplan-contract.md`：§1 顶层八键、§4.1 E1–E6、§4.2 W1–W8 / I1、
  §4.3 未编号 warning 登记表、§5 诊断载荷六字段。
- `docs/specs/2026-09-01-eg-cli-contract.md`：§3 `--json` 信封五键、§4 退出码表、§7 显著标记口径。

**硬边界（先读）**

1. 本合同**不引入 M4–M6 能力**：不做对账（S3/M4）、不建 `.index/`（S4/M5）、
   不做强原子 / 锁 / 写前复核 / 崩溃恢复（S5/M6）、不引 CI 依赖门禁（M6）。
2. **不推翻 F1–F6**：提案落盘形态（§7）逐条与 F1 目录布局、F5 五分区语义、
   F6「Markdown 权威 / `.index/` 可重建」对齐。
3. **S2 不承诺「权威 Markdown 一个字节都不改」**——§10.1 逐字：「是**目标态（S5）**口径，**S2 不承诺**」。
   因此本合同**不得**出现「执行失败后字节级不变」这类承诺。
4. 提案**字段完整度不属于冻结范围**（§10 章首 + §3.1 + §4.4）：S2 可先实现子集，
   缺项**按 warning 处理而不是拒绝创建**。

---

## 1. S2 与提案子系统的适用范围

| 项 | 口径 | 依据 |
|-|-|-|
| 提案子系统的引入阶段 | **整章 S2 起提供**；S1 不实现提案、不执行逻辑删除、`proposals/` 目录在 S1 可以不存在、`eg proposal *` 在 S1 不交付 | §10 章首逐字 |
| 提案类型 | **v1 只有 `logical_delete` 一种**（S2 起） | §10 章首；§4.4 `type` 注「v1 唯一类型」 |
| 提案承载的确认 | 产品里**唯一一处执行前确认**，其余全部动作直接生效（EG-CFM-01、EG-CFM-04） | §10 章首 |
| 一提案的粒度 | **语义上一提案 = 一原子操作 = 一次 `eg apply`**，不拆分成多步确认 | §10.1 第 5 条 |
| S2 是否提供多文件强原子 | **不提供**（§9.3）。逻辑删除涉及多文件时**仍可能部分写入** | §10.1 第 5 条逐字 |

---

## 2. `status` 四态与完整状态机

### 2.1 四态定义（`status` 是**用户决定**，四取值，不存在第五种）

| 态 | 含义 | 关键约束 | 依据 |
|-|-|-|-|
| **`pending`** | 已创建、等待用户决定 | `eg proposal list --status pending` 是**待办清单的唯一形态**；未处理提案**不阻塞任何命令的退出码** | §4.4 `status` 注；§10.1 第 3 条；§10.3「不阻塞、不催办」 |
| **`approved`** | 已批准 | **不代表已执行成功**——执行结果由 `execution` 独立承载 | §10.1 第 1 条逐字：「`approved` 只表示已批准，**不代表已执行成功**」 |
| **`rejected`** | 已拒绝 | **拒绝无副作用**：只写 `status` + `decision.reason`，**不删原始文章与材料**；理由与提案一并留在文件与 Git 历史 | §10.3「拒绝无副作用」行 |
| **`superseded`** | 被取代 | 必须写 `decision.superseded_by`（新提案 ID）；两个触发见 §3 | §10.1 第 4 条 |

> §10.1 第 1 条逐字：「`status` 是**用户决定**，四取值，**不存在第五种**。」

### 2.2 状态机（原文 §10 `stateDiagram-v2` 的 `ST` 子图，逐边抄录）

```text
[*]        --> pending      : eg proposal new
pending    --> approved     : eg proposal approve
pending    --> rejected     : eg proposal reject --reason
pending    --> superseded   : 部分接受 / 前提变化 → 生成新提案
approved   --> superseded   : 执行前提已变化 → 不强行执行
rejected   --> [*]
superseded --> [*]
```

### 2.3 合法迁移表（恰 4 条真实迁移 + 1 条初始边）

| # | 起态 | 止态 | 触发命令 / 条件 | 必写字段 | 依据 |
|-|-|-|-|-|-|
| T0 | `[*]` | `pending` | `eg proposal new --type logical_delete --target <id>` | `id` `type` `status` `created_at` `targets` `impact` | §10 状态图；§10.2 时序第 1 步 |
| T1 | `pending` | `approved` | `eg proposal approve <id> --confirm` **且**重算影响面与 `impact` 一致 | `decision.result` | §10 状态图；§10.2 时序第 7 步 + `else 前提成立` 分支 |
| T2 | `pending` | `rejected` | `eg proposal reject <id> --reason <r>` | `decision.result` `decision.reason` | §10 状态图；§4.4 十项必备 ⑨「拒绝必带 reason」 |
| T3 | `pending` | `superseded` | 触发① 部分接受 / 触发② 前提变化 | `decision.result` `decision.superseded_by` | §10.1 第 4 条 |
| T4 | `approved` | `superseded` | 触发② 执行前提已变化 → **不强行执行** | `decision.result` `decision.superseded_by` | §10 状态图边「`approved --> superseded`：执行前提已变化 / 不强行执行」 |

**合法边总数**：真实迁移 **4** 条（T1–T4）+ 初始边 **1** 条（T0）= **5** 条。

### 2.4 非法迁移表（恰 12 条，触发即 **E9**）

4 个态两两有序配对共 4 × 4 = **16** 对，减去 4 条合法迁移（T1–T4），余 **12** 条**全部非法**：

| 起态 \ 止态 | `pending` | `approved` | `rejected` | `superseded` |
|-|-|-|-|-|
| **`pending`** | 🔴 自环 | ✅ T1 | ✅ T2 | ✅ T3 |
| **`approved`** | 🔴 | 🔴 自环 | 🔴 | ✅ T4 |
| **`rejected`** | 🔴 | 🔴 | 🔴 自环 | 🔴 |
| **`superseded`** | 🔴 | 🔴 | 🔴 | 🔴 自环 |

**非法边 = 12**（其中自环 4 条、异向 8 条）。四条最容易被实现写错的：

- `approved → rejected`：**非法**。已批准不可反悔；需要撤销时走 T4 生成新提案。
- `approved → pending`：**非法**。不存在「退回待办」。
- `rejected → *`：**非法**。`rejected` 是终态。
- `superseded → *`：**非法**。`superseded` 是终态。

### 2.5 终态

| 态 | 是否终态 | 依据 |
|-|-|-|
| `rejected` | **是** | §10 状态图有 `rejected --> [*]` |
| `superseded` | **是** | §10 状态图有 `superseded --> [*]` |
| `pending` | 否 | 有三条出边 |
| `approved` | **原文未定义** | §10 状态图**没有** `approved --> [*]` 边，但有 `approved --> superseded`。见 §10 登记项 **A-22** |

**本合同的临时裁决（本合同新定）**：`approved` 判为**非终态**，其**唯一合法出边是 `→ superseded`**。
理由：状态图明确画出了 `approved --> superseded`，若判 `approved` 为终态则该边不可达，与图矛盾。
执行成功后提案**停在 `approved` + `execution.succeeded`**，不再迁移——这与「处理过的提案留在
`proposals/` 原地，不搬目录」（§10.1 第 3 条）一致。

---

## 3. `superseded` 的两个触发（原文出处逐字）

**出处**：飞书原文 **§10.1「两个字段的实现口径」第 4 个 bullet**（本地快照 `feishu_tech_design_full.md`
**第 1192 行**）。逐字引用：

> 「`superseded` 两个触发条件：① 用户只接受一部分内容 → 生成新提案承载实际接受内容，原提案标
> `superseded`；② 执行前提已变化、原影响范围不再成立 → **不强行执行**，生成新提案重新描述当前
> 影响面，原提案标 `superseded`。**两者都必须写 `decision.superseded_by`**。」

| 触发 | 名称 | 前置条件 | 系统动作（三步，缺一不可） | 触发时机 | 补充出处 |
|-|-|-|-|-|-|
| **S-①** | **部分接受** | 用户只接受提案的**一部分内容** | ① 生成**新提案**承载实际接受内容；② 原提案 `status = superseded`；③ 原提案写 `decision.superseded_by = <新提案 ID>` | 用户决定时（`pending` → `superseded`，T3） | §4.4 十项必备 ⑧「可应用内容\|**整体执行，不支持部分应用**」——部分接受**只能**经由本触发表达 |
| **S-②** | **前提变化** | 执行前重算影响面，与提案中的 `impact` **不一致**（原影响范围不再成立） | ① **不强行执行**；② 生成**新提案**重新描述当前影响面；③ 原提案 `status = superseded` + `decision.superseded_by` | `eg proposal approve` 执行前的重算比对（T3 或 T4） | §10.2 时序图 `alt 前提已变化` 分支（第 1227–1229 行）：「`C->>FS: 生成新提案 + 原提案标 superseded` / `C-->>U: 不执行删除，进报告`」；§10.2 正文（第 1241 行）：「`eg proposal approve` 在执行前**重算一次影响面并与提案中的 `impact` 比对**：不一致即走 `superseded` 分支，**不强行执行**」 |

**S-② 的硬要求**：`eg proposal approve` **必须**在执行前重算影响面并与 `impact` 比对。
「先批准后重算」是本触发的**唯一**实现顺序，不得省略重算直接执行。

---

## 4. `execution` 维度与 `status` 的正交关系

### 4.1 `execution` 三态（原文 §10 状态图 `EX` 子图）

```text
[*]         --> not_started
not_started --> succeeded : 写入并提交成功 / 记 git_commit
not_started --> failed    : 校验失败 / 写入或提交失败 / 保留现状 · 不回滚 · 进报告
failed      --> [*]
succeeded   --> [*]
```

| 态 | 含义 | 必写字段 |
|-|-|-|
| `not_started` | 尚未执行（默认值） | — |
| `succeeded` | 写入并提交成功 | `attempted_at`、`git_commit` |
| `failed` | 校验失败 / 写入或提交失败 | `attempted_at`、**`reason`**、**已写 / 未写路径**（§4.3） |

### 4.2 正交的精确含义 + 4 × 3 可达矩阵

> §10.1 第 2 条逐字：「`execution` 是**实际执行结果**，与 `status` **正交**。」
> §4.4 `execution` 注：「实际执行结果，**与 `status` 严格分离**」。

「正交」= **两个维度各自独立取值、互不推导**：`approved` 推不出 `succeeded`，
`succeeded` 也不回写 `status`。但**并非 12 种组合都可达**——可达性由状态机与执行时序共同约束。

| `status` \ `execution` | `not_started` | `succeeded` | `failed` |
|-|-|-|-|
| **`pending`** | ✅ 唯一合法（新建默认） | 🔴 未批准不得有执行结果 | 🔴 未批准不得有执行结果 |
| **`approved`** | ✅ 已批准待执行 | ✅ 执行成功 | ✅ 执行失败（B4：保留现状、不回滚） |
| **`rejected`** | ✅ 唯一合法 | 🔴 拒绝无副作用，不可能执行过 | 🔴 同左 |
| **`superseded`** | ✅ 「不强行执行」的正常落点 | 🔴 执行成功后不再改 `status` | ⚠️ **原文未定义** → **本合同新定：禁止** |

**统计**：12 格中 **✅ 6 格**、**🔴 5 格**（原文可推）、**⚠️ 1 格**（本合同新定）。

**`superseded` × `failed` 判禁止的理由（本合同新定）**：`superseded` 的语义是「**不强行执行**」
（§10.1 第 4 条 + §10.2 `alt` 分支），而 `failed` 意味着执行**已经发生并产生了副作用**
（部分文件已写盘）。二者在语义上互斥。执行失败后的修正**改由新建提案承载**，
原提案停在 `approved` + `failed`，如实留证——这也与 B4「保留现状、不做破坏性回滚」一致。

**违反可达矩阵即 error**：任何落盘的提案出现 🔴 或 ⚠️ 格的组合 → **E9**（§8.1）。

### 4.3 `execution=failed` 时的报告硬要求（M3 完成判据第 2 句）

> §16.1 M3 完成判据逐字：「**`execution=failed` 时报告逐路径列出已写 / 未写**」
> §10.1 第 5 条逐字：「逻辑删除若涉及多个文件，仍可能出现部分写入，此时 `execution.status: failed`
> 并**在报告里逐路径列出已写与未写**。」
> §10.1 第 2 条逐字：「按第九章 B4，**失败不做破坏性回滚**：已完成的写入保留在磁盘，
> **未完成的路径与原因一并进 `execution.reason` 与最终报告**，由用户用 `git status` / `git diff` 判断。」

**落点（本合同新定两个键）**：§4.4 的 `execution` 只有四个子字段
（`status` / `attempted_at` / `reason` / `git_commit`），**没有承载路径清单的键**，
而 §10.1 表却要求 `execution`「含 `failed` + `reason` + **已写 / 未写路径**」——
原文内部不一致，登记为 **A-21**。本合同据「提案字段完整度不属于冻结范围、字段可增补」
（§10 章首 + §4.4）新增两个键：

| 新键 | 类型 | 语义 | 必填条件 |
|-|-|-|-|
| `execution.written_paths` | `[]string` | 本次执行**已成功写盘**的 vault 相对路径，逐条列出 | `execution.status == failed` 时**必填**（可为空数组，但键必须存在） |
| `execution.unwritten_paths` | `[]string` | 本次执行**未写入**的 vault 相对路径，逐条列出 | 同上 |

**同时**，最终报告（§4.6）侧的义务不变：
`skipped[]` 逐条列出跳过项与原因，`exit_code` 按 §7.2 取 `3`（部分写入）或 `4`（提交失败）。

**两侧必须一致**（本合同新定的一致性规则）：
`execution.written_paths ∪ execution.unwritten_paths` = 本次提案 `targets` 的影响文件全集，
且 `execution.unwritten_paths` ⊇ 报告 `skipped[].target` 中属本提案的条目。

---

## 5. 逻辑删除全时序（`deprecate` / `restore` / `replaced_by` / `delete` / `undelete`）

### 5.1 两个维度**正交**（F3 冻结合同，S1 起生效）

> §5 章首逐字：两个**正交**维度——`status`（`active` / `deprecated`）答「这条判断还算不算数」，
> 删除标记（`deleted_at` 有值 / 无值）答「这份内容还要不要出现在当前知识库里」。
> 「塞进同一枚举会在恢复时被迫猜删除前的状态，因此分成两维（EG-EDIT-06）。」

| 象限 | 默认检索 | 参与收敛/冲突判断 | 作为关系端点默认展示 | 综述取材 | 可显式查看 | 可作 `replaced_by` 目标 |
|-|-|-|-|-|-|-|
| `active` + 未删除 | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `deprecated` + 未删除 | ✅ 标 `[失效]` | 🔴 | 🔴 | 🔴 | ✅ | 🟡 允许但提示（→ **W12**） |
| `active` + 已删除 | 🔴 | 🔴 | 🔴 | 🔴 | ✅ 标 `[已删除]` | 🔴（→ **E10**） |
| `deprecated` + 已删除 | 🔴 | 🔴 | 🔴 | 🔴 | ✅ **双标记** | 🔴（→ **E10**） |

**M3 的新增部分**：S1 的库里只存在第一象限（真值表在 S1 是**读取与展示口径**）；
**M3 首次提供 `deprecated` 与 `deleted_at` 的写入路径**，四象限在 M3 全部可达。

### 5.2 五个动作的语义

| 动作 | 命令 | op | 前置 | 写什么 | 保留什么 | 依据 |
|-|-|-|-|-|-|-|
| **失效** | `eg deprecate --reason` | `deprecate` | 用户发起 + `--reason` 必填 | `status: deprecated` | 关系与依据**全保留，无文件删除**（T-EDIT-04） | §7.1；§14.3 EG-EDIT-04 |
| **恢复失效** | `eg restore --reason` | `restore` | 用户发起 + `--reason`；**不以存在有效 `support` 为前提** | `status: active` | 失效经历、失效原因、`against` 依据**全部保留** | §5.3 表第 1 行 |
| **生命周期指针** | `eg replaced-by` | `set_replaced_by` | 用户发起；**在失效卡上**写 `{ target, reason }` | `replaced_by` | 权威仍在失效卡，**单向存储**，反向由 SQL 完成（**S4**，M3 不做） | §7.1；ADR-15 |
| **逻辑删除** | `eg delete` | `delete` | 用户发起 + **必须引用 `status=approved` 提案** + `--confirm` | `deleted_at` + `deleted_reason` | **关系记录不被物理删除**；四类产物同构 | §7.1；§5.2；ADR-11 |
| **撤销删除** | `eg undelete --reason` | `undelete` | 用户发起 + `--reason`；**不二次确认** | 清空 `deleted_at` / `deleted_reason` | **`status` 原封不动** | §5.3 表第 2 行 |

**同一张卡可多次失效与恢复，每次原因从 Git 历史查阅**（§5.3）。
恢复无有效 `support` 的卡时系统**不拦截**，只在报告与视图打 `[材料支持不足]`（ADR-14）——
但该标记的**判定属 S3**，M3 **不计算、不输出**该标记（§6.2）。

### 5.3 逻辑删除的完整执行时序（§10.2 时序图逐步抄录）

| 步 | 参与方 | 动作 | M3 的硬约束 |
|-|-|-|-|
| 1 | Agent → CLI | `eg proposal new --type logical_delete --target k-xxx` | Agent **允许**调用（§10.3「可提不可执」） |
| 2 | CLI → vault | **直接扫描 Markdown 与 frontmatter 计算影响面** | **S2 不依赖索引**；`.index/` 是 S4 的性能优化，**不是 S2 的前置条件** |
| 3 | vault → CLI | 返回影响面四项 | 见 §5.4 |
| 4 | CLI → vault | 写 `proposals/`（`status: pending`, `execution: not_started`） | 落盘形态见 §7 |
| 5 | CLI → Git | `commit proposal(delete)` | `verb = proposal`（§7.1） |
| 6 | CLI → 用户 | 展示影响面（**唯一一次执行前确认**） | EG-CFM-05 |
| 7 | 用户 → CLI | `eg proposal approve p-xxx --confirm` | 无 `--confirm` → 退 **`6`**，权威 Markdown **完全不变** |
| 8 | CLI | **重算影响面并与提案比对** | **不得省略** |
| 9a | 前提已变化 | 生成新提案 + 原提案标 `superseded`；**不执行删除**，进报告 | 触发 **S-②** |
| 9b | 前提成立 | **逐文件**写 `deleted_at` 与 `deleted_reason`；`status: approved`；`execution: succeeded` | 逐文件 = 非强原子 |
| 10 | CLI → Git | `commit delete(domain)` | `verb = delete` |
| 11 | CLI → 用户 | 报告：**「本次删除没有自动改变任何知识卡的状态」** + 附**建议 `deprecated` 清单** | 逐字要求，见 §5.5 |
| — | 中途失败 | **保留现状、不做破坏性回滚**，`execution = failed` 并**列出已写 / 未写路径** | §10.2 note 逐字；B4 |

**第 9b 步的硬禁令（2026-09-02 owner 对 A-24 的裁决第 5 条逐字下沉，只加严）**：

> 逻辑删除实体时不得级联删除关系。

即：第 9b 步**只**写目标产物的 `deleted_at` / `deleted_reason`，**不得**顺带从任何宿主卡的
`relations[]` 里移除指向被删对象的记录——关系条目的物理移除**只有**用户显式
`eg rel remove` / `remove_relation` 一个入口（§8.5.2 口径 5）。与 §5.2 表「逻辑删除」行
「**关系记录不被物理删除**」、§5.5「关系过滤靠端点有效性，**记录不动**」、F3「状态与删除正交」
三处同一口径。反证归 **T-…-041**（时序）：`go test ./internal/plan -run TestDelete_NoCascadeRemoveRelation -count=1`
全绿 + 删卡后指向它的 `relations[]` 条目数**不变**。

### 5.4 影响面四项（批准前必须算准，EG-CFM-05）

| # | 项 | `impact` 子字段 | M3 口径 |
|-|-|-|-|
| 1 | 会**退出默认视图**的具体内容 | `exits_default_view` | 直接扫描算出 |
| 2 | 会因此**没有剩余有效 `support`** 的知识卡 | `cards_losing_support` | 直接扫描算出 |
| 3 | 受影响的**材料关系与论证关系** | `affected_material_rels` / `affected_relations` | 计数即可 |
| 4 | **可能失准的主题综述** | `stale_reviews` | **S2 只按 `source_cards` 命中给出提示**；「综述失准的**自动判定**属 S3 之后」 |

> §10.2 正文逐字：「确认重点是**影响面**，不是文件是否可恢复。用户批准就是这一次确认，
> **执行时不再二次询问**（EG-CFM-05）。」

### 5.5 删除后的历史保留规则（M3 最容易做错的地方）

| 规则 | 逐字依据 |
|-|-|
| **执行后没有任何知识卡的状态被自动改变** | §5.2：「删除执行后**没有任何知识卡的状态被自动改变**」 |
| 失去有效 `support` 的卡**只产生「建议标记失效」提示**（进报告） | §5.2；报告字段 `support_check[]` 的 `recommendation` |
| 仍保有有效 `support` 的卡收到「建议重新检查材料关系」提示 | §5.2 |
| **用户不处理时它们仍是 `active`** | §5.2（EG-KNW-04、EG-EDIT-06） |
| 被删对象的材料关系、论证关系、`opposing`、`replaced_by` **全部保留** | §5.2 表「知识卡」行：「全部保留，但默认不展示、不参与分析」 |
| 关系过滤靠**端点有效性**，**记录不动** | §5.2 表「实现」列 |
| 报告必须明确写出「**本次删除没有自动改变任何知识卡的状态**」，并附「**建议标记 `deprecated`**」的卡清单 | §10.2 正文逐字 |
| 失效卡仍可**只读匹配**：仅用于防止重复建卡、继续追加 `support`/`against`；**不改状态、不产生恢复建议** | §5.1 正文（EG-EXT-01、EG-KNW-07） |
| **依据累积不因失效或删除停止**：`add_material_rel` 允许目标为 `deprecated` 卡，报告写 `deprecated_new_support[]` | §5.1 正文；§4.6 该字段标 **S2** |

---

## 6. `reviewed_at`、`eg unreviewed` 与显著标记的输出口径

### 6.1 `reviewed_at` 与 `eg unreviewed`

| 项 | 口径 | 依据 |
|-|-|-|
| `reviewed_at` 的更新触发 | 原文两种：①**用户编辑并保存**（直接编辑 Markdown「后者**在对账时补齐**」）、②**用户明确标记已过目**（`eg mark-reviewed`）。**M3 只承接触发②**（对账属 S3/M4） | §5.5；裁决与登记见授权合同 §9 **A-16** |
| 一律**不**更新 `reviewed_at` 的动作 | 被动查看、**结果报告**、索引读取、**Agent 写入** | §5.5 逐字（EG-CFM-06）；§4.6 末尾：「`reviewed_at` 自 **S2** 引入后，报告同样**不更新**它」 |
| `eg unreviewed` | 按 `updated_at > reviewed_at` 筛选，可叠加领域 / 标签 / 时间 | §7.1（EG-VIEW-08） |
| **无 `reviewed_at` 的产物** | **计入** `eg unreviewed`（缺省 `reviewed_at` 视为「从未过目」，判定等价于 `reviewed_at = -∞`） | §14.4 EG-VIEW-08 行验收测试逐字「**无 `reviewed_at` 计入**；不改状态、无催办」 |
| `eg unreviewed` 的副作用 | **不改状态、无催办** | §14.4 T-VIEW-08 |
| **ADR-20 硬约束** | `updated_at > reviewed_at` 是**只读信号**，**只允许出现在筛选条件里**；隔离在 `query/filter/unreviewed.go` 一个文件内，`query/rank`、`query/relations`、`rules/converge`、`query/review` 四个包**不导入**该文件 | §5.5 callout。**CI 依赖方向检查属 M6，M3 只保证文件级隔离事实成立** |

### 6.2 显著标记（ADR-18）：M3 新增恰 2 个

| 标记 | 判定 | JSON 字段 | 阶段 | M3 |
|-|-|-|-|-|
| `[失效]` | `status == deprecated` | `deprecated: true` | S1 口径已冻结 | 沿用，**不改** |
| **`[已删除]`** | `deleted_at != null` | `deleted: true` | **S2** | **M3 新增** |
| **`[未过目]`** | `updated_at > reviewed_at` | `unreviewed: true` | **S2** | **M3 新增** |
| `[材料支持不足]` | 查询时实时判定 | `insufficient_support: true` | **S3** | **M3 不实现、不输出** |

**与 S1 既有约束的衔接**：`2026-09-01-eg-cli-contract.md` §7 与 §7.3 标题已写明
「**S1 仅 `[失效]` 口径可用**，`[已删除]`/`[未过目]` **S2**、`[材料支持不足]` **S3**」——
M3 是**按原定阶段表启用**这两个标记，**不是推翻 S1 约束**。S1 侧「只允许 `[失效]`」
是 S1 阶段结论，随阶段推进由 §7.3 阶段表接管。

**双标记口径**：`deprecated` + 已删除的产物在显式查看时**同时**输出两个标记
（§5.1 真值表第 4 行「✅ **双标记**」）。**标记顺序（本合同新定）**：
按 §7.3 的定义顺序输出，即 `[失效][已删除]`，再叠加 `[未过目]`。
理由：原文未定义顺序，需要一个确定性口径才能写逐字比对的测试（M2 已冻结「同一语料两次执行
输出逐字相同」）。

**提案自身不参与标记与检索**：`eg context` 只输出 `proposals/` 的**标题与 targets 摘要**用于去重，
**不输出正文**；提案在任何知识检索与综述取材里一律按 `kind='proposal'` 排除。
**S2 靠目录过滤实现**（`proposals/` 不进知识扫描范围），S4 起改由索引的 `kind` 列过滤，**口径不变**（§10.3）。

---

## 7. 提案在 Markdown 上的落盘形态

### 7.1 目录与文件名（与 F1 / §3.1 逐条对齐）

| 项 | 值 | 依据 | 是否冲突 F1 |
|-|-|-|-|
| 目录 | `vault/proposals/`，**一项一文件，不属于任何领域** | §3 目录树 `proposals/` 行标 **[S2]**；F1 已把该目录名写进冻结合同 | **不冲突**（F1 已含 `proposals/`） |
| S1 是否需存在 | **可以不存在**；`eg context` 在 S1 直接跳过提案去重 | §3 表 `proposals/` 行 | — |
| ID 规则 | `p-<yyyymmdd>-<seq>`，`<seq>` 是**当日三位序号** | §3.1 表「提案」行 + 正文 | 不冲突 F2 |
| 默认文件名 | `<id>.md`，例：`p-20260701-001.md` | §3 目录树；§3.1「默认文件名是 `<id>.md`」 | — |
| 文件可否重命名 / 移动 | **可以**：ID 写在 frontmatter，`id → path` 在 S1/M3 由**扫描**完成 | §3.1 正文 | 不冲突 F2 |
| 处理过的提案 | **留在 `proposals/` 原地，不搬目录** | §10.1 第 3 条 | — |
| 文件形态 | frontmatter（十项必备）+ 影响面正文 | §3.1 表「提案」行 | — |

### 7.2 frontmatter 键（§4.4 yaml 逐键抄录 + 本合同新增 2 键）

```yaml
---
id: p-20260701-001
type: logical_delete             # v1 唯一类型
status: pending                  # pending | approved | rejected | superseded（用户决定）
created_at: 2026-07-01
targets: [s-20260605-vendor-bench]        # 本次原子操作的删除对象
impact:                          # 批准前算准的影响面（EG-CFM-05）
  exits_default_view: [s-20260605-vendor-bench]
  cards_losing_support: [k-20260412-moe-routing-cost]
  affected_material_rels: 2
  affected_relations: 0
  stale_reviews: [r-20260620-moe-cost]
decision:                        # 用户决定
  result:                        # approved | rejected | superseded
  reason:
  superseded_by:                 # 新提案 ID
execution:                       # 实际执行结果，与 status 严格分离
  status: not_started            # not_started | succeeded | failed
  attempted_at:
  reason:
  git_commit:
  written_paths: []              # 【本合同新定】failed 时必填，见 §4.3 与登记项 A-21
  unwritten_paths: []            # 【本合同新定】failed 时必填，见 §4.3 与登记项 A-21
---
```

**`status` 与 `decision.result` 的关系（本合同新定裁决）**：两者取值域重叠
（`approved` / `rejected` / `superseded`），原文未定义一致性 → 登记 **A-20**。
本合同定：**顶层 `status` 是权威**；`decision.result` 必须与 `status` **逐字相等**，
`status == pending` 时 `decision.result` 必须为**空**。不满足即 **E8**。
理由：`status` 是状态机的唯一载体（§10 状态图、§10.1「四取值」），
`decision.result` 是「用户决定」的记录副本；两值不一致时无法判定以谁为准，
必须先定权威侧才能写状态机测试。

**提案不设 `reviewed_at` / `deleted_at` / `deleted_reason`（本合同新定）**：
§4.4 的提案 yaml **没有**这三个键（而原文、综述、知识卡的 yaml 都有）。本合同据此定：
提案**不是知识产物**，不进 `eg unreviewed` 筛选，**不可被逻辑删除**。
理由：`reject` 已提供「无副作用的终止路径」（§10.3），再叠一层逻辑删除属冗余；
且 §5.2 的「逻辑删除四类影响面」枚举的被删对象**恰为原文 / 材料笔记 / 知识卡 / 主题综述四类**，
**不含提案**。

### 7.3 正文分区（恰 7 个 H2，§4.4 逐字抄录）

```markdown
## 推荐修改
## 理由与证据
## 影响的文件、领域与关系
## 执行后状态
## 不执行的影响
## 替代方案
## 可应用内容
```

**与 F5 的关系（必须写清，否则易被误判为冲突）**：
F5 冻结的是「**知识卡五分区 + 材料笔记五分区**」的分区名与含义（§1.2 F5 行逐字）。
**提案的七分区是另一套正文结构，不在 F5 的冻结范围内**，因此
「提案有 7 个 H2」**不构成对 F5 的推翻**。§4.2「五个分区标题固定为 H2」一节的适用对象
同样是知识卡与材料笔记。

### 7.4 十项必备与 S2 的字段完整度收敛（§10.1 表）

| 内容项 | S2 | 缺失时 |
|-|-|-|
| `status`（四态） | **必须** | **E8** |
| 推荐修改 | **必须** | **W9** |
| 理由与证据（非空，可追溯到具体卡 / 笔记 ID） | **必须** | **W9** |
| 影响范围 `impact`（S2 可只给直接命中项） | **必须** | **W9** |
| 可应用内容（批准后直接可执行的 op） | **必须** | **W9** |
| 用户决定 `decision`（含 `reason` 与 `superseded_by`） | **必须** | **W9**（`superseded` 时缺 `superseded_by` 升 **E7**） |
| 执行结果 `execution`（含 `failed` + `reason` + 已写 / 未写路径） | **必须** | **W9** |
| 执行后状态 · 不执行的影响 · 替代方案 | **S2 之后** | 模板分区**先留着**，内容缺失记 **W9**，随使用反馈补齐 |

> §10.1 逐字：「S2 落地时先满足下表「S2 必须」的**七项**，其余缺项按 **warning** 处理，
> **不拒绝创建提案**。」

### 7.5 与 F6 的关系

| F6 要求 | M3 的落地 |
|-|-|
| Markdown 是权威 | 提案的 `status` / `decision` / `execution` **全部落在 `proposals/*.md` 的 frontmatter**，无第二份状态 |
| `.index/` 是可重建派生 | **M3 不建 `.index/`**；提案的检索与去重靠**目录过滤 + 直接扫描**（§10.3） |
| 删掉 `.index/` 后语义不变 | M3 天然成立（无索引） |

---

## 8. 提案与 ChangePlan 的关系：M3 新增 op 与诊断码

### 8.1 M3 新增 op（恰 8 个，均有直接原文或冻结裁决依据）

§4.5 明文：「**op 是开放集合**：`ops[].op` 的取值随阶段增补，**未知 op 报错（error，整条 op 不执行）**」。
下表 8 个 op 全部来自 §4.5 op 表的 **S2** 行，且各有 §16.1 M3 交付列或冻结裁决背书。

| # | op | 字段 | 授权要求 | 主要诊断码 | 依据 |
|-|-|-|-|-|-|
| 1 | `deprecate` | `target`（ID）、`reason`（必填）、`initiator: user` | **用户发起专属** | W7（升 error）、W11 | §4.5 op 表；§16.1 M3「`deprecate`」 |
| 2 | `restore` | `target`、`reason`（必填）、`initiator: user` | **用户发起专属** | W7（升 error）、W11 | §4.5；§5.3；§16.1 |
| 3 | `set_replaced_by` | `target`（失效卡）、`replaced_by.target`、`replaced_by.reason`、`initiator: user` | **用户发起专属** | W7（升 error）、**E10**、**W12**、W2 | §4.5；§7.1 `eg replaced-by`；§16.1「`replaced_by`」；ADR-15 |
| 4 | `delete` | `target`、`reason`、`proposal`（**必须引用 `status=approved` 提案**）、`initiator: user` | **用户发起专属 + 已批准提案** | W7（升 error）、**E7/E8/E9** | §4.5 op 表逐字「`delete` 还需已批准提案」；§10.3 V10；§16.1「逻辑删除全时序」 |
| 5 | `undelete` | `target`、`reason`（必填）、`initiator: user` | **用户发起专属** | W7（升 error）、W11 | §4.5；§5.3；§16.1 |
| 6 | `mark_reviewed` | `target`、`initiator: user` | 用户发起 | W7（**A-15 窄口径：warning**） | §4.5；§7.1 `eg mark-reviewed`；§16.1「`reviewed_at`」 |
| 7 | `replace_block` | `target`、`section: 理解自检`、`block`、**`base_block_hash`**（必带） | 仅用于更新「**当前有效自检问题块**」 | `skipped[kind=file_changed, cause=content_hash_mismatch]`（见 §8.3 加严说明） | §4.5 op 表逐字；§4.2 分区表第 5 行；§16.1「自检问题更新」 |
| 8 | `remove_relation` | `from`、`type`、`target`、`reason`、`initiator: user` | 用户发起（**本合同新定 N-3**） | **W10**、W2 | §4.5 op 表（标 S2）；**冻结裁决：`rel remove` + `remove_relation` 属 M3/S2**（`M-002-m2.md` A-6、CLI 合同附录、`2026-09-19-m2-query-contract.md`） |

**M3 接管 M2 的 `eg rel remove` 占位**：M2 已实现该命令的占位（退 `1`、逐字输出
「**M3/S2 未实现**」、零写入零 commit）。M3 的义务是**接管**该占位：
把逐字文案与 `MilestoneTag` 换成真实实现，并保证 `grep -n "M3/S2 未实现" internal/cli/rel.go`
在 M3 收口后**零命中**。

**归属未知的 3 个 S2 op**：`set_tags`、`reprocess_note`、`save_review` 在 §4.5 标 **S2**，
但 §16.1 M3 交付列**未点名**。其 M3 归属 **未知，待 owner 确认**（授权合同 §9 **A-18**），
本合同**不为其定义字段与诊断码**。

**提案自身的创建 / 批准是否走 ChangePlan：原文未定义** → 登记 **A-23**。
**2026-09-02 owner 裁决已关闭 A-23**：`CLI 直写例外`（仅限 `proposals/**` 提案控制面，必须复用 guarded store，禁止裸写文件；CLI 负责 Git 与报告；批准后的知识数据修改仍走 ChangePlan；Agent 可创建提案，但不可批准或执行）——逐条落地见 **§8.5.1**，故本合同**仍不新增**提案类 op，§8.1 的 op 集合保持**恰 8 个**。

### 8.2 诊断码：新增编号严格避开 E1–E6 / W1–W8 / I1

**已占用区间**（`2026-09-08-changeplan-contract.md` §4.1 / §4.2）：
error `E1`–`E6`、warning `W1`–`W8`、info `I1`。
**M3 新增编号从 `E7` / `W9` 起，不新增 info 编号。**

#### 8.2.1 新增 error（恰 4 条：**E7 – E10**）

| 编号 | 触发条件 | CLI 行为 | 规则出处 | 编号性质 |
|-|-|-|-|-|
| **E7** | `status: superseded` 但 `decision.superseded_by` 为空，或无法解析到一个存在的提案 ID | 退 `2`，零写入 | §10.1 第 4 条逐字「**两者都必须写 `decision.superseded_by`**」 | **编号本合同新定**（规则出自原文）。判 error 的理由：提案链断裂后**后继提案无法定位**，符合 §4.5.1「关系无法定位的问题才是 error」 |
| **E8** | 提案 `status` 不在四取值内；或 `execution.status` 不在三取值内；或 `decision.result` 与 `status` **不一致**（`pending` 时 `decision.result` 非空亦算） | 退 `2`，零写入 | §10.1「四取值，**不存在第五种**」；§4.4 枚举注释；一致性规则见 §7.2（**本合同新定**） | **编号本合同新定**；一致性子句**本合同新定** |
| **E9** | **非法 `status` 迁移**（§2.4 表 12 条之一）；或落盘的 `status` × `execution` 组合命中 §4.2 可达矩阵的 🔴 / ⚠️ 格 | 退 `2`，零写入 | §10 状态图；§4.2 可达矩阵 | **编号本合同新定**（规则出自原文状态图） |
| **E10** | `set_replaced_by` 的 `target` **已被逻辑删除**（`deleted_at` 非空） | 退 `2`，零写入 | §5.1 真值表「可作 `replaced_by` 目标」列：两个「已删除」象限均标 **🔴** | **编号本合同新定**（规则出自原文真值表） |

#### 8.2.2 新增 warning（恰 4 条：**W9 – W12**）

| 编号 | 触发条件 | CLI 行为 | 规则出处 | 编号性质 |
|-|-|-|-|-|
| **W9** | **提案十项必备缺项**（`impact` 子字段不全、正文分区为空、`decision`/`execution` 子字段缺失等） | **照常创建提案** + 进报告 `warnings[]` | §10 章首「缺项按 **warning** 处理而不是拒绝创建」；§10.1 表末行 | **编号本合同新定**（规则出自原文） |
| **W10** | `remove_relation` **未命中任何既有关系**（幂等 no-op） | 该 op **零写入、零 commit**（不改字节、不产生空 commit） + 进报告；**不拦截其余 op**，退出码不受影响 | **原文未定义**；「零 commit」一句为 **2026-09-02 owner 对 A-24 的裁决第 4 条**逐字下沉（见 §8.5.2 口径 4），只加严 | **规则与编号均本合同新定**。理由：与 §7.5 已冻结的幂等语义方向一致（「重复执行结果收敛、不产生空 commit」）；判 error 会让重跑同一 plan 失败，违反 §9.3「重跑同一 plan 幂等」 |
| **W11** | `deprecate` 目标已是 `deprecated`；或 `restore` 目标已是 `active`；或 `undelete` 目标未被删除（三类幂等 no-op） | 该 op 零写入 + 进报告 | **原文未定义** | **规则与编号均本合同新定**，理由同 W10 |
| **W12** | `set_replaced_by` 的 `target` 是 `deprecated` **且未删除** | **照常写入** + 进报告提示 | §5.1 真值表该象限标 **🟡「允许但提示」**；符号口径「🟡 不拦截但进报告提示」 | **编号本合同新定**（规则出自原文真值表） |

#### 8.2.3 既有编号在 M3 的分级变化（**不新增编号**）

| 编号 | S1 分级 | **M3 分级** | 逐字依据 |
|-|-|-|-|
| **W7** | warning（S1 无状态类 op，保留占位） | **error** | §4.5.1 W7 行逐字：「状态类 op 缺 `initiator=user` 或 `reason`；`delete` 未引用已批准提案 \| Agent 自动失效 / 删除 \| **S2 起 error**（S1 无这些 op）」。§10.3 亦称「这两条自 S2 起就是 **error 级**，**不随 §4.5.1 的宽松口径放宽**」 |
| **W3** | warning（S1 无失效卡，正常链路不产生） | **warning，但 M3 起真正开始判定** | §4.5.1 W3 行逐字：「论证关系某一端不是 `active` 或已被逻辑删除 \| warning（**S2 起判定**、S5 起 error）」 |

**V9 / V10 的接驳**：§10.3 引用的校验编号 **V9**（要求 `initiator=user`）与 **V10**
（`delete` op 必须引用 `status=approved` 提案）**在全文无定义表**（登记 **A-14**）。
本合同判定 **V9 ≡ W7 前半、V10 ≡ W7 后半**，因此 **M3 不为它们新增 error 编号**。

#### 8.2.4 编号占用总览（M3 收口后）

| 级别 | S1 已占用 | M3 新增 | M3 后全集 | 预留 |
|-|-|-|-|-|
| error | `E1`–`E6` | **`E7`–`E10`** | `E1`–`E10` | `E11`+ 留给 M4–M6 |
| warning | `W1`–`W8` | **`W9`–`W12`** | `W1`–`W12` | `W13`+ 留给 M4–M6 |
| info | `I1` | **无** | `I1` | `I2`+ 留给 M4–M6 |

**诊断载荷结构不变**：仍是 CLI 合同 §5 的**六字段**（`code` / `level` / `path` / `op_index` /
`message` / `target`），**不新增第七个字段**。

**但 `code` 的取值枚举必须同步（见 M-003 登记项 A-29）**：`2026-09-01-eg-cli-contract.md` §5 把
`code` 的取值逐字写成「`E1`–`E6` / `W1`–`W8` / `I1`」——那是**S1 阶段结论**。M3 落地 `E7`–`E10` /
`W9`–`W12` 后，该枚举句必须由 **T-…-046** 在 CLI 合同里**追加阶段脚注**（只追加、不改写 S1 结论），
否则会出现「本仓两份合同互相矛盾」。**M3 的实现不得先于该同步动作扩大 `code` 的取值**——
判定见 M-003 完成判据 15 的集合闭合断言。

### 8.3 `skipped[].kind` 与报告字段

**不新增 `kind`（2026-09-02 加严，M2 实证订正）**：`replace_block` 的块级冲突
**复用既有封闭两值**中的 `{"kind": "file_changed", "cause": "content_hash_mismatch"}`，
块 locator 与 `base_block_hash` 落在**自由文本** `detail` 里（`detail` 不属封闭枚举）。

> **为什么改（原写法是自相矛盾的，必须订正）**：本节标题逐字「不新增 `kind`」，但原文让
> `replace_block` 用 `block_conflict` —— 而 `github.com/ikaqiu-Lemon/EverGreen/internal/store/receipt.go` 的
> `SkipReason` 是**恰两值**封闭常量（`file_changed` / `user_block_unsafe`），`CauseFor` 的注释
> 逐字「S1 的 kind 恰两值，没有第三种」。用 `block_conflict` 就**是**新增第三值，与本节标题、
> 与 M-003 完成判据 15「`skipped[].kind` 不新增」直接冲突。更硬的反证：
> `evergreen/test/e2e/m1_test.go:660` 把 `block_conflict` / `block_hash_changed` 列进**禁止字面量**表，
> 而 M-003 完成判据 17 要求「M1 / M2 的用例一条不得删改」——两者不可同时成立。
> 本次取**只加严不放宽**的一侧：封闭集合仍**恰两值**，不扩张。这与 M-003 风险 **R-13**
> 冻结的「整文件跳过口径在 M3 保持不变」也完全一致（块级合并属 S5，M3 不做）。
> 反证命令：`cd evergreen && grep -rn "block_conflict\|block_hash_changed" internal/ | wc -l` → **0**。

**S1 侧行文不动（登记，不改写）**：`2026-09-08-changeplan-contract.md` §4.6 那句「`block_conflict` /
`block_hash_changed` **属 S2 块替换路径**，S1 不使用」**逐字保留、一个字不改** —— 它是 T-…-011 的验收断言
（「四个取值名各 `grep -c` ≥ 1」）与 `round4_final_gate.py` / `gate_common.py` 反证词表的依据，改它会同时
撞坏 M1 门禁与「M1 用例一条不得删改」。本节只是在 **S2 侧**裁决「这条预留路径 M3 不启用」：
两个名字**永久保留为未启用预留字面量**，S1 不用、S2 也不用，`internal/` 与报告输出中恒为 **0** 命中。
若 M4+ 真要做块级合并（S5 范围），届时须另开裁决重新评估，**不得**在 M3 窗口内悄悄启用。

**M3 首次产出值的报告字段**（§4.6 表标 **S2** 的两项）：

> **阶段冲突提示（见 M-003 登记项 A-28）**：飞书 §4.6 把 `support_check[]` 整列标 **S3**「S1 不计算，输出空值」，
> 但 §5.2 与 §10.2 又逐字要求 **S2** 的删除报告「附**建议标记 `deprecated`** 的卡清单」。
> 本合同按「路径分治」落地：**只有 `eg delete` 路径**产出 `recommendation` 条目，其余路径与其余子字段一律留空，
> 对账派生部分仍属 S3/M4。M3 不因此把 `support_check[]` 整列提前到 S2。

| 字段 | S1 | M3 |
|-|-|-|
| `proposals[]` | 空数组 | **产出真实值**（PRD 十项必备 ⑦） |
| `deprecated_new_support[]` | 空数组 | **产出真实值**（失效卡出现新支持材料） |
| `support_check[]`（**仅 `eg delete` 路径**） | 空数组 | **产出 `recommendation` 条目**：失去有效 `support` 的卡给「建议标记 `deprecated`」、仍保有有效 `support` 的卡给「建议重新检查材料关系」。**只产 `recommendation`，不产任何对账派生字段** |
| `support_check[]`（`eg delete` 以外的一切命令路径） | 空数组 | **仍为空数组** |
| `affected` / `reconcile` | 空值 / `{"ran": false}` | **仍为空值 / `{"ran": false}`**（属 S3/M4，M3 不计算） |

---

### 8.4 八个新增 op 与 `convergence[]` 校验合同（承接 T-…-030 / T-…-031，2026-09-02 新增，只加严）

**为什么必须有本节**：T-…-030 冻结了「专用关系命令的 convergence 校验合同」，T-…-031 已在
`github.com/ikaqiu-Lemon/EverGreen/internal/plan/validate.go` 落地为**窄例外** `isDedicatedRelationPlan()`
（`verb == relate` **且** `ops` 恰一条 **且** `ops[0].op == add_relation` → 空 `convergence[]` 不出 **W5**）。
M3 新增 8 个 op 后，「哪些 op 会把 plan 拖进 W5 判定、哪些落入窄例外」**原合同一条都没写** ——
不写清就会重演 I-…-001（为过校验而伪造收敛条目）。本节逐 op 关闭该缺口。

**M2 实证的判定形状**（`internal/plan/validate.go`，只读核对，非假设）：

- `convergence()`：`len(p.Convergence) == 0` **且** `touchesExistingCard()` **且** `!isDedicatedRelationPlan()` → 出 **W5**「缺条目」；
- `touchesExistingCard()` 当前的成员**恰三个**：`append_card` / `add_relation` / `add_material_rel`；
- `convergence[]` **非空**时，逐条校验三维度 + 七值处理关系，异常一律并入 W5（不新增编号）。

| # | op | 计入 `touchesExistingCard()` | 是否要求 `convergence[]` | 落入窄例外 | 依据 |
|-|-|-|-|-|-|
| 1 | `deprecate` | **否** | **不要求** | 不适用 | 只改 `status` 标量，不产生新的知识语义；反推三维度即伪造证据（T-…-030 逐字禁止「CLI 反推三维度或伪造条目」） |
| 2 | `restore` | **否** | **不要求** | 不适用 | 同上 |
| 3 | `set_replaced_by` | **否** | **不要求** | 不适用 | 生命周期指针，非语义加工；替代关系的理由由 `replaced_by.reason` 承载 |
| 4 | `delete` | **否** | **不要求** | 不适用 | 语义论证由 `status=approved` 提案的七个 H2 承载，**强于** `convergence[]`；再要一份等于重复举证 |
| 5 | `undelete` | **否** | **不要求** | 不适用 | 同 `delete` 的反向操作 |
| 6 | `mark_reviewed` | **否** | **不要求** | 不适用 | 只写 `reviewed_at`，ADR-20 明定它是**只读信号**，不得进入收敛判定 |
| 7 | `replace_block` | **否** | **不要求** | 不适用 | 只换「当前有效自检问题块」，是自检元数据，不是三维度结论 |
| 8 | `remove_relation` | **是**（与 `add_relation` **对称**，必须加入） | **不要求** | **是**（窄例外须同步扩写） | T-…-030 Acceptance 逐字「M3 的 `rel remove` 可复用该原则」 |

**三条加严条款（M-13 ~ M-15，本合同新定）**

- **M-13（`remove_relation` 必须计入 `touchesExistingCard()`）**：`isDedicatedRelationPlan()` 扩写为
  「`verb == relate` **且** `ops` 恰一条 **且** `ops[0].op ∈ {add_relation, remove_relation}`」，**同时**
  把 `remove_relation` 加进 `touchesExistingCard()` 的成员集合。**两处必须同时改**：只改窄例外而不计入
  `touchesExistingCard()`，会让一份 `verb=process` 的多 op plan 夹带 `remove_relation` 时**静默不判 W5** ——
  那是放宽，**禁止**。反证：`cd evergreen && go test ./internal/plan -run TestRemoveRelation_CountedInTouchesExistingCard -count=1` 全绿。
- **M-14（「不计入」不等于「可抵消」）**：第 1–7 个 op **不计入**只表示「自己不触发 W5」。若它们与
  `append_card` / `add_relation` / `add_material_rel` 同处一份 `verb=process` / `reprocess` 的 plan，
  该 plan 仍因后者触发 W5，**七个 op 不因此获得豁免**。反证：
  `cd evergreen && go test ./internal/plan -run TestStateOps_DoNotSuppressW5_WhenMixed -count=1` 全绿。
- **M-15（窄例外不得外溢到状态类 op）**：窄例外**只认** `verb == relate`。不得出现
  `verb=relate` + 单条 `deprecate` / `delete` 这类「借关系动词躲开 W5」的形态；`verb` 与 op 的配对必须
  逐对可判。反证：`cd evergreen && go test ./internal/plan -run TestDedicatedRelationPlan_VerbOpPairing -count=1`
  全绿——表驱动至少 3 行（`relate`+`add_relation` → 例外成立；`relate`+`remove_relation` → 例外成立；
  `relate`+`deprecate` → 例外**不**成立）。

**责任落点**：M-13 / M-14 / M-15 的实现与用例归 **T-…-037**（`plan` 层 op 与校验底座），
`remove_relation` 的命令侧复算归 **T-…-044**。M2 已冻结的 `add_relation` 例外行为**一字不改**
（`go test ./internal/plan -run Convergence -count=1` 与 `bash test/e2e/m2_rel_add.sh` 必须继续全绿）。

### 8.5 owner 裁决落地：A-23 `CLI 直写例外` / A-24 `物理移除`（2026-09-02，只加严）

**来源**：owner **项目维护者 `<maintainer@example.com>`** 于 **2026-09-02** 正式裁决，逐字原文与逐条加严含义见
`docs/specs/2026-10-13-m3-prestart-adjudication.md` **§7**。本节是该裁决在本合同内的**落地条款**：
**只加严、不放宽**，且**不新增 op、不新增诊断码、不新增 `skipped[].kind`、不改四态与 12 条非法边**。
§10 登记表的 A-23 / A-24 两行**已同步关闭**（关闭条件列记明入口）。

#### 8.5.1 A-23 落地：提案控制面走 CLI 直写例外（附加约束 5 条，逐字）

> 1. 仅限 `proposals/**` 提案控制面。
> 2. 必须复用 guarded store，禁止裸写文件。
> 3. CLI 负责 Git 与报告。
> 4. 批准后的知识数据修改仍走 ChangePlan。
> 5. Agent 可创建提案，但不可批准或执行。（**写权限矩阵侧的落地在授权合同 §2.9**）

**§4.5「ChangePlan 是 Agent → CLI 的唯一程序化写入通道」的例外声明**（本节即该声明，A-23 要求的
「在 §4.5 补例外声明」由此满足）：

| 面 | 写入通道 | 例外是否适用 | 反证（`cd evergreen`） |
|-|-|-|-|
| **提案控制面** = `proposals/**` 的创建与状态变更（`status` / `decision` / `execution`） | CLI 直写，**经 guarded store**（`internal/store` 的写口 + B1–B4 前置检查 + B3 `content_hash` 比对） | **适用**（仅此一面） | `grep -rn "content_hash\|ContentHash" internal/proposal/ \| wc -l` → **≥ 1** |
| **知识数据面** = 五分区权威 Markdown（`sources/` / `notes/` / `cards/` / `domains/` / `reviews/`） | 恒为 `internal/plan` → `internal/store`，**批准后也不例外** | **不适用** | `grep -rn "vault/cards\|vault/notes\|vault/sources" internal/proposal/ \| wc -l` → **0** |
| **裸写文件**（`os.WriteFile` / `os.Create` / `os.OpenFile` / `ioutil.WriteFile`） | **禁止**（直写例外**不等于**绕过 B3 校验） | **永不适用** | `grep -rnE "os\.(WriteFile\|Create\|OpenFile)\|ioutil\.WriteFile" internal/proposal/ \| grep -v _test.go \| wc -l` → **0** |
| **Git 与报告** | **CLI 负责**：commit 与报告体由 CLI 层发起，`internal/proposal` 既不发 commit 也不组报告 | — | `grep -rn "repo.Commit\|git.New\|internal/report" internal/proposal/ \| grep -v _test.go \| wc -l` → **0** |

**两条路径分离（第 4 条的精确含义）**：`eg proposal approve` 通过后，**批准这一动作本身不产生任何知识数据写入**；
真实的知识改动仍是 ChangePlan 的 op #4 `delete`（§8.1 逐字「`delete` 还需已批准提案」）。
即：**提案控制面 = CLI 直写例外，知识数据面 = ChangePlan**，两者**不得**互相借道。
反证：`grep -rn "internal/plan" internal/proposal/ | wc -l` → **0**（提案包不反向依赖 plan）；
`go test ./internal/cli -run TestApprove_NoKnowledgeWriteWithoutChangePlan -count=1` 全绿。

**U-07 的精确适用范围**（与授权合同 §6 一致，不放宽）：U-07「绕过 ChangePlan 直接调 `store`」
约束的是 **Agent 自动路径**；用户发起的 CLI 命令（`eg init` / `eg capture` / 提案三命令）不在其列，
但**必须**经 guarded store —— 任何路径都**不得**裸写文件。

#### 8.5.2 A-24 落地：`remove_relation` 的落盘语义 = 物理移除（附加约束 5 条，逐字）

> 1. 从 `relations[]` 删除规范化 `(from,type,target)` 匹配的全部记录。
> 2. `opposing` 先按 ID 字典序规范化。
> 3. 不留墓碑，不创建 RelationID。
> 4. 未命中按 W10 幂等处理，零写入、零 commit。
> 5. 逻辑删除实体时不得级联删除关系。

| # | 落地口径（本合同据此定死 `remove_relation` 的落盘语义） | 反证（`cd evergreen`） |
|-|-|-|
| 1 | 匹配键 = **规范化后的三元组** `(from, type, target)`；命中即从宿主卡 frontmatter 的 `relations[]` **物理移除**，且移除**匹配的全部记录**（历史遗留的重复条目一并清掉，不是只删首条）。`reason` **不参与**匹配 | `go test ./internal/store -run TestRemoveRelation_RemovesAllNormalizedMatches -count=1` 全绿（夹具含同三元组两条，删后 `relations[]` 条目数 **−2**） |
| 2 | `opposing` **先按两端 ID 字典序规范化**再匹配：与既有「`opposing` 单向存储、必须写在字典序较小的一端」（`internal/rules` 唯一实现、W8、`store.ErrOpposingNotNormalized`）**同一套规范化**，删除方向无关（`a→b` 与 `b→a` 命中同一条记录） | `go test ./internal/store -run TestRemoveRelation_OpposingNormalizedBeforeMatch -count=1` 全绿；`grep -rn "internal/rules" internal/store/relation.go \| wc -l` → **≥ 1**（复用唯一实现，不另写一套） |
| 3 | **不留墓碑，不创建 RelationID**：`Relation` 结构体仍**恰三字段**（`type` / `target` / `reason`），不加 `removed_at` 之类墓碑位，也不给关系条目引入任何新主键；F4 关系类型集合仍**恰四值** | `grep -rn "removed_at\|RemovedAt\|RelationID\|relation_id" internal/ \| grep -v _test.go \| wc -l` → **0**；`go test ./internal/model -run TestRelation_ExactlyThreeFields -count=1` 全绿 |
| 4 | **未命中按 W10 幂等处理，零写入、零 commit**：该 op 不写任何字节、不产生空 commit、不拦截同一 plan 的其余 op、退出码不受影响（§8.2.2 W10 一字不改） | `go test ./internal/plan -run TestW10_RemoveRelationNoMatch -count=1` 全绿；e2e 断言 `git log --oneline \| wc -l` **不变**、目标文件字节不变 |
| 5 | **逻辑删除实体时不得级联删除关系**：`delete` / `deprecate` 走的是 §5.2 的「关系记录不被物理删除」「端点有效性过滤，**记录不动**」，与本 op **正交**（F3「状态与删除正交」）。只有用户显式 `eg rel remove` / `remove_relation` 才移除记录 | `go test ./internal/store -run TestLogicalDelete_RelationsPreserved -count=1` 全绿（删卡后指向它的 `relations[]` 条目数**不变**）；`go test ./internal/plan -run TestDelete_NoCascadeRemoveRelation -count=1` 全绿 |

**B3 不放宽**：物理移除同样走 `ExpectedHash` 前置比对，冲突进 `skipped{kind: file_changed}`
（`kind` 仍**恰两值**，见 §8.3）。**B1 不放宽**：Git 历史承载删除前的完整信息，不靠墓碑。

**责任落点**：口径 1–4 的 op 字段与校验归 **T-…-037**，命令与端到端复算归 **T-…-044**；
口径 5 的时序反证归 **T-…-041**（逻辑删除十一步）与 **T-…-043**（标记渲染）。

---


## 9. 机器可判定方式（供后续 task 的 Acceptance 直接引用）

| 规则 | 判定方式 |
|-|-|
| 四态封闭，无第五值 | `go test ./internal/proposal -run TestStatus_ExactlyFourValues`（`draft`/`applied` 等 → **E8**、退 `2`） |
| 合法边恰 5 条 | `go test ./internal/proposal -run TestStateMachine_LegalTransitions`：**表驱动 5 行**（T0–T4）全绿 |
| 非法边恰 12 条 | `go test ./internal/proposal -run TestStateMachine_IllegalTransitions`：**表驱动 12 行**，每行断言 **E9** + 退 `2` + 目标文件字节不变 |
| 终态不可再迁 | `go test ./internal/proposal -run TestTerminalStates`：`rejected` / `superseded` 的任意出边 → **E9** |
| **`superseded` 触发①** | `go test ./internal/proposal -run TestSuperseded_TriggerPartialAccept`：断言三件事——新提案存在、原提案 `status=superseded`、原提案 `decision.superseded_by == 新提案 ID` |
| **`superseded` 触发②** | `go test ./internal/proposal -run TestSuperseded_TriggerImpactChanged`：批准前改动语料使影响面变化 → **不执行删除**、生成新提案、原提案 `superseded`、报告含「不执行」 |
| 两触发都必写 `superseded_by` | `go test ./internal/proposal -run TestSupersededBy_Required` → 缺失即 **E7** |
| `approve` 必须重算影响面 | `cd evergreen && grep -n "recomputeImpact\|RecomputeImpact" internal/cli/proposal.go \| wc -l` → **≥ 1**；配合 `TestApprove_RecomputesImpactBeforeExecute` |
| `status` × `execution` 可达矩阵 | `go test ./internal/proposal -run TestOrthogonality_ReachabilityMatrix`：**表驱动 12 行**（6 ✅ / 6 拒绝） |
| `approved` 不等于已执行 | `go test ./internal/proposal -run TestApprovedDoesNotImplySucceeded`：`approve` 后立即读盘，断言 `execution.status == not_started` |
| **`execution=failed` 逐路径列出** | `go test ./internal/proposal -run TestExecutionFailed_ListsWrittenAndUnwrittenPaths`：注入第二个文件写失败 → 断言 `execution.status==failed`、`written_paths` 非空、`unwritten_paths` 非空、二者并集 == 影响文件全集、报告 `skipped[]` 可定位 |
| 失败不回滚 | `cd evergreen && grep -rnE "checkout --\|reset --hard" internal/ \| grep -v _test.go \| wc -l` → **0** |
| 删除后无自动状态变化 | `go test ./internal/cli -run TestDelete_NoAutoStatusChange`：断言全部卡 `status` 逐字未变；报告含逐字串「本次删除没有自动改变任何知识卡的状态」 |
| 建议清单进报告 | `eg delete … --json \| jq -e '.data.support_check[]?.recommendation'` → 命中「建议标记 `deprecated`」 |
| 关系记录不被物理删除 | `go test ./internal/store -run TestLogicalDelete_RelationsPreserved`：删除后 `relations[]` / `sources[]` / `opposing` / `replaced_by` 条目数**逐条不变** |
| 恢复不校验 `support` | `go test ./internal/cli -run TestRestore_NoSupportPrecondition` → 退 `0` |
| `undelete` 不动 `status` | `go test ./internal/cli -run TestUndelete_StatusUntouched` |
| `[已删除]` / `[未过目]` 标记 | `go test ./internal/cli -run TestMarkers_DeletedAndUnreviewed`；双标记顺序断言 `[失效][已删除]` |
| `[材料支持不足]` 不在 M3 | `cd evergreen && grep -rn "材料支持不足\|insufficient_support" internal/ \| grep -v _test.go \| wc -l` → **0** |
| ADR-20 隔离 | `cd evergreen && grep -rln "filter/unreviewed" internal/query/rank internal/query/relations internal/rules/converge internal/query/review 2>/dev/null \| wc -l` → **0** |
| 提案不进知识检索 | `go test ./internal/query -run TestProposalsExcludedFromKnowledgeScan`：`eg search` / 综述取材命中 `proposals/` 条目数 = **0** |
| `eg context` 只给摘要 | `go test ./internal/query -run TestContext_ProposalSummaryOnly`：输出含 title + targets，**不含提案正文** |
| 提案不阻塞 | `go test ./internal/cli -run TestPendingProposalDoesNotBlock`：存在 pending 提案时 `eg apply` 退 `0` |
| 落盘形态 | `go test ./internal/proposal -run TestProposalFileLayout`：路径 `vault/proposals/p-<yyyymmdd>-<3d>.md`、frontmatter 键集合、正文**恰 7 个 H2** |
| 提案无 `reviewed_at` / `deleted_at` | `go test ./internal/proposal -run TestProposalHasNoReviewedOrDeletedKeys` |
| W7 在 M3 是 error | `go test ./internal/plan -run TestW7_IsErrorInM3`：缺 `initiator=user` → 退 `2`、零写入 |
| 新增诊断码不越界 | `cd evergreen && grep -rhoE '"(E\|W\|I)[0-9]+"' internal/ \| sort -u` → 集合 ⊆ `E1..E10 ∪ W1..W12 ∪ {I1}` |
| 不新增 `kind` | `cd evergreen && grep -rhoE 'kind":\s*"[a-z_]+"' internal/ \| sort -u` → ⊆ 既有封闭集合 |
| M3 接管 `rel remove` 占位 | `cd evergreen && grep -n "M3/S2 未实现" internal/cli/rel.go \| wc -l` → **0**（M3 收口后） |
| 不引入 M4–M6 | `grep -rn "reconcile" internal/cli/ \| wc -l` → 0；`grep -rnE "flock\|txn/\|run\.lock\|FTS5" internal/ \| wc -l` → 0 |

---

## 10. 待 owner 修正 / 确认登记（A-19 ~ A-24）

登记规则沿用 M-001 / M-002：**只登记，不改飞书文档**。编号接续本仓授权合同的 **A-18** 顺延。

| 编号 | 位置 | 原文口径 | 冲突 / 缺失 | 本合同的临时裁决 | 关闭条件 |
|-|-|-|-|-|-|
| **A-19** | 飞书 **§14.4** EG-CHK-06 行 vs **§4.2** / **§4.5** / **§16.1** | §14.4 追溯矩阵把 **EG-CHK-06** 的「首次满足阶段」标为 **S1** | 但 §4.2 分区表第 5 行写「替换『当前有效问题块』属 **S2**（EG-CHK-06）」、§4.5 op 表把 `replace_block` 标 **S2**（「仅用于更新『当前有效自检问题块』（EG-CHK-06）」）、§16.1 M3 交付列含「**自检问题更新**」。§14 声明 S2 恰 **18 条**需求，**EG-CHK-06 不在其中** → **M3 的「自检问题更新」没有 S2 需求 ID 承载** | **按 S2 执行**：`replace_block` 归 M3（三处证据 vs 矩阵一处）。M3 的「自检问题更新」在追溯上**暂挂 EG-CHK-06**，并标注该 ID 的阶段列存疑 | owner 把 §14.4 EG-CHK-06 阶段列拆为「历史块只追加 = S1；当前有效问题块替换 = S2」，S2 条数由 18 相应调整 |
| **A-20** | 飞书 **§4.4** 提案 yaml | 同时存在顶层 `status`（`pending\|approved\|rejected\|superseded`）与 `decision.result`（`approved\|rejected\|superseded`） | **两者取值域重叠，原文未定义一致性与权威侧**；不一致时无法判定以谁为准，状态机测试无法编写 | **顶层 `status` 为权威**；`decision.result` 必须与 `status` **逐字相等**，`pending` 时必须为空。不满足即 **E8**（§7.2） | owner 明示两字段的分工，或删去其一 |
| **A-21** | 飞书 **§10.1 表** vs **§4.4** yaml | §10.1 表「执行结果 `execution`」行要求「含 `failed` + `reason` + **已写 / 未写路径**」 | 但 §4.4 的 `execution` **只有四个子字段**（`status` / `attempted_at` / `reason` / `git_commit`），**无任何承载路径清单的键**；而 §16.1 M3 完成判据又硬性要求「`execution=failed` 时报告逐路径列出已写 / 未写」 | 据「提案字段完整度**不属于冻结范围**、字段可增补」（§10 章首 + §4.4）**新增两键**：`execution.written_paths[]` / `execution.unwritten_paths[]`，`failed` 时必填（§4.3） | owner 在 §4.4 的 `execution` 补入路径清单键（键名可另定，本仓同步改名） |
| **A-22** | 飞书 **§10** `stateDiagram-v2` 的 `ST` 子图 | `rejected --> [*]`、`superseded --> [*]` 两条终止边存在 | **`approved` 没有 `--> [*]` 边**，也没有自环；但它有 `approved --> superseded` 出边。**`approved` 是否终态未定义**——执行成功后提案停在哪里，图上无表达 | **判 `approved` 为非终态**，唯一合法出边 `→ superseded`；执行成功后停在 `approved` + `execution.succeeded` 不再迁移（与 §10.1「处理过的提案留在 `proposals/` 原地」一致） | owner 在状态图补 `approved --> [*]`，或明示 approved 执行成功后的终止表达 |
| **A-23** | 飞书 **§4.5** op 表 vs **§10.2** 时序图 | §4.5 逐字：「ChangePlan 是 Agent → CLI 的**唯一程序化写入通道**」 | 但 §4.5 op 表**没有** `create_proposal` / `approve_proposal` / `reject_proposal` 任何提案类 op；§10.2 时序图第 4 步是 **CLI 直接写 `proposals/`**（`C->>FS: 写 proposals`），未经 ChangePlan。**提案的创建与状态变更是否走 ChangePlan，原文未定义**且与「唯一程序化写入通道」张力明显 | （**历史，2026-10-10**）**本合同不裁决**，标为**未知 / 待 owner 确认**。M3 拆分时必须先关闭本项，否则 `proposal` 包与 `plan` 包的边界无法确定。参考先例：M2 的 `eg rel add` 采用「CLI 组装 plan → `internal/plan` → `internal/store`」，**但该先例不自动适用于提案** | **已关闭（2026-09-02 owner 裁决落地）**：owner `项目维护者 <maintainer@example.com>` 裁决 **`CLI 直写例外`** + 5 条附加约束；例外声明与逐条落地见 **§8.5.1**，裁决原文见 `docs/specs/2026-10-13-m3-prestart-adjudication.md` §7.1。原关闭条件（owner 明示①走 ChangePlan / ②CLI 直写例外并补例外声明）已由 ② 满足 |
| **A-24** | 飞书 **§4.5** op 表 `remove_relation` vs **§5.2** | §5.2 反复强调被逻辑删除对象的「**关系记录不被物理删除**」「端点有效性过滤，**记录不动**」 | 但那是**逻辑删除场景**的口径。**`remove_relation` 自身是物理移除 `relations[]` 条目，还是写某种失效标记，原文未定义**；F4 的关系类型集合里也没有「已移除」这一维度 | （**历史，2026-10-10**）**本合同不裁决**，标为**未知 / 待 owner 确认**。W10（未命中即幂等 no-op）只定义了「找不到目标时怎么办」，**不预设**找到时是物理删还是标记 | **已关闭（2026-09-02 owner 裁决落地）**：owner `项目维护者 <maintainer@example.com>` 裁决 **`物理移除`** + 5 条附加约束（规范化三元组匹配全部记录 / `opposing` 先按 ID 字典序规范化 / 不留墓碑不创建 RelationID / 未命中按 W10 幂等零写入零 commit / 逻辑删除不级联删关系）；逐条落地与反证见 **§8.5.2**，裁决原文见 §7.2。适用边界差异已在 §8.5.2 口径 5 写明 |

---

## 11. 本合同「新定」条款汇总（原文未定义，逐条标注理由）

| # | 新定条款 | 位置 | 理由 |
|-|-|-|-|
| M-1 | `approved` 判为**非终态**，唯一合法出边 `→ superseded` | §2.5、A-22 | 状态图画出了 `approved --> superseded`；若判终态则该边不可达，与图自相矛盾 |
| M-2 | `status` × `execution` **4 × 3 可达矩阵**，其中 `superseded` × `failed` **禁止** | §4.2 | `superseded` 语义是「不强行执行」，`failed` 意味执行已发生副作用，二者互斥；修正改由新建提案承载 |
| M-3 | 新增 `execution.written_paths[]` / `execution.unwritten_paths[]` 两键 | §4.3、§7.2、A-21 | §10.1 表要求 `execution` 含已写/未写路径，§4.4 却无此键；提案字段完整度不属冻结范围，可增补 |
| M-4 | 两侧一致性：并集 == 影响文件全集，且 `unwritten_paths` ⊇ 报告 `skipped[].target` 中属本提案者 | §4.3 | 无一致性约束时，两处可各说各话，M3 完成判据「逐路径列出」无法机器校验 |
| M-5 | **顶层 `status` 为权威**，`decision.result` 必须逐字相等，`pending` 时为空 | §7.2、A-20 | 两值不一致时无法判定以谁为准，状态机测试无从写起 |
| M-6 | **提案不设** `reviewed_at` / `deleted_at` / `deleted_reason` | §7.2 | §4.4 提案 yaml 无此三键；`reject` 已提供无副作用终止路径；§5.2 的被删对象**恰四类，不含提案** |
| M-7 | 双标记输出顺序固定为 `[失效][已删除]`，再叠 `[未过目]` | §6.2 | 原文未定义顺序；M2 已冻结「同一语料两次执行输出逐字相同」，需确定性口径才能写逐字比对 |
| M-8 | **W10**（`remove_relation` 未命中即幂等 no-op + warning）**规则与编号均新定** | §8.2.2 | 与 §7.5 幂等语义方向一致；判 error 会让重跑同一 plan 失败，违反 §9.3「重跑同一 plan 幂等」 |
| M-9 | **W11**（`deprecate`/`restore`/`undelete` 幂等 no-op + warning）**规则与编号均新定** | §8.2.2 | 同 M-8 |
| M-10 | **E7 / E8 / E9 / E10 / W9 / W12 的编号**新定（规则均出自原文） | §8.2.1 / §8.2.2 | 原文只给规则不给编号；诊断载荷 `code` 字段必须有值才能被 Agent 定位与重投（CLI 合同 §5） |
| M-11 | **V9 ≡ W7 前半、V10 ≡ W7 后半**，M3 不新增 error 编号 | §8.2.3、A-14 | W7 逐字含两者语义且标明「S2 起 error」；另起编号会造成同一规则两个 `code` |
| M-12 | **提案七分区不属 F5 冻结范围**，不构成对 F5 的推翻 | §7.3 | F5 逐字冻结的是「**知识卡五分区、材料笔记五分区**」，未涉及提案正文结构 |
| M-13 | `remove_relation` **必须**计入 `touchesExistingCard()`，`isDedicatedRelationPlan()` 同步扩写为两 op | §8.4 | 只扩窄例外而不计入，会让多 op plan 夹带 `remove_relation` 时静默不判 **W5**——那是放宽 |
| M-14 | 七个非关系类 op「不计入」**不等于**「可抵消」：与 `append_card` 同 plan 时 W5 照常触发 | §8.4 | 否则把状态类 op 塞进加工 plan 就能整体豁免收敛义务 |
| M-15 | 窄例外**只认** `verb == relate`，不得外溢到状态类 op | §8.4 | 防「借关系动词躲开 W5」；`verb` 与 op 的配对必须逐对可判 |

---

## 12. 开工前 checklist（M3 各 task 开工首日逐项确认）

1. §2.3 合法边 5 条 / §2.4 非法边 12 条 / §4.2 可达矩阵 12 格——三张表是否已进自己的表驱动用例。
2. `superseded` **两个**触发是否**都**有用例，且都断言了 `decision.superseded_by`。
3. `execution=failed` 的用例是否断言了 `written_paths` **与** `unwritten_paths` **两个**键。
4. 自己新增的诊断码是否在 `E7`–`E10` / `W9`–`W12` 之内——**越界即改本合同，不得自造**。
5. 自己是否需要 `set_tags` / `reprocess_note` / `save_review`——若是，先关闭授权合同 **A-18**。
6. 自己是否触碰提案的写入通道——A-23 **已由 owner 于 2026-09-02 关闭**（`CLI 直写例外`）：
   开工首日须逐条核对 **§8.5.1** 的 5 条附加约束（路径面仅限 `proposals/**` / 必须复用 guarded store 禁止裸写文件 /
   CLI 负责 Git 与报告 / 批准后的知识数据修改仍走 ChangePlan / Agent 可创建提案但不可批准或执行），
   `proposal` 包与 `plan` 包的边界按该节执行，**不得**再按二选一分支设计。
7. 自己是否实现 `remove_relation` 的落盘语义——A-24 **已由 owner 于 2026-09-02 关闭**（`物理移除`）：
   开工首日须逐条核对 **§8.5.2** 的 5 条附加约束，**不得**再按二选一分支设计。
8. 自己是否引入了 §9 末三行的 M4–M6 能力（对账 / 索引 / 强原子 / 崩溃恢复 / CI 门禁）。
