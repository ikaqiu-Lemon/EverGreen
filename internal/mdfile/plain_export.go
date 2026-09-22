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
	t := bytes.TrimSpace(trimLineEnd(line))
	n := leadingByteCount(t, ':')
	reserved := plainDivReserved(line)
	if n < 3 || n == len(t) || (t[n] != ' ' && t[n] != '\t') {
		if reserved {
			return 0, false, fmt.Errorf("plain export malformed reserved L2 candidate opener")
		}
		return 0, false, nil
	}
	attrs := bytes.TrimSpace(t[n:])
	if len(attrs) < 2 || attrs[0] != '{' || attrs[len(attrs)-1] != '}' {
		if reserved {
			return 0, false, fmt.Errorf("plain export malformed reserved L2 candidate attributes")
		}
		return 0, false, nil
	}
	fields := bytes.Fields(attrs[1 : len(attrs)-1])
	if len(fields) != 3 {
		if reserved {
			return 0, false, fmt.Errorf("plain export L2 candidate attributes must contain id and two classes")
		}
		return 0, false, nil
	}
	var key string
	classes := map[string]int{}
	for _, field := range fields {
		switch {
		case bytes.HasPrefix(field, []byte("#")):
			if key != "" {
				return 0, false, fmt.Errorf("plain export L2 candidate has duplicate id")
			}
			key = string(field[1:])
		case bytes.HasPrefix(field, []byte(".")):
			classes[string(field[1:])]++
		default:
			return 0, false, fmt.Errorf("plain export L2 candidate has invalid attribute %q", field)
		}
	}
	if !candidateKeyRE.MatchString(key) || classes["eg-candidate"] != 1 ||
		len(classes) != 2 ||
		(classes["knowledge"] != 1 && classes["opinion"] != 1) {
		if reserved {
			return 0, false, fmt.Errorf("plain export malformed reserved L2 candidate attributes")
		}
		return 0, false, nil
	}
	return n, true, nil
}

func plainDivReserved(line []byte) bool {
	t := bytes.TrimSpace(trimLineEnd(line))
	if leadingByteCount(t, ':') < 3 {
		return false
	}
	return bytes.Contains(t, []byte(".eg-candidate")) ||
		bytes.Contains(t, []byte(".knowledge")) ||
		bytes.Contains(t, []byte(".opinion")) ||
		bytes.Contains(t, []byte("#cand-"))
}

func plainDivClose(line []byte) (int, bool) {
	t := bytes.TrimSpace(trimLineEnd(line))
	n := leadingByteCount(t, ':')
	return n, n >= 3 && n == len(t)
}

func leadingByteCount(raw []byte, want byte) int {
	n := 0
	for n < len(raw) && raw[n] == want {
		n++
	}
	return n
}
