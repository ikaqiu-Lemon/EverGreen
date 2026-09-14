package store

// T-…-045 的 store 层机器判据：`eg edit` 的分区级替换落盘形态
// （授权合同 §5「授权不是豁免」的 B2 / B3 两条 + §9 A-13 + 矩阵 #12 / #15）。
//
// 两条用例的重心都在「**写入侧根本不认识授权**」这个结构性事实上：
// ApplyReplaceSection 的签名里没有、也不会有任何「路径」或「豁免」参数——
// 谁有权发起替换由 internal/plan 的写权限矩阵判定，落盘一律走同一个 mutateGuarded。

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// editCardSample 是一张**同时**带「用户补充」与「存疑与待验证」的卡：
// 前者是知识卡固定五分区之一（永不可写，U-03），后者作为用户自建第六分区出现
// （F5：第六个及以后的 H2 一律原样保留）。两段都要在编辑「知识内容」后逐字节不变。
const editCardSample = `---
id: k-20260901-edit-target
title: 待用户显式修改的卡
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
reviewed_at: '2026-09-05T09:00:00+08:00'
sources:
  - source: s-20260901-spec
    note: n-20260901-spec
    rel: support
    reason: 技术方案 §16.4
tags:
  - s1
---

## 知识内容

旧的结论一行。

第二段旧正文。

## 解释与依据

旧依据。

## 条件与边界

仅 S1。

## 用户补充

	这里是用户手写的缩进块。

- 用户自己的列表
    - 未知子结构

## 理解自检

- [ ] 能说出固定次序

## 存疑与待验证

- 用户手记的疑点一条
`

// sectionBody 取某分区的正文字节（半开区间 Body..End，含尾随空行）。
func sectionBody(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	doc, err := mdfile.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	span, ok := doc.Section(name)
	if !ok {
		t.Fatalf("分区「%s」不存在：\n%s", name, raw)
	}
	return raw[span.Body:span.End]
}

// TestEdit_HashCheckedBeforeWrite 断言 B3 **不因授权豁免**：文件在读盘后被外部改动
// → 跳过该文件（Reason = file_changed，一一对应报告 `skipped[].kind`）、零写入、
// 磁盘字节逐字不变（外部改动原样保留，不做任何还原：U-02 无破坏性回滚）。
func TestEdit_HashCheckedBeforeWrite(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", editCardSample)

	f, err := s.Read("cards/k.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	changed := editCardSample + "\n用户在 CLI 之外手改的一行\n"
	if err := os.WriteFile(abs, []byte(changed), 0o644); err != nil {
		t.Fatalf("external write: %v", err)
	}

	res, err := s.ApplyReplaceSection(ReplaceSectionSpec{
		Rel: "cards/k.md", ExpectedHash: f.Hash, ID: "k-20260901-edit-target",
		Section: mdfile.SecKnowledge, Content: []byte("用户显式改写的新结论。\n"),
	})
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("凭据过期必须跳过（B3 对用户显式路径同样成立），实得 err=%v", err)
	}
	if skip.Reason != SkipFileChanged || res.Reason != SkipFileChanged {
		t.Fatalf("跳过原因必须是 %q，实得 %q / %q", SkipFileChanged, skip.Reason, res.Reason)
	}
	if CauseFor(skip.Reason) == "" {
		t.Fatalf("跳过必须有可进报告的 cause，%q 没有", skip.Reason)
	}
	if res.Written {
		t.Fatal("跳过时不得写入")
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, []byte(changed)) {
		t.Fatal("被跳过文件的字节必须逐字不变")
	}
	assertNoTmp(t, root+"/cards")

	// 凭据刷新后同一次替换照常成立：跳过是**凭据过期**，不是拒绝这个动作。
	f2, err := s.Read("cards/k.md")
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	res2, err := s.ApplyReplaceSection(ReplaceSectionSpec{
		Rel: "cards/k.md", ExpectedHash: f2.Hash, ID: "k-20260901-edit-target",
		Section: mdfile.SecKnowledge, Content: []byte("用户显式改写的新结论。\n"),
	})
	if err != nil || !res2.Written {
		t.Fatalf("凭据刷新后必须写成，实得 written=%v err=%v", res2.Written, err)
	}
}

