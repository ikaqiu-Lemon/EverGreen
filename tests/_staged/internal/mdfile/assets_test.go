package mdfile

// assets_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-2B
// 结构资产扫描器 ScanAssets 的判据（Schema v2 契约 §4.2.1 第 5 条：图片 URL、图注、代码块内容、
// 表格行、列表项、引用、链接、脚注八类的稳定签名与相对顺序；原始 HTML / 无法可靠解析的资产形态
// fail closed）。本文件是 mdfile 层的单元判据；plan 层按来源范围逐块比对的验收判据见
// tests/_staged/internal/plan/note_fidelity_test.go。

import (
	"strings"
	"testing"
)

// sigs 把事件序列渲染成 `kind|sig` 行（用于顺序 / 数量 / 签名的整体断言）。
func sigs(t *testing.T, body string) []string {
	t.Helper()
	evs, err := ScanAssets([]byte(body))
	if err != nil {
		t.Fatalf("ScanAssets(%q) 意外报错：%v", body, err)
	}
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, string(e.Kind)+"|"+e.Sig)
	}
	return out
}

func requireSeq(t *testing.T, body string, want ...string) {
	t.Helper()
	got := sigs(t, body)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("资产序列不符\n  body=%q\n  want=%v\n  got =%v", body, want, got)
	}
}

// requireScanError 断言 body 触发结构扫描错误（fail closed），并落在期望物理行。
func requireScanError(t *testing.T, body string, wantLine int) {
	t.Helper()
	_, err := ScanAssets([]byte(body))
	if err == nil {
		t.Fatalf("期望结构扫描错误，实际 nil：body=%q", body)
	}
	se, ok := err.(*AssetScanError)
	if !ok {
		t.Fatalf("期望 *AssetScanError，实得 %T：%v", err, err)
	}
	if wantLine > 0 && se.Line != wantLine {
		t.Fatalf("结构扫描错误定位行不符：want L%d，got L%d（%v）", wantLine, se.Line, err)
	}
}

// —— ① 八类合法资产的签名 ——

func TestScanImageAndCaption(t *testing.T) {
	// 有 alt → image + caption；无 alt → 仅 image（图注按存在性计数）。
	requireSeq(t, "![说明](pic.png)\n", "image|image|pic.png", "caption|caption")
	requireSeq(t, "![](pic.png)\n", "image|image|pic.png")
}

func TestScanLinkAndAutolink(t *testing.T) {
	requireSeq(t, "见 [文档](https://ex.com/a)。\n", "link|link|https://ex.com/a")
	// 裸 autolink 也计 link。
	requireSeq(t, "裸链 https://bare.example.com 结束\n", "link|link|https://bare.example.com")
}

func TestScanImageNotDoubleCountedAsLink(t *testing.T) {
	// 一图一链：恰一个 image（+caption）与一个 link，图片绝不双计为 link。
	requireSeq(t, "![图](p.png) 和 [链](https://ex.com/x)\n",
		"image|image|p.png", "caption|caption", "link|link|https://ex.com/x")
}

func TestScanImageInsideLink(t *testing.T) {
	// [![alt](i.png)](u)：link 目标 u + 内部 image i.png（图片仍以 image 计、不双计 link）。
	requireSeq(t, "[![alt](i.png)](https://ex.com/u)\n",
		"link|link|https://ex.com/u", "image|image|i.png", "caption|caption")
}

func TestScanCodeFencedAndIndented(t *testing.T) {
	// fenced 与 indented 都归 code，签名只锁 payload（围栏字符 / info 不进签名）。
	requireSeq(t, "```go\nx := 1\n```\n", "code|code|x := 1\n")
	requireSeq(t, "    x := 1\n    y := 2\n", "code|code|x := 1\ny := 2\n")
}

