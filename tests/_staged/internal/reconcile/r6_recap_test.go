package reconcile

// R6 综述可能失准标记（`recap_stale` / W19）的表驱动单测（M4 · T-…-055 阶段 2）。
//
// 五组（deliverables 逐字要求，用例名不得改字）：
//  1. TestR6SingleTriggerOnly            —— M4 唯一触发条件的判定真值表（其余情形明确不命中）；
//  2. TestR6StaleReasonClosedThreeValues —— 理由恰封闭三值（真源在 model，第四值产不出来）；
//  3. TestR6FirstMatchOrderDeterministic —— 多因并存按固定顺序取第一个命中值（可复算）；
//  4. TestR6IdempotentZeroWrite          —— 已标记同理由时零写入零 commit，finding 仍产出；
//  5. TestR6NeverRecomputeRecap          —— 永不重算综述、永不自动清除标记。
//
// 另加四组本 task 自守（反证面，deliverables 之外的硬约束）：
//   - TestR6PackageZeroDiskWriteSHA256   —— 包内零写盘：跑检查器前后**磁盘 sha256 全等**；
//   - TestR6NoDoubleCountWithOtherChecks —— 与 R1–R5 / R7 逐条不重复计数；
//   - TestR6RecapsNotSampledNoJudgement  —— 事实未采样即不判（不把「没采样」当「不存在」）；
//   - TestR6WrittenKeySetClosed          —— 待写键集合恒封闭两键 + Finding 四键不放宽。
//
// 除零写盘那一组用 t.TempDir() 造了一个真实目录（用来证明检查器**不动**它），全部用例
// 只用**内存构造**的 query.ScanResult + RecapFact 快照，不建 vault、不读盘 —— 检查器是
// 纯函数，这正是收益。

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 用例常量：领域名、三张卡、一篇综述（ID 形态逐字满足 model 的既有规则，不新造）。
const (
	r6Domain = "ai-infra"

	r6CardA = "k-20261128-alpha"
	r6CardB = "k-20261128-beta"
	r6CardC = "k-20261128-gamma"
	r6Gone  = "k-20269999-gone"
	r6Recap = "r-20261128-weekly"

	// 三个时刻：早于综述 / 综述自身 / 晚于综述（RFC3339 带时区，口径同 model.ParseStamp）。
	r6TEarly = "2026-11-27T09:00:00+08:00"
	r6TRecap = "2026-11-28T09:00:00+08:00"
	r6TLate  = "2026-11-29T09:00:00+08:00"
)

// r6CardPath 拼出知识卡的 vault 相对路径（F1 骨架）。
func r6CardPath(id string) string {
	return domainsDirName + "/" + r6Domain + "/" + knowledgeDirName + "/" + id + ".md"
}

// r6RecapPath 拼出综述的 vault 相对路径（`reviews/` 分区，S2 的既有位置）。
func r6RecapPath(id string) string { return "reviews/" + id + ".md" }

// r6Card 造一张「活着且未更新」的知识卡（updated_at 逐字给到扫描面）。
func r6Card(id, updatedAt string) query.CardEntry {
	return query.CardEntry{
		ID: id, Path: r6CardPath(id), Domain: r6Domain,
		Status: "active", UpdatedAt: updatedAt,
	}
}

// r6Deprecated 把卡改成失效（状态维度；扫描面的失效位已展开）。
func r6Deprecated(c query.CardEntry) query.CardEntry {
	c.Status, c.Deprecated = "deprecated", true
	return c
}

// r6Deleted 把卡改成逻辑删除（删除维度两键 + 已展开的布尔位）。
func r6Deleted(c query.CardEntry) query.CardEntry {
	c.DeletedAt, c.DeletedReason, c.Deleted = r6TEarly, "用例造数", true
	return c
}

// r6DeletedKeyOnly 只给删除维度的键、不给展开的布尔位（反证「键非空即如实命中」）。
func r6DeletedKeyOnly(c query.CardEntry) query.CardEntry {
	c.DeletedAt = r6TEarly
	return c
}

// r6Fact 造一篇综述的采样事实（updated_at 恒取 r6TRecap，除专门指定的用例）。
func r6Fact(refs ...string) RecapFact {
	return RecapFact{
		ID: r6Recap, Path: r6RecapPath(r6Recap), UpdatedAt: r6TRecap, SourceCards: refs,
	}
}

// r6In 折出一份 R6 的输入（综述分区恒已采样，除专门反证未采样的那一组）。
func r6In(recaps []RecapFact, cards ...query.CardEntry) Input {
	scan := &query.ScanResult{Cards: cards, ScannedFiles: len(cards)}
	return R6ScanOf("", scan, recaps)
}

