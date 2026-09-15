package plan

// 写权限矩阵的逐格反证（授权合同 §2.1–§2.7 + Schema v2 契约 §3.3 恰 50 行 /
// §2.8 计数规则 / §2.9 锁定条款）。
//
// 期望值在本文件里是**写死的**（50 / 100 / 16 / 2 / 5 / 8 与逐格符号），这是刻意的：
// 实现侧一律由符号规则派生，测试侧一律逐字抄合同——两边同时改错才可能同时变绿。
//
// **M4 · T-…-055 阶段 1 按实测重钉**：#33 的 P-A 依 A-34 由 🔴 改 ✅（净放开一格），
// 于是「两格均 🔴」由 M3 期的 5 变 **4**；43 / 86 / 16 / 1 / 7 五个数字一格未动。
// 只改判据形态、不放宽本体：新增的 #33 逐格断言把「P-U 不得顺手放开」也钉死了。
//
// **Schema v2 · T-…-003 追加七行（只增不改）**：契约 §3.3 给 Note v2 的
// `整理正文` / `提取结果` 与 Opinion 的五个分区各登记一行（#44–#50），
// 行数由 43 变 **50**、对象类由 7 变 **8**（新增 ObjectOpinion）。
// 既有 43 行**一格未动**——#13/#16/#17 的 `解释与依据` / `理解自检` 与
// #23/#24/#27 的 v1 Note 分区照旧在册：矩阵是「规则全集」而非「已实现动作清单」，
// 存量文件的写权限仍须可查（分区是否**仍是固定分区**由 mdfile 的模板决定，
// 与矩阵是否登记该行是两件事）。派生计数随之变为
// 严格解锁 **16**（新增行无「P-A 🔴 且 P-U ✅」形态）、
// 条件解锁 **2**（#12 知识内容 + #46 观点，同口径）、两格均 🔴 由 4 变 **5**（新增 #50）。

