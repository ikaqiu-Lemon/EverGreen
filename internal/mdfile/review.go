package mdfile

// 审阅式 Note「整理正文」的**窄读 API**（Schema v2 契约 §4.2 第 4 条 / §5.1 / T-012）：
// 用机器锚点定界，严格读回每个块的 role/heading/body/source_ref/annotation/label 与
// omissions[] 的 source_ref/reason 顺序，并保证 render→parse→render 字节稳定。
//
// # 为什么用锚点定界而不是 SplitBlocks
//
// 一个 NoteBlock 的正文可以含多个 Markdown 块（多段、列表、围栏），SplitBlocks 会把它切成
// 若干块，无法反推「这几块原本属于同一个 NoteBlock」。因此读侧不猜块归属：每个 NoteBlock
// 前面有且只有一个机器锚点，锚点之间（到下一个锚点前）的可见行就是这个块的完整正文。
//
// # fail closed
//
// 未知锚点版本 / 种类、非法或重复元数据、可见正文缺前置锚点、anchor 与可见固定/扩展标签
// 不一致、截断的 blockquote、agent 标记缺失——一律返回 error，绝不静默猜。读侧宁可拒绝一份
// 读不确定的正文，也不产出一个「看起来读回来了但字段是编的」的结构。

import (
	"bytes"
	"fmt"
	"strings"
)

// 审阅块角色（用裸字符串而非 store.NoteBlockRole：mdfile 是 store 的下游包，不反向依赖）。
const (
	ReviewRoleSource = "source"
	ReviewRoleAgent  = "agent"
)

// ReviewBlock 是读回的一个审阅块。Body 是**去锚点、去 blockquote 前缀后的原始正文字节**
// （与 writer 落盘前 bytes.Trim(body,"\n") 的口径一致，故可原样喂回 writer 复现字节）。
type ReviewBlock struct {
	Role       string
	Heading    string
	Body       []byte
	SourceRef  string
	Annotation string
	Label      string
}

// ReviewOmission 是读回的一条遗漏元数据（只来自机器锚点，没有可见「遗漏说明」分区）。
type ReviewOmission struct {
	SourceRef string
	Reason    string
}

// ReviewNote 是「整理正文」读回的结构：块按落盘顺序、omissions 按落盘顺序。
type ReviewNote struct {
	Blocks    []ReviewBlock
	Omissions []ReviewOmission
}

