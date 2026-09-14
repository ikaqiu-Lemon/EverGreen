package index

// 一致性比对与陈旧诊断 `W22 index_stale`（合同 §5.2 三态判定 + §6.3 诊断码分配）。
//
// **只报不阻断**：本文件不改任何读命令的退出码、不选后端、不做降级 —— 读侧「陈旧就走全量
// 扫描」的实现属 T-…-067。本 task 只负责把「索引现在是 fresh / stale / unusable」这件事
// **算出来并如实交代**。BlocksRead() 恒 false 就是这条约束的机器形态。
//
// 三态与诊断码（合同 §5.2 表 + §6.3 表，逐字）：
//
//	fresh    ：库可用且自洽，且 head 与 files_hash 均与现态一致            → 无码
//	stale    ：库可用且自洽，但 head 或 files_hash 与现态不一致            → W22 index_stale
//	unusable ：缺失 / 打不开 / 版本不匹配 / integrity 非 ok / 自指矛盾 …   → W23 或 W24
//
// **三码互斥**（W22 / W23 / W24 一次最多一条）：本文件的 Code 字段是单值而非集合，
// 互斥因此是**类型层面**的事实，不靠调用方自律（TestConsistencyCodesMutuallyExclusive）。
//
// 明确不做：`Q5 index_degraded`（查询域降级码）属 T-…-067，本包一个字面量都不写。

import (
	"fmt"
)

// CodeIndexStale 是索引陈旧诊断码（合同 §6.3：索引可用但 head 或 files_hash 与现态不一致）。
//
// 退出码影响：**无**。读命令遇到 W22 仍必须退 0（合同 §6.1），它只是一条 warning
// 加一句「请跑 eg index sync」的修复建议。
const CodeIndexStale = "W22"

// Freshness 是索引相对**权威 Markdown 现态**的新鲜度（恰 3 值，合同 §5.2）。
//
// 与 Health（corrupt.go，恰 3 值 healthy/missing/corrupt）的分工：
// Health 只看索引**自身**是否自洽（不需要现态输入）；Freshness 看索引与**现态**的关系。
// 因此 Health=healthy 仍可能 Freshness=stale —— 这正是 T-…-066 要补上的那一格。
type Freshness string

const (
	// FreshnessFresh 索引可用且与现态一致：读路径**可以**用索引后端（用不用属 067）。
	FreshnessFresh Freshness = "fresh"
	// FreshnessStale 索引可用但落后于现态：读结果不可信（可能返回已删卡 / 漏掉新卡），
	// 处置 = `eg index sync`。
	//
	// **取值独立自持，刻意不引用 model.FMKeyStale**（T-…-069 纠正轮，owner 裁决）：
	// 本常量是**索引域的新鲜度令牌**，`model.FMKeyStale` 是**知识卡 frontmatter 的键名**
	// （「综述可能失准」标记，M3 · R6）。两者只是**恰好都写作 `stale` 这个英文词**，语义、
	// 生命周期、变更动机完全无关：frontmatter 键名改名要走数据迁移与历史兼容，索引令牌改名
	// 只是换一个对外字符串。把后者绑到前者上会造出一条**跨领域的假共享**——将来任何一侧改名
	// 都会静默污染另一侧，这是耦合而不是复用。因此本包这里独立声明字面量，且**不 import model**。
	// 取值字节与 T-…-066 落地时逐字相等，`eg index status` 的对外契约零变化。
	// 「`stale` 字面量只允许出现在具名语义所有者处」这条约束改由 TestStaleKeyLiteralsAppearOnce
	// 按**两个所有者双侧精确计数**守（model frontmatter 键名 1 次 + index 新鲜度令牌 1 次）。
	FreshnessStale Freshness = "stale"
	// FreshnessUnusable 索引缺失或不可用：处置 = `eg index build` / `eg index rebuild`。
	FreshnessUnusable Freshness = "unusable"
)

// Freshnesses 返回封闭的新鲜度集合（恰 3 值，次序即合同 §5.2 表的行序）。
func Freshnesses() []string {
	return []string{string(FreshnessFresh), string(FreshnessStale), string(FreshnessUnusable)}
}

