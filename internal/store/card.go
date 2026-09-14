package store

// `create_card` 与 `append_card` 的落盘（技术方案 §4.1 / §4.2；T-…-013）。
//
// 口径：
//   - 新建卡**五个分区都可写**，「知识内容」必写（卡片没有知识内容就不成立）；
//     frontmatter 恰 `id` / `status: active` / `created_at`（YYYY-MM-DD）/ `updated_at`（RFC3339）
//     / `sources[]`（+ 可选 `title` / `tags`）；顶层 `domain` / `type` 永不写进 frontmatter（黑名单）。
//   - `sources[]` 是新建卡的硬前提（EG-SRC-04，V3/V7）：缺或空 → 拒绝建卡、零写入。
//     该拦截由 plan 校验器与本 writer **双侧**保证，防止绕过校验器直调。
//   - 对**已有卡**只允许追加「解释与依据」「条件与边界」「理解自检」三分区；
//     「知识内容」对自动路径只读（EG-CVG-03）、「用户补充」永不写（B2）——由 writableSection 兜底。
//   - 「理解自检」历史块**只追加、永不改写**（EG-CHK-06）：问题文本原样保留、不折叠为引用；
//     当前有效问题块的替换（replace_block）属 S2，本包不提供。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

var (
	// ErrCardRelRequired 缺目标相对路径：路径由上层按领域算好后传入。
	ErrCardRelRequired = errors.New("缺知识卡目标相对路径")
	// ErrNoMaterialRef 新建卡没有材料关系：每次知识加工必须有可回读文章作依据，
	// 新建卡必须建立材料关系（EG-SRC-04）。
	ErrNoMaterialRef = errors.New("新建知识卡必须带 sources[]：每次知识加工必须有可回读文章作依据，新建卡必须建立材料关系")
)

// CardSpec 是一次 create_card 的落盘输入。
type CardSpec struct {
	Rel      string
	ID       model.CardID
	Title    string
	Date     model.Date
	Stamp    model.Stamp
	Tags     []string
	Sources  []model.SourceRef // 四要素材料关系，必须非空
	Sections []SectionAppend
}

// CardAppendSpec 是一次 append_card 的落盘输入（只追加三个分区）。
type CardAppendSpec struct {
	Rel          string
	ExpectedHash string
	Stamp        model.Stamp
	Sections     []SectionAppend
}

// ApplyCard 新建一张知识卡。
func (s *Store) ApplyCard(spec CardSpec) (Result, error) {
	if spec.Rel == "" {
		return Result{}, ErrCardRelRequired
	}
	if len(spec.Sources) == 0 {
		return Result{Path: spec.Rel}, ErrNoMaterialRef
	}
	content, warnings, err := cardContent(spec)
	if err != nil {
		return Result{Path: spec.Rel}, err
	}
	res, err := s.CreateFile(spec.Rel, mdfile.KindCard, content)
	res.Warnings = append(res.Warnings, warnings...)
	return res, err
}

// ApplyCardAppend 向已有知识卡追加分区内容（分区白名单由 WriteGuarded 兜底）。
func (s *Store) ApplyCardAppend(spec CardAppendSpec) (Result, error) {
	if spec.Rel == "" {
		return Result{}, ErrCardRelRequired
	}
	return s.sectionEdit(spec.Rel, spec.ExpectedHash, mdfile.KindCard, spec.Stamp, spec.Sections)
}

// cardContent 拼装新建卡的完整字节，并返回四要素不全一类的 warning（照写、进报告）。
func cardContent(spec CardSpec) ([]byte, []string, error) {
	sections, err := sectionMap(mdfile.KindCard, spec.Sections)
	if err != nil {
		return nil, nil, err
	}
	var fm []byte
	for _, kv := range [][2]string{
		{"id", string(spec.ID)},
		{"title", spec.Title},
		{"status", string(model.StatusActive)},
		{"created_at", spec.Date.String()},
		{"updated_at", spec.Stamp.String()},
	} {
		if kv[1] == "" {
			continue
		}
		line, err := fmLine(kv[0], kv[1])
		if err != nil {
			return nil, nil, err
		}
		fm = append(fm, line...)
	}
	block, warnings, err := sourcesBlock(spec.Sources)
	if err != nil {
		return nil, nil, err
	}
	fm = append(fm, block...)
	tags, err := fmSeq("tags", spec.Tags)
	if err != nil {
		return nil, nil, err
	}
	fm = append(fm, tags...)

	content, err := document(mdfile.KindCard, fm, sections)
	if err != nil {
		return nil, nil, err
	}
	return content, warnings, nil
}

// sourcesBlock 拼 `sources:` 块状序列：每条一项，四要素各一行（source / note / rel / reason）。
// 四要素不全只出 warning（W2 口径：照写 + 进报告，S1 不拦截）。
func sourcesBlock(refs []model.SourceRef) ([]byte, []string, error) {
	var out []byte
	var warnings []string
	out = append(out, "sources:\n"...)
	for i, ref := range refs {
		if !ref.Complete() {
			warnings = append(warnings, fmt.Sprintf("sources[%d] 四要素不全：缺 %v（照写并进报告）",
				i, ref.MissingFields()))
		}
		for j, kv := range [][2]string{
			{"source", string(ref.Source)},
			{"note", string(ref.Note)},
			{"rel", string(ref.Rel)},
			{"reason", ref.Reason},
		} {
			value, err := quoted(kv[1])
			if err != nil {
				return nil, nil, fmt.Errorf("sources[%d].%s：%w", i, kv[0], err)
			}
			out = append(out, seqIndent...)
			if j == 0 {
				out = append(out, '-', ' ')
			} else {
				out = append(out, seqIndent...)
			}
			out = append(out, kv[0]...)
			out = append(out, ':', ' ')
			out = append(out, value...)
			out = append(out, '\n')
		}
	}
	return out, warnings, nil
}
