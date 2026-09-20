package mdfile

// coverage.go —— 「提取结果」覆盖矩阵的**确定性渲染 + 窄读 API**（Schema v2 契约 §4.2.3 / §5.1 · T12-4）。
//
// # 为什么是「可见表 + 每行一条机器锚点」而不是只留可见表
//
// 覆盖矩阵既要给人读（模块 / 来源范围 / 语义模块 / 处置一目了然），又要能被机器**逐字**读回。
// 可见单元格为了不破表必须对 `|` / CR / LF 做单行转义，转义之后就不再是原始字节——若只留可见表，
// summary / reason 里的换行与管道符再也无法无损还原。因此每条覆盖项额外落一条**独立版本化**的
// 单行机器锚点 `eg:nc:1`（载荷 base64url(JSON)，与 T12-3 的 `eg:nr:1` 分属两套协议、互不复用）：
// 锚点是**真源**，parser 只信锚点；可见表则由 parser 用锚点**重渲染后逐字节比对**——任何对表头 /
// 单元格 / 顺序 / 尾随内容的篡改都会让「重渲染 == 原文」这条等式不成立，当场 fail closed。
//
// # 单一归属
//
// 协议常量、编码、解码、可见渲染、校验都只在本文件定义一次；store 的 writer 与 plan 的接线冒烟
// 都调用这里，不各抄一份字面量。重复键检测复用本包 review_anchor.go 的 rejectDuplicateJSONKeys。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// 覆盖矩阵机器锚点的协议常量。family 用于**识别**一条注释是否属于本协议（据此对未知版本
// fail closed），version 固定为 1；open 是 v1 的精确前缀，CoverageAnchorClose 是注释结束符。
const (
	coverageAnchorFamily  = "eg:nc:"
	coverageAnchorVersion = "1"
	coverageAnchorTag     = coverageAnchorFamily + coverageAnchorVersion // eg:nc:1
	coverageAnchorOpen    = "<!-- " + coverageAnchorTag + " "
)

// CoverageAnchorClose 是覆盖矩阵机器锚点的注释结束符（供切块 / 定位复用同一份字面量）。
const CoverageAnchorClose = " -->"

// 覆盖矩阵可见形态的固定字面量（标题 / 表头 / 分隔行；渲染与交叉核对共用同一份）。
const (
	coverageHeading   = "### 覆盖矩阵"
	coverageHeaderRow = "| 模块 | 来源范围 | 语义模块 | 处置 |"
	coverageSepRow    = "| --- | --- | --- | --- |"
)

// disposition 的封闭三值（契约 §4.2.3）。导出以便 plan 侧语义校验复用同一份字面量，
// 不在两包各写一遍 "outputs"/"note_only"/"missing"（封闭枚举的单一归属）。
const (
	CoverageDispOutputs  = "outputs"
	CoverageDispNoteOnly = "note_only"
	CoverageDispMissing  = "missing"

	coverageDispOutputs  = CoverageDispOutputs
	coverageDispNoteOnly = CoverageDispNoteOnly
	coverageDispMissing  = CoverageDispMissing
)

// 处置单元格的可见前缀（note_only / missing 的人读渲染）。
const (
	coverageNoteOnlyPrefix = "Note-only："
	coverageMissingPrefix  = "缺漏："
)

// ReviewCoverage 是覆盖矩阵读回 / 渲染的一条覆盖项（字段名逐字对齐契约 §4.2.3）。
type ReviewCoverage struct {
	Module      string
	SourceRefs  []string
	Summary     string
	Disposition string
	Outputs     []string
	Reason      string
}

// coverageAnchor 是机器锚点的结构化载荷。字段声明序即 JSON 编码序（Go struct 编码确定性 +
// omitempty），保证同一结构体每次 Marshal 出同一份字节 → render 稳定。json tag 用短名压缩注释长度。
// outputs 只在 disposition=outputs 时非空，reason 只在 note_only / missing 时非空，二者互斥出现，
// 故都用 omitempty：缺省字段不占字节，解码时不出现的字段自然为零值。
type coverageAnchor struct {
	Module      string   `json:"m"`
	SourceRefs  []string `json:"s"`
	Summary     string   `json:"g"`
	Disposition string   `json:"d"`
	Outputs     []string `json:"o,omitempty"`
	Reason      string   `json:"r,omitempty"`
}

