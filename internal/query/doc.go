// [S1] internal/query：检索、单卡视图、关系正反查（S1 为全库扫描版）。
//
// 允许依赖：model（S1 直连 mdfile）。
// 首次落地：S1（施工索引 §13）。
// 依赖禁令：rules / query/rank / query/review 不得导入 query/filter。
// 本阶段为占位空包，业务实现见 T-evergreen.s1_main_flow-158614-010。
package query
