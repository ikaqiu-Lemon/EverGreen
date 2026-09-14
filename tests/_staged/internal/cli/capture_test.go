package cli

// T-…-009 的验收用例：eg capture 的最小收录合同（§7.5）。
// 用例名以 Capture 开头，可用 `go test ./internal/cli/... -run Capture` 单独跑。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const captureBody = "这是一篇足够长的正文，用于收录测试。它由 Agent 侧清洗后传入，eg 不抓取网页。\n"

// captureAt 是全部收录用例使用的固定时刻：ID 与 saved_at 因此完全确定性。
func captureAt(t *testing.T) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, "2026-09-01T10:00:00+08:00")
	if err != nil {
		t.Fatalf("解析固定时刻失败：%v", err)
	}
	return ts
}

// captureVault 建一个已配置 default_domain 的干净 vault。
func captureVault(t *testing.T) string {
	t.Helper()
	setGitIdentity(t)
	dir := t.TempDir()
	code, _, errOut := runCLI(t, newTestRoot(t, dir), "init", "--vault", dir, "--domain", "ai-infra")
	if code != ExitOK {
		t.Fatalf("eg init 退出码 = %d：%s", code, errOut)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

// runCapture 执行一次收录，返回退出码与 --json 信封。
func runCaptureCLI(t *testing.T, dir, body string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.In = strings.NewReader(body)
	r.Now = func() time.Time { return captureAt(t) }
	code, out, errOut := runCLI(t, r, append([]string{
		"capture", "--vault", dir, "--json", "--body-stdin"}, args...)...)
	var env Envelope
	if out != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

func captureOnce(t *testing.T, dir string, args ...string) Envelope {
	t.Helper()
	code, env, errOut := runCaptureCLI(t, dir, captureBody, args...)
	if code != ExitOK {
		t.Fatalf("eg capture 退出码 = %d，期望 0：%s", code, errOut)
	}
	return env
}

func sourceFiles(t *testing.T, dir string) []string {
	t.Helper()
	got, err := filepath.Glob(filepath.Join(dir, store.DirSources, "*.md"))
	if err != nil {
		t.Fatalf("glob sources/: %v", err)
	}
	sort.Strings(got)
	return got
}

func inboxEntries(t *testing.T, dir string) int {
	t.Helper()
	raw := string(mustRead(t, filepath.Join(dir, store.UnprocessedFile)))
	n := 0
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "- source_id:") {
			n++
		}
	}
	return n
}

func logCount(t *testing.T, dir string) int {
	t.Helper()
	return len(strings.Split(strings.TrimSpace(gitOut(t, dir, "log", "--oneline")), "\n"))
}

// —— ① 同一 URL 二次收录：复用、不产生第二份、干净工作区下无新 commit ——

func TestCaptureDedupBySameURLConverges(t *testing.T) {
	dir := captureVault(t)
	first := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "注意力机制入门", "--reason", "打底材料")
	before := mustRead(t, filepath.Join(dir, first.Data["path"].(string)))
	logBefore := logCount(t, dir)

	second := captureOnce(t, dir, "--url", "https://example.com/a?utm_source=x#frag",
		"--title", "注意力机制入门", "--reason", "打底材料")

	if second.Data["deduped"] != true {
		t.Fatalf("二次收录必须 deduped=true：%v", second.Data)
	}
	if second.Data["source_id"] != first.Data["source_id"] {
		t.Fatalf("source_id 必须与首次相同：%v ≠ %v", second.Data["source_id"], first.Data["source_id"])
	}
	if got := sourceFiles(t, dir); len(got) != 1 {
		t.Fatalf("sources/ 只应有一份原文，实得 %v", got)
	}
	if n := inboxEntries(t, dir); n != 1 {
		t.Fatalf("unprocessed.md 只应有一条条目，实得 %d", n)
	}
	if after := mustRead(t, filepath.Join(dir, first.Data["path"].(string))); string(after) != string(before) {
		t.Fatalf("同理由二次收录必须字节不变：\n%s\n---\n%s", before, after)
	}
	if got := logCount(t, dir); got != logBefore {
		t.Fatalf("干净工作区下二次收录不得产生新 commit：%d → %d", logBefore, got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("二次收录后工作区应仍干净，得到 %q", got)
	}
}

// —— ② 仅标题相同（URL 不同）同样判重命中 ——

func TestCaptureDedupByTitleOnly(t *testing.T) {
	dir := captureVault(t)
	first := captureOnce(t, dir, "--url", "https://a.example.com/x", "--title", "同一篇文章", "--reason", "r1")
	second := captureOnce(t, dir, "--url", "https://b.example.com/y", "--title", "同一篇文章", "--reason", "r1")

	if second.Data["deduped"] != true || second.Data["source_id"] != first.Data["source_id"] {
		t.Fatalf("标题相同必须判重命中：%v", second.Data)
	}
	if got := sourceFiles(t, dir); len(got) != 1 {
		t.Fatalf("sources/ 只应有一份原文，实得 %v", got)
	}
	if n := inboxEntries(t, dir); n != 1 {
		t.Fatalf("unprocessed.md 只应有一条条目，实得 %d", n)
	}
}

// —— ③ 判重命中且理由不同：正文字节不变、收录理由列表新增一条 ——

func TestCaptureDedupAppendsReasonWithoutTouchingBody(t *testing.T) {
	dir := captureVault(t)
	first := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "注意力机制入门", "--reason", "打底材料")
	rel := first.Data["path"].(string)
	before := string(mustRead(t, filepath.Join(dir, rel)))

	captureOnce(t, dir, "--url", "https://example.com/a", "--title", "注意力机制入门", "--reason", "第二个理由")
	after := string(mustRead(t, filepath.Join(dir, rel)))

	if !strings.Contains(after, "- '打底材料'") || !strings.Contains(after, "- '第二个理由'") {
		t.Fatalf("收录理由列表应新增一条：\n%s", after)
	}
	if !strings.Contains(after, captureBody) {
		t.Fatal("原文正文必须逐字保留、不被覆盖")
	}
	body := before[strings.Index(before, "\n---\n")+len("\n---\n"):]
	if !strings.HasSuffix(after, body) {
		t.Fatalf("正文字节必须不变：\n%q", after)
	}
	// 理由列表变了 → 有改动 → 本次收录产生一条 capture commit（不是空 commit）。
	if !strings.Contains(gitOut(t, dir, "log", "--oneline"), "capture(") {
		t.Fatal("理由追加后应有 capture commit")
	}
}

