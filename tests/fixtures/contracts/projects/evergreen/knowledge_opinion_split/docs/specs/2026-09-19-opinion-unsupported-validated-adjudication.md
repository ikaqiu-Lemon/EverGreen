---
title: 观点「已验证但零支持证据」对账判据前置裁决（A-62 · 7C0，Teamwork-only 文档批）
task: T-evergreen.knowledge_opinion_split-158614-007
epic: knowledge_opinion_split
created: '2026-09-19'
status: closed
adjudicator: Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）
---

# 观点「已验证但零支持证据」对账判据前置裁决（Schema v2 · Epic `knowledge_opinion_split` · 交付 T-…-007）

**唯一职责**：为 T-007 Acceptance 第 5 条「一条 `validated` 但无 `supports` 的 Opinion 被 `eg check` /
`eg reconcile` 检出」定出可落地、可复算、可回滚的**单条裁决 A-62**，作为后续 **7C 代码批**（新增 R3 子检查
`opinion_unsupported_validated`）的开工输入。**本文件不写代码，`evergreen/` 下零改动**；仅在本 Epic 新增本
裁决文档，并对 schema-v2 §6.4 与历史 M4 对账合同 §3/§7/§13/§14 各作**带日期的 additive addendum/引用**，
不改写任何历史结论。

**关联需求 / 契约**：schema-v2 设计 §6.4「必须能看到支持、限制和反对它的内容」；T-007 Scope/Acceptance；
历史 M4 对账合同 `../../../s1_main_flow/docs/specs/2026-11-12-m4-reconcile-contract.md` §3/§7/§13/§14。

## 0. 授权边界与代拍留痕规则（先读这一节）

T-007 的判定权归 owner；2026-09-05 owner（`项目维护者` / `maintainer` / `maintainer@example.com`）给出的是**概括性推进
授权**——逐字「允许开始实施任务，逐个实施任务，完成后立即开始下一个任务，不需要我的确认，直到你完成所有
任务」——**这不是逐项裁决**。本裁决沿用历史 `2026-11-15-m4-prestart-adjudication.md` §0 的四条代拍规则，
规则本身也是本文件的验收面：

1. **结论列必须落在封闭选项内**：A-62 的「裁决结论」取值于该项自身的封闭选项集合；不填
   `未知 / 待 owner 确认`——那会依 `AGENTS.md` 的硬依赖语义阻断 7C 代码批与 T-007 收口，与 owner 推进
   指令直接冲突。
2. **裁决人列逐字留痕代拍事实**：一律逐字写 `Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决）`。
   **严禁**把 Agent 代拍写成 owner 的逐项裁决——「判定权归 owner」这条口径在本文件里不是被推翻，而是被
   改写成「可以代拍，但必须逐条标明是代拍、并给出可反证的依据与可回滚的范围」。
3. **每项都必须有代码实证**：结论取自 §1 的只读实证（`evergreen@9d137db` 实测），而非「合同暂定」或「看
   起来合理」。实证与合同 / task 的建议或前提冲突时**以实证为准**，并在 §6 逐条登记差异。
4. **每项都必须给出回滚范围**：owner 事后改判时，受影响的文件 / 判据 / task 在 §5 逐项列明，保证改判成本
   可估、且不需要重做实证。

**本文件不做的事**：不改 `evergreen/` 任何源码 / 测试 / 门禁；不改 schema-v2 §6.4 既有句子（只追加带日期
附录）；不改写历史 M4 对账合同 §3/§7/§13/§14 的既有结论（只在文末追加一节 additive addendum 并引用之）；
不新增关系类型（F4 恒 8）；不新增 R 规则（本判据归 **R3**，不新增 R8）；不触碰 R7/W20 的知识卡语义；
不进入 7C 代码实现（12→13 扩表、测试、`eg check` 纳入面留给 7C 批，须在本裁决落地后进行）。

