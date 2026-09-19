---
topic: M4 一致性与对账合同（internal/reconcile 包 / R1–R7 / finding schema / eg reconcile / eg check / 报告 reconcile 字段）
stage: S3
milestone: M-004
task: 未分配（本合同先于 M4 拆分冻结，后续 task 反向引用本文）
authority: 飞书《[技术方案]常青（Evergreen）v1 技术方案》§4.5 / §4.6 / §5.1 / §5.2 / §7.1 / §7.2 / §11 / §11.1 / §11.2 / §13 / §14 / §16.1
authority_url: https://docs.example.invalid/evergreen/design
authority_role: provenance-only
authority_extracted_at: '2026-11-12'
authority_mutation: 只读，未修改
baseline: docs/specs/2026-08-31-evergreen-s1-tech-design.md#ch7
created: '2026-11-12'
updated: '2026-11-12'
updated_by: 项目维护者
---

# M4 一致性与对账 — Contract（S3 · 里程碑 M-004）

本文件把技术方案 **§16.1 M4 行**——「一致性与对账；交付 `reconcile` 包，R1 / R2 / R4 必做，
R3 / R5 / R6 / R7 逐步补；对账**不作为写命令前置**」——展开成可施工、可机器校验的合同。
体例对齐两份 M3 合同（2026-10-10 冻结）：**先冻结合同、再拆 task**，故本文 frontmatter
`task: 未分配`，由 T-…-048 ~ T-…-063 反向引用。

## 0. 依据与抽取声明

- **权威来源**：飞书技术方案（`authority_url`），2026-11-12 只读抽取，**未修改一个字**。
  本仓快照 `refs/tech_design.md`（同日抓取）仅作复算用途，不构成权威。
- **本合同不裁决的事项**一律进 §16 登记表 **A-30 ~ A-36**，由 T-…-048（M4 开工前置裁决）关闭。
  本合同**不得替 owner 拍板**。
- **只加严不放宽**：M1 / M2 / M3 的任何已冻结结论、判据、门禁断言，本合同一字不改。
  凡本合同与 M1–M3 合同冲突之处，一律以「M4 侧新增约束」形态表达，不修改旧条款。

## 0.1 硬边界（S3 不承诺的四件事，逐条写死）

1. **不引入 `.index/` / SQLite / FTS5 / 增量索引 / 性能门槛**。对账一律走**全量 Markdown 扫描**
   （复用 `internal/query` 的扫描底座）。反证：`ls evergreen/vault/.index 2>/dev/null | wc -l` → `0`；
   `grep -rnE "FTS5|sqlite|\.index/" github.com/ikaqiu-Lemon/EverGreen/internal/reconcile/ | wc -l` → `0`。
2. **不实现强原子事务、`run.lock` / `txn/`、短临界区锁、崩溃恢复、块级安全合并、退出码 `5`**——均属 S5 / M6。
3. **对账不作为任何写命令的前置**。`eg capture` / `eg apply` / `eg edit` / `eg delete` / `eg rel` 等
   写命令**不得**在执行前调用 `internal/reconcile`。反证：
   `grep -rn "internal/reconcile" github.com/ikaqiu-Lemon/EverGreen/internal/cli/*.go | grep -vE "^internal/cli/(reconcile|check)\.go" | wc -l` → `0`。
   「写前对账」属 **S5 / M6**（技术方案 §11 末段）。
4. **不承诺「权威 Markdown 一个字节都不改」**——该承诺属 S5 目标态；M4 的 R2 / R6 明确**会**改
   frontmatter（见 §5 / §9），但改动面被本合同逐键封闭。

## 1. 阶段口径与包边界

### 1.1 `internal/reconcile` 的唯一职责

技术方案 §13 逐字：`internal/reconcile` 的职责是「**R1–R7 检查项与报告聚合**」，
允许依赖 `model` / `mdfile` / `git` / `query`。本合同据此把它定死为：

> **`internal/reconcile` 是纯只读、纯函数的检查器**：输入 = vault 快照 + Git 工作区状态，
> 输出 = `[]Finding` + `[]RepairSpec`（**修复意向的描述，不是写动作**）。
> 该包**零写盘、零 commit、零 `os/exec`**。

**机器反证（三条，全部由 T-…-049 落地）**：

