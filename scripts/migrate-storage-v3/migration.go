package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

type Options struct {
	VaultRoot    string
	ManifestPath string
	Apply        bool
}

type Report struct {
	SchemaVersion int                `json:"schema_version"`
	MigrationID   string             `json:"migration_id"`
	Mode          string             `json:"mode"`
	ManifestHash  string             `json:"manifest_hash"`
	Status        string             `json:"status"`
	TxnID         string             `json:"txn_id"`
	RecoveredTxn  string             `json:"recovered_txn_id"`
	FilesChanged  int                `json:"files_changed"`
	Files         []FileReport       `json:"files"`
	Mappings      []CandidateMapping `json:"candidate_output_mappings"`
	Counts        ExpectedCounts     `json:"counts"`
}

type FileReport struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	BeforeHash string `json:"before_hash"`
	AfterHash  string `json:"after_hash"`
	BeforeSize int    `json:"before_size"`
	AfterSize  int    `json:"after_size"`
	Diff       string `json:"diff"`
}

type CandidateMapping struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind"`
	Output     string   `json:"output"`
	Path       string   `json:"path"`
	SourceRefs []string `json:"source_refs"`
}

type RunError struct {
	Report *Report
	Err    error
}

func (e *RunError) Error() string { return e.Err.Error() }
func (e *RunError) Unwrap() error { return e.Err }

type plannedFile struct {
	path   string
	before []byte
	target []byte
	exists bool
}

type migrationPlan struct {
	files    []plannedFile
	mappings []CandidateMapping
	counts   ExpectedCounts
}

func Run(opts Options) (*Report, error) {
	root, err := filepath.Abs(opts.VaultRoot)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault is not a directory")
	}
	manifest, rawManifest, err := loadManifest(opts.ManifestPath)
	if err != nil {
		return nil, err
	}
	mode := "dry-run"
	if opts.Apply {
		mode = "apply"
	}
	report := &Report{
		SchemaVersion: migrationManifestVersion,
		MigrationID:   manifest.MigrationID,
		Mode:          mode,
		ManifestHash:  store.ContentHash(rawManifest),
		Status:        "planned",
		TxnID:         "",
		RecoveredTxn:  "",
		Files:         []FileReport{},
		Mappings:      []CandidateMapping{},
	}

	if !opts.Apply {
		scan, err := txn.Scan(root)
		if err != nil {
			report.Status = "failed"
			return nil, &RunError{Report: report, Err: err}
		}
		if err := scan.Blocked(); err != nil {
			report.Status = "failed"
			return nil, &RunError{Report: report, Err: err}
		}
		if open := scan.Open(); len(open) != 0 {
			report.Status = "failed"
			return nil, &RunError{Report: report, Err: fmt.Errorf(
				"dry-run refuses an unrecovered transaction: %s", open[0].TxnID)}
		}
		built, err := buildMigrationPlan(root, manifest)
		if err != nil {
			report.Status = "failed"
			return nil, &RunError{Report: report, Err: err}
		}
		fillReport(report, built)
		report.Status = "dry-run"
		return report, nil
	}

	lock, err := txn.Acquire(root, txn.LockOptions{
		Argv: []string{"migrate-storage-v3", "--apply"},
	})
	if err != nil {
		report.Status = "failed"
		return nil, &RunError{Report: report, Err: err}
	}
	defer lock.Release()

	recovered, err := txn.Recover(root)
	if err != nil {
		report.Status = "failed"
		return nil, &RunError{Report: report, Err: err}
	}
	report.RecoveredTxn = recovered.TxnID

	built, err := buildMigrationPlan(root, manifest)
	if err != nil {
		report.Status = "failed"
		return nil, &RunError{Report: report, Err: err}
	}
	fillReport(report, built)
	changes := changedFiles(built.files)
	if len(changes) == 0 {
		report.Status = "noop"
		return report, nil
	}

	txnID, err := txn.AllocateTxnID(root)
	if err != nil {
		report.Status = "failed"
		return nil, &RunError{Report: report, Err: err}
	}
	report.TxnID = txnID
	_ = lock.RecordTxnID(txnID)

	intentFiles := make([]txn.FileSpec, 0, len(changes))
	commitFiles := make([]txn.CommitFile, 0, len(changes))
	for _, file := range changes {
		op := "update"
		if !file.exists {
			op = "create"
		}
		intentFiles = append(intentFiles, txn.FileSpec{
			Path: file.path, Create: !file.exists, PreBytes: file.before,
			TargetBytes: file.target, TargetOp: "storage_v3_" + op,
		})
		commitFiles = append(commitFiles, txn.CommitFile{
			Path: file.path, TargetBytes: file.target,
		})
	}
	if _, err := txn.WriteIntent(root, txnID, txn.IntentInput{
		Argv:  []string{"migrate-storage-v3", "--apply"},
		Files: intentFiles, ExpectCommit: false,
	}); err != nil {
		report.Status = "failed"
		return nil, &RunError{Report: report, Err: err}
	}
	result, err := txn.Commit(root, txnID, txn.CommitInput{Files: commitFiles})
	if err != nil {
		report.Status = "failed"
		return nil, &RunError{Report: report, Err: err}
	}
	if result == nil || !result.Committed {
		report.Status = "failed"
		return nil, &RunError{Report: report,
			Err: fmt.Errorf("transaction %s did not reach committed state", txnID)}
	}
	for _, file := range changes {
		raw, err := readVaultFile(root, file.path)
		if err != nil {
			report.Status = "failed"
			return nil, &RunError{Report: report, Err: err}
		}
		if !bytes.Equal(raw, file.target) {
			report.Status = "failed"
			return nil, &RunError{Report: report,
				Err: fmt.Errorf("post-commit bytes drifted for %s", file.path)}
		}
	}
	report.Status = "applied"
	return report, nil
}

