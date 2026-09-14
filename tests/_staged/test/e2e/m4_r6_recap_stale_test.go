// M4 · R6 综述失准标记的**端到端驱动**（T-evergreen.s1_main_flow-158614-055 阶段 3）。
//
// 存在的理由与 `m4_r2_reviewed_test.go` / `m4_r7_support_test.go` 同源：R6 的三侧能力都已落地
// （只读判定在 `internal/reconcile`，修复桥在 `internal/cli`，落盘在 `internal/plan` →
// `internal/store`），但 `eg reconcile` **命令本体属 T-…-058**，本 task 明确**零命令注册**。
// 于是 e2e 无法用 `eg` 子命令驱动整条链路 —— 照既有先例，本文件充当**唯一的驱动壳**：
// 在脚本给定的真实临时 vault 上按模式跑「扫描 + 综述分区采样 → reconcile.Run → R6 修复桥
// → 恰一次纳管 commit」，把事实写成紧凑 JSON 供 `test/e2e/m4_r6_recap_stale.sh` 逐条断言。
//
// 五条纪律：
//   - **不新增产品能力、不复制平行实现**：finding 只由 reconcile 包产出，写入只由修复桥经
//     内存 ChangePlan 落盘，commit 只由 R1 的纳管写口产生（A-35：整轮恰一次）；
//   - **A-34**：修复入参里**逐字没有**授权佐证那一格 —— R6 写两键不需要 `--user-request`；
//   - probe 模式恒零写入零提交；repair / stale 模式的提交条数恒 0 或 1；
//   - stale 模式刻意给一个**过期的观测 hash**，用来反证 B3 不豁免（跳过且零写入）；
//   - 未设 EG_M4_R6_VAULT 时**直接 skip**，因此 `go test ./...` 的行为一字不变。
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
	envR6Vault  = "EG_M4_R6_VAULT"  // 真实临时 vault 根
	envR6Mode   = "EG_M4_R6_MODE"   // probe（只检查）/ repair（检查 + 修复 + 纳管）/ stale（B3）
	envR6Out    = "EG_M4_R6_OUT"    // JSON 事实落盘路径（脚本的 mktemp 目录内）
	envR6Domain = "EG_M4_R6_DOMAIN" // 提交主题的域位
	envR6Stamp  = "EG_M4_R6_STAMP"  // 本次对账时刻（RFC3339）
)

// r6StampDefault 是缺省对账时刻（脚本可覆盖；固定值使全过程确定性）。
const r6StampDefault = "2026-11-30T10:00:00+08:00"

// r6StaleHash 是 stale 模式注入的**过期观测 hash**（B3 前置比对必然不通过）。
const r6StaleHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// r6Facts 是驱动壳回报的全部事实（JSON 键固定，供脚本 grep / jq 逐条复算）。
type r6Facts struct {
	Mode string `json:"mode"`
	Ran  bool   `json:"ran"`
	// Commit 是整轮对账那**唯一**一次提交的 SHA（空串 = 未提交）。
	Commit  string `json:"commit"`
	Commits int    `json:"commits"`
	// Findings / Codes / Repairs 是只读检查的原样产出（四键 schema，不加工不裁剪）。
	Findings []reconcile.Finding    `json:"findings"`
	Codes    []string               `json:"codes"`
	Repairs  []reconcile.RepairSpec `json:"repairs"`
	// RecapStale 是 R6 的 finding 条数；Targets / TargetIDs 是命中综述（可复算序）。
	RecapStale int      `json:"recap_stale"`
	Targets    []string `json:"targets"`
	TargetIDs  []string `json:"target_ids"`
	// TargetReasons 是逐篇命中综述的失准理由（封闭三值之一，与落盘值同源）。
	TargetReasons []string `json:"target_reasons"`
	// AlreadyMarked 是落盘上**已是**同一标记与同一理由的命中综述 ID（幂等面）。
	AlreadyMarked []string `json:"already_marked"`
	// 采样面：综述分区采样到的路径与条数（nil = 未采样即不判，故此处恒非 nil）。
	SampledPaths []string `json:"sampled_paths"`
	Sampled      int      `json:"sampled"`
	// 修复侧事实：op 条数、待写键集合、实际写入路径、落盘成功 / 幂等的综述、跳过项。
	Ops         int                   `json:"ops"`
	Keys        []string              `json:"keys"`
	Written     []string              `json:"written"`
	Staled      []string              `json:"staled"`
	Idempotent  []string              `json:"idempotent"`
	Reasons     map[string]string     `json:"reasons"`
	Skipped     []cli.StaleRepairSkip `json:"skipped"`
	SkippedKind []string              `json:"skipped_kinds"`
	Warnings    []string              `json:"warnings"`
	Failures    []string              `json:"failures"`
	Pending     []string              `json:"pending"`
	Existing    []string              `json:"existing"`
	Files       []string              `json:"files"`
	HasError    bool                  `json:"has_error"`
	ExitCode    int                   `json:"exit_code"`
}