// RenderReviewNote 把审阅块与遗漏元数据渲染成「整理正文」分区正文，是 ParseReviewNote 的
// **精确逆**：ParseReviewNote(RenderReviewNote(x)) 还原 x，且再次 Render 字节稳定。
//
// 落盘形态（每块一个机器锚点 + 可见内容；块 / 遗漏之间恰一个空行分隔）：
//
//	<!-- eg:nr:1 <base64url(source 锚点)> -->
//	### <heading?>
//
//	<source 块正文逐字>
//
//	<!-- eg:nr:1 <base64url(agent 锚点)> -->
//	> **[Agent <显示标签>]** <agent 块正文，续行以 "> " 引用>
//
//	<!-- eg:nr:1 <base64url(omission 元数据)> -->
//
// source 块正文逐字落盘（bytes.Trim 掉首尾换行后一字不改）；agent 块只在行首加引用前缀。
// 每个锚点前若已有内容就插一个空行（统一分隔符），保证 render→parse→render 稳定。
func RenderReviewNote(blocks []ReviewBlock, omissions []ReviewOmission) ([]byte, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("审阅式 Note 至少要有一个块：没有来源内容的 Note 不是整理版文章")
	}
	var out []byte
	sourceCount := 0
	appendAnchor := func(a reviewAnchor) {
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, encodeReviewAnchor(a)...)
		out = append(out, '\n')
	}
	for i, b := range blocks {
		body := bytes.Trim(b.Body, "\n")
		if len(bytes.TrimSpace(body)) == 0 {
			return nil, fmt.Errorf("blocks[%d] 正文为空：空块没有可整理的内容", i)
		}
		// 机器协议完整单行若原样落进正文，读侧会把它误当下一块的边界锚点：拒绝这类保留字
		// 冲突，绝不落盘一份读回来会串块的正文（source 逐字落盘、agent 首行也来自原始正文）。
		if line, bad := firstReservedAnchorLine(body); bad {
			return nil, fmt.Errorf("blocks[%d] 正文含机器锚点保留字（读侧会误判为块边界）：%q", i, line)
		}
		switch b.Role {
		case ReviewRoleSource:
			if b.Annotation != "" || b.Label != "" {
				return nil, fmt.Errorf("blocks[%d] 是 source 块，不得带 annotation/label", i)
			}
			if strings.TrimSpace(b.SourceRef) == "" {
				return nil, fmt.Errorf("blocks[%d] 是 source 块，必须给非空 source_ref（回指原文行段；按 validator 的 TrimSpace 口径，纯空白视为缺失）", i)
			}
			sourceCount++
			appendAnchor(reviewAnchor{Kind: anchorKindSource, Heading: b.Heading, SourceRef: b.SourceRef})
			out = appendReviewHeading(out, b.Heading)
			out = append(out, body...)
			out = append(out, '\n')
		case ReviewRoleAgent:
			if b.SourceRef != "" {
				return nil, fmt.Errorf("blocks[%d] 是 agent 块，不得带 source_ref", i)
			}
			// 与 parser（recoverAgentBlock）复用**同一套** annotation/label 判定，writer 才与
			// parser 严格互逆：ResolveAgentLabel 对内置 key 无条件回固定标签、不看 label，若不先过
			// 这道校验，「内置 key + 非空 label」会被 writer 接受却被 parser 拒（违反 round-trip）。
			if err := validateAgentAnchorAnnotation(b.Annotation, b.Label); err != nil {
				return nil, fmt.Errorf("blocks[%d]：%w", i, err)
			}
			label, err := ResolveAgentLabel(b.Annotation, b.Label)
			if err != nil {
				return nil, fmt.Errorf("blocks[%d]：%w", i, err)
			}
			appendAnchor(reviewAnchor{Kind: anchorKindAgent, Heading: b.Heading, Annotation: b.Annotation, Label: b.Label})
			out = appendReviewHeading(out, b.Heading)
			out = append(out, quoteAgentBodyMarker(body, AgentMarker(displayAgentLabel(label)))...)
		default:
			return nil, fmt.Errorf("blocks[%d] role 越界：%q（恰 %q/%q）", i, b.Role, ReviewRoleSource, ReviewRoleAgent)
		}
	}
	if sourceCount == 0 {
		return nil, fmt.Errorf("审阅式 Note 必须至少有一个 source 块：通篇 Agent 推导是分析笔记，不是原文的整理版")
	}
	for i, o := range omissions {
		if strings.TrimSpace(o.SourceRef) == "" {
			return nil, fmt.Errorf("omissions[%d] 必须给非空 source_ref（回指被删的原文行段；按 validator 的 TrimSpace 口径，纯空白视为缺失）", i)
		}
		if strings.TrimSpace(o.Reason) == "" {
			return nil, fmt.Errorf("omissions[%d] 必须给非空 reason（说明为何删；按 validator 的 TrimSpace 口径，纯空白视为缺失）", i)
		}
		appendAnchor(reviewAnchor{Kind: anchorKindOmission, SourceRef: o.SourceRef, Reason: o.Reason})
	}
	return out, nil
}

// firstReservedAnchorLine 报告 body 里是否有任何一行是机器锚点保留字（据以拒绝保留字冲突）。
func firstReservedAnchorLine(body []byte) ([]byte, bool) {
	for _, line := range bytes.Split(body, []byte("\n")) {
		if isReviewAnchorLine(line) {
			return line, true
		}
	}
	return nil, false
}

// appendReviewHeading 在有 heading 时追加「### <heading>」+ 一个空行（H3：不与分区骨架 H2 冲突）。
func appendReviewHeading(out []byte, heading string) []byte {
	if heading == "" {
		return out
	}
	out = append(out, "### "...)
	out = append(out, heading...)
	out = append(out, '\n', '\n')
	return out
}

