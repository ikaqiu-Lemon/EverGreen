package store

// T-006-B2c Phase 2：论证关系写链路的**跨类型端点**落盘取证（store 侧）。
//
// 论证关系是跨类型的（端点前缀 k- / o-，见 model.RelationEndpoint）：宿主与对端都可能
// 是知识卡或观点。本文件把 add_relation / remove_relation 的四组合（k→k / k→o /
// o→k / o→o）逐一钉在字节上：
//   - 关系恒写在 **from 宿主自身** 的 relations[]，对端文件一个字节都不动（单向存储）；
//   - o- 宿主与 k- 宿主同口径：既有条目 / 正文 / 用户补充逐字保留，updated_at 整行推进；
//   - 非 k/o 端点、悬空端点、自环、未规范化 opposing 一律零写入、零副作用（B4 不回滚不吞错）。
//
// 与 relations_test.go（k→k 基线）、remove_relation_test.go（k→k 物理移除）互补：
// 那两份锁 k-only 的历史语义不放宽，本文件只加**跨类型**的新判据。

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/rules"
)

// ---------- 跨类型夹具：两张知识卡 + 两条观点 ----------

const koCardA = `---
id: k-20260901-koa
title: 知识卡 A
status: active
created_at: '2026-09-01'
sources: []
tags:
  - s1
---

## 知识内容

卡 A 的知识内容。

## 条件与边界

## 用户补充

卡 A 的用户私货，勿动。

## 理解自检
`

const koCardB = `---
id: k-20260901-kob
title: 知识卡 B
status: active
created_at: '2026-09-01'
sources: []
---

## 知识内容

卡 B 的知识内容。

## 条件与边界

## 用户补充

## 理解自检
`

// koOpinionA 带 updated_at（供刷新判据）与用户补充（供 B2 保真判据），
// **不带 relations 键**：首条关系应新建块状序列（与知识卡同口径）。
const koOpinionA = `---
id: o-20260901-opa
status: active
validation: pending
created_at: '2026-09-01'
updated_at: '2026-09-01T00:00:00+08:00'
sources: []
---

## 观点

观点 A 的主张。

## 论据与推理

占位论据。

## 用户补充

观点 A 的用户私货，勿动。
`

const koOpinionB = `---
id: o-20260901-opb
status: active
validation: pending
created_at: '2026-09-01'
sources: []
---

## 观点

观点 B 的主张。

## 论据与推理

占位论据。
`

// koVault 建一个含四实体（k-koa / k-kob / o-opa / o-opb）的库。
func koVault(t *testing.T) (*Store, string) {
	t.Helper()
	s, root := newVault(t)
	writeSeed(t, root, CardRel("ai-infra", "k-20260901-koa"), koCardA)
	writeSeed(t, root, CardRel("ai-infra", "k-20260901-kob"), koCardB)
	writeSeed(t, root, OpinionRel("ai-infra", "o-20260901-opa"), koOpinionA)
	writeSeed(t, root, OpinionRel("ai-infra", "o-20260901-opb"), koOpinionB)
	return s, root
}

// hostRel 把端点 ID 映射回它在库里的相对路径（k- 走 knowledge、o- 走 opinions）。
func hostRel(id string) string {
	if strings.HasPrefix(id, model.PrefixOpinion) {
		return OpinionRel("ai-infra", id)
	}
	return CardRel("ai-infra", id)
}

// hostRelations 读回端点宿主的 relations[]（类型无关，按前缀分流）。
func hostRelations(t *testing.T, root, id string) []model.Relation {
	t.Helper()
	host, err := RelationHostOf(model.RelationEndpoint(id), mustBytes(t, filepath.Join(root, hostRel(id))))
	if err != nil {
		t.Fatalf("解析宿主 %s 的关系事实：%v", id, err)
	}
	return host.Relations
}

// ---------- add_relation 四组合：关系写在 from 宿主，对端零改动 ----------

