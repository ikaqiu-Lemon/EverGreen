package proposal_test

// T-…-033 的机器判据：提案的**落盘形态**（目录 / ID / frontmatter 键 / 正文七分区）
// 与 round-trip 逐字节保真。
//
// 判据来源：提案合同 `2026-10-10-m3-proposal-state-contract.md` §7.1 / §7.2 / §7.3、
// §9 机器判定表的「落盘形态 / 提案无 reviewed_at」两行，以及
// `2026-10-13-m3-prestart-adjudication.md` §7 的 A-23 裁决（本包不写盘、不发 commit、不组报告）。
//
// 本文件不测状态机迁移（034）、superseded 触发（035）、execution 回写（036）与任何 CLI 子命令（040）。

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
)

// demoTemplate 是一份用于形态判定的模板输入（取自提案合同 §7.2 的 yaml 示例值）。
func demoTemplate(t *testing.T) proposal.Template {
	t.Helper()
	date, err := model.ParseDate("2026-07-01")
	if err != nil {
		t.Fatalf("ParseDate：%v", err)
	}
	id, err := proposal.NewID(date, 1)
	if err != nil {
		t.Fatalf("NewID：%v", err)
	}
	if id != "p-20260701-001" {
		t.Fatalf("NewID = %q，期望 p-20260701-001", id)
	}
	return proposal.Template{
		ID:        id,
		Title:     "逻辑删除 vendor 基准原文",
		CreatedAt: date.String(),
		Targets:   []string{"s-20260605-vendor-bench"},
		Impact: proposal.Impact{
			ExitsDefaultView:     []string{"s-20260605-vendor-bench"},
			CardsLosingSupport:   []string{"k-20260412-moe-routing-cost"},
			AffectedMaterialRels: 2,
			AffectedRelations:    0,
			StaleReviews:         []string{"r-20260620-moe-cost"},
		},
	}
}

func render(t *testing.T) []byte {
	t.Helper()
	raw, err := proposal.RenderTemplate(demoTemplate(t))
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	return raw
}

