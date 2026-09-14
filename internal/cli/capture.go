package cli

// eg capture 的业务实现（合同 §1.3；技术方案 §7.5 最小收录合同）。
//
// **CLI 不做任何网络请求**：抓网页、解析 HTML、清洗正文全在 Agent 侧，
// 本命令只把「已经拿到的正文字节」确定性地落成原文文件 + 收件区条目。
//
// 判重（EG-SRC-01）：URL 规范化后精确匹配，其次标题精确匹配；命中任一即同一篇 →
// 复用已有原文、不产生第二份、正文不覆盖、`--reason` 追加到收录理由列表。
// 已有材料笔记默认不重复加工（返回 has_note + note_id 并提示跳过），仅 --reprocess 放行。
//
// 一次 capture = 一次 commit（verb=capture）；重复执行的结果是幂等的——至多一份原文、
// 至多一条未处理条目，干净工作区下不产生空 commit。
//
// 措辞登记（T-…-026）：本段原文用「重复执行收敛<全角冒号>」描述幂等性，与逐卡收敛记录
// 无关，但会被「唯一渲染实现」的 grep 反证误判，故改写为「重复执行的结果是幂等的」。

// 事务化（M6 · T-…-072 批次 C1；合同 §2「事务边界与临界区 S0~S9」）：
//
//	S0  正文 / 参数 / stamp 解析              —— **锁外**（不触碰库内任何权威文件）
//	S1  txn.Acquire(vault/.index/run.lock)    —— 临界区开始；W28 进 Result.Warnings
//	S2  txn.Recover                            —— 临界区内第一件事；真实回滚产 W26
//	S3  ScanSources / FindDuplicate / NoteOf   —— **全部锁内重做**：S2 可能刚把文件回滚到
//	    前像，在我们拿到锁之前也可能有别的写者刚提交，锁外算出的判重结论必然可能过期
//	S4  BeginAtomic + 现有 ApplyXxx 写口        —— 内存预演，实盘零变化，得 accepted write-set
//	S5  AllocateTxnID → RecordTxnID → WriteIntent（仅当 write-set 非空）
//	S6  txn.Commit                             —— commit marker 在盘，Markdown 才算生效
//	S7  git commit                             —— 严格晚于 commit marker、仍持同一把锁；
//	    失败退 4、Markdown 保持目标态、**不回滚、不做第二次权威写**
//	S8  syncIndexAfterWrite                    —— 仍在同一把锁内、S7 之后、Release 之前；
//	    Git 成败都要走（成功推进了 HEAD，失败也已由 commit marker 改变了权威内容），
//	    索引问题只产 W22 / W24，不改退出码、不回滚、不二次写权威
//	S9  Release                                —— data / summary 渲染在**锁外**
//
// 两条边界与 plan 写链逐字同源：
//   - **零 accepted write-set 不开事务**：判重命中且理由已在列表、条目又已 detach 时
//     本次一个字节都不写 —— 不分配 txn_id、不发 intent、不跑 Git、不造空 commit。
//   - **txn_id 的审计边界是「分配成功」**（A-59）：号码一旦到手就必进 `data.txn_id`，
//     此后 intent 失败 / 提交失败 / Git 失败 / 成功四种结局都不撤销；分配之前失败
//     （锁失败、恢复阻断、参数错误、B3 跳过、零 write-set）则**省略**该键。

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// MinBodySize 是正文（去首尾空白后）的最小字节数：低于此值视为「正文为空或过短」，
// 判 error 退 2 零写入（§7.5）。阈值刻意取小：eg 不判断正文质量，只拦截明显的空投喂。
const MinBodySize = 8

// TruncationHintSize 是「正文疑似截断」的 warning 阈值（字节）。只出 warning，收录照常完成。
const TruncationHintSize = 200

// TitleMaxLen 是「标题异常」的 warning 阈值（字节）。
const TitleMaxLen = 200

