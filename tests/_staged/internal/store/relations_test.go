package store

// T-…-014 的验收用例：add_material_rel / add_relation / opposing 规范化去重。
//
// 关系写入全部落在 frontmatter 序列上——断言逐字落在字节：既有条目不动、只增新条目。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/rules"
)

const relCard = `---
id: k-20260901-attention
title: 注意力机制
status: active
created_at: '2026-09-01'
sources:
  - source: s-20260901-a
    note: n-20260901-a
    rel: support
    reason: 第 1 节
  - source: s-20260901-b
    note: n-20260901-b
    rel: against
    reason: 第 2 节
  - source: s-20260901-c
    note: n-20260901-c
    rel: context
    reason: 第 3 节
tags:
  - s1
---

## 知识内容

注意力是加权求和。

## 解释与依据

## 条件与边界

## 用户补充

## 理解自检
`

const relCardOther = `---
id: k-20260815-rnn
status: active
created_at: '2026-08-15'
sources:
  - source: s-20260815-r
    note: n-20260815-r
    rel: support
    reason: 综述
---

## 知识内容

RNN 串行。

## 解释与依据

## 条件与边界

## 用户补充

## 理解自检
`

func relVault(t *testing.T) (*Store, string) {
	t.Helper()
	s, root := newVault(t)
	writeSeed(t, root, CardRel("ai-infra", "k-20260901-attention"), relCard)
	writeSeed(t, root, CardRel("ai-infra", "k-20260815-rnn"), relCardOther)
	return s, root
}

func cardPath(id string) string { return CardRel("ai-infra", id) }

func mustCard(t *testing.T, root, id string) model.Card {
	t.Helper()
	card, err := CardOf(mustBytes(t, filepath.Join(root, cardPath(id))))
	if err != nil {
		t.Fatalf("解析 %s：%v", id, err)
	}
	return card
}

// ---------- 封闭枚举（F4） ----------

func TestMaterialRelEnumIsClosed(t *testing.T) {
	s, root := relVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)
	for _, bad := range []string{"supports", "contradicts", "", "SUPPORT"} {
		res, err := s.ApplyMaterialRel(MaterialRelSpec{Rel: cardPath("k-20260901-attention"),
			Ref: model.SourceRef{Source: "s-20260901-d", Note: "n-20260901-d",
				Rel: model.MaterialRel(bad), Reason: "理由"}})
		if err == nil || res.Written {
			t.Fatalf("材料 rel=%q 必须拒绝：%v / %+v", bad, err, res)
		}
		for _, want := range []string{"support", "against", "context"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("错误信息须逐字列出合法取值集合，缺 %q：%v", want, err)
			}
		}
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("拒绝时目标卡字节必须不变")
	}
}

func TestRelationTypeEnumIsClosed(t *testing.T) {
	s, root := relVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)
	for _, bad := range []string{"depends_on", "refines", "support", ""} {
		res, err := s.ApplyRelation(RelationSpec{Rel: cardPath("k-20260901-attention"),
			From:     "k-20260901-attention",
			Relation: model.Relation{Type: model.RelationType(bad), Target: "k-20260815-rnn", Reason: "理由"}})
		if err == nil || res.Written {
			t.Fatalf("论证 type=%q 必须拒绝：%v / %+v", bad, err, res)
		}
		for _, want := range []string{"derives", "supports", "limits", "opposing"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("错误信息须逐字列出合法取值集合，缺 %q：%v", want, err)
			}
		}
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("拒绝时目标卡字节必须不变")
	}
}

// ---------- E3 双向反例 ----------

func TestRelationE3SourceIDIntoRelations(t *testing.T) {
	s, root := relVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)
	res, err := s.ApplyRelation(RelationSpec{Rel: cardPath("k-20260901-attention"),
		From:     "k-20260901-attention",
		Relation: model.Relation{Type: model.RelationLimits, Target: "s-20260901-a", Reason: "写混了"}})
	if err == nil || res.Written {
		t.Fatalf("s- 写进 relations[].target 必须拒绝：%v / %+v", err, res)
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("目标卡字节必须不变")
	}
}

func TestMaterialRelE3CardIDIntoSources(t *testing.T) {
	s, root := relVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)
	res, err := s.ApplyMaterialRel(MaterialRelSpec{Rel: cardPath("k-20260901-attention"),
		Ref: model.SourceRef{Source: "k-20260815-rnn", Note: "n-20260901-d",
			Rel: model.MaterialSupport, Reason: "写混了"}})
	if err == nil || res.Written {
		t.Fatalf("k- 写进 sources[].source 必须拒绝：%v / %+v", err, res)
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("目标卡字节必须不变")
	}
}

