package store

// M3 的两条**非追加**落盘形态：`remove_relation`（物理移除关系记录）与
// `replace_block`（替换「当前有效自检问题块」）。阶段归属 S2 / M3。
//
// 为什么这两条不违反 B1：B1 约束的是**Agent 自动路径**「只追加」。这两条 op 都是
// **用户显式发起**（`initiator: user`，授权判定在 internal/plan），且：
//   - `remove_relation` 的语义由 owner 裁决 **A-24** 定死为「物理移除、不留墓碑」，
//     删除前的完整信息由 **Git 历史**承载（提案合同 §8.5.2「B1 不放宽：不靠墓碑」）；
//   - `replace_block` 只换「当前有效自检问题块」，历史记录块**只追加、永不改写**
//     （EG-CHK-06 的 S2 那一半）。
//
// B2 / B3 / B4 一条都不放宽：
//   - B3：写前 `content_hash` 比对照旧，冲突 → *SkipError{file_changed}；
//     `skipped[].kind` 仍**恰两值**（receipt.go），块级冲突复用 file_changed /
//     content_hash_mismatch，块 locator 与期望 hash 落在自由文本 Detail 里；
//   - B2：用户分区逐字保留由 PreserveUserSectionsAfterCut 兜底；
//   - B4：本包不 commit、不回滚，失败即保留磁盘现状。
//
// 字节机制仍然只有一条：mdfile 的区间拼接（CutFMSeqItems / ReplaceBlock），
// 本包不拼 YAML、不做「结构体 → 序列化」回写。

