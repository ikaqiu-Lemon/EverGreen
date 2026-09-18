package query_test

// T-…-033 的 query 侧机器判据（提案合同 §10.3 / §6.2 末段、§9 机器判定表两行）：
//
//	① 提案**不进知识扫描面**：eg search / card show / rel / 综述取材一律看不到它，
//	   且 scanned_files **不把 proposals/ 计入**（反证虚假计数）；
//	② eg context **只给摘要**：输出含 title 与 targets，**不含**提案正文任一 H2 的内容。

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// bodySentinel 是提案正文里的哨兵串：它一旦出现在任何查询 / 上下文输出里，
// 就说明提案正文泄漏了。
const bodySentinel = "SENTINEL-PROPOSAL-BODY-MUST-NOT-LEAK"

// proposalFixture 造一份形态合法的提案（frontmatter 8 键 + 正文恰 7 个 H2）。
// 正文每个分区都塞哨兵串，任何一处泄漏都能被抓到。
func proposalFixture(id, title string, targets []string) string {
	fm := "---\nid: " + id + "\ntype: logical_delete\nstatus: pending\n" +
		"created_at: '2026-07-01'\ntargets:\n"
	for _, tgt := range targets {
		fm += "  - '" + tgt + "'\n"
	}
	fm += "impact:\n  exits_default_view: []\n  cards_losing_support: []\n" +
		"  affected_material_rels: 0\n  affected_relations: 0\n  stale_reviews: []\n" +
		"decision:\n  result:\n  reason:\n  superseded_by:\n" +
		"execution:\n  status: 'not_started'\n  attempted_at:\n  reason:\n" +
		"  git_commit:\n  written_paths: []\n  unwritten_paths: []\n---\n"
	body := "\n# " + title + "\n"
	for _, sec := range []string{"推荐修改", "理由与证据", "影响的文件、领域与关系",
		"执行后状态", "不执行的影响", "替代方案", "可应用内容"} {
		body += "\n## " + sec + "\n\n" + bodySentinel + " 注意力机制 attention 磁盘调度\n"
	}
	return fm + body
}

