---
topic: ChangePlan 数据合同与校验分级（S1 唯一程序化写入通道）
stage: S1
milestone: M-001
task: T-evergreen.s1_main_flow-158614-011
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》§4.5 / §4.5.1 / §4.2 / §8.2
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
baseline: docs/specs/2026-08-31-evergreen-s1-tech-design.md#ch4
created: '2026-09-01'
updated: '2026-09-01'
updated_by: 项目维护者
---

# ChangePlan 数据合同与校验分级 — Contract（S1）

ChangePlan 是 Agent → CLI 的**唯一程序化写入通道**，也是本方案的**最小稳定合同**：
**结构稳定、字段与 op 可增**。本文件把结构定死，下游 T-…-012 / 013 / 014 / 015 / 016 / 017
一律照本文写代码，T-…-018 据此做 §14 逐行核对。实现与本文冲突时**先回本文补合同**，
不得在实现里自行发明字段或编号。

**唯一可执行基线**：`docs/specs/2026-08-31-evergreen-s1-tech-design.md`
（§4 数据合同 = `#ch4`，§9 四条安全底线 = `#ch9`，写路径硬约束 = `#write-path`，
M1 明确不做 = `#m1-not`）。姊妹合同：`docs/specs/2026-09-01-eg-cli-contract.md`
（`apply` 参数、`--json` 信封五键、退出码五值、诊断载荷结构）。
章节号（§4.5 等）是对权威文档的**来源标注**（provenance），本阶段未访问外部链接。

**硬边界（先读）**

1. **宽松口径**：只有会导致「数据无法解析、或关系无法定位」的问题才是 error（E1–E6，退 `2` 零写入）；
   其余一律 warning / info，**照写 + 进报告**。W1–W8 的**当前分级一律是 warning**，
   I1 是 info；文中任何「S5 起…」「S2 起…」的表述都是**阶段路线图**，不是 S1 分级。
2. **字段与 op 只增不改**：已有键不改名、不改语义、不改类型；新增 op 必须向后兼容。
3. **CLI 不做语义判断**：不读原文推断、不补全缺失、不猜领域、不改写 Agent 给的文本。
4. **写路径**：ChangePlan 的执行一律经 `store` 的字节区间插入（只追加），
   **禁止任何 YAML 序列化回写**（`yaml.Marshal` / `yaml.NewEncoder` 是 `make lint` 门禁）。

---

## 1. 顶层键（恰 8 个）

`plan` 是一个 JSON 对象（YAML 亦可，只读解析）。顶层键**恰 8 个**，与 §4.5 逐行一致：

| # | 键 | 类型 | S1 必填性 | 缺失 / 异常时的分级与 CLI 行为 |
|-|-|-|-|-|
| 1 | `plan_version` | int | **必填**，固定 `1` | 缺失或 `!= 1` → **E5 error**，退 `2`、零写入 |
| 2 | `verb` | string | **必填**；取值见 CLI 合同 §6（`process` / `reprocess` / `capture` / `init` / `reconcile`） | 缺失或未知值 → **未编号 warning**（§4.5「未知值退化」）+ 退化为 `process`，照常提交 |
| 3 | `domain` | string \| null | **必填**；本次加工的**唯一领域**；全局操作（如只改 `evergreen.yml`）填 `null` | 缺失 / 空串 → 按 `evergreen.yml` 的 `default_domain` 落位，并在报告 `default_domain_fallback.used = true` 如实说明；`default_domain` 未配置 → 退 `1`（EG-DOM-03，CLI 绝不自选领域）。写入目标落在 `domain` 之外 → **W1 warning**（照写） |
| 4 | `reason` | string | **必填**；进 commit 正文 `Reason:` 行 | 缺失 / 空串 → **I1 info**，commit 正文不输出该行，不拦截 |
| 5 | `requirement_ids` | string[] | 可选；进 commit 正文 `Requirement:` 行 | 缺失 / 空数组 → **I1 info**，不输出该行，不拦截 |
| 6 | `convergence[]` | object[] | 涉及已有卡时**建议填**；**逐卡条目、可重复**，每张被比较的候选卡一条 | 缺条目 / 三维度不齐 / `relation` 异常 → **W5 warning**，不拦截（详见 §2） |
| 7 | `base` | object（`id 或路径` → `content_hash`） | **必填**（涉及已有文件时）；`eg context` 的输出**原样**填入 | 未覆盖某个被改文件 → **W6 warning** + **跳过该文件**；`content_hash` 与磁盘不一致 → **跳过该文件**并记 `skipped[]`（B3）。新建文件可省或填 `null` |
| 8 | `ops[]` | object[] | **必填** | 空列表 → **未编号 warning（§4.5 `ops[]` 空列表）** + 输出**零写入报告**，退 `0`（零知识结果合法，EG-KNW-05） |

