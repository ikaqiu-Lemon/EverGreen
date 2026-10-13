package workspacebackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

const (
	Spec             = "evergreen.workspace-backup/v1"
	ManifestFileName = "backup-manifest.json"
)

var notebookIDPattern = regexp.MustCompile(`^[0-9]{14}-[0-9a-z]{7}$`)

type FileEntry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

type Manifest struct {
	Spec               string      `json:"spec"`
	CreatedAt          string      `json:"created_at"`
	GitHead            string      `json:"git_head"`
	DoctorSpec         string      `json:"doctor_spec"`
	DoctorDigest       string      `json:"doctor_digest,omitempty"`
	ArchiveDigest      string      `json:"archive_digest,omitempty"`
	RequiresHostDoctor bool        `json:"requires_host_doctor"`
	EncryptedNotebooks []string    `json:"encrypted_notebooks"`
	Files              []FileEntry `json:"files"`
}

type CreateOptions struct {
	DataDir   string
	OutputDir string
	CreatedAt time.Time
	Registry  *core.Registry
}

type CreateResult struct {
	Spec               string   `json:"spec"`
	Output             string   `json:"output"`
	FileCount          int      `json:"file_count"`
	TotalBytes         int64    `json:"total_bytes"`
	GitHead            string   `json:"git_head"`
	DoctorDigest       string   `json:"doctor_digest,omitempty"`
	ArchiveDigest      string   `json:"archive_digest,omitempty"`
	RequiresHostDoctor bool     `json:"requires_host_doctor"`
	EncryptedNotebooks []string `json:"encrypted_notebooks"`
}

type RestoreOptions struct {
	InputDir  string
	TargetDir string
	Registry  *core.Registry
}

type RestoreResult struct {
	Spec               string   `json:"spec"`
	Target             string   `json:"target"`
	DataDir            string   `json:"data_dir"`
	FileCount          int      `json:"file_count"`
	TotalBytes         int64    `json:"total_bytes"`
	GitHead            string   `json:"git_head"`
	DoctorDigest       string   `json:"doctor_digest,omitempty"`
	ArchiveDigest      string   `json:"archive_digest,omitempty"`
	Parity             bool     `json:"parity"`
	RequiresHostDoctor bool     `json:"requires_host_doctor"`
	EncryptedNotebooks []string `json:"encrypted_notebooks"`
}

func Create(ctx context.Context, options CreateOptions) (CreateResult, error) {
	dataDir, workspace, confDir, err := sourcePaths(options.DataDir)
	if err != nil {
		return CreateResult{}, err
	}
	if err = validateRequiredWorkspace(dataDir, confDir); err != nil {
		return CreateResult{}, err
	}
	output, err := independentDestination(workspace, options.OutputDir, core.CodeBackupInvalid)
	if err != nil {
		return CreateResult{}, err
	}
	if _, err = os.Lstat(output); err == nil {
		return CreateResult{}, backupError(core.CodeBackupTargetNotEmpty, "output", "backup output must not already exist")
	} else if !errors.Is(err, os.ErrNotExist) {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "output", err.Error())
	}

	lease, err := core.AcquireWorkspaceMaintenanceLease(dataDir)
	if err != nil {
		return CreateResult{}, err
	}
	defer lease.Release()
	if err = checkJournal(ctx, dataDir, options.Registry); err != nil {
		return CreateResult{}, err
	}
	encrypted, err := validateCryptoMaterials(dataDir, confDir)
	if err != nil {
		return CreateResult{}, err
	}
	head, err := gitHead(ctx, dataDir)
	if err != nil {
		return CreateResult{}, err
	}
	var doctor core.WorkspaceDoctorSnapshot
	if len(encrypted) == 0 {
		doctor, err = inspectDoctor(ctx, dataDir, options.Registry)
		if err != nil {
			return CreateResult{}, err
		}
	}

	parent := filepath.Dir(output)
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "output", err.Error())
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(output)+".tmp-")
	if err != nil {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "output", err.Error())
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()

	entries, err := collectWorkspace(dataDir, confDir, stage)
	if err != nil {
		return CreateResult{}, err
	}
	current, err := collectWorkspace(dataDir, confDir, "")
	if err != nil {
		return CreateResult{}, err
	}
	if !reflect.DeepEqual(entries, current) {
		return CreateResult{}, backupError(
			core.CodeBackupChecksum,
			"source",
			"workspace files changed while the cold backup lease was held",
		)
	}
	if currentHead, headErr := gitHead(ctx, dataDir); headErr != nil || currentHead != head {
		return CreateResult{}, backupError(
			core.CodeBackupChecksum,
			"git",
			"Git HEAD changed while the cold backup was being created",
		)
	}
	createdAt := options.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	manifest := Manifest{
		Spec: Spec, CreatedAt: createdAt.Format(time.RFC3339Nano), GitHead: head,
		DoctorSpec: core.WorkspaceDoctorSpec, RequiresHostDoctor: len(encrypted) > 0,
		EncryptedNotebooks: encrypted, Files: entries,
	}
	if len(encrypted) == 0 {
		manifest.DoctorDigest = doctor.Digest
		manifest.ArchiveDigest = doctor.ArchiveDigest
	}
	if err = writeJSONFile(filepath.Join(stage, ManifestFileName), manifest, 0o600); err != nil {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "manifest", err.Error())
	}
	if err = validateArchive(stage, manifest); err != nil {
		return CreateResult{}, err
	}
	if err = syncTreeDirectories(stage); err != nil {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "output", err.Error())
	}
	if err = lease.Release(); err != nil {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "workspace", err.Error())
	}
	if err = os.Rename(stage, output); err != nil {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "output", err.Error())
	}
	published = true
	if err = syncDirectory(parent); err != nil {
		return CreateResult{}, backupError(core.CodeBackupInvalid, "output", err.Error())
	}
	return createResult(output, manifest), nil
}

