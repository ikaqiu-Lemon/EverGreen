package evergreencore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

type WorkspaceWriterLease struct {
	path   string
	lock   *flock.Flock
	record WriterLeaseRecord

	mu       sync.Mutex
	released bool
}

func AcquireKernelLease(root, holder string) (*WorkspaceWriterLease, error) {
	return acquireWriterLease(root, "kernel", holder, true)
}

func AcquireOfflineLease(root, holder string) (*WorkspaceWriterLease, error) {
	return acquireWriterLease(root, "offline", holder, false)
}

func acquireWriterLease(root, mode, holder string, replaceStale bool) (*WorkspaceWriterLease, error) {
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
	if !replaceStale && len(strings.TrimSpace(string(existing))) > 0 {
		_ = fileLock.Unlock()
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

func RecoverStaleLease(root string) error {
	path := filepath.Join(root, filepath.FromSlash(RuntimeDirName), WriterLeaseFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	fileLock := flock.New(path, flock.SetPermissions(0o644))
	locked, err := fileLock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return validationError(
			CodeWorkspaceWriterActive,
			"workspace",
			"cannot recover a lease while a writer still holds it",
		)
	}
	defer fileLock.Unlock()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
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

func randomNonce() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("pid-%d-%d", os.Getpid(), time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}