// cellEscaper 把自由文本单元格（summary / reason）压成**单行且不破表**的可见字节：
// 反斜杠先转义（避免与后续转义序列混淆），管道符转义（否则截断单元格），CR / LF 转义
// （否则数据行被拆成多行、破坏「一行一条」）。单次遍历、不回扫输出，故不会二次转义。
// 可见转义只需**确定性**：读回的真源是机器锚点（逐字节原始值），可见表只参与重渲染比对。
var cellEscaper = strings.NewReplacer("\\", "\\\\", "|", "\\|", "\r", "\\r", "\n", "\\n")

// validateReviewCoverage 是渲染 / 解码共用的**形态闸门**（fail closed）：
// module trim 后非空、source_refs 非空且每项 trim 后非空、summary trim 后非空、
// disposition 封闭三值，且三种处置的 outputs / reason 形态互斥满足（outputs 处置的每个 output
// 也需 trim 后非空）。渲染前与解码后都过同一道闸，保证「能渲染的必能读回、能读回的必能重渲染」。
// 注：本闸只做**单条**形态校验；module 在整份矩阵内的原始字节唯一性由 RenderCoverageMatrix 兜。
func validateReviewCoverage(c ReviewCoverage) error {
	if strings.TrimSpace(c.Module) == "" {
		return fmt.Errorf("覆盖项 module 为空")
	}
	if len(c.SourceRefs) == 0 {
		return fmt.Errorf("覆盖项 %q 的 source_refs 为空", c.Module)
	}
	for i, ref := range c.SourceRefs {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("覆盖项 %q 的 source_refs[%d] 为空", c.Module, i)
		}
	}
	if strings.TrimSpace(c.Summary) == "" {
		return fmt.Errorf("覆盖项 %q 的 summary 为空", c.Module)
	}
	switch c.Disposition {
	case coverageDispOutputs:
		if len(c.Outputs) == 0 {
			return fmt.Errorf("覆盖项 %q 处置为 outputs 却无 outputs", c.Module)
		}
		for i, out := range c.Outputs {
			if strings.TrimSpace(out) == "" {
				return fmt.Errorf("覆盖项 %q 的 outputs[%d] 为空", c.Module, i)
			}
		}
		if strings.TrimSpace(c.Reason) != "" {
			return fmt.Errorf("覆盖项 %q 处置为 outputs 不得带 reason", c.Module)
		}
	case coverageDispNoteOnly, coverageDispMissing:
		if len(c.Outputs) != 0 {
			return fmt.Errorf("覆盖项 %q 处置为 %s 不得带 outputs", c.Module, c.Disposition)
		}
		if strings.TrimSpace(c.Reason) == "" {
			return fmt.Errorf("覆盖项 %q 处置为 %s 必须给非空 reason", c.Module, c.Disposition)
		}
	default:
		return fmt.Errorf("覆盖项 %q 的 disposition 越界：%q（封闭三值 %s/%s/%s）",
			c.Module, c.Disposition, coverageDispOutputs, coverageDispNoteOnly, coverageDispMissing)
	}
	return nil
}

// longestBacktickRun 返回 s 中最长连续反引号串的长度（用于给 code span 选一条足够长的围栏）。
func longestBacktickRun(s string) int {
	longest, cur := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			cur++
			if cur > longest {
				longest = cur
			}
		} else {
			cur = 0
		}
	}
	return longest
}

// codeSpan 把一段文本（module / source_ref / output ID）渲染成**不破表、代码跨度自洽**的行内
// code span，是 module 与 backtickJoin 共用的**唯一**安全渲染器：
//   - 先用 cellEscaper 消除会截断单元格 / 拆行的字节（管道→\|、CR/LF→\r/\n；反斜杠先转义，
//     否则 \| 里的反斜杠会被当作转义源、放过后一个裸管道）；
//   - 再取一条比内部最长反引号串更长的反引号围栏，使内部反引号无法提前闭合 code span
//     （code span 内反斜杠是字面量，无法用 \` 逃逸掉一个反引号，只能靠变长围栏兜住）；
//   - 内容以反引号 / 空格开头或结尾（含整体皆空白）时，按 CommonMark 规则两侧各垫一个空格，
//     避免围栏粘连或首尾空格被吞。
//
// 普通内容（无特殊字节）退化为单反引号包裹，与既有 golden 逐字一致。可见表只需**确定性 + 不破表**：
// 读回的真源是机器锚点（逐字节原始值），可见片段仅参与重渲染比对。
func codeSpan(s string) string {
	esc := cellEscaper.Replace(s)
	fence := strings.Repeat("`", longestBacktickRun(esc)+1)
	pad := ""
	if esc != "" {
		if b := esc[0]; b == '`' || b == ' ' {
			pad = " "
		} else if e := esc[len(esc)-1]; e == '`' || e == ' ' {
			pad = " "
		}
	}
	return fence + pad + esc + pad + fence
}

