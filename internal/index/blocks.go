package index

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// BlocksDirName is the rebuildable per-Note candidate sidecar directory.
	// It is derived data, not a runtime-reserved entry.
	BlocksDirName = "blocks"

	// BlockSidecarVersion is independent from the SQLite schema version.
	BlockSidecarVersion = 1
)

const (
	BlockHealthHealthy = "healthy"
	BlockHealthMissing = "missing"
	BlockHealthStale   = "stale"
	BlockHealthCorrupt = "corrupt"
)

const (
	BlockReasonNone       = ""
	BlockReasonDirMissing = "blocks_dir_missing"
	BlockReasonMissing    = "blocks_file_missing"
	BlockReasonChanged    = "blocks_changed"
	BlockReasonOrphan     = "blocks_orphan"
	BlockReasonMixed      = "blocks_mixed"
	BlockReasonCorrupt    = "blocks_corrupt"
)

const (
	BlockActionNoop    = "noop"
	BlockActionBuilt   = "built"
	BlockActionSynced  = "synced"
	BlockActionRebuilt = "rebuilt"
)

var (
	blockNoteIDRE = regexp.MustCompile(`^n-[a-z0-9][a-z0-9-]*$`)
	blockKeyRE    = regexp.MustCompile(`^cand-[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	blockHashRE   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// BlockSpan records the exact source offsets supplied by the authoritative
// Markdown parser. All intervals are half-open.
type BlockSpan struct {
	AnchorStart   int `json:"anchor_start"`
	AnchorEnd     int `json:"anchor_end"`
	BoundaryStart int `json:"boundary_start"`
	BoundaryEnd   int `json:"boundary_end"`
	HeadingStart  int `json:"heading_start"`
	HeadingEnd    int `json:"heading_end"`
	ContentEnd    int `json:"content_end"`
}

// BlockCandidate is a read-only projection of one Note candidate. It contains
// no body bytes; payload integrity is represented by PayloadHash.
type BlockCandidate struct {
	Key         string    `json:"key"`
	Kind        string    `json:"kind"`
	Syntax      string    `json:"syntax"`
	Title       string    `json:"title"`
	Status      string    `json:"status"`
	Output      string    `json:"output"`
	PayloadHash string    `json:"payload_hash"`
	Span        BlockSpan `json:"span"`
}

// BlockDiagnostic preserves deterministic candidate parse diagnostics so a
// healthy sidecar and direct Markdown scanning expose the same query facts.
type BlockDiagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// BlockDocument is the complete sidecar for one authoritative Note.
type BlockDocument struct {
	SchemaVersion int               `json:"schema_version"`
	NoteID        string            `json:"note_id"`
	NotePath      string            `json:"note_path"`
	NoteHash      string            `json:"note_hash"`
	Candidates    []BlockCandidate  `json:"candidates"`
	Diagnostics   []BlockDiagnostic `json:"diagnostics"`
}

// BlockChanges is a deterministic Note-ID diff between sidecars and Markdown.
type BlockChanges struct {
	Added     []string
	Modified  []string
	Removed   []string
	Unchanged []string
}

func (c BlockChanges) Empty() bool {
	return len(c.Added) == 0 && len(c.Modified) == 0 && len(c.Removed) == 0
}

// BlockStatus is the independently diagnosable health of .index/blocks.
type BlockStatus struct {
	Health    string
	Code      string
	Reason    string
	Message   string
	Changes   BlockChanges
	Documents []BlockDocument
}

func (s BlockStatus) Healthy() bool { return s.Health == BlockHealthHealthy }

// BlockSyncResult reports a deterministic sidecar synchronization.
type BlockSyncResult struct {
	Action  string
	Before  BlockStatus
	After   BlockStatus
	Changes BlockChanges
	Count   int
}

// BlocksDirPath returns the sidecar directory below a vault root.
func BlocksDirPath(vaultRoot string) string {
	return filepath.Join(DirPath(vaultRoot), BlocksDirName)
}

func blocksDir(indexDir string) string { return filepath.Join(indexDir, BlocksDirName) }

// RenderBlockDocument emits the canonical sidecar bytes.
func RenderBlockDocument(doc BlockDocument) ([]byte, error) {
	if err := validateBlockDocument(doc); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func validateBlockDocument(doc BlockDocument) error {
	if doc.SchemaVersion != BlockSidecarVersion {
		return fmt.Errorf("block sidecar schema_version=%d，期望 %d",
			doc.SchemaVersion, BlockSidecarVersion)
	}
	if !blockNoteIDRE.MatchString(doc.NoteID) {
		return fmt.Errorf("block sidecar note_id 非法：%q", doc.NoteID)
	}
	if err := validateBlockNotePath(doc.NotePath); err != nil {
		return err
	}
	if !blockHashRE.MatchString(doc.NoteHash) {
		return fmt.Errorf("block sidecar note_hash 非法：%q", doc.NoteHash)
	}
	if doc.Candidates == nil || doc.Diagnostics == nil {
		return fmt.Errorf("block sidecar candidates/diagnostics 必须是数组")
	}
	seen := map[string]bool{}
	lastAnchor := -1
	for i, candidate := range doc.Candidates {
		if err := validateBlockCandidate(candidate); err != nil {
			return fmt.Errorf("block sidecar candidates[%d]：%w", i, err)
		}
		if seen[candidate.Key] {
			return fmt.Errorf("block sidecar candidate key 重复：%q", candidate.Key)
		}
		if candidate.Span.AnchorStart <= lastAnchor {
			return fmt.Errorf("block sidecar candidates 未按 source span 严格递增")
		}
		seen[candidate.Key] = true
		lastAnchor = candidate.Span.AnchorStart
	}
	for i, diag := range doc.Diagnostics {
		if strings.TrimSpace(diag.Code) == "" || strings.TrimSpace(diag.Level) == "" ||
			strings.TrimSpace(diag.Path) == "" || strings.TrimSpace(diag.Message) == "" {
			return fmt.Errorf("block sidecar diagnostics[%d] 字段不完整", i)
		}
	}
	if len(doc.Diagnostics) > 0 && len(doc.Candidates) > 0 {
		return fmt.Errorf("block sidecar 含解析诊断时不得同时声称存在 candidates")
	}
	return nil
}

func validateBlockNotePath(notePath string) error {
	if notePath == "" || strings.Contains(notePath, `\`) ||
		!filepath.IsLocal(filepath.FromSlash(notePath)) || path.Clean(notePath) != notePath ||
		!strings.HasSuffix(notePath, ".md") {
		return fmt.Errorf("block sidecar note_path 非法：%q", notePath)
	}
	return nil
}

func validateBlockCandidate(candidate BlockCandidate) error {
	if !blockKeyRE.MatchString(candidate.Key) {
		return fmt.Errorf("key 非法：%q", candidate.Key)
	}
	if candidate.Kind != CardKindKnowledge && candidate.Kind != CardKindOpinion {
		return fmt.Errorf("kind 非法：%q", candidate.Kind)
	}
	if candidate.Syntax != "h3" && candidate.Syntax != "fenced_div" {
		return fmt.Errorf("syntax 非法：%q", candidate.Syntax)
	}
	if strings.TrimSpace(candidate.Title) == "" ||
		candidate.Title != strings.TrimSpace(candidate.Title) ||
		strings.ContainsAny(candidate.Title, "\r\n") {
		return fmt.Errorf("title 必须是无首尾空白的非空单行文本")
	}
	wantStatus := "draft"
	if candidate.Output != "" {
		wantStatus = "materialized"
		wantPrefix := "k-"
		if candidate.Kind == CardKindOpinion {
			wantPrefix = "o-"
		}
		if !strings.HasPrefix(candidate.Output, wantPrefix) {
			return fmt.Errorf("output=%q 与 kind=%s 不一致", candidate.Output, candidate.Kind)
		}
	}
	if candidate.Status != wantStatus {
		return fmt.Errorf("status=%q 与 output=%q 不一致", candidate.Status, candidate.Output)
	}
	if !blockHashRE.MatchString(candidate.PayloadHash) {
		return fmt.Errorf("payload_hash 非法：%q", candidate.PayloadHash)
	}
	s := candidate.Span
	if s.AnchorStart < 0 || s.AnchorEnd <= s.AnchorStart ||
		s.BoundaryStart != s.AnchorEnd || s.BoundaryEnd <= s.BoundaryStart ||
		s.HeadingStart < s.BoundaryStart || s.HeadingEnd <= s.HeadingStart ||
		s.ContentEnd < s.HeadingEnd || s.ContentEnd > s.BoundaryEnd {
		return fmt.Errorf("span 非法：%+v", s)
	}
	return nil
}

func validateBlockDocuments(docs []BlockDocument) error {
	ids := map[string]bool{}
	paths := map[string]bool{}
	for i, doc := range docs {
		if err := validateBlockDocument(doc); err != nil {
			return fmt.Errorf("block sidecar documents[%d]：%w", i, err)
		}
		if ids[doc.NoteID] {
			return fmt.Errorf("block sidecar note_id 重复：%q", doc.NoteID)
		}
		if paths[doc.NotePath] {
			return fmt.Errorf("block sidecar note_path 重复：%q", doc.NotePath)
		}
		ids[doc.NoteID], paths[doc.NotePath] = true, true
	}
	return nil
}

func sortedBlockDocuments(in []BlockDocument) []BlockDocument {
	out := append([]BlockDocument(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].NotePath != out[j].NotePath {
			return out[i].NotePath < out[j].NotePath
		}
		return out[i].NoteID < out[j].NoteID
	})
	return out
}

func blockFileName(noteID string) string { return noteID + ".json" }

func blockDocumentMap(docs []BlockDocument) map[string]BlockDocument {
	out := make(map[string]BlockDocument, len(docs))
	for _, doc := range docs {
		out[doc.NoteID] = doc
	}
	return out
}

func canonicalBlockMap(docs []BlockDocument) (map[string][]byte, error) {
	if err := validateBlockDocuments(docs); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(docs))
	for _, doc := range docs {
		raw, err := RenderBlockDocument(doc)
		if err != nil {
			return nil, err
		}
		out[doc.NoteID] = raw
	}
	return out, nil
}

var errBlocksMissing = errors.New("block sidecar directory missing")

func readBlockDocuments(indexDir string) ([]BlockDocument, error) {
	dir := blocksDir(indexDir)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errBlocksMissing
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%s 必须是普通目录且不得为 symlink", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	docs := make([]BlockDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".json") {
			return nil, fmt.Errorf("block sidecar 含非法条目 %q", entry.Name())
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		doc, err := decodeBlockDocument(raw)
		if err != nil {
			return nil, fmt.Errorf("block sidecar %s：%w", entry.Name(), err)
		}
		if entry.Name() != blockFileName(doc.NoteID) {
			return nil, fmt.Errorf("block sidecar 文件名 %q 与 note_id=%q 不一致",
				entry.Name(), doc.NoteID)
		}
		docs = append(docs, doc)
	}
	docs = sortedBlockDocuments(docs)
	if err := validateBlockDocuments(docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func decodeBlockDocument(raw []byte) (BlockDocument, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc BlockDocument
	if err := dec.Decode(&doc); err != nil {
		return BlockDocument{}, fmt.Errorf("JSON 非法：%w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return BlockDocument{}, fmt.Errorf("JSON 后含多余数据")
	}
	if err := validateBlockDocument(doc); err != nil {
		return BlockDocument{}, err
	}
	canonical, err := RenderBlockDocument(doc)
	if err != nil {
		return BlockDocument{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return BlockDocument{}, fmt.Errorf("JSON 不是 canonical 编码")
	}
	return doc, nil
}

// CheckBlocks compares the sidecar directory with a projection freshly
// derived from authoritative Markdown. allowExtra is used by domain-scoped
// context queries, where sidecars for other domains are outside the query.
func CheckBlocks(indexDir string, expected []BlockDocument, allowExtra bool) BlockStatus {
	if err := validateBlockDocuments(expected); err != nil {
		return blockCorrupt("待对账的权威 sidecar 投影非法：%v", err)
	}
	actual, err := readBlockDocuments(indexDir)
	if errors.Is(err, errBlocksMissing) {
		return BlockStatus{
			Health: BlockHealthMissing, Code: CodeIndexMissing,
			Reason: BlockReasonDirMissing,
			Message: fmt.Sprintf("%s/%s 不存在；candidate 查询将回落权威 Markdown，"+
				"运行 eg index build|sync 可重建", DirName, BlocksDirName),
			Changes: BlockChanges{Added: blockIDs(expected)},
		}
	}
	if err != nil {
		return blockCorrupt("block sidecar 不可用：%v；candidate 查询将回落权威 Markdown", err)
	}

	wantRaw, _ := canonicalBlockMap(expected)
	gotRaw, _ := canonicalBlockMap(actual)
	gotDocs := blockDocumentMap(actual)
	changes := BlockChanges{}
	selected := make([]BlockDocument, 0, len(expected))
	for _, doc := range sortedBlockDocuments(expected) {
		got, ok := gotDocs[doc.NoteID]
		switch {
		case !ok:
			changes.Added = append(changes.Added, doc.NoteID)
		case !bytes.Equal(wantRaw[doc.NoteID], gotRaw[doc.NoteID]):
			changes.Modified = append(changes.Modified, doc.NoteID)
		default:
			changes.Unchanged = append(changes.Unchanged, doc.NoteID)
			selected = append(selected, got)
		}
	}
	if !allowExtra {
		wantDocs := blockDocumentMap(expected)
		for _, doc := range actual {
			if _, ok := wantDocs[doc.NoteID]; !ok {
				changes.Removed = append(changes.Removed, doc.NoteID)
			}
		}
	}
	sort.Strings(changes.Added)
	sort.Strings(changes.Modified)
	sort.Strings(changes.Removed)
	sort.Strings(changes.Unchanged)
	if changes.Empty() {
		return BlockStatus{
			Health: BlockHealthHealthy, Reason: BlockReasonNone,
			Message: fmt.Sprintf("block sidecar 与权威 Markdown 一致：notes=%d", len(expected)),
			Changes: changes, Documents: selected,
		}
	}
	reason := blockChangeReason(changes)
	return BlockStatus{
		Health: BlockHealthStale, Code: CodeIndexStale, Reason: reason,
		Message: fmt.Sprintf("block sidecar 已陈旧（%s）：新增 %d / 修改 %d / 孤儿 %d；"+
			"candidate 查询将回落权威 Markdown，运行 eg index sync 可收敛",
			reason, len(changes.Added), len(changes.Modified), len(changes.Removed)),
		Changes: changes, Documents: actual,
	}
}

func blockCorrupt(format string, args ...interface{}) BlockStatus {
	return BlockStatus{
		Health: BlockHealthCorrupt, Code: CodeIndexCorrupt, Reason: BlockReasonCorrupt,
		Message: fmt.Sprintf(format, args...),
	}
}

func blockChangeReason(changes BlockChanges) string {
	kinds := 0
	if len(changes.Added) > 0 {
		kinds++
	}
	if len(changes.Modified) > 0 {
		kinds++
	}
	if len(changes.Removed) > 0 {
		kinds++
	}
	if kinds > 1 {
		return BlockReasonMixed
	}
	if len(changes.Added) > 0 {
		return BlockReasonMissing
	}
	if len(changes.Removed) > 0 {
		return BlockReasonOrphan
	}
	return BlockReasonChanged
}

func blockIDs(docs []BlockDocument) []string {
	out := make([]string, 0, len(docs))
	for _, doc := range docs {
		out = append(out, doc.NoteID)
	}
	sort.Strings(out)
	return out
}

// SyncBlocks incrementally converges .index/blocks to the supplied complete
// Markdown projection. Missing or structurally corrupt directories are
// replaced wholesale; healthy directories update only changed Note files.
func SyncBlocks(indexDir string, expected []BlockDocument) (*BlockSyncResult, error) {
	if err := validateBlockDocuments(expected); err != nil {
		return nil, err
	}
	before := CheckBlocks(indexDir, expected, false)
	if before.Healthy() {
		return &BlockSyncResult{
			Action: BlockActionNoop, Before: before, After: before,
			Changes: before.Changes, Count: len(expected),
		}, nil
	}
	action := BlockActionSynced
	switch before.Health {
	case BlockHealthMissing:
		action = BlockActionBuilt
		if err := replaceBlockDocuments(indexDir, expected); err != nil {
			return nil, err
		}
	case BlockHealthCorrupt:
		action = BlockActionRebuilt
		if err := replaceBlockDocuments(indexDir, expected); err != nil {
			return nil, err
		}
	default:
		if err := applyBlockChanges(indexDir, expected, before.Changes); err != nil {
			return nil, err
		}
	}
	after := CheckBlocks(indexDir, expected, false)
	if !after.Healthy() {
		return nil, fmt.Errorf("block sidecar 同步后仍不健康：%s / %s",
			after.Health, after.Message)
	}
	return &BlockSyncResult{
		Action: action, Before: before, After: after,
		Changes: before.Changes, Count: len(expected),
	}, nil
}

// ApplyBlockDocuments updates only the supplied Notes in an already healthy
// sidecar. It never creates or repairs a missing/corrupt sidecar; write
// commands must report that derived-layer failure without changing their
// authoritative result.
func ApplyBlockDocuments(indexDir string,
	updates []BlockDocument,
) (*BlockSyncResult, error) {
	if err := validateBlockDocuments(updates); err != nil {
		return nil, err
	}
	actual, err := readBlockDocuments(indexDir)
	if err != nil {
		return nil, fmt.Errorf("block sidecar 不可增量更新：%w", err)
	}
	merged := blockDocumentMap(actual)
	for _, doc := range updates {
		merged[doc.NoteID] = doc
	}
	expected := make([]BlockDocument, 0, len(merged))
	for _, doc := range merged {
		expected = append(expected, doc)
	}
	expected = sortedBlockDocuments(expected)
	if err := validateBlockDocuments(expected); err != nil {
		return nil, err
	}
	before := CheckBlocks(indexDir, expected, false)
	if before.Health == BlockHealthCorrupt || before.Health == BlockHealthMissing {
		return nil, fmt.Errorf("block sidecar 不可增量更新：%s", before.Message)
	}
	if before.Healthy() {
		return &BlockSyncResult{
			Action: BlockActionNoop, Before: before, After: before,
			Changes: before.Changes, Count: len(expected),
		}, nil
	}
	if err := applyBlockChanges(indexDir, expected, before.Changes); err != nil {
		return nil, err
	}
	after := CheckBlocks(indexDir, expected, false)
	if !after.Healthy() {
		return nil, fmt.Errorf("block sidecar 增量更新后仍不健康：%s / %s",
			after.Health, after.Message)
	}
	return &BlockSyncResult{
		Action: BlockActionSynced, Before: before, After: after,
		Changes: before.Changes, Count: len(expected),
	}, nil
}

func replaceBlockDocuments(indexDir string, docs []BlockDocument) error {
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(indexDir, ".blocks-stage-")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = removeTree(staging)
		}
	}()
	for _, doc := range sortedBlockDocuments(docs) {
		raw, err := RenderBlockDocument(doc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(staging, blockFileName(doc.NoteID)), raw, 0o644); err != nil {
			return err
		}
	}
	target := blocksDir(indexDir)
	if _, err := os.Lstat(target); err == nil {
		if err := purgeEntry(target); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func applyBlockChanges(indexDir string, docs []BlockDocument, changes BlockChanges) error {
	dir := blocksDir(indexDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	desired := blockDocumentMap(docs)
	for _, id := range append(append([]string{}, changes.Added...), changes.Modified...) {
		doc, ok := desired[id]
		if !ok {
			return fmt.Errorf("block sidecar diff 引用不存在的 Note %s", id)
		}
		raw, err := RenderBlockDocument(doc)
		if err != nil {
			return err
		}
		if err := writeBlockFile(filepath.Join(dir, blockFileName(id)), raw); err != nil {
			return err
		}
	}
	for _, id := range changes.Removed {
		if err := os.Remove(filepath.Join(dir, blockFileName(id))); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeBlockFile(target string, raw []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".block-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}
