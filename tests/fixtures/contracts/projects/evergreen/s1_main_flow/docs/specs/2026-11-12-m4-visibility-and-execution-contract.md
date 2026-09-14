---
topic: M4 可见性与执行回执合同（§4.3 不存 SHA 的落地口径 / deprecated 关系端点默认隐藏与显式筛选）
stage: S3
milestone: M-004
task: 未分配（本合同先于 M4 拆分冻结，后续 task 反向引用本文）
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》§4.3 / §4.6 / §5.1 / §5.2 / §7.1 / §7.2 / §11 / §14 / §16.1
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
authority_extracted_at: '2026-11-12'
authority_mutation: 只读，未修改
baseline: docs/specs/2026-10-10-m3-proposal-state-contract.md
created: '2026-11-12'
updated: '2026-11-12'
updated_by: 项目维护者
---

# M4 可见性与执行回执 — Contract（S3 · 里程碑 M-004）

本文件只处理 **owner 于本轮新冻结的两项裁决**，把它们从一句话口径展开为
可施工、可机器校验、且**不误伤既有安全机制**的合同：

- **裁决 ①（放宽合同 §4.3）**：**不持久化该处提出的 SHA 字段**。
  该决定**仅**针对 §4.3 的 SHA，**不得误删 B3 已有的 `content_hash` 并发保护**。
- **裁决 ②（关系端点可见性）**：`deprecated` 关系端点**默认不展示**；
  用户通过**显式筛选** `deprecated` 后才展示。需要在 M4 明确**查询参数、输出合同和回归影响**。

两项裁决关闭的是 M3 验收报告 §F 登记的两条遗留：**K-041-01**（提案脏 diff / 自指哈希张力）
与 **K-043-01**（deprecated 端点展示口径与 M2 冲突）。

## 0. 依据与边界

- 本合同**只加严不放宽**：M1 / M2 / M3 的既有判据、门禁与用例，本合同一字不改；
  一切变更以「M4 侧新增约束 / M4 侧重钉」形态表达。
- 本合同**不裁决**的事项进姊妹合同 `2026-11-12-m4-reconcile-contract.md` §16 的
  **A-32 / A-33** 与本文 §5 的 **A-37 / A-38 / A-39**，由 T-…-048 关闭。
- 「**不改飞书文档**」：权威侧行文与本仓口径的差异只登记，不回写。

### 0.1 修订记录（append-only）

| 日期 | 修订 | 理由 |
|-|-|-|
| 2026-11-12 | 初稿冻结 | owner 两项新裁决落地 |
| 2026-11-12（阶段 B 复核） | ① 新增 §1.4：报告 `proposals[].execution.git_commit` **投影键**的单独口径（三个 A-32 选项下键集合都一字不改）；② §2.3 的 K-041-01 判据从 `cd evergreen && git status` 改为 `git -C "$VAULT"`（原写法检的是源码仓，是**永远为真的假反证**），并新增「commit 数恰 +1」；③ §2.4 反证 ② 的 `-run` 正则实测**只命中 1 个用例**（`TestContentHash` / `TestFileChanged` 根本不存在），改为 6 个实测存在的用例名 + 强制 `^=== RUN` 条数下限；④ **`W21` → `Q4`**（§3.5.1：`W2x` 属 ChangePlan 的 E/W/I 域，只读查询命令用它违反 M2 §5.1）；⑤ §3.5 补 `eg card show` 的 `data` 键集合不扩张；⑥ §3.5.2 补 `Q4` 的 `path` / `op_index` / 条数 / **不触发 `Q3`** / 诊断次序；⑦ 测试矩阵 4 组 → **10 组**（补 G7 flag 不作用面、G8 键集合不扩张、G9 `Q3` 反向可判性、G10 已删除维度未被替换）；⑧ 新增 §4.5「不误伤」反证（裁决二侧）与 §4.6「必然被推翻用例」实测清单（**为空**，但必须复算留痕）；⑨ 新增 **A-39** 登记 | 阶段 B 审计：原文存在 2 处**假反证**（空跑退 0 / 检错仓库）、1 处**违反 M2 冻结合同**（码空间越域）、1 处**键集合口径缺口**（报告投影键） |

---

# 第一部分 · §4.3 不持久化 SHA

## 1. 精确定位：这条裁决动的是**哪一个**字段

### 1.1 §4.3 提出的 SHA = 提案 frontmatter 的 `execution.git_commit`

`docs/specs/2026-10-10-m3-proposal-state-contract.md` **§4.3** 定义
「`execution=failed` 时报告的硬要求」，并在同节说明 `execution` 块原有四字段
`status` / `attempted_at` / `reason` / **`git_commit`**，M3 新增 `written_paths[]` / `unwritten_paths[]`。
**§7.2**（提案落盘形态）把 `execution.git_commit` 写进 frontmatter 键集合。

**这就是本裁决的唯一目标字段。**

### 1.2 代码使用面（2026-11-12 在 evergreen@`0277b58` 上 grep 实测，非推断）

