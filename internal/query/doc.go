// [S1] internal/query：检索、单卡视图、关系正反查（S1 为全库扫描版）。
//
// 允许依赖：model（S1 直连 mdfile）。
// 首次落地：S1（施工索引 §13）。
// 依赖禁令：rules / query/rank / query/review 不得导入 query/filter。
// S1 业务实现：context.go（eg context 的白名单上下文组装 + 候选相似卡 + 逐文件 content_hash）。
// M2 落地范围（T-…-020）：scan.go（VaultScan 通用全库扫描 + Filter + SortEntries 四级全序）
// 与 diagnostic.go（Q1–Q3 诚实诊断，不可解析文件不再静默跳过）。
// 阶段标注：**扫描版、无索引**——不建索引目录、不引数据库与检索引擎（均属 S4），
// 单次命令一次扫描，不设任何性能门槛。
package query
