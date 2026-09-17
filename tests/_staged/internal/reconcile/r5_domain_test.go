package reconcile

// R5 手工跨领域移动检测（`domain_moved` / W18）的表驱动单测（M4 · T-…-054）。
//
// 五组（deliverables 逐字要求，用例名不得改字）：
//  1. TestR5DomainSegmentMismatch            —— 判据① 领域目录段 ≠ 对象自报领域 → 命中；
//  2. TestR5CrossDomainRenameFromExternalEdit —— 判据② 非 `eg` 产生的跨领域 rename → 命中；
//  3. TestR5EgOwnRenameNotFlagged             —— `eg` 自身产生的 rename **不**命中（零误报）；
//  4. TestR5TargetsTripleOrderFixed           —— targets 恰三元且顺序固定 [ID, 旧领域, 新领域]；
//  5. TestR5ReportOnlyNoMoveNoRelationFix     —— 只报告：零 RepairSpec / 零写盘 / 入参不改 / 幂等。
//
// 另加四组本 task 自守：命中 / 未命中**真值表**（两判据的四象限 + 全部「不判」口径）、
// 同一对象**恒一条**的去重、与 R4 的 duplicate_id 零重复计数（照 T-…-052 / 053 的反证形态）、
// 与 R1 / R2 / R3 零重复计数（同一份输入里四项各记各自那一件事实）。
//
// 全部用例只用**内存构造**的 query.ScanResult + RenameFact 快照，不建 vault、不读盘
// （唯一的读盘是只报告用例对本包源文件做的 grep 反证）—— 检查器是纯函数，这正是收益。

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// 用例常量：领域名与对象 ID（ID 形态逐字满足 model 的既有规则，不新造）。
const (
	domAI    = "ai"
	domInfra = "infra"
	domOps   = "ops"

	r5Alpha = "k-20261126-alpha"
	r5Beta  = "k-20261126-beta"
	r5Zeta  = "k-20261126-zeta"
	r5Note  = "n-20261126-note"
)

// r5Path 拼出领域分区内的对象路径（`domains/<领域>/knowledge/<id>.md`）。
func r5Path(domain, id string) string {
	return domainsDirName + "/" + domain + "/" + knowledgeDirName + "/" + id + ".md"
}

// r5NotePath 拼出材料笔记的路径（`domains/<领域>/notes/<id>.md`）。
func r5NotePath(domain, id string) string {
	return domainsDirName + "/" + domain + "/" + notesDirName + "/" + id + ".md"
}

// r5Card 构造一张落在 pathDomain 目录、自报领域为 claim 的知识卡。
//
// claim 传空串 = 取数侧未给出「对象自报领域」这一事实（M2 扫描底座的常态：领域由目录唯一
// 决定、`domain` 是产物 frontmatter 黑名单键），此时判据① 恒不命中。
func r5Card(id, pathDomain, claim string) query.CardEntry {
	return query.CardEntry{ID: id, Path: r5Path(pathDomain, id), Domain: claim}
}

// r5Scan 把若干张卡折成扫描快照。
func r5Scan(cards ...query.CardEntry) *query.ScanResult {
	return &query.ScanResult{Cards: cards, ScannedFiles: len(cards)}
}

// r5Move 构造一条 rename 事实（新路径 = 该对象当前落盘位置）。
func r5Move(id, from, to, verb string) RenameFact {
	return RenameFact{OldPath: r5Path(from, id), NewPath: r5Path(to, id), Verb: verb}
}

// r5Of 跑 R5 检查项本体并逐条校验：四键 schema 合规 + **零 RepairSpec**（R5 只报告）
// + 每条 targets 恰三元且非空 + check 恒是 domain_moved。
func r5Of(t *testing.T, in Input) []Finding {
	t.Helper()
	fs, rs := checkR5DomainMoved(in)
	if len(rs) != 0 {
		t.Fatalf("R5 是只报告项，RepairSpec 必须恒 0 条，实得 %d 条：%+v", len(rs), rs)
	}
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			t.Fatalf("finding 不合四键 schema：%v（%+v）", err, f)
		}
		if f.Check != CheckDomainMoved {
			t.Fatalf("R5 只产 %s，实得 %q", CheckDomainMoved, f.Check)
		}
		if f.Severity != SeverityWarning {
			t.Fatalf("%s 的 severity 必须逐字取自单射表（warning），实得 %q", f.Check, f.Severity)
		}
		if len(f.Targets) != DomainMovedTargetArity {
			t.Fatalf("targets 必须恰 %d 元，实得 %d：%v", DomainMovedTargetArity, len(f.Targets), f.Targets)
		}
		for i, tg := range f.Targets {
			if strings.TrimSpace(tg) == "" {
				t.Fatalf("targets[%d] 为空：%v", i, f.Targets)
			}
		}
		if strings.TrimSpace(f.Detail) == "" {
			t.Fatalf("detail 为空：%+v", f)
		}
	}
	return fs
}

