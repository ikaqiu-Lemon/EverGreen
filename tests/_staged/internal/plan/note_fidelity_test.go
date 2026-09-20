package plan

// note_fidelity_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-2B
// 「结构资产保真」的**先红**判据（Schema v2 契约 §4.2.1 第 5 条）。
//
// 在 T12-2A 的来源覆盖（source_ref + omissions + Source 快照）之上，进一步核验八类结构资产
// ——图片 URL、图注、代码块内容、表格行、列表项、引用、链接、脚注——的**数量与相对顺序**在
// Source 对应来源范围与整理正文之间一致：格式恢复或翻译不免除保真。
//
// 诚实到机器能力为止：图片 / 链接 / 脚注 / 代码锁精确签名（目标值 / label / payload），这类
// 可稳定标识的事件乱序也判；纯文本型资产（表格行 / 列表项 / 引用）只锁结构形状、数量与跨类型
// 顺序，不声称能识别两条同形但语义互换的翻译行。诊断一律钉 ops[i].blocks[j].body（范围被切断
// 时钉造成切分的 source_ref / omission），Target=source id；任一失败该 write_note 零 action。
//
// 本文件只加测试；解析在 internal/mdfile（goldmark 只读 AST），plan 经 store 转发消费，
// 绝不自己手写 Markdown 解析。

import (
	"strings"
	"testing"
)

// fidNote 造一条「单 source 块覆盖 Source 全部正文行、无删除」的 v2 write_note，
// 让问题只落在资产保真这一层（覆盖 / 顺序 / 交叉早已在 T12-2A 判过）。
func fidNote(t *testing.T, src, ref, note string) *Result {
	t.Helper()
	return covValidate(t, src, covNote("n-20261101-fid",
		[]string{covSB(ref, note)}, nil, true))
}

// —— 八类：合法（翻译 / 排版变化仍过）与每类丢失 / 增加 / 身份改变 / 乱序（E2@blocks[0].body）——