func fillReport(report *Report, built *migrationPlan) {
	report.Mappings = append(report.Mappings, built.mappings...)
	report.Counts = built.counts
	report.FilesChanged = 0
	report.Files = make([]FileReport, 0, len(built.files))
	for _, file := range built.files {
		action := "noop"
		if !bytes.Equal(file.before, file.target) {
			report.FilesChanged++
			if file.exists {
				action = "update"
			} else {
				action = "create"
			}
		}
		report.Files = append(report.Files, FileReport{
			Path: file.path, Action: action,
			BeforeHash: contentHashOrEmpty(file.exists, file.before),
			AfterHash:  store.ContentHash(file.target),
			BeforeSize: len(file.before), AfterSize: len(file.target),
			Diff: unifiedDiff(file.path, file.before, file.target),
		})
	}
}

func buildMigrationPlan(root string, manifest Manifest) (*migrationPlan, error) {
	currentNote, err := readVaultFile(root, manifest.Note.Path)
	if err != nil {
		return nil, fmt.Errorf("read current note: %w", err)
	}
	template, err := readInput(root, manifest.Note.TemplatePath, manifest.Note.TemplateHash)
	if err != nil {
		return nil, fmt.Errorf("read note template: %w", err)
	}
	reviewPlan, err := readInput(root, manifest.Note.ReviewPlanPath, manifest.Note.ReviewPlanHash)
	if err != nil {
		return nil, fmt.Errorf("read review plan: %w", err)
	}

	templateDoc, templateNote, err := mdfile.ParseNote(template)
	if err != nil {
		return nil, fmt.Errorf("parse note template: %w", err)
	}
	if string(templateNote.ID) != manifest.Note.ID ||
		string(templateNote.Source) == "" {
		return nil, fmt.Errorf("note template identity does not match manifest")
	}
	templateBody, err := sectionBytes(templateDoc, template, mdfile.SecNoteBody)
	if err != nil {
		return nil, err
	}
	if got := store.ContentHash(templateBody); got != manifest.Note.ExpectedSourceBody {
		return nil, fmt.Errorf("note template source body hash drifted: %s != %s",
			got, manifest.Note.ExpectedSourceBody)
	}

	review, err := reviewBytesFromPlan(reviewPlan, manifest.Note.ID,
		manifest.Note.ReviewSourceRefs)
	if err != nil {
		return nil, err
	}
	if visible := stripReviewAnchors(frameSection(review)); !bytes.Equal(visible, templateBody) {
		return nil, fmt.Errorf("review plan visible body differs from note template")
	}

	drafts := make([]store.CandidateDraft, 0, len(manifest.Candidates))
	targets := make(map[string][]byte, len(manifest.Candidates))
	mappings := make([]CandidateMapping, 0, len(manifest.Candidates))
	var counts ExpectedCounts
	counts.Notes = 1
	for i, declared := range manifest.Candidates {
		seed, err := readInput(root, declared.SeedPath, declared.SeedHash)
		if err != nil {
			return nil, fmt.Errorf("candidate %s seed: %w", declared.Key, err)
		}
		draft, artifactCounts, err := candidateFromSeed(
			declared, seed, templateNote.Source, templateNote.ID)
		if err != nil {
			return nil, fmt.Errorf("candidate %s: %w", declared.Key, err)
		}
		drafts = append(drafts, draft)
		counts.Knowledge += artifactCounts.Knowledge
		counts.Opinions += artifactCounts.Opinions
		counts.PendingOpinions += artifactCounts.PendingOpinions
		counts.MaterialRelations += artifactCounts.MaterialRelations
		counts.ArgumentRelations += artifactCounts.ArgumentRelations

		target := seed
		current, exists, err := readOptionalVaultFile(root, declared.Path)
		if err != nil {
			return nil, err
		}
		if exists {
			target, err = carrySections(current, seed, []string{mdfile.SecUserAppend})
			if err != nil {
				return nil, fmt.Errorf("candidate %s preserve user section: %w",
					declared.Key, err)
			}
			if !bytes.Equal(current, target) && declared.BeforeHash == "" {
				return nil, fmt.Errorf("candidate %s target exists but before_hash is empty",
					declared.Key)
			}
			if !bytes.Equal(current, target) &&
				store.ContentHash(current) != declared.BeforeHash {
				return nil, fmt.Errorf("candidate %s target hash drifted: %s != %s",
					declared.Key, store.ContentHash(current), declared.BeforeHash)
			}
		}
		targets[declared.Path] = target
		mappings = append(mappings, CandidateMapping{
			Key: declared.Key, Kind: string(draft.Kind), Output: declared.Output,
			Path: declared.Path, SourceRefs: append([]string(nil), declared.SourceRefs...),
		})
		if i == 0 && len(target) == 0 {
			return nil, fmt.Errorf("candidate target bytes are empty")
		}
	}

	draftCoverage, finalCoverage, err := coverageStates(manifest)
	if err != nil {
		return nil, err
	}
	counts.CoverageModules = len(finalCoverage)
	for _, item := range finalCoverage {
		if item.Disposition == store.CoverageDispNoteOnly {
			counts.NoteOnlyModules++
		}
	}
	draftExtraction, err := store.CandidateDraftBytes(drafts, draftCoverage)
	if err != nil {
		return nil, fmt.Errorf("render candidate draft extraction: %w", err)
	}
	finalExtraction, err := finalExtractionBytes(manifest.Candidates, finalCoverage)
	if err != nil {
		return nil, err
	}

	targetNote, err := replaceSectionExact(template, mdfile.SecNoteBody, frameSection(review))
	if err != nil {
		return nil, err
	}
	targetNote, err = replaceSectionExact(
		targetNote, mdfile.SecExtraction, frameSection(draftExtraction))
	if err != nil {
		return nil, err
	}
	outputs := make(map[string]string, len(manifest.Candidates))
	for _, candidate := range manifest.Candidates {
		outputs[candidate.Key] = candidate.Output
	}
	targetNote, err = mdfile.ReplaceCandidateOutputs(targetNote, outputs)
	if err != nil {
		return nil, fmt.Errorf("write candidate outputs: %w", err)
	}
	targetNote, err = mdfile.FinalizeCandidateExtraction(targetNote, finalExtraction)
	if err != nil {
		return nil, fmt.Errorf("finalize extraction: %w", err)
	}
	targetNote, err = carrySections(currentNote, targetNote, manifest.Note.PreserveSections)
	if err != nil {
		return nil, fmt.Errorf("preserve note sections: %w", err)
	}
	if err := store.PreserveUserSectionsAfterCut(
		manifest.Note.Path, currentNote, targetNote); err != nil {
		return nil, err
	}
	if _, _, _, _, err := store.ParseMaterializationNote(targetNote); err != nil {
		return nil, fmt.Errorf("target note validation: %w", err)
	}
	if !bytes.Equal(currentNote, targetNote) &&
		store.ContentHash(currentNote) != manifest.Note.BeforeHash {
		return nil, fmt.Errorf("note hash drifted: %s != %s",
			store.ContentHash(currentNote), manifest.Note.BeforeHash)
	}

	files := make([]plannedFile, 0,
		len(manifest.Candidates)+len(manifest.Tombstones)+1)
	for _, candidate := range manifest.Candidates {
		current, exists, err := readOptionalVaultFile(root, candidate.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, plannedFile{
			path: candidate.Path, before: current,
			target: targets[candidate.Path], exists: exists,
		})
	}

	tombstoneTargets, err := buildTombstones(root, manifest.Tombstones)
	if err != nil {
		return nil, err
	}
	counts.Tombstones = len(tombstoneTargets)
	for _, tombstone := range manifest.Tombstones {
		target := tombstoneTargets[tombstone.Path]
		current, _, err := readOptionalVaultFile(root, tombstone.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, plannedFile{
			path: tombstone.Path, before: current, target: target, exists: true,
		})
	}
	files = append(files, plannedFile{
		path: manifest.Note.Path, before: currentNote, target: targetNote, exists: true,
	})
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].path == manifest.Note.Path {
			return false
		}
		if files[j].path == manifest.Note.Path {
			return true
		}
		return files[i].path < files[j].path
	})

	if err := verifyCounts(manifest.Expected, counts); err != nil {
		return nil, err
	}
	if err := verifyArtifactGraph(manifest, templateNote, targets); err != nil {
		return nil, err
	}
	if err := verifyMaterializedProjection(manifest, targetNote, targets); err != nil {
		return nil, err
	}
	return &migrationPlan{files: files, mappings: mappings, counts: counts}, nil
}

