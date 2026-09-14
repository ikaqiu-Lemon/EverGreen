package reconcile

// R2 —— `reviewed_at` 补齐的**只读判定**（对账合同 §5；M4 · T-…-051）。
//
// 这是 M3 明确留给 S3 / M4 的**触发①**（M3 只做触发② `eg mark-reviewed`，见授权合同
// A-16 / N-7）：用户**直接**（不经 `eg`）改过一张知识卡或一篇材料笔记之后，过目信号
// 会滞后于内容——本文件把这件事检出为一条 `reviewed_at_missing` finding（W14 / warning，
// 分级与诊断码一律取自 check.go 的唯一真源表），并给出**逐键封闭**的修复意向。
//
// # 判定：三条同真才命中（合同 §5 逐字）
//
//	① 对象是**知识卡或材料笔记**（`proposals/**` 与 `unprocessed.md` 不在范围内）；
//	② 该文件在 Git 上有**用户直接编辑**的证据 —— `git_uncommitted` 命中该路径，
//	   **或** 最近一次改动该文件的 commit 的 verb **不属** `eg` 已知 verb 集合；
//	③ `reviewed_at` 缺失，**或** `reviewed_at` 早于 `updated_at`。
//
// 三条是**合取**：只满足两条一律不命中（反证见 r2_reviewed_test.go 的真值表）。
//
// # 本文件**不做**的事（结构上做不到，不靠自律）
//
//   - 不写盘、不发提交、不起子进程：本文件不引 os 的任何写 API、不引子进程包，
//     也**不持有** *git.Repo。R2 的动作是「改知识对象的 frontmatter」，因此它**只**产出
//     RepairSpec（修复意向的**描述**），真正的落盘由命令层编排成**内存 ChangePlan**、
//     经计划层的完整校验链交落盘层写入（修复桥文件 reconcile_repair_reviewed.go 在
//     命令层，合同 §1.3 写口归属表第 2 行）。
//   - **不自己决定补写的时刻**：值由写入侧按「本次对账时刻」给出（与 `eg mark-reviewed`
//     同一 RFC3339 格式化口径），本包只说「这个键要补」，不生产时间戳。
//   - **不碰其余任何 frontmatter 键**：待写键集合恒 `{reviewed_at}` 一个值
//     （ReviewedKeys 是长度固定数组，第二个键在编译期就加不进来），状态维度 / 删除维度 /
//     替代指针维度**一格不写**（合同 §5「三维不牵连」）。
//   - **不豁免 B3**：hash 过期属写入侧的 `skipped[kind=file_changed]`；本包提供
//     WithSkipNotice 把「已跳过」如实追加进同一条 finding 的 detail（finding 仍产出）。
//   - 不判定 R1 / R3 ~ R7 任何一项，不注册任何命令，不动报告体。
//
// # ADR-20 文件级隔离不放宽
//
// 本文件只把 `reviewed_at` 当**过目信号的落盘原值**读进来做「缺失 / 滞后」比对，
// 既不排序、不收敛，也不参与关系计算：四个被 ADR-20 隔离的落点（排序 / 关系 /
// 收敛规则 / 复习面）在本包一律零命中（反证：包边界用例 + task Acceptance 的 grep 恒 0）。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// R2 是本检查项的 R 编号（与 checkTable 内 CheckReviewedAtMissing 行的 R 列同值）。
const R2 = "R2"

// ReviewedKeyCount 是 R2 允许写的 frontmatter 键数：**恰 1**（逐键封闭，合同 §5）。
const ReviewedKeyCount = 1

// reviewedKeyTable 是待写键的封闭全集。类型是**长度固定的数组**而不是 slice：
// 想在 R2 的修复面里顺手加第二个键（例如状态维度那格），编译期就过不去。
var reviewedKeyTable = [ReviewedKeyCount]string{"reviewed_at"}

// ReviewedKeys 返回封闭待写键集合的副本（恒恰一个元素）。
func ReviewedKeys() []string {
	out := make([]string, 0, ReviewedKeyCount)
	out = append(out, reviewedKeyTable[:]...)
	return out
}