// quoteAgentBodyMarker 把 agent 正文渲染成 blockquote：首行带 marker，续行带 "> "。
// 只有**真正的空行**（长度为 0）渲染成 ">"；仅含空格 / Tab 的行是有内容的真实行，
// 必须走 "> " + 原行逐字保留——否则 TrimSpace 判空会把它塌成 ">"，round-trip 丢字节。
func quoteAgentBodyMarker(body []byte, marker string) []byte {
	lines := bytes.Split(body, []byte("\n"))
	out := make([]byte, 0, len(body)+len(marker)+2*len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, marker...)
			out = append(out, line...)
			out = append(out, '\n')
			continue
		}
		if len(line) == 0 {
			out = append(out, ">\n"...)
			continue
		}
		out = append(out, AgentQuotePrefix...)
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

// ParseReviewNote 解析「整理正文」分区正文。见文件头 fail-closed 契约。
//
// 解析后 blocks 与 omissions 分开归集，writer 固定把 omissions 落在全部块之后；因此一旦
// 见过 omission，其后再出现 source/agent 块即畸形（parse→render 会把块重排到 omission 之前，
// 破坏字节稳定），当场拒绝。多个 omissions 保持其原始先后顺序。
func ParseReviewNote(body []byte) (ReviewNote, error) {
	lines := bytes.Split(body, []byte("\n"))
	var rn ReviewNote
	sourceCount := 0
	sawOmission := false
	idx := 0
	for idx < len(lines) {
		if isBlankReviewLine(lines[idx]) {
			idx++
			continue
		}
		if !isReviewAnchorLine(lines[idx]) {
			return ReviewNote{}, fmt.Errorf("整理正文出现无前置机器锚点的可见内容：%q", lines[idx])
		}
		a, err := decodeReviewAnchor(lines[idx])
		if err != nil {
			return ReviewNote{}, err
		}
		idx++
		start := idx
		for idx < len(lines) && !isReviewAnchorLine(lines[idx]) {
			idx++
		}
		content := lines[start:idx]
		switch a.Kind {
		case anchorKindOmission:
			om, err := recoverOmission(a, content)
			if err != nil {
				return ReviewNote{}, err
			}
			rn.Omissions = append(rn.Omissions, om)
			sawOmission = true
		case anchorKindSource:
			if sawOmission {
				return ReviewNote{}, fmt.Errorf("omission 之后不得再出现 source 块：omissions 固定落在全部块之后，块重排会破坏顺序保真")
			}
			blk, err := recoverSourceBlock(a, content)
			if err != nil {
				return ReviewNote{}, err
			}
			rn.Blocks = append(rn.Blocks, blk)
			sourceCount++
		case anchorKindAgent:
			if sawOmission {
				return ReviewNote{}, fmt.Errorf("omission 之后不得再出现 agent 块：omissions 固定落在全部块之后，块重排会破坏顺序保真")
			}
			blk, err := recoverAgentBlock(a, content)
			if err != nil {
				return ReviewNote{}, err
			}
			rn.Blocks = append(rn.Blocks, blk)
		}
	}
	if sourceCount == 0 {
		return ReviewNote{}, fmt.Errorf("整理正文必须至少有一个 source 块：空正文 / 只有 omission / 通篇 Agent 推导都不是原文的整理版")
	}
	return rn, nil
}

// recoverSourceBlock 从 source 锚点 + 可见内容读回一个来源块。
// 字段互斥与必填（fail closed，不重复 plan 的 Lx-Ly 覆盖 / 顺序业务校验）：
// source 块只用 source_ref，不得带 annotation/label/reason，且 source_ref 必须非空。
func recoverSourceBlock(a reviewAnchor, content [][]byte) (ReviewBlock, error) {
	if a.Annotation != "" || a.Label != "" {
		return ReviewBlock{}, fmt.Errorf("source 锚点携带了 annotation/label（source 块只用 source_ref）")
	}
	if a.Reason != "" {
		return ReviewBlock{}, fmt.Errorf("source 锚点携带了 reason（reason 只属于 omission）")
	}
	if strings.TrimSpace(a.SourceRef) == "" {
		return ReviewBlock{}, fmt.Errorf("source 锚点缺 source_ref（来源块必须回指原文行段；按 TrimSpace 口径，纯空白视为缺失）")
	}
	bodyLines, err := stripHeading(a.Heading, content)
	if err != nil {
		return ReviewBlock{}, err
	}
	if len(bodyLines) == 0 {
		return ReviewBlock{}, fmt.Errorf("source 块可见正文为空")
	}
	return ReviewBlock{
		Role:      ReviewRoleSource,
		Heading:   a.Heading,
		Body:      joinLines(bodyLines),
		SourceRef: a.SourceRef,
	}, nil
}

