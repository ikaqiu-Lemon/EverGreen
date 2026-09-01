package cli

// eg apply 的业务实现（合同 §1.5 / §2；技术方案 §7.1、§8、§9、§4.5.1）。
//
// `eg apply` 是 S1 **唯一的写入入口**，本文件只做**编排**，五步固定顺序：
//
//	① 反序列化 + E1–E6 校验（任一 error → 退 2、零写入、无 commit）
//	② 按 ops[] 声明顺序逐个执行（internal/plan 的执行器 → internal/store 的写口）
//	③ 按 §4.6 汇总实际写入事实与 skipped[] / warnings[]
//	④ 一次 git add -A + commit（verb 取 plan.verb）
//	⑤ 产出最终报告（internal/report），失败路径同样出报告
//
// 校验、op 展开、字节写入、提交信息与 B4 全部复用既有实现，本文件**不重新实现**任何一层。
// 退出码只用 exit.go 的四类带类型错误表达，**绝不在这里自行拼装码值**：
// 0 全部成功 / 1 环境或参数错误 / 2 校验失败（零写入）/ 3 部分成功 / 4 提交失败（不回滚，B4）。
//
// 本文件没有事务意图文件、没有两阶段写入、没有文件锁、没有写前复核、没有任何回滚入口
// （S5 / M6 的范围，S1 一律不做）。

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"evergreen/internal/git"
	"evergreen/internal/model"
	"evergreen/internal/plan"
	"evergreen/internal/report"
	"evergreen/internal/store"
)

// StateDir 是 vault 内的**工程状态**目录：只放 `eg` 自己的运行痕迹，
// 不是知识产物（EG-AGT-04），也不在 domains/** 或 sources/** 之下。
const StateDir = ".eg"

// LastReportFile 是最近一次 apply 报告的落盘位置（供 `eg report --last` 逐字复现）。
const LastReportFile = StateDir + "/last-report.json"

// coverageGapNames 是 coverage_gaps 受控枚举的中文覆盖要点名（EG-EXT-02：透传不判断）。
var coverageGapNames = map[string]string{
	"core_claim":     "核心主张",
	"key_evidence":   "关键证据",
	"counterexample": "反例",
	"boundary":       "边界",
	"method":         "方法",
	"conclusion":     "结论",
	"limitation":     "局限",
}

// runApply 实现 eg apply：只负责「取字节 → 反序列化」，其余编排交给 runPlan。
func (r *Root) runApply(inv *Invocation) (*Result, error) {
	raw, err := r.planBytes(inv)
	if err != nil {
		return nil, err
	}

	p, perr := plan.Parse(raw)
	if perr != nil {
		rep := report.New()
		rep.NoteZeroCards("plan")
		return applyResult(rep, nil, nil), &ValidationError{
			Msg: perr.Error(),
			Diags: []Diagnostic{{
				Code: "E5", Level: LevelError, Path: "plan", OpIndex: NonOpDiagnostic,
				Message: perr.Error(),
			}},
		}
	}
	return runPlan(r, inv, p)
}

