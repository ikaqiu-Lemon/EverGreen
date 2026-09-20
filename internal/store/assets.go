package store

// 结构资产口径转发层（依赖方向 §13：internal/plan 不直连 mdfile）。
// 「一段 Markdown 正文里有哪些结构资产、按什么顺序、各自的稳定签名」这类**文件结构事实**，
// 与分区名 / frontmatter 可解析性同属 mdfile 的只读解析范畴，由本包原样转发给 plan：
// 口径只有 mdfile.ScanAssets 一份定义，plan 只消费本层，绝不自己手写 Markdown 解析。
//
// 本文件不产生任何落盘字节。

import "github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"

// AssetKind / AssetEvent / AssetScanError 是 mdfile 结构资产模型的别名（非另一套类型）：
// 在此再定义同形结构体就得写逐字段拷贝的转换函数，那正是两处漂移的起点。
type AssetKind = mdfile.AssetKind

// 结构资产类别（设计真源 §4.2.1 第 5 条的八类；caption 与 image 分列）。
const (
	AssetImage      = mdfile.AssetImage
	AssetCaption    = mdfile.AssetCaption
	AssetCode       = mdfile.AssetCode
	AssetTableRow   = mdfile.AssetTableRow
	AssetListItem   = mdfile.AssetListItem
	AssetBlockquote = mdfile.AssetBlockquote
	AssetLink       = mdfile.AssetLink
	AssetFootnote   = mdfile.AssetFootnote
)

// AssetEvent 是一条有序结构资产事件（口径见 mdfile.AssetEvent）。
type AssetEvent = mdfile.AssetEvent

// AssetScanError 是结构扫描错误（fail closed 触发点，口径见 mdfile.AssetScanError）。
type AssetScanError = mdfile.AssetScanError

// ScanAssets 只读解析一段 Markdown 正文的结构资产序列（转发 mdfile.ScanAssets）：
// 返回按来源顺序排序的事件序列；遇到无法可靠解析的资产形态返回 *AssetScanError。
func ScanAssets(body []byte) ([]AssetEvent, error) {
	return mdfile.ScanAssets(body)
}
