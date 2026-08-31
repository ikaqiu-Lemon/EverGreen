package plan

// ChangePlan 校验（E1–E6 / W1–W8 / I1）与 op → store 展开。
//
// 口径是**宽松**的：只有会导致「数据无法解析、或关系无法定位」的问题才拦（error → 零写入），
// 其余一律照写 + 进报告。W1 / W2 / W4 / W6 在 S1 一律 warning，**严禁**提前升级为 error
// （严格化属 S5 / M6）。本包只做「校验 + 展开」，不写盘、不 commit。

import (
	"fmt"

	"evergreen/internal/model"
	"evergreen/internal/rules"
	"evergreen/internal/store"
)

// ActionKind 是一条 op 展开出的写入形态（封闭集合）。
type ActionKind string

// 写入形态。Reuse 形态是「命中已有产物、按 S1 口径复用不重写」，零写入但要进报告。
const (
	ActSourceNew    ActionKind = "source_new"
	ActSourceReuse  ActionKind = "source_reuse"
	ActNoteNew      ActionKind = "note_new"
	ActNoteReuse    ActionKind = "note_reuse"
	ActNoteAppend   ActionKind = "note_append"
	ActCardNew      ActionKind = "card_new"
	ActCardAppend   ActionKind = "card_append"
	ActMaterialRel  ActionKind = "material_rel"
	ActRelation     ActionKind = "relation"
	ActOpenQuestion ActionKind = "open_question"
)

// SectionWrite 是一次分区追加（载荷逐字，必须以换行结束；本包不改写一个字节）。
type SectionWrite struct {
	Section string
	Payload []byte
}

// RelationWrite 是一条论证关系的写入意图（方向已规范化）。
type RelationWrite struct {
	Type   string
	Target string
	Reason string
	// Duplicate 为真表示同对已存在：**不追加第二条**（幂等），只进报告。
	Duplicate bool
}

// Action 是展开结果：按 ops[] 声明顺序执行，实际落盘由 store 的字节区间插入完成。
type Action struct {
	Kind         ActionKind
	OpIndex      int
	Op           *Op
	Domain       string
	ID           string
	Path         string
	ExpectedHash string
	Sections     []SectionWrite
	Material     *MaterialRef
	Relation     *RelationWrite
	OutputCards  []OutputCard
	Gaps         []string

	// 跳过标记（W6：base 未覆盖该文件）。命名口径见 store.SkipReason / store.CauseFor。
	Skip       bool
	SkipKind   store.SkipReason
	SkipCause  string
	SkipDetail string
}

// Result 是一次校验 + 展开的结果。
type Result struct {
	Verb   string
	Domain string
	// DomainFallback 为真表示 plan.domain 缺失、已按 evergreen.yml 的 default_domain 落位。
	DomainFallback bool
	// DomainUnavailable 为真表示既没有 plan.domain 也没有 default_domain（EG-DOM-03：
	// CLI 绝不自选领域，由调用方退 1）。
	DomainUnavailable bool
	// ZeroWrite 为真表示 ops[] 为空（零写入报告，退 0；零知识结果合法）。
	ZeroWrite bool

	Errors      []Diagnostic
	Warnings    []Diagnostic
	Actions     []Action
	Convergence []Convergence
}

// Failed 报告是否有 error 级诊断（调用方据此零写入并退 2）。
func (r *Result) Failed() bool { return len(r.Errors) > 0 }

// Env 是校验所需的**只读**库侧视图。
type Env struct {
	// Index 是 id → vault 内相对路径（全库扫描结果，F2：关系只引用 ID）。
	Index store.Index
	// Read 按相对路径读取字节（只读；用于 E4 与既有关系的解析）。nil 表示无库可读。
	Read func(rel string) ([]byte, error)
	// DefaultDomain 是 evergreen.yml 的 default_domain（plan.domain 缺失时的落位依据）。
	DefaultDomain string
}

