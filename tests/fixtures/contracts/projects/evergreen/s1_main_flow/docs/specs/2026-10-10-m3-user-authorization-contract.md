---
topic: M3 用户授权口径合同（写权限矩阵 / 授权表达 / 退出码 6 / 不可授权项）
stage: S2
milestone: M-003
task: 未分配（本合同先于 M3 拆分冻结，后续 task 反向引用本文）
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》§1.1 / §2.1 / §4.2 / §4.5 / §4.5.1 / §5.3 / §5.4 / §5.5 / §7.1 / §7.2 / §7.4 / §9.1 / §10.1 / §10.3 / §16.1
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
authority_extracted_at: '2026-10-10'
authority_mutation: 只读，未修改
baseline: docs/specs/2026-08-31-evergreen-s1-tech-design.md#ch7
created: '2026-10-10'
updated: '2026-10-10'
updated_by: 项目维护者
---

# M3 用户授权口径 — Contract（S2 · 里程碑 M3）

本文件把 §16.1 **M3 完成判据**里那句「**用户显式命令可改核心内容，Agent 自动路径改不了**」
展开成一张**逐「对象 × 字段 × 路径」可判定**的写权限矩阵，并一次定死：什么算「用户显式授权」、
退出码 `6` 在哪两条命令上启用、授权与 B1–B4 的边界、以及**即使用户显式要求也不做**的清单。

下游 M3 task 一律对着本文写 Acceptance；实现与本文冲突时**先回本文补合同**，
不得在实现里自行发明字段、编号或豁免。

## 0. 依据与抽取声明

| 项 | 内容 |
|-|-|
| 权威原文 | 飞书《[技术方案]常青（Evergreen）v1 技术方案》 |
| 抽取日期 | **2026-10-10** |
| 抽取方式 | 全文本地快照 `feishu_tech_design_full.md`（1839 行）逐章定位 |
| 对原文的改动 | **飞书原文只读，未修改**。本文所有「与原文冲突 / 原文缺失」一律只登记（§9），不回改权威文档 |
| 引用的原文章节 | §1.1 阶段模型（S2 定义）、§2.1 职责边界与写权限、§4.2 正文分区写权限三列表、§4.5 op 表与 `initiator`、§4.5.1 校验分级（W7 / W3）、§5.3 恢复规则、§5.4「不存在自动失效路径」的代码层保证、§5.5 `reviewed_at` 隔离、§7.1 命令表、§7.2 退出码表、§7.4 手工路径、§9.1 四条安全底线、§10.1 / §10.3 提案实现口径与隔离性、§16.1 里程碑与完成判据 |

**姊妹合同**（本文不重复其内容，只引用）

- `docs/specs/2026-09-01-eg-cli-contract.md`：§3 `--json` 信封五键、§4 退出码表（S1 恰五条）、
  §5 诊断载荷六字段、文末「阶段口径消歧登记」附录。
- `docs/specs/2026-09-08-changeplan-contract.md`：§1 顶层八键、§4.1 E1–E6、§4.2 W1–W8 / I1、
  §4.3 未编号 warning 登记表、§7 黑名单判定口径。
- `docs/specs/2026-09-19-m2-query-contract.md`：M2 查询与 `rel add` 写入链路。
- `docs/specs/2026-10-10-m3-proposal-state-contract.md`：提案四态状态机、`execution` 维度、
  M3 新增 op 与诊断码（**本文不重复定义诊断码**）。

**硬边界（先读）**

1. 本合同**不引入 M4–M6 能力**：不做对账（`eg reconcile`，S3/M4）、不建 `.index/`（S4/M5）、
   不做强原子事务 / 短临界区锁 / 写前复核 / 崩溃恢复（S5/M6）、不引 CI 依赖方向门禁（M6）。
2. 本合同**不推翻 F1–F6**、不改 ID 前缀与关系类型枚举、不改知识卡与材料笔记的五分区语义。
3. **授权只放宽「谁可以发起」，从不放宽「写入怎么做」**：B1–B4 四条安全底线在 M3 一字不改（§7）。
4. **字段只增不改**：本合同引用的 `--json` 键与 frontmatter 键此后只允许新增。

---

## 1. 两条路径的定义（全库唯一口径）

M3 的一切授权判定都建立在**恰两条写入路径**上，不存在第三条。

| # | 路径 | 定义 | 原文依据 |
|-|-|-|-|
| **P-A** | **Agent 自动路径** | 由 coding agent 生成 ChangePlan、经 `eg apply` 执行的程序化写入。Agent **只能写 ChangePlan 文件**，不能直接写 `vault/`、不能执行删除 | §2.1 职责边界表「coding agent」行：可写「仅 ChangePlan 文件」，禁止「直接写 `vault/`、执行删除」 |
| **P-U** | **用户显式路径** | 用户在终端敲的显式子命令，或用户**直接编辑 `vault/` 下的 Markdown**。两者**等效** | §7.4「直接编辑 `vault/` 下的 Markdown 是**与 CLI 并列的手工路径**，结果等效」；§4.2「两条路径等效、直接生效、不二次确认」 |

**判定这两条路径的唯一机器信号**（M3 起）：ChangePlan 顶层的 `initiator` 字段。

- `initiator` 缺省或为 Agent → 判为 **P-A**；
- `initiator: user` **且**带 `--user-request` 佐证 → 判为 **P-U**。

> 原文依据：§4.5 字段表 `initiator` 行「**S2 起状态类 op 必须为 `user`**；S1 无状态类 op，因此不需要」；
> §4.5 op 表 `deprecate` / `restore` / `set_replaced_by` / `delete` / `undelete` 行逐字：
> 「**用户发起专属**（`initiator=user` + `--user-request` 佐证）；`delete` 还需已批准提案」。

**反伪造条款（本合同新定）**：Agent 在 ChangePlan 里自行写 `initiator: user` **不构成授权**。
`--user-request` 是 CLI 侧的**命令行佐证**，必须由发起进程在命令行给出，不能由 plan 文件内容自证。

- **理由**：原文把 `initiator=user` 与 `--user-request` 并列为**两个**条件（§4.5 op 表原话是
  「`initiator=user` **+** `--user-request` 佐证」）。若允许 plan 内容自证，两个条件退化成一个，
  §10.3「Agent 可提不可执」与 §16.1「Agent 自动路径改不了」将失去代码层依据。
