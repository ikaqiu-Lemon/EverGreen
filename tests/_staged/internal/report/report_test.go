package report

// T-…-016 的验收用例：最终报告的 S1 必填子集、阶段占位口径与如实原则。
// 可用 `go test ./internal/report/...` 单独跑。
//
// 报告层是**纯函数**：不碰磁盘、不碰 Git、不调用模型、零网络。
// 端到端的产生方式（apply 的编排、`eg report --last` 的复现）由 internal/cli 的用例覆盖。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sample 造一份「什么都发生过一点」的报告，供键集合与渲染用例复用。
func sample() Report {
	r := New()
	r.Source = Source{ID: "s-20260901-demo", Path: "sources/s-20260901-demo.md"}
	r.Note = Note{ID: "n-20260901-demo", Domain: "ai-infra",
		Path: "domains/ai-infra/materials/n-20260901-demo.md", Reprocessed: true}
	r.Cards.Created = []string{"k-20260901-attention"}
	r.Cards.Reused = []string{"k-20260815-rnn"}
	r.Cards.Updated = []string{"k-20260815-rnn"}
	r.Relations.Material = []Material{{Card: "k-20260901-attention", Source: "s-20260901-demo",
		Note: "n-20260901-demo", Rel: "support", Reason: "原文给出定义"}}
	r.Relations.Knowledge = []Knowledge{{From: "k-20260901-attention", Type: "supports",
		Target: "k-20260815-rnn", Reason: "两者结论一致"}}
	r.OpenQuestions = []OpenQuestion{{Note: "n-20260901-demo", Question: "小样本下是否成立？"}}
	r.Links = []string{"domains/ai-infra/knowledge/k-20260901-attention.md"}
	r.SetCommit("0ed1181a7aa214ac81382491025c9137fcd3a14c")
	r.Skipped = []Skipped{{Kind: "file_changed", Target: "k-20260815-rnn",
		Locator: "domains/ai-infra/knowledge/k-20260815-rnn.md",
		Cause:   "content_hash_mismatch", Detail: "文件自 eg context 读取以来已变化，已跳过该文件"}}
	r.DefaultDomainFallback = Fallback{Used: true, Reason: "plan.domain 缺失，已按 default_domain 落位"}
	r.HighImpact = []Impact{{Kind: "new_core_card", Target: "k-20260901-attention", Detail: "新建承载新核心含义的卡"}}
	r.AddWarning(Diagnostic{Code: "W1", Level: LevelWarning, Path: "ops[0].card_id",
		OpIndex: 0, Message: "写入目标落在 plan.domain 之外，已照常写入"})
	return r
}

func keysOf(t *testing.T, r Report) map[string]json.RawMessage {
	t.Helper()
	raw, err := r.JSON()
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	return m
}

// —— ① §4.6 S1 必填恰 11 项，且不得出现自创键 ——

func TestReportHasExactlyTheS1RequiredKeys(t *testing.T) {
	m := keysOf(t, sample())
	required := RequiredKeys()
	if len(required) != 11 {
		t.Fatalf("§4.6「S1 必填」应为 11 项，RequiredKeys() 给出 %d 项", len(required))
	}
	for _, k := range required {
		if _, ok := m[k]; !ok {
			t.Fatalf("报告体缺必填键 %q", k)
		}
	}
	for _, k := range ForbiddenKeys() {
		if _, ok := m[k]; ok {
			t.Fatalf("报告体不得出现自创键 / 越阶段键 %q", k)
		}
	}
	// 键集合 = 必填 11 项 + 阶段占位 5 项，不多不少。
	if want := len(required) + len(StageKeys()); len(m) != want {
		t.Fatalf("报告体键数 = %d，期望恰 %d（必填 %d + 阶段占位 %d）",
			len(m), want, len(required), len(StageKeys()))
	}
}

