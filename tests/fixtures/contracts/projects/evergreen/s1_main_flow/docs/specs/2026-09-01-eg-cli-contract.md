---
topic: eg CLI 输出与错误码约定（S1 九命令接口合同）
stage: S1
milestone: M-001
task: T-evergreen.s1_main_flow-158614-002
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》§7.1 / §7.2 / §7.3 / §4.6
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
baseline: docs/specs/2026-08-31-evergreen-s1-tech-design.md#ch7
created: '2026-09-01'
updated: '2026-09-01'
updated_by: 项目维护者
---

# `eg` CLI 输出与错误码约定 — Contract（S1）

本文件是 `eg` 的**对外接口合同**：九个命令的签名、`--json` 输出信封、退出码语义、诊断载荷结构。
下游 T-…-003 / 008 / 009 / 010 / 015 / 016 / 017 / 018 一律对着本文写代码；实现与本文冲突时，
**先回本文补合同**，不得在实现里自行发明。

**唯一可执行基线**：`docs/specs/2026-08-31-evergreen-s1-tech-design.md`（§7 CLI 设计 = `#ch7`，
§7.5 收录合同 = `#ch75`，四条安全底线 = `#ch9`，写路径硬约束 = `#write-path`）。
章节号（§7.1 等）是对权威文档的**来源标注**（provenance），本阶段未访问外部链接。

**硬边界（先读）**

1. `eg` 是**确定性本地 CLI**：**不调用任何模型、不做任何网络请求**（§7.5）。抓网页、清洗正文、
   生成 ChangePlan 全部在 Agent 侧。
2. **无确认交互**（EG-CFM-04）：S1 全部命令直接执行，**不做任何二次确认**，因此不存在
   「需确认」这一退出码（见 §3 的 `6`）。
3. **CLI 绝不自选默认领域**（EG-DOM-03）：未配置 `default_domain` 时，除 `init` / `config` 外
   一律提示配置并退 `1`。
4. **字段只增不改**：`--json` 信封与各命令 `data` 的键**只允许新增**，不允许改名或改语义。

---

## 1. 九命令表（逐命令：用法 / 参数 / 是否写入 / 是否产生 commit / 可能退出码）

命令集与 §7.1 表格**逐行对齐，无遗漏无新增**。参数层面唯一超出 §7.1/§7.2 的项是
`apply --dry-run`，其出处与边界见 §2。

| # | 命令 | 是否写入 | 是否产生 commit（`verb`） | 可能退出码 | M1 归属 |
|-|-|-|-|-|-|
| 1 | `eg init` | 是 | 是（`init`） | `0` `1` `4` | M1 实现 |
| 2 | `eg config get\|set` | `get` 否 / `set` 是 | `set` 是（`reconcile`） | `0` `1` `4` | M1 实现 |
| 3 | `eg capture` | 是 | 是（`capture`） | `0` `1` `2` `3` `4` | M1 实现 |
| 4 | `eg context` | 否 | 否 | `0` `1` `2` | M1 实现 |
| 5 | `eg apply --plan <file\|->` | 是 | 是（取 `plan.verb`） | `0` `1` `2` `3` `4` | M1 实现 |
| 6 | `eg search` | 否 | 否 | `1` | **M1 占位**（S1 命令，M2 落地） |
| 7 | `eg card show` | 否 | 否 | `1` | **M1 占位**（S1 命令，M2 落地） |
| 8 | `eg rel`（读）/ `rel add\|remove`（写） | 否（M1 占位） | 否（M1 占位；M2 为 `relate`） | `1` | **M1 占位**（S1 命令，M2 落地） |
| 9 | `eg report --last` | 否 | 否 | `0` `1` | M1 实现 |

**M1 占位口径（三处逐字一致：本合同、T-…-003、`milestones/M-001-m1.md` 差异表 / `EPIC.md` M2 行）**：
`search` / `card show` / `rel`（**读路径与 `rel add|remove` 写路径一并**）在 M1 只注册占位，
被调用即输出「**M1 未实现（S1 命令，M2 落地）**」并退 `1`，**不 panic、不静默成功、零副作用**。
措辞固定，**禁止写成「S1 未覆盖」**——它们都是 §7.1 的 **S1 命令**，只是按 §16.1 划归 **M2 实现**。

### 1.0 全局 flag（所有命令通用）

| flag | 类型 | 默认 | 说明 |
|-|-|-|-|
| `--json` | bool | `false` | 输出 §3 的结构化信封；不加则输出人类可读文本。两套渲染**同源同事实** |
| `--vault <path>` | string | 由 cwd 向上查找含 `evergreen.yml` 的目录 | vault 根（= Git 仓库根） |
| `--help` / `-h` | bool | `false` | 打印该命令用法后退 `0` |