func TestScanCodeNormalizesCRLFOnly(t *testing.T) {
	// 只规范 CRLF→LF；payload 其余精确，故 CRLF 版与 LF 版签名一致。
	if a, b := sigs(t, "```\nA=1\nB=2\n```\n"), sigs(t, "```\r\nA=1\r\nB=2\r\n```\r\n"); strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatalf("CRLF 规范化后签名应一致：lf=%v crlf=%v", a, b)
	}
}

func TestScanTableRows(t *testing.T) {
	requireSeq(t, "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
		"table_row|table_row|header|2", "table_row|table_row|body|2")
}

func TestScanListItemsDepthAndOrdering(t *testing.T) {
	// 无序两项 + 一层嵌套；再一段有序两项。
	requireSeq(t, "- 甲\n- 乙\n  - 丙\n\n1. x\n2. y\n",
		"list_item|list_item|1|unordered",
		"list_item|list_item|1|unordered",
		"list_item|list_item|2|unordered",
		"list_item|list_item|1|ordered",
		"list_item|list_item|1|ordered")
}

func TestScanBlockquoteDepth(t *testing.T) {
	requireSeq(t, "> 甲\n> > 乙\n", "blockquote|blockquote|1", "blockquote|blockquote|2")
}

func TestScanFootnoteRefAndDef(t *testing.T) {
	// 引用与定义都计脚注，签名含引用 / 定义身份 + label；顺序按物理行（ref 先于 def）。
	requireSeq(t, "正文[^n] 结束。\n\n[^n]: 定义文字\n",
		"footnote|footnote|ref|n", "footnote|footnote|def|n")
}

// —— ② 屏蔽 / 互换 / 转义 ——

func TestScanInlineCodeMasksPseudoSyntax(t *testing.T) {
	// 行内代码里的伪图片 / 伪链接语法被屏蔽，不计资产。
	requireSeq(t, "看 `![x](y)` 与 `[a](b)` 都不算\n")
}

func TestScanEscapedPseudoSyntaxNotCounted(t *testing.T) {
	// 转义的方括号不构成链接 / 图片。
	requireSeq(t, "转义 \\[非链接\\](x) 与 \\!\\[非图\\](y)\n")
}

func TestScanInlineReferenceImageInterchange(t *testing.T) {
	// inline 与 reference 写法目标一致即等价（都解析到 refimg.png）。
	inline := sigs(t, "![甲](refimg.png)\n")
	ref := sigs(t, "![乙][r]\n\n[r]: refimg.png\n")
	if strings.Join(inline, "|") != strings.Join(ref, "|") {
		t.Fatalf("inline 与 reference 图片目标一致时签名应等价：inline=%v ref=%v", inline, ref)
	}
}

// —— ③ fail closed ——

func TestScanRawHTMLBlockAssetFailsClosed(t *testing.T) {
	requireScanError(t, "<img src=\"r.png\">\n", 1)
	requireScanError(t, "<table><tr><td>x</td></tr></table>\n", 1)
}

func TestScanRawHTMLInlineAssetFailsClosed(t *testing.T) {
	requireScanError(t, "文字 <a href=\"z\">链接</a> 更多\n", 1)
	requireScanError(t, "前缀\n\n段落含 <img src=\"x\"> 图\n", 3)
}

func TestScanBenignRawHTMLNotFailed(t *testing.T) {
	// 非资产标签的原始 HTML（换行 / 注释）不触发 fail closed，也不产出资产。
	requireSeq(t, "一段<br>文字\n")
	requireSeq(t, "<!-- 注释 -->\n\n正文\n")
}

// —— ④ 顺序与行区间 ——

