package proposal

// `superseded` 的**两个触发**与 `approve` 执行前的影响面重算改判
// （提案合同 §3 全节、§2.3 的 T3 / T4、§5.3 第 8 / 9a 步、§8.2.1 的 E7 行；
// T-evergreen.s1_main_flow-158614-035）。
//
// # 两个触发（§3 表，逐字）
//
//	S-① 部分接受   用户只接受提案的**一部分内容**
//	               ① 生成**新提案**承载实际接受内容 ② 原提案 status = superseded
//	               ③ 原提案写 decision.superseded_by = <新提案 ID>          三步缺一不可
//	S-② 前提变化   `eg proposal approve` 执行前重算影响面，与提案里的 impact **不一致**
//	               ① **不强行执行** ② 生成新提案重新描述当前影响面
//	               ③ 原提案 status = superseded + decision.superseded_by
//
// 「**两者都必须写 `decision.superseded_by`**」：缺失或无法解析到一个**存在的**提案 ID
// 即提案链断裂 —— CheckSupersededChain 返回结构化违规，037 映射为 M3 的第一个新增 error、
// CLI 退 ExitCodeValidation 且零写入（本包**不出现任何编号字面量**）。
//
// # 唯一实现顺序（§3「S-② 的硬要求」逐字）
//
//	「`eg proposal approve` **必须**在执行前重算影响面并与 `impact` 比对。
//	 「先批准后重算」是本触发的**唯一**实现顺序，不得省略重算直接执行。」
//
// 本文件把它落成不可绕过的代码路径：DecideApprove 是「能否继续执行」的**唯一**裁决口，
// 它的入参**就是**重算结果 —— 拿不到重算结果就得不到裁决，因此「省略重算直接执行」
// 在类型层就写不出来（CLI 侧的调用顺序由 internal/cli 的注入计数器用例再钉一道）。
//
// # 状态判定一律复用 T-…-034（不另写一套）
//
//	CheckTransition            → T3（pending → superseded）/ T4（approved → superseded）合法性，
//	                             非法边（含终态出边）在这里就被拒，写口永不被调用
//	LegalEdge + CheckRequiredFields → T3 / T4 的**必写字段**（decision.result + decision.superseded_by）
//	CheckDecisionConsistency   → 写后复核 status ⟷ decision.result 逐字一致（A-20）
//	CheckStatus                → 四态封闭
//
// # 落盘口径（A-23 + B1–B4）
//
//   - 新提案：RenderTemplate 字节拼装 → store 的提案 guarded 创建写口（不覆盖既有文件）；
//   - 原提案：mdfile 的 Doc/Span 半开区间 + **字节级行替换**（未知分区、未知 YAML 键、
//     注释、空行逐字保留）→ store 的提案 guarded 改写写口（B3 content_hash 比对）；
//   - 写前 Parse→Render 字节自检 + ValidateLayout + 状态一致性复核，任一不过**拒写**；
//   - **任一步失败按 B4 保留现状**：不回滚已完成的步骤、不做任何破坏性动作，
//     已完成 / 未完成的步骤逐条回给调用方进报告（Steps / Message，报告由 CLI 组）。

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— ① 触发枚举（恰两个，§3 的 S-① / S-②）——

// Trigger 是 `superseded` 的触发种类。
type Trigger string

// 两个触发（封闭集合，不存在第三个）。
const (
	// TriggerPartialAccept 是 S-①「部分接受」：用户只接受提案的一部分内容。
	TriggerPartialAccept Trigger = "partial_accept"
	// TriggerImpactChanged 是 S-②「前提变化」：执行前重算影响面与 impact 不一致。
	TriggerImpactChanged Trigger = "impact_changed"
)

// Triggers 返回触发的封闭集合（顺序 = §3 表的 S-① / S-② 行序）。
func Triggers() []Trigger { return []Trigger{TriggerPartialAccept, TriggerImpactChanged} }

// Valid 报告触发取值是否在封闭集合内。
func (t Trigger) Valid() bool {
	for _, v := range Triggers() {
		if t == v {
			return true
		}
	}
	return false
}

// Label 返回触发的中文名（只进报告文本，不参与判定）。
func (t Trigger) Label() string {
	switch t {
	case TriggerPartialAccept:
		return "触发① 部分接受"
	case TriggerImpactChanged:
		return "触发② 前提变化"
	default:
		return string(t)
	}
}