func TestAddRelationAcrossKindsWritesOnFromHostOnly(t *testing.T) {
	cases := []struct {
		name, from, target string
	}{
		{"k_to_k", "k-20260901-koa", "k-20260901-kob"},
		{"k_to_o", "k-20260901-koa", "o-20260901-opb"},
		{"o_to_k", "o-20260901-opa", "k-20260901-kob"},
		{"o_to_o", "o-20260901-opa", "o-20260901-opb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, root := koVault(t)
			targetAbs := filepath.Join(root, hostRel(tc.target))
			targetBefore := mustBytes(t, targetAbs)

			res, err := s.ApplyRelation(RelationSpec{
				Rel:  hostRel(tc.from),
				From: model.RelationEndpoint(tc.from),
				Relation: model.Relation{Type: model.RelationSupports,
					Target: model.RelationEndpoint(tc.target), Reason: "跨类型 supports"},
			})
			if err != nil || !res.Written {
				t.Fatalf("%s：add_relation 应写入：%v / %+v", tc.name, err, res)
			}
			if res.Path != hostRel(tc.from) {
				t.Fatalf("%s：关系必须写在 from 宿主 %s，实得 %s", tc.name, hostRel(tc.from), res.Path)
			}
			rels := hostRelations(t, root, tc.from)
			if len(rels) != 1 || string(rels[0].Target) != tc.target ||
				rels[0].Type != model.RelationSupports {
				t.Fatalf("%s：from 宿主应恰有 1 条指向 %s 的 supports，实得 %+v", tc.name, tc.target, rels)
			}
			// 单向存储：对端一个字节都不动（连它自己的 updated_at 都不许被刷新）。
			if !bytes.Equal(mustBytes(t, targetAbs), targetBefore) {
				t.Fatalf("%s：关系只写 from 宿主，对端 %s 字节必须逐字不变", tc.name, tc.target)
			}
		})
	}
}

// ---------- remove_relation 四组合：物理移除 from 宿主上的匹配条目 ----------

func TestRemoveRelationAcrossKindsRemovesFromHostOnly(t *testing.T) {
	cases := []struct {
		name, from, target string
	}{
		{"k_to_k", "k-20260901-koa", "k-20260901-kob"},
		{"k_to_o", "k-20260901-koa", "o-20260901-opb"},
		{"o_to_k", "o-20260901-opa", "k-20260901-kob"},
		{"o_to_o", "o-20260901-opa", "o-20260901-opb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, root := koVault(t)
			// 先在 from 宿主上落一条 supports（复用 add 链路），再点名删除它。
			if res, err := s.ApplyRelation(RelationSpec{
				Rel:  hostRel(tc.from),
				From: model.RelationEndpoint(tc.from),
				Relation: model.Relation{Type: model.RelationSupports,
					Target: model.RelationEndpoint(tc.target), Reason: "待删的 supports"},
			}); err != nil || !res.Written {
				t.Fatalf("%s：前置 add_relation 应成功：%v / %+v", tc.name, err, res)
			}
			if got := len(hostRelations(t, root, tc.from)); got != 1 {
				t.Fatalf("%s：前置条件，from 宿主应有 1 条关系，实得 %d", tc.name, got)
			}
			targetAbs := filepath.Join(root, hostRel(tc.target))
			targetBefore := mustBytes(t, targetAbs)

			out, err := s.ApplyRemoveRelation(RemoveRelationSpec{
				Rel:    hostRel(tc.from),
				From:   model.RelationEndpoint(tc.from),
				Type:   model.RelationSupports,
				Target: model.RelationEndpoint(tc.target),
			})
			if err != nil || !out.Written || out.Removed != 1 {
				t.Fatalf("%s：remove_relation 应物理移除 1 条：%v / %+v", tc.name, err, out)
			}
			if got := len(hostRelations(t, root, tc.from)); got != 0 {
				t.Fatalf("%s：from 宿主关系应清空，实得 %d", tc.name, got)
			}
			if !bytes.Equal(mustBytes(t, targetAbs), targetBefore) {
				t.Fatalf("%s：删除只动 from 宿主，对端 %s 字节必须不变", tc.name, tc.target)
			}
		})
	}
}

