# SKILL.md — Evergreen S1 Agent 操作规程

> 本文件随 `eg` 二进制内嵌发布（源：`evergreen/skill/SKILL.md`），由 `eg init` **逐字**写入 vault 根并纳入 Git，
> 保证「工具与规程同版本」（EG-AGT-05）。vault 根的副本与二进制内嵌副本**字节相同**，请勿手工改写。
>
> **读者是 coding agent，不是终端用户。** `eg` 只保证「不写坏数据」；「写得对不对」由本规程约束。
> 与实现冲突时，以两份冻结合同为准：`2026-09-01-eg-cli-contract.md`（命令 / 退出码 / 信封）与
> `2026-09-08-changeplan-contract.md`（ChangePlan 字段 / 校验分级）。

## 1. 硬边界（先读，四条）

1. `eg` 是**确定性本地 CLI**：**不调用任何模型、不做任何网络请求、不抓取网页**。抓取、清洗正文、
   一切语义判断都在 Agent 侧。
2. **S1 主链路无确认交互**：`init` / `capture` / `context` / `apply` / 三条只读查询 / `report --last`
   直接执行，不做二次确认。**例外只有两条**（S2/M3 的用户闸门，见 §8.4）：`eg proposal approve`
   与 `eg delete` 有确认门，校验已过但缺 `--confirm` 时退 `6`，权威 Markdown 完全不变。
3. **CLI 绝不自选默认领域**：未配置 `default_domain` 时，除 `init` / `config` 外一律提示配置并退 `1`。
4. **`eg apply --plan` 是唯一程序化写入通道**：任何绕过它的文件改动都是违规（见 §5 B-01）。

S1 命令恰九个：`init`、`config get|set`、`capture`、`context`、`apply`、`search`、`card show`、`rel`、
`report --last`。**M2 起九个命令全部真实可用**：

| 命令 | M2 状态 | 副作用 |
|-|-|-|
| `eg init`、`eg config get`、`eg config set`、`eg capture`、`eg context`、`eg apply`、`eg report --last` | 可用 | 见 §2（`init` / `capture` / `config set` / `apply` 各产生恰一次 commit；`context` / `report --last` 只读） |
| `eg search <query> [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--json]` | 可用（只读查询） | 零文件变化、零 commit |
| `eg card show <k-id> [--json]` | 可用（只读查询） | 零文件变化、零 commit |
| `eg rel <k-id> [--to <id>] [--json]` | 可用（只读查询） | 零文件变化、零 commit |
| `eg rel add <from> <type> <to> --reason <text> [--domain <d>] [--json]` | 可用（写入） | 走与 `eg apply` **同一条** ChangePlan 链路，产生恰一次 commit（verb = `relate`） |
| `eg rel remove <from> <type> <to> --reason <text> [--json]` | 可用（写入） | 走与 `eg apply` **同一条** ChangePlan 链路，产生恰一次 commit（verb = `relate`） |

`eg rel remove` 自 M3 起是真实写入（owner 裁决 A-24）：命中即从**起点卡** frontmatter 的
`relations[]` 里**物理移除**规范化 `(from, type, target)` 匹配的**全部**记录，不留墓碑、不新建标识；
`opposing` 先按两端 ID 字典序归一再匹配（删除与方向无关）。未命中任何既有关系时是**幂等 no-op**：
记一条 `W10` warning，零写入、零 commit、退 `0`（可放心重跑同一 plan）。
逻辑删除一张卡**不会**级联删除指向它的关系记录——那是另一回事（关系记录不因删卡而消失）。

## 2. S1 调用顺序（0–5 步，逐步写明输入 / 输出 / 失败处置）

```text
第 0 步 Agent 自行抓取并清洗正文（CLI 不做网络请求）
  → ① eg capture   收录原文 + 登记收件区
  → ② eg context   取候选卡与 base（id → content_hash）
  → ③ 语义处理     唯一允许调用模型的环节：材料笔记 / 知识卡 / 关系判断
  → ④ eg apply --plan   唯一程序化写入通道
  → ⑤ 渲染最终报告  把 apply 返回的 JSON 转成人类可读结论
```

### 2.0 第 0 步：Agent 自行抓取并清洗正文（CLI 不做网络请求）

- **输入**：用户给出的 URL 或已有正文。
- **输出**：纯文本正文（已去掉导航 / 广告 / 评论区）、标题、URL。
- **失败处置（首行分支）**：**抓取失败 / 正文为空 → 不调 `eg capture`，直接在报告写明原因与 URL，不落任何文件**
  ——不产生原文、不产生收件区条目、不产生 commit。**不要**用占位文本、摘要或搜索结果冒充正文。

### 2.1 ① `eg capture`（收录原文）

```
eg capture --url <url> --title <title> (--body-stdin | --body-file <path>) --reason <text>
           [--domain <d>] [--tag <t>]... [--captured-at <rfc3339>] [--reprocess] [--json]
```

- **输入**：`--url` 与 `--title` **至少一个**（判重键）；正文二选一（`--body-stdin` / `--body-file`）；`--reason` 必填。
- **输出**（`data`）：`source_id` / `path` / `deduped` / `has_note` / `note_id` / `commit` / `warnings[]`；commit verb = `capture`。
- **`deduped: true`**：URL 规范化或标题精确命中已有原文 → **复用已有原文**，正文不覆盖，`--reason` 追加到理由列表；
  不要为同一篇再造一个 `s-` ID。
- **`has_note: true`**：该原文已有材料笔记 → **默认跳过加工**，不要重复产出笔记；确需重新加工才带 `--reprocess`，
  且必须逐字保留用户块（见 §5 B-06）。
- **失败处置**：退 `2`（正文为空 / 过短，或 `--url` 与 `--title` 同时缺失）→ 回第 0 步补正文或补判重键，**零写入**；
  退 `1` → 参数写错，改命令行；退 `4` → 见 §2.6。

### 2.2 ② `eg context`（只读取上下文）

```
eg context (--source <s-id> | --note <n-id>) [--domain <d>] [--json]
```

- **输入**：`--source` 与 `--note` 二选一。
- **输出**（`data`）：`source`（含正文字节）、`cards` / `notes` / `candidates`（候选卡白名单）、
  **`base`（`id 或 vault 内相对路径 → content_hash`）**、`domain` / `default_domain` / `default_domain_fallback`。
- **只读**：零文件变化、零 commit。可安全重跑。
- **失败处置**：退 `2`（对象不存在或 frontmatter 不可解析）→ 核对 ID；退 `1`（未配置 `default_domain`）→
  先 `eg config set default_domain <d>`，**不要**自行猜一个领域。

### 2.3 ③ 语义处理（唯一允许调用模型的环节）

在这一步产出：材料笔记四类内容、知识卡（新建 / 复用 / 追加）、材料关系与论证关系、存疑条目、
`convergence[]` 逐卡判定、`coverage_gaps` 自评。判据见 §3，落成 ChangePlan 的规则见 §4。
**这一步不碰磁盘**：不要用编辑器 / shell 直接写 vault 里的任何文件。

**判断收敛前可用三条只读查询命令复核**（M2 起全部真实可用；**零文件变化、零 commit，可任意次调用**，
不需要也不允许把它们的结论当成写入）：

```
eg search <query> [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--json]
eg card show <k-id> [--include-deprecated] [--json]
eg rel <k-id> [--to <id>] [--include-deprecated] [--json]
```

- `eg search`：`eg context` 的候选卡只覆盖同领域相似卡；怀疑漏了别的说法时按关键词再搜一遍，
  确认「是不是已经有一张卡在讲同一件事」。失效卡也会出现在结果里并带 `[失效]` 标记。
- `eg card show`：拿到候选卡的**五分区全文** + `sources[]` + 正反向关系，用来逐维度比对
  §3.1 的三维度（只看标题下判断 = 判错）。
- `eg rel`：查这张卡**已经**有哪些论证关系（正向 `relations[]` + 反向全库扫描），
  避免把已存在的关系再写一遍（同对重复会记 W8），也便于判 `core_change` / `conflict_coexist`。
- **`--include-deprecated`（M4 起，`eg rel` / `eg card show` 专属只读 flag）**：默认视图**隐藏对端
  `deprecated` 的关系条目**（口径见 §9.3）；加此 flag 才把失效对端带回来并保留 `[失效]` 标记。
  它**只放开 `deprecated` 维度，不影响逻辑删除维度**（已删除端点任何 flag 下都不出现），与 `--to` 正交。
  `eg search` / `eg context` 等命令**不接受**该 flag，传入即退 `1`。
- 三条命令都**不会**修改 vault：不产生 commit、不改任何文件，因此判不准就多查几次，不要靠猜。
- 需要**新增**一条论证关系且不涉及卡内容改动时，可直接用
  `eg rel add <from> <type> <to> --reason <text> [--domain <d>] [--json]`（写路径，产生一次
  `relate` commit）；涉及卡正文的任何改动仍然只能走 `eg apply --plan`。

### 2.4 ④ `eg apply --plan`（唯一程序化写入通道）

```
eg apply --plan <file|-> [--dry-run] [--json]
```

- **输入**：一份 ChangePlan（`-` 表示 stdin）。字段见 §4。
- **输出**：`data.report`（§4.6 报告体）+ `data.domain` + `data.convergence`；`--dry-run` 时额外给 `planned[]`
  （`path` / `section` / `op_index`）且 `git.commit` 为空。
- **`--dry-run`**：完整走 E1–E6 校验与 op 展开，输出**将**写入的文件与分区清单，**零写入、零 commit**；
  校验失败仍退 `2`，展开成功退 `0`。**建议每份新 plan 先 `--dry-run` 一遍再正式 apply。**
- **失败处置**：见 §2.6。

### 2.5 ⑤ 渲染最终报告

把 `eg apply` 返回的 JSON（或 `eg report --last`）渲染成人类可读结论：写了什么、跳过了什么、为什么跳过、
commit 是哪一个、有哪些 warning。**如实转述，不美化、不合并、不省略 `skipped[]`。**
`eg report --last` 只读复现最近一次**产出 §4.6 报告体的写命令**的报告（S1 主链路上是 `eg apply` 与 `eg capture`；M3 / M4 起另有状态类与对账写命令，覆盖集合见 §8 与 CLI 合同 §1.9 脚注），零写入零 commit，可随时重放；回放的第一行会交代这条记录来自哪条命令（如 `eg capture`）。
**只做素材入库时也可用**：一次成功的 `eg capture` 之后直接 `eg report --last` 即可复现该次收录报告，不需要先跑 `eg apply`（I-…-018）。不产出 §4.6 报告体的写命令（关系写入、编辑、索引维护等）**不刷新**这份记录。

### 2.6 退出码处置指引（S1 恰五值；M3 起另有 `6`，见 §8.4；**M6 起另有 `5`**，见 §11.1）