// runPlan 是 ChangePlan 的**唯一**执行编排：`eg apply`（T-…-015）与 `eg rel add`
// （T-…-024）共用这一份实现，绝不各留一份平行副本。
//
// 入参 p 是一份**已解析**的 plan：apply 由 `plan.Parse` 得到，rel add 在内存里组装
// （不落临时文件）。两条入口自此往下逐字走同一条链路：
//
//	plan.Validate（E1–E6 / W1–W8 / I1）→ plan.Execute（executor → internal/store 写口）
//	→ report 汇总 → internal/git 一次 commit（verb 取 plan.verb）
//
// 因此「B1–B4」「退出码 0–4」「一次命令 = 一次 commit」对两条入口是同一套代码事实。
func runPlan(r *Root, inv *Invocation, p *plan.ChangePlan) (*Result, error) {
	root := inv.VaultRoot
	st := store.New(root)

	env, err := plan.EnvFor(st, inv.Config.DefaultDomain)
	if err != nil {
		return nil, err
	}
	pres := plan.Validate(p, env)

	// EG-DOM-03：既无 plan.domain 也无 default_domain → 退 1，CLI 绝不自选领域。
	if pres.DomainUnavailable {
		return nil, &UsageError{Msg: fmt.Sprintf(
			"无法确定落位领域：plan.domain 缺失且 %s 未配置 default_domain（CLI 绝不自选领域）",
			ConfigFileName)}
	}

	rep := report.New()
	if pres.DomainFallback && pres.Domain != "" {
		rep.DefaultDomainFallback = report.Fallback{Used: true, Reason: fmt.Sprintf(
			"plan.domain 缺失，已按 %s 的 default_domain=%q 落位", ConfigFileName, pres.Domain)}
	}
	for _, d := range pres.Warnings {
		rep.AddWarning(toReportDiag(d))
	}
	addCoverageGapInfos(&rep, pres)

	// ① 校验失败：零写入、无 commit，报告照样产出（失败也要有交代）。
	if pres.Failed() {
		rep.NoteZeroCards("cards")
		res := applyResult(rep, pres, nil)
		return res, &ValidationError{
			Msg:   fmt.Sprintf("ChangePlan 校验失败（%d 条 error）：零写入、无 commit", len(pres.Errors)),
			Diags: toCLIDiags(pres.Errors),
		}
	}

	// --dry-run：完整走校验与展开，只输出将写入的清单，零写入零 commit。
	if inv.Set("dry-run") {
		return r.applyDryRun(rep, pres), nil
	}

	// ② + ③ 逐 op 执行并汇总事实。
	stamp := model.NewStamp(r.now())
	ex := plan.Execute(st, pres, plan.ExecOptions{
		Stamp: stamp, Date: model.NewDate(r.now()), Base: p.Base, Index: &env.Index,
	})
	fillReport(&rep, ex)

	// ④ 一次 apply = 一次 commit；无实际改动不产生空 commit。
	repo := r.repo(root)
	if sample, err := repo.Sample(ex.Written); err == nil && len(sample.Existing) > 0 {
		rep.AddInfo("git.commit", report.NonOp,
			"本次 commit 含 %d 处工作区既有改动（S1 按 git add -A 口径一并提交，不做对账）：%s",
			len(sample.Existing), strings.Join(sample.Existing, "、"))
	}
	info, cerr := repo.Commit(git.Message{
		Verb:           pres.Verb,
		Domain:         commitDomain(pres.Domain),
		Subject:        applySubject(ex),
		Reason:         p.Reason,
		RequirementIDs: p.RequirementIDs,
	})
	for _, w := range info.Warnings {
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit",
			OpIndex: report.NonOp, Message: w,
		})
	}
	rep.NoteZeroCards("cards")

	if cerr != nil {
		rep.SetCommit("")
		rep.AddWarning(report.Diagnostic{
			Code: "", Level: report.LevelWarning, Path: "git.commit", OpIndex: report.NonOp,
			Message: commitFailureMessage(cerr),
		})
		res := applyResult(rep, pres, nil)
		r.saveReport(root, res, ExitCommitFailed)
		return res, &CommitFailedError{
			Msg: "Git 提交失败：已写入的文件保持写入后状态，未做任何还原（B4）", Err: cerr,
			Diags: []Diagnostic{{
				Code: "", Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(cerr),
			}},
		}
	}
	if info.Created {
		rep.SetCommit(info.SHA)
	} else {
		rep.SetCommit("")
	}

	// ⑤ 报告 + 退出码。
	res := applyResult(rep, pres, nil)
	if ex.Partial() {
		r.saveReport(root, res, ExitPartialWrite)
		return res, &PartialWriteError{
			Msg: fmt.Sprintf("部分成功：%d 处被跳过、%d 处写入失败；已写入内容保留并已提交",
				len(ex.Skipped), len(ex.Failures)),
			Diags: skippedDiags(ex),
		}
	}
	r.saveReport(root, res, ExitOK)
	return res, nil
}