```
cd evergreen
grep -rn "os.WriteFile\|os.Create\|os.Remove\|os.Rename" internal/reconcile/ | grep -v _test.go | wc -l   # → 0
grep -rn "github.com/ikaqiu-Lemon/EverGreen/internal/store\|github.com/ikaqiu-Lemon/EverGreen/internal/plan" internal/reconcile/ | grep -v _test.go | wc -l # → 0
grep -rn "Commit(" internal/reconcile/ | grep -v _test.go | wc -l                                          # → 0
```

### 1.2 依赖方向（ADR-20 文件级隔离同源口径）

允许：`reconcile` → {`model`, `mdfile`, `git`（**只读 API**）, `query`}。
禁止：`reconcile` → {`store`, `plan`, `cli`, `report`, `proposal`}；
禁止：`query` / `store` / `plan` / `proposal` → `reconcile`（反向依赖即环）。

`git` 包的只读面 = 状态查询（`Porcelain` / `HEAD` / `Log` / `Diff`）。
`reconcile` **不得**调用 `git` 包的 `Commit` / `Add` 等写方法（上条 grep 反证）。

### 1.3 写口归属（A-23 裁决的延伸，不放宽）

| 写入对象 | 谁写 | 通道 | 依据 |
|-|-|-|-|
| Git 提交（R1 纳管 commit） | `internal/cli/reconcile.go` | 直连 `internal/git`（与 `eg capture` / `eg init` 同类：用户发起 + CLI 自带 commit） | A-23 裁决第 3 条「CLI 负责 Git 与报告」 |
| `reviewed_at` 补齐（R2） | `internal/plan` → `internal/store` | **内存 ChangePlan**（`verb` 见 A-30），复用 `runPlan` | A-23 裁决第 4 条「知识数据修改仍走 ChangePlan」 |
| `stale` / `stale_reason`（R6） | `internal/plan` → `internal/store` | 同上 | 同上 |
| 其余一切（R3 / R4 / R5 / R7） | **无人写** | 只报告 | §11.1「只报告，不自动补齐」 |

**硬约束**：`internal/cli/reconcile.go` **不得**直接 `import internal/store`。
反证：`grep -n "internal/store" internal/cli/reconcile.go | wc -l` → `0`。

## 2. Finding schema（§11.2 落地，封闭四键）

技术方案 §11.2 逐字：最终报告 `reconcile` 字段含 `ran` / `commit` / `findings[]`，
finding 含 `check` / `severity` / `targets[]` / `detail`。本合同把它封闭为：

```go
type Finding struct {
    Check    string   `json:"check"`     // §3 十二值封闭枚举
    Severity string   `json:"severity"`  // "error" | "warning"，恰两值
    Targets  []string `json:"targets"`   // 非 nil；空集合序列化为 []，不得为 null
    Detail   string   `json:"detail"`    // 非空中文单句，含足以复算的事实
}
```

- **恰四键，不增不减**。反证：`Finding` 结构体字段数 == 4，且 `json` tag 集合逐字
  `{check, severity, targets, detail}`。
- `severity` **恰两值** `error` / `warning`。`info` **不启用**（登记为 S3 之后）。
- `targets[]` 元素一律是**可定位标识**：知识卡 / 笔记 / 材料 ID，或 vault 相对路径。
  同一 finding 的 `targets[]` 内部**去重且字典序升序**（可复算）。
- `findings[]` 整体排序键 = `(severity, check, targets[0])`，`error` 在前（稳定、可复算）。

## 3. `check` 十二值封闭枚举与诊断码单射

| # | `check` | R 编号 | `severity` | 诊断码 | 含义 |
|-|-|-|-|-|-|
| 1 | `git_uncommitted` | R1 | `warning` | **W13** | 发现未提交的外部编辑（本次已纳管） |
| 2 | `reviewed_at_missing` | R2 | `warning` | **W14** | 用户直接编辑后 `reviewed_at` 缺失或早于 `updated_at`（本次已补齐） |
| 3 | `duplicate_id` | R4 | `error` | **E11** | 同一 ID 在 vault 中出现于两个及以上文件 |
| 4 | `dangling_ref` | R4 | `error` | **E12** | 笔记 / 材料的引用指向不存在的对象 |
| 5 | `orphan` | R4 | `warning` | **W17** | 孤儿：笔记无所属材料 / 材料无派生笔记 / 知识卡零关系 |
| 6 | `relation_target_missing` | R3 | `error` | **E13** | 关系 `target` 在 vault 中不存在 |
| 7 | `relation_prefix_invalid` | R3 | `error` | **E14** | 关系 `target` 的 ID 前缀不合法（不属既有 ID 规则） |
| 8 | `relation_opposing_asymmetric` | R3 | `warning` | **W15** | `opposing` 只有单向记录（缺反向） |
| 9 | `relation_duplicate` | R3 | `warning` | **W16** | 规范化 `(from,type,target)` 出现两条及以上记录 |
| 10 | `domain_moved` | R5 | `warning` | **W18** | 手工跨领域移动：文件所在领域目录与 ID / frontmatter 隐含领域不一致 |
| 11 | `recap_stale` | R6 | `warning` | **W19** | 综述可能失准（本次已写 `stale` / `stale_reason`） |
| 12 | `support_insufficient` | R7 | `warning` | **W20** | 材料支持不足（实时判定，**只报告**） |