| 退出码 | 含义 | Agent 必须怎么做 |
|-|-|-|
| `0` | 成功 | 继续下一步；仍要读 `warnings[]` 并在报告里如实转述 |
| — | （**任何**退出码，含失败态） | `warnings[]` 恒是 warning / info 级诊断的唯一去处，`data.errors[]` 只装 error 级；失败**不代表**没有 warning（典型：退 `5` 时 `W28` 在 `warnings[]`、`E16` 在 `data.errors[]`），见 §12.5 |
| `1` | 用法 / 参数非法（零写入） | 读 `data.errors[]` 的 `code`（命令层 `E17`；见 §12）后**改命令行参数**重试；不要改 plan 内容来「绕」 |
| `2` | 校验失败（仅 error 级，零写入） | 读 `data.errors[]` 的 `code`（op 级 `E1`–`E6`；命令级 `E17`–`E21`，见 §12）+ `op_index` + `path`，**改 plan 后重投**；磁盘未变，不需要清理 |
| `3` | 部分写入被跳过 | 已写入部分**已提交**。读报告 `skipped[]`：`kind=file_changed` / `cause=content_hash_mismatch` 表示该文件自 `eg context` 读取以来变了 → **重新 `eg context` 取新 `content_hash`，只为被跳过的部分重投一份新 plan**；不得强写、不得重试同一份 plan |
| `4` | Git 提交失败 | **磁盘保留现状，不做破坏性还原。** 在报告写明「文件已写入但提交失败」并把未提交清单交给用户；**不得**自行 `git commit` / `git checkout` / `git reset` / 删文件 |
| `5` | 写前强校验失败（`E15`）或锁不可用（`E16`）（**M6·T-074 启用**，`ExitPrecheckOrLock`；见 §11.1） | **权威 Markdown 零改动、零 commit。** `E15`：写前强校验 / 恢复屏障 / B3 前像不匹配失败 → 按 `data.errors[]` 修 plan 或先跑 `eg check --strict` 定位后重投；`E16`：`run.lock` 等待超时 → 稍后重试或排查是否有并发写者持锁；**不得**绕过校验、不得删 `run.lock` / `.index/txn/` 强写 |
| `6` | 校验已通过、仅缺用户确认（M3；仅 `eg delete` 与 `eg proposal approve` 两条命令，见 §8.4） | **不要替用户补 `--confirm`**：把「校验已过、只差确认」这个事实与待执行命令原样交给用户，由**用户自己**重跑；本次零写入、零 commit、权威 Markdown 完全不变 |

## 3. 判据（可执行清单，逐条勾选）

### 3.1 收敛三维度（每张被比较的候选卡都要过一遍）

- [ ] **核心知识**（`core_knowledge`）是否相同？`same` / `different`
- [ ] **成立条件**（`conditions`）是否相同？`same` / `different`
- [ ] **独立复用用途**（`reuse_purpose`）是否相同？`same` / `different`
- [ ] **任一维度 `different` → 拆两张卡**（宁拆勿并）
- [ ] **判不出（拿不准 / 信息不足）也拆两张卡**，并在 `convergence[].note` 写明为什么判不出
- [ ] 三维度**全 `same`** → 只能填 `same_semantics`（复用同一张卡，不新建）
- [ ] 复用已有卡做**非核心补充**时，被补充的那个维度就是 `different`（例如补了一条成立条件
      → `conditions: "different"`）：`relation` 只有 `same_semantics` 允许三维度全 `same`，
      其余六值必须**至少一个维度 `different`**，否则记 W5
- [ ] 每张被比较的候选卡在 `convergence[]` 里**各写一条**，含 `card` + `relation` + 三维度结论 + `note`

### 3.2 七种处理关系 → 产物结果对照表（恰 7 行，判完三维度按表选 op，不要自由发挥）

| `convergence[].relation` | 处理关系 | 应产出的 op 组合 |
|-|-|-|
| `independent_new` | 独立新增 | `create_card`（新卡直接 `active`）+ `add_material_rel` |
| `same_semantics` | 语义相同 | **不新建卡**；只 `add_material_rel` 把新材料挂到已有卡 |
| `non_core_supplement` | 非核心补充 | `append_card` 到「解释与依据」/「条件与边界」（**不动「知识内容」**）+ `add_material_rel` |
| `core_change` | 核心变化 | `create_card` 新卡 + `add_relation`（`limits` / `derives` 等）指向原卡；**原卡不改写、不失效** |
| `conflict_coexist` | 冲突并存 | 两卡同时 `active` + **恰一条** `add_relation`（`type: opposing`，方向由 CLI 规范化） |
| `uncertain` | 存疑 | `add_open_question` 写入材料笔记「存疑与待验证」分区；不建卡 |
| `deprecated` | 失效 | **S1/M1 不可用**：只由用户提出（S2），Agent **不得**生成任何状态类 op |

`relation` 必须与三维度结论自洽：**三维度全 `same` ⟺ `same_semantics`**，其余六值必须至少一个维度
`different`（CLI 的一致性判据逐字如此）。
不自洽或缺失只会得到 **W5 warning**（不拦截），但**属于规程违规**，不得当作默认写法。

### 3.3 知识卡粒度、材料笔记、关系、自检

- [ ] **一卡 = 一个可独立理解 / 引用 / 复用的知识单元**；一张卡只承载一个知识点。
- [ ] **材料笔记忠于原文、不夹带评价**；Agent 自己的判断只放「Agent 分析」分区。
- [ ] **材料关系四要素齐全**：`source` + `note` + `rel`（`support` / `against` / `context`）+ `reason`；
      **论证关系四要素齐全**：`from` + `type`（`derives` / `supports` / `limits` / `opposing`）+ `target` + `reason`。
- [ ] `reason` 必须有实质内容，**不许写成关系名本身**（否则 W2 warning）。
- [ ] **理解自检只写开放式问题**，不预设成立方；**不得引入掌握度、评分、复习排程一类字段**；笔记**不生成**理解自检。
- [ ] **自检回答不自动成为知识**：回答只落在知识卡「理解自检」分区，要不要转成卡**由用户自行决定**，
      Agent 不得把回答直接建成新卡或追加进「知识内容」。

### 3.4 提炼覆盖自评 `coverage_gaps`（EG-EXT-02）

生成 `write_note` 之前，按七类要点逐项自评本篇原文是否表达过；**原文未表达的要点写进 `coverage_gaps`**，
使「覆盖项缺失」在最终报告中可见。取值是**受控枚举七值**，逐字如下：

| 枚举值 | 覆盖要点 |
|-|-|
| `core_claim` | 核心论点 |
| `key_evidence` | 关键证据 |
| `counterexample` | 反例 |
| `boundary` | 条件与边界 |
| `method` | 方法 |
| `conclusion` | 结论 |
| `limitation` | 局限与不确定性 |

- [ ] 逐项自评七类要点，**不得编造原文未出现的要点**来填满七项（与「不虚构材料来源与依据」同一条底线）。
- [ ] `coverage_gaps` **只登记原文没有**的要点，**不得**用来掩盖 Agent 自己漏写的要点。
- [ ] **无缺失时不写该字段**（缺字段或空数组 = 无缺失，CLI 不会自行推断、不会补全）。
- [ ] 取值不在受控枚举内 → I1 info，原样透传进报告，不拦截；但仍属规程违规。

### 3.5 收敛结论怎么被呈现（M2 起四个出口同源同事实）

`convergence[]` 写进 plan 之后，CLI **原样回带**、不重新判断、不重排、不去重、不合并。它出现在
四个出口上，四处内容同源（同一份数组 + 同一个渲染函数）：

| 出口 | 位置 | 形态 |
|-|-|-|
| `eg apply --json` | `data.convergence[]`，每条恰 6 键：`card` / `relation` / `core_knowledge` / `conditions` / `reuse_purpose` / `note` | 结构化 |
| `eg apply`（文本） | 报告体各行**之后**的「逐卡收敛记录」行块 | 人类可读 |
| `eg report --last --json` | `data.convergence[]`（回放落盘字节，与该次 apply 逐字相同） | 结构化 |
| `eg report --last`（文本） | 同一个「逐卡收敛记录」行块，与该次 apply 的输出**逐字相等** | 人类可读 |

文本形态每张卡恰一段、每段恰 5 行（`<card> → <relation>` + 三维度 + 说明）；**字段缺失处打「未给出」**，
CLI 不补算、不推断。数组为空或不涉及已有卡时该行块整段不出现（另见 W5：涉及已有卡却不给条目 = 规程违规）。

- [ ] 最终报告（§2.5）必须**如实转述**逐卡收敛记录：卡 ID、处理关系、三维度结论、`note` 一一照搬。
- [ ] **不得改写、不得合并同类项、不得省略「未给出」**，也不得把 CLI 没说的收敛理由补进报告。
- [ ] 想复述「为什么这么收敛」就把 `note` 原文引出来；`note` 是空的就写「未给出」，不要临时编一个理由。

## 4. ChangePlan 填写规则

顶层恰 8 键：`plan_version`(=1) / `verb` / `domain` / `reason` / `requirement_ids` / `convergence[]` /
`base` / `ops[]`。S1 的 op 恰七个：`add_source`、`write_note`、`create_card`、`append_card`、
`add_material_rel`、`add_relation`、`add_open_question`（未知 op → **E5**，退 `2` 零写入）。

- **`verb`**：主链路用 `process`；重新加工用 `reprocess`。未知值 → warning 并退化为 `process`。
  （另有三个 commit verb 由 CLI 自己产生：`init`（`eg init`）、`capture`（`eg capture`）、以及 commit verb `reconcile`（`eg config set`）——commit verb `reconcile` **≠ S3 `eg reconcile` 命令**，后者属 S3、**S1 不可用**，`eg --help` 里也不出现。）
- **`domain`**：**一份 plan 只写一个领域**。无法判断领域时交给 `default_domain` 并在报告说明，**不要追问用户**、
  更不要自选。写入目标落在 `plan.domain` 之外 → W1 warning（照写，但属规程违规）。
- **`base`**：**`eg context` 返回的 `content_hash` 必须原样填入 `plan.base`**，不得省略、不得自己算、不得改写。
  省略某个被改文件 → **W6 warning + 跳过该文件**（该处内容根本不会落盘）；与磁盘不一致 → 按 B3 跳过该文件并进
  `skipped[]`（`kind=file_changed`、`cause=content_hash_mismatch`）。新建文件可以不给 `base`。
- **`convergence[]`**：涉及已有卡时每张被比较的候选卡写一条（§3.1）。
- **`sections` 的分区名逐字固定**：知识卡「知识内容」「解释与依据」「条件与边界」「用户补充」「理解自检」；
  材料笔记「材料提炼」「Agent 分析」「用户补充」「存疑与待验证」「产出知识卡」。
- **诊断读法**：每条 error / warning / info 都带 `code` + `level` + `path` + `op_index` + `message` + `target`，
  按 `op_index` 与 `path` 定位后改 plan 重投即可。

## 5. 边界与禁止项（B-01 ~ B-13，逐条硬约束）

- **B-01 不得绕过 `eg apply` 直接改文件**：不得用编辑器 / shell / 脚本改写 vault 内任何 Markdown；
  也不得自行 `git commit` / `git checkout` / `git reset` / 删除文件。
- **B-02 一份 plan 不得写两个 domain**：一次加工 = 一个领域；跨域目标 → W1 warning，属规程违规。
- **B-03 对已有卡的自动加工只追加三分区**：「解释与依据」「条件与边界」「理解自检」；
  **不改「知识内容」**（自动路径写它即 E6）；**「用户补充」任何时候永不写**（E6）。
- **B-04 新建卡（`create_card`）五分区都可写**，但「知识内容」必写、「用户补充」仍然不可写。
- **B-05 禁止替换或删除已有普通块**：S1 只追加；块替换属 S2，`replace_block` 在 S1 是未知 op（E5）。
- **B-06 重新加工必须逐字保留用户块**：`write_note` 带 `reprocess: true` 时，「用户补充」与「存疑与待验证」
  的既有内容原样保留（含空行与缩进）；无法逐字保留时 CLI 会跳过并记 `user_block_unsafe`。
- **B-07 禁止生成状态类 op**：卡建出来就是 `active`，Agent 不得改 `status`、不得写删除标记。
  自 S2 起这不再只是规程约束：状态类 op（`deprecate` / `restore` / `set_replaced_by` / `delete` /
  `undelete`）缺 `initiator: user` 在 S2 是 **error**（W7 升级，见 §8.2），生成即退 `2` 零写入。
- **B-08 禁止把 `active` 说成「已确认」「已入库」**：`active` 只表示**当前有效**，不代表被人确认过。
- **B-09 禁止读改其他领域的产物**：只在 `plan.domain` 指定的领域内读写。
- **B-10 禁止把多跳推导结论沉淀成新卡**：多跳结论没有自己的材料依据，落盘会绕过材料依据强制；
  这类结论写进材料笔记「Agent 分析」或存疑，不建卡。
