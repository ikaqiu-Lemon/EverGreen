// [S1] internal/report：最终报告 JSON schema 与人类可读渲染。
//
// 允许依赖：model、query。
// 首次落地：S1（施工索引 §13）。
//
// 业务实现见 report.go（T-evergreen.s1_main_flow-158614-016）：§4.6 的 S1 必填 11 项、
// 非 S1 阶段字段的占位形态、`--json` 与人类可读双渲染。
// 本包**只描述报告**：不写盘、不 commit、不认识 plan / store / cli 的类型，
// 由 internal/cli 把执行事实喂进来，因此报告与写入实现解耦、可单独断言。
package report
