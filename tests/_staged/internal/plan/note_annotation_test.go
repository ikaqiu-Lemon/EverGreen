package plan

// note_annotation_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-3
// 批注词表 + 机器锚点 + 渲染的**校验层与落盘形态**验收（Schema v2 契约 §4.2 第 4 条 /
// §4.2.2 / §5.1 / D-10）。
//
// 本批只加严 plan_version: 2 且 blocks[] 给出的 write_note：
//   ① source 块只用 source_ref；agent 块只用 annotation（agent annotation 必须非空），
//      两类字段不得混用——每条错误逐项定位到 ops[i].blocks[j].<字段>，整条 write_note 零 action；
//   ② 内置七类固定标签单一真源、声明序固定，渲染 `> **[Agent <固定中文>]**`，
//      内置 key 的 label trim 后非空即 E2；
//   ③ 扩展 annotation 必须匹配 ^[a-z][a-z0-9_-]{0,31}$ 且 label trim 后非空，
//      合法扩展按原 label 渲染；非法 key / 缺 label 均 E2；
//   ④ 每个 block 有一个版本化单行 HTML 注释机器锚点，删除锚点与 agent 块后 source 完整。
//
// 判据全部落到**诊断字段路径 + 零写入**或**落盘字节**上，不用「函数被调用过」这类间接证据。

import (
	"strings"
	"testing"
)

// —— 夹具 ——

// annSource 造一份 Source：body 逐字落在 frontmatter 之后（物理行布局由调用方决定）。
func annSource(body string) string {
	return "---\nid: s-20260901-ann\nurl: https://example.com/a\ntitle: 批注夹具\n" +
		"saved_at: '2026-09-01T10:00:00+08:00'\n---\n" + body
}

func annFiles(body string) map[string]string {
	return map[string]string{"sources/s-20260901-ann.md": annSource(body)}
}

// annNote 造一条 v2 write_note op（omissions 显式空数组，声明无删除）。
func annNote(blocks ...string) string {
	return `{"op":"write_note","source":"s-20260901-ann","note_id":"n-20261017-ann","blocks":[` +
		strings.Join(blocks, ",") + `],"omissions":[]}`
}

// annValidate 用给定 Source body 与 blocks 跑一次 v2 校验。
func annValidate(t *testing.T, body string, blocks ...string) *Result {
	t.Helper()
	files := annFiles(body)
	return run(t, vault(t, files), v2Plan(t, files, annNote(blocks...)))
}

const annNoteRel = "domains/ai-infra/notes/n-20261017-ann.md"

// srcJSON / agentJSON 是**原样字段**的块构造器：允许注入契约禁止的字段组合，
// 这正是本批要拦的错误形态（不能用只会造合法块的 srcBlock / agentBlock）。
func srcJSON(fields string) string   { return "{" + `"role":"source",` + fields + "}" }
func agentJSON(fields string) string { return "{" + `"role":"agent",` + fields + "}" }

// —— ① 字段互斥：每条错误逐项定位到 blocks[j].<字段>，整条 write_note 零 action ——

// TestSourceBlockRejectsAnnotationField —— source 块携 annotation → blocks[i].annotation 处 E2、零写入。
func TestSourceBlockRejectsAnnotationField(t *testing.T) {
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一","annotation":"supplement"`))
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].annotation")
	if !strings.Contains(d.Message, "source") {
		t.Fatalf("诊断文案应点出 source 块不得带 annotation，实得：%s", d.Message)
	}
	assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
}

// TestSourceBlockRejectsLabelField —— source 块携 label → blocks[i].label 处 E2、零写入。
func TestSourceBlockRejectsLabelField(t *testing.T) {
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一","label":"我的标签"`))
	requireErrorAt(t, res, E2, "ops[0].blocks[0].label")
	assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
}

// TestAgentBlockRejectsSourceRefField —— agent 块携 source_ref → blocks[i].source_ref 处 E2、零写入。
func TestAgentBlockRejectsSourceRefField(t *testing.T) {
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
		agentJSON(`"annotation":"supplement","body":"补充","source_ref":"L2-L2"`))
	requireErrorAt(t, res, E2, "ops[0].blocks[1].source_ref")
	assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
}

// TestAgentBlockRequiresAnnotation —— agent 块缺 annotation → blocks[i].annotation 处 E2、零写入。
func TestAgentBlockRequiresAnnotation(t *testing.T) {
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
		agentJSON(`"body":"补充"`))
	requireErrorAt(t, res, E2, "ops[0].blocks[1].annotation")
	assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
}

// TestSourceBlockRejectsAnnotationAndLabelTogether —— 同一 source 块同时携 annotation 与 label：
// 逐项校验**不首错短路**，data.errors 里必须同时出现 blocks[i].annotation 与 blocks[i].label
// 两条字段级 E2（作者一次看清所有要改的字段，而不是改一个再撞下一个），整条 write_note 零写入。
func TestSourceBlockRejectsAnnotationAndLabelTogether(t *testing.T) {
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一","annotation":"supplement","label":"我的标签"`))
	requireErrorAt(t, res, E2, "ops[0].blocks[0].annotation")
	requireErrorAt(t, res, E2, "ops[0].blocks[0].label")
	assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
}

