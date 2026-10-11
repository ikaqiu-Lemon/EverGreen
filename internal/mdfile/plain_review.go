package mdfile

import (
	"bytes"
	"crypto/sha256"
	"fmt"
)

// ParsePlainReviewNote 只接受与 ns manifest 的内容 hash 逐项匹配的正文，绝不猜测 stale 边界。
func ParsePlainReviewNote(body []byte, manifest NoteBlockManifest) (ReviewNote, error) {
	lines := bytes.Split(bytes.Trim(body, "\n"), []byte("\n"))
	at := 0
	result := ReviewNote{Omissions: manifest.Omissions}
	for _, entry := range manifest.Blocks {
		for at < len(lines) && len(lines[at]) == 0 {
			at++
		}
		start := at
		var found *ReviewBlock
		for end := start + 1; end <= len(lines); end++ {
			anchor := reviewAnchor{
				Heading: entry.Heading, SourceRef: entry.SourceRef,
				Annotation: entry.Annotation, Label: entry.Label,
			}
			var block ReviewBlock
			var err error
			if entry.Role == ReviewRoleSource {
				block, err = recoverSourceBlock(anchor, lines[start:end])
			} else {
				block, err = recoverAgentBlock(anchor, lines[start:end])
			}
			if err != nil {
				continue
			}
			sum := sha256.Sum256(bytes.Trim(block.Body, "\n"))
			if fmt.Sprintf("sha256:%x", sum) == entry.ContentHash {
				found = &block
				at = end
				break
			}
		}
		if found == nil {
			return ReviewNote{}, fmt.Errorf("EG_MIGRATION_SEGMENT_HASH: %s cannot be matched to its captured content", entry.Ref)
		}
		result.Blocks = append(result.Blocks, *found)
	}
	plain, err := RenderPlainReviewNote(result.Blocks, result.Omissions)
	if err != nil {
		return ReviewNote{}, err
	}
	if !bytes.Equal(bytes.Trim(plain, "\n"), bytes.Trim(body, "\n")) {
		return ReviewNote{}, fmt.Errorf("EG_MIGRATION_SEGMENT_HASH: unmatched Note content remains")
	}
	return result, nil
}
