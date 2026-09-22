package plan

import (
	"fmt"
	"sort"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// MaterializeRequest selects either one candidate or every candidate in a
// Note. Date and Stamp are injected by the process boundary.
type MaterializeRequest struct {
	Note      model.NoteID
	Candidate string
	All       bool
	Date      model.Date
	Stamp     model.Stamp
}

// MaterializedCandidate records the stable candidate-to-artifact mapping.
type MaterializedCandidate struct {
	Key     string
	Kind    store.CandidateKind
	Output  string
	Path    string
	Created bool
}

// MaterializeResult is the pure preflight result consumed by the transaction
// layer. WriteSet is empty for a successful idempotent replay.
type MaterializeResult struct {
	Note       model.NoteID
	NotePath   string
	Candidates []MaterializedCandidate
	Finalized  bool
	WriteSet   []store.AtomicFileSpec
}

type plannedCandidate struct {
	candidate store.Candidate
	output    string
	path      string
	create    bool
}

// MaterializeCandidates validates a Note and all persistent mappings, then
// builds the complete in-memory write-set. It never writes the vault or calls
// a model.
func MaterializeCandidates(s *store.Store,
	req MaterializeRequest,
) (*MaterializeResult, error) {
	if s == nil {
		return nil, fmt.Errorf("materialize store 为空")
	}
	if !req.Note.Valid() {
		return nil, fmt.Errorf("materialize note ID 非法：%q", req.Note)
	}
	if (req.Candidate != "") == req.All {
		return nil, fmt.Errorf("materialize 必须且只能选择 candidate 或 all")
	}
	if req.Date.IsZero() || req.Stamp.IsZero() {
		return nil, fmt.Errorf("materialize 必须注入非零 date/stamp")
	}

	index, err := s.ScanIDs()
	if err != nil {
		return nil, fmt.Errorf("materialize 扫描 ID：%w", err)
	}
	noteRel, err := index.Resolve(string(req.Note))
	if err != nil {
		return nil, fmt.Errorf("materialize 定位 Note：%w", err)
	}
	domain := store.DomainOf(noteRel)
	if domain == "" {
		return nil, fmt.Errorf("materialize Note 不在领域目录：%s", noteRel)
	}
	noteFile, err := s.Read(noteRel)
	if err != nil {
		return nil, err
	}
	note, candidates, coverageState, sourceRefs, err :=
		store.ParseMaterializationNote(noteFile.Bytes)
	if err != nil {
		return nil, fmt.Errorf("materialize 解析 Note：%w", err)
	}
	if note.ID != req.Note {
		return nil, fmt.Errorf("materialize Note 路径与 frontmatter ID 不一致：%s != %s",
			req.Note, note.ID)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("Note %s 不含 candidate", req.Note)
	}
	byKey := make(map[string]int, len(candidates))
	for i, candidate := range candidates {
		byKey[candidate.Key] = i
	}
	if !req.All {
		if _, ok := byKey[req.Candidate]; !ok {
			return nil, fmt.Errorf("candidate 不存在：%s", req.Candidate)
		}
	}

	if coverageState.Finalized {
		if err := validateFinalCoverage(coverageState.Final, candidates, sourceRefs); err != nil {
			return nil, err
		}
	} else if err := validateDraftCoverage(coverageState.Draft, candidates, sourceRefs); err != nil {
		return nil, err
	}

	seenOutput := make(map[string]string, len(candidates))
	for _, candidate := range candidates {
		if candidate.Anchor.Output == "" {
			continue
		}
		if first := seenOutput[candidate.Anchor.Output]; first != "" {
			return nil, fmt.Errorf("candidate %s 与 %s 重复映射 output %s",
				first, candidate.Key, candidate.Anchor.Output)
		}
		seenOutput[candidate.Anchor.Output] = candidate.Key
	}
	planned := make([]plannedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Anchor.Output != "" {
			path, err := verifyMappedCandidate(s, index, domain, candidate)
			if err != nil {
				return nil, err
			}
			planned = append(planned, plannedCandidate{
				candidate: candidate,
				output:    candidate.Anchor.Output,
				path:      path,
			})
			continue
		}
		if coverageState.Finalized {
			return nil, fmt.Errorf("candidate %s 无 output，但 Note 已写最终覆盖", candidate.Key)
		}
		if !req.All && candidate.Key != req.Candidate {
			planned = append(planned, plannedCandidate{candidate: candidate})
			continue
		}
		output, path := newCandidateTarget(domain, req.Date, candidate)
		if first := seenOutput[output]; first != "" {
			return nil, fmt.Errorf("candidate %s 与 %s 生成相同 output %s",
				first, candidate.Key, output)
		}
		if err := ensureCandidateTargetFree(s, index, output, path); err != nil {
			return nil, fmt.Errorf("candidate %s：%w", candidate.Key, err)
		}
		seenOutput[output] = candidate.Key
		planned = append(planned, plannedCandidate{
			candidate: candidate,
			output:    output,
			path:      path,
			create:    true,
		})
	}

	allClosed := true
	for _, item := range planned {
		if item.output == "" {
			allClosed = false
			break
		}
	}
	eligible := allClosed && coverageEligible(coverageState)
	var finalExtraction []byte
	if coverageState.Finalized {
		finalExtraction, err = materializedExtractionBytes(planned, coverageState.Final)
	} else if eligible {
		finalCoverage, convErr := materializedCoverage(coverageState.Draft, planned)
		if convErr != nil {
			return nil, convErr
		}
		finalExtraction, err = materializedExtractionBytes(planned, finalCoverage)
	}
	if err != nil {
		return nil, fmt.Errorf("materialize 渲染最终提取结果：%w", err)
	}

	if err := s.BeginAtomic(); err != nil {
		return nil, err
	}
	defer s.EndAtomic()

	toCreate := make([]plannedCandidate, 0, len(planned))
	for _, item := range planned {
		if item.create {
			toCreate = append(toCreate, item)
		}
	}
	sort.Slice(toCreate, func(i, j int) bool { return toCreate[i].path < toCreate[j].path })
	for _, item := range toCreate {
		if err := createCandidateTarget(s, note, req, item); err != nil {
			return nil, fmt.Errorf("materialize candidate %s：%w", item.candidate.Key, err)
		}
		if _, err := verifyMappedCandidate(s, indexWithOutput(index, item.output, item.path),
			domain, candidateWithOutput(item.candidate, item.output)); err != nil {
			return nil, err
		}
	}

	outputUpdates := make(map[string]string, len(toCreate))
	for _, item := range toCreate {
		outputUpdates[item.candidate.Key] = item.output
	}
	if len(outputUpdates) > 0 || finalExtraction != nil {
		if _, err := s.ApplyCandidateNoteMaterialization(store.CandidateNoteMaterializationSpec{
			Rel:             noteRel,
			ExpectedHash:    noteFile.Hash,
			Outputs:         outputUpdates,
			FinalExtraction: finalExtraction,
		}); err != nil {
			return nil, fmt.Errorf("materialize 更新 Note：%w", err)
		}
	}

	result := &MaterializeResult{
		Note:      req.Note,
		NotePath:  noteRel,
		Finalized: coverageState.Finalized || eligible,
		WriteSet:  s.AtomicWriteSet(),
	}
	for _, item := range planned {
		if req.All || item.candidate.Key == req.Candidate {
			result.Candidates = append(result.Candidates, MaterializedCandidate{
				Key: item.candidate.Key, Kind: item.candidate.Kind,
				Output: item.output, Path: item.path, Created: item.create,
			})
		}
	}
	return result, nil
}

func validateDraftCoverage(coverage []store.CandidateCoverage,
	candidates []store.Candidate,
	sourceRefs map[string]bool,
) error {
	keys := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		keys[candidate.Key] = true
		for _, ref := range candidate.Anchor.SourceRefs {
			if !sourceRefs[ref] {
				return fmt.Errorf("candidate %s 的 source_ref=%q 不存在于整理正文",
					candidate.Key, ref)
			}
		}
	}
	coveredRefs := make(map[string]bool)
	referenced := make(map[string]bool)
	for _, item := range coverage {
		for _, ref := range item.SourceRefs {
			if !sourceRefs[ref] {
				return fmt.Errorf("candidate coverage %s 的 source_ref=%q 不存在于整理正文",
					item.Module, ref)
			}
			coveredRefs[ref] = true
		}
		if item.Disposition != store.CandidateCoverageCandidate {
			continue
		}
		for _, key := range item.Candidates {
			if !keys[key] {
				return fmt.Errorf("candidate coverage %s 引用不存在的 candidate %s",
					item.Module, key)
			}
			referenced[key] = true
		}
	}
	for ref := range sourceRefs {
		if !coveredRefs[ref] {
			return fmt.Errorf("整理正文 source_ref=%q 未被 candidate coverage 覆盖", ref)
		}
	}
	for key := range keys {
		if !referenced[key] {
			return fmt.Errorf("candidate %s 未被 candidate coverage 引用", key)
		}
	}
	return nil
}

