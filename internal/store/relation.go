package store

// `add_material_rel` 与 `add_relation` 的落盘（技术方案 §4.1、§6.1、§6.2；T-…-014）。
//
// 关系是 Evergreen 的价值核心，写错方向或写混 ID 类型会让整库不可信，因此这里的**硬拦**
// 比功能本身更重要，且一律是**代码级**约束，不依赖上层自律：
//   - 封闭枚举（冻结合同 F4）：材料 `rel` 恰 support / against / context 三值，
//     论证 `type` 恰 derives / supports / limits / opposing 四值；集合外取值一律拒绝，
//     错误信息逐字列出该组合法取值。材料 `support` 与论证 `supports` 只差一个字母。
//   - E3 双向硬拦：`s-` 出现在 relations[].target → 拒；`k-` 出现在 sources[].source → 拒。
//   - E2：target 必须能在全库解析到文件（ID 不存在 / 格式非法 → 拒，零写入）。
//   - F2：关系只引用 ID，永不引用路径，文件移动 / 改名不影响关系有效性（路径由 id 扫描得到）。
//   - `opposing` 单向存储：必须写在**字典序较小**的一端；方向规范化与同对去重的唯一实现在
//     internal/rules（§13 依赖方向不允许 store 依赖 rules），本包只做「已规范化」的硬校验，
//     并对同对重复做幂等（不产生第二条、不生成议题组、不补全关系图）。
//
// 写机制：只对 frontmatter 序列做**字节区间插入**——既有序列沿用其首个 `- ` 的缩进，
// 键缺失时在 frontmatter 末尾新建块状序列；序列存在但为空（无缩进风格可循）时**不猜**，
// 出 warning 进报告、本条不写。既有条目永不改写、永不重排。

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

var (
	// ErrRelationTargetType 关系两端的 ID 类型写混（E3）。
	ErrRelationTargetType = errors.New("关系 ID 类型写混")
	// ErrRelationTargetUnresolved 关系 target 无法在全库解析（E2：悬空引用）。
	ErrRelationTargetUnresolved = errors.New("关系 target 无法解析")
	// ErrOpposingNotNormalized opposing 未写在字典序较小的一端。
	ErrOpposingNotNormalized = errors.New("opposing 必须写在两端 ID 字典序较小的一端（规范化见 internal/rules）")
	// ErrSelfRelation 自反关系。
	ErrSelfRelation = errors.New("关系两端不得是同一张卡")
)

// MaterialRelSpec 是一次 add_material_rel 的落盘输入：写入知识卡 frontmatter 的 sources[]。
type MaterialRelSpec struct {
	Rel          string // 目标卡相对路径（由 id 解析得到，关系本身只存 ID）
	ExpectedHash string
	Stamp        model.Stamp
	Ref          model.SourceRef
}

// RelationSpec 是一次 add_relation 的落盘输入：写入**宿主实体**（From 端）frontmatter 的 relations[]。
//
// From 是跨类型端点（RelationEndpoint，前缀 k- / o-）：论证关系可发生在知识卡或观点之间，
// 宿主既可能是知识卡也可能是观点，落盘按端点前缀分流（k- 读写知识卡、o- 读写观点）。
type RelationSpec struct {
	Rel          string // 写入端（From 实体）的相对路径
	ExpectedHash string
	Stamp        model.Stamp
	From         model.RelationEndpoint
	Relation     model.Relation
	Index        *Index // id → path（用于 E2 解析；nil → 现场全库扫描）
}