- **B-11 无法判断领域时落 `default_domain` 且不追问**：在报告里说明这次用了 `default_domain_fallback`。
- **B-12 不确定是否同一知识单元就拆两张卡**，并**逐卡记录三维度比较结论**到 `convergence[]`。
- **B-13 不虚构材料来源与依据**：文章没表达的部分**留空**，并按 §3.4 登记 `coverage_gaps`；
  不得编造 `source` / `note` / `reason`，不得引用不存在的 ID。

**不得复活的已废弃设计**（出现即 W4，字段被原样忽略；写进 plan 属规程违规）：`candidate` 字段、
独立未决问题实体、`open/` 目录、`source_check`、观点倾向 / `lean` 字段、产物 frontmatter **顶层**的
`domain` / `type` 冗余字段、物理（永久）删除、迁移提案、跨领域能力。

**不得把 `s-` 写进 `relations`、不得把 `k-` 写进 `sources`**：两者都是 E3，代码级硬拦，退 `2` 零写入。

**不得承诺尚未落地的能力**（以下在 **S1 主链路不可用**，加工 Agent 既不得调用，也不得在报告里承诺）：块替换（`replace_block` 属 S2，S1 仍是 E5）。**M6（S5）已落地**多文件强原子事务、`run.lock` 文件锁、`.index/txn/` 事务日志、崩溃恢复、块级安全合并与写前强校验（见 §11），如实按 §11 转述、不再声称「未落地」。S3 对账面的 `eg reconcile` / `eg check` 两条命令**已在 M4 落地并可用**，但它们是**运维/对账命令、不属加工主链路**——完整口径见 §9，加工主链路（capture→apply）不需要在每次写入前先跑对账（`eg check --strict` 的写前强校验是 M6 在写命令内部自动执行的前置，不需要 Agent 手动先跑）。S4 的派生索引 `eg index`（**M5** 落地，见 §10）同理：它**不属加工主链路**、**不是任何命令的前置**；M5 收口后 `eg search` / `eg card show` / `eg rel` 三条读路径**已接到索引后端**，但索引缺失 / 陈旧 / 损坏时一律**降级为全量 Markdown 扫描**（留痕 `W22` / `W23` / `W24` + `Q5`），结果恒以 Markdown 为准 —— 因此「索引没建」永远不是读失败的理由。

**M5（S4）已落地的派生索引能力**：`eg index` 的**恰四个子命令** `build` / `rebuild` / `status` / `sync`，三条读路径接入索引并可降级，`--limit` / `--offset` 分页（默认 `--limit 50`，截断留痕 `W25`），`replaced_by` 正反向查询，以及只读性能采样 `eg bench`（五键）——规程见 §10。**Markdown 是唯一权威来源**，`.index/` 只是可随时删除、可完整重建的派生物（整目录在 `.gitignore` 内、不入 Git、不随仓库分发）；**索引不是写命令、也不是读命令的前置**。

**M4（S3）已落地的对账与可见性能力**：全库对账 `eg reconcile`、只读结构体检 `eg check`、失效对端默认隐藏与 `--include-deprecated` 显式展示——规程见 §9。**`eg reconcile` / `eg check` 命令本身不是写命令的手动前置**：加工主链路不需要在每次写入前先跑对账。（M6 起，A 类写命令**内部**已自动执行写前强校验，复核不过退 `5` + `E15`，见 §11.6——这是写命令内建的闸门，仍不需要 Agent 手动先跑 `eg check`。）

**M3（S2）已落地但受授权约束的能力**：提案（`proposal`，Agent 只能 `new`）、失效与状态变更（`deprecate` / `restore` / `replaced-by`）、逻辑删除与恢复（`delete` / `undelete`）、核心分区编辑（`edit`）——它们**都不是** Agent 可以自行发起的写入，规程见 §8。

## 6. 两份完整 ChangePlan 样例

两份样例都能被 `eg apply --dry-run` 接受（退 `0`、零写入）。**`base` 里的 `content_hash` 是占位值，
正式 apply 前必须用 `eg context` 输出的同名键值逐字替换。**

### 6.1 样例 ①：新建卡 + 材料关系（`independent_new`，带 `coverage_gaps`）

> 样例 ① 同时给出 `create_card.sources[]` 与 `add_material_rel`（§3.2 `independent_new` 行的 op 组合）：
> 新卡的材料关系随建卡一并落盘，随后那条**四要素逐字相同**的 `add_material_rel` 会被 CLI 幂等去重，
> 卡上不会出现第二条。四要素只要有一个字不同，就是另一条关系，会照写。

<!-- e2e-sample: 1 -->
```json
{
  "plan_version": 1,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "把《The Bitter Lesson》沉淀成一张可独立复用的知识卡",
  "requirement_ids": ["EG-KNW-04", "EG-CVG-01", "EG-EXT-02"],
  "convergence": [],
  "base": {
    "unprocessed.md": "sha256:00000000000000000000000000000000000000000000000000000000placeholder"
  },
  "ops": [
    {
      "op": "write_note",
      "source": "s-20260917-the-bitter-lesson",
      "note_id": "n-20260917-the-bitter-lesson",
      "title": "The Bitter Lesson（Rich Sutton, 2019）",
      "sections": {
        "材料提炼": "- 原文主张：70 年 AI 研究最大的教训是，能利用算力的通用方法长期看最有效，且优势很大。\n- 原文依据：算力成本持续指数下降（摩尔定律的推广），因此搜索与学习这两类可随算力扩展的通用方法最终胜出。\n- 原文举例：国际象棋、围棋、语音识别、计算机视觉四个领域都出现过「人工注入知识短期领先、算力驱动的通用方法最终反超」。\n- 原文结论：应把人类已有认知当作待发现对象，而不是直接写进系统；系统要能自己发现。\n",
        "Agent 分析": "- 该主张的适用面：以「有明确评估信号、可大规模搜索或学习」的任务为主；原文未讨论数据受限或评估信号缺失的场景。\n- 与工程实践的接口：它约束的是长期技术路线选择，不是单次交付的取舍。\n"
      },
      "output_cards": [{ "card": "k-20260917-bitter-lesson", "mode": "新建" }],
      "coverage_gaps": ["counterexample"]
    },
    {
      "op": "create_card",
      "card_id": "k-20260917-bitter-lesson",
      "title": "能利用算力的通用方法长期胜过人工注入知识",
      "tags": ["ai", "method"],
      "sources": [
        {
          "source": "s-20260917-the-bitter-lesson",
          "note": "n-20260917-the-bitter-lesson",
          "rel": "support",
          "reason": "原文用国际象棋 / 围棋 / 语音 / 视觉四个领域的历史给出该结论的直接依据"
        }
      ],
      "sections": {
        "知识内容": "在算力成本持续指数下降的前提下，依赖搜索与学习、能随算力扩展的通用方法，长期表现优于把人类领域知识直接写进系统的方法。\n",
        "解释与依据": "- 依据原文：算力可用量随时间指数增长，方法的可扩展性决定长期上限。\n- 依据原文的四个历史案例：人工注入知识的方案短期领先，随算力增长被通用方法反超。\n",
        "条件与边界": "- 前提是算力可持续增长、任务具备可大规模搜索或学习的结构。\n- 原文讨论的是长期趋势，不否认短期内人工知识有效。\n",
        "理解自检": "- 如果算力成本停止下降，这个结论还成立吗？\n- 「通用方法」与「无先验」是同一件事吗？\n"
      }
    },
    {
      "op": "add_material_rel",
      "card": "k-20260917-bitter-lesson",
      "source": "s-20260917-the-bitter-lesson",
      "note": "n-20260917-the-bitter-lesson",
      "rel": "support",
      "reason": "原文用国际象棋 / 围棋 / 语音 / 视觉四个领域的历史给出该结论的直接依据"
    },
    {
      "op": "add_open_question",
      "note": "n-20260917-the-bitter-lesson",
      "question": "在数据或评估信号受限的任务上，这个结论是否仍然成立？"
    }
  ]
}
```

### 6.2 样例 ②：复用已有卡 `append_card` 三分区 + 论证关系（`non_core_supplement`，不带 `coverage_gaps`）

<!-- e2e-sample: 2 -->
```json
{
  "plan_version": 1,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "第二篇材料补充「通用方法为何能扩展」的前提，按非核心补充追加到已有卡",
  "requirement_ids": ["EG-CVG-01", "EG-KNW-04"],
  "convergence": [
    {
      "card": "k-20260917-bitter-lesson",
      "relation": "non_core_supplement",
      "core_knowledge": "same",
      "conditions": "different",
      "reuse_purpose": "same",
      "note": "两篇都在讲「可随团队扩展的通用方法」，核心知识与独立复用用途相同；第二篇补了一条成立条件（知识必须携带上下文、边界与反例），故 conditions=different，按非核心补充追加到已有卡，不拆新卡"
    }
  ],
  "base": {
    "unprocessed.md": "sha256:00000000000000000000000000000000000000000000000000000000placeholder",
    "domains/ai-infra/knowledge/k-20260917-bitter-lesson.md": "sha256:00000000000000000000000000000000000000000000000000000000placeholder"
  },
  "ops": [
    {
      "op": "write_note",
      "source": "s-20260917-knowledge-compounding",
      "note_id": "n-20260917-knowledge-compounding",
      "title": "Knowledge Compounding in Small Teams（internal onboarding note, 2026）",
      "sections": {
        "材料提炼": "- 原文主张：小团队知识沉淀要优先记录可复用的决策依据，而不是只记录结论。\n- 原文论据：后续成员接手时需要看到上下文、边界和反例，才能在不询问原作者的情况下复用知识。\n- 原文结论：把背景、适用条件与自检问题一起沉淀，知识才能随项目迭代持续复利。\n",
        "Agent 分析": "- 这一条给「通用方法为何能随团队扩展」补了一个前提：知识条目必须携带可复核的上下文与边界。\n"
      },
      "output_cards": [{ "card": "k-20260917-bitter-lesson", "mode": "补充" }]
    },
    {
      "op": "append_card",
      "card": "k-20260917-bitter-lesson",
      "sections": {
        "解释与依据": "- 补充依据（Knowledge Compounding in Small Teams, 2026）：通用方法能在团队中扩展的前提之一是知识条目携带可复核的决策背景，否则接手成本会抵消复用收益。\n",
        "条件与边界": "- 补充边界：该结论在「知识携带上下文、边界与反例」时最稳固；只记录孤立结论不满足该条件。\n",
        "理解自检": "- 如果一条知识只有结论而缺少背景、边界与反例，下一位接手者还能安全复用它吗？\n"
      }
    },
    {
      "op": "add_material_rel",
      "card": "k-20260917-bitter-lesson",
      "source": "s-20260917-knowledge-compounding",
      "note": "n-20260917-knowledge-compounding",
      "rel": "support",
      "reason": "第二篇从「知识沉淀必须携带上下文」的角度为该卡结论提供支持性材料依据"
    }
  ]
}
```

## 7. 一次主链路的自检清单（提交报告前逐条勾）

- [ ] 第 0 步正文真实抓到且已清洗；抓取失败时**没有**调用 `eg capture`。
- [ ] `eg capture` 的 `deduped` / `has_note` 已读，且据此决定是否继续加工。
- [ ] `plan.base` 的每个值都来自本次 `eg context`，逐字未改。
- [ ] 每张候选卡在 `convergence[]` 里各一条，`relation` 取自 §3.2 七值且与三维度自洽。
- [ ] op 组合与 §3.2 表格对得上；没有状态类 op、没有 S2+ op。
- [ ] `coverage_gaps` 只登记原文没有的要点；无缺失时该字段不出现。
- [ ] 逐卡收敛结论已被最终报告如实转述（卡 ID / 处理关系 / 三维度 / `note` 照搬，缺的写「未给出」，见 §3.5）。
- [ ] 报告如实转述 `skipped[]` 与 `warnings[]`，退 `3` / `4` 时按 §2.6 处置，**没有**自行做 Git 操作。

## 8. S2 / M3 增量规程（Agent 必读：能做什么、绝不能做什么）