func reviewBytesFromPlan(raw []byte, noteID string, refs []string) ([]byte, error) {
	parsed, err := plan.Parse(raw)
	if err != nil {
		return nil, err
	}
	var noteOp *plan.Op
	for _, op := range parsed.Ops {
		if op.Name == "write_note" && op.NoteID == noteID {
			if noteOp != nil {
				return nil, fmt.Errorf("review plan contains duplicate write_note for %s", noteID)
			}
			noteOp = op
		}
	}
	if noteOp == nil || !noteOp.BlocksGiven {
		return nil, fmt.Errorf("review plan lacks blocks for %s", noteID)
	}
	blocks := make([]store.NoteBlock, len(noteOp.Blocks))
	copy(blocks, noteOp.Blocks)
	sourceIndex := 0
	for i := range blocks {
		if blocks[i].Role != store.NoteBlockSource {
			continue
		}
		if sourceIndex >= len(refs) {
			return nil, fmt.Errorf("review plan has more source blocks than source refs")
		}
		blocks[i].SourceRef = refs[sourceIndex]
		sourceIndex++
	}
	if sourceIndex != len(refs) {
		return nil, fmt.Errorf("review source count=%d, manifest refs=%d",
			sourceIndex, len(refs))
	}
	return store.NoteReviewBytes(blocks, nil)
}

