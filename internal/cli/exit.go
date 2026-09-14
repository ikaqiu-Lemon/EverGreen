package cli

// 退出码集中映射 + `--json` / 人类可读双渲染（合同 §3 / §4 / §5）。
//
// 硬约束：**业务层只返回带类型的错误，退出码只在本文件翻译一次**。
// 各命令实现禁止调用 os.Exit（唯一允许处是 cmd/eg/main.go），
// 也禁止自行拼装退出码——否则 §4 的五值封闭集合守不住。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// 退出码：M6 收口后全集恰 `{0,1,2,3,4,5,6}`（7 值，合同 §9.1）。
// 5 属写前强校验失败 / 锁不可用（常量见 exitcode.go，自 M6 起**已启用**）；
// 6 属 S2，自 S2/M3 起已启用（仅缺用户确认）。本文件的五个常量值与语义不因此改变，
// 5 与 6 都由各自的类型化错误在 ExitCodeFor 里单点翻译，未分类错误仍按 1 兜底（不得兜底到 5）。
const (
	// ExitOK 成功。
	ExitOK = 0
	// ExitUsage 用法 / 参数非法：零写入。
	ExitUsage = 1
	// ExitValidation 校验失败（仅 error 级）：零写入。
	ExitValidation = 2
	// ExitPartialWrite 部分写入被跳过：已完成写入保留并提交。
	ExitPartialWrite = 3
	// ExitCommitFailed Git 提交失败：磁盘保留当前状态，不做破坏性还原（B4）。
	ExitCommitFailed = 4
)

// 信封 status 取值（由 exit_code 唯一派生，合同 §3）。
const (
	StatusCompleted = "completed"
	StatusPartial   = "partial"
	StatusFailed    = "failed"
)

// Diagnostic 是 error / warning / info 诊断条目（合同 §5）。
// 每条都带 op 下标 + 字段路径，Agent 拿到即可定位并重投。
type Diagnostic struct {
	Code    string `json:"code"`
	Level   string `json:"level"`
	Path    string `json:"path"`
	OpIndex int    `json:"op_index"`
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

// 诊断级别。
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelInfo    = "info"
)

// NonOpDiagnostic 是非 op 级诊断的 op 下标占位值（合同 §5）。
const NonOpDiagnostic = -1

// —— 四类带类型错误：业务层只返回这些，退出码由 ExitCodeFor 统一翻译 ——

// UsageError → 退出码 1（用法 / 参数非法，零写入）。
type UsageError struct {
	Msg   string
	Diags []Diagnostic
}

func (e *UsageError) Error() string { return e.Msg }

// Diagnostics 实现 diagnoser。
func (e *UsageError) Diagnostics() []Diagnostic { return e.Diags }

// ValidationError → 退出码 2（校验失败，仅 error 级，零写入）。
type ValidationError struct {
	Msg   string
	Diags []Diagnostic
}

func (e *ValidationError) Error() string { return e.Msg }

// Diagnostics 实现 diagnoser。
func (e *ValidationError) Diagnostics() []Diagnostic { return e.Diags }

// PartialWriteError → 退出码 3（部分写入被跳过；已写入部分保留并提交，链路继续）。
type PartialWriteError struct {
	Msg   string
	Diags []Diagnostic
}

func (e *PartialWriteError) Error() string { return e.Msg }

// Diagnostics 实现 diagnoser。
func (e *PartialWriteError) Diagnostics() []Diagnostic { return e.Diags }

// CommitFailedError → 退出码 4（Git 提交失败；磁盘保留现状，B4 禁止破坏性还原）。
type CommitFailedError struct {
	Msg   string
	Diags []Diagnostic
	Err   error
}

func (e *CommitFailedError) Error() string {
	if e.Err == nil {
		return e.Msg
	}
	return e.Msg + "：" + e.Err.Error()
}

// Unwrap 暴露底层 Git 错误。
func (e *CommitFailedError) Unwrap() error { return e.Err }