M3 起 `eg` 的顶层命令是 **18** 个：S1 九命令 + M3 新增的 `deprecate` / `restore` / `replaced-by` /
`proposal`（S2 提案子系统，五子命令）/ `delete` / `undelete` / `mark-reviewed` / `unreviewed` / `edit`。
新增命令**几乎全部属于用户显式路径**，本节写清 Agent 的边界；越界的 plan 会被退 `2`，不是被容忍。
（**M4 起顶层命令是 20 个**：在这 18 条之上新增 S3 对账面的 `reconcile` 与 `check`，规程见 §9；
**M5 起是 22 个**：再新增 S4 的 `index`（派生索引）与 `bench`（性能采样），规程见 §10；
本节的 S2 / M3 口径一字不变。）

**M5 起 22 命令一览**（与 `eg --help` 命令区逐条对应，供文档一致性判据消费）：
`eg init`、`eg config`、`eg capture`、`eg context`、`eg apply`、`eg search`、`eg card show`、
`eg rel`、`eg report --last`、`eg deprecate`、`eg restore`、`eg replaced-by`、`eg proposal`、
`eg delete`、`eg undelete`、`eg mark-reviewed`、`eg unreviewed`、`eg edit`、`eg reconcile`、`eg check`、
`eg index`、`eg bench`。

### 8.1 三个正交维度：改一个绝不碰另两个

| 维度 | 字段 | 唯一写入命令 | 谁能发起 |
|-|-|-|-|
| 生命周期状态 | `status`（`active` / `deprecated`） | `eg deprecate` / `eg restore` | 用户显式 |
| 删除维度 | `deleted_at` + `deleted_reason` | `eg delete` / `eg undelete` | 用户显式（`delete` 另加确认门） |
| 过目维度 | `reviewed_at` | `eg mark-reviewed` | 用户显式（A-15 窄口径例外，见 §8.2） |

- 逻辑删除**绝不改 `status`**；失效**绝不写 `deleted_at`**；过目**只写 `reviewed_at`**，`updated_at` 与内容一格不碰。
- 逻辑删除是**标记**不是抹除：文件不被物理删除，`relations[]` 与 `sources[]` 的记录**一条不删**。
- 渲染上三者各有标记：`[失效]` / `[已删除]` / `[未过目]`。报告里请照搬这些标记，不要自行合并成「已删」。

### 8.2 授权：两条路径 + 反伪造

- **P-A（Agent 自动路径）**：Agent 生成 plan 并 `eg apply`。写权限矩阵里标 🔴 的格子在这条路径上一律拒绝。
- **P-U（用户显式路径）**：用户在命令行发起，且命令行带 `--user-request`。
- **N-1 反伪造**：plan 或提案里写 `initiator: user` **不能自证**授权，必须由命令行的 `--user-request` 佐证；
  只写字段不给佐证 = 仍是 Agent 路径。
- **B3 不因授权放宽**：即使走用户显式路径，写前照样逐文件比对 `content_hash`，不一致就跳过该文件并退 `3`。
- **矩阵第 12 行（🔴）**：「知识内容」分区在 **Agent 自动路径不追加、不改写**；只有 `create_card`
  新建卡时可以写它。自动路径写「知识内容」→ **E6 退 `2` 零写入**（E6 先于 W7 判定）。
  「用户补充」分区**任何路径、任何时候都不写**，`eg edit --section 用户补充` 也必被拒。
- **W7 升级为 error（R-10，自 S2 起）**：状态类 op（`deprecate` / `restore` / `set_replaced_by` /
  `delete` / `undelete`）缺 `initiator=user` 在 S1 只是 warning，**自 S2 起是 error**——
  Agent **不要再生成这类 plan**，生成即退 `2`，零写入。
- **唯一例外**：`mark_reviewed`（A-15 窄口径）仍是 **warning**，不因此拦截；但它属过目维度，
  Agent 也没有理由代替用户「过目」。

### 8.3 提案：可提不可执

- `eg proposal new` **对 Agent 开放**（不要求 `--user-request`）：发现「这份材料该删了」时，
  Agent 的正确动作是**提一份 `logical_delete` 提案**，把影响面摆出来，等用户裁决。
- `eg proposal list` / `eg proposal show` 只读，随便调。
- `eg proposal approve` 是**高风险闸门**：只允许用户显式路径（U-12），Agent 调用直接退 `2` 零写入；
  它还要求 `--confirm`，缺确认且校验已过时退 `6`。
- `eg proposal reject` 必带 `--reason`。
- 十项必备缺项按 **W9** 处理：照常创建 + 进 `warnings[]`，**绝不**因此拒绝创建提案。
- 一句话记法：**可提不可执**——提案是 Agent 表达意见的渠道，执行永远是用户的事。

### 8.4 退出码 `6` 与用户闸门

- `6`（`ExitNeedConfirm`）= **校验已通过、仅缺用户确认**，此时**权威 Markdown 完全不变**：零写入、零 commit。
- 启用白名单**恰两条命令**：`eg proposal approve`、`eg delete`。其它命令**不产生** `6`；`5` 全程不启用。
- 判定顺序锁死：**参数错 `1` → 校验失败 `2` → 缺确认 `6`**。提案还是 `pending` 时缺确认得到的是 `2`。
- `eg delete` 的三件前置必须**同时**齐备：① 用户发起且命令行带 `--user-request`；
  ② `--proposal` 引用一份 `status=approved` 的提案；③ `--confirm`。缺任何一件都不会落盘。
- 报告里的 `support_check[]` 只是**建议**（例如「某卡失去唯一支持材料，建议标记失效」）：
  它**不写盘、不改状态**，Agent 不得据此自行发起状态变更，只能如实转述给用户。
- 拿到 `6` 时 Agent 的动作只有一个：**把待确认的事实和那条命令原样交给用户**，由用户自己重跑。

### 8.5 其它 M3 变化

- `eg rel remove <from> <type> <to>` 已由占位变为**真实命令**（详见 §1）：命中即**物理移除**匹配条目，
  未命中记 **W10**、退 `0`、零写入零 commit。
- `eg search` 新增 `--include-deleted`；**默认视图排除已删除项**。转述检索结果时别把默认视图说成「全库」。
- `eg edit` 的可编辑分区白名单**恰三个**：知识内容 / 解释与依据 / 条件与边界；它是用户命令，
  缺 `--user-request` → 退 `2` 零写入，`content_hash` 过期 → 跳过退 `3`，**不产生** `6`。

### 8.6 可跑示例

下列命令在 `tests/e2e/docs-cli/docs_cli_contract_and_exitcodes.sh` 里被逐条实跑。前置：`$VAULT` 指向一个已 `eg init`
的库，`$CARD` / `$CARD2` 是库里两张已有知识卡的 ID，`$NOTE` 是一份材料笔记的 ID，`eg` 在 `PATH` 上。
行尾 `# expect: N` 表示该条**预期退出码是 N**（没有标注的都是 `0`）。

```bash
eg --vault "$VAULT" card show "$CARD"
eg --vault "$VAULT" mark-reviewed --target "$CARD"
eg --vault "$VAULT" unreviewed --json
eg --vault "$VAULT" edit --target "$CARD" --section 知识内容 --content "并行注意力用位置编码补回顺序信息。" --user-request
eg --vault "$VAULT" edit --target "$CARD" --section 用户补充 --content "任何路径都写不进去" --user-request  # expect: 2
eg --vault "$VAULT" edit --target "$CARD" --section 知识内容 --content "缺命令行佐证就不是用户路径"  # expect: 2
eg --vault "$VAULT" deprecate --target "$CARD" --reason "结论已被更完整的版本取代"
eg --vault "$VAULT" replaced-by --target "$CARD" --to "$CARD2" --reason "新卡覆盖同一问题且更完整"
eg --vault "$VAULT" restore --target "$CARD" --reason "复核后判断原结论仍成立"
eg --vault "$VAULT" rel add "$CARD" supports "$CARD2" --reason "前者为后者提供论证支持"
eg --vault "$VAULT" rel remove "$CARD" supports "$CARD2" --reason "关系判断有误，撤回这一条"
PID=$(eg --vault $VAULT proposal new --type logical_delete --target $NOTE --reason '该材料已被更权威的版本取代' --json | grep -oE 'p-[0-9]{8}-[0-9]{3}' | head -1)
eg --vault "$VAULT" proposal show "$PID"
eg --vault "$VAULT" proposal approve "$PID" --confirm --user-request
eg --vault "$VAULT" delete --target "$NOTE" --reason "该材料已被更权威的版本取代" --proposal "$PID" --user-request  # expect: 6
eg --vault "$VAULT" delete --target "$NOTE" --reason "该材料已被更权威的版本取代" --proposal "$PID" --confirm --user-request
eg --vault "$VAULT" search 注意力 --include-deleted
eg --vault "$VAULT" undelete --target "$NOTE" --reason "复核后决定保留这份材料"
```

### 8.7 把审核与确认交回给用户

Agent 不是用户的代理人，**不得代替用户点头**：

- [ ] 需要确认的地方（退 `6`）如实上报，**没有**自己补 `--confirm` 重跑。
- [ ] 没有生成任何状态类 op（`deprecate` / `restore` / `set_replaced_by` / `delete` / `undelete`）。
- [ ] 没有在自动路径改写「知识内容」；「用户补充」全程未写。
- [ ] 需要删除时只提交了 `eg proposal new`，把批准与执行留给用户。
- [ ] `support_check[]` / `warnings[]` 原样转述，没有把「建议」写成「已处理」。

## 9. S3 / M4 增量规程：对账命令与失效端点可见性（Agent 必读）

M4（S3）在 M3 的 18 条命令之上新增 **恰两条** 顶层命令——`eg reconcile`（全库对账）与
`eg check`（只读结构体检），使顶层命令数从 18 变为 **20**；同时把「失效对端默认隐藏」
落成 `eg rel` / `eg card show` 的 `--include-deprecated` 只读 flag。三者都**不属加工主链路**
（capture→context→apply），本节写清它们的口径与加工 Agent 的边界。

**一句话先记住：`eg reconcile` / `eg check` 命令本身不是写命令的手动前置。** 它们都**不**需要 Agent
挂在任何写命令之前；加工主链路每次写入**不需要**、也**不应**先手动跑对账。（M6 起，A 类写命令
**内部**自动跑写前强校验——校验不过则退 `5` + `E15`、零写入，见 §11.6；这道闸门在写命令内部，
不是要 Agent 先手动跑 `eg check`。）

### 9.1 `eg reconcile`（全库对账，M4；有写入）

```
eg reconcile [--dry-run] [--json]
```

| 项 | 口径 |
|-|-|
| 职责 | **全库**对账：R1（Git 纳管）+ R2（`reviewed_at` 补齐）+ R3（关系校验，只报告）+ R4（重复 ID / 悬空引用 / 孤儿）+ R5（跨领域移动检测，只报告）+ R6（综述可能失准标记）+ R7（材料支持不足实时判定，只报告，**永不改 `status`**）<br>**悬空引用（`dangling_ref` / E12）覆盖 frontmatter 全部引用承载字段（恰四类）**：`note.source→原文` / `card.sources[].note→材料笔记` / `card.sources[].source→原文` / `replaced_by.target→知识卡`；指向原文的两类只在 `sources/` 已采样时判定。**关系条目 `target` 缺失归 R3（E13/E14），不进 E12**；`sources[].source` / `sources[].note` 缺失由 E12 独家承载，R7 让位、不重复报 `W20`（一件事一码） |
| 是否写入 | **是**：R1 的纳管 commit + R2 / R6 的 frontmatter 补写（经内存 ChangePlan 落盘，B3 前置比对不豁免） |
| commit | **恰 0 或 1 次**（零改动即零 commit；R1 与 R2 / R6 合并为一次） |
| `--dry-run` | 只跑检查与报告，**零写入、零 commit**；`reconcile.ran = true`、`reconcile.commit = null` |
| 全库口径 | **不接受任何对象参数或范围收窄参数**（无 `--target` / `--domain`；收窄属 S4 性能面）；未声明参数当场判非法 |
| 是否前置 | **不作为任何写命令的前置**；不做交互式确认（不产生退出码 `6`）。`reconcile` 的写入落盘后会顺带做一次写后索引同步（S4；索引问题只报 `W22` / `W24`，不改退出码、不回滚）。**M6 现态**：`reconcile --repair` 是 A 类写命令，其写路径已接入 M6 强原子事务与 `run.lock`（写后索引同步在同一把锁内），锁不可用退 `5` + `E16`、写前复核失败退 `5` + `E15`（见 §11）；`--dry-run` / 只报告路径仍零写入、零锁副作用 |

