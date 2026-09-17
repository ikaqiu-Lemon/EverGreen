package reconcile

// R7 材料支持不足实时判定（`support_insufficient` / W20）的表驱动单测（M4 · T-…-056）。
//
// 五组（deliverables 逐字要求，用例名不得改字）：
//  1. TestR7ZeroEffectiveSupport               —— 零有效 support 即命中（判定真值表）；
//  2. TestR7EffectivenessExcludesDeletedAndMissing —— 对端逻辑删除 / 缺失不计入有效；
//  3. TestR7NeverTouchesStatus                 —— 永不改 status（源码级 + 用例级双反证）；
//  4. TestR7NoOnDiskMarker                     —— 零落盘标记（零写盘 / 零 RepairSpec）；
//  5. TestR7DisjointFromDeleteSupportCheck     —— 与 M3 删除路径那张材料建议清单路径分治。
//
// 另加四组本 task 自守：与 R4 的 `dangling_ref`（E12 第 ② 类）零重复计数（正反两向）、
// 与 R4 的 `orphan`（W17）「两个不同 frontmatter 键」的并存不算重复、与 R1 / R2 / R3 / R5
// 零重复计数（同一份输入里各记各自那一件事实）、`sources/` 分区未采样时整体不判定
// （诚实性口径：不把缺事实当证据）。
//
// 全部用例只用**内存构造**的 query.ScanResult + SourceFact 快照，不建 vault、不读盘
// （唯一的读盘是三组反证用例对本包源文件做的 grep）—— 检查器是纯函数，这正是收益。

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 用例常量：领域名与对象 ID（ID 形态逐字满足 model 的既有规则，不新造）。
const (
	r7Domain = "ai-infra"

	r7Card  = "k-20261127-alpha"
	r7Card2 = "k-20261127-beta"
	r7Note  = "n-20261127-attention"
	r7Note2 = "n-20261127-second"
	r7Src   = "s-20261127-attention"
	r7Src2  = "s-20261127-second"
	r7Gone  = "n-20269999-gone"
)

// r7CardPath / r7NotePath / r7SourcePath 拼出三类对象的 vault 相对路径（F1 骨架）。
func r7CardPath(id string) string {
	return domainsDirName + "/" + r7Domain + "/" + knowledgeDirName + "/" + id + ".md"
}

func r7NotePath(id string) string {
	return domainsDirName + "/" + r7Domain + "/" + notesDirName + "/" + id + ".md"
}

func r7SourcePath(id string) string { return "sources/" + id + ".md" }

// r7Ref 造一条材料关系条目（rel 取值一律走 internal/model 的封闭三值）。
func r7Ref(source, note string, rel model.MaterialRel) model.SourceRef {
	return model.SourceRef{
		Source: model.SourceID(source), Note: model.NoteID(note),
		Rel: rel, Reason: "用例造数：该结论由这份材料" + string(rel),
	}
}

// r7CardEntry 造一张知识卡（status 逐字给到扫描面，用于反证「状态不参与判定」）。
func r7CardEntry(id, status string, refs ...model.SourceRef) query.CardEntry {
	return query.CardEntry{
		ID: id, Path: r7CardPath(id), Domain: r7Domain, Status: status,
		Deprecated: status == "deprecated", Sources: refs,
	}
}

// r7NoteEntry 造一篇材料笔记（deleted 为 true 即「已逻辑删除」）。
func r7NoteEntry(id, source string, deleted bool) query.NoteEntry {
	n := query.NoteEntry{ID: id, Path: r7NotePath(id), Domain: r7Domain, Source: source}
	if deleted {
		n.Deleted, n.DeletedAt = true, "2026-11-27T10:00:00+08:00"
	}
	return n
}

// r7SourceFacts 造 `sources/` 分区的采样快照（非 nil = 已采样）。
func r7SourceFacts(ids ...string) []SourceFact {
	out := make([]SourceFact, 0, len(ids))
	for _, id := range ids {
		out = append(out, SourceFact{ID: id, Path: r7SourcePath(id)})
	}
	return out
}