func Restore(ctx context.Context, options RestoreOptions) (RestoreResult, error) {
	input, err := physicalDirectory(options.InputDir, "input", core.CodeBackupInvalid)
	if err != nil {
		return RestoreResult{}, err
	}
	manifest, err := readManifest(filepath.Join(input, ManifestFileName))
	if err != nil {
		return RestoreResult{}, err
	}
	if err = validateArchive(input, manifest); err != nil {
		return RestoreResult{}, err
	}
	dataDir := filepath.Join(input, "data")
	confDir := filepath.Join(input, "conf")
	if err = validateRequiredWorkspace(dataDir, confDir); err != nil {
		return RestoreResult{}, err
	}
	encrypted, err := validateCryptoMaterials(dataDir, confDir)
	if err != nil {
		return RestoreResult{}, err
	}
	if !reflect.DeepEqual(encrypted, manifest.EncryptedNotebooks) {
		return RestoreResult{}, backupError(
			core.CodeBackupCryptoInvalid,
			"crypto",
			"encrypted notebook set does not match the backup manifest",
		)
	}

	target, existed, targetMode, err := restoreTarget(input, options.TargetDir)
	if err != nil {
		return RestoreResult{}, err
	}
	parent := filepath.Dir(target)
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return RestoreResult{}, backupError(core.CodeBackupInvalid, "target", err.Error())
	}
	stage, err := os.MkdirTemp(parent, "."+filepath.Base(target)+".restore-")
	if err != nil {
		return RestoreResult{}, backupError(core.CodeBackupInvalid, "target", err.Error())
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	entries, err := collectWorkspace(dataDir, confDir, stage)
	if err != nil {
		return RestoreResult{}, err
	}
	if !reflect.DeepEqual(entries, manifest.Files) {
		return RestoreResult{}, backupError(
			core.CodeBackupChecksum,
			"files",
			"restored staging files do not match the backup manifest",
		)
	}
	stagedData, stagedConf := filepath.Join(stage, "data"), filepath.Join(stage, "conf")
	if err = validateRequiredWorkspace(stagedData, stagedConf); err != nil {
		return RestoreResult{}, err
	}
	if _, err = validateCryptoMaterials(stagedData, stagedConf); err != nil {
		return RestoreResult{}, err
	}
	if err = checkJournal(ctx, stagedData, options.Registry); err != nil {
		return RestoreResult{}, err
	}
	head, err := gitHead(ctx, stagedData)
	if err != nil {
		return RestoreResult{}, err
	}
	if head != manifest.GitHead {
		return RestoreResult{}, backupError(core.CodeBackupChecksum, "git", "restored Git HEAD does not match the backup manifest")
	}
	result := restoreResult(target, manifest)
	if !manifest.RequiresHostDoctor {
		doctor, doctorErr := inspectDoctor(ctx, stagedData, options.Registry)
		if doctorErr != nil {
			return RestoreResult{}, doctorErr
		}
		if doctor.Digest != manifest.DoctorDigest || doctor.ArchiveDigest != manifest.ArchiveDigest {
			return RestoreResult{}, backupError(
				core.CodeBackupChecksum,
				"doctor",
				"restored authority does not match the backup doctor and canonical digests",
			)
		}
		result.DoctorDigest = doctor.Digest
		result.ArchiveDigest = doctor.ArchiveDigest
		result.Parity = true
	}
	if err = syncTreeDirectories(stage); err != nil {
		return RestoreResult{}, backupError(core.CodeBackupInvalid, "target", err.Error())
	}
	if existed {
		if err = os.Remove(target); err != nil {
			return RestoreResult{}, backupError(core.CodeBackupTargetNotEmpty, "target", err.Error())
		}
		if err = syncDirectory(parent); err != nil {
			_ = os.Mkdir(target, targetMode)
			return RestoreResult{}, backupError(core.CodeBackupInvalid, "target", err.Error())
		}
	}
	if err = os.Rename(stage, target); err != nil {
		if existed {
			_ = os.Mkdir(target, targetMode)
		}
		return RestoreResult{}, backupError(core.CodeBackupInvalid, "target", err.Error())
	}
	published = true
	if err = syncDirectory(parent); err != nil {
		return RestoreResult{}, backupError(core.CodeBackupInvalid, "target", err.Error())
	}
	return result, nil
}

