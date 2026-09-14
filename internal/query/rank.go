package query

// [S4] 关系条目的**四级排序键**（M5 索引架构合同 §7.4 + `M-005` 判据 11；T-…-068）。
//
// # 为什么排序要从 scan.go 搬到这里
//
// M2 期 §3.3 只写了两级（type 固定次序 → 对端 ID），第三级 path 是实现里的兜底，
// 三者散在 `SortEdges` 的比较函数里，**看不出「一共几级」**，也就没法反证
// 「不许有第五级」。S4 起索引后端与扫描后端要给出**逐字相同**的顺序（合同 §7.4：
// FTS5 的 `rank` 一律不用，排序必须能在内存里复算），因此把排序键抽成一份
// **具名、有序、封闭**的清单 `sortKeyOrder`，比较函数由清单驱动 ——
// 清单是判据本体：`SortKeyOrder()` 能被逐格比对，第五键在 `ValidateSortKeys` 处当场被拒。
//
// # 四级键（顺序固定，逐字）
//
//	① relation_type —— 固定业务次序 opposing → limits → supports → derives
//	                    （未登记类型排在最后，不按字母序，不静默丢弃）
//	② peer_id       —— 对端 ID 升序（正向比 target、反向比 from）
//	③ path          —— 记录所在文件的 vault 内相对路径升序
//	④ edge_id       —— 同键兜底：条目**输出全等标识**的字典序（**全序封闭点**）
//	                    逐字为 `from|type|target|reason`（次序固定）
//
// 第 ④ 级的存在意义只有一个：**同键稳定**。前三级全部相等时（同一张卡里对同一对端
// 写了两条同型关系），仍必须有一个与输入顺序、与文件系统返回序无关的确定次序，
// 否则「同一语料两次执行输出逐字相同」这条 M2 冻结口径在索引路径上会失守。
//
// # 第 ④ 级为什么必须是「输出全等标识」而不是 `from|target`
//
// 关系条目的可见字段**恰五个**（scan.go 的 RelationEdge：from / type / target / reason /
// path），这五个就是 JSON 里出得去的全部事实。前三级只覆盖 type、对端 ID、path 三格：
// 一张卡里对同一对端写两条同型关系、只有 `reason` 不同（M2 明确「重复条目照实输出、
// 不折叠、不去重」），四级键若只到 `from|target` 就会**判为同键**，两条 reason 不同的
// 条目谁先谁后只能落回 `SliceStable` 的输入顺序 —— 而输入顺序来自 frontmatter 的
// 书写次序 / 索引的行返回次序，两个后端并不保证一致，「索引与扫描逐字等价」当场失守。
//
// 因此第 ④ 级取 `edgeIdentity`：**除 path（③ 已比过）以外的全部可见字段**。
// 由此得到一条可机器反证的性质（`TestRelationSortTieImpliesIdenticalOutput`）：
// 四级键全等 ⇒ 两条条目五格逐字相同 ⇒ 它们的先后**不可能**改变任何输出字节。
// 排序的确定性从此不依赖输入顺序，而是**构造性**的。
//
// # 明确不做
//
//   - 不引入第五级排序键（要加必须回合同补裁决 —— `TestRelationSortRejectsFifthKey`）；
//   - 不改 `SortEntries`（`search` 的四级全序是 M2 §1.4 冻结口径，本文件一格不碰）；
//   - 不改任何可见性 / 诊断语义（那是 card.go / diagnostic.go 的事）；
//   - 不因排序需要去碰索引 schema（T-…-065 的 schema 在本 task 内是硬边界）。

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// 四级排序键的**键名字面量**（对外可判定，用于 sortKeyOrder 与用例逐格比对）。
const (
	// SortKeyRelationType 第 ① 级：关系类型的固定业务次序。
	SortKeyRelationType = "relation_type"
	// SortKeyPeerID 第 ② 级：对端 ID 升序。
	SortKeyPeerID = "peer_id"
	// SortKeyPath 第 ③ 级：记录所在文件路径升序。
	SortKeyPath = "path"
	// SortKeyEdgeID 第 ④ 级：条目输出全等标识 `from|type|target|reason` 的字典序兜底
	// （全序封闭点：四级全等 ⇒ 两条条目逐字相同）。
	SortKeyEdgeID = "edge_id"
)

