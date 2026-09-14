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

// PlanVersion 是 S1 唯一支持的 plan 版本。
const PlanVersion = 1

// TopLevelKeys 是顶层键的封闭集合（恰 8 个，合同 §1）。
func TopLevelKeys() []string {
	return []string{"plan_version", "verb", "domain", "reason",
		"requirement_ids", "convergence", "base", "ops"}
}

// S1 七个 op 名（封闭集合，合同 §3）。
const (
	OpAddSource       = "add_source"
	OpWriteNote       = "write_note"
	OpCreateCard      = "create_card"
	OpAppendCard      = "append_card"
	OpAddMaterialRel  = "add_material_rel"
	OpAddRelation     = "add_relation"
	OpAddOpenQuestion = "add_open_question"
)

// OpNames 是 S1 支持的 op 名（恰七个，声明顺序即合同 §3 顺序）。
func OpNames() []string {
	return []string{OpAddSource, OpWriteNote, OpCreateCard, OpAppendCard,
		OpAddMaterialRel, OpAddRelation, OpAddOpenQuestion}
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
		return []string{"op", "source", "note_id", "title", "domain", "tags",
			"sections", "output_cards", "coverage_gaps", "reprocess"}
	case OpCreateCard:
		return []string{"op", "title", "card_id", "domain", "tags", "sources", "sections"}
	case OpAppendCard:
		return []string{"op", "card", "sections"}
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
	case OpCreateCard, OpAppendCard, OpAddMaterialRel, OpAddRelation:
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
