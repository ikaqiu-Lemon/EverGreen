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

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
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
	// ImpactCoreKnowledge 用户显式改写核心内容分区（M3 的 edit_section，A-13）：
	// 追加取值，既有四类的语义与取值一字不改。
	ImpactCoreKnowledge = "core_knowledge_edit"
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

	// OpinionsCreated / OpinionsUpdated 是本次新建与被追加论据的观点 ID（Schema v2）。
	//
	// 刻意与 Cards* 三个字段分开：观点与知识是**同级**实体（§3.1），
	// 报告里「新增了几条知识」与「提出了几条待验证的判断」是两个不同的事实。
	// 折叠进 CardsCreated 会让一次「只提了观点、没沉淀知识」的加工在报告里
	// 看起来与「新建了知识卡」完全一样，而这正是本 Epic 要解决的混淆本身。
	OpinionsCreated []string
	OpinionsUpdated []string

	Material  []MaterialRecord
	Knowledge []KnowledgeRecord
	Questions []QuestionRecord

	// KnowledgeRemoved 是本次**物理移除**的论证关系（M3 的 remove_relation，A-24）。
	// BlocksReplaced 是本次被替换的「理解自检」当前有效块 locator（M3 的 replace_block）。
	// SectionsEdited 是本次被整段替换正文的文件路径（M3 的 edit_section，A-13）。
	KnowledgeRemoved []KnowledgeRecord
	BlocksReplaced   []string
	SectionsEdited   []string

	// Reviewed 是本次被补写**过目信号单键**的对象 ID（M4 · A-33 的 mark_reviewed 落盘）。
	// 刻意与 CardsUpdated 分开：过目维度与内容维度正交，「过目」不是一次内容更新，
	// 报告不得把它折叠进 cards.updated（否则一次对账会看起来像改过知识内容）。
	Reviewed []string

	// RecapsStaled 是本次被写上**失准标记两键**的综述 ID（M4 · A-33 的 set_stale 落盘）。
	// 同样刻意与 CardsUpdated / Reviewed 分开：失准标记既不是内容修改也不是过目信号，
	// 而且合同 §9 明文「不自动重算综述」——报告把它折叠进任何一个内容维度都会失真。
	RecapsStaled []string

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
	// fresh 记本次 plan 内**自己**写出的文件的最新 content_hash（rel → hash）。
	//
	// B3 防的是「自 eg context 读取以来**外部**改动」；同一个 plan 里前序 op 刚写过的
	// 文件不属于外部改动。合同 §2.1 的 non_core_supplement 行本就要求
	// `append_card` + `add_material_rel` 落在同一张卡上，若第二个 op 仍拿 plan.base 的
	// 旧 hash 去比对，必然自撞 B3 —— 这里改用前序写入后的真实 hash，B3 对外部改动的
	// 拦截强度不变（外部一改，fresh 与磁盘同样对不上）。
	fresh map[string]string
}

// Execute 按 `ops[]` 声明顺序逐个执行 Action。
//
// **调用前提**：res.Failed() 为假（有 error 级诊断时调用方必须零写入）。
// 任何单个 op 的跳过或失败都不中断后续 op：S1 明确接受部分成功，前提是如实上报。
func Execute(s *store.Store, res *Result, opt ExecOptions) *ExecResult {
	e := &executor{s: s, opt: opt, out: &ExecResult{}, fresh: map[string]string{}}
	for i := range res.Actions {
		e.run(res.Actions[i])
	}
	return e.out
}

// AtomicResult 是一次**原子预演**执行的结果（T-072 批次 B1）。
type AtomicResult struct {
	// Exec 是与 Execute 同源的完整账本：写 / 跳过 / 失败一条不折叠。
	Exec *ExecResult
	// WriteSet 是 accepted write-set：按首次写入路径顺序去重，每路径一个 FileSpec，
	// 目标为最终 staged 字节。仅当 Complete 为真时非空。
	WriteSet []store.AtomicFileSpec
	// Complete 为真表示预演期间无普通写失败（Failures 为空）；为假时 WriteSet 必为 nil，
	// 绝不把部分 staged 集合误当成功。跳过（B2/B3）不影响 Complete，也从不进入 WriteSet。
	Complete bool
}

