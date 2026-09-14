package proposal

// `execution=failed` 的**逐路径**账本与回写（提案合同 §4.1 / §4.3、§9 的
// 「execution=failed 逐路径 / 失败不回滚」两行；T-evergreen.s1_main_flow-158614-036）。
//
// 用例名逐字采用 task Acceptance / verify 的约定形态：
//   - TestExecutionFailed_ListsWrittenAndUnwrittenPaths  两键齐全、并集 == 全集、交集为空
//   - TestExecutionPaths_ConsistentWithReport            unwritten ⊇ 报告 skipped 中属本提案者
//   - TestExecution_ManualEditNotTrusted                 手工改写的 execution 不被采信
//   - TestWrittenPaths_SingleSourceOfTruth               唯一计数来源（I-…-002 的 DoR 反证）
//
// 全部用例在**真实 vault**（t.TempDir() + 真实 Markdown + guarded store）上跑，
// 断言一律读回落盘字节。

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// fxStamp 是回写用的尝试时刻（本包不读系统时钟：时刻一律由调用方给，结果可复算）。
var fxStamp = model.NewStamp(mustDate("2026-10-20").Time())

// fxRels 把提案 targets 解析成 vault 内相对路径（定位靠 id 索引，不猜文件名）。
func fxRels(t *testing.T, s *store.Store) []string {
	t.Helper()
	idx, err := s.ScanIDs()
	if err != nil {
		t.Fatalf("ScanIDs：%v", err)
	}
	var out []string
	for _, id := range fxTargets() {
		rel, err := idx.Resolve(id)
		if err != nil {
			t.Fatalf("解析 target %s：%v", id, err)
		}
		out = append(out, rel)
	}
	return out
}

// newFullLedger 用提案影响文件全集开一本空账（一条都还没写）。
func newFullLedger(t *testing.T, s *store.Store) *PathLedger {
	t.Helper()
	led, err := NewPathLedger(fxRels(t, s))
	if err != nil {
		t.Fatalf("NewPathLedger：%v", err)
	}
	return led
}

// approvedFixture 落一份**已批准**提案（approved 行才可能有执行结果）。
func approvedFixture(t *testing.T, root string) []byte {
	t.Helper()
	return writeProposalFixture(t, root, fxProposal, StatusApproved, fxTargets(),
		wantFixtureImpact(), "")
}