// r7In 折出一份 R7 的输入（`sources/` 分区恒已采样，除专门反证未采样的那一组）。
func r7In(cards []query.CardEntry, notes []query.NoteEntry, sources []SourceFact) Input {
	scan := &query.ScanResult{Cards: cards, Notes: notes,
		ScannedFiles: len(cards) + len(notes)}
	return R7ScanOf("", scan, sources)
}

// r7Of 跑 R7 检查项本体并逐条校验：四键 schema 合规 + **零 RepairSpec**（R7 只报告）
// + targets 恰一元且非空 + check 恒是 support_insufficient + severity 恒取自单射表。
func r7Of(t *testing.T, in Input) []Finding {
	t.Helper()
	fs, rs := checkR7SupportInsufficient(in)
	if len(rs) != 0 {
		t.Fatalf("R7 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条：%+v", len(rs), rs)
	}
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		if f.Check != CheckSupportInsufficient {
			t.Fatalf("R7 只产 %s，实得 %q", CheckSupportInsufficient, f.Check)
		}
		if f.Severity != SeverityWarning {
			t.Fatalf("%s 的 severity 必须逐字取自单射表（warning），实得 %q", f.Check, f.Severity)
		}
		if code, ok := CodeOf(f.Check); !ok || code != CodeW20 {
			t.Fatalf("%s 的诊断码必须恰 %s，实得 %q（ok=%v）", f.Check, CodeW20, code, ok)
		}
		if len(f.Targets) != 1 || strings.TrimSpace(f.Targets[0]) == "" {
			t.Fatalf("targets 必须恰一元且非空（[知识卡 ID]），实得 %v", f.Targets)
		}
		if strings.TrimSpace(f.Detail) == "" {
			t.Fatalf("detail 为空：%+v", f)
		}
	}
	return fs
}

// r7IDs 取出命中卡的 ID 集合（升序，可逐字复算）。
func r7IDs(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Targets[0])
	}
	return NormalizeTargets(out)
}