// EnvFor 从 store 建立只读视图（一次全库扫描，S1 接受 O(N) 代价）。
func EnvFor(s *store.Store, defaultDomain string) (Env, error) {
	idx, err := s.ScanIDs()
	if err != nil {
		return Env{}, err
	}
	return Env{Index: idx, DefaultDomain: defaultDomain, Read: func(rel string) ([]byte, error) {
		f, err := s.Read(rel)
		if err != nil {
			return nil, err
		}
		return f.Bytes, nil
	}}, nil
}

// validator 串起一次校验：诊断收集 + 本 plan 内已声明 ID 的登记。
type validator struct {
	p   *ChangePlan
	env Env
	res *Result
	// pending 是本 plan 内将新建的 id → 将建相对路径（同一 plan 内的前后引用要能解析）。
	pending map[string]string
	// declared 是本 plan 内已声明的新产物 id → 首次声明的 op 下标（plan 内重复也是 E1）。
	declared map[string]int
}

// Validate 校验并展开一份 plan。error 非空时**不得执行任何 action**（零写入）。
func Validate(p *ChangePlan, env Env) *Result {
	v := &validator{p: p, env: env, res: &Result{Convergence: p.Convergence},
		pending: map[string]string{}, declared: map[string]int{}}
	for _, d := range p.Diags {
		v.add(d)
	}
	v.planLevel()
	v.convergence()
	for _, op := range p.Ops {
		v.op(op)
	}
	return v.res
}

func (v *validator) add(d Diagnostic) {
	if d.Level == LevelError {
		v.res.Errors = append(v.res.Errors, d)
		return
	}
	v.res.Warnings = append(v.res.Warnings, d)
}

// planLevel 校验顶层八键。
func (v *validator) planLevel() {
	p, res := v.p, v.res

	// plan_version：缺失或 != 1 → E5（零写入）。
	if p.VersionRaw == nil {
		v.add(errorAt(E5, NonOp, "plan_version", "plan_version 缺失：S1 只支持 plan_version=%d", PlanVersion))
	} else if p.Version != PlanVersion {
		v.add(errorAt(E5, NonOp, "plan_version",
			"plan_version=%v 不被支持：S1 只支持 plan_version=%d", p.VersionRaw, PlanVersion))
	}

	// verb：缺失或未知值 → 未编号 warning + 退化为 process。
	res.Verb = string(model.VerbProcess)
	if verb, ok := model.NormalizeVerb(p.Verb); ok {
		res.Verb = string(verb)
	} else {
		v.add(Diagnostic{Code: Unnumbered, Level: LevelWarning, Path: "verb", OpIndex: NonOp,
			Message: fmt.Sprintf("%s（原值 %q）", VerbUnknownNotice, p.Verb)})
	}

	// domain：缺失 / 空串 → 按 default_domain 落位并如实说明；两者皆无 → 由调用方退 1。
	switch {
	case p.DomainNull:
		res.Domain = ""
	case p.Domain != "":
		res.Domain = p.Domain
	default:
		res.Domain = v.env.DefaultDomain
		res.DomainFallback = true
		if res.Domain == "" {
			res.DomainUnavailable = true
		} else {
			v.add(infoAt(NonOp, "plan.domain",
				"plan.domain 缺失，已按 evergreen.yml 的 default_domain=%q 落位", res.Domain))
		}
	}

	if p.Reason == "" {
		v.add(infoAt(NonOp, "reason", "reason 缺失：commit 正文不输出 Reason: 行"))
	}
	if len(p.RequirementIDs) == 0 {
		v.add(infoAt(NonOp, "requirement_ids", "requirement_ids 缺失：commit 正文不输出 Requirement: 行"))
	}
	if len(p.Ops) == 0 {
		res.ZeroWrite = true
		v.add(Diagnostic{Code: Unnumbered, Level: LevelWarning, Path: "ops", OpIndex: NonOp,
			Message: OpsEmptyNotice})
	}
}