### 1.1 `eg init`

```
eg init [--vault <path>] [--domain <domain>]...
```

| 参数 | 必填 | 默认 | 说明 |
|-|-|-|-|
| `--vault <path>` | 否 | cwd | 要初始化的 vault 根；目录不存在则创建 |
| `--domain <d>` | 否（可重复） | 空 | 初始领域；给出时创建 `domains/<d>/{notes,knowledge}/`，写入 `domains`，首个作为 `default_domain` |

- **写入**：`.git/`、`.gitignore`（内容 `.index/`）、`evergreen.yml`、`SKILL.md`、`unprocessed.md`、
  `sources/`、`domains/<d>/{notes,knowledge}/`（冻结合同 F1 的 `[S1]` 列）。
  **不创建** `reviews/`（S2）、`proposals/`（S2）、`.index/`（S4）。
- **commit**：`verb = init`（出处：§7.1 `eg init` 行「产生 commit」列）。
- **幂等**：对已存在的 vault 重复执行不改任何已有字节；干净工作区下**不产生空 commit**。
- 退出码：`0` / `1`（参数非法）/ `4`（Git 提交失败）。

### 1.2 `eg config get|set`

```
eg config get <key>
eg config set <key> <value>
```

| 参数 | 必填 | 取值 | 说明 |
|-|-|-|-|
| `<key>` | 是 | `default_domain` \| `domains` | 其他键退 `1`；**S1 只此两键** |
| `<value>` | `set` 必填 | 领域名 / 逗号分隔领域列表 | `set domains a,b` 为**并集追加**，不删除既有领域 |

- **写入**：仅 `evergreen.yml`（`version: 1` / `domains: [...]` / `default_domain` 三个顶层键）；
  新增领域时按需创建 `domains/<d>/{notes,knowledge}/`。`evergreen.yml` 是**工程配置而非知识产物**，
  不属于任何领域，纳入 Git。
- **commit**：`get` 无；`set` 产生 `verb = reconcile`（出处：§7.1 `eg config get|set` 行「产生 commit」列）。
- `get default_domain` 在未设置时输出**空值**并退 `0`（不是错误）。
- 退出码：`0` / `1` / `4`。

### 1.3 `eg capture`

```
eg capture --url <url> --title <title> (--body-stdin | --body-file <path>) --reason <text>
           [--domain <d>] [--tag <t>]... [--captured-at <rfc3339>] [--reprocess]
```

| 参数 | 必填 | 说明 |
|-|-|-|
| `--url <url>` | 与 `--title` **至少一个** | 判重第一键：URL 规范化后精确匹配 |
| `--title <t>` | 与 `--url` **至少一个** | 判重第二键：标题精确匹配 |
| `--body-stdin` / `--body-file <p>` | 二选一必填 | 正文字节（Agent 已清洗）；`eg` **不抓取、不解析 HTML** |
| `--reason <text>` | 是 | 收录理由；命中已有原文时**追加**到理由列表 |
| `--domain <d>` | 否 | 目标领域（白名单用法，不写进产物 frontmatter） |
| `--tag <t>` | 否（可重复） | 标签 |
| `--captured-at <rfc3339>` | 否 | 收录时刻（带时区 RFC3339），默认取本机时间 |
| `--reprocess` | 否 | 已有材料笔记时，仅此 flag 允许重新加工 |

- **写入**：`sources/<s-id>.md` + `unprocessed.md` 一条顶层列表项。**commit**：`verb = capture`。
- 判重命中 → 复用、不产生第二份、**正文不覆盖**，`deduped: true`；已有笔记 → `has_note: true` 并提示跳过。
- `data` 键（§7.5，只增不改）：`source_id` / `path` / `deduped` / `has_note` / `note_id` / `commit` / `warnings[]`。
- 退出码：`0`；`1` 参数非法；`2` 正文为空/过短、或 `--url` 与 `--title` 同时缺失（零写入）；
  `3` 部分写入被跳过；`4` Git 提交失败。标题异常 / 领域缺省 / 正文疑似截断 → **warning**，收录照常完成。

### 1.4 `eg context`

```
eg context (--source <s-id> | --note <n-id>) [--domain <d>] [--json]
```

| 参数 | 必填 | 说明 |
|-|-|-|
| `--source <s-id>` / `--note <n-id>` | 二选一必填 | 加工对象 |
| `--domain <d>` | 否 | 限定领域（白名单用法） |

