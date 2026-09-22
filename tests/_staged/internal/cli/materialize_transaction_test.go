package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

func materializeTxnDate(t *testing.T, raw string) model.Date {
	t.Helper()
	value, err := model.ParseDate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func materializeTxnStamp(t *testing.T, raw string) model.Stamp {
	t.Helper()
	value, err := model.ParseStamp(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func materializeTxnFixture(t *testing.T) (string, string) {
	t.Helper()
	dir, _, _ := initVault(t)
	st := store.New(dir)
	review, err := store.NoteReviewBytes([]store.NoteBlock{
		{Role: store.NoteBlockSource, Body: []byte("知识来源。"), SourceRef: "L1-L1"},
		{Role: store.NoteBlockSource, Body: []byte("观点来源。"), SourceRef: "L2-L2"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	extraction, err := store.CandidateDraftBytes([]store.CandidateDraft{
		{
			Key: "cand-k", Kind: store.CandidateKindKnowledge, Title: "Txn Knowledge",
			SourceRefs: []string{"L1-L1"}, Rel: "support", Reason: "来源定义",
			Sections: []store.CandidateDraftSection{
				{Name: store.SecKnowledge, Body: []byte("事务知识。\n")},
			},
		},
		{
			Key: "cand-o", Kind: store.CandidateKindOpinion, Title: "Txn Opinion",
			SourceRefs: []string{"L2-L2"}, Rel: "context", Reason: "来源判断",
			Sections: []store.CandidateDraftSection{
				{Name: store.SecOpinionClaim, Body: []byte("事务观点。\n")},
			},
		},
	}, []store.CandidateCoverage{
		{Module: "m-k", SourceRefs: []string{"L1-L1"}, Summary: "知识",
			Disposition: store.CandidateCoverageCandidate, Candidates: []string{"cand-k"}},
		{Module: "m-o", SourceRefs: []string{"L2-L2"}, Summary: "观点",
			Disposition: store.CandidateCoverageCandidate, Candidates: []string{"cand-o"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	noteID := "n-20260922-txn-materialize"
	noteRel := store.NoteRel("ai-infra", noteID)
	_, err = st.ApplyNote(store.NoteSpec{
		Rel: noteRel, ID: model.NoteID(noteID), SourceID: "s-20260922-source",
		Title: "Txn materialize", Date: materializeTxnDate(t, "2026-09-22"),
		Stamp: materializeTxnStamp(t, "2026-09-22T09:00:00+08:00"),
		Sections: []store.SectionAppend{
			{Section: store.SecNoteBody, Payload: review},
			{Section: store.SecExtraction, Payload: extraction},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-qm", "fixture")
	return dir, noteRel
}

func materializeTxnRequest(t *testing.T) plan.MaterializeRequest {
	t.Helper()
	return plan.MaterializeRequest{
		Note:  "n-20260922-txn-materialize",
		All:   true,
		Date:  materializeTxnDate(t, "2026-09-22"),
		Stamp: materializeTxnStamp(t, "2026-09-22T10:00:00+08:00"),
	}
}

func materializeTxnInvocation(dir string) *Invocation {
	return &Invocation{
		Cmd: &Command{Name: "materialize"}, VaultRoot: dir, UserRequest: true,
	}
}

func TestMaterializeCriticalUsesOneJournalV1AndOneGitCommit(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return materializeTxnStamp(t, "2026-09-22T10:00:00+08:00").Time()
	}
	req, err := prepareMaterializeRequest(dir, materializeTxnRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	beforeCommits := gitOut(t, dir, "rev-list", "--count", "HEAD")
	steps := watchTxn(t, nil)
	out, err := root.materializeCritical(materializeTxnInvocation(dir), &Result{}, req)
	if err != nil {
		t.Fatal(err)
	}
	if out.TxnID == "" || out.Materialized == nil || len(out.Materialized.WriteSet) != 3 {
		t.Fatalf("物化事务结果不完整：%+v", out)
	}
	if !out.Commit.Created || out.GitErr != nil {
		t.Fatalf("物化应产生恰一个 Git commit：%+v", out)
	}
	beforeCount, _ := strconv.Atoi(strings.TrimSpace(beforeCommits))
	afterCount, _ := strconv.Atoi(strings.TrimSpace(
		gitOut(t, dir, "rev-list", "--count", "HEAD")))
	if afterCount != beforeCount+1 {
		t.Fatalf("物化应恰增加一个 Git commit：%d -> %d", beforeCount, afterCount)
	}
	wantSteps := []string{
		TxnStepAcquired, TxnStepRecovered, TxnStepExecuted, TxnStepAllocated,
		TxnStepIntent, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing,
	}
	if len(steps.names) != len(wantSteps) {
		t.Fatalf("事务节点数量错误：%v", steps.names)
	}
	for i, want := range wantSteps {
		if steps.names[i] != want {
			t.Fatalf("事务节点[%d]=%s，期望 %s；全部=%v", i, steps.names[i], want, steps.names)
		}
	}

	scan, err := txn.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	var intent *txn.Intent
	for _, entry := range scan.Entries {
		if entry.TxnID == out.TxnID {
			intent = entry.Intent
		}
	}
	if intent == nil || intent.JournalVersion != 1 || len(intent.Files) != 3 ||
		!intent.Git.ExpectCommit {
		t.Fatalf("materialize intent 必须是 journal v1 三文件：%+v", intent)
	}
	wantPaths := []string{
		"domains/ai-infra/knowledge/k-20260922-txn-knowledge.md",
		"domains/ai-infra/opinions/o-20260922-txn-opinion.md",
		noteRel,
	}
	for i, want := range wantPaths {
		if intent.Files[i].Path != want {
			t.Fatalf("intent.files[%d]=%s，期望 %s", i, intent.Files[i].Path, want)
		}
		write := out.Materialized.WriteSet[i]
		if intent.Files[i].TargetHash != txn.HashBytes(write.TargetBytes) ||
			intent.Files[i].TargetSize != int64(len(write.TargetBytes)) {
			t.Fatalf("intent.files[%d] target hash/size 未复用 write-set：%+v", i, intent.Files[i])
		}
		if !write.IsNew && (intent.Files[i].PreHash != txn.HashBytes(write.PreBytes) ||
			intent.Files[i].PreBytesRef == "") {
			t.Fatalf("intent.files[%d] 既有文件前像不完整：%+v", i, intent.Files[i])
		}
	}
	if intent.Files[0].Create != true || intent.Files[1].Create != true ||
		intent.Files[2].Create {
		t.Fatalf("intent create 位错误：%+v", intent.Files)
	}

	txnsBefore := txnIDsOn(t, dir)
	commitsBeforeReplay := gitOut(t, dir, "rev-list", "--count", "HEAD")
	replayReq, err := prepareMaterializeRequest(dir, materializeTxnRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := root.materializeCritical(materializeTxnInvocation(dir), &Result{}, replayReq)
	if err != nil {
		t.Fatal(err)
	}
	if replay.TxnID != "" || len(replay.Materialized.WriteSet) != 0 {
		t.Fatalf("no-op 重跑不得创建 txn/write-set：%+v", replay)
	}
	if got := txnIDsOn(t, dir); len(got) != len(txnsBefore) {
		t.Fatalf("no-op 重跑新增了事务：before=%v after=%v", txnsBefore, got)
	}
	if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != commitsBeforeReplay {
		t.Fatal("no-op 重跑制造了空 Git commit")
	}
}

func TestMaterializeCriticalB3RejectsOutsideLockEdit(t *testing.T) {
	t.Run("note changed", func(t *testing.T) {
		dir, noteRel := materializeTxnFixture(t)
		req, err := prepareMaterializeRequest(dir, materializeTxnRequest(t))
		if err != nil {
			t.Fatal(err)
		}
		abs := filepath.Join(dir, filepath.FromSlash(noteRel))
		raw, err := os.ReadFile(abs)
		if err != nil {
			t.Fatal(err)
		}
		edited := bytes.Replace(raw, []byte("事务知识。"), []byte("锁外编辑。"), 1)
		if err := os.WriteFile(abs, edited, 0o644); err != nil {
			t.Fatal(err)
		}
		beforeTxns := txnIDsOn(t, dir)
		_, err = newTestRoot(t, dir).materializeCritical(
			materializeTxnInvocation(dir), &Result{}, req)
		var skip *store.SkipError
		if !errors.As(err, &skip) || skip.Reason != store.SkipFileChanged {
			t.Fatalf("锁外 Note 改动应按 B3 跳过：%T %v", err, err)
		}
		if got := txnIDsOn(t, dir); len(got) != len(beforeTxns) {
			t.Fatalf("B3 跳过不得开事务：before=%v after=%v", beforeTxns, got)
		}
		if got, _ := os.ReadFile(abs); !bytes.Equal(got, edited) {
			t.Fatal("B3 跳过覆盖了锁外编辑")
		}
	})

	t.Run("new target occupied", func(t *testing.T) {
		dir, _ := materializeTxnFixture(t)
		req, err := prepareMaterializeRequest(dir, materializeTxnRequest(t))
		if err != nil {
			t.Fatal(err)
		}
		rel := store.CardRel("ai-infra", "k-20260922-txn-knowledge")
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		external := []byte("external target\n")
		if err := os.WriteFile(abs, external, 0o644); err != nil {
			t.Fatal(err)
		}
		beforeTxns := txnIDsOn(t, dir)
		_, err = newTestRoot(t, dir).materializeCritical(
			materializeTxnInvocation(dir), &Result{}, req)
		if err == nil {
			t.Fatal("锁外新占用目标路径应 fail closed")
		}
		if got := txnIDsOn(t, dir); len(got) != len(beforeTxns) {
			t.Fatalf("目标冲突不得开事务：before=%v after=%v", beforeTxns, got)
		}
		if got, _ := os.ReadFile(abs); !bytes.Equal(got, external) {
			t.Fatal("物化覆盖了锁外创建的目标")
		}
	})
}

func TestMaterializeCriticalRebasesNoteRestoredByRecovery(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	abs := filepath.Join(dir, filepath.FromSlash(noteRel))
	pre, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	target := bytes.Replace(pre, []byte("事务知识。"), []byte("崩溃目标。"), 1)
	id, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn.WriteIntent(dir, id, txn.IntentInput{
		Argv: []string{"eg", "materialize"},
		Files: []txn.FileSpec{{
			Path: noteRel, PreBytes: pre, TargetBytes: target, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, target, 0o644); err != nil {
		t.Fatal(err)
	}
	req, err := prepareMaterializeRequest(dir, materializeTxnRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := newTestRoot(t, dir).materializeCritical(
		materializeTxnInvocation(dir), &Result{}, req)
	if err != nil {
		t.Fatalf("恢复后的自算 Note hash 应重采：%v", err)
	}
	if out.TxnID == "" || len(out.Materialized.WriteSet) != 3 {
		t.Fatalf("恢复后物化未完成：%+v", out)
	}
	if _, err := os.Stat(filepath.Join(txn.TxnDirPath(dir, id), txn.AbortMarker)); err != nil {
		t.Fatalf("旧未闭合事务应被恢复并写 abort：%v", err)
	}
}

func TestMaterializeCriticalGitFailureKeepsCommittedMarkdown(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	root := newTestRoot(t, dir)
	root.NewRepo = failingCommitRepo()
	req, err := prepareMaterializeRequest(dir, materializeTxnRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := root.materializeCritical(materializeTxnInvocation(dir), &Result{}, req)
	if err != nil {
		t.Fatal(err)
	}
	if out.GitErr == nil || out.TxnID == "" {
		t.Fatalf("应只失败在 Git：%+v", out)
	}
	if _, err := os.Stat(filepath.Join(txn.TxnDirPath(dir, out.TxnID), txn.CommitMarker)); err != nil {
		t.Fatalf("Git 失败前 Markdown commit marker 应在盘：%v", err)
	}
	for _, write := range out.Materialized.WriteSet {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(write.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, write.TargetBytes) {
			t.Fatalf("Git 失败后 %s 未保持目标态", write.Path)
		}
	}
	note, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(noteRel)))
	if err != nil || !bytes.Contains(note, []byte("eg:nc:1")) {
		t.Fatalf("Git 失败后 Note 最终覆盖应保持生效：%v", err)
	}
}
