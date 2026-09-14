package mdfile

// 收件区条目的**区间剪除**（技术方案 §4.4 / EG-SRC-02：write_note 成功即移出条目）。
//
// 本包的知识产物写路径只有「追加」一种形态（append.go）。收件区 `unprocessed.md`
// **不是知识产物**：它是待加工队列，条目移出是队列语义而非知识内容删除，
// 因此这里给出唯一一处区间剪除，并把它**限定在收件区条目**上：
//   - 只按 `source_id` 定位**一个**顶层列表项，只剪除该条目自身的字节区间；
//   - 条目之外的一切字节（标题、说明段落、其它条目、空行、未知字段）逐字不动；
//   - 剪除结果立刻做 Parse→Render 字节自检 + 条目数校验，不通过即拒绝返回。
//
// 知识卡 / 材料笔记**没有**对应能力：分区与块的替换、删除属 S2 起的用户显式命令。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrFMSeqBlockExists frontmatter 已有该键：块状序列只在键**缺失**时创建。
var ErrFMSeqBlockExists = errors.New("frontmatter 已存在该键（块状序列只在键缺失时创建）")

// ErrEntryNotFound 收件区没有该 source_id 的条目。
var ErrEntryNotFound = errors.New("收件区无该 source_id 条目")

// ErrCutUnsafe 剪除结果未通过自检（字节区间归属不可确定），拒绝返回。
var ErrCutUnsafe = errors.New("收件区条目剪除结果未通过自检，拒绝写入")

// Cut 返回「移出 id 条目后」的新字节切片：`Raw[:start]` + `Raw[end:]`，
// 不改写保留部分的任何一个字节。条目不存在时返回 ErrEntryNotFound
// （上层据此判「已移出」的幂等情形）。
func (u *Unprocessed) Cut(id model.SourceID) ([]byte, error) {
	e, ok := u.Find(id)
	if !ok {
		return nil, fmt.Errorf("%w：%s", ErrEntryNotFound, id)
	}
	raw := u.Doc.Raw
	out := make([]byte, 0, len(raw)-(e.End-e.Start))
	out = append(out, raw[:e.Start]...)
	out = append(out, raw[e.End:]...)

	if err := SelfCheck(out); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrCutUnsafe, err)
	}
	after, err := ParseUnprocessed(out)
	if err != nil {
		return nil, fmt.Errorf("%w：%v", ErrCutUnsafe, err)
	}
	if len(after.Entries) != len(u.Entries)-1 {
		return nil, fmt.Errorf("%w：条目数 %d → %d（期望减一）",
			ErrCutUnsafe, len(u.Entries), len(after.Entries))
	}
	if _, still := after.Find(id); still {
		return nil, fmt.Errorf("%w：%s 仍在收件区", ErrCutUnsafe, id)
	}
	return out, nil
}

// AppendFMSeqBlock 在 frontmatter **末尾**新建一个块状序列：`key:` 一行 + items 逐字插入。
// 只在键缺失时可用（已存在同名键 → ErrFMSeqBlockExists；已存在序列请用 AppendFMSeqItem，
// 沿用既有缩进）。items 由调用方自带缩进与 `- `，本包不改写其一个字节。
//
// 机制仍然只有一条：区间拼接 + 在 FMEnd 处插入。
func (d *Doc) AppendFMSeqBlock(key string, items []byte) ([]byte, error) {
	if !d.HasFM {
		return nil, ErrNoFrontmatter
	}
	exists, err := d.HasFMKey(key)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("%w：%s", ErrFMSeqBlockExists, key)
	}
	if len(items) == 0 || items[len(items)-1] != '\n' {
		return nil, ErrPayloadNotLineTerminated
	}
	payload := make([]byte, 0, len(key)+2+len(items))
	payload = append(payload, key...)
	payload = append(payload, ':', '\n')
	payload = append(payload, items...)
	return insert(d.Raw, d.FMEnd, payload), nil
}
