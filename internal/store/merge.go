package store

// 块级安全合并的写口接线（M6 范围⑥，合同 A-57 / §7；`T-…-073`）。
//
// 与 M3 的关系（保持匹配路径逐字不变，只在不匹配时插入安全判定）：
//   - **匹配路径**：当前有效块（分区最后一个块）的 block_hash == base_block_hash →
//     行为与 M3 逐字相同（替换最后一个块），M6 不改这条路径一个字节；
//   - **不匹配路径**：进入 A-57 块级安全判定（mdfile.DecideBlockMerge）——
//     · 安全（当期仍存在逐字未变的目标块，差异与目标块区间不相交）→ 替换该目标块；
//     · 不安全 → 跳过并留痕 `W27 block_merge_conflict`，进既有 skipped[]，`kind` 恒 2 值。
//
// 为什么 replace_block 不再走整文件 content_hash 的 B3 硬跳过：A-57 把 replace_block 的
// 「自读取以来是否变化」凭据从**整文件 hash** 收窄为**块级 base_block_hash**——整文件在别处
// 变化（如关系/用户补充/其它分区）不再一律阻断本次块替换，只要目标块自身逐字未变即安全合并。
// 这不是放宽 B3：目标块的逐字比对（base_block_hash）就是本 op 的 B3 凭据，缺它上层早已 E5 拦下。
// 其余写形态（append / edit_section / remove_relation / 状态写）的整文件 B3 一字不改。
//
// 并发：两个 `eg` 写进程的互斥、`W28` 等待留痕与 `E16` 超时由 M6 的 S5 锁与事务层承载
// （§8，`071`），本层（S1 写口）不引入任何 S5 机制、不重复实现锁语义，也**不启用退出码 5**
// （退 5 由 `074` 单点映射）。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// applyReplaceBlockMerge 是 replace_block 的落盘编排（A-57 接线）。
//
// 固定次序（复用 mutateGuarded 的守卫尾段：候选自检 → B2 用户分区逐字保留 → 原子落盘 →
// 写后 YAML 复核）：读盘 → 字节自检 → 块级安全判定（匹配路径等价 M3；不匹配走安全合并）→
// 安全则区间替换目标块，不安全则 *SkipError{file_changed} 并在自由文本带 W27 + locator + 期望 hash。
//
// 传入 mutateGuarded 的 expectedHash 恒为空：整文件 B3 由本 op 的块级 base_block_hash 承载
// （见文件头说明）。
func (s *Store) applyReplaceBlockMerge(spec ReplaceBlockSpec) (Result, error) {
	// 块替换成功即改了正文 → 同一次守卫写里刷新 `updated_at`（矩阵第 8 行）；
	// 走不安全分支时 build 返回 *SkipError，零写入，自然也不刷新。
	return s.mutateGuarded(spec.Rel, "",
		withUpdatedAt(spec.Stamp, func(f File, doc *mdfile.Doc) ([]byte, error) {
			dec := mdfile.DecideBlockMerge(f.Bytes, spec.Section, spec.BaseBlockHash)
			if !dec.Safe() {
				return nil, s.blockMergeSkip(spec, dec)
			}
			return doc.ReplaceBlock(spec.Section, dec.BlockIndex, spec.Block)
		}))
}

// blockMergeSkip 构造块级安全合并不通过的 *SkipError：
//   - `kind` 恒复用封闭两值 `file_changed`（cause=content_hash_mismatch），**不新造第三值**；
//   - `W27 block_merge_conflict`、块 locator、期望 base_block_hash 与磁盘当前有效块 hash
//     全部落在**自由文本** Detail 里（B1/§7 口径：块级冲突不扩张 kind）。
func (s *Store) blockMergeSkip(spec ReplaceBlockSpec, dec mdfile.BlockMergeDecision) *SkipError {
	locator := spec.Locator
	if locator == "" {
		who := string(spec.ID)
		if who == "" {
			who = spec.Rel
		}
		// locator 只进诊断自由文本：序号用「当前有效块」的位置口径（分区最后一个块）。
		locator = mdfile.Locator(who, spec.Section, blockLocatorIndex(dec))
	}
	return &SkipError{Path: spec.Rel, Reason: SkipFileChanged,
		Detail: fmt.Sprintf(
			"块级安全合并不通过（%s %s）：locator=%s，期望 base_block_hash=%s，磁盘当前有效块 %s；原因：%s",
			mdfile.CodeBlockMergeConflict, mdfile.BlockMergeConflictReason,
			locator, spec.BaseBlockHash, dec.CurrentHash, dec.Reason)}
}

// blockLocatorIndex 给诊断 locator 一个稳定序号：安全判定失败时无命中块，用 0 占位
// （locator 不稳定、不进权威侧、只作报告可读性，序号本身不参与任何比对）。
func blockLocatorIndex(dec mdfile.BlockMergeDecision) int {
	if dec.Safe() {
		return dec.BlockIndex
	}
	return 0
}