// 陈旧子因（封闭集合，恰 3 值）。
//
// 刻意**不并入** corrupt.go 的 Reasons()：那 9 个是「索引坏了」的分因，这 3 个是
// 「索引没坏但落后了」的分因。两个集合的语义域不同，合并会让 `eg index status` 的
// reason 字段失去「坏 / 旧」这一层区分度。
const (
	// StaleReasonHeadMoved 只有 Git HEAD 变了（典型：commit / checkout 后未 sync）。
	StaleReasonHeadMoved = "head_moved"
	// StaleReasonFilesChanged 只有文件内容变了（典型：外部编辑器改了 Markdown 但未提交）。
	StaleReasonFilesChanged = "files_changed"
	// StaleReasonHeadAndFilesChanged 两者都变了（典型：commit 之后又继续编辑）。
	StaleReasonHeadAndFilesChanged = "head_and_files_changed"
)

// StaleReasons 返回封闭的陈旧子因集合（恰 3 值）。
func StaleReasons() []string {
	return []string{StaleReasonHeadMoved, StaleReasonFilesChanged, StaleReasonHeadAndFilesChanged}
}

// Current 是一次比对所需的**权威现态**输入（由调用方扫描 Markdown 得到）。
//
// 索引层不读 Markdown、不跑 git（合同 §13）：Head 来自 `internal/git` 的只读 API，
// Files 来自 M2 的只读扫描底座 + M1 同源的 content_hash。
type Current struct {
	Head  string
	Files []File
	// Cards 是调用方**已经解析好**的权威 Markdown 卡投影（与 build 快照同口径的中性 DTO：
	// id/path/domain/title/status/deprecated/deleted/replaced_by/content_hash 逐列，外加喂 FTS
	// 的 Body）。它是 `Check` 做**行级**权威一致性核对（I-…-024）的唯一输入来源 ——
	// 索引层**不**自己读 Markdown、不自己算 content_hash（§13：依赖方向恒是调用方 → index）。
	//
	// 语义为 **nil = 调用方没提供权威投影 ⇒ 本次不做行级核对**（只做水位线三态判定，与历史一致）；
	// 非 nil（含空切片 = 权威零卡）⇒ 逐行核对 `cards` / `cards_fts` 是否对权威撒谎。
	// 读命令与 `eg index status` 一律提供它（见 cli.indexCurrent / query.probeIndex），
	// 因此真实读路径恒受行级核对保护；只测水位线的单测可省略它、走老口径。
	Cards []Card
}

// Consistency 是一次一致性比对的完整结论（机器可读 + 人类可读双份，不引入第三套事实）。
type Consistency struct {
	// Freshness 是三态结论。
	Freshness Freshness
	// Code 是诊断码：fresh 为空串；stale 为 W22；unusable 为 W23（缺失）或 W24（其余）。
	// 单值字段 ⇒ 三码天然互斥。
	Code string
	// Reason 是机器可读子因：stale 取 StaleReasons()，unusable 取 Reasons()，fresh 为空串。
	Reason string
	// Message 是人类可读说明（含修复建议；与 Reason 同源同事实）。
	Message string
	// Diagnosis 是底层只读体检结论（unusable 时的分因就在里面）。
	Diagnosis Diagnosis
	// Indexed 是索引自认为的水位线；Actual 是现态水位线（unusable 时 Indexed 为零值）。
	Indexed Watermark
	Actual  Watermark
	// Changes 是 stale 时的三向 diff（fresh / unusable 时为零值：前者无变更，后者无从比对）。
	Changes ChangeSet
}

// Fresh 报告索引是否与现态一致。
func (c Consistency) Fresh() bool { return c.Freshness == FreshnessFresh }

// Stale 报告索引是否可用但落后。
func (c Consistency) Stale() bool { return c.Freshness == FreshnessStale }

// Unusable 报告索引是否缺失 / 不可用。
func (c Consistency) Unusable() bool { return c.Freshness == FreshnessUnusable }

// UseIndex 报告「就一致性而言，索引后端是否可信」。
//
// 只有 fresh 为 true：stale 与 unusable 在**读结果**上行为一致（都必须走全量扫描），
// 只在诊断码与修复建议上不同（合同 §5.2 关键裁决：陈旧不允许「先用旧结果再提示」）。
// 后端**实际**怎么选属 T-…-067；这里只给出可信性判断，不做选择。
func (c Consistency) UseIndex() bool { return c.Freshness == FreshnessFresh }

// BlocksRead 恒为 false：索引的任何不健康状态都**不得**阻断读命令，也不得改变退出码
// （合同 §6.1 总纲）。写成方法而不是散落在调用方的注释，是为了让这条约束可被单测直接反证。
func (c Consistency) BlocksRead() bool { return false }