func sourcePaths(data string) (dataDir, workspace, confDir string, err error) {
	dataDir, err = physicalDirectory(data, "vault", core.CodeBackupInvalid)
	if err != nil {
		return "", "", "", err
	}
	workspace = filepath.Dir(dataDir)
	confDir = filepath.Join(workspace, "conf")
	return dataDir, workspace, confDir, nil
}

func validateRequiredWorkspace(dataDir, confDir string) error {
	for _, required := range []struct {
		path string
		kind string
	}{
		{filepath.Join(dataDir, ".git"), "dir"},
		{filepath.Join(dataDir, filepath.FromSlash(core.RuntimeDirName), core.TransactionsDirName), "dir"},
		{filepath.Join(dataDir, "assets"), "dir"},
		{confDir, "dir"},
		{filepath.Join(confDir, "conf.json"), "file"},
	} {
		info, err := os.Lstat(required.path)
		if err != nil {
			return backupError(core.CodeBackupInvalid, "workspace", fmt.Sprintf("required %s is unavailable: %s", required.path, err))
		}
		if info.Mode()&os.ModeSymlink != 0 ||
			required.kind == "dir" && !info.IsDir() ||
			required.kind == "file" && !info.Mode().IsRegular() {
			return backupError(core.CodeBackupInvalid, "workspace", "required backup component is not physical: "+required.path)
		}
	}
	for _, lock := range []string{
		filepath.Join(dataDir, ".git", "index.lock"),
		filepath.Join(dataDir, ".git", "HEAD.lock"),
	} {
		if _, err := os.Lstat(lock); err == nil {
			return backupError(core.CodeBackupInvalid, "git", "Git lock remains in the source workspace: "+lock)
		} else if !errors.Is(err, os.ErrNotExist) {
			return backupError(core.CodeBackupInvalid, "git", err.Error())
		}
	}
	return nil
}

func checkJournal(ctx context.Context, dataDir string, registry *core.Registry) error {
	authority, err := core.NewFilesystemAuthority(core.FilesystemAuthorityOptions{
		Root: dataDir, Registry: registry,
	})
	if err != nil {
		return backupError(core.CodeBackupInvalid, "journal", err.Error())
	}
	return authority.CheckBackgroundWrite(ctx)
}

func inspectDoctor(ctx context.Context, dataDir string, registry *core.Registry) (core.WorkspaceDoctorSnapshot, error) {
	repository, err := core.NewSYReadRepository(dataDir, registry)
	if err != nil {
		return core.WorkspaceDoctorSnapshot{}, backupError(core.CodeBackupInvalid, "doctor", err.Error())
	}
	snapshot, err := core.InspectWorkspaceDoctor(ctx, repository, registry)
	if err != nil {
		return core.WorkspaceDoctorSnapshot{}, backupError(core.CodeBackupInvalid, "doctor", err.Error())
	}
	return snapshot, nil
}