// convergence 校验收敛条目（异常一律并入 W5，不拦截、不新增编号）。
func (v *validator) convergence() {
	if len(v.p.Convergence) == 0 {
		if v.touchesExistingCard() {
			v.add(warnAt(W5, NonOp, "convergence",
				"convergence[] 缺条目：涉及已有卡的加工应逐卡给出三维度结论与处理关系"))
		}
		return
	}
	relations := set(ConvergeRelations())
	for _, c := range v.p.Convergence {
		base := fmt.Sprintf("convergence[%d]", c.Index)
		if _, err := model.ParseCardID(c.Card); err != nil {
			v.add(warnAt(W5, NonOp, base+".card", "card 缺失或格式非法：%v", err))
		}
		dims := rules.Dims{
			Core:         rules.ParseVerdict(c.Core),
			Conditions:   rules.ParseVerdict(c.Conditions),
			ReusePurpose: rules.ParseVerdict(c.ReusePurpose),
		}
		if !dims.Complete() {
			v.add(warnAt(W5, NonOp, base,
				"三维度不齐（core_knowledge / conditions / reuse_purpose 取值须为 same 或 different）"))
		}
		switch {
		case !c.RelationGiven || c.Relation == "":
			v.add(warnAt(W5, NonOp, base+".relation",
				"relation 缺失：七值封闭枚举 %v", ConvergeRelations()))
		case !relations[c.Relation]:
			v.add(warnAt(W5, NonOp, base+".relation",
				"relation=%q 不在七值封闭枚举内 %v，已照写", c.Relation, ConvergeRelations()))
		case !rules.ConsistentWithDims(c.Relation, dims):
			d := v.dimsConflict(base, c, dims)
			v.add(d)
		}
	}
}

func (v *validator) dimsConflict(base string, c Convergence, dims rules.Dims) Diagnostic {
	d := warnAt(W5, NonOp, base+".relation",
		"relation=%q 与三维度结论矛盾（三维度全 same 应为 same_semantics，否则应拆两张卡：%s），已照写",
		c.Relation, rules.Converge(dims))
	d.Target = c.Card
	return d
}

// touchesExistingCard 报告本 plan 是否改动了已有卡（W5「缺条目」的触发前提）。
func (v *validator) touchesExistingCard() bool {
	for _, op := range v.p.Ops {
		switch op.Name {
		case OpAppendCard, OpAddRelation, OpAddMaterialRel:
			return true
		}
	}
	return false
}

// op 分发单条 op 的校验与展开。未知 op → E5，整条不执行。
func (v *validator) op(op *Op) {
	switch op.Name {
	case OpAddSource:
		v.addSource(op)
	case OpWriteNote:
		v.writeNote(op)
	case OpCreateCard:
		v.createCard(op)
	case OpAppendCard:
		v.appendCard(op)
	case OpAddMaterialRel:
		v.materialRel(op)
	case OpAddRelation:
		v.relation(op)
	case OpAddOpenQuestion:
		v.openQuestion(op)
	default:
		hint := ""
		for _, s2 := range s2OpNames() {
			if op.Name == s2 {
				hint = "（该 op 属 S2，S1 不实现）"
			}
		}
		v.add(errorAt(E5, op.Index, opPath(op.Index, "op"),
			"未知 op %q%s：整条 op 不执行；S1 只有七个 op %v", op.Name, hint, OpNames()))
	}
}

// —— 通用判据 ——

// resolve 解析一个 ID 到 vault 内相对路径：先看全库扫描结果，再看本 plan 内将新建的产物。
func (v *validator) resolve(id string) (string, bool) {
	if rel, err := v.env.Index.Resolve(id); err == nil {
		return rel, true
	}
	if rel, ok := v.pending[id]; ok {
		return rel, true
	}
	return "", false
}

