package plan

// M3 两条**非追加**写入形态的执行编排：`remove_relation`（物理移除）与 `replace_block`（块替换）。
//
// 与 executor.go 同源纪律：本文件只调用 internal/store 的写口
// （ApplyRemoveRelation / ApplyReplaceBlock），自己**不拼字节、不写盘、不 commit**；
// 跳过命名只用 store.SkipReason 的**封闭两值**，不自造第三种 kind。
//
// B1 的边界：这两条形态**只对用户显式发起的 op 开放**（授权判定在 validate_m3.go 的
// requireInitiator），Agent 自动路径仍只有追加型写口。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// removeRelationWrite 执行 remove_relation：物理移除匹配的全部关系记录（A-24 口径 1）。
//
// 未命中的情形**不会到这里**：validate_m3.go 在校验期就按 **W10** 幂等收口，不产出 action
// （零写入、零 commit）。这里再遇到 ErrRelationNotFound 说明磁盘在读后被改动，
// 按普通失败如实上报，不静默。
func (e *executor) removeRelationWrite(a Action) {
	rm := a.Removal
	if rm == nil {
		return
	}
	out, err := e.s.ApplyRemoveRelation(store.RemoveRelationSpec{
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
		Stamp:        e.opt.Stamp,
		From:         model.CardID(a.ID),
		Type:         model.RelationType(rm.Type),
		Target:       model.CardID(rm.Target),
	})
	if !e.record(a, out.Result, err) {
		return
	}
	e.out.KnowledgeRemoved = append(e.out.KnowledgeRemoved,
		KnowledgeRecord{From: a.ID, Type: rm.Type, Target: rm.Target, Reason: a.Op.Reason})
	e.out.CardsUpdated = append(e.out.CardsUpdated, a.ID)
	kind := ImpactRelation
	if rm.Type == string(model.RelationOpposing) {
		kind = ImpactOpposing
	}
	e.impact(kind, a.ID, a.OpIndex,
		fmt.Sprintf("移除论证关系 %s → %s：物理移除 %d 条匹配记录（不留墓碑，删除前的信息由 Git 历史承载）",
			rm.Type, rm.Target, out.Removed))
}

// replaceBlockWrite 执行 replace_block：替换「理解自检」的当前有效问题块。
//
// 块 hash 冲突由 store 侧返回 *SkipError{file_changed}，经 record 落进
// `skipped[]`（cause=content_hash_mismatch），块 locator 与期望 hash 在自由文本 detail 里。
func (e *executor) replaceBlockWrite(a Action) {
	blk := a.Block
	if blk == nil {
		return
	}
	res, err := e.s.ApplyReplaceBlock(store.ReplaceBlockSpec{
		Rel:           a.Path,
		ID:            model.CardID(a.ID),
		ExpectedHash:  e.expect(a),
		Stamp:         e.opt.Stamp,
		Section:       blk.Section,
		BaseBlockHash: blk.BaseBlockHash,
		Block:         blk.Payload,
		Locator:       blk.Locator,
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.BlocksReplaced = append(e.out.BlocksReplaced, res.Path)
	e.out.CardsUpdated = append(e.out.CardsUpdated, a.ID)
}

// —— M3 状态维度的两条形态 ——
//
// 两者都只经 store 的**导出状态写入入口** ApplyStateWrite 落盘，绝不直呼三个 setter：
// 「写口唯一」的机械判据要求 SetStatus / SetReplacedBy / SetDeleted 的非测试调用点
// 全部落在 internal/store/ 内（护栏见 store_test.go），plan/ 与 cli/ 必须零命中。
//
// 幂等（W11）与授权（W7）都在校验期收口：走到这里的 action 一定是「确实要改一格」，
// 因此这里不再重判状态，也不吞任何错误——hash 不匹配由 store 返回 *SkipError，
// 经 record 落进 skipped[]（cause=content_hash_mismatch），上层据此退 3。

// setStatusWrite 执行 deprecate / restore：覆盖 status 单键（正交维度，绝不碰 deleted_*）。
func (e *executor) setStatusWrite(a Action) {
	st := a.Status
	if st == nil {
		return
	}
	res, err := e.s.ApplyStateWrite(store.StateWriteSpec{
		Op:           store.StateWriteStatus,
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
		Status:       model.Status(st.Status),
		// 状态流转是一次实际写入 → 刷新 `updated_at`（授权合同 §2.1 矩阵第 8 行）。
		// 时刻取本次执行注入的 Stamp，本文件不读时钟（可复算）。
		Stamp: e.opt.Stamp,
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.CardsUpdated = append(e.out.CardsUpdated, a.ID)
}

// setReplacedByWrite 执行 set_replaced_by：只在主体卡上写替代指针（单向存储，
// 被指向的目标卡不打开、不改写——避免双写产生两份真相）。
func (e *executor) setReplacedByWrite(a Action) {
	rb := a.Replaced
	if rb == nil {
		return
	}
	res, err := e.s.ApplyStateWrite(store.StateWriteSpec{
		Op:           store.StateWriteReplacedBy,
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
		Target:       model.CardID(rb.Target),
		Reason:       rb.Reason,
		// 写替代指针同样是实际写入 → 刷新 `updated_at`（矩阵第 8 行）。
		Stamp: e.opt.Stamp,
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.CardsUpdated = append(e.out.CardsUpdated, a.ID)
}

// —— A-13 载体：`eg edit` 的分区正文替换 ——

// editSectionWrite 执行 edit_section：用新正文逐字替换目标分区（store.ApplyReplaceSection）。
//
// 与本文件其余形态同源纪律：只调 store 的写口，自己不拼字节、不写盘、不 commit；
// B3 冲突由 store 返回 *SkipError{file_changed}，经 record 落进 `skipped[]`
// （cause=content_hash_mismatch），上层据此退 3——**不因为是用户显式命令就跳过比对**。
//
// 授权在校验期已收口（editSectionGate：非 P-U 一律 E6、零 action），因此走到这里的
// action 一定是用户显式路径；这里不再重判授权，也不吞任何错误。
func (e *executor) editSectionWrite(a Action) {
	ed := a.Edit
	if ed == nil {
		return
	}
	res, err := e.s.ApplyReplaceSection(store.ReplaceSectionSpec{
		Rel:          a.Path,
		ID:           model.CardID(a.ID),
		ExpectedHash: e.expect(a),
		Section:      ed.Section,
		Content:      ed.Payload,
		// 正文被替换必须推进 `updated_at`，否则「过目 → 改内容」的卡再也进不了
		// `eg unreviewed`（矩阵第 8 行 / I-…-009）。
		Stamp: e.opt.Stamp,
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.SectionsEdited = append(e.out.SectionsEdited, res.Path)
	e.out.CardsUpdated = append(e.out.CardsUpdated, a.ID)
	e.impact(ImpactCoreKnowledge, a.ID, a.OpIndex, fmt.Sprintf(
		"用户显式修改分区「%s」：整段正文按 --content 逐字替换（其余分区、frontmatter 三维度字节不变）",
		ed.Section))
}
