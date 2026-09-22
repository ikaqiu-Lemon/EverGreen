package cli

// [S4] internal/cli/index_after_write.go：**写后索引同步**在本仓的唯一落点
// （M5 索引架构合同 §5.3 / §7.2；M5 · T-evergreen.s1_main_flow-158614-066 阶段 B）。
//
// # 唯一职责（一句话）
//
// 在写命令**已经把 Markdown 落盘（必要时已 commit 成功）之后**，把这次受影响的路径
// 挂给 `index.Apply`，让派生索引跟上权威 —— 六条写命令共用本文件这**一个** helper，
// 不在六处各抄一份。
//
// # 四条不得越线的边界
//
//   - **权威优先，索引垫后**：本 helper 只可能在写入成功之后被调用。它绝不回滚、
//     绝不重试写入、绝不因为「索引没同步好」而改变命令的退出码或 JSON 键面 ——
//     索引是可重建派生，Markdown 才是唯一权威（合同 §2 / §5.3）。
//   - **只追加既有诊断结构**：出问题一律走 `report.Diagnostic`（W22 / W23 / W24）与
//     `AddInfo`，不新增 data 键、不动 `data.report` 之外的形态。因此接上它之后，
//     写命令的合同产物（`data.reconcile`、`data.cards`、`report` 体…）逐字不变。
//   - **不替用户建索引**：索引缺失（W23）时**静默跳过** —— 没建索引是完全正常的状态
//     （索引不是任何命令的前置，合同 §0.1），一次 `eg capture` 不该悄悄给用户
//     造出一个几十 MB 的库；索引损坏（W24）时如实报 warning 并跳过，等用户显式
//     `eg index rebuild`（自动修复只会掩盖「库为什么坏了」这件事）。
//   - **不接读路径**：本文件只在写后被调用，`search` / `card` / `rel` 的读路径一格不碰
//     （属 T-…-067），也不产 `Q5`。
//
// # 为什么用「全量扫描 + 只交受影响行」而不是「只解析受影响文件」
//
// 快照组装是 `indexSnapshot` 这唯一一处（扫描走 M2 只读底座、`content_hash` 走 M1 同源
// 算法）。若这里另起一套「只解析这几个文件」的解析路径，就等于把权威解析口径抄成第二份 ——
// 一旦两份漂移，索引里的行会与 `eg index rebuild` 的结果不一致，而这恰恰是 M5 最不能
// 出问题的地方（§5.3「增量结果必须与全量重建等价」）。因此：**读权威全量、写索引只写
// 受影响行**。扫描开销的优化属 T-…-068（性能门槛），不在本 task 提前动手。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// IndexAfterWriteSkipMissingNotice 是「索引缺失 → 静默跳过」这条决定的固定文案
// （供用例逐字断言：跳过是**明写的决定**，不是忘了写代码）。
const IndexAfterWriteSkipMissingNotice = "派生索引尚未构建（.index/ 不存在）：" +
	"写命令不替用户建索引（索引不是任何命令的前置），需要检索加速时显式跑 eg index build"