| 文件 | 位置 | 角色 |
|-|-|-|
| `internal/proposal/schema.go` | `KeyGitCommit = "git_commit"`（:134）、`ExecKeys()` 含 `KeyGitCommit`（:163）、`Exec.GitCommit`（:222）、`:660` 的三键清理 | frontmatter 键定义与落盘 |
| `internal/proposal/execution.go` | `Exec.GitCommit`（:285-286）、`succeeded` **必填校验**（:328-330）、渲染（:429）、写盘键值对（:448）、回读比对（:516） | 状态校验与落盘 |
| `internal/cli/delete.go` | :395-401 注释与 `GitCommit: info.SHA` 回写、:420 报告文案 | **K-041-01 的成因**：SHA 只能在 commit 之后取得，回写产生自指哈希张力，成功 `eg delete` 后 `proposals/<pid>.md` 留 1 条脏 diff |
| `internal/cli/proposal_approve.go` / `proposal_execution.go` / `proposal_cmd.go` | `GitCommit: nil` 若干处、`proposal_cmd.go:363` 的 frontmatter 键值投影 | 报告投影 |
| `internal/report/report.go` | `ProposalExecution.GitCommit *string`（:172） | **报告投影**，键名与提案 frontmatter 逐字同名 |

### 1.3 **不在**本裁决范围内的两处（严禁误伤）

1. **B3 `content_hash` / `ContentHash`（并发保护）**——落点在
   `internal/store/store.go`、`internal/store/mutate.go`、`internal/store/write.go`、
   `internal/store/section_write.go`、`internal/plan/executor.go`。
   它是**写前内容比对**，与「执行回执 SHA」毫无关系。
   **2026-11-12 基线实测**：
   `cd evergreen && grep -rn "ContentHash\|content_hash" internal/store internal/plan --include=*.go | grep -v _test.go | wc -l` → **41**。
2. **§4.6 报告体的 `git.commit`**——S1 必填 11 项之一，M4 **保留不动**。
   本裁决退役的是**提案 frontmatter 的执行回执**，不是报告里的 Git 提交字段。
   代码落点：`internal/report/report.go:123-126` 的 `type Git struct { Commit *string \`json:"commit"\` }`
   与 `:205` 的 `Git Git \`json:"git"\``。**注意它与 §1.2 表格里的 `report.go:172` 是两个不同的东西**
   （见下 §1.4），阶段 B 复核时确认二者未被混为一谈。

### 1.4 **在**范围内但**必须单独定口径**的一处：报告的 `proposals[].execution.git_commit` 投影

`internal/report/report.go:167-174` 的 `ProposalExecution` 结构体持有
`GitCommit *string \`json:"git_commit"\``，注释逐字为「提案 `execution` 事实在报告里的投影：
键名与提案 frontmatter **逐字同名**」。它由 `internal/cli/proposal_approve.go:203`、
`proposal_execution.go:107`、`proposal_cmd.go:221/421`（均 `GitCommit: nil`）
与 `proposal_cmd.go:363` 的 frontmatter 键值投影填充。

**这不是 §1.3 那样的「不碰」项 —— 源字段退役后，这个投影键必然受影响**，
而 M3 报告合同已冻结 `proposals[].execution` 的键集合。故本合同冻结如下口径：

| A-32 选项 | 提案 frontmatter | 报告 `proposals[].execution` 的键集合 | 值 |
|-|-|-|-|
| ① 整键删除 | 不再写 `git_commit` | **键仍在**（`json:"git_commit"` 不删） | 恒 `null` |
| ② 保留键恒 `null` | 键在、值恒 `null` | **键仍在** | 恒 `null` |
| ③ 改存非 SHA 回执 | 键改语义 | **键仍在** | 由 A-32 裁决决定 |

**三个选项下报告的 `proposals[].execution` 键集合与键序都一字不改**——这是刻意的：
删掉报告投影键会扩/缩 M3 已冻结的报告键集合，触发跨里程碑回归（与姊妹裁决「不扩张
`data` 键集合」同一套保守原则）。**报告键集合的稳定性优先于「键值恒 null 显得冗余」这种美学诉求。**

机器反证（M4 门禁硬断言）：

```
cd evergreen
# 报告投影键仍在，键名逐字未改
grep -c 'GitCommit      \*string  `json:"git_commit"`' internal/report/report.go   # → 恰 1
# 报告体 git.commit（§4.6）与投影键是两处，二者都必须在
grep -c 'Commit \*string `json:"commit"`' internal/report/report.go               # → 恰 1
```

## 2. M4 落地口径

### 2.1 三条硬要求

1. **写侧不再产生** `execution.git_commit`：`eg delete` / `eg proposal approve` 成功后
   **不得**因回写 SHA 而再次改写 `proposals/<pid>.md`。
2. **`succeeded` 不再把 `git_commit` 作为必填键**：`internal/proposal/execution.go` 的
   `succeeded` 校验中，`git_commit` 缺席**合法**。
3. **读侧宽容**：M3 期已落盘的历史提案文件里若已有 `execution.git_commit`，
   **原样保留、不报错、不参与任何校验、不在读取时被清除**（沿用 `internal/model` 对
   S2 预留字段「出现即原样保留」的同源精神）。

### 2.2 执行回执改由谁承载

