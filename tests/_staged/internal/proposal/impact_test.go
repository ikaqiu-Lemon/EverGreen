package proposal

// 影响面重算的机器判据（T-…-035；提案合同 §5.4 四项 + §9 的「重算确定性 /
// stale_reviews 只给提示」两行）。
//
// 用例名逐字采用提案合同 §9 与 task Acceptance 的约定形态：
//   - TestRecomputeImpact_Deterministic  同一语料连续两次重算的四项结果**逐字相同**
//   - TestImpact_StaleReviewsHintOnly    只按 source_cards 命中产出，不做失准自动判定
//   - TestRecomputeImpact_FourItems      四项各自的口径逐项钉死（含计数）
//
// 夹具是**真实 vault**（t.TempDir() + 真实 Markdown），重算全程只经 store 的只读口，
// 不建任何派生缓存：S2 的「直接扫描 Markdown 与 frontmatter」是这条链路的唯一实现。

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 夹具里的固定 ID（同一份语料被 impact / superseded 两组用例共用）。
const (
	fxCardTarget = "k-20260901-attention" // 被删目标卡
	fxCardKeep   = "k-20260815-rnn"       // 唯一有效 support 落在被删笔记上的卡
	fxNote       = "n-20260901-bench"     // 被删材料笔记
	fxSource     = "s-20260901-bench"     // 笔记对应的原文
	fxReview     = "r-20260920-attention" // source_cards 命中被删卡的主题综述
	fxDomain     = "ai-infra"
)

// fxTargets 是夹具提案的 targets：一张卡 + 一篇材料笔记。
func fxTargets() []string { return []string{fxCardTarget, fxNote} }

// wantFixtureImpact 是夹具语料下四项的**期望值**（逐项写死，避免用产品实现自证）。
func wantFixtureImpact() Impact {
	return Impact{
		ExitsDefaultView:     []string{fxCardTarget, fxNote},
		CardsLosingSupport:   []string{fxCardKeep},
		AffectedMaterialRels: 2,
		AffectedRelations:    2,
		StaleReviews:         []string{fxReview},
	}
}

// writeFile 在 vault 内落一份文件（测试夹具专用；产品路径的写入一律经 guarded store）。
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("写夹具 %s 失败：%v", rel, err)
	}
}

// cardFixture 拼一张知识卡（五分区 + sources[] + relations[]）。
func cardFixture(id, status string, sources, relations string) string {
	return fmt.Sprintf(`---
id: %s
status: %s
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
%s%s---

## 知识内容

正文占位。

## 解释与依据

依据占位。

## 条件与边界

边界占位。

## 用户补充

## 理解自检

- 自检问题占位？
`, id, status, sources, relations)
}

// sourcesBlock 拼一条四要素材料关系。
func sourcesBlock(source, note, rel, reason string) string {
	return fmt.Sprintf(`sources:
  - source: %s
    note: %s
    rel: %s
    reason: %s
`, source, note, rel, reason)
}

// relationsBlock 拼一条论证关系。
func relationsBlock(relType, target, reason string) string {
	return fmt.Sprintf(`relations:
  - type: %s
    target: %s
    reason: %s
`, relType, target, reason)
}