// r5One 断言恰 1 条 W18 并返回它。
func r5One(t *testing.T, in Input) Finding {
	t.Helper()
	fs := r5Of(t, in)
	if len(fs) != 1 {
		t.Fatalf("应恰 1 条 %s，实得 %d 条：%+v", CheckDomainMoved, len(fs), fs)
	}
	return fs[0]
}

// TestR5DomainSegmentMismatch：判据① —— 文件所在领域目录段 ≠ 对象自报领域即命中，
// 旧领域取自报值、新领域取目录段；相等 / 自报为空 / 路径不带领域段一律**不**命中。
func TestR5DomainSegmentMismatch(t *testing.T) {
	cases := []struct {
		name          string
		card          query.CardEntry
		wantHit       bool
		wantOld       string
		wantNew       string
		wantEvidences []string
	}{
		{
			name:          "自报 infra 但落在 ai 目录：命中，旧 infra → 新 ai",
			card:          r5Card(r5Alpha, domAI, domInfra),
			wantHit:       true,
			wantOld:       domInfra,
			wantNew:       domAI,
			wantEvidences: []string{DomainEvidenceSegmentMismatch},
		},
		{
			name:    "自报与目录段一致：不命中（正常库零误报）",
			card:    r5Card(r5Alpha, domAI, domAI),
			wantHit: false,
		},
		{
			name:    "自报为空（未采到该事实）：不命中——不把缺事实当证据",
			card:    r5Card(r5Alpha, domAI, ""),
			wantHit: false,
		},
		{
			name:    "自报只差空白：不命中（比对前逐字 TrimSpace，不放宽也不收紧）",
			card:    r5Card(r5Alpha, domAI, "  "+domAI+"  "),
			wantHit: false,
		},
		{
			name:    "路径不带领域段（收件区）：整体不判——读不出领域 ≠ 领域变了",
			card:    query.CardEntry{ID: r5Alpha, Path: "unprocessed.md", Domain: domInfra},
			wantHit: false,
		},
		{
			name:    "路径在原文分区（无领域层级）：整体不判",
			card:    query.CardEntry{ID: r5Alpha, Path: "sources/s-20261126-x.md", Domain: domInfra},
			wantHit: false,
		},
		{
			name:    "层级不符（第三段既非 knowledge 也非 notes）：整体不判",
			card:    query.CardEntry{ID: r5Alpha, Path: "domains/ai/other/x.md", Domain: domInfra},
			wantHit: false,
		},
		{
			name:    "ID 为空（扫描面缺事实）：整体不判",
			card:    query.CardEntry{ID: "  ", Path: r5Path(domAI, r5Alpha), Domain: domInfra},
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := R5ScanOf("", r5Scan(tc.card), nil)
			fs := r5Of(t, in)
			if !tc.wantHit {
				if len(fs) != 0 {
					t.Fatalf("应零命中，实得 %d 条：%+v", len(fs), fs)
				}
				return
			}
			f := r5One(t, in)
			if got := f.Targets[1]; got != tc.wantOld {
				t.Fatalf("旧领域 = %q，期望 %q（targets=%v）", got, tc.wantOld, f.Targets)
			}
			if got := f.Targets[2]; got != tc.wantNew {
				t.Fatalf("新领域 = %q，期望 %q（targets=%v）", got, tc.wantNew, f.Targets)
			}
			mv := DomainMoves(in)
			if len(mv) != 1 || !reflect.DeepEqual(mv[0].Evidence, tc.wantEvidences) {
				t.Fatalf("证据集合 = %+v，期望 %v", mv, tc.wantEvidences)
			}
		})
	}
}

