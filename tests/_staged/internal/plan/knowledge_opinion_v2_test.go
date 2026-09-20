package plan

// knowledge_opinion_v2_test.go —— Schema v2 写口的验收判据
// （T-evergreen.knowledge_opinion_split-158614-003；契约 §4.1–§4.5 / D-6 / D-7）。
//
// 本文件逐条对应 T-…-003 的 Acceptance，一条判据一支用例，全部**落到字节或诊断**上，
// 不用「函数被调用过」这类间接证据：
//
//	① create_card 与 create_knowledge 同语义 → 落盘字节等价，且别名侧多一条 I1；
//	② write_note 的 blocks[] 顺序逐字保留（不排序 / 不去重 / 不重排）；
//	③ blocks[] 与 v1 sections{} 互斥 → E2、零写入；
//	④ 空 blocks[] / 全 role: agent → E2、零写入；
//	⑤ 6 个 H2 的原文配 2 个来源块 → W21；配 4 个 → 不产出；--strict 下 W21 不升级；
//	⑥ plan_version: 1 仍可执行（走 v1 固定分区口径）并产出 I1 兼容提示；
//	⑦ create_opinion 落盘的 frontmatter 含 validation: pending。
//
// 判据都用**实盘执行**复算（Execute + 真实 store），而不是只看 Actions：
// 「校验期零展开」只是零写入的必要条件，落盘比对才是充分条件。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// v2Plan 造一份 plan_version: 2 的 plan，base 覆盖全库（避免 W6 跳过掩盖判定）。
//
// 与 m3Plan 的唯一差别就是版本号：v2 的顶层 8 键与 v1 逐字相同（契约 §4.1 末条），
// 所以这里不能顺手改别的键——一旦改了，「顶层键一个未动」这条事实就没有用例守着了。
func v2Plan(t *testing.T, files map[string]string, ops string) string {
	t.Helper()
	var base []string
	for rel, content := range files {
		base = append(base, fmt.Sprintf("%q:%q", rel, store.ContentHash([]byte(content))))
	}
	return fmt.Sprintf(`{"plan_version":%d,"verb":"process","domain":"ai-infra",`+
		`"reason":"Schema v2 用例","requirement_ids":["EG-KNW-04"],"base":{%s},"ops":[%s]}`,
		PlanVersion, strings.Join(base, ","), ops)
}

// v2Run 用给定库跑一次 v2 校验（自动路径 P-A：不带命令行佐证）。
//
// 刻意不设 UserRequest：本批次的四个新 op 全都是 Agent 自动路径的写口，
// 带上用户佐证反而会让矩阵取到 P-U 列，考不到自动路径的真实门闸。
func v2Run(t *testing.T, files map[string]string, ops string) *Result {
	t.Helper()
	return run(t, vault(t, files), v2Plan(t, files, ops))
}

// execOn 在一份临时实盘库上真实执行一次校验结果，返回库根目录。
func execOn(t *testing.T, files map[string]string, res *Result) (string, *ExecResult) {
	t.Helper()
	if res.Failed() {
		t.Fatalf("执行前提不成立（有 error）：%v", codes(res.Errors))
	}
	dir := t.TempDir()
	writeVault(t, dir, files)
	out := Execute(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Failures) != 0 {
		t.Fatalf("执行期不应有失败：%+v", out.Failures)
	}
	return dir, out
}

// v2Files 是本文件的基础库：一张 v1 存量卡（兼容面）、一篇笔记、一份原文。
//
// 复用 plan_test.go 的 card/note/source 夹具，不另造一套：这三份夹具本身就是
// v1 存量形态（卡里有 `解释与依据` / `理解自检`），正好同时作为「v2 写口在存量库上
// 也要能跑」的场地。
func v2Files() map[string]string {
	return map[string]string{
		"domains/ai-infra/knowledge/k-20260901-attention.md": card("k-20260901-attention"),
		"domains/ai-infra/notes/n-20260901-attention.md":     note("n-20260901-attention"),
		"sources/s-20260901-attention.md":                    source("s-20260901-attention"),
	}
}

