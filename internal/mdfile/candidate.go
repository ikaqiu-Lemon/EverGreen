package mdfile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

const (
	candidateAnchorFamily  = "eg:cd:"
	candidateAnchorVersion = "1"
	candidateAnchorTag     = candidateAnchorFamily + candidateAnchorVersion
	candidateAnchorOpen    = "<!-- " + candidateAnchorTag + " "
	candidateAnchorClose   = " -->"
)

// CandidateKind is the closed candidate type set encoded by the H3 classes.
type CandidateKind string

const (
	CandidateKindKnowledge CandidateKind = "knowledge"
	CandidateKindOpinion   CandidateKind = "opinion"
)

func (k CandidateKind) valid() bool {
	return k == CandidateKindKnowledge || k == CandidateKindOpinion
}

// CandidateAnchor is the versioned machine metadata immediately preceding a candidate H3.
type CandidateAnchor struct {
	SourceRefs []string
	Rel        string
	Reason     string
	Tags       []string
	Output     string
}

// CandidateDraftSection is one H4 template section supplied by write_note.
// Body is user content, not the blank-line framing around an H4 payload.
type CandidateDraftSection struct {
	Name string
	Body []byte
}

// CandidateDraft is the deterministic input used to render one H3 candidate.
type CandidateDraft struct {
	Key        string
	Kind       CandidateKind
	Title      string
	SourceRefs []string
	Rel        string
	Reason     string
	Tags       []string
	Output     string
	Sections   []CandidateDraftSection
}

// CandidateSection identifies one direct H4 payload. Payload is the exact
// half-open source interval BodyStart:End, including its blank-line framing.
type CandidateSection struct {
	Name         string
	HeadingStart int
	HeadingEnd   int
	BodyStart    int
	End          int
	Payload      []byte
}

// Candidate is one parsed H3 candidate and its exact source intervals.
type Candidate struct {
	Key          string
	Kind         CandidateKind
	Title        string
	Anchor       CandidateAnchor
	AnchorStart  int
	AnchorEnd    int
	HeadingStart int
	HeadingEnd   int
	End          int
	ContentEnd   int
	Sections     []CandidateSection
}

// Raw returns the candidate's L1 interval, beginning at its H3 heading.
func (c Candidate) Raw(raw []byte) []byte {
	if c.HeadingStart < 0 || c.ContentEnd < c.HeadingStart || c.ContentEnd > len(raw) {
		return nil
	}
	return raw[c.HeadingStart:c.ContentEnd]
}

type candidateAnchorWire struct {
	SourceRefs []string `json:"source_refs"`
	Rel        string   `json:"rel"`
	Reason     string   `json:"reason"`
	Tags       []string `json:"tags"`
	Output     string   `json:"output"`
}