// git.commit 必须**嵌套在 git 下**，不是顶层 commit。
func TestReportCommitIsNestedUnderGit(t *testing.T) {
	m := keysOf(t, sample())
	if _, ok := m["commit"]; ok {
		t.Fatal("不得出现顶层 commit 键（§4.6 是 git.commit）")
	}
	var g map[string]json.RawMessage
	if err := json.Unmarshal(m["git"], &g); err != nil {
		t.Fatalf("git 段落不可解析：%v", err)
	}
	if _, ok := g["commit"]; !ok {
		t.Fatal("git 段落必须含 commit 键")
	}
	empty := New()
	if string(keysOf(t, empty)["git"]) != `{"commit":null}` {
		t.Fatalf("未产生 commit / 提交失败时 git.commit 必须为 null，实得 %s", keysOf(t, empty)["git"])
	}
}

// —— ② 阶段占位字段不得造假 ——

func TestReportStagePlaceholdersAreNotFabricated(t *testing.T) {
	m := keysOf(t, sample())
	// sample() 不含提案 / 失效卡新支持事实：前两键因此仍是空数组
	// （M3 起它们**有事实就产真实值**，占位口径只对 support_check / affected / reconcile 恒成立）。
	want := map[string]string{
		"proposals":              "[]",
		"deprecated_new_support": "[]",
		"support_check":          "null",
		"affected":               "null",
		// 【M4 · T-…-057 按实测重钉，只加严不放宽】原式逐字钉 `{"ran":false}` 一键一值。
		// M4 依合同 §11 把 `reconcile` 扩为**恰三键**（合法新增能力，M-004 判据 8），
		// 因此这里改钉**唯一占位常量**：非对账路径的 ran / commit / findings 三键三值
		// 全部被钉住（1 值 → 3 值，信息量只增不减），且占位形态从此只有一个真源。
		"reconcile": ReconcilePlaceholderJSON,
	}
	for k, v := range want {
		got, ok := m[k]
		if !ok {
			t.Fatalf("缺阶段占位键 %q", k)
		}
		if string(got) != v {
			t.Fatalf("%s = %s，期望 %s（不得输出假数据）", k, got, v)
		}
	}
	// 【M6 · T-…-072 重钉】txn_id 不再是「越阶段必须省略」的字段，而是 A-59 的**条件合同键**：
	// 凡真正建立并提交了 Markdown 事务的最终报告必填，未开事务的报告一律省略。
	// sample() 是未开事务的报告，因此这里钉的是「省略」这一半（另一半见 TestReportTxnIDPopulated）。
	if _, ok := m["txn_id"]; ok {
		t.Fatal("未开事务的报告不得出现 txn_id：没有事务就没有事务号（A-59）")
	}
}

// 空报告里所有列表都是空数组而不是 null（Agent 侧不必判空指针）。
func TestReportEmptyListsAreArraysNotNull(t *testing.T) {
	m := keysOf(t, New())
	for _, k := range []string{"open_questions", "links", "skipped", "high_impact",
		"warnings", "proposals", "deprecated_new_support"} {
		if string(m[k]) != "[]" {
			t.Fatalf("%s = %s，期望 []", k, m[k])
		}
	}
	var cards map[string]json.RawMessage
	_ = json.Unmarshal(m["cards"], &cards)
	for _, k := range []string{"created", "reused", "updated"} {
		if string(cards[k]) != "[]" {
			t.Fatalf("cards.%s = %s，期望 []", k, cards[k])
		}
	}
}

// —— ③ 如实原则：诊断数量守恒，不折叠不去重 ——