// knowledgeOp 造一条建卡 op；opName 取 `create_knowledge`（规范名）或 `create_card`（别名）。
//
// 两次调用只换 op 名，其余字段逐字相同——「同语义」这个前提必须由构造函数保证，
// 否则字节比对通不过时无法区分「别名不等价」与「用例给了两份不同的输入」。
func knowledgeOp(opName string) string {
	return `{"op":"` + opName + `","card_id":"k-20261017-scaling","title":"缩放点积注意力",
 "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support",
 "reason":"原文第 3 节给出缩放因子的来历"}],
 "sections":{"知识内容":"缩放因子是 1/sqrt(d_k)。\n","条件与边界":"仅在点积注意力下成立。\n"}}`
}

const scalingRel = "domains/ai-infra/knowledge/k-20261017-scaling.md"

// TestCreateCardAliasIsByteEquivalent —— 验收①：别名与规范名落盘**字节等价**，
// 且别名侧额外产出一条 I1 迁移提示。
//
// 为什么必须比字节而不是比 Actions：别名改写发生在 expand 阶段（契约 §4.4），
// 若哪天有人在 validate 或 executor 里补一份 `case "create_card"`，Actions 层面
// 很可能仍然长得一样，但落盘模板、分区顺序或 frontmatter 键序会悄悄分叉。
// 字节等价是唯一能把这种分叉当场抓住的判据。
func TestCreateCardAliasIsByteEquivalent(t *testing.T) {
	files := v2Files()

	canonical := v2Run(t, files, knowledgeOp(OpCreateKnowledge))
	aliased := v2Run(t, files, knowledgeOp(OpCreateCard))

	canonDir, canonOut := execOn(t, files, canonical)
	aliasDir, aliasOut := execOn(t, files, aliased)

	got := readVaultFile(t, canonDir, scalingRel)
	want := readVaultFile(t, aliasDir, scalingRel)
	if got != want {
		t.Fatalf("别名与规范名落盘字节不等价：\n--- create_knowledge ---\n%s\n--- create_card ---\n%s", got, want)
	}
	if len(canonOut.CardsCreated) != 1 || len(aliasOut.CardsCreated) != 1 {
		t.Fatalf("两侧都应恰建一张卡：canonical=%v alias=%v",
			canonOut.CardsCreated, aliasOut.CardsCreated)
	}

	// 规范名侧不得出现「别名已改写」这条提示；别名侧必须有，且指名道姓两个 op 名。
	if d, ok := findMentioning(infosOf(canonical), OpCreateCard); ok {
		t.Fatalf("规范名 plan 不应产出别名迁移提示：%+v", d)
	}
	d, ok := findMentioning(infosOf(aliased), OpCreateCard)
	if !ok {
		t.Fatalf("别名 plan 必须产出一条 I1 迁移提示，实得 infos=%v", codes(infosOf(aliased)))
	}
	if d.Code != I1 {
		t.Fatalf("别名迁移提示必须是 I1（info 级、不拦截），实得 %s", d.Code)
	}
	if !strings.Contains(d.Message, OpCreateKnowledge) {
		t.Fatalf("迁移提示必须点出规范名 %s，实得：%s", OpCreateKnowledge, d.Message)
	}
	// 别名侧的诊断数恰好只多这一条：改写不得顺带产生别的噪声。
	if len(infosOf(aliased)) != len(infosOf(canonical))+1 {
		t.Fatalf("别名侧应恰多一条 info：canonical=%v alias=%v",
			codes(infosOf(canonical)), codes(infosOf(aliased)))
	}
}

// infosOf 取一次校验里的 info 级诊断。
//
// 单列成小函数的理由：Result 只有 Errors / Warnings 两个篮子，info 与 warning 同住
// Warnings（分级由 Diagnostic.Level 表达，见 validator.add）。用例若直接遍历
// res.Warnings 数 info，就会把真正的 warning 一起数进来，让「别名只多一条 info」
// 这类计数判据在任意 warning 波动时假红。
func infosOf(res *Result) []Diagnostic {
	out := make([]Diagnostic, 0, len(res.Warnings))
	for _, d := range res.Warnings {
		if d.Level == LevelInfo {
			out = append(out, d)
		}
	}
	return out
}

// findMentioning 在诊断集合里找第一条消息包含 needle 的诊断。
func findMentioning(diags []Diagnostic, needle string) (Diagnostic, bool) {
	for _, d := range diags {
		if strings.Contains(d.Message, needle) {
			return d, true
		}
	}
	return Diagnostic{}, false
}

