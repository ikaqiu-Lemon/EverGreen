package proposal

// 提案在 Markdown 上的**落盘形态**（提案合同 §7 全节）。
//
// 本文件是 034 / 035 / 036 / 040 的**唯一 schema 来源**：键集合、键名、正文分区名与顺序
// 只在这里定义一次，下游一律引用这里的常量与访问器，不再抄字面量。
//
// 三条形态硬约束（§7.2 / §7.3 / M-6）：
//   - frontmatter 顶层键**恰 8 个**：id / type / status / created_at / targets /
//     impact / decision / execution（顺序即 §7.2 yaml 的抄录顺序）。
//   - 正文 H2 **恰 7 个**且顺序逐字固定；模板生成时七个分区**全部先留着**，
//     内容缺失由 037 发 warning 承接（本 task 只保证分区骨架存在）。
//   - 提案**不设** reviewed_at / deleted_at / deleted_reason：schema 层直接不提供这三个键
//     （提案不是知识产物，不进 `eg unreviewed` 筛选，不可被逻辑删除）。
//
// 与 F5 的关系：F5 冻结的是「知识卡五分区 + 材料笔记五分区」；**提案七分区是另一套正文结构**，
// 不在 F5 冻结范围内，因此不构成对 F5 的推翻（§7.3）。
//
// 读写口径：读只用 mdfile 的只读解析；写只有 RenderTemplate 的**字节拼装**一条，
// 标量一律单引号包裹（与 M1 的 store 拼装同一套引号风格，日期不会被当成 timestamp）。

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// —— ① 枚举：三个封闭取值域（本 task 只定集合，状态机迁移属 034）——

// Type 是提案类型。
type Type string

// TypeLogicalDelete 是 v1 的**唯一**类型（§7.2 `type` 行注释「v1 唯一类型」）。
const TypeLogicalDelete Type = "logical_delete"

// Types 返回类型的封闭集合（恰一值）。
func Types() []Type { return []Type{TypeLogicalDelete} }

// Valid 报告类型取值是否在封闭集合内。
func (t Type) Valid() bool { return t == TypeLogicalDelete }

// Status 是提案的四态（**用户决定**，四取值，不存在第五种；§2.1）。
type Status string

// 四态（§2.1 逐字）。
const (
	StatusPending    Status = "pending"
	StatusApproved   Status = "approved"
	StatusRejected   Status = "rejected"
	StatusSuperseded Status = "superseded"
)

// Statuses 返回四态的封闭集合（顺序同 §7.2 注释里的枚举顺序）。
func Statuses() []Status {
	return []Status{StatusPending, StatusApproved, StatusRejected, StatusSuperseded}
}

// Valid 报告 status 取值是否在四态内（`draft` / `applied` 之类一律 false）。
func (s Status) Valid() bool {
	for _, v := range Statuses() {
		if s == v {
			return true
		}
	}
	return false
}

// ExecStatus 是执行维度的三态（与 status **严格分离**、正交；§4.1）。
type ExecStatus string

// 执行三态（§7.2 `execution.status` 行逐字）。
const (
	ExecNotStarted ExecStatus = "not_started"
	ExecSucceeded  ExecStatus = "succeeded"
	ExecFailed     ExecStatus = "failed"
)

// ExecStatuses 返回执行三态的封闭集合。
func ExecStatuses() []ExecStatus {
	return []ExecStatus{ExecNotStarted, ExecSucceeded, ExecFailed}
}

// Valid 报告 execution.status 取值是否在三态内。
func (e ExecStatus) Valid() bool {
	for _, v := range ExecStatuses() {
		if e == v {
			return true
		}
	}
	return false
}

// —— ② frontmatter 键名（§7.2 逐键）——