// TestR5CrossDomainRenameFromExternalEdit：判据② —— Git 历史存在跨领域目录 rename
// 且那次提交**不是** `eg` 产生的即命中；旧领域取 rename 旧路径的领域段。
func TestR5CrossDomainRenameFromExternalEdit(t *testing.T) {
	cases := []struct {
		name    string
		renames []RenameFact
		wantHit bool
		wantOld string
	}{
		{
			name:    "外部 verb 的跨领域 rename：命中，旧领域取旧路径领域段",
			renames: []RenameFact{r5Move(r5Beta, domInfra, domAI, "mv")},
			wantHit: true, wantOld: domInfra,
		},
		{
			name:    "外部 verb 的多跳历史：只报落到当前路径的那一跳，恒一条",
			renames: []RenameFact{r5Move(r5Beta, domOps, domAI, "chore"), r5Move(r5Beta, domInfra, domAI, "mv")},
			wantHit: true, wantOld: domInfra, // (旧路径, verb) 升序：domains/infra… < domains/ops…
		},
		{
			name:    "同领域内改名：不命中（改名不是跨领域移动）",
			renames: []RenameFact{{OldPath: r5Path(domAI, "k-20261126-old"), NewPath: r5Path(domAI, r5Beta), Verb: "mv"}},
			wantHit: false,
		},
		{
			name:    "旧路径读不出领域段：不命中——读不出 ≠ 变了",
			renames: []RenameFact{{OldPath: "unprocessed.md", NewPath: r5Path(domAI, r5Beta), Verb: "mv"}},
			wantHit: false,
		},
		{
			name:    "verb 未采到（空串）：不命中——不把缺事实当证据",
			renames: []RenameFact{r5Move(r5Beta, domInfra, domAI, "")},
			wantHit: false,
		},
		{
			name:    "rename 的新路径是别的对象：不命中（索引按当前落盘路径对齐）",
			renames: []RenameFact{r5Move(r5Zeta, domInfra, domAI, "mv")},
			wantHit: false,
		},
		{
			name:    "Renames 为 nil（未采样）：判据② 整体不判",
			renames: nil,
			wantHit: false,
		},
		{
			name:    "Renames 为空集合（采样了但无 R 记录）：不命中",
			renames: []RenameFact{},
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 卡的自报领域与目录段一致 → 判据① 恒不命中，命中只可能来自判据②。
			in := R5ScanOf("", r5Scan(r5Card(r5Beta, domAI, domAI)), tc.renames)
			fs := r5Of(t, in)
			if !tc.wantHit {
				if len(fs) != 0 {
					t.Fatalf("应零命中，实得 %d 条：%+v", len(fs), fs)
				}
				return
			}
			f := r5One(t, in)
			if f.Targets[1] != tc.wantOld || f.Targets[2] != domAI {
				t.Fatalf("targets 旧 / 新领域 = %q / %q，期望 %q / %q（%v）",
					f.Targets[1], f.Targets[2], tc.wantOld, domAI, f.Targets)
			}
			mv := DomainMoves(in)
			if len(mv) != 1 || !reflect.DeepEqual(mv[0].Evidence, []string{DomainEvidenceCrossDomainRename}) {
				t.Fatalf("证据集合 = %+v，期望恰 [%s]", mv, DomainEvidenceCrossDomainRename)
			}
			if !strings.Contains(f.Detail, followNameStatusSpec) {
				t.Fatalf("detail 未写明事实口径 %q：%q", followNameStatusSpec, f.Detail)
			}
		})
	}
}