// ---------- opposing 跨类型：按 ID 全串字典序规范化，写在较小端，幂等 ----------

// k- 与 o- 相比 'k' < 'o'，因此 opposing 恒规范化到知识卡一端。
func TestOpposingCrossKindNormalizesToLexicalSmallerEnd(t *testing.T) {
	s, root := koVault(t)
	pair := rules.Opposing("o-20260901-opa", "k-20260901-koa")
	if pair.From != "k-20260901-koa" || pair.Target != "o-20260901-opa" {
		t.Fatalf("跨类型 opposing 应规范化到字典序较小端 k-…：实得 from=%s target=%s", pair.From, pair.Target)
	}
	spec := RelationSpec{
		Rel:      hostRel(string(pair.From)),
		From:     pair.From,
		Relation: model.Relation{Type: model.RelationOpposing, Target: pair.Target, Reason: "结论相反"},
	}
	if res, err := s.ApplyRelation(spec); err != nil || !res.Written {
		t.Fatalf("规范化后的 opposing 应写在 k- 端：%v / %+v", err, res)
	}
	// 写在 k- 端；o- 端零关系。
	if got := len(hostRelations(t, root, "k-20260901-koa")); got != 1 {
		t.Fatalf("opposing 应写在 k- 端且恰一条，实得 %d", got)
	}
	if got := len(hostRelations(t, root, "o-20260901-opa")); got != 0 {
		t.Fatalf("单向存储：o- 端不得出现第二条记录，实得 %d", got)
	}
	// 幂等：换个 reason 再提交一次，不产生第二条、字节不变。
	kAbs := filepath.Join(root, hostRel("k-20260901-koa"))
	after := mustBytes(t, kAbs)
	spec.Relation.Reason = "换个理由"
	res, err := s.ApplyRelation(spec)
	if err != nil {
		t.Fatalf("重复 opposing 不应报错：%v", err)
	}
	if res.Written {
		t.Fatal("同对 opposing 必须幂等，不产生第二条")
	}
	if !bytes.Equal(mustBytes(t, kAbs), after) {
		t.Fatal("幂等：字节必须逐字不变")
	}
}

// 未规范化方向（写在字典序较大的 o- 端）必须拒写，零副作用。
func TestOpposingCrossKindRejectsUnnormalizedDirection(t *testing.T) {
	s, root := koVault(t)
	oAbs := filepath.Join(root, hostRel("o-20260901-opa"))
	before := mustBytes(t, oAbs)
	res, err := s.ApplyRelation(RelationSpec{
		Rel:  hostRel("o-20260901-opa"),
		From: "o-20260901-opa",
		Relation: model.Relation{Type: model.RelationOpposing,
			Target: "k-20260901-koa", Reason: "方向未规范化"},
	})
	if err == nil || res.Written {
		t.Fatalf("未规范化的 opposing（写在较大端 o-）必须拒写：%v / %+v", err, res)
	}
	if !bytes.Equal(mustBytes(t, oAbs), before) {
		t.Fatal("拒写必须零字节变化")
	}
}

// ---------- o- 宿主：非目标字节保真 + updated_at 推进 ----------