- **`check` 恰 12 值封闭**；第 13 个取值即 FAIL。
- **`check` ↔ 诊断码双向单射**：12 个 `check` 对应 12 个互不相同的码，反之亦然。
- 另新增 **I2**：本次对账**零 finding**（正常态的信息条目，不对应任何 `check`）。
- 另新增 **`Q4`**（**查询域**，不是 `W21`）：见姊妹合同
  `2026-11-12-m4-visibility-and-execution-contract.md` §3.5.1 / §3.5.2
  （deprecated 端点默认隐藏提示），**不属对账 finding**，不进 `check` 枚举。
  **2026-11-12 阶段 B 修正**：初稿把它写成 `W21`，那会把**只读查询命令**的诊断塞进
  ChangePlan 的 E/W/I 表，**违反 M2 查询合同 §5.1 逐字冻结的「`Q1–Q3` 只用于只读查询命令、
  不进 §4.5.1 的 E/W/I 表」**。故本合同的 W 段**恰止于 W20**，`W21` **不再分配**。

**M4 后的诊断码全集** = `E1..E14` ∪ `W1..W20` ∪ `{I1, I2}` ∪ `Q1..Q4`
（`Q` 段属只读查询域，与 E/W/I 段**不同域**，见 M2 合同 §5.1）。
M4 新增恰 **14** 个（E11–E14 四个、W13–W20 **八**个、`Q4` 一个、I2 一个）。
`skipped[].kind` **不新增**（仍恰 `file_changed` / `user_block_unsafe` 两值）。

## 4. R1 — Git 纳管（必做）

**触发**：`eg reconcile` 启动时。
**检查**：`git status --porcelain` 在 `vault/` 范围内的非空条目。
**判定**：命中即产出 `git_uncommitted` finding，`targets[]` = 受影响的 vault 相对路径（升序去重）。
**动作**：`eg reconcile` 在**全部**检查与修复写入完成后，执行**恰一次** `git add -A` + commit
（`git add -A` 口径与 M1 起的既有口径**一字不变**，不得改成选择性 add）。

- 该 commit 的 `verb` 见 **A-30**（新增第 9 个 verb `reconcile` vs 复用 `process`），由 T-…-048 裁决。
- **零改动即零 commit**：工作区干净且本次无修复写入时，**不得**产生空 commit；
  `reconcile.commit` = `null`，`reconcile.ran` = `true`。
- **恰一次 commit** 的原因：§11.2 的 `reconcile.commit` 是**单值**字段，两次 commit 无法表达。
  该口径同时被 **A-35** 复核。
- **不改判 M2 / M3 的 `git add -A` 与整文件跳过口径**（R-13 继承，M4 不偷偷改成强原子）。

## 5. R2 — `reviewed_at` 补齐（必做）

**这是 M3 明确留给 S3 / M4 的触发①**（M3 只做触发② `eg mark-reviewed`，见
`2026-10-10-m3-user-authorization-contract.md` A-16 / N-7 与 T-…-042 的「明确不做」）。

**判定**（三条同真才算命中）：

1. 对象是**知识卡或材料笔记**（`proposals/**` 与 `unprocessed.md` 不在范围内）；
2. 该文件在 Git 上有**用户直接编辑**的证据——即 `git_uncommitted` 命中该路径，
   **或** 最近一次改动该文件的 commit 的 `verb` **不属** `eg` 已知 verb 集合（外部编辑）；
3. `reviewed_at` 缺失，**或** `reviewed_at < updated_at`。

**动作**：把 `reviewed_at` 补写为**本次对账的时刻**（RFC3339，与 `eg mark-reviewed` 同一格式化口径），
经**内存 ChangePlan** → `internal/plan` → `internal/store` 落盘。产出 `reviewed_at_missing` finding。

