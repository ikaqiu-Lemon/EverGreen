package reconcile

// T-004-C1：对账域纳管观点（`domains/<d>/opinions/o-*.md`）的**先红**合同。
//
// 本批只钉三件事，其余一格不动：
//  1. 扫描底座（internal/query 的 VaultScan）把观点当成第三类落盘对象带出来，
//     于是 reconcile 的 Input.Scan **天然**承载 `o-*`，本包仍不另写扫描器；
//  2. R3（关系异常）判定面覆盖观点持有的 `relations[]`——`o-*` 指向不存在的 `k-*`
//     必须明确产 E13，指向非知识卡 ID 必须明确产 E14，两者互斥；
//  3. R4（结构完整性）判定面覆盖观点——同 ID 多文件产 E11、观点 frontmatter 的引用
//     承载字段悬空产 E12；合法观点参与判定后**零误报**，且「只被观点引用的知识卡」
//     不再被误判成零关系孤儿。
//
// 本批**不做**（因此本文件一条断言都不碰）：R2 的 `reviewed_at` 补写面、R6 的综述失准面、
// `eg check` / `eg reconcile` 命令层采样、索引侧 kind 列、观点的 validation 生命周期。
// R1–R7 的注册项数与顺序、check 表基数、孤儿封闭三子类型一律不变（末尾自守用例逐条钉住）。
//
// 除第一支用例（要证明扫描底座真的读盘）外，全部用例只用**内存构造**的 query.ScanResult
// 快照：检查器是纯函数，这正是收益。

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 用例里用到的合法观点 ID（`o-` + 8 位 yyyymmdd + 非空 slug，逐字满足 model 的既有规则）。
const (
	oA = "o-20260916-alpha"
	oB = "o-20260916-beta"
	// oGone 形态合法但库内不存在（观点端点的 E13 判定面，与 kGone 对称）。
	oGone = "o-20260916-gone"
)

// opOpinion 构造一条观点条目（路径由 ID 派生，保证扫描序稳定可复算）。
func opOpinion(id string, rels ...model.Relation) query.OpinionEntry {
	return opOpinionAt(id, "domains/ai/opinions/"+id+".md", rels...)
}

// opOpinionAt 构造一条落在指定路径上的观点条目（同 ID 多文件用例需要它）。
func opOpinionAt(id, path string, rels ...model.Relation) query.OpinionEntry {
	return query.OpinionEntry{ID: id, Path: path, Domain: "ai", Relations: rels}
}

// opWithSourceRef 给观点挂一条 sources[] 引用（原文端 + 来源笔记端各一个逐字原值）。
func opWithSourceRef(o query.OpinionEntry, source, noteID string) query.OpinionEntry {
	o.Sources = append(o.Sources, model.SourceRef{
		Source: model.SourceID(source), Note: model.NoteID(noteID),
		Rel: model.MaterialSupport, Reason: "单测事实",
	})
	return o
}

// opWithReplacedBy 给观点设置 replaced_by.target（失效观点的替代指针，指向端点：知识卡或观点）。
func opWithReplacedBy(o query.OpinionEntry, target string) query.OpinionEntry {
	o.ReplacedByTarget = target
	return o
}

// opValidated 把观点标成 validation=validated（A-62 · W29 判定面只看 validated 观点）。
func opValidated(o query.OpinionEntry) query.OpinionEntry {
	o.Validation = string(model.ValidationValidated)
	return o
}

// opWithValidation 把观点标成任意 validation 取值（W29 的 pending / rejected 边界用）。
func opWithValidation(o query.OpinionEntry, v model.Validation) query.OpinionEntry {
	o.Validation = string(v)
	return o
}

// opDeleted 把观点标成逻辑删除（W29 不判已删除观点；也用作被删除的 supporter 端点）。
func opDeleted(o query.OpinionEntry) query.OpinionEntry {
	o.Deleted = true
	return o
}

// opDeprecated 把观点标成失效（deprecated 仍是有效 supporter，W29 判定面须照旧计入）。
func opDeprecated(o query.OpinionEntry) query.OpinionEntry {
	o.Deprecated = true
	return o
}

// opScanOf 把卡 / 笔记 / 观点折成扫描快照（计数守恒照 query 侧口径填，本包不消费它）。
func opScanOf(cards []query.CardEntry, notes []query.NoteEntry,
	opinions []query.OpinionEntry) *query.ScanResult {
	return &query.ScanResult{Cards: cards, Notes: notes, Opinions: opinions,
		ScannedFiles: len(cards) + len(notes) + len(opinions)}
}

// opWrite 在临时目录里落一个文件（含所需目录链），返回 vault 根。
func opVault(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("写文件失败：%v", err)
		}
	}
	return root
}