// r6Run 跑 R6 检查项本体并逐条校验产出的形状：
// finding 四键 schema 合规 + check 恒 recap_stale + severity 恒 warning + 码恒 W19 +
// targets 恰二元（路径 + 综述 ID，去重升序）+ detail 非空；
// RepairSpec 与 finding **一一对应** + 待写键恒是封闭两键 + path 指向综述 + reason 非空。
func r6Run(t *testing.T, in Input) ([]Finding, []RepairSpec) {
	t.Helper()
	fs, rs := checkR6RecapStale(in)
	if len(fs) != len(rs) {
		t.Fatalf("finding %d 条 / RepairSpec %d 条：R6 必须一一对应", len(fs), len(rs))
	}
	for i, f := range fs {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		if f.Check != CheckRecapStale {
			t.Fatalf("R6 只产 %s，实得 %q", CheckRecapStale, f.Check)
		}
		if f.Severity != SeverityWarning {
			t.Fatalf("%s 的 severity 必须逐字取自单射表（warning），实得 %q", f.Check, f.Severity)
		}
		if code, ok := CodeOf(f.Check); !ok || code != CodeW19 {
			t.Fatalf("%s 的诊断码必须恰 %s，实得 %q（ok=%v）", f.Check, CodeW19, code, ok)
		}
		if len(f.Targets) != 2 {
			t.Fatalf("targets 必须恰二元（[路径, 综述 ID]），实得 %v", f.Targets)
		}
		if !reflect.DeepEqual(f.Targets, NormalizeTargets(f.Targets)) {
			t.Fatalf("targets 必须去重 + 升序，实得 %v", f.Targets)
		}
		if strings.TrimSpace(f.Detail) == "" {
			t.Fatalf("detail 为空：%+v", f)
		}
		r := rs[i]
		if err := r.Validate(); err != nil {
			t.Fatalf("RepairSpec 不合规：%v（%+v）", err, r)
		}
		if r.Check != CheckRecapStale {
			t.Fatalf("RepairSpec.check = %q，期望 %s", r.Check, CheckRecapStale)
		}
		if !reflect.DeepEqual(r.Keys, RecapStaleKeys()) {
			t.Fatalf("待写键 = %v，必须恒是封闭两键 %v", r.Keys, RecapStaleKeys())
		}
		if !strings.HasSuffix(r.Path, ".md") || !strings.Contains(r.Path, r6Recap) {
			t.Fatalf("RepairSpec.path = %q，必须指向那篇综述", r.Path)
		}
		if strings.TrimSpace(r.Reason) == "" {
			t.Fatalf("RepairSpec.reason 为空：%+v", r)
		}
	}
	return fs, rs
}

// TestR6SingleTriggerOnly：M4 唯一触发条件的判定真值表 ——
// 只有「引用卡 updated_at 晚于综述」「引用卡逻辑删除」「引用卡失效」三条命中，
// 其余失准情形（语义漂移、覆盖不足等）一律**不命中**。
func TestR6SingleTriggerOnly(t *testing.T) {
	cases := []struct {
		name       string
		recapStamp string
		refs       []string
		cards      []query.CardEntry
		wantHit    bool
		wantReason model.StaleReason
	}{
		{
			name: "条件① 引用卡 updated_at 晚于综述 → 命中「已更新」",
			refs: []string{r6CardA}, cards: []query.CardEntry{r6Card(r6CardA, r6TLate)},
			wantHit: true, wantReason: model.StaleReasonUpdated,
		},
		{
			name: "条件① 引用卡 updated_at 与综述**相等** → 不命中（判据逐字是「晚于」）",
			refs: []string{r6CardA}, cards: []query.CardEntry{r6Card(r6CardA, r6TRecap)},
		},
		{
			name: "条件① 引用卡 updated_at 早于综述 → 不命中",
			refs: []string{r6CardA}, cards: []query.CardEntry{r6Card(r6CardA, r6TEarly)},
		},
		{
			name:    "条件② 引用卡逻辑删除（deleted_at 非空）→ 命中「已逻辑删除」",
			refs:    []string{r6CardA},
			cards:   []query.CardEntry{r6Deleted(r6Card(r6CardA, r6TEarly))},
			wantHit: true, wantReason: model.StaleReasonDeleted,
		},
		{
			name:    "条件② 只有 deleted_at 键、布尔位未展开 → 仍如实命中「已逻辑删除」",
			refs:    []string{r6CardA},
			cards:   []query.CardEntry{r6DeletedKeyOnly(r6Card(r6CardA, r6TEarly))},
			wantHit: true, wantReason: model.StaleReasonDeleted,
		},
		{
			name:    "条件② 引用卡失效（status: deprecated）→ 命中「已失效」",
			refs:    []string{r6CardA},
			cards:   []query.CardEntry{r6Deprecated(r6Card(r6CardA, r6TEarly))},
			wantHit: true, wantReason: model.StaleReasonDeprecated,
		},
		{
			name: "存在量词：三张卡里只有一张变了 → 照命中",
			refs: []string{r6CardA, r6CardB, r6CardC},
			cards: []query.CardEntry{
				r6Card(r6CardA, r6TEarly), r6Card(r6CardB, r6TEarly),
				r6Deprecated(r6Card(r6CardC, r6TEarly)),
			},
			wantHit: true, wantReason: model.StaleReasonDeprecated,
		},
		{
			name:  "全部引用卡都没动 → 不命中（综述标题 / 正文怎么改都不是判据：语义漂移不命中）",
			refs:  []string{r6CardA, r6CardB},
			cards: []query.CardEntry{r6Card(r6CardA, r6TEarly), r6Card(r6CardB, r6TEarly)},
		},
		{
			name: "覆盖不足①：引用集合为空 → 不命中（引用了 0 张不是失准判据）",
			refs: nil, cards: []query.CardEntry{r6Card(r6CardA, r6TLate)},
		},
		{
			name: "覆盖不足②：库里新增了**未被引用**的新卡 → 不命中（不看没引用的卡）",
			refs: []string{r6CardA},
			cards: []query.CardEntry{r6Card(r6CardA, r6TEarly), r6Card(r6CardB, r6TLate),
				r6Deprecated(r6Card(r6CardC, r6TLate))},
		},
		{
			name: "引用卡缺 updated_at → 条件① 不判（缺事实不当证据）",
			refs: []string{r6CardA}, cards: []query.CardEntry{r6Card(r6CardA, "")},
		},
		{
			name:       "综述缺 updated_at → 条件① 不判",
			recapStamp: " ",
			refs:       []string{r6CardA}, cards: []query.CardEntry{r6Card(r6CardA, r6TLate)},
		},
		{
			name:       "综述缺 updated_at 但引用卡已失效 → 条件② 照判（两条正交）",
			recapStamp: " ",
			refs:       []string{r6CardA},
			cards:      []query.CardEntry{r6Deprecated(r6Card(r6CardA, r6TLate))},
			wantHit:    true, wantReason: model.StaleReasonDeprecated,
		},
		{
			name:  "引用项形态非法 → 让位（不参与判定、也不由 R6 发码）",
			refs:  []string{"n-20261128-note", "k-bad", ""},
			cards: []query.CardEntry{r6Card(r6CardA, r6TLate)},
		},
		{
			name: "引用的卡不在库 → 让位（不把「查无此卡」当失准）",
			refs: []string{r6Gone}, cards: []query.CardEntry{r6Card(r6CardA, r6TLate)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rf := r6Fact(c.refs...)
			if c.recapStamp != "" {
				rf.UpdatedAt = strings.TrimSpace(c.recapStamp)
			}
			in := r6In([]RecapFact{rf}, c.cards...)
			fs, rs := r6Run(t, in)
			targets := RecapTargets(in)
			if !c.wantHit {
				if len(fs) != 0 || len(rs) != 0 || len(targets) != 0 {
					t.Fatalf("应不命中，实得 %d finding / %d repair / %d target：%+v",
						len(fs), len(rs), len(targets), fs)
				}
				return
			}
			if len(fs) != 1 || len(targets) != 1 {
				t.Fatalf("应恰 1 条 W19，实得 %d finding / %d target：%+v", len(fs), len(targets), fs)
			}
			if got := targets[0].Reason; got != c.wantReason {
				t.Fatalf("stale_reason = %q，期望 %q", got, c.wantReason)
			}
			if !strings.Contains(fs[0].Detail, string(c.wantReason)) {
				t.Fatalf("detail 未如实写出取值 %q：%q", c.wantReason, fs[0].Detail)
			}
			if !reflect.DeepEqual(fs[0].Targets,
				NormalizeTargets([]string{r6RecapPath(r6Recap), r6Recap})) {
				t.Fatalf("targets = %v", fs[0].Targets)
			}
		})
	}
}

