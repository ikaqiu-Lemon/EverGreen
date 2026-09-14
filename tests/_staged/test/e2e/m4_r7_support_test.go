// M4 · R7 材料支撑不足实时判定（`support_insufficient` / W20）的**端到端驱动**
// （T-evergreen.s1_main_flow-158614-056）。
//
// 存在的理由与 `m4_r4_structure_test.go` / `m4_r5_domain_moved_test.go` 同源：R7 已落地在
// `internal/reconcile`，但 `eg check` / `eg reconcile` **命令本体属 T-…-059 / T-…-058**，
// 本 task 明确不注册命令。于是 e2e 无法用 `eg` 子命令驱动这条链路 —— 照既有先例，
// 本文件充当**唯一的驱动壳**：在脚本给定的真实临时 vault 上跑「M2 扫描底座 +
// `sources/` 分区只读采样 → reconcile.Run → 命令层渲染函数」，把事实写成紧凑 JSON
// 供 `test/e2e/m4_r7_support.sh` 逐条断言。
//
// 五条纪律：
//   - **不新增产品能力、不复制平行实现**：finding 全部由 reconcile 包产出，扫描全部由
//     query.VaultScan 产出，`sources/` 分区采样复用本包既有的 sampleSources（T-…-052 建的，
//     不另写第二份），人类可读提示全部由命令层的渲染函数产出；本文件只做「取数 + 落 JSON」；
//   - **零写入**：除了把事实 JSON 写到脚本的 mktemp 目录（**不在 vault 内**），
//     本文件对 vault 一个字节都不写；R7 本身不产 RepairSpec，此处也不合成任何修复，
//     更不写任何标记、不动任何状态；
//   - **状态维度只读回报**：卡的 `status` 逐字回报进事实 JSON，供脚本比对「检查前后一字不变」；
//   - 未设 EG_M4_R7_VAULT 时**直接 skip**，因此 `go test ./...` 的行为一字不变；
//   - R1 的 Git 状态与 R2 的编辑事实、R5 的 rename 事实**均不采样**（Input.Status / Edits /
//     Renames 保持零值）：本驱动壳只驱动 R7，那些事实面分属各自 task 的 e2e。于是脚本可对
//     finding 总数做等号断言，也能反证「R7 不替 R1–R5 记账」。
package e2e

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// 环境变量（全部由 e2e 脚本设置；缺 vault 即 skip）。
const (
	envR7Vault = "EG_M4_R7_VAULT" // 真实临时 vault 根
	envR7Out   = "EG_M4_R7_OUT"   // JSON 事实落盘路径（脚本的 mktemp 目录内）
)

// r7Facts 是驱动壳回报的全部事实（JSON 键固定，供脚本 grep / sed 逐条复算）。
type r7Facts struct {
	// Findings 是 reconcile.Run 的原样产出（四键 schema，不加工不裁剪）。
	Findings []reconcile.Finding `json:"findings"`
	// Codes 是逐条 finding 的诊断码（顺序与 findings 一致）。
	Codes []string `json:"codes"`
	// Repairs 是修复意向：R7 是只报告项，此处恒空（合同 §1.3「R7 无人写」）。
	Repairs []reconcile.RepairSpec `json:"repairs"`
	// SupportInsufficient 是 W20 的条数（脚本据此断言「恰 2 条」）。
	SupportInsufficient int `json:"support_insufficient"`
	// 其余七项的条数（本驱动壳只驱动 R7，它们恒 0 —— 不重复计数的反证面）。
	GitUncommitted    int `json:"git_uncommitted"`
	ReviewedAtMissing int `json:"reviewed_at_missing"`
	DuplicateID       int `json:"duplicate_id"`
	DanglingRef       int `json:"dangling_ref"`
	Orphan            int `json:"orphan"`
	RelationFindings  int `json:"relation_findings"`
	DomainMoved       int `json:"domain_moved"`
	// HasError 是 error 级 finding 是否存在 —— `eg check` 退 2 的唯一判据来源（A-31）。
	HasError bool `json:"has_error"`
	// ExitCode 是**按 A-31 语义换算**的退出码（W20 是 warning，因此单有 W20 时恒 0）。
	// 命令本体属 T-…-059：这里只回报换算结果，不代替命令注册。
	ExitCode int `json:"exit_code"`
	// Supports 是逐张卡的支持面事实 `<ID>|<声明条数>|<有效条数>|<无效原因…>`（按卡 ID 升序）。
	Supports []string `json:"supports"`
	// TargetsArity 是逐条 W20 的 targets 元数（恒 1 —— `[知识卡 ID]`）。
	TargetsArity []int `json:"targets_arity"`
	// Statuses 是逐张卡的状态维度事实 `<ID>|<status>|<deprecated>|<deleted>`（升序）：
	// 「永不改 status」的字节级反证面（脚本比对检查前后逐字相同）。
	Statuses []string `json:"statuses"`
	// HintLines 是命令层渲染函数产出的人类可读行（视图提示只在这里出现，只进 stdout）。
	HintLines []string `json:"hint_lines"`
	// HintSummary 是渲染层的一行摘要（同样只进 stdout）。
	HintSummary string `json:"hint_summary"`
	// HintCount 是带视图提示的 check 数（封闭表长度，恒 1）。
	HintCount int `json:"hint_count"`
	// 取数事实：卡 / 笔记 / 原文条数、扫描与跳过文件数、查询域诊断条数。
	Cards            int `json:"cards"`
	Notes            int `json:"notes"`
	Sources          int `json:"sources"`
	ScannedFiles     int `json:"scanned_files"`
	SkippedFiles     int `json:"skipped_files"`
	QueryDiagnostics int `json:"query_diagnostics"`
	// SupportEntries 是全库 `sources[]` 条目总数（R7 不改材料关系的正面反证之一）。
	SupportEntries int `json:"support_entries"`
}