## 1. 只读代码实证复核（2026-09-19 在 `evergreen@9d137db` 实测，非推断）

裁决的事实底座。每行都是可复跑的只读事实，**本 task 未改动其中任何一个文件**。

| # | 实证点 | 实测事实 | 用于 |
|-|-|-|-|
| E-1 | `internal/reconcile/check.go:64` / `:68` / `:66` / `:120` | `CheckCount = 12`；`CodeCount = CheckCount`；`SeverityCount = 2`；`OrderedTargetsCheckCount = 1` | A-62 计数面 |
| E-2 | `internal/reconcile/check.go:85` | `checkTable` 是 `[CheckCount]CheckSpec` **定长数组**——多写第 13 行编译期 `index 12 out of bounds`、漏写留零值行被 `check_test.go` 判红 | A-62 扩表落点 |
| E-3 | `internal/reconcile/check.go:91`-`:94` | R3 现四行：`relation_target_missing`(E13)、`relation_prefix_invalid`(E14)、`relation_opposing_asymmetric`(W15)、`relation_duplicate`(W16)，`R` 列均 `"R3"` | A-62 归属 R3 |
| E-4 | `internal/reconcile/r3_relation.go:79` / `:107` | `R3SubcheckCount = 4`；`F4RelationValueCount = 3 + 4 + 1`（= **8**，材料三值 + 论证四值 + `replaced_by`），且注释逐字「R3 不改 F4」 | A-62 R3 子检查数 / F4 不变 |
| E-5 | `internal/model/enums.go:292`-`:300` | 论证关系封闭四值 `derives` / `supports` / `limits` / `opposing`；`ValidRelationTypes()` 恰这四值（`replaced_by` 属生命周期字段，不在内） | 有效 supports 取值 |
| E-6 | `internal/cli/rel.go:46` / `:61` | `eg rel add <from> <type> <to>` 的方向语义：一条 `supports` 边逐字表达「**from 支持 to**」（`A --supports--> B` = A 支持 B）；`opposing` 单向存储（A-24） | 方向裁决锚点 |
| E-7 | `internal/query/opinion_show.go:236`-`:248` / `:106`-`:112` | 观点关系视图分**正向**（`opinionForwardEdges`：以本观点为起点，`o → target`）与**反向**（`opinionReverseEdges`：全库扫描 `target == o-id`，来源面 = Knowledge ∪ Opinion，`source → o`）；`supports`/`limits`/`opposing` 各组 `RelationGroup{Forward,Reverse}` | 有效 supports 方向 |
| E-8 | `internal/reconcile/r7_support.go:8`-`:16` / `:229` | R7/W20 的判定面是**知识卡** frontmatter `sources[]` 里 `rel == support` 的**材料关系**（`model.MaterialSupport`），与观点 `relations[]` 的**论证** `supports` 是两件不同的落盘事实；R7「有效 = 对端存在且未逻辑删除」 | 不复用 W20；有效性口径参照 |
| E-9 | `internal/reconcile/r3_relation.go:60`-`:64` | R3 target 存在性「看落盘事实，不做 status / deleted_at / 可见性过滤」；`Scan == nil`（未取数）时四项整体零产出 | 有效性 / nil scan 口径 |
| E-10 | `internal/cli/check.go:54`-`:65` / `:68` | `eg check` 检查面 = `checkScopeR = [2]{reconcile.R3, reconcile.R4}`，`CheckScopeCount = 7`（R3 四 + R4 三），`CheckExcludedCount = CheckCount - CheckScopeCount`；finding 按 R∈{R3,R4} 过滤，**集合外一律丢弃** | 「`eg check` 也检出」自动成立 |
| E-11 | `internal/plan/diagnostics.go:55` | `W21` 已被 **plan 域**占用（`--strict` 结构覆盖诊断，`strict_exempt.go` 豁免表），**不属对账 check** | W29 取号，W21 保持 plan |
| E-12 | `index/consistency.go:28`、`index/corrupt.go:40`/`:43`、`query/page.go:50`、`txn/recover.go:56`、`mdfile/block_merge.go:25`、`txn/doc.go:62` | `W22`(索引陈旧)、`W23`(索引缺失)、`W24`(索引损坏)、`W25`(分页截断)、`W26`(事务恢复)、`W27`(块合并)、`W28`(锁等待)各有域主；`grep -rnE '"W29"' internal/ --include=*.go \| grep -v _test.go` → **空**（`W29` 全局未分配） | W29 = 当前全局下一空号 |
| E-13 | `internal/cli/codes.go:24` 注释 | 逐字登记「`W1`–`W20`、`W22`–`W28`」已在盘——`W21` 单列于 plan、`W29` 未登记 | 佐证 E-11/E-12 |

