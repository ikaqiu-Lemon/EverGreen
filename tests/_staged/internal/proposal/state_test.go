package proposal

// 提案四态状态机的表驱动用例集（T-…-034；提案合同 §2 全节 + §9 前四行判定方式）。
//
// 用例名逐字采用提案合同 §9 的约定形态：
//   - TestStatus_ExactlyFourValues      四态封闭（draft / applied / candidate 三反例）
//   - TestStateMachine_LegalTransitions 合法边**恰 5 行**（T0–T4）
//   - TestStateMachine_IllegalTransitions 非法边**恰 12 行**（4×4 − 4，含自环与终态出边）
//   - TestTerminalStates                终态不可再迁
//   - TestDecisionResultMatchesStatus   status ⟷ decision.result 一致性（M-5 / A-20）
//
// 三条共同断言（每条非法用例都要成立）：
//   ① 结构化错误的 kind / DiagClass 落在正确的诊断族（037 据此发码，本包不写编号）；
//   ② 映射到的退出码是 ExitCodeValidation（= 2，校验失败）；
//   ③ **目标文件字节不变**——用例把提案夹具落到 t.TempDir()，判定前后各算一次 SHA256，
//      逐字相等即证「零写入」（本包是纯函数层，没有写口，这条是结构性反证）。

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// —— 夹具：一份落在临时目录里的合法提案（只用于「字节不变」反证）——

// stateFixture 用**产品自己的模板渲染器**落一份合规提案到 t.TempDir()，
// 返回路径与其 SHA256。模板恒产出 status: pending（T0 的止态），后续判定一律是纯函数，
// 因此这份文件在任何拒绝路径上都必须逐字节不变。
func stateFixture(t *testing.T) (string, string) {
	t.Helper()
	raw, err := RenderTemplate(Template{
		ID:        "p-20260701-001",
		Title:     "逻辑删除一张过时卡",
		CreatedAt: "2026-07-01",
		Targets:   []string{"k-20260601-001"},
	})
	if err != nil {
		t.Fatalf("渲染提案夹具失败：%v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, DirProposals, "p-20260701-001.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatalf("写夹具失败：%v", err)
	}
	return p, fileSHA(t, p)
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件失败：%v", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// assertRejected 是三条共同断言的唯一落点：kind + 诊断族 + 退出码 + 文件字节不变。
func assertRejected(t *testing.T, err error, wantKind ViolationKind, wantClass DiagClass,
	path, before string) {
	t.Helper()
	if err == nil {
		t.Fatalf("必须被拒绝，实为通过")
	}
	if got := KindOf(err); got != wantKind {
		t.Fatalf("violation kind = %q，期望 %q（err=%v）", got, wantKind, err)
	}
	if got := DiagClassOf(KindOf(err)); got != wantClass {
		t.Fatalf("诊断族 = %q，期望 %q（037 据此发码）", got, wantClass)
	}
	if ExitCodeValidation != 2 {
		t.Fatalf("校验失败必须映射到退出码 2，实为 %d", ExitCodeValidation)
	}
	if after := fileSHA(t, path); after != before {
		t.Fatalf("拒绝路径必须零写入：SHA256 %s → %s", before, after)
	}
}

// —— ① 四态封闭：三个反例各断言「枚举越界 + 退 2 + 零写入」——

func TestStatus_ExactlyFourValues(t *testing.T) {
	// 四态集合本身逐字锁死（顺序 = 合同 §2.1 表的行序）。
	want := []Status{StatusPending, StatusApproved, StatusRejected, StatusSuperseded}
	got := Statuses()
	if len(got) != len(want) {
		t.Fatalf("status 取值数 = %d，必须恰 4（不存在第五种）", len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个 status = %q，期望 %q", i+1, got[i], want[i])
		}
	}
	if len(allStatuses()) != 4 {
		t.Fatalf("非法边表的迭代源必须恰 4 态，实为 %d", len(allStatuses()))
	}

	path, before := stateFixture(t)
	for _, bad := range []Status{"draft", "applied", "candidate"} {
		t.Run(string(bad), func(t *testing.T) {
			if bad.Valid() {
				t.Fatalf("%q 不得被判为合法 status", bad)
			}
			assertRejected(t, CheckStatus(bad),
				ViolationStatusEnum, DiagEnumOrConsistency, path, before)
			// 越界值出现在迁移的任一端都必须被拦下（不是只在读盘时才判）。
			assertRejected(t, CheckTransition(StatusPending, bad),
				ViolationStatusEnum, DiagEnumOrConsistency, path, before)
			assertRejected(t, CheckTransition(bad, StatusApproved),
				ViolationStatusEnum, DiagEnumOrConsistency, path, before)
		})
	}
	// 初始伪态 [*] 永不落盘：它不是第五个取值。
	if StatusNone.Valid() {
		t.Fatalf("初始伪态 [*] 不得算合法 status 取值")
	}
	if err := CheckStatus(StatusNone); err == nil {
		t.Fatalf("落盘的 status 不得为空")
	}
}

// —— ② 合法边恰 5 条（表驱动 5 行，T0–T4 逐行）——

func TestStateMachine_LegalTransitions(t *testing.T) {
	// 表驱动 5 行：id + 起止态 + 必写字段，逐字对齐合同 §2.3 的表。
	cases := []struct {
		id       string
		from, to Status
		required []string
	}{
		{"T0:", StatusNone, StatusPending,
			[]string{"id", "type", "status", "created_at", "targets", "impact"}},
		{"T1:", StatusPending, StatusApproved, []string{"decision.result"}},
		{"T2:", StatusPending, StatusRejected,
			[]string{"decision.result", "decision.reason"}},
		{"T3:", StatusPending, StatusSuperseded,
			[]string{"decision.result", "decision.superseded_by"}},
		{"T4:", StatusApproved, StatusSuperseded,
			[]string{"decision.result", "decision.superseded_by"}},
	}
	if len(LegalEdges()) != len(cases) {
		t.Fatalf("合法边 = %d 条，必须恰 %d 条（真实迁移 4 + 初始边 1）",
			len(LegalEdges()), len(cases))
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			e, ok := LegalEdge(c.from, c.to)
			if !ok {
				t.Fatalf("%s %s → %s 必须是合法边", c.id, c.from, c.to)
			}
			if e.ID+":" != c.id {
				t.Fatalf("边号 = %q，期望 %q", e.ID, c.id)
			}
			if err := CheckTransition(c.from, c.to); err != nil {
				t.Fatalf("%s 合法边被拒：%v", c.id, err)
			}
			if len(e.Required) != len(c.required) {
				t.Fatalf("%s 必写字段数 = %d，期望 %d：%v",
					c.id, len(e.Required), len(c.required), e.Required)
			}
			for i := range c.required {
				if e.Required[i] != c.required[i] {
					t.Fatalf("%s 第 %d 个必写字段 = %q，期望 %q",
						c.id, i+1, e.Required[i], c.required[i])
				}
			}
			// 必写字段缺一即拒（缺失路径逐字进错误消息，便于 CLI 直接透出）。
			present := map[string]bool{}
			for _, k := range c.required {
				present[k] = true
			}
			if err := CheckRequiredFields(e, present); err != nil {
				t.Fatalf("%s 必写字段齐备仍被拒：%v", c.id, err)
			}
			delete(present, c.required[0])
			err := CheckRequiredFields(e, present)
			if err == nil || KindOf(err) != ViolationRequiredField {
				t.Fatalf("%s 缺 %s 必须被拒，实为 %v", c.id, c.required[0], err)
			}
		})
	}
	// 出边数逐态复算：pending 3 条、approved 1 条（A-22）、两个终态 0 条。
	for st, want := range map[Status]int{
		StatusPending: 3, StatusApproved: 1, StatusRejected: 0, StatusSuperseded: 0,
	} {
		if got := len(OutEdges(st)); got != want {
			t.Fatalf("%s 的合法出边 = %d 条，期望 %d", st, got, want)
		}
	}
}

