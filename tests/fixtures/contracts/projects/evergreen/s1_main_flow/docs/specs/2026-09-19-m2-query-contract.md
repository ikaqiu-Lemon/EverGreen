---
topic: M2 查询与关系写入合同（search / card show / rel 查询 / rel add / Q 系列诊断）
stage: S1
milestone: M-002
task: T-evergreen.s1_main_flow-158614-019
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》§7.1 / §7 / §6 / §4.5 / §16.1
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
baseline: docs/specs/2026-08-31-evergreen-s1-tech-design.md#ch7
created: '2026-09-01'
updated: '2026-09-01'
updated_by: 项目维护者
---

# M2 查询与关系写入 — Contract（S1 · 里程碑 M2）

本文件是 **M2 的唯一接口合同**：把「查询看到什么、关系怎么写、扫不动的东西怎么诚实说出来」
一次定死。下游 T-…-020 / 021 / 022 / 023 / 024 / 025 一律对着本文写代码；实现与本文冲突时
**先回本文补合同**，不得在实现里自行发明字段、编号或排序规则。

**姊妹合同（本文不重复其内容，只引用）**

- `docs/specs/2026-09-01-eg-cli-contract.md`：§1.6 / §1.7 / §1.8 的 M1 占位参数表、§1.10 只读命令
  零副作用清单、§3 `--json` 信封五键、§4 退出码五值、§5 诊断载荷结构。
- `docs/specs/2026-09-08-changeplan-contract.md`：§1 顶层八键、§3.6 `add_relation` 字段表与
  `opposing` 单向存储、§4.2 的 W8。
- `milestones/M-002-m2.md`：M2 六项实做范围、9 条完成判据、阶段裁决、待修正项 A-6 / A-7、风险 R-1。

**硬边界（先读）**

1. `eg` 是**确定性本地 CLI**：不调用任何模型、不做任何网络请求。查询命令的匹配与排序全部是
   确定性算法——**同一语料两次执行输出逐字相同**（含顺序、得分、诊断条目与其次序）。
2. **M2 仍是 Markdown 直接扫描**：不建索引目录、不引 SQLite / FTS5（均属 S4，本合同**不做**）。
3. **字段只增不改**：本合同新增的 `data` 键此后只允许新增，不改名、不改语义、不改类型；
   `--json` 信封仍是五键，**不新增第六个信封键**。
4. **写路径唯一通道仍是 ChangePlan**：`eg rel add` 组装 plan 走 `internal/plan` → `internal/store`，
   **禁止绕过 ChangePlan 直接调 store**。
5. 六条冻结合同 F1–F6、ID 与关系枚举、五分区语义在 M2 不可变。

---

## 1. `eg search` — 输出 schema、过滤与四级稳定排序

### 1.1 用法与参数表（EG-VIEW-02，§14 追溯矩阵阶段列 = **S2**，M2 提前落地）

```
eg search <query> [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--json]
```

| 参数 | 必填 | 默认 | 说明 |
|-|-|-|-|
| `<query>` | 是 | — | 关键词串；空串 / 只含空白 → 退 `1`，零输出内容 |
| `--domain <d>` | 否 | 全库 | 限定领域；值不在 `evergreen.yml` 的 `domains` 内 → 退 `1`（CLI 绝不自选领域，见 CLI 合同 §1.10 末段） |
| `--tag <t>` | 否（**可重复**） | 无 | 标签过滤；多次给出为 **AND**（卡必须同时含全部标签），比较为逐字相等 |
| `--since <YYYY-MM-DD>` | 否 | 无下界 | `updated_at` 的**日期部分** ≥ 该值（闭区间） |
| `--until <YYYY-MM-DD>` | 否 | 无上界 | `updated_at` 的**日期部分** ≤ 该值（闭区间） |
| `--json` | 否 | `false` | 输出 §3 信封五键 |

