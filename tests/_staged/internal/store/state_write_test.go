package store

// M3 状态维度写口（state_write.go）的单测：单键覆盖、B3 hash 守卫、单向存储、删除骨架。
//
// 每条断言都对应一条合同硬约束，而不是对代码行为的复述：
//   - status 与删除维度正交（F3）→ 写 status 不得碰 deleted_at / deleted_reason；
//   - frontmatter 不许长出历史键 → 反复覆盖单键，变更原因由 Git 历史承载；
//   - B3 → hash 不符即跳过且**字节不变**；
//   - 单向存储 → 目标卡文件字节不变。

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

const stateCardSample = `---
id: k-20260901-state
title: 状态维度写口
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-spec
    note: n-20260901-spec
    rel: support
    reason: 合同 §7.1
tags:
  - m3
vendor_unknown_key: 未知字段逐字保留
---

## 知识内容

正文一个字节都不许动。

## 解释与依据

依据段落。

## 理解自检

- 问题一

## 存疑与待验证

- 待验证项

## 用户补充

用户自己的话。
`

func seedStateCard(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k-20260901-state.md", stateCardSample)
	return s, root, abs
}

// fmOf 取 frontmatter 文本（含首尾分隔行之间的内容），用于逐键断言。
func fmOf(t *testing.T, raw []byte) string {
	t.Helper()
	parts := bytes.SplitN(raw, []byte("---\n"), 3)
	if len(parts) < 3 {
		t.Fatal("样例必须有 frontmatter")
	}
	return string(parts[1])
}

// TestSetStatusOnlyTouchesStatusKey：只改 status 单键，删除维度、正文、未知字段全不动。
func TestSetStatusOnlyTouchesStatusKey(t *testing.T) {
	s, _, abs := seedStateCard(t)
	before := mustBytes(t, abs)
	f, err := s.Read("cards/k-20260901-state.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 时刻传零值：本用例锁的是「**只有** status 那一行变」，因此不要求时间戳刷新
	// （刷新本身另有专门用例 TestStateWriteRefreshesUpdatedAt）。
	res, err := s.SetStatus("cards/k-20260901-state.md", f.Hash, model.StatusDeprecated, model.Stamp{})
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if !res.Written {
		t.Fatal("hash 相符时必须落盘")
	}
	after := mustBytes(t, abs)
	fm := fmOf(t, after)
	if !strings.Contains(fm, "status: 'deprecated'\n") {
		t.Fatalf("status 未被覆盖：%s", fm)
	}
	if strings.Contains(fm, "status: 'active'") || strings.Contains(fm, "status: active") {
		t.Fatalf("旧值残留：%s", fm)
	}
	for _, forbidden := range []string{"deleted_at", "deleted_reason"} {
		if strings.Contains(fm, forbidden) {
			t.Fatalf("状态与删除是正交两维（F3）：SetStatus 不得引入 %s：%s", forbidden, fm)
		}
	}
	if !strings.Contains(fm, "vendor_unknown_key: 未知字段逐字保留\n") {
		t.Fatalf("未知 YAML 字段必须逐字保留：%s", fm)
	}
	// 正文（frontmatter 结束分隔行之后）逐字不变。
	if got, want := bodyOf(t, after), bodyOf(t, before); got != want {
		t.Fatalf("正文必须逐字不变：\n得到 %q\n期望 %q", got, want)
	}
	// 除 status 那一行外，frontmatter 逐行不变，且不得新增任何键。
	assertOnlyLineChanged(t, before, after, "status:")
}

// TestSetStatusRepeatedOverwritesSingleKey：反复调用只覆盖单键，绝不累积历史数组。
func TestSetStatusRepeatedOverwritesSingleKey(t *testing.T) {
	s, _, abs := seedStateCard(t)
	rel := "cards/k-20260901-state.md"
	for _, want := range []model.Status{model.StatusDeprecated, model.StatusActive, model.StatusDeprecated} {
		f, err := s.Read(rel)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if _, err := s.SetStatus(rel, f.Hash, want, model.Stamp{}); err != nil {
			t.Fatalf("SetStatus %s: %v", want, err)
		}
	}
	fm := fmOf(t, mustBytes(t, abs))
	if n := strings.Count(fm, "status:"); n != 1 {
		t.Fatalf("status 键必须恰一个（单键覆盖，不累积）：实得 %d：%s", n, fm)
	}
	for _, forbidden := range []string{"status_history", "history", "changes", "previous_status"} {
		if strings.Contains(fm, forbidden) {
			t.Fatalf("frontmatter 不得新增历史键 %s（变更原因由 Git 历史查阅）：%s", forbidden, fm)
		}
	}
	if !strings.Contains(fm, "status: 'deprecated'\n") {
		t.Fatalf("末次取值须生效：%s", fm)
	}
}

// TestSetStatusHashMismatchSkipsWithoutWrite：B3——hash 不符即跳过，文件字节不变。
func TestSetStatusHashMismatchSkipsWithoutWrite(t *testing.T) {
	s, root, abs := seedStateCard(t)
	before := mustBytes(t, abs)
	res, err := s.SetStatus("cards/k-20260901-state.md",
		"sha256:0000000000000000000000000000000000000000000000000000000000000000",
		model.StatusDeprecated, mustStamp(t))
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("hash 不符必须返回可识别的跳过结果（上层据此退 3），实得 %v", err)
	}
	if skip.Reason != SkipFileChanged {
		t.Fatalf("跳过原因须是 %s，实得 %s", SkipFileChanged, skip.Reason)
	}
	if res.Written {
		t.Fatal("跳过时不得落盘")
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("跳过时文件字节必须一个都不变")
	}
	assertNoTmp(t, filepath.Join(root, "cards"))
}