// planBase 按 `eg context` 的**同一口径**为若干 ID 计算 plan.base（id → content_hash）。
//
// B3 的版本依据只有一份实现——`store.ContentHash`；这里只是把它按 ID 取用，
// 不另起第二套哈希算法（`eg context` 是 Agent 链路的取数口径，`eg rel add` 是单命令闭环，
// 两者算出的 hash 必须逐字相同）。解析不到的 ID 原样跳过：它们由 plan 校验按 E2 处理。
func planBase(root string, ids []string) (map[string]string, map[string]string, error) {
	base, paths := map[string]string{}, map[string]string{}
	st := store.New(root)
	idx, err := st.ScanIDs()
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		rel, err := idx.Resolve(id)
		if err != nil {
			continue
		}
		paths[id] = rel
		f, err := st.Read(rel)
		if err != nil {
			continue
		}
		base[rel] = store.ContentHash(f.Bytes)
	}
	return base, paths, nil
}

// domainOfPath 复用 store 的唯一实现，从 vault 内相对路径取领域（EG-DOM-01：
// 领域由**目录**唯一决定，绝不从 frontmatter 猜）。
func domainOfPath(rel string) string { return store.DomainOf(rel) }

// applyDryRun 输出「将写入的文件与分区清单」，零写入零 commit（合同 §2）。
func (r *Root) applyDryRun(rep report.Report, pres *plan.Result) *Result {
	var planned []map[string]interface{}
	for _, a := range pres.Actions {
		if a.Skip {
			rep.Skipped = append(rep.Skipped, report.Skipped{
				Kind: string(a.SkipKind), Target: a.ID, Locator: a.Path,
				Cause: a.SkipCause, Detail: a.SkipDetail,
			})
			continue
		}
		if a.Path == "" {
			continue
		}
		if len(a.Sections) == 0 {
			planned = append(planned, map[string]interface{}{
				"path": a.Path, "section": "", "op_index": a.OpIndex})
			continue
		}
		for _, s := range a.Sections {
			planned = append(planned, map[string]interface{}{
				"path": a.Path, "section": s.Section, "op_index": a.OpIndex})
		}
	}
	rep.Links = pres.Targets()
	if rep.Links == nil {
		rep.Links = []string{}
	}
	rep.SetCommit("")
	rep.NoteZeroCards("cards")
	res := applyResult(rep, pres, planned)
	res.Summary = append([]string{"--dry-run：零写入、零 commit，以下是将写入的清单"}, res.Summary...)
	return res
}

// fillReport 把执行事实逐条填进报告：**不折叠、不去重丢失**（数量守恒）。
func fillReport(rep *report.Report, ex *plan.ExecResult) {
	rep.Source = report.Source{ID: ex.Source.ID, Path: ex.Source.Path}
	rep.Note = report.Note{ID: ex.Note.ID, Domain: ex.Note.Domain, Path: ex.Note.Path,
		Reprocessed: ex.Note.Reprocessed}
	rep.Cards.Created = appendAll(rep.Cards.Created, ex.CardsCreated)
	rep.Cards.Updated = appendAll(rep.Cards.Updated, ex.CardsUpdated)
	if ex.Note.Reused {
		rep.Note.Path = ex.Note.Path
	}
	rep.Cards.Reused = appendAll(rep.Cards.Reused, ex.CardsReused)
	for _, m := range ex.Material {
		rep.Relations.Material = append(rep.Relations.Material, report.Material{
			Card: m.Card, Source: m.Source, Note: m.Note, Rel: m.Rel, Reason: m.Reason})
	}
	for _, k := range ex.Knowledge {
		rep.Relations.Knowledge = append(rep.Relations.Knowledge, report.Knowledge{
			From: k.From, Type: k.Type, Target: k.Target, Reason: k.Reason})
	}
	for _, q := range ex.Questions {
		rep.OpenQuestions = append(rep.OpenQuestions, report.OpenQuestion{
			Note: q.Note, Question: q.Question})
	}
	rep.Links = appendAll(rep.Links, ex.Written)
	for _, s := range ex.Skipped {
		rep.Skipped = append(rep.Skipped, report.Skipped{
			Kind: string(s.Kind), Target: s.Target, Locator: s.Locator,
			Cause: s.Cause, Detail: s.Detail})
	}
	for _, h := range ex.HighImpact {
		rep.HighImpact = append(rep.HighImpact, report.Impact{
			Kind: h.Kind, Target: h.Target, Detail: h.Detail})
	}
	for _, d := range ex.Warnings {
		rep.AddWarning(toReportDiag(d))
	}
	for _, d := range ex.Failures {
		rep.AddWarning(toReportDiag(d))
	}
}

