package reconcile

// R2（`reviewed_at` 补齐）只读判定的表驱动单测（M4 · T-…-051）。
//
// 五组（deliverables 逐字要求前两个用例名，其余三组是本 task 自守）：
//  1. TestR2ThreeConditionsAllTrue          —— 三条判定的**真值表**：八种组合里只有「三条同真」命中，
//     只满足两条的三行逐行不命中；证据②的两个析取项各自单独成立时都算命中；
//  2. TestR2ProposalsAndUnprocessedOutOfScope —— 范围排除：`proposals/**` 与 `unprocessed.md`
//     即便三条全真也恒不命中（判定第 1 条的路径面）；
//  3. TestR2TargetsSortedDeduped            —— `targets` 去重 + 升序、多对象按 (path, id) 可复算排序；
//  4. TestR2RepairSpecClosedKeys            —— RepairSpec 内容正确性：check / path / reason 与
//     `keys` **恰** {reviewed_at} 一个键，且它只是描述（无写方法、无句柄）；
//  5. TestR2SkipNoticeKeepsFinding          —— B3 跳过时 finding **仍在**：detail 追加固定文案，
//     check / severity / targets 一格不动。
//
// 全部用例只用**内存构造**的 query.ScanResult + git.Status + []EditFact 快照：不建 vault、
// 不读盘、不起子进程 —— R2 是纯函数，这正是收益。辅助函数 st / scanOf / pick 复用
// r1_git_test.go 与 r4_structure_test.go 已有的那份，不另抄一套。

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 判定第 3 条的两个时刻（固定字面量，便于逐字断言；格式与落盘口径一致）。
const (
	tsOld = "2026-11-20T08:00:00+08:00" // 早
	tsNew = "2026-11-21T09:30:00+08:00" // 晚
)

// rCard 造一张带两个时刻的知识卡（reviewed 为空 = frontmatter 缺该键）。
func rCard(id, path, updated, reviewed string) query.CardEntry {
	return query.CardEntry{ID: id, Path: path, UpdatedAt: updated, ReviewedAt: reviewed}
}

// rNote 造一篇带两个时刻的材料笔记（四类产物同构，字段名与语义与卡一致）。
func rNote(id, path, updated, reviewed string) query.NoteEntry {
	return query.NoteEntry{ID: id, Path: path, UpdatedAt: updated, ReviewedAt: reviewed}
}

// edits 把 (path, verb) 对折成 EditFact 快照（非 nil = 已采样）。
func edits(pairs ...[2]string) []EditFact {
	out := make([]EditFact, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, EditFact{Path: p[0], Verb: p[1]})
	}
	return out
}

// r2Of 跑 R2 一项（不经注册表，便于逐项断言），并逐条校验 finding 的四键 schema
// 与 RepairSpec 的形态；同时反证「finding 与 RepairSpec 一一对应」。
func r2Of(t *testing.T, in Input) ([]Finding, []RepairSpec) {
	t.Helper()
	fs, rs := checkR2ReviewedAt(in)
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		if f.Check != CheckReviewedAtMissing {
			t.Fatalf("R2 只产 %q，实得 %q", CheckReviewedAtMissing, f.Check)
		}
		if f.Severity != SeverityWarning {
			t.Fatalf("R2 的 severity 应恒 warning（分级取自 check.go 真源表），实得 %q", f.Severity)
		}
		if f.Code() != "W14" {
			t.Fatalf("R2 的诊断码应恒 W14（合同 §3 单射），实得 %q", f.Code())
		}
	}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			t.Fatalf("RepairSpec 不合形态：%v（%+v）", err, r)
		}
	}
	if len(fs) != len(rs) {
		t.Fatalf("命中对象的 finding 与 RepairSpec 必须一一对应，实得 %d / %d", len(fs), len(rs))
	}
	return fs, rs
}

