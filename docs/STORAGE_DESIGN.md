# EverGreen 存储技术方案：分块边界与物化

> 状态：已定稿（待实现）｜适用范围：Note 候选分块、知识卡物化、派生层分工
> 语言：本文为中文正式方案，与 `docs/ARCHITECTURE.md`（英文，现状描述）配套阅读。

本文回答一个问题：**Agent 在 Note 原位标出的候选块，边界应该以什么形式存在于权威数据里。**
结论一句话：**权威格式仍是 Markdown，不变；变的只是边界从哪里来——从"写在正文里的不可见标记"改为"由标题层级推导的结构"。**

---

## 1. 背景与问题

现行工作流已经收敛为：

1. 处理一篇 source，**只生成 Note**，不直接生成 `knowledge/` 与 `opinions/` 产物。
2. Agent 在 Note **原位**标出候选块，标明类型（`knowledge` / `opinion`）、无日期逻辑名、起止边界。
3. 使用者在 Note 上审阅、修改，确认后由脚本**确定性截取**、补日期与 frontmatter、按逻辑名落文件，
   这一步**不再调用模型**做总结或改写。

第 2 步的"边界"此前的候选实现是 HTML 注释哨兵：

```markdown
<!-- kb:begin type=knowledge name=agent-development-boundaries -->
候选正文……
<!-- kb:end -->
```

它在"使用者愿意配合维护标记"的假设下三条判据全绿。但真实约束不是这样：

> **使用者在 Note 原文里改的是正文，不是边界。**

段落内改字、增删段落、段落重排都会发生；把内容"挪进两行标记之间"这种要求是可以兜底、但不可能长期成立的
交互契约。哨兵在渲染视图里不可见、在源码态没有语义，使用者既看不到边界，也没有动力维护边界。
一旦正文编辑与边界维护脱钩，哨兵方案的第一否决项直接失败。

## 2. 判据

分两类。**否决项**不满足即出局，**权衡项**用于排序。

| 类别 | 判据 | 含义 |
| --- | --- | --- |
| 否决项 1 | 权威可评审 | 权威数据必须是人手可编辑的纯文本，且 Git diff / blame / review 有意义 |
| 否决项 2 | **改正文鲁棒性** | 使用者只做正文编辑、不维护任何边界，重新解析后每个候选块的身份与内容仍然正确 |
| 权衡项 1 | 改造代价 | 相对现有 `internal/mdfile` / `internal/plan` / `internal/index` 的增量 |
| 权衡项 2 | 可视化可达性 | 是否存在现成前端可直接打开权威数据，或可低成本挂接 |
| 权衡项 3 | 退出成本 | 将来要换存储形态时，能否一条命令无损剥离本方案引入的语法 |

否决项 2 是本方案的支点，且它有可执行形式：**能写成一条随机扰动正文后的不变式断言**（见 §9 断言 1）。

## 3. 选型结论

被评估过的边界机制共六族：正文内哨兵标记、结构内生边界（标题小节 / 围栏容器）、org 子树、
文件即块、外挂选择器与 CRDT 锚点、块原生可编辑运行时（权威数据为数据库）。

- 外挂选择器与 CRDT 锚点整族在否决项 2 上出局：字符偏移在资源变化后极其脆弱（W3C 自陈 very brittle），
  工业实现要靠模糊重锚概率性恢复；CRDT 相对位置离开其文档对象即无法解析，无法持久化进纯文本权威层。
- 块原生运行时（权威数据是 SQLite / 私有 JSON）与否决项 1 直接冲突：权威数据不可 diff、不能用普通同步盘、
  官方均声明"非用户可编辑"。
- org 子树在两条否决项上都成立，但要付出整套换格式与换生态的代价。
- 文件即块缺少"把目录拼成一篇 Note"的可靠现成前端，且会打破现有"单文件字节切片"模型。
- **标题小节**是唯一在五条判据上都不亮红灯的选项，且落在本仓已有能力的延长线上。

**定稿：三级边界优先级。**

