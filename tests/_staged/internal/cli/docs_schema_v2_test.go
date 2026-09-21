package cli

// T-008 · T8-2 的 Schema v2 **当前文档合同**机器判据（聚焦、只做静态断言）。
//
// 与 docs_test.go 的分工：docs_test.go 守 M2–M6 的历史判据（脚手架措辞、退出码表、命令登记、
// 版本三处同源）；本文件**只守 Schema v2 用户合同的当前态**——两份 README 的 quick-start
// JSON（ChangePlan / context）、四实体与模板、主链路 op、search/index，以及 README.zh-CN.md 与
// CHANGELOG.md 的 Unreleased 登记。
//
// 关键设计（T8-2 两轮复审后加固）：
//   1) 判据一律**钉在对应章节内**（sectionBetween / planJSONBlock / contextJSONBlock），不搜整篇；
//   2) 「三行原文逐字保真」不再用句中子串——而是从 quick-start capture 的 heredoc 取三条**完整正文**，
//      再把 plan JSON 的 write_note.blocks **结构化 json 解析**，断言 L2-L2/L3-L3/L4-L4 各恰一条、
//      body 与对应 capture 行**逐字相等**、且无 L1；删前缀 / 删后缀会翻红（见 self-check）；
//   3) **正式断言与 mutation/self-check 复用同一份 validator**（vPlanVersionTwo / vOpinionCandidates /
//      vKnowledgeThree / vWriteNoteFidelity）——self-check 证明的是正式判据本身、不是另写的简化 lambda；
//   4) 章节内措辞逐字锁定：ChangePlan 明确「当前 plan_version=2」；search 精确到 `--kind opinion` /
//      `--kind all`；index 精确到 `schema_version = 2` + 「六表」措辞 + 六个表名 + 整库重建；v1 Note 兼容
//      说明除三旧名外还断言「兼容映射 + 按字节 / byte-for-byte 原样保留」语义；
//   5) CHANGELOG 的 plan_version 非 Breaking 按**整条 bullet**判断（不是同一行）。
//
// 实现真值锚点（供审阅逐条复核）：
//   internal/plan/schema.go：PlanVersion=2、SupportedPlanVersions={1,2}、TopLevelKeys 恰 8、
//     OpNames 恰 9（add_source/write_note/create_knowledge/append_knowledge/create_opinion/
//     append_opinion/add_material_rel/add_relation/add_open_question）、OpAliases{create_card→
//     create_knowledge, append_card→append_knowledge}；write_note 字段表含 blocks/omissions/
//     extraction_coverage。
//   internal/query/context.go：candidates == knowledge_candidates（同一底层切片），另有
//     opinion_candidates；query.Build 在 diagnostics 中**无条件**追加恰一条 candidates 弃用 I1
//     （与调用方是否读取字段无关），两类候选都进 base。
//   internal/cli/search.go：--kind 默认 knowledge，可选 opinion|all。
//   internal/index/schema.go：IndexSchemaVersion=2、cards/cards_fts 带 kind + validation、共 6 表、
//     版本不符整库重建（无增量迁移）。
//   internal/mdfile/sections.go：Knowledge 三分区（知识内容/条件与边界/用户补充）、Opinion 五分区
//     （观点/论据与推理/条件与反例/待验证/用户补充）、Note 四分区（整理正文/提取结果/存疑与待验证/
//     用户补充）；解释与依据 / 理解自检 属 LegacyV1Sections（兼容原样保留 + info，非当前固定分区）。
//   internal/store：Source 落盘正文 L1 为模板前导空行，真实正文自 L2 起。
//   internal/plan/validate_opinion.go：create_opinion 默认 validation=pending，plan 内直接写
//     validated/rejected 判 E2。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

const (
	docsREADMEZH  = "../../README.zh-CN.md"
	docsCHANGELOG = "../../CHANGELOG.md"
)

// docsREADMEs 是两份需要携带 Schema v2 当前用户合同的说明书（英文 + 简体中文）。
func docsREADMEs() []string { return []string{docsREADME, docsREADMEZH} }

// readmeAnchors 把两份 README 各自的章节标题、当前 Note 模板句边界，以及若干**逐字合同短语**登记下来。
// 判据据此把断言钉在**对应章节**内，杜绝「全文件某处出现即算数」的假绿。
type readmeAnchors struct {
	path string

	ctxStart, ctxEnd               string // quick-start step 3（eg context）
	planStart, planEnd             string // quick-start step 4（ChangePlan 写入）
	entitiesStart, entitiesEnd     string // 四学习实体（含 vault 目录）
	templatesStart, templatesEnd   string // 实体模板
	noteTplStart, noteTplEnd       string // 当前 Note 模板句（不含其后的兼容说明段）
	noteCompatStart, noteCompatEnd string // v1 Note 兼容说明段
	changePlanStart, changePlanEnd string // ChangePlan 概念小节
	readStart, readEnd             string // 读路径（search --kind 匹配分说明）
	indexStart, indexEnd           string // 派生索引（index schema）

	// context 兼容口径逐字短语（钉在 ctx 小节内）。
	ctxEqualPhrase string // candidates ≡ knowledge_candidates
	ctxI1Phrase    string // eg context 无条件发出恰一条 I1
	ctxBasePhrase  string // 两类候选都进 base

	// ChangePlan 逐字短语（钉在 changePlan 小节内）。
	eightKeysPhrase          string
	planVersionCurrentPhrase string // 明确「当前 plan_version = 2」

	// search / index 逐字短语（各钉在自己章节内）。
	searchDefaultPhrase  string // eg search 默认 --kind knowledge
	sixTablesPhrase      string // 「六表 / 六张表」措辞
	indexNoMigratePhrase string // 无增量 schema 迁移
	indexRebuildPhrase   string // 版本不符 → 整库重建

	// v1 Note 兼容说明语义短语（钉在 noteCompat 段内）。
	noteCompatMapPhrase  string // 兼容映射 / compatibility-mapped
	noteCompatBytePhrase string // 按字节 / byte-for-byte

	// Knowledge 分区数措辞（供 EntityTemplates 断言 + mutation 自检共用 validator）。
	knowledgeThree string
	knowledgeFive  string

	// 旧 v1 Note 分区名（当前模板句里必须禁止，兼容说明段落里必须如实登记）。
	staleNoteSections []string
}

