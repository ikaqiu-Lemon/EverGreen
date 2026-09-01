package cli

// 逐卡收敛结论的机器判据（T-evergreen.s1_main_flow-158614-026 Acceptance）。
//
// 用例名以 Convergence / Report 开头，可用 `go test ./internal/cli/... -run 'Convergence|Report'` 单跑。
// 判据来源：ChangePlan 合同 §2 / §2.1（字段与七值封闭枚举）、CLI 合同 §1.5 / §1.9、
// 技术方案 §4.5 / §4.6，以及 M-002-m2.md 完成判据 6 / 9。
//
// 本文件**不出现**渲染首行的字面量（按运行期拼接成 convergeMarker）：
// 「唯一渲染实现」这条判据靠 `grep -rn` 反证——字面量在 internal/ 下只允许出现在 convergence.go。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// convergeMarker 是每张卡首行的前缀（运行期拼接，避免污染 grep 反证）。
var convergeMarker = "收敛" + "："

// sevenRelations 是合同 §2.1 的七值封闭枚举（顺序即构造顺序，用于顺序稳定判据）。
var sevenRelations = []string{
	"independent_new", "same_semantics", "non_core_supplement", "core_change",
	"conflict_coexist", "uncertain", "deprecated",
}

func countMarker(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, convergeMarker) {
			n++
		}
	}
	return n
}

// —— ① 两条路径同源：唯一渲染函数，且 internal/ 下没有第二套渲染 ——

func TestConvergenceRenderedOnce(t *testing.T) {
	// apply.go 与 report.go 各至少调用一次 convergenceLines（同源渲染的正证）。
	for _, f := range []string{"apply.go", "report.go"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到 %s：%v", f, err)
		}
		if n := strings.Count(string(raw), "convergenceLines"); n < 1 {
			t.Fatalf("%s 未调用 convergenceLines（两条路径必须共用唯一渲染函数）", f)
		}
	}
	// 反证：渲染字面量在 internal/ 下只允许出现在 convergence.go。
	root := filepath.Join("..", "..", "internal")
	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) == "convergence.go" {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(raw), convergeMarker) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 internal/ 失败：%v", err)
	}
	if len(offenders) != 0 {
		t.Fatalf("除 convergence.go 外还有 %d 处渲染实现（禁止两份渲染漂移）：%v",
			len(offenders), offenders)
	}
}

// —— ② 七值关系全覆盖：恰 7 段，关系名逐字出现 ——

func TestConvergenceAllSevenRelations(t *testing.T) {
	items := make([]convergenceItem, 0, 7)
	for i, rel := range sevenRelations {
		items = append(items, convergenceItem{
			Card:         "k-2026090" + string(rune('1'+i)) + "-demo",
			Relation:     rel,
			Core:         "same",
			Conditions:   "different",
			ReusePurpose: "same",
			Note:         "第 " + rel + " 条",
		})
	}
	lines := convergenceLines(items)
	if got := countMarker(lines); got != 7 {
		t.Fatalf("卡段数 = %d，期望恰 7（七值封闭枚举各一条）：\n%s", got, strings.Join(lines, "\n"))
	}
	if want := 1 + 7*5; len(lines) != want {
		t.Fatalf("行数 = %d，期望 %d（标题 1 行 + 每卡恰 5 行）", len(lines), want)
	}
	text := strings.Join(lines, "\n")
	for _, rel := range sevenRelations {
		if !strings.Contains(text, rel) {
			t.Fatalf("输出缺关系名 %q（渲染不得改写枚举字面量）", rel)
		}
	}
	for _, dim := range []string{"核心知识", "成立条件", "独立复用用途", "说明"} {
		if strings.Count(text, dim) != 7 {
			t.Fatalf("维度 %q 出现 %d 次，期望 7 次", dim, strings.Count(text, dim))
		}
	}
}

// —— ③ 缺字段如实标注「未给出」，不编造 ——

