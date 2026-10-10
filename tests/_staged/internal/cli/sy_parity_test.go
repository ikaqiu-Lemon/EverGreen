package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/client"
	"github.com/ikaqiu-Lemon/EverGreen/internal/migrate"
	"github.com/ikaqiu-Lemon/EverGreen/internal/segment"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestSYCLIMigratedMaterializationPreviewCommitRetryAndStale(t *testing.T) {
	t.Setenv(client.KernelURLEnv, "")
	noteRaw, err := os.ReadFile("../../internal/segment/testdata/deepseek-review/note.md")
	if err != nil {
		t.Fatal(err)
	}
	workspaceRaw, err := os.ReadFile("../../internal/segment/testdata/deepseek-review/workspace.md")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := segment.ImportLegacyWorkspace(noteRaw, workspaceRaw)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	notePath := filepath.Join(target, "box", "note.sy")
	if err := os.MkdirAll(filepath.Dir(notePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notePath, imported.SY, 0o600); err != nil {
		t.Fatal(err)
	}
	gitOut(t, target, "init", "-q")
	setGitIdentity(t)
	gitOut(t, target, "add", ".")
	gitOut(t, target, "commit", "-qm", "fixture")
	snapshot, err := core.InspectNoteReview(imported.SY, nil)
	if err != nil {
		t.Fatal(err)
	}
	candidate := snapshot.Candidates[0]
	request := core.ReviewMaterializeRequest{OperationID: "op-migrated-cli", NoteID: snapshot.NoteID,
		CandidateID: candidate.CandidateID, NoteBase: snapshot.SemanticHash, ClaimID: "k-20261010-migrated",
		ClaimPath: "box/20261010123000-clitest.sy", ClaimDocumentID: "20261010123000-clitest", CreatedAt: "2026-10-10T12:30:00Z"}
	requestPath := filepath.Join(t.TempDir(), "request.json")
	requestRaw, _ := json.Marshal(request)
	if err := os.WriteFile(requestPath, requestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	before := gitOut(t, target, "rev-parse", "HEAD")
	code, output, _ := runCLI(t, New(), "materialize", "--backend", "sy", "--vault", target,
		"--request", requestPath, "--user-request", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("materialization preview: %d %s", code, output)
	}
	if gitOut(t, target, "rev-parse", "HEAD") != before {
		t.Fatal("preview committed")
	}
	var envelope struct {
		Data struct {
			Plan core.PlannedMaterialization `json:"plan"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatal(err)
	}
	planRaw, _ := json.Marshal(envelope.Data.Plan.Operation)
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		code, output, _ := runCLI(t, New(), "apply", "--backend", "sy", "--vault", target, "--plan", planPath, "--user-request", "--json")
		if code != 0 {
			t.Fatalf("apply attempt %d: %d %s", attempt, code, output)
		}
	}
	if count := gitOut(t, target, "rev-list", "--count", "HEAD"); count != "2\n" {
		t.Fatalf("apply/retry commit count=%q, want one new commit", count)
	}
	repository, err := core.NewSYReadRepository(target, nil)
	if err != nil {
		t.Fatal(err)
	}
	claimRaw, err := repository.Load(context.Background(), request.ClaimID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := core.DecodeSY(claimRaw, nil)
	if err != nil {
		t.Fatal(err)
	}
	var claimRoot map[string]json.RawMessage
	_ = json.Unmarshal(claimRaw, &claimRoot)
	claimBody, _ := core.MarkdownText(claimRoot["Children"])
	candidateBody, _ := core.MarkdownText(candidate.Payload)
	if string(claimBody) != string(candidateBody) || claim.Envelope.Claim.ClaimKind != candidate.Metadata.ClaimKind {
		t.Fatal("materialization changed reviewed payload or kind")
	}
	afterNote, _ := os.ReadFile(notePath)
	after, err := core.InspectNoteReview(afterNote, nil)
	if err != nil || after.Candidates[0].Metadata.MaterializedClaimID != request.ClaimID {
		t.Fatalf("output mapping missing: %v", err)
	}
	code, output, _ = runCLI(t, New(), "materialize", "--backend", "sy", "--vault", target,
		"--request", requestPath, "--user-request", "--dry-run", "--json")
	if code != 2 {
		t.Fatalf("stale Note base was accepted: %d %s", code, output)
	}
}

func TestSYCLIContextSearchCardAndRelationBehaviorParity(t *testing.T) {
	t.Setenv(client.KernelURLEnv, "")
	source := captureVault(t)
	fixtures := map[string]string{
		"sources/s-20261010-example.md":                      "---\nid: s-20261010-example\ntitle: Storage authority\nurl: https://example.org/source\nsaved_at: 2026-10-10T12:00:00Z\n---\n# Storage authority\n\nSource text.\n",
		"domains/ai-infra/notes/n-20261010-example.md":       "---\nid: n-20261010-example\nsource: s-20261010-example\ntitle: Storage note\ncreated_at: 2026-10-10\nupdated_at: 2026-10-10T12:00:00Z\n---\n# Storage note\n\n## 整理正文\nText.\n\n## 存疑与待验证\n\n## 用户补充\n",
		"domains/ai-infra/knowledge/k-20261010-authority.md": "---\nid: k-20261010-authority\nstatus: active\ntags: [storage, authority]\ncreated_at: 2026-10-10\nupdated_at: 2026-10-10T12:00:00Z\nsources:\n  - source: s-20261010-example\n    note: n-20261010-example\n    rel: support\n    reason: Establishes authority\nrelations:\n  - type: supports\n    target: o-20261010-view\n    reason: Supports the view\n---\n# Storage authority\n\n## 知识内容\nA fact.\n\n## 条件与边界\nA bound.\n\n## 用户补充\nUser text.\n",
		"domains/ai-infra/opinions/o-20261010-view.md":       "---\nid: o-20261010-view\nstatus: active\nvalidation: pending\ntags: [storage]\ncreated_at: 2026-10-10\nupdated_at: 2026-10-10T12:00:00Z\nsources:\n  - source: s-20261010-example\n    note: n-20261010-example\n    rel: context\n    reason: Establishes context\n---\n# Storage view\n\n## 观点\nA view.\n\n## 论据与推理\nAn argument.\n\n## 条件与反例\nA counter.\n\n## 用户补充\n\n## 待验证\nTest it.\n",
	}
	for path, raw := range fixtures {
		absolute := filepath.Join(source, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := migrate.Inventory(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "data")
	if err := migrate.ColdImport(source, target, prepared.Manifest); err != nil {
		t.Fatal(err)
	}
	run := func(backend string, arguments ...string) map[string]any {
		t.Helper()
		vault := source
		if backend == "sy" {
			vault = target
			arguments = append(arguments, "--backend", "sy")
		}
		code, output, _ := runCLI(t, New(), append(arguments, "--vault", vault, "--json")...)
		if code != 0 {
			t.Fatalf("%s %v: code=%d %s", backend, arguments, code, output)
		}
		var envelope map[string]any
		if err := json.Unmarshal([]byte(output), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope["data"].(map[string]any)
	}
	contextArgs := []string{"context", "--note", "n-20261010-example", "--domain", "ai-infra"}
	oldContext, newContext := run("", contextArgs...), run("sy", contextArgs...)
	for _, key := range []string{"notes", "cards", "knowledge_candidates", "opinion_candidates", "draft_candidates"} {
		assertSYParityValue(t, key, oldContext[key], newContext[key])
	}
	oldSearch := run("", "search", "Storage", "--kind", "all", "--limit", "0")
	newSearch := run("sy", "search", "Storage", "--kind", "all", "--limit", "0")
	assertSYParityValue(t, "hits", oldSearch["hits"], newSearch["hits"])
	oldCard := run("", "card", "show", "k-20261010-authority")
	newCard := run("sy", "card", "show", "k-20261010-authority")
	for _, key := range []string{"id", "title", "domain", "status", "tags", "sections", "unknown_sections",
		"sources", "relations_out", "relations_in", "deprecated", "deleted", "unreviewed", "markers"} {
		assertSYParityValue(t, key, oldCard[key], newCard[key])
	}
	for _, id := range []string{"k-20261010-authority", "o-20261010-view"} {
		for _, flags := range [][]string{{"--limit", "0"}, {"--limit", "1", "--offset", "1"},
			{"--replaced-by"}, {"--to", "o-20261010-view"}} {
			arguments := append([]string{"rel", id}, flags...)
			oldRel, newRel := run("", arguments...), run("sy", arguments...)
			for _, key := range []string{"relations_out", "relations_in"} {
				assertSYParityValue(t, key, oldRel[key], newRel[key])
			}
		}
	}
	run("sy", "card", "show", "o-20261010-view")
	missing, _, _ := runCLI(t, New(), "rel", "k-20261010-missing", "--backend", "sy", "--vault", target, "--json")
	if missing != 1 {
		t.Fatalf("missing relation endpoint code=%d, want 1", missing)
	}
	archiveBefore, _ := migrate.Export(target)
	// 创建损坏的派生文件后，读行为和 authority hash 不变。
	if err := os.MkdirAll(filepath.Join(target, ".index"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, ".index", "cache.db"), []byte("corrupt cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertSYParityValue(t, "index independent", newSearch["hits"], run("sy", "search", "Storage", "--kind", "all", "--limit", "0")["hits"])
	archiveAfter, _ := migrate.Export(target)
	diff, err := core.DiffCanonical(archiveBefore, archiveAfter)
	if err != nil || len(diff) != 0 {
		t.Fatalf("read commands changed authority: %v %v", diff, err)
	}
}

func assertSYParityValue(t *testing.T, label string, left, right any) {
	t.Helper()
	var normalize func(any) any
	normalize = func(value any) any {
		switch object := value.(type) {
		case map[string]any:
			result := map[string]any{}
			for key, child := range object {
				if key == "path" {
					continue
				}
				result[key] = normalize(child)
				if key == "tags" {
					tags := result[key].([]any)
					sort.Slice(tags, func(i, j int) bool { return tags[i].(string) < tags[j].(string) })
				}
			}
			return result
		case []any:
			result := []any{}
			for _, child := range object {
				result = append(result, normalize(child))
			}
			return result
		}
		return value
	}
	left, right = normalize(left), normalize(right)
	if label == "tags" {
		for _, value := range []any{left, right} {
			tags := value.([]any)
			sort.Slice(tags, func(i, j int) bool { return tags[i].(string) < tags[j].(string) })
		}
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("%s parity differs:\nold=%+v\nnew=%+v", label, left, right)
	}
}