// ReviewedKey 是那个唯一的待写键名（供写入侧做键集合封闭比对）。
func ReviewedKey() string { return reviewedKeyTable[0] }

// 出范围面（判定第 1 条的反面，与 R4 的对账域口径同源）：
// `proposals/**` 是控制面（提案不是知识对象），`unprocessed.md` 是收件区（条目不是对象）。
const (
	proposalsPrefix = "proposals/"
	unprocessedName = "unprocessed.md"
)

// 用户直接编辑的**封闭两值**证据串（进 detail，供逐条复算；不是新 check、不是新诊断码）。
const (
	// EvidenceUncommitted 证据①：`git status --porcelain` 命中该路径（R1 的同一份事实）。
	EvidenceUncommitted = "git_uncommitted"
	// EvidenceForeignVerb 证据②：最近一次改动该文件的 commit 的 verb 不属 `eg` 已知 verb。
	EvidenceForeignVerb = "foreign_commit_verb"
)

// EvidenceCount 是证据的封闭基数：恰 2（合同 §5 判定第 2 条的两个析取项）。
const EvidenceCount = 2

// evidenceLabel 是证据的中文标签（只用于 detail 渲染，不参与任何判定）。
var evidenceLabel = map[string]string{
	EvidenceUncommitted: "工作区有未提交的外部编辑",
	EvidenceForeignVerb: "最近一次改动该文件的提交 verb 不属 eg 已知 verb（外部编辑）",
}

// 判定第 3 条的**封闭两形态**（进 detail；`missing` 与 `stale` 都由同一个 check 承载）。
const (
	// StaleReasonMissing 过目信号缺失（键不在 frontmatter 里，或落盘原值读不成时刻）。
	StaleReasonMissing = "missing"
	// StaleReasonBehind 过目信号早于 `updated_at`（内容比过目新）。
	StaleReasonBehind = "behind_updated_at"
)

// EditFact 是「最近一次改动某文件的 commit 的 verb」这一条只读 Git 历史事实。
//
// 只有两个字段：本包不需要 SHA、作者、时间，也不该持有它们（对账只判 verb 归属）。
// Verb 逐字取自提交主题的动词位（`<verb>(<domain>): <subject>` 的第一段），由调用方
// 用 `git log` 的只读用法采样；**空 Verb = 该路径未采到事实**，证据②对它不成立。
type EditFact struct {
	// Path 是 vault 相对路径（/ 分隔），与 query 扫描面的 Path 口径一致。
	Path string
	// Verb 是最近一次改动该文件的 commit 的 verb 字面量（不做任何归一化）。
	Verb string
}

// ReviewedTarget 是一条命中 R2 的对象事实（**纯数据**：无函数字段、无句柄、无写方法）。
//
// 为什么导出：写入侧（命令层）需要「对象 ID + 落盘路径」才能编排 ChangePlan，
// 而 RepairSpec 按合同只承载 `check / path / keys / reason` 四键、不带 ID。
// 两者同源同事实：ReviewedTargets 与 checkR2ReviewedAt 走的是同一个判定函数。
type ReviewedTarget struct {
	// ID 是对象 ID（知识卡 `k-` / 材料笔记 `n-`），逐字取自扫描面。
	ID string
	// Path 是 vault 相对路径（/ 分隔）。
	Path string
	// Kind 是对象类别：KindCard 或 KindNote（与 R4 的类别串同源，不另定义一套）。
	Kind string
	// ReviewedAt / UpdatedAt 是两个时刻的**逐字原值**（缺省即空串，不回填默认值）。
	ReviewedAt string
	UpdatedAt  string
	// Evidence 是命中的证据串（封闭两值的子集，去重 + 升序，恒非空）。
	Evidence []string
	// StaleReason 是判定第 3 条的形态（封闭两值之一）。
	StaleReason string
}

