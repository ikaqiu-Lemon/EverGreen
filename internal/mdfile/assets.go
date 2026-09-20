package mdfile

// 结构资产扫描（Schema v2 契约 §4.2.1「来源范围与保真」）：把一段 Markdown 正文只读解析成
// 一条**有序的结构资产事件序列**。八类资产逐字取自设计真源 §4.2.1 第 5 条——图片 URL、图注、
// 代码块内容、表格行、列表项、引用、链接、脚注——其「数量及相对顺序」必须在来源范围与整理正文
// 之间一致（格式恢复或翻译不免除保真）。
//
// 解析器只在本包出现（依赖方向：plan → store → mdfile）：goldmark 只读 AST，启用 GFM
// （table / linkify / …）与 footnote 扩展，不渲染 HTML、不改写任何权威字节。比较逻辑不在这里，
// 由 internal/plan 消费本序列按来源范围逐块比对。
//
// # 稳定签名（可机械证明的身份，诚实到机器能力为止）
//
//	image      = 解析后的目标（destination）精确值；inline / reference 两种写法目标一致即等价
//	caption    = 与该 image 关联的非空 alt/图注的**存在性**（文字可翻译，不进签名）
//	code       = fenced / indented 的 payload（只规范 CRLF→LF，其余精确；围栏字符 / info 可修复）
//	table_row  = header / body 角色 + 单元格数（单元格文字可翻译，不进签名）
//	list_item  = 嵌套深度 + 有序 / 无序
//	blockquote = 嵌套深度
//	link       = 解析后的目标精确值（裸 autolink 也计 link；图片不再双计 link）
//	footnote   = 规范化 label（大小写折叠 + 内部空白折叠）+ 引用 / 定义身份
//
// 纯文本型资产（表格行 / 列表项 / 引用）只锁结构形状、数量与跨类型顺序：机器无法辨认两条同形
// 但语义互换的翻译行，因此不声称能识别这种乱序。围栏与行内代码里的伪资产语法被 CommonMark 自然
// 屏蔽；转义的伪语法不解析成资产，故不计。
//
// # 物理行区间（真实 span 与「完整归属范围」scope）
//
// 每个事件带**真实物理行闭区间** StartLine/EndLine（1 基，口径同 bodyPhysicalLines），优先用
// goldmark 的 node.Pos() 定位起点（行内 / 空节点也先用 Pos，再回退子 Text / 祖先），**事件的相对
// 顺序按真实行排序**。另带**完整归属范围** ScopeStartLine/ScopeEndLine，供来源侧把资产钉进恰一个
// source_ref / omission 并做切分检测：普通事件 scope 即自身 span；多行资产覆盖其全部物理行（围栏
// 代码从真实开围栏扫到匹配闭围栏）。**表格行的真实 span 取所在表头 / 数据行**，而 scope 取**整张
// Table 容器（含 delimiter 行）**——如此表头 + 分隔行与数据行被两个 source_ref 拆开可经 scope 检出，
// 同时表内 link / image 仍按真实行与本行相邻排序（避免所有 row 事件塌到表首行、把资产乱序检漏成假绿）。
//
// # fail closed（看似资产却无法可靠解析）
//
// 除原始 HTML 的 img / figure / figcaption / a / table / pre / code 资产标签外，还对以下
// 「看似资产却无法由所支持语法可靠解析」的形态做保守预检，一律返回 *AssetScanError，由 plan
// 以 E2 fail closed，绝不静默当「无资产」：
//
//	① 未闭合的围栏代码块（缺少匹配长度的收尾围栏）；
//	② 未转义的畸形 image/link（destination 未闭合）；
//	③ 悬空 footnote 引用（引用了不存在的定义 label）；
//	④ 悬空 reference link / reference image（用了引用式 label 却无对应定义）；
//	⑤ 脚注 ref / def 与 AST 实际产出不一致：ref↔def 规范化归一同 goldmark 逐字节匹配不符（同文档
//	   `[^A]` 配 `[^a]:` 会双双退化），或定义未被引用命中 / 重复 / 匹配失败而被 transformer 移除。
//
// 预检在**屏蔽了围栏 / 行内代码 / HTML 注释 / 转义标点之后**的视图上进行：HTML 注释里的
// img/a 只是注释文本、不是资产（不误报）；合法的普通 [text] 与转义语法也不误判。
import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// AssetKind 是结构资产的封闭类别（设计真源 §4.2.1 第 5 条的八类；caption 与 image 分列为两个
// 事件，因为「图片 URL」与「图注」是两项独立要保真的资产）。
type AssetKind string

const (
	AssetImage      AssetKind = "image"
	AssetCaption    AssetKind = "caption"
	AssetCode       AssetKind = "code"
	AssetTableRow   AssetKind = "table_row"
	AssetListItem   AssetKind = "list_item"
	AssetBlockquote AssetKind = "blockquote"
	AssetLink       AssetKind = "link"
	AssetFootnote   AssetKind = "footnote"
)