**三维不牵连**：**只**写 `reviewed_at`；`status` / `deleted_at` / `deleted_reason` / `replaced_by`
**一格不写**。`updated_at` 由 store 既有口径决定，不由 reconcile 额外指定。

**B3 不豁免**：补写同样走 `ExpectedHash` 前置比对；hash 过期 → 进 `skipped[kind=file_changed]`，
本文件本次不写，finding 仍产出（detail 里注明「已跳过」）。

**ADR-20 文件级隔离不放宽**：`internal/reconcile` **不得** import `internal/query/rank` /
`internal/query/relations` / `internal/rules/converge` / `internal/query/review`，
`reviewed_at` 仍不参与排序、收敛与关系计算。

## 6. R4 — 重复 ID / 悬空引用 / 孤儿（必做）

### 6.1 `duplicate_id`（error / E11）

全量扫描 vault 五分区，按对象 ID 建索引；同一 ID 命中 ≥ 2 个文件即产出，
`targets[]` = **全部**冲突文件的 vault 相对路径（不只报第一个）。
**不自动改名、不自动删除**（物理删除属 U-01，任何阶段不做）。

### 6.2 `dangling_ref`（error / E12）

覆盖两类引用：① 材料笔记 frontmatter 指向的 `source`；② 知识卡 frontmatter 指向的来源笔记。
目标不存在即产出，`targets[]` = `[引用方 ID, 缺失的目标 ID]`。
**关系条目的 target 缺失不走这条**——它属 R3 的 `relation_target_missing`（避免两码重复计一件事）。

### 6.3 `orphan`（warning / W17）

三种孤儿合并为**一个** `check`，由 `detail` 区分子类型（子类型串封闭三值：
`note_without_source` / `source_without_note` / `card_without_relation`）：

- 笔记不属任何材料；
- 材料没有任何派生笔记；
- 知识卡的 `relations_out` 与 `relations_in` **同时**为空。

「知识卡零关系」的判定**必须与姊妹合同 §2 的可见性过滤解耦**：
孤儿判定看**落盘事实**（`relations[]` 原始条目 + 全库反向扫描），
**不看**展示面过滤结果。否则「deprecated 端点默认隐藏」会把正常卡误判成孤儿。
这是本合同与姊妹合同之间**唯一的交叉硬约束**，由 T-…-052 与 T-…-061 双侧用例锁死。

## 7. R3 — 关系校验（M4 落地，只报告）

**逐项评估结论（技术方案 §11.1 写「S3 之后逐步补」，本仓采用「M4 落地」口径，登记 A-36）**：
R3 的四个子检查全部只依赖 `internal/query` 已有的扫描与关系底座（M2 已交付），
新增成本 = 纯只读遍历 + 四条判定，**不引入任何新写口**，故 M4 落地，只报告、不自动修。

| 子检查 | `check` | 判定 |
|-|-|-|
| target 存在性 | `relation_target_missing` | `relations[].target` 在 vault 中查无此对象 |
| 前缀合法性 | `relation_prefix_invalid` | `target` 不匹配既有 ID 规则（`k-` / `n-` / `s-` 等，取 `internal/model` 的现成校验，**不新造规则**） |
| `opposing` 方向异常 | `relation_opposing_asymmetric` | A ⟶`opposing`⟶ B 存在而 B ⟶`opposing`⟶ A 缺失。**规范化口径与 A-24 一致**：先按 ID 字典序规范化 `(from,target)` 再比对 |
| 重复对 | `relation_duplicate` | 规范化 `(from,type,target)` 出现 ≥ 2 条记录（M3 的 `rel add` 幂等去重只在**写侧**生效，外部编辑绕过写侧，故读侧仍需检查） |

- **只报告**：一律**不**自动补反向关系、不自动去重、不物理移除。修复由用户显式跑
  `eg rel add` / `eg rel remove`（M2 / M3 既有命令）。
- **不改 F4**：关系类型集合仍恰 8 值封闭，本合同不新增关系类型、不新增关系字段。
- **与 A-24 不冲突**：A-24 定义的是 `remove_relation` 的**落盘语义**；R3 只读不写。

## 8. R5 — 手工跨领域移动检测（M4 落地，只报告）

**逐项评估结论**：技术方案 §11.1 逐字「**只报告，不自动补齐关系**」——该口径本身就把
R5 限定为只读检查，实现面 = 「文件路径的领域段」与「对象隐含领域」的一次比对，
无新写口、无新 schema，故 M4 落地。

**判定**（命中任一即产出 `domain_moved`）：

