package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	RuntimeDirName      = ".siyuan/evergreen"
	TransactionsDirName = "transactions"

	FaultAfterPrepared     = "after_prepared"
	FaultBeforeRename      = "before_rename"
	FaultAfterRename       = "after_rename"
	FaultAfterFilesApplied = "after_files_applied"
	FaultBeforeGit         = "before_git"
	FaultAfterGit          = "after_git"
	FaultBeforeEvent       = "before_event"
)

const operationJournalVersion = 1

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type FilesystemAuthorityOptions struct {
	Root      string
	Registry  *Registry
	Committer OperationCommitter
	IndexSink IndexSink
	Faults    FaultInjector
}

type FilesystemAuthority struct {
	root      string
	registry  *Registry
	committer OperationCommitter
	indexSink IndexSink
	faults    FaultInjector

	writeGate sync.Mutex
}

type operationManifest struct {
	JournalVersion int              `json:"journal_version"`
	Operation      PlannedOperation `json:"operation"`
	Record         OperationRecord  `json:"record"`
	ExpectedParent string           `json:"expected_git_parent,omitempty"`
	Files          []journalFile    `json:"files"`
}

type journalFile struct {
	LogicalID      LogicalID `json:"logical_id"`
	Path           string    `json:"path"`
	Create         bool      `json:"create"`
	BeforeSemantic string    `json:"before_semantic_hash,omitempty"`
	BeforeContent  string    `json:"before_content_hash,omitempty"`
	AfterSemantic  string    `json:"after_semantic_hash"`
	AfterContent   string    `json:"after_content_hash"`
	BeforeRef      string    `json:"before_ref,omitempty"`
	AfterRef       string    `json:"after_ref"`
}

func NewFilesystemAuthority(options FilesystemAuthorityOptions) (*FilesystemAuthority, error) {
	if strings.TrimSpace(options.Root) == "" {
		return nil, errors.New("authority root is required")
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, err
	}
	if info, statErr := os.Stat(root); statErr != nil {
		return nil, statErr
	} else if !info.IsDir() {
		return nil, fmt.Errorf("authority root %s is not a directory", root)
	}
	if options.Registry == nil {
		options.Registry = DefaultRegistry()
	}
	return &FilesystemAuthority{
		root: root, registry: options.Registry, committer: options.Committer,
		indexSink: options.IndexSink, faults: options.Faults,
	}, nil
}

func (a *FilesystemAuthority) Root() string { return a.root }

func (a *FilesystemAuthority) Load(_ context.Context, logicalID LogicalID) ([]byte, error) {
	location, err := a.Resolve(context.Background(), logicalID)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(a.authorityPath(location.Path))
}

func (a *FilesystemAuthority) List(_ context.Context) ([]LogicalID, error) {
	index, err := ScanFS(os.DirFS(a.root), a.registry)
	if err != nil {
		return nil, err
	}
	return SortedLogicalIDs(index), nil
}

func (a *FilesystemAuthority) Resolve(_ context.Context, logicalID LogicalID) (LogicalLocation, error) {
	index, err := ScanFS(os.DirFS(a.root), a.registry)
	if err != nil {
		return LogicalLocation{}, err
	}
	location, ok := index.Locations[logicalID]
	if !ok {
		return LogicalLocation{}, fs.ErrNotExist
	}
	return location, nil
}

func (a *FilesystemAuthority) ApplyOperation(ctx context.Context, operation PlannedOperation) (OperationRecord, error) {
	a.writeGate.Lock()
	defer a.writeGate.Unlock()

	if err := ValidatePlannedOperation(operation, a.registry); err != nil {
		return OperationRecord{}, err
	}
	if !operationIDPattern.MatchString(operation.Plan.OperationID) {
		return OperationRecord{}, validationError(CodeInvalidPlan, "operation_id", "operation ID is not safe for journal storage")
	}
	if err := a.blockedByOtherOperation(operation.Plan.OperationID); err != nil {
		return OperationRecord{}, err
	}

	manifest, err := a.loadManifest(operation.Plan.OperationID)
	switch {
	case err == nil:
		if manifest.Record.PlanHash != operation.PlanHash {
			return manifest.Record, validationError(
				CodeOperationIDReused,
				"operation_id",
				fmt.Sprintf("operation ID %q is already bound to plan %s", operation.Plan.OperationID, manifest.Record.PlanHash),
			)
		}
	case errors.Is(err, fs.ErrNotExist):
		manifest, err = a.createManifest(ctx, operation)
		if err != nil {
			return OperationRecord{}, err
		}
	default:
		return OperationRecord{}, err
	}
	return a.resume(ctx, manifest)
}

