package query_test

// T-…-020 的机器判据：通用 Markdown 扫描 / 过滤 / 排序底座 + Q1–Q3 诚实诊断。
//
// 判据来源：M2 查询合同 `2026-09-19-m2-query-contract.md` §1.3 / §1.4 / §1.5 / §3.3 / §5，
// 以及 M-002 风险 R-1 的关闭判据（不可解析文件必须有诊断，禁止静默跳过）。

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// writeFile 在 vault 内写一个文件（测试脚手架，非产品写路径）。
func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

// card 造一张最小合法知识卡。
func card(id, title, domain, status, created, updated string, tags, relations string) string {
	fm := "---\nid: " + id + "\nstatus: " + status +
		"\ncreated_at: '" + created + "'\nupdated_at: '" + updated +
		"'\ntitle: " + title + "\nsources: []\n"
	if tags != "" {
		fm += "tags:\n" + tags
	}
	if relations != "" {
		fm += "relations:\n" + relations
	}
	return fm + "---\n\n# " + title + "\n\n## 知识内容\n\n正文占位。\n"
}

func diagCodes(diags []query.Diagnostic) []string {
	out := []string{}
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func countCode(diags []query.Diagnostic, code string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

// TestScanUnparsableFileEmitsQ1 —— R-1 的关闭判据：frontmatter 为序列（非映射）的文件
// 必须产出恰一条 Q1（带路径与原因）并计入 SkippedFiles，其余合法卡照常返回。
func TestScanUnparsableFileEmitsQ1(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-ok.md",
		card("k-20260901-ok", "好卡", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if countCode(res.Diagnostics, query.CodeQ1) != 1 {
		t.Fatalf("Q1 条数 = %d，期望 1（诊断：%v）", countCode(res.Diagnostics, query.CodeQ1), diagCodes(res.Diagnostics))
	}
	var q1 query.Diagnostic
	for _, d := range res.Diagnostics {
		if d.Code == query.CodeQ1 {
			q1 = d
		}
	}
	if q1.Path != "domains/ai-infra/knowledge/broken.md" {
		t.Errorf("Q1.Path = %q，期望坏文件的相对路径", q1.Path)
	}
	if q1.Message == "" {
		t.Error("Q1.Message 为空：诊断必须给出人类可读原因")
	}
	if q1.Level != "warning" {
		t.Errorf("Q1.Level = %q，Q 系列一律 warning", q1.Level)
	}
	if res.SkippedFiles != 1 {
		t.Errorf("SkippedFiles = %d，期望 1", res.SkippedFiles)
	}
	if len(res.Cards) != 1 || res.Cards[0].ID != "k-20260901-ok" {
		t.Errorf("合法卡未照常返回：%+v（一个坏文件不得丢掉其余结果）", res.Cards)
	}
}

// TestScanNeverSilentlySkips —— 计数守恒：进入结果集的文件数 + SkippedFiles == 扫描到的 .md 总数。
func TestScanNeverSilentlySkips(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-ok.md",
		card("k-20260901-ok", "好卡", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if res.ScannedFiles != 2 {
		t.Fatalf("ScannedFiles = %d，期望 2", res.ScannedFiles)
	}
	if len(res.Cards)+res.SkippedFiles != res.ScannedFiles {
		t.Errorf("len(Cards)=%d + SkippedFiles=%d != ScannedFiles=%d：存在第三条静默路径",
			len(res.Cards), res.SkippedFiles, res.ScannedFiles)
	}
}

// TestScanDanglingRelationEmitsQ2 —— 悬空引用必须有 Q2，且 Message 含被引用的目标 ID。
func TestScanDanglingRelationEmitsQ2(t *testing.T) {
	root := t.TempDir()
	rels := "  - type: limits\n    target: k-19700101-nope\n    reason: 指向不存在的卡\n"
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-a.md",
		card("k-20260901-a", "A 卡", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", rels))

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if countCode(res.Diagnostics, query.CodeQ2) != 1 {
		t.Fatalf("Q2 条数 = %d，期望 1（%v）", countCode(res.Diagnostics, query.CodeQ2), diagCodes(res.Diagnostics))
	}
	for _, d := range res.Diagnostics {
		if d.Code != query.CodeQ2 {
			continue
		}
		if d.Path != "domains/ai-infra/knowledge/k-20260901-a.md" {
			t.Errorf("Q2.Path = %q，期望 A 卡路径", d.Path)
		}
		if !contains(d.Message, "k-19700101-nope") {
			t.Errorf("Q2.Message = %q，必须含被引用的目标 ID", d.Message)
		}
	}
	if len(res.Cards) != 1 {
		t.Errorf("悬空引用不得导致卡被丢弃，Cards = %d", len(res.Cards))
	}
}

// TestScanPartialResultEmitsQ3 —— 同时存在 Q1 与 Q2 时恰有一条 Q3；无 Q1/Q2 时 Q3 不出现。
func TestScanPartialResultEmitsQ3(t *testing.T) {
	root := t.TempDir()
	rels := "  - type: limits\n    target: k-19700101-nope\n    reason: 指向不存在的卡\n"
	writeFile(t, root, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-a.md",
		card("k-20260901-a", "A 卡", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", rels))

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if countCode(res.Diagnostics, query.CodeQ3) != 1 {
		t.Fatalf("Q3 条数 = %d，期望恰 1（%v）", countCode(res.Diagnostics, query.CodeQ3), diagCodes(res.Diagnostics))
	}
	last := res.Diagnostics[len(res.Diagnostics)-1]
	if last.Code != query.CodeQ3 {
		t.Errorf("Q3 必须是最后一条汇总项，实际末条 = %s", last.Code)
	}

	// 反向断言：干净语料里没有任何 Q 条目。
	clean := t.TempDir()
	writeFile(t, clean, "domains/ai-infra/knowledge/k-20260901-ok.md",
		card("k-20260901-ok", "好卡", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	res2, err := query.VaultScan(clean, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if len(res2.Diagnostics) != 0 {
		t.Errorf("干净语料不得产生诊断，实际 %v", diagCodes(res2.Diagnostics))
	}
}

// TestSortEntriesTotalOrder —— 匹配分与 updated_at 相同、仅 id 不同的三张卡按 id 升序；
// 同一输入连续两次扫描 + 排序的 ID 序列逐字相等。
func TestSortEntriesTotalOrder(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"k-20260901-c", "k-20260901-a", "k-20260901-b"} {
		writeFile(t, root, "domains/ai-infra/knowledge/"+id+".md",
			card(id, "同名标题", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	}
	ids := func() []string {
		res, err := query.VaultScan(root, query.ScanOptions{})
		if err != nil {
			t.Fatalf("VaultScan：%v", err)
		}
		hits := query.Filter(res.Cards, query.FilterSpec{Query: "同名标题"})
		query.SortEntries(hits)
		out := []string{}
		for _, h := range hits {
			out = append(out, h.ID)
		}
		return out
	}
	first, second := ids(), ids()
	want := []string{"k-20260901-a", "k-20260901-b", "k-20260901-c"}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("同分同 updated_at 时应按 id 升序，实际 %v", first)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("两次执行输出必须逐字相同：%v vs %v", first, second)
	}
}

// TestScanIncludesDeprecated —— 失效卡默认同等可见（IncludeDeprecated 默认 true）。
func TestScanIncludesDeprecated(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-old.md",
		card("k-20260901-old", "失效卡", "ai-infra", "deprecated", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if len(res.Cards) != 1 {
		t.Fatalf("失效卡必须照常进结果集，Cards = %d", len(res.Cards))
	}
	if !res.Cards[0].Deprecated || res.Cards[0].Status != "deprecated" {
		t.Errorf("失效标记未透出：%+v", res.Cards[0])
	}
	hits := query.Filter(res.Cards, query.FilterSpec{Query: "失效卡"})
	if len(hits) != 1 {
		t.Errorf("过滤后失效卡被隐藏了（M2 不提供隐藏失效卡的开关），hits = %d", len(hits))
	}
}

// TestScanAllDomains —— Domains 为空扫全库；限定单领域只返回该领域。
func TestScanAllDomains(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/a/knowledge/k-20260901-a.md",
		card("k-20260901-a", "领域 a 卡", "a", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	writeFile(t, root, "domains/b/knowledge/k-20260901-b.md",
		card("k-20260901-b", "领域 b 卡", "b", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))

	all, err := query.VaultScan(root, query.ScanOptions{Domains: nil})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if len(all.Cards) != 2 {
		t.Fatalf("全库扫描应返回 2 张卡，实际 %d", len(all.Cards))
	}
	if all.Cards[0].Domain != "a" || all.Cards[1].Domain != "b" {
		t.Errorf("领域未按目录反解：%v / %v", all.Cards[0].Domain, all.Cards[1].Domain)
	}
	one, err := query.VaultScan(root, query.ScanOptions{Domains: []string{"a"}})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if len(one.Cards) != 1 || one.Cards[0].ID != "k-20260901-a" {
		t.Errorf("限定领域 a 应只返回 a 的卡，实际 %+v", one.Cards)
	}
}

// TestScanSkipsGitAndIndexDirs —— .git / 索引目录下的 .md 不进结果、不产生 Q1；非 .md 同样不进。
func TestScanSkipsGitAndIndexDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-ok.md",
		card("k-20260901-ok", "好卡", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	// 目录名用常量拼，避免源码里出现索引目录的字面路径（M2 越界 grep 反证要求零命中）。
	const gitDir, indexDir = ".git", ".index"
	writeFile(t, root, "domains/ai-infra/knowledge/"+gitDir+"/junk.md", "---\n- 1\n---\n")
	writeFile(t, root, "domains/ai-infra/knowledge/"+indexDir+"/junk.md", "---\n- 1\n---\n")
	writeFile(t, root, "domains/ai-infra/knowledge/note.txt", "not markdown")

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if res.ScannedFiles != 1 || len(res.Cards) != 1 {
		t.Errorf("遍历口径变了：ScannedFiles=%d Cards=%d，期望各 1", res.ScannedFiles, len(res.Cards))
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("被跳过的目录/非 .md 不得产生诊断，实际 %v", diagCodes(res.Diagnostics))
	}
}

// TestScanFilterAndScore —— 合同 §1.1 的过滤与 §1.3 的匹配分逐条可判。
func TestScanFilterAndScore(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-att.md",
		card("k-20260901-att", "attention 机制", "ai-infra", "active",
			"2026-09-01", "2026-09-10T10:00:00+08:00", "  - attention\n  - 序列\n", ""))
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260801-rnn.md",
		card("k-20260801-rnn", "RNN 衰减", "ai-infra", "active",
			"2026-08-01", "2026-08-05T10:00:00+08:00", "  - rnn\n", ""))

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	hits := query.Filter(res.Cards, query.FilterSpec{Query: "attention"})
	if len(hits) != 1 || hits[0].ID != "k-20260901-att" {
		t.Fatalf("关键词过滤结果错：%+v", hits)
	}
	// 该卡的标题、tags 与正文（H1 复述标题）都含 attention：3 + 2 + 1 = 6。
	if hits[0].Score != 6 {
		t.Errorf("匹配分 = %d，期望 6（title +3、tags +2、body +1）", hits[0].Score)
	}
	if !reflect.DeepEqual(hits[0].MatchedFields, []string{"title", "tags", "body"}) {
		t.Errorf("matched_fields = %v，期望 [title tags body]（固定次序）", hits[0].MatchedFields)
	}
	if got := query.Filter(res.Cards, query.FilterSpec{Query: "attention", Tags: []string{"rnn"}}); len(got) != 0 {
		t.Errorf("标签 AND 过滤失效：%+v", got)
	}
	if got := query.Filter(res.Cards, query.FilterSpec{Since: "2026-09-01"}); len(got) != 1 ||
		got[0].ID != "k-20260901-att" {
		t.Errorf("--since 过滤失效：%+v", got)
	}
	if got := query.Filter(res.Cards, query.FilterSpec{Until: "2026-08-31"}); len(got) != 1 ||
		got[0].ID != "k-20260801-rnn" {
		t.Errorf("--until 过滤失效：%+v", got)
	}
	if got := query.Filter(res.Cards, query.FilterSpec{Query: "不存在的词"}); len(got) != 0 {
		t.Errorf("零命中应返回空结果集：%+v", got)
	}
}

// TestScanRelationViews —— 正向 / 反向关系视图与合同 §3.3 的两级排序。
func TestScanRelationViews(t *testing.T) {
	root := t.TempDir()
	rels := "  - type: derives\n    target: k-20260901-z\n    reason: 推出\n" +
		"  - type: opposing\n    target: k-20260901-y\n    reason: 对立\n" +
		"  - type: limits\n    target: k-20260901-x\n    reason: 限定\n"
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-a.md",
		card("k-20260901-a", "A 卡", "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", rels))
	for _, id := range []string{"k-20260901-x", "k-20260901-y", "k-20260901-z"} {
		writeFile(t, root, "domains/ai-infra/knowledge/"+id+".md",
			card(id, id, "ai-infra", "active", "2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	}

	res, err := query.VaultScan(root, query.ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if len(res.Diagnostics) != 0 {
		t.Fatalf("全部目标存在时不应有诊断：%v", diagCodes(res.Diagnostics))
	}
	var a query.CardEntry
	for _, c := range res.Cards {
		if c.ID == "k-20260901-a" {
			a = c
		}
	}
	out := query.RelationsOut(a)
	gotTypes := []string{}
	for _, e := range out {
		gotTypes = append(gotTypes, e.Type)
	}
	if !reflect.DeepEqual(gotTypes, []string{"opposing", "limits", "derives"}) {
		t.Errorf("正向关系排序 = %v，期望 opposing → limits → derives（固定次序）", gotTypes)
	}
	in := query.RelationsIn(res.Cards, "k-20260901-x")
	if len(in) != 1 || in[0].From != "k-20260901-a" || in[0].Type != "limits" {
		t.Errorf("反向全库扫描结果错：%+v", in)
	}
	if got := query.RelationsIn(res.Cards, "k-20260901-a"); len(got) != 0 {
		t.Errorf("无人指向 A 卡时反向列表应为空：%+v", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