// TestNoteFidelityPerClass —— 逐类正反用例：合法的忠实翻译 / 排版恢复不报；数量 / 身份 / 顺序
// 变化一律 E2 钉到 ops[0].blocks[0].body，且失败即零 action。
func TestNoteFidelityPerClass(t *testing.T) {
	cases := []struct {
		name string
		src  string // Source 正文（决定物理行布局）
		ref  string // 单块覆盖全部非空行
		note string // 整理正文
		ok   bool   // true=合法（零 error），false=E2@blocks[0].body
	}{
		// —— 合法：忠实翻译 / 排版恢复保住每类资产的稳定签名 ——
		{"合法-图片图注仅翻译文字", "![原图注](p.png)\n", "L1-L1", "![译图注](p.png)", true},
		{"合法-代码CRLF与围栏info可修复", "```\nA=1\n```\n", "L1-L3", "```py\r\nA=1\r\n```", true},
		{"合法-表格单元格翻译", "| a | b |\n| --- | --- |\n| 1 | 2 |\n", "L1-L3", "| A | B |\n| --- | --- |\n| x | y |", true},
		{"合法-列表项文字翻译", "- 甲\n- 乙\n", "L1-L2", "- A\n- B", true},
		{"合法-引用文字翻译", "> 原文\n", "L1-L1", "> 译文", true},
		{"合法-脚注定义文字翻译-label不变", "文字[^a]。\n\n[^a]: 原定义\n", "L1-L3", "内容[^a]。\n\n[^a]: 译定义", true},
		{"合法-行内代码屏蔽伪资产", "见 `[假](x)` 文字\n", "L1-L1", "见 `[伪](y)` 内容", true},
		{"合法-转义伪资产不计", "转义 \\[非链接\\](x) 文字\n", "L1-L1", "转义 \\[伪\\](y) 内容", true},
		{"合法-图片内嵌于链接不双计link", "[![图](i.png)](https://ex.com/u)\n", "L1-L1", "[![图译](i.png)](https://ex.com/u)", true},
		{"合法-inline与reference图片等价", "![甲](refimg.png)\n", "L1-L1", "![乙][r]\n\n[r]: refimg.png", true},
		{"合法-同形列表项语义互换不误判", "- 甲\n- 乙\n", "L1-L2", "- 乙\n- 甲", true},

		// —— 图片 URL / 图注 ——
		{"错-图片丢失", "![图](p.png)\n", "L1-L1", "此处无图", false},
		{"错-图片目标改变", "![图](p.png)\n", "L1-L1", "![图](q.png)", false},
		{"错-图片增加", "纯文字\n", "L1-L1", "文字 ![新图](n.png)", false},
		{"错-图注丢失", "![说明](p.png)\n", "L1-L1", "![](p.png)", false},
		{"错-图注增加", "![](p.png)\n", "L1-L1", "![新增图注](p.png)", false},

		// —— 代码块内容 ——
		{"错-代码payload改变", "```\nA=1\n```\n", "L1-L3", "```\nA=2\n```", false},
		{"错-代码丢失", "```\nA=1\n```\n", "L1-L3", "普通文字，无代码", false},
		{"错-代码增加", "纯文字\n", "L1-L1", "```\nnew=1\n```", false},

		// —— 表格行 ——
		{"错-表格行丢失", "| a | b |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |\n", "L1-L4", "| A | B |\n| --- | --- |\n| x | y |", false},
		{"错-表格行增加", "| a | b |\n| --- | --- |\n| 1 | 2 |\n", "L1-L3", "| A | B |\n| --- | --- |\n| x | y |\n| m | n |", false},

		// —— 列表项 ——
		{"错-列表项丢失", "- 甲\n- 乙\n", "L1-L2", "- 甲", false},
		{"错-列表项嵌套深度改变", "- 甲\n- 乙\n", "L1-L2", "- 甲\n  - 乙", false},
		{"错-列表项增加", "纯文字\n", "L1-L1", "- 项一\n- 项二", false},

		// —— 引用 ——
		{"错-引用丢失", "> 引用\n", "L1-L1", "引用（去引用）", false},
		{"错-引用增加", "普通段落\n", "L1-L1", "> 引用", false},

		// —— 脚注 ——
		{"错-脚注label改变", "文字[^a]。\n\n[^a]: 定义\n", "L1-L3", "内容[^b]。\n\n[^b]: 译定义", false},
		{"错-脚注丢失", "文字[^a]。\n\n[^a]: 定义\n", "L1-L3", "文字。\n\n定义", false},
		{"错-脚注增加", "纯文字\n", "L1-L1", "见[^z]。\n\n[^z]: 定义", false},

		// —— 链接 ——
		{"错-链接丢失", "见 [l](https://ex.com/l) 结束\n", "L1-L1", "见结束", false},
		{"错-链接增加", "纯文字无链接\n", "L1-L1", "文字 [新链](https://ex.com/new)", false},
		{"错-链接目标改变", "[文](https://ex.com/a)\n", "L1-L1", "[文](https://ex.com/b)", false},

		// —— 顺序：image / code / footnote / link 各一组同类可标识乱序 + 跨类型乱序 ——
		{"错-跨类型乱序", "![i](p.png) 见 [l](https://ex.com/l)\n", "L1-L1", "[l](https://ex.com/l) 见 ![i](p.png)", false},
		{"错-同类可标识链接乱序", "[甲](https://ex.com/1) 与 [乙](https://ex.com/2)\n", "L1-L1", "[乙](https://ex.com/2) 与 [甲](https://ex.com/1)", false},
		{"错-同类可标识图片乱序", "![甲](p.png) 与 ![乙](q.png)\n", "L1-L1", "![乙译](q.png) 与 ![甲译](p.png)", false},
		{"错-同类可标识代码乱序", "```\nA\n```\n\n```\nB\n```\n", "L1-L7", "```\nB\n```\n\n```\nA\n```", false},
		{"错-同类可标识脚注乱序", "参见[^a]与[^b]。\n\n[^a]: 甲\n\n[^b]: 乙\n", "L1-L5", "参见[^b]与[^a]。\n\n[^a]: 甲\n\n[^b]: 乙", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := fidNote(t, c.src, c.ref, c.note)
			if c.ok {
				requireNoError(t, res)
				if len(res.Actions) == 0 {
					t.Fatal("合法用例应展开写入 action")
				}
				return
			}
			d := requireErrorAt(t, res, E2, "ops[0].blocks[0].body")
			if d.Target != covSrcID {
				t.Fatalf("资产保真诊断的 Target 应逐项等于 source id %q，实得 %q", covSrcID, d.Target)
			}
			if len(res.Actions) != 0 {
				t.Fatalf("资产保真失败时 write_note 必须零展开，实得 %d 条 action", len(res.Actions))
			}
		})
	}
}

