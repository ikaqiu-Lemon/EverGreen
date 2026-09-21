# Evergreen Schema v2 设计：Knowledge/Opinion 分层与 Note 审阅式学习版重构

> 状态：**契约定稿**（T-…-001 基线 + T-…-011 审阅式 Note 补充）
> 适用版本：`eg` 0.7.0-m7（`plan_version: 2`，`index schema_version: 2`）
> 本文件是本 Epic 内所有「会约束他人工作的选择」的**唯一真源**。下游 Task 通过
> `design_doc:` 钉到本文的具体章节；与本文冲突的实现一律以本文为准，除非先修本文。

---

## 0.0 版本号与发布口径（本 Epic 当期唯一决策出处）

> **追加说明（T8-4，实施期）**：`docs_test.go` 的 `docsReleaseSpec` 指针与
> `tests/e2e/docs-cli/*.sh` 的 `SPEC_REL` 自本 Epic 起由 M6 发布口径文档改指向**本文件**——
> 判据本体一字不改（决策文档必须逐字含当期版本号、且对「是否推送远端 / 打 tag」标「未知」），
> 只是当期出处随里程碑前移。M2–M6 五份发布口径文档仍在盘、事实一字未改（只读历史）。
> 本节是纯加性追加，不改动本文件其余任何结论。

- 当期活跃版本号：**`0.7.0-m7`**（`plan_version: 2`、`index schema_version: 2`，与 §4.1 / §7 同真）。
- 语义：里程碑试用版（收口前带 `-m7` 预发布后缀），**不是**对外正式发布版。
- 四处同真：`Makefile` 的 `VERSION ?= 0.7.0-m7`、`internal/version/version.go` 的
  `const DefaultVersion = "0.7.0-m7"`、本节声明的 `0.7.0-m7`、`README.md` / `README.zh-CN.md` /
  `INSTALL.md` 的版本消费面，五个位置逐字相等（由 `TestDocsVersionThreeSourcesConsistent`
  与 `tests/e2e/docs-cli/*.sh` 逐字比对）。
- 版本沿革（历史事实，逐字保留）：M2 `0.2.0-m2` → M3 `0.3.0-m3` → M4 `0.4.0-m4` →
  M5 `0.5.0-m5` → M6 `0.6.0-m6` → **本 Epic `0.7.0-m7`**。各历史值的唯一决策出处仍是其
  对应的 `*-release-and-version.md`，结论不改。
- tag 命名（若创建）：`v0.7.0-m7`（附注 tag，前缀 `v` 固定，与历史同规则）。**本轮未创建。**

### 发布 / tag 状态（本文件不声称已发布）

| 问题 | 结论 | 依据 |
|-|-|-|
| 是否已打 `v0.7.0-m7`？ | **否**（本轮未创建） | 本轮不执行任何 tag 命令 |
| 是否**应该**正式发布 / 创建发布 tag？ | **未知 / 待确认** | 属组织决策，本地无依据 |
| 本任务实际动作 | 只推进版本真值；本任务不发布、不打 tag、不合产品 main。任务分支与 integration 之间的协作推送属日常协作流转，**不等于发布**。 | T8-4 边界 |

> 口径说明（延续 M2–M6）：`README.md` 与 `INSTALL.md` 中**不出现**任何正式发布声明；
> 是否正式发布 / 创建发布 tag 一律标「未知」，不得声称已发布。

---

## 0. 一句话概括

把「一个 Note 五分区 + 一种知识卡」的单层产物模型，改成
**Source → Note（审阅式学习版）→ {Knowledge 稳定知识, Opinion 待验证观点}**
的三层四实体模型。

---

## 1. 问题陈述（为什么必须动 Schema）

### 1.1 Note 退化为摘要

现状：`write_note` 在 `internal/plan/validate.go:602` 强制要求「材料提炼」分区存在，
且它是加工产出的**唯一落点**（`internal/plan/matrix.go:231` 授权矩阵第 23 行）。
Note 的五分区是 `材料提炼 / Agent 分析 / 用户补充 / 存疑与待验证 / 产出知识卡`
（`internal/mdfile/sections.go:45`）。

后果：一篇 504 行的 Source 产出 38 行的 Note。结构上，「把整篇文章的加工结果塞进一个
叫『提炼』的分区」这个设计本身就在要求压缩。同时「Agent 分析」是**全局分离**的——
来源内容和 Agent 推导被切到文档两端，读者无法在原文语境里看到补充说明。

**关键判断：问题不在长度，而在信息与结构覆盖不足。** 因此修复方向不是「加最小行数门禁」，
而是改变落盘结构，并把校验从「长度/压缩率」换成「结构覆盖」。

### 1.2 Knowledge 模板迫使 Agent 编造论证

现状：知识卡五分区是 `知识内容 / 解释与依据 / 条件与边界 / 用户补充 / 理解自检`
（`internal/mdfile/sections.go:39`）。代码层面只有「知识内容」是 required
（`RequiredSection(KindCard)`，`create_card` 校验见 `validate.go:660`），
但**模板与 SKILL 的存在本身就是强制力**：Agent 看到固定分区就会填。

后果：对「ReAct Loop 的执行流程」这种稳定事实，Agent 被迫写出「解释与依据」，
于是把来源定义和自己的推导混成一段，污染了权威内容。

**关键判断：Knowledge 不需要 Agent 论证。原文怎么定义，就完整地整理进去。**

### 1.3 观点无处安放

「Harness 的核心边界决定扩展成本」「Agent 自进化的瓶颈是评测与准入」——
这类**可讨论的判断**当前只能写成 Knowledge，与稳定知识共享同一个检索空间。
检索「Harness」时，稳定定义和未经验证的判断等权出现，知识视图被污染。

**关键判断：需要第二种实体，且必须在检索层默认分开。**

### 1.4 来源保真、批注意图与提炼覆盖仍不可证明

首轮 Schema v2 用有序 `blocks[]` 解决了「无序分区无法表达原文顺序」的问题，
但复核完整 golden Note 后仍发现三类合同缺口：

1. `role: source|agent` 只区分来源归属，没有要求说明来源范围，也没有规定图片、图注、
   表格、代码块、列表、引用、链接和脚注必须保留。Agent 仍可能在一个 `source` 块内部
   静默删掉例子或中间论证。
2. writer 把所有 `role: agent` 块固定渲染成 `[Agent 补充]`。导读、强调、总结、辨析、
   待验证和反思的教学意图被压成一个标签，且未知批注类型没有安全扩展方式。
3. `提取结果` 只是产物 ID 清单，`W21` 也只比较标题数与来源块数。两者都不能回答
   「Note 的每个语义模块去了哪里」，因此漏提 API、流程、边界或中间观点时不可见。

根因不是 Agent 不够认真，而是合同只定义了**容器形态**，没有定义来源保真清单、
审阅批注意图和模块级提炼完成条件。后续规则必须让「有意只留在 Note」与「忘记提炼」
成为可区分的状态。

---

## 2. 目标模型

```text
Source (s-*)      未经加工的原文
  │
  │  保留完整来源正文；按原文顺序插入显式、可移除的教学批注
  │  不压缩、不重排、不静默删除、不提前结论
  ↓
Note (n-*)        一篇可直接用于学习的「审阅式学习版文章」
  │
  │  按独立复用粒度拆分
  ├──────────────────────────┬──────────────────────────┐
  ↓                          ↓
Knowledge (k-*)            Opinion (o-*)
稳定知识点                  需论证和验证的观点
domains/<d>/knowledge/     domains/<d>/opinions/
```

### 2.1 Note 的准确含义（本 Epic 最重要的定义）

**Note 是文章的「审阅式学习版」。它不是 summary、改写稿或 manifest。**

```text
Note = 干净、顺序忠实的来源正文 + 就近、显式、可移除的 Agent 批注
```

删除全部 Agent 批注后，剩余内容必须仍是一篇结构完整、可独立阅读的来源正文；
保留批注后，Note 应帮助用户建立学习路线、识别重点、辨析概念、理解局限并回顾全文。
原文语言符合用户阅读需要时原则上逐字保留；需要翻译时，只能忠实转换语言，
不得借翻译重组论证、压缩例子或改变主张边界。

