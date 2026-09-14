package mdfile

// 块级三方「安全合并」判定（M6 范围⑥，合同 A-57 / §7）。
//
// 触发条件：**仅当 `base_block_hash` 与当前有效块不匹配时**才进入本判定；
// 匹配路径的行为与 M3 逐字相同（调用方在 store 侧先走匹配快路，见 internal/store/merge.go）。
//
// 判定**只在块粒度**发生：把当期文件切成块（§4.2 的五种块型），用 base_block_hash
// 在这些块里找「仍然逐字未变」的目标块。**不做行级 diff / patch、不引入任何 diff 依赖**。
//
// 与 A-57 四判据的对应（全真才「安全」）：
//  1. 目标分区可按 Span 定位，且分区名与 base 时相同 —— 由 Blocks(section) 成功承载；
//  2. 「前像 → 当前值」的差异块集合与本次要写的块字节区间**不相交** —— 在块粒度上等价于
//     「当期文件里仍存在一个 block_hash == base_block_hash 的块」：该块自读取以来逐字未变，
//     因此当期发生的差异一定落在**别的**块，与目标块区间不相交；反之若无任何块命中
//     base_block_hash，说明差异**触及了目标块本身**，区间相交 → 不安全；
//  3. 目标分区**不属于**「用户补充」（永不写「用户补充」，B2）；
//  4. 当期文件 frontmatter 仍是合法 YAML（E4 不成立）。
//
// 不安全一律**跳过**并留痕 `W27 block_merge_conflict`（由 store 侧落进报告 skipped[]，
// `kind` 恒 2 值、不扩张）。本模块只给「安全 / 不安全 + 目标块序号」的纯判定，不写盘、不改字节。

// CodeBlockMergeConflict 是块级安全合并不通过的诊断码 `W27`（合同 §12）。
// 落点在本包与 internal/store/merge.go；`kind` 不因此扩张（W27 是 warning，不是新 kind）。
const CodeBlockMergeConflict = "W27"

// BlockMergeConflictReason 是 W27 的机器可读子因（只进自由文本诊断，不进 skipped[].kind）。
const BlockMergeConflictReason = "block_merge_conflict"

// BlockMergeOutcome 是块级安全判定的二值结论。
type BlockMergeOutcome int

const (
	// BlockMergeConflict 表示不安全：调用方必须跳过该块并留痕 W27。
	BlockMergeConflict BlockMergeOutcome = iota
	// BlockMergeSafe 表示安全：调用方可用 BlockIndex 定位的块做区间替换。
	BlockMergeSafe
)

// BlockMergeDecision 是一次块级安全判定的结论。
type BlockMergeDecision struct {
	Outcome BlockMergeOutcome
	// BlockIndex 是安全时可替换的目标块序号（Outcome==BlockMergeSafe 时有效）。
	BlockIndex int
	// CurrentHash 是当前有效块（分区最后一个块）的 block_hash，只用于诊断自由文本；
	// 分区无块时为空。
	CurrentHash string
	// Reason 是不安全时的可读原因（只进诊断自由文本，不进 skipped[].kind）。
	Reason string
}

// Safe 返回本次判定是否「安全可合并」。
func (d BlockMergeDecision) Safe() bool { return d.Outcome == BlockMergeSafe }

// DecideBlockMerge 在**块粒度**上判定当期文件能否把目标块安全合并（A-57 四判据）。
//
// 参数：
//   - current：当期磁盘文件的完整字节（**只读**，不被修改）；
//   - section：目标分区名（replace_block 恒为「理解自检」）；
//   - baseBlockHash：调用方读到目标块时的 block_hash（凭据，缺它上层早已 E5 拦下）。
//
// 结论：四判据全真 → BlockMergeSafe + 目标块序号；任一不真 → BlockMergeConflict + 原因。
//
// 目标块选择：命中 base_block_hash 的块可能不止一个（同分区出现逐字相同的块）。
// 为与 M3「当前有效块 = 分区最后一个块」的口径一致、并保持匹配路径逐字不变，
// 命中多个时取**最后一个命中**的块（最靠近当前有效块的那个）。
func DecideBlockMerge(current []byte, section, baseBlockHash string) BlockMergeDecision {
	// 判据 4：当期文件仍是可解析文档且 frontmatter 是合法 YAML。
	doc, err := Parse(current)
	if err != nil {
		return BlockMergeDecision{Outcome: BlockMergeConflict,
			Reason: "当期文件无法解析：" + err.Error()}
	}
	var fm map[string]interface{}
	if err := doc.DecodeFM(&fm); err != nil {
		return BlockMergeDecision{Outcome: BlockMergeConflict,
			Reason: "当期文件 frontmatter 不是合法 YAML（E4）：" + err.Error()}
	}
	// 判据 3：永不写「用户补充」（B2）。
	for _, never := range NeverWriteSections() {
		if section == never {
			return BlockMergeDecision{Outcome: BlockMergeConflict,
				Reason: "目标分区「" + section + "」永不可写（B2）"}
		}
	}
	// 判据 1：目标分区可按 Span 定位（分区名与 base 时相同由调用方保证：section 恒「理解自检」）。
	blocks, err := doc.Blocks(section)
	if err != nil {
		return BlockMergeDecision{Outcome: BlockMergeConflict,
			Reason: "目标分区不可定位：" + err.Error()}
	}
	if len(blocks) == 0 {
		return BlockMergeDecision{Outcome: BlockMergeConflict,
			Reason: "目标分区无任何块，无可合并目标"}
	}
	raw := doc.Raw
	last := len(blocks) - 1
	curHash := BlockHash(blocks[last].Bytes(raw))

	// 判据 2（块粒度落地）：找仍逐字未变的目标块（block_hash == base_block_hash）。
	// 命中即说明差异落在别的块、与目标块区间不相交 → 安全。取最后一个命中，贴合 M3 口径。
	match := -1
	for i := range blocks {
		if BlockHash(blocks[i].Bytes(raw)) == baseBlockHash {
			match = i
		}
	}
	if match < 0 {
		return BlockMergeDecision{Outcome: BlockMergeConflict, CurrentHash: curHash,
			Reason: "目标块自读取以来已变化（当期无块命中 base_block_hash，差异触及目标块区间）"}
	}
	return BlockMergeDecision{Outcome: BlockMergeSafe, BlockIndex: match, CurrentHash: curHash}
}
