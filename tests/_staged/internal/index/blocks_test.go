package index_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

const blockTestHash = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func blockDocument(id, rel, key, title string, anchor int) index.BlockDocument {
	return index.BlockDocument{
		SchemaVersion: index.BlockSidecarVersion,
		NoteID:        id,
		NotePath:      rel,
		NoteHash:      blockTestHash,
		Candidates: []index.BlockCandidate{{
			Key: key, Kind: index.CardKindKnowledge, Syntax: "h3",
			Title: title, Status: "draft", Output: "",
			PayloadHash: blockTestHash,
			Span: index.BlockSpan{
				AnchorStart: anchor, AnchorEnd: anchor + 10,
				BoundaryStart: anchor + 10, BoundaryEnd: anchor + 100,
				HeadingStart: anchor + 10, HeadingEnd: anchor + 30,
				ContentEnd: anchor + 90,
			},
		}},
		Diagnostics: []index.BlockDiagnostic{},
	}
}

func blockTree(t *testing.T, indexDir string) map[string]string {
	t.Helper()
	dir := filepath.Join(indexDir, index.BlocksDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = string(raw)
	}
	return out
}

func TestBlockSidecarsDeterministicBuildSyncAndRebuild(t *testing.T) {
	docs := []index.BlockDocument{
		blockDocument("n-20260922-b", "domains/ai/notes/n-20260922-b.md",
			"cand-b", "B", 120),
		blockDocument("n-20260922-a", "domains/ai/notes/n-20260922-a.md",
			"cand-a", "A", 0),
	}
	dir := filepath.Join(t.TempDir(), index.DirName)
	first, err := index.SyncBlocks(dir, docs)
	if err != nil {
		t.Fatal(err)
	}
	if first.Action != index.BlockActionBuilt || first.Count != 2 {
		t.Fatalf("首次同步回执不对：%+v", first)
	}
	before := blockTree(t, dir)
	second, err := index.SyncBlocks(dir, docs)
	if err != nil {
		t.Fatal(err)
	}
	if second.Action != index.BlockActionNoop ||
		!reflect.DeepEqual(before, blockTree(t, dir)) {
		t.Fatalf("相同输入必须逐字 no-op：%+v", second)
	}

	changed := append([]index.BlockDocument(nil), docs...)
	changed[0].Candidates[0].Title = "B2"
	changed = append(changed, blockDocument(
		"n-20260922-c", "domains/ai/notes/n-20260922-c.md", "cand-c", "C", 240))
	changed = changed[1:]
	synced, err := index.SyncBlocks(dir, changed)
	if err != nil {
		t.Fatal(err)
	}
	if synced.Action != index.BlockActionSynced ||
		strings.Join(synced.Changes.Added, ",") != "n-20260922-c" ||
		strings.Join(synced.Changes.Removed, ",") != "n-20260922-b" {
		t.Fatalf("增量 sidecar diff 不对：%+v", synced)
	}
	if status := index.CheckBlocks(dir, changed, false); !status.Healthy() {
		t.Fatalf("同步后应 healthy：%+v", status)
	}

	snap := sampleSnapshot()
	snap.Blocks = changed
	if _, err := index.Rebuild(dir, snap, fixedOptions()); err != nil {
		t.Fatal(err)
	}
	rebuilt := blockTree(t, dir)
	if err := os.RemoveAll(filepath.Join(dir, index.BlocksDirName)); err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(dir, snap, fixedOptions()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rebuilt, blockTree(t, dir)) {
		t.Fatal("删除整个 blocks/ 后重建的 sidecar 字节不一致")
	}
	if len(index.TableNames()) != 6 || len(index.MetaKeys()) != 6 {
		t.Fatalf("sidecar 不得改变 SQLite 封闭集合：tables=%d meta=%d",
			len(index.TableNames()), len(index.MetaKeys()))
	}
}

func TestBlockSidecarDetectsMissingStaleCorruptAndOrphan(t *testing.T) {
	doc := blockDocument("n-20260922-a", "domains/ai/notes/n-20260922-a.md",
		"cand-a", "权威标题", 0)
	dir := filepath.Join(t.TempDir(), index.DirName)
	missing := index.CheckBlocks(dir, []index.BlockDocument{doc}, false)
	if missing.Health != index.BlockHealthMissing ||
		missing.Code != index.CodeIndexMissing {
		t.Fatalf("缺失 sidecar 诊断不对：%+v", missing)
	}
	if _, err := index.SyncBlocks(dir, []index.BlockDocument{doc}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, index.BlocksDirName, doc.NoteID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tampered index.BlockDocument
	if err := json.Unmarshal(raw, &tampered); err != nil {
		t.Fatal(err)
	}
	tampered.Candidates[0].Title = "被篡改"
	tamperedRaw, err := index.RenderBlockDocument(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tamperedRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	stale := index.CheckBlocks(dir, []index.BlockDocument{doc}, false)
	if stale.Health != index.BlockHealthStale ||
		stale.Code != index.CodeIndexStale ||
		strings.Join(stale.Changes.Modified, ",") != doc.NoteID {
		t.Fatalf("合法 JSON 篡改应判 stale：%+v", stale)
	}

	if err := os.WriteFile(path, []byte("{bad json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	corrupt := index.CheckBlocks(dir, []index.BlockDocument{doc}, false)
	if corrupt.Health != index.BlockHealthCorrupt ||
		corrupt.Code != index.CodeIndexCorrupt {
		t.Fatalf("损坏 sidecar 诊断不对：%+v", corrupt)
	}
	if _, err := index.SyncBlocks(dir, []index.BlockDocument{doc}); err != nil {
		t.Fatal(err)
	}

	orphan := blockDocument("n-20260922-orphan",
		"domains/ai/notes/n-20260922-orphan.md", "cand-orphan", "孤儿", 200)
	if _, err := index.SyncBlocks(dir, []index.BlockDocument{doc, orphan}); err != nil {
		t.Fatal(err)
	}
	status := index.CheckBlocks(dir, []index.BlockDocument{doc}, false)
	if status.Health != index.BlockHealthStale ||
		status.Reason != index.BlockReasonOrphan ||
		strings.Join(status.Changes.Removed, ",") != orphan.NoteID {
		t.Fatalf("孤儿 sidecar 未检出：%+v", status)
	}
	if scoped := index.CheckBlocks(dir, []index.BlockDocument{doc}, true); !scoped.Healthy() {
		t.Fatalf("领域子集对账应忽略其它 Note sidecar：%+v", scoped)
	}
}

func TestBlockSidecarRejectsNonCanonicalAndUnsafeDocuments(t *testing.T) {
	doc := blockDocument("n-20260922-a", "domains/ai/notes/n-20260922-a.md",
		"cand-a", "A", 0)
	raw, err := index.RenderBlockDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if raw[len(raw)-1] != '\n' || !strings.Contains(string(raw), "\"schema_version\": 1") {
		t.Fatalf("canonical JSON 形态不对：%q", raw)
	}

	bad := doc
	bad.NotePath = "../escape.md"
	if _, err := index.RenderBlockDocument(bad); err == nil {
		t.Fatal("逃逸 note_path 必须拒绝")
	}
	bad = doc
	bad.Candidates[0].Output = "o-20260922-wrong"
	bad.Candidates[0].Status = "materialized"
	if _, err := index.RenderBlockDocument(bad); err == nil {
		t.Fatal("kind/output 不一致必须拒绝")
	}
}
