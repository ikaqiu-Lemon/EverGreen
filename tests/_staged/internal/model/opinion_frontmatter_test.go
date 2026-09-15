package model

// Opinion frontmatter 的读出往返（Schema v2 §3.4）。
//
// 与 `reviewed_at` 同一条纪律，判据也同样是「最容易做丢的那条」：
//
//   - `validation` 是**封闭三值**：第四值反序列化即报错，不静默降级为 pending；
//   - 未知键落进 `Extra` 原样透传，不因为 struct 里没有对应字段就被丢掉；
//   - 生命周期三个维度（status / reviewed_at / deleted_*）与 validation **互不牵连**：
//     rejected 不得连带把 status 读成 deprecated——「已确认不成立」本身是知识资产。

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// opinionFM 是一份完整的观点 frontmatter：含 validation、关系、以及一个未知键。
const opinionFM = `id: o-20260915-harness-boundary
status: active
created_at: '2026-09-15'
updated_at: '2026-09-15T10:00:00+08:00'
validation: pending
sources:
  - source: s-20260915-agent-arch
    note: n-20260915-agent-arch
    rel: support
    reason: 原文第 3 节讨论了扩展成本
relations:
  - target: k-20260915-harness-capabilities
    type: derives
    reason: 该判断由这条知识推导
tags:
  - agent
future_key: 未来才实现的字段
`

func TestOpinionFrontmatterRoundTrip(t *testing.T) {
	var op Opinion
	if err := yaml.Unmarshal([]byte(opinionFM), &op); err != nil {
		t.Fatalf("观点 frontmatter 应可解析：%v", err)
	}
	if op.ID != OpinionID("o-20260915-harness-boundary") {
		t.Fatalf("id 应逐字读出，实际 %q", op.ID)
	}
	if !op.ID.Valid() {
		t.Fatalf("读出的 id 必须自校验通过：%q", op.ID)
	}
	if op.Validation != ValidationPending {
		t.Fatalf("validation 应为 pending，实际 %q", op.Validation)
	}
	if op.Status != StatusActive {
		t.Fatalf("status 应为 active，实际 %q", op.Status)
	}
	if len(op.Sources) != 1 || op.Sources[0].Source != "s-20260915-agent-arch" {
		t.Fatalf("sources 应逐条读出，实际 %+v", op.Sources)
	}
	if len(op.Relations) != 1 || op.Relations[0].Type != RelationDerives {
		t.Fatalf("relations 应逐条读出，实际 %+v", op.Relations)
	}
	// 未知键必须落进 Extra：不认识不等于可以丢。
	if _, ok := op.Extra["future_key"]; !ok {
		t.Fatalf("未知键 future_key 应落进 Extra，实际 Extra=%v", op.Extra)
	}
	// 可选生命周期键缺省即缺省，不回填。
	if op.ReviewedAt != nil {
		t.Fatalf("缺 %s 时必须保持 nil，实际 %v", FMKeyReviewedAt, *op.ReviewedAt)
	}
	if op.DeletedAt != nil || op.DeletedReason != "" || op.ReplacedBy != nil {
		t.Fatalf("未删除的观点不得读出删除维度：deleted_at=%v reason=%q replaced_by=%v",
			op.DeletedAt, op.DeletedReason, op.ReplacedBy)
	}
}

func TestOpinionValidationMissingKeyStaysEmptyNotDefaulted(t *testing.T) {
	// 缺 validation 的文件读出后是空串，而不是被回填成 pending：
	// 「键不存在」与「键为 pending」必须可区分，否则迁移器无法判断哪些文件还没补键。
	const noValidation = `id: o-20260915-x
status: active
created_at: '2026-09-15'
updated_at: '2026-09-15T10:00:00+08:00'
`
	var op Opinion
	if err := yaml.Unmarshal([]byte(noValidation), &op); err != nil {
		t.Fatalf("解析应成功：%v", err)
	}
	if op.Validation != "" {
		t.Fatalf("缺 %s 时不得回填默认值，实际 %q", FMKeyValidation, op.Validation)
	}
	if op.Validation.Valid() {
		t.Fatal("空 validation 不是合法取值")
	}
}

func TestOpinionValidationFourthValueRejectedAtUnmarshal(t *testing.T) {
	const bad = `id: o-20260915-x
status: active
created_at: '2026-09-15'
updated_at: '2026-09-15T10:00:00+08:00'
validation: confirmed
`
	var op Opinion
	err := yaml.Unmarshal([]byte(bad), &op)
	if err == nil {
		t.Fatal("第四档 validation 必须在反序列化处就报错，不得静默接受")
	}
	if !strings.Contains(err.Error(), "confirmed") {
		t.Fatalf("错误信息应回显非法值，实际：%v", err)
	}
	for _, v := range []string{"pending", "validated", "rejected"} {
		if !strings.Contains(err.Error(), v) {
			t.Fatalf("错误信息应列出合法取值 %q，实际：%v", v, err)
		}
	}
}

func TestOpinionValidationOrthogonalToStatus(t *testing.T) {
	// rejected 的观点仍是 active：两个维度不得互相推导。
	const rejectedButActive = `id: o-20260915-x
status: active
created_at: '2026-09-15'
updated_at: '2026-09-15T10:00:00+08:00'
validation: rejected
`
	var op Opinion
	if err := yaml.Unmarshal([]byte(rejectedButActive), &op); err != nil {
		t.Fatalf("解析应成功：%v", err)
	}
	if op.Validation != ValidationRejected {
		t.Fatalf("validation 应为 rejected，实际 %q", op.Validation)
	}
	if op.Status != StatusActive {
		t.Fatalf("rejected 不得连带把 status 读成 %q", op.Status)
	}
	if !op.Validation.Settled() {
		t.Fatal("rejected 属已定论：Settled() 应为 true")
	}
	if ValidationPending.Settled() {
		t.Fatal("pending 未定论：Settled() 应为 false")
	}
	// 键名常量与 struct tag 必须对得上，否则写入侧按常量定位会写错行。
	if !strings.Contains(rejectedButActive, FMKeyValidation+":") {
		t.Fatalf("FMKeyValidation=%q 与实际 frontmatter 键名不一致", FMKeyValidation)
	}
}