var candidateKeyRE = regexp.MustCompile(
	`^cand-[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func candidateAnchorFromDraft(d CandidateDraft) CandidateAnchor {
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	return CandidateAnchor{
		SourceRefs: d.SourceRefs,
		Rel:        d.Rel,
		Reason:     d.Reason,
		Tags:       tags,
		Output:     d.Output,
	}
}

func encodeCandidateAnchor(a CandidateAnchor) string {
	tags := a.Tags
	if tags == nil {
		tags = []string{}
	}
	raw, err := json.Marshal(candidateAnchorWire{
		SourceRefs: a.SourceRefs,
		Rel:        a.Rel,
		Reason:     a.Reason,
		Tags:       tags,
		Output:     a.Output,
	})
	if err != nil {
		panic(fmt.Sprintf("candidate anchor encoding failed: %v", err))
	}
	return candidateAnchorOpen + base64.RawURLEncoding.EncodeToString(raw) + candidateAnchorClose
}

func isCandidateAnchorLine(line []byte) bool {
	return bytes.HasPrefix(bytes.TrimSpace(line), []byte("<!-- "+candidateAnchorFamily))
}

func decodeCandidateAnchor(line []byte, kind CandidateKind) (CandidateAnchor, error) {
	t := bytes.TrimSpace(line)
	if !bytes.HasPrefix(t, []byte(candidateAnchorOpen)) {
		return CandidateAnchor{}, fmt.Errorf(
			"未知的 candidate 锚点版本（仅支持 %s）：%q", candidateAnchorTag, t)
	}
	if !bytes.HasSuffix(t, []byte(candidateAnchorClose)) {
		return CandidateAnchor{}, fmt.Errorf("candidate 锚点缺注释结束符：%q", t)
	}
	enc := t[len(candidateAnchorOpen) : len(t)-len(candidateAnchorClose)]
	raw, err := base64.RawURLEncoding.DecodeString(string(enc))
	if err != nil {
		return CandidateAnchor{}, fmt.Errorf("candidate 锚点载荷 base64url 非法：%v", err)
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return CandidateAnchor{}, fmt.Errorf("candidate 锚点载荷含重复键：%v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return CandidateAnchor{}, fmt.Errorf("candidate 锚点载荷 JSON 非法：%v", err)
	}
	want := map[string]bool{
		"source_refs": true,
		"rel":         true,
		"reason":      true,
		"tags":        true,
		"output":      true,
	}
	if len(fields) != len(want) {
		return CandidateAnchor{}, fmt.Errorf(
			"candidate 锚点字段集不完整：必须恰含 source_refs/rel/reason/tags/output")
	}
	for key := range fields {
		if !want[key] {
			return CandidateAnchor{}, fmt.Errorf("candidate 锚点含未知字段 %q", key)
		}
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var wire candidateAnchorWire
	if err := dec.Decode(&wire); err != nil {
		return CandidateAnchor{}, fmt.Errorf("candidate 锚点载荷 JSON 非法：%v", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return CandidateAnchor{}, fmt.Errorf("candidate 锚点载荷含多余数据（应恰一个 JSON 对象）")
	}
	a := CandidateAnchor{
		SourceRefs: wire.SourceRefs,
		Rel:        wire.Rel,
		Reason:     wire.Reason,
		Tags:       wire.Tags,
		Output:     wire.Output,
	}
	if err := validateCandidateAnchor(a, kind); err != nil {
		return CandidateAnchor{}, err
	}
	return a, nil
}

func validateCandidateAnchor(a CandidateAnchor, kind CandidateKind) error {
	if len(a.SourceRefs) == 0 {
		return fmt.Errorf("candidate anchor source_refs 为空")
	}
	for i, ref := range a.SourceRefs {
		if strings.TrimSpace(ref) == "" {
			return fmt.Errorf("candidate anchor source_refs[%d] 为空", i)
		}
	}
	if _, err := model.ParseMaterialRel(a.Rel); err != nil {
		return fmt.Errorf("candidate anchor rel 非法：%w", err)
	}
	if strings.TrimSpace(a.Reason) == "" {
		return fmt.Errorf("candidate anchor reason 为空")
	}
	if a.Tags == nil {
		return fmt.Errorf("candidate anchor tags 必须是数组")
	}
	for i, tag := range a.Tags {
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("candidate anchor tags[%d] 为空", i)
		}
	}
	if a.Output == "" {
		return nil
	}
	switch kind {
	case CandidateKindKnowledge:
		if _, err := model.ParseCardID(a.Output); err != nil {
			return fmt.Errorf("knowledge candidate output 非法：%w", err)
		}
	case CandidateKindOpinion:
		if _, err := model.ParseOpinionID(a.Output); err != nil {
			return fmt.Errorf("opinion candidate output 非法：%w", err)
		}
	default:
		return fmt.Errorf("candidate kind 越界：%q", kind)
	}
	return nil
}

// RenderCandidateDraft emits one canonical anchor + H3 + ordered H4 payload sequence.
func RenderCandidateDraft(d CandidateDraft) ([]byte, error) {
	if err := validateCandidateDraft(d); err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, encodeCandidateAnchor(candidateAnchorFromDraft(d))...)
	out = append(out, '\n')
	out = append(out, "### "...)
	out = append(out, d.Title...)
	out = append(out, " {#"...)
	out = append(out, d.Key...)
	out = append(out, " .eg-candidate ."...)
	out = append(out, string(d.Kind)...)
	out = append(out, '}', '\n')
	for i, section := range d.Sections {
		if i == 0 {
			out = append(out, '\n')
		}
		out = append(out, "#### "...)
		out = append(out, section.Name...)
		out = append(out, '\n', '\n')
		out = append(out, section.Body...)
		out = append(out, '\n')
	}
	return out, nil
}

func validateCandidateDraft(d CandidateDraft) error {
	if !candidateKeyRE.MatchString(d.Key) {
		return fmt.Errorf("candidate key 非法：%q", d.Key)
	}
	if !d.Kind.valid() {
		return fmt.Errorf("candidate kind 越界：%q", d.Kind)
	}
	if strings.TrimSpace(d.Title) == "" || d.Title != strings.TrimSpace(d.Title) ||
		strings.ContainsAny(d.Title, "\r\n") {
		return fmt.Errorf("candidate title 必须是无首尾空白的非空单行文本")
	}
	if err := validateCandidateAnchor(candidateAnchorFromDraft(d), d.Kind); err != nil {
		return err
	}
	if err := validateDraftSections(d.Kind, d.Sections); err != nil {
		return err
	}
	return nil
}

func candidateSectionOrder(kind CandidateKind) ([]string, string) {
	switch kind {
	case CandidateKindKnowledge:
		return []string{SecKnowledge, SecBoundary}, SecKnowledge
	case CandidateKindOpinion:
		return []string{SecOpinionClaim, SecArgument, SecCounter, SecToVerify}, SecOpinionClaim
	default:
		return nil, ""
	}
}

func validateDraftSections(kind CandidateKind, sections []CandidateDraftSection) error {
	order, required := candidateSectionOrder(kind)
	if order == nil {
		return fmt.Errorf("candidate kind 越界：%q", kind)
	}
	rank := make(map[string]int, len(order))
	for i, name := range order {
		rank[name] = i
	}
	seen := map[string]bool{}
	last := -1
	for i, section := range sections {
		r, ok := rank[section.Name]
		if !ok {
			return fmt.Errorf("%s candidate 的 sections[%d]=%q 不在模板 %v", kind, i, section.Name, order)
		}
		if seen[section.Name] {
			return fmt.Errorf("%s candidate 的 H4 分区 %q 重复", kind, section.Name)
		}
		if r <= last {
			return fmt.Errorf("%s candidate 的 H4 分区顺序错误：必须按 %v", kind, order)
		}
		if len(section.Body) == 0 || section.Body[len(section.Body)-1] != '\n' {
			return fmt.Errorf("candidate H4 %q 的 body 必须非空且以换行结束", section.Name)
		}
		if len(bytes.TrimSpace(section.Body)) == 0 {
			return fmt.Errorf("candidate H4 %q 的 body 为空", section.Name)
		}
		seen[section.Name] = true
		last = r
	}
	if !seen[required] {
		return fmt.Errorf("%s candidate 缺必需 H4 分区 %q", kind, required)
	}
	return nil
}

type candidateHeading struct {
	node        *ast.Heading
	start       int
	end         int
	title       string
	key         string
	kind        CandidateKind
	isCandidate bool
}

type sourceLine struct {
	start int
	end   int
}

// ParseCandidates reads candidates only from the Note's 提取结果 H2.
func ParseCandidates(raw []byte) ([]Candidate, error) {
	doc, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	extraction, ok := doc.Section(SecExtraction)
	if !ok {
		return nil, nil
	}

	md := goldmark.New(goldmark.WithParserOptions(parser.WithAttribute()))
	root := md.Parser().Parse(text.NewReader(raw))
	protected := candidateCodeLines(root, raw)
	var headings []candidateHeading
	searchFrom := doc.BodyFrom
	err = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		h, ok := n.(*ast.Heading)
		if !ok || h.Parent() != root {
			return ast.WalkContinue, nil
		}
		start, end, title, locErr := candidateHeadingSpan(raw, h, searchFrom, protected)
		if locErr != nil {
			return ast.WalkStop, locErr
		}
		searchFrom = end
		info := candidateHeading{node: h, start: start, end: end, title: title}
		if start >= extraction.Body && start < extraction.End && h.Level == 3 {
			info.key, info.kind, info.isCandidate, locErr = parseCandidateHeading(raw[start:end], h)
			if locErr != nil {
				return ast.WalkStop, fmt.Errorf("candidate H3 at byte %d: %w", start, locErr)
			}
		}
		headings = append(headings, info)
		return ast.WalkContinue, nil
	})
	if err != nil {
		return nil, err
	}

	anchors := map[int]sourceLine{}
	for at := extraction.Body; at < extraction.End; {
		end := lineEnd(raw, at)
		if end > extraction.End {
			end = extraction.End
		}
		if !protected[at] && isCandidateAnchorLine(raw[at:end]) {
			anchors[end] = sourceLine{start: at, end: end}
		}
		at = end
	}

	usedAnchors := map[int]bool{}
	seenKeys := map[string]bool{}
	var out []Candidate
	for i, h := range headings {
		if h.start < extraction.Body || h.start >= extraction.End || !h.isCandidate {
			continue
		}
		anchorLine, ok := anchors[h.start]
		if !ok {
			return nil, fmt.Errorf("candidate %q 缺少逐行相邻的 %s 锚点", h.key, candidateAnchorTag)
		}
		anchor, err := decodeCandidateAnchor(raw[anchorLine.start:anchorLine.end], h.kind)
		if err != nil {
			return nil, fmt.Errorf("candidate %q：%w", h.key, err)
		}
		if seenKeys[h.key] {
			return nil, fmt.Errorf("同一 Note 内 candidate key 重复：%q", h.key)
		}
		seenKeys[h.key] = true
		usedAnchors[anchorLine.start] = true

		boundary := extraction.End
		for j := i + 1; j < len(headings); j++ {
			next := headings[j]
			if next.start <= h.start {
				continue
			}
			if next.start >= extraction.End {
				break
			}
			if next.node.Level <= 3 {
				boundary = next.start
				break
			}
		}
		contentEnd := boundary
		if line, exists := anchors[boundary]; exists {
			contentEnd = line.start
		}
		sections, err := parseCandidateSections(raw, headings, i, h.kind, h.end, contentEnd)
		if err != nil {
			return nil, fmt.Errorf("candidate %q：%w", h.key, err)
		}
		out = append(out, Candidate{
			Key:          h.key,
			Kind:         h.kind,
			Title:        h.title,
			Anchor:       anchor,
			AnchorStart:  anchorLine.start,
			AnchorEnd:    anchorLine.end,
			HeadingStart: h.start,
			HeadingEnd:   h.end,
			End:          boundary,
			ContentEnd:   contentEnd,
			Sections:     sections,
		})
	}
	for _, line := range anchors {
		if !usedAnchors[line.start] {
			return nil, fmt.Errorf("%s 锚点未与下一行合法 candidate H3 配对（byte %d）",
				candidateAnchorTag, line.start)
		}
	}
	return out, nil
}

func candidateHeadingSpan(raw []byte, h *ast.Heading, searchFrom int,
	protected map[int]bool) (int, int, string, error) {
	if h.Lines() == nil || h.Lines().Len() == 0 {
		for at := searchFrom; at < len(raw); {
			end := lineEnd(raw, at)
			if !protected[at] && atxHeadingLevel(raw[at:end]) == h.Level {
				return at, end, "", nil
			}
			at = end
		}
		return 0, 0, "", fmt.Errorf("无法定位 level=%d 空标题的源字节", h.Level)
	}
	seg := h.Lines().At(0)
	start := seg.Start
	if before := bytes.LastIndexByte(raw[:start], '\n'); before >= 0 {
		start = before + 1
	} else {
		start = 0
	}
	end := lineEnd(raw, start)
	title := string(h.Lines().Value(raw))
	return start, end, title, nil
}

func parseCandidateHeading(line []byte, h *ast.Heading) (string, CandidateKind, bool, error) {
	trimmed := trimLineEnd(line)
	attrStart := bytes.LastIndexByte(trimmed, '{')
	rawReserved := false
	if attrStart >= 0 {
		tail := trimmed[attrStart:]
		rawReserved = bytes.Contains(tail, []byte(".eg-candidate")) ||
			bytes.Contains(tail, []byte(".knowledge")) ||
			bytes.Contains(tail, []byte(".opinion")) ||
			bytes.Contains(tail, []byte("#cand-"))
	}
	astReserved := false
	for _, attr := range h.Attributes() {
		if bytes.Equal(attr.Name, []byte("id")) {
			if value, ok := attr.Value.([]byte); ok && bytes.HasPrefix(value, []byte("cand-")) {
				astReserved = true
			}
		}
		if bytes.Equal(attr.Name, []byte("class")) {
			if value, ok := attr.Value.([]byte); ok {
				for _, class := range bytes.Fields(value) {
					if bytes.Equal(class, []byte("eg-candidate")) ||
						bytes.Equal(class, []byte("knowledge")) ||
						bytes.Equal(class, []byte("opinion")) {
						astReserved = true
					}
				}
			}
		}
	}
	if !rawReserved && !astReserved {
		return "", "", false, nil
	}
	if !isATXHeadingLevel(trimmed, 3) {
		return "", "", false, fmt.Errorf("candidate 标题必须是 ATX H3")
	}
	if attrStart < 0 {
		return "", "", false, fmt.Errorf("candidate 标题缺属性块")
	}
	attrReader := text.NewReader(trimmed[attrStart:])
	attrs, ok := parser.ParseAttributes(attrReader)
	if !ok {
		return "", "", false, fmt.Errorf("candidate 标题属性语法非法")
	}
	left, _ := attrReader.PeekLine()
	if len(bytes.TrimSpace(left)) != 0 {
		return "", "", false, fmt.Errorf("candidate 标题属性后含多余字节")
	}

	var key string
	var classes []string
	idCount := 0
	for _, attr := range attrs {
		switch string(attr.Name) {
		case "id":
			idCount++
			value, ok := attr.Value.([]byte)
			if !ok {
				return "", "", false, fmt.Errorf("candidate id 必须是字符串")
			}
			key = string(value)
		case "class":
			value, ok := attr.Value.([]byte)
			if !ok {
				return "", "", false, fmt.Errorf("candidate class 必须是字符串")
			}
			for _, class := range bytes.Fields(value) {
				classes = append(classes, string(class))
			}
		default:
			return "", "", false, fmt.Errorf("candidate 标题含未知属性 %q", attr.Name)
		}
	}
	if idCount != 1 || !candidateKeyRE.MatchString(key) {
		return "", "", false, fmt.Errorf("candidate id 必须恰一个且匹配 %s：%q", candidateKeyRE, key)
	}
	classCount := map[string]int{}
	for _, class := range classes {
		classCount[class]++
	}
	if len(classes) != 2 || classCount["eg-candidate"] != 1 {
		return "", "", false, fmt.Errorf(
			"candidate classes 必须恰含 .eg-candidate 与一个 kind class：%v", classes)
	}
	var kind CandidateKind
	switch {
	case classCount["knowledge"] == 1 && classCount["opinion"] == 0:
		kind = CandidateKindKnowledge
	case classCount["opinion"] == 1 && classCount["knowledge"] == 0:
		kind = CandidateKindOpinion
	default:
		return "", "", false, fmt.Errorf("candidate kind class 必须恰为 .knowledge 或 .opinion：%v", classes)
	}
	return key, kind, true, nil
}

func isATXHeadingLevel(line []byte, level int) bool {
	return atxHeadingLevel(line) == level
}

func atxHeadingLevel(line []byte) int {
	line = trimLineEnd(line)
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	start := i
	for i < len(line) && line[i] == '#' {
		i++
	}
	level := i - start
	if level < 1 || level > 6 {
		return 0
	}
	if i == len(line) {
		return level
	}
	if line[i] == ' ' || line[i] == '\t' {
		return level
	}
	return 0
}

func candidateCodeLines(root ast.Node, raw []byte) map[int]bool {
	protected := map[int]bool{}
	sc := &scanner{src: raw}
	sc.indexLines()
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var start, end int
		switch n.(type) {
		case *ast.FencedCodeBlock:
			var ok bool
			start, end, ok = sc.fenceExtent(n)
			if !ok {
				return ast.WalkContinue, nil
			}
		case *ast.CodeBlock:
			start, end = sc.blockSpan(n, 1)
		default:
			return ast.WalkContinue, nil
		}
		for line := start; line <= end && line <= len(sc.lineStarts); line++ {
			protected[sc.lineStarts[line-1]] = true
		}
		return ast.WalkContinue, nil
	})
	return protected
}

func parseCandidateSections(raw []byte, headings []candidateHeading, candidateIndex int,
	kind CandidateKind, from, to int) ([]CandidateSection, error) {
	var h4 []candidateHeading
	for i := candidateIndex + 1; i < len(headings); i++ {
		h := headings[i]
		if h.start >= to {
			break
		}
		if h.start >= from && h.node.Level == 4 {
			if len(h.node.Attributes()) != 0 {
				return nil, fmt.Errorf("H4 模板分区 %q 不得带属性", h.title)
			}
			h4 = append(h4, h)
		}
	}
	sections := make([]CandidateSection, len(h4))
	for i, h := range h4 {
		end := to
		if i+1 < len(h4) {
			end = h4[i+1].start
		}
		sections[i] = CandidateSection{
			Name:         h.title,
			HeadingStart: h.start,
			HeadingEnd:   h.end,
			BodyStart:    h.end,
			End:          end,
			Payload:      raw[h.end:end],
		}
	}
	if err := validateParsedSections(kind, sections); err != nil {
		return nil, err
	}
	return sections, nil
}

func validateParsedSections(kind CandidateKind, sections []CandidateSection) error {
	order, required := candidateSectionOrder(kind)
	rank := make(map[string]int, len(order))
	for i, name := range order {
		rank[name] = i
	}
	seen := map[string]bool{}
	last := -1
	for _, section := range sections {
		r, ok := rank[section.Name]
		if !ok {
			return fmt.Errorf("%s candidate 含未知 H4 分区 %q（仅允许 %v）", kind, section.Name, order)
		}
		if seen[section.Name] {
			return fmt.Errorf("%s candidate 的 H4 分区 %q 重复", kind, section.Name)
		}
		if r <= last {
			return fmt.Errorf("%s candidate 的 H4 分区顺序错误：必须按 %v", kind, order)
		}
		if section.Name == required && len(bytes.TrimSpace(section.Payload)) == 0 {
			return fmt.Errorf("%s candidate 的必需 H4 分区 %q 为空", kind, required)
		}
		seen[section.Name] = true
		last = r
	}
	if !seen[required] {
		return fmt.Errorf("%s candidate 缺必需 H4 分区 %q", kind, required)
	}
	return nil
}

// ReplaceCandidateSection replaces one candidate H4 payload and leaves every
// byte outside that half-open interval unchanged.
func ReplaceCandidateSection(raw []byte, key, section string, content []byte) ([]byte, error) {
	if len(content) == 0 || content[len(content)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return nil, fmt.Errorf("candidate H4 新正文为空")
	}
	candidates, err := ParseCandidates(raw)
	if err != nil {
		return nil, err
	}
	var candidate *Candidate
	for i := range candidates {
		if candidates[i].Key == key {
			candidate = &candidates[i]
			break
		}
	}
	if candidate == nil {
		return nil, fmt.Errorf("candidate 不存在：%s", key)
	}
	if candidate.Anchor.Output != "" {
		return nil, fmt.Errorf("candidate %s 已物化为 %s，不得再通过命令式路径编辑",
			key, candidate.Anchor.Output)
	}
	var target *CandidateSection
	for i := range candidate.Sections {
		if candidate.Sections[i].Name == section {
			target = &candidate.Sections[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("candidate %s 不含 H4 分区 %q", key, section)
	}
	payload := make([]byte, 0, len(content)+2)
	payload = append(payload, '\n')
	payload = append(payload, content...)
	payload = append(payload, '\n')
	out := make([]byte, 0, len(raw)-(target.End-target.BodyStart)+len(payload))
	out = append(out, raw[:target.BodyStart]...)
	out = append(out, payload...)
	out = append(out, raw[target.End:]...)

	parsed, err := ParseCandidates(out)
	if err != nil {
		return nil, fmt.Errorf("candidate 分区替换后自检失败：%w", err)
	}
	for _, c := range parsed {
		if c.Key != key {
			continue
		}
		for _, s := range c.Sections {
			if s.Name == section && bytes.Equal(s.Payload, payload) {
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("candidate 分区替换后自检失败：目标 payload 不一致")
}