**一句话**：Acceptance 要求 `eg check` 必检出，而 `eg check` 结构上只跑 R3/R4、**永不**产 R7/W20（E-8/E-10），
故此判据**不能**复用 R7/W20，**必须**落为真正的 **R3 子检查**；这必然把封闭表 12→13（E-1/E-2），属对 M4 §3
冻结表的正式修订，须先显式裁决 + 文档化（本文件），**严禁静默扩表**。

## 2. 「有效 supports」的方向与有效性裁决（本裁决的语义内核）

判据文案（schema-v2 §6.4 逐字）：「一条 Opinion 若 `validation: validated` 但**零条** `supports` 关系，
`eg check` 报 `R3` 类关系异常——『已验证的观点没有任何支持证据』」。文案未给方向 / 有效性细节，本节据
现有关系方向语义（E-6/E-7）与既有对账有效性口径（E-8/E-9）逐项裁定，供 7C 一比一实现。

### 2.1 方向：只认 incoming（反向，`X --supports--> O`）

- **裁决**：一条**有效支持证据**恒是一条**指向本观点**的 `supports` 边——即以本观点为 `target` 的反向边
  `X --supports--> O`（E-7 的 `Reverse` 段；E-6 的「from 支持 to」语义下，`X` 支持 `O`）。
- **outgoing（正向，`O --supports--> Y`）不计入**：那是「本观点作为他者的支撑证据」，语义上是 `O` 在支持
  `Y`，**不构成** `O` 自身「被支撑」。schema-v2 §6.4 的展示视图把正反向都渲染在「支持」组下（E-7），那是
  **可读性**需求；「支持**证据**」在判定语义上只能是流入本观点的边。
- **被否决的替代**：「正反向任一条 `supports` 即算被支撑」——会把「本观点去支撑别人」误当成「本观点被支撑」，
  与「没有任何支持证据」直接矛盾；且会让一条只对外发散、无人支撑的 validated 观点逃过判定。

### 2.2 有效性：对端存在且未逻辑删除；不做 deprecated 展示面过滤

对一条 incoming 边 `X --supports--> O`（`X` 是持有方，`O` 是本观点），**有效**当且仅当：

1. **持有方 `X` 落盘存在**：反向边由 `X` 的 `relations[]` 承载，`X` 必是被全库扫描到的落盘对象，故此条恒
   成立（不存在「incoming 边的来源缺失」这一形态——来源缺失只可能出现在**正向**边的 target 侧，那属 E13，
   且与本判据无关）。
2. **持有方 `X` 未逻辑删除**（`deleted_at == null`）：与 R7「有效 = 对端存在且**未逻辑删除**」逐字同源
   （E-8）。一条来自已逻辑删除对象的支持边不构成 live 支撑。
3. **不因 `X` 为 `deprecated` 而失格**：deprecated 是**展示面**维度（默认隐藏 + Q4），R3 明文「展示面过滤
   **不得**渗进对账判定」（E-9），R7 的有效性也**只**过滤逻辑删除、不过滤 deprecated（E-8）。故一条来自
   `deprecated` 但未逻辑删除对象的支持边**仍计为有效**。此为「与既有对账口径一致」的选择，owner 若要求
   「deprecated 支持不算数」见 §5 回滚。

