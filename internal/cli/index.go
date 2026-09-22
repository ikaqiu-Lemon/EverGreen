package cli

// [S4] internal/cli/index.go：`eg index` 命令本体在本仓的**唯一**落点
// （M5 索引架构合同 `docs/specs/2026-12-19-m5-index-architecture-contract.md` §8.1；
// M5 · T-evergreen.s1_main_flow-158614-065 建面，**T-…-066 阶段 B** 补齐 `sync` 与陈旧判定）。
//
// # 唯一职责（一句话）
//
// 把 `internal/index` 的四个能力（全量构建 / 无损重建 / 只读体检 / 增量收敛）接到命令面上，
// 并且**由本层**负责读权威 Markdown —— 索引包自己不碰权威文件的读口（§13 禁令第一条）。
//
// # 三条边界（逐条不得越线）
//
//   - **权威零改动**：本文件对 vault 的写入面**恰一个目录** `.index/`（由 index 包写）。
//     `domains/` `sources/` `proposals/` `reviews/` 一个字节都不碰，也**不发 commit**
//     （`.index/` 被 `eg init` 写的 `.gitignore` 整目录忽略，本来就不进 Git）。
//   - **退出码只用 {0, 1, 4}，不启用 5**：`5` 属 M6 强校验，本 task 一格都不提前实现。
//     参数非法 → `1`（`*UsageError`，零写入）；派生索引写盘失败 → `4`（写入侧失败，
//     磁盘保留现状：`.index/` 已被 index 包清成「不存在」，权威 Markdown 恒零改动）。
//     `eg index status` **恒退 0** —— 「索引坏了 / 索引旧了」都是诊断（W22 / W23 / W24），
//     不是一次失败（合同 §6.1；§8.1 status 行逐字要求「含 stale / unusable 也退 0」）。
//   - **派生面分离**：SQLite 继续服务 `search` / `card` / `rel`；Storage v3 的
//     `.index/blocks/` 只加速 `context.draft_candidates`。两者共享维护命令，但不混
//     schema，也不让任一派生故障改变权威 Markdown。
//
// # T-…-066 阶段 B 在本文件的增量（只增不改既有语义）
//
//   - 第 4 个子命令 `sync` 的**注册与分发**在本文件，**实现不在本文件** ——
//     按 Task `code_paths` 落在 `internal/cli/index_sync.go`（以全量现态收敛到 fresh，
//     缺失退化 full build、损坏退化 rebuild，退化一律如实留痕、绝不静默）。
//   - `status` 由「只体检库」升级为「体检库 + 比对权威现态」，因此新增三态中的
//     `stale`（W22）；`--strict` 忽略 `(size, mtime)` 快路径、对全部文件重算
//     `content_hash`（合同 §5.1 / §8.1）。两条路径共用 `index.Check` 同一套判定，
//     只可能在「省不省一次 hash 计算」上不同，不可能在结论口径上分叉。
//   - `status` 仍是**只读**：判陈旧要读一遍权威 Markdown（只读扫描），但不写 `.index/`、
//     不写权威、不落 `eg report --last`。
//
// # 为什么快照在本层组装（而不是让 index 包自己扫）
//
// 「Markdown 是唯一权威来源」这条最高约束要求索引层**只吃已经读好的事实**：
// 扫描底座是 M2 的 `internal/query.VaultScan`（只读、全量、诚实登记 Q 系列诊断），
// `content_hash` 取 M1 `internal/store.ContentHash`（与写口 B3 同源同算法，
// 合同 §5.1 明令索引层不得自定义第二套 hash）。本层把两者投影成 index 包的中性 DTO，
// 于是 index 包既不解析 Markdown、也不依赖 `store` / `query`。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// `eg index` 的子命令名（**恰 4 个**，合同 §8.1 的封闭集合；T-…-066 阶段 B 补齐第 4 个）。
const (
	IndexSubBuild   = "build"
	IndexSubRebuild = "rebuild"
	IndexSubStatus  = "status"
	IndexSubSync    = "sync"
)

// IndexStrictFlag 是 `eg index status` 的**唯一**命令私有 flag：忽略 `(size, mtime)`
// 快路径，对全部文件重算 `content_hash` 再比对（合同 §5.1 强校验路径 / §8.1 status 行）。
//
// 它只对 `status` 有意义，因此挂在其它子命令上一律判非法（退 1、零写入）——
// 「参数在这个子命令上没有语义」必须当场说清楚，不能默默忽略。
const IndexStrictFlag = "strict"

// IndexSubcommands 返回封闭的子命令集合（次序即 `--help` 次序）。
func IndexSubcommands() []string {
	return []string{IndexSubBuild, IndexSubRebuild, IndexSubStatus, IndexSubSync}
}

// IndexAuthorityNotice 是「权威来源」这条最高约束在命令面的固定文案（便于逐字断言）。
const IndexAuthorityNotice = "Markdown 是唯一权威来源，.index/ 只是可重建派生：" +
	"本命令只写 .index/，不改一个字节权威文件、不发 commit；整目录随时可删，删完 eg index build 复原"

// IndexNotDoneNotice 是「本命令这一轮明确没做什么」的诚实交代（未做 ≠ 已做）。
const IndexNotDoneNotice = "SQLite 六表与 index_meta 六键保持不变；.index/blocks/ 是独立可重建派生，" +
	"只服务 context.draft_candidates。任一派生缺失、陈旧或损坏都只诊断并回落权威 Markdown，" +
	"不阻断读取、不改变权威写入结果"

