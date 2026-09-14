// [S5] internal/txn：M6 强原子事务的**锁 + 事务日志**单点（T-…-071 首次落地）。
//
// 判据来源：M6 原子性与强校验合同 `docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md`
//
//	§2（事务边界与临界区 S0 ~ S9；S3 的重新候选发现 + 重读**必须在锁内**）
//	§3（`run.lock` 形态：`flock` 唯一真源、锁 inode 持锁期零替换、10s 超时与退避、陈旧锁安全接管）
//	§4 / §4.1 / §4.2 / §4.3（`.index/txn/` 布局、`intent.json` 七字段、intent 发布屏障、
//	  `Scan` 三分类、路径规范化约束、保留策略 7 天 / 20 个）
//	§12（M6 新增诊断码恰 5 条，本包产 `E16` / `W28`，并为 `E15` 提供可映射的类型化错误）
//	§16.1（命令三类别：A 类走完整事务，**B 类 index 维护命令持同一把锁但不开事务**）
//	§17（退出码 `5` 的落地形态：本包只返回**可被映射的错误值**）
//
// # 允许依赖与依赖禁令
//
// 允许依赖：Go 标准库（含 syscall）。**零本仓依赖**。
// 技术方案 §13 与合同 §16.5 的依赖方向在本包内被逐字落地：本包不得导入
// cli / query / index / report / reconcile 五包，也不得使用子进程执行器；
// `.index` / `run.lock` / `txn` 这三个字面量在本包内自持，**不**向索引包借。
// 因此诊断码在本包内只以中性常量（Diag / Code*）表达，**翻译成报告字段与进程退出码
// 这件事不在本包内发生**（合同 §17.1：常量与 ExitCodeFor 单点映射属 T-…-074，
// 唯一 `os.Exit` 调用点是 `cmd/eg/main.go`）。本包一行 `os.Exit` 都不写，
// 也不定义任何数字退出码常量。
//
// # 本包的范围（T-…-071）
//
//   - lock.go —— `vault/.index/run.lock` 单机文件锁：`Acquire` / `Release` / `WithLock`，
//     `flock` 加在**长驻 fd** 上；持有者正文（pid / acquired_at / argv，可选补写 txn_id）
//     只能由**同一把已加锁 fd** 做 `Ftruncate(0)` + `Pwrite` + `Fsync` 原地写；
//     等待期留痕 `W28`、超时返回携 `E16` 的 sentinel；陈旧锁接管的唯一判据 = `flock` 是否可获得。
//   - id.go —— `txn_id` 分配：`t` + 16 位零填充小写十六进制，三源取最大
//     （持久 `seq` / 现存目录最大序号 / **进程游标**）+ 1，`O_EXCL` 建目录，溢出 fail closed。
//   - journal.go —— `.index/txn/<txn_id>/{intent.json,commit,abort}` + `pre/` + `quarantine/`：
//     `WriteIntent`（含逐字定死的 intent 发布屏障）、`MarkCommit` / `MarkAbort`、
//     `Scan`（**全量只读三分类**）、`Prune`（保留策略）、`ValidateIntentPaths`（路径防逃逸）。
//
// # 三条不可放宽的硬约束
//
//	被加锁 inode 在持锁期零替换 —— 对 run.lock 永不 tmp+rename、永不 unlink / 删除、永不先释放再重建
//	intent 日志写入先于任何权威文件写入 —— WriteIntent 返回成功 = 发布屏障已完成
//	Scan 全程零写盘 —— 清理只允许调用方在全局裁决通过之后另行调用 Prune
//
// # 写入面（除此之外本包一个字节都不写）
//
//	vault/.index/run.lock                （只原地写正文，永不换 inode）
//	vault/.index/txn/**                  （事务目录、pre/、quarantine/、intent.json、commit、abort、seq）
//
// # 明确不做（各有归属，早做即越界）
//
//   - 权威文件的原子提交与崩溃恢复 hook（Pass A / Pass B 两遍恢复、`W26`）→ T-…-072；
//   - index 包与 index 命令的 `.index/` 共存兼容改造 → T-…-072；
//   - 块级安全合并与并发冲突语义（`W27`）→ T-…-073；
//   - `eg check` 强校验、退出码 `5` 的 CLI 出口与 `ExitCodeFor` 映射 → T-…-074；
//   - 对外文档、版本推进、历史脚本现态重钉与全量回归 → T-…-075。
package txn

// 诊断码：M6 合同 §12 的分配表在本包内的中性投影（**不新增码**）。
// 本包只**产出码与结构化事实**，severity 升级、报告渲染与退出码映射都在上层。
const (
	// CodeLockTimeout 锁不可用（等待超时）；由 lock.go 产出，上层映射为退 5 且零写入。
	CodeLockTimeout = "E16"
	// CodeLockWaitRetry 锁等待期的退避重试留痕；由 lock.go 产出，不改退出码。
	CodeLockWaitRetry = "W28"
	// CodePrecheckFailed 写前安全复核失败（语义大类，合同 §18.4）；
	// 本包的损坏事务 / 路径违规 / 扫描全集异常 / seq 溢出四类原因经由 precheck 面产出。
	CodePrecheckFailed = "E15"
)

// 诊断 severity 的中性字面量（与报告层同口径，但不依赖报告层类型）。
const (
	LevelError   = "error"
	LevelWarning = "warning"
)

// Diag 是本包对外的**中性**诊断事实：只有码、级别、路径、对象与人类可读信息，
// 不含 op_index / 报告信封等上层概念（避免反向依赖，见包注释的依赖禁令）。
//
// Path 与 Target 的分工是**硬约定**（CLI 合同 §5 / C2 · I-…-025）：
//
//	Path   = **文件路径**（`.index/txn/<txn_id>` 这种真实相对路径，或空串）；
//	Target = 被诊断的**对象标识**（本包里就是 `txn_id`）。
//
// 为什么必须分两格：`txn_id` 曾被塞进 `Path`，脚本据 `path` 无法分辨「这是一个文件还是一个事务号」，
// 而合同给 `path` 的语义是文件路径。两格分开之后，「列出全部问题事务」这件事才有稳定的机器读法。
type Diag struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path,omitempty"`
	Target  string `json:"target,omitempty"`
	Message string `json:"message"`
}

// coded 是「携带诊断码的错误」的最小契约：T-…-074 的 ExitCodeFor 单点映射
// 通过 errors.As 拿到它就能决定退出码与报告诊断，而本包不感知任何数字退出码。
type coded interface {
	error
	Code() string
	Diagnostics() []Diag
}

// 编译期自证：本包的阻断错误都实现了 coded 契约。
var (
	_ coded = (*LockTimeoutError)(nil)
	_ coded = (*LockPathError)(nil)
	_ coded = (*ScanBlockedError)(nil)
	_ coded = (*PathViolationError)(nil)
	_ coded = (*IntentExistsError)(nil)
	_ coded = (*SeqOverflowError)(nil)
	_ coded = (*SeqStateError)(nil)
	_ coded = (*RuntimeDirError)(nil)
)
