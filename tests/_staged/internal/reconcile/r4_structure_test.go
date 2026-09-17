package reconcile

// R4 三项只读检查（E11 / E12 / W17）的表驱动单测（M4 · T-…-052）。
//
// 六组（deliverables 逐字要求，用例名不得改字）：
//  1. TestR4DuplicateIdAllConflictFiles   —— 同 ID 三文件冲突：恰 1 条 E11、targets 长度恰 3 且全列；
//  2. TestR4DanglingRefTwoKinds           —— 恰两类悬空引用：targets = [引用方 ID, 缺失目标 ID]；
//  3. TestR4OrphanThreeSubtypes           —— 孤儿封闭三子类型：一个 check、detail 区分、W17 / warning；
//  4. TestR4OrphanUsesOnDiskFacts         —— 孤儿看落盘事实：唯一邻居为 deprecated / 已删除时**不判**孤儿；
//  5. TestR4NoDoubleCountWithQ1Q2         —— 与查询域 Q1 / Q2 零双计数（同事实不同域）；
//  6. TestR4RelationTargetNotInDanglingRef —— 关系 target 缺失不走 E12（属 R3 的 E13）。
//
// 另加三组本 task 自守：targets 排序去重可复算、只读零副作用 + 零 RepairSpec、
// 判定不依赖展示面过滤（源码级 grep 反证 VisibleEndpoints 零引用）。
//
// 全部用例只用**内存构造**的 query.ScanResult + Input.Sources 快照，不建 vault、不读盘
// （唯一的读盘是三组自守用例对本包源文件做的 grep 反证）——检查器是纯函数，这正是收益。

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// card 构造一张知识卡条目（rels 为出边目标，deprecated / deleted 只作落盘事实留痕）。
func card(id, path string, rels ...string) query.CardEntry {
	c := query.CardEntry{ID: id, Path: path}
	for _, r := range rels {
		c.Relations = append(c.Relations,
			model.Relation{Type: model.RelationSupports, Target: model.RelationEndpoint(r), Reason: "e2e 事实"})
	}
	return c
}

// withSourceRefs 给卡挂来源笔记引用（`sources[].note`，第 ② 类悬空引用的判定面）。
// `source` 端一律用 s-b2（这些用例恒把 s-b2 放进 Sources 采样面），使本 helper 只在
// **来源笔记**这一维度制造悬空，不误触第 ③ 类（`sources[].source→原文`）——
// 后者的逐字段反证由 r4_dangling_fields_test.go 独立承载。
func withSourceRefs(c query.CardEntry, notes ...string) query.CardEntry {
	for _, n := range notes {
		c.Sources = append(c.Sources, model.SourceRef{
			Source: model.SourceID("s-b2"), Note: model.NoteID(n),
			Rel: model.MaterialSupport, Reason: "e2e 事实",
		})
	}
	return c
}

// note 构造一篇笔记条目（source 为空 = 未声明所属材料）。
func note(id, path, source string) query.NoteEntry {
	return query.NoteEntry{ID: id, Path: path, Source: source}
}

// scanOf 把卡 / 笔记折成扫描结果（计数守恒照 query 侧口径填，本包不消费它们）。
func scanOf(cards []query.CardEntry, notes []query.NoteEntry) *query.ScanResult {
	return &query.ScanResult{Cards: cards, Notes: notes,
		ScannedFiles: len(cards) + len(notes)}
}

// findingsOf 跑 R4 一项（不经注册表，便于逐项断言），并逐条校验四键 schema。
func findingsOf(t *testing.T, in Input) []Finding {
	t.Helper()
	fs, rs := checkR4Structure(in)
	if len(rs) != 0 {
		t.Fatalf("R4 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条：%+v", len(rs), rs)
	}
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
	}
	return fs
}

