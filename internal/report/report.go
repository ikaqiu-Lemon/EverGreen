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
//   - 非 S1 阶段字段只输出占位形态，**不得输出假数据**：proposals[] 与
//     deprecated_new_support[] 为空数组，support_check[] 与 affected 为空值，
//     reconcile 为 {"ran": false}，txn_id **省略**（结构体里根本没有这个字段）。
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
func ForbiddenKeys() []string {
	return []string{"commit", "written", "errors", "timestamps", "txn_id", "reviews"}
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

// Reconcile 是 S3 才计算的对账段落，S1 固定输出 {"ran": false}（不得输出假数据）。
type Reconcile struct {
	Ran bool `json:"ran"`
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

	// —— 非 S1 阶段字段：只输出占位形态，绝不输出假数据（§4.6）——
	Proposals            []interface{} `json:"proposals"`
	DeprecatedNewSupport []interface{} `json:"deprecated_new_support"`
	SupportCheck         interface{}   `json:"support_check"`
	Affected             interface{}   `json:"affected"`
	Reconcile            Reconcile     `json:"reconcile"`
	// txn_id 属 S5：**省略**（这里没有该字段，序列化结果里也不会出现）。
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
		Proposals:            []interface{}{},
		DeprecatedNewSupport: []interface{}{},
		SupportCheck:         nil,
		Affected:             nil,
		Reconcile:            Reconcile{Ran: false},
	}
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
// 措辞里**不出现**「待确认」「待批准」这类中间态（EG-CFM-01）。
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

func idList(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "（" + strings.Join(ids, "、") + "）"
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
}
