// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package evergreen

import (
	"context"
	"testing"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func TestRuntimeHoldsWorkspaceWriterLease(t *testing.T) {
	root := t.TempDir()
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Root: root, Holder: "kernel-test", Committer: runtimeTestCommitter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = evergreencore.AcquireOfflineLease(root, "offline-test"); !evergreencore.HasDiagnostic(err, evergreencore.CodeWorkspaceWriterActive) {
		t.Fatalf("offline writer overlap error = %v", err)
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	offline, err := evergreencore.AcquireOfflineLease(root, "offline-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = offline.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestCommandPolicyDerivesCapabilitiesFromPrincipal(t *testing.T) {
	policy := CommandPolicy{}
	if err := policy.Authorize(context.Background(),
		evergreencore.Principal{
			Type: evergreencore.PrincipalAgent, ID: "agent-1",
			AuthSource: "api-token", RequestReason: "review candidate",
		},
		"claim.update", []evergreencore.LogicalID{"c-a"}); err != nil {
		t.Fatal(err)
	}
	if err := policy.Authorize(context.Background(),
		evergreencore.Principal{
			Type: evergreencore.PrincipalAgent, ID: "agent-1",
			AuthSource: "api-token", RequestReason: "review candidate",
		},
		"workspace.delete", nil); err == nil {
		t.Fatal("agent received a client-selected high-impact capability")
	}
}

type runtimeTestCommitter struct{}

func (runtimeTestCommitter) Head(context.Context) (string, error) { return "parent", nil }
func (runtimeTestCommitter) Commit(_ context.Context, request evergreencore.CommitRequest) (string, error) {
	return "commit-" + request.OperationID, nil
}
