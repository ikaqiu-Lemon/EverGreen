package mdfile

// frontmatter 的**只读**解析（施工索引 §16.2 的「读」侧）。
//
// yaml.v3 只用于 Unmarshal：取字段值、判 E4（frontmatter YAML 不可解析 → error）。
// 序列化类 API（Marshal / Encoder 系列）在本包**任何路径都不出现**——
// 键顺序、引号风格、折叠 / 字面标量、锚点别名因不经过序列化器而天然逐字不变。

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrFrontmatterYAML 对应校验分级 E4：frontmatter YAML 不可解析。
var ErrFrontmatterYAML = errors.New("E4 frontmatter YAML 不可解析")

// DecodeFM 把 frontmatter 反序列化进 out（只读）。无 frontmatter 时按空映射处理。
func (d *Doc) DecodeFM(out interface{}) error {
	if !d.HasFM {
		return nil
	}
	if err := yaml.Unmarshal(d.FMBytes(), out); err != nil {
		return fmt.Errorf("%w：%v", ErrFrontmatterYAML, err)
	}
	return nil
}

// FMKeys 返回 frontmatter 的顶层键（**出现顺序**，只读）。
func (d *Doc) FMKeys() ([]string, error) {
	if !d.HasFM {
		return nil, nil
	}
	var node yaml.Node
	if err := yaml.Unmarshal(d.FMBytes(), &node); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrFrontmatterYAML, err)
	}
	if len(node.Content) == 0 || node.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	m := node.Content[0]
	out := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out, nil
}

// HasFMKey 报告 frontmatter 是否已有该顶层键。
func (d *Doc) HasFMKey(key string) (bool, error) {
	keys, err := d.FMKeys()
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k == key {
			return true, nil
		}
	}
	return false, nil
}

// ParseCard 解析知识卡：索引 + frontmatter 字段 + 分区结构校验。
func ParseCard(raw []byte) (*Doc, model.Card, error) {
	var card model.Card
	d, err := Parse(raw)
	if err != nil {
		return nil, card, err
	}
	if err := d.DecodeFM(&card); err != nil {
		return nil, card, err
	}
	if err := d.ValidateSections(KindCard); err != nil {
		return nil, card, err
	}
	return d, card, nil
}

// ParseNote 解析材料笔记。
//
// 材料笔记**没有** status：model.Note 无该字段，frontmatter 里若出现 status
// 会落进 Note.Extra **原样保留**，不参与任何判定，也绝不由本包生成。
// 材料笔记同样**不产出「理解自检」分区**（该分区是知识卡独有）。
func ParseNote(raw []byte) (*Doc, model.Note, error) {
	var note model.Note
	d, err := Parse(raw)
	if err != nil {
		return nil, note, err
	}
	if err := d.DecodeFM(&note); err != nil {
		return nil, note, err
	}
	if err := d.ValidateSections(KindNote); err != nil {
		return nil, note, err
	}
	return d, note, nil
}

// ParseSource 解析原文：frontmatter（id/url/title/saved_at）+ 正文全文。
// 正文收录后**不因任何加工改写**，因此原文没有固定分区约束。
func ParseSource(raw []byte) (*Doc, model.Source, error) {
	var src model.Source
	d, err := Parse(raw)
	if err != nil {
		return nil, src, err
	}
	if err := d.DecodeFM(&src); err != nil {
		return nil, src, err
	}
	return d, src, nil
}
