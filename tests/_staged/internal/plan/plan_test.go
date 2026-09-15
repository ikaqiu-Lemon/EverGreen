package plan

// 分区名与 F5 固定顺序经 store 只读转发（§13：plan 不直连 mdfile）；本测试只构造合法样例做校验分级断言。

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const cardFixture = `---
id: %s
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-attention
    note: n-20260901-attention
    rel: support
    reason: 原文第 3 节给出该机制的定义
relations:
  - type: limits
    target: k-20260815-rnn
    reason: 限定了原结论的适用序列长度
---

# 注意力机制

## 知识内容

注意力是一种加权聚合。

## 解释与依据

- 依据原文第 3 节

## 条件与边界

- 仅适用于序列建模

## 用户补充

我自己的理解：先看 QKV。

## 理解自检

- 为什么需要缩放？
`

const noteFixture = `---
id: %s
source: s-20260901-attention
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
---

# 注意力机制入门（材料笔记）

## 材料提炼

- 原文主张：注意力可替代循环结构

## Agent 分析

- 该主张的适用面有限

## 用户补充

我的疑问先记在这里。

## 存疑与待验证

- 超长序列下的复杂度？

## 产出知识卡

- k-20260901-attention（新建）
`

const sourceFixture = `---
id: %s
url: https://example.com/attention
title: Attention 机制入门
saved_at: '2026-09-01T10:00:00+08:00'
---

原文正文。
`

func card(id string) string   { return fmt.Sprintf(cardFixture, id) }
func note(id string) string   { return fmt.Sprintf(noteFixture, id) }
func source(id string) string { return fmt.Sprintf(sourceFixture, id) }

// vault 构造一份内存库视图：rel → 字节内容，id 由 frontmatter 只读解析得到。
func vault(t *testing.T, files map[string]string) Env {
	t.Helper()
	idx := store.Index{ByID: map[string]string{}}
	for rel, content := range files {
		var fm map[string]interface{}
		if err := store.FrontmatterInto([]byte(content), &fm); err != nil {
			continue // 故意构造的坏 frontmatter：由用例显式登记进索引
		}
		if id, ok := fm["id"].(string); ok {
			idx.ByID[id] = rel
		}
	}
	return Env{Index: idx, DefaultDomain: "ai-infra", Read: func(rel string) ([]byte, error) {
		c, ok := files[rel]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(c), nil
	}}
}

// baseVault 是最常用的一组样例：一张卡 + 一篇笔记 + 一份原文。
func baseVault(t *testing.T) (Env, map[string]string) {
	t.Helper()
	files := map[string]string{
		"domains/ai-infra/knowledge/k-20260901-attention.md": card("k-20260901-attention"),
		"domains/ai-infra/knowledge/k-20260815-rnn.md":       card("k-20260815-rnn"),
		"domains/ai-infra/notes/n-20260901-attention.md":     note("n-20260901-attention"),
		"sources/s-20260901-attention.md":                    source("s-20260901-attention"),
	}
	return vault(t, files), files
}

func run(t *testing.T, env Env, body string) *Result {
	t.Helper()
	p, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("plan 解析失败：%v", err)
	}
	return Validate(p, env)
}