// TestAgentBlockRejectsSourceRefAndMissingAnnotationTogether —— 同一 agent 块同时「携 source_ref」
// 且「缺 annotation」：两处违规必须同时钉出——blocks[i].source_ref 与 blocks[i].annotation 各一条
// 字段级 E2（source_ref 判定不得吞掉后续 annotation 必填校验），整条 write_note 零写入。
func TestAgentBlockRejectsSourceRefAndMissingAnnotationTogether(t *testing.T) {
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
		agentJSON(`"body":"补充","source_ref":"L2-L2"`))
	requireErrorAt(t, res, E2, "ops[0].blocks[1].source_ref")
	requireErrorAt(t, res, E2, "ops[0].blocks[1].annotation")
	assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
}

// TestBuiltinAnnotationRejectsLabelOverride —— 内置 key 携非空 label → blocks[i].label 处 E2、零写入。
func TestBuiltinAnnotationRejectsLabelOverride(t *testing.T) {
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
		agentJSON(`"annotation":"distinction","label":"擅自改写","body":"辨析"`))
	requireErrorAt(t, res, E2, "ops[0].blocks[1].label")
	assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
}

// —— ③ 扩展 key 形态：首字母 / 大写 / 非法字符 / 33 字符 / 缺 label ——

// TestExtensionAnnotationKeyErrors —— 非法扩展 key 一律 blocks[i].annotation 处 E2、零写入。
func TestExtensionAnnotationKeyErrors(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"首字母数字", "1custom"},
		{"含大写", "Custom"},
		{"非法字符点", "a.b"},
		{"非法字符空格", "a b"},
		{"33字符超界", "a" + strings.Repeat("b", 32)}, // 共 33 字符，越过 1+31 上界
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := annValidate(t, "正文一\n",
				srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
				agentJSON(`"annotation":"`+c.key+`","label":"人读标签","body":"批注"`))
			requireErrorAt(t, res, E2, "ops[0].blocks[1].annotation")
			assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
		})
	}
}

