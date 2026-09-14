package cli

import "errors"

// 命令层 error 诊断编号（`E17`–`E25`）与「错误族 → 编号」的**唯一**映射点。
//
// # 为什么有这个文件（I-…-015）
//
// 冻结合同 `2026-09-01-eg-cli-contract.md` §5 只授权**一种**空码：「未编号 **warning**
// 填 `""` 并在 `message` 说明」。但实现里有 37 处 **error 级**条目把 `code` 留空，另有 3 处
// 把 `skipped[].cause` 的枚举值（`content_hash_mismatch`）塞进 `code` 位。后果有三层：
//
//	① Agent 集成面失能：`skill/SKILL.md` 逐字要求退 `2` / 退 `5` 的处置「读 `data.errors[]`
//	   的 `code` + `op_index` + `path`，改 plan 后重投」。`code` 为空时接入方只能对中文
//	   `message` 做子串匹配 —— 而 `message` 从不在任何合同里被冻结，属随时可改的自由文本。
//	② 码域被污染：`content_hash_mismatch` 与 `E*`/`W*`/`I*` 同处一个键，按码前缀路由的调用方
//	   （「`E` 开头 → 修 plan 重投；`W` 开头 → 继续」）会命中 `default` 分支或直接崩。
//	③ 可观测性倒退：空码条目无法聚合统计，同一告警在监控里退化成一条不可分类的 `""`。
//
// # 编号纪律（A-29「只增不改」）
//
// 既有编号一格不动：`E1`–`E6`（plan 校验，internal/plan）、`E7`–`E10`（M3 提案态）、
// `E11`–`E14`（R3/R4 结构体检，internal/reconcile）、`E15`/`E16`（M6 写前强校验 / 锁不可用，
// internal/txn）、`W1`–`W20`、`W22`–`W28`、`I1`、`Q1`–`Q5`。本文件**只追加** `E17`–`E25`，
// 且落点封闭在命令层：plan / txn / reconcile / index 各自的码仍由各自的包发，本文件不抄它们。
//
// # 分档口径：`code` 说「要改什么」，`exit_code` 说「有多严重」
//
// 两者**故意解耦**，不是一一对应：
//   - 同一个 `E17`（参数 / 用法非法）可能伴随退 `1`（多数）或退 `2`（合同 §1.3 把
//     `eg capture` 的 `--url`/`--title` 同时缺失指派为校验失败），因为「该补哪个参数」这件
//     处置事实与「本次有多严重」是两个正交问题；
//   - 反过来，同一个退 `2` 也可能是 `E18`（目标不存在）/ `E19`（缺授权佐证）/ `E20`（产物
//     不可解析）—— 处置动作完全不同，正是 `code` 存在的理由。
//
// 因此**不得**把 `code` 实现成 `exit_code` 的同义词（那样它就没有信息量了），也不得反过来
// 用 `code` 去推导退出码：退出码的唯一翻译点仍是 `exit.go` 的 `ExitCodeFor`。
const (
	// E17 用法 / 参数非法：缺必填参数、参数取值非法、互斥参数同时给出、分页参数越界、
	// 未知命令或子命令、业务实现尚未挂载（占位命令）。
	// 处置：**改命令行**后重投（不必改库内数据）。
	E17 = "E17"
	// E18 前置事实不成立：`--target` / 提案 ID 在库里解析不到文件、目标不在提案 targets 内、
	// 无历史报告可读、未找到 vault。
	// 处置：**核对对象是否存在**（`eg search` / `eg card show` / `eg proposal list` 确认）后重投。
	E18 = "E18"
	// E19 授权或确认门禁未满足：缺 `--user-request` 显式佐证、缺二次确认标记、
	// 缺 `approved` 提案背书、提案 status ≠ approved。
	//
	// 注：此处刻意不写二次确认那个参数的英文名 —— eg 有一条静态门禁把「二次确认流程」的落点
	// 逐字锁在 delete.go / exitcode.go / proposal*.go 五个文件里（lifecycle_state.sh ⑤）。
	// 本文件是纯码表、零流程，不该因为一句注释就挤进那份白名单把门禁放宽到六个文件。
	// 处置：**补授权链**（先 `eg proposal create` / `eg proposal approve`，再带齐佐证标记重投）；
	// 提案文件里写 `initiator: user` 不能自证（授权合同 N-1）。
	E19 = "E19"
	// E20 权威产物读取或解析失败：文件读不动、frontmatter YAML 解析失败、
	// 库内时刻不可比较。**零写入**。
	// 处置：**修数据**（`eg check` 定位后手工修正该文件）再重投。
	E20 = "E20"
	// E21 执行未能完成：预演期写口失败、原子事务放弃、对账修复未能执行、提案改判失败、
	// 崩溃恢复屏障阻断，以及 `eg bench` 的采样中途失败。共同事实：
	// **权威 Markdown 零改动、无 commit**（bench 连 `.index/` 都不写）。
	// 处置：读 `path` + `message` 排查磁盘 / 权限 / 并发写者，或先 `eg check --strict` 定位后重投。
	E21 = "E21"
	// E22 Git 提交失败（B4）：权威 Markdown **已按目标态落盘并保持现状**，不回滚、不二次写。
	// 处置：**修 Git 环境后手工提交**（`git status` / `git commit`），**不要**重跑写命令，
	// 也不要做破坏性还原 —— 字节已经是目标态了。
	E22 = "E22"
	// E23 部分成功汇总：本次有 N 处被跳过 / M 处写入失败，已完成的写入保留并已提交（退 3）。
	// 逐条明细见同一 `data.errors[]` 里的其余条目与 `report.skipped[]`。
	// 处置：按明细逐条处置后**只重投被跳过的部分**。
	E23 = "E23"
	// E24 本次跳过未写入（B2 / B3）：`kind` / `cause` 逐字见 `report.skipped[]`，
	// 也在本条 `message` 里可读。**该文件字节零变化。**
	// 处置：`content_hash_mismatch` → 重新 `eg context` 取 base 后重投；
	// `user_block_not_preserved` → 用户分区无法安全逐字保留，需人工处理后重投。
	E24 = "E24"
	// E25 未分类错误：走到了 `ExitCodeFor` 的兜底分支（按 `1` 处置）。
	// 这是**实现 bug 的信号**，不是新的对外语义 —— 给它独立编号正是为了让它可被聚合统计、
	// 从而暴露出来，而不是继续混在空码里查不出。
	// 处置：按 `message` 提 issue；调用方**不应**对 `E25` 写业务分支。
	E25 = "E25"
)