// opFileBytes 造一份形态像真实观点文件的字节（frontmatter 五键 + 两个分区）。
func opFileBytes(id, relTarget, replacedBy string) string {
	return "---\nid: " + id + "\nstatus: active\nvalidation: pending\n" +
		"created_at: '2026-09-16'\nupdated_at: '2026-09-16T10:00:00Z'\n" +
		"sources:\n  - source: s-20260916-paper\n    note: n-20260916-read\n" +
		"    rel: support\n    reason: 单测事实\n" +
		"relations:\n  - type: supports\n    target: " + relTarget +
		"\n    reason: 单测事实\n" +
		"replaced_by:\n  target: " + replacedBy + "\n  reason: 单测事实\n---\n\n" +
		"## 观点\n\n主张一句。\n\n## 论据与推理\n\n占位。\n"
}

// opCardBytes 造一份最小知识卡字节。
func opCardBytes(id string) string {
	return "---\nid: " + id + "\nstatus: active\ncreated_at: '2026-09-01'\n" +
		"sources: []\nrelations: []\n---\n\n## 知识内容\n\n占位。\n"
}

// TestOpinionScanBaseCarriesOpinionsIntoReconcile：扫描底座把 `domains/*/opinions/o-*.md`
// 当成第三类落盘对象带出来，于是 Input.Scan 天然承载观点、R4 的对象索引能看见它。
//
// 这是本文件**唯一**读盘的用例：要证明的正是「采样面覆盖观点目录」这件 IO 事实。
func TestOpinionScanBaseCarriesOpinionsIntoReconcile(t *testing.T) {
	const (
		cardID    = "k-20260901-attention"
		opinionID = "o-20260916-harness"
	)
	root := opVault(t, map[string]string{
		"domains/ai-infra/knowledge/" + cardID + ".md":   opCardBytes(cardID),
		"domains/ai-infra/opinions/" + opinionID + ".md": opFileBytes(opinionID, cardID, cardID),
	})
	res, err := query.VaultScan(root, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("VaultScan 失败：%v", err)
	}
	if len(res.Opinions) != 1 {
		t.Fatalf("扫描底座应带出恰 1 条观点，实得 %d 条：%+v", len(res.Opinions), res.Opinions)
	}
	got := res.Opinions[0]
	if got.ID != opinionID {
		t.Fatalf("观点 ID = %q，期望 %q", got.ID, opinionID)
	}
	if want := "domains/ai-infra/opinions/" + opinionID + ".md"; got.Path != want {
		t.Fatalf("观点 Path = %q，期望 %q（vault 相对路径、/ 分隔）", got.Path, want)
	}
	if got.Domain != "ai-infra" {
		t.Fatalf("观点 Domain = %q，期望 %q", got.Domain, "ai-infra")
	}
	if len(got.Relations) != 1 || string(got.Relations[0].Target) != cardID {
		t.Fatalf("观点 relations[] 未逐字带出：%+v", got.Relations)
	}
	if len(got.Sources) != 1 || string(got.Sources[0].Note) != "n-20260916-read" {
		t.Fatalf("观点 sources[] 未逐字带出：%+v", got.Sources)
	}
	if got.ReplacedByTarget != cardID {
		t.Fatalf("观点 replaced_by.target = %q，期望 %q", got.ReplacedByTarget, cardID)
	}
	// 计数守恒：观点进了扫描面，就必须进守恒等式，不得成为第三条静默路径。
	if n := len(res.Cards) + len(res.Notes) + len(res.Opinions) + res.SkippedFiles; n != res.ScannedFiles {
		t.Fatalf("计数不守恒：Cards=%d + Notes=%d + Opinions=%d + Skipped=%d != Scanned=%d",
			len(res.Cards), len(res.Notes), len(res.Opinions), res.SkippedFiles, res.ScannedFiles)
	}
	// 对账域的对象索引因此能看见观点（类别串与 R4 同源，不另定义一套）。
	x := NewStructureIndex(Input{VaultRoot: root, Scan: res})
	if !x.Has(opinionID, KindOpinion) {
		t.Fatalf("对象索引未登记观点 %s：%+v", opinionID, x.ObjectKinds[opinionID])
	}
	if x.Has(opinionID, KindCard) {
		t.Fatalf("观点被登记成知识卡：%+v", x.ObjectKinds[opinionID])
	}
}

