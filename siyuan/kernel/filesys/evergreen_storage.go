// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package filesys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/88250/lute/ast"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/av"
)

// EvergreenIsEncryptedBox 由 model 注入，配置损坏时也必须按加密笔记本处理。
var EvergreenIsEncryptedBox func(string) bool

// 资源认证复用 model 的既有容器读写器，避免底层存储复制资产格式和名称元数据规则。
var EvergreenDecodeAsset func(boxID, diskName string, key, stored []byte) ([]byte, string, error)
var EvergreenEncodeAsset func(boxID, diskName, originalName string, key, raw []byte) ([]byte, error)

// EvergreenAssetContent 将加密资源原名和正文同时纳入明文 CAS；磁盘继续使用既有资产容器。
type EvergreenAssetContent struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

// EvergreenDocumentStorage 默认只扫描普通笔记本；Boxes 非空时只访问显式选择的笔记本。
// Scope 的生命周期由调用方的响应租约覆盖，存储会额外持文件级密钥租约到事务结束。
type EvergreenDocumentStorage struct {
	Root  string
	Boxes []string
}

type evergreenNotebookScopeKey struct{}

// WithEvergreenNotebookScope freezes the notebook set used by a shared Runtime request.
// An empty set keeps the existing public scope: all ordinary notebooks and global files.
func WithEvergreenNotebookScope(ctx context.Context, boxIDs []string) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	selected := map[string]struct{}{}
	for _, boxID := range boxIDs {
		if !ast.IsNodeIDPattern(boxID) {
			return nil, errors.New("invalid Evergreen notebook scope")
		}
		selected[boxID] = struct{}{}
	}
	boxes := make([]string, 0, len(selected))
	for boxID := range selected {
		boxes = append(boxes, boxID)
	}
	sort.Strings(boxes)
	return context.WithValue(ctx, evergreenNotebookScopeKey{}, boxes), nil
}

func evergreenNotebookScope(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	boxes, exists := ctx.Value(evergreenNotebookScopeKey{}).([]string)
	return append([]string(nil), boxes...), exists
}

type evergreenDocumentSession struct {
	root     string
	keys     map[string][]byte
	releases []func()
	global   bool
}

func (s EvergreenDocumentStorage) Open(ctx context.Context) (evergreencore.DocumentStorageSession, error) {
	boxes := append([]string(nil), s.Boxes...)
	if requestBoxes, scoped := evergreenNotebookScope(ctx); scoped {
		if len(boxes) != 0 && !sameEvergreenNotebookScope(boxes, requestBoxes) {
			return nil, errors.New("request scope does not match fixed Evergreen notebook storage")
		}
		boxes = requestBoxes
	}
	session := &evergreenDocumentSession{root: s.Root, keys: map[string][]byte{}, global: len(boxes) == 0}
	if EvergreenIsEncryptedBox == nil {
		return nil, errors.New("Evergreen notebook encryption policy is unavailable")
	}
	selected := map[string]bool{}
	for _, boxID := range boxes {
		if !ast.IsNodeIDPattern(boxID) {
			return nil, errors.New("invalid Evergreen notebook scope")
		}
		selected[boxID] = true
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	availableBoxes := []string{}
	for _, entry := range entries {
		if entry.IsDir() && ast.IsNodeIDPattern(entry.Name()) &&
			(len(selected) == 0 && !EvergreenIsEncryptedBox(entry.Name()) || selected[entry.Name()]) {
			availableBoxes = append(availableBoxes, entry.Name())
		}
	}
	sort.Strings(availableBoxes)
	for _, boxID := range availableBoxes {
		key, _, release, err := acquireCryptoLease(boxID)
		if err != nil {
			session.Close()
			return nil, err
		}
		if EvergreenIsEncryptedBox(boxID) && (!selected[boxID] || key == nil) {
			release()
			if !selected[boxID] {
				continue
			}
			session.Close()
			return nil, errors.New("encrypted notebook is not unlocked")
		}
		session.keys[boxID] = key
		session.releases = append(session.releases, release)
	}
	for boxID := range selected {
		if _, exists := session.keys[boxID]; !exists {
			session.Close()
			return nil, errors.New("Evergreen notebook scope is unavailable")
		}
	}
	return session, nil
}

func sameEvergreenNotebookScope(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := map[string]struct{}{}
	for _, boxID := range left {
		leftSet[boxID] = struct{}{}
	}
	if len(leftSet) != len(right) {
		return false
	}
	for _, boxID := range right {
		if _, exists := leftSet[boxID]; !exists {
			return false
		}
	}
	return true
}

func (s *evergreenDocumentSession) Close() {
	for index := len(s.releases) - 1; index >= 0; index-- {
		s.releases[index]()
	}
	s.releases = nil
	s.keys = nil
}

func (s *evergreenDocumentSession) Filesystem() fs.FS {
	return evergreenDocumentFS{session: s, root: os.DirFS(s.root)}
}

func (s *evergreenDocumentSession) AuthoritySnapshotScope() string {
	boxes := make([]string, 0, len(s.keys))
	for boxID := range s.keys {
		boxes = append(boxes, boxID)
	}
	sort.Strings(boxes)
	mode := "selected"
	if s.global {
		mode = "global"
	}
	return mode + ":" + strings.Join(boxes, ",")
}

// 恢复岛只按冻结的范围分类；范围内的路径仍由 Allow 完整校验。
func (s *evergreenDocumentSession) InScope(path string) bool {
	boxID, _, _ := strings.Cut(path, "/")
	if _, selected := s.keys[boxID]; selected {
		return true
	}
	return s.global && (boxID == "assets" || boxID == "storage")
}

func (s *evergreenDocumentSession) key(path string) (string, string, []byte, error) {
	if !fs.ValidPath(path) || strings.Contains(path, "\\") {
		return "", "", nil, errors.New("invalid Evergreen document path")
	}
	if err := evergreencore.ValidateTransactionFilePath(path); err != nil {
		return "", "", nil, err
	}
	boxID, relative, ok := strings.Cut(path, "/")
	if !ok || !ast.IsNodeIDPattern(boxID) {
		if !s.global {
			return "", "", nil, errors.New("global file is outside the authenticated Evergreen scope")
		}
		return "", path, nil, nil
	}
	if strings.HasSuffix(relative, ".sy") {
		if err := validateTreePath(boxID, relative); err != nil {
			return "", "", nil, err
		}
	}
	key, scoped := s.keys[boxID]
	if !scoped {
		return "", "", nil, errors.New("document is outside the authenticated Evergreen scope")
	}
	return boxID, relative, key, nil
}

func (s *evergreenDocumentSession) Allow(path string) error {
	_, _, _, err := s.key(path)
	if err != nil {
		return err
	}
	parts := strings.Split(path, "/")
	for index := range parts {
		info, err := os.Lstat(filepath.Join(s.root, filepath.FromSlash(strings.Join(parts[:index+1], "/"))))
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Evergreen document path crosses a symlink")
		}
	}
	return nil
}