// addCoverageGapInfos 把 write_note 的 coverage_gaps 逐项原样透传成 I1 info
// （EG-EXT-02：只透传，不推断、不补全；缺失或空数组 → 一条都不输出）。
func addCoverageGapInfos(rep *report.Report, pres *plan.Result) {
	for _, a := range pres.Actions {
		for _, gap := range a.Gaps {
			name := coverageGapNames[gap]
			if name == "" {
				name = "受控枚举外的取值，已原样保留"
			}
			rep.AddInfo(fmt.Sprintf("ops[%d].coverage_gaps", a.OpIndex), a.OpIndex,
				"提炼覆盖项缺失：%s（%s），已原样透传进报告", gap, name)
		}
	}
}

// applyResult 组装命令结果：报告体在 data.report，执行元信息与 plan 回带字段平铺在 data。
func applyResult(rep report.Report, pres *plan.Result, planned []map[string]interface{}) *Result {
	data := map[string]interface{}{"report": rep}
	if pres != nil {
		data["domain"] = pres.Domain
		data["convergence"] = convergenceData(pres.Convergence)
	} else {
		data["domain"] = ""
		data["convergence"] = []map[string]interface{}{}
	}
	if planned != nil {
		data["planned"] = planned
	}
	res := &Result{Data: data, Warnings: toCLIReportDiags(rep.Warnings)}
	res.Summary = rep.Lines()
	// 报告体行之后追加逐卡收敛结论：渲染走 convergence.go 的唯一实现
	// （`eg report --last` 回放时调的是同一个 convergenceLines，两条路径逐字相等）。
	if pres != nil {
		res.Summary = append(res.Summary,
			convergenceLines(convergenceItemsFromPlan(pres.Convergence))...)
	}
	return res
}

// skippedDiags 把跳过与失败转成退 3 的诊断载荷（调用方不得吞掉，B3）。
func skippedDiags(ex *plan.ExecResult) []Diagnostic {
	var out []Diagnostic
	for _, s := range ex.Skipped {
		out = append(out, Diagnostic{
			Code: s.Cause, Level: LevelError, Path: s.Locator, OpIndex: s.OpIndex,
			Message: fmt.Sprintf("%s（kind=%s）", s.Detail, s.Kind), Target: s.Target,
		})
	}
	for _, f := range ex.Failures {
		out = append(out, Diagnostic{
			Code: f.Code, Level: LevelError, Path: f.Path, OpIndex: f.OpIndex,
			Message: f.Message, Target: f.Target,
		})
	}
	return out
}

func commitFailureMessage(err error) string {
	var failed *git.CommitFailed
	if errors.As(err, &failed) {
		msg := "Git 提交失败：" + failed.Reason + "；磁盘保留当前状态，未做任何还原（B4）"
		if len(failed.Uncommitted) > 0 {
			msg += "；未提交清单：" + strings.Join(failed.Uncommitted, "、")
		}
		return msg
	}
	return "Git 提交失败：" + err.Error() + "；磁盘保留当前状态，未做任何还原（B4）"
}

