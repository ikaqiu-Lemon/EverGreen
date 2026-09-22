package cli

// [S4] internal/cli/index_sync.go：`eg index sync` 子命令在本仓的**唯一**落点
// （M5 索引架构合同 §5.3 / §8.1；M5 · T-evergreen.s1_main_flow-158614-066 阶段 B）。
//
// # 为什么单独一个文件
//
// Task 的 `code_paths` 逐字点名 `internal/cli/index_sync.go`：`sync` 是本 task **新增**的
// 命令面能力，而 `index.go` 承载的是 `065` 已冻结的 build / rebuild / status 三支。
// 把新增的收敛入口放在自己的文件里，读代码的人一眼就能分清「哪些是 065 的既有面、
// 哪些是 066 新长出来的面」，review 与后续回溯都不必在一个大文件里辨认改动边界。
//
// # 唯一职责（一句话）
//
// 把「读权威 Markdown → 组装全量现态快照 → 交给 `index.Sync` 收敛 → 如实交代结果」
// 这四步接到命令面上；判定与写盘一格都不在本文件重新实现（那是 `internal/index` 的活）。
//
// # 三条边界（与 index.go 完全同口径，不因为换了文件就松一格）
//
//   - **权威零改动**：写入面**恰一个目录** `.index/`；`domains/` `sources/` `proposals/`
//     `reviews/` 一个字节不碰，也不发 commit。
//   - **退出码只用 {0, 1, 4}**：参数 / 环境问题（扫不动 vault）→ `1`（零写入）；
//     派生索引写盘失败 → `4`（不留半成品，权威恒零改动）。**不启用 `5`**（属 M6）。
//   - **不越界到下游 task**：不接读路径、不产 `Q5`（属 `067`），无排序 / 分页 / `bench`
//     （属 `068`），不引入 `run.lock` / 事务日志（属 M6）。
//
// # 与 `status` 的唯一实现差异
//
// `status` 默认吃 `(size, mtime)` 快路径（省一次 hash 计算），`sync` **恒全量重算
// `content_hash`**：快路径的假阴性只会让一次只读体检说错话，可以下一次纠正；
// 而写入路径一旦吃了假阴性，就会把错的 hash **固化进索引**，之后每次体检都被它骗过。
// 写入路径不接受这种风险。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
)