// NotExecutedNotice 是触发② 的**逐字**报告用语（§5.3 第 9a 步「不执行删除，进报告」）。
//
// 报告里必须逐字出现它：用户看到的结论是「本次**不执行删除**」，而不是「已执行」。
const NotExecutedNotice = "不执行删除"

// —— ② E7 的结构化判定：superseded 必须能解析到一个存在的后继提案 ——

// ViolationSupersededChain 是提案链断裂：`status: superseded` 但 `decision.superseded_by`
// 为空、形态非法、指向自己，或无法解析到一个**存在的**提案 ID。
//
// 037 把它映射到 M3 新增 error 表的第一行（编号只在 internal/plan 发）；
// CLI 行为是 ExitCodeValidation + **零写入**。
const ViolationSupersededChain ViolationKind = "superseded_chain_broken"

// DiagSupersededChain 是提案链断裂对应的诊断族（DiagClass 的第三个取值，M3 · T-…-035 新增）。
//
// 与既有两族的分工：枚举 / 一致性 → DiagEnumOrConsistency；非法迁移 → DiagIllegalTransition；
// **提案链断裂 → 本族**。三族各自映射到一个既有编号，不新增第四个 error 编号。
const DiagSupersededChain DiagClass = "superseded_chain"

// DiagClassOfSupersededChain 报告某个 kind 是否属提案链断裂族。
//
// 之所以不在 state.go 的 DiagClassOf 里加分支：那张表是 T-…-034 的交付物，本 task 只**追加**
// 一族而不改写既有映射（既有 kind → 族的对应关系逐字不变）。plan 侧的映射表同时消费两者。
func DiagClassOfSupersededChain(kind ViolationKind) DiagClass {
	if kind == ViolationSupersededChain {
		return DiagSupersededChain
	}
	return DiagNone
}

// CheckSupersededChain 判定提案链是否成立（**两触发都必写 `superseded_by`** 的唯一判据）。
//
// exists 是「该提案 ID 在库存在」的只读探针（由调用方按扫描事实提供；nil 表示无法探测，
// 此时只判形态不判存在性）。判定顺序：
//
//	status 必须先在四态内 → 非 superseded 直接通过（本判据只管 superseded 一行）
//	→ superseded_by 非空 → 形态是合法提案 ID → 不得指向自己 → 必须解析到一个**存在的**提案。
func CheckSupersededChain(p Proposal, exists func(ID) bool) error {
	if err := CheckStatus(p.Status); err != nil {
		return err
	}
	if p.Status != StatusSuperseded {
		return nil
	}
	field := KeyDecision + "." + KeySupersededBy
	sb := p.Decision.SupersededBy
	if sb == "" {
		return violate(ViolationSupersededChain, field,
			"status = %s 但 %s 为空：两个触发都必须写 %s，否则提案链断裂、后继提案无法定位",
			StatusSuperseded, field, field)
	}
	next := ID(sb)
	if !next.Valid() {
		return violate(ViolationSupersededChain, field,
			"%s = %q 不是合法提案 ID（形如 p-YYYYMMDD-NNN）：提案链断裂", field, sb)
	}
	if next == p.ID {
		return violate(ViolationSupersededChain, field,
			"%s = %q 指向自己：后继提案必须是另一份提案", field, sb)
	}
	if exists != nil && !exists(next) {
		return violate(ViolationSupersededChain, field,
			"%s = %q 无法解析到一个存在的提案：提案链断裂", field, sb)
	}
	return nil
}

// —— ③ approve 执行前的重算比对与改判（触发②）——

// ApproveDecision 是 `eg proposal approve` **执行前重算比对**的裁决结果。
//
// Proceed 为真 → 前提成立，走 T1（pending → approved）继续执行（§5.3 第 9b 步）；
// Proceed 为假 → 前提已变化，**不强行执行**，改走触发② 的 superseded 分支（第 9a 步）。
type ApproveDecision struct {
	// Recomputed 是执行前重算出的影响面（归一形态）。
	Recomputed Impact
	// Diff 是与提案里 impact 的逐项差异（空 = 一致）。
	Diff []string
	// Changed 报告影响面是否已变化（= len(Diff) > 0）。
	Changed bool
	// Proceed 报告是否继续执行删除。
	Proceed bool
	// Edge 是本次应当走的合法边 ID（一致 → T1；不一致 → T3 / T4，取决于起态）。
	Edge string
	// Trigger 在不一致时为 TriggerImpactChanged，一致时为空。
	Trigger Trigger
	// Message 是进报告的结论；不执行时**逐字**含 NotExecutedNotice。
	Message string
}