// applySubject 生成 commit 主题的事实部分（只复述已发生的写入，不加评价）。
func applySubject(ex *plan.ExecResult) string {
	return fmt.Sprintf("写入 %d 个文件（新建卡 %d、补充卡 %d、关系 %d、未决问题 %d）",
		len(ex.Written), len(ex.CardsCreated), len(ex.CardsUpdated),
		len(ex.Material)+len(ex.Knowledge), len(ex.Questions))
}

// commitDomain 返回提交主题的域位；plan.domain 为 null（全局操作）时写 `-`（合同 §6）。
func commitDomain(domain string) string {
	if strings.TrimSpace(domain) == "" {
		return "-"
	}
	return domain
}

// planBytes 读取 plan 字节：`-` 走 stdin（Root.In 可注入），否则读文件。
func (r *Root) planBytes(inv *Invocation) ([]byte, error) {
	src := inv.String("plan")
	if src == "-" {
		in := r.In
		if in == nil {
			in = os.Stdin
		}
		raw, err := io.ReadAll(in)
		if err != nil {
			return nil, &UsageError{Msg: "读取 stdin 的 plan 失败：" + err.Error()}
		}
		return raw, nil
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("--plan 不可读：%v", err)}
	}
	return raw, nil
}

// repo 返回该 vault 的 Git 仓（可注入，供提交失败用例）。
func (r *Root) repo(root string) *git.Repo {
	if r.NewRepo != nil {
		return r.NewRepo(root)
	}
	return git.New(root)
}

// saveReport 把本次报告落到 vault 的工程状态目录，供 `eg report --last` 逐字复现。
//
// 落点不在 domains/** 或 sources/** 之下（报告不是知识产物，EG-AGT-04），
// 且在 commit **之后**写、并登记进仓库本地忽略清单，因此既不进本次 commit，
// 也不会污染下一次 apply 的工作区。写失败只出 warning，不影响本次退出码。
func (r *Root) saveReport(root string, res *Result, code int) {
	if err := r.repo(root).Exclude(StateDir + "/"); err != nil {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelWarning, Path: git.ExcludeRel, OpIndex: NonOpDiagnostic,
			Message: "登记忽略清单失败，报告仍已落盘：" + err.Error()})
	}
	if err := writeLastReport(root, res, code); err != nil {
		res.Warnings = append(res.Warnings, Diagnostic{
			Level: LevelWarning, Path: LastReportFile, OpIndex: NonOpDiagnostic,
			Message: "最近一次报告落盘失败（不影响本次写入与提交）：" + err.Error()})
	}
}

func appendAll(dst []string, src []string) []string {
	for _, s := range src {
		dst = append(dst, s)
	}
	return dst
}

// toReportDiag / toCLIDiags / toCLIReportDiags 在三套同构诊断结构间做**无损**搬运：
// internal/plan、internal/report、internal/cli 各自不认识对方的类型（依赖方向 §13），
// 字段一一对应，条目数守恒。
func toReportDiag(d plan.Diagnostic) report.Diagnostic {
	return report.Diagnostic{Code: d.Code, Level: d.Level, Path: d.Path,
		OpIndex: d.OpIndex, Message: d.Message, Target: d.Target}
}

func toCLIDiags(list []plan.Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(list))
	for _, d := range list {
		out = append(out, Diagnostic{Code: d.Code, Level: d.Level, Path: d.Path,
			OpIndex: d.OpIndex, Message: d.Message, Target: d.Target})
	}
	return out
}

func toCLIReportDiags(list []report.Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(list))
	for _, d := range list {
		out = append(out, Diagnostic{Code: d.Code, Level: d.Level, Path: d.Path,
			OpIndex: d.OpIndex, Message: d.Message, Target: d.Target})
	}
	return out
}

// stateAbs 返回状态文件的绝对路径。
func stateAbs(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}