func TestScanOrderedBySourceLine(t *testing.T) {
	// 脚注定义在 AST 里被搬到文末，但事件按真实物理行归位（def 在 code 之后、ref 在最前）。
	evs, err := ScanAssets([]byte("引用[^a] 见下。\n\n```\nk=1\n```\n\n[^a]: 末尾定义\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 {
		t.Fatalf("期望 3 个事件，实得 %d：%+v", len(evs), evs)
	}
	if evs[0].Kind != AssetFootnote || evs[0].Sig != "footnote|ref|a" {
		t.Fatalf("首事件应为脚注引用，实得 %+v", evs[0])
	}
	if evs[1].Kind != AssetCode {
		t.Fatalf("次事件应为 code，实得 %+v", evs[1])
	}
	if evs[2].Kind != AssetFootnote || evs[2].Sig != "footnote|def|a" || evs[2].StartLine != 7 {
		t.Fatalf("末事件应为 L7 的脚注定义，实得 %+v", evs[2])
	}
}

func TestScanFencedCodeSpanIncludesFences(t *testing.T) {
	// 围栏代码的行区间含起止围栏行（供 plan 侧检出「区间切进代码块」）。
	evs, err := ScanAssets([]byte("```\nA\nB\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != AssetCode {
		t.Fatalf("期望单个 code 事件，实得 %+v", evs)
	}
	if evs[0].StartLine != 1 || evs[0].EndLine != 4 {
		t.Fatalf("围栏代码区间应含起止围栏 L1-L4，实得 L%d-L%d", evs[0].StartLine, evs[0].EndLine)
	}
}

func TestScanEmptyAndNoAsset(t *testing.T) {
	requireSeq(t, "")
	requireSeq(t, "只是一段没有任何结构资产的纯文字。\n")
}

// —— ⑤ 表格：真实行 span 与整表「完整归属范围」scope ——

// TestScanTableRowRealLineAndScope —— 表格每一行事件的**真实 span** 取所在表头 / 数据行的真实
// 物理行（表头 L1、数据 L3 / L4），而**完整归属范围**（scope）取整张 Table 容器（L1-L4，含
// delimiter L2）。真实行用于排序、scope 用于「整表被拆开」的切断检测，两者拆开互不干扰。
func TestScanTableRowRealLineAndScope(t *testing.T) {
	// L1 表头 / L2 delimiter / L3 数据一 / L4 数据二。
	evs, err := ScanAssets([]byte("| a | b |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 {
		t.Fatalf("期望 3 行事件（表头 + 两数据），实得 %d：%+v", len(evs), evs)
	}
	wantReal := []struct{ s, e int }{{1, 1}, {3, 3}, {4, 4}}
	for i, e := range evs {
		if e.Kind != AssetTableRow {
			t.Fatalf("期望 table_row，实得 %+v", e)
		}
		if e.StartLine != wantReal[i].s || e.EndLine != wantReal[i].e {
			t.Fatalf("第 %d 行真实 span 应为 L%d-L%d，实得 L%d-L%d",
				i, wantReal[i].s, wantReal[i].e, e.StartLine, e.EndLine)
		}
		if e.ScopeStartLine != 1 || e.ScopeEndLine != 4 {
			t.Fatalf("第 %d 行 scope 应为整表 L1-L4（含 delimiter），实得 L%d-L%d",
				i, e.ScopeStartLine, e.ScopeEndLine)
		}
	}
}

// TestScanTableInnerLinkOrdering —— 表头 / 数据行里的 link 必须按**真实行**与本行事件相邻排序，
// 不被排到所有 row 事件之后（否则资产从一行移到另一行会假绿）：数据行一的 link 紧跟数据行一的
// row 事件，数据行二的 link 紧跟数据行二的 row 事件。
func TestScanTableInnerLinkOrdering(t *testing.T) {
	// L1 表头 / L2 delimiter / L3 含 link1 / L4 含 link2。
	evs, err := ScanAssets([]byte(
		"| a | b |\n| --- | --- |\n| [x](http://e/1) | c |\n| d | [y](http://e/2) |\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(evs))
	for _, e := range evs {
		got = append(got, string(e.Kind)+"|"+e.Sig)
	}
	want := []string{
		"table_row|table_row|header|2",
		"table_row|table_row|body|2",
		"link|link|http://e/1",
		"table_row|table_row|body|2",
		"link|link|http://e/2",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("表内 link 应按真实行紧邻其行事件排序\n  want=%v\n  got =%v", want, got)
	}
	// link1 真实行 L3、link2 真实行 L4（与所在数据行一致）。
	if evs[2].StartLine != 3 || evs[4].StartLine != 4 {
		t.Fatalf("表内 link 真实行应为 L3 / L4，实得 L%d / L%d", evs[2].StartLine, evs[4].StartLine)
	}
}

// —— ⑥ 空围栏 / 未闭合围栏的精确定位与 fail closed ——

// TestScanEmptyFencedCodeLineRange —— 前置多行后的**空围栏代码块**不退化到 fbLine=1：
// 用真实开围栏行定位，区间从 opener 扫到匹配 closer。
func TestScanEmptyFencedCodeLineRange(t *testing.T) {
	// L1 前置一 / L2 前置二 / L3 空 / L4 开围栏 / L5 闭围栏 / L6 尾。
	evs, err := ScanAssets([]byte("前置一\n前置二\n\n```\n```\n尾\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Kind != AssetCode {
		t.Fatalf("期望单个 code 事件，实得 %+v", evs)
	}
	if evs[0].StartLine != 4 || evs[0].EndLine != 5 {
		t.Fatalf("空围栏应精确定位到 L4-L5（不退化到 L1），实得 L%d-L%d", evs[0].StartLine, evs[0].EndLine)
	}
	if evs[0].Sig != "code|" {
		t.Fatalf("空围栏 payload 应为空，实得 sig=%q", evs[0].Sig)
	}
}

// TestScanUnclosedFenceFailsClosed —— 未闭合的围栏代码块必须 *AssetScanError（不得当合法 code），
// 定位到开围栏行。
func TestScanUnclosedFenceFailsClosed(t *testing.T) {
	requireScanError(t, "```go\ncode 无收尾围栏\n", 1)
	requireScanError(t, "前置\n\n```\nx 无收尾\n", 3)
}

// —— ⑦ 看似资产却无法可靠解析：畸形 image/link 与悬空脚注 ——

// TestScanMalformedImageLinkFailsClosed —— 未转义的畸形 image/link（destination 未闭合）保守
// 预检并 fail closed。
func TestScanMalformedImageLinkFailsClosed(t *testing.T) {
	requireScanError(t, "文字 ![alt](未闭合目的地\n", 1)
	requireScanError(t, "见 [文](https://ex.com/x 少了右括号\n", 1)
}

// TestScanDanglingFootnoteFailsClosed —— 悬空 footnote 引用（无对应定义）fail closed。
func TestScanDanglingFootnoteFailsClosed(t *testing.T) {
	requireScanError(t, "见脚注[^x] 但通篇无定义。\n", 1)
}

// TestScanHTMLCommentNotAsset —— HTML 注释里的 img/a 文本只是注释、不是资产：不 fail closed、
// 也不产出资产（负控，避免把注释误报成原始 HTML 资产）。
func TestScanHTMLCommentNotAsset(t *testing.T) {
	requireSeq(t, "<!-- <img src=x> 与 <a href=y>链接</a> -->\n\n正文\n")
}

// TestScanLegalBracketsNotMisjudged —— 合法的普通 [text]（无定义的引用式写法退化为文本）与
// 转义语法不被误判为畸形资产。
func TestScanLegalBracketsNotMisjudged(t *testing.T) {
	requireSeq(t, "普通 [just text] 不是链接，也没有资产。\n")
	requireSeq(t, "转义 \\[非链接\\](x) 不构成畸形资产。\n")
	// 合法的 reference link（usage + definition）解析为 link，不误判。
	requireSeq(t, "见 [文档][r] 结束\n\n[r]: https://ex.com/r\n", "link|link|https://ex.com/r")
}

// —— ⑧ 脚注 label 规范化 ——

// TestScanFootnoteLabelNormalized —— label 按解析语义规范化（大小写折叠 + 内部空白折叠）：
// `[^Foo Bar]` 与 `[^foo  bar]` 归一为同一签名；真正改变 label 仍产生不同签名。
func TestScanFootnoteLabelNormalized(t *testing.T) {
	a := sigs(t, "见[^Foo Bar]。\n\n[^Foo Bar]: 定义\n")
	b := sigs(t, "见[^foo  bar]。\n\n[^foo  bar]: 定义\n")
	if strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatalf("大小写 / 空白差异的同一 label 应归一：a=%v b=%v", a, b)
	}
	if strings.Join(a, "|") != "footnote|footnote|ref|foo bar|footnote|footnote|def|foo bar" {
		t.Fatalf("规范化后签名不符：%v", a)
	}
	// 真正改变 label 仍不同。
	c := sigs(t, "见[^baz]。\n\n[^baz]: 定义\n")
	if strings.Join(a, "|") == strings.Join(c, "|") {
		t.Fatalf("不同 label 不应归一：a=%v c=%v", a, c)
	}
}

// —— ⑨ 容器（blockquote / 列表项）内围栏代码 ——

// TestScanBlockquoteFencedCode —— blockquote 内的合法围栏代码：goldmark 能解析（node.Pos 指向
// 去掉 `> ` 前缀后的真实开围栏字符），必须产出 code + 外层 blockquote 事件、且不 fail closed。
// 收尾围栏按容器上下文（`> ```）识别，代码区间含起止围栏（L1-L3）。
func TestScanBlockquoteFencedCode(t *testing.T) {
	requireSeq(t, "> ```\n> code\n> ```\n",
		"blockquote|blockquote|1", "code|code|code\n")
	evs, err := ScanAssets([]byte("> ```\n> code\n> ```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if evs[1].Kind != AssetCode || evs[1].StartLine != 1 || evs[1].EndLine != 3 {
		t.Fatalf("blockquote 内围栏代码应覆盖 L1-L3，实得 %+v", evs[1])
	}
}

// TestScanListItemFencedCode —— 列表项内的合法围栏代码（无序 / 有序）：产出 code + 外层 list_item
// 事件、不 fail closed。收尾围栏按列表续行缩进（>3 空格也可）识别。
func TestScanListItemFencedCode(t *testing.T) {
	requireSeq(t, "- ```\n  code\n  ```\n",
		"list_item|list_item|1|unordered", "code|code|code\n")
	requireSeq(t, "1. ```\n   code\n   ```\n",
		"list_item|list_item|1|ordered", "code|code|code\n")
}

// TestScanContainerFenceMasksMalformedInside —— 容器内围栏代码里的伪 malformed 片段（未闭合
// image/link）被按真实行区间屏蔽，不误报为畸形资产；整体只产出 code + 外层结构事件。
func TestScanContainerFenceMasksMalformedInside(t *testing.T) {
	requireSeq(t, "> ```\n> ![a](未闭合\n> ```\n",
		"blockquote|blockquote|1", "code|code|![a](未闭合\n")
}

// TestScanNestedUnclosedFenceFailsClosed —— 容器内**真正未闭合**的围栏（缺收尾围栏）不得因为
// 放宽容器前缀而被放过：blockquote / 列表项内未闭合围栏均 *AssetScanError，定位到开围栏行。
func TestScanNestedUnclosedFenceFailsClosed(t *testing.T) {
	requireScanError(t, "> ```\n> code 无收尾\n", 1)
	requireScanError(t, "- ```\n  code 无收尾\n", 1)
}

// —— ⑩ caret 起头的 image alt / link 文字不是脚注引用 ——

// TestScanCaretImageAltNotFootnote —— `![^ok](p.png)` 的 `[^ok]` 是图片 alt 文字、`[^ok](url)`
// 的 `[^ok]` 是链接文字（goldmark 均按 image/link 解析），不得被当成悬空脚注引用而误报。
func TestScanCaretImageAltNotFootnote(t *testing.T) {
	requireSeq(t, "![^ok](p.png)\n", "image|image|p.png", "caption|caption")
	requireSeq(t, "[^ok](https://ex.com/x)\n", "link|link|https://ex.com/x")
}

// TestScanCaretReferenceAltNotFootnote —— reference 写法里 caret 起头、后接**第二组方括号**的
// alt / 文字同样不是脚注引用：`![^ok][r]`（reference image，alt=`^ok`）解析为 image + caption；
// `[^ok][r]`（reference link，文字=`^ok`）解析为 link。两者都有对应 `[r]:` 定义，绝不误报悬空脚注。
func TestScanCaretReferenceAltNotFootnote(t *testing.T) {
	requireSeq(t, "![^ok][r]\n\n[r]: https://ex.com/img.png\n",
		"image|image|https://ex.com/img.png", "caption|caption")
	requireSeq(t, "[^ok][r]\n\n[r]: https://ex.com/link\n",
		"link|link|https://ex.com/link")
}

// —— ⑪ 悬空 reference link / reference image fail closed ——

// TestScanDanglingReferenceLinkFailsClosed —— 引用式写法用了 label 却无对应定义（full / collapsed，
// 含 reference image）：goldmark 会退化成普通文本、与目标侧一同退化即「假绿」，故 fail closed。
func TestScanDanglingReferenceLinkFailsClosed(t *testing.T) {
	requireScanError(t, "见 [文][missing] 结束\n", 1)     // full
	requireScanError(t, "见 [lbl][] 结束\n", 1)          // collapsed
	requireScanError(t, "配图 ![alt][missing] 结束\n", 1) // reference image
}

// —— ⑫ 预检不误报：代码 / 注释 / 转义内的畸形片段 ——

// TestScanMalformedShieldedNotMisreported —— 未闭合 image/link、悬空 footnote ref、悬空
// reference link 若出现在 fenced code / inline code / HTML 注释 / 转义序列内，都被屏蔽、不 fail
// closed（各一条负控）。
func TestScanMalformedShieldedNotMisreported(t *testing.T) {
	// 围栏代码内：整体只是一个 code 事件，绝不 fail closed。
	evs, err := ScanAssets([]byte("```\n![a](未闭合\n[b][missing]\n[^x]\n```\n"))
	if err != nil {
		t.Fatalf("围栏代码内的畸形片段不应 fail closed：%v", err)
	}
	if len(evs) != 1 || evs[0].Kind != AssetCode {
		t.Fatalf("围栏代码内畸形片段应仅得单个 code 事件，实得 %+v", evs)
	}
	// 行内代码内 / HTML 注释内 / 转义序列内：均无资产、无错误。
	requireSeq(t, "看 `![a](未闭合` 和 `[b][missing]` 与 `[^x]` 都不算\n")
	requireSeq(t, "<!-- ![a](未闭合 [b][missing] [^x] -->\n\n正文\n")
	requireSeq(t, "转义 \\[^x\\] 与 \\[b\\]\\[missing\\] 与 \\!\\[a\\]\\(未闭合\n")
}

// —— ⑬ 脚注 ref↔def 规范化归一与 goldmark 逐字节匹配的不一致 ——

// TestScanFootnoteCaseMismatchFailsClosed —— 同一文档里 `[^A]` 引用配 `[^a]:` 定义：本包按规范化
// （大小写折叠）会把二者视作成对而不报悬空，但 goldmark 的 Footnote 扩展按**原始字节**匹配、大小写
// 不同即不配对，令引用与定义**双双退化成普通文本**（既无 FootnoteLink 也无脚注定义）。若静默放行就
// 漏掉了一对脚注资产 → 必须 *AssetScanError（fail closed），定位到引用行。
func TestScanFootnoteCaseMismatchFailsClosed(t *testing.T) {
	requireScanError(t, "见脚注[^A]。\n\n[^a]: 定义\n", 1)
	// 空白归一层面的不一致同理：`[^Foo Bar]` 引用配 `[^Foo  Bar]:`（内部空白不同）goldmark 不配对。
	requireScanError(t, "见[^Foo Bar]。\n\n[^Foo  Bar]: 定义\n", 1)
}

// TestScanFootnoteByteExactMatchLegal —— 同一文档里 ref 与 def **字节一致**（含带内部空白 / 大小写
// 的 label）时 goldmark 正常配对：本包产出成对的规范化 ref/def 事件、绝不 fail closed。这保住了
// 跨 Source / target 两份文档各自内部一致、仅在两文档间做大小写 / 空白归一的合法改名场景。
func TestScanFootnoteByteExactMatchLegal(t *testing.T) {
	requireSeq(t, "见[^Foo Bar]。\n\n[^Foo Bar]: 定义\n",
		"footnote|footnote|ref|foo bar", "footnote|footnote|def|foo bar")
	requireSeq(t, "见[^foo  bar]。\n\n[^foo  bar]: 定义\n",
		"footnote|footnote|ref|foo bar", "footnote|footnote|def|foo bar")
}

// —— ⑭ 脚注定义与 AST 实际保留定义的多重集一致性 ——

// TestScanUnreferencedFootnoteDefFailsClosed —— 只有脚注定义、无任何引用命中：goldmark 的 footnote
// transformer 会把该定义连同正文一并从 AST 移除，ScanAssets 屏蔽视图却数得出这条定义。若不校验定义
// 多重集，Source 只含未引用定义时会返回空事件序列，目标侧删掉该定义也能「假绿」蒙混——故 fail closed，
// 定位到定义行。
func TestScanUnreferencedFootnoteDefFailsClosed(t *testing.T) {
	requireScanError(t, "[^a]: 未引用定义\n", 1)
	// 有正文段落但仍无引用命中，定义位于 L3，同样 fail closed 并定位到定义行。
	requireScanError(t, "正文一段。\n\n[^a]: 未引用定义\n", 3)
}

// TestScanDuplicateFootnoteDefFailsClosed —— 同一 label 出现多条定义：goldmark 只保留其一，屏蔽视图
// 却数出多条，定义多重集不等 → fail closed，定位到首条重复定义行。
func TestScanDuplicateFootnoteDefFailsClosed(t *testing.T) {
	requireScanError(t, "见[^a]。\n\n[^a]: 定义一\n\n[^a]: 定义二\n", 3)
}

// TestScanReferencedFootnoteDefLegal —— 一条引用配一条被命中的定义：定义被 AST 保留，引用 / 定义
// 多重集相等，产出成对的规范化 ref/def 事件、绝不 fail closed。
func TestScanReferencedFootnoteDefLegal(t *testing.T) {
	requireSeq(t, "见[^a]。\n\n[^a]: 定义\n",
		"footnote|footnote|ref|a", "footnote|footnote|def|a")
}

// TestScanReferencedFootnoteDefBodyAssetsCounted —— 被引用命中的脚注定义会随其正文保留在 AST 里，
// 定义正文内的链接 / 图片 / 图注照常按物理行计数、不因定义搬到文末而丢失（防未引用定义误删导致的正文
// 资产回归）。
func TestScanReferencedFootnoteDefBodyAssetsCounted(t *testing.T) {
	requireSeq(t, "见[^a]。\n\n[^a]: 定义含 [链接](u) 和 ![图](i.png)\n",
		"footnote|footnote|ref|a", "footnote|footnote|def|a",
		"link|link|u", "image|image|i.png", "caption|caption")
}