`base` 的形态是 **`{id 或 vault 内相对路径: content_hash}`**，是 **S1 唯一的并发保护依据**：
`apply` 在写每个文件前重算该文件字节的 `content_hash` 并与 `base` 比对，不一致就**跳过该文件**
（不覆盖、不强写、不重试、不排队）。`content_hash` 形如 `sha256:<hex>`，由 `eg context` 给出，
**Agent 不得自己算、不得省略**。

**最小合法 plan（顶层骨架）**

```json
{
  "plan_version": 1,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "把注意力机制这篇材料沉淀成一张知识卡",
  "requirement_ids": ["EG-KNW-04"],
  "convergence": [],
  "base": {},
  "ops": []
}
```

---

## 2. `convergence[]` 条目字段表（逐卡条目，可重复）

**每张被比较的已有候选卡一条**，**不要求**汇总成单一全局判定。字段名与 §4.5 plan 示例逐字一致
（示例中 `"relation": "core_change"` 逐字出现）。落点见 §14 EG-CVG-01 行的「落点」列
`convergence.relation`。

| 字段 | 类型 | S1 必填性 | 缺失 / 异常时的分级 |
|-|-|-|-|
| `card` | string（`k-…`） | **必填**（被比较的已有卡 ID） | 缺失 → **W5 warning**；ID 格式非法 → W5（本字段**不参与关系定位**，故不是 E2） |
| `relation` | string（**七值封闭枚举**，见 §2.1） | 涉及已有卡时**建议填** | 缺失 / 取值不在枚举内 / 与三维度结论矛盾 → 一律并入 **W5 warning** |
| `core_knowledge` | string（`same` \| `different`） | 建议填（三维度之一：核心知识） | 缺失或取值非法 → **W5 warning** |
| `conditions` | string（`same` \| `different`） | 建议填（三维度之一：成立条件） | 缺失或取值非法 → **W5 warning** |
| `reuse_purpose` | string（`same` \| `different`） | 建议填（三维度之一：独立复用用途） | 缺失或取值非法 → **W5 warning** |
| `note` | string | 可选（判定说明） | 缺失 → 不告警 |

`convergence[].relation` 的诊断字段路径形如 `convergence[0].relation`（诊断载荷见 §5）。
三维度判据：**任一维度 `different` → 拆两张卡**；**判不出（缺失 / 非法）也拆**（宁拆勿并，
EG-CVG-01 / EG-CVG-02 / EG-EXT-03）；三维度全 `same` 才允许复用已有卡。

### 2.1 `relation` 七值封闭枚举 → 处理关系 → 应产出的 op 组合（恰 7 行）

| `convergence[].relation` | 处理关系（中文） | 应产出的 op 组合 | 执行方 |
|-|-|-|-|
| `independent_new` | 独立新增 | `create_card`（新卡直接 `active`）+ `add_material_rel` | Agent 自动执行 |
| `same_semantics` | 语义相同 | **不新建卡**；只 `add_material_rel` 把新材料挂到已有卡 | Agent 自动执行 |
| `non_core_supplement` | 非核心补充 | `append_card` 到「解释与依据」/「条件与边界」（**不动「知识内容」**）+ `add_material_rel` | Agent 自动执行 |
| `core_change` | 核心变化 | `create_card` 新卡 + `add_relation`（`limits` / `derives` 等）指向原卡；原卡不改写、不失效 | Agent 自动执行 |
| `conflict_coexist` | 冲突并存 | 两卡同时 `active` + **恰一条** `add_relation`（`type: opposing`，方向由 CLI 规范化） | Agent 自动执行 |
| `uncertain` | 存疑 | `add_open_question` 写入材料笔记「存疑与待验证」分区；不建卡 | Agent 自动执行 |
| `deprecated` | 失效 | **S1/M1 不可用**：只由用户提出（S2 `eg deprecate`）；Agent 不得生成任何状态类 op | **只由用户提出（S2）** |