// backtickJoin 把 ID / 行段清单渲染成安全 code span、空格分隔的可见片段（`a` `b`）。
// 每一项都过 codeSpan，避免公共渲染器在 ref / output 含 `|` / CR/LF / 反引号时吐出破表字节。
func backtickJoin(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = codeSpan(s)
	}
	return strings.Join(parts, " ")
}

// coverageDispositionCell 渲染「处置」单元格：outputs 列反引号 ID，note_only / missing 走人读前缀。
func coverageDispositionCell(c ReviewCoverage) string {
	switch c.Disposition {
	case coverageDispOutputs:
		return backtickJoin(c.Outputs)
	case coverageDispNoteOnly:
		return coverageNoteOnlyPrefix + cellEscaper.Replace(c.Reason)
	default: // missing（已由 validate 收窄到封闭三值）
		return coverageMissingPrefix + cellEscaper.Replace(c.Reason)
	}
}

// coverageVisibleRow 渲染一条可见数据行（单行、转义、不破表）。module / source_refs / outputs
// 都过统一的安全 code-span 渲染器（codeSpan / backtickJoin），summary / reason 走自由文本转义。
func coverageVisibleRow(c ReviewCoverage) string {
	return "| " + codeSpan(c.Module) + " | " + backtickJoin(c.SourceRefs) + " | " +
		cellEscaper.Replace(c.Summary) + " | " + coverageDispositionCell(c) + " |"
}

// encodeCoverageAnchor 把一条覆盖项编码成单行机器锚点。struct 编码不会失败，故不返回 error。
func encodeCoverageAnchor(c ReviewCoverage) string {
	a := coverageAnchor{
		Module:      c.Module,
		SourceRefs:  c.SourceRefs,
		Summary:     c.Summary,
		Disposition: c.Disposition,
		Outputs:     c.Outputs,
		Reason:      c.Reason,
	}
	raw, err := json.Marshal(a)
	if err != nil { // 理论不可达：字段全为 string / []string。
		panic(fmt.Sprintf("覆盖矩阵锚点编码失败（不可达）：%v", err))
	}
	return coverageAnchorOpen + base64.RawURLEncoding.EncodeToString(raw) + CoverageAnchorClose
}

