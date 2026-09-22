package store

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// Candidate draft types are aliases of the markdown protocol's single source
// of truth. Store only sequences their already-validated byte renderings.
type CandidateDraft = mdfile.CandidateDraft
type CandidateDraftSection = mdfile.CandidateDraftSection
type CandidateCoverage = mdfile.CandidateCoverage

// CandidateDraftBytes renders candidate drafts followed by their separate
// draft coverage matrix. It never emits final output lists or final coverage.
func CandidateDraftBytes(drafts []CandidateDraft, coverage []CandidateCoverage) ([]byte, error) {
	if len(drafts) == 0 {
		return nil, fmt.Errorf("candidate_drafts 为空")
	}
	if len(coverage) == 0 {
		return nil, fmt.Errorf("candidate_coverage 为空")
	}
	seen := make(map[string]bool, len(drafts))
	var out []byte
	for i, draft := range drafts {
		if seen[draft.Key] {
			return nil, fmt.Errorf("candidate_drafts[%d].key=%q 重复", i, draft.Key)
		}
		seen[draft.Key] = true
		body, err := mdfile.RenderCandidateDraft(draft)
		if err != nil {
			return nil, fmt.Errorf("candidate_drafts[%d] 不成立：%w", i, err)
		}
		out = append(out, body...)
	}
	matrix, err := mdfile.RenderCandidateCoverageMatrix(coverage)
	if err != nil {
		return nil, err
	}
	out = append(out, matrix...)
	return out, nil
}