- **只读**：零文件变化、零 commit（EG-VIEW-01）。
- `data` 至少含：候选卡列表、`base`（`id → content_hash`，§4.5 唯一并发保护依据）、
  `default_domain` 与其是否回退。
- 退出码：`0` / `1`（含未配置 `default_domain`）/ `2`（对象不存在或 frontmatter 不可解析）。

### 1.5 `eg apply`

```
eg apply --plan <file|-> [--dry-run] [--json]
```

| 参数 | 必填 | 默认 | 说明 |
|-|-|-|-|
| `--plan <file\|->` | 是 | — | ChangePlan（`-` = stdin）。字段定义见 `2026-09-08-changeplan-contract.md` |
| `--dry-run` | 否 | `false` | 见 §2：**零写入零 commit** 的校验 + 展开预演 |

- **写入**：按 `ops[]` 展开逐文件写入（B1 只追加 / B2 保留用户块 / B3 `content_hash` 校验）。
- **commit**：`verb` 取自 `plan.verb`；未知值 → **warning 并退化为 `process`**（§4.5），不拒绝提交。
- 退出码：`0`；`1` 参数非法；`2` 校验失败（仅 error 级，零写入）；`3` 部分写入被跳过；`4` Git 提交失败。

### 1.6 `eg search`（M1 占位）

```
eg search <query> [--domain <d>]
```

只读命令；M1 被调用即输出「M1 未实现（S1 命令，M2 落地）」退 `1`。参数表按 §7.1 登记，供 M2 直接落地。

### 1.7 `eg card show`（M1 占位）

```
eg card show <k-id>
```

只读命令；M1 行为同 §1.6。M2 落地时输出单卡视图（含 `sources[]` / `relations[]` 与显著标记）。

### 1.8 `eg rel`（M1 占位，读写路径一并）

```
eg rel <k-id>                                  # 读：正向 relations[] + 反向全库扫描
eg rel add <from> <type> <to> --reason <text>   # 写（M2，verb = relate）
eg rel remove <from> <type> <to>                # 写（M2，verb = relate）
```

- `<type>` ∈ `derives` / `supports` / `limits` / `opposing`（冻结合同 F4）。
- **M1 读路径与写路径均不实现**，五种调用形态（`search`、`card show`、`rel`、`rel add`、`rel remove`）
  一律退 `1`，`--help` 中 `rel` 本身与 `add|remove` 处均标注「**M2 落地**」。
  理由：反向关系视图需要 `internal/query` 的全库反查，M1 的 18 个 task 中无 task 承担该实现。

### 1.9 `eg report --last`

```
eg report --last [--json]
```

- **只读**：零文件变化、零 commit。输出最近一次 `apply` / `capture` 的最终报告（§4.6 S1 必填子集）。
- 退出码：`0` / `1`（无历史报告或参数非法）。

> **C2 · I-…-018 现态脚注（2026-09-13，覆盖范围写死，不新增语义）**：本节「最近一次 `apply` / `capture`」
> 是**声明面**，实现侧原先只有 `apply`（及后续 M3/M4 的几条写命令）落 `.eg/last-report.json`，
> `capture` 这一半入口不落，于是一次成功的收录之后 `report --last` 照样退 `1`，提示语还要求用户
> 「先执行一次 `eg apply`」。已在 C2 按**本节声明面**收敛（不收窄合同）。由此固化三条：
>
> 1. **覆盖集合**（产出 §4.6 报告体的写命令，恰这些）：`apply`、`capture`、`delete`、`undelete`、
>    `mark-reviewed`、`proposal new|approve|reject`、`reconcile`。它们的**每一种结局**都留记录
>    （成功 / 退 `3` 跳过或回滚 / 退 `4` Git 失败 / 退 `5` 阻断），记录里的 `exit_code` 与该次进程
>    退出码同源。
> 2. **不刷新集合**（不产出 §4.6 报告体，因此不动这份记录）：`rel add|remove`、`edit`、`deprecate`、
>    `restore`、`replaced-by`、`index build|rebuild|sync`、`init`、`config set`，以及全部只读命令
>    （`check` / `index status` / `bench` / `report` 本身）。**没有第三套口径。**
> 3. **来源如实**：记录里存产出它的命令表面，回放时如实交代（如「复现自最近一次 `eg capture`」）；
>    老记录没有这一键时只说「写命令」，不猜。无历史报告时的提示**不预设**用户上一步做了什么。
>    `capture` 的信封 `data` 键面不受影响（§1.3 的封闭键集里没有 `report`，报告体只进记录）。

### 1.10 只读命令零副作用清单（EG-VIEW-01）

