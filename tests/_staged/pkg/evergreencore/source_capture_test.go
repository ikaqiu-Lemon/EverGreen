package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceCaptureCreatesAndDeduplicatesWithReasonOnlyUpdate(t *testing.T) {
	service, host := sourceCaptureService(t)
	principal := Principal{
		Type: PrincipalAgent, ID: "capture-agent",
		AuthSource: "fixture", RequestReason: "capture cleaned source",
	}
	first := SourceCaptureRequest{
		OperationID: "op-source-first",
		LogicalID:   "s-20261012-original",
		DocumentID:  "20261012120000-src0001",
		Path:        "20000101000000-egvault/20261012120000-src0001.sy",
		URL:         "https://EXAMPLE.com/article/?utm_source=test&b=2&a=1#section",
		Title:       "Original title",
		Body:        "Original cleaned body.\n\nSecond paragraph.\n",
		Reason:      "First capture",
		Domain:      "systems",
		Tags:        []string{"source", "architecture"},
		CapturedAt:  "2026-10-12T12:00:00+08:00",
	}
	created, err := service.PlanSourceCapture(context.Background(), principal, first)
	if err != nil {
		t.Fatal(err)
	}
	if created.Deduped || created.SourceID != first.LogicalID || created.Operation == nil {
		t.Fatalf("new source plan = %+v", created)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: *created.Operation}); err != nil {
		t.Fatal(err)
	}

	duplicate := SourceCaptureRequest{
		OperationID: "op-source-duplicate",
		LogicalID:   "s-20261012-should-not-exist",
		DocumentID:  "20261012120001-src0002",
		Path:        "20000101000000-egvault/20261012120001-src0002.sy",
		URL:         "https://example.com/article?a=1&b=2",
		Title:       "Conflicting new title",
		Body:        "Conflicting replacement body that must not be written.",
		Reason:      "Second capture reason",
		Domain:      "different-domain",
		Tags:        []string{"replacement"},
		CapturedAt:  "2026-10-12T12:01:00+08:00",
	}
	deduped, err := service.PlanSourceCapture(context.Background(), principal, duplicate)
	if err != nil {
		t.Fatal(err)
	}
	if !deduped.Deduped || deduped.MatchedBy != "url" ||
		deduped.SourceID != first.LogicalID || deduped.Operation == nil || !deduped.ReasonAppended {
		t.Fatalf("duplicate source plan = %+v", deduped)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: *deduped.Operation}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.InspectSource(context.Background(), first.LogicalID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Title != first.Title || snapshot.Metadata.URL != first.URL ||
		snapshot.Metadata.Domain != first.Domain || string(snapshot.Body) != first.Body {
		t.Fatalf("duplicate capture rewrote immutable Source: %+v body=%q", snapshot, snapshot.Body)
	}
	if got, want := snapshot.Metadata.Reasons, []string{first.Reason, duplicate.Reason}; !equalStrings(got, want) {
		t.Fatalf("capture reasons = %#v, want %#v", got, want)
	}
	if _, err = os.Stat(filepath.Join(host.Root(), duplicate.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("duplicate capture created a second Source: %v", err)
	}
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("new + duplicate reason commit count = %q", got)
	}

	retry := duplicate
	retry.OperationID = "op-source-idempotent"
	unchanged, err := service.PlanSourceCapture(context.Background(), principal, retry)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.Deduped || unchanged.Operation != nil || unchanged.ReasonAppended {
		t.Fatalf("same reason should be a zero-write retry: %+v", unchanged)
	}
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "3\n" {
		t.Fatalf("zero-write retry changed Git history: %q", got)
	}
}