| 需求 | M3 载体 | M4 载体 |
|-|-|-|
| 「这次执行落在哪个 commit」 | 提案 frontmatter `execution.git_commit` | **报告 `git.commit`（§4.6 既有键）+ Git 历史** |
| 「执行是否成功」 | `execution.status` | **不变**（`execution.status` 保留） |
| 「哪些路径写了 / 没写」 | `execution.written_paths[]` / `unwritten_paths[]` | **不变**（M3 §4.3 的逐路径义务一字不放宽） |

**§4.3 的逐路径义务（`failed` 时两键必在、并集 == 影响文件全集）不受本裁决影响，一字不改。**

### 2.3 K-041-01 的关闭判据（机器可判）

> **仓库范围必须写清（阶段 B 修正）**：提案文件 `proposals/<pid>.md` 落在**用户 vault 仓**里，
> **不是** `evergreen/` 源码仓。初稿的 `cd evergreen && git status` 检的是**源码仓**，检不到提案文件的脏
> diff —— 那是一条**永远为真**的假反证。下面全部改成显式 `git -C "$VAULT"`。

```
# VAULT = 被操作的 vault 根（一个独立 git 仓），PID = 本次 eg delete 产生的提案 ID
# 一次成功的 eg delete（带 --confirm）之后：
git -C "$VAULT" status --porcelain | wc -l                    # → 0   （提案文件零脏 diff）
git -C "$VAULT" log --oneline -1 | wc -l                      # → 1   （恰一条 delete 相关 commit）
git -C "$VAULT" log --oneline | wc -l                         # → 与执行前相比**恰 +1**（无第二条回写 commit）
grep -c "git_commit" "$VAULT/proposals/$PID.md"               # → 0   （新产生的提案文件不含该键）
git -C "$VAULT" show --stat HEAD | grep -c "proposals/$PID.md"  # → ≥ 1（提案文件在**这一条** commit 里）
```

**「恰 +1」是本判据的核心**：K-041-01 的现象是「成功 `eg delete` 之后提案文件留 1 条脏 diff」，
所以既要证明「没有脏 diff」，也要证明「不是靠追加第二条 commit 把脏 diff 洗掉的」。
只检 `git status` 为空会被「多做一次 commit」这种解法骗过。

### 2.4 **不得误伤**的机器反证（M4 门禁硬断言，双侧锁）

> **阶段 B 修正（这是一处真实的假绿隐患）**：初稿写的是
> `go test ./internal/store -run 'TestB3|TestContentHash|TestFileChanged'`。
> 2026-11-12 实测：该正则在 evergreen@`0277b58` 上**只命中 1 个用例**
> （`TestB3_UserExplicitPathStillHashChecked`）——`TestContentHash` 与 `TestFileChanged`
> **一个都不存在**（真实用例名是 `TestGuardB3FileChangedSkipsAndKeepsBytes`）。
> 而 `go test -run` 打不中任何用例时**退 0**，所以「用例被整批删掉」这件事这条反证**测不出来**。
> 修正后：① 用实测存在的 6 个用例名；② 强制断言 `^=== RUN` 条数 ≥ 6。

```
cd evergreen
# ① B3 并发保护只增不减（基线 41，非测试面）
grep -rn "ContentHash\|content_hash" internal/store internal/plan --include=*.go \
  | grep -v _test.go | wc -l                                                     # → ≥ 41
# ② B3 / 写前哈希比对用例一条不删改 —— 6 个用例名逐字实测存在，且必须真跑起来
go test ./internal/store ./internal/plan -count=1 -v -run \
'TestB3_UserExplicitPathStillHashChecked|TestGuardB3FileChangedSkipsAndKeepsBytes|TestEdit_HashCheckedBeforeWrite|TestGuardExpectedHashFromContext|TestSetDeletedHashMismatchSkipsWithoutWrite|TestSetStatusHashMismatchSkipsWithoutWrite' \
  | tee /tmp/b3.log | grep -c '^=== RUN'                                          # → ≥ 6（**空跑退 0 会被这条抓住**）
grep -c '^--- FAIL' /tmp/b3.log                                                   # → 0
# ③ 报告体 git.commit 仍在（S1 必填 11 项不变）
grep -c 'Commit \*string `json:"commit"`' internal/report/report.go               # → 恰 1
# ③b 报告的 proposals[].execution.git_commit 投影键仍在（§1.4）
grep -c 'GitCommit      \*string  `json:"git_commit"`' internal/report/report.go  # → 恰 1
# ④ execution 的逐路径义务不变（用例名实测存在于 internal/proposal/execution_test.go:65）
go test ./internal/proposal -count=1 -v \
  -run TestExecutionFailed_ListsWrittenAndUnwrittenPaths \
  | tee /tmp/exec.log | grep -c '^=== RUN'                                        # → ≥ 1
grep -c '^--- FAIL' /tmp/exec.log                                                 # → 0
```

**任一条 FAIL 即判「裁决落地误伤既有安全机制」，本条裁决的实现整体回退。**

### 2.5 兼容与迁移

- **不做数据迁移**：既有提案文件的 `execution.git_commit` **不批量清除**
  （批量改写历史落盘数据 = 破坏性操作，属 U-02 精神）。