// —— 八类全覆盖的合法长文（正文翻译 + 排版变化仍过）——

// 一份含全部八类资产的 Source：图片(+图注) / 链接 / 脚注引用 / 引用 / 列表 / 表格 / 代码 / 脚注定义。
const fidAllSrc = "![原始配图](assets/a.png)\n" + // L1 image + caption
	"\n" + // L2
	"正文含 [参考链接](https://ex.com/ref) 与脚注[^n]。\n" + // L3 link + footnote ref
	"\n" + // L4
	"> 一段引用文字\n" + // L5 blockquote
	"\n" + // L6
	"- 列表甲\n" + // L7 list item
	"- 列表乙\n" + // L8 list item
	"\n" + // L9
	"| 头1 | 头2 |\n" + // L10 table header
	"| --- | --- |\n" + // L11 table delimiter
	"| 甲 | 乙 |\n" + // L12 table body
	"\n" + // L13
	"```py\n" + // L14 code fence open
	"v = 1\n" + // L15 code payload
	"```\n" + // L16 code fence close
	"\n" + // L17
	"[^n]: 脚注定义文字\n" // L18 footnote def

// 整理正文：逐块翻译 + 改围栏 info（py→js）+ 换单元格/列表/引用文字，但保住每类资产的稳定签名
// （图片目标 / 链接目标 / 脚注 label / 代码 payload / 结构形状 / 顺序全不变）。
const fidAllNote = "![配图（译）](assets/a.png)\n" +
	"\n" +
	"正文（译）含 [文档链接](https://ex.com/ref) 与脚注[^n]。\n" +
	"\n" +
	"> 引用（译）\n" +
	"\n" +
	"- 项甲（译）\n" +
	"- 项乙（译）\n" +
	"\n" +
	"| A | B |\n" +
	"| --- | --- |\n" +
	"| x | y |\n" +
	"\n" +
	"```js\n" +
	"v = 1\n" +
	"```\n" +
	"\n" +
	"[^n]: 脚注（译）"

// TestNoteFidelityAllClassesLegal —— 八类资产同现的长文：整体翻译 + 排版恢复后，数量 / 身份 /
// 顺序全部守恒 → 零 error、正常展开写入。
func TestNoteFidelityAllClassesLegal(t *testing.T) {
	res := fidNote(t, fidAllSrc, "L1-L18", fidAllNote)
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("八类合法长文应展开写入 action")
	}
}

// —— 多行资产被 source_ref / omission / 未覆盖空白切断 → E2 钉造成切分的区间 ——

// noWriteNoteAction 报告结果里归属于某 op 下标的 write_note（note_new）action 条数。
// same-plan 里 add_source 会各自展开 action，因此不能用「总 action 数」判 write_note 零展开，
// 必须按 OpIndex 精确点名 write_note 自身没有产出写入 action。
func noWriteNoteAction(res *Result, opIndex int) int {
	n := 0
	for _, a := range res.Actions {
		if a.Kind == ActNoteNew && a.OpIndex == opIndex {
			n++
		}
	}
	return n
}

