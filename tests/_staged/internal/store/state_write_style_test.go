package store

// frontmatter 标量序列化风格的规范化断言（I-evergreen.system_assurance-158614-014）。
//
// 这组用例钉住四件事，缺一都会让 I-…-014 复发或让修法越界：
//   ① canonical 风格唯一：状态写路径写出的**字符串**标量一律单引号；
//   ② 布尔量不得被引号化（`stale: true` 保持裸写）—— 加引号会把 YAML 类型
//      从 bool 变 string，那是**语义变更**，越过了「只统一序列化风格」的边界；
//   ③ 幂等：同值重复写不产生 diff（风格不再抖动，这正是缺陷的实害所在）；
//   ④ 兼容性边界：不批量重写历史文件 —— 只有本次真正写入的那个键会归一，
//      同一份文件里其它键的历史风格（裸写 / 双引号）逐字保留，且仍可读。
//
// 为什么把风格断言集中在这里而不是散进各形态用例：散开写会让「语义断言」与
// 「字面量断言」纠缠，风格一变就有一批语义没变的用例集体转红（假红）。
// 各形态用例只断言语义，风格只在本文件钉一次。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// legacyStyleCard 刻意混用三种历史风格，用于验证「读得懂 + 不批量重写」。
//
//	status      裸写
//	reviewed_at 双引号
//	updated_at  单引号
const legacyStyleCard = `---
id: k-20260901-style
title: 风格兼容
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
reviewed_at: "2026-09-02T10:00:00+08:00"
vendor_unknown_key: 未知字段逐字保留
---

## 知识内容

正文不动。
`

func seedStyleCard(t *testing.T) (*Store, string) {
	t.Helper()
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k-20260901-style.md", legacyStyleCard)
	return s, abs
}

const styleRel = "cards/k-20260901-style.md"

