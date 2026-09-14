package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HashAlgoPrefix 是 content_hash 的自描述前缀（算法可演进，格式先定死）。
const HashAlgoPrefix = "sha256:"

// ContentHash 计算整文件字节的 content_hash（B3 的比对依据）。
// 只按字节计算：不做换行归一化、不做编码转换，任何一个字节变化都会改变结果。
func ContentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return HashAlgoPrefix + hex.EncodeToString(sum[:])
}

// ErrOutsideVault 表示相对路径逃出了 vault 根（`..` 穿越或绝对路径）。
var ErrOutsideVault = errors.New("路径逃出 vault 根")

// Store 是 vault 的唯一状态写口。零缓存：每次写入前都重新读盘（§2.2 手工路径等价）。
//
// overlay 非 nil 时进入预演模式（见 atomic.go）：所有权威写口只写内存 overlay，
// 实盘与 Git 零变化；overlay 为 nil 时行为与预演层引入前逐字一致。
type Store struct {
	root    string
	overlay *atomicOverlay
}

// New 以 vault 根目录构造 Store。
func New(root string) *Store { return &Store{root: filepath.Clean(root)} }

// Root 返回 vault 根目录。
func (s *Store) Root() string { return s.root }

// Abs 把 vault 内相对路径解析为绝对路径，并拒绝逃出 vault 根的路径。
func (s *Store) Abs(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w：%s", ErrOutsideVault, rel)
	}
	clean := filepath.Clean(filepath.Join(s.root, rel))
	inside := clean == s.root || len(clean) > len(s.root) && clean[:len(s.root)+1] == s.root+string(filepath.Separator)
	if !inside {
		return "", fmt.Errorf("%w：%s", ErrOutsideVault, rel)
	}
	return clean, nil
}

// File 是一次读盘的结果：路径 + 原始字节 + content_hash。
type File struct {
	Rel   string
	Path  string
	Bytes []byte
	Hash  string
}

// Read 读盘并返回内容与 content_hash。进程内不缓存：调用方每次写入前都应重新 Read。
//
// 预演模式下：首次读取来自实盘并保留原始前像，之后同一预演内的读取看到最新
// staged 字节（既包含本 plan 前序写入，也包含预演内新建的文件）。
func (s *Store) Read(rel string) (File, error) {
	abs, err := s.Abs(rel)
	if err != nil {
		return File{}, err
	}
	if s.overlay != nil {
		sf, err := s.overlay.preimage(s, rel)
		if err != nil {
			return File{}, err
		}
		if !sf.exists() {
			return File{}, &os.PathError{Op: "open", Path: abs, Err: os.ErrNotExist}
		}
		return File{Rel: rel, Path: abs,
			Bytes: append([]byte(nil), sf.staged...), Hash: sf.stagedHash}, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return File{}, err
	}
	return File{Rel: rel, Path: abs, Bytes: b, Hash: ContentHash(b)}, nil
}

// Exists 判断 vault 内相对路径是否已存在文件。预演模式下以 overlay 为准
// （首次实盘存在，或预演内已 staged 新建）。
func (s *Store) Exists(rel string) (bool, error) {
	abs, err := s.Abs(rel)
	if err != nil {
		return false, err
	}
	if s.overlay != nil {
		sf, err := s.overlay.preimage(s, rel)
		if err != nil {
			return false, err
		}
		return sf.exists(), nil
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