- `internal/proposal` 的 frontmatter 键集合校验必须容忍该键存在（**已知历史键**白名单）。
- 具体形态（整键删除 / 保留键恒 `null` / 改存非 SHA 回执）见姊妹合同 **A-32**，由 T-…-048 裁决；
  本合同的三条硬要求在**任一选项下都成立**。

---

# 第二部分 · deprecated 关系端点默认隐藏

## 3. 现状、冲突与 M4 口径

### 3.1 现状（2026-11-12 实测）

- `internal/query/card.go:215-221` 的 `VisibleEndpoints` 注释逐字：过滤掉**对端已删除**的关系条目。
  它**只**过滤 `deleted`，**不**过滤 `deprecated`。
- 调用点恰 **4** 处、覆盖 **2** 个命令：
  `internal/query/relation.go:92`（`eg rel` 正向）、`:95`（`eg rel` 反向）、
  `internal/query/card.go:166`（`eg card show` 正向）、`:169`（反向）。
- M2 查询合同 `2026-09-19-m2-query-contract.md` §2.2 / §3：deprecated 端点**默认展示**并加 `[失效]` 标记；
  `eg rel --json` 的 `data` 键序固定**五键** `id` / `relations_out` / `relations_in` /
  `scanned_files` / `skipped_files`（`internal/query/relation.go:39` 的 `DataKeys()` 逐字同真）。
- M3 合同 §5.1 四象限真值表把「deprecated + 未删除」作为关系端点默认展示标为 🔴，
  即 **K-043-01**：M3 合同与 M2 合同口径直接冲突，M3 只实现「端点已删除 → 退出展示」，
  失效端点仍保持 M2 口径。

### 3.2 M4 冻结口径（owner 裁决落地）

> **默认隐藏 `deprecated` 对端；`--include-deprecated` 显式筛选后展示，展示时保留 M2 的 `[失效]` 标记。**

**四象限真值表（M4 目标态，取代 M2 §2.2 在该单元格上的口径；M2 的历史门禁与用例不动，见 §4.3）**：

| 对端 `status` | 对端 `deleted_at` | 默认（无 flag） | `--include-deprecated` |
|-|-|-|-|
| `active` | 空 | **展示** | 展示 |
| `deprecated` | 空 | **隐藏** | **展示，带 `[失效]` 标记** |
| `active` | 非空 | **隐藏** | **仍隐藏** |
| `deprecated` | 非空 | **隐藏** | **仍隐藏** |

**正交性（本合同最关键的一条）**：`--include-deprecated` **只**影响 `deprecated` 维度，
**绝不**影响逻辑删除维度。已逻辑删除的端点在任何 flag 下都不出现——这条继承 M3，不放宽。

### 3.3 「对端」的定义（防止把 deprecated 卡的关系视图整个清空）

筛选看的是**对端（peer）** 的 `status`，**不是**被查询卡自身的 `status`。
在 `k-x` 上执行 `eg rel k-x`：

- `k-x` 自身是否 `deprecated` **不影响**任何过滤；
- 只有 `k-x` 的每一条关系**另一端**的对象是 `deprecated` 时，该条目才被默认隐藏。

**`replaced_by` 的特例必须写死**：`replaced_by` 的典型形态是「旧卡（deprecated）→ 新卡（active）」。
因此：

- 在**旧卡**上查：`relations_out` 里的 `replaced_by` 对端是 active → **默认可见**；
- 在**新卡**上查：`relations_in` 里的 `replaced_by` 来源是 deprecated → **默认隐藏**，
  加 `--include-deprecated` 才见。

这不是 bug，是本裁决的直接推论，必须在 `eg card show` 的人类可读输出里通过 §3.5 的
`Q4`（§3.5.2）提示让用户知道「有被隐藏的条目」，否则用户会以为替代链断了。

### 3.4 查询参数合同

```
eg rel <k-id> [--to <id>] [--include-deprecated] [--json]
eg card show <id> [--include-deprecated] [--json]
```

| 项 | 口径 |
|-|-|
| flag 名 | `--include-deprecated`（**逐字**，无短选项、无别名） |
| 类型 | 布尔，默认 `false` |
| 作用面 | **恰 2 个命令**（`eg rel` / `eg card show`），即 `VisibleEndpoints` 的 4 个调用点 |
| 不作用面 | `eg search` / `eg context` / `eg unreviewed` / `eg reconcile` / `eg check` —— 这些命令**不接受**该 flag，传入即退 `1`（参数非法） |
| 是否写入 | **否**（纯读 flag，零写入零 commit） |
| 与 `--to` 组合 | 正交：`--to` 收窄对端，`--include-deprecated` 放开可见性，两者可同时出现 |

### 3.5 输出合同（**不扩张 `data` 键集合**）

**`eg rel --json` 的 `data` 仍恰五键，键序不变**——这是刻意的：
`internal/query/relation.go:39` 的 `RelDataKeys()` 与 M2 合同 §3 的五键表都被 M2 / M3 门禁引用，
扩张会触发跨里程碑回归。

**`eg card show --json` 的 `data` 键集合同样一字不扩张**：
`internal/query/card.go:110` 的 `CardDataKeys()` 现含 `…, "relations_out", "relations_in", FieldDeleted, FieldUnreviewed`，
M4 **不新增第 N+1 个键**（不新增 `hidden_deprecated` 之类）。两个命令的 `data` 键集合都是 M2 冻结面。