func validateFinalCoverage(coverage []store.ExtractionCoverage,
	candidates []store.Candidate,
	sourceRefs map[string]bool,
) error {
	outputs := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate.Anchor.Output == "" {
			return fmt.Errorf("candidate %s 无 output，但 Note 已写最终覆盖", candidate.Key)
		}
		outputs[candidate.Anchor.Output] = true
	}
	coveredOutputs := make(map[string]bool)
	coveredRefs := make(map[string]bool)
	for _, item := range coverage {
		if item.Disposition == store.CoverageDispMissing {
			return fmt.Errorf("candidate 最终覆盖不得含 missing：%s", item.Module)
		}
		for _, ref := range item.SourceRefs {
			if !sourceRefs[ref] {
				return fmt.Errorf("最终覆盖 %s 的 source_ref=%q 不存在于整理正文",
					item.Module, ref)
			}
			coveredRefs[ref] = true
		}
		for _, output := range item.Outputs {
			if !outputs[output] {
				return fmt.Errorf("最终覆盖 %s 引用非 candidate output %s",
					item.Module, output)
			}
			coveredOutputs[output] = true
		}
	}
	for ref := range sourceRefs {
		if !coveredRefs[ref] {
			return fmt.Errorf("整理正文 source_ref=%q 未被最终覆盖", ref)
		}
	}
	for output := range outputs {
		if !coveredOutputs[output] {
			return fmt.Errorf("candidate output %s 未被最终覆盖引用", output)
		}
	}
	return nil
}

