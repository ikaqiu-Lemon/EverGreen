package mdfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

const (
	noteBlockManifestFamily  = "eg:nb:"
	noteBlockManifestVersion = "1"
	noteBlockManifestOpen    = "<!-- " + noteBlockManifestFamily + noteBlockManifestVersion + " "
	noteBlockManifestClose   = " -->"
)

// NoteBlockManifestEntry records one version-local B reference outside the
// editable Note. The body itself stays only in n-*; ns-* keeps its digest and
// provenance fields so candidate references remain auditable.
type NoteBlockManifestEntry struct {
	Ref         string
	Role        string
	Heading     string
	SourceRef   string
	Annotation  string
	Label       string
	ContentHash string
}

// NoteBlockManifest is the block vocabulary for one exact note_hash.
type NoteBlockManifest struct {
	Blocks    []NoteBlockManifestEntry
	Omissions []ReviewOmission
}

type noteBlockManifestWire struct {
	Blocks    []noteBlockManifestEntryWire `json:"blocks"`
	Omissions []noteBlockOmissionWire      `json:"omissions"`
}

type noteBlockManifestEntryWire struct {
	Ref         string `json:"ref"`
	Role        string `json:"role"`
	Heading     string `json:"heading,omitempty"`
	SourceRef   string `json:"source_ref,omitempty"`
	Annotation  string `json:"annotation,omitempty"`
	Label       string `json:"label,omitempty"`
	ContentHash string `json:"content_hash"`
}

type noteBlockOmissionWire struct {
	SourceRef string `json:"source_ref"`
	Reason    string `json:"reason"`
}

// RenderNoteBlockManifest moves review-block identity into ns-* so n-* can be
// plain Markdown without any Evergreen machine anchors.
func RenderNoteBlockManifest(
	blocks []ReviewBlock,
	omissions []ReviewOmission,
) ([]byte, error) {
	if _, err := RenderReviewNote(blocks, omissions); err != nil {
		return nil, err
	}
	wire := noteBlockManifestWire{
		Blocks:    make([]noteBlockManifestEntryWire, len(blocks)),
		Omissions: make([]noteBlockOmissionWire, len(omissions)),
	}
	for i, block := range blocks {
		sum := sha256.Sum256(bytes.Trim(block.Body, "\n"))
		wire.Blocks[i] = noteBlockManifestEntryWire{
			Ref:         fmt.Sprintf("B%d", i+1),
			Role:        block.Role,
			Heading:     block.Heading,
			SourceRef:   block.SourceRef,
			Annotation:  block.Annotation,
			Label:       block.Label,
			ContentHash: fmt.Sprintf("sha256:%x", sum),
		}
	}
	for i, omission := range omissions {
		wire.Omissions[i] = noteBlockOmissionWire{
			SourceRef: omission.SourceRef,
			Reason:    omission.Reason,
		}
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("note block manifest JSON 编码失败：%w", err)
	}
	return []byte(noteBlockManifestOpen +
		base64.RawURLEncoding.EncodeToString(raw) + noteBlockManifestClose + "\n"), nil
}