被隐藏的信息改由 **envelope 的 `warnings[]`** 承载：

> **`Q4`**（新增**查询域**诊断码）：`本次查询默认隐藏了 N 个 deprecated 端点，使用 --include-deprecated 查看`

#### 3.5.1 为什么是 `Q4` 而不是 `W21`（2026-11-12 阶段 B 修正，**这是一处硬冲突**）

本合同**初稿写的是 `W21`，那是错的**，因为它撞上 M2 查询合同已冻结的**码空间分域**约束：

- `2026-09-19-m2-query-contract.md` §5.1 逐字写死：
  **「`Q1–Q3` 不进 §4.5.1 的 E/W/I 表：它们只用于**只读查询命令**，不改 ChangePlan…」**
  （代码侧同真：`internal/query/diagnostic.go:20-25` 只定义 `CodeQ1` / `CodeQ2` / `CodeQ3`；
  `internal/plan/diagnostics.go:41-45` 的 `W10` / `W11` / `W12` 才是 E/W/I 域，属**写命令**。）
- `eg rel` / `eg card show` 是**纯读命令**（本合同 §3.4「是否写入 = 否」）。把它们的诊断记成 `W2x`
  等于把只读查询的诊断塞进 ChangePlan 的 E/W/I 表 —— **直接违反 M2 §5.1**。
- 姊妹合同 `2026-11-12-m4-reconcile-contract.md` §3 已把 `W13`–`W20` 分配给 `eg reconcile`
  的 findings（那是写命令，属 E/W/I 域，合规）。`W21` 若再给只读查询用，**既越域又与
  reconcile 的码段相邻易混**。

**故本合同冻结：新增码为 `Q4`，落在查询域。**

> **⚠️ 遗留冲突（阶段 B 实测，必须由 owner 裁决）**：M2 查询合同 §5.1 的小节标题逐字是
> **「### 5.1 Q 码表（**恰三条**）」**（`2026-09-19-m2-query-contract.md:338`），
> 这是一个**封闭集合**表述。因此「新增 `Q4`」不是纯追加，而是**修订 M2 冻结合同的一句计数**。
> 本合同的处置：① **不改** M2 合同任何既有条款（含该标题），只在本合同登记 **A-39**；
> ② 机器侧已核实**没有任何历史门禁**断言过「恰三条」这个字面量
> （反证：`grep -rn "恰三条" projects/evergreen/s1_main_flow/tools/ | wc -l` → **0**），
> 故 owner 若批准，改动 M2 合同标题为「恰四条（S3 起）」不会破坏任何历史判据；
> ③ 未获批准前，T-…-061 **不得**实现 `Q4`，`--include-deprecated` 的隐藏计数只走人类可读输出。

M4 新增诊断码总数**仍是 14**
（`E11`–`E14` 四个 + `W13`–`W20` 八个 + `Q4` 一个 + `I2` 一个），姊妹合同 §3 / §14 的计数表已同步。

#### 3.5.2 `Q4` 的完整口径（每一条都机器可判）

| 项 | 口径 |
|-|-|
| `code` | 逐字 `Q4` |
| `level` | `warning`（沿用 Q 系列，**不影响退出码**，仍退 `0`） |
| `path` | 逐字 `(汇总)` —— 与 `Q3` 同形（`internal/query/diagnostic.go:83` 的 `Path: "(汇总)"`），因为它是**全查询级**汇总而非单文件问题 |
| `op_index` | `-1`（沿用 M2 合同 §5 对 Q 系列的规定） |
| 产出条件 | 仅当**确有**条目因 `deprecated` 被隐藏（`N ≥ 1`）；`N == 0` 时**不产出**（避免噪声，反向可判） |
| `--include-deprecated` 生效时 | **恒不产出**（此时零隐藏） |
| `N` 的口径 | 因 `deprecated` 被隐藏的**条目数**（不是对象数）；**不含**因逻辑删除被隐藏的条目（后者沿用 M3 口径，静默隐藏、无提示） |
| 条数 | 每次查询**至多一条**（与 `Q3` 同形，不逐条目产出） |

**与 `Q3` 的正交性（M2 冻结面，必须逐字守住）**：

- M2 §5.1 逐字：**「`Q3` 只在有 `Q1` / `Q2` 时出现；无 `Q1` / `Q2` 时 `warnings[]` 里**没有** `Q3`（反向可判）」**。
  因此 **`Q4` 绝不触发 `Q3`**：只隐藏了 deprecated 端点、没有任何 `Q1` / `Q2` 时，
  `warnings[]` 恰含 **1 条 `Q4`、0 条 `Q3`**。把 `Q4` 计入 `Q3` 的触发条件 = 放宽 M2 的反向可判性，**禁止**。
- 语义上也不该触发：`Q3` 说的是「结果**不完整**（有文件读不了 / 引用是悬空的）」，
  而默认隐藏 deprecated 是**按合同的正确行为**，结果是完整的。
