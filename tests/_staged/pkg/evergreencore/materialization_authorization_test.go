package evergreencore

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMaterializationRejectsAgentAndUnboundClaimAtPlanAndApply(t *testing.T) {
	note, noteAfter, claim := materializationAuthorityFixture(t)
	assertOperationDenied(t, PrincipalAgent, "note.review.materialize", [][]byte{note, nil}, [][]byte{noteAfter, claim})
	assertOperationDenied(t, PrincipalUser, "note.review.materialize", [][]byte{nil}, [][]byte{claim})
	assertOperationDenied(t, PrincipalUser, "note.review.materialize", [][]byte{note}, [][]byte{noteAfter})
}

func TestMaterializationRecomputesCompleteClaimAndNoteImages(t *testing.T) {
	note, noteAfter, claim := materializationAuthorityFixture(t)
	for _, test := range []struct {
		name string
		edit func(RawObject)
	}{
		{"body", func(root RawObject) { root["Children"] = json.RawMessage(`[{"Type":"NodeParagraph","Data":"Forged"}]`) }},
		{"title", func(root RawObject) { root["Properties"] = json.RawMessage(`{"title":"Forged"}`) }},
		{"revision", func(root RawObject) {
			envelope, _ := UnmarshalDocumentEnvelope(root[evergreenJSONKey])
			envelope.Entity.SemanticRevision = 99
			root[evergreenJSONKey], _ = MarshalDocumentEnvelope(envelope)
		}},
		{"provenance", func(root RawObject) {
			envelope, _ := UnmarshalDocumentEnvelope(root[evergreenJSONKey])
			envelope.Provenance[0].Reason = "Forged"
			root[evergreenJSONKey], _ = MarshalDocumentEnvelope(envelope)
		}},
		{"material reason", func(root RawObject) {
			envelope, _ := UnmarshalDocumentEnvelope(root[evergreenJSONKey])
			envelope.Relations.Outgoing[0].Reason = "Forged"
			root[evergreenJSONKey], _ = MarshalDocumentEnvelope(envelope)
		}},
		{"nonmaterial edge", func(root RawObject) {
			envelope, _ := UnmarshalDocumentEnvelope(root[evergreenJSONKey])
			envelope.Relations.Outgoing = append(envelope.Relations.Outgoing, TypedEdge{
				ID: "edge-forged", Schema: ArgumentSchema, Type: "supports", Reason: "Forged",
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-forged"},
			})
			root[evergreenJSONKey], _ = MarshalDocumentEnvelope(envelope)
		}},
		{"tags", func(root RawObject) {
			envelope, _ := UnmarshalDocumentEnvelope(root[evergreenJSONKey])
			envelope.Claim.Tags = []string{"forged"}
			root[evergreenJSONKey], _ = MarshalDocumentEnvelope(envelope)
		}},
		{"extension", func(root RawObject) {
			envelope, _ := UnmarshalDocumentEnvelope(root[evergreenJSONKey])
			envelope.Extension = RawObject{"third.party/v1": json.RawMessage(`{"n":9007199254740993}`)}
			root[evergreenJSONKey], _ = MarshalDocumentEnvelope(envelope)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			forged := mutateAuthorityObject(t, claim, test.edit)
			assertOperationDenied(t, PrincipalUser, "note.review.materialize", [][]byte{note, nil}, [][]byte{noteAfter, forged})
		})
	}
	t.Run("materialization cannot edit Note body", func(t *testing.T) {
		forged := mutateReviewSegment(t, noteAfter, "seg-01", "Forged source content")
		assertOperationDenied(t, PrincipalUser, "note.review.materialize", [][]byte{note, nil}, [][]byte{forged, claim})
	})
	t.Run("materialization cannot edit Note coverage", func(t *testing.T) {
		forged := mutateReviewEnvelope(t, noteAfter, func(review *NoteReview) { review.Coverage[1].Reason = "Forged" })
		assertOperationDenied(t, PrincipalUser, "note.review.materialize", [][]byte{note, nil}, [][]byte{forged, claim})
	})
	t.Run("legitimate deterministic images", func(t *testing.T) {
		// The fixture preserves the same Note and Claim ordering as the sorted plan.
		assertOperationAccepted(t, PrincipalUser, "note.review.materialize", [][]byte{nil, note}, [][]byte{claim, noteAfter})
	})
}