`context`、`search`、`card show`、`rel`（读）、`report` 执行后**零文件变化、零 commit**
（`git status --porcelain` 为空、`git log` 条数不变）。`search` / `card show` / `rel` 的 M1 占位分支
**只写 stderr**，同样受该保证覆盖。

**未配置 `default_domain`**：除 `init` / `config` 外一律提示配置并退 `1`，**CLI 绝不自选默认领域**（EG-DOM-03）。

---

## 2. `apply --dry-run`（M1 工程 flag）

> **§7.1/§7.2 未定义该 flag，属 M1 工程 flag**，**不新增任何产品能力**（不改写入语义、不产生新产物、
> 不引入新 `verb`），只把已有的「校验 + 展开」结果先回给 Agent 看。

三句合同（下游 T-…-015 的实现与 T-…-017 的验收以此为唯一依据）：

1. **语义**：完整走 E1–E6 校验与 op 展开，输出**将**写入的文件与分区清单，**零写入、零 commit**。
2. **退出码语义不变**：校验失败仍退 `2`；展开成功退 `0`。
3. **出处与边界**：§7.1/§7.2 未定义，属 M1 工程 flag，不新增产品能力。

`--dry-run` 的 `data` 增加 `planned[]`（元素：`path` / `section` / `op_index`），
`warnings[]` 与正常路径同源；`git.commit` 输出空值。

---

## 3. `--json` envelope（信封键表，恰五项）

**稳定性承诺：字段只增不改。** 新增键必须向后兼容；已有键**不改名、不改语义、不改类型**。

| 键 | 类型 | 必有 | 说明 |
|-|-|-|-|
| `ok` | bool | 是 | `exit_code == 0` 为 `true`；`3` / `4` 为 `false`（有写入但未完整成功） |
| `data` | object | 是 | 各命令自有载荷（逐命令见 §1）；无载荷时为 `{}` |
| `warnings[]` | array | 是 | warning / info 级诊断条目（结构见 §5）；无则空数组 |
| `exit_code` | int | 是 | 与进程退出码**逐字相同**（§4 五值之一） |
| `status` | string | 是 | 由 `exit_code` **唯一派生**的**信封**字段，取值 `completed` / `partial` / `failed`；**不属 §4.6 报告体** |

`status` 派生表（不携带独立信息、**不得与 `exit_code` 冲突**）：

| `exit_code` | `status` |
|-|-|
| `0` | `completed` |
| `3` / `4` | `partial` |
| `2` | `failed` |
| `1` | `failed`（用法/参数非法，零写入） |

> `status` 是 **CLI 输出信封**字段（§7.1/§7.2 的 CLI 层），技术方案 §4.6 的**报告体**里没有这个键。
> 因此 T-…-016 的「报告体内不得自创键」约束**不因本键被破坏**——报告体是 `data.report`，
> 信封键不下沉进报告体。

错误（error 级）条目放在 `data.errors[]`，结构与 `warnings[]` 同构（§5）。

> **C2 · I-…-016 现态脚注（2026-09-13，纪律不变、只把落点写死）**：本节两句
> （`warnings[]` = warning / info 级；`data.errors[]` = error 级）是**按 `level` 分桶**的
> 唯一口径，实现侧原先把诊断整桶塞进 `data.errors[]`，已在 C2 收敛为逐条按 `level` 投递。
> 由此固化四条读法（合同语义**零变更**，仅消除歧义）：
>
> 1. **失败态也可能有 `warnings[]`**：退 `1`/`2`/`3`/`4`/`5` 都不代表"本次没有 warning"。
>    典型：锁忙路径退 `5` 时，`warnings[]` 承载 N 条 `W28`（等待退避留痕），
>    `data.errors[]` 恰一条 `E16`（终态）。接入方**不得**用「有 error 就不读 warnings」的写法。
> 2. **反向亦封闭**：`data.errors[]` 内不出现任何非 error 级条目；`warnings[]` 内不出现 error 级条目。
> 3. **不留空桶**：若本次无 error 级条目，则不产出 `data.errors` 键（而不是给一个空数组，
>    否则「键在场」会被误读成「有错误」）。`warnings[]` 是信封必备键，无 warning 时仍为空数组。
> 4. **桶内原序**：分桶只改去处，不改条目内容、条目数与相对顺序；信封仍恰五键。