- **诊断次序**（M2 §5.1 冻结「先按 `code`（`Q1` → `Q2`），同码按 `path` 字典序，最后追加 `Q3`」）
  扩展为：先按 code `Q1` → `Q2` → **`Q4`**，同码按 `path` 字典序，**最后仍追加 `Q3`**。
  即 `Q3` 保持列表末位（M2 既有断言「`Q3` 是最后一条」不被打破），`Q4` 插在 `Q2` 之后、`Q3` 之前。

**人类可读输出**：同样打印该提示行（与 `--json` 的 `warnings[]` **同源同事实**，沿用 M2 §5 的
「人类可读与 `warnings[]` 同源」承诺）；`--json` 下进 `warnings[]`。

**`[失效]` 标记**：`--include-deprecated` 展示 deprecated 端点时，**必须**带 M2 §2.2 的 `[失效]` 标记；
与 M3 的 `[已删除]` / `[未过目]` 标记的顺序规则（`[失效][已删除]`）**一字不改**
——但由于已删除端点在任何 flag 下都不出现，`[失效][已删除]` 组合**不会**出现在关系端点渲染面
（它仍出现在对象自身的标题渲染面，M3 口径不变）。

## 4. 回归影响面（M4 必须逐条复核，不得靠改旧断言让它变绿）

### 4.1 代码面

| 位置 | 影响 |
|-|-|
| `internal/query/card.go` `VisibleEndpoints` | 需接受**可见性策略**参数（默认策略 = 隐藏 deprecated + 隐藏 deleted），并返回被隐藏的 deprecated 计数 |
| `internal/query/relation.go:92/95`、`card.go:166/169` | 4 个调用点全部传入同一策略；**正向与反向必须同策略**（不得只改一侧） |
| `internal/cli/rel.go`、`internal/cli/card.go` | 解析 `--include-deprecated`，把 `Q4`（§3.5.2）写进 `warnings[]`；**不得**改用 `W2x` 码（越域，见 §3.5.1） |
| `internal/query/relation.go:39` `DataKeys()` | **一字不改**（仍五键） |

### 4.2 与 R4 孤儿检查的交叉约束（姊妹合同 §6.3）

「知识卡零关系」的孤儿判定**必须看落盘事实**，**不得**看展示面过滤结果。
否则一张只有 deprecated 邻居的正常卡会被误判为孤儿。
该约束由 T-…-052（R4）与 T-…-061（可见性）**双侧**用例锁死。

### 4.3 M2 / M3 历史门禁与用例的处理原则

- **一律不改**。M2 的「默认展示 + `[失效]`」是 **M2 期事实**，M2 门禁断言的是那个事实，
  改它等于放宽 M2 结论。
- M4 的新口径**只**通过**新增** M4 侧用例与 M4 侧门禁断言表达。
- 若某条 M2 / M3 既有 Go 用例在新默认下**必然失败**（例如断言「deprecated 端点默认出现在
  `relations_out` 里」），则该用例属于**被新裁决推翻的事实断言**，处理方式**恰一种**：
  在 M4 侧**重钉**该用例（保留用例名、改断言为新事实并在用例注释里逐字写明
  「2026-xx-xx 依 owner 裁决『deprecated 端点默认隐藏』重钉，旧事实见 M2 合同 §2.2」），
  **不得删除用例、不得改成跳过**。重钉清单必须逐条进 T-…-061 的 Activity Log 与 M4 验收报告。
- **重钉的边界**：只允许重钉「deprecated 端点默认可见性」这**一个**事实点；
  同一用例里的其它断言（排序、计数、`[失效]` 标记文案、`scanned_files`）一字不动。

## 4.4 测试矩阵（**十组**，T-…-061 必须逐组落地；G7–G10 为 2026-11-12 阶段 B 补齐）

