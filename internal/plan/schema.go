package plan

// ChangePlan 的**只读**反序列化（合同 §1 / §2 / §3 / §6）。
//
// 写路径硬约束（施工索引 §16）：本包只 Unmarshal，**永不**序列化回写——
// YAML 序列化器（`make lint` 的 guard 步守卫的那两个符号）连注释里都不出现。
// plan 与 op 的**未知附加字段**原样忽略并产出一条 I1 info（前向兼容规则第 1 条）；
// **未知 op 报 E5 error，整条 op 不执行**（第 2 条）。
// 用户内容（正文分区、原文正文、reason 文本）一律以 []byte 交给写入侧，本包不做规范化。

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// PlanVersion 是**当前**的 plan 版本（Schema v2 契约 §4.1）。
//
// v1 → v2 的差别只在写口：`write_note` 由固定分区 `sections` 改为有序 `blocks[]`，
// 并新增 `create_knowledge` / `append_knowledge` / `create_opinion` / `append_opinion`
// 四个 op。顶层 8 键**一个未改**（契约 §4.1 末条），因此 v1 与 v2 共用同一条解析路径。
const PlanVersion = 2

// PlanVersionV1 是**兼容期**仍被接受的旧版本号。
//
// 单列成常量而不是写裸 1：v1 兼容分支在 planLevel（I1 提示）、writeNote（走 sections 口径）
// 与 noteBlocks（互斥判定）三处都要判版本，三处必须引用同一个字面量。
const PlanVersionV1 = 1

// SupportedPlanVersions 是受支持的版本集合（声明顺序 = 从旧到新）。
//
// 为什么是「集合」而不是「最低版本 + 单调放宽」：plan 版本不是语义化版本，
// 两个版本各自对应一套**写口形态**，不存在「≥ N 都行」的连续区间。
// 集合让「哪一版被接受」可机器复算，也让兼容期结束时的收窄只需删一个元素。
func SupportedPlanVersions() []int { return []int{PlanVersionV1, PlanVersion} }

// PlanVersionSupported 报告某个版本号是否在受支持集合内。
func PlanVersionSupported(n int) bool {
	for _, ok := range SupportedPlanVersions() {
		if n == ok {
			return true
		}
	}
	return false
}

// TopLevelKeys 是顶层键的封闭集合（恰 8 个，合同 §1）。
func TopLevelKeys() []string {
	return []string{"plan_version", "verb", "domain", "reason",
		"requirement_ids", "convergence", "base", "ops"}
}

// 主链路 op 名（封闭集合，合同 §3 + Schema v2 契约 §4.4）。
const (
	OpAddSource       = "add_source"
	OpWriteNote       = "write_note"
	OpAddMaterialRel  = "add_material_rel"
	OpAddRelation     = "add_relation"
	OpAddOpenQuestion = "add_open_question"

	// OpCreateKnowledge / OpAppendKnowledge 是 Knowledge 的**规范名**（契约 §4.4）。
	OpCreateKnowledge = "create_knowledge"
	OpAppendKnowledge = "append_knowledge"

	// OpCreateOpinion / OpAppendOpinion 是 Opinion 的写口（契约 §4.4，新增）。
	OpCreateOpinion = "create_opinion"
	OpAppendOpinion = "append_opinion"

	// OpCreateCard / OpAppendCard 是**兼容别名**，在 normalizeAliases 阶段改写成
	// 上面两个 Knowledge 规范名，validate 与 executor **看不到**它们（契约 §4.4）。
	OpCreateCard = "create_card"
	OpAppendCard = "append_card"
)

// OpNames 是主链路 op 的**规范名**（恰九个，声明顺序即契约 §4.4 表格顺序）。
//
// 两个兼容别名**不在**本清单里：它们不是独立 op，只是同一个 op 的旧名字，
// 在解析结束前就已被改写。把别名也列进来会让「本阶段恰有 N 个 op」的报错文案
// 把同一件事数两遍。
func OpNames() []string {
	return []string{OpAddSource, OpWriteNote, OpCreateKnowledge, OpAppendKnowledge,
		OpCreateOpinion, OpAppendOpinion, OpAddMaterialRel, OpAddRelation, OpAddOpenQuestion}
}