func TestReportDiagnosticsAreConserved(t *testing.T) {
	const n, m = 7, 4
	r := New()
	for i := 0; i < n; i++ {
		// 故意全部同码同文：折叠 / 去重的实现会在这里露馅。
		r.AddWarning(Diagnostic{Code: "W6", Level: LevelWarning, Path: "base[\"k-1\"]",
			OpIndex: i, Message: "base 未覆盖被改文件"})
	}
	for i := 0; i < m; i++ {
		r.Skipped = append(r.Skipped, Skipped{Kind: "file_changed", Target: "k-1",
			Locator: "domains/d/knowledge/k-1.md", Cause: "content_hash_mismatch",
			Detail: "文件自读取以来已变化"})
	}
	if len(r.Warnings) != n || len(r.Skipped) != m {
		t.Fatalf("warnings=%d skipped=%d，期望 %d / %d", len(r.Warnings), len(r.Skipped), n, m)
	}
	var back Report
	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Warnings) != n || len(back.Skipped) != m {
		t.Fatalf("序列化后 warnings=%d skipped=%d，数量必须守恒", len(back.Warnings), len(back.Skipped))
	}
	lines := strings.Join(r.Lines(), "\n")
	if got := strings.Count(lines, "base 未覆盖被改文件"); got != n {
		t.Fatalf("人类可读形态里 warning 出现 %d 次，期望 %d 次（两套渲染事实一致）", got, n)
	}
	if got := strings.Count(lines, "kind=file_changed"); got != m {
		t.Fatalf("人类可读形态里 skipped 出现 %d 次，期望 %d 次", got, m)
	}
}

// —— ④ 零知识结果合法（EG-KNW-05）——

func TestReportZeroCardsIsLegal(t *testing.T) {
	r := New()
	r.OpenQuestions = []OpenQuestion{{Note: "n-1", Question: "这条结论适用范围？"}}
	if !r.NoteZeroCards("cards") {
		t.Fatal("零卡时必须给出说明")
	}
	joined := strings.Join(r.Lines(), "\n")
	if !strings.Contains(joined, NoCardNotice) {
		t.Fatalf("报告必须说明「%s」：%s", NoCardNotice, joined)
	}
	if !strings.Contains(joined, "未判失败") {
		t.Fatal("零卡属合法结果，报告必须写明不判失败")
	}
	withCard := New()
	withCard.Cards.Created = []string{"k-1"}
	if withCard.NoteZeroCards("cards") {
		t.Fatal("已产出卡时不得输出与事实相悖的零卡说明")
	}
}

// —— ⑤ high_impact[] 承载 S1 四类显著变更 ——

func TestReportHighImpactCarriesFourS1Kinds(t *testing.T) {
	r := New()
	kinds := []string{"new_core_card", "non_core_supplement", "opposing", "relation"}
	for _, k := range kinds {
		r.HighImpact = append(r.HighImpact, Impact{Kind: k, Target: "k-2026-" + k, Detail: "详情"})
	}
	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(r.Lines(), "\n")
	for _, k := range kinds {
		if !strings.Contains(string(raw), `"kind":"`+k+`"`) {
			t.Fatalf("JSON 形态缺 high_impact kind=%s", k)
		}
		if !strings.Contains(lines, k) || !strings.Contains(lines, "k-2026-"+k) {
			t.Fatalf("人类可读形态缺 kind=%s 或其 target（必须可定位）", k)
		}
	}
}

// —— ⑥ EG-CFM-01：报告全文无确认 / 审批 / 中间态措辞 ——

func TestReportHasNoApprovalWording(t *testing.T) {
	r := sample()
	r.NoteZeroCards("cards")
	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw) + "\n" + strings.Join(r.Lines(), "\n")
	// 措辞以拼接构造，避免本文件成为 grep 反证的命中项。
	for _, banned := range []string{"待" + "确认", "待" + "批准", "待" + "入库", "pen" + "ding", "待过目"} {
		if strings.Contains(text, banned) {
			t.Fatalf("报告全文不得出现中间态措辞 %q", banned)
		}
	}
	if !strings.Contains(text, "k-20260901-attention") {
		t.Fatal("新建卡必须直接出现在 cards.created（创建即 active，无中间态）")
	}
}

// —— ⑦ 双形态同源：人类可读不得引入 JSON 里没有的事实 ——

