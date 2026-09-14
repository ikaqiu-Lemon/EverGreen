package cli

// 报告体 `reconcile` 三键与对账包 Finding 四键的**两侧等号锁**
// （M4 · T-evergreen.s1_main_flow-158614-057）。
//
// # 为什么锁必须放在命令层
//
// 依赖方向是单向的：`internal/report` 是 S1 的下游叶子包，**不得** import `internal/reconcile`
// （`cmd/eg/arch_test.go` 的 S3 包边界用例把 `report` 逐名列进反向依赖禁令）。因此报告侧持有
// 一份四键**镜像**结构体，而「镜像与真源逐字同名」这件事只能在**同时看得见两个包**的层反证 ——
// 这一层就是 `internal/cli`。本文件因此是那道等号的唯一落点，办法与 M3 的
// `report.ExecutionFailed` ⟷ `proposal` 状态字面量同一套：谁改了任一侧的键名，这里当场判红。
//
// # 本文件的边界
//
//   - **零命令注册**：不注册 `eg reconcile` / `eg check`（属 T-…-058 / T-…-059），
//     不写任何用法行；文件名以 `reconcile` 起头，满足合同 §0.1 第 3 条的位置锁。
//   - **零写入**：全是纯结构层断言，不建临时 vault、不落盘、不提交。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
)

// TestReconcileFindingKeysLockedToReconcilePackage 钉住报告镜像与对账真源的**等号**：
// 键集合逐元素相等、键数相等，且两侧都恰四键。
func TestReconcileFindingKeysLockedToReconcilePackage(t *testing.T) {
	src := reconcile.FindingKeys()
	mirror := report.ReconcileFindingKeys()
	if len(src) != reconcile.FindingKeyCount || len(mirror) != report.ReconcileFindingKeyCount {
		t.Fatalf("键数：对账包 %d（常量 %d）/ 报告镜像 %d（常量 %d），四者必须同为 4",
			len(src), reconcile.FindingKeyCount, len(mirror), report.ReconcileFindingKeyCount)
	}
	if reconcile.FindingKeyCount != report.ReconcileFindingKeyCount {
		t.Fatalf("键数常量不等：对账包 %d ≠ 报告镜像 %d", reconcile.FindingKeyCount,
			report.ReconcileFindingKeyCount)
	}
	if strings.Join(src, ",") != strings.Join(mirror, ",") {
		t.Fatalf("finding 键集合两侧不等：对账包 %v ≠ 报告镜像 %v（键名与键序都必须逐字同名）",
			src, mirror)
	}
	if strings.Join(mirror, ",") != "check,severity,targets,detail" {
		t.Fatalf("镜像键集合 = %v，期望逐字 [check severity targets detail]（合同 §2）", mirror)
	}
}

// TestReconcileReportFieldThreeKeysFromCLI 从命令层再钉一遍报告 `reconcile` 的三键与空值口径：
// 非对账路径「键在值空」、对账路径三键都产真实值，且**恰三键**不因经过命令层而变形。
func TestReconcileReportFieldThreeKeysFromCLI(t *testing.T) {
	keys := report.ReconcileKeys()
	if len(keys) != report.ReconcileKeyCount || strings.Join(keys, ",") != "ran,commit,findings" {
		t.Fatalf("reconcile 键集合 = %v，期望恰 3 键且逐字 [ran commit findings]（合同 §11）", keys)
	}

	// ① 非对账路径：占位形态逐字等于唯一占位常量。
	raw, err := report.New().JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"reconcile":`+report.ReconcilePlaceholderJSON) {
		t.Fatalf("非对账路径的 reconcile 不是占位形态：%s", raw)
	}

	// ② 对账路径：真实值经报告层搬运后，四键 finding 逐字保留，数量守恒。
	r := report.New()
	f := reconcile.Finding{Check: reconcile.CheckGitUncommitted, Severity: reconcile.SeverityWarning,
		Targets: reconcile.NormalizeTargets([]string{"domains/ai-infra/knowledge/k-1.md"}),
		Detail:  "vault 内有未提交改动，已纳管"}
	r.SetReconcile(true, "0ed1181a7aa214ac81382491025c9137fcd3a14c", []report.ReconcileFinding{
		{Check: f.Check, Severity: f.Severity, Targets: f.Targets, Detail: f.Detail},
	})
	out, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"ran":true`,
		`"commit":"0ed1181a7aa214ac81382491025c9137fcd3a14c"`,
		`"check":"` + f.Check + `"`, `"severity":"warning"`,
		`"targets":["domains/ai-infra/knowledge/k-1.md"]`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("对账路径的报告缺事实 %s：%s", want, out)
		}
	}
	// 报告只搬运：不排序、不去重、不重新判定 —— 两条完全相同的 finding 必须原样保留两条。
	r2 := report.New()
	dup := report.ReconcileFinding{Check: f.Check, Severity: f.Severity,
		Targets: f.Targets, Detail: f.Detail}
	r2.SetReconcile(true, "", []report.ReconcileFinding{dup, dup})
	out2, err := r2.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(out2), `"check":"`+f.Check+`"`); n != 2 {
		t.Fatalf("两条相同 finding 被折叠成 %d 条：报告层数量守恒（不去重、不折叠）", n)
	}
	if !strings.Contains(string(out2), `"commit":null`) {
		t.Fatalf("零改动路径的 commit 键必须为 null：%s", out2)
	}
}
