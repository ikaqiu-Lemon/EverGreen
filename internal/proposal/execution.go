package proposal

// `execution` 三态的回写与 `execution=failed` 的**逐路径**账本
// （提案合同 §4.1 / §4.3 / §5.3 第 9b–10 步、§8.4；T-evergreen.s1_main_flow-158614-036）。
//
// # 唯一计数来源（I-…-002 升级为本 task DoR 的硬约束）
//
// 「已写 / 未写」在本仓**只有一个**产生点：PathLedger。
//
//	all      本次提案的影响文件全集 —— 构造时一次给定，之后只读；
//	written  **唯一**可变状态 —— 只有 MarkWritten 能改；
//	未写     **派生量** Unwritten() = all − written —— 不存第二份清单。
//
// 因此「written ∪ unwritten == 影响文件全集 ∧ written ∩ unwritten == ∅」在**结构上**恒成立
// （Verify 只是把这条等式再断言一遍），而不是靠两处清单事后对齐。提案回写（RecordExecution）、
// 报告投影、CLI 输出三处一律从**同一个账本对象**取数，谁都不再数一遍 ——
// I-…-002 那种「dry-run 与正式执行各数一遍」的双源形态在这里从类型上就写不出来。
//
// PathSkip 只承载「未写的原因」（kind / cause 一律取 store 的封闭命名，恰两值，不新增第三种），
// **不参与任何计数**：Unwritten() 从不查它。
//
// # 写口径（A-23 + B1–B4 + F3）
//
//   - 回写只改 `execution` 块的 5 个子键：3 个标量走 setFMScalar 的字节级整行替换，
//     2 个路径数组走 setFMSeq 的字节级块替换；**全程不经任何 YAML 序列化器**；
//     （A-32「整键删除」：git_commit 已退役，写侧不再产出该键）
//   - `status` / `decision` / `impact` / 未知键 / 注释 / 空行 / 正文七分区**一个字节不动**
//     （正交：执行结果不回写用户决定，写后逐字复核 status 未变）；
//   - 写前 Parse→Render 字节自检 + ValidateLayout + 一致性复核 + **可达矩阵**复核，
//     任一不过一律**拒写**（本工具因此永远写不出一个不可达组合）；
//   - 落盘走 store 的提案 guarded 写口（content_hash 比对 → tmp + fsync + rename）；
//   - **B4**：失败不做任何破坏性回滚 —— 已写入的内容留在磁盘，未写入的路径逐条在册，
//     部分成功就如实记成部分成功，既不假装全失败也不假装全成功。
//
// # 手工改写不被采信（#39 / N-4）
//
// `execution.*` 是 CLI 独占。RecordExecution 的产出**只来自入参**（状态 + 时刻 + 账本），
// 从不读取文件里的 execution 旧值做任何推导或合并：手工填的 succeeded、手工塞进
// written_paths 的路径，都会被本次执行的真实结果整块覆盖。

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// ViolationExecFields 是 `execution` 必写字段不齐 / 与账本自相矛盾
// （§4.1 三态的「必写字段」列、§4.3 两个路径键的必填）。
//
// 它是**回写口自身**的前置条件违规（CLI 独占路径的实现缺陷），不是读盘时的形态违规，
// 因此不进 DiagClassOf 的映射表：调用方拿到它必须停手，而不是把它翻译成某个诊断编号。
const ViolationExecFields ViolationKind = "execution_fields"

// —— ① PathLedger：已写 / 未写的唯一账本 ——

// PathSkip 是一条「未写的原因」。Kind / Cause 取 store 的封闭命名（恰两值），
// 与报告 `skipped[]` 同源同名，本文件不自造第三种 kind、也不使用同义词。
type PathSkip struct {
	Rel    string
	Kind   store.SkipReason
	Cause  string
	Detail string
}