> **C2 · I-…-017 现态脚注（2026-09-13，「必有」二字的适用范围写死）**：本节「必有」列全为「是」，
> 且 `status` 派生表把退 `1`（用法 / 参数非法）列为有信封的一档 —— 合同**没有**给出
> 「参数解析失败时不输出信封」的例外。实现侧原先只在**全局位**解析到 `--json` 时才走信封渲染器，
> 子命令位的 `--json`（§1 各命令用法行与 `<cmd> --help` 的写法，如 `eg index build [--json]`）
> 一旦伴随别的非法 flag 就会退化成人类可读文本；已在 C2 收敛。由此固化三条（合同语义**零变更**）：
>
> 1. **位置等价**：`eg --json <cmd> …` 与 `eg <cmd> … --json` 对同一 argv 给出**逐字一致**的信封。
> 2. **解析层同样有信封**：未知 flag（两个位置）、子动词非法、缺必需子命令、未知命令这四类
>    退 `1` 的失败，`--json` 在场时都输出五键（`exit_code: 1` / `status: "failed"` /
>    `data.errors[]` 携编号，命令层编号见 §5 脚注）。
> 3. **不是"恒 JSON"**：未表达 `--json`（含显式 `--json=false`）时输出仍为人类可读面 ——
>    输出格式由调用方意图决定，不由失败发生在哪一层决定。

---

## 4. 退出码表（S1 恰五条）

| 码 | 含义 | 磁盘/提交后果 |
|-|-|-|
| `0` | 成功 | 按命令语义写入并提交（只读命令零变化） |
| `1` | 用法 / 参数非法 | **零写入** |
| `2` | 校验失败（**仅 error 级**） | **零写入** |
| `3` | 部分写入被跳过 | 已完成写入**保留并提交**，链路继续 |
| `4` | Git 提交失败 | **磁盘保留当前状态，不做破坏性还原**（B4） |

**`5` 属 S5（锁与写前复核）、`6` 属 S2（需确认），S1 不实现、不出现。** 本合同不为它们预留任何
参数或字段，也不把它们表述为已提供的能力。

---

## 5. 诊断载荷结构（error / warning 条目）

每个条目**必须**带 **op 下标 + 字段路径**，Agent 拿到后能定位并重投：

| 字段 | 类型 | 说明 |
|-|-|-|
| `code` | string | 分级编号：`E1`–`E6` / `W1`–`W8` / `I1`；未编号 warning 填 `""` 并在 `message` 说明 |
| `level` | string | `error` / `warning` / `info` |
| `path` | string | 字段路径，形如 `ops[2].reason`、`base["k-20260901-x"]`、`convergence[0].relation` |
| `op_index` | int | op 下标（非 op 级诊断填 `-1`） |
| `message` | string | 人类可读说明 |
| `target` | string | 受影响的 ID 或文件路径（可空） |

**error 样例**（退 `2`，零写入）：

```json
{"code": "E3", "level": "error", "op_index": 2, "path": "ops[2].target",
 "message": "原文 ID s-20260901-foo 不能写进 relations[]（论证关系只连知识卡）",
 "target": "s-20260901-foo"}
```

**warning 样例**（不拦截，进报告）：

```json
{"code": "W6", "level": "warning", "op_index": 2, "path": "ops[2].reason",
 "message": "base 未覆盖 k-20260901-bar，已跳过该文件；reason 保留原样",
 "target": "k-20260901-bar"}
```

> **阶段脚注（A-29，T-…-046 追加；上表 S1 结论原句逐字保留、不改写）**：上表 `code` 一行给出的
> 是 **S1 阶段**的取值域。S2 / M3 落地后，实现侧的分级编号在其上**只增不改**，追加
> `E7`–`E10` 与 `W9`–`W12`（例如 `E10` 替代指针指向已逻辑删除对象、`W9` 提案十项必备缺项、
> `W10` `rel remove` 未命中的幂等 no-op、`W11` 状态类幂等 no-op、`W12` 替代指针指向 deprecated 卡）。
> S1 已有编号的语义**一个不改、一个不删**。取值域的完整口径以
> `2026-10-10-m3-user-authorization-contract.md` 与 `2026-10-10-m3-proposal-state-contract.md` 为准。
> **载荷结构本身不变**：字段集合仍恰是上表这六行（`code` / `level` / `path` / `op_index` /
> `message` / `target`），S2 / M3 **不新增任何字段**，未编号 warning 仍填 `""`。