// DecideApprove 是「批准能否继续执行」的**唯一**裁决口（触发② 的判定半边）。
//
// 入参 recomputed **就是**执行前重算的结果：绕过重算就得不到裁决。
// 一致 → 走 T1；不一致 → 不执行删除，报告逐字写明，并按起态给出 T3 / T4。
// 起态非法（如提案已是终态 rejected / superseded）→ 返回 CheckTransition 的结构化违规。
func DecideApprove(p Proposal, recomputed Impact) (ApproveDecision, error) {
	d := ApproveDecision{Recomputed: NormalizeImpact(recomputed)}
	if err := CheckStatus(p.Status); err != nil {
		return d, err
	}
	d.Diff = ImpactDiff(p.Impact, recomputed)
	d.Changed = len(d.Diff) > 0
	if !d.Changed {
		if err := CheckTransition(p.Status, StatusApproved); err != nil {
			return d, err
		}
		e, _ := LegalEdge(p.Status, StatusApproved)
		d.Proceed, d.Edge = true, e.ID
		d.Message = fmt.Sprintf("执行前重算影响面与提案 %s 的 impact 逐项一致：走 %s（%s → %s）继续执行",
			p.ID, e.ID, p.Status, StatusApproved)
		return d, nil
	}
	if err := CheckTransition(p.Status, StatusSuperseded); err != nil {
		return d, err
	}
	e, _ := LegalEdge(p.Status, StatusSuperseded)
	d.Edge, d.Trigger = e.ID, TriggerImpactChanged
	d.Message = fmt.Sprintf("%s：提案 %s 的执行前提已变化（%d 项不一致：%s），生成新提案重新描述当前影响面，原提案走 %s（%s → %s）",
		NotExecutedNotice, p.ID, len(d.Diff), joinDiff(d.Diff), e.ID, p.Status, StatusSuperseded)
	return d, nil
}

func joinDiff(diff []string) string {
	out := ""
	for i, d := range diff {
		if i > 0 {
			out += "；"
		}
		out += d
	}
	return out
}

// —— ④ 两个触发的**事务性**落地 ——

// SupersedeSpec 是一次 `superseded` 落地的输入（两个触发共用同一个事务性函数）。
type SupersedeSpec struct {
	// Store 是 guarded 写口（A-23：提案控制面走 CLI 直写例外，但**必须**复用 guarded store）。
	Store *store.Store
	// Original 是原提案 ID。
	Original ID
	// OriginalRel 是原提案在库的实际路径；留空按默认落位（文件允许被改名 / 移动，F2）。
	OriginalRel string
	// Trigger 是触发种类（恰两值）。
	Trigger Trigger
	// Reason 是 decision.reason（T3 / T4 的用户决定理由，进原提案）。
	Reason string
	// Today 是新提案 ID 与 created_at 的日期段：本包**不读系统时钟**（结果可复算）。
	Today model.Date
	// NewTitle 是新提案的标题（正文 H1）。
	NewTitle string
	// NewTargets 是新提案的 targets：触发① 填**实际接受**的那部分，触发② 填当前仍成立的对象。
	NewTargets []string
	// NewImpact 是新提案承载的影响面：触发② 必须是**重算结果**。
	NewImpact Impact
}

// SupersedeResult 是一次 `superseded` 落地的回执（供 CLI 组报告与发 commit）。
type SupersedeResult struct {
	Trigger Trigger
	// NewID / NewRel / NewHash 是新提案（第 ① 步）。
	NewID   ID
	NewRel  string
	NewHash string
	// OriginalRel / OriginalHash 是原提案（第 ② / ③ 步落在同一次 guarded 写）。
	OriginalRel  string
	OriginalHash string
	// From 是原提案迁移前的状态，Edge 是本次走的合法边（T3 / T4）。
	From Status
	Edge string
	// Steps 是三步动作的实际执行轨迹（**已完成**的步骤逐条在册；失败时据此看出停在哪一步）。
	Steps []string
	// Message 是结论文本；触发② 逐字含 NotExecutedNotice。
	Message string
}

