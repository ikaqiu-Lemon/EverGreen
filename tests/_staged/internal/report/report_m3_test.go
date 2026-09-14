package report

// T-…-036 的验收用例：M3 起报告的阶段字段口径 ——
// `proposals[]` 与 `deprecated_new_support[]` **首次产出真实值**，
// `support_check` / `affected` / `reconcile` **仍是占位形态**（属 S3/M4，绝不输出假数据）；
// 且 `execution=failed` 时两个路径数组**逐路径**齐全、键必须存在。
//
// 判据来源：提案合同 §4.3（两个新键必填 + 两侧一致性）、§8.3（阶段字段口径），
// 技术方案 §4.6（键集合封闭）。
//
// 报告层仍是**纯函数**：这里只断言结构与渲染，不碰磁盘、不认识 internal/proposal。
// 「两个数组由唯一账本产生」的证明在 internal/proposal 的 TestWrittenPaths_SingleSourceOfTruth，
// 端到端的两侧逐字一致在 test/e2e/m3_execution_failed.sh。

import (
	"encoding/json"
	"strings"
	"testing"
)

// m3Sample 造一份「提案执行失败 + 失效卡出现新支持材料」的报告。
func m3Sample() Report {
	r := New()
	r.Links = []string{"domains/ai-infra/knowledge/k-1.md"}
	r.Skipped = []Skipped{{Kind: "file_changed", Target: "k-2",
		Locator: "domains/ai-infra/knowledge/k-2.md",
		Cause:   "content_hash_mismatch", Detail: "文件自读取以来已变化，已跳过该文件"}}
	r.AddProposal(ProposalEntry{
		ID: "p-20260701-001", Path: "proposals/p-20260701-001.md", Status: "approved",
		Targets: []string{"k-1", "k-2"},
		Execution: ProposalExecution{
			Status: ExecutionFailed, AttemptedAt: "2026-10-20T10:00:00+08:00",
			Reason:         "本次执行部分完成：影响文件 2 个，已写 1 个、未写 1 个；已写入的内容保留在磁盘，未做任何还原（B4）",
			WrittenPaths:   []string{"domains/ai-infra/knowledge/k-1.md"},
			UnwrittenPaths: []string{"domains/ai-infra/knowledge/k-2.md"},
		},
	})
	r.AddDeprecatedNewSupport(DeprecatedSupport{
		Card: "k-3", Source: "s-20260901-demo", Note: "n-20260901-demo", Rel: "support",
		Detail: "已失效的卡又出现新的支持材料，如实上报，不自动恢复",
	})
	return r
}

