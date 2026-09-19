package cli

// [S3] internal/cli/check.go：`eg check` 命令本体的**唯一**落点
// （对账合同 §13 全文 + 可见性合同 §3.4 不作用面；M4 · T-evergreen.s1_main_flow-158614-059）。
//
// # 唯一职责（一句话）
//
// 把 `eg reconcile --dry-run` 收窄成一个**只读结构体检**入口：只判 R3 / R4 两组结构检查
// （恰八个 check；A-62 起 R3 含 opinion_unsupported_validated），恒 0 次提交、零写入，用退出码 `2` 表达「库里存在结构性 error」。
//
// # 与 `eg reconcile` 的关系（真子集，不是并行实现）
//
// 检查逻辑一行不新增（全在 `internal/reconcile`）；finding 的报告投影、error 计数、
// 诊断投影、人类可读渲染**一律复用** 058 的既有函数（`reconcileReportFindings` /
// `reconcileErrorCount` / `reconcileFindingDiags` / `ReconcileFindingLines`）。
// 本文件只有两件自己的事：
//
//	① **取数面收窄**：六格采样里只采 `Scan` + `Sources` 两格（另四格保持零值 / nil）；
//	② **产物白名单**：`Run` 的 finding 按 R3 / R4 派生集合过滤，集合外一律丢弃。
//
// 两层是**双保险**而不是重复：① 让 R1 / R2 / R5 条件② / R6 没有输入可判；
// 而 R7 只靠 `Scan` + `Sources` 也能起判，只有 ② 能拦住它。
//
// # 边界（逐条不得越线）
//
//   - **零写入、恒 0 次提交**：本文件不引计划层、不碰 store 的写方法、不落盘最近一次报告
//     （只读命令不改写 `eg report --last` 的事实），也因此没有退出码 3 / 4 的语义。
//   - **不改 `eg reconcile`**：058 的参数面、退出码、编排顺序、报告三键一字未动。
//   - **不作为任何写命令的前置**（合同 §0.1 第 3 条）：`runCheck` 只被 `root.go` 的
//     `wireImplemented` 挂到 `check` 这一条命令上，别的命令执行路径一字未改。
//     「强校验」（校验不通过就拦住写）属 S5 / M6，本 task 不做。
//   - **不接受范围收窄参数、不接受可见性参数**：`--include-deprecated` 属不作用面（§3.4），
//     不声明即由参数解析当场判非法 → 退 1、零写入；同理不吃对象参数与位置参数。
//   - **不引入修复开关**：没有 `--fix` / `--repair`，检查与修复在本命令里彻底分离。
//   - **不引锁 / 索引 / 性能门槛**：走全量 Markdown 扫描（S4 / M5 才谈索引与门槛）。
//
// # 五个被排除的 check 为什么在本文件里一次都不出现
//
// 排除集合由**真源取补集**得到（`reconcile.Specs()` 里 `R` 不属 {R3, R4} 的那些行），
// 而不是抄一份名单：抄名单会在检查表变动时静默失真，且 Acceptance 明确要求本文件对那五个
// check 值的 grep 恒 `0`。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// CheckScopeCount 是 `eg check` 的检查面基数：**恰 8**（R3 五个 + R4 三个；A-62 起 R3 含 W29）。
//
// 它是封闭计数而不是「派生出多少就多少」：派生集合的长度必须恰等于本常量，
// 否则说明检查表的 R 归属被改动过，用例当场判红（check_test.go 逐字复算）。
const CheckScopeCount = 8

// CheckExcludedCount 是被排除的 check 数：**恰 5**（R1 / R2 / R5 / R6 / R7 各一个）。
// 写成减法而不是字面量 5：`8 + 5 = 13` 这条封闭等式因此在编译期与真源绑定。
const CheckExcludedCount = reconcile.CheckCount - CheckScopeCount

// checkScopeR 是 `eg check` 的检查面归属：恰两组 R（真源里的 R 编号常量，不写字符串字面量）。
var checkScopeR = [2]string{reconcile.R3, reconcile.R4}

// CheckScopeNotice 陈述本命令的范围口径（只读、只判结构、全库不收窄），措辞固定便于逐字断言。
const CheckScopeNotice = "eg check 只判结构：R3 关系异常 + R4 结构完整性（恰 8 个 check）；" +
	"只读、零写入、恒 0 次提交，覆盖整个 vault（不按对象、不按领域收窄）"