// OpAliases 是「兼容别名 → 规范名」的唯一真源（契约 §4.4）。
//
// 唯一真源的意思是：改写在 normalizeAliases 一处发生，报错文案、迁移提示与用例
// 全部读这一份表。禁止在 validate / executor 里各写一份 `case "create_card"`——
// 两处分支必然漂移，而漂移的表现是「同一份 plan 在校验期与执行期作用于不同实体」。
func OpAliases() map[string]string {
	return map[string]string{
		OpCreateCard: OpCreateKnowledge,
		OpAppendCard: OpAppendKnowledge,
	}
}

// s2OpNames 是「归属 S2+ 但**本仓仍未定义语义**」的 op：一律按「未知 op」拒绝（E5），
// 不猜语义、不降级为 warning。本切片只用于把错误信息写得更明确。
//
// M3（T-…-037）已把 `deprecate` / `restore` / `set_replaced_by` / `delete` / `undelete` /
// `mark_reviewed` / `replace_block` / `remove_relation` **八个搬进 M3OpNames 实装**，
// 故它们**不再**在此列；剩下三个 `set_tags` / `reprocess_note` / `save_review`（A-18 归属未知）
// 继续按未知 op 拒绝（授权合同 §9），本阶段不为它们定义任何字段。
func s2OpNames() []string {
	// A-18：仅用于把「未知 op」的错误信息写得更明确，不是字段定义、不参与派发。
	return []string{"set_tags", "reprocess_note", "save_review"} // A-18
}

// ConvergeRelations 是 convergence[].relation 的**七值封闭枚举**（合同 §2.1）。
func ConvergeRelations() []string {
	return []string{"independent_new", "same_semantics", "non_core_supplement",
		"core_change", "conflict_coexist", "uncertain", "deprecated"}
}

// CoverageGaps 是 coverage_gaps 的**受控枚举七值**（合同 §3.2.1）。
func CoverageGaps() []string {
	return []string{"core_claim", "key_evidence", "counterexample", "boundary",
		"method", "conclusion", "limitation"}
}

// Convergence 是一条逐卡收敛条目（合同 §2）。
type Convergence struct {
	Index         int
	Card          string
	Relation      string
	RelationGiven bool
	Core          string
	Conditions    string
	ReusePurpose  string
	Note          string
	Extra         map[string]interface{}
}

// OutputCard 是笔记「产出知识卡」的一条快照（`- k-xxx（新建｜复用｜补充）`）。
type OutputCard struct {
	Card string
	Mode string
}

// MaterialRef 是材料关系四要素（写进知识卡 frontmatter 的 sources[]）。
type MaterialRef struct {
	Source string
	Note   string
	Rel    string
	Reason string
}

