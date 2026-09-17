package reconcile

// R3 四项只读关系检查（E13 / E14 / W15 / W16）的表驱动单测（M4 · T-…-053）。
//
// 六组（deliverables 逐字要求，用例名不得改字）：
//  1. TestR3TargetMissing                 —— target 查无此对象 → E13 / error，聚合与去重可复算；
//  2. TestR3PrefixInvalidReusesModelRule   —— 前缀 / 形态非法 → E14 / error，判定逐条与
//     model.ParseRelationEndpoint 一致（零新造规则），且与 E13 互斥；
//  3. TestR3OpposingAsymmetricNormalized   —— 按 A-24 的 ID 字典序规范化后判方向不对称 → W15 / warning；
//  4. TestR3DuplicateNormalizedTriple      —— 规范化 (from,type,target) ≥ 2 条 → W16 / warning；
//  5. TestR3ReportOnlyNoAutoFix            —— 只报告零自动修：零 RepairSpec / 零写盘 / 入参不改 / 幂等；
//  6. TestR3RelationTypesStillEight        —— F4 关系类型仍恰 8 值封闭，Relation 仍恰 3 字段。
//
// 另加三组本 task 自守：与 R4 的 dangling_ref 零重复计数（反证形态双侧对称）、
// targets 去重升序可复算、四子检查 ↔ 诊断码单射与产出顺序。
//
// 全部用例只用**内存构造**的 query.ScanResult 快照，不建 vault、不读盘（唯一的读盘是
// 自守用例对本包源文件做的 grep 反证）—— 检查器是纯函数，这正是收益。

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 用例里用到的合法 ID（`k-` + 8 位 yyyymmdd + 非空 slug，逐字满足 model 的既有规则）。
const (
	kA = "k-20261123-alpha"
	kB = "k-20261123-beta"
	kC = "k-20261124-gamma"
	kZ = "k-20261125-zeta"
	// kGone 形态合法但库内不存在（E13 的判定面）。
	kGone = "k-20261199-gone"
)

// r3Rel 构造一条关系条目（type / target 逐字落盘，不做任何补默认）。
func r3Rel(typ model.RelationType, target string) model.Relation {
	return model.Relation{Type: typ, Target: model.RelationEndpoint(target), Reason: "单测事实"}
}

// r3Card 构造一张带任意类型关系的知识卡（路径由 ID 派生，保证扫描序稳定可复算）。
func r3Card(id string, rels ...model.Relation) query.CardEntry {
	return query.CardEntry{ID: id, Path: "domains/ai/knowledge/" + id + ".md", Relations: rels}
}

// r3ScanOf 把若干张卡折成扫描快照（本包不消费计数字段，按 query 侧口径填）。
func r3ScanOf(cards ...query.CardEntry) *query.ScanResult {
	return &query.ScanResult{Cards: cards, ScannedFiles: len(cards)}
}

// r3Of 跑 R3 检查项本体并逐条校验：四键 schema 合规 + **零 RepairSpec**（R3 只报告）。
func r3Of(t *testing.T, in Input) []Finding {
	t.Helper()
	fs, rs := checkR3Relation(in)
	if len(rs) != 0 {
		t.Fatalf("R3 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条：%+v", len(rs), rs)
	}
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		if got := NormalizeTargets(f.Targets); !reflect.DeepEqual(got, f.Targets) {
			t.Fatalf("targets 未归一化（须去重 + 字典序升序）：%v", f.Targets)
		}
	}
	return fs
}

// r3One 断言指定 check 恰 1 条并返回它（其余三个子检查的条数由调用方另行断言）。
func r3One(t *testing.T, fs []Finding, check string) Finding {
	t.Helper()
	got := pick(fs, check)
	if len(got) != 1 {
		t.Fatalf("%s 应恰 1 条，实得 %d 条：%+v", check, len(got), fs)
	}
	return got[0]
}

// r3Counts 返回四个子检查的条数（顺序 = 合同 §7 判定表行序）。
func r3Counts(fs []Finding) [R3SubcheckCount]int {
	var out [R3SubcheckCount]int
	for i, c := range R3Subchecks() {
		out[i] = len(pick(fs, c))
	}
	return out
}