// TestProposalsExcludedFromKnowledgeScan —— 提案不进知识检索。
//
// 断言四组：
//   - VaultScan 的 Cards / Notes 里零条目来自 proposals/，ID 也不出现；
//   - ScannedFiles / SkippedFiles **都不把 proposals/ 计入**，且计数守恒成立；
//   - 提案目录里放一个**不可解析**的文件也不产生任何 Q 诊断（提案不得变成 Q1 噪声）；
//   - Filter（eg search 的匹配面）与 RelationsIn / RelationsOut（eg rel 的两向面）
//     都命中 0 条提案。
func TestProposalsExcludedFromKnowledgeScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "domains/ai-infra/knowledge/k-20260901-ok.md",
		card("k-20260901-ok", "注意力机制", "ai-infra", "active",
			"2026-09-01", "2026-09-01T10:00:00+08:00", "", ""))
	writeFile(t, root, "domains/ai-infra/notes/n-20260901-x.md",
		"---\nid: n-20260901-x\nsource: s-20260901-a\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-01T10:00:00+08:00'\n---\n\n## 原文提炼\n\n- 要点\n")
	writeFile(t, root, "proposals/p-20260701-001.md",
		proposalFixture("p-20260701-001", "删除注意力机制原文", []string{"s-20260901-a"}))
	// 提案目录里的坏文件：既不进结果，也**不产生 Q1**（否则提案会变成检索面的噪声）。
	writeFile(t, root, "proposals/broken.md", "---\n- 1\n---\n\n# 坏提案\n")

	res, err := query.VaultScan(root, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}

	// ① 结果集里零提案。
	if len(res.Cards) != 1 || res.Cards[0].ID != "k-20260901-ok" {
		t.Fatalf("Cards = %d 条，期望恰 1 条 k-20260901-ok", len(res.Cards))
	}
	if len(res.Notes) != 1 {
		t.Fatalf("Notes = %d 条，期望恰 1 条", len(res.Notes))
	}
	for _, c := range res.Cards {
		if query.IsProposalPath(c.Path) || strings.HasPrefix(c.ID, "p-") {
			t.Fatalf("知识扫描面出现提案：%s（%s）", c.ID, c.Path)
		}
	}
	for _, n := range res.Notes {
		if query.IsProposalPath(n.Path) {
			t.Fatalf("知识扫描面出现提案：%s", n.Path)
		}
	}

	// ② 计数：proposals/ 两个文件都不计入，且守恒式成立（没有第三条静默路径）。
	if res.ScannedFiles != 2 {
		t.Fatalf("ScannedFiles = %d，期望 2（proposals/ 的 2 个文件都不得计入）", res.ScannedFiles)
	}
	if res.SkippedFiles != 0 {
		t.Fatalf("SkippedFiles = %d，期望 0（提案不进扫描面，也就无从被跳过）", res.SkippedFiles)
	}
	if res.ScannedFiles != len(res.Cards)+len(res.Notes)+res.SkippedFiles {
		t.Fatalf("计数不守恒：scanned=%d cards=%d notes=%d skipped=%d",
			res.ScannedFiles, len(res.Cards), len(res.Notes), res.SkippedFiles)
	}

	// ③ 提案不产生任何 Q 诊断（含 Q3 汇总项）。
	if len(res.Diagnostics) != 0 {
		t.Fatalf("提案目录不得产生诊断，实得 %v", diagCodes(res.Diagnostics))
	}
	if res.HasQ() {
		t.Fatal("提案目录不得让结果被判为不完整")
	}

	// ④ eg search 的匹配面：提案正文里的词一个都不能把提案带进结果。
	for _, q := range []string{bodySentinel, "删除注意力机制原文", "p-20260701-001"} {
		for _, hit := range query.Filter(res.Cards, query.FilterSpec{Query: q}) {
			if query.IsProposalPath(hit.Path) {
				t.Fatalf("search 关键词 %q 命中提案 %s", q, hit.Path)
			}
		}
	}
	// eg rel 的两向面：提案既不作 from 也不作 target。
	for _, e := range query.RelationsOut(res.Cards[0]) {
		if strings.HasPrefix(e.From, "p-") || strings.HasPrefix(e.Target, "p-") {
			t.Fatalf("rel 正向出现提案：%+v", e)
		}
	}
	if edges := query.RelationsIn(res.Cards, "p-20260701-001"); len(edges) != 0 {
		t.Fatalf("rel 反向查提案 ID 必须 0 条，实得 %+v", edges)
	}

	// 受限扫描面（综述取材按领域取）同样看不到提案。
	limited, err := query.VaultScan(root, query.ScanOptions{
		Domains: []string{"ai-infra"}, IncludeNotes: true,
	})
	if err != nil {
		t.Fatalf("VaultScan(受限)：%v", err)
	}
	for _, c := range limited.Cards {
		if query.IsProposalPath(c.Path) {
			t.Fatalf("受限扫描面出现提案：%s", c.Path)
		}
	}
	if limited.ScannedFiles != 2 {
		t.Fatalf("受限扫描 ScannedFiles = %d，期望 2", limited.ScannedFiles)
	}
}

