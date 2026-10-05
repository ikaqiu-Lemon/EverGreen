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
	pure, err := doc.RemoveSection(mdfile.SecExtraction)
	if err != nil {
		return LegacyCandidateMigration{}, err
	}
	if _, _, err := mdfile.ParseNote(pure); err != nil {
		return LegacyCandidateMigration{}, fmt.Errorf("迁移后的纯 Note 不成立：%w", err)
	}
	return LegacyCandidateMigration{
		Note: note, PureNoteBytes: pure, SegmentationBody: segmentationBody,
	}, nil
}