// CommandErrorCodes 是命令层追加的 error 编号全集（恰 `E17`–`E25`，声明顺序即编号顺序）。
// 供门禁与用例断言「编号连续、无空洞、无重复」。
func CommandErrorCodes() []string {
	return []string{E17, E18, E19, E20, E21, E22, E23, E24, E25}
}

// CodeForError 把一个**带类型的错误**映射到命令层 error 编号。
//
// 它只在一处被用到：`mergeErrorIntoResult`（root.go）为「没有自带诊断的顶层错误」补一条
// 条目时。判据取的是**已经决定退出码的那套类型化错误分类**（见 exit.go 的 ExitCodeFor），
// 因此这个映射不是凭空发明的第二套口径，而是同一套分类的另一个投影 —— 也正因此，
// 它给出的码天然与退出码自洽、可被调用方稳定路由。
//
// 兜底给 E25（未分类）而**不是** E17：把实现 bug 伪装成「参数非法」会让调用方去改命令行，
// 而真正该做的是提 issue。宁可暴露成一个专用码，也不要静默归并到看起来正常的族里。
func CodeForError(err error) string {
	if err == nil {
		return ""
	}
	var (
		usage     *UsageError
		notWired  *NotWiredError
		pageParam *PageParamError
		invalid   *ValidationError
		partial   *PartialWriteError
		commit    *CommitFailedError
		needCfm   *NeedConfirmError
		benchFail *BenchError
	)
	switch {
	// 次序与 ExitCodeFor 一致：更具体的类型先说话，避免被宽泛族抢走。
	case errors.As(err, &needCfm):
		return E19
	case errors.As(err, &usage), errors.As(err, &notWired), errors.As(err, &pageParam):
		return E17
	case errors.As(err, &invalid):
		return E18
	case errors.As(err, &partial):
		return E23
	case errors.As(err, &commit):
		return E22
	case errors.As(err, &benchFail):
		// bench 采样失败与 Git 提交失败共用退出码 4，但**处置完全不同**（前者重跑采样、
		// 后者手工提交），因此码不共用：这正是 code 与 exit_code 解耦的意义。
		return E21
	}
	return E25
}

// 退出码 5 的两类成因（写前强校验失败 / 锁不可用）**不在本表内**：它们的诊断由
// internal/txn 在错误里带出（`E15` / `E16` 的字面量按诊断码闭合门禁只许落在 internal/txn，
// 本包一律走运行期搬运）。mergeErrorIntoResult 见到「错误已自带 error 级诊断」时不再补条目，
// 因此这两类错误既不会丢码、也不会被本表兜底成 E25。