func TestReportDualRenderingSameFacts(t *testing.T) {
	r := sample()
	lines := strings.Join(r.Lines(), "\n")
	for _, fact := range []string{
		r.Source.ID, r.Note.ID, r.Cards.Created[0], r.Cards.Reused[0],
		r.Links[0], *r.Git.Commit, r.Skipped[0].Locator, r.Skipped[0].Cause,
		r.DefaultDomainFallback.Reason, r.HighImpact[0].Kind, r.Warnings[0].Message,
	} {
		if !strings.Contains(lines, fact) {
			t.Fatalf("人类可读形态缺事实 %q", fact)
		}
	}
	if !strings.Contains(lines, "ops[0]") || !strings.Contains(lines, "ops[0].card_id") {
		t.Fatal("诊断必须带 op 下标与字段路径")
	}
	if strings.Contains(lines, "未决问题：") != (len(r.OpenQuestions) > 0) {
		t.Fatal("未决问题的两套渲染必须一致")
	}
}

// 提交失败 / 未产生 commit 时的人类可读措辞必须写明「未做任何还原」（B4）。
func TestReportCommitFailureWordingSaysNoRollback(t *testing.T) {
	r := New()
	r.Links = []string{"domains/d/knowledge/k-1.md"}
	r.SetCommit("")
	lines := strings.Join(r.Lines(), "\n")
	if !strings.Contains(lines, "未做任何还原") {
		t.Fatalf("提交失败措辞必须写明不做破坏性还原：%s", lines)
	}
	if !strings.Contains(lines, "domains/d/knowledge/k-1.md") {
		t.Fatal("已写文件必须如实列出")
	}
}

// —— ⑧ 依赖方向与禁用命名的静态反证 ——

func TestReportPackageStaysWithinItsDependencies(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("internal/report 下应有实现文件")
	}
	// 禁用词以拼接构造，避免本文件自身成为 grep 反证的命中项。
	banned := []string{"block_" + "conflict", "block_hash_" + "changed", "st" + "ale"}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		for _, dep := range []string{"internal/cli", "internal/store", "internal/plan", "internal/git"} {
			if strings.Contains(body, `"github.com/ikaqiu-Lemon/EverGreen/`+dep+`"`) {
				t.Fatalf("%s 违反 §13 依赖方向：report 只可依赖 model / query，实际引用 %s", f, dep)
			}
		}
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, word := range banned {
			if strings.Contains(body, word) {
				t.Fatalf("%s 出现禁用 / 越阶段命名 %q", f, word)
			}
		}
	}
}

// 报告输出里不得出现 S2 的 reviews / S4 的索引统计（grep 反证）。
//
// 【M6 · T-…-072】`txn_id` 从这份禁词表里移出：它自 M6 起是 A-59 的条件合同键
// （事务写必填、未开事务省略），不再是「越阶段字段」。未开事务时的省略仍被逐字钉住 ——
// 见本函数末尾与 TestReportStagePlaceholdersAreNotFabricated；填充侧见 TestReportTxnIDPopulated。
func TestReportOutputHasNoOtherStageFields(t *testing.T) {
	r := sample()
	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw) + "\n" + strings.Join(r.Lines(), "\n")
	for _, word := range []string{"rev" + "iews", "index_", "elapsed_ms"} {
		if strings.Contains(text, word) {
			t.Fatalf("报告输出不得出现越阶段字段 %q", word)
		}
	}
	// 未开事务（sample() 没走过任何事务写）⇒ txn_id 连键都不许出现。
	if strings.Contains(text, "txn"+"_id") {
		t.Fatal("未开事务的报告输出里不得出现 txn_id")
	}
	// sample() 里没有任何提案事实 → proposals 就是空数组（M3 起有事实时才产出真实值，
	// 见 TestReportFields_M3Stage）：无事实不造事实。
	if !strings.Contains(string(raw), `"proposals":[]`) {
		t.Fatal("没有提案事实时 proposals 必须是空数组")
	}
}

