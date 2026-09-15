package cli

// T-…-022 的 CLI 层机器判据：`eg card show` 的 data 键序、五分区键集合恒定、
// 正反向关系、[失效] 标记、Q2 悬空引用 warning、卡不存在退 1 与**只读零副作用**。
//
// 判据来源：M2 查询合同 §2（用法 / data 键表 / 五分区 / markers / 退出码）、§3.2 / §3.3
// （正反向取数与排序）、§5（Q 系列）、§6（零副作用）；CLI 合同 §3（信封五键）、§4（退出码）。
// 测试名统一含 `CardShow`，与 verify.test 的 `-run CardShow` 对齐。

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// cardVault 造一个 vault：A ↔ B 互指（A 三条正向：supports B / opposing C / limits 悬空）、
// C 是失效卡、B 只写了「知识内容」一个分区。
func cardVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\n  - ops\ndefault_domain: ai-infra\n")
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260901-a.md"),
		"---\nid: k-20260901-a\nstatus: active\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-12T10:00:00+08:00'\n"+
			// 语料显式置「已过目」（reviewed_at == updated_at，严格大于才算未过目）：
			// 本组用例钉的是关系与分区口径，不该被 T-…-043 启用的过目维度标记连带影响。
			"reviewed_at: '2026-09-12T10:00:00+08:00'\ntitle: 注意力机制的计算代价\n"+
			"tags:\n  - attention\nsources:\n"+
			"  - source: s-20260901-x\n    note: n-20260901-x\n    rel: support\n"+
			"    reason: 实测数据支持该结论\n"+
			"relations:\n"+
			"  - type: supports\n    target: k-20260902-b\n    reason: 支持 B\n"+
			"  - type: opposing\n    target: k-20260903-c\n    reason: 与 C 冲突\n"+
			"  - type: limits\n    target: k-20260909-none\n    reason: 指向不存在的卡\n"+
			"---\n\n## 知识内容\n\n结论正文。\n\n## 解释与依据\n\n依据正文。\n\n"+
			"## 条件与边界\n\n边界正文。\n\n## 用户补充\n\n用户写的。\n\n## 理解自检\n\n自检问题。\n")
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260902-b.md"),
		"---\nid: k-20260902-b\nstatus: active\ncreated_at: '2026-09-02'\n"+
			"updated_at: '2026-09-02T10:00:00+08:00'\n"+
			"reviewed_at: '2026-09-02T10:00:00+08:00'\ntitle: 被指向的卡\nsources: []\n"+
			"relations:\n  - type: derives\n    target: k-20260901-a\n    reason: 由 A 推出\n"+
			"---\n\n## 知识内容\n\n只写了这一个分区。\n")
	// C 直接写文件而不走 seedCard：需要显式带上 reviewed_at 让它处于「已过目」，
	// 这样 TestCardShowDeprecatedMarker 钉的仍是**失效维度单独**成立（三维正交）。
	writeFileMk(t, filepath.Join(dir, "domains", "ops", "knowledge", "k-20260903-c.md"),
		"---\nid: k-20260903-c\nstatus: deprecated\ncreated_at: '2026-09-03'\n"+
			"updated_at: '2026-09-03T10:00:00+08:00'\n"+
			"reviewed_at: '2026-09-03T10:00:00+08:00'\ntitle: 失效的卡\nsources: []\n"+
			"---\n\n## 知识内容\n\n正文占位。\n")
	return dir
}

// runCardShowJSON 跑一次 `eg card show --json` 并解出信封。
func runCardShowJSON(t *testing.T, dir string, args ...string) (int, map[string]interface{}, string) {
	t.Helper()
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r,
		append([]string{"--vault", dir, "--json", "card", "show"}, args...)...)
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q / %q）", err, out, errOut)
	}
	assertEnvelopeKeys(t, env)
	return code, env, errOut
}