1. 文件所在的领域目录段 ≠ 对象 frontmatter / ID 所隐含的领域；
2. Git 历史显示该文件发生过**跨领域目录**的 rename（`git log --follow --name-status` 的 `R` 记录，
   且新旧路径的领域段不同），且该 rename 的 commit **不是** `eg` 产生的（外部编辑）。

**只报告**：**不**移动文件、**不**改写 frontmatter 领域字段、**不**补关系。
`targets[]` = `[对象 ID, 旧领域, 新领域]`（三元，顺序固定）。

## 9. R6 — 综述可能失准标记（M4 收窄落地）

**逐项评估结论**：技术方案 §11.1 把 R6 写为「写 `stale` / `stale_reason`，
**自动失准判定为 S3 之后逐步补**」。本合同据此**收窄**：M4 只实现**一条**显式触发条件，
其余失准情形（语义漂移、覆盖不足等）明确留给 S3 之后。

**M4 唯一触发条件**：综述所引用的知识卡中，**存在**至少一张满足下列任一条的卡：

- 该卡的 `updated_at` **晚于**综述的 `updated_at`；
- 该卡在综述生成后被**逻辑删除**（`deleted_at` 非空）或**失效**（`status: deprecated`）。

**动作**：给综述 frontmatter 写 `stale: true` 与 `stale_reason: <中文单句>`，
经**内存 ChangePlan** → `internal/plan` → `internal/store` 落盘；产出 `recap_stale` finding。

- `stale_reason` 取值**封闭三值**：`引用卡已更新` / `引用卡已逻辑删除` / `引用卡已失效`；
  多因并存时按此顺序取**第一个**命中值（可复算）。
- **幂等**：已是 `stale: true` 且 `stale_reason` 相同 → 零写入、零 commit，finding 仍产出。
- **不自动重算综述、不自动清除 `stale`**。清除 `stale` 属用户重新生成综述的路径，M4 不做。
- `stale` / `stale_reason` 是**综述专属**的两个新 frontmatter 键，**不得**出现在知识卡 /
  材料笔记 / 材料 / 提案上。反证由 T-…-055 用例锁死。
- **是否需要 `--user-request`** 见 **A-34**（reconcile 写核心知识数据的授权定性）。

## 10. R7 — 材料支持不足实时判定（M4 落地，永不改 status）

**逐项评估结论**：技术方案 §11.1 逐字「查询 / 对账**实时判定**，只报告或视图提示，
**永不改 `status`**」。「实时判定 + 不落盘」意味着零迁移成本、零 schema 改动，故 M4 落地。

**判定**：知识卡的有效 `support` 关系数为 `0`（有效 = 对端存在、未逻辑删除）。
**动作**：产出 `support_insufficient` finding（warning / W20）。

- **永不改 `status`**：`grep -rn "SetStatus\|StatusDeprecated" internal/reconcile/ | wc -l` → `0`。
- **不落盘任何标记**：`[材料支持不足]` 是**视图提示**，由 `eg reconcile` / `eg check` 的
  人类可读输出渲染，**不写进任何 Markdown**。反证：
  `grep -rn "材料支持不足" internal/reconcile/ internal/store/ | grep -v _test.go | wc -l` → `0`（store 侧恒 0）。
- 该判定与 M3 `eg delete` 路径已有的 `support_check[].recommendation`（A-28 路径分治）**互不覆盖**：
  前者是对账的只读 finding，后者是删除报告的建议清单，两者字段不同、路径不同。

## 11. 报告 `reconcile` 字段（§11.2 落地）

```json
"reconcile": {
  "ran": true,
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "findings": [
    {"check": "duplicate_id", "severity": "error", "targets": ["k-a1", "k-a1"], "detail": "…"}
  ]
}
```

- **恰三键** `ran` / `commit` / `findings`，不增不减。
- `ran`：bool，必填。`commit`：string 或 `null`（零改动时为 `null`）。
  `findings`：数组，**非 null**（空时为 `[]`）。
- **`reconcile` 是 S3 阶段键，不进 §4.6 报告体 S1 必填集合**。
  §4.6 的 **S1 必填 11 项**一字不动；非对账命令路径下 `reconcile` 恒为
  `{"ran": false, "commit": null, "findings": []}`（**键在、值为空**，不得整键缺席）。
- **不新增 `affected` 顶层键**：M3 的 A-28 把 `affected` 与 `reconcile` 一并挂在 S3/M4，
  但技术方案 §11.2 只点名 `reconcile` 三键，`affected` 无任何字段级定义。
  M4 **不实现** `affected`，并在 §16 以 **A-36** 登记待 owner 补齐定义。