// TestExecutionFailed_ListsWrittenAndUnwrittenPaths 钉死 §4.3：`failed` 时逐路径列出
// 已写 / 未写，**两键都在**、**并集恰等于影响文件全集**、**交集为空**，
// 且跳过原因能定位到未写路径；失败一律保留现状（B4）。
func TestExecutionFailed_ListsWrittenAndUnwrittenPaths(t *testing.T) {
	s, root := newFixtureVault(t)
	before := approvedFixture(t, root)
	snap := snapshotKnowledge(t, root)

	all := fxRels(t, s)
	if len(all) != 2 {
		t.Fatalf("夹具影响文件全集 = %d 个，期望 2（一张卡 + 一篇笔记）", len(all))
	}
	led, err := NewPathLedger(all)
	if err != nil {
		t.Fatalf("NewPathLedger：%v", err)
	}
	// 第一个文件写成，第二个文件被 B3 拦下（部分成功：不假装全失败，也不假装全成功）。
	if err := led.MarkWritten(all[0]); err != nil {
		t.Fatalf("MarkWritten：%v", err)
	}
	if err := led.MarkSkipped(all[1], store.SkipFileChanged, "自读取以来文件已变化"); err != nil {
		t.Fatalf("MarkSkipped：%v", err)
	}

	res, err := RecordExecution(RecordSpec{
		Store: s, ID: fxProposal, Status: ExecFailed, AttemptedAt: fxStamp,
		Reason: "第二个文件写入失败：已写入的内容保留在磁盘，未做任何还原", Ledger: led,
	})
	if err != nil {
		t.Fatalf("RecordExecution（failed）：%v", err)
	}

	got, raw := readProposal(t, s, fxProposal)
	ex := got.P.Execution
	if ex.Status != ExecFailed {
		t.Fatalf("%s.%s = %s，期望 %s", KeyExecBlock, KeyExecStatus, ex.Status, ExecFailed)
	}
	if ex.AttemptedAt != fxStamp.String() || ex.Reason == "" {
		t.Fatalf("%s 必写 %s 与 %s，实得 %+v", ExecFailed, KeyAttemptedAt, KeyExecReason, ex)
	}
	if len(ex.WrittenPaths) == 0 {
		t.Fatalf("%s 非空断言不成立：%v", KeyWrittenPaths, ex.WrittenPaths)
	}
	if len(ex.UnwrittenPaths) == 0 {
		t.Fatalf("%s 非空断言不成立：%v", KeyUnwrittenPaths, ex.UnwrittenPaths)
	}
	// —— 可机械断言的两条等式：并集 == 全集，交集 == ∅ ——
	assertPathsPartition(t, all, ex.WrittenPaths, ex.UnwrittenPaths)
	if ex.WrittenPaths[0] != all[0] || ex.UnwrittenPaths[0] != all[1] {
		t.Fatalf("逐路径事实不符：已写 %v / 未写 %v，期望已写 [%s]、未写 [%s]",
			ex.WrittenPaths, ex.UnwrittenPaths, all[0], all[1])
	}
	// 回执与落盘逐条一致（报告从回执取数，两侧不可能各说一套）。
	if !sameStrings(res.Written, ex.WrittenPaths) || !sameStrings(res.Unwritten, ex.UnwrittenPaths) {
		t.Fatalf("回执与落盘不一致：回执 %v / %v，落盘 %v / %v",
			res.Written, res.Unwritten, ex.WrittenPaths, ex.UnwrittenPaths)
	}
	if res.Counts.Total != len(all) || res.Counts.Written != 1 || res.Counts.Unwritten != 1 {
		t.Fatalf("计数不符：%+v", res.Counts)
	}
	// 跳过原因能定位到未写路径（kind / cause 取 store 的封闭命名，不新增第三种）。
	skips := led.Skips()
	if len(skips) != 1 || skips[0].Rel != all[1] {
		t.Fatalf("跳过原因应恰一条且指向未写路径，实得 %+v", skips)
	}
	if skips[0].Kind != store.SkipFileChanged || skips[0].Cause != store.CauseFor(store.SkipFileChanged) {
		t.Fatalf("跳过的 kind / cause 必须取 store 的封闭命名，实得 %+v", skips[0])
	}
	// B4：报告结论逐字写明「未做任何还原」，且知识数据一字未动、用户内容逐字保留。
	if !strings.Contains(res.Message, "未做任何还原") {
		t.Fatalf("failed 的结论必须写明不做还原，实得 %q", res.Message)
	}
	assertPreservedVerbatim(t, raw)
	assertKnowledgeUntouched(t, root, snap)
	// 正交：status / decision 一个字节没动；变化只落在 execution 块。
	if got.P.Status != StatusApproved || got.P.Decision.Result != StatusApproved {
		t.Fatalf("执行结果不得回写用户决定，实得 %s / %s", got.P.Status, got.P.Decision.Result)
	}
	// execution 块之外的字节（frontmatter 其余键、注释、正文七分区）逐字未变。
	assertOnlyExecutionBlockChanged(t, before, raw)
}