// TestSetStatusWritesCanonicalSingleQuoted 断言 status 写出为单引号 canonical 形态。
//
// 缺陷原态是裸写 `status: deprecated`，与建卡路径的 `status: 'active'` 风格冲突 ——
// 同一个字段在不同命令下两种形态，是 I-…-014 最直接的现场。
func TestSetStatusWritesCanonicalSingleQuoted(t *testing.T) {
	s, abs := seedStyleCard(t)
	f, err := s.Read(styleRel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetStatus(styleRel, f.Hash, model.StatusDeprecated, model.Stamp{}); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	fm := fmOf(t, mustBytes(t, abs))
	if !strings.Contains(fm, "status: 'deprecated'\n") {
		t.Fatalf("status 未写成 canonical 单引号形态：\n%s", fm)
	}
	if strings.Contains(fm, "status: deprecated\n") {
		t.Fatalf("status 仍是裸写形态（I-…-014 未修）：\n%s", fm)
	}
}

// TestStaleBooleanStaysUnquoted 断言布尔量 stale 不被引号化（语义边界）。
//
// 这条是规范化的**否定边界**：若把 canonical 无差别套到所有值上，
// `stale: true` 会变成 `stale: 'true'`，YAML 类型由 bool 变 string —— 语义变更。
func TestStaleBooleanStaysUnquoted(t *testing.T) {
	s, abs := seedStyleCard(t)
	f, err := s.Read(styleRel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetStale(styleRel, f.Hash, model.StaleReasonDeprecated); err != nil {
		t.Fatalf("SetStale: %v", err)
	}
	fm := fmOf(t, mustBytes(t, abs))
	if !strings.Contains(fm, "stale: true\n") {
		t.Fatalf("布尔量 stale 必须保持裸写（加引号即语义变更）：\n%s", fm)
	}
	if strings.Contains(fm, "stale: 'true'") || strings.Contains(fm, `stale: "true"`) {
		t.Fatalf("布尔量被引号化，YAML 类型由 bool 变 string（语义变更）：\n%s", fm)
	}
	// 同一次写入里的**字符串**标量仍须 canonical。
	if !strings.Contains(fm, "stale_reason: '"+string(model.StaleReasonDeprecated)+"'\n") {
		t.Fatalf("stale_reason 未写成 canonical 单引号形态：\n%s", fm)
	}
}

// TestCanonicalWriteIsIdempotent 断言同值重复写零 diff（风格不再抖动）。
//
// I-…-014 的实害是「一次只改状态的命令顺带产出引号变更噪声」。
// 归一之后，第二次写同一个值必须产生**逐字节相同**的结果，否则噪声只是换了个形态。
func TestCanonicalWriteIsIdempotent(t *testing.T) {
	s, abs := seedStyleCard(t)
	f, err := s.Read(styleRel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetStatus(styleRel, f.Hash, model.StatusDeprecated, model.Stamp{}); err != nil {
		t.Fatalf("SetStatus#1: %v", err)
	}
	first := string(mustBytes(t, abs))

	f2, err := s.Read(styleRel)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if _, err := s.SetStatus(styleRel, f2.Hash, model.StatusDeprecated, model.Stamp{}); err != nil {
		t.Fatalf("SetStatus#2: %v", err)
	}
	if second := string(mustBytes(t, abs)); second != first {
		t.Fatalf("同值重复写产生了 diff（风格仍在抖动）：\n第一次：\n%s\n第二次：\n%s", first, second)
	}
}

// TestNoBulkRestyleOfUntouchedKeys 断言不批量重写历史文件（兼容性边界）。
//
// 写 status 时，同一份文件里 reviewed_at 的历史双引号形态、status 之外的裸写键、
// 未知字段都必须**逐字保留**。这条守住「append-only / 旧值兼容」：
// 归一是随键的下一次真实写入顺带发生，不是一次性 restyle 全库。
func TestNoBulkRestyleOfUntouchedKeys(t *testing.T) {
	s, abs := seedStyleCard(t)
	f, err := s.Read(styleRel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetStatus(styleRel, f.Hash, model.StatusDeprecated, model.Stamp{}); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	fm := fmOf(t, mustBytes(t, abs))
	for _, keep := range []string{
		// 历史双引号形态：本次未写该键，必须原样保留（不被顺带 restyle）。
		`reviewed_at: "2026-09-02T10:00:00+08:00"` + "\n",
		"updated_at: '2026-09-01T10:00:00+08:00'\n",
		"created_at: '2026-09-01'\n",
		"vendor_unknown_key: 未知字段逐字保留\n",
		"id: k-20260901-style\n",
	} {
		if !strings.Contains(fm, keep) {
			t.Fatalf("未写入的键被顺带改写（越过 append-only 边界），缺失 %q：\n%s", keep, fm)
		}
	}
}

// TestLegacyStylesRemainReadable 断言读侧不收紧：三种历史风格都能解析出同一语义值。
//
// 规范化只动写侧。若读侧被顺带收紧，历史文件会突然变得不可读 —— 那是比引号噪声
// 严重得多的回归，因此单独钉一条。
func TestLegacyStylesRemainReadable(t *testing.T) {
	s, _ := seedStyleCard(t)
	f, err := s.Read(styleRel)
	if err != nil {
		t.Fatalf("含裸写 + 单引号 + 双引号三种风格的历史文件必须可读：%v", err)
	}
	if f.Hash == "" {
		t.Fatal("读取成功但未给出 hash")
	}

	// 逐一确认三种风格的键都被解析到（而不是解析器悄悄跳过了它读不懂的那些行）。
	doc, card, err := mdfile.ParseCard([]byte(legacyStyleCard))
	if err != nil {
		t.Fatalf("解析历史风格文件失败：%v", err)
	}
	if got := string(card.Status); got != string(model.StatusActive) {
		t.Fatalf("裸写 status 应解析为 active，实得 %q", got)
	}
	keys, err := doc.FMKeys()
	if err != nil {
		t.Fatalf("FMKeys: %v", err)
	}
	for _, want := range []string{"status", "reviewed_at", "updated_at", "vendor_unknown_key"} {
		var found bool
		for _, k := range keys {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("键 %q 未被解析到（读侧疑似跳过了不认识的风格）：%v", want, keys)
		}
	}
}
