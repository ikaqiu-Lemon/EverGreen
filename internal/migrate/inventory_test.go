package migrate

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestColdImportDeterministicManifestParityAndFailClosed(t *testing.T) {
	source := t.TempDir()
	fixtures := map[string]string{
		"sources/s-one.md":                "---\nid: s-one\ntitle: Source\nurl: https://example.org/source\nsaved_at: 2026-10-10T12:00:00Z\n---\n# Source\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n```go\nfmt.Println(1)\n```\n",
		"domains/test/notes/n-one.md":     "---\nid: n-one\nsource: s-one\ncreated_at: 2026-10-10\nupdated_at: 2026-10-10T12:00:00Z\n---\n# Note\n\n## 整理正文\nText.\n\n## 存疑与待验证\n\n## 用户补充\n",
		"domains/test/knowledge/k-one.md": "---\nid: k-one\nstatus: active\ncreated_at: 2026-10-10\nupdated_at: 2026-10-10T12:00:00Z\ntags: [b, a]\ncustom_future: {enabled: true}\nsources:\n  - source: s-one\n    note: n-one\n    rel: support\n    reason: Source establishes the claim\nrelations:\n  - type: supports\n    target: o-two\n    reason: Shared evidence supports the argument\n---\n# Knowledge\n\n## 知识内容\nA fact.\n\n## 解释与依据\nA reason.\n\n## 条件与边界\nA bound.\n\n## 用户补充\nUser bytes.\n\n## 理解自检\nAn open question?\n",
		"domains/test/opinions/o-two.md":  "---\nid: o-two\nstatus: active\nvalidation: pending\ncreated_at: 2026-10-10\nupdated_at: 2026-10-10T12:00:00Z\nsources:\n  - source: s-one\n    note: n-one\n    rel: context\n    reason: Source gives context\n---\n# Opinion\n\n## 观点\nA view.\n\n## 论据与推理\nAn argument.\n\n## 条件与反例\nA counter.\n\n## 用户补充\n\n## 待验证\nTest it.\n",
	}
	for path, raw := range fixtures {
		writeFixture(t, source, path, []byte(raw))
	}
	writeFixture(t, source, "assets/diagram.png", []byte("fixture image bytes"))
	writeFixture(t, source, "sources/s-one.md", []byte(fixtures["sources/s-one.md"]+"\n![diagram](../assets/diagram.png)\n"))
	gitTest(t, source, "init", "-q")
	gitTest(t, source, "add", ".")
	gitTest(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.org", "commit", "-qm", "fixture")
	first, err := Inventory(source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Inventory(source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.Manifest.Quarantine) != 0 {
		t.Fatalf("deterministic inventory failed: %+v", first.Manifest.Quarantine)
	}
	if len(first.Manifest.Entities) != 4 || first.Manifest.Counts["edges"] != 3 {
		t.Fatalf("counts = %+v", first.Manifest.Counts)
	}
	if len(first.Manifest.Assets) != 1 {
		t.Fatal("asset blob not inventoried")
	}
	target := filepath.Join(t.TempDir(), "data")
	if err = ColdImport(source, target, first.Manifest); err != nil {
		t.Fatal(err)
	}
	assetRaw, err := os.ReadFile(filepath.Join(target, first.Manifest.Assets[0].TargetPath))
	if err != nil || string(assetRaw) != "fixture image bytes" {
		t.Fatal("asset blob did not survive cold import")
	}
	parity, err := Parity(source, target, first.Manifest)
	if err != nil || len(parity.Changes) != 0 || parity.Entities != 4 || parity.Locations != 5 {
		t.Fatalf("parity=%+v err=%v", parity, err)
	}
	if err = ColdImport(source, target, first.Manifest); err == nil || !strings.Contains(err.Error(), "EG_MIGRATION_TARGET") {
		t.Fatalf("existing target must be preserved: %v", err)
	}
	archive, err := Export(target)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, entity := range archive.Entities {
		document, err := core.DecodeSY(entity.Document, nil)
		if err != nil {
			t.Fatal(err)
		}
		if document.Envelope.Claim != nil {
			kinds = append(kinds, string(document.Envelope.Claim.ClaimKind))
		}
		if entity.LogicalID == "k-one" && !strings.Contains(string(entity.Document), `"custom_future"`) {
			t.Fatal("unknown field lost")
		}
		if entity.LogicalID == "s-one" && (!strings.Contains(string(entity.Document), "NodeTable") ||
			!strings.Contains(string(entity.Document), "NodeCodeBlock")) {
			t.Fatal("source assets flattened")
		}
	}
	if !reflect.DeepEqual(kinds, []string{"knowledge", "opinion"}) {
		t.Fatalf("kinds=%v", kinds)
	}
	provider, _ := Repository(target)
	if _, err = core.CanonicalExport(context.Background(), provider, nil); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, source, "domains/test/notes/n-one.md", []byte(fixtures["domains/test/notes/n-one.md"]+"new user input\n"))
	rejectTarget := filepath.Join(t.TempDir(), "data")
	if err = ColdImport(source, rejectTarget, first.Manifest); err == nil || !strings.Contains(err.Error(), "EG_MIGRATION_SOURCE_CHANGED") {
		t.Fatalf("source drift must stop import: %v", err)
	}
	if _, err = os.Stat(rejectTarget); !os.IsNotExist(err) {
		t.Fatal("failed migration published a target")
	}
}

func TestMissingEdgeReasonQuarantinesAndBlocksAllImport(t *testing.T) {
	source := t.TempDir()
	writeFixture(t, source, "domains/test/knowledge/k-invalid.md", []byte("---\nid: k-invalid\nstatus: active\nrelations:\n  - type: supports\n    target: k-invalid\n---\n# Invalid\n"))
	gitTest(t, source, "init", "-q")
	gitTest(t, source, "add", ".")
	gitTest(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.org", "commit", "-qm", "fixture")
	prepared, err := Inventory(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Manifest.Quarantine) != 1 {
		raw, _ := json.Marshal(prepared.Manifest)
		t.Fatalf("quarantine missing: %s", raw)
	}
	target := filepath.Join(t.TempDir(), "data")
	if err = ColdImport(source, target, prepared.Manifest); err == nil || !strings.Contains(err.Error(), "EG_MIGRATION_QUARANTINE") {
		t.Fatalf("unsafe import permitted: %v", err)
	}
}

func writeFixture(t *testing.T, root, rel string, raw []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitTest(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