func coverageEligible(state store.CandidateCoverageState) bool {
	if state.Finalized {
		return true
	}
	for _, item := range state.Draft {
		if item.Disposition == store.CandidateCoverageUnresolved {
			return false
		}
	}
	return true
}

func materializedCoverage(coverage []store.CandidateCoverage,
	planned []plannedCandidate,
) ([]store.ExtractionCoverage, error) {
	outputs := make(map[string]string, len(planned))
	for _, item := range planned {
		outputs[item.candidate.Key] = item.output
	}
	out := make([]store.ExtractionCoverage, 0, len(coverage))
	for _, item := range coverage {
		final := store.ExtractionCoverage{
			Module: item.Module, SourceRefs: item.SourceRefs, Summary: item.Summary,
		}
		switch item.Disposition {
		case store.CandidateCoverageCandidate:
			final.Disposition = store.CoverageDispOutputs
			for _, key := range item.Candidates {
				output := outputs[key]
				if output == "" {
					return nil, fmt.Errorf("candidate coverage %s 的 candidate %s 尚未物化",
						item.Module, key)
				}
				final.Outputs = append(final.Outputs, output)
			}
		case store.CandidateCoverageNoteOnly:
			final.Disposition = store.CoverageDispNoteOnly
			final.Reason = item.Reason
		default:
			return nil, fmt.Errorf("candidate coverage %s 尚有 unresolved", item.Module)
		}
		out = append(out, final)
	}
	return out, nil
}