// TestR5EgOwnRenameNotFlagged：`eg` 自己产生的跨领域 rename **一条都不报** ——
// 判定复用 R2 的同一个 helper（IsForeignVerb → model.KnownVerbs()，恒 8 值），
// 本文件因此逐个已知 verb 遍历，任何一个被误判为外部编辑都立刻变红。
func TestR5EgOwnRenameNotFlagged(t *testing.T) {
	verbs := model.KnownVerbs()
	if len(verbs) != 8 {
		t.Fatalf("KnownVerbs 应恒 8 值（M4 不增减），实得 %d：%v", len(verbs), verbs)
	}
	for _, v := range verbs {
		t.Run(string(v), func(t *testing.T) {
			in := R5ScanOf("", r5Scan(r5Card(r5Beta, domAI, domAI)),
				[]RenameFact{r5Move(r5Beta, domInfra, domAI, string(v))})
			if fs := r5Of(t, in); len(fs) != 0 {
				t.Fatalf("verb=%q 是 eg 自身提交，应零命中，实得 %d 条：%+v", v, len(fs), fs)
			}
			if IsForeignVerb(string(v)) {
				t.Fatalf("verb=%q 被判为外部编辑：verb 白名单真源被绕开了", v)
			}
		})
	}
	// 对照：外部 verb 在同一份输入上必须命中——否则「不误报」会退化成「永不报」。
	in := R5ScanOf("", r5Scan(r5Card(r5Beta, domAI, domAI)),
		[]RenameFact{r5Move(r5Beta, domInfra, domAI, "mv")})
	if f := r5One(t, in); f.Targets[1] != domInfra {
		t.Fatalf("外部 verb 应命中且旧领域 = %q，实得 %v", domInfra, f.Targets)
	}
	// 本文件不得自带 verb 字面量清单（单一真源）：源码里不出现已知 verb 的字面量。
	src := readSourceFile(t, "r5_domain.go")
	for _, v := range verbs {
		if strings.Contains(src, `"`+string(v)+`"`) {
			t.Fatalf("r5_domain.go 内出现 verb 字面量 %q：verb 清单必须唯一真源在 internal/model", v)
		}
	}
}

// TestR5TargetsTripleOrderFixed：targets **恰三元、顺序固定** [对象 ID, 旧领域, 新领域]，
// 且该顺序**不是**排序结果 —— 这三元不参与合同 §2 的「去重 + 字典序升序」。
func TestR5TargetsTripleOrderFixed(t *testing.T) {
	// 刻意挑选：字典序升序会是 [ai, infra, k-…zeta]，与固定三元顺序完全不同。
	in := R5ScanOf("", r5Scan(r5Card(r5Zeta, domAI, domInfra)), nil)
	f := r5One(t, in)
	want := []string{r5Zeta, domInfra, domAI}
	if !reflect.DeepEqual(f.Targets, want) {
		t.Fatalf("targets = %v，期望恰 %v（按位：ID / 旧领域 / 新领域）", f.Targets, want)
	}
	if sorted := NormalizeTargets(f.Targets); reflect.DeepEqual(sorted, f.Targets) {
		t.Fatalf("本用例必须让固定序 ≠ 字典序，否则钉不住顺序：%v", f.Targets)
	}
	// 三元与 DomainMovedTargets 同源、与 DomainMove 三字段逐字一致。
	mv := DomainMoves(in)
	if len(mv) != 1 || !reflect.DeepEqual(DomainMovedTargets(mv[0]), want) {
		t.Fatalf("DomainMovedTargets 与 finding 不同源：%+v", mv)
	}
	// 例外面是**封闭**的：恰 1 个 check 走有序三元，其余 11 个仍是集合语义。
	if OrderedTargetsCheckCount != 1 {
		t.Fatalf("有序 targets 例外必须恰 1 项，实得 %d", OrderedTargetsCheckCount)
	}
	if n, ok := TargetsArity(CheckDomainMoved); !ok || n != DomainMovedTargetArity {
		t.Fatalf("TargetsArity(%s) = %d / %v，期望 %d / true", CheckDomainMoved, n, ok, DomainMovedTargetArity)
	}
	if !TargetsOrdered(CheckDomainMoved) {
		t.Fatalf("%s 必须登记为有序 targets", CheckDomainMoved)
	}
	for _, c := range AllChecks() {
		if c == CheckDomainMoved {
			continue
		}
		if TargetsOrdered(c) {
			t.Fatalf("%s 不该是有序 targets：例外面被扩张了", c)
		}
	}
	// 元数不符 / 有空位一律**报错**，不静默降级成集合语义。
	for _, bad := range [][]string{
		{r5Zeta, domAI}, {r5Zeta, domInfra, domAI, domOps}, {r5Zeta, "", domAI}, nil,
	} {
		if _, err := NewFinding(CheckDomainMoved, bad, "x"); err == nil {
			t.Fatalf("targets=%v 应构造失败（元数 / 空位不合规）", bad)
		}
	}
	// 有序三元**不去重**：重复元素合法（旧 = 新的情况由判定层排除，schema 层不越权）。
	if f2, err := NewFinding(CheckDomainMoved, []string{r5Zeta, domAI, domAI}, "x"); err != nil ||
		len(f2.Targets) != DomainMovedTargetArity {
		t.Fatalf("有序三元不该被去重：%+v / %v", f2, err)
	}
}

