package model

// `reviewed_at` 可选键的读写往返判据（T-evergreen.s1_main_flow-158614-042）。
//
// 核心判据只有一条、也是最容易被做丢的一条：**不回填默认值**。
// 原本无 `reviewed_at` 的产物，读一遍再看一遍，既不能长出这个键，也不能被代入一个具体时刻
// （缺省等价于「从未过目」，判定按 `reviewed_at = -∞`，判定本身在 query/filter 单文件里）。

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// cardFMWithoutReviewedAt 是一份**不含** reviewed_at 的知识卡 frontmatter（历史文件形态）。
const cardFMWithoutReviewedAt = `id: k-20260901-attention
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-attention
    note: n-20260901-attention
    rel: support
    reason: 该结论由这份材料支持
tags:
  - attention
`

func TestReviewedAt_OptionalNoBackfill(t *testing.T) {
	// —— ① 缺该键：读出即「缺省」，且不长出任何默认时刻 ——
	var card Card
	if err := yaml.Unmarshal([]byte(cardFMWithoutReviewedAt), &card); err != nil {
		t.Fatalf("解析无 %s 的 frontmatter 失败：%v", FMKeyReviewedAt, err)
	}
	if card.ReviewedAt != nil {
		t.Fatalf("原本无 %s 的文件被读成了 %v：可选键缺省必须保持 nil（不回填默认值）",
			FMKeyReviewedAt, *card.ReviewedAt)
	}
	if at, ok := CardReviewedAt(card); ok {
		t.Fatalf("CardReviewedAt 报告有值 %s：键缺省时 ok 必须为 false", at.String())
	}
	if got := ReviewedAtText(card.ReviewedAt); got != "" {
		t.Fatalf("ReviewedAtText = %q，键缺省时必须是空串（不代入任何时刻）", got)
	}
	// 读出的其余维度一格不受影响（三个维度正交）。
	if card.Status != StatusActive {
		t.Fatalf("status = %q，期望 active：读 %s 不得连带改动状态维度", card.Status, FMKeyReviewedAt)
	}
	if card.DeletedAt != nil || card.DeletedReason != "" {
		t.Fatal("删除维度被读出了值：无 deleted_at / deleted_reason 的文件不得长出删除标记")
	}

	// —— ② 往返：把同一份字节再解析一遍，仍然不含该键 ——
	// 读写往返在本仓的写路径上是「字节区间拼接」（禁用 YAML 序列化回写），因此这里的往返
	// 判据就是：只读解析 → 原字节里依然没有那个键，读操作没有任何副作用。
	var again Card
	if err := yaml.Unmarshal([]byte(cardFMWithoutReviewedAt), &again); err != nil {
		t.Fatalf("二次解析失败：%v", err)
	}
	if again.ReviewedAt != nil {
		t.Fatalf("二次读出 %s 有值：读操作绝不回填", FMKeyReviewedAt)
	}
	if strings.Contains(cardFMWithoutReviewedAt, FMKeyReviewedAt) {
		t.Fatalf("语料自身含 %s，本用例失去判据", FMKeyReviewedAt)
	}

	// —— ③ 有该键：原样读出，值逐字保留 ——
	const want = "2026-09-02T09:00:00+08:00"
	var marked Card
	raw := cardFMWithoutReviewedAt + FMKeyReviewedAt + ": '" + want + "'\n"
	if err := yaml.Unmarshal([]byte(raw), &marked); err != nil {
		t.Fatalf("解析含 %s 的 frontmatter 失败：%v", FMKeyReviewedAt, err)
	}
	at, ok := CardReviewedAt(marked)
	if !ok {
		t.Fatalf("键在却报告缺省：%s 必须被读出", FMKeyReviewedAt)
	}
	if at.String() != want {
		t.Fatalf("%s = %q，期望逐字 %q", FMKeyReviewedAt, at.String(), want)
	}
	if got := ReviewedAtText(marked.ReviewedAt); got != want {
		t.Fatalf("ReviewedAtText = %q，期望 %q", got, want)
	}

	// —— ④ 材料笔记同构：同一套字段名与语义，不按产物类型分叉 ——
	var note Note
	noteFM := "id: n-20260901-attention\nsource: s-20260901-attention\n" +
		"created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
	if err := yaml.Unmarshal([]byte(noteFM), &note); err != nil {
		t.Fatalf("解析笔记 frontmatter 失败：%v", err)
	}
	if _, ok := NoteReviewedAt(note); ok {
		t.Fatalf("笔记无 %s 却被读成有值：四类产物同构，缺省口径一致", FMKeyReviewedAt)
	}
	if _, ok := NoteReviewedAt(Note{ReviewedAt: &Stamp{}}); ok {
		t.Fatal("零值时刻被当成「已过目」：零值与缺省同样按缺省处理，本层不猜也不补值")
	}
}