func codes(diags []Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func find(diags []Diagnostic, code string) (Diagnostic, bool) {
	for _, d := range diags {
		if d.Code == code {
			return d, true
		}
	}
	return Diagnostic{}, false
}

func requireError(t *testing.T, res *Result, code string) Diagnostic {
	t.Helper()
	if !res.Failed() {
		t.Fatalf("期望 %s error，实际零 error（warnings=%v）", code, codes(res.Warnings))
	}
	d, ok := find(res.Errors, code)
	if !ok {
		t.Fatalf("期望 %s error，实际 errors=%v", code, codes(res.Errors))
	}
	if d.Path == "" {
		t.Fatalf("%s 诊断缺字段路径：%+v", code, d)
	}
	return d
}

func requireWarning(t *testing.T, res *Result, code string) Diagnostic {
	t.Helper()
	if res.Failed() {
		t.Fatalf("期望 %s warning 不拦截，实际有 error=%v", code, codes(res.Errors))
	}
	d, ok := find(res.Warnings, code)
	if !ok {
		t.Fatalf("期望 %s warning，实际 warnings=%v", code, codes(res.Warnings))
	}
	return d
}

func planWith(ops string, extra ...string) string {
	head := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"沉淀一张卡",
 "requirement_ids":["EG-KNW-04"],"base":{},`
	if len(extra) > 0 {
		head += strings.Join(extra, ",") + ","
	}
	return head + `"ops":[` + ops + `]}`
}

// ---------- error 级：E1–E6 各一例 ----------

func TestE1DuplicateID(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, planWith(`{"op":"create_card","card_id":"k-20260901-attention","title":"注意力",
 "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support","reason":"依据第 3 节"}],
 "sections":{"知识内容":"内容\n"}}`))
	d := requireError(t, res, E1)
	if !strings.Contains(d.Path, "ops[0].card_id") {
		t.Fatalf("E1 诊断路径应含 ops[0].card_id：%+v", d)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("error 必须零展开（零写入）：%+v", res.Actions)
	}
}

func TestE2UnresolvableTarget(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, planWith(`{"op":"add_relation","from":"k-20260901-attention","type":"limits",
 "target":"k-20260101-missing","reason":"限定适用面"}`))
	requireError(t, res, E2)

	res = run(t, env, planWith(`{"op":"add_relation","from":"k-20260901-attention","type":"limits",
 "target":"note-3","reason":"限定适用面"}`))
	requireError(t, res, E2)
}

func TestE3IDTypeMixed(t *testing.T) {
	env, _ := baseVault(t)
	// 反例一：把原文 ID 写进论证关系。
	res := run(t, env, planWith(`{"op":"add_relation","from":"k-20260901-attention","type":"limits",
 "target":"s-20260901-attention","reason":"限定适用面"}`))
	d := requireError(t, res, E3)
	if !strings.Contains(d.Path, "target") {
		t.Fatalf("E3 诊断路径应指向 target：%+v", d)
	}
	// 反例二：把卡 ID 写进材料关系。
	res = run(t, env, planWith(`{"op":"add_material_rel","card":"k-20260901-attention",
 "source":"k-20260815-rnn","note":"n-20260901-attention","rel":"support","reason":"第二篇材料支持"}`))
	d = requireError(t, res, E3)
	if !strings.Contains(d.Path, "source") {
		t.Fatalf("E3 诊断路径应指向 source：%+v", d)
	}
	if len(res.Actions) != 0 {
		t.Fatal("E3 必须零展开")
	}
}

func TestE4BrokenFrontmatter(t *testing.T) {
	broken := "---\nid: [未闭合\n---\n\n# 坏卡\n\n## 知识内容\n"
	rel := "domains/ai-infra/knowledge/k-20260901-broken.md"
	env := vault(t, map[string]string{
		rel:                               broken,
		"sources/s-20260901-attention.md": source("s-20260901-attention"),
	})
	env.Index.ByID["k-20260901-broken"] = rel
	res := run(t, env, planWith(`{"op":"append_card","card":"k-20260901-broken",
 "sections":{"条件与边界":"- 补充\n"}}`))
	requireError(t, res, E4)
}

// TestE5VersionAndUnknownOp 钉住 E5 的两条来源：版本不被支持、op 未知。
//
// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：`plan_version: 2` 原本是
// 「不被支持的版本」反例，契约 §4.1 把它变成**当前版本**，v1 则进入兼容期继续被接受。
// 于是「不被支持」的反例必须换成受支持集合 `{1, 2}` 之外的号：这里取 `3`（下一个尚未
// 定义的版本，同时覆盖「未来版本不得被当前实现静默当作 v2 执行」这一真实风险）。
// 判据本身没有放宽：仍要求 E5 + 零展开，且 v2 侧另有正例证明当前版本零 error（见下）。
func TestE5VersionAndUnknownOp(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":3,"verb":"process","domain":"ai-infra","ops":[]}`)
	d0 := requireError(t, res, E5)
	if !strings.Contains(d0.Message, "不被支持") {
		t.Fatalf("越界版本应注明不被支持：%+v", d0)
	}
	if len(res.Actions) != 0 {
		t.Fatal("版本不被支持时整条 plan 不执行")
	}
	// 缺 plan_version 同样是 E5（与「越界版本」同码不同文案，两者都不得静默取默认值）。
	res = run(t, env, `{"verb":"process","domain":"ai-infra","ops":[]}`)
	if d := requireError(t, res, E5); !strings.Contains(d.Message, "缺失") {
		t.Fatalf("缺 plan_version 应注明缺失：%+v", d)
	}
	// 反向正例：当前版本 2 不再是 E5 的来源（空 ops[] 本身不是错误）。
	res = run(t, env, `{"plan_version":2,"verb":"process","domain":"ai-infra","ops":[]}`)
	if _, ok := find(res.Errors, E5); ok {
		t.Fatalf("plan_version=%d 是当前版本，不得再判 E5：%v", PlanVersion, codes(res.Errors))
	}

	// M3（T-…-037）重钉：`replace_block` 已实装（提案合同 §8.1 第 7 行），不再是未知 op。
	// 未知 op 的反例改用**归属仍未定**的 `set_tags`（授权合同 §9 A-18，本仓不定义其字段）。
	res = run(t, env, planWith(`{"op":"set_tags","card":"k-20260901-attention"}`))
	d := requireError(t, res, E5)
	if !strings.Contains(d.Message, "不实现") {
		t.Fatalf("未知 op 应注明本仓不实现：%+v", d)
	}
	if len(res.Actions) != 0 {
		t.Fatal("未知 op 整条不执行")
	}
	// 同一形态的 replace_block **不再**报 E5：它进入 M3 校验链，缺 target 时报 E2。
	res = run(t, env, planWith(`{"op":"replace_block","card":"k-20260901-attention"}`))
	if _, ok := find(res.Errors, E5); ok {
		t.Fatalf("replace_block 自 M3 起是已知 op，不得再判 E5：%v", codes(res.Errors))
	}
	requireError(t, res, E2)
}