// newFixtureVault 落一份完整语料并返回 store 与 vault 根路径。
//
// 语料关系图（targets = 卡 fxCardTarget + 笔记 fxNote）：
//
//	fxCardTarget --limits--> fxCardKeep      （宿主命中 targets → 计入 affected_relations）
//	fxCardKeep   --supports--> fxCardTarget  （另一端命中 targets → 计入 affected_relations）
//	fxCardTarget 的 sources[]：context 材料关系，端点 = 被删笔记
//	fxCardKeep   的 sources[]：**唯一** support 材料关系，端点 = 被删笔记
//	                            → 删除后没有剩余有效 support → 进 cards_losing_support
//	fxReview     的 source_cards 含 fxCardTarget → 进 stale_reviews（**只提示**）
func newFixtureVault(t *testing.T) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "sources/"+fxSource+".md", fmt.Sprintf(`---
id: %s
url: https://example.com/bench
title: 基准测试原文
saved_at: '2026-09-01T09:00:00+08:00'
---

原文正文占位。
`, fxSource))
	writeFile(t, root, "domains/"+fxDomain+"/notes/"+fxNote+".md", fmt.Sprintf(`---
id: %s
source: %s
created_at: '2026-09-01'
updated_at: '2026-09-01T09:30:00+08:00'
---

## 材料提炼

提炼占位。

## Agent 分析

分析占位。

## 用户补充

## 存疑与待验证

## 产出知识卡

- %s
`, fxNote, fxSource, fxCardKeep))
	writeFile(t, root, "domains/"+fxDomain+"/knowledge/"+fxCardTarget+".md",
		cardFixture(fxCardTarget, "active",
			sourcesBlock(fxSource, fxNote, "context", "背景材料"),
			relationsBlock("limits", fxCardKeep, "限定旧结论的适用范围")))
	writeFile(t, root, "domains/"+fxDomain+"/knowledge/"+fxCardKeep+".md",
		cardFixture(fxCardKeep, "active",
			sourcesBlock(fxSource, fxNote, "support", "该笔记是唯一支持材料"),
			relationsBlock("supports", fxCardTarget, "支持新结论")))
	writeFile(t, root, "domains/"+fxDomain+"/reviews/"+fxReview+".md", fmt.Sprintf(`---
id: %s
created_at: '2026-09-20'
generated_at: '2026-09-20T10:00:00+08:00'
source_cards:
  - %s
---

## 综述正文

综述占位。
`, fxReview, fxCardTarget))
	return store.New(root), root
}

// —— ① 四项口径 ——

// TestRecomputeImpact_FourItems 钉死 §5.4 四项各自的口径（列表逐项 + 计数数值）。
func TestRecomputeImpact_FourItems(t *testing.T) {
	s, _ := newFixtureVault(t)
	got, err := RecomputeImpact(s, fxTargets())
	if err != nil {
		t.Fatalf("RecomputeImpact：%v", err)
	}
	want := wantFixtureImpact()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("影响面四项 = %+v，期望 %+v", got, want)
	}
	if diff := ImpactDiff(want, got); len(diff) != 0 {
		t.Fatalf("ImpactDiff 必须为空，实得 %v", diff)
	}
}

