package model_test

// Opinion 实体基座（Schema v2 契约 §3.1 / §3.4）。
//
// 本文件锁定三件事：
//  1. `o-` 前缀被 ParseID 与 ParseOpinionID 正确纳管，且与 s-/n-/k- 互不可混用；
//  2. Opinion 的 validation 是封闭三值枚举，第四值反序列化即报错；
//  3. `opinion.type` / `opinion.stance` / `opinion.lean` 与 card/note 对称进黑名单，
//     不给 Opinion 开「类型写进 frontmatter」的后门（EG-DOM-01 的对称沿用）。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

func TestOpinionIDPrefixParsed(t *testing.T) {
	p, err := model.ParseID("o-20260915-harness-boundary-cost")
	if err != nil {
		t.Fatalf("ParseID(o-…) 应成功，得到 err=%v", err)
	}
	if p.Prefix != model.PrefixOpinion {
		t.Fatalf("前缀应为 %q，实际 %q", model.PrefixOpinion, p.Prefix)
	}
	if p.Date != "20260915" {
		t.Fatalf("日期段应为 20260915，实际 %q", p.Date)
	}
	if p.Slug != "harness-boundary-cost" {
		t.Fatalf("slug 应为 harness-boundary-cost，实际 %q", p.Slug)
	}
}

func TestParseOpinionIDRejectsOtherPrefixes(t *testing.T) {
	if _, err := model.ParseOpinionID("o-20260915-x"); err != nil {
		t.Fatalf("合法 o- ID 应被接受，得到 err=%v", err)
	}
	// 四类 ID 互不可混用（F2）：k-/n-/s- 串不得被当成 Opinion ID。
	for _, bad := range []string{
		"k-20260915-x",
		"n-20260915-x",
		"s-20260915-x",
		"p-20260915-1",
	} {
		if _, err := model.ParseOpinionID(bad); err == nil {
			t.Fatalf("ParseOpinionID(%q) 应报错（前缀不匹配），却成功", bad)
		}
	}
	// 形态非法同样被拒。
	for _, bad := range []string{
		"o-2026915-x", // 日期段 7 位
		"o-20260915-", // slug 为空
		"o-20260915",  // 缺 slug 段
		"o20260915-x", // 缺前缀分隔
		"opinion-x-y", // 伪前缀
	} {
		if _, err := model.ParseOpinionID(bad); err == nil {
			t.Fatalf("ParseOpinionID(%q) 应报错（形态非法），却成功", bad)
		}
	}
}

func TestOpinionIDValidAndString(t *testing.T) {
	id := model.OpinionID("o-20260915-dsh-over-engineered")
	if !id.Valid() {
		t.Fatal("合法 OpinionID 的 Valid() 应为 true")
	}
	if id.String() != "o-20260915-dsh-over-engineered" {
		t.Fatalf("String() 应逐字回显，实际 %q", id.String())
	}
	if (model.OpinionID("k-20260915-x")).Valid() {
		t.Fatal("前缀为 k- 的串不得被 OpinionID.Valid() 认可")
	}
}

func TestNewOpinionIDIdempotent(t *testing.T) {
	d, err := model.ParseDate("2026-09-15")
	if err != nil {
		t.Fatalf("固定日期应可解析：%v", err)
	}
	a := model.NewOpinionID(d, "Harness 的核心边界决定扩展成本")
	b := model.NewOpinionID(d, "Harness 的核心边界决定扩展成本")
	if a != b {
		t.Fatalf("同一 (日期, 标题) 必须幂等：%q vs %q", a, b)
	}
	if !strings.HasPrefix(a.String(), model.PrefixOpinion+"20260915-") {
		t.Fatalf("生成的 ID 应形如 o-20260915-…，实际 %q", a)
	}
	if !a.Valid() {
		t.Fatalf("生成的 ID 必须自校验通过，实际 %q", a)
	}
}

func TestParseIDErrorMentionsOpinionPrefix(t *testing.T) {
	_, err := model.ParseID("z-20260915-x")
	if err == nil {
		t.Fatal("非法前缀应报错")
	}
	// 错误信息必须把 o- 列进合法前缀集合，否则使用者无从得知 Opinion 已被纳管。
	if !strings.Contains(err.Error(), model.PrefixOpinion) {
		t.Fatalf("ParseID 的错误信息应列出 %q 前缀，实际：%s", model.PrefixOpinion, err.Error())
	}
}

func TestValidationEnumClosedThreeValues(t *testing.T) {
	want := []model.Validation{
		model.ValidationPending,
		model.ValidationValidated,
		model.ValidationRejected,
	}
	got := model.ValidValidations()
	if len(got) != len(want) {
		t.Fatalf("validation 应恰有 3 个合法值，实际 %d：%v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("validation 顺序应为 pending / validated / rejected，实际 %v", got)
		}
	}
	for _, v := range want {
		if !v.Valid() {
			t.Fatalf("%q 应为合法 validation", v)
		}
	}
	if (model.Validation("confirmed")).Valid() {
		t.Fatal("第四值 confirmed 不得被认可")
	}
}

func TestParseValidationRejectsFourthValue(t *testing.T) {
	if _, err := model.ParseValidation("validated"); err != nil {
		t.Fatalf("validated 应被接受，得到 err=%v", err)
	}
	err := errOf(model.ParseValidation("maybe"))
	if err == nil {
		t.Fatal("非法 validation 必须报错")
	}
	// 错误信息必须带合法取值集合，否则使用者只能猜。
	for _, v := range []string{"pending", "validated", "rejected"} {
		if !strings.Contains(err.Error(), v) {
			t.Fatalf("错误信息应列出合法取值 %q，实际：%s", v, err.Error())
		}
	}
}

func errOf(_ model.Validation, err error) error { return err }

func TestOpinionDeprecatedFieldPathsSymmetric(t *testing.T) {
	// card / note 已禁的三个「类型 / 倾向」字段必须对称地在 opinion 上也禁掉：
	// 类型由 ID 前缀决定、领域由目录决定，冗余元数据会让真源漂移。
	for _, p := range []string{
		"opinion.domain",
		"opinion.type",
		"opinion.stance",
		"opinion.lean",
		"opinion.tendency",
		"opinion.candidate",
		"opinion.source_check",
	} {
		if !model.IsDeprecatedFieldPath(p) {
			t.Fatalf("%q 应命中废弃字段黑名单", p)
		}
	}
	// validation 是 Opinion 唯一新增的合法 frontmatter 键，绝不能被黑名单误伤。
	if model.IsDeprecatedFieldPath("opinion.validation") {
		t.Fatal("opinion.validation 是合法字段，不得命中黑名单")
	}
	// 白名单的既有条目不受影响。
	if model.IsDeprecatedFieldPath("relations[].type") {
		t.Fatal("relations[].type 是白名单字段，不得命中黑名单")
	}
}