func candidateFromSeed(declared CandidateManifest, raw []byte,
	source model.SourceID, note model.NoteID,
) (store.CandidateDraft, ExpectedCounts, error) {
	var (
		doc       *mdfile.Doc
		title     string
		tags      []string
		sources   []model.SourceRef
		relations []model.Relation
		kind      store.CandidateKind
		counts    ExpectedCounts
	)
	if strings.HasPrefix(declared.Output, model.PrefixCard) {
		parsed, card, err := mdfile.ParseCard(raw)
		if err != nil {
			return store.CandidateDraft{}, counts, err
		}
		if string(card.ID) != declared.Output {
			return store.CandidateDraft{}, counts,
				fmt.Errorf("seed id=%s, want %s", card.ID, declared.Output)
		}
		doc, sources, relations = parsed, card.Sources, card.Relations
		kind = store.CandidateKindKnowledge
		counts.Knowledge = 1
	} else {
		parsed, opinion, err := mdfile.ParseOpinion(raw)
		if err != nil {
			return store.CandidateDraft{}, counts, err
		}
		if string(opinion.ID) != declared.Output {
			return store.CandidateDraft{}, counts,
				fmt.Errorf("seed id=%s, want %s", opinion.ID, declared.Output)
		}
		if opinion.Validation != model.ValidationPending {
			return store.CandidateDraft{}, counts,
				fmt.Errorf("seed opinion validation=%s, want pending", opinion.Validation)
		}
		doc, sources, relations = parsed, opinion.Sources, opinion.Relations
		kind = store.CandidateKindOpinion
		counts.Opinions = 1
		counts.PendingOpinions = 1
	}
	var meta struct {
		Title string   `yaml:"title"`
		Tags  []string `yaml:"tags"`
	}
	if err := doc.DecodeFM(&meta); err != nil {
		return store.CandidateDraft{}, counts, err
	}
	title, tags = meta.Title, meta.Tags
	if tags == nil {
		tags = []string{}
	}
	var material *model.SourceRef
	for i := range sources {
		if sources[i].Source == source && sources[i].Note == note {
			if material != nil {
				return store.CandidateDraft{}, counts,
					fmt.Errorf("seed has duplicate matching material relations")
			}
			ref := sources[i]
			material = &ref
		}
	}
	if material == nil {
		return store.CandidateDraft{}, counts,
			fmt.Errorf("seed lacks material relation to %s/%s", source, note)
	}
	counts.MaterialRelations = len(sources)
	counts.ArgumentRelations = len(relations)
	sections, err := candidateSections(doc, raw, kind)
	if err != nil {
		return store.CandidateDraft{}, counts, err
	}
	return store.CandidateDraft{
		Key: declared.Key, Kind: kind, Title: title,
		SourceRefs: append([]string(nil), declared.SourceRefs...),
		Rel:        string(material.Rel), Reason: material.Reason,
		Tags: append([]string(nil), tags...), Sections: sections,
	}, counts, nil
}

