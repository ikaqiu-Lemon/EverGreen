package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func TestClaimsAVProviderProjectsClaimsKnowledgeAndOpinionViews(t *testing.T) {
	provider, _ := newClaimsAVProviderFixture(t)
	ctx := context.Background()

	claims, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	knowledge, err := provider.Query(ctx, KnowledgeViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	opinion, err := provider.Query(ctx, OpinionViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}

	assertRowIDs(t, claims.Rows, "k-alpha", "o-beta")
	assertRowIDs(t, knowledge.Rows, "k-alpha")
	assertRowIDs(t, opinion.Rows, "o-beta")
	if got := claims.Rows[0].Cells["claim_kind"].Text; got != "knowledge" {
		t.Fatalf("claim kind = %q", got)
	}
	if got := claims.Rows[1].Cells["validation"].Text; got != "validated" {
		t.Fatalf("opinion validation = %q", got)
	}
	if got := len(claims.Rows[0].Cells["provenance"].Provenance); got != 2 {
		t.Fatalf("knowledge provenance count = %d, want 2", got)
	}

	cell, err := provider.GetCell(ctx, claims.Rows[0].Ref, AVColumnBinding{Field: "claim.status"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cell, claims.Rows[0].Cells["status"]) {
		t.Fatalf("detail cell differs from projected row: %#v != %#v", cell, claims.Rows[0].Cells["status"])
	}
}

func TestClaimsAVProviderProjectsTypedEdgesAndIncomingOwnership(t *testing.T) {
	provider, _ := newClaimsAVProviderFixture(t)
	ctx := context.Background()
	page, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	alpha := rowByID(t, page.Rows, "k-alpha")
	beta := rowByID(t, page.Rows, "o-beta")

	outgoing := alpha.Cells["arguments_outgoing"]
	if outgoing.EdgeCount != 2 || outgoing.UniqueTargetCount != 1 || len(outgoing.Edges) != 2 {
		t.Fatalf("outgoing metrics = edges:%d unique:%d values:%d", outgoing.EdgeCount, outgoing.UniqueTargetCount, len(outgoing.Edges))
	}
	if outgoing.Edges[0].ID == outgoing.Edges[1].ID {
		t.Fatal("duplicate targets lost their independent edge identities")
	}
	incoming := beta.Cells["arguments_incoming"]
	if incoming.EdgeCount != 2 || incoming.UniqueTargetCount != 1 || len(incoming.Edges) != 2 {
		t.Fatalf("incoming metrics = edges:%d unique:%d values:%d", incoming.EdgeCount, incoming.UniqueTargetCount, len(incoming.Edges))
	}
	for _, edge := range incoming.Edges {
		if edge.OwnerID != "k-alpha" || edge.Direction != EdgeDirectionIncoming || edge.OwnerHash != alpha.Ref.SemanticHash {
			t.Fatalf("incoming edge ownership is incomplete: %#v", edge)
		}
	}
}

func TestClaimsAVProviderFilterSortGroupEmptyAndCountSemantics(t *testing.T) {
	provider, _ := newClaimsAVProviderFixture(t)
	ctx := context.Background()

	grouped, err := provider.Query(ctx, ClaimsViewID, AVQuery{
		Sorts:   []AVSort{{Column: "claim_kind", Direction: "desc"}},
		GroupBy: "claim_kind",
		Limit:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if grouped.Total != 2 || len(grouped.Rows) != 1 || grouped.Rows[0].Ref.LogicalID != "o-beta" {
		t.Fatalf("sort/page result = total:%d rows:%#v", grouped.Total, grouped.Rows)
	}
	if grouped.GroupCounts["knowledge"] != 1 || grouped.GroupCounts["opinion"] != 1 {
		t.Fatalf("kind groups = %#v", grouped.GroupCounts)
	}

	withOutgoing, err := provider.Query(ctx, ClaimsViewID, AVQuery{Filters: []AVFilter{{
		Column: "arguments_outgoing", Operator: "not_empty",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	assertRowIDs(t, withOutgoing.Rows, "k-alpha")
	if cell := withOutgoing.Rows[0].Cells["arguments_outgoing"]; cell.EdgeCount != 2 || cell.UniqueTargetCount != 1 {
		t.Fatalf("relation rollup metrics = %#v", cell)
	}

	withoutOutgoing, err := provider.Query(ctx, ClaimsViewID, AVQuery{Filters: []AVFilter{{
		Column: "arguments_outgoing", Operator: "empty",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	assertRowIDs(t, withoutOutgoing.Rows, "o-beta")
}

func TestClaimsAVProviderPlansOwnerSideTypedEdgeMutations(t *testing.T) {
	provider, planner := newClaimsAVProviderFixture(t)
	ctx := context.Background()
	page, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	alpha := rowByID(t, page.Rows, "k-alpha")
	beta := rowByID(t, page.Rows, "o-beta")

	planned, err := provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-update-incoming",
		RowID:       beta.Ref.LogicalID,
		Base:        BaseRef{LogicalID: alpha.Ref.LogicalID, SemanticHash: alpha.Ref.SemanticHash},
		Column: AVColumnBinding{
			Direction:  EdgeDirectionIncoming,
			EdgeSchema: ArgumentSchema,
		},
		Edge: AVEdgePatch{
			Action:  AVEdgeUpdate,
			OwnerID: "k-alpha",
			Edge: TypedEdge{
				ID:     "edge-duplicate-2",
				Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"},
				Type:   "limits",
				Reason: "The evidence only bounds the conclusion.",
				Context: EdgeContext{
					NoteID:      "n-two",
					SegmentRefs: []LogicalID{"seg-two"},
					QuoteHash:   "sha256:updated",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if planned.Plan.Command != "claim.edge.update" || len(planned.Plan.Writes) != 1 ||
		planned.Plan.Writes[0].LogicalID != "k-alpha" {
		t.Fatalf("incoming edit did not compile to owner-side write: %#v", planned.Plan)
	}
	if planner.lastRequest.Command != "claim.edge.update" {
		t.Fatalf("planner command = %q", planner.lastRequest.Command)
	}
	after := decodeClaimFixture(t, planner.lastRequest.Writes[0].After)
	first := edgeByID(t, after.Envelope.Relations.Outgoing, "edge-duplicate-1")
	second := edgeByID(t, after.Envelope.Relations.Outgoing, "edge-duplicate-2")
	if first.Type != "supports" || second.Type != "limits" || second.Context.QuoteHash != "sha256:updated" {
		t.Fatalf("edge-level update changed the wrong fact: first=%#v second=%#v", first, second)
	}
	if second.CreatedAt != "2026-10-10T12:00:00Z" || second.UpdatedAt != "2026-10-10T12:30:00Z" ||
		string(second.Extension["com.example.edge/v1"]) != `{"kept":true}` ||
		string(second.Extra["future_edge_field"]) != `"preserved"` ||
		string(second.Context.Data["future_context"]) != `"preserved"` {
		t.Fatalf("edge update lost source-only metadata: %#v", second)
	}

	if _, err = provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-stale",
		RowID:       "k-alpha",
		Base:        BaseRef{LogicalID: "k-alpha", SemanticHash: "sha256:stale"},
		Column:      AVColumnBinding{Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema},
		Edge: AVEdgePatch{
			Action: AVEdgeDelete,
			Edge:   TypedEdge{ID: "edge-duplicate-1"},
		},
	}); !HasDiagnostic(err, CodeBaseMismatch) {
		t.Fatalf("stale base error = %v", err)
	}
}

func TestClaimsAVProviderPlansSourceBackedScalarPatch(t *testing.T) {
	provider, planner := newClaimsAVProviderFixture(t)
	ctx := context.Background()
	page, err := provider.Query(ctx, OpinionViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	beta := rowByID(t, page.Rows, "o-beta")
	_, err = provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-validation",
		RowID:       "o-beta",
		Base:        BaseRef{LogicalID: "o-beta", SemanticHash: beta.Ref.SemanticHash},
		Column:      AVColumnBinding{Field: "claim.kind_data.validation"},
		Value: &AVCellValue{
			Kind: "json",
			JSON: json.RawMessage(`{"status":"pending","method":"manual-review","evidence":[]}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	after := decodeClaimFixture(t, planner.lastRequest.Writes[0].After)
	validation, status := claimValidation(after.Envelope.Claim.KindData)
	if status != "pending" || !json.Valid(validation) {
		t.Fatalf("validation after patch = status:%q value:%s", status, validation)
	}
	if planner.lastRequest.Command != "claim.update" {
		t.Fatalf("scalar patch command = %q", planner.lastRequest.Command)
	}
}

func TestClaimsAVProviderAddDeleteReorderAndSchemaValidation(t *testing.T) {
	provider, planner := newClaimsAVProviderFixture(t)
	ctx := context.Background()
	page, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	alpha := rowByID(t, page.Rows, "k-alpha")

	_, err = provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-add",
		RowID:       "k-alpha",
		Base:        BaseRef{LogicalID: "k-alpha", SemanticHash: alpha.Ref.SemanticHash},
		Column:      AVColumnBinding{Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema},
		Edge: AVEdgePatch{
			Action: AVEdgeAdd,
			Edge: TypedEdge{
				ID:     "edge-new",
				Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"},
				Type:   "derives",
				Reason: "The source material directly entails this claim.",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	afterAdd := decodeClaimFixture(t, planner.lastRequest.Writes[0].After)
	if len(afterAdd.Envelope.Relations.Outgoing) != 3 {
		t.Fatalf("edge count after add = %d", len(afterAdd.Envelope.Relations.Outgoing))
	}

	_, err = provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-delete",
		RowID:       "k-alpha",
		Base:        BaseRef{LogicalID: "k-alpha", SemanticHash: alpha.Ref.SemanticHash},
		Column:      AVColumnBinding{Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema},
		Edge: AVEdgePatch{
			Action: AVEdgeDelete,
			Edge:   TypedEdge{ID: "edge-duplicate-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	afterDelete := decodeClaimFixture(t, planner.lastRequest.Writes[0].After)
	if len(afterDelete.Envelope.Relations.Outgoing) != 1 ||
		afterDelete.Envelope.Relations.Outgoing[0].ID != "edge-duplicate-2" {
		t.Fatalf("delete did not address one stable edge ID: %#v", afterDelete.Envelope.Relations.Outgoing)
	}

	_, err = provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-reorder",
		RowID:       "k-alpha",
		Base:        BaseRef{LogicalID: "k-alpha", SemanticHash: alpha.Ref.SemanticHash},
		Column:      AVColumnBinding{Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema},
		Edge: AVEdgePatch{
			Action: AVEdgeReorder,
			Order:  []EdgeID{"edge-duplicate-2", "edge-duplicate-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	afterReorder := decodeClaimFixture(t, planner.lastRequest.Writes[0].After)
	if got := afterReorder.Envelope.Relations.Outgoing[0].ID; got != "edge-duplicate-2" {
		t.Fatalf("first reordered edge = %q", got)
	}
	reorderedHash, err := SemanticHash(planner.lastRequest.Writes[0].After)
	if err != nil {
		t.Fatal(err)
	}
	if reorderedHash != alpha.Ref.SemanticHash || afterReorder.Envelope.Entity.SemanticRevision != 7 {
		t.Fatalf("display-only reorder changed semantic identity: hash=%s revision=%d", reorderedHash, afterReorder.Envelope.Entity.SemanticRevision)
	}

	_, err = provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-invalid",
		RowID:       "k-alpha",
		Base:        BaseRef{LogicalID: "k-alpha", SemanticHash: alpha.Ref.SemanticHash},
		Column:      AVColumnBinding{Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema},
		Edge: AVEdgePatch{
			Action: AVEdgeAdd,
			Edge: TypedEdge{
				ID:     "edge-invalid",
				Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"},
				Type:   "invented",
				Reason: "Invalid closed-enum type.",
			},
		},
	})
	if !HasDiagnostic(err, CodeInvalidEdge) {
		t.Fatalf("invalid edge type error = %v", err)
	}

	_, err = provider.PlanPatch(ctx, Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-invalid-target-kind",
		RowID:       "k-alpha",
		Base:        BaseRef{LogicalID: "k-alpha", SemanticHash: alpha.Ref.SemanticHash},
		Column: AVColumnBinding{
			Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema, TargetKind: KnowledgeKind,
		},
		Edge: AVEdgePatch{
			Action: AVEdgeAdd,
			Edge: TypedEdge{
				ID: "edge-invalid-kind", Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"},
				Type:   "supports", Reason: "The target kind violates the column constraint.",
			},
		},
	})
	if !HasDiagnostic(err, CodeInvalidEdge) {
		t.Fatalf("invalid target kind error = %v", err)
	}
}

func TestClaimsAVProviderMovesOpposingEdgeToCanonicalOwner(t *testing.T) {
	repository := &claimsAVRepositoryStub{documents: map[LogicalID][]byte{
		"c-a": claimAVFixture(t, "c-a", KnowledgeKind, "Canonical low", "active",
			json.RawMessage(`{}`), nil, nil, nil),
		"c-z": claimAVFixture(t, "c-z", KnowledgeKind, "Canonical high", "active",
			json.RawMessage(`{}`), nil, nil, []TypedEdge{{
				ID: "edge-move", Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-a"},
				Type:   "supports", Reason: "Starts as a directed edge.",
			}}),
	}}
	planner := &claimsAVPlannerStub{}
	provider := NewClaimsAVProvider(repository, planner, DefaultRegistry())
	page, err := provider.Query(context.Background(), ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	high := rowByID(t, page.Rows, "c-z")
	planned, err := provider.PlanPatch(context.Background(), Principal{Type: PrincipalUser, ID: "user-1"}, AVPatch{
		OperationID: "op-opposing-owner-move",
		RowID:       "c-z",
		Base:        BaseRef{LogicalID: "c-z", SemanticHash: high.Ref.SemanticHash},
		Column:      AVColumnBinding{Direction: EdgeDirectionOutgoing, EdgeSchema: ArgumentSchema},
		Edge: AVEdgePatch{
			Action: AVEdgeUpdate,
			Edge: TypedEdge{
				ID: "edge-move", Schema: ArgumentSchema,
				Target: EntityRef{EntityType: EntityClaim, LogicalID: "c-a"},
				Type:   "opposing", Reason: "Opposition uses canonical ownership.",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.Plan.Writes) != 2 {
		t.Fatalf("opposing owner move writes = %#v", planned.Plan.Writes)
	}
	afterByID := map[LogicalID]*SYDocument{}
	for _, write := range planner.lastRequest.Writes {
		afterByID[write.LogicalID] = decodeClaimFixture(t, write.After)
	}
	if edges := afterByID["c-z"].Envelope.Relations.Outgoing; len(edges) != 0 {
		t.Fatalf("old owner retained opposing edge: %#v", edges)
	}
	edges := afterByID["c-a"].Envelope.Relations.Outgoing
	if len(edges) != 1 || edges[0].ID != "edge-move" || edges[0].Type != "opposing" ||
		edges[0].Target.LogicalID != "c-z" {
		t.Fatalf("canonical owner edge = %#v", edges)
	}
}

func TestClaimsAVProviderRebuildIsSourceCompleteAndDeterministic(t *testing.T) {
	provider, _ := newClaimsAVProviderFixture(t)
	ctx := context.Background()
	before, err := provider.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforePage, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	provider.DropDerived()
	after, err := provider.Rebuild(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterPage, err := provider.Query(ctx, ClaimsViewID, AVQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if before.Digest != after.Digest {
		t.Fatalf("rebuild digest changed: %s != %s", before.Digest, after.Digest)
	}
	if got, want := mustJSON(t, afterPage), mustJSON(t, beforePage); got != want {
		t.Fatalf("rebuilt projection differs:\n%s\nwant:\n%s", got, want)
	}
}

type claimsAVPlannerStub struct {
	lastRequest PlanRequest
}

func (s *claimsAVPlannerStub) Plan(_ context.Context, principal Principal, request PlanRequest) (PlannedOperation, error) {
	s.lastRequest = request
	writes := make([]PlannedWrite, 0, len(request.Writes))
	images := make([]AfterImage, 0, len(request.Writes))
	for index, write := range request.Writes {
		hash, err := SemanticHash(write.After)
		if err != nil {
			return PlannedOperation{}, err
		}
		writes = append(writes, PlannedWrite{
			LogicalID: write.LogicalID,
			Before:    request.Base[index].SemanticHash,
			After:     hash,
		})
		images = append(images, AfterImage{
			LogicalID:    write.LogicalID,
			Path:         write.Path,
			Bytes:        write.After,
			SemanticHash: hash,
			ContentHash:  contentHash(write.After),
		})
	}
	plan := ChangePlan{
		OperationID: request.OperationID,
		Protocol:    request.Protocol,
		Principal:   principal,
		Command:     request.Command,
		Base:        request.Base,
		Writes:      writes,
	}
	operation := PlannedOperation{Plan: plan, Images: images}
	operation.PlanHash = ComputePlanHash(plan, images)
	return operation, nil
}

type claimsAVRepositoryStub struct {
	documents map[LogicalID][]byte
}

func (s *claimsAVRepositoryStub) Load(_ context.Context, id LogicalID) ([]byte, error) {
	raw, ok := s.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), raw...), nil
}

func (s *claimsAVRepositoryStub) List(context.Context) ([]LogicalID, error) {
	ids := make([]LogicalID, 0, len(s.documents))
	for id := range s.documents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func newClaimsAVProviderFixture(t *testing.T) (*ClaimsAVProvider, *claimsAVPlannerStub) {
	t.Helper()
	repository := &claimsAVRepositoryStub{documents: map[LogicalID][]byte{
		"k-alpha": claimAVFixture(t, "k-alpha", KnowledgeKind, "Alpha knowledge", "active",
			json.RawMessage(`{}`),
			[]string{"architecture", "storage"},
			[]Provenance{
				{SourceID: "s-one", NoteID: "n-one", SegmentRefs: []LogicalID{"seg-one"}, Reason: "Primary evidence."},
				{SourceID: "s-two", NoteID: "n-two", SegmentRefs: []LogicalID{"seg-two"}, Reason: "Independent confirmation."},
			},
			[]TypedEdge{
				{
					ID: "edge-duplicate-1", Schema: ArgumentSchema,
					Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"},
					Type:   "supports", Reason: "First independent support.",
					Context: EdgeContext{NoteID: "n-one", SegmentRefs: []LogicalID{"seg-one"}},
				},
				{
					ID: "edge-duplicate-2", Schema: ArgumentSchema,
					Target: EntityRef{EntityType: EntityClaim, LogicalID: "o-beta"},
					Type:   "supports", Reason: "Second independent support.",
					Context: EdgeContext{
						NoteID: "n-two", SegmentRefs: []LogicalID{"seg-two"},
						Data: RawObject{"future_context": json.RawMessage(`"preserved"`)},
					},
					Extension: RawObject{"com.example.edge/v1": json.RawMessage(`{"kept":true}`)},
					CreatedAt: "2026-10-10T12:00:00Z",
					UpdatedAt: "2026-10-10T12:30:00Z",
					Extra:     RawObject{"future_edge_field": json.RawMessage(`"preserved"`)},
				},
			}),
		"o-beta": claimAVFixture(t, "o-beta", OpinionKind, "Beta opinion", "active",
			json.RawMessage(`{"validation":{"status":"validated","method":"triangulation","evidence":[]}}`),
			[]string{"architecture"},
			[]Provenance{
				{SourceID: "s-two", NoteID: "n-two", SegmentRefs: []LogicalID{"seg-two"}, Reason: "Opinion context."},
			},
			nil),
	}}
	planner := &claimsAVPlannerStub{}
	return NewClaimsAVProvider(repository, planner, DefaultRegistry()), planner
}

func claimAVFixture(
	t *testing.T,
	id LogicalID,
	kind ClaimKind,
	title string,
	status string,
	kindData json.RawMessage,
	tags []string,
	provenance []Provenance,
	edges []TypedEdge,
) []byte {
	t.Helper()
	kindSchema := KnowledgeKindSchema
	if kind == OpinionKind {
		kindSchema = OpinionKindSchema
	}
	envelope := &DocumentEnvelope{
		Spec: DocumentSpec,
		Entity: Entity{
			LogicalID: id, EntityType: EntityClaim, Schema: ClaimSchema, SemanticRevision: 7,
		},
		Claim: &Claim{
			ClaimKind: kind, KindSchema: kindSchema, Status: status, Tags: tags, KindData: kindData,
		},
		Provenance: provenance,
		Relations:  Relations{Outgoing: edges},
	}
	base := []byte(fmt.Sprintf(`{
		"ID":"20261010120000-%s",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[{"ID":"20261010120001-child","Type":"NodeHeading","Data":%q}]
	}`, id, title))
	raw, err := EncodeSY(base, envelope, DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertRowIDs(t *testing.T, rows []AVRow, expected ...LogicalID) {
	t.Helper()
	actual := make([]LogicalID, 0, len(rows))
	for _, row := range rows {
		actual = append(actual, row.Ref.LogicalID)
	}
	if fmt.Sprint(actual) != fmt.Sprint(expected) {
		t.Fatalf("row IDs = %v, want %v", actual, expected)
	}
}

func rowByID(t *testing.T, rows []AVRow, id LogicalID) AVRow {
	t.Helper()
	for _, row := range rows {
		if row.Ref.LogicalID == id {
			return row
		}
	}
	t.Fatalf("row %q not found", id)
	return AVRow{}
}

func edgeByID(t *testing.T, edges []TypedEdge, id EdgeID) TypedEdge {
	t.Helper()
	for _, edge := range edges {
		if edge.ID == id {
			return edge
		}
	}
	t.Fatalf("edge %q not found", id)
	return TypedEdge{}
}

func decodeClaimFixture(t *testing.T, raw []byte) *SYDocument {
	t.Helper()
	document, err := DecodeSY(raw, DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