// ApplyMaterialRel 向知识卡 sources[] 追加一条四要素材料关系。
func (s *Store) ApplyMaterialRel(spec MaterialRelSpec) (Result, error) {
	res := Result{Path: spec.Rel}
	if spec.Rel == "" {
		return res, ErrCardRelRequired
	}
	ref := spec.Ref
	if bytes.HasPrefix([]byte(ref.Source), []byte(model.PrefixCard)) {
		return res, fmt.Errorf("%w：sources[].source 只接受 %s 前缀的原文 ID，得到 %q（E3）",
			ErrRelationTargetType, model.PrefixSource, ref.Source)
	}
	if !bytes.HasPrefix([]byte(ref.Source), []byte(model.PrefixSource)) {
		return res, fmt.Errorf("%w：sources[].source 必须是 %s 前缀的原文 ID，得到 %q",
			ErrRelationTargetType, model.PrefixSource, ref.Source)
	}
	if ref.Note != "" && !bytes.HasPrefix([]byte(ref.Note), []byte(model.PrefixNote)) {
		return res, fmt.Errorf("%w：sources[].note 必须是 %s 前缀的笔记 ID，得到 %q",
			ErrRelationTargetType, model.PrefixNote, ref.Note)
	}
	if _, err := model.ParseMaterialRel(string(ref.Rel)); err != nil {
		return res, fmt.Errorf("材料关系取值封闭（冻结合同 F4，合法取值恰 %v）：%w",
			model.ValidMaterialRels(), err)
	}

	f, card, doc, err := s.readCard(spec.Rel)
	if err != nil {
		return res, err
	}
	res.Hash = f.Hash
	for _, exist := range card.Sources {
		if exist == ref {
			res.Detail = fmt.Sprintf("sources[] 已有完全相同的条目（%s / %s）：幂等去重，不追加第二条",
				ref.Source, ref.Rel)
			return res, nil
		}
	}
	item, warnings, err := materialItem(ref)
	if err != nil {
		return res, err
	}
	return s.seqEdit(spec.Rel, spec.ExpectedHash, f, doc, mdfile.KindCard, "sources", item, spec.Stamp, warnings)
}

// ApplyRelation 向知识卡 relations[] 追加一条论证关系。
func (s *Store) ApplyRelation(spec RelationSpec) (Result, error) {
	res := Result{Path: spec.Rel}
	rel := spec.Relation
	// from 端也是论证关系端点：与 target 同口径，只接受 k- / o-（E3 硬拦，**先于**任何
	// 读盘/写盘/解析——非法或其它 kind 的 from 一律零副作用拒绝）。
	if err := relationEndpointOnly("relations[].from", spec.From); err != nil {
		return res, err
	}
	if _, err := model.ParseRelationType(string(rel.Type)); err != nil {
		return res, fmt.Errorf("论证关系取值封闭（冻结合同 F4，合法取值恰 %v）：%w",
			model.ValidRelationTypes(), err)
	}
	if bytes.HasPrefix([]byte(rel.Target), []byte(model.PrefixSource)) {
		return res, fmt.Errorf("%w：relations[].target 只接受 %s / %s 前缀的端点，得到 %q（E3）",
			ErrRelationTargetType, model.PrefixCard, model.PrefixOpinion, rel.Target)
	}
	if _, err := model.ParseRelationEndpoint(string(rel.Target)); err != nil {
		return res, fmt.Errorf("%w：relations[].target 必须是 %s / %s 前缀的端点，得到 %q（%v）",
			ErrRelationTargetType, model.PrefixCard, model.PrefixOpinion, rel.Target, err)
	}
	if string(rel.Target) == string(spec.From) {
		return res, fmt.Errorf("%w：%s", ErrSelfRelation, rel.Target)
	}
	if rel.Type == model.RelationOpposing && string(spec.From) > string(rel.Target) {
		return res, fmt.Errorf("%w：应写在 %s 一端，实得 %s", ErrOpposingNotNormalized,
			rel.Target, spec.From)
	}
	// E2：target 必须能解析到具体文件（关系只存 ID，路径由扫描得到——F2）。
	idx := spec.Index
	if idx == nil {
		scanned, err := s.ScanIDs()
		if err != nil {
			return res, err
		}
		idx = &scanned
	}
	if _, err := idx.Resolve(string(rel.Target)); err != nil {
		return res, fmt.Errorf("%w：%s（%v）", ErrRelationTargetUnresolved, rel.Target, err)
	}
	if spec.Rel == "" {
		path, err := idx.Resolve(string(spec.From))
		if err != nil {
			return res, fmt.Errorf("%w：%s（%v）", ErrRelationTargetUnresolved, spec.From, err)
		}
		spec.Rel = path
		res.Path = path
	}

	f, hostRelations, doc, kind, err := s.readRelationHost(spec.From, spec.Rel)
	if err != nil {
		return res, err
	}
	res.Hash = f.Hash
	for _, exist := range hostRelations {
		if exist == rel {
			res.Detail = fmt.Sprintf("relations[] 已有完全相同的关系（%s → %s）：幂等去重，不追加第二条",
				rel.Type, rel.Target)
			return res, nil
		}
		// 同对 opposing 已存在：一对一条记录，不追加第二条（reason 的更新属 S2 的键改写）。
		if rel.Type == model.RelationOpposing && exist.Type == model.RelationOpposing &&
			exist.Target == rel.Target {
			res.Detail = fmt.Sprintf("同对 opposing 已存在（%s ↔ %s）：一对一条记录，不追加第二条",
				spec.From, rel.Target)
			res.Warnings = append(res.Warnings,
				fmt.Sprintf("opposing 同对重复（%s ↔ %s）：已去重，不产生第二条记录", spec.From, rel.Target))
			return res, nil
		}
	}
	item, warnings, err := relationItem(rel)
	if err != nil {
		return res, err
	}
	return s.seqEdit(spec.Rel, spec.ExpectedHash, f, doc, kind, "relations", item, spec.Stamp, warnings)
}