func (s *evergreenDocumentSession) Encode(path string, raw []byte) ([]byte, error) {
	if err := s.Allow(path); err != nil {
		return nil, err
	}
	boxID, relative, key, err := s.key(path)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(relative, ".sy"):
		return encryptDataWithDEK(boxID, relative, raw, key)
	case relative == ".siyuan/conf.json" || key == nil:
		return raw, nil
	case strings.HasPrefix(relative, "storage/av/"):
		return av.EncryptAVDataWithDEK(boxID, evergreenAVObject(relative), raw, key)
	default:
		if EvergreenEncodeAsset == nil {
			return nil, errors.New("Evergreen asset encryption policy is unavailable")
		}
		var content EvergreenAssetContent
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&content); err != nil || content.Name == "" {
			return nil, errors.New("encrypted asset write requires its authenticated name and content")
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			return nil, errors.New("encrypted asset write contains trailing data")
		}
		return EvergreenEncodeAsset(boxID, filepath.Base(relative), content.Name, key, content.Content)
	}
}

func (s *evergreenDocumentSession) Decode(path string, raw []byte) ([]byte, error) {
	if err := s.Allow(path); err != nil {
		return nil, err
	}
	boxID, relative, key, err := s.key(path)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasSuffix(relative, ".sy"):
		return decryptDataWithDEK(boxID, relative, raw, key)
	case relative == ".siyuan/conf.json" || key == nil:
		return raw, nil
	case strings.HasPrefix(relative, "storage/av/"):
		return av.DecryptAVDataWithDEK(boxID, evergreenAVObject(relative), raw, key)
	default:
		if EvergreenDecodeAsset == nil {
			return nil, errors.New("Evergreen asset authentication policy is unavailable")
		}
		plain, name, err := EvergreenDecodeAsset(boxID, filepath.Base(relative), key, raw)
		if err != nil {
			return nil, err
		}
		return json.Marshal(EvergreenAssetContent{Name: name, Content: plain})
	}
}

func evergreenAVObject(relative string) string {
	switch path.Base(relative) {
	case "blocks.msgpack":
		return "mirror"
	case "relations.msgpack":
		return "relation"
	default:
		return strings.TrimSuffix(path.Base(relative), ".json")
	}
}

type evergreenDocumentFS struct {
	session *evergreenDocumentSession
	root    fs.FS
}

func (f evergreenDocumentFS) Open(path string) (fs.File, error) {
	if path == "." {
		file, err := f.root.Open(path)
		if err != nil {
			return nil, err
		}
		directory, ok := file.(fs.ReadDirFile)
		if !ok {
			file.Close()
			return nil, errors.New("Evergreen data root is not a directory")
		}
		return &evergreenNotebookDirectory{ReadDirFile: directory, session: f.session}, nil
	}
	boxID, _, _ := strings.Cut(path, "/")
	_, scoped := f.session.keys[boxID]
	if !scoped && !(f.session.global && (boxID == "assets" || boxID == "storage")) {
		return nil, fs.ErrNotExist
	}
	info, err := fs.Stat(f.root, path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return f.root.Open(path)
	}
	raw, err := f.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &evergreenPlainFile{Reader: strings.NewReader(string(raw)), info: info}, nil
}

func (f evergreenDocumentFS) ReadFile(path string) ([]byte, error) {
	if err := f.session.Allow(path); err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(f.root, path)
	if err != nil {
		return nil, err
	}
	return f.session.Decode(path, raw)
}

type evergreenNotebookDirectory struct {
	fs.ReadDirFile
	session *evergreenDocumentSession
	entries []fs.DirEntry
	offset  int
}

func (d *evergreenNotebookDirectory) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.entries == nil {
		all, err := d.ReadDirFile.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		d.entries = []fs.DirEntry{}
		for _, entry := range all {
			if entry.IsDir() {
				if _, scoped := d.session.keys[entry.Name()]; scoped {
					d.entries = append(d.entries, entry)
				}
			}
		}
	}
	end := len(d.entries)
	if n > 0 && end == d.offset {
		return nil, io.EOF
	}
	if n > 0 && end-d.offset > n {
		end = d.offset + n
	}
	entries := d.entries[d.offset:end]
	d.offset = end
	return entries, nil
}

type evergreenPlainFile struct {
	*strings.Reader
	info fs.FileInfo
}

func (f *evergreenPlainFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *evergreenPlainFile) Close() error               { return nil }