// syncIndexAfterWrite 是六条写命令（apply / edit → runPlan、delete、undelete、
// mark-reviewed、reconcile）**唯一**的写后索引同步入口。
//
// 调用前置（调用方必须已满足，否则就是把「未完成」当成「已完成」）：
//
//	① 本次要写的 Markdown 已经落盘；② 如该命令带 commit，则 commit 已成功；
//	③ written 是本次**实际写过**的 vault 内相对路径（斜杠分隔）。
//
// 行为（任何一支都不返回 error —— 它没有失败可言，只有「同步了 / 没同步 + 为什么」）：
//
//	索引缺失（W23）        → 静默跳过（不建库、不报警：没建索引是正常状态）
//	索引损坏（W24）        → 报 W24 warning + 建议 rebuild，不动索引
//	扫描权威失败           → 报 W22 warning + 建议 eg index sync
//	索引写入失败           → 报 W22 warning + 建议 eg index sync（权威已落盘，绝不回滚）
//	同步成功 / 无需同步    → 进 info（action / 变更计数 / 水位线推进，如实交代）
func (r *Root) syncIndexAfterWrite(rep *report.Report, root, cmd string, written []string) {
	if rep == nil || root == "" || len(written) == 0 {
		return
	}
	dir := index.DirPath(root)
	diag := index.Inspect(dir)
	switch diag.Health {
	case index.HealthMissing:
		// 静默：见文件头第三条边界。这里刻意**不**产 W23 —— 把「你没建索引」变成每次写入
		// 都要看一眼的警告，会让真正的警告淹死在噪声里。
		return
	case index.HealthCorrupt:
		rep.AddWarning(report.Diagnostic{
			Code: diag.Code, Level: report.LevelWarning, Path: index.DirName + "/",
			OpIndex: report.NonOp,
			Message: fmt.Sprintf("%s：本次 %s 的写入**已成功落盘**，但派生索引不可用，"+
				"因此未做写后同步（不自动修复：先跑 eg index rebuild 重建，"+
				"权威 Markdown 不受影响）", diag.Message, cmd),
		})
		return
	}

	snap, _, err := r.indexSnapshot(root)
	if err != nil {
		// 扫描侧的 Q 系列诊断刻意不转述：写命令的报告讲的是这次写入，
		// 不该因为顺带同步了一次索引，就把全库既有病态搬进来（那是 eg check 的活）。
		r.noteIndexNotSynced(rep, cmd, fmt.Sprintf("读取权威 Markdown 失败：%v", err))
		return
	}

	sres, aerr := index.Apply(dir, indexDeltaFor(snap, written), index.Options{Now: r.Now})
	if aerr != nil {
		r.noteIndexNotSynced(rep, cmd, fmt.Sprintf("派生索引写入失败：%v", aerr))
		return
	}
	if sres.Action == index.ActionSkipped {
		// 竞态：Inspect 之后索引被外部弄坏 / 删掉。如实说，不假装同步过。
		r.noteIndexNotSynced(rep, cmd, fmt.Sprintf("索引在本次同步期间变为不可用（%s / %s）",
			sres.Diagnosis.Health, sres.Diagnosis.Reason))
		return
	}
	rep.AddInfo(index.DirName+"/", report.NonOp,
		"写后索引同步：action=%s；变更 新增 %d / 修改 %d / 删除 %d（未变更 %d 个文件的行原样保留）；"+
			"水位线 %s → %s；Markdown 恒为唯一权威，索引只是可重建派生",
		sres.Action, len(sres.Changes.Added), len(sres.Changes.Modified),
		len(sres.Changes.Removed), len(sres.Changes.Unchanged), sres.Before, sres.After)
}

// syncResultIndex adapts the shared report-based index synchronizer for
// direct commands whose diagnostics live on Result.
func (r *Root) syncResultIndex(res *Result, inv *Invocation, root string, written []string) {
	var scratch report.Report
	r.syncIndexAfterWrite(&scratch, root, indexWriteLabel(inv), written)
	for _, d := range scratch.Warnings {
		res.Warnings = append(res.Warnings, Diagnostic{
			Code: d.Code, Level: d.Level, Path: d.Path,
			OpIndex: NonOpDiagnostic, Message: d.Message,
		})
	}
}

// noteIndexNotSynced 把「本次没同步上」统一降级成一条 W22 提示。
//
// 用 W22（index_stale）而不是造新码：从用户视角，「索引没跟上权威」就是陈旧这件事，
// 修法也只有一条 —— `eg index sync`。退出码一格不动（合同 §6.1：索引问题不改退出码）。
func (r *Root) noteIndexNotSynced(rep *report.Report, cmd, why string) {
	rep.AddWarning(report.Diagnostic{
		Code: index.CodeIndexStale, Level: report.LevelWarning, Path: index.DirName + "/",
		OpIndex: report.NonOp,
		Message: fmt.Sprintf("%s 的写入**已成功落盘**，但派生索引未能同步（%s）："+
			"索引现已陈旧，跑 eg index sync 即可收敛；权威 Markdown 与本次 commit 不受影响"+
			"（索引问题不改变退出码，也不回滚任何已写入内容）", cmd, why),
	})
}