// noteBlocksOp 造一条 v2 write_note op：blocks 数组按给定顺序原样传入，omissions 传空数组
// （本 op 声明「无删除」）。source_ref 覆盖校验属 T12-2A，需要覆盖 Source 全部非空行的用例
// 改用 noteBlocksOpOm 显式给 omissions。
func noteBlocksOp(noteID string, blocks ...string) string {
	return noteBlocksOpOm(noteID, nil, blocks...)
}

// noteBlocksOpOm 造一条 v2 write_note op，omissions 显式给出（数组按输入顺序原样保留，
// 不排序 / 不去重 / 不重排；本批不预设 omissions 的任何「报告序」语义）。
func noteBlocksOpOm(noteID string, omissions []string, blocks ...string) string {
	return `{"op":"write_note","source":"s-20260901-attention","note_id":"` + noteID + `",
 "blocks":[` + strings.Join(blocks, ",") + `],"omissions":[` + strings.Join(omissions, ",") + `]}`
}

// srcBlock 造一个带 source_ref 的 source 块（契约 §4.2 第 2 条：source 块必给非空 ref）。
func srcBlock(ref, heading, body string) string {
	return fmt.Sprintf(`{"role":"source","source_ref":%q,"heading":%q,"body":%q}`, ref, heading, body)
}

// agentBlock 造一个 agent 块。T12-3 起 agent 块必须声明非空 annotation（契约 §4.2.2）；
// 这里取内置 `supplement`，其固定渲染标签「补充」恰与旧 store.AgentBlockMarker 一致，
// 因此本文件既有的「按标记 + 顺序断言落盘字节」判据一字不改仍成立。
func agentBlock(heading, body string) string {
	return fmt.Sprintf(`{"role":"agent","annotation":"supplement","heading":%q,"body":%q}`, heading, body)
}

// TestWriteNoteBlocksOrderIsPreservedVerbatim —— 验收②：落盘顺序 == 数组顺序。
//
// 夹具刻意给**字典序倒置**的 heading（丁 / 丙 / 乙 / 甲）：任何一次「顺手排序」
// 都会让落盘序变成正序而当场判红。同时混入一个 agent 块，证明两种角色共享同一条
// 顺序（不是「先排 source 再排 agent」）。
func TestWriteNoteBlocksOrderIsPreservedVerbatim(t *testing.T) {
	files := v2Files()
	// 正文恰四个非空物理行（L1..L4），供四个 source 块各覆盖一行；块的落盘顺序与
	// source_ref 无关，倒置的 heading 仍钉住「不排序」，而 refs 按 blocks 顺序递增合法。
	files["sources/s-20260901-attention.md"] =
		covSource("s-20260901-attention", "第四节正文。\n第三节正文。\n第二节正文。\n第一节正文。\n")
	res := v2Run(t, files, noteBlocksOp("n-20261017-order",
		srcBlock("L1-L1", "丁 第四节", "第四节正文。"),
		srcBlock("L2-L2", "丙 第三节", "第三节正文。"),
		agentBlock("", "这一段是我补的。"),
		srcBlock("L3-L3", "乙 第二节", "第二节正文。"),
		srcBlock("L4-L4", "甲 第一节", "第一节正文。")))

	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("write_note 必须真实落盘")
	}
	raw := readVaultFile(t, dir, "domains/ai-infra/notes/n-20261017-order.md")

	wantOrder := []string{"### 丁 第四节", "### 丙 第三节",
		store.AgentBlockMarker + "这一段是我补的。", "### 乙 第二节", "### 甲 第一节"}
	at := 0
	for _, want := range wantOrder {
		idx := strings.Index(raw[at:], want)
		if idx < 0 {
			t.Fatalf("落盘正文里找不到 %q（或顺序被重排）：\n%s", want, raw)
		}
		at += idx + len(want)
	}
	// 顺序对了还不够：块数必须一致（不去重、不合并、不丢块）。
	if got := strings.Count(raw, "### "); got != 4 {
		t.Fatalf("四个带 heading 的块应渲染出 4 个 H3，实得 %d：\n%s", got, raw)
	}
}