// TestSetReplacedByLeavesTargetCardUntouched：单向存储——被指向的目标卡文件字节不变。
func TestSetReplacedByLeavesTargetCardUntouched(t *testing.T) {
	s, root, abs := seedStateCard(t)
	targetRel := "cards/k-20260902-target.md"
	targetAbs := writeSeed(t, root, targetRel,
		strings.Replace(stateCardSample, "k-20260901-state", "k-20260902-target", 1))
	targetBefore := mustBytes(t, targetAbs)
	before := mustBytes(t, abs)

	f, err := s.Read("cards/k-20260901-state.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 时刻传零值：本用例锁的是「只在失效卡上写一个 replaced_by 键、目标卡零字节改动」，
	// 时间戳刷新另有专门用例。
	res, err := s.SetReplacedBy("cards/k-20260901-state.md", f.Hash,
		model.RelationEndpoint("k-20260902-target"), "口径已更新，见新卡", model.Stamp{})
	if err != nil {
		t.Fatalf("SetReplacedBy: %v", err)
	}
	if !res.Written {
		t.Fatal("hash 相符时必须落盘")
	}
	after := mustBytes(t, abs)
	fm := fmOf(t, after)
	if !strings.Contains(fm,
		"replaced_by: {target: k-20260902-target, reason: \"口径已更新，见新卡\"}\n") {
		t.Fatalf("replaced_by 须写成 {target, reason} 形态：%s", fm)
	}
	var card model.Card
	if err := decodeFMInto(after, &card); err != nil {
		t.Fatalf("写后 frontmatter 必须仍是合法 YAML 且能读回结构体：%v", err)
	}
	if card.ReplacedBy == nil || card.ReplacedBy.Target != "k-20260902-target" ||
		card.ReplacedBy.Reason != "口径已更新，见新卡" {
		t.Fatalf("replaced_by 须读回两个子字段：%+v", card.ReplacedBy)
	}
	if !bytes.Equal(mustBytes(t, targetAbs), targetBefore) {
		t.Fatal("单向存储：被指向的目标卡文件一个字节都不许动")
	}
	if got, want := bodyOf(t, after), bodyOf(t, before); got != want {
		t.Fatal("正文必须逐字不变")
	}
	if strings.Contains(fm, "deleted_at") || strings.Contains(fm, "deleted_reason") {
		t.Fatalf("SetReplacedBy 不得碰删除维度：%s", fm)
	}
}

// TestSetReplacedByRequiresBothFields：缺 target 或 reason → 拒写（不写半个指针）。
func TestSetReplacedByRequiresBothFields(t *testing.T) {
	s, _, abs := seedStateCard(t)
	before := mustBytes(t, abs)
	f, err := s.Read("cards/k-20260901-state.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetReplacedBy("cards/k-20260901-state.md", f.Hash, "k-20260902-target", "", model.Stamp{}); err == nil {
		t.Fatal("缺 reason 必须报错")
	}
	if _, err := s.SetReplacedBy("cards/k-20260901-state.md", f.Hash, "", "理由", model.Stamp{}); err == nil {
		t.Fatal("缺 target 必须报错")
	}
	if _, err := s.SetReplacedBy("cards/k-20260901-state.md", f.Hash, "s-20260902-src", "理由", model.Stamp{}); err == nil {
		t.Fatal("target 只接受知识卡 ID（E3）")
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("参数不合法时零写入")
	}
}

// bodyOf 取 frontmatter 结束分隔行之后的正文字节（字符串形式便于比较）。
func bodyOf(t *testing.T, raw []byte) string {
	t.Helper()
	parts := bytes.SplitN(raw, []byte("---\n"), 3)
	if len(parts) < 3 {
		t.Fatal("样例必须有 frontmatter")
	}
	return string(parts[2])
}

// assertOnlyLineChanged 断言 frontmatter 只有以 keyPrefix 开头的那一行发生变化，
// 行数不增不减（= 没有新增任何键）。
func assertOnlyLineChanged(t *testing.T, before, after []byte, keyPrefix string) {
	t.Helper()
	b := strings.Split(fmOf(t, before), "\n")
	a := strings.Split(fmOf(t, after), "\n")
	if len(a) != len(b) {
		t.Fatalf("frontmatter 行数必须不变（不新增键）：%d → %d", len(b), len(a))
	}
	for i := range b {
		if b[i] == a[i] {
			continue
		}
		if !strings.HasPrefix(b[i], keyPrefix) {
			t.Fatalf("第 %d 行不该变：%q → %q", i+1, b[i], a[i])
		}
	}
}

// decodeFMInto 只读解码 frontmatter（读侧允许 YAML 解析；写侧禁止序列化）。
func decodeFMInto(raw []byte, out interface{}) error {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return err
	}
	return doc.DecodeFM(out)
}
