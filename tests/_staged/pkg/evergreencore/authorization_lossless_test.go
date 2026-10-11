package evergreencore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedClaimFieldsRejectLosslessRawPlanAndApply(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*DocumentEnvelope)
	}{
		{"validation integer", func(e *DocumentEnvelope) {
			e.Claim.KindData = bytes.ReplaceAll(e.Claim.KindData, []byte("9007199254740992"), []byte("9007199254740993"))
		}},
		{"validation unknown removed", func(e *DocumentEnvelope) {
			e.Claim.KindData = json.RawMessage(`{"validation":{"status":"pending"}}`)
		}},
		{"provenance integer", func(e *DocumentEnvelope) {
			e.Provenance[0].Extra["future"] = json.RawMessage(`{"count":9007199254740993,"values":[null,{"kept":true}]}`)
		}},
		{"provenance null", func(e *DocumentEnvelope) {
			e.Provenance[0].Extra["future"] = json.RawMessage(`null`)
		}},
		{"provenance removed", func(e *DocumentEnvelope) {
			delete(e.Provenance[0].Extra, "future")
		}},
		{"entity schema", func(e *DocumentEnvelope) { e.Entity.Schema = "third-party.claim/v1" }},
		{"entity unknown", func(e *DocumentEnvelope) { delete(e.Entity.Extra, "future") }},
		{"claim unknown", func(e *DocumentEnvelope) { delete(e.Claim.Extra, "future") }},
		{"envelope unknown", func(e *DocumentEnvelope) { delete(e.Extra, "future") }},
		{"extension integer", func(e *DocumentEnvelope) {
			e.Extension["example/v1"] = json.RawMessage(`{"count":9007199254740993}`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := DefaultRegistry()
			raw := losslessClaimFixture(t, registry)
			document, err := DecodeSY(raw, registry)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(document.Envelope)
			after, err := EncodeSY(raw, document.Envelope, registry)
			if err != nil {
				t.Fatal(err)
			}
			assertRawAuthorizationDenied(t, registry, raw, after)
		})
	}
}

func TestProtectedRelationsIncludeEveryUnknownPartition(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Relations)
	}{
		{"relations", func(r *Relations) { delete(r.Extra, "future") }},
		{"edge", func(r *Relations) { delete(r.Outgoing[0].Extra, "future") }},
		{"target", func(r *Relations) { delete(r.Outgoing[0].Target.Extra, "future") }},
		{"context", func(r *Relations) { delete(r.Outgoing[0].Context.Extra, "future") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := DefaultRegistry()
			descriptor, _ := registry.Descriptor(OpinionKind)
			descriptor.RequiredCapabilities["claim.relations"] = []PrincipalType{PrincipalUser}
			registry.Unregister(OpinionKind)
			if err := registry.Register(descriptor); err != nil {
				t.Fatal(err)
			}
			raw := losslessClaimFixture(t, registry)
			document, err := DecodeSY(raw, registry)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(&document.Envelope.Relations)
			after, err := EncodeSY(raw, document.Envelope, registry)
			if err != nil {
				t.Fatal(err)
			}
			assertRawAuthorizationDenied(t, registry, raw, after)
		})
	}
}

func TestClaimUnknownPartitionsSurviveAuthorizedScalarEdit(t *testing.T) {
	registry := DefaultRegistry()
	raw := losslessClaimFixture(t, registry)
	document, err := DecodeSY(raw, registry)
	if err != nil {
		t.Fatal(err)
	}
	document.Envelope.Claim.Tags = []string{"updated"}
	// Whitespace and object key order are not changes to protected JSON.
	document.Envelope.Provenance[0].Extra["future"] = json.RawMessage(`{
		"values": [null, {"kept":true}], "count": 9007199254740992
	}`)
	after, err := EncodeSY(raw, document.Envelope, registry)
	if err != nil {
		t.Fatal(err)
	}
	root := initGitFixture(t)
	writeFile(t, root, "box/a.sy", raw)
	gitCommand(t, root, "add", ".")
	gitCommand(t, root, "commit", "-m", "initial")
	committer, err := NewLocalGitCommitter(root)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Registry: registry, Committer: committer})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(host, allowAllAuthorizer{}, registry)
	agent := Principal{Type: PrincipalAgent, ID: "agent", AuthSource: "fixture", RequestReason: "Edit tags"}
	request := losslessPlanRequest(t, raw, after)
	planned, err := service.Plan(context.Background(), agent, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Apply(context.Background(), agent, ApplyRequest{Operation: planned}); err != nil {
		t.Fatal(err)
	}
	actual := readFile(t, root, "box/a.sy")
	if !bytes.Contains(actual, []byte("9007199254740993")) || !bytes.Contains(actual, []byte(`"tags":["updated"]`)) {
		t.Fatal("authorized edit lost exact unknown values or tags")
	}
	if count := gitCommand(t, root, "rev-list", "--count", "HEAD"); count != "2\n" {
		t.Fatalf("authorized edit commit count = %q", count)
	}
}

