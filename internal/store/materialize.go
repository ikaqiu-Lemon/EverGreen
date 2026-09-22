package store

import (
	"bytes"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// CandidateCoverageState is the draft or finalized coverage attached to a
// candidate-bearing Note.
type CandidateCoverageState struct {
	Draft     []CandidateCoverage
	Final     []ExtractionCoverage
	Finalized bool
}

// CandidateNoteMaterializationSpec is the only store write shape for
// candidate materialization. FinalExtraction is nil until the draft coverage
// is eligible to become final extraction coverage.
type CandidateNoteMaterializationSpec struct {
	Rel             string
	ExpectedHash    string
	Outputs         map[string]string
	FinalExtraction []byte
}

// CandidateArtifactSpec contains the facts copied from one Note candidate into
// a new Knowledge or Opinion artifact.
type CandidateArtifactSpec struct {
	Rel       string
	Output    string
	Candidate Candidate
	Source    model.SourceID
	Note      model.NoteID
	Date      model.Date
	Stamp     model.Stamp
}

// ParseMaterializationNote reads all Markdown-owned facts needed by the plan
// materializer through the store package boundary.
func ParseMaterializationNote(
	raw []byte,
) (model.Note, []Candidate, CandidateCoverageState, map[string]bool, error) {
	doc, note, err := mdfile.ParseNote(raw)
	if err != nil {
		return model.Note{}, nil, CandidateCoverageState{}, nil, err
	}
	candidates, err := mdfile.ParseCandidates(raw)
	if err != nil {
		return model.Note{}, nil, CandidateCoverageState{}, nil, err
	}
	state, err := mdfile.ParseCandidateCoverageState(raw)
	if err != nil {
		return model.Note{}, nil, CandidateCoverageState{}, nil, err
	}
	span, ok := doc.Section(mdfile.SecNoteBody)
	if !ok {
		return model.Note{}, nil, CandidateCoverageState{}, nil,
			fmt.Errorf("Note 缺分区「%s」", mdfile.SecNoteBody)
	}
	review, err := mdfile.ParseReviewNote(raw[span.Body:span.End])
	if err != nil {
		return model.Note{}, nil, CandidateCoverageState{}, nil,
			fmt.Errorf("解析整理正文 source_ref：%w", err)
	}
	sourceRefs := make(map[string]bool)
	for _, block := range review.Blocks {
		if block.Role == mdfile.ReviewRoleSource {
			sourceRefs[block.SourceRef] = true
		}
	}
	coverage := CandidateCoverageState{
		Draft:     state.Draft,
		Finalized: state.Finalized,
	}
	coverage.Final = make([]ExtractionCoverage, len(state.Final))
	for i, item := range state.Final {
		coverage.Final[i] = ExtractionCoverage{
			Module: item.Module, SourceRefs: item.SourceRefs, Summary: item.Summary,
			Disposition: item.Disposition, Outputs: item.Outputs, Reason: item.Reason,
		}
	}
	return note, candidates, coverage, sourceRefs, nil
}

// ApplyCandidateNoteMaterialization updates candidate output anchors and,
// when supplied, replaces draft coverage with the canonical final extraction
// block. Identical target bytes are a true no-op.
func (s *Store) ApplyCandidateNoteMaterialization(
	spec CandidateNoteMaterializationSpec,
) (Result, error) {
	res := Result{Path: spec.Rel}
	if spec.Rel == "" {
		return res, ErrNoteRelRequired
	}
	f, err := s.Read(spec.Rel)
	if err != nil {
		return res, err
	}
	res.Hash = f.Hash
	if spec.ExpectedHash != "" && spec.ExpectedHash != f.Hash {
		skip := &SkipError{Path: spec.Rel, Reason: SkipFileChanged,
			Detail: fmt.Sprintf("自读取以来文件已变化：期望 %s，磁盘 %s",
				spec.ExpectedHash, f.Hash)}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if err := mdfile.SelfCheck(f.Bytes); err != nil {
		skip := &SkipError{Path: spec.Rel, Reason: SkipFileChanged,
			Detail: ErrSelfCheckFailed.Error() + "：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	target, err := candidateMaterializedBytes(f.Bytes, spec)
	if err != nil {
		return res, err
	}
	if bytes.Equal(target, f.Bytes) {
		return res, nil
	}
	return s.mutateGuarded(spec.Rel, f.Hash,
		func(_ File, _ *mdfile.Doc) ([]byte, error) {
			return append([]byte(nil), target...), nil
		})
}

func candidateMaterializedBytes(raw []byte,
	spec CandidateNoteMaterializationSpec,
) ([]byte, error) {
	out, err := mdfile.ReplaceCandidateOutputs(raw, spec.Outputs)
	if err != nil {
		return nil, err
	}
	if spec.FinalExtraction != nil {
		out, err = mdfile.FinalizeCandidateExtraction(out, spec.FinalExtraction)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ApplyCandidateArtifact maps a candidate through the existing Knowledge or
// Opinion writer. Candidate payload framing is converted so the resulting H2
// body is byte-identical to the source H4 body.
func (s *Store) ApplyCandidateArtifact(spec CandidateArtifactSpec) (Result, error) {
	sections, err := candidateWriterSections(spec.Candidate)
	if err != nil {
		return Result{Path: spec.Rel}, err
	}
	rel, err := model.ParseMaterialRel(spec.Candidate.Anchor.Rel)
	if err != nil {
		return Result{Path: spec.Rel}, err
	}
	sources := []model.SourceRef{{
		Source: spec.Source,
		Note:   spec.Note,
		Rel:    rel,
		Reason: spec.Candidate.Anchor.Reason,
	}}
	switch spec.Candidate.Kind {
	case CandidateKindKnowledge:
		id, err := model.ParseCardID(spec.Output)
		if err != nil {
			return Result{Path: spec.Rel}, err
		}
		return s.ApplyCard(CardSpec{
			Rel: spec.Rel, ID: id, Title: spec.Candidate.Title,
			Date: spec.Date, Stamp: spec.Stamp, Tags: spec.Candidate.Anchor.Tags,
			Sources: sources, Sections: sections,
		})
	case CandidateKindOpinion:
		id, err := model.ParseOpinionID(spec.Output)
		if err != nil {
			return Result{Path: spec.Rel}, err
		}
		return s.ApplyOpinion(OpinionSpec{
			Rel: spec.Rel, ID: id, Title: spec.Candidate.Title,
			Date: spec.Date, Stamp: spec.Stamp, Tags: spec.Candidate.Anchor.Tags,
			Sources: sources, Sections: sections,
		})
	default:
		return Result{Path: spec.Rel},
			fmt.Errorf("candidate %s kind 越界：%s", spec.Candidate.Key, spec.Candidate.Kind)
	}
}

func candidateWriterSections(candidate Candidate) ([]SectionAppend, error) {
	out := make([]SectionAppend, 0, len(candidate.Sections))
	for _, section := range candidate.Sections {
		payload := section.Payload
		if len(payload) < 3 || payload[0] != '\n' || payload[len(payload)-1] != '\n' ||
			payload[len(payload)-2] != '\n' {
			return nil, fmt.Errorf("candidate %s 的 H4 %q payload framing 非 canonical",
				candidate.Key, section.Name)
		}
		content := append([]byte(nil), payload[1:len(payload)-1]...)
		if len(bytes.TrimSpace(content)) == 0 {
			return nil, fmt.Errorf("candidate %s 的 H4 %q payload 为空",
				candidate.Key, section.Name)
		}
		out = append(out, SectionAppend{Section: section.Name, Payload: content})
	}
	return out, nil
}

// VerifyCandidateArtifact checks the stable mapping facts without requiring a
// byte-for-byte match of user-controlled frontmatter or user supplements.
func (s *Store) VerifyCandidateArtifact(rel string, candidate Candidate) error {
	file, err := s.Read(rel)
	if err != nil {
		return err
	}
	output := candidate.Anchor.Output
	var doc *mdfile.Doc
	var artifactID string
	switch candidate.Kind {
	case CandidateKindKnowledge:
		parsed, card, err := mdfile.ParseCard(file.Bytes)
		if err != nil {
			return fmt.Errorf("Knowledge 目标不可解析：%w", err)
		}
		doc, artifactID = parsed, string(card.ID)
	case CandidateKindOpinion:
		parsed, opinion, err := mdfile.ParseOpinion(file.Bytes)
		if err != nil {
			return fmt.Errorf("Opinion 目标不可解析：%w", err)
		}
		doc, artifactID = parsed, string(opinion.ID)
	default:
		return fmt.Errorf("candidate %s kind 越界：%s", candidate.Key, candidate.Kind)
	}
	if artifactID != output {
		return fmt.Errorf("目标 ID 漂移：%s != %s", artifactID, output)
	}
	var meta struct {
		Title string `yaml:"title"`
	}
	if err := doc.DecodeFM(&meta); err != nil {
		return err
	}
	if meta.Title != candidate.Title {
		return fmt.Errorf("目标 title 漂移：%q != %q", meta.Title, candidate.Title)
	}
	expected := make(map[string][]byte, len(candidate.Sections))
	for _, section := range candidate.Sections {
		expected[section.Name] = section.Payload
	}
	var names []string
	if candidate.Kind == CandidateKindKnowledge {
		names = []string{mdfile.SecKnowledge, mdfile.SecBoundary}
	} else {
		names = []string{
			mdfile.SecOpinionClaim, mdfile.SecArgument,
			mdfile.SecCounter, mdfile.SecToVerify,
		}
	}
	for _, name := range names {
		span, ok := doc.Section(name)
		if !ok {
			return fmt.Errorf("目标缺 H2 %q", name)
		}
		want := expected[name]
		if want == nil {
			want = []byte("\n")
		}
		if !bytes.Equal(file.Bytes[span.Body:span.End], want) {
			return fmt.Errorf("目标 H2 %q payload 漂移", name)
		}
	}
	return nil
}