func collectWorkspace(dataDir, confDir, destination string) ([]FileEntry, error) {
	var entries []FileEntry
	for _, root := range []struct {
		source string
		name   string
	}{
		{dataDir, "data"},
		{confDir, "conf"},
	} {
		collected, err := collectTree(root.source, root.name, destination)
		if err != nil {
			return nil, err
		}
		entries = append(entries, collected...)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func collectTree(source, archiveRoot, destination string) ([]FileEntry, error) {
	var entries []FileEntry
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return backupError(core.CodeBackupInvalid, archiveRoot, walkErr.Error())
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return backupError(core.CodeBackupInvalid, archiveRoot, err.Error())
		}
		rel = filepath.ToSlash(rel)
		archivePath := archiveRoot
		if rel != "." {
			archivePath += "/" + rel
		}
		if archivePath == "data/"+filepath.ToSlash(filepath.Join(core.RuntimeDirName, core.WriterLeaseFileName)) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return backupError(core.CodeBackupInvalid, archivePath, err.Error())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return backupError(core.CodeBackupInvalid, archivePath, "workspace backup refuses symbolic links")
		}
		mode := uint32(info.Mode().Perm())
		target := ""
		if destination != "" {
			target = filepath.Join(destination, filepath.FromSlash(archivePath))
		}
		switch {
		case entry.IsDir():
			entries = append(entries, FileEntry{Path: archivePath, Kind: "dir", Mode: mode})
			if target != "" {
				if err = os.MkdirAll(target, info.Mode().Perm()); err != nil {
					return backupError(core.CodeBackupInvalid, archivePath, err.Error())
				}
				if err = os.Chmod(target, info.Mode().Perm()); err != nil {
					return backupError(core.CodeBackupInvalid, archivePath, err.Error())
				}
			}
		case info.Mode().IsRegular():
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return backupError(core.CodeBackupInvalid, archivePath, readErr.Error())
			}
			sum := sha256.Sum256(raw)
			entries = append(entries, FileEntry{
				Path: archivePath, Kind: "file", Mode: mode, Size: int64(len(raw)),
				SHA256: hex.EncodeToString(sum[:]),
			})
			if target != "" {
				if err = writeFile(target, raw, info.Mode().Perm()); err != nil {
					return backupError(core.CodeBackupInvalid, archivePath, err.Error())
				}
			}
		default:
			return backupError(core.CodeBackupInvalid, archivePath, "workspace backup refuses special files")
		}
		return nil
	})
	return entries, err
}

func validateArchive(root string, manifest Manifest) error {
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if err := validateArchiveLayout(root); err != nil {
		return err
	}
	actual, err := collectWorkspace(filepath.Join(root, "data"), filepath.Join(root, "conf"), "")
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, manifest.Files) {
		return backupError(core.CodeBackupChecksum, "files", firstEntryDifference(manifest.Files, actual))
	}
	return nil
}

