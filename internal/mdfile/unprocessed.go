package mdfile

// 收件区 `unprocessed.md`（技术方案 §4.4）：**一个顶层列表项 = 一个条目**，键为 `source_id`。
//
// 解析同样是只读的：条目字节区间原样保留，追加走字节插入。

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrDuplicateEntry 收件区出现同一 source_id 的第二条条目。
var ErrDuplicateEntry = errors.New("收件区条目重复（键为 source_id）")

// Entry 是收件区的一个条目：条目字节区间 + 只读解析出的字段。
type Entry struct {
	Item  model.UnprocessedItem
	Start int
	End   int
}

// Unprocessed 是 `unprocessed.md` 的只读索引。
type Unprocessed struct {
	Doc     *Doc
	Entries []Entry
}

// ParseUnprocessed 解析收件区文件。顶层列表项之外的内容（标题、说明段落、空行）
// 原样保留在区间之外，Render 逐字还原。
func ParseUnprocessed(raw []byte) (*Unprocessed, error) {
	d, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	u := &Unprocessed{Doc: d}
	for _, b := range SplitBlocks(raw, d.BodyFrom, len(raw)) {
		if b.Kind != BlockListItem {
			continue
		}
		var items []model.UnprocessedItem
		if err := yaml.Unmarshal(b.Bytes(raw), &items); err != nil {
			return nil, fmt.Errorf("%w：收件区条目（偏移 %d，第 %d 行）：%v",
				ErrFrontmatterYAML, b.Start, lineNumber(raw, b.Start), err)
		}
		if len(items) == 0 {
			continue
		}
		u.Entries = append(u.Entries, Entry{Item: items[0], Start: b.Start, End: b.End})
	}
	seen := map[model.SourceID]bool{}
	for _, e := range u.Entries {
		if e.Item.SourceID == "" {
			continue
		}
		if seen[e.Item.SourceID] {
			return nil, fmt.Errorf("%w：%s", ErrDuplicateEntry, e.Item.SourceID)
		}
		seen[e.Item.SourceID] = true
	}
	return u, nil
}

// Render 区间拼接还原字节（无逻辑变更时逐字等于输入）。
func (u *Unprocessed) Render() []byte { return u.Doc.Render() }

// Find 按 source_id 取条目（键为 source_id，判重与幂等都据此）。
func (u *Unprocessed) Find(id model.SourceID) (Entry, bool) {
	for _, e := range u.Entries {
		if e.Item.SourceID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Append 在最后一个条目之后追加一条（无条目时追加到文件末尾的最后一个非空行之后）。
// item 逐字插入，必须以换行结束。
func (u *Unprocessed) Append(item []byte) ([]byte, error) {
	if len(item) == 0 || item[len(item)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	raw := u.Doc.Raw
	at := u.Doc.BodyFrom
	if n := len(u.Entries); n > 0 {
		at = u.Entries[n-1].End
	} else {
		for cur := u.Doc.BodyFrom; cur < len(raw); {
			end := lineEnd(raw, cur)
			if !isBlank(raw[cur:end]) {
				at = end
			}
			cur = end
		}
	}
	return insert(raw, at, item), nil
}
