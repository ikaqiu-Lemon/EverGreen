package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestCommandsCannotCrossEntityOrPhysicalIdentityAtPlanAndApply(t *testing.T) {
	claim := syFixture("c-a", "Original claim")
	sourceEnvelope := &DocumentEnvelope{
		Spec:      DocumentSpec,
		Entity:    Entity{LogicalID: "s-a", EntityType: EntitySource, Schema: "evergreen.source/v1", SemanticRevision: 1},
		Relations: Relations{Outgoing: []TypedEdge{}},
	}
	source, err := NewMarkdownDocument("s-a", "Source", []byte("Original source"), sourceEnvelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	note := reviewSYFixture(t)
	for _, test := range []struct {
		name, command string
		before, after []byte
	}{
		{"claim writes Source", "claim.update", source, mutateSYFixture(t, source, "Changed source")},
		{"claim creates Source", "claim.update", nil, source},
		{"claim writes Note", "claim.update", note, note},
		{"edge writes Source", "claim.edge.update", source, source},
		{"edge writes Note", "claim.edge.update", note, note},
		{"edge creates Claim", "claim.edge.update", nil, claim},
		{"review writes Claim", "note.review.update", claim, claim},
		{"review creates Note", "note.review.update", nil, note},
		{"physical ID", "claim.update", claim, mutateAuthorityObject(t, claim, func(root RawObject) {
			root["ID"] = json.RawMessage(`"20261010120000-swapped"`)
		})},
		{"physical Spec", "claim.update", claim, mutateAuthorityObject(t, claim, func(root RawObject) {
			root["Spec"] = json.RawMessage(`"6"`)
		})},
		{"edge body", "claim.edge.update", claim, mutateSYFixture(t, claim, "Changed body")},
		{"edge title", "claim.edge.update", claim, mutateAuthorityObject(t, claim, func(root RawObject) {
			root["Properties"] = json.RawMessage(`{"title":"Changed title"}`)
		})},
		{"claim carries review blocks", "claim.update", claim, mutateAuthorityObject(t, claim, func(root RawObject) {
			tree, _ := parseReviewTree(note, nil)
			node, _ := tree.root.children[0].encode()
			root["Children"], _ = json.Marshal([]json.RawMessage{node})
		})},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertOperationDenied(t, PrincipalUser, test.command, [][]byte{test.before}, [][]byte{test.after})
		})
	}
}