// AssetEvent 是一条有序结构资产事件。
//
//   - Sig 是**稳定签名**：同类事件相等当且仅当 Kind 相同且 Sig 逐字节相等（顺序 / 数量比对的依据）。
//   - ID 是给诊断读的人读标识（如 image 的目标、footnote 的 label），不参与相等判定。
//   - StartLine / EndLine 是该事件在被解析正文中的**真实物理行闭区间**（1 基，口径同
//     bodyPhysicalLines）：这是事件的真实所在位置，**事件排序（相对顺序）以此为准**。多行资产
//     （代码块 / 多行列表项 / 引用 / 脚注定义）覆盖其全部物理行（围栏代码含起止围栏行）。表格行取
//     其**所在数据 / 表头行的真实行号**（而非整表），使表头 / 数据行里的 link / image 能按真实行
//     与本行事件相邻排序、不被排到所有 row 事件之后（否则资产从一行移到另一行会假绿）。
//   - ScopeStartLine / ScopeEndLine 是该事件的**完整归属范围**：来源侧「资产必须完整落在恰一个
//     source_ref / omission」与目标侧「必须完整落在恰一个 source 块」的容纳 / 切断判定都按 scope。
//     普通事件 scope 等于自身 span；表格行的 scope 是**整张 Table 容器（含 delimiter 行）**——
//     同一张 GFM table 的任一行被两个 source_ref / omission 拆开都要能检出，而这不影响行事件的真实
//     排序位置。
type AssetEvent struct {
	Kind           AssetKind
	Sig            string
	ID             string
	StartLine      int
	EndLine        int
	ScopeStartLine int
	ScopeEndLine   int
}

// Label 是事件在诊断里的短标识：`kind(ID)` 或裸 `kind`。
func (e AssetEvent) Label() string {
	if e.ID == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + "(" + e.ID + ")"
}

// AssetScanError 是结构扫描错误：正文里存在无法由所支持语法可靠解析的资产形态（原始 HTML 资产
// 标签、未闭合围栏、畸形 image/link、悬空脚注引用等）。Line 是命中处的 1 基物理行（0 表示未定位）。
type AssetScanError struct {
	Reason string
	Line   int
}

func (e *AssetScanError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("结构扫描错误（第 %d 行）：%s", e.Line, e.Reason)
	}
	return "结构扫描错误：" + e.Reason
}

// reRawHTMLAsset 命中原始 HTML 的资产形态标签（大小写不敏感）：img / figure / figcaption / a /
// table / pre / code。标签名后须紧跟空白、`>` 或 `/`，避免把 `<article>` 之类误判成 `<a>`。
var reRawHTMLAsset = regexp.MustCompile(`(?i)<\s*/?\s*(img|figure|figcaption|a|table|pre|code)(\s|>|/)`)

// reHTMLComment 命中一段 HTML 注释（跨行）。注释里的 img/a 文本只是注释、不是资产。
var reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// reFootnoteDef / reFootnoteRef 用于**预检悬空引用**（在屏蔽视图上跑）：定义须行首（至多 3 个
// 前导空格）`[^label]:`；引用是任意位置的 `[^label]`（紧跟 `:` 的那次出现是定义本身，跳过）。
var (
	reFootnoteDef = regexp.MustCompile(`(?m)^ {0,3}\[\^([^\]\r\n]+)\]:`)
	reFootnoteRef = regexp.MustCompile(`\[\^([^\]\r\n]+)\]`)
)

// reLinkRefDef 命中链接引用定义 `[label]: dest`（行首至多 3 空格）；label 首字符排除 `^` 以避开
// 脚注定义 `[^label]:`。reRefLinkFull / reRefLinkCollapsed 命中引用式用法：full `[text][label]`
// （含 `![text][label]` 图片，`!` 不入捕获）与 collapsed `[label][]`；捕获组均为被引用的 label。
// shortcut `[text]`（无第二个 `[...]`）不匹配二者，故合法普通方括号文本不会被当成悬空 reference link。
var (
	reLinkRefDef       = regexp.MustCompile(`(?m)^ {0,3}\[([^\^\]\r\n][^\]\r\n]*)\]:`)
	reRefLinkFull      = regexp.MustCompile(`\[[^\]\r\n]*\]\[([^\]\r\n]+)\]`)
	reRefLinkCollapsed = regexp.MustCompile(`\[([^\]\r\n]+)\]\[\]`)
)

// ScanAssets 只读解析 body 的结构资产，返回按来源顺序（物理行升序，同行按阅读顺序）排序的事件序列。
// 先解析出只读 AST，再在其上做保守预检（看似资产却无法可靠解析的形态 fail closed），最后遍历取事件。
// body 为纯文本 / 无资产时返回空序列、nil。
func ScanAssets(body []byte) ([]AssetEvent, error) {
	sc := &scanner{src: body}
	sc.indexLines()

	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote))
	doc := md.Parser().Parse(text.NewReader(body))
	sc.fnLabel = footnoteLabels(doc)

	// 预检（在只读 AST 之上）：先按 goldmark 定位的围栏 / 缩进代码 / 行内代码 / 注释 / 转义屏蔽出
	// 「代码与非语法」视图（这样 blockquote / 列表容器里的合法围栏代码也被精确按其真实行区间屏蔽，
	// 不会因整行带容器前缀而漏屏蔽或误判），再识别未闭合围栏、畸形 image/link、悬空脚注引用与悬空
	// reference link。看似资产却无法可靠解析的形态一律 *AssetScanError，由 plan 以 E2 fail closed。
	if err := sc.precheck(doc); err != nil {
		return nil, err
	}

	if err := sc.walk(doc, 0, 0, 1); err != nil {
		return nil, err
	}
	// 脚注定义在 AST 里被搬到文末的 FootnoteList，其事件按真实物理行归位；同行内保持阅读顺序
	// （稳定排序保留 walk 追加序）。
	sort.SliceStable(sc.events, func(i, j int) bool {
		return sc.events[i].StartLine < sc.events[j].StartLine
	})
	return sc.events, nil
}

