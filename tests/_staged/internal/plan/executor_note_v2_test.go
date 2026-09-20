package plan

// executor_note_v2_test.go —— T-004-A 的验收判据：executor 只见规范 op，
// Note 的「提取结果」按 Knowledge / Opinion 两组成清单且 **Opinion 行带 `[<validation>]`**
// （T-evergreen.knowledge_opinion_split-158614-004；契约 §5.1 / §4.4）。
//
// 本文件与 T-003 的 knowledge_opinion_v2_test.go 不重叠：那一份证的是**校验层**
// （blocks[] 顺序、互斥、W21、create_opinion 的 validation 门闸），本份证的是
// **执行落盘后读者在文件里看到什么**：
//
//	① `提取结果` 的两组 H3 清单里，Opinion 行必须带验证状态标记，Knowledge 行不带；
//	② 验证状态不是「渲染时随手填 pending」，而是取自唯一真源 ——
//	   本 plan 内新建的观点取新建默认值，已有观点取盘上 frontmatter 的实际值；
//	③ 既无法在本 plan 内新建、又不在库里的 `o-*`，**不许伪造**一个状态：
//	   如实记一条 info、行照常列出但不带标记；
//	④ executor 的分发面只认 ActionKind，别名与 op 名一律进不来（结构 + 行为双证）。
//
// 判据全部落在**落盘字节**与**诊断**上：只看 Actions 无法区分「渲染对了」与
// 「渲染函数被调用过」。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// opinionFile 造一份**盘上已有**的观点：frontmatter 的 `validation` 由参数给定。
//
// 为什么用例要自己造一份而不复用 create_opinion 的产物：本判据要证「已有观点的行取
// 盘上的实际状态」，而 create_opinion 恒写 pending（契约 §4.5），用它造夹具就只能考到
// pending 一种取值 —— 那样「取自真源」与「写死 pending」两种实现都会绿。
func opinionFile(id, title string, val model.Validation) string {
	return fmt.Sprintf(`---
id: %s
title: %s
status: active
validation: %s
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-attention
    note: n-20260901-attention
    rel: support
    reason: 原文第 5 节的复杂度分析
---

# %s

## %s

超长序列下该机制不经济。

## %s

复杂度随序列长度平方增长。

## %s

短序列下不成立。

## %s

- 待补一个真实反例

## %s

我的看法先记在这里。
`, id, title, val, title,
		store.SecOpinionClaim, store.SecArgument, store.SecCounter,
		store.SecToVerify, store.SecUserAppend)
}

// outputCard 造一条 `output_cards[]` 条目。
func outputCard(card, mode string) string {
	return fmt.Sprintf(`{"card":%q,"mode":%q}`, card, mode)
}

// noteWithOutputCards 造一条带 `output_cards[]` 的 v2 write_note op（正文给一个来源块）。
//
// 来源 s-20260901-attention 用 v2Files 的 source() 夹具：正文为「(空行)\n原文正文。」，
// L1 空白可不覆盖、L2 由这个 source 块（L2-L2）覆盖，omissions 传空数组声明无删除 ——
// 这样 T12-2A 的来源覆盖校验通过，本判据得以专注于「提取结果」两组清单的落盘字节。
func noteWithOutputCards(noteID string, cards ...string) string {
	return `{"op":"write_note","source":"s-20260901-attention","note_id":"` + noteID + `",
 "blocks":[` + srcBlock("L2-L2", "第一节", "原文第一节的整理。") + `],"omissions":[],
 "output_cards":[` + strings.Join(cards, ",") + `]}`
}

// newOpinionOp 造一条 create_opinion op（不给 validation：新建恒为 pending）。
func newOpinionOp(id, title string) string {
	return `{"op":"create_opinion","opinion_id":"` + id + `","title":"` + title + `",
 "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support",
 "reason":"原文第 5 节的复杂度分析"}],
 "sections":{"` + store.SecOpinionClaim + `":"一句主张。\n"}}`
}

// extractionOf 从落盘笔记里切出「提取结果」分区的正文（到下一个 H2 之前）。
//
// 不做「全文 Contains」了事：`o-…` 与标记若出现在别的分区（例如被误写进「整理正文」），
// 全文包含判据照样会绿，而读者在「提取结果」里什么也看不到。
func extractionOf(t *testing.T, raw string) string {
	t.Helper()
	head := "## " + store.SecExtraction + "\n"
	at := strings.Index(raw, head)
	if at < 0 {
		t.Fatalf("落盘笔记缺「%s」分区：\n%s", store.SecExtraction, raw)
	}
	body := raw[at+len(head):]
	if next := strings.Index(body, "\n## "); next >= 0 {
		body = body[:next+1]
	}
	return body
}

