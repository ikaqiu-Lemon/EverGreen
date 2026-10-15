package evergreen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/evergreen/writegate"
)

func TestRuntimeBackgroundWriteSerializesCanonicalWorkspaceAliases(t *testing.T) {
	root := t.TempDir()
	before := runtimeClaimFixture(t, "c-background-gate", core.KnowledgeKind, nil)
	writeRuntimeClaim(t, root, "box/20261010120000-runtime.sy", before)
	runtime, err := NewRuntime(context.Background(), RuntimeOptions{Root: root, Committer: runtimeTestCommitter{}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	alias := filepath.Join(t.TempDir(), "workspace")
	if err = os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	release, err := writegate.Acquire(context.Background(), alias)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.writeGate.TryLock() {
		runtime.writeGate.Unlock()
		release()
		t.Fatal("ordinary write did not retain the runtime gate through its canonical alias")
	}
	snapshot, err := runtime.InspectClaim(context.Background(), "c-background-gate")
	if err != nil {
		release()
		t.Fatal(err)
	}
	user := core.Principal{Type: core.PrincipalUser, ID: "user"}
	tags := []string{"after"}
	plan, err := runtime.PlanClaimEdit(context.Background(), user, core.ClaimEditRequest{
		OperationID: "op-after-background", LogicalID: "c-background-gate", Base: snapshot.Base,
		Edit: core.ClaimEdit{Tags: &tags},
	})
	if err != nil {
		release()
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := runtime.Apply(context.Background(), user, core.ApplyRequest{Operation: plan})
		finished <- err
	}()
	select {
	case err := <-finished:
		release()
		t.Fatalf("semantic operation passed an active ordinary write: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(filepath.Join(root, core.RuntimeDirName, "transactions", plan.Plan.OperationID)); !os.IsNotExist(err) {
		release()
		t.Fatal("semantic operation prepared a journal before the ordinary write ended")
	}
	release()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ordinary write leaked its runtime gate")
	}
	release, err = writegate.Acquire(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	release()
}