| 允许的整理 | 说明 |
| --- | --- |
| 忠实翻译或统一语言 | 逐段转换语言，不压缩、重组或替作者收窄结论 |
| 删除网页导航、广告、评论区、重复页眉页脚 | 只删可证明的页面噪声，并逐项进入 `omissions[]` |
| 修复损坏的排版 | 可机械调整标题层级、恢复折叠表格；不得改变信息 |
| 完整保留结构资产 | 图片、图注、表格、代码块、列表、引用、链接和脚注尽可能原位保留 |
| 为便于理解补充必要说明 | 只能作为显式 Agent 批注就近插入；**因此 Note 可以比原文更长** |
| 保持原文的章节顺序、论证顺序、叙事顺序 | 顺序是硬约束 |

| 不应发生 | 为什么 |
| --- | --- |
| 把 500 行文章压成 7 条摘要 | 丢掉「具体讲了什么」 |
| 把来源段落改写成 Agent 自己的概括 | 丢掉作者的定义、例子、推理过程和措辞边界 |
| 静默删除图片、表格、代码、脚注或中间论证 | 结构仍像文章，但关键信息已不可恢复 |
| 按 Agent 自己的分类重排文章 | 破坏原文论证结构 |
| 将后文结论提前 | 破坏叙事顺序，读者失去推导过程 |
| 把来源内容和 Agent 新推导混成一段 | 权威性污染，无法区分谁说的 |
| 只记录「文章提到了什么」 | 退化为 manifest |

**Agent 批注的落位规则**：批注**紧邻它所解释的原文内容**（同一章节内），
而不是全局分离到文档末尾。批注必须显式标记其教学意图，使读者能一眼区分
来源与 Agent 说明；具体 vocabulary 与扩展规则见 §4.2。

### 2.2 Knowledge 与 Opinion 的职责

| 类型 | 职责 | Agent 被允许做什么 |
| --- | --- | --- |
| **Knowledge** (`k-*`) | 直接记录稳定知识；完整保留定义、组成、步骤、条件和必要例子 | **只整理、拆分、去重**。不论证、不推导 |
| **Opinion** (`o-*`) | 记录可讨论的判断；包含观点、论据、推理、条件、反例和验证结果 | 论证、给反例、标注验证状态 |

两条硬规则：

1. **Knowledge 只允许直接来自 Note 中的整理内容。** Agent 的多跳推导不得写成 Knowledge。
2. **Opinion 不需要作者模型。** 谁提出的不重要；重要的是它有什么依据、是否成立。

判定口径（用于 Agent 与 review）：

- 如果一句话在原文里是**定义/组成/步骤/条件/数据**，它是 Knowledge。
- 如果它是**评价/因果判断/预测/优劣比较/取舍主张**，它是 Opinion。
- 拿不准时优先 Opinion（Opinion 可以经验证升级；错标为 Knowledge 会污染知识视图）。

举例（以一篇 Agent 工程文章为例）：

- Knowledge：Agent 的基本组成（Model、Harness，及 Harness 的五类能力与各自定义）；
  ReAct Loop 的执行流程；Library / Product / Platform 三种开发边界；
  DSH 的 Cordis 插件结构与 `id/name` 机制；离线 / 近线 / 在线学习的区别。
- Opinion：Harness 的核心边界决定扩展成本；DSH 当前存在明显过度工程；
  Agent 自进化当前瓶颈是评测和准入，而不是生成修改。

---

## 3. 实体与存储布局

### 3.1 ID 前缀与目录

```text
sources/                        s-*.md      原文
domains/<domain>/notes/         n-*.md      审阅式学习版文章
domains/<domain>/knowledge/     k-*.md      稳定知识（不变）
domains/<domain>/opinions/      o-*.md      观点（新增）
proposals/                      p-*.md      高风险操作提案
```

**类型只由 ID 前缀 + 目录表达。** 不引入 `card.type` / `stance` / `lean` 等 frontmatter
字段——这三者已在 `internal/model/blacklist.go:13` 被列为 `DeprecatedFieldPaths`，
原因是「领域由目录决定、类型由 ID 前缀决定」，冗余元数据会导致真源漂移。
本 Epic **对称扩展**该黑名单：`opinion.type` / `opinion.stance` / `opinion.lean`
同样禁止，命中报 `W4`（`--strict` 下升级为 error）。

### 3.2 分区模板

**Knowledge（`k-*`）**

```markdown
## 知识内容      ← required；完整、可直接学习；不需要 Agent 论证
## 条件与边界    ← optional；原文给出的适用条件与限制
## 用户补充      ← never-write（安全底线 B2）
```

相对 v1 的变化：从固定五分区收敛为三分区。**移除 `解释与依据` 与 `理解自检`**：

- `解释与依据` 的职责整体迁移到 Opinion 的 `论据与推理`。稳定知识不需要依据段。
- `理解自检` 是「Agent 必须自证读懂了」时代的产物；在新模型里，
  Knowledge 的正确性由「是否忠实整理自 Note」保证，不靠自检段。

**兼容性**：v1 的 `k-*` 文件里已存在的 `解释与依据` / `理解自检` H2 不会报错——
按现有 `ValidateSections` 语义（`internal/mdfile/sections.go:111`），
非固定分区落入 `UnknownSections`，**原样保留、只记 info**。它们由 T-…-009 迁移时人工收口。

> **决策记录 D-7：Knowledge 移除 `解释与依据` 与 `理解自检` 两分区。**
> 决策内容：Knowledge 从 v1 的五分区收敛为三分区
> （`知识内容` / `条件与边界` / `用户补充`）；被移除的两个分区不再是固定分区，
> 但存量文件中的同名 H2 原样保留并只记 info，不报错、不重写、字节保真。
> 理由：(a) 需求原文明确「Knowledge 不应强制要求 Agent 写解释与依据；
> 原文怎么定义，就完整地整理进去」——强制依据段是 Agent 编造内容的结构性诱因
> （见 `I-…-002`）；(b) 论证职责整体转移到 Opinion 的 `论据与推理`，
> 保留两处会造成职责重叠；(c) `理解自检` 的保证目标改由「是否忠实整理自 Note」
> 承担，而后者可通过 Note→Knowledge 的来源关系机械检查。
> 若要推翻（恢复五分区）：改 `internal/mdfile/sections.go:19-88` 的
> `KnownSections` / `RequiredSection` / `AutoWritableSections` 三处 switch、
> `internal/plan/matrix.go:214` 起的授权矩阵补两行、
> `internal/plan/authorize.go:150,216` 的分区授权分支、
> `internal/plan/validate.go:658` 的 `sectionPayloads` 固定分区列表，
> 并同步 `skill/SKILL.md` 与四份文档中的卡片模板；同时需重新论证
> 如何避免 `I-…-002` 描述的编造依据问题。

**Opinion（`o-*`）**

```markdown
## 观点          ← required；一句话说清主张
## 论据与推理    ← 支持它的证据与推理链
## 条件与反例    ← 在什么条件下成立；已知反例
## 待验证        ← 还需要什么证据才能定论
## 用户补充      ← never-write（安全底线 B2）
```

**Note（`n-*`）**

```markdown
## 整理正文      ← required；完整来源正文为骨架；Agent 批注就近内嵌
## 提取结果      ← Knowledge / Opinion 清单 + 模块级覆盖矩阵
## 存疑与待验证  ← 保留（见下方决策记录）
## 用户补充      ← never-write
```

相对 v1 的变化：`材料提炼` + `Agent 分析` 两个分区被**合并**为 `整理正文`；
`产出知识卡` 更名并扩展为 `提取结果`（同时列 Knowledge 和 Opinion）。