// TestR2ThreeConditionsAllTrue：三条判定是**合取** —— 真值表逐行复算。
//
// 三条（合同 §5 逐字）：① 对象是知识卡或材料笔记且在对账域内；② 有用户直接编辑证据
// （未提交改动 **或** 最近一次提交的 verb 不属 `eg` 已知 verb）；③ `reviewed_at` 缺失
// 或早于 `updated_at`。表里既有「三条同真 → 恰 1 条 finding」的正向行，也有
// 「只满足两条 → 0 条」的三行反向行（每次只掀掉一条腿）。
func TestR2ThreeConditionsAllTrue(t *testing.T) {
	const cardPath = "domains/ai-infra/knowledge/k-20261120-a.md"
	const notePath = "domains/ai-infra/notes/n-20261120-b.md"

	cases := []struct {
		name string
		in   Input
		want int    // 期望命中条数
		hit  string // 期望命中路径（want=1 时校验）
		ev   string // 期望命中的证据串（want=1 时校验，单证据行）
		why  string // 期望的第 3 条形态
	}{
		{
			name: "三条同真 · 未提交改动 + reviewed_at 缺失 → 命中",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "")}, nil),
				Status: st([2]string{" M", cardPath})},
			want: 1, hit: cardPath, ev: EvidenceUncommitted, why: StaleReasonMissing,
		},
		{
			name: "三条同真 · 未提交改动 + reviewed_at 早于 updated_at → 命中",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, tsOld)}, nil),
				Status: st([2]string{" M", cardPath})},
			want: 1, hit: cardPath, ev: EvidenceUncommitted, why: StaleReasonBehind,
		},
		{
			name: "三条同真 · 外来 commit verb（已提交的外部编辑）+ reviewed_at 缺失 → 命中",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "")}, nil),
				Edits: edits([2]string{cardPath, "hotfix"})},
			want: 1, hit: cardPath, ev: EvidenceForeignVerb, why: StaleReasonMissing,
		},
		{
			name: "三条同真 · 材料笔记同样在判定第 1 条内 → 命中",
			in: Input{Scan: scanOf(nil, []query.NoteEntry{rNote("n-20261120-b", notePath, tsNew, "")}),
				Status: st([2]string{"??", notePath})},
			want: 1, hit: notePath, ev: EvidenceUncommitted, why: StaleReasonMissing,
		},
		{
			name: "只满足 ①③（无任何编辑证据：工作区干净、未采 Edits）→ 不命中",
			in:   Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "")}, nil)},
			want: 0,
		},
		{
			name: "只满足 ①③（最近一次提交的 verb 属 eg 已知 verb）→ 不命中",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "")}, nil),
				Edits: edits([2]string{cardPath, "process"})},
			want: 0,
		},
		{
			name: "只满足 ①②（reviewed_at 不早于 updated_at）→ 不命中",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsOld, tsNew)}, nil),
				Status: st([2]string{" M", cardPath})},
			want: 0,
		},
		{
			name: "只满足 ①②（两个时刻相等：不是「早于」）→ 不命中",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, tsNew)}, nil),
				Status: st([2]string{" M", cardPath})},
			want: 0,
		},
		{
			name: "只满足 ②③（对象不在扫描面：未扫描 → 空集合，不把「没扫描」当「没有对象」）",
			in:   Input{Status: st([2]string{" M", cardPath})},
			want: 0,
		},
		{
			name: "只满足 ②③（编辑证据落在别的路径上）→ 不命中",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "")}, nil),
				Status: st([2]string{" M", "domains/ai-infra/knowledge/k-20261120-z.md"})},
			want: 0,
		},
		{
			name: "诚实性 · updated_at 读不成时刻 → 无法比对，宁可不报（不命中）",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, "不是时刻", tsOld)}, nil),
				Status: st([2]string{" M", cardPath})},
			want: 0,
		},
		{
			name: "诚实性 · reviewed_at 读不成时刻 → 按缺失命中（读不出的过目信号不能当已过目）",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "坏值")}, nil),
				Status: st([2]string{" M", cardPath})},
			want: 1, hit: cardPath, ev: EvidenceUncommitted, why: StaleReasonMissing,
		},
		{
			name: "诚实性 · Edits 已采样但该路径 verb 为空串 → 不算外部编辑（不命中）",
			in: Input{Scan: scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "")}, nil),
				Edits: edits([2]string{cardPath, ""})},
			want: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs, rs := r2Of(t, c.in)
			if len(fs) != c.want {
				t.Fatalf("finding 条数 = %d，期望 %d（三条判定是合取）：%+v", len(fs), c.want, fs)
			}
			if c.want == 0 {
				if len(rs) != 0 {
					t.Fatalf("不命中却产出了修复意向：%+v", rs)
				}
				return
			}
			tg := ReviewedTargets(c.in)
			if len(tg) != c.want {
				t.Fatalf("ReviewedTargets 条数 = %d，期望 %d", len(tg), c.want)
			}
			if tg[0].Path != c.hit {
				t.Fatalf("命中路径 = %q，期望 %q", tg[0].Path, c.hit)
			}
			if !reflect.DeepEqual(tg[0].Evidence, []string{c.ev}) {
				t.Fatalf("证据 = %v，期望恰 [%s]", tg[0].Evidence, c.ev)
			}
			if tg[0].StaleReason != c.why {
				t.Fatalf("第 3 条形态 = %q，期望 %q", tg[0].StaleReason, c.why)
			}
			if !strings.Contains(fs[0].Detail, ReviewedKey()) {
				t.Fatalf("detail 未注明待写键 %s：%q", ReviewedKey(), fs[0].Detail)
			}
		})
	}

	// 证据是**封闭两值**：两条同时成立时去重升序并列，不产生第三种取值。
	both := Input{
		Scan:   scanOf([]query.CardEntry{rCard("k-20261120-a", cardPath, tsNew, "")}, nil),
		Status: st([2]string{" M", cardPath}),
		Edits:  edits([2]string{cardPath, "hotfix"}),
	}
	tg := ReviewedTargets(both)
	if len(tg) != 1 {
		t.Fatalf("两条证据同时成立仍应恰 1 条命中，实得 %d", len(tg))
	}
	if want := []string{EvidenceForeignVerb, EvidenceUncommitted}; !reflect.DeepEqual(tg[0].Evidence, want) {
		t.Fatalf("证据集合 = %v，期望 %v（封闭两值、去重升序）", tg[0].Evidence, want)
	}
	if EvidenceCount != 2 {
		t.Fatalf("证据基数 = %d，合同 §5 判定第 2 条恰两个析取项", EvidenceCount)
	}
}