// assertOnlyExecutionBlockChanged 断言两份字节**只有** `execution:` 块内发生变化：
// 块之前的行与块之后的行逐字相同（execution 是 frontmatter 的最后一个键，
// 因此块内行数可以变化 —— 路径数组从 `[]` 变成多行块状序列本就要多占行）。
func assertOnlyExecutionBlockChanged(t *testing.T, before, after []byte) {
	t.Helper()
	cut := func(raw []byte) (string, string, string) {
		lines := strings.Split(string(raw), "\n")
		at := -1
		for i, l := range lines {
			if l == KeyExecBlock+":" {
				at = i
				break
			}
		}
		if at < 0 {
			t.Fatalf("找不到 %s: 块", KeyExecBlock)
		}
		end := at + 1
		for end < len(lines) && strings.HasPrefix(lines[end], mapIndent) {
			end++
		}
		return strings.Join(lines[:at+1], "\n"),
			strings.Join(lines[at+1:end], "\n"),
			strings.Join(lines[end:], "\n")
	}
	bh, bb, bt := cut(before)
	ah, ab, at := cut(after)
	if bh != ah {
		t.Fatalf("%s 块之前的字节被改动了", KeyExecBlock)
	}
	if bt != at {
		t.Fatalf("%s 块之后的字节被改动了（正文七分区必须逐字保留）", KeyExecBlock)
	}
	if bb == ab {
		t.Fatalf("%s 块没有任何变化：回写没生效", KeyExecBlock)
	}
}

// assertPathsPartition 断言「written ∪ unwritten == 全集 ∧ written ∩ unwritten == ∅」。
func assertPathsPartition(t *testing.T, all, written, unwritten []string) {
	t.Helper()
	union := map[string]int{}
	for _, rel := range written {
		union[rel]++
	}
	for _, rel := range unwritten {
		union[rel]++
	}
	for rel, n := range union {
		if n > 1 {
			t.Fatalf("路径 %s 同时出现在两个数组里：交集必须为空", rel)
		}
	}
	if len(union) != len(all) {
		t.Fatalf("并集 %d 条 ≠ 影响文件全集 %d 条：%v ∪ %v vs %v",
			len(union), len(all), written, unwritten, all)
	}
	for _, rel := range all {
		if union[rel] != 1 {
			t.Fatalf("影响文件 %s 不在并集里：并集必须恰等于全集", rel)
		}
	}
}

// TestExecutionPaths_ConsistentWithReport 钉死两侧一致性（M-4 第二条）：
// `unwritten_paths` ⊇ 报告 `skipped[]` 中**属本提案**的条目；
// 全集外的条目不属本提案（忽略），属本提案却被记成已写 = 实现缺陷（直接判失败）。
func TestExecutionPaths_ConsistentWithReport(t *testing.T) {
	s, root := newFixtureVault(t)
	approvedFixture(t, root)

	all := fxRels(t, s)
	led, err := NewPathLedger(all)
	if err != nil {
		t.Fatalf("NewPathLedger：%v", err)
	}
	if err := led.MarkWritten(all[0]); err != nil {
		t.Fatalf("MarkWritten：%v", err)
	}
	if err := led.MarkSkipped(all[1], store.SkipUserBlockUnsafe, "用户分区无法安全保留"); err != nil {
		t.Fatalf("MarkSkipped：%v", err)
	}
	if _, err := RecordExecution(RecordSpec{
		Store: s, ID: fxProposal, Status: ExecFailed, AttemptedAt: fxStamp,
		Reason: "用户分区无法安全保留，未写入", Ledger: led,
	}); err != nil {
		t.Fatalf("RecordExecution：%v", err)
	}

	// 报告 skipped[] 的 target / locator 清单：一条属本提案（未写），一条属别的 op（全集外）。
	reported := []string{all[1], "domains/" + fxDomain + "/knowledge/k-19990101-other.md"}
	if err := led.CoversSkipped(reported); err != nil {
		t.Fatalf("unwritten_paths 应覆盖报告 skipped[] 中属本提案的条目：%v", err)
	}
	got, _ := readProposal(t, s, fxProposal)
	unwritten := map[string]bool{}
	for _, rel := range got.P.Execution.UnwrittenPaths {
		unwritten[rel] = true
	}
	for _, rel := range reported {
		if !led.Has(rel) {
			continue
		}
		if !unwritten[rel] {
			t.Fatalf("报告把 %s 记为跳过，落盘的 %s 里却没有它", rel, KeyUnwrittenPaths)
		}
	}
	// 反证：把一条**已写**路径报成跳过，一致性判据必须报错（不允许两侧各说一套）。
	if err := led.CoversSkipped([]string{all[0]}); err == nil {
		t.Fatalf("已写路径被报成跳过时，一致性判据必须报错")
	}
	// 反证：账本自身的两条等式恒成立。
	if err := led.Verify(); err != nil {
		t.Fatalf("账本等式不成立：%v", err)
	}
}

