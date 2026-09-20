package store

// 审阅式提炼的两个**解析承载数据结构**（Schema v2 契约 §4.2.1 遗漏项 / §4.2.3 提炼覆盖）。
//
// # 用途：承载，不预设落盘形态
//
// 这两个结构体只承载解析层（internal/plan）从 `write_note` 读到的 omissions[] /
// extraction_coverage[] 原始字段。它们**不**规定任何落盘分区、清单或渲染模板——
// 唯一设计真源对 omissions 的要求是「校验 + source_ref round-trip」，并未定义某个名为
// 「遗漏说明」的分区或清单形态；对 extraction_coverage 的落盘形态同样由后续批次按设计真源确定。
// 因此本文件刻意不声称「最终会渲染成某某清单」，以免把尚未定案的形态写死成注释里的伪约束。
//
// # 为什么定义在 store
//
// 与 NoteBlock / NoteExtraction 同处一包：这两组数据的下游消费（校验、round-trip、
// 以及将来可能的渲染）都围绕写路径展开（§16.3），放在 store 让 plan 侧以类型别名引用、
// 免去逐字段拷贝的转换函数，也就免去「加字段只改一处」的漂移点。
//
// # 本批（T12-1）只定义、不消费
//
// 这里没有任何方法/渲染函数：T12-1 的边界是「解析承载」。字段命名逐字采用契约源键
// （§4.2.1 的 source_ref / reason；§4.2.3 的 module / source_refs / summary /
// disposition / outputs / reason），语义与源键一一对应，不增删、不改写。

// Omission 承载一条 `omissions[]` 项（契约 §4.2.1）：SourceRef 指向一段来源行段
// （形如 `L<start>-L<end>`），Reason 说明为何未纳入整理。行段可解析性、与 blocks 覆盖的
// 关系、round-trip 等判定由后续批次按设计真源处理，本结构体只承载解析结果。
type Omission struct {
	SourceRef string
	Reason    string
}

// ExtractionCoverage 承载一条 `extraction_coverage[]` 项（契约 §4.2.3）：
//   - Module 内容模块标识；
//   - SourceRefs 对应的来源行段；
//   - Summary 概述；
//   - Disposition 去向（outputs / note_only / missing）；
//   - Outputs 产出卡 ID；
//   - Reason 缘由。
//
// 唯一性、引用有效性、与 output_cards 的一致性、disposition 取值等判定全部属后续批次；
// 本结构体在 T12-1 只承载解析结果。
type ExtractionCoverage struct {
	Module      string
	SourceRefs  []string
	Summary     string
	Disposition string
	Outputs     []string
	Reason      string
}