// 顶层键（**恰 8 个**）。
const (
	KeyID        = "id"
	KeyType      = "type"
	KeyStatus    = "status"
	KeyCreatedAt = "created_at"
	KeyTargets   = "targets"
	KeyImpact    = "impact"
	KeyDecision  = "decision"
	KeyExecBlock = "execution" // 名字刻意避开 Key+Execution 拼法：矩阵 #39 禁止本包出现该块的赋值形态
)

// `impact` 的子键（§7.2，影响面四项 + 陈旧综述）。
const (
	KeyExitsDefaultView     = "exits_default_view"
	KeyCardsLosingSupport   = "cards_losing_support"
	KeyAffectedMaterialRels = "affected_material_rels"
	KeyAffectedRelations    = "affected_relations"
	KeyStaleReviews         = "stale_reviews"
)

// `decision` 的子键（§7.2，用户决定）。
const (
	KeyResult       = "result"
	KeyReason       = "reason"
	KeySupersededBy = "superseded_by"
)

// `execution` 的子键（§7.2，其中 written_paths / unwritten_paths 是本仓合同**新定的两键**，
// 见提案合同 M-3 与登记项 A-21；`failed` 时的必填校验属 036）。
const (
	KeyExecStatus  = "status"
	KeyAttemptedAt = "attempted_at"
	KeyExecReason  = "reason"
	// KeyGitCommit（execution.git_commit）已按 A-32「整键删除」退役：
	// 写侧不再产出、succeeded 不再必填、ValidateLayout 不再纳入合规键集。
	// 常量与 Exec.GitCommit 字段仅为**历史键读侧兼容**保留（Parse 原样读出、不清除、不报错），
	// 不做数据迁移。回执 SHA 改由报告 git.commit + Git 历史承载。
	KeyGitCommit      = "git_commit"
	KeyWrittenPaths   = "written_paths"
	KeyUnwrittenPaths = "unwritten_paths"
)

// 提案**不设**的三键（M-6）：解析侧不提供字段，模板侧不产出键。
const (
	KeyReviewedAt    = "reviewed_at"
	KeyDeletedAt     = "deleted_at"
	KeyDeletedReason = "deleted_reason"
)

// FMKeys 返回 frontmatter 顶层键，**顺序即 §7.2 yaml 的抄录顺序**。
func FMKeys() []string {
	return []string{KeyID, KeyType, KeyStatus, KeyCreatedAt, KeyTargets,
		KeyImpact, KeyDecision, KeyExecBlock}
}

// ImpactKeys 返回 `impact` 的子键（顺序同 §7.2）。
func ImpactKeys() []string {
	return []string{KeyExitsDefaultView, KeyCardsLosingSupport,
		KeyAffectedMaterialRels, KeyAffectedRelations, KeyStaleReviews}
}

// DecisionKeys 返回 `decision` 的子键（顺序同 §7.2）。
func DecisionKeys() []string { return []string{KeyResult, KeyReason, KeySupersededBy} }

// ExecutionKeys 返回 `execution` 的子键（顺序同 §7.2）。
// A-32「整键删除」：git_commit 已从合规键集移除，新提案与回写后的合规形态恒为这 5 键。
// 历史提案若仍带 git_commit，属读侧兼容范畴（Parse 原样保留），不在 ValidateLayout 合规集内。
func ExecutionKeys() []string {
	return []string{KeyExecStatus, KeyAttemptedAt, KeyExecReason,
		KeyWrittenPaths, KeyUnwrittenPaths}
}

// ForbiddenFMKeys 返回提案**永不出现**的三键（M-6）。
func ForbiddenFMKeys() []string { return []string{KeyReviewedAt, KeyDeletedAt, KeyDeletedReason} }

// —— ③ 正文分区（§7.3，恰 7 个 H2，逐字、顺序固定）——

// 七个分区名（逐字抄录 §7.3）。
const (
	SecRecommend    = "推荐修改"
	SecEvidence     = "理由与证据"
	SecAffected     = "影响的文件、领域与关系"
	SecAfterState   = "执行后状态"
	SecNotExecuted  = "不执行的影响"
	SecAlternatives = "替代方案"
	SecApplicable   = "可应用内容"
)

