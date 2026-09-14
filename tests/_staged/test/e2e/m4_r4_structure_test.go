// M4 · R4 结构检查的**端到端驱动**（T-evergreen.s1_main_flow-158614-052）。
//
// 存在的理由与 `m4_r1_takeover_test.go` 同源：R4 的三项只读检查已落地在
// `internal/reconcile`，但 `eg check` / `eg reconcile` **命令本体属 T-…-059 / T-…-058**，
// 本 task 明确不注册命令。于是 e2e 无法用 `eg` 子命令驱动这条链路 —— 照既有先例，
// 本文件充当**唯一的驱动壳**：在脚本给定的真实临时 vault 上跑「M2 扫描底座 + `sources/`
// 分区采样 → reconcile.Run」，把事实写成紧凑 JSON 供 `test/e2e/m4_r4_structure.sh`
// 逐条断言。
//
// 四条纪律：
//   - **不新增产品能力、不复制平行实现**：finding 全部由 reconcile 包产出，扫描全部由
//     query.VaultScan 产出；本文件只做「取数 + 落 JSON」；
//   - `sources/` 分区的采样是**只读**的：os.ReadDir + os.ReadFile + mdfile.ParseSource，
//     零写入（写 JSON 事实文件落在脚本的 mktemp 目录内，不在 vault 内）；
//   - 未设 EG_M4_R4_VAULT 时**直接 skip**，因此 `go test ./...` 的行为一字不变；
//   - R1 的 Git 状态**不采样**（Input.Status 保持零值）：本驱动壳只驱动 R4，
//     「工作区未提交改动」是 T-…-050 的事实面，由 `m4_r1_takeover.sh` 自己钉。
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// 环境变量（全部由 e2e 脚本设置；缺 vault 即 skip）。
const (
	envR4Vault = "EG_M4_R4_VAULT" // 真实临时 vault 根
	envR4Out   = "EG_M4_R4_OUT"   // JSON 事实落盘路径（脚本的 mktemp 目录内）
)

// r4Facts 是驱动壳回报的全部事实（JSON 键固定，供脚本 grep / sed 逐条复算）。
type r4Facts struct {
	// Findings 是 reconcile.Run 的原样产出（四键 schema，不加工不裁剪）。
	Findings []reconcile.Finding `json:"findings"`
	// Codes 是逐条 finding 的诊断码（顺序与 findings 一致）。
	Codes []string `json:"codes"`
	// Repairs 是修复意向：R4 是只报告项，此处恒空。
	Repairs []reconcile.RepairSpec `json:"repairs"`
	// 三项 check 的条数（脚本据此断言「各恰 1 条」）。
	DuplicateID int `json:"duplicate_id"`
	DanglingRef int `json:"dangling_ref"`
	Orphan      int `json:"orphan"`
	// OrphanSubtypes 是命中的孤儿子类型（升序去重，取自 detail 内的封闭三值）。
	OrphanSubtypes []string `json:"orphan_subtypes"`
	// HasError 是 error 级 finding 是否存在 —— `eg check` 退 2 的唯一判据来源（A-31）。
	HasError bool `json:"has_error"`
	// ExitCode 是**按 A-31 语义换算**的退出码（2 = 存在 error 级 finding）。
	// 命令本体属 T-…-059：这里只回报换算结果，不代替命令注册。
	ExitCode int `json:"exit_code"`
	// 取数事实：扫描到的卡 / 笔记 / 原文条数与跳过数（供脚本核对造数是否落地）。
	Cards        int `json:"cards"`
	Notes        int `json:"notes"`
	Sources      int `json:"sources"`
	ScannedFiles int `json:"scanned_files"`
	SkippedFiles int `json:"skipped_files"`
	// QueryDiagnostics 是查询域 Q 系列诊断条数（对账域不消费它，只如实回报以便核对零双计数）。
	QueryDiagnostics int `json:"query_diagnostics"`
}

// TestM4R4StructureHarness 在脚本给定的真实 vault 上跑 R4 三项只读检查。
func TestM4R4StructureHarness(t *testing.T) {
	vault := os.Getenv(envR4Vault)
	if vault == "" {
		t.Skip("未设 " + envR4Vault + "：本文件只在 e2e 脚本驱动下运行（go test ./... 行为不变）")
	}
	// ① 取数：M2 的全量扫描底座（全库，含笔记）——本 task 不另写扫描器。
	scan, err := query.VaultScan(vault, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("扫描 vault 失败：%v", err)
	}
	sources, err := sampleSources(vault)
	if err != nil {
		t.Fatalf("采样 sources/ 分区失败：%v", err)
	}

	// ② 只读检查：finding 全部由 reconcile 包产出（本文件不自造 finding）。
	res := reconcile.Run(reconcile.R4ScanOf(vault, scan, sources))
	facts := r4Facts{
		Findings: res.Findings, Repairs: res.Repairs,
		HasError: res.HasError(),
		Cards:    len(scan.Cards), Notes: len(scan.Notes), Sources: len(sources),
		ScannedFiles: scan.ScannedFiles, SkippedFiles: scan.SkippedFiles,
		QueryDiagnostics: len(scan.Diagnostics),
	}
	if facts.HasError {
		facts.ExitCode = 2 // A-31：有 error 级 finding 即退 2（复用 S1「校验失败、零写入」语义）
	}
	seen := map[string]bool{}
	for _, f := range res.Findings {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		facts.Codes = append(facts.Codes, f.Code())
		switch f.Check {
		case reconcile.CheckDuplicateID:
			facts.DuplicateID++
		case reconcile.CheckDanglingRef:
			facts.DanglingRef++
		case reconcile.CheckOrphan:
			facts.Orphan++
			for _, s := range reconcile.OrphanSubtypes() {
				if strings.Contains(f.Detail, s) {
					seen[s] = true
				}
			}
		}
	}
	for _, s := range reconcile.OrphanSubtypes() {
		if seen[s] {
			facts.OrphanSubtypes = append(facts.OrphanSubtypes, s)
		}
	}
	sort.Strings(facts.OrphanSubtypes)
	if len(res.Repairs) != 0 {
		t.Fatalf("R4 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条", len(res.Repairs))
	}

	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv(envR4Out); out != "" {
		if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("写事实文件失败：%v", err)
		}
	}
	t.Logf("R4 事实：%s", raw)
}

// sampleSources 只读采样 `sources/` 分区：每份可解析的原文回报 (ID, vault 相对路径)。
//
// 不可解析的文件**不静默吞掉**、也不冒充成一条原文：跳过即视为「该文件不是可定位的原文」，
// 与查询侧「记 Q1 并继续」的诚实性口径同源（此处的 Q1 由 query 扫描面自己产出，
// 驱动壳不重复造诊断）。目录不存在时返回空集合（vault 尚未收录任何原文）。
func sampleSources(vault string) ([]reconcile.SourceFact, error) {
	dir := filepath.Join(vault, "sources")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []reconcile.SourceFact{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]reconcile.SourceFact, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		_, src, err := mdfile.ParseSource(raw)
		if err != nil || string(src.ID) == "" {
			continue // 不可解析 / 缺 id：查询域已记 Q1，此处不冒充成一条原文事实
		}
		out = append(out, reconcile.SourceFact{
			ID: string(src.ID), Path: path2rel("sources", e.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// path2rel 拼 vault 相对路径（/ 分隔，与 query 扫描面的 Path 口径一致）。
func path2rel(dir, name string) string { return dir + "/" + name }
