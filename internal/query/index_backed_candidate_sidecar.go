package query

import (
	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

const candidateSidecarReadPath = "context draft_candidates"

// CandidateBlockDocuments projects authoritative Notes into the neutral
// sidecar DTO consumed by internal/index. Candidate parse failures are stored
// as deterministic Q1 facts so a healthy sidecar remains query-equivalent to
// direct scanning.
func CandidateBlockDocuments(notes []NoteEntry, hash Hasher) []index.BlockDocument {
	docs := make([]index.BlockDocument, 0, len(notes))
	for _, note := range notes {
		doc := index.BlockDocument{
			SchemaVersion: index.BlockSidecarVersion,
			NoteID:        note.ID,
			NotePath:      note.Path,
			NoteHash:      hash(note.Raw),
			Candidates:    []index.BlockCandidate{},
			Diagnostics:   []index.BlockDiagnostic{},
		}
		candidates, err := mdfile.ParseCandidates(note.Raw)
		if err != nil {
			diag := newQ1(note.Path,
				"Note candidate 协议不可解析，候选已跳过：%v", err)
			doc.Diagnostics = append(doc.Diagnostics, index.BlockDiagnostic{
				Code: diag.Code, Level: diag.Level, Path: diag.Path, Message: diag.Message,
			})
			docs = append(docs, doc)
			continue
		}
		for _, candidate := range candidates {
			status := "draft"
			if candidate.Anchor.Output != "" {
				status = "materialized"
			}
			doc.Candidates = append(doc.Candidates, index.BlockCandidate{
				Key: candidate.Key, Kind: string(candidate.Kind),
				LogicalSlug: candidate.LogicalSlug,
				Syntax:      string(candidate.Syntax), Title: candidate.Title,
				Status: status, Output: candidate.Anchor.Output,
				PayloadHash: hash(candidate.Raw(note.Raw)),
				Span: index.BlockSpan{
					AnchorStart: candidate.AnchorStart, AnchorEnd: candidate.AnchorEnd,
					BoundaryStart: candidate.BoundaryStart, BoundaryEnd: candidate.BoundaryEnd,
					HeadingStart: candidate.HeadingStart, HeadingEnd: candidate.HeadingEnd,
					ContentEnd: candidate.ContentEnd,
				},
			})
		}
		docs = append(docs, doc)
	}
	return docs
}

// CandidateWorkspaceDocuments projects ns-* when present and falls back to
// legacy candidate-bearing Notes that have not been migrated yet.
func CandidateWorkspaceDocuments(
	notes []NoteEntry,
	workspaces []NoteSegmentationEntry,
	hash Hasher,
) []index.BlockDocument {
	noteByID := make(map[string]NoteEntry, len(notes))
	workspaceByNote := make(map[string]NoteSegmentationEntry, len(workspaces))
	for _, note := range notes {
		noteByID[note.ID] = note
	}
	for _, workspace := range workspaces {
		workspaceByNote[workspace.Note] = workspace
	}
	out := make([]index.BlockDocument, 0, len(notes))
	for _, note := range notes {
		workspace, ok := workspaceByNote[note.ID]
		if !ok {
			out = append(out, CandidateBlockDocuments([]NoteEntry{note}, hash)...)
			continue
		}
		noteHash := hash(note.Raw)
		doc := index.BlockDocument{
			SchemaVersion:     index.BlockSidecarVersion,
			NoteID:            note.ID,
			NotePath:          note.Path,
			NoteHash:          noteHash,
			WorkspaceID:       workspace.ID,
			WorkspacePath:     workspace.Path,
			WorkspaceHash:     hash(workspace.Raw),
			WorkspaceNoteHash: workspace.NoteHash,
			Stale:             workspace.NoteHash != noteHash,
			Candidates:        []index.BlockCandidate{},
			Diagnostics:       []index.BlockDiagnostic{},
		}
		candidates, err := mdfile.ParseCandidates(workspace.Raw)
		if err != nil {
			diag := newQ1(workspace.Path,
				"Note segmentation candidate 协议不可解析，候选已跳过：%v", err)
			doc.Diagnostics = append(doc.Diagnostics, index.BlockDiagnostic{
				Code: diag.Code, Level: diag.Level, Path: diag.Path, Message: diag.Message,
			})
			out = append(out, doc)
			continue
		}
		for _, candidate := range candidates {
			status := "draft"
			if candidate.Anchor.Output != "" {
				status = "materialized"
			}
			doc.Candidates = append(doc.Candidates, index.BlockCandidate{
				Key: candidate.Key, Kind: string(candidate.Kind),
				LogicalSlug: candidate.LogicalSlug,
				Syntax:      string(candidate.Syntax), Title: candidate.Title,
				Status: status, Output: candidate.Anchor.Output,
				PayloadHash: hash(candidate.Raw(workspace.Raw)),
				Span: index.BlockSpan{
					AnchorStart: candidate.AnchorStart, AnchorEnd: candidate.AnchorEnd,
					BoundaryStart: candidate.BoundaryStart, BoundaryEnd: candidate.BoundaryEnd,
					HeadingStart: candidate.HeadingStart, HeadingEnd: candidate.HeadingEnd,
					ContentEnd: candidate.ContentEnd,
				},
			})
		}
		out = append(out, doc)
	}
	return out
}

func projectDraftCandidates(root string, notes []NoteEntry,
	workspaces []NoteSegmentationEntry,
	hash Hasher,
) ([]DraftCandidate, []Diagnostic, []Diagnostic) {
	if len(notes) == 0 {
		return []DraftCandidate{}, []Diagnostic{}, nil
	}
	expected := CandidateWorkspaceDocuments(notes, workspaces, hash)
	hasCandidateFacts := false
	for _, doc := range expected {
		if len(doc.Candidates) > 0 || len(doc.Diagnostics) > 0 {
			hasCandidateFacts = true
			break
		}
	}
	if !hasCandidateFacts {
		return []DraftCandidate{}, []Diagnostic{}, nil
	}
	status := index.CheckBlocks(index.DirPath(root), expected, true)
	docs := expected
	var fallback []Diagnostic
	if status.Healthy() {
		docs = status.Documents
	} else {
		fallback = []Diagnostic{
			{
				Code: status.Code, Level: DiagLevel,
				Path:    index.DirName + "/" + index.BlocksDirName + "/",
				Message: status.Message,
			},
			newQ5(candidateSidecarReadPath, status.Reason),
		}
	}

	var drafts []DraftCandidate
	var parseDiags []Diagnostic
	for _, doc := range docs {
		for _, candidate := range doc.Candidates {
			drafts = append(drafts, DraftCandidate{
				Note: doc.NoteID, Path: doc.NotePath, Key: candidate.Key,
				Workspace: doc.WorkspaceID, WorkspacePath: doc.WorkspacePath,
				Stale: doc.Stale,
				Kind:  candidate.Kind, LogicalSlug: candidate.LogicalSlug,
				Title: candidate.Title, Status: candidate.Status,
				Output: candidate.Output, PayloadHash: candidate.PayloadHash,
			})
		}
		for _, diag := range doc.Diagnostics {
			parseDiags = append(parseDiags, Diagnostic{
				Code: diag.Code, Level: diag.Level, Path: diag.Path, Message: diag.Message,
			})
		}
	}
	if len(drafts) == 0 {
		drafts = []DraftCandidate{}
	}
	if len(parseDiags) == 0 {
		parseDiags = []Diagnostic{}
	}
	return drafts, parseDiags, fallback
}