// ---------- E2 悬空引用 ----------

func TestRelationE2UnresolvedTarget(t *testing.T) {
	s, root := relVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)
	for _, target := range []string{"k-20260101-missing", "note-3"} {
		res, err := s.ApplyRelation(RelationSpec{Rel: cardPath("k-20260901-attention"),
			From:     "k-20260901-attention",
			Relation: model.Relation{Type: model.RelationLimits, Target: model.RelationEndpoint(target), Reason: "限定"}})
		if err == nil || res.Written {
			t.Fatalf("target=%q 必须拒绝：%v / %+v", target, err, res)
		}
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("零写入：目标卡字节必须不变")
	}
}

// ---------- opposing 规范化与去重（EG-CVG-05） ----------

func TestRelationOpposingIsStoredOnceOnLexicalSmallerEnd(t *testing.T) {
	for _, order := range [][2]model.RelationEndpoint{
		{"k-20260901-attention", "k-20260815-rnn"},
		{"k-20260815-rnn", "k-20260901-attention"},
	} {
		s, root := relVault(t)
		pair := rules.Opposing(order[0], order[1])
		if pair.From != "k-20260815-rnn" {
			t.Fatalf("规范化后写入端应是字典序较小者，实得 %s", pair.From)
		}
		res, err := s.ApplyRelation(RelationSpec{From: model.CardID(pair.From),
			Relation: model.Relation{Type: model.RelationOpposing, Target: pair.Target,
				Reason: "两者对长序列的结论相反"}})
		if err != nil || !res.Written {
			t.Fatalf("输入顺序 %v：opposing 应写入：%v / %+v", order, err, res)
		}
		if res.Path != cardPath("k-20260815-rnn") {
			t.Fatalf("必须写在字典序较小的一端，实得 %s", res.Path)
		}
		small := mustCard(t, root, "k-20260815-rnn")
		big := mustCard(t, root, "k-20260901-attention")
		if len(small.Relations) != 1 || len(big.Relations) != 0 {
			t.Fatalf("库中只应有一条 opposing 记录：%v / %v", small.Relations, big.Relations)
		}
		if small.Relations[0].Target != "k-20260901-attention" {
			t.Fatalf("方向不对：%+v", small.Relations[0])
		}
		// 无议题组对象：既无 frontmatter 键，也无正文小节
		for _, rel := range []string{cardPath("k-20260815-rnn"), cardPath("k-20260901-attention")} {
			raw := mustBytes(t, filepath.Join(root, rel))
			for _, ban := range []string{"issue_group", "issueGroup", "议题组"} {
				if bytes.Contains(raw, []byte(ban)) {
					t.Fatalf("%s 出现 %q：一对一条记录，不聚合成第三方对象", rel, ban)
				}
			}
		}
	}
}

func TestRelationOpposingDuplicateIsIdempotent(t *testing.T) {
	s, root := relVault(t)
	pair := rules.Opposing("k-20260901-attention", "k-20260815-rnn")
	spec := RelationSpec{From: model.CardID(pair.From),
		Relation: model.Relation{Type: model.RelationOpposing, Target: pair.Target, Reason: "结论相反"}}
	if _, err := s.ApplyRelation(spec); err != nil {
		t.Fatalf("首次写入：%v", err)
	}
	abs := filepath.Join(root, cardPath("k-20260815-rnn"))
	after := mustBytes(t, abs)

	spec.Relation.Reason = "换个理由再提交一次"
	res, err := s.ApplyRelation(spec)
	if err != nil {
		t.Fatalf("重复提交不应报错：%v", err)
	}
	if res.Written {
		t.Fatal("同对重复不得产生第二条")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("同对重复应产出 warning 进报告")
	}
	if !bytes.Equal(mustBytes(t, abs), after) {
		t.Fatal("幂等：条目数与字节都不变")
	}
	if card := mustCard(t, root, "k-20260815-rnn"); len(card.Relations) != 1 {
		t.Fatalf("关系条目数必须不变，实得 %d", len(card.Relations))
	}
}

func TestRelationOpposingMustBeNormalizedBeforeWrite(t *testing.T) {
	s, _ := relVault(t)
	res, err := s.ApplyRelation(RelationSpec{From: "k-20260901-attention",
		Relation: model.Relation{Type: model.RelationOpposing, Target: "k-20260815-rnn",
			Reason: "未规范化的方向"}})
	if err == nil || res.Written {
		t.Fatalf("未规范化方向必须拒写（规范化由 rules.Opposing 负责）：%v / %+v", err, res)
	}
}

// ---------- 追加语义与 W2 ----------