// —— ③ 非法边恰 12 条（4×4 全集减法导出，逐行断言 E9 族 + 退 2 + 字节不变）——

func TestStateMachine_IllegalTransitions(t *testing.T) {
	path, before := stateFixture(t)

	// 期望表**手写 12 行**，与代码里的减法表对撞：两者不等即说明有人动了状态机。
	want := []Transition{
		{StatusPending, StatusPending},
		{StatusApproved, StatusPending},
		{StatusApproved, StatusApproved},
		{StatusApproved, StatusRejected},
		{StatusRejected, StatusPending},
		{StatusRejected, StatusApproved},
		{StatusRejected, StatusRejected},
		{StatusRejected, StatusSuperseded},
		{StatusSuperseded, StatusPending},
		{StatusSuperseded, StatusApproved},
		{StatusSuperseded, StatusRejected},
		{StatusSuperseded, StatusSuperseded},
	}
	got := IllegalTransitions()
	if len(got) != 12 || len(want) != 12 {
		t.Fatalf("非法边 = %d 条（期望表 %d 行），必须恰 12 条 = 4×4 − 4",
			len(got), len(want))
	}
	index := map[Transition]bool{}
	for _, tr := range got {
		index[tr] = true
	}
	for _, tr := range want {
		if !index[tr] {
			t.Fatalf("%s → %s 必须在非法边表里（减法表漏项）", tr.From, tr.To)
		}
	}
	// 自环 4 条 + 异向 8 条（§2.4 的分解，逐类复算）。
	self, cross := 0, 0
	for _, tr := range got {
		if tr.From == tr.To {
			self++
		} else {
			cross++
		}
	}
	if self != 4 || cross != 8 {
		t.Fatalf("非法边分解 = 自环 %d / 异向 %d，期望 4 / 8", self, cross)
	}

	for _, tr := range got {
		t.Run(string(tr.From)+"->"+string(tr.To), func(t *testing.T) {
			assertRejected(t, CheckTransition(tr.From, tr.To),
				ViolationIllegalTransition, DiagIllegalTransition, path, before)
		})
	}

	// 四条最易写错的边**各有独立断言**（§2.4 末段逐条点名）。
	for _, tr := range []Transition{
		{StatusApproved, StatusRejected},
		{StatusApproved, StatusPending},
		{StatusRejected, StatusPending},
		{StatusSuperseded, StatusApproved},
	} {
		if IsLegalTransition(tr.From, tr.To) {
			t.Fatalf("%s → %s 必须非法", tr.From, tr.To)
		}
		assertRejected(t, CheckTransition(tr.From, tr.To),
			ViolationIllegalTransition, DiagIllegalTransition, path, before)
	}
}

