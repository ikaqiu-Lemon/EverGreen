package mdfile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	candidateCoverageAnchorFamily  = "eg:cc:"
	candidateCoverageAnchorVersion = "1"
	candidateCoverageAnchorTag     = candidateCoverageAnchorFamily + candidateCoverageAnchorVersion
	candidateCoverageAnchorOpen    = "<!-- " + candidateCoverageAnchorTag + " "

	candidateCoverageHeading   = "### 候选覆盖"
	candidateCoverageHeaderRow = "| 模块 | 来源范围 | 语义模块 | 草稿处置 |"
	candidateCoverageSepRow    = "| --- | --- | --- | --- |"
)

const (
	CandidateCoverageCandidate  = "candidate"
	CandidateCoverageNoteOnly   = "note_only"
	CandidateCoverageUnresolved = "unresolved"
)

// CandidateCoverage is a draft-time source coverage fact. Candidate keys are
// intentionally separate from final k-*/o-* outputs.
type CandidateCoverage struct {
	Module      string
	SourceRefs  []string
	Summary     string
	Disposition string
	Candidates  []string
	Reason      string
}

// CandidateCoverageState is the machine-managed tail following the last
// candidate. Exactly one of Draft or Final is populated.
type CandidateCoverageState struct {
	Draft     []CandidateCoverage
	Final     []ReviewCoverage
	Finalized bool

	start int
	end   int
}

type candidateCoverageWire struct {
	Module      string   `json:"module"`
	SourceRefs  []string `json:"source_refs"`
	Summary     string   `json:"summary"`
	Disposition string   `json:"disposition"`
	Candidates  []string `json:"candidates"`
	Reason      string   `json:"reason"`
}

func validateCandidateCoverage(c CandidateCoverage) error {
	if strings.TrimSpace(c.Module) == "" {
		return fmt.Errorf("候选覆盖项 module 为空")
	}
	if len(c.SourceRefs) == 0 {
		return fmt.Errorf("候选覆盖项 %q 的 source_refs 为空", c.Module)
	}
	for i, ref := range c.SourceRefs {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("候选覆盖项 %q 的 source_refs[%d] 为空", c.Module, i)
		}
	}
	if strings.TrimSpace(c.Summary) == "" {
		return fmt.Errorf("候选覆盖项 %q 的 summary 为空", c.Module)
	}
	switch c.Disposition {
	case CandidateCoverageCandidate:
		if len(c.Candidates) == 0 {
			return fmt.Errorf("候选覆盖项 %q 处置为 candidate 却无 candidates", c.Module)
		}
		for i, key := range c.Candidates {
			if !candidateKeyRE.MatchString(key) {
				return fmt.Errorf("候选覆盖项 %q 的 candidates[%d] 非法：%q", c.Module, i, key)
			}
		}
		if strings.TrimSpace(c.Reason) != "" {
			return fmt.Errorf("候选覆盖项 %q 处置为 candidate 不得带 reason", c.Module)
		}
	case CandidateCoverageNoteOnly, CandidateCoverageUnresolved:
		if len(c.Candidates) != 0 {
			return fmt.Errorf("候选覆盖项 %q 处置为 %s 不得带 candidates",
				c.Module, c.Disposition)
		}
		if strings.TrimSpace(c.Reason) == "" {
			return fmt.Errorf("候选覆盖项 %q 处置为 %s 必须给非空 reason",
				c.Module, c.Disposition)
		}
	default:
		return fmt.Errorf("候选覆盖项 %q 的 disposition 越界：%q", c.Module, c.Disposition)
	}
	return nil
}

func candidateCoverageDispositionCell(c CandidateCoverage) string {
	switch c.Disposition {
	case CandidateCoverageCandidate:
		return backtickJoin(c.Candidates)
	case CandidateCoverageNoteOnly:
		return coverageNoteOnlyPrefix + cellEscaper.Replace(c.Reason)
	default:
		return "未解决：" + cellEscaper.Replace(c.Reason)
	}
}

func candidateCoverageVisibleRow(c CandidateCoverage) string {
	return "| " + codeSpan(c.Module) + " | " + backtickJoin(c.SourceRefs) + " | " +
		cellEscaper.Replace(c.Summary) + " | " + candidateCoverageDispositionCell(c) + " |"
}

func encodeCandidateCoverageAnchor(c CandidateCoverage) string {
	candidates := c.Candidates
	if candidates == nil {
		candidates = []string{}
	}
	raw, err := json.Marshal(candidateCoverageWire{
		Module:      c.Module,
		SourceRefs:  c.SourceRefs,
		Summary:     c.Summary,
		Disposition: c.Disposition,
		Candidates:  candidates,
		Reason:      c.Reason,
	})
	if err != nil {
		panic(fmt.Sprintf("candidate coverage anchor encoding failed: %v", err))
	}
	return candidateCoverageAnchorOpen +
		base64.RawURLEncoding.EncodeToString(raw) + candidateAnchorClose
}