// assertKeyOrder 断言原文里若干键**按给定次序**先后出现（逐字反证键序，不只比键集合）。
func assertKeyOrder(t *testing.T, raw string, from int, keys []string) {
	t.Helper()
	at := from
	for _, k := range keys {
		i := strings.Index(raw[at:], k)
		if i < 0 {
			t.Fatalf("缺键 %s 或键序与合同不一致：%s", k, raw)
		}
		at += i
	}
}

// TestCardShowDataKeyOrderMatchesContract —— data 键序 = 合同 §2.2 键表次序；
// sections 键序 = 五分区声明序（F5）；relations_out[] / relations_in[] 元素恰五键。
func TestCardShowDataKeyOrderMatchesContract(t *testing.T) {
	dir := cardVault(t)
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "--vault", dir, "--json", "card", "show", "k-20260901-a")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（%s / %s）", code, out, errOut)
	}
	assertKeyOrder(t, out, strings.Index(out, `"data":`), []string{
		`"id":`, `"title":`, `"domain":`, `"status":`, `"deprecated":`, `"created_at":`,
		`"updated_at":`, `"path":`, `"tags":`, `"markers":`, `"sections":`, `"sources":`,
		`"relations_out":`, `"relations_in":`})
	secAt := strings.Index(out, `"sections":`)
	var secKeys []string
	for _, name := range mdfile.CardSections() {
		secKeys = append(secKeys, `"`+name+`":`)
	}
	assertKeyOrder(t, out, secAt, secKeys)
	assertKeyOrder(t, out, strings.Index(out, `"relations_out":`),
		[]string{`"from":`, `"type":`, `"target":`, `"reason":`, `"path":`})
	assertKeyOrder(t, out, strings.Index(out, `"sources":`),
		[]string{`"source":`, `"note":`, `"rel":`, `"reason":`})
}

// TestCardShowFixedSectionKeys —— 固定分区键集合恒定：「齐全」与「只写知识内容」两张卡的
// sections 键集合与键序两次完全相同，缺分区值为空串而非缺键；文本模式标注「（本分区缺失）」。
//
// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：原名 TestCardShowFiveSections，
// 钉的是 v1 的五分区。契约 D-7 把 Knowledge 收敛为 `知识内容 / 条件与边界 / 用户补充`
// 三分区，`card show` 的固定键表随 mdfile.CardSections() 一起变成三键。
//
// 判据一格没放宽，反而多了一条：键数从写死的 5 改为 len(CardSections())（键表是唯一真源，
// 用例不再各写一份数字），并**新增**「被移除的两个分区不得再出现在固定键表里」——
// 否则「模板收敛了」这件事在读路径上无从验证。
//
// 本用例的夹具 k-20260901-a 是一份**手写的 v1 存量卡**（五个 H2 都在），因此它同时是
// 「v2 读路径读 v1 存量文件」的场地：固定三键照常给出，`解释与依据` / `理解自检` 的字节
// 仍在文件里（字节保真由 mdfile / store 层的用例守着），但**不在** card show 的固定键表中。
// 这条可见性缺口已登记为 I-…-007（minor），由 T-…-006 的读路径任务收口；
// 本任务不放宽固定键表来提前兑现它 —— 把未知分区混进固定键集合会让「键集合恒定」失效。
func TestCardShowFixedSectionKeys(t *testing.T) {
	dir := cardVault(t)
	keysOf := func(id string) ([]string, map[string]interface{}) {
		_, env, _ := runCardShowJSON(t, dir, id)
		data, _ := env["data"].(map[string]interface{})
		sec, ok := data["sections"].(map[string]interface{})
		if !ok {
			t.Fatalf("data.sections 不是对象：%v", data["sections"])
		}
		out := []string{}
		for _, name := range mdfile.CardSections() {
			v, ok := sec[name]
			if !ok {
				t.Fatalf("%s 缺分区键 %s（键集合必须恒定）：%v", id, name, sec)
			}
			out = append(out, name)
			if _, isStr := v.(string); !isStr {
				t.Fatalf("分区 %s 的值必须是字符串，实际 %T", name, v)
			}
		}
		if len(sec) != len(mdfile.CardSections()) {
			t.Fatalf("%s 的 sections 键数 = %d，期望恰 %d（%v）",
				id, len(sec), len(mdfile.CardSections()), mdfile.CardSections())
		}
		// v2 起被移出模板的分区不得再作为固定键出现（见上：读路径可见性走 I-…-007）。
		for _, legacy := range mdfile.LegacyV1Sections(mdfile.KindCard) {
			if _, ok := sec[legacy]; ok {
				t.Fatalf("%s 的固定键表不得含 v1 存量分区 %q：%v", id, legacy, sec)
			}
		}
		return out, sec
	}
	fullKeys, full := keysOf("k-20260901-a")
	partKeys, part := keysOf("k-20260902-b")
	if strings.Join(fullKeys, ",") != strings.Join(partKeys, ",") {
		t.Fatalf("两张卡的 sections 键序不同：%v / %v", fullKeys, partKeys)
	}
	if full[mdfile.SecBoundary] == "" {
		t.Fatal("分区齐全的卡「条件与边界」不应为空")
	}
	if part[mdfile.SecBoundary] != "" {
		t.Fatalf("缺分区应为空串，实际 %q", part[mdfile.SecBoundary])
	}
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260902-b")
	if !strings.Contains(out, "分区 "+mdfile.SecBoundary+"：（本分区缺失）") {
		t.Fatalf("文本模式应标注缺失分区：%s", out)
	}
}