**前六种 Agent 自动执行；第七种 `deprecated` 只由用户提出（S2，S1 plan 中出现即视为越界）。**
`deprecated` 出现在 S1 plan 里同样**只判 W5**（S1 无状态类 op，该 op 组合本就走不通），
**不升级**为拦截项；严格化属 S5。

**`relation` 的 S1 分级口径（逐字）**：`relation` **缺失 / 取值非法 / 与三维度结论矛盾**
（如三维度全 `same` 却填 `core_change`）→ 一律并入 **W5** warning，**不拦截写入、不影响退出码**
（`0` / `3`），**不新增任何编号**。W5 的原义是「`convergence[]` 缺条目或三维度不齐」，
本组判定共用该编号。

---

## 3. S1 七个 op

`ops[]` 元素形如 `{"op": "<name>", …}`，**按声明顺序执行**。`op` 名的封闭集合恰七个：
`add_source`、`write_note`、`create_card`、`append_card`、`add_material_rel`、`add_relation`、
`add_open_question`。**未知 `op` → E5 error，整条 op 不执行**（见 §6）。

分区名在所有 op 中**逐字固定**（F5）：知识卡「知识内容」「解释与依据」「条件与边界」「用户补充」
「理解自检」；材料笔记「材料提炼」「Agent 分析」「用户补充」「存疑与待验证」「产出知识卡」。
`sections` 的键必须是上述固定名之一，且**「用户补充」任何时候出现即 E6**。

### 3.1 `add_source`（原文 + 收件区条目）

| 字段 | 类型 | S1 必填性 | 说明与分级 |
|-|-|-|-|
| `op` | string | **必填** | 固定 `add_source` |
| `url` | string | 与 `title` **至少一个** | 判重第一键（URL 规范化后精确匹配）；两者同缺 → **E5**（op 字段不成立，退 `2`） |
| `title` | string | 与 `url` **至少一个** | 判重第二键（标题精确匹配） |
| `body` | string | 新原文**必填** | 原文正文字节（Agent 已清洗）；命中已有原文 → **复用、正文不覆盖** |
| `reason` | string | **必填** | 收录理由；命中已有原文时**追加**到理由列表 |
| `source_id` | string（`s-…`） | 可选 | 省略时由 CLI 按 `saved_at` + `title` 生成；格式非法 → **E1** |
| `saved_at` | string（RFC3339） | 可选 | 默认取本机时间 |
| `target_domain` | string | 可选 | 收件区条目字段（**白名单用法**，不写进原文 frontmatter）；缺省 → 取 `plan.domain` |
| `tags` | string[] | 可选 | 标签 |

```json
{"op": "add_source", "url": "https://example.com/attention",
 "title": "Attention 机制入门", "body": "正文…\n", "reason": "补齐注意力机制的基础材料",
 "saved_at": "2026-09-01T10:00:00+08:00", "target_domain": "ai-infra"}
```

### 3.2 `write_note`（材料笔记五分区 + 收件区条目移出）

| 字段 | 类型 | S1 必填性 | 说明与分级 |
|-|-|-|-|
| `op` | string | **必填** | 固定 `write_note` |
| `source` | string（`s-…`） | **必填** | 笔记 frontmatter 的 `source`（单值）；不可解析 → **E2** |
| `note_id` | string（`n-…`） | 可选 | 省略时按日期 + 标题生成；格式非法 → **E1**；全库重复 → **E1** |
| `title` | string | 可选 | 笔记标题（H1 之外的展示名，供 slug 用） |
| `domain` | string | 可选 | 默认取 `plan.domain`；落在 `plan.domain` 之外 → **W1** |
| `tags` | string[] | 可选 | 标签 |
| `sections` | object（分区名 → 文本） | **必填**（至少「材料提炼」） | 键只能是笔记五分区名；「用户补充」出现即 **E6**；未知分区名 → **I1 info**（原样忽略） |
| `output_cards` | object[]（`card` + `mode`） | 可选 | 写成 `- k-xxx（新建｜复用｜补充）`；本次加工快照，此后不回写 |
| `coverage_gaps` | string[]（**受控枚举**，见 §3.2.1） | **可选** | 元素为**受控枚举七值**；缺该字段或空数组 = **无缺失**，不报错、不告警 |
| `reprocess` | bool | 可选（默认 `false`） | 笔记已存在时默认**复用不重写**；仅此字段为真才允许重新加工（B2 逐字保留用户块） |

