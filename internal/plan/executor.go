package plan

// op 顺序执行与部分成功汇总（T-evergreen.s1_main_flow-158614-015）。
//
// 本文件是 S1 **唯一的写入编排点**：按 `ops[]` 的声明顺序逐个执行已展开的 Action，
// 每个 action 只调用 internal/store 的既有写口（CreateFile / AppendToSection / WriteGuarded
// 及其之上的 ApplyXxx），本文件自己**不拼字节、不写盘、不 commit**。
//
// 四条安全底线在这里的落点：
//   - B1：只调用追加型写口，没有覆盖 / 替换 / 删除形态；
//   - B2：用户块保留由 store.PreserveUserSections 兜底，返回 SkipUserBlockUnsafe 即如实汇总；
//   - B3：写前 content_hash 比对由 store.WriteGuarded 完成，`base` 未覆盖的文件在校验期
//     就被标记为跳过（W6），两条路径都产出 `skipped[]` 条目，**继续执行其余 op**；
//   - B4：Git 由调用方在全部 op 执行完后统一提交，失败不回滚——本文件不感知 Git。
//
// 跳过命名口径**只有 store.SkipReason 一份**（`file_changed` / `user_block_unsafe`），
// 本文件不自造第三种 kind，也不使用任何同义词。

import (
	"fmt"

	"evergreen/internal/model"
	"evergreen/internal/store"
)

// SkipItem 是一条「本次未写入」的如实上报（报告 `skipped[]` 的数据源，形态见合同 §8）。
type SkipItem struct {
	OpIndex int
	Kind    store.SkipReason
	Target  string
	Locator string
	Cause   string
	Detail  string
}

// SourceRecord 是本次涉及的原文事实。
type SourceRecord struct {
	ID     string
	Path   string
	Reused bool
}

// NoteRecord 是本次涉及的材料笔记事实。
type NoteRecord struct {
	ID          string
	Domain      string
	Path        string
	Reprocessed bool
	Reused      bool
}

// MaterialRecord 是一条已写入的材料关系（四要素）。
type MaterialRecord struct {
	Card   string
	Source string
	Note   string
	Rel    string
	Reason string
}

// KnowledgeRecord 是一条已写入的论证关系（方向已规范化）。
type KnowledgeRecord struct {
	From   string
	Type   string
	Target string
	Reason string
}

// QuestionRecord 是一条已写入的未决问题。
type QuestionRecord struct {
	Note     string
	Question string
}

// 显著变更的四类（S1 可发生，报告 `high_impact[]` 的 kind 取值）。
const (
	// ImpactNewCoreCard 新建承载新核心含义的卡。
	ImpactNewCoreCard = "new_core_card"
	// ImpactNonCoreSupplement 向已有卡追加非核心补充。
	ImpactNonCoreSupplement = "non_core_supplement"
	// ImpactOpposing 建立或调整 opposing。
	ImpactOpposing = "opposing"
	// ImpactRelation 建立或调整论证关系（opposing 之外的三值）。
	ImpactRelation = "relation"
)

// ImpactItem 是一条显著变更。
type ImpactItem struct {
	Kind    string
	Target  string
	Detail  string
	OpIndex int
}

// ExecOptions 是执行期的外部输入：时间来源、base 映射与 id 索引，全部由调用方注入，
// 使执行完全确定性（`eg` 不调用模型、不做网络请求）。
type ExecOptions struct {
	Stamp model.Stamp
	Date  model.Date
	Base  map[string]string
	Index *store.Index
}

// ExecResult 是一次执行的全部事实：写了什么、跳过了什么、为什么跳过，一条都不折叠。
type ExecResult struct {
	Written  []string
	Skipped  []SkipItem
	Warnings []Diagnostic

	Source SourceRecord
	Note   NoteRecord

	CardsCreated []string
	CardsReused  []string
	CardsUpdated []string

	Material  []MaterialRecord
	Knowledge []KnowledgeRecord
	Questions []QuestionRecord

	HighImpact []ImpactItem

	// Failures 是执行期的非跳过型失败（写口返回的普通错误）。它们既不是 B2/B3 跳过，
	// 也不得被吞掉：如实进报告 warnings[]，并让本次 apply 落到「部分成功」。
	Failures []Diagnostic
}

// Partial 报告本次是否属于部分成功（有跳过或有失败）。
func (e *ExecResult) Partial() bool { return len(e.Skipped) > 0 || len(e.Failures) > 0 }