`eg reconcile` 的**退出码**（M4 历史语义：`0/1/2/3/4`；**M6 现态**：`--repair` 写路径接入强事务后另可退 `5`——锁不可用 `E16` / 写前复核失败 `E15`，均零权威写入，见 §11；仍**不产生** `6`）：

| 退出码 | 含义 |
|-|-|
| `0` | 成功（**含只有 warning 级 finding** 的情形，例如仅有孤儿 `W17`） |
| `1` | 参数非法（零写入） |
| `2` | 存在 **error 级 finding**（`duplicate_id` / `dangling_ref` / `relation_target_missing` / `relation_prefix_invalid`）；**先完成 R1 纳管 commit** 再退 `2` |
| `3` | 有写入被 B3 跳过（`content_hash` 过期），已写部分保留并提交 |
| `4` | Git 提交失败（磁盘保留现状，不做破坏性还原） |

- **加工 Agent 通常不需要主动调用它**：对账是运维/收口动作，不是每篇材料加工的一环。
  若确要自查结构，优先用只读的 `eg reconcile --dry-run` 或 `eg check`（零写入）。
- 结果落在 envelope 的 `data.reconcile` 对象里，最终报告如实转述，不美化、不合并。

### 9.2 `eg check`（只读结构体检，M4；零写入）

```
eg check [--json]
```

| 项 | 口径 |
|-|-|
| 职责 | `eg reconcile --dry-run` 的**只读别名子集**：**只跑 R3 + R4 恰 7 个 check**——重复 ID / 悬空引用 / 孤儿 / 关系目标缺失 / 关系前缀非法 / 反向关系不对称 / 关系重复<br>其中**悬空引用（E12）覆盖 frontmatter 全部引用承载字段（恰四类）**：`note.source→原文` / `card.sources[].note→材料笔记` / `card.sources[].source→原文` / `replaced_by.target→知识卡`；关系条目 `target` 缺失走 R3 的 `relation_target_missing`（E13），不进 E12 |
| 是否写入 | **否**：零写入、**恒 0 次 commit**、不改写 `eg report --last`，也没有 `--fix` / `--repair`（修复走 `eg reconcile`） |
| 未判面 | R1 / R2 / R5 / R6 / R7 的 5 个 check **不判**（未判 ≠ 不存在，全量判定请跑 `eg reconcile`） |
| 是否前置 | `eg check` **命令本身不作为任何写命令的前置**；不接受任何范围收窄或可见性参数。**M6 现态**：`eg check --strict` 对本命令 finding 是**恒等变换**——`eg check` 只判 R3/R4 结构（`E11`–`E14` / `W13`–`W20`），与强校验升级面（`W1`/`W2`/`W3`/`W4`/`W6`，真源 `internal/reconcile/strict.go`）**不相交**，故 `--strict` 不改本命令任何 finding severity、不改退出码。真正把 `W1`/`W2`/`W3`/`W4`/`W6` 升 error 并退 `5` + `E15` 的是 **A 类写命令内部的写前强校验**（自动执行，复用同一 strict 逻辑），**不需要 Agent 手动先跑 `eg check`**（见 §11） |

`eg check` 的**退出码**（恰三档）：`0` 无 error 级 finding（含只有 warning）/ `1` 参数非法（零写入）/
`2` 存在 error 级 finding（零写入、零提交）。它**永不**产出 `git_uncommitted` / `reviewed_at_missing` /
`domain_moved` / `recap_stale` / `support_insufficient` 五个 `check`（那些只属 `eg reconcile`）。

### 9.3 失效对端默认隐藏与 `--include-deprecated`（M4 裁决②落地）

`eg rel` / `eg card show` 的关系端点视图，**默认隐藏对端 `status: deprecated` 的条目**；
用户/Agent 显式加 `--include-deprecated` 才展示，展示时保留 M2 的 `[失效]` 标记。四象限真值表：

| 对端 `status` | 对端 `deleted_at` | 默认（无 flag） | `--include-deprecated` |
|-|-|-|-|
| `active` | 空 | **展示** | 展示 |
| `deprecated` | 空 | **隐藏** | **展示，带 `[失效]` 标记** |
| `active` | 非空 | **隐藏** | **仍隐藏** |
| `deprecated` | 非空 | **隐藏** | **仍隐藏** |

- **正交性（最关键一条）**：`--include-deprecated` **只**放开 `deprecated` 维度，**绝不**影响逻辑删除维度——
  已逻辑删除的端点在任何 flag 下都不出现（继承 M3，不放宽）。它与 `--to` 也正交（`--to` 收窄对端，flag 放开可见性）。
- **看的是对端（peer）不是被查卡自身**：在 `k-x` 上执行 `eg rel k-x`，`k-x` 自身是否 `deprecated` 不影响过滤；
  只有关系**另一端**是 `deprecated` 的条目才被默认隐藏。`replaced_by` 的典型形态「旧卡(deprecated)→新卡(active)」因此有推论：
  在新卡上查反向 `replaced_by`（来源是旧的 deprecated 卡）**默认隐藏**，加 flag 才见——这不是 bug。
- **`Q4` 提示**：默认视图确有 `N ≥ 1` 个条目因 `deprecated` 被隐藏时，envelope 追加**恰一条**查询域诊断
  `Q4`（`level=warning`、`path=(汇总)`、`op_index=-1`，不影响退出码，仍退 `0`）：
  `本次查询默认隐藏了 N 个 deprecated 端点，使用 --include-deprecated 查看`；
  加 `--include-deprecated` 时**恒不产出** `Q4`。`Q4` **绝不触发 `Q3`**，诊断次序为 `Q1 → Q2 → Q4 → Q3`（`Q3` 仍在末位）；
  `N` 只数因 `deprecated` 被隐藏的**条目数**，不含因逻辑删除被隐藏的条目（后者静默隐藏、无提示）。
- **`--json` 的 `data` 键集合不扩张**：`eg rel` 仍恰五键、`eg card show` 键集合不新增（不加 `hidden_deprecated` 之类）；
  被隐藏的信息只经 `warnings[]` 的 `Q4` 承载。

### 9.4 加工 Agent 的边界（本节自检）

- [ ] 没有把 `eg reconcile` / `eg check` 当成加工主链路的一步：**对账不是写命令的前置**，每次 `eg apply` 前不需要先对账。
- [ ] 需要只读自查时用 `eg check` 或 `eg reconcile --dry-run`（零写入零 commit），不误用会写入的 `eg reconcile`。
- [ ] 转述关系视图时说清默认隐藏了失效对端：看到 `Q4` 就如实报「有 N 个 deprecated 端点被隐藏，加 `--include-deprecated` 可见」，不要把默认视图说成「全部关系」。
- [ ] 不给 `eg search` / `eg context` / `eg unreviewed` / `eg reconcile` / `eg check` 传 `--include-deprecated`（这些命令不接受该 flag，传入退 `1`）。

## 10. S4 / M5 增量规程：派生索引 `eg index`、检索接入索引、排序分页与 `eg bench`（Agent 必读）

M5（S4）在 M4 收口的 20 条命令之上新增 **恰两条** 顶层命令 —— `eg index`（派生索引）与
`eg bench`（性能采样），使顶层命令数从 20 变为 **22**（这是 M5 的**收口值**）。

**一句话先记住：Markdown 是唯一权威来源，`.index/` 只是可随时删除、可完整重建的派生物。**
索引里的一切都能从权威 Markdown 重算出来，因此：**任何时候「索引说的」与「Markdown 说的」不一致，
一律以 Markdown 为准**，处置方式是重建索引，而不是去改 Markdown 迁就索引。

**第二句：索引不是任何命令的前置。** `eg search` / `eg card show` / `eg rel` 自 M5 起**会**在索引
健康时走索引后端（见 §10.2），但索引缺失 / 损坏 / 陈旧时一律**降级为全量 Markdown 扫描**并如实留痕：
结果与索引在位时**等价**，退出码不变。**不建索引不影响任何命令可用**，只影响快不快。

### 10.1 `eg index`（派生索引构建 / 重建 / 只读体检 / 增量收敛，M5）

```
eg index build   [--json]
eg index rebuild [--json]
eg index status  [--strict] [--json]
eg index sync    [--json]
```

| 项 | 口径 |
|-|-|
| 子命令 | **恰 4 个**：`build` / `rebuild` / `status` / `sync`。必须带子命令，缺子命令退 `1` |
| 命令私有 flag | **恰 1 个**：`--strict`（只对 `status` 有语义，挂在别的子命令上退 `1`；`--json` / `--vault` 是全局 flag，另计） |
| 写入面 | **恰一个目录** `.index/`。`domains/` / `sources/` / `proposals/` / `reviews/` 字节零变更 |
| commit | **恒 0 次**（`.index/` 已被 `eg init` 写进 vault 的 `.gitignore`，本来就不进 Git） |
| 退出码 | `0` 成功 / `1` 参数非法（零写入）/ `4` 派生索引写盘失败（`.index/` 已清理、权威 Markdown 零改动，重跑即可）/ `5` 锁不可用（`E16`，`run.lock` 等待超时）或写前保留条目类型违规（`E15`，`.index/` 零写、维护一格未跑）——**M6·T-074 起启用**，三条维护命令与 plan 写链共用同一把 `run.lock`。**不产生 `6`**（无用户闸门） |

- **`eg index build`** 三支语义，按现况自动择一，且都只写 `.index/`：
  索引**不存在** → 全量构建；索引**存在且健康** → **no-op（本次一个字节都不写）**；
  索引**存在但不可用** → 可恢复重建（先删 `.index/` 再全量建，并在 `warnings[]` 留痕）。
- **`eg index rebuild`** 无条件先删 `.index/` 再全量构建，结果与全新构建**逐字等价**。
  schema 不兼容一律**整库重建，永不迁移** —— 派生物没有迁移的必要。
- **`eg index status` 恒退 `0`**：「索引坏了 / 索引旧了」都是**诊断**，不是一次失败。
  它同时给出**库体检**（`health` = `healthy` / `missing` / `corrupt`）与**一致性**
  （`freshness` = `fresh` / `stale` / `unusable`）两格，诊断码单值互斥：
  `stale`（**`W22`**，索引落后于权威）/ `missing`（**`W23`**）/ `corrupt`（**`W24`**），并附
  `index_meta` 摘要。它是**只读**命令：零写入、恒 0 次 commit，也**不改写** `eg report --last`。
  - 默认路径允许 `(size, mtime)` 与索引记录逐格一致时沿用索引里的 `content_hash`（省一次计算）；
    **`--strict` 忽略这条快路径，对全部文件重算 `content_hash`**，用于「怀疑索引说了假话」时对账。
    两条路径的判定口径完全相同，差别**恰在**省不省一次 hash 计算，且都不写一个字节。
- **`eg index sync`** 把索引**收敛**到与权威 Markdown 一致：只重算受影响文件对应的行，
  与整库重建的结果**等价**，且**幂等**（第二次跑恒 `action=noop`、零写入）。
  索引缺失 → 退化为全量构建、索引不可用 → 退化为整库重建，**退化一律如实留痕**（不静默）。
- **写命令写后自动同步**：`eg apply` / `eg edit` / `eg delete` / `eg undelete` /
  `eg mark-reviewed` / `eg reconcile`（以及同走 `apply` 写口的 `deprecate` / `restore` /
  `replaced-by` / `rel add|remove`）在 **Markdown 落盘且 commit 成功之后**自动把索引带到 fresh。
  三条硬边界：① 索引**未建**时静默跳过 —— 写命令**不替你建索引**；② 索引不可用时如实报
  `W24` 并跳过，**不自动修**（修复走 `eg index rebuild`）；③ 索引同步失败**绝不回滚**已落盘的
  Markdown、**不改变退出码**，只降级成一条 `W22` 提示你补跑 `eg index sync`。