func readmeAnchorTable() []readmeAnchors {
	return []readmeAnchors{
		{
			path:            docsREADME,
			ctxStart:        "### 3. Get context for processing",
			ctxEnd:          "### 4. Write through a ChangePlan",
			planStart:       "### 4. Write through a ChangePlan",
			planEnd:         "### 5. Read it back",
			entitiesStart:   "## Core concepts",
			entitiesEnd:     "### Entity templates",
			templatesStart:  "### Entity templates",
			templatesEnd:    "### Relationships",
			noteTplStart:    "**Note — four sections:**",
			noteTplEnd:      "The legacy v1 Knowledge sections",
			noteCompatStart: "Likewise, the legacy v1 Note sections",
			noteCompatEnd:   "This split is what keeps",
			changePlanStart: "### The ChangePlan",
			changePlanEnd:   "## Using Evergreen with a coding agent",
			readStart:       "## Reading: sorting, pagination, and fallback",
			readEnd:         "## Safety model",
			indexStart:      "## The derived index",
			indexEnd:        "## Performance",

			ctxEqualPhrase:           "identically equal to\n`knowledge_candidates`",
			ctxI1Phrase:              "unconditionally emits exactly one `I1` deprecation info",
			ctxBasePhrase:            "Both typed\nlists feed the plan's `base`",
			eightKeysPhrase:          "exactly eight top-level keys",
			planVersionCurrentPhrase: "`2` for current plans",
			searchDefaultPhrase:      "defaults to `--kind knowledge`",
			sixTablesPhrase:          "six tables",
			indexNoMigratePhrase:     "no incremental schema migration",
			indexRebuildPhrase:       "the whole index is discarded and rebuilt from Markdown",
			noteCompatMapPhrase:      "compatibility-mapped",
			noteCompatBytePhrase:     "byte-for-byte",
			knowledgeThree:           "Knowledge — three sections",
			knowledgeFive:            "Knowledge — five sections",
			staleNoteSections:        []string{"材料提炼", "Agent 分析", "产出知识卡"},
		},
		{
			path:            docsREADMEZH,
			ctxStart:        "### 3. 取加工上下文",
			ctxEnd:          "### 4. 通过 ChangePlan 写入",
			planStart:       "### 4. 通过 ChangePlan 写入",
			planEnd:         "### 5. 读回来",
			entitiesStart:   "## 核心概念",
			entitiesEnd:     "### 实体模板",
			templatesStart:  "### 实体模板",
			templatesEnd:    "### 关系",
			noteTplStart:    "**Note —— 四分区：**",
			noteTplEnd:      "旧 v1 的 Knowledge 分区",
			noteCompatStart: "同样，旧 v1 的 Note 分区",
			noteCompatEnd:   "这套切分正是让",
			changePlanStart: "### ChangePlan",
			changePlanEnd:   "## 与 coding agent 配合使用",
			readStart:       "## 读路径：排序、分页与降级",
			readEnd:         "## 安全模型",
			indexStart:      "## 派生索引",
			indexEnd:        "## 性能",

			ctxEqualPhrase:           "恒等于 `knowledge_candidates`",
			ctxI1Phrase:              "无条件发出恰一条 `I1` 弃用提示",
			ctxBasePhrase:            "分别进入 plan 的 `base`",
			eightKeysPhrase:          "顶层恰 8 个键",
			planVersionCurrentPhrase: "当前 plan 为 `2`",
			searchDefaultPhrase:      "默认 `--kind knowledge`",
			sixTablesPhrase:          "六张表",
			indexNoMigratePhrase:     "没有增量 schema 迁移",
			indexRebuildPhrase:       "整个索引被丢弃并从 Markdown 重建",
			noteCompatMapPhrase:      "兼容映射",
			noteCompatBytePhrase:     "按字节",
			knowledgeThree:           "Knowledge —— 三分区",
			knowledgeFive:            "Knowledge —— 五分区",
			staleNoteSections:        []string{"材料提炼", "Agent 分析", "产出知识卡"},
		},
	}
}

func anchorsFor(t *testing.T, path string) readmeAnchors {
	t.Helper()
	for _, a := range readmeAnchorTable() {
		if a.path == path {
			return a
		}
	}
	t.Fatalf("无 %s 的章节锚点登记", path)
	return readmeAnchors{}
}

// sectionBetween 返回 doc 中从 startAnchor（含）到其后首个 endAnchor（不含）之间的正文，用章节标题
// 子串精确定位；缺任一 anchor 直接 Fatal，确保判据钉在**具体章节**内而非整篇。
func sectionBetween(t *testing.T, doc, startAnchor, endAnchor string) string {
	t.Helper()
	si := strings.Index(doc, startAnchor)
	if si < 0 {
		t.Fatalf("定位不到章节起点 anchor %q", startAnchor)
	}
	rest := doc[si:]
	ei := strings.Index(rest[len(startAnchor):], endAnchor)
	if ei < 0 {
		t.Fatalf("定位不到章节终点 anchor %q（起点 %q 之后）", endAnchor, startAnchor)
	}
	return rest[:len(startAnchor)+ei]
}