// TestR3TargetMissing：关系 target 在 vault 内查无此对象 → E13 / error。
//
// 判定粒度是去重后的 `(from,target)`：同一张卡两条不同类型指向同一缺失目标是**一件**事实。
func TestR3TargetMissing(t *testing.T) {
	cases := []struct {
		name   string
		in     Input
		want   [R3SubcheckCount]int // E13 / E14 / W15 / W16
		target []string             // 期望的 targets（仅在 E13 恰 1 条时校验）
	}{
		{
			name: "target 指向不存在的卡 → 恰 1 条 E13",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kGone)),
			)},
			want:   [R3SubcheckCount]int{1, 0, 0, 0},
			target: []string{kA, kGone},
		},
		{
			name: "target 存在 → 四码全零",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kB)),
				r3Card(kB),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
		{
			name: "同卡两条不同类型指向同一缺失目标 → 仍恰 1 条（聚合，不按条目报）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kGone), r3Rel(model.RelationLimits, kGone)),
			)},
			want:   [R3SubcheckCount]int{1, 0, 0, 0},
			target: []string{kA, kGone},
		},
		{
			name: "两张卡各指向同一缺失目标 → 2 条（引用方不同即不同事实）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kGone)),
				r3Card(kB, r3Rel(model.RelationDerives, kGone)),
			)},
			want: [R3SubcheckCount]int{2, 0, 0, 0},
		},
		{
			name: "目标卡失效 / 已逻辑删除仍算存在（存在性只看落盘事实）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationOpposing, kZ)),
				func() query.CardEntry {
					c := r3Card(kZ, r3Rel(model.RelationOpposing, kA))
					c.Status, c.Deprecated = "deprecated", true
					c.DeletedAt, c.Deleted = "2026-11-24T10:00:00+08:00", true
					return c
				}(),
			)},
			// 两端各有一条 opposing → 同对两条记录 = W16（方向对称，故不产 W15）。
			want: [R3SubcheckCount]int{0, 0, 0, 1},
		},
		{
			name: "Scan 为 nil（未取数）→ 四码全零，不把「没取数」说成「不存在」",
			in:   Input{},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
		{
			name: "卡无任何关系条目 → 四码全零",
			in:   Input{Scan: r3ScanOf(r3Card(kA), r3Card(kB))},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := r3Of(t, c.in)
			if got := r3Counts(fs); got != c.want {
				t.Fatalf("四码条数 = %v，期望 %v：%+v", got, c.want, fs)
			}
			if c.want[0] != 1 {
				return
			}
			f := r3One(t, fs, CheckRelationTargetMissing)
			if f.Severity != SeverityError || f.Code() != CodeE13 {
				t.Fatalf("E13 的 severity / code 错位：%q / %q", f.Severity, f.Code())
			}
			if !reflect.DeepEqual(f.Targets, c.target) {
				t.Fatalf("E13 的 targets = %v，期望 %v", f.Targets, c.target)
			}
			if !strings.Contains(f.Detail, kGone) || strings.TrimSpace(f.Detail) == "" {
				t.Fatalf("E13 的 detail 未含足以复算的事实：%q", f.Detail)
			}
		})
	}
}