// TestOpinionScanSkipsUnreadableOpinionLoudly：观点文件读不动 / 解析不了 / 缺 id 时
// 一律记诊断并计入 SkippedFiles——**禁止静默跳过**（与卡 / 笔记同口径）。
func TestOpinionScanSkipsUnreadableOpinionLoudly(t *testing.T) {
	root := opVault(t, map[string]string{
		"domains/ai/opinions/o-bad-yaml.md": "---\nid: o-20260916-x\nrelations: 不是数组\n---\n\n## 观点\n\n占位。\n",
		"domains/ai/opinions/o-no-id.md":    "---\nstatus: active\n---\n\n## 观点\n\n占位。\n",
	})
	res, err := query.VaultScan(root, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("VaultScan 失败：%v", err)
	}
	if len(res.Opinions) != 0 {
		t.Fatalf("两个坏观点文件都不该进结果：%+v", res.Opinions)
	}
	if res.SkippedFiles != 2 || res.ScannedFiles != 2 {
		t.Fatalf("Skipped=%d / Scanned=%d，期望各 2（一个坏文件不丢全部结果，但必须计数）",
			res.SkippedFiles, res.ScannedFiles)
	}
	// 逐文件各记一条 Q1（**不是**只报第一个坏文件），另有一条汇总诊断如实说「结果不完整」。
	var q1 []query.Diagnostic
	summary := ""
	for _, d := range res.Diagnostics {
		if d.Code == "Q1" {
			q1 = append(q1, d)
			continue
		}
		summary += d.Message
	}
	if len(q1) != 2 {
		t.Fatalf("应恰两条 Q1（逐文件点名），实得 %d 条：%+v", len(q1), res.Diagnostics)
	}
	joined := ""
	for _, d := range q1 {
		joined += d.Path + "|" + d.Message + "\n"
	}
	for _, want := range []string{"o-bad-yaml.md", "o-no-id.md", "观点"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Q1 未点名 %s：%q", want, joined)
		}
	}
	if !strings.Contains(summary, "跳过 2 个") {
		t.Fatalf("汇总诊断未如实报出跳过数（结果不完整必须显式说出）：%q", summary)
	}
}

// TestOpinionRelationTargetMissingDiagnosedByR3：`o-*` 的 relations[] 指向不存在的 `k-*`
// → 恰 1 条 E13（error），不静默通过；detail 如实称呼引用方为观点而不是知识卡。
func TestOpinionRelationTargetMissingDiagnosedByR3(t *testing.T) {
	in := Input{Scan: opScanOf(
		[]query.CardEntry{card(kA, "domains/ai/knowledge/"+kA+".md")},
		nil,
		[]query.OpinionEntry{opOpinion(oA, r3Rel(model.RelationSupports, kGone))},
	)}
	fs := r3Of(t, in)
	if got := r3Counts(fs); got != [R3SubcheckCount]int{1, 0, 0, 0} {
		t.Fatalf("四码条数 = %v，期望 [1 0 0 0]（恰 E13 一条）：%+v", got, fs)
	}
	f := r3One(t, fs, CheckRelationTargetMissing)
	want := []string{kGone, oA}
	sort.Strings(want)
	if !reflect.DeepEqual(f.Targets, want) {
		t.Fatalf("targets 期望 %v，实得 %v", want, f.Targets)
	}
	if f.Code() != CodeE13 || f.Severity != SeverityError {
		t.Fatalf("悬空关系 target 必须 error 级 + %s，实得 %s / %s", CodeE13, f.Severity, f.Code())
	}
	if !strings.Contains(f.Detail, oA) || !strings.Contains(f.Detail, kGone) {
		t.Fatalf("E13 的 detail 未含足以复算的两端：%q", f.Detail)
	}
	if !strings.Contains(f.Detail, labelOf(KindOpinion)) {
		t.Fatalf("E13 的 detail 未如实称呼引用方类别（应含 %q）：%q", labelOf(KindOpinion), f.Detail)
	}
	if strings.Contains(f.Detail, labelOf(KindCard)+" "+oA) {
		t.Fatalf("E13 的 detail 把观点说成知识卡：%q", f.Detail)
	}
	// 同一件事不许两码重复计：关系条目的 target 缺失只走 R3，不进 R4 的 E12。
	if got := pick(findingsOf(t, in), CheckDanglingRef); len(got) != 0 {
		t.Fatalf("观点关系 target 缺失不得进 E12（属 R3）：%+v", got)
	}
}