- `skipped[]` 与 `warnings[]` 仍按既有口径产出；R2 / R6 被 B3 拦下的写入进
  `skipped[kind=file_changed]`，**不新增 kind**。

## 12. `eg reconcile` 命令合同

```
eg reconcile [--json] [--dry-run]
```

| 项 | 口径 |
|-|-|
| 是否写入 | **是**（R1 的 commit + R2 / R6 的 frontmatter 补写） |
| commit | **恰 0 或 1 次**（零改动即零 commit） |
| 退出码 | `0` 成功（含「有 warning 级 finding」）；`1` 参数非法；`2` 存在 **error 级 finding**（`duplicate_id` / `dangling_ref` / `relation_target_missing` / `relation_prefix_invalid`）时，**先完成 R1 纳管 commit**再退 `2`；`3` 有写入被 B3 跳过；`4` Git 提交失败 |
| `--dry-run` | 只跑检查与报告，**零写入零 commit**；`reconcile.ran = true`、`reconcile.commit = null` |
| 退出码 `5` / `6` | **不使用**（`5` 属 S5；`6` 是「仅缺确认」语义，对账无确认前置） |
| 输出信封 | 复用 §3 五键 envelope，`data` 内含 §11 的 `reconcile` 对象 |

- **不作为写命令前置**（§0.1 第 3 条）。
- **不接受对象参数**：M4 的 `eg reconcile` 是**全库**对账，不支持 `--target` / `--domain` 收窄
  （收窄属性能优化面，与 S4 索引同期，M4 不做）。

## 13. `eg check` 命令合同

```
eg check [--json]
```

| 项 | 口径 |
|-|-|
| 是否写入 | **否**——`eg check` 是 `eg reconcile --dry-run` 的**只读别名子集**：只跑 R3 / R4 两组结构检查（重复 ID、悬空引用、孤儿、关系异常），**不跑** R1 / R2 / R5 / R6 / R7 |
| commit | **恒 0 次** |
| 退出码 | `0` 无 error 级 finding；`1` 参数非法；`2` 有 error 级 finding（**零写入**） |
| 定位 | §7.1 逐字「`eg check`：S3；**强校验口径 S5**」——M4 只做**报告式**校验，**不**把它挂到任何写命令前 |

**`eg check` 与 `eg reconcile --dry-run` 的差**：前者检查项是后者的**真子集**（只 R3 / R4），
且**永不**产出 `git_uncommitted` / `reviewed_at_missing` / `domain_moved` / `recap_stale` /
`support_insufficient` 五个 `check`。反证由 T-…-059 表驱动用例锁死。

- **退出码 `2` 的定性**见 **A-31**：`2` 在 S1 合同里的语义是「校验失败（仅 error 级，零写入）」，
  `eg check` 复用该语义**不引入新退出码**，但需 owner 确认这不构成「强校验提前到 S3」。

## 14. 命令数与枚举计数的变更面（供门禁逐项复算）

| 计数项 | M3 收口值 | M4 目标值 | 说明 |
|-|-|-|-|
| `eg` 命令数 | 18 | **20** | 新增 `eg reconcile`、`eg check` |
| `plan.AllOpNames` | 16 | **16 或 17** | R2 / R6 若复用既有 `mark_reviewed` + 新增 `set_stale`，则 17；见 **A-33** |
| `model.KnownVerbs` | 8 | **8 或 9** | 见 **A-30** |
| 诊断码（E/W/I 域） | `E1..E10 ∪ W1..W12 ∪ {I1}` | `E1..E14 ∪ W1..W20 ∪ {I1,I2}` | 新增 13 个 |
| 诊断码（Q 域，只读查询） | `Q1..Q3` | **`Q1..Q4`** | 新增 1 个（`Q4`，见姊妹合同 §3.5.1）；合计新增恰 14 个 |
| `skipped[].kind` | 恰 2 值 | **恰 2 值（不变）** | 不新增 |
| 退出码 | `0/1/2/3/4/6` | **不变** | `5` 仍不启用 |
| e2e 脚本数 | 25 | **只增不减** | M4 新增脚本数由 M-004 判据钉死 |

## 15. 明确不做（M4 边界，逐条排除）