// ExecuteAtomic 在 store 预演事务模式下按 `ops[]` 顺序执行 Action，复用 Execute 的
// **同一** run/action 分发（不复制 op 分发逻辑）。全程实盘与 Git 零变化：所有权威写
// 只落内存 overlay。
//
// 语义边界（与 Execute 逐条对齐，仅增原子预演口径）：
//   - skip / failure / 结果账本与 Execute 完全一致；
//   - 任一普通写失败（Exec.Failures 非空）→ Complete=false 且 WriteSet=nil；
//   - skip 在写口前返回、从不 stage，因此从不进入 accepted write-set；
//   - accepted write-set 按首次写入顺序去重，每路径一个 FileSpec，目标为最终 staged
//     字节，create 保持首次不存在事实；
//   - 本入口**不**调用 internal/txn（不 WriteIntent、不 Commit），不接 CLI / index。
func ExecuteAtomic(s *store.Store, res *Result, opt ExecOptions) (*AtomicResult, error) {
	return ExecuteAtomicFinal(s, res, opt, nil)
}

// AtomicFinalizer 是**预演收尾钩子**：在 overlay 仍然开着、accepted write-set 尚未定盘时
// 被调用**恰一次**（T-072 批次 B2a）。
//
// 为什么必须有它（最小 API 调整的理由）：一次 apply 里除了 `ops[]` 展开出的权威写，
// 还有一类「与本次写入同生共死」的收尾回写 —— 提案 `execution` 的逐路径回写。
// 它在 M3 是「执行完 → 回写 → 一起进同一个 commit」，到了 M6 就必须是「执行完 →
// 回写进**同一个 overlay** → 一起进同一份 intent / 同一次原子提交」。若把它留在
// ExecuteAtomic 之后再做，那次回写就会直落实盘、漏出原子域（既不在 intent 里、
// 也不受崩溃恢复保护），等于在事务外偷偷改权威 Markdown。
//
// 契约：
//   - 只在预演本体**无普通写失败**时才被调用（失败即整事务放弃，收尾无意义）；
//   - 钩子内的一切权威写仍然只落 overlay（store 仍处于预演模式），实盘零变化；
//   - 钩子可以往 ex 里追加事实，但**不得**自己提交、不得触碰 internal/txn；
//   - 钩子返回后才导出 accepted write-set，因此它的写入必然在同一份 write-set 里。
type AtomicFinalizer func(s *store.Store, ex *ExecResult)