func materializedExtractionBytes(planned []plannedCandidate,
	coverage []store.ExtractionCoverage,
) ([]byte, error) {
	extraction := store.NoteExtraction{}
	for _, item := range planned {
		switch item.candidate.Kind {
		case store.CandidateKindKnowledge:
			extraction.Knowledge = append(extraction.Knowledge, item.output)
		case store.CandidateKindOpinion:
			extraction.Opinions = append(extraction.Opinions,
				item.output+" "+store.ExtractionValidationMark(model.ValidationPending))
		}
	}
	extraction.Coverage = make([]store.ExtractionCoverage, len(coverage))
	for i, item := range coverage {
		extraction.Coverage[i] = store.ExtractionCoverage{
			Module: item.Module, SourceRefs: item.SourceRefs, Summary: item.Summary,
			Disposition: item.Disposition, Outputs: item.Outputs, Reason: item.Reason,
		}
	}
	return extraction.Bytes()
}

func newCandidateTarget(domain string, date model.Date,
	candidate store.Candidate,
) (string, string) {
	if candidate.Kind == store.CandidateKindKnowledge {
		id := string(model.NewCardID(date, candidate.Title))
		return id, store.CardRel(domain, id)
	}
	id := string(model.NewOpinionID(date, candidate.Title))
	return id, store.OpinionRel(domain, id)
}

func ensureCandidateTargetFree(s *store.Store, index store.Index,
	output, path string,
) error {
	for _, duplicate := range index.Duplicates {
		if duplicate.ID == output {
			return fmt.Errorf("output ID 冲突：%s", duplicate)
		}
	}
	if existing, ok := index.ByID[output]; ok {
		return fmt.Errorf("output ID %s 已被 %s 占用", output, existing)
	}
	exists, err := s.Exists(path)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("output 路径已存在：%s", path)
	}
	return nil
}

func candidateWithOutput(candidate store.Candidate, output string) store.Candidate {
	candidate.Anchor.Output = output
	return candidate
}

func indexWithOutput(index store.Index, output, path string) store.Index {
	clone := store.Index{ByID: make(map[string]string, len(index.ByID)+1),
		Duplicates: append([]store.Duplicate(nil), index.Duplicates...)}
	for id, rel := range index.ByID {
		clone.ByID[id] = rel
	}
	clone.ByID[output] = path
	return clone
}

func createCandidateTarget(s *store.Store, note model.Note,
	req MaterializeRequest,
	item plannedCandidate,
) error {
	_, err := s.ApplyCandidateArtifact(store.CandidateArtifactSpec{
		Rel: item.path, Output: item.output, Candidate: item.candidate,
		Source: note.Source, Note: note.ID, Date: req.Date, Stamp: req.Stamp,
	})
	return err
}

func verifyMappedCandidate(s *store.Store, index store.Index, domain string,
	candidate store.Candidate,
) (string, error) {
	output := candidate.Anchor.Output
	var expectedPath string
	switch candidate.Kind {
	case store.CandidateKindKnowledge:
		if _, err := model.ParseCardID(output); err != nil {
			return "", err
		}
		expectedPath = store.CardRel(domain, output)
	case store.CandidateKindOpinion:
		if _, err := model.ParseOpinionID(output); err != nil {
			return "", err
		}
		expectedPath = store.OpinionRel(domain, output)
	default:
		return "", fmt.Errorf("candidate %s kind 越界：%s", candidate.Key, candidate.Kind)
	}
	actualPath, err := index.Resolve(output)
	if err != nil {
		return "", fmt.Errorf("candidate %s 的映射目标缺失：%w", candidate.Key, err)
	}
	if actualPath != expectedPath {
		return "", fmt.Errorf("candidate %s 的 output 路径漂移：%s != %s",
			candidate.Key, actualPath, expectedPath)
	}
	if err := s.VerifyCandidateArtifact(actualPath, candidate); err != nil {
		return "", fmt.Errorf("candidate %s 的映射目标漂移：%w", candidate.Key, err)
	}
	return actualPath, nil
}