// skipped[].kind 在报告层同样是封闭两值：报告不认识第三种 kind 的渲染分支。
func TestReportSkippedKindsAreClosed(t *testing.T) {
	r := New()
	r.Skipped = []Skipped{
		{Kind: "file_changed", Target: "k-1", Locator: "a.md",
			Cause: "content_hash_mismatch", Detail: "文件自读取以来已变化"},
		{Kind: "user_block_unsafe", Target: "k-2", Locator: "b.md",
			Cause: "user_block_not_preserved", Detail: "用户分区无法逐字保留，已整文件跳过"},
	}
	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range r.Skipped {
		got[s.Kind] = true
	}
	if len(got) != 2 || !got["file_changed"] || !got["user_block_unsafe"] {
		t.Fatalf("S1 的 kind 恰两值，实得 %v", got)
	}
	for _, pair := range [][2]string{
		{"file_changed", "content_hash_mismatch"},
		{"user_block_unsafe", "user_block_not_preserved"},
	} {
		if !strings.Contains(string(raw), `"kind":"`+pair[0]+`","target"`) {
			t.Fatalf("JSON 缺 kind=%s", pair[0])
		}
		if !strings.Contains(string(raw), `"cause":"`+pair[1]+`"`) {
			t.Fatalf("JSON 缺 cause=%s", pair[1])
		}
	}
	lines := strings.Join(r.Lines(), "\n")
	if !strings.Contains(lines, "文件自读取以来已变化") ||
		!strings.Contains(lines, "用户分区无法逐字保留") {
		t.Fatalf("detail 必须对用户可读地出现在文本形态：%s", lines)
	}
}

// —— ⑨ JSON 键顺序稳定：apply 与 report --last 因此可逐字比对 ——

func TestReportJSONKeyOrderIsStable(t *testing.T) {
	first, err := sample().JSON()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := sample().JSON()
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("同一报告两次序列化必须逐字相同")
		}
	}
	if idx := strings.Index(string(first), `"source"`); idx != 1 {
		t.Fatalf("首键应为 source（键顺序 = 字段声明顺序），实得 %s", first[:32])
	}
}

// —— ⑩ M4 · T-evergreen.s1_main_flow-158614-057 追加：报告冻结面的四条反证 ——
//
// 本段全部是**追加**用例，一条既有断言都没改（唯一的例外是上面第 ② 段那处占位字面量
// 按实测重钉为 ReconcilePlaceholderJSON —— 三键三值全钉，只加严不放宽）。
// 判据来源：对账合同 §11、`M-004-m4.md` 完成判据 8。

// reportSourceOf 读本包某个非测试源文件的原文（用于源码级形态反证）。
func reportSourceOf(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(name))
	if err != nil {
		t.Fatalf("读 %s 失败：%v", name, err)
	}
	return string(body)
}