// TestExecution_ManualEditNotTrusted 钉死 #39 / N-4：`execution.*` 是 CLI 独占，
// 手工改写的值**不被采信** —— CLI 每次执行以自身结果整块覆盖。
func TestExecution_ManualEditNotTrusted(t *testing.T) {
	s, root := newFixtureVault(t)
	approvedFixture(t, root)

	// 手工把 execution.status 改成 succeeded，并往 written_paths 里塞一条假路径。
	fakeRel := "domains/" + fxDomain + "/knowledge/k-19990101-fake.md"
	edited := strings.Replace(string(mustBytes(t, root, Rel(fxProposal))),
		mapIndent+KeyExecStatus+": '"+string(ExecNotStarted)+"'\n",
		mapIndent+KeyExecStatus+": '"+string(ExecSucceeded)+"'\n", 1)
	edited = strings.Replace(edited,
		mapIndent+KeyWrittenPaths+": []\n",
		mapIndent+KeyWrittenPaths+":\n"+seqIndent+"- '"+fakeRel+"'\n", 1)
	writeFile(t, root, Rel(fxProposal), edited)
	manual, _ := readProposal(t, s, fxProposal)
	if manual.P.Execution.Status != ExecSucceeded || len(manual.P.Execution.WrittenPaths) != 1 {
		t.Fatalf("前置条件：手工改写应已落盘，实得 %+v", manual.P.Execution)
	}

	// 再执行一次：本次真实结果是 failed，逐路径事实全部来自账本。
	all := fxRels(t, s)
	led, err := NewPathLedger(all)
	if err != nil {
		t.Fatalf("NewPathLedger：%v", err)
	}
	if err := led.MarkWritten(all[0]); err != nil {
		t.Fatalf("MarkWritten：%v", err)
	}
	if _, err := RecordExecution(RecordSpec{
		Store: s, ID: fxProposal, Status: ExecFailed, AttemptedAt: fxStamp,
		Reason: "本次执行部分完成", Ledger: led,
	}); err != nil {
		t.Fatalf("RecordExecution：%v", err)
	}
	got, raw := readProposal(t, s, fxProposal)
	if got.P.Execution.Status != ExecFailed {
		t.Fatalf("手工填的 %s 不得被采信，实得 %s", ExecSucceeded, got.P.Execution.Status)
	}
	if strings.Contains(string(raw), fakeRel) {
		t.Fatalf("手工塞进 %s 的假路径必须被本次结果整块覆盖", KeyWrittenPaths)
	}
	assertPathsPartition(t, all, got.P.Execution.WrittenPaths, got.P.Execution.UnwrittenPaths)
	if !sameStrings(got.P.Execution.WrittenPaths, led.Written()) {
		t.Fatalf("%s = %v，必须逐条等于账本 %v", KeyWrittenPaths,
			got.P.Execution.WrittenPaths, led.Written())
	}
}

