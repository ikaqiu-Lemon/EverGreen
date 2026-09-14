package plan

// `replace_block` 的校验与展开（提案合同 §8.1 第 7 行、§8.3；ChangePlan 合同 §4.2 分区表第 5 行）。
//
// 语义边界：只替换**「理解自检」的当前有效问题块**。历史记录块**只追加、永不改写**
// （EG-CHK-06 的 S2 那一半）——区间只覆盖被判定的目标块，之前的块逐字不动，由 store 侧的
// ApplyReplaceBlock + TestReplaceBlock_HistoryAppendOnly 钉死。
//
// M6（A-57 / §7）在 store 侧接入**块级安全合并**：本 op 的执行仍只经 store 写口
// （execute_m3.go → ApplyReplaceBlock），plan 层不改 op 名称、不改总数（AllOpNames 恒 17）、
// 不拼字节、不判合并——安全判定内核在 internal/mdfile.DecideBlockMerge、接线在
// internal/store/merge.go：base_block_hash 匹配路径与 M3 逐字一致；不匹配时才进入安全判定，
// 安全则替换仍逐字未变的目标块，不安全则跳过。并发（两个 `eg` 写进程互斥、`W28` 等待留痕、
// `E16` 超时）由 internal/txn 的 run.lock 承载（§8，`071`）；本 op **不启用退出码 5**
// （退 5 由 `074` 单点映射）。
//
// 冲突口径**复用既有两值**（§8.3 加严说明，2026-09-02 M2 实证订正）：
// 块 hash 不符 / 安全合并不通过 → `skipped{kind: file_changed, cause: content_hash_mismatch}`，
// 块 locator、`base_block_hash` 与 `W27 block_merge_conflict` 落在**自由文本** detail 里。
// **严禁**为块级冲突新造第三个 kind（如「块冲突」「块哈希已变」之类的专用字面量）——
// `skipped[].kind` 恒为封闭两值，`W27` 是 warning 诊断码、不是新 kind，
// 这两个名字是**永久未启用的预留字面量**（`internal/` 内恒零命中）。

import "github.com/ikaqiu-Lemon/EverGreen/internal/store"

// replaceBlock 校验并展开 replace_block。
//
// 判据：
//   - `target` 必须是可解析的知识卡（E2 / E4）；
//   - `section` 必须逐字是「理解自检」：其余分区一律 E6（分区写入越权，「用户补充」永不可写，B2）；
//   - `block` 缺失或不以换行结束 → E5（载荷形态不成立，不猜、不补字节）；
//   - `base_block_hash` 缺失 → E5：**无凭据不改字节**（B3 不放宽）；
//   - 授权：`replace_block` **不在** A-15 窄口径五个状态类 op 内，缺 `initiator: user`
//     只记 W7 的 warning 形态，不拦截。
func (v *validator) replaceBlock(op *Op) {
	rel, ok := v.cardTarget(op, "target", op.Target)
	if !ok {
		return
	}
	if op.Section != SelfCheckSection {
		v.add(errorAt(E6, op.Index, opPath(op.Index, "section"),
			"replace_block 只允许替换「%s」的当前有效问题块，得到 %q：其余分区不得块替换",
			SelfCheckSection, op.Section))
		return
	}
	if !op.BlockGiven || len(op.Block) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "block"),
			"replace_block 缺 block：新块内容必须逐字给出（本工具不生成、不补写用户内容）"))
		return
	}
	if op.Block[len(op.Block)-1] != '\n' {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "block"),
			"block 必须以换行结束：写入是字节级区间插入，不替调用方补字节"))
		return
	}
	if op.BaseBlockHash == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "base_block_hash"),
			"replace_block 缺 base_block_hash：无凭据不改字节（B3 不放宽；"+
				"块级冲突复用 skipped{kind=%s, cause=%s}）",
			store.SkipFileChanged, store.CauseFor(store.SkipFileChanged)))
		return
	}
	v.requireInitiator(op)
	// 矩阵 #17「分区「理解自检」— 替换当前有效问题块」两格都是 ✅：
	// 与 #7 / #11 相反，本 op 在 P-A 下**可写**，W7 的 warning 不拦截它。
	// 仍然逐 op 查表，是为了让任何一次符号翻转立刻在这里生效，而不是靠人记住。
	if !v.matrixGate(op, ObjectCard, FieldSelfCheckReplace) {
		return
	}

	block := BlockReplace{Section: op.Section, BaseBlockHash: op.BaseBlockHash,
		Payload: op.Block}
	act := Action{Kind: ActReplaceBlock, OpIndex: op.Index, Op: op, ID: op.Target, Path: rel,
		Domain: store.DomainOf(rel), Block: &block}
	v.baseCheck(op, op.Target, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// locator 的口径：BlockReplace.Locator 在 plan 侧**留空**——真实序号只有读盘后才知道
// （当前有效块 = 该分区最后一个块），plan 层不猜盘上块数，也不跨层直取 mdfile
// （§13 依赖方向：plan 只能经 store 拿文件结构口径）。store 侧在冲突时按
// `<id>#<分区>#<序号>` 补齐，locator 只进报告 / 诊断自由文本，永不写进文件内容。