// TestR7ZeroEffectiveSupport：判定真值表 —— 有效 support 数为 0 即命中，≥ 1 即不命中。
//
// 覆盖：零 support 条目、只有 against / context（不是支持）、有一条有效、
// 一条有效 + 一条无效（仍不命中）、卡自身已逻辑删除（不判）、重复 ID（让位 E11）。
func TestR7ZeroEffectiveSupport(t *testing.T) {
	notes := []query.NoteEntry{
		r7NoteEntry(r7Note, r7Src, false),
		r7NoteEntry(r7Note2, r7Src2, true), // 已逻辑删除的对端
	}
	sources := r7SourceFacts(r7Src, r7Src2)
	cases := []struct {
		name      string
		card      query.CardEntry
		wantHit   bool // SupportFact.Insufficient()：有效 support 数是否为 0
		declared  int
		effective int
		yields    bool // 无效原因是否被 E12 独家承载 → W20 让位（finding 不落地）
	}{
		{"零 support 条目", r7CardEntry(r7Card, "active"), true, 0, 0, false},
		{"只有 against", r7CardEntry(r7Card, "active",
			r7Ref(r7Src, r7Note, model.MaterialAgainst)), true, 0, 0, false},
		{"只有 context", r7CardEntry(r7Card, "active",
			r7Ref(r7Src, r7Note, model.MaterialContext)), true, 0, 0, false},
		{"一条有效 support", r7CardEntry(r7Card, "active",
			r7Ref(r7Src, r7Note, model.MaterialSupport)), false, 1, 1, false},
		{"一条有效 + 一条对端已删除", r7CardEntry(r7Card, "active",
			r7Ref(r7Src, r7Note, model.MaterialSupport),
			r7Ref(r7Src2, r7Note2, model.MaterialSupport)), false, 2, 1, false},
		{"全部 support 的对端已删除", r7CardEntry(r7Card, "active",
			r7Ref(r7Src2, r7Note2, model.MaterialSupport)), true, 1, 0, false},
		{"四要素不全（note 为空）", r7CardEntry(r7Card, "active",
			r7Ref(r7Src, "", model.MaterialSupport)), true, 1, 0, false},
		// 原文端缺失：SupportFact 仍判无效（Insufficient()==true），但该事实由 E12 第 ③ 类
		// 独家承载（I-…-019 修复），W20 整体让位不落地 —— 逐字锁死「同一件事不两码」。
		{"原文端缺失（让位 E12）", r7CardEntry(r7Card, "active",
			r7Ref("s-20269999-gone", r7Note, model.MaterialSupport)), true, 1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := r7In([]query.CardEntry{c.card}, notes, sources)
			facts := SupportFacts(in)
			if len(facts) != 1 {
				t.Fatalf("应恰一条支持面事实，实得 %d 条：%+v", len(facts), facts)
			}
			if facts[0].Declared != c.declared || facts[0].Effective != c.effective {
				t.Fatalf("声明 / 有效条数 = %d / %d，期望 %d / %d（%+v）",
					facts[0].Declared, facts[0].Effective, c.declared, c.effective, facts[0])
			}
			if facts[0].Insufficient() != c.wantHit {
				t.Fatalf("Insufficient() = %v，期望 %v（%+v）",
					facts[0].Insufficient(), c.wantHit, facts[0])
			}
			// W20 落地 = 有效数为 0 **且**不让位给 E12。
			wantFinding := c.wantHit && !c.yields
			fs := r7Of(t, in)
			if got := len(fs) == 1; got != wantFinding {
				t.Fatalf("finding 命中 = %v（%d 条），期望 %v", got, len(fs), wantFinding)
			}
			if wantFinding && fs[0].Targets[0] != c.card.ID {
				t.Fatalf("targets 应恰 [%s]，实得 %v", c.card.ID, fs[0].Targets)
			}
		})
	}
	// 卡自身已逻辑删除 → 整体不判（删除维度，与状态三值无关）。
	deleted := r7CardEntry(r7Card, "active")
	deleted.Deleted, deleted.DeletedAt = true, "2026-11-27T09:00:00+08:00"
	if fs := r7Of(t, r7In([]query.CardEntry{deleted}, notes, sources)); len(fs) != 0 {
		t.Fatalf("已逻辑删除的卡不该被判定材料支撑，实得 %d 条：%+v", len(fs), fs)
	}
	// 同一卡 ID 落在两个文件 → 让位 R4 的 duplicate_id（E11 独家承载）。
	dup1 := r7CardEntry(r7Card, "active")
	dup2 := r7CardEntry(r7Card, "active")
	dup2.Path = domainsDirName + "/ml/" + knowledgeDirName + "/" + r7Card + ".md"
	if fs := r7Of(t, r7In([]query.CardEntry{dup1, dup2}, notes, sources)); len(fs) != 0 {
		t.Fatalf("同 ID 多文件应整体让位 E11，实得 %d 条：%+v", len(fs), fs)
	}
	// `sources/` 分区未采样（nil）→ 整体不判定：绝不把「没采样」当成「原文不存在」。
	if fs := r7Of(t, r7In([]query.CardEntry{r7CardEntry(r7Card, "active")}, notes, nil)); len(fs) != 0 {
		t.Fatalf("原文分区未采样时必须整体不判定，实得 %d 条：%+v", len(fs), fs)
	}
	if facts := SupportFacts(r7In(nil, nil, nil)); facts != nil {
		t.Fatalf("Scan / Sources 皆未给出时应返回空集合，实得 %+v", facts)
	}
}