// TestNoteFidelityMultilineCut —— 多行资产（代码块 / 表格容器 / 多行列表项 / 多行·嵌套引用 /
// 多行脚注定义）被三种方式切断，各钉到造成切分的区间 path，且失败即零 write_note action、
// 诊断 Target=source id。不只测围栏代码。
func TestNoteFidelityMultilineCut(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		blocks   []string
		oms      []string
		wantPath string
	}{
		// —— 围栏代码块：三种切断 ——
		{"代码-source_ref切断", "```\nA\nB\n```\n",
			[]string{covSB("L1-L2", "抄前半代码。"), covSB("L3-L4", "抄后半代码。")}, nil,
			"ops[0].blocks[0].source_ref"},
		{"代码-omission切断", "```\nA\nB\n```\n",
			[]string{covSB("L3-L4", "抄后半代码。")}, []string{covOm("L1-L2", "误删代码开头")},
			"ops[0].omissions[0].source_ref"},
		{"代码-未覆盖空白切断", "```\nA\n\nB\n```\n",
			[]string{covSB("L1-L2", "抄前半。"), covSB("L4-L5", "抄后半。")}, nil,
			"ops[0].blocks[0].source_ref"},

		// —— 表格容器（整表 span 含 delimiter）：被两个 source_ref 拆开 ——
		{"表格容器-source_ref切断", "| a | b |\n| --- | --- |\n| 1 | 2 |\n",
			[]string{covSB("L1-L2", "抄表头+分隔"), covSB("L3-L3", "抄数据行")}, nil,
			"ops[0].blocks[0].source_ref"},

		// —— 多行列表项（续行 / 松散）：三种切断 ——
		{"列表项-source_ref切断", "- 甲行一\n  甲行二\n- 乙\n",
			[]string{covSB("L1-L1", "项一首行"), covSB("L2-L3", "续行与项二")}, nil,
			"ops[0].blocks[0].source_ref"},
		{"列表项-omission切断", "- 甲行一\n  甲行二\n- 乙\n",
			[]string{covSB("L2-L3", "续行与项二")}, []string{covOm("L1-L1", "误删项一首行")},
			"ops[0].omissions[0].source_ref"},
		{"列表项-未覆盖空白切断", "- 甲\n\n  续\n- 乙\n",
			[]string{covSB("L1-L1", "项一首段"), covSB("L3-L4", "项一续段与项二")}, nil,
			"ops[0].blocks[0].source_ref"},

		// —— 多行 / 嵌套引用：source_ref / omission 切断 ——
		{"引用-source_ref切断", "> 甲\n> 乙\n",
			[]string{covSB("L1-L1", "引用首行"), covSB("L2-L2", "引用次行")}, nil,
			"ops[0].blocks[0].source_ref"},
		{"引用-omission切断", "> 甲\n> 乙\n",
			[]string{covSB("L2-L2", "引用次行")}, []string{covOm("L1-L1", "误删引用首行")},
			"ops[0].omissions[0].source_ref"},

		// —— 多行脚注定义：三种切断 ——
		{"脚注定义-source_ref切断", "正文[^n]。\n\n[^n]: 定义一\n    定义二\n",
			[]string{covSB("L1-L3", "正文与定义首行"), covSB("L4-L4", "定义续行")}, nil,
			"ops[0].blocks[0].source_ref"},
		{"脚注定义-omission切断", "正文[^n]。\n\n[^n]: 定义一\n    定义二\n",
			[]string{covSB("L1-L2", "正文"), covSB("L4-L4", "定义续行")},
			[]string{covOm("L3-L3", "误删定义首行")},
			"ops[0].omissions[0].source_ref"},
		{"脚注定义-未覆盖空白切断", "正文[^n]。\n\n[^n]: 甲\n\n    续\n",
			[]string{covSB("L1-L3", "正文与定义首段"), covSB("L5-L5", "定义续段")}, nil,
			"ops[0].blocks[0].source_ref"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := covValidate(t, c.src, covNote("n-20261101-cut", c.blocks, c.oms, true))
			d := requireErrorAt(t, res, E2, c.wantPath)
			if d.Target != covSrcID {
				t.Fatalf("切断诊断 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
			}
			if len(res.Actions) != 0 {
				t.Fatalf("范围切断资产时必须零展开，实得 %d 条 action", len(res.Actions))
			}
		})
	}
}

