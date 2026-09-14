package store

// T-…-044 的落盘语义判据：`remove_relation` 的存储侧行为必须与 owner 裁决逐字一致。
//
// 逐字引用 032 交付物 `2026-10-13-m3-prestart-adjudication.md` 中 A-24 行的结论字样：
// **`物理移除`**（§7.2「A-24：采用 `物理移除`」，附加约束 5 条）。因此本文件断言的是
// 「条目数 −1 且 Git diff 可见、不留墓碑、不创建 RelationID」这一侧，
// **不是**「标记删除」那一侧——两侧互斥，不得同时断言。
//
// 与 §5.2「关系记录不被物理删除」并不冲突，二者主语不同：
//   - §5.2 说的是**逻辑删除一张卡**时不得级联清掉指向它的关系（反证在
//     state_write_deleted_test.go 的 TestLogicalDelete_RelationsPreserved，仍须全绿）；
//   - 本文件说的是**用户点名删一条关系**这个独立动作（A-24 第 5 条把两者显式分开）。

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/rules"
)

// removeRelSeed 是带三条正向关系的宿主卡：同三元组两条（验「移除全部匹配」）+ 一条无关关系。
const removeRelSeed = `---
id: k-20260901-attention
title: 注意力机制
status: active
created_at: '2026-09-01'
sources:
  - source: s-20260901-a
    note: n-20260901-a
    rel: support
    reason: 第 1 节
relations:
  - type: limits
    target: k-20260815-rnn
    reason: 早年的结论已不适用
  - type: limits
    target: k-20260815-rnn
    reason: 历史重复条目（同三元组）
  - type: supports
    target: k-20260815-rnn
    reason: 另一条不该被牵连的关系
tags:
  - s1
---

## 知识内容

注意力是加权求和。

## 解释与依据

## 条件与边界

## 用户补充

我自己的理解：先看 QKV。

## 理解自检
`

// removeRelVault 建库：宿主卡带上面三条关系，对端卡沿用既有夹具。
func removeRelVault(t *testing.T) (*Store, string) {
	t.Helper()
	s, root := newVault(t)
	writeSeed(t, root, CardRel("ai-infra", "k-20260901-attention"), removeRelSeed)
	writeSeed(t, root, CardRel("ai-infra", "k-20260815-rnn"), relCardOther)
	return s, root
}

// TestRemoveRelation_StorageSemantics：A-24 裁决 `物理移除` 的落盘取证（5 条约束逐条）。
func TestRemoveRelation_StorageSemantics(t *testing.T) {
	// ① 命中：条目数 −1（这里同三元组两条 → 3 条降到 1 条），Git diff 可见（字节变化）。
	s, root := removeRelVault(t)
	abs := filepath.Join(root, cardPath("k-20260901-attention"))
	before := mustBytes(t, abs)
	if got := len(mustCard(t, root, "k-20260901-attention").Relations); got != 3 {
		t.Fatalf("前置条件：宿主卡应有 3 条关系，实得 %d", got)
	}

	out, err := s.ApplyRemoveRelation(RemoveRelationSpec{
		Rel: cardPath("k-20260901-attention"), From: "k-20260901-attention",
		Type: model.RelationLimits, Target: "k-20260815-rnn",
	})
	if err != nil {
		t.Fatalf("命中时应写入成功：%v", err)
	}
	if !out.Written {
		t.Fatal("命中必须真的写盘（条目数 −1 且 Git diff 可见）")
	}
	if out.Removed != 2 {
		t.Fatalf("A-24 第 1 条：匹配的**全部**记录都要移除，应为 2 条，实得 %d", out.Removed)
	}
	after := mustBytes(t, abs)
	if bytes.Equal(before, after) {
		t.Fatal("物理移除必须改动字节（Git diff 可见），不是标记删除")
	}
	card := mustCard(t, root, "k-20260901-attention")
	if len(card.Relations) != 1 {
		t.Fatalf("剩余关系应恰 1 条（那条 supports），实得 %d：%+v", len(card.Relations), card.Relations)
	}
	if card.Relations[0].Type != model.RelationSupports {
		t.Fatalf("不得牵连其他三元组：%+v", card.Relations[0])
	}
	// A-24 第 3 条：不留墓碑、不创建 RelationID——被删条目在字节层彻底消失。
	for _, ban := range []string{"removed_at", "removed", "relation_id", "RelationID", "已移除", "墓碑"} {
		if bytes.Contains(after, []byte(ban)) {
			t.Fatalf("不得留任何墓碑 / 新主键（命中 %q）：\n%s", ban, after)
		}
	}
	if strings.Count(string(after), "早年的结论已不适用") != 0 ||
		strings.Count(string(after), "历史重复条目") != 0 {
		t.Fatalf("被移除条目的字节必须彻底消失：\n%s", after)
	}
	// 正文与其余 frontmatter 键逐字保留（B2：用户补充一字不动）。
	for _, keep := range []string{"我自己的理解：先看 QKV。", "source: s-20260901-a", "tags:", "- s1"} {
		if !bytes.Contains(after, []byte(keep)) {
			t.Fatalf("正文与未涉及的 YAML 字段必须逐字保留，缺 %q：\n%s", keep, after)
		}
	}

	// ② 未命中：ErrRelationNotFound + **零写入**（A-24 第 4 条，上层据此判 W10 幂等）。
	s2, root2 := removeRelVault(t)
	abs2 := filepath.Join(root2, cardPath("k-20260901-attention"))
	snap := mustBytes(t, abs2)
	out2, err2 := s2.ApplyRemoveRelation(RemoveRelationSpec{
		Rel: cardPath("k-20260901-attention"), From: "k-20260901-attention",
		Type: model.RelationDerives, Target: "k-20260815-rnn",
	})
	if err2 == nil {
		t.Fatal("未命中必须返回 ErrRelationNotFound（供上层判 W10）")
	}
	if !errors.Is(err2, ErrRelationNotFound) {
		t.Fatalf("未命中的错误必须是 ErrRelationNotFound，实得 %v", err2)
	}
	if out2.Written || out2.Removed != 0 {
		t.Fatalf("未命中必须零写入：%+v", out2)
	}
	if !bytes.Equal(snap, mustBytes(t, abs2)) {
		t.Fatal("未命中必须字节不变（不产生空 commit 的前提）")
	}

	// ③ A-24 第 2 条：`opposing` 必须已按字典序规范化，未规范化一律拒写。
	s3, root3 := removeRelVault(t)
	pair := rules.Opposing("k-20260901-attention", "k-20260815-rnn")
	if pair.From != "k-20260815-rnn" {
		t.Fatalf("前置条件：字典序较小端应是 k-20260815-rnn，实得 %s", pair.From)
	}
	abs3 := filepath.Join(root3, cardPath("k-20260901-attention"))
	snap3 := mustBytes(t, abs3)
	_, err3 := s3.ApplyRemoveRelation(RemoveRelationSpec{
		Rel: cardPath("k-20260901-attention"), From: "k-20260901-attention",
		Type: model.RelationOpposing, Target: "k-20260815-rnn",
	})
	if !errors.Is(err3, ErrOpposingNotNormalized) {
		t.Fatalf("未规范化的 opposing 必须拒写（ErrOpposingNotNormalized），实得 %v", err3)
	}
	if !bytes.Equal(snap3, mustBytes(t, abs3)) {
		t.Fatal("拒写必须零字节变化")
	}
}