// TestWriteNoteBlocksAndSectionsAreMutuallyExclusive —— 验收③：两者同时给出 → E2、零写入。
func TestWriteNoteBlocksAndSectionsAreMutuallyExclusive(t *testing.T) {
	files := v2Files()
	res := v2Run(t, files, `{"op":"write_note","source":"s-20260901-attention",
 "note_id":"n-20261017-both","blocks":[`+srcBlock("L1-L1", "甲", "正文。")+`],
 "sections":{"整理正文":"另一份正文。\n"}}`)

	d := requireError(t, res, E2)
	if !strings.Contains(d.Path, "blocks") {
		t.Fatalf("互斥诊断的字段路径应指向 blocks，实得 %q", d.Path)
	}
	assertZeroWrite(t, files, res, "domains/ai-infra/notes/n-20261017-both.md")
}

// TestWriteNoteBlocksEmptyOrAllAgent —— 验收④：空数组 / 全 agent → E2、零写入。
//
// 两种成因分开写在一张表里：空数组是「声称按块整理却一块都没给」，全 agent 是
// 「通篇没有来源内容」。二者都判 E2，但诊断文案必须能区分——把它们折叠成一句
// 「blocks 不合法」会让写 plan 的人不知道该补什么。
func TestWriteNoteBlocksEmptyOrAllAgent(t *testing.T) {
	cases := []struct {
		name   string
		blocks []string
		want   string // 诊断文案必须出现的字样
	}{
		{"空数组", nil, "为空"},
		{"全 agent", []string{agentBlock("甲", "我的推导。"), agentBlock("乙", "继续推导。")},
			string(NoteBlockAgent)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := v2Files()
			rel := "domains/ai-infra/notes/n-20261017-empty.md"
			res := v2Run(t, files, noteBlocksOp("n-20261017-empty", c.blocks...))
			d := requireError(t, res, E2)
			if !strings.Contains(d.Message, c.want) {
				t.Fatalf("诊断文案应点出成因 %q，实得：%s", c.want, d.Message)
			}
			assertZeroWrite(t, files, res, rel)
		})
	}
}

// assertZeroWrite 断言一次失败的校验真的**零写入**：既零展开，也不在实盘留下文件。
//
// 两层都查的理由见文件头：Actions 为空只说明校验期没展开，若哪天 error 路径被误接到
// executor 上，只有磁盘比对能抓住。
func assertZeroWrite(t *testing.T, files map[string]string, res *Result, rel string) {
	t.Helper()
	if len(res.Actions) != 0 {
		t.Fatalf("E2 必须零展开，实得 %d 条 action", len(res.Actions))
	}
	dir := t.TempDir()
	writeVault(t, dir, files)
	before := map[string]string{}
	for r := range files {
		before[r] = readVaultFile(t, dir, r)
	}
	Execute(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)})
	for r, want := range before {
		if got := readVaultFile(t, dir, r); got != want {
			t.Fatalf("零写入被破坏：%s 字节发生变化", r)
		}
	}
	if _, err := store.New(dir).Read(rel); err == nil {
		t.Fatalf("零写入被破坏：%s 竟被创建", rel)
	}
}

// sixHeadingSource 造一份**恰 6 个 H2** 的原文（W21 判定的分母）。
//
// 保留一篇像样文章的形态：一个 H1 文档标题、六个 H2 章节、每节一段正文，节与节之间留空行。
// CountBodyAnchors 只数 H2 + H3，故锚点恰为 6（H1 不计）。正文的物理行布局固定为 27 行
// （L1 空行 / L2 H1 / L3 空 / L4 H2 / L5 空 / L6 正文 / …每节 4 行 / 末尾 L27 空行），非空行是
// L2 及此后每隔一行的 H2 与正文行。这样 W21（看来源块数 vs 锚点数）可以用「若干 source_ref
// 区间不重不漏地分割覆盖全部非空行、omissions=[]」来满足 T12-2A 的覆盖校验，两条判据互不
// 干扰——而不必拿 omission 把真实章节谎称为「本次未整理」（那违反 omissions 只删页面噪声）。
func sixHeadingSource(id string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `---
id: %s
url: https://example.com/long
title: 六节长文
saved_at: '2026-09-01T10:00:00+08:00'
---

# 六节长文

`, id)
	for i := 1; i <= 6; i++ {
		fmt.Fprintf(&b, "## 第 %d 节\n\n第 %d 节正文。\n\n", i, i)
	}
	return b.String()
}

