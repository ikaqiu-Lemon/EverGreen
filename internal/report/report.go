package report

// 最终报告的 S1 必填子集与双形态渲染（T-evergreen.s1_main_flow-158614-016）。
//
// 报告是**唯一**让用户知道「这次到底发生了什么」的东西。底线不是好看，而是**如实**：
// 写了什么、跳过了什么、为什么跳过，一条都不折叠、不去重丢失、不编造。
//
// 键集合是**封闭**的（技术方案 §4.6）：
//   - S1 必填恰 11 项：source / note / cards / relations / open_questions / links /
//     git.commit（**嵌套在 git 下，不是顶层 commit**）/ skipped[] / default_domain_fallback /
//     high_impact[] / warnings[]；
//   - 阶段字段一律**不得输出假数据**：`proposals[]` 与 `deprecated_new_support[]` 自 M3 起
//     产出**真实值**（无事实时仍是空数组），`support_check[]` 与 `affected` 仍为空值、
//     `reconcile` 自 M4 起是**恰三键**对象（schema 见 reconcile.go；非对账命令路径下
//     恒为「键在值空」的占位形态），`txn_id` 自 M6（T-…-072）起按 A-59 生效：**凡成功分配了
//     事务号的最终报告一律必填**（取值 = `.index/txn/<txn_id>` 目录名），未开事务的路径
//     （只读 / 校验失败 / --dry-run / accepted write-set 为空 / 分配前即失败）**省略**该键。
//
// 报告体内**不得自创键**（顶层 commit / written / errors / timestamps 一律不允许）。
// `ok` / `data` / `warnings` / `exit_code` / `status` 是 CLI **信封**字段，由 internal/cli
// 渲染，不下沉进报告体。
//
// 报告**不是知识产物**：它不写进 vault 的 domains/** 或 sources/**（EG-AGT-04）。
// 报告只陈述既成事实，**不请求确认**——S1 无审批闸门、卡创建即 active、无中间态（EG-CFM-01）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// RequiredKeys 是 §4.6「S1 必填」行的键名清单（**恰 11 项**，顺序即字段声明顺序）。
func RequiredKeys() []string {
	return []string{
		"source", "note", "cards", "relations", "open_questions", "links",
		"git", "skipped", "default_domain_fallback", "high_impact", "warnings",
	}
}

// StageKeys 是非 S1 阶段字段的键名清单（只输出占位形态）。
func StageKeys() []string {
	return []string{"proposals", "deprecated_new_support", "support_check", "affected", "reconcile"}
}

// ForbiddenKeys 是**禁止出现**在报告体里的自创键（供单测反证）。
//
// 【T-…-072 收紧】`txn_id` 自 M6 起是**条件合法键**（事务写时按 A-59 填充、其余路径省略），
// 因此从禁列里移出——它不再是「自创 / 越阶段」键，而是 S5 事务写路径的合同字段。
func ForbiddenKeys() []string {
	return []string{"commit", "written", "errors", "timestamps", "reviews"}
}

// NoCardNotice 是「零知识结果合法」（EG-KNW-05）的固定说明文案。
const NoCardNotice = "本次未产生知识卡"

// CodeI1 是 §4.5.1 的 info 编号（覆盖项缺失标注、工作区既有改动说明都走它）。
const CodeI1 = "I1"

// 诊断级别（与 CLI 合同 §5 同构）。
const (
	LevelWarning = "warning"
	LevelInfo    = "info"
)

// NonOp 是非 op 级诊断的 op 下标占位值（合同 §5）。
const NonOp = -1

// Diagnostic 是一条 W / I 条目（合同 §5：必带 op 下标 + 字段路径）。
type Diagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	OpIndex int    `json:"op_index"`
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

// Source 是本次涉及的原文（§4.6：id / path）。
type Source struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Note 是本次涉及的材料笔记（§4.6：id / domain / path / reprocessed）。
type Note struct {
	ID          string `json:"id"`
	Domain      string `json:"domain"`
	Path        string `json:"path"`
	Reprocessed bool   `json:"reprocessed"`
}

// Cards 是知识卡的三类事实（§4.6：created / reused / updated）。
type Cards struct {
	Created []string `json:"created"`
	Reused  []string `json:"reused"`
	Updated []string `json:"updated"`
}

// Material 是一条材料关系（四要素）。
type Material struct {
	Card   string `json:"card"`
	Source string `json:"source"`
	Note   string `json:"note"`
	Rel    string `json:"rel"`
	Reason string `json:"reason"`
}