// TestR6StaleReasonClosedThreeValues：`stale_reason` 恰封闭三值 ——
// 取值与顺序的真源恒是 model 的封闭枚举，本包不另造第二份字面量；第四个取值产不出来。
func TestR6StaleReasonClosedThreeValues(t *testing.T) {
	want := []model.StaleReason{"引用卡已更新", "引用卡已逻辑删除", "引用卡已失效"}
	if got := RecapStaleReasons(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RecapStaleReasons() = %v，期望逐字 %v（顺序 = 合同 §9）", got, want)
	}
	if len(want) != RecapStaleReasonCount || len(model.ValidStaleReasons()) != RecapStaleReasonCount {
		t.Fatalf("封闭基数被改：期望恰 %d", RecapStaleReasonCount)
	}
	// 真源转手而非第二份声明：本包非测试源里**一个取值字面量都没有**（拼接构造，不自命中）。
	for _, lit := range []string{`"` + "引用卡已" + `更新"`, `"` + "引用卡已逻辑" + `删除"`,
		`"` + "引用卡已" + `失效"`} {
		for name, body := range nonTestSources(t) {
			if strings.Contains(body, lit) {
				t.Fatalf("%s 手写了取值字面量 %s：三值只许有 model 一份真源", name, lit)
			}
		}
	}
	// 三值逐个都能被判定产出（映上满射），且每个取值都通过 model 的封闭校验。
	produced := map[model.StaleReason]bool{}
	for _, tc := range []struct {
		reason model.StaleReason
		card   query.CardEntry
	}{
		{model.StaleReasonUpdated, r6Card(r6CardA, r6TLate)},
		{model.StaleReasonDeleted, r6Deleted(r6Card(r6CardA, r6TEarly))},
		{model.StaleReasonDeprecated, r6Deprecated(r6Card(r6CardA, r6TEarly))},
	} {
		got := RecapTargets(r6In([]RecapFact{r6Fact(r6CardA)}, tc.card))
		if len(got) != 1 || got[0].Reason != tc.reason {
			t.Fatalf("期望产出 %q，实得 %+v", tc.reason, got)
		}
		if _, err := model.ParseStaleReason(string(got[0].Reason)); err != nil {
			t.Fatalf("产出的取值 %q 未通过 model 的封闭校验：%v", got[0].Reason, err)
		}
		produced[tc.reason] = true
	}
	if len(produced) != RecapStaleReasonCount {
		t.Fatalf("只产出了 %d 种取值，三值必须逐个可达", len(produced))
	}
	// 第四个取值：任何输入都产不出表外理由（判定只遍历真源三值）。
	for _, c := range []query.CardEntry{
		r6Card(r6CardA, r6TLate), r6Deleted(r6Card(r6CardA, r6TLate)),
		r6Deprecated(r6Card(r6CardA, r6TLate)), r6Card(r6CardA, "not-a-stamp"),
	} {
		for _, tg := range RecapTargets(r6In([]RecapFact{r6Fact(r6CardA)}, c)) {
			if !tg.Reason.Valid() {
				t.Fatalf("产出了表外理由 %q", tg.Reason)
			}
			for _, h := range tg.Hits {
				if !h.Reason.Valid() {
					t.Fatalf("命中因里出现表外理由 %q", h.Reason)
				}
			}
		}
	}
	// 让位原因同样封闭三值（detail 侧的口径，不是新 check、不是新码）。
	if got := RecapYieldReasons(); len(got) != RecapYieldReasonCount {
		t.Fatalf("让位原因 = %v，期望恰 %d 值", got, RecapYieldReasonCount)
	}
	for _, bad := range []string{"", " ", "ref_stale", "REF_MISSING"} {
		if IsKnownRecapYieldReason(bad) {
			t.Fatalf("IsKnownRecapYieldReason(%q) = true：必须封闭", bad)
		}
	}
}

