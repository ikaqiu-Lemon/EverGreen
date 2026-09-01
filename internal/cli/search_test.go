package cli

// T-…-021 的 CLI 层机器判据：`eg search` 的双渲染同源、[失效] 标记、Q 类 warning 透出、
// 退出码边界与**只读零副作用**。
//
// 判据来源：M2 查询合同 §1（参数 / 键表 / 排序 / 失效卡）、§5（Q 系列）、§6（零副作用）；
// CLI 合同 §3（信封五键）、§4（退出码）。测试名统一含 `Search`，与 verify.test 的
// `-run Search` 对齐。

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// searchVault 造一个可检索的 vault：三张卡（标题 / 正文 / tags 各命中一处）+ 一张失效卡。
func searchVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\n  - ops\ndefault_domain: ai-infra\n")
	seedCard(t, dir, "ai-infra", "k-20260901-t", "注意力机制的计算代价", "active",
		"2026-09-01", "2026-09-01T10:00:00+08:00", nil, "正文占位。")
	seedCard(t, dir, "ai-infra", "k-20260902-b", "无关标题", "active",
		"2026-09-02", "2026-09-02T10:00:00+08:00", nil, "正文里写了注意力三个字。")
	seedCard(t, dir, "ops", "k-20260903-g", "别的题目", "active",
		"2026-09-03", "2026-09-03T10:00:00+08:00", []string{"注意力"}, "正文占位。")
	seedCard(t, dir, "ai-infra", "k-20260904-d", "失效的注意力卡", "deprecated",
		"2026-09-04", "2026-09-04T10:00:00+08:00", nil, "正文占位。")
	return dir
}

// writeFileMk 建目录后写文件（cli_test.go 的 writeFile 只写不建目录）。
func writeFileMk(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, content)
}

// seedCard 写一张最小合法知识卡（测试脚手架，不是产品写路径）。
func seedCard(t *testing.T, root, domain, id, title, status, created, updated string,
	tags []string, body string) {
	t.Helper()
	fm := "---\nid: " + id + "\nstatus: " + status + "\ncreated_at: '" + created +
		"'\nupdated_at: '" + updated + "'\ntitle: " + title + "\nsources: []\n"
	if len(tags) > 0 {
		fm += "tags:\n"
		for _, tag := range tags {
			fm += "  - " + tag + "\n"
		}
	}
	fm += "---\n\n## 知识内容\n\n" + body + "\n"
	writeFileMk(t, filepath.Join(root, "domains", domain, "knowledge", id+".md"), fm)
}

// runSearchJSON 跑一次 `eg search --json` 并解出信封。
func runSearchJSON(t *testing.T, dir string, args ...string) (int, map[string]interface{}, string) {
	t.Helper()
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, append([]string{"--vault", dir, "--json", "search"}, args...)...)
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q / %q）", err, out, errOut)
	}
	assertEnvelopeKeys(t, env)
	return code, env, errOut
}

func searchHitIDs(t *testing.T, env map[string]interface{}) []string {
	t.Helper()
	data, _ := env["data"].(map[string]interface{})
	hits, ok := data["hits"].([]interface{})
	if !ok {
		t.Fatalf("data.hits 不是数组：%v", data["hits"])
	}
	out := []string{}
	for _, h := range hits {
		m, _ := h.(map[string]interface{})
		out = append(out, fmt.Sprint(m["id"]))
	}
	return out
}

// TestSearchDataKeysAndOrderMatchContract —— data 键序 = 合同 §1.2 表次序，
// hits[] 元素键序 = §1.2 九项 + score，逐字反证（不是只比键集合）。
func TestSearchDataKeysAndOrderMatchContract(t *testing.T) {
	dir := searchVault(t)
	r := newTestRoot(t, dir)
	code, out, _ := runCLI(t, r, "--vault", dir, "--json", "search", "注意力")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（%s）", code, out)
	}
	dataRaw := out[strings.Index(out, `"data":`)+len(`"data":`):]
	wantData := []string{`"hits":`, `"total":`, `"scanned_files":`, `"skipped_files":`}
	at := 0
	for _, k := range wantData {
		i := strings.Index(dataRaw[at:], k)
		if i < 0 {
			t.Fatalf("data 缺键 %s 或键序与合同 §1.2 不一致：%s", k, out)
		}
		at += i
	}
	wantHit := []string{`"id":`, `"title":`, `"domain":`, `"tags":`, `"status":`, `"deprecated":`,
		`"updated_at":`, `"created_at":`, `"path":`, `"matched_fields":`, `"score":`}
	at = strings.Index(out, `"hits":[`)
	for _, k := range wantHit {
		i := strings.Index(out[at:], k)
		if i < 0 {
			t.Fatalf("hits[] 元素缺键 %s 或键序与合同 §1.2 不一致：%s", k, out)
		}
		at += i
	}
}