// captureOutcome 是**临界区内**产出的全部事实，供锁外渲染 data / summary。
//
// 之所以要专门带出来：`data.warnings[]` 必须是本次的**全部**诊断（含 S9 释放锁失败那条），
// 而释放发生在临界区函数返回时 —— 在锁内拼 data 会漏掉它。
type captureOutcome struct {
	SourceID model.SourceID
	Rel      string
	Deduped  bool
	HasNote  bool
	NoteID   string
	// TxnID 在 AllocateTxnID 成功之后即非空（= `.index/txn/<txn_id>` 目录名）；
	// 零 write-set 与分配之前失败的路径恒为空串（此时 data 省略该键）。
	TxnID string
	// Commit 是 Git 回执；未跑 Git（零 write-set / 提交前阻断）时为零值。
	Commit git.CommitInfo
	// InboxSkip 是收件区 B3 跳过：其余写入照常提交，本条如实上报（退 3）。
	//
	// **它与下面几种失败正交**：Git 失败退 4 优先，但跳过这件事已经发生过，
	// 不因为「有更高优先级的错误」就从 data.warnings / 错误明细里消失。
	InboxSkip *store.SkipError
	// RolledBack 表示 S6 提交期出现普通 I/O 失败、txn 层已按合同 §5.2 主动放弃：
	// 全部目标一条未写、磁盘已回前像、abort 最后落盘、Git 未跑（退 3，**不是**退 1）。
	RolledBack bool
	// RollbackErr 是触发上述回滚的原始 I/O 失败（只进文案，不参与退出码分类）。
	RollbackErr error
	// Unwritten 是本次 accepted write-set 的路径清单（**不是**已写入清单）：
	// 回滚路径要逐条交代「这些目标一个都没写成」。
	Unwritten []string
	// Blocked 是「号码已分配、但事务在提交前被阻断」（intent 发布失败 / 提交未收敛为
	// 已回滚状态）。与 RolledBack 互斥：那一格是干净放弃，这一格需要人工处置。
	Blocked error
	// GitErr 是 S7 的 Git 失败：Markdown 已原子生效并保持目标态，不回滚、不二次写（退 4）。
	GitErr error
}