func TestConvergenceMissingFieldsHonest(t *testing.T) {
	in := []convergenceItem{{Card: "k-20260901-attention", Relation: "uncertain"}}
	lines := convergenceLines(in)
	if got := countMarker(lines); got != 1 {
		t.Fatalf("卡段数 = %d，期望 1", got)
	}
	if n := strings.Count(strings.Join(lines, "\n"), ConvergenceMissing); n != 4 {
		t.Fatalf("「%s」出现 %d 次，期望 4 次（三维度 + note 全缺）：\n%s",
			ConvergenceMissing, n, strings.Join(lines, "\n"))
	}
	// 输出里出现的卡 ID 集合必须与输入完全相等（反证「不编造」）。
	idRE := regexp.MustCompile(`k-[0-9]{8}-[a-z0-9-]+`)
	got := map[string]bool{}
	for _, m := range idRE.FindAllString(strings.Join(lines, "\n"), -1) {
		got[m] = true
	}
	want := map[string]bool{"k-20260901-attention": true}
	if len(got) != len(want) {
		t.Fatalf("输出卡 ID 集合 = %v，输入 = %v（不得编造未出现过的 ID）", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Fatalf("输出缺输入里的卡 ID %s", id)
		}
	}
	// 空数组不打任何行（不替 Agent 说「没有收敛」）。
	if got := convergenceLines(nil); got != nil {
		t.Fatalf("空输入必须零行输出，实得 %v", got)
	}
}

// —— ④ 顺序稳定：不重排、不去重、不合并；重复跑逐字相等 ——

func TestConvergenceOrderPreserved(t *testing.T) {
	in := []convergenceItem{
		{Card: "k-20260903-c", Relation: "core_change", Core: "different"},
		{Card: "k-20260901-a", Relation: "same_semantics", Core: "same"},
		{Card: "k-20260901-a", Relation: "non_core_supplement", Core: "same"}, // 同 ID 重复：不得去重
		{Card: "k-20260902-b", Relation: "uncertain", Core: "different"},
	}
	lines := convergenceLines(in)
	if got := countMarker(lines); got != len(in) {
		t.Fatalf("卡段数 = %d，期望 %d（不得去重、不得合并）", countMarker(lines), len(in))
	}
	var order []string
	for _, l := range lines {
		if strings.Contains(l, convergeMarker) {
			order = append(order, l)
		}
	}
	for i, c := range in {
		if !strings.Contains(order[i], c.Card) || !strings.Contains(order[i], c.Relation) {
			t.Fatalf("第 %d 段 = %q，期望对应输入 %+v（渲染顺序 == convergence[] 原始顺序）",
				i, order[i], c)
		}
	}
	if a, b := strings.Join(convergenceLines(in), "\n"), strings.Join(lines, "\n"); a != b {
		t.Fatal("同一输入连续渲染结果不一致：渲染必须是纯函数")
	}
}

// —— ⑤ apply 与 report --last 的文本行块逐字相等（同源同事实）——

func TestConvergenceApplyAndReportRenderIdentically(t *testing.T) {
	dir := applyVault(t)
	code, _, errOut := runApplyPlan(t, dir, notePlanWith("", convergenceTop))
	if code != ExitOK {
		t.Fatalf("apply 退出码 = %d，期望 0：%s", code, errOut)
	}
	// 文本模式重跑一次 apply（同一份 plan，走 --dry-run 会少 commit，故直接用落盘记录比对）。
	_, applyText, _ := runCLI(t, newTestRoot(t, dir), "report", "--last", "--vault", dir)
	replayBlock := convergeBlock(t, applyText)
	if countMarkerText(replayBlock) != 2 {
		t.Fatalf("report --last 的收敛行块应含 2 段：\n%s", replayBlock)
	}

	// 直接对比 applyResult 的 Summary 与回放 Summary 的行块：两者由同一函数产出。
	dir2 := applyVault(t)
	r := newTestRoot(t, dir2)
	r.In = strings.NewReader(notePlanWith("", convergenceTop))
	code, applyHuman, errOut := runCLI(t, r, "apply", "--vault", dir2, "--plan", "-")
	if code != ExitOK {
		t.Fatalf("apply（文本模式）退出码 = %d：%s", code, errOut)
	}
	applyBlock := convergeBlock(t, applyHuman)
	if applyBlock == "" {
		t.Fatalf("apply 文本模式未渲染收敛行块：\n%s", applyHuman)
	}
	_, reportHuman, _ := runCLI(t, newTestRoot(t, dir2), "report", "--last", "--vault", dir2)
	if got := convergeBlock(t, reportHuman); got != applyBlock {
		t.Fatalf("两条路径的收敛行块必须逐字相等\napply ：\n%s\nreport：\n%s", applyBlock, got)
	}
}