// TestSearchKeywordMatchesTitleBodyTags —— 标题 / 正文 / tags 三处各自命中，
// matched_fields 如实回填（合同 §1.2 / §1.3）。
func TestSearchKeywordMatchesTitleBodyTags(t *testing.T) {
	dir := searchVault(t)
	code, env, _ := runSearchJSON(t, dir, "注意力")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0", code)
	}
	data, _ := env["data"].(map[string]interface{})
	hits, _ := data["hits"].([]interface{})
	want := map[string][]string{
		"k-20260901-t": {"title"},
		"k-20260902-b": {"body"},
		"k-20260903-g": {"tags"},
		"k-20260904-d": {"title"},
	}
	if len(hits) != len(want) {
		t.Fatalf("命中数 = %d，期望 %d：%v", len(hits), len(want), searchHitIDs(t, env))
	}
	for _, h := range hits {
		m, _ := h.(map[string]interface{})
		id := fmt.Sprint(m["id"])
		var got []string
		for _, f := range m["matched_fields"].([]interface{}) {
			got = append(got, fmt.Sprint(f))
		}
		if strings.Join(got, ",") != strings.Join(want[id], ",") {
			t.Errorf("%s 的 matched_fields = %v，期望 %v", id, got, want[id])
		}
	}
}

// TestSearchFilters —— --domain / --tag / --since / --until 逐项生效（合同 §1.1）。
func TestSearchFilters(t *testing.T) {
	dir := searchVault(t)
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"注意力", "--domain", "ops"}, []string{"k-20260903-g"}},
		{[]string{"注意力", "--tag", "注意力"}, []string{"k-20260903-g"}},
		{[]string{"注意力", "--tag", "注意力", "--tag", "缺这个"}, []string{}},
		{[]string{"注意力", "--since", "2026-09-04"}, []string{"k-20260904-d"}},
		{[]string{"注意力", "--until", "2026-09-01"}, []string{"k-20260901-t"}},
		{[]string{"注意力", "--since", "2026-09-02", "--until", "2026-09-03"},
			[]string{"k-20260903-g", "k-20260902-b"}},
	}
	for _, c := range cases {
		code, env, _ := runSearchJSON(t, dir, c.args...)
		if code != ExitOK {
			t.Fatalf("eg search %v 退出码 = %d，期望 0", c.args, code)
		}
		if got := searchHitIDs(t, env); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("eg search %v 命中 = %v，期望 %v", c.args, got, c.want)
		}
	}
}

// TestSearchStableOrder —— 同一 vault 连续两次执行的 hits[].id 序列逐字相等（合同 §1.4），
// 且同分同时间戳时按 id 升序（第 ④ 级保证全序）。
func TestSearchStableOrder(t *testing.T) {
	dir := searchVault(t)
	_, first, _ := runSearchJSON(t, dir, "注意力")
	_, second, _ := runSearchJSON(t, dir, "注意力")
	a, b := searchHitIDs(t, first), searchHitIDs(t, second)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("两次执行的 ID 序列不同：%v vs %v", a, b)
	}
	tie := t.TempDir()
	writeFile(t, filepath.Join(tie, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n")
	for _, id := range []string{"k-20260901-c", "k-20260901-a", "k-20260901-b"} {
		seedCard(t, tie, "ai-infra", id, "同题", "active",
			"2026-09-01", "2026-09-01T10:00:00+08:00", nil, "正文占位。")
	}
	_, env, _ := runSearchJSON(t, tie, "同题")
	if got := searchHitIDs(t, env); strings.Join(got, ",") !=
		"k-20260901-a,k-20260901-b,k-20260901-c" {
		t.Fatalf("同分同时间戳未按 id 升序：%v", got)
	}
}

// TestSearchDeprecatedVisible —— 失效卡同等可见：JSON 里 deprecated == true、
// 文本模式该行以 [失效] 开头；且实现里没有任何隐藏失效卡的开关（合同 §1.5）。
func TestSearchDeprecatedVisible(t *testing.T) {
	dir := searchVault(t)
	_, env, _ := runSearchJSON(t, dir, "失效")
	data, _ := env["data"].(map[string]interface{})
	hits, _ := data["hits"].([]interface{})
	if len(hits) != 1 {
		t.Fatalf("命中数 = %d，期望 1（失效卡照常进结果集）", len(hits))
	}
	m, _ := hits[0].(map[string]interface{})
	if m["deprecated"] != true || m["status"] != "deprecated" {
		t.Fatalf("失效卡的 deprecated / status 不对：%v", m)
	}
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "search", "失效")
	var marked bool
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, MarkerDeprecated) {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("文本模式失效卡行未以 %s 开头：\n%s", MarkerDeprecated, out)
	}
	// S2 / S3 标记不得提前输出。
	for _, forbidden := range []string{"[已删除]", "[未过目]", "[材料支持不足]"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("M2 不得输出标记 %s：\n%s", forbidden, out)
		}
	}
	raw, err := os.ReadFile("search.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"hide-deprecated", "active-only", "include-deprecated"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("M2 不提供隐藏失效卡的开关，search.go 命中 %q", forbidden)
		}
	}
}

