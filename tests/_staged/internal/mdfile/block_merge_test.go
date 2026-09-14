package mdfile

// 块级安全合并纯判定的单测（M6 A-57 / §7，`T-…-073`）。
// 只覆盖 DecideBlockMerge 的四判据与「块粒度、无行级 diff」两条本质，不碰落盘。

import (
	"strings"
	"testing"
)

// selfCheckDoc 是判定用固定语料：「理解自检」恰两块（历史块 + 当前有效块），另有非空「用户补充」。
const selfCheckDoc = `---
id: k-1
title: t
---

## 知识内容

x

## 用户补充

用户手写，别动。

## 理解自检

- 历史块：为什么需要缩放？

- 当前有效块：头数如何选？
`

// blockHashAt 取某分区第 idx 块的 block_hash（测试辅助，只读）。
func blockHashAt(t *testing.T, raw, section string, idx int) string {
	t.Helper()
	d, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	bs, err := d.Blocks(section)
	if err != nil {
		t.Fatalf("blocks: %v", err)
	}
	if idx < 0 || idx >= len(bs) {
		t.Fatalf("块序号越界：%d（共 %d 块）", idx, len(bs))
	}
	return BlockHash(bs[idx].Bytes(d.Raw))
}

// TestBlockMergeSafeCase：目标块自读取以来仍逐字未变 → 安全，且能定位到正确的目标块。
// 覆盖三态：①当前值 == 前像；②整文件在别处漂移（用户补充变了）但目标块未变；
// ③目标块之后又追加了新块（目标块不再是最后一块）——命中仍在，安全，序号落在命中块上。
func TestBlockMergeSafeCase(t *testing.T) {
	base := blockHashAt(t, selfCheckDoc, SecSelfCheck, 1) // 当前有效块（最后一块）

	// ① 当前值 == 前像：命中最后一块。
	dec := DecideBlockMerge([]byte(selfCheckDoc), SecSelfCheck, base)
	if !dec.Safe() {
		t.Fatalf("当前值==前像应安全：%+v", dec)
	}
	if dec.BlockIndex != 1 {
		t.Fatalf("应定位到当前有效块（序号 1），实得 %d", dec.BlockIndex)
	}

	// ② 整文件在别处漂移（用户补充改了）但「理解自检」目标块逐字未变 → 仍安全。
	drifted := strings.Replace(selfCheckDoc, "用户手写，别动。", "用户后来补了一大段自己的话。", 1)
	if drifted == selfCheckDoc {
		t.Fatal("语料构造失败：用户补充未被替换")
	}
	dec = DecideBlockMerge([]byte(drifted), SecSelfCheck, base)
	if !dec.Safe() || dec.BlockIndex != 1 {
		t.Fatalf("整文件他处漂移、目标块未变应安全并仍定位最后一块：%+v", dec)
	}

	// ③ 目标块之后追加了新块（并发自检追加），目标块不再是最后一块 → 命中仍在，安全。
	appended := selfCheckDoc + "\n- 更晚追加的当前有效块？\n"
	dec = DecideBlockMerge([]byte(appended), SecSelfCheck, base)
	if !dec.Safe() {
		t.Fatalf("目标块仍逐字存在（虽已非最后一块）应安全：%+v", dec)
	}
	if got := blockHashAt(t, appended, SecSelfCheck, dec.BlockIndex); got != base {
		t.Fatalf("安全时 BlockIndex 必须指向命中 base_block_hash 的块：期望 %s，实得序号 %d 的 %s",
			base, dec.BlockIndex, got)
	}
}

// TestBlockMergeBlockGranularityOnly：判定只在块粒度进行——
//   - 目标块内容按 NormalizeBlock 归一后一致（仅行尾空白 / 换行差异）仍命中 → 安全（无行级 diff，整块比对）；
//   - 目标块正文实质变化 → 不命中 → 不安全（整块判为已变，不做行内合并）；
//   - 另一块（历史块）变化不影响仍未变的目标块的可合并性。
func TestBlockMergeBlockGranularityOnly(t *testing.T) {
	if CodeBlockMergeConflict != "W27" {
		t.Fatalf("W27 诊断码常量必须为 W27，实得 %q", CodeBlockMergeConflict)
	}
	base := blockHashAt(t, selfCheckDoc, SecSelfCheck, 1)

	// 仅目标块行尾加空白：NormalizeBlock 归一后 hash 不变 → 命中、安全（证明是整块归一比对，非逐行 diff）。
	trailing := strings.Replace(selfCheckDoc,
		"- 当前有效块：头数如何选？\n", "- 当前有效块：头数如何选？   \n", 1)
	if trailing == selfCheckDoc {
		t.Fatal("语料构造失败：目标块行尾空白未注入")
	}
	dec := DecideBlockMerge([]byte(trailing), SecSelfCheck, base)
	if !dec.Safe() || dec.BlockIndex != 1 {
		t.Fatalf("仅行尾空白差异应命中并安全（块级归一比对）：%+v", dec)
	}

	// 目标块正文实质变化 → 整块 hash 变 → 不命中 → 不安全（不做行内 patch）。
	edited := strings.Replace(selfCheckDoc,
		"- 当前有效块：头数如何选？\n", "- 当前有效块：注意力头维度如何取？\n", 1)
	dec = DecideBlockMerge([]byte(edited), SecSelfCheck, base)
	if dec.Safe() {
		t.Fatalf("目标块正文实质变化必须判不安全（整块粒度，不做行级合并）：%+v", dec)
	}

	// 历史块变化、目标块未变：目标块仍命中 → 安全（差异落在别的块，与目标块区间不相交）。
	histChanged := strings.Replace(selfCheckDoc,
		"- 历史块：为什么需要缩放？\n", "- 历史块：为什么要做缩放点积？（后来改写）\n", 1)
	dec = DecideBlockMerge([]byte(histChanged), SecSelfCheck, base)
	if !dec.Safe() {
		t.Fatalf("目标块未变、仅他块变化应安全：%+v", dec)
	}
	if got := blockHashAt(t, histChanged, SecSelfCheck, dec.BlockIndex); got != base {
		t.Fatalf("安全序号必须指向仍命中的目标块：期望 %s，实得序号 %d 的 %s", base, dec.BlockIndex, got)
	}
}