// Op 是一条已解析的 op。七个 op 的字段合成一个联合体：字段名与合同 §3 的字段表逐字一致，
// 未出现在该 op 字段表里的键进 Extra（原样忽略 + I1）。
type Op struct {
	Index int
	Name  string

	// add_source
	URL          string
	Body         []byte
	SavedAt      string
	TargetDomain string
	SourceID     string

	// write_note
	Source       string
	NoteID       string
	OutputCards  []OutputCard
	Gaps         []string
	Reprocess    bool
	ReprocessSet bool
	// Blocks 是 v2 `write_note` 的**有序块数组**（契约 §4.2）：数组顺序即落盘顺序。
	// BlocksGiven 区分「缺 blocks 字段」与「给了空数组」——前者可能是 v1 plan，
	// 后者是「声称按块整理却一个块都没有」，两种成因的诊断不同，不得折叠成一个判断。
	Blocks      []NoteBlock
	BlocksGiven bool
	// Omissions / ExtractionCoverage 是 v2 `write_note` 的审阅式提炼两组清单
	// （契约 §4.2.1 遗漏项 / §4.2.3 提炼覆盖），数组顺序原样保留（不排序/不去重/不重排）。
	// OmissionsGiven / ExtractionCoverageGiven 区分「缺该字段」与「给了空数组」——
	// 空数组是「声称审阅过但一项都没记」，与「压根没提供该字段」的成因不同，
	// 后续批次对二者的诊断不同，故不得折叠成「切片长度是否为 0」一个判断。
	Omissions               []Omission
	OmissionsGiven          bool
	ExtractionCoverage      []ExtractionCoverage
	ExtractionCoverageGiven bool
	// CandidateDrafts / CandidateCoverage are draft-time extraction facts.
	// They are intentionally disjoint from OutputCards / ExtractionCoverage,
	// which only describe already materialized outputs.
	CandidateDrafts        []CandidateDraft
	CandidateDraftsGiven   bool
	CandidateCoverage      []CandidateCoverage
	CandidateCoverageGiven bool

	// create_opinion / append_opinion
	//
	// OpinionID / Opinion 与 CardID / Card **不复用**同一对字段：Opinion 与 Knowledge
	// 是同级实体，共用字段会让「这条 op 到底作用于哪一类产物」只能靠 op 名反推，
	// 而 executor 的落点（opinions/ vs cards/）恰恰不能靠反推决定。
	OpinionID string
	Opinion   string
	// Validation 是 `create_opinion` 显式给出的验证状态；ValidationGiven 区分
	// 「缺该键」（默认 pending）与「显式给了值」（越权取值须判 E2）。
	Validation      string
	ValidationGiven bool

	// create_card
	CardID  string
	Sources []MaterialRef
	// SourcesGiven 区分「缺 sources[] 字段」与「sources[] 是空数组」（两种都拒绝建卡）。
	SourcesGiven bool

	// 共用字段
	Title       string
	Domain      string
	DomainGiven bool
	Tags        []string
	Sections    map[string][]byte
	Card        string
	Note        string
	Rel         string
	From        string
	Type        string
	Target      string
	Question    string
	Reason      string
	ReasonGiven bool

	// M3 新增 8 个 op 的字段（字段表见 ops_m3.go 的 M3OpFields；解析在 parseM3Fields）。
	// InitiatorGiven 区分「缺 initiator 字段」与「给了但取值不是 user」（W7 的两种成因）。
	Initiator      string
	InitiatorGiven bool
	// Proposal 是 delete 引用的提案 ID（必须 status=approved）。
	Proposal string
	// Content 属 edit_section（`eg edit` 的新分区正文）；ContentGiven 区分「缺 content」
	// 与「给了空串」——两者的诊断成因不同，不得折叠成一个判断。
	Content      []byte
	ContentGiven bool
	// Section / Block / BaseBlockHash 属 replace_block 与 edit_section；
	// BlockGiven 区分「缺 block」与「空块」。
	Section       string
	Candidate     string
	Block         []byte
	BlockGiven    bool
	BaseBlockHash string
	// ReplacedBy 属 set_replaced_by（nil 表示未给该字段）。
	ReplacedBy *ReplacedBy

	Extra map[string]interface{}
}

// ChangePlan 是一份已解析的 ChangePlan。
type ChangePlan struct {
	Version     int
	VersionRaw  interface{}
	Verb        string
	VerbGiven   bool
	Domain      string
	DomainGiven bool
	DomainNull  bool

	Reason         string
	RequirementIDs []string
	Convergence    []Convergence
	Base           map[string]string
	Ops            []*Op

	Extra map[string]interface{}
	// Diags 是解析期诊断（未知字段 I1、类型不成立的 error 等），Validate 会原样并入结果。
	Diags []Diagnostic
}