func candidateSections(doc *mdfile.Doc, raw []byte,
	kind store.CandidateKind,
) ([]store.CandidateDraftSection, error) {
	names := []string{mdfile.SecKnowledge, mdfile.SecBoundary}
	required := mdfile.SecKnowledge
	if kind == store.CandidateKindOpinion {
		names = []string{
			mdfile.SecOpinionClaim, mdfile.SecArgument,
			mdfile.SecCounter, mdfile.SecToVerify,
		}
		required = mdfile.SecOpinionClaim
	}
	var out []store.CandidateDraftSection
	for _, name := range names {
		body, err := sectionBytes(doc, raw, name)
		if err != nil {
			return nil, err
		}
		content, err := unframeSection(body)
		if err != nil {
			return nil, fmt.Errorf("section %q: %w", name, err)
		}
		if len(bytes.TrimSpace(content)) == 0 {
			if name == required {
				return nil, fmt.Errorf("required section %q is empty", name)
			}
			continue
		}
		out = append(out, store.CandidateDraftSection{Name: name, Body: content})
	}
	return out, nil
}

func coverageStates(m Manifest) ([]store.CandidateCoverage,
	[]store.ExtractionCoverage, error,
) {
	keyByOutput := make(map[string]string, len(m.Candidates))
	for _, candidate := range m.Candidates {
		keyByOutput[candidate.Output] = candidate.Key
	}
	draft := make([]store.CandidateCoverage, 0, len(m.Coverage))
	final := make([]store.ExtractionCoverage, 0, len(m.Coverage))
	for _, item := range m.Coverage {
		d := store.CandidateCoverage{
			Module: item.Module, SourceRefs: append([]string(nil), item.SourceRefs...),
			Summary: item.Summary, Reason: item.Reason,
		}
		f := store.ExtractionCoverage{
			Module: item.Module, SourceRefs: append([]string(nil), item.SourceRefs...),
			Summary: item.Summary, Disposition: item.Disposition,
			Outputs: append([]string(nil), item.Outputs...), Reason: item.Reason,
		}
		if item.Disposition == store.CoverageDispNoteOnly {
			d.Disposition = store.CandidateCoverageNoteOnly
		} else {
			d.Disposition = store.CandidateCoverageCandidate
			for _, output := range item.Outputs {
				key := keyByOutput[output]
				if key == "" {
					return nil, nil, fmt.Errorf(
						"coverage %s has no candidate for %s", item.Module, output)
				}
				d.Candidates = append(d.Candidates, key)
			}
		}
		if d.Candidates == nil {
			d.Candidates = []string{}
		}
		if f.Outputs == nil {
			f.Outputs = []string{}
		}
		draft = append(draft, d)
		final = append(final, f)
	}
	return draft, final, nil
}