// sortKeyOrder 是关系排序键的**封闭有序清单**（恰四级，次序即优先级）。
//
// 这份清单是 `M-005` 判据 11 的落点：比较函数不再各写一套 if 链，而是按本清单逐级取
// 比较器（sortKeyCompare），因此「几级、哪几级、什么次序」在源码里只有一处事实。
var sortKeyOrder = []string{
	SortKeyRelationType,
	SortKeyPeerID,
	SortKeyPath,
	SortKeyEdgeID,
}

// SortKeyCount 是排序键的**封闭级数**（恰 4）。第五键必须回合同补裁决，不在代码里加。
const SortKeyCount = 4

// ErrSortKeyRejected 是「排序键集合被越界修改」的哨兵错误（拒绝第五键 / 改名 / 重排）。
var ErrSortKeyRejected = errors.New("关系排序键集合封闭：恰四级，次序固定")

// SortKeyOrder 返回四级排序键的副本（外部拿不到内部切片，改不动本包的排序口径）。
func SortKeyOrder() []string { return append([]string{}, sortKeyOrder...) }

// ValidateSortKeys 判定一份排序键清单是否**恰是**本包封闭的四级键（逐字、同序）。
//
// 任何第五键、任何改名、任何重排一律 wrap ErrSortKeyRejected：
// 排序是「索引与扫描等价」的地基（合同 §7.4），它不允许被某个调用方局部放宽。
func ValidateSortKeys(keys []string) error {
	if len(keys) != SortKeyCount {
		return fmt.Errorf("%w：得到 %d 级 %v，期望恰 %d 级 %v",
			ErrSortKeyRejected, len(keys), keys, SortKeyCount, sortKeyOrder)
	}
	for i, want := range sortKeyOrder {
		if keys[i] != want {
			return fmt.Errorf("%w：第 %d 级 = %q，期望 %q（全清单 %v）",
				ErrSortKeyRejected, i+1, keys[i], want, sortKeyOrder)
		}
	}
	return nil
}

// relationTypeOrder 是合同 §3.3 的固定次序：opposing → limits → supports → derives
// （opposing 最影响判断故首位；该次序不随字母序变化）。
//
// 归位说明：M2 期它在 scan.go，T-…-068 随排序键一并搬到本文件 —— 排序的全部事实
// （几级键、每级怎么比）收在一个文件里，才谈得上「拒绝第五键」的机器反证。
var relationTypeOrder = map[string]int{
	string(model.RelationOpposing): 0,
	string(model.RelationLimits):   1,
	string(model.RelationSupports): 2,
	string(model.RelationDerives):  3,
}

// RelationTypeOrder 返回关系类型的固定次序（副本，供用例逐格比对业务次序）。
func RelationTypeOrder() []string {
	out := make([]string, len(relationTypeOrder))
	for t, i := range relationTypeOrder {
		out[i] = t
	}
	return out
}

// relationTypeRank 给出关系类型的次序值；**未登记类型排在最后**（不按字母序、不丢弃）。
func relationTypeRank(t string) int {
	if r, ok := relationTypeOrder[t]; ok {
		return r
	}
	return len(relationTypeOrder)
}

// edgeIdentity 是一条关系条目的**输出全等标识**：按固定次序列出条目里除 `path` 以外的
// 全部可见字段（`path` 是第 ③ 级排序键，已经比过）。
//
// 「全部可见字段」不是形容词而是可核对的事实：RelationEdge 恰有五格
// （from / type / target / reason / path），本函数取其四，第 ③ 级取剩下那一格。
// 于是「四级键全等」与「两条条目五格逐字相同」是**等价**命题
// （`TestRelationSortTieImpliesIdenticalOutput` 逐格反证），同键先后因此不可能影响输出。
//
// 次序固定为 from → type → target → reason：`reason` 是唯一可能含任意字符的自由文本，
// 放在最后使拼接串（edgeID）不存在分隔歧义 —— 前三格都取自封闭集合
// （ID 是 `k-YYYYMMDD-slug`、type 是封闭枚举 / `replaced_by`），一律不含竖线。
func edgeIdentity(e RelationEdge) []string {
	return []string{e.From, e.Type, e.Target, e.Reason}
}