`--since` / `--until` 的日期非法或 `--since > --until` → 退 `1`。**「非法」含两层**
（I-…-011 收敛）：① **形态**不是恰 `YYYY-MM-DD`（十位、连字符在第 5 / 8 位、其余全数字；
缺前导零的 `2026-9-1`、`20260901`、`2026/09/01`、带时刻值一律不收）；② 形态过关但
**日历上不存在** —— 月份须 `01`–`12`、日须在该年该月的实际天数内，闰年按公历真实规则
（4 年闰 / 百年不闰 / 400 年再闰）判定：`2024-02-29`、`2000-02-29` 合法，
`2025-02-29`、`2100-02-29`、`2026-02-30`、`2026-04-31`、`2026-13-45`、`0000-00-00`、
`9999-99-99` 非法。日历非法值**绝不**允许静默参与 §1.4 的逐字字符串比较后退 `0` ——
那会让调用方把「日期拼错」误读成「库里没有符合条件的卡」。语义合理性不做推断：
`1900-01-01`、`2999-12-31` 是真实日期，照收。同族参数 `eg unreviewed --since/--until`
共用同一实现（`query.ValidDay`），两条命令判定逐字一致。
搜索面 = 知识卡（`domains/<d>/knowledge/**.md`）。材料笔记与原文**不进 `hits[]`**
（材料层产物不参与知识收敛，见 ChangePlan 合同 §3.5；M2 不提供 `--kind` 开关）。

### 1.2 `data` 结构（键序即下表次序）

```json
{
  "hits": [
    {"id": "k-20260815-rnn", "title": "RNN 的长序列衰减", "domain": "ai-infra",
     "tags": ["rnn", "序列建模"], "status": "active", "deprecated": false,
     "updated_at": "2026-09-12T10:00:00+08:00", "created_at": "2026-08-15",
     "path": "domains/ai-infra/knowledge/k-20260815-rnn.md",
     "matched_fields": ["title", "tags"], "score": 5}
  ],
  "total": 1,
  "scanned_files": 12,
  "skipped_files": 1
}
```

| 键 | 类型 | 说明 |
|-|-|-|
| `hits[]` | object[] | 命中卡列表，元素键恰为下表九项 + `score` |
| `hits[].id` | string | 卡稳定 ID（`k-…`） |
| `hits[].title` | string | 卡标题（frontmatter `title`，缺失时退化为 `id`） |
| `hits[].domain` | string | 所属领域（由目录决定） |
| `hits[].tags` | string[] | 标签；无则空数组（不是 `null`） |
| `hits[].status` | string | `active` / `deprecated`（F3 封闭二值） |
| `hits[].deprecated` | bool | `status == "deprecated"` 的派生值，不携带独立信息 |
| `hits[].updated_at` | string | frontmatter 原值 |
| `hits[].created_at` | string | frontmatter 原值 |
| `hits[].path` | string | vault 内相对路径（`/` 分隔） |
| `hits[].matched_fields[]` | string[] | 命中字段，取值 ∈ `title` / `tags` / `body`，按该固定次序去重输出 |
| `hits[].score` | int | §1.3 的匹配分（写出来是为了让排序可被人工复算） |
| `total` | int | `len(hits)`；**不截断、不分页**，两值恒等 |
| `scanned_files` | int | 本次扫描读取的 `.md` 文件数 |
| `skipped_files` | int | 因不可解析被跳过的文件数（每一个都有一条 `Q1`，见 §5） |

人类可读输出每行一条命中：`<标记><id>  <title>  [<domain>] <tags>  <updated_at>`，
其中 `<标记>` 仅在失效卡时为 `[失效]`。

### 1.3 匹配分计算规则（定死，无随机、无时间因素）

1. **切词口径**：查询串与被检字段一律先归一——ASCII 字母折小写、全角 ASCII 折半角；
   ASCII 字母数字连续段为一个词；非 ASCII（中文）按**相邻两字**切二元组（单字自成一词）。
   该口径与 `internal/query` 现有 `tokens()` 同源，**不另起一套**。
2. **逐词计分**（每个查询词对每个字段至多计一次）：命中 `title` **+3**；命中 `tags` 任一项 **+2**；
   命中正文（五分区全文）**+1**。
3. **匹配分** = 各查询词得分之和。分 `== 0` 的卡不进结果集。
4. 分值不随执行时间、文件顺序、机器环境变化；**无衰减、无随机、无 IDF 之类语料相关项**。

### 1.4 四级全序排序键

结果集按下列四级键**全序**排列，前一级相等才比下一级：

