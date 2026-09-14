package plan

// 写权限矩阵的逐格反证（授权合同 §2.1–§2.7 恰 43 行 / §2.8 计数规则 / §2.9 锁定条款）。
//
// 期望值在本文件里是**写死的**（43 / 86 / 16 / 1 / 4 / 7 与逐格符号），这是刻意的：
// 实现侧一律由符号规则派生，测试侧一律逐字抄合同——两边同时改错才可能同时变绿。
//
// **M4 · T-…-055 阶段 1 按实测重钉**：#33 的 P-A 依 A-34 由 🔴 改 ✅（净放开一格），
// 于是「两格均 🔴」由 M3 期的 5 变 **4**；43 / 86 / 16 / 1 / 7 五个数字一格未动。
// 只改判据形态、不放宽本体：新增的 #33 逐格断言把「P-U 不得顺手放开」也钉死了。

import (
	"fmt"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// TestWritePermissionMatrix 表驱动逐格断言 43 行 / 86 格与合同 §2 一致。
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
	}

	got := Matrix()
	if len(got) != 43 {
		t.Fatalf("写权限矩阵必须恰 43 行（合同 §2.8），实得 %d", len(got))
	}
	if len(want) != len(got) {
		t.Fatalf("用例表 %d 行与矩阵 %d 行不等", len(want), len(got))
	}
	for i, w := range want {
		row := got[i]
		if row.Num != i+1 {
			t.Fatalf("第 %d 行的 Num = %d：行号必须逐字 1..43 连续", i+1, row.Num)
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

	if MatrixCells() != 86 {
		t.Fatalf("判定格总数必须是 43 × 2 = 86，实得 %d", MatrixCells())
	}
	if n := len(MatrixObjects()); n != 7 {
		t.Fatalf("对象类必须恰 7 个（合同 §2.8），实得 %d：%v", n, MatrixObjects())
	}
	if n := len(StrictUnlockRows()); n != 16 {
		t.Fatalf("严格解锁行必须恰 16 行，实得 %d：%v", n, rowNums(StrictUnlockRows()))
	}
	if n := len(ConditionalUnlockRows()); n != 1 {
		t.Fatalf("条件解锁行必须恰 1 行（#12），实得 %d：%v", n, rowNums(ConditionalUnlockRows()))
	}
	// **M4 · T-…-055 阶段 1 按实测重钉（只改形态、本体一格不放宽）**：
	// #33（`stale / stale_reason`）的 **P-A** 依 A-34 由 🔴 改 ✅（Agent 自动路径 = 对账
	// 自动写入），**P-U 一格未动**。因此两格均 🔴 的行由 **M3 期 5 − 1 = 4**；
	// 严格解锁行（P-A 🔴 且 P-U ✅）**仍恰 16**：#33 的 P-A 已不含 🔴、P-U 仍是 🔴，
	// 它既不进严格解锁也不进条件解锁 —— 这与主控施工卡预估的「16 → 17」不符，
	// 实测口径以本断言为准（见 audit/impl_T055_stage1.md 的登记）。
	// 2026-09-07 阶段 4a 只把该格改成可复算的**减法等式形态**（数值 4 与本体均未变）。
	const m3BothDenied, a34Unlocked = 5, 1
	if n := len(BothDeniedRows()); n != m3BothDenied-a34Unlocked {
		t.Fatalf("两格均 🔴 的行必须恰 %d 行（M3 期 %d − #33 的 P-A 依 A-34 放开 %d），实得 %d：%v",
			m3BothDenied-a34Unlocked, m3BothDenied, a34Unlocked, n, rowNums(BothDeniedRows()))
	}
	// 三组行号也逐字锁死：只锁个数会让「翻两格互相抵消」蒙混过关。
	assertRowNums(t, "严格解锁行", StrictUnlockRows(),
		[]int{3, 4, 5, 6, 7, 11, 18, 21, 22, 31, 32, 34, 36, 37, 38, 40})
	assertRowNums(t, "条件解锁行", ConditionalUnlockRows(), []int{12})
	assertRowNums(t, "两格均 🔴 的行", BothDeniedRows(), []int{15, 25, 29, 39})

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