- **本合同新定的部分**：原文未定义 `--user-request` 的**载体**（命令行 flag 还是 plan 字段）。
  本合同定为**命令行 flag**，理由如上。

---

## 2. 写权限矩阵（逐「对象 × 字段/分区 × 路径」，恰 43 行）

**读法**：✅ = 允许；🔴 = 禁止（触发即拒绝写入）；🟡 = 允许但必须进报告提示；
「—」= 该路径在 M3 无此动作。「Agent 自动」列 = P-A，「用户显式」列 = P-U。

### 2.1 知识卡 `k-`（15 行）

| # | 字段 / 分区 | Agent 自动（P-A） | 用户显式（P-U） | 依据 |
|-|-|-|-|-|
| 1 | `id` | ✅ 仅 `create_card` 新建时 | 🔴 **不得改已有 ID** | F2 稳定 ID；改 ID 等于批量毁关系。**本合同新定**「用户亦不可改」，理由见 §5 不可授权项 U-10 |
| 2 | `tags` | ✅ 新建时 `create_card.tags` | ✅ `eg tag rename\|merge`（S2） | §4.5 op 表 `create_card` / `set_tags`；§7.1 `eg tag` 行「不改变任何卡状态与关系」 |
| 3 | `status`（`active`/`deprecated`） | 🔴 **绝对禁止** | ✅ `eg deprecate --reason` / `eg restore --reason` | §5.4 写口唯一；§7.1 两行；W7 自 S2 起 **error**（§4.5.1） |
| 4 | `replaced_by` | 🔴 **绝对禁止** | ✅ `eg replaced-by`（在失效卡上写 `{ target, reason }`） | §5.4 写口唯一（`SetReplacedBy`）；§7.1；ADR-15 |
| 5 | `deleted_at` / `deleted_reason` | 🔴 **绝对禁止** | ✅ 但**必须先有 `status=approved` 提案**（`eg delete`） | §5.4 写口唯一（`SetDeleted`）；§7.1 `eg delete` 行「必须引用已批准提案」；§10.3 校验 V10 |
| 6 | 清空 `deleted_at` / `deleted_reason` | 🔴 | ✅ `eg undelete --reason`，**`status` 原封不动**、不二次确认 | §5.3 恢复规则表第 2 行；§7.1 `eg undelete` 行 |
| 7 | `reviewed_at` | 🔴 **Agent 写入一律不更新** | ✅ 仅 `eg mark-reviewed` | §5.5「被动查看、结果报告、索引读取、**Agent 写入**一律不更新（EG-CFM-06）」。触发①见 §9 登记项 **A-16** |
| 8 | `updated_at` | ✅ 由 CLI 在实际写入时更新 | ✅ 同左 | §5.5 标题「`updated_at` / `reviewed_at` 的隔离」；两条路径无差别 |
| 9 | `sources[]`（材料关系四要素） | ✅ `add_material_rel` 追加，**允许目标为 `deprecated` 卡** | ✅ | §5.1「依据累积不因失效或删除停止」；报告写 `deprecated_new_support[]` |
| 10 | `relations[]`（论证关系）**新增** | ✅ `add_relation` | ✅ `eg rel add` | §4.5 op 表；M2 已实现 |
| 11 | `relations[]`（论证关系）**删除** | 🔴 | ✅ `eg rel remove` / `remove_relation` op（**M3/S2**，M3 接管 M2 占位） | 冻结裁决：`rel remove` + `remove_relation` 属 **M3/S2**（`M-002-m2.md` A-6 / CLI 合同附录）。**本合同新定**「Agent 自动路径不得删关系」，理由见 §5 U-04 |
| 12 | 分区「**知识内容**」 | 🔴 **不追加、不改写**（对已有卡） / ✅ **必写**（`create_card` 新建） | ✅ **可改**（CLI 命令或直接编辑 Markdown） | §4.2 分区表第 1 行逐字。**这是 §16.1 M3 判据「用户显式命令可改核心内容，Agent 自动路径改不了」的唯一落点** |
| 13 | 分区「解释与依据」 | ✅ **只追加块** | ✅ 可改可删 | §4.2 分区表第 2 行 |
| 14 | 分区「条件与边界」 | ✅ **只追加块** | ✅ 可改可删 | §4.2 分区表第 3 行 |
| 15 | 分区「**用户补充**」 | 🔴 **永不写**（全阶段硬底线） | 🔴 **CLI 写入路径永不写**；仅用户**直接编辑 Markdown** | §4.2 分区表第 4 行「**🔴 永不写 / 🔴 永不写 / 仅用户**」；E6「任何时候」；B2 |

### 2.2 知识卡 `k-` 续 · 自检与用户自建分区（3 行）

| # | 字段 / 分区 | Agent 自动（P-A） | 用户显式（P-U） | 依据 |
|-|-|-|-|-|
| 16 | 分区「理解自检」— 追加块 | ✅ 只追加 | ✅ | §4.2 分区表第 5 行 |
| 17 | 分区「理解自检」— **替换「当前有效问题块」** | ✅ **M3 起允许**，`replace_block` op + `base_block_hash`；历史记录块**只追加、永不改写** | ✅ | §4.2 第 5 行「替换『当前有效问题块』属 **S2**（EG-CHK-06）」；§4.5 op 表 `replace_block` 行；§16.1 M3「自检问题更新」。阶段冲突见 §9 登记项 **A-19** |
| 18 | **第六个 H2**（用户自建分区） | 🔴 **不写入其中** | ✅ 用户自便 | §4.2「出现第六个 H2 视为用户自建分区，**原样保留**：不报错、不删除、不重排、**不写入其中**」 |

### 2.3 材料笔记 `n-`（9 行）

| # | 字段 / 分区 | Agent 自动（P-A） | 用户显式（P-U） | 依据 |
|-|-|-|-|-|
| 19 | `id` | ✅ 仅 `write_note` 新建时 | 🔴 不得改已有 ID | F2；同 U-10 |
| 20 | `tags` | ✅ 新建时 | ✅ `eg tag` | §4.5 `write_note.tags` |
| 21 | `deleted_at` / `deleted_reason` | 🔴 | ✅ 须已批准提案（`eg delete`） | §5.2 被删对象含材料笔记；§5.4 |
| 22 | `reviewed_at` | 🔴 | ✅ `eg mark-reviewed` | §5.5 |
| 23 | 分区「材料提炼」 | ✅ | ✅ | §4.3 五分区；`write_note.sections` |
| 24 | 分区「Agent 分析」 | ✅ | ✅ | 同上 |
| 25 | 分区「**用户补充**」 | 🔴 **永不写** | 🔴 CLI 永不写；仅用户直接编辑 | E6「任何时候」；B2 逐字保留；EG-NOTE-02 |
| 26 | 分区「存疑与待验证」 | ✅ `add_open_question` 追加 | ✅ | §4.5 op 表；B2 要求**重新加工时逐字保留** |
| 27 | 分区「产出知识卡」 | ✅ | ✅ | §4.3 五分区 |