// fencedBlocks 机械抽取给定语言的围栏代码块正文（```<lang> … ```）。
func fencedBlocks(doc, lang string) []string {
	var blocks []string
	var cur []string
	inBlock := false
	open := "```" + lang
	for _, line := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(line)
		if !inBlock {
			if t == open {
				inBlock = true
				cur = nil
			}
			continue
		}
		if strings.HasPrefix(t, "```") {
			blocks = append(blocks, strings.Join(cur, "\n"))
			inBlock = false
			continue
		}
		cur = append(cur, line)
	}
	return blocks
}

// planJSONBlock 返回 quick-start 的 ChangePlan 示例块（含 ops 且含 write_note 的那个 json 围栏）。
// 该块只在 quick-start step 4 小节里，天然钉在当前示例内。
func planJSONBlock(t *testing.T, path string) string {
	t.Helper()
	for _, b := range fencedBlocks(readDocs(t, path), "json") {
		if strings.Contains(b, `"ops"`) && strings.Contains(b, `"write_note"`) {
			return b
		}
	}
	t.Fatalf("%s 缺 quick-start 的 ChangePlan 示例（含 ops 与 write_note 的 ```json 块）", path)
	return ""
}

// contextJSONBlock 返回 quick-start 的 eg context 示例块（含 base 的那个 json 围栏）。
func contextJSONBlock(t *testing.T, path string) string {
	t.Helper()
	for _, b := range fencedBlocks(readDocs(t, path), "json") {
		if strings.Contains(b, `"base"`) && !strings.Contains(b, `"ops"`) {
			return b
		}
	}
	t.Fatalf("%s 缺 quick-start 的 eg context 示例（含 base 的 ```json 块）", path)
	return ""
}

// captureBodyLines 从 quick-start「收录原文」小节的 heredoc（```console 里 <<'EOF' … EOF）取三条
// **完整正文行**——这是 write_note.blocks 的来源真值，用于逐字保真比对（不是句中子串）。
func captureBodyLines(t *testing.T, path string) []string {
	t.Helper()
	for _, c := range fencedBlocks(readDocs(t, path), "console") {
		if !strings.Contains(c, "<<'EOF'") {
			continue
		}
		lines := strings.Split(c, "\n")
		start := -1
		for i, l := range lines {
			if strings.Contains(l, "<<'EOF'") {
				start = i
				break
			}
		}
		var body []string
		for _, l := range lines[start+1:] {
			if strings.TrimSpace(l) == "EOF" {
				break
			}
			body = append(body, l)
		}
		if len(body) != 3 {
			t.Fatalf("%s 的 capture heredoc 正文应为 3 行，实得 %d 行", path, len(body))
		}
		return body
	}
	t.Fatalf("%s 缺 quick-start capture 的 heredoc（<<'EOF' … EOF）", path)
	return nil
}

// writeNoteRegion 从 plan 块里切出 write_note 这一条 op 的文本（从它的 "op":"write_note"
// 到下一个 "op": 之前），用于对 write_note **本条**做结构性「含 / 不含」判定，不误伤同块的
// create_knowledge / create_opinion（它们合法地使用 sections{}）。
func writeNoteRegion(t *testing.T, block string) string {
	t.Helper()
	opRE := regexp.MustCompile(`"op"\s*:\s*"write_note"`)
	loc := opRE.FindStringIndex(block)
	if loc == nil {
		t.Fatal("plan 块内未定位到 write_note op")
	}
	rest := block[loc[1]:]
	nextRE := regexp.MustCompile(`"op"\s*:\s*"`)
	if next := nextRE.FindStringIndex(rest); next != nil {
		return rest[:next[0]]
	}
	return rest
}

// ---------------------------------------------------------------------------
// 共享 validator：正式断言与 mutation/self-check **调用同一份**，保证 self-check 证明的是正式判据
// 本身、而非测试里另写的简化逻辑（T8-2 复审要求 2）。每个 validator 返回 nil=通过 / error=翻红。
// ---------------------------------------------------------------------------

var (
	planV2RE = regexp.MustCompile(`"plan_version"\s*:\s*2\b`)
	planV1RE = regexp.MustCompile(`"plan_version"\s*:\s*1\b`)
)

// vPlanVersionTwo：quick-start ChangePlan 示例必须声明 plan_version:2 且不得再出现 plan_version:1。
func vPlanVersionTwo(planBlock string) error {
	if !planV2RE.MatchString(planBlock) {
		return fmt.Errorf(`未声明 "plan_version": 2`)
	}
	if planV1RE.MatchString(planBlock) {
		return fmt.Errorf(`当前示例块仍出现 "plan_version": 1`)
	}
	return nil
}

// vOpinionCandidates：eg context 示例必须含 opinion_candidates 字段。
func vOpinionCandidates(ctxBlock string) error {
	if !strings.Contains(ctxBlock, `"opinion_candidates"`) {
		return fmt.Errorf(`eg context 示例缺 "opinion_candidates" 字段`)
	}
	return nil
}