// TestM4R7SupportHarness 在脚本给定的真实 vault 上跑 R7 只读检查。
func TestM4R7SupportHarness(t *testing.T) {
	vault := os.Getenv(envR7Vault)
	if vault == "" {
		t.Skip("未设 " + envR7Vault + "：本文件只在 e2e 脚本驱动下运行（go test ./... 行为不变）")
	}
	// ① 取数：M2 的全量扫描底座（全库，含笔记）——本 task 不另写扫描器。
	scan, err := query.VaultScan(vault, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("扫描 vault 失败：%v", err)
	}
	// ② `sources/` 分区只读采样：复用 T-…-052 建的 sampleSources（不写第二份实现）。
	sources, err := sampleSources(vault)
	if err != nil {
		t.Fatalf("采样 sources/ 分区失败：%v", err)
	}

	// ③ 只读检查：finding 全部由 reconcile 包产出（本文件不自造 finding）。
	in := reconcile.R7ScanOf(vault, scan, sources)
	res := reconcile.Run(in)
	facts := r7Facts{
		Findings: res.Findings, Repairs: res.Repairs, HasError: res.HasError(),
		Cards: len(scan.Cards), Notes: len(scan.Notes), Sources: len(sources),
		ScannedFiles: scan.ScannedFiles, SkippedFiles: scan.SkippedFiles,
		QueryDiagnostics: len(scan.Diagnostics), HintCount: cli.ReconcileHintCount,
	}
	if facts.HasError {
		facts.ExitCode = 2 // A-31：有 error 级 finding 即退 2
	}
	for _, c := range scan.Cards {
		facts.SupportEntries += len(c.Sources)
		facts.Statuses = append(facts.Statuses,
			c.ID+"|"+c.Status+"|"+boolText(c.Deprecated)+"|"+boolText(c.Deleted))
	}
	sort.Strings(facts.Statuses)
	for _, f := range res.Findings {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		facts.Codes = append(facts.Codes, f.Code())
		switch f.Check {
		case reconcile.CheckSupportInsufficient:
			facts.SupportInsufficient++
			facts.TargetsArity = append(facts.TargetsArity, len(f.Targets))
		case reconcile.CheckGitUncommitted:
			facts.GitUncommitted++
		case reconcile.CheckReviewedAtMissing:
			facts.ReviewedAtMissing++
		case reconcile.CheckDuplicateID:
			facts.DuplicateID++
		case reconcile.CheckDanglingRef:
			facts.DanglingRef++
		case reconcile.CheckOrphan:
			facts.Orphan++
		case reconcile.CheckDomainMoved:
			facts.DomainMoved++
		case reconcile.CheckRelationTargetMissing, reconcile.CheckRelationPrefixInvalid,
			reconcile.CheckRelationOpposingAsymmetric, reconcile.CheckRelationDuplicate:
			facts.RelationFindings++
		}
	}
	// ④ 支持面事实与 finding 同源（SupportFacts 与检查项走同一个判定函数）。
	hit := 0
	for _, f := range reconcile.SupportFacts(in) {
		row := f.ID + "|" + itoa(f.Declared) + "|" + itoa(f.Effective)
		for _, why := range f.Ineffective {
			row += "|" + why
		}
		facts.Supports = append(facts.Supports, row)
		if f.Insufficient() {
			hit++
		}
	}
	if hit < facts.SupportInsufficient {
		t.Fatalf("SupportFacts 命中数（%d）少于 W20 finding 数（%d）：两者必须同源",
			hit, facts.SupportInsufficient)
	}
	if len(res.Repairs) != 0 {
		t.Fatalf("R7 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条", len(res.Repairs))
	}
	// ⑤ 视图提示：只由命令层渲染函数产出（本文件不拼一遍提示文案）。
	facts.HintLines = cli.SupportInsufficientLines(res.Findings)
	facts.HintSummary = cli.ReconcileHintSummary(res.Findings)
	if len(facts.HintLines) != facts.SupportInsufficient {
		t.Fatalf("渲染行数（%d）与 W20 条数（%d）不一致",
			len(facts.HintLines), facts.SupportInsufficient)
	}
	for _, line := range facts.HintLines {
		if !strings.HasPrefix(line, cli.SupportInsufficientHint) {
			t.Fatalf("渲染行未以视图提示开头：%q", line)
		}
	}

	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv(envR7Out); out != "" {
		if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("写事实文件失败：%v", err)
		}
	}
	t.Logf("R7 事实：%s", raw)
}

// boolText 把布尔折成 true / false 文本（脚本按位比对用，不引 strconv 的小工具）。
// 十进制转换复用本包既有的 itoa（T-…-053 建的，不另写第二份）。
func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