func TestMaterialRelAppendKeepsExistingEntriesVerbatim(t *testing.T) {
	s, root := relVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)

	res, err := s.ApplyMaterialRel(MaterialRelSpec{Rel: cardPath("k-20260901-attention"),
		Ref: model.SourceRef{Source: "s-20260901-d", Note: "n-20260901-d",
			Rel: model.MaterialSupport, Reason: "第 4 节"}})
	if err != nil || !res.Written {
		t.Fatalf("追加应成功：%v / %+v", err, res)
	}
	card := mustCard(t, root, "k-20260901-attention")
	if len(card.Sources) != 4 {
		t.Fatalf("3 条 + 1 条 = 4 条，实得 %d", len(card.Sources))
	}
	for i, want := range []model.SourceID{"s-20260901-a", "s-20260901-b", "s-20260901-c", "s-20260901-d"} {
		if card.Sources[i].Source != want {
			t.Fatalf("既有条目顺序必须不变：%v", card.Sources)
		}
	}
	after := mustBytes(t, abs)
	added := []byte("  - source: 's-20260901-d'\n    note: 'n-20260901-d'\n    rel: 'support'\n    reason: '第 4 节'\n")
	idx := bytes.Index(after, added)
	if idx < 0 {
		t.Fatalf("新条目应沿用既有缩进逐字追加：\n%s", after)
	}
	rest := append(append([]byte{}, after[:idx]...), after[idx+len(added):]...)
	if !bytes.Equal(rest, before) {
		t.Fatalf("除新增条目外其余字节必须逐字不变：\n%q", rest)
	}
}

func TestMaterialRelReasonWarningStillWrites(t *testing.T) {
	s, root := relVault(t)
	res, err := s.ApplyMaterialRel(MaterialRelSpec{Rel: cardPath("k-20260901-attention"),
		Ref: model.SourceRef{Source: "s-20260901-d", Note: "n-20260901-d",
			Rel: model.MaterialSupport, Reason: "support"}})
	if err != nil || !res.Written {
		t.Fatalf("W2 只告警不拦截：%v / %+v", err, res)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("reason 等于关系名本身应出 warning")
	}
	if card := mustCard(t, root, "k-20260901-attention"); len(card.Sources) != 4 {
		t.Fatalf("关系应照常写入，实得 %d 条", len(card.Sources))
	}

	res, err = s.ApplyMaterialRel(MaterialRelSpec{Rel: cardPath("k-20260901-attention"),
		Ref: model.SourceRef{Source: "s-20260901-e", Rel: model.MaterialSupport}})
	if err != nil || !res.Written {
		t.Fatalf("四要素不全也照写：%v / %+v", err, res)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("四要素不全应出 warning")
	}
}

func TestMaterialRelDuplicateIsIdempotent(t *testing.T) {
	s, root := relVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)
	res, err := s.ApplyMaterialRel(MaterialRelSpec{Rel: cardPath("k-20260901-attention"),
		Ref: model.SourceRef{Source: "s-20260901-a", Note: "n-20260901-a",
			Rel: model.MaterialSupport, Reason: "第 1 节"}})
	if err != nil {
		t.Fatalf("完全重复不应报错：%v", err)
	}
	if res.Written {
		t.Fatal("完全重复的关系必须幂等去重")
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("幂等：字节不变")
	}
}

// ---------- relations[] 键缺失时新建块状序列 ----------

func TestRelationCreatesBlockSequenceWhenKeyMissing(t *testing.T) {
	s, root := relVault(t)
	res, err := s.ApplyRelation(RelationSpec{Rel: cardPath("k-20260901-attention"),
		From: "k-20260901-attention",
		Relation: model.Relation{Type: model.RelationDerives, Target: "k-20260815-rnn",
			Reason: "由 RNN 的局限推出"}})
	if err != nil || !res.Written {
		t.Fatalf("首条论证关系应写入：%v / %+v", err, res)
	}
	raw := mustBytes(t, filepath.Join(root, cardPath("k-20260901-attention")))
	want := "relations:\n  - type: 'derives'\n    target: 'k-20260815-rnn'\n    reason: '由 RNN 的局限推出'\n"
	if !bytes.Contains(raw, []byte(want)) {
		t.Fatalf("应新建块状序列：\n%s", raw)
	}
	doc, err := mdfile.Parse(raw)
	if err != nil {
		t.Fatalf("落盘结果必须可解析：%v", err)
	}
	var fm map[string]interface{}
	if err := doc.DecodeFM(&fm); err != nil {
		t.Fatalf("落盘结果必须是合法 YAML：%v", err)
	}
	if _, ok := fm["relations"]; !ok {
		t.Fatal("relations 键应生效")
	}
	// 第二条沿用刚建立的缩进风格
	if _, err := s.ApplyRelation(RelationSpec{Rel: cardPath("k-20260901-attention"),
		From: "k-20260901-attention",
		Relation: model.Relation{Type: model.RelationLimits, Target: "k-20260815-rnn",
			Reason: "适用面不同"}}); err != nil {
		t.Fatalf("第二条应写入：%v", err)
	}
	if card := mustCard(t, root, "k-20260901-attention"); len(card.Relations) != 2 {
		t.Fatalf("应有 2 条论证关系，实得 %v", card.Relations)
	}
}