func TestSourceCaptureDeduplicatesMigratedSourceByTitleWithoutMetadataLoss(t *testing.T) {
	service, host := sourceCaptureService(t)
	legacyMetadata := json.RawMessage(`{
		"url":"https://legacy.example/source",
		"title":"Migrated title",
		"saved_at":"2026-10-01T10:00:00+08:00",
		"reasons":["migration"],
		"domain":"systems",
		"future":{"large":9007199254740993,"nested":[null,{"kept":true}]}
	}`)
	before, err := NewMarkdownDocument("s-20261001-migrated", "Migrated title", []byte("Migrated body stays immutable."),
		&DocumentEnvelope{
			Spec: DocumentSpec,
			Entity: Entity{
				LogicalID: "s-20261001-migrated", EntityType: EntitySource,
				Schema: SourceSchema, SemanticRevision: 1,
			},
			Relations: Relations{Outgoing: []TypedEdge{}},
			Extra:     RawObject{"legacy_metadata": legacyMetadata},
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, host.Root(), "box/migrated.sy", before)
	gitCommand(t, host.Root(), "add", ".")
	gitCommand(t, host.Root(), "commit", "-m", "migrated source")

	principal := Principal{Type: PrincipalUser, ID: "owner", AuthSource: "fixture"}
	planned, err := service.PlanSourceCapture(context.Background(), principal, SourceCaptureRequest{
		OperationID: "op-source-legacy-title", LogicalID: "s-20261012-new",
		DocumentID: "20261012123000-src0001", Path: "box/new.sy",
		URL: "https://different.example/source", Title: "Migrated title",
		Body: "Replacement body must be ignored.", Reason: "follow-up capture",
		CapturedAt: "2026-10-12T12:30:00+08:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !planned.Deduped || planned.MatchedBy != "title" ||
		planned.SourceID != "s-20261001-migrated" || planned.Operation == nil {
		t.Fatalf("legacy title dedup = %+v", planned)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: *planned.Operation}); err != nil {
		t.Fatal(err)
	}
	after, err := host.Load(context.Background(), "s-20261001-migrated")
	if err != nil {
		t.Fatal(err)
	}
	document, err := DecodeSY(after, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, introduced := document.Envelope.Extension["source"]; introduced {
		t.Fatal("legacy Source was silently moved to a new metadata partition")
	}
	metadata, err := decodeRawObject(document.Envelope.Extra["legacy_metadata"])
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	_ = json.Unmarshal(metadata["reasons"], &reasons)
	if !equalStrings(reasons, []string{"migration", "follow-up capture"}) ||
		!semanticValuesEqual(metadata["future"], json.RawMessage(`{"large":9007199254740993,"nested":[null,{"kept":true}]}`)) ||
		!semanticValuesEqual(metadata["title"], json.RawMessage(`"Migrated title"`)) {
		t.Fatalf("legacy metadata was not preserved: %s", metadata)
	}
}

func TestSourceCaptureRechecksDuplicateInsideWriterGate(t *testing.T) {
	service, host := sourceCaptureService(t)
	principal := Principal{Type: PrincipalUser, ID: "owner", AuthSource: "fixture"}
	left := SourceCaptureRequest{
		OperationID: "op-source-race-left", LogicalID: "s-20261012-left",
		DocumentID: "20261012130000-src0001", Path: "box/20261012130000-src0001.sy",
		URL: "https://example.com/same", Title: "Left", Body: "Left source body.",
		Reason: "left", CapturedAt: "2026-10-12T13:00:00+08:00",
	}
	right := SourceCaptureRequest{
		OperationID: "op-source-race-right", LogicalID: "s-20261012-right",
		DocumentID: "20261012130001-src0002", Path: "box/20261012130001-src0002.sy",
		URL: "https://example.com/same", Title: "Right", Body: "Right source body.",
		Reason: "right", CapturedAt: "2026-10-12T13:00:01+08:00",
	}
	leftPlan, err := service.PlanSourceCapture(context.Background(), principal, left)
	if err != nil {
		t.Fatal(err)
	}
	rightPlan, err := service.PlanSourceCapture(context.Background(), principal, right)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: *leftPlan.Operation}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: *rightPlan.Operation}); !HasDiagnostic(err, CodeDuplicateID) {
		t.Fatalf("stale duplicate plan was not rejected at apply: %v", err)
	}
	if _, err = os.Stat(filepath.Join(host.Root(), right.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected duplicate wrote authority: %v", err)
	}
	if _, err = os.Stat(host.operationDir(right.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected duplicate created a journal: %v", err)
	}
	if got := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); got != "2\n" {
		t.Fatalf("rejected duplicate changed Git history: %q", got)
	}
}

func TestSourceCaptureRawPlanCannotRewriteImmutableSource(t *testing.T) {
	before := sourceCaptureFixture(t)
	for _, test := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"body", func(raw []byte) []byte {
			return mutateSYFixture(t, appendSourceReasonFixture(t, raw, "new reason"), "Rewritten body")
		}},
		{"title", func(raw []byte) []byte {
			raw = appendSourceReasonFixture(t, raw, "new reason")
			return mutateAuthorityObject(t, raw, func(root RawObject) {
				root["Properties"] = json.RawMessage(`{"title":"Rewritten title"}`)
			})
		}},
		{"url", func(raw []byte) []byte {
			return mutateSourceMetadataFixture(t, raw, func(metadata RawObject) {
				metadata["url"] = json.RawMessage(`"https://attacker.invalid"`)
				metadata["reasons"] = json.RawMessage(`["original reason","new reason"]`)
			})
		}},
		{"reason replacement", func(raw []byte) []byte {
			return mutateSourceMetadataFixture(t, raw, func(metadata RawObject) {
				metadata["reasons"] = json.RawMessage(`["replacement reason"]`)
			})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertOperationDenied(t, PrincipalUser, SourceCaptureCommand,
				[][]byte{before}, [][]byte{test.edit(before)})
		})
	}
}

