// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
// SPDX-License-Identifier: AGPL-3.0-or-later

package evergreen

import (
	"context"
	"errors"
	"testing"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

type doctorOneShotFault struct {
	point string
	fired bool
}

func (f *doctorOneShotFault) Fail(point string, _ int) error {
	if point == f.point && !f.fired {
		f.fired = true
		return errors.New("doctor test interruption")
	}
	return nil
}

func TestRuntimeDoctorChecksJournalBeforeInspectAndRebuild(t *testing.T) {
	root := t.TempDir()
	before := runtimeClaimFixture(t, "c-doctor", core.KnowledgeKind, nil)
	writeRuntimeClaim(t, root, "box/20261010120000-runtime.sy", before)
	fault := &doctorOneShotFault{point: core.FaultAfterRename}
	inspectCalls, rebuildCalls := 0, 0
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Root: root, Committer: &runtimeAVCommitter{}, Faults: fault,
		InspectDerived: func(context.Context, []core.AuthorityDocumentRef) (core.DerivedProjectionStatus, error) {
			inspectCalls++
			return core.DerivedProjectionStatus{Available: true, Healthy: true}, nil
		},
		RebuildDerived: func(context.Context, []core.AuthorityDocumentRef) error {
			rebuildCalls++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	document, err := core.DecodeSY(before, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	document.Envelope.Claim.Tags = []string{"doctor"}
	document.Envelope.Entity.SemanticRevision++
	after, err := core.EncodeSY(before, document.Envelope, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	base, _ := core.SemanticHash(before)
	principal := core.Principal{Type: core.PrincipalUser, ID: "owner", AuthSource: "workspace-session"}
	planned, err := runtime.Plan(context.Background(), principal, core.PlanRequest{
		OperationID: "op-doctor-interrupted", Protocol: core.ChangePlanProtocol, Command: "claim.update",
		Base:   []core.BaseRef{{LogicalID: "c-doctor", SemanticHash: base}},
		Writes: []core.WriteInput{{LogicalID: "c-doctor", After: after}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Apply(context.Background(), principal, core.ApplyRequest{Operation: planned}); err == nil {
		t.Fatal("fault injection did not leave an unfinished journal")
	}
	if _, err = runtime.Doctor(context.Background(), true); !core.HasDiagnostic(err, core.CodeTransactionBlocked) {
		t.Fatalf("doctor bypassed unfinished journal: %v", err)
	}
	if inspectCalls != 0 || rebuildCalls != 0 {
		t.Fatalf("doctor touched derived projections before journal recovery: inspect=%d rebuild=%d",
			inspectCalls, rebuildCalls)
	}
	records, err := runtime.Recover(context.Background())
	if err != nil || len(records) != 1 || records[0].State != core.OperationCompleted {
		t.Fatalf("journal recovery = %+v err=%v", records, err)
	}
	report, err := runtime.Doctor(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Parity || !report.DerivedAfter.Healthy || inspectCalls != 2 || rebuildCalls != 1 {
		t.Fatalf("doctor report=%+v inspect=%d rebuild=%d", report, inspectCalls, rebuildCalls)
	}
}

func TestRuntimeDoctorReportsStaleProjectionBeforeOptionalRebuild(t *testing.T) {
	root := t.TempDir()
	before := runtimeClaimFixture(t, "c-doctor-stale", core.KnowledgeKind, nil)
	writeRuntimeClaim(t, root, "box/20261010120000-runtime.sy", before)
	healthy := false
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Root: root, Committer: &runtimeAVCommitter{},
		InspectDerived: func(context.Context, []core.AuthorityDocumentRef) (core.DerivedProjectionStatus, error) {
			return core.DerivedProjectionStatus{
				Available: true, Healthy: healthy,
				ExpectedDigest: "sha256:expected", ActualDigest: "sha256:actual",
			}, nil
		},
		RebuildDerived: func(context.Context, []core.AuthorityDocumentRef) error {
			healthy = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	report, err := runtime.Doctor(context.Background(), false)
	if !core.HasDiagnostic(err, core.CodeDerivedStale) || report.DerivedBefore.Healthy {
		t.Fatalf("stale doctor = %+v err=%v", report, err)
	}
	report, err = runtime.Doctor(context.Background(), true)
	if err != nil || !report.Parity || !report.DerivedAfter.Healthy {
		t.Fatalf("rebuilt doctor = %+v err=%v", report, err)
	}
}
