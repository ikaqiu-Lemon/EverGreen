package store

// 逻辑删除写口（SetDeleted / ClearDeleted）的单测。
//
// 每条断言对应一条合同硬约束，不是行为复述：
//   - §9 判定行：删除执行后**没有任何知识卡的状态被自动改变** → status 逐字未变；
//   - §5.2 ADR-11：知识卡 / 笔记 / 原文 / 综述**四类同构** → 字段名与渲染逐字一致；
//   - U-01 无物理删除：relations[] / sources[] / opposing / replaced_by 记录**一条不删**；
//   - B3：hash 不符即跳过且文件字节不变；B4：失败保留现状，不回滚。

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

const deletedStamp = "2026-09-03T10:00:00+08:00"

// 四类产物各一份最小样例：**同一套** SetDeleted 落地，样例差异只在 frontmatter 既有键，
// 用来反证本层不按产物类型分叉。
const (
	delCardSample = `---
id: k-20260901-del
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-spec
    note: n-20260901-spec
    rel: support
    reason: 合同 §9
vendor_unknown_key: 未知字段逐字保留
---

## 知识内容

卡正文一个字节都不许动。
`
	delNoteSample = `---
id: n-20260901-del
source: s-20260901-spec
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
vendor_unknown_key: 未知字段逐字保留
---

## 摘要

笔记正文一个字节都不许动。
`
	delSourceSample = `---
id: s-20260901-del
url: https://example.com/a
title: 原文标题
saved_at: '2026-09-01T10:00:00+08:00'
vendor_unknown_key: 未知字段逐字保留
---

## 原文

原文收录后不因任何加工改写。
`
	delReviewSample = `---
id: r-20260901-del
title: 主题综述
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
vendor_unknown_key: 未知字段逐字保留
---

## 综述

综述正文一个字节都不许动。
`
)

func mustStamp(t *testing.T) model.Stamp {
	t.Helper()
	st, err := model.ParseStamp(deletedStamp)
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	return st
}

// TestSetDeleted_AllFourKinds：四类产物（知识卡 / 笔记 / 原文 / 综述）**字段名与行为完全一致**。
func TestSetDeleted_AllFourKinds(t *testing.T) {
	cases := []struct {
		kind    string
		rel     string
		content string
	}{
		{"知识卡", "domains/ai/knowledge/k-20260901-del.md", delCardSample},
		{"笔记", "domains/ai/notes/n-20260901-del.md", delNoteSample},
		{"原文", "sources/s-20260901-del.md", delSourceSample},
		{"综述", "domains/ai/reviews/r-20260901-del.md", delReviewSample},
	}
	wantAt := `deleted_at: '` + deletedStamp + `'`
	wantReason := `deleted_reason: '用户要求删除'`
	for _, c := range cases {
		s, root := newVault(t)
		abs := writeSeed(t, root, c.rel, c.content)
		before := mustBytes(t, abs)
		f, err := s.Read(c.rel)
		if err != nil {
			t.Fatalf("%s read: %v", c.kind, err)
		}
		res, err := s.SetDeleted(c.rel, f.Hash, mustStamp(t), "用户要求删除", model.Stamp{})
		if err != nil {
			t.Fatalf("%s SetDeleted: %v", c.kind, err)
		}
		if !res.Written {
			t.Fatalf("%s：hash 相符时必须落盘", c.kind)
		}
		after := mustBytes(t, abs)
		fm := fmOf(t, after)
		// 四类共用**同名字段、同一渲染**：这就是「同构」的机械判据。
		if !strings.Contains(fm, wantAt+"\n") || !strings.Contains(fm, wantReason+"\n") {
			t.Fatalf("%s：两个删除标记字段必须逐字一致地写入，实得 frontmatter：%s", c.kind, fm)
		}
		if bodyOf(t, after) != bodyOf(t, before) {
			t.Fatalf("%s：正文必须逐字保留", c.kind)
		}
		if !strings.Contains(fm, "vendor_unknown_key: 未知字段逐字保留\n") {
			t.Fatalf("%s：未知 YAML 字段必须逐字保留", c.kind)
		}
		// 只新增两行，不重排、不新增第三个键。
		if got, want := len(strings.Split(fm, "\n")), len(strings.Split(fmOf(t, before), "\n"))+2; got != want {
			t.Fatalf("%s：只允许新增 deleted_at / deleted_reason 两行，行数 %d ≠ %d", c.kind, got, want)
		}
		// §9：删除**绝不**自动改状态——有 status 的产物 status 行逐字未变。
		if strings.Contains(fmOf(t, before), "status:") && !strings.Contains(fm, "status: active\n") {
			t.Fatalf("%s：删除维度绝不碰 status（F3 正交）：%s", c.kind, fm)
		}
	}
}