func (a *FilesystemAuthority) Operation(_ context.Context, operationID string) (OperationRecord, error) {
	if !operationIDPattern.MatchString(operationID) {
		return OperationRecord{}, fs.ErrNotExist
	}
	manifest, err := a.loadManifest(operationID)
	if err != nil {
		return OperationRecord{}, err
	}
	return manifest.Record, nil
}

func (a *FilesystemAuthority) RecoverOperations(ctx context.Context) ([]OperationRecord, error) {
	a.writeGate.Lock()
	defer a.writeGate.Unlock()

	manifests, err := a.listManifests()
	if err != nil {
		return nil, err
	}
	var records []OperationRecord
	for _, manifest := range manifests {
		if manifest.Record.State == OperationCompleted && !manifest.Record.DerivedStale {
			continue
		}
		record, resumeErr := a.resume(ctx, manifest)
		records = append(records, record)
		if resumeErr != nil {
			return records, resumeErr
		}
	}
	return records, nil
}

func (a *FilesystemAuthority) createManifest(ctx context.Context, operation PlannedOperation) (*operationManifest, error) {
	if err := a.verifyCAS(ctx, operation); err != nil {
		return nil, err
	}
	operationDir := a.operationDir(operation.Plan.OperationID)
	if err := os.MkdirAll(a.transactionsDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(operationDir, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			existing, loadErr := a.loadManifest(operation.Plan.OperationID)
			if loadErr != nil {
				return nil, loadErr
			}
			return existing, nil
		}
		return nil, err
	}
	if err := syncDir(a.transactionsDir()); err != nil {
		return nil, err
	}

	expectedParent := ""
	if reader, ok := a.committer.(interface {
		Head(context.Context) (string, error)
	}); ok {
		parent, headErr := reader.Head(ctx)
		if headErr != nil {
			return nil, headErr
		}
		expectedParent = parent
	}
	manifest := &operationManifest{
		JournalVersion: operationJournalVersion,
		Operation:      operation,
		ExpectedParent: expectedParent,
		Record: OperationRecord{
			OperationID: operation.Plan.OperationID,
			State:       OperationPlanned,
			PlanHash:    operation.PlanHash,
		},
		Files: make([]journalFile, 0, len(operation.Images)),
	}
	for index, image := range operation.Images {
		file := journalFile{
			LogicalID:     image.LogicalID,
			Path:          image.Path,
			Create:        image.Create,
			AfterSemantic: image.SemanticHash,
			AfterContent:  imageContentHash(image),
			AfterRef:      filepath.ToSlash(filepath.Join("after", fmt.Sprintf("%04d", index))),
		}
		if !image.Create {
			current, readErr := os.ReadFile(a.authorityPath(image.Path))
			if readErr != nil {
				return nil, readErr
			}
			file.BeforeSemantic = operation.Plan.Writes[index].Before
			file.BeforeContent = contentHash(current)
			file.BeforeRef = filepath.ToSlash(filepath.Join("before", fmt.Sprintf("%04d", index)))
		}
		manifest.Files = append(manifest.Files, file)
	}
	if err := a.writeManifest(manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (a *FilesystemAuthority) resume(ctx context.Context, manifest *operationManifest) (OperationRecord, error) {
	if manifest == nil {
		return OperationRecord{}, errors.New("operation manifest is nil")
	}
	for {
		switch manifest.Record.State {
		case OperationPlanned:
			if err := a.verifyCAS(ctx, manifest.Operation); err != nil {
				return manifest.Record, err
			}
			if err := a.prepare(manifest); err != nil {
				return manifest.Record, err
			}
			if err := a.fire(FaultAfterPrepared, -1); err != nil {
				return manifest.Record, err
			}
		case OperationPrepared:
			if err := a.applyAfterImages(manifest); err != nil {
				return manifest.Record, err
			}
			manifest.Record.State = OperationFilesApplied
			if err := a.writeManifest(manifest); err != nil {
				return manifest.Record, err
			}
			if err := a.fire(FaultAfterFilesApplied, -1); err != nil {
				return manifest.Record, err
			}
		case OperationFilesApplied:
			if err := a.verifyAllAfter(manifest); err != nil {
				return manifest.Record, err
			}
			if a.committer == nil {
				return manifest.Record, validationError(CodeGitCommitFailed, "git", "operation committer is unavailable")
			}
			if err := a.fire(FaultBeforeGit, -1); err != nil {
				return manifest.Record, err
			}
			paths := make([]string, 0, len(manifest.Files))
			for _, file := range manifest.Files {
				paths = append(paths, file.Path)
			}
			sha, err := a.committer.Commit(ctx, CommitRequest{
				OperationID:    manifest.Record.OperationID,
				Principal:      manifest.Operation.Plan.Principal,
				Command:        manifest.Operation.Plan.Command,
				PlanHash:       manifest.Record.PlanHash,
				ExpectedParent: manifest.ExpectedParent,
				Paths:          paths,
			})
			if err != nil {
				manifest.Record.Diagnostics = []Diagnostic{{
					Code: CodeGitCommitFailed, Level: "error", Path: "git", Message: err.Error(),
				}}
				_ = a.writeManifest(manifest)
				return manifest.Record, &DiagnosticError{Diagnostics: manifest.Record.Diagnostics}
			}
			if err := a.fire(FaultAfterGit, -1); err != nil {
				return manifest.Record, err
			}
			manifest.Record.GitCommit = sha
			manifest.Record.State = OperationGitCommitted
			manifest.Record.Diagnostics = nil
			if err := a.writeManifest(manifest); err != nil {
				return manifest.Record, err
			}
		case OperationGitCommitted:
			a.completeDerived(ctx, manifest)
			manifest.Record.State = OperationCompleted
			if err := a.writeManifest(manifest); err != nil {
				return manifest.Record, err
			}
		case OperationCompleted:
			if manifest.Record.DerivedStale {
				a.completeDerived(ctx, manifest)
				if err := a.writeManifest(manifest); err != nil {
					return manifest.Record, err
				}
			}
			return manifest.Record, nil
		default:
			return manifest.Record, validationError(CodeTransactionBlocked, "state", fmt.Sprintf("unknown operation state %q", manifest.Record.State))
		}
	}
}

func (a *FilesystemAuthority) verifyCAS(ctx context.Context, operation PlannedOperation) error {
	for index, write := range operation.Plan.Writes {
		image := operation.Images[index]
		current, err := a.Load(ctx, write.LogicalID)
		if image.Create {
			if err == nil {
				return validationError(CodeBaseMismatch, image.Path, "create target already exists")
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err != nil {
			return validationError(CodeBaseMismatch, image.Path, "base entity cannot be reloaded: "+err.Error())
		}
		hash, err := SemanticHash(current)
		if err != nil {
			return err
		}
		if hash != write.Before {
			return validationError(CodeBaseMismatch, image.Path, fmt.Sprintf("semantic base hash %q does not match current %q", write.Before, hash))
		}
		location, err := a.Resolve(ctx, write.LogicalID)
		if err != nil {
			return err
		}
		if location.Path != image.Path {
			return validationError(CodeBaseMismatch, image.Path, fmt.Sprintf("logical ID moved to %s after planning", location.Path))
		}
	}
	return nil
}

func (a *FilesystemAuthority) prepare(manifest *operationManifest) error {
	beforeDir := filepath.Join(a.operationDir(manifest.Record.OperationID), "before")
	afterDir := filepath.Join(a.operationDir(manifest.Record.OperationID), "after")
	if err := os.MkdirAll(beforeDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(afterDir, 0o755); err != nil {
		return err
	}
	for index, file := range manifest.Files {
		if !file.Create {
			current, err := os.ReadFile(a.authorityPath(file.Path))
			if err != nil {
				return err
			}
			if contentHash(current) != file.BeforeContent {
				return validationError(CodeBaseMismatch, file.Path, "content changed while preparing operation")
			}
			if err := writeFileSynced(filepath.Join(a.operationDir(manifest.Record.OperationID), file.BeforeRef), current); err != nil {
				return err
			}
		}
		if err := writeFileSynced(
			filepath.Join(a.operationDir(manifest.Record.OperationID), file.AfterRef),
			manifest.Operation.Images[index].Bytes,
		); err != nil {
			return err
		}
	}
	if err := syncDir(beforeDir); err != nil {
		return err
	}
	if err := syncDir(afterDir); err != nil {
		return err
	}
	manifest.Record.State = OperationPrepared
	return a.writeManifest(manifest)
}

type stagedAuthorityFile struct {
	index int
	temp  string
	final string
	dir   string
}

func (a *FilesystemAuthority) applyAfterImages(manifest *operationManifest) error {
	staged := make([]stagedAuthorityFile, 0, len(manifest.Files))
	for index, file := range manifest.Files {
		after, err := os.ReadFile(filepath.Join(a.operationDir(manifest.Record.OperationID), file.AfterRef))
		if err != nil {
			return err
		}
		if got := contentHash(after); got != file.AfterContent {
			return validationError(CodeTransactionBlocked, file.AfterRef,
				fmt.Sprintf("journal after-image checksum mismatch: got %s want %s", got, file.AfterContent))
		}
		final := a.authorityPath(file.Path)
		if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
			return err
		}
		current, readErr := os.ReadFile(final)
		switch {
		case readErr == nil && contentHash(current) == file.AfterContent:
			continue
		case readErr == nil && !file.Create && contentHash(current) == file.BeforeContent:
		case errors.Is(readErr, fs.ErrNotExist) && file.Create:
		case readErr != nil:
			return readErr
		default:
			return validationError(CodeTransactionBlocked, file.Path, "authority bytes match neither journal before nor after image")
		}
		temp := filepath.Join(filepath.Dir(final), "."+filepath.Base(final)+".eg-"+manifest.Record.OperationID)
		if err := writeFileSynced(temp, after); err != nil {
			return err
		}
		staged = append(staged, stagedAuthorityFile{index: index, temp: temp, final: final, dir: filepath.Dir(final)})
	}
	for _, dir := range uniqueAuthorityDirs(staged) {
		if err := syncDir(dir); err != nil {
			return err
		}
	}
	for _, stagedFile := range staged {
		if err := a.fire(FaultBeforeRename, stagedFile.index); err != nil {
			return err
		}
		if err := os.Rename(stagedFile.temp, stagedFile.final); err != nil {
			return err
		}
		if err := syncDir(stagedFile.dir); err != nil {
			return err
		}
		if err := a.fire(FaultAfterRename, stagedFile.index); err != nil {
			return err
		}
	}
	return nil
}

func (a *FilesystemAuthority) verifyAllAfter(manifest *operationManifest) error {
	for _, file := range manifest.Files {
		current, err := os.ReadFile(a.authorityPath(file.Path))
		if err != nil {
			return validationError(CodeTransactionBlocked, file.Path, "authority after-image is unavailable: "+err.Error())
		}
		if contentHash(current) != file.AfterContent {
			return validationError(CodeTransactionBlocked, file.Path, "authority bytes do not match journal after-image")
		}
	}
	return nil
}

func (a *FilesystemAuthority) completeDerived(ctx context.Context, manifest *operationManifest) {
	ids := make([]LogicalID, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		ids = append(ids, file.LogicalID)
	}
	err := a.fire(FaultBeforeEvent, -1)
	if err == nil && a.indexSink != nil {
		err = a.indexSink.Invalidate(ctx, ids)
	}
	if err != nil {
		manifest.Record.DerivedStale = true
		manifest.Record.Diagnostics = []Diagnostic{{
			Code: CodeDerivedStale, Level: "warning", Path: "derived", Message: err.Error(),
		}}
		return
	}
	manifest.Record.DerivedStale = false
	manifest.Record.Diagnostics = nil
}

func (a *FilesystemAuthority) blockedByOtherOperation(operationID string) error {
	manifests, err := a.listManifests()
	if err != nil {
		return err
	}
	for _, manifest := range manifests {
		if manifest.Record.OperationID == operationID || manifest.Record.State == OperationCompleted {
			continue
		}
		return validationError(
			CodeTransactionBlocked,
			"operation_id",
			fmt.Sprintf("operation %s is blocked by unfinished operation %s in state %s",
				operationID, manifest.Record.OperationID, manifest.Record.State),
		)
	}
	return nil
}

func (a *FilesystemAuthority) listManifests() ([]*operationManifest, error) {
	entries, err := os.ReadDir(a.transactionsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifests []*operationManifest
	for _, entry := range entries {
		if !entry.IsDir() || !operationIDPattern.MatchString(entry.Name()) {
			continue
		}
		manifest, loadErr := a.loadManifest(entry.Name())
		if loadErr != nil {
			return nil, loadErr
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Record.OperationID < manifests[j].Record.OperationID
	})
	return manifests, nil
}

func (a *FilesystemAuthority) loadManifest(operationID string) (*operationManifest, error) {
	data, err := os.ReadFile(filepath.Join(a.operationDir(operationID), "manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest operationManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, validationError(CodeTransactionBlocked, "manifest", "cannot parse operation manifest: "+err.Error())
	}
	if manifest.JournalVersion != operationJournalVersion ||
		manifest.Record.OperationID != operationID ||
		manifest.Operation.Plan.OperationID != operationID {
		return nil, validationError(CodeTransactionBlocked, "manifest", "operation manifest identity or version mismatch")
	}
	state, err := os.ReadFile(filepath.Join(a.operationDir(operationID), "state"))
	if err != nil {
		return nil, err
	}
	if OperationState(strings.TrimSpace(string(state))) != manifest.Record.State {
		return nil, validationError(CodeTransactionBlocked, "state", "state file and manifest disagree")
	}
	for index := range manifest.Files {
		after, readErr := os.ReadFile(filepath.Join(a.operationDir(operationID), manifest.Files[index].AfterRef))
		if readErr != nil {
			if manifest.Record.State == OperationPlanned && errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			return nil, readErr
		}
		if index >= len(manifest.Operation.Images) {
			return nil, validationError(CodeTransactionBlocked, "manifest", "after-image metadata count mismatch")
		}
		manifest.Operation.Images[index].Bytes = after
	}
	return &manifest, nil
}

func (a *FilesystemAuthority) writeManifest(manifest *operationManifest) error {
	if manifest == nil {
		return errors.New("operation manifest is nil")
	}
	dir := a.operationDir(manifest.Record.OperationID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "manifest.json"), body); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, "state"), []byte(manifest.Record.State+"\n")); err != nil {
		return err
	}
	return syncDir(dir)
}

func cloneImageMetadata(images []AfterImage) []AfterImage {
	out := make([]AfterImage, len(images))
	for index, image := range images {
		out[index] = image
		out[index].Bytes = nil
		if out[index].ContentHash == "" {
			out[index].ContentHash = contentHash(image.Bytes)
		}
	}
	return out
}

func imageContentHash(image AfterImage) string {
	if image.ContentHash != "" {
		return image.ContentHash
	}
	return contentHash(image.Bytes)
}

func (a *FilesystemAuthority) authorityPath(rel string) string {
	return filepath.Join(a.root, filepath.FromSlash(rel))
}

func (a *FilesystemAuthority) transactionsDir() string {
	return filepath.Join(a.root, filepath.FromSlash(RuntimeDirName), TransactionsDirName)
}

func (a *FilesystemAuthority) operationDir(operationID string) string {
	return filepath.Join(a.transactionsDir(), operationID)
}

func (a *FilesystemAuthority) fire(point string, index int) error {
	if a.faults == nil {
		return nil
	}
	return a.faults.Fail(point, index)
}

func writeFileSynced(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeFileAtomic(path string, data []byte) error {
	temp := path + ".tmp"
	if err := writeFileSynced(temp, data); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err = dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}

func uniqueAuthorityDirs(files []stagedAuthorityFile) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, file := range files {
		if _, exists := seen[file.dir]; exists {
			continue
		}
		seen[file.dir] = struct{}{}
		out = append(out, file.dir)
	}
	return out
}