// pick 取指定 check 的全部 finding（顺序保持产出顺序）。
func pick(fs []Finding, check string) []Finding {
	var out []Finding
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// TestR4DuplicateIdAllConflictFiles：同一 ID 命中多个文件 → 恰一条 E11，
// targets 列出**全部**冲突文件（不只报第一个），去重 + 字典序升序。
func TestR4DuplicateIdAllConflictFiles(t *testing.T) {
	cases := []struct {
		name    string
		in      Input
		targets []string
	}{
		{
			name: "同 ID 三文件冲突（三个领域各放一份）",
			in: Input{Scan: scanOf([]query.CardEntry{
				card("k-a1", "domains/zz/knowledge/k-a1.md"),
				card("k-a1", "domains/ai/knowledge/k-a1.md"),
				card("k-a1", "domains/ml/knowledge/k-a1.md"),
			}, nil)},
			targets: []string{
				"domains/ai/knowledge/k-a1.md",
				"domains/ml/knowledge/k-a1.md",
				"domains/zz/knowledge/k-a1.md",
			},
		},
		{
			name: "同 ID 两文件冲突（笔记分区）",
			in: Input{Scan: scanOf(nil, []query.NoteEntry{
				note("n-c3", "domains/ml/notes/n-c3.md", "s-b2"),
				note("n-c3", "domains/ai/notes/n-c3.md", "s-b2"),
			})},
			targets: []string{"domains/ai/notes/n-c3.md", "domains/ml/notes/n-c3.md"},
		},
		{
			name: "跨分区撞同一 ID（卡 + 原文）同样算重复",
			in: Input{
				Scan:    scanOf([]query.CardEntry{card("x-1", "domains/ai/knowledge/x-1.md")}, nil),
				Sources: []SourceFact{{ID: "x-1", Path: "sources/x-1.md"}},
			},
			targets: []string{"domains/ai/knowledge/x-1.md", "sources/x-1.md"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dups := pick(findingsOf(t, c.in), CheckDuplicateID)
			if len(dups) != 1 {
				t.Fatalf("duplicate_id 条数 = %d，同一个重复 ID 恒产 1 条（不按文件产多条）：%+v",
					len(dups), dups)
			}
			f := dups[0]
			if f.Severity != SeverityError {
				t.Fatalf("severity = %q，E11 定死为 %q", f.Severity, SeverityError)
			}
			if f.Code() != CodeE11 {
				t.Fatalf("诊断码 = %q，duplicate_id 单射到 %q", f.Code(), CodeE11)
			}
			if !reflect.DeepEqual(f.Targets, c.targets) {
				t.Fatalf("targets = %v，期望全部冲突文件 %v（去重升序）", f.Targets, c.targets)
			}
			// 逐条反证「全列」而不是「只报第一个」：每个冲突路径都在 targets 与 detail 内。
			for _, p := range c.targets {
				if !strings.Contains(f.Detail, p) {
					t.Fatalf("detail 未含冲突路径 %s：%s", p, f.Detail)
				}
			}
			// 不自动改名 / 删除 / 移动：detail 逐字登记「只报告」，且零 RepairSpec（findingsOf 已断言）。
			if !strings.Contains(f.Detail, "只报告") {
				t.Fatalf("detail 未登记「只报告」口径：%s", f.Detail)
			}
		})
	}
	// 三文件冲突时 targets 长度恰 3（Acceptance 逐字要求的那条断言）。
	three := pick(findingsOf(t, Input{Scan: scanOf([]query.CardEntry{
		card("k-a1", "domains/a/knowledge/k-a1.md"),
		card("k-a1", "domains/b/knowledge/k-a1.md"),
		card("k-a1", "domains/c/knowledge/k-a1.md"),
	}, nil)}), CheckDuplicateID)
	if len(three) != 1 || len(three[0].Targets) != 3 {
		t.Fatalf("三文件同 ID：期望 1 条 finding / targets 长度恰 3，实得 %+v", three)
	}
	// 唯一一份落盘的 ID 不产 E11（判定条件是「命中 ≥ 2 个文件」）。
	clean := pick(findingsOf(t, Input{Scan: scanOf([]query.CardEntry{
		card("k-a1", "domains/ai/knowledge/k-a1.md", "k-b2"),
		card("k-b2", "domains/ai/knowledge/k-b2.md", "k-a1"),
	}, nil)}), CheckDuplicateID)
	if len(clean) != 0 {
		t.Fatalf("无重复 ID 时不得产 E11：%+v", clean)
	}
}

// TestR4DanglingRefTwoKinds：恰覆盖两类引用，targets = [引用方 ID, 缺失的目标 ID]。
func TestR4DanglingRefTwoKinds(t *testing.T) {
	cases := []struct {
		name    string
		in      Input
		targets []string
		kind    string
	}{
		{
			name: "第一类：笔记的 source 指向不存在的原文",
			in: Input{
				Scan:    scanOf(nil, []query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-gone")}),
				Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}},
			},
			targets: []string{"n-c3", "s-gone"},
			kind:    danglingNoteSource,
		},
		{
			name: "第二类：知识卡的 sources[].note 指向不存在的笔记",
			in: Input{
				Scan: scanOf([]query.CardEntry{withSourceRefs(
					card("k-a1", "domains/ai/knowledge/k-a1.md"), "n-gone")},
					[]query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-b2")}),
				Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}},
			},
			targets: []string{"k-a1", "n-gone"},
			kind:    danglingCardNote,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refs := pick(findingsOf(t, c.in), CheckDanglingRef)
			if len(refs) != 1 {
				t.Fatalf("dangling_ref 条数 = %d，期望恰 1：%+v", len(refs), refs)
			}
			f := refs[0]
			if f.Severity != SeverityError || f.Code() != CodeE12 {
				t.Fatalf("severity / code = %q / %q，dangling_ref 单射到 error / %s",
					f.Severity, f.Code(), CodeE12)
			}
			// targets 的两元素：引用方在前、缺失目标在后（升序结果与 §6.2 元素次序一致）。
			if !reflect.DeepEqual(f.Targets, c.targets) {
				t.Fatalf("targets = %v，期望 [引用方 ID, 缺失目标 ID] = %v", f.Targets, c.targets)
			}
			if !strings.Contains(f.Detail, c.kind) {
				t.Fatalf("detail 未登记引用类别 %s：%s", c.kind, f.Detail)
			}
		})
	}
	// 前两类（历史 §6.2）**同时**存在时各产一条，共恰 2 条。第 ③ / ④ 类
	// （sources[].source / replaced_by.target）的四字段全覆盖冻结在
	// r4_dangling_fields_test.go；本用例仍逐字守住原两类的方向与去重语义。
	both := Input{
		Scan: scanOf([]query.CardEntry{withSourceRefs(
			card("k-a1", "domains/ai/knowledge/k-a1.md", "k-b2"), "n-gone")},
			[]query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-gone")}),
		Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}},
	}
	if got := pick(findingsOf(t, both), CheckDanglingRef); len(got) != 2 {
		t.Fatalf("前两类引用同时悬空应恰 2 条，实得 %d 条：%+v", len(got), got)
	}
	// 目标存在即不报；同一 (引用方, 缺失目标) 出现两次只报一条。
	solid := Input{
		Scan: scanOf([]query.CardEntry{withSourceRefs(
			card("k-a1", "domains/ai/knowledge/k-a1.md"), "n-c3", "n-c3")},
			[]query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-b2")}),
		Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}},
	}
	if got := pick(findingsOf(t, solid), CheckDanglingRef); len(got) != 0 {
		t.Fatalf("引用目标存在时不得报 E12：%+v", got)
	}
	dupRef := Input{
		Scan: scanOf([]query.CardEntry{withSourceRefs(
			card("k-a1", "domains/ai/knowledge/k-a1.md"), "n-gone", "n-gone")}, nil),
	}
	if got := pick(findingsOf(t, dupRef), CheckDanglingRef); len(got) != 1 {
		t.Fatalf("同一 (引用方, 缺失目标) 是一件事实，应恰 1 条，实得 %d 条：%+v", len(got), got)
	}
	// `sources/` 分区**未采样**（Sources == nil）时不判笔记的 source 引用：
	// 「没采样」绝不许被说成「不存在」。
	unsampled := Input{Scan: scanOf(nil,
		[]query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-b2")})}
	if got := pick(findingsOf(t, unsampled), CheckDanglingRef); len(got) != 0 {
		t.Fatalf("sources/ 未采样时不得判悬空 source：%+v", got)
	}
}

// TestR4OrphanThreeSubtypes：三种孤儿合并为**一个** check（W17 / warning），
// 由 detail 区分封闭三子类型；targets 恰一个对象 ID；产出顺序 = 子类型行序 → ID 升序。
func TestR4OrphanThreeSubtypes(t *testing.T) {
	in := Input{
		Scan: scanOf([]query.CardEntry{
			card("k-lonely", "domains/ai/knowledge/k-lonely.md"), // 零关系 → 子类型 ③
			card("k-a1", "domains/ai/knowledge/k-a1.md", "k-b2"), // 有出边 → 不孤
			card("k-b2", "domains/ai/knowledge/k-b2.md"),         // 有入边 → 不孤
		}, []query.NoteEntry{
			note("n-free", "domains/ai/notes/n-free.md", ""),   // 未声明 source → 子类型 ①
			note("n-c3", "domains/ai/notes/n-c3.md", "s-used"), // 有所属材料 → 不孤
		}),
		Sources: []SourceFact{
			{ID: "s-used", Path: "sources/s-used.md"}, // 有派生笔记 → 不孤
			{ID: "s-idle", Path: "sources/s-idle.md"}, // 无派生笔记 → 子类型 ②
		},
	}
	orphans := pick(findingsOf(t, in), CheckOrphan)
	if len(orphans) != OrphanSubtypeCount {
		t.Fatalf("孤儿条数 = %d，本例应恰 %d（三子类型各 1）：%+v",
			len(orphans), OrphanSubtypeCount, orphans)
	}
	want := []struct{ subtype, id string }{
		{OrphanNoteWithoutSource, "n-free"},
		{OrphanSourceWithoutNote, "s-idle"},
		{OrphanCardWithoutRelation, "k-lonely"},
	}
	for i, w := range want {
		f := orphans[i]
		if f.Check != CheckOrphan {
			t.Fatalf("第 %d 条的 check = %q，三子类型必须合并为同一个 %q", i, f.Check, CheckOrphan)
		}
		if f.Severity != SeverityWarning || f.Code() != CodeW17 {
			t.Fatalf("第 %d 条 severity / code = %q / %q，orphan 单射到 warning / %s",
				i, f.Severity, f.Code(), CodeW17)
		}
		if !reflect.DeepEqual(f.Targets, []string{w.id}) {
			t.Fatalf("第 %d 条 targets = %v，期望恰 [%s]", i, f.Targets, w.id)
		}
		if !strings.Contains(f.Detail, w.subtype) {
			t.Fatalf("第 %d 条 detail 未登记子类型 %s：%s", i, w.subtype, f.Detail)
		}
	}
	// 三子类型是**封闭三值**：第四个取值不在表内，表长恒 3。
	if got := OrphanSubtypes(); len(got) != OrphanSubtypeCount ||
		!reflect.DeepEqual(got, []string{OrphanNoteWithoutSource, OrphanSourceWithoutNote,
			OrphanCardWithoutRelation}) {
		t.Fatalf("子类型全集 = %v，期望封闭三值且顺序 = 合同 §6.3 行序", got)
	}
	for _, bad := range []string{"note_orphan", "source_orphan", "card_orphan", "", "orphan"} {
		if IsKnownOrphanSubtype(bad) {
			t.Fatalf("IsKnownOrphanSubtype(%q) = true：子类型是恰 %d 值的封闭集合",
				bad, OrphanSubtypeCount)
		}
	}
	// 未采样 `sources/` 时不判 source_without_note（诚实性：没采样 ≠ 没有笔记）。
	unsampled := pick(findingsOf(t, Input{Scan: scanOf(nil,
		[]query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-b2")})}), CheckOrphan)
	for _, f := range unsampled {
		if strings.Contains(f.Detail, OrphanSourceWithoutNote) {
			t.Fatalf("sources/ 未采样时不得判 %s：%s", OrphanSourceWithoutNote, f.Detail)
		}
	}
	// 同 ID 落两个文件的孤儿卡只报**一次**（重复本身由 E11 单独报，不在孤儿里重复出现）。
	dup := pick(findingsOf(t, Input{Scan: scanOf([]query.CardEntry{
		card("k-lonely", "domains/b/knowledge/k-lonely.md"),
		card("k-lonely", "domains/a/knowledge/k-lonely.md"),
	}, nil)}), CheckOrphan)
	if len(dup) != 1 || !reflect.DeepEqual(dup[0].Targets, []string{"k-lonely"}) {
		t.Fatalf("同 ID 两文件的孤儿卡应恰 1 条 W17，实得 %+v", dup)
	}
	// 已声明 source 但目标缺失的笔记**不算** note_without_source（那是 E12，避免双计）。
	miss := findingsOf(t, Input{
		Scan:    scanOf(nil, []query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-gone")}),
		Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}},
	})
	for _, f := range pick(miss, CheckOrphan) {
		if strings.Contains(f.Detail, OrphanNoteWithoutSource) {
			t.Fatalf("已声明 source 的笔记不得判 %s（应只报 E12）：%s", OrphanNoteWithoutSource, f.Detail)
		}
	}
	if got := pick(miss, CheckDanglingRef); len(got) != 1 {
		t.Fatalf("source 目标缺失应恰 1 条 E12，实得 %+v", got)
	}
}

// TestR4OrphanUsesOnDiskFacts：孤儿判定只看落盘事实，**不看**展示面过滤结果 ——
// 唯一邻居为 deprecated（且未删除）或已逻辑删除时，卡都不得被判孤儿（与 T-…-061 双侧锁）。
func TestR4OrphanUsesOnDiskFacts(t *testing.T) {
	deprecated := query.CardEntry{ID: "k-old", Path: "domains/ai/knowledge/k-old.md",
		Status: "deprecated", Deprecated: true}
	deleted := query.CardEntry{ID: "k-del", Path: "domains/ai/knowledge/k-del.md",
		DeletedAt: "2026-11-01", Deleted: true}
	cases := []struct {
		name       string
		in         Input
		notOrphans []string
	}{
		{
			name: "唯一入边来自 deprecated 卡 → 不判孤儿",
			in: Input{Scan: scanOf([]query.CardEntry{
				func() query.CardEntry {
					c := deprecated
					c.Relations = []model.Relation{{Type: model.RelationSupports, Target: "k-live",
						Reason: "落盘事实"}}
					return c
				}(),
				card("k-live", "domains/ai/knowledge/k-live.md"),
			}, nil)},
			notOrphans: []string{"k-live", "k-old"},
		},
		{
			name: "唯一出边指向 deprecated 卡 → 不判孤儿",
			in: Input{Scan: scanOf([]query.CardEntry{
				card("k-live", "domains/ai/knowledge/k-live.md", "k-old"),
				deprecated,
			}, nil)},
			notOrphans: []string{"k-live", "k-old"},
		},
		{
			name: "唯一邻居已逻辑删除（deleted_at 非空）→ 仍不判孤儿",
			in: Input{Scan: scanOf([]query.CardEntry{
				card("k-live", "domains/ai/knowledge/k-live.md", "k-del"),
				deleted,
			}, nil)},
			notOrphans: []string{"k-del", "k-live"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, f := range pick(findingsOf(t, c.in), CheckOrphan) {
				got = append(got, f.Targets...)
			}
			sort.Strings(got)
			for _, id := range c.notOrphans {
				for _, g := range got {
					if g == id {
						t.Fatalf("%s 被判孤儿：落盘事实里它有边，展示面是否隐藏邻居与判定无关（孤儿 = %v）",
							id, got)
					}
				}
			}
			if len(got) != 0 {
				t.Fatalf("本例不应有任何孤儿卡，实得 %v", got)
			}
		})
	}
	// 悬空出边同样算「有边」：卡不是孤立的，那条边的缺失属 R3 的 E13。
	dangOut := pick(findingsOf(t, Input{Scan: scanOf([]query.CardEntry{
		card("k-live", "domains/ai/knowledge/k-live.md", "k-gone")}, nil)}), CheckOrphan)
	if len(dangOut) != 0 {
		t.Fatalf("有落盘出边（即使目标缺失）的卡不得判孤儿：%+v", dangOut)
	}
	// 结构反证：本文件与实现文件都不引展示面过滤入口（VisibleEndpoints 零命中），
	// 也不 import 任何过滤器包 —— 口径与 Acceptance 的 grep 逐字一致。
	src := nonTestSources(t)
	body, ok := src["r4_structure.go"]
	if !ok {
		t.Fatal("未找到 r4_structure.go：R4 实现必须落在本包内")
	}
	for _, token := range []string{"Visible" + "Endpoints", "Visible(", "filter."} {
		if strings.Contains(body, token) {
			t.Fatalf("r4_structure.go 出现 %q：孤儿判定必须看落盘事实（合同 §6.3 末段）", token)
		}
	}
	// 落盘事实的两个读口存在且同源（出边度数只有 OutDegree 一个读口）。
	x := NewStructureIndex(Input{Scan: scanOf([]query.CardEntry{
		card("k-a1", "domains/ai/knowledge/k-a1.md", "k-b2"),
		card("k-b2", "domains/ai/knowledge/k-b2.md"),
	}, nil)})
	if x.OutDegree("k-a1") != 1 || x.InDegree("k-b2") != 1 ||
		x.OutDegree("k-b2") != 0 || x.InDegree("k-a1") != 0 {
		t.Fatalf("落盘出入度不符：out(k-a1)=%d in(k-b2)=%d out(k-b2)=%d in(k-a1)=%d",
			x.OutDegree("k-a1"), x.InDegree("k-b2"), x.OutDegree("k-b2"), x.InDegree("k-a1"))
	}
	if !reflect.DeepEqual(x.RelationsIn["k-b2"], []string{"k-a1"}) {
		t.Fatalf("反向表 = %v，期望 [k-a1]（全库反向扫描，去重升序）", x.RelationsIn["k-b2"])
	}
}

// TestR4NoDoubleCountWithQ1Q2：对账域的 E11 / E12 与查询域 Q1 / Q2 同事实不同域，
// 但**同一个输出里不计两次**：R4 一个字节都不读 Scan.Diagnostics，也不透传 Q 码。
func TestR4NoDoubleCountWithQ1Q2(t *testing.T) {
	// 造一份「查询域已记 Q1 / Q2」的扫描结果：同 ID 两文件 + 一条悬空引用。
	scan := scanOf([]query.CardEntry{
		card("k-a1", "domains/a/knowledge/k-a1.md", "k-gone"),
		card("k-a1", "domains/b/knowledge/k-a1.md", "k-gone"),
	}, []query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-gone")})
	scan.Diagnostics = []query.Diagnostic{
		{Code: query.CodeQ1, Path: "domains/b/knowledge/k-a1.md", Message: "同 ID 重复（查询域）"},
		{Code: query.CodeQ2, Path: "domains/a/knowledge/k-a1.md", Message: "悬空关系（查询域）"},
	}
	in := Input{Scan: scan, Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}}}
	fs := findingsOf(t, in)
	// ① 重复 ID：查询域会为第 2 个文件记一条 Q1，对账域恒**一条** E11（不按文件计两次）。
	if dups := pick(fs, CheckDuplicateID); len(dups) != 1 || len(dups[0].Targets) != 2 {
		t.Fatalf("同 ID 两文件应恰 1 条 E11 且 targets 恰 2 条，实得 %+v", dups)
	}
	// ② 悬空引用：只报 frontmatter 两类引用的那一条，Q2 的关系悬空不进 E12。
	if refs := pick(fs, CheckDanglingRef); len(refs) != 1 ||
		!reflect.DeepEqual(refs[0].Targets, []string{"n-c3", "s-gone"}) {
		t.Fatalf("应恰 1 条 E12（笔记 source 悬空），实得 %+v", refs)
	}
	// ③ Q 码一律不出现在对账域输出里（check / code / detail 三处都不许）。
	for _, f := range fs {
		if strings.HasPrefix(f.Code(), "Q") || strings.Contains(f.Check, "Q") {
			t.Fatalf("对账域 finding 携带查询域码：%+v", f)
		}
		for _, q := range []string{"Q" + "1", "Q" + "2"} {
			if strings.Contains(f.Detail, q) {
				t.Fatalf("detail 透传了查询域码 %s（同一事实不得在一个输出里计两次）：%s", q, f.Detail)
			}
		}
	}
	// ④ 结构反证：实现文件不读 Scan.Diagnostics（读了就等于把 Q 系列搬进对账域）。
	body := nonTestSources(t)["r4_structure.go"]
	for _, token := range []string{".Diagnostics", "HasQ(", "newQ1", "CodeQ"} {
		if strings.Contains(body, token) {
			t.Fatalf("r4_structure.go 出现 %q：Q 系列是查询域诊断，对账域不消费、不改写、不透传",
				token)
		}
	}
	// ⑤ 查询域诊断本体未被改动（入参一字不变：条数与首条码逐字保持）。
	if len(scan.Diagnostics) != 2 || scan.Diagnostics[0].Code != query.CodeQ1 {
		t.Fatalf("R4 改动了查询域诊断：%+v", scan.Diagnostics)
	}
	// ⑥ 对账域产出的码全部落在本包封闭 12 值内。
	for _, f := range fs {
		if !IsKnownCode(f.Code()) {
			t.Fatalf("finding 的码 %q 不在本包封闭 %d 值内", f.Code(), CodeCount)
		}
	}
}

// TestR4RelationTargetNotInDanglingRef：关系条目的 target 缺失**不走** E12
// （属 R3 的 relation_target_missing / E13，归 T-…-053）——一件事不许两码重复计。
func TestR4RelationTargetNotInDanglingRef(t *testing.T) {
	cases := []struct {
		name string
		in   Input
	}{
		{
			name: "出边 target 指向不存在的卡",
			in: Input{Scan: scanOf([]query.CardEntry{
				card("k-a1", "domains/ai/knowledge/k-a1.md", "k-gone")}, nil)},
		},
		{
			name: "出边 target 前缀非法（n- 前缀）",
			in: Input{Scan: scanOf([]query.CardEntry{
				card("k-a1", "domains/ai/knowledge/k-a1.md", "n-c3")}, nil)},
		},
		{
			name: "出边 target 为空串",
			in: Input{Scan: scanOf([]query.CardEntry{
				card("k-a1", "domains/ai/knowledge/k-a1.md", "")}, nil)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := findingsOf(t, c.in)
			if refs := pick(fs, CheckDanglingRef); len(refs) != 0 {
				t.Fatalf("关系 target 的问题不得走 dangling_ref（属 R3 的 %s）：%+v",
					CheckRelationTargetMissing, refs)
			}
			// R3 的四个 check 一个都不该由本 task 产出（R3 属 T-…-053）。
			for _, r3 := range []string{CheckRelationTargetMissing, CheckRelationPrefixInvalid,
				CheckRelationOpposingAsymmetric, CheckRelationDuplicate} {
				if got := pick(fs, r3); len(got) != 0 {
					t.Fatalf("本 task 越界产出了 R3 的 %s：%+v", r3, got)
				}
			}
			// 其他 R 编号的 check 同样零产出（R2 / R5 / R6 / R7 分属别的 task）。
			for _, other := range []string{CheckGitUncommitted, CheckReviewedAtMissing,
				CheckDomainMoved, CheckRecapStale, CheckSupportInsufficient} {
				if got := pick(fs, other); len(got) != 0 {
					t.Fatalf("本 task 越界产出了 %s：%+v", other, got)
				}
			}
		})
	}
	// 空 target 不算一条边：那张卡因此仍是零出边（形态问题归 R3），孤儿判定照常成立。
	empty := pick(findingsOf(t, Input{Scan: scanOf([]query.CardEntry{
		card("k-a1", "domains/ai/knowledge/k-a1.md", "")}, nil)}), CheckOrphan)
	if len(empty) != 1 || !reflect.DeepEqual(empty[0].Targets, []string{"k-a1"}) {
		t.Fatalf("空 target 不构成一条边：该卡应被判孤儿，实得 %+v", empty)
	}
	// 只有本 task 的三个 check 会出现在 R4 的产出集合内（三值封闭）。
	rich := Input{
		Scan: scanOf([]query.CardEntry{
			card("k-a1", "domains/a/knowledge/k-a1.md"),
			card("k-a1", "domains/b/knowledge/k-a1.md"),
		}, []query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "s-gone")}),
		Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}},
	}
	seen := map[string]bool{}
	for _, f := range findingsOf(t, rich) {
		seen[f.Check] = true
	}
	got := make([]string, 0, len(seen))
	for k := range seen {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{CheckDanglingRef, CheckDuplicateID, CheckOrphan}) {
		t.Fatalf("R4 产出的 check 集合 = %v，应恰 {duplicate_id, dangling_ref, orphan}", got)
	}
}