// TestCardShowRelationsOut —— 正向关系恰为本卡 relations[]，按 type 固定次序输出
// （opposing 在前），from 恒为本卡 ID。
//
// 【T-…-061 重钉（owner 裁决② A-38/A-39）】原断言期望默认 relations_out 恰 3 条（含 opposing→C，C 失效）。
// 新合同：deprecated 对端**默认隐藏**（§5.1），默认只剩 2 条（limits→none、supports→B）；
// 显式 --include-deprecated 后 3 条全出且 C 带 [失效] 标记。用例名与「from 恒本卡 / 元素恰 5 键 / type 次序」
// 判据保留，只改被裁决推翻的条数与展示那一格。
func TestCardShowRelationsOut(t *testing.T) {
	dir := cardVault(t)
	// 默认视图：opposing→C 被隐藏（C 为 deprecated 对端）。
	_, env, _ := runCardShowJSON(t, dir, "k-20260901-a")
	data, _ := env["data"].(map[string]interface{})
	edges, _ := data["relations_out"].([]interface{})
	if len(edges) != 2 {
		t.Fatalf("默认 relations_out 条数 = %d，期望 2（deprecated 对端 C 默认隐藏）", len(edges))
	}
	var got []string
	for _, e := range edges {
		m, _ := e.(map[string]interface{})
		if len(m) != 5 {
			t.Fatalf("relations_out 元素键数 = %d，期望恰 5：%v", len(m), m)
		}
		if m["from"] != "k-20260901-a" {
			t.Fatalf("from = %v，应恒为本卡 ID", m["from"])
		}
		got = append(got, fmt.Sprintf("%v→%v", m["type"], m["target"]))
	}
	wantDefault := []string{"limits→k-20260909-none", "supports→k-20260902-b"}
	if strings.Join(got, ",") != strings.Join(wantDefault, ",") {
		t.Fatalf("默认 relations_out 次序 = %v，期望 %v（§5.1 deprecated 默认隐藏）", got, wantDefault)
	}
	// 显式放开：3 条全出，type 固定次序不变（合同 §3.3），data 元素仍恰 5 键（不扩张）。
	_, envAll, _ := runCardShowJSON(t, dir, "k-20260901-a", "--include-deprecated")
	dataAll, _ := envAll["data"].(map[string]interface{})
	edgesAll, _ := dataAll["relations_out"].([]interface{})
	var all []string
	for _, e := range edgesAll {
		m, _ := e.(map[string]interface{})
		if len(m) != 5 {
			t.Fatalf("--include-deprecated relations_out 元素键数 = %d，期望恰 5", len(m))
		}
		all = append(all, fmt.Sprintf("%v→%v", m["type"], m["target"]))
	}
	wantAll := []string{"opposing→k-20260903-c", "limits→k-20260909-none", "supports→k-20260902-b"}
	if strings.Join(all, ",") != strings.Join(wantAll, ",") {
		t.Fatalf("--include-deprecated relations_out 次序 = %v，期望 %v（合同 §3.3）", all, wantAll)
	}
	// 人类可读：显式放开时展示的失效对端带 [失效] 标记（§2.2 文案不变）。
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260901-a", "--include-deprecated")
	if !strings.Contains(out, "k-20260903-c") || !strings.Contains(out, MarkerDeprecated) {
		t.Fatalf("显式放开时应展示 C 并带 %s 标记：%s", MarkerDeprecated, out)
	}
}