// Parse 只读解析一份 ChangePlan（JSON 或 YAML；JSON 是 YAML 的子集，同一条解析路径）。
// 返回 error 只表示「整份 plan 无法解析成映射」；字段级问题一律以诊断表达。
func Parse(raw []byte) (*ChangePlan, error) {
	var root map[string]interface{}
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("plan 无法解析（既不是合法 JSON 也不是合法 YAML）：%w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("plan 为空：顶层必须是对象，且恰含 %d 个键", len(TopLevelKeys()))
	}
	p := &ChangePlan{Base: map[string]string{}, Extra: map[string]interface{}{}}
	known := set(TopLevelKeys())

	if v, ok := root["plan_version"]; ok {
		p.VersionRaw = v
		if n, ok := asInt(v); ok {
			p.Version = n
		}
	}
	if v, ok := root["verb"]; ok {
		p.VerbGiven = true
		p.Verb, _ = asString(v)
	}
	if v, ok := root["domain"]; ok {
		p.DomainGiven = true
		if v == nil {
			p.DomainNull = true
		} else {
			p.Domain, _ = asString(v)
		}
	}
	if v, ok := root["reason"]; ok {
		p.Reason, _ = asString(v)
	}
	if v, ok := root["requirement_ids"]; ok {
		p.RequirementIDs = asStringSlice(v)
	}
	if v, ok := root["convergence"]; ok {
		p.Convergence, p.Diags = parseConvergence(v, p.Diags)
	}
	if v, ok := root["base"]; ok {
		if m, ok := asMap(v); ok {
			for k, hv := range m {
				s, _ := asString(hv)
				p.Base[k] = s
			}
		} else if v != nil {
			p.Diags = append(p.Diags, errorAt(E5, NonOp, "base",
				"base 必须是「id 或路径 → content_hash」的映射"))
		}
	}
	if v, ok := root["ops"]; ok {
		items, ok := asList(v)
		if !ok && v != nil {
			p.Diags = append(p.Diags, errorAt(E5, NonOp, "ops", "ops 必须是列表"))
		}
		for i, item := range items {
			op, diags := parseOp(i, item)
			p.Diags = append(p.Diags, diags...)
			if op != nil {
				p.Ops = append(p.Ops, op)
			}
		}
	}
	for k, v := range root {
		if known[k] {
			continue
		}
		p.Extra[k] = v
		p.Diags = append(p.Diags, classifyExtra(NonOp, "plan", "plan."+k, k))
	}
	// 别名改写是解析的**最后一步**（契约 §4.4）：此后 validate 与 executor 只见规范名。
	normalizeAliases(p)
	return p, nil
}

func parseConvergence(v interface{}, diags []Diagnostic) ([]Convergence, []Diagnostic) {
	items, ok := asList(v)
	if !ok && v != nil {
		return nil, append(diags, warnAt(W5, NonOp, "convergence", "convergence 必须是条目列表"))
	}
	known := set([]string{"card", "relation", "core_knowledge", "conditions",
		"reuse_purpose", "note"})
	var out []Convergence
	for i, item := range items {
		m, ok := asMap(item)
		if !ok {
			diags = append(diags, warnAt(W5, NonOp, fmt.Sprintf("convergence[%d]", i),
				"convergence 条目必须是对象"))
			continue
		}
		c := Convergence{Index: i, Extra: map[string]interface{}{}}
		c.Card, _ = asString(m["card"])
		if rv, ok := m["relation"]; ok {
			c.RelationGiven = true
			c.Relation, _ = asString(rv)
		}
		c.Core, _ = asString(m["core_knowledge"])
		c.Conditions, _ = asString(m["conditions"])
		c.ReusePurpose, _ = asString(m["reuse_purpose"])
		c.Note, _ = asString(m["note"])
		for k, ev := range m {
			if known[k] {
				continue
			}
			c.Extra[k] = ev
			diags = append(diags, infoAt(NonOp, fmt.Sprintf("convergence[%d].%s", i, k),
				"未知附加字段已原样忽略（前向兼容）"))
		}
		out = append(out, c)
	}
	return out, diags
}

// opKnownKeys 是每个 op 的字段表键集合（合同 §3 逐个 op 的字段表）。
func opKnownKeys(name string) []string {
	switch name {
	case OpAddSource:
		return []string{"op", "url", "title", "body", "reason", "source_id",
			"saved_at", "target_domain", "tags"}
	case OpWriteNote:
		// `blocks` 必须是**已知字段**：否则 classifyExtra 会把它当未知附加字段
		// 原样忽略并只记一条 I1，v2 的 write_note 会静默退化成「没有任何正文」。
		// `omissions` / `extraction_coverage` 同理：不列进字段表就会被当未知字段吞掉，
		// 审阅式提炼的两组清单会静默丢失。
		return []string{"op", "source", "note_id", "title", "domain", "tags",
			"sections", "blocks", "output_cards", "coverage_gaps", "reprocess",
			"omissions", "extraction_coverage", "candidate_drafts", "candidate_coverage"}
	case OpCreateKnowledge, OpCreateCard:
		return []string{"op", "title", "card_id", "domain", "tags", "sources", "sections"}
	case OpAppendKnowledge, OpAppendCard:
		return []string{"op", "card", "sections"}
	case OpCreateOpinion:
		// `validation` 是 Opinion 唯一新增的 frontmatter 键（契约 §3.4）。它是**已知字段**
		// 而不是 extra：只有已知才能对「plan 内直接写 validated/rejected」判 E2；
		// 若走 extra 分支，越权取值会被当成前向兼容字段原样忽略。
		return []string{"op", "title", "opinion_id", "domain", "tags", "sources",
			"sections", "validation"}
	case OpAppendOpinion:
		return []string{"op", "opinion", "sections"}
	case OpAddMaterialRel:
		return []string{"op", "card", "source", "note", "rel", "reason"}
	case OpAddRelation:
		return []string{"op", "from", "type", "target", "reason"}
	case OpAddOpenQuestion:
		return []string{"op", "note", "question"}
	default:
		if keys := m3OpKnownKeys(name); keys != nil {
			return keys
		}
		if keys := editOpKnownKeys(name); keys != nil {
			return keys
		}
		// M4 · A-33 的 `set_stale`（R6 综述失准标记，见 ops_m3.go / validate_m4.go）。
		if keys := m4OpKnownKeys(name); keys != nil {
			return keys
		}
		return []string{"op"}
	}
}