1. **匹配分降序**（§1.3 的 `score`）；
2. **`updated_at` 倒序**（逐字字符串降序比较，格式统一为 §4.2 的时间戳形态）；
3. **`created_at` 倒序**；
4. **id 升序**（`id` 字段的 ASCII 字典序升序）。

第 ④ 级保证**全序**：`id` 是全库唯一稳定 ID，因此不存在并列，排序结果与输入顺序无关。
**同一语料两次执行输出逐字相同**（下游用 `reflect.DeepEqual` 对两次结果的 ID 序列反证）。
**不做截断、不做分页**（分页参数 `--page` / `--all` 与排序截断属 S4，本合同**不定义**）。

### 1.5 失效卡口径

- **失效卡同等可见**：`status == "deprecated"` 的卡**照常进结果集**，参与同一套排序，不降权、不后置。
- 人类可读输出以 `[失效]` 前缀显著标记，`--json` 置 `deprecated: true`。
- **M2 不提供隐藏失效卡的开关**：没有 `--active-only` / `--include-deprecated` 之类参数，
  也不得用 `--domain` 过滤（`EG-KNW-02` 的 `status='active'` 语义）冒充默认隐藏。
- 其余显著标记（`[已删除]` / `[未过目]` / `[材料支持不足]`）属 S2 / S3，M2 **不输出**。

### 1.6 退出码

`0` 成功（**零命中也退 `0`**，`hits: []` 是合法结果，零知识结果合法）；`1` 参数非法 / 领域未登记 /
未配置 `default_domain`。只读命令**不出现** `2` / `3` / `4`（见 §6）。

---

## 2. `eg card show` — 单卡视图输出 schema

### 2.1 用法（EG-VIEW-07，§14 追溯矩阵阶段列 = **S2**，M2 提前落地）

```
eg card show <k-id> [--json]
```

| 参数 | 必填 | 说明 |
|-|-|-|
| `<k-id>` | 是 | 目标卡稳定 ID；格式非法或**卡不存在** → 退 `1`，**零副作用** |
| `--json` | 否 | 输出 §3 信封五键 |

目标卡的定位走**全库扫描**（不限定领域：卡 ID 全库唯一）。同一 ID 出现在两处 → 取路径字典序
最小者并记一条 `Q1`（如实说明重复），不静默择一。

### 2.2 `data` 结构

```json
{
  "id": "k-20260901-attention", "title": "注意力机制的计算代价",
  "domain": "ai-infra", "status": "active", "deprecated": false,
  "created_at": "2026-09-01", "updated_at": "2026-09-12T10:00:00+08:00",
  "path": "domains/ai-infra/knowledge/k-20260901-attention.md",
  "tags": ["attention"],
  "markers": [],
  "sections": {"知识内容": "…", "解释与依据": "…", "条件与边界": "…",
               "用户补充": "…", "理解自检": "…"},
  "sources": [{"source": "s-20260901-x", "note": "n-20260901-x",
               "rel": "support", "reason": "实测数据支持该结论"}],
  "relations_out": [{"from": "k-20260901-attention", "type": "limits",
                     "target": "k-20260815-rnn", "reason": "限定了原结论的序列长度",
                     "path": "domains/ai-infra/knowledge/k-20260901-attention.md"}],
  "relations_in": [{"from": "k-20260902-flash", "type": "derives",
                    "target": "k-20260901-attention", "reason": "由该结论推出",
                    "path": "domains/ai-infra/knowledge/k-20260902-flash.md"}]
}
```

- `sections` 的**键序固定为五分区声明序**：`知识内容` → `解释与依据` → `条件与边界` →
  `用户补充` → `理解自检`（F5）。值是该分区的**原始文本**（逐字，不做 Markdown 再渲染）。
  分区缺失时键仍在、值为空串，人类可读输出在该分区标注「（本分区缺失）」——
  分区缺失**不属于** Q 系列（文件本身可解析），因此不计入 `skipped_files`。
- `sources[]` 为卡 frontmatter 的四要素材料关系（`source` / `note` / `rel` / `reason`，EG-SRC-01 口径）。
- `relations_out[]` = 本卡 frontmatter `relations[]`（正向）；`relations_in[]` = 全库反向扫描
  （§3.2）。两者元素结构同构，排序同 §3.3。
- `markers[]`：M2 唯一可能取值为 `["[失效]"]`（`status == deprecated` 时）；其余为空数组。