// TestR7EffectivenessExcludesDeletedAndMissing：有效性判定逐条排除
// 「对端已逻辑删除」与「对端缺失」，并把原因归到封闭四值之一。
func TestR7EffectivenessExcludesDeletedAndMissing(t *testing.T) {
	if len(SupportIneffectiveReasons()) != SupportIneffectiveReasonCount {
		t.Fatalf("无效原因必须恰 %d 值，实得 %v",
			SupportIneffectiveReasonCount, SupportIneffectiveReasons())
	}
	if IsKnownSupportIneffectiveReason("note_maybe_gone") {
		t.Fatal("第五个无效原因取值必须被拒（封闭四值）")
	}
	notes := []query.NoteEntry{
		r7NoteEntry(r7Note, r7Src, false),
		r7NoteEntry(r7Note2, r7Src2, true),
	}
	cases := []struct {
		name      string
		ref       model.SourceRef
		sources   []SourceFact
		wantWhy   string
		wantValid bool
	}{
		{"对端齐全且未删除 → 有效", r7Ref(r7Src, r7Note, model.MaterialSupport),
			r7SourceFacts(r7Src), "", true},
		{"来源笔记已逻辑删除 → 无效", r7Ref(r7Src2, r7Note2, model.MaterialSupport),
			r7SourceFacts(r7Src2), SupportIneffectiveNoteDeleted, false},
		{"来源笔记缺失 → 无效", r7Ref(r7Src, r7Gone, model.MaterialSupport),
			r7SourceFacts(r7Src), SupportIneffectiveNoteMissing, false},
		{"原文缺失 → 无效", r7Ref("s-20269999-gone", r7Note, model.MaterialSupport),
			r7SourceFacts(r7Src), SupportIneffectiveSourceMissing, false},
		{"source 端为空 → 无效", r7Ref("", r7Note, model.MaterialSupport),
			r7SourceFacts(r7Src), SupportIneffectiveEndpointIncomplete, false},
		{"note 端为空 → 无效", r7Ref(r7Src, "", model.MaterialSupport),
			r7SourceFacts(r7Src), SupportIneffectiveEndpointIncomplete, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := r7In([]query.CardEntry{r7CardEntry(r7Card, "active", c.ref)}, notes, c.sources)
			facts := SupportFacts(in)
			if len(facts) != 1 || facts[0].Declared != 1 {
				t.Fatalf("应恰一条支持面事实且声明 1 条 support，实得 %+v", facts)
			}
			f := facts[0]
			if c.wantValid {
				if f.Effective != 1 || len(f.Ineffective) != 0 {
					t.Fatalf("应恰 1 条有效、零无效原因，实得 %+v", f)
				}
				return
			}
			if f.Effective != 0 {
				t.Fatalf("有效条数应为 0，实得 %d（%+v）", f.Effective, f)
			}
			if !reflect.DeepEqual(f.Ineffective, []string{c.wantWhy}) {
				t.Fatalf("无效原因应恰 [%s]，实得 %v", c.wantWhy, f.Ineffective)
			}
			if !IsKnownSupportIneffectiveReason(c.wantWhy) {
				t.Fatalf("原因 %q 不在封闭四值内", c.wantWhy)
			}
		})
	}
	// 「重复 ID 里的一份被删」不等于对端消失：另一份仍在 → 该 support 仍有效。
	half := []query.NoteEntry{r7NoteEntry(r7Note, r7Src, true), r7NoteEntry(r7Note, r7Src, false)}
	in := r7In([]query.CardEntry{r7CardEntry(r7Card, "active",
		r7Ref(r7Src, r7Note, model.MaterialSupport))}, half, r7SourceFacts(r7Src))
	if facts := SupportFacts(in); len(facts) != 1 || facts[0].Effective != 1 {
		t.Fatalf("至少一份对端未删除时该 support 仍有效，实得 %+v", facts)
	}
}

