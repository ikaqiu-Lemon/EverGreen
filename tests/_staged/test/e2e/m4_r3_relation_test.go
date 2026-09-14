// M4 · R3 关系校验四子检查的**端到端驱动**（T-evergreen.s1_main_flow-158614-053）。
//
// 存在的理由与 `m4_r1_takeover_test.go` / `m4_r4_structure_test.go` 同源：R3 的四项只读检查
// 已落地在 `internal/reconcile`，但 `eg check` / `eg reconcile` **命令本体属 T-…-059 / T-…-058**，
// 本 task 明确不注册命令。于是 e2e 无法用 `eg` 子命令驱动这条链路 —— 照既有先例，
// 本文件充当**唯一的驱动壳**：在脚本给定的真实临时 vault 上跑「M2 扫描底座 → reconcile.Run」，
// 把事实写成紧凑 JSON 供 `test/e2e/m4_r3_relation.sh` 逐条断言。
//
// 四条纪律：
//   - **不新增产品能力、不复制平行实现**：finding 全部由 reconcile 包产出，扫描全部由
//     query.VaultScan 产出；本文件只做「取数 + 落 JSON」；
//   - **零写入**：除了把事实 JSON 写到脚本的 mktemp 目录（**不在 vault 内**），
//     本文件对 vault 一个字节都不写；R3 本身不产 RepairSpec，此处也不合成任何修复；
//   - 未设 EG_M4_R3_VAULT 时**直接 skip**，因此 `go test ./...` 的行为一字不变；
//   - R1 的 Git 状态与 R2 的编辑事实**均不采样**（Input.Status / Input.Edits 保持零值）：
//     本驱动壳只驱动 R3，那两个事实面分属 T-…-050 / T-…-051 自己的 e2e。
//     `sources/` 分区同样不采样（R3 只看知识卡的 relations[]），因此 R4 的两条源侧判定
//     整体跳过 —— 干净库的 finding 总数因此恒等于 R3 自己的产出，脚本可做等号断言。
package e2e

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// 环境变量（全部由 e2e 脚本设置；缺 vault 即 skip）。
const (
	envR3Vault = "EG_M4_R3_VAULT" // 真实临时 vault 根
	envR3Out   = "EG_M4_R3_OUT"   // JSON 事实落盘路径（脚本的 mktemp 目录内）
)

// r3Facts 是驱动壳回报的全部事实（JSON 键固定，供脚本 grep / sed 逐条复算）。
type r3Facts struct {
	// Findings 是 reconcile.Run 的原样产出（四键 schema，不加工不裁剪）。
	Findings []reconcile.Finding `json:"findings"`
	// Codes 是逐条 finding 的诊断码（顺序与 findings 一致）。
	Codes []string `json:"codes"`
	// Repairs 是修复意向：R3 是只报告项，此处恒空。
	Repairs []reconcile.RepairSpec `json:"repairs"`
	// 四项子检查的条数（脚本据此断言「各恰 1 条」）。
	RelationTargetMissing      int `json:"relation_target_missing"`
	RelationPrefixInvalid      int `json:"relation_prefix_invalid"`
	RelationOpposingAsymmetric int `json:"relation_opposing_asymmetric"`
	RelationDuplicate          int `json:"relation_duplicate"`
	// R4 三项的条数（本驱动壳不采样 sources/，此处用于反证「R3 不与 R4 重复计数」）。
	DuplicateID int `json:"duplicate_id"`
	DanglingRef int `json:"dangling_ref"`
	Orphan      int `json:"orphan"`
	// HasError 是 error 级 finding 是否存在 —— `eg check` 退 2 的唯一判据来源（A-31）。
	HasError bool `json:"has_error"`
	// ExitCode 是**按 A-31 语义换算**的退出码（2 = 存在 error 级 finding）。
	// 命令本体属 T-…-059：这里只回报换算结果，不代替命令注册。
	ExitCode int `json:"exit_code"`
	// 取数事实：扫描到的卡条数、关系条目总数、扫描 / 跳过文件数。
	Cards           int `json:"cards"`
	RelationEntries int `json:"relation_entries"`
	ScannedFiles    int `json:"scanned_files"`
	SkippedFiles    int `json:"skipped_files"`
	// QueryDiagnostics 是查询域 Q 系列诊断条数（对账域不消费它，只如实回报）。
	QueryDiagnostics int `json:"query_diagnostics"`
	// RelationsByCard 是逐卡的 relations[] 条目数（`<卡 ID>=<条数>`，升序）——
	// 脚本据此断言「检查前后条目数完全一致」（零自动修的正面反证之一）。
	RelationsByCard []string `json:"relations_by_card"`
}

// TestM4R3RelationHarness 在脚本给定的真实 vault 上跑 R3 四项只读检查。
func TestM4R3RelationHarness(t *testing.T) {
	vault := os.Getenv(envR3Vault)
	if vault == "" {
		t.Skip("未设 " + envR3Vault + "：本文件只在 e2e 脚本驱动下运行（go test ./... 行为不变）")
	}
	// ① 取数：M2 的全量扫描底座（全库，含笔记）——本 task 不另写扫描器。
	//    全库口径是硬要求：受限扫描面会把「没扫到」误判成「target 不存在」。
	scan, err := query.VaultScan(vault, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("扫描 vault 失败：%v", err)
	}

	// ② 只读检查：finding 全部由 reconcile 包产出（本文件不自造 finding）。
	res := reconcile.Run(reconcile.R3ScanOf(vault, scan))
	facts := r3Facts{
		Findings: res.Findings, Repairs: res.Repairs,
		HasError:     res.HasError(),
		Cards:        len(scan.Cards),
		ScannedFiles: scan.ScannedFiles, SkippedFiles: scan.SkippedFiles,
		QueryDiagnostics: len(scan.Diagnostics),
	}
	if facts.HasError {
		facts.ExitCode = 2 // A-31：有 error 级 finding 即退 2（复用 S1「校验失败、零写入」语义）
	}
	for _, c := range scan.Cards {
		facts.RelationEntries += len(c.Relations)
		facts.RelationsByCard = append(facts.RelationsByCard,
			c.ID+"="+itoa(len(c.Relations)))
	}
	sort.Strings(facts.RelationsByCard)
	for _, f := range res.Findings {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		facts.Codes = append(facts.Codes, f.Code())
		switch f.Check {
		case reconcile.CheckRelationTargetMissing:
			facts.RelationTargetMissing++
		case reconcile.CheckRelationPrefixInvalid:
			facts.RelationPrefixInvalid++
		case reconcile.CheckRelationOpposingAsymmetric:
			facts.RelationOpposingAsymmetric++
		case reconcile.CheckRelationDuplicate:
			facts.RelationDuplicate++
		case reconcile.CheckDuplicateID:
			facts.DuplicateID++
		case reconcile.CheckDanglingRef:
			facts.DanglingRef++
		case reconcile.CheckOrphan:
			facts.Orphan++
		}
	}
	if len(res.Repairs) != 0 {
		t.Fatalf("R3 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条", len(res.Repairs))
	}

	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv(envR3Out); out != "" {
		if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("写事实文件失败：%v", err)
		}
	}
	t.Logf("R3 事实：%s", raw)
}

// itoa 是不引 strconv 的小整数转串（条目数恒是小非负整数）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