// TestSearchDualRenderingSameFacts —— 人类可读与 --json 同源同事实：
// 每条命中的 id / title / domain / updated_at 都出现在文本渲染里，Q 诊断两边都有。
func TestSearchDualRenderingSameFacts(t *testing.T) {
	dir := searchVault(t)
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "broken.md"),
		"---\n- 1\n---\n\n# 坏卡\n")
	_, env, _ := runSearchJSON(t, dir, "注意力")
	r := newTestRoot(t, dir)
	code, out, _ := runCLI(t, r, "--vault", dir, "search", "注意力")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（Q 类 warning 不改退出码）", code)
	}
	data, _ := env["data"].(map[string]interface{})
	for _, h := range data["hits"].([]interface{}) {
		m, _ := h.(map[string]interface{})
		for _, k := range []string{"id", "title", "domain", "updated_at"} {
			if !strings.Contains(out, fmt.Sprint(m[k])) {
				t.Errorf("文本渲染缺 hits[].%s = %v：\n%s", k, m[k], out)
			}
		}
	}
	warnings, _ := env["warnings"].([]interface{})
	if len(warnings) == 0 {
		t.Fatal("坏文件必须产生 warnings[]")
	}
	for _, w := range warnings {
		m, _ := w.(map[string]interface{})
		if !strings.Contains(out, fmt.Sprint(m["message"])) {
			t.Errorf("文本渲染缺 warning：%v\n%s", m["message"], out)
		}
	}
}

// TestSearchQ1WarningKeepsExitZero —— 不可解析文件：warnings[] 含 Q1（路径为坏文件）
// 与恰一条 Q3，skipped_files == 1，退出码仍 0（合同 §5.1 / §6）。
func TestSearchQ1WarningKeepsExitZero(t *testing.T) {
	dir := searchVault(t)
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "broken.md"),
		"---\n- 1\n---\n\n# 坏卡\n")
	code, env, _ := runSearchJSON(t, dir, "注意力")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0", code)
	}
	data, _ := env["data"].(map[string]interface{})
	if data["skipped_files"] != float64(1) {
		t.Fatalf("skipped_files = %v，期望 1", data["skipped_files"])
	}
	var q1, q3 int
	for _, w := range env["warnings"].([]interface{}) {
		m, _ := w.(map[string]interface{})
		switch m["code"] {
		case "Q1":
			q1++
			if m["path"] != "domains/ai-infra/knowledge/broken.md" {
				t.Errorf("Q1.path = %v，期望坏文件相对路径", m["path"])
			}
			if m["level"] != LevelWarning || m["op_index"] != float64(NonOpDiagnostic) {
				t.Errorf("Q 系列必须是 warning + op_index=-1：%v", m)
			}
		case "Q3":
			q3++
		}
	}
	if q1 != 1 || q3 != 1 {
		t.Fatalf("Q1 = %d、Q3 = %d，期望各恰一条", q1, q3)
	}
}

