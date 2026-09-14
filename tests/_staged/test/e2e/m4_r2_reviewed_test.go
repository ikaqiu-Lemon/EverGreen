// M4 · R2 `reviewed_at` 补齐的**端到端驱动**（T-evergreen.s1_main_flow-158614-051）。
//
// 存在的理由与 `m4_r1_takeover_test.go` / `m4_r4_structure_test.go` 同源：R2 的三侧能力
// 都已落地（只读判定在 `internal/reconcile`、修复桥在 `internal/cli`、落盘在
// `internal/plan` → `internal/store`），但 `eg reconcile` **命令本体属 T-…-058**，本 task
// 明确不注册命令。于是 e2e 无法用 `eg` 子命令驱动整条链路 —— 照既有先例，本文件充当
// **唯一的驱动壳**：在脚本给定的真实临时 vault 上按模式跑
// 「扫描 + Git 只读采样 → reconcile.Run → R2 修复桥 → 恰一次纳管 commit」，
// 把事实写成紧凑 JSON 供 `test/e2e/m4_r2_reviewed_backfill.sh` 逐条断言。
//
// 五条纪律：
//   - **不新增产品能力、不复制平行实现**：finding 只由 reconcile 包产出，写入只由修复桥
//     经内存 ChangePlan 落盘，commit 只由 R1 的纳管写口产生（A-35：整轮恰一次）；
//   - 时刻由环境变量注入（默认固定值），因此补写进 frontmatter 的值可逐字断言；
//   - probe 模式恒零写入零提交；repair / stale 模式的提交条数恒 0 或 1；
//   - stale 模式刻意给一个**过期的观测 hash**，用来反证 B3 不豁免（跳过且零写入）；
//   - 未设 EG_M4_R2_VAULT 时**直接 skip**，因此 `go test ./...` 的行为一字不变。
package e2e

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// 环境变量（全部由 e2e 脚本设置；缺 vault 即 skip）。
const (
	envR2Vault  = "EG_M4_R2_VAULT"  // 真实临时 vault 根
	envR2Mode   = "EG_M4_R2_MODE"   // probe（只检查）/ repair（检查 + 修复 + 纳管）/ stale（B3）
	envR2Out    = "EG_M4_R2_OUT"    // JSON 事实落盘路径（脚本的 mktemp 目录内）
	envR2Domain = "EG_M4_R2_DOMAIN" // 提交主题的域位
	envR2Stamp  = "EG_M4_R2_STAMP"  // 本次对账时刻（RFC3339）
)

// r2StampDefault 是缺省对账时刻（脚本可覆盖；固定值使补写的键值可逐字断言）。
const r2StampDefault = "2026-11-24T10:00:00+08:00"

// r2StaleHash 是 stale 模式注入的**过期观测 hash**（B3 前置比对必然不通过）。
const r2StaleHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// r2Facts 是驱动壳回报的全部事实（JSON 键固定，供脚本 grep / sed 逐条复算）。
type r2Facts struct {
	Mode string `json:"mode"`
	Ran  bool   `json:"ran"`
	// Commit 是整轮对账那**唯一**一次提交的 SHA（空串 = 未提交）。
	Commit  string `json:"commit"`
	Commits int    `json:"commits"`
	// Findings / Codes / Repairs 是只读检查的原样产出（四键 schema，不加工不裁剪）。
	Findings []reconcile.Finding    `json:"findings"`
	Codes    []string               `json:"codes"`
	Repairs  []reconcile.RepairSpec `json:"repairs"`
	// ReviewedAtMissing 是 R2 的 finding 条数；Targets 是命中对象的路径（可复算序）。
	ReviewedAtMissing int      `json:"reviewed_at_missing"`
	Targets           []string `json:"targets"`
	TargetIDs         []string `json:"target_ids"`
	// 修复侧事实：op 条数、待写键集合、实际写入路径、补齐的对象 ID、跳过项。
	Ops         int                      `json:"ops"`
	Keys        []string                 `json:"keys"`
	Written     []string                 `json:"written"`
	Reviewed    []string                 `json:"reviewed"`
	Skipped     []cli.ReviewedRepairSkip `json:"skipped"`
	SkippedKind []string                 `json:"skipped_kinds"`
	Stamp       string                   `json:"stamp"`
	Warnings    []string                 `json:"warnings"`
	Failures    []string                 `json:"failures"`
	Pending     []string                 `json:"pending"`
	Existing    []string                 `json:"existing"`
	Files       []string                 `json:"files"`
	HasError    bool                     `json:"has_error"`
	ExitCode    int                      `json:"exit_code"`
}