func TestE6ForbiddenSections(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, planWith(`{"op":"append_card","card":"k-20260901-attention",
 "sections":{"知识内容":"改写\n"}}`))
	requireError(t, res, E6)

	res = run(t, env, planWith(`{"op":"append_card","card":"k-20260901-attention",
 "sections":{"用户补充":"我的话\n"}}`))
	d := requireError(t, res, E6)
	if !strings.Contains(d.Path, "用户补充") {
		t.Fatalf("E6 诊断应指向「用户补充」：%+v", d)
	}
}

// ---------- V3/V7：建卡必须带材料关系（EG-SRC-04），正反两例 ----------

func TestCreateCardRequiresSources(t *testing.T) {
	env, _ := baseVault(t)
	for name, ops := range map[string]string{
		"缺字段": `{"op":"create_card","title":"新卡","sections":{"知识内容":"内容\n"}}`,
		"空数组": `{"op":"create_card","title":"新卡","sources":[],"sections":{"知识内容":"内容\n"}}`,
	} {
		res := run(t, env, planWith(ops))
		d := requireError(t, res, E5)
		if !strings.Contains(d.Path, "ops[0].sources") {
			t.Fatalf("%s：诊断路径应含 ops[0].sources，实际 %+v", name, d)
		}
		if !strings.Contains(d.Message, "新建卡必须建立材料关系") {
			t.Fatalf("%s：错误信息须说明建卡硬前提：%+v", name, d)
		}
		if len(res.Actions) != 0 {
			t.Fatalf("%s：必须零展开", name)
		}
	}
	res := run(t, env, planWith(`{"op":"create_card","card_id":"k-20260902-flash","title":"FlashAttention",
 "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support","reason":"原文实测"}],
 "sections":{"知识内容":"分块计算\n"}}`))
	if res.Failed() {
		t.Fatalf("带合法四要素的建卡应通过：%v", codes(res.Errors))
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActCardNew {
		t.Fatalf("应展开为一条 card_new：%+v", res.Actions)
	}
	if res.Actions[0].Path != "domains/ai-infra/knowledge/k-20260902-flash.md" {
		t.Fatalf("落位路径不对：%s", res.Actions[0].Path)
	}
}

// ---------- warning 级 ----------

func TestW1CrossDomainStillWrites(t *testing.T) {
	files := map[string]string{
		"domains/other/knowledge/k-20260901-attention.md": card("k-20260901-attention"),
		"domains/ai-infra/notes/n-20260901-attention.md":  note("n-20260901-attention"),
	}
	env := vault(t, files)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"补充",
 "base":{"k-20260901-attention":"sha256:x"},
 "ops":[{"op":"append_card","card":"k-20260901-attention","sections":{"条件与边界":"- 补充\n"}}]}`)
	requireWarning(t, res, W1)
	if len(res.Actions) != 1 || res.Actions[0].Skip {
		t.Fatalf("W1 必须照常写入：%+v", res.Actions)
	}
}

func TestW2RelationReasonEqualsRelName(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"挂材料",
 "base":{"k-20260901-attention":"sha256:x"},
 "convergence":[{"card":"k-20260901-attention","relation":"non_core_supplement",
   "core_knowledge":"same","conditions":"different","reuse_purpose":"same"}],
 "ops":[{"op":"add_material_rel","card":"k-20260901-attention","source":"s-20260901-attention",
   "note":"n-20260901-attention","rel":"support","reason":"support"}]}`)
	d := requireWarning(t, res, W2)
	if !strings.Contains(d.Path, "reason") {
		t.Fatalf("W2 诊断应指向 reason：%+v", d)
	}
	if len(res.Actions) != 1 || res.Actions[0].Skip {
		t.Fatalf("W2 必须照常写入：%+v", res.Actions)
	}
}