// TestExtractionOpinionRowsCarryValidation —— 验收①②：Opinion 行带 `[<validation>]`，
// 取值来自唯一真源；Knowledge 行**不带**标记。
//
// 夹具刻意混三种来源：本 plan 内新建的观点（默认 pending）、盘上已被用户判定为
// validated 的观点、以及一张复用的知识卡。三者同处一份 `output_cards[]`，
// 只有「按 ID 前缀分组 + 按真源取状态」的实现能全部通过。
func TestExtractionOpinionRowsCarryValidation(t *testing.T) {
	const settled = "o-20260901-longseq"
	files := v2Files()
	files[store.OpinionRel("ai-infra", settled)] =
		opinionFile(settled, "缩放注意力不适合超长序列", model.ValidationValidated)

	res := v2Run(t, files, newOpinionOp("o-20261017-cost", "核心边界决定扩展成本")+","+
		noteWithOutputCards("n-20261017-extract",
			outputCard("k-20260901-attention", "复用"),
			outputCard("o-20261017-cost", "新建"),
			outputCard(settled, "补充")))

	dir, out := execOn(t, files, res)
	if len(out.Written) == 0 {
		t.Fatal("write_note 与 create_opinion 都应真实落盘")
	}
	raw := readVaultFile(t, dir, "domains/ai-infra/notes/n-20261017-extract.md")
	body := extractionOf(t, raw)

	kAt := strings.Index(body, "### "+store.ExtractionKnowledgeHeading)
	oAt := strings.Index(body, "### "+store.ExtractionOpinionHeading)
	if kAt < 0 || oAt < 0 || kAt > oAt {
		t.Fatalf("「%s」应按 %s / %s 两组 H3 顺序成清单：\n%s", store.SecExtraction,
			store.ExtractionKnowledgeHeading, store.ExtractionOpinionHeading, body)
	}

	wantLines := []string{
		"- k-20260901-attention（复用）",
		"- o-20261017-cost（新建） " + store.ExtractionValidationMark(model.ValidationPending),
		"- " + settled + "（补充） " + store.ExtractionValidationMark(model.ValidationValidated),
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want+"\n") {
			t.Fatalf("「%s」缺行 %q：\n%s", store.SecExtraction, want, body)
		}
	}
	// Knowledge 行不得带验证标记：验证状态是 Opinion 独有的 frontmatter 键（契约 §3.4），
	// 给知识卡也盖一个标记等于把「论证进度」这件事扩散到不持有它的实体上。
	knowledgeGroup := body[kAt:oAt]
	if strings.Contains(knowledgeGroup, "`[") {
		t.Fatalf("Knowledge 组不得出现验证状态标记：\n%s", knowledgeGroup)
	}
	// 新建观点的行不得被写成 validated：状态只能来自真源，不许从同组别的条目串味。
	opinionGroup := body[oAt:]
	if strings.Contains(opinionGroup, "o-20261017-cost（新建） "+
		store.ExtractionValidationMark(model.ValidationValidated)) {
		t.Fatalf("新建观点的验证状态被串味成 validated：\n%s", opinionGroup)
	}
	// 标记取值必须落在封闭三值内（不许出现第四种字面量）。
	for _, tok := range strings.Split(opinionGroup, "`") {
		if !strings.HasPrefix(tok, "[") || !strings.HasSuffix(tok, "]") {
			continue
		}
		if _, err := model.ParseValidation(strings.Trim(tok, "[]")); err != nil {
			t.Fatalf("验证标记 %q 不在封闭三值枚举内：%v", tok, err)
		}
	}
}

// TestExtractionUndeterminedOpinionStateIsDisclosed —— 验收③：状态无法确定时**不伪造**。
//
// 场景：`output_cards[]` 里的 `o-*` 既不在本 plan 内新建、也不在库里（悬空引用，
// 由 reconcile 的 R3/R4 负责检出）。此时渲染一个 `[pending]` 是在替读者断言
// 「这条观点还没验证」—— 而事实是「这条观点根本找不到」。两件事必须区分：
// 行照常列出（不静默丢弃产出），但不带标记，并如实记一条 info 点名该 ID。
func TestExtractionUndeterminedOpinionStateIsDisclosed(t *testing.T) {
	const ghost = "o-20261017-ghost"
	files := v2Files()
	res := v2Run(t, files, noteWithOutputCards("n-20261017-ghost", outputCard(ghost, "新建")))

	dir, _ := execOn(t, files, res)
	body := extractionOf(t, readVaultFile(t, dir, "domains/ai-infra/notes/n-20261017-ghost.md"))

	if !strings.Contains(body, "- "+ghost+"（新建）\n") {
		t.Fatalf("产出条目不得被静默丢弃，且不得带伪造标记：\n%s", body)
	}
	for _, v := range model.ValidValidations() {
		if strings.Contains(body, store.ExtractionValidationMark(v)) {
			t.Fatalf("状态无法确定时不得伪造 %s 标记：\n%s", v, body)
		}
	}
	d, ok := findMentioning(infosOf(res), ghost)
	if !ok {
		t.Fatalf("必须如实记一条 info 点名 %s，实得 infos=%v", ghost, codes(infosOf(res)))
	}
	if d.Code != I1 {
		t.Fatalf("该提示应为 I1（info 级、不拦截），实得 %s", d.Code)
	}
	if !strings.Contains(d.Message, model.FMKeyValidation) {
		t.Fatalf("提示必须点出缺的是 %s，实得：%s", model.FMKeyValidation, d.Message)
	}
	if !strings.Contains(d.Path, "output_cards") {
		t.Fatalf("提示的字段路径应指向 output_cards，实得 %q", d.Path)
	}
}