// TestOpinionRelationTargetPrefixInvalid：观点关系的 target 不是合法端点（`n-` / `s-` /
// 畸形 `o-` / 空串）→ 恰 1 条 E14，且与 E13 互斥（形态非法时不再判存在性）。
//
// 端点合同（B2c）：`o-*` 已是合法端点，因此指向观点不再是 E14；本用例只钉「非 k/o 端点」
// 与「畸形端点」两支（合法但缺失的 o-* 走 E13，见 TestOpinionRelationEndpointExistence）。
func TestOpinionRelationTargetPrefixInvalid(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"target 是笔记 ID（n- 非端点）", "n-20260916-note"},
		{"target 是原文 ID（s- 非端点）", "s-20260916-src"},
		{"target 是畸形观点端点（o- 但缺日期 / slug 段）", "o-a1"},
		{"target 为空串", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{Scan: opScanOf(nil, nil, []query.OpinionEntry{
				opOpinion(oA, r3Rel(model.RelationSupports, c.target)),
				opOpinion(oB),
			})}
			fs := r3Of(t, in)
			if got := r3Counts(fs); got != [R3SubcheckCount]int{0, 1, 0, 0} {
				t.Fatalf("四码条数 = %v，期望 [0 1 0 0]（恰 E14 一条）：%+v", got, fs)
			}
			f := r3One(t, fs, CheckRelationPrefixInvalid)
			if f.Code() != CodeE14 || f.Severity != SeverityError {
				t.Fatalf("非法 target 必须 error 级 + %s，实得 %s / %s", CodeE14, f.Severity, f.Code())
			}
			if !strings.Contains(f.Detail, oA) {
				t.Fatalf("E14 的 detail 未点名引用方：%q", f.Detail)
			}
		})
	}
}

// assertEndpointWording 是 B2c 文案回归的最小判据：关系类 finding 的**用户可见** detail
// 不得再出现只连知识卡的措辞（「目标卡」/「同一张卡」），且必须如实称呼「端点」——
// 关系端点宇宙含知识卡与观点，指向 o-* 的诊断文案不能再把持有方 / 目标说成「卡」。
func assertEndpointWording(t *testing.T, code, detail string) {
	t.Helper()
	for _, bad := range []string{"目标卡", "同一张卡"} {
		if strings.Contains(detail, bad) {
			t.Fatalf("%s detail 仍含只连知识卡的措辞 %q：%s", code, bad, detail)
		}
	}
	if !strings.Contains(detail, "端点") {
		t.Fatalf("%s detail 未如实称呼「端点」：%s", code, detail)
	}
}

// TestOpinionRelationEndpointExistence：观点关系指向**存在**的 `o-*` / `k-*` 端点零 E13/E14；
// 指向**缺失**的 `o-*` 与缺失的 `k-*` 都产 E13（观点是合法端点，缺失才是存在性问题）。
func TestOpinionRelationEndpointExistence(t *testing.T) {
	// 落盘宇宙：知识卡 kA + 观点 oB 存在；持有方是观点 oA。
	build := func(target string) Input {
		return Input{Scan: opScanOf(
			[]query.CardEntry{card(kA, "domains/ai/knowledge/"+kA+".md")}, nil,
			[]query.OpinionEntry{opOpinion(oA, r3Rel(model.RelationSupports, target)), opOpinion(oB)},
		)}
	}
	cases := []struct {
		name   string
		target string
		want   [R3SubcheckCount]int
	}{
		{"观点持有关系指向存在的观点（o-）→ 零 E13/E14", oB, [R3SubcheckCount]int{0, 0, 0, 0}},
		{"观点持有关系指向存在的知识卡（k-）→ 零 E13/E14", kA, [R3SubcheckCount]int{0, 0, 0, 0}},
		{"观点持有关系指向缺失的观点（o-）→ E13", oGone, [R3SubcheckCount]int{1, 0, 0, 0}},
		{"观点持有关系指向缺失的知识卡（k-）→ E13", kGone, [R3SubcheckCount]int{1, 0, 0, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := r3Of(t, build(c.target))
			if got := r3Counts(fs); got != c.want {
				t.Fatalf("四码条数 = %v，期望 %v：%+v", got, c.want, fs)
			}
			if c.want[0] == 1 {
				f := r3One(t, fs, CheckRelationTargetMissing)
				if f.Code() != CodeE13 || f.Severity != SeverityError {
					t.Fatalf("缺失端点必须 error 级 + %s，实得 %s / %s", CodeE13, f.Severity, f.Code())
				}
				// 文案回归（B2c）：观点持有 / 目标为 o-* 的 E13 detail 不得再用只连知识卡的
				// 措辞（「目标卡」/「同一张卡」），且必须如实称呼「端点」。
				assertEndpointWording(t, string(CodeE13), f.Detail)
			}
		})
	}
}