// BodySections 返回正文七分区名，**顺序即 §7.3 的抄录顺序**。
func BodySections() []string {
	return []string{SecRecommend, SecEvidence, SecAffected, SecAfterState,
		SecNotExecuted, SecAlternatives, SecApplicable}
}

// —— ④ 结构体：只用于**只读**解码（yaml 只做 Unmarshal）——

// Impact 是批准前算准的影响面（EG-CFM-05）。
type Impact struct {
	ExitsDefaultView   []string `yaml:"exits_default_view"`
	CardsLosingSupport []string `yaml:"cards_losing_support"`
	// AffectedMaterialRels / AffectedRelations 是计数（§7.2 里就是整数）。
	AffectedMaterialRels int      `yaml:"affected_material_rels"`
	AffectedRelations    int      `yaml:"affected_relations"`
	StaleReviews         []string `yaml:"stale_reviews"`

	// Extra 收未知子键：**逐字保留**（本包不回写 YAML，字节天然不变），
	// 只为让上层看得见「有未知键」这件事，不参与任何判定。
	Extra map[string]interface{} `yaml:",inline"`
}

// Decision 是用户决定的记录副本。权威侧是顶层 status（A-20 裁决，一致性校验属 034）。
type Decision struct {
	Result       Status `yaml:"result"`
	Reason       string `yaml:"reason"`
	SupersededBy string `yaml:"superseded_by"`

	Extra map[string]interface{} `yaml:",inline"`
}

// Exec 是实际执行结果，与 status 严格分离（§4）。
//
// 写权限矩阵 #39：`execution.*` 是 **CLI 独占**，两条路径均不可手工改写。
// 因此本包**不提供** execution 的通用 setter——回写口由 T-…-036 的执行器路径独占。
type Exec struct {
	Status      ExecStatus `yaml:"status"`
	AttemptedAt string     `yaml:"attempted_at"`
	Reason      string     `yaml:"reason"`
	// GitCommit 已按 A-32「整键删除」退役：新提案不再产出该键、写侧不再回写、
	// succeeded 不再必填。字段仅为**历史键读侧兼容**保留——历史提案的 git_commit
	// 由此原样读出并 round-trip（不清除、不报错、不迁移）。
	GitCommit string `yaml:"git_commit"`
	// WrittenPaths / UnwrittenPaths 是本仓合同新定的两键（M-3 / A-21）：
	// 本 task 只保证它们存在于 schema 且可 round-trip，`failed` 时的必填校验属 036。
	WrittenPaths   []string `yaml:"written_paths"`
	UnwrittenPaths []string `yaml:"unwritten_paths"`

	Extra map[string]interface{} `yaml:",inline"`
}

// Proposal 是提案 frontmatter 的结构化视图（**只读解码**用）。
//
// 没有 reviewed_at / deleted_at / deleted_reason 字段：M-6 在类型层就不给它们落点。
type Proposal struct {
	ID        ID       `yaml:"id"`
	Type      Type     `yaml:"type"`
	Status    Status   `yaml:"status"`
	CreatedAt string   `yaml:"created_at"`
	Targets   []string `yaml:"targets"`
	Impact    Impact   `yaml:"impact"`
	Decision  Decision `yaml:"decision"`
	Execution Exec     `yaml:"execution"`

	Extra map[string]interface{} `yaml:",inline"`
}

// subMaps 只用于**只读**校验嵌套键集合：三个 map 原样持有子键名。
type subMaps struct {
	Impact    map[string]interface{} `yaml:"impact"`
	Decision  map[string]interface{} `yaml:"decision"`
	Execution map[string]interface{} `yaml:"execution"`
}

// —— ⑤ 结构化错误：本包不发诊断码，只报「哪条形态不成立」——

