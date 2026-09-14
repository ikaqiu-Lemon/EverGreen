package cli

// T-…-023 的 CLI 层机器判据：`eg rel` 读路径的 data 键序、正向 / 反向、`--to` 过滤、
// Q1 / Q2 诊断透出、写子命令仍未实现、退出码边界与**只读零副作用**。
//
// 判据来源：M2 查询合同 §3（用法 / data 键表 / 取数口径 / 排序 / 退出码）、§5（Q 系列）、
// §6（零副作用）；CLI 合同 §1.8（rel 五种调用形态）、§3（信封五键）、§4（退出码）。
// 测试名统一以 `RelQuery` 开头，与 verify.test 的 `-run RelQuery` 对齐。

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// relVault 造关系语料（跨领域）：A→supports B、A→opposing C、C→limits B、
// C→derives 悬空目标；B 无正向关系，是反向查询的观察点。
func relVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\n  - ops\ndefault_domain: ai-infra\n")
	seedRelCard(t, dir, "ai-infra", "k-20260901-a", "A 卡",
		"  - type: supports\n    target: k-20260902-b\n    reason: 支持 B\n"+
			"  - type: opposing\n    target: k-20260903-c\n    reason: 与 C 冲突\n")
	seedRelCard(t, dir, "ai-infra", "k-20260902-b", "B 卡", "")
	seedRelCard(t, dir, "ops", "k-20260903-c", "C 卡",
		"  - type: limits\n    target: k-20260902-b\n    reason: 限定 B 的适用范围\n"+
			"  - type: derives\n    target: k-20260909-missing\n    reason: 指向不存在的卡\n")
	return dir
}

// seedRelCard 写一张带 relations[] 的最小合法知识卡（测试脚手架，不是产品写路径）。
func seedRelCard(t *testing.T, root, domain, id, title, relations string) {
	t.Helper()
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2026-09-01'\n" +
		"updated_at: '2026-09-12T10:00:00+08:00'\ntitle: " + title + "\nsources: []\n"
	if relations != "" {
		fm += "relations:\n" + relations
	}
	fm += "---\n\n## 知识内容\n\n正文占位。\n"
	writeFileMk(t, filepath.Join(root, "domains", domain, "knowledge", id+".md"), fm)
}

// runRelJSON 跑一次 `eg rel <k-id> --json` 并解出信封。
func runRelJSON(t *testing.T, dir string, args ...string) (int, map[string]interface{}, string) {
	t.Helper()
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, append([]string{"--vault", dir, "--json", "rel"}, args...)...)
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q / %q）", err, out, errOut)
	}
	assertEnvelopeKeys(t, env)
	return code, env, errOut
}

func relEdgeSigs(t *testing.T, data map[string]interface{}, key, peer string) []string {
	t.Helper()
	edges, ok := data[key].([]interface{})
	if !ok {
		t.Fatalf("data.%s 不是数组：%v", key, data[key])
	}
	out := []string{}
	for _, e := range edges {
		m, _ := e.(map[string]interface{})
		if len(m) != 5 {
			t.Fatalf("%s 元素键数 = %d，期望恰 5（合同 §3.1）：%v", key, len(m), m)
		}
		out = append(out, fmt.Sprintf("%v(%v)", m["type"], m[peer]))
	}
	return out
}

// TestRelQueryDataKeyOrderMatchesContract —— data 键序 = 合同 §3.1；条目键恰五项；
// 输出含 relations_out / relations_in 且退 0（M2 完成判据 1）。
func TestRelQueryDataKeyOrderMatchesContract(t *testing.T) {
	dir := relVault(t)
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "--vault", dir, "--json", "rel", "k-20260901-a")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（%s / %s）", code, out, errOut)
	}
	assertKeyOrder(t, out, strings.Index(out, `"data":`), []string{
		`"id":`, `"relations_out":`, `"relations_in":`, `"scanned_files":`, `"skipped_files":`})
	assertKeyOrder(t, out, strings.Index(out, `"relations_out":`),
		[]string{`"from":`, `"type":`, `"target":`, `"reason":`, `"path":`})
}

// TestRelQueryForward —— 正向恰为本卡 relations[]，opposing 排在 supports 前，
// 每条都带 type / target / reason（合同 §3.1 元素五键的子集断言）。
func TestRelQueryForward(t *testing.T) {
	dir := relVault(t)
	_, env, _ := runRelJSON(t, dir, "k-20260901-a")
	data, _ := env["data"].(map[string]interface{})
	got := relEdgeSigs(t, data, "relations_out", "target")
	want := []string{"opposing(k-20260903-c)", "supports(k-20260902-b)"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("relations_out = %v，期望 %v（opposing 在前）", got, want)
	}
	edges, _ := data["relations_out"].([]interface{})
	for _, e := range edges {
		m, _ := e.(map[string]interface{})
		for _, k := range []string{"type", "target", "reason"} {
			if fmt.Sprint(m[k]) == "" {
				t.Fatalf("正向条目缺 %s：%v", k, m)
			}
		}
		if m["from"] != "k-20260901-a" {
			t.Fatalf("from = %v，应恒为本卡 ID", m["from"])
		}
	}
}

