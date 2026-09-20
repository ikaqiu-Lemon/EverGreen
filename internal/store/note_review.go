package store

import "github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"

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
//   - extraction_coverage[] **已被消费**：语义校验由 plan 侧 note_coverage.go 承担（T12-4，
//     契约 §4.2.3），校验通过后原样透传给 NoteExtraction.Coverage；落盘渲染 / 读回的唯一实现
//     收在 mdfile 的覆盖矩阵协议（RenderCoverageMatrix / ParseCoverageMatrix）——渲染成「提取
//     结果」里紧随 Knowledge/Opinion 清单之后的一张覆盖矩阵表 + 每行一条独立版本化机器锚点。
//
// # 为什么定义在 store
//
// 与 NoteBlock / NoteExtraction 同处一包：这两组数据的下游消费（校验、round-trip、渲染）
// 都围绕写路径展开（§16.3），放在 store 让 plan 侧以类型别名引用、
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
// 唯一性、引用有效性、与 output_cards 的一致性、disposition 取值等语义判定由 plan 侧
// note_coverage.go 承担（T12-4，契约 §4.2.3）；本结构体承载解析结果，其落盘渲染由 mdfile 的
// 覆盖矩阵协议按各原始字段落地。
type ExtractionCoverage struct {
	Module      string
	SourceRefs  []string
	Summary     string
	Disposition string
	Outputs     []string
	Reason      string
}

// —— 审阅式批注词表 / 覆盖处置枚举的**转发层**（契约 §4.2.2 / §4.2.3 / §5.1 / D-10）——
//
// 内置批注 key→标签、扩展 key 合法性、覆盖 disposition 三值的**单一字面量真源**都在 mdfile
// （词表与渲染标记同源，见 mdfile/note_review.go 与 mdfile/coverage.go）。plan 侧的字段级校验
// （note_annotation.go / note_coverage.go）只消费本转发层，**不**直接 import mdfile——依赖方向
// 恒为 plan → store → mdfile（施工索引 §13；由 cmd/eg/arch_test.go 的「plan 直连 mdfile」判据钉死）。
// 与 assets.go 的资产口径转发同一手法：薄函数 / 常量，加一类批注只改 mdfile 一处。

// BuiltinAnnotationKeys 按声明序返回七类内置批注 key（转发 mdfile.BuiltinAnnotationKeys）。
func BuiltinAnnotationKeys() []string { return mdfile.BuiltinAnnotationKeys() }

// BuiltinAnnotationLabel 返回内置批注 key 的固定中文标签；非内置返回 ("", false)
// （转发 mdfile.BuiltinAnnotationLabel）。
func BuiltinAnnotationLabel(key string) (string, bool) { return mdfile.BuiltinAnnotationLabel(key) }

// ValidExtensionAnnotationKey 报告 key 是否为合法扩展批注 key（转发 mdfile.ValidExtensionAnnotationKey）。
func ValidExtensionAnnotationKey(key string) bool { return mdfile.ValidExtensionAnnotationKey(key) }

// 覆盖矩阵 disposition 的封闭三值（契约 §4.2.3）：单一字面量真源在 mdfile，转发以便 plan 侧
// note_coverage.go 语义校验复用同一份，不在 plan 再写一遍 "outputs"/"note_only"/"missing"。
const (
	CoverageDispOutputs  = mdfile.CoverageDispOutputs
	CoverageDispNoteOnly = mdfile.CoverageDispNoteOnly
	CoverageDispMissing  = mdfile.CoverageDispMissing
)
