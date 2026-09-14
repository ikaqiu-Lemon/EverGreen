// M4 · R1 纳管的**端到端驱动**（T-evergreen.s1_main_flow-158614-050）。
//
// 存在的理由：R1 的两侧能力已经落地（只读检查 `internal/reconcile` + 恰一次纳管 commit
// 的写口 `internal/cli` 的 ReconcileTakeover），但 `eg reconcile` **命令本体属 T-…-058**，
// 本 task 明确不注册命令。于是 e2e 无法用 `eg` 子命令驱动这条链路——照 `m3_superseded.sh`
// 的先例（能力已落地、外壳属下游 task 时用 `go test` 驱动真实临时 vault），本文件充当
// **唯一的驱动壳**：读环境变量指定的真实 vault，跑检查 / 跑纳管，把事实写成 JSON 供
// `test/e2e/m4_r1_takeover.sh` 逐条断言。
//
// 三条纪律：
//   - 不新增任何产品能力、不复制平行实现：检查只调 reconcile.Run，纳管只调写口；
//   - 未设 EG_M4_R1_VAULT 时**直接 skip**，因此 `go test ./...` 的行为一字不变；
//   - probe 模式恒零写入零提交（只读检查），takeover 模式恒 0 或 1 次提交。
package e2e

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// 环境变量（全部由 e2e 脚本设置；缺 vault 即 skip）。
const (
	envR1Vault  = "EG_M4_R1_VAULT"  // 真实临时 vault 根
	envR1Mode   = "EG_M4_R1_MODE"   // probe（只检查）/ takeover（检查 + 纳管）/ dryrun
	envR1Out    = "EG_M4_R1_OUT"    // JSON 事实落盘路径
	envR1Domain = "EG_M4_R1_DOMAIN" // 提交主题的域位
	envR1Ours   = "EG_M4_R1_OURS"   // 本次「由 eg 自己写入」的路径（可空）
)

// r1Facts 是驱动壳回报的全部事实（JSON 键固定，供脚本 grep 逐条复算）。
type r1Facts struct {
	Mode           string                 `json:"mode"`
	Ran            bool                   `json:"ran"`
	Commit         string                 `json:"commit"` // 空串 = 未产生提交
	Commits        int                    `json:"commits"`
	GitUncommitted int                    `json:"git_uncommitted"`
	Findings       []reconcile.Finding    `json:"findings"`
	Codes          []string               `json:"codes"`
	Repairs        []reconcile.RepairSpec `json:"repairs"`
	Pending        []string               `json:"pending"`
	Existing       []string               `json:"existing"`
	Files          []string               `json:"files"`
	Warnings       []string               `json:"warnings"`
}

// TestM4R1TakeoverHarness 在脚本给定的真实 vault 上跑 R1 的检查与纳管。
func TestM4R1TakeoverHarness(t *testing.T) {
	vault := os.Getenv(envR1Vault)
	if vault == "" {
		t.Skip("未设 " + envR1Vault + "：本文件只在 e2e 脚本驱动下运行（go test ./... 行为不变）")
	}
	mode := os.Getenv(envR1Mode)
	if mode == "" {
		mode = "probe"
	}
	repo := git.New(vault)
	st, err := repo.Status()
	if err != nil {
		t.Fatalf("读 Git 状态失败：%v", err)
	}

	// ① 只读检查：R1 的 finding 全部由 reconcile 包产出（本文件不自造 finding）。
	res := reconcile.Run(reconcile.R1StatusOf(vault, st))
	facts := r1Facts{Mode: mode, Findings: res.Findings, Repairs: res.Repairs}
	for _, f := range res.Findings {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合 schema：%v（%+v）", err, f)
		}
		facts.Codes = append(facts.Codes, f.Code())
		if f.Check == reconcile.CheckGitUncommitted {
			facts.GitUncommitted++
		}
	}
	if len(res.Repairs) != 0 {
		t.Fatalf("R1 不产 RepairSpec（Git 纳管不是知识数据修改），实际 %d 条", len(res.Repairs))
	}

	// ② 纳管：写口只在 takeover / dryrun 两个模式下调用，且一次实例只调一次。
	if mode != "probe" {
		tk := cli.NewReconcileTakeover(repo)
		out, terr := tk.Takeover(cli.ReconcileTakeoverInput{
			Domain:         os.Getenv(envR1Domain),
			Findings:       len(res.Findings),
			Repairs:        0,
			OurWrites:      splitEnvList(os.Getenv(envR1Ours)),
			ChecksDone:     true,
			RepairsDone:    true,
			DryRun:         mode == "dryrun",
			Reason:         "e2e：R1 外部编辑纳管（恰一次 commit）",
			RequirementIDs: []string{"EG-EDIT-05"},
		})
		if terr != nil {
			t.Fatalf("纳管失败：%v", terr)
		}
		if n := tk.Commits(); n > 1 {
			t.Fatalf("写口提交条数 = %d，恒不得超过 1（A-35）", n)
		}
		facts.Ran = out.Ran
		facts.Commits = tk.Commits()
		if out.Commit != nil {
			facts.Commit = *out.Commit
		}
		facts.Pending, facts.Existing = out.Pending, out.Existing
		facts.Files, facts.Warnings = out.Files, out.Warnings
	}

	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv(envR1Out); out != "" {
		if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
			t.Fatalf("写事实文件失败：%v", err)
		}
	}
	t.Logf("R1 事实：%s", raw)
}

// splitEnvList 把逗号分隔的路径列表切开（空串 → nil）。
func splitEnvList(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
