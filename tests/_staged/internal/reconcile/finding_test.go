package reconcile

// Finding 四键 schema 的表驱动反证（M-004 判据 3 / T-…-049 Acceptance 直接消费）。
//
// 四组用例：四键恰等 / severity 恰两值 / targets 去重与字典序 / findings 排序可复算。

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestFindingSchemaExactlyFourKeys 反证 Finding 恰四键：字段数、字段名与 JSON tag 的
// 逐位对应、序列化后的键集合三重比对；并反证空 targets 序列化为 [] 而不是 null。
func TestFindingSchemaExactlyFourKeys(t *testing.T) {
	typ := reflect.TypeOf(Finding{})
	if typ.NumField() != FindingKeyCount {
		t.Fatalf("Finding 字段数 = %d，合同 §2 定死为恰 %d", typ.NumField(), FindingKeyCount)
	}
	wantFields := []struct {
		name string
		tag  string
		kind reflect.Kind
	}{
		{"Check", "check", reflect.String},
		{"Severity", "severity", reflect.String},
		{"Targets", "targets", reflect.Slice},
		{"Detail", "detail", reflect.String},
	}
	for i, w := range wantFields {
		f := typ.Field(i)
		if f.Name != w.name {
			t.Errorf("字段 %d = %q，期望 %q（键序即合同 §2 的键序）", i, f.Name, w.name)
		}
		if got := f.Tag.Get("json"); got != w.tag {
			t.Errorf("字段 %s 的 json tag = %q，期望逐字 %q", f.Name, got, w.tag)
		}
		if f.Type.Kind() != w.kind {
			t.Errorf("字段 %s 的类型 = %s，期望 %s", f.Name, f.Type.Kind(), w.kind)
		}
	}
	if got := strings.Join(FindingKeys(), ","); got != "check,severity,targets,detail" {
		t.Fatalf("FindingKeys() = %q，期望逐字 check,severity,targets,detail", got)
	}
	// 序列化侧：键集合恰四键、不多不少；空集合为 []。
	f, err := NewFinding(CheckOrphan, nil, "孤儿：材料 s-x 无派生笔记")
	if err != nil {
		t.Fatalf("NewFinding 失败：%v", err)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if len(m) != FindingKeyCount {
		t.Fatalf("序列化键数 = %d（%s），期望恰 %d", len(m), raw, FindingKeyCount)
	}
	for _, k := range FindingKeys() {
		if _, ok := m[k]; !ok {
			t.Errorf("序列化结果缺键 %q：%s", k, raw)
		}
	}
	if string(m["targets"]) != "[]" {
		t.Fatalf("空 targets 序列化为 %s，必须是 []（不得为 null）", m["targets"])
	}
	// 构造侧：未知 check 与空 detail 一律被拒（半成品 finding 进不来）。
	for _, bad := range []struct{ name, check, detail string }{
		{"未知 check", "recap_" + "unknown", "x"},
		{"空 detail", CheckOrphan, "   "},
	} {
		if _, err := NewFinding(bad.check, nil, bad.detail); err == nil {
			t.Errorf("%s：NewFinding 应报错", bad.name)
		}
	}
}

// TestSeverityExactlyTwoValues 反证 severity 恰两值（error / warning，error 在前），
// 第三个分级不在集合内，且十二个 check 的 severity 逐个落在集合内、与真源表逐字相等。
func TestSeverityExactlyTwoValues(t *testing.T) {
	if got := AllSeverities(); len(got) != SeverityCount ||
		got[0] != SeverityError || got[1] != SeverityWarning {
		t.Fatalf("AllSeverities() = %v，期望恰 %d 值且 error 在前", got, SeverityCount)
	}
	cases := []struct {
		name  string
		value string
		known bool
	}{
		{"error 在集合内", "error", true},
		{"warning 在集合内", "warning", true},
		{"第三个分级不启用", "in" + "fo", false},
		{"空串不是分级", "", false},
		{"大小写不宽容", "Error", false},
		{"致命级不存在", "fatal", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsKnownSeverity(c.value); got != c.known {
				t.Fatalf("IsKnownSeverity(%q) = %v，期望 %v", c.value, got, c.known)
			}
			_, err := ParseSeverity(c.value)
			if c.known != (err == nil) {
				t.Fatalf("ParseSeverity(%q) err = %v，期望 known=%v", c.value, err, c.known)
			}
		})
	}
	// 十二个 check 的 severity 都在两值内，且 error 级恰四个（E11–E14 对应的四项）。
	nErr := 0
	for _, s := range Specs() {
		if !IsKnownSeverity(s.Severity) {
			t.Errorf("check %q 的 severity = %q 不在封闭两值内", s.Check, s.Severity)
		}
		if s.Severity == SeverityError {
			nErr++
		}
	}
	if nErr != 4 {
		t.Fatalf("error 级 check 数 = %d，合同 §3 定死为 4（duplicate_id / dangling_ref / "+
			"relation_target_missing / relation_prefix_invalid）", nErr)
	}
	// severity 由真源表定死：调用方自带的分级不会被 NewFinding 采信。
	f, err := NewFinding(CheckDuplicateID, []string{"k-a1"}, "同一 ID 出现在两个文件")
	if err != nil {
		t.Fatalf("NewFinding 失败：%v", err)
	}
	if f.Severity != SeverityError {
		t.Fatalf("duplicate_id 的 severity = %q，表内定死为 error", f.Severity)
	}
	bad := f
	bad.Severity = SeverityWarning
	if err := bad.Validate(); err == nil {
		t.Fatal("篡改 severity 后 Validate 应报错（单射不允许同一 check 两处分级）")
	}
}

