package reconcile

// I-evergreen.system_assurance-158614-019 的实现侧完整修复反证：`dangling_ref`（E12）
// 覆盖 frontmatter **全部**引用承载字段（恰四类），而不是历史 M4 合同 §6.2 的「恰两类」。
//
// 逐字段各注入一个悬空目标，必须各报**恰一条** E12；四类字段的目标都存在时零 E12；
// 关系条目的 target 仍归 R3（E13/E14），一个字节都不进 E12（一件事一码）。
//
// 本文件是 T-…-004 批次3 的**先红**证据：实现补齐前，`card.sources[].source` 与
// `replaced_by.target` 两类会红。

import (
	"reflect"
	"sort"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// withOneSourceRef 给卡追加**一条** sources[] 条目（显式 source + note，rel=support）。
func withOneSourceRef(c query.CardEntry, source, noteID string) query.CardEntry {
	c.Sources = append(c.Sources, model.SourceRef{
		Source: model.SourceID(source), Note: model.NoteID(noteID),
		Rel: model.MaterialSupport, Reason: "e2e 事实",
	})
	return c
}

// withReplacedBy 给卡设置 replaced_by.target（失效卡的替代指针）。
func withReplacedBy(c query.CardEntry, target string) query.CardEntry {
	c.ReplacedByTarget = target
	return c
}

// TestR4DanglingRefAllReferenceFields：E12 覆盖四类引用承载字段，逐字段先红后绿。
func TestR4DanglingRefAllReferenceFields(t *testing.T) {
	existingSources := []SourceFact{{ID: "s-ok", Path: "sources/s-ok.md"}}
	okNote := note("n-ok", "domains/ai/notes/n-ok.md", "s-ok")
	okCard := card("k-ok", "domains/ai/knowledge/k-ok.md")

	cases := []struct {
		name             string
		underTest        query.CardEntry
		extraNote        query.NoteEntry
		wantFrom, wantTo string
	}{
		{
			name:      "note.source→原文 悬空",
			underTest: okCard,
			extraNote: note("n-bad", "domains/ai/notes/n-bad.md", "s-gone"),
			wantFrom:  "n-bad", wantTo: "s-gone",
		},
		{
			name:      "card.sources[].note→材料笔记 悬空",
			underTest: withOneSourceRef(card("k-a", "domains/ai/knowledge/k-a.md"), "s-ok", "n-gone"),
			extraNote: okNote,
			wantFrom:  "k-a", wantTo: "n-gone",
		},
		{
			name:      "card.sources[].source→原文 悬空",
			underTest: withOneSourceRef(card("k-a", "domains/ai/knowledge/k-a.md"), "s-gone", "n-ok"),
			extraNote: okNote,
			wantFrom:  "k-a", wantTo: "s-gone",
		},
		{
			name:      "replaced_by.target→知识卡 悬空",
			underTest: withReplacedBy(card("k-a", "domains/ai/knowledge/k-a.md"), "k-gone"),
			extraNote: okNote,
			wantFrom:  "k-a", wantTo: "k-gone",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{
				Scan: scanOf([]query.CardEntry{c.underTest, okCard},
					[]query.NoteEntry{c.extraNote, okNote}),
				Sources: existingSources,
			}
			refs := pick(findingsOf(t, in), CheckDanglingRef)
			if len(refs) != 1 {
				t.Fatalf("字段 %q 悬空应恰 1 条 E12，实得 %d 条：%+v", c.name, len(refs), refs)
			}
			f := refs[0]
			if f.Code() != CodeE12 || f.Severity != SeverityError {
				t.Fatalf("悬空引用必须 error 级 + %s，实得 %s / %s", CodeE12, f.Severity, f.Code())
			}
			want := []string{c.wantFrom, c.wantTo}
			sort.Strings(want)
			if !reflect.DeepEqual(f.Targets, want) {
				t.Fatalf("targets 期望 %v，实得 %v", want, f.Targets)
			}
		})
	}

	// 四类字段的目标都存在 → 零 E12。
	clean := Input{
		Scan: scanOf([]query.CardEntry{
			withReplacedBy(withOneSourceRef(
				card("k-a", "domains/ai/knowledge/k-a.md"), "s-ok", "n-ok"), "k-ok"),
			okCard},
			[]query.NoteEntry{okNote}),
		Sources: existingSources,
	}
	if got := pick(findingsOf(t, clean), CheckDanglingRef); len(got) != 0 {
		t.Fatalf("四类引用目标都存在时不得报 E12：%+v", got)
	}

	// 覆盖面基数冻结：多一类 / 少一类都要改这里，防止边界被静默改动。
	// 本文件钉的是知识卡 / 笔记侧的四类；schema v2 的观点又带来两类
	// （`opinion.sources[]` 的两端），逐字段反证由 opinion_reconcile_test.go 承载。
	if DanglingRefKindCount != 6 {
		t.Fatalf("dangling_ref 覆盖面应恰 6 类（卡 / 笔记侧 4 类 + 观点侧 2 类），"+
			"DanglingRefKindCount = %d", DanglingRefKindCount)
	}
}

// TestR4DanglingRefSourceUnsampledStaysSilent：`sources/` 分区未采样时，
// 指向原文的两类字段（note.source / card.sources[].source）绝不误报「不存在」。
func TestR4DanglingRefSourceUnsampledStaysSilent(t *testing.T) {
	in := Input{
		Scan: scanOf(
			[]query.CardEntry{withOneSourceRef(
				card("k-a", "domains/ai/knowledge/k-a.md"), "s-gone", "n-ok")},
			[]query.NoteEntry{
				note("n-ok", "domains/ai/notes/n-ok.md", "s-also-gone")}),
		// Sources 为 nil = 未采样。
	}
	if got := pick(findingsOf(t, in), CheckDanglingRef); len(got) != 0 {
		t.Fatalf("原文分区未采样时，指向原文的字段不得判悬空：%+v", got)
	}
}