// indexDeltaFor 从**全量现态快照**里切出「只含本次受影响路径」的 Delta。
//
// 三件事：
//
//	① 只留索引对象面路径：索引的对象面是卡与观点（`domains/<d>/(knowledge|opinions)/*.md`
//	   同批同口径进 cards / cards_fts / relations 三表），写命令顺带写的提案 / 评审 / 原文 /
//	   笔记不在索引里，不该被算成「受影响行」；
//	② 现态里还在的路径 → 取它的现态行（新增 / 修改同一条口径）；
//	   现态里已经没有的路径 → 进 Removed（逻辑删除不删文件，因此这一支通常为空，
//	   但重命名 / 外部删除必须能收敛，不能靠「大概不会发生」兜着）；
//	③ Head 恒取本次落盘后的 HEAD —— 即使一张卡都没改（比如只写了提案），
//	   commit 也会让 HEAD 移动，水位线必须跟着推进，否则下一次 status 会把索引误判成陈旧。
func indexDeltaFor(snap index.Snapshot, written []string) index.Delta {
	affected := map[string]bool{}
	for _, p := range written {
		if rel := strings.TrimSpace(p); rel != "" && isIndexedRel(rel) {
			affected[rel] = true
		}
	}
	d := index.Delta{Head: snap.Head}
	affectedBlocks := map[string]bool{}
	for _, rel := range written {
		if isNoteRel(rel) {
			affectedBlocks[rel] = true
		}
	}
	for _, block := range snap.Blocks {
		if affectedBlocks[block.NotePath] {
			d.Blocks = append(d.Blocks, block)
		}
	}
	present := map[string]bool{}
	for _, f := range snap.Files {
		if affected[f.Path] {
			present[f.Path] = true
			d.Files = append(d.Files, f)
		}
	}
	for _, c := range snap.Cards {
		if affected[c.Path] {
			d.Cards = append(d.Cards, c)
		}
	}
	for _, rel := range snap.Relations {
		if affected[rel.SrcPath] {
			d.Relations = append(d.Relations, rel)
		}
	}
	for p := range affected {
		if !present[p] {
			d.Removed = append(d.Removed, p)
		}
	}
	return d
}

// isIndexedRel 判定一个 vault 内相对路径是否落在索引对象面上：知识卡与观点两类同批入库
// （`domains/<domain>/knowledge/<id>.md` 与 `domains/<domain>/opinions/<id>.md`）。
//
// 口径与 indexSnapshotWith 的扫描面同源：cards / cards_fts / relations 三表以卡与观点为单位。
// Note / Source / Proposal 仍排除（它们不进索引，写命令顺带写到它们不算「受影响行」）。
// 刻意不做「文件是否真的存在」的判断 —— 那是调用方给的事实，本函数只管形态。
func isIndexedRel(rel string) bool {
	parts := strings.Split(strings.TrimPrefix(rel, "./"), "/")
	if len(parts) != 4 || parts[0] != store.DirDomains || !strings.HasSuffix(parts[3], ".md") {
		return false
	}
	return parts[2] == store.DirKnowledge || parts[2] == store.DirOpinions
}

func isNoteRel(rel string) bool {
	parts := strings.Split(strings.TrimPrefix(rel, "./"), "/")
	return len(parts) == 4 && parts[0] == store.DirDomains &&
		parts[2] == store.DirNotes && strings.HasSuffix(parts[3], ".md")
}

// indexWriteLabel 是诊断文案里的命令名（`eg edit`、`eg rel add`…）。
//
// 只用于**措辞**：诊断码、退出码、data 键面都与它无关。取 `Invocation` 而不是写死字符串，
// 是为了让 runPlan 这一个共同写口在六条命令下都说得出自己是谁。
func indexWriteLabel(inv *Invocation) string {
	if inv == nil || inv.Cmd == nil {
		return "写命令"
	}
	label := "eg " + inv.Cmd.Name
	if inv.Sub != "" {
		label += " " + inv.Sub
	}
	return label
}