func finalExtractionBytes(candidates []CandidateManifest,
	coverage []store.ExtractionCoverage,
) ([]byte, error) {
	extraction := store.NoteExtraction{Coverage: coverage}
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate.Output, model.PrefixCard) {
			extraction.Knowledge = append(extraction.Knowledge, candidate.Output)
		} else {
			extraction.Opinions = append(extraction.Opinions,
				candidate.Output+" "+store.ExtractionValidationMark(model.ValidationPending))
		}
	}
	return extraction.Bytes()
}

func buildTombstones(root string,
	declared []TombstoneManifest,
) (map[string][]byte, error) {
	if len(declared) == 0 {
		return map[string][]byte{}, nil
	}
	st := store.New(root)
	if err := st.BeginAtomic(); err != nil {
		return nil, err
	}
	defer st.EndAtomic()
	for i, item := range declared {
		current, err := st.Read(item.Path)
		if err != nil {
			return nil, fmt.Errorf("tombstones[%d]: %w", i, err)
		}
		deletedAt, _ := model.ParseStamp(item.DeletedAt)
		updatedAt, _ := model.ParseStamp(item.UpdatedAt)
		if _, err := st.ApplyStateWrite(store.StateWriteSpec{
			Op: store.StateWriteDeleted, Rel: item.Path, ExpectedHash: current.Hash,
			At: deletedAt, Reason: item.Reason, Stamp: updatedAt,
		}); err != nil {
			return nil, fmt.Errorf("tombstones[%d] deleted: %w", i, err)
		}
		staged, err := st.Read(item.Path)
		if err != nil {
			return nil, err
		}
		replacement, _ := model.ParseRelationEndpoint(item.Replacement)
		if _, err := st.ApplyStateWrite(store.StateWriteSpec{
			Op: store.StateWriteReplacedBy, Rel: item.Path, ExpectedHash: staged.Hash,
			Target: replacement, Reason: item.Reason, Stamp: updatedAt,
		}); err != nil {
			return nil, fmt.Errorf("tombstones[%d] replaced_by: %w", i, err)
		}
	}
	targets := make(map[string][]byte, len(declared))
	for _, file := range st.AtomicWriteSet() {
		targets[file.Path] = file.TargetBytes
	}
	for i, item := range declared {
		current, err := readVaultFile(root, item.Path)
		if err != nil {
			return nil, err
		}
		target := targets[item.Path]
		if target == nil {
			return nil, fmt.Errorf("tombstones[%d] produced no target bytes", i)
		}
		if !bytes.Equal(current, target) &&
			store.ContentHash(current) != item.BeforeHash {
			return nil, fmt.Errorf("tombstone %s hash drifted: %s != %s",
				item.Path, store.ContentHash(current), item.BeforeHash)
		}
	}
	return targets, nil
}

func verifyMaterializedProjection(manifest Manifest,
	note []byte, artifacts map[string][]byte,
) error {
	shadow, err := os.MkdirTemp("", "evergreen-storage-v3-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(shadow)
	for rel, raw := range artifacts {
		if err := writeShadowFile(shadow, rel, raw); err != nil {
			return err
		}
	}
	if err := writeShadowFile(shadow, manifest.Note.Path, note); err != nil {
		return err
	}
	date, err := model.ParseDate("2000-01-01")
	if err != nil {
		return err
	}
	stamp, err := model.ParseStamp("2000-01-01T00:00:00Z")
	if err != nil {
		return err
	}
	result, err := plan.MaterializeCandidates(store.New(shadow), plan.MaterializeRequest{
		Note: model.NoteID(manifest.Note.ID), All: true, Date: date, Stamp: stamp,
	})
	if err != nil {
		return fmt.Errorf("materializer projection check: %w", err)
	}
	if len(result.WriteSet) != 0 || !result.Finalized ||
		len(result.Candidates) != len(manifest.Candidates) {
		return fmt.Errorf("materializer projection is not a finalized no-op")
	}
	return nil
}

func writeShadowFile(root, rel string, raw []byte) error {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, raw, 0o644)
}

func verifyCounts(want, got ExpectedCounts) error {
	if want != got {
		return fmt.Errorf("migration counts differ: got %+v, want %+v", got, want)
	}
	return nil
}

