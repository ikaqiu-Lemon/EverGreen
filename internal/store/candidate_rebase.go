package store

import (
	"bytes"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// CandidateWorkspaceRebase is the complete atomic target for a stale
// candidate workspace. NoteBytes differs from the input only when an editor
// escaped the opening bracket in canonical Agent markers.
type CandidateWorkspaceRebase struct {
	NoteBytes      []byte
	WorkspaceBytes []byte
	NoteNormalized bool
}

// RebaseCandidateWorkspaceBytes rebuilds the version-local block manifest and
// complete candidate coverage against the current anchorless Note body.
func RebaseCandidateWorkspaceBytes(
	noteRaw, workspaceRaw []byte,
	blocks []NoteBlock,
	omissions []Omission,
	drafts []CandidateDraft,
	coverage []CandidateCoverage,
	stamp model.Stamp,
) ([]byte, error) {
	rebased, err := RebaseCandidateWorkspace(
		noteRaw, workspaceRaw, blocks, omissions, drafts, coverage, stamp)
	if err != nil {
		return nil, err
	}
	if rebased.NoteNormalized {
		return nil, fmt.Errorf(
			"rebase 需要同时规范化 Note；请使用 RebaseCandidateWorkspace 的完整原子目标")
	}
	return rebased.WorkspaceBytes, nil
}

// RebaseCandidateWorkspace rebuilds the workspace and, when necessary,
// canonicalizes SiYuan's semantically equivalent `\[Agent` marker escape in
// the Note in the same transaction. Every other byte must still match the
// submitted note_blocks exactly.
func RebaseCandidateWorkspace(
	noteRaw, workspaceRaw []byte,
	blocks []NoteBlock,
	omissions []Omission,
	drafts []CandidateDraft,
	coverage []CandidateCoverage,
	stamp model.Stamp,
) (CandidateWorkspaceRebase, error) {
	noteDoc, _, err := mdfile.ParseNote(noteRaw)
	if err != nil {
		return CandidateWorkspaceRebase{}, err
	}
	body, ok := noteDoc.Section(mdfile.SecNoteBody)
	if !ok {
		return CandidateWorkspaceRebase{}, fmt.Errorf("Note 缺整理正文")
	}
	plain, err := NotePlainReviewBytes(blocks, omissions)
	if err != nil {
		return CandidateWorkspaceRebase{}, fmt.Errorf("rebase note_blocks 不成立：%w", err)
	}
	wantBody := sectionBodyFraming(plain)
	noteTarget := noteRaw
	noteNormalized := false
	if !bytes.Equal(noteRaw[body.Body:body.End], wantBody) {
		normalized, changed := normalizeEscapedAgentMarkers(
			noteRaw[body.Body:body.End])
		if !changed || !bytes.Equal(normalized, wantBody) {
			return CandidateWorkspaceRebase{}, fmt.Errorf(
				"rebase note_blocks 无法逐字重渲染当前 Note「%s」正文",
				mdfile.SecNoteBody)
		}
		noteTarget, err = noteDoc.ReplaceSectionBody(mdfile.SecNoteBody, wantBody)
		if err != nil {
			return CandidateWorkspaceRebase{}, err
		}
		noteNormalized = true
	}
	refs := make(map[string]bool, len(blocks))
	for i := range blocks {
		refs[fmt.Sprintf("B%d", i+1)] = true
	}
	if err := ValidateNoteSegmentation(drafts, coverage, refs); err != nil {
		return CandidateWorkspaceRebase{}, err
	}
	reviewBlocks, reviewOmissions := noteReviewInputs(blocks, omissions)
	out, err := mdfile.ReplaceNoteBlockManifest(
		workspaceRaw, reviewBlocks, reviewOmissions)
	if err != nil {
		return CandidateWorkspaceRebase{}, err
	}
	out, err = mdfile.ReplaceCandidateDraftState(out, drafts, coverage)
	if err != nil {
		return CandidateWorkspaceRebase{}, err
	}
	out, err = NoteSegmentationRebasedBytes(
		out, ContentHash(noteTarget), stamp)
	if err != nil {
		return CandidateWorkspaceRebase{}, err
	}
	_, segmentation, _, _, _, err := ParseMaterializationWorkspace(noteTarget, out)
	if err != nil {
		return CandidateWorkspaceRebase{}, fmt.Errorf("rebase 后 n/ns 对不成立：%w", err)
	}
	if segmentation.NoteHash != ContentHash(noteTarget) {
		return CandidateWorkspaceRebase{}, fmt.Errorf(
			"rebase 后 note_hash=%s，当前 Note hash=%s",
			segmentation.NoteHash, ContentHash(noteTarget))
	}
	return CandidateWorkspaceRebase{
		NoteBytes: noteTarget, WorkspaceBytes: out,
		NoteNormalized: noteNormalized,
	}, nil
}

func normalizeEscapedAgentMarkers(body []byte) ([]byte, bool) {
	const escaped = `> **\[Agent `
	const canonical = `> **[Agent `
	lines := bytes.Split(body, []byte("\n"))
	out := make([]byte, 0, len(body))
	changed := false
	for i, line := range lines {
		if bytes.HasPrefix(line, []byte(escaped)) {
			out = append(out, canonical...)
			out = append(out, line[len(escaped):]...)
			changed = true
		} else {
			out = append(out, line...)
		}
		if i < len(lines)-1 {
			out = append(out, '\n')
		}
	}
	return out, changed
}