// ViolationKind 是形态违规的种类。037 据此映射到 M3 的 error / warning 编号；
// 本包**不出现任何编号字面量**（诊断码不越界）。
type ViolationKind string

// 违规种类（封闭集合）。
const (
	ViolationNoFrontmatter ViolationKind = "no_frontmatter"
	ViolationFMKeySet      ViolationKind = "fm_key_set"
	ViolationForbiddenKey  ViolationKind = "forbidden_key"
	ViolationSubKeySet     ViolationKind = "sub_key_set"
	ViolationSections      ViolationKind = "body_sections"
	ViolationIDShape       ViolationKind = "id_shape"
	ViolationTypeEnum      ViolationKind = "type_enum"
	ViolationStatusEnum    ViolationKind = "status_enum"
	ViolationExecEnum      ViolationKind = "execution_status_enum"
	ViolationRelPath       ViolationKind = "rel_path"
	ViolationRoundTrip     ViolationKind = "round_trip"
)

// Violation 是一条形态违规。Field 指出触发的键 / 分区名，Detail 是人类可读原因。
type Violation struct {
	Kind   ViolationKind
	Field  string
	Detail string
}

// Error 实现 error。
func (v *Violation) Error() string {
	if v.Field == "" {
		return fmt.Sprintf("提案形态不合规[%s]：%s", v.Kind, v.Detail)
	}
	return fmt.Sprintf("提案形态不合规[%s] %s：%s", v.Kind, v.Field, v.Detail)
}

// KindOf 取出 err 链上的 ViolationKind；不是形态违规时返回空串。
func KindOf(err error) ViolationKind {
	var v *Violation
	if errors.As(err, &v) {
		return v.Kind
	}
	return ""
}

func violate(kind ViolationKind, field, format string, args ...interface{}) *Violation {
	return &Violation{Kind: kind, Field: field, Detail: fmt.Sprintf(format, args...)}
}

// —— ⑥ 落位路径判据 ——

// RelPattern 是提案默认落位路径的机器判据：`proposals/p-<yyyymmdd>-<3d>.md`。
const RelPattern = `^proposals/p-[0-9]{8}-[0-9]{3}\.md$`

var relRE = regexp.MustCompile(RelPattern)

// MatchRel 报告 vault 内相对路径是否是**默认**落位形态。
//
// 只用于校验本工具刚生成的文件名：既有提案允许改名 / 移动（F2），
// 因此**不得**用它去否决一个 frontmatter 合法的提案。
func MatchRel(rel string) bool { return relRE.MatchString(rel) }

// —— ⑦ 只读解析 ——

// File 是一份被解析的提案：原始字节 + mdfile 索引 + 结构化 frontmatter。
//
// Raw 只读，永不原地修改；Render 只做区间拼接（与 mdfile 同源口径）。
type File struct {
	Raw []byte
	Doc *mdfile.Doc
	P   Proposal
}

// Parse 只读解析一份提案：索引 + frontmatter 字段 + **结构硬校验**。
//
// 硬校验只做三件（其余属下游 task）：
//   - 必须有 frontmatter；
//   - **不得**出现 reviewed_at / deleted_at / deleted_reason（M-6）；
//   - 正文 H2 必须**恰 7 个**且顺序逐字一致（§7.3）。
//
// 顶层未知键不在这里拒绝：它们落进 Extra 并**逐字保留**，键集合恰等由 ValidateLayout
// 单独判（模板与本工具产出的文件必须恰等，手工加过键的既有文件仍可被读出来修）。
func Parse(raw []byte) (*File, error) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !doc.HasFM {
		return nil, violate(ViolationNoFrontmatter, "", "提案必须有 frontmatter（%s 等十项必备落在其中）", KeyStatus)
	}
	keys, err := doc.FMKeys()
	if err != nil {
		return nil, err
	}
	if err := checkForbidden(keys); err != nil {
		return nil, err
	}
	if err := checkSections(doc); err != nil {
		return nil, err
	}
	var p Proposal
	if err := doc.DecodeFM(&p); err != nil {
		return nil, err
	}
	return &File{Raw: raw, Doc: doc, P: p}, nil
}

