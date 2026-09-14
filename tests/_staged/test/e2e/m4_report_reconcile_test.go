// M4 · 报告 `reconcile` 字段的**端到端驱动**（T-evergreen.s1_main_flow-158614-057）。
//
// 存在的理由：本 task 只交付报告投影，**不注册** `eg reconcile` / `eg check`（属 T-…-058 /
// T-…-059）。因此「跑过对账的那条路径」在 M4 当下还没有命令外壳可用，照
// `m4_r1_takeover_test.go` 的先例，本文件充当唯一的驱动壳：
//
//   - 读**真实 eg 命令**（非对账写命令）落盘的 `--json` 信封，从 `data.report` 里取报告体，
//     回报它的顶层键序与 `reconcile` 那一块的原文；
//   - 用同一套报告结构造「跑过对账」的那条路径（`SetReconcile`），回报同样的两项事实；
//   - 把两条路径的顶层键序比对结果写成 JSON，供 `test/e2e/m4_report_reconcile.sh` 逐条断言。
//
// 三条纪律：
//   - 不新增任何产品能力、不复制平行实现：占位形态取自 report 包的唯一常量，
//     finding 键集合取自对账包的唯一真源（两者的等号在 internal/cli 侧已锁死）；
//   - 未设 EG_M4_RPT_ENVELOPE 时**直接 skip**，因此 `go test ./...` 的行为一字不变；
//   - 全程零写盘（除了把事实写进脚本指定的输出文件）、零 commit、零命令注册。
package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
)

// 环境变量（全部由 e2e 脚本设置；缺信封文件即 skip）。
const (
	envRptEnvelope = "EG_M4_RPT_ENVELOPE" // 真实 eg 命令 --json 输出的文件路径
	envRptOut      = "EG_M4_RPT_OUT"      // JSON 事实落盘路径
	envRptSha      = "EG_M4_RPT_SHA"      // 「跑过对账」那条路径要写进 commit 键的 sha
)

// rptFacts 是驱动壳回报的全部事实（键名固定，供脚本 grep 逐条复算）。
type rptFacts struct {
	RealKeys        []string `json:"real_keys"`        // 真实命令输出的报告体顶层键序
	ReconciledKeys  []string `json:"reconciled_keys"`  // 跑过对账那条路径的报告体顶层键序
	KeySetEqual     bool     `json:"key_set_equal"`    // 两条路径的顶层键序是否逐位相等
	RealReconcile   string   `json:"real_reconcile"`   // 真实命令输出里 reconcile 那一块的原文
	Placeholder     string   `json:"placeholder"`      // report 包的唯一占位常量
	RanReal         bool     `json:"ran_real"`         // 非对账路径的 ran
	RanReconciled   bool     `json:"ran_reconciled"`   // 对账路径的 ran
	CommitReconcile string   `json:"commit_reconcile"` // 对账路径的 commit 键值（空串 = null）
	FindingsCount   int      `json:"findings_count"`   // 对账路径的 findings 条数
	FindingKeys     []string `json:"finding_keys"`     // findings 元素的键序（回读序列化结果）
	MirrorKeys      []string `json:"mirror_keys"`      // report 包镜像键集合
	SourceKeys      []string `json:"source_keys"`      // 对账包 Finding 真源键集合
	StableTwice     bool     `json:"stable_twice"`     // 同一报告两次序列化逐字节相同
	S1RequiredCount int      `json:"s1_required"`      // S1 必填项数（恒 11）
	AffectedRaw     string   `json:"affected_raw"`     // affected 占位键的原文（恒 null）
}