type scanner struct {
	src        []byte
	lineStarts []int // lineStarts[k] = 第 (k+1) 行的起始字节偏移
	numLines   int
	fnLabel    map[int]string
	events     []AssetEvent
}

// indexLines 预计算物理行起点（口径同 bodyPhysicalLines：以 \n 切分，末尾换行不制造虚假尾行）。
func (s *scanner) indexLines() {
	s.lineStarts = []int{0}
	for i := 0; i < len(s.src); i++ {
		if s.src[i] == '\n' && i+1 <= len(s.src) {
			s.lineStarts = append(s.lineStarts, i+1)
		}
	}
	// 末尾换行后的空段不算一行。
	n := len(s.lineStarts)
	if n > 1 && s.lineStarts[n-1] == len(s.src) {
		s.lineStarts = s.lineStarts[:n-1]
	}
	s.numLines = len(s.lineStarts)
}

// lineOf 把字节偏移映射成 1 基物理行号（off 落在最后一行之后时钳到最后一行）。
func (s *scanner) lineOf(off int) int {
	if off < 0 {
		off = 0
	}
	lo, hi := 0, len(s.lineStarts)-1
	ans := 0
	for lo <= hi {
		mid := (lo + hi) / 2
		if s.lineStarts[mid] <= off {
			ans = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return ans + 1
}

// lineText 返回第 n 行（1 基）的原始字节（不含行尾 \n）；越界返回 nil。
func (s *scanner) lineText(n int) []byte {
	if n < 1 || n > s.numLines {
		return nil
	}
	start := s.lineStarts[n-1]
	end := len(s.src)
	if n < s.numLines {
		end = s.lineStarts[n] - 1 // 去掉行尾 \n
	} else if end > start && s.src[end-1] == '\n' {
		end--
	}
	return s.src[start:end]
}

func (s *scanner) emit(kind AssetKind, sig, id string, start, end int) {
	if end < start {
		end = start
	}
	// 普通事件：完整归属范围（scope）等于自身真实 span。
	s.events = append(s.events, AssetEvent{
		Kind: kind, Sig: sig, ID: id,
		StartLine: start, EndLine: end,
		ScopeStartLine: start, ScopeEndLine: end,
	})
}

// emitScoped 追加一条真实 span 与「完整归属范围」不同的事件（目前仅表格行：真实行号定位其排序位置，
// 整张 Table 的 scope 供来源 / 目标侧做「不可切分容器」的容纳判定）。
func (s *scanner) emitScoped(kind AssetKind, sig, id string, start, end, scopeStart, scopeEnd int) {
	if end < start {
		end = start
	}
	if scopeEnd < scopeStart {
		scopeEnd = scopeStart
	}
	s.events = append(s.events, AssetEvent{
		Kind: kind, Sig: sig, ID: id,
		StartLine: start, EndLine: end,
		ScopeStartLine: scopeStart, ScopeEndLine: scopeEnd,
	})
}

// walk 深度优先遍历 AST，产出资产事件；命中原始 HTML 资产形态即返回结构扫描错误。
//
//	bqDepth   已进入的 blockquote 层数（引用嵌套深度）
//	listDepth 已进入的 list 层数（列表项嵌套深度）
//	fbLine    最近一个含行信息的祖先块首行——供无自身行信息的行内节点兜底定位
func (s *scanner) walk(n ast.Node, bqDepth, listDepth, fbLine int) error {
	switch t := n.(type) {
	case *ast.Image:
		line := s.nodeStartLine(n, fbLine)
		s.emit(AssetImage, "image|"+string(t.Destination), string(t.Destination), line, line)
		if alt := inlineText(s.src, n); len(bytes.TrimSpace(alt)) > 0 {
			s.emit(AssetCaption, "caption", "", line, line)
		}
		return nil // alt 文字为图注本体，视作不透明，不再深入

	case *ast.Link:
		line := s.nodeStartLine(n, fbLine)
		s.emit(AssetLink, "link|"+string(t.Destination), string(t.Destination), line, line)
		// 链接文字里可能嵌图片（[![img](i)](url)）——继续深入捕获，图片仍以 Image 节点计、不双计 link。

	case *ast.AutoLink:
		line := s.nodeStartLine(n, fbLine)
		url := string(t.URL(s.src))
		s.emit(AssetLink, "link|"+url, url, line, line)
		return nil

	case *east.FootnoteLink:
		line := s.nodeStartLine(n, fbLine)
		label := s.fnLabel[t.Index]
		s.emit(AssetFootnote, "footnote|ref|"+label, "ref:"+label, line, line)
		return nil

	case *east.Footnote:
		start, end := s.blockSpan(n, fbLine)
		label := normalizeFootnoteLabel(string(t.Ref))
		s.emit(AssetFootnote, "footnote|def|"+label, "def:"+label, start, end)
		// 定义正文里可能含链接 / 图片——继续深入，但兜底行改为定义首行。
		fbLine = start

	case *east.FootnoteBacklink:
		return nil // 渲染回链，非来源资产

	case *east.Table:
		// 表格行的**真实 span** 取所在表头 / 数据行的真实物理行；**完整归属范围（scope）**取整张
		// Table 容器（含表头、delimiter 与全部数据行）。这样：①同一张表被两个 source_ref / omission
		// 拆开时，按 scope（整表）能检出切断；②单元格里的 link / image 按其真实行与本行事件相邻排序，
		// 不会因所有 row 事件都塌到表首行而被排到全部行之后（否则资产从一行移到另一行会假绿）。
		ts, te := s.blockSpan(n, fbLine)
		for row := n.FirstChild(); row != nil; row = row.NextSibling() {
			role := "body"
			if _, isHead := row.(*east.TableHeader); isHead {
				role = "header"
			}
			cells := row.ChildCount()
			rs, re := s.blockSpan(row, ts) // 该行真实物理行 span（定位排序位置）
			s.emitScoped(AssetTableRow, fmt.Sprintf("table_row|%s|%d", role, cells),
				fmt.Sprintf("%s×%d", role, cells), rs, re, ts, te)
			// 单元格内的行内资产（图片 / 链接）继续深入捕获，兜底行用该行真实首行。
			for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
				if err := s.walk(cell, bqDepth, listDepth, rs); err != nil {
					return err
				}
			}
		}
		return nil

	case *ast.FencedCodeBlock:
		start, end, ok := s.fenceExtent(n)
		if !ok {
			return &AssetScanError{Reason: "未闭合的围栏代码块（缺少匹配长度的收尾围栏），无法可靠界定代码资产", Line: start}
		}
		payload := normalizeCode(t.Lines().Value(s.src))
		s.emit(AssetCode, "code|"+string(payload), codePreview(payload), start, end)
		return nil

	case *ast.CodeBlock:
		start, end := s.blockSpan(n, fbLine)
		payload := normalizeCode(t.Lines().Value(s.src))
		s.emit(AssetCode, "code|"+string(payload), codePreview(payload), start, end)
		return nil

	case *ast.Blockquote:
		start, end := s.blockSpan(n, fbLine)
		bqDepth++
		s.emit(AssetBlockquote, fmt.Sprintf("blockquote|%d", bqDepth), fmt.Sprintf("depth%d", bqDepth), start, end)
		fbLine = start

	case *ast.List:
		listDepth++ // 列表项深度 = 所处 List 的嵌套层数
		fbLine = s.nodeStartLine(n, fbLine)

	case *ast.ListItem:
		start, end := s.blockSpan(n, fbLine)
		ordered := "unordered"
		if p, ok := n.Parent().(*ast.List); ok && p.IsOrdered() {
			ordered = "ordered"
		}
		s.emit(AssetListItem, fmt.Sprintf("list_item|%d|%s", listDepth, ordered),
			fmt.Sprintf("depth%d/%s", listDepth, ordered), start, end)
		fbLine = start

	case *ast.CodeSpan:
		return nil // 行内代码：伪资产语法被屏蔽，内部不含可解析资产

	case *ast.RawHTML:
		if seg := t.Segments; seg != nil && seg.Len() > 0 {
			raw := stripHTMLComments(t.Segments.Value(s.src))
			if reRawHTMLAsset.Match(raw) {
				return &AssetScanError{Reason: "行内原始 HTML 含资产标签（img/figure/figcaption/a/table/pre/code），无法可靠解析为结构资产", Line: s.lineOf(t.Segments.At(0).Start)}
			}
		}
		return nil

	case *ast.HTMLBlock:
		raw := stripHTMLComments(t.Lines().Value(s.src))
		var firstLine int
		if t.Lines().Len() > 0 {
			firstLine = s.lineOf(t.Lines().At(0).Start)
		}
		if reRawHTMLAsset.Match(raw) {
			return &AssetScanError{Reason: "原始 HTML 块含资产标签（img/figure/figcaption/a/table/pre/code），无法可靠解析为结构资产", Line: firstLine}
		}
		return nil

	case *ast.Heading, *ast.Paragraph, *ast.TextBlock:
		fbLine = s.nodeStartLine(n, fbLine)
	}

	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if err := s.walk(c, bqDepth, listDepth, fbLine); err != nil {
			return err
		}
	}
	return nil
}