// TestW21StructureCoverage —— 验收⑤：6 个 H2 配 2 个来源块 → W21；配 3 / 4 个 → 不产出。
//
// 阈值取 ceil(6/2) = 3，所以 2 个块判、3 / 4 个块不判；等号侧（恰 3 个块）单独取一例，
// 这是「差一」回归唯一能被抓住的地方。阈值本身用 CoverageThreshold 复算而不是写死，
// 但这里仍显式钉住它当前等于 3 —— 只有把「口径」与「取值」两件事都钉住，
// 日后有人偷偷把 ceil 改成 floor 或把除数改掉才会当场变红，而不是让 2 / 4 这组样例
// 恰好在新口径下也成立而蒙混过关。
//
// 每个 case 的来源块都用 source_ref 把 27 行正文的全部非空行不重不漏地**分割**覆盖，
// omissions=[]（本次无删除）：覆盖并集的闭合与 W21 的「来源块数 vs 锚点数」判定彼此独立，
// 用真实的范围分割证明二者互不干扰，而不是拿 omission 把真实章节标成「未整理」来凑合法。
func TestW21StructureCoverage(t *testing.T) {
	if want := CoverageThreshold(6); want != 3 {
		t.Fatalf("阈值复算口径变了：ceil(6/2) 应为 3，实得 %d", want)
	}
	files := v2Files()
	files["sources/s-20260901-attention.md"] = sixHeadingSource("s-20260901-attention")

	// ① 2 个来源块 < 3 → W21，且照常写入（warning 不拦截）。
	// 两个区间对半分割 L1-L13 / L14-L27，把 27 行的全部非空行不重不漏地覆盖，omissions=[]。
	blocks := []string{srcBlock("L1-L13", "上半", "抄前三节。"), srcBlock("L14-L27", "下半", "抄后三节。")}
	res := v2Run(t, files, noteBlocksOp("n-20261017-thin", blocks...))
	d := requireWarning(t, res, W21)
	if d.Target != "s-20260901-attention" {
		t.Fatalf("W21 应指向被比对的原文，实得 Target=%q", d.Target)
	}
	if !strings.Contains(d.Message, "2") || !strings.Contains(d.Message, "6") {
		t.Fatalf("W21 文案必须给出来源块数与原文章节数两个事实，实得：%s", d.Message)
	}
	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("W21 是 warning，必须照常写入")
	}
	if raw := readVaultFile(t, dir, "domains/ai-infra/notes/n-20261017-thin.md"); !strings.Contains(raw, "抄前三节。") {
		t.Fatal("W21 情形下正文仍应逐字落盘")
	}

	// ② 恰好达到阈值（3 个块 == ceil(6/2)）→ 不判：判据是「严格小于」，等号侧必须放过。
	// 三个区间三等分 L1-L9 / L10-L18 / L19-L27，同样覆盖全部非空行、omissions=[]。
	equal := []string{
		srcBlock("L1-L9", "首", "抄第一段区间。"),
		srcBlock("L10-L18", "中", "抄第二段区间。"),
		srcBlock("L19-L27", "末", "抄第三段区间。"),
	}
	res = v2Run(t, files, noteBlocksOp("n-20261017-equal", equal...))
	if _, ok := find(res.Warnings, W21); ok {
		t.Fatalf("来源块数恰好达到阈值时不得产出 W21：warnings=%v", codes(res.Warnings))
	}

	// ③ 超过阈值（4 个块）同样不判 —— 验收条目里的「配 4 个 → 不产出」逐字落地。
	// 四个区间 L1-L7 / L8-L13 / L14-L20 / L21-L27 覆盖全部非空行、omissions=[]。
	fat := []string{
		srcBlock("L1-L7", "一", "抄第一区间。"),
		srcBlock("L8-L13", "二", "抄第二区间。"),
		srcBlock("L14-L20", "三", "抄第三区间。"),
		srcBlock("L21-L27", "四", "抄第四区间。"),
	}
	res = v2Run(t, files, noteBlocksOp("n-20261017-fat", fat...))
	if _, ok := find(res.Warnings, W21); ok {
		t.Fatalf("来源块数超过阈值时不得产出 W21：warnings=%v", codes(res.Warnings))
	}

	// ④ 原文标题数不足门槛（CoverageAnchorFloor）时不判：短文没有可比结构。
	// source() 夹具正文为「(空行)\n原文正文。」：L1 空白可不覆盖，L2 由单个 source 块覆盖。
	files["sources/s-20260901-attention.md"] = source("s-20260901-attention")
	res = v2Run(t, files, noteBlocksOp("n-20261017-short", srcBlock("L2-L2", "甲", "抄一段。")))
	if _, ok := find(res.Warnings, W21); ok {
		t.Fatalf("原文标题数 < %d 时不得判 W21：warnings=%v", CoverageAnchorFloor, codes(res.Warnings))
	}
}