// —— ④ 正文为空 / 过短 → 退 2，零写入 ——

func TestCaptureEmptyBodyExitsTwoWithoutWriting(t *testing.T) {
	dir := captureVault(t)
	beforeInbox := mustRead(t, filepath.Join(dir, store.UnprocessedFile))
	logBefore := logCount(t, dir)

	code, _, _ := runCaptureCLI(t, dir, "   \n", "--url", "https://example.com/a", "--title", "空正文", "--reason", "r")
	if code != ExitValidation {
		t.Fatalf("空正文退出码 = %d，期望 2", code)
	}
	if got := sourceFiles(t, dir); len(got) != 0 {
		t.Fatalf("零写入：sources/ 应为空，实得 %v", got)
	}
	if after := mustRead(t, filepath.Join(dir, store.UnprocessedFile)); string(after) != string(beforeInbox) {
		t.Fatal("零写入：unprocessed.md 字节必须不变")
	}
	if got := logCount(t, dir); got != logBefore {
		t.Fatalf("零写入：不得产生 commit（%d → %d）", logBefore, got)
	}
}

// —— ⑤ --url 与 --title 同时缺失 → 退 2 零写入 ——

func TestCaptureWithoutURLAndTitleExitsTwo(t *testing.T) {
	dir := captureVault(t)
	code, _, _ := runCaptureCLI(t, dir, captureBody, "--reason", "r")
	if code != ExitValidation {
		t.Fatalf("--url 与 --title 同时缺失退出码 = %d，期望 2", code)
	}
	if got := sourceFiles(t, dir); len(got) != 0 {
		t.Fatalf("零写入：sources/ 应为空，实得 %v", got)
	}
}

// —— ⑥ --json 的 data 键恰七项（键名单测锁定，只增不改） ——
//
// M6（T-…-072 批次 C1）在此之上增加**恰一个条件键** `txn_id`：审计边界是
// 「AllocateTxnID 成功」，因此有权威写入的收录必有它，零 accepted write-set 的收录
// 必无它（后者由 TestCaptureZeroWriteSetOpensNoTxnAndNoGit 单独钉住）。
// 用例名保留 T-…-009 的原名以便追溯；键面判据仍是**精确等号**，不放宽成「包含」。
func TestCaptureJSONDataHasExactlySevenKeys(t *testing.T) {
	dir := captureVault(t)
	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "键名锁定", "--reason", "r")
	// M1 七键 + M6 成功事务写的条件键 txn_id。
	want := []string{"commit", "deduped", "has_note", "note_id", "path", "source_id", "txn_id", "warnings"}
	var got []string
	for k := range env.Data {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("data 键 = %v，期望恰为 %v", got, want)
	}
}

// —— ⑦ 领域缺省：target_domain = default_domain + 一条领域缺省 warning ——