// TestEdit_PreservesUserSections 断言 B2：编辑「知识内容」后，「用户补充」与
// 「存疑与待验证」两段**逐字节相同**；同时反证三个正交维度一格未动
// （status / deleted_at / reviewed_at）与「新内容逐字生效」。
func TestEdit_PreservesUserSections(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", editCardSample)
	before := mustBytes(t, abs)

	f, err := s.Read("cards/k.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	payload := []byte("用户显式改写的新结论。\n\n带空行的第二段。\n")
	res, err := s.ApplyReplaceSection(ReplaceSectionSpec{
		Rel: "cards/k.md", ExpectedHash: f.Hash, ID: "k-20260901-edit-target",
		Section: mdfile.SecKnowledge, Content: payload,
	})
	if err != nil || !res.Written {
		t.Fatalf("用户显式替换必须写成，实得 written=%v err=%v", res.Written, err)
	}
	after := mustBytes(t, abs)

	// ① B2：两段用户所有的分区逐字节相同（含内部缩进、列表与尾随空行）。
	for _, name := range []string{mdfile.SecUserAppend, mdfile.SecOpenQuest} {
		want, got := sectionBody(t, before, name), sectionBody(t, after, name)
		if !bytes.Equal(want, got) {
			t.Fatalf("分区「%s」必须逐字节相同：\n期望 %q\n实得 %q", name, want, got)
		}
	}
	// ② 新内容逐字生效：载荷原样出现，旧正文两段一并消失（整段替换而非追加）。
	if !bytes.Contains(after, payload) {
		t.Fatalf("新正文必须逐字生效：\n%s", after)
	}
	for _, gone := range []string{"旧的结论一行。", "第二段旧正文。"} {
		if bytes.Contains(after, []byte(gone)) {
			t.Fatalf("整段替换后旧正文 %q 不应残留：\n%s", gone, after)
		}
	}
	// ③ 其它分区与 frontmatter 逐字不动：三个正交维度一格未写。
	for _, keep := range []string{"旧依据。", "仅 S1。", "- [ ] 能说出固定次序",
		"status: active", "reviewed_at: '2026-09-05T09:00:00+08:00'",
		"updated_at: '2026-09-01T10:00:00+08:00'", "reason: 技术方案 §16.4"} {
		if !bytes.Contains(after, []byte(keep)) {
			t.Fatalf("%q 必须逐字保留：\n%s", keep, after)
		}
	}
	if strings.Contains(string(after), "deleted_at") {
		t.Fatalf("编辑正文不得引入删除维度：\n%s", after)
	}
	// ④ 分区名与顺序不变（替换正文不得增删或重排分区）。
	beforeDoc, _ := mdfile.Parse(before)
	afterDoc, _ := mdfile.Parse(after)
	if strings.Join(beforeDoc.SectionNames(), "|") != strings.Join(afterDoc.SectionNames(), "|") {
		t.Fatalf("分区序列不得变化：%v → %v", beforeDoc.SectionNames(), afterDoc.SectionNames())
	}
	assertNoTmp(t, root+"/cards")
}

// TestEdit_UserSectionRejectedAtStore 是 U-03 在**写入侧**的兜底反证：
// 即便有人绕过 plan 的门闸直接调 store，「用户补充」依然写不进去（fail fast，不静默跳过）。
func TestEdit_UserSectionRejectedAtStore(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "cards/k.md", editCardSample)
	f, err := s.Read("cards/k.md")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	res, err := s.ApplyReplaceSection(ReplaceSectionSpec{
		Rel: "cards/k.md", ExpectedHash: f.Hash, ID: "k-20260901-edit-target",
		Section: mdfile.SecUserAppend, Content: []byte("任何路径都不许写这里。\n"),
	})
	if err == nil {
		t.Fatal("「用户补充」必须被拒（U-03：CLI 任何路径任何时候都不写它）")
	}
	if res.Written {
		t.Fatal("被拒时不得写入")
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, []byte(editCardSample)) {
		t.Fatal("被拒时字节必须逐字不变")
	}
	assertNoTmp(t, root+"/cards")
}