func TestW4DeprecatedFieldPath(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, planWith(`{"op":"create_card","card_id":"k-20260902-flash","title":"FlashAttention",
 "candidate":true,"source_check":"ok",
 "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support","reason":"原文实测"}],
 "sections":{"知识内容":"分块计算\n"}}`))
	requireWarning(t, res, W4)
	if len(res.Actions) != 1 {
		t.Fatalf("W4 只忽略该字段，其余照写：%+v", res.Actions)
	}
}

// TestNoKeywordScan 是「禁止关键词扫描」的反例：合法 plan 同时包含
// relations[].type、sources[].rel、顶层 plan.domain 与 target_domain，必须零 W4。
func TestNoKeywordScan(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"混合用例",
 "base":{"k-20260901-attention":"sha256:x","k-20260815-rnn":"sha256:y"},
 "convergence":[{"card":"k-20260901-attention","relation":"core_change",
   "core_knowledge":"different","conditions":"same","reuse_purpose":"same"}],
 "ops":[
  {"op":"add_source","url":"https://example.com/x","title":"新原文","body":"正文\n",
   "reason":"补齐材料","target_domain":"ai-infra"},
  {"op":"add_material_rel","card":"k-20260901-attention","source":"s-20260901-attention",
   "note":"n-20260901-attention","rel":"context","reason":"提供背景"},
  {"op":"add_relation","from":"k-20260901-attention","type":"limits","target":"k-20260815-rnn",
   "reason":"限定适用序列长度"}]}`)
	if res.Failed() {
		t.Fatalf("合法 plan 不应有 error：%v", res.Errors)
	}
	if _, ok := find(res.Warnings, W4); ok {
		t.Fatalf("白名单字段路径不得误判为废弃字段：%v", res.Warnings)
	}
}

func TestW5ConvergenceRelation(t *testing.T) {
	env, _ := baseVault(t)
	base := `"base":{"k-20260901-attention":"sha256:x"},`
	ops := `"ops":[{"op":"append_card","card":"k-20260901-attention","sections":{"条件与边界":"- 补充\n"}}]`

	// ① 七个枚举值各一条合法条目 → 零 W5。
	var entries []string
	for _, rel := range ConvergeRelations() {
		dims := `"core_knowledge":"different","conditions":"same","reuse_purpose":"same"`
		if rel == "same_semantics" {
			dims = `"core_knowledge":"same","conditions":"same","reuse_purpose":"same"`
		}
		entries = append(entries, fmt.Sprintf(
			`{"card":"k-20260901-attention","relation":%q,%s}`, rel, dims))
	}
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"r",`+base+
		`"convergence":[`+strings.Join(entries, ",")+`],`+ops+`}`)
	if d, ok := find(res.Warnings, W5); ok {
		t.Fatalf("七个合法枚举值不应产生 W5：%+v", d)
	}

	// ② relation 与三维度结论矛盾 → 一条 W5，不拦截。
	res = run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"r",`+base+
		`"convergence":[{"card":"k-20260901-attention","relation":"core_change",
   "core_knowledge":"same","conditions":"same","reuse_purpose":"same"}],`+ops+`}`)
	d := requireWarning(t, res, W5)
	if d.Path != "convergence[0].relation" {
		t.Fatalf("W5 诊断路径应是 convergence[0].relation：%+v", d)
	}
	if len(res.Actions) != 1 || res.Actions[0].Skip {
		t.Fatalf("W5 不影响写入：%+v", res.Actions)
	}

	// ③ relation 缺失 / 取值不在枚举内 → 同样只产出 W5（不是新编号、不是 error）。
	for _, entry := range []string{
		`{"card":"k-20260901-attention","core_knowledge":"different","conditions":"same","reuse_purpose":"same"}`,
		`{"card":"k-20260901-attention","relation":"merged","core_knowledge":"different","conditions":"same","reuse_purpose":"same"}`,
	} {
		res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"r",`+base+
			`"convergence":[`+entry+`],`+ops+`}`)
		d := requireWarning(t, res, W5)
		if d.Code != W5 || d.Level != LevelWarning {
			t.Fatalf("relation 异常一律并入 W5 warning：%+v", d)
		}
	}
}