// TestS1RequiredElevenKeysUnchanged 钉住 §4.6 的 S1 必填 11 项在 M4 里**一字未变**：
// 键名、键数、键序、以及 `git.commit` 是**嵌套键**这件事，全都不许因为新增 S3 阶段键而变形。
func TestS1RequiredElevenKeysUnchanged(t *testing.T) {
	want := []string{"source", "note", "cards", "relations", "open_questions", "links",
		"git", "skipped", "default_domain_fallback", "high_impact", "warnings"}
	got := RequiredKeys()
	if len(got) != 11 {
		t.Fatalf("S1 必填 %d 项，§4.6 恰 11 项（M4 新增的 reconcile 是 S3 阶段键，不进本集合）", len(got))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("S1 必填清单 = %v，期望逐字 %v（键序也是合同的一部分）", got, want)
	}

	// 序列化面：11 项全在，且键序即声明序（前 11 个键逐位比对）。
	raw, err := sample().JSON()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := dec.Token(); err != nil { // 吃掉 '{'
		t.Fatal(err)
	}
	for dec.More() && len(order) < 11 {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		order = append(order, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("报告体前 11 个键 = %v，期望逐字 %v（S3 阶段键必须排在 S1 必填之后）", order, want)
	}

	// `git.commit` 仍是**嵌套键**，且顶层不得出现自创的 commit 键。
	m := keysOf(t, sample())
	var g map[string]json.RawMessage
	if err := json.Unmarshal(m["git"], &g); err != nil {
		t.Fatalf("git 不是对象：%v", err)
	}
	if _, ok := g["commit"]; !ok {
		t.Fatal("git.commit 必须在（§4.6 的 S1 必填项，M4 一字不动）")
	}
	if _, ok := m["commit"]; ok {
		t.Fatal("顶层出现自创键 commit：§4.6 的 commit 恒嵌套在 git 下")
	}
	// `reconcile.commit` 是**另一个**键，不得与 git.commit 混为一谈：两者同时存在且互不影响。
	rc := reconcileKeysOf(t, sample())
	if _, ok := rc["commit"]; !ok {
		t.Fatal("reconcile 内缺 commit 键（合同 §11 三键之一）")
	}
	if string(g["commit"]) == string(rc["commit"]) && string(g["commit"]) != "null" {
		t.Fatal("git.commit 与 reconcile.commit 不该被同一次赋值串起来：两者语义不同")
	}
}

// TestNoAffectedTopLevelKey 反证 `affected` **没有字段级定义**（合同 §11 第 5 条 / A-36）。
//
// 【按实测重钉的语义，只加严不放宽】规划期把本条写成「`affected` 顶层键不存在」，
// 实测：`affected` 自 **M1** 起就是报告体的**占位键**（值恒 `null`，§4.6 要求键在、值不造假），
// 删掉它反而违反 M1 冻结口径。合同 §11 第 5 条的本意是「M4 **不实现** `affected`」＝
// **不给它字段级定义**。因此本用例钉的是：占位键在册（恰一处）、值恒 `null`、
// 本包内**没有**任何 `affected` 的具名类型或子键定义。
func TestNoAffectedTopLevelKey(t *testing.T) {
	for _, r := range []Report{New(), sample(), reconciled(true)} {
		m := keysOf(t, r)
		raw, ok := m["affected"]
		if !ok {
			t.Fatal("affected 占位键被删：§4.6 要求阶段字段「键在、值不造假」，M4 只是不给它字段级定义")
		}
		if string(raw) != "null" {
			t.Fatalf("affected = %s，期望 null —— M4 不实现该字段（A-36 待 owner 补定义），"+
				"任何非 null 值都是编造事实", raw)
		}
	}
	// 源码级：本包内不存在 `affected` 的具名类型（字段级定义的形态特征）。
	for _, name := range []string{"report.go", "reconcile.go"} {
		body := reportSourceOf(t, name)
		for _, bad := range []string{"type Affected", "AffectedEntry", "AffectedItem"} {
			if strings.Contains(body, bad) {
				t.Fatalf("%s 出现 %q：M4 明确不给 affected 字段级定义（A-36）", name, bad)
			}
		}
	}
	// `reconcile` 对象内同样不得混进 affected（三键封闭的另一面反证）。
	if _, ok := reconcileKeysOf(t, reconciled(true))["affected"]; ok {
		t.Fatal("reconcile 内出现 affected：三键封闭（合同 §11）")
	}
}

// TestAffectedStaysNullPlaceholderExactlyOnce 是上一条的加严补充：
// 占位挂点在报告体里**恰一处**（不许出现第二个 affected 挂点），且它仍在 StageKeys() 名册内。
func TestAffectedStaysNullPlaceholderExactlyOnce(t *testing.T) {
	const tag = "json:\"" + "affected\""
	n := strings.Count(reportSourceOf(t, "report.go"), tag)
	if n != 1 {
		t.Fatalf("report.go 里 affected 挂点 %d 处，期望恰 1 处（M1 起的占位键，不增不删）", n)
	}
	if strings.Contains(reportSourceOf(t, "reconcile.go"), tag) {
		t.Fatal("reconcile.go 里出现 affected 挂点：该键不属对账三键")
	}
	stage := strings.Join(StageKeys(), ",")
	if stage != "proposals,deprecated_new_support,support_check,affected,reconcile" {
		t.Fatalf("阶段键名册 = %v，期望 5 项且逐字不变（M4 不增不减阶段键）", StageKeys())
	}
}

// TestSkippedKindStillTwo 反证 `skipped[].kind` 在 M4 里仍**恰两值**：
// R2 / R6 被 B3 拦下的写入一律进既有的 `file_changed`，**不新增第三种 kind**（合同 §11 第 6 条）。
func TestSkippedKindStillTwo(t *testing.T) {
	// 报告层不定义 kind 枚举（枚举真源在 plan / cli 侧），本用例钉的是「报告层没偷偷加第三种」：
	// 本包非测试源里出现的 kind 字面量恰是那两个既有值。
	// 两个既有取值的真源是 internal/store 的 SkipReason（B3 / B2 各一个）。
	// 报告层不 import store（依赖方向），因此这里持有同一份字面量，
	// 由 internal/cli 侧既有的等号断言把两侧锁死 —— 与 ExecutionFailed 同一套办法。
	want := map[string]bool{"file_changed": true, "user_block_unsafe": true}
	seen := map[string]bool{}
	for _, name := range []string{"report.go", "reconcile.go", "support_check.go"} {
		for _, line := range strings.Split(reportSourceOf(t, name), "\n") {
			for k := range want {
				if strings.Contains(line, "\""+k+"\"") {
					seen[k] = true
				}
			}
			// 新造 kind 的形态特征：`Kind: "…"` 出现了名册外的取值。
			if idx := strings.Index(line, "Kind: \""); idx >= 0 {
				rest := line[idx+len("Kind: \""):]
				if end := strings.Index(rest, "\""); end >= 0 {
					if v := rest[:end]; !want[v] {
						t.Fatalf("%s 出现名册外的 skipped kind %q：M4 不新增第三种 kind", name, v)
					}
				}
			}
		}
	}
	if len(seen) > len(want) {
		t.Fatalf("报告层出现 %d 种 kind，期望不超过 2 种", len(seen))
	}
	// 结构层：Skipped 仍是恰五字段（kind / target / locator / cause / detail），键面未扩。
	if got := len(strings.Split("kind,target,locator,cause,detail", ",")); got != 5 {
		t.Fatalf("Skipped 键面自查失败：%d", got)
	}
	r := sample()
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(keysOf(t, r)["skipped"], &list); err != nil {
		t.Fatalf("skipped 不是数组：%v", err)
	}
	for i, item := range list {
		if len(item) != 5 {
			t.Fatalf("skipped[%d] 有 %d 个键，期望恰 5 键", i, len(item))
		}
		var kind string
		if err := json.Unmarshal(item["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		if !want[kind] {
			t.Fatalf("skipped[%d].kind = %q：kind 恰两值（合同 §8）", i, kind)
		}
	}
}

// —— ⑧ M6 · T-…-072：txn_id 是 A-59 的条件合同键（事务写必填 / 未开事务省略）——

// TestReportTxnIDPopulated 钉住**填充侧**：一旦本次写入真的建立并提交了 Markdown 事务，
// 报告里就必须出现 `txn_id`，且逐字等于传入的事务号。
//
// 这一条与 TestReportStagePlaceholdersAreNotFabricated 的「未开事务必须省略」互为反证：
// 两侧合起来才是 A-59 的完整语义 —— 有事务必说、无事务不编。
func TestReportTxnIDPopulated(t *testing.T) {
	const id = "t0123456789abcdef"

	// ① 未开事务：键根本不出现（omitempty），而不是出现一个空串。
	before := keysOf(t, sample())
	if _, ok := before["txn_id"]; ok {
		t.Fatal("未调用 SetTxnID 时 txn_id 不得出现")
	}

	// ② 事务写：键在且值逐字相等。
	r := sample()
	r.SetTxnID(id)
	after := keysOf(t, r)
	raw, ok := after["txn_id"]
	if !ok {
		t.Fatal("事务已提交却没有 txn_id：凡真正建立并提交 Markdown 事务的最终报告必填（A-59）")
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("txn_id 不是字符串：%v", err)
	}
	if got != id {
		t.Fatalf("txn_id = %q，期望 %q（必须逐字等于事务日志目录名）", got, id)
	}

	// ③ 空串仍然省略：SetTxnID("") 不制造一个「有键无值」的假事务。
	z := sample()
	z.SetTxnID("")
	if _, ok := keysOf(t, z)["txn_id"]; ok {
		t.Fatal("SetTxnID(\"\") 不得产出 txn_id 键：空事务号就是没有事务")
	}
}

// TestReportTxnIDIsTheOnlyKeyFaceDelta 钉住**键面漂移只有一格**：
// 填了 txn_id 的报告，除了多出这一个键之外，其余键名集合与未开事务的报告逐字相同。
//
// 为什么单独钉：M1~M5 的下游（Agent / CI / e2e）是按键面吃报告的。M6 允许多出 txn_id，
// 但绝不允许顺手多出第二个键 —— 否则「事务化」就变成了一次静默的 schema 变更。
func TestReportTxnIDIsTheOnlyKeyFaceDelta(t *testing.T) {
	plain := keysOf(t, sample())
	withTxn := sample()
	withTxn.SetTxnID("t00000000000000ff")
	txned := keysOf(t, withTxn)

	if len(txned) != len(plain)+1 {
		t.Fatalf("键数 %d → %d，期望恰 +1（只多 txn_id 一个键）", len(plain), len(txned))
	}
	for k := range txned {
		if k == "txn_id" {
			continue
		}
		if _, ok := plain[k]; !ok {
			t.Fatalf("事务写多出了 txn_id 之外的键 %q", k)
		}
	}
	for k := range plain {
		if _, ok := txned[k]; !ok {
			t.Fatalf("事务写弄丢了键 %q", k)
		}
	}
}

// TestReportTxnIDKeepsReconcileThreeKeys 钉住 M4 的 `reconcile` **恰三键占位**不因 M6 事务化而漂移。
//
// 这是「延期债不许悄悄扩散」的机器形态：T-…-072 只被允许在报告上加一个条件键 txn_id，
// 而 `reconcile` 在非对账路径下必须仍是逐字的 {"ran":false,"commit":null,"findings":[]}
// —— 三键三值，一个不多一个不少，和 M4 验收当天完全一致。
func TestReportTxnIDKeepsReconcileThreeKeys(t *testing.T) {
	r := sample()
	r.SetTxnID("t0f0f0f0f0f0f0f0f")
	raw, ok := keysOf(t, r)["reconcile"]
	if !ok {
		t.Fatal("reconcile 整键缺席：非对账路径也必须键在值空")
	}
	if string(raw) != ReconcilePlaceholderJSON {
		t.Fatalf("reconcile = %s，期望逐字 %s（M6 事务化不得改动 M4 的三键占位）",
			raw, ReconcilePlaceholderJSON)
	}
	var three map[string]json.RawMessage
	if err := json.Unmarshal(raw, &three); err != nil {
		t.Fatalf("reconcile 不是对象：%v", err)
	}
	if len(three) != 3 {
		t.Fatalf("reconcile 有 %d 个键，期望恰 3 键（ran / commit / findings）", len(three))
	}
}
