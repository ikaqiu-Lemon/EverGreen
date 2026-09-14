// [S1] internal/rules：opposing 规范化、收敛三维度判据。
//
// 允许依赖：model、query。
// 首次落地：S1（部分，施工索引 §13）。
// 依赖禁令：不得导入 query/filter。
// 文件分工：
//   - converge.go  收敛三维度判据（任一不同 → 拆两张卡；判不出也拆）
//   - opposing.go  opposing 方向规范化与同对去重的唯一实现（W8）
//
// 本包只做判据，不写盘、不产出诊断编号（编号登记在 internal/plan）。
package rules