// TestR3PrefixInvalidReusesModelRule：target 的前缀 / 形态非法 → E14 / error。
//
// 两条硬要求：① 判定**逐条**与 model 的现成规则（ParseRelationEndpoint）一致，本包零新造
// 规则；② E14 与 E13 **互斥**——形态非法时不再判存在性，一件事只报一码。
//
// 端点合同（B2c）：合法端点是知识卡（`k-`）∪ 观点（`o-`）。因此形态合法但库内不存在的
// `k-` / `o-` 都走 E13（见末两例）；`n-` / `s-` / `r-` / `p-` / 畸形 / 空一律 E14。
func TestR3PrefixInvalidReusesModelRule(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		want    [R3SubcheckCount]int
		targets []string
	}{
		{"笔记 ID 当关系 target（n- 前缀）", "n-20261123-note",
			[R3SubcheckCount]int{0, 1, 0, 0}, []string{kA, "n-20261123-note"}},
		{"原文 ID 当关系 target（s- 前缀）", "s-20261123-src",
			[R3SubcheckCount]int{0, 1, 0, 0}, []string{kA, "s-20261123-src"}},
		{"S2 预留前缀（r-）不是合法端点", "r-20261123-recap",
			[R3SubcheckCount]int{0, 1, 0, 0}, []string{kA, "r-20261123-recap"}},
		{"提案前缀（p-）不是合法端点", "p-20261123-plan",
			[R3SubcheckCount]int{0, 1, 0, 0}, []string{kA, "p-20261123-plan"}},
		{"无前缀裸串", "gone", [R3SubcheckCount]int{0, 1, 0, 0}, []string{"gone", kA}},
		{"k- 但缺 yyyymmdd-slug 段", "k-a1", [R3SubcheckCount]int{0, 1, 0, 0}, []string{kA, "k-a1"}},
		{"o- 但缺 yyyymmdd-slug 段", "o-a1", [R3SubcheckCount]int{0, 1, 0, 0}, []string{kA, "o-a1"}},
		{"k- 但日期段非 8 位", "k-2026-alpha", [R3SubcheckCount]int{0, 1, 0, 0},
			[]string{"k-2026-alpha", kA}},
		{"k- 但 slug 段为空", "k-20261123-", [R3SubcheckCount]int{0, 1, 0, 0},
			[]string{"k-20261123-", kA}},
		{"空 target（形态问题归 R3，targets 只剩引用方）", "",
			[R3SubcheckCount]int{0, 1, 0, 0}, []string{kA}},
		{"k- 形态合法但库内不存在 → 走 E13，不走 E14（两码互斥）", kGone,
			[R3SubcheckCount]int{1, 0, 0, 0}, nil},
		{"o- 形态合法但库内不存在 → 同样走 E13（观点也是合法端点）", oGone,
			[R3SubcheckCount]int{1, 0, 0, 0}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Input{Scan: r3ScanOf(r3Card(kA, r3Rel(model.RelationSupports, c.target)))}
			fs := r3Of(t, in)
			if got := r3Counts(fs); got != c.want {
				t.Fatalf("四码条数 = %v，期望 %v：%+v", got, c.want, fs)
			}
			// ① 判定逐条与 model 的现成端点规则一致（零新造规则的等价性复算）。
			_, err := model.ParseRelationEndpoint(strings.TrimSpace(c.target))
			if ValidRelationTarget(c.target) != (err == nil) {
				t.Fatalf("ValidRelationTarget(%q) = %v，与 model.ParseRelationEndpoint 的判定不一致（err=%v）",
					c.target, ValidRelationTarget(c.target), err)
			}
			if c.want[1] != 1 {
				return
			}
			f := r3One(t, fs, CheckRelationPrefixInvalid)
			if f.Severity != SeverityError || f.Code() != CodeE14 {
				t.Fatalf("E14 的 severity / code 错位：%q / %q", f.Severity, f.Code())
			}
			if !reflect.DeepEqual(f.Targets, c.targets) {
				t.Fatalf("E14 的 targets = %v，期望 %v", f.Targets, c.targets)
			}
		})
	}
	// ② 源码级反证：前缀规则来自 model，本文件零正则、零前缀规则字面量表。
	src := nonTestSources(t)
	body, ok := src["r3_relation.go"]
	if !ok {
		t.Fatalf("未找到 r3_relation.go（实际文件集：%v）", keysOf(src))
	}
	if !strings.Contains(body, "internal/model") {
		t.Fatal("r3_relation.go 未引用 internal/model：前缀校验必须复用现成规则")
	}
	if !strings.Contains(body, "ParseRelationEndpoint") {
		t.Fatal("r3_relation.go 未复用 model.ParseRelationEndpoint：端点校验必须走现成入口")
	}
	for _, bad := range []string{"regexp", "MustCompile", "HasPrefix(\"k-\"", "HasPrefix(\"o-\"",
		"PrefixCard =", "PrefixOpinion ="} {
		if strings.Contains(body, bad) {
			t.Errorf("r3_relation.go 出现 %q：ID 端点规则不得在本包重造", bad)
		}
	}
}

// keysOf 返回 map 的键集合（只用于失败信息，判定不依赖顺序）。
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return NormalizeTargets(out)
}