// TestR6FirstMatchOrderDeterministic：多因并存时按 已更新 → 已逻辑删除 → 已失效 的固定顺序
// 取**第一个**命中值 —— 与卡的扫描序、引用书写序、命中卡张数**逐字无关**（可复算）。
func TestR6FirstMatchOrderDeterministic(t *testing.T) {
	updated := r6Card(r6CardA, r6TLate)                   // 只命中 ①
	deleted := r6Deleted(r6Card(r6CardB, r6TEarly))       // 只命中 ②-删除
	deprecated := r6Deprecated(r6Card(r6CardC, r6TEarly)) // 只命中 ②-失效
	cases := []struct {
		name       string
		refs       []string
		cards      []query.CardEntry
		wantReason model.StaleReason
		wantHits   []model.StaleReason
	}{
		{
			name:       "三因并存（各由不同卡命中）→ 取第一个：已更新",
			refs:       []string{r6CardC, r6CardB, r6CardA}, // 引用书写序刻意逆着来
			cards:      []query.CardEntry{deprecated, deleted, updated},
			wantReason: model.StaleReasonUpdated,
			wantHits: []model.StaleReason{model.StaleReasonUpdated,
				model.StaleReasonDeleted, model.StaleReasonDeprecated},
		},
		{
			name: "删除 + 失效并存，无更新 → 取已逻辑删除",
			refs: []string{r6CardC, r6CardB}, cards: []query.CardEntry{deprecated, deleted},
			wantReason: model.StaleReasonDeleted,
			wantHits: []model.StaleReason{model.StaleReasonDeleted,
				model.StaleReasonDeprecated},
		},
		{
			name:       "同一张卡三因全中 → 取已更新",
			refs:       []string{r6CardA},
			cards:      []query.CardEntry{r6Deprecated(r6Deleted(r6Card(r6CardA, r6TLate)))},
			wantReason: model.StaleReasonUpdated,
			wantHits: []model.StaleReason{model.StaleReasonUpdated,
				model.StaleReasonDeleted, model.StaleReasonDeprecated},
		},
		{
			name:       "同一张卡：删除 + 失效 → 取已逻辑删除",
			refs:       []string{r6CardB},
			cards:      []query.CardEntry{r6Deprecated(r6Deleted(r6Card(r6CardB, r6TEarly)))},
			wantReason: model.StaleReasonDeleted,
			wantHits: []model.StaleReason{model.StaleReasonDeleted,
				model.StaleReasonDeprecated},
		},
		{
			name: "只有失效一因 → 取已失效",
			refs: []string{r6CardC}, cards: []query.CardEntry{deprecated},
			wantReason: model.StaleReasonDeprecated,
			wantHits:   []model.StaleReason{model.StaleReasonDeprecated},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := r6In([]RecapFact{r6Fact(c.refs...)}, c.cards...)
			got := RecapTargets(in)
			if len(got) != 1 {
				t.Fatalf("应恰 1 篇命中，实得 %d", len(got))
			}
			if got[0].Reason != c.wantReason {
				t.Fatalf("取值 = %q，期望 %q", got[0].Reason, c.wantReason)
			}
			var hits []model.StaleReason
			for _, h := range got[0].Hits {
				hits = append(hits, h.Reason)
			}
			if !reflect.DeepEqual(hits, c.wantHits) {
				t.Fatalf("命中因序列 = %v，期望 %v（顺序 = 封闭三值声明顺序）", hits, c.wantHits)
			}
			if got[0].Reason != got[0].Hits[0].Reason {
				t.Fatalf("取值不等于第一个命中因：%q vs %q", got[0].Reason, got[0].Hits[0].Reason)
			}
			// 换扫描序 / 换引用序：结论逐字不变（可复算）。
			shuffled := append([]query.CardEntry{}, c.cards...)
			sort.SliceStable(shuffled, func(i, j int) bool {
				return shuffled[i].ID > shuffled[j].ID
			})
			refs := append([]string{}, c.refs...)
			sort.Strings(refs)
			again := RecapTargets(r6In([]RecapFact{r6Fact(refs...)}, shuffled...))
			if !reflect.DeepEqual(got, again) {
				t.Fatalf("换序后结论变了：%+v ≠ %+v", got, again)
			}
		})
	}
}