### 2.4 原文 `s-`（4 行）

| # | 字段 / 分区 | Agent 自动（P-A） | 用户显式（P-U） | 依据 |
|-|-|-|-|-|
| 28 | `id` / `url` / `title` / `saved_at` | ✅ 仅 `add_source` 首次收录 | 🔴 不得改已有 ID | §4.4 原文 yaml；F2 |
| 29 | **原文正文** | 🔴 **收录后不因任何加工被改写** | 🔴 CLI 无改写入口 | §4.4 原文示例注「（原文正文，**收录后不因任何加工被改写**）」；§7.5「正文不覆盖」 |
| 30 | 收录理由列表 | ✅ 命中判重时 `--reason` **追加** | ✅ | §7.5「`--reason` 追加到该原文的收录理由列表」 |
| 31 | `deleted_at` / `deleted_reason` | 🔴 | ✅ 须已批准提案 | §5.2 被删对象含原文；§10.2 时序 `--target s-…` |

### 2.5 主题综述 `r-`（3 行）

| # | 字段 / 分区 | Agent 自动（P-A） | 用户显式（P-U） | 依据 |
|-|-|-|-|-|
| 32 | 文件整体（`save_review`） | 🔴 | ✅ **仅用户明确要求** | §4.5 op 表 `save_review` 行「**仅用户明确要求**」；§7.1 `eg review save` |
| 33 | `stale` / `stale_reason` | 🔴 M3 不计算 | 🔴 M3 无写入路径 | §4.4「由对账计算并自动写入」；对账属 **S3/M4**，M3 不做 |
| 34 | `deleted_at` / `deleted_reason` | 🔴 | ✅ 须已批准提案 | §5.2 被删对象含主题综述 |

### 2.6 提案 `p-`（5 行）

| # | 字段 | Agent 自动（P-A） | 用户显式（P-U） | 依据 |
|-|-|-|-|-|
| 35 | 新建提案（`status: pending`） | ✅ **允许 Agent 调用** `eg proposal new` | ✅ | §10.3「Agent 可提不可执」行：「`proposal new` **允许 Agent 调用**」 |
| 36 | `status` → `approved` | 🔴 **绝对禁止** | ✅ `eg proposal approve p-xxx --confirm` | §10.3「Agent **无法自行**把 `status` 写成 `approved`（V9 要求 `initiator=user`）」，**error 级，不随宽松口径放宽** |
| 37 | `status` → `rejected` / `superseded` | 🔴 | ✅ `eg proposal reject --reason` / 系统在 `approve` 时按两触发自动改判 | §10 状态图；§10.1 两个触发 |
| 38 | `decision.*` | 🔴 | ✅ | §4.4 `decision` 注「用户决定」 |
| 39 | `execution.*` | 🔴 Agent 不写 | 🔴 用户不写 | **CLI 独占**：`execution` 是「实际执行结果」（§10.1），由 CLI 在执行后回写。**本合同新定**：任何路径的手工改写 `execution` 都不被 CLI 采信（CLI 每次执行以自身结果覆盖） |

### 2.7 工程文件与 Git（4 行）

| # | 对象 | Agent 自动（P-A） | 用户显式（P-U） | 依据 |
|-|-|-|-|-|
| 40 | `vault/evergreen.yml` | 🔴 | ✅ `eg config set` | §2.1；§3.2；ADR-13 |
| 41 | `vault/SKILL.md` | 🔴 **不可写** | 🟡 `eg init` 生成；用户可编辑但**不得承载状态或数据** | §2.1 职责边界表「SKILL.md」行：可写「**不可写**」，禁止「承载状态或数据」 |
| 42 | `vault/unprocessed.md` 条目 | ✅ `add_source` 登记 / `write_note` 成功即移出 | ✅ 直接编辑 | §4.5 op 表说明；EG-SRC-02 |
| 43 | Git 历史 | ✅ 经 CLI 一次 `apply` 一次 commit | ✅ 用户可自行 `git add` / `git commit` | §2.1「Git\|完整历史\|**仅 CLI**」+ §7.4 / §9.3「用户可用 `git status` / `git diff` 自查」 |

### 2.8 矩阵总计

| 维度 | 值 |
|-|-|
| 对象类 | **7**（知识卡 `k-`、材料笔记 `n-`、原文 `s-`、主题综述 `r-`、提案 `p-`、工程文件（`evergreen.yml` / `SKILL.md` / `unprocessed.md`）、Git） |
| 路径 | **2**（P-A Agent 自动 / P-U 用户显式） |
| 矩阵行数 | **43**（2.1 十五 + 2.2 三 + 2.3 九 + 2.4 四 + 2.5 三 + 2.6 五 + 2.7 四） |
| 判定格数 | 43 × 2 = **86** |
| **严格解锁行**：P-A 格含 🔴 **且不含** ✅，P-U 格含 ✅ | **16** |
| **条件解锁行**：P-A 格**同时**含 🔴 与 ✅（按子情形分叉），P-U 格含 ✅ | **1**（#12「知识内容」：对已有卡 🔴 / `create_card` 新建 ✅） |
| 两条路径同为 🔴 的行（授权也解不开） | **5** |

**计数规则（必须可机器复算，不许凭感觉数）**：只看单元格里的符号——
「严格解锁行」= P-A 格含 🔴 **且不含** ✅ **且** P-U 格含 ✅；#12 的 P-A 格同时含 🔴 与 ✅
（对已有卡 🔴 / `create_card` 新建 ✅），故单列为「条件解锁行」，**不计入 16**。

**严格解锁行（P-A 🔴 → P-U ✅），恰 16 行**：
#3 `status`、#4 `replaced_by`、#5 写删除标记、#6 清删除标记、#7 `reviewed_at`、
#11 删关系、**#18 第六个 H2（用户自建分区）**、#21 笔记删除标记、#22 笔记 `reviewed_at`、
#31 原文删除标记、#32 保存综述、#34 综述删除标记、#36 `approved`、
#37 `rejected` / `superseded`、**#38 `decision.*`**、#40 配置。