// TestExtensionAnnotationRequiresLabel —— 合法扩展 key 但 label 缺 / 全空白 → blocks[i].label 处 E2、零写入。
func TestExtensionAnnotationRequiresLabel(t *testing.T) {
	cases := []struct {
		name   string
		fields string
	}{
		{"缺 label", `"annotation":"custom_note","body":"批注"`},
		{"空白 label", `"annotation":"custom_note","label":"   ","body":"批注"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := annValidate(t, "正文一\n",
				srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
				agentJSON(c.fields))
			requireErrorAt(t, res, E2, "ops[0].blocks[1].label")
			assertZeroWrite(t, annFiles("正文一\n"), res, annNoteRel)
		})
	}
}

// —— ② 内置七类固定标签渲染（声明序 + 固定中文），③ 合法扩展按原 label 渲染 ——

// TestBuiltinAnnotationsRenderFixedLabels —— 七类内置批注各按固定中文标签渲染 `> **[Agent X]** `。
//
// 逐类型独立执行：任何一类被降级为 `[Agent 补充]`（I-008 的历史缺陷）都会当场判红。
func TestBuiltinAnnotationsRenderFixedLabels(t *testing.T) {
	cases := []struct {
		annotation string
		label      string // 契约 §4.2.2 固定中文
	}{
		{"guide", "导读"},
		{"supplement", "补充"},
		{"emphasis", "强调"},
		{"summary", "总结"},
		{"distinction", "辨析"},
		{"verification", "待验证"},
		{"reflection", "反思"},
	}
	for _, c := range cases {
		t.Run(c.annotation, func(t *testing.T) {
			files := annFiles("正文一\n")
			res := annValidate(t, "正文一\n",
				srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
				agentJSON(`"annotation":"`+c.annotation+`","body":"批注文本"`))
			dir, out := execOn(t, files, res)
			if len(out.Written) == 0 {
				t.Fatal("write_note 必须真实落盘")
			}
			raw := readVaultFile(t, dir, annNoteRel)
			want := "> **[Agent " + c.label + "]** 批注文本"
			if !strings.Contains(raw, want) {
				t.Fatalf("内置 %s 应渲染固定标签 %q，实得：\n%s", c.annotation, want, raw)
			}
			// 不得把该类批注降级为「补充」（除非它本就是 supplement）。
			if c.annotation != "supplement" && strings.Contains(raw, "> **[Agent 补充]**") {
				t.Fatalf("内置 %s 被降级为「补充」：\n%s", c.annotation, raw)
			}
		})
	}
}

// TestExtensionAnnotationRendersLabel —— 合法扩展 key 携非空 label，按 label 原样渲染。
func TestExtensionAnnotationRendersLabel(t *testing.T) {
	files := annFiles("正文一\n")
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
		agentJSON(`"annotation":"case_study","label":"案例研究","body":"扩展批注"`))
	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("write_note 必须真实落盘")
	}
	raw := readVaultFile(t, dir, annNoteRel)
	want := "> **[Agent 案例研究]** 扩展批注"
	if !strings.Contains(raw, want) {
		t.Fatalf("合法扩展应按 label 渲染 %q，实得：\n%s", want, raw)
	}
}

// TestExtensionAnnotationKeyUpperBoundLegal —— 恰 32 字符的扩展 key 合法（1 首字母 + 31 尾字符，
// 命中正则 ^[a-z][a-z0-9_-]{0,31}$ 的**上界**）：与 TestExtensionAnnotationKeyErrors 的 33 字符负例
// 对称，正反两侧夹住上界、钉死没有 off-by-one。校验零 error，且按 label 原样落盘渲染。
func TestExtensionAnnotationKeyUpperBoundLegal(t *testing.T) {
	key := "a" + strings.Repeat("b", 31) // 共 32 字符，恰在 1+31 上界（33 字符即越界，见负例）
	if len(key) != 32 {
		t.Fatalf("夹具口径错：边界 key 应为 32 字符，实得 %d", len(key))
	}
	files := annFiles("正文一\n")
	res := annValidate(t, "正文一\n",
		srcJSON(`"source_ref":"L1-L1","body":"正文一"`),
		agentJSON(`"annotation":"`+key+`","label":"边界标签","body":"扩展批注"`))
	requireNoError(t, res)
	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("合法 32 字符扩展 key 的 write_note 必须真实落盘")
	}
	raw := readVaultFile(t, dir, annNoteRel)
	if want := "> **[Agent 边界标签]** 扩展批注"; !strings.Contains(raw, want) {
		t.Fatalf("32 字符扩展 key 应按 label 渲染 %q，实得：\n%s", want, raw)
	}
}

// —— ④ 机器锚点：每个 block 一个版本化单行 HTML 注释，删除锚点与 agent 块后 source 完整 ——

// TestReviewAnchorsAreEmittedAndRemovable —— 整理正文含机器锚点；剔除全部 HTML 注释锚点
// 与整段 agent blockquote 后，来源 heading / 正文按顺序完整保留。
func TestReviewAnchorsAreEmittedAndRemovable(t *testing.T) {
	// L1 / L2 两非空行各由一个 source 块覆盖，中间插一个 agent 块。
	body := "第一节正文。\n第二节正文。\n"
	files := annFiles(body)
	res := annValidate(t, body,
		srcJSON(`"source_ref":"L1-L1","heading":"第一节","body":"第一节正文。"`),
		agentJSON(`"annotation":"emphasis","body":"这里是重点。"`),
		srcJSON(`"source_ref":"L2-L2","heading":"第二节","body":"第二节正文。"`))
	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("write_note 必须真实落盘")
	}
	raw := readVaultFile(t, dir, annNoteRel)
	noteBody := noteReviewSection(t, raw)

	// 机器锚点必须存在（单行 HTML 注释形态）。
	if !strings.Contains(noteBody, "<!--") {
		t.Fatalf("整理正文缺机器锚点（单行 HTML 注释）：\n%s", noteBody)
	}

	// 剔除全部 HTML 注释锚点行与整段 agent blockquote 行后，来源正文必须完整、有序。
	var kept []string
	for _, line := range strings.Split(noteBody, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->") {
			continue // 机器锚点
		}
		if strings.HasPrefix(trimmed, ">") {
			continue // agent blockquote
		}
		kept = append(kept, line)
	}
	rest := strings.Join(kept, "\n")
	for _, must := range []string{"### 第一节", "第一节正文。", "### 第二节", "第二节正文。"} {
		if !strings.Contains(rest, must) {
			t.Fatalf("剔除锚点与 agent 块后来源 %q 丢失：\n%s", must, rest)
		}
	}
	if strings.Contains(rest, "这里是重点。") {
		t.Fatalf("agent 批注未随 blockquote 一并剔除：\n%s", rest)
	}
	// 顺序：第一节必须在第二节之前。
	if strings.Index(rest, "### 第一节") > strings.Index(rest, "### 第二节") {
		t.Fatalf("来源块顺序被打乱：\n%s", rest)
	}
}

// noteReviewSection 从落盘笔记里切出「整理正文」分区正文（到下一个 H2 之前）。
func noteReviewSection(t *testing.T, raw string) string {
	t.Helper()
	head := "## 整理正文\n"
	at := strings.Index(raw, head)
	if at < 0 {
		t.Fatalf("落盘笔记缺「整理正文」分区：\n%s", raw)
	}
	body := raw[at+len(head):]
	if next := strings.Index(body, "\n## "); next >= 0 {
		body = body[:next+1]
	}
	return body
}