```json
{"op": "write_note", "source": "s-20260901-attention", "note_id": "n-20260901-attention",
 "domain": "ai-infra",
 "sections": {"材料提炼": "- 原文主张…\n", "Agent 分析": "- 该主张的适用面…\n"},
 "output_cards": [{"card": "k-20260901-attention", "mode": "新建"}],
 "coverage_gaps": ["counterexample", "limitation"]}
```

#### 3.2.1 `coverage_gaps` 受控枚举七值（EG-EXT-02 在合同侧的唯一落点）

`coverage_gaps` **独立成行、可选、元素为受控枚举**：由 **Agent 自评**「本篇原文未表达、因此笔记中
留空」的提炼覆盖要点。七个取值与 §14 EG-EXT-02 的覆盖要点一一对应：

| 枚举值 | 对应覆盖要点（中文） |
|-|-|
| `core_claim` | 核心论点 |
| `key_evidence` | 关键证据 |
| `counterexample` | 反例 |
| `boundary` | 条件与边界 |
| `method` | 方法 |
| `conclusion` | 结论 |
| `limitation` | 局限与不确定性 |

**分级与职责边界（四条，逐字）**

1. **缺该字段或为空数组 → 视为「无缺失」，不报错、不告警**，报告里不出现任何该类条目。
2. 元素取值**不在受控枚举内** → 判为 **I1 info**，**原样保留该值并进报告**，
   不拦截写入、不影响退出码。
3. **不新增任何 error / warning 编号**：本字段的异常一律落在既有 I1 上（§4.5.1 既有编号）。
   升级为拦截项属 S5 严格化，S1 不做。
4. **CLI 只做枚举校验与原样透传，不读正文、不推断缺失项、不补全**：报告侧同样不得自行推断
   或编造缺失项（与「不得输出假数据」同一条底线）。

**职责边界**：`coverage_gaps` 只登记**原文未表达**的要点，**不得**用它标注 Agent 自己偷懒未写的
要点。它与 `plan.reason` 同在 plan 层、互不替代：`reason` 说明「为何采用该处理关系」，
`coverage_gaps` 承担逐项可枚举的缺失标注。

### 3.3 `create_card`（新建知识卡，五分区都可写）

| 字段 | 类型 | S1 必填性 | 说明与分级 |
|-|-|-|-|
| `op` | string | **必填** | 固定 `create_card` |
| `title` | string | **必填** | 卡标题（H1 与 slug 来源） |
| `card_id` | string（`k-…`） | 可选 | 省略时按日期 + 标题生成；格式非法 / 全库重复 → **E1** |
| `domain` | string | 可选 | 默认 `plan.domain`；落在 `plan.domain` 之外 → **W1** |
| `tags` | string[] | 可选 | 标签 |
| `sources` | object[]（四要素） | **必填且非空** | 建卡的硬前提（EG-SRC-04 / V3、V7）：缺失或空数组 → **拒绝建卡**，退 `2` 零写入；元素字段见 §3.5 |
| `sections` | object（分区名 → 文本） | **必填**，「知识内容」必写 | 新建卡五分区都可写，但「用户补充」出现即 **E6**；缺「知识内容」→ 退 `2` |

新卡 frontmatter 由 CLI 生成：`id` / `status: active` / `created_at`（`YYYY-MM-DD`）/
`updated_at`（RFC3339）/ `tags`（可选）/ `sources[]`。**不写**顶层 `domain` / `type` /
`candidate` / `source_check`（黑名单，§7）。