// indexCommand 注册 `eg index`（合同 §8.1：写 `.index/` = 是、写权威 Markdown = 否）。
//
// 参数面**恰 1 个命令私有 flag**（`--strict`，只对 `status` 有语义）：
// `--json` / `--vault` 由 `dispatch` 统一声明；`--limit` 等分页参数属 `068`，不声明 ——
// 传入即由参数解析当场判非法 → 退 1、零写入。
func indexCommand() *Command {
	return &Command{
		Name:        "index",
		Display:     "index build|rebuild|status|sync",
		Summary:     "构建 / 重建 / 只读体检 / 增量收敛派生索引 .index/（Markdown 恒为唯一权威来源）",
		Owner:       "T-evergreen.s1_main_flow-158614-066",
		Subs:        IndexSubcommands(),
		SubRequired: true,
		Flags: func(fs *flagSet) {
			fs.Bool(IndexStrictFlag, false,
				"仅 status：忽略 (size, mtime) 快路径，对全部文件重算 content_hash 再比对（只读）")
		},
		Usage: `eg index build [--json]
eg index rebuild [--json]
eg index status [--strict] [--json]
eg index sync [--json]

子命令（恰四个）：
  build     索引不存在 → 全量构建 SQLite 与 blocks sidecar；已存在且 healthy → no-op；
            已存在但不可用（W24）→ 可恢复重建（先删 .index/ 再全量建，并留痕 W24）
  rebuild   保留 run.lock/txn，删除并重建 SQLite 与 blocks sidecar；结果与全新构建逐字等价
  status    只读体检 + 与权威 Markdown 比对：fresh / stale（W22）/ missing（W23）/
            corrupt（W24）+ index_meta 摘要；**恒退 0** —— 索引旧了 / 坏了是诊断，不是失败
  sync      以权威现态把 SQLite 与 blocks sidecar 收敛到 fresh：只更新受影响项、幂等、
            与全量重建等价；缺失 → 退化全量构建，损坏 → 退化整库重建（如实留痕）

参数：
  --strict  仅 status：忽略 (size, mtime) 快路径，全量重算 content_hash（只读，零写入）

权威与派生：只写 vault 下的 .index/（eg init 已把整目录写进 .gitignore），
domains/ sources/ proposals/ reviews/ 字节零变更，且本命令**不发 commit**。
schema 不兼容一律整库重建，**永不迁移**；索引恒为可重建派生，删掉零信息损失。
退出码：0 成功（status 恒 0） | 1 参数非法（零写入） | 4 派生索引写盘失败（权威零改动） |
        5 run.lock 不可用（E16）/ 写前强校验失败（E15），两者均零写入（build·rebuild·sync；status 只读不取锁）
`,
		// 子命令之外不吃任何位置参数：多余位置参数一律退 1（零写入）。
		// `--strict` 只对 status 有语义：挂在别的子命令上当场判非法，不静默忽略。
		Validate: func(inv *Invocation) error {
			if inv.Sub == "" {
				return &UsageError{Msg: fmt.Sprintf(
					"eg index 必须带子命令：%s", joinPipe(IndexSubcommands()))}
			}
			if inv.Set(IndexStrictFlag) && inv.Sub != IndexSubStatus {
				return &UsageError{Msg: fmt.Sprintf(
					"--%s 只对 eg index %s 有语义（它是「忽略快路径、全量重算 content_hash」的只读开关），"+
						"不能用在 eg index %s 上", IndexStrictFlag, IndexSubStatus, inv.Sub)}
			}
			return noPositionalArgs(inv)
		},
	}
}

// joinPipe 把子命令集合折成 `a | b | c`（单一真源，不抄第二份名单）。
func joinPipe(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += " | "
		}
		out += s
	}
	return out
}

// wireIndex 是 `eg index` 处理函数在本仓的**唯一**挂载点。
//
// 与 `wireCheck` 同一手法：把 Wire 收进本文件，使「索引不作为任何读写命令的前置」
// 这条判据由**文件边界**结构性保证 —— `runIndex` 这个符号在 `internal/cli` 里
// 只出现在 `index.go` / `index_test.go`，root.go 只调用本函数一次。
// 读路径接入索引属 `T-…-067`，那时才会有第二个消费点。
func (r *Root) wireIndex() { _ = r.Wire("index", r.runIndex) }

// runIndex 是 `eg index` 的唯一入口：按子命令分四支，没有第五支。
func (r *Root) runIndex(inv *Invocation) (*Result, error) {
	switch inv.Sub {
	case IndexSubStatus:
		return r.runIndexStatus(inv)
	case IndexSubBuild:
		return r.runIndexBuild(inv, false)
	case IndexSubRebuild:
		return r.runIndexBuild(inv, true)
	case IndexSubSync:
		return r.runIndexSync(inv)
	}
	// 走不到这里：dispatch 已按 Subs 判过子命令。留着是为了「不静默」——
	// 万一注册表与本函数不一致，也必须退 1 并说清楚，而不是当成成功。
	return nil, &UsageError{Msg: fmt.Sprintf("eg index 不支持子命令 %q：恰 %s",
		inv.Sub, joinPipe(IndexSubcommands()))}
}