// TestR3OpposingAsymmetricNormalized：`opposing` 方向不对称 → W15 / warning。
//
// 规范化口径逐字沿用 A-24：先按两端 ID 字典序把 `(from,target)` 归一，**较小端是唯一规范
// 写入端**（单向存储）。因此：
//
//	较小端 → 较大端 一条          = 规范形态，**不报**（否则每个正常 vault 都会误报）
//	较大端 → 较小端 且规范方向缺失 = W15
//	两个方向各一条                = 同对两条记录 → W16（不是「缺方向」）
func TestR3OpposingAsymmetricNormalized(t *testing.T) {
	// kA < kB（字典序），故 kA 是规范写入端。
	if !(kA < kB) {
		t.Fatalf("用例前提不成立：%s 应字典序小于 %s", kA, kB)
	}
	cases := []struct {
		name string
		in   Input
		want [R3SubcheckCount]int
	}{
		{
			name: "规范方向单条（较小端持有）→ 不报 W15",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationOpposing, kB)),
				r3Card(kB),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
		{
			name: "非规范方向单条（较大端持有）→ 恰 1 条 W15",
			in: Input{Scan: r3ScanOf(
				r3Card(kA),
				r3Card(kB, r3Rel(model.RelationOpposing, kA)),
			)},
			want: [R3SubcheckCount]int{0, 0, 1, 0},
		},
		{
			name: "两个方向各一条 → 不报 W15（属 W16 重复对）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationOpposing, kB)),
				r3Card(kB, r3Rel(model.RelationOpposing, kA)),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 1},
		},
		{
			name: "非规范方向但对端不在库内 → 只产 E13，不叠加 W15",
			in: Input{Scan: r3ScanOf(
				r3Card(kZ, r3Rel(model.RelationOpposing, kGone)),
			)},
			want: [R3SubcheckCount]int{1, 0, 0, 0},
		},
		{
			name: "对端形态非法 → 只产 E14，不叠加 W15",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationOpposing, "n-20261123-note")),
			)},
			want: [R3SubcheckCount]int{0, 1, 0, 0},
		},
		{
			name: "自反 opposing（两端同一张卡）→ 规范方向即自身，不报 W15",
			in:   Input{Scan: r3ScanOf(r3Card(kA, r3Rel(model.RelationOpposing, kA)))},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
		{
			name: "非 opposing 的单向边（supports / derives / limits）→ 一律不报 W15",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kB), r3Rel(model.RelationDerives, kC),
					r3Rel(model.RelationLimits, kZ)),
				r3Card(kB), r3Card(kC), r3Card(kZ),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := r3Of(t, c.in)
			if got := r3Counts(fs); got != c.want {
				t.Fatalf("四码条数 = %v，期望 %v：%+v", got, c.want, fs)
			}
			if c.want[2] != 1 {
				return
			}
			f := r3One(t, fs, CheckRelationOpposingAsymmetric)
			if f.Severity != SeverityWarning || f.Code() != CodeW15 {
				t.Fatalf("W15 的 severity / code 错位：%q / %q", f.Severity, f.Code())
			}
			// targets = 规范化后的 [较小端, 较大端]（等于合同 §2 的去重升序结果）。
			if !reflect.DeepEqual(f.Targets, []string{kA, kB}) {
				t.Fatalf("W15 的 targets = %v，期望规范化后的 [%s %s]", f.Targets, kA, kB)
			}
		})
	}
	// 规范化函数本身的口径表（与写侧「取小者为写入端」同源，顺序无关、幂等）。
	for _, p := range [][2]string{{kA, kB}, {kB, kA}} {
		small, large := NormalizeOpposingPair(p[0], p[1])
		if small != kA || large != kB {
			t.Fatalf("NormalizeOpposingPair(%q,%q) = (%q,%q)，期望 (%q,%q)",
				p[0], p[1], small, large, kA, kB)
		}
		s2, l2 := NormalizeOpposingPair(small, large)
		if s2 != small || l2 != large {
			t.Fatal("NormalizeOpposingPair 不幂等")
		}
	}
	if s, l := NormalizeOpposingPair(kA, kA); s != kA || l != kA {
		t.Fatalf("自反对的规范化应原样返回，实得 (%q,%q)", s, l)
	}
}

// TestR3DuplicateNormalizedTriple：规范化 `(from,type,target)` 出现 ≥ 2 条 → W16 / warning。
func TestR3DuplicateNormalizedTriple(t *testing.T) {
	cases := []struct {
		name    string
		in      Input
		want    [R3SubcheckCount]int
		targets []string
		detail  string // W16 的 detail 必须逐字包含的事实片段
	}{
		{
			name: "同卡同类型同目标两条 → 恰 1 条 W16",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kB), r3Rel(model.RelationSupports, kB)),
				r3Card(kB),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 1}, targets: []string{kA, kB},
			detail: "出现 2 条记录",
		},
		{
			name: "同卡不同类型同目标各一条 → 不重复（type 进判重键）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kB), r3Rel(model.RelationLimits, kB)),
				r3Card(kB),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
		{
			name: "同卡同目标同类型三条 → 仍恰 1 条 W16，条目数进 detail",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationDerives, kB), r3Rel(model.RelationDerives, kB),
					r3Rel(model.RelationDerives, kB)),
				r3Card(kB),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 1}, targets: []string{kA, kB},
			detail: "出现 3 条记录",
		},
		{
			name: "opposing 两个方向各一条 → 同对重复（方向无关的规范化判重）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationOpposing, kB)),
				r3Card(kB, r3Rel(model.RelationOpposing, kA)),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 1}, targets: []string{kA, kB},
			detail: "两个方向各有记录",
		},
		{
			name: "opposing 同一方向两条 → 恰 1 条 W16",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationOpposing, kB), r3Rel(model.RelationOpposing, kB)),
				r3Card(kB),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 1}, targets: []string{kA, kB},
			detail: "出现 2 条记录",
		},
		{
			name: "非 opposing 的反向边不算同一条（有向逐字比对）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kB)),
				r3Card(kB, r3Rel(model.RelationSupports, kA)),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 0},
		},
		{
			name: "重复条目的 target 不存在 → E13 与 W16 各 1 条（两件独立事实）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kGone), r3Rel(model.RelationSupports, kGone)),
			)},
			want: [R3SubcheckCount]int{1, 0, 0, 1}, targets: []string{kA, kGone},
			detail: "出现 2 条记录",
		},
		{
			name: "重复条目的 target 形态非法 → 只产 E14，不参与判重",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, "n-20261123-note"),
					r3Rel(model.RelationSupports, "n-20261123-note")),
			)},
			want: [R3SubcheckCount]int{0, 1, 0, 0},
		},
		{
			name: "自反重复（from == target）→ targets 去重成 1 个元素",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationSupports, kA), r3Rel(model.RelationSupports, kA)),
			)},
			want: [R3SubcheckCount]int{0, 0, 0, 1}, targets: []string{kA},
			detail: "出现 2 条记录",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := r3Of(t, c.in)
			if got := r3Counts(fs); got != c.want {
				t.Fatalf("四码条数 = %v，期望 %v：%+v", got, c.want, fs)
			}
			if c.want[3] != 1 {
				return
			}
			f := r3One(t, fs, CheckRelationDuplicate)
			if f.Severity != SeverityWarning || f.Code() != CodeW16 {
				t.Fatalf("W16 的 severity / code 错位：%q / %q", f.Severity, f.Code())
			}
			if !reflect.DeepEqual(f.Targets, c.targets) {
				t.Fatalf("W16 的 targets = %v，期望 %v", f.Targets, c.targets)
			}
			if !strings.Contains(f.Detail, c.detail) {
				t.Fatalf("W16 的 detail 未含 %q：%q", c.detail, f.Detail)
			}
		})
	}
}