// Knowledge 是一条论证关系（方向已规范化）。
type Knowledge struct {
	From   string `json:"from"`
	Type   string `json:"type"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// Relations 是两类关系（§4.6：material[] / knowledge[]）。
type Relations struct {
	Material  []Material  `json:"material"`
	Knowledge []Knowledge `json:"knowledge"`
}

// OpenQuestion 是一条未决问题。
type OpenQuestion struct {
	Note     string `json:"note"`
	Question string `json:"question"`
}

// Git 承载 §4.6 的 `git.commit`：**嵌套键**，提交失败或未产生 commit 时为 null。
// 失败原因与未提交清单走 warnings[]（§4.6 明文允许的 W / I 容器），不新增报告体键。
type Git struct {
	Commit *string `json:"commit"`
}

// Skipped 是一条跳过（合同 §8 的封闭命名：kind 恰两值，cause 一一对应）。
type Skipped struct {
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Locator string `json:"locator"`
	Cause   string `json:"cause"`
	Detail  string `json:"detail"`
}

// Fallback 是领域缺省落位说明（§4.6：used / reason，EG-DOM-03）。
type Fallback struct {
	Used   bool   `json:"used"`
	Reason string `json:"reason"`
}

// Impact 是一条显著变更（§4.6 high_impact[]）。
type Impact struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

// ExecutionFailed 是提案执行三态里「失败」那一个取值。
//
// 报告不得依赖 S2 的 internal/proposal（依赖方向：report 是 S1 的下游叶子包），
// 因此这里持有**同一份**字面量，由 internal/cli 的等号断言把两侧锁死
// （与 store / query 各自持有 "proposals" 目录名的处理方式同一套办法）。
const ExecutionFailed = "failed"

// ProposalExecution 是提案 `execution` 事实在报告里的投影：键名与提案 frontmatter **逐字同名**。
//
// written_paths / unwritten_paths 是**逐路径**清单（不是计数），且 `execution=failed` 时
// 两键必须都在（可为空数组，不得是 null）：报告要让用户直接看出「哪些写了、哪些没写」。
// 两个清单由**唯一账本**产生（internal/proposal 的 PathLedger），报告只搬运、不重新数。
type ProposalExecution struct {
	Status         string   `json:"status"`
	AttemptedAt    string   `json:"attempted_at"`
	Reason         string   `json:"reason"`
	GitCommit      *string  `json:"git_commit"`
	WrittenPaths   []string `json:"written_paths"`
	UnwrittenPaths []string `json:"unwritten_paths"`
}

// ProposalEntry 是 `proposals[]` 的一条**真实值**（M3 起首次产出真实值）。
type ProposalEntry struct {
	ID        string            `json:"id"`
	Path      string            `json:"path"`
	Status    string            `json:"status"`
	Targets   []string          `json:"targets"`
	Execution ProposalExecution `json:"execution"`
}

// DeprecatedSupport 是 `deprecated_new_support[]` 的一条**真实值**：
// 已 deprecated 的卡又出现了新的支持材料（四要素 + 说明）。
type DeprecatedSupport struct {
	Card   string `json:"card"`
	Source string `json:"source"`
	Note   string `json:"note"`
	Rel    string `json:"rel"`
	Detail string `json:"detail"`
}

// Report 是最终报告体。字段顺序即 JSON 键顺序（结构体序列化天然稳定，
// 因此 `eg apply` 与 `eg report --last` 的输出可逐字比对）。
type Report struct {
	Source                Source         `json:"source"`
	Note                  Note           `json:"note"`
	Cards                 Cards          `json:"cards"`
	Relations             Relations      `json:"relations"`
	OpenQuestions         []OpenQuestion `json:"open_questions"`
	Links                 []string       `json:"links"`
	Git                   Git            `json:"git"`
	Skipped               []Skipped      `json:"skipped"`
	DefaultDomainFallback Fallback       `json:"default_domain_fallback"`
	HighImpact            []Impact       `json:"high_impact"`
	Warnings              []Diagnostic   `json:"warnings"`

	// —— 阶段字段：M3 起 proposals[] / deprecated_new_support[] 产出**真实值**，
	// M4 起 reconcile 在对账命令路径上产出**真实值**（非对账路径恒是占位形态）；
	// support_check / affected 仍是占位形态，绝不输出假数据（§4.6 / §8.3）——
	Proposals            []ProposalEntry     `json:"proposals"`
	DeprecatedNewSupport []DeprecatedSupport `json:"deprecated_new_support"`
	SupportCheck         interface{}         `json:"support_check"`
	Affected             interface{}         `json:"affected"`
	Reconcile            Reconcile           `json:"reconcile"`

	// TxnID 是 M6 事务化写路径的事务标识（合同 §12 / A-59），取值恒等于
	// `.index/txn/<txn_id>` 日志目录名 —— 有事务就必须交代是哪一个，
	// 否则崩溃恢复与人工对账都无从下手。
	//
	// **审计边界是「号码分配成功」，不是「事务提交成功」。** 事务号一旦分配，日志目录就已
	// 在盘上占位、能被 Scan / Recover 读到，用户必须能从报告顺藤摸瓜找到它。因此下列四种
	// 结局**全部必填**：
	//   - 分配后 intent 发布失败（事务已开、屏障未发，零权威写）；
	//   - 提交期普通 I/O 失败、事务主动回滚 + abort（此时本键恰是 abort 标记所在目录名）；
	//   - Markdown 提交成功但 Git 失败；
	//   - 全链路成功。
	//
	// 反过来，**号码根本没分配出来**的路径一律省略本键：只读命令、校验失败、`--dry-run`、
	// accepted write-set 为空（无写可提交、不造空事务）、以及分配之前就失败的阻断
	// （锁不可用、崩溃恢复 fail closed、预演放弃）。这些路径盘上不存在对应事务目录，
	// 填一个 txn_id 就是编造事实。因此用 `omitempty`：非事务写的报告键面与 M5 逐字不变。
	TxnID string `json:"txn_id,omitempty"`
}

// New 返回一份空报告：所有列表都是空数组（不是 null），阶段占位字段已就位。
func New() Report {
	return Report{
		Cards:                Cards{Created: []string{}, Reused: []string{}, Updated: []string{}},
		Relations:            Relations{Material: []Material{}, Knowledge: []Knowledge{}},
		OpenQuestions:        []OpenQuestion{},
		Links:                []string{},
		Skipped:              []Skipped{},
		HighImpact:           []Impact{},
		Warnings:             []Diagnostic{},
		Proposals:            []ProposalEntry{},
		DeprecatedNewSupport: []DeprecatedSupport{},
		SupportCheck:         nil,
		Affected:             nil,
		Reconcile:            newReconcilePlaceholder(),
	}
}

// AddProposal 追加一条提案事实（`proposals[]`）。**不折叠、不去重**：数量守恒。
//
// 两个路径清单一律用空数组兜底（`execution=failed` 时两键必须存在，不得是 null）；
// 报告只搬运调用方给的清单，**不在这里重新收集或重新计数**（唯一账本在 internal/proposal）。
func (r *Report) AddProposal(p ProposalEntry) {
	if p.Targets == nil {
		p.Targets = []string{}
	}
	if p.Execution.WrittenPaths == nil {
		p.Execution.WrittenPaths = []string{}
	}
	if p.Execution.UnwrittenPaths == nil {
		p.Execution.UnwrittenPaths = []string{}
	}
	r.Proposals = append(r.Proposals, p)
}

// AddDeprecatedNewSupport 追加一条「失效卡出现新支持材料」的事实（`deprecated_new_support[]`）。
func (r *Report) AddDeprecatedNewSupport(d DeprecatedSupport) {
	r.DeprecatedNewSupport = append(r.DeprecatedNewSupport, d)
}

// SetCommit 记录本次 commit 的 sha；空串表示「未产生 commit / 提交失败」→ 输出 null。
func (r *Report) SetCommit(sha string) {
	if sha == "" {
		r.Git.Commit = nil
		return
	}
	value := sha
	r.Git.Commit = &value
}

// SetTxnID 记录本次事务写的事务标识（= `.index/txn/<txn_id>` 目录名）。空串保持省略。
// 只由 CLI 的事务编排单点在 **AllocateTxnID 成功之后立即**调用（合同 A-59）：号码在盘
// 即入报告，此后 intent 失败 / 主动回滚 / Git 失败 / 提交成功都不再撤销这一格。
func (r *Report) SetTxnID(id string) { r.TxnID = id }

// AddWarning 追加一条 W / I 条目。**不折叠、不去重**：数量守恒是如实原则的代码化。
func (r *Report) AddWarning(d Diagnostic) { r.Warnings = append(r.Warnings, d) }

// AddInfo 追加一条 I1 info 条目（覆盖项缺失、工作区既有改动、零卡说明都走它）。
func (r *Report) AddInfo(path string, opIndex int, format string, args ...interface{}) {
	r.AddWarning(Diagnostic{
		Code: CodeI1, Level: LevelInfo, Path: path, OpIndex: opIndex,
		Message: fmt.Sprintf(format, args...),
	})
}

// NoteZeroCards 在本次没有产生任何知识卡时追加固定说明（EG-KNW-05：零知识结果合法，
// 不判为失败）。已有卡时不输出，避免造出与事实相悖的说明。
func (r *Report) NoteZeroCards(path string) bool {
	if len(r.Cards.Created) > 0 {
		return false
	}
	r.AddInfo(path, NonOp, "%s：本次只产出材料层内容或关系，属合法结果，未判失败", NoCardNotice)
	return true
}

// JSON 序列化报告体（键顺序 = 字段声明顺序；不转义 HTML，保证与 CLI 信封同一口径）。
func (r Report) JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Lines 渲染人类可读报告：事实与 JSON 同源，只换表达形式，不引入 JSON 里没有的事实。
// 措辞里**不出现**任何审批 / 中间态字样（EG-CFM-01：S1 无闸门，卡创建即 active）。
func (r Report) Lines() []string {
	var out []string
	if r.Source.ID != "" {
		out = append(out, fmt.Sprintf("原文：%s（%s）", r.Source.ID, r.Source.Path))
	}
	if r.Note.ID != "" {
		out = append(out, fmt.Sprintf("材料笔记：%s（领域 %s，%s，重新加工 %t）",
			r.Note.ID, r.Note.Domain, r.Note.Path, r.Note.Reprocessed))
	}
	out = append(out, fmt.Sprintf("知识卡：新建 %d 张%s，复用 %d 张%s，补充 %d 张%s",
		len(r.Cards.Created), idList(r.Cards.Created),
		len(r.Cards.Reused), idList(r.Cards.Reused),
		len(r.Cards.Updated), idList(r.Cards.Updated)))
	out = append(out, fmt.Sprintf("关系：材料 %d 条，论证 %d 条",
		len(r.Relations.Material), len(r.Relations.Knowledge)))
	for _, q := range r.OpenQuestions {
		out = append(out, fmt.Sprintf("未决问题：%s ← %s", q.Note, oneLine(q.Question)))
	}
	out = append(out, fmt.Sprintf("写入文件 %d 个：%s", len(r.Links), strings.Join(r.Links, "、")))
	if r.Git.Commit == nil {
		out = append(out, "commit：无（未产生 commit 或提交失败；磁盘保留当前状态，未做任何还原）")
	} else {
		out = append(out, "commit："+*r.Git.Commit)
	}
	for _, s := range r.Skipped {
		out = append(out, fmt.Sprintf("跳过：%s（kind=%s，cause=%s，%s）",
			s.Locator, s.Kind, s.Cause, s.Detail))
	}
	if r.DefaultDomainFallback.Used {
		out = append(out, "领域缺省落位："+r.DefaultDomainFallback.Reason)
	}
	out = append(out, proposalLines(r.Proposals)...)
	for _, d := range r.DeprecatedNewSupport {
		out = append(out, fmt.Sprintf("失效卡出现新支持材料：%s ← %s / %s（%s）%s",
			d.Card, d.Source, d.Note, d.Rel, d.Detail))
	}
	for _, h := range r.HighImpact {
		out = append(out, fmt.Sprintf("显著变更：%s（%s）%s", h.Kind, h.Target, h.Detail))
	}
	for _, d := range r.Warnings {
		out = append(out, "· "+DiagLine(d))
	}
	return out
}

// DiagLine 渲染单条诊断（带编号 / 级别 / op 下标 / 字段路径）。
func DiagLine(d Diagnostic) string {
	var b strings.Builder
	if d.Code != "" {
		fmt.Fprintf(&b, "[%s] ", d.Code)
	}
	b.WriteString(d.Level)
	if d.OpIndex >= 0 {
		fmt.Fprintf(&b, " ops[%d]", d.OpIndex)
	}
	if d.Path != "" {
		fmt.Fprintf(&b, " %s", d.Path)
	}
	fmt.Fprintf(&b, "：%s", d.Message)
	if d.Target != "" {
		fmt.Fprintf(&b, "（target=%s）", d.Target)
	}
	return b.String()
}

// proposalLines 渲染 `proposals[]`：`execution=failed` 时**逐路径**列出已写 / 未写
// （一条都不折叠、不省略），并复述 B4 口径 —— 已写入的内容留在磁盘，未做任何还原。
func proposalLines(list []ProposalEntry) []string {
	var out []string
	for _, p := range list {
		out = append(out, fmt.Sprintf("提案 %s（%s）：status=%s，execution=%s",
			p.ID, p.Path, p.Status, p.Execution.Status))
		out = append(out, fmt.Sprintf("  影响文件 %d 个：已写 %d 个、未写 %d 个",
			len(p.Execution.WrittenPaths)+len(p.Execution.UnwrittenPaths),
			len(p.Execution.WrittenPaths), len(p.Execution.UnwrittenPaths)))
		for _, w := range p.Execution.WrittenPaths {
			out = append(out, "  已写："+w)
		}
		for _, u := range p.Execution.UnwrittenPaths {
			out = append(out, "  未写："+u)
		}
		if p.Execution.Reason != "" {
			out = append(out, "  原因："+oneLine(p.Execution.Reason))
		}
		if p.Execution.Status == ExecutionFailed {
			out = append(out, "  已写入的内容保留在磁盘，未写入的路径逐条在册，未做任何还原（B4）；"+
				"可用 git status / git diff 自行核对")
		}
	}
	return out
}

func idList(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "（" + strings.Join(ids, "、") + "）"
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
}