// TestWrittenPaths_SingleSourceOfTruth 是 I-…-002 的**反证用例**：
// 「已写 / 未写」的计数只有**一个**产生点（PathLedger），不存在第二处独立计数。
//
// 三条机械断言：
//   - 结构层：账本里只有**一个**路径清单字段（all）与**一个**可变集合（written）——
//     没有第二份 unwritten 清单可供某处「另数一遍」；
//   - 派生层：穷举全部 2^n 种「已写子集」，逐个断言并集 == 全集 ∧ 交集为空 ∧
//     Written + Unwritten == Total（派生关系不可能只对某些子集成立）；
//   - 落盘层：回写的两个数组逐条等于账本的两个访问器（回写侧不重新收集）。
func TestWrittenPaths_SingleSourceOfTruth(t *testing.T) {
	// —— 结构层 ——
	rt := reflect.TypeOf(PathLedger{})
	fields := map[string]string{}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		fields[f.Name] = f.Type.String()
		if strings.Contains(strings.ToLower(f.Name), "unwritten") {
			t.Fatalf("账本不得持有第二份未写清单字段 %s：未写路径必须是派生量", f.Name)
		}
	}
	if got, want := fields["all"], "[]string"; got != want {
		t.Fatalf("影响文件全集字段 all 的类型 = %q，期望 %q", got, want)
	}
	slices := 0
	for name, typ := range fields {
		if typ == "[]string" {
			slices++
			if name != "all" {
				t.Fatalf("账本里出现了第二个路径清单字段 %s：计数来源必须只有一处", name)
			}
		}
	}
	if slices != 1 {
		t.Fatalf("账本里的路径清单字段 = %d 个，必须恰 1 个（唯一计数来源）", slices)
	}

	// —— 派生层：穷举 2^n 个已写子集 ——
	s, root := newFixtureVault(t)
	approvedFixture(t, root)
	all := fxRels(t, s)
	for mask := 0; mask < 1<<len(all); mask++ {
		led, err := NewPathLedger(all)
		if err != nil {
			t.Fatalf("NewPathLedger：%v", err)
		}
		for i, rel := range all {
			if mask&(1<<i) != 0 {
				if err := led.MarkWritten(rel); err != nil {
					t.Fatalf("MarkWritten：%v", err)
				}
			}
		}
		assertPathsPartition(t, all, led.Written(), led.Unwritten())
		c := led.Counts()
		if c.Written+c.Unwritten != c.Total || c.Total != len(all) {
			t.Fatalf("计数不自洽：%+v（全集 %d）", c, len(all))
		}
		if c.Written != len(led.Written()) || c.Unwritten != len(led.Unwritten()) {
			t.Fatalf("计数与清单长度不一致：%+v vs %d / %d",
				c, len(led.Written()), len(led.Unwritten()))
		}
		if err := led.Verify(); err != nil {
			t.Fatalf("mask=%d 的账本等式不成立：%v", mask, err)
		}
	}

	// —— 落盘层：回写只从账本取数 ——
	led, err := NewPathLedger(all)
	if err != nil {
		t.Fatalf("NewPathLedger：%v", err)
	}
	if err := led.MarkWritten(all[0]); err != nil {
		t.Fatalf("MarkWritten：%v", err)
	}
	res, err := RecordExecution(RecordSpec{
		Store: s, ID: fxProposal, Status: ExecFailed, AttemptedAt: fxStamp,
		Reason: "只写成了第一个文件", Ledger: led,
	})
	if err != nil {
		t.Fatalf("RecordExecution：%v", err)
	}
	got, _ := readProposal(t, s, fxProposal)
	for _, pair := range []struct {
		key           string
		disk, receipt []string
		ledger        []string
	}{
		{KeyWrittenPaths, got.P.Execution.WrittenPaths, res.Written, led.Written()},
		{KeyUnwrittenPaths, got.P.Execution.UnwrittenPaths, res.Unwritten, led.Unwritten()},
	} {
		if !sameStrings(pair.disk, pair.ledger) || !sameStrings(pair.receipt, pair.ledger) {
			t.Fatalf("%s 三处不同源：落盘 %v / 回执 %v / 账本 %v",
				pair.key, pair.disk, pair.receipt, pair.ledger)
		}
	}

	// 账本不接受全集外的事实（第二处计数连入口都没有）。
	if err := led.MarkWritten("domains/" + fxDomain + "/knowledge/k-19990101-outside.md"); err == nil {
		t.Fatal("账本必须拒绝全集外的路径")
	}
}