func validateArchiveLayout(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return backupError(core.CodeBackupInvalid, "archive", err.Error())
	}
	expected := map[string]string{
		ManifestFileName: "file",
		"data":           "dir",
		"conf":           "dir",
	}
	for _, entry := range entries {
		kind, ok := expected[entry.Name()]
		if !ok {
			return backupError(core.CodeBackupInvalid, "archive", "unexpected archive root entry: "+entry.Name())
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Mode()&os.ModeSymlink != 0 ||
			kind == "dir" && !info.IsDir() ||
			kind == "file" && !info.Mode().IsRegular() {
			return backupError(core.CodeBackupInvalid, "archive", "archive root entry is not physical: "+entry.Name())
		}
		delete(expected, entry.Name())
	}
	if len(expected) != 0 {
		return backupError(core.CodeBackupInvalid, "archive", "backup archive root is incomplete")
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Spec != Spec || manifest.DoctorSpec != core.WorkspaceDoctorSpec {
		return backupError(core.CodeBackupInvalid, "manifest", "unsupported backup or doctor specification")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		return backupError(core.CodeBackupInvalid, "manifest", "backup created_at is invalid")
	}
	if strings.TrimSpace(manifest.GitHead) == "" || len(manifest.Files) == 0 {
		return backupError(core.CodeBackupInvalid, "manifest", "backup Git head or file set is empty")
	}
	if manifest.RequiresHostDoctor != (len(manifest.EncryptedNotebooks) > 0) {
		return backupError(core.CodeBackupInvalid, "manifest", "host doctor requirement does not match encrypted notebook set")
	}
	if !manifest.RequiresHostDoctor &&
		(manifest.DoctorDigest == "" || manifest.ArchiveDigest == "") {
		return backupError(core.CodeBackupInvalid, "manifest", "plain backup is missing doctor or canonical digest")
	}
	for index, file := range manifest.Files {
		if !safeArchivePath(file.Path) ||
			file.Kind != "dir" && file.Kind != "file" ||
			file.Kind == "file" && (file.Size < 0 || len(file.SHA256) != sha256.Size*2) ||
			file.Kind == "dir" && (file.Size != 0 || file.SHA256 != "") {
			return backupError(core.CodeBackupInvalid, "manifest", "backup file entry is invalid: "+file.Path)
		}
		if index > 0 && manifest.Files[index-1].Path >= file.Path {
			return backupError(core.CodeBackupInvalid, "manifest", "backup file entries are not strictly sorted")
		}
	}
	if !sort.StringsAreSorted(manifest.EncryptedNotebooks) {
		return backupError(core.CodeBackupInvalid, "manifest", "encrypted notebook list is not sorted")
	}
	return nil
}

func safeArchivePath(path string) bool {
	if path == "" || strings.Contains(path, `\`) || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != "." && clean != ".." &&
		!strings.HasPrefix(clean, "../") &&
		(clean == "data" || strings.HasPrefix(clean, "data/") ||
			clean == "conf" || strings.HasPrefix(clean, "conf/"))
}

func firstEntryDifference(expected, actual []FileEntry) string {
	limit := len(expected)
	if len(actual) < limit {
		limit = len(actual)
	}
	for index := 0; index < limit; index++ {
		if !reflect.DeepEqual(expected[index], actual[index]) {
			return fmt.Sprintf("backup entry mismatch at %d: expected %+v, actual %+v", index, expected[index], actual[index])
		}
	}
	return fmt.Sprintf("backup entry count mismatch: expected %d, actual %d", len(expected), len(actual))
}

func readManifest(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("backup manifest is not a physical regular file")
		}
		return Manifest{}, backupError(core.CodeBackupInvalid, "manifest", err.Error())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, backupError(core.CodeBackupInvalid, "manifest", err.Error())
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil {
		return Manifest{}, backupError(core.CodeBackupInvalid, "manifest", err.Error())
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, backupError(core.CodeBackupInvalid, "manifest", "backup manifest contains trailing JSON")
	}
	if err = validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func gitHead(ctx context.Context, dataDir string) (string, error) {
	output, err := exec.CommandContext(ctx, "git", "-C", dataDir, "rev-parse", "--verify", "HEAD").CombinedOutput()
	if err != nil {
		return "", backupError(core.CodeBackupInvalid, "git", strings.TrimSpace(string(output)))
	}
	head := strings.TrimSpace(string(output))
	if head == "" {
		return "", backupError(core.CodeBackupInvalid, "git", "Git HEAD is empty")
	}
	return head, nil
}

func physicalDirectory(path, field, code string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", backupError(code, field, field+" directory is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", backupError(code, field, err.Error())
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = errors.New("path is not a physical directory")
		}
		return "", backupError(code, field, err.Error())
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", backupError(code, field, err.Error())
	}
	return resolved, nil
}

func independentDestination(sourceRoot, destination, code string) (string, error) {
	if strings.TrimSpace(destination) == "" {
		return "", backupError(code, "output", "destination directory is required")
	}
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return "", backupError(code, "output", err.Error())
	}
	resolved, err := resolveFuturePath(absolute)
	if err != nil {
		return "", backupError(code, "output", err.Error())
	}
	resolvedSource, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return "", backupError(code, "output", err.Error())
	}
	if pathContains(resolvedSource, resolved) || pathContains(resolved, resolvedSource) {
		return "", backupError(code, "output", "backup and source workspace must be independent directories")
	}
	return resolved, nil
}

func resolveFuturePath(path string) (string, error) {
	ancestor := filepath.Dir(path)
	for {
		info, err := os.Lstat(ancestor)
		switch {
		case err == nil:
			if !info.IsDir() {
				return "", errors.New("destination parent is not a directory")
			}
			resolved, evalErr := filepath.EvalSymlinks(ancestor)
			if evalErr != nil {
				return "", evalErr
			}
			suffix, relErr := filepath.Rel(ancestor, path)
			if relErr != nil {
				return "", relErr
			}
			return filepath.Join(resolved, suffix), nil
		case !errors.Is(err, os.ErrNotExist):
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", errors.New("destination has no existing parent")
		}
		ancestor = parent
	}
}

func restoreTarget(input, target string) (string, bool, fs.FileMode, error) {
	resolved, err := independentDestination(input, target, core.CodeBackupTargetNotEmpty)
	if err != nil {
		return "", false, 0, err
	}
	info, err := os.Lstat(resolved)
	if errors.Is(err, os.ErrNotExist) {
		return resolved, false, 0, nil
	}
	if err != nil {
		return "", false, 0, backupError(core.CodeBackupTargetNotEmpty, "target", err.Error())
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, 0, backupError(core.CodeBackupTargetNotEmpty, "target", "restore target is not an empty physical directory")
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", false, 0, backupError(core.CodeBackupTargetNotEmpty, "target", err.Error())
	}
	if len(entries) != 0 {
		return "", false, 0, backupError(core.CodeBackupTargetNotEmpty, "target", "restore target must be empty")
	}
	return resolved, true, info.Mode().Perm(), nil
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func writeFile(path string, raw []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(path, mode)
}

func writeJSONFile(path string, value any, mode fs.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(raw, '\n'), mode)
}

func syncTreeDirectories(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool {
		return len(directories[i]) > len(directories[j])
	})
	for _, directory := range directories {
		if err = syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err = directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}

func createResult(output string, manifest Manifest) CreateResult {
	return CreateResult{
		Spec: Spec, Output: output, FileCount: fileCount(manifest.Files),
		TotalBytes: totalBytes(manifest.Files), GitHead: manifest.GitHead,
		DoctorDigest: manifest.DoctorDigest, ArchiveDigest: manifest.ArchiveDigest,
		RequiresHostDoctor: manifest.RequiresHostDoctor,
		EncryptedNotebooks: append([]string(nil), manifest.EncryptedNotebooks...),
	}
}

func restoreResult(target string, manifest Manifest) RestoreResult {
	return RestoreResult{
		Spec: Spec, Target: target, DataDir: filepath.Join(target, "data"),
		FileCount: fileCount(manifest.Files), TotalBytes: totalBytes(manifest.Files),
		GitHead: manifest.GitHead, RequiresHostDoctor: manifest.RequiresHostDoctor,
		EncryptedNotebooks: append([]string(nil), manifest.EncryptedNotebooks...),
	}
}

func fileCount(files []FileEntry) int {
	count := 0
	for _, file := range files {
		if file.Kind == "file" {
			count++
		}
	}
	return count
}

func totalBytes(files []FileEntry) int64 {
	var total int64
	for _, file := range files {
		total += file.Size
	}
	return total
}

type argon2Params struct {
	Memory      uint32 `json:"memory"`
	Iterations  uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	KeyLength   uint32 `json:"keyLength"`
}

type notebookCrypto struct {
	Enabled         bool         `json:"enabled"`
	MasterSalt      []byte       `json:"masterSalt"`
	KDFParams       argon2Params `json:"kdfParams"`
	KEKVerifier     []byte       `json:"kekVerifier"`
	VerifierNonce   []byte       `json:"verifierNonce"`
	AutoLockMinutes int          `json:"autoLockMinutes"`
	Spec            int          `json:"spec"`
	BackupID        string       `json:"backupID,omitempty"`
	CreatedAt       int64        `json:"createdAt,omitempty"`
	Checksum        string       `json:"checksum,omitempty"`
	KEKMAC          []byte       `json:"kekMAC,omitempty"`
	HistoryKEKs     [][]byte     `json:"historyKEKs,omitempty"`
}

type boxCrypto struct {
	Spec       int    `json:"spec"`
	WrappedDEK []byte `json:"wrappedDEK"`
	WrapNonce  []byte `json:"wrapNonce"`
	Metadata   []byte `json:"metadata,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
}

type boxConfig struct {
	Encrypted bool       `json:"encrypted"`
	BoxCrypt  *boxCrypto `json:"boxCrypt"`
}

func validateCryptoMaterials(dataDir, confDir string) ([]string, error) {
	rootRaw, err := os.ReadFile(filepath.Join(confDir, "conf.json"))
	if err != nil {
		return nil, cryptoError("conf/conf.json", err.Error())
	}
	var rootFields map[string]json.RawMessage
	if err = json.Unmarshal(rootRaw, &rootFields); err != nil {
		return nil, cryptoError("conf/conf.json", err.Error())
	}
	var configured *notebookCrypto
	if raw := rootFields["notebookCrypto"]; len(raw) != 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var value notebookCrypto
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, cryptoError("conf/conf.json", err.Error())
		}
		configured = &value
	}

	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, cryptoError("data", err.Error())
	}
	var encrypted []string
	for _, entry := range entries {
		if !entry.IsDir() || !notebookIDPattern.MatchString(entry.Name()) {
			continue
		}
		boxDir := filepath.Join(dataDir, entry.Name())
		confPath := filepath.Join(boxDir, ".siyuan", "conf.json")
		backupPath := filepath.Join(boxDir, ".siyuan", "notebook-crypto-backup.json")
		var config boxConfig
		raw, confExists, readErr := readNotebookCryptoSidecar(boxDir, confPath)
		if readErr != nil {
			return nil, cryptoError(entry.Name(), readErr.Error())
		}
		if confExists {
			if err = json.Unmarshal(raw, &config); err != nil {
				return nil, cryptoError(entry.Name(), "notebook conf is invalid: "+err.Error())
			}
		}
		var backup boxCrypto
		raw, backupExists, readErr := readNotebookCryptoSidecar(boxDir, backupPath)
		if readErr != nil {
			return nil, cryptoError(entry.Name(), readErr.Error())
		}
		if backupExists {
			if err = decodeStrict(raw, &backup); err != nil {
				return nil, cryptoError(entry.Name(), "notebook recovery backup is invalid: "+err.Error())
			}
		}
		ciphertext, scanErr := containsCiphertext(boxDir)
		if scanErr != nil {
			return nil, cryptoError(entry.Name(), scanErr.Error())
		}
		isEncrypted := config.Encrypted || backupExists || ciphertext
		if !isEncrypted {
			continue
		}
		encrypted = append(encrypted, entry.Name())
		if !confExists || !config.Encrypted || config.BoxCrypt == nil || !backupExists {
			return nil, cryptoError(entry.Name(), "encrypted notebook is missing conf or notebook recovery backup")
		}
		if err = validateBoxCrypto(config.BoxCrypt); err != nil {
			return nil, cryptoError(entry.Name(), "notebook conf key envelope is invalid: "+err.Error())
		}
		if err = validateBoxCrypto(&backup); err != nil {
			return nil, cryptoError(entry.Name(), "notebook recovery key envelope is invalid: "+err.Error())
		}
		if !reflect.DeepEqual(*config.BoxCrypt, backup) {
			return nil, cryptoError(entry.Name(), "notebook conf and recovery backup identify different key envelopes")
		}
	}
	sort.Strings(encrypted)

	globalPath := filepath.Join(dataDir, ".siyuan", "data-crypto-backup.json")
	globalRaw, globalErr := os.ReadFile(globalPath)
	globalExists := globalErr == nil
	if globalErr != nil && !errors.Is(globalErr, os.ErrNotExist) {
		return nil, cryptoError("data/.siyuan/data-crypto-backup.json", globalErr.Error())
	}
	requiresGlobal := len(encrypted) > 0 || globalExists || configured != nil && notebookCryptoHasIdentity(configured)
	if !requiresGlobal {
		return encrypted, nil
	}
	if configured == nil || !globalExists {
		return nil, cryptoError("crypto", "encrypted data requires matching conf and data crypto recovery backups")
	}
	var backup notebookCrypto
	if err = decodeStrict(globalRaw, &backup); err != nil {
		return nil, cryptoError("data/.siyuan/data-crypto-backup.json", err.Error())
	}
	if err = validateNotebookCrypto(configured); err != nil {
		return nil, cryptoError("conf/conf.json", err.Error())
	}
	if err = validateNotebookCrypto(&backup); err != nil {
		return nil, cryptoError("data/.siyuan/data-crypto-backup.json", err.Error())
	}
	left, right := *configured, backup
	left.AutoLockMinutes, right.AutoLockMinutes = 0, 0
	if !reflect.DeepEqual(left, right) {
		return nil, cryptoError("crypto", "conf and data crypto recovery backups identify different key histories")
	}
	return encrypted, nil
}

