package mdfile

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

type candidateFence struct {
	openStart  int
	openEnd    int
	closeStart int
	closeEnd   int
	titleStart int
	key        string
	kind       CandidateKind
}

func candidateExtraction(raw []byte) (Span, ast.Node, map[int]bool, []candidateFence, error) {
	doc, err := Parse(raw)
	if err != nil {
		return Span{}, nil, nil, nil, err
	}
	extraction, ok := doc.Section(SecExtraction)
	if !ok {
		return Span{}, nil, nil, nil, nil
	}
	md := goldmark.New(goldmark.WithParserOptions(parser.WithAttribute()))
	root := md.Parser().Parse(text.NewReader(raw))
	protected := candidateCodeLines(root, raw)
	fences, end, err := scanCandidateFences(raw, extraction.Body, protected)
	if err != nil {
		return Span{}, nil, nil, nil, err
	}
	extraction.End = end
	return extraction, root, protected, fences, nil
}

func scanCandidateFences(raw []byte, from int,
	protected map[int]bool,
) ([]candidateFence, int, error) {
	var fences []candidateFence
	for at := from; at < len(raw); {
		end := lineEnd(raw, at)
		if protected[at] {
			at = end
			continue
		}
		if atxHeadingLevel(raw[at:end]) == 2 {
			return fences, at, nil
		}
		width, key, kind, reserved, err := parseCandidateFenceOpen(raw[at:end])
		if err != nil {
			return nil, 0, fmt.Errorf("candidate L2 opener at byte %d: %w", at, err)
		}
		if !reserved {
			if _, close := candidateFenceClose(raw[at:end]); close {
				return nil, 0, fmt.Errorf("candidate L2 孤立闭围栏 at byte %d", at)
			}
			at = end
			continue
		}

		fence := candidateFence{
			openStart: at, openEnd: end, titleStart: end, key: key, kind: kind,
		}
		cursor := end
		closed := false
		for cursor < len(raw) {
			lineEndAt := lineEnd(raw, cursor)
			if protected[cursor] {
				cursor = lineEndAt
				continue
			}
			if closeWidth, close := candidateFenceClose(raw[cursor:lineEndAt]); close {
				if closeWidth < width {
					return nil, 0, fmt.Errorf(
						"candidate L2 %q 闭围栏冒号数 %d 小于开围栏 %d",
						key, closeWidth, width)
				}
				fence.closeStart = cursor
				fence.closeEnd = lineEndAt
				fences = append(fences, fence)
				at = lineEndAt
				closed = true
				break
			}
			_, nestedKey, _, nested, nestedErr :=
				parseCandidateFenceOpen(raw[cursor:lineEndAt])
			if nestedErr != nil {
				return nil, 0, fmt.Errorf(
					"candidate L2 %q 内含畸形嵌套 opener：%w", key, nestedErr)
			}
			if nested {
				return nil, 0, fmt.Errorf(
					"candidate L2 不允许嵌套：%q 内出现 %q", key, nestedKey)
			}
			if candidateFenceOpenShape(raw[cursor:lineEndAt]) {
				return nil, 0, fmt.Errorf(
					"candidate L2 不允许嵌套 fenced div：%q", key)
			}
			cursor = lineEndAt
		}
		if !closed {
			return nil, 0, fmt.Errorf("candidate L2 %q 未闭合", key)
		}
	}
	return fences, len(raw), nil
}

func candidateFenceOpenShape(line []byte) bool {
	t := bytes.TrimSpace(trimLineEnd(line))
	width := leadingByteCount(t, ':')
	return width >= 3 && width < len(t) && (t[width] == ' ' || t[width] == '\t')
}