> **决策记录 D-1：保留 `存疑与待验证`。**
> 用户给出的目标结构只列了 `整理正文 / 提取结果 / 用户补充` 三段，未提及本分区。
> 本契约决定**保留**它，理由：(a) `add_open_question` 是主链路 7 个 op 之一，
> 已有存量数据；(b) 整理 Note 时浮现的「疑问」尚不是「判断」，强行建 Opinion 会
> 制造大量空壳 `o-*`；(c) 保留是纯加性的，不影响 §2.1 的任何定义。
> 若 owner 认为应当移除，改本节 + T-…-002 的分区常量即可，其余任务不受影响。
> 具体推翻代价：`internal/mdfile/sections.go:19-88` 的 Note 分区常量与
> `KnownSections` / `RequiredSection` / `AutoWritableSections` 三处 switch、
> `internal/plan/validate.go` 中 `add_open_question` 的落点分区、
> `internal/plan/matrix.go` 的 Note 授权行；并需为存量含该分区的 `n-*`
> 定义收口方式（迁移进 `整理正文` 还是转为 Opinion 的 `待验证`）。

### 3.3 分区写权限矩阵

沿用 `internal/plan/matrix.go` 的编号矩阵，新增 Opinion 行。口径：

| 对象 | 分区 | Agent 自动路径 P-A | 用户显式路径 P-U |
| --- | --- | --- | --- |
| Knowledge | `知识内容` | deny（创建时可写，已有卡不得改写） | allow（`eg edit`） |
| Knowledge | `条件与边界` | allow（只追加块） | allow |
| Knowledge | `用户补充` | **deny** | allow |
| Opinion | `观点` | deny（同 `知识内容` 口径） | allow |
| Opinion | `论据与推理` | allow（只追加块） | allow |
| Opinion | `条件与反例` | allow（只追加块） | allow |
| Opinion | `待验证` | allow（只追加块） | allow |
| Opinion | `用户补充` | **deny** | allow |
| Note | `整理正文` | allow（`write_note.blocks`） | allow |
| Note | `提取结果` | allow | allow |

`NeverWriteSections()` 保持返回 `用户补充`——对全部四类实体生效。

### 3.4 Opinion frontmatter

在 Knowledge 现有字段基础上，Opinion 增加**恰一个**新键：

```yaml
validation: pending        # pending | validated | rejected
```

- 创建时默认 `pending`，由 `create_opinion` 写入。
- 只能由**用户显式路径**流转（见 §6.3）；Agent 自动路径不得改写。
- 合法流转：`pending → validated`、`pending → rejected`、
  `validated → rejected`、`rejected → pending`（复议）。
  `validated → pending` 同样允许（出现新反例时降级）。
- **不引入** `confidence` / `author_model` / `stance` / `lean`。
  「谁提出的」不建模；「有多可信」由 §6.4 的关系视图回答，不由标量字段回答。

---

## 4. ChangePlan v2

### 4.1 版本与兼容

- 新增 `plan_version: 2`。`internal/plan/schema.go:18` 的 `PlanVersion` 常量
  改为「当前版本 = 2，受支持版本集 = {1, 2}」。
- `plan_version: 1` **继续被接受**（兼容期），语义为 v1 旧口径：
  `write_note` 走固定分区 payload，`create_card` / `append_card` 作用于 Knowledge。
  接受时产出 `I1` info 提示「plan_version 1 已进入兼容期」。
- 顶层 8 键不变：`plan_version` / `verb` / `domain` / `reason` / `requirement_ids`
  / `convergence[]` / `base` / `ops[]`。**本 Epic 不改顶层键集合。**

### 4.2 `write_note`：有序 `blocks[]`

v1 形态（固定分区映射）：

```json
{ "op": "write_note", "source": "s-…", "sections": { "材料提炼": "…", "Agent 分析": "…" } }
```

v2 形态（有序块数组）：

```json
{
  "op": "write_note",
  "source": "s-20260915-example",
  "blocks": [
    {
      "role": "source",
      "source_ref": "L14-L28",
      "heading": "1. 背景",
      "body": "…按原文顺序保留的正文…"
    },
    {
      "role": "agent",
      "annotation": "distinction",
      "body": "…容易混淆的概念边界…"
    },
    {
      "role": "source",
      "source_ref": "L29-L51",
      "heading": "2. 方法",
      "body": "…"
    }
  ],
  "omissions": [],
  "output_cards": [
    { "card": "k-20260915-example-method", "mode": "新建" }
  ],
  "extraction_coverage": [
    {
      "module": "m-001",
      "source_refs": ["L14-L28"],
      "summary": "背景与问题定义",
      "disposition": "note_only",
      "reason": "文章元数据与阅读背景不具备独立复用价值"
    },
    {
      "module": "m-002",
      "source_refs": ["L29-L51"],
      "summary": "方法的三个执行步骤",
      "disposition": "outputs",
      "outputs": ["k-20260915-example-method"]
    }
  ]
}
```

规则：

1. **数组顺序即落盘顺序。** 不排序、不去重、不重排。
2. `role` 是**二值封闭枚举**：`source`（来源正文或忠实逐段翻译）| `agent`（Agent 批注）。
   它只表达内容来源，不承担批注类型语义。
3. `heading` 可选。给出时渲染为 H3，用于保持原文章节结构。
4. `role: source` 必须给出 `source_ref`；`role: agent` 必须给出 `annotation`，
   两类字段不得混用。`label` 只用于扩展批注，见 §4.2.2。
5. `omissions[]` 只记录从 Source 删除的页面噪声，每项必须有 `source_ref` 与具体理由；
   没有删除时传空数组。
6. `extraction_coverage[]` 必须逐项说明每个细粒度语义模块的去向，见 §4.2.3。
7. **取消固定分区要求**，取消任何长度 / 压缩率门禁。
8. 保留**结构覆盖诊断** `W21`（warning，非 error）：
   当 `blocks[]` 中 `role: source` 的块数 **显著少于** Source 的 H2/H3 章节数时告警，
   提示「疑似退化为摘要」。阈值与算法见 §4.3。
   选择 warning 而非 error：覆盖度无法机械判定为对错，加严为 error 会诱导凑数。
9. `blocks[]` 与 v1 的 `sections` **互斥**。同时出现报 `E2`。
10. `blocks[]` 为空或全为 `role: agent` 报 `E2`——Note 必须有来源内容。

#### 4.2.1 来源范围与保真

- `source_ref` 使用 `L<start>-L<end>`，行号相对于 Source Markdown 正文
  （不含 YAML frontmatter）；起止行均包含在范围内。
- 来源范围按 Source 顺序单调递增，不得重复或交叉。同一连续范围只对应一个 source 块；
  需要插入批注时，在两个连续 source 块之间插入 agent 块。
- `blocks[].source_ref` 与 `omissions[].source_ref` 的并集必须覆盖 Source 的全部非空正文行。
  空洞报 `E2`，重叠或逆序同样报 `E2`。
- `omissions[]` 只允许页面导航、广告、评论区、重复页眉页脚等噪声；不得用它删除作者的
  定义、论据、例子、结论或参考资料。
- 图片 URL 与图注、代码块内容、表格行、列表项、引用、链接和脚注的数量及相对顺序
  必须与对应来源范围一致。格式恢复或翻译不免除结构资产保真。
- 删除全部 agent 块和来源范围标记后，剩余正文必须连续、完整、可独立阅读。

#### 4.2.2 批注意图

内置批注 vocabulary：

| `annotation` | 渲染标记 | 用途 |
| --- | --- | --- |
| `guide` | `[Agent 导读]` | 给出学习路线、前置概念与阅读问题 |
| `supplement` | `[Agent 补充]` | 背景、术语、额外例子、损坏格式的可读恢复 |
| `emphasis` | `[Agent 强调]` | 指出本段学习重点及其重要性 |
| `summary` | `[Agent 总结]` | 章节结束后的局部总结，不得替代章节正文 |
| `distinction` | `[Agent 辨析]` | 容易混淆的概念、边界或常见误读 |
| `verification` | `[Agent 待验证]` | 时效事实、证据不足判断与后续核验方法 |
| `reflection` | `[Agent 反思]` | 全文方法、价值、局限和可迁移经验的复盘 |