// Counts 是三个计数（全部由账本一处算出）。
type Counts struct {
	// Total 是影响文件全集的规模，Written / Unwritten 恒满足 Written + Unwritten == Total。
	Total     int
	Written   int
	Unwritten int
}

// PathLedger 是本次执行「已写 / 未写」路径的**唯一**账本（见文件头「唯一计数来源」）。
type PathLedger struct {
	all     []string
	inAll   map[string]bool
	written map[string]bool
	skips   map[string]PathSkip
}

// NewPathLedger 用**影响文件全集**开一本账（去重 + 升序，路径必须是 vault 内相对路径）。
//
// all 一次给定、之后只读：全集不可能在执行中途变大或变小，能变的只有「哪些已写」。
func NewPathLedger(all []string) (*PathLedger, error) {
	l := &PathLedger{
		inAll:   map[string]bool{},
		written: map[string]bool{},
		skips:   map[string]PathSkip{},
	}
	for _, rel := range all {
		if err := checkVaultRel(rel); err != nil {
			return nil, err
		}
		if l.inAll[rel] {
			continue
		}
		l.inAll[rel] = true
		l.all = append(l.all, rel)
	}
	sort.Strings(l.all)
	return l, nil
}

// checkVaultRel 判定一条路径是不是干净的 vault 内相对路径（不接受绝对路径与 `..` 上跳）。
func checkVaultRel(rel string) error {
	if strings.TrimSpace(rel) == "" {
		return violate(ViolationRelPath, KeyExecBlock, "路径不得为空")
	}
	if path.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return violate(ViolationRelPath, KeyExecBlock, "路径 %q 必须是 vault 内相对路径", rel)
	}
	if path.Clean(rel) != rel {
		return violate(ViolationRelPath, KeyExecBlock,
			"路径 %q 不是归一形态（应为 %q）", rel, path.Clean(rel))
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return violate(ViolationRelPath, KeyExecBlock, "路径 %q 不得上跳出 vault", rel)
		}
	}
	return nil
}

// All 返回影响文件全集（升序副本）。
func (l *PathLedger) All() []string { return append([]string{}, l.all...) }

// Has 报告某条路径是否属于本次影响文件全集。
func (l *PathLedger) Has(rel string) bool { return l.inAll[rel] }

// ErrPathNotInScope 表示登记的路径不在影响文件全集内（账本不接受全集外的事实）。
var ErrPathNotInScope = fmt.Errorf("路径不属于本次影响文件全集")

// ErrPathConflict 表示同一条路径既被登记为已写又被登记为跳过（两者互斥）。
var ErrPathConflict = fmt.Errorf("同一路径不得既已写又未写")

// MarkWritten 登记一条**已成功写盘**的路径（账本唯一的可变入口）。
//
// 重复登记同一路径是幂等的（写口可能被同一次执行的多个 op 命中同一文件）。
func (l *PathLedger) MarkWritten(rel string) error {
	if !l.inAll[rel] {
		return fmt.Errorf("%w：%s", ErrPathNotInScope, rel)
	}
	if _, bad := l.skips[rel]; bad {
		return fmt.Errorf("%w：%s 已被登记为跳过", ErrPathConflict, rel)
	}
	l.written[rel] = true
	return nil
}

// MarkSkipped 登记一条**未写路径的原因**（不参与计数，只解释「为什么没写」）。
func (l *PathLedger) MarkSkipped(rel string, kind store.SkipReason, detail string) error {
	if !l.inAll[rel] {
		return fmt.Errorf("%w：%s", ErrPathNotInScope, rel)
	}
	if l.written[rel] {
		return fmt.Errorf("%w：%s 已被登记为已写", ErrPathConflict, rel)
	}
	l.skips[rel] = PathSkip{Rel: rel, Kind: kind, Cause: store.CauseFor(kind), Detail: detail}
	return nil
}