func TestW5ConvergenceDedicatedRelationPlanException(t *testing.T) {
	env, _ := baseVault(t)
	relationOp := `{"op":"add_relation","from":"k-20260901-attention","type":"limits",
 "target":"k-20260815-rnn","reason":"限定适用范围"}`

	t.Run("relate_single_add_relation", func(t *testing.T) {
		res := run(t, env, `{"plan_version":1,"verb":"relate","domain":"ai-infra","reason":"建立关系",
 "requirement_ids":[],"convergence":[],"base":{"k-20260901-attention":"sha256:x",
 "k-20260815-rnn":"sha256:y"},"ops":[`+relationOp+`]}`)
		if d, ok := find(res.Warnings, W5); ok {
			t.Fatalf("专用 relate 单关系计划不应因空 convergence[] 产生 W5：%+v", d)
		}
	})

	t.Run("process_still_requires_convergence", func(t *testing.T) {
		res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"建立关系",
 "requirement_ids":[],"convergence":[],"base":{"k-20260901-attention":"sha256:x",
 "k-20260815-rnn":"sha256:y"},"ops":[`+relationOp+`]}`)
		d := requireWarning(t, res, W5)
		if d.Path != "convergence" {
			t.Fatalf("普通 process 计划仍应在 convergence 路径产生 W5：%+v", d)
		}
	})

	t.Run("relate_multiple_ops_still_requires_convergence", func(t *testing.T) {
		res := run(t, env, `{"plan_version":1,"verb":"relate","domain":"ai-infra","reason":"批量建立关系",
 "requirement_ids":[],"convergence":[],"base":{"k-20260901-attention":"sha256:x",
 "k-20260815-rnn":"sha256:y"},"ops":[`+relationOp+`,`+relationOp+`]}`)
		d := requireWarning(t, res, W5)
		if d.Path != "convergence" {
			t.Fatalf("多 op 的 relate 计划仍应在 convergence 路径产生 W5：%+v", d)
		}
	})

	t.Run("explicit_invalid_entry_still_warns", func(t *testing.T) {
		res := run(t, env, `{"plan_version":1,"verb":"relate","domain":"ai-infra","reason":"建立关系",
 "requirement_ids":[],"convergence":[{"card":"k-20260901-attention","relation":"core_change",
 "core_knowledge":"same","conditions":"same","reuse_purpose":"same"}],
 "base":{"k-20260901-attention":"sha256:x","k-20260815-rnn":"sha256:y"},"ops":[`+relationOp+`]}`)
		d := requireWarning(t, res, W5)
		if d.Path != "convergence[0].relation" {
			t.Fatalf("显式矛盾 convergence 仍应产生 W5：%+v", d)
		}
	})
}

func TestW6BaseNotCoveringSkipsOnlyThatFile(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"两处改动",
 "base":{"k-20260815-rnn":"sha256:y"},
 "convergence":[{"card":"k-20260901-attention","relation":"non_core_supplement",
   "core_knowledge":"same","conditions":"different","reuse_purpose":"same"}],
 "ops":[{"op":"append_card","card":"k-20260901-attention","sections":{"条件与边界":"- 补充\n"}},
        {"op":"append_card","card":"k-20260815-rnn","sections":{"条件与边界":"- 补充\n"}}]}`)
	d := requireWarning(t, res, W6)
	if d.Target != "k-20260901-attention" {
		t.Fatalf("W6 应指向未被 base 覆盖的文件：%+v", d)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("其余 op 照常展开：%+v", res.Actions)
	}
	if !res.Actions[0].Skip || res.Actions[1].Skip {
		t.Fatalf("只跳过未覆盖的那个文件：%+v", res.Actions)
	}
	if res.Actions[0].SkipKind != store.SkipFileChanged ||
		res.Actions[0].SkipCause != "content_hash_mismatch" {
		t.Fatalf("跳过命名口径必须是 file_changed / content_hash_mismatch：%+v", res.Actions[0])
	}
	if res.Actions[1].ExpectedHash != "sha256:y" {
		t.Fatalf("base 覆盖的文件应带 content_hash：%+v", res.Actions[1])
	}
}

