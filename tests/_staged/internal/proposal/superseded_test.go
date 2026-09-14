package proposal

// `superseded` 两个触发的机器判据（提案合同 §3 全节、§2.3 的 T3 / T4、§5.3 第 8 / 9a 步、
// §8.2.1 的提案链断裂那一行；T-evergreen.s1_main_flow-158614-035）。
//
// 用例名逐字采用 task Acceptance / verify 里的约定形态：
//   - TestSuperseded_TriggerPartialAccept   S-① 部分接受：三步动作缺一不可
//   - TestSuperseded_TriggerImpactChanged   S-② 前提变化：不执行删除 + 三步动作
//   - TestSupersededBy_Required             两触发都必写 superseded_by，断链即拒 + 零写入
//
// 全部用例都在**真实 vault**（t.TempDir() + 真实 Markdown + guarded store）上跑，
// 断言一律读回落盘字节，不靠函数返回值自证。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 夹具日期与提案 ID（`Today` 一律由用例给：本包不读系统时钟，结果可复算）。
var fxToday = mustDate("2026-10-17")

func mustDate(raw string) model.Date {
	d, err := model.ParseDate(raw)
	if err != nil {
		panic(err)
	}
	return d
}

const (
	fxProposal    = "p-20261017-001" // 原提案（pending）
	fxNextID      = "p-20261017-002" // 同日下一个可用序号 = 后继提案
	fxFMComment   = "# 这行注释不是键：改写后必须逐字保留"
	fxUserContent = "用户手写的替代方案正文：一个字都不许被改写。"
)