// CheckExcludedNotice 是「本次没判什么」的诚实交代（**未判 ≠ 没问题**）。
//
// 为什么必须有这句：`eg check` 退 0 只代表「结构面没有 error」，不代表库是干净的
// —— Git 未提交改动、过目信号缺失、跨领域移动、综述失准、材料支撑不足这五类事实
// 本命令**根本没判**，它们属 `eg reconcile` 的面。
const CheckExcludedNotice = "本次未判的 5 个 check（属 R1 / R2 / R5 / R6 / R7）请跑 eg reconcile：" +
	"未判 ≠ 不存在，eg check 退 0 只代表结构面没有 error 级 finding"

// CheckReportFieldNotice 交代报告 `reconcile` 三键为什么仍是占位（裁决 A-41，待 owner 复核）。
//
// `ran` 的语义是「本次跑了（全库）对账」。`eg check` 是刻意收窄的只读子集，
// 把 `ran` 置 true 会让 `eg report --last` 的读者以为 R1–R7 全判过了 —— 那是失真。
// 因此本命令**不动**报告三键（保持 T-…-057 的占位），子集结果放在 `data.check` 里；
// 也**不落盘**最近一次报告：只读命令不改写 `eg report --last` 的事实。
const CheckReportFieldNotice = "报告 reconcile 三键保持占位（ran=false）：eg check 是 R3 / R4 只读子集，" +
	"不是全库对账；本次结果在 data.check 里，且本命令不改写 eg report --last"

// CheckStrictNotice 交代 `eg check --strict` 为什么对本命令 finding 是**恒等变换**。
//
// 强校验升级面恰 `W1`/`W2`/`W3`/`W4`/`W6`（真源在 internal/reconcile/strict.go），五个码都属
// plan 层写前校验诊断；而 `eg check` 只判 R3/R4 结构，finding 用的是 `E11`–`E14` / `W13`–`W20`
// 段，**与升级面不相交**。因此 `--strict` 在本命令上是恒等变换：不改任何 finding 的 severity、
// 不改退出码、不扩 check 枚举——这是「升级面恰五条」的必然，也是默认路径零漂移的一体两面。
// 之所以仍接这个 flag：写命令与只读体检对 `--strict` 的**参数面**要一致（A-56 默认宽松 +
// `--strict` 显式开启），命令间不能一个认得一个不认得。真正让 W1~W6 升 error 并退 5 的是
// 写命令的写前强校验（S3 precheck），不是本命令。
const CheckStrictNotice = "eg check --strict 对本命令 finding 是恒等变换：强校验升级面（W1/W2/W3/W4/W6）" +
	"属 plan 层写前校验诊断，与 eg check 的 R3/R4 结构码（E11–E14 / W13–W20）不相交；" +
	"退出码与 check 枚举一字不变（真正把 W1~W6 升 error 退 5 的是写命令的写前强校验）"