// TestM4R6RecapStaleHarness 在脚本给定的真实 vault 上跑 R6 的检查与修复。
func TestM4R6RecapStaleHarness(t *testing.T) {
	vault := os.Getenv(envR6Vault)
	if vault == "" {
		t.Skip("未设 " + envR6Vault + "：本文件只在 e2e 脚本驱动下运行（go test ./... 行为不变）")
	}
	mode := os.Getenv(envR6Mode)
	if mode == "" {
		mode = "probe"
	}
	stampRaw := os.Getenv(envR6Stamp)
	if stampRaw == "" {
		stampRaw = r6StampDefault
	}
	at, err := time.Parse(time.RFC3339, stampRaw)
	if err != nil {
		t.Fatalf("对账时刻 %q 不是合法 RFC3339：%v", stampRaw, err)
	}

	// ① 取数：M2 的全量扫描底座 + 综述分区的只读采样（本文件不另写扫描器、不发写命令）。
	scan, serr := query.VaultScan(vault, query.ScanOptions{IncludeNotes: true})
	if serr != nil {
		t.Fatalf("扫描 vault 失败：%v", serr)
	}
	sample, rerr := cli.SampleRecaps(vault)
	if rerr != nil {
		t.Fatalf("采样综述分区失败：%v", rerr)
	}
	if sample.Recaps == nil {
		t.Fatal("采样结果为 nil：nil 语义是「未采样即不判」，采样成功时必须是非 nil 切片")
	}
	in := reconcile.R6ScanOf(vault, scan, sample.Recaps)

	// ② 只读检查：finding / RepairSpec 全部由 reconcile 包产出。
	res := reconcile.Run(in)
	targets := reconcile.RecapTargets(in)
	facts := r6Facts{Mode: mode, Findings: res.Findings, Repairs: res.Repairs,
		Keys: reconcile.RecapStaleKeys(), HasError: res.HasError(),
		SampledPaths: sample.Paths, Sampled: len(sample.Recaps),
		Reasons: map[string]string{}}
	if facts.HasError {
		facts.ExitCode = 2 // A-31：存在 error 级 finding → 退 2（命令本体属 T-…-058）
	}
	for _, f := range res.Findings {
		if verr := f.Validate(); verr != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", verr, f)
		}
		facts.Codes = append(facts.Codes, f.Code())
		if f.Check == reconcile.CheckRecapStale {
			facts.RecapStale++
		}
	}
	for _, tg := range targets {
		facts.Targets = append(facts.Targets, tg.Path)
		facts.TargetIDs = append(facts.TargetIDs, tg.ID)
		facts.TargetReasons = append(facts.TargetReasons, string(tg.Reason))
		if tg.AlreadyMarked {
			facts.AlreadyMarked = append(facts.AlreadyMarked, tg.ID)
		}
	}

	// ③ 修复 + 纳管（probe 模式一步都不做：只读检查恒零写入零提交）。
	if mode != "probe" {
		r := cli.New()
		r.Now = func() time.Time { return at }
		r.In = nil
		// A-34：入参里**没有**授权佐证那一格 —— R6 写两键不需要 `--user-request`，仍走 ChangePlan。
		input := cli.StaleRepairInput{
			VaultRoot: vault, Targets: targets, Repairs: res.Repairs, Findings: res.Findings,
			Reason:         "e2e：对账 R6 为引用卡已变化的主题综述写失准标记两键",
			RequirementIDs: []string{"EG-EDIT-05"},
		}
		if mode == "stale" {
			// B3 不豁免的反证：观测 hash 过期 → 写前比对不通过 → 跳过且零写入。
			input.Base = map[string]string{}
			for _, tg := range targets {
				input.Base[tg.ID] = r6StaleHash
			}
		}
		out, xerr := r.RepairRecapStale(input)
		if xerr != nil {
			t.Fatalf("R6 修复失败：%v（%+v）", xerr, out)
		}
		facts.Ops, facts.Keys = out.Ops, out.Keys
		facts.Written, facts.Staled, facts.Idempotent = out.Written, out.Staled, out.Idempotent
		facts.Reasons, facts.Skipped = out.Reasons, out.Skipped
		for _, s := range out.Skipped {
			facts.SkippedKind = append(facts.SkippedKind, s.Kind)
		}
		facts.Warnings, facts.Failures = out.Warnings, out.Failures
		facts.Findings = out.Findings // 跳过时带「已跳过」注记的同一批 finding
		if out.Commits != 0 {
			t.Fatalf("修复桥的提交条数 = %d，恒应为 0（A-35：提交归纳管写口）", out.Commits)
		}

		// 整轮对账的**恰一次** commit：R1 的外部编辑与 R6 的两键写入同进这一条。
		repo := git.New(vault)
		tk := cli.NewReconcileTakeover(repo)
		tout, terr := tk.Takeover(cli.ReconcileTakeoverInput{
			Domain:   os.Getenv(envR6Domain),
			Findings: len(res.Findings), Repairs: len(res.Repairs),
			OurWrites: out.Written, ChecksDone: true, RepairsDone: true,
			Reason:         "e2e：对账（R1 纳管 + R6 失准标记，恰一次 commit）",
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
	if out := os.Getenv(envR6Out); out != "" {
		if werr := os.WriteFile(out, append(raw, '\n'), 0o644); werr != nil {
			t.Fatalf("写事实文件失败：%v", werr)
		}
	}
	t.Logf("R6 事实：%s", raw)
}