// TestW21IsNotUpgradedUnderStrict —— 验收⑤后半：`--strict` 下 W21 **不**升级为 error。
//
// 三层证据，缺一层都留有后门：
//   - 集合层：W21 在显式豁免面里，且豁免面与升级面无交集；
//   - 策略层：Precheck(strict=true) 之后 W21 仍是 warning、不拦截；
//   - 事实层：strict 下含 W21 的 plan 仍然照常写入（不是「拦截但没报 error」）。
func TestW21IsNotUpgradedUnderStrict(t *testing.T) {
	if !IsStrictExemptCode(W21) {
		t.Fatalf("W21 必须登记在 --strict 豁免面（契约 D-6），实得 %v", StrictExemptCodes())
	}
	if !StrictSetsDisjoint() {
		t.Fatal("升级面与豁免面出现交集：同一个码不能既升级又豁免")
	}
	if model.IsStrictUpgradeCode(W21) {
		t.Fatalf("W21 竟落在 --strict 升级面：%v", model.StrictUpgradeCodes())
	}

	files := v2Files()
	files["sources/s-20260901-attention.md"] = sixHeadingSource("s-20260901-attention")
	// 2 个来源块对半分割 L1-L13 / L14-L27 覆盖全部非空行、omissions=[]：触发 W21（2 < 3）。
	res := v2Run(t, files, noteBlocksOp("n-20261017-strict",
		srcBlock("L1-L13", "上半", "抄前三节。"), srcBlock("L14-L27", "下半", "抄后三节。")))
	requireWarning(t, res, W21)

	pc := Precheck(res, true)
	if pc.Failed {
		t.Fatalf("strict 下 W21 不得把写拦下：升级清单 %v", codes(pc.Upgraded))
	}
	if _, ok := find(pc.Upgraded, W21); ok {
		t.Fatalf("W21 竟被 strict 升级：%v", codes(pc.Upgraded))
	}
	if _, out := execOn(t, files, res); len(out.Written) == 0 {
		t.Fatal("strict 下含 W21 的 plan 仍应照常写入")
	}
}

// TestPlanV1WriteNoteStillExecutes —— 验收⑥：v1 存量 plan 仍能执行并产出 I1 兼容提示。
//
// 判据落在两处：一是真有字节落盘（v1 的 `材料提炼` / `产出知识卡` 按固定映射进 v2 分区），
// 二是必须有一条 I1 点出「plan_version」——兼容期最忌讳的是**静默**兼容：
// 作者以为自己写的是当前口径，工具却在按旧口径解释他的字段。
func TestPlanV1WriteNoteStillExecutes(t *testing.T) {
	files := v2Files()
	v1 := fmt.Sprintf(`{"plan_version":%d,"verb":"process","domain":"ai-infra",
 "reason":"v1 兼容用例","requirement_ids":["EG-KNW-04"],"base":{},
 "ops":[{"op":"write_note","source":"s-20260901-attention","note_id":"n-20261017-v1",
 "sections":{"材料提炼":"原文主张 A。\n","Agent 分析":"该主张的边界有限。\n"},
 "output_cards":[{"card":"k-20261017-scaling","mode":"新建"}]}]}`, PlanVersionV1)

	res := run(t, vault(t, files), v1)
	if res.Failed() {
		t.Fatalf("v1 plan 必须仍可执行（兼容期），实得 errors=%v", codes(res.Errors))
	}
	if _, ok := findMentioning(infosOf(res), "plan_version"); !ok {
		t.Fatalf("v1 plan 必须产出 plan_version 兼容提示，实得 infos=%v", codes(infosOf(res)))
	}
	d, ok := findByPath(infosOf(res), I1, "sections.材料提炼")
	if !ok {
		t.Fatalf("v1 分区映射必须逐个如实登记，实得 infos=%v", codes(infosOf(res)))
	}
	if !strings.Contains(d.Message, store.SecNoteBody) {
		t.Fatalf("映射提示必须点出落到哪个 v2 分区，实得：%s", d.Message)
	}

	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("v1 plan 必须真实落盘")
	}
	raw := readVaultFile(t, dir, "domains/ai-infra/notes/n-20261017-v1.md")
	// 两个 v1 分区都并入「整理正文」，字节逐字搬过去、顺序按 v1 固定分区序。
	body := strings.Index(raw, "## "+store.SecNoteBody)
	first := strings.Index(raw, "原文主张 A。")
	second := strings.Index(raw, "该主张的边界有限。")
	if body < 0 || first < body || second < first {
		t.Fatalf("v1 两个分区应按声明顺序并入「%s」：\n%s", store.SecNoteBody, raw)
	}
	// v1 的分区名本身不得出现在 v2 落盘结果里（那会造出一个未知分区）。
	for _, legacy := range []string{store.SecDigest, store.SecAgentReview} {
		if strings.Contains(raw, "## "+legacy) {
			t.Fatalf("v1 分区名 %q 不得作为 H2 落进 v2 笔记：\n%s", legacy, raw)
		}
	}
}