- **写后同步的挂载面是一个封闭枚举**：上面列出的写命令**之外**的命令（`eg capture` /
  `eg proposal new|approve|reject` / `eg config set` / `eg init`）也会产生 commit，但**不挂**写后同步。
  它们让 Git HEAD 前进后，`eg index status` 会**如实**报 `stale`（`W22`，`reason=head_moved`，
  三向 diff 计数全 0 —— 没有任何文件内容变化），**只报不阻断读**；一条 `eg index sync`
  （或紧随其后的任意一条已挂载写命令）即收敛回 `fresh`。**不主动追 HEAD 是明写的边界**，
  不是缺陷：悄悄把 head 抹平等于让 `status` 说假话。
- **`.index/` 随时可删**：删掉整个目录**零信息损失**，重跑 `eg index build` 即复原。
  磁盘紧张、索引可疑、或不确定索引是否可信时，删掉重建**永远是安全动作**。
  **M6 唯一例外**：`.index/txn/` 事务日志**不是**索引派生物——存在未闭合事务时删 `.index/`
  会抹掉崩溃恢复所需的前像。因此「随时可删」在 M6 收敛为「无未闭合事务时随时可删」，详见 §11。
- 纯 Go 实现，全程 `CGO_ENABLED=0` 静态构建，不引入 C 工具链。**索引维护命令自身不开事务**
  （`.index/` 是可重建派生物，没有前像可回滚），但**与 plan 写链共用同一把 `run.lock`**：
  否则一次 `rebuild` 可能在别人事务提交中途把库抽走。M6 引入的 `run.lock` 与 `.index/txn/`
  事务日志的完整语义见 §11。

### 10.2 检索读路径接入索引与降级语义（M5）

三条只读命令 `eg search` / `eg card show` / `eg rel` 自 M5 起**优先走索引后端**，索引不可信时
**降级为全量 Markdown 扫描**。这套语义的全部要点：

| 索引现况 | 本次读走谁 | 留痕 |
|-|-|-|
| 健康且与权威一致（`fresh`） | 索引后端 | 无诊断 |
| 落后于权威（`stale`） | **降级**为全量扫描 | `W22` + `Q5` |
| 缺失（`missing`） | **降级**为全量扫描 | `W23` + `Q5` |
| 损坏 / 不可用（`corrupt`） | **降级**为全量扫描 | `W24` + `Q5` |

- **`Q5` 是「本次读走了降级路径」的查询域诊断码**：每次读**至多一条**，且必与
  `W22` / `W23` / `W24` 中的**恰一条**原因码同现（有 `Q5` 必有原因码，反之亦然）。
- **降级不是失败**：三条读命令**恒退 `0`**，字段、键序、总数口径与索引在位时**逐字等价** ——
  `Q5` 只是告诉你「这次结果是扫描算出来的，不是索引算出来的」。它**绝不触发** `Q3`
  （`Q3` 的语义是「结果不完整」，而降级结果是完整的）。
- **权威永远赢**：降级读的结果以权威 Markdown 为准。索引与 Markdown 不一致时不存在
  「用哪个」的选择题 —— 一律 Markdown，处置是 `eg index sync` / `eg index rebuild`。
- **「索引健康但这次查询不能用索引表达」不是降级**，因此**不产** `Q5`：索引既没坏也没旧，
  没有任何要修的东西，如实走扫描即可。
- **Agent 怎么转述**：看到 `Q5` 就说「本次检索走了降级路径（原因见同时给出的索引诊断），
  结果以权威 Markdown 为准、与索引在位时一致」，**不要**说成「检索失败 / 数据不全 / 索引坏了导致查不到」。

### 10.3 排序、分页与 `replaced_by` 反向查询（M5）

**关系视图四级全序**（`eg rel` / `eg card show` 的关系条目）：
`relation_type` → 对端 `peer_id` → `path` → 边身份（`from` | `type` | `target` | `reason`）。
第四级保证**任何两条不同的边都有确定先后**，同一语料的输出因此逐字可复现。

**分页参数恰两个**，作用面**恰** `eg search` / `eg card show` / `eg rel` 三条读命令：

```
  --limit <n>     否，默认 50；最多返回条数，0 = 不限量；发生截断时产恰一条 W25
  --offset <n>    否，默认 0；跳过的条数；超出总数返回空结果且仍退 0
```

- `--limit` 是**本次返回条数的全局上限**：`eg card show` / `eg rel` 的正向与反向关系走
  **同一个合并序列**分页，不会各自取满而返回 `2 × limit` 条。
- **截断产恰一条 `W25`**，且 `total` 仍是**截断前**的总数 —— 截断结果绝不能被读成完整结果。
  `--limit 0`（不限量）**永不产** `W25`。
- `--offset` 超出总数是**正常情形**：返回空结果、仍退 `0`，不是错误。
- 两者为**负数或非整数** → 参数错，退 **`1`**（`status=failed`）、零写入。这两个 flag **只属**
  上述三条读命令，挂到别的命令上（如 `eg index status --limit 5`）同样判非法退 `1`。
  （C2 · I-…-008 改判：分页参数非法属**参数错**，退 `1`／`status=failed`，不再退 `4`／`partial`）：
  只读命令不产 commit，`4`（Git 提交失败）/ `partial`（部分写入已提交）这两个语义在只读面上
  根本不成立，故统一收敛到「参数错 = 退 `1`」这一档。

**`replaced_by` 正反双向查询**：`eg rel <k-id> --replaced-by`

- 正向（**谁取代了本卡**）：读本卡 frontmatter 的 `replaced_by.target`，**至多一条**。
- 反向（**本卡取代了谁**）：全库反查 `replaced_by.target == 本卡` 的卡，可多条。
- 方向语义逐字沿用 M3：`A.replaced_by = B` 读作「**A 已失效，被 B 取代**」。因此
  `eg rel A --replaced-by` 的正向与 `eg rel B --replaced-by` 的反向**读的是同一条记录**。
- 条目 `type` 逐字为 `replaced_by`，每条恰五键（`from` / `type` / `target` / `reason` / `path`），
  `eg rel` 的 `data` 仍恰五键 —— **形态零扩张**。
- 取数一律以**权威 Markdown** 的 `replaced_by` 字段为准（索引侧只复用既有列、**不新增表**）。
- 与可见性**正交**：被取代的卡通常正是 `deprecated` 卡，默认视图按既有口径隐藏并产 `Q4`，
  加 `--include-deprecated` 可见。这是既有策略的结果，不是这里的新规则。

### 10.4 `eg bench`（性能采样，M5；只读）

```
eg bench [--json]
```

- **只报实测值，不判合格**：它采样五个指标并原样报出，**不**做门槛比较、**不**因为慢而失败。
- `--json` 输出**恰五键**：`search_p95_ms` / `card_show_p95_ms` / `rel_p95_ms` /
  `index_build_ms` / `index_incremental_ms`。
- **只读采样**：当前 vault 的权威 Markdown 与 `.index/` **一个字节都不写**、恒 0 次 commit
  （构建两指标在临时目录里采样，不碰你的 `.index/`）。
- 不收位置参数；vault 里没有任何知识卡时如实报错退 `1`（零副作用）。
- **Agent 边界**：`eg bench` 是**诊断命令**，不是加工主链路的一步，也不是任何命令的前置。
  转述时报「实测 P95 = N ms」，**不要**替它下「性能合格 / 不合格」的结论 —— 门槛是
  M5 合同里的工程约定，不是这条命令的输出。

### 10.5 索引子系统的边界（M5 现态；强原子 / 锁 / 恢复见 §11）

- **索引不做「陈旧就拦住你」**：陈旧（`W22`）只报不阻断 —— 任何读命令都照常退 `0`。
- **索引不参与写入决策**：写命令的判定、退出码、Markdown 落盘一律与索引无关；
  索引只在**写成功之后**被带到 fresh（见 §10.1 的写后同步三条硬边界）。M6 起写后索引同步在写事务的**同一把 `run.lock`** 内完成，故写命令成功后 `index status=fresh`。
- **强原子事务 / `run.lock` / 事务日志 / 崩溃恢复 / 块级安全合并 / 写前强校验 / 退出码 `5`**：这些属 S5，**已在 M6 · T-070～074 落地并启用**（见 §11）；退出码全集自 M6 起为 `{0,1,2,3,4,5,6}`。索引子系统本身仍不是任何命令的前置。

### 10.6 加工 Agent 的边界（本节自检）

- [ ] 没有把 `eg index build` 当成加工主链路（capture→context→apply）的一步：**索引不是写命令的前置**。
- [ ] 没有把 `eg bench` 当成主链路的一步：它是只读诊断命令。
- [ ] 没有把索引当权威：转述事实一律以 Markdown / 命令输出为准；`.index/` 只是派生物。
- [ ] 没有手工编辑、手工删除 `.index/` 里的单个文件；需要处置时整目录删掉后 `eg index build` 重建。
- [ ] 看到 `W23` / `W24` 就如实报「索引缺失 / 索引不可用，可跑 `eg index rebuild` 重建」，不把它说成数据损坏。
- [ ] 看到 `W22` 就如实报「索引落后于权威，跑 `eg index sync` 收敛」，**不**说成「检索被锁住 / 数据丢了」。
- [ ] 看到 `Q5` 就如实报「本次读走降级路径，结果以权威 Markdown 为准、与索引在位时一致」，
      **不**说成「检索失败 / 结果不完整」（结果不完整才是 `Q3`）。
- [ ] 看到 `W25` 就如实报「结果被 `--limit` 截断，共 M 条、本页 N 条」，**不**把截断结果当成全部结果。
- [ ] 看到 `eg capture` / `eg proposal` / `eg config set` 之后的 `stale`（`reason=head_moved`）不当成 bug 上报：
      这是封闭挂载面的明写边界，跑 `eg index sync` 收敛即可。
- [ ] 对**强原子事务 / 锁 / 事务日志 / 块级安全合并 / 写前强校验 / 退出码 `5`**：M6 · T-070～074 已落地并启用（见 §11），如实按 §11 的行为转述，**不**再声称「属 S5 / M6、还没做」。
- [ ] 没有给 `eg index` 传 `--strict` 之外的命令私有 flag，也没有把 `--strict` 挂到 `status` 之外的子命令上（退 `1`）。
- [ ] 没有把 `--limit` / `--offset` 传给三条读命令之外的命令（传入退 `1`）。

### 10.7 可跑示例

```bash
eg --vault "$VAULT" index status --json
eg --vault "$VAULT" index build --json
eg --vault "$VAULT" index status --json
eg --vault "$VAULT" index rebuild --json
eg --vault "$VAULT" index sync --json
eg --vault "$VAULT" index status --strict --json
eg --vault "$VAULT" index build --strict  # expect: 1
eg --vault "$VAULT" bench --json
eg --vault "$VAULT" search 注意力 --limit 10 --offset 0 --json
eg --vault "$VAULT" card show "$CARD" --limit 0 --json
eg --vault "$VAULT" rel "$CARD" --limit 5 --json
eg --vault "$VAULT" rel "$CARD" --replaced-by --json
eg --vault "$VAULT" rel "$CARD" --replaced-by --include-deprecated --json
eg --vault "$VAULT" search 注意力 --limit -1  # expect: 1
eg --vault "$VAULT" index status --limit 5  # expect: 1
```

## 11. S5 / M6 增量规程：强原子事务、锁、事务日志、崩溃恢复、块级安全合并、写前强校验（Agent 必读）