// TestR6IdempotentZeroWrite：落盘已是同一标记与同一理由时 —— **零写入、零 commit**，
// 但 finding 仍如实产出；理由不同则如实按新取值给修复意向（仍不清除、不重算）。
func TestR6IdempotentZeroWrite(t *testing.T) {
	card := r6Deprecated(r6Card(r6CardA, r6TEarly))
	base := r6Fact(r6CardA)

	// ① 未标记：finding + RepairSpec 各 1 条，且未标记时 detail 不带幂等注记。
	fs, rs := r6Run(t, r6In([]RecapFact{base}, card))
	if len(fs) != 1 || len(rs) != 1 {
		t.Fatalf("未标记时应恰 1 finding + 1 repair，实得 %d / %d", len(fs), len(rs))
	}
	if strings.Contains(fs[0].Detail, RecapIdempotentNotice) {
		t.Fatalf("未标记却带了幂等注记：%q", fs[0].Detail)
	}

	// ② 已标记且理由相同：finding 仍产出，且如实注明零写入零 commit。
	marked := base
	marked.Marked, marked.MarkedReason = true, string(model.StaleReasonDeprecated)
	in := r6In([]RecapFact{marked}, card)
	fs2, rs2 := r6Run(t, in)
	if len(fs2) != 1 || len(rs2) != 1 {
		t.Fatalf("已标记时 finding 仍必须产出，实得 %d / %d", len(fs2), len(rs2))
	}
	tg := RecapTargets(in)
	if len(tg) != 1 || !tg[0].AlreadyMarked {
		t.Fatalf("AlreadyMarked 应为 true：%+v", tg)
	}
	if !strings.Contains(fs2[0].Detail, RecapIdempotentNotice) {
		t.Fatalf("已标记时 detail 必须注明零写入零 commit：%q", fs2[0].Detail)
	}
	if !reflect.DeepEqual(rs2[0].Keys, RecapStaleKeys()) {
		t.Fatalf("待写键集合恒两键，实得 %v", rs2[0].Keys)
	}

	// ③ 已标记但理由**不同**（落盘是旧理由）：按新取值如实给意向，不算幂等。
	stalePair := base
	stalePair.Marked, stalePair.MarkedReason = true, string(model.StaleReasonUpdated)
	tg3 := RecapTargets(r6In([]RecapFact{stalePair}, card))
	if len(tg3) != 1 || tg3[0].AlreadyMarked {
		t.Fatalf("理由不同不得算幂等：%+v", tg3)
	}
	if tg3[0].Reason != model.StaleReasonDeprecated {
		t.Fatalf("取值应按本次事实重算为 %q，实得 %q",
			model.StaleReasonDeprecated, tg3[0].Reason)
	}

	// ④ 同一输入连跑三次逐字相同（纯函数 + 可复算）。
	first, firstRepairs := checkR6RecapStale(in)
	for i := 0; i < 2; i++ {
		again, againRepairs := checkR6RecapStale(in)
		if !reflect.DeepEqual(first, again) || !reflect.DeepEqual(firstRepairs, againRepairs) {
			t.Fatalf("第 %d 次重跑结果不同：%+v / %+v", i+2, again, againRepairs)
		}
	}
	// ⑤ 入参未被改动（检查器不改调用方的切片内容）。
	if in.Recaps[0].Marked != true || in.Recaps[0].MarkedReason !=
		string(model.StaleReasonDeprecated) || len(in.Recaps[0].SourceCards) != 1 {
		t.Fatalf("入参 Recaps 被检查器改动：%+v", in.Recaps[0])
	}
}

// TestR6NeverRecomputeRecap：永不重算综述、永不自动清除 `stale` —— 四条反证。
func TestR6NeverRecomputeRecap(t *testing.T) {
	// ① 结构反证：RecapFact 只有可复算的落盘事实字段，**没有正文 / 原始字节 / 文档句柄**，
	//    「重算一篇综述」在本包内不可表达。
	typ := reflect.TypeOf(RecapFact{})
	wantFields := map[string]reflect.Kind{
		"ID": reflect.String, "Path": reflect.String, "UpdatedAt": reflect.String,
		"SourceCards": reflect.Slice, "Marked": reflect.Bool, "MarkedReason": reflect.String,
	}
	if typ.NumField() != len(wantFields) {
		t.Fatalf("RecapFact 字段数 = %d，期望恰 %d（多一个字段就可能把正文带进只读域）",
			typ.NumField(), len(wantFields))
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		want, ok := wantFields[f.Name]
		if !ok || f.Type.Kind() != want {
			t.Fatalf("RecapFact 字段 %s（%s）不在允许集合内：只许纯数据事实", f.Name, f.Type)
		}
	}
	if typ.NumMethod() != 0 {
		t.Fatalf("RecapFact 挂了 %d 个方法：只读事实不得有任何动作", typ.NumMethod())
	}
	// ② 源码反证：本包非测试源不出现任何「重算 / 重新生成综述」的动作符号与正文读取口。
	src := readSourceFile(t, "r6_recap.go")
	for _, bad := range []string{"os." + "WriteFile", "os." + "Create", "os." + "Remove",
		"Commit" + "(", "exec.", "os" + "/exec", ".Body()", "Recompute", "Regenerate",
		"Rebuild", "Clear" + "Stale"} {
		if strings.Contains(src, bad) {
			t.Fatalf("r6_recap.go 出现 %q：R6 只读、永不重算综述", bad)
		}
	}
	// ③ 行为反证：不命中时**零产出** —— 不产生任何「把标记去掉」的意向。
	//    落盘上带着旧标记、但本次全部引用卡都没变：R6 不报、也不建议清除。
	stale := r6Fact(r6CardA)
	stale.Marked, stale.MarkedReason = true, string(model.StaleReasonUpdated)
	in := r6In([]RecapFact{stale}, r6Card(r6CardA, r6TEarly))
	fs, rs := r6Run(t, in)
	if len(fs) != 0 || len(rs) != 0 {
		t.Fatalf("已标记 + 本次无触发因时必须零产出（不得自动清除），实得 %d / %d：%+v",
			len(fs), len(rs), fs)
	}
	// ④ 待写键与值域反证：命中时也只写那两个键、值只落在「标记 + 封闭三值」上，
	//    正文与其余 frontmatter 不在待写集合内。
	hit, hitRepairs := r6Run(t, r6In([]RecapFact{r6Fact(r6CardA)},
		r6Deprecated(r6Card(r6CardA, r6TEarly))))
	if len(hitRepairs) != 1 {
		t.Fatalf("命中应恰 1 条修复意向，实得 %d", len(hitRepairs))
	}
	for _, forbidden := range []string{"title", "body", "tags", "summary", "updated_at",
		"status", "deleted_at", "reviewed_at", "source_cards"} {
		for _, k := range hitRepairs[0].Keys {
			if k == forbidden {
				t.Fatalf("待写键集合出现 %q：R6 只写失准标记那两键", forbidden)
			}
		}
	}
	if !strings.Contains(hit[0].Detail, "不重算综述") ||
		!strings.Contains(hit[0].Detail, "不清除标记") {
		t.Fatalf("detail 必须如实写明边界：%q", hit[0].Detail)
	}
}