// TestR4TargetsSortedAndDeduped：三项的 targets 都是去重 + 字典序升序，可逐字复算。
func TestR4TargetsSortedAndDeduped(t *testing.T) {
	in := Input{
		Scan: scanOf([]query.CardEntry{
			card("k-a1", "domains/z/knowledge/k-a1.md"),
			card("k-a1", "domains/a/knowledge/k-a1.md"),
			card("k-a1", "domains/a/knowledge/k-a1.md"), // 同路径重复登记 → 去重
			withSourceRefs(card("k-zz", "domains/z/knowledge/k-zz.md", "k-a1"), "n-gone"),
		}, []query.NoteEntry{
			note("n-y", "domains/z/notes/n-y.md", "s-gone"),
			note("n-x", "domains/a/notes/n-x.md", ""),
		}),
		Sources: []SourceFact{{ID: "s-b2", Path: "sources/s-b2.md"}},
	}
	fs := findingsOf(t, in)
	if len(fs) == 0 {
		t.Fatal("本例应有 finding")
	}
	for _, f := range fs {
		for i := 1; i < len(f.Targets); i++ {
			if f.Targets[i-1] >= f.Targets[i] {
				t.Fatalf("%s 的 targets 未严格升序去重：%v", f.Check, f.Targets)
			}
		}
	}
	if dups := pick(fs, CheckDuplicateID); len(dups) != 1 ||
		!reflect.DeepEqual(dups[0].Targets, []string{
			"domains/a/knowledge/k-a1.md", "domains/z/knowledge/k-a1.md"}) {
		t.Fatalf("同路径重复登记未去重：%+v", dups)
	}
	// 打乱输入顺序不改变输出（顺序与 map 迭代序无关）。
	shuffled := Input{Sources: in.Sources, Scan: scanOf(
		[]query.CardEntry{in.Scan.Cards[3], in.Scan.Cards[1], in.Scan.Cards[0], in.Scan.Cards[2]},
		[]query.NoteEntry{in.Scan.Notes[1], in.Scan.Notes[0]})}
	if !reflect.DeepEqual(fs, findingsOf(t, shuffled)) {
		t.Fatalf("输入顺序改变了输出：R4 必须可逐字复算\n%+v\n%+v", fs, findingsOf(t, shuffled))
	}
	// 经注册表跑一遍：排序键 (severity, check, targets[0]) 下 error 段在 warning 段之前。
	res := Run(in)
	lastRank := -1
	for _, f := range res.Findings {
		r := severityRank(f.Severity)
		if r < lastRank {
			t.Fatalf("findings 未按 severity 排序（error 在前）：%+v", res.Findings)
		}
		lastRank = r
	}
	if !res.HasError() {
		t.Fatal("本例含 E11 / E12，HasError 必须为真（eg check 的 error 级语义由 T-…-059 消费）")
	}
}

