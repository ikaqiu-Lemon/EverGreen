package evergreencore

import (
	"context"
	"encoding/json"
	"testing"
)

func TestInspectWorkspaceDoctorBindsEntitiesEdgesCandidatesCoverageAndQueries(t *testing.T) {
	repository := &claimsAVRepositoryStub{documents: map[LogicalID][]byte{
		"k-alpha": claimAVFixture(t, "k-alpha", KnowledgeKind, "Alpha knowledge", "active",
			json.RawMessage(`{}`), []string{"architecture"}, nil, []TypedEdge{{
				ID: "edge-doctor", Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"},
				Type:   "supports", Reason: "The evidence supports the bounded opinion.",
				Context: EdgeContext{NoteID: "n-review", SegmentRefs: []LogicalID{"seg-01"}},
			}}),
		"n-review": reviewSYFixture(t),
		"o-beta": claimAVFixture(t, "o-beta", OpinionKind, "Beta opinion", "active",
			json.RawMessage(`{"validation":{"status":"pending","method":"","evidence":[]}}`),
			[]string{"architecture"}, nil, nil),
	}}
	first, err := InspectWorkspaceDoctor(context.Background(), repository, ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	second, err := InspectWorkspaceDoctor(context.Background(), repository, ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == "" || first.Digest != second.Digest {
		t.Fatalf("doctor snapshot is not deterministic: first=%q second=%q", first.Digest, second.Digest)
	}
	if first.Counts.Entities != 3 || first.Counts.Claims != 2 || first.Counts.Edges != 1 ||
		first.Counts.Segments != 3 || first.Counts.Candidates != 1 || first.Counts.Coverage != 2 {
		t.Fatalf("doctor counts = %+v", first.Counts)
	}
	if len(first.Reviews) != 1 || first.Reviews[0].NoteID != "n-review" ||
		len(first.Reviews[0].Candidates) != 1 || len(first.Reviews[0].Coverage) != 2 {
		t.Fatalf("doctor review set = %+v", first.Reviews)
	}
	for _, viewID := range []string{ClaimsViewID, KnowledgeViewID, OpinionViewID, "hypothesis"} {
		if _, exists := first.Queries[viewID]; !exists {
			t.Fatalf("doctor omitted registered query set %q: %+v", viewID, first.Queries)
		}
	}
	if first.Projection.ClaimCount != 2 || first.Projection.EdgeCount != 1 ||
		first.Projection.ReverseCount != 1 {
		t.Fatalf("doctor projection = %+v", first.Projection)
	}

	delete(repository.documents, "n-review")
	withoutReview, err := InspectWorkspaceDoctor(context.Background(), repository, ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if withoutReview.Digest == first.Digest ||
		withoutReview.Counts.Segments != 0 ||
		withoutReview.Counts.Candidates != 0 ||
		withoutReview.Counts.Coverage != 0 {
		t.Fatalf("doctor did not bind review state: before=%+v after=%+v", first.Counts, withoutReview.Counts)
	}
}
