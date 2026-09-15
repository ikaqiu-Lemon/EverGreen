package store

// `create_opinion` 与 `append_opinion` 的落盘（Schema v2 契约 §3.1 / §3.4 / §4.5）。
//
// # 为什么不复用 card.go
//
// 三处字节级差异，任何一处都不能靠参数开关消化：
//
//	落位目录     domains/<domain>/opinions/  ≠  .../cards/（OpinionRel vs CardRel）
//	分区模板     观点五分区                    ≠  知识卡三分区（KnownSections 按 Kind 分流）
//	frontmatter  多一个 `validation`           ≠  知识卡没有这个键
//
// 把它们合成一个带 `kind` 参数的 writer，等于让「知识卡的字节形态」与「观点的字节形态」
// 共用一条拼装路径。第一次给知识卡加 frontmatter 键的人就会顺手写进 observation 的字节里。
//
// # 与 card.go **确实**共用的部分
//
// `sectionMap` / `document` / `fmLine` / `fmSeq` / `sourcesBlock` / `sectionEdit`
// 全部复用既有实现（content.go / note.go），一行未抄。模板拼装、frontmatter 行渲染、
// 追加写的守卫路径在两类实体上本就是同一套机制。
//
// # `validation` 恒为 pending
//
// 本 writer **只写** `validation: pending`，不接受调用方指定的值（契约 §4.5 / §6.3）：
// 验证状态只能由用户显式路径流转，新建路径不是那条路径。这不是「校验器已经拦过一次、
// 这里再拦一次」的冗余——plan 校验器可以被绕过（直调 store），而「创建即已验证的观点」
// 一旦落盘就是一个既没有论据也没有用户确认的结论，此后无法与真正被确认过的观点区分。

import (
	"errors"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

var (
	// ErrOpinionRelRequired 缺目标相对路径：路径由上层按领域算好后传入。
	ErrOpinionRelRequired = errors.New("缺观点目标相对路径")
	// ErrOpinionNoMaterialRef 新建观点没有材料关系。
	//
	// 与 ErrNoMaterialRef（知识卡）分成两个哨兵：两者的**理由不同**。知识卡是
	// EG-SRC-04「每次知识加工必须有可回读文章作依据」；观点更强——一个没有材料出处的
	// 判断连反驳都无处着手，`validation` 也就永远无法从 pending 流转出去。
	// 合成一个哨兵会让调用方无法按类型给出各自的修复建议。
	ErrOpinionNoMaterialRef = errors.New(
		"新建观点必须带 sources[]：观点也要有可回读文章作依据，否则它既无法被验证也无法被反驳")
)

// OpinionSpec 是一次 create_opinion 的落盘输入。
//
// **没有** Validation 字段：新建观点恒为 `pending`（见文件头）。要让一个字段
// 存在却只接受一个取值，等于给绕过留一个看起来合法的入口。
type OpinionSpec struct {
	Rel      string
	ID       model.OpinionID
	Title    string
	Date     model.Date
	Stamp    model.Stamp
	Tags     []string
	Sources  []model.SourceRef // 四要素材料关系，必须非空
	Sections []SectionAppend
}

// OpinionAppendSpec 是一次 append_opinion 的落盘输入（只追加三个分区）。
//
// 分区白名单由 WriteGuarded 按 `AutoWritableSections(KindOpinion)` 兜底：
// 「观点」对已有观点只读（矩阵 #46 的 🔴 子情形）、「用户补充」永不写（B2）。
type OpinionAppendSpec struct {
	Rel          string
	ExpectedHash string
	Stamp        model.Stamp
	Sections     []SectionAppend
}

// ApplyOpinion 新建一条观点。
func (s *Store) ApplyOpinion(spec OpinionSpec) (Result, error) {
	if spec.Rel == "" {
		return Result{}, ErrOpinionRelRequired
	}
	if len(spec.Sources) == 0 {
		return Result{Path: spec.Rel}, ErrOpinionNoMaterialRef
	}
	content, warnings, err := opinionContent(spec)
	if err != nil {
		return Result{Path: spec.Rel}, err
	}
	res, err := s.CreateFile(spec.Rel, mdfile.KindOpinion, content)
	res.Warnings = append(res.Warnings, warnings...)
	return res, err
}

// ApplyOpinionAppend 向已有观点追加分区内容（分区白名单由 WriteGuarded 兜底）。
func (s *Store) ApplyOpinionAppend(spec OpinionAppendSpec) (Result, error) {
	if spec.Rel == "" {
		return Result{}, ErrOpinionRelRequired
	}
	return s.sectionEdit(spec.Rel, spec.ExpectedHash, mdfile.KindOpinion, spec.Stamp, spec.Sections)
}

// opinionContent 拼装新建观点的完整字节，并返回四要素不全一类的 warning（照写、进报告）。
//
// frontmatter 键序：`id` / `title` / `status` / `validation` / `created_at` /
// `updated_at` / `sources[]` / `tags[]`。
// `validation` 紧跟 `status`——两者是**正交**的两个维度（§3.4：rejected 的观点仍可为
// active，「已确认不成立」本身是资产），排在一起才能让读文件的人一眼看到两个维度而不是
// 误以为后者是前者的细化。
func opinionContent(spec OpinionSpec) ([]byte, []string, error) {
	sections, err := sectionMap(mdfile.KindOpinion, spec.Sections)
	if err != nil {
		return nil, nil, err
	}
	var fm []byte
	for _, kv := range [][2]string{
		{"id", string(spec.ID)},
		{"title", spec.Title},
		{"status", string(model.StatusActive)},
		{model.FMKeyValidation, string(model.ValidationPending)},
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

	content, err := document(mdfile.KindOpinion, fm, sections)
	if err != nil {
		return nil, nil, err
	}
	return content, warnings, nil
}