// TestOpinionLegalVaultProducesNoFindings：合法观点参与 R3 / R4 后**零误报**。
//
// 三格一起钉：①观点自身不被判成孤儿（封闭三子类型不含观点，本批不新增第四值）；
// ②只被观点引用的知识卡**不是**零关系孤儿（入边看落盘事实，观点的出边同样算）；
// ③观点的三类 frontmatter 引用都指向存在的对象时零 E12。
func TestOpinionLegalVaultProducesNoFindings(t *testing.T) {
	sources := []SourceFact{{ID: "s-paper", Path: "sources/s-paper.md"}}
	okNote := note("n-read", "domains/ai/notes/n-read.md", "s-paper")
	// kA 自己零出边、零来自知识卡的入边：唯一入边来自观点 oA。
	scan := opScanOf(
		[]query.CardEntry{card(kA, "domains/ai/knowledge/"+kA+".md")},
		[]query.NoteEntry{okNote},
		[]query.OpinionEntry{opWithReplacedBy(
			opWithSourceRef(opOpinion(oA, r3Rel(model.RelationSupports, kA)), "s-paper", "n-read"),
			kA)},
	)
	in := Input{Scan: scan, Sources: sources}
	if fs := r3Of(t, in); len(fs) != 0 {
		t.Fatalf("合法观点不得触发任何 R3 finding：%+v", fs)
	}
	if fs := findingsOf(t, in); len(fs) != 0 {
		t.Fatalf("合法观点不得触发任何 R4 finding：%+v", fs)
	}
	// 反证入边确实来自观点（而不是「因为某处豁免了孤儿判定」才没红）。
	x := NewStructureIndex(in)
	if got := x.RelationsIn[kA]; !reflect.DeepEqual(got, []string{oA}) {
		t.Fatalf("知识卡 %s 的落盘入边 = %v，期望恰 [%s]", kA, got, oA)
	}
	if x.OutDegree(oA) != 1 {
		t.Fatalf("观点 %s 的落盘出边 = %d，期望 1", oA, x.OutDegree(oA))
	}
	// 观点不进孤儿判定面：封闭子类型基数一格未动。
	if OrphanSubtypeCount != 3 {
		t.Fatalf("孤儿子类型基数 = %d，本批不得改（应恰 3）", OrphanSubtypeCount)
	}
	// 零关系、零引用的观点单独成库时同样零 finding（Sources 传 nil = 原文分区未采样，
	// 免得把「原文没有派生笔记」这件与观点无关的事混进本断言）。
	lonely := Input{Scan: opScanOf(nil, nil, []query.OpinionEntry{opOpinion(oA)})}
	if got := findingsOf(t, lonely); len(got) != 0 {
		t.Fatalf("零关系的观点不在封闭三子类型内，不得报任何 R4 finding：%+v", got)
	}
}

// TestOpinionDanglingReferenceFields：观点 frontmatter 的引用承载字段悬空 → 逐字段恰 1 条 E12。
//
// 三类：`opinion.sources[].note→材料笔记`、`opinion.sources[].source→原文`、
// `replaced_by.target→端点（知识卡或观点）`（末者与知识卡共用同一类，字段键与语义逐字相同，
// 目标端是论证关系端点宇宙：知识卡 ∪ 观点）。
func TestOpinionDanglingReferenceFields(t *testing.T) {
	sources := []SourceFact{{ID: "s-paper", Path: "sources/s-paper.md"}}
	okNote := note("n-read", "domains/ai/notes/n-read.md", "s-paper")
	okCard := card(kA, "domains/ai/knowledge/"+kA+".md")

	cases := []struct {
		name             string
		underTest        query.OpinionEntry
		wantFrom, wantTo string
	}{
		{
			name:      "opinion.sources[].note→材料笔记 悬空",
			underTest: opWithSourceRef(opOpinion(oA), "s-paper", "n-gone"),
			wantFrom:  oA, wantTo: "n-gone",
		},
		{
			name:      "opinion.sources[].source→原文 悬空",
			underTest: opWithSourceRef(opOpinion(oA), "s-gone", "n-read"),
			wantFrom:  oA, wantTo: "s-gone",
		},
		{
			name:      "观点的 replaced_by.target→端点（知识卡或观点） 悬空",
			underTest: opWithReplacedBy(opOpinion(oA), kGone),
			wantFrom:  oA, wantTo: kGone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{
				Scan: opScanOf([]query.CardEntry{okCard}, []query.NoteEntry{okNote},
					[]query.OpinionEntry{c.underTest}),
				Sources: sources,
			}
			refs := pick(findingsOf(t, in), CheckDanglingRef)
			if len(refs) != 1 {
				t.Fatalf("字段 %q 悬空应恰 1 条 E12，实得 %d 条：%+v", c.name, len(refs), refs)
			}
			f := refs[0]
			if f.Code() != CodeE12 || f.Severity != SeverityError {
				t.Fatalf("悬空引用必须 error 级 + %s，实得 %s / %s", CodeE12, f.Severity, f.Code())
			}
			want := []string{c.wantFrom, c.wantTo}
			sort.Strings(want)
			if !reflect.DeepEqual(f.Targets, want) {
				t.Fatalf("targets 期望 %v，实得 %v", want, f.Targets)
			}
		})
	}

	// 三类字段的目标都存在 → 零 E12。
	clean := Input{
		Scan: opScanOf([]query.CardEntry{okCard}, []query.NoteEntry{okNote},
			[]query.OpinionEntry{opWithReplacedBy(
				opWithSourceRef(opOpinion(oA), "s-paper", "n-read"), kA)}),
		Sources: sources,
	}
	if got := pick(findingsOf(t, clean), CheckDanglingRef); len(got) != 0 {
		t.Fatalf("观点三类引用目标都存在时不得报 E12：%+v", got)
	}

	// `sources/` 分区未采样时，指向原文的那一类绝不误报「不存在」。
	unsampled := Input{Scan: opScanOf(nil, []query.NoteEntry{okNote},
		[]query.OpinionEntry{opWithSourceRef(opOpinion(oA), "s-gone", "n-read")})}
	if got := pick(findingsOf(t, unsampled), CheckDanglingRef); len(got) != 0 {
		t.Fatalf("原文分区未采样时不得判观点的原文端悬空：%+v", got)
	}

	// 覆盖面基数：观点的两类引用字段接入后恰 6 类（多一类 / 少一类都要改这里）。
	if DanglingRefKindCount != 6 {
		t.Fatalf("dangling_ref 覆盖面应恰 6 类，DanglingRefKindCount = %d", DanglingRefKindCount)
	}
}