### 2.3 退出码

`0` 成功；`1` 参数非法 / 卡不存在。悬空引用、库内存在不可解析文件时仍退 `0`，
但 `warnings[]` 必须带上对应的 Q 条目（§5）。

---

## 3. `eg rel`（读）— 正向 / 反向输出 schema 与排序

### 3.1 用法（EG-CVG-06）

```
eg rel <k-id> [--to <id>] [--json]
```

| 参数 | 必填 | 说明 |
|-|-|-|
| `<k-id>` | 是 | 起点卡；卡不存在 → 退 `1` |
| `--to <id>` | 否 | 只保留对端 == 该 ID 的条目（正反向同时过滤）；ID 不存在 → 结果为空 + 一条 `Q2` |
| `--json` | 否 | 输出 §3 信封五键 |

`data` = `{"id", "relations_out": [...], "relations_in": [...], "scanned_files", "skipped_files"}`，
元素键恰为 `from` / `type` / `target` / `reason` / `path` 五项。

### 3.2 正向与反向的取数口径

- **正向 `relations_out[]`**：只读本卡 frontmatter 的 `relations[]`，`from` 恒为本卡 ID。
- **反向 `relations_in[]`**：**全库 Markdown 反向扫描**——遍历所有领域的知识卡，凡 `relations[]`
  中 `target == <k-id>` 者产出一条，`from` 为该卡 ID、`path` 为该卡路径。**不走任何索引**
  （M-002 完成判据 4 以 `grep` 反证）。
- `opposing` **单向存储**：库中只有一条记录（规范方向），因此它在规范方向的起点卡出现在
  `relations_out[]`、在另一端出现在 `relations_in[]`；**CLI 不在读路径上补出对称条目**，
  也不改写库内方向。

### 3.3 排序键（两级，稳定确定）

1. **`type` 固定次序**：`opposing` → `limits` → `supports` → `derives`
   （`opposing` 最影响判断，故置首位；该次序**不随字母序变化**）；
2. **对端 ID 升序**（正向比 `target`、反向比 `from`，ASCII 字典序）。

同一 `type` + 同一对端理论上唯一（同对去重，§4.3），故两级即全序；
若语料里仍出现重复条目，则**照实全部输出**（不折叠、不去重）并按 `path` 字典序兜底，
同时记一条 `Q1` 说明重复。多跳展开 `--depth` 属 S2、四级排序 + 20 条截断属 S4：
本合同**只登记不定义**。

### 3.4 退出码

`0` / `1`，口径同 §2.3。

---

## 4. `eg rel add` — 参数 / 校验 / 报告 / commit

### 4.1 用法

```
eg rel add <from> <type> <to> --reason <text> [--domain <d>] [--json]
```

| 参数 | 必填 | 说明与分级 |
|-|-|-|
| `<from>` | 是 | 记录写入端（`k-…`）；ID 不可解析 / 不存在 → **E2**（退 `2`，零写入） |
| `<type>` | 是 | **封闭四值** `derives` / `supports` / `limits` / `opposing`（冻结合同 F4）；集合外一律拒绝，退 `1` |
| `<to>` | 是 | 关系另一端（`k-…`）；写成 `s-…` → **E3**；不存在 / 格式非法 → **E2** |
| `--reason <text>` | 是 | 缺失 / 空 / 等于关系名本身 → **W2 warning**（照写，不拦截） |
| `--domain <d>` | 否 | 只用于 plan 的 `domain` 字段；未给则取 `default_domain`，未配置 → 退 `1` |

分级编号与 ChangePlan 合同 §4 **完全同源**，本合同**不新增任何 E / W / I 编号**。

### 4.2 必须复用现有确定性写入链路

CLI 只做「参数 → plan」的组装，其余一律复用既有实现：

```
eg rel add  →  组装 ChangePlan（verb: relate，单个 add_relation op）
            →  internal/plan 校验（E1–E6 / W1–W8 / I1）与 executor 展开
            →  internal/store 写口（CreateFile / AppendToSection / WriteGuarded）
            →  internal/git 一次 commit（verb = relate）
```