// TestR6PackageZeroDiskWriteSHA256：包内零写盘 —— 跑检查器前后**磁盘 sha256 全等**。
func TestR6PackageZeroDiskWriteSHA256(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "reviews"), 0o755); err != nil {
		t.Fatalf("造数失败：%v", err)
	}
	recap := filepath.Join(dir, "reviews", r6Recap+".md")
	body := "---\nid: " + r6Recap + "\nupdated_at: " + r6TRecap + "\n---\n综述正文\n"
	if err := os.WriteFile(recap, []byte(body), 0o644); err != nil {
		t.Fatalf("造数失败：%v", err)
	}
	before := r6TreeSHA256(t, dir)

	in := r6In([]RecapFact{r6Fact(r6CardA, r6CardB)},
		r6Card(r6CardA, r6TLate), r6Deprecated(r6Card(r6CardB, r6TEarly)))
	in.VaultRoot = dir
	fs, rs := r6Run(t, in)
	if len(fs) != 1 || len(rs) != 1 {
		t.Fatalf("应恰 1 finding + 1 repair，实得 %d / %d", len(fs), len(rs))
	}
	if after := r6TreeSHA256(t, dir); after != before {
		t.Fatalf("检查器产生了磁盘变化：sha256 %s → %s", before, after)
	}
	// 综述文件逐字未变：既没写标记键，也没重排 frontmatter。
	raw, err := os.ReadFile(recap)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	if string(raw) != body {
		t.Fatalf("综述文件被改动：%q", string(raw))
	}
	// 走注册表整体跑一遍，磁盘仍逐字不变（R6 入册没引入任何写口）。
	if got := countCheck(Run(in).Findings, CheckRecapStale); got != 1 {
		t.Fatalf("注册表里 W19 应恰 1 条，实得 %d", got)
	}
	if after := r6TreeSHA256(t, dir); after != before {
		t.Fatalf("Run 产生了磁盘变化：sha256 %s → %s", before, after)
	}
}