func TestClaimBodyPreservesCoreAndUserBlockIdentity(t *testing.T) {
	before := sectionClaimFixture(t)
	for _, test := range []struct {
		name string
		role PrincipalType
		edit func(*reviewNode)
	}{
		{"Agent replaces core", PrincipalAgent, func(root *reviewNode) { root.children[1].object["Data"] = json.RawMessage(`"Changed"`) }},
		{"Agent appends core", PrincipalAgent, func(root *reviewNode) {
			root.children = append(root.children[:2], append([]*reviewNode{plainAuthorityNode("new-core", "NodeParagraph", "Added")}, root.children[2:]...)...)
		}},
		{"Agent rewrites evidence", PrincipalAgent, func(root *reviewNode) { root.children[3].object["Data"] = json.RawMessage(`"Changed"`) }},
		{"Agent deletes evidence", PrincipalAgent, func(root *reviewNode) { root.children = append(root.children[:3], root.children[4:]...) }},
		{"Agent recreates evidence ID", PrincipalAgent, func(root *reviewNode) { root.children[3].object["ID"] = json.RawMessage(`"different-id"`) }},
		{"Agent edits user notes", PrincipalAgent, func(root *reviewNode) { root.children[7].object["Data"] = json.RawMessage(`"Changed"`) }},
		{"User edits user notes", PrincipalUser, func(root *reviewNode) { root.children[7].object["Data"] = json.RawMessage(`"Changed"`) }},
		{"User removes user notes", PrincipalUser, func(root *reviewNode) { root.children = append(root.children[:6], root.children[8:]...) }},
		{"User recreates user ID", PrincipalUser, func(root *reviewNode) { root.children[7].object["ID"] = json.RawMessage(`"different-id"`) }},
		{"Agent edits unknown section", PrincipalAgent, func(root *reviewNode) { root.children[9].object["Data"] = json.RawMessage(`"Changed"`) }},
		{"User edits unknown section", PrincipalUser, func(root *reviewNode) { root.children[9].object["Data"] = json.RawMessage(`"Changed"`) }},
		{"User reorders unknown section", PrincipalUser, func(root *reviewNode) {
			children := append([]*reviewNode(nil), root.children[:6]...)
			children = append(children, root.children[8:]...)
			root.children = append(children, root.children[6:8]...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree, err := parseReviewNode(before)
			if err != nil {
				t.Fatal(err)
			}
			test.edit(tree)
			after, _ := tree.encode()
			assertOperationDenied(t, test.role, "claim.update", [][]byte{before}, [][]byte{after})
		})
	}
	t.Run("unpartitioned Agent body", func(t *testing.T) {
		raw := syFixture("c-a", "Unpartitioned")
		assertOperationDenied(t, PrincipalAgent, "claim.update", [][]byte{raw}, [][]byte{mutateSYFixture(t, raw, "Changed")})
	})
	t.Run("creation cannot fabricate user notes", func(t *testing.T) {
		assertOperationDenied(t, PrincipalAgent, "claim.update", [][]byte{nil}, [][]byte{before})
	})
}

func TestAgentCannotDeleteRelationsOrCreateReplacement(t *testing.T) {
	before := syFixture("c-a", "Original")
	document, _ := DecodeSY(before, nil)
	document.Envelope.Relations.Outgoing = []TypedEdge{{
		ID: "edge-old", Schema: ArgumentSchema, Type: "supports", Reason: "Original evidence",
		Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-z"},
	}}
	before, _ = EncodeSY(before, document.Envelope, nil)
	for _, command := range []string{"claim.update", "claim.edge.update"} {
		t.Run(command, func(t *testing.T) {
			document, _ := DecodeSY(before, nil)
			document.Envelope.Relations.Outgoing = nil
			deleted, _ := EncodeSY(before, document.Envelope, nil)
			assertOperationDenied(t, PrincipalAgent, command, [][]byte{before}, [][]byte{deleted})
			document.Envelope.Relations.Outgoing = []TypedEdge{{
				ID: "edge-old", Schema: ReplacementSchema, Type: "replaced_by", Reason: "Replacement",
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-z"},
			}}
			replaced, _ := EncodeSY(before, document.Envelope, nil)
			assertOperationDenied(t, PrincipalAgent, command, [][]byte{before}, [][]byte{replaced})
		})
	}
	t.Run("creation replacement", func(t *testing.T) {
		document, _ := DecodeSY(before, nil)
		document.Envelope.Relations.Outgoing[0].Schema = ReplacementSchema
		document.Envelope.Relations.Outgoing[0].Type = "replaced_by"
		after, _ := EncodeSY(before, document.Envelope, nil)
		assertOperationDenied(t, PrincipalAgent, "claim.update", [][]byte{nil}, [][]byte{after})
	})
}

func TestNoteReviewCannotForgeOutputOrModifyUserBlocks(t *testing.T) {
	before := reviewSYFixture(t)
	tree, _ := parseReviewTree(before, nil)
	tree.document.Envelope.Extra = RawObject{"source_id": json.RawMessage(`"s-review"`)}
	tree.root.children = append(tree.root.children, plainAuthorityNode("user-note", "NodeParagraph", "User's original note"))
	_ = tree.writeDocumentEnvelope()
	before, _ = tree.encode()
	for _, test := range []struct {
		name string
		edit func(*reviewTree)
	}{
		{"source mapping", func(tree *reviewTree) { tree.document.Envelope.Extra["source_id"] = json.RawMessage(`"s-forged"`) }},
		{"output mapping", func(tree *reviewTree) {
			tree.candidates[0].envelope.Candidate.MaterializedClaimID = "c-forged"
			tree.candidates[0].envelope.Candidate.State = "materialized"
			_ = tree.candidates[0].writeEnvelope()
		}},
		{"user block content", func(tree *reviewTree) { tree.root.children[4].object["Data"] = json.RawMessage(`"Changed"`) }},
		{"user block removal", func(tree *reviewTree) { tree.root.children = tree.root.children[:4] }},
		{"user block recreated", func(tree *reviewTree) { tree.root.children[4].object["ID"] = json.RawMessage(`"recreated-user-note"`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree, _ := parseReviewTree(before, nil)
			test.edit(tree)
			_ = tree.writeDocumentEnvelope()
			after, _ := tree.encode()
			assertOperationDenied(t, PrincipalUser, "note.review.update", [][]byte{before}, [][]byte{after})
		})
	}
}

func TestAuthorizedBodyAppendAndOpposingOwnerMoveCommitOnce(t *testing.T) {
	for _, test := range []struct {
		name string
		role PrincipalType
		edit func(*reviewNode)
	}{
		{"Agent appends evidence", PrincipalAgent, func(root *reviewNode) {
			root.children = append(root.children[:4], append([]*reviewNode{plainAuthorityNode("added-evidence", "NodeParagraph", "More evidence")}, root.children[4:]...)...)
		}},
		{"User edits core", PrincipalUser, func(root *reviewNode) { root.children[1].object["Data"] = json.RawMessage(`"User changed core"`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := sectionClaimFixture(t)
			root, _ := parseReviewNode(before)
			test.edit(root)
			after, _ := root.encode()
			assertOperationAccepted(t, test.role, "claim.update", [][]byte{before}, [][]byte{after})
		})
	}
	low, high := syFixture("c-a", "Low"), syFixture("c-z", "High")
	highDoc, _ := DecodeSY(high, nil)
	highDoc.Envelope.Relations.Outgoing = []TypedEdge{{
		ID: "edge-move", Schema: ArgumentSchema, Type: "supports", Reason: "Directed evidence",
		Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-a"},
	}}
	high, _ = EncodeSY(high, highDoc.Envelope, nil)
	highDoc.Envelope.Relations.Outgoing = nil
	highAfter, _ := EncodeSY(high, highDoc.Envelope, nil)
	lowDoc, _ := DecodeSY(low, nil)
	lowDoc.Envelope.Relations.Outgoing = []TypedEdge{{
		ID: "edge-move", Schema: ArgumentSchema, Type: "opposing", Reason: "Symmetric evidence",
		Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-z"},
	}}
	lowAfter, _ := EncodeSY(low, lowDoc.Envelope, nil)
	assertOperationAccepted(t, PrincipalAgent, "claim.edge.update", [][]byte{low, high}, [][]byte{lowAfter, highAfter})
}

func plainAuthorityNode(id, kind, text string) *reviewNode {
	node := &reviewNode{object: RawObject{}}
	node.object["ID"], _ = json.Marshal(id)
	node.object["Type"], _ = json.Marshal(kind)
	node.object["Data"], _ = json.Marshal(text)
	if kind == "NodeHeading" {
		node.object["HeadingLevel"] = json.RawMessage(`2`)
	}
	return node
}

func sectionClaimFixture(t *testing.T) []byte {
	t.Helper()
	root, _ := parseReviewNode(syFixture("c-a", "Original"))
	root.children = nil
	for _, section := range []struct{ title, text string }{
		{"知识内容", "Immutable core"}, {"解释与依据", "Existing evidence"}, {"条件与边界", "Existing boundary"},
		{"用户补充", "User's original note"}, {"User section", "Custom content"},
	} {
		root.children = append(root.children,
			plainAuthorityNode(StablePhysicalID(section.title), "NodeHeading", section.title),
			plainAuthorityNode(StablePhysicalID(section.text), "NodeParagraph", section.text))
	}
	raw, err := root.encode()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mutateAuthorityObject(t *testing.T, raw []byte, mutate func(RawObject)) []byte {
	t.Helper()
	root, err := decodeRawObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	mutate(root)
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertOperationDenied(t *testing.T, role PrincipalType, command string, before, after [][]byte) {
	t.Helper()
	for _, transport := range []string{"plan", "raw apply"} {
		t.Run(transport, func(t *testing.T) {
			service, host, principal, request, operation := operationAttackFixture(t, role, command, before, after)
			var err error
			if transport == "plan" {
				_, err = service.Plan(context.Background(), principal, request)
			} else {
				if err = ValidatePlannedOperation(operation, nil); err != nil {
					t.Fatalf("attack did not reach authorization: %v", err)
				}
				_, err = service.Apply(context.Background(), principal, ApplyRequest{Operation: operation})
			}
			if !HasDiagnostic(err, CodeUnauthorized) {
				t.Errorf("unauthorized operation accepted or returned wrong diagnostic: %v", err)
			}
			for index, image := range operation.Images {
				if before[index] == nil {
					if _, err := os.Stat(filepath.Join(host.Root(), image.Path)); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("denied create wrote authority: %v", err)
					}
				} else {
					assertFileEquals(t, host.Root(), image.Path, before[index])
				}
			}
			if count := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); count != "1\n" {
				t.Errorf("denied operation commit count = %q", count)
			}
			if _, err := os.Stat(host.operationDir(request.OperationID)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("denied operation created a journal: %v", err)
			}
		})
	}
}

func assertOperationAccepted(t *testing.T, role PrincipalType, command string, before, after [][]byte) {
	t.Helper()
	service, host, principal, request, _ := operationAttackFixture(t, role, command, before, after)
	planned, err := service.Plan(context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := service.Apply(context.Background(), principal, ApplyRequest{Operation: planned}); err != nil {
			t.Fatal(err)
		}
	}
	for index, image := range planned.Images {
		assertFileEquals(t, host.Root(), image.Path, after[index])
	}
	if count := gitCommand(t, host.Root(), "rev-list", "--count", "HEAD"); count != "2\n" {
		t.Fatalf("authorized operation or retry commit count = %q", count)
	}
}

func operationAttackFixture(t *testing.T, role PrincipalType, command string, before, after [][]byte) (
	*AuthorityService, *FilesystemAuthority, Principal, PlanRequest, PlannedOperation,
) {
	t.Helper()
	root := initGitFixture(t)
	writeFile(t, root, "unrelated.txt", []byte("Initial unrelated file"))
	request := PlanRequest{Protocol: ChangePlanProtocol, OperationID: "op-command-attack", Command: command}
	principal := Principal{Type: role, ID: "fixture", AuthSource: "fixture", RequestReason: "Exercise command boundaries"}
	operation := PlannedOperation{Plan: ChangePlan{
		Protocol: request.Protocol, OperationID: request.OperationID, Command: command, Principal: principal,
	}}
	for index, raw := range after {
		doc, err := DecodeSY(raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		id := doc.Envelope.Entity.LogicalID
		path := "box/" + string(id) + ".sy"
		base := BaseRef{LogicalID: id}
		create := before[index] == nil
		if create {
			base.Expected = "absent"
		} else {
			writeFile(t, root, path, before[index])
			base.SemanticHash = semanticHashBytes(t, before[index])
		}
		hash := semanticHashBytes(t, raw)
		request.Base = append(request.Base, base)
		request.Writes = append(request.Writes, WriteInput{LogicalID: id, Path: path, After: raw})
		operation.Plan.Base = append(operation.Plan.Base, base)
		operation.Plan.Writes = append(operation.Plan.Writes, PlannedWrite{LogicalID: id, Before: base.SemanticHash, After: hash})
		operation.Images = append(operation.Images, AfterImage{
			LogicalID: id, Path: path, Bytes: raw, SemanticHash: hash, ContentHash: contentHash(raw), Create: create,
		})
	}
	sort.Slice(request.Writes, func(i, j int) bool { return request.Writes[i].LogicalID < request.Writes[j].LogicalID })
	operation.PlanHash = ComputePlanHash(operation.Plan, operation.Images)
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
	return NewAuthorityService(host, CommandPolicy{}, nil), host, principal, request, operation
}
