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
2. **无确认交互**：S1 全部命令直接执行，不做二次确认，也不存在「需确认」这一退出码。
3. **CLI 绝不自选默认领域**：未配置 `default_domain` 时，除 `init` / `config` 外一律提示配置并退 `1`。
4. **`eg apply --plan` 是唯一程序化写入通道**：任何绕过它的文件改动都是违规（见 §5 B-01）。

S1 命令恰九个：`init`、`config get|set`、`capture`、`context`、`apply`、`search`、`card show`、`rel`、
`report --last`。其中 `search` / `card show` / `rel`（含 `rel add|remove`）在 M1 **只注册占位**，
被调用即输出「M1 未实现（S1 命令，M2 落地）」并退 `1`，零副作用——不要把它们排进主链路。

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
`eg report --last` 只读复现最近一次 apply 的报告，零写入零 commit，可随时重放。

### 2.6 退出码处置指引（S1 恰五值）

| 退出码 | 含义 | Agent 必须怎么做 |
|-|-|-|
| `0` | 成功 | 继续下一步；仍要读 `warnings[]` 并在报告里如实转述 |
| `1` | 用法 / 参数非法（零写入） | 改命令行参数后重试；不要改 plan 内容来「绕」 |
| `2` | 校验失败（仅 error 级，零写入） | 读 `data.errors[]` 的 `code`（E1–E6）+ `op_index` + `path`，**改 plan 后重投**；磁盘未变，不需要清理 |
| `3` | 部分写入被跳过 | 已写入部分**已提交**。读报告 `skipped[]`：`kind=file_changed` / `cause=content_hash_mismatch` 表示该文件自 `eg context` 读取以来变了 → **重新 `eg context` 取新 `content_hash`，只为被跳过的部分重投一份新 plan**；不得强写、不得重试同一份 plan |
| `4` | Git 提交失败 | **磁盘保留现状，不做破坏性还原。** 在报告写明「文件已写入但提交失败」并把未提交清单交给用户；**不得**自行 `git commit` / `git checkout` / `git reset` / 删文件 |

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
- **B-07 禁止生成状态类 op**：S1 没有失效 / 恢复 / 逻辑删除；卡建出来就是 `active`，Agent 不得改 `status`。
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

**不得承诺 S2+ 能力**（以下每一项在 **S1 不可用**，Agent 既不得调用，也不得在报告里承诺）：提案（`proposal`）、失效与状态变更（`deprecate`）、逻辑删除与恢复（`undelete`）、对账（S3 的 `eg reconcile` 命令；同名的 commit verb `reconcile` 是 `eg config set` 的提交动词，**≠ S3 `eg reconcile` 命令**，见 §4）、索引与检索调优（`index`，S4）、块替换、多文件强原子事务。

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
      "note": "两篇都在讲「可随算力扩展的通用方法」，核心知识与独立复用用途相同；第二篇补了一条成立条件（知识须能被系统自验证），故 conditions=different，按非核心补充追加到已有卡，不拆新卡"
    }
  ],
  "base": {
    "unprocessed.md": "sha256:00000000000000000000000000000000000000000000000000000000placeholder",
    "domains/ai-infra/knowledge/k-20260917-bitter-lesson.md": "sha256:00000000000000000000000000000000000000000000000000000000placeholder"
  },
  "ops": [
    {
      "op": "write_note",
      "source": "s-20260917-verification-the-key-to-ai",
      "note_id": "n-20260917-verification-the-key-to-ai",
      "title": "Verification, The Key to AI（Rich Sutton, 2001）",
      "sections": {
        "材料提炼": "- 原文主张：AI 系统必须能自己判断自己是否工作正常，这是可扩展的前提（验证原则）。\n- 原文论据：依赖人工构建与维护的大型知识库无法自我验证，规模一大就维护不动。\n- 原文结论：只保留能被系统自身验证的知识，学习与搜索才能持续放大。\n",
        "Agent 分析": "- 这一条给「通用方法为何能随算力扩展」补了一个前提：没有自验证，扩展就会被人工维护成本卡住。\n"
      },
      "output_cards": [{ "card": "k-20260917-bitter-lesson", "mode": "补充" }]
    },
    {
      "op": "append_card",
      "card": "k-20260917-bitter-lesson",
      "sections": {
        "解释与依据": "- 补充依据（Verification, The Key to AI, 2001）：通用方法能扩展的前提之一是系统能自行验证知识，否则维护成本会随规模抵消算力收益。\n",
        "条件与边界": "- 补充边界：该结论在「知识可被系统自身验证」时最稳固；纯人工维护的知识库不满足该条件。\n",
        "理解自检": "- 如果一个方法能吃下更多算力但无法自我验证，它还算「可扩展」吗？\n"
      }
    },
    {
      "op": "add_material_rel",
      "card": "k-20260917-bitter-lesson",
      "source": "s-20260917-verification-the-key-to-ai",
      "note": "n-20260917-verification-the-key-to-ai",
      "rel": "support",
      "reason": "第二篇从「自验证是可扩展前提」的角度为该卡结论提供支持性材料依据"
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
- [ ] 报告如实转述 `skipped[]` 与 `warnings[]`，退 `3` / `4` 时按 §2.6 处置，**没有**自行做 Git 操作。
