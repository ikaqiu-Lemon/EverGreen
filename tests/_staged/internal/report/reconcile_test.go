package report

// 报告体 `reconcile` 三键的验收用例（M4 · T-evergreen.s1_main_flow-158614-057）。
//
// 判据来源：对账合同 `2026-11-12-m4-reconcile-contract.md` **§11**（恰三键 / 类型与空值 /
// S3 阶段键不进 S1 必填集合 / 非对账路径「键在值空」/ 不新增 `affected` / 不新增
// `skipped[].kind`）与 **§2**（finding 四键）；`M-004-m4.md` 完成判据 8。
//
// 本文件是**纯结构层**用例：不碰磁盘、不碰 Git、不注册命令 —— 命令路径的端到端形态由
// `test/e2e/m4_report_reconcile.sh` 在真实临时 vault 上反证。

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// reconcileKeysOf 取出报告里 `reconcile` 那一块的键集合（按序列化结果读，不看结构体自报）。
func reconcileKeysOf(t *testing.T, r Report) map[string]json.RawMessage {
	t.Helper()
	raw, ok := keysOf(t, r)["reconcile"]
	if !ok {
		t.Fatal("报告体缺 reconcile 键：S3 阶段键必须「键在」，不得整键缺席（合同 §11）")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("reconcile 不是对象：%v（实得 %s）", err, raw)
	}
	return m
}

// —— ① 恰三键：字段数 + 标签集合 + 序列化键集合三重比对 ——

func TestReconcileFieldExactlyThreeKeys(t *testing.T) {
	rt := reflect.TypeOf(Reconcile{})
	if rt.NumField() != ReconcileKeyCount {
		t.Fatalf("Reconcile 有 %d 个字段，合同 §11 要求恰 %d 键（不增不减）",
			rt.NumField(), ReconcileKeyCount)
	}
	// 标签集合与声明顺序逐位比对：键序也是合同的一部分。
	for i, want := range ReconcileKeys() {
		got := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if got != want {
			t.Fatalf("Reconcile 第 %d 个字段的键 = %q，期望 %q（键序即合同 §11 的键序）",
				i, got, want)
		}
	}

	// 三种路径（占位 / 跑过零改动 / 跑过有改动有发现）的序列化键集合必须恒等于三键。
	cases := []struct {
		name string
		r    Report
	}{
		{"占位路径", New()},
		{"对账跑过但零改动", reconciled(false)},
		{"对账跑过且有改动", reconciled(true)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := reconcileKeysOf(t, c.r)
			if len(m) != ReconcileKeyCount {
				t.Fatalf("reconcile 有 %d 个键：%v，期望恰 %d 键", len(m), sortedMapKeys(m), ReconcileKeyCount)
			}
			for _, k := range ReconcileKeys() {
				if _, ok := m[k]; !ok {
					t.Fatalf("reconcile 缺键 %q（三键不增不减）", k)
				}
			}
			// 第四键反证：常见误加的四个键一个都不许在。
			for _, bad := range []string{"affected", "skipped", "checks", "repairs"} {
				if _, ok := m[bad]; ok {
					t.Fatalf("reconcile 出现第四键 %q：三键封闭（合同 §11）", bad)
				}
			}
		})
	}
}

// —— ② 非对账路径：键在、值为空，且逐字等于唯一占位常量 ——

func TestReconcileAbsentPathKeyPresentEmpty(t *testing.T) {
	for _, c := range []struct {
		name string
		r    Report
	}{
		{"空报告", New()},
		{"有原文有卡有关系但没跑对账", sample()},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := keysOf(t, c.r)["reconcile"]
			if len(raw) == 0 {
				t.Fatal("reconcile 整键缺席：非对账路径要求「键在值空」，不是省略键")
			}
			if string(raw) != ReconcilePlaceholderJSON {
				t.Fatalf("非对账路径 reconcile = %s，期望逐字 %s（下游不必区分「键缺席」与「值为空」）",
					raw, ReconcilePlaceholderJSON)
			}
			m := reconcileKeysOf(t, c.r)
			if string(m["ran"]) != "false" {
				t.Fatalf("非对账路径 ran = %s，期望 false（不得输出假数据）", m["ran"])
			}
			if string(m["commit"]) != "null" {
				t.Fatalf("非对账路径 commit 键 = %s，期望 null", m["commit"])
			}
			if string(m["findings"]) != "[]" {
				t.Fatalf("非对账路径 findings = %s，期望 []（不是 null）", m["findings"])
			}
		})
	}
}

// —— ③ findings 永不为 null ——

