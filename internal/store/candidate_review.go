package store

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// ValidateCandidateReview validates a complete desired draft state against
// the source_ref vocabulary parsed from the Note's 整理正文 section.
func ValidateCandidateReview(
	drafts []CandidateDraft,
	coverage []CandidateCoverage,
	sourceRefs map[string]bool,
) error {
	if _, err := mdfile.RenderCandidateDraftState(drafts, coverage); err != nil {
		return err
	}
	keys := make(map[string]bool, len(drafts))
	for i, draft := range drafts {
		keys[draft.Key] = true
		for j, ref := range draft.SourceRefs {
			if !sourceRefs[ref] {
				return fmt.Errorf(
					"candidates[%d].source_refs[%d]=%q 不存在于整理正文",
					i, j, ref)
			}
		}
	}

	coveredRefs := make(map[string]bool, len(sourceRefs))
	referenced := make(map[string]bool, len(drafts))
	for i, item := range coverage {
		for j, ref := range item.SourceRefs {
			if !sourceRefs[ref] {
				return fmt.Errorf(
					"coverage[%d].source_refs[%d]=%q 不存在于整理正文",
					i, j, ref)
			}
			coveredRefs[ref] = true
		}
		if item.Disposition != CandidateCoverageCandidate {
			continue
		}
		for j, key := range item.Candidates {
			if !keys[key] {
				return fmt.Errorf(
					"coverage[%d].candidates[%d]=%q 不存在", i, j, key)
			}
			referenced[key] = true
		}
	}
	for ref := range sourceRefs {
		if !coveredRefs[ref] {
			return fmt.Errorf("整理正文 source_ref=%q 未被 coverage 覆盖", ref)
		}
	}
	for key := range keys {
		if !referenced[key] {
			return fmt.Errorf("candidate %s 未被 disposition=candidate 的 coverage 引用", key)
		}
	}
	return nil
}