| 级别 | 机制 | 定位 |
| --- | --- | --- |
| **L1** | 标题小节（heading-scoped section）+ 标题属性 ID | **主边界**，覆盖全部常规候选 |
| **L2** | 围栏容器（Pandoc fenced div 最小子集） | **兜底**，只用于标题层级表达不了的情形 |
| **L3** | HTML 注释哨兵 | **退役**，迁移期只读兼容 + 告警，不再新写 |

## 4. 语法定稿

### 4.1 L1 · 标题小节即块（主路径）

候选块以 **H3 小节**为单位；H2 保留为分区语义（沿用 `internal/mdfile/doc_index.go` 的 `indexSections`）。
逻辑名写在标题行尾的属性里。

```markdown
## 待处理候选

### Agent 开发边界的三条硬约束 {#k-20260918-agent-development-boundaries}

边界一：Agent 不得越过工作区写入宿主目录。

边界二：所有外部调用必须显式声明超时与重试上限。

### 下一张候选卡的标题 {#k-20260918-next-candidate}

这里开始就是另一张卡片了。
```

**边界定义**：从该 H3 标题行的**首字节**起，到**下一个同级或更高级标题行的前一个字节**止；
文档结束时到文件末尾。这条规则与 org 的 section 定义同构，与 Pandoc `--section-divs` 的推导一致。

**属性语法**采用 Pandoc `header_attributes`（`{#identifier}`）。Go 侧由 goldmark 的
`parser.WithAttribute()` 原生解析，已本地实测通过（goldmark v1.8.6），**无需第三方扩展**。

**类型维度**用 class 承载，与 ID 正交：

```markdown
### 标题 {#k-20260918-slug .knowledge}
### 标题 {#p-20260918-slug .opinion}
```

类型改写因此是"改一个 class 词"，逻辑名改写是"改一个 identifier"，都在标题行内完成，不牵动正文。

**为什么它满足否决项 2**：使用者在小节内部做任何正文编辑都不会触碰前后两条标题行，边界自动保持。
这是与哨兵的本质差别——边界不再是正文里的一行内容，而是文档结构本身。

**教学批注与候选标记是两个独立维度**：`[Agent 强调]` 之类的行内批注写在正文里，不参与边界推导；
候选标记只存在于标题行（或 L2 围栏行）的属性里。两者互不干扰。

### 4.2 L2 · 围栏容器（兜底）

仅在标题层级表达不了的两种情形下使用：同一小节里要圈出多个互不相邻的候选；候选内容跨越标题结构。

```markdown
### 一次会议里冒出的三个想法

前面这段是背景，不属于任何候选。

:::{#k-20260918-agent-development-boundaries .knowledge}
Agent 开发边界的三条硬约束……

这段也属于同一张卡片。
:::

中间这段又是背景。

:::{#k-20260918-next-candidate .knowledge}
另一个候选的正文。
:::
```

语法遵循 Pandoc `fenced_divs` 的**最小子集**：

- 行首 ≥3 个连续冒号 + 属性 = 开围栏；行首 ≥3 个连续冒号且无属性 = 闭围栏。
- 围栏行与前后块之间留空行。
- 属性顺序固定为 identifier → classes。
- **不支持嵌套**。这是刻意收窄：AsciiDoc 开放块同样禁止嵌套，放弃嵌套能让自实现扫描器短到完全可控，
  避免押注体量极小且已停更的第三方 goldmark fences 扩展。

实现路径走 `internal/mdfile/block.go` 中 `SplitBlocks` 的 `fenceEnd` 扩展，代价低于处理跨行注释闭合。

### 4.3 L3 · 哨兵注释退役

读到 `<!-- kb:begin ... -->` / `<!-- kb:end -->` 时正常解析，同时输出一条告警并给出"转成 L1 或 L2"的
建议命令。解析器**不再为哨兵增加任何新能力**，写入路径最终移除（§8 第三步）。

退役理由除 §1 的否决项失败外，还有明确的反面先例：TiddlyWiki 为 CompoundTiddlers 自陈"不允许正文中出现
单独一行的 `+`"；CriticMarkup 与宿主 Markdown 语法的耦合让正确转换"相当复杂、边缘情况极多"。
**用正文里的特殊行做分隔符，一定会反噬普通编辑。**