func TestCaptureDefaultDomainFallback(t *testing.T) {
	dir := captureVault(t)
	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "领域缺省", "--reason", "r")
	inbox := string(mustRead(t, filepath.Join(dir, store.UnprocessedFile)))
	if !strings.Contains(inbox, "target_domain: 'ai-infra'") {
		t.Fatalf("条目 target_domain 应等于 default_domain：\n%s", inbox)
	}
	found := false
	for _, w := range env.Warnings {
		if strings.Contains(w.Message, "default_domain_fallback") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings[] 必须含领域缺省提示：%+v", env.Warnings)
	}
}

// —— ⑧ 已有材料笔记：has_note + note_id，笔记字节不变、干净工作区下无 commit ——

func TestCaptureSkipsWhenNoteExists(t *testing.T) {
	dir := captureVault(t)
	first := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "已有笔记", "--reason", "r")
	sourceID := first.Data["source_id"].(string)

	noteRel := store.NoteRel("ai-infra", "n-20260901-note")
	notePath := filepath.Join(dir, noteRel)
	noteBytes := []byte("---\nid: 'n-20260901-note'\nsource: '" + sourceID + "'\n" +
		"created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n---\n\n" +
		"## 材料提炼\n\n已提炼。\n\n## Agent 分析\n\n## 用户补充\n\n## 存疑与待验证\n\n## 产出知识卡\n")
	if err := os.WriteFile(notePath, noteBytes, 0o644); err != nil {
		t.Fatalf("写笔记 fixture 失败：%v", err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "process(ai-infra): 笔记 fixture")
	logBefore := logCount(t, dir)

	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "已有笔记", "--reason", "r")
	if env.Data["has_note"] != true || env.Data["note_id"] != "n-20260901-note" {
		t.Fatalf("已有笔记时应输出 has_note=true 与正确 note_id：%v", env.Data)
	}
	if got := mustRead(t, notePath); string(got) != string(noteBytes) {
		t.Fatal("已有笔记的字节必须不变")
	}
	if got := logCount(t, dir); got != logBefore {
		t.Fatalf("干净工作区下不得产生 commit：%d → %d", logBefore, got)
	}
}

// —— ⑨ 收件区落位反证（冻结合同 F1）：队列文件在 vault 根，sources/ 下不得有第二个 ——

func TestCaptureInboxLivesAtVaultRoot(t *testing.T) {
	dir := captureVault(t)
	captureOnce(t, dir, "--url", "https://example.com/a", "--title", "落位反证", "--reason", "r")

	if _, err := os.Stat(filepath.Join(dir, store.UnprocessedFile)); err != nil {
		t.Fatalf("vault 根必须有 %s：%v", store.UnprocessedFile, err)
	}
	stray, err := filepath.Glob(filepath.Join(dir, store.DirSources, store.UnprocessedFile))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(stray) != 0 {
		t.Fatalf("sources/ 下不得出现第二个队列文件：%v", stray)
	}
	if n := inboxEntries(t, dir); n != 1 {
		t.Fatalf("条目数应为 1，实得 %d", n)
	}
}

// —— ⑩ 首次收录：一条 capture commit，--name-only 恰两个路径 ——

func TestCaptureFirstCommitTouchesExactlyTwoPaths(t *testing.T) {
	dir := captureVault(t)
	env := captureOnce(t, dir, "--url", "https://example.com/a", "--title", "提交范围", "--reason", "r")
	head := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
	if !strings.HasPrefix(head, "capture(") {
		t.Fatalf("首次收录应产生 capture(...) commit，实得 %q", head)
	}
	names := strings.Fields(gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD"))
	sort.Strings(names)
	want := []string{env.Data["path"].(string), store.UnprocessedFile}
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("commit 文件清单 = %v，期望 %v", names, want)
	}
}

// —— ⑪ 全程无网络：以 httptest 反证 eg 不对 --url 发起任何请求 ——

func TestCaptureMakesNoNetworkRequest(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
	}))
	defer srv.Close()

	dir := captureVault(t)
	captureOnce(t, dir, "--url", srv.URL+"/article", "--title", "无网络反证", "--reason", "r")
	if n := atomic.LoadInt64(&hits); n != 0 {
		t.Fatalf("eg 不得发起任何网络请求，实测 %d 次", n)
	}
}

// —— ⑫ --captured-at 决定 ID 与 saved_at（确定性，无本机时间依赖） ——

func TestCaptureUsesCapturedAtForID(t *testing.T) {
	dir := captureVault(t)
	env := captureOnce(t, dir, "--url", "https://example.com/demo", "--title", "demo",
		"--reason", "r", "--captured-at", "2026-04-12T00:00:00+08:00")
	if env.Data["source_id"] != "s-20260412-demo" {
		t.Fatalf("source_id = %v，期望 s-20260412-demo", env.Data["source_id"])
	}
}