import (
	"errors"
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

var (
	// ErrRelationNotFound 宿主卡的 relations[] 里没有匹配的三元组（上层按 W10 幂等处理）。
	ErrRelationNotFound = errors.New("relations[] 无匹配记录（幂等 no-op）")
	// ErrBlockSectionNotAllowed 块替换的目标分区不是「理解自检」。
	ErrBlockSectionNotAllowed = errors.New("块替换只允许「理解自检」的当前有效问题块")
	// ErrNoCurrentBlock 「理解自检」没有可替换的当前有效问题块。
	ErrNoCurrentBlock = errors.New("「理解自检」没有可替换的当前有效问题块")
	// ErrBaseBlockHashRequired 缺 base_block_hash：块替换必须带它（无凭据不改字节）。
	ErrBaseBlockHashRequired = errors.New("块替换必须带 base_block_hash")
)

// RemoveRelationSpec 是一次 remove_relation 的落盘输入。
//
// From / Target 是跨类型端点（RelationEndpoint，前缀 k- / o-）：宿主与对端都可能是
// 知识卡或观点。两者必须是**已规范化**的两端（`opposing` 写在字典序较小的一端）：
// 规范化的唯一实现在 internal/rules，由 internal/plan 在校验期完成——本包只做
// 「已规范化」的硬校验，不反向依赖 rules（§13 依赖方向）。
type RemoveRelationSpec struct {
	Rel          string // 宿主实体（From 端）相对路径
	ExpectedHash string
	Stamp        model.Stamp
	From         model.RelationEndpoint
	Type         model.RelationType
	Target       model.RelationEndpoint
}

// RemoveRelationResult 是一次物理移除的回执：移除条数 + 单文件写入回执。
type RemoveRelationResult struct {
	Result
	Removed int
}

// ApplyRemoveRelation 从宿主卡 frontmatter 的 relations[] 里**物理移除**匹配的全部记录。
//
// A-24 五条逐条落地：
//  1. 匹配键 = 规范化后的三元组 `(from, type, target)`，`reason` **不参与**匹配；
//     命中即移除**匹配的全部记录**（历史遗留重复条目一并清掉，不是只删首条）。
//  2. `opposing` 必须已按两端 ID 字典序规范化（未规范化 → ErrOpposingNotNormalized）。
//  3. 不留墓碑、不给关系条目引入任何新主键：model.Relation 仍恰三字段，移除即字节消失。
//  4. 未命中 → ErrRelationNotFound + **零写入**（上层按 W10 幂等处理，不产生空 commit）。
//  5. 逻辑删除实体**不**走这里：状态与删除正交（F3），只有用户显式移除才动记录。
func (s *Store) ApplyRemoveRelation(spec RemoveRelationSpec) (RemoveRelationResult, error) {
	out := RemoveRelationResult{Result: Result{Path: spec.Rel}}
	if spec.Rel == "" {
		return out, ErrCardRelRequired
	}
	if _, err := model.ParseRelationType(string(spec.Type)); err != nil {
		return out, fmt.Errorf("论证关系取值封闭（冻结合同 F4，合法取值恰 %v）：%w",
			model.ValidRelationTypes(), err)
	}
	if err := relationEndpointOnly("relations[].target", spec.Target); err != nil {
		return out, err
	}
	if spec.Type == model.RelationOpposing && string(spec.From) > string(spec.Target) {
		return out, fmt.Errorf("%w：应写在 %s 一端，实得 %s", ErrOpposingNotNormalized,
			spec.Target, spec.From)
	}

	f, hostRelations, _, _, err := s.readRelationHost(spec.From, spec.Rel)
	if err != nil {
		return out, err
	}
	out.Hash = f.Hash
	var hit []int
	for i, exist := range hostRelations {
		if exist.Type == spec.Type && string(exist.Target) == string(spec.Target) {
			hit = append(hit, i)
		}
	}
	if len(hit) == 0 {
		out.Detail = fmt.Sprintf("relations[] 无 (%s, %s, %s)：幂等 no-op，零写入、零 commit",
			spec.From, spec.Type, spec.Target)
		return out, fmt.Errorf("%w：(%s, %s, %s)", ErrRelationNotFound,
			spec.From, spec.Type, spec.Target)
	}

	// 关系被物理移除是一次**实际写入** → 同一次守卫写里刷新 `updated_at`（矩阵第 8 行）。
	res, err := s.mutateGuarded(spec.Rel, spec.ExpectedHash,
		withUpdatedAt(spec.Stamp, func(_ File, doc *mdfile.Doc) ([]byte, error) {
			return doc.CutFMSeqItems("relations", hit)
		}))
	out.Result = res
	if err != nil {
		return out, err
	}
	out.Removed = len(hit)
	out.Detail = fmt.Sprintf("已物理移除 relations[] 中 (%s, %s, %s) 的全部 %d 条记录（不留墓碑）",
		spec.From, spec.Type, spec.Target, len(hit))
	return out, nil
}

// ReplaceBlockSpec 是一次 replace_block 的落盘输入。
type ReplaceBlockSpec struct {
	Rel          string
	ExpectedHash string
	Stamp        model.Stamp
	// ID 是宿主卡 ID，只用于拼 locator（`<id>#<分区>#<序号>`）；留空则退回相对路径。
	ID model.CardID
	// Section 必须逐字是「理解自检」：M3 只允许替换当前有效自检问题块。
	Section string
	// BaseBlockHash 是调用方读到的当前有效块的 block_hash（必带）。
	BaseBlockHash string
	// Block 是新的块字节，逐字插入，必须以 \n 结束。
	Block []byte
	// Locator 只进报告 / 诊断文本（`<id>#<分区>#<序号>`），永不写进文件内容。
	Locator string
}

// ApplyReplaceBlock 替换「理解自检」的**当前有效问题块**（= 该分区最后一个块）。
//
// 固定次序：读盘 → 字节自检 → 块级安全判定（A-57）→ 区间替换 → B2 用户分区逐字保留 → 原子落盘。
//
// 块级安全判定（内核在 internal/mdfile.DecideBlockMerge，接线在 internal/store/merge.go）：
//   - **匹配路径**（当前有效块 hash == base_block_hash）行为与 M3 逐字相同（替换最后一个块）；
//   - **不匹配路径**才进入安全合并——安全（当期仍存在逐字未变的目标块）则替换该块，
//     不安全则 *SkipError{file_changed}，Detail 带 W27 + 块 locator + 期望 hash。
//
// 历史记录块（当前有效块之前的块）逐字不变：区间只覆盖被判定的目标块。
func (s *Store) ApplyReplaceBlock(spec ReplaceBlockSpec) (Result, error) {
	res := Result{Path: spec.Rel}
	if spec.Rel == "" {
		return res, ErrCardRelRequired
	}
	if spec.Section != mdfile.SecSelfCheck {
		return res, fmt.Errorf("%w：得到「%s」", ErrBlockSectionNotAllowed, spec.Section)
	}
	if spec.BaseBlockHash == "" {
		return res, ErrBaseBlockHashRequired
	}
	if len(spec.Block) == 0 || spec.Block[len(spec.Block)-1] != '\n' {
		return res, mdfile.ErrPayloadNotLineTerminated
	}
	return s.applyReplaceBlockMerge(spec)
}

// CurrentSelfCheckBlockHash 是只读转发：取「理解自检」当前有效问题块（最后一个块）
// 的 block_hash，供上层在不直连 mdfile（§13 依赖方向）的前提下取得 base_block_hash。
// 只读、不落盘、不改字节。
func CurrentSelfCheckBlockHash(raw []byte) (string, error) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return "", err
	}
	_, hash, err := currentSelfCheckBlock(doc)
	return hash, err
}

// currentSelfCheckBlock 定位「理解自检」的当前有效问题块：**最后一个块**。
//
// 口径：历史记录块只追加、永不改写（EG-CHK-06），因此分区里最后追加的那个块
// 就是「当前有效」的那个；它之前的块一律是历史记录。
func currentSelfCheckBlock(doc *mdfile.Doc) (int, string, error) {
	blocks, err := doc.Blocks(mdfile.SecSelfCheck)
	if err != nil {
		return 0, "", err
	}
	if len(blocks) == 0 {
		return 0, "", ErrNoCurrentBlock
	}
	last := len(blocks) - 1
	return last, mdfile.BlockHash(blocks[last].Bytes(doc.Raw)), nil
}