- **`.index/` / SQLite / FTS5 / `eg bench` / P95 / 10,000 卡语料门槛** — S4 / M5。
- **强原子、`run.lock` / `txn/`、崩溃恢复、块级安全合并、退出码 `5`、写前对账** — S5 / M6。
- **四条 Deferred 跨领域需求** — 一字不改，M4 不认领。
- **`affected` 顶层报告键** — 无字段级定义，登记 A-36。
- **自动修复 R3 / R4 / R5 / R7 的任一问题** — 只报告。
- **自动重算综述、自动清除 `stale`** — 不做。
- **`eg reconcile --target` / `--domain` 收窄、并发对账、增量对账** — 不做。
- **物理删除、破坏性回滚**（U-01 / U-02） — 任何阶段任何授权都不做。
- **修改 M1 / M2 / M3 的历史 Activity Log、历史门禁与既有 manifest 成员** — 不做；
  M4 一律使用**独立 manifest** `M4_TASK_IDS`（`048`–`063`）与独立门禁脚本。

## 16. 待 owner 裁决 / 登记（A-30 ~ A-36）

编号顺延 M-003 的 A-25 ~ A-29。**A-30 ~ A-35 为阻塞型**（T-…-048 必须关闭，
未关闭则如实写「未知 / 待 owner 确认」并阻断硬下游）；**A-36 为登记型**（不阻塞开工）。

| 编号 | 冲突 / 待定事项 | 封闭选项 | 影响面 | 关闭条件 |
|-|-|-|-|-|
| **A-30** | `eg reconcile` 的 commit `verb`：`model.KnownVerbs` M3 收口**恰 8** 值封闭，且 `model_test.go` 有 `len(seen) != 8` 型硬断言 | ① `新增 verb reconcile`（第 9 值，需改 M2/M3 已验收用例 → 触发「M1/M2 用例一条不得删改」豁免申请）；② `复用 process`（零改动，但 `git log` 里对账与 `eg apply` 不可区分） | T-…-048 / 050 / 058 / 062 | owner 二选一<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §3 —— 结论 = `新增 verb reconcile`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |
| **A-31** | `eg check` 发现 error 级 finding 时退 `2` 是否构成「强校验（S5）提前到 S3」 | ① `退 2`（复用 S1 语义「校验失败，零写入」）；② `恒退 0`（纯报告，CI 无法据此阻断） | T-…-048 / 059 | owner 二选一<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §3 —— 结论 = `退 2`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |
| **A-32** | §4.3 不持久化 SHA 的**落地形态**（见姊妹合同 §1） | ① `整键删除`（写侧不再产生 `execution.git_commit`，读侧宽容既有键）；② `保留键恒 null`；③ `改存非 SHA 回执` | T-…-048 / 060 | owner 三选一<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §3 —— 结论 = `整键删除`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |
| **A-33** | R2 / R6 的写入走哪个 op：复用 M3 的 `mark_reviewed` + 新增 `set_stale`，还是新增单个 `reconcile_fix` 复合 op | ① `mark_reviewed + set_stale`（op 数 16 → 17）；② `单个 reconcile_fix`（op 数 16 → 17，但语义混合）；③ `mark_reviewed + replace_block 复用`（op 数不变） | T-…-048 / 051 / 055 | owner 三选一<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §3 —— 结论 = `mark_reviewed + set_stale`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |
| **A-34** | R6 写 `stale` / `stale_reason` 是否属「用户显式改核心知识数据」，从而是否需要命令行 `--user-request` | ① `需要`（与 `eg edit` 同口径）；② `不需要`（对账是系统派生标记，非用户内容） | T-…-048 / 055 | owner 二选一<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §3 —— 结论 = `不需要`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |
| **A-35** | R1 纳管 commit 与 R2 / R6 修复写入是否合并为**恰一次** commit | ① `恰一次`（本合同 §4 暂定口径，与 `reconcile.commit` 单值字段一致）；② `允许两次`（需把 `commit` 改成数组，扩张 §11.2 字段） | T-…-048 / 050 / 058 | owner 二选一<br>**2026-11-15 已由 T-…-048 关闭**：见 `docs/specs/2026-11-15-m4-prestart-adjudication.md` §3 —— 结论 = `恰一次`；裁决人 = Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）（既有结论与选项原文不改） |
| **A-36** | 权威侧 §11.1 逐字「R3 / R5 / R6 / R7 **S3 之后**逐步补」，与本仓 M4 采用的「七项全落、R3/R5/R6/R7 只报告不自动修」口径不一致；且 §4.6 的 `affected` 键无字段级定义 | 本仓口径：**七项全落 + 只报告**；`affected` **不实现** | 全 M4 | 登记型：待 owner 在 §11.1 补阶段脚注、在 §4.6 补 `affected` 字段定义。**本仓不改飞书文档** |