**条件解锁行，恰 1 行**：#12 分区「知识内容」——**这一行才是 §16.1 M3 判据
「用户显式命令可改核心内容，Agent 自动路径改不了」的唯一落点**（P-A 对**已有卡** 🔴，
`create_card` 新建时 ✅ 必写；P-U ✅ 可改）。

> **2026-10-10 独立评审修正**：本节原写「恰 15 行」并在清单里列入 #12、漏列 #18 与 #38——
> 该数字**按上述符号规则不可复算**（严格行实测 16 行），属自证式数字。现改为
> 「严格 16 行 + 条件 1 行」，规则与清单同时给出，供 `m3_final_gate.py` 逐行复算。

**两条路径同为 🔴 的行（授权也解不开），恰 5 行**：#15 与 #25「用户补充」（CLI 写入路径）、
#29 原文正文、#33 `stale`（M3 不做）、#39 `execution`（CLI 独占）。

### 2.9 owner 裁决落地：「Agent 可创建提案，但不可批准或执行」（2026-09-02，逐格对齐，只加严）

**来源**：owner **项目维护者 `<maintainer@example.com>`** 于 **2026-09-02** 对 **A-23** 的正式裁决第 5 条，逐字：

> Agent 可创建提案，但不可批准或执行。

裁决原文与另外 4 条约束见 `docs/specs/2026-10-13-m3-prestart-adjudication.md` **§7.1**；
提案控制面的写入通道条款见提案合同 **§8.5.1**。本节只做一件事：把这一条与 §2 写权限矩阵
**逐格对齐**并锁死，**不改矩阵行数（仍 43 行 / 86 格）、不改 16 / 1 / 5 三个计数**。

| 矩阵行 | 判定格 | 盘上取值 | 与裁决第 5 条的对齐 | 是否需要改格 |
|-|-|-|-|-|
| **#35** 新建提案（`status: pending`） | P-A | ✅ **允许 Agent 调用** `eg proposal new` | 「Agent **可创建提案**」= 本格 ✅ | **不改**（已对齐） |
| **#36** `status` → `approved` | P-A | 🔴 **绝对禁止** | 「**不可批准**」= 本格 🔴，且 `approve` 属**用户显式路径 P-U**（需 `--confirm`） | **不改**（已对齐） |
| **#37** `status` → `rejected` / `superseded` | P-A | 🔴 | 「不可批准」的同族：拒绝与改判同样是用户决定 | **不改**（已对齐） |
| **#38** `decision.*` | P-A | 🔴 | 裁决结论字段属用户决定，Agent 不得写 | **不改**（已对齐） |
| **#39** `execution.*` | P-A / P-U | 🔴 / 🔴（**CLI 独占**） | 「**不可执行**」= Agent 不得写执行结果；本格比裁决**更严**（连用户也不手写，由 CLI 回写） | **不改**（已更严） |
| **#5 / #21 / #31 / #34** 写删除标记（卡 / 笔记 / 原文 / 综述） | P-A | 🔴（P-U ✅ **须已批准提案**） | 「不可执行」的**知识数据面**落点：Agent 既不能批准，也拿不到批准后的执行口 | **不改**（已对齐） |

**结论（逐格核对后）**：**没有任何一格与 owner 裁决冲突，本次落地改动矩阵格数 = 0**。
矩阵 #35 / #36 / #37 / #38 / #39 五格原本即为「Agent 可创建、不可批准、不可写决定与执行结果」，
与裁决第 5 条逐格一致；#39 的 P-U 侧 🔴 严于裁决（裁决只约束 Agent），按「只加严不放宽」**保留**。

**锁定条款（本节新增，只加严）**：

- #35 的 P-A 格**不得**由 ✅ 改 🔴（那会取消「Agent 可创建提案」，与裁决第 1 句冲突）；
- #36 / #37 / #38 的 P-A 格**不得**由 🔴 改 ✅（那会让 Agent 自批提案，与裁决第 2 句冲突，
  也直接违反 U-12 与 V9「`approved` 要求 `initiator=user`」）；
- #39 两格**不得**由 🔴 改 ✅（`execution` 恒为 CLI 回写）；
- 以上五格的任何一次符号翻转都会同时改变 §2.8 的严格解锁行计数（16），
  由 `m3_final_gate.py` G2 / G6 / G7 逐行复算 + 本节逐格断言**双重**兜住。

**与 U-12 / U-13 的关系**：U-12「Agent 不能批准提案」是本条的**不可授权项**表达（即使用户口头说
「让 Agent 直接批」也不做）；本节只把它在矩阵上的落点写清，**不新增、不删除**任何 U- 条目（仍恰 13 条）。

---

## 3. 授权的表达方式（恰 4 种，封闭集合）

| 代号 | 表达方式 | 适用对象 | 是否需 `--confirm` | 原文依据 |
|-|-|-|-|-|
| **X1** | **显式子命令**：用户在终端直接敲 `eg deprecate` / `eg restore` / `eg replaced-by` / `eg undelete` / `eg mark-reviewed` / `eg edit` / `eg tag` / `eg review save` / `eg rel remove`（**M3/S2**） | 状态、生命周期指针、标签、综述、关系删除 | **否** | §7.1 命令表「需确认」列：上述各行**全为「否」** |
| **X2** | **`--reason` 必填**：X1 中的状态类命令必须带 `--reason`，理由进 frontmatter 与 Git 历史 | `deprecate` / `restore` / `undelete` / `rel remove` | 否 | §7.1「`--reason` 必填」×3；§5.3 恢复规则表「前置」列「用户发起 + `--reason`」 |
| **X3** | **ChangePlan 携带 `initiator: user` + 命令行 `--user-request` 佐证** | 一切状态类 op 的程序化落地 | 否 | §4.5 字段表 `initiator` 行 + op 表「用户发起专属」行 |
| **X4** | **提案批准**：`eg proposal approve p-xxx --confirm` | **仅逻辑删除**（`eg delete`） | **是** | §10.2 时序图第 7 步逐字 `eg proposal approve p-xxx --confirm` |

