package mdfile

import (
	"bytes"
	"fmt"
)

// CandidateDraftFromParsed projects one authoritative candidate into the
// canonical draft shape used by the review command.
func CandidateDraftFromParsed(candidate Candidate) (CandidateDraft, error) {
	draft := CandidateDraft{
		Key:         candidate.Key,
		Kind:        candidate.Kind,
		LogicalSlug: candidate.LogicalSlug,
		Title:       candidate.Title,
		SourceRefs:  append([]string(nil), candidate.Anchor.SourceRefs...),
		NoteRefs:    append([]string(nil), candidate.Anchor.NoteRefs...),
		Rel:         candidate.Anchor.Rel,
		Reason:      candidate.Anchor.Reason,
		Tags:        append([]string(nil), candidate.Anchor.Tags...),
		Output:      candidate.Anchor.Output,
		Sections:    make([]CandidateDraftSection, len(candidate.Sections)),
	}
	for i, section := range candidate.Sections {
		if len(section.Payload) < 2 || section.Payload[0] != '\n' ||
			section.Payload[len(section.Payload)-1] != '\n' {
			return CandidateDraft{}, fmt.Errorf(
				"candidate %s 的 H4 %q payload framing 非法",
				candidate.Key, section.Name)
		}
		body := section.Payload[1 : len(section.Payload)-1]
		draft.Sections[i] = CandidateDraftSection{
			Name: section.Name,
			Body: append([]byte(nil), body...),
		}
	}
	if err := validateCandidateDraft(draft); err != nil {
		return CandidateDraft{}, fmt.Errorf(
			"candidate %s 无法投影为 review draft：%w", candidate.Key, err)
	}
	return draft, nil
}

// RenderCandidateDraftState emits the complete canonical review-managed
// candidate region: ordered candidates followed by draft coverage.
func RenderCandidateDraftState(
	drafts []CandidateDraft,
	coverage []CandidateCoverage,
) ([]byte, error) {
	if len(drafts) == 0 {
		return nil, fmt.Errorf("candidate review candidates 为空")
	}
	var out []byte
	seen := make(map[string]bool, len(drafts))
	for i, draft := range drafts {
		if seen[draft.Key] {
			return nil, fmt.Errorf(
				"candidate review candidates[%d].key=%q 重复", i, draft.Key)
		}
		seen[draft.Key] = true
		rendered, err := RenderCandidateDraft(draft)
		if err != nil {
			return nil, fmt.Errorf(
				"candidate review candidates[%d] 不成立：%w", i, err)
		}
		out = append(out, rendered...)
	}
	renderedCoverage, err := RenderCandidateCoverageMatrix(coverage)
	if err != nil {
		return nil, err
	}
	out = append(out, renderedCoverage...)
	return out, nil
}

// ReplaceCandidateDraftState replaces only the review-managed region in
// 提取结果. Every byte before the first candidate anchor and after the draft
// coverage block is preserved.
func ReplaceCandidateDraftState(
	raw []byte,
	drafts []CandidateDraft,
	coverage []CandidateCoverage,
) ([]byte, error) {
	candidates, err := ParseCandidates(raw)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("Note 不含 candidate")
	}
	state, err := ParseCandidateCoverageState(raw)
	if err != nil {
		return nil, err
	}
	if state.Finalized {
		return nil, fmt.Errorf("Note 已写最终覆盖，不得应用 review spec")
	}
	replacement, err := RenderCandidateDraftState(drafts, coverage)
	if err != nil {
		return nil, err
	}
	start := candidates[0].AnchorStart
	if start < 0 || state.end < start || state.end > len(raw) {
		return nil, fmt.Errorf("candidate review 管理区间非法：%d:%d", start, state.end)
	}
	if bytes.Equal(raw[start:state.end], replacement) {
		return append([]byte(nil), raw...), nil
	}
	out := make([]byte, 0, len(raw)-(state.end-start)+len(replacement))
	out = append(out, raw[:start]...)
	out = append(out, replacement...)
	out = append(out, raw[state.end:]...)

	got, err := ParseCandidates(out)
	if err != nil {
		return nil, fmt.Errorf("candidate review 替换后解析失败：%w", err)
	}
	if len(got) != len(drafts) {
		return nil, fmt.Errorf(
			"candidate review 替换后数量变化：期望 %d，实际 %d",
			len(drafts), len(got))
	}
	gotState, err := ParseCandidateCoverageState(out)
	if err != nil {
		return nil, fmt.Errorf("candidate review 覆盖替换后解析失败：%w", err)
	}
	if gotState.Finalized {
		return nil, fmt.Errorf("candidate review 替换意外生成最终覆盖")
	}
	return out, nil
}