**禁止绕过 ChangePlan 直接调 store**：`internal/cli` 的 `rel add` 实现文件内不得出现任何
`store.` 调用（M-002 完成判据 5 以 `grep` 反证）。组装出的 plan（`--json` 时原样出现在
`data.plan`，便于 Agent 复投）：

```json
{
  "plan_version": 1,
  "verb": "relate",
  "domain": "ai-infra",
  "reason": "该机制限定了原结论的适用序列长度",
  "requirement_ids": [],
  "convergence": [],
  "base": {"domains/ai-infra/knowledge/k-20260901-attention.md": "sha256:<hex>"},
  "ops": [
    {"op": "add_relation", "from": "k-20260901-attention", "type": "limits",
     "target": "k-20260815-rnn", "reason": "该机制限定了原结论的适用序列长度"}
  ]
}
```

**`convergence[]` 适用规则**：上述 plan 是 `eg rel add` 为单一、已明确的关系写入合成的
**专用关系计划**，判定条件固定为 `verb == "relate"` 且 `ops[]` 恰含一个
`add_relation`。该计划不承载文章加工或候选卡收敛过程，因此允许
`convergence: []`，不得仅因缺少逐卡条目产生 W5。

这一例外不授权 CLI 反推或填充 `core_knowledge` / `conditions` / `reuse_purpose`：
从关系类型无法可靠推出三维度，自动填充会制造虚假语义证据。若调用方显式提供了
`convergence[]`，其中字段缺失、枚举非法或三维度与 `relation` 矛盾仍按 ChangePlan
合同产生 W5。普通 `process` / `reprocess` 计划涉及已有卡时也继续执行原 W5 规则。
M3 的专用 `rel remove` 可沿用“专用关系计划不要求 convergence”的原则，但
`remove_relation` 的字段和写入语义仍由 M3/S2 单独定义，本合同不提前扩展。

`base` 由 CLI 自己算（等价于 `eg context` 的口径，`store.ContentHash` 唯一实现）：
`rel add` 是**单命令闭环**，不要求用户先跑 `eg context`；B3 的校验不因此放宽——
写前重算不一致仍然跳过并进 `skipped[]`。

### 4.3 `opposing` 归一与去重（ADR-16 / EG-CVG-05）

- **字典序取小者为 `from`**：`opposing` 单向存储，按两端稳定 ID 字典序取小者为 `from`、
  大者为 `target`，这是唯一规范方向；用户给的方向不规范时 CLI 自动规范化。
- **同对已存在则更新 `reason` 且不产生第二条**。
- 上述两种情形（方向被规范化、同对重复）一律**记 W8**（ChangePlan 合同 §4.2 原有编号，
  本合同不新增分级）。
- 三方及以上对立只保存实际成立的两两 `opposing`，不引入议题组、不自动补全图。

### 4.4 报告与 commit

- `data` 与 `eg apply` **同构**：`data.report` 为 §4.6 报告体，新增关系落在
  `report.relations.knowledge[]`（`from` / `type` / `target` / `reason`），
  `report.git.commit` 为本次 sha，`report.skipped[]` 承载 B3 跳过项。
- **一次 `rel add` = 一次 `relate` commit**（commit subject 以 `relate(` 开头）。
- **无实际改动**（同对已存在且 `reason` 未变）→ **不产生空 commit**，`report.git.commit` 为 `null`，
  并在 `warnings[]` 如实写明「关系已存在且 reason 未变，未写入、未提交」，仍退 `0`。

### 4.5 退出码

| 码 | 触发 |
|-|-|
| `0` | 成功（含「无实际改动、未提交」） |
| `1` | 参数非法（`type` 不在四值内、缺 `--reason`）/ 卡不存在于库 / 未配置 `default_domain` |
| `2` | 校验失败（E 类，**零写入**） |
| `3` | B3 跳过（`skipped[].kind = file_changed`、`cause = content_hash_mismatch`），已写部分保留并提交 |
| `4` | commit 失败——**磁盘保留当前状态，不做破坏性还原**（B4） |

---

## 5. Q 系列只读诊断（不可解析 Markdown 绝不静默跳过）

M1 的 `internal/query` 对 `mdfile.Parse` 失败与 frontmatter 解码失败**一律静默跳过**
（M-002 风险 **R-1**），后果是「缺失结果看起来像完整结果」。M2 用 Q 系列关闭该风险。