// ParseNoteBlockManifest reads the single eg:nb:1 anchor from an ns-* file.
// found=false is reserved for workspaces written before the anchorless Note
// format; callers may then use the legacy n-* review anchors.
func ParseNoteBlockManifest(raw []byte) (NoteBlockManifest, bool, error) {
	var encoded []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if !bytes.HasPrefix(trimmed, []byte("<!-- "+noteBlockManifestFamily)) {
			continue
		}
		if encoded != nil {
			return NoteBlockManifest{}, false,
				fmt.Errorf("ns-* 含多个 note block manifest")
		}
		if !bytes.HasPrefix(trimmed, []byte(noteBlockManifestOpen)) ||
			!bytes.HasSuffix(trimmed, []byte(noteBlockManifestClose)) {
			return NoteBlockManifest{}, false,
				fmt.Errorf("未知或畸形的 note block manifest：%q", trimmed)
		}
		encoded = append([]byte(nil),
			trimmed[len(noteBlockManifestOpen):len(trimmed)-len(noteBlockManifestClose)]...)
	}
	if encoded == nil {
		return NoteBlockManifest{}, false, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(string(encoded))
	if err != nil {
		return NoteBlockManifest{}, false,
			fmt.Errorf("note block manifest base64url 非法：%w", err)
	}
	if err := rejectDuplicateJSONKeys(payload); err != nil {
		return NoteBlockManifest{}, false,
			fmt.Errorf("note block manifest 含重复 JSON 键：%w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	var wire noteBlockManifestWire
	if err := dec.Decode(&wire); err != nil {
		return NoteBlockManifest{}, false,
			fmt.Errorf("note block manifest JSON 非法：%w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return NoteBlockManifest{}, false,
			fmt.Errorf("note block manifest 必须恰含一个 JSON 对象")
	}
	if len(wire.Blocks) == 0 {
		return NoteBlockManifest{}, false,
			fmt.Errorf("note block manifest 的 blocks 为空")
	}
	out := NoteBlockManifest{
		Blocks:    make([]NoteBlockManifestEntry, len(wire.Blocks)),
		Omissions: make([]ReviewOmission, len(wire.Omissions)),
	}
	for i, block := range wire.Blocks {
		wantRef := fmt.Sprintf("B%d", i+1)
		if block.Ref != wantRef {
			return NoteBlockManifest{}, false,
				fmt.Errorf("note block manifest blocks[%d].ref=%q，期望 %q",
					i, block.Ref, wantRef)
		}
		switch block.Role {
		case ReviewRoleSource:
			if block.SourceRef == "" || block.Annotation != "" || block.Label != "" {
				return NoteBlockManifest{}, false,
					fmt.Errorf("note block manifest %s 的 source 元数据非法", block.Ref)
			}
		case ReviewRoleAgent:
			if block.SourceRef != "" ||
				validateAgentAnchorAnnotation(block.Annotation, block.Label) != nil {
				return NoteBlockManifest{}, false,
					fmt.Errorf("note block manifest %s 的 agent 元数据非法", block.Ref)
			}
		default:
			return NoteBlockManifest{}, false,
				fmt.Errorf("note block manifest %s 的 role=%q 非法", block.Ref, block.Role)
		}
		if !contentHashRE.MatchString(block.ContentHash) {
			return NoteBlockManifest{}, false,
				fmt.Errorf("note block manifest %s 的 content_hash 非法：%q",
					block.Ref, block.ContentHash)
		}
		out.Blocks[i] = NoteBlockManifestEntry{
			Ref: block.Ref, Role: block.Role, Heading: block.Heading,
			SourceRef: block.SourceRef, Annotation: block.Annotation,
			Label: block.Label, ContentHash: block.ContentHash,
		}
	}
	for i, omission := range wire.Omissions {
		if omission.SourceRef == "" || omission.Reason == "" {
			return NoteBlockManifest{}, false,
				fmt.Errorf("note block manifest omissions[%d] 缺 source_ref/reason", i)
		}
		out.Omissions[i] = ReviewOmission{
			SourceRef: omission.SourceRef,
			Reason:    omission.Reason,
		}
	}
	return out, true, nil
}

// ReplaceNoteBlockManifest replaces the single eg:nb manifest line while
// preserving every other workspace byte.
func ReplaceNoteBlockManifest(
	raw []byte,
	blocks []ReviewBlock,
	omissions []ReviewOmission,
) ([]byte, error) {
	if _, found, err := ParseNoteBlockManifest(raw); err != nil {
		return nil, err
	} else if !found {
		return nil, fmt.Errorf("ns-* 缺 note block manifest")
	}
	replacement, err := RenderNoteBlockManifest(blocks, omissions)
	if err != nil {
		return nil, err
	}
	start, end := -1, -1
	for at := 0; at < len(raw); {
		endLine := lineEnd(raw, at)
		trimmed := bytes.TrimSpace(raw[at:endLine])
		if bytes.HasPrefix(trimmed, []byte("<!-- "+noteBlockManifestFamily)) {
			if start >= 0 {
				return nil, fmt.Errorf("ns-* 含多个 note block manifest")
			}
			start, end = at, endLine
		}
		at = endLine
	}
	if start < 0 {
		return nil, fmt.Errorf("ns-* 缺 note block manifest")
	}
	out := make([]byte, 0, len(raw)-(end-start)+len(replacement))
	out = append(out, raw[:start]...)
	out = append(out, replacement...)
	out = append(out, raw[end:]...)
	if _, found, err := ParseNoteBlockManifest(out); err != nil || !found {
		if err != nil {
			return nil, fmt.Errorf("替换后的 note block manifest 不成立：%w", err)
		}
		return nil, fmt.Errorf("替换后的 note block manifest 缺失")
	}
	return out, nil
}

// NoteBlockManifestVocabulary returns the exact B1..Bn set stored in ns-*.
func NoteBlockManifestVocabulary(raw []byte) (map[string]bool, bool, error) {
	manifest, found, err := ParseNoteBlockManifest(raw)
	if err != nil || !found {
		return nil, found, err
	}
	refs := make(map[string]bool, len(manifest.Blocks))
	for _, block := range manifest.Blocks {
		refs[block.Ref] = true
	}
	return refs, true, nil
}

// ContainsReviewAnchors reports whether a Note still uses the legacy eg:nr
// block-boundary protocol.
func ContainsReviewAnchors(raw []byte) bool {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if isReviewAnchorLine(line) {
			return true
		}
	}
	return false
}

// ContainsEvergreenMachineAnchors reports any Evergreen protocol comment.
func ContainsEvergreenMachineAnchors(raw []byte) bool {
	return bytes.Contains(raw, []byte("<!-- eg:"))
}