M6（S5）**不新增任何顶层命令**（命令总量仍 **22**），也不改任何命令的用户可见默认行为；它把
「写入的原子性与安全性」这一层从「直落实盘 + 事后 Git」升级为**强原子事务**：所有 A 类写命令
统一走「持锁 → 崩溃恢复 → 锁内重读重校验 → 内存预演 → 发布 intent → 多文件原子提交 →
提交后 Git → 锁内索引同步 → 释放锁」的固定时序。退出码全集自 M6 起从 `{0,1,2,3,4,6}`
扩为 **`{0,1,2,3,4,5,6}`**，新增诊断码**恰 5 条**：`E15` / `E16` / `W26` / `W27` / `W28`
（`W21` 仍不分配）。**Markdown 仍是唯一权威来源**，事务与锁只保护写入过程，不改变「权威=Markdown、
`.index/` 是派生物」这条根边界。

**一句话先记住：M6 让写入要么全成、要么全不成，且并发写者串行化；Agent 侧不需要任何新动作，
只需正确识别退出码 `5` 与五个新诊断码，并如实转述。**

### 11.1 退出码 `5` 与两类写前阻断（`E15` / `E16`）

退出码 `5`（导出常量 `ExitPrecheckOrLock`，全表**唯一**取值 5 的常量）是**两类写前阻断共用一个码**，
共同事实是：**本次请求零权威写入、无 commit、事务保持未闭合（或未开）**。

| 诊断码 | 触发 | 处置 |
|-|-|-|
| `E15`（写前强校验失败） | 锁内重读重校验后强校验不过（`W1`/`W2`/`W3`/`W4`/`W6` 升 error）、崩溃恢复屏障 fail closed、`.index/txn/` 保留条目类型违规、B3 前像不匹配、损坏事务 / 路径逃逸 / seq 溢出等写前安全复核异常 | 读 `data.errors[]` 的 `code` + `path` + **`target`**（事务类原因的 `txn_id` 在 `target` 位），或先跑 `eg check` / `eg check --strict` 定位后**修 plan / 修数据**再重投；**事务扫描阻断态**（多 `OpenTxn` / 损坏事务）没有 plan 可修、重投必然再退 `5`，须按 §11.4.1 拿 `txn_id` 列表 + 不可解析原因 + 人工出路处置；**不得**绕过校验强写 |
| `E16`（锁不可用） | `run.lock` 等待超过超时（默认 10s / `EG_LOCK_TIMEOUT_MS`）仍拿不到 | 说明有并发写者持锁：**稍后重试**或排查是否有卡死的写进程；**不得**删 `run.lock` 强写 |

- **映射是单点的**：`E15` / `E16` → `5` 的翻译只发生在 `internal/cli` 的 `classifyExit5` +
  `ExitCodeFor` 一处，最终 `os.Exit` 唯一调用点仍是 `cmd/eg/main.go`；`internal/**` 零 `os.Exit`。
- **`5` 覆盖 A 类写命令与 B 类索引维护命令**：`eg apply` / `eg edit` / `eg deprecate` /
  `eg restore` / `eg replaced-by` / `eg rel add|remove` / `eg delete` / `eg undelete` /
  `eg mark-reviewed` / `eg reconcile --repair` / `eg proposal approve|reject` 这些写路径，
  以及 `eg index build|rebuild|sync`（与写链共用同一把 `run.lock`，锁忙 / 保留条目违规同样退 `5`）。
- **只读路径永不退 `5`**：`eg search` / `eg card show` / `eg rel`（只读）/ `eg index status` /
  `eg bench` / `eg check` 等不取锁、不退 `5`，索引 / 结构问题一律用 `W22`–`W24` / `Q5` / `E11`–`E14`
  等**诊断**表达，恒不阻断读。

### 11.2 `run.lock` 单机文件锁（`E16` / `W28`）

- **形态**：`vault/.index/run.lock`，用 `flock` 加在**长驻 fd** 上；`run.lock` 是**普通文件**，
  与 `.index/` 里的索引派生物（`eg.db` / `eg.db-wal` / `eg.db-shm`）**并存**、互不冲突。
- **持锁期锁 inode 零替换**：对 `run.lock` **永不** tmp+rename、**永不** unlink / 删除、
  **永不**先释放再重建；正文（pid / acquired_at / argv，可选 txn_id）只由**同一把已加锁 fd**
  原地 `Ftruncate(0)` + `Pwrite` + `Fsync` 写入。正文写失败只是纯诊断，不影响互斥与退出码。
- **等待与超时**：拿不到锁时按真实墙钟退避重试，每次退避留痕 **`W28`**（`level=warning`，不改退出码，
  落在信封 `warnings[]`，见 §12.5）；超过超时仍失败 → 携 **`E16`** 的 `LockUnavailableError`（`level=error`，
  落 `data.errors[]` 且恰一条）→ 退 `5`，此刻 `.index/` 索引产物零变化。
- **陈旧锁安全接管**：唯一判据是 `flock` 是否可获得——持锁进程已死则 `flock` 立刻可得、直接接管；
  **不靠读 pid 猜活死**。
- **边界**：`run.lock` 是**单机**文件锁，**不承诺**跨机 / 网络盘 / 同步盘互斥（NFS / 同步盘上
  `flock` 语义不保证，见 INSTALL 已知限制）。

### 11.3 事务日志 `.index/txn/` 与多文件原子提交（S0~S9）

- **布局**：`vault/.index/txn/<txn_id>/{intent.json, commit, abort}` + `pre/`（前像）+
  `quarantine/`；`txn_id` = `t` + 16 位零填充小写十六进制，`O_EXCL` 建目录、溢出 fail closed。
  `.index/txn/` 是**事务日志、不是索引派生物**——它没有「重建」一说，是崩溃恢复的唯一依据。
- **A 类写链固定时序**（`internal/cli/recover_hook.go`，`apply` / `edit` / `deprecate` /
  `restore` / `replaced-by` / `rel add` / `rel remove` 等共用）：
  `S0` 锁外解析 plan → `S1` 取锁（`W28`）→ `S2` 崩溃恢复（`W26`）→ `S3` **锁内**重建
  store / env、重注入 UserRequest、重跑 `plan.Validate`（严禁复用锁外快照）→ `S4` 内存预演得
  accepted write-set（实盘零变化）→ `S5` 分配 `txn_id` + 记锁 + 写 intent（发布屏障）→
  `S6` 多文件原子提交（commit marker 在盘才算数）→ `S7` **提交后** Git（失败退 `4`、不回滚、
  不做第二次权威写）→ `S8` 锁内索引同步 → `S9` 释放锁、锁外渲染报告。
- **要么全成要么全不成**：多文件写在 intent 发布屏障之后原子提交；`commit` 标记落盘之前的任何
  中断，恢复时都会按 `pre/` 前像整体回滚。**零 accepted write-set 不开事务**（不分配 `txn_id`、
  不发 intent、不造空 Git commit）。
- **`txn_id` 审计边界是「分配成功」而非「提交成功」**：`AllocateTxnID` 一旦返回，
  intent 失败 / 主动 abort / Git 失败 / 提交成功四种结局都必须把 `txn_id` 写进报告。
- **保留策略**：事务目录保留 7 天 / 20 个，`Prune` 只在全局裁决通过后由调用方另行触发；
  `Scan` 全程零写盘。

### 11.4 崩溃恢复（`W26`）

- **每次进临界区的第一件事**（S2 / 索引维护 M2）就是 `txn.Recover`：扫 `.index/txn/`，
  把**未闭合**事务（有 intent、无 commit / abort）按 `pre/` 前像**回滚**到事务前状态。
- **真实回滚才产 `W26`**（`level=warning`，`CodeTxnRecovered`）：它是一次**权威 Markdown 写入**，
  必须如实进本次命令的报告——「我只是建了个索引 / 做了次写入」和「我顺手把上一次崩溃回滚了」
  对用户是两件事。noop（无未闭合事务）时不产 `W26`。
- **fail closed**：恢复本身被阻断（如 `txn/` 父目录被替换成符号链接、前像损坏）→ 携 `E15` →
  退 `5`、零权威写入、未闭合事务**保持未闭合**（绝不「先删了再说」）。
- **顺序不可颠倒**：恢复必须早于任何一次权威读 / 快照——否则会把索引写进一批「恢复前」内容、
  水位线却指向「恢复后」HEAD，得到一个自洽性已破却自称 fresh 的索引，比没有索引更坏。
- **Agent 怎么转述**：看到 `W26` 就说「检测到上一次未完成的写入，已安全回滚到该次写入前的状态」，
  **不要**说成「数据丢了 / 本次命令失败」。

### 11.4.1 事务阻断态怎么诊断、怎么处置（C2 · I-…-025）

**什么是阻断态**：**多于 1 个**未闭合事务，或存在**任一**损坏事务（`intent.json` 在盘但不可解析）。
此时全部 A 类写命令与 B 类 `index` 维护命令恒退 `5` + `E15`、权威零写入；C 类只读命令不受影响。
它**不会自愈**（单个未闭合事务才会被下一条写命令自动恢复），因此「重投一次试试」必然是死循环。

**怎么诊断（Agent 首选，只读、零副作用、退 `0`）**：

```bash
eg --vault "$VAULT" check --json            # eg --vault "$VAULT" check --strict --json 披露一样多（事务态在默认面就给）
```

阻断态下 `eg check` 的 `warnings[]` 里逐条给出：

| 读什么 | 含义 |
|-|-|
| `target` | 问题事务的 `txn_id`（**机器读法就取这一格**；`path` 是 `.index/txn/<txn_id>` 这一真实目录） |
| `message`（逐条） | 损坏事务的**不可解析原因**（JSON 非法 / 缺必需字段 / `journal_version` 不支持 / 标记形态非法…） |
| `message`（总述） | **全部问题 `txn_id` 列表** + 计数 |
| `message`（出路） | 人工处置步骤与风险 |

写命令退 `5` 时，同样这批事实在 `data.errors[]` 里（`code=E15`、`target`=`txn_id`）。
**`target` 是稳定读法，不要去解析文案。**

**怎么处置（无清理命令；M6 命令数恒 22，刻意不提供强制放弃开关）**：
① 看 `.index/txn/<txn_id>/intent.json` 判断该事务动过哪些权威文件；② 先备份整个
`.index/txn/<txn_id>/`；③ 删除多余的未闭合事务目录（只留 1 个）与损坏事务目录；
④ 之后任一写命令会自动恢复剩下那个未闭合事务并留痕 `W26`。

**Agent 怎么转述**：说「上一次写入留下了 N 个未完成/损坏的事务记录，写侧已被安全阻断（数据没被改坏），
需要人工按 README 的『事务阻断态的人工处置』清理其中多余的记录」，并**如实附上 `txn_id` 列表与原因**；
**不要**说成「数据损坏 / 库坏了」，也**不要**自行 `rm -rf` 事务目录 —— 删除会丢弃前像副本，
该事务已写出的字节将无法自动回滚（风险由人决定，不由 Agent 代劳）。

### 11.5 块级安全合并（`W27`）

- **触发**：仅当 `base_block_hash` 与当期有效块**不匹配**时才进入块级判定；匹配走 M3 逐字快路。
- **块粒度、无行级 diff**：把当期文件切成块，用 `base_block_hash` 找「仍逐字未变」的目标块。
  当期文件里仍存在 `block_hash == base_block_hash` 的块 ⇒ 差异落在别的块、与目标块不相交 ⇒
  **安全**，可做区间替换；否则差异触及目标块本身 ⇒ **不安全**。**不引入任何 diff 依赖**。
- **不安全一律跳过并留痕 `W27`**（`block_merge_conflict`，`level=warning`，落 `skipped[]`）：
  **绝不**强行覆盖、绝不做破坏性合并；用户补充分区永不写（B2）。
- **Agent 怎么转述**：看到 `W27` 就说「目标块自读取以来被改动，本次为安全起见跳过该块合并，
  请重新 `eg context` 取最新内容后重投」，**不要**说成「合并失败 / 冲突需手动解决冲突标记」。

### 11.6 写前强校验（`E15`）