// runCapture 实现 eg capture。
func (r *Root) runCapture(inv *Invocation) (*Result, error) {
	// —— S0：正文 / 参数 / stamp 全在锁外解析，一律不触碰库内权威文件。——
	body, err := r.captureBody(inv)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) < MinBodySize {
		return nil, &ValidationError{
			Msg: fmt.Sprintf("eg capture 的正文为空或过短（去首尾空白后 %d 字节 < %d）：零写入",
				len(bytes.TrimSpace(body)), MinBodySize),
			Diags: []Diagnostic{{
				Level: LevelError, Path: "--body-stdin|--body-file", OpIndex: NonOpDiagnostic,
				Code:    E17,
				Message: "正文为空或过短：eg 不抓取网页，正文字节必须由 Agent 传入",
			}},
		}
	}
	stamp, err := captureStamp(inv, r.now())
	if err != nil {
		return nil, err
	}

	rawURL, title, reason := inv.String("url"), inv.String("title"), inv.String("reason")
	domain, fallback := captureDomain(inv)
	res := &Result{}
	warn := func(path, msg string) {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelWarning, Path: path, OpIndex: NonOpDiagnostic, Message: msg,
		})
	}
	if fallback {
		warn(ConfigFileName, fmt.Sprintf(
			"未给 --domain：按 %s 的 default_domain 落位 %s（default_domain_fallback）",
			ConfigFileName, domain))
	}
	if !inv.Config.HasDomain(domain) {
		warn("--domain", fmt.Sprintf("领域 %s 未登记在 %s 的 domains 里：条目照常登记",
			domain, ConfigFileName))
	}
	for _, msg := range titleWarnings(title, rawURL) {
		warn("--title", msg)
	}
	if n := len(bytes.TrimSpace(body)); n < TruncationHintSize {
		warn("--body-stdin|--body-file", fmt.Sprintf(
			"正文仅 %d 字节（< %d）：疑似截断，收录照常完成", n, TruncationHintSize))
	}

	// —— S1~S7 + S9：整段写入在同一把 run.lock 内完成，返回即已释放。——
	oc, cerr := r.captureCritical(inv, res, captureInput{
		Body: body, Stamp: stamp, RawURL: rawURL, Title: title, Reason: reason, Domain: domain,
	}, warn)
	if oc == nil {
		// 连 source_id / path 都没定盘（锁失败、恢复阻断、扫描失败、B3 跳过…）：
		// 本次零权威写、无 commit，data 无从谈起。
		return nil, cerr
	}

	// —— 渲染：全部在锁外。——
	//
	// `data.warnings[]` 里**必须**含收件区跳过那条：它与后面的退出码裁决正交 ——
	// 哪怕本次以退 4（Git 失败）收场，「条目没登记」也已经是既成事实，不能因为
	// 有更高优先级的错误就从产物里消失（否则用户永远不知道队列里少了一条）。
	warnMsgs := warningMessages(res.Warnings)
	if oc.InboxSkip != nil {
		warnMsgs = append(warnMsgs, oc.InboxSkip.Error())
	}
	res.Data = map[string]interface{}{
		"source_id": string(oc.SourceID),
		"path":      oc.Rel,
		"deduped":   oc.Deduped,
		"has_note":  oc.HasNote,
		"note_id":   oc.NoteID,
		"commit":    commitInfoData(oc.Commit),
		"warnings":  warnMsgs,
	}
	// 第八键**条件出现**：号码到手才有，且必与 `.index/txn/<txn_id>` 目录名逐字一致。
	if oc.TxnID != "" {
		res.Data["txn_id"] = oc.TxnID
	}
	res.Summary = append(res.Summary, fmt.Sprintf("原文：%s（%s）", oc.SourceID, oc.Rel))
	if oc.Deduped {
		res.Summary = append(res.Summary, "判重命中：复用已有原文，未产生第二份")
	}
	if oc.Commit.Created {
		res.Summary = append(res.Summary, "commit："+oc.Commit.Subject)
	} else {
		res.Summary = append(res.Summary, "无改动，未产生 commit")
	}

	// —— 最近一次报告落盘（I-…-018）。——
	//
	// 合同 §1.9 的覆盖面是「最近一次 `apply` / `capture` 的最终报告」：capture 这一半入口
	// 过去不落 `.eg/last-report.json`，于是 `eg report --last` 在成功收录之后照样退 1，
	// 提示语还反过来要求用户「先执行一次 eg apply」——声明的能力在一半入口上不可用。
	//
	// 三条边界：
	//   · 落盘在**锁外**、Git 之后，与 apply / delete / mark-reviewed 同址同形态
	//     （`.eg/` 不是知识产物、已进本地忽略清单，不进本次 commit、不污染工作区）；
	//   · **信封 `data` 键面逐字不变**：§1.3 的封闭键集里没有 `report`，报告体只进记录
	//     （走 saveReportBody 的独立容器），因此对接方看到的 capture 输出一个字节没变；
	//   · 退出码**先裁决、后落盘**：记录里的 `exit_code` 与本次进程退出码同源，
	//     四种结局（主动回滚 / 提交前阻断 / Git 失败 / 收件区跳过 / 成功）都留记录 ——
	//     失败那次的报告恰恰最需要能被复现。
	saveCapture := func(code int) {
		r.saveReportBody(inv.VaultRoot, res, map[string]interface{}{
			"report": captureReportBody(res, oc, domain, fallback),
		}, code)
	}

	// —— 退出码裁决。收件区跳过的诊断随每一格一起交付，一条都不丢。——
	skipDiags := inboxSkipDiags(oc.InboxSkip)
	switch {
	case oc.RolledBack:
		// S6 提交期普通 I/O 失败 ⇒ txn 层已把**全部**目标还原成前像、最后写下 abort。
		// 这是「一条都没写成」的主动放弃，不是基础设施崩坏：与 runPlan 同一裁决 —— 退 3。
		// 逐路径的「目标未写入」已在临界区内进了 warnings（因此也在 data.warnings 里）。
		saveCapture(ExitPartialWrite)
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("原子提交失败，事务 %s 已整体回滚：%d 个目标文件一个都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
				oc.TxnID, len(oc.Unwritten), oc.RollbackErr),
			Diags: skipDiags,
		}
	case oc.Blocked != nil:
		// 事务未能走完且未被判定为主动放弃：诊断（E15/E16…）由 blockedError 搬运，
		// 这里只补收件区跳过的事实。
		//
		// 注：本行刻意不写「收敛」+ 全角冒号的连写。那串字面量是逐卡收敛结论的渲染标记，
		// TestConvergenceRenderedOnce 用 grep 反证它在 internal/ 下只许出现在 convergence.go，
		// 以此禁止第二套渲染实现——连注释里的巧合命中也算破窗。
		if len(skipDiags) > 0 {
			if b, ok := oc.Blocked.(*TxnBlockedError); ok {
				b.Diags = append(b.Diags, skipDiags...)
			}
		}
		saveCapture(ExitCodeFor(classifyExit5(oc.Blocked)))
		return res, oc.Blocked
	case oc.GitErr != nil:
		// 退 4 优先于收件区跳过的退 3，但跳过的明细必须一并交付（不得丢）。
		saveCapture(ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "eg capture 的 Git 提交失败（磁盘保留现状）", Err: oc.GitErr,
			Diags: append([]Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(oc.GitErr),
			}}, skipDiags...),
		}
	case oc.InboxSkip != nil:
		saveCapture(ExitPartialWrite)
		return res, &PartialWriteError{
			Msg:   "收件区条目未登记，其余写入已保留并提交",
			Diags: skipDiags,
		}
	}
	saveCapture(ExitCodeFor(classifyExit5(cerr)))
	return res, cerr
}