内置 key 使用固定中文标签。`annotation` 不是永久封闭枚举：扩展 key 必须匹配
`^[a-z][a-z0-9_-]{0,31}$` 并同时给出非空 `label`；内置 key 不得用 `label`
改写其固定含义。新增类型必须回答「它解决什么独立学习需求，为什么不能由现有类型表达」。
批注紧邻被解释正文；导读位于正文开头，总结位于对应章节末尾，反思位于全文与参考资料之后。

> **决策记录 D-10：Note 采用“来源正文 + 可移除的显式审阅批注”，批注 vocabulary 可扩展。**
> 决策内容：`role` 继续保持 `source|agent` 二值；Agent 块新增 `annotation`，
> 内置七类教学意图，未知 key 通过显式 `label` 扩展；来源块新增 `source_ref`，
> 删除内容必须进入 `omissions[]`。理由：(a) `role` 是 provenance，批注意图是另一维，
> 混成枚举会把来源模型与教学模型耦合；(b) 固定 `[Agent 补充]` 无法表达导读、辨析、
> 待验证与反思，学习价值不可检索也不可验收；(c) 可扩展 key 避免未来每新增一种教学意图
> 都升级 Schema。若要推翻：需同时改 `internal/plan.NoteBlock`、v2 validator、
> Note writer / parser、golden fixture、SKILL 与迁移器；若删除 `source_ref`，
> 还必须提供另一种可机械证明来源无遗漏、无重复、无交叉的等价机制。

#### 4.2.3 语义模块与提炼覆盖

提炼前必须先把 Note 拆成比章节更细的语义模块：定义、组成、步骤或状态流、
API / 数据结构 / 接入位置、条件与边界、数据或时效事实，以及评价、因果判断、
预测、优劣比较和取舍主张。不得直接从章节标题跳到少量主题卡。

`extraction_coverage[]` 规则：

1. `module` 在本 Note 内唯一；`source_refs[]` 非空且只能引用本次 `blocks[]`
   已声明的来源范围；`summary` 必须具体描述该模块。
2. `disposition` 是封闭三值：`outputs | note_only | missing`。
3. `outputs` 必须列出一个或多个 `k-*` / `o-*`，并与 `output_cards` 双向一致；
   `note_only` 必须给出非空 `reason`；`missing` 必须说明待补内容，并使校验报 `E2`、
   阻止 apply。三种处置不得同时出现。
4. 每个 `source_ref` 至少进入一个模块。跨多个来源范围的模块可以引用多个 ref；
   同一来源范围包含多个独立模块时必须拆成多项。
5. 可独立复用的事实不能只藏在 Opinion 的「论据与推理」里，必须同时产出 Knowledge。
6. plan 生成阶段发现尚未处置的模块时标记为 `缺漏` 并停止 apply；
   可执行 ChangePlan 中 `缺漏` 必须为 0。
7. writer 在 `提取结果` 中渲染 Knowledge / Opinion 清单和完整覆盖矩阵，
   使后续 review 能从任一模块追到产物或 `Note-only` 理由。

> **决策记录 D-11：模块级覆盖矩阵是提炼完成条件，`W21` 不是覆盖证明。**
> 决策内容：每个来源语义模块必须映射到 Knowledge / Opinion，或以理由明确标记
> `Note-only`；`缺漏 = 0` 才允许完成加工。理由：(a) 标题数与来源块数只能发现极端结构退化，
> 看不见块内遗漏；(b) 综合大卡会造成“标题已覆盖”的假象，独立 API、流程和边界仍不可检索；
> (c) 明确记录 `Note-only` 才能区分有意保留与忘记处理。若要推翻：需提供另一种能逐模块回答
> 「提炼到哪里、为何不提炼」的可审计机制，并同步 `write_note` schema、Note 渲染、
> SKILL、迁移器和 `I-…-004` 的验收标准；仅依赖 `W21` 或产物 ID 清单不构成等价替代。

### 4.3 `W21` 结构覆盖诊断算法

> **编号更正（2026-09-15，实施期）**：本节原定编号为 `W25`。实施 T-…-003 时核对
> 全库编号占用发现 `W25` 已被 `internal/query/page.go:51` 的 `CodeResultTruncated`
> 占用（查询结果截断），**不是**空号。结构覆盖诊断改用 `W21` —— 该号在
> `internal/cli/codes.go:24` 的占用总览里是唯一空号，且 `internal/query/diagnostic.go:38`
> 与 `internal/reconcile/r7_support.go:51` 两处注释已逐字把它预留给
> **ChangePlan 分级表**（「不使用 `W21`（越域码，属 ChangePlan 分级表，查询域不得借用）」）。
> 本诊断正是 ChangePlan 写前校验产出的 warning，落在 `W21` 与既有留白意图完全一致。
> 详见决策记录 D-9。

```text
src_anchors  = Source 正文中 H2 + H3 标题数（去空标题）
note_source_blocks = blocks[] 中 role == "source" 的块数
若 src_anchors >= 3 且 note_source_blocks < ceil(src_anchors / 2)
  → W21「整理正文的来源块数 N 显著少于原文章节数 M：疑似退化为摘要」
```

`src_anchors < 3`（短文、无标题原文）时不判——避免对短原文误报。
该诊断在 `--strict` 下**不**升级为 error（豁免表落点见下方「落点更正」），
理由同 §4.2 第 8 条。

> **落点更正（2026-09-15，实施期）**：本节与 D-6 原定把豁免表写进
> `internal/model/strict.go`。实施 T-…-003 时与 M3 的**诊断编号分域纪律**
> （`internal/plan/m3_test.go` 的 `TestDiagnosticCodes_Closed`）冲突：该判据要求
> `W21` 这个**字面量**只在 owner 包 `internal/plan` 出现一次（其余落点一律引用常量），
> 而豁免表若写在 model 就会在 model 里留下第二处 `W21` 字面量，令「一码一落点」退化。
> 因此豁免表实际落点改为 **`internal/plan/strict_exempt.go`**（引用同包 `W21` 常量，
> 源码不出现第二处字面量），并 import `internal/model` 对升级面做交集复算。
> 升级面（`W1`/`W2`/`W3`/`W4`/`W6`，恰五条）**仍留在 `internal/model/strict.go`**——
> 它同时被互不 import 的 plan 与 reconcile 消费，只能落在零依赖的 model；
> 而豁免面只有 plan 侧消费者（`W21` 由本包发放，reconcile 永远看不到它），
> 没有跨层共享约束。两侧「不得有交集」的不变量由
> `plan.StrictSetsDisjoint()`（复算 `model.IsStrictUpgradeCode`）保证，
> 与原设计等价、且不放宽任何既有判据。

> **决策记录 D-6：`W21` 结构覆盖只报 warning，且在 `--strict` 下不升级为 error。**
> 决策内容：`W21` 恒为 warning；strict 豁免表（落点见上方「落点更正」：
> `internal/plan/strict_exempt.go`）把它列入豁免，
> 使 `--strict` 不会因覆盖度告警而失败。
> 理由：(a)「Note 是否覆盖了原文结构」本质上是质量判断，不存在机械正确答案，
> 阈值 `ceil(src_anchors/2)` 只是启发式；(b) 若升级为 error，Agent 的最优策略
> 会变成「按标题数凑够 `role: source` 块」，把语义门禁劣化为计数游戏，
> 这与本 Epic 要解决的「Note 退化」问题同源；(c) 真正的把关点是用户
> `mark-reviewed`，warning 只需把疑点暴露到 `diagnostics` 即可。
> 若要推翻（升级为 strict error）：改 `internal/plan/strict_exempt.go` 移出豁免、
> 并把 `W21` 加进 `internal/model/strict.go` 的升级面（两处不得同时登记，
> 由 `plan.StrictSetsDisjoint()` 复算），
> 并须同时给出一个不可被凑数规避的判据（例如基于内容对齐而非块计数），
> 否则会直接违反本 Epic「不得用弱断言/凑数满足门禁」的约束；
> 另需修改 §4.2 第 8 条、§4.3 与 §10 对该诊断的表述。

