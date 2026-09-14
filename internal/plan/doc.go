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
// M3 追加（T-evergreen.s1_main_flow-158614-037）：
//   - ops_m3.go       M3 新增 8 个 op 的注册与字段表（A-15 窄口径的五个状态类 op 在此界定）
//   - diagnostics.go  E7–E10 / W9–W12 的编号注册 + 提案结构化违规 → 编号的映射表
//   - validate_m3.go  8 个 op 的校验链（W7 升 error、W3 真判定、W9–W12、A-24 物理移除）
//   - replace_block.go「理解自检」当前有效块替换的校验与展开（冲突复用 file_changed）
//   - execute_m3.go   两条非追加写形态的执行编排（只调 store 写口，不拼字节）
//
// 依赖例外（M3 起，§13 表格无法表达）：本包**直接 import S2 的 internal/proposal**，
// 只为把提案的结构化违规映射成 E7 / E8 / E9（编号只在本包发，proposal 包不出现编号字面量）。
// 该例外由 cmd/eg/arch_test.go 的 stage2Consumers 等号与 stage2Forbidden 禁令双侧钉死。
//
// 写路径硬约束：本包只 Unmarshal，永不序列化回写；不写盘、不 commit。
package plan
