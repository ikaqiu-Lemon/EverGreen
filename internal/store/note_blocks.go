package store

// `write_note` 的**有序块**落盘形态（Schema v2 契约 §4.2 形态 / §5.1 渲染）。
//
// # 为什么渲染落在 store 而不是 plan
//
// §16.3 的写路径硬约束把「按模板拼字节」这件事收在本包：plan 只做校验与展开，
// 把用户内容以 []byte 原样交给写入侧，自己不做规范化。有序块要变成「整理正文」
// 分区的字节，本质就是一次按模板拼装（H3 标题 + 正文 + Agent 补充标记），
// 因此它的唯一实现必须在这里，plan 只调用 NoteBlockBytes 拿结果。
//
// 反过来说：如果 plan 自己拼这段字节，`write_note` 的落盘形态就会有两处口径
// （新建走 plan 拼、重新加工走 store 追加），两处必然漂移。
//
// # 为什么 role: agent 用 blockquote + 粗体标记
//
// 契约 §5.1 的三条理由逐字落地：(a) 任何 Markdown 渲染器都看得见；(b) 是纯文本，
// 不破坏字节保真读写；(c) 能被 mdfile.SplitBlocks 正确切块（blockquote 的每一行都是
// 非空行，整段落成一个 paragraph 块，不会把 Agent 补充与来源正文粘成一块）。
//
// # 顺序即语义
//
// 数组顺序就是落盘顺序：本文件**不排序、不去重、不重排、不合并**。
// Note 的价值在于「保持原文的章节顺序、论证顺序、叙事顺序」（契约 §2.1），
// 任何一次「顺手排一下」都会把整理版文章变回摘要。

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// NoteBlockRole 是一个有序块的来源角色（**二值封闭枚举**，契约 §4.2 第 2 条）。
type NoteBlockRole string

// 两个角色。source = 来源内容的整理；agent = Agent 就近补充。
//
// 不设第三值：Note 里的每一段字要么来自原文、要么是 Agent 加的，
// 「不确定来自哪」不是一种可落盘的状态——它是一个必须在生成时解决的问题。
const (
	NoteBlockSource NoteBlockRole = "source"
	NoteBlockAgent  NoteBlockRole = "agent"
)

// NoteBlockRoles 返回封闭枚举的全部取值（顺序即契约 §4.2 的声明序）。
func NoteBlockRoles() []NoteBlockRole { return []NoteBlockRole{NoteBlockSource, NoteBlockAgent} }

// Valid 报告角色是否落在封闭枚举内。
func (r NoteBlockRole) Valid() bool {
	for _, ok := range NoteBlockRoles() {
		if r == ok {
			return true
		}
	}
	return false
}

// AgentBlockMarker 是 `role: agent` 块的**逐字**行首标记（契约 §5.1）。
//
// 单列成常量：渲染、读路径识别与用例断言共用同一份字面量，不各写一遍。
const AgentBlockMarker = "> **[Agent 补充]** "

// agentQuotePrefix 是 Agent 补充块**续行**的引用前缀（首行用 AgentBlockMarker）。
const agentQuotePrefix = "> "

// ErrNoteBlockRole 是块角色越界：二值封闭枚举之外的取值不落盘。
var ErrNoteBlockRole = errors.New("有序块的 role 不在封闭二值枚举内")

// ErrNoteBlockEmpty 是块正文为空：空块没有可整理的内容，不落盘。
var ErrNoteBlockEmpty = errors.New("有序块的 body 为空")

// NoteBlock 是「整理正文」里的一个有序块。
//
// Heading 可选：给出时渲染为 **H3**（契约 §4.2 第 3 条），用于保持原文章节结构。
// 之所以是 H3 而不是 H2：H2 是分区骨架，Note 的固定四分区靠它切分，
// 原文章节若也用 H2 就会把一篇 Note 切成十几个「未知分区」。
// SourceRef / Annotation / Label 是 Schema v2 §4.2 的**审阅式 Note 元数据**，由解析层
// （internal/plan）原样携带过来：source 块用 SourceRef 标出对应 Source 正文行段
// （形如 `L<start>-L<end>`，是行段引用而非批注），agent 块用 Annotation 标出内置批注类型、
// Label 承载扩展批注的人读标签。三者都是**尚未参与落盘渲染**的元数据——NoteBlockBytes 当前一个字节
// 都不读它们（T12-1 边界：只承载、不改 writer 输出）。渲染消费留待后续批次接入。
type NoteBlock struct {
	Role       NoteBlockRole
	Heading    string
	Body       []byte
	SourceRef  string
	Annotation string
	Label      string
}