### 4.4 写口 op 集合

| op | 作用 | 备注 |
| --- | --- | --- |
| `add_source` | 登记原文 | 不变 |
| `write_note` | 写整理版 Note | **改为 `blocks[]`**（§4.2） |
| `create_knowledge` | 新建 `k-*` | **新增** |
| `append_knowledge` | 追加 `k-*` 分区 | **新增** |
| `create_opinion` | 新建 `o-*`（`validation: pending`） | **新增** |
| `append_opinion` | 追加 `o-*` 分区 | **新增** |
| `add_material_rel` | 材料关系（Note↔Source 等） | 不变 |
| `add_relation` | 论证关系（`derives/supports/limits/opposing`） | 扩展到 `o-*`（§6.4） |
| `add_open_question` | 写「存疑与待验证」 | 不变（D-1） |
| `create_card` | **兼容别名** → `create_knowledge` | 产出 `I1` 迁移提示 |
| `append_card` | **兼容别名** → `append_knowledge` | 产出 `I1` 迁移提示 |

主链路 op 数从 7 增至 9（不含两个兼容别名）。

**别名实现要求**：在 `internal/plan/expand.go` 的规范化阶段做**名称改写**，
使 `internal/plan/validate.go` 与 `executor.go` 只见到规范名。
禁止在 validate/executor 里各写一份 `case "create_card"`——两处分支会漂移。

### 4.5 `create_opinion` / `append_opinion` 校验

- `create_opinion` 必须带 `观点` 分区（对应 `create_knowledge` 的 `知识内容`），
  缺失报 `E2`：「create_opinion 缺「观点」：观点没有主张就不成立」。
- `create_opinion` 不得携带 `validation` 以外的新 frontmatter 键；
  且**不得**在 plan 里直接把 `validation` 设为 `validated`/`rejected`——
  创建即验证等于绕过用户授权，报 `E2`。
- `append_opinion` 只能写 `论据与推理` / `条件与反例` / `待验证`（§3.3）。
- Knowledge 侧的 `coreKnowledgeGate`（`validate.go:697`）等价迁移到
  `create_knowledge`，判据不放宽。

### 4.6 收敛（convergence）语义

三维度（`core_knowledge` / `conditions` / `reuse_purpose`）与七值关系枚举
（`independent_new` / `same_semantics` / `non_core_supplement` / `core_change`
/ `conflict_coexist` / `uncertain` / `deprecated`）**不变**
（`internal/plan/schema.go:56`，规则见 `internal/rules/converge.go`）。

新增约束：`convergence[].card` 字段现在可指向 `k-*` **或** `o-*`。
字段名保持 `card` 不改——改名会破坏所有存量 plan，收益仅为命名美观。
本决策记入 D-2。

> **决策记录 D-2：`convergence[].card` 不改名为 `target`。**
> 语义扩展为「收敛对象（Knowledge 或 Opinion）」，由文档说明，不动键名。
> 理由：`card` 是 ChangePlan 顶层 8 键之一 `convergence[]` 的成员键，
> 改名会让全部存量 plan 与 `internal/rules/converge.go` 的七档收敛判定同时失效，
> 而收益仅为命名美观；且 `internal/model/blacklist.go` 已把 `card.type` 一类
> 字段列入弃用路径，再动 `card` 家族键名会让黑名单语义更难解释。
> 若要推翻：需改 `internal/plan/schema.go:56` 的 `convergence[]` 结构体标签、
> `internal/plan/expand.go` 的别名规范化（把 `card` 收为 `target` 的兼容读）、
> `internal/plan/validate.go` 的收敛校验诊断路径文案，并为存量 plan 增加
> 一轮 v1→v2 键名迁移，代价与收益不成比例。

---

## 5. 渲染与读路径

### 5.1 Note 渲染

`整理正文` 内，`role: agent` 的块按 `annotation` 渲染显式前缀：

```markdown
### 1. 背景

…按原文顺序整理的正文…

> **[Agent 辨析]** …容易混淆的概念边界…

### 2. 方法
```

选择 blockquote + 粗体标记而非 HTML 注释或自定义容器，理由：
(a) 在任何 Markdown 渲染器里都可见；(b) 是纯文本，不破坏字节保真读写；
(c) 可被 `internal/mdfile/block.go` 的 `SplitBlocks` 正确切块。
内置标签按 §4.2.2 固定映射；扩展类型使用 `label`。writer 不得把所有 Agent 块
降级为 `[Agent 补充]`。

`提取结果` 分区在末尾按两组列出本 Note 产出的实体：

```markdown
## 提取结果

### Knowledge
- `k-20260915-react-loop` — ReAct Loop 的执行流程
- `k-20260915-harness-capabilities` — Harness 的五类能力

### Opinion
- `o-20260915-core-boundary-cost` — 核心边界决定扩展成本 `[pending]`

### 覆盖矩阵
| 模块 | 来源范围 | 语义模块 | 处置 |
| --- | --- | --- | --- |
| `m-001` | `L14-L28` | 背景与问题定义 | Note-only：文章元数据 |
| `m-002` | `L29-L51` | 方法的三个执行步骤 | `k-20260915-example-method` |
```

### 5.2 检索拆分

| 命令 | 行为 |
| --- | --- |
| `eg search <q>` | **默认只搜 Knowledge**，给出干净的知识视图 |
| `eg search <q> --kind opinion\|all` | 显式放宽检索面 |
| `eg opinion search <q>` | Opinion 专用检索（带 `validation` 标记与关系摘要） |
| `eg opinion show <o-id>` | 单条 Opinion 详情（五分区 + 支持/限制/反对视图） |
| `eg card show <k-id>` | 不变（仅 `k-*`；传 `o-*` 报 `E` 并提示改用 `eg opinion show`） |
| `eg rel <id>` | 支持 `k-*` 与 `o-*` 两侧 |

`eg search` 的默认收窄是**行为破坏性变更**：v1 的 `eg search` 本就只搜知识卡
（`internal/query/search.go:147`），因此实际语义**不变**；变的是它现在有了
可放宽的 `--kind`，且 Opinion 不会因为「都是卡」而混入。此点记入 CHANGELOG。

### 5.3 `eg context` 输出

`query.Context`（`internal/query/context.go:107`）的 `Candidates` 单一列表
拆为两个字段：

```json
{
  "source": "s-…",
  "notes": [...],
  "knowledge_candidates": [...],
  "opinion_candidates": [...],
  "proposals": [...],
  "base": { "id": "content_hash", ... },
  "diagnostics": [...]
}
```

**这是 `--json` 信封内的破坏性变更**，是本 Epic 唯一允许的破坏性接口变更。
兼容策略：过渡期同时保留 `candidates`（= `knowledge_candidates` 的内容）
并在 `diagnostics` 里给 `I1`「`candidates` 已弃用，改读 `knowledge_candidates`」。
`candidates` 在 0.8.0 移除。此策略记入 D-3。

> **决策记录 D-3：`eg context` 过渡期同时保留 `candidates`。**
> 决策内容：`--json` 信封同时输出 `candidates`、`knowledge_candidates`、
> `opinion_candidates` 三个字段，`candidates` 内容恒等于 `knowledge_candidates`，
> 并伴随 `I1` 弃用提示；`candidates` 在 0.7.x 全程保留，0.8.0 删除。
> 理由：(a) `eg context` 是 Agent 主链路的唯一读入口，直接删字段会让所有
> 已写好的调用方在升级瞬间静默拿到空候选集，而不是显式报错；
> (b) 保留是纯加性的，不影响 `eg search` 默认不含 `o-*` 这一出口条件；
> (c) 弃用信号走既有 `diagnostics` + `I1` 机制，无需新增告警通道。
> 若要推翻（即立刻硬切）：改 `internal/query/context.go:107` 删字段、
> 删对应 `I1` 诊断、改 `internal/cli/context.go` 的渲染，并把
> §11 出口条件第 4 条改写为「不含 `candidates`」；同时需在 CHANGELOG 标注
> breaking，且本 Epic 的「唯一允许的破坏性接口变更」范围会从「字段新增」
> 扩大为「字段删除」，需 owner 另行批准。