// TestR2ProposalsAndUnprocessedOutOfScope：判定第 1 条的路径面 —— `proposals/**` 与
// `unprocessed.md` 恒不命中，哪怕另外两条判定全真（提案是控制面、收件区是条目）。
func TestR2ProposalsAndUnprocessedOutOfScope(t *testing.T) {
	out := []string{
		"proposals/p-20261120-a.md",
		"proposals/2026/p-20261120-b.md",
		"unprocessed.md",
		"domains/ai-infra/unprocessed.md",
	}
	for _, p := range out {
		t.Run("出范围 "+p, func(t *testing.T) {
			if InReviewedScope(p) {
				t.Fatalf("%q 不该在 R2 的对账域内", p)
			}
			in := Input{
				Scan:   scanOf([]query.CardEntry{rCard("k-20261120-a", p, tsNew, "")}, nil),
				Status: st([2]string{" M", p}),
				Edits:  edits([2]string{p, "hotfix"}),
			}
			fs, rs := r2Of(t, in)
			if len(fs) != 0 || len(rs) != 0 {
				t.Fatalf("出范围路径产出了 %d 条 finding / %d 条修复意向：%+v", len(fs), len(rs), fs)
			}
		})
	}

	in := []string{
		"domains/ai-infra/knowledge/k-20261120-a.md",
		"domains/ai-infra/notes/n-20261120-b.md",
	}
	for _, p := range in {
		if !InReviewedScope(p) {
			t.Fatalf("%q 应在 R2 的对账域内", p)
		}
	}
	// 空路径同样出范围（不把「没路径」当对象）。
	if InReviewedScope("  ") {
		t.Fatal("空路径不该在对账域内")
	}
}

