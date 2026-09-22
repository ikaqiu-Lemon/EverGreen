package store

import (
	"bytes"
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