// TestR3ReportOnlyNoAutoFix：只报告、零自动修 —— 零 RepairSpec、零写盘、入参不被改动、
// 同输入两次结果逐字相同，且源码级反证包内三条零（写盘 / 提交 / 子进程）。
func TestR3ReportOnlyNoAutoFix(t *testing.T) {
	dir := t.TempDir()
	before := dirSnapshot(t, dir)

	// 一份四种异常齐备的脏快照：E13 / E14 / W15 / W16 各恰 1 条。
	rels := []model.Relation{
		r3Rel(model.RelationSupports, kGone),           // E13
		r3Rel(model.RelationDerives, "n-20261123-bad"), // E14
		r3Rel(model.RelationLimits, kC),                // 干净边
		r3Rel(model.RelationLimits, kC),                // W16（同三元组两条）
	}
	in := R3ScanOf(dir, r3ScanOf(
		r3Card(kA, rels...),
		r3Card(kC),
		r3Card(kZ, r3Rel(model.RelationOpposing, kA)), // W15：kA < kZ，记录落在较大端
	))
	fs, rs := checkR3Relation(in)
	if len(rs) != 0 {
		t.Fatalf("R3 恒零 RepairSpec，实得 %d 条：%+v", len(rs), rs)
	}
	if got := r3Counts(fs); got != [R3SubcheckCount]int{1, 1, 1, 1} {
		t.Fatalf("四码条数 = %v，期望各恰 1 条：%+v", got, fs)
	}
	// 幂等：同一输入两次产出逐字相同（含顺序）。
	again, _ := checkR3Relation(in)
	if !reflect.DeepEqual(fs, again) {
		t.Fatalf("同一输入两次结果不同：%+v vs %+v", fs, again)
	}
	// 入参不被改动：关系条目数、type / target 逐字不变（不补反向、不去重、不移除）。
	if len(in.Scan.Cards[0].Relations) != len(rels) {
		t.Fatalf("入参 relations[] 条目数被改动：%d → %d", len(rels), len(in.Scan.Cards[0].Relations))
	}
	for i, r := range in.Scan.Cards[0].Relations {
		if r != rels[i] {
			t.Fatalf("入参第 %d 条关系被改写：%+v → %+v", i+1, rels[i], r)
		}
	}
	if after := dirSnapshot(t, dir); after != before {
		t.Fatalf("检查器产生了磁盘变化：%q → %q", before, after)
	}
	// 经注册表跑：R3 恰注册一次（四码各恰 1 条不变），且不产 RepairSpec。
	res := Run(in)
	if got := r3Counts(res.Findings); got != [R3SubcheckCount]int{1, 1, 1, 1} {
		t.Fatalf("Run 后四码条数 = %v，期望各恰 1 条（R3 不得重复注册）", got)
	}
	if len(res.Repairs) != 0 {
		t.Fatalf("R3 只报告：Run 的 Repairs 应为空，实得 %+v", res.Repairs)
	}
	if !res.HasError() {
		t.Fatal("E13 / E14 是 error 级：HasError 应为 true（A-31 退 2 的唯一判据来源）")
	}
	if R3 != "R3" {
		t.Fatalf("R3 编号常量 = %q", R3)
	}
	// 源码级反证：写盘 / 提交 / 子进程 / 关系写 API 一律零命中（Acceptance 的 grep 同口径）。
	body := nonTestSources(t)["r3_relation.go"]
	if body == "" {
		t.Fatal("未读到 r3_relation.go 源码")
	}
	for _, bad := range []string{"os.WriteFile", "os.Create", "os.Remove", "os.Rename",
		"os.OpenFile", "os" + "/exec", "exec.Command", "Add" + "Relation", "Remove" + "Relation"} {
		if strings.Contains(body, bad) {
			t.Errorf("r3_relation.go 出现 %q：R3 零写入零自动修", bad)
		}
	}
	// 四个 check 值逐字齐备且互不相同（Acceptance 的 sort -u 恰 4）。
	seen := map[string]bool{}
	for _, c := range R3Subchecks() {
		if !strings.Contains(body, c) {
			t.Errorf("r3_relation.go 未逐字出现 check 值 %q", c)
		}
		seen[c] = true
	}
	if len(seen) != R3SubcheckCount {
		t.Fatalf("四子检查的 check 值应互不相同，实得 %d 个", len(seen))
	}
}