// ReviewedTargets 返回全部命中 R2 三条判定的对象（按 (path, id) 升序，可逐字复算）。
//
// 纯函数：同一 Input 恒得同一输出；不改入参、不做任何 IO。
// Scan 为 nil（未扫描）→ 空集合：绝不把「没扫描」当成「没有对象」。
func ReviewedTargets(in Input) []ReviewedTarget {
	if in.Scan == nil {
		return nil
	}
	dirty := make(map[string]bool, len(in.Status.Changes))
	for _, p := range UncommittedTargets(in) {
		dirty[p] = true
	}
	verbs, sampled := editIndex(in.Edits)

	out := make([]ReviewedTarget, 0, len(in.Scan.Cards)+len(in.Scan.Notes))
	add := func(kind, id, path, updatedAt, reviewedAt string) {
		t, ok := reviewedHit(kind, id, path, updatedAt, reviewedAt, dirty, verbs, sampled)
		if ok {
			out = append(out, t)
		}
	}
	for _, c := range in.Scan.Cards {
		add(KindCard, c.ID, c.Path, c.UpdatedAt, c.ReviewedAt)
	}
	for _, n := range in.Scan.Notes {
		add(KindNote, n.ID, n.Path, n.UpdatedAt, n.ReviewedAt)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// reviewedHit 是三条判定的**唯一**实现（合取；任一条不成立即 false）。
func reviewedHit(kind, id, path, updatedAt, reviewedAt string,
	dirty map[string]bool, verbs map[string]string, sampled bool) (ReviewedTarget, bool) {
	id, path = strings.TrimSpace(id), strings.TrimSpace(path)
	// ① 对象是知识卡或材料笔记（调用方只喂这两类），且落盘路径在对账域内。
	if id == "" || !InReviewedScope(path) {
		return ReviewedTarget{}, false
	}
	// ② 用户直接编辑的证据（两个析取项，至少一条成立）。
	ev := EditEvidence(path, dirty, verbs, sampled)
	if len(ev) == 0 {
		return ReviewedTarget{}, false
	}
	// ③ 过目信号缺失或滞后。
	reason, ok := ReviewedStale(reviewedAt, updatedAt)
	if !ok {
		return ReviewedTarget{}, false
	}
	return ReviewedTarget{
		ID: id, Path: path, Kind: kind,
		ReviewedAt: strings.TrimSpace(reviewedAt), UpdatedAt: strings.TrimSpace(updatedAt),
		Evidence: ev, StaleReason: reason,
	}, true
}

// InReviewedScope 判定第 1 条的路径面：`proposals/**` 与 `unprocessed.md` 恒 false。
//
// 与 R4 的对账域口径同源（提案是控制面、收件区是条目而不是对象）。这条是**双保险**：
// 调用方喂进来的本就只有知识卡与材料笔记两类，路径面再挡一次，
// 使「有人把提案面的文件塞进扫描结果」也不会被 R2 补写任何键。
func InReviewedScope(rel string) bool {
	p := strings.TrimSpace(rel)
	p = strings.TrimPrefix(p, "./")
	switch {
	case p == "":
		return false
	case p == unprocessedName || strings.HasSuffix(p, "/"+unprocessedName):
		return false
	case p == strings.TrimSuffix(proposalsPrefix, "/") ||
		strings.HasPrefix(p, proposalsPrefix) ||
		strings.Contains(p, "/"+proposalsPrefix):
		return false
	}
	return true
}

// EditEvidence 返回该路径上成立的**用户直接编辑**证据（封闭两值的子集，去重 + 升序）。
//
// 空集合 = 判定第 2 条不成立。sampled 为 false（`Input.Edits` 未采样）时证据②整体不判：
// 「没采到最近一次提交的 verb」不等于「那次提交是外部编辑」。
func EditEvidence(path string, dirty map[string]bool, verbs map[string]string, sampled bool) []string {
	var ev []string
	if dirty[path] {
		ev = append(ev, EvidenceUncommitted)
	}
	if sampled {
		if verb, ok := verbs[path]; ok && IsForeignVerb(verb) {
			ev = append(ev, EvidenceForeignVerb)
		}
	}
	return NormalizeTargets(ev)
}

// IsForeignVerb 报告该 verb 是否**不属** `eg` 已知 verb 集合（= 外部编辑的证据）。
//
// 已知 verb 的真源恒是 `internal/model`（`KnownVerbs` 恒 8，M4 不增减），本包不另抄一份。
// 空串 = 未采到事实 → **不**算外部编辑（诚实性：不把缺事实当证据）。
func IsForeignVerb(verb string) bool {
	v := strings.TrimSpace(verb)
	if v == "" {
		return false
	}
	_, known := model.NormalizeVerb(v)
	return !known
}

// ReviewedStale 判定第 3 条：过目信号是否缺失或早于 `updated_at`。
//
// 返回 (形态, 是否命中)。三条诚实性口径：
//   - 过目原值为空 → 命中 StaleReasonMissing（键缺失）；
//   - 过目原值**读不成时刻**（落盘值形态非法）→ 同样命中 StaleReasonMissing：
//     一个读不出来的过目信号不能当「已过目」用；
//   - `updated_at` 缺失或读不成时刻 → **不命中**：两个时刻缺一个就无法比对，
//     宁可不报也不误报（合同 §5 的判定是「早于」，不是「不等于」）。
func ReviewedStale(reviewedAt, updatedAt string) (string, bool) {
	rv := strings.TrimSpace(reviewedAt)
	if rv == "" {
		return StaleReasonMissing, true
	}
	rs, err := model.ParseStamp(rv)
	if err != nil {
		return StaleReasonMissing, true
	}
	us, err := model.ParseStamp(strings.TrimSpace(updatedAt))
	if err != nil {
		return "", false
	}
	if rs.Time().Before(us.Time()) {
		return StaleReasonBehind, true
	}
	return "", false
}

// editIndex 把 EditFact 快照折成 path → verb 的索引（第二个返回值 = 是否采样过）。
func editIndex(in []EditFact) (map[string]string, bool) {
	if in == nil {
		return nil, false
	}
	out := make(map[string]string, len(in))
	for _, e := range in {
		p := strings.TrimSpace(e.Path)
		if p == "" {
			continue
		}
		out[p] = strings.TrimSpace(e.Verb)
	}
	return out, true
}

// —— finding / RepairSpec 的渲染 ——

// r2Detail 渲染 W14 的 detail：对象类别 + ID + 路径 + 两个时刻 + 证据 + 待写键（足以复算）。
func r2Detail(t ReviewedTarget) string {
	return fmt.Sprintf(
		"%s %s（%s）有用户直接编辑的证据（%s），且过目信号%s（reviewed_at=%s / updated_at=%s）："+
			"本次对账把 %s 补写为对账时刻，**只**写这一个键——状态维度、删除维度与替代指针维度一格不写（合同 §5）",
		labelOf(t.Kind), t.ID, t.Path, strings.Join(evidenceLabels(t.Evidence), "、"),
		staleLabel(t.StaleReason), stampOrNone(t.ReviewedAt), stampOrNone(t.UpdatedAt),
		ReviewedKey())
}

// r2Reason 渲染 RepairSpec 的 reason（与同一条 finding 的 detail 同源同事实）。
func r2Reason(t ReviewedTarget) string {
	return fmt.Sprintf("%s %s 经用户直接编辑（%s）后过目信号%s：补齐 %s 单键",
		labelOf(t.Kind), t.ID, strings.Join(evidenceLabels(t.Evidence), "、"),
		staleLabel(t.StaleReason), ReviewedKey())
}

// evidenceLabels 把证据串翻成中文标签（未知取值原样返回，保证 detail 恒非空）。
func evidenceLabels(ev []string) []string {
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		if s, ok := evidenceLabel[e]; ok {
			out = append(out, s)
			continue
		}
		out = append(out, e)
	}
	return out
}

// staleLabel 把第 3 条的形态翻成中文标签。
func staleLabel(reason string) string {
	if reason == StaleReasonBehind {
		return "早于内容更新时刻"
	}
	return "缺失"
}

// stampOrNone 把空时刻渲染成「（缺该键）」，避免 detail 里出现空洞。
func stampOrNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（缺该键）"
	}
	return s
}