// TestExecutionRecord_RequiredFields 钉死 §4.1 三态的必写字段与「不接受 not_started」。
func TestExecutionRecord_RequiredFields(t *testing.T) {
	led, err := NewPathLedger([]string{"domains/" + fxDomain + "/knowledge/" + fxCardTarget + ".md"})
	if err != nil {
		t.Fatalf("NewPathLedger：%v", err)
	}
	cases := []struct {
		name string
		spec RecordSpec
	}{
		{"not_started 不是执行结果", RecordSpec{Status: ExecNotStarted, AttemptedAt: fxStamp}},
		{"三态越界", RecordSpec{Status: ExecStatus("done"), AttemptedAt: fxStamp}},
		{"缺 attempted_at", RecordSpec{Status: ExecFailed, Reason: "x", Ledger: led}},
		// A-32「整键删除」：succeeded 不再必写 git_commit——原「succeeded 缺 git_commit 必拒」
		// 用例已退役，正向合法性由 TestSucceededWithoutGitCommitIsValid 钉死。
		{"failed 缺 reason", RecordSpec{Status: ExecFailed, AttemptedAt: fxStamp, Ledger: led}},
		{"failed 缺账本", RecordSpec{Status: ExecFailed, AttemptedAt: fxStamp, Reason: "x"}},
		{"succeeded 却有未写路径", RecordSpec{Status: ExecSucceeded, AttemptedAt: fxStamp,
			GitCommit: "0123456789abcdef", Ledger: led}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := CheckExecutionRecord(c.spec); err == nil {
				t.Fatalf("%s 必须被拒", c.name)
			}
		})
	}
	if err := CheckExecutionRecord(RecordSpec{
		Status: ExecFailed, AttemptedAt: fxStamp, Reason: "第二个文件写入失败", Ledger: led,
	}); err != nil {
		t.Fatalf("合法的 failed 入参被拒：%v", err)
	}
}

// TestSucceededWithoutGitCommitIsValid 钉死 A-32「整键删除」的正向合法性：
// succeeded 不再必写 git_commit——回写口放行、落盘形态合规（5 键）、提案里**不再出现** git_commit 键。
func TestSucceededWithoutGitCommitIsValid(t *testing.T) {
	// ① 纯入参层：succeeded 不带 git_commit（无账本 → 无逐路径义务）必须放行。
	//    （A-32 前此形态会因「缺 git_commit」被拒；这里恰是行为翻转点。）
	if err := CheckExecutionRecord(RecordSpec{
		Status: ExecSucceeded, AttemptedAt: fxStamp,
	}); err != nil {
		t.Fatalf("succeeded 不带 git_commit 必须放行（A-32），实得拒绝：%v", err)
	}

	// ② 真实 vault 落盘层：回写 succeeded（不带 git_commit）→ 成功、形态合规、无 git_commit 键。
	s, root := newFixtureVault(t)
	approvedFixture(t, root)
	led := newFullLedger(t, s)
	for _, rel := range led.All() {
		if err := led.MarkWritten(rel); err != nil {
			t.Fatalf("MarkWritten：%v", err)
		}
	}
	if _, err := RecordExecution(RecordSpec{
		Store: s, ID: fxProposal, Status: ExecSucceeded, AttemptedAt: fxStamp, Ledger: led,
	}); err != nil {
		t.Fatalf("RecordExecution（succeeded 无 git_commit）：%v", err)
	}
	// readProposal 内含 ValidateLayout：落盘形态必须恰是 A-32 后的 5 键（多一个 git_commit 即判死）。
	got, raw := readProposal(t, s, fxProposal)
	if got.P.Execution.Status != ExecSucceeded {
		t.Fatalf("%s.%s = %s，期望 %s", KeyExecBlock, KeyExecStatus, got.P.Execution.Status, ExecSucceeded)
	}
	if got.P.Execution.GitCommit != "" {
		t.Fatalf("A-32：succeeded 不得回写 git_commit，实得 %q", got.P.Execution.GitCommit)
	}
	if strings.Contains(string(raw), KeyGitCommit) {
		t.Fatalf("A-32「整键删除」：提案落盘字节里不得再出现 %q 键：\n%s", KeyGitCommit, raw)
	}
}

