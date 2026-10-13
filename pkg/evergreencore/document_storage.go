package evergreencore

import (
	"context"
	"io/fs"
	"os"
	"strconv"
	"sync"
)

// DocumentStorage 把认证读取、文件加密和密钥生命周期交给宿主；core 只处理明文语义。
// Open 必须冻结本次访问范围，并在 Close 前阻止该范围的密钥被清除。
type DocumentStorage interface {
	Open(context.Context) (DocumentStorageSession, error)
}

type DocumentStorageSession interface {
	// Filesystem 仅暴露当前范围内经过认证的明文 .sy，不能用明文回退处理认证失败。
	Filesystem() fs.FS
	Allow(path string) error
	Encode(path string, plaintext []byte) ([]byte, error)
	Decode(path string, stored []byte) ([]byte, error)
	Close()
}

// DocumentStorageScope 可选地限定恢复岛；仅完全范围外的操作可以暂缓，部分范围必须拒绝。
// 范围外的未完成操作仍参与全局新写门禁，InScope 不替代 Allow 的安全校验。
type DocumentStorageScope interface {
	InScope(path string) bool
}

// DocumentStorageSnapshotScope identifies the exact authenticated visibility
// boundary in which a logical-location snapshot may be reused.
type DocumentStorageSnapshotScope interface {
	AuthoritySnapshotScope() string
}

type plainDocumentStorage struct{ root string }
type plainDocumentSession struct{ root string }

func (s plainDocumentStorage) Open(context.Context) (DocumentStorageSession, error) {
	return plainDocumentSession{root: s.root}, nil
}

func (s plainDocumentSession) Filesystem() fs.FS { return os.DirFS(s.root) }
func (s plainDocumentSession) Allow(path string) error {
	return ValidateTransactionFilePath(path)
}
func (s plainDocumentSession) Encode(path string, raw []byte) ([]byte, error) {
	return raw, s.Allow(path)
}
func (s plainDocumentSession) Decode(path string, raw []byte) ([]byte, error) {
	return raw, s.Allow(path)
}
func (s plainDocumentSession) AuthoritySnapshotScope() string { return "plain:" + s.root }
func (s plainDocumentSession) Close()                         {}

type authorityStorageKey struct{}
type authorityStorageContext struct {
	authority *FilesystemAuthority
	session   DocumentStorageSession
	snapshot  *authorityLocationSnapshot
	scope     string
	cacheable bool
}

type authorityLocationSnapshot struct {
	mu         sync.Mutex
	index      *LogicalIndex
	err        error
	generation uint64
}

func (a *FilesystemAuthority) openStorage(ctx context.Context) (context.Context, DocumentStorageSession, func(), error) {
	if value, ok := ctx.Value(authorityStorageKey{}).(authorityStorageContext); ok && value.authority == a {
		return ctx, value.session, func() {}, nil
	}
	session, err := a.storage.Open(ctx)
	if err != nil {
		return ctx, nil, func() {}, err
	}
	value := authorityStorageContext{
		authority: a,
		session:   session,
		snapshot:  &authorityLocationSnapshot{},
	}
	if scoped, ok := session.(DocumentStorageSnapshotScope); ok {
		value.scope = scoped.AuthoritySnapshotScope()
		value.cacheable = value.scope != ""
	}
	return context.WithValue(ctx, authorityStorageKey{}, value), session, session.Close, nil
}

func (a *FilesystemAuthority) openAuthoritySnapshot(ctx context.Context) (context.Context, func(), error) {
	ctx, _, close, err := a.openStorage(ctx)
	return ctx, close, err
}

func (a *FilesystemAuthority) locationIndex(
	ctx context.Context,
	session DocumentStorageSession,
) (*LogicalIndex, error) {
	value, ok := ctx.Value(authorityStorageKey{}).(authorityStorageContext)
	if !ok || value.authority != a || value.snapshot == nil {
		return ScanFS(session.Filesystem(), a.registry)
	}
	value.snapshot.mu.Lock()
	defer value.snapshot.mu.Unlock()
	if value.snapshot.index == nil && value.snapshot.err == nil {
		for {
			generation := a.authorityGeneration.Load()
			value.snapshot.index, value.snapshot.err = ScanFS(session.Filesystem(), a.registry)
			if value.snapshot.err != nil || generation == a.authorityGeneration.Load() {
				value.snapshot.generation = generation
				break
			}
		}
	}
	return value.snapshot.index, value.snapshot.err
}