// TestExecutorDispatchTakesOnlyCanonicalOps —— 验收④：executor 的分发面只认 ActionKind。
//
// 两面证据：
//   - 行为面：v1 的 `create_card` / `append_card` 别名 plan 展开后，Action 上挂的
//     `Op.Name` 已是规范名 —— 别名在 expand 阶段就被改写，executor 根本见不到它；
//     四个规范写口各自展开成**专属**的 ActionKind（Knowledge 与 Opinion 不共用形态）。
//   - 结构面：执行层源码里既没有 op 名分发（`op.Name`），也没有任何 op 名字面量。
//     一旦有人在 executor 里补一句 `case "create_card"`，落盘模板就会出现第二条路径，
//     而那条路径不会产出别名迁移提示 —— 结构判据是唯一能当场拦住它的地方。
func TestExecutorDispatchTakesOnlyCanonicalOps(t *testing.T) {
	files := v2Files()
	// ① 别名侧：v1 plan 用 create_card + append_card。
	aliasPlan := fmt.Sprintf(`{"plan_version":%d,"verb":"process","domain":"ai-infra",
 "reason":"别名 plan","requirement_ids":["EG-KNW-04"],"base":{"%s":"%s"},"ops":[
 %s,
 {"op":"append_card","card":"k-20260901-attention",
  "sections":{"%s":"补一条边界。\n"}}]}`,
		PlanVersionV1, "domains/ai-infra/knowledge/k-20260901-attention.md",
		store.ContentHash([]byte(files["domains/ai-infra/knowledge/k-20260901-attention.md"])),
		knowledgeOp(OpCreateCard), store.SecBoundary)
	res := run(t, vault(t, files), aliasPlan)
	if res.Failed() {
		t.Fatalf("别名 plan 应可执行，实得 errors=%v", codes(res.Errors))
	}
	wantKinds := map[ActionKind]string{ActCardNew: OpCreateKnowledge, ActCardAppend: OpAppendKnowledge}
	seen := map[ActionKind]bool{}
	for _, a := range res.Actions {
		canonical, ok := wantKinds[a.Kind]
		if !ok {
			t.Fatalf("别名 plan 不应展开出 %s 形态", a.Kind)
		}
		seen[a.Kind] = true
		if a.Op == nil {
			t.Fatalf("%s 形态缺 Op 引用", a.Kind)
		}
		if a.Op.Name != canonical {
			t.Fatalf("executor 只应见到规范名 %s，实得 %q（别名未在 expand 阶段改写）",
				canonical, a.Op.Name)
		}
	}
	if len(seen) != len(wantKinds) {
		t.Fatalf("别名 plan 应展开出 %v 两种形态，实得 %v", wantKinds, seen)
	}
	if _, out := execOn(t, files, res); len(out.Written) == 0 {
		t.Fatal("别名 plan 必须真实落盘")
	}

	// ② 规范侧：四个写口各自展开成专属形态，Knowledge 与 Opinion 不共用 Kind。
	canonicalPlan := v2Plan(t, files, strings.Join([]string{
		knowledgeOp(OpCreateKnowledge),
		`{"op":"append_knowledge","card":"k-20260901-attention",
  "sections":{"` + store.SecBoundary + `":"再补一条边界。\n"}}`,
		newOpinionOp("o-20261017-cost", "核心边界决定扩展成本"),
	}, ","))
	res = run(t, vault(t, files), canonicalPlan)
	if res.Failed() {
		t.Fatalf("规范 plan 应可执行，实得 errors=%v", codes(res.Errors))
	}
	gotKinds := map[ActionKind]int{}
	for _, a := range res.Actions {
		gotKinds[a.Kind]++
	}
	for _, kind := range []ActionKind{ActCardNew, ActCardAppend, ActOpinionNew} {
		if gotKinds[kind] != 1 {
			t.Fatalf("%s 应恰展开一条，实得 %d（全部形态：%v）", kind, gotKinds[kind], gotKinds)
		}
	}

	// ③ 结构面：执行层源码零 op 名分发、零 op 名字面量。
	for _, name := range []string{"executor.go", "execute_m3.go", "execute_m4.go"} {
		path := filepath.Join(repoInternalDir(t), "plan", name)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读执行层源码失败：%v", err)
		}
		src := string(raw)
		if strings.Contains(src, "op.Name") || strings.Contains(src, "Op.Name") {
			t.Fatalf("%s 出现 op 名分发：执行层只许按 ActionKind 分发", name)
		}
		for _, op := range append(AllOpNames(), OpCreateCard, OpAppendCard) {
			if strings.Contains(src, `"`+op+`"`) {
				t.Fatalf("%s 出现 op 名字面量 %q：执行层不得按 op 名开分支", name, op)
			}
		}
	}
}