| 组 | 场景 | 断言 |
|-|-|-|
| **G1 默认隐藏** | `k-a` ⟶`supports`⟶ `k-b`，`k-b.status = deprecated`，未删除 | `eg rel k-a --json` 的 `relations_out` **不含** `k-b`；`warnings[]` 含 **恰 1 条 `Q4`** 且 `N == 1`、**0 条 `Q3`**；退 `0` |
| **G2 显式显示** | 同 G1 + `--include-deprecated` | `relations_out` **含** `k-b`；人类可读输出该行带 `[失效]`；`warnings[]` **不含** `Q4` |
| **G3 正反向一致** | `k-b` 上反查 | 默认：`eg rel k-b` 的 `relations_in` 含 `k-a`（`k-a` 是 active，对端不是 deprecated → 可见）；`eg rel k-a` 的 `relations_out` 不含 `k-b`。加 flag 后两侧对称可见。`eg card show` 的两侧与 `eg rel` **逐字同结果** |
| **G4 逻辑删除正交** | `k-c.status = deprecated` 且 `deleted_at` 非空 | 默认隐藏；**加 `--include-deprecated` 仍隐藏**；`warnings[]` 的 `Q4` 计数 `N` **不含** `k-c`（若 `k-c` 是唯一 deprecated 端点则**不产出** `Q4`） |
| **G5 `replaced_by` 链** | 旧卡 `k-old`（deprecated）⟶`replaced_by`⟶ 新卡 `k-new`（active） | 在 `k-new` 上默认 `relations_in` **不含** `k-old`、加 flag 后含；在 `k-old` 上默认 `relations_out` **含** `k-new`（对端 active）。两个方向的 `Q4` 计数各自独立且正确 |
| **G6 孤儿检查与可见性正交** | `k-d` 的唯一邻居是 deprecated 的 `k-e`（均未删除） | `eg reconcile` **不得**把 `k-d` 报成 `orphan`（`W17`）——孤儿判定看**落盘事实**、不看展示面过滤（§4.2）；同时 `eg rel k-d` 默认 `relations_out` 为空 + 1 条 `Q4`。**两条断言必须在同一个用例里同时成立**，否则「正交」无从证明 |
| **G7 flag 不作用面** | 对 `eg search` / `eg context` / `eg unreviewed` / `eg reconcile` / `eg check` 传 `--include-deprecated` | 每个命令都退 **`1`**（参数非法，§3.4「不作用面」），且**零写入零 commit**；`eg rel` / `eg card show` 传该 flag 退 `0`。**恰 2 个命令接受、恰 5 个命令拒绝**，逐命令断言 |
| **G8 `data` 键集合不扩张** | G1 与 G2 两种形态下分别取 `--json` 输出 | `eg rel` 的 `data` 键序**逐字**等于 `RelDataKeys()` 的五键；`eg card show` 的 `data` 键序**逐字**等于 `CardDataKeys()`；两者在**有 / 无 flag、有 / 无隐藏**四种组合下**键集合完全一致**（键集合不随可见性变化） |
| **G9 `Q3` 不被 `Q4` 触发（M2 反向可判性）** | 只有 deprecated 隐藏、无任何不可解析文件与悬空引用 | `warnings[]` 恰 1 条 `Q4` + **0 条 `Q3`**；再人为造 1 条 `Q1` 后，`warnings[]` 次序逐字为 `Q1` → `Q4` → `Q3`（`Q3` 恒末位，§3.5.2） |
| **G10 「已删除」过滤未被替换掉** | `k-f.status = active` 且 `deleted_at` 非空（**不是** deprecated） | 默认隐藏、加 `--include-deprecated` **仍隐藏**、`Q4` **不产出**（`N == 0`）。这条防的是「实现时把 `deleted` 判据改写成 `deprecated` 判据」——两个维度必须**都在**，不是二选一 |

**每组都必须同时覆盖 `eg rel` 与 `eg card show` 两个命令**（`VisibleEndpoints` 的 4 个调用点全覆盖）：
只测 `eg rel` 会漏掉 `card.go:166/169` 这两个调用点，属「正向与反向 / 两命令必须同策略」的漏测面。

### 4.5 「不误伤」的机器反证（裁决二侧，M4 门禁硬断言）

> **反证有效性前置（阶段 B 教训，必须遵守）**：所有 `go test -run <正则>` 形态的反证，
> **必须**同时断言 `-v` 输出里 `^=== RUN` 的**条数下限**。否则正则打不中任何用例时
> `go test` 会**空跑退 0**（假绿）。本节所有用例名都已在 evergreen@`0277b58` 上 grep 实测存在。

```
cd evergreen
# ① VisibleEndpoints 的定义 + 调用点只增不减（基线：定义 1 处 + 调用 4 处，跨 2 个文件）
grep -rn "VisibleEndpoints" internal/query --include=*.go | grep -v _test.go | wc -l   # → ≥ 5
grep -c "VisibleEndpoints" internal/query/relation.go                                  # → ≥ 2（:92 正向 / :95 反向）
grep -c "VisibleEndpoints" internal/query/card.go                                      # → ≥ 3（:166 / :169 + 定义）
# ② 逻辑删除维度仍在（防「把 deleted 判据改写成 deprecated 判据」），基线 11
grep -c "Deleted" internal/query/card.go                                               # → ≥ 11
go test ./internal/query -run 'TestDeletedExcludedFromDefaultView' -count=1 -v \
  | grep -c '^=== RUN'                                                                 # → ≥ 1 且 PASS
# ③ data 键集合一字不改（两个命令各一条）
grep -c 'return \[\]string{"id", "relations_out", "relations_in", "scanned_files", "skipped_files"}' \
  internal/query/relation.go                                                           # → 恰 1
grep -n 'func CardDataKeys' -A 4 internal/query/card.go | grep -c 'relations_out'       # → ≥ 1
# ④ M2 冻结的 Q 码语义未被改写：Q3 仍「只由 Q1/Q2 触发」且「无 Q1/Q2 时不出现」
go test ./internal/query \
  -run 'TestScanPartialResultEmitsQ3|TestSearchNoQ3WithoutQ1OrQ2|TestCardShowDanglingRelationQ2|TestRelQueryDanglingQ2' \
  -count=1 -v | grep -c '^=== RUN'                                                     # → ≥ 4 且逐用例 PASS
# ⑤ 悬空条目「照常输出、不隐藏」的 M2 口径未被新过滤器顺手隐藏（Q2 与 Q4 是两个维度）
go test ./internal/query -run 'TestCardShowDanglingRelationQ2' -count=1 -v \
  | grep -c '^=== RUN'                                                                 # → ≥ 1 且 PASS
```

**任一条 FAIL 即判「可见性裁决落地误伤既有查询语义」，本条裁决的实现整体回退。**