// TestOpinionHeldOpposingNormalizedAcrossKinds：观点持有的 `opposing` 同样按 A-24 的完整 ID
// 字典序规范化（端点合同 B2c）——观点不再豁免方向对称性检查。
//
// 'k' < 'o'，故 kA < oA：规范写入端是 kA。因此：
//   - 观点 oA 反对卡 kA（落在较大端 → 非规范方向）单条 → W15；
//   - 卡 kA 反对观点 oA（规范方向）单条 → 零 finding；
//   - 两个方向各一条 → W16（无 W15）；
//   - 观点侧同一条非规范边写两遍：既非规范方向又重复 → W15 + W16 各 1 条（两件独立事实）。
func TestOpinionHeldOpposingNormalizedAcrossKinds(t *testing.T) {
	kaPath := "domains/ai/knowledge/" + kA + ".md"
	// ① 非规范方向单条（观点持有较大端 oA → 较小端 kA）→ 恰 1 条 W15，targets 规范化为 [kA, oA]。
	rev := Input{Scan: opScanOf(
		[]query.CardEntry{card(kA, kaPath)}, nil,
		[]query.OpinionEntry{opOpinion(oA, r3Rel(model.RelationOpposing, kA))})}
	fsRev := r3Of(t, rev)
	if got := r3Counts(fsRev); got != [R3SubcheckCount]int{0, 0, 1, 0} {
		t.Fatalf("观点持有非规范方向 opposing 应恰 1 条 W15，四码条数 = %v：%+v", got, fsRev)
	}
	w15 := r3One(t, fsRev, CheckRelationOpposingAsymmetric)
	if w15.Code() != CodeW15 || w15.Severity != SeverityWarning {
		t.Fatalf("W15 必须 warning 级 + %s，实得 %s / %s", CodeW15, w15.Severity, w15.Code())
	}
	if want := []string{kA, oA}; !reflect.DeepEqual(w15.Targets, want) {
		t.Fatalf("W15 的 targets = %v，期望规范化后的 %v", w15.Targets, want)
	}
	// ② 规范方向单条（知识卡持有较小端 kA → 较大端 oA）→ 零 finding。
	canon := Input{Scan: opScanOf(
		[]query.CardEntry{r3Card(kA, r3Rel(model.RelationOpposing, oA))}, nil,
		[]query.OpinionEntry{opOpinion(oA)})}
	if got := r3Counts(r3Of(t, canon)); got != [R3SubcheckCount]int{0, 0, 0, 0} {
		t.Fatalf("规范方向的合法 opposing 不得产任何 R3 finding，四码条数 = %v", got)
	}
	// ③ 两个方向各一条 → W16（无 W15）。
	both := Input{Scan: opScanOf(
		[]query.CardEntry{r3Card(kA, r3Rel(model.RelationOpposing, oA))}, nil,
		[]query.OpinionEntry{opOpinion(oA, r3Rel(model.RelationOpposing, kA))})}
	fsBoth := r3Of(t, both)
	if got := r3Counts(fsBoth); got != [R3SubcheckCount]int{0, 0, 0, 1} {
		t.Fatalf("两个方向各一条应恰 1 条 W16（无 W15），四码条数 = %v：%+v", got, fsBoth)
	}
	w16 := r3One(t, fsBoth, CheckRelationDuplicate)
	if w16.Code() != CodeW16 || w16.Severity != SeverityWarning {
		t.Fatalf("W16 必须 warning 级 + %s，实得 %s / %s", CodeW16, w16.Severity, w16.Code())
	}
	if want := []string{kA, oA}; !reflect.DeepEqual(w16.Targets, want) {
		t.Fatalf("W16 的 targets = %v，期望 %v", w16.Targets, want)
	}
	// ④ 观点侧同一条非规范边写两遍：既非规范方向又重复 → W15 + W16 各 1 条。
	dupRev := Input{Scan: opScanOf(
		[]query.CardEntry{card(kA, kaPath)}, nil,
		[]query.OpinionEntry{opOpinion(oA,
			r3Rel(model.RelationOpposing, kA), r3Rel(model.RelationOpposing, kA))})}
	if got := r3Counts(r3Of(t, dupRev)); got != [R3SubcheckCount]int{0, 0, 1, 1} {
		t.Fatalf("非规范方向重复应各产 1 条 W15 与 W16，四码条数 = %v", got)
	}
}