// —— 目标侧：资产必须完整落在恰一个 source 块，不得靠相邻块拼接复原 ——

// TestNoteFidelityTargetCrossBlockFailsClosed —— 目标侧不能只按 StartLine 归属：一个 fenced
// code 被拆到两个 source 块的 body，拼接视图能复原同一 payload（与 Source 完全一致），但因终点
// 越出起点所在块，必须 E2 钉到造成跨块的 block0.body（fail closed），且零 action。
func TestNoteFidelityTargetCrossBlockFailsClosed(t *testing.T) {
	// Source 六行：L1 ``` / L2 A / L3 空 / L4 B / L5 ``` / L6 尾。
	const src = "```\nA\n\nB\n```\n尾\n"
	// source refs：首块 L1-L5（整块代码）、次块 L6-L6（尾）。
	// 目标 block0.body = "```\nA"（视图 L1-L2）、block1.body = "B\n```\n尾"（视图 L4-L6）。
	// 拼接视图 "```\nA\n\nB\n```\n尾" 能复原同一 code 事件（payload=A\n\nB\n，与 Source 等价），
	// 但该事件起于 block0、终于 block1 → 必须 E2@block0.body。
	res := covValidate(t, src, covNote("n-20261101-xblk",
		[]string{covSB("L1-L5", "```\nA"), covSB("L6-L6", "B\n```\n尾")}, nil, true))
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].body")
	if d.Target != covSrcID {
		t.Fatalf("跨块诊断 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("跨块拼接资产必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// —— reference link 的跨块解析与定义目标改变 ——

// TestNoteFidelityReferenceLinkCrossBlockLegal —— reference link 的 usage 与 definition 落在
// 两个 source 块：拼接成整视图后仍能解析出同一 link（目标不变）→ 合法、正常写入。
func TestNoteFidelityReferenceLinkCrossBlockLegal(t *testing.T) {
	// Source：L1 usage（reference link）/ L2 空 / L3 definition。
	const src = "见 [文档][r] 结束。\n\n[r]: https://ex.com/r\n"
	res := covValidate(t, src, covNote("n-20261101-reflink",
		[]string{covSB("L1-L2", "见 [文档][r] 结束。"), covSB("L3-L3", "[r]: https://ex.com/r")}, nil, true))
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("reference link 跨块合法用例应展开写入 action")
	}
}

// TestNoteFidelityDefinitionDestChangePinsUsage —— definition 的 destination 被改（换了目标 URL）：
// link 事件挂在 **usage 所属的块**（block0），mismatch 钉到 usage 的 block.body，而非定义所在块。
func TestNoteFidelityDefinitionDestChangePinsUsage(t *testing.T) {
	const src = "见 [文档][r] 结束。\n\n[r]: https://ex.com/r\n"
	// definition 的目标从 /r 改成 /CHANGED：整视图解析出的 link 目标随之改变，与 Source 不一致。
	res := covValidate(t, src, covNote("n-20261101-defchg",
		[]string{covSB("L1-L2", "见 [文档][r] 结束。"), covSB("L3-L3", "[r]: https://ex.com/CHANGED")}, nil, true))
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].body")
	if d.Target != covSrcID {
		t.Fatalf("定义目标改变诊断 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("定义目标改变必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// TestNoteFidelityMismatchMessageUsesLineRange —— 资产 mismatch 的诊断文案里，来源范围用真实物理
// 行区间 L<起>-L<止> 表述（而非字段路径 source_ref）；Path 仍保持字段级 blocks[j].body。
func TestNoteFidelityMismatchMessageUsesLineRange(t *testing.T) {
	res := fidNote(t, "![图](p.png)\n", "L1-L1", "![图](q.png)")
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].body")
	if !strings.Contains(d.Message, "来源范围 L1-L1") {
		t.Fatalf("mismatch 文案应含真实行区间「来源范围 L1-L1」，实得：%s", d.Message)
	}
	if strings.Contains(d.Message, "source_ref") {
		t.Fatalf("mismatch 文案不应再以字段路径 source_ref 表述来源范围：%s", d.Message)
	}
}

// TestNoteFidelityContainerFencedCodeLegal —— 来源与整理正文都含 blockquote 内围栏代码：
// mdfile 能正确解析容器内围栏（不再误判未闭合），保真闸门放行、正常展开写入。
func TestNoteFidelityContainerFencedCodeLegal(t *testing.T) {
	res := fidNote(t, "> ```\n> v=1\n> ```\n", "L1-L3", "> ```\n> v=1\n> ```")
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("blockquote 内围栏代码的合法保真应展开写入 action")
	}
}

// —— 表格：表内 link / image 的行内位置按真实行守恒（资产不得在行间迁移或互换）——

// TestNoteFidelityTableInnerLinkMovedRow —— 表内 link 的**真实行位置**参与排序：把同一 URL 的 link
// 从表头行移到数据行（表格行结构 / 单元格数 / link 目标都不变），事件相对顺序改变 → 必须 E2。
// 若表格行仍塌到表首行、link 被排到所有 row 事件之后，这种「行间迁移」就会假绿。
func TestNoteFidelityTableInnerLinkMovedRow(t *testing.T) {
	// Source：链接在**表头行**（L1）。整理正文把它挪到**数据行**（L3），URL 不变。
	const src = "| [x](http://e/1) | b |\n| --- | --- |\n| c | d |\n"
	const note = "| a | b |\n| --- | --- |\n| [x](http://e/1) | d |"
	res := fidNote(t, src, "L1-L3", note)
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].body")
	if d.Target != covSrcID {
		t.Fatalf("表内 link 行间迁移诊断 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("表内 link 行间迁移必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// TestNoteFidelityTableInnerLinkSwapped —— 两个数据行各含一个**稳定目标**的 link，整理正文把它们
// 互换（两个 URL 都还在、表结构不变），但事件顺序改变 → 必须 E2（可标识 link 的乱序也判）。
func TestNoteFidelityTableInnerLinkSwapped(t *testing.T) {
	// Source：数据行一(L3) link1、数据行二(L4) link2。整理正文互换两 link。
	const src = "| a | b |\n| --- | --- |\n| [x](http://e/1) | e |\n| f | [y](http://e/2) |\n"
	const note = "| a | b |\n| --- | --- |\n| [y](http://e/2) | e |\n| f | [x](http://e/1) |"
	res := fidNote(t, src, "L1-L4", note)
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].body")
	if d.Target != covSrcID {
		t.Fatalf("表内 link 互换诊断 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("表内 link 互换必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// TestNoteFidelityTableInnerLinkStableLegal —— 表内 link 留在原数据行、仅翻译单元格文字：真实行
// 位置守恒、顺序不变 → 合法、正常展开写入（正控，防止上面两个反例被过度拦截）。
func TestNoteFidelityTableInnerLinkStableLegal(t *testing.T) {
	const src = "| a | b |\n| --- | --- |\n| [x](http://e/1) | e |\n| f | [y](http://e/2) |\n"
	const note = "| A | B |\n| --- | --- |\n| [链](http://e/1) | 译 |\n| 译 | [接](http://e/2) |"
	res := fidNote(t, src, "L1-L4", note)
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("表内 link 行内位置守恒的合法翻译应展开写入 action")
	}
}

// TestNoteFidelityTableSplitBySourceRefPinned —— 整张表被两个 source_ref 拆开（表头 + 分隔归一块、
// 数据行归另一块）：表格行的 scope 是整表，任一行的 scope 都跨出其起点所在区间 → E2 钉到造成切分的
// source_ref（不因真实行 span 变小而漏检），且零 action、Target=source id。
func TestNoteFidelityTableSplitBySourceRefPinned(t *testing.T) {
	res := covValidate(t, "| a | b |\n| --- | --- |\n| 1 | 2 |\n", covNote("n-20261101-tblsplit",
		[]string{covSB("L1-L2", "抄表头+分隔"), covSB("L3-L3", "抄数据行")}, nil, true))
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].source_ref")
	if d.Target != covSrcID {
		t.Fatalf("整表被拆诊断 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("整表被 source_ref 拆开必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// —— 完整落在 omission 的资产从期望序列移除（合法）——

// TestNoteFidelityOmittedAssetLegal —— 广告配图整幅落在 omission 内：从期望序列移除，
// 对应 source 块（正文）无资产，整理正文也无资产 → 合法、正常写入。
func TestNoteFidelityOmittedAssetLegal(t *testing.T) {
	// L1 广告图（删）/ L2 正文。
	res := covValidate(t, "![广告](ad.png)\n正文甲\n", covNote("n-20261101-omit",
		[]string{covSB("L2-L2", "正文甲（译）")},
		[]string{covOm("L1-L1", "页面广告图，非作者内容")}, true))
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("完整落在 omission 的资产移除后应正常展开写入 action")
	}
}

// —— 原始 HTML / 畸形资产 fail closed ——

// TestNoteFidelitySourceRawHTMLFailClosed —— Source 正文含原始 HTML 资产标签（<img>）：
// 无法可靠解析 → E2@ops[0].source（fail closed），绝不静默当「无资产」，Target=source id，且零 action。
func TestNoteFidelitySourceRawHTMLFailClosed(t *testing.T) {
	res := covValidate(t, "<img src=\"r.png\" />\n正文甲\n", covNote("n-20261101-rawsrc",
		[]string{covSB("L1-L2", "配图与正文")}, nil, true))
	d := requireErrorAt(t, res, E2, "ops[0].source")
	if d.Target != covSrcID {
		t.Fatalf("Source 资产解析失败诊断的 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("Source 原始 HTML 资产 fail closed 必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// TestNoteFidelityTargetRawHTMLFailClosed —— 整理正文含行内原始 HTML 资产标签（<img>）：
// 无法可靠解析 → E2 钉到肇事的 ops[0].blocks[0].body（fail closed），Target=source id，且零 action。
func TestNoteFidelityTargetRawHTMLFailClosed(t *testing.T) {
	res := covValidate(t, "正文甲\n", covNote("n-20261101-rawtgt",
		[]string{covSB("L1-L1", "正文甲 <img src=\"x.png\">")}, nil, true))
	d := requireErrorAt(t, res, E2, "ops[0].blocks[0].body")
	if d.Target != covSrcID {
		t.Fatalf("整理正文资产解析失败诊断的 Target 应为 source id %q，实得 %q", covSrcID, d.Target)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("整理正文原始 HTML 资产 fail closed 必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// —— same-plan add_source 快照下的资产保真 ——

// TestNoteFidelitySamePlanAddSource —— 同一 plan 先 add_source、后 write_note：资产保真同样
// 用 add_source 的 op.Body **落盘后**布局解析。body `![图](p.png)\n甲\n` 落盘后为
// 「(空行)\n![图](p.png)\n甲\n」：L1 空白 / L2 图片(+图注) / L3 甲。两个 source 块 L2 / L3
// 分别对上图片块与正文块，整理正文守住资产 → 合法。
func TestNoteFidelitySamePlanAddSource(t *testing.T) {
	res := covInline(t, "![图](p.png)\\n甲\\n",
		[]string{covSB("L2-L2", "![图译](p.png)"), covSB("L3-L3", "甲译")}, nil, true)
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("same-plan add_source 合法资产保真应展开写入 action")
	}
}

// TestNoteFidelitySamePlanAddSourceMismatch —— 同上快照布局，但整理正文把图片目标改掉 → E2。
// 证明资产保真吃的是落盘后快照、且 same-plan 路径同样被保真闸门覆盖。
func TestNoteFidelitySamePlanAddSourceMismatch(t *testing.T) {
	res := covInline(t, "![图](p.png)\\n甲\\n",
		[]string{covSB("L2-L2", "![图译](q.png)"), covSB("L3-L3", "甲译")}, nil, true)
	// 图片在 L2，对应第一个 source 块（blocks[0]）；write_note 是本 plan 的第二个 op（ops[1]）。
	// 注意：同 plan 的 add_source（ops[0]）仍会各自展开自己的 action，这里只钉 write_note 失败
	// 与它的字段级路径，并按 OpIndex 断言 write_note 自身零展开（不能只看总 action 数）。
	requireErrorAt(t, res, E2, "ops[1].blocks[0].body")
	if n := noWriteNoteAction(res, 1); n != 0 {
		t.Fatalf("same-plan write_note 失败时其自身（ops[1]）必须零展开 write_note action，实得 %d 条", n)
	}
}

// —— 无资产 / v1 / v2-sections / W21 回归：保真闸门不误伤兼容路径 ——

// TestNoteFidelityNoAssetLegal —— 纯文本 Source / 整理正文（零资产）：保真闸门平凡通过。
func TestNoteFidelityNoAssetLegal(t *testing.T) {
	res := covValidate(t, covBody4, covNote("n-20261101-noasset",
		[]string{covSB("L1-L2", "抄前两行。"), covSB("L3-L4", "抄后两行。")}, nil, true))
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("零资产合法用例应展开写入 action")
	}
}

// TestNoteFidelityV1SectionsUnaffected —— 含资产的 Source 上跑 plan_version:1 的 sections{}
// write_note：兼容路径不被 T12-2B 保真波及（既不解析资产、也不产资产类 E2）。
func TestNoteFidelityV1SectionsUnaffected(t *testing.T) {
	res := covV1(t, "![图](p.png)\n正文\n",
		`{"op":"write_note","source":"`+covSrcID+`","note_id":"n-20261101-v1",
 "sections":{"材料提炼":"整理正文，图注可翻译。\n"}}`)
	requireNoError(t, res)
	for _, d := range res.Errors {
		if strings.Contains(d.Message, "结构资产") {
			t.Fatalf("v1 兼容路径不应触发结构资产保真：%s@%s", d.Code, d.Path)
		}
	}
}

// TestNoteFidelityV2SectionsUnaffected —— plan_version:2 但仍用 sections{}（非 blocks[]）：
// 走兼容映射，同样不触发结构资产保真。
func TestNoteFidelityV2SectionsUnaffected(t *testing.T) {
	files := covSourceFile("![图](p.png)\n正文\n")
	res := run(t, vault(t, files), v2Plan(t, files,
		`{"op":"write_note","source":"`+covSrcID+`","note_id":"n-20261101-v2sec",
 "sections":{"材料提炼":"整理正文，图注可翻译。\n"}}`))
	requireNoError(t, res)
	for _, d := range res.Errors {
		if strings.Contains(d.Message, "结构资产") {
			t.Fatalf("v2 sections 兼容路径不应触发结构资产保真：%s@%s", d.Code, d.Path)
		}
	}
}

// TestNoteFidelityW21StaysWarning —— 保真闸门通过（零资产）时，W21 仍是 warning、不被保真
// 阶段吞掉或升级：6 个 H2、2 个 source 块覆盖全文 → W21 warning、照常写入。
func TestNoteFidelityW21StaysWarning(t *testing.T) {
	res := covValidate(t, covAnchorBody, covNote("n-20261101-w21",
		[]string{covSB("L1-L6", "抄前半。"), covSB("L7-L12", "抄后半。")}, nil, true))
	requireWarning(t, res, W21)
	if res.Failed() {
		t.Fatalf("W21 是 warning，保真闸门不得把它拦成 error：errors=%v", diagPaths(res.Errors))
	}
}