// Diagnostics 实现 diagnoser。
func (e *CommitFailedError) Diagnostics() []Diagnostic { return e.Diags }

// NotWiredError 是**骨架阶段专用**错误：命令已按合同注册，但业务实现尚未挂载
// （由 T-…-008 / 009 / 010 / 015 / 016 分别 Wire 进来）。
// 映射到退出码 1 并明确说明「实现未挂载」，绝不静默成功、绝不 panic、零写入。
type NotWiredError struct {
	Command string
	Owner   string // 承担实现的 task 简写
}

func (e *NotWiredError) Error() string {
	return fmt.Sprintf("eg %s 的业务实现尚未挂载（骨架阶段；实现见 %s）", e.Command, e.Owner)
}

type diagnoser interface{ Diagnostics() []Diagnostic }

// ExitCodeFor 是**唯一**的退出码翻译点。
//
// 未分类错误不得凭空造出第六个码（5 属 S5、6 属 S2），统一按 1 处理并在文案里标注
// 「未分类错误」——这是实现 bug 的信号，不是新的对外语义。
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var (
		usage    *UsageError
		notWired *NotWiredError
		invalid  *ValidationError
		partial  *PartialWriteError
		commit   *CommitFailedError
		needCfm  *NeedConfirmError
		// precheck / lockBusy 是 M6 退出码 5 的两类成因（合同 §17.1）：分别携 E15 / E16。
		// 它们各映射为退出码 5，是本函数**仅有**的两条 5 号分支，位置在 needCfm 之后、usage
		// 之前。ExitCodeFor 仍是唯一退出码翻译点，5 不进未分类兜底。
		precheck *PrecheckFailedError
		lockBusy *LockUnavailableError
		// pageParam 是 S4 的分页参数错。I-…-008 裁决后它映射为 **1**（只读命令闭集
		// 恰 {0,1}，M2 §1.6 / §6）；`4` 仍专表 Git 提交失败，见 page.go 文件头。
		pageParam *PageParamError
		// bench 是 S4 的性能采样执行失败（M5 合同 §8.1 给 eg bench 的退出码集合恰 {0,1,4}）。
		// 同样**不新增**退出码，只是给 4 增加一类合同已指派的成因，见 bench.go 文件头。
		benchFail *BenchError
	)
	switch {
	// 「仅缺用户确认」优先于其余分支翻译，但退出码本身仍由 NeedConfirmError.ExitCode()
	// 决定（白名单外退化为 1）：白名单的守法责任留在 exitcode.go 一处，这里不复制判据。
	case errors.As(err, &needCfm):
		return needCfm.ExitCode()
	// 退出码 5 恰两条分支（写前强校验失败 E15 / 锁不可用 E16），单点映射，别处不得再翻译成 5。
	case errors.As(err, &precheck):
		return ExitPrecheckOrLock
	case errors.As(err, &lockBusy):
		return ExitPrecheckOrLock
	case errors.As(err, &usage):
		return ExitUsage
	case errors.As(err, &notWired):
		return ExitUsage
	case errors.As(err, &invalid):
		return ExitValidation
	case errors.As(err, &partial):
		return ExitPartialWrite
	case errors.As(err, &commit):
		return ExitCommitFailed
	case errors.As(err, &pageParam):
		return ExitPageParam
	case errors.As(err, &benchFail):
		return ExitCommitFailed
	}
	return ExitUsage
}

// StatusFor 由退出码唯一派生信封 status（合同 §3 派生表）。
func StatusFor(code int) string {
	switch code {
	case ExitOK:
		return StatusCompleted
	case ExitPartialWrite, ExitCommitFailed:
		return StatusPartial
	default:
		return StatusFailed
	}
}