// TestR7NeverTouchesStatus：永不改 status —— 三条反证。
//
//	① 源码级：本文件的实现里，状态维度的写口符号族与状态字段读取一律零命中；
//	② 用例级：同一批卡把 status 逐个换值（active / deprecated / 空），R7 的输出**逐字不变**；
//	③ 入参级：跑完检查后，入参快照里每张卡的状态维度字节逐字未变（结构体深比对）。
func TestR7NeverTouchesStatus(t *testing.T) {
	src := readSourceFile(t, "r7_support.go")
	for _, bad := range []string{"SetStatus", "Status" + "Deprecated", ".Status",
		"Status =", "Deprecated =", "StatusActive"} {
		if strings.Contains(src, bad) {
			t.Fatalf("r7_support.go 出现状态写口 / 状态字段符号 %q：R7 永不改也不读 status", bad)
		}
	}
	notes := []query.NoteEntry{r7NoteEntry(r7Note2, r7Src2, true)}
	sources := r7SourceFacts(r7Src2)
	refs := []model.SourceRef{r7Ref(r7Src2, r7Note2, model.MaterialSupport)}
	var want []Finding
	for i, status := range []string{"active", "deprecated", ""} {
		cards := []query.CardEntry{r7CardEntry(r7Card, status, refs...)}
		before := append([]query.CardEntry(nil), cards...)
		in := r7In(cards, notes, sources)
		fs := r7Of(t, in)
		if len(fs) != 1 {
			t.Fatalf("status=%q 时应恰 1 条 W20，实得 %d 条", status, len(fs))
		}
		if i == 0 {
			want = fs
		} else if !reflect.DeepEqual(fs, want) {
			t.Fatalf("status 参与了判定：status=%q 的输出与 active 不同（%+v ≠ %+v）",
				status, fs, want)
		}
		if !reflect.DeepEqual(cards, before) {
			t.Fatalf("入参被改写：%+v ≠ %+v", cards, before)
		}
		if cards[0].Status != status || cards[0].Deprecated != (status == "deprecated") {
			t.Fatalf("卡的状态维度被动过：status=%q deprecated=%v",
				cards[0].Status, cards[0].Deprecated)
		}
	}
	// 整包非测试源里状态写口符号族恒零命中（合同 §10 第 1 条的 grep 反证）。
	for name, body := range nonTestSources(t) {
		for _, bad := range []string{"SetStatus", "Status" + "Deprecated"} {
			if strings.Contains(body, bad) {
				t.Fatalf("%s 出现 %q：对账包内永不改 status", name, bad)
			}
		}
	}
}

// TestR7NoOnDiskMarker：零落盘标记 —— 检查器不写任何文件、不产 RepairSpec，
// 视图提示的字面量在本包（含落盘层口径）一次都不出现。
func TestR7NoOnDiskMarker(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "card.md")
	if err := os.WriteFile(card, []byte("---\nid: "+r7Card+"\n---\n正文\n"), 0o644); err != nil {
		t.Fatalf("造数失败：%v", err)
	}
	before := dirSnapshot(t, dir)
	in := r7In([]query.CardEntry{r7CardEntry(r7Card, "active")},
		[]query.NoteEntry{r7NoteEntry(r7Note, r7Src, false)}, r7SourceFacts(r7Src))
	in.VaultRoot = dir
	fs, rs := checkR7SupportInsufficient(in)
	if len(fs) != 1 {
		t.Fatalf("零 support 卡应恰 1 条 W20，实得 %d 条", len(fs))
	}
	if len(rs) != 0 {
		t.Fatalf("R7 恒零 RepairSpec，实得 %d 条：%+v", len(rs), rs)
	}
	if after := dirSnapshot(t, dir); after != before {
		t.Fatalf("检查器产生了磁盘变化：%q → %q", before, after)
	}
	raw, err := os.ReadFile(card)
	if err != nil {
		t.Fatalf("回读失败：%v", err)
	}
	hint := "材料" + "支持不足"
	if strings.Contains(string(raw), hint) {
		t.Fatal("视图提示被写进了 Markdown：实时判定必须零落盘标记")
	}
	// 源码级：写盘 / 提交 / 子进程 API 与提示字面量在实现文件里零命中。
	src := readSourceFile(t, "r7_support.go")
	for _, bad := range []string{"os." + "WriteFile", "os." + "Create", "os." + "Remove",
		"Commit" + "(", "exec.", "os/exec", hint} {
		if strings.Contains(src, bad) {
			t.Fatalf("r7_support.go 出现 %q：R7 只读、零落盘", bad)
		}
	}
	// 整包非测试源：提示字面量零命中（提示只在命令渲染层，合同 §10 第 2 条）。
	for name, body := range nonTestSources(t) {
		if strings.Contains(body, hint) {
			t.Fatalf("%s 出现视图提示字面量：它只许在命令渲染层出现", name)
		}
	}
	// 幂等可复算：同一输入连跑两次逐字相同。
	again, _ := checkR7SupportInsufficient(in)
	if !reflect.DeepEqual(fs, again) {
		t.Fatalf("同一输入两次结果不同：%+v ≠ %+v", fs, again)
	}
}