**用户直接编辑 Markdown 不在上表内**：它不经 CLI，因此不需要任何授权表达；
§7.4 已把它定为「与 CLI 并列的手工路径，结果等效」。CLI 对它的义务只有一条——**不覆盖**（B3）。

### 3.1 与 S1 已冻结的 EG-CFM-04 如何衔接（不冲突的证明）

S1 侧已冻结：**「S1 全部命令直接执行，不做任何二次确认，因此不存在『需确认』这一退出码」**
（`2026-09-01-eg-cli-contract.md` 硬边界第 2 条 + §4）。

M3 引入 `--confirm` **不推翻**该条，理由是原文把两者写在同一句里：

> §10 章首（原文第 1160 行）逐字：「v1 只有 `logical_delete` 一种提案（**S2 起**）。它承载产品里
> **唯一一处执行前确认**，其余全部动作直接生效（**EG-CFM-01、EG-CFM-04**）。**S1 阶段这处确认不存在，
> 因为 S1 根本不提供删除路径。**」

因此正确的衔接口径是：

| 阶段 | EG-CFM-04 的成立形态 | 确认点数量 |
|-|-|-|
| S1 / M1 / M2 | 全部命令直接执行 | **0**（因为 S1 无删除路径） |
| **S2 / M3** | **除删除外**全部命令直接执行 | **1**（`logical_delete` 提案的批准，EG-CFM-05） |

- EG-CFM-04 的语义是「**不做多余的二次确认**」，不是「一个确认都没有」；S1 的 0 个确认是
  「S1 无删除路径」的**推论**，不是 EG-CFM-04 的**定义**。
- §10.2 末尾进一步锁死「**用户批准就是这一次确认，执行时不再二次询问**（EG-CFM-05）」——
  即 `eg delete` 在已有 approved 提案后**不再**弹第二次确认。
- 因此 M3 的确认点**恰 1 处**（提案批准），`eg delete` 自身是该确认的**执行**而非第二次确认。

---

## 4. 退出码 `6`（需确认）的启用边界

### 4.1 裁决

| 问题 | 裁决 | 依据 |
|-|-|-|
| M3 是否启用 `6` | **启用** | §7.2 退出码表 `6` 行「阶段」列逐字 = **S2（`delete` / `proposal approve` 路径）** |
| 在哪些命令上启用 | **恰 2 条**：`eg delete`、`eg proposal approve` | 同上，原文括号内已封闭枚举 |
| 语义 | **需要确认但未提供 `--confirm`** | §7.2 「含义」列逐字 |
| 对权威 Markdown 的影响 | **完全不变**（零写入、零 commit） | §7.2「对权威 Markdown 的影响」列逐字 = 「完全不变」 |
| `5` 是否启用 | **不启用** | §7.2 `5` 行「阶段」列 = **目标态 S5**；M3 不做锁与写前复核 |
| M3 的退出码全集 | **`{0, 1, 2, 3, 4, 6}`，恰 6 值** | S1 五值（CLI 合同 §4）+ `6`；`5` 缺位。行序与「S1 恰五条」的表述冲突见 §9 登记项 **A-17** |

### 4.2 `6` 与 `1` / `2` 的分界（本合同新定，防止实现把三者混用）

| 场景 | 退出码 | 理由 |
|-|-|-|
| `eg delete` 未带 `--confirm`，但提案存在且 `status=approved` | **`6`** | 前提齐备，只差确认 |
| `eg delete` 未引用任何提案，或提案 `status≠approved` | **`2`** | 校验失败（W7 自 S2 起升 error），不是「差一个确认」 |
| `eg delete` 参数拼写错误 / 缺 `--target` | **`1`** | 用法错误 |
| `eg proposal approve` 未带 `--confirm` | **`6`** | 同上 |
| `eg proposal approve` 指向不存在的提案 ID | **`2`** | E2 目标无法解析 |

- **本合同新定的部分**：原文只给了 `6` 的一句话定义，未给出与 `1` / `2` 的优先级。
- **理由**：`6` 的语义是「**完全不变**」＋「只差确认」。若把校验失败也退 `6`，Agent 会误以为
  「补个 `--confirm` 就能过」，从而进入重试循环；而 `2` 明确表示「计划本身不可执行」（§7.2）。
  故**校验先行、确认在后**：任何 error 级问题优先退 `2`，全部通过后才可能退 `6`。

---

## 5. 授权与 B1–B4 的关系（授权**不是**豁免）

**总口径：授权改变的是「谁可以发起哪个动作」，从不改变「写入时怎么做」。**
B1–B4 是 §9.1 明文的「**S1 必须实现且不可放宽**」，M3 一字不改。

| 底线 | M3 下的精确边界 | 授权**不能**换来什么 |
|-|-|-|
| **B1 只追加** | B1 的原文定义**本身**就把用户显式路径排除在外：「替换与删除**只在用户显式发起的命令里存在（S2 起）**」（§9.1 B1 实现方式列）。因此 P-U 的替换能力是 **B1 的内含**，不是对 B1 的突破 | 不能让 **P-A** 获得替换能力。渲染器**不提供**「替换已有块」能力给自动路径——这是代码层约束，不是运行时开关 |
| **B2 逐字保留用户块** | `reprocess_note`（S2，用户发起）同样必须逐字回填「用户补充」与「存疑与待验证」，含空行、缩进、未知子结构 | 不能以「用户自己要求重新加工」为由跳过 round-trip 保真。无法安全保留时**跳过并报告**（ADR-19、`skipped[kind=user_block_unsafe]`） |
| **B3 `content_hash` 变过即跳过** | **用户显式命令同样先读盘、逐文件比对 `content_hash`**，不一致即跳过该文件、退 `3`、进报告 | **授权不得成为绕过 `content_hash` 校验的理由。** §2.2「手工路径等价」要求 CLI「**不假设自己是唯一写入者**，任何写入前重新读盘」——该约束不区分发起方 |
| **B4 提交失败不回滚** | Git 提交失败一律退 `4`、保留磁盘现状、清单进报告 | **不得**执行 `git checkout -- <paths>` / `git reset --hard` / 删除文件，**任何授权、任何阶段都不行**（ADR-05 原「回滚」设计**已废弃**，§15.1 逐字：「**任何阶段都不做破坏性还原**」） |

**三条禁止措辞（下游 task 不得写出）**：

1. ❌「用户显式命令可跳过 hash 比对」——违反 B3。
2. ❌「用户确认后失败可自动回滚到执行前」——违反 B4 与 ADR-05。
3. ❌「用户授权后 Agent 自动路径亦可改核心内容」——违反 §16.1 M3 判据与 §4.2 分区表。