// recoverAgentBlock 从 agent 锚点 + 可见 blockquote 读回一个批注块，并核对可见标签一致。
// 字段互斥与必填：agent 块只用 annotation/label，不得带 source_ref/reason；annotation 必须非空、
// 合法（内置七类不得被 label 覆盖，扩展 key 合法且 label 非空）。
func recoverAgentBlock(a reviewAnchor, content [][]byte) (ReviewBlock, error) {
	if a.SourceRef != "" {
		return ReviewBlock{}, fmt.Errorf("agent 锚点携带了 source_ref（agent 块只用 annotation）")
	}
	if a.Reason != "" {
		return ReviewBlock{}, fmt.Errorf("agent 锚点携带了 reason（reason 只属于 omission）")
	}
	if err := validateAgentAnchorAnnotation(a.Annotation, a.Label); err != nil {
		return ReviewBlock{}, err
	}
	want, err := ResolveAgentLabel(a.Annotation, a.Label)
	if err != nil {
		return ReviewBlock{}, err
	}
	bodyLines, err := stripHeading(a.Heading, content)
	if err != nil {
		return ReviewBlock{}, err
	}
	if len(bodyLines) == 0 {
		return ReviewBlock{}, fmt.Errorf("agent 块可见正文为空")
	}
	// 用锚点得出的 want 生成**完整** marker，要求可见首行精确以它开头，再从精确长度后取正文。
	// 不用「首次 close」猜标签：合法自定义 label 可能含 "]** " 子串，首次 close 会把标签截短，
	// 从而误判「可见标签与锚点不一致」。可见 label 经 displayAgentLabel 单行转义（与 writer 同一
	// 函数），故用 displayAgentLabel(want) 构造 marker 核对，核对通过后 Label 仍回读锚点里的原值。
	marker := AgentMarker(displayAgentLabel(want))
	first := string(bodyLines[0])
	if !strings.HasPrefix(first, marker) {
		return ReviewBlock{}, fmt.Errorf("agent 块首行未以锚点解析出的标记 %q 开头（缺失 / 标签不一致 / 被截断）：%q", marker, first)
	}
	out := [][]byte{[]byte(first[len(marker):])}
	for _, line := range bodyLines[1:] {
		s := string(line)
		switch {
		case s == ">":
			out = append(out, []byte(""))
		case len(s) >= len(AgentQuotePrefix) && s[:len(AgentQuotePrefix)] == AgentQuotePrefix:
			out = append(out, []byte(s[len(AgentQuotePrefix):]))
		default:
			return ReviewBlock{}, fmt.Errorf("agent blockquote 续行缺引用前缀（疑似截断）：%q", line)
		}
	}
	return ReviewBlock{
		Role:       ReviewRoleAgent,
		Heading:    a.Heading,
		Body:       joinLines(out),
		Annotation: a.Annotation,
		Label:      a.Label,
	}, nil
}