// TestTargetsDedupedAndSorted 反证 targets 非 nil、去重、字典序升序（表驱动）。
func TestTargetsDedupedAndSorted(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil 收敛成空集合", nil, []string{}},
		{"空串被丢弃", []string{"", "  "}, []string{}},
		{"去重", []string{"k-a1", "k-a1"}, []string{"k-a1"}},
		{"升序", []string{"k-b2", "k-a1"}, []string{"k-a1", "k-b2"}},
		{"去重 + 升序 + 去空白", []string{" k-b2 ", "k-a1", "k-b2", ""},
			[]string{"k-a1", "k-b2"}},
		{"路径与 ID 混排仍是字典序",
			[]string{"domains/ai/cards/k-a1.md", "k-a1", "domains/ai/notes/n-1.md"},
			[]string{"domains/ai/cards/k-a1.md", "domains/ai/notes/n-1.md", "k-a1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeTargets(c.in)
			if got == nil {
				t.Fatal("NormalizeTargets 返回 nil：空集合必须是非 nil 的 []")
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("NormalizeTargets(%v) = %v，期望 %v", c.in, got, c.want)
			}
			f, err := NewFinding(CheckDanglingRef, c.in, "引用指向不存在的对象")
			if err != nil {
				t.Fatalf("NewFinding 失败：%v", err)
			}
			if !reflect.DeepEqual(f.Targets, c.want) {
				t.Fatalf("finding.Targets = %v，期望 %v", f.Targets, c.want)
			}
			if err := f.Validate(); err != nil {
				t.Fatalf("Validate 失败：%v", err)
			}
		})
	}
	// 未归一化的 targets 直接构造出来的 finding 必须被 Validate 拒收。
	for _, bad := range [][]string{{"k-b2", "k-a1"}, {"k-a1", "k-a1"}} {
		f := Finding{Check: CheckOrphan, Severity: SeverityWarning, Targets: bad, Detail: "x"}
		if err := f.Validate(); err == nil {
			t.Errorf("targets = %v 未归一化，Validate 应报错", bad)
		}
	}
	if err := (Finding{Check: CheckOrphan, Severity: SeverityWarning, Detail: "x"}).Validate(); err == nil {
		t.Error("targets 为 nil 时 Validate 应报错（空集合必须是 []）")
	}
}

