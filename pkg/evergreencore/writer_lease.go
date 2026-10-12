package evergreencore

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const WriterLeaseFileName = "writer.lease"

type WriterLeaseRecord struct {
	Mode       string `json:"mode"`
	Nonce      string `json:"nonce"`
	Holder     string `json:"holder"`
	PID        int    `json:"pid"`
	AcquiredAt string `json:"acquired_at"`
}

type LeaseRecoveryEvidence struct {
	ExpectedPID   int    `json:"expected_pid"`
	ExpectedNonce string `json:"expected_nonce"`
}

type WorkspaceWriterLease struct {
	path   string
	lock   *flock.Flock
	record WriterLeaseRecord

	mu       sync.Mutex
	released bool
}

// WorkspaceMaintenanceLease excludes every kernel/offline writer without
// publishing a writer identity. It is used by cold, read-only maintenance
// that must leave the source workspace byte-for-byte unchanged.
type WorkspaceMaintenanceLease struct {
	path          string
	runtimeDir    string
	lock          *flock.Flock
	removeOnClose bool

	mu       sync.Mutex
	released bool
}

func AcquireKernelLease(root, holder string) (*WorkspaceWriterLease, error) {
	return acquireWriterLease(root, "kernel", holder)
}

func AcquireOfflineLease(root, holder string) (*WorkspaceWriterLease, error) {
	return acquireWriterLease(root, "offline", holder)
}

func AcquireWorkspaceMaintenanceLease(root string) (*WorkspaceMaintenanceLease, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace root is required")
	}
	runtimeDir := filepath.Join(root, filepath.FromSlash(RuntimeDirName))
	if info, err := os.Lstat(runtimeDir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, validationError(
				CodeWorkspaceLeaseCorrupt,
				"workspace",
				"the Evergreen runtime directory is not a physical directory",
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if err = os.MkdirAll(runtimeDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(runtimeDir, WriterLeaseFileName)
	existed := false
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, validationError(
				CodeWorkspaceLeaseCorrupt,
				"workspace",
				"the writer lease path is not a physical regular file",
			)
		}
		existed = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	fileLock := flock.New(path, flock.SetPermissions(0o644))
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, validationError(
			CodeWorkspaceWriterActive,
			"workspace",
			"cold maintenance requires every workspace writer to stop",
		)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		_ = fileLock.Unlock()
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = fileLock.Unlock()
		if _, parseErr := parseWriterLeaseRecord(raw); parseErr != nil {
			return nil, validationError(
				CodeWorkspaceLeaseCorrupt,
				"workspace",
				"the unlocked writer lease record is corrupt; preserve it for operator diagnosis",
			)
		}
		return nil, validationError(
			CodeWorkspaceLeaseStale,
			"workspace",
			"an unlocked writer lease record remains; recover it before cold maintenance",
		)
	}
	return &WorkspaceMaintenanceLease{
		path: path, runtimeDir: runtimeDir, lock: fileLock, removeOnClose: !existed,
	}, nil
}

func acquireWriterLease(root, mode, holder string) (*WorkspaceWriterLease, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("workspace root is required")
	}
	runtimeDir := filepath.Join(root, filepath.FromSlash(RuntimeDirName))
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(runtimeDir, WriterLeaseFileName)
	fileLock := flock.New(path, flock.SetPermissions(0o644))
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, validationError(
			CodeWorkspaceWriterActive,
			"workspace",
			"the SiYuan kernel or another offline writer currently holds the workspace writer lease",
		)
	}

	existing, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		_ = fileLock.Unlock()
		return nil, readErr
	}
	if len(strings.TrimSpace(string(existing))) > 0 {
		_ = fileLock.Unlock()
		if _, err = parseWriterLeaseRecord(existing); err != nil {
			return nil, validationError(
				CodeWorkspaceLeaseCorrupt,
				"workspace",
				"the unlocked writer lease record is corrupt; preserve it for operator diagnosis",
			)
		}
		return nil, validationError(
			CodeWorkspaceLeaseStale,
			"workspace",
			"an unlocked writer lease record remains; run explicit recovery before offline writes",
		)
	}
	record := WriterLeaseRecord{
		Mode: mode, Nonce: randomNonce(), Holder: holder, PID: os.Getpid(),
		AcquiredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeLeaseRecord(path, record); err != nil {
		_ = fileLock.Unlock()
		return nil, err
	}
	return &WorkspaceWriterLease{path: path, lock: fileLock, record: record}, nil
}