// TestR7DisjointFromDeleteSupportCheck：与 M3 删除路径那张材料建议清单的路径分治
// （A-28）—— 字段不同、路径不同、互不覆盖。
func TestR7DisjointFromDeleteSupportCheck(t *testing.T) {
	// ① 本包**全部** .go 文件（含测试）都不出现删除路径那套建议清单的字段名 / 符号 /
	//    建议文案 —— 全部判据串在本用例内由分段拼出，因此这条 grep 不会自命中。
	del := deleteSupportSymbols()
	for name, body := range allSources(t) {
		for _, bad := range del {
			if strings.Contains(body, bad) {
				t.Fatalf("%s 出现删除路径字段 / 符号 %q：A-28 路径分治，两套东西互不覆盖",
					name, bad)
			}
		}
	}
	// ② 删除路径那套仍在原处且一字未动（本 task 不改它：逐字回读结构体键与两条建议文案）。
	raw, err := os.ReadFile(filepath.Join("..", "report", del[0]+".go"))
	if err != nil {
		t.Fatalf("读删除路径实现失败：%v", err)
	}
	body := string(raw)
	for _, want := range del {
		if !strings.Contains(body, want) {
			t.Fatalf("删除路径的 %q 不在原处：本 task 不得改写它", want)
		}
	}
	// ③ R7 的产出是四键 finding，键集合恒是那四个 —— 不含删除路径的建议键。
	in := r7In([]query.CardEntry{r7CardEntry(r7Card, "active")},
		[]query.NoteEntry{r7NoteEntry(r7Note, r7Src, false)}, r7SourceFacts(r7Src))
	fs := r7Of(t, in)
	if len(fs) != 1 {
		t.Fatalf("应恰 1 条 W20，实得 %d 条", len(fs))
	}
	if !reflect.DeepEqual(FindingKeys(),
		[]string{"check", "severity", "targets", "detail"}) {
		t.Fatalf("finding 键集合被改：%v", FindingKeys())
	}
	// ④ 触发条件不同：R7 是全库实时判定，与「本次删了谁」无关 —— 输入里没有任何
	//    「被删集合」概念，删除路径的建议清单也不会因为 R7 跑过而变化。
	if got := SupportFacts(in); len(got) != 1 || got[0].Effective != 0 {
		t.Fatalf("R7 的事实只由落盘支持面决定，实得 %+v", got)
	}
}

// deleteSupportSymbols 拼出 M3 删除路径那套建议清单的判据串（分段拼接，避免本文件自命中）。
// 第 0 项同时是那份实现文件的文件名主干。
func deleteSupportSymbols() []string {
	return []string{
		"support" + "_" + "check",
		"recommend" + "ation",
		"Recommend" + "DeprecateMark",
		"Recommend" + "RecheckMaterials",
		"lost" + "_" + "support",
		"remaining" + "_" + "support",
		"建议标记 `" + "deprecated`",
		"建议重新" + "检查材料关系",
	}
}