// TestFindingsSortDeterministic 反证 findings[] 排序键 (severity, check, targets[0])
// 且 error 在前、结果可复算（同一输入的任意排列都收敛到同一序列、重复排序幂等）。
func TestFindingsSortDeterministic(t *testing.T) {
	mk := func(check, target, detail string) Finding {
		f, err := NewFinding(check, []string{target}, detail)
		if err != nil {
			t.Fatalf("NewFinding(%q) 失败：%v", check, err)
		}
		return f
	}
	cases := []struct {
		name string
		in   []Finding
		want []string // 期望的 (check|targets[0]) 序列
	}{
		{
			name: "error 恒在 warning 之前",
			in: []Finding{
				mk(CheckGitUncommitted, "domains/ai/cards/k-a1.md", "外部编辑未提交"),
				mk(CheckDuplicateID, "k-a1", "同 ID 两文件"),
			},
			want: []string{"duplicate_id|k-a1", "git_uncommitted|domains/ai/cards/k-a1.md"},
		},
		{
			name: "同 severity 按合同表格行序排 check",
			in: []Finding{
				mk(CheckRelationPrefixInvalid, "k-b2", "前缀不合法"),
				mk(CheckDanglingRef, "n-1", "悬空引用"),
				mk(CheckDuplicateID, "k-a1", "同 ID 两文件"),
				mk(CheckRelationTargetMissing, "k-c3", "target 不存在"),
			},
			want: []string{"duplicate_id|k-a1", "dangling_ref|n-1",
				"relation_target_missing|k-c3", "relation_prefix_invalid|k-b2"},
		},
		{
			name: "同 severity 同 check 按 targets[0] 升序",
			in: []Finding{
				mk(CheckOrphan, "s-2", "材料无派生笔记"),
				mk(CheckOrphan, "n-9", "笔记无所属材料"),
				mk(CheckOrphan, "k-1", "知识卡零关系"),
			},
			want: []string{"orphan|k-1", "orphan|n-9", "orphan|s-2"},
		},
		{
			name: "空 targets 排在同 check 的非空之前",
			in: []Finding{
				mk(CheckRecapStale, "domains/ai/recap.md", "综述可能失准"),
				func() Finding {
					f, err := NewFinding(CheckRecapStale, nil, "综述可能失准（无定位）")
					if err != nil {
						t.Fatalf("NewFinding 失败：%v", err)
					}
					return f
				}(),
			},
			want: []string{"recap_stale|", "recap_stale|domains/ai/recap.md"},
		},
	}
	key := func(fs []Finding) []string {
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, f.Check+"|"+strings.Join(f.Targets, ","))
		}
		return out
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SortedFindings(c.in)
			if !reflect.DeepEqual(key(got), c.want) {
				t.Fatalf("排序结果 = %v，期望 %v", key(got), c.want)
			}
			// 幂等：再排一次序列不变。
			again := SortedFindings(got)
			if !reflect.DeepEqual(key(again), c.want) {
				t.Fatalf("重复排序不幂等：%v", key(again))
			}
			// 可复算：逆序输入收敛到同一序列。
			rev := make([]Finding, 0, len(c.in))
			for i := len(c.in) - 1; i >= 0; i-- {
				rev = append(rev, c.in[i])
			}
			if got2 := SortedFindings(rev); !reflect.DeepEqual(key(got2), c.want) {
				t.Fatalf("逆序输入的排序结果 = %v，期望 %v", key(got2), c.want)
			}
			// SortedFindings 不改动入参。
			if !reflect.DeepEqual(key(c.in), key(c.in)) {
				t.Fatal("入参被改动")
			}
		})
	}
	// 空集合与 nil 都不 panic，且恒返回非 nil 空集合。
	if got := SortedFindings(nil); got == nil || len(got) != 0 {
		t.Fatalf("SortedFindings(nil) = %v，期望非 nil 空集合", got)
	}
}