// captureReportBody 把本次收录的既成事实投影成 §4.6 的**报告体**（capture 子集）。
//
// 它只搬运 `captureOutcome` 与 `Result.Warnings` 里已有的事实，**不重新扫描、不重新计算、
// 不补算**：报告的底线是如实。capture 不加工笔记、不产卡，所以 `note` / `cards` /
// `relations` / `open_questions` 一律是 New() 给的空值形态 —— 空数组是事实，编造条目才是错。
//
//	source                  ← 本次原文（判重命中时是复用的那一份）
//	links[]                 ← 原文相对路径（真正走完提交才有；回滚 / 阻断路径为空）
//	git.commit              ← 本次 commit sha（未产生 / 提交失败 → null）
//	skipped[]               ← 收件区 B3 跳过（kind / cause 的家在这里，不占 code 位）
//	default_domain_fallback ← 未给 --domain 时按 evergreen.yml 落位这一事实
//	warnings[]              ← 本次全部 W / I，条目数守恒（不折叠、不去重、不改级别）
//	txn_id                  ← 号码一旦分配就必填（A-59），未开事务的路径省略
func captureReportBody(res *Result, oc *captureOutcome, domain string, fallback bool) report.Report {
	rep := report.New()
	rep.Source = report.Source{ID: string(oc.SourceID), Path: oc.Rel}
	rep.DefaultDomainFallback = report.Fallback{Used: fallback}
	if fallback {
		rep.DefaultDomainFallback.Reason = fmt.Sprintf(
			"未给 --domain：按 %s 的 default_domain 落位 %s", ConfigFileName, domain)
	}
	// 回滚与提交前阻断这两条路径上「一个字节都没写成」，因此不许留 links[]（那是预演的账）。
	if !oc.RolledBack && oc.Blocked == nil && oc.Rel != "" {
		rep.Links = append(rep.Links, oc.Rel)
	}
	rep.SetCommit(oc.Commit.SHA)
	rep.SetTxnID(oc.TxnID)
	if oc.InboxSkip != nil && strings.TrimSpace(oc.InboxSkip.Path) != "" {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind:    string(oc.InboxSkip.Reason),
			Target:  oc.InboxSkip.Path,
			Locator: oc.InboxSkip.Path,
			Cause:   store.CauseFor(oc.InboxSkip.Reason),
			Detail:  oc.InboxSkip.Detail,
		})
	}
	for _, d := range res.Warnings {
		rep.AddWarning(report.Diagnostic{
			Code: d.Code, Level: d.Level, Path: d.Path, OpIndex: report.NonOp,
			Message: d.Message, Target: d.Target,
		})
	}
	return rep
}