func readNotebookCryptoSidecar(boxDir, target string) ([]byte, bool, error) {
	metadata := filepath.Join(boxDir, ".siyuan")
	info, err := os.Lstat(metadata)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, errors.New("notebook metadata path is a symbolic link")
	}
	if info.Mode().IsRegular() {
		return nil, false, nil
	}
	if !info.IsDir() {
		return nil, false, errors.New("notebook metadata path is not a regular file or directory")
	}
	raw, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func notebookCryptoHasIdentity(value *notebookCrypto) bool {
	return value != nil && (value.Enabled || len(value.MasterSalt) > 0 || len(value.KEKVerifier) > 0 ||
		value.BackupID != "" || value.Checksum != "" || len(value.KEKMAC) > 0 || len(value.HistoryKEKs) > 0)
}

func validateNotebookCrypto(value *notebookCrypto) error {
	if value == nil || value.Spec != 1 || len(value.MasterSalt) != 16 ||
		value.KDFParams.Memory == 0 || value.KDFParams.Iterations == 0 ||
		value.KDFParams.Parallelism == 0 || value.KDFParams.KeyLength != 32 ||
		value.BackupID == "" || value.CreatedAt <= 0 || len(value.KEKMAC) != sha256.Size {
		return errors.New("notebook crypto recovery identity is incomplete")
	}
	nonce, err := sencNonce(value.KEKVerifier)
	if err != nil || !bytes.Equal(nonce, value.VerifierNonce) {
		return errors.New("notebook crypto verifier envelope is invalid")
	}
	check := *value
	check.AutoLockMinutes = 0
	check.Checksum = ""
	check.KEKMAC = nil
	raw, err := json.Marshal(check)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	if !strings.EqualFold(value.Checksum, hex.EncodeToString(sum[:])) {
		return errors.New("notebook crypto recovery checksum mismatch")
	}
	return nil
}

