package report

// support_check[] 的单测：三条反直觉规则 + 零写盘 / 零状态变更 + 逐字串常量。
//
// 断言对应合同 §9 的判定行，不复述实现：
//   - 失去有效 support → 只给「建议标记 `deprecated`」，且该卡**仍是 active**；
//   - 仍有有效 support → 给「建议重新检查材料关系」；
//   - 构造过程零写盘（磁盘字节不变）、零 status 变更（输入投影逐字未变）；
//   - `recommendation` 是对外键名（jq 直读），逐字串常量一字不差。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubVault 是只读投影：故意没有任何写方法，因此本模块结构上拿不到改状态的能力。
type stubVault struct {
	cards []SupportCard
	err   error
	calls int
}

func (v *stubVault) SupportCards() ([]SupportCard, error) {
	v.calls++
	return v.cards, v.err
}

func sampleVault() *stubVault {
	return &stubVault{cards: []SupportCard{
		{ID: "k-lost", Status: "active", Supports: []SupportRef{
			{Source: "s-del", Note: "n-del"},
		}},
		{ID: "k-partial", Status: "active", Supports: []SupportRef{
			{Source: "s-del", Note: "n-del"},
			{Source: "s-keep", Note: "n-keep"},
		}},
		{ID: "k-untouched", Status: "active", Supports: []SupportRef{
			{Source: "s-keep", Note: "n-keep"},
		}},
		{ID: "k-del", Status: "active", Supports: []SupportRef{
			{Source: "s-del", Note: "n-del"},
		}},
	}}
}