// inboxSkipDiags 把收件区跳过转成错误明细（无跳过时返回 nil）。
//
// I-…-015：`code` 恒为 E24（本次跳过未写入），`kind` / `cause` 如实进 `message` ——
// 枚举值的家是 `report.skipped[].kind` / `.cause`，不得占用编号域封闭的 `code` 位。
func inboxSkipDiags(skip *store.SkipError) []Diagnostic {
	if skip == nil {
		return nil
	}
	return []Diagnostic{{
		Level: LevelError, Path: skip.Path, OpIndex: NonOpDiagnostic,
		Code:    E24,
		Message: skipDiagMessage(skip),
	}}
}

// skipDiagMessage 把 detail 与 kind / cause 拼成一句：归因一个字都不丢，只是从 code 位
// 回到 message 里（机器可读枚举仍在 report.skipped[]）。
func skipDiagMessage(skip *store.SkipError) string {
	return fmt.Sprintf("%s（kind=%s，cause=%s）",
		skip.Detail, skip.Reason, store.CauseFor(skip.Reason))
}

// captureInput 是锁外解析好、原样带进临界区的输入（不含任何库内读取结论）。
type captureInput struct {
	Body   []byte
	Stamp  model.Stamp
	RawURL string
	Title  string
	Reason string
	Domain string
}