### 5.1 Q 码表（恰三条）

| 码 | 触发条件 | 分级 | CLI 行为 | 影响退出码？ |
|-|-|-|-|-|
| `Q1` | 文件不可解析：`mdfile.Parse` 失败 / frontmatter 解码失败 / 缺 `id`；同 ID 重复文件亦记本码 | warning | 跳过该文件但**继续扫描**，计入 `skipped_files`，逐文件一条诊断（带路径与原因） | 否，仍退 `0` |
| `Q2` | 悬空引用：某卡 `relations[]` 的 `target` 在全库中不存在（`--to` 指向不存在的 ID 同理） | warning | 条目**照常输出**（不隐藏），附一条诊断，`message` 必须含被引用的目标 ID | 否，仍退 `0` |
| `Q3` | 结果不完整：本次查询存在 ≥1 条 `Q1` 或 `Q2` | warning | 追加**恰一条**汇总诊断，写明「本次结果不完整」及 Q1 / Q2 条数 | 否，仍退 `0` |

- 每条诊断**必须**带**文件路径**（`path`，vault 内相对路径）与**人类可读原因**（`message`）。
  载荷结构复用 CLI 合同 §5：`code` 填 `Q1` / `Q2` / `Q3`，`level` 填 `warning`，`op_index` 填 `-1`。
- **Q3 只在有 Q1 / Q2 时出现**；无 Q1 / Q2 时 `warnings[]` 里**没有** Q3（反向可判）。
- 诊断次序确定：先按 `code`（`Q1` → `Q2`），同码按 `path` 字典序，最后追加 `Q3`。
- **Q1–Q3 不进 §4.5.1 的 E/W/I 表**：它们只用于**只读查询命令**，不改 ChangePlan 的
  E1–E6 / W1–W8 / I1 分级表，不影响写路径的任何判定，也不参与 `eg apply` 的报告分级。

### 5.2 同源同事实

人类可读输出与 `--json` 的 `warnings[]` **同源同事实**：同一批诊断、同样的路径与原因，
不得只在 `--json` 里出现。人类可读输出的形态：

```
warning [Q1] domains/ai-infra/knowledge/broken.md：frontmatter 不是映射，无法解析，已跳过
warning [Q3] （汇总）本次结果不完整：跳过 1 个不可解析文件、0 条悬空引用
```

**禁止静默跳过**：任何被跳过的文件、任何指不到目标的引用都必须出现在诊断里。
一句话判据——**缺失结果绝不能看起来像完整结果**。

### 5.3 计数守恒

`scanned_files == 进入结果集的文件数 + skipped_files`（同一次扫描内恒等），
这是「没有第三条静默路径」的机器判据。

---

## 6. 只读命令零副作用判据

`search` / `card show` / `rel`（读）/ `context` / `report` 一律**零文件变化、零 commit**
（CLI 合同 §1.10 只读命令零副作用清单）。判据可直接复制执行：

```bash
before_status="$(git -C "$VAULT" status --porcelain)"
before_log="$(git -C "$VAULT" log --oneline | wc -l)"
before_meta="$(find "$VAULT" -name '*.md' -newermt '1970-01-01' -printf '%p %s %T@\n' | sort)"

eg search 注意力 --vault "$VAULT" --json; echo "exit=$?"

[ "$before_status" = "$(git -C "$VAULT" status --porcelain)" ] || exit 1
[ "$before_log" = "$(git -C "$VAULT" log --oneline | wc -l)" ] || exit 1
[ "$before_meta" = "$(find "$VAULT" -name '*.md' -newermt '1970-01-01' -printf '%p %s %T@\n' | sort)" ] || exit 1
```

- `git status --porcelain` 前后逐字相等；`git log --oneline | wc -l` 前后相等。
- vault 内文件**字节与 mtime 不变**（上面的 `find` 快照同时覆盖大小与修改时间）。
- **退出码只可能是 0 / 1**：只读命令**不出现** `2` / `3` / `4`（它们的语义都涉及写入或提交）。
- **有 Q 类 warning 时仍退 `0`**：诊断不改变退出码。
- 下游 021 / 022 / 023 / 025 各自的测试必须有该判据的实证用例（M-002 完成判据 3）。

---

## 7. `eg rel remove` 的 M3/S2 占位行为