func TestReconcileFindingsNeverNull(t *testing.T) {
	cases := []struct {
		name     string
		findings []ReconcileFinding
		wantLen  int
	}{
		{"nil 入参", nil, 0},
		{"零长切片", []ReconcileFinding{}, 0},
		{"一条 error", []ReconcileFinding{{Check: "duplicate_id", Severity: "error",
			Targets: []string{"domains/a/knowledge/k-1.md", "domains/b/knowledge/k-1.md"},
			Detail:  "同一 ID 落在两个文件"}}, 1},
		{"targets 为 nil 的一条", []ReconcileFinding{{Check: "git_uncommitted",
			Severity: "warning", Detail: "工作区有未提交改动"}}, 1},
		{"两条不折叠", []ReconcileFinding{
			{Check: "orphan", Severity: "warning", Targets: []string{"x"}, Detail: "d1"},
			{Check: "orphan", Severity: "warning", Targets: []string{"x"}, Detail: "d1"},
		}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := New()
			r.SetReconcile(true, "0ed1181a7aa214ac81382491025c9137fcd3a14c", c.findings)
			m := reconcileKeysOf(t, r)
			if string(m["findings"]) == "null" {
				t.Fatal("findings = null：合同 §11 要求数组非 null，空时为 []")
			}
			var got []ReconcileFinding
			if err := json.Unmarshal(m["findings"], &got); err != nil {
				t.Fatalf("findings 不是数组：%v", err)
			}
			if len(got) != c.wantLen {
				t.Fatalf("findings 有 %d 条，期望 %d 条（数量守恒：不折叠、不去重）", len(got), c.wantLen)
			}
			// 每条 finding 的 targets 同样永不为 null。
			var rawList []map[string]json.RawMessage
			if err := json.Unmarshal(m["findings"], &rawList); err != nil {
				t.Fatalf("findings 元素不是对象：%v", err)
			}
			for i, item := range rawList {
				if len(item) != ReconcileFindingKeyCount {
					t.Fatalf("findings[%d] 有 %d 个键：%v，期望恰 %d 键（合同 §2）",
						i, len(item), sortedMapKeys(item), ReconcileFindingKeyCount)
				}
				if string(item["targets"]) == "null" {
					t.Fatalf("findings[%d].targets = null：空集合必须是 []", i)
				}
			}
		})
	}
}

// —— ④ 零改动时 commit 键为 null（不是空串） ——

func TestReconcileCommitNullOnZeroChange(t *testing.T) {
	sha := "0ed1181a7aa214ac81382491025c9137fcd3a14c"
	cases := []struct {
		name string
		ran  bool
		sha  string
		want string
	}{
		{"跑过 + 零改动零 commit", true, "", "null"},
		{"跑过 + --dry-run", true, "", "null"},
		{"跑过 + 一次纳管 commit", true, sha, `"` + sha + `"`},
		{"没跑", false, "", "null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := New()
			r.SetReconcile(c.ran, c.sha, nil)
			m := reconcileKeysOf(t, r)
			if string(m["commit"]) != c.want {
				t.Fatalf("commit 键 = %s，期望 %s（空串必须落成 null，绝不输出空 sha）",
					m["commit"], c.want)
			}
			if string(m["commit"]) == `""` {
				t.Fatal("commit 键 = 空串：脚本会误以为存在一个空 sha")
			}
			wantRan := "false"
			if c.ran {
				wantRan = "true"
			}
			if string(m["ran"]) != wantRan {
				t.Fatalf("ran = %s，期望 %s", m["ran"], wantRan)
			}
		})
	}
}

// —— ⑤ finding 四键镜像：本包的镜像键集合恰四项、顺序即合同 §2 的键序 ——
//
// 与对账包 `reconcile.FindingKeys()` 的**等号**在 internal/cli 侧断言
// （依赖方向单向，report 不得 import 对账包，见 reconcile.go 文件头）。
func TestReconcileFindingKeysMirrorFour(t *testing.T) {
	rt := reflect.TypeOf(ReconcileFinding{})
	if rt.NumField() != ReconcileFindingKeyCount {
		t.Fatalf("ReconcileFinding 有 %d 个字段，合同 §2 要求恰 %d 键",
			rt.NumField(), ReconcileFindingKeyCount)
	}
	want := ReconcileFindingKeys()
	for i, w := range want {
		got := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if got != w {
			t.Fatalf("ReconcileFinding 第 %d 个字段的键 = %q，期望 %q（与对账包逐字同名）", i, got, w)
		}
	}
	if strings.Join(want, ",") != "check,severity,targets,detail" {
		t.Fatalf("镜像键集合 = %v，期望逐字 [check severity targets detail]", want)
	}
}

// reconciled 造一份「跑过对账」的报告：有改动的那支带 commit 与两条 finding。
func reconciled(changed bool) Report {
	r := sample()
	findings := []ReconcileFinding{
		{Check: "git_uncommitted", Severity: "warning",
			Targets: []string{"domains/ai-infra/knowledge/k-20260815-rnn.md"},
			Detail:  "vault 内有未提交改动，已纳管"},
		{Check: "reviewed_at_missing", Severity: "warning",
			Targets: []string{"domains/ai-infra/knowledge/k-20260815-rnn.md"},
			Detail:  "用户直接编辑后缺过目时刻"},
	}
	if !changed {
		r.SetReconcile(true, "", nil)
		return r
	}
	r.SetReconcile(true, "0ed1181a7aa214ac81382491025c9137fcd3a14c", findings)
	return r
}

// sortedMapKeys 把键集合排成可读串，供失败信息使用。
func sortedMapKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// 简单插入排序：用例规模恒个位数，避免为排序引入额外依赖。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