func TestW8OpposingNormalizedAndDeduped(t *testing.T) {
	env, _ := baseVault(t)
	// 输入方向为 (k-2026 09 01, k-2026 08 15)：字典序小者是 k-20260815-rnn，须被规范化。
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"冲突并存",
 "base":{"k-20260815-rnn":"sha256:y"},
 "convergence":[{"card":"k-20260815-rnn","relation":"conflict_coexist",
   "core_knowledge":"different","conditions":"same","reuse_purpose":"same"}],
 "ops":[{"op":"add_relation","from":"k-20260901-attention","type":"opposing",
   "target":"k-20260815-rnn","reason":"两卡结论对立"}]}`)
	requireWarning(t, res, W8)
	if len(res.Actions) != 1 {
		t.Fatalf("W8 不拦截：%+v", res.Actions)
	}
	act := res.Actions[0]
	if act.ID != "k-20260815-rnn" || act.Relation.Target != "k-20260901-attention" {
		t.Fatalf("opposing 必须写在字典序较小的一端：%+v", act)
	}
}

// ---------- W3 / W7：只锁定分级与诊断形态，S2 起判定，不计入门禁 ----------

// 说明（T-…-006）：这两支曾是 `t.Skip` 的"声明式占位"。统一 runner 禁止隐式 skip
// （合同 D6.2），而它们的断言体本来就只校验诊断构造器的形态（W3/W7 的码位与分级），
// 与"S1 正常链路是否会产生该 warning"无关——后者才是 S2 的判定范围。因此去掉 skip、
// 让形态断言真的执行；S2 起的链路级判定仍留给 S2，不在此处冒充覆盖。
func TestW3RelationEndNotActive(t *testing.T) {
	d := RelationStatusWarning(0, "target", "k-20260815-rnn", model.StatusDeprecated)
	if d.Code != W3 || d.Level != LevelWarning {
		t.Fatalf("W3 必须是 warning：%+v", d)
	}
}

func TestW7StateOpPlaceholder(t *testing.T) {
	d := StateOpWarning(0, "op", "状态类 op 的相关告警")
	if d.Code != W7 || d.Level != LevelWarning {
		t.Fatalf("W7 必须是 warning：%+v", d)
	}
}

// ---------- 未编号 warning / 前向兼容 / 诊断格式 ----------

func TestOpsEmptyIsUnnumberedWarning(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"零知识结果",
 "convergence":[],"base":{},"ops":[]}`)
	if res.Failed() {
		t.Fatalf("空 ops 不是失败：%v", res.Errors)
	}
	if !res.ZeroWrite {
		t.Fatal("空 ops 必须标记零写入")
	}
	d, ok := find(res.Warnings, Unnumbered)
	if !ok {
		t.Fatalf("空 ops 必须产出未编号 warning：%v", codes(res.Warnings))
	}
	if d.Code == W5 {
		t.Fatal("未编号 warning 不得复用 W5（W5 是 convergence[] 缺条目）")
	}
	if !strings.Contains(d.Message, "ops[]") {
		t.Fatalf("未编号 warning 须写明出处：%+v", d)
	}
	if len(res.Targets()) != 0 {
		t.Fatal("零写入报告不得有写入目标")
	}
}

func TestVerbUnknownDegradesToProcess(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"frobnicate","domain":"ai-infra","reason":"r","ops":[]}`)
	if res.Verb != string(model.VerbProcess) {
		t.Fatalf("未知 verb 应退化为 process：%s", res.Verb)
	}
	found := false
	for _, d := range res.Warnings {
		if d.Code == Unnumbered && strings.Contains(d.Message, "verb") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未知 verb 须产出未编号 warning：%v", res.Warnings)
	}
}

// TestRelAddVerbRelateIsKnownAndNotDegraded —— M2 起 `relate` 是已知 verb（T-…-024）：
// 校验后 verb 原样保留，不退化为 process、不产出「未知 verb」未编号 warning。
// 与上面的退化用例成对：证明「已知集合」的边界确实变了，而不是把警告静默了。
func TestRelAddVerbRelateIsKnownAndNotDegraded(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"relate","domain":"ai-infra","reason":"建立论证关系","ops":[]}`)
	if res.Verb != string(model.VerbRelate) {
		t.Fatalf("relate 必须原样保留，实际 %q", res.Verb)
	}
	for _, d := range res.Warnings {
		if d.Code == Unnumbered && strings.Contains(d.Message, "verb") {
			t.Fatalf("relate 不得再产出未知 verb 警告：%+v", d)
		}
	}
}