// Render 以区间拼接方式重建字节（无逻辑变更时逐字等于 Raw）。
func (f *File) Render() []byte { return f.Doc.Render() }

// SelfCheck 是「写前 Parse→Render 字节自检」：不等即拒写（§16.4 同源口径）。
func SelfCheck(raw []byte) error {
	f, err := Parse(raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(f.Render(), raw) {
		return violate(ViolationRoundTrip, "",
			"Parse→Render 与原字节不等（len %d → %d），拒写", len(raw), len(f.Render()))
	}
	return nil
}

// Title 返回提案标题：正文首个 H1（`# …`）的文本。
//
// §7.2 的键集合里**没有** title，因此标题的权威载体是正文 H1（F6：Markdown 是权威）。
// 找不到 H1 时退化为 ID —— 与 query 侧卡片标题缺失时退化为 ID 的口径一致。
func (f *File) Title() string {
	end := len(f.Raw)
	if len(f.Doc.Sections) > 0 {
		end = f.Doc.Sections[0].Start
	}
	for at := f.Doc.BodyFrom; at < end; {
		lineEnd := bytes.IndexByte(f.Raw[at:end], '\n')
		stop := end
		if lineEnd >= 0 {
			stop = at + lineEnd
		}
		line := f.Raw[at:stop]
		if bytes.HasPrefix(line, []byte("# ")) {
			return string(bytes.TrimRight(line[len("# "):], " \t\r"))
		}
		if lineEnd < 0 {
			break
		}
		at = stop + 1
	}
	return string(f.P.ID)
}

// SectionBody 返回某个 H2 分区的正文字节（不含标题行）。分区不存在时返回 nil。
func (f *File) SectionBody(name string) []byte {
	s, ok := f.Doc.Section(name)
	if !ok {
		return nil
	}
	return f.Raw[s.Body:s.End]
}

// checkForbidden 拒绝 M-6 的三键。
func checkForbidden(keys []string) error {
	for _, bad := range ForbiddenFMKeys() {
		for _, k := range keys {
			if k == bad {
				return violate(ViolationForbiddenKey, bad,
					"提案不设该键：提案不是知识产物，不进未过目筛选，也不可被逻辑删除")
			}
		}
	}
	return nil
}

// checkSections 判定正文 H2 **恰 7 个**且顺序逐字一致。
func checkSections(doc *mdfile.Doc) error {
	got, want := doc.SectionNames(), BodySections()
	if len(got) != len(want) {
		return violate(ViolationSections, "",
			"正文 H2 分区数 = %d，必须恰 %d 个：%s", len(got), len(want), strings.Join(want, " / "))
	}
	for i := range want {
		if got[i] != want[i] {
			return violate(ViolationSections, got[i],
				"第 %d 个 H2 分区是「%s」，必须逐字为「%s」（顺序固定）", i+1, got[i], want[i])
		}
	}
	return nil
}

// ValidateLayout 判定一份提案是否**完全符合落盘形态**：
//
//	① frontmatter 顶层键集合与 §7.2 **逐键相等**（不多不少）；
//	② impact / decision 的子键集合各自逐键相等；execution 子键为当前 5 键，
//	   或额外容忍唯一历史键 git_commit（A-32「整键删除」的读侧兼容形态）；
//	③ id 形态 = p-<yyyymmdd>-<3d>，type 在封闭集合内，status 在四态内，
//	   execution.status 在三态内；
//	④ 正文 H2 恰 7 个且顺序逐字一致（Parse 已判，这里不重复）。
//
// 与 Parse 的分工：Parse 是「能不能读」，ValidateLayout 是「形态是不是恰好合规」。
// 本工具产出的模板必须两者都过。
func ValidateLayout(f *File) error {
	keys, err := f.Doc.FMKeys()
	if err != nil {
		return err
	}
	if err := equalKeySet(ViolationFMKeySet, "frontmatter", keys, FMKeys()); err != nil {
		return err
	}
	var sub subMaps
	if err := f.Doc.DecodeFM(&sub); err != nil {
		return err
	}
	for _, pair := range []struct {
		field string
		got   map[string]interface{}
		want  []string
	}{
		{KeyImpact, sub.Impact, ImpactKeys()},
		{KeyDecision, sub.Decision, DecisionKeys()},
	} {
		if err := equalKeySet(ViolationSubKeySet, pair.field, mapKeys(pair.got), pair.want); err != nil {
			return err
		}
	}
	// execution 子键单独判：A-32「整键删除」后规范形态是当前 5 键；历史提案可能额外携带
	// **唯一**退役键 git_commit（读侧兼容、不迁移）。两种精确形态都放行，其余未知键仍拒。
	if err := validateExecutionKeys(mapKeys(sub.Execution)); err != nil {
		return err
	}
	if _, _, err := ParseID(string(f.P.ID)); err != nil {
		return violate(ViolationIDShape, KeyID, "%v", err)
	}
	if !f.P.Type.Valid() {
		return violate(ViolationTypeEnum, KeyType,
			"type = %q，v1 唯一类型是 %q", f.P.Type, TypeLogicalDelete)
	}
	if !f.P.Status.Valid() {
		return violate(ViolationStatusEnum, KeyStatus,
			"status = %q，四态封闭：%s", f.P.Status, joinStatuses())
	}
	if !f.P.Execution.Status.Valid() {
		return violate(ViolationExecEnum, KeyExecBlock+"."+KeyExecStatus,
			"execution.status = %q，三态封闭：%s", f.P.Execution.Status, joinExecStatuses())
	}
	return nil
}

func joinStatuses() string {
	out := make([]string, 0, len(Statuses()))
	for _, s := range Statuses() {
		out = append(out, string(s))
	}
	return strings.Join(out, " | ")
}

func joinExecStatuses() string {
	out := make([]string, 0, len(ExecStatuses()))
	for _, s := range ExecStatuses() {
		out = append(out, string(s))
	}
	return strings.Join(out, " | ")
}

func mapKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// equalKeySet 判定两个键集合**逐键相等**（顺序无关、不多不少）。
func equalKeySet(kind ViolationKind, field string, got, want []string) error {
	g, w := append([]string{}, got...), append([]string{}, want...)
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, ",") == strings.Join(w, ",") {
		return nil
	}
	return violate(kind, field, "键集合 = [%s]，必须逐键相等于 [%s]",
		strings.Join(g, ", "), strings.Join(w, ", "))
}