```json
{"op": "create_card", "card_id": "k-20260901-attention", "title": "注意力机制",
 "sources": [{"source": "s-20260901-attention", "note": "n-20260901-attention",
              "rel": "support", "reason": "原文第 3 节给出该机制的定义与推导"}],
 "sections": {"知识内容": "注意力是一种加权聚合…\n", "解释与依据": "- 依据原文第 3 节\n",
              "条件与边界": "- 仅适用于序列建模\n", "理解自检": "- 为什么需要缩放？\n"}}
```

### 3.4 `append_card`（对已有卡只追加三分区）

| 字段 | 类型 | S1 必填性 | 说明与分级 |
|-|-|-|-|
| `op` | string | **必填** | 固定 `append_card` |
| `card` | string（`k-…`） | **必填** | 目标卡 ID；不可解析 → **E2** |
| `sections` | object（分区名 → 文本） | **必填**，至少一项 | **只允许**「解释与依据」「条件与边界」「理解自检」；落在自动路径的「知识内容」或任何时候的「用户补充」→ **E6** |

「理解自检」历史记录块**只追加、永不改写**（EG-CHK-06 属 S1）；当前有效问题块的**替换**
（`replace_block`）属 S2，S1 不实现。

```json
{"op": "append_card", "card": "k-20260901-attention",
 "sections": {"解释与依据": "- 第二篇材料补充了实现细节\n", "条件与边界": "- 长序列下需分块\n"}}
```

### 3.5 `add_material_rel`（材料关系四要素 → 卡 `sources[]`）

| 字段 | 类型 | S1 必填性 | 说明与分级 |
|-|-|-|-|
| `op` | string | **必填** | 固定 `add_material_rel` |
| `card` | string（`k-…`） | **必填** | 关系落在该卡的 `sources[]`；不可解析 → **E2** |
| `source` | string（`s-…`） | **必填** | 四要素之一；写成 `k-…` → **E3** |
| `note` | string（`n-…`） | **必填** | 四要素之一，存**笔记 ID**、**不存路径**（F2） |
| `rel` | string | **必填** | 四要素之一，**封闭三值**：`support` / `against` / `context`；集合外取值一律拒绝 |
| `reason` | string | **必填** | 四要素之一；缺失 / 空 / 等于关系名本身 → **W2 warning**（照写 + 进报告） |

`context` 与 `support` 在查询层**严格分离**，任何「证据」类统计不含 `context`。

```json
{"op": "add_material_rel", "card": "k-20260901-attention", "source": "s-20260902-flash",
 "note": "n-20260902-flash", "rel": "support", "reason": "第二篇材料实测支持该结论"}
```

### 3.6 `add_relation`（论证关系 → 来源卡 `relations[]`）

| 字段 | 类型 | S1 必填性 | 说明与分级 |
|-|-|-|-|
| `op` | string | **必填** | 固定 `add_relation` |
| `from` | string（`k-…`） | **必填** | 记录写入端；不可解析 → **E2** |
| `type` | string | **必填** | **封闭四值**：`derives` / `supports` / `limits` / `opposing`；集合外取值一律拒绝 |
| `target` | string（`k-…`） | **必填** | 关系另一端；写成 `s-…` → **E3**；ID 不存在或格式非法 → **E2** |
| `reason` | string | **必填** | 缺失 / 空 / 等于关系名本身 → **W2 warning** |

`opposing` **单向存储**：按两端稳定 ID **字典序**取小者为 `from`（唯一规范方向），
**写入前同对去重**（已存在则更新 `reason`，不产生第二条）；输入方向不规范或同对重复
→ **W8 warning + CLI 自动规范化并去重**。三方及以上对立只保存实际成立的两两 `opposing`，
不引入议题组、不自动补全图。

```json
{"op": "add_relation", "from": "k-20260901-attention", "type": "limits",
 "target": "k-20260815-rnn", "reason": "该机制限定了原结论的适用序列长度"}
```

### 3.7 `add_open_question`（笔记「存疑与待验证」追加一条）

| 字段 | 类型 | S1 必填性 | 说明与分级 |
|-|-|-|-|
| `op` | string | **必填** | 固定 `add_open_question` |
| `note` | string（`n-…`） | **必填** | 目标材料笔记；不可解析 → **E2** |
| `question` | string | **必填** | 一条存疑文本；空 → **E5**（op 字段不成立） |