**missing / deleted / deprecated 三态归一表**（针对 incoming 支持边的持有方 `X`）：

| `X` 的状态 | 是否计入有效支持 | 依据 |
|-|-|-|
| 存在且 active | ✅ 计入 | E-7/E-8 |
| 存在但 `deprecated`（未删除） | ✅ 计入（不做展示面过滤） | E-8/E-9 |
| 已逻辑删除（`deleted_at != null`） | ❌ 不计入 | 与 R7 同口径（E-8） |
| 「缺失」 | 不适用（incoming 边来源恒存在；正向 target 缺失属 E13，与本判据不相交） | E-9 |

### 2.3 重复边、nil scan、本观点自身的三条边界

- **重复边**：`X --supports--> O` 若被写了多条（含外部编辑绕过写侧幂等），按规范化 `(from, supports, target)`
  **去重后计一次有效支持**。因本判据只关心「有效 incoming 支持数是否为 0」的**布尔**结论，去重不改变结论
  （≥1 仍 ≥1）；规定去重是为与 W16（`relation_duplicate`）**不重复计数 / 不相互顶替**：重复本身仍由 W16
  独家报，本判据只读「是否至少一个不同来源在支撑」。
- **nil scan（未取数）**：`Scan == nil` 时本子检查**整体零产出**，与 R3 现状（E-9）逐字一致——「没取数」
  不产 finding，绝不把「没扫到支持」误判成「零支持」。
- **本观点 `O` 自身的资格**：
  - 仅对 `validation == validated` 的观点判定；`pending` / `rejected` 一律不判。
  - `O` 若**已逻辑删除**（`deleted_at != null`）→ 跳过不判（与 R7 跳过已删除卡同口径）。
  - `O` 的 ID 若**重复**（同一 `o-id` 落在 ≥ 2 个文件，属 R4 `duplicate_id`/E11）→ **整体让位 E11**、
    本判据不判（与 R7/R5 对 `duplicate_id` 的让位同口径），避免一件 ID 冲突事实被两码各记一次。

### 2.4 判据成立条件（供 7C 逐字实现）

> 对每一条满足 §2.3 资格的 `validated` 观点 `O`：统计**有效 incoming 支持数** = 全库中
> `type == supports 且 target == O 且 持有方未逻辑删除` 的边、按规范化 `(from,supports,O)` 去重后的条数。
> **该数为 0** → 产一条 `opinion_unsupported_validated` finding（**W29 / warning**），`targets[] = [O 的 ID]`
> （默认集合语义，非顺序固定）。**零 RepairSpec、零自动降级 validation、零落盘、零 commit**（只报告）。

## 3. 单条裁决（A-62）

列 = `编号 / 冲突摘要 / 封闭选项 / 裁决结论 / 裁决人 / 裁决日期 / 受影响 task / 机器反证 / Agent 建议裁决`。
第 4 列取值**必须**来自该行封闭选项集合；第 9 列是 Agent 依 §1/§2 实证给出的推荐值，与第 4 列**严格分离**
（本轮两列同值，因为代拍就是照推荐值落的，且推荐值有 §5 的依据链）。