## 17. 开工前 checklist（M4 各 task 开工首日逐项确认）

1. 读 `milestones/M-004-m4.md`——M4 范围、明确不做、完成判据、依赖图、排期、风险。
2. 读本合同 §1（包边界）、§2 / §3（Finding schema 与十二值枚举）、§11（报告字段）。
3. 读姊妹合同 `2026-11-12-m4-visibility-and-execution-contract.md` 全文（两项 owner 新裁决的落点）。
4. 读 `docs/specs/2026-10-13-m3-prestart-adjudication.md` §7（A-23 / A-24 的 10 条附加约束，M4 一字不放宽）。
5. 读 `docs/specs/2026-11-11-m3-acceptance-report.md` §F.1 / §F.2（K-041-01 / K-043-01 两项 M3 遗留，M4 负责关闭）。
6. 确认 T-…-048 的 A-30 ~ A-35 已关闭；未关闭则本 task **不得**进入 `ready`。
7. 只读核对代码现状，不得把已有能力当新能力：
   - `internal/query/scan.go`（全量扫描底座，R1–R7 一律复用，不另写扫描器）；
   - `internal/query/card.go:215` `VisibleEndpoints`（当前**只**过滤已删除端点）；
   - `internal/git/`（只读 API 面）；
   - `internal/store/` 的 B3 `content_hash` 前置比对（**M4 不得削弱**）。

## 18. 附录 · M7（`knowledge_opinion_split`）additive addendum（2026-09-19 · 裁决 A-62）

**本节为 additive 追加，M4 的历史结论一字不改，只登记 Schema v2 / M7 阶段对本合同的向后兼容增量。**
增量来源：Epic `knowledge_opinion_split` 的前置裁决
`../../../knowledge_opinion_split/docs/specs/2026-09-19-opinion-unsupported-validated-adjudication.md`（A-62），
为 schema-v2 §6.4「validated 观点零支持证据」落地一条**新增 R3 子检查** `opinion_unsupported_validated`。

- **对 §3（`check` 十二值封闭枚举与诊断码单射）**：M4 收口时 `check` 恰 **12** 值封闭、`W` 段恰止于 `W20`、
  `W21` 不再分配（回到对账域）——**该历史结论不变**。M7 起对账 check 表 **additive 扩至 13**：新增第 13 行
  `opinion_unsupported_validated`（R3 · `warning`），诊断码取**当前全局下一空号 `W29`**（M7 中 `W21` 已由
  `internal/plan/` 占用、`W22`–`W28` 各有域主，故 `W29` 是下一空号；**不复活 `W21`**，与 §3「`W21` 不再
  分配（回到对账域）」一致）。`SeverityCount` 恒 2、`check ↔ 码`仍双向单射（13 对）。
- **对 §7（R3 关系校验）**：R3 子检查由 **4 → 5**；新子检查仍**只报告、零 RepairSpec、不自动修**，且
  **不改 F4**（关系类型集合恒 8 值封闭，不新增关系类型 / 字段）。「有效支持」= 指向本观点的 incoming
  `supports` 边、持有方存在且未逻辑删除、重复边去重计一次、nil scan 不判、正向 supports 不计入
  （方向 / 有效性细则见 A-62 §2）。
- **对 §13（`eg check` 命令合同）**：`eg check` 仍是「只跑 R3 / R4」的只读子集、**永不**产
  `support_insufficient`/W20（R7 语义不变）——**该历史结论不变**。因新码归 **R3**，`eg check` 按 R∈{R3,R4}
  过滤**自动**纳入 `opinion_unsupported_validated`（W29），满足 A-62 Acceptance「`eg check` 与
  `eg reconcile` 都检出」。`cli.CheckScopeCount` 因此 **7 → 8**（R3 五 + R4 三），`CheckExcludedCount` 仍
  **5**（R1/R2/R5/R6/R7）。
- **对 §14（计数变更面）**：M7 additive 增量——`reconcile.CheckCount` 12→**13**、`R3SubcheckCount` 4→**5**、
  `cli.CheckScopeCount` 7→**8**、新增诊断码 **W29** 一个；`SeverityCount`(2) / `OrderedTargetsCheckCount`(1) /
  `F4RelationValueCount`(8) / 已落地 R 规则数(7) / `skipped[].kind`(2 值) / 退出码全集 **均不变**。
  代码 / 测试 / 夹具落地归 T-…-007 的 7C 代码批，本附录不改任何代码。