// nodeStartLine 定位节点起点物理行：优先 goldmark 的 node.Pos()（未定义时返回 -1），
// 再回退子树里第一个 Text 段的起点，最后用兜底行（供空 alt 图片 / 空围栏等无 Pos 场景兜底）。
func (s *scanner) nodeStartLine(n ast.Node, fbLine int) int {
	if p := n.Pos(); p >= 0 {
		return s.lineOf(p)
	}
	if off, ok := firstTextOffset(n); ok {
		return s.lineOf(off)
	}
	if lo, _, ok := subtreeOffsets(n); ok {
		return s.lineOf(lo)
	}
	return fbLine
}

// blockSpan 返回块级节点的物理行闭区间：起点用 nodeStartLine（Pos 优先），终点取 Lines() 末段
// 与后代所有可定位段最大终点的较大者（覆盖多行 / 松散块的续行）。
func (s *scanner) blockSpan(n ast.Node, fbLine int) (int, int) {
	start := s.nodeStartLine(n, fbLine)
	end := start
	if ls := n.Lines(); ls != nil && ls.Len() > 0 {
		if e := s.lineOf(ls.At(ls.Len()-1).Stop - 1); e > end {
			end = e
		}
	}
	if _, hi, ok := subtreeOffsets(n); ok {
		if e := s.lineOf(hi - 1); e > end {
			end = e
		}
	}
	return start, end
}

