// [S1] internal/plan：ChangePlan schema、op → store 展开、校验。
//
// 允许依赖：model、rules、query、store。
// 首次落地：S1（骨架 + error 级子集，施工索引 §13）。
// 本阶段为占位空包，业务实现见 T-evergreen.s1_main_flow-158614-011 / -012。
package plan