---

## 6. 不可授权项（即使用户显式要求也不做，恰 13 条）

| # | 不可授权项 | 依据 | 替代路径 |
|-|-|-|-|
| **U-01** | **永久删除 / 物理删除**任何产物 | §1.3「物理删除…在本方案的 schema 与校验规则里**被主动拒绝**，而非仅靠约定回避」；EG-EDIT-06 / T-EDIT-06「**无物理删除路径**」 | 逻辑删除（`deleted_at` + `deleted_reason`），可 `eg undelete` 摘回 |
| **U-02** | **破坏性回滚**：`git checkout -- <paths>`、`git reset --hard`、删除已写文件 | B4；ADR-05「**已废弃**…任何阶段都不做破坏性还原」 | 保留现状 + 报告清单 + 用户自行 `git diff` 判断 |
| **U-03** | **CLI 写入「用户补充」分区**（任何路径、任何时候） | E6「写入目标落在…**『用户补充』（任何时候）**」= error；§4.2「**🔴 永不写 / 🔴 永不写 / 仅用户**」 | 用户直接编辑 Markdown（不经 CLI） |
| **U-04** | **Agent 自动路径改核心内容 / 改状态 / 删关系 / 删除** | §16.1 M3 判据「**Agent 自动路径改不了**」；§2.1 禁止列「直接写 `vault/`、执行删除」；§5.4 写口唯一 | Agent 只能 `proposal new` 提案（§10.3「可提不可执」） |
| **U-05** | **`status` 出现第三值**（`candidate` 等） | EG-KNW-01 / T-KNW-01「`candidate` 等第三值**被拒**」；§1.3 `candidate` 被主动拒绝 | 用 `deprecated` 或逻辑删除表达 |
| **U-06** | **自动失效路径**：因失去 `support`、因材料被删而自动改 `status` | §5.2「删除执行后**没有任何知识卡的状态被自动改变**」；EG-KNW-06 三道物理约束 | 只出「建议标记 `deprecated`」提示进报告，由用户决定 |
| **U-07** | **绕过 ChangePlan 直接调 `store`** | §4.5「ChangePlan 是 Agent → CLI 的**唯一程序化写入通道**」；M2 合同硬边界第 4 条 | 组装 plan 走 `internal/plan` → `internal/store` |
| **U-08** | **跨领域迁移 / 同一原文多领域加工 / 跨领域比较** | §1.4 Deferred 四条 **EG-DOM-04 / 06 / 07 / 08**「不随阶段变化」 | 逻辑删除旧领域产物 + 目标领域重新加工（§1.4 原文指定） |
| **U-09** | **提案的部分应用** | §4.4 十项必备表 ⑧「可应用内容\|**整体执行，不支持部分应用**」 | 只接受一部分 → 走 `superseded` 触发①，生成新提案 |
| **U-10** | **修改已落盘产物的稳定 `id`** | F2「稳定 ID 规则 + 关系一律引用 ID」；「改成路径引用后，重命名与移动会批量毁关系」。**本合同新定**「用户亦不可改」 | 文件名可随意改（`id → path` 靠扫描解析），ID 不动 |
| **U-11** | **让 `.index/` 承载独占状态**或把它当权威 | F6；§2.1 `.index/` 禁止列「保存任何独占状态、进 Git」 | M3 无索引，全部直接扫描 Markdown |
| **U-12** | **Agent 自行把提案 `status` 写成 `approved`** | §10.3 校验 **V9**（要求 `initiator=user`），**error 级，不随 §4.5.1 宽松口径放宽** | 由用户 `eg proposal approve --confirm` |
| **U-13** | **未处理提案产生阻塞 / 催办 / 红点 / 待办** | §10.3「不阻塞、不催办」行：「未处理提案**不影响任何其他命令的退出码**；无提醒、无红点、无待办生成」 | `eg proposal list --status pending` 是待办清单的**唯一形态** |

**U-07 的精确适用范围（2026-09-02 owner 裁决落地，只加严）**：U-07 约束的是 **Agent 自动路径**；用户发起的 CLI 命令（`eg init` / `eg capture` / `eg proposal new|approve|reject`）不在其列——提案控制面按 owner 对 A-23 的裁决走 **`CLI 直写例外`**，但**仅限 `proposals/**`**，且**必须复用 guarded store，禁止裸写文件**（直写例外**不等于**绕过 B3 `content_hash` 校验）；**批准后的知识数据修改仍走 ChangePlan**。逐条落地见提案合同 **§8.5.1**，矩阵逐格对齐见 **§2.9**。本段**不解除** U-07，也**不新增**不可授权项（仍恰 13 条）。

---

## 7. 机器可判定方式（供后续 task 的 Acceptance 直接引用）

每条规则给出**命令 / grep / 测试名**三选一的可执行形态。测试名为**形态约定**，实现时逐字采用。

### 7.1 写口唯一（矩阵 #3 #4 #5 #6 的总闸）

```bash
# ① 状态写口恰三个函数，且只被五个 cli 文件引用（§5.4 第 1 条）
cd evergreen && grep -rn "SetStatus\|SetReplacedBy\|SetDeleted" internal/ \
  | grep -v "^internal/store/" \
  | grep -vE "^internal/cli/(deprecate|restore|replaced_by|delete|undelete)\.go" \
  | wc -l    # 期望 0

# ② rules 包编译期无可变引用（§5.4 第 2 条）
cd evergreen && grep -rn "\*model\.Card" internal/rules/ | grep -v "_test.go" | wc -l   # 期望 0
```

### 7.2 逐条规则的判定方式

