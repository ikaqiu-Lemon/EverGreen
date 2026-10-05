package cli

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

type candidateMigrateOutcome struct {
	TxnID         string
	Commit        git.CommitInfo
	NotePath      string
	Workspace     string
	WorkspacePath string
	Changed       bool
	Normalized    bool
	RolledBack    bool
	RollbackErr   error
	Blocked       error
	GitErr        error
}

func (r *Root) runCandidateMigrate(inv *Invocation) (*Result, error) {
	noteText := strings.TrimSpace(inv.String("note"))
	if !inv.UserRequest {
		return nil, &ValidationError{
			Msg: "eg candidate migrate 属用户显式写路径：请加 --user-request",
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: "--" + UserRequestFlag,
				OpIndex: NonOpDiagnostic, Target: noteText,
				Message: "candidate migrate 缺少用户显式发起佐证（--user-request）",
			}},
		}
	}
	noteID, _ := model.ParseNoteID(noteText)
	st := store.New(inv.VaultRoot)
	index, err := st.ScanIDs()
	if err != nil {
		return nil, err
	}
	notePath, err := index.Resolve(noteText)
	if err != nil {
		return nil, candidateReviewValidationError(
			noteText, "candidate migrate 无法定位 Note", err)
	}
	noteFile, err := st.Read(notePath)
	if err != nil {
		return nil, err
	}

	res := &Result{}
	out, err := r.candidateMigrateCritical(
		inv, res, noteID, notePath, noteFile.Hash)
	if err != nil {
		if out != nil && out.Blocked != nil {
			return res, out.Blocked
		}
		return res, candidateReviewValidationError(
			noteText, "candidate migrate 失败", err)
	}
	res.Data = map[string]interface{}{
		"note": noteText, "note_path": out.NotePath,
		"workspace": out.Workspace, "workspace_path": out.WorkspacePath,
		"txn_id": out.TxnID, "commit": candidateApplyCommitData(out.Commit),
	}
	res.DataOrder = []string{
		"note", "note_path", "workspace", "workspace_path", "txn_id", "commit",
	}
	switch {
	case out.RolledBack:
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：n-* 与 ns-* 均未生效（原因：%v）",
			out.TxnID, out.RollbackErr)}
	case out.Blocked != nil:
		return res, out.Blocked
	case out.GitErr != nil:
		return res, &CommitFailedError{
			Msg: "Git 提交失败：candidate migration 已原子生效并保留，未做任何还原（B4）",
			Err: out.GitErr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit",
				OpIndex: NonOpDiagnostic, Message: commitFailureMessage(out.GitErr),
			}},
		}
	case !out.Changed:
		res.Summary = []string{fmt.Sprintf(
			"candidate migrate：Note %s 已是 canonical n/ns，幂等 no-op", noteText)}
	case out.Normalized:
		res.Summary = []string{fmt.Sprintf(
			"candidate migrate：Note %s 已移除机器锚点，块元数据迁入 workspace %s",
			noteText, out.Workspace)}
	default:
		res.Summary = []string{fmt.Sprintf(
			"candidate migrate：Note %s 已原子拆分为纯 n-* 与 workspace %s",
			noteText, out.Workspace)}
	}
	return res, nil
}