// Supersede 落地 `superseded` 的**三步动作**（§3 表「系统动作（三步，缺一不可）」）。
//
//	① 生成新提案（status: pending / execution: not_started，当日三位序号）
//	② 原提案 status = superseded
//	③ 原提案写 decision.superseded_by = <新提案 ID>
//
// ② 与 ③ 落在**同一次** guarded 写：同一份文件的两处 frontmatter 改动一次原子替换，
// 因此不存在「标了 superseded 却没写 superseded_by」的中间态（E7 在结构上不可能由本函数产生）。
//
// 失败语义（B4）：任一步失败即返回错误，**保留现状、不做破坏性回滚**——
// 已完成的第 ① 步不会被删除（Git 历史与报告承载事实），Steps 逐条记下已完成的动作。
func Supersede(spec SupersedeSpec) (SupersedeResult, error) {
	res := SupersedeResult{Trigger: spec.Trigger}
	if spec.Store == nil {
		return res, ErrNoStore
	}
	if !spec.Trigger.Valid() {
		return res, fmt.Errorf("未知的 superseded 触发 %q：封闭集合恰 %d 个（%s / %s）",
			spec.Trigger, len(Triggers()), TriggerPartialAccept, TriggerImpactChanged)
	}
	rel := spec.OriginalRel
	if rel == "" {
		rel = Rel(spec.Original)
	}
	res.OriginalRel = rel

	f, err := spec.Store.Read(rel)
	if err != nil {
		return res, err
	}
	orig, err := Parse(f.Bytes)
	if err != nil {
		return res, err
	}
	if err := ValidateLayout(orig); err != nil {
		return res, err
	}
	res.From = orig.P.Status
	// 迁移合法性一律走 T-…-034 的状态机：非法边（含终态出边）在这里被拒，写口不被调用。
	if err := CheckTransition(orig.P.Status, StatusSuperseded); err != nil {
		return res, err
	}
	edge, _ := LegalEdge(orig.P.Status, StatusSuperseded)
	res.Edge = edge.ID

	// —— 第 ① 步：生成新提案 ——
	newID, err := nextProposalID(spec.Store, spec.Today)
	if err != nil {
		return res, err
	}
	title := spec.NewTitle
	if title == "" {
		title = fmt.Sprintf("%s：取代提案 %s", spec.Trigger.Label(), orig.P.ID)
	}
	content, err := RenderTemplate(Template{
		ID:        newID,
		Title:     title,
		CreatedAt: spec.Today.String(),
		Targets:   spec.NewTargets,
		Impact:    NormalizeImpact(spec.NewImpact),
	})
	if err != nil {
		return res, err
	}
	newRel := Rel(newID)
	created, err := spec.Store.ApplyProposalCreate(newRel, content)
	if err != nil {
		return res, fmt.Errorf("第 ① 步失败（生成新提案 %s）：%w", newID, err)
	}
	res.NewID, res.NewRel, res.NewHash = newID, newRel, created.Hash
	res.Steps = append(res.Steps,
		fmt.Sprintf("① 已生成新提案 %s（%s，status: %s / %s: %s）",
			newID, newRel, StatusPending, KeyExecBlock, ExecNotStarted))

	// —— 第 ② + ③ 步：原提案标 superseded 并写 superseded_by（同一次 guarded 写）——
	out, err := markSuperseded(f.Bytes, newID, spec.Reason)
	if err != nil {
		return res, fmt.Errorf("第 ② / ③ 步失败（改写原提案 %s）：%w", orig.P.ID, err)
	}
	upd, err := spec.Store.ApplyProposalUpdate(store.ProposalUpdateSpec{
		Rel: rel, ExpectedHash: f.Hash, Content: out,
	})
	if err != nil {
		return res, fmt.Errorf("第 ② / ③ 步失败（改写原提案 %s）：%w", orig.P.ID, err)
	}
	res.OriginalHash = upd.Hash
	res.Steps = append(res.Steps,
		fmt.Sprintf("② 原提案 %s 的 %s 已改为 %s（%s：%s → %s）",
			orig.P.ID, KeyStatus, StatusSuperseded, edge.ID, res.From, StatusSuperseded),
		fmt.Sprintf("③ 原提案 %s 已写 %s.%s = %s",
			orig.P.ID, KeyDecision, KeySupersededBy, newID))
	res.Message = supersedeMessage(spec.Trigger, orig.P.ID, newID)
	return res, nil
}