// TestRelQueryReverseScan —— 反向走全库扫描（跨领域可见）：`rel B` 的 relations_in
// 恰两条且顺序为 limits(C) → supports(A)；文本模式同样出现两条反向条目。
func TestRelQueryReverseScan(t *testing.T) {
	dir := relVault(t)
	_, env, _ := runRelJSON(t, dir, "k-20260902-b")
	data, _ := env["data"].(map[string]interface{})
	got := relEdgeSigs(t, data, "relations_in", "from")
	want := []string{"limits(k-20260903-c)", "supports(k-20260901-a)"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("relations_in = %v，期望 %v（合同 §3.3）", got, want)
	}
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "rel", "k-20260902-b")
	for _, want := range []string{
		"反向关系：k-20260903-c --limits--> k-20260902-b",
		"反向关系：k-20260901-a --supports--> k-20260902-b",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("文本模式缺反向条目 %q：%s", want, out)
		}
	}
	if strings.Index(out, "--limits-->") > strings.Index(out, "--supports-->") {
		t.Fatalf("文本模式的反向次序也必须是 limits → supports：%s", out)
	}
}

// TestRelQueryToFilter —— `--to` 正反向同时过滤；对端不存在时结果为空并多一条 Q2，仍退 0。
func TestRelQueryToFilter(t *testing.T) {
	dir := relVault(t)
	code, env, _ := runRelJSON(t, dir, "k-20260902-b", "--to", "k-20260901-a")
	if code != ExitOK {
		t.Fatalf("--to 过滤退出码 = %d，期望 0", code)
	}
	data, _ := env["data"].(map[string]interface{})
	if got := relEdgeSigs(t, data, "relations_in", "from"); len(got) != 1 ||
		got[0] != "supports(k-20260901-a)" {
		t.Fatalf("--to 过滤后的 relations_in = %v", got)
	}
	if edges, _ := data["relations_out"].([]interface{}); len(edges) != 0 {
		t.Fatalf("B 无正向关系，--to 过滤后仍应为空：%v", edges)
	}
	code, env, _ = runRelJSON(t, dir, "k-20260901-a", "--to", "k-20260909-missing")
	if code != ExitOK {
		t.Fatalf("--to 指向不存在的卡仍应退 0，实际 %d", code)
	}
	data, _ = env["data"].(map[string]interface{})
	outEdges, _ := data["relations_out"].([]interface{})
	inEdges, _ := data["relations_in"].([]interface{})
	if len(outEdges) != 0 || len(inEdges) != 0 {
		t.Fatalf("--to 指向不存在的卡时结果应为空：%v / %v", outEdges, inEdges)
	}
	warns, _ := env["warnings"].([]interface{})
	hit := 0
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		if m["code"] == "Q2" && strings.Contains(fmt.Sprint(m["message"]), "--to") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("--to 不存在应恰一条 Q2，实际 warnings=%v", warns)
	}
}

// TestRelQueryDanglingQ2 —— 悬空引用：warnings[] 含点名目标 ID 的 Q2，条目仍在
// relations_out[] 里且文本模式标注「目标不存在」，退出码仍 0（不静默丢弃、不升级为 error）。
func TestRelQueryDanglingQ2(t *testing.T) {
	dir := relVault(t)
	code, env, _ := runRelJSON(t, dir, "k-20260903-c")
	if code != ExitOK {
		t.Fatalf("悬空引用不改变退出码，实际 %d", code)
	}
	data, _ := env["data"].(map[string]interface{})
	if got := relEdgeSigs(t, data, "relations_out", "target"); len(got) != 2 {
		t.Fatalf("悬空条目不得消失，relations_out = %v", got)
	}
	warns, _ := env["warnings"].([]interface{})
	q2 := 0
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		if m["code"] == "Q2" && strings.Contains(fmt.Sprint(m["message"]), "k-20260909-missing") {
			q2++
			if m["level"] != LevelWarning || m["op_index"] != float64(NonOpDiagnostic) {
				t.Fatalf("Q 系列必须是 warning + op_index=-1：%v", m)
			}
		}
	}
	if q2 != 1 {
		t.Fatalf("Q2 条数 = %d，期望恰一条（warnings=%v）", q2, warns)
	}
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "rel", "k-20260903-c")
	if !strings.Contains(out, "k-20260909-missing（目标不存在）") {
		t.Fatalf("文本模式应保留悬空条目并标注「目标不存在」：%s", out)
	}
}