## 5. 逻辑名与物化路径

逻辑名即标题属性或围栏属性里的 identifier，形如 `k-20260918-agent-development-boundaries`。

- 生成沿用 `internal/model/id.go` 的 `NewCardID` / `Slug`，格式 `k-yyyyMMdd-slug`：
  日期段取候选**创建日**，slug 段由标题文本 slug 化。
- Agent 在 Note 原位标注时写**无日期逻辑名**；日期在物化时由脚本补齐——这保持了"确认后才补日期"的既定流程，
  也让 Note 侧标记对人更短、更可改。
- ID 缺失时执行"有则复用、无则创建"，语义对齐 `org-id-get-create`，保证幂等。
- 字符集遵循可移植口径（首字符为字母、不含空格、不含 `? # / \ * " < > | %`）；`k-yyyyMMdd-slug` 已同时满足。

物化时文件名**直接等于逻辑名加 `.md`**，落位仍由 `internal/store/layout.go` 的 `CardRel` 唯一拼装：

```text
Note 侧：  domains/<domain>/notes/<n-id>.md
           └─ ### Agent 开发边界的三条硬约束 {#k-20260918-agent-development-boundaries .knowledge}

物化后：  domains/<domain>/knowledge/k-20260918-agent-development-boundaries.md
```

映射一对一且无歧义：文件名由 ID 决定、ID 由标题属性固定，因此同一逻辑名重复物化必然幂等。
领域仍**由目录唯一决定**（EG-DOM-01），frontmatter 不写 `domain`。

## 6. 物化流程：`materialize_card`

新增一个 op `materialize_card`，注册进 `internal/plan` 的封闭集合 `AllOpNames()`
（当前 17 个，本方案使之成为 18 个；`OpNames` / `M3OpNames` / `EditOpNames` / `M4OpNames`
四个历史分组的计数口径不改，新增走独立分组，见 §7）。

执行顺序固定五步：

1. **定位 Span**。按逻辑名求出字节区间：L1 走标题层级推导（H2 分区索引已由 `indexSections` 提供，
   需新增 H3 小节区间推导）；L2 走围栏行推导（`FencedContainer` 节点无 line segments，
   区间由子节点或围栏行自行算出）。
2. **取字节**。复用 `Unprocessed.Cut` 的"定位—剪除—自检"路径取出该段字节，保持现有保字节契约。
3. **写目标**。写入 `CardRel(domain, id)`，frontmatter 承载上下文字段，字段集合对齐 org
   `org-archive-save-context-info` 的默认语义：物化时间、源文件、标题路径、分类、标签。
4. **改源**。用 `internal/mdfile/mutate.go` 的 `ReplaceSectionBody` 把源 Note 中该小节正文替换为
   一行指向卡片的链接，**保留标题行与 ID**便于回溯。
5. **重建索引**。索引层不参与本次写入，事后由 `internal/index` 的 `Rebuild` 全量重建 `cards` 与 `relations`。

**第 4 步是移动而非复制。** org 官方对 `org-refile-copy` / `org-refile-keep` 的警告是"可能导致 ID 重复"；
本项目的逻辑名唯一性同样撑不住复制语义。保留原件的需求由 Git 历史满足，不在工作树里留两份。

## 7. 原子性与崩溃语义

跨进程互斥已由 `internal/txn` 的 `run.lock` 承载（`flock` 唯一真源、持锁期零 inode 替换、
10s 超时与退避、陈旧锁安全接管），事务日志与恢复已有 `journal.go` / `recover.go` / `commit.go`。
物化是**跨文件**写（写卡片 + 改 Note），必须整套复用，不新造机制：

- **两阶段落盘**：先写同目录临时名（`.../.k-20260918-slug.md.tmp`），再 `rename` 到正式名——
  同目录 rename 在主流文件系统上是原子替换。