func TestUnknownFieldsAreInfoAndKeptForward(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"r",
 "initiator":"agent",
 "base":{"k-20260901-attention":"sha256:x"},
 "convergence":[{"card":"k-20260901-attention","relation":"non_core_supplement",
   "core_knowledge":"same","conditions":"different","reuse_purpose":"same","confidence":0.7}],
 "ops":[{"op":"append_card","card":"k-20260901-attention","sections":{"条件与边界":"- 补充\n"},
   "priority":3}]}`)
	if res.Failed() {
		t.Fatalf("未知附加字段不得拦截：%v", res.Errors)
	}
	if _, ok := find(res.Warnings, I1); !ok {
		t.Fatalf("未知附加字段须产出 I1 info：%v", codes(res.Warnings))
	}
	var paths []string
	for _, d := range res.Warnings {
		paths = append(paths, d.Path)
	}
	for _, want := range []string{"plan.initiator", "ops[0].priority", "convergence[0].confidence"} {
		if !contains(paths, want) {
			t.Fatalf("I1 诊断应带字段路径 %s：%v", want, paths)
		}
	}
	if len(res.Actions) != 1 {
		t.Fatalf("含未知字段的 plan 应正常执行：%+v", res.Actions)
	}
}

func TestDiagnosticCarriesOpIndexAndFieldPath(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"r",
 "base":{"k-20260901-attention":"sha256:x","k-20260815-rnn":"sha256:y"},
 "convergence":[{"card":"k-20260901-attention","relation":"non_core_supplement",
   "core_knowledge":"same","conditions":"different","reuse_purpose":"same"}],
 "ops":[{"op":"append_card","card":"k-20260901-attention","sections":{"条件与边界":"- a\n"}},
        {"op":"append_card","card":"k-20260815-rnn","sections":{"条件与边界":"- b\n"}},
        {"op":"add_relation","from":"k-20260901-attention","type":"limits",
         "target":"k-20260815-rnn","reason":""}]}`)
	found := false
	for _, d := range res.Diagnostics() {
		if d.Path == "ops[2].reason" && d.OpIndex == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("至少一条诊断应形如 ops[2].reason：%v", res.Diagnostics())
	}
}

func TestCoverageGapsPassThrough(t *testing.T) {
	env, files := baseVault(t)
	delete(files, "domains/ai-infra/notes/n-20260901-attention.md")
	env = vault(t, files)
	res := run(t, env, planWith(`{"op":"write_note","source":"s-20260901-attention",
 "note_id":"n-20260903-new","title":"新笔记","sections":{"材料提炼":"- 提炼\n"},
 "coverage_gaps":["counterexample","limitation"]}`))
	if res.Failed() {
		t.Fatalf("受控枚举值不应拦截：%v", res.Errors)
	}
	if _, ok := find(res.Warnings, I1); ok {
		for _, d := range res.Warnings {
			if strings.Contains(d.Path, "coverage_gaps") {
				t.Fatalf("枚举内取值不应告警：%+v", d)
			}
		}
	}
	if len(res.Actions) != 1 || len(res.Actions[0].Gaps) != 2 {
		t.Fatalf("coverage_gaps 必须原样透传：%+v", res.Actions)
	}
	res = run(t, env, planWith(`{"op":"write_note","source":"s-20260901-attention",
 "note_id":"n-20260903-new","sections":{"材料提炼":"- 提炼\n"},"coverage_gaps":["nuance"]}`))
	// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：v1 plan 现在自带两条兼容期
	// I1（`plan_version` 兼容提示 + `材料提炼` → `整理正文` 的固定映射提示，契约 §4.1），
	// 于是「取第一条 I1」会取到 plan_version 那条。改为在**全部** I1 里按字段路径定位
	// coverage_gaps 那一条 —— 比原断言更紧：既要求该条存在，也要求它的路径精确、
	// 且原样保留了越界取值本身。
	gapInfo, ok := findByPath(res.Warnings, I1, "coverage_gaps")
	if !ok {
		t.Fatalf("枚举外取值应判 I1 并原样保留：%v", res.Warnings)
	}
	if !strings.Contains(gapInfo.Message, "nuance") {
		t.Fatalf("I1 应逐字带上越界取值本身：%+v", gapInfo)
	}
	if res.Failed() {
		t.Fatal("枚举外取值不得升级为 error")
	}
	if len(res.Actions) != 1 || len(res.Actions[0].Gaps) != 1 || res.Actions[0].Gaps[0] != "nuance" {
		t.Fatalf("枚举外取值同样原样透传进 action：%+v", res.Actions)
	}
}