// TestR5ReportOnlyNoMoveNoRelationFix：只报告 —— 零 RepairSpec、零写盘、入参不改、幂等，
// 且源文件里没有任何写盘 / 子进程 / 关系补齐的 API 面（结构上做不到，不靠自律）。
func TestR5ReportOnlyNoMoveNoRelationFix(t *testing.T) {
	dir := t.TempDir()
	before := dirSnapshot(t, dir)
	cards := []query.CardEntry{
		r5Card(r5Alpha, domAI, domInfra),
		{ID: r5Beta, Path: r5Path(domAI, r5Beta), Domain: domAI,
			Relations: []model.Relation{{Type: model.RelationSupports, Target: model.RelationEndpoint(r5Alpha)}}},
	}
	in := R5ScanOf(dir, r5Scan(cards...), []RenameFact{r5Move(r5Beta, domInfra, domAI, "mv")})
	fs1, rs1 := checkR5DomainMoved(in)
	fs2, rs2 := checkR5DomainMoved(in)
	if len(rs1) != 0 || len(rs2) != 0 {
		t.Fatalf("R5 必须零 RepairSpec：%d / %d", len(rs1), len(rs2))
	}
	if !reflect.DeepEqual(fs1, fs2) {
		t.Fatalf("同一输入两次产出不同（非纯函数）：%+v vs %+v", fs1, fs2)
	}
	if len(fs1) != 2 {
		t.Fatalf("两个对象各命中一条，应恰 2 条：%+v", fs1)
	}
	if after := dirSnapshot(t, dir); after != before {
		t.Fatalf("检查器产生了磁盘变化：%q → %q", before, after)
	}
	// 入参逐字未改：关系条目、路径、自报领域都还是原值。
	if len(in.Scan.Cards[1].Relations) != 1 ||
		in.Scan.Cards[1].Relations[0].Target != model.RelationEndpoint(r5Alpha) {
		t.Fatalf("入参 relations 被改动（R5 不补关系）：%+v", in.Scan.Cards[1].Relations)
	}
	if in.Scan.Cards[0].Domain != domInfra || in.Scan.Cards[0].Path != r5Path(domAI, r5Alpha) {
		t.Fatalf("入参领域 / 路径被改写：%+v", in.Scan.Cards[0])
	}
	if in.Renames[0].OldPath != r5Path(domInfra, r5Beta) {
		t.Fatalf("入参 rename 事实被改写：%+v", in.Renames[0])
	}
	// 源码反证：写盘 / 子进程 / 关系补齐 / Git 句柄的 API 面一个都不出现。
	src := readSourceFile(t, "r5_domain.go")
	for _, bad := range []string{
		"os.WriteFile", "os.Create", "os.Rename", "os.Remove", "os.MkdirAll",
		"AddRelation", "os/exec", "exec.Command", "git.Repo", "*git.", "store.",
	} {
		if strings.Contains(src, bad) {
			t.Fatalf("r5_domain.go 出现禁用 API 面 %q（只报告 + 包内三条零）", bad)
		}
	}
	// detail 逐字写明「只报告」的边界，供人工与脚本复算。
	if !strings.Contains(fs1[0].Detail, "只报告") {
		t.Fatalf("detail 未写明只报告边界：%q", fs1[0].Detail)
	}
}