// Written 返回已写路径（升序，全集子序）。
func (l *PathLedger) Written() []string {
	out := make([]string, 0, len(l.written))
	for _, rel := range l.all {
		if l.written[rel] {
			out = append(out, rel)
		}
	}
	return out
}

// Unwritten 返回未写路径 —— **派生量**：全集减去已写，不存第二份清单。
func (l *PathLedger) Unwritten() []string {
	out := make([]string, 0, len(l.all)-len(l.written))
	for _, rel := range l.all {
		if !l.written[rel] {
			out = append(out, rel)
		}
	}
	return out
}

// Skips 返回未写原因（升序；只覆盖登记过原因的那些路径）。
func (l *PathLedger) Skips() []PathSkip {
	out := make([]PathSkip, 0, len(l.skips))
	for _, rel := range l.all {
		if s, ok := l.skips[rel]; ok {
			out = append(out, s)
		}
	}
	return out
}

// Counts 返回三个计数（同一处算出：Written + Unwritten == Total 恒成立）。
func (l *PathLedger) Counts() Counts {
	w := len(l.Written())
	return Counts{Total: len(l.all), Written: w, Unwritten: len(l.all) - w}
}

// Complete 报告全集是否已全部写入（无未写路径）。
func (l *PathLedger) Complete() bool { return len(l.written) == len(l.all) }

// Verify 断言账本的两条等式（§4.3 的「两侧一致性」M-4 第一条）：
//
//	written ∪ unwritten == 影响文件全集　∧　written ∩ unwritten == ∅
//
// 结构上恒成立，这里仍显式复核一遍：任何未来改动破坏派生关系都会在写盘前被拦住。
// 同时复核「跳过原因 ⊆ 未写路径」（M-4 第二条的账本侧）。
func (l *PathLedger) Verify() error {
	w, u := l.Written(), l.Unwritten()
	union := map[string]bool{}
	for _, rel := range w {
		union[rel] = true
	}
	for _, rel := range u {
		if union[rel] {
			return violate(ViolationExecFields, KeyExecBlock,
				"路径 %s 同时出现在 %s 与 %s 中：交集必须为空", rel, KeyWrittenPaths, KeyUnwrittenPaths)
		}
		union[rel] = true
	}
	if len(union) != len(l.all) {
		return violate(ViolationExecFields, KeyExecBlock,
			"%s ∪ %s 共 %d 条，影响文件全集 %d 条：并集必须恰等于全集",
			KeyWrittenPaths, KeyUnwrittenPaths, len(union), len(l.all))
	}
	for _, rel := range l.all {
		if !union[rel] {
			return violate(ViolationExecFields, KeyExecBlock,
				"影响文件 %s 既不在 %s 也不在 %s 中", rel, KeyWrittenPaths, KeyUnwrittenPaths)
		}
	}
	for _, s := range l.Skips() {
		if l.written[s.Rel] {
			return violate(ViolationExecFields, KeyExecBlock,
				"路径 %s 已写却带着跳过原因：跳过原因必须只落在未写路径上", s.Rel)
		}
	}
	return nil
}

// CoversSkipped 断言「unwritten_paths ⊇ 报告 skipped[] 中属本提案的条目」
// （§4.3 的两侧一致性 M-4 第二条）。
//
// 入参是报告 `skipped[]` 里属本提案影响范围的 locator / target 清单（由调用方按同一账本筛出）：
// 全集外的条目不属本提案，直接忽略；属本提案却被记成「已写」= 实现缺陷，直接报错。
func (l *PathLedger) CoversSkipped(reported []string) error {
	for _, rel := range reported {
		if !l.inAll[rel] {
			continue
		}
		if l.written[rel] {
			return violate(ViolationExecFields, KeyUnwrittenPaths,
				"报告把 %s 记为跳过，账本却记为已写：%s 必须覆盖报告 skipped[] 中属本提案的条目",
				rel, KeyUnwrittenPaths)
		}
	}
	return nil
}

