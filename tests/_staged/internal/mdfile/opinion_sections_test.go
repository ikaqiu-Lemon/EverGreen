package mdfile_test

// 观点分区模板（Schema v2 契约 §3.2）与「新增实体不得改动既有两类模板」的加性边界。
//
// 本文件锁三件事：
//  1. Opinion 固定五分区的**名称逐字**与**顺序**，以及它的必需分区与自动可写集合；
//  2. 新增 Opinion **没有**改动 Knowledge / Note 的固定分区口径——分区模板的切换
//     必须与它的执行者（授权矩阵、plan 校验、writers）同一提交进行，本次是纯加性；
//  3. 「用户补充」在任何实体上都不可自动写入（安全底线 B2）。
//
// 顺序颠倒与必需分区缺失两条错误用例是防线：任何「为了让新实体跑通而放宽
// ValidateSections」的改动都会在这里翻车。

import (
	"errors"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOpinionSectionsFiveInOrder(t *testing.T) {
	want := []string{"观点", "论据与推理", "条件与反例", "待验证", "用户补充"}
	got := mdfile.KnownSections(mdfile.KindOpinion)
	if !eqStrings(got, want) {
		t.Fatalf("Opinion 固定分区应为 %v，实际 %v", want, got)
	}
	if got := mdfile.OpinionSections(); !eqStrings(got, want) {
		t.Fatalf("OpinionSections() 应与 KnownSections(KindOpinion) 一致，实际 %v", got)
	}
	if mdfile.RequiredSection(mdfile.KindOpinion) != "观点" {
		t.Fatalf("Opinion 必需分区应为「观点」，实际 %q",
			mdfile.RequiredSection(mdfile.KindOpinion))
	}
	// 论据 / 反例 / 待验证 三段是论证的落点，允许自动追加；
	// 「观点」沿用「知识内容」口径（创建时写定，不由自动路径改写），「用户补充」永不写。
	wantAuto := []string{"论据与推理", "条件与反例", "待验证"}
	if got := mdfile.AutoWritableSections(mdfile.KindOpinion); !eqStrings(got, wantAuto) {
		t.Fatalf("Opinion 自动可写分区应为 %v，实际 %v", wantAuto, got)
	}
	// 观点不是知识卡的子类型：两者的 Kind 值必须不同，否则按 Kind 取分区会串档。
	if mdfile.KindOpinion == mdfile.KindCard {
		t.Fatal("KindOpinion 不得与 KindCard 相等")
	}
}

func TestOpinionAdditionKeepsCardAndNoteTemplatesIntact(t *testing.T) {
	// 加性边界：本次只新增实体，Knowledge / Note 的固定分区、必需分区、
	// 自动可写集合逐字不变。三张表在写口切换任务里一起改，不在这里偷偷改一半。
	wantCard := []string{"知识内容", "解释与依据", "条件与边界", "用户补充", "理解自检"}
	if got := mdfile.KnownSections(mdfile.KindCard); !eqStrings(got, wantCard) {
		t.Fatalf("Knowledge 固定分区本次不应改动，期望 %v，实际 %v", wantCard, got)
	}
	wantNote := []string{"材料提炼", "Agent 分析", "用户补充", "存疑与待验证", "产出知识卡"}
	if got := mdfile.KnownSections(mdfile.KindNote); !eqStrings(got, wantNote) {
		t.Fatalf("Note 固定分区本次不应改动，期望 %v，实际 %v", wantNote, got)
	}
	if got := mdfile.RequiredSection(mdfile.KindCard); got != "知识内容" {
		t.Fatalf("Knowledge 必需分区应仍为「知识内容」，实际 %q", got)
	}
	if got := mdfile.RequiredSection(mdfile.KindNote); got != "材料提炼" {
		t.Fatalf("Note 必需分区应仍为「材料提炼」，实际 %q", got)
	}
	wantCardAuto := []string{"解释与依据", "条件与边界", "理解自检"}
	if got := mdfile.AutoWritableSections(mdfile.KindCard); !eqStrings(got, wantCardAuto) {
		t.Fatalf("Knowledge 自动可写分区本次不应改动，期望 %v，实际 %v", wantCardAuto, got)
	}
	// 观点独有的四个分区名不得渗入知识卡 / 笔记的固定分区。
	for _, kind := range []mdfile.Kind{mdfile.KindCard, mdfile.KindNote} {
		for _, sec := range mdfile.KnownSections(kind) {
			switch sec {
			case "观点", "论据与推理", "条件与反例", "待验证":
				t.Fatalf("%s 的固定分区不应出现观点专属分区 %q", kind, sec)
			}
		}
	}
	// 未知类型仍返回空：新增一个 Kind 不得让 default 分支意外命中某张模板。
	if got := mdfile.KnownSections(mdfile.Kind("proposal")); got != nil {
		t.Fatalf("未知类型应返回 nil，实际 %v", got)
	}
	if got := mdfile.RequiredSection(mdfile.KindSource); got != "" {
		t.Fatalf("原文没有固定分区，RequiredSection 应为空串，实际 %q", got)
	}
}

func TestNeverWriteSectionsIsUserAppendForAllKinds(t *testing.T) {
	want := []string{"用户补充"}
	if got := mdfile.NeverWriteSections(); !eqStrings(got, want) {
		t.Fatalf("NeverWriteSections() 应恒为 %v，实际 %v", want, got)
	}
	// 四类实体的自动可写集合都不得包含「用户补充」（安全底线 B2）。
	for _, k := range []mdfile.Kind{
		mdfile.KindCard, mdfile.KindNote, mdfile.KindOpinion, mdfile.KindSource,
	} {
		for _, s := range mdfile.AutoWritableSections(k) {
			if s == "用户补充" {
				t.Fatalf("%s 的自动可写分区不得包含「用户补充」", k)
			}
		}
	}
	// 「用户补充」必须真的在 Opinion 的固定分区里——不在模板里就谈不上「永不写入」。
	found := false
	for _, s := range mdfile.OpinionSections() {
		if s == "用户补充" {
			found = true
		}
	}
	if !found {
		t.Fatal("Opinion 固定分区必须以「用户补充」收尾")
	}
}

// sampleOpinion 是一份完整的观点正文：五分区齐全，另有一个用户自建分区。
const sampleOpinion = `---
id: o-20260915-harness-boundary
status: active
created_at: 2026-09-15
updated_at: 2026-09-15T10:00:00Z
validation: pending
sources:
  - source: s-20260915-agent-arch
    note: n-20260915-agent-arch
    rel: support
    reason: 原文第 3 节讨论了扩展成本
---

## 观点

Harness 的核心边界决定了 Agent 的扩展成本。

## 论据与推理

原文第 3 节给出的五类 Harness 能力中，工具注册与上下文管理各自独立演进。

## 条件与反例

在只有单一工具的场景下，边界划分带来的收益接近于零。

## 待验证

需要一组跨项目的迁移成本数据才能定论。

## 用户补充

我在自己的项目里观察到相反的现象。

## 我的阅读笔记

这里是用户自建分区。
`

func TestOpinionDocParsesWithSectionsAddressableAndBytesVerbatim(t *testing.T) {
	raw := []byte(sampleOpinion)
	doc, err := mdfile.Parse(raw)
	if err != nil {
		t.Fatalf("观点正文应可解析，得到 err=%v", err)
	}
	if err := doc.ValidateSections(mdfile.KindOpinion); err != nil {
		t.Fatalf("五分区齐全的观点不应报结构错误，得到：%v", err)
	}
	// 用户自建分区落入 UnknownSections：原样保留、只记 info，不报错。
	unknown := doc.UnknownSections(mdfile.KindOpinion)
	if len(unknown) != 1 || unknown[0].Name != "我的阅读笔记" {
		names := make([]string, 0, len(unknown))
		for _, s := range unknown {
			names = append(names, s.Name)
		}
		t.Fatalf("应恰有一个用户自建分区「我的阅读笔记」，实际 %v", names)
	}
	// 四个观点分区都能按名取到内容，读路径不丢数据。
	for _, sec := range []string{"观点", "论据与推理", "条件与反例", "待验证"} {
		blocks, err := doc.Blocks(sec)
		if err != nil {
			t.Fatalf("按名读取 %q 应成功，得到 err=%v", sec, err)
		}
		if len(blocks) == 0 {
			t.Fatalf("%q 的内容不得读成空", sec)
		}
	}
	// 字节保真：解析不改动原文一个字节。
	if string(doc.Raw) != sampleOpinion {
		t.Fatal("解析不得改动原始字节")
	}
}

func TestOpinionSectionOrderViolationReported(t *testing.T) {
	// 顺序颠倒仍必须报错——放宽 ValidateSections 会让这条测试翻车。
	raw := []byte(`---
id: o-20260915-x
---

## 论据与推理

先给论据。

## 观点

再给观点。
`)
	doc, err := mdfile.Parse(raw)
	if err != nil {
		t.Fatalf("解析应成功：%v", err)
	}
	err = doc.ValidateSections(mdfile.KindOpinion)
	if err == nil {
		t.Fatal("Opinion 固定分区顺序颠倒必须报错")
	}
	if !strings.Contains(err.Error(), "顺序颠倒") {
		t.Fatalf("错误信息应指明顺序颠倒，实际：%v", err)
	}
	// 错误必须带类型与位置，否则用户无法定位是哪份文件的哪一行。
	var se *mdfile.SectionError
	if !errors.As(err, &se) {
		t.Fatalf("应返回 *mdfile.SectionError，实际 %T", err)
	}
	if se.Kind != mdfile.KindOpinion {
		t.Fatalf("SectionError.Kind 应为 opinion，实际 %q", se.Kind)
	}
	if se.Line <= 0 {
		t.Fatalf("SectionError 应带 1 起行号，实际 %d", se.Line)
	}
}

func TestOpinionMissingRequiredSectionReported(t *testing.T) {
	raw := []byte(`---
id: o-20260915-x
---

## 论据与推理

只有论据，没有观点。
`)
	doc, err := mdfile.Parse(raw)
	if err != nil {
		t.Fatalf("解析应成功：%v", err)
	}
	err = doc.ValidateSections(mdfile.KindOpinion)
	if err == nil {
		t.Fatal("缺少必需分区「观点」必须报错")
	}
	if !strings.Contains(err.Error(), "观点") {
		t.Fatalf("错误信息应指明缺失的「观点」，实际：%v", err)
	}
	// 错误信息要把五个固定分区都列出来，用户才知道正确模板长什么样。
	for _, sec := range mdfile.OpinionSections() {
		if !strings.Contains(err.Error(), sec) {
			t.Fatalf("错误信息应列出固定分区 %q，实际：%v", sec, err)
		}
	}
}