```
eg rel remove <from> <type> <to>
```

- M2 完成后该形态**仍返回固定文案「M3/S2 未实现」并退 `1`**，**零写入、零 commit**
  （M-002 完成判据 2；`--help` 文案同步为该口径，由 T-…-024 回改）。
- 本合同**只定义占位行为**，不定义其参数语义、校验规则与写入行为。
- **裁决依据（复述 M-002）**：**§4.5 的 op 阶段归属优先于 §7.1 的命令行归属**——写路径能力
  由 ChangePlan op 决定，删关系只能表达为一个 `remove_relation` op，而该 op 在 §4.5 属 S2，
  故该命令与该 op 一并归 **M3/S2**，M2 **不做**。
- **覆盖声明**：本节覆盖 `2026-09-01-eg-cli-contract.md` §1.8 中把关系删除调用形态标为
  「写（M2）」的旧标注（M-002 待修正项 **A-7**）；该合同正文属历史结论不回改，其文末附录
  「阶段口径消歧登记」已声明附录裁决优先，本节再逐字复述一次。

---

## 8. 与既有合同的关系（envelope / 覆盖 / 越界登记）

### 8.1 envelope

M2 沿用 `--json` 五键信封（`ok` / `data` / `warnings[]` / `exit_code` / `status`）与「字段只增不改」
承诺，**不新增第六个信封键**。Q 系列条目落在 `warnings[]`，与 W / I 条目结构同构（§5.1）。

### 8.2 本合同覆盖的旧标注

| # | 旧标注位置 | 旧表述 | 本合同口径 |
|-|-|-|-|
| 1 | `2026-09-01-eg-cli-contract.md` §1.8 | 关系删除调用形态标「写（M2）」 | 归 **M3/S2**，M2 只留占位（§7） |
| 2 | `2026-09-01-eg-cli-contract.md` §1.6 / §1.7 | `search` / `card show` 为「M1 占位」 | M2 落地为真实实现（§1 / §2） |

### 8.3 越界登记（本合同**不做**、**不定义**）

| 项 | 归属 | 本合同处置 |
|-|-|-|
| 索引目录 `.index/`、SQLite、FTS5、增量索引、`eg bench` | S4 | **不做**、不定义 |
| 分页 `--page` / 排序截断 | S4 | **不做**、不定义 |
| 多跳展开 `--depth` | S2 | 只登记，**不定义** |
| P95 延迟与万卡语料性能门槛 | S4 | **不做**：M2 不设任何性能门槛 |
| `remove_relation` op | M3/S2 | **不做**、不定义（§7） |
| `[已删除]` / `[未过目]` / `[材料支持不足]` 标记 | S2 / S3 | **不做**、不输出 |

---

## 9. 开工前 checklist（T-…-020 / 021 / 022 / 023 / 024 / 025 开工首日逐项确认）

在各自 `Activity Log` 分区逐项签字确认且无阻塞项；出现阻塞项**先回本合同补，再继续实现**。

| # | 项 | 位置 | 确认口径 |
|-|-|-|-|
| 1 | `search` 结果键表 | §1.2 | `hits[]` 元素键 + `total` / `scanned_files` / `skipped_files` 齐全，无需自行发明字段 |
| 2 | `card show` 五分区 + 正反向键表 | §2.2 | `sections` 键序 = 五分区声明序；`relations_out` / `relations_in` 元素键恰五项 |
| 3 | `rel` 排序次序 | §3.3 | `type` 固定次序 + 对端 ID 升序，两级即全序 |
| 4 | `rel add` plan 组装样例 | §4.2 | `verb: relate` + 单个 `add_relation` op；禁止绕过 ChangePlan 直接调 store |
| 5 | Q1–Q3 诊断表 | §5.1 | 三码的触发条件 / 分级 / CLI 行为 / 不影响退出码 |
| 6 | 只读零副作用判定命令 | §6 | 可直接复制执行的三条前后比对 + 退出码只可能是 0 / 1 |

确认结论与阻塞项汇总记入 T-…-019 的 Activity Log。

---

*本合同由 项目维护者 于 2026-09-01 交付（T-evergreen.s1_main_flow-158614-019）。字段只增不改；
修改需在本文件与下游 Activity Log 同时留痕。*