| 编号 | 冲突摘要 | 封闭选项 | 裁决结论 | 裁决人 | 裁决日期 | 受影响 task | 机器反证（7C 落地后逐项复算） | Agent 建议裁决 |
|-|-|-|-|-|-|-|-|-|
| **A-62** | 「`validated` 且零 `supports` 证据」如何落为对账判据：既要 `eg check` 与 `eg reconcile` 都检出（Acceptance 5），又要不破坏 M4「`check` 恰 12 值封闭、`eg check` 只跑 R3/R4 永不产 R7/W20、W 段恰止于 W20」的历史结论（E-8/E-10；M4 §3/§13） | ① `新增 R3 子检查 opinion_unsupported_validated（warning=W29）`；② `复用 R7/W20`；③ `只走 eg reconcile 人类可读输出，不进 check 枚举` | `新增 R3 子检查 opinion_unsupported_validated（severity=warning，码=W29）` | Agent 代拍（owner 2026-09-05 概括性授权，非逐项裁决） | 2026-09-19 | 007 | ①`reconcile.CheckCount == 13` 且 `CodeCount == 13`；②`reconcile.R3SubcheckCount == 5`；③`reconcile.SeverityCount == 2`、`OrderedTargetsCheckCount == 1`、`F4RelationValueCount == 8` 均**不变**；④`checkTable` 第 13 行 `{opinion_unsupported_validated, warning, W29, R3}`、`grep -rnE '"W29"' internal/ --include=*.go \| grep -v _test.go` 由 0 变为已分配、`W21` 仍只在 `internal/plan/`；⑤`cli.CheckScopeCount == 8`（R3 五 + R4 三）、`CheckExcludedCount == 5` 不变；⑥ 一条 `validated` 且零有效 incoming supports 的观点，`eg check --json` 与 `eg reconcile --json` **都**产恰一条 `opinion_unsupported_validated`（W29），且该命令 `git status --porcelain` 为空、commit 数不变；⑦ 该 finding 零 `RepairSpec`、不改任何 `validation` | `新增 R3 子检查 opinion_unsupported_validated（warning=W29）` |

**选项②③被实证排除**：② `eg check` 结构上永不产 R7/W20（E-10「finding 按 R∈{R3,R4} 过滤，集合外丢弃」+
M4 §13 反证用例），复用 R7/W20 无法满足 Acceptance 5「`eg check` 也检出」，且会污染 W20 的知识卡材料语义
（E-8）；③ 不进 check 枚举则 `eg check --json` 的机器消费方拿不到该码，Acceptance 5「被 `eg check` 检出」
不成立。故只有 ① 可行。

## 4. 「有效 supports」裁决蕴含的连锁改动与回滚范围（供 7C 逐项落地，本批不实现）

「重钉」= **按实测改期望值、判据本体一字不动**；严禁删判据、加白名单、frozen-red、删弱断言、吞失败或降级缺陷。

### 4.1 代码连锁（`evergreen/`，全部留给 7C）

| 落点 | 改动 | 保持不变 |
|-|-|-|
| `internal/reconcile/check.go` | 新增 `CheckOpinionUnsupportedValidated = "opinion_unsupported_validated"`（第 13 行 · R3）、`CodeW29 = "W29"`；`CheckCount` 12→13（`CodeCount` 随动）；`checkTable` 追加 `{…, SeverityWarning, CodeW29, "R3"}`；注释「十二值」→「十三值」、W 段说明补 W29 | `SeverityCount = 2`；`OrderedTargetsCheckCount = 1`（新 check 的 `targets` = `[o-id]`，默认集合语义，不进 `orderedTargetsTable`） |
| `internal/reconcile/r3_relation.go` | `R3SubcheckCount` 4→5；`r3SubcheckTable` 追加新子检查；新增 `checkR3OpinionUnsupportedValidated`（§2.4 判定：反向 supports 扫描 + 持有方未逻辑删除 + 去重 + validated/未删/非 duplicate_id 资格闸），并入 `checkR3Relation` 的产出（仍**一项注册**） | `F4RelationValueCount = 8`（不新增关系类型 / 字段）；R3 现有四子检查语义一字不动 |
| `internal/cli/check.go` | `CheckScopeCount` 7→8（R3 五 + R4 三）；`CheckScopeNotice` 等固定文案「恰 7 个 check」→「恰 8 个 check」；`7 + 5 = 12` 封闭等式→`8 + 5 = 13` | `checkScopeR = [2]{R3,R4}` 不变；`CheckExcludedCount = CheckCount - CheckScopeCount`（= 5，仍 R1/R2/R5/R6/R7）随动不变；`eg check` 自动纳入新 R3 码（无需改过滤逻辑） |
| （实现细节，无独立常量） | 判定需要「对象 ID → 是否逻辑删除」集合与「validated 观点集合」：由 `scan.Cards[].Deleted` / `scan.Opinions[].Deleted` / `scan.Opinions[].Validation` 折算，复用 `collectRelationFacts` 的关系事实与 `StructureIndex` | 不新写扫描器（复用 `internal/query` 底座）；`internal/reconcile` 依赖方向不变（仅 model/mdfile/git 只读/query） |