// TestR5TruthTableTwoConditions：两条判据的**四象限**真值表（析取；同一对象恒一条），
// 两条同时命中时领域对取判定行序在前者（判据①），证据两条都进 detail。
func TestR5TruthTableTwoConditions(t *testing.T) {
	cases := []struct {
		name     string
		claim    string
		verb     string
		wantHit  bool
		wantOld  string
		wantEvid []string
	}{
		{name: "① 假 ② 假", claim: domAI, verb: "", wantHit: false},
		{name: "① 真 ② 假", claim: domInfra, verb: "", wantHit: true, wantOld: domInfra,
			wantEvid: []string{DomainEvidenceSegmentMismatch}},
		{name: "① 假 ② 真", claim: domAI, verb: "mv", wantHit: true, wantOld: domOps,
			wantEvid: []string{DomainEvidenceCrossDomainRename}},
		{name: "① 真 ② 真：恒一条，领域对取判据①", claim: domInfra, verb: "mv", wantHit: true, wantOld: domInfra,
			wantEvid: []string{DomainEvidenceCrossDomainRename, DomainEvidenceSegmentMismatch}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rn []RenameFact
			if tc.verb != "" {
				rn = []RenameFact{r5Move(r5Beta, domOps, domAI, tc.verb)}
			}
			in := R5ScanOf("", r5Scan(r5Card(r5Beta, domAI, tc.claim)), rn)
			fs := r5Of(t, in)
			if !tc.wantHit {
				if len(fs) != 0 {
					t.Fatalf("应零命中，实得 %+v", fs)
				}
				return
			}
			f := r5One(t, in)
			if f.Targets[1] != tc.wantOld {
				t.Fatalf("旧领域 = %q，期望 %q", f.Targets[1], tc.wantOld)
			}
			mv := DomainMoves(in)
			if len(mv) != 1 || !reflect.DeepEqual(mv[0].Evidence, tc.wantEvid) {
				t.Fatalf("证据 = %+v，期望 %v", mv, tc.wantEvid)
			}
			for _, e := range mv[0].Evidence {
				if !IsKnownDomainEvidence(e) {
					t.Fatalf("证据 %q 不在封闭两值内", e)
				}
			}
		})
	}
	// 证据集合封闭：恰 2 值，第三个取值一律 false。
	if ev := DomainEvidences(); len(ev) != DomainEvidenceCount || DomainEvidenceCount != 2 {
		t.Fatalf("证据集合应恰 2 值：%v", ev)
	}
	if IsKnownDomainEvidence("moved_by_hand") {
		t.Fatalf("证据集合被扩张了")
	}
}

// TestR5DedupOneFindingPerObject：同一对象恒一条、多对象按 (path, id) 升序可复算，
// 且材料笔记与知识卡同构对待（notes 分区同样判）。
func TestR5DedupOneFindingPerObject(t *testing.T) {
	scan := &query.ScanResult{
		Cards: []query.CardEntry{
			r5Card(r5Zeta, domAI, domInfra),
			r5Card(r5Alpha, domAI, domOps),
		},
		Notes: []query.NoteEntry{
			{ID: r5Note, Path: r5NotePath(domAI, r5Note), Domain: domInfra},
		},
	}
	// 同一对象同时有两条外部 rename 记录 + 判据① 也命中：仍恰一条。
	in := R5ScanOf("", scan, []RenameFact{
		r5Move(r5Zeta, domOps, domAI, "mv"),
		r5Move(r5Zeta, domInfra, domAI, "chore"),
	})
	fs := r5Of(t, in)
	if len(fs) != 3 {
		t.Fatalf("三个对象应恰 3 条（同一对象不得重复计数）：%+v", fs)
	}
	// DomainMoves 的顺序按 (path, id) 升序：alpha < note < zeta（路径同前缀，ID 决定）。
	mv := DomainMoves(in)
	got := []string{mv[0].ID, mv[1].ID, mv[2].ID}
	want := []string{r5Alpha, r5Zeta, r5Note} // knowledge/… < notes/…
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("顺序不可复算：%v，期望 %v", got, want)
	}
	// 类别标签同源（不另定义一套类别串）。
	kinds := map[string]string{r5Alpha: KindCard, r5Zeta: KindCard, r5Note: KindNote}
	for _, m := range mv {
		if m.Kind != kinds[m.ID] {
			t.Fatalf("%s 的类别 = %q，期望 %q", m.ID, m.Kind, kinds[m.ID])
		}
	}
	// Scan 为 nil（未扫描）→ 空集合：不把「没扫描」当成「没有对象」。
	if fs := r5Of(t, Input{Renames: []RenameFact{r5Move(r5Zeta, domInfra, domAI, "mv")}}); len(fs) != 0 {
		t.Fatalf("未扫描时应零产出：%+v", fs)
	}
}