// declare 登记本 plan 将新建的产物 id（E1：格式非法 / 全库重复 / plan 内重复）。
func (v *validator) declare(op *Op, field, id, rel string) bool {
	if prev, ok := v.declared[id]; ok {
		v.add(errorAt(E1, op.Index, opPath(op.Index, field),
			"id %s 在本 plan 内重复声明（首次出现在 ops[%d]）", id, prev))
		return false
	}
	if existing, err := v.env.Index.Resolve(id); err == nil {
		v.add(errorAt(E1, op.Index, opPath(op.Index, field),
			"id %s 与全库既有产物重复（%s）", id, existing))
		return false
	}
	v.declared[id] = op.Index
	v.pending[id] = rel
	return true
}

// frontmatterCheck 判 E4（目标文件 frontmatter YAML 无法解析）。
// 返回 false 表示已登记 E4，调用方必须中止该 op；读不到文件不是错误
// （本 plan 内刚声明、尚未落盘的产物由后续 action 新建）。
func (v *validator) frontmatterCheck(op *Op, field, rel string) bool {
	raw, ok := v.readExisting(rel)
	if !ok {
		return true
	}
	var fm map[string]interface{}
	if err := store.FrontmatterInto(raw, &fm); err != nil {
		v.add(errorAt(E4, op.Index, opPath(op.Index, field),
			"%s 的 frontmatter 无法解析：%v", rel, err))
		return false
	}
	return true
}

// readExisting 只读取既有文件字节；读不到返回 false（不产生诊断）。
func (v *validator) readExisting(rel string) ([]byte, bool) {
	if v.env.Read == nil {
		return nil, false
	}
	raw, err := v.env.Read(rel)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// domainCheck 判 W1：写入目标落在 plan.domain 之外（照常写入，退出码不变）。
func (v *validator) domainCheck(op *Op, field, rel string) {
	target := store.DomainOf(rel)
	if target == "" || v.res.Domain == "" || target == v.res.Domain {
		return
	}
	d := warnAt(W1, op.Index, opPath(op.Index, field),
		"写入目标落在 plan.domain=%q 之外（实际领域 %q），已照常写入", v.res.Domain, target)
	d.Target = rel
	v.add(d)
}

// baseCheck 判 W6：base 未覆盖该既有文件 → 跳过该文件（其余 op 照常执行）。
// 覆盖时把 content_hash 带给 action，由 store 在写前做 B3 比对。
func (v *validator) baseCheck(op *Op, id, rel string, act *Action) {
	if hash, ok := v.p.Base[id]; ok && hash != "" {
		act.ExpectedHash = hash
		return
	}
	if hash, ok := v.p.Base[rel]; ok && hash != "" {
		act.ExpectedHash = hash
		return
	}
	act.Skip = true
	act.SkipKind = store.SkipFileChanged
	act.SkipCause = store.CauseFor(store.SkipFileChanged)
	act.SkipDetail = fmt.Sprintf("base 未覆盖 %s：无法确认文件自 eg context 读取以来未变化，已跳过该文件", rel)
	d := warnAt(W6, op.Index, fmt.Sprintf("base[%q]", id),
		"base 未覆盖被改文件 %s：已跳过该文件，其余 op 照常执行", rel)
	d.Target = id
	v.add(d)
}

// sectionPayloads 把 sections 映射按**固定分区顺序**（F5）展开成写入序列，并做 E6 / I1 判定。
// writable 为该 op 允许的分区白名单；nil 表示该类型的全部固定分区（新建路径）。
func (v *validator) sectionPayloads(op *Op, kind store.Kind, writable []string) []SectionWrite {
	if len(op.Sections) == 0 {
		return nil
	}
	allowed := set(writable)
	known := set(store.KnownSections(kind))
	var out []SectionWrite
	for _, name := range store.KnownSections(kind) {
		payload, ok := op.Sections[name]
		if !ok {
			continue
		}
		if name == store.SecUserAppend {
			v.add(errorAt(E6, op.Index, opPath(op.Index, "sections.用户补充"),
				"「用户补充」任何时候都不得写入（安全底线 B2）：目标文件字节不变"))
			continue
		}
		if !allowed[name] {
			v.add(errorAt(E6, op.Index, opPath(op.Index, "sections."+name),
				"「%s」对自动路径只读：本 op 只允许追加 %v", name, writable))
			continue
		}
		if len(payload) == 0 {
			v.add(infoAt(op.Index, opPath(op.Index, "sections."+name), "分区载荷为空，已跳过该分区"))
			continue
		}
		out = append(out, SectionWrite{Section: name, Payload: lineTerminated(payload)})
	}
	for name := range op.Sections {
		if known[name] {
			continue
		}
		v.add(infoAt(op.Index, opPath(op.Index, "sections."+name),
			"未知分区名已原样忽略（%s 的固定分区是 %v）", kind, store.KnownSections(kind)))
	}
	return out
}

// lineTerminated 保证载荷以换行结束（mdfile 绝不替调用方补字节，因此在这里补齐；
// 只可能在**尾部**追加一个 \n，不改写载荷已有的任何字节）。
func lineTerminated(payload []byte) []byte {
	if len(payload) > 0 && payload[len(payload)-1] == '\n' {
		return payload
	}
	out := make([]byte, 0, len(payload)+1)
	out = append(out, payload...)
	return append(out, '\n')
}

// —— 七个 op ——

// addSource 校验并展开 add_source（原文 + 收件区条目）。
func (v *validator) addSource(op *Op) {
	if op.URL == "" && op.Title == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "url"),
			"add_source 的 url 与 title 同时缺失：判重无键可用（零写入）"))
		return
	}
	id := op.SourceID
	if id != "" {
		if _, err := model.ParseSourceID(id); err != nil {
			v.add(errorAt(E1, op.Index, opPath(op.Index, "source_id"), "source_id 格式非法：%v", err))
			return
		}
	}
	if op.Reason == "" {
		v.add(infoAt(op.Index, opPath(op.Index, "reason"), "add_source 未给收录理由（reason 为空）"))
	}
	act := Action{Kind: ActSourceNew, OpIndex: op.Index, Op: op, ID: id, Domain: v.res.Domain}
	if id != "" {
		if rel, ok := v.resolve(id); ok {
			// 命中已有原文：复用、正文不覆盖（判重键是 URL / 标题，理由追加）。
			act.Kind, act.Path = ActSourceReuse, rel
			v.add(infoAt(op.Index, opPath(op.Index, "source_id"),
				"原文 %s 已存在（%s）：复用、正文不覆盖", id, rel))
			v.res.Actions = append(v.res.Actions, act)
			return
		}
	}
	if len(op.Body) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "body"),
			"add_source 新建原文缺 body：原文没有正文不成立（零写入）"))
		return
	}
	if id == "" {
		id = string(model.NewSourceID(v.date(op.SavedAt), op.Title))
		act.ID = id
	}
	act.Path = store.SourceRel(id)
	if !v.declare(op, "source_id", id, act.Path) {
		return
	}
	v.res.Actions = append(v.res.Actions, act)
}

