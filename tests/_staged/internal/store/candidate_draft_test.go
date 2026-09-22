package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

func storeCandidate(key string, kind mdfile.CandidateKind, title, section string) CandidateDraft {
	return CandidateDraft{
		Key: key, Kind: kind, Title: title,
		SourceRefs: []string{"L1-L2"}, Rel: "support", Reason: "原文直接支持", Tags: []string{"draft"},
		Sections: []CandidateDraftSection{{Name: section, Body: []byte("候选正文。\n")}},
	}
}

func TestCandidateDraftBytesMapsCandidatesAndCoverage(t *testing.T) {
	drafts := []CandidateDraft{
		storeCandidate("cand-knowledge", mdfile.CandidateKindKnowledge, "知识候选", mdfile.SecKnowledge),
		storeCandidate("cand-opinion", mdfile.CandidateKindOpinion, "观点候选", mdfile.SecOpinionClaim),
	}
	coverage := []CandidateCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L2"}, Summary: "完整模块",
		Disposition: mdfile.CandidateCoverageCandidate,
		Candidates:  []string{"cand-knowledge", "cand-opinion"},
	}}
	body, err := CandidateDraftBytes(drafts, coverage)
	if err != nil {
		t.Fatal(err)
	}
	note := append([]byte("## 提取结果\n\n"), body...)
	candidates, err := mdfile.ParseCandidates(note)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Key != "cand-knowledge" ||
		candidates[1].Key != "cand-opinion" {
		t.Fatalf("candidate 顺序或映射错误：%+v", candidates)
	}
	at := bytes.Index(body, []byte("### 候选覆盖"))
	if at < 0 {
		t.Fatalf("缺候选覆盖矩阵：\n%s", body)
	}
	gotCoverage, err := mdfile.ParseCandidateCoverageMatrix(body[at:])
	if err != nil {
		t.Fatal(err)
	}
	if len(gotCoverage) != 1 || len(gotCoverage[0].Candidates) != 2 {
		t.Fatalf("候选覆盖映射错误：%+v", gotCoverage)
	}
}

func TestCandidateDraftBytesRejectsDuplicateKey(t *testing.T) {
	d := storeCandidate("cand-dup", mdfile.CandidateKindKnowledge, "知识候选", mdfile.SecKnowledge)
	coverage := []CandidateCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L2"}, Summary: "模块",
		Disposition: mdfile.CandidateCoverageCandidate, Candidates: []string{"cand-dup"},
	}}
	if _, err := CandidateDraftBytes([]CandidateDraft{d, d}, coverage); err == nil {
		t.Fatal("重复 candidate key 应拒绝")
	}
}

func TestCandidateDraftBytesRequiresBothLists(t *testing.T) {
	d := storeCandidate("cand-one", mdfile.CandidateKindKnowledge, "知识候选", mdfile.SecKnowledge)
	coverage := []CandidateCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L2"}, Summary: "模块",
		Disposition: mdfile.CandidateCoverageCandidate, Candidates: []string{"cand-one"},
	}}
	if _, err := CandidateDraftBytes(nil, coverage); err == nil {
		t.Fatal("没有 candidate drafts 应拒绝")
	}
	if _, err := CandidateDraftBytes([]CandidateDraft{d}, nil); err == nil {
		t.Fatal("没有 candidate coverage 应拒绝")
	}
}

func TestApplyReplaceCandidateSectionPreservesOtherBytes(t *testing.T) {
	dir := t.TempDir()
	rel := NoteRel("ai-infra", "n-20260922-edit-candidate")
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	draft := storeCandidate(
		"cand-edit", mdfile.CandidateKindKnowledge, "可编辑候选", mdfile.SecKnowledge)
	coverage := []CandidateCoverage{{
		Module: "m-1", SourceRefs: []string{"L1-L2"}, Summary: "模块",
		Disposition: mdfile.CandidateCoverageCandidate, Candidates: []string{"cand-edit"},
	}}
	extraction, err := CandidateDraftBytes([]CandidateDraft{draft}, coverage)
	if err != nil {
		t.Fatal(err)
	}
	raw := append([]byte("---\nid: n-20260922-edit-candidate\nsource: s-20260922-source\n"+
		"created_at: '2026-09-22'\nupdated_at: '2026-09-22T10:00:00+08:00'\n---\n\n"+
		"## 整理正文\n\n原文。\n\n## 提取结果\n\n"), extraction...)
	raw = append(raw, []byte("\n## 存疑与待验证\n\n保留。\n\n## 用户补充\n\n用户内容。\n")...)
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := mdfile.ParseCandidates(raw)
	if err != nil {
		t.Fatal(err)
	}
	target := before[0].Sections[0]
	content := []byte("新候选正文。\n\n第二段。\n")
	res, err := New(dir).ApplyReplaceCandidateSection(ReplaceCandidateSectionSpec{
		Rel: rel, ExpectedHash: ContentHash(raw), ID: "n-20260922-edit-candidate",
		Candidate: "cand-edit", Section: mdfile.SecKnowledge, Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Written {
		t.Fatal("candidate edit 应真实写入")
	}
	after, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	framed := append([]byte("\n"), content...)
	framed = append(framed, '\n')
	if !bytes.Equal(after[:target.BodyStart], raw[:target.BodyStart]) {
		t.Fatal("candidate payload 前字节发生变化（含 frontmatter）")
	}
	if !bytes.Equal(after[target.BodyStart:target.BodyStart+len(framed)], framed) {
		t.Fatalf("candidate payload 未逐字替换：%q", after[target.BodyStart:target.BodyStart+len(framed)])
	}
	if !bytes.Equal(after[target.BodyStart+len(framed):], raw[target.End:]) {
		t.Fatal("candidate payload 后字节发生变化")
	}
}