> **现态脚注（C2 · I-…-015 追加；上表与上一条脚注的原句逐字保留、不改写）**：上表「未编号 warning
> 填 `""`」这一句是**只对 warning 开放**的授权，实现侧曾把它误读成「`code` 可以缺省」，导致命令层
> 37 处 **error 级**条目 `code=""`、另有 3 处把 `report.skipped[].cause` 的枚举值
> （`content_hash_mismatch`）塞进 `code` 位。现态收敛（**只增不改**，A-29）：
>
> 1. **error 级条目的 `code` 恒非空**，且恒落在 `^(E|W|I|Q)\d+$` 编号域内；`code=""` **只对
>    未编号 warning** 保留（该授权原样有效，不得被「顺手补码」抹掉）。
> 2. 命令层追加 **`E17`–`E25` 恰九个**编号，落点封闭在 `internal/cli/codes.go` 一个文件：
>    `E17` 用法 / 参数非法 · `E18` 前置事实不成立 · `E19` 授权或确认门禁未满足 ·
>    `E20` 权威产物读取 / 解析失败 · `E21` 执行未能完成（零权威写入） · `E22` Git 提交失败 ·
>    `E23` 部分成功汇总 · `E24` 本次跳过未写入（B2/B3） · `E25` 未分类错误（实现 bug 信号）。
>    既有 `E1`–`E16` / `W1`–`W28` / `I1` / `Q1`–`Q5` 的语义与落点**一格未动**。
> 3. **`code` 与 `exit_code` 解耦**：`code` 表达「要改什么」，`exit_code` 表达「有多严重 / 盘面
>    什么状态」。同一 `E17` 可伴随退 `1`（多数）或退 `2`（§1.3 把 `eg capture` 的
>    `--url`/`--title` 同时缺失指派为校验失败）；同一退 `2` 也可能是 `E18` / `E19` / `E20`。
>    退出码的唯一翻译点仍是 `ExitCodeFor`，**不得**由 `code` 反推。
> 4. **`kind` / `cause` 枚举不得占用 `code` 位**：B2 / B3 跳过的 `code` 为 `E24`，
>    `report.skipped[].kind` / `.cause` 的枚举值**逐字保留在原处**，并在同条 `message` 里可读。
> 5. **同一次失败只登记一次**：错误自带 error 级诊断时，信封不再追加空码的顶层孪生条目
>    （原实现会给出 `["E2", ""]` 这种一实一空的成对条目）。
>
> **载荷结构仍不变**：字段集合仍恰是上表六行，本次**不新增任何字段**，只收敛 `code` 位的取值纪律。

> **现态脚注（C2 · I-…-025 追加；上表与前两条脚注的原句逐字保留、不改写）**：上表 `path` 与 `target`
> 两行的**分工**在事务扫描阻断态（多于 1 个未闭合事务，或存在损坏事务）上曾被实现侧混用 ——
> 把 `txn_id` 塞进 `path` 位，且只报计数不报是哪几个。现态收敛（**只增不改**；不新增字段、不新增
> 编号、不新增命令）：
>
> 1. **`path` 恒是路径**（字段路径或仓内文件路径）：事务类诊断的 `path` 写 `.index/txn/<txn_id>`
>    这一**真实存在的目录**；**`target` 恒是对象标识**：事务类诊断的 `target` 写 `<txn_id>` 本身。
>    `txn_id` **不得**占用 `path` 位。**机器读法唯一取 `target`**，不得解析 `message` 文案。
> 2. **阻断态必须逐个可枚举**：每个问题事务各出一条诊断（`target` = 该 `txn_id`），损坏事务的
>    `message` 必须给出**不可解析原因**（JSON 非法 / 缺必需字段 / `journal_version` 不支持 /
>    标记形态非法等）；另有一条**总述**条目在 `message` 中列出**全部**问题 `txn_id` 与计数。
>    只报「N 个未闭合事务」而不给 `txn_id` 的形态**不再合规**。
> 3. **A 类写命令口径不变**：仍恒退 `5` + `E15`、权威零写入（M6 合同 §18.4 / §12），本次只补齐
>    该批 error 条目的 `target` 与原因文案，**退出码域与编号域一格未动**。
> 4. **C 类只读命令口径不变**：`eg check` / `eg check --strict` 在阻断态**仍退 `0`/`1`/`2` 三档、
>    仍不产 error、仍不退 `5`**（M6 合同 §18.1、§4.1.2），阻断事实以**未编号 warning**
>    （`code=""`，授权见 I-…-015 脚注第 1 条）落在顶层 `warnings[]`，含上条同样的 `target` /
>    原因 / 全量 `txn_id` 列表，并追加一条**人工出路**说明。`data.check` 的键面**守恒**，
>    可诊断性**不靠新增 `data` 键**换取。事务态披露在**默认面**就给，`--strict` 至少同样多。
> 5. **不提供清理命令**：M6 命令数恒 22（M6 合同 §13.4），人工处置步骤（备份 → 删除多余
>    未闭合 / 损坏事务目录 → 下一条写命令自动恢复留痕 `W26`）与其风险（丢弃前像副本后
>    已写出的字节无法自动回滚）落 README / `skill/SKILL.md` / `eg check --help` 三面。
>
> **载荷结构仍不变**：字段集合仍恰是上表六行，本次**不新增任何字段**，只收敛 `path` / `target`
> 两位的分工与阻断态的披露完备性。