// convergenceTop 是两条收敛记录的 plan 顶层片段（一条齐全、一条只给 card + relation）。
const convergenceTop = `,"convergence":[
{"card":"k-20260901-attention","relation":"non_core_supplement","core_knowledge":"same",
"conditions":"different","reuse_purpose":"same","note":"补了一条成立条件"},
{"card":"k-20260902-rnn","relation":"uncertain"}]`

// convergeBlock 抽出输出里的收敛行块（标题行起，至「数据：」/「警告（」前止）。
func convergeBlock(t *testing.T, out string) string {
	t.Helper()
	var block []string
	in := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "逐卡收敛记录") {
			in = true
		}
		if in && (strings.HasPrefix(line, "数据：") || strings.HasPrefix(line, "警告（")) {
			break
		}
		if in {
			block = append(block, line)
		}
	}
	return strings.Join(block, "\n")
}

func countMarkerText(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, convergeMarker) {
			n++
		}
	}
	return n
}

// —— ⑥ 落盘记录缺该字段：退 0、如实说明、无 panic ——

func TestReportLastWithoutConvergence(t *testing.T) {
	dir := applyVault(t)
	if code, _, errOut := runApplyPlan(t, dir, notePlan()); code != ExitOK {
		t.Fatalf("apply 退出码 = %d：%s", code, errOut)
	}
	// 手工把 convergence 键从落盘记录里摘掉（模拟 M1 期旧记录）。
	abs := filepath.Join(dir, filepath.FromSlash(LastReportFile))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读不到落盘记录：%v", err)
	}
	var rec struct {
		ExitCode int                        `json:"exit_code"`
		Status   string                     `json:"status"`
		Data     map[string]json.RawMessage `json:"data"`
		Warnings []Diagnostic               `json:"warnings"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("落盘记录不可解析：%v", err)
	}
	delete(rec.Data, "convergence")
	patched, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, patched, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCLI(t, newTestRoot(t, dir), "report", "--last", "--vault", dir)
	if code != ExitOK {
		t.Fatalf("缺字段时必须照常退 0，实得 %d：%s", code, errOut)
	}
	if !strings.Contains(out, ConvergenceAbsentNotice) {
		t.Fatalf("输出必须如实说明「%s」：\n%s", ConvergenceAbsentNotice, out)
	}
	if strings.Contains(out, convergeMarker) {
		t.Fatalf("缺字段时不得凭空渲染任何卡段：\n%s", out)
	}
}

// —— ⑦ --json 的 data 键集合与每条收敛记录的键集合与 M1 逐字不变 ——

func TestApplyDataKeysUnchanged(t *testing.T) {
	dir := applyVault(t)
	code, env, errOut := runApplyPlan(t, dir, notePlanWith("", convergenceTop))
	if code != ExitOK {
		t.Fatalf("apply 退出码 = %d：%s", code, errOut)
	}
	// M1 基线：data 顶层恰三键（--dry-run 时另加 planned）。
	var gotKeys []string
	for k := range env.Data {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	if strings.Join(gotKeys, ",") != "convergence,domain,report" {
		t.Fatalf("data 顶层键 = %v，期望 [convergence domain report]（M1 基线逐字不变）", gotKeys)
	}
	// 每条收敛记录恰 6 键。
	raw, err := json.Marshal(env.Data["convergence"])
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("data.convergence 不可解析：%v", err)
	}
	if len(items) != 2 {
		t.Fatalf("data.convergence 条目数 = %d，期望 2（原样回带，不去重）", len(items))
	}
	want := []string{"card", "conditions", "core_knowledge", "note", "relation", "reuse_purpose"}
	for i, it := range items {
		var keys []string
		for k := range it {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Fatalf("第 %d 条键集合 = %v，期望 %v（M1 逐字不变）", i, keys, want)
		}
	}
}