func entryOf(t *testing.T, list []SupportCheckEntry, id string) SupportCheckEntry {
	t.Helper()
	for _, e := range list {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("清单里必须有 %s：%+v", id, list)
	return SupportCheckEntry{}
}

// TestBuildSupportCheckRecommendations：两条建议各命中各自的卡，且状态如实回报 active。
func TestBuildSupportCheckRecommendations(t *testing.T) {
	v := sampleVault()
	got, err := BuildSupportCheck([]string{"s-del", "n-del", "k-del"}, v)
	if err != nil {
		t.Fatalf("BuildSupportCheck: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("只有受影响且未被删的卡进清单（k-lost / k-partial），实得 %+v", got)
	}
	lost := entryOf(t, got, "k-lost")
	if lost.Recommendation != RecommendDeprecateMark {
		t.Fatalf("失去有效 support 的卡必须命中「%s」，实得 %q",
			RecommendDeprecateMark, lost.Recommendation)
	}
	if lost.RemainingSupport != 0 || len(lost.LostSupport) != 1 {
		t.Fatalf("支持面事实必须如实：%+v", lost)
	}
	// §9：只建议，不自动改状态——它仍是 active。
	if lost.Status != "active" {
		t.Fatalf("用户不处理时卡仍是 active（系统绝不自动改状态），实得 %q", lost.Status)
	}
	partial := entryOf(t, got, "k-partial")
	if partial.Recommendation != RecommendRecheckMaterials {
		t.Fatalf("仍有有效 support 的卡必须命中「%s」，实得 %q",
			RecommendRecheckMaterials, partial.Recommendation)
	}
	if partial.RemainingSupport != 1 || partial.Status != "active" {
		t.Fatalf("仍有有效 support 且状态未变：%+v", partial)
	}
}

// TestSupportCheckLiteralStringsAreExact：逐字串常量一字不差（CLI 与验收都逐字比对）。
func TestSupportCheckLiteralStringsAreExact(t *testing.T) {
	if NoAutoStatusChangeNotice != "本次删除没有自动改变任何知识卡的状态" {
		t.Fatalf("逐字串被改动：%q", NoAutoStatusChangeNotice)
	}
	if RecommendDeprecateMark != "建议标记 `deprecated`" {
		t.Fatalf("建议文案被改动：%q", RecommendDeprecateMark)
	}
	if RecommendRecheckMaterials != "建议重新检查材料关系" {
		t.Fatalf("建议文案被改动：%q", RecommendRecheckMaterials)
	}
}

// TestSupportCheckJSONKeyIsRecommendation：对外键名必须是 `recommendation`
// （验收用 jq -e '.data.support_check[]?.recommendation' 直读）。
func TestSupportCheckJSONKeyIsRecommendation(t *testing.T) {
	entries, err := BuildSupportCheck([]string{"s-del"}, sampleVault())
	if err != nil {
		t.Fatalf("BuildSupportCheck: %v", err)
	}
	r := New()
	r.SetSupportCheck(entries)
	raw, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var envelope struct {
		SupportCheck []struct {
			ID             string `json:"id"`
			Recommendation string `json:"recommendation"`
		} `json:"support_check"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(envelope.SupportCheck) == 0 {
		t.Fatalf("support_check[] 必须真的出现在报告体里：%s", raw)
	}
	hit := false
	for _, e := range envelope.SupportCheck {
		if e.Recommendation == RecommendDeprecateMark {
			hit = true
		}
		if e.Recommendation == "" {
			t.Fatalf("每条都必须带 recommendation：%s", raw)
		}
	}
	if !hit {
		t.Fatalf("必须能读到「%s」：%s", RecommendDeprecateMark, raw)
	}
	if !strings.Contains(string(raw), `"recommendation"`) {
		t.Fatalf("JSON 键名必须逐字是 recommendation：%s", raw)
	}
}

// TestBuildSupportCheckWritesNothingAndChangesNoStatus：构造过程零写盘、零 status 变更。
func TestBuildSupportCheckWritesNothingAndChangesNoStatus(t *testing.T) {
	dir := t.TempDir()
	card := filepath.Join(dir, "k-lost.md")
	const seed = "---\nid: k-lost\nstatus: active\n---\n\n## 知识内容\n\n正文。\n"
	if err := os.WriteFile(card, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	v := sampleVault()
	before := make([]SupportCard, len(v.cards))
	copy(before, v.cards)

	if _, err := BuildSupportCheck([]string{"s-del", "n-del"}, v); err != nil {
		t.Fatalf("BuildSupportCheck: %v", err)
	}

	// 零写盘：目录里既没有新文件，原文件字节也没变。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "k-lost.md" {
		t.Fatalf("构造建议清单不得写盘，目录内容变了：%+v", entries)
	}
	after, err := os.ReadFile(card)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(after) != seed {
		t.Fatalf("文件字节必须逐字不变：%s", after)
	}
	// 零 status 变更：输入投影里每张卡的 status 逐字未变。
	for i := range before {
		if v.cards[i].Status != before[i].Status || v.cards[i].ID != before[i].ID {
			t.Fatalf("绝不改状态：%s 的 status 从 %q 变成 %q",
				before[i].ID, before[i].Status, v.cards[i].Status)
		}
	}
}

// TestSupportCheckSourceHasNoWriteCall 是机械反证：本文件里不出现任何写盘 / 改状态调用。
func TestSupportCheckSourceHasNoWriteCall(t *testing.T) {
	src, err := os.ReadFile("support_check.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	for _, bad := range []string{"os.WriteFile", "os.Create", "os.Remove", "os.OpenFile",
		"SetStatus", "SetDeleted", "ApplyStateWrite", "ioutil."} {
		if strings.Contains(string(src), bad) {
			t.Fatalf("support_check 只产出建议，不得出现 %s", bad)
		}
	}
}

// TestBuildSupportCheckNilVaultFailsFast：没有 vault 投影就报错，不返回空清单假装无事发生。
func TestBuildSupportCheckNilVaultFailsFast(t *testing.T) {
	if _, err := BuildSupportCheck([]string{"s-del"}, nil); err == nil {
		t.Fatal("nil vault 必须 fail fast")
	}
	// 空的被删集合 → 无受影响卡 → 空清单（不是 null，也不编造条目）。
	got, err := BuildSupportCheck(nil, sampleVault())
	if err != nil {
		t.Fatalf("BuildSupportCheck: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("没有被删目标就没有受影响卡：%+v", got)
	}
	r := New()
	r.SetSupportCheck(got)
	raw, err := r.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(raw), `"support_check":[]`) {
		t.Fatalf("空清单必须是 []（不是 null）：%s", raw)
	}
}