// TestR3RelationTypesStillEight：F4 关系类型集合仍恰 8 值封闭 —— R3 不新增关系类型、
// 不新增关系字段（合同 §7「不改 F4」）。
func TestR3RelationTypesStillEight(t *testing.T) {
	if F4RelationValueCount != 8 {
		t.Fatalf("F4 关系类型封闭基数 = %d，冻结合同定死为 8", F4RelationValueCount)
	}
	vals := F4RelationValues()
	if len(vals) != F4RelationValueCount || len(NormalizeTargets(vals)) != F4RelationValueCount {
		t.Fatalf("F4 全集 = %v，应恰 %d 个互不相同的取值", vals, F4RelationValueCount)
	}
	// 三段构成：材料恰 3 + 论证恰 4 + 生命周期恰 1。
	if len(model.ValidMaterialRels()) != 3 || len(model.ValidRelationTypes()) != 4 {
		t.Fatalf("材料关系 %v / 论证关系 %v：应恰 3 值与 4 值",
			model.ValidMaterialRels(), model.ValidRelationTypes())
	}
	want := []string{"against", "context", "derives", "limits", "opposing",
		F4LifecycleRelation, "support", "supports"}
	if got := NormalizeTargets(vals); !reflect.DeepEqual(got, want) {
		t.Fatalf("F4 全集（升序）= %v，期望 %v", got, want)
	}
	// 集合外取值一律被 model 拒收（R3 不给任何新类型开口子）。
	for _, bad := range []string{"refines", "contradicts", "depends_on", "extends",
		F4LifecycleRelation, "Opposing", ""} {
		if _, err := model.ParseRelationType(bad); err == nil {
			t.Errorf("ParseRelationType(%q) 未报错：论证关系恰 4 值封闭", bad)
		}
	}
	// 关系条目仍恰三字段（不加墓碑位、不加第四个子字段；A-24 同口径）。
	rt := reflect.TypeOf(model.Relation{})
	if rt.NumField() != 3 {
		t.Fatalf("model.Relation 字段数 = %d，应恰 3（type / target / reason）", rt.NumField())
	}
	// 生命周期键 `replaced_by` 在知识卡上恰一个落点（它是字段键，不是论证关系取值）。
	ct, hits := reflect.TypeOf(model.Card{}), 0
	for i := 0; i < ct.NumField(); i++ {
		if strings.HasPrefix(ct.Field(i).Tag.Get("yaml"), F4LifecycleRelation) {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("知识卡上 %s 键的落点数 = %d，应恰 1", F4LifecycleRelation, hits)
	}
	// R3 只读关系条目的 type / target 两个事实，不引用任何新类型字面量。
	body := nonTestSources(t)["r3_relation.go"]
	for _, bad := range []string{"refines", "contradicts", "depends_on", "removed_at",
		"RelationID", "relation_id"} {
		if strings.Contains(body, bad) {
			t.Errorf("r3_relation.go 出现 %q：F4 集合与关系 schema 一格不动", bad)
		}
	}
}

// TestR3NoDoubleCountWithR4DanglingRef：与 R4 的 dangling_ref（E12）零重复计数 ——
// 关系条目的 target 问题只走 R3 的 E13 / E14，frontmatter 的两类引用只走 R4 的 E12，
// 两个判定面在集合上不相交（与 R4 侧 TestR4RelationTargetNotInDanglingRef 双侧对称）。
func TestR3NoDoubleCountWithR4DanglingRef(t *testing.T) {
	cases := []struct {
		name    string
		in      Input
		r3      [R3SubcheckCount]int
		wantE12 int
	}{
		{
			name: "关系 target 缺失：只产 R3 的 E13，R4 的 E12 恒 0",
			in:   Input{Scan: r3ScanOf(r3Card(kA, r3Rel(model.RelationSupports, kGone)))},
			r3:   [R3SubcheckCount]int{1, 0, 0, 0},
		},
		{
			name: "关系 target 前缀非法：只产 R3 的 E14，R4 的 E12 恒 0",
			in:   Input{Scan: r3ScanOf(r3Card(kA, r3Rel(model.RelationSupports, "n-20261123-x")))},
			r3:   [R3SubcheckCount]int{0, 1, 0, 0},
		},
		{
			name: "关系 target 为空串：只产 R3 的 E14，R4 的 E12 恒 0",
			in:   Input{Scan: r3ScanOf(r3Card(kA, r3Rel(model.RelationSupports, "")))},
			r3:   [R3SubcheckCount]int{0, 1, 0, 0},
		},
		{
			name: "笔记 source 悬空：只产 R4 的 E12，R3 四码恒 0（反向不相交）",
			in: Input{
				Scan: &query.ScanResult{
					Cards: []query.CardEntry{r3Card(kA, r3Rel(model.RelationSupports, kB)), r3Card(kB)},
					Notes: []query.NoteEntry{note("n-20261123-bad", "domains/ai/notes/n-bad.md",
						"s-20261199-gone")},
				},
				Sources: []SourceFact{{ID: "s-20261123-ok", Path: "sources/s-20261123-ok.md"}},
			},
			wantE12: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r3fs := r3Of(t, c.in)
			if got := r3Counts(r3fs); got != c.r3 {
				t.Fatalf("R3 四码条数 = %v，期望 %v：%+v", got, c.r3, r3fs)
			}
			r4fs, r4rs := checkR4Structure(c.in)
			if len(r4rs) != 0 {
				t.Fatalf("R4 恒零 RepairSpec，实得 %+v", r4rs)
			}
			if got := len(pick(r4fs, CheckDanglingRef)); got != c.wantE12 {
				t.Fatalf("R4 的 dangling_ref 条数 = %d，期望 %d：%+v", got, c.wantE12, r4fs)
			}
			// R4 一个 R3 码都不产；R3 一个 R4 码都不产（越界即红）。
			for _, r3c := range R3Subchecks() {
				if got := pick(r4fs, r3c); len(got) != 0 {
					t.Fatalf("R4 越界产出 R3 的 %s：%+v", r3c, got)
				}
			}
			for _, r4c := range []string{CheckDuplicateID, CheckDanglingRef, CheckOrphan,
				CheckGitUncommitted, CheckReviewedAtMissing, CheckDomainMoved,
				CheckRecapStale, CheckSupportInsufficient} {
				if got := pick(r3fs, r4c); len(got) != 0 {
					t.Fatalf("R3 越界产出 %s：%+v", r4c, got)
				}
			}
			// 同一条落盘事实不被两码各记一次：E12 与 E13 / E14 的 targets 集合不相交。
			for _, a := range append(pick(r3fs, CheckRelationTargetMissing),
				pick(r3fs, CheckRelationPrefixInvalid)...) {
				for _, b := range pick(r4fs, CheckDanglingRef) {
					if reflect.DeepEqual(a.Targets, b.Targets) {
						t.Fatalf("同一 targets 被 %s 与 %s 各记一次：%v", a.Check, b.Check, a.Targets)
					}
				}
			}
		})
	}
}