- **先目标后源**：只有目标文件 rename 成功后，才用 `ReplaceSectionBody` 改源 Note。顺序反过来会在崩溃时丢内容。
- **journal 记录**：物化前写一条 journal（源文件、字节 Span、目标路径、逻辑名、当前阶段），
  崩溃后启动时按 journal 判定回放或回滚，使仓库只可能停在"完全未物化"或"完全已物化"两态。
- **全程持锁 + 索引事后重建**：整个 op 持 `run.lock`，禁止并发物化；索引不进事务，一律事后 `Rebuild`——
  索引可重建，所以不需要强一致。

## 8. 分层与派生层

自底向上五层，**权威层以下不变、派生层以上可整层替换**：

```text
[5] 可插拔可视化层   CommonMark 渲染器 / Obsidian / 自建视图      ← 可整层换掉
[4] 派生消费层       SQLite + FTS5 索引、块 JSON sidecar          ← 纯计算产物，可删可重建
--------------------------------- 隔离带 ---------------------------------
[3] 确定性物化层     materialize_card：定位 Span → 剪字节 → 落卡片
[2] 候选语义层       标题属性 {#id .class} / 围栏属性
[1] Markdown 权威层  Note / Card 的原始字节 + frontmatter
```

隔离带的位置是关键：第 1–3 层只认"Markdown 字节 + 标题小节 + 逻辑名"，不认任何具体前端。
org-roam 已把这种分工写成官方承诺——"笔记首先是普通 org 文件，数据库只是辅助索引，
即使 org-roam 不存在了笔记仍然可用"。本仓现有 `Rebuild`（清空非保留条目后全量构建）也正是这个取向。

**可视化可达性有三档互不依赖的出口**：

1. 直接用任意 CommonMark 渲染器 / GitHub / Obsidian 打开权威 Markdown。不支持标题属性的渲染器会把
   `{#id}` 留成标题末尾的字面文字（markdown-it-py 4.2.0 实测），可读性不受影响。
2. 需要"多文件拼成一篇"时，Obsidian 的 `![[Note#Heading]]` 按标题嵌入现成可用——
   **标题小节的边界与 embed 的寻址粒度天然对齐**，这是选 L1 而非 L2 的额外收益（围栏容器没有主流前端能按 ID 嵌入）。
3. 需要块级交互时用派生的块 JSON sidecar 喂自建视图，换视图不动权威层。

`knowledge/` 下一卡一文件保留"文件即块"的形态，但**不把它当权威分块机制**：它只是物化出口，
好处是重构近似单纯 rename、Git 侧有文件级重命名自动追溯；Note 侧不拆文件，因此避开了
"缺少能把目录拼成一篇笔记的可靠现成前端"这个真正的堵点。

## 9. 对现有代码的改造清单

按包列出增量，并标出触及**封闭合同**的点——这些点必须同步改注释里的可复算计数与对应契约测试，否则 CI 必红。

| 包 / 文件 | 改动 | 是否触及封闭合同 |
| --- | --- | --- |
| `internal/mdfile/doc_index.go` | `indexSections` 之外新增 H3 小节区间索引（首字节 → 下一同级/更高级标题前一字节）；沿用 `inFence` 状态机屏蔽围栏内的伪标题 | 否 |
| `internal/mdfile/block.go` | `SplitBlocks` 的 `fenceEnd` 路径扩展识别 `:::` 开/闭围栏；`BlockKind` 新增容器块型 | **是**（`BlockKind` 为封闭值集） |
| `internal/mdfile/` 新增 | 标题属性 `{#id .class}` 的解析与**剥离**（渲染/导出用），基于 goldmark `parser.WithAttribute()` | 否 |
| `internal/model/id.go` | 复用 `NewCardID` / `Slug`；新增"无日期逻辑名 + 创建日 → 完整 ID"的补齐函数 | 否 |
| `internal/store/layout.go` | 落位仍走 `CardRel`，不新增目录常量；物化临时名规则（同目录隐藏 `.tmp`）在此集中定义 | 否 |
| `internal/plan/` 新增 | `materialize_card` op：字段集合、校验、授权口径（需 `initiator=user`，因为它会改源 Note）、注册进 `AllOpNames()` | **是**（op 集合封闭，17 → 18） |
| `internal/txn/` | 物化写集接入既有 journal / commit / recover，新增"先目标后源"的阶段枚举 | **是**（journal 版本；若字段变化需升 `SupportedJournalVersion`） |
| `internal/index/schema.go` | **本方案不新增表**。当前恰 6 张表（`index_meta` / `cards` / `cards_fts` / `relations` / `files` / `skipped`）是冻结合同 §4.1。候选块的派生视图先落在**可重建的块 JSON sidecar**里；若将来确需 `blocks` 表，走独立 ADR + schema 版本升级 | **是**（故本方案主动避开） |
| `internal/cli` | `eg materialize` 命令；L3 哨兵告警输出；`eg migrate sentinel-to-heading` 转换命令；`eg export --plain` 逃生舱导出 | 否 |
| `skill/SKILL.md` | Agent 标注契约改为写 H3 标题 + `{#id .class}`，不再写哨兵注释 | **是**（Agent 操作契约） |