---

## 6. S1 `verb` 集合 + 出处

M1 会产生的 commit `verb` **恰五个**；`relate` 单列为 M2。实现者**不得自造动词**。

| `verb` | 出处（技术方案） | 由谁产生 |
|-|-|-|
| `init` | §7.1 `eg init` 行「产生 commit」列 | `eg init` |
| `reconcile` | §7.1 `eg config get\|set` 行「产生 commit」列 | `eg config set` |
| `capture` | §7.1 `eg capture` 行 | `eg capture` |
| `process` | §4.5 plan 示例 `"verb": "process"`；也是 `verb` 未知值时的**退化目标** | `eg apply` |
| `reprocess` | §14 EG-NOTE-05 行落点 `verb: reprocess` | `eg apply`（重新加工） |
| ~~`relate`~~ | §7.1 `eg rel` 行 | **M2**（`rel add\|remove`，M1 不产生） |

**`reconcile` 消歧（逐字口径）**：commit verb `reconcile` ≠ S3 `eg reconcile` 命令 ≠ §4.6 报告字段
`reconcile`。三者同名但分属三层：前者是 §7.1 规定的 `eg config set` 提交动词（**S1 就有**）；
`eg reconcile` 是 S3 的外部编辑对账命令（**S1 不注册、`eg --help` 中不出现**）；
`reconcile: {"ran": false}` 是 §4.6 报告字段（**S3 才计算**）。

**提交信息形状**：主题 `<verb>(<domain>): <subject>`；正文追加 `Reason:`（取 `plan.reason`）与
`Requirement:`（取 `plan.requirement_ids`，缺失 → warning 且不输出该行）。
`eg apply` 的 `verb` 取自 `plan.verb`，未知值 → warning 并退化为 `process`（§4.5）。
`domain` 为 `null`（全局操作，如 `evergreen.yml`）时主题域位写 `-`
（**权威未定义该占位形态，属 M1 工程约定**，登记为待对齐项）。

---

## 7. 显著标记口径

| 标记 | 触发 | 阶段 |
|-|-|-|
| `[失效]` | `status == deprecated` → 输出 `deprecated: true` | **S1 可用** |
| `[已删除]` | `deleted_at` 非空 | S2，S1 不输出 |
| `[未过目]` | `reviewed_at` 缺失 | S2，S1 不输出 |
| `[材料支持不足]` | 材料支持度判定 | S3，S1 不输出 |

---

## 8. 开工前 checklist（下游 003 / 008 / 009 / 010 / 015 / 016 / 017 开工首日逐项确认）

在各自 Activity Log 逐项签字确认且无阻塞项；出现阻塞项**先回本合同补，再继续实现**。

| # | 项 | 位置 | 确认口径 |
|-|-|-|-|
| 1 | 九命令参数表 | §1.1–§1.9 | 本 task 要注册 / 实现的命令与参数**全部在表内**，无需自行发明 |
| 2 | `--json` envelope 五键表 | §3 | `ok` / `data` / `warnings[]` / `exit_code` / `status`；`status` 由 `exit_code` 派生、不进 §4.6 报告体 |
| 3 | 退出码五条表 | §4 | `0`/`1`/`2`/`3`/`4`；`5` 属 S5、`6` 属 S2，不实现不出现 |
| 4 | S1 `verb` 集合表与 `reconcile` 消歧句 | §6 | 五个 verb + `relate`(M2)；消歧句逐字复述 |
| 5 | 只读命令零副作用清单 | §1.10 | `context`/`search`/`card show`/`rel`(读)/`report` 零文件变化零 commit |

确认结论与阻塞项汇总记入 T-…-002 的 Activity Log。

---

*本合同由 项目维护者 于 2026-09-01 交付（T-evergreen.s1_main_flow-158614-002）。字段只增不改；
修改需在本文件与下游 Activity Log 同时留痕。*

---

## 附录 · 阶段口径消歧登记（2026-09-18 补记）

> 本附录**只做登记与裁决说明，不改写正文任何历史结论**。正文写于 2026-09-01（M1 期），
> 其阶段归属表述与后续冻结的 M2 范围存在一处冲突，特此登记。
> **凡正文（含 §1 九命令表第 8 行、§1.8、§6 `verb` 出处表）中关于「关系删除」调用形态的阶段归属表述，
> 一律以本附录的裁决为准。**

