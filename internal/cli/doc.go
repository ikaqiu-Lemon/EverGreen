// [S1] internal/cli：命令定义、参数、退出码、--json 开关。
//
// 允许依赖：以上全部（model、mdfile、store、git、plan、report、query、rules）。
// 首次落地：S1（九命令，施工索引 §13）。
//
// 骨架职责（T-evergreen.s1_main_flow-158614-003）：
//   - root.go     九命令注册树、全局 flag（--json / --vault）、统一守卫、分发
//   - commands.go 各命令参数表（逐项对齐 CLI 合同 §1.1–§1.9）与参数形态校验
//   - exit.go     退出码 0/1/2/3/4 的集中映射 + --json / 人类可读双渲染
//   - rel.go / placeholder.go  M1 占位命令（search / card show / rel）
//   - vault.go    vault 定位与 evergreen.yml 只读解析（守卫用）
//
// 各命令业务实现由后续 task 通过 Root.Wire 挂载；骨架阶段调用未挂载命令返回
// NotWiredError（退 1、零写入）。**除 cmd/eg/main.go 外任何地方不得调用 os.Exit。**
package cli
