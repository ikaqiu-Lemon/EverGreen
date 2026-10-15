//go:build fts5

package model

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/filesys"
	"github.com/siyuan-note/siyuan/kernel/sql"
	"github.com/siyuan-note/siyuan/kernel/treenode"
	"github.com/siyuan-note/siyuan/kernel/util"
)

type doctorCommitter struct{ commits int }

func (c *doctorCommitter) Head(context.Context) (string, error) { return "parent", nil }
func (c *doctorCommitter) Commit(_ context.Context, _ core.CommitRequest) (string, error) {
	c.commits++
	return "unexpected", nil
}

func TestEvergreenDoctorRepairsRealSQLAndBlocktreeFromSY(t *testing.T) {
	if os.Getenv("SIYUAN_TEST_EG_DOCTOR") != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0],
			"-test.run=^TestEvergreenDoctorRepairsRealSQLAndBlocktreeFromSY$", "-test.timeout=80s", "-test.v")
		command.Env = append(os.Environ(), "SIYUAN_TEST_EG_DOCTOR=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated doctor: %v\n%s", err, output)
		}
		return
	}
	root := t.TempDir()
	util.WorkspaceDir = root
	util.DataDir, util.TempDir, util.ConfDir = filepath.Join(root, "data"), filepath.Join(root, "temp"), filepath.Join(root, "conf")
	util.QueueDir = filepath.Join(util.TempDir, "queue")
	util.DBPath, util.BlockTreeDBPath = filepath.Join(util.TempDir, util.DBName), filepath.Join(util.TempDir, "blocktree.db")
	for _, directory := range []string{util.DataDir, util.TempDir, util.ConfDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sql.InitDatabase(true)

	const boxID = "20261010100000-box0001"
	raw := doctorClaimFixture(t)
	relative := boxID + "/20261010120000-runtime.sy"
	path := filepath.Join(util.DataDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	committer := &doctorCommitter{}
	runtime, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: util.DataDir, Committer: committer,
		Storage:        filesys.EvergreenDocumentStorage{Root: util.DataDir, Boxes: []string{boxID}},
		InspectDerived: InspectEvergreenDerivedProjection,
		RebuildDerived: RebuildEvergreenDocumentProjection,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	archiveBefore, err := runtime.CanonicalExport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	report, err := runtime.Doctor(context.Background(), true)
	if err != nil || report.DerivedBefore.Healthy || !report.DerivedAfter.Healthy || !report.Parity {
		t.Fatalf("initial rebuild = %+v err=%v", report, err)
	}
	const rootID = "20261010120000-runtime"
	const childID = "20261010120001-child"
	if block := sql.GetBlock(childID); block == nil || block.RootID != rootID {
		t.Fatalf("SQL projection missing after doctor: %+v", block)
	}
	if block := treenode.GetBlockTreeInBox(childID, boxID); block == nil || block.RootID != rootID {
		t.Fatalf("blocktree projection missing after doctor: %+v", block)
	}
	ftsIDs, err := sql.AuthorityProjectionFTSBlockIDs(context.Background(), boxID, rootID)
	if err != nil || !containsDoctorID(ftsIDs, childID) {
		t.Fatalf("FTS projection missing after doctor: ids=%v err=%v", ftsIDs, err)
	}
	if err = sql.Exec("DELETE FROM blocks WHERE root_id = ?", rootID); err != nil {
		t.Fatal(err)
	}
	if err = treenode.RemoveAuthorityBlockTree(boxID, rootID); err != nil {
		t.Fatal(err)
	}
	report, err = runtime.Doctor(context.Background(), false)
	if !core.HasDiagnostic(err, core.CodeDerivedStale) || report.DerivedBefore.Healthy {
		t.Fatalf("corrupt projection was not detected: %+v err=%v", report, err)
	}
	report, err = runtime.Doctor(context.Background(), true)
	if err != nil || !report.DerivedAfter.Healthy || !report.Parity {
		t.Fatalf("corrupt projection rebuild = %+v err=%v", report, err)
	}
	archiveAfter, err := runtime.CanonicalExport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	diff, err := core.DiffCanonical(archiveBefore, archiveAfter)
	if err != nil || len(diff) != 0 || committer.commits != 0 {
		t.Fatalf("doctor changed authority or Git: diff=%+v commits=%d err=%v", diff, committer.commits, err)
	}
}

func containsDoctorID(ids []string, expected string) bool {
	for _, id := range ids {
		if id == expected {
			return true
		}
	}
	return false
}

func doctorClaimFixture(t *testing.T) []byte {
	t.Helper()
	base := []byte(`{
		"ID":"20261010120000-runtime",
		"Type":"NodeDocument",
		"Spec":"5",
		"Children":[{"ID":"20261010120001-child","Type":"NodeParagraph","Data":"Doctor projection needle"}]
	}`)
	raw, err := core.EncodeSY(base, &core.DocumentEnvelope{
		Spec: core.DocumentSpec,
		Entity: core.Entity{
			LogicalID: "c-doctor-real", EntityType: core.EntityClaim,
			Schema: core.ClaimSchema, SemanticRevision: 1,
		},
		Claim: &core.Claim{
			ClaimKind: core.KnowledgeKind, KindSchema: core.KnowledgeKindSchema,
			Status: "active", KindData: json.RawMessage(`{}`),
		},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
	}, core.ApplicationRegistry())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