// TestM4R2ReviewedHarness 在脚本给定的真实 vault 上跑 R2 的检查与修复。
func TestM4R2ReviewedHarness(t *testing.T) {
	vault := os.Getenv(envR2Vault)
	if vault == "" {
		t.Skip("未设 " + envR2Vault + "：本文件只在 e2e 脚本驱动下运行（go test ./... 行为不变）")
	}
	mode := os.Getenv(envR2Mode)
	if mode == "" {
		mode = "probe"
	}
	stampRaw := os.Getenv(envR2Stamp)
	if stampRaw == "" {
		stampRaw = r2StampDefault
	}
	at, err := time.Parse(time.RFC3339, stampRaw)
	if err != nil {
		t.Fatalf("对账时刻 %q 不是合法 RFC3339：%v", stampRaw, err)
	}

	// ① 取数：M2 的全量扫描底座 + Git 的只读状态采样（本文件不另写扫描器、不发写命令）。
	scan, serr := query.VaultScan(vault, query.ScanOptions{IncludeNotes: true})
	if serr != nil {
		t.Fatalf("扫描 vault 失败：%v", serr)
	}
	repo := git.New(vault)
	status, gerr := repo.Status()
	if gerr != nil {
		t.Fatalf("读 Git 状态失败：%v", gerr)
	}
	in := reconcile.R2ScanOf(vault, scan, status, nil)

	// ② 只读检查：finding / RepairSpec 全部由 reconcile 包产出。
	res := reconcile.Run(in)
	targets := reconcile.ReviewedTargets(in)
	facts := r2Facts{Mode: mode, Findings: res.Findings, Repairs: res.Repairs,
		Keys: reconcile.ReviewedKeys(), HasError: res.HasError()}
	if facts.HasError {
		facts.ExitCode = 2 // A-31：存在 error 级 finding → 退 2（命令本体属 T-…-058）
	}
	for _, f := range res.Findings {
		if verr := f.Validate(); verr != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", verr, f)
		}
		facts.Codes = append(facts.Codes, f.Code())
		if f.Check == reconcile.CheckReviewedAtMissing {
			facts.ReviewedAtMissing++
		}
	}
	for _, tg := range targets {
		facts.Targets = append(facts.Targets, tg.Path)
		facts.TargetIDs = append(facts.TargetIDs, tg.ID)
	}

	// ③ 修复 + 纳管（probe 模式一步都不做：只读检查恒零写入零提交）。
	if mode != "probe" {
		r := cli.New()
		r.Now = func() time.Time { return at }
		r.In = nil
		input := cli.ReviewedRepairInput{
			VaultRoot: vault, Targets: targets, Repairs: res.Repairs, Findings: res.Findings,
			UserRequest: true, Reason: "e2e：对账 R2 补齐外部编辑后的过目信号",
			RequirementIDs: []string{"EG-EDIT-05"},
		}
		if mode == "stale" {
			// B3 不豁免的反证：观测 hash 过期 → 写前比对不通过 → 跳过且零写入。
			input.Base = map[string]string{}
			for _, tg := range targets {
				input.Base[tg.ID] = r2StaleHash
			}
		}
		out, rerr := r.RepairReviewed(input)
		if rerr != nil {
			t.Fatalf("R2 修复失败：%v（%+v）", rerr, out)
		}
		facts.Ops, facts.Keys, facts.Stamp = out.Ops, out.Keys, out.Stamp
		facts.Written, facts.Reviewed = out.Written, out.Reviewed
		facts.Skipped = out.Skipped
		for _, s := range out.Skipped {
			facts.SkippedKind = append(facts.SkippedKind, s.Kind)
		}
		facts.Warnings, facts.Failures = out.Warnings, out.Failures
		facts.Findings = out.Findings // 跳过时带「已跳过」注记的同一批 finding
		if out.Commits != 0 {
			t.Fatalf("修复桥的提交条数 = %d，恒应为 0（A-35：提交归纳管写口）", out.Commits)
		}

		// 整轮对账的**恰一次** commit：R1 的外部编辑与 R2 的补写同进这一条。
		tk := cli.NewReconcileTakeover(repo)
		tout, terr := tk.Takeover(cli.ReconcileTakeoverInput{
			Domain:   os.Getenv(envR2Domain),
			Findings: len(res.Findings), Repairs: len(res.Repairs),
			OurWrites: out.Written, ChecksDone: true, RepairsDone: true,
			Reason:         "e2e：对账（R1 纳管 + R2 补齐，恰一次 commit）",
			RequirementIDs: []string{"EG-EDIT-05"},
		})
		if terr != nil {
			t.Fatalf("纳管失败：%v", terr)
		}
		if n := tk.Commits(); n > 1 {
			t.Fatalf("整轮对账的提交条数 = %d，恒不得超过 1（A-35）", n)
		}
		facts.Ran, facts.Commits = tout.Ran, tk.Commits()
		if tout.Commit != nil {
			facts.Commit = *tout.Commit
		}
		facts.Pending, facts.Existing, facts.Files = tout.Pending, tout.Existing, tout.Files
	}

	raw, merr := json.Marshal(facts)
	if merr != nil {
		t.Fatal(merr)
	}
	if out := os.Getenv(envR2Out); out != "" {
		if werr := os.WriteFile(out, append(raw, '\n'), 0o644); werr != nil {
			t.Fatalf("写事实文件失败：%v", werr)
		}
	}
	t.Logf("R2 事实：%s", raw)
}
