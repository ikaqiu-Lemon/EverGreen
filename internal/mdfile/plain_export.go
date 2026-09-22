package mdfile

import (
	"bytes"
	"fmt"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// PlainExport removes Evergreen's machine-only Markdown protocol while
// preserving every visible byte and its order. Candidate payloads remain in
// place; only anchors, candidate heading attributes, and L2 fence lines are
// removed.
func PlainExport(raw []byte) ([]byte, error) {
	candidates, err := ParseCandidates(raw)
	if err != nil {
		return nil, fmt.Errorf("plain export candidate parse: %w", err)
	}
	headings := make(map[int][]byte, len(candidates))
	for _, candidate := range candidates {
		if candidate.Syntax == CandidateSyntaxFencedDiv {
			continue
		}
		line := raw[candidate.HeadingStart:candidate.HeadingEnd]
		plain, err := plainCandidateHeading(line)
		if err != nil {
			return nil, fmt.Errorf("plain export candidate %s: %w", candidate.Key, err)
		}
		headings[candidate.HeadingStart] = plain
	}

	md := goldmark.New(goldmark.WithParserOptions(parser.WithAttribute()))
	root := md.Parser().Parse(text.NewReader(raw))
	protected := candidateCodeLines(root, raw)

	var out bytes.Buffer
	openDivColons := 0
	for at := 0; at < len(raw); {
		end := lineEnd(raw, at)
		line := raw[at:end]
		if replacement, ok := headings[at]; ok {
			out.Write(replacement)
			at = end
			continue
		}
		if protected[at] {
			out.Write(line)
			at = end
			continue
		}
		if isPlainProtocolAnchor(line) {
			at = end
			continue
		}
		if openDivColons > 0 {
			if n, ok := plainDivClose(line); ok {
				if n < openDivColons {
					return nil, fmt.Errorf(
						"plain export L2 close fence has %d colons; need at least %d",
						n, openDivColons)
				}
				openDivColons = 0
				at = end
				continue
			}
			if plainDivReserved(line) {
				return nil, fmt.Errorf("plain export L2 candidate divs cannot nest")
			}
		} else if n, reserved, err := plainDivOpen(line); err != nil {
			return nil, err
		} else if reserved {
			openDivColons = n
			at = end
			continue
		}
		out.Write(line)
		at = end
	}
	if openDivColons > 0 {
		return nil, fmt.Errorf("plain export L2 candidate div is not closed")
	}
	return out.Bytes(), nil
}

func plainCandidateHeading(line []byte) ([]byte, error) {
	body := trimLineEnd(line)
	at := bytes.LastIndexByte(body, '{')
	if at < 0 {
		return nil, fmt.Errorf("candidate heading has no attribute block")
	}
	prefix := bytes.TrimRight(body[:at], " \t")
	if len(prefix) == 0 {
		return nil, fmt.Errorf("candidate heading becomes empty after removing attributes")
	}
	out := append([]byte(nil), prefix...)
	out = append(out, line[len(body):]...)
	return out, nil
}

func isPlainProtocolAnchor(line []byte) bool {
	t := bytes.TrimSpace(line)
	if !bytes.HasPrefix(t, []byte("<!-- eg:")) || !bytes.HasSuffix(t, []byte("-->")) {
		return false
	}
	for _, family := range [][]byte{
		[]byte("<!-- eg:nr:"),
		[]byte("<!-- eg:cd:"),
		[]byte("<!-- eg:cc:"),
		[]byte("<!-- eg:nc:"),
	} {
		if bytes.HasPrefix(t, family) {
			return true
		}
	}
	return false
}

func plainDivOpen(line []byte) (int, bool, error) {
	width, _, _, reserved, err := parseCandidateFenceOpen(line)
	return width, reserved, err
}

func plainDivReserved(line []byte) bool {
	return candidateFenceReserved(line)
}

func plainDivClose(line []byte) (int, bool) {
	return candidateFenceClose(line)
}
