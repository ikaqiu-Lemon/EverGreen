package store

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// LegacyCandidateMigration is the deterministic split of one unmaterialized
// candidate-bearing Note into a pure Note and an ns-* workspace body.
type LegacyCandidateMigration struct {
	Note             model.Note
	PureNoteBytes    []byte
	SegmentationBody []byte
}

// CandidateWorkspaceNormalization moves legacy review anchors out of an
// already split n/ns pair without changing candidate or user-authored bytes.
type CandidateWorkspaceNormalization struct {
	Note              model.Note
	Segmentation      model.NoteSegmentation
	PureNoteBytes     []byte
	SegmentationBytes []byte
	Changed           bool
}

// MigrateLegacyCandidateBytes performs the semantic conversion in memory.
func MigrateLegacyCandidateBytes(raw []byte) (LegacyCandidateMigration, error) {
	note, candidates, state, sourceRefs, err := ParseMaterializationNote(raw)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	if state.Finalized {
		return LegacyCandidateMigration{}, fmt.Errorf(
			"已写最终覆盖的 legacy Note 不自动迁移")
	}
	drafts := make([]CandidateDraft, len(candidates))
	for i, candidate := range candidates {
		if candidate.Anchor.Output != "" {
			return LegacyCandidateMigration{}, fmt.Errorf(
				"candidate %s 已物化为 %s，不自动迁移", candidate.Key, candidate.Anchor.Output)
		}
		drafts[i], err = mdfile.CandidateDraftFromParsed(candidate)
		if err != nil {
			return LegacyCandidateMigration{}, err
		}
	}
	if err := ValidateCandidateReview(drafts, state.Draft, sourceRefs); err != nil {
		return LegacyCandidateMigration{}, err
	}

	doc, _, err := mdfile.ParseNote(raw)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	body, ok := doc.Section(mdfile.SecNoteBody)
	if !ok {
		return LegacyCandidateMigration{}, fmt.Errorf("legacy Note 缺整理正文")
	}
	review, err := mdfile.ParseReviewNote(raw[body.Body:body.End])
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	sourceToBlock := map[string]string{}
	allBlocks := make(map[string]bool, len(review.Blocks))
	for i, block := range review.Blocks {
		ref := fmt.Sprintf("B%d", i+1)
		allBlocks[ref] = true
		if block.Role != mdfile.ReviewRoleSource {
			continue
		}
		if first := sourceToBlock[block.SourceRef]; first != "" {
			return LegacyCandidateMigration{}, fmt.Errorf(
				"legacy source_ref=%s 同时对应 %s/%s，无法无歧义迁移",
				block.SourceRef, first, ref)
		}
		sourceToBlock[block.SourceRef] = ref
	}
	mapRefs := func(refs []string) ([]string, error) {
		out := make([]string, len(refs))
		for i, sourceRef := range refs {
			blockRef := sourceToBlock[sourceRef]
			if blockRef == "" {
				return nil, fmt.Errorf(
					"legacy source_ref=%s 无法匹配唯一 Note source block", sourceRef)
			}
			out[i] = blockRef
		}
		return out, nil
	}
	for i := range drafts {
		drafts[i].NoteRefs, err = mapRefs(drafts[i].SourceRefs)
		if err != nil {
			return LegacyCandidateMigration{}, err
		}
		drafts[i].SourceRefs = nil
	}
	coverage := make([]CandidateCoverage, len(state.Draft))
	covered := map[string]bool{}
	for i, item := range state.Draft {
		coverage[i] = item
		coverage[i].NoteRefs, err = mapRefs(item.SourceRefs)
		if err != nil {
			return LegacyCandidateMigration{}, err
		}
		coverage[i].SourceRefs = nil
		for _, ref := range coverage[i].NoteRefs {
			covered[ref] = true
		}
	}
	for i := range review.Blocks {
		ref := fmt.Sprintf("B%d", i+1)
		if covered[ref] {
			continue
		}
		coverage = append(coverage, CandidateCoverage{
			Module:      "legacy-note-only-" + ref,
			NoteRefs:    []string{ref},
			Summary:     "legacy Note block retained outside candidate coverage",
			Disposition: CandidateCoverageNoteOnly,
			Reason:      "legacy candidate protocol only referenced source blocks",
		})
	}
	if err := ValidateNoteSegmentation(drafts, coverage, allBlocks); err != nil {
		return LegacyCandidateMigration{}, err
	}
	segmentationBody, err := CandidateDraftBytes(drafts, coverage)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	manifest, err := mdfile.RenderNoteBlockManifest(review.Blocks, review.Omissions)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	segmentationBody = append(manifest, segmentationBody...)
	plainBody, err := mdfile.RenderPlainReviewNote(review.Blocks, review.Omissions)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	plainNote, err := doc.ReplaceSectionBody(
		mdfile.SecNoteBody, sectionBodyFraming(plainBody))
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	plainDoc, err := mdfile.Parse(plainNote)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	pure, err := plainDoc.RemoveSection(mdfile.SecExtraction)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	if mdfile.ContainsEvergreenMachineAnchors(pure) {
		return LegacyCandidateMigration{}, fmt.Errorf(
			"迁移后的纯 Note 仍含 Evergreen 机器锚点")
	}
	if _, _, err := mdfile.ParseNote(pure); err != nil {
		return LegacyCandidateMigration{}, fmt.Errorf("迁移后的纯 Note 不成立：%w", err)
	}
	return LegacyCandidateMigration{
		Note: note, PureNoteBytes: pure, SegmentationBody: segmentationBody,
	}, nil
}