// vKnowledgeThree：模板小节必须把 Knowledge 标为「三分区」且不得说成「五分区」。
func vKnowledgeThree(templatesSec, three, five string) error {
	if !strings.Contains(templatesSec, three) {
		return fmt.Errorf("模板小节未把 Knowledge 标为三分区（缺 %q）", three)
	}
	if strings.Contains(templatesSec, five) {
		return fmt.Errorf("模板小节把 Knowledge 说成五分区（%q）", five)
	}
	return nil
}

// planBlockJSON / planOpJSON / planDocJSON：write_note.blocks 的结构化解析目标（只取需要的字段）。
type planBlockJSON struct {
	Role      string `json:"role"`
	SourceRef string `json:"source_ref"`
	Body      string `json:"body"`
}

type planOpJSON struct {
	Op     string          `json:"op"`
	Blocks []planBlockJSON `json:"blocks"`
}

type planDocJSON struct {
	Ops []planOpJSON `json:"ops"`
}

// vWriteNoteFidelity：把 plan JSON 结构化解析，断言 write_note 恰一条、其来源块恰 3 条，
// L2-L2/L3-L3/L4-L4 **各恰一条**、body 与对应 capture 行**逐字相等**、且无回指 L1 的来源块。
// capture 是 quick-start heredoc 的三条完整正文行（capture[0..2] ↔ L2/L3/L4）。
func vWriteNoteFidelity(planBlock string, capture []string) error {
	if len(capture) != 3 {
		return fmt.Errorf("capture 正文应为 3 行，实得 %d", len(capture))
	}
	var pd planDocJSON
	if err := json.Unmarshal([]byte(planBlock), &pd); err != nil {
		return fmt.Errorf("plan JSON 解析失败：%v", err)
	}
	wnCount := 0
	var blocks []planBlockJSON
	for _, op := range pd.Ops {
		if op.Op == "write_note" {
			wnCount++
			blocks = op.Blocks
		}
	}
	if wnCount != 1 {
		return fmt.Errorf("write_note op 应恰 1 个，实得 %d", wnCount)
	}
	byRef := map[string][]string{}
	total := 0
	for _, b := range blocks {
		if b.Role != "source" {
			continue
		}
		total++
		if strings.HasPrefix(b.SourceRef, "L1") {
			return fmt.Errorf("来源块回指模板前导空行 %q（真实正文自 L2 起，不得引用 L1）", b.SourceRef)
		}
		byRef[b.SourceRef] = append(byRef[b.SourceRef], b.Body)
	}
	if total != 3 {
		return fmt.Errorf("write_note 来源块（role=source）应恰 3 条，实得 %d", total)
	}
	want := []struct{ ref, body string }{
		{"L2-L2", capture[0]},
		{"L3-L3", capture[1]},
		{"L4-L4", capture[2]},
	}
	for _, w := range want {
		got := byRef[w.ref]
		if len(got) != 1 {
			return fmt.Errorf("source_ref %q 应恰 1 条，实得 %d", w.ref, len(got))
		}
		if got[0] != w.body {
			return fmt.Errorf("source_ref %q 的 body 与 capture 原文不逐字相等：\n  block  =%q\n  capture=%q", w.ref, got[0], w.body)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 正式断言
// ---------------------------------------------------------------------------

// TestDocsV2_PlanVersionIsTwo：两份 README 的 quick-start ChangePlan 示例 plan_version 必须为 2，
// 且当前示例块内不得再出现 plan_version:1。调用共享 validator vPlanVersionTwo。
func TestDocsV2_PlanVersionIsTwo(t *testing.T) {
	for _, p := range docsREADMEs() {
		if err := vPlanVersionTwo(planJSONBlock(t, p)); err != nil {
			t.Fatalf("%s 的 quick-start ChangePlan：%v", p, err)
		}
	}
}

// TestDocsV2_WriteNoteCanonicalShape：quick-start 的 write_note 必须是 v2 canonical 形态——
// 含 blocks[] / omissions[] / extraction_coverage[]，不得再用 sections{} 或 coverage_gaps；
// 并调用共享 validator vWriteNoteFidelity，结构化断言三条来源块 body 与 capture 原文逐字相等。
func TestDocsV2_WriteNoteCanonicalShape(t *testing.T) {
	for _, p := range docsREADMEs() {
		block := planJSONBlock(t, p)
		region := writeNoteRegion(t, block)
		for _, must := range []string{`"blocks"`, `"omissions"`, `"extraction_coverage"`} {
			if !strings.Contains(region, must) {
				t.Fatalf("%s 的 write_note 缺 v2 canonical 字段 %s", p, must)
			}
		}
		for _, banned := range []string{`"sections"`, `"coverage_gaps"`} {
			if strings.Contains(region, banned) {
				t.Fatalf("%s 的 write_note 仍含 v1 字段 %s（v2 write_note 用 blocks[]，禁用它）", p, banned)
			}
		}
		if err := vWriteNoteFidelity(block, captureBodyLines(t, p)); err != nil {
			t.Fatalf("%s 的 write_note 来源块保真校验失败：%v", p, err)
		}
	}
}

// TestDocsV2_QuickStartUsesDualEntities：quick-start plan 必须同时示范 create_knowledge 与
// create_opinion（各至少一个 k-* 与一个 o-*），且不得再用 create_card / append_card（兼容别名）。
func TestDocsV2_QuickStartUsesDualEntities(t *testing.T) {
	for _, p := range docsREADMEs() {
		block := planJSONBlock(t, p)
		for _, must := range []string{`"create_knowledge"`, `"create_opinion"`} {
			if !strings.Contains(block, must) {
				t.Fatalf("%s 的 quick-start plan 未示范 %s（v2 双入口）", p, must)
			}
		}
		for _, banned := range []string{`"create_card"`, `"append_card"`} {
			if strings.Contains(block, banned) {
				t.Fatalf("%s 的 quick-start plan 仍用兼容别名 %s（新 plan 禁用，改用 create_knowledge/append_knowledge）", p, banned)
			}
		}
		if !regexp.MustCompile(`\bk-`).MatchString(block) {
			t.Fatalf("%s 的 quick-start plan 缺 Knowledge 产物 id（k-*）", p)
		}
		if !regexp.MustCompile(`\bo-`).MatchString(block) {
			t.Fatalf("%s 的 quick-start plan 缺 Opinion 产物 id（o-*）", p)
		}
	}
}

// TestDocsV2_ContextDualCandidates：quick-start 的 eg context 示例块必须同时含 candidates、
// knowledge_candidates、opinion_candidates 三字段（后者走共享 validator）；且**同一小节的兼容正文**须
// 逐字写明：candidates ≡ knowledge_candidates、I1 由 eg context 无条件发出、0.7.x 保留 / 0.8.0 删除、
// 两类候选都进 base。断言全部钉在 step-3 小节内。
func TestDocsV2_ContextDualCandidates(t *testing.T) {
	for _, p := range docsREADMEs() {
		a := anchorsFor(t, p)
		block := contextJSONBlock(t, p)
		for _, must := range []string{`"candidates"`, `"knowledge_candidates"`} {
			if !strings.Contains(block, must) {
				t.Fatalf("%s 的 eg context 示例缺字段 %s（v2 三字段并存）", p, must)
			}
		}
		if err := vOpinionCandidates(block); err != nil {
			t.Fatalf("%s 的 eg context 示例：%v", p, err)
		}
		ctx := sectionBetween(t, readDocs(t, p), a.ctxStart, a.ctxEnd)
		checks := []struct{ what, phrase string }{
			{"candidates ≡ knowledge_candidates", a.ctxEqualPhrase},
			{"eg context 无条件发出恰一条 I1", a.ctxI1Phrase},
			{"两类候选都进 base", a.ctxBasePhrase},
			{"0.7.x 兼容保留", "0.7"},
			{"0.8.0 删除", "0.8.0"},
		}
		for _, c := range checks {
			if !strings.Contains(ctx, c.phrase) {
				t.Fatalf("%s 的 eg context 小节未写明「%s」，缺短语 %q", p, c.what, c.phrase)
			}
		}
		// 反证事实错误：不得把 I1 说成「读取 candidates 才触发」。
		for _, wrong := range []string{
			"reading it raises an", "reading `candidates` raises",
			"读取 candidates 才", "读取该字段才", "读取字段触发",
		} {
			if strings.Contains(ctx, wrong) {
				t.Fatalf("%s 的 eg context 小节仍把 I1 归因于调用方读取字段（%q）——I1 由 eg context 无条件发出", p, wrong)
			}
		}
	}
}

// TestDocsV2_FourLearningEntities：核心概念小节里把 Source s-*/Note n-*/Knowledge k-*/Opinion o-*
// 与两目录（knowledge/、opinions/）写全，并把 Proposal 明确为控制面（p-*）。
func TestDocsV2_FourLearningEntities(t *testing.T) {
	for _, p := range docsREADMEs() {
		a := anchorsFor(t, p)
		sec := sectionBetween(t, readDocs(t, p), a.entitiesStart, a.entitiesEnd)
		for _, must := range []string{"s-", "n-", "k-", "o-", "Opinion", "opinions/", "knowledge/", "p-"} {
			if !strings.Contains(sec, must) {
				t.Fatalf("%s 的核心概念小节缺标记 %q", p, must)
			}
		}
	}
}

// TestDocsV2_EntityTemplates：实体模板小节内，Knowledge 三分区 / Opinion 五分区 / Note 四分区的
// **当前固定分区名**逐条在场；Knowledge「三分区而非五分区」走共享 validator vKnowledgeThree。
func TestDocsV2_EntityTemplates(t *testing.T) {
	knowledge := []string{"知识内容", "条件与边界", "用户补充"}
	opinion := []string{"观点", "论据与推理", "条件与反例", "待验证", "用户补充"}
	note := []string{"整理正文", "提取结果", "存疑与待验证", "用户补充"}
	for _, p := range docsREADMEs() {
		a := anchorsFor(t, p)
		sec := sectionBetween(t, readDocs(t, p), a.templatesStart, a.templatesEnd)
		for _, s := range knowledge {
			if !strings.Contains(sec, s) {
				t.Fatalf("%s 的模板小节缺 Knowledge 当前分区名「%s」", p, s)
			}
		}
		for _, s := range opinion {
			if !strings.Contains(sec, s) {
				t.Fatalf("%s 的模板小节缺 Opinion 当前分区名「%s」", p, s)
			}
		}
		for _, s := range note {
			if !strings.Contains(sec, s) {
				t.Fatalf("%s 的模板小节缺 Note 当前分区名「%s」", p, s)
			}
		}
		if err := vKnowledgeThree(sec, a.knowledgeThree, a.knowledgeFive); err != nil {
			t.Fatalf("%s 的模板小节：%v", p, err)
		}
	}
}

// TestDocsV2_ChangePlanOps：ChangePlan 概念小节写明「顶层恰 8 键」、明确「当前 plan_version=2」/ 支持
// {1,2} / v1 给恰一条 I1；主链路 canonical op 恰九个逐条在场；create_card / append_card 归一 + I1 + 新 plan 不用。
func TestDocsV2_ChangePlanOps(t *testing.T) {
	nineOps := []string{
		"add_source", "write_note", "create_knowledge", "append_knowledge",
		"create_opinion", "append_opinion", "add_material_rel", "add_relation", "add_open_question",
	}
	for _, p := range docsREADMEs() {
		a := anchorsFor(t, p)
		sec := sectionBetween(t, readDocs(t, p), a.changePlanStart, a.changePlanEnd)
		if !strings.Contains(sec, a.eightKeysPhrase) {
			t.Fatalf("%s 的 ChangePlan 小节未写明「顶层恰 8 键」（缺 %q）", p, a.eightKeysPhrase)
		}
		// 顶层八键逐条在场（反引号闭合完整，含 convergence[] / ops[]）。
		for _, k := range []string{
			"`plan_version`", "`verb`", "`domain`", "`reason`",
			"`requirement_ids`", "`convergence[]`", "`base`", "`ops[]`",
		} {
			if !strings.Contains(sec, k) {
				t.Fatalf("%s 的 ChangePlan 小节缺顶层键 %s", p, k)
			}
		}
		if !strings.Contains(sec, a.planVersionCurrentPhrase) {
			t.Fatalf("%s 的 ChangePlan 小节未明确「当前 plan_version=2」（缺 %q）", p, a.planVersionCurrentPhrase)
		}
		if !strings.Contains(sec, "{1, 2}") {
			t.Fatalf("%s 的 ChangePlan 小节未写明支持集合 {1, 2}", p)
		}
		if !strings.Contains(sec, "I1") {
			t.Fatalf("%s 的 ChangePlan 小节未写明 v1 plan 的 I1 兼容提示", p)
		}
		for _, op := range nineOps {
			if !strings.Contains(sec, op) {
				t.Fatalf("%s 的 ChangePlan 小节缺主链路 canonical op %q（v2 恰九个）", p, op)
			}
		}
		for _, alias := range []string{"create_card", "append_card"} {
			if !strings.Contains(sec, alias) {
				t.Fatalf("%s 的 ChangePlan 小节未把 %s 记为兼容别名", p, alias)
			}
		}
		// 反证旧态：plan_version「恒为 1」与主链路「七个 op」当前口径必须清除（钉在小节内）。
		for _, stale := range []string{"Always `1`", "恒为 `1`", "恒为 1", "Always 1", "seven operations", "七个 op", "七 op"} {
			if strings.Contains(sec, stale) {
				t.Fatalf("%s 的 ChangePlan 小节仍含旧态口径 %q", p, stale)
			}
		}
	}
}

// TestDocsV2_SearchAndIndex：读路径小节写明 eg search 默认 --kind knowledge，并精确到 `--kind opinion`
// 与 `--kind all`；派生索引小节写明 `schema_version = 2`、「六表」措辞 + 六个表名、cards/cards_fts 承载
// kind + opinion validation、无增量迁移 / 版本不符整库重建。两组事实各钉在自己的章节内。
func TestDocsV2_SearchAndIndex(t *testing.T) {
	sixTables := []string{"index_meta", "cards", "cards_fts", "relations", "files", "skipped"}
	for _, p := range docsREADMEs() {
		a := anchorsFor(t, p)
		doc := readDocs(t, p)

		read := sectionBetween(t, doc, a.readStart, a.readEnd)
		if !strings.Contains(read, a.searchDefaultPhrase) {
			t.Fatalf("%s 的读路径小节未写明 eg search 默认 --kind knowledge（缺 %q）", p, a.searchDefaultPhrase)
		}
		for _, must := range []string{"`--kind opinion`", "`--kind all`"} {
			if !strings.Contains(read, must) {
				t.Fatalf("%s 的读路径小节未精确写明放宽标志 %s", p, must)
			}
		}

		idx := sectionBetween(t, doc, a.indexStart, a.indexEnd)
		if !strings.Contains(idx, "schema_version = 2") {
			t.Fatalf("%s 的派生索引小节未精确写明 `schema_version = 2`", p)
		}
		if !strings.Contains(idx, a.sixTablesPhrase) {
			t.Fatalf("%s 的派生索引小节未写明「六表」措辞（缺 %q）", p, a.sixTablesPhrase)
		}
		for _, tbl := range sixTables {
			if !strings.Contains(idx, "`"+tbl+"`") {
				t.Fatalf("%s 的派生索引小节缺六表之一 `%s`", p, tbl)
			}
		}
		for _, must := range []string{"kind", "validation"} {
			if !strings.Contains(idx, must) {
				t.Fatalf("%s 的派生索引小节未写明 cards/cards_fts 承载 %q", p, must)
			}
		}
		if !strings.Contains(idx, a.indexNoMigratePhrase) {
			t.Fatalf("%s 的派生索引小节未写明「无增量迁移」（缺 %q）", p, a.indexNoMigratePhrase)
		}
		if !strings.Contains(idx, a.indexRebuildPhrase) {
			t.Fatalf("%s 的派生索引小节未写明「版本不符整库重建」（缺 %q）", p, a.indexRebuildPhrase)
		}
	}
}

// unreleasedSection 抽出 CHANGELOG.md 的 ## Unreleased 段落（到下一个 "## " 之前）。
func unreleasedSection(t *testing.T, doc string) string {
	t.Helper()
	start := strings.Index(doc, "## Unreleased")
	if start < 0 {
		t.Fatal("CHANGELOG.md 缺 ## Unreleased 段落")
	}
	section := doc[start:]
	if next := strings.Index(section[len("## Unreleased"):], "\n## "); next >= 0 {
		section = section[:len("## Unreleased")+next]
	}
	return section
}

// topLevelBullets 把一段 Markdown 拆成若干**顶层无序列表项**（每项含其缩进续行），供「整条 bullet」判断。
func topLevelBullets(section string) []string {
	var bullets []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			bullets = append(bullets, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, ln := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(ln, "- "):
			flush()
			cur = []string{ln}
		case len(cur) > 0 && strings.HasPrefix(ln, "  "):
			cur = append(cur, ln) // 缩进续行属于当前 bullet
		default:
			flush()
		}
	}
	flush()
	return bullets
}

// TestDocsV2_ChangelogUnreleased：CHANGELOG.md 的 Unreleased 段落如实登记 Schema v2；并**修正分类**：
//   - plan_version 2 不得标 Breaking——按**整条 bullet**判断（不是同一行），且该 bullet 须写成
//     additive/compatibility；
//   - context 三字段并存/弃用是接口变更但**不立即断裂**；
//   - index schema2 仅可标「派生存储 schema」breaking，且明确无权威数据迁移；
//   - candidates I1 由 eg context 无条件发出。
//
// 且不得新增 0.7.0-m7 发布标题（版本推进留 T8-4）。
func TestDocsV2_ChangelogUnreleased(t *testing.T) {
	doc := readDocs(t, docsCHANGELOG)
	section := unreleasedSection(t, doc)
	// 关键项登记：opinion/kind/knowledge 用**一条精确字面量**覆盖，避免散词重复。
	for _, must := range []string{
		"plan_version", "schema_version", "candidates", "0.8.0",
		"`eg search --kind knowledge|opinion|all`",
	} {
		if !strings.Contains(section, must) {
			t.Fatalf("CHANGELOG Unreleased 未登记 Schema v2 关键项 %q", must)
		}
	}

	bullets := topLevelBullets(section)
	// 分类修正：**含 plan_version 的整条 bullet** 都不得标 Breaking；且其中至少一条写成 additive/compatibility。
	planBulletHasAdditive := false
	sawPlanBullet := false
	for _, b := range bullets {
		if !strings.Contains(b, "plan_version") {
			continue
		}
		sawPlanBullet = true
		if strings.Contains(b, "Breaking") {
			t.Fatalf("CHANGELOG Unreleased 把含 plan_version 的整条 bullet 标为 Breaking：\n%s", strings.TrimSpace(b))
		}
		if strings.Contains(b, "additive schema change with compatibility") {
			planBulletHasAdditive = true
		}
	}
	if !sawPlanBullet {
		t.Fatal("CHANGELOG Unreleased 未找到含 plan_version 的 bullet")
	}
	if !planBulletHasAdditive {
		t.Fatal("CHANGELOG Unreleased 未把 plan_version 2 写成 additive/compatibility")
	}

	// index schema2：breaking 仅限派生存储 schema，且明确无权威数据迁移。
	if !strings.Contains(section, "Breaking (derived storage schema only)") {
		t.Fatal("CHANGELOG Unreleased 未把 index schema2 的 breaking 限定为「派生存储 schema」")
	}
	if !strings.Contains(section, "No authoritative data migration") {
		t.Fatal("CHANGELOG Unreleased 未写明 index schema2 无权威数据迁移")
	}
	// context：接口变更但不立即断裂 + I1 由 eg context 无条件发出。
	if !strings.Contains(section, "no caller breaks now") {
		t.Fatal("CHANGELOG Unreleased 未写明 context 三字段并存不立即断裂（no caller breaks now）")
	}
	if !strings.Contains(section, "unconditionally emits exactly one `I1`") {
		t.Fatal("CHANGELOG Unreleased 未写明 candidates I1 由 eg context 无条件发出")
	}
	// 版本推进（0.7.0-m7 发布标题）不属本批。
	if regexp.MustCompile(`(?m)^##\s+0\.7\.0-m7\b`).MatchString(doc) {
		t.Fatal("CHANGELOG 出现 0.7.0-m7 发布标题（版本推进留 T8-4，本批仅登记 Unreleased）")
	}
}

// TestDocsV2_InstallSchemaSummary：INSTALL.md 的「Schema v2 summary」小节内简洁登记安装后需要知道的
// Schema v2 兼容 / 验证摘要——当前/兼容 plan 版本、index schema v2、context 双候选 + legacy、四实体/两目录、
// canonical op / 别名迁移、search 默认，以及 candidates I1 由 eg context 无条件发出。
func TestDocsV2_InstallSchemaSummary(t *testing.T) {
	doc := readDocs(t, docsINSTALL)
	sec := sectionBetween(t, doc, "## Schema v2 summary", "## Review and logical deletion")
	for _, must := range []string{
		"plan_version", "{1, 2}", "schema_version", "knowledge_candidates", "opinion_candidates",
		"opinions/", "create_knowledge", "create_opinion", "--kind knowledge",
	} {
		if !strings.Contains(sec, must) {
			t.Fatalf("INSTALL.md 的 Schema v2 summary 小节缺摘要项 %q", must)
		}
	}
	if !strings.Contains(sec, "unconditionally emits exactly one `I1`") {
		t.Fatal("INSTALL.md 的 Schema v2 summary 未写明 candidates I1 由 eg context 无条件发出")
	}
}

// TestDocsV2_NoStaleNoteSections：只在**当前 Note 模板句**里禁止旧 v1 笔记分区名（材料提炼 / Agent 分析 /
// 产出知识卡）；其后的独立兼容说明段落则必须如实登记三旧名，且写明「兼容映射 + 按字节 / byte-for-byte
// 原样保留」语义（避免把旧名一律清零，也避免只列名而丢掉保真语义）。
func TestDocsV2_NoStaleNoteSections(t *testing.T) {
	for _, p := range docsREADMEs() {
		a := anchorsFor(t, p)
		doc := readDocs(t, p)
		noteTpl := sectionBetween(t, doc, a.noteTplStart, a.noteTplEnd)
		for _, stale := range a.staleNoteSections {
			if strings.Contains(noteTpl, stale) {
				t.Fatalf("%s 的当前 Note 模板句仍列旧 v1 分区名「%s」（须只保留四分区：整理正文/提取结果/存疑与待验证/用户补充）", p, stale)
			}
		}
		compat := sectionBetween(t, doc, a.noteCompatStart, a.noteCompatEnd)
		for _, stale := range a.staleNoteSections {
			if !strings.Contains(compat, stale) {
				t.Fatalf("%s 的 v1 Note 兼容说明段缺旧分区名「%s」", p, stale)
			}
		}
		if !strings.Contains(compat, a.noteCompatMapPhrase) {
			t.Fatalf("%s 的 v1 Note 兼容说明段未写明「兼容映射」语义（缺 %q）", p, a.noteCompatMapPhrase)
		}
		if !strings.Contains(compat, a.noteCompatBytePhrase) {
			t.Fatalf("%s 的 v1 Note 兼容说明段未写明「按字节 / byte-for-byte 原样保留」语义（缺 %q）", p, a.noteCompatBytePhrase)
		}
	}
}

// TestDocsV2_SelfCheckMutations：定向 mutation/self-check——把**当前章节 / 当前 JSON**里的关键事实故意
// 改坏，必须让对应判据翻红。关键是：这里调用的是**正式断言用的同一份 validator**（vPlanVersionTwo /
// vOpinionCandidates / vKnowledgeThree / vWriteNoteFidelity），所以证明的是正式判据本身、不是简化 lambda。
// 覆盖：
//
//	(a) plan_version 2 → 1；
//	(b) eg context 删掉 opinion_candidates；
//	(c) 模板小节 Knowledge 三分区 → 五分区；
//	(d) write_note 来源 body 删前缀 / 删后缀（逐字保真的反向证明）。
func TestDocsV2_SelfCheckMutations(t *testing.T) {
	for _, a := range readmeAnchorTable() {
		doc := readDocs(t, a.path)

		// (a) plan_version。
		plan := planJSONBlock(t, a.path)
		if err := vPlanVersionTwo(plan); err != nil {
			t.Fatalf("%s: 自检基线异常，当前 plan 块未过 vPlanVersionTwo：%v", a.path, err)
		}
		if vPlanVersionTwo(planV2RE.ReplaceAllString(plan, `"plan_version": 1`)) == nil {
			t.Fatalf("%s: 自检失效——plan_version 2→1 后 vPlanVersionTwo 仍绿", a.path)
		}

		// (b) opinion_candidates。
		ctx := contextJSONBlock(t, a.path)
		if err := vOpinionCandidates(ctx); err != nil {
			t.Fatalf("%s: 自检基线异常，ctx 块未过 vOpinionCandidates：%v", a.path, err)
		}
		if vOpinionCandidates(strings.Replace(ctx, `"opinion_candidates"`, `"legacy_only"`, 1)) == nil {
			t.Fatalf("%s: 自检失效——删 opinion_candidates 后 vOpinionCandidates 仍绿", a.path)
		}

		// (c) Knowledge 三分区。
		tpl := sectionBetween(t, doc, a.templatesStart, a.templatesEnd)
		if err := vKnowledgeThree(tpl, a.knowledgeThree, a.knowledgeFive); err != nil {
			t.Fatalf("%s: 自检基线异常，模板小节未过 vKnowledgeThree：%v", a.path, err)
		}
		if vKnowledgeThree(strings.Replace(tpl, a.knowledgeThree, a.knowledgeFive, 1), a.knowledgeThree, a.knowledgeFive) == nil {
			t.Fatalf("%s: 自检失效——Knowledge 三分区→五分区后 vKnowledgeThree 仍绿", a.path)
		}

		// (d) write_note 来源 body 逐字保真：删前缀 / 删后缀都必须翻红。
		capture := captureBodyLines(t, a.path)
		if err := vWriteNoteFidelity(plan, capture); err != nil {
			t.Fatalf("%s: 自检基线异常，plan 块未过 vWriteNoteFidelity：%v", a.path, err)
		}
		r := []rune(capture[0])
		if len(r) < 8 {
			t.Fatalf("%s: capture[0] 过短，无法构造删前后缀 mutation", a.path)
		}
		prefixMut := strings.Replace(plan, capture[0], string(r[3:]), 1) // 删前 3 个字符
		if vWriteNoteFidelity(prefixMut, capture) == nil {
			t.Fatalf("%s: 自检失效——来源 body 删前缀后 vWriteNoteFidelity 仍绿", a.path)
		}
		suffixMut := strings.Replace(plan, capture[0], string(r[:len(r)-3]), 1) // 删后 3 个字符
		if vWriteNoteFidelity(suffixMut, capture) == nil {
			t.Fatalf("%s: 自检失效——来源 body 删后缀后 vWriteNoteFidelity 仍绿", a.path)
		}
	}
}