// relSample 带齐四种关系容器：删除后条目数必须逐条不变（U-01：记录一条不删）。
const relSample = `---
id: k-20260901-rel
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-a
    note: n-20260901-a
    rel: support
    reason: 支持材料一
  - source: s-20260901-b
    note: n-20260901-b
    rel: against
    reason: 反对材料
relations:
  - type: supports
    target: k-20260901-x
    reason: 论证一
  - type: refines
    target: k-20260901-y
    reason: 论证二
opposing:
  - k-20260901-z
  - k-20260901-w
replaced_by: {target: k-20260901-new, reason: "被新卡取代"}
---

## 知识内容

正文。
`

// TestLogicalDelete_RelationsPreserved：逻辑删除**不动任何关系记录**。
//
// 关系过滤靠**端点有效性**（查询层判定），落盘层一条都不删——因此四个容器的条目数
// 删除前后逐条相等，且原始字节逐字保留。
func TestLogicalDelete_RelationsPreserved(t *testing.T) {
	s, root := newVault(t)
	rel := "domains/ai/knowledge/k-20260901-rel.md"
	abs := writeSeed(t, root, rel, relSample)
	before := mustBytes(t, abs)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetDeleted(rel, f.Hash, mustStamp(t), "端点删除测试", model.Stamp{}); err != nil {
		t.Fatalf("SetDeleted: %v", err)
	}
	after := mustBytes(t, abs)
	counts := map[string]int{
		"sources 条目":   strings.Count(string(before), "  - source: "),
		"relations 条目": strings.Count(string(before), "  - type: "),
		"opposing 条目":  strings.Count(string(before), "  - k-2026"),
		"replaced_by":  strings.Count(string(before), "replaced_by: {"),
	}
	got := map[string]int{
		"sources 条目":   strings.Count(string(after), "  - source: "),
		"relations 条目": strings.Count(string(after), "  - type: "),
		"opposing 条目":  strings.Count(string(after), "  - k-2026"),
		"replaced_by":  strings.Count(string(after), "replaced_by: {"),
	}
	for k, want := range counts {
		if want == 0 {
			t.Fatalf("样例本身必须含 %s，否则测试空转", k)
		}
		if got[k] != want {
			t.Fatalf("%s 条目数必须逐条不变（U-01 无物理删除）：%d → %d", k, want, got[k])
		}
	}
	// 逐字保留：整段关系文本原封不动地仍在文件里。
	for _, block := range []string{
		"sources:\n  - source: s-20260901-a\n    note: n-20260901-a\n    rel: support\n    reason: 支持材料一\n",
		"relations:\n  - type: supports\n    target: k-20260901-x\n    reason: 论证一\n",
		"opposing:\n  - k-20260901-z\n  - k-20260901-w\n",
		"replaced_by: {target: k-20260901-new, reason: \"被新卡取代\"}\n",
	} {
		if !strings.Contains(string(after), block) {
			t.Fatalf("关系记录必须逐字保留，缺失：%q", block)
		}
	}
	if !strings.Contains(fmOf(t, after), "status: active\n") {
		t.Fatal("删除不得改变 status（§9）")
	}
}

