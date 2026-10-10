package evergreencore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNoteReviewRoundTripCoverageAndRebase(t *testing.T) {
	raw := reviewSYFixture(t)
	snapshot, err := InspectNoteReview(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.CanMaterialize || snapshot.Summary.SegmentCount != 3 ||
		snapshot.Summary.CandidateCount != 1 ||
		snapshot.Summary.CoveredSegments != 3 {
		t.Fatalf("initial snapshot = %+v", snapshot)
	}
	if snapshot.Segments[0].BlockID == "" ||
		snapshot.Candidates[0].CurrentPayloadHash == "" {
		t.Fatalf("physical identity or payload hash missing: %+v", snapshot)
	}

	edited := mutateReviewSegment(t, raw, "seg-01", "edited source body")
	normalized, err := NormalizeNoteReviewEdit(raw, edited, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized.Lineage) != 1 || normalized.Lineage[0].Mutation != "edit" {
		t.Fatalf("edit lineage = %+v", normalized.Lineage)
	}
	if !normalized.Snapshot.Candidates[0].Stale ||
		normalized.Snapshot.Candidates[0].Metadata.State != "stale" {
		t.Fatalf("edited candidate was not stale: %+v", normalized.Snapshot.Candidates[0])
	}
	if normalized.Snapshot.Segments[0].StoredHash !=
		normalized.Snapshot.Segments[0].CurrentHash {
		t.Fatalf("segment hash was not normalized in the same operation")
	}

	rebased, diff, err := ApplyReviewCommand(normalized.After, ReviewCommand{
		Action: ReviewActionCandidateRebase, CandidateID: "cand-review",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.RefHashes) != 1 || diff.RefHashes[0].SegmentID != "seg-01" {
		t.Fatalf("rebase diff = %+v", diff)
	}
	rebasedSnapshot, err := InspectNoteReview(rebased, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rebasedSnapshot.Candidates[0].Stale ||
		rebasedSnapshot.Candidates[0].Metadata.State != "draft" ||
		!rebasedSnapshot.CanMaterialize {
		t.Fatalf("rebased snapshot = %+v", rebasedSnapshot)
	}
}

func TestNoteReviewCoverageFailsClosed(t *testing.T) {
	t.Run("missing and duplicate", func(t *testing.T) {
		raw := mutateReviewEnvelope(t, reviewSYFixture(t), func(review *NoteReview) {
			review.Coverage[0].SegmentRefs = []LogicalID{"seg-01", "seg-01"}
		})
		snapshot, err := InspectNoteReview(raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.CanMaterialize ||
			!hasReviewDiagnostic(snapshot.Diagnostics, CodeCoverageDuplicate) ||
			!hasReviewDiagnostic(snapshot.Diagnostics, CodeCoverageMissing) {
			t.Fatalf("coverage diagnostics = %+v", snapshot.Diagnostics)
		}
	})

	t.Run("unresolved", func(t *testing.T) {
		raw := mutateReviewEnvelope(t, reviewSYFixture(t), func(review *NoteReview) {
			review.Coverage[1].Disposition = "unresolved"
			review.Coverage[1].Reason = "Needs human review."
		})
		snapshot, err := InspectNoteReview(raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.CanMaterialize ||
			!hasReviewDiagnostic(snapshot.Diagnostics, CodeReviewUnresolved) {
			t.Fatalf("unresolved diagnostics = %+v", snapshot.Diagnostics)
		}
	})

	t.Run("reason required", func(t *testing.T) {
		tree, err := parseReviewTree(reviewSYFixture(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		tree.document.Envelope.Review.Coverage[1].Reason = ""
		if err = ValidateDocument(tree.document.Envelope, DefaultRegistry()); !HasDiagnostic(err, CodeCoverageInvalid) {
			t.Fatalf("missing reason error = %v", err)
		}
	})

	t.Run("candidate referenced", func(t *testing.T) {
		raw := mutateReviewEnvelope(t, reviewSYFixture(t), func(review *NoteReview) {
			review.Coverage[0].CandidateIDs = []LogicalID{"cand-missing"}
		})
		snapshot, err := InspectNoteReview(raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.CanMaterialize ||
			!hasReviewDiagnostic(snapshot.Diagnostics, CodeCandidateMissing) {
			t.Fatalf("missing candidate diagnostics = %+v", snapshot.Diagnostics)
		}
	})
}

func TestNoteReviewSegmentIdentityAndLineageMatrix(t *testing.T) {
	raw := reviewSYFixture(t)
	t.Run("move preserves identity", func(t *testing.T) {
		tree, err := parseReviewTree(raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		tree.root.children[0], tree.root.children[1] =
			tree.root.children[1], tree.root.children[0]
		moved, err := tree.encode()
		if err != nil {
			t.Fatal(err)
		}
		result, err := NormalizeNoteReviewEdit(raw, moved, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Lineage) != 0 || result.Snapshot.Candidates[0].Stale {
			t.Fatalf("move changed semantic identity: %+v", result)
		}
		byID := map[LogicalID]string{}
		for _, segment := range result.Snapshot.Segments {
			byID[segment.SegmentID] = segment.BlockID
		}
		if byID["seg-01"] != "20261010120001-seg0001" {
			t.Fatalf("moved segment block ID = %q", byID["seg-01"])
		}
	})

	tests := []struct {
		name     string
		mutation string
		edit     func(*testing.T, []byte) []byte
		lineage  SegmentLineage
	}{
		{
			name: "split", mutation: "split",
			edit: func(t *testing.T, raw []byte) []byte {
				return splitReviewSegment(t, raw, "seg-01", "seg-01a", "seg-01b")
			},
			lineage: SegmentLineage{
				EventID: "lineage-split", Mutation: "split",
				PreviousIDs: []LogicalID{"seg-01"},
				NextIDs:     []LogicalID{"seg-01a", "seg-01b"},
			},
		},
		{
			name: "merge", mutation: "merge",
			edit: func(t *testing.T, raw []byte) []byte {
				return mergeReviewSegments(t, raw, []LogicalID{"seg-01", "seg-02"}, "seg-merged")
			},
			lineage: SegmentLineage{
				EventID: "lineage-merge", Mutation: "merge",
				PreviousIDs: []LogicalID{"seg-01", "seg-02"},
				NextIDs:     []LogicalID{"seg-merged"},
			},
		},
		{
			name: "delete", mutation: "delete",
			edit: func(t *testing.T, raw []byte) []byte {
				return deleteReviewSegment(t, raw, "seg-01")
			},
			lineage: SegmentLineage{
				EventID: "lineage-delete", Mutation: "delete",
				PreviousIDs: []LogicalID{"seg-01"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := NormalizeNoteReviewEdit(
				raw, test.edit(t, raw), []SegmentLineage{test.lineage}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Lineage) != 1 ||
				result.Lineage[0].Mutation != test.mutation ||
				!result.Snapshot.Candidates[0].Stale {
				t.Fatalf("%s result = %+v", test.name, result)
			}
		})
	}

	t.Run("identity change without lineage is rejected", func(t *testing.T) {
		edited := deleteReviewSegment(t, raw, "seg-01")
		if _, err := NormalizeNoteReviewEdit(raw, edited, nil, nil); !HasDiagnostic(err, CodeSegmentLineage) {
			t.Fatalf("missing lineage error = %v", err)
		}
	})
}

func TestReviewPlanAndDeterministicMaterializationRecoverAtomically(t *testing.T) {
	root := t.TempDir()
	notePath := "box/note.sy"
	writeFile(t, root, notePath, reviewSYFixture(t))
	commits := newRecordingCommitter()
	faults := &oneShotFault{point: FaultAfterRename, index: 0}
	authority, err := NewFilesystemAuthority(FilesystemAuthorityOptions{
		Root: root, Committer: commits, Faults: faults,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewAuthorityService(authority, allowAllAuthorizer{}, nil)
	principal := Principal{Type: PrincipalUser, ID: "reviewer"}
	noteRaw := readFile(t, root, notePath)
	noteHash, _ := SemanticHash(noteRaw)
	request := ReviewMaterializeRequest{
		OperationID: "op-materialize", NoteID: "n-review",
		CandidateID: "cand-review", NoteBase: noteHash,
		ClaimID: "k-review-output", ClaimPath: "box/claim.sy",
		ClaimDocumentID: "20261010130000-claim01",
		CreatedAt:       "2026-10-10T13:00:00Z",
	}
	first, err := service.PlanReviewMaterialization(
		context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PlanReviewMaterialization(
		context.Background(), principal, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("materialization plan is not deterministic")
	}
	if len(first.Operation.Plan.Writes) != 2 ||
		first.Operation.Plan.Writes[0].LogicalID != "k-review-output" ||
		first.Operation.Plan.Writes[1].LogicalID != "n-review" {
		t.Fatalf("materialization write set = %+v", first.Operation.Plan.Writes)
	}

	if _, err = service.Apply(context.Background(), principal,
		ApplyRequest{Operation: first.Operation}); err == nil {
		t.Fatal("fault injection did not interrupt materialization")
	}
	recovered, err := service.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].State != OperationCompleted {
		t.Fatalf("recovered operations = %+v", recovered)
	}
	claimRaw := readFile(t, root, "box/claim.sy")
	claim, err := DecodeSY(claimRaw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Envelope.Entity.LogicalID != "k-review-output" ||
		len(claim.Envelope.Provenance) != 1 ||
		len(claim.Envelope.Relations.Outgoing) != 1 {
		t.Fatalf("materialized claim = %+v", claim.Envelope)
	}
	noteAfter := readFile(t, root, notePath)
	snapshot, err := InspectNoteReview(noteAfter, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Candidates[0].Metadata.MaterializedClaimID != "k-review-output" ||
		snapshot.Candidates[0].Metadata.State != "materialized" ||
		commits.Count("op-materialize") != 1 {
		t.Fatalf("materialized Note=%+v commits=%d",
			snapshot.Candidates[0], commits.Count("op-materialize"))
	}
}

func reviewSYFixture(t *testing.T) []byte {
	t.Helper()
	source1 := reviewSegmentFixture(t, "20261010120001-seg0001", "seg-01",
		"note_segment", "source body one", "L1-L4", "")
	source2 := reviewSegmentFixture(t, "20261010120002-seg0002", "seg-02",
		"note_segment", "source body two", "L5-L8", "")
	annotation := reviewSegmentFixture(t, "20261010120003-seg0003", "seg-03",
		"agent_annotation", "agent distinction", "", "distinction")
	segments := []*reviewNode{source1, source2, annotation}

	payload := &reviewNode{object: RawObject{}}
	payload.object["ID"], _ = json.Marshal("20261010121001-payload")
	payload.object["Type"], _ = json.Marshal("NodeParagraph")
	payload.object["Data"], _ = json.Marshal("A self-contained reviewed claim.")
	candidateNode := &reviewNode{object: RawObject{}, children: []*reviewNode{payload}}
	candidateNode.object["ID"], _ = json.Marshal("20261010121000-cand001")
	candidateNode.object["Type"], _ = json.Marshal("NodeSuperBlock")
	refHashes := map[LogicalID]string{
		"seg-01": segmentNodeHash(source1),
		"seg-02": segmentNodeHash(source2),
	}
	candidateEnvelope := &BlockEnvelope{
		Spec: BlockSpec, Role: "candidate",
		Candidate: &CandidateMetadata{
			CandidateID: "cand-review", ClaimKind: KnowledgeKind,
			KindSchema: KnowledgeKindSchema, Title: "Reviewed claim",
			LogicalSlug: "reviewed-claim",
			SegmentRefs: []LogicalID{"seg-01", "seg-02"},
			PayloadHash: candidatePayloadHash(candidateNode), RefHashes: refHashes,
			Relation: "support", Reason: "The two source segments directly support this claim.",
			Tags: []string{"review"}, State: "draft",
		},
	}
	candidateNode.object[evergreenJSONKey] = mustMarshalBlock(t, candidateEnvelope)

	root := &reviewNode{object: RawObject{}, children: append(segments, candidateNode)}
	root.object["ID"], _ = json.Marshal("20261010120000-note001")
	root.object["Type"], _ = json.Marshal("NodeDocument")
	root.object["Spec"], _ = json.Marshal("5")
	documentEnvelope := &DocumentEnvelope{
		Spec: DocumentSpec,
		Entity: Entity{
			LogicalID: "n-review", EntityType: EntityNote,
			Schema: "evergreen.note/v1", SemanticRevision: 1,
		},
		Relations: Relations{Outgoing: []TypedEdge{}},
		Review: &NoteReview{
			Spec: NoteReviewSpec,
			Coverage: []CoverageModule{
				{
					ModuleID: "coverage-main", Disposition: "candidate",
					SegmentRefs:  []LogicalID{"seg-01", "seg-02"},
					CandidateIDs: []LogicalID{"cand-review"},
				},
				{
					ModuleID: "coverage-annotation", Disposition: "note_only",
					SegmentRefs: []LogicalID{"seg-03"},
					Reason:      "Annotation remains useful only in the Note.",
				},
			},
		},
	}
	envelopeRaw, err := MarshalDocumentEnvelope(documentEnvelope)
	if err != nil {
		t.Fatal(err)
	}
	root.object[evergreenJSONKey] = envelopeRaw
	raw, err := root.encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeSY(raw, nil); err != nil {
		t.Fatal(err)
	}
	return raw
}

func reviewSegmentFixture(
	t *testing.T,
	blockID string,
	segmentID LogicalID,
	role, data, locator, annotation string,
) *reviewNode {
	t.Helper()
	node := &reviewNode{object: RawObject{}}
	node.object["ID"], _ = json.Marshal(blockID)
	node.object["Type"], _ = json.Marshal("NodeParagraph")
	node.object["Data"], _ = json.Marshal(data)
	metadata := &SegmentMetadata{SegmentID: segmentID}
	if role == "note_segment" {
		metadata.SourceRef = SourceLocator{
			SourceID: "s-review",
			Locator:  Locator{Kind: "source-lines", Value: locator},
		}
	} else {
		metadata.Annotation = &Annotation{Kind: annotation}
	}
	metadata.NormalizedHash = segmentNodeHash(node)
	node.object[evergreenJSONKey] = mustMarshalBlock(t, &BlockEnvelope{
		Spec: BlockSpec, Role: role, Segment: metadata,
	})
	return node
}

func mustMarshalBlock(t *testing.T, envelope *BlockEnvelope) json.RawMessage {
	t.Helper()
	raw, err := MarshalBlockEnvelope(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mutateReviewSegment(t *testing.T, raw []byte, id LogicalID, data string) []byte {
	t.Helper()
	tree, err := parseReviewTree(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree.segmentByID[id].node.object["Data"], _ = json.Marshal(data)
	out, err := tree.encode()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mutateReviewEnvelope(t *testing.T, raw []byte, mutate func(*NoteReview)) []byte {
	t.Helper()
	tree, err := parseReviewTree(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	mutate(tree.document.Envelope.Review)
	if err = tree.writeDocumentEnvelope(); err != nil {
		t.Fatal(err)
	}
	out, err := tree.encode()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func splitReviewSegment(
	t *testing.T,
	raw []byte,
	oldID, firstID, secondID LogicalID,
) []byte {
	t.Helper()
	tree, err := parseReviewTree(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := tree.segmentByID[oldID]
	first := cloneReviewNode(t, old.node)
	second := cloneReviewNode(t, old.node)
	setReviewSegmentIdentity(t, first, firstID, "20261010122001-split01", "split body one")
	setReviewSegmentIdentity(t, second, secondID, "20261010122002-split02", "split body two")
	replaceTopLevelNode(tree.root, old.node, first, second)
	out, err := tree.encode()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mergeReviewSegments(
	t *testing.T,
	raw []byte,
	oldIDs []LogicalID,
	nextID LogicalID,
) []byte {
	t.Helper()
	tree, err := parseReviewTree(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	merged := cloneReviewNode(t, tree.segmentByID[oldIDs[0]].node)
	setReviewSegmentIdentity(t, merged, nextID, "20261010122003-merge01", "merged body")
	remove := map[*reviewNode]bool{}
	for _, id := range oldIDs {
		remove[tree.segmentByID[id].node] = true
	}
	var children []*reviewNode
	inserted := false
	for _, child := range tree.root.children {
		if remove[child] {
			if !inserted {
				children = append(children, merged)
				inserted = true
			}
			continue
		}
		children = append(children, child)
	}
	tree.root.children = children
	out, err := tree.encode()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func deleteReviewSegment(t *testing.T, raw []byte, id LogicalID) []byte {
	t.Helper()
	tree, err := parseReviewTree(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	remove := tree.segmentByID[id].node
	var children []*reviewNode
	for _, child := range tree.root.children {
		if child != remove {
			children = append(children, child)
		}
	}
	tree.root.children = children
	out, err := tree.encode()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func cloneReviewNode(t *testing.T, node *reviewNode) *reviewNode {
	t.Helper()
	raw, err := node.encode()
	if err != nil {
		t.Fatal(err)
	}
	clone, err := parseReviewNode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return clone
}

func setReviewSegmentIdentity(
	t *testing.T,
	node *reviewNode,
	id LogicalID,
	blockID, data string,
) {
	t.Helper()
	envelope, err := UnmarshalBlockEnvelope(node.object[evergreenJSONKey])
	if err != nil {
		t.Fatal(err)
	}
	envelope.Segment.SegmentID = id
	node.object["ID"], _ = json.Marshal(blockID)
	node.object["Data"], _ = json.Marshal(data)
	envelope.Segment.NormalizedHash = segmentNodeHash(node)
	node.object[evergreenJSONKey] = mustMarshalBlock(t, envelope)
}

func replaceTopLevelNode(root, old *reviewNode, replacements ...*reviewNode) {
	var children []*reviewNode
	for _, child := range root.children {
		if child == old {
			children = append(children, replacements...)
		} else {
			children = append(children, child)
		}
	}
	root.children = children
}

func hasReviewDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func TestReviewFixturePathIsStable(t *testing.T) {
	// Keep fixture paths compatible with the filesystem authority's normalized
	// slash-separated .sy contract.
	if got := filepath.ToSlash(filepath.Join("box", "note.sy")); got != "box/note.sy" {
		t.Fatalf("fixture path = %q", got)
	}
}