func sourceCaptureService(t *testing.T) (*AuthorityService, *FilesystemAuthority) {
	t.Helper()
	root := initGitFixture(t)
	writeFile(t, root, "unrelated.txt", []byte("Initial unrelated file"))
	gitCommand(t, root, "add", ".")
	gitCommand(t, root, "commit", "-m", "initial")
	committer, err := NewLocalGitCommitter(root)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Committer: committer})
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthorityService(host, CommandPolicy{}, nil), host
}

func sourceCaptureFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := NewMarkdownDocument("s-20261012-source", "Original title", []byte("Original source body."),
		&DocumentEnvelope{
			Spec: DocumentSpec,
			Entity: Entity{
				LogicalID: "s-20261012-source", EntityType: EntitySource,
				Schema: SourceSchema, SemanticRevision: 1,
			},
			Relations: Relations{Outgoing: []TypedEdge{}},
			Extension: RawObject{"source": json.RawMessage(
				`{"url":"https://example.com/original","saved_at":"2026-10-12T10:00:00+08:00",` +
					`"reasons":["original reason"],"tags":[],"domain":"systems"}`)},
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func appendSourceReasonFixture(t *testing.T, raw []byte, reason string) []byte {
	t.Helper()
	return mutateSourceMetadataFixture(t, raw, func(metadata RawObject) {
		var reasons []string
		_ = json.Unmarshal(metadata["reasons"], &reasons)
		reasons = append(reasons, reason)
		metadata["reasons"], _ = json.Marshal(reasons)
	})
}

func mutateSourceMetadataFixture(t *testing.T, raw []byte, edit func(RawObject)) []byte {
	t.Helper()
	document, err := DecodeSY(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := decodeRawObject(document.Envelope.Extension["source"])
	if err != nil {
		t.Fatal(err)
	}
	edit(metadata)
	document.Envelope.Extension["source"], _ = json.Marshal(metadata)
	document.Envelope.Entity.SemanticRevision++
	after, err := EncodeSY(raw, document.Envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return after
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
