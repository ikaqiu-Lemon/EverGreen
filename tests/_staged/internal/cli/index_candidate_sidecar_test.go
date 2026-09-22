package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

func readCandidateSidecar(t *testing.T, root, noteID string) index.BlockDocument {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(index.BlocksDirPath(root), noteID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc index.BlockDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestIndexBuildAndMaterializeSynchronizeCandidateSidecar(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("index build 失败：%d %s", code, errOut)
	}
	before := readCandidateSidecar(t, dir, "n-20260922-txn-materialize")
	if len(before.Candidates) != 2 || before.Candidates[0].Status != "draft" ||
		before.Candidates[1].Status != "draft" {
		t.Fatalf("build 未写入两条 draft candidate：%+v", before)
	}
	sidecarRaw, err := os.ReadFile(filepath.Join(
		index.BlocksDirPath(dir), "n-20260922-txn-materialize.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{[]byte("事务知识。"), []byte("事务观点。")} {
		if bytes.Contains(sidecarRaw, body) {
			t.Fatalf("sidecar 不得复制 candidate 正文字节 %q", body)
		}
	}

	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 9, 22, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	code, env, errOut := runStorageV3CLI(t, root,
		"materialize", "--vault", dir, "--json", "--note",
		"n-20260922-txn-materialize", "--all", "--user-request")
	if code != ExitOK {
		t.Fatalf("materialize 失败：%d %s %+v", code, errOut, env)
	}
	after := readCandidateSidecar(t, dir, "n-20260922-txn-materialize")
	if len(after.Candidates) != 2 ||
		after.Candidates[0].Status != "materialized" ||
		after.Candidates[0].Output != "k-20260922-txn-knowledge" ||
		after.Candidates[1].Status != "materialized" ||
		after.Candidates[1].Output != "o-20260922-txn-opinion" {
		t.Fatalf("写后 sidecar 未同步持久映射：%+v", after.Candidates)
	}
	code, out, errOut := runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("index status 失败：%d %s", code, errOut)
	}
	if got := string(rcRawAt(t, []byte(out), "data", "index", "blocks", "health")); got != `"healthy"` {
		t.Fatalf("blocks health=%s，期望 healthy", got)
	}
	if len(index.TableNames()) != 6 || len(index.MetaKeys()) != 6 {
		t.Fatalf("sidecar 不得改变 SQLite schema：tables=%d meta=%d",
			len(index.TableNames()), len(index.MetaKeys()))
	}

	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	noteRaw, err := os.ReadFile(notePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notePath, append(noteRaw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("外部改 Note 后 status 失败：%d %s", code, errOut)
	}
	if got := string(rcRawAt(t, []byte(out), "data", "index", "blocks", "health")); got != `"stale"` {
		t.Fatalf("外部改 Note 后 blocks health=%s，期望 stale", got)
	}
	if code, _, errOut = runIndexCLI(t, dir, "sync"); code != ExitOK {
		t.Fatalf("index sync 未收敛 sidecar：%d %s", code, errOut)
	}
	code, out, errOut = runIndexCLI(t, dir, "status")
	if code != ExitOK {
		t.Fatalf("sync 后 status 失败：%d %s", code, errOut)
	}
	if got := string(rcRawAt(t, []byte(out), "data", "index", "blocks", "health")); got != `"healthy"` {
		t.Fatalf("sync 后 blocks health=%s，期望 healthy", got)
	}
}

func TestMaterializeSidecarFailureDoesNotRollbackAuthority(t *testing.T) {
	dir, _ := materializeTxnFixture(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("index build 失败：%d %s", code, errOut)
	}
	if err := os.RemoveAll(index.BlocksDirPath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(index.BlocksDirPath(dir), []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 9, 22, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	code, env, errOut := runStorageV3CLI(t, root,
		"materialize", "--vault", dir, "--json", "--note",
		"n-20260922-txn-materialize", "--all", "--user-request")
	if code != ExitOK {
		t.Fatalf("sidecar 失败不得改变权威写入退出码：%d %s %+v", code, errOut, env)
	}
	if !envHasWarnCode(env, index.CodeIndexStale) {
		t.Fatalf("sidecar 同步失败必须留 W22：%v", warnCodesOfEnv(env))
	}
	for _, rel := range []string{
		"domains/ai-infra/knowledge/k-20260922-txn-knowledge.md",
		"domains/ai-infra/opinions/o-20260922-txn-opinion.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("sidecar 失败不应回滚权威目标 %s：%v", rel, err)
		}
	}
}
