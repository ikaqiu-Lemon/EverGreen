# Schema v2 迁移职责接续到 Storage v3

- 日期：2026-09-22
- 状态：执行约束
- 上游 Epic：`knowledge_opinion_split`
- 接续 Epic：`block_boundary_materialization`

## 1. 当前事实

- `T-evergreen.knowledge_opinion_split-158614-008` 已完成，产品接续 SHA 为
  `ff9868652b685eae4502ad0b546e3912724117c0`。
- `T-evergreen.knowledge_opinion_split-158614-009` 与
  `T-evergreen.knowledge_opinion_split-158614-010` 均为 `created`，没有代码提交、
  没有迁移执行、没有 release/tag。
- 真实 Vault 尚未迁移；`mark-reviewed` 仍按
  `I-evergreen.knowledge_opinion_split-158614-006` 冻结。
- 上游 T-009 写下的 16 Knowledge / 9 Opinion / 25 材料关系 / 29 论证关系是特定
  golden 的审计基准，不是 Storage v3 通用迁移器可凭空生成的固定配额。

## 2. 为什么不直接启动旧 T-009/T-010

旧 T-009 的迁移计划在候选草稿与确定性物化之前直接重建真实 Vault，并假设最终
`output_cards` / `extraction_coverage` 已在 Note 写入时成立。Storage v3 改变了顺序：

```text
审阅式 Note
  -> candidate_drafts + candidate_coverage
  -> 用户编辑
  -> 确定性 materialize
  -> 最终 output_cards + extraction_coverage
```

若先执行旧 T-009，随后还要把同一批产物再迁成 candidate/output 映射，违反“迁移只做一次”。
旧 T-010 又硬依赖旧 T-009，因此同样不能启动。

## 3. 接续规则

旧任务保持 `created`，不伪造已完成或已取消状态。本 Epic 新建两个对应任务：

- `T-evergreen.block_boundary_materialization-158614-008`（Storage v3 迁移工具与副本演练）：
  承接旧 T-009 的工具开发、保真、覆盖和关系对账，但在副本上完成，不碰真实 Vault。
- `T-evergreen.block_boundary_materialization-158614-009`（最终聚合回归、真实 Vault 一次迁移与收口）：
  承接旧 T-009 的真实执行以及旧 T-010 的七档、脱敏、版本、交付和 Epic 收口。

新任务必须把本文件列入 `references:`。最终收口时再根据真实结果处理旧任务状态：

- 若新任务完整覆盖其 Acceptance，则在旧任务 Activity Log 记录替代任务与证据，再按
  Teamwork 合法流程关闭；
- 若仍有未覆盖项，则旧任务保持 open，并明确剩余项；不得为了 dashboard 变绿强制关闭。

## 4. 真实 Vault 闸门

以下条件全部满足前，真实 Vault 零写入：

1. H3 B1-B4 已集成；
2. L2 与 sidecar 已集成；
3. 迁移工具支持 dry-run，且 fixture 与完整副本演练通过；
4. 最终产品 SHA 已固定；
5. `LC_ALL=C` 下聚合回归全部通过；
6. 迁移前快照、hash manifest、回滚路径和目标计数均已记录。

真实迁移只运行一次。失败时保留现场和报告，不以第二次“修复式迁移”掩盖首轮缺陷。
若迁移输入包含与权威产物同 ID 的 ignored golden Markdown，首次 apply 和幂等复核完成后，
必须先将这些输入完整备份到 Vault 外并移出 Vault，再恢复 `eg materialize` 等依赖全库
ID 解析的写命令；不得修改 `ScanIDs` 的“文件可移动”语义来猜测同 ID 文件的权威性。

## 5. `mark-reviewed`

Storage v3 开发、fixture 演练和副本演练期间都不解除冻结。只有真实 Vault 迁移完成，
并且 Note 保真、候选映射、最终 coverage、Knowledge/Opinion、关系、索引和 reconcile
全部通过后，才关闭冻结 issue 并恢复流程。