// TestProposalFileLayout —— 落盘形态可判（合同 §7.1 / §7.2 / §7.3 三段一次判完）：
// ① 路径匹配 `proposals/p-<yyyymmdd>-<3d>.md`；② frontmatter 键集合与 §7.2 逐键相等
// （含 execution.written_paths / unwritten_paths 两新键）；③ 正文 H2 恰 7 个且顺序逐字一致。
func TestProposalFileLayout(t *testing.T) {
	tpl := demoTemplate(t)
	raw := render(t)

	// ① 路径：默认落位 = proposals/<id>.md，且落在 vault 根的 proposals/ 下。
	rel := proposal.Rel(tpl.ID)
	if rel != "proposals/p-20260701-001.md" {
		t.Fatalf("默认落位 = %q，期望 proposals/p-20260701-001.md", rel)
	}
	if !proposal.MatchRel(rel) {
		t.Fatalf("%q 不匹配落位判据 %s", rel, proposal.RelPattern)
	}
	if !proposal.IsProposalRel(rel) {
		t.Fatalf("%q 必须被识别为提案控制面路径", rel)
	}
	for _, bad := range []string{
		"proposals/p-20260701-1.md",       // 序号非三位
		"proposals/p-2026071-001.md",      // 日期非 8 位
		"proposals/p-20260701-vendor.md",  // 第三段不是序号
		"domains/ai/knowledge/p-1.md",     // 不在 proposals/ 下
		"proposals/sub/p-20260701-001.md", // 默认落位不含子目录
	} {
		if proposal.MatchRel(bad) {
			t.Fatalf("%q 不应匹配落位判据", bad)
		}
	}
	// 真实落一个文件：绝对路径的尾部必须逐字匹配落位形态（vault 根即库根）。
	vault := t.TempDir()
	abs := filepath.Join(vault, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	tailRE := regexp.MustCompile(`proposals/p-[0-9]{8}-[0-9]{3}\.md$`)
	if !tailRE.MatchString(filepath.ToSlash(abs)) {
		t.Fatalf("落盘路径 %q 不匹配 proposals/p-<yyyymmdd>-<3d>.md", abs)
	}

	// ② frontmatter 键集合逐键相等（顶层 8 键 + 三个嵌套块的子键集合）。
	f, err := proposal.Parse(raw)
	if err != nil {
		t.Fatalf("Parse：%v", err)
	}
	keys, err := f.Doc.FMKeys()
	if err != nil {
		t.Fatalf("FMKeys：%v", err)
	}
	if got, want := strings.Join(keys, ","), strings.Join(proposal.FMKeys(), ","); got != want {
		t.Fatalf("frontmatter 顶层键 = [%s]，期望逐键相等于 [%s]", got, want)
	}
	if len(proposal.FMKeys()) != 8 {
		t.Fatalf("顶层键数 = %d，合同 §7.2 恰 8 个", len(proposal.FMKeys()))
	}
	if err := proposal.ValidateLayout(f); err != nil {
		t.Fatalf("ValidateLayout：%v", err)
	}
	// 两个新定键必须在 execution 里逐字存在（合同 M-3 / 登记项 A-21）。
	for _, k := range []string{proposal.KeyWrittenPaths, proposal.KeyUnwrittenPaths} {
		if !bytes.Contains(raw, []byte("  "+k+": []\n")) {
			t.Fatalf("execution 缺新定键 %s：\n%s", k, raw)
		}
	}
	// 值也要能读出来：写 / 未写路径两键 round-trip 可见（空数组，不是 nil 之外的形态）。
	if f.P.Execution.Status != proposal.ExecNotStarted {
		t.Fatalf("新建提案 execution.status = %q，期望 not_started", f.P.Execution.Status)
	}
	if f.P.Status != proposal.StatusPending {
		t.Fatalf("新建提案 status = %q，期望 pending", f.P.Status)
	}
	if f.P.Decision.Result != "" || f.P.Decision.SupersededBy != "" {
		t.Fatalf("新建提案 decision 必须留空：%+v", f.P.Decision)
	}
	if !reflect.DeepEqual(f.P.Targets, []string{"s-20260605-vendor-bench"}) {
		t.Fatalf("targets = %v", f.P.Targets)
	}
	if f.P.Impact.AffectedMaterialRels != 2 || f.P.Impact.AffectedRelations != 0 {
		t.Fatalf("impact 计数未 round-trip：%+v", f.P.Impact)
	}

	// ③ 正文 H2 恰 7 个、顺序逐字一致。
	got := f.Doc.SectionNames()
	want := []string{"推荐修改", "理由与证据", "影响的文件、领域与关系",
		"执行后状态", "不执行的影响", "替代方案", "可应用内容"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("正文 H2 = %v，期望恰 7 个且顺序为 %v", got, want)
	}
	if !reflect.DeepEqual(proposal.BodySections(), want) {
		t.Fatalf("BodySections() = %v，期望 %v", proposal.BodySections(), want)
	}
	if f.Title() != "逻辑删除 vendor 基准原文" {
		t.Fatalf("标题 = %q", f.Title())
	}

	// 反例：多一个 / 少一个 / 顺序错的 H2 都必须被拒（分区判据是「恰等」不是「包含」）。
	for name, mutated := range map[string][]byte{
		"多一个分区": append(append([]byte{}, raw...), []byte("\n## 附录\n")...),
		"少一个分区": bytes.Replace(raw, []byte("\n## 替代方案\n"), []byte("\n"), 1),
		"顺序错":   bytes.Replace(raw, []byte("## 推荐修改"), []byte("## 理由与证据"), 1),
	} {
		if _, err := proposal.Parse(mutated); err == nil {
			t.Fatalf("%s 必须被拒", name)
		} else if k := proposal.KindOf(err); k != proposal.ViolationSections {
			t.Fatalf("%s 的违规种类 = %q，期望 %q", name, k, proposal.ViolationSections)
		}
	}
	// 反例：顶层多一个键 → Parse 仍可读（未知字段逐字保留），但 ValidateLayout 必须拒。
	extra := bytes.Replace(raw, []byte("\nid: "), []byte("\nfoo: 'bar'\nid: "), 1)
	ef, err := proposal.Parse(extra)
	if err != nil {
		t.Fatalf("含未知键的提案必须仍可读出来：%v", err)
	}
	if _, ok := ef.P.Extra["foo"]; !ok {
		t.Fatalf("未知顶层键必须落进 Extra：%+v", ef.P.Extra)
	}
	if err := proposal.ValidateLayout(ef); err == nil {
		t.Fatal("顶层键集合不恰等时 ValidateLayout 必须报错")
	} else if k := proposal.KindOf(err); k != proposal.ViolationFMKeySet {
		t.Fatalf("违规种类 = %q，期望 %q", k, proposal.ViolationFMKeySet)
	}
	// 反例：execution 少一个子键 → 子键集合不恰等。
	less := bytes.Replace(raw, []byte("  unwritten_paths: []\n"), nil, 1)
	lf, err := proposal.Parse(less)
	if err != nil {
		t.Fatalf("Parse：%v", err)
	}
	if err := proposal.ValidateLayout(lf); err == nil {
		t.Fatal("execution 缺 unwritten_paths 时 ValidateLayout 必须报错")
	} else if k := proposal.KindOf(err); k != proposal.ViolationSubKeySet {
		t.Fatalf("违规种类 = %q，期望 %q", k, proposal.ViolationSubKeySet)
	}
}

// TestProposalHasNoReviewedOrDeletedKeys —— 合同 M-6：提案不设
// reviewed_at / deleted_at / deleted_reason，**解析与序列化两侧均不存在**。
func TestProposalHasNoReviewedOrDeletedKeys(t *testing.T) {
	banned := proposal.ForbiddenFMKeys()
	if len(banned) != 3 {
		t.Fatalf("禁用键 = %v，合同恰三键", banned)
	}

	// 序列化侧：模板产出的字节里一个字都不出现。
	raw := render(t)
	for _, k := range banned {
		if bytes.Contains(raw, []byte(k)) {
			t.Fatalf("模板产出含禁用键 %s：\n%s", k, raw)
		}
	}

	// 类型侧：四个结构体的 yaml 标签里没有任何一个禁用键（schema 层不给它们落点）。
	for _, rt := range []reflect.Type{
		reflect.TypeOf(proposal.Proposal{}), reflect.TypeOf(proposal.Impact{}),
		reflect.TypeOf(proposal.Decision{}), reflect.TypeOf(proposal.Exec{}),
	} {
		for i := 0; i < rt.NumField(); i++ {
			tag := rt.Field(i).Tag.Get("yaml")
			for _, k := range banned {
				if tag == k {
					t.Fatalf("%s.%s 的 yaml 标签是禁用键 %s", rt.Name(), rt.Field(i).Name, k)
				}
			}
		}
	}

	// 解析侧：手工塞进去也必须被拒（不是「忽略」，是「报错」——否则等于默许它存在）。
	for _, k := range banned {
		bad := bytes.Replace(raw, []byte("\nstatus: "), []byte("\n"+k+": '2026-07-02'\nstatus: "), 1)
		if _, err := proposal.Parse(bad); err == nil {
			t.Fatalf("含 %s 的提案必须被拒", k)
		} else if kind := proposal.KindOf(err); kind != proposal.ViolationForbiddenKey {
			t.Fatalf("含 %s 时违规种类 = %q，期望 %q", k, kind, proposal.ViolationForbiddenKey)
		}
	}
}

// TestProposalRoundTripByteEqual —— round-trip 保真：读入 → 写出逐字节相等
// （与 M1 的 B1/B2 round-trip 同源口径）。含「未知键 / 注释 / 双引号风格」的手写提案同样成立。
func TestProposalRoundTripByteEqual(t *testing.T) {
	cases := map[string][]byte{
		"模板产出": render(t),
		"手写变体": []byte(handWritten),
	}
	for name, raw := range cases {
		f, err := proposal.Parse(raw)
		if err != nil {
			t.Fatalf("%s Parse：%v", name, err)
		}
		if out := f.Render(); !bytes.Equal(out, raw) {
			t.Fatalf("%s round-trip 不等：\n原（%d）=%q\n出（%d）=%q", name, len(raw), raw, len(out), out)
		}
		if err := proposal.SelfCheck(raw); err != nil {
			t.Fatalf("%s SelfCheck：%v", name, err)
		}
	}
	// 手写变体的未知键与注释都在，且键序 / 引号风格逐字未变（不经序列化器的直接后果）。
	f, err := proposal.Parse([]byte(handWritten))
	if err != nil {
		t.Fatalf("Parse：%v", err)
	}
	if _, ok := f.P.Extra["reviewer"]; !ok {
		t.Fatalf("未知顶层键 reviewer 必须落进 Extra：%+v", f.P.Extra)
	}
	if _, ok := f.P.Execution.Extra["retry_count"]; !ok {
		t.Fatalf("未知嵌套键 retry_count 必须落进 execution.Extra：%+v", f.P.Execution.Extra)
	}
	if !strings.Contains(string(f.Render()), "# 注释必须逐字保留") {
		t.Fatal("YAML 注释必须逐字保留")
	}
	if f.P.Status != proposal.StatusApproved || f.P.Decision.Result != proposal.StatusApproved {
		t.Fatalf("手写变体的 status / decision.result 未读出：%q / %q", f.P.Status, f.P.Decision.Result)
	}
	if body := string(f.SectionBody("可应用内容")); !strings.Contains(body, "remove_relation") {
		t.Fatalf("分区正文取用失败：%q", body)
	}
}

// handWritten 是一份**手工维护过**的提案：键序不同、双引号标量、YAML 注释、
// 未知顶层键与未知嵌套键、正文分区内有内容。它必须能被读出来且 round-trip 逐字节相等。
const handWritten = `---
# 注释必须逐字保留
id: "p-20260701-007"
type: logical_delete
status: approved
created_at: 2026-07-01
targets:
  - s-20260605-vendor-bench
impact:
  exits_default_view: []
  cards_losing_support: []
  affected_material_rels: 0
  affected_relations: 0
  stale_reviews: []
decision:
  result: approved
  reason: "证据不足，删除"
  superseded_by:
execution:
  status: not_started
  attempted_at:
  reason:
  git_commit:
  written_paths: []
  unwritten_paths: []
  retry_count: 0
reviewer: zhouhang
---

# 手写提案

## 推荐修改

删除该原文。

## 理由与证据

见 k-20260412-moe-routing-cost。

## 影响的文件、领域与关系

## 执行后状态

## 不执行的影响

## 替代方案

## 可应用内容

- remove_relation
`