// TestPlanV1BlocksRouteToLegacyNoteBlockBytes —— 兼容回归：`plan_version:1 + blocks[]`
// 仍走旧的 store.NoteBlockBytes 落盘形态，直接锁住 noteBlockWrites 在 plan 版本非 v2 时的
// v1 分支不被 v2 审阅式 writer 顺走。
//
// 关键差异：agent 块**不带 annotation**。v2 路径会因契约 §4.2.2 缺 annotation 判 E2、零写入；
// v1 兼容路径根本不读 annotation，照旧把 agent 块渲染成 `> **[Agent 补充]** ` 且**不写**任何
// `<!-- eg:nr:1 ... -->` 机器锚点。这条用例把两件事同时钉死：
//
//	① agent 块逐字使用旧 marker store.AgentBlockMarker（不是 v2 的多类型标签）；
//	② 落盘正文里一个 review 机器锚点都没有（NoteReviewBytes 才会写 eg:nr:）。
//
// 若哪天让 v1 blocks 也走 NoteReviewBytes，会在「缺 annotation 报错」与「多出机器锚点」
// 两处当场判红——正好守住 note_review_writer.go 注释里那条「只有 v1 blocks 走 NoteBlockBytes」。
func TestPlanV1BlocksRouteToLegacyNoteBlockBytes(t *testing.T) {
	files := v2Files()
	// agent 块刻意不给 annotation：v2 会拒，v1 兼容路径不读它，正是本回归要区分的分岔点。
	v1 := fmt.Sprintf(`{"plan_version":%d,"verb":"process","domain":"ai-infra",
 "reason":"v1 blocks 兼容用例","requirement_ids":["EG-KNW-04"],"base":{},
 "ops":[{"op":"write_note","source":"s-20260901-attention","note_id":"n-20261017-v1blocks",
 "blocks":[%s,%s]}]}`, PlanVersionV1,
		srcBlock("L1-L1", "甲 第一节", "来源正文逐字。"),
		`{"role":"agent","heading":"","body":"这一段是我补的，且没有 annotation。"}`)

	res := run(t, vault(t, files), v1)
	if res.Failed() {
		t.Fatalf("v1 + blocks[] 且 agent 块无 annotation 必须仍可执行（兼容期不读 annotation），"+
			"实得 errors=%v", codes(res.Errors))
	}

	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("v1 + blocks[] 必须真实落盘")
	}
	raw := readVaultFile(t, dir, "domains/ai-infra/notes/n-20261017-v1blocks.md")

	// ① agent 块按旧 marker 逐字落盘（v2 审阅式标签绝不出现）。
	if !strings.Contains(raw, store.AgentBlockMarker+"这一段是我补的，且没有 annotation。") {
		t.Fatalf("v1 blocks 的 agent 块应逐字使用旧 marker %q：\n%s", store.AgentBlockMarker, raw)
	}
	// ② 零机器锚点：只有 NoteReviewBytes 才会写 `<!-- eg:nr:1 ... -->`，v1 兼容路径一个都不能有。
	if strings.Contains(raw, "eg:nr:") {
		t.Fatalf("v1 blocks 兼容路径不得写任何 review 机器锚点（eg:nr:）：\n%s", raw)
	}
	// ③ source 块正文逐字保留（兼容路径同样一个字节不改写）。
	if !strings.Contains(raw, "来源正文逐字。") {
		t.Fatalf("v1 blocks 的 source 块正文应逐字落盘：\n%s", raw)
	}
}