// TestR5NoDoubleCountWithR4DuplicateID：与 R4 的 duplicate_id（E11）零重复计数 ——
// 同一 ID 落在两个**不同领域**目录时，那件落盘事实归 E11 独家承载，R5 整体跳过该 ID；
// 反证同时锁住「别放宽成永不报」：把重复消掉后 W18 必须立刻出现。
func TestR5NoDoubleCountWithR4DuplicateID(t *testing.T) {
	dup := r5Scan(
		query.CardEntry{ID: r5Alpha, Path: r5Path(domAI, r5Alpha), Domain: domInfra},
		query.CardEntry{ID: r5Alpha, Path: r5Path(domInfra, r5Alpha), Domain: domInfra},
	)
	in := R5ScanOf("", dup, []RenameFact{r5Move(r5Alpha, domOps, domAI, "mv")})
	if fs := r5Of(t, in); len(fs) != 0 {
		t.Fatalf("同一 ID 落在 ≥2 个文件时 R5 必须整体跳过（归 E11），实得 %+v", fs)
	}
	// R4 侧仍恰报一条 duplicate_id：这件事实**有人**承载，不是两边都漏。
	if fs, _ := checkR4Structure(in); len(pick(fs, CheckDuplicateID)) != 1 {
		t.Fatalf("duplicate_id 应恰 1 条（由 R4 独家承载）：%+v", fs)
	}
	// 反向：ID 不重复后 W18 立刻出现（判据没被放宽成永不报）。
	single := r5Scan(query.CardEntry{ID: r5Alpha, Path: r5Path(domAI, r5Alpha), Domain: domInfra})
	if f := r5One(t, R5ScanOf("", single, nil)); f.Targets[0] != r5Alpha {
		t.Fatalf("去重后应恰 1 条 W18：%+v", f)
	}
}

// TestR5NoDoubleCountWithR1R2R3：同一份输入里 R1 / R2 / R3 / R5 各记各自那一件事实，
// 谁都不替谁记账 —— R5 不读 Status（未提交）、不读 reviewed_at、不读 relations[]。
func TestR5NoDoubleCountWithR1R2R3(t *testing.T) {
	rel := model.Relation{Type: model.RelationSupports, Target: model.RelationEndpoint("k-20261199-gone")}
	card := query.CardEntry{
		ID: r5Alpha, Path: r5Path(domAI, r5Alpha), Domain: domInfra,
		UpdatedAt: "2026-11-26T10:00:00+08:00", Relations: []model.Relation{rel},
	}
	in := Input{
		Scan:    r5Scan(card),
		Status:  git.Status{Changes: []git.Change{{Code: " M", Path: r5Path(domAI, r5Alpha)}}},
		Renames: []RenameFact{r5Move(r5Alpha, domOps, domAI, "mv")},
	}
	// R5 只报 1 条 W18；把 Status 清空后 W18 条数**一字不变**（证明 R5 不读未提交事实）。
	base := len(r5Of(t, in))
	noStatus := in
	noStatus.Status = git.Status{}
	if got := len(r5Of(t, noStatus)); got != base || base != 1 {
		t.Fatalf("R5 条数受 Status 影响或不为 1：%d vs %d", base, got)
	}
	// 全注册表跑一遍：四个 check 各恰 1 条，W18 不吞并也不被吞并。
	res := Run(in)
	for _, c := range []string{CheckGitUncommitted, CheckReviewedAtMissing, CheckRelationTargetMissing, CheckDomainMoved} {
		if n := len(pick(res.Findings, c)); n != 1 {
			t.Fatalf("%s 应恰 1 条，实得 %d（四项不重复计数）：%+v", c, n, res.Findings)
		}
	}
	// R5 恒零 RepairSpec：整条流水线里的 RepairSpec 都不来自 R5。
	for _, r := range res.Repairs {
		if r.Check == CheckDomainMoved {
			t.Fatalf("RepairSpec 里出现 %s：R5 只报告", CheckDomainMoved)
		}
	}
}

// readSourceFile 只读读取本包内的源文件（用于 grep 形态的反证）。
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读源文件 %s 失败：%v", name, err)
	}
	return string(b)
}