// parseOp 解析一条 op。未知 op 名同样返回 Op（Name 原样保留），由 Validate 判 E5。
func parseOp(index int, item interface{}) (*Op, []Diagnostic) {
	var diags []Diagnostic
	m, ok := asMap(item)
	if !ok {
		return nil, append(diags, errorAt(E5, index, opPath(index, ""), "op 必须是对象"))
	}
	op := &Op{Index: index, Extra: map[string]interface{}{}}
	op.Name, _ = asString(m["op"])

	op.URL, _ = asString(m["url"])
	op.Title, _ = asString(m["title"])
	if v, ok := m["body"]; ok {
		s, _ := asString(v)
		op.Body = []byte(s)
	}
	op.SavedAt, _ = asString(m["saved_at"])
	op.TargetDomain, _ = asString(m["target_domain"])
	op.SourceID, _ = asString(m["source_id"])
	op.OpinionID, _ = asString(m["opinion_id"])
	op.Opinion, _ = asString(m["opinion"])
	if v, ok := m["validation"]; ok {
		op.ValidationGiven = true
		op.Validation, _ = asString(v)
	}
	op.Source, _ = asString(m["source"])
	op.NoteID, _ = asString(m["note_id"])
	op.CardID, _ = asString(m["card_id"])
	op.Card, _ = asString(m["card"])
	op.Note, _ = asString(m["note"])
	op.Rel, _ = asString(m["rel"])
	op.From, _ = asString(m["from"])
	op.Type, _ = asString(m["type"])
	op.Target, _ = asString(m["target"])
	op.Question, _ = asString(m["question"])
	if v, ok := m["reason"]; ok {
		op.ReasonGiven = true
		op.Reason, _ = asString(v)
	}
	if v, ok := m["domain"]; ok {
		op.DomainGiven = true
		op.Domain, _ = asString(v)
	}
	if v, ok := m["tags"]; ok {
		op.Tags = asStringSlice(v)
	}
	if v, ok := m["reprocess"]; ok {
		op.ReprocessSet = true
		op.Reprocess, _ = asBool(v)
	}
	if v, ok := m["coverage_gaps"]; ok {
		op.Gaps = asStringSlice(v)
	}
	if v, ok := m["sections"]; ok {
		sm, ok := asMap(v)
		if !ok {
			diags = append(diags, errorAt(E5, index, opPath(index, "sections"),
				"sections 必须是「分区名 → 文本」的映射"))
		} else {
			op.Sections = map[string][]byte{}
			for k, sv := range sm {
				s, _ := asString(sv)
				op.Sections[k] = []byte(s)
			}
		}
	}
	if v, ok := m["blocks"]; ok {
		op.BlocksGiven = true
		blocks, bd := parseNoteBlocks(index, v)
		op.Blocks = blocks
		diags = append(diags, bd...)
	}
	if v, ok := m["omissions"]; ok {
		op.OmissionsGiven = true
		oms, od := parseOmissions(index, v)
		op.Omissions = oms
		diags = append(diags, od...)
	}
	if v, ok := m["extraction_coverage"]; ok {
		op.ExtractionCoverageGiven = true
		cov, cd := parseExtractionCoverage(index, v)
		op.ExtractionCoverage = cov
		diags = append(diags, cd...)
	}
	if v, ok := m["candidate_drafts"]; ok {
		op.CandidateDraftsGiven = true
		drafts, dd := parseCandidateDrafts(index, v)
		op.CandidateDrafts = drafts
		diags = append(diags, dd...)
	}
	if v, ok := m["candidate_coverage"]; ok {
		op.CandidateCoverageGiven = true
		coverage, cd := parseCandidateCoverage(index, v)
		op.CandidateCoverage = coverage
		diags = append(diags, cd...)
	}
	if v, ok := m["output_cards"]; ok {
		for _, oc := range listOf(v) {
			cm, ok := asMap(oc)
			if !ok {
				continue
			}
			card, _ := asString(cm["card"])
			mode, _ := asString(cm["mode"])
			op.OutputCards = append(op.OutputCards, OutputCard{Card: card, Mode: mode})
		}
	}
	if v, ok := m["sources"]; ok {
		op.SourcesGiven = true
		for _, sr := range listOf(v) {
			rm, ok := asMap(sr)
			if !ok {
				continue
			}
			ref := MaterialRef{}
			ref.Source, _ = asString(rm["source"])
			ref.Note, _ = asString(rm["note"])
			ref.Rel, _ = asString(rm["rel"])
			ref.Reason, _ = asString(rm["reason"])
			op.Sources = append(op.Sources, ref)
		}
	}

	diags = parseM3Fields(op, m, diags)
	// A-13 载体 `edit_section` 的独有字段（见 edit_section.go）。
	parseEditFields(op, m)

	known := set(opKnownKeys(op.Name))
	prefix := fieldPathPrefix(op.Name)
	for k := range m {
		if known[k] {
			continue
		}
		op.Extra[k] = m[k]
		diags = append(diags, classifyExtra(index, prefix, opPath(index, k), k))
	}
	return op, diags
}