// ExecuteAtomicFinal 是 ExecuteAtomic 带**预演收尾钩子**的完整形态（见 AtomicFinalizer）。
// fin 为 nil 时与 ExecuteAtomic 逐字等价。
func ExecuteAtomicFinal(s *store.Store, res *Result, opt ExecOptions,
	fin AtomicFinalizer) (*AtomicResult, error) {
	if err := s.BeginAtomic(); err != nil {
		return nil, err
	}
	defer s.EndAtomic()

	e := &executor{s: s, opt: opt, out: &ExecResult{}, fresh: map[string]string{}}
	for i := range res.Actions {
		e.run(res.Actions[i])
	}

	complete := len(e.out.Failures) == 0
	if complete && fin != nil {
		// overlay 仍然开着：钩子的权威写与 ops[] 的权威写落进**同一个** write-set。
		fin(s, e.out)
		// 钩子若追加了普通写失败，同样按「整事务放弃」处理（口径与 ops[] 失败一致）。
		complete = len(e.out.Failures) == 0
	}

	out := &AtomicResult{Exec: e.out, Complete: complete}
	if out.Complete {
		out.WriteSet = s.AtomicWriteSet()
	}
	return out, nil
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
	// Schema v2 观点两形态（落盘走 store.ApplyOpinion / ApplyOpinionAppend）。
	case ActOpinionNew:
		e.opinionNew(a)
	case ActOpinionAppend:
		e.opinionAppend(a)
	case ActMaterialRel:
		e.materialRelWrite(a)
	case ActRelation:
		e.relationWrite(a)
	case ActOpenQuestion:
		e.openQuestionWrite(a)
	// M3 两条非追加形态（执行体在 execute_m3.go）。
	case ActRemoveRelation:
		e.removeRelationWrite(a)
	case ActReplaceBlock:
		e.replaceBlockWrite(a)
	// M3 状态维度的两条形态（执行体在 execute_m3.go，落盘走 store.ApplyStateWrite）。
	case ActSetStatus:
		e.setStatusWrite(a)
	case ActSetReplacedBy:
		e.setReplacedByWrite(a)
	// A-13 载体：分区正文替换（执行体在 execute_m3.go，落盘走 store.ApplyReplaceSection）。
	case ActEditSection:
		e.editSectionWrite(a)
	// M4 · A-33：过目信号单键覆盖（执行体在 execute_m4.go，落盘同样走 store.ApplyStateWrite
	// 的**既有** reviewed_at 形态 —— 不新增 op、不新增写形态、矩阵不新增行）。
	case ActMarkReviewed:
		e.markReviewedWrite(a)
	// M4 · A-33：综述失准标记两键（执行体在 execute_m4.go，落盘走 store.ApplyStateWrite
	// 的**第五种**形态 stale —— 两键一次守卫写，矩阵仍不新增行：命中既有 #33）。
	case ActSetStale:
		e.setStaleWrite(a)
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
		Rel:        a.Path,
		ID:         model.NoteID(a.ID),
		SourceID:   model.SourceID(op.Source),
		Title:      op.Title,
		Date:       e.dateOf(),
		Stamp:      e.opt.Stamp,
		Tags:       op.Tags,
		Sections:   sectionAppends(a.Sections),
		Extraction: a.Extraction,
		Inbox:      store.InboxSpec{ExpectedHash: e.baseHash(store.UnprocessedFile)},
	})
	if !e.record(a, out.Note, err) {
		return
	}
	e.out.Note = NoteRecord{ID: a.ID, Domain: a.Domain, Path: a.Path, Reused: out.Reused}
	// EG-SRC-02：条目未成功移出必须显式上报，绝不静默。
	e.inbox(a, op.Source, out.Inbox, out.InboxSkip, "收件区条目未移出")
}

