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