func verifyArtifactGraph(m Manifest, note model.Note, targets map[string][]byte) error {
	ids := make(map[string]bool, len(m.Candidates))
	for _, candidate := range m.Candidates {
		ids[candidate.Output] = true
	}
	for _, candidate := range m.Candidates {
		raw := targets[candidate.Path]
		var sources []model.SourceRef
		var relations []model.Relation
		if strings.HasPrefix(candidate.Output, model.PrefixCard) {
			_, artifact, err := mdfile.ParseCard(raw)
			if err != nil {
				return err
			}
			sources, relations = artifact.Sources, artifact.Relations
		} else {
			_, artifact, err := mdfile.ParseOpinion(raw)
			if err != nil {
				return err
			}
			sources, relations = artifact.Sources, artifact.Relations
		}
		for _, source := range sources {
			if source.Source != note.Source || source.Note != note.ID {
				return fmt.Errorf("%s has material relation outside migrated Note: %s/%s",
					candidate.Output, source.Source, source.Note)
			}
		}
		for _, relation := range relations {
			if !ids[string(relation.Target)] {
				return fmt.Errorf("%s has relation to undeclared output %s",
					candidate.Output, relation.Target)
			}
		}
	}
	return nil
}

func changedFiles(files []plannedFile) []plannedFile {
	out := make([]plannedFile, 0, len(files))
	for _, file := range files {
		if !bytes.Equal(file.before, file.target) {
			out = append(out, file)
		}
	}
	return out
}

func readInput(root, rel, expectedHash string) ([]byte, error) {
	raw, err := readVaultFile(root, rel)
	if err != nil {
		return nil, err
	}
	if got := store.ContentHash(raw); got != expectedHash {
		return nil, fmt.Errorf("%s hash drifted: %s != %s", rel, got, expectedHash)
	}
	return raw, nil
}

func readVaultFile(root, rel string) ([]byte, error) {
	if err := validateRelPath(rel); err != nil {
		return nil, err
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	relToRoot, err := filepath.Rel(root, resolved)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("path escapes Vault through symlink: %s", rel)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file: %s", rel)
	}
	return os.ReadFile(resolved)
}

func readOptionalVaultFile(root, rel string) ([]byte, bool, error) {
	raw, err := readVaultFile(root, rel)
	if err == nil {
		return raw, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return nil, false, err
}

func sectionBytes(doc *mdfile.Doc, raw []byte, name string) ([]byte, error) {
	span, ok := doc.Section(name)
	if !ok {
		return nil, fmt.Errorf("section %q is missing", name)
	}
	return append([]byte(nil), raw[span.Body:span.End]...), nil
}

func frameSection(payload []byte) []byte {
	out := make([]byte, 0, len(payload)+2)
	out = append(out, '\n')
	out = append(out, payload...)
	return append(out, '\n')
}

func unframeSection(body []byte) ([]byte, error) {
	if len(body) < 3 || body[0] != '\n' ||
		body[len(body)-1] != '\n' || body[len(body)-2] != '\n' {
		return nil, fmt.Errorf("section body framing is not canonical")
	}
	return append([]byte(nil), body[1:len(body)-1]...), nil
}

func replaceSectionExact(raw []byte, section string, body []byte) ([]byte, error) {
	if len(body) > 0 && body[len(body)-1] != '\n' {
		return nil, fmt.Errorf("replacement section %q is not line terminated", section)
	}
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return nil, err
	}
	span, ok := doc.Section(section)
	if !ok {
		return nil, fmt.Errorf("section %q is missing", section)
	}
	out := make([]byte, 0, len(raw)-(span.End-span.Body)+len(body))
	out = append(out, raw[:span.Body]...)
	out = append(out, body...)
	out = append(out, raw[span.End:]...)
	if err := mdfile.SelfCheck(out); err != nil {
		return nil, fmt.Errorf("replace section %q: %w", section, err)
	}
	return out, nil
}

func carrySections(before, target []byte, names []string) ([]byte, error) {
	beforeDoc, err := mdfile.Parse(before)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), target...)
	for _, name := range names {
		span, ok := beforeDoc.Section(name)
		if !ok {
			continue
		}
		body := append([]byte(nil), before[span.Body:span.End]...)
		out, err = replaceSectionExact(out, name, body)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func stripReviewAnchors(raw []byte) []byte {
	var out []byte
	for _, line := range bytes.SplitAfter(raw, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("<!-- eg:nr:")) {
			continue
		}
		out = append(out, line...)
	}
	return out
}

func contentHashOrEmpty(exists bool, raw []byte) string {
	if !exists {
		return ""
	}
	return store.ContentHash(raw)
}