// TestR3NoDoubleCountWithR4DuplicateID：与 R4 的 duplicate_id（E11）零重复计数 ——
// 一个 ID 落在两个文件里时，两份文件的**镜像**关系条目**不得**被 W16 当成「重复关系对」
// 再记一次：那是同一件 ID 冲突事实的投影，只归 E11。
//
// 反面必须仍然判红（本用例同时锁住「别把判据放宽成永不报」）：同一份文件内真的写重了、
// 或同一对 `opposing` 两个方向各有记录，W16 照报。
func TestR3NoDoubleCountWithR4DuplicateID(t *testing.T) {
	// mirror 造「同 ID 两文件」：ID 相同、路径不同（R4 的 E11 判定面）。
	mirror := func(id, path string, rels ...model.Relation) query.CardEntry {
		return query.CardEntry{ID: id, Path: path, Relations: rels}
	}
	pA := "domains/ai/knowledge/" + kA + ".md"
	pB := "domains/ml/knowledge/" + kA + ".md"
	cases := []struct {
		name    string
		in      Input
		r3      [R3SubcheckCount]int
		wantE11 int
	}{
		{
			name: "同 ID 两文件各有同一条边：只产 R4 的 E11，W16 恒 0（镜像不是重复对）",
			in: Input{Scan: r3ScanOf(
				mirror(kA, pA, r3Rel(model.RelationSupports, kB)),
				mirror(kA, pB, r3Rel(model.RelationSupports, kB)),
				r3Card(kB),
			)},
			r3: [R3SubcheckCount]int{0, 0, 0, 0}, wantE11: 1,
		},
		{
			name: "同 ID 两文件各有同一条 opposing（同方向）：W15 / W16 均 0，只产 E11",
			in: Input{Scan: r3ScanOf(
				mirror(kA, pA, r3Rel(model.RelationOpposing, kZ)),
				mirror(kA, pB, r3Rel(model.RelationOpposing, kZ)),
				r3Card(kZ),
			)},
			r3: [R3SubcheckCount]int{0, 0, 0, 0}, wantE11: 1,
		},
		{
			name: "同 ID 两文件 + 其中一份自己写重了：W16 恰 1（同文件内重复照判）",
			in: Input{Scan: r3ScanOf(
				mirror(kA, pA, r3Rel(model.RelationSupports, kB), r3Rel(model.RelationSupports, kB)),
				mirror(kA, pB, r3Rel(model.RelationSupports, kB)),
				r3Card(kB),
			)},
			r3: [R3SubcheckCount]int{0, 0, 0, 1}, wantE11: 1,
		},
		{
			name: "无 ID 冲突：同一对 opposing 两个方向各有记录 → W16 恰 1（双向照判）",
			in: Input{Scan: r3ScanOf(
				r3Card(kA, r3Rel(model.RelationOpposing, kZ)),
				r3Card(kZ, r3Rel(model.RelationOpposing, kA)),
			)},
			r3: [R3SubcheckCount]int{0, 0, 0, 1}, wantE11: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r3fs := r3Of(t, c.in)
			if got := r3Counts(r3fs); got != c.r3 {
				t.Fatalf("R3 四码条数 = %v，期望 %v：%+v", got, c.r3, r3fs)
			}
			r4fs, r4rs := checkR4Structure(c.in)
			if len(r4rs) != 0 {
				t.Fatalf("R4 恒零 RepairSpec，实得 %+v", r4rs)
			}
			if got := len(pick(r4fs, CheckDuplicateID)); got != c.wantE11 {
				t.Fatalf("R4 的 duplicate_id 条数 = %d，期望 %d：%+v", got, c.wantE11, r4fs)
			}
			// 两侧都不越界产对方的码（与 E12 侧同型的双侧不相交反证）。
			for _, r3c := range R3Subchecks() {
				if got := pick(r4fs, r3c); len(got) != 0 {
					t.Fatalf("R4 越界产出 R3 的 %s：%+v", r3c, got)
				}
			}
			if got := pick(r3fs, CheckDuplicateID); len(got) != 0 {
				t.Fatalf("R3 越界产出 duplicate_id：%+v", got)
			}
		})
	}
}