// r6TreeSHA256 把目录内容（相对路径 + 文件字节）折成一个 sha256 十六进制串。
func r6TreeSHA256(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		h.Write([]byte(filepath.ToSlash(rel) + "\n"))
		if info.IsDir() {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		h.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 %s 失败：%v", dir, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestR6NoDoubleCountWithOtherChecks：与 R1–R5 / R7 逐条不重复计数 ——
// 同一件落盘事实不许两个码各记一次，各码只报自己判定面上的那一件事实。
func TestR6NoDoubleCountWithOtherChecks(t *testing.T) {
	// ① 与 R4 的 duplicate_id（E11）：被引用的卡 ID 落在两个文件 → E11 报，W19 让位为 0
	//    （两份文件上的 updated_at / 删除维度可能分叉，「哪份是事实」不可复算）。
	dupA := r6Card(r6CardA, r6TLate)
	dupB := r6Card(r6CardA, r6TLate)
	dupB.Path = domainsDirName + "/ml/" + knowledgeDirName + "/" + r6CardA + ".md"
	res := Run(r6In([]RecapFact{r6Fact(r6CardA)}, dupA, dupB))
	if got := countCheck(res.Findings, CheckDuplicateID); got < 1 {
		t.Fatalf("同 ID 多文件应由 %s 报，实得 %d 条：%+v", CheckDuplicateID, got, res.Findings)
	}
	if got := countCheck(res.Findings, CheckRecapStale); got != 0 {
		t.Fatalf("该事实已由 E11 独家承载，W19 应恰 0 条，实得 %d 条", got)
	}
	// ①' 反向（防「让位」被放宽成「永不报」）：ID 唯一时同样的卡照报 W19 恰 1 条。
	res = Run(r6In([]RecapFact{r6Fact(r6CardA)}, dupA))
	if got := countCheck(res.Findings, CheckRecapStale); got != 1 {
		t.Fatalf("ID 唯一时 W19 应恰 1 条，实得 %d 条：%+v", got, res.Findings)
	}

	// ② 与 R3 的 relation_prefix_invalid / relation_target_missing（E14 / E13）：
	//    两码的判定面恰是知识卡的 relations[]，R6 的判定面恰是综述的取材卡清单 ——
	//    集合不相交。这里让同一张卡既命中 R6 的触发因，又带一条形态非法的关系条目：
	//    E14 恰 1 条、W19 恰 1 条，两条说的是两件不同的落盘事实（不是同一件被记两次）。
	bad := r6Deprecated(r6Card(r6CardA, r6TEarly))
	bad.Relations = []model.Relation{{
		Type: model.RelationSupports, Target: model.CardID("n-20261128-note"), Reason: "用例造数",
	}}
	res = Run(r6In([]RecapFact{r6Fact(r6CardA)}, bad))
	if got := countCheck(res.Findings, CheckRelationPrefixInvalid); got != 1 {
		t.Fatalf("形态非法的关系条目应恰 1 条 %s，实得 %d 条：%+v",
			CheckRelationPrefixInvalid, got, res.Findings)
	}
	if got := countCheck(res.Findings, CheckRecapStale); got != 1 {
		t.Fatalf("失效的引用卡应恰 1 条 W19，实得 %d 条", got)
	}
	for _, f := range onlyCheck(res.Findings, CheckRecapStale) {
		if strings.Contains(f.Detail, "relations") {
			t.Fatalf("W19 的 detail 不该指向关系面：%q", f.Detail)
		}
	}
	// ②' 综述取材卡清单上的形态非法项：R6 只让位、**不发码**，R3 也不会替它发码
	//     （那不在 relations[] 上）—— 于是同一条引用项在全表里恰 0 个码，不存在重复计数。
	res = Run(r6In([]RecapFact{r6Fact("n-20261128-note")}, r6Card(r6CardA, r6TLate)))
	if got := countCheck(res.Findings, CheckRecapStale); got != 0 {
		t.Fatalf("引用项形态非法不得被 R6 当成失准，实得 %d 条 W19", got)
	}
	if got := countCheck(res.Findings, CheckRelationPrefixInvalid); got != 0 {
		t.Fatalf("综述取材清单不在 R3 判定面内，E14 应恰 0 条，实得 %d 条", got)
	}

	// ③ 与 R1（W13）/ R2（W14）/ R5（W18）/ R7（W20）：R6 的产出与 Git 未提交状态、
	//    编辑事实、rename 事实、`sources/` 采样面**逐字无关** —— 把它们全加满，
	//    R6 的结果一字不变（各码各报自己那一件事实，互不吞并）。
	card := r6Deprecated(r6Card(r6CardA, r6TEarly))
	base := r6In([]RecapFact{r6Fact(r6CardA)}, card)
	want, wantRepairs := r6Run(t, base)
	if len(want) != 1 {
		t.Fatalf("基线应恰 1 条 W19，实得 %d 条", len(want))
	}
	rich := base
	rich.Status = git.Status{Changes: []git.Change{{Code: " M", Path: r6CardPath(r6CardA)}}}
	rich.Edits = []EditFact{{Path: r6CardPath(r6CardA), Verb: "vim"}}
	rich.Renames = []RenameFact{{
		OldPath: domainsDirName + "/ml/" + knowledgeDirName + "/" + r6CardA + ".md",
		NewPath: r6CardPath(r6CardA), Verb: "vim",
	}}
	rich.Sources = []SourceFact{}
	got, gotRepairs := r6Run(t, rich)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotRepairs, wantRepairs) {
		t.Fatalf("R6 读了别人的事实面：%+v / %+v", got, gotRepairs)
	}
	res = Run(rich)
	if n := countCheck(res.Findings, CheckRecapStale); n != 1 {
		t.Fatalf("W19 应恰 1 条，实得 %d 条：%+v", n, res.Findings)
	}
	if n := countCheck(res.Findings, CheckGitUncommitted); n != 1 {
		t.Fatalf("R1 应照报 1 条 W13，实得 %d 条", n)
	}
	if n := countCheck(res.Findings, CheckSupportInsufficient); n != 1 {
		t.Fatalf("R7 应照报 1 条 W20（该卡零 support），实得 %d 条", n)
	}
	// ④ 与 R4 的 orphan（W17）并存不算重复：W17 说的是那张**知识卡**的关系面，
	//    W19 说的是那篇**综述**的失准标记 —— 判定对象类不同、targets 不同。
	if n := countCheck(res.Findings, CheckOrphan); n != 1 {
		t.Fatalf("零关系卡应照报 1 条 W17，实得 %d 条", n)
	}
	for _, f := range onlyCheck(res.Findings, CheckOrphan) {
		if f.Targets[0] == r6Recap || strings.Contains(f.Targets[0], "reviews/") {
			t.Fatalf("W17 落到了综述上：%v", f.Targets)
		}
	}
	// ⑤ 修复意向侧不重复：R2 与 R6 是两个产 RepairSpec 的检查项，按 check 与 path 分治，
	//    同一条 (check, path) 不出现两次，且两者的待写键集合互不相交。
	seen := map[string]bool{}
	for _, r := range res.Repairs {
		key := r.Check + "\x00" + r.Path
		if seen[key] {
			t.Fatalf("同一 (check, path) 出现两条修复意向：%+v", r)
		}
		seen[key] = true
	}
	for _, r6r := range onlyRepairs(res.Repairs, CheckRecapStale) {
		for _, k := range r6r.Keys {
			for _, other := range ReviewedKeys() {
				if k == other {
					t.Fatalf("R6 与 R2 的待写键相交于 %q：写入面必须分治", k)
				}
			}
		}
	}
}

// TestR6RecapsNotSampledNoJudgement：事实未采样即不判 ——
// 绝不把「没采样」当成「原文不存在」或「引用集合为空」。
func TestR6RecapsNotSampledNoJudgement(t *testing.T) {
	card := r6Deprecated(r6Card(r6CardA, r6TLate))
	// ① 综述分区未采样（Recaps == nil）：整体不判定。
	notSampled := R6ScanOf("", &query.ScanResult{Cards: []query.CardEntry{card}}, nil)
	if fs, rs := r6Run(t, notSampled); len(fs) != 0 || len(rs) != 0 {
		t.Fatalf("未采样必须零产出，实得 %d / %d", len(fs), len(rs))
	}
	if got := RecapTargets(notSampled); got != nil {
		t.Fatalf("未采样时 RecapTargets 应为 nil，实得 %+v", got)
	}
	// ② 已采样但分区为空（Recaps == []）：同样零产出，但这是「确实没有综述」而非「没采样」。
	empty := R6ScanOf("", &query.ScanResult{Cards: []query.CardEntry{card}}, []RecapFact{})
	if fs, _ := r6Run(t, empty); len(fs) != 0 {
		t.Fatalf("空分区应零产出，实得 %d 条", len(fs))
	}
	// ③ 未扫描（Scan == nil）：拿不到任何引用卡事实 → 不判定（不猜「卡都没变」）。
	noScan := Input{Recaps: []RecapFact{r6Fact(r6CardA)}}
	if fs, rs := r6Run(t, noScan); len(fs) != 0 || len(rs) != 0 {
		t.Fatalf("未扫描必须零产出，实得 %d / %d", len(fs), len(rs))
	}
	// ④ 采样了、也扫描了 → 照判（证明上面三条不是「永不报」）。
	if fs, _ := r6Run(t, r6In([]RecapFact{r6Fact(r6CardA)}, card)); len(fs) != 1 {
		t.Fatalf("两侧事实齐备时应恰 1 条 W19，实得 %d 条", len(fs))
	}
	// ⑤ 综述缺 ID 或缺路径：无法定位，不发 finding（同样不猜）。
	for _, bad := range []RecapFact{
		{Path: r6RecapPath(r6Recap), UpdatedAt: r6TRecap, SourceCards: []string{r6CardA}},
		{ID: r6Recap, UpdatedAt: r6TRecap, SourceCards: []string{r6CardA}},
	} {
		if fs, _ := r6Run(t, r6In([]RecapFact{bad}, card)); len(fs) != 0 {
			t.Fatalf("缺 ID / 缺路径的综述不得产出 finding：%+v", bad)
		}
	}
	// ⑥ 同一篇综述被采样两次：只判一次（不产生第二条 finding）。
	twice := r6Fact(r6CardA)
	if fs, _ := r6Run(t, r6In([]RecapFact{twice, twice}, card)); len(fs) != 1 {
		t.Fatalf("同一份落盘文件应只判一次，实得 %d 条", len(fs))
	}
}

// TestR6WrittenKeySetClosed：待写键集合恒封闭两键（编译期数组 + 运行期断言），
// 且 Finding 的封闭四键与 check 十二值封闭枚举一字不放宽。
func TestR6WrittenKeySetClosed(t *testing.T) {
	keys := RecapStaleKeys()
	if len(keys) != RecapStaleKeyCount {
		t.Fatalf("待写键 = %v，期望恰 %d 个", keys, RecapStaleKeyCount)
	}
	if !reflect.DeepEqual(keys, NormalizeTargets(keys)) {
		t.Fatalf("待写键必须去重 + 升序，实得 %v", keys)
	}
	if !reflect.DeepEqual(keys, []string{model.FMKeyStale, model.FMKeyStaleReason}) {
		t.Fatalf("待写键必须逐字取自 model 的键名常量，实得 %v", keys)
	}
	// 编译期封闭：真源表是长度固定的数组，第三个键加不进来。
	tt := reflect.TypeOf(recapStaleKeyTable)
	if tt.Kind() != reflect.Array || tt.Len() != RecapStaleKeyCount {
		t.Fatalf("recapStaleKeyTable 类型 = %s，必须是长度恰 %d 的数组", tt, RecapStaleKeyCount)
	}
	// 副本语义：改返回值不污染真源。
	keys[0] = "tampered"
	if RecapStaleKeys()[0] == "tampered" {
		t.Fatal("RecapStaleKeys() 返回的不是副本")
	}
	// Finding 四键与 check 封闭枚举不放宽（R6 入册后仍恰 12 值）。
	if !reflect.DeepEqual(FindingKeys(), []string{"check", "severity", "targets", "detail"}) {
		t.Fatalf("finding 键集合被改：%v", FindingKeys())
	}
	if len(AllChecks()) != CheckCount {
		t.Fatalf("check 枚举 = %d 值，必须恒 %d", len(AllChecks()), CheckCount)
	}
	spec, ok := SpecOf(CheckRecapStale)
	if !ok || spec.Code != CodeW19 || spec.R != R6 || spec.Severity != SeverityWarning {
		t.Fatalf("recap_stale 的四元组 = %+v（ok=%v），期望 W19 / R6 / warning", spec, ok)
	}
}

// onlyRepairs 过滤出某个 check 的修复意向（顺序保持）。
func onlyRepairs(rs []RepairSpec, check string) []RepairSpec {
	out := make([]RepairSpec, 0, len(rs))
	for _, r := range rs {
		if r.Check == check {
			out = append(out, r)
		}
	}
	return out
}