存疑**无独立 ID、无独立文件、无状态字段**（EG-KNW-03）：落点就是笔记「存疑与待验证」分区，
既有块字节不变。

```json
{"op": "add_open_question", "note": "n-20260901-attention",
 "question": "该机制在超长序列下的复杂度是否仍可接受？"}
```

---

## 4. 校验分级表

### 4.1 error（**恰 E1–E6**，触发即退 `2`、零写入、无 commit）

| 编号 | 触发条件 | 分级 | CLI 行为 |
|-|-|-|-|
| **E1** | `id` 缺失，或与全库既有产物 `id` **重复** | error | 退 `2`，零写入；诊断带 op 下标 + 字段路径与重复方文件 |
| **E2** | 关系 `target`（或 op 的目标 ID）**无法解析**：ID 不存在于全库 / 格式非法 | error | 退 `2`，零写入 |
| **E3** | `s-` 写进 `relations`，或 `k-` 写进 `sources` | error | 退 `2`，零写入（**代码级硬拦**，不依赖 Agent 自律） |
| **E4** | 目标文件 frontmatter YAML **无法解析** | error | 退 `2`，零写入 |
| **E5** | `plan_version` 不匹配、**未知 op**，或 **op 字段取值组合不成立**（`url`/`title` 同缺、`question` 为空、**`add_relation` 的 `from == target` 自环** —— 自环由 `I-…-010` 登记：它是纯静态 plan 级约束，与 E2/E3/E4 同阶段判定，此前落在 store 写入层被折成 `3` / `partial`，而事实是零写入零 commit） | error | 退 `2`，零写入；未知 op **整条不执行** |
| **E6** | 写入目标落在**自动路径的「知识内容」**，或**任何时候的「用户补充」** | error | 退 `2`，零写入；目标文件字节不变 |

**E3 的两个反例（逐条可测）**

1. 把原文 ID 写进论证关系：`{"op":"add_relation","from":"k-20260901-a","type":"limits","target":"s-20260901-x","reason":"…"}`
   → **E3**，退 `2` 零写入（`relations[]` 只连知识卡）。
2. 把卡 ID 写进材料关系：`{"op":"add_material_rel","card":"k-20260901-a","source":"k-20260815-b","note":"n-20260901-a","rel":"support","reason":"…"}`
   → **E3**，退 `2` 零写入（`sources[].source` 只接受原文 ID）。

### 4.2 warning W1–W8 与 info I1（一律**照写 + 进报告**，不影响退出码）

| 编号 | 触发条件 | 分级 | CLI 行为 |
|-|-|-|-|
| **W1** | 写入目标落在 `plan.domain` 之外（领域越界，EG-DOM-02） | warning | **照常写入** + 进报告 `warnings[]`（带 op 下标与字段路径）；退出码仍为 `0` / `3`。S5 起严格化，S1/M1 不得提前 |
| **W2** | 关系四要素不全，或 `reason` 为空 / 等于关系名本身 | warning | 关系**照常写入** + 进报告；S5 起严格化 |
| **W3** | 论证关系某端不是 `status: active` | warning（**S2 起判定、S5 起 error**） | 照写 + 进报告，不拦截。**S1 无 `deprecate` 命令、无失效卡，正常链路不产生 W3**；S1 只锁定分级与诊断形态，不实现 S2 判定语义 |
| **W4** | 顶层命中**废弃字段黑名单**（按字段路径判定，§7） | warning | 该字段原样忽略，其余照写 + 进报告；S5 起严格化 |
| **W5** | `convergence[]` 缺条目或三维度不齐；**含** `relation` 缺失 / 取值非法 / 与三维度矛盾（§2.1） | warning | 照写 + 进报告，不拦截；不新增编号 |
| **W6** | `base` **未覆盖**某个被改文件 | warning | 进报告 + **跳过该文件**（其余 op 照常执行）；S5 起严格化 |
| **W7** | 状态类 op 的相关告警（**S1 无状态类 op，保留占位**） | warning | S1 正常链路不产生；只锁定分级与诊断形态 |
| **W8** | `opposing` 方向未规范化，或同对重复 | warning | **CLI 自动规范化并去重**（字典序小者为 `from`，已存在则更新 `reason`）+ 进报告；不产生第二条 |
| **I1** | 未知附加字段 / 未知正文分区 / 未知 frontmatter 键被原样保留；`coverage_gaps` 逐项透传；`coverage_gaps` 取值不在受控枚举内；工作区既有改动说明 | info | 原样保留 / 原样透传 + 进报告 `warnings[]`；不拦截、不影响退出码 |