| 规则 | 判定方式 |
|-|-|
| 矩阵 #12 Agent 改不了「知识内容」 | `go test ./internal/plan -run TestE6_AgentAppendCoreKnowledgeRejected`：plan 含 `append_card` 且 `section=知识内容` → 退 `2`、目标文件**字节不变** |
| 矩阵 #12 用户显式可改「知识内容」 | `go test ./internal/cli -run TestEdit_UserExplicitCoreKnowledgeAccepted`：`eg edit` 改写「知识内容」→ 退 `0`、内容逐字生效 |
| 矩阵 #15 #25「用户补充」永不写 | `go test ./internal/plan -run TestE6_UserSectionNeverWritten`（两条：Agent 路径 / 用户显式路径**均**退 `2`） |
| 矩阵 #3 #4 #5 P-A 禁止 | `go test ./internal/plan -run TestW7_StatusOpWithoutUserInitiatorIsError`：缺 `initiator=user` → **error**、退 `2`、零写入 |
| §1 反伪造条款 | `go test ./internal/cli -run TestUserRequestFlagRequired`：plan 内写 `initiator: user` 但命令行无 `--user-request` → 退 `2` |
| X2 `--reason` 必填 | `eg deprecate k-x` 无 `--reason` → 退 `1`；`go test ./internal/cli -run TestDeprecate_ReasonRequired` |
| 退出码 `6`（`eg delete`） | `eg delete --target k-x --proposal p-y`（无 `--confirm`）→ `echo $?` = **6**，且 `git status --porcelain` **零变化** |
| 退出码 `6`（`eg proposal approve`） | `eg proposal approve p-y` → `echo $?` = **6**，`git log --oneline -1` 与执行前相同 |
| `6` 与 `2` 的分界 | `go test ./internal/cli -run TestExitCode6_OnlyAfterValidationPasses`（三例：未批准提案→`2`；参数错→`1`；仅缺 confirm→`6`） |
| M3 退出码全集闭合 | `cd evergreen && grep -rn "os.Exit(5)" internal/ \| wc -l` → 期望 **0**（`5` 属 S5） |
| B3 不因授权豁免 | `go test ./internal/store -run TestB3_UserExplicitPathStillHashChecked`：用户显式命令 + 文件已被外部改动 → **跳过该文件**、退 `3`、进报告 |
| B4 不因授权豁免 | `cd evergreen && grep -rnE "checkout --|reset --hard|RemoveAll" internal/ \| grep -v _test.go \| wc -l` → 期望 **0** |
| U-01 无物理删除 | `cd evergreen && grep -rn "os.Remove\b" internal/ \| grep -v _test.go \| wc -l` → 期望 **0** |
| U-05 无第三值 | `go test ./internal/model -run TestStatus_OnlyTwoValues`（`candidate` → 解析失败） |
| U-06 无自动失效 | `go test ./internal/cli -run TestDelete_NoAutoStatusChange`：删除后断言**所有卡 `status` 逐字未变**，报告含「本次删除没有自动改变任何知识卡的状态」 |
| U-12 Agent 不能批准 | `go test ./internal/plan -run TestV9_AgentCannotApproveProposal` → 退 `2` |
| U-13 提案不阻塞 | `go test ./internal/cli -run TestPendingProposalDoesNotAffectExitCode`：存在 pending 提案时 `eg apply` 仍退 `0` |
| §3.1 确认点恰 1 处 | `cd evergreen && grep -rn "confirm" internal/cli/ \| grep -v _test.go` → 命中文件集合 ⊆ `{delete.go, proposal.go}` |

---

## 8. 阶段边界自检（不得越界的负向清单）

| 不得出现在 M3 | 归属 | 机器反证 |
|-|-|-|
| `eg reconcile` / 对账 / 补齐 `reviewed_at` 的自动路径 | S3 / M4 | `grep -rn "reconcile" internal/cli/ \| wc -l` → 0 |
| `.index/` / SQLite / FTS5 / 增量索引 / P95 / 10,000 卡 | S4 / M5 | `ls evergreen/vault/.index 2>/dev/null \| wc -l` → 0 |
| 强原子事务 / `run.lock` / `txn/` / 写前复核 / 崩溃恢复 | S5 / M6 | `grep -rnE "flock\|txn/\|run\.lock" internal/ \| wc -l` → 0 |
| 退出码 `5` | S5 / M6 | 见 §7.2 |
| CI 依赖方向门禁 | M6 | M3 不新增 CI 断言 |
| 「权威 Markdown 一个字节都不改」的承诺 | S5 | §10.1 逐字：「是**目标态（S5）**口径，**S2 不承诺**」 |

---

## 9. 待 owner 修正 / 确认登记（A-13 ~ A-18）

登记规则沿用 M-001 / M-002：**只登记，不改飞书文档**；本表只收「原文自身前后矛盾、或原文缺失
而本合同必须先行裁决」的条目。编号在全仓现有最大编号 **A-12** 之后顺延。