// captureCritical 是 eg capture 的整个临界区：进函数即取锁，出函数即释放锁。
//
// 返回 (nil, err) 表示「连 data 都产不出来」；返回 (oc, nil) 时失败事实挂在 oc.Err 上，
// 由调用方在锁外连同 data 一起交付。
func (r *Root) captureCritical(inv *Invocation, res *Result, in captureInput,
	warn func(path, msg string)) (*captureOutcome, error) {
	root := inv.VaultRoot

	// —— S1 + S2：取锁、崩溃恢复屏障（与 plan 写链共用同一底座与同一把锁）。——
	sess, serr := r.enterTxnCritical(inv, resultWarnSink{res}, txnCriticalOpts{
		ZeroWrite: "本次零写入", ReleaseNotice: "本次收录与提交不受影响",
	})
	if serr != nil {
		return nil, serr
	}
	defer sess.release()

	// —— S3：锁内重新建 Store、重新全库发现、重新判重、重新读笔记。——
	//
	// 严禁把这三步挪到锁外：S2 刚可能把若干原文回滚到前像，且在取锁之前别的写者
	// 可能刚收录完同一篇 —— 拿锁外快照去判重，等于把「不产生第二份」变成一句空话。
	st := store.New(root)
	sources, err := st.ScanSources()
	if err != nil {
		return nil, err
	}
	hit, deduped := store.FindDuplicate(sources, in.RawURL, in.Title)

	var sourceID model.SourceID
	var rel string
	if deduped {
		sourceID, rel = hit.Source.ID, hit.Source.Rel
		warn("sources/", fmt.Sprintf("判重命中（%s 键）：复用已有原文 %s，正文不覆盖、不产生第二份",
			hit.Key, sourceID))
	} else {
		sourceID = model.NewSourceID(model.NewDate(in.Stamp.Time()), captureSlugSeed(in.Title, in.RawURL))
		rel = store.SourceRel(string(sourceID))
	}

	note, hasNote, err := st.NoteOf(sourceID)
	if err != nil {
		return nil, err
	}
	reprocess := inv.Set("reprocess")
	if hasNote && !reprocess {
		warn(note.Rel, fmt.Sprintf(
			"原文 %s 已有材料笔记 %s：默认不重复加工，本次跳过加工（要重新加工请显式加 --reprocess）",
			sourceID, note.ID))
	}

	entry := store.InboxEntry{
		Title:        in.Title,
		Stamp:        in.Stamp,
		Reason:       in.Reason,
		TargetDomain: model.Domain(in.Domain),
		// 已有笔记且未显式 --reprocess 时不再把条目放回队列：收件区是「待加工」队列，
		// 笔记已存在说明该篇已加工完（EG-SRC-02 的条目移出由 write_note 完成）。
		Detached: hasNote && !reprocess,
	}

	// —— S4：原子预演。复用**现有**写口，实盘与 Git 全程零变化，只攒 accepted write-set。——
	if err := st.BeginAtomic(); err != nil {
		return nil, blockedError("原子预演无法开始，本次零写入", err)
	}
	defer st.EndAtomic()

	var inboxSkip *store.SkipError
	if deduped {
		reasonRes, err := st.ApplySourceReason(rel, "", in.Reason)
		if err != nil {
			if skip, ok := store.AsSkip(err); ok {
				// 预演期跳过 ⇒ overlay 丢弃即零写入：不开事务、不发 intent、不跑 Git。
				return nil, skipToPartial(res, skip)
			}
			return nil, err
		}
		if reasonRes.Written {
			warn(rel, "收录理由已追加到该原文的 reasons 列表（正文字节不变）")
		}
		if !entry.Detached {
			inboxRes, skip := st.AttachEntry(entry, sourceID)
			inboxSkip = skip
			_ = inboxRes
		}
	} else {
		out, err := st.ApplySource(store.SourceSpec{
			Rel:     rel,
			ID:      sourceID,
			URL:     in.RawURL,
			Title:   in.Title,
			Stamp:   in.Stamp,
			Tags:    captureTags(inv),
			Reasons: []string{in.Reason},
			Body:    in.Body,
			Inbox:   entry,
		})
		if err != nil {
			return nil, err
		}
		// 收件区 B3 跳过**不进 write-set**（AttachEntry 跳过时根本没 stage 任何字节），
		// 但原文那一条照常提交：这正是「其余写入已保留并提交」的字面含义。
		inboxSkip = out.InboxSkip
	}

	oc := &captureOutcome{
		SourceID: sourceID, Rel: rel, Deduped: deduped,
		HasNote: hasNote, InboxSkip: inboxSkip,
	}
	if hasNote {
		oc.NoteID = string(note.ID)
	}

	ws := st.AtomicWriteSet()
	if len(ws) == 0 {
		// 零 accepted write-set（判重命中 + 理由已在列表 + 条目已 detach）：
		// 不分配 txn_id、不发 intent、不提交、**不跑 Git**（不制造空提交）。
		return oc, nil
	}

	// —— S5：分配 txn_id → 记进锁正文 → 发布 intent（发布屏障）。——
	txnID, oerr := sess.openTxn(inv, intentFilesOf(ws), captureSkippedOf(inboxSkip), r.Now, nil)
	oc.TxnID = txnID // 分配成功即写进产物；分配失败时为空串，data 省略该键
	if oerr != nil {
		if txnID == "" {
			return nil, oerr
		}
		// 号码已在盘 ⇒ 必须带着 txn_id 交付（用户要靠它定位那笔未闭合事务）。
		oc.Blocked = oerr
		return oc, nil
	}

	// —— S6：多文件原子提交。commit marker 在盘之前，Markdown 一律不算生效。——
	//
	// 两种失败必须分开翻译（合同 §5.2 vs §16），与 runPlan 逐字同源：
	//   ① RolledBack=true：提交期普通 I/O 失败，txn 层已把**全部**权威文件还原成前像、
	//      最后写下 abort。磁盘停在事务开始前，Git 不该跑。这是「主动放弃」，退 3。
	//   ② 其余：未能收敛为已回滚状态，库可能需要人工处置 ⇒ 阻断（带 txn_id 交付）。
	cres, cerr := sess.commitWriteSet(txnID, ws)
	switch {
	case cerr != nil && cres != nil && cres.RolledBack:
		oc.RolledBack, oc.RollbackErr = true, cerr
		oc.Unwritten = writeSetPaths(ws)
		warn("ops", fmt.Sprintf(
			"原子提交失败，事务 %s 已按合同 §5.2 主动放弃：%v", txnID, cerr))
		// 逐路径交代「这一条没写成」：报告里只留一句总述，用户无从知道到底哪些目标受影响。
		for _, p := range oc.Unwritten {
			warn(p, "目标未写入：本次事务已整体回滚，该文件保持事务开始前的字节（前像）")
		}
		warn("ops", fmt.Sprintf(
			"事务 %s 的 %d 个目标文件一个都没有写入：txn 层已逐个还原前像、"+
				"全部还原完成后才写下 abort 标记，随后未执行 Git", txnID, len(ws)))
		return oc, nil
	case cerr != nil:
		oc.Blocked = blockedError(fmt.Sprintf(
			"事务 %s 的原子提交失败且未能收敛为已回滚状态，需人工处置", txnID), cerr)
		return oc, nil
	}
	fireTxnStep(TxnStepCommitted, txnID)

	// —— S7：Git。严格晚于 commit marker，仍持同一把锁。——
	//
	// 走 r.repo(root) 而不是 git.New(root)：`Root.NewRepo` 是全仓统一的 Git 注入面，
	// 「Git 失败不回滚、不二次写」这类反证全靠它把提交打成失败。绕过注入面就等于让
	// capture 这条写链**无法被反证**。
	verb := model.VerbCapture
	if reprocess && hasNote {
		verb = model.VerbReprocess
	}
	repo := r.repo(root)
	// 既有改动同样会被 add -A 带进本次 commit：采样早于 Add，如实披露（I-…-023）。
	// capture 没有 report 容器，披露落 `Result.Warnings` → `data.warnings[]`（同一句措辞）。
	noteExistingChangesInResult(res, repo, writeSetPaths(ws))
	info, gerr := repo.Commit(git.Message{
		Verb:    string(verb),
		Domain:  in.Domain,
		Subject: fmt.Sprintf("收录 %s", sourceID),
		Reason:  in.Reason,
	})
	// **无论成败都在尝试结束后触发**：这个节点的语义是「Git 已经跑过了」，
	// 「Git 之后没有第二次权威写」这条反证正是靠它在失败支上取证的。
	fireTxnStep(TxnStepGit, txnID)
	res.Warnings = append(res.Warnings, gitWarnings(info)...)
	if gerr != nil {
		// **不回滚、不做第二次权威写**：Markdown 已由 commit marker 定盘并保持目标态，
		// Git 失败只是「这批已生效的改动没进版本历史」。
		oc.GitErr = gerr
	} else {
		oc.Commit = info
	}

	// —— S8：写后索引同步。仍在**同一把锁内**、Git 之后、Release 之前（合同 §16.1 / §16.3）。——
	//
	// Git 成败都要走这一步，理由是对称的：
	//   · 成功 ⇒ HEAD 前进了，索引水位线不跟上，命令一返回索引就是陈旧的；
	//   · 失败 ⇒ Markdown 已由 commit marker 原子生效，索引的对象面同样已经变了，
	//     「Git 没进版本历史」不构成让派生索引继续指着旧内容的理由。
	// 从 S7 提前 return 绕过 S8，等于把陈旧窗口留到锁外——那时任何进程都能读到
	// 一个自称健康、水位线却落后于权威的索引。
	//
	// 它不开第二个事务、不写任何权威 Markdown，也不改退出码：索引缺失静默跳过、
	// 损坏报 W24、同步失败报 W22（合同 §6.1）。
	r.syncCaptureIndex(res, inv, root, writeSetPaths(ws))
	fireTxnStep(TxnStepIndexSync, txnID)
	return oc, nil
}