// readRelationHost 读一个论证关系宿主（知识卡或观点，按端点前缀分流）：
// 原始字节 + 只读解析出的 relations[] + 文档索引 + 宿主类型（供守卫写沿用固定分区口径）。
func (s *Store) readRelationHost(from model.RelationEndpoint, rel string) (File, []model.Relation, *mdfile.Doc, mdfile.Kind, error) {
	f, err := s.Read(rel)
	if err != nil {
		return File{}, nil, nil, "", err
	}
	if bytes.HasPrefix([]byte(from), []byte(model.PrefixOpinion)) {
		doc, op, err := mdfile.ParseOpinion(f.Bytes)
		if err != nil {
			return f, nil, nil, mdfile.KindOpinion, err
		}
		return f, op.Relations, doc, mdfile.KindOpinion, nil
	}
	doc, card, err := mdfile.ParseCard(f.Bytes)
	if err != nil {
		return f, nil, nil, mdfile.KindCard, err
	}
	return f, card.Relations, doc, mdfile.KindCard, nil
}

// readCard 读一张卡：原始字节 + 只读解析出的 frontmatter 字段 + 文档索引。
func (s *Store) readCard(rel string) (File, model.Card, *mdfile.Doc, error) {
	f, err := s.Read(rel)
	if err != nil {
		return File{}, model.Card{}, nil, err
	}
	doc, card, err := mdfile.ParseCard(f.Bytes)
	if err != nil {
		return f, model.Card{}, nil, err
	}
	return f, card, doc, nil
}

