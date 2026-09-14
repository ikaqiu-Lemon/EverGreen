// M4 · R5 手工跨领域移动检测（`domain_moved` / W18）的**端到端驱动**
// （T-evergreen.s1_main_flow-158614-054）。
//
// 存在的理由与 `m4_r1_takeover_test.go` / `m4_r3_relation_test.go` 同源：R5 已落地在
// `internal/reconcile`，但 `eg check` / `eg reconcile` **命令本体属 T-…-059 / T-…-058**，
// 本 task 明确不注册命令。于是 e2e 无法用 `eg` 子命令驱动这条链路 —— 照既有先例，
// 本文件充当**唯一的驱动壳**：在脚本给定的真实临时 vault 上跑
// 「M2 扫描底座 → `internal/git` 的只读 rename 采样 → reconcile.Run」，
// 把事实写成紧凑 JSON 供 `test/e2e/m4_r5_domain_moved.sh` 逐条断言。
//
// 四条纪律：
//   - **不新增产品能力、不复制平行实现**：finding 全部由 reconcile 包产出，扫描全部由
//     query.VaultScan 产出，rename 事实全部由 git.Repo.FollowRenames（`git log --follow
//     --name-status` 的只读用法）产出；本文件只做「取数 + 落 JSON」；
//   - **零写入**：除了把事实 JSON 写到脚本的 mktemp 目录（**不在 vault 内**），
//     本文件对 vault 一个字节都不写；R5 本身不产 RepairSpec，此处也不合成任何修复，
//     更不搬动文件、不补关系；
//   - 未设 EG_M4_R5_VAULT 时**直接 skip**，因此 `go test ./...` 的行为一字不变；
//   - R1 的 Git 状态与 R2 的编辑事实**均不采样**（Input.Status / Input.Edits 保持零值），
//     `sources/` 分区同样不采样（Input.Sources 恒 nil）：本驱动壳只驱动 R5，
//     那些事实面分属 T-…-050 / T-…-051 / T-…-052 自己的 e2e。于是脚本可对
//     finding 总数做等号断言，也能反证「R5 不替 R1–R4 记账」。
package e2e

