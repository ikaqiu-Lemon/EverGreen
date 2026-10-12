package evergreencore

import (
	"context"
	"io/fs"
	"os"
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
func (s plainDocumentSession) Close() {}

type authorityStorageKey struct{}
type authorityStorageContext struct {
	authority *FilesystemAuthority
	session   DocumentStorageSession
}

func (a *FilesystemAuthority) openStorage(ctx context.Context) (context.Context, DocumentStorageSession, func(), error) {
	if value, ok := ctx.Value(authorityStorageKey{}).(authorityStorageContext); ok && value.authority == a {
		return ctx, value.session, func() {}, nil
	}
	session, err := a.storage.Open(ctx)
	if err != nil {
		return ctx, nil, func() {}, err
	}
	return context.WithValue(ctx, authorityStorageKey{}, authorityStorageContext{a, session}), session, session.Close, nil
}

// NewSYReadRepositoryWithStorage 使用与事务宿主相同的认证读取范围；不会自行打开加密文件。
func NewSYReadRepositoryWithStorage(ctx context.Context, storage DocumentStorage, registry *Registry) (*SYReadRepository, error) {
	session, err := storage.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	index, err := ScanFS(session.Filesystem(), registry)
	if err != nil {
		return nil, err
	}
	return &SYReadRepository{storage: storage, locations: index.Locations}, nil
}