func parseCandidateFenceOpen(
	line []byte,
) (int, string, CandidateKind, bool, error) {
	t := bytes.TrimSpace(trimLineEnd(line))
	width := leadingByteCount(t, ':')
	reserved := candidateFenceReserved(line)
	if width < 3 || width == len(t) {
		if reserved {
			return 0, "", "", false, fmt.Errorf("candidate L2 opener 形态非法")
		}
		return 0, "", "", false, nil
	}
	if t[width] != ' ' && t[width] != '\t' {
		if reserved {
			return 0, "", "", false, fmt.Errorf("candidate L2 opener 的属性前必须有空白")
		}
		return 0, "", "", false, nil
	}
	attrs := bytes.TrimSpace(t[width:])
	if len(attrs) < 2 || attrs[0] != '{' || attrs[len(attrs)-1] != '}' {
		if reserved {
			return 0, "", "", false, fmt.Errorf("candidate L2 opener 缺合法属性块")
		}
		return 0, "", "", false, nil
	}
	if !reserved {
		return 0, "", "", false, nil
	}
	key, kind, err := parseCandidateAttributes(attrs)
	if err != nil {
		return 0, "", "", false, err
	}
	return width, key, kind, true, nil
}

func parseCandidateAttributes(attrs []byte) (string, CandidateKind, error) {
	attrReader := text.NewReader(attrs)
	parsed, ok := parser.ParseAttributes(attrReader)
	if !ok {
		return "", "", fmt.Errorf("candidate 属性语法非法")
	}
	left, _ := attrReader.PeekLine()
	if len(bytes.TrimSpace(left)) != 0 {
		return "", "", fmt.Errorf("candidate 属性后含多余字节")
	}

	var key string
	var classes []string
	idCount := 0
	for _, attr := range parsed {
		switch string(attr.Name) {
		case "id":
			idCount++
			value, ok := attr.Value.([]byte)
			if !ok {
				return "", "", fmt.Errorf("candidate id 必须是字符串")
			}
			key = string(value)
		case "class":
			value, ok := attr.Value.([]byte)
			if !ok {
				return "", "", fmt.Errorf("candidate class 必须是字符串")
			}
			for _, class := range bytes.Fields(value) {
				classes = append(classes, string(class))
			}
		default:
			return "", "", fmt.Errorf("candidate 含未知属性 %q", attr.Name)
		}
	}
	if idCount != 1 || !candidateKeyRE.MatchString(key) {
		return "", "", fmt.Errorf(
			"candidate id 必须恰一个且匹配 %s：%q", candidateKeyRE, key)
	}
	classCount := map[string]int{}
	for _, class := range classes {
		classCount[class]++
	}
	if len(classes) != 2 || classCount["eg-candidate"] != 1 {
		return "", "", fmt.Errorf(
			"candidate classes 必须恰含 .eg-candidate 与一个 kind class：%v", classes)
	}
	switch {
	case classCount["knowledge"] == 1 && classCount["opinion"] == 0:
		return key, CandidateKindKnowledge, nil
	case classCount["opinion"] == 1 && classCount["knowledge"] == 0:
		return key, CandidateKindOpinion, nil
	default:
		return "", "", fmt.Errorf(
			"candidate kind class 必须恰为 .knowledge 或 .opinion：%v", classes)
	}
}

func candidateFenceReserved(line []byte) bool {
	t := bytes.TrimSpace(trimLineEnd(line))
	if leadingByteCount(t, ':') < 3 {
		return false
	}
	return bytes.Contains(t, []byte(".eg-candidate")) ||
		bytes.Contains(t, []byte(".knowledge")) ||
		bytes.Contains(t, []byte(".opinion")) ||
		bytes.Contains(t, []byte("#cand-"))
}

func candidateFenceClose(line []byte) (int, bool) {
	t := bytes.TrimSpace(trimLineEnd(line))
	width := leadingByteCount(t, ':')
	return width, width >= 3 && width == len(t)
}

func leadingByteCount(raw []byte, want byte) int {
	n := 0
	for n < len(raw) && raw[n] == want {
		n++
	}
	return n
}

func candidateFenceContaining(fences []candidateFence, offset int) (candidateFence, bool) {
	for _, fence := range fences {
		if offset >= fence.openEnd && offset < fence.closeStart {
			return fence, true
		}
	}
	return candidateFence{}, false
}

func nextCandidateContent(raw []byte, from, limit int) int {
	for at := from; at < limit; {
		end := lineEnd(raw, at)
		if !isBlankReviewLine(raw[at:end]) {
			return at
		}
		at = end
	}
	return limit
}