// TestR7NoDoubleCountWithR4DanglingRef：与 R4 的 `dangling_ref`（E12 第 ② 类）
// 零重复计数 —— 正向让位 + 反向不放宽。
func TestR7NoDoubleCountWithR4DanglingRef(t *testing.T) {
	sources := r7SourceFacts(r7Src)
	// 正向：唯一 support 的来源笔记缺失 → E12 恰 1 条、W20 恰 0 条（同一件事实只一码）。
	miss := r7In([]query.CardEntry{r7CardEntry(r7Card, "active",
		r7Ref(r7Src, r7Gone, model.MaterialSupport))},
		[]query.NoteEntry{r7NoteEntry(r7Note, r7Src, false)}, sources)
	res := Run(miss)
	if got := countCheck(res.Findings, CheckDanglingRef); got != 1 {
		t.Fatalf("来源笔记缺失应恰 1 条 %s，实得 %d 条：%+v", CheckDanglingRef, got, res.Findings)
	}
	if got := countCheck(res.Findings, CheckSupportInsufficient); got != 0 {
		t.Fatalf("该事实已由 E12 独家承载，W20 应恰 0 条，实得 %d 条", got)
	}
	// 反向（防「让位」被放宽成「永不报」）：对端**存在但已逻辑删除** → W20 恰 1 条、E12 恰 0 条。
	del := r7In([]query.CardEntry{r7CardEntry(r7Card, "active",
		r7Ref(r7Src, r7Note, model.MaterialSupport))},
		[]query.NoteEntry{r7NoteEntry(r7Note, r7Src, true)}, sources)
	res = Run(del)
	if got := countCheck(res.Findings, CheckSupportInsufficient); got != 1 {
		t.Fatalf("对端已逻辑删除应恰 1 条 W20，实得 %d 条：%+v", got, res.Findings)
	}
	if got := countCheck(res.Findings, CheckDanglingRef); got != 0 {
		t.Fatalf("对端存在（只是被删）不是悬空引用，E12 应恰 0 条，实得 %d 条", got)
	}
	// 反向之二（I-…-019 修复后的新真相）：原文端缺失现由 E12 第 ③ 类独家承载 →
	// E12 恰 1 条、W20 恰 0 条（R7 让位）。这不是放宽让位：对端缺失是「一件事」，
	// 由 error 级的 E12 记，warning 级的 W20 不再重复记同一件事。
	srcMiss := r7In([]query.CardEntry{r7CardEntry(r7Card, "active",
		r7Ref("s-20269999-gone", r7Note, model.MaterialSupport))},
		[]query.NoteEntry{r7NoteEntry(r7Note, r7Src, false)}, sources)
	res = Run(srcMiss)
	if got := countCheck(res.Findings, CheckDanglingRef); got != 1 {
		t.Fatalf("原文端缺失现归 E12 第 ③ 类，应恰 1 条 %s，实得 %d 条：%+v",
			CheckDanglingRef, got, res.Findings)
	}
	if got := countCheck(res.Findings, CheckSupportInsufficient); got != 0 {
		t.Fatalf("原文端缺失已由 E12 独家承载，W20 应恰 0 条（让位），实得 %d 条", got)
	}
}