func materializationAuthorityFixture(t *testing.T) (note, noteAfter, claim []byte) {
	t.Helper()
	note = reviewSYFixture(t)
	tree, err := parseReviewTree(note, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := ReviewMaterializeRequest{
		OperationID: "op-command-attack", NoteID: "n-review", CandidateID: "cand-review",
		NoteBase: semanticHashBytes(t, note), ClaimID: "c-materialized",
		ClaimPath: "box/c-materialized.sy", ClaimDocumentID: "20261010130000-new0001", CreatedAt: "2026-10-10T13:00:00Z",
	}
	claim, _, err = tree.materializedClaim(context.Background(), &memoryRepository{}, tree.candidates[0], request)
	if err != nil {
		t.Fatal(err)
	}
	tree.candidates[0].envelope.Candidate.State = "materialized"
	tree.candidates[0].envelope.Candidate.MaterializedClaimID = request.ClaimID
	_ = tree.candidates[0].writeEnvelope()
	tree.document.Envelope.Entity.SemanticRevision++
	_ = tree.writeDocumentEnvelope()
	noteAfter, err = tree.encode()
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestRematerializationPreservesUserSectionsAndLosslessNativeMetadata(t *testing.T) {
	note, _, _ := materializationAuthorityFixture(t)
	tree, _ := parseReviewTree(note, nil)
	claim := sectionClaimFixture(t)
	root, _ := decodeRawObject(claim)
	root["Properties"] = json.RawMessage(`{"title":"Original","custom-user":"Preserve","future":{"n":9007199254740993}}`)
	root["Future"] = json.RawMessage(`{"nested":[null,9007199254740993]}`)
	claim, _ = json.Marshal(root)
	document, _ := DecodeSY(claim, nil)
	document.Envelope.Relations.Extra = RawObject{"future": json.RawMessage(`{"n":9007199254740993}`)}
	claim, _ = EncodeSY(claim, document.Envelope, nil)
	repository := &claimsAVRepositoryStub{documents: map[LogicalID][]byte{"c-a": claim}}
	output, create, err := tree.materializedClaim(context.Background(), repository, tree.candidates[0], ReviewMaterializeRequest{
		NoteID: "n-review", CandidateID: "cand-review", ClaimID: "c-a",
		ClaimBase: semanticHashBytes(t, claim), CreatedAt: "2026-10-10T13:00:00Z",
	})
	if err != nil || create {
		t.Fatalf("rematerialization: create=%v err=%v", create, err)
	}
	previous, _ := parseReviewNode(claim)
	next, _ := parseReviewNode(output)
	if len(next.children) < 4 {
		t.Fatal("rematerialization removed protected user sections")
	}
	for index, original := range previous.children[6:] {
		actual := next.children[len(next.children)-4+index]
		left, _ := original.encode()
		right, _ := actual.encode()
		if !semanticValuesEqual(json.RawMessage(left), json.RawMessage(right)) {
			t.Errorf("rematerialization changed protected user block %d", index)
		}
	}
	nextObject, _ := decodeRawObject(output)
	properties, _ := decodeRawObject(nextObject["Properties"])
	if !semanticValuesEqual(root["Future"], nextObject["Future"]) ||
		!semanticValuesEqual(properties["future"], json.RawMessage(`{"n":9007199254740993}`)) ||
		!semanticValuesEqual(properties["custom-user"], json.RawMessage(`"Preserve"`)) {
		t.Fatal("rematerialization lost exact native metadata")
	}
	updated, _ := DecodeSY(output, nil)
	if !semanticValuesEqual(updated.Envelope.Relations.Extra, document.Envelope.Relations.Extra) {
		t.Fatal("rematerialization lost relations Extra")
	}
	t.Run("interleaved user sections fail closed", func(t *testing.T) {
		original := append([]*reviewNode(nil), previous.children...)
		previous.children = append(append(append([]*reviewNode{}, original[:2]...), original[6:]...), original[2:6]...)
		interleaved, _ := previous.encode()
		repository.documents["c-a"] = interleaved
		_, _, err := tree.materializedClaim(context.Background(), repository, tree.candidates[0], ReviewMaterializeRequest{
			NoteID: "n-review", CandidateID: "cand-review", ClaimID: "c-a",
			ClaimBase: semanticHashBytes(t, interleaved), CreatedAt: "2026-10-10T13:00:00Z",
		})
		if !HasDiagnostic(err, CodeUnauthorized) {
			t.Fatalf("unsafe user block relocation accepted: %v", err)
		}
	})
}