// writeProposalFixture 落一份提案：先按模板拼字节，再按需改 status / decision 三键。
//
// 改动一律走产品路径 applyFMScalars（字节级行替换），夹具因此不会「造出产品写不出的形态」。
func writeProposalFixture(t *testing.T, root string, id ID, status Status, targets []string,
	im Impact, supersededBy ID) []byte {
	t.Helper()
	raw, err := RenderTemplate(Template{
		ID: id, Title: "逻辑删除一张卡与其材料",
		CreatedAt: fxToday.String(), Targets: targets, Impact: im,
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	// 在 frontmatter 里塞一行注释、在正文分区里塞一段用户文字：
	// 二者都是「未知内容」，改写后必须逐字保留（F5 / B2）。
	raw = []byte(strings.Replace(string(raw), KeyDecision+":\n",
		fxFMComment+"\n"+KeyDecision+":\n", 1))
	raw = []byte(strings.Replace(string(raw), "## "+SecAlternatives+"\n",
		"## "+SecAlternatives+"\n\n"+fxUserContent+"\n", 1))
	if status != StatusPending {
		sets := []fmScalar{
			{Key: KeyStatus, Value: string(status)},
			{Block: KeyDecision, Key: KeyResult, Value: string(status)},
		}
		if status == StatusRejected {
			sets = append(sets, fmScalar{Block: KeyDecision, Key: KeyReason, Value: "不采纳"})
		}
		if supersededBy != "" {
			sets = append(sets, fmScalar{Block: KeyDecision, Key: KeySupersededBy, Value: string(supersededBy)})
		}
		out, err := applyFMScalars(raw, sets)
		if err != nil {
			t.Fatalf("夹具改 status 失败：%v", err)
		}
		raw = out
	}
	if err := SelfCheck(raw); err != nil {
		t.Fatalf("夹具提案 %s 自检不过：%v", id, err)
	}
	writeFile(t, root, Rel(id), string(raw))
	return raw
}

// readProposal 读回落盘提案并做形态校验（断言一律基于落盘字节）。
func readProposal(t *testing.T, s *store.Store, id ID) (*File, []byte) {
	t.Helper()
	f, err := s.Read(Rel(id))
	if err != nil {
		t.Fatalf("读回提案 %s：%v", id, err)
	}
	p, err := Parse(f.Bytes)
	if err != nil {
		t.Fatalf("解析提案 %s：%v", id, err)
	}
	if err := ValidateLayout(p); err != nil {
		t.Fatalf("提案 %s 落盘形态不合规：%v", id, err)
	}
	return p, f.Bytes
}

// mustBytes 读一份文件的原始字节（用于「知识数据一字未动」的反证）。
func mustBytes(t *testing.T, root, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读 %s：%v", rel, err)
	}
	return b
}

// knowledgeRels 是夹具语料里全部**知识数据**文件（提案控制面之外的一切）。
func knowledgeRels() []string {
	return []string{
		"sources/" + fxSource + ".md",
		"domains/" + fxDomain + "/notes/" + fxNote + ".md",
		"domains/" + fxDomain + "/knowledge/" + fxCardTarget + ".md",
		"domains/" + fxDomain + "/knowledge/" + fxCardKeep + ".md",
		"domains/" + fxDomain + "/reviews/" + fxReview + ".md",
	}
}

// snapshotKnowledge 拍一份知识数据快照。
func snapshotKnowledge(t *testing.T, root string) map[string][]byte {
	t.Helper()
	snap := map[string][]byte{}
	for _, rel := range knowledgeRels() {
		snap[rel] = mustBytes(t, root, rel)
	}
	return snap
}

// assertKnowledgeUntouched 反证「知识数据一个字节都没被改」（A-23：批准后的知识修改走 ChangePlan）。
func assertKnowledgeUntouched(t *testing.T, root string, snap map[string][]byte) {
	t.Helper()
	for rel, want := range snap {
		if got := mustBytes(t, root, rel); string(got) != string(want) {
			t.Fatalf("知识数据 %s 被改写了：提案控制面的写入不得触碰知识数据", rel)
		}
	}
}

// assertPreservedVerbatim 反证注释与用户正文逐字保留。
func assertPreservedVerbatim(t *testing.T, raw []byte) {
	t.Helper()
	for _, keep := range []string{fxFMComment, fxUserContent} {
		if !strings.Contains(string(raw), keep) {
			t.Fatalf("改写后丢了应逐字保留的内容：%q", keep)
		}
	}
}

// assertChainOK 反证提案链成立（superseded_by 能解析到一个存在的提案）。
func assertChainOK(t *testing.T, s *store.Store, p Proposal) {
	t.Helper()
	probe, err := ExistsProbe(s)
	if err != nil {
		t.Fatalf("ExistsProbe：%v", err)
	}
	if err := CheckSupersededChain(p, probe); err != nil {
		t.Fatalf("提案链应当成立，实得违规：%v", err)
	}
}

// —— 触发① 部分接受 ——

// TestSuperseded_TriggerPartialAccept 钉死 §3 表 S-① 行的**三步动作（缺一不可）**：
//
//	① 生成新提案承载实际接受内容 ② 原提案 status = superseded ③ 原提案写 decision.superseded_by
//
// 并反证：提案**不支持部分应用**——被接受的部分由**新提案整体承载**，
// 原提案不被裁剪、知识数据一字不动。
func TestSuperseded_TriggerPartialAccept(t *testing.T) {
	s, root := newFixtureVault(t)
	orig := writeProposalFixture(t, root, fxProposal, StatusPending, fxTargets(), wantFixtureImpact(), "")
	snap := snapshotKnowledge(t, root)

	// 用户只接受「删除那篇材料笔记」这一部分：新提案的 targets 只剩 fxNote，
	// 影响面按**实际接受的那部分**重算（不照抄原提案的 impact）。
	accepted := []string{fxNote}
	acceptedImpact, err := RecomputeImpact(s, accepted)
	if err != nil {
		t.Fatalf("重算接受部分的影响面：%v", err)
	}
	res, err := Supersede(SupersedeSpec{
		Store: s, Original: fxProposal, Trigger: TriggerPartialAccept,
		Reason: "用户只接受删除材料笔记这一部分", Today: fxToday,
		NewTitle: "部分接受：只删除材料笔记", NewTargets: accepted, NewImpact: acceptedImpact,
	})
	if err != nil {
		t.Fatalf("Supersede（触发①）：%v", err)
	}

	// —— 第 ① 步：新提案存在，且是一份**全新的 pending 提案** ——
	if res.NewID != fxNextID {
		t.Fatalf("新提案 ID = %s，期望同日下一个序号 %s", res.NewID, fxNextID)
	}
	next, _ := readProposal(t, s, res.NewID)
	if next.P.Status != StatusPending {
		t.Fatalf("新提案 status = %s，期望 %s", next.P.Status, StatusPending)
	}
	if next.P.Execution.Status != ExecNotStarted {
		t.Fatalf("新提案 execution 状态 = %s，期望 %s", next.P.Execution.Status, ExecNotStarted)
	}
	if len(next.P.Targets) != 1 || next.P.Targets[0] != fxNote {
		t.Fatalf("新提案 targets = %v，期望恰 [%s]（承载实际接受内容）", next.P.Targets, fxNote)
	}
	if !ImpactEqual(next.P.Impact, acceptedImpact) {
		t.Fatalf("新提案 impact 与接受部分的重算结果不一致：%v", ImpactDiff(acceptedImpact, next.P.Impact))
	}
	if next.P.Decision.SupersededBy != "" {
		t.Fatalf("新提案不得预填 %s.%s，实得 %q", KeyDecision, KeySupersededBy, next.P.Decision.SupersededBy)
	}

	// —— 第 ② / ③ 步：原提案标 superseded 且写了 superseded_by ——
	got, raw := readProposal(t, s, fxProposal)
	if got.P.Status != StatusSuperseded {
		t.Fatalf("原提案 status = %s，期望 %s", got.P.Status, StatusSuperseded)
	}
	if got.P.Decision.SupersededBy != string(res.NewID) {
		t.Fatalf("原提案 %s.%s = %q，期望 %s", KeyDecision, KeySupersededBy,
			got.P.Decision.SupersededBy, res.NewID)
	}
	if got.P.Decision.Result != StatusSuperseded {
		t.Fatalf("原提案 %s.%s = %q，必须与 status 逐字一致", KeyDecision, KeyResult, got.P.Decision.Result)
	}
	if got.P.Decision.Reason == "" {
		t.Fatalf("原提案 %s.%s 应记下用户理由", KeyDecision, KeyReason)
	}
	assertChainOK(t, s, got.P)

	// 走的是 T3（pending → superseded），三步轨迹在册。
	if res.From != StatusPending || res.Edge != "T3" {
		t.Fatalf("起态 / 边 = %s / %s，期望 %s / T3", res.From, res.Edge, StatusPending)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("三步动作缺一不可，Steps = %v", res.Steps)
	}
	if !strings.Contains(res.Message, "不支持部分应用") {
		t.Fatalf("触发① 的结论应写明提案不支持部分应用，实得 %q", res.Message)
	}

	// —— 反证：原提案的 targets / impact 未被裁剪；注释与用户正文逐字保留；知识数据一字未动 ——
	if len(got.P.Targets) != len(fxTargets()) {
		t.Fatalf("原提案 targets 被裁剪了：%v", got.P.Targets)
	}
	if !ImpactEqual(got.P.Impact, wantFixtureImpact()) {
		t.Fatalf("原提案 impact 被改写了：%v", ImpactDiff(wantFixtureImpact(), got.P.Impact))
	}
	assertPreservedVerbatim(t, raw)
	assertKnowledgeUntouched(t, root, snap)
	// 原提案只有 status / decision 三行发生变化：其余字节逐行相同。
	if changed := changedLines(orig, raw); changed != 4 {
		t.Fatalf("原提案变化行数 = %d，期望恰 4（status + decision 的 result / reason / superseded_by）", changed)
	}
}

// changedLines 数两份字节里**不同的行数**（两侧行数必须一致：本写路径只做整行替换）。
func changedLines(before, after []byte) int {
	a := strings.Split(string(before), "\n")
	b := strings.Split(string(after), "\n")
	if len(a) != len(b) {
		return -1
	}
	n := 0
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}

// —— 触发② 前提变化 ——

// TestSuperseded_TriggerImpactChanged 钉死 §3 表 S-② 行与 §5.3 第 9a 步：
//
//	`eg proposal approve` **必须**在执行前重算影响面并与 impact 比对；
//	不一致 → ① 不强行执行 ② 生成新提案重新描述当前影响面 ③ 原提案 superseded + superseded_by。
//
// 「先批准后重算」是唯一实现顺序：DecideApprove 的入参就是重算结果，
// 拿不到重算结果就得不到裁决（CLI 侧的调用顺序另有注入计数器用例）。
func TestSuperseded_TriggerImpactChanged(t *testing.T) {
	s, root := newFixtureVault(t)
	writeProposalFixture(t, root, fxProposal, StatusPending, fxTargets(), wantFixtureImpact(), "")

	// ① 语料未变时：重算与 impact 一致 → 走 T1 继续执行。
	p, _ := readProposal(t, s, fxProposal)
	before, err := RecomputeImpact(s, p.P.Targets)
	if err != nil {
		t.Fatalf("执行前重算：%v", err)
	}
	ok, err := DecideApprove(p.P, before)
	if err != nil {
		t.Fatalf("DecideApprove（前提成立）：%v", err)
	}
	if !ok.Proceed || ok.Changed || ok.Edge != "T1" || ok.Trigger != "" {
		t.Fatalf("前提成立时应走 T1 继续执行，实得 %+v", ok)
	}
	if strings.Contains(ok.Message, NotExecutedNotice) {
		t.Fatalf("前提成立时结论不得出现「%s」：%q", NotExecutedNotice, ok.Message)
	}

	// ② 批准前语料变了：把 fxCardKeep 的唯一 support 换成 context —— 它不再会失去 support，
	//    材料关系计数随之下降。影响面与提案里的 impact 不再一致。
	writeFile(t, root, "domains/"+fxDomain+"/knowledge/"+fxCardKeep+".md",
		cardFixture(fxCardKeep, "active",
			sourcesBlock(fxSource, fxNote, "context", "改成背景材料"),
			relationsBlock("supports", fxCardTarget, "支持新结论")))
	snap := snapshotKnowledge(t, root)

	after, err := RecomputeImpact(s, p.P.Targets)
	if err != nil {
		t.Fatalf("变更后重算：%v", err)
	}
	if ImpactEqual(p.P.Impact, after) {
		t.Fatalf("语料已变，重算结果却与 impact 一致：夹具没有真正改变影响面")
	}
	dec, err := DecideApprove(p.P, after)
	if err != nil {
		t.Fatalf("DecideApprove（前提变化）：%v", err)
	}
	if dec.Proceed {
		t.Fatalf("前提变化时**不得**继续执行删除")
	}
	if !dec.Changed || dec.Trigger != TriggerImpactChanged || dec.Edge != "T3" {
		t.Fatalf("前提变化时应判触发② 并走 T3，实得 %+v", dec)
	}
	if !strings.Contains(dec.Message, NotExecutedNotice) {
		t.Fatalf("裁决结论必须逐字含「%s」，实得 %q", NotExecutedNotice, dec.Message)
	}
	if len(dec.Diff) == 0 {
		t.Fatalf("裁决必须给出逐项差异，供报告说明原影响范围为何不再成立")
	}

	// ③ 三步动作落地：新提案承载**重算后的**影响面。
	res, err := Supersede(SupersedeSpec{
		Store: s, Original: fxProposal, Trigger: dec.Trigger,
		Reason: "执行前影响面已变化", Today: fxToday,
		NewTitle: "前提变化：按当前影响面重新描述", NewTargets: p.P.Targets, NewImpact: dec.Recomputed,
	})
	if err != nil {
		t.Fatalf("Supersede（触发②）：%v", err)
	}
	if len(res.Steps) != 3 || res.Edge != "T3" {
		t.Fatalf("触发② 的三步动作 / 边不对：Steps = %v，Edge = %s", res.Steps, res.Edge)
	}
	if !strings.Contains(res.Message, NotExecutedNotice) {
		t.Fatalf("回执结论必须逐字含「%s」，实得 %q", NotExecutedNotice, res.Message)
	}
	next, _ := readProposal(t, s, res.NewID)
	if !ImpactEqual(next.P.Impact, after) {
		t.Fatalf("新提案必须重新描述**当前**影响面：%v", ImpactDiff(after, next.P.Impact))
	}
	got, raw := readProposal(t, s, fxProposal)
	if got.P.Status != StatusSuperseded || got.P.Decision.SupersededBy != string(res.NewID) {
		t.Fatalf("原提案未按三步动作落地：status = %s，%s = %q",
			got.P.Status, KeySupersededBy, got.P.Decision.SupersededBy)
	}
	assertChainOK(t, s, got.P)
	assertPreservedVerbatim(t, raw)

	// ④ **不执行删除**的落盘反证：目标对象一个字节都没被改（deleted_at 仍为空）。
	assertKnowledgeUntouched(t, root, snap)
	for _, rel := range []string{
		"domains/" + fxDomain + "/knowledge/" + fxCardTarget + ".md",
		"domains/" + fxDomain + "/notes/" + fxNote + ".md",
	} {
		if strings.Contains(string(mustBytes(t, root, rel)), KeyDeletedAt) {
			t.Fatalf("%s 出现了 %s：触发② 明确不执行删除", rel, KeyDeletedAt)
		}
	}
	// 执行面一字未动：`approved` / `superseded` 都不等于「已执行」（execution 回写属另一 task）。
	if got.P.Execution.Status != ExecNotStarted {
		t.Fatalf("原提案 execution 状态 = %s，期望仍是 %s", got.P.Execution.Status, ExecNotStarted)
	}
}

// TestSuperseded_TriggersClosedSet 钉「触发恰两个」：封闭集合外的取值直接被 Supersede 拒，且零写入。
func TestSuperseded_TriggersClosedSet(t *testing.T) {
	if len(Triggers()) != 2 {
		t.Fatalf("superseded 触发必须恰 2 个，实得 %d", len(Triggers()))
	}
	for _, tr := range Triggers() {
		if !tr.Valid() || tr.Label() == "" {
			t.Fatalf("触发 %q 应当合法且有中文名", tr)
		}
	}
	if Trigger("manual_edit").Valid() {
		t.Fatalf("封闭集合外的触发不得合法")
	}
	s, root := newFixtureVault(t)
	writeProposalFixture(t, root, fxProposal, StatusPending, fxTargets(), wantFixtureImpact(), "")
	ids, err := ListIDs(s)
	if err != nil {
		t.Fatalf("ListIDs：%v", err)
	}
	if _, err := Supersede(SupersedeSpec{
		Store: s, Original: fxProposal, Trigger: Trigger("manual_edit"), Today: fxToday,
	}); err == nil {
		t.Fatalf("未知触发必须被拒")
	}
	after, err := ListIDs(s)
	if err != nil {
		t.Fatalf("ListIDs：%v", err)
	}
	if len(after) != len(ids) {
		t.Fatalf("被拒的调用不得产生新提案：%v → %v", ids, after)
	}
	p, _ := readProposal(t, s, fxProposal)
	if p.P.Status != StatusPending {
		t.Fatalf("被拒的调用不得改原提案 status，实得 %s", p.P.Status)
	}
}

// —— 提案链：两触发都必写 superseded_by ——

// TestSupersededBy_Required 钉死「两个触发都必须写 decision.superseded_by」：
// 缺失 / 形态非法 / 指向自己 / 解析不到存在的提案 → 结构化违规（提案链断裂族），
// CLI 行为是退 ExitCodeValidation + **零写入**。
//
// 本包**不出现任何诊断码字面量**：编号由 internal/plan 统一发，这里只断言诊断族与退出码。
func TestSupersededBy_Required(t *testing.T) {
	base := Proposal{ID: fxProposal, Type: TypeLogicalDelete, Status: StatusSuperseded}
	exists := func(id ID) bool { return id == fxNextID }

	// ① 四种断链形态逐个必须被判违规。
	for _, tc := range []struct {
		name string
		sb   string
	}{
		{"为空", ""},
		{"形态非法", "p-2026-1"},
		{"指向自己", fxProposal},
		{"指向不存在的提案", "p-20261017-777"},
	} {
		p := base
		p.Decision = Decision{Result: StatusSuperseded, SupersededBy: tc.sb}
		err := CheckSupersededChain(p, exists)
		if err == nil {
			t.Fatalf("%s：提案链断裂必须被判违规", tc.name)
		}
		kind := KindOf(err)
		if kind != ViolationSupersededChain {
			t.Fatalf("%s：违规种类 = %q，期望 %q", tc.name, kind, ViolationSupersededChain)
		}
		if DiagClassOfSupersededChain(kind) != DiagSupersededChain {
			t.Fatalf("%s：诊断族 = %q，期望 %q", tc.name, DiagClassOfSupersededChain(kind), DiagSupersededChain)
		}
		if !strings.Contains(err.Error(), KeyDecision+"."+KeySupersededBy) {
			t.Fatalf("%s：违规说明应指出断链的键，实得 %v", tc.name, err)
		}
	}
	// 退出码恒为 2（校验失败、零写入）。
	if ExitCodeValidation != 2 {
		t.Fatalf("校验失败退出码 = %d，必须是 2", ExitCodeValidation)
	}
	// 既有诊断族表逐字不变：新族只由 DiagClassOfSupersededChain 追加，不改 T-…-034 的映射。
	if DiagClassOf(ViolationSupersededChain) != DiagNone {
		t.Fatalf("不得改写既有 DiagClassOf 的映射")
	}
	if DiagClassOfSupersededChain(ViolationIllegalTransition) != DiagNone {
		t.Fatalf("新族不得吞掉既有 kind：%q", ViolationIllegalTransition)
	}

	// ② 链成立时通过；非 superseded 的三态不受本判据约束（只管 superseded 一行）。
	good := base
	good.Decision = Decision{Result: StatusSuperseded, SupersededBy: fxNextID}
	if err := CheckSupersededChain(good, exists); err != nil {
		t.Fatalf("链成立却被判违规：%v", err)
	}
	for _, st := range []Status{StatusPending, StatusApproved, StatusRejected} {
		p := Proposal{ID: fxProposal, Status: st}
		if err := CheckSupersededChain(p, exists); err != nil {
			t.Fatalf("status = %s 时不该被本判据拦：%v", st, err)
		}
	}
	// 越界 status 仍先撞四态封闭（复用 CheckStatus，不另写一套）。
	bad := Proposal{ID: fxProposal, Status: Status("applied")}
	if KindOf(CheckSupersededChain(bad, exists)) != ViolationStatusEnum {
		t.Fatalf("越界 status 应先撞四态封闭判定")
	}

	// ③ **零写入**反证：只标 superseded 却不写 superseded_by 的候选字节被写前复核拒下，
	//    落盘文件一个字节都没变。
	s, root := newFixtureVault(t)
	orig := writeProposalFixture(t, root, fxProposal, StatusPending, fxTargets(), wantFixtureImpact(), "")
	half, err := applyFMScalars(orig, []fmScalar{
		{Key: KeyStatus, Value: string(StatusSuperseded)},
		{Block: KeyDecision, Key: KeyResult, Value: string(StatusSuperseded)},
	})
	if err != nil {
		t.Fatalf("构造断链候选字节：%v", err)
	}
	probe, err := ExistsProbe(s)
	if err != nil {
		t.Fatalf("ExistsProbe：%v", err)
	}
	err = verifyControlPlane(half, probe)
	if err == nil {
		t.Fatalf("断链候选字节必须被写前复核拒下")
	}
	if KindOf(err) != ViolationSupersededChain {
		t.Fatalf("写前复核的违规种类 = %q，期望 %q", KindOf(err), ViolationSupersededChain)
	}
	if got := mustBytes(t, root, Rel(fxProposal)); string(got) != string(orig) {
		t.Fatalf("拒写路径必须零写入：落盘字节被改了")
	}
	// 反过来：产品路径产出的候选字节（第 ② / ③ 步同一次改写）恒通过复核。
	full, err := markSuperseded(orig, fxNextID, "用户只接受一部分")
	if err != nil {
		t.Fatalf("markSuperseded：%v", err)
	}
	if err := verifyControlPlane(full, func(id ID) bool { return id == fxNextID }); err != nil {
		t.Fatalf("产品路径的候选字节应当通过复核：%v", err)
	}
}

// —— 与四态合同（合法边恰 5 / 非法边恰 12）的一致性 ——

// TestSuperseded_EdgesMatchStateContract 自证本 task 没有绕开 T-…-034 的状态机：
//
//	① 通向 superseded 的**合法**入边恰 2 条（T3 / T4），本 task 两个触发各自落在其中；
//	② superseded 是终态（出边恒 0）；
//	③ §2.4 的 12 条非法边里，凡「→ superseded」的都被 Supersede 拒下且零写入。
func TestSuperseded_EdgesMatchStateContract(t *testing.T) {
	if len(LegalEdges()) != 5 {
		t.Fatalf("合法边必须恰 5 条，实得 %d", len(LegalEdges()))
	}
	if len(IllegalTransitions()) != 12 {
		t.Fatalf("非法边必须恰 12 条，实得 %d", len(IllegalTransitions()))
	}
	var into []string
	for _, e := range LegalEdges() {
		if e.To == StatusSuperseded {
			into = append(into, e.ID)
		}
	}
	if strings.Join(into, ",") != "T3,T4" {
		t.Fatalf("通向 %s 的合法入边 = %v，期望恰 T3 / T4", StatusSuperseded, into)
	}
	if !IsTerminal(StatusSuperseded) || len(OutEdges(StatusSuperseded)) != 0 {
		t.Fatalf("%s 必须是终态且无出边", StatusSuperseded)
	}
	// T4：approved → superseded 走得通（A-22：approved 的唯一出边）。
	s, root := newFixtureVault(t)
	writeProposalFixture(t, root, fxProposal, StatusApproved, fxTargets(), wantFixtureImpact(), "")
	res, err := Supersede(SupersedeSpec{
		Store: s, Original: fxProposal, Trigger: TriggerImpactChanged,
		Reason: "已批准但执行前提变化", Today: fxToday,
		NewTargets: fxTargets(), NewImpact: wantFixtureImpact(),
	})
	if err != nil {
		t.Fatalf("T4（%s → %s）应当走得通：%v", StatusApproved, StatusSuperseded, err)
	}
	if res.From != StatusApproved || res.Edge != "T4" {
		t.Fatalf("起态 / 边 = %s / %s，期望 %s / T4", res.From, res.Edge, StatusApproved)
	}
	got, _ := readProposal(t, s, fxProposal)
	assertChainOK(t, s, got.P)

	// 非法入边（rejected → superseded、superseded → superseded 自环）逐条被拒 + 零写入。
	for _, tc := range []struct {
		from Status
		sb   ID
	}{
		{StatusRejected, ""},
		{StatusSuperseded, fxNextID},
	} {
		if IsLegalTransition(tc.from, StatusSuperseded) {
			t.Fatalf("%s → %s 不该是合法边", tc.from, StatusSuperseded)
		}
		s2, root2 := newFixtureVault(t)
		if tc.sb != "" {
			writeProposalFixture(t, root2, tc.sb, StatusPending, fxTargets(), wantFixtureImpact(), "")
		}
		before := writeProposalFixture(t, root2, fxProposal, tc.from, fxTargets(), wantFixtureImpact(), tc.sb)
		ids, err := ListIDs(s2)
		if err != nil {
			t.Fatalf("ListIDs：%v", err)
		}
		_, err = Supersede(SupersedeSpec{
			Store: s2, Original: fxProposal, Trigger: TriggerPartialAccept, Today: fxToday,
			NewTargets: fxTargets(), NewImpact: wantFixtureImpact(),
		})
		if KindOf(err) != ViolationIllegalTransition {
			t.Fatalf("%s → %s 应当撞非法迁移判定，实得 %v", tc.from, StatusSuperseded, err)
		}
		if got := mustBytes(t, root2, Rel(fxProposal)); string(got) != string(before) {
			t.Fatalf("非法边必须零写入：%s 的落盘字节被改了", fxProposal)
		}
		after, err := ListIDs(s2)
		if err != nil {
			t.Fatalf("ListIDs：%v", err)
		}
		if len(after) != len(ids) {
			t.Fatalf("非法边必须零写入：不得生成新提案（%d → %d）", len(ids), len(after))
		}
	}
}

// TestMarkApproved_ExecutionUntouched 钉 T1 的落盘口径：只改 status 与 decision，
// **`execution` 块一个字节都不动**（`approved` ≠ 已执行；execution 回写属另一 task），
// 且再次批准（approved → approved 自环）撞非法边、零写入。
func TestMarkApproved_ExecutionUntouched(t *testing.T) {
	s, root := newFixtureVault(t)
	orig := writeProposalFixture(t, root, fxProposal, StatusPending, fxTargets(), wantFixtureImpact(), "")
	snap := snapshotKnowledge(t, root)
	if _, err := MarkApproved(ApproveSpec{Store: s, Original: fxProposal, Reason: "影响面已核对"}); err != nil {
		t.Fatalf("MarkApproved：%v", err)
	}
	got, raw := readProposal(t, s, fxProposal)
	if got.P.Status != StatusApproved || got.P.Decision.Result != StatusApproved {
		t.Fatalf("T1 落盘不对：status = %s，%s.%s = %s", got.P.Status, KeyDecision, KeyResult, got.P.Decision.Result)
	}
	if got.P.Execution.Status != ExecNotStarted {
		t.Fatalf("批准后 %s 状态 = %s，必须仍是 %s", KeyExecBlock, got.P.Execution.Status, ExecNotStarted)
	}
	if got.P.Decision.SupersededBy != "" {
		t.Fatalf("T1 不得写 %s", KeySupersededBy)
	}
	if changed := changedLines(orig, raw); changed != 3 {
		t.Fatalf("T1 变化行数 = %d，期望恰 3（status + decision 的 result / reason）", changed)
	}
	assertPreservedVerbatim(t, raw)
	assertKnowledgeUntouched(t, root, snap)

	// 自环（approved → approved）是 12 条非法边之一：拒 + 零写入。
	if _, err := MarkApproved(ApproveSpec{Store: s, Original: fxProposal}); KindOf(err) != ViolationIllegalTransition {
		t.Fatalf("重复批准应撞非法迁移判定，实得 %v", err)
	}
	if again := mustBytes(t, root, Rel(fxProposal)); string(again) != string(raw) {
		t.Fatalf("被拒的重复批准必须零写入")
	}
}

// TestSupersede_GuardedWritePort 反证提案控制面的写入**复用 guarded store**（A-23）：
// content_hash 不匹配（文件在读后被并发改写）时写口拒绝，原文件保持现状。
func TestSupersede_GuardedWritePort(t *testing.T) {
	s, root := newFixtureVault(t)
	orig := writeProposalFixture(t, root, fxProposal, StatusPending, fxTargets(), wantFixtureImpact(), "")
	f, err := s.Read(Rel(fxProposal))
	if err != nil {
		t.Fatalf("读提案：%v", err)
	}
	out, err := markSuperseded(orig, fxNextID, "并发改写反证")
	if err != nil {
		t.Fatalf("markSuperseded：%v", err)
	}
	// 拿着**过期**的 content_hash 去写：guarded 写口必须拒。
	if _, err := s.ApplyProposalUpdate(store.ProposalUpdateSpec{
		Rel: Rel(fxProposal), ExpectedHash: f.Hash + "0", Content: out,
	}); err == nil {
		t.Fatalf("content_hash 不匹配时 guarded 写口必须拒写")
	}
	if got := mustBytes(t, root, Rel(fxProposal)); string(got) != string(orig) {
		t.Fatalf("拒写路径必须保留现状")
	}
	// 提案控制面之外的路径一律不给直写例外（A-23 的唯一例外只有 proposals/**）。
	if store.IsProposalRel("domains/" + fxDomain + "/knowledge/" + fxCardTarget + ".md") {
		t.Fatalf("知识数据路径不得被当成提案控制面")
	}
	if _, err := s.ApplyProposalUpdate(store.ProposalUpdateSpec{
		Rel:     "domains/" + fxDomain + "/knowledge/" + fxCardTarget + ".md",
		Content: []byte("x"),
	}); err == nil {
		t.Fatalf("提案控制面写口不得接受知识数据路径")
	}
}