// TestR4ReadOnlyNoSideEffectZeroRepair：只读、零副作用、零 RepairSpec、入参不被改动。
func TestR4ReadOnlyNoSideEffectZeroRepair(t *testing.T) {
	in := Input{
		Scan: scanOf([]query.CardEntry{
			card("k-a1", "domains/b/knowledge/k-a1.md"),
			card("k-a1", "domains/a/knowledge/k-a1.md"),
		}, []query.NoteEntry{note("n-c3", "domains/ai/notes/n-c3.md", "")}),
		Sources: []SourceFact{{ID: "s-idle", Path: "sources/s-idle.md"}},
	}
	firstCardPath, firstNoteID := in.Scan.Cards[0].Path, in.Scan.Notes[0].ID
	srcID := in.Sources[0].ID
	first, repairs := checkR4Structure(in)
	second, repairs2 := checkR4Structure(in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("同输入两次产出不同：%+v vs %+v", first, second)
	}
	if len(repairs) != 0 || len(repairs2) != 0 {
		t.Fatalf("R4 恒零 RepairSpec（只报告项，写口归属表第 4 行「无人写」）：%+v", repairs)
	}
	if in.Scan.Cards[0].Path != firstCardPath || in.Scan.Notes[0].ID != firstNoteID ||
		in.Sources[0].ID != srcID {
		t.Fatal("入参被检查器改动：R4 必须是纯函数")
	}
	// 空输入：未扫描 + 未采样 → 空产出（「没取数」不产 finding）。
	if fs, rs := checkR4Structure(Input{}); len(fs) != 0 || len(rs) != 0 {
		t.Fatalf("空输入应零产出，实得 %d finding / %d repair", len(fs), len(rs))
	}
	if fs, _ := checkR4Structure(Input{Scan: scanOf(nil, nil)}); len(fs) != 0 {
		t.Fatalf("空 vault 应零产出，实得 %+v", fs)
	}
	// 索引的两张反向表在空输入下非 nil 且为空（下游 T-…-053 / 055 / 056 复用同一份）。
	x := NewStructureIndex(Input{})
	if x.SourcesSampled || len(x.RelationsIn) != 0 || len(x.NotesBySource) != 0 ||
		len(x.ObjectPaths) != 0 {
		t.Fatalf("空输入的索引不干净：%+v", x)
	}
	// R4 编号常量与 check 表的 R 列一致（三行都是 R4）。
	for _, c := range []string{CheckDuplicateID, CheckDanglingRef, CheckOrphan} {
		spec, ok := SpecOf(c)
		if !ok || spec.R != R4 {
			t.Fatalf("check %q 的 R 列 = %q，应为 %q", c, spec.R, R4)
		}
	}
}