**W5 的专用关系计划例外**：`verb == "relate"` 且 `ops[]` 恰含一个
`add_relation` 的计划由 `eg rel add` 根据用户已经明确给出的 `from` / `type` / `to` /
`reason` 合成，不承载文章加工或候选卡收敛过程，因此 `convergence: []` 不产生
“缺条目”W5。CLI 不得据关系类型反推三维度或伪造条目。若该计划显式携带
`convergence[]`，字段缺失、枚举非法或三维度矛盾仍产生 W5；普通
`process` / `reprocess` 计划的 W5 规则不变。

### 4.3 未编号 warning 登记表（§4.5.1 表**未收录**这些编号）

| 出处 | 触发条件 | 分级 | CLI 行为 | 编号 |
|-|-|-|-|-|
| §4.5 `ops[]` 行 | `ops[]` 为**空列表** | warning | 输出**零写入报告**，退 `0`，不产生 commit | **未编号 warning（§4.5 `ops[]` 空列表）**：§4.5.1 表未收录该编号，**不得复用 `W5`**——W5 是 `convergence[]` 缺条目 |
| §4.5 / CLI 合同 §6 | `verb` 缺失或未知值 | warning | 退化为 `process` 并照常提交 | 未编号 warning（§4.5 `verb` 未知值退化）；同样不得复用任何 W 编号 |

诊断载荷里未编号 warning 的 `code` 字段填 `""`，并在 `message` 中写明出处（CLI 合同 §5）。

---

## 5. 诊断载荷结构

与 CLI 合同 §5 **同一结构**，字段恰六个：`code` / `level` / `path` / `op_index` / `message` /
`target`。**每条 error / warning / info 都必须带 op 下标 + 字段路径**：

- op 级诊断：`op_index` 是 `ops[]` 下标（0 起），`path` 形如 `ops[2].reason`、`ops[0].sources`、
  `ops[0].coverage_gaps`；
- 非 op 级诊断：`op_index` 填 `-1`，`path` 形如 `plan.domain`、`base["k-20260901-x"]`、
  `convergence[0].relation`。

```json
{"code": "W5", "level": "warning", "op_index": -1, "path": "convergence[0].relation",
 "message": "relation 与三维度结论矛盾（三维度全 same 却填 core_change），已照写",
 "target": "k-20260815-rnn"}
```

---

## 6. 前向兼容规则（两条，逐字）

1. **plan 与 op 允许携带未知附加字段**：**原样忽略**，并产出一条 **I1 info**（带字段路径）；
   不拦截、不影响退出码。
2. **未知 `op` 报 error（E5），整条 op 不执行**：不猜语义、不降级为 warning；退 `2` 零写入。

**S2+ op 清单（一律「S1 不实现」）**

| op | 阶段 | S1 口径 |
|-|-|-|
| `replace_block` | S2（安全合并 S5） | **S1 不实现**（未知 op → E5） |
| `set_tags` | S2 | **S1 不实现** |
| `remove_relation` | S2 | **S1 不实现**（S1 只有加、没有删） |
| `reprocess_note` | S2 | **S1 不实现**（重新加工在 `write_note.reprocess` 内表达） |
| `mark_reviewed` | S2 | **S1 不实现**（不写 `reviewed_at`） |
| `deprecate` | S2 | **S1 不实现**（无失效卡） |
| `restore` | S2 | **S1 不实现** |
| `set_replaced_by` | S2 | **S1 不实现** |
| `delete` | S2 | **S1 不实现**（逻辑删除属 S2，物理删除永久废弃） |
| `undelete` | S2 | **S1 不实现** |
| `save_review` | S2 | **S1 不实现**（不写 `domains/<d>/reviews/`） |