// —— ② execution 回写 ——

// RecordSpec 是一次 `execution` 回写的输入。
//
// 产出**只**由这些入参决定（不读文件里的 execution 旧值、不读系统时钟）：
// 手工改写的 execution 值因此天然不被采信（#39 / N-4）。
type RecordSpec struct {
	// Store 是 guarded 写口（A-23：提案控制面的 CLI 直写例外仍走 guarded store）。
	Store *store.Store
	// ID 是提案 ID；Rel 是它在库的实际路径（留空按默认落位，F2 允许改名 / 移动）。
	ID  ID
	Rel string
	// Status 是本次执行的真实结果（三态之一）。
	Status ExecStatus
	// AttemptedAt 是本次执行的尝试时刻（CLI 传入）。
	AttemptedAt model.Stamp
	// Reason 是失败原因（failed 必写）。
	Reason string
	// GitCommit 已按 A-32「整键删除」退役：回写口不再消费该字段（写侧不产出、
	// succeeded 不再必填、验证不再比对）。字段保留仅为不改变 RecordSpec 的对外形状，
	// 现有调用方即使仍传值也不会落盘，也不参与任何判定。
	GitCommit string
	// Ledger 是唯一账本（failed 必给；succeeded 给了则必须已全部写入）。
	Ledger *PathLedger
}

// RecordResult 是一次回写的回执（报告与 CLI 输出**一律从这里取数**，不再自己数一遍）。
type RecordResult struct {
	ID     ID
	Rel    string
	Hash   string
	Status ExecStatus
	// Written / Unwritten / Counts / Skips 全部来自同一个账本对象。
	Written   []string
	Unwritten []string
	Counts    Counts
	Skips     []PathSkip
	// Message 是进报告的结论文本（failed 一律带上 B4 的「保留现状、未做还原」口径）。
	Message string
}

// CheckExecutionRecord 判定回写入参是否满足 §4.1 的必写字段规则（不碰磁盘）。
//
//	not_started  不是执行结果：回写口不接受它（模板创建时才产出该值）
//	succeeded    必写 attempted_at；带账本时必须**全部**已写入
//	             （A-32：git_commit 已整键退役，succeeded 不再必填 git_commit）
//	failed       必写 attempted_at + reason + **账本**（两个路径键必须存在，可为空数组）
func CheckExecutionRecord(spec RecordSpec) error {
	field := KeyExecBlock + "." + KeyExecStatus
	if !spec.Status.Valid() {
		return violate(ViolationExecEnum, field,
			"%s = %q，三态封闭：%s", field, spec.Status, joinExecStatuses())
	}
	if spec.Status == ExecNotStarted {
		return violate(ViolationExecFields, field,
			"%s 不是执行结果：回写口只接受 %s / %s（默认值只由模板产出）",
			ExecNotStarted, ExecSucceeded, ExecFailed)
	}
	if spec.AttemptedAt.IsZero() {
		return violate(ViolationExecFields, KeyExecBlock+"."+KeyAttemptedAt,
			"%s = %s 必写 %s", field, spec.Status, KeyAttemptedAt)
	}
	switch spec.Status {
	case ExecSucceeded:
		// A-32「整键删除」：succeeded 不再必写 git_commit（回执 SHA 由报告 git.commit + Git 历史承载）。
		if spec.Ledger != nil && !spec.Ledger.Complete() {
			return violate(ViolationExecFields, KeyUnwrittenPaths,
				"%s = %s 却仍有 %d 条未写路径：部分写入必须如实记成 %s",
				field, ExecSucceeded, spec.Ledger.Counts().Unwritten, ExecFailed)
		}
	case ExecFailed:
		if strings.TrimSpace(spec.Reason) == "" {
			return violate(ViolationExecFields, KeyExecBlock+"."+KeyExecReason,
				"%s = %s 必写 %s", field, ExecFailed, KeyExecReason)
		}
		if spec.Ledger == nil {
			return violate(ViolationExecFields, KeyWrittenPaths,
				"%s = %s 必须逐路径列出 %s 与 %s：两键必须存在（可为空数组）",
				field, ExecFailed, KeyWrittenPaths, KeyUnwrittenPaths)
		}
	}
	if spec.Ledger != nil {
		return spec.Ledger.Verify()
	}
	return nil
}