// checkCommand 注册 `eg check`（合同 §13：写入=否、commit 恒 0 次、三档退出码）。
//
// 参数面**恰 0 个写入相关 flag**：`--json` / `--vault` 由 `dispatch` 统一声明。
// `--strict`（M6 · T-…-074）是唯一命令私有 flag，且对本命令 finding 恒等（见 CheckStrictNotice）；
// `--include-deprecated` 不声明（§3.4 不作用面）→ 传入即退 1、零写入。
func checkCommand() *Command {
	return &Command{
		Name:    "check",
		Display: "check",
		Summary: "只读结构体检：只判 R3 关系异常 + R4 结构完整性（恰 8 个 check，零写入零 commit）",
		Owner:   "T-evergreen.s1_main_flow-158614-059",
		Usage: `eg check [--strict] [--json]

参数：
  --strict   否；对本命令 finding 恒等（升级面 W1/W2/W3/W4/W6 属写前校验诊断，与 R3/R4 结构码不相交）；
             接它只为与写命令的参数面一致（--json / --vault 是全局参数）

检查面：**恰** R3 + R4 八个 check —— 重复 ID / 悬空引用 / 孤儿 /
关系目标缺失 / 关系前缀非法 / 反向关系不对称 / 关系重复 / validated 观点缺支撑。
悬空引用（dangling_ref / E12）覆盖 frontmatter **全部**引用承载字段（` + checkDanglingHelpLine() + `）；
指向原文的两类只在 sources/ 分区已采样时判定（不把「没采样」说成「不存在」）。
关系条目的 target 缺失**不进** E12，走 R3 的关系目标缺失（E13）/ 关系前缀非法（E14）。
事务态披露（默认就给，--strict 不影响）：库被事务扫描阻断时（损坏事务，或未闭合事务多于 1 个），
本命令逐条列出全部问题事务的 txn_id（在诊断的 target 位）、每个损坏事务的不可解析原因，
并给出人工出路（` + checkTxnRemedyHelpLine + `）。
事务态一律是 warning：只读、退出码一字不变（写命令遇同一状态才退 5 + E15）。
本命令**只读**：零写入、恒 0 次 commit、不改写 eg report --last，也不做任何修复
（没有 --fix / --repair；修复走 eg reconcile）。
未判面：R1 / R2 / R5 / R6 / R7 的 5 个 check 本命令**不判**（未判 ≠ 不存在），请跑 eg reconcile。
本命令**不作为任何写命令的前置**（强校验属 S5），也不接受任何范围收窄或可见性参数
（未声明的参数由参数解析当场判非法 → 退 1、零写入）。
退出码：0 成功（无 error 级 finding，含只有 warning） | 1 参数非法（零写入） |
       2 存在 error 级 finding（零写入零提交）
`,
		Flags: func(fs *flagSet) {
			// --strict：只读体检与写命令共用同一 flag 名与默认值（唯一注册点见 precheck_wire.go）。
			registerStrictFlag(fs)
		},
		// 只读结构体检不吃对象参数：多余位置参数一律退 1（*UsageError），零写入。
		Validate: noPositionalArgs,
	}
}

// CheckScopeChecks 返回 `eg check` 会产出的 check 集合：由真源表按 R 归属**派生**
// （`reconcile.Specs()` 的行序保持不变），恒 CheckScopeCount 个。
func CheckScopeChecks() []string {
	out := make([]string, 0, CheckScopeCount)
	for _, spec := range reconcile.Specs() {
		if checkInScope(spec.R) {
			out = append(out, spec.Check)
		}
	}
	return out
}

// CheckExcludedChecks 返回 `eg check` **永不产出**的 check 集合：真源表的**补集**
// （同样不写任何 check 字面量），恒 CheckExcludedCount 个。
func CheckExcludedChecks() []string {
	out := make([]string, 0, CheckExcludedCount)
	for _, spec := range reconcile.Specs() {
		if !checkInScope(spec.R) {
			out = append(out, spec.Check)
		}
	}
	return out
}

// checkInScope 判定一个 R 编号是否属本命令的检查面（恰两组）。
func checkInScope(r string) bool {
	for _, in := range checkScopeR {
		if r == in {
			return true
		}
	}
	return false
}

// checkScopeSet 把派生集合物化成查表用的集合（白名单过滤的唯一判据来源）。
func checkScopeSet() map[string]bool {
	set := make(map[string]bool, CheckScopeCount)
	for _, c := range CheckScopeChecks() {
		set[c] = true
	}
	return set
}

// checkDanglingHelpLine 渲染帮助文本里 E12 的覆盖面：**类别数与名单都从真源取**。
//
// 与本文件「排除集合由真源取补集、不抄名单」是同一条纪律（见文件头注释）：
// 帮助文本里一旦出现手抄的类别数或字段名单，覆盖面变动时它就会静默失真——
// 用户读到的帮助与运行时 detail 说的不是一件事，而这种假话没有任何机器判据拦得住。
// 因此这里只做渲染，事实全部来自 `reconcile.DanglingRefKinds()` / `DanglingRefKindCount`。
func checkDanglingHelpLine() string {
	kinds := reconcile.DanglingRefKinds()
	return fmt.Sprintf("恰 %d 类：%s", reconcile.DanglingRefKindCount, strings.Join(kinds, " / "))
}

// checkExitCode 是本命令退出码的**唯一**判定点：只有两个出口。
//
//	2（存在 error 级 finding，零写入零提交） → 0（无 error 级 finding，含只有 warning）
//
// `1`（参数非法）不在本函数内：它发生在更早的参数解析 / Validate 阶段（零写入），
// 由 `dispatch` 直接返回 `*UsageError`。`3` / `4` / `5` / `6` 本命令**不使用**：
// 没有写入面就没有「部分写入」，没有提交面就没有「提交失败」，没有锁与确认前置。
func checkExitCode(errFindings int) int {
	if errFindings > 0 {
		return ExitValidation
	}
	return ExitOK
}