**特别说明——为什么派生层先用 sidecar 而不是新建表**：`internal/index/schema.go` 明确写着
"恰 6 张，合同 §4.1，本 task 不得增删"。候选块的查询需求（按逻辑名查块、按类型筛块）在数据量上远未到
需要 SQL 的程度，且它 100% 可由权威 Markdown 重建。先用 sidecar 能让本方案**零成本通过现有索引契约测试**，
把 schema 变更留到真正有查询压力时再做，并且届时变更只影响派生层。

## 10. 迁移路径

三步，每步可单独回滚。

1. **只读兼容**。解析器同时认 L1 / L2 / L3；读到 L3 即告警，不改任何现有文件。
   上线后可立即统计现存 Note 里还有多少块依赖哨兵。
2. **批量转换**。提供 `eg migrate sentinel-to-heading`：哨兵区间的首段若已是 H3 标题则只补 `{#id .class}`，
   否则在区间首插入一行 H3 标题（标题文本取原 `name=` 的反 slug 或首句）。转换前后必须跑一遍 §11 断言 2（字节保真）。
3. **收口**。移除 L3 写入路径，只保留读取兼容与告警。`skill/SKILL.md` 在本步同时改为只描述 L1 / L2。

## 11. 验收标准（七条，全部进 CI）

约束"上线后再换存储格式是最不希望出现的结果"的落地方式：**选对无法被证明，能退出可以被断言。**

| # | 断言 | 怎么测 | 守住什么 |
| --- | --- | --- | --- |
| 1 | 改正文不改边界 | 对每篇 Note 随机做 N 次正文扰动（段落内改字、增删段落、段落重排），不动标题行与围栏行；重新解析后候选块的逻辑名集合与"逻辑名 → 正文内容"映射保持一致 | 否决项 2 本身，全部结论的地基 |
| 2 | 物化字节保真 | 卡片正文字节与源 Note 该小节正文字节逐字节相等（剥去标题行、frontmatter 与首尾空行后比对） | `internal/mdfile` 保字节契约不被物化破坏 |
| 3 | 物化幂等 | 同一逻辑名重复执行 `materialize_card`：不产生第二个文件、目标文件哈希不变、源 Note 不再被改动 | 逻辑名 → 文件名的一对一映射 |
| 4 | 索引可重建 | 删除全部派生产物（`.index/` 与 sidecar）后 `Rebuild`，`cards` 与 `relations` 与删除前逐行等价 | 派生层是可丢弃产物，隔离带成立 |
| 5 | 降级不丢正文 | 含 L2 围栏的 Note 过一遍 CommonMark 渲染器，断言全部正文段落出现在输出中（围栏行原样显示为文字是已知且允许的，丢正文不允许） | 可视化可达性的底线 |
| 6 | **语法可剥离（逃生舱）** | `eg export --plain` 把整个 vault 导出为不含任何容器语法的纯 CommonMark（剥 `:::` 行、把 `{#id .class}` 移入 frontmatter 或 sidecar），断言剥离前后正文字节集合完全相同 | 随时能退出这套语法而不丢内容 |
| 7 | 崩溃两态 | 在两阶段落盘的每个阶段注入失败，断言仓库停在"完全未物化"或"完全已物化"，不存在半成品或孤儿临时文件 | 跨文件原子性缺口的兜底 |