// RenderCandidateCoverageMatrix renders the visible draft matrix followed by
// one eg:cc:1 source-of-truth anchor per row.
func RenderCandidateCoverageMatrix(cov []CandidateCoverage) ([]byte, error) {
	if len(cov) == 0 {
		return nil, fmt.Errorf("候选覆盖矩阵为空")
	}
	seen := map[string]bool{}
	for i, c := range cov {
		if err := validateCandidateCoverage(c); err != nil {
			return nil, fmt.Errorf("候选覆盖矩阵第 %d 项不成立：%w", i, err)
		}
		if seen[c.Module] {
			return nil, fmt.Errorf("候选覆盖矩阵 module 原始字节重复：%q", c.Module)
		}
		seen[c.Module] = true
	}
	var b strings.Builder
	b.WriteString(candidateCoverageHeading)
	b.WriteString("\n\n")
	b.WriteString(candidateCoverageHeaderRow)
	b.WriteByte('\n')
	b.WriteString(candidateCoverageSepRow)
	b.WriteByte('\n')
	for _, c := range cov {
		b.WriteString(candidateCoverageVisibleRow(c))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	for _, c := range cov {
		b.WriteString(encodeCandidateCoverageAnchor(c))
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func isCandidateCoverageAnchorLine(line []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(line), []byte("<!-- "+candidateCoverageAnchorFamily))
}

func decodeCandidateCoverageAnchor(line []byte) (CandidateCoverage, error) {
	t := bytes.TrimSpace(line)
	if !bytes.HasPrefix(t, []byte(candidateCoverageAnchorOpen)) {
		return CandidateCoverage{}, fmt.Errorf(
			"未知的候选覆盖锚点版本（仅支持 %s）：%q", candidateCoverageAnchorTag, t)
	}
	if !bytes.HasSuffix(t, []byte(candidateAnchorClose)) {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点缺注释结束符：%q", t)
	}
	if len(t) < len(candidateCoverageAnchorOpen)+len(candidateAnchorClose) {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点载荷缺失：%q", t)
	}
	enc := t[len(candidateCoverageAnchorOpen) : len(t)-len(candidateAnchorClose)]
	raw, err := base64.RawURLEncoding.DecodeString(string(enc))
	if err != nil {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点载荷 base64url 非法：%v", err)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点载荷含重复键：%v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点载荷 JSON 非法：%v", err)
	}
	want := map[string]bool{
		"module": true, "source_refs": true, "summary": true,
		"disposition": true, "candidates": true, "reason": true,
	}
	if len(fields) != len(want) {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点字段集不完整")
	}
	for key := range fields {
		if !want[key] {
			return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点含未知字段 %q", key)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var wire candidateCoverageWire
	if err := dec.Decode(&wire); err != nil {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点载荷 JSON 非法：%v", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点载荷含多余数据")
	}
	if wire.SourceRefs == nil || wire.Candidates == nil {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点 source_refs/candidates 必须是数组")
	}
	c := CandidateCoverage{
		Module: wire.Module, SourceRefs: wire.SourceRefs, Summary: wire.Summary,
		Disposition: wire.Disposition, Candidates: wire.Candidates, Reason: wire.Reason,
	}
	if err := validateCandidateCoverage(c); err != nil {
		return CandidateCoverage{}, fmt.Errorf("候选覆盖锚点形态违规：%w", err)
	}
	return c, nil
}

// ParseCandidateCoverageMatrix verifies the visible table against its anchors.
func ParseCandidateCoverageMatrix(body []byte) ([]CandidateCoverage, error) {
	var cov []CandidateCoverage
	for _, line := range bytes.Split(body, []byte("\n")) {
		if !isCandidateCoverageAnchorLine(line) {
			continue
		}
		item, err := decodeCandidateCoverageAnchor(line)
		if err != nil {
			return nil, err
		}
		cov = append(cov, item)
	}
	if len(cov) == 0 {
		return nil, fmt.Errorf("候选覆盖矩阵缺机器锚点（%s）", candidateCoverageAnchorTag)
	}
	want, err := RenderCandidateCoverageMatrix(cov)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(want, body) {
		return nil, fmt.Errorf("候选覆盖矩阵可见内容与机器锚点不一致（fail closed）")
	}
	return cov, nil
}

// ParseCandidateCoverageState reads the canonical draft or final coverage
// block after the last candidate. A single trailing section-framing newline is
// accepted but is not part of the managed block.
func ParseCandidateCoverageState(raw []byte) (CandidateCoverageState, error) {
	start, tail, err := candidateExtractionTail(raw)
	if err != nil {
		return CandidateCoverageState{}, err
	}
	if draft, rendered, ok, err := parseDraftCoveragePrefix(tail); err != nil {
		return CandidateCoverageState{}, err
	} else if ok {
		return CandidateCoverageState{
			Draft: draft,
			start: start,
			end:   start + len(rendered),
		}, nil
	}
	final, rendered, ok, err := parseFinalCoverageTail(tail)
	if err != nil {
		return CandidateCoverageState{}, err
	}
	if !ok {
		return CandidateCoverageState{}, fmt.Errorf(
			"candidate 后缺 canonical 候选覆盖或最终覆盖")
	}
	return CandidateCoverageState{
		Final:     final,
		Finalized: true,
		start:     start,
		end:       start + len(rendered),
	}, nil
}

// FinalizeCandidateExtraction replaces the draft coverage block with a
// canonical final extraction payload. Repeating the exact final payload is a
// byte-level no-op; any other finalized tail is treated as drift.
func FinalizeCandidateExtraction(raw, final []byte) ([]byte, error) {
	if len(final) == 0 || final[len(final)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	state, err := ParseCandidateCoverageState(raw)
	if err != nil {
		return nil, err
	}
	current := raw[state.start:state.end]
	if state.Finalized {
		if !bytes.Equal(current, final) {
			return nil, fmt.Errorf("已物化 candidate 的最终提取结果发生漂移")
		}
		return append([]byte(nil), raw...), nil
	}
	out := make([]byte, 0, len(raw)-(state.end-state.start)+len(final))
	out = append(out, raw[:state.start]...)
	out = append(out, final...)
	out = append(out, raw[state.end:]...)
	got, err := ParseCandidateCoverageState(out)
	if err != nil {
		return nil, fmt.Errorf("candidate 最终提取结果替换后自检失败：%w", err)
	}
	if !got.Finalized || !bytes.Equal(out[got.start:got.end], final) {
		return nil, fmt.Errorf("candidate 最终提取结果替换后字节不一致")
	}
	return out, nil
}

func candidateExtractionTail(raw []byte) (int, []byte, error) {
	candidates, err := ParseCandidates(raw)
	if err != nil {
		return 0, nil, err
	}
	if len(candidates) == 0 {
		return 0, nil, fmt.Errorf("Note 不含 candidate")
	}
	extraction, root, _, _, err := candidateExtraction(raw)
	if err != nil {
		return 0, nil, err
	}
	if root == nil {
		return 0, nil, fmt.Errorf("Note 缺分区「%s」", SecExtraction)
	}
	start := candidates[len(candidates)-1].End
	if start >= extraction.End {
		return 0, nil, fmt.Errorf("candidate 后缺覆盖状态")
	}
	return start, raw[start:extraction.End], nil
}

func parseDraftCoveragePrefix(tail []byte) ([]CandidateCoverage, []byte, bool, error) {
	if !bytes.HasPrefix(tail, []byte(candidateCoverageHeading+"\n")) {
		return nil, nil, false, nil
	}
	var items []CandidateCoverage
	for _, line := range bytes.Split(tail, []byte("\n")) {
		if !isCandidateCoverageAnchorLine(line) {
			continue
		}
		item, err := decodeCandidateCoverageAnchor(line)
		if err != nil {
			return nil, nil, false, err
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, nil, false, fmt.Errorf(
			"候选覆盖矩阵缺机器锚点（%s）", candidateCoverageAnchorTag)
	}
	rendered, err := RenderCandidateCoverageMatrix(items)
	if err != nil {
		return nil, nil, false, err
	}
	if err := matchManagedTail(tail, rendered); err != nil {
		return nil, nil, false, err
	}
	return items, rendered, true, nil
}

func parseFinalCoverageTail(tail []byte) ([]ReviewCoverage, []byte, bool, error) {
	needle := []byte(coverageHeading + "\n")
	at := -1
	for from := 0; from < len(tail); {
		i := bytes.Index(tail[from:], needle)
		if i < 0 {
			break
		}
		i += from
		if i == 0 || tail[i-1] == '\n' {
			at = i
			break
		}
		from = i + 1
	}
	if at < 0 {
		return nil, nil, false, nil
	}
	var items []ReviewCoverage
	for _, line := range bytes.Split(tail[at:], []byte("\n")) {
		if !isCoverageAnchorLine(line) {
			continue
		}
		item, err := decodeCoverageAnchor(line)
		if err != nil {
			return nil, nil, false, err
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, nil, false, fmt.Errorf(
			"最终覆盖矩阵缺机器锚点（%s）", coverageAnchorTag)
	}
	matrix, err := RenderCoverageMatrix(items)
	if err != nil {
		return nil, nil, false, err
	}
	rendered := append(append([]byte(nil), tail[:at]...), matrix...)
	if err := matchManagedTail(tail, rendered); err != nil {
		return nil, nil, false, err
	}
	return items, rendered, true, nil
}

func matchManagedTail(tail, rendered []byte) error {
	if !bytes.HasPrefix(tail, rendered) {
		return fmt.Errorf("candidate 覆盖可见内容与机器锚点不一致（fail closed）")
	}
	rest := tail[len(rendered):]
	if len(rest) != 0 && !bytes.Equal(rest, []byte("\n")) {
		return fmt.Errorf("candidate 覆盖之后含非 canonical 内容")
	}
	return nil
}