### 5.4 命令名册

顶层命令从 **22 条增至 23 条**（新增 `opinion`）。
`eg opinion` 采用与 `eg proposal` 相同的子命令结构
（`internal/cli/proposal_cmd.go:83-95` 的 `ProposalSubcommands()` + `Subs` 字段是模板）：

```text
opinion search|show|validate|reject
```

**这会触发文档双向恰等门禁**，必须同步更新（见 §8）。

---

## 6. Opinion 验证生命周期

### 6.1 状态机

```text
pending ──validate──▶ validated
   │                      │
   │                      └──reject──▶ rejected
   └──reject──▶ rejected ──reopen──▶ pending
validated ──reopen──▶ pending          （出现新反例时降级）
```

### 6.2 命令

```text
eg opinion validate <o-id> --reason "…"   # → validated
eg opinion reject   <o-id> --reason "…"   # → rejected
```

两者都是**用户显式路径**：必须带 `--user-request`，产生 `commit verb=process`，
只写 `validation` 单键（外加 `updated_at`），不改任何正文分区。
`--reason` 必填，写入 `待验证` 分区的追加块，保留判定理由的可追溯性。

回到 `pending`（复议）复用 `eg opinion validate --reopen`，避免再加命令。

### 6.3 授权

`validation` 的流转**只能**走 P-U（用户显式路径）。Agent 自动路径 P-A
对 `validation` 一律 deny。这与 `deprecate`/`restore`/`delete` 的现有口径一致：
状态判定权归用户。

### 6.4 「必须能看到支持、限制和反对它的内容」

不引入新机制——复用现有论证关系枚举
（`internal/model/enums.go:143`：`derives` / `supports` / `limits` / `opposing`）。

`eg opinion show <o-id>` 必须渲染三组视图：

```text
支持（supports →/← 本观点）
限制（limits）
反对（opposing）
```

正反向都要显示（沿用 `eg card show` 的正反向关系视图实现）。
判定标准：一条 Opinion 若 `validation: validated` 但**零条** `supports` 关系，
`eg check` 报 `R3` 类关系异常——「已验证的观点没有任何支持证据」。

> **附录（2026-09-19 追加 · 裁决 A-62 · 不改写上文结论）**：上文「零条 `supports` 关系」
> 的方向与有效性口径由前置裁决
> `2026-09-19-opinion-unsupported-validated-adjudication.md`（A-62，同目录）细化如下——
> 「支持证据」恒指**指向本观点的 incoming 边**（`X --supports--> O`，以本观点为 `target`），
> 一条**有效** incoming 支持要求**持有方存在且未逻辑删除**（deprecated 不失格，与 R3/R7 判定
> 不做展示面过滤一致），重复边去重后计一次，`Scan` 为 nil（未取数）时整体不判；本观点**正向**
> `supports`（`O --supports--> Y`，本观点作为他者的支撑）**不计入**。该判据落为**新增 R3 子检查**
> `opinion_unsupported_validated`（`severity=warning`、码 `W29`，`eg check` 与 `eg reconcile`
> 都检出、零 `RepairSpec`、零自动降级 `validation`），代码实现（对账封闭表 12→13 等连锁改动）
> 归 T-007 的 7C 代码批，本附录本身不改任何代码。

---

## 7. 派生索引 v2

- `.index/` 仍是**可重建派生物**；Markdown 恒为唯一权威来源。
- 现有 6 表：`index_meta` / `cards` / `cards_fts` / `relations` / `files` / `skipped`
  （`internal/index/schema.go`）。
- **方案：在 `cards` / `cards_fts` 上加 `kind` 判别列**，取值 `knowledge` | `opinion`，
  而不是新建 `opinions` / `opinions_fts` 两张表。理由：
  (a) 排序与分页逻辑（`internal/query/scan.go:573` 的 `SortEntries` 四级全序）
      只需一份实现；
  (b) 关系表 `relations` 天然跨类型，分表会让 join 分裂；
  (c) 检索面收窄只是一个 `WHERE kind = ?`。
- 额外列：`validation`（仅 opinion 行非空），供 `eg opinion search` 直接过滤。
- `IndexSchemaVersion` 由 `1` 升至 `2`。**沿用现有「版本不匹配 → 整库重建」策略**
  （`internal/index/schema.go:18`），不写增量迁移脚本——索引是派生物，重建即正确。
- 回退路径（`internal/query/backend.go` 的 `SelectBackend` 直接扫 Markdown）
  必须同样支持按 kind 收窄，否则索引不可用时 `eg search` 会混入 Opinion。

> **决策记录 D-4：索引用 `kind` 判别列，而非 `opinions` / `opinions_fts` 分表。**
> 决策内容：`cards` / `cards_fts` 各加一列 `kind`（`knowledge` | `opinion`）
> 与一列 `validation`（仅 opinion 行非空）；表数量保持 6 张不变。
> 理由：见上述 (a)(b)(c)——排序全序实现单一化、`relations` 跨类型 join 不分裂、
> 检索收窄退化为一个 `WHERE kind = ?`。
> 若要推翻（改为分表）：需在 `internal/index/schema.go` 新增两张表与其 FTS 触发器、
> 在 `internal/index/rebuild.go` 写两条装载路径、把 `internal/query/search.go`
> 的扫描与分页拆成两份或引入 UNION ALL 后重新证明四级全序稳定、
> 并让 `internal/query/backend.go` 的回退路径与双表结果保持等价；
> 同时 `relations` 的 join 需要按端点类型分派。工作量约为本方案的 3 倍，
> 且新增「两套排序实现是否等价」这一必须证明的风险点。

---

## 8. 文档与合同同步（硬门禁）

本仓存在**机械双向恰等**门禁，改命令必须同步改文档，否则 CI 拦截：

| 门禁 | 位置 | 断言 |
| --- | --- | --- |
| CLI 名册双向恰等 | `tests/e2e/docs-cli/docs_cli_roster_and_embedded_skill.sh` | `README.md` / `INSTALL.md` / `skill/SKILL.md` 中的 `eg <name>` 集合 == 真实命令集 |
| `--help` 契约 | `tests/_staged/internal/cli/root_help_contract_test.go` | `eg --help` 输出的命令表 == 注册表 |
| 平台与限制文案 | `tests/contract/release-hygiene/platform_and_workspace.sh` | 平台限制 literal 逐字存在 |
| 测试资产清单 | `tests/manifest/inventory.tsv` | 每个测试文件的层级/域/用例数/字节数 |

必须同步更新的文件：
`README.md`、`README.zh-CN.md`、`INSTALL.md`、`skill/SKILL.md`、`CHANGELOG.md`、
`tests/manifest/inventory.tsv`，以及命令总数「共 22 条」→「共 23 条」的所有出现处。

`skill/SKILL.md` 是 Agent 面向的操作合同，必须重写以下部分：
Note 审阅式学习口径（§2.1 的定义与允许/禁止两张表逐条进入 SKILL）、
来源保真清单与 `omissions[]`（§4.2.1）、七类内置批注及扩展规则（§4.2.2）、
语义模块拆分与覆盖矩阵（§4.2.3）、Knowledge vs Opinion 判定标准（§2.2）、
`blocks[]` 写法（§4.2）、四个新 op、Opinion 生命周期。

SKILL 必须明确：批注类型与数量按真实学习需求决定，不要求每篇凑齐七类；
来源保真和 `缺漏 = 0` 则是每篇必过项。few-shot 必须同时提供目标 Note 与
对应覆盖矩阵，Agent 学习其方法，不复制固定批注数量。

---

## 9. 迁移

### 9.1 工具形态

**不新增顶层命令。** 迁移器落在 `scripts/migrate-schema-v2/`，作为一次性工具。
理由：迁移是**一次性、面向未过目本地 Vault**的动作，把它做成 `eg migrate`
会永久占用命令名册一格、并触发全套文档门禁，成本与收益不匹配。