// TestOpinionOpposingCrossKindNormalized：观点↔观点（o↔o）的 `opposing` 同样按 A-24 的
// 完整 ID 字典序规范化 —— 端点合同 B2c 把规范化 / 判重从 k↔k 扩到 k↔o / o↔k / o↔o。
//
// oA < oB（字典序），故 oA 是这对边的唯一规范写入端。四种落盘形态各钉一次：
//   - oA → oB 单条（规范方向）        → 零 finding；
//   - oB → oA 单条（非规范方向）      → 恰 1 条 W15；
//   - 两个方向各一条                   → 恰 1 条 W16（无 W15）；
//   - 同一观点文件里同方向写两遍       → 恰 1 条 W16。
func TestOpinionOpposingCrossKindNormalized(t *testing.T) {
	if !(oA < oB) {
		t.Fatalf("用例前提不成立：期望 %s 字典序小于 %s", oA, oB)
	}
	// ① 规范方向单条 → 零 finding。
	canon := Input{Scan: opScanOf(nil, nil, []query.OpinionEntry{
		opOpinion(oA, r3Rel(model.RelationOpposing, oB)), opOpinion(oB)})}
	if got := r3Counts(r3Of(t, canon)); got != [R3SubcheckCount]int{0, 0, 0, 0} {
		t.Fatalf("o↔o 规范方向合法 opposing 不得产任何 R3 finding，四码条数 = %v", got)
	}
	// ② 非规范方向单条（较大端 oB 持有）→ 恰 1 条 W15，targets 规范化为 [oA, oB]。
	rev := Input{Scan: opScanOf(nil, nil, []query.OpinionEntry{
		opOpinion(oA), opOpinion(oB, r3Rel(model.RelationOpposing, oA))})}
	fsRev := r3Of(t, rev)
	if got := r3Counts(fsRev); got != [R3SubcheckCount]int{0, 0, 1, 0} {
		t.Fatalf("o↔o 非规范方向应恰 1 条 W15，四码条数 = %v：%+v", got, fsRev)
	}
	w15 := r3One(t, fsRev, CheckRelationOpposingAsymmetric)
	if w15.Code() != CodeW15 || w15.Severity != SeverityWarning {
		t.Fatalf("W15 必须 warning 级 + %s，实得 %s / %s", CodeW15, w15.Severity, w15.Code())
	}
	if want := []string{oA, oB}; !reflect.DeepEqual(w15.Targets, want) {
		t.Fatalf("W15 的 targets = %v，期望规范化后的 %v", w15.Targets, want)
	}
	// ③ 两个方向各一条 → 恰 1 条 W16（无 W15）。
	both := Input{Scan: opScanOf(nil, nil, []query.OpinionEntry{
		opOpinion(oA, r3Rel(model.RelationOpposing, oB)),
		opOpinion(oB, r3Rel(model.RelationOpposing, oA))})}
	fsBoth := r3Of(t, both)
	if got := r3Counts(fsBoth); got != [R3SubcheckCount]int{0, 0, 0, 1} {
		t.Fatalf("o↔o 两个方向各一条应恰 1 条 W16（无 W15），四码条数 = %v：%+v", got, fsBoth)
	}
	w16 := r3One(t, fsBoth, CheckRelationDuplicate)
	if want := []string{oA, oB}; !reflect.DeepEqual(w16.Targets, want) {
		t.Fatalf("W16 的 targets = %v，期望 %v", w16.Targets, want)
	}
	// ④ 同一观点文件同方向写两遍 → 恰 1 条 W16。
	dup := Input{Scan: opScanOf(nil, nil, []query.OpinionEntry{
		opOpinion(oA, r3Rel(model.RelationOpposing, oB), r3Rel(model.RelationOpposing, oB)),
		opOpinion(oB)})}
	fsDup := r3Of(t, dup)
	if got := r3Counts(fsDup); got != [R3SubcheckCount]int{0, 0, 0, 1} {
		t.Fatalf("o↔o 同文件同方向重复应恰 1 条 W16，四码条数 = %v：%+v", got, fsDup)
	}
	// 文案回归（B2c）：目标为 o-* 的 W16（同一持有端点 relations[] 内重复）detail 不得再用
	// 只连知识卡的措辞（「目标卡」/「同一张卡」），且必须如实称呼「端点」。
	w16dup := r3One(t, fsDup, CheckRelationDuplicate)
	assertEndpointWording(t, string(CodeW16), w16dup.Detail)
}

