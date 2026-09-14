package query

// 切词口径的 fuzz 属性（system_assurance · T-…-005 · `fuzz` profile 第 3–4 个 target）。
//
// 为什么切词值得 fuzz：`tokens` 是**全包唯一一套切词口径**（context 候选与 search 匹配分
// 共用，合同 §1.3）。它同时处理三类输入：ASCII 连续段、全角 ASCII、非 ASCII 二元组。
// 单测只能钉住若干具名样例；一旦有人改动折半角或二元组边界，受影响面是"召回率"这种
// 不会让编译失败、也不会让具名单测变红的东西。这里钉的是**性质**，不是样例：
//
//   FuzzTokensInvariants   任意字节输入下：不 panic、无空词、ASCII 词恒为小写、
//                          词数不超过 rune 数（切词不得凭空造词）、同输入两次结果相同。
//   FuzzTokensWidthFold    全角 ASCII 与其半角写法必须切出**同一个**词集合
//                          （§1.3 第 1 条归一口径；这条一旦破，全角查询直接零召回）。
//
// 只读、纯内存、零落盘：fuzz 语料不涉及任何 vault 与 .index/。

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func fuzzTokenSeeds() []string {
	return []string{
		"",
		"a",
		"Evergreen 索引",
		"ＦＴＳ５ 全角",
		"retrieval-augmented generation",
		"k-20261201-abc",
		"索引",
		"   ",
		"AAA___bbb999",
		"混合 mixed 语料 corpus",
		"\xff\xfe 非法字节",
		strings.Repeat("长", 64),
	}
}

// FuzzTokensInvariants：切词的五条结构性质，任意输入都必须成立。
func FuzzTokensInvariants(f *testing.F) {
	for _, s := range fuzzTokenSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := tokens(s)

		// ① 无空词：空串进了词集合会让任何查询都"命中"。
		if got[""] {
			t.Fatalf("切出空词：输入 %q", s)
		}
		for w := range got {
			// ② ASCII 词恒小写（大小写不敏感是合同承诺，靠归一而非比较时兜底）。
			if isASCIIWord(w) && w != strings.ToLower(w) {
				t.Fatalf("ASCII 词未折小写：%q（输入 %q）", w, s)
			}
			// ③ 词长有界：ASCII 段任意长，非 ASCII 恒为 1–2 个 rune 的二元组。
			if !isASCIIWord(w) && utf8.RuneCountInString(w) > 2 {
				t.Fatalf("非 ASCII 词超过二元组：%q（%d runes，输入 %q）",
					w, utf8.RuneCountInString(w), s)
			}
		}
		// ④ 不凭空造词：词数不可能超过 rune 数（每个 rune 最多贡献一个二元组起点）。
		if n := utf8.RuneCountInString(s); len(got) > n && n >= 0 {
			t.Fatalf("词数 %d > rune 数 %d：切词在造词（输入 %q）", len(got), n, s)
		}
		// ⑤ 确定性：只读打分必须可复算，同输入两次逐键相同。
		again := tokens(s)
		if len(again) != len(got) {
			t.Fatalf("两次切词词数不同：%d vs %d（输入 %q）", len(got), len(again), s)
		}
		for w := range got {
			if !again[w] {
				t.Fatalf("两次切词结果不同：第二次缺词 %q（输入 %q）", w, s)
			}
		}
	})
}

// FuzzTokensWidthFold：全角 ASCII 与半角写法命中同一个词集合（§1.3 归一）。
func FuzzTokensWidthFold(f *testing.F) {
	for _, s := range fuzzTokenSeeds() {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// 把输入里的半角 ASCII 可见字符逐个折成全角，词集合必须不变。
		var b strings.Builder
		for _, r := range s {
			if r >= 0x21 && r <= 0x7E {
				b.WriteRune(r + 0xFEE0)
				continue
			}
			b.WriteRune(r)
		}
		half, full := tokens(s), tokens(b.String())
		if len(half) != len(full) {
			t.Fatalf("全角/半角词数不同：%d vs %d（输入 %q → %q）",
				len(half), len(full), s, b.String())
		}
		for w := range half {
			if !full[w] {
				t.Fatalf("全角写法丢词 %q（输入 %q → %q）", w, s, b.String())
			}
		}
	})
}

func isASCIIWord(w string) bool {
	for i := 0; i < len(w); i++ {
		if w[i] >= 0x80 {
			return false
		}
	}
	return true
}