import (
	"fmt"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// TestWritePermissionMatrix 表驱动逐格断言 50 行 / 100 格与合同 §2 + 契约 §3.3 一致。
func TestWritePermissionMatrix(t *testing.T) {
	a, d, r := VerdictAllow, VerdictDeny, VerdictReport
	// want 逐字抄合同 §2.1–§2.7 的三列（对象 / 字段 / P-A / P-U）。
	want := []struct {
		num   int
		obj   Object
		field string
		auto  Cell
		user  Cell
	}{
		{1, ObjectCard, FieldID, Cell{a}, Cell{d}},
		{2, ObjectCard, FieldTags, Cell{a}, Cell{a}},
		{3, ObjectCard, FieldStatus, Cell{d}, Cell{a}},
		{4, ObjectCard, FieldReplacedBy, Cell{d}, Cell{a}},
		{5, ObjectCard, FieldDeleteMark, Cell{d}, Cell{a}},
		{6, ObjectCard, FieldClearDeleteMark, Cell{d}, Cell{a}},
		{7, ObjectCard, FieldReviewedAt, Cell{d}, Cell{a}},
		{8, ObjectCard, FieldUpdatedAt, Cell{a}, Cell{a}},
		{9, ObjectCard, FieldSources, Cell{a}, Cell{a}},
		{10, ObjectCard, FieldRelationsAdd, Cell{a}, Cell{a}},
		{11, ObjectCard, FieldRelationsRemove, Cell{d}, Cell{a}},
		{12, ObjectCard, SectionField(store.SecKnowledge), Cell{d, a}, Cell{a}},
		{13, ObjectCard, SectionField(store.SecRationale), Cell{a}, Cell{a}},
		{14, ObjectCard, SectionField(store.SecBoundary), Cell{a}, Cell{a}},
		{15, ObjectCard, SectionField(store.SecUserAppend), Cell{d}, Cell{d}},
		{16, ObjectCard, FieldSelfCheckAppend, Cell{a}, Cell{a}},
		{17, ObjectCard, FieldSelfCheckReplace, Cell{a}, Cell{a}},
		{18, ObjectCard, FieldSixthH2, Cell{d}, Cell{a}},
		{19, ObjectNote, FieldID, Cell{a}, Cell{d}},
		{20, ObjectNote, FieldTags, Cell{a}, Cell{a}},
		{21, ObjectNote, FieldDeleteMark, Cell{d}, Cell{a}},
		{22, ObjectNote, FieldReviewedAt, Cell{d}, Cell{a}},
		{23, ObjectNote, SectionField(store.SecDigest), Cell{a}, Cell{a}},
		{24, ObjectNote, SectionField(store.SecAgentReview), Cell{a}, Cell{a}},
		{25, ObjectNote, SectionField(store.SecUserAppend), Cell{d}, Cell{d}},
		{26, ObjectNote, SectionField(store.SecOpenQuest), Cell{a}, Cell{a}},
		{27, ObjectNote, SectionField(store.SecOutputCards), Cell{a}, Cell{a}},
		{28, ObjectSource, FieldSourceIdentity, Cell{a}, Cell{d}},
		{29, ObjectSource, FieldSourceBody, Cell{d}, Cell{d}},
		{30, ObjectSource, FieldSourceReasons, Cell{a}, Cell{a}},
		{31, ObjectSource, FieldDeleteMark, Cell{d}, Cell{a}},
		{32, ObjectReview, FieldReviewWhole, Cell{d}, Cell{a}},
		{33, ObjectReview, FieldReviewStale, Cell{a}, Cell{d}},
		{34, ObjectReview, FieldDeleteMark, Cell{d}, Cell{a}},
		{35, ObjectProposal, FieldProposalNew, Cell{a}, Cell{a}},
		{36, ObjectProposal, FieldProposalApproved, Cell{d}, Cell{a}},
		{37, ObjectProposal, FieldProposalRejected, Cell{d}, Cell{a}},
		{38, ObjectProposal, FieldProposalDecision, Cell{d}, Cell{a}},
		{39, ObjectProposal, FieldProposalExecution, Cell{d}, Cell{d}},
		{40, ObjectProject, FieldConfigYML, Cell{d}, Cell{a}},
		{41, ObjectProject, FieldSkillMD, Cell{d}, Cell{r}},
		{42, ObjectProject, FieldUnprocessed, Cell{a}, Cell{a}},
		{43, ObjectGit, FieldGitHistory, Cell{a}, Cell{a}},
		// —— Schema v2 契约 §3.3 追加的七行 ——
		{44, ObjectNote, SectionField(store.SecNoteBody), Cell{a}, Cell{a}},
		{45, ObjectNote, SectionField(store.SecExtraction), Cell{a}, Cell{a}},
		{46, ObjectOpinion, SectionField(store.SecOpinionClaim), Cell{d, a}, Cell{a}},
		{47, ObjectOpinion, SectionField(store.SecArgument), Cell{a}, Cell{a}},
		{48, ObjectOpinion, SectionField(store.SecCounter), Cell{a}, Cell{a}},
		{49, ObjectOpinion, SectionField(store.SecToVerify), Cell{a}, Cell{a}},
		{50, ObjectOpinion, SectionField(store.SecUserAppend), Cell{d}, Cell{d}},
	}

	got := Matrix()
	if len(got) != 50 {
		t.Fatalf("写权限矩阵必须恰 50 行（合同 §2.8 + 契约 §3.3），实得 %d", len(got))
	}
	if len(want) != len(got) {
		t.Fatalf("用例表 %d 行与矩阵 %d 行不等", len(want), len(got))
	}
	for i, w := range want {
		row := got[i]
		if row.Num != i+1 {
			t.Fatalf("第 %d 行的 Num = %d：行号必须逐字 1..50 连续", i+1, row.Num)
		}
		if row.Num != w.num || row.Object != w.obj || row.Field != w.field {
			t.Fatalf("#%d 的对象/字段与合同 §2 不符：实得 %s · %s", w.num, row.Object, row.Field)
		}
		if row.Auto.String() != w.auto.String() {
			t.Fatalf("#%d 的 P-A 格 = %s，合同 §2 是 %s", w.num, row.Auto, w.auto)
		}
		if row.User.String() != w.user.String() {
			t.Fatalf("#%d 的 P-U 格 = %s，合同 §2 是 %s", w.num, row.User, w.user)
		}
		// 逐格再查一次表：LookupRow 必须能按「对象 + 字段逐字」精确命中同一行。
		found, ok := LookupRow(w.obj, w.field)
		if !ok || found.Num != w.num {
			t.Fatalf("LookupRow(%s, %s) 未命中 #%d", w.obj, w.field, w.num)
		}
	}

	if MatrixCells() != 100 {
		t.Fatalf("判定格总数必须是 50 × 2 = 100，实得 %d", MatrixCells())
	}
	if n := len(MatrixObjects()); n != 8 {
		t.Fatalf("对象类必须恰 8 个（合同 §2.8 的 7 + 契约 §3.3 的 Opinion），实得 %d：%v",
			n, MatrixObjects())
	}
	if n := len(StrictUnlockRows()); n != 16 {
		t.Fatalf("严格解锁行必须恰 16 行，实得 %d：%v", n, rowNums(StrictUnlockRows()))
	}
	// 条件解锁行由 1 变 2：契约 §3.3 明确 Opinion 的 `观点` 沿用 `知识内容` 的口径
	// （创建时可写、已有实体不得由自动路径改写核心主张），因此它是**同一种**形态的第二行，
	// 不是新形态。
	if n := len(ConditionalUnlockRows()); n != 2 {
		t.Fatalf("条件解锁行必须恰 2 行（#12 知识内容 + #46 观点），实得 %d：%v",
			n, rowNums(ConditionalUnlockRows()))
	}
	// **M4 · T-…-055 阶段 1 按实测重钉（只改形态、本体一格不放宽）**：
	// #33（`stale / stale_reason`）的 **P-A** 依 A-34 由 🔴 改 ✅（Agent 自动路径 = 对账
	// 自动写入），**P-U 一格未动**。因此两格均 🔴 的行由 **M3 期 5 − 1 = 4**；
	// 严格解锁行（P-A 🔴 且 P-U ✅）**仍恰 16**：#33 的 P-A 已不含 🔴、P-U 仍是 🔴，
	// 它既不进严格解锁也不进条件解锁 —— 这与主控施工卡预估的「16 → 17」不符，
	// 实测口径以本断言为准（见 audit/impl_T055_stage1.md 的登记）。
	// 2026-09-07 阶段 4a 只把该格改成可复算的**减法等式形态**（数值 4 与本体均未变）。
	// Schema v2 只在末尾加一行两格均 🔴 的 #50（Opinion 的 `用户补充`，安全底线 B2）。
	const m3BothDenied, a34Unlocked, v2BothDenied = 5, 1, 1
	wantBothDenied := m3BothDenied - a34Unlocked + v2BothDenied
	if n := len(BothDeniedRows()); n != wantBothDenied {
		t.Fatalf("两格均 🔴 的行必须恰 %d 行（M3 期 %d − #33 的 P-A 依 A-34 放开 %d "+
			"+ 契约 §3.3 新增 %d），实得 %d：%v",
			wantBothDenied, m3BothDenied, a34Unlocked, v2BothDenied, n, rowNums(BothDeniedRows()))
	}
	// 三组行号也逐字锁死：只锁个数会让「翻两格互相抵消」蒙混过关。
	assertRowNums(t, "严格解锁行", StrictUnlockRows(),
		[]int{3, 4, 5, 6, 7, 11, 18, 21, 22, 31, 32, 34, 36, 37, 38, 40})
	assertRowNums(t, "条件解锁行", ConditionalUnlockRows(), []int{12, 46})
	assertRowNums(t, "两格均 🔴 的行", BothDeniedRows(), []int{15, 25, 29, 39, 50})

	// #33 的两格逐格钉死（A-34 只放开 P-A 一格，P-U 不得顺手放开）：
	row33, ok := RowNum(33)
	if !ok || !row33.Auto.Has(VerdictAllow) || row33.Auto.Has(VerdictDeny) {
		t.Fatalf("#33 的 P-A 必须恰 ✅（M4 · A-34：由对账自动写入），实得 %s", row33.Auto)
	}
	if !row33.User.Has(VerdictDeny) || row33.User.Has(VerdictAllow) {
		t.Fatalf("#33 的 P-U 必须仍是 🔴（本次只净放开 P-A 一格），实得 %s", row33.User)
	}

	// #41 的 P-U 是 🟡 不是 ✅，因此**不计入**严格解锁行（合同 §2.8 的读法）。
	row41, ok := RowNum(41)
	if !ok || !row41.User.Has(VerdictReport) || row41.User.Has(VerdictAllow) {
		t.Fatalf("#41 的 P-U 必须是 🟡（允许但进报告），实得 %s", row41.User)
	}

	// §2.9 锁定条款：五行六格逐格钉死。
	if len(AdjudicationLockedRows()) != 5 {
		t.Fatalf("§2.9 锁定条款覆盖恰 5 行（#35–#39），实得 %v", AdjudicationLockedRows())
	}
	for _, lc := range AdjudicationLockedCells() {
		row, ok := RowNum(lc.Num)
		if !ok {
			t.Fatalf("§2.9 锁定的 #%d 不在矩阵里", lc.Num)
		}
		cell := row.CellFor(lc.Path)
		if !cell.Has(lc.Want) || (lc.Want == VerdictDeny && cell.Has(VerdictAllow)) {
			t.Fatalf("§2.9 锁定格 #%d/%s 必须是 %s，实得 %s（%s）",
				lc.Num, lc.Path, lc.Want, cell, lc.Why)
		}
	}

	// 符号集合闭合：任何一格都只许出现四个已知符号。
	known := map[Verdict]bool{}
	for _, v := range Verdicts() {
		known[v] = true
	}
	if len(known) != 4 {
		t.Fatalf("判定符号恰四值，实得 %d：%v", len(known), Verdicts())
	}
	for _, row := range got {
		for _, path := range Paths() {
			cell := row.CellFor(path)
			if len(cell) == 0 {
				t.Fatalf("#%d/%s 是空格：每格至少一个符号", row.Num, path)
			}
			for _, v := range cell {
				if !known[v] {
					t.Fatalf("#%d/%s 出现未知符号 %q", row.Num, path, v)
				}
			}
		}
	}
}

// rowNums 抽行号（只用于失败信息与集合比对）。
func rowNums(rows []MatrixRow) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Num)
	}
	return out
}

func assertRowNums(t *testing.T, what string, rows []MatrixRow, want []int) {
	t.Helper()
	got := rowNums(rows)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s的行号集合 = %v，合同 §2.8 是 %v", what, got, want)
	}
}
