package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

func runStorageV3CLI(t *testing.T, r *Root, args ...string) (int, Envelope, string) {
	t.Helper()
	code, out, errOut := runCLI(t, r, args...)
	var env Envelope
	if out != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("JSON 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

func TestMaterializeCommandRealKnowledgeOpinionClosure(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	before := mustRead(t, notePath)
	beforeCandidates, err := mdfile.ParseCandidates(before)
	if err != nil {
		t.Fatal(err)
	}
	commitsBefore := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD"))

	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 9, 22, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	code, env, errOut := runStorageV3CLI(t, root,
		"materialize", "--vault", dir, "--json", "--note",
		"n-20260922-txn-materialize", "--all", "--user-request")
	if code != ExitOK {
		t.Fatalf("materialize 退出码=%d：%s %+v", code, errOut, env)
	}
	items, ok := env.Data["materialized_candidates"].([]interface{})
	if !ok || len(items) != 2 || env.Data["finalized"] != true {
		t.Fatalf("materialize 回执不完整：%+v", env.Data)
	}
	kPath := filepath.Join(dir, "domains/ai-infra/knowledge/k-20260922-txn-knowledge.md")
	oPath := filepath.Join(dir, "domains/ai-infra/opinions/o-20260922-txn-opinion.md")
	if got := mustRead(t, kPath); !bytes.Contains(got, []byte("## 知识内容\n\n事务知识。\n")) {
		t.Fatalf("Knowledge 未逐字承接 candidate payload：\n%s", got)
	}
	if got := mustRead(t, oPath); !bytes.Contains(got, []byte("validation: 'pending'")) ||
		!bytes.Contains(got, []byte("## 观点\n\n事务观点。\n")) {
		t.Fatalf("Opinion 未以 pending 逐字承接 candidate payload：\n%s", got)
	}
	after := mustRead(t, notePath)
	afterCandidates, err := mdfile.ParseCandidates(after)
	if err != nil {
		t.Fatal(err)
	}
	for i := range beforeCandidates {
		if !bytes.Equal(beforeCandidates[i].Raw(before), afterCandidates[i].Raw(after)) {
			t.Fatalf("candidate %s 的 H3/payload 被物化改写", beforeCandidates[i].Key)
		}
	}
	if commitsAfter := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD")); commitsAfter == commitsBefore {
		t.Fatal("首次 materialize 必须产生 Git commit")
	}

	root.Now = func() time.Time {
		return time.Date(2026, 9, 30, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	beforeReplay := gitOut(t, dir, "rev-list", "--count", "HEAD")
	code, replay, errOut := runStorageV3CLI(t, root,
		"materialize", "--vault", dir, "--json", "--note",
		"n-20260922-txn-materialize", "--all", "--user-request")
	if code != ExitOK {
		t.Fatalf("跨日重跑失败：%d %s %+v", code, errOut, replay)
	}
	if replay.Data["txn_id"] != "" || replay.Data["commit"] != nil {
		t.Fatalf("跨日重跑必须是零 txn / 零 commit no-op：%+v", replay.Data)
	}
	if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != beforeReplay {
		t.Fatal("跨日重跑产生了空 commit")
	}
}

func TestMaterializeCommandAuthorizationAndSelection(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	before := mustRead(t, filepath.Join(dir, filepath.FromSlash(noteRel)))
	cases := [][]string{
		{"materialize", "--vault", dir, "--json", "--note",
			"n-20260922-txn-materialize", "--all"},
		{"materialize", "--vault", dir, "--json", "--note",
			"n-20260922-txn-materialize", "--all", "--candidate", "cand-k", "--user-request"},
		{"materialize", "--vault", dir, "--json", "--note",
			"bad", "--all", "--user-request"},
	}
	want := []int{ExitValidation, ExitUsage, ExitUsage}
	for i, args := range cases {
		code, _, _ := runStorageV3CLI(t, newTestRoot(t, dir), args...)
		if code != want[i] {
			t.Fatalf("case %d exit=%d want=%d", i, code, want[i])
		}
		if got := mustRead(t, filepath.Join(dir, filepath.FromSlash(noteRel))); !bytes.Equal(got, before) {
			t.Fatalf("case %d 拒绝后改写了 Note", i)
		}
	}
}

func TestExportPlainWritesOutsideVaultAndPreservesVisibleContent(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	beforeVault := snapshot(t, dir)
	beforeLog := gitOut(t, dir, "log", "--oneline")
	out := filepath.Join(t.TempDir(), "plain")
	code, env, errOut := runStorageV3CLI(t, newTestRoot(t, dir),
		"export", "--vault", dir, "--json", "--plain", "--output", out)
	if code != ExitOK {
		t.Fatalf("export 退出码=%d：%s %+v", code, errOut, env)
	}
	exported := mustRead(t, filepath.Join(out, filepath.FromSlash(noteRel)))
	for _, forbidden := range []string{
		"<!-- eg:nr:", "<!-- eg:cd:", "<!-- eg:cc:", "<!-- eg:nc:",
		".eg-candidate", "#cand-",
	} {
		if bytes.Contains(exported, []byte(forbidden)) {
			t.Fatalf("plain export 残留 %q：\n%s", forbidden, exported)
		}
	}
	for _, visible := range []string{
		"知识来源。", "观点来源。", "### Txn Knowledge",
		"#### 知识内容", "事务知识。", "### Txn Opinion", "事务观点。",
	} {
		if !bytes.Contains(exported, []byte(visible)) {
			t.Fatalf("plain export 丢失可见内容 %q：\n%s", visible, exported)
		}
	}
	if after := snapshot(t, dir); after != beforeVault {
		t.Fatalf("export 改动了 vault：\n前=%s\n后=%s", beforeVault, after)
	}
	if got := gitOut(t, dir, "log", "--oneline"); got != beforeLog {
		t.Fatal("export 产生了 Git commit")
	}
	if _, err := os.Stat(filepath.Join(out, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("plain export 只导出知识数据，不应复制运行规程 SKILL.md")
	}
}

func TestExportPlainRejectsUnsafeOutputs(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	nonempty := filepath.Join(t.TempDir(), "nonempty")
	if err := os.MkdirAll(nonempty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonempty, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		filepath.Join(dir, "plain"), nonempty, link,
	} {
		code, _, _ := runStorageV3CLI(t, newTestRoot(t, dir),
			"export", "--vault", dir, "--json", "--plain", "--output", output)
		if code != ExitUsage {
			t.Fatalf("不安全输出 %s 应退 1，实得 %d", output, code)
		}
	}
}