// RecordExecution 把一次执行的真实结果回写进提案的 `execution` 块。
//
// 固定次序，任一步不过即返回错误且**零写入**（B4：保留现状，不做破坏性动作）：
//
//	① 入参必写字段与账本等式复核（CheckExecutionRecord）
//	② 读盘 + 只读解析 + 落盘形态复核
//	③ **可达矩阵**复核：磁盘上的 status × 本次 execution 必须落在 ✅ 格
//	④ 字节级替换 execution 块的 5 个子键（其余字节一字不动；A-32 后 git_commit 不再回写）
//	⑤ 写前复核：字节自检 + 形态 + 一致性 + status 逐字未变 + 回写值逐字等于入参 + 矩阵再判
//	⑥ guarded 写口落盘（content_hash 比对 → tmp + fsync + rename）
func RecordExecution(spec RecordSpec) (RecordResult, error) {
	res := RecordResult{ID: spec.ID, Status: spec.Status}
	if spec.Store == nil {
		return res, ErrNoStore
	}
	if err := CheckExecutionRecord(spec); err != nil {
		return res, err
	}
	rel := spec.Rel
	if rel == "" {
		rel = Rel(spec.ID)
	}
	res.Rel = rel

	f, err := spec.Store.Read(rel)
	if err != nil {
		return res, err
	}
	before, err := Parse(f.Bytes)
	if err != nil {
		return res, err
	}
	if err := ValidateLayout(before); err != nil {
		return res, err
	}
	if spec.ID != "" && before.P.ID != spec.ID {
		return res, violate(ViolationIDShape, KeyID,
			"%s 的 id 是 %s，与待回写的提案 %s 不同：定位靠 frontmatter 的 id（F2）",
			rel, before.P.ID, spec.ID)
	}
	// ③ 落盘前的最后一道组合校验：不可达组合一律拒写（本工具因此写不出红格 / ⚠️ 格）。
	if err := CheckReachable(before.P.Status, spec.Status); err != nil {
		return res, err
	}

	out, err := applyExecution(f.Bytes, spec)
	if err != nil {
		return res, err
	}
	if err := verifyExecution(before, out, spec); err != nil {
		return res, err
	}
	upd, err := spec.Store.ApplyProposalUpdate(store.ProposalUpdateSpec{
		Rel: rel, ExpectedHash: f.Hash, Content: out,
	})
	if err != nil {
		return res, err
	}
	res.Hash = upd.Hash
	res.ID = before.P.ID
	if spec.Ledger != nil {
		res.Written, res.Unwritten = spec.Ledger.Written(), spec.Ledger.Unwritten()
		res.Counts, res.Skips = spec.Ledger.Counts(), spec.Ledger.Skips()
	} else {
		res.Written, res.Unwritten = []string{}, []string{}
	}
	res.Message = executionMessage(before.P.ID, spec, res.Counts)
	return res, nil
}

// executionMessage 拼进报告的结论文本：failed 一律写明 B4 口径（保留现状、未做还原）。
func executionMessage(id ID, spec RecordSpec, c Counts) string {
	head := fmt.Sprintf("提案 %s 的 %s.%s = %s（%s：%s）",
		id, KeyExecBlock, KeyExecStatus, spec.Status, KeyAttemptedAt, spec.AttemptedAt)
	if spec.Status != ExecFailed {
		// A-32「整键删除」：succeeded 不再回写 git_commit，回执 SHA 由报告 git.commit + Git 历史承载。
		return head
	}
	return head + fmt.Sprintf("；影响文件 %d 个：已写 %d 个、未写 %d 个（逐路径在册）；"+
		"已写入的内容保留在磁盘，未做任何还原（B4）；原因：%s",
		c.Total, c.Written, c.Unwritten, spec.Reason)
}