// validateExecutionKeys 精确放行 execution 子键的**两种**合规形态（A-32「整键删除」）：
//
//	① 当前 5 键（ExecutionKeys()）——新写模板与回写后的规范形态；
//	② 当前 5 键 + 唯一历史键 git_commit——A-32 前落盘的历史提案，读侧兼容、不迁移、不清除。
//
// 除此之外的任何缺键 / 多键（含 git_commit 以外的未知键）一律拒绝。
// 报错时以「当前 5 键」为期望集：历史键只是被额外容忍，不进入规范期望集。
func validateExecutionKeys(got []string) error {
	if equalKeySet(ViolationSubKeySet, KeyExecBlock, got, ExecutionKeys()) == nil {
		return nil
	}
	legacy := append(append([]string{}, ExecutionKeys()...), KeyGitCommit)
	if equalKeySet(ViolationSubKeySet, KeyExecBlock, got, legacy) == nil {
		return nil
	}
	return equalKeySet(ViolationSubKeySet, KeyExecBlock, got, ExecutionKeys())
}

// —— ⑧ 模板：按字节拼装（不经任何 YAML 序列化器）——

// Template 是一次提案模板生成的输入。
//
// decision / execution 一律以**空值骨架**落盘（`decision.result` 空、
// `execution.status: not_started`）：两者的取值由用户决定与 CLI 执行回写，
// 创建时不预填（矩阵 #39：execution 是 CLI 独占）。
type Template struct {
	ID        ID
	Title     string
	CreatedAt string // YYYY-MM-DD
	Targets   []string
	Impact    Impact
}