### 4.2 测试连锁（`evergreen/`，全部留给 7C，先红后绿、不 skip / 不 allowlist）

- `internal/reconcile/check_test.go`：`TestCheckEnumExactlyTwelveValues`→Thirteen、`TestCheckEnumRejectsThirteenthValue`→Fourteenth、`TestCheckDiagnosticBijection`（13 对单射）、severity/ordered-targets/F4 计数不变用例。
- `internal/reconcile/r3_relation_test.go`（及新增用例）：`R3SubcheckCount == 5`；正例（有效 incoming 支持 ≥1 → 不产）、反例（validated 且零有效 incoming → 产 W29）；边界：**正向 supports 不计入**、来自**已逻辑删除**持有方的支持不计入、来自 **deprecated** 持有方的支持**计入**、重复边去重后计一次、`Scan==nil` 不产、`pending`/`rejected` 不判、已删除观点跳过、`duplicate_id` 观点让位 E11、与 W16/E13 不重复计数。
- `internal/cli/check_test.go`：`CheckScopeCount == 8`、scope/excluded notice 逐字断言。
- e2e / check-set 枚举脚本（如 `test/e2e/*` 与 `tools/` 的 check 全集断言）：按 13 值 / R3 五子检查重钉。
- `tests/manifest/*`（`inventory.tsv` / `suites.yaml` / `traceability.tsv` / `coverage.tsv`）：新增 staged 测试后按既有顺序 `traceability.py --write` → `inventory.py --write` → `suites.py --write` 重算，并 `git add` 新测试入 index（否则 materializer git-mode 幽灵条目致 `materialize_safety` 红）。

### 4.3 文档连锁

- **本批（7C0）已做**：本裁决文档（A-62）；schema-v2 §6.4 带日期 additive addendum；历史 M4 对账合同 §3/§7/§13/§14 文末 additive addendum。
- **留给 7C**：`evergreen/tests/fixtures/contracts/…/2026-11-12-m4-reconcile-contract.md` **冻结副本**若被 EverGreen 契约测试逐字比对，须与 Teamwork 侧 addendum 同步（该副本是测试夹具，非本 Teamwork-only 批的改动面）；`README` / `SKILL` 若列举 check 全集或 `eg check` 范围文案，按 13 值 / 8 scope 同步。

### 4.4 owner 改判后的回滚范围

| 若 owner 改判为 | 回滚 / 调整 |
|-|-|
| ②`复用 R7/W20` | 本裁决文档追加修订段（append-only，不重写）；7C 不扩表：`CheckCount` 保持 12、`R3SubcheckCount` 保持 4、`CheckScopeCount` 保持 7、`W29` 释放；改为在 R7 判定面扩「观点论证 supports」——但须同时推翻「`eg check` 也检出」（M4 §13 反证）或另裁 `eg check` 纳入 R7，代价更大。 |
| ③`只走人类可读输出` | 同上不扩表、`W29` 释放；且须推翻 Acceptance 5「被 `eg check` 检出」。 |
| 方向改为「正反向任一即算被支撑」 | §2.1 结论翻转；7C 判定改为「正向 + 反向 supports 合计去重」；机器反证 ⑥ 的夹具随之改；本裁决追加修订段。 |
| 有效性改为「deprecated 支持不算数」 | §2.2 第 3 条翻转；7C 判定在「未逻辑删除」之外加「未 deprecated」过滤（此时与 R3「不做展示面过滤」的口径出现局部例外，须在 7C 用例注释里显式登记该例外）；本裁决追加修订段。 |
| 码号 / severity 改（如定 error 走 E 段） | 改 `checkTable` 该行 severity/码；若定 error 则 `SeverityCount` 仍 2、但需评估 `eg check` 退 2 语义（A-31）是否波及；本裁决追加修订段。 |