// runIndexStatus 是 `eg index status [--strict]`：**只读**体检 + 与权威 Markdown 比对，恒退 0。
//
// 只读到底的三条机器形态：① 只调 `index.Check`（内部 `Inspect` 以 `mode=ro` 打开，
// 只读 `files` 表算 diff）；② 扫描权威 Markdown 走 M2 的只读底座 `query.VaultScan`；
// ③ 不落盘最近一次报告（只读命令不改写 `eg report --last` 的事实，与 `eg check` 同口径）。
//
// 默认路径 vs `--strict`：唯一差别是**省不省一次 hash 计算** ——
// 默认允许对 `(path, size, mtime)` 与索引记录完全一致的文件沿用索引里的 `content_hash`
// （合同 §5.1 快路径）；`--strict` 忽略快路径，对全部文件重算 `content_hash`。
// 两条路径的三态判定共用 `index.Check` 同一套代码，因此结论口径不可能分叉。
//
// 本命令**不转述**扫描侧的 Q 系列诊断：索引体检讲的是「索引跟不跟得上权威」，
// 库自身的病态由 `eg check` 负责，status 不替它体检、也不把它的结论搬进自己的报告。
//
// # M6 · T-…-072 B2c1：status 是 C 类，**不经过** runIndexCritical
//
// 三条不得越线（与 build / rebuild / sync 恰好相反）：**不取 run.lock**、**不 Recover**、
// **不创建 `.index/`**。理由是只读命令一旦取锁，`eg index status` 就会被任意一个正在跑的
// 长写事务阻塞住 —— 一条用来「看看现在怎么样」的命令必须在库忙的时候照样能回答；
// 一旦 Recover，它就会替用户回滚权威 Markdown，那是彻头彻尾的写入，与「只读」自相矛盾。
// 代价是它读到的可能是一个正在被改写的中间态，这完全可以接受：结论本来就是**诊断**
// （fresh / stale / missing / corrupt），下一次再看即可纠正，且它恒退 0、不阻断任何人。
func (r *Root) runIndexStatus(inv *Invocation) (*Result, error) {
	root := inv.VaultRoot
	dir := index.DirPath(root)
	strict := inv.Set(IndexStrictFlag)

	cur, err := r.indexCurrent(root, strict)
	if err != nil {
		// 扫不动整个 vault 属**参数 / 环境**问题（例如 --vault 指到了不存在的目录），
		// 不是「索引不健康」：退 1、零写入。索引本身的任何状态仍恒退 0。
		return nil, &UsageError{Msg: fmt.Sprintf("读取权威 Markdown 失败（零写入）：%v", err)}
	}
	con := index.Check(dir, cur)
	blocks := index.CheckBlocks(dir, cur.Blocks, false)

	// 分型计数**现算**（T-005-D）：只从派生表按 `cards.kind` 数，不落 `index_meta` 第七键。
	// 读不到就整格缺席 —— 见 indexKindCounts 的注释。
	kinds, kindsOK := indexKindCounts(dir)

	rep := report.New()
	addConsistencyDiagnosis(&rep, con)
	if con.Code == "" {
		addBlockDiagnosis(&rep, blocks)
	}
	rep.AddInfo("eg index status", report.NonOp, "%s", IndexAuthorityNotice)
	rep.AddInfo("eg index status", report.NonOp, "%s", IndexNotDoneNotice)

	res := proposalResult(rep, []string{
		indexStatusSummary(con, blocks, strict, kinds, kindsOK),
	})
	data := indexConsistencyData(con, strict)
	data["blocks"] = blockStatusData(blocks)
	addIndexKindCounts(data, kinds, kindsOK)
	res.Data["index"] = data
	res.DataOrder = []string{"index", "report"}
	return res, nil
}

// indexKindCounts 现算 `cards.kind` 的分型行数，第二个返回值表示「这份事实读到了没有」。
//
// 为什么不用 `con.Diagnosis.Health` 当门闸而是直接试读：健康度是**结论**，可读性是
// **事实**，两者并不等价 —— 行级不一致（W24 / row_level_divergence）下库结构完全合法、
// 派生表照样读得出来，此时如实报出分型计数比整格消失更有用（它正是用户判断「坏了多少」
// 的依据）；而索引缺失 / 库被截断时连打开都失败，事实确实不存在。因此门闸就取
// 「这次读成不成功」本身。
//
// 读失败**不产诊断码、不改退出码**：status 恒退 0，且库不可读这件事已经由体检结论
// （missing / corrupt）如实说清楚了，再补一条码只会重复计数同一个事实。
func indexKindCounts(dir string) (map[string]int, bool) {
	counts, err := index.CountByKind(dir)
	if err != nil {
		return nil, false
	}
	return counts, true
}

// addIndexKindCounts 把分型计数摊成两个稳定键（读不到就一格都不摊）。
//
// 键名 `knowledge_count` / `opinion_count` 与 `card_count` 同构，且分型令牌逐字取自
// index.CardKind*（不另抄字面量）：`knowledge_count + opinion_count == card_count`
// 因此是一条可由机器逐格复算的守恒式。
func addIndexKindCounts(data map[string]interface{}, counts map[string]int, ok bool) {
	if !ok {
		return
	}
	data[index.CardKindKnowledge+"_count"] = counts[index.CardKindKnowledge]
	data[index.CardKindOpinion+"_count"] = counts[index.CardKindOpinion]
}