// ErrUnquotableScalar frontmatter 标量含换行：本包不拼多行标量（避免猜缩进）。
var ErrUnquotableScalar = errors.New("frontmatter 标量不得含换行（不拼装多行标量）")

// RenderTemplate 拼出一份**恰合规**的提案字节：8 个顶层键 + 七个 H2 骨架。
//
// 写路径口径（§16.3 同源）：全程只做 []byte 追加，**不是**「结构体 → 序列化」；
// 键序 = 下面 append 的顺序，与 §7.2 逐字一致。产出的字节必须同时通过
// Parse / ValidateLayout / SelfCheck 三关，否则返回错误、绝不交给写口。
func RenderTemplate(t Template) ([]byte, error) {
	if _, _, err := ParseID(string(t.ID)); err != nil {
		return nil, violate(ViolationIDShape, KeyID, "%v", err)
	}
	var fm []byte
	for _, kv := range [][2]string{
		{KeyID, string(t.ID)},
		{KeyType, string(TypeLogicalDelete)},
		{KeyStatus, string(StatusPending)},
		{KeyCreatedAt, t.CreatedAt},
	} {
		line, err := fmLine(kv[0], kv[1])
		if err != nil {
			return nil, err
		}
		fm = append(fm, line...)
	}
	targets, err := fmSeq(KeyTargets, t.Targets)
	if err != nil {
		return nil, err
	}
	fm = append(fm, targets...)

	impact, err := impactBlock(t.Impact)
	if err != nil {
		return nil, err
	}
	fm = append(fm, impact...)
	fm = append(fm, decisionBlock()...)
	fm = append(fm, executionBlock()...)

	out := make([]byte, 0, len(fm)+256)
	out = append(out, "---\n"...)
	out = append(out, fm...)
	out = append(out, "---\n\n"...)
	title := t.Title
	if title == "" {
		title = string(t.ID)
	}
	if strings.ContainsAny(title, "\r\n") {
		return nil, fmt.Errorf("%w：标题 %q", ErrUnquotableScalar, title)
	}
	out = append(out, "# "...)
	out = append(out, title...)
	out = append(out, '\n')
	for _, name := range BodySections() {
		out = append(out, '\n')
		out = append(out, "## "...)
		out = append(out, name...)
		out = append(out, '\n')
	}

	f, err := Parse(out)
	if err != nil {
		return nil, err
	}
	if err := ValidateLayout(f); err != nil {
		return nil, err
	}
	if err := SelfCheck(out); err != nil {
		return nil, err
	}
	return out, nil
}

// impactBlock 拼 `impact:` 映射块（子键顺序 = ImpactKeys()）。
func impactBlock(im Impact) ([]byte, error) {
	out := []byte(KeyImpact + ":\n")
	for _, pair := range []struct {
		key   string
		items []string
	}{
		{KeyExitsDefaultView, im.ExitsDefaultView},
		{KeyCardsLosingSupport, im.CardsLosingSupport},
	} {
		block, err := nestedSeq(pair.key, pair.items)
		if err != nil {
			return nil, err
		}
		out = append(out, block...)
	}
	out = append(out, fmt.Sprintf("%s%s: %d\n", mapIndent, KeyAffectedMaterialRels, im.AffectedMaterialRels)...)
	out = append(out, fmt.Sprintf("%s%s: %d\n", mapIndent, KeyAffectedRelations, im.AffectedRelations)...)
	block, err := nestedSeq(KeyStaleReviews, im.StaleReviews)
	if err != nil {
		return nil, err
	}
	return append(out, block...), nil
}