// Check 比对索引与现态，给出三态结论。
//
// 它**永不返回 error**（与 Inspect 同型）：索引不可用 / 陈旧都是诊断而非失败。
// 全程只读：不建库、不改库、不写权威 Markdown（`eg index status [--strict]` 的语义底座）。
//
// 快路径与强校验的分界不在本函数，而在**调用方怎么填 cur.Files**：
//   - 默认路径：`(path,size,mtime)` 命中 QuickUnchanged 时沿用 `files` 表里的 content_hash；
//   - `--strict`：忽略快路径，对全部文件重算 content_hash 后再喂进来。
//
// 两条路径共用本函数这一套判定，因此 strict 与默认**只可能**在「调用方省不省一次 hash
// 计算」上不同，不可能在结论口径上分叉。
func Check(dir string, cur Current) Consistency {
	diag := Inspect(dir)
	actual := WatermarkFrom(cur.Head, cur.Files)
	if !diag.Usable() {
		return Consistency{
			Freshness: FreshnessUnusable,
			Code:      diag.Code,
			Reason:    diag.Reason,
			Message:   diag.Message,
			Diagnosis: diag,
			Actual:    actual,
		}
	}
	indexed := WatermarkOf(diag.Meta)
	if indexed.Equal(actual) {
		// 水位线一致 ⇒ 进入 fresh 候选态。仅当调用方提供了权威卡投影（Current.Cards != nil）
		// 时才做**行级**核对：把 `cards` / `cards_fts` 与权威 Markdown 投影逐列比对，抓出
		// 「库结构合法、水位线不动、card_count 不失配，但派生表内容对权威撒谎」这类行级损坏
		// （I-…-024）。任一不一致统一收在既有 W24 之下（row_level_divergence 子因）。
		// Current.Cards == nil（只测水位线的场景）保持历史口径，不做行级核对。
		if cur.Cards != nil {
			if detail, diverged := checkRowLevel(dir, cur.Cards); diverged {
				corruptDiag := rowLevelCorrupt(diag, detail)
				return Consistency{
					Freshness: FreshnessUnusable,
					Code:      corruptDiag.Code,
					Reason:    corruptDiag.Reason,
					Message:   corruptDiag.Message,
					Diagnosis: corruptDiag,
					Indexed:   indexed,
					Actual:    actual,
				}
			}
		}
		return Consistency{
			Freshness: FreshnessFresh,
			Code:      "",
			Reason:    ReasonNone,
			Message: fmt.Sprintf("索引与权威 Markdown 一致：%s，card_count=%d",
				indexed, diag.Meta.CardCount),
			Diagnosis: diag,
			Indexed:   indexed,
			Actual:    actual,
		}
	}

	// 陈旧：把「差在哪」算清楚再交代。files 表读不出来不影响三态结论（水位线已经判定
	// 不一致），只是拿不到逐文件 diff —— 那时 Changes 留零值，Message 仍如实说明。
	cs := ChangeSet{}
	if indexedFiles, err := ReadFiles(dir); err == nil {
		cs = DiffFiles(indexedFiles, cur.Files)
	}
	return Consistency{
		Freshness: FreshnessStale,
		Code:      CodeIndexStale,
		Reason:    staleReason(indexed, actual),
		Message: fmt.Sprintf(
			"索引已陈旧（%s）：索引记录 %s，现态 %s，%s；索引可用但结果不可信，请跑 eg index sync",
			staleReason(indexed, actual), indexed, actual, cs),
		Diagnosis: diag,
		Indexed:   indexed,
		Actual:    actual,
		Changes:   cs,
	}
}

// staleReason 按「head 变 / files 变 / 都变」三分给出封闭子因。
//
// 调用前置：indexed 与 actual **已确认不相等**，故三支必有一支命中；
// 兜底返回 StaleReasonFilesChanged 只是为了让函数在任何输入下都返回封闭集合内的值。
func staleReason(indexed, actual Watermark) string {
	headMoved := indexed.Head != actual.Head
	filesChanged := indexed.FilesHash != actual.FilesHash
	switch {
	case headMoved && filesChanged:
		return StaleReasonHeadAndFilesChanged
	case headMoved:
		return StaleReasonHeadMoved
	default:
		return StaleReasonFilesChanged
	}
}