// executor 串起一次执行。
type executor struct {
	s   *store.Store
	opt ExecOptions
	out *ExecResult
}

// Execute 按 `ops[]` 声明顺序逐个执行 Action。
//
// **调用前提**：res.Failed() 为假（有 error 级诊断时调用方必须零写入）。
// 任何单个 op 的跳过或失败都不中断后续 op：S1 明确接受部分成功，前提是如实上报。
func Execute(s *store.Store, res *Result, opt ExecOptions) *ExecResult {
	e := &executor{s: s, opt: opt, out: &ExecResult{}}
	for i := range res.Actions {
		e.run(res.Actions[i])
	}
	return e.out
}

func (e *executor) run(a Action) {
	if a.Skip {
		e.skip(a.OpIndex, a.SkipKind, a.ID, a.Path, a.SkipDetail)
		return
	}
	switch a.Kind {
	case ActSourceNew:
		e.sourceNew(a)
	case ActSourceReuse:
		e.out.Source = SourceRecord{ID: a.ID, Path: a.Path, Reused: true}
	case ActNoteNew:
		e.noteNew(a)
	case ActNoteReuse:
		e.out.Note = NoteRecord{ID: a.ID, Domain: a.Domain, Path: a.Path, Reused: true}
	case ActNoteAppend:
		e.noteAppend(a)
	case ActCardNew:
		e.cardNew(a)
	case ActCardAppend:
		e.cardAppend(a)
	case ActMaterialRel:
		e.materialRelWrite(a)
	case ActRelation:
		e.relationWrite(a)
	case ActOpenQuestion:
		e.openQuestionWrite(a)
	}
}

// —— 七个 op 的执行 ——

func (e *executor) sourceNew(a Action) {
	op := a.Op
	stamp := e.stampOf(op.SavedAt)
	domain := op.TargetDomain
	if domain == "" {
		domain = a.Domain
	}
	var reasons []string
	if op.Reason != "" {
		reasons = []string{op.Reason}
	}
	out, err := e.s.ApplySource(store.SourceSpec{
		Rel:     a.Path,
		ID:      model.SourceID(a.ID),
		URL:     op.URL,
		Title:   op.Title,
		Stamp:   stamp,
		Tags:    op.Tags,
		Reasons: reasons,
		Body:    op.Body,
		Inbox: store.InboxEntry{
			Title:        op.Title,
			Stamp:        stamp,
			Reason:       op.Reason,
			TargetDomain: model.Domain(domain),
			ExpectedHash: e.baseHash(store.UnprocessedFile),
		},
	})
	if !e.record(a, out.Source, err) {
		return
	}
	e.out.Source = SourceRecord{ID: a.ID, Path: a.Path}
	e.inbox(a, a.ID, out.Inbox, out.InboxSkip, "收件区条目未登记")
}

func (e *executor) noteNew(a Action) {
	op := a.Op
	out, err := e.s.ApplyNote(store.NoteSpec{
		Rel:         a.Path,
		ID:          model.NoteID(a.ID),
		SourceID:    model.SourceID(op.Source),
		Title:       op.Title,
		Date:        e.dateOf(),
		Stamp:       e.opt.Stamp,
		Tags:        op.Tags,
		Sections:    sectionAppends(a.Sections),
		OutputCards: outputCardItems(a.OutputCards),
		Inbox:       store.InboxSpec{ExpectedHash: e.baseHash(store.UnprocessedFile)},
	})
	if !e.record(a, out.Note, err) {
		return
	}
	e.out.Note = NoteRecord{ID: a.ID, Domain: a.Domain, Path: a.Path, Reused: out.Reused}
	// EG-SRC-02：条目未成功移出必须显式上报，绝不静默。
	e.inbox(a, op.Source, out.Inbox, out.InboxSkip, "收件区条目未移出")
}