// TestR2TargetsSortedDeduped：`targets` 去重 + 升序，多对象按 (path, id) 可复算排序。
func TestR2TargetsSortedDeduped(t *testing.T) {
	pa := "domains/ai-infra/knowledge/k-20261120-a.md"
	pb := "domains/ai-infra/knowledge/k-20261121-b.md"
	pn := "domains/ai-infra/notes/n-20261120-c.md"
	in := Input{
		Scan: scanOf(
			// 刻意乱序喂入：产出必须按路径升序。
			[]query.CardEntry{rCard("k-20261121-b", pb, tsNew, ""), rCard("k-20261120-a", pa, tsNew, tsOld)},
			[]query.NoteEntry{rNote("n-20261120-c", pn, tsNew, "")}),
		Status: st([2]string{" M", pb}, [2]string{" M", pa}, [2]string{"??", pn}),
	}
	fs, rs := r2Of(t, in)
	if len(fs) != 3 {
		t.Fatalf("三个对象各应恰 1 条 finding，实得 %d", len(fs))
	}
	wantPaths := []string{pa, pb, pn}
	for i, f := range fs {
		if len(f.Targets) != 2 {
			t.Fatalf("第 %d 条 targets 应恰 2 项（路径 + 对象 ID），实得 %v", i, f.Targets)
		}
		if f.Targets[0] != wantPaths[i] {
			t.Fatalf("第 %d 条 targets[0] = %q，期望 %q（按路径升序可复算）", i, f.Targets[0], wantPaths[i])
		}
		if f.Targets[0] >= f.Targets[1] {
			t.Fatalf("第 %d 条 targets 未按升序去重：%v", i, f.Targets)
		}
		if rs[i].Path != wantPaths[i] {
			t.Fatalf("第 %d 条修复意向 path = %q，期望 %q", i, rs[i].Path, wantPaths[i])
		}
	}
	// 同一 Input 跑两次逐字相同（纯函数、可复算）。
	fs2, rs2 := checkR2ReviewedAt(in)
	if !reflect.DeepEqual(fs, fs2) || !reflect.DeepEqual(rs, rs2) {
		t.Fatal("同一 Input 两次产出不一致（R2 必须是纯函数、可复算）")
	}
	// 「路径 + ID」两条 target 同值时（异常输入）仍去重：直接走 NewFinding 的归一化口径。
	f, err := NewFinding(CheckReviewedAtMissing, []string{pa, pa, " " + pa + " "}, "去重反证")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.Targets, []string{pa}) {
		t.Fatalf("targets 去重失效：%v", f.Targets)
	}
}