// TestReportFields_M3Stage 是 task 逐字要求的用例名（提案合同 §9）。
func TestReportFields_M3Stage(t *testing.T) {
	r := m3Sample()
	m := keysOf(t, r)

	// ① 键集合不变：仍是必填 11 项 + 阶段 5 项，M3 没有新增报告体键。
	if want := len(RequiredKeys()) + len(StageKeys()); len(m) != want {
		t.Fatalf("报告体键数 = %d，期望恰 %d（M3 不新增报告体键）", len(m), want)
	}

	// ② proposals[] / deprecated_new_support[] 产出**真实值**（不再是空数组占位）。
	var props []ProposalEntry
	if err := json.Unmarshal(m["proposals"], &props); err != nil {
		t.Fatalf("proposals 不可解析：%v", err)
	}
	if len(props) != 1 {
		t.Fatalf("proposals 应有 1 条真实值，实得 %d 条", len(props))
	}
	got := props[0]
	if got.ID != "p-20260701-001" || got.Status != "approved" || got.Path == "" {
		t.Fatalf("proposals[0] 事实不完整：%+v", got)
	}
	var deps []DeprecatedSupport
	if err := json.Unmarshal(m["deprecated_new_support"], &deps); err != nil {
		t.Fatalf("deprecated_new_support 不可解析：%v", err)
	}
	if len(deps) != 1 || deps[0].Card != "k-3" || deps[0].Rel != "support" {
		t.Fatalf("deprecated_new_support 应有 1 条真实值，实得 %+v", deps)
	}

	// ③ execution=failed：两个路径键**都在**，逐路径列出，且并集 == 影响文件全集、交集为空。
	raw := string(m["proposals"])
	for _, key := range []string{`"written_paths":[`, `"unwritten_paths":[`} {
		if !strings.Contains(raw, key) {
			t.Fatalf("execution=failed 时 %s 必须存在（可为空数组，不得是 null）：%s", key, raw)
		}
	}
	union := map[string]int{}
	for _, p := range got.Execution.WrittenPaths {
		union[p]++
	}
	for _, p := range got.Execution.UnwrittenPaths {
		union[p]++
	}
	if len(union) != len(got.Targets) {
		t.Fatalf("两个数组的并集 %d 条，影响文件全集 %d 条：必须恰相等", len(union), len(got.Targets))
	}
	for p, n := range union {
		if n != 1 {
			t.Fatalf("路径 %s 同时出现在两个数组里：交集必须为空", p)
		}
	}

	// ④ 阶段占位字段仍是空值：S3/M4 的事实一个字都不许编。
	for k, want := range map[string]string{
		// `reconcile` 的期望值【M4 · T-…-057 按实测重钉】：由 `{"ran":false}` 改钉唯一占位
		// 常量（三键三值全钉，只加严）。M3 的结论一格未放宽 —— 非对账路径下 ran 恒 false。
		"support_check": "null", "affected": "null", "reconcile": ReconcilePlaceholderJSON,
	} {
		if string(m[k]) != want {
			t.Fatalf("%s = %s，期望 %s（属 S3/M4，不得输出假数据）", k, m[k], want)
		}
	}

	// ⑤ 人类可读形态同源：逐路径可见 + B4 口径写明。
	lines := strings.Join(r.Lines(), "\n")
	for _, fact := range []string{
		got.ID, got.Path, "execution=" + ExecutionFailed,
		got.Execution.WrittenPaths[0], got.Execution.UnwrittenPaths[0],
		"未做任何还原", deps[0].Card, deps[0].Detail,
	} {
		if !strings.Contains(lines, fact) {
			t.Fatalf("人类可读形态缺事实 %q：%s", fact, lines)
		}
	}
}

// 空报告里两个新数组仍是空数组（无事实 → 不造事实），且 failed 时两键一律用空数组兜底。
func TestReportProposalPathsNeverNull(t *testing.T) {
	m := keysOf(t, New())
	for _, k := range []string{"proposals", "deprecated_new_support"} {
		if string(m[k]) != "[]" {
			t.Fatalf("%s = %s，无事实时期望空数组", k, m[k])
		}
	}
	r := New()
	r.AddProposal(ProposalEntry{ID: "p-20260701-002", Path: "proposals/p-20260701-002.md",
		Status: "approved", Execution: ProposalExecution{
			Status: ExecutionFailed, AttemptedAt: "2026-10-20T10:00:00+08:00",
			Reason: "全部影响文件都没写成", WrittenPaths: nil, UnwrittenPaths: nil}})
	raw, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"targets":[]`, `"written_paths":[]`, `"unwritten_paths":[]`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("failed 时 %s 必须以空数组出现（键不得缺、不得是 null）：%s", key, raw)
		}
	}
}

// 数量守恒：proposals[] 不折叠、不去重（同一提案被两次回写就是两条事实）。
func TestReportProposalsAreConserved(t *testing.T) {
	r := New()
	const n = 3
	for i := 0; i < n; i++ {
		r.AddProposal(ProposalEntry{ID: "p-20260701-001", Path: "proposals/p-20260701-001.md",
			Status: "approved", Targets: []string{"k-1"},
			Execution: ProposalExecution{Status: ExecutionFailed,
				AttemptedAt: "2026-10-20T10:00:00+08:00", Reason: "同码同文，折叠的实现会在这里露馅",
				WrittenPaths: []string{}, UnwrittenPaths: []string{"domains/d/knowledge/k-1.md"}}})
	}
	if len(r.Proposals) != n {
		t.Fatalf("proposals = %d 条，期望 %d 条（数量守恒）", len(r.Proposals), n)
	}
	lines := strings.Join(r.Lines(), "\n")
	if got := strings.Count(lines, "  未写：domains/d/knowledge/k-1.md"); got != n {
		t.Fatalf("人类可读形态里未写路径出现 %d 次，期望 %d 次", got, n)
	}
}