// TestRecomputeImpact_Deterministic：同一语料连续两次重算的四项结果**逐字相同**。
func TestRecomputeImpact_Deterministic(t *testing.T) {
	s, _ := newFixtureVault(t)
	first, err := RecomputeImpact(s, fxTargets())
	if err != nil {
		t.Fatalf("第一次重算：%v", err)
	}
	second, err := RecomputeImpact(s, fxTargets())
	if err != nil {
		t.Fatalf("第二次重算：%v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("两次重算不一致：%+v vs %+v", first, second)
	}
	if !ImpactEqual(first, second) {
		t.Fatalf("ImpactEqual 判两次重算不等：%v", ImpactDiff(first, second))
	}
	// 逐字相同的更强反证：把两次结果各渲染成一份提案的 impact 块，字节必须逐字相等。
	renderOnce := func(im Impact) []byte {
		raw, err := RenderTemplate(Template{
			ID: "p-20261017-002", Title: "重算确定性反证",
			CreatedAt: "2026-10-17", Targets: fxTargets(), Impact: im,
		})
		if err != nil {
			t.Fatalf("RenderTemplate：%v", err)
		}
		return raw
	}
	if a, b := renderOnce(first), renderOnce(second); string(a) != string(b) {
		t.Fatalf("两次重算渲染出的字节不逐字相同")
	}
	// targets 顺序颠倒 / 含重复项也不改变结论（归一口径与顺序无关）。
	shuffled, err := RecomputeImpact(s, []string{fxNote, fxCardTarget, fxNote})
	if err != nil {
		t.Fatalf("乱序重算：%v", err)
	}
	if !reflect.DeepEqual(first, shuffled) {
		t.Fatalf("targets 乱序 / 重复改变了结论：%+v vs %+v", first, shuffled)
	}
}

// TestImpact_StaleReviewsHintOnly：stale_reviews **只按 source_cards 命中**产出提示，
// 不做「综述是否真的失准」的自动判定（那属 S3 之后）。
func TestImpact_StaleReviewsHintOnly(t *testing.T) {
	s, root := newFixtureVault(t)
	got, err := RecomputeImpact(s, fxTargets())
	if err != nil {
		t.Fatalf("RecomputeImpact：%v", err)
	}
	if len(got.StaleReviews) != 1 || got.StaleReviews[0] != fxReview {
		t.Fatalf("stale_reviews = %v，期望恰 [%s]", got.StaleReviews, fxReview)
	}
	// ① 命中判据只有 source_cards：把该综述的 source_cards 换成另一张卡后，命中即消失，
	//    综述正文一字不动 —— 证明产出与正文内容（是否真的失准）无关。
	writeFile(t, root, "domains/"+fxDomain+"/reviews/"+fxReview+".md", fmt.Sprintf(`---
id: %s
created_at: '2026-09-20'
generated_at: '2026-09-20T10:00:00+08:00'
source_cards:
  - %s
---

## 综述正文

综述占位。
`, fxReview, fxCardKeep))
	after, err := RecomputeImpact(s, fxTargets())
	if err != nil {
		t.Fatalf("改 source_cards 后重算：%v", err)
	}
	if len(after.StaleReviews) != 0 {
		t.Fatalf("source_cards 不再命中时 stale_reviews 必须为空，实得 %v", after.StaleReviews)
	}
	// ② 判据键名逐字是 source_cards，且本包只读这一个键。
	if KeySourceCards != "source_cards" {
		t.Fatalf("stale_reviews 的命中键名 = %q，必须逐字是 source_cards", KeySourceCards)
	}
}

// TestRecomputeImpact_SkipsProposalsAndDeleted 钉两条边界：
//
//	① 提案目录下的文件**不进**知识扫描面（提案不是知识产物）；
//	② 已被逻辑删除的对象不再进 exits_default_view（它已经不在默认视图里）。
func TestRecomputeImpact_SkipsProposalsAndDeleted(t *testing.T) {
	s, root := newFixtureVault(t)
	// 落一份提案：它的 targets 与语料重叠，但重算绝不把它自己当成知识产物。
	raw, err := RenderTemplate(Template{
		ID: "p-20261017-001", Title: "逻辑删除一张卡与其材料",
		CreatedAt: "2026-10-17", Targets: fxTargets(), Impact: wantFixtureImpact(),
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	writeFile(t, root, Rel("p-20261017-001"), string(raw))
	got, err := RecomputeImpact(s, fxTargets())
	if err != nil {
		t.Fatalf("RecomputeImpact：%v", err)
	}
	if !reflect.DeepEqual(got, wantFixtureImpact()) {
		t.Fatalf("提案入库后影响面变了：%+v", got)
	}
	// 把被删笔记标成已逻辑删除：它退出 exits_default_view，且不再让 fxCardKeep 失去 support
	// （该 support 早已无效）。
	writeFile(t, root, "domains/"+fxDomain+"/notes/"+fxNote+".md", fmt.Sprintf(`---
id: %s
source: %s
created_at: '2026-09-01'
updated_at: '2026-09-01T09:30:00+08:00'
deleted_at: '2026-10-16T09:00:00+08:00'
deleted_reason: 材料已过期
---

## 材料提炼

提炼占位。

## Agent 分析

分析占位。

## 用户补充

## 存疑与待验证

## 产出知识卡

- %s
`, fxNote, fxSource, fxCardKeep))
	after, err := RecomputeImpact(s, fxTargets())
	if err != nil {
		t.Fatalf("标记删除后重算：%v", err)
	}
	if len(after.ExitsDefaultView) != 1 || after.ExitsDefaultView[0] != fxCardTarget {
		t.Fatalf("exits_default_view = %v，期望恰 [%s]", after.ExitsDefaultView, fxCardTarget)
	}
	if len(after.CardsLosingSupport) != 0 {
		t.Fatalf("已无效的 support 不得再让卡「失去 support」，实得 %v", after.CardsLosingSupport)
	}
	if ImpactEqual(wantFixtureImpact(), after) {
		t.Fatalf("语料变化后 ImpactEqual 必须判不等")
	}
}