// fieldPathPrefix 是该 op 的产物字段路径前缀（黑名单只按字段路径判定，见 classifyExtra）。
func fieldPathPrefix(name string) string {
	switch name {
	case OpCreateOpinion, OpAppendOpinion:
		// 黑名单对称项是 `opinion.type` / `opinion.stance` / `opinion.lean`
		// （契约 §3.1）：观点的类型由 ID 前缀与目录表达，不由字段表达。
		return "opinion"
	case OpCreateKnowledge, OpAppendKnowledge, OpCreateCard, OpAppendCard,
		OpAddMaterialRel, OpAddRelation:
		return "card"
	case OpWriteNote, OpAddOpenQuestion:
		return "note"
	case OpAddSource:
		return "source"
	default:
		// M3 八个 op 全部以知识卡为对象（提案合同 §8.1），黑名单按 card.* 判。
		if IsM3Op(name) {
			return "card"
		}
		return "plan"
	}
}

// —— 只读类型断言助手：不做规范化、不改写任何字节 ——

func set(keys []string) map[string]bool {
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

func asString(v interface{}) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case nil:
		return "", false
	case bool:
		return fmt.Sprintf("%t", x), false
	case int:
		return fmt.Sprintf("%d", x), false
	case float64:
		return fmt.Sprintf("%v", x), false
	}
	return "", false
}

func asInt(v interface{}) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		if float64(int(x)) == x {
			return int(x), true
		}
	}
	return 0, false
}

func asBool(v interface{}) (bool, bool) {
	if b, ok := v.(bool); ok {
		return b, true
	}
	return false, false
}

func asMap(v interface{}) (map[string]interface{}, bool) {
	switch x := v.(type) {
	case map[string]interface{}:
		return x, true
	case map[interface{}]interface{}:
		out := map[string]interface{}{}
		for k, val := range x {
			ks, _ := asString(k)
			out[ks] = val
		}
		return out, true
	}
	return nil, false
}

func asList(v interface{}) ([]interface{}, bool) {
	if l, ok := v.([]interface{}); ok {
		return l, true
	}
	return nil, false
}

func listOf(v interface{}) []interface{} {
	l, _ := asList(v)
	return l
}

func asStringSlice(v interface{}) []string {
	var out []string
	for _, item := range listOf(v) {
		s, _ := asString(item)
		out = append(out, s)
	}
	return out
}