// syncCaptureIndex 把唯一的写后索引同步入口 syncIndexAfterWrite 接到 capture 上。
//
// capture 没有 `data.report` 容器，它的诊断面是 `Result.Warnings`（再派生成
// `data.warnings[]`）。所以这里用一份**临时报告体**当诊断容器，同步完把里面的条目
// 原样搬进 Result —— 条目数守恒，不折叠、不去重、不改级别：
// W22（索引未能同步）、W24（索引损坏）与成功时的 info（action / 变更计数 / 水位线推进）
// 一条都不许丢，也一条都不许被改写成别的码。
//
// 刻意**不**另抄一套同步逻辑：全仓写后同步只此一处，抄第二份就会与 `eg index rebuild`
// 的口径漂移，而那正是 M5 最不能出问题的地方。
func (r *Root) syncCaptureIndex(res *Result, inv *Invocation, root string, written []string) {
	var scratch report.Report
	r.syncIndexAfterWrite(&scratch, root, indexWriteLabel(inv), written)
	for _, d := range scratch.Warnings {
		res.Warnings = append(res.Warnings, Diagnostic{
			Code: d.Code, Level: d.Level, Path: d.Path,
			OpIndex: NonOpDiagnostic, Message: d.Message,
		})
	}
}

// captureSkippedOf 把收件区 B3 跳过映射进 intent 的 skipped[]（审计面，不属原子域）。
//
// `kind` 是**封闭两值**（合同 §4）：名册外的形态一律不进日志 —— 硬塞进去会让下一次
// Scan 把整个事务判成 corrupt，把「一条审计记录写歪」升级成「整个库再也写不进去」。
func captureSkippedOf(skip *store.SkipError) []txn.SkippedFile {
	if skip == nil || strings.TrimSpace(skip.Path) == "" {
		return nil
	}
	kind := string(skip.Reason)
	if kind != txn.SkipKindFileChanged && kind != txn.SkipKindUserBlockUnsafe {
		return nil
	}
	return []txn.SkippedFile{{Path: skip.Path, Kind: kind}}
}