// TestContext_ProposalSummaryOnly —— eg context 只给摘要。
//
// 断言：输出含提案 title 与 targets；**不含**提案正文任一 H2 的内容；
// ProposalSummary 的字段集合恰四项（结构上没有承载正文的地方）；
// 提案不进 Base、不进 Cards / Candidates；多提案时顺序确定（按路径升序）。
func TestContext_ProposalSummaryOnly(t *testing.T) {
	root := fixture(t)
	writeFile(t, root, "proposals/p-20260701-002.md",
		proposalFixture("p-20260701-002", "删除磁盘调度原文", []string{"s-20260412-demo"}))
	writeFile(t, root, "proposals/p-20260701-001.md",
		proposalFixture("p-20260701-001", "删除注意力机制原文",
			[]string{"s-20260412-demo", "n-20260412-demo"}))

	ctx := build(t, root, query.Request{Source: "s-20260412-demo"})

	// ① 摘要在：标题与 targets 都拿得到，顺序按路径升序（可复算）。
	if len(ctx.Proposals) != 2 {
		t.Fatalf("提案摘要 = %d 条，期望 2：%+v", len(ctx.Proposals), ctx.Proposals)
	}
	wantIDs := []string{"p-20260701-001", "p-20260701-002"}
	var gotIDs []string
	for _, p := range ctx.Proposals {
		gotIDs = append(gotIDs, p.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("提案顺序 = %v，期望按路径升序 %v", gotIDs, wantIDs)
	}
	first := ctx.Proposals[0]
	if first.Title != "删除注意力机制原文" {
		t.Fatalf("提案标题 = %q", first.Title)
	}
	if !reflect.DeepEqual(first.Targets, []string{"s-20260412-demo", "n-20260412-demo"}) {
		t.Fatalf("提案 targets = %v", first.Targets)
	}
	if first.Path != "proposals/p-20260701-001.md" {
		t.Fatalf("提案路径 = %q", first.Path)
	}

	// ② 正文不在：整份上下文（含 JSON 形态）里没有任何 H2 分区的内容。
	raw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("Marshal：%v", err)
	}
	blob := string(raw) + "\n" + dump(ctx)
	if strings.Contains(blob, bodySentinel) {
		t.Fatalf("提案正文泄漏进上下文：\n%s", blob)
	}
	for _, sec := range []string{"## 推荐修改", "## 理由与证据", "## 影响的文件、领域与关系",
		"## 执行后状态", "## 不执行的影响", "## 替代方案", "## 可应用内容"} {
		if strings.Contains(blob, sec) {
			t.Fatalf("提案正文分区 %q 泄漏进上下文", sec)
		}
	}

	// ③ 结构上没有正文的落点：ProposalSummary 字段恰四项。
	rt := reflect.TypeOf(query.ProposalSummary{})
	var fields []string
	for i := 0; i < rt.NumField(); i++ {
		fields = append(fields, rt.Field(i).Name)
	}
	if !reflect.DeepEqual(fields, []string{"ID", "Path", "Title", "Targets"}) {
		t.Fatalf("ProposalSummary 字段 = %v，期望恰 ID / Path / Title / Targets", fields)
	}

	// ④ 提案不进 Base（不是本次知识加工会改的文件），也不进 Cards / Candidates。
	for p := range ctx.Base {
		if query.IsProposalPath(p) {
			t.Fatalf("提案不得进 base：%s", p)
		}
	}
	for _, c := range ctx.Cards {
		if strings.HasPrefix(c.ID, "p-") || query.IsProposalPath(c.Path) {
			t.Fatalf("提案不得进同领域卡列表：%+v", c)
		}
	}
	for _, c := range ctx.Candidates {
		if strings.HasPrefix(c.ID, "p-") || query.IsProposalPath(c.Path) {
			t.Fatalf("提案不得进候选相似卡：%+v", c)
		}
	}
	// ⑤ 提案目录缺席时不是错误（合同 §7.1：S1 可以不存在），摘要为空数组而非 nil。
	bare := fixture(t)
	bctx := build(t, bare, query.Request{Source: "s-20260412-demo"})
	if bctx.Proposals == nil || len(bctx.Proposals) != 0 {
		t.Fatalf("无 proposals/ 时摘要必须是空数组，实得 %#v", bctx.Proposals)
	}
	// 缺提案目录不是「结果不完整」：**不产生任何 Q 系列诊断**。唯一应在的诊断是 D-3 的
	// candidates 弃用提示 I1（info，query.Context.Diagnostics 无条件产出恰一条）。
	if got := diagsByCode(bctx, query.CodeI1); len(got) != 1 || got[0].Level != query.DiagLevelInfo {
		t.Fatalf("无 proposals/ 时应恰有一条 I1 info，实得 %v", diagCodeSeq(bctx))
	}
	for _, code := range diagCodeSeq(bctx) {
		if code != query.CodeI1 {
			t.Fatalf("无 proposals/ 时除 I1 外不得产生任何诊断（尤其无 Q 系列），实得 %v", diagCodeSeq(bctx))
		}
	}
}

// TestProposalSummaryHonestDiagnostics —— 摘要面同样**不静默跳过**：
// 读不动 / 缺 id 的提案各记一条 Q1，并补恰一条 Q3 汇总项；合法提案照常返回。
func TestProposalSummaryHonestDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "proposals/p-20260701-001.md",
		proposalFixture("p-20260701-001", "好提案", []string{"s-1"}))
	writeFile(t, root, "proposals/broken.md", "---\n- 1\n---\n\n# 坏提案\n")
	writeFile(t, root, "proposals/no-id.md",
		"---\ntype: logical_delete\n---\n\n# 无 id 提案\n")

	sums, diags, err := query.ProposalSummaries(root)
	if err != nil {
		t.Fatalf("ProposalSummaries：%v", err)
	}
	if len(sums) != 1 || sums[0].ID != "p-20260701-001" {
		t.Fatalf("摘要 = %+v，期望恰 1 条好提案", sums)
	}
	if n := countCode(diags, query.CodeQ1); n != 2 {
		t.Fatalf("Q1 = %d 条，期望 2（不可解析 + 缺 id）：%v", n, diagCodes(diags))
	}
	for _, d := range diags {
		if !strings.HasPrefix(d.Path, "proposals/") {
			t.Fatalf("诊断必须带提案路径：%+v", d)
		}
		if d.Level != query.DiagLevel {
			t.Fatalf("Q 系列一律 %s：%+v", query.DiagLevel, d)
		}
	}
}