func (a *FilesystemAuthority) invalidateLocationSnapshot(ctx context.Context) {
	value, ok := ctx.Value(authorityStorageKey{}).(authorityStorageContext)
	if !ok || value.authority != a || value.snapshot == nil {
		return
	}
	value.snapshot.mu.Lock()
	value.snapshot.index = nil
	value.snapshot.err = nil
	value.snapshot.generation = 0
	value.snapshot.mu.Unlock()
}

func (a *FilesystemAuthority) retainLocationSnapshot(ctx context.Context, planHash string) {
	value, ok := ctx.Value(authorityStorageKey{}).(authorityStorageContext)
	if !ok || value.authority != a || value.snapshot == nil || !value.cacheable {
		return
	}
	value.snapshot.mu.Lock()
	index, snapshotErr, generation := value.snapshot.index, value.snapshot.err, value.snapshot.generation
	value.snapshot.mu.Unlock()
	if index == nil || snapshotErr != nil {
		return
	}
	a.retainedSnapshotMu.Lock()
	a.retainedSnapshot = index
	a.retainedPlanHash = planHash
	a.retainedScope = value.scope
	a.retainedGeneration = generation
	a.retainedSnapshotMu.Unlock()
}

func (a *FilesystemAuthority) consumeLocationSnapshot(ctx context.Context, planHash string) {
	value, ok := ctx.Value(authorityStorageKey{}).(authorityStorageContext)
	if !ok || value.authority != a || value.snapshot == nil || !value.cacheable {
		a.discardRetainedSnapshot()
		return
	}
	a.retainedSnapshotMu.Lock()
	index := a.retainedSnapshot
	generation := a.retainedGeneration
	if a.retainedPlanHash != planHash || a.retainedScope != value.scope ||
		generation != a.authorityGeneration.Load() {
		index = nil
	}
	a.retainedSnapshot = nil
	a.retainedPlanHash = ""
	a.retainedScope = ""
	a.retainedGeneration = 0
	a.retainedSnapshotMu.Unlock()
	if index == nil {
		return
	}
	value.snapshot.mu.Lock()
	value.snapshot.index = index
	value.snapshot.err = nil
	value.snapshot.generation = generation
	value.snapshot.mu.Unlock()
}

func (a *FilesystemAuthority) discardRetainedSnapshot() {
	a.retainedSnapshotMu.Lock()
	a.retainedSnapshot = nil
	a.retainedPlanHash = ""
	a.retainedScope = ""
	a.retainedGeneration = 0
	a.retainedSnapshotMu.Unlock()
}

func (a *FilesystemAuthority) RepositoryGeneration(ctx context.Context) (string, bool, error) {
	_, session, close, err := a.openStorage(ctx)
	if err != nil {
		return "", false, err
	}
	defer close()
	scoped, ok := session.(DocumentStorageSnapshotScope)
	if !ok || scoped.AuthoritySnapshotScope() == "" {
		return "", false, nil
	}
	return scoped.AuthoritySnapshotScope() + ":" +
		strconv.FormatUint(a.authorityGeneration.Load(), 10), true, nil
}

// NewSYReadRepositoryWithStorage 使用与事务宿主相同的认证读取范围；不会自行打开加密文件。
func NewSYReadRepositoryWithStorage(ctx context.Context, storage DocumentStorage, registry *Registry) (*SYReadRepository, error) {
	if registry == nil {
		registry = DefaultRegistry()
	}
	session, err := storage.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	index, err := scanFS(session.Filesystem(), registry, true)
	if err != nil {
		return nil, err
	}
	return &SYReadRepository{
		locations: index.Locations, documents: index.documents,
		generation: index.generation, registry: registry,
	}, nil
}
