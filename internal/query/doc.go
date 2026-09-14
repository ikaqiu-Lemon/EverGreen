// [S1] internal/query：检索、单卡视图、关系正反查（S1 为全库扫描版）。
//
// 允许依赖：model（S1 直连 mdfile）。
// 首次落地：S1（施工索引 §13）。
// 依赖禁令：rules / query/rank / query/review 不得导入 query/filter。
// S1 业务实现：context.go（eg context 的白名单上下文组装 + 候选相似卡 + 逐文件 content_hash）。
// M2 落地范围（T-…-020）：scan.go（VaultScan 通用全库扫描 + Filter + SortEntries 四级全序）
// 与 diagnostic.go（Q1–Q3 诚实诊断，不可解析文件不再静默跳过）。
// M3 落地范围（T-…-033）：scan.go 追加提案控制面的**目录过滤**——`proposals/` 不进
// 知识扫描面（search / card show / rel / 综述取材一律看不到提案，也不产生 Q1 噪声），
// 另出 ProposalSummaries「标题 + targets」摘要供 eg context 去重，**不含正文**。
// M5 落地范围（T-…-067）：backend.go（后端选择**单点** SelectBackend）、index_backed.go
// （索引后端：只做候选集召回 + 按需回权威解析）、degrade.go（missing / corrupt / stale
// 三形态确定性降级为全量扫描）、diagnostic.go 追加 Q5（S4 起 Q 码恰五条）。
// 阶段标注：**Markdown 恒为唯一权威来源**——本包读派生索引（只读消费 internal/index），
// 但一行也不建、不写、不修索引（那属 internal/cli 的 `eg index`），索引缺失 / 损坏 / 陈旧
// 一律降级为全量扫描并如实留痕（W22|W23|W24 + Q5），**绝不因索引问题阻断读或改退出码**；
// 排序 / 截断 / 分页 / 反查性能与任何时间门槛仍不在本阶段（属 T-…-068）。
package query