// NoteBlockBytes 把有序块渲染成「整理正文」的分区正文字节。
//
// 落盘形态（契约 §5.1）：
//
//	### <heading>
//
//	<source 块正文逐字>
//
//	> **[Agent 补充]** <agent 块正文，续行以 "> " 引用>
//
// 块之间恰一个空行分隔（mdfile.SplitBlocks 的块分隔符），返回字节以 \n 结束。
// source 块的正文**逐字**落盘，一个字节都不改写；agent 块只在行首加引用前缀，
// 行内文本同样逐字。
func NoteBlockBytes(blocks []NoteBlock) ([]byte, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("%w：blocks[] 为空", ErrNoteBlockEmpty)
	}
	var out []byte
	for i, b := range blocks {
		if !b.Role.Valid() {
			return nil, fmt.Errorf("%w：blocks[%d].role=%q，恰两值 %v",
				ErrNoteBlockRole, i, b.Role, NoteBlockRoles())
		}
		body := bytes.Trim(b.Body, "\n")
		if len(bytes.TrimSpace(body)) == 0 {
			return nil, fmt.Errorf("%w：blocks[%d]", ErrNoteBlockEmpty, i)
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		if b.Heading != "" {
			out = append(out, "### "...)
			out = append(out, b.Heading...)
			out = append(out, '\n', '\n')
		}
		if b.Role == NoteBlockAgent {
			out = append(out, quoteAgentBody(body)...)
		} else {
			out = append(out, body...)
			out = append(out, '\n')
		}
	}
	return out, nil
}

// quoteAgentBody 把 Agent 补充块渲染成 blockquote：首行带 AgentBlockMarker，
// 续行带 "> "（空行渲染为 ">"，否则 blockquote 会被截断成两段）。
// 行内文本逐字不动。
func quoteAgentBody(body []byte) []byte {
	lines := bytes.Split(body, []byte("\n"))
	out := make([]byte, 0, len(body)+len(AgentBlockMarker)+2*len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, AgentBlockMarker...)
			out = append(out, line...)
			out = append(out, '\n')
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			out = append(out, ">\n"...)
			continue
		}
		out = append(out, agentQuotePrefix...)
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

// NoteExtraction 是「提取结果」分区的两组清单（契约 §5.1）。
//
// 定义在 store 而不是 plan：渲染的唯一实现在 ExtractionList（写路径硬约束 §16.3），
// 而结构体与它的渲染函数分家的第一个后果就是「加一组清单」时只改了其中一处。
// plan 侧以类型别名引用它（同 NoteBlock 的处理），因此不存在逐字段拷贝的转换函数。
//
// 分组本身**不在**这里做：按 ID 前缀分组会产出诊断（前缀既非 `k-` 也非 `o-` 的条目
// 记一条 I1），而 store 不产出 plan 诊断。分组只发生在 plan 侧一处。
type NoteExtraction struct {
	Knowledge []string
	Opinions  []string
}

// Empty 报告两组是否都为空（都空则不写「提取结果」的正文）。
func (e *NoteExtraction) Empty() bool {
	return e == nil || (len(e.Knowledge) == 0 && len(e.Opinions) == 0)
}

// Bytes 把两组清单渲染成「提取结果」的分区正文字节。
//
// 命名不带 `Render` 前缀：B1 用「写形态」名字前缀（Write / Create / Append / Render …）
// 界定本包的**写入口恰三个**，纯字节格式化函数一旦叫 Render* 就会被计入写形态，
// 让「三个写入口」这条结构性判据失真。函数做的是拼字节、不碰磁盘，Bytes 才是准确的名字。
func (e *NoteExtraction) Bytes() []byte {
	if e == nil {
		return nil
	}
	return ExtractionList(e.Knowledge, e.Opinions)
}

// ExtractionList 把「提取结果」的两组清单渲染成分区正文字节。
//
// 形态（契约 §5.1）：Knowledge 与 Opinion 各一个 H3 小节，条目逐行 `- <item>`。
// 空组不写小节——空的 H3 只会让读者以为「这里本该有东西但丢了」。
// 两组都为空时返回 nil：调用方据此不写该分区（分区头仍在，正文为空）。
func ExtractionList(knowledge, opinions []string) []byte {
	var out []byte
	for _, group := range []struct {
		title string
		items []string
	}{
		{ExtractionKnowledgeHeading, knowledge},
		{ExtractionOpinionHeading, opinions},
	} {
		if len(group.items) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, "### "...)
		out = append(out, group.title...)
		out = append(out, '\n', '\n')
		for _, item := range group.items {
			out = append(out, "- "...)
			out = append(out, item...)
			out = append(out, '\n')
		}
	}
	return out
}

// 「提取结果」两组清单的 H3 标题（逐字固定，供渲染与用例共用）。
const (
	ExtractionKnowledgeHeading = "Knowledge"
	ExtractionOpinionHeading   = "Opinion"
)

// ExtractionValidationMark 渲染 Opinion 行末的验证状态标记（契约 §5.1 逐字：“ `[pending]` “）。
//
// # 为什么标记的字面量在 store 而条目文本在 plan
//
// 「行末带一个反引号包裹的状态」是**落盘形态**，与 H3 标题、`- ` 行首同属一套模板，
// 因此字面量收在本包（§16.3 写路径硬约束）；而「这条观点的状态取什么值」是一次
// 判定——真源是本 plan 的新建默认值或盘上的 frontmatter，判定要读库、要产出诊断，
// 只能发生在 plan 侧。两件事分层的判据很实际：换标记写法只改本函数一处，
// 换取值口径只改 plan 一处，两者互不牵连。
//
// 只接受 model.Validation（封闭三值）而不是 string：Note 是本次加工的快照，
// 一个来路不明的字符串一旦落进快照就再也没人能判断它当时是什么意思。
func ExtractionValidationMark(v model.Validation) string {
	return fmt.Sprintf("`[%s]`", v)
}