断言 6 之所以能成立，恰恰因为选的是标题小节：**摘掉 `{#id}` 之后，剩下的还是一篇完整的普通 Markdown。**

## 12. 已知代价与前提

**三条已知代价，均无法用语法消除，只能用契约 + 断言约束：**

1. **"不删标题、不改层级"成为新的隐性契约**。使用者不必维护边界标记，但删掉一个 H3 标题、
   或把 H3 改成 H2，边界仍会变化。缓解手段是派生层保留 exact + 前后上下文的尽力而为提示，
   加上断言 1 把"什么算正文编辑"写死。
2. **L2 围栏在部分渲染器里显示为字面文字**。Obsidian 官方支持清单未列出 `:::` 与 `{#id}`，
   本轮未实操核验其实际渲染。断言 5 正是为守住这个缺口而设（可以丑，不能丢）。
3. **候选粒度限制在"一段到若干段连续内容"**。这与一个小节的自然粒度匹配。

**三条成立前提，缺任何一条都要重新评估本方案：**

1. **权威存储必须是人手可编辑、Git 可评审的纯文本。** 若放松为"编辑只通过自建前端进行"，
   块原生运行时（SQLite 权威）的全部红灯转绿，Trilium 那份"为什么不用 flat files"的六条判据
   会从反方论据变成正方论据。
2. **候选粒度是段级。** 若需求变成"圈出一句话"或"圈出跨越多个小节的内容"，L2 使用比例会显著上升，
   届时应重新比较围栏容器与文件即块。
3. **单写者。** 全部结论建立在"一次只有一个进程写"之上，`run.lock` 是这条前提的实现。
   一旦引入多端并发编辑，Peritext 那组并发标记交错的反例开始生效，选型需从 CRDT 一侧重做。

## 13. 参考

- Pandoc Manual — `header_attributes`、`fenced_divs`、`--section-divs`：<https://pandoc.org/MANUAL.html>
- CommonMark Spec（标题本身不是容器块，故"小节即块"是项目约定）：<https://spec.commonmark.org/>
- Org Mode Syntax（section 边界由标题层级确定）：<https://orgmode.org/worg/org-syntax.html>
- Org Mode Manual — Refile and Archive（子树确定性搬运 + 搬运即补元数据）：<https://orgmode.org/manual/Refile-and-Copy.html>
- W3C Web Annotation Data Model（字符偏移选择器 very brittle）：<https://www.w3.org/TR/annotation-model/>
- Hypothesis fuzzy anchoring（工业实现需模糊重锚）：<https://web.hypothes.is/blog/fuzzy-anchoring/>
- Yjs RelativePosition（CRDT 锚点依赖 `Y.Doc`）：<https://docs.yjs.dev/api/relative-positions>
- TiddlyWiki CompoundTiddlers（正文分隔符反噬普通编辑的先例）：<https://tiddlywiki.com/static/CompoundTiddlers.html>
- CriticMarkup（正文内标记与宿主语法耦合）：<http://criticmarkup.com/>
- org-roam Manual（权威纯文本 + 辅助数据库的分工承诺）：<https://www.orgroam.com/manual.html>
- Obsidian Embeds（`![[Note#Heading]]` 按标题嵌入）：<https://help.obsidian.md/embeds>
- AsciiDoctor Open Blocks（禁止嵌套的先例）：<https://docs.asciidoctor.org/asciidoc/latest/blocks/open-blocks/>
- Trilium FAQ（"为什么不用 flat files"的六条判据，本项目的反方论据）：<https://docs.triliumnotes.org/user-guide/faq>
- Git blame（文件级重命名追溯）：<https://git-scm.com/docs/git-blame>