import (
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// 环境变量（全部由 e2e 脚本设置；缺 vault 即 skip）。
const (
	envR5Vault = "EG_M4_R5_VAULT" // 真实临时 vault 根（vault 即 git 仓库根）
	envR5Out   = "EG_M4_R5_OUT"   // JSON 事实落盘路径（脚本的 mktemp 目录内）
)

// r5Facts 是驱动壳回报的全部事实（JSON 键固定，供脚本 grep / sed 逐条复算）。
type r5Facts struct {
	// Findings 是 reconcile.Run 的原样产出（四键 schema，不加工不裁剪）。
	Findings []reconcile.Finding `json:"findings"`
	// Codes 是逐条 finding 的诊断码（顺序与 findings 一致）。
	Codes []string `json:"codes"`
	// Repairs 是修复意向：R5 是只报告项，此处恒空。
	Repairs []reconcile.RepairSpec `json:"repairs"`
	// DomainMoved 是 W18 的条数（脚本据此断言「恰 1 条」/「零命中」）。
	DomainMoved int `json:"domain_moved"`
	// 其余六项的条数（本驱动壳只驱动 R5，它们恒 0 —— 不重复计数的反证面）。
	GitUncommitted    int `json:"git_uncommitted"`
	ReviewedAtMissing int `json:"reviewed_at_missing"`
	DuplicateID       int `json:"duplicate_id"`
	DanglingRef       int `json:"dangling_ref"`
	Orphan            int `json:"orphan"`
	RelationFindings  int `json:"relation_findings"`
	// HasError 是 error 级 finding 是否存在 —— `eg check` 退 2 的唯一判据来源（A-31）。
	HasError bool `json:"has_error"`
	// ExitCode 是**按 A-31 语义换算**的退出码（W18 是 warning，因此单有 W18 时恒 0）。
	// 命令本体属 T-…-059：这里只回报换算结果，不代替命令注册。
	ExitCode int `json:"exit_code"`
	// Moves 是逐条移动事实 `<ID>|<旧领域>|<新领域>|<证据…>`（DomainMoves 的顺序原样保留）。
	Moves []string `json:"moves"`
	// TargetsArity 是逐条 W18 的 targets 元数（恒 3 —— 三元顺序固定的正面反证之一）。
	TargetsArity []int `json:"targets_arity"`
	// OrderedNotSorted 报告是否**至少有一条** W18 的固定三元 ≠ 字典序升序：
	// 它为 true 才证明这三元真的走了有序例外，而不是「碰巧排好了」。
	OrderedNotSorted bool `json:"ordered_not_sorted"`
	// 取数事实：卡 / 笔记条数、扫描与跳过文件数、查询域诊断条数。
	Cards            int `json:"cards"`
	Notes            int `json:"notes"`
	ScannedFiles     int `json:"scanned_files"`
	SkippedFiles     int `json:"skipped_files"`
	QueryDiagnostics int `json:"query_diagnostics"`
	// RenamesSampled 是采到的 rename 记录条数；CrossDomainRenames 是其中跨领域的条数。
	RenamesSampled     int `json:"renames_sampled"`
	CrossDomainRenames int `json:"cross_domain_renames"`
	// RenameFacts 是采到的 rename 事实 `<旧路径>|<新路径>|<verb>`（升序，可逐字复算）。
	RenameFacts []string `json:"rename_facts"`
	// RelationEntries 是全库 relations[] 条目总数（R5 不补关系的正面反证之一）。
	RelationEntries int `json:"relation_entries"`
}

// TestM4R5DomainMovedHarness 在脚本给定的真实 vault 上跑 R5 只读检查。
func TestM4R5DomainMovedHarness(t *testing.T) {
	vault := os.Getenv(envR5Vault)
	if vault == "" {
		t.Skip("未设 " + envR5Vault + "：本文件只在 e2e 脚本驱动下运行（go test ./... 行为不变）")
	}
	// ① 取数：M2 的全量扫描底座（全库，含笔记）——本 task 不另写扫描器。
	scan, err := query.VaultScan(vault, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("扫描 vault 失败：%v", err)
	}

	// ② 采样 Git 历史 rename 事实：唯一的子进程发生在 internal/git（只读 `git log`）。
	//    对账包因此拿到的是结构化事实，而不是一个能跑任意 git 命令的句柄。
	repo := git.New(vault)
	paths := make([]string, 0, len(scan.Cards)+len(scan.Notes))
	for _, c := range scan.Cards {
		paths = append(paths, c.Path)
	}
	for _, n := range scan.Notes {
		paths = append(paths, n.Path)
	}
	sort.Strings(paths)
	seen := map[string]bool{}
	var renames []reconcile.RenameFact
	facts := r5Facts{}
	for _, p := range paths {
		recs, err := repo.FollowRenames(p)
		if err != nil {
			t.Fatalf("采样 rename 事实失败（%s）：%v", p, err)
		}
		for _, r := range recs {
			key := r.OldPath + "|" + r.NewPath + "|" + r.Verb
			if seen[key] {
				continue
			}
			seen[key] = true
			renames = append(renames, reconcile.RenameFact{
				OldPath: r.OldPath, NewPath: r.NewPath, Verb: r.Verb,
			})
			facts.RenameFacts = append(facts.RenameFacts, key)
			from, okFrom := reconcile.DomainOfPath(r.OldPath)
			to, okTo := reconcile.DomainOfPath(r.NewPath)
			if okFrom && okTo && from != to {
				facts.CrossDomainRenames++
			}
		}
	}
	sort.Strings(facts.RenameFacts)
	facts.RenamesSampled = len(renames)

	// ③ 只读检查：finding 全部由 reconcile 包产出（本文件不自造 finding）。
	in := reconcile.R5ScanOf(vault, scan, renames)
	res := reconcile.Run(in)
	facts.Findings, facts.Repairs = res.Findings, res.Repairs
	facts.HasError = res.HasError()
	facts.Cards, facts.Notes = len(scan.Cards), len(scan.Notes)
	facts.ScannedFiles, facts.SkippedFiles = scan.ScannedFiles, scan.SkippedFiles
	facts.QueryDiagnostics = len(scan.Diagnostics)
	if facts.HasError {
		facts.ExitCode = 2 // A-31：有 error 级 finding 即退 2
	}
	for _, c := range scan.Cards {
		facts.RelationEntries += len(c.Relations)
	}
	for _, f := range res.Findings {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		facts.Codes = append(facts.Codes, f.Code())
		switch f.Check {
		case reconcile.CheckDomainMoved:
			facts.DomainMoved++
			facts.TargetsArity = append(facts.TargetsArity, len(f.Targets))
			if !sameStrings(reconcile.NormalizeTargets(f.Targets), f.Targets) {
				facts.OrderedNotSorted = true
			}
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
		case reconcile.CheckRelationTargetMissing, reconcile.CheckRelationPrefixInvalid,
			reconcile.CheckRelationOpposingAsymmetric, reconcile.CheckRelationDuplicate:
			facts.RelationFindings++
		}
	}
	// 移动事实与 finding 同源（DomainMoves 与检查项走同一个判定函数）。
	for _, m := range reconcile.DomainMoves(in) {
		row := m.ID + "|" + m.OldDomain + "|" + m.NewDomain
		for _, e := range m.Evidence {
			row += "|" + e
		}
		facts.Moves = append(facts.Moves, row)
	}
	if len(facts.Moves) != facts.DomainMoved {
		t.Fatalf("DomainMoves（%d 条）与 W18 finding（%d 条）不同源",
			len(facts.Moves), facts.DomainMoved)
	}
	if len(res.Repairs) != 0 {
		t.Fatalf("R5 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条", len(res.Repairs))
	}

	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv(envR5Out); out != "" {
		if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("写事实文件失败：%v", err)
		}
	}
	t.Logf("R5 事实：%s", raw)
}

// sameStrings 比较两个字符串切片是否逐字相同（不引 reflect 的小工具）。
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