// findByPath 在诊断集合里按「码 + 字段路径子串」定位一条诊断。
//
// 存在的理由：v1 兼容期起，同一次校验可能同码多条（例如 `I1` 既有 plan_version 兼容提示、
// 又有字段级提示），`find` 只取第一条会让「某个字段有没有被诊断到」这类断言取到无关的一条。
func findByPath(diags []Diagnostic, code, pathSub string) (Diagnostic, bool) {
	for _, d := range diags {
		if d.Code == code && strings.Contains(d.Path, pathSub) {
			return d, true
		}
	}
	return Diagnostic{}, false
}

func TestNoteReuseByDefault(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, planWith(`{"op":"write_note","source":"s-20260901-attention",
 "note_id":"n-20260901-attention","sections":{"材料提炼":"- 又一次提炼\n"}}`))
	if res.Failed() {
		t.Fatalf("笔记已存在不是错误：%v", res.Errors)
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActNoteReuse {
		t.Fatalf("默认复用不重写：%+v", res.Actions)
	}
	if len(res.Targets()) != 0 {
		t.Fatalf("复用形态零写入：%v", res.Targets())
	}
}

func TestExpandKeepsOpsOrder(t *testing.T) {
	env, _ := baseVault(t)
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"r",
 "base":{"k-20260901-attention":"sha256:x"},
 "convergence":[{"card":"k-20260901-attention","relation":"non_core_supplement",
   "core_knowledge":"same","conditions":"different","reuse_purpose":"same"}],
 "ops":[{"op":"create_card","card_id":"k-20260902-flash","title":"FlashAttention",
   "sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support","reason":"原文实测"}],
   "sections":{"知识内容":"分块计算\n"}},
  {"op":"append_card","card":"k-20260902-flash","sections":{"条件与边界":"- 补充\n"}},
  {"op":"add_open_question","note":"n-20260901-attention","question":"长序列复杂度？"}]}`)
	if res.Failed() {
		t.Fatalf("合法 plan：%v", res.Errors)
	}
	var kinds []ActionKind
	for _, a := range res.Actions {
		kinds = append(kinds, a.Kind)
	}
	want := []ActionKind{ActCardNew, ActCardAppend, ActOpenQuestion}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("action 顺序必须等于 ops[] 声明顺序：%v", kinds)
	}
}

func TestMaterialRelClosedEnum(t *testing.T) {
	env, _ := baseVault(t)
	for _, bad := range []string{"supports", "contradicts"} {
		res := run(t, env, planWith(fmt.Sprintf(`{"op":"add_material_rel","card":"k-20260901-attention",
 "source":"s-20260901-attention","note":"n-20260901-attention","rel":%q,"reason":"理由"}`, bad)))
		d := requireError(t, res, E5)
		for _, want := range []string{"support", "against", "context"} {
			if !strings.Contains(d.Message, want) {
				t.Fatalf("错误信息须逐字列出合法取值集合：%+v", d)
			}
		}
	}
}

func TestRelationClosedEnum(t *testing.T) {
	env, _ := baseVault(t)
	for _, bad := range []string{"depends_on", "refines", "support"} {
		res := run(t, env, planWith(fmt.Sprintf(`{"op":"add_relation","from":"k-20260901-attention",
 "type":%q,"target":"k-20260815-rnn","reason":"理由"}`, bad)))
		d := requireError(t, res, E5)
		for _, want := range []string{"derives", "supports", "limits", "opposing"} {
			if !strings.Contains(d.Message, want) {
				t.Fatalf("错误信息须逐字列出合法取值集合：%+v", d)
			}
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