**代拍兜底**：A-62 若 owner 事后改判，本文件**只追加修订段、不重写既有结论**（append-only），并由 T-007 收口
（及后续 Epic 验收）逐项复述最终状态。**不得**把改判伪装成「本来就是这么裁的」。

## 5. 每项代拍的依据与「owner 改判后的回滚范围」

| 项 | 实证依据（为什么这样代拍） | 改动面与对历史门禁的冲击 | owner 改判后的回滚范围 |
|-|-|-|-|
| A-62（落为 R3 子检查） | Acceptance 5 要求 `eg check` 必检出，而 `eg check` 结构上只跑 R3/R4、finding 按 R∈{R3,R4} 过滤集合外丢弃（E-10），永不产 R7/W20（E-8 + M4 §13）；故此判据**只能**落为 R3 子检查，必然 12→13（E-1/E-2）。归 R3 而非新 R8：判据本体是「关系（`supports`）一致性」，与 R3 现四项同宇宙（`relations[]`），复用其 `collectRelationFacts`/`StructureIndex`（E-3/E-4）。 | `CheckCount` 12→13、`R3SubcheckCount` 4→5、`cli.CheckScopeCount` 7→8；`SeverityCount`/`OrderedTargetsCheckCount`/`F4RelationValueCount`/R 规则数(7)/R7·W20 语义**全不变**。诊断码取 `W29`（当前全局下一空号，E-11/E-12/E-13），**不复活 W21**（W21 现属 plan），与 M4 §3「W21 不再分配」**不冲突**（W21 从未回到对账域）。 | 见 §4.4。 |
| 方向 = incoming | E-6「from 支持 to」+ E-7「反向 = `target==o-id`」→「支持**证据**」= 流入本观点的边；「没有任何支持证据」= 零有效 incoming。 | 判定只扫反向 supports；正向 supports 与本判据无关（仍照常在 `opinion show` 渲染）。 | 见 §4.4 方向行。 |
| 有效性 = 存在且未逻辑删除、不过滤 deprecated | 与 R7「有效 = 对端存在且未逻辑删除」（E-8）、R3「不做展示面过滤」（E-9）逐字对齐——最小惊讶、口径统一。 | 判定复用逻辑删除维度；不引入 deprecated 过滤，避免与 R3 口径分叉。 | 见 §4.4 有效性行。 |

## 6. 与 task 现文 / schema-v2 现文的对应关系与差异登记（实证 vs 现文；以实证为准）

1. **T-007 Acceptance「四条合法流转」是计数笔误**：schema-v2 §6.1 状态机实列**五条**合法边
   （`pending→validated`、`pending→rejected`、`validated→rejected`、`rejected→pending`、`validated→pending`）；
   已在 T-007 Activity Log（7A kickoff ⑤）登记，机器表以五条为准。**与本裁决无关**，照录不闭合。
2. **schema-v2 §6.4「零条 `supports` 关系」未给方向 / 有效性**：本裁决 §2 以实证补全为「零有效 **incoming**
   支持」，属**细化而非改写**——§6.4 既有句子一字不动，仅追加带日期附录（见 §7 与本批对 schema-v2 的
   addendum）。
