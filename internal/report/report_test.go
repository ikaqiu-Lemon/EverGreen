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
	want := map[string]string{
		"proposals":              "[]",
		"deprecated_new_support": "[]",
		"support_check":          "null",
		"affected":               "null",
		"reconcile":              `{"ran":false}`,
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
	if _, ok := m["txn_id"]; ok {
		t.Fatal("txn_id 属 S5，必须省略")
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
			if strings.Contains(body, `"evergreen/`+dep+`"`) {
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

// 报告输出里不得出现 S2 的 reviews / S5 的 txn_id / S4 的索引统计（grep 反证）。
func TestReportOutputHasNoOtherStageFields(t *testing.T) {
	r := sample()
	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw) + "\n" + strings.Join(r.Lines(), "\n")
	for _, word := range []string{"rev" + "iews", "txn" + "_id", "index_", "elapsed_ms"} {
		if strings.Contains(text, word) {
			t.Fatalf("报告输出不得出现越阶段字段 %q", word)
		}
	}
	if !strings.Contains(string(raw), `"proposals":[]`) {
		t.Fatal("proposals 仅以空数组形态出现")
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