func (e *executor) noteAppend(a Action) {
	res, err := e.s.WriteGuarded(a.Path, e.expect(a), store.Edit{
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
		e.addMaterial(MaterialRecord{
			Card: a.ID, Source: ref.Source, Note: ref.Note, Rel: ref.Rel, Reason: ref.Reason,
		})
	}
	e.impact(ImpactNewCoreCard, a.ID, a.OpIndex, "新建知识卡：承载本次加工的新核心含义")
}

func (e *executor) cardAppend(a Action) {
	res, err := e.s.ApplyCardAppend(store.CardAppendSpec{
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
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

// opinionNew 落盘一条新观点。
//
// 与 cardNew 的三处刻意差异：
//
//	① 走 ApplyOpinion（观点目录 + 五分区模板 + `validation: pending`）；
//	② 记进 OpinionsCreated 而不是 CardsCreated —— 报告里「新建了几条知识」与
//	   「新建了几条观点」必须分得开，否则 §5.2 的检索拆分在报告侧又被合并回去；
//	③ **不产出 ImpactNewCoreCard**。影响面登记回答的是「本次加工对知识库的核心含义
//	   做了什么」，而新建观点恰恰**没有**动核心知识——它新增了一条待验证的判断。
//	   套用知识卡的影响面会让收敛报告把「提了个观点」读成「改了知识」。
//
// 材料关系照样逐条登记：观点与知识同源同据，`sources[]` 是它的硬前提。
func (e *executor) opinionNew(a Action) {
	op := a.Op
	res, err := e.s.ApplyOpinion(store.OpinionSpec{
		Rel:      a.Path,
		ID:       model.OpinionID(a.ID),
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
	e.out.OpinionsCreated = append(e.out.OpinionsCreated, a.ID)
	for _, ref := range op.Sources {
		e.addMaterial(MaterialRecord{
			Card: a.ID, Source: ref.Source, Note: ref.Note, Rel: ref.Rel, Reason: ref.Reason,
		})
	}
}

// opinionAppend 向已有观点追加「论据与推理」/「条件与反例」/「待验证」。
//
// 同样不产出影响面记录：补充论据不改变任何知识卡的核心含义，
// 它改变的是这条观点自身的论证进度，而那由 `validation` 表达（且只由用户流转）。
func (e *executor) opinionAppend(a Action) {
	res, err := e.s.ApplyOpinionAppend(store.OpinionAppendSpec{
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
		Stamp:        e.opt.Stamp,
		Sections:     sectionAppends(a.Sections),
	})
	if !e.record(a, res, err) {
		return
	}
	e.out.OpinionsUpdated = append(e.out.OpinionsUpdated, a.ID)
}

func (e *executor) materialRelWrite(a Action) {
	ref := a.Material
	if ref == nil {
		return
	}
	res, err := e.s.ApplyMaterialRel(store.MaterialRelSpec{
		Rel:          a.Path,
		ExpectedHash: e.expect(a),
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
	e.addMaterial(MaterialRecord{
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
		ExpectedHash: e.expect(a),
		Stamp:        e.opt.Stamp,
		From:         model.CardID(a.ID),
		Relation: model.Relation{
			Type:   model.RelationType(rel.Type),
			Target: model.RelationEndpoint(rel.Target),
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
		ExpectedHash: e.expect(a),
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
			Path: opPath(a.OpIndex, ""),
			// I-…-010：M6 原子事务下「其余 op 照常执行，磁盘保留现状」已不成立 ——
			// 任一普通写失败即整事务放弃、零权威写入、无 commit（见 ExecResult.Complete）。
			Message: fmt.Sprintf("写入 %s 失败：%v（原子事务：本次零权威写入、无 commit，"+
				"磁盘保持事务开始前的状态）", path, err),
			Target: a.ID,
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
		e.noteFresh(path, res.Hash)
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
		e.noteFresh(res.Path, res.Hash)
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

// addMaterial 登记一条材料关系事实。四要素逐字相同的重复条目在卡上被 store 幂等去重
// （sources[] 只会有一条），报告同样只登记一条 —— 报告必须与磁盘一致。
func (e *executor) addMaterial(rec MaterialRecord) {
	for _, exist := range e.out.Material {
		if exist == rec {
			return
		}
	}
	e.out.Material = append(e.out.Material, rec)
}

func (e *executor) impact(kind, target string, opIndex int, detail string) {
	e.out.HighImpact = append(e.out.HighImpact,
		ImpactItem{Kind: kind, Target: target, Detail: detail, OpIndex: opIndex})
}

// baseHash 取某个文件在本次写入前应有的 content_hash：本 plan 已写过就用写后的真值，
// 否则用 plan.base 里 Agent 逐字抄来的值。
func (e *executor) baseHash(key string) string {
	if h, ok := e.fresh[key]; ok {
		return h
	}
	return e.opt.Base[key]
}

// expect 返回 action 应带的 expectedHash（同上口径，作用于 Action.Path）。
func (e *executor) expect(a Action) string {
	if h, ok := e.fresh[a.Path]; ok {
		return h
	}
	return a.ExpectedHash
}

// noteFresh 登记「本 plan 刚把该文件写成这个样子」。
func (e *executor) noteFresh(path, hash string) {
	if path == "" || hash == "" {
		return
	}
	e.fresh[path] = hash
}

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