func TestReviewLargeNumbersInvalidateSegmentAndCandidateHashes(t *testing.T) {
	for _, partition := range []string{"segment", "candidate"} {
		t.Run(partition, func(t *testing.T) {
			tree, err := parseReviewTree(reviewSYFixture(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			node := tree.root.children[0]
			if partition == "candidate" {
				node = tree.root.children[3].children[0]
			}
			node.object["Future"] = json.RawMessage(`{"count":9007199254740992,"nested":[null,{"value":9007199254740992}]}`)
			raw, err := tree.encode()
			if err != nil {
				t.Fatal(err)
			}
			raw, _, err = RefreshNoteReview(raw, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, _, err = ApplyReviewCommand(raw, ReviewCommand{Action: ReviewActionCandidateRebase, CandidateID: "cand-review"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			before, err := InspectNoteReview(raw, nil)
			if err != nil || before.Candidates[0].Stale {
				t.Fatalf("fixture was not fresh: %+v %v", before, err)
			}
			edited := bytes.ReplaceAll(raw, []byte("9007199254740992"), []byte("9007199254740993"))
			after, err := InspectNoteReview(edited, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !after.Candidates[0].Stale || after.CanMaterialize {
				t.Fatalf("adjacent large integers did not invalidate review: %+v", after)
			}
			if partition == "segment" && before.Segments[0].CurrentHash == after.Segments[0].CurrentHash {
				t.Fatal("different segment integers collided")
			}
			if partition == "candidate" && before.Candidates[0].CurrentPayloadHash == after.Candidates[0].CurrentPayloadHash {
				t.Fatal("different candidate integers collided")
			}
		})
	}
}

func TestCanonicalComparisonPreservesNumbersNullAndArrayOrder(t *testing.T) {
	for _, test := range []struct {
		left, right string
		equal       bool
	}{
		{`{"n":9007199254740992}`, `{"n":9007199254740993}`, false},
		{`{"n":9007199254740993,"v":[null,{"a":1}]}`, `{"v":[null,{"a":1}], "n":9007199254740993}`, true},
		{`{"v":null}`, `{}`, false},
		{`[null,1,2]`, `[null,2,1]`, false},
		{`{"n":-9007199254740992}`, `{"n":-9007199254740993}`, false},
	} {
		if got := semanticValuesEqual(json.RawMessage(test.left), json.RawMessage(test.right)); got != test.equal {
			t.Errorf("%s versus %s: equal=%v", test.left, test.right, got)
		}
	}
	if semanticValuesEqual(make(chan int), make(chan int)) {
		t.Fatal("marshal failures must not compare equal")
	}
}

func losslessClaimFixture(t *testing.T, registry *Registry) []byte {
	t.Helper()
	document, err := DecodeSY(syFixture("o-one", "unchanged body"), registry)
	if err != nil {
		t.Fatal(err)
	}
	future := func() RawObject {
		return RawObject{"future": json.RawMessage(`{"count":9007199254740993,"values":[null,{"kept":true}]}`)}
	}
	e := document.Envelope
	e.Entity.Extra, e.Extra = future(), future()
	e.Claim = &Claim{
		ClaimKind: OpinionKind, KindSchema: OpinionKindSchema, Status: "active", Extra: future(),
		KindData: json.RawMessage(`{"validation":{"status":"pending","future":{"count":9007199254740992,"nested":[null,{"value":9007199254740992}]}}}`),
	}
	e.Provenance = []Provenance{{
		SourceID: "s-one", NoteID: "n-one", SegmentRefs: []LogicalID{"seg-one"}, Reason: "Evidence",
		Extra: RawObject{"future": json.RawMessage(`{"count":9007199254740992,"values":[null,{"kept":true}]}`)},
	}}
	e.Relations = Relations{Extra: future(), Outgoing: []TypedEdge{{
		ID: "edge-one", Schema: ArgumentSchema, Type: "supports", Reason: "Evidence", Extra: future(),
		Target:  EntityRef{EntityType: EntityClaim, LogicalID: "c-target", Extra: future()},
		Context: EdgeContext{Extra: future()},
	}}}
	e.Extension = RawObject{"example/v1": json.RawMessage(`{"count":9007199254740992}`)}
	raw, err := EncodeSY(document.Raw, e, registry)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func losslessPlanRequest(t *testing.T, before, after []byte) PlanRequest {
	t.Helper()
	return PlanRequest{
		Protocol: ChangePlanProtocol, OperationID: "op-lossless", Command: "claim.update",
		Base:   []BaseRef{{LogicalID: "o-one", SemanticHash: semanticHashBytes(t, before)}},
		Writes: []WriteInput{{LogicalID: "o-one", After: after}},
	}
}

func assertRawAuthorizationDenied(t *testing.T, registry *Registry, before, after []byte) {
	t.Helper()
	// Plan and raw Apply run against separate real Git authorities. Raw Apply
	// recomputes every digest rather than relying on an invalid plan hash.
	for _, transport := range []string{"plan", "raw apply"} {
		t.Run(transport, func(t *testing.T) {
			root := initGitFixture(t)
			writeFile(t, root, "box/a.sy", before)
			gitCommand(t, root, "add", ".")
			gitCommand(t, root, "commit", "-m", "initial")
			committer, err := NewLocalGitCommitter(root)
			if err != nil {
				t.Fatal(err)
			}
			host, err := NewFilesystemAuthority(FilesystemAuthorityOptions{Root: root, Registry: registry, Committer: committer})
			if err != nil {
				t.Fatal(err)
			}
			service := NewAuthorityService(host, allowAllAuthorizer{}, registry)
			agent := Principal{Type: PrincipalAgent, ID: "agent", AuthSource: "fixture", RequestReason: "Authorization attack"}
			request := losslessPlanRequest(t, before, after)
			if transport == "plan" {
				_, err = service.Plan(context.Background(), agent, request)
			} else {
				hash := semanticHashBytes(t, after)
				operation := PlannedOperation{
					Plan: ChangePlan{
						OperationID: request.OperationID, Protocol: request.Protocol, Command: request.Command, Principal: agent,
						Base: request.Base, Writes: []PlannedWrite{{LogicalID: "o-one", Before: request.Base[0].SemanticHash, After: hash}},
					},
					Images: []AfterImage{{LogicalID: "o-one", Path: "box/a.sy", Bytes: after, SemanticHash: hash, ContentHash: contentHash(after)}},
				}
				operation.PlanHash = ComputePlanHash(operation.Plan, operation.Images)
				if err := ValidatePlannedOperation(operation, registry); err != nil {
					t.Fatalf("attack failed protocol validation before authorization: %v", err)
				}
				_, err = service.Apply(context.Background(), agent, ApplyRequest{Operation: operation})
			}
			if !HasDiagnostic(err, CodeUnauthorized) {
				t.Errorf("protected change accepted: %v", err)
			}
			assertFileEquals(t, root, "box/a.sy", before)
			if count := gitCommand(t, root, "rev-list", "--count", "HEAD"); count != "1\n" {
				t.Errorf("denied operation commit count = %q", count)
			}
			journal := filepath.Join(root, filepath.FromSlash(RuntimeDirName), TransactionsDirName, request.OperationID)
			if _, err := os.Stat(journal); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("denied operation created journal: %v", err)
			}
		})
	}
}