// applyExecution 产出候选字节：`execution` 块的 3 个标量 + 2 个路径数组按字节替换。
// A-32「整键删除」：git_commit 不再在写侧产出；历史提案已有的该键不在此路径上，故不被触碰。
//
// 两个路径数组的取数**只有账本一处**：Written() / Unwritten() 是同一个对象的两个访问器。
func applyExecution(raw []byte, spec RecordSpec) ([]byte, error) {
	attempted := ""
	if !spec.AttemptedAt.IsZero() {
		attempted = spec.AttemptedAt.String()
	}
	out, err := applyFMScalars(raw, []fmScalar{
		{Block: KeyExecBlock, Key: KeyExecStatus, Value: string(spec.Status)},
		{Block: KeyExecBlock, Key: KeyAttemptedAt, Value: attempted},
		{Block: KeyExecBlock, Key: KeyExecReason, Value: spec.Reason},
	})
	if err != nil {
		return nil, err
	}
	written, unwritten := []string{}, []string{}
	if spec.Ledger != nil {
		written, unwritten = spec.Ledger.Written(), spec.Ledger.Unwritten()
	}
	for _, pair := range []struct {
		key   string
		items []string
	}{
		{KeyWrittenPaths, written},
		{KeyUnwrittenPaths, unwritten},
	} {
		out, err = setFMSeq(out, KeyExecBlock, pair.key, pair.items)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// verifyExecution 是写前复核（不过即拒写）：
//
//	既有控制面复核（字节自检 / 形态 / status ⟷ decision.result / 提案链 / 必写字段）
//	→ **正交**：status 与 decision 逐字未变（执行结果不回写用户决定）
//	→ 回写值逐字等于入参（三态 + 时刻 + 原因 + 两个路径数组；A-32 后不再含 git_commit）
//	→ 可达矩阵再判一次（候选字节自身必须落在 ✅ 格）
func verifyExecution(before *File, out []byte, spec RecordSpec) error {
	if err := verifyControlPlane(out, func(id ID) bool {
		return id == ID(before.P.Decision.SupersededBy)
	}); err != nil {
		return err
	}
	after, err := Parse(out)
	if err != nil {
		return err
	}
	if after.P.Status != before.P.Status {
		return violate(ViolationExecFields, KeyStatus,
			"回写 %s 改动了 status（%s → %s）：两维正交，执行结果不得回写用户决定",
			KeyExecBlock, before.P.Status, after.P.Status)
	}
	for _, pair := range [][3]string{
		{KeyResult, string(after.P.Decision.Result), string(before.P.Decision.Result)},
		{KeyReason, after.P.Decision.Reason, before.P.Decision.Reason},
		{KeySupersededBy, after.P.Decision.SupersededBy, before.P.Decision.SupersededBy},
	} {
		if pair[1] != pair[2] {
			return violate(ViolationExecFields, KeyDecision+"."+pair[0],
				"回写 %s 改动了 %s.%s（%q → %q）：两维正交，执行结果不得回写用户决定",
				KeyExecBlock, KeyDecision, pair[0], pair[2], pair[1])
		}
	}
	got := after.P.Execution
	if got.Status != spec.Status {
		return violate(ViolationExecFields, KeyExecBlock+"."+KeyExecStatus,
			"回写后 %s = %q，入参是 %q", KeyExecStatus, got.Status, spec.Status)
	}
	wantAttempted := ""
	if !spec.AttemptedAt.IsZero() {
		wantAttempted = spec.AttemptedAt.String()
	}
	for _, pair := range [][3]string{
		{KeyAttemptedAt, got.AttemptedAt, wantAttempted},
		{KeyExecReason, got.Reason, spec.Reason},
	} {
		if pair[1] != pair[2] {
			return violate(ViolationExecFields, KeyExecBlock+"."+pair[0],
				"回写后 %s = %q，入参是 %q", pair[0], pair[1], pair[2])
		}
	}
	written, unwritten := []string{}, []string{}
	if spec.Ledger != nil {
		written, unwritten = spec.Ledger.Written(), spec.Ledger.Unwritten()
	}
	for _, pair := range []struct {
		key  string
		got  []string
		want []string
	}{
		{KeyWrittenPaths, got.WrittenPaths, written},
		{KeyUnwrittenPaths, got.UnwrittenPaths, unwritten},
	} {
		if !sameStrings(pair.got, pair.want) {
			return violate(ViolationExecFields, KeyExecBlock+"."+pair.key,
				"回写后 %s = %v，账本给的是 %v（唯一计数来源是账本）", pair.key, pair.got, pair.want)
		}
	}
	return CheckReachable(after.P.Status, got.Status)
}

// sameStrings 逐条比较两个清单（顺序敏感：账本的顺序就是落盘顺序）。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// —— ③ frontmatter 块状序列的字节级替换 ——

// setFMSeq 把 frontmatter 某个块内的**序列键**整体替换成新清单（字节级区间拼接）。
//
// 定位口径与 setFMScalar 同源（fmLineSpan）：先命中键行，再把紧随其后的序列项行一并纳入
// 待替换区间（seqSpanEnd）。空清单写成 `key: []`（提案的路径键**必须存在**，
// 因此空值不能省键）；非空写成块状序列，缩进在键行缩进上再加两格。
// 全程只做 []byte 区间拼接，**不经任何 YAML 序列化器**。
func setFMSeq(raw []byte, block, key string, items []string) ([]byte, error) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !doc.HasFM {
		return nil, violate(ViolationNoFrontmatter, "", "提案必须有 frontmatter")
	}
	start, end, indent, ok := fmLineSpan(raw, doc.FMStart, doc.FMEnd, block, key)
	if !ok {
		field := key
		if block != "" {
			field = block + "." + key
		}
		return nil, fmt.Errorf("%w：%s", ErrFMKeyNotFound, field)
	}
	end = seqSpanEnd(raw, end, doc.FMEnd, indent)
	lines, err := seqLines(indent, key, items)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)-(end-start)+len(lines))
	out = append(out, raw[:start]...)
	out = append(out, lines...)
	return append(out, raw[end:]...), nil
}