// TestSearchExitCodes —— 退出码边界：零命中退 0；空检索词 / 多余位置参数 / 非法日期 /
// 区间倒置 / 领域未登记 / 未配置 default_domain 一律退 1；**不出现** 2 / 3 / 4。
func TestSearchExitCodes(t *testing.T) {
	dir := searchVault(t)
	code, env, _ := runSearchJSON(t, dir, "根本没有这个词")
	data, _ := env["data"].(map[string]interface{})
	if code != ExitOK || data["total"] != float64(0) {
		t.Fatalf("零命中应退 0 且 total=0，实际 code=%d total=%v", code, data["total"])
	}
	bad := [][]string{
		{"search", ""},
		{"search", "   "},
		{"search"},
		{"search", "a", "b"},
		{"search", "注意力", "--since", "2026-9-1"},
		{"search", "注意力", "--until", "2026/09/01"},
		{"search", "注意力", "--since", "2026-09-05", "--until", "2026-09-01"},
		{"search", "注意力", "--domain", "not-registered"},
	}
	for _, args := range bad {
		r := newTestRoot(t, dir)
		code, _, errOut := runCLI(t, r, append([]string{"--vault", dir}, args...)...)
		if code != ExitUsage {
			t.Errorf("eg %v 退出码 = %d，期望 1", args, code)
		}
		if errOut == "" {
			t.Errorf("eg %v 未给 stderr 提示", args)
		}
	}
	// 未配置 default_domain（框架守卫，EG-DOM-03：CLI 绝不自选领域）。
	noCfg := t.TempDir()
	writeFile(t, filepath.Join(noCfg, ConfigFileName), "version: 1\ndomains:\n  - ai-infra\n")
	r := newTestRoot(t, noCfg)
	code, _, errOut := runCLI(t, r, "--vault", noCfg, "search", "注意力")
	if code != ExitUsage || !strings.Contains(errOut, "default_domain") {
		t.Fatalf("未配置 default_domain 应退 1 并提示配置，实际 code=%d err=%q", code, errOut)
	}
}

// TestSearchZeroSideEffect —— 合同 §6 判据的实证：git status --porcelain 前后逐字相等、
// git log 条数不变、vault 内每个文件的字节与 mtime 不变；退出码只可能 0 / 1。
func TestSearchZeroSideEffect(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("无 git，跳过 git 层零副作用断言")
	}
	dir := searchVault(t)
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "broken.md"),
		"---\n- 1\n---\n\n# 坏卡\n")
	git := func(args ...string) string {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v 失败：%v（%s）", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	git("-c", "user.email=eg@example.com", "-c", "user.name=eg", "add", ".")
	git("-c", "user.email=eg@example.com", "-c", "user.name=eg", "commit", "-q", "-m", "seed")

	beforeStatus, beforeLog, beforeTree := git("status", "--porcelain"),
		git("rev-list", "--count", "HEAD"), vaultSnapshot(t, dir)
	for _, args := range [][]string{
		{"search", "注意力"},
		{"search", "注意力", "--json"},
		{"search", "根本没有这个词"},
		{"search", ""},
		{"search", "注意力", "--domain", "not-registered"},
	} {
		r := newTestRoot(t, dir)
		code, _, _ := runCLI(t, r, append([]string{"--vault", dir}, args...)...)
		if code != ExitOK && code != ExitUsage {
			t.Fatalf("eg %v 退出码 = %d：只读命令只可能 0 / 1", args, code)
		}
	}
	if s := git("status", "--porcelain"); s != beforeStatus {
		t.Fatalf("git status 前后不同：%q → %q", beforeStatus, s)
	}
	if s := git("rev-list", "--count", "HEAD"); s != beforeLog {
		t.Fatalf("git log 条数变了：%q → %q", beforeLog, s)
	}
	if s := vaultSnapshot(t, dir); s != beforeTree {
		t.Fatalf("vault 内文件字节 / mtime 变了：\n%s\n%s", beforeTree, s)
	}
}

// vaultSnapshot 快照 vault 内 .md 文件的路径 + 字节数 + mtime + 内容摘要
// （合同 §6 的 find 快照的 Go 形态：同时覆盖大小、修改时间与内容）。
func vaultSnapshot(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		lines = append(lines, fmt.Sprintf("%s %d %d %x", filepath.ToSlash(rel),
			info.Size(), info.ModTime().UnixNano(), sha256.Sum256(raw)))
		return nil
	})
	if err != nil {
		t.Fatalf("快照失败：%v", err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