// fenceExtent 计算一个围栏代码块的物理行闭区间（含起止围栏行）。起点用 goldmark 的 node.Pos()——
// 它是去掉 blockquote / 列表等容器前缀后开围栏字符的真实偏移，故顶层与容器内围栏都能定位（不对整行
// 前缀硬编码）。围栏字符 / 长度取自 marker offset 处；内容末行由 content Lines 决定（空围栏末行即
// 开围栏行）；收尾围栏须紧邻内容末行下一行、且在其容器上下文里长度足够。找不到合法收尾围栏（含跑到
// 文末 / 容器末的真正未闭合围栏）→ ok=false，由调用方 fail closed。
func (s *scanner) fenceExtent(n ast.Node) (start, end int, ok bool) {
	p := n.Pos()
	if p < 0 {
		return 1, 1, false
	}
	start = s.lineOf(p)
	line := s.lineText(start)
	col := p - s.lineStarts[start-1]
	if col < 0 || col >= len(line) {
		return start, start, false
	}
	ch := line[col]
	if ch != '`' && ch != '~' {
		return start, start, false
	}
	run := 0
	for col+run < len(line) && line[col+run] == ch {
		run++
	}
	if run < 3 {
		return start, start, false
	}
	lastContent := start // 空围栏：无内容行，收尾围栏紧跟开围栏行
	if ls := n.Lines(); ls != nil && ls.Len() > 0 {
		lastContent = s.lineOf(ls.At(ls.Len()-1).Stop - 1)
	}
	closer := lastContent + 1
	if closer <= s.numLines && isContainerFenceCloser(s.lineText(closer), ch, run) {
		return start, closer, true
	}
	return start, start, false // 未闭合
}

// isContainerFenceCloser 判断一行是否为匹配某开围栏（字符 ch、长度 openRun）的收尾围栏：先剥离
// 容器前缀（blockquote 的 `>` 标记与空白 / 制表符），再要求同字符、长度 >= openRun，其后只余空白。
// 剥离整段前导容器前缀（而非硬编码至多 3 空格）是因为 goldmark 已确定内容边界、本函数只据此确认
// 「这一行确是收尾围栏」；列表续行可能缩进多于 3 空格、blockquote 收尾带 `>` 前缀。
func isContainerFenceCloser(line []byte, ch byte, openRun int) bool {
	i := 0
	for i < len(line) && (line[i] == '>' || line[i] == ' ' || line[i] == '\t') {
		i++
	}
	run := 0
	for i < len(line) && line[i] == ch {
		run++
		i++
	}
	if run < openRun {
		return false
	}
	for ; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' && line[i] != '\r' {
			return false
		}
	}
	return true
}

// —— 保守预检（在只读 AST + 屏蔽视图上跑）——

// precheck 在屏蔽了围栏 / 缩进代码、行内代码、HTML 注释、转义标点的正文视图上，检出「看似资产却
// 无法可靠解析」的形态：未闭合围栏、畸形 image/link（destination 未闭合）、悬空脚注引用、悬空
// reference link（用了引用式 label 却无对应定义）。代码区间按 goldmark 定位（含 blockquote /
// 列表容器内围栏），故容器内合法代码里的伪 malformed / dangling 片段不会被误报。
func (s *scanner) precheck(doc ast.Node) error {
	masked := make([]byte, len(s.src))
	copy(masked, s.src)

	// ① 代码区间：按 goldmark AST 定位并整行 / 整段屏蔽围栏 / 缩进代码；未闭合围栏在此 fail closed。
	if err := s.maskCodeBlocks(doc, masked); err != nil {
		return err
	}

	// ② 转义标点：`\X`（X 为 ASCII 标点）不构成语法，先中和，避免 `\`` 误开行内代码、`\]` 误配
	//    `](`。③ 行内代码：等长反引号 run 之间的内容屏蔽。④ HTML 注释屏蔽。
	maskEscapes(masked)
	maskInlineCode(masked)
	maskCommentBytes(masked)

	// ⑤ 畸形 image/link：未转义的 `](` 之后 destination 在本行内未闭合（右括号缺失）→ fail closed。
	for p := 0; p+1 < len(masked); p++ {
		if masked[p] != ']' || masked[p+1] != '(' {
			continue
		}
		depth, q := 1, p+2
		for q < len(masked) && masked[q] != '\n' {
			if masked[q] == '(' {
				depth++
			} else if masked[q] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
			q++
		}
		if depth != 0 {
			return &AssetScanError{Reason: "疑似 image/link 的 destination 未闭合（缺少匹配的右括号），无法可靠解析为链接 / 图片资产", Line: s.lineOf(p)}
		}
		p = q
	}

	// ⑥ 悬空脚注引用；⑦ 悬空 reference link（含 reference image）：均在屏蔽视图上比对定义 label。
	if err := s.precheckDanglingFootnotes(masked); err != nil {
		return err
	}
	if err := s.precheckDanglingRefLinks(masked); err != nil {
		return err
	}
	// ⑧ 脚注 ref↔def 的规范化归一与 goldmark 的**逐字节**匹配一致性：屏蔽视图里真实脚注引用的
	//    数量 / 规范化 label 必须与 AST 实际产出的 FootnoteLink 一一对应，否则（如同文档 `[^A]`
	//    引用配 `[^a]:` 定义：预检按规范化视作成对、goldmark 却因大小写不同不匹配而双双退化）
	//    会静默漏掉脚注资产 → fail closed。
	return s.precheckFootnoteConsistency(doc, masked)
}