func RecoverStaleLease(root string, evidence LeaseRecoveryEvidence) (WriterLeaseRecord, error) {
	if strings.TrimSpace(root) == "" {
		return WriterLeaseRecord{}, errors.New("workspace root is required")
	}
	path := filepath.Join(root, filepath.FromSlash(RuntimeDirName), WriterLeaseFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WriterLeaseRecord{}, err
	}
	fileLock := flock.New(path, flock.SetPermissions(0o644))
	locked, err := fileLock.TryLock()
	if err != nil {
		return WriterLeaseRecord{}, err
	}
	if !locked {
		return WriterLeaseRecord{}, validationError(
			CodeWorkspaceWriterActive,
			"workspace",
			"cannot recover a lease while a writer still holds it",
		)
	}
	defer fileLock.Unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return WriterLeaseRecord{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return WriterLeaseRecord{}, validationError(
			CodeWorkspaceLeaseStale,
			"workspace",
			"there is no stale writer lease record to recover",
		)
	}
	record, err := parseWriterLeaseRecord(raw)
	if err != nil {
		return WriterLeaseRecord{}, validationError(
			CodeWorkspaceLeaseCorrupt,
			"workspace",
			"the writer lease record is corrupt; preserve it for operator diagnosis",
		)
	}
	if evidence.ExpectedPID <= 0 || strings.TrimSpace(evidence.ExpectedNonce) == "" {
		return WriterLeaseRecord{}, validationError(
			CodeWorkspaceLeaseStale,
			"workspace",
			"stale lease recovery requires the recorded PID and nonce",
		)
	}
	if evidence.ExpectedPID != record.PID || evidence.ExpectedNonce != record.Nonce {
		return WriterLeaseRecord{}, validationError(
			CodeWorkspaceLeaseStale,
			"workspace",
			"stale lease recovery evidence does not match the current record",
		)
	}
	alive, err := writerProcessAlive(record.PID)
	if err != nil {
		return WriterLeaseRecord{}, validationError(
			CodeWorkspaceWriterActive,
			"workspace",
			fmt.Sprintf("cannot prove writer process %d has exited: %v", record.PID, err),
		)
	}
	if alive {
		return WriterLeaseRecord{}, validationError(
			CodeWorkspaceWriterActive,
			"workspace",
			fmt.Sprintf("writer process %d is still active; lease age cannot authorize recovery", record.PID),
		)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return WriterLeaseRecord{}, err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return WriterLeaseRecord{}, err
	}
	if err = file.Close(); err != nil {
		return WriterLeaseRecord{}, err
	}
	if err = syncDir(filepath.Dir(path)); err != nil {
		return WriterLeaseRecord{}, err
	}
	return record, nil
}

func (l *WorkspaceWriterLease) Record() WriterLeaseRecord {
	if l == nil {
		return WriterLeaseRecord{}
	}
	return l.record
}

func (l *WorkspaceWriterLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	file, err := os.OpenFile(l.path, os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		if syncErr := file.Sync(); syncErr != nil {
			err = syncErr
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
	}
	unlockErr := l.lock.Unlock()
	if err != nil {
		return err
	}
	if unlockErr != nil {
		return unlockErr
	}
	return syncDir(filepath.Dir(l.path))
}

func (l *WorkspaceMaintenanceLease) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	var releaseErr error
	if l.removeOnClose {
		raw, err := os.ReadFile(l.path)
		switch {
		case err == nil && len(raw) == 0:
			releaseErr = os.Remove(l.path)
		case err == nil:
			releaseErr = validationError(
				CodeWorkspaceLeaseCorrupt,
				"workspace",
				"maintenance lease path changed while the lock was held",
			)
		case errors.Is(err, os.ErrNotExist):
		default:
			releaseErr = err
		}
		if releaseErr == nil {
			releaseErr = syncDir(l.runtimeDir)
		}
	}
	unlockErr := l.lock.Unlock()
	if releaseErr != nil {
		return releaseErr
	}
	return unlockErr
}

func writeLeaseRecord(path string, record WriterLeaseRecord) error {
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func parseWriterLeaseRecord(raw []byte) (WriterLeaseRecord, error) {
	var record WriterLeaseRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return WriterLeaseRecord{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("writer lease contains trailing JSON")
		}
		return WriterLeaseRecord{}, err
	}
	if record.Mode != "kernel" && record.Mode != "offline" {
		return WriterLeaseRecord{}, errors.New("writer lease mode is invalid")
	}
	if strings.TrimSpace(record.Nonce) == "" || strings.TrimSpace(record.Holder) == "" || record.PID <= 0 {
		return WriterLeaseRecord{}, errors.New("writer lease identity is incomplete")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.AcquiredAt); err != nil {
		return WriterLeaseRecord{}, errors.New("writer lease acquired_at is invalid")
	}
	return record, nil
}

func randomNonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("pid-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