// seqSpanEnd 从键行之后继续吃掉属于该键的序列项行：缩进**更深**且以 `-` 开头的行。
// 遇到空行、注释、缩进不更深的行或 frontmatter 结束即停。
func seqSpanEnd(raw []byte, at, fmEnd int, indent string) int {
	for at < fmEnd {
		stop := fmEnd
		if nl := bytes.IndexByte(raw[at:fmEnd], '\n'); nl >= 0 {
			stop = at + nl + 1
		}
		line := raw[at:stop]
		trimmed := bytes.TrimLeft(line, " \t")
		lead := len(line) - len(trimmed)
		if lead <= len(indent) || len(trimmed) == 0 || trimmed[0] != '-' {
			return at
		}
		at = stop
	}
	return at
}

// seqLines 拼「键行 + 序列项行」；空清单写成 `key: []`。
func seqLines(indent, key string, items []string) ([]byte, error) {
	if len(items) == 0 {
		return []byte(indent + key + ": []\n"), nil
	}
	out := []byte(indent + key + ":\n")
	for _, it := range items {
		q, err := quoted(it)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", key, err)
		}
		out = append(out, indent...)
		out = append(out, ' ', ' ', '-', ' ')
		out = append(out, q...)
		out = append(out, '\n')
	}
	return out, nil
}
