package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestMigrationCLIInventoryAndSemanticDiffAreExecutable(t *testing.T) {
	r := New()
	if code, output, _ := runCLI(t, r, "migrate", "inventory", "--vault", t.TempDir(), "--output",
		filepath.Join(t.TempDir(), "manifest.json"), "--json"); code != 2 {
		t.Fatalf("invalid Git source should fail closed, got %d: %s", code, output)
	}
	archive := core.CanonicalArchive{Spec: core.CanonicalExportSpec, Entities: []core.CanonicalEntity{}}
	raw, _ := json.Marshal(archive)
	path := filepath.Join(t.TempDir(), "archive.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, output, _ := runCLI(t, r, "diff", "--before", path, "--after", path, "--json"); code != 0 {
		t.Fatalf("empty semantic diff should pass, got %d: %s", code, output)
	}
}