// TestM4ReportReconcileHarness 是驱动壳入口（脚本用 -run 精确选中）。
func TestM4ReportReconcileHarness(t *testing.T) {
	envPath := os.Getenv(envRptEnvelope)
	if envPath == "" {
		t.Skipf("未设 %s：本用例只在 e2e 脚本里运行", envRptEnvelope)
	}

	// ① 真实命令输出：从信封的 data.report 取报告体（不看实现自报，回读 eg 自己的输出）。
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("读信封 %s 失败：%v", envPath, err)
	}
	var envelope struct {
		Data struct {
			Report json.RawMessage `json:"report"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("信封不是 JSON：%v", err)
	}
	if len(envelope.Data.Report) == 0 {
		t.Fatalf("信封的 data.report 为空：%s", raw)
	}
	realKeys := topLevelKeys(t, envelope.Data.Report)
	realBlock := rawKey(t, envelope.Data.Report, "reconcile")
	facts := rptFacts{
		RealKeys:        realKeys,
		RealReconcile:   realBlock,
		Placeholder:     report.ReconcilePlaceholderJSON,
		RanReal:         strings.Contains(realBlock, `"ran":false`),
		AffectedRaw:     rawKey(t, envelope.Data.Report, "affected"),
		MirrorKeys:      report.ReconcileFindingKeys(),
		SourceKeys:      reconcile.FindingKeys(),
		S1RequiredCount: len(report.RequiredKeys()),
	}

	// ② 跑过对账的那条路径：同一套报告结构 + SetReconcile（真实 finding 四键取自对账包真源）。
	f1 := reconcile.Finding{
		Check:    reconcile.CheckGitUncommitted,
		Severity: reconcile.SeverityWarning,
		Targets:  reconcile.NormalizeTargets([]string{"unprocessed.md"}),
		Detail:   "vault 内有未提交改动，已纳管",
	}
	f2 := reconcile.Finding{
		Check:    reconcile.CheckDuplicateID,
		Severity: reconcile.SeverityError,
		Targets: reconcile.NormalizeTargets([]string{
			"domains/ai-infra/knowledge/k-20261201-dup.md",
			"domains/robotics/knowledge/k-20261201-dup.md",
		}),
		Detail: "同一 ID 落在两个文件",
	}
	rep := report.New()
	rep.SetReconcile(true, os.Getenv(envRptSha), []report.ReconcileFinding{
		{Check: f1.Check, Severity: f1.Severity, Targets: f1.Targets, Detail: f1.Detail},
		{Check: f2.Check, Severity: f2.Severity, Targets: f2.Targets, Detail: f2.Detail},
	})
	// C2a·M6 现态重钉（合同 §4.6 / A-59；保留历史不变量 + 现态对齐，非放宽）：
	// `eg apply` 与 `eg reconcile` 同属 A 类事务写命令，成功分配事务号后按 A-59 **必填** `txn_id`
	// （report 包以 omitempty 承载：事务写填、非事务路径省略）。真实命令信封（apply.json）因此
	// 带 `txn_id` 顶层键；本驱动壳的合成对账报告同样代表一条**事务写路径**，故对齐真实路径显式补一个
	// A-59 形态（^t[0-9a-f]{16}$）的 `txn_id`，使「两条写路径顶层键集合逐位相等」这一历史不变量在 M6 下
	// 原样成立。键集合比较只看键名、不看取值，此处用固定合法形态占位即可（驱动壳未跑真实事务）。
	rep.SetTxnID("t0123456789abcdef")
	body, err := rep.JSON()
	if err != nil {
		t.Fatalf("序列化对账路径的报告失败：%v", err)
	}
	again, err := rep.JSON()
	if err != nil {
		t.Fatal(err)
	}
	facts.StableTwice = string(body) == string(again)
	facts.ReconciledKeys = topLevelKeys(t, body)
	facts.KeySetEqual = strings.Join(facts.RealKeys, ",") == strings.Join(facts.ReconciledKeys, ",")

	block := rawKey(t, body, "reconcile")
	var rc struct {
		Ran      bool              `json:"ran"`
		Commit   *string           `json:"commit"`
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal([]byte(block), &rc); err != nil {
		t.Fatalf("对账路径的 reconcile 不可解析：%v（%s）", err, block)
	}
	facts.RanReconciled = rc.Ran
	if rc.Commit != nil {
		facts.CommitReconcile = *rc.Commit
	}
	facts.FindingsCount = len(rc.Findings)
	if len(rc.Findings) > 0 {
		facts.FindingKeys = topLevelKeys(t, rc.Findings[0])
	}

	out := os.Getenv(envRptOut)
	if out == "" {
		t.Fatalf("未设 %s：脚本必须指定事实落盘路径", envRptOut)
	}
	blob, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, blob, 0o644); err != nil {
		t.Fatalf("写事实文件失败：%v", err)
	}
	t.Logf("非对账路径 reconcile = %s；对账路径 findings %d 条、commit=%q",
		facts.RealReconcile, facts.FindingsCount, facts.CommitReconcile)
}

// topLevelKeys 按**序列化次序**取一个 JSON 对象的顶层键（键序也是合同的一部分）。
func topLevelKeys(t *testing.T, blob []byte) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(blob)))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("不是 JSON 对象：%v", err)
	}
	var out []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("读键失败：%v", err)
		}
		name, ok := tok.(string)
		if !ok {
			t.Fatalf("键不是字符串：%v", tok)
		}
		out = append(out, name)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("跳过 %s 的值失败：%v", name, err)
		}
	}
	return out
}

// rawKey 取顶层某个键的**原文**（缺键即判红：阶段键必须「键在」）。
func rawKey(t *testing.T, blob []byte, key string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(blob, &m); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	v, ok := m[key]
	if !ok {
		t.Fatalf("报告体缺 %q 键（阶段字段要求键在、值不造假）", key)
	}
	return string(v)
}