// Result 是命令的结果结构体：**人类可读与 --json 两套渲染的唯一数据源**。
type Result struct {
	// Data 是该命令的 data 载荷（合同 §1 逐命令定义）；nil 渲染成 {}。
	Data map[string]interface{}
	// DataOrder 声明 Data 键的输出次序：M2 查询合同把 `search` / `card show` / `rel` 的
	// data 键序定死为「键序即表次序」（§1.2 / §2.2 / §3.1），因此渲染不能退化成字典序。
	// 留空即沿用 M1 口径（按键名字典序），未列出的键按字典序追加在已声明键之后。
	DataOrder []string
	// Warnings 是 warning / info 级诊断。
	Warnings []Diagnostic
	// Errors 是 error 级诊断，渲染进 data.errors[]（合同 §3）。
	Errors []Diagnostic
	// Summary 是人类可读摘要行（事实必须能从 Data / Warnings 复述，不得引入新事实）。
	Summary []string
}

// Envelope 是 --json 输出信封，键**恰五项**（合同 §3；字段只增不改）。
type Envelope struct {
	OK       bool                   `json:"ok"`
	Data     map[string]interface{} `json:"data"`
	Warnings []Diagnostic           `json:"warnings"`
	ExitCode int                    `json:"exit_code"`
	Status   string                 `json:"status"`

	// dataOrder 是 data 键的声明次序（不导出、不参与 JSON 键集合：信封仍是五键）。
	dataOrder []string
}

// EnvelopeKeys 是信封键的封闭集合（供测试与下游断言）。
func EnvelopeKeys() []string {
	return []string{"ok", "data", "warnings", "exit_code", "status"}
}

// NewEnvelope 把 Result + 退出码归一成信封。两套渲染都只吃它，保证事实同源。
func NewEnvelope(res *Result, code int) Envelope {
	env := Envelope{
		OK:       code == ExitOK,
		Data:     map[string]interface{}{},
		Warnings: []Diagnostic{},
		ExitCode: code,
		Status:   StatusFor(code),
	}
	if res == nil {
		return env
	}
	env.dataOrder = res.DataOrder
	for k, v := range res.Data {
		env.Data[k] = v
	}
	if len(res.Warnings) > 0 {
		env.Warnings = append(env.Warnings, res.Warnings...)
	}
	// I-…-016：两个桶按 `level` 严格分流 —— 合同 §3 逐字规定信封 `warnings[]` 装
	// 「**warning / info 级**诊断条目」、`data.errors[]` 装「**error 级**条目」。
	//
	// 为什么把分流放在这里（而不是各命令自己分好）：`Result.Errors` 是「本次失败随手带出的
	// 诊断序列」，写路径会把过程留痕（如锁等待的 `W28`）与终态（如 `E16`）按发生次序append 进
	// 同一条切片 —— 让每个产错点都记得分桶是不可维护的（实测 15 条命令全都漏了）。信封归一是
	// **唯一漏斗**：在这里分一次，就对「任一命令 × 任一退出码」一致生效，也不需要调用方反过来
	// 按 level 二次过滤（那个动作没有任何合同依据）。
	//
	// 失败 ≠ 没有 warning：退 3 / 4 / 5 的路径同样可能带 warning / info，分流与退出码无关。
	// 次序在各桶内**逐字保留**（`W28` 的重试序列仍按真实发生次序），只是换了桶。
	if len(res.Errors) > 0 {
		errorsOnly := make([]Diagnostic, 0, len(res.Errors))
		for _, d := range res.Errors {
			if d.Level == LevelError {
				errorsOnly = append(errorsOnly, d)
				continue
			}
			env.Warnings = append(env.Warnings, d)
		}
		// 全是 warning / info 时不建 `errors` 键：`data.errors` 在场就意味着「有 error 级条目」。
		if len(errorsOnly) > 0 {
			env.Data["errors"] = errorsOnly
		}
	}
	return env
}