---

## 7. 黑名单判定口径（按字段路径，禁止关键词扫描）

**黑名单一律按 YAML / JSON 字段路径匹配**，**明令禁止**对文件或 plan 做关键词扫描——
关键词扫描会把下面这些**合法且必需**的用法误判为废弃设计。

| 白名单用法（字段路径） | 说明 |
|-|-|
| `relations[].type` | 论证关系类型（F4 四值），合法必需 |
| `sources[].rel` | 材料关系类型（F4 三值），合法必需 |
| `plan.domain` | ChangePlan 的本次加工领域，合法必需 |
| `--domain` | CLI flag（`init` / `capture` / `context` / `search`），合法必需 |
| `unprocessed.md` 的 `target_domain` | 收件区条目字段，合法必需 |
| 提案 `type`（S2） | 提案对象自身的字段，合法必需（S1 不产生提案） |

**废弃字段路径（命中 → W4，字段原样忽略，其余照写）**：产物 frontmatter **顶层** `domain` /
`type`、`candidate`、`source_check`、观点倾向 / `lean`、独立未决问题实体、`open/` 目录、
物理（永久）删除、迁移提案、跨领域能力字段（`docs/specs/2026-08-31-evergreen-s1-tech-design.md#dead`）。

---

## 8. `skipped[]` 条目的封闭命名（S1 全库唯一口径）

条目形态 `kind` / `target` / `locator` / `cause` / `detail`（§4.6）。S1 的 `kind` 是
**恰两值的封闭枚举**，`cause` 与 `kind` **一一对应**：

| `kind` | `cause` | 触发 | 产生方 |
|-|-|-|-|
| `file_changed` | `content_hash_mismatch` | 文件自 `eg context` 读取以来变化（B3），**整文件跳过** | `apply` 的 B3 路径（含 `base` 未覆盖的 W6 跳过） |
| `user_block_unsafe` | `user_block_not_preserved` | 重新加工无法**逐字保留**用户块（B2） | `store` 写入路径（`write_note --reprocess` 等） |

- **S1 不出现第三种 `kind`**。
- §4.6 示例中的 `block_conflict` / `block_hash_changed` **属 S2 块替换路径**，S1 不使用。
- **`stale` 一类同义词任何时候不得使用**（下游 015 / 016 / 018 按本表逐值断言，并对该词做 grep 反证）。
- `locator` 是可定位串（如 `domains/ai-infra/knowledge/k-20260901-attention.md#解释与依据`），
  `detail` 是**人类可读文本**（不裸露内部错误码）。

---

## 9. 开工前 checklist（下游 012 / 013 / 014 / 015 / 016 / 017 开工首日逐项确认）

在各自 Activity Log **逐项签字确认且无阻塞项**；出现阻塞项**先回本合同补，再继续实现**。
确认结论与阻塞项汇总记入 T-…-011 的 Activity Log。

| # | 项 | 位置 | 确认口径 |
|-|-|-|-|
| 1 | 顶层键表（恰 8 个） | §1 | 类型 / S1 必填性 / 缺失分级三列齐全，与 §4.5 逐行一致 |
| 2 | 七个 op 字段表 + 最小样例（含 `coverage_gaps`） | §3 | 本 task 要实现 / 消费的 op 字段**全部在表内**，样例可直接当测试输入 |
| 3 | `convergence[]` 字段表与 `relation` 七值 | §2 / §2.1 | 六字段齐全；七值封闭且恰 7 行；`relation` 异常一律 W5 |
| 4 | 校验分级表（E1–E6 / W1–W8 / I1 / 未编号 warning） | §4 | 条目数与 §4.5.1 完全对应；W1/W2/W4/W6 在 S1 一律 warning |
| 5 | `skipped[]` 封闭命名表 | §8 | `kind` 恰两值、`cause` 一一对应、无第三种、无 `stale` |

---

*本合同由 项目维护者 于 2026-09-01 交付（T-evergreen.s1_main_flow-158614-011）。
结构稳定、字段与 op 可增；修改需在本文件与下游 Activity Log 同时留痕。*
