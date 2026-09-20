// [S3] internal/reconcile：R1–R7 一致性检查项与报告聚合（M4 · T-…-049 只落包骨架）。
//
// 允许依赖：model / mdfile / git（只读 API） / query。
// 首次落地：S3（里程碑 M-004；对账合同 docs/specs/2026-11-12-m4-reconcile-contract.md §1）。
//
// # 三条包边界（合同 §1.1 / §1.2，逐条都有机器反证）
//
//  1. **纯只读、纯函数的检查器**：输入 = vault 快照 + Git 工作区状态，
//     输出 = []Finding + []RepairSpec —— RepairSpec 是**修复意向的描述，不是写动作**。
//     本包不产生任何副作用：同一输入恒得同一输出（findings 排序可复算，见 finding.go）。
//  2. **零写盘、零 commit、零子进程**：本包不出现任何文件写入调用、不发任何 Git 提交、
//     不引 os 标准库的 exec 子包（三条 grep 反证由 check_test.go 的包边界自守用例在包内复跑）。
//     写盘由 plan → store 这条唯一写盘链承担；提交由 CLI 层唯一的对账提交口承担
//     （T-…-048 裁决 §2「谁检查 / 谁写盘 / 谁 commit」三段定义）。
//  3. **依赖方向单向**：只允许 reconcile → {model, mdfile, git 只读 API, query}；
//     禁止 reconcile → {store, plan, cli, report, proposal}；也禁止任何包反向 import 本包
//     （反向依赖即成环）。双侧反证见 check_test.go 与 cmd/eg/arch_test.go 的
//     TestStage3ReconcilePackageBoundary。
//
// # 本 task（T-…-049）的范围边界
//
// 本包在 T-…-049 只定死两张封闭表 —— Finding 四键 schema（finding.go）与 check 十三值
// 封闭枚举 + 诊断码双向单射（check.go；十二值起，A-62 追加 W29 后为十三值）—— 外加只读检查器入口骨架（reconcile.go）。
// **R1–R7 七项检查逻辑一项都不实现**（分属 T-…-050 ~ T-…-056）；不注册任何命令
// （eg reconcile 属 T-…-058、eg check 属 T-…-059）；不动报告体（属 T-…-057）。
// 阶段标注：**全量 Markdown 扫描版**，复用 query 的扫描底座，本包不另写扫描器、
// 不建任何缓存目录、不设性能门槛（均属 S4 / M5，本包零命中）。
package reconcile