// writeNote 校验并展开 write_note（材料笔记五分区 + 收件区条目移出）。
func (v *validator) writeNote(op *Op) {
	if op.Source == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "source"),
			"write_note 缺 source：材料笔记必须指向可回读原文"))
		return
	}
	if _, err := model.ParseSourceID(op.Source); err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "source"), "source 无法解析：%v", err))
		return
	}
	if _, ok := v.resolve(op.Source); !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "source"),
			"source %s 不存在于全库：关系无法定位", op.Source))
		return
	}
	domain := v.opDomain(op)
	id := op.NoteID
	if id != "" {
		if _, err := model.ParseNoteID(id); err != nil {
			v.add(errorAt(E1, op.Index, opPath(op.Index, "note_id"), "note_id 格式非法：%v", err))
			return
		}
	}
	sections := v.sectionPayloads(op, store.KindNote, store.NoteSections())
	v.coverageGaps(op)

	act := Action{Kind: ActNoteNew, OpIndex: op.Index, Op: op, ID: id, Domain: domain,
		Sections: sections, OutputCards: op.OutputCards, Gaps: op.Gaps}
	if id != "" {
		if rel, ok := v.resolve(id); ok {
			act.Path = rel
			v.domainCheck(op, "note_id", rel)
			if !v.frontmatterCheck(op, "note_id", rel) {
				return
			}
			if !op.Reprocess {
				// 默认不重复加工：笔记已存在即复用不重写（EG-NOTE-05），零写入。
				act.Kind = ActNoteReuse
				v.add(infoAt(op.Index, opPath(op.Index, "note_id"),
					"笔记 %s 已存在（%s）：默认复用不重写；重新加工须显式给 reprocess: true", id, rel))
				v.res.Actions = append(v.res.Actions, act)
				return
			}
			act.Kind = ActNoteAppend
			v.baseCheck(op, id, rel, &act)
			v.res.Actions = append(v.res.Actions, act)
			return
		}
	}
	if len(sections) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "sections"),
			"write_note 缺 sections：至少要写「%s」", store.SecDigest))
		return
	}
	if !hasSection(sections, store.SecDigest) {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "sections"),
			"write_note 缺「%s」：材料提炼是加工产出的落点", store.SecDigest))
		return
	}
	if domain == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "domain"),
			"无法确定落位领域：plan.domain 与 default_domain 都未给出（CLI 绝不自选领域）"))
		return
	}
	if id == "" {
		id = string(model.NewNoteID(v.date(""), op.Title))
		act.ID = id
	}
	act.Path = store.NoteRel(domain, id)
	if !v.declare(op, "note_id", id, act.Path) {
		return
	}
	v.res.Actions = append(v.res.Actions, act)
}