// TestR3TargetsSortedDedupedAndOrdered：四码的 targets 恒去重升序，
// finding 产出顺序恒等于合同 §7 判定表行序（可逐字复算）。
func TestR3TargetsSortedDedupedAndOrdered(t *testing.T) {
	in := Input{Scan: r3ScanOf(
		// 逆序 / 重复输入：产出仍恒定（不受扫描序与条目序影响）。
		r3Card(kZ, r3Rel(model.RelationOpposing, kA), r3Rel(model.RelationSupports, kGone),
			r3Rel(model.RelationSupports, kGone), r3Rel(model.RelationDerives, "bad-id")),
		r3Card(kA),
	)}
	fs := r3Of(t, in)
	if got := r3Counts(fs); got != [R3SubcheckCount]int{1, 1, 1, 1} {
		t.Fatalf("四码条数 = %v，期望各恰 1 条：%+v", got, fs)
	}
	// 产出顺序 = E13 → E14 → W15 → W16。
	order := make([]string, 0, len(fs))
	for _, f := range fs {
		order = append(order, f.Check)
	}
	if !reflect.DeepEqual(order, R3Subchecks()) {
		t.Fatalf("产出顺序 = %v，期望合同 §7 行序 %v", order, R3Subchecks())
	}
	for _, f := range fs {
		if !reflect.DeepEqual(f.Targets, NormalizeTargets(f.Targets)) {
			t.Fatalf("%s 的 targets 未去重升序：%v", f.Check, f.Targets)
		}
		if len(f.Targets) == 0 {
			t.Fatalf("%s 的 targets 为空集合：至少要有引用方这一个可定位标识", f.Check)
		}
	}
	// W15 的 targets 恒是规范化后的 [较小端, 较大端]。
	if got := r3One(t, fs, CheckRelationOpposingAsymmetric).Targets; !reflect.DeepEqual(got,
		[]string{kA, kZ}) {
		t.Fatalf("W15 的 targets = %v，期望 [%s %s]", got, kA, kZ)
	}
	// 四子检查 ↔ 诊断码单射（顺序与 check.go 真源表逐字一致）。
	wantCodes := []string{CodeE13, CodeE14, CodeW15, CodeW16}
	for i, c := range R3Subchecks() {
		code, ok := CodeOf(c)
		if !ok || code != wantCodes[i] {
			t.Fatalf("%s ↔ %s 单射错位（实得 %q）", c, wantCodes[i], code)
		}
		back, ok := CheckOfCode(wantCodes[i])
		if !ok || back != c {
			t.Fatalf("诊断码 %s 反查得 %q，期望 %q", wantCodes[i], back, c)
		}
	}
	if len(R3Subchecks()) != R3SubcheckCount {
		t.Fatalf("R3 子检查数 = %d，应恰 %d", len(R3Subchecks()), R3SubcheckCount)
	}
}