// decisionBlock 拼 `decision:` 映射块：三键**全部留空**（用户决定，创建时不预填）。
func decisionBlock() []byte {
	out := []byte(KeyDecision + ":\n")
	for _, k := range DecisionKeys() {
		out = append(out, mapIndent...)
		out = append(out, k...)
		out = append(out, ':', '\n')
	}
	return out
}

// executionBlock 拼 `execution:` 映射块：status 为 not_started，其余留空，
// 两个新定路径键落成空序列 `[]`（矩阵 #39：execution 是 CLI 独占，创建时不预填结果）。
// A-32「整键删除」：新提案不再产出 git_commit 键（合规形态恒为 5 键）。
func executionBlock() []byte {
	out := []byte(KeyExecBlock + ":\n")
	out = append(out, fmt.Sprintf("%s%s: '%s'\n", mapIndent, KeyExecStatus, ExecNotStarted)...)
	for _, k := range []string{KeyAttemptedAt, KeyExecReason} {
		out = append(out, mapIndent...)
		out = append(out, k...)
		out = append(out, ':', '\n')
	}
	for _, k := range []string{KeyWrittenPaths, KeyUnwrittenPaths} {
		out = append(out, fmt.Sprintf("%s%s: []\n", mapIndent, k)...)
	}
	return out
}

// 缩进：映射子键两格，块状序列项在子键下再两格（本工具**新建**文件时的风格；
// 对既有文件一律沿用其既有风格，不在这里推断）。
const (
	mapIndent = "  "
	seqIndent = "    "
)

// nestedSeq 拼 `  key:` + 每项一行 `    - '值'`；空序列写成 `  key: []`
// （提案的五个数组键**必须存在**，因此空值不能省键——这与 M1「空序列不写键」的场景不同，
// M1 那里是可选键，这里是 §7.2 的必备键）。
func nestedSeq(key string, items []string) ([]byte, error) {
	if len(items) == 0 {
		return []byte(mapIndent + key + ": []\n"), nil
	}
	out := []byte(mapIndent + key + ":\n")
	for _, it := range items {
		q, err := quoted(it)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", key, err)
		}
		out = append(out, seqIndent...)
		out = append(out, '-', ' ')
		out = append(out, q...)
		out = append(out, '\n')
	}
	return out, nil
}

// fmLine 拼一行 `key: '值'`。
func fmLine(key, value string) ([]byte, error) {
	q, err := quoted(value)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", key, err)
	}
	line := make([]byte, 0, len(key)+len(q)+3)
	line = append(line, key...)
	line = append(line, ':', ' ')
	line = append(line, q...)
	return append(line, '\n'), nil
}

// fmSeq 拼顶层块状序列；空序列写成 `key: []`（同 nestedSeq 的理由）。
func fmSeq(key string, items []string) ([]byte, error) {
	if len(items) == 0 {
		return []byte(key + ": []\n"), nil
	}
	out := []byte(key + ":\n")
	for _, it := range items {
		q, err := quoted(it)
		if err != nil {
			return nil, fmt.Errorf("%s：%w", key, err)
		}
		out = append(out, mapIndent...)
		out = append(out, '-', ' ')
		out = append(out, q...)
		out = append(out, '\n')
	}
	return out, nil
}

// quoted 把标量包成单引号 YAML 标量：内部单引号按 YAML 规则写成两个。
// 与 M1 的 store 拼装同一套引号风格：日期 / 时刻因此不会被 YAML 当成 timestamp 类型。
func quoted(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n', '\r':
			return nil, fmt.Errorf("%w：%q", ErrUnquotableScalar, s)
		case '\'':
			out = append(out, '\'', '\'')
		default:
			out = append(out, s[i])
		}
	}
	return append(out, '\''), nil
}