func (e *executor) noteAppend(a Action) {
	res, err := e.s.WriteGuarded(a.Path, a.ExpectedHash, store.Edit{
		Kind:     store.KindNote,
		Sections: sectionAppends(a.Sections),
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.Note = NoteRecord{ID: a.ID, Domain: a.Domain, Path: a.Path, Reprocessed: true}
}

func (e *executor) cardNew(a Action) {
	op := a.Op
	res, err := e.s.ApplyCard(store.CardSpec{
		Rel:      a.Path,
		ID:       model.CardID(a.ID),
		Title:    op.Title,
		Date:     e.dateOf(),
		Stamp:    e.opt.Stamp,
		Tags:     op.Tags,
		Sources:  sourceRefs(op.Sources),
		Sections: sectionAppends(a.Sections),
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.CardsCreated = append(e.out.CardsCreated, a.ID)
	for _, ref := range op.Sources {
		e.out.Material = append(e.out.Material, MaterialRecord{
			Card: a.ID, Source: ref.Source, Note: ref.Note, Rel: ref.Rel, Reason: ref.Reason,
		})
	}
	e.impact(ImpactNewCoreCard, a.ID, a.OpIndex, "新建知识卡：承载本次加工的新核心含义")
}

func (e *executor) cardAppend(a Action) {
	res, err := e.s.ApplyCardAppend(store.CardAppendSpec{
		Rel:          a.Path,
		ExpectedHash: a.ExpectedHash,
		Stamp:        e.opt.Stamp,
		Sections:     sectionAppends(a.Sections),
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.CardsUpdated = append(e.out.CardsUpdated, a.ID)
	e.impact(ImpactNonCoreSupplement, a.ID, a.OpIndex,
		fmt.Sprintf("向已有卡追加非核心补充：%s", sectionNames(a.Sections)))
}

func (e *executor) materialRelWrite(a Action) {
	ref := a.Material
	if ref == nil {
		return
	}
	res, err := e.s.ApplyMaterialRel(store.MaterialRelSpec{
		Rel:          a.Path,
		ExpectedHash: a.ExpectedHash,
		Stamp:        e.opt.Stamp,
		Ref: model.SourceRef{
			Source: model.SourceID(ref.Source),
			Note:   model.NoteID(ref.Note),
			Rel:    model.MaterialRel(ref.Rel),
			Reason: ref.Reason,
		},
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.Material = append(e.out.Material, MaterialRecord{
		Card: a.ID, Source: ref.Source, Note: ref.Note, Rel: ref.Rel, Reason: ref.Reason,
	})
}

func (e *executor) relationWrite(a Action) {
	rel := a.Relation
	if rel == nil {
		return
	}
	record := KnowledgeRecord{From: a.ID, Type: rel.Type, Target: rel.Target, Reason: rel.Reason}
	if rel.Duplicate {
		// 同对 opposing 已存在：幂等不写第二条，但关系事实照样进报告。
		e.out.Knowledge = append(e.out.Knowledge, record)
		return
	}
	res, err := e.s.ApplyRelation(store.RelationSpec{
		Rel:          a.Path,
		ExpectedHash: a.ExpectedHash,
		Stamp:        e.opt.Stamp,
		From:         model.CardID(a.ID),
		Relation: model.Relation{
			Type:   model.RelationType(rel.Type),
			Target: model.CardID(rel.Target),
			Reason: rel.Reason,
		},
		Index: e.opt.Index,
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.Knowledge = append(e.out.Knowledge, record)
	kind, detail := ImpactRelation, fmt.Sprintf("建立论证关系 %s → %s", rel.Type, rel.Target)
	if rel.Type == string(model.RelationOpposing) {
		kind = ImpactOpposing
		detail = fmt.Sprintf("建立或调整 opposing：%s ↔ %s（单向存储，一对一条记录）", a.ID, rel.Target)
	}
	e.impact(kind, a.ID, a.OpIndex, detail)
}

func (e *executor) openQuestionWrite(a Action) {
	op := a.Op
	res, err := e.s.ApplyOpenQuestion(store.OpenQuestionSpec{
		Rel:          a.Path,
		ExpectedHash: a.ExpectedHash,
		Stamp:        e.opt.Stamp,
		Question:     lineTerminated([]byte(op.Question)),
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.Questions = append(e.out.Questions, QuestionRecord{Note: a.ID, Question: op.Question})
}

// —— 汇总助手 ——

// record 归一处理单文件回执：写入 → 记 links[]；跳过 → 记 skipped[]；其余错误 → 记 Failures。
// 返回值表示「该 op 可以继续记账」（跳过与失败都返回 false）。
func (e *executor) record(a Action, res store.Result, err error) bool {
	path := res.Path
	if path == "" {
		path = a.Path
	}
	if err != nil {
		if skip, ok := store.AsSkip(err); ok {
			e.skip(a.OpIndex, skip.Reason, a.ID, skip.Path, skip.Detail)
			return false
		}
		e.out.Failures = append(e.out.Failures, Diagnostic{
			Code: Unnumbered, Level: LevelWarning, OpIndex: a.OpIndex,
			Path:    opPath(a.OpIndex, ""),
			Message: fmt.Sprintf("写入 %s 失败：%v（其余 op 照常执行，磁盘保留现状）", path, err),
			Target:  a.ID,
		})
		return false
	}
	for _, w := range res.Warnings {
		e.out.Warnings = append(e.out.Warnings, Diagnostic{
			Code: Unnumbered, Level: LevelWarning, OpIndex: a.OpIndex,
			Path: opPath(a.OpIndex, ""), Message: w, Target: path,
		})
	}
	if res.Written {
		e.link(path)
		return true
	}
	if res.Detail != "" {
		e.out.Warnings = append(e.out.Warnings, infoAt(a.OpIndex, opPath(a.OpIndex, ""), "%s", res.Detail))
	}
	return true
}

// inbox 汇总收件区条目的登记 / 移出结果：未成功即显式上报（EG-SRC-02）。
// inbox 汇报收件区条目的登记 / 移出结果。
//
// EG-SRC-02：条目未成功登记或未成功移出**必须显式上报**（target 取 source_id，
// 让用户一眼看出是哪一篇原文的条目留在了收件区），绝不静默。
func (e *executor) inbox(a Action, sourceID string, res store.Result, skip *store.SkipError, what string) {
	if skip != nil {
		e.skip(a.OpIndex, skip.Reason, sourceID, skip.Path,
			fmt.Sprintf("%s（source_id=%s）：%s", what, sourceID, skip.Detail))
		return
	}
	if res.Written {
		e.link(res.Path)
	}
}

func (e *executor) skip(opIndex int, kind store.SkipReason, target, locator, detail string) {
	if kind == store.SkipNone {
		kind = store.SkipFileChanged
	}
	e.out.Skipped = append(e.out.Skipped, SkipItem{
		OpIndex: opIndex,
		Kind:    kind,
		Target:  target,
		Locator: locator,
		Cause:   store.CauseFor(kind),
		Detail:  detail,
	})
}

func (e *executor) link(path string) {
	if path == "" {
		return
	}
	for _, existing := range e.out.Written {
		if existing == path {
			return
		}
	}
	e.out.Written = append(e.out.Written, path)
}

func (e *executor) impact(kind, target string, opIndex int, detail string) {
	e.out.HighImpact = append(e.out.HighImpact,
		ImpactItem{Kind: kind, Target: target, Detail: detail, OpIndex: opIndex})
}

func (e *executor) baseHash(key string) string { return e.opt.Base[key] }

func (e *executor) stampOf(savedAt string) model.Stamp {
	if savedAt != "" {
		if s, err := model.ParseStamp(savedAt); err == nil {
			return s
		}
	}
	return e.opt.Stamp
}

func (e *executor) dateOf() model.Date {
	if !e.opt.Date.IsZero() {
		return e.opt.Date
	}
	return model.NewDate(e.opt.Stamp.Time())
}

func sectionAppends(list []SectionWrite) []store.SectionAppend {
	out := make([]store.SectionAppend, 0, len(list))
	for _, s := range list {
		out = append(out, store.SectionAppend{Section: s.Section, Payload: s.Payload})
	}
	return out
}

func sectionNames(list []SectionWrite) string {
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Section)
	}
	return fmt.Sprintf("%v", names)
}

func sourceRefs(refs []MaterialRef) []model.SourceRef {
	out := make([]model.SourceRef, 0, len(refs))
	for _, r := range refs {
		out = append(out, model.SourceRef{
			Source: model.SourceID(r.Source),
			Note:   model.NoteID(r.Note),
			Rel:    model.MaterialRel(r.Rel),
			Reason: r.Reason,
		})
	}
	return out
}

// outputCardItems 把「产出知识卡」快照渲染成列表项文本（逐字，不做任何推断）。
func outputCardItems(cards []OutputCard) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		if c.Card == "" {
			continue
		}
		if c.Mode == "" {
			out = append(out, c.Card)
			continue
		}
		out = append(out, fmt.Sprintf("%s（%s）", c.Card, c.Mode))
	}
	return out
}