| 编号 | 位置 | 原文口径 | 冲突 / 缺失 | 本合同的临时裁决 | 关闭条件 |
|-|-|-|-|-|-|
| **A-13** | 飞书 **§7.1** `eg edit` 行 vs **§16.1** M3 交付列 | §7.1 把 `eg edit`（用户显式发起的核心内容修改入口）标 **S2** | §16.1 **M3 交付列未点名 `eg edit`**，但 M3 完成判据要求「**用户显式命令可改核心内容**」——无 `eg edit` 则该判据无可验收的命令载体 | **判 `eg edit` 属 M3 范围**（否则完成判据无法验收）。矩阵 #12 的 P-U 列据此成立 | owner 在 §16.1 M3 交付列补入 `eg edit`，或明示该判据由「直接编辑 Markdown」单独承载 |
| **A-14** | 飞书 **§10.3**（原文第 1248 行） | 引用校验编号 **V9**（要求 `initiator=user`）与 **V10**（`delete` op 必须引用 `status=approved` 提案） | **V9 / V10 在全文其他任何位置均无定义**：§4.5.1 的编号体系是 E1–E6 / W1–W8 / I1；§14 追溯矩阵出现 V1–V8 / V13 / V14 但同样无定义表 | **按 §4.5.1 的 W7 承接**：W7 逐字含「状态类 op 缺 `initiator=user` 或 `reason`；`delete` 未引用已批准提案」，与 V9 + V10 语义完全重合，且 W7 标明「**S2 起 error**」。故 M3 **不新增 error 编号**，V9 ≡ W7 前半、V10 ≡ W7 后半 | owner 给出 V 系列编号表并与 E/W 系列建立映射，或删去 §10.3 的 V9/V10 改引 W7 |
| **A-15** | 飞书 **§4.5.1 W7** 行 | 「**状态类 op** 缺 `initiator=user` 或 `reason`」 | **「状态类 op」的外延未定义**。§4.5 op 表只把 `deprecate` / `restore` / `set_replaced_by` / `delete` / `undelete` 五个标为「用户发起专属」，而 `set_tags` / `mark_reviewed` / `reprocess_note` / `remove_relation` / `save_review` 未标 | **本合同按窄口径执行**：「状态类 op」= 上述**五个**。另五个 op 仍要求 `initiator=user`（因均属 P-U 专属动作），但**缺失时按 warning 而非 error**，避免超出原文授权范围收紧校验 | owner 在 §4.5.1 W7 行补「状态类 op」的封闭枚举 |
| **A-16** | 飞书 **§5.5** vs **§16.1** M3 交付列 | §5.5：`reviewed_at` 只在两种动作下更新——①**用户编辑并保存**（直接编辑 Markdown「后者**在对账时补齐**」）；②`eg mark-reviewed` | 触发①的落地依赖 `eg reconcile`，而对账整章属 **S3**（§11 章首逐字：「S1 与 S2 **不提供 `eg reconcile`**」）。M3 交付「`reviewed_at` 与 `eg unreviewed`」时，触发① **无任何落地路径** | **M3 只承接触发②**（`eg mark-reviewed`）。矩阵 #7 #22 据此写死；`eg unreviewed` 在 M3 的语料里只会看到经 `eg mark-reviewed` 写入的 `reviewed_at` | owner 明示 M3 的 `reviewed_at` 是否只含触发②，或把触发①提前到 S2 |
| **A-17** | 飞书 **§7.2** 退出码表 vs 本仓 CLI 合同 §4 | §7.2 表内行序为 `0` `1` `2` `3` `4` **`6`** `5`（`6` 排在 `5` 之前）；本仓 CLI 合同 §4 写「**S1 恰五条**」并声明「`5` 属 S5、`6` 属 S2，S1 不实现、不出现」 | 两处**不冲突但易被误读**为「M3 的退出码是连续的 0–6」。原文实际是 **`5` 在 M3 缺位、`6` 在 M3 到位**的**非连续**集合 | **M3 退出码全集 = `{0,1,2,3,4,6}`，恰 6 值，`5` 不实现不出现**（§4.1 表末行）。CLI 合同 §4 的「S1 恰五条」属 S1 历史结论，**不回改** | owner 在 §7.2 把 `6` 行移到 `5` 行之前之后统一行序，并补一行「S2 退出码全集」 |
| **A-18** | 飞书 **§16.1** M3 行「交付」列 vs **§7.1** 命令表 | M3 交付列点名：`proposal` 包、逻辑删除全时序、`deprecate`/`restore`/`replaced_by`、`reviewed_at` 与 `eg unreviewed`、`[已删除]`/`[未过目]` 标记、自检问题更新 | §7.1 命令表共有 **14 行标 S2** 的命令（`eg open`、`eg unreviewed`、`eg mark-reviewed`、`eg deprecate`、`eg restore`、`eg replaced-by`、`eg edit`、`eg delete`、`eg undelete`、`eg proposal *`、`eg review save\|show`、`eg recap`、`eg tag *`、`eg recent`），**外加**冻结裁决移入 S2 的 `eg rel remove`。M3 交付列**未点名**其中 `eg open` / `eg edit` / `eg recap` / `eg tag` / `eg review` / `eg recent` / `rel remove` | **M3 交付列是摘要而非封闭枚举**。本合同只对**有直接原文依据或冻结裁决依据**的能力定合同；`eg open` / `eg recap` / `eg tag` / `eg review save` / `eg recent` 的 M3 归属**未知，待 owner 确认**，本合同**不为其定写权限之外的行为** | owner 明示 M3 是否一次交付全部 S2 命令，或给出 S2 内部的二次拆分 |

---

## 10. 本合同「新定」条款汇总（原文未定义，逐条标注理由）

| # | 新定条款 | 位置 | 理由 |
|-|-|-|-|
| N-1 | `--user-request` 的载体 = **命令行 flag**，不可由 plan 内容自证 | §1 反伪造条款 | 原文把 `initiator=user` 与 `--user-request` 并列为两个条件；若允许 plan 自证则退化为一个，§16.1「Agent 自动路径改不了」失去代码层依据 |
| N-2 | **用户亦不可修改已落盘产物的稳定 `id`** | 矩阵 #1 #19 #28、U-10 | F2 冻结「关系一律引用 ID」；改 ID 会批量毁关系，且文件重命名已提供全部人眼可读性需求（§3.1「文件允许被重命名或移动」） |
| N-3 | **Agent 自动路径不得删除论证关系** | 矩阵 #11 | 原文 §4.5 只说 `remove_relation` 属 S2，未标发起方。但 §2.1 禁止 Agent「执行删除」，B1 亦把「删除」限定在用户显式命令，故按窄口径归 P-U |
| N-4 | `execution.*` 为 **CLI 独占**，两条路径均不可手工改写 | 矩阵 #39 | `execution` 定义是「**实际执行结果**」（§10.1）；允许手工改写会使其不再反映事实，`execution=failed` 的报告义务失去数据源 |
| N-5 | 退出码 **`6` 与 `1` / `2` 的优先级：校验先行、确认在后** | §4.2 | 原文只给 `6` 一句话定义。混用会让 Agent 误入「补 `--confirm` 重试」死循环；`2` 明确表示「计划本身不可执行」 |
| N-6 | A-15 窄口径：非「用户发起专属」五 op 缺 `initiator=user` 时按 **warning** 而非 error | §9 A-15 | 原文只对五个 op 给了「用户发起专属」的明文；对其余 op 升 error 属超出原文授权的收紧 |
| N-7 | **M3 只承接 `reviewed_at` 触发②** | §9 A-16、矩阵 #7 #22 | 触发①依赖 `eg reconcile`，而对账整章属 S3；M3 实现触发①等于提前引入 M4 能力 |

---

## 11. 开工前 checklist（M3 各 task 开工首日逐项确认）

1. 本文 §2 矩阵 43 行已逐行读过，自己负责的对象 × 字段在哪一行、两列取值各是什么。
2. 自己的 task 是否触碰 §6 的 13 条不可授权项——**触碰即改方案，不改本合同**。
3. 自己的 Acceptance 是否直接引用 §7 的判定方式（命令 / grep / 测试名逐字一致）。
4. 自己是否在实现里引入了 §8 负向清单中的任何一项。
5. 自己是否需要退出码 `6`——**只有 `eg delete` 与 `eg proposal approve` 两条命令可以**。
6. 自己是否依赖 §9 中 A-13 ~ A-18 的任一临时裁决——若是，在 task 的 Risk 段落复述该编号。