3. **M4 §3「W 段恰止于 W20、W21 不再分配」在本 Epic 的现状**：M4 收口时 W21 未分配；**本 Epic** 已把 W21
   分配给 plan 域（E-11），W22–W28 各有域主（E-12）。因此本判据取 **W29**（全局下一空号），既满足
   M4 §3「W21 不再分配（回到对账域）」，又不与任何已分配码冲突。M4 §3 的**历史结论不变**，本裁决只additive
   记「M7 起对账 check 表additive扩至 13，新码 W29」。
4. **M4 §13「`eg check` 永不产 support_insufficient/W20」不变**：本判据不产 W20，产的是**新 R3 码 W29**，
   `eg check` 因 R∈{R3,R4} 过滤而**自动**纳入 W29（E-10）——与 §13 关于 R7/W20 的反证**不冲突**（§13 讲的是
   R7，不是 R3）。

## 7. additive addendum 索引（本批对既有文档的追加，均带日期、不改写历史）

- **schema-v2 设计 §6.4**：在其结论段后追加带日期附录，指向本裁决 A-62，钉定「有效 supports = 有效 incoming」。
- **历史 M4 对账合同**（`../../../s1_main_flow/docs/specs/2026-11-12-m4-reconcile-contract.md`）：文末追加一节
  「附录 · M7 additive addendum（2026-09-19，A-62）」，逐条**引用** §3（12→13、新码 W29、W21 保持 plan）、
  §7（R3 子检查 4→5，F4 恒 8）、§13（`eg check` 经 R3 过滤自动纳入 W29，R7/W20 语义不变）、§14（计数变更面
  的 M7 增量），并声明「M4 历史结论一字不改，本节只追加 M7 增量」。

## 8. 下游开工输入（7C 代码批按此逐项签字）

7C 代码批开工首日在 T-007 Activity Log 逐项确认：

| # | 项 | 本裁决给出的开工输入（逐字） |
|-|-|-|
| ① | check 名 / 归属 / 码 / severity | `opinion_unsupported_validated`，归 **R3**，码 **W29**，severity **warning** |
| ② | 判据成立条件 | validated 观点的**有效 incoming supports 数 == 0**（§2.4）；`targets=[o-id]`；零 RepairSpec、零降级 validation |
| ③ | 有效 supports 定义 | incoming（`X --supports--> O`）；持有方存在且未逻辑删除；deprecated 仍计入；重复边去重计一次；nil scan 不判 |
| ④ | 资格闸 | 仅 `validated`；已删除观点跳过；`duplicate_id` 观点让位 E11 |
| ⑤ | 计数迁移 | `CheckCount` 12→13、`R3SubcheckCount` 4→5、`cli.CheckScopeCount` 7→8；`SeverityCount=2`/`OrderedTargetsCheckCount=1`/`F4=8`/R 规则数=7 **不变** |
| ⑥ | 双命令检出 | `eg check` 与 `eg reconcile` **都**检出（R∈{R3,R4} 过滤自动纳入 W29） |
| ⑦ | 不做 | 不复活 W21、不新增关系类型/字段、不新增 R8、不动 R7/W20 知识卡语义、不自动修 |

## 9. 遗留问题与后续（如实登记，不粉饰）

1. **A-62 为 Agent 代拍**（owner 2026-09-05 概括性授权，非逐项裁决）；owner 改判回滚范围见 §4.4 / §5。
2. **本批为 Teamwork-only 文档批**：`evergreen/` 零改动，12→13 扩表 / 测试 / `eg check` 纳入面 / 夹具同步
   全部留给 **7C 代码批**（须在本裁决落地后进行，先红后绿，不 skip / 不 allowlist / 不 frozen-red）。
3. **EverGreen 契约夹具副本**（§4.3）与 Teamwork 侧 addendum 的同步归 7C；本批不碰 `evergreen/`。
4. **T-007 保持 `in_progress`**：7C reconcile 判据尚未实现，本裁决只解除其「须先显式裁决 + 文档化」的前置阻塞。