### 4.6 「必然被推翻」的既有用例：**实测清单为空**，但 T-…-061 必须复算

2026-11-12 在 evergreen@`0277b58` 上逐个用例核对，`internal/query` / `internal/cli` 里
带 `Deprecated` 断言的用例是：

| 用例 | 断言对象 | 是否被本裁决推翻 |
|-|-|-|
| `TestScanIncludesDeprecated` | **扫描面**是否收录 deprecated 卡（不是关系端点可见性） | **否**（M4 不改扫描面） |
| `TestSearchDeprecatedCardsStayVisible` | `eg search` 结果里 deprecated 卡仍可见 | **否**（`eg search` 属 §3.4「不作用面」） |
| `TestContextDeprecatedNotRecommended` / `TestContextExcludesDeprecatedCard` | `eg context` 的推荐口径 | **否**（同上，不作用面） |
| `TestCardShowDeprecatedMarkers` | **卡自身**的 `markers` 含 `[失效]` | **否**（§3.3：筛选看**对端**，不看被查询卡自身） |
| `TestCardShowDanglingRelationQ2` / `TestRelQueryDanglingQ2` | 悬空条目**不得**从 `relations_out` 消失 | **否**（悬空 ≠ deprecated，两维度正交；但 G10 / §4.5⑤ 必须锁死这一点） |

**结论：`§4.3` 的「重钉」机制在当前基线下预计**零使用**。**
但 T-…-061 **仍必须**在开工首日复算这张表（用 `grep -rln "Deprecated" internal/query/*_test.go
internal/cli/*_test.go` 逐文件过一遍），并把复算结果（无论是否为空）写入 Activity Log 与 M4 验收报告：
**「清单为空」本身也是一条必须留痕的结论**，不留痕就无法反证「没有偷偷删用例」。
若复算发现新的被推翻用例，一律走 §4.3 的重钉流程，**不得删除、不得跳过**。

## 5. 本合同新增登记（A-37 / A-38 / A-39）

| 编号 | 事项 | 封闭选项 | 关闭条件 |
|-|-|-|-|
| **A-37** | 权威侧 / M2 合同 §2.2 与 §3 的「deprecated 端点默认展示 + `[失效]`」行文与本裁决冲突（K-043-01 的权威侧残留） | 本仓口径 = **默认隐藏 + 显式筛选**；M2 历史门禁不动 | 登记型：待 owner 在飞书 §5.1 / 本仓 M2 合同 §2.2 补「S3 起默认隐藏」脚注。**本仓不改飞书文档，也不改 M2 合同既有条款** |
| **A-38** | 被隐藏信息的承载方式：本合同选「不扩张 `data` 键集合、改用 `warnings[]` 的 **`Q4`**」（阶段 B 已把初稿的 `W21` 纠正为 `Q4`，理由见 §3.5.1：`W2x` 属 ChangePlan 的 E/W/I 域，只读查询命令用它违反 M2 §5.1）。另一选项是把 `data` 扩为六键（新增 `hidden_deprecated`），语义更直接但会扩张 M2 已冻结的键集合 | ① `warnings[] Q4`（本合同暂定）；② `data 扩为六键` | **阻塞型**：由 T-…-048 关闭。owner 若选 ②，T-…-061 的输出合同与 M2 五键断言的关系需另行豁免申请<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §4 —— 结论 = `warnings 承载 Q4`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |
| **A-39** | 【阶段 B 新增】M2 查询合同 §5.1 的标题逐字是「Q 码表（**恰三条**）」（该文件 :338），并冻结了「`Q3` 只在有 `Q1`/`Q2` 时出现（反向可判）」。M4 新增 `Q4` 因此**不是纯追加**，而是要动 M2 冻结合同的一句计数：本合同的口径是「只追加成员、不改既有三码的任何一条语义，`Q3` 仍恒为末位且不被 `Q4` 触发」，且本仓**不改** M2 合同原文 | ① 追加 `Q4` 并由 owner 批准把 M2 §5.1 计数改成「恰四条（S3 起）」（本合同暂定，只加严不放宽；已实测 `tools/` 下 0 处断言「恰三条」，改后不破坏任何历史门禁）；② 复用 `Q3` 承载（**不可取**：会破坏 M2 的反向可判性）；③ 不产出机器可读诊断，隐藏计数只走人类可读输出（**降级兜底**：未获批准前 T-…-061 走此选项） | **阻塞型**：与 A-38 同批由 T-…-048 关闭<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §4 —— 结论 = `改为恰四条`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |

## 6. 明确不做

- **不改 `[失效]` 标记的文案与顺序规则**（M2 / M3 口径一字不动）。
- **不改逻辑删除的可见性口径**（M3 口径一字不动）。
- **不引入 `--exclude-active` / `--only-deprecated` 等反向筛选**（M4 只加**一个** flag）。
- **不把 `--include-deprecated` 扩到写命令**（纯读 flag）。
- **不批量清除历史提案的 `execution.git_commit`**（§2.5）。
- **不改 §4.6 报告体的 `git.commit`**、**不削弱 B3 `content_hash`**（§1.3 / §2.4）。
- **不删除、不跳过任何 M1 / M2 / M3 既有用例**（§4.3 的重钉边界）。
