package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

func TestMigrationDryRunApplyAndIdempotentReplay(t *testing.T) {
	root, manifest := copyMigrationFixture(t)
	before := snapshotTree(t, root, false)

	dry, err := Run(Options{VaultRoot: root, ManifestPath: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if dry.Status != "dry-run" || dry.FilesChanged != 4 || dry.TxnID != "" {
		t.Fatalf("unexpected dry-run report: %+v", dry)
	}
	if got := snapshotTree(t, root, false); !reflect.DeepEqual(got, before) {
		t.Fatal("dry-run changed the Vault")
	}
	for _, file := range dry.Files {
		if file.Action != "noop" && !strings.Contains(file.Diff, "+++ b/"+file.Path) {
			t.Fatalf("missing per-file diff for %s", file.Path)
		}
		if strings.Contains(file.Diff, root) {
			t.Fatalf("diff leaks an absolute Vault path: %s", file.Path)
		}
	}

	oldNote := readTestFile(t, root, "domains/demo/notes/n-20260901-migration.md")
	oldNoteUser, err := store.UserSectionBytes(oldNote)
	if err != nil {
		t.Fatal(err)
	}
	oldCard := readTestFile(t, root, "domains/demo/knowledge/k-20260901-migration-fact.md")
	oldCardUser, err := store.UserSectionBytes(oldCard)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := Run(Options{
		VaultRoot: root, ManifestPath: manifest, Apply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != "applied" || applied.FilesChanged != 4 ||
		applied.TxnID == "" {
		t.Fatalf("unexpected apply report: %+v", applied)
	}
	scan, err := txn.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Entries) != 1 || scan.Entries[0].Intent == nil ||
		scan.Entries[0].Intent.JournalVersion != 1 {
		t.Fatalf("migration must use one journal v1 transaction: %+v", scan.Entries)
	}
	intent := scan.Entries[0].Intent
	if len(intent.Files) != 4 ||
		intent.Files[len(intent.Files)-1].Path != "domains/demo/notes/n-20260901-migration.md" {
		t.Fatalf("write-set must contain all targets with Note last: %+v", intent.Files)
	}

	newNote := readTestFile(t, root, "domains/demo/notes/n-20260901-migration.md")
	_, candidates, coverage, _, err := store.ParseMaterializationNote(newNote)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || !coverage.Finalized || len(coverage.Final) != 3 {
		t.Fatalf("candidate/final coverage shape drifted: candidates=%d coverage=%+v",
			len(candidates), coverage)
	}
	newNoteUser, err := store.UserSectionBytes(newNote)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(newNoteUser, oldNoteUser) {
		t.Fatal("Note open question or user supplement bytes changed")
	}
	if !bytes.Contains(newNote, []byte("![Diagram](https://example.invalid/diagram.png)")) ||
		!bytes.Contains(newNote, []byte("```text\ncode remains exact\n```")) ||
		!bytes.Contains(newNote, []byte("| key | value |")) ||
		!bytes.Contains(newNote, []byte("[^1]: Footnote remains in order.")) {
		t.Fatal("structured Note assets were not preserved")
	}

	newCard := readTestFile(t, root, "domains/demo/knowledge/k-20260901-migration-fact.md")
	newCardUser, err := store.UserSectionBytes(newCard)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(newCardUser, oldCardUser) {
		t.Fatal("Knowledge user supplement bytes changed")
	}
	opinion := readTestFile(t, root, "domains/demo/opinions/o-20260901-migration-opinion.md")
	_, parsedOpinion, err := mdfile.ParseOpinion(opinion)
	if err != nil {
		t.Fatal(err)
	}
	if parsedOpinion.Validation != model.ValidationPending {
		t.Fatalf("new Opinion validation=%s", parsedOpinion.Validation)
	}
	tombstone := readTestFile(t, root,
		"domains/demo/knowledge/k-20260901-legacy-opinion.md")
	_, parsedTombstone, err := mdfile.ParseCard(tombstone)
	if err != nil {
		t.Fatal(err)
	}
	if parsedTombstone.DeletedAt == nil || parsedTombstone.ReplacedBy == nil ||
		string(parsedTombstone.ReplacedBy.Target) != "o-20260901-migration-opinion" ||
		!bytes.Contains(tombstone, []byte("Keep the tombstone supplement.")) {
		t.Fatalf("legacy wrong-type artifact did not become a traceable tombstone: %+v",
			parsedTombstone)
	}

	authorityAfter := snapshotTree(t, filepath.Join(root, "domains"), false)
	replay, err := Run(Options{
		VaultRoot: root, ManifestPath: manifest, Apply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Status != "noop" || replay.FilesChanged != 0 || replay.TxnID != "" {
		t.Fatalf("second apply must be a true no-op: %+v", replay)
	}
	if got := snapshotTree(t, filepath.Join(root, "domains"), false); !reflect.DeepEqual(got, authorityAfter) {
		t.Fatal("idempotent replay changed authoritative bytes")
	}
	secondScan, err := txn.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondScan.Entries) != 1 {
		t.Fatalf("no-op replay created a transaction: %+v", secondScan.Entries)
	}
}

func TestMigrationFailsClosedOnHashDrift(t *testing.T) {
	root, manifest := copyMigrationFixture(t)
	notePath := filepath.Join(root, "domains/demo/notes/n-20260901-migration.md")
	raw, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\nexternal edit\n")...)
	if err := os.WriteFile(notePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root, false)
	report, err := Run(Options{VaultRoot: root, ManifestPath: manifest})
	if err == nil || report != nil {
		t.Fatalf("hash drift must fail closed: report=%+v err=%v", report, err)
	}
	if got := snapshotTree(t, root, false); !reflect.DeepEqual(got, before) {
		t.Fatal("failed dry-run changed the Vault")
	}
}

func TestMigrationApplyRecoversOpenJournalBeforePlanning(t *testing.T) {
	root, manifest := copyMigrationFixture(t)
	rel := "domains/demo/notes/n-20260901-migration.md"
	before := readTestFile(t, root, rel)
	partial := append(append([]byte(nil), before...), []byte("\npartial write\n")...)
	staleID, err := txn.AllocateTxnID(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn.WriteIntent(root, staleID, txn.IntentInput{
		Argv: []string{"fixture-crash"},
		Files: []txn.FileSpec{{
			Path: rel, PreBytes: before, TargetBytes: partial, TargetOp: "fixture",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), partial, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(Options{
		VaultRoot: root, ManifestPath: manifest, Apply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "applied" || report.RecoveredTxn != staleID ||
		report.TxnID == "" || report.TxnID == staleID {
		t.Fatalf("recovery/apply sequencing not reported: %+v", report)
	}
	scan, err := txn.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Open()) != 0 {
		t.Fatalf("migration left an open transaction: %+v", scan.Open())
	}
}

func copyMigrationFixture(t *testing.T) (string, string) {
	t.Helper()
	base := filepath.Join("..", "..", "tests", "fixtures", "e2e", "storage-v3-migration")
	root := t.TempDir()
	copyTree(t, filepath.Join(base, "vault"), root)
	manifest := filepath.Join(base, "manifest.json")
	return root, manifest
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func snapshotTree(t *testing.T, root string, includeDirs bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if includeDirs {
				rel, _ := filepath.Rel(root, path)
				out[filepath.ToSlash(rel)+"/"] = "dir"
			}
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = store.ContentHash(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readTestFile(t *testing.T, root, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReportJSONContainsNoAbsolutePaths(t *testing.T) {
	root, manifest := copyMigrationFixture(t)
	report, err := Run(Options{VaultRoot: root, ManifestPath: manifest})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(root)) || bytes.Contains(raw, []byte(filepath.Dir(manifest))) {
		t.Fatal("report contains an absolute workspace or Vault path")
	}
}
