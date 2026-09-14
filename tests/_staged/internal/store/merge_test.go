package store

// 块级安全合并写口接线的集成单测（M6 A-57 / §7，`T-…-073`）。
// 经真实 Store.ApplyReplaceBlock 落盘，覆盖：M3 匹配路径逐字一致、不安全跳过 + W27、用户字节零丢失。

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// mergeCard 是接线用固定语料：「理解自检」恰两块（历史块 + 当前有效块），另有含缩进 / 列表 / 未知子结构的「用户补充」。
const mergeCard = `---
id: k-20260901-merge
title: 块级安全合并
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-spec
    note: n-20260901-spec
    rel: support
    reason: 合同 §7
tags:
  - m6
---

## 知识内容

要点。

## 解释与依据

依据。

## 条件与边界

边界。

## 用户补充

	这里是用户手写的缩进块。

- 用户自己的列表
    - 未知子结构

## 理解自检

- 历史块：为什么需要缩放？

- 当前有效块：头数如何选？
`

const mergeRel = "domains/x/knowledge/k-20260901-merge.md"

func mergeSpec(base string, block string) ReplaceBlockSpec {
	return ReplaceBlockSpec{
		Rel:           mergeRel,
		ID:            model.CardID("k-20260901-merge"),
		Stamp:         model.Stamp{},
		Section:       mdfile.SecSelfCheck,
		BaseBlockHash: base,
		Block:         []byte(block),
	}
}

// TestBlockMergeM3BehaviourPreservedWhenHashMatches：base_block_hash 命中当前有效块（最后一块）
// 且整文件未变时，落盘字节与 M3 口径**逐字相等**——只替换最后一块，历史块与用户补充逐字不动。
func TestBlockMergeM3BehaviourPreservedWhenHashMatches(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, mergeRel, mergeCard)

	base, err := CurrentSelfCheckBlockHash([]byte(mergeCard))
	if err != nil {
		t.Fatalf("取当前有效块 hash：%v", err)
	}
	res, err := s.ApplyReplaceBlock(mergeSpec(base, "- 当前有效块：换新问题？\n"))
	if err != nil {
		t.Fatalf("命中路径应写入成功：%v（%+v）", err, res)
	}
	if !res.Written {
		t.Fatalf("命中路径必须落盘：%+v", res)
	}

	want := strings.Replace(mergeCard,
		"- 当前有效块：头数如何选？\n", "- 当前有效块：换新问题？\n", 1)
	got := mustBytes(t, abs)
	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("命中路径落盘字节必须与 M3 逐字相等（只换最后一块）：\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if !bytes.Contains(got, []byte("- 历史块：为什么需要缩放？")) {
		t.Fatal("历史记录块必须逐字保留（只追加、永不改写）")
	}
	assertNoTmp(t, filepath.Dir(abs))
}

// TestBlockMergeUnsafeSkipsWithW27：base_block_hash 无任何块命中（目标块自读取以来已变）→
// 不安全，跳过并留痕 W27；kind 仍恰 file_changed / content_hash_mismatch；目标文件字节零变化。
func TestBlockMergeUnsafeSkipsWithW27(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, mergeRel, mergeCard)
	before := mustBytes(t, abs)

	res, err := s.ApplyReplaceBlock(mergeSpec("sha256:deadbeef", "- 冲突块\n"))
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("不安全合并必须返回 *SkipError，得到 %v", err)
	}
	if skip.Reason != SkipFileChanged || res.Reason != SkipFileChanged {
		t.Fatalf("kind 必须复用封闭两值 file_changed（不新造第三值）：skip=%q res=%q", skip.Reason, res.Reason)
	}
	if CauseFor(res.Reason) != "content_hash_mismatch" {
		t.Fatalf("cause 必须是 content_hash_mismatch，实得 %q", CauseFor(res.Reason))
	}
	if res.Written {
		t.Fatal("不安全合并必须零写入")
	}
	// W27 + block_merge_conflict 留痕在自由文本 Detail 里；kind 不因此扩张。
	for _, want := range []string{
		mdfile.CodeBlockMergeConflict, mdfile.BlockMergeConflictReason,
		"sha256:deadbeef", "k-20260901-merge#" + mdfile.SecSelfCheck + "#",
	} {
		if !strings.Contains(res.Detail, want) {
			t.Fatalf("Detail 必须含 %q：%q", want, res.Detail)
		}
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatal("不安全合并时目标文件字节必须逐字不变")
	}
	assertNoTmp(t, filepath.Dir(abs))
}

// TestBlockMergeNeverLosesUserBytes：整文件在别处漂移（用户补充被并发编辑）但目标块逐字未变时，
// 安全合并照常落盘，且既有「用户补充」字节**逐字零丢失**（合同 §7 用户字节零丢失 / B2）。
func TestBlockMergeNeverLosesUserBytes(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, mergeRel, mergeCard)

	// base_block_hash 取自原始语料的当前有效块（最后一块）。
	base, err := CurrentSelfCheckBlockHash([]byte(mergeCard))
	if err != nil {
		t.Fatalf("取当前有效块 hash：%v", err)
	}

	// 模拟并发：用户在读取之后又改写了「用户补充」（整文件 hash 漂移，但「理解自检」目标块未变）。
	drifted := strings.Replace(mergeCard,
		"\t这里是用户手写的缩进块。\n\n- 用户自己的列表\n    - 未知子结构\n",
		"\t用户后来重写了这段缩进块。\n\n- 用户新列表项\n    - 新的未知子结构\n", 1)
	if drifted == mergeCard {
		t.Fatal("语料构造失败：用户补充未被改写")
	}
	writeSeed(t, root, mergeRel, drifted)
	userBefore, err := UserSectionBytes([]byte(drifted))
	if err != nil {
		t.Fatalf("user sections: %v", err)
	}

	// 整文件 hash 已漂移，但目标块 base_block_hash 仍命中 → A-57 安全合并，照常落盘。
	res, err := s.ApplyReplaceBlock(mergeSpec(base, "- 当前有效块：换新问题？\n"))
	if err != nil {
		t.Fatalf("整文件他处漂移、目标块未变应安全落盘：%v（%+v）", err, res)
	}
	if !res.Written {
		t.Fatalf("安全合并必须落盘：%+v", res)
	}

	after := mustBytes(t, abs)
	if !bytes.Contains(after, []byte("- 当前有效块：换新问题？")) {
		t.Fatal("新块未写入")
	}
	if bytes.Contains(after, []byte("- 当前有效块：头数如何选？")) {
		t.Fatal("旧的当前有效块应已被替换")
	}
	if !bytes.Contains(after, []byte("- 历史块：为什么需要缩放？")) {
		t.Fatal("历史记录块必须逐字保留")
	}
	// 关键：并发写入的用户字节逐字零丢失。
	userAfter, err := UserSectionBytes(after)
	if err != nil {
		t.Fatalf("user sections after: %v", err)
	}
	if !bytes.Equal(userBefore[mdfile.SecUserAppend], userAfter[mdfile.SecUserAppend]) {
		t.Fatalf("用户补充必须逐字零丢失：\n--- before ---\n%s\n--- after ---\n%s",
			userBefore[mdfile.SecUserAppend], userAfter[mdfile.SecUserAppend])
	}
	assertNoTmp(t, filepath.Dir(abs))
}