// runIndexSync 是 `eg index sync`：以**全量现态**把索引收敛到 fresh（合同 §5.3）。
//
// 三支走向全部来自 `index.Sync`，本层只负责「读权威 → 组装快照 → 如实交代」：
//
//	索引 healthy → synced（只重算受影响文件对应的行）或 noop（零写入）
//	索引 missing → 退化全量构建（built + degraded + 留痕 W23）
//	索引 corrupt → 退化整库重建（rebuilt + degraded + 留痕 W24）
//
// M6 · T-…-072 B2c1：sync 与 build / rebuild 同属 **B 类维护命令**，整段「体检 + 快照 +
// 收敛」被 `runIndexCritical` 关进与 plan 写链同一把 `.index/run.lock`，持锁后先过
// 崩溃恢复与运行时保留条目两道屏障。本函数自己不开事务、不碰 Git（见 index_lock.go）。
func (r *Root) runIndexSync(inv *Invocation) (*Result, error) {
	root := inv.VaultRoot
	dir := index.DirPath(root)

	rep := report.New()
	var (
		sres        *index.SyncResult
		after       index.Diagnosis
		blockBefore index.BlockStatus
		blockAfter  index.BlockStatus
	)
	// —— 临界区：进闭包时已持锁、已恢复、保留条目已体检通过 ——
	if err := r.runIndexCritical(inv, &rep, func() error {
		// 先体检再收敛（退化 missing → build / corrupt → rebuild 必须如实留痕），
		// 否则用户会以为自己只是做了一次例行增量。**必须在锁内**：锁外的现态可能
		// 早于本次崩溃恢复，也可能被另一个写者的 S8 索引同步改写。
		before := index.Inspect(dir)

		snap, warnings, err := r.indexSnapshot(root)
		if err != nil {
			return &UsageError{Msg: fmt.Sprintf("读取权威 Markdown 失败（未写一个字节索引）：%v", err)}
		}
		for _, d := range warnings {
			rep.AddWarning(d)
		}
		blockBefore = index.CheckBlocks(dir, snap.Blocks, false)

		sres, err = index.Sync(dir, snap, index.Options{Now: r.Now})
		if err != nil {
			return &CommitFailedError{
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
		// 退化恒留痕（合同 §5.3 最后一条：退化不得静默）。
		if sres.Degraded {
			addIndexDiagnosis(&rep, before)
		}
		if before.Usable() && !blockBefore.Healthy() {
			addBlockDiagnosis(&rep, blockBefore)
		}
		// 收敛后复检同样留在锁内：出了锁再看，看到的可能是别人写的结果。
		after = index.Inspect(dir)
		if !after.Usable() {
			// 收敛完还不健康属实现 bug 级事实：如实登记 warning，绝不静默成功。
			addIndexDiagnosis(&rep, after)
		}
		blockAfter = index.CheckBlocks(dir, snap.Blocks, false)
		if !blockAfter.Healthy() {
			return &CommitFailedError{
				Msg: "派生 block sidecar 同步后对账失败；权威 Markdown 零改动、无 commit",
				Err: fmt.Errorf("%s / %s", blockAfter.Health, blockAfter.Message),
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	rep.AddInfo("eg index sync", report.NonOp, "%s", IndexAuthorityNotice)
	rep.AddInfo("eg index sync", report.NonOp, "%s", IndexNotDoneNotice)

	out := proposalResult(rep, []string{indexSyncSummary(sres, after) +
		fmt.Sprintf("；block sidecar action=%s / notes=%d",
			sres.BlockAction, sres.BlockCount)})
	data := indexSyncData(sres, after)
	data["blocks"] = blockStatusData(blockAfter)
	out.Data["index"] = data
	out.DataOrder = []string{"index", "report"}
	return out, nil
}

// indexSyncData 组装 `data.index`（sync 分支：action / degraded / 三向 diff / 收敛后行数）。
//
// 底座复用 `indexStatusData`（体检口径），因此 sync 与 status / build 的 `data.index`
// 共享同一批键名 —— 键面只增不改，调用方不必按子命令切换解析口径。
func indexSyncData(sres *index.SyncResult, after index.Diagnosis) map[string]interface{} {
	data := indexStatusData(after)
	data["action"] = string(sres.Action)
	data["degraded"] = sres.Degraded
	data["before_head"] = sres.Before.Head
	data["before_files_hash"] = sres.Before.FilesHash
	data["after_head"] = sres.After.Head
	data["after_files_hash"] = sres.After.FilesHash
	data["cards"] = sres.CardCount
	data["relations"] = sres.RelationCount
	data["files"] = sres.FileCount
	data["skipped"] = sres.SkippedCount
	data["blocks_action"] = sres.BlockAction
	data["block_sidecars"] = sres.BlockCount
	data["blocks_changed_added"] = len(sres.BlockChanges.Added)
	data["blocks_changed_modified"] = len(sres.BlockChanges.Modified)
	data["blocks_changed_removed"] = len(sres.BlockChanges.Removed)
	data["blocks_changed_unchanged"] = len(sres.BlockChanges.Unchanged)
	data["dropped_duplicate_cards"] = sres.DroppedDuplicateCards
	data["dropped_duplicate_relations"] = sres.DroppedDuplicateRelations
	addChangeCounts(data, sres.Changes)
	return data
}

// indexSyncSummary 是 sync 的人类可读摘要（退化一律写在明面上）。
func indexSyncSummary(sres *index.SyncResult, after index.Diagnosis) string {
	head := fmt.Sprintf("eg index sync：action=%s", sres.Action)
	switch sres.Action {
	case index.ActionSyncNoop:
		head += "；索引与权威 Markdown 已一致，本次零写入（水位线 " + sres.Before.String() + "）"
	case index.ActionSkipped:
		head += fmt.Sprintf("；索引不可用（%s / %s），本次不动索引", after.Code, after.Reason)
	default:
		head += fmt.Sprintf("；变更 新增 %d / 修改 %d / 删除 %d（未变更 %d 个文件的行原样保留）",
			len(sres.Changes.Added), len(sres.Changes.Modified),
			len(sres.Changes.Removed), len(sres.Changes.Unchanged))
		head += fmt.Sprintf("；水位线 %s → %s", sres.Before, sres.After)
		head += fmt.Sprintf("；收敛后 cards %d / relations %d / files %d",
			sres.CardCount, sres.RelationCount, sres.FileCount)
	}
	if sres.Degraded {
		head += fmt.Sprintf("；已退化（原状态 %s / %s：增量无从下手，本次按全量口径重来）",
			sres.Diagnosis.Health, sres.Diagnosis.Reason)
	}
	if sres.DroppedDuplicateCards > 0 || sres.DroppedDuplicateRelations > 0 {
		head += fmt.Sprintf("；按主键去重丢弃 重复卡 %d / 重复关系 %d（库里的既有病态，"+
			"请跑 eg check 处置，索引不替库治病）",
			sres.DroppedDuplicateCards, sres.DroppedDuplicateRelations)
	}
	head += fmt.Sprintf("；收敛后体检 = %s", after.Health)
	return head + "；权威 Markdown 零改动、commit 无（.index/ 不进 Git）"
}
