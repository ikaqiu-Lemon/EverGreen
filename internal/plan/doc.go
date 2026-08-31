// [S1] internal/plan：ChangePlan schema、op → store 展开、校验。
//
// 允许依赖：model、rules、query、store。
// 首次落地：S1（骨架 + error 级子集，施工索引 §13）。
// 数据合同：`docs/specs/2026-09-08-changeplan-contract.md`（T-…-011）。
//
// 文件分工（T-evergreen.s1_main_flow-158614-012）：
//   - diagnostic.go  E1–E6 / W1–W8 / I1 与两条未编号 warning 的封闭编号集合 + 诊断载荷
//   - schema.go      只读反序列化（JSON / YAML 同一条路径）、七个 op 字段表、未知字段收纳
//   - validate.go    顶层八键、收敛条目、五个非关系 op 的校验与展开
//   - validate_rel.go 两类关系的校验（E2 / E3 / W2 / W8 与封闭枚举硬拦）
//   - expand.go      写入目标清单、跳过清单、按字段路径的黑名单判定（严禁关键词扫描）
//
// 写路径硬约束：本包只 Unmarshal，永不序列化回写；不写盘、不 commit。
package plan
