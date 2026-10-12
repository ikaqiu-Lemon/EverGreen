package evergreencore

import (
	"bytes"
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

const operationJournalVersion = 3

var operationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type FilesystemAuthorityOptions struct {
	Root      string
	Registry  *Registry
	Storage   DocumentStorage
	Committer OperationCommitter
	IndexSink IndexSink
	Faults    FaultInjector
}

type FilesystemAuthority struct {
	root      string
	registry  *Registry
	storage   DocumentStorage
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
	NativeEditor   bool             `json:"native_editor,omitempty"`
}

type journalFile struct {
	LogicalID      LogicalID `json:"logical_id"`
	Path           string    `json:"path"`
	Create         bool      `json:"create"`
	Delete         bool      `json:"delete,omitempty"`
	BeforeSemantic string    `json:"before_semantic_hash,omitempty"`
	BeforeContent  string    `json:"before_content_hash,omitempty"`
	AfterSemantic  string    `json:"after_semantic_hash"`
	AfterContent   string    `json:"after_content_hash"`
	BeforeRef      string    `json:"before_ref,omitempty"`
	AfterRef       string    `json:"after_ref"`
	StoredAfter    []byte    `json:"stored_after,omitempty"`
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
	if options.Storage == nil {
		options.Storage = plainDocumentStorage{root}
	}
	return &FilesystemAuthority{
		root: root, registry: options.Registry, storage: options.Storage, committer: options.Committer,
		indexSink: options.IndexSink, faults: options.Faults,
	}, nil
}

func (a *FilesystemAuthority) Root() string { return a.root }

// LoadPhysicalDocument 只读取本次认证范围内的普通物理路径，不允许通过符号链接越界。
func (a *FilesystemAuthority) LoadPhysicalDocument(ctx context.Context, path string) ([]byte, error) {
	if err := validateAuthorityPath(path); err != nil {
		return nil, err
	}
	return a.LoadPhysicalFile(ctx, path)
}

func (a *FilesystemAuthority) LoadPhysicalFile(ctx context.Context, path string) ([]byte, error) {
	_, session, close, err := a.openStorage(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	if err = session.Allow(path); err != nil {
		return nil, err
	}
	if err = a.verifyAuthorityLocation(path); err != nil {
		return nil, err
	}
	return fs.ReadFile(session.Filesystem(), path)
}

func (a *FilesystemAuthority) Load(ctx context.Context, logicalID LogicalID) ([]byte, error) {
	ctx, session, close, err := a.openStorage(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	location, err := a.Resolve(ctx, logicalID)
	if err != nil {
		return nil, err
	}
	if err = session.Allow(location.Path); err != nil {
		return nil, err
	}
	return fs.ReadFile(session.Filesystem(), location.Path)
}

func (a *FilesystemAuthority) List(ctx context.Context) ([]LogicalID, error) {
	_, session, close, err := a.openStorage(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	index, err := ScanFS(session.Filesystem(), a.registry)
	if err != nil {
		return nil, err
	}
	return SortedLogicalIDs(index), nil
}

func (a *FilesystemAuthority) Resolve(ctx context.Context, logicalID LogicalID) (LogicalLocation, error) {
	_, session, close, err := a.openStorage(ctx)
	if err != nil {
		return LogicalLocation{}, err
	}
	defer close()
	index, err := ScanFS(session.Filesystem(), a.registry)
	if err != nil {
		return LogicalLocation{}, err
	}
	location, ok := index.Locations[logicalID]
	if !ok {
		return LogicalLocation{}, fs.ErrNotExist
	}
	return location, nil
}

func (a *FilesystemAuthority) ApplyOperation(ctx context.Context, operation PlannedOperation, validate OperationValidator) (OperationRecord, error) {
	a.writeGate.Lock()
	defer a.writeGate.Unlock()

	if validate == nil {
		return OperationRecord{}, validationError(CodeUnauthorized, "validator", "authority operation requires a validator")
	}
	ctx, _, close, err := a.openStorage(ctx)
	if err != nil {
		return OperationRecord{}, err
	}
	defer close()
	if err := ValidatePlannedOperation(operation, a.registry); err != nil {
		return OperationRecord{}, err
	}
	if !operationIDPattern.MatchString(operation.Plan.OperationID) {
		return OperationRecord{}, validationError(CodeInvalidPlan, "operation_id", "operation ID is not safe for journal storage")
	}
	if err := a.blockedByOtherOperation(ctx, operation.Plan.OperationID); err != nil {
		return OperationRecord{}, err
	}

	manifest, err := a.loadManifest(ctx, operation.Plan.OperationID)
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
		before, readErr := a.authorityBeforeImages(ctx, operation)
		if readErr != nil {
			return OperationRecord{}, readErr
		}
		if err = validate(ctx, operation, before); err != nil {
			return OperationRecord{}, err
		}
		manifest, err = a.createManifest(ctx, operation)
		if err != nil {
			return OperationRecord{}, err
		}
		return a.resume(ctx, manifest)
	default:
		return OperationRecord{}, err
	}
	before, err := a.journalBeforeImages(ctx, manifest)
	if err != nil {
		return manifest.Record, err
	}
	if err = validate(ctx, manifest.Operation, before); err != nil {
		return manifest.Record, err
	}
	return a.resume(ctx, manifest)
}

func (a *FilesystemAuthority) Operation(ctx context.Context, operationID string) (OperationRecord, error) {
	if !operationIDPattern.MatchString(operationID) {
		return OperationRecord{}, fs.ErrNotExist
	}
	manifest, err := a.loadManifest(ctx, operationID)
	if err != nil {
		return OperationRecord{}, err
	}
	return manifest.Record, nil
}

func (a *FilesystemAuthority) RecoverOperations(ctx context.Context, validate OperationValidator) ([]OperationRecord, error) {
	a.writeGate.Lock()
	defer a.writeGate.Unlock()

	if validate == nil {
		return nil, validationError(CodeUnauthorized, "validator", "authority recovery requires a validator")
	}
	ctx, session, close, err := a.openStorage(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	manifests, err := a.listManifests(ctx)
	if err != nil {
		return nil, err
	}
	var records []OperationRecord
	for _, manifest := range manifests {
		if manifest.Record.State == OperationCompleted && !manifest.Record.DerivedStale {
			continue
		}
		if scope, ok := session.(DocumentStorageScope); ok {
			inScope := 0
			for _, file := range manifest.Files {
				if scope.InScope(file.Path) {
					inScope++
				}
			}
			if inScope == 0 {
				continue
			}
			if inScope != len(manifest.Files) {
				return records, validationError(CodeTransactionBlocked, "scope",
					"recovery requires the complete authenticated operation scope")
			}
		}
		manifest, err = a.loadManifest(ctx, manifest.Record.OperationID)
		if err != nil {
			return records, err
		}
		before, readErr := a.journalBeforeImages(ctx, manifest)
		if readErr != nil {
			return records, readErr
		}
		if err = validate(ctx, manifest.Operation, before); err != nil {
			return records, err
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
	ctx, session, close, err := a.openStorage(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	// 所有 after-image 在创建 journal 前完成加密；同一操作重试复用固定密文。
	storedAfter := make([][]byte, len(operation.Images))
	for index, image := range operation.Images {
		if image.Delete {
			continue
		}
		storedAfter[index], err = session.Encode(image.Path, image.Bytes)
		if err != nil {
			return nil, err
		}
	}
	operationDir := a.operationDir(operation.Plan.OperationID)
	if err := os.MkdirAll(a.transactionsDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(operationDir, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			existing, loadErr := a.loadManifest(ctx, operation.Plan.OperationID)
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
		JournalVersion: 2,
		Operation:      operation,
		ExpectedParent: expectedParent,
		NativeEditor:   operation.nativeEditor,
		Record: OperationRecord{
			OperationID: operation.Plan.OperationID,
			State:       OperationPlanned,
			PlanHash:    operation.PlanHash,
		},
		Files: make([]journalFile, 0, len(operation.Images)),
	}
	for index, image := range operation.Images {
		if image.Delete || image.Auxiliary != nil {
			manifest.JournalVersion = operationJournalVersion
		}
		file := journalFile{
			LogicalID:     image.LogicalID,
			Path:          image.Path,
			Create:        image.Create,
			Delete:        image.Delete,
			AfterSemantic: image.SemanticHash,
			AfterContent:  contentHash(storedAfter[index]),
			AfterRef:      filepath.ToSlash(filepath.Join("after", fmt.Sprintf("%04d", index))),
			StoredAfter:   storedAfter[index],
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
			if err := a.prepare(ctx, manifest); err != nil {
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
			var deletedPaths []string
			for _, file := range manifest.Files {
				paths = append(paths, file.Path)
				if file.Delete {
					deletedPaths = append(deletedPaths, file.Path)
				}
			}
			sha, err := a.committer.Commit(ctx, CommitRequest{
				OperationID:    manifest.Record.OperationID,
				Principal:      manifest.Operation.Plan.Principal,
				Command:        manifest.Operation.Plan.Command,
				PlanHash:       manifest.Record.PlanHash,
				ExpectedParent: manifest.ExpectedParent,
				Paths:          paths,
				DeletedPaths:   deletedPaths,
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
			return manifest.Record, nil
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
	_, err := a.authorityBeforeImages(ctx, operation)
	return err
}

func (a *FilesystemAuthority) authorityBeforeImages(ctx context.Context, operation PlannedOperation) ([][]byte, error) {
	before := make([][]byte, len(operation.Images))
	for index, write := range operation.Plan.Writes {
		image := operation.Images[index]
		var current []byte
		var err error
		if image.Auxiliary != nil {
			current, err = a.LoadPhysicalFile(ctx, image.Path)
		} else if image.Native != nil {
			current, err = a.LoadPhysicalDocument(ctx, image.Path)
		} else {
			current, err = a.Load(ctx, write.LogicalID)
		}
		if image.Create {
			if err == nil {
				return nil, validationError(CodeBaseMismatch, image.Path, "create target already exists")
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			if err = a.CheckCreate(ctx, image); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, validationError(CodeBaseMismatch, image.Path, "base entity cannot be reloaded: "+err.Error())
		}
		hash, err := imageSemanticHash(image, current)
		if err != nil {
			return nil, err
		}
		if hash != write.Before {
			return nil, validationError(CodeBaseMismatch, image.Path, fmt.Sprintf("semantic base hash %q does not match current %q", write.Before, hash))
		}
		if image.Auxiliary != nil {
			if err = authorizeNativeFileChange(image, current); err != nil {
				return nil, err
			}
		} else if image.Native != nil {
			if err = authorizeNativePhysicalChange(image, current); err != nil {
				return nil, err
			}
		} else {
			location, err := a.Resolve(ctx, write.LogicalID)
			if err != nil {
				return nil, err
			}
			if location.Path != image.Path {
				return nil, validationError(CodeBaseMismatch, image.Path, fmt.Sprintf("logical ID moved to %s after planning", location.Path))
			}
		}
		before[index] = current
	}
	return before, nil
}

func (a *FilesystemAuthority) CheckCreate(ctx context.Context, image AfterImage) error {
	if err := ValidateTransactionFilePath(image.Path); err != nil {
		return validationError(CodeInvalidPlan, image.Path, err.Error())
	}
	ctx, session, close, err := a.openStorage(ctx)
	if err != nil {
		return err
	}
	defer close()
	if err = session.Allow(image.Path); err != nil {
		return err
	}
	if err = a.verifyAuthorityLocation(image.Path); err != nil {
		return err
	}
	if _, err := os.Lstat(a.authorityPath(image.Path)); !errors.Is(err, fs.ErrNotExist) {
		return validationError(CodeBaseMismatch, image.Path, "create path is already occupied or unavailable")
	}
	if image.Auxiliary != nil {
		return nil
	}
	return a.verifyCreatePhysicalID(session, image)
}

func (a *FilesystemAuthority) verifyCreatePhysicalID(session DocumentStorageSession, image AfterImage) error {
	root, err := decodeRawObject(image.Bytes)
	if err != nil {
		return err
	}
	var physicalID string
	if err = json.Unmarshal(root["ID"], &physicalID); err != nil {
		return err
	}
	return fs.WalkDir(session.Filesystem(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".sy") {
			return nil
		}
		raw, err := fs.ReadFile(session.Filesystem(), path)
		if err != nil {
			return err
		}
		object, err := decodeRawObject(raw)
		if err != nil {
			return err
		}
		var existing string
		if err = json.Unmarshal(object["ID"], &existing); err != nil {
			return err
		}
		if existing == physicalID {
			return validationError(CodeDuplicateID, image.Path, "physical document ID is already occupied")
		}
		return nil
	})
}

func (a *FilesystemAuthority) journalBeforeImages(ctx context.Context, manifest *operationManifest) ([][]byte, error) {
	if manifest.Record.State == OperationPlanned {
		return a.authorityBeforeImages(ctx, manifest.Operation)
	}
	_, session, close, err := a.openStorage(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	before := make([][]byte, len(manifest.Files))
	for index, file := range manifest.Files {
		if file.Create {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(a.operationDir(manifest.Record.OperationID), file.BeforeRef))
		if err != nil {
			return nil, validationError(CodeTransactionBlocked, file.BeforeRef, "journal before-image is unavailable")
		}
		if contentHash(raw) != file.BeforeContent {
			return nil, validationError(CodeTransactionBlocked, file.BeforeRef, "journal before-image checksum mismatch")
		}
		raw, err = session.Decode(file.Path, raw)
		if err != nil {
			return nil, err
		}
		hash, err := imageSemanticHash(manifest.Operation.Images[index], raw)
		if err != nil || hash != file.BeforeSemantic {
			return nil, validationError(CodeTransactionBlocked, file.BeforeRef, "journal before-image semantic hash mismatch")
		}
		before[index] = raw
	}
	return before, nil
}

func (a *FilesystemAuthority) prepare(ctx context.Context, manifest *operationManifest) error {
	_, session, close, err := a.openStorage(ctx)
	if err != nil {
		return err
	}
	defer close()
	beforeDir := filepath.Join(a.operationDir(manifest.Record.OperationID), "before")
	afterDir := filepath.Join(a.operationDir(manifest.Record.OperationID), "after")
	if err := os.MkdirAll(beforeDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(afterDir, 0o755); err != nil {
		return err
	}
	for index, file := range manifest.Files {
		if err := session.Allow(file.Path); err != nil {
			return err
		}
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
		after := file.StoredAfter
		if manifest.JournalVersion == 1 {
			after = manifest.Operation.Images[index].Bytes
		}
		if err := writeFileSynced(
			filepath.Join(a.operationDir(manifest.Record.OperationID), file.AfterRef),
			after,
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
	index  int
	temp   string
	final  string
	dir    string
	delete bool
}

func (a *FilesystemAuthority) applyAfterImages(manifest *operationManifest) error {
	staged := make([]stagedAuthorityFile, 0, len(manifest.Files))
	for index, file := range manifest.Files {
		if err := a.verifyAuthorityLocation(file.Path); err != nil {
			return err
		}
		after, err := os.ReadFile(filepath.Join(a.operationDir(manifest.Record.OperationID), file.AfterRef))
		if err != nil {
			return err
		}
		if got := contentHash(after); got != file.AfterContent {
			return validationError(CodeTransactionBlocked, file.AfterRef,
				fmt.Sprintf("journal after-image checksum mismatch: got %s want %s", got, file.AfterContent))
		}
		final := a.authorityPath(file.Path)
		if !file.Delete {
			if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
				return err
			}
		}
		current, readErr := os.ReadFile(final)
		if file.Delete {
			if errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return readErr
			}
			if contentHash(current) != file.BeforeContent {
				return validationError(CodeTransactionBlocked, file.Path, "delete target differs from journal before-image")
			}
			staged = append(staged, stagedAuthorityFile{index: index, final: final, dir: filepath.Dir(final), delete: true})
			continue
		}
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
		var err error
		if stagedFile.delete {
			err = os.Remove(stagedFile.final)
		} else {
			err = os.Rename(stagedFile.temp, stagedFile.final)
		}
		if err != nil {
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
		if file.Delete {
			if !errors.Is(err, fs.ErrNotExist) {
				return validationError(CodeTransactionBlocked, file.Path, "deleted authority path is not absent")
			}
			continue
		}
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
	refs := make([]AuthorityDocumentRef, 0, len(manifest.Files))
	hasNative := false
	for index, file := range manifest.Files {
		image := manifest.Operation.Images[index]
		ref := AuthorityDocumentRef{LogicalID: file.LogicalID, Path: file.Path, Delete: image.Delete}
		if image.Auxiliary != nil {
			hasNative = true
			ref.LogicalID = ""
			ref.Auxiliary = image.Auxiliary.Kind
		} else if image.Native != nil {
			hasNative = true
			ref.LogicalID = ""
			ref.DocumentID = image.Native.DocumentID
		} else {
			ids = append(ids, file.LogicalID)
		}
		refs = append(refs, ref)
	}
	err := a.fire(FaultBeforeEvent, -1)
	if err == nil && a.indexSink != nil {
		if sink, ok := a.indexSink.(documentIndexSink); ok {
			err = sink.InvalidateDocuments(ctx, refs)
		} else if hasNative {
			err = errors.New("index sink cannot refresh the complete native document write set")
		} else {
			err = a.indexSink.Invalidate(ctx, ids)
		}
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

func (a *FilesystemAuthority) blockedByOtherOperation(ctx context.Context, operationID string) error {
	manifests, err := a.listManifests(ctx)
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

func (a *FilesystemAuthority) listManifests(ctx context.Context) ([]*operationManifest, error) {
	entries, err := os.ReadDir(a.transactionsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifests []*operationManifest
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, validationError(CodeTransactionBlocked, entry.Name(), "journal entry crosses a symlink")
		}
		if !entry.IsDir() || !operationIDPattern.MatchString(entry.Name()) {
			continue
		}
		manifest, loadErr := a.readStoredManifest(entry.Name())
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

func (a *FilesystemAuthority) loadManifest(ctx context.Context, operationID string) (*operationManifest, error) {
	manifest, err := a.readStoredManifest(operationID)
	if err != nil {
		return nil, err
	}
	_, session, close, err := a.openStorage(ctx)
	if err != nil {
		return nil, err
	}
	defer close()
	for index, file := range manifest.Files {
		if err = session.Allow(file.Path); err != nil {
			return nil, err
		}
		if manifest.JournalVersion >= 2 && !manifest.Operation.Images[index].Delete {
			manifest.Operation.Images[index].Bytes, err = session.Decode(file.Path, file.StoredAfter)
			if err != nil {
				return nil, err
			}
		}
	}
	if err = ValidatePlannedOperation(manifest.Operation, a.registry); err != nil {
		return nil, validationError(CodeTransactionBlocked, "manifest", "operation manifest failed validation: "+err.Error())
	}
	if manifest.Record.State != OperationPlanned {
		if _, err = a.journalBeforeImages(ctx, manifest); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

// 全局门禁只校验存储字节和计划元数据，不取得其他认证作用域的密钥。
func (a *FilesystemAuthority) readStoredManifest(operationID string) (*operationManifest, error) {
	if err := verifyJournalRef(a.operationDir(operationID), "manifest.json"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(a.operationDir(operationID), "manifest.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if _, dirErr := os.Lstat(a.operationDir(operationID)); dirErr == nil {
				return nil, validationError(CodeTransactionBlocked, "manifest", "existing operation directory has no manifest")
			}
		}
		return nil, err
	}
	var manifest operationManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, validationError(CodeTransactionBlocked, "manifest", "cannot parse operation manifest: "+err.Error())
	}
	if (manifest.JournalVersion < 1 || manifest.JournalVersion > operationJournalVersion) ||
		manifest.Record.OperationID != operationID ||
		manifest.Operation.Plan.OperationID != operationID {
		return nil, validationError(CodeTransactionBlocked, "manifest", "operation manifest identity or version mismatch")
	}
	if manifest.Record.PlanHash != manifest.Operation.PlanHash ||
		len(manifest.Files) != len(manifest.Operation.Images) ||
		len(manifest.Files) != len(manifest.Operation.Plan.Writes) ||
		len(manifest.Files) != len(manifest.Operation.Plan.Base) {
		return nil, validationError(CodeTransactionBlocked, "manifest", "operation manifest plan hash or file count mismatch")
	}
	if err = ValidateChangePlan(manifest.Operation.Plan); err != nil ||
		ComputePlanHash(manifest.Operation.Plan, manifest.Operation.Images) != manifest.Record.PlanHash {
		return nil, validationError(CodeTransactionBlocked, "manifest", "operation plan metadata checksum mismatch")
	}
	if manifest.NativeEditor != (manifest.Operation.Plan.Command == NativeEditCommand) {
		return nil, validationError(CodeTransactionBlocked, "manifest", "native operation admission is invalid")
	}
	manifest.Operation.nativeEditor = manifest.NativeEditor
	if manifest.JournalVersion < 3 {
		for _, image := range manifest.Operation.Images {
			if image.Delete || image.Auxiliary != nil {
				return nil, validationError(CodeTransactionBlocked, "manifest", "delete and auxiliary files require journal v3")
			}
		}
	}
	switch manifest.Record.State {
	case OperationPlanned, OperationPrepared, OperationFilesApplied, OperationGitCommitted, OperationCompleted:
	default:
		return nil, validationError(CodeTransactionBlocked, "state", "operation manifest state is invalid")
	}
	if manifest.JournalVersion >= 2 {
		for index, file := range manifest.Files {
			image := &manifest.Operation.Images[index]
			if len(image.Bytes) != 0 || (!image.Delete && image.Auxiliary == nil && len(file.StoredAfter) == 0) ||
				(image.Delete && len(file.StoredAfter) != 0) || contentHash(file.StoredAfter) != file.AfterContent {
				return nil, validationError(CodeTransactionBlocked, "manifest", "invalid stored after-image")
			}
		}
	}
	if err := verifyJournalRef(a.operationDir(operationID), "state"); err != nil {
		return nil, err
	}
	state, err := os.ReadFile(filepath.Join(a.operationDir(operationID), "state"))
	if err != nil {
		return nil, err
	}
	if OperationState(strings.TrimSpace(string(state))) != manifest.Record.State {
		return nil, validationError(CodeTransactionBlocked, "state", "state file and manifest disagree")
	}
	baseByID := make(map[LogicalID]BaseRef, len(manifest.Operation.Plan.Base))
	for _, base := range manifest.Operation.Plan.Base {
		baseByID[base.LogicalID] = base
	}
	for index := range manifest.Files {
		file := manifest.Files[index]
		image := manifest.Operation.Images[index]
		write := manifest.Operation.Plan.Writes[index]
		if err = ValidateTransactionFilePath(file.Path); err != nil {
			return nil, validationError(CodeTransactionBlocked, "manifest", "journal authority path is invalid")
		}
		expectedAfterRef := fmt.Sprintf("after/%04d", index)
		expectedBeforeRef := ""
		if !image.Create {
			expectedBeforeRef = fmt.Sprintf("before/%04d", index)
		}
		if file.LogicalID != image.LogicalID || file.LogicalID != write.LogicalID ||
			file.Path != image.Path || file.Create != image.Create ||
			file.Create != (baseByID[file.LogicalID].Expected == "absent") ||
			file.Delete != image.Delete || file.Delete != write.Delete || file.AfterSemantic != write.After ||
			file.AfterRef != expectedAfterRef || file.BeforeRef != expectedBeforeRef ||
			(manifest.JournalVersion == 1 && file.AfterContent != imageContentHash(image)) || file.AfterSemantic != image.SemanticHash ||
			file.BeforeSemantic != write.Before || (!image.Create && file.BeforeContent == "") ||
			(image.Create && file.BeforeContent != "") {
			return nil, validationError(CodeTransactionBlocked, "manifest", "journal file metadata does not match operation")
		}
		for _, ref := range []string{file.BeforeRef, file.AfterRef} {
			if ref != "" {
				if err = verifyJournalRef(a.operationDir(operationID), ref); err != nil {
					return nil, err
				}
			}
		}
		after, readErr := os.ReadFile(filepath.Join(a.operationDir(operationID), file.AfterRef))
		if readErr != nil {
			if manifest.Record.State == OperationPlanned && errors.Is(readErr, fs.ErrNotExist) {
				continue
			}
			return nil, validationError(CodeTransactionBlocked, file.AfterRef, "journal after-image is unavailable")
		}
		expectedStored := image.Bytes
		if manifest.JournalVersion >= 2 {
			expectedStored = file.StoredAfter
		}
		if contentHash(after) != file.AfterContent || !bytes.Equal(after, expectedStored) {
			return nil, validationError(CodeTransactionBlocked, file.AfterRef, "journal after-image checksum mismatch")
		}
		if file.BeforeRef != "" && manifest.Record.State != OperationPlanned {
			before, readErr := os.ReadFile(filepath.Join(a.operationDir(operationID), file.BeforeRef))
			if readErr != nil || contentHash(before) != file.BeforeContent {
				return nil, validationError(CodeTransactionBlocked, file.BeforeRef, "journal before-image checksum mismatch")
			}
		}
	}
	return &manifest, nil
}

func verifyJournalRef(dir, ref string) error {
	for _, path := range []string{dir, filepath.Join(dir, filepath.Dir(ref)), filepath.Join(dir, ref)} {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return validationError(CodeTransactionBlocked, ref, "journal reference is unavailable or crosses a symlink")
		}
	}
	return nil
}

func (a *FilesystemAuthority) writeManifest(manifest *operationManifest) error {
	if manifest == nil {
		return errors.New("operation manifest is nil")
	}
	dir := a.operationDir(manifest.Record.OperationID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	persisted := *manifest
	if manifest.JournalVersion >= 2 {
		persisted.Operation.Images = cloneImageMetadata(manifest.Operation.Images)
	}
	body, err := json.MarshalIndent(&persisted, "", "  ")
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

func (a *FilesystemAuthority) verifyAuthorityLocation(rel string) error {
	if err := ValidateTransactionFilePath(rel); err != nil {
		return validationError(CodeInvalidPlan, rel, err.Error())
	}
	current := a.root
	for _, component := range strings.Split(rel, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return validationError(CodeInvalidPlan, rel, "authority path crosses a symlink")
		}
	}
	return nil
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