func (r *Root) candidateMigrateCritical(
	inv *Invocation,
	res *Result,
	noteID model.NoteID,
	expectedPath, expectedHash string,
) (*candidateMigrateOutcome, error) {
	sess, err := r.enterTxnCritical(inv, resultWarnSink{res}, txnCriticalOpts{
		ZeroWrite:     "本次 candidate migration 零写入",
		ReleaseNotice: "本次 candidate migration 与提交不受影响",
	})
	if err != nil {
		return nil, err
	}
	defer sess.release()

	out := &candidateMigrateOutcome{NotePath: expectedPath}
	st := store.New(inv.VaultRoot)
	index, err := st.ScanIDs()
	if err != nil {
		return out, err
	}
	notePath, err := index.Resolve(string(noteID))
	if err != nil {
		return out, err
	}
	if notePath != expectedPath {
		return out, fmt.Errorf(
			"note_path 已变化：期望 %s，磁盘 %s", expectedPath, notePath)
	}
	noteFile, err := st.Read(notePath)
	if err != nil {
		return out, err
	}
	if noteFile.Hash != expectedHash {
		return out, fmt.Errorf(
			"note_hash 已变化：期望 %s，磁盘 %s", expectedHash, noteFile.Hash)
	}
	existing, hasWorkspace, err := st.NoteSegmentationOf(noteID)
	if err != nil {
		return out, err
	}
	doc, err := mdfile.Parse(noteFile.Bytes)
	if err != nil {
		return out, err
	}
	_, hasExtraction := doc.Section(mdfile.SecExtraction)
	var ws []store.AtomicFileSpec
	var written []string
	commitSubject := "迁移 candidates " + string(noteID)
	commitReason := "用户显式拆分 legacy Note 与 ns-*"
	if hasWorkspace {
		out.Workspace = string(existing.ID)
		out.WorkspacePath = existing.Rel
		if hasExtraction {
			return out, fmt.Errorf(
				"ns-* 已存在但 legacy Note 仍含提取结果，拒绝覆盖任一侧")
		}
		workspaceFile, readErr := st.Read(existing.Rel)
		if readErr != nil {
			return out, readErr
		}
		normalized, normalizeErr := store.NormalizeCandidateWorkspaceBytes(
			noteFile.Bytes, workspaceFile.Bytes, model.NewStamp(r.now()))
		if normalizeErr != nil {
			return out, normalizeErr
		}
		if !normalized.Changed {
			return out, nil
		}
		if err := store.PreserveUserSectionsAfterCut(
			notePath, noteFile.Bytes, normalized.PureNoteBytes); err != nil {
			return out, err
		}
		if err := store.PreserveUserSectionsAfterCut(
			existing.Rel, workspaceFile.Bytes,
			normalized.SegmentationBytes); err != nil {
			return out, err
		}
		ws = []store.AtomicFileSpec{
			{
				Path: notePath, TargetBytes: normalized.PureNoteBytes,
				TargetHash: store.ContentHash(normalized.PureNoteBytes),
				PreBytes:   noteFile.Bytes, PreHash: noteFile.Hash,
			},
			{
				Path: existing.Rel, TargetBytes: normalized.SegmentationBytes,
				TargetHash: store.ContentHash(normalized.SegmentationBytes),
				PreBytes:   workspaceFile.Bytes, PreHash: workspaceFile.Hash,
			},
		}
		written = []string{notePath, existing.Rel}
		commitSubject = "移除 Note 机器锚点 " + string(noteID)
		commitReason = "用户显式规范化 n/ns 块元数据边界"
		out.Normalized = true
	} else {
		if !hasExtraction {
			return out, fmt.Errorf("Note 不含 legacy 提取结果，也没有 ns-* 可迁移")
		}
		migration, migrateErr := store.MigrateLegacyCandidateBytes(noteFile.Bytes)
		if migrateErr != nil {
			return out, migrateErr
		}
		segmentationID, deriveErr := model.NoteSegmentationIDForNote(noteID)
		if deriveErr != nil {
			return out, deriveErr
		}
		segmentationPath := store.NoteSegmentationRel(
			store.DomainOf(notePath), string(segmentationID))
		title := string(noteID)
		if value, ok := migration.Note.Extra["title"].(string); ok && value != "" {
			title = value
		}
		workspaceBytes, renderErr := store.NoteSegmentationBytes(
			store.NoteSegmentationSpec{
				Rel: segmentationPath, ID: segmentationID, Note: noteID,
				NoteHash: store.ContentHash(migration.PureNoteBytes), Title: title,
				Date: migration.Note.CreatedAt, Stamp: model.NewStamp(r.now()),
				Tags: migration.Note.Tags,
				Sections: []store.SectionAppend{{
					Section: store.SecSegmentation,
					Payload: migration.SegmentationBody,
				}},
			})
		if renderErr != nil {
			return out, renderErr
		}
		if err := store.PreserveUserSectionsAfterCut(
			notePath, noteFile.Bytes, migration.PureNoteBytes); err != nil {
			return out, err
		}
		ws = []store.AtomicFileSpec{
			{
				Path: notePath, TargetBytes: migration.PureNoteBytes,
				TargetHash: store.ContentHash(migration.PureNoteBytes),
				PreBytes:   noteFile.Bytes, PreHash: noteFile.Hash,
			},
			{
				Path: segmentationPath, TargetBytes: workspaceBytes,
				TargetHash: store.ContentHash(workspaceBytes), IsNew: true,
			},
		}
		written = []string{notePath, segmentationPath}
		out.Workspace = string(segmentationID)
		out.WorkspacePath = segmentationPath
	}
	fireTxnStep(TxnStepExecuted, "")
	out.Changed = true
	txnID, err := sess.openTxn(
		inv, intentFilesOf(ws), nil, r.Now, func(id string) { out.TxnID = id })
	if err != nil {
		if txnID == "" {
			return out, err
		}
		out.Blocked = err
		return out, nil
	}
	commitResult, commitErr := sess.commitWriteSet(txnID, ws)
	switch {
	case commitErr != nil && commitResult != nil && commitResult.RolledBack:
		out.RolledBack = true
		out.RollbackErr = commitErr
		return out, nil
	case commitErr != nil:
		out.Blocked = blockedError(fmt.Sprintf(
			"事务 %s 的 candidate migration 提交失败且未能收敛为已回滚状态，需人工处置",
			txnID), commitErr)
		return out, nil
	}
	fireTxnStep(TxnStepCommitted, txnID)

	repo := r.repo(inv.VaultRoot)
	noteExistingChangesInResult(res, repo, written)
	info, gitErr := repo.Commit(git.Message{
		Verb: string(model.VerbProcess), Domain: store.DomainOf(notePath),
		Subject: commitSubject,
		Reason:  commitReason,
	})
	res.Warnings = append(res.Warnings, gitWarnings(info)...)
	out.Commit = info
	out.GitErr = gitErr
	fireTxnStep(TxnStepGit, txnID)

	r.syncResultIndex(res, inv, inv.VaultRoot, written)
	fireTxnStep(TxnStepIndexSync, txnID)
	return out, nil
}