### 1. 原文表述位置与原话（逐字引用，不做修改）

| 位置 | 原话（节选逐字） |
|-|-|
| 第 51 行（§1 九命令表 第 8 行） | 「`eg rel`（读）/ `rel add\|remove`（写）」…「**M1 占位**（S1 命令，M2 落地）」 |
| 第 55 行（§1 表后说明） | 「`search` / `card show` / `rel`（**读路径与 `rel add\|remove` 写路径一并**）在 M1 只注册占位」 |
| 第 180 行（§1.8 用法块） | 「`eg rel remove <from> <type> <to>` # 写（M2，verb = relate）」 |
| 第 184 ~ 185 行（§1.8 说明） | 「**M1 读路径与写路径均不实现**，五种调用形态（`search`、`card show`、`rel`、`rel add`、`rel remove`）一律退 `1`」 |
| 第 309 行（§6 `verb` 出处表） | 「~~`relate`~~ \| §7.1 `eg rel` 行 \| **M2**（`rel add\|remove`，M1 不产生）」 |

以上五处的共同问题：把「关系删除」这一调用形态与 `rel add` 一并记为 **M2 落地**。

### 2. 权威文档内部的冲突（飞书《[技术方案]常青（Evergreen）v1 技术方案》）

- **§7.1 CLI 命令表**：把整条 `eg rel`（含两种写形态）记为 **S1**——正文第 51 / 180 / 309 行即据此撰写。
- **§4.5 ChangePlan 操作表**：`add_relation` 记 **S1**，而承载关系删除语义的那条 op（`remove_` 前缀那条）记 **S2**。
- 两处对同一能力给出不同阶段：§7.1 的 CLI 形态是 S1，而它唯一可依赖的写入 op 是 S2 ——
  按 M1 已冻结的写入链路（CLI → ChangePlan → executor → 一次 commit），**CLI 形态不可能早于其 op 落地**，
  因此 §7.1 与 §4.5 不可同时成立。

### 3. 本次裁决（2026-09-18，随 M2 范围冻结一并生效）

| 能力 | 阶段归属 | 说明 |
|-|-|-|
| `eg rel <k-id>` 正向 + 反向查询 | **M2 / S1** | 全库扫描反查，只读、零写入、零 commit |
| `eg rel add <from> <type> <to>` | **M2 / S1** | 走既有 ChangePlan `add_relation` + 一次 `relate` commit |
| 关系删除的 CLI 形态与其对应 op | **M3 / S2** | M2 **不实现**；M2 完成后该形态仍必须明确返回「M3/S2 未实现」，且**零写入、零 commit** |

### 4. 裁决依据

1. **以 §4.5 的 op 阶段为准**：写入能力的阶段下界由其 ChangePlan op 决定，op 是 S2 → CLI 形态不得早于 S2。
   §7.1 属命令清单层面的粗粒度标注，粒度低于 §4.5，冲突时以细粒度为准。
2. **与 §16.1 的 M2 范围一致**：§16.1 的 M2 条目为「`eg context` 打磨、`convergence[]` 收敛体验、
   `search` / `card show` / `rel` 基础查询」，并未列出关系删除。
3. **本仓落地文档**：`milestones/M-002-m2.md` 的「`rel remove` 阶段裁决（P0 消歧）」节给出同一结论，
   并把本合同正文的相关表述登记为待修正项 **A-7**；`EPIC.md` 的里程碑表已按本裁决改正 M2 范围。
4. **可机器复核**：M2 完成判据 2 规定——该形态的调用须满足「退出码 == 合同规定值 + stderr 含
   『M3/S2 未实现』+ `git status --porcelain` 为空 + `git log` 条数不变」，由 `T-…-024` 的
   `evergreen/test/e2e/m2_rel_add.sh` 断言。

### 5. 效力范围

- 本附录**不改动**正文任何字符：正文第 51 / 55 / 180 / 184 ~ 185 / 309 行原文原样保留，作为 M1 期历史结论留痕。
- 阅读顺序：**正文 → 本附录**；两者冲突时以本附录为准。
- 后续 M2 查询与关系写入合同（`docs/specs/2026-09-19-m2-query-contract.md`，`T-…-019` 交付）
  必须与本附录逐条一致；若两者再出现分歧，以本附录 + `M-002-m2.md` 的裁决为准，并在两处同时补记。

*本附录由 项目维护者 于 2026-09-18 补记（随 M2 规划落地）。只增不改；正文历史结论不回改。*