// TestCardShowRelationsIn —— 反向关系走全库扫描：A 指向 B，`card show B` 的 relations_in[]
// 恰含一条 from == A；文本模式同样出现该条目。opposing 单向存储，读路径不补对称条目。
func TestCardShowRelationsIn(t *testing.T) {
	dir := cardVault(t)
	_, env, _ := runCardShowJSON(t, dir, "k-20260902-b")
	data, _ := env["data"].(map[string]interface{})
	edges, _ := data["relations_in"].([]interface{})
	if len(edges) != 1 {
		t.Fatalf("relations_in 条数 = %d，期望 1（%v）", len(edges), edges)
	}
	m, _ := edges[0].(map[string]interface{})
	if m["from"] != "k-20260901-a" || m["type"] != "supports" ||
		m["target"] != "k-20260902-b" ||
		m["path"] != "domains/ai-infra/knowledge/k-20260901-a.md" {
		t.Fatalf("反向条目不符合合同 §3.2：%v", m)
	}
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260902-b")
	if !strings.Contains(out, "反向关系：k-20260901-a --supports--> k-20260902-b") {
		t.Fatalf("文本模式缺反向关系行：%s", out)
	}
	// 跨领域也看得见：C 在 ops 领域，A 在 ai-infra 领域。
	_, envC, _ := runCardShowJSON(t, dir, "k-20260903-c")
	dataC, _ := envC["data"].(map[string]interface{})
	inC, _ := dataC["relations_in"].([]interface{})
	outC, _ := dataC["relations_out"].([]interface{})
	if len(inC) != 1 || len(outC) != 0 {
		t.Fatalf("opposing 单向存储：C 应只在 relations_in 出现一条，实际 in=%v out=%v", inC, outC)
	}
}

// TestCardShowDeprecatedMarker —— 失效卡 --json 的 deprecated == true 且
// markers == ["[失效]"]；文本模式标题行以 [失效] 开头。
func TestCardShowDeprecatedMarker(t *testing.T) {
	dir := cardVault(t)
	code, env, _ := runCardShowJSON(t, dir, "k-20260903-c")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0", code)
	}
	data, _ := env["data"].(map[string]interface{})
	if data["deprecated"] != true || data["status"] != "deprecated" {
		t.Fatalf("失效卡 deprecated / status = %v / %v", data["deprecated"], data["status"])
	}
	markers, _ := data["markers"].([]interface{})
	if len(markers) != 1 || markers[0] != MarkerDeprecated {
		t.Fatalf("markers = %v，期望 [%s]", markers, MarkerDeprecated)
	}
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260903-c")
	title := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, MarkerDeprecated) {
			title = line
		}
	}
	if title == "" {
		t.Fatalf("文本模式标题行应以 %s 开头：%s", MarkerDeprecated, out)
	}
	for _, banned := range []string{"[已删除]", "[未过目]", "[材料支持不足]"} {
		if strings.Contains(out, banned) {
			t.Fatalf("M2 不得输出 %s（S2 / S3）：%s", banned, out)
		}
	}
}