// checkInputSample 是两格采样的回执：只读输入 + 逐格的诚实性诊断。
type checkInputSample struct {
	In       reconcile.Input
	Warnings []report.Diagnostic
}

// sampleCheckInput 采样**恰两格**：`Scan` 与 `Sources`。
//
// 这不是 058 六格采样的复制品，而是**刻意收窄的取数面**（合同 §13「只读子集」的第一层保险）：
//
//   - `Scan`：`query.VaultScan(root, {IncludeNotes: true})` —— R3 的关系事实与 R4 的
//     ID / 引用 / 孤儿判定都从这里来。nil = 未扫描（依赖扫描面的判定整体不判）。
//   - `Sources`：`store.ScanSources()` 投影成两字段事实 —— R4 的两条源侧判定要它。
//     nil = 未采样（那两条判定不判，绝不把「没采样」当成「原文不存在」）。
//   - `Status` / `Edits` / `Renames` / `Recaps`：**一格都不采**（零值 / nil）。
//     这四格分别只服务 R1 / R2 / R5 条件② / R6 —— 本命令不判它们，采了就是白采，
//     还会让读者误以为本命令在看 Git 工作区。
//
// 零副作用：全程只读（全量 Markdown 扫描 + frontmatter 解析），不写盘、不发提交、不起子进程。
func (r *Root) sampleCheckInput(root string) checkInputSample {
	out := checkInputSample{In: reconcile.Input{VaultRoot: root}}
	warn := func(path, msg string) {
		out.Warnings = append(out.Warnings, report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: path, OpIndex: report.NonOp, Message: msg})
	}

	// ① Scan：全量 Markdown 扫描底座（合同 §0.1 第 1 条：不引索引，一律全量扫描）。
	scan, serr := query.VaultScan(root, query.ScanOptions{IncludeNotes: true})
	if serr != nil {
		warn("domains/", "全库扫描未采样（nil）："+oneLineReason(serr.Error())+
			"；R3 与 R4 本次整体不判 —— 未采样 ≠ 库里没有对象")
	} else {
		out.In.Scan = scan
	}

	// ② Sources：`sources/` 分区不在扫描底座的对象面内，由本层投影成两字段事实。
	if infos, err := store.New(root).ScanSources(); err != nil {
		warn(store.DirSources+"/", "原文分区未采样（nil）："+oneLineReason(err.Error())+
			"；R4 的两条源侧判定本次不判 —— 未采样 ≠ 库里没有原文")
	} else {
		facts := make([]reconcile.SourceFact, 0, len(infos))
		for _, s := range infos {
			facts = append(facts, reconcile.SourceFact{ID: string(s.ID), Path: s.Rel})
		}
		out.In.Sources = facts
	}
	return out
}

// checkScopeFindings 是产物白名单（第二层保险）：只留检查面内的 finding，集合外一律丢弃。
//
// 条数只会减不会增，顺序保持检查包的排序（本层不重排、不去重、不合并、不改级别）。
func checkScopeFindings(fs []reconcile.Finding) []reconcile.Finding {
	set := checkScopeSet()
	out := make([]reconcile.Finding, 0, len(fs))
	for _, f := range fs {
		if set[f.Check] {
			out = append(out, f)
		}
	}
	return out
}

// checkFindingChecks 取一批 finding 里出现过的 check 值（去重升序），供摘要与用例复算。
func checkFindingChecks(fs []reconcile.Finding) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		if !seen[f.Check] {
			seen[f.Check] = true
			out = append(out, f.Check)
		}
	}
	sort.Strings(out)
	return out
}

// wireCheck 是 `eg check` 处理函数在本仓的**唯一**挂载点。
//
// 为什么挂载点写在本文件而不是 root.go：合同 §13 与 M-004 判据 12 要求
// 「`eg check` 不作为任何写命令的前置」可以被一条 grep 机器判定 ——
// 即 `runCheck` 这个符号在 `internal/cli` 里**只出现在 check.go / check_test.go**。
// 把 Wire 收进本文件，那条判据就由「文件边界」结构性保证，root.go 只调用本函数一次。
func (r *Root) wireCheck() { _ = r.Wire("check", r.runCheck) }