// maskCodeBlocks 遍历只读 AST，把围栏 / 缩进代码块的物理行整段改成空格（屏蔽其内容，供后续按字节 /
// 正则扫描不误踩代码里的伪语法）。围栏用 fenceExtent 精确界定（含容器内围栏）；未闭合围栏此处直接
// fail closed（返回 *AssetScanError，定位到开围栏行）。
func (s *scanner) maskCodeBlocks(doc ast.Node, masked []byte) error {
	var scanErr error
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		if scanErr != nil {
			return
		}
		switch n.(type) {
		case *ast.FencedCodeBlock:
			start, end, ok := s.fenceExtent(n)
			if !ok {
				scanErr = &AssetScanError{Reason: "未闭合的围栏代码块（缺少匹配长度的收尾围栏），无法可靠界定代码资产", Line: start}
				return
			}
			for l := start; l <= end; l++ {
				s.maskLine(masked, l)
			}
			return // 内容不透明，无需深入
		case *ast.CodeBlock:
			if ls := n.Lines(); ls != nil && ls.Len() > 0 {
				start := s.lineOf(ls.At(0).Start)
				endL := s.lineOf(ls.At(ls.Len()-1).Stop - 1)
				for l := start; l <= endL; l++ {
					s.maskLine(masked, l)
				}
			}
			return
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(doc)
	return scanErr
}

// fnRef 是屏蔽视图里一个**真实脚注引用**的规范化 label 与命中偏移。
type fnRef struct {
	label string
	off   int
}

// scanFootnoteRefs 在屏蔽视图上收集**真正的脚注引用**（返回规范化 label + 偏移），剔除以下并非
// 脚注引用的 `[^…]` 命中：
//   - 紧跟 `:`：这是脚注定义 `[^label]:` 本身；
//   - 紧跟 `(` 或 `[`：inline / reference 的 image/link 文字（如 `![^x](p)` / `![^x][r]` /
//     `[^x](u)` / `[^x][r]`，goldmark 按 image/link 而非脚注解析）；
//   - 前缀为未转义 `!`：image 的 alt 文字（`![^x]…`；屏蔽视图已把转义 `\!` 中和为空格，
//     故此处出现的 `!` 必为未转义）。
func (s *scanner) scanFootnoteRefs(ms string) []fnRef {
	var out []fnRef
	for _, loc := range reFootnoteRef.FindAllStringSubmatchIndex(ms, -1) {
		matchStart, matchEnd, g1s, g1e := loc[0], loc[1], loc[2], loc[3]
		if matchEnd < len(ms) {
			if c := ms[matchEnd]; c == ':' || c == '(' || c == '[' {
				continue
			}
		}
		if matchStart > 0 && ms[matchStart-1] == '!' {
			continue // image alt（`![^…]`），非脚注引用
		}
		out = append(out, fnRef{label: normalizeFootnoteLabel(ms[g1s:g1e]), off: matchStart})
	}
	return out
}

// precheckDanglingFootnotes 在屏蔽视图上收集脚注定义 label，检出引用了不存在定义的悬空引用。
// 只对**真正的脚注引用**判定（见 scanFootnoteRefs：剔除定义本身、image/link 文字、image alt）。
func (s *scanner) precheckDanglingFootnotes(masked []byte) error {
	ms := string(masked)
	defs := map[string]bool{}
	for _, m := range reFootnoteDef.FindAllStringSubmatch(ms, -1) {
		defs[normalizeFootnoteLabel(m[1])] = true
	}
	for _, ref := range s.scanFootnoteRefs(ms) {
		if !defs[ref.label] {
			return &AssetScanError{Reason: "悬空 footnote 引用（引用了不存在的定义 label），无法可靠解析为脚注资产", Line: s.lineOf(ref.off)}
		}
	}
	return nil
}

// precheckFootnoteConsistency 核对「屏蔽视图里的脚注引用 / 定义」与「AST 实际保留的 FootnoteLink /
// Footnote 定义」是否按**规范化 label 的多重集**一一对应。两条不变量都要守：
//
//   - 引用侧：goldmark 的 Footnote 扩展按**原始字节**匹配 ref↔def，同一文档里 `[^A]` 引用配 `[^a]:`
//     定义会因大小写不同而双双退化成普通文本，本包却因规范化把二者视作成对而不报悬空——静默漏掉一对
//     脚注资产。故屏蔽视图数出的真实脚注引用多重集必须与 AST 的 FootnoteLink（经规范化 label）相等。
//   - 定义侧：goldmark 的 footnote transformer 会**移除任何未被引用命中的定义**（连同定义正文里的
//     链接 / 图片一并从 AST 消失），重复定义只保留其一，匹配失败的定义也被丢弃。若只核对引用多重集，
//     Source 只有「未引用定义」时 ScanAssets 会返回空、目标删掉该定义也能蒙混（假绿）。故屏蔽视图数出
//     的定义多重集必须与 AST 实际保留的 Footnote 定义（经规范化 label）相等：未引用 / 重复 / 因匹配
//     失败而被移除的定义都会在此露馅。
//
// 任一不变量被破坏 → *AssetScanError（fail closed）。跨 Source / target 两份文档各自内部一致（大小写 /
// 空白仅在两文档之间不同）的合法改名不受影响：每份文档内 ref 与 def 字节一致，goldmark 正常配对且定义
// 被引用命中而保留。
func (s *scanner) precheckFootnoteConsistency(doc ast.Node, masked []byte) error {
	ms := string(masked)

	// 屏蔽视图：真实脚注引用（多重集 + 首个偏移）与脚注定义（多重集 + 首个偏移）。
	viewRefs := map[string]int{}
	refOff := map[string]int{}
	for _, r := range s.scanFootnoteRefs(ms) {
		viewRefs[r.label]++
		if _, ok := refOff[r.label]; !ok {
			refOff[r.label] = r.off
		}
	}
	viewDefs := map[string]int{}
	defOff := map[string]int{}
	for _, loc := range reFootnoteDef.FindAllStringSubmatchIndex(ms, -1) {
		label := normalizeFootnoteLabel(ms[loc[2]:loc[3]])
		viewDefs[label]++
		if _, ok := defOff[label]; !ok {
			defOff[label] = loc[0]
		}
	}

	// AST：实际产出的 FootnoteLink 与实际保留的 Footnote 定义（均经规范化 label）。
	astRefs := map[string]int{}
	astDefs := map[string]int{}
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		switch t := n.(type) {
		case *east.FootnoteLink:
			astRefs[s.fnLabel[t.Index]]++
		case *east.Footnote:
			astDefs[normalizeFootnoteLabel(string(t.Ref))]++
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(doc)

	if !multisetEqual(viewRefs, astRefs) {
		return &AssetScanError{
			Reason: "脚注引用与定义的规范化归一同 goldmark 的逐字节匹配不一致（疑似同一文档内 `[^Ref]` 与 " +
				"`[^ref]:` 大小写 / 空白不一致，goldmark 未配对而双双退化），无法可靠解析为脚注资产",
			Line: multisetExcessLine(s, viewRefs, astRefs, refOff),
		}
	}
	if !multisetEqual(viewDefs, astDefs) {
		return &AssetScanError{
			Reason: "脚注定义与 AST 实际保留的定义不一致（未被任何引用命中、重复、或因逐字节匹配失败而被 " +
				"goldmark 移除的定义会连同其正文一并从 AST 消失），无法可靠解析为脚注资产",
			Line: multisetExcessLine(s, viewDefs, astDefs, defOff),
		}
	}
	return nil
}

// multisetEqual 报告两个 label→计数多重集是否逐项相等。
func multisetEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// multisetExcessLine 找出屏蔽视图多出（view 计数 > ast 计数）的一个 label，返回其首个命中行；
// 找不到（罕见：AST 多于视图）则返回 0（未定位）。
func multisetExcessLine(s *scanner, view, astSide, off map[string]int) int {
	for label, n := range view {
		if astSide[label] < n {
			if o, ok := off[label]; ok {
				return s.lineOf(o)
			}
		}
	}
	return 0
}

// precheckDanglingRefLinks 在屏蔽视图上检出**悬空 reference link / reference image**：引用式写法
// （full `[text][label]` 或 collapsed `[label][]`，含 `![...][...]` 图片）用了链接引用 label，
// 却没有对应的 `[label]: dest` 定义——goldmark 会把它退化成普通文本，与目标侧一同退化就可能「假绿」，
// 故 fail closed。合法的普通方括号文本（shortcut `[text]`，无第二个 `[...]`）不在此列，继续放行。
func (s *scanner) precheckDanglingRefLinks(masked []byte) error {
	ms := string(masked)
	defs := map[string]bool{}
	for _, m := range reLinkRefDef.FindAllStringSubmatch(ms, -1) {
		defs[normalizeFootnoteLabel(m[1])] = true
	}
	report := func(loc []int, label string) error {
		if defs[normalizeFootnoteLabel(label)] {
			return nil
		}
		return &AssetScanError{Reason: "悬空 reference link（引用了不存在的链接定义 label），无法可靠解析为链接 / 图片资产", Line: s.lineOf(loc[0])}
	}
	for _, loc := range reRefLinkFull.FindAllStringSubmatchIndex(ms, -1) {
		if err := report(loc, ms[loc[2]:loc[3]]); err != nil {
			return err
		}
	}
	for _, loc := range reRefLinkCollapsed.FindAllStringSubmatchIndex(ms, -1) {
		if err := report(loc, ms[loc[2]:loc[3]]); err != nil {
			return err
		}
	}
	return nil
}

// maskLine 把第 n 行（1 基）除行尾 \n 外的字节全部改成空格（保留行结构，供后续按字节 / 正则扫描）。
func (s *scanner) maskLine(buf []byte, n int) {
	if n < 1 || n > s.numLines {
		return
	}
	start := s.lineStarts[n-1]
	end := len(s.src)
	if n < s.numLines {
		end = s.lineStarts[n] - 1
	} else if end > start && s.src[end-1] == '\n' {
		end--
	}
	for i := start; i < end && i < len(buf); i++ {
		buf[i] = ' '
	}
}

// maskEscapes 把 `\X`（X 为 ASCII 标点）两字节都改成空格：转义序列不构成任何 Markdown 语法。
func maskEscapes(buf []byte) {
	for i := 0; i+1 < len(buf); i++ {
		if buf[i] == '\\' && isASCIIPunct(buf[i+1]) {
			buf[i], buf[i+1] = ' ', ' '
			i++
		}
	}
}

// maskInlineCode 把行内代码 span（等长反引号 run 之间的内容，含起止 run）改成空格：
// 保守屏蔽，未配对的 run 保留原样。换行不改写以保住行结构。
func maskInlineCode(buf []byte) {
	i := 0
	for i < len(buf) {
		if buf[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(buf) && buf[j] == '`' {
			j++
		}
		runLen := j - i
		// 找长度相等的收尾 run。
		k := j
		found := -1
		for k < len(buf) {
			if buf[k] != '`' {
				k++
				continue
			}
			e := k
			for e < len(buf) && buf[e] == '`' {
				e++
			}
			if e-k == runLen {
				found = e
				break
			}
			k = e
		}
		if found < 0 {
			i = j // 无配对：跳过这段 run，不屏蔽
			continue
		}
		for p := i; p < found; p++ {
			if buf[p] != '\n' {
				buf[p] = ' '
			}
		}
		i = found
	}
}

// maskCommentBytes 把 HTML 注释（含 <!-- 与 -->）改成空格（换行保留）：注释里的 img/a 只是文本。
func maskCommentBytes(buf []byte) {
	for _, loc := range reHTMLComment.FindAllIndex(buf, -1) {
		for i := loc[0]; i < loc[1]; i++ {
			if buf[i] != '\n' {
				buf[i] = ' '
			}
		}
	}
}

// isASCIIPunct 报告 b 是否为 CommonMark 认可的可转义 ASCII 标点。
func isASCIIPunct(b byte) bool {
	switch {
	case b >= '!' && b <= '/':
		return true
	case b >= ':' && b <= '@':
		return true
	case b >= '[' && b <= '`':
		return true
	case b >= '{' && b <= '~':
		return true
	}
	return false
}

// stripHTMLComments 去掉字节里的所有 HTML 注释段（用于原始 HTML 资产判定：注释内的 img/a 不算资产）。
func stripHTMLComments(b []byte) []byte {
	return reHTMLComment.ReplaceAll(b, nil)
}

// normalizeFootnoteLabel 按解析语义规范化脚注 label：折叠内部空白为单空格、去首尾空白、大小写折叠。
// 使 `[^Foo Bar]` 与 `[^foo  bar]` 归一；真正改变 label（如 a→b）仍产生不同签名。
func normalizeFootnoteLabel(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// normalizeCode 只把 CRLF 规范成 LF，其余字节精确保留（契约 §4.2.1：代码 payload 除换行外精确）。
func normalizeCode(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// codePreview 给代码事件一个短人读标识（首行 trim 后截断），仅用于诊断、不入签名。
func codePreview(payload []byte) string {
	line := payload
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = bytes.TrimSpace(line)
	const max = 24
	if len(line) > max {
		return string(line[:max]) + "…"
	}
	if len(line) == 0 {
		return "空代码块"
	}
	return string(line)
}

// footnoteLabels 收集文末 FootnoteList 里每个脚注定义的 index→**规范化 label**，供脚注引用回指。
func footnoteLabels(doc ast.Node) map[int]string {
	m := map[int]string{}
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		if fn, ok := n.(*east.Footnote); ok {
			m[fn.Index] = normalizeFootnoteLabel(string(fn.Ref))
		}
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(doc)
	return m
}

// inlineText 拼接节点子树里所有 Text / String 段的字节值（图片 alt / 链接文字用）。
func inlineText(src []byte, n ast.Node) []byte {
	var out []byte
	var visit func(x ast.Node)
	visit = func(x ast.Node) {
		switch t := x.(type) {
		case *ast.Text:
			out = append(out, t.Segment.Value(src)...)
		case *ast.String:
			out = append(out, t.Value...)
		}
		for c := x.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(n)
	return out
}

// firstTextOffset 返回子树里第一个 Text 段的起始偏移（用于行内节点定位）。
func firstTextOffset(n ast.Node) (int, bool) {
	if t, ok := n.(*ast.Text); ok {
		return t.Segment.Start, true
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if off, ok := firstTextOffset(c); ok {
			return off, true
		}
	}
	return 0, false
}

// subtreeOffsets 返回子树里所有可定位段的最小起点与最大终点（并入块级 Lines() 与行内 Text 段）。
func subtreeOffsets(n ast.Node) (lo, hi int, ok bool) {
	lo = 1<<62 - 1
	hi = -1
	var visit func(x ast.Node)
	visit = func(x ast.Node) {
		if x.Type() == ast.TypeBlock {
			if ls := x.Lines(); ls != nil && ls.Len() > 0 {
				if a := ls.At(0).Start; a < lo {
					lo = a
				}
				if b := ls.At(ls.Len() - 1).Stop; b > hi {
					hi = b
				}
			}
		}
		if t, is := x.(*ast.Text); is {
			if t.Segment.Start < lo {
				lo = t.Segment.Start
			}
			if t.Segment.Stop > hi {
				hi = t.Segment.Stop
			}
		}
		for c := x.FirstChild(); c != nil; c = c.NextSibling() {
			visit(c)
		}
	}
	visit(n)
	if hi < 0 {
		return 0, 0, false
	}
	return lo, hi, true
}