// coverageGaps 只做受控枚举校验与**原样透传**：不读正文、不推断缺失项、不补全。
func (v *validator) coverageGaps(op *Op) {
	if len(op.Gaps) == 0 {
		return // 缺该字段或空数组 → 视为「无缺失」，不报错、不告警。
	}
	allowed := set(CoverageGaps())
	for i, g := range op.Gaps {
		if allowed[g] {
			continue
		}
		v.add(infoAt(op.Index, fmt.Sprintf("ops[%d].coverage_gaps[%d]", op.Index, i),
			"coverage_gaps 取值 %q 不在受控枚举内 %v：已原样保留并进报告", g, CoverageGaps()))
	}
}

// createCard 校验并展开 create_card（新建知识卡，五分区都可写）。
func (v *validator) createCard(op *Op) {
	if op.Title == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "title"), "create_card 缺 title：卡片标题是 H1 与 slug 来源"))
		return
	}
	// V3/V7（EG-SRC-04）：建卡必须带材料关系——缺 sources[] 或空数组一律拒绝建卡。
	if !op.SourcesGiven || len(op.Sources) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "sources"),
			"create_card 缺 sources[]（或为空数组）：每次知识加工必须有可回读文章作依据，新建卡必须建立材料关系"))
		return
	}
	domain := v.opDomain(op)
	id := op.CardID
	if id != "" {
		if _, err := model.ParseCardID(id); err != nil {
			v.add(errorAt(E1, op.Index, opPath(op.Index, "card_id"), "card_id 格式非法：%v", err))
			return
		}
	}
	sections := v.sectionPayloads(op, store.KindCard, []string{store.SecKnowledge,
		store.SecRationale, store.SecBoundary, store.SecSelfCheck})
	if !hasSection(sections, store.SecKnowledge) {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "sections"),
			"create_card 缺「%s」：卡片没有知识内容就不成立", store.SecKnowledge))
		return
	}
	refs := v.materialRefs(op)
	if v.res.Failed() {
		return
	}
	if domain == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "domain"),
			"无法确定落位领域：plan.domain 与 default_domain 都未给出（CLI 绝不自选领域）"))
		return
	}
	if id == "" {
		id = string(model.NewCardID(v.date(""), op.Title))
	}
	act := Action{Kind: ActCardNew, OpIndex: op.Index, Op: op, ID: id, Domain: domain,
		Path: store.CardRel(domain, id), Sections: sections}
	if len(refs) > 0 {
		act.Material = &refs[0]
	}
	if !v.declare(op, "card_id", id, act.Path) {
		return
	}
	v.res.Actions = append(v.res.Actions, act)
}