// supersedeMessage 拼结论文本：触发② 逐字含「不执行删除」。
func supersedeMessage(trigger Trigger, original, next ID) string {
	head := fmt.Sprintf("%s：提案 %s 已标记 %s，后继提案 %s",
		trigger.Label(), original, StatusSuperseded, next)
	if trigger == TriggerImpactChanged {
		return head + "；" + NotExecutedNotice + "，本次不改任何知识数据，详情进报告"
	}
	return head + "；提案**不支持部分应用**，被接受的那部分由新提案整体承载"
}

// nextProposalID 分配「同日下一个可用序号」的新提案 ID。
//
// 序号来源是**扫描到的全部在库提案 ID**（NextSeq 纯函数，不读时钟、不猜文件名）；
// 日期段由调用方给（Today），因此同一语料 + 同一日期恒得同一 ID（可复算）。
func nextProposalID(s *store.Store, today model.Date) (ID, error) {
	if today.IsZero() {
		return "", fmt.Errorf("生成新提案需要日期（本包不读系统时钟）")
	}
	existing, err := ListIDs(s)
	if err != nil {
		return "", err
	}
	seq, err := NextSeq(today.Compact(), existing)
	if err != nil {
		return "", err
	}
	return NewID(today, seq)
}

// ListIDs 扫描出全部在库提案 ID（升序，只读）。
//
// 定位一律靠扫描 frontmatter 的 id（F2：文件允许被改名 / 移动），不依赖文件名。
func ListIDs(s *store.Store) ([]ID, error) {
	if s == nil {
		return nil, ErrNoStore
	}
	idx, err := s.ScanIDs()
	if err != nil {
		return nil, err
	}
	var out []ID
	for id, rel := range idx.ByID {
		if !store.IsProposalRel(rel) {
			continue
		}
		if pid := ID(id); pid.Valid() {
			out = append(out, pid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// ExistsProbe 返回一个「提案 ID 是否在库」的只读探针（供 CheckSupersededChain 使用）。
func ExistsProbe(s *store.Store) (func(ID) bool, error) {
	ids, err := ListIDs(s)
	if err != nil {
		return nil, err
	}
	set := make(map[ID]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return func(id ID) bool { return set[id] }, nil
}

// —— ⑤ T1：前提成立时的批准落盘（status → approved，execution 一字不动）——

// ApproveSpec 是一次 T1 落盘的输入（前提成立分支；`execution` 三态回写属 T-…-036）。
type ApproveSpec struct {
	Store       *store.Store
	Original    ID
	OriginalRel string
	Reason      string
}

// MarkApproved 落地 T1（pending → approved）：只改 status 与 decision 两处。
//
// **`approved` 不等于已执行**：本函数一个字节都不碰 `execution` 块（矩阵 #39：
// execution 是 CLI 执行器独占，回写属 T-…-036），因此批准后立即读盘，
// `execution.status` 仍是 not_started。知识数据的真实改动仍走 ChangePlan（A-23 第 4 条）。
func MarkApproved(spec ApproveSpec) (store.Result, error) {
	if spec.Store == nil {
		return store.Result{}, ErrNoStore
	}
	rel := spec.OriginalRel
	if rel == "" {
		rel = Rel(spec.Original)
	}
	f, err := spec.Store.Read(rel)
	if err != nil {
		return store.Result{Path: rel}, err
	}
	orig, err := Parse(f.Bytes)
	if err != nil {
		return store.Result{Path: rel}, err
	}
	if err := ValidateLayout(orig); err != nil {
		return store.Result{Path: rel}, err
	}
	if err := CheckTransition(orig.P.Status, StatusApproved); err != nil {
		return store.Result{Path: rel}, err
	}
	sets := []fmScalar{
		{Key: KeyStatus, Value: string(StatusApproved)},
		{Block: KeyDecision, Key: KeyResult, Value: string(StatusApproved)},
	}
	if spec.Reason != "" {
		sets = append(sets, fmScalar{Block: KeyDecision, Key: KeyReason, Value: spec.Reason})
	}
	out, err := applyFMScalars(f.Bytes, sets)
	if err != nil {
		return store.Result{Path: rel}, err
	}
	if err := verifyControlPlane(out, nil); err != nil {
		return store.Result{Path: rel}, err
	}
	return spec.Store.ApplyProposalUpdate(store.ProposalUpdateSpec{
		Rel: rel, ExpectedHash: f.Hash, Content: out,
	})
}

// —— ⑥ 字节级 frontmatter 标量替换（写路径的唯一形态）——

// fmScalar 是一处待替换的 frontmatter 标量：Block 为空表示顶层键，否则是块内子键。
type fmScalar struct {
	Block string
	Key   string
	Value string
}

// markSuperseded 产出「原提案标 superseded + 写 superseded_by」的候选字节，并做写前复核。
//
// 第 ② / ③ 步在**同一份候选字节**里完成：status / decision.result / decision.reason /
// decision.superseded_by 四处标量按字节替换，其余字节（未知键、注释、空行、正文七分区）
// 逐字不动。复核不过一律返回错误 → 写口不被调用（零写入）。
func markSuperseded(raw []byte, next ID, reason string) ([]byte, error) {
	sets := []fmScalar{
		{Key: KeyStatus, Value: string(StatusSuperseded)},
		{Block: KeyDecision, Key: KeyResult, Value: string(StatusSuperseded)},
		{Block: KeyDecision, Key: KeySupersededBy, Value: string(next)},
	}
	if reason != "" {
		sets = append(sets, fmScalar{Block: KeyDecision, Key: KeyReason, Value: reason})
	}
	out, err := applyFMScalars(raw, sets)
	if err != nil {
		return nil, err
	}
	if err := verifyControlPlane(out, func(id ID) bool { return id == next }); err != nil {
		return nil, err
	}
	return out, nil
}

// verifyControlPlane 是提案控制面候选字节的**写前复核**（不过即拒写）：
//
//	Parse→Render 字节自检 → 落盘形态恰合规（ValidateLayout）
//	→ 四态封闭 + status ⟷ decision.result 一致（CheckStatus / CheckDecisionConsistency）
//	→ 该条合法边的必写字段齐备（CheckRequiredFields）
//	→ superseded 时提案链成立（CheckSupersededChain，exists 探针由调用方给）
func verifyControlPlane(out []byte, exists func(ID) bool) error {
	if err := SelfCheck(out); err != nil {
		return err
	}
	f, err := Parse(out)
	if err != nil {
		return err
	}
	if err := ValidateLayout(f); err != nil {
		return err
	}
	if err := CheckDecisionConsistency(f.P); err != nil {
		return err
	}
	// 提案链**先判**：`superseded` 少写 superseded_by 有专属诊断族（DiagSupersededChain，
	// 编号由 internal/plan 统一发），必须由本判据说话，不能被「必写字段缺失」这条更泛的判定
	// 抢先吞掉 —— 否则同一事实会落到另一个编号上。
	if err := CheckSupersededChain(f.P, exists); err != nil {
		return err
	}
	// 必写字段按**目标态**查边：pending → approved 即 T1、pending → superseded 即 T3。
	// T4（approved → superseded）的必写字段列与 T3 逐字相同（§2.3 两行同列），
	// 因此这里用 pending 起点查到的那条边即可覆盖两者，不必分叉。
	if e, ok := LegalEdge(StatusPending, f.P.Status); ok {
		return CheckRequiredFields(e, presentFields(f))
	}
	return nil
}

// presentFields 收集**非空**的必写字段键路径（点分），供 CheckRequiredFields 判「在不在」。
func presentFields(f *File) map[string]bool {
	dot := func(block, sub string) string { return block + "." + sub }
	present := map[string]bool{}
	for key, val := range map[string]string{
		KeyID:                             string(f.P.ID),
		KeyType:                           string(f.P.Type),
		KeyStatus:                         string(f.P.Status),
		KeyCreatedAt:                      f.P.CreatedAt,
		dot(KeyDecision, KeyResult):       string(f.P.Decision.Result),
		dot(KeyDecision, KeyReason):       f.P.Decision.Reason,
		dot(KeyDecision, KeySupersededBy): f.P.Decision.SupersededBy,
	} {
		if val != "" {
			present[key] = true
		}
	}
	if len(f.P.Targets) > 0 {
		present[KeyTargets] = true
	}
	present[KeyImpact] = true
	return present
}

// applyFMScalars 逐处替换 frontmatter 标量：每处替换后**重新索引**（偏移会随长度变化移动），
// 只做 []byte 区间拼接，全程不经任何 YAML 序列化器。
func applyFMScalars(raw []byte, sets []fmScalar) ([]byte, error) {
	cur := raw
	for _, s := range sets {
		out, err := setFMScalar(cur, s)
		if err != nil {
			return nil, err
		}
		cur = out
	}
	return cur, nil
}

// ErrFMKeyNotFound 表示 frontmatter 里找不到待替换的键（键集合不齐时不猜、不新增键）。
var ErrFMKeyNotFound = fmt.Errorf("frontmatter 缺少待改写的键")

// setFMScalar 把一个 frontmatter 标量替换成新值（字节级整行替换）。
//
// 定位口径：顶层键 = frontmatter 区间内**列 0** 的 `key:` 行；块内子键 = 先命中列 0 的
// `block:` 行，再在其**缩进子行**里命中 `key:`（遇到下一个列 0 键即离开该块）。
// 原行的缩进逐字沿用；值一律单引号包裹（与 M1 的 store 拼装同一套引号风格），空值写成裸键。
func setFMScalar(raw []byte, s fmScalar) ([]byte, error) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !doc.HasFM {
		return nil, violate(ViolationNoFrontmatter, "", "提案必须有 frontmatter")
	}
	start, end, indent, ok := fmLineSpan(raw, doc.FMStart, doc.FMEnd, s.Block, s.Key)
	if !ok {
		field := s.Key
		if s.Block != "" {
			field = s.Block + "." + s.Key
		}
		return nil, fmt.Errorf("%w：%s", ErrFMKeyNotFound, field)
	}
	line, err := scalarLine(indent, s.Key, s.Value)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)-(end-start)+len(line))
	out = append(out, raw[:start]...)
	out = append(out, line...)
	return append(out, raw[end:]...), nil
}

// scalarLine 拼一行 `<缩进><key>: '<值>'`；空值写成 `<缩进><key>:`（与模板的空值骨架同形）。
func scalarLine(indent, key, value string) ([]byte, error) {
	if value == "" {
		return []byte(indent + key + ":\n"), nil
	}
	q, err := quoted(value)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", key, err)
	}
	line := make([]byte, 0, len(indent)+len(key)+len(q)+3)
	line = append(line, indent...)
	line = append(line, key...)
	line = append(line, ':', ' ')
	line = append(line, q...)
	return append(line, '\n'), nil
}

// fmLineSpan 在 frontmatter 区间里定位一个标量所在的**整行**（半开区间 [start, end)，含行尾换行）。
func fmLineSpan(raw []byte, fmStart, fmEnd int, block, key string) (int, int, string, bool) {
	inBlock := block == ""
	for at := fmStart; at < fmEnd; {
		stop := fmEnd
		if nl := bytes.IndexByte(raw[at:fmEnd], '\n'); nl >= 0 {
			stop = at + nl + 1
		}
		line := raw[at:stop]
		trimmed := bytes.TrimLeft(line, " \t")
		indent := string(line[:len(line)-len(trimmed)])
		name, ok := lineKey(trimmed)
		switch {
		case indent == "" && ok && block != "" && name == block:
			inBlock = true
		case indent == "" && ok && block != "" && name != block:
			inBlock = false
		case indent == "" && block == "" && ok && name == key:
			return at, stop, "", true
		case indent != "" && inBlock && block != "" && ok && name == key:
			return at, stop, indent, true
		}
		at = stop
	}
	return 0, 0, "", false
}

// lineKey 取一行的键名（`key: value` → `key`）；不是键行（序列项、空行、纯值）返回 false。
func lineKey(trimmed []byte) (string, bool) {
	if len(trimmed) == 0 || trimmed[0] == '#' || trimmed[0] == '-' {
		return "", false
	}
	i := bytes.IndexByte(trimmed, ':')
	if i <= 0 {
		return "", false
	}
	name := string(bytes.TrimRight(trimmed[:i], " \t"))
	if name == "" || bytes.ContainsAny([]byte(name), " \t") {
		return "", false
	}
	return name, true
}
