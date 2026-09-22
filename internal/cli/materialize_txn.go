package cli

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// materializeTxnOutcome carries the locked materialization facts to the
// command layer added in the next batch.
type materializeTxnOutcome struct {
	Materialized *plan.MaterializeResult
	TxnID        string
	Commit       git.CommitInfo
	RolledBack   bool
	RollbackErr  error
	Unwritten    []string
	Blocked      error
	GitErr       error
}

// prepareMaterializeRequest captures the Note hash outside the lock. The
// locked path compares it again, preserving the existing B3 contract.
func prepareMaterializeRequest(root string,
	req plan.MaterializeRequest,
) (plan.MaterializeRequest, error) {
	st := store.New(root)
	index, err := st.ScanIDs()
	if err != nil {
		return req, err
	}
	rel, err := index.Resolve(string(req.Note))
	if err != nil {
		return req, err
	}
	file, err := st.Read(rel)
	if err != nil {
		return req, err
	}
	req.ExpectedNotePath = rel
	req.ExpectedNoteHash = file.Hash
	return req, nil
}

// materializeCritical runs one typed materialization through the shared
// run.lock, recovery, journal v1, Git, and index sequence. It does not register
// a CLI command or render the final response.
func (r *Root) materializeCritical(inv *Invocation, res *Result,
	req plan.MaterializeRequest,
) (*materializeTxnOutcome, error) {
	if req.ExpectedNotePath == "" || req.ExpectedNoteHash == "" {
		return nil, fmt.Errorf(
			"materialize 临界区缺锁外 Note path/hash 凭据，拒绝绕过 B3")
	}
	root := inv.VaultRoot
	sess, err := r.enterTxnCritical(inv, resultWarnSink{res}, txnCriticalOpts{
		ZeroWrite: "本次物化零写入", ReleaseNotice: "本次物化与提交不受影响",
	})
	if err != nil {
		return nil, err
	}
	defer sess.release()

	if err := rebaseMaterializeNote(root, sess.restored, &req); err != nil {
		return nil, err
	}
	materialized, err := plan.MaterializeCandidates(store.New(root), req)
	if err != nil {
		return nil, err
	}
	fireTxnStep(TxnStepExecuted, "")

	out := &materializeTxnOutcome{Materialized: materialized}
	if len(materialized.WriteSet) == 0 {
		return out, nil
	}

	txnID, err := sess.openTxn(inv, intentFilesOf(materialized.WriteSet), nil,
		r.Now, func(id string) { out.TxnID = id })
	if err != nil {
		if txnID == "" {
			return nil, err
		}
		out.Blocked = err
		return out, nil
	}

	commitResult, commitErr := sess.commitWriteSet(txnID, materialized.WriteSet)
	switch {
	case commitErr != nil && commitResult != nil && commitResult.RolledBack:
		out.RolledBack = true
		out.RollbackErr = commitErr
		out.Unwritten = writeSetPaths(materialized.WriteSet)
		return out, nil
	case commitErr != nil:
		out.Blocked = blockedError(fmt.Sprintf(
			"事务 %s 的物化提交失败且未能收敛为已回滚状态，需人工处置", txnID), commitErr)
		return out, nil
	}
	fireTxnStep(TxnStepCommitted, txnID)

	written := writeSetPaths(materialized.WriteSet)
	repo := r.repo(root)
	noteExistingChangesInResult(res, repo, written)
	info, gitErr := repo.Commit(git.Message{
		Verb:    string(model.VerbProcess),
		Domain:  store.DomainOf(materialized.NotePath),
		Subject: "物化 " + string(req.Note),
		Reason:  "用户显式物化 Note candidate",
	})
	res.Warnings = append(res.Warnings, gitWarnings(info)...)
	out.Commit = info
	out.GitErr = gitErr
	fireTxnStep(TxnStepGit, txnID)

	r.syncResultIndex(res, inv, root, written)
	fireTxnStep(TxnStepIndexSync, txnID)
	return out, nil
}

func rebaseMaterializeNote(root string, restored []string,
	req *plan.MaterializeRequest,
) error {
	if req == nil || req.ExpectedNoteHash == "" || len(restored) == 0 {
		return nil
	}
	st := store.New(root)
	index, err := st.ScanIDs()
	if err != nil {
		return err
	}
	rel, err := index.Resolve(string(req.Note))
	if err != nil {
		return err
	}
	if !stringIn(restored, rel) {
		return nil
	}
	file, err := st.Read(rel)
	if err != nil {
		return err
	}
	req.ExpectedNoteHash = file.Hash
	return nil
}

func stringIn(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