func TestAddRelationOnOpinionHostFidelityAndUpdatedAt(t *testing.T) {
	s, root := koVault(t)
	abs := filepath.Join(root, hostRel("o-20260901-opa"))
	before := mustBytes(t, abs)

	res, err := s.ApplyRelation(RelationSpec{
		Rel:  hostRel("o-20260901-opa"),
		From: "o-20260901-opa",
		Relation: model.Relation{Type: model.RelationSupports,
			Target: "k-20260901-koa", Reason: "观点支持该卡"},
		Stamp: refreshStamp(t),
	})
	if err != nil || !res.Written {
		t.Fatalf("观点宿主 add_relation 应写入：%v / %+v", err, res)
	}
	after := mustBytes(t, abs)

	// updated_at 整行推进到注入时刻，且恰一行。
	fm := fmOf(t, after)
	if !strings.Contains(fm, wantUpdatedAtLine()) {
		t.Fatalf("观点宿主写关系必须刷新 updated_at：%s", fm)
	}
	if n := strings.Count(fm, "updated_at:"); n != 1 {
		t.Fatalf("updated_at 必须恰一行，实得 %d：\n%s", n, fm)
	}
	// 用户补充与观点正文逐字保留（B2）。
	for _, keep := range []string{"观点 A 的用户私货，勿动。", "## 观点", "观点 A 的主张。", "占位论据。"} {
		if !bytes.Contains(after, []byte(keep)) {
			t.Fatalf("观点宿主的正文 / 用户补充必须逐字保留，缺 %q：\n%s", keep, after)
		}
	}
	// 除新增 relations 块与 updated_at 行外，frontmatter 其余字节不失真：
	// 反向核验既有键仍在。
	for _, keep := range []string{"id: o-20260901-opa", "status: active", "validation: pending",
		"created_at: '2026-09-01'"} {
		if !bytes.Contains(after, []byte(keep)) {
			t.Fatalf("观点宿主既有 frontmatter 键必须保留，缺 %q：\n%s", keep, fm)
		}
	}
	// 关系块确实新建成功。
	if got := hostRelations(t, root, "o-20260901-opa"); len(got) != 1 ||
		string(got[0].Target) != "k-20260901-koa" {
		t.Fatalf("观点宿主应新建一条指向 k-20260901-koa 的关系，实得 %+v", got)
	}
	if bytes.Equal(after, before) {
		t.Fatal("写入必须改动字节（Git diff 可见）")
	}
}

// ---------- 失败零副作用：非 k/o 端点 / 悬空端点 / 自环 ----------

func TestRelationWriteFailuresLeaveOpinionHostByteIdentical(t *testing.T) {
	badTargets := []struct {
		name, target string
	}{
		{"source_id", "s-20260901-x"},     // s- 写进 relations[].target → E3
		{"note_id", "n-20260901-x"},       // n- 非论证性产物 → 拒绝
		{"unresolved", "k-20260101-gone"}, // 悬空端点 → E2 / unresolved
	}
	for _, tc := range badTargets {
		t.Run(tc.name, func(t *testing.T) {
			s, root := koVault(t)
			abs := filepath.Join(root, hostRel("o-20260901-opa"))
			before := mustBytes(t, abs)
			res, err := s.ApplyRelation(RelationSpec{
				Rel:  hostRel("o-20260901-opa"),
				From: "o-20260901-opa",
				Relation: model.Relation{Type: model.RelationSupports,
					Target: model.RelationEndpoint(tc.target), Reason: "非法端点"},
				Stamp: refreshStamp(t),
			})
			if err == nil || res.Written {
				t.Fatalf("%s：非法 / 悬空端点必须拒写：%v / %+v", tc.name, err, res)
			}
			if !bytes.Equal(mustBytes(t, abs), before) {
				t.Fatalf("%s：拒写必须零副作用（含不刷新 updated_at）", tc.name)
			}
		})
	}
}

// 自环（from == target，无论 k 还是 o）必须拒写、零副作用。
func TestRelationSelfLoopOnOpinionHostRejected(t *testing.T) {
	s, root := koVault(t)
	abs := filepath.Join(root, hostRel("o-20260901-opa"))
	before := mustBytes(t, abs)
	res, err := s.ApplyRelation(RelationSpec{
		Rel:  hostRel("o-20260901-opa"),
		From: "o-20260901-opa",
		Relation: model.Relation{Type: model.RelationSupports,
			Target: "o-20260901-opa", Reason: "自环"},
		Stamp: refreshStamp(t),
	})
	if err == nil || res.Written {
		t.Fatalf("自环必须拒写：%v / %+v", err, res)
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("自环拒写必须零字节变化")
	}
}