> **决策记录 D-5：迁移器落在 `scripts/`，不占命令名册。**
> 决策内容：以 `scripts/migrate-schema-v2/` 提供一次性迁移器，
> 不新增 `eg migrate`；顶层命令数因此只从 22 增至 23（仅 `opinion`），
> 而非 24。
> 理由：(a) 迁移只需对已存在的本地 Vault 跑一次，之后永久无用，
> 但命令名册是长期契约；(b) 每加一条命令都要同步 README / README.zh-CN /
> INSTALL / SKILL 四份文档并通过双向恰等门禁，给一次性动作付永久文档税不合理；
> (c) `scripts/` 下已有 `check-public.sh` 等一次性/辅助工具的先例。
> 若要推翻（改为 `eg migrate`）：需在 `internal/cli/commands.go:17` 注册、
> `internal/cli/root.go:202` wire、新增 `internal/cli/migrate_cmd.go`、
> 定义其退出码语义与 `--strict` 行为，并把 §11 出口条件第 5 条的
> 命令数从 23 改为 24、同步四份文档，且此后无法在不 breaking 的前提下移除。

### 9.2 迁移动作

1. **重建当前 Note**：按原文五个章节生成审阅式学习版，替换退化的 38 行摘要。
   `材料提炼` + `Agent 分析` 的存量内容合并进 `整理正文`，
   来源块逐项记录 `source_ref`，Agent 段落按真实教学意图转为七类内置批注或显式扩展类型。
   删除全部批注后，来源正文仍须完整、连续、顺序忠实；图片、表格、代码、脚注及其顺序保真。
2. **`产出知识卡` → `提取结果`**：分区改名 + 按 Knowledge / Opinion 两组重排。
3. **先做模块分解再提炼**：逐项列出定义、流程、API、边界、时效事实与判断，
   建立 `Note 模块 → Knowledge/Opinion/Note-only` 覆盖矩阵，`缺漏 = 0`。
4. **Knowledge / Opinion 补全**：迁移审计基准为 16 个 Knowledge、9 个 Opinion；
   独立 API、流程、边界和时效事实不得被综合卡吞并，事实不得只藏在 Opinion 的论据里。
5. **关系补全**：材料关系覆盖全部 25 个产物；论证关系基准为 29 条
   （`supports` / `limits`），每条均有具体 reason。
6. **旧类型收口**：被错误存为 Knowledge 的判断迁为 `o-*`；旧 `k-*` 走逻辑删除路径
   并以 `replaced-by` 指向新 `o-*`，**不物理删除**，保留可追溯性。
7. **收尾**：`eg index rebuild` + `eg reconcile` + `eg check` 全绿；
   golden manifest 的数量、顺序、哈希和关系计数全部通过。

### 9.3 流程冻结

**迁移完成前，当前 Vault 暂停 `mark-reviewed`**（见 `I-…-006`）。
理由：`reviewed_at` 表示「用户已过目并认可」，对即将被重建的退化 Note 打这个标记，
会把错误内容固化为「已认可」，并让 `unreviewed` 失去信号意义。

---

## 10. 非目标（明确不做）

- 不给每个 Claim 加认识论元数据（confidence / provenance graph / 作者模型）。
- 不引入 `card.type` / `stance` / `lean`（§3.1）。
- 不改 ChangePlan 顶层 8 键（§4.1）。
- 不改三维度收敛与七值关系枚举（§4.6）。
- 不为派生索引写增量迁移脚本（§7）。
- 不把 Note 的 `W21` 启发式结构覆盖做成 error 级门禁（§4.2 第 8 条）；
  `source_ref` 完整性与 `extraction_coverage` 的 `missing` 仍是 error。
- 不做跨 Vault 的通用迁移工具（§9.1）。
- 不把七类内置批注做成每篇必须凑齐的配额。
- 不用章节标题命中或单张综合卡替代模块级提炼覆盖。

---

## 11. 出口条件

1. 四类实体（`s-` / `n-` / `k-` / `o-`）在 model / mdfile / store / plan / txn /
   reconcile / index / query / cli 九层全部贯通，无「只在某层认识 Opinion」的断点。
2. `plan_version: 1` 与 `2` 都能执行；`create_card` / `append_card` 别名产出
   `I1` 提示且落盘结果与 `create_knowledge` / `append_knowledge` **字节等价**。
3. `eg search` 默认结果集不含任何 `o-*`；`eg opinion search` 只含 `o-*`。
4. `eg context --json` 同时输出 `knowledge_candidates` / `opinion_candidates`。
5. 命令名册 23 条，四份文档与 `--help` 双向恰等门禁全绿。
6. 索引 `schema_version: 2`；`kind` 列可用；索引不可用时的回退路径同样按 kind 收窄。
7. Note 去掉全部 Agent 批注后仍是完整连续的来源正文；来源范围无遗漏、无重复、无交叉，
   图片、表格、代码、链接和脚注的数量与顺序保真。
8. 七类内置 annotation 均可按固定标签渲染，扩展 key 必须携带 label；writer 不再把所有
   Agent 块统一渲染为 `[Agent 补充]`。
9. 每篇 Note 都有模块级覆盖矩阵，所有模块均映射到 `k-*` / `o-*` 或带理由的
   `Note-only`，`缺漏 = 0`；`W21` 只保留为启发式诊断。
10. 现存 Vault 迁移完成：基准为 16 个 Knowledge、9 个 Opinion、29 条论证关系，
   `eg check` / `eg reconcile` 全绿，`mark-reviewed` 解冻。
11. 同一 commit 上 `core` / `full` / `race` / `perf` / `manifest` / `mutation` / `fuzz`
   七档全绿，出 r6 交付包（r3/r4/r5 不得覆盖）。
12. 公开仓卫生：`scripts/check-public.sh` 通过；新增 commit 的 author/committer
   为发布者个人身份；提交消息与代码不含公司身份、助手产品身份、公司邮箱、
   凭据路径或工作区绝对路径。

---

## 附录 A：受影响代码位清单

| 层 | 文件 | 改动 |
| --- | --- | --- |
| model | `internal/model/id.go:20-27,51` | 加 `PrefixOpinion`、`ParseID` 前缀表、`NewOpinionID` |
| model | `internal/model/artifact.go` | 加 `Opinion` 结构体（含 `Validation`） |
| model | `internal/model/blacklist.go:13` | 对称加 `opinion.type/stance/lean` |
| model | `internal/model/enums.go:143` | 关系枚举不变；确认可用于 `o-*` |
| plan | `internal/plan/strict_exempt.go` | `W21` 加入 strict 豁免（落点更正见 §4.3；原定 `internal/model/strict.go`） |
| mdfile | `internal/mdfile/sections.go:19-88` | 加 `KindOpinion`；重写 Knowledge/Note 分区常量与 `KnownSections`/`RequiredSection`/`AutoWritableSections` 四个 switch |
| store | `internal/store/layout.go:12-23` | 加 `DirOpinions` + `OpinionRel` |
| store | `internal/store/scan.go:60` | 确认 `ScanIDs` 自动覆盖新前缀 |
| store | `internal/store/schema.go:38-51` | 透出 Opinion 分区判定 |
| plan | `internal/plan/schema.go:18,96,159` | `PlanVersion` 支持集；`Op` 加 `Blocks[]`、`Omissions[]`、`ExtractionCoverage[]`；`NoteBlock` 加 `source_ref` / `annotation` / `label`；op 名册 |
| plan | `internal/plan/expand.go:59` | 别名规范化；`blocks[]` 的 extra 分类 |
| plan | `internal/plan/validate.go:300,548,658` | op 分发加 4 个 case；`writeNote` 校验来源范围、批注与覆盖矩阵；`W21` |
| plan | `internal/plan/diagnostics.go:53` | `WarningCodes()` 末位追加 `W21`（闭合集 12→13，D-9） |
| plan | `internal/plan/matrix.go:214-231` | 加 Opinion 授权行 |
| plan | `internal/plan/authorize.go:150,216` | Opinion 分区授权 |
| plan | `internal/plan/executor.go:251` | 执行分发 + Note 按数组序写 + annotation / 覆盖矩阵透传 |
| txn | `internal/txn/journal.go:45` | 按路径索引，预期无需改；需验证 |
| reconcile | `internal/reconcile/reconcile.go:26,151` | Input 采样加 opinions；R3/R4 覆盖 |
| index | `internal/index/schema.go:18` | `schema_version` 2；`cards`/`cards_fts` 加 `kind`/`validation` |
| index | `internal/index/rebuild.go` | 扫描 `opinions/` |
| query | `internal/query/search.go:126,147` | 按 kind 收窄；四级全序不变（`internal/query/scan.go:573` 的 `SortEntries` 不改） |
| query | `internal/query/context.go:107` | 双候选字段 |
| query | `internal/query/backend.go` | 回退路径按 kind 收窄 |
| cli | `internal/cli/commands.go:17` | 在 `commands()` 名册注册 `opinion` 命令组 |
| cli | `internal/cli/proposal_cmd.go:83-95` | 多子命令结构参照模板（`Subs` + `Subcommands()`） |
| cli | `internal/cli/root.go:202` | `Wire("opinion", …)` |
| cli | `internal/cli/opinion_cmd.go` | 新文件 |
| cli | `internal/cli/search.go` / `context.go` / `card.go` / `rel.go` | 按 §5 调整 |
| docs | `README.md` / `README.zh-CN.md` / `INSTALL.md` / `skill/SKILL.md` / `CHANGELOG.md` | §8 |
| tests | `tests/manifest/inventory.tsv` + 各层用例 | 新增覆盖 |