// ReviewedSkipNotice 是「B3 前置比对未通过、本文件本次未写入」的**固定文案**。
//
// 措辞固定便于用例与 e2e 逐字断言：finding **仍产出**（合同 §5「B3 不豁免」末句），
// 只是在同一条 detail 里如实注明已跳过 —— 不是删掉这条 finding、也不是降级。
const ReviewedSkipNotice = "；本文件本次**已跳过**未写入（写前内容比对未通过，kind=file_changed）：" +
	"补齐仍待下次对账，本条 finding 如实保留"

// WithSkipNotice 把「已跳过」如实追加进 finding 的 detail，返回**新值**（不改入参）。
//
// cause 是写入侧给出的跳过原因（非空时一并写进 detail）。check / severity / targets
// 一律不动：分级恒由 checkTable 决定，跳过与否**不改变**这条 finding 的分级与目标。
func WithSkipNotice(f Finding, cause string) (Finding, error) {
	detail := f.Detail + ReviewedSkipNotice
	if c := strings.TrimSpace(cause); c != "" {
		detail += "（" + c + "）"
	}
	return NewFinding(f.Check, f.Targets, detail)
}

// —— 检查项本体与注册 ——

// checkR2ReviewedAt 是 R2 检查项本体：每个命中对象产**恰一条** finding + **恰一条**
// RepairSpec（`targets` = [路径, 对象 ID] 去重升序；`keys` = 恰 {reviewed_at}）。
//
// 纯函数：同一 Input 恒得同一输出，不改入参、不产生任何副作用。
// 零命中 → 空集合：于是报告侧 `reviewed_at_missing` 条数为 0、写入侧零写入零 commit
// （幂等：干净库上重跑既不写盘也不产生空 commit）。
func checkR2ReviewedAt(in Input) ([]Finding, []RepairSpec) {
	targets := ReviewedTargets(in)
	if len(targets) == 0 {
		return nil, nil
	}
	fs := make([]Finding, 0, len(targets))
	rs := make([]RepairSpec, 0, len(targets))
	for _, t := range targets {
		f, err := NewFinding(CheckReviewedAtMissing, []string{t.Path, t.ID}, r2Detail(t))
		if err != nil {
			// 只有「未知 check / 空 detail」两种构造错误，两者在本文件都不可能发生
			// （check 取自封闭表常量、r2Detail 恒产非空串）。防御性丢弃而不 panic：
			// 对账是只读检查，任何情况下都不该让进程死在检查器里。
			continue
		}
		r, err := NewRepairSpec(CheckReviewedAtMissing, t.Path, ReviewedKeys(), r2Reason(t))
		if err != nil {
			continue
		}
		fs = append(fs, f)
		rs = append(rs, r)
	}
	return fs, rs
}

// 注册：R2 是本包**第一个产 RepairSpec** 的检查项（R1 / R4 都是只报告项）。
//
// 注册表 checkers 在 reconcile.go 内声明，一个 task 只在自己的文件里追加自己那一项 ——
// R3 / R5 / R6 / R7 分属 T-…-053 ~ T-…-056，本文件因此**恰追加一项**。
func init() { checkers = append(checkers, checkR2ReviewedAt) }

// R2ScanOf 是给命令层与用例的便利函数：把扫描快照 + Git 只读事实折成 Input。
//
// 存在的理由同 R1StatusOf：R2 只需要「扫描结果 + 未提交状态 + 最近提交 verb」三样事实，
// 不需要 `sources/` 分区快照（Sources 恒可为 nil）——但扫描底座仍复用 internal/query，
// 本包不另写扫描器（合同 §0.1 第 1 条）。
func R2ScanOf(vaultRoot string, scan *query.ScanResult, status git.Status, edits []EditFact) Input {
	return Input{VaultRoot: vaultRoot, Scan: scan, Status: status, Edits: edits}
}