// seqEdit 把一条序列条目写进 frontmatter：既有序列沿用缩进追加，键缺失则新建块状序列，
// 序列为空（无缩进风格）则不猜、不写、出 warning。
//
// kind 是宿主固定分区口径（KindCard / KindOpinion）：关系与材料关系都只动 frontmatter
// 序列、不追加任何正文分区，因此 kind 不改变本函数写出的字节；透传它只为守卫写沿用
// 宿主真实类型的分区白名单（自动路径永不误写「用户补充」等分区，B2）。
func (s *Store) seqEdit(rel, expectedHash string, f File, doc *mdfile.Doc, kind mdfile.Kind, key string,
	item []string, stamp model.Stamp, warnings []string) (Result, error) {
	hash := expectedHash
	if hash == "" {
		hash = f.Hash
	}
	keys, err := updatedAtAppendIfMissing(f.Bytes, stamp)
	if err != nil {
		return Result{Path: rel}, err
	}
	// 既有 `updated_at` 由 Edit.Stamp 在同一次守卫写里整行刷新（矩阵第 8 行）：
	// 关系维度的写入同样是「实际写入」，rel add / eg apply 都必须推进内容时间戳。
	edit := Edit{Kind: kind, FMKeys: keys, Stamp: stamp}
	switch seq, err := doc.FMSeq(key); {
	case err == nil:
		edit.FMSeqItems = []FMSeqAppend{{Key: key, Item: seqItem(item, append(seq.Indent, ' ', ' '))}}
	case errors.Is(err, mdfile.ErrSeqKeyNotFound):
		edit.FMSeqNew = []FMSeqBlock{{Key: key, Items: seqBlockItem(item)}}
	case errors.Is(err, mdfile.ErrNoIndentStyle):
		res := Result{Path: rel, Hash: f.Hash}
		res.Warnings = append(warnings, fmt.Sprintf(
			"%s 是空序列，无既有缩进风格可沿用：S1 不猜缩进，本条关系未写入（进报告）", key))
		return res, nil
	default:
		return Result{Path: rel, Hash: f.Hash}, err
	}

	res, err := s.WriteGuarded(rel, hash, edit)
	res.Warnings = append(res.Warnings, warnings...)
	return res, err
}

// seqItem 把「字段行」拼成 AppendFMSeqItem 需要的条目字节：首行不带缩进与 `- `
// （由 mdfile 按既有风格补），续行自带 contIndent。
func seqItem(lines []string, contIndent []byte) []byte {
	var out []byte
	for i, line := range lines {
		if i > 0 {
			out = append(out, contIndent...)
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

// seqBlockItem 把「字段行」拼成新建块状序列的首条条目（自带 2 空格缩进 + `- `）。
func seqBlockItem(lines []string) []byte {
	var out []byte
	for i, line := range lines {
		out = append(out, seqIndent...)
		if i == 0 {
			out = append(out, '-', ' ')
		} else {
			out = append(out, seqIndent...)
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

// materialItem 拼一条 sources[] 条目的字段行，并给出 W2 口径的 warning（照写 + 进报告）。
func materialItem(ref model.SourceRef) ([]string, []string, error) {
	var warnings []string
	if !ref.Complete() {
		warnings = append(warnings, fmt.Sprintf("sources[] 四要素不全：缺 %v（照写并进报告，S1 不拦截）",
			ref.MissingFields()))
	}
	if ref.Reason == string(ref.Rel) {
		warnings = append(warnings, fmt.Sprintf("sources[].reason 等于关系名本身（%q）：等于没写理由（照写并进报告）",
			ref.Reason))
	}
	lines, err := kvLines([][2]string{
		{"source", string(ref.Source)},
		{"note", string(ref.Note)},
		{"rel", string(ref.Rel)},
		{"reason", ref.Reason},
	})
	return lines, warnings, err
}

// relationItem 拼一条 relations[] 条目的字段行。
func relationItem(rel model.Relation) ([]string, []string, error) {
	var warnings []string
	if rel.Reason == "" || rel.Reason == string(rel.Type) {
		warnings = append(warnings, fmt.Sprintf(
			"relations[].reason 缺失或等于关系名本身（%q）：等于没写理由（照写并进报告）", rel.Reason))
	}
	lines, err := kvLines([][2]string{
		{"type", string(rel.Type)},
		{"target", string(rel.Target)},
		{"reason", rel.Reason},
	})
	return lines, warnings, err
}

func kvLines(kvs [][2]string) ([]string, error) {
	out := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		value, err := quoted(kv[1])
		if err != nil {
			return nil, fmt.Errorf("%s：%w", kv[0], err)
		}
		out = append(out, kv[0]+": "+string(value))
	}
	return out, nil
}