func TestRelationEmptySequenceIsNotGuessed(t *testing.T) {
	s, root := newVault(t)
	empty := strings.Replace(relCard, `sources:
  - source: s-20260901-a
    note: n-20260901-a
    rel: support
    reason: 第 1 节
  - source: s-20260901-b
    note: n-20260901-b
    rel: against
    reason: 第 2 节
  - source: s-20260901-c
    note: n-20260901-c
    rel: context
    reason: 第 3 节
`, "sources:\n", 1)
	abs := writeSeed(t, root, cardPath("k-20260901-attention"), empty)
	before := mustBytes(t, abs)
	res, err := s.ApplyMaterialRel(MaterialRelSpec{Rel: cardPath("k-20260901-attention"),
		Ref: model.SourceRef{Source: "s-20260901-d", Note: "n-20260901-d",
			Rel: model.MaterialSupport, Reason: "第 4 节"}})
	if err != nil {
		t.Fatalf("空序列不猜缩进，只出 warning：%v", err)
	}
	if res.Written {
		t.Fatal("空序列时本条不写")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("空序列应出 warning 进报告")
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("未写入时字节必须不变")
	}
}

// ---------- F2：关系只引用 ID，文件移动不影响有效性 ----------

func TestRelationSurvivesFileRename(t *testing.T) {
	s, root := relVault(t)
	from := filepath.Join(root, cardPath("k-20260815-rnn"))
	to := filepath.Join(root, "domains/ai-infra/knowledge/任意改名.md")
	if err := os.Rename(from, to); err != nil {
		t.Fatalf("rename: %v", err)
	}
	res, err := s.ApplyRelation(RelationSpec{From: "k-20260815-rnn",
		Relation: model.Relation{Type: model.RelationOpposing, Target: "k-20260901-attention",
			Reason: "结论相反"}})
	if err != nil || !res.Written {
		t.Fatalf("改名后仍应按 ID 定位并写入：%v / %+v", err, res)
	}
	if res.Path != "domains/ai-infra/knowledge/任意改名.md" {
		t.Fatalf("路径应由 id 扫描得到，实得 %s", res.Path)
	}
}

// ---------- W3：S2 起判定，只锁形态，不计入门禁 ----------

// 曾是空体 `t.Skip` 占位（T-…-006 去 skip）。"warning-only"在 store 层其实是可判的：
// 把关系的目标端置成 `deprecated` 之后，ApplyRelation 仍必须写入成功——store 不因端
// 状态非 active 而拦截，拦不拦截由上层诊断分级决定。W3 究竟会不会在正常链路上产生，
// 仍是 S2 的判定范围，这里不冒充。
func TestRelationEndNotActiveIsWarningOnly(t *testing.T) {
	s, root := relVault(t)
	target := cardPath("k-20260901-attention")
	before := mustCard(t, root, "k-20260901-attention")
	// 时刻传零值：本用例判的是「端状态非 active 也照样写入」，与时间戳刷新无关
	// （零值 = 不要求刷新，见 internal/store/updated_at.go）。
	if _, err := s.SetStatus(target, "", model.StatusDeprecated, model.Stamp{}); err != nil {
		t.Fatalf("把目标端置为 deprecated：%v", err)
	}
	if got := mustCard(t, root, "k-20260901-attention").Status; got != model.StatusDeprecated {
		t.Fatalf("前置条件不成立：目标端 status 实得 %q", got)
	}
	if before.Status == model.StatusDeprecated {
		t.Fatalf("语料前提错误：目标端本来就该是 active，实得 %q", before.Status)
	}
	res, err := s.ApplyRelation(RelationSpec{From: "k-20260815-rnn",
		Relation: model.Relation{Type: model.RelationOpposing, Target: "k-20260901-attention",
			Reason: "结论相反"}})
	if err != nil || !res.Written {
		t.Fatalf("端状态非 active 只该是 warning，store 不得拦截写入：%v / %+v", err, res)
	}
}
