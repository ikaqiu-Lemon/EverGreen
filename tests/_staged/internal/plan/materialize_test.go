package plan

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

type materializeFixture struct {
	root    string
	noteRel string
	noteRaw []byte
}

func materializeDate(t *testing.T, raw string) model.Date {
	t.Helper()
	value, err := model.ParseDate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func materializeStamp(t *testing.T, raw string) model.Stamp {
	t.Helper()
	value, err := model.ParseStamp(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newMaterializeFixture(t *testing.T, unresolved bool) materializeFixture {
	t.Helper()
	root := t.TempDir()
	s := store.New(root)
	date := materializeDate(t, "2026-09-20")
	stamp := materializeStamp(t, "2026-09-20T10:00:00+08:00")
	review, err := store.NoteReviewBytes([]store.NoteBlock{
		{Role: store.NoteBlockSource, Heading: "定义", Body: []byte("第一段来源。"),
			SourceRef: "L1-L1"},
		{Role: store.NoteBlockAgent, Body: []byte("保持这条批注。"),
			Annotation: "supplement"},
		{Role: store.NoteBlockSource, Heading: "判断", Body: []byte("第二段来源。"),
			SourceRef: "L2-L2"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	drafts := []store.CandidateDraft{
		{
			Key: "cand-knowledge", Kind: mdfile.CandidateKindKnowledge,
			Title: "Knowledge Candidate", SourceRefs: []string{"L1-L1"},
			Rel: "support", Reason: "来源给出定义", Tags: []string{"core", "go"},
			Sections: []store.CandidateDraftSection{
				{Name: mdfile.SecKnowledge, Body: []byte("知识第一段。\n\n- 细节\n")},
				{Name: mdfile.SecBoundary, Body: []byte("仅适用于测试边界。\n")},
			},
		},
		{
			Key: "cand-opinion", Kind: mdfile.CandidateKindOpinion,
			Title: "Opinion Candidate", SourceRefs: []string{"L2-L2"},
			Rel: "context", Reason: "来源提供判断背景", Tags: []string{"claim"},
			Sections: []store.CandidateDraftSection{
				{Name: mdfile.SecOpinionClaim, Body: []byte("这是可反驳主张。\n")},
				{Name: mdfile.SecArgument, Body: []byte("论据 A。\n")},
				{Name: mdfile.SecCounter, Body: []byte("反例 B。\n")},
				{Name: mdfile.SecToVerify, Body: []byte("仍需实验。\n")},
			},
		},
	}
	coverage := []store.CandidateCoverage{
		{Module: "m-1", SourceRefs: []string{"L1-L1"}, Summary: "知识模块",
			Disposition: mdfile.CandidateCoverageCandidate, Candidates: []string{"cand-knowledge"}},
		{Module: "m-2", SourceRefs: []string{"L2-L2"}, Summary: "观点模块",
			Disposition: mdfile.CandidateCoverageCandidate, Candidates: []string{"cand-opinion"}},
	}
	if unresolved {
		coverage[1] = store.CandidateCoverage{
			Module: "m-2", SourceRefs: []string{"L2-L2"}, Summary: "未闭合模块",
			Disposition: mdfile.CandidateCoverageUnresolved, Reason: "反例尚未确认",
		}
		drafts = drafts[:1]
	}
	extraction, err := store.CandidateDraftBytes(drafts, coverage)
	if err != nil {
		t.Fatal(err)
	}
	noteID := model.NoteID("n-20260920-materialize")
	noteRel := store.NoteRel("ai-infra", string(noteID))
	_, err = s.ApplyNote(store.NoteSpec{
		Rel: noteRel, ID: noteID, SourceID: "s-20260920-source",
		Title: "Materialize Source", Date: date, Stamp: stamp,
		Sections: []store.SectionAppend{
			{Section: mdfile.SecNoteBody, Payload: review},
			{Section: mdfile.SecExtraction, Payload: extraction},
			{Section: mdfile.SecOpenQuest, Payload: []byte("保留这个问题。\n")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(root, filepath.FromSlash(noteRel))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("## 用户补充\n\n"),
		[]byte("## 用户补充\n\n保留用户补充。\n"), 1)
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return materializeFixture{root: root, noteRel: noteRel, noteRaw: raw}
}

func materializeRequest(t *testing.T, date string) MaterializeRequest {
	t.Helper()
	return MaterializeRequest{
		Note:  "n-20260920-materialize",
		All:   true,
		Date:  materializeDate(t, date),
		Stamp: materializeStamp(t, date+"T12:00:00+08:00"),
	}
}

func applyMaterializeWriteSet(t *testing.T, root string, writes []store.AtomicFileSpec) {
	t.Helper()
	for _, write := range writes {
		abs := filepath.Join(root, filepath.FromSlash(write.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, write.TargetBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func materializeWrite(t *testing.T, writes []store.AtomicFileSpec, rel string) []byte {
	t.Helper()
	for _, write := range writes {
		if write.Path == rel {
			return write.TargetBytes
		}
	}
	t.Fatalf("write-set 缺路径 %s：%+v", rel, writes)
	return nil
}

func noteSectionBytes(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	doc, err := mdfile.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	span, ok := doc.Section(name)
	if !ok {
		t.Fatalf("缺分区 %s", name)
	}
	return raw[span.Body:span.End]
}

func TestMaterializeCandidatesCreatesExactTargetsAndFinalizes(t *testing.T) {
	fx := newMaterializeFixture(t, false)
	beforeCandidates, err := mdfile.ParseCandidates(fx.noteRaw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := MaterializeCandidates(store.New(fx.root),
		materializeRequest(t, "2026-09-22"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Finalized || len(result.Candidates) != 2 || len(result.WriteSet) != 3 {
		t.Fatalf("首次 all 物化结果不完整：%+v", result)
	}
	wantOrder := []string{
		"domains/ai-infra/knowledge/k-20260922-knowledge-candidate.md",
		"domains/ai-infra/opinions/o-20260922-opinion-candidate.md",
		fx.noteRel,
	}
	for i, want := range wantOrder {
		if result.WriteSet[i].Path != want {
			t.Fatalf("write-set[%d]=%s，期望 %s", i, result.WriteSet[i].Path, want)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.root, "domains/ai-infra/knowledge",
		"k-20260922-knowledge-candidate.md")); !os.IsNotExist(err) {
		t.Fatal("纯物化内核不得直接写实盘")
	}

	for i, candidate := range beforeCandidates {
		target := materializeWrite(t, result.WriteSet, wantOrder[i])
		var doc *mdfile.Doc
		if candidate.Kind == mdfile.CandidateKindKnowledge {
			doc, _, err = mdfile.ParseCard(target)
		} else {
			var opinion model.Opinion
			doc, opinion, err = mdfile.ParseOpinion(target)
			if opinion.Validation != model.ValidationPending {
				t.Fatalf("新 Opinion validation=%s，期望 pending", opinion.Validation)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, section := range candidate.Sections {
			span, ok := doc.Section(section.Name)
			if !ok {
				t.Fatalf("目标缺 H2 %s", section.Name)
			}
			if !bytes.Equal(target[span.Body:span.End], section.Payload) {
				t.Fatalf("%s payload 未逐字映射：\nsource=%q\ntarget=%q",
					section.Name, section.Payload, target[span.Body:span.End])
			}
		}
	}

	noteAfter := materializeWrite(t, result.WriteSet, fx.noteRel)
	for _, section := range []string{
		mdfile.SecNoteBody, mdfile.SecOpenQuest, mdfile.SecUserAppend,
	} {
		if !bytes.Equal(noteSectionBytes(t, fx.noteRaw, section),
			noteSectionBytes(t, noteAfter, section)) {
			t.Fatalf("物化改写了 Note 分区 %s", section)
		}
	}
	afterCandidates, err := mdfile.ParseCandidates(noteAfter)
	if err != nil {
		t.Fatal(err)
	}
	for i := range beforeCandidates {
		if !bytes.Equal(beforeCandidates[i].Raw(fx.noteRaw),
			afterCandidates[i].Raw(noteAfter)) {
			t.Fatalf("candidate %s 的 H3/payload 被改写", beforeCandidates[i].Key)
		}
	}
	state, err := mdfile.ParseCandidateCoverageState(noteAfter)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Finalized || bytes.Contains(noteAfter, []byte("eg:cc:1")) ||
		!bytes.Contains(noteAfter, []byte("eg:nc:1")) ||
		!bytes.Contains(noteAfter, []byte("o-20260922-opinion-candidate `[pending]`")) {
		t.Fatalf("最终 output list/coverage 不成立：\n%s", noteAfter)
	}

	applyMaterializeWriteSet(t, fx.root, result.WriteSet)
	for _, rerunDate := range []string{"2026-09-22", "2026-09-23"} {
		replay, err := MaterializeCandidates(store.New(fx.root),
			materializeRequest(t, rerunDate))
		if err != nil {
			t.Fatalf("%s 重跑失败：%v", rerunDate, err)
		}
		if len(replay.WriteSet) != 0 {
			t.Fatalf("%s 重跑必须零 write-set：%+v", rerunDate, replay.WriteSet)
		}
		for _, item := range replay.Candidates {
			if item.Created {
				t.Fatalf("%s 重跑不得创建第二文件：%+v", rerunDate, item)
			}
		}
	}
}

func TestMaterializeCandidatesKeepsDraftCoverageWhileUnresolved(t *testing.T) {
	fx := newMaterializeFixture(t, true)
	result, err := MaterializeCandidates(store.New(fx.root),
		materializeRequest(t, "2026-09-22"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Finalized || len(result.WriteSet) != 2 {
		t.Fatalf("有 unresolved 时只应写目标与 Note anchor：%+v", result)
	}
	noteAfter := materializeWrite(t, result.WriteSet, fx.noteRel)
	state, err := mdfile.ParseCandidateCoverageState(noteAfter)
	if err != nil {
		t.Fatal(err)
	}
	if state.Finalized || len(state.Draft) != 2 ||
		!bytes.Contains(noteAfter, []byte("eg:cc:1")) ||
		bytes.Contains(noteAfter, []byte("eg:nc:1")) ||
		bytes.Contains(noteAfter, []byte("missing")) {
		t.Fatalf("unresolved 不得伪造成最终覆盖：\n%s", noteAfter)
	}
}

func TestMaterializeCandidatesPersistsMappingAcrossPartialDays(t *testing.T) {
	fx := newMaterializeFixture(t, false)
	firstReq := materializeRequest(t, "2026-09-22")
	firstReq.All, firstReq.Candidate = false, "cand-knowledge"
	first, err := MaterializeCandidates(store.New(fx.root), firstReq)
	if err != nil {
		t.Fatal(err)
	}
	if first.Finalized || len(first.WriteSet) != 2 ||
		first.Candidates[0].Output != "k-20260922-knowledge-candidate" {
		t.Fatalf("首日单候选物化结果错误：%+v", first)
	}
	applyMaterializeWriteSet(t, fx.root, first.WriteSet)

	secondReq := materializeRequest(t, "2026-09-23")
	secondReq.All, secondReq.Candidate = false, "cand-opinion"
	second, err := MaterializeCandidates(store.New(fx.root), secondReq)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Finalized || len(second.WriteSet) != 2 ||
		second.Candidates[0].Output != "o-20260923-opinion-candidate" {
		t.Fatalf("次日剩余候选物化结果错误：%+v", second)
	}
	noteAfter := materializeWrite(t, second.WriteSet, fx.noteRel)
	candidates, err := mdfile.ParseCandidates(noteAfter)
	if err != nil {
		t.Fatal(err)
	}
	if candidates[0].Anchor.Output != "k-20260922-knowledge-candidate" ||
		candidates[1].Anchor.Output != "o-20260923-opinion-candidate" {
		t.Fatalf("跨日映射未持久保持：%+v", candidates)
	}
}

func TestMaterializeCandidatesFailsClosedOnCollisionMissingAndDrift(t *testing.T) {
	t.Run("id collision", func(t *testing.T) {
		fx := newMaterializeFixture(t, false)
		s := store.New(fx.root)
		id := model.CardID("k-20260922-knowledge-candidate")
		_, err := s.ApplyCard(store.CardSpec{
			Rel: store.CardRel("ai-infra", string(id)), ID: id, Title: "Occupied",
			Date:  materializeDate(t, "2026-09-22"),
			Stamp: materializeStamp(t, "2026-09-22T11:00:00+08:00"),
			Sources: []model.SourceRef{{
				Source: "s-20260920-source", Note: "n-20260920-materialize",
				Rel: model.MaterialSupport, Reason: "占位",
			}},
			Sections: []store.SectionAppend{{
				Section: mdfile.SecKnowledge, Payload: []byte("占位。\n"),
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := materializeRequest(t, "2026-09-22")
		req.All, req.Candidate = false, "cand-knowledge"
		if _, err := MaterializeCandidates(s, req); err == nil ||
			!strings.Contains(err.Error(), "占用") {
			t.Fatalf("ID 冲突应 fail closed：%v", err)
		}
		if got, _ := os.ReadFile(filepath.Join(fx.root, filepath.FromSlash(fx.noteRel))); !bytes.Equal(got, fx.noteRaw) {
			t.Fatal("ID 冲突不得修改 Note")
		}
	})

	t.Run("mapped target missing", func(t *testing.T) {
		fx := newMaterializeFixture(t, false)
		out, err := mdfile.ReplaceCandidateOutputs(fx.noteRaw,
			map[string]string{"cand-knowledge": "k-20260922-knowledge-candidate"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fx.root, filepath.FromSlash(fx.noteRel)),
			out, 0o644); err != nil {
			t.Fatal(err)
		}
		req := materializeRequest(t, "2026-09-23")
		req.All, req.Candidate = false, "cand-knowledge"
		if _, err := MaterializeCandidates(store.New(fx.root), req); err == nil ||
			!strings.Contains(err.Error(), "映射目标缺失") {
			t.Fatalf("缺失映射目标应 fail closed：%v", err)
		}
	})

	t.Run("mapped payload drift", func(t *testing.T) {
		fx := newMaterializeFixture(t, false)
		first, err := MaterializeCandidates(store.New(fx.root),
			materializeRequest(t, "2026-09-22"))
		if err != nil {
			t.Fatal(err)
		}
		applyMaterializeWriteSet(t, fx.root, first.WriteSet)
		rel := "domains/ai-infra/knowledge/k-20260922-knowledge-candidate.md"
		abs := filepath.Join(fx.root, filepath.FromSlash(rel))
		raw, err := os.ReadFile(abs)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.Replace(raw, []byte("知识第一段。"), []byte("漂移内容。"), 1)
		if err := os.WriteFile(abs, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := MaterializeCandidates(store.New(fx.root),
			materializeRequest(t, "2026-09-23")); err == nil ||
			!strings.Contains(err.Error(), "payload 漂移") {
			t.Fatalf("映射内容漂移应 fail closed：%v", err)
		}
	})
}

func TestMaterializeCandidatesRejectsDuplicateOutput(t *testing.T) {
	fx := newMaterializeFixture(t, false)
	raw, err := mdfile.ReplaceCandidateOutputs(fx.noteRaw, map[string]string{
		"cand-knowledge": "k-20260922-duplicate",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(".opinion}"), []byte(".knowledge}"), 1)
	raw = bytes.Replace(raw, []byte("#### 观点"), []byte("#### 知识内容"), 1)
	raw = bytes.Replace(raw, []byte("#### 论据与推理\n\n论据 A。\n\n"), nil, 1)
	raw = bytes.Replace(raw, []byte("#### 条件与反例\n\n反例 B。\n\n"), nil, 1)
	raw = bytes.Replace(raw, []byte("#### 待验证\n\n仍需实验。\n\n"), nil, 1)
	raw, err = mdfile.ReplaceCandidateOutputs(raw, map[string]string{
		"cand-opinion": "k-20260922-duplicate",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.root, filepath.FromSlash(fx.noteRel)),
		raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeCandidates(store.New(fx.root),
		materializeRequest(t, "2026-09-23")); err == nil ||
		!strings.Contains(err.Error(), "重复映射 output") {
		t.Fatalf("重复 output 应 fail closed：%v", err)
	}
}