// RenderCoverageMatrix 把覆盖项渲染成「提取结果」里的覆盖矩阵字节（可见表 + 机器锚点）。
//
// 形态（golden 逐字）：H3 标题 + 空行 + 表头 + 分隔行 + N 条数据行 + 空行 + N 条机器锚点，
// 以 \n 结束。行顺序 == 输入序（不排序 / 不去重 / 不重排）；空输入、任一项形态违规、
// 或 module 原始字节在矩阵内重复即 fail closed（module 唯一性是 store 直调时的最后一道闸，
// 契约 §4.2.3）。
func RenderCoverageMatrix(cov []ReviewCoverage) ([]byte, error) {
	if len(cov) == 0 {
		return nil, fmt.Errorf("覆盖矩阵为空：没有可渲染的覆盖项")
	}
	seenModule := make(map[string]bool, len(cov))
	for i := range cov {
		if err := validateReviewCoverage(cov[i]); err != nil {
			return nil, fmt.Errorf("覆盖矩阵第 %d 项不成立：%w", i, err)
		}
		if seenModule[cov[i].Module] {
			return nil, fmt.Errorf("覆盖矩阵第 %d 项 module=%q 原始字节重复：模块名在矩阵内必须唯一"+
				"（契约 §4.2.3）", i, cov[i].Module)
		}
		seenModule[cov[i].Module] = true
	}
	var b strings.Builder
	b.WriteString(coverageHeading)
	b.WriteString("\n\n")
	b.WriteString(coverageHeaderRow)
	b.WriteByte('\n')
	b.WriteString(coverageSepRow)
	b.WriteByte('\n')
	for i := range cov {
		b.WriteString(coverageVisibleRow(cov[i]))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	for i := range cov {
		b.WriteString(encodeCoverageAnchor(cov[i]))
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// isCoverageAnchorLine 报告一行（trim 后）是否**声称**是本协议的锚点（任意版本）：
// 只看 family 前缀与注释结束符，据此把「疑似本协议但版本不对」的行也识别出来交给解码 fail closed。
func isCoverageAnchorLine(line []byte) bool {
	t := bytes.TrimSpace(line)
	return bytes.HasPrefix(t, []byte("<!-- "+coverageAnchorFamily)) &&
		bytes.HasSuffix(t, []byte(CoverageAnchorClose))
}

// decodeCoverageAnchor 解码一行机器锚点（fail closed，判据与 review_anchor.decodeReviewAnchor 对称）：
// 未知版本、base64 非法、JSON 非法 / 含未知字段 / 含重复键 / 多余数据、以及形态违规一律拒绝。
func decodeCoverageAnchor(line []byte) (ReviewCoverage, error) {
	t := bytes.TrimSpace(line)
	if !bytes.HasPrefix(t, []byte(coverageAnchorOpen)) {
		return ReviewCoverage{}, fmt.Errorf("未知的覆盖矩阵锚点版本（仅支持 %s）：%q", coverageAnchorTag, t)
	}
	if !bytes.HasSuffix(t, []byte(CoverageAnchorClose)) {
		return ReviewCoverage{}, fmt.Errorf("覆盖矩阵锚点缺注释结束符：%q", t)
	}
	enc := t[len(coverageAnchorOpen) : len(t)-len(CoverageAnchorClose)]
	raw, err := base64.RawURLEncoding.DecodeString(string(enc))
	if err != nil {
		return ReviewCoverage{}, fmt.Errorf("覆盖矩阵锚点载荷 base64url 非法：%v", err)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return ReviewCoverage{}, fmt.Errorf("覆盖矩阵锚点载荷含重复键：%v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a coverageAnchor
	if err := dec.Decode(&a); err != nil {
		return ReviewCoverage{}, fmt.Errorf("覆盖矩阵锚点载荷 JSON 非法：%v", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return ReviewCoverage{}, fmt.Errorf("覆盖矩阵锚点载荷含多余数据（应恰一个 JSON 对象）")
	}
	c := ReviewCoverage{
		Module:      a.Module,
		SourceRefs:  a.SourceRefs,
		Summary:     a.Summary,
		Disposition: a.Disposition,
		Outputs:     a.Outputs,
		Reason:      a.Reason,
	}
	if err := validateReviewCoverage(c); err != nil {
		return ReviewCoverage{}, fmt.Errorf("覆盖矩阵锚点形态违规：%w", err)
	}
	return c, nil
}

// ParseCoverageMatrix 从覆盖矩阵字节读回全部覆盖项，并交叉核对可见矩阵与机器锚点。
//
// 真源是机器锚点：先按出现序解码每条锚点（任一解码失败即 fail closed），至少一条；再用解出的
// 覆盖项**重渲染**一份矩阵，与传入字节**逐字节比对**——相等才返回。任何对可见表头 / 单元格 /
// 行顺序 / 锚点数量 / 尾随内容的篡改都会让「重渲染 == 原文」不成立，当场 error（绝不猜）。
func ParseCoverageMatrix(body []byte) ([]ReviewCoverage, error) {
	var covs []ReviewCoverage
	for _, ln := range bytes.Split(body, []byte("\n")) {
		if !isCoverageAnchorLine(ln) {
			continue
		}
		c, err := decodeCoverageAnchor(ln)
		if err != nil {
			return nil, err
		}
		covs = append(covs, c)
	}
	if len(covs) == 0 {
		return nil, fmt.Errorf("覆盖矩阵缺机器锚点（%s）：无法确定真源", coverageAnchorTag)
	}
	want, err := RenderCoverageMatrix(covs)
	if err != nil {
		return nil, fmt.Errorf("覆盖矩阵锚点重渲染失败：%w", err)
	}
	if !bytes.Equal(want, body) {
		return nil, fmt.Errorf("覆盖矩阵可见内容与机器锚点不一致（fail closed）")
	}
	return covs, nil
}