// NormalizeCandidateWorkspaceBytes converts an existing anchored n/ns pair to
// the v3 representation. A previously stale workspace remains stale; a fresh
// workspace advances to the hash of the anchorless Note.
func NormalizeCandidateWorkspaceBytes(
	noteRaw, segmentationRaw []byte,
	stamp model.Stamp,
) (CandidateWorkspaceNormalization, error) {
	_, note, err := mdfile.ParseNote(noteRaw)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	_, segmentation, err := mdfile.ParseNoteSegmentation(segmentationRaw)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	if segmentation.Note != note.ID {
		return CandidateWorkspaceNormalization{}, fmt.Errorf(
			"ns-* note=%s 与 Note id=%s 不一致", segmentation.Note, note.ID)
	}
	_, foundManifest, err := mdfile.ParseNoteBlockManifest(segmentationRaw)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	hasReviewAnchors := mdfile.ContainsReviewAnchors(noteRaw)
	switch {
	case !hasReviewAnchors && foundManifest:
		return CandidateWorkspaceNormalization{
			Note: note, Segmentation: segmentation,
			PureNoteBytes:     append([]byte(nil), noteRaw...),
			SegmentationBytes: append([]byte(nil), segmentationRaw...),
		}, nil
	case !hasReviewAnchors:
		return CandidateWorkspaceNormalization{}, fmt.Errorf(
			"anchorless Note 的 ns-* 缺 note block manifest，无法恢复 B 引用")
	case foundManifest:
		return CandidateWorkspaceNormalization{}, fmt.Errorf(
			"Note 仍含 eg:nr 锚点，但 ns-* 已有 note block manifest，拒绝猜测冲突真源")
	}

	noteDoc, _, err := mdfile.ParseNote(noteRaw)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	body, ok := noteDoc.Section(mdfile.SecNoteBody)
	if !ok {
		return CandidateWorkspaceNormalization{}, fmt.Errorf("Note 缺整理正文")
	}
	review, err := mdfile.ParseReviewNote(noteRaw[body.Body:body.End])
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	plainBody, err := mdfile.RenderPlainReviewNote(review.Blocks, review.Omissions)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	pureNote, err := noteDoc.ReplaceSectionBody(
		mdfile.SecNoteBody, sectionBodyFraming(plainBody))
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	if mdfile.ContainsEvergreenMachineAnchors(pureNote) {
		return CandidateWorkspaceNormalization{}, fmt.Errorf(
			"规范化后的 Note 仍含 Evergreen 机器锚点")
	}
	manifest, err := mdfile.RenderNoteBlockManifest(
		review.Blocks, review.Omissions)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	candidates, err := mdfile.ParseCandidates(segmentationRaw)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	if len(candidates) == 0 {
		return CandidateWorkspaceNormalization{}, fmt.Errorf(
			"ns-* 不含 candidate，无法定位 note block manifest 插入点")
	}
	at := candidates[0].AnchorStart
	withManifest := make([]byte, 0, len(segmentationRaw)+len(manifest))
	withManifest = append(withManifest, segmentationRaw[:at]...)
	withManifest = append(withManifest, manifest...)
	withManifest = append(withManifest, segmentationRaw[at:]...)

	nextHash := segmentation.NoteHash
	if segmentation.NoteHash == ContentHash(noteRaw) {
		nextHash = ContentHash(pureNote)
	}
	normalizedSegmentation, err := NoteSegmentationRebasedBytes(
		withManifest, nextHash, stamp)
	if err != nil {
		return CandidateWorkspaceNormalization{}, err
	}
	_, normalizedMeta, _, _, _, err := ParseMaterializationWorkspace(
		pureNote, normalizedSegmentation)
	if err != nil {
		return CandidateWorkspaceNormalization{}, fmt.Errorf(
			"规范化后的 n/ns 对不成立：%w", err)
	}
	return CandidateWorkspaceNormalization{
		Note: note, Segmentation: normalizedMeta,
		PureNoteBytes: pureNote, SegmentationBytes: normalizedSegmentation,
		Changed: true,
	}, nil
}