// `eg index sync` 的实现落在 internal/cli/index_sync.go（Task code_paths 逐字点名的文件）：
// 本文件只承载 065 已冻结的 build / rebuild / status 三支，新增的收敛入口不混进来。

// indexCurrent 组装一次比对所需的**权威现态**（只读）。
//
// quick=true（默认 status）：对 `(path, size, mtime)` 与索引记录逐格一致的文件沿用
// 索引里的 `content_hash`；quick=false（`--strict` / 写入路径）：全部重算。
//
// **带上权威卡投影 `snap.Cards`**（I-…-024）：`indexSnapshotWith` 已经把权威 Markdown
// 解析成了与 build 同口径的 index.Card（含 Body / content_hash），这里原样交给
// `index.Check` 做**行级**核对 —— 索引层据此抓「库结构合法、水位线一致，但 cards /
// cards_fts 行内容对权威撒谎」这类损坏。丢弃它就等于把 status 的行级体检能力白白关掉。
func (r *Root) indexCurrent(root string, strict bool) (index.Current, error) {
	snap, _, err := r.indexSnapshotWith(root, !strict)
	if err != nil {
		return index.Current{}, err
	}
	return index.Current{
		Head: snap.Head, Files: snap.Files, Cards: snap.Cards, Blocks: snap.Blocks,
	}, nil
}