// TestR2RepairSpecClosedKeys：修复意向的内容正确性 —— `keys` **恰** {reviewed_at}。
//
// 这是「只写一键」的第一道反证（第二道在写入侧 internal/cli 的 diff 级用例）：
// 待写键表是**长度固定数组**，因此「顺手多写一个键」在编译期就加不进来。
func TestR2RepairSpecClosedKeys(t *testing.T) {
	const p = "domains/ai-infra/knowledge/k-20261120-a.md"
	in := Input{
		Scan:   scanOf([]query.CardEntry{rCard("k-20261120-a", p, tsNew, tsOld)}, nil),
		Status: st([2]string{" M", p}),
	}
	fs, rs := r2Of(t, in)
	if len(rs) != 1 {
		t.Fatalf("修复意向应恰 1 条，实得 %d", len(rs))
	}
	r := rs[0]
	if r.Check != CheckReviewedAtMissing {
		t.Fatalf("修复意向 check = %q", r.Check)
	}
	if r.Path != p {
		t.Fatalf("修复意向 path = %q，期望 %q", r.Path, p)
	}
	if !reflect.DeepEqual(r.Keys, []string{"reviewed_at"}) {
		t.Fatalf("修复意向 keys = %v，期望恰 [reviewed_at]（逐键封闭）", r.Keys)
	}
	if ReviewedKeyCount != 1 || len(ReviewedKeys()) != 1 || ReviewedKey() != "reviewed_at" {
		t.Fatalf("待写键集合不再是恰一个键：count=%d keys=%v key=%q",
			ReviewedKeyCount, ReviewedKeys(), ReviewedKey())
	}
	// ReviewedKeys 返回**副本**：调用方改不动封闭表（把它塞成两个键也影响不到下一次调用）。
	got := ReviewedKeys()
	got[0] = "status"
	if ReviewedKey() != "reviewed_at" {
		t.Fatal("ReviewedKeys 返回的不是副本：封闭表被外部改写了")
	}
	if strings.TrimSpace(r.Reason) == "" {
		t.Fatal("修复意向缺 reason")
	}
	// 与同一条 finding 同源同事实：两处都必须点出那个唯一的待写键。
	if !strings.Contains(r.Reason, ReviewedKey()) || !strings.Contains(fs[0].Detail, ReviewedKey()) {
		t.Fatalf("reason / detail 未点出待写键：%q / %q", r.Reason, fs[0].Detail)
	}
	// RepairSpec 只是**描述**：本包不提供任何写入能力（结构体零方法集里的写方法）。
	if got := reflect.TypeOf(r).NumMethod(); got != 1 {
		t.Fatalf("RepairSpec 的方法数 = %d，应恰 1（只有 Validate；无 Apply / Write / Commit）", got)
	}
}

// TestR2SkipNoticeKeepsFinding：B3 不豁免 —— 写入侧跳过时 finding **仍产出**，
// detail 追加固定文案，check / severity / targets 一格不动。
func TestR2SkipNoticeKeepsFinding(t *testing.T) {
	const p = "domains/ai-infra/knowledge/k-20261120-a.md"
	in := Input{
		Scan:   scanOf([]query.CardEntry{rCard("k-20261120-a", p, tsNew, "")}, nil),
		Status: st([2]string{" M", p}),
	}
	fs, _ := r2Of(t, in)
	if len(fs) != 1 {
		t.Fatalf("应恰 1 条 finding，实得 %d", len(fs))
	}
	base := fs[0]

	cases := []struct {
		name  string
		cause string
	}{
		{"带跳过原因", "content_hash_mismatch"},
		{"不带跳过原因", ""},
		{"原因是空白串（按不带处理）", "   "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := WithSkipNotice(base, c.cause)
			if err != nil {
				t.Fatalf("WithSkipNotice 失败：%v", err)
			}
			if got.Check != base.Check || got.Severity != base.Severity {
				t.Fatalf("跳过不得改变 check / severity：%q %q", got.Check, got.Severity)
			}
			if !reflect.DeepEqual(got.Targets, base.Targets) {
				t.Fatalf("跳过不得改变 targets：%v → %v", base.Targets, got.Targets)
			}
			if !strings.HasPrefix(got.Detail, base.Detail) {
				t.Fatal("跳过注记必须**追加**在原 detail 之后（不覆盖原事实）")
			}
			if !strings.Contains(got.Detail, ReviewedSkipNotice) {
				t.Fatalf("detail 未注明已跳过：%q", got.Detail)
			}
			if strings.TrimSpace(c.cause) != "" && !strings.Contains(got.Detail, strings.TrimSpace(c.cause)) {
				t.Fatalf("detail 未带跳过原因：%q", got.Detail)
			}
			// 入参不被改（纯函数）。
			if base.Detail == got.Detail {
				t.Fatal("WithSkipNotice 应返回新值且 detail 有增量")
			}
		})
	}
	if !strings.Contains(ReviewedSkipNotice, "file_changed") {
		t.Fatalf("跳过文案应逐字点出 kind=file_changed（封闭两值之一）：%q", ReviewedSkipNotice)
	}
}