// EdgeIdentityFields 返回输出全等标识所覆盖的字段名（副本）。
//
// 用例拿它与 RelationEdge 的 JSON 键集合做差集，反证「除 path 外一格不漏」：
// 少覆盖任何一格，第 ④ 级就不再是全序封闭点，同键顺序就会退回依赖输入顺序。
func EdgeIdentityFields() []string { return []string{"from", "type", "target", "reason"} }

// edgeID 是第 ④ 级兜底键的**字面形态**：`from|type|target|reason`。
//
// 只用于诊断文案与用例可读比对；排序本身走 cmpEdgeIdentity 的逐格比较，
// 因此即便 `reason` 里出现竖线也不会与拼接歧义混在一起（两条路的次序判定一致）。
func edgeID(e RelationEdge) string { return strings.Join(edgeIdentity(e), "|") }

// cmpEdgeIdentity 按 edgeIdentity 的固定次序逐格比较（负 = a 在前，0 = 输出全等）。
func cmpEdgeIdentity(a, b RelationEdge) int {
	ia, ib := edgeIdentity(a), edgeIdentity(b)
	for i := range ia {
		if c := cmpString(ia[i], ib[i]); c != 0 {
			return c
		}
	}
	return 0
}

// SameEdgeOutput 报告两条条目是否**五格逐字相同**（即输出字节不可区分）。
//
// 判据 11 的机器反证入口：排序四级全等时本函数必须为真，否则第 ④ 级不是全序封闭点。
func SameEdgeOutput(a, b RelationEdge) bool {
	return a == b // RelationEdge 是纯字符串结构体，== 即逐格全等
}

// sortKeyCompare 是每一级排序键的比较器：负数 = a 在前、正数 = b 在前、0 = 同键。
//
// peerOf 只影响第 ② 级（正向比 target、反向比 from），其余三级与方向无关。
func sortKeyCompare(key string, a, b RelationEdge, peerOf func(RelationEdge) string) int {
	switch key {
	case SortKeyRelationType:
		return cmpInt(relationTypeRank(a.Type), relationTypeRank(b.Type))
	case SortKeyPeerID:
		return cmpString(peerOf(a), peerOf(b))
	case SortKeyPath:
		return cmpString(a.Path, b.Path)
	case SortKeyEdgeID:
		return cmpEdgeIdentity(a, b)
	}
	// 到不了：键集合由 sortKeyOrder 封闭，且 ValidateSortKeys 守着入口。
	// 真到了这里也**不许静默当作同键**（那会让顺序随输入序漂移），故显式判为同键并让
	// 上层的 SortEdgesBy 报错路径去暴露它。
	return 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// SortEdges 就地按**四级键**排序关系条目（合同 §3.3 两级 + §7.4 可复算全序）。
//
// peerOf 给出「对端」：正向比 target、反向比 from。重复条目照实输出、不折叠、不去重。
// 与 M2 口径的关系：前三级逐字沿用（type 固定次序 → 对端 ID → path），第 ④ 级只在
// 前三级**全部相等**时才起作用，因此 M2 / M3 / M4 的既有输出一字不变。
func SortEdges(edges []RelationEdge, peerOf func(RelationEdge) string) {
	SortEdgesBy(edges, peerOf, sortKeyOrder)
}

// SortEdgesBy 是按给定键清单排序的显式形态：清单**必须**恰是封闭的四级键，
// 否则原地退回 SortEdges 的口径并**不做任何排序放宽**（错误由 ValidateSortKeys 暴露给用例）。
//
// 为什么保留这个入口：判据 11 要求「拒绝第五键」可被机器反证 —— 用例传五级键进来，
// 必须拿到 ErrSortKeyRejected，而不是「悄悄按前四级排了」。
func SortEdgesBy(edges []RelationEdge, peerOf func(RelationEdge) string, keys []string) error {
	if err := ValidateSortKeys(keys); err != nil {
		return err
	}
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		for _, key := range keys {
			if c := sortKeyCompare(key, a, b, peerOf); c != 0 {
				return c < 0
			}
		}
		// 四级全等 ⇔ 两条条目五格逐字相同（见 edgeIdentity 的等价性说明）：
		// 此时先后**不改变任何输出字节**，故落回 SliceStable 的输入序是安全的，
		// 「确定性依赖输入顺序」这件事在这里被构造性地排除掉了。
		return false
	})
	return nil
}