- **A 类写命令在锁内重读重校验后自动执行写前强校验**：把 `eg check --strict` 的升级面
  （`W1`/`W2`/`W3`/`W4`/`W6` 升 error，真源 `internal/reconcile/strict.go`）复用为**写前闸门**——
  强校验不过则携 `E15` fail closed、退 `5`、零权威写入。
- **`eg check --strict` 命令本身是恒等变换**（见 §9.2）：它只判 R3/R4 结构码，
  不改自身 finding severity、不退 `5`；**真正**据强校验退 `5` 的是写命令内部，Agent
  **不需要**手动先跑 `eg check` 再写。
- **写前强校验也覆盖运行时保留条目体检**：`.index/txn/` 的 `run.lock` 必须是普通文件、
  `txn` 必须是目录，形态违规携 `E15`、零索引写。

### 11.7 三类命令分层（不得越线）

| 类别 | 命令 | 取锁 | 开事务 | 崩溃恢复 | 退 `5` |
|-|-|-|-|-|-|
| **A 类** plan 写链 / 直写 | `apply` / `edit` / `deprecate` / `restore` / `replaced-by` / `rel add|remove` / `delete` / `undelete` / `mark-reviewed` / `reconcile --repair` / `proposal approve|reject` | 是 | 是（S0~S9） | 是（S2） | 可（`E15` / `E16`） |
| **B 类** 索引维护 | `index build` / `index rebuild` / `index sync` | 是（同一把锁） | **否**（`.index/` 可重建、无前像可回滚） | 是（M2） | 可（`E15` / `E16`） |
| **C 类** 只读 | `search` / `card show` / `rel`（只读）/ `index status` / `bench` / `check` / `context` / `report` 等 | 否 | 否 | 否 | **否** |

- **A / B 共用同一把 `run.lock`**：因为它们的写入面在**同一个目录**里相互可见（写链 S8 在锁内
  `index.Apply`，`rebuild` 会清 `.index/` 非保留条目）。各持各锁 = 并发下不可判定态。
- **B 类不开事务的理由**：`.index/` 是可重建派生物，崩在半路的补救是重跑 `eg index rebuild`，
  而不是回放事务日志；给派生物开事务只会凭空造出一批「正确恢复动作恰好是什么都不做」的未闭合事务。

### 11.8 明确不做（各有边界，早做即越界）

- **不做分布式锁 / 跨机互斥**：`run.lock` 是单机 `flock`，网络盘 / 同步盘不保证。
- **不做行级 diff / patch**：块级安全合并只在块粒度判定，不引入 diff 依赖。
- **不做破坏性还原**：Git 提交失败（退 `4`）保留磁盘现状，绝不 `git checkout` / `reset` / 删文件。
- **不扩张命令 / kind 枚举**：命令总量仍 22，`skipped[].kind` 不因 `W27` 扩张。
- **不让 `.index/` 成为权威**：事务与锁只保护写入过程；转述事实一律以 Markdown / 命令输出为准。
- **不引入 cgo / 网络请求**：全程 `CGO_ENABLED=0` 静态构建。
- **不修改历史结论**：M1~M5 的历史验收事实与产品代码不因 M6 改写。

### 11.9 加工 Agent 的边界（本节自检）

- [ ] 看到退出码 `5` 就知道「本次零权威写入、无 commit」：`E15` → 修 plan / 数据后重投（或先
      `eg check --strict` 定位），`E16` → 稍后重试；**绝不**删 `run.lock` / `.index/txn/` 或绕过校验强写。
- [ ] 看到 `W26` 如实报「检测到上次未完成写入并已安全回滚」，**不**说成数据丢失或命令失败。
- [ ] 看到 `W27` 如实报「目标块已被改动、本次安全跳过该块，请重取上下文后重投」，**不**说成需手动解冲突。
- [ ] 看到 `W28` 如实报「正在等待并发写者释放锁」，**不**当成错误上报；持续超时才会转成 `E16` 退 `5`。
- [ ] 不把「强原子事务 / 锁 / 事务日志 / 崩溃恢复 / 块级合并 / 写前强校验 / 退出码 `5`」再说成
      「属 S5 / M6、还没做」——它们**已在 M6 · T-070~074 落地并启用**。
- [ ] 不把 `run.lock` / `.index/txn/` 当索引派生物删除：存在未闭合事务时删 `.index/` 会破坏崩溃恢复。
- [ ] 不承诺跨机 / 网络盘的写入互斥——`run.lock` 只保证单机。

## 12. 命令层 error 诊断编号 `E17`–`E25`（**每条 error 都有码**，C2 · I-…-015 补齐）

**一句话先记住：`data.errors[]` 里 `level=error` 的条目，`code` 永远非空且永远落在
`E*`/`W*`/`I*`/`Q*` 编号域内；只有「未编号 **warning**」才允许 `code=""`。**

以前命令层的 error 条目大量留空 `code`（`""`），Agent 只能去匹配中文 `message` —— 而 `message`
从不被任何合同冻结、随时可改。C2 批次把命令层错误族补上编号 `E17`–`E25`（既有 `E1`–`E16` /
`W*` / `I1` / `Q*` **一格未动**，A-29「只增不改」）。

### 12.1 `code` 与 `exit_code` 是两件事

- `code` 回答「**要改什么**」：改命令行、改 plan、补授权、修数据、手工提交……
- `exit_code` 回答「**有多严重 / 盘面什么状态**」：零写入？已提交？部分提交？
- 两者**故意不一一对应**：同一个 `E17` 可能退 `1`（多数）也可能退 `2`（`eg capture` 的
  `--url`/`--title` 同时缺失，合同 §1.3 指派为校验失败）；同一个退 `2` 也可能是 `E18`/`E19`/`E20`。
  **不要**用 `code` 反推退出码，也**不要**把 `code` 当退出码的同义词。

### 12.2 码表（处置口径逐条明写）

| 诊断码 | 触发（错误族） | Agent 处置 |
|-|-|-|
| `E17` | 用法 / 参数非法：缺必填参数、取值非法、互斥参数同时给出、分页参数非法、未知（子）命令、占位未挂载 | **改命令行**后重投；库内数据不用动 |
| `E18` | 前置事实不成立：`--target` / 提案 ID 解析不到文件、目标不在提案 `targets` 内、无历史报告、未找到 vault | 先 `eg search` / `eg card show` / `eg proposal list` **核对对象是否存在**，再重投 |
| `E19` | 授权 / 确认门禁未满足：缺 `--user-request`、缺二次确认标记、缺 `approved` 提案背书、提案 `status ≠ approved` | **补授权链**（`eg proposal create` → `approve`，再带齐佐证重投）；提案里写 `initiator: user` 不能自证 |
| `E20` | 权威产物读取 / 解析失败：文件读不动、frontmatter YAML 解析失败、库内时刻不可比较（**零写入**） | **修数据**：`eg check` 定位后修正该文件再重投；不要重试同一条命令 |
| `E21` | 执行未能完成：预演写口失败、原子事务放弃、对账修复未执行、提案改判失败、恢复屏障阻断、`eg bench` 采样失败（**权威 Markdown 零改动、无 commit**） | 读 `path` + `message` 排查磁盘 / 权限 / 并发写者，或先 `eg check --strict` 定位后重投 |
| `E22` | Git 提交失败（B4）：**字节已按目标态落盘并保持现状** | **修 Git 环境后手工提交**；**不要**重跑写命令、不要 `checkout` / `reset` / 删文件 |
| `E23` | 部分成功汇总（退 `3`）：有 N 处跳过 / 失败，已完成部分**已提交** | 按同一 `errors[]` 的逐条明细与 `report.skipped[]` 处置，**只重投被跳过的部分** |
| `E24` | 本次跳过未写入（B2 / B3）：该文件**字节零变化**；`kind` / `cause` 逐字见 `report.skipped[]`，也在本条 `message` 里可读 | `cause=content_hash_mismatch` → 重新 `eg context` 取 base 后重投；`user_block_not_preserved` → 用户分区无法逐字保留，人工处理后重投 |
| `E25` | 未分类错误（走到兜底分支，按 `1` 处置）——**这是实现 bug 的信号** | 按 `message` 提 issue；**不要**对 `E25` 写业务分支 |

### 12.3 `cause` 的家在 `report.skipped[].cause`，不在 `code`

B2 / B3 跳过的 `content_hash_mismatch` / `file_changed` / `user_block_unsafe` /
`user_block_not_preserved` 是 **`report.skipped[].kind` / `.cause` 的枚举值**，以前被塞进
`code` 位，会让「按 `E` / `W` 前缀路由」的接入方直接落到 `default` 分支。现态：

- `data.errors[].code` = `E24`（编号域内，可路由、可聚合）；
- `report.skipped[].kind` / `.cause` = 原枚举值**逐字不变**（机器可读归因的唯一去处）；
- 同一条 `message` 里同时写清 `kind=` / `cause=`（人读也不丢归因）。

### 12.4 自检

- [ ] 不再对 `message` 做子串匹配来判错误类型 —— 一律读 `code`。
- [ ] 见到 `code=""` 且 `level=error` 视为**产品缺陷**（合同 §5 只对 warning 开放空码）并如实上报。
- [ ] 不把 `E24` 的 `cause` 当成 `code`，也不把 `code` 当 `cause`。
- [ ] 见到 `E25` 不去改命令行「碰运气」，而是提 issue。

### 12.5 两个桶按 `level` 分流：`warnings[]` 与 `data.errors[]`（C2 · I-…-016）

**一句话先记住：`data.errors[]` 只装 `level=error`；warning / info 一律在信封顶层
`warnings[]`。失败也照样可能有 warning —— 退 `1`/`2`/`3`/`4`/`5` 时都要读 `warnings[]`。**

- 典型场景：撞上并发写者时，等待期每次退避留痕 `W28`（`level=warning`）落 **`warnings[]`**，
  终态 `E16`（`level=error`）落 **`data.errors[]`** 且**恰一条**；退 `5`、零写入、零 commit。
  以前 `W28` 混在 `data.errors[]` 里，按 `level` 分流的接入方会把「正在等锁」误报成 5 个错误。
- 反向也封闭：`data.errors[]` 里不会出现非 error 级条目，`warnings[]` 里不会出现 error 级条目。
- 本次没有 error 时**不产出** `data.errors` 键（不给空数组）；`warnings[]` 是必备键，无则空数组。
- 分桶只改「去处」：条目内容、条目数、桶内相对顺序、信封五键形状全都不变。

自检：

- [ ] 读诊断时**先看 `level` 再看桶**，不假设「有 error 就没 warning」。
- [ ] 锁忙场景如实转述为「正在等待并发写者释放锁（`W28` × N）+ 最终等待超时（`E16`）」。
- [ ] 见到 `warnings[]` 里出现 `level=error`，或 `data.errors[]` 里出现 warning / info，
      视为**产品缺陷**（违反合同 §3）并如实上报。

### 12.6 `--json` 两个位置等价，**解析失败也有信封**（C2 · I-…-017）

**一句话先记住：`eg --json <cmd> …` 与 `eg <cmd> … --json` 完全等价；参数打错（退 `1`）时
stdout 仍是五键信封，不会退化成人类可读文本。**

- 因此接入方可以**无条件**按「解析 stdout JSON → 读 `exit_code` / `status` / `data.errors[].code`」
  对接：参数非法时拿到的是 `exit_code: 1` / `status: "failed"` / `code: "E17"`（改命令行后重投），
  而不是解析异常导致的「未知失败」。
- 覆盖面包含**解析层**的四类失败：未知 flag（子命令位或全局位）、子动词非法、缺必需子命令、未知命令。
- `exit_code` 与进程退出码**逐字相同**；`--json=false` 表示显式关闭，仍走人类可读面。
- 不带 `--json` 时输出**保持人类可读**（不是"恒 JSON"）：格式由调用方的意图决定。

自检：

- [ ] 不再为「参数可能打错」写「先探测输出是不是 JSON」的兼容分支。
- [ ] 见到 `--json` 在场却拿到非 JSON 的 stdout，视为**产品缺陷**（违反合同 §3「五键必有」）并如实上报。
