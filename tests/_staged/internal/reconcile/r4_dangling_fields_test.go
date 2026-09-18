package reconcile

// I-evergreen.system_assurance-158614-019 的实现侧完整修复反证：`dangling_ref`（E12）
// 覆盖 frontmatter **全部**引用承载字段，而不是历史 M4 合同 §6.2 的「恰两类」。
//
// 本文件钉**知识卡 / 笔记侧的四类**：逐字段各注入一个悬空目标，必须各报**恰一条** E12；
// 四类字段的目标都存在时零 E12；关系条目的 target 仍归 R3（E13/E14），
// 一个字节都不进 E12（一件事一码）。
// 观点侧的另两类（`opinion.sources[].note` / `opinion.sources[].source`）由
// `opinion_reconcile_test.go` 承载，覆盖面总数（DanglingRefKindCount）在下面逐字复算。
//
// 本文件是 T-…-004 批次3 的**先红**证据：实现补齐前，`card.sources[].source` 与
// `replaced_by.target` 两类会红。

import (
	"reflect"
	"sort"
	"strings"
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

// TestR4DanglingRefAllReferenceFields：E12 覆盖知识卡 / 笔记侧四类引用承载字段，逐字段先红后绿。
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
			name:      "replaced_by.target→端点（知识卡或观点） 悬空",
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

// TestR4ReplacedByTargetEndpointUniverse：`replaced_by.target` 的存在性判定面是**论证关系
// 端点宇宙**（知识卡 ∪ 观点），与 R3 关系端点同源。宿主可为知识卡或观点、target 可为知识卡
// 或观点：四组合（k→k / k→o / o→k / o→o）目标存在时一律零 E12；目标形态合法但库内缺失时
// 一律恰 1 条 E12（targets = [宿主, 缺失目标]、code=E12、severity=error、detail 用端点类别措辞）。
//
// **先红点**：旧实现用 `x.Has(target, KindCard)` 判存在性，会把「`replaced_by.target` 指向一条
// 真实存在的观点（`o-*`）」误报成悬空引用（E12）——k→o 与 o→o 两行在补丁前必红；缺失目标
// 各行的 detail 端点措辞在术语统一前也必红。
func TestR4ReplacedByTargetEndpointUniverse(t *testing.T) {
	const (
		kHost = "k-20260918-host"
		oHost = "o-20260918-host"
		kTgt  = "k-20260918-target"
		oTgt  = "o-20260918-target"
		kMiss = "k-20260918-missing"
		oMiss = "o-20260918-missing"
	)
	cp := func(id string) string { return "domains/ai/knowledge/" + id + ".md" }
	op := func(id string) string { return "domains/ai/opinions/" + id + ".md" }

	// 落盘存在的两个可指向端点：一张知识卡 + 一条观点（都不带 replaced_by，纯作存在目标）。
	baseCards := []query.CardEntry{card(kTgt, cp(kTgt))}
	baseOpinions := []query.OpinionEntry{opOpinionAt(oTgt, op(oTgt))}

	build := func(host string, hostIsOpinion bool, target string) Input {
		cards := append([]query.CardEntry{}, baseCards...)
		opinions := append([]query.OpinionEntry{}, baseOpinions...)
		if hostIsOpinion {
			opinions = append(opinions, opWithReplacedBy(opOpinionAt(host, op(host)), target))
		} else {
			cards = append(cards, withReplacedBy(card(host, cp(host)), target))
		}
		return Input{Scan: opScanOf(cards, nil, opinions)}
	}

	existing := []struct {
		name          string
		host          string
		hostIsOpinion bool
		target        string
	}{
		{"k→k 目标存在 → 零 E12", kHost, false, kTgt},
		{"k→o 目标存在 → 零 E12（旧实现在此误报）", kHost, false, oTgt},
		{"o→k 目标存在 → 零 E12", oHost, true, kTgt},
		{"o→o 目标存在 → 零 E12（旧实现在此误报）", oHost, true, oTgt},
	}
	for _, c := range existing {
		t.Run(c.name, func(t *testing.T) {
			if got := pick(findingsOf(t, build(c.host, c.hostIsOpinion, c.target)),
				CheckDanglingRef); len(got) != 0 {
				t.Fatalf("replaced_by.target 指向存在端点不得报 E12：%+v", got)
			}
		})
	}

	missing := []struct {
		name          string
		host          string
		hostIsOpinion bool
		target        string
	}{
		{"k→缺失 k → 恰 1 条 E12", kHost, false, kMiss},
		{"k→缺失 o → 恰 1 条 E12", kHost, false, oMiss},
		{"o→缺失 k → 恰 1 条 E12", oHost, true, kMiss},
		{"o→缺失 o → 恰 1 条 E12", oHost, true, oMiss},
	}
	for _, c := range missing {
		t.Run(c.name, func(t *testing.T) {
			refs := pick(findingsOf(t, build(c.host, c.hostIsOpinion, c.target)), CheckDanglingRef)
			if len(refs) != 1 {
				t.Fatalf("replaced_by.target 指向缺失端点应恰 1 条 E12，实得 %d 条：%+v", len(refs), refs)
			}
			f := refs[0]
			if f.Code() != CodeE12 || f.Severity != SeverityError {
				t.Fatalf("悬空 replaced_by 必须 error 级 + %s，实得 %s / %s", CodeE12, f.Severity, f.Code())
			}
			want := []string{c.host, c.target}
			sort.Strings(want)
			if !reflect.DeepEqual(f.Targets, want) {
				t.Fatalf("targets 期望 %v，实得 %v", want, f.Targets)
			}
			if !strings.Contains(f.Detail, "replaced_by.target→端点（知识卡或观点）") {
				t.Fatalf("detail 应含端点类别措辞（知识卡或观点），实得：%s", f.Detail)
			}
		})
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
