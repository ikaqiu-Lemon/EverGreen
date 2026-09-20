package store

// 审阅式提炼的两个**解析承载数据结构**（Schema v2 契约 §4.2.1 遗漏项 / §4.2.3 提炼覆盖）。
//
// # 用途：承载解析结果；落盘形态按设计真源逐项接入
//
// 这两个结构体承载解析层（internal/plan）从 `write_note` 读到的 omissions[] /
// extraction_coverage[] 原始字段。二者的落盘去向不同，按设计真源分别接入：
//   - omissions[] **已被消费**：v2 审阅式 writer（NoteReviewBytes → mdfile 的机器锚点协议）
//     把每条 omission 的 source_ref / reason 编进一条版本化机器锚点，满足设计真源对 omissions
//     的「校验 + source_ref round-trip」要求。它**不**新增任何名为「遗漏说明」的可见分区或清单——
//     遗漏是机器元数据，靠锚点严格读回，而不是渲染成读者可见的段落。
//   - extraction_coverage[] **尚未消费**：其落盘形态由后续批次（T12-4）按设计真源确定，
//     本文件刻意不声称它「最终会渲染成某某清单」，以免把尚未定案的形态写死成注释里的伪约束。
//
// # 为什么定义在 store
//
// 与 NoteBlock / NoteExtraction 同处一包：这两组数据的下游消费（校验、round-trip、
// 以及将来可能的渲染）都围绕写路径展开（§16.3），放在 store 让 plan 侧以类型别名引用、
// 免去逐字段拷贝的转换函数，也就免去「加字段只改一处」的漂移点。
//
// # 命名口径
//
// 字段命名逐字采用契约源键（§4.2.1 的 source_ref / reason；§4.2.3 的 module / source_refs /
// summary / disposition / outputs / reason），语义与源键一一对应，不增删、不改写。

// Omission 承载一条 `omissions[]` 项（契约 §4.2.1）：SourceRef 指向一段来源行段
// （形如 `L<start>-L<end>`），Reason 说明为何未纳入整理。行段可解析性、与 blocks 覆盖的
// 关系由 plan 侧校验（source_coverage.go）；source_ref / reason 的机器锚点 round-trip 由
// NoteReviewBytes → mdfile 落地。本结构体本身只承载解析结果。
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
// 本结构体目前只承载解析结果，其落盘形态待 T12-4 按设计真源接入。
type ExtractionCoverage struct {
	Module      string
	SourceRefs  []string
	Summary     string
	Disposition string
	Outputs     []string
	Reason      string
}