// skipToPartial 把 store 的「必须跳过」信号如实转成退 3（调用方不得吞掉，§9 B3）。
func skipToPartial(res *Result, skip *store.SkipError) error {
	_ = res
	return &PartialWriteError{
		Msg: "写入被跳过：" + skip.Detail,
		Diags: []Diagnostic{{
			Level: LevelError, Path: skip.Path, OpIndex: NonOpDiagnostic,
			Code:    E24,
			Message: skipDiagMessage(skip),
		}},
	}
}

// captureBody 读取正文字节：--body-stdin 走 Root.In（默认 os.Stdin），--body-file 走磁盘。
// 正文只用 []byte 传递，不做 rune 迭代重建、不做任何规范化。
func (r *Root) captureBody(inv *Invocation) ([]byte, error) {
	if file := inv.String("body-file"); file != "" {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, &UsageError{Msg: fmt.Sprintf("--body-file 不可读：%v", err)}
		}
		return raw, nil
	}
	in := r.In
	if in == nil {
		in = os.Stdin
	}
	raw, err := io.ReadAll(in)
	if err != nil {
		return nil, &UsageError{Msg: "读取 stdin 正文失败：" + err.Error()}
	}
	return raw, nil
}

// captureStamp 解析 --captured-at（带时区 RFC3339），缺省取本机时间。
func captureStamp(inv *Invocation, now time.Time) (model.Stamp, error) {
	raw := inv.String("captured-at")
	if raw == "" {
		return model.NewStamp(now), nil
	}
	s, err := model.ParseStamp(raw)
	if err != nil {
		return model.Stamp{}, &UsageError{Msg: "--captured-at 非法：" + err.Error()}
	}
	return s, nil
}

// captureDomain 决定目标领域：--domain 优先，否则回退 default_domain（并如实上报）。
func captureDomain(inv *Invocation) (string, bool) {
	if d := inv.String("domain"); d != "" {
		return d, false
	}
	return inv.Config.DefaultDomain, true
}

// captureTags 取 --tag（可重复）。
func captureTags(inv *Invocation) []string {
	f := inv.Flags.Lookup("tag")
	if f == nil {
		return nil
	}
	list, ok := f.Value.(*stringList)
	if !ok {
		return nil
	}
	return []string(*list)
}

// captureSlugSeed 决定 ID 里 slug 的取值来源：优先标题，标题缺失时退化到 URL。
func captureSlugSeed(title, rawURL string) string {
	if title != "" {
		return title
	}
	return store.NormalizeURL(rawURL)
}

// titleWarnings 汇报「标题异常」（缺失 / 过长 / 与 URL 相同）——只出 warning，收录照常完成。
func titleWarnings(title, rawURL string) []string {
	var out []string
	switch {
	case title == "":
		out = append(out, "缺 --title：仅按 URL 判重，ID 的 slug 退化自 URL")
	case len(title) > TitleMaxLen:
		out = append(out, fmt.Sprintf("标题长 %d 字节（> %d）：疑似正文误入标题", len(title), TitleMaxLen))
	case rawURL != "" && title == rawURL:
		out = append(out, "标题与 URL 完全相同：疑似标题抓取失败")
	}
	return out
}

// warningMessages 把诊断压成字符串列表，作为 --json 的 data.warnings（合同 §7.5 七键之一）。
func warningMessages(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Message)
	}
	return out
}