// —— ④ 终态：rejected / superseded 的任意出边都非法 ——

func TestTerminalStates(t *testing.T) {
	path, before := stateFixture(t)

	if got := TerminalStatuses(); len(got) != 2 ||
		got[0] != StatusRejected || got[1] != StatusSuperseded {
		t.Fatalf("终态集合 = %v，期望恰 [rejected superseded]", got)
	}
	for _, st := range []Status{StatusRejected, StatusSuperseded} {
		if !IsTerminal(st) {
			t.Fatalf("%s 必须是终态", st)
		}
		if n := len(OutEdges(st)); n != 0 {
			t.Fatalf("终态 %s 不得有合法出边，实为 %d 条", st, n)
		}
		for _, to := range allStatuses() {
			t.Run(string(st)+"->"+string(to), func(t *testing.T) {
				assertRejected(t, CheckTransition(st, to),
					ViolationIllegalTransition, DiagIllegalTransition, path, before)
			})
		}
	}
	// A-22 临时裁决：approved **不是**终态，唯一合法出边 → superseded。
	if IsTerminal(StatusApproved) {
		t.Fatalf("approved 按 A-22 判为非终态")
	}
	outs := OutEdges(StatusApproved)
	if len(outs) != 1 || outs[0].To != StatusSuperseded {
		t.Fatalf("approved 的唯一合法出边必须是 → superseded，实为 %v", outs)
	}
	// pending 不是终态（三条出边）。
	if IsTerminal(StatusPending) {
		t.Fatalf("pending 不是终态")
	}
}

// —— ⑤ status ⟷ decision.result 一致性（M-5 / A-20）——

func TestDecisionResultMatchesStatus(t *testing.T) {
	path, before := stateFixture(t)

	// ① status != decision.result → 不一致。
	bad := Proposal{Status: StatusApproved, Decision: Decision{Result: StatusRejected}}
	assertRejected(t, CheckDecisionConsistency(bad),
		ViolationDecisionMismatch, DiagEnumOrConsistency, path, before)

	// ② pending 且 decision.result 非空 → 不一致（还没有决定）。
	bad2 := Proposal{Status: StatusPending, Decision: Decision{Result: StatusApproved}}
	assertRejected(t, CheckDecisionConsistency(bad2),
		ViolationDecisionMismatch, DiagEnumOrConsistency, path, before)

	// ③ approved 且 decision.result == approved → 通过。
	ok := Proposal{Status: StatusApproved, Decision: Decision{Result: StatusApproved}}
	if err := CheckDecisionConsistency(ok); err != nil {
		t.Fatalf("一致的记录副本被拒：%v", err)
	}
	// pending 且 result 为空 → 通过；四态越界先报枚举越界（不是一致性错）。
	if err := CheckDecisionConsistency(
		Proposal{Status: StatusPending}); err != nil {
		t.Fatalf("pending + 空 result 被拒：%v", err)
	}
	assertRejected(t, CheckDecisionConsistency(Proposal{Status: "draft"}),
		ViolationStatusEnum, DiagEnumOrConsistency, path, before)
}

// —— ⑥ 本包不发编号：DiagClass 是唯一对外映射面 ——

func TestDiagClassMapsToM3Families(t *testing.T) {
	for kind, want := range map[ViolationKind]DiagClass{
		ViolationStatusEnum:        DiagEnumOrConsistency,
		ViolationExecEnum:          DiagEnumOrConsistency,
		ViolationDecisionMismatch:  DiagEnumOrConsistency,
		ViolationIllegalTransition: DiagIllegalTransition,
		ViolationSections:          DiagNone,
		ViolationRequiredField:     DiagNone,
	} {
		if got := DiagClassOf(kind); got != want {
			t.Fatalf("DiagClassOf(%q) = %q，期望 %q", kind, got, want)
		}
	}
}