// appendCard 校验并展开 append_card（对已有卡只追加三分区）。
func (v *validator) appendCard(op *Op) {
	rel, ok := v.cardTarget(op, "card", op.Card)
	if !ok {
		return
	}
	sections := v.sectionPayloads(op, store.KindCard, store.AutoWritableSections(store.KindCard))
	if v.res.Failed() {
		return
	}
	if len(sections) == 0 {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "sections"),
			"append_card 缺 sections：至少要追加一个分区（%v）", store.AutoWritableSections(store.KindCard)))
		return
	}
	act := Action{Kind: ActCardAppend, OpIndex: op.Index, Op: op, ID: op.Card,
		Domain: store.DomainOf(rel), Path: rel, Sections: sections}
	v.baseCheck(op, op.Card, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// openQuestion 校验并展开 add_open_question（笔记「存疑与待验证」追加一条）。
func (v *validator) openQuestion(op *Op) {
	if op.Note == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "note"), "add_open_question 缺 note：存疑的落点是材料笔记分区"))
		return
	}
	if _, err := model.ParseNoteID(op.Note); err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "note"), "note 无法解析：%v", err))
		return
	}
	rel, ok := v.resolve(op.Note)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "note"), "note %s 不存在于全库：无法定位落点", op.Note))
		return
	}
	if op.Question == "" {
		v.add(errorAt(E5, op.Index, opPath(op.Index, "question"), "add_open_question 的 question 为空：op 字段不成立"))
		return
	}
	if !v.frontmatterCheck(op, "note", rel) {
		return
	}
	v.domainCheck(op, "note", rel)
	act := Action{Kind: ActOpenQuestion, OpIndex: op.Index, Op: op, ID: op.Note,
		Domain: store.DomainOf(rel), Path: rel}
	v.baseCheck(op, op.Note, rel, &act)
	v.res.Actions = append(v.res.Actions, act)
}

// —— 小工具 ——

// opDomain 取该 op 的落位领域：op.domain 优先，其次 plan.domain / default_domain。
func (v *validator) opDomain(op *Op) string {
	if op.DomainGiven && op.Domain != "" {
		if v.res.Domain != "" && op.Domain != v.res.Domain {
			d := warnAt(W1, op.Index, opPath(op.Index, "domain"),
				"op 的 domain=%q 落在 plan.domain=%q 之外，已照常写入", op.Domain, v.res.Domain)
			v.add(d)
		}
		return op.Domain
	}
	return v.res.Domain
}

// cardTarget 解析一个必填的知识卡目标（E2 / E3 / E4 集中判定）。
func (v *validator) cardTarget(op *Op, field, id string) (string, bool) {
	if id == "" {
		v.add(errorAt(E2, op.Index, opPath(op.Index, field), "%s 缺 %s：目标卡无法定位", op.Name, field))
		return "", false
	}
	if _, err := model.ParseCardID(id); err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, field), "%s 无法解析为知识卡 ID：%v", field, err))
		return "", false
	}
	rel, ok := v.resolve(id)
	if !ok {
		v.add(errorAt(E2, op.Index, opPath(op.Index, field), "%s %s 不存在于全库：关系无法定位", field, id))
		return "", false
	}
	if !v.frontmatterCheck(op, field, rel) {
		return "", false
	}
	v.domainCheck(op, field, rel)
	return rel, true
}

func (v *validator) date(savedAt string) model.Date {
	if savedAt != "" {
		if s, err := model.ParseStamp(savedAt); err == nil {
			return model.NewDate(s.Time())
		}
	}
	return model.NewDate(nowFn())
}

func hasSection(list []SectionWrite, name string) bool {
	for _, s := range list {
		if s.Section == name {
			return true
		}
	}
	return false
}
