package cli

// eg report --last 的业务实现（合同 §1.9；技术方案 §4.6、§7.1）。
//
// **只读**：零文件变化、零 commit。它把最近一次**写命令**（`apply` / `capture` 等，
// 合同 §1.9）的报告**原样复现**——从落盘记录里读出报告体字节直接回放，**不重新计算**、
// 不重新扫描 vault，因此与该次命令的 `--json` 报告体逐字一致（键顺序同源于 report.Report
// 的字段序）。记录里同时存着产出它的命令表面，回放文案据此如实交代来源（I-…-018）。
//
// EG-CFM-01：报告只陈述既成事实，**不请求确认**——S1 无审批闸门、卡创建即 active、
// 无中间态，因此输出里也不会出现任何审批 / 中间态字样。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
)

// lastReportRecord 是 `.eg/last-report.json` 的落盘形态。
//
// Data 的值用 json.RawMessage 承载：写入与回放都是**同一串字节**，
// 保证 `eg report --last` 与该次写命令的报告体逐字相同。
//
// Command 是产出这条记录的**命令表面**（`eg apply` / `eg capture` / `eg delete`…，
// I-…-018）。它存在的唯一理由是「报告只陈述既成事实」：回放时必须说得出复现的是哪条
// 命令，不能把每条记录都写成 apply。老记录（M1~M5 期）没有这一键 → 反序列化得空串，
// 回放文案退回到不指名的说法，**不猜**。
type lastReportRecord struct {
	ExitCode int                        `json:"exit_code"`
	Status   string                     `json:"status"`
	Command  string                     `json:"command,omitempty"`
	Data     map[string]json.RawMessage `json:"data"`
	Warnings []Diagnostic               `json:"warnings"`
}

// writeLastReport 落盘最近一次报告（先建目录再原子替换；报告不是知识产物，不走 store 写口）。
//
// data 是**报告体容器**，与 res.Data 分开传：`eg capture` 的信封 data 是收录事实的封闭键集
// （§1.3），报告体（§4.6）只进记录、不进信封，因此两者不是同一张表（I-…-018）。
func writeLastReport(root, command string, res *Result, data map[string]interface{}, code int) error {
	rec := lastReportRecord{
		ExitCode: code,
		Status:   StatusFor(code),
		Command:  command,
		Data:     map[string]json.RawMessage{},
		Warnings: res.Warnings,
	}
	if rec.Warnings == nil {
		rec.Warnings = []Diagnostic{}
	}
	for k, v := range data {
		raw, err := marshalStable(v)
		if err != nil {
			return err
		}
		rec.Data[k] = raw
	}
	payload, err := marshalStable(rec)
	if err != nil {
		return err
	}
	abs := stateAbs(root, LastReportFile)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, append(payload, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, abs)
}

// marshalStable 是本包唯一的 JSON 序列化口径：紧凑、不转义 HTML，与信封渲染一致。
// （JSON 是 CLI 的输出格式与状态记录格式，与「写路径禁止 YAML 序列化」的硬约束无关：
// vault 内的 Markdown 产物一律走 internal/store 的字节区间写口。）
func marshalStable(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// runReport 实现 eg report --last。
func (r *Root) runReport(inv *Invocation) (*Result, error) {
	abs := stateAbs(inv.VaultRoot, LastReportFile)
	raw, err := os.ReadFile(abs)
	if err != nil {
		// I-…-018：提示**不预设**用户上一步做了什么。声明面覆盖的是「最近一次写命令」
		// （`apply` / `capture` 等，见合同 §1.9），只把人往 apply 上引会把刚成功
		// capture 完的用户指向一个他没有理由执行的动作。
		return nil, &UsageError{Msg: fmt.Sprintf(
			"无历史报告：%s 不存在（本 vault 还没有产出过报告的写命令；"+
				"先执行一次 eg capture 或 eg apply 等写命令即可）", LastReportFile)}
	}
	var rec lastReportRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, &UsageError{Msg: fmt.Sprintf("%s 不可解析：%v", LastReportFile, err)}
	}

	data := make(map[string]interface{}, len(rec.Data))
	for k, v := range rec.Data {
		data[k] = v
	}
	res := &Result{Data: data, Warnings: rec.Warnings}
	res.Warnings = append(res.Warnings, Diagnostic{
		Level: LevelInfo, Path: LastReportFile, OpIndex: NonOpDiagnostic,
		Message: fmt.Sprintf(
			"本报告复现自最近一次 %s（该次 exit_code=%d、status=%s），内容原样回放、未重新计算",
			lastReportSource(rec.Command), rec.ExitCode, rec.Status),
	})
	res.Summary = append(res.Summary, fmt.Sprintf(
		"最近一次 %s：exit_code=%d，status=%s（只读复现，零写入零 commit）",
		lastReportSource(rec.Command), rec.ExitCode, rec.Status))
	res.Summary = append(res.Summary, reportLines(rec.Data["report"])...)
	// 逐卡收敛结论与 `eg apply` 共用 convergence.go 的 convergenceLines：
	// 同一份落盘字节 → 同一个渲染函数 → 两条路径的收敛行块逐字相等
	// （渲染字面量只允许出现在 convergence.go，故此处不复述该前缀）。
	// 记录里没有该字段（M1 期旧记录）只陈述事实，照常退 0、不补算。
	res.Summary = append(res.Summary, convergenceReplayLines(rec.Data["convergence"])...)
	return res, nil
}

// lastReportSource 如实交代「这条记录是谁产出的」。
//
// 记录里没有 `command`（M1~M5 期的老记录）时**不猜**：只说它来自一条写命令，
// 并点明该记录未登记命令表面 —— 报告宁可少说一句，也不许编造来源（I-…-018）。
func lastReportSource(command string) string {
	if strings.TrimSpace(command) == "" {
		return "写命令（该记录未登记命令表面）"
	}
	return command
}

// reportLines 把落盘的报告体还原成人类可读行；不可解析时只说明事实，不编造内容。
func reportLines(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{"报告体缺失：该记录未包含 report 字段"}
	}
	var rep report.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return []string{"报告体不可解析：" + err.Error()}
	}
	return rep.Lines()
}