// TestR7NoDoubleCountWithR4Orphan：与 R4 的 `orphan`（W17 · card_without_relation）
// 并存不算重复计数 —— 两码读的是两个不同的 frontmatter 键，且 R7 的输出与 `relations[]`
// 完全无关。
func TestR7NoDoubleCountWithR4Orphan(t *testing.T) {
	notes := []query.NoteEntry{r7NoteEntry(r7Note, r7Src, false)}
	sources := r7SourceFacts(r7Src)
	lonely := r7CardEntry(r7Card, "active") // 零关系 + 零 support
	res := Run(r7In([]query.CardEntry{lonely}, notes, sources))
	if got := countCheck(res.Findings, CheckOrphan); got != 1 {
		t.Fatalf("零关系卡应恰 1 条 %s，实得 %d 条：%+v", CheckOrphan, got, res.Findings)
	}
	if got := countCheck(res.Findings, CheckSupportInsufficient); got != 1 {
		t.Fatalf("零 support 卡应恰 1 条 W20，实得 %d 条", got)
	}
	// 两条 finding 的事实来源不同：W17 说的是关系面，W20 说的是材料面。
	for _, f := range res.Findings {
		switch f.Check {
		case CheckOrphan:
			if !strings.Contains(f.Detail, "relations") {
				t.Fatalf("W17 的 detail 应指向关系面：%q", f.Detail)
			}
		case CheckSupportInsufficient:
			if !strings.Contains(f.Detail, "sources[]") &&
				!strings.Contains(f.Detail, "support") {
				t.Fatalf("W20 的 detail 应指向材料面：%q", f.Detail)
			}
		}
	}
	// R7 与 relations[] 无关：给同一张卡加满关系，W20 逐字不变（W17 因此消失）。
	linked := lonely
	linked.Relations = []model.Relation{{
		Type: model.RelationSupports, Target: model.RelationEndpoint(r7Card2), Reason: "用例造数",
	}}
	res2 := Run(r7In([]query.CardEntry{linked, r7CardEntry(r7Card2, "active",
		r7Ref(r7Src, r7Note, model.MaterialSupport))}, notes, sources))
	if got := countCheck(res2.Findings, CheckOrphan); got != 0 {
		t.Fatalf("补上关系后 W17 应消失，实得 %d 条", got)
	}
	if got := r7IDs(onlyCheck(res2.Findings, CheckSupportInsufficient)); !reflect.DeepEqual(
		got, []string{r7Card}) {
		t.Fatalf("W20 只该落在零 support 的那张卡上，实得 %v", got)
	}
}

// TestR7NoDoubleCountWithR1R2R3R5：同一份输入里，R7 与 R1 / R2 / R3 / R5 各记各自那一件
// 事实 —— R7 的输出与 Git 状态 / 编辑事实 / rename 事实 / 关系条目**逐字无关**。
func TestR7NoDoubleCountWithR1R2R3R5(t *testing.T) {
	notes := []query.NoteEntry{r7NoteEntry(r7Note, r7Src, false)}
	sources := r7SourceFacts(r7Src)
	card := r7CardEntry(r7Card, "active")
	base := r7In([]query.CardEntry{card}, notes, sources)
	want := r7Of(t, base)
	if len(want) != 1 {
		t.Fatalf("基线应恰 1 条 W20，实得 %d 条", len(want))
	}
	rich := base
	rich.Status = git.Status{Changes: []git.Change{{
		Code: " M", Path: r7CardPath(r7Card),
	}}}
	rich.Edits = []EditFact{{Path: r7CardPath(r7Card), Verb: "vim"}}
	rich.Renames = []RenameFact{{
		OldPath: domainsDirName + "/ml/" + knowledgeDirName + "/" + r7Card + ".md",
		NewPath: r7CardPath(r7Card), Verb: "vim",
	}}
	if got := r7Of(t, rich); !reflect.DeepEqual(got, want) {
		t.Fatalf("R7 读了别人的事实面：%+v ≠ %+v", got, want)
	}
	// 注册表整体跑一遍：W20 恒恰 1 条（其余码各自按自己的事实报，互不吞并）。
	res := Run(rich)
	if got := countCheck(res.Findings, CheckSupportInsufficient); got != 1 {
		t.Fatalf("W20 应恰 1 条，实得 %d 条：%+v", got, res.Findings)
	}
	if got := countCheck(res.Findings, CheckGitUncommitted); got != 1 {
		t.Fatalf("R1 应照报 1 条 W13，实得 %d 条", got)
	}
}

// countCheck 数某个 check 的条数。
func countCheck(fs []Finding, check string) int {
	n := 0
	for _, f := range fs {
		if f.Check == check {
			n++
		}
	}
	return n
}

// onlyCheck 过滤出某个 check 的 finding（顺序保持）。
func onlyCheck(fs []Finding, check string) []Finding {
	out := make([]Finding, 0, len(fs))
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}