// TestLegacyGitCommitKeyPreservedOnRead 钉死 A-32 的读侧兼容：历史提案仍带 execution.git_commit 时，
//
//	A) 纯字节层：Parse 原样读出该键、ValidateLayout **通过**（不参与拒绝）、Render 逐字节复原；
//	B) 落盘回写层：历史提案经 RecordExecution(succeeded) 后，git_commit 字节仍**原样保留**
//	   （不清除、不报错、不迁移；RecordExecution 内的 ValidateLayout(before) 也不再拒绝历史提案）。
func TestLegacyGitCommitKeyPreservedOnRead(t *testing.T) {
	const legacySHA = "legacy-sha-0001beef"
	anchor := "\n  " + KeyWrittenPaths + ": []"
	inject := func(raw string) string {
		out := strings.Replace(raw, anchor,
			"\n  "+KeyGitCommit+": '"+legacySHA+"'"+anchor, 1)
		if out == raw {
			t.Fatalf("注入 git_commit 失败：找不到 %q 锚点", KeyWrittenPaths)
		}
		return out
	}

	// —— A) 纯字节层 ——
	base, err := RenderTemplate(Template{
		ID: fxProposal, Title: "历史提案（带 git_commit）",
		CreatedAt: fxToday.String(), Targets: fxTargets(), Impact: wantFixtureImpact(),
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	legacy := inject(string(base))
	f, err := Parse([]byte(legacy))
	if err != nil {
		t.Fatalf("A-32 读侧兼容：历史提案必须仍能被 Parse，实得错误：%v", err)
	}
	if f.P.Execution.GitCommit != legacySHA {
		t.Fatalf("历史 git_commit 未原样读出：实得 %q，期望 %q", f.P.Execution.GitCommit, legacySHA)
	}
	// 关键：历史键不得让 ValidateLayout 报错（“出现不报错、不参与校验”）。
	if err := ValidateLayout(f); err != nil {
		t.Fatalf("A-32：含唯一历史键 git_commit 的提案 ValidateLayout 必须通过，实得拒绝：%v", err)
	}
	if out := string(f.Render()); out != legacy {
		t.Fatalf("历史 git_commit 未逐字节复原（不得清除/迁移）：\n原=%q\n出=%q", legacy, out)
	}

	// —— B) 落盘 + 回写层 ——
	s, root := newFixtureVault(t)
	approvedFixture(t, root) // 5 键 approved 提案落在 fxProposal
	// 把它改造成“历史提案”：直接在磁盘上注入一行 git_commit（模拟 A-32 前的落盘形态）。
	diskLegacy := inject(string(mustBytes(t, root, Rel(fxProposal))))
	writeFile(t, root, Rel(fxProposal), diskLegacy)

	led := newFullLedger(t, s)
	for _, rel := range led.All() {
		if err := led.MarkWritten(rel); err != nil {
			t.Fatalf("MarkWritten：%v", err)
		}
	}
	// RecordExecution 内会先 ValidateLayout(before)：历史提案不再被拒，回写才能发生。
	if _, err := RecordExecution(RecordSpec{
		Store: s, ID: fxProposal, Status: ExecSucceeded, AttemptedAt: fxStamp, Ledger: led,
	}); err != nil {
		t.Fatalf("历史提案回写 succeeded 被拒（A-32 后不应拒绝）：%v", err)
	}
	got, raw := readProposal(t, s, fxProposal) // 内含 ValidateLayout：历史形态仍须合规
	if got.P.Execution.Status != ExecSucceeded {
		t.Fatalf("%s.%s = %s，期望 %s", KeyExecBlock, KeyExecStatus, got.P.Execution.Status, ExecSucceeded)
	}
	if got.P.Execution.GitCommit != legacySHA {
		t.Fatalf("回写后历史 git_commit 未原样保留：实得 %q，期望 %q", got.P.Execution.GitCommit, legacySHA)
	}
	if want := "  " + KeyGitCommit + ": '" + legacySHA + "'"; !strings.Contains(string(raw), want) {
		t.Fatalf("回写后历史 git_commit 字节未原样保留（不得清除/迁移）：期望含 %q\n%s", want, raw)
	}
}