// validateAgentAnchorAnnotation 校验 agent 锚点的 annotation/label 组合（§4.2.2，与 plan 侧同规则）：
// annotation 必须非空；内置七类 key 不得携 **TrimSpace 后非空** 的 label（含义固定，且与 plan 的
// 放行口径一致——纯空白 label 是 plan 放行并落盘的合法值，读侧必须原样接纳、不得静默拒回）；
// 扩展 key 必须合法且 label（trim 后）非空。
func validateAgentAnchorAnnotation(annotation, label string) error {
	if annotation == "" {
		return fmt.Errorf("agent 锚点缺 annotation（每条批注都要声明教学意图）")
	}
	if _, builtin := BuiltinAnnotationLabel(annotation); builtin {
		if strings.TrimSpace(label) != "" {
			return fmt.Errorf("内置批注 %q 含义固定，不得携 trim 后非空的 label：%q", annotation, label)
		}
		return nil
	}
	if !ValidExtensionAnnotationKey(annotation) {
		return fmt.Errorf("非法扩展批注 key %q（须匹配 ^[a-z][a-z0-9_-]{0,31}$）", annotation)
	}
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("扩展批注 %q 缺 trim 后非空的 label", annotation)
	}
	return nil
}

// recoverOmission 从 omission 锚点 + 可见内容读回一条遗漏元数据。
// omission 只用 source_ref/reason（二者必填非空），不得带 heading/annotation/label；
// 且其后不得有可见正文（遗漏只是机器元数据，没有可见分区）。
func recoverOmission(a reviewAnchor, content [][]byte) (ReviewOmission, error) {
	if a.Heading != "" || a.Annotation != "" || a.Label != "" {
		return ReviewOmission{}, fmt.Errorf("omission 锚点携带了非法字段（只允许 source_ref/reason）")
	}
	if strings.TrimSpace(a.SourceRef) == "" {
		return ReviewOmission{}, fmt.Errorf("omission 锚点缺 source_ref（须回指被删的原文行段；按 TrimSpace 口径，纯空白视为缺失）")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return ReviewOmission{}, fmt.Errorf("omission 锚点缺 reason（须说明为何删；按 TrimSpace 口径，纯空白视为缺失）")
	}
	if err := requireBlankContent(content); err != nil {
		return ReviewOmission{}, err
	}
	return ReviewOmission{SourceRef: a.SourceRef, Reason: a.Reason}, nil
}

// stripHeading 去掉可见内容尾部的分隔空行；若锚点声明了 heading，再校验并剥离
// 「### <heading>」+ 其后的一个空行。返回块正文行（不含 heading、不含尾部分隔空行）。
func stripHeading(heading string, content [][]byte) ([][]byte, error) {
	trimmed := stripTrailingSeparator(content)
	if heading == "" {
		return trimmed, nil
	}
	wantHead := "### " + heading
	if len(trimmed) == 0 || string(trimmed[0]) != wantHead {
		return nil, fmt.Errorf("锚点声明了 heading %q，可见内容首行却不是 %q", heading, wantHead)
	}
	if len(trimmed) < 2 || !isBlankReviewLine(trimmed[1]) {
		return nil, fmt.Errorf("heading %q 之后缺一个空行分隔", heading)
	}
	return trimmed[2:], nil
}

// requireBlankContent 要求可见内容全为空行（omission 锚点后不得出现可见正文）。
func requireBlankContent(content [][]byte) error {
	for _, c := range content {
		if !isBlankReviewLine(c) {
			return fmt.Errorf("omission 机器元数据之后不得有可见正文：%q", c)
		}
	}
	return nil
}

// stripTrailingSeparator 只剥掉 writer 自己产生的**恰一个**终止 / 分隔空行——它必是**真正的
// 空行**（长度为 0：块正文的收尾 '\n' 或块间分隔 '\n' 落在这里）。
//
// 关键：不能用 TrimSpace 连续裁剪。source/agent 正文里仅含空格 / Tab 的行是有内容的真实行，
// 若按「空白即空」连裁，会把这类尾部行一并吞掉，破坏 render→parse→render 字节稳定。
func stripTrailingSeparator(lines [][]byte) [][]byte {
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		return lines[:n-1]
	}
	return lines
}

// joinLines 用 \n 连接行（复现 writer 的正文字节）。
func joinLines(lines [][]byte) []byte {
	return bytes.Join(lines, []byte("\n"))
}

// isBlankReviewLine 判定审阅正文里的空行（只认真正的空 / 纯空白行；blockquote 的 ">" 不算空）。
func isBlankReviewLine(line []byte) bool {
	return len(bytes.TrimSpace(line)) == 0
}