## 附录 B：决策记录索引

十一条决策各自在正文中或本附录中有一个决策块，
每块均含**决策内容 / 理由 / 若要推翻需要改哪里**三部分；十一条全部已定稿，
无悬置项。D-1..D-7 定稿于契约阶段；D-8 是实施期追加的**落地顺序**修订，
D-9 是实施期追加的**编号更正**；D-10/D-11 来自完整 golden Note 的二次覆盖审计，
分别收紧 Note 保真/批注合同与提炼完成条件。

| ID | 决策 | 章节 | 推翻代价量级 |
| --- | --- | --- | --- |
| D-1 | Note 保留 `存疑与待验证` 分区 | §3.2 | 小：分区常量 + 存量收口方式 |
| D-2 | `convergence[].card` 不改名为 `target` | §4.6 | 大：破坏全部存量 plan |
| D-3 | `eg context` 过渡期同时保留 `candidates` | §5.3 | 中：需 owner 批准扩大破坏性范围 |
| D-4 | 索引用 `kind` 判别列而非分表 | §7 | 大：约 3 倍工作量 + 排序等价性风险 |
| D-5 | 迁移器落在 `scripts/`，不占命令名册 | §9.1 | 中：命令数 23→24 + 永久文档税 |
| D-6 | `W21` 结构覆盖只报 warning，不入 strict | §4.3 | 中：须先给出不可凑数的判据 |
| D-7 | Knowledge 移除 `解释与依据` / `理解自检` | §3.2 | 大：五处代码 + 需重解 `I-…-002` |
| D-8 | 分区模板切换与写口同一提交落地（顺序修订） | 附录 B | 小：只影响任务边界，不影响契约结论 |
| D-9 | 结构覆盖诊断编号由 `W25` 更正为 `W21`（编号更正） | §4.3 / 附录 B | 小：一处常量 + 闭合集断言 |
| D-10 | Note = 完整来源正文 + 可移除的显式审阅批注；annotation 可扩展 | §4.2.1 / §4.2.2 | 大：plan / writer / parser / SKILL / migration |
| D-11 | 模块级覆盖矩阵是提炼完成条件，`W21` 不是覆盖证明 | §4.2.3 | 大：plan schema / validator / renderer / migration |

> **决策记录 D-8：Knowledge / Note 的分区模板切换不在「Opinion 基座」任务里做，
> 而与 ChangePlan v2 写口同一提交落地。**
> 决策内容：T-…-002 收敛为纯加性——只新增 `KindOpinion`、观点五分区、`opinions/` 布局、
> `validation` 枚举与 `opinion.*` 黑名单对称项；D-7 的 Knowledge 三分区与 §3.2 的
> Note 四分区，连同授权矩阵、`plan` 校验、writers、CLI 文案与存量用例的同步，
> 一并由 T-…-003 承担。§3.2 与 D-7 的**结论不变**，只改落地顺序。
> 理由：(a) `mdfile.AutoWritableSections` / `RequiredSection` 是
> `internal/plan` 授权判定与 `write_note` / `append_card` 校验的唯一口径来源，
> 只改模板会让写口处于「模板 v2、校验 v1」的自相矛盾状态；
> (b) 实测证据——先按原计划改模板后 `go test ./...` 出现 291 条失败，
> 分布在 `internal/plan`（`write_note` 强制「材料提炼」、`append_card` 白名单、授权矩阵）、
> `internal/store`（writers / 原子事务）、`internal/cli`（apply / card show / check）
> 与 `test/e2e` 四层，全部是「模板变了但执行者没变」的必然失败，而非测试脆弱；
> (c) 把这些必然失败留在任务之间，等价于 frozen-red——本 Epic 明令禁止。
> 若要推翻（仍在基座任务里一次切完）：需把 T-…-003 的
> `internal/plan/matrix.go` / `authorize.go` / `validate.go` 与 T-…-006 的读路径文案
> 提前并入 T-…-002，实际后果是 T-002/T-003 合并为一个跨四层的大任务，
> 依赖顺序表（§10）与两条任务分支需重画。

> **决策记录 D-9：结构覆盖诊断的编号由 `W25` 更正为 `W21`，并把 `plan` 包的
> 编号闭合集从 `W1..W12` 扩为 `W1..W12 ∪ {W21}`。**
> 决策内容：§4.2 第 8 条与 §4.3 的结构覆盖 warning 落在 `W21`；
> `internal/plan/diagnostics.go` 的 `WarningCodes()` 由 12 项扩为 13 项（`W21` 末位追加，
> **不**改 `W1..W12` 的既有语义与顺序）；`internal/plan/strict_exempt.go` 的 strict 豁免登记 `W21`；
> `internal/query/diagnostic.go` 与 `internal/reconcile/r7_support.go` 里
> 「不使用 `W21`」的越域注释保持有效——它们说的是「查询域 / R7 不得借用」，
> 现在 ChangePlan 域正式认领它，恰好印证该留白。
> 理由：(a) 契约阶段写下 `W25` 是**事实性错误**——`W25` 自 M5 起已是
> `internal/query/page.go:51` 的 `CodeResultTruncated`（查询结果截断），
> 一个机器码不能同时表达「分页截断」和「Note 疑似退化」两件无关的事，
> 否则 `--json` 消费方无法据 `code` 分派；(b) `W21` 是
> `internal/cli/codes.go:24` 占用总览（`W1`–`W20`、`W22`–`W28`）里的**唯一空号**，
> 且已被两处注释逐字预留给「ChangePlan 分级表」，本诊断正是 ChangePlan 写前校验的产物；
> (c) 选择向 `W29+` 顺延会让 ChangePlan 域的号段割裂成 `W1..W12` + `W29`，
> 而 `W21` 就在既有留白里，读者按占用总览就能定位。
> 若要推翻（换回 `W25` 或改用新号）：换回 `W25` 必须先给 `CodeResultTruncated` 改号，
> 连带 `internal/query/page.go`、`internal/cli` 的截断文案、
> `tests/e2e` 的分页断言与 CLI 合同 §5 的码表同时改，且会破坏存量 `--json` 消费方；
> 改用 `W29+` 只需改本文件、`internal/plan/diagnostics.go` 的闭合集与
> `internal/plan/strict_exempt.go` 的豁免登记，代价小但会留下号段割裂。