// TestCreateOpinionWritesValidationPending —— 验收⑦：新建观点的 frontmatter 含
// `validation: pending`，且落在 opinions/ 目录下。
func TestCreateOpinionWritesValidationPending(t *testing.T) {
	files := v2Files()
	res := v2Run(t, files, `{"op":"create_opinion","opinion_id":"o-20261017-scaling",
 "title":"缩放注意力不适合超长序列",
 "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support",
 "reason":"原文第 5 节的复杂度分析"}],
 "sections":{"观点":"超长序列下该机制不经济。\n","论据与推理":"复杂度随长度平方增长。\n"}}`)

	dir, out := execOn(t, files, res)
	if len(out.OpinionsCreated) != 1 || out.OpinionsCreated[0] != "o-20261017-scaling" {
		t.Fatalf("应恰新建一条观点，实得 %v（cards=%v）", out.OpinionsCreated, out.CardsCreated)
	}
	if len(out.CardsCreated) != 0 {
		t.Fatalf("观点不得被记进 cards.created（同级实体，报告口径分开）：%v", out.CardsCreated)
	}
	rel := store.OpinionRel("ai-infra", "o-20261017-scaling")
	raw := readVaultFile(t, dir, rel)
	// 断言落在**解析后的值**上，不比对引号风格：frontmatter 的标量引号是 writer 的
	// 统一风格（全部单引号），把它写进判据等于让「换一种等价 YAML 写法」也变红，
	// 而这条验收要证的是「新建观点的验证状态是 pending」，不是 YAML 的排版。
	var fm struct {
		ID         string `yaml:"id"`
		Validation string `yaml:"validation"`
	}
	if err := store.FrontmatterInto([]byte(raw), &fm); err != nil {
		t.Fatalf("新建观点的 frontmatter 必须可解析：%v\n%s", err, raw)
	}
	if fm.Validation != string(model.ValidationPending) {
		t.Fatalf("新建观点必须写出 validation: %s，实得 %q：\n%s",
			model.ValidationPending, fm.Validation, raw)
	}
	if _, err := model.ParseValidation(fm.Validation); err != nil {
		t.Fatalf("落盘的 validation 必须落在封闭三值枚举内：%v", err)
	}
	for _, sec := range []string{store.SecOpinionClaim, store.SecArgument,
		store.SecCounter, store.SecToVerify, store.SecUserAppend} {
		if !strings.Contains(raw, "## "+sec) {
			t.Fatalf("观点模板缺分区「%s」：\n%s", sec, raw)
		}
	}
}

// TestCreateOpinionRejectsPrevalidated —— 与上一支成对：plan 里直接把 validation 设成
// validated / rejected 必须判 E2、零写入（契约 §4.5 / §6.3：验证只能由用户显式路径流转）。
func TestCreateOpinionRejectsPrevalidated(t *testing.T) {
	for _, v := range []model.Validation{model.ValidationValidated, model.ValidationRejected} {
		t.Run(string(v), func(t *testing.T) {
			files := v2Files()
			res := v2Run(t, files, fmt.Sprintf(`{"op":"create_opinion",
 "opinion_id":"o-20261017-pre","title":"预先盖章的观点","validation":%q,
 "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support",
 "reason":"原文第 5 节"}],"sections":{"观点":"一句主张。\n"}}`, v))
			d := requireError(t, res, E2)
			if !strings.Contains(d.Path, "validation") {
				t.Fatalf("诊断应指向 validation 字段，实得 %q", d.Path)
			}
			assertZeroWrite(t, files, res, store.OpinionRel("ai-infra", "o-20261017-pre"))
		})
	}
}