// TestOpinionDuplicateIDReportedOnce：同一个 `o-*` 落在两个文件 → 恰 1 条 E11，
// targets 列出全部冲突文件，detail 如实写出对象类别。
func TestOpinionDuplicateIDReportedOnce(t *testing.T) {
	in := Input{Scan: opScanOf(nil, nil, []query.OpinionEntry{
		opOpinionAt(oA, "domains/ai/opinions/"+oA+".md"),
		opOpinionAt(oA, "domains/infra/opinions/"+oA+".md"),
	})}
	dups := pick(findingsOf(t, in), CheckDuplicateID)
	if len(dups) != 1 {
		t.Fatalf("同 ID 两文件应恰 1 条 E11，实得 %d 条：%+v", len(dups), dups)
	}
	f := dups[0]
	want := []string{"domains/ai/opinions/" + oA + ".md", "domains/infra/opinions/" + oA + ".md"}
	if !reflect.DeepEqual(f.Targets, want) {
		t.Fatalf("targets 期望 %v，实得 %v", want, f.Targets)
	}
	if !strings.Contains(f.Detail, labelOf(KindOpinion)) {
		t.Fatalf("E11 的 detail 未如实写出对象类别（应含 %q）：%q", labelOf(KindOpinion), f.Detail)
	}
}

// TestOpinionReconcileBoundariesUnchanged：观点判定面不新增检查器（checkers 恒 7）；
// A-62 后 check 表基数与 R3 子检查数抬到 13 / 5（新增 R3·W29 opinion_unsupported_validated），
// 其余封闭基数与顺序一格不动。
func TestOpinionReconcileBoundariesUnchanged(t *testing.T) {
	if len(checkers) != 7 {
		t.Fatalf("checkers 注册项 = %d，R1–R7 全在册应恰 7（W29 并入 R3，不新增 / 重排检查项）",
			len(checkers))
	}
	if CheckCount != 13 {
		t.Fatalf("check 表基数 = %d，A-62 后应恰 13", CheckCount)
	}
	if R3SubcheckCount != 5 {
		t.Fatalf("R3 子检查数 = %d，A-62 后应恰 5", R3SubcheckCount)
	}
	if got := R3Subchecks(); !reflect.DeepEqual(got, []string{
		CheckRelationTargetMissing, CheckRelationPrefixInvalid,
		CheckRelationOpposingAsymmetric, CheckRelationDuplicate,
		CheckOpinionUnsupportedValidated,
	}) {
		t.Fatalf("R3 子检查顺序被改动：%v", got)
	}
	if got := OrphanSubtypes(); len(got) != OrphanSubtypeCount {
		t.Fatalf("孤儿子类型 = %v，基数应恰 %d", got, OrphanSubtypeCount)
	}
	if labelOf(KindOpinion) == KindOpinion {
		t.Fatalf("观点类别缺中文标签：labelOf(%q) = %q", KindOpinion, labelOf(KindOpinion))
	}
	// 观点进判定面后 R3 / R4 仍是**只报告**项：零 RepairSpec（r3Of / findingsOf 已逐条断言，
	// 这里再用注册表整体跑一次，防止「某一项绕过检查器直接产修复」）。
	in := Input{Scan: opScanOf(nil, nil,
		[]query.OpinionEntry{opOpinion(oA, r3Rel(model.RelationSupports, kGone))})}
	res := Run(in)
	if len(res.Repairs) != 0 {
		t.Fatalf("含观点的输入不得产 RepairSpec（R2 / R6 的补写面属下一批）：%+v", res.Repairs)
	}
	if len(res.Findings) == 0 {
		t.Fatalf("含观点的悬空关系必须被注册表内的检查项检出，实得零 finding")
	}
}