// runIndexBuild 是 `eg index build` 与 `eg index rebuild` 的共同实现。
//
// 两者只差一件事：`rebuild` 无条件先删再建（`index.Rebuild`），`build` 走三支语义
// （缺失→建 / healthy→no-op / 不可用→可恢复重建，`index.EnsureBuilt`）。
// 其余（快照组装、诊断转述、data 形态）逐字相同，因此不写两份。
//
// M6 · T-…-072 B2c1：两者都是 **B 类维护命令** —— 「体检 + 快照 + 索引写」整段被
// `runIndexCritical` 关进与 plan 写链同一把 `.index/run.lock`，且持锁后先过崩溃恢复
// 与运行时保留条目两道屏障。本函数自己**不**开事务、不碰 Git（见 index_lock.go）。
func (r *Root) runIndexBuild(inv *Invocation, force bool) (*Result, error) {
	root := inv.VaultRoot
	dir := index.DirPath(root)

	rep := report.New()
	var (
		res         *index.Result
		action      index.Action
		after       index.Diagnosis
		blockBefore index.BlockStatus
		blockAction string
		blockCount  int
		blockAfter  index.BlockStatus
	)
	// —— 临界区：进闭包时已持锁、已恢复、保留条目已体检通过 ——
	if err := r.runIndexCritical(inv, &rep, func() error {
		// 体检先于构建：`build` 要靠它决定走哪一支，`rebuild` 要靠它把「原本坏了」如实留痕
		// （否则用户会以为自己只是做了一次例行重建）。**必须在锁内**：锁外读到的现态
		// 可能已被另一个写者的 S8 索引同步改写，也可能早于本次的崩溃恢复。
		before := index.Inspect(dir)

		snap, warnings, err := r.indexSnapshot(root)
		if err != nil {
			// 扫不动整个 vault 属**参数 / 环境**问题（例如 --vault 指到了不存在的目录），
			// 不是索引写盘失败：退 1、零写入。
			return &UsageError{Msg: fmt.Sprintf("读取权威 Markdown 失败（未写一个字节索引）：%v", err)}
		}
		for _, d := range warnings {
			rep.AddWarning(d)
		}
		blockBefore = index.CheckBlocks(dir, snap.Blocks, false)

		if force {
			res, err = index.Rebuild(dir, snap, index.Options{Now: r.Now})
			action = index.ActionRebuilt
		} else {
			res, action, _, err = index.EnsureBuilt(dir, snap, index.Options{Now: r.Now})
		}
		if err != nil {
			// 派生索引写盘失败：index 包已把半成品清掉（索引恒为可重建派生，删掉零信息损失），
			// 权威 Markdown 一个字节都没动。退 4（写入侧失败，磁盘保留现状）。
			return &CommitFailedError{
				// 措辞只说得出口的事实：index 包保证**不留半成品**（半截库文件会被清掉），
				// 但磁盘上那个挡路的东西（比如被外部塞进来的同名文件 / 坏链接）不是本命令该动的，
				// 因此这里不宣称「已清理」，只如实告诉用户「权威没事、障碍要你处置」。
				Msg: fmt.Sprintf("派生索引写盘失败（不留半成品：%s 里不会残留半截库文件；"+
					"权威 Markdown 零改动、无 commit）；清掉磁盘上挡路的同名文件 / 坏链接后"+
					"重跑 eg index rebuild 即可", index.DirName),
				Err: err,
				Diags: []Diagnostic{{
					Code: index.CodeIndexCorrupt, Level: LevelError, Path: index.DirName + "/",
					OpIndex: NonOpDiagnostic, Message: err.Error(),
				}},
			}
		}
		if res != nil {
			blockAction, blockCount = res.BlockAction, res.BlockCount
		} else {
			blockResult, blockErr := index.SyncBlocks(dir, snap.Blocks)
			if blockErr != nil {
				return &CommitFailedError{
					Msg: "派生 block sidecar 写盘失败；权威 Markdown 零改动、无 commit",
					Err: blockErr,
					Diags: []Diagnostic{{
						Code: index.CodeIndexCorrupt, Level: LevelError,
						Path:    index.DirName + "/" + index.BlocksDirName + "/",
						OpIndex: NonOpDiagnostic, Message: blockErr.Error(),
					}},
				}
			}
			blockAction, blockCount = blockResult.Action, blockResult.Count
		}

		// 原本不可用（含 rebuild 时的坏索引）必须留痕：修好了也要说清楚修的是什么。
		if before.Health == index.HealthCorrupt {
			addIndexDiagnosis(&rep, before)
		}
		if before.Usable() && !blockBefore.Healthy() {
			addBlockDiagnosis(&rep, blockBefore)
		}
		// 建完复检同样留在锁内：出了锁再看，看到的可能是别人写的结果。
		after = index.Inspect(dir)
		if !after.Usable() {
			// 建完还不健康属实现 bug 级事实：如实登记 warning，绝不静默成功。
			addIndexDiagnosis(&rep, after)
		}
		blockAfter = index.CheckBlocks(dir, snap.Blocks, false)
		if !blockAfter.Healthy() {
			return &CommitFailedError{
				Msg: "派生 block sidecar 写后对账失败；权威 Markdown 零改动、无 commit",
				Err: fmt.Errorf("%s / %s", blockAfter.Health, blockAfter.Message),
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	rep.AddInfo("eg index "+inv.Sub, report.NonOp, "%s", IndexAuthorityNotice)
	rep.AddInfo("eg index "+inv.Sub, report.NonOp, "%s", IndexNotDoneNotice)

	summary := indexBuildSummary(inv.Sub, action, res, after) +
		fmt.Sprintf("；block sidecar action=%s / notes=%d", blockAction, blockCount)
	out := proposalResult(rep, []string{summary})
	data := indexBuildData(action, res, after)
	data["blocks_action"] = blockAction
	data["blocks"] = blockStatusData(blockAfter)
	out.Data["index"] = data
	out.DataOrder = []string{"index", "report"}
	return out, nil
}

// indexSnapshot 把权威 Markdown 的**只读**全量扫描结果投影成 index 包的中性快照。
//
// 恒**全量重算** `content_hash`：写入路径（build / rebuild / sync / 写后同步）一律走这条 ——
// 快路径的假阴性会把错的 hash 固化进索引，只读体检说错话可以下一次纠正，写入不行。
func (r *Root) indexSnapshot(root string) (index.Snapshot, []report.Diagnostic, error) {
	return r.indexSnapshotWith(root, false)
}

// indexSnapshotWith 是快照组装的唯一实现。
//
// 三件事在这里发生，且只在这里发生：
//
//	① 扫描（`query.VaultScan`，只读）；② `content_hash`（`store.ContentHash`，与 B3 同源）；
//	③ 水位线的 `head`（`internal/git` 只读 API；非 git 仓写空串）。
//
// SQLite 扫描面仍只有 Knowledge/Opinion；Notes 只投影到 `.index/blocks/`，不进入
// files 水位线、表集合或 meta 键集合。两类派生物共享本次只读扫描，但保持独立对账。
//
// quick=true 时启用合同 §5.1 的快路径：`(path, size, mtime)` 与索引记录逐格一致的文件
// 沿用索引里的 `content_hash`（省一次 hash 计算）。**只有默认 `eg index status` 用它**，
// 且它只影响「省不省一次计算」，不改变任何判定口径 —— 最终结论恒以 `content_hash` 为准。
func (r *Root) indexSnapshotWith(root string, quick bool) (index.Snapshot, []report.Diagnostic, error) {
	var warnings []report.Diagnostic
	scan, err := query.VaultScan(root, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		return index.Snapshot{}, nil, err
	}
	// Q 系列诊断原样转述：跳过的文件在扫描层已记 Q1，索引层不得把它们变成静默丢弃。
	for _, d := range scan.Diagnostics {
		warnings = append(warnings, report.Diagnostic{
			Code: d.Code, Level: d.Level, Path: d.Path, OpIndex: report.NonOp, Message: d.Message})
	}

	// 快路径的对照表：读不出来（索引缺失 / 损坏）就退回全量重算 —— 拿不到就不猜。
	indexed := map[string]index.File{}
	if quick {
		if files, ferr := index.ReadFiles(index.DirPath(root)); ferr == nil {
			for _, f := range files {
				indexed[f.Path] = f
			}
		}
	}

	snap := index.Snapshot{Head: indexHead(root)}
	snap.Blocks = query.CandidateBlockDocuments(scan.Notes, store.ContentHash)
	for _, c := range scan.Cards {
		size, mtime := fileStat(filepath.Join(root, filepath.FromSlash(c.Path)))
		// trueHash 是**现态字节**的真 content_hash（store.ContentHash，与 B3 同源）。
		trueHash := store.ContentHash(c.Raw)
		// fileHash 是**水位线**用的 content_hash：默认快路径命中 (size, mtime) 即沿用 files 表
		// 旧值（合同 §5.1，省一次 hash 计算，对等长原地改写天生不敏感）；--strict / 写入路径
		// （quick=false）恒用真值。
		fileHash := trueHash
		if quick {
			if prev, ok := indexed[c.Path]; ok &&
				index.QuickUnchanged(prev, index.File{Path: c.Path, Size: size, MTimeUnix: mtime}) {
				fileHash = prev.ContentHash
			}
		}
		// Cards 投影恒带**真** content_hash：index.Check 的行级核对据此圈定「文件字节确未变」
		// 的作用域（rowlevel.go）—— 快路径若把旧 hash 塞进 Cards，等长改写会被误判成行级撒谎，
		// eg index status 默认路径就会把「陈旧」误报成 W24 损坏。Files 仍走 fileHash（水位线
		// 口径一字不变）。两者仅在「快路径命中但内容其实变了」时相异 —— 那恰是区分「陈旧」与
		// 「撒谎」的关键信号：内容真变了 ⇒ trueHash≠fileHash ⇒ 该卡落在行级核对作用域外（陈旧，
		// 归 --strict / sync）；内容没变而派生行撒谎 ⇒ trueHash==fileHash ⇒ 在作用域内被逐列抓到。
		snap.Cards = append(snap.Cards, index.Card{
			ID: c.ID, Path: c.Path, Domain: c.Domain, Title: c.Title, Status: c.Status,
			Deprecated: c.Deprecated, Deleted: c.Deleted,
			ReplacedBy: c.ReplacedByTarget, Body: c.Body(),
			ContentHash: trueHash, MTimeUnix: mtime,
			// 分型显式给值（Schema v2）：`scan.Cards` 的扫描面恰是知识卡面
			// （`domains/<域>/knowledge/**`），因此这里逐张都是 knowledge、validation 恒空。
			// 索引层不接受空 kind（也不会替调用方兜底成 knowledge），所以「谁在什么扫描面上
			// 取到的行」这件事必须在**取数处**写明 —— 观点面接进快照属后续批次，
			// 那时这里会多出一段 opinion 投影，而不是把本行改成"看情况"。
			Kind: index.CardKindKnowledge, Validation: "",
		})
		snap.Files = append(snap.Files, index.File{
			Path: c.Path, ContentHash: fileHash, Size: size, MTimeUnix: mtime,
		})
		for _, rel := range c.Relations {
			snap.Relations = append(snap.Relations, index.Relation{
				SrcID: c.ID, Verb: string(rel.Type), DstID: string(rel.Target), SrcPath: c.Path,
			})
		}
	}
	// 观点投影（Schema v2·T-…-066-B）：`domains/<域>/opinions/o-*` 与知识卡**同批同口径**
	// 进 cards / cards_fts / files / relations。判别列显式给值——`kind=opinion`、
	// `validation` 取 frontmatter 真值（scan 已解出 OpinionEntry.Validation），不做任何兜底：
	// 索引层的 CHECK 要求 opinion 行 validation ∈ 三值、knowledge 行必空，谁在哪个扫描面上
	// 取到的行就在取数处写明分型（与上面知识卡面的 knowledge/空串对称）。
	//
	// 为什么必须与知识卡面共用同一套 (size, mtime, content_hash) 口径：读路径的
	// `index.Check` 会把 files 表水位线与 cards 行级投影一起对账，若写入侧观点的 content_hash
	// 与读路径投影不同源，含观点的库会被误判成陈旧 / 行级撒谎，`eg search` 每次都降级。
	for _, o := range scan.Opinions {
		size, mtime := fileStat(filepath.Join(root, filepath.FromSlash(o.Path)))
		trueHash := store.ContentHash(o.Raw)
		fileHash := trueHash
		if quick {
			if prev, ok := indexed[o.Path]; ok &&
				index.QuickUnchanged(prev, index.File{Path: o.Path, Size: size, MTimeUnix: mtime}) {
				fileHash = prev.ContentHash
			}
		}
		snap.Cards = append(snap.Cards, index.Card{
			ID: o.ID, Path: o.Path, Domain: o.Domain, Title: o.Title, Status: o.Status,
			Deprecated: o.Deprecated, Deleted: o.Deleted,
			ReplacedBy: o.ReplacedByTarget, Body: o.Body(),
			ContentHash: trueHash, MTimeUnix: mtime,
			Kind: index.CardKindOpinion, Validation: o.Validation,
		})
		snap.Files = append(snap.Files, index.File{
			Path: o.Path, ContentHash: fileHash, Size: size, MTimeUnix: mtime,
		})
		for _, rel := range o.Relations {
			snap.Relations = append(snap.Relations, index.Relation{
				SrcID: o.ID, Verb: string(rel.Type), DstID: string(rel.Target), SrcPath: o.Path,
			})
		}
	}
	return snap, warnings, nil
}

// fileStat 返回文件大小与 mtime（读不到即 0）。
//
// 两者按合同 §5.1 **只作快路径过滤**，绝不单独作为「未变更」的最终结论 ——
// 最终结论恒以 `content_hash` 为准，因此这里 stat 失败也不算错误。
func fileStat(abs string) (int64, int64) {
	st, err := os.Stat(abs)
	if err != nil {
		return 0, 0
	}
	return st.Size(), st.ModTime().UTC().Unix()
}

// readIndexDeps 组装读路径（`eg search` / `eg card show` / `eg rel`）的 A-44 水位线口径
// （S4，T-…-067；对应 query.IndexDeps）。
//
// 两处口径都**引用既有单点**，不新造实现：
//
//	① content_hash = `store.ContentHash`（M1 写口 B3 同源同算法，合同 §5.1 末段
//	   逐字禁止第二套 hash）；
//	② head        = 本文件的 `indexHead`（`internal/git` 只读 API，取不到即空串）——
//	   与 `eg index build/sync/status` 组装快照时**同一个函数**。
//
// 因此读路径判「索引是否跟得上权威」与 `eg index status`（默认快路径）不可能分叉：
// 同一套 hash、同一个 head、同一个 `index.Check`。
//
// 为什么由 cli 注入：`internal/query` 依施工索引 §13 不得 import `store` / `git`。
// 这与 `eg context` 注入 Hasher 是同一套既有做法，不新增机制。
func readIndexDeps(root string) query.IndexDeps {
	return query.IndexDeps{
		Hash: store.ContentHash,
		Head: func() string { return indexHead(root) },
	}
}

// indexHead 读 Git HEAD（只读）。非 git 仓 / 空仓一律空串 —— 水位线允许 head 为空
// （合同 §4.2），空串本身就是一个明确的状态，不是错误。
func indexHead(root string) string {
	head, err := git.New(root).Head()
	if err != nil {
		return ""
	}
	return head
}

// addIndexDiagnosis 把索引诊断（W23 / W24）转成报告 warning。
//
// healthy 不产 warning：没有事实就不造条目。
func addIndexDiagnosis(rep *report.Report, diag index.Diagnosis) {
	if diag.Code == "" {
		return
	}
	rep.AddWarning(report.Diagnostic{
		Code: diag.Code, Level: report.LevelWarning, Path: index.DirName + "/",
		OpIndex: report.NonOp, Message: diag.Message,
	})
}

// addConsistencyDiagnosis 把一致性结论（W22 陈旧 / W23 缺失 / W24 损坏）转成报告 warning。
//
// fresh 不产 warning（没有事实就不造条目）；三码互斥由 `Consistency.Code` 的单值性保证。
func addConsistencyDiagnosis(rep *report.Report, con index.Consistency) {
	if con.Code == "" {
		return
	}
	rep.AddWarning(report.Diagnostic{
		Code: con.Code, Level: report.LevelWarning, Path: index.DirName + "/",
		OpIndex: report.NonOp, Message: con.Message,
	})
}

func addBlockDiagnosis(rep *report.Report, status index.BlockStatus) {
	if status.Code == "" {
		return
	}
	rep.AddWarning(report.Diagnostic{
		Code: status.Code, Level: report.LevelWarning,
		Path:    index.DirName + "/" + index.BlocksDirName + "/",
		OpIndex: report.NonOp, Message: status.Message,
	})
}

func blockStatusData(status index.BlockStatus) map[string]interface{} {
	return map[string]interface{}{
		"health":            status.Health,
		"code":              status.Code,
		"reason":            status.Reason,
		"message":           status.Message,
		"usable":            status.Healthy(),
		"documents":         len(status.Documents),
		"changed_added":     len(status.Changes.Added),
		"changed_modified":  len(status.Changes.Modified),
		"changed_removed":   len(status.Changes.Removed),
		"changed_unchanged": len(status.Changes.Unchanged),
		"schema_version":    index.BlockSidecarVersion,
	}
}

// indexStatusData 组装 `data.index` 的**公共底座**（体检口径：health / code / reason / …）。
func indexStatusData(diag index.Diagnosis) map[string]interface{} {
	data := map[string]interface{}{
		"health":  string(diag.Health),
		"code":    diag.Code,
		"reason":  diag.Reason,
		"message": diag.Message,
		"usable":  diag.Usable(),
		"db_path": index.DirName + "/" + index.DBFileName,
	}
	addIndexMeta(data, diag)
	return data
}

// indexConsistencyData 组装 `data.index`（status 分支：三态 + 现态水位线 + 三向 diff 计数）。
//
// 键面对 T-…-065 只增不改：`health` / `usable` 仍是**库体检**口径，
// `code` / `reason` / `message` 升级为**一致性**口径（fresh 空串、stale W22、unusable W23/W24）——
// 因为「索引旧了」与「索引坏了」对用户是同一个问题域：这份索引现在能不能信。
func indexConsistencyData(con index.Consistency, strict bool) map[string]interface{} {
	data := indexStatusData(con.Diagnosis)
	data["code"] = con.Code
	data["reason"] = con.Reason
	data["message"] = con.Message
	data["freshness"] = string(con.Freshness)
	data["fresh"] = con.Fresh()
	// 键名取自 index.FreshnessStale 而非裸字面量：这一格报的是**索引新鲜度**是否为 stale，
	// 与 index 域的令牌**同一个语义**，因此引用同域常量是正当复用（不是跨域借词）。
	// 与 internal/model 的 frontmatter `stale` 键**无关**：那是知识卡「综述可能失准」标记，
	// 命名撞词而语义不同，两边各自具名、各自演进（T-…-069 纠正轮 owner 裁决）。
	data[string(index.FreshnessStale)] = con.Stale()
	data["strict"] = strict
	// 陈旧 / 不可用都**不阻断读**（合同 §6.1）：把这条写成机器可读的一格，
	// 而不是只写在文档里 —— 读侧真正怎么降级属 T-…-067。
	data["blocks_read"] = con.BlocksRead()
	data["use_index"] = con.UseIndex()
	data["actual_head"] = con.Actual.Head
	data["actual_files_hash"] = con.Actual.FilesHash
	addChangeCounts(data, con.Changes)
	return data
}

// addChangeCounts 把三向 diff 摊成四个计数（新增 / 修改 / 删除 / 未变更）。
//
// 只摊计数不摊路径清单：路径清单可能有几千条，摘要与 data 都不该被它撑爆；
// 需要逐路径的场合有 `eg check` 与 `eg index status --strict` 的 message。
func addChangeCounts(data map[string]interface{}, cs index.ChangeSet) {
	data["changed_added"] = len(cs.Added)
	data["changed_modified"] = len(cs.Modified)
	data["changed_removed"] = len(cs.Removed)
	data["changed_unchanged"] = len(cs.Unchanged)
}

// indexBuildData 组装 `data.index`（build / rebuild 分支：多 action 与本次写入计数）。
func indexBuildData(action index.Action, res *index.Result, after index.Diagnosis) map[string]interface{} {
	data := indexStatusData(after)
	data["action"] = string(action)
	if res == nil {
		// no-op 分支（索引原本 healthy）：**没有**本次写入计数这一格 ——
		// 造一个 0 会让读者以为「本次写了 0 张卡」，而事实是「本次一个字节都没写」。
		return data
	}
	data["cards"] = res.CardCount
	data["relations"] = res.RelationCount
	data["files"] = res.FileCount
	data["skipped"] = res.SkippedCount
	data["block_sidecars"] = res.BlockCount
	data["dropped_duplicate_cards"] = res.DroppedDuplicateCards
	data["dropped_duplicate_relations"] = res.DroppedDuplicateRelations
	return data
}

// addIndexMeta 把 `index_meta` 六键摊进 data（读不到就不摊 —— 不拿零值冒充事实）。
func addIndexMeta(data map[string]interface{}, diag index.Diagnosis) {
	data["meta_readable"] = diag.MetaReadable
	if !diag.MetaReadable {
		return
	}
	data["schema_version"] = diag.Meta.SchemaVersion
	data["head"] = diag.Meta.Head
	data["files_hash"] = diag.Meta.FilesHash
	data["tokenizer_mode"] = diag.Meta.TokenizerMode
	data["built_at_unix"] = diag.Meta.BuiltAtUnix
	data["card_count"] = diag.Meta.CardCount
}

// indexStatusSummary 是 status 的人类可读摘要（事实全部能从 data.index 复述）。
//
// kinds / kindsOK 是现算的分型计数：读到了就与机器输出**同源同事实**地一并显示
// （人读一侧少一格，用户就得去翻 JSON 才能知道库里知识卡与观点各有多少）；读不到
// 则一个字都不提 —— 与 data 侧「整格缺席」的口径一致，不在摘要里拿 0 兜底。
func indexStatusSummary(con index.Consistency, blocks index.BlockStatus, strict bool,
	kinds map[string]int, kindsOK bool) string {
	diag := con.Diagnosis
	head := fmt.Sprintf("索引体检：%s / %s", diag.Health, con.Freshness)
	if con.Code != "" {
		head += fmt.Sprintf("（%s / %s）", con.Code, con.Reason)
	}
	head += "；" + con.Message
	if diag.MetaReadable {
		head += fmt.Sprintf("；schema_version=%d、card_count=%d、tokenizer_mode=%s",
			diag.Meta.SchemaVersion, diag.Meta.CardCount, diag.Meta.TokenizerMode)
	}
	if kindsOK {
		head += fmt.Sprintf("；%s_count=%d、%s_count=%d",
			index.CardKindKnowledge, kinds[index.CardKindKnowledge],
			index.CardKindOpinion, kinds[index.CardKindOpinion])
	}
	head += fmt.Sprintf("；block sidecar=%s（notes=%d）",
		blocks.Health, len(blocks.Documents))
	if strict {
		head += "；--strict：已忽略 (size, mtime) 快路径，全部文件重算 content_hash"
	} else {
		head += "；默认路径：(size, mtime) 命中即沿用索引里的 content_hash（--strict 可全量重算）"
	}
	if !con.Fresh() {
		// 「不新鲜」与「拦不拦读」是两件事：把后者一并说清楚，避免用户以为检索被锁了。
		head += "；陈旧 / 不可用只报不阻断读（读侧降级属 T-…-067）"
	}
	return head + "；只读命令：零写入、commit 无"
}

// indexBuildSummary 是 build / rebuild 的人类可读摘要。
func indexBuildSummary(sub string, action index.Action, res *index.Result, after index.Diagnosis) string {
	head := fmt.Sprintf("eg index %s：action=%s", sub, action)
	if res == nil {
		return head + "；索引原本健康，本次零写入（不重建、不产生任何新字节）；权威 Markdown 零改动"
	}
	head += fmt.Sprintf("；已写入 cards %d / relations %d / files %d（tokenizer_mode=%s）",
		res.CardCount, res.RelationCount, res.FileCount, res.Meta.TokenizerMode)
	if res.DroppedDuplicateCards > 0 || res.DroppedDuplicateRelations > 0 {
		head += fmt.Sprintf("；按主键去重丢弃 重复卡 %d / 重复关系 %d（库里的既有病态，"+
			"请跑 eg check 处置，索引不替库治病）",
			res.DroppedDuplicateCards, res.DroppedDuplicateRelations)
	}
	head += fmt.Sprintf("；建完体检 = %s", after.Health)
	return head + "；权威 Markdown 零改动、commit 无（.index/ 不进 Git）"
}

// indexErrIsExists 判定错误是否为「索引已存在」（供用例断言 build 不覆盖既有库）。
// 保留一个显式判定点，避免调用方去比对错误文案。
func indexErrIsExists(err error) bool { return errors.Is(err, index.ErrIndexExists) }