// MarshalJSON 输出信封五键（次序 = EnvelopeKeys()），并按 dataOrder 输出 data 的键序。
//
// 只做「键序」这一件事：键集合、键名、取值与 encoding/json 的默认结果逐字相同，
// 信封仍是五键、data 仍是各命令合同定义的那几个键（字段只增不改）。
func (e Envelope) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString("{")
	for i, k := range EnvelopeKeys() {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "%q:", k)
		var (
			raw []byte
			err error
		)
		switch k {
		case "ok":
			raw, err = marshalJSONValue(e.OK)
		case "data":
			raw, err = marshalOrderedMap(e.Data, e.dataOrder)
		case "warnings":
			raw, err = marshalJSONValue(e.Warnings)
		case "exit_code":
			raw, err = marshalJSONValue(e.ExitCode)
		case "status":
			raw, err = marshalJSONValue(e.Status)
		}
		if err != nil {
			return nil, err
		}
		b.Write(raw)
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

// marshalOrderedMap 按 order 给出的键序序列化 map；order 未覆盖的键按字典序追加。
func marshalOrderedMap(data map[string]interface{}, order []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString("{")
	first := true
	emit := func(k string) error {
		v, ok := data[k]
		if !ok {
			return nil
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "%q:", k)
		raw, err := marshalJSONValue(v)
		if err != nil {
			return err
		}
		b.Write(raw)
		return nil
	}
	declared := map[string]bool{}
	for _, k := range order {
		if declared[k] {
			continue
		}
		declared[k] = true
		if err := emit(k); err != nil {
			return nil, err
		}
	}
	for _, k := range sortedDataKeys(data) {
		if declared[k] {
			continue
		}
		if err := emit(k); err != nil {
			return nil, err
		}
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

// marshalJSONValue 编码单个值：与 RenderJSON 同款设置（不转义 HTML、无尾换行）。
func marshalJSONValue(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// RenderJSON 输出信封（稳定顺序由结构体字段序保证；末尾带换行）。
func RenderJSON(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(env)
}

// RenderHuman 输出人类可读文本。**只从信封派生**，因此与 --json 事实一致：
// 同一 Result 的两种渲染里，status / exit_code / 每个 data 键值 / 每条诊断都能互相对上。
func RenderHuman(w io.Writer, env Envelope, summary []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "状态：%s（exit_code=%d，ok=%t）\n", env.Status, env.ExitCode, env.OK)
	for _, line := range summary {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if len(env.Data) > 0 {
		fmt.Fprintf(&b, "数据：\n")
		for _, k := range envelopeDataKeys(env) {
			fmt.Fprintf(&b, "  %s: %s\n", k, scalarString(env.Data[k]))
		}
	}
	if len(env.Warnings) > 0 {
		fmt.Fprintf(&b, "警告（%d 条）：\n", len(env.Warnings))
		for _, d := range env.Warnings {
			fmt.Fprintf(&b, "  %s\n", diagLine(d))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// envelopeDataKeys 返回 data 键的渲染次序：声明序优先（M2 合同「键序即表次序」），
// 未声明的键按字典序追加。人类可读与 --json 用同一函数取序，两套渲染因此逐键对得上。
func envelopeDataKeys(env Envelope) []string {
	out := make([]string, 0, len(env.Data))
	seen := map[string]bool{}
	for _, k := range env.dataOrder {
		if seen[k] {
			continue
		}
		seen[k] = true
		if _, ok := env.Data[k]; ok {
			out = append(out, k)
		}
	}
	for _, k := range sortedDataKeys(env.Data) {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}

func sortedDataKeys(data map[string]interface{}) []string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scalarString 把 data 值渲染成单行文本。复合值用 JSON 编码（JSON 是 CLI 输出格式，
// 与「写路径禁止 YAML 序列化」的硬约束无关：这里只写终端，不写产物文件）。
func scalarString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", x)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(raw)
}

func diagLine(d Diagnostic) string {
	var b strings.Builder
	if d.Code != "" {
		fmt.Fprintf(&b, "[%s] ", d.Code)
	}
	fmt.Fprintf(&b, "%s", d.Level)
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