// TestCardShowDanglingRelationQ2 —— 悬空引用：warnings[] 含一条 Q2 且 message 含目标 ID，
// 条目仍在 relations_out[] 里且文本模式标注「目标不存在」，退出码仍 0。
func TestCardShowDanglingRelationQ2(t *testing.T) {
	dir := cardVault(t)
	code, env, _ := runCardShowJSON(t, dir, "k-20260901-a")
	if code != ExitOK {
		t.Fatalf("悬空引用不改变退出码，实际 %d", code)
	}
	warns, _ := env["warnings"].([]interface{})
	q2 := 0
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		if m["code"] == "Q2" && strings.Contains(fmt.Sprint(m["message"]), "k-20260909-none") {
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
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260901-a")
	if !strings.Contains(out, "k-20260909-none（目标不存在）") {
		t.Fatalf("文本模式应标注「目标不存在」：%s", out)
	}
}

// TestCardShowNotFound —— 卡不存在退 1、stderr 可定位、--json 的 ok == false 且
// exit_code == 1；ID 形态非法同样退 1。
func TestCardShowNotFound(t *testing.T) {
	dir := cardVault(t)
	code, env, _ := runCardShowJSON(t, dir, "k-20260909-none")
	if code != ExitUsage {
		t.Fatalf("卡不存在应退 1，实际 %d", code)
	}
	if env["ok"] != false || env["exit_code"] != float64(ExitUsage) || env["status"] != StatusFailed {
		t.Fatalf("信封与退出码不一致：%v", env)
	}
	r := newTestRoot(t, dir)
	_, _, errOut := runCLI(t, r, "--vault", dir, "card", "show", "k-20260909-none")
	if !strings.Contains(errOut, "k-20260909-none") {
		t.Fatalf("stderr 应给出可定位提示：%q", errOut)
	}
}

// TestCardShowDualRenderingSameFacts —— 人类可读与 --json 同源同事实：
// 文本里的 ID / 路径 / 关系条数都能在 data 里逐字对上。
func TestCardShowDualRenderingSameFacts(t *testing.T) {
	dir := cardVault(t)
	_, env, _ := runCardShowJSON(t, dir, "k-20260901-a")
	data, _ := env["data"].(map[string]interface{})
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260901-a")
	for _, want := range []string{
		fmt.Sprint(data["id"]), fmt.Sprint(data["title"]), fmt.Sprint(data["path"]),
		fmt.Sprint(data["updated_at"]), "s-20260901-x",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("文本模式缺 %q（与 --json 不同源）：%s", want, out)
		}
	}
	outEdges, _ := data["relations_out"].([]interface{})
	if n := strings.Count(out, "正向关系："); n != len(outEdges) {
		t.Fatalf("文本模式正向关系行数 = %d，data 里 %d 条", n, len(outEdges))
	}
}

// TestCardShowExitCodes —— 退出码边界：正常 0；卡不存在 / ID 非法 / 位置参数个数不对 /
// 缺子命令一律 1；**不出现** 2 / 3 / 4。
func TestCardShowExitCodes(t *testing.T) {
	dir := cardVault(t)
	r := newTestRoot(t, dir)
	if code, _, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260901-a"); code != ExitOK {
		t.Fatalf("正常路径退出码 = %d，期望 0", code)
	}
	for _, args := range [][]string{
		{"card", "show"},
		{"card", "show", "a", "b"},
		{"card", "show", "not-an-id"},
		{"card", "show", "s-20260901-x"},
		{"card", "show", "k-20260909-none"},
		{"card"},
		{"card", "list"},
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

// TestCardShowZeroSideEffect —— 合同 §6：git status --porcelain 前后逐字相等、
// git log 条数不变、vault 内文件字节与 mtime 不变；退出码只可能 0 / 1。
func TestCardShowZeroSideEffect(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("无 git，跳过 git 层零副作用断言")
	}
	dir := cardVault(t)
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
		{"card", "show", "k-20260901-a"},
		{"card", "show", "k-20260901-a", "--json"},
		{"card", "show", "k-20260903-c"},
		{"card", "show", "k-20260909-none"},
		{"card", "show", "not-an-id"},
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