// runCheck 是 `eg check` 的唯一入口。四步固定次序（合同 §13）：
//
//	① 两格只读采样 → ② reconcile.Run → ③ 白名单过滤（集合外丢弃）
//	→ ④ 组装 data.check + 摘要 + 两档退出码
//
// 步骤里**没有**写入、没有修复、没有提交：这三件事在本函数里连调用点都不存在。
func (r *Root) runCheck(inv *Invocation) (*Result, error) {
	root := inv.VaultRoot

	// ① + ② 取数与检查：本文件不新增任何判定，finding 全部由检查包产出。
	sample := r.sampleCheckInput(root)
	ran := reconcile.Run(sample.In)
	// ③ 白名单：集合外的 finding（含只靠 Scan + Sources 也能起判的 R7）在此丢弃。
	findings := checkScopeFindings(ran.Findings)

	rep := report.New()
	for _, d := range sample.Warnings {
		rep.AddWarning(d)
	}
	// 事务态披露（C2 · I-…-025）：只读采样，warning 级，**不进 error 计数、不改退出码**。
	// 位置在结构 finding 之前：库要是已经被事务堵住，那才是读者当下最该看到的第一句话。
	txnDiags, txnLines := txnStateDisclosure(root)
	for _, d := range txnDiags {
		rep.AddWarning(d)
	}
	rep.AddInfo("eg check", report.NonOp, "%s", CheckScopeNotice)
	rep.AddInfo("eg check", report.NonOp, "%s", CheckExcludedNotice)
	rep.AddInfo("eg check", report.NonOp, "%s", CheckReportFieldNotice)
	// `--strict` 只在被显式给出时留一条 info（恒等变换的诚实交代）：升级面与 R3/R4 结构码
	// 不相交，退出码与 finding severity 一字不变。不给该 flag 时本行不出现（默认路径零漂移）。
	if inv.Set(StrictFlag) {
		rep.AddInfo("eg check", report.NonOp, "%s", CheckStrictNotice)
	}
	// 提交这一格**不需要任何调用**：`report.New()` 的初值就是 `git.commit = null`，
	// 而本命令零写入零提交，因此这里连「登记提交」的写法都不出现
	// （Acceptance 对本文件的提交面 grep 恒 0，靠的是结构上没有调用点，不是靠自律）。

	errN := reconcileErrorCount(findings)
	code := checkExitCode(errN)

	res := proposalResult(rep, append(txnLines, checkSummary(findings, errN)...))
	// `data.check`：本命令的合同产物。四个键都可复算 ——
	// `scope` / `excluded` 由真源派生，`findings` 与 `report` 同源同一批事实。
	res.Data["check"] = map[string]interface{}{
		"scope":    CheckScopeChecks(),
		"excluded": CheckExcludedChecks(),
		"findings": reconcileReportFindings(findings),
	}
	res.DataOrder = []string{"check", "report"}

	if code == ExitValidation {
		return res, &ValidationError{
			Msg: fmt.Sprintf("结构体检发现 %d 条 error 级 finding（零写入零 commit；"+
				"本命令只判 R3 / R4 的 %d 个 check，未判的 %d 个请跑 eg reconcile）",
				errN, CheckScopeCount, CheckExcludedCount),
			Diags: reconcileFindingDiags(findings),
		}
	}
	return res, nil
}

// checkSummary 组装人类可读摘要。
//
// finding 行与视图提示**一律复用** reconcile_render.go：那里是这些措辞在整个仓库里的
// 唯一落点，本文件不拼第二份。
func checkSummary(fs []reconcile.Finding, errN int) []string {
	head := fmt.Sprintf("结构体检已执行：finding %d 条（error %d、warning %d）；"+
		"检查面 %d 个 check（%s）；零写入、commit 无（只读命令恒 0 次提交）",
		len(fs), errN, len(fs)-errN, CheckScopeCount, strings.Join(CheckScopeChecks(), "、"))
	out := []string{head, CheckExcludedNotice}
	out = append(out, ReconcileFindingLines(fs)...)
	if hint := ReconcileHintSummary(fs); hint != "" {
		out = append(out, hint)
	}
	return out
}