// mutateGuarded 是两条 M3 非追加写形态共用的守卫写入口（固定次序，与 WriteGuarded 同源）：
//
//	读盘 → content_hash 与 expectedHash 比对（B3，不一致 → SkipFileChanged）
//	→ Parse→Render 与原字节逐字比对，不等即拒写
//	→ build 产出候选字节（只允许 mdfile 的区间拼接；build 可返回 *SkipError）
//	→ 候选字节再自检 + 用户分区逐字保留（B2）
//	→ tmp + fsync + rename（并 fsync 目录）
//	→ 写后 yaml.v3 只读复核仍是合法 YAML
//
// 全程不覆盖、不强写、不重试、不回滚。
func (s *Store) mutateGuarded(rel, expectedHash string,
	build func(f File, doc *mdfile.Doc) ([]byte, error)) (Result, error) {
	f, err := s.Read(rel)
	if err != nil {
		return Result{Path: rel}, err
	}
	res := Result{Path: rel, Hash: f.Hash}
	if expectedHash != "" && expectedHash != f.Hash {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: fmt.Sprintf("自读取以来文件已变化：期望 %s，磁盘 %s", expectedHash, f.Hash)}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if err := mdfile.SelfCheck(f.Bytes); err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: ErrSelfCheckFailed.Error() + "：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	doc, err := mdfile.Parse(f.Bytes)
	if err != nil {
		res.Detail = err.Error()
		return res, err
	}
	cur, err := build(f, doc)
	if err != nil {
		if skip, ok := AsSkip(err); ok {
			res.Reason, res.Detail = skip.Reason, skip.Detail
		} else {
			res.Detail = err.Error()
		}
		return res, err
	}
	if err := mdfile.SelfCheck(cur); err != nil {
		skip := &SkipError{Path: rel, Reason: SkipFileChanged,
			Detail: ErrSelfCheckFailed.Error() + "（候选输出）：" + err.Error()}
		res.Reason, res.Detail = skip.Reason, skip.Detail
		return res, skip
	}
	if err := PreserveUserSectionsAfterCut(rel, f.Bytes, cur); err != nil {
		if skip, ok := AsSkip(err); ok {
			res.Reason, res.Detail = skip.Reason, skip.Detail
		}
		return res, err
	}
	if err := s.persist(rel, f.Path, cur, 0o644); err != nil {
		res.Detail = err.Error()
		return res, err
	}
	res.Written = true
	res.Hash = ContentHash(cur)
	if doc, err := mdfile.Parse(cur); err != nil {
		res.Warnings = append(res.Warnings, "写后复核失败：写入结果无法解析："+err.Error())
	} else {
		var fm map[string]interface{}
		if err := doc.DecodeFM(&fm); err != nil {
			res.Warnings = append(res.Warnings, "写后复核失败：frontmatter 不是合法 YAML："+err.Error())
		}
	}
	return res, nil
}

// cardIDOnly 是 E3 的硬拦：关系另一端只接受知识卡 ID（`k-…`）。
func cardIDOnly(field string, id model.CardID) error {
	if len(id) >= len(model.PrefixSource) && string(id[:len(model.PrefixSource)]) == model.PrefixSource {
		return fmt.Errorf("%w：%s 只接受 %s 前缀的卡 ID，得到 %q（E3）",
			ErrRelationTargetType, field, model.PrefixCard, id)
	}
	if len(id) < len(model.PrefixCard) || string(id[:len(model.PrefixCard)]) != model.PrefixCard {
		return fmt.Errorf("%w：%s 必须是 %s 前缀的卡 ID，得到 %q",
			ErrRelationTargetType, field, model.PrefixCard, id)
	}
	return nil
}

// relationEndpointOnly 是论证关系端点（`relations[].target`）的 E3 硬拦：
// 只接受 k- / o- 端点。s- 明确按 E3「ID 类型写混」拒绝；其余前缀（n- / r- / p-）与
// 不可解析 ID 一律拒绝。论证关系永远发生在两条论证性产物（知识卡 / 观点）之间。
func relationEndpointOnly(field string, id model.RelationEndpoint) error {
	if len(id) >= len(model.PrefixSource) && string(id[:len(model.PrefixSource)]) == model.PrefixSource {
		return fmt.Errorf("%w：%s 只接受 %s / %s 前缀的端点，得到 %q（E3）",
			ErrRelationTargetType, field, model.PrefixCard, model.PrefixOpinion, id)
	}
	if _, err := model.ParseRelationEndpoint(string(id)); err != nil {
		return fmt.Errorf("%w：%s 必须是 %s / %s 前缀的端点，得到 %q（%v）",
			ErrRelationTargetType, field, model.PrefixCard, model.PrefixOpinion, id, err)
	}
	return nil
}