// TestRelQueryUnparsableQ1 —— 坏 .md 记 Q1（含相对路径）+ Q3 汇总说明结果不完整，
// 结果仍返回、退出码仍 0，skipped_files 计入。
func TestRelQueryUnparsableQ1(t *testing.T) {
	dir := relVault(t)
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "broken.md"),
		"---\n- 1\n---\n\n# 坏卡\n")
	code, env, _ := runRelJSON(t, dir, "k-20260902-b")
	if code != ExitOK {
		t.Fatalf("Q 类不得改变退出码，实际 %d", code)
	}
	data, _ := env["data"].(map[string]interface{})
	if data["skipped_files"] != float64(1) || data["scanned_files"] != float64(4) {
		t.Fatalf("计数不符：scanned=%v skipped=%v（期望 4 / 1）",
			data["scanned_files"], data["skipped_files"])
	}
	warns, _ := env["warnings"].([]interface{})
	q1, q3 := 0, 0
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		switch m["code"] {
		case "Q1":
			q1++
			if !strings.Contains(fmt.Sprint(m["path"]), "domains/ai-infra/knowledge/broken.md") {
				t.Fatalf("Q1 缺坏文件相对路径：%v", m)
			}
		case "Q3":
			q3++
		}
	}
	if q1 != 1 || q3 != 1 {
		t.Fatalf("Q1 = %d、Q3 = %d，期望各恰一条（warnings=%v）", q1, q3, warns)
	}
	if got := relEdgeSigs(t, data, "relations_in", "from"); len(got) != 2 {
		t.Fatalf("一个坏文件不得丢掉其余结果：%v", got)
	}
}

// TestRelQueryNotFound —— 卡不存在退 1；--json 的 ok == false 且 exit_code == 1。
func TestRelQueryNotFound(t *testing.T) {
	dir := relVault(t)
	code, env, _ := runRelJSON(t, dir, "k-20260909-missing")
	if code != ExitUsage {
		t.Fatalf("卡不存在应退 1，实际 %d", code)
	}
	if env["ok"] != false || env["exit_code"] != float64(ExitUsage) || env["status"] != StatusFailed {
		t.Fatalf("信封与退出码不一致：%v", env)
	}
	r := newTestRoot(t, dir)
	_, _, errOut := runCLI(t, r, "--vault", dir, "rel", "k-20260909-missing")
	if !strings.Contains(errOut, "k-20260909-missing") {
		t.Fatalf("stderr 应给出可定位提示：%q", errOut)
	}
}

// TestRelQueryExitCodes —— 退出码边界：正常 0；卡不存在 / ID 非法 / 位置参数个数不对 /
// --to 形态非法一律 1；**不出现** 2 / 3 / 4。
func TestRelQueryExitCodes(t *testing.T) {
	dir := relVault(t)
	r := newTestRoot(t, dir)
	if code, _, _ := runCLI(t, r, "--vault", dir, "rel", "k-20260901-a"); code != ExitOK {
		t.Fatalf("正常路径退出码 = %d，期望 0", code)
	}
	for _, args := range [][]string{
		{"rel"},
		{"rel", "k-20260901-a", "extra"},
		{"rel", "not-an-id"},
		{"rel", "s-20260901-x"},
		{"rel", "k-20260909-missing"},
		{"rel", "k-20260901-a", "--to", "not-an-id"},
	} {
		rr := newTestRoot(t, dir)
		code, _, errOut := runCLI(t, rr, append([]string{"--vault", dir}, args...)...)
		if code != ExitUsage {
			t.Errorf("eg %v 退出码 = %d，期望 1", args, code)
		}
		if errOut == "" {
			t.Errorf("eg %v 未给 stderr 提示", args)
		}
	}
}

// TestRelQueryDualRenderingSameFacts —— 人类可读与 --json 同源同事实：
// 文本里的条目数与 data 里的条数逐一相等。
func TestRelQueryDualRenderingSameFacts(t *testing.T) {
	dir := relVault(t)
	_, env, _ := runRelJSON(t, dir, "k-20260903-c")
	data, _ := env["data"].(map[string]interface{})
	outEdges, _ := data["relations_out"].([]interface{})
	inEdges, _ := data["relations_in"].([]interface{})
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "rel", "k-20260903-c")
	if n := strings.Count(out, "正向关系："); n != len(outEdges) {
		t.Fatalf("文本正向行数 = %d，data 里 %d 条", n, len(outEdges))
	}
	if n := strings.Count(out, "反向关系："); n != len(inEdges) {
		t.Fatalf("文本反向行数 = %d，data 里 %d 条", n, len(inEdges))
	}
	if !strings.Contains(out, fmt.Sprint(data["id"])) {
		t.Fatalf("文本模式缺起点卡 ID：%s", out)
	}
}

// TestRelQueryZeroSideEffect —— 合同 §6：三类**只读**调用（读 / 不存在的 id /
// M3S2 占位的 remove）前后 git status --porcelain、git log 条数、vault 内文件字节与
// mtime 逐一相等；退出码只 0 / 1。
//
// T-…-024 起 `rel add` 是真实写路径（verb=relate，一次 commit），因此从本清单移出，
// 它的写入事实与退出码由 TestRelAdd* 与 test/e2e/m2_rel_add.sh 接管（覆盖面不减）。
func TestRelQueryZeroSideEffect(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("无 git，跳过 git 层零副作用断言")
	}
	dir := relVault(t)
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
		{"rel", "k-20260901-a"},
		{"rel", "k-20260909-missing"},
		{"rel", "remove", "k-20260901-a", "supports", "k-20260902-b"},
	} {
		rr := newTestRoot(t, dir)
		code, _, _ := runCLI(t, rr, append([]string{"--vault", dir}, args...)...)
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
