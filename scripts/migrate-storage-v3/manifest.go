package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const migrationManifestVersion = 1

var (
	migrationIDPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	candidateKeyPattern = regexp.MustCompile(
		`^cand-[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	contentHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	sourceRefPattern   = regexp.MustCompile(`^L[1-9][0-9]*-L[1-9][0-9]*$`)
)

type Manifest struct {
	SchemaVersion int                 `json:"schema_version"`
	MigrationID   string              `json:"migration_id"`
	Domain        string              `json:"domain"`
	Note          NoteManifest        `json:"note"`
	Candidates    []CandidateManifest `json:"candidates"`
	Coverage      []CoverageManifest  `json:"coverage"`
	Tombstones    []TombstoneManifest `json:"tombstones"`
	Expected      ExpectedCounts      `json:"expected"`
}

type NoteManifest struct {
	ID                 string   `json:"id"`
	Path               string   `json:"path"`
	BeforeHash         string   `json:"before_hash"`
	TemplatePath       string   `json:"template_path"`
	TemplateHash       string   `json:"template_hash"`
	ReviewPlanPath     string   `json:"review_plan_path"`
	ReviewPlanHash     string   `json:"review_plan_hash"`
	ReviewSourceRefs   []string `json:"review_source_refs"`
	PreserveSections   []string `json:"preserve_sections"`
	ExpectedSourceBody string   `json:"expected_source_body_hash"`
}

type CandidateManifest struct {
	Key        string   `json:"key"`
	Output     string   `json:"output"`
	Path       string   `json:"path"`
	BeforeHash string   `json:"before_hash,omitempty"`
	SeedPath   string   `json:"seed_path"`
	SeedHash   string   `json:"seed_hash"`
	SourceRefs []string `json:"source_refs"`
}

type CoverageManifest struct {
	Module      string   `json:"module"`
	SourceRefs  []string `json:"source_refs"`
	Summary     string   `json:"summary"`
	Disposition string   `json:"disposition"`
	Outputs     []string `json:"outputs"`
	Reason      string   `json:"reason"`
}

type TombstoneManifest struct {
	Path        string `json:"path"`
	BeforeHash  string `json:"before_hash"`
	Replacement string `json:"replacement"`
	Reason      string `json:"reason"`
	DeletedAt   string `json:"deleted_at"`
	UpdatedAt   string `json:"updated_at"`
}

type ExpectedCounts struct {
	Notes             int `json:"notes"`
	Knowledge         int `json:"knowledge"`
	Opinions          int `json:"opinions"`
	PendingOpinions   int `json:"pending_opinions"`
	MaterialRelations int `json:"material_relations"`
	ArgumentRelations int `json:"argument_relations"`
	CoverageModules   int `json:"coverage_modules"`
	NoteOnlyModules   int `json:"note_only_modules"`
	Tombstones        int `json:"tombstones"`
}

func loadManifest(filename string) (Manifest, []byte, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return Manifest{}, nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var manifest Manifest
	if err := dec.Decode(&manifest); err != nil {
		return Manifest{}, nil, fmt.Errorf("manifest JSON: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return Manifest{}, nil, fmt.Errorf("manifest JSON must contain exactly one object")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, raw, nil
}

func validateManifest(m Manifest) error {
	if m.SchemaVersion != migrationManifestVersion {
		return fmt.Errorf("manifest schema_version=%d, want %d",
			m.SchemaVersion, migrationManifestVersion)
	}
	if !migrationIDPattern.MatchString(m.MigrationID) {
		return fmt.Errorf("invalid migration_id %q", m.MigrationID)
	}
	if strings.TrimSpace(m.Domain) == "" || strings.ContainsAny(m.Domain, `/\`) {
		return fmt.Errorf("invalid domain %q", m.Domain)
	}
	if _, err := model.ParseNoteID(m.Note.ID); err != nil {
		return fmt.Errorf("note.id: %w", err)
	}
	if m.Note.Path != store.NoteRel(m.Domain, m.Note.ID) {
		return fmt.Errorf("note.path=%q, want %q",
			m.Note.Path, store.NoteRel(m.Domain, m.Note.ID))
	}
	for name, value := range map[string]string{
		"note.before_hash":      m.Note.BeforeHash,
		"note.template_hash":    m.Note.TemplateHash,
		"note.review_plan_hash": m.Note.ReviewPlanHash,
	} {
		if !contentHashPattern.MatchString(value) {
			return fmt.Errorf("%s is not a content hash", name)
		}
	}
	for _, p := range []string{
		m.Note.Path, m.Note.TemplatePath, m.Note.ReviewPlanPath,
	} {
		if err := validateRelPath(p); err != nil {
			return err
		}
	}
	if len(m.Note.ReviewSourceRefs) == 0 {
		return fmt.Errorf("note.review_source_refs is empty")
	}
	reviewRefs := make(map[string]bool, len(m.Note.ReviewSourceRefs))
	previousEnd := 0
	for i, ref := range m.Note.ReviewSourceRefs {
		if !sourceRefPattern.MatchString(ref) {
			return fmt.Errorf("note.review_source_refs[%d]=%q is invalid", i, ref)
		}
		var start, end int
		if _, err := fmt.Sscanf(ref, "L%d-L%d", &start, &end); err != nil ||
			start > end || start <= previousEnd {
			return fmt.Errorf("note.review_source_refs[%d]=%q is not ordered and disjoint", i, ref)
		}
		if reviewRefs[ref] {
			return fmt.Errorf("duplicate note.review_source_ref %q", ref)
		}
		reviewRefs[ref] = true
		previousEnd = end
	}
	wantPreserve := map[string]bool{
		store.SecOpenQuest:  true,
		store.SecUserAppend: true,
	}
	if len(m.Note.PreserveSections) != len(wantPreserve) {
		return fmt.Errorf("note.preserve_sections must contain exactly %q and %q",
			store.SecOpenQuest, store.SecUserAppend)
	}
	for _, section := range m.Note.PreserveSections {
		if !wantPreserve[section] {
			return fmt.Errorf("note.preserve_sections contains unsupported section %q", section)
		}
		delete(wantPreserve, section)
	}
	if !contentHashPattern.MatchString(m.Note.ExpectedSourceBody) {
		return fmt.Errorf("note.expected_source_body_hash is not a content hash")
	}

	seenKeys := map[string]bool{}
	seenOutputs := map[string]bool{}
	seenTargets := map[string]bool{m.Note.Path: true}
	for i, candidate := range m.Candidates {
		if !candidateKeyPattern.MatchString(candidate.Key) {
			return fmt.Errorf("candidates[%d].key=%q is invalid", i, candidate.Key)
		}
		if seenKeys[candidate.Key] {
			return fmt.Errorf("duplicate candidate key %q", candidate.Key)
		}
		seenKeys[candidate.Key] = true
		endpoint, err := model.ParseRelationEndpoint(candidate.Output)
		if err != nil {
			return fmt.Errorf("candidates[%d].output: %w", i, err)
		}
		if seenOutputs[string(endpoint)] {
			return fmt.Errorf("duplicate candidate output %q", endpoint)
		}
		seenOutputs[string(endpoint)] = true
		wantPath := store.CardRel(m.Domain, candidate.Output)
		if strings.HasPrefix(candidate.Output, model.PrefixOpinion) {
			wantPath = store.OpinionRel(m.Domain, candidate.Output)
		}
		if candidate.Path != wantPath {
			return fmt.Errorf("candidates[%d].path=%q, want %q", i, candidate.Path, wantPath)
		}
		for _, p := range []string{candidate.Path, candidate.SeedPath} {
			if err := validateRelPath(p); err != nil {
				return err
			}
		}
		if seenTargets[candidate.Path] {
			return fmt.Errorf("duplicate target path %q", candidate.Path)
		}
		seenTargets[candidate.Path] = true
		if !contentHashPattern.MatchString(candidate.SeedHash) {
			return fmt.Errorf("candidates[%d].seed_hash is not a content hash", i)
		}
		if candidate.BeforeHash != "" && !contentHashPattern.MatchString(candidate.BeforeHash) {
			return fmt.Errorf("candidates[%d].before_hash is not a content hash", i)
		}
		if len(candidate.SourceRefs) == 0 {
			return fmt.Errorf("candidates[%d].source_refs is empty", i)
		}
		candidateRefs := map[string]bool{}
		for j, ref := range candidate.SourceRefs {
			if !sourceRefPattern.MatchString(ref) || !reviewRefs[ref] {
				return fmt.Errorf(
					"candidates[%d].source_refs[%d]=%q is not a review source ref", i, j, ref)
			}
			if candidateRefs[ref] {
				return fmt.Errorf("candidates[%d] repeats source_ref %q", i, ref)
			}
			candidateRefs[ref] = true
		}
	}
	if len(m.Candidates) == 0 {
		return fmt.Errorf("candidates is empty")
	}

	seenModules := map[string]bool{}
	for i, item := range m.Coverage {
		if strings.TrimSpace(item.Module) == "" || seenModules[item.Module] {
			return fmt.Errorf("coverage[%d].module is empty or duplicate", i)
		}
		seenModules[item.Module] = true
		if len(item.SourceRefs) == 0 || strings.TrimSpace(item.Summary) == "" {
			return fmt.Errorf("coverage[%d] requires source_refs and summary", i)
		}
		for j, ref := range item.SourceRefs {
			if !sourceRefPattern.MatchString(ref) || !reviewRefs[ref] {
				return fmt.Errorf(
					"coverage[%d].source_refs[%d]=%q is not a review source ref", i, j, ref)
			}
		}
		switch item.Disposition {
		case store.CoverageDispOutputs:
			if len(item.Outputs) == 0 || item.Reason != "" {
				return fmt.Errorf("coverage[%d] outputs disposition shape is invalid", i)
			}
			for _, output := range item.Outputs {
				if !seenOutputs[output] {
					return fmt.Errorf("coverage[%d] references undeclared output %q", i, output)
				}
			}
		case store.CoverageDispNoteOnly:
			if len(item.Outputs) != 0 || strings.TrimSpace(item.Reason) == "" {
				return fmt.Errorf("coverage[%d] note_only disposition shape is invalid", i)
			}
		default:
			return fmt.Errorf("coverage[%d].disposition=%q; missing/unresolved is forbidden",
				i, item.Disposition)
		}
	}
	if len(m.Coverage) == 0 {
		return fmt.Errorf("coverage is empty")
	}

	for i, tombstone := range m.Tombstones {
		if err := validateRelPath(tombstone.Path); err != nil {
			return err
		}
		if seenTargets[tombstone.Path] {
			return fmt.Errorf("duplicate target path %q", tombstone.Path)
		}
		seenTargets[tombstone.Path] = true
		if !contentHashPattern.MatchString(tombstone.BeforeHash) {
			return fmt.Errorf("tombstones[%d].before_hash is not a content hash", i)
		}
		if _, err := model.ParseRelationEndpoint(tombstone.Replacement); err != nil {
			return fmt.Errorf("tombstones[%d].replacement: %w", i, err)
		}
		if !seenOutputs[tombstone.Replacement] {
			return fmt.Errorf("tombstones[%d].replacement=%q is not a candidate output",
				i, tombstone.Replacement)
		}
		wantPrefix := path.Join(store.DirDomains, m.Domain, store.DirKnowledge) + "/"
		if !strings.HasPrefix(tombstone.Path, wantPrefix) {
			return fmt.Errorf("tombstones[%d].path must be in %s", i, wantPrefix)
		}
		if strings.TrimSpace(tombstone.Reason) == "" {
			return fmt.Errorf("tombstones[%d].reason is empty", i)
		}
		if _, err := model.ParseStamp(tombstone.DeletedAt); err != nil {
			return fmt.Errorf("tombstones[%d].deleted_at: %w", i, err)
		}
		if _, err := model.ParseStamp(tombstone.UpdatedAt); err != nil {
			return fmt.Errorf("tombstones[%d].updated_at: %w", i, err)
		}
	}
	if m.Expected.Notes != 1 {
		return fmt.Errorf("expected.notes must be 1")
	}
	return nil
}

func validateRelPath(rel string) error {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, `\`) ||
		path.Clean(rel) != rel || rel == "." || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("path must be a canonical Vault-relative slash path: %q", rel)
	}
	return nil
}