// TestSetDeletedHashMismatchSkipsWithoutWrite：B3——hash 不符即跳过，文件字节不变（上层据此退 3）。
func TestSetDeletedHashMismatchSkipsWithoutWrite(t *testing.T) {
	s, root := newVault(t)
	rel := "domains/ai/knowledge/k-20260901-del.md"
	abs := writeSeed(t, root, rel, delCardSample)
	before := mustBytes(t, abs)
	// 时刻给真值：B3 跳过时**连时间戳也不许动**（零写入的负半边，I-…-009）。
	res, err := s.SetDeleted(rel,
		"sha256:1111111111111111111111111111111111111111111111111111111111111111",
		mustStamp(t), "用户要求删除", mustStamp(t))
	skip, ok := AsSkip(err)
	if !ok || skip.Reason != SkipFileChanged {
		t.Fatalf("hash 不符必须跳过（B3），实得 %v", err)
	}
	if res.Written {
		t.Fatal("跳过时不得落盘")
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("跳过时文件字节必须不变（B4：保留现状，不回滚）")
	}
	// 必带参数缺失 → 立刻失败，零写入。
	if _, err := s.SetDeleted(rel, "", model.Stamp{}, "用户要求删除", model.Stamp{}); err == nil {
		t.Fatal("deleted_at 必带")
	}
	if _, err := s.SetDeleted(rel, "", mustStamp(t), "", model.Stamp{}); err == nil {
		t.Fatal("deleted_reason 必带")
	}
	if !bytes.Equal(mustBytes(t, abs), before) {
		t.Fatal("参数不合法时零写入")
	}
}

// TestClearDeletedKeepsStatusUntouched：清空删除标记（undelete 用）同样**不碰 status**，
// 且清空后回到与从未被删逐字一致的形态（不留墓碑）。
func TestClearDeletedKeepsStatusUntouched(t *testing.T) {
	s, root := newVault(t)
	rel := "domains/ai/knowledge/k-20260901-del.md"
	abs := writeSeed(t, root, rel, delCardSample)
	origin := mustBytes(t, abs)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetDeleted(rel, f.Hash, mustStamp(t), "先删后恢复", model.Stamp{}); err != nil {
		t.Fatalf("SetDeleted: %v", err)
	}
	f2, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	res, err := s.ClearDeleted(rel, f2.Hash, model.Stamp{})
	if err != nil {
		t.Fatalf("ClearDeleted: %v", err)
	}
	if !res.Written {
		t.Fatal("清空必须落盘")
	}
	after := mustBytes(t, abs)
	if !bytes.Equal(after, origin) {
		t.Fatalf("清空后必须逐字回到原形态（不留墓碑）：\n%s", after)
	}
	if !strings.Contains(fmOf(t, after), "status: active\n") {
		t.Fatal("清空删除标记不得改变 status")
	}
	// 本来没有删除标记 → fail fast，不静默成功。
	f3, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.ClearDeleted(rel, f3.Hash, model.Stamp{}); err == nil {
		t.Fatal("未被删除的目标必须报错（无静默兜底）")
	}
	if !bytes.Equal(mustBytes(t, abs), origin) {
		t.Fatal("报错时零写入")
	}
}

// TestApplyStateWriteDeletedBothDirections：唯一对外入口能同时表达「删」与「清空」，
// 两个方向共用同一条守卫路径（写口唯一不被绕开）。
func TestApplyStateWriteDeletedBothDirections(t *testing.T) {
	s, root := newVault(t)
	rel := "domains/ai/knowledge/k-20260901-del.md"
	abs := writeSeed(t, root, rel, delCardSample)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.ApplyStateWrite(StateWriteSpec{Op: StateWriteDeleted, Rel: rel,
		ExpectedHash: f.Hash, At: mustStamp(t), Reason: "入口删"}); err != nil {
		t.Fatalf("ApplyStateWrite 删: %v", err)
	}
	if !strings.Contains(string(mustBytes(t, abs)), `deleted_reason: '入口删'`) {
		t.Fatal("入口必须真的落盘删除标记")
	}
	f2, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.ApplyStateWrite(StateWriteSpec{Op: StateWriteDeleted, Rel: rel,
		ExpectedHash: f2.Hash, Clear: true}); err != nil {
		t.Fatalf("ApplyStateWrite 清空: %v", err)
	}
	if strings.Contains(fmOf(t, mustBytes(t, abs)), "deleted_") {
		t.Fatal("清空后不得残留删除标记键")
	}
}
