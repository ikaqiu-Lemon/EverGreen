package store

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrNoteSegmentationRelRequired 表示调用方没有给出 ns-* 的目标路径。
var ErrNoteSegmentationRelRequired = errors.New("缺划分工作区目标相对路径")

// NoteSegmentationSpec 是新建 ns-* 工作区的完整落盘输入。
type NoteSegmentationSpec struct {
	Rel      string
	ID       model.NoteSegmentationID
	Note     model.NoteID
	NoteHash string
	Title    string
	Date     model.Date
	Stamp    model.Stamp
	Tags     []string
	Sections []SectionAppend
}

// ApplyNoteSegmentation 新建一份划分工作区。既有文件不覆盖。
func (s *Store) ApplyNoteSegmentation(spec NoteSegmentationSpec) (Result, error) {
	if spec.Rel == "" {
		return Result{}, ErrNoteSegmentationRelRequired
	}
	content, err := NoteSegmentationBytes(spec)
	if err != nil {
		return Result{Path: spec.Rel}, err
	}
	return s.CreateFile(spec.Rel, mdfile.KindNoteSegmentation, content)
}

// NoteSegmentationBytes builds canonical ns-* bytes without touching disk.
func NoteSegmentationBytes(spec NoteSegmentationSpec) ([]byte, error) {
	sections, err := sectionMap(mdfile.KindNoteSegmentation, spec.Sections)
	if err != nil {
		return nil, err
	}
	var fm []byte
	for _, kv := range [][2]string{
		{"id", string(spec.ID)},
		{"note", string(spec.Note)},
		{"note_hash", spec.NoteHash},
		{"title", spec.Title},
		{"created_at", spec.Date.String()},
		{"updated_at", spec.Stamp.String()},
	} {
		if kv[1] == "" {
			continue
		}
		line, err := fmLine(kv[0], kv[1])
		if err != nil {
			return nil, err
		}
		fm = append(fm, line...)
	}
	tags, err := fmSeq("tags", spec.Tags)
	if err != nil {
		return nil, err
	}
	fm = append(fm, tags...)
	content, err := document(mdfile.KindNoteSegmentation, fm, sections)
	if err != nil {
		return nil, err
	}
	if _, _, err := mdfile.ParseNoteSegmentation(content); err != nil {
		return nil, err
	}
	return content, nil
}

// NoteBlockVocabulary returns the complete version-local B1..Bn vocabulary
// for all source and agent blocks in the Note's 整理正文.
func NoteBlockVocabulary(raw []byte) (map[string]bool, error) {
	doc, _, err := mdfile.ParseNote(raw)
	if err != nil {
		return nil, err
	}
	span, ok := doc.Section(mdfile.SecNoteBody)
	if !ok {
		return nil, fmt.Errorf("Note 缺分区「%s」", mdfile.SecNoteBody)
	}
	review, err := mdfile.ParseReviewNote(raw[span.Body:span.End])
	if err != nil {
		return nil, fmt.Errorf("解析 Note blocks：%w", err)
	}
	refs := make(map[string]bool, len(review.Blocks))
	for i := range review.Blocks {
		refs[fmt.Sprintf("B%d", i+1)] = true
	}
	return refs, nil
}

// NoteSegmentationRebasedBytes is the low-level scalar update used only after
// callers have validated or rebuilt the workspace against the target Note.
// It advances note_hash and updated_at; candidate state and 用户补充 stay
// byte-identical.
func NoteSegmentationRebasedBytes(
	raw []byte,
	noteHash string,
	stamp model.Stamp,
) ([]byte, error) {
	doc, _, err := mdfile.ParseNoteSegmentation(raw)
	if err != nil {
		return nil, err
	}
	hashLine, err := fmLine("note_hash", noteHash)
	if err != nil {
		return nil, err
	}
	out, err := setFMScalarKey(doc, "note_hash", hashLine, true)
	if err != nil {
		return nil, err
	}
	out, err = refreshUpdatedAt(out, stamp)
	if err != nil {
		return nil, err
	}
	if _, _, err := mdfile.ParseNoteSegmentation(out); err != nil {
		return nil, err
	}
	return out, nil
}
