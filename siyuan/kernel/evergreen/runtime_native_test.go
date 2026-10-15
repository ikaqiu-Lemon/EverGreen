package evergreen

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

type nativePlanBarrier struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *nativePlanBarrier) Authorize(ctx context.Context, principal core.Principal, command string, ids []core.LogicalID) error {
	if command == core.NativeEditCommand {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	return (core.CommandPolicy{}).Authorize(ctx, principal, command, ids)
}

func TestRuntimeNativeBatchSerializesPlanAndCompetingApply(t *testing.T) {
	root := t.TempDir()
	relative := "box/20261010120000-runtime.sy"
	before := runtimeClaimFixture(t, "c-native-gate", core.KnowledgeKind, nil)
	writeRuntimeClaim(t, root, relative, before)
	if output, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("Git setup: %v %s", err, output)
	}
	committer, err := core.NewLocalGitCommitter(root)
	if err != nil {
		t.Fatal(err)
	}
	barrier := &nativePlanBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	defer release()
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{
		Root: root, Committer: committer, Authorizer: barrier,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		release()
		runtime.Close()
	}()
	user := core.Principal{Type: core.PrincipalUser, ID: "owner", AuthSource: "workspace-session"}
	base, _ := core.SemanticHash(before)
	decoded, err := core.DecodeSY(before, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	decoded.Envelope.Claim.Status = "archived"
	decoded.Envelope.Entity.SemanticRevision++
	rivalRaw, err := core.EncodeSY(before, decoded.Envelope, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	rival, err := runtime.Plan(context.Background(), user, core.PlanRequest{
		OperationID: "op-competing-apply", Protocol: core.ChangePlanProtocol, Command: "claim.update",
		Base:   []core.BaseRef{{LogicalID: "c-native-gate", SemanticHash: base}},
		Writes: []core.WriteInput{{LogicalID: "c-native-gate", After: rivalRaw}},
	})
	if err != nil {
		t.Fatal(err)
	}
	native, err := core.PrepareNativeDocumentEdit(before,
		bytes.ReplaceAll(before, []byte("Runtime claim"), []byte("Native claim")), core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	nativeResult := make(chan error, 1)
	go func() {
		_, err := runtime.ApplyNativeBatch(context.Background(), user, core.PlanRequest{
			OperationID: "op-native-gate", Protocol: core.ChangePlanProtocol,
			Base:   []core.BaseRef{{LogicalID: "c-native-gate", SemanticHash: base}},
			Writes: []core.WriteInput{{LogicalID: "c-native-gate", After: native}},
		}, nil)
		nativeResult <- err
	}()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("native plan did not enter authorization")
	}
	if runtime.writeGate.TryLock() {
		runtime.writeGate.Unlock()
		release()
		<-nativeResult
		t.Fatal("competing writer can enter while native planning is in progress")
	}
	rivalResult := make(chan error, 1)
	go func() {
		_, err := runtime.Apply(context.Background(), user, core.ApplyRequest{Operation: rival})
		rivalResult <- err
	}()
	release()
	if err = <-nativeResult; err != nil {
		t.Fatal(err)
	}
	if err = <-rivalResult; !core.HasDiagnostic(err, core.CodeBaseMismatch) {
		t.Fatalf("competing stale apply was admitted: %v", err)
	}
	archive, err := runtime.CanonicalExport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(archive)
	if err != nil || !bytes.Contains(raw, []byte("Native claim")) || bytes.Contains(raw, []byte("archived")) {
		t.Fatalf("competing writer overwrote native authority: %v", err)
	}
	output, err := exec.Command("git", "-C", root, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "1" {
		t.Fatalf("native operation commit count: %v %s", err, output)
	}
}