func validateBoxCrypto(value *boxCrypto) error {
	if value == nil || value.Spec != 1 || value.CreatedAt <= 0 {
		return errors.New("notebook key envelope is incomplete")
	}
	nonce, err := sencNonce(value.WrappedDEK)
	if err != nil || !bytes.Equal(nonce, value.WrapNonce) {
		return errors.New("wrapped DEK envelope nonce mismatch")
	}
	if _, err = sencNonce(value.Metadata); err != nil {
		return errors.New("encrypted notebook metadata envelope is invalid")
	}
	return nil
}

func sencNonce(raw []byte) ([]byte, error) {
	if len(raw) < 7 || !bytes.Equal(raw[:4], []byte("SENC")) || raw[4] != 1 || raw[5] != 1 {
		return nil, errors.New("invalid SENC envelope")
	}
	size := int(raw[6])
	if size <= 0 || len(raw) < 7+size+16 {
		return nil, errors.New("truncated SENC envelope")
	}
	return append([]byte(nil), raw[7:7+size]...), nil
}

func containsCiphertext(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if found || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("notebook path crosses a symbolic link")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		var prefix [4]byte
		count, readErr := io.ReadFull(file, prefix[:])
		closeErr := file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		found = count == 4 && (bytes.Equal(prefix[:], []byte("SENC")) || bytes.Equal(prefix[:], []byte("SYAE")))
		return nil
	})
	return found, err
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("recovery material contains trailing JSON")
	}
	return nil
}

func backupError(code, path, message string) error {
	return &core.DiagnosticError{Diagnostics: []core.Diagnostic{{
		Code: code, Level: "error", Path: path, Message: message,
	}}}
}

func cryptoError(path, message string) error {
	return backupError(core.CodeBackupCryptoInvalid, path, message)
}
