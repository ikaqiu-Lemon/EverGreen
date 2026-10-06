package store

import (
	"bytes"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

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
	noteDoc, _, err := mdfile.ParseNote(noteRaw)
	if err != nil {
		return nil, err
	}
	body, ok := noteDoc.Section(mdfile.SecNoteBody)
	if !ok {
		return nil, fmt.Errorf("Note 缺整理正文")
	}
	plain, err := NotePlainReviewBytes(blocks, omissions)
	if err != nil {
		return nil, fmt.Errorf("rebase note_blocks 不成立：%w", err)
	}
	if !bytes.Equal(noteRaw[body.Body:body.End], sectionBodyFraming(plain)) {
		return nil, fmt.Errorf(
			"rebase note_blocks 无法逐字重渲染当前 Note「%s」正文",
			mdfile.SecNoteBody)
	}
	refs := make(map[string]bool, len(blocks))
	for i := range blocks {
		refs[fmt.Sprintf("B%d", i+1)] = true
	}
	if err := ValidateNoteSegmentation(drafts, coverage, refs); err != nil {
		return nil, err
	}
	reviewBlocks, reviewOmissions := noteReviewInputs(blocks, omissions)
	out, err := mdfile.ReplaceNoteBlockManifest(
		workspaceRaw, reviewBlocks, reviewOmissions)
	if err != nil {
		return nil, err
	}
	out, err = mdfile.ReplaceCandidateDraftState(out, drafts, coverage)
	if err != nil {
		return nil, err
	}
	out, err = NoteSegmentationRebasedBytes(
		out, ContentHash(noteRaw), stamp)
	if err != nil {
		return nil, err
	}
	_, segmentation, _, _, _, err := ParseMaterializationWorkspace(noteRaw, out)
	if err != nil {
		return nil, fmt.Errorf("rebase 后 n/ns 对不成立：%w", err)
	}
	if segmentation.NoteHash != ContentHash(noteRaw) {
		return nil, fmt.Errorf(
			"rebase 后 note_hash=%s，当前 Note hash=%s",
			segmentation.NoteHash, ContentHash(noteRaw))
	}
	return out, nil
}
