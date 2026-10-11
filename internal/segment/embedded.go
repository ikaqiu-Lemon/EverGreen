package segment

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 旧 source-line 引用必须精确匹配已有 review block；不猜测或扩大范围。
func mapEmbeddedReview(review mdfile.ReviewNote, candidates []store.Candidate, state store.CandidateCoverageState) ([]store.Candidate, store.CandidateCoverageState, error) {
	refs := map[string]string{}
	for index, block := range review.Blocks {
		if block.Role == mdfile.ReviewRoleSource {
			if refs[block.SourceRef] != "" {
				return nil, state, fmt.Errorf("duplicate legacy source locator %s", block.SourceRef)
			}
			refs[block.SourceRef] = fmt.Sprintf("B%d", index+1)
		}
	}
	mapRefs := func(source []string) ([]string, error) {
		note := []string{}
		for _, ref := range source {
			if refs[ref] == "" {
				return nil, fmt.Errorf("legacy source locator %s has no exact Note block", ref)
			}
			note = append(note, refs[ref])
		}
		return note, nil
	}
	for index := range candidates {
		mapped, err := mapRefs(candidates[index].Anchor.SourceRefs)
		if err != nil {
			return nil, state, err
		}
		candidates[index].Anchor.NoteRefs = mapped
		candidates[index].Anchor.SourceRefs = nil
	}
	for index := range state.Draft {
		mapped, err := mapRefs(state.Draft[index].SourceRefs)
		if err != nil {
			return nil, state, err
		}
		state.Draft[index].NoteRefs = mapped
		state.Draft[index].SourceRefs = nil
	}
	for index := range state.Final {
		mapped, err := mapRefs(state.Final[index].SourceRefs)
		if err != nil {
			return nil, state, err
		}
		state.Final[index].SourceRefs = mapped
	}
	if state.Finalized {
		var err error
		state, err = mapFinalCoverage(candidates, state)
		if err != nil {
			return nil, state, err
		}
	}
	// 历史 Source coverage 可能同范围多模块；新模型每个 Note segment 恰一处。
	byRef := map[string]int{}
	disjoint := []store.CandidateCoverage{}
	for _, module := range state.Draft {
		for _, ref := range module.NoteRefs {
			if index, exists := byRef[ref]; exists {
				current := &disjoint[index]
				if current.Disposition == "unresolved" || module.Disposition == "unresolved" {
					return nil, state, fmt.Errorf("conflicting legacy coverage dispositions for %s", ref)
				}
				// 同一原文范围含可提炼事实及 Note-only 观察时，segment 有 Candidate 关联；
				// 细粒度处置和理由继续保留在 legacy coverage 元数据。
				if module.Disposition == "candidate" {
					current.Disposition = "candidate"
					current.Reason = ""
				} else if current.Disposition == "note_only" && current.Reason != module.Reason {
					current.Reason += " / " + module.Reason
				}
				current.Module += " / " + module.Module
				current.Summary += " / " + module.Summary
				for _, candidate := range module.Candidates {
					present := false
					for _, existing := range current.Candidates {
						present = present || existing == candidate
					}
					if !present {
						current.Candidates = append(current.Candidates, candidate)
					}
				}
				continue
			}
			copy := module
			copy.NoteRefs = []string{ref}
			copy.Candidates = append([]string(nil), module.Candidates...)
			byRef[ref] = len(disjoint)
			disjoint = append(disjoint, copy)
		}
	}
	for index, block := range review.Blocks {
		ref := fmt.Sprintf("B%d", index+1)
		if _, covered := byRef[ref]; covered {
			continue
		}
		if block.Role != mdfile.ReviewRoleAgent {
			return nil, state, fmt.Errorf("legacy source segment %s lacks coverage", ref)
		}
		disjoint = append(disjoint, store.CandidateCoverage{Module: "annotation-" + ref, NoteRefs: []string{ref},
			Summary: "Legacy Agent annotation", Disposition: "note_only", Reason: "Existing annotation remains in the Note."})
	}
	state.Draft = disjoint
	return candidates, state, nil
}

func mapFinalCoverage(candidates []store.Candidate, state store.CandidateCoverageState) (store.CandidateCoverageState, error) {
	outputs := map[string]string{}
	for _, candidate := range candidates {
		if candidate.Anchor.Output != "" {
			outputs[candidate.Anchor.Output] = candidate.Key
		}
	}
	state.Draft = []store.CandidateCoverage{}
	for _, module := range state.Final {
		draft := store.CandidateCoverage{Module: module.Module, NoteRefs: module.SourceRefs,
			Summary: module.Summary, Disposition: module.Disposition, Reason: module.Reason}
		switch module.Disposition {
		case "outputs":
			draft.Disposition = "candidate"
			for _, output := range module.Outputs {
				key, exists := outputs[output]
				if !exists {
					return state, fmt.Errorf("final output %s has no Candidate mapping", output)
				}
				draft.Candidates = append(draft.Candidates, key)
			}
		case "missing":
			draft.Disposition = "unresolved"
		}
		state.Draft = append(state.Draft, draft)
	}
	return state, nil
}
