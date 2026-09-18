package store

// 观点验证生命周期落盘原语（SetValidation / ApplyStateWrite 第六形态；Schema v2 §6.1 / §6.2；
// T-007-A1）的单测。每条断言对应契约硬约束而非行为复述：
//   - 一次合法流转 = 单文件、单次 mutateGuarded、单次 persist；落盘只覆盖 `validation`
//     单行、刷新既有 `updated_at` 单行、并向「待验证」尾部追加**恰一个**审计块，其余字节保真；
//   - 五条合法边逐条落盘、四格非法边零写；审计块逐字锁定（含 multiline reason 的可逆 blockquote）；
//   - hash 冲突 / 非法边 / 空白 reason / 零 stamp / 错误 kind / 坏 FM / 缺键 / 重复键 / 非标量
//     一律零字节写入；同态重试不追加；ApplyStateWrite 支持第六形态，default 文案钉「恰六种」。

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

const opinionValidationRel = "domains/ai/opinions/o-20260901-lifecycle.md"

// opinionValidationSample 是一份最小合法 Opinion（固定五分区，v2）。
// 刻意带上 status / 未知键 / 既有待验证项 / 用户补充：它们都是「一格不许动」的反证面。
// `validation` 与 `updated_at` 均为单引号标量，与建卡路径 content.go 的 fmLine 逐字同风格。
const opinionValidationSample = `---
id: o-20260901-lifecycle
title: 生命周期落盘原语
status: active
validation: 'pending'
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: s-20260901-spec
    note: n-20260901-spec
    rel: support
    reason: 契约 §6
vendor_unknown_key: 未知字段逐字保留
---

## 观点

主张正文一个字节都不许动。

## 论据与推理

论据段落。

## 条件与反例

反例段落。

## 待验证

- 既有待验证项

## 用户补充

用户自己的话，勿动。
`

// seedOpinionValidation 铺一份 current validation = from 的 Opinion，返回 store、rel、绝对路径、hash。
func seedOpinionValidation(t *testing.T, from model.Validation) (*Store, string, string, string) {
	t.Helper()
	s, root := newVault(t)
	content := strings.Replace(opinionValidationSample,
		"validation: 'pending'", "validation: '"+string(from)+"'", 1)
	abs := writeSeed(t, root, opinionValidationRel, content)
	f, err := s.Read(opinionValidationRel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return s, opinionValidationRel, abs, f.Hash
}

// expectedAuditBlock 在测试侧**独立**重算审计块的逐字格式（不复用产品实现），
// 因此产品若改一个字节，本用例立刻变红。格式：前导一个换行、末尾换行；reason 每一原始行
// 渲染为 blockquote（非空行 `> <原行>`、空行 `>`，仅用 TrimSpace 判空，记录保留原始字节）。
func expectedAuditBlock(action, from, to, at, reason string) string {
	var b strings.Builder
	b.WriteString("\n### validation audit\n\n")
	b.WriteString("- at: " + at + "\n")
	b.WriteString("- action: " + action + "\n")
	b.WriteString("- transition: " + from + " -> " + to + "\n")
	b.WriteString("- reason:\n")
	for _, ln := range strings.Split(reason, "\n") {
		if len(bytes.TrimSpace([]byte(ln))) == 0 {
			b.WriteString(">\n")
			continue
		}
		b.WriteString("> " + ln + "\n")
	}
	return b.String()
}

// expectedAfter 用三处**受控**改动从 before 重建期望字节：validation 单行覆盖、updated_at 单行刷新、
// 「待验证」既有项之后追加审计块。bytes.Equal(after, expectedAfter) 因此同时锁定字节保真 + 审计格式。
func expectedAfter(before, from, to, action, oldStamp, newStamp, reason string) string {
	out := strings.Replace(before, "validation: '"+from+"'", "validation: '"+to+"'", 1)
	out = strings.Replace(out, "updated_at: '"+oldStamp+"'", "updated_at: '"+newStamp+"'", 1)
	block := expectedAuditBlock(action, from, to, newStamp, reason)
	out = strings.Replace(out, "- 既有待验证项\n", "- 既有待验证项\n"+block, 1)
	return out
}

// legalEdge 是一条合法迁移的期望三元组。
type legalEdge struct {
	from, to model.Validation
	action   string
}

var validationLegalEdges = []legalEdge{
	{model.ValidationPending, model.ValidationValidated, "validate"},
	{model.ValidationPending, model.ValidationRejected, "reject"},
	{model.ValidationValidated, model.ValidationRejected, "reject"},
	{model.ValidationValidated, model.ValidationPending, "reopen"},
	{model.ValidationRejected, model.ValidationPending, "reopen"},
}

const oldStampLiteral = "2026-09-01T10:00:00+08:00"

// TestSetValidation_FiveLegalEdgesWriteExpectedBytes：五条合法边逐条落盘，
// 且落盘结果与「三处受控改动」重建的期望字节逐字相等（字节保真 + 审计格式一次锁死）。
func TestSetValidation_FiveLegalEdgesWriteExpectedBytes(t *testing.T) {
	for _, e := range validationLegalEdges {
		s, rel, abs, hash := seedOpinionValidation(t, e.from)
		before := string(mustBytes(t, abs))
		reason := "判定理由：" + e.action

		res, err := s.SetValidation(rel, hash, e.to, reason, mustStamp(t))
		if err != nil {
			t.Fatalf("%s->%s：SetValidation 必须成功，得 %v", e.from, e.to, err)
		}
		if !res.Written {
			t.Fatalf("%s->%s：hash 相符时必须落盘", e.from, e.to)
		}
		got := string(mustBytes(t, abs))
		want := expectedAfter(before, string(e.from), string(e.to), e.action,
			oldStampLiteral, mustStamp(t).String(), reason)
		if got != want {
			t.Fatalf("%s->%s：落盘字节与期望不符\n--- got ---\n%s\n--- want ---\n%s",
				e.from, e.to, got, want)
		}
		assertNoTmp(t, filepath.Dir(abs))
	}
}

// TestSetValidation_FourIllegalEdgesZeroWrite：四格非法边（三自环 + rejected->validated）
// 一律报错且**零字节写入**。
func TestSetValidation_FourIllegalEdgesZeroWrite(t *testing.T) {
	illegal := []struct{ from, to model.Validation }{
		{model.ValidationPending, model.ValidationPending},
		{model.ValidationValidated, model.ValidationValidated},
		{model.ValidationRejected, model.ValidationRejected},
		{model.ValidationRejected, model.ValidationValidated},
	}
	for _, e := range illegal {
		s, rel, abs, hash := seedOpinionValidation(t, e.from)
		before := mustBytes(t, abs)
		res, err := s.SetValidation(rel, hash, e.to, "非法边不该落盘", mustStamp(t))
		if err == nil {
			t.Fatalf("非法边 %s->%s 必须报错", e.from, e.to)
		}
		if res.Written {
			t.Fatalf("非法边 %s->%s 拒写时不得落盘：%+v", e.from, e.to, res)
		}
		if got := mustBytes(t, abs); !bytes.Equal(got, before) {
			t.Fatalf("非法边 %s->%s 拒写时文件字节必须逐字不变", e.from, e.to)
		}
	}
}

// TestSetValidation_MultilineReasonExactBytes：多行 reason（含中间空行、首尾空白行）
// 逐行渲染为可逆 blockquote，落盘字节与期望逐字相等（不压成单行、不丢中间空行）。
func TestSetValidation_MultilineReasonExactBytes(t *testing.T) {
	s, rel, abs, hash := seedOpinionValidation(t, model.ValidationPending)
	before := string(mustBytes(t, abs))
	reason := "第一行理由\n\n  第三行带前导空白\n第四行"

	res, err := s.SetValidation(rel, hash, model.ValidationValidated, reason, mustStamp(t))
	if err != nil || !res.Written {
		t.Fatalf("多行 reason 合法流转必须落盘：%v / %+v", err, res)
	}
	got := string(mustBytes(t, abs))
	want := expectedAfter(before, "pending", "validated", "validate",
		oldStampLiteral, mustStamp(t).String(), reason)
	if got != want {
		t.Fatalf("多行 reason 审计块字节不符\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// 显式反证：中间空行必须以裸 `>` 保留，且不得出现「压成单行」的痕迹。
	if !strings.Contains(got, "> 第一行理由\n>\n>   第三行带前导空白\n> 第四行\n") {
		t.Fatalf("多行 reason 的可逆 blockquote 不符：\n%s", got)
	}
}

// TestSetValidation_SameStateRetryDoesNotAppend：一次成功流转后，同态重试（自环）被状态机
// 拒绝、零写入，不产生第二个审计块（append-only 不等于允许幂等重复追加）。
func TestSetValidation_SameStateRetryDoesNotAppend(t *testing.T) {
	s, rel, abs, hash := seedOpinionValidation(t, model.ValidationPending)
	if _, err := s.SetValidation(rel, hash, model.ValidationValidated, "首次确认", mustStamp(t)); err != nil {
		t.Fatalf("首次流转必须成功：%v", err)
	}
	afterFirst := mustBytes(t, abs)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// validated->validated 是自环：必须被拒且零写入。
	res, err := s.SetValidation(rel, f.Hash, model.ValidationValidated, "重复确认", mustStamp(t))
	if err == nil || res.Written {
		t.Fatalf("同态重试（validated->validated）必须被拒且零写入：%v / %+v", err, res)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, afterFirst) {
		t.Fatal("同态重试后字节必须逐字不变（不追加第二个审计块）")
	}
	if n := bytes.Count(mustBytes(t, abs), []byte("### validation audit\n")); n != 1 {
		t.Fatalf("审计块必须恰一个，实得 %d", n)
	}
}

// TestSetValidation_HashConflictZeroWrite：B3——hash 不符即跳过，零写入、零字节改动。
func TestSetValidation_HashConflictZeroWrite(t *testing.T) {
	s, rel, abs, _ := seedOpinionValidation(t, model.ValidationPending)
	before := mustBytes(t, abs)
	res, err := s.SetValidation(rel, "deadbeef", model.ValidationValidated, "理由", mustStamp(t))
	if err == nil {
		t.Fatal("B3：hash 不符必须报跳过")
	}
	if _, ok := AsSkip(err); !ok {
		t.Fatalf("B3 跳过必须是 *SkipError，实得 %T：%v", err, err)
	}
	if res.Written {
		t.Fatalf("B3 跳过时不得落盘：%+v", res)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatal("B3 跳过时文件字节必须逐字不变")
	}
}

// TestSetValidation_BlankReasonZeroWrite：空 / 纯空白 reason 一律拒写、零字节改动
// （审计块的 reason 是可追溯性唯一载体，允许空白等于丢掉判定依据）。
func TestSetValidation_BlankReasonZeroWrite(t *testing.T) {
	for _, reason := range []string{"", "   ", "\n\t \n"} {
		s, rel, abs, hash := seedOpinionValidation(t, model.ValidationPending)
		before := mustBytes(t, abs)
		res, err := s.SetValidation(rel, hash, model.ValidationValidated, reason, mustStamp(t))
		if err == nil || res.Written {
			t.Fatalf("空白 reason=%q 必须拒写且零写入：%v / %+v", reason, err, res)
		}
		if got := mustBytes(t, abs); !bytes.Equal(got, before) {
			t.Fatalf("空白 reason=%q 拒写时字节必须不变", reason)
		}
	}
}

// TestSetValidation_ZeroStampZeroWrite：零值时刻拒写、零字节改动（审计 at 与 updated_at
// 刷新都取这个时刻，零值等于「没有发生时间的生命周期事件」）。
func TestSetValidation_ZeroStampZeroWrite(t *testing.T) {
	s, rel, abs, hash := seedOpinionValidation(t, model.ValidationPending)
	before := mustBytes(t, abs)
	res, err := s.SetValidation(rel, hash, model.ValidationValidated, "理由", model.Stamp{})
	if err == nil || res.Written {
		t.Fatalf("零 stamp 必须拒写且零写入：%v / %+v", err, res)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatal("零 stamp 拒写时字节必须不变")
	}
}

// TestSetValidation_WrongKindZeroWrite：对非 Opinion（知识卡）落 validation 一律拒写、零改动
// （目标不是合法 Opinion，本层不猜、不改写）。
func TestSetValidation_WrongKindZeroWrite(t *testing.T) {
	s, root := newVault(t)
	rel := "cards/k-20260901-state.md"
	abs := writeSeed(t, root, rel, stateCardSample)
	before := mustBytes(t, abs)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	res, err := s.SetValidation(rel, f.Hash, model.ValidationValidated, "理由", mustStamp(t))
	if err == nil || res.Written {
		t.Fatalf("对知识卡落 validation 必须拒写且零写入：%v / %+v", err, res)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatal("wrong-kind 拒写时字节必须不变")
	}
}

// TestSetValidation_MissingDuplicateNonScalarZeroWrite：缺 validation 键 / 重复键 / 非标量
// 一律拒写、零字节改动（坏 FM / 缺键 / 重复键 / 非标量都在写前失败）。
func TestSetValidation_MissingDuplicateNonScalarZeroWrite(t *testing.T) {
	cases := map[string]string{
		"缺 validation 键": strings.Replace(opinionValidationSample, "validation: 'pending'\n", "", 1),
		"validation 重复键": strings.Replace(opinionValidationSample,
			"validation: 'pending'\n", "validation: 'pending'\nvalidation: 'rejected'\n", 1),
		"validation 非标量": strings.Replace(opinionValidationSample,
			"validation: 'pending'\n", "validation:\n  nested: true\n", 1),
	}
	for name, content := range cases {
		s, root := newVault(t)
		abs := writeSeed(t, root, opinionValidationRel, content)
		before := mustBytes(t, abs)
		f, err := s.Read(opinionValidationRel)
		if err != nil {
			// 读阶段失败同样是零写入（文件未变）；直接比对字节。
			if got := mustBytes(t, abs); !bytes.Equal(got, before) {
				t.Fatalf("%s：读失败也必须零改动", name)
			}
			continue
		}
		res, err := s.SetValidation(opinionValidationRel, f.Hash,
			model.ValidationValidated, "理由", mustStamp(t))
		if err == nil || res.Written {
			t.Fatalf("%s：必须拒写且零写入：%v / %+v", name, err, res)
		}
		if got := mustBytes(t, abs); !bytes.Equal(got, before) {
			t.Fatalf("%s：拒写时字节必须不变", name)
		}
	}
}

// TestApplyStateWriteValidation_IsTheSixthShape：唯一对外入口能表达**第六**形态，
// 且第七形态写不出来（default 报错文案钉「恰六种」）。Validation 目标态经**独立** Validation 字段
// 传入，不复用 Status / Reason 槽。
func TestApplyStateWriteValidation_IsTheSixthShape(t *testing.T) {
	s, _, abs, hash := seedOpinionValidation(t, model.ValidationPending)
	before := string(mustBytes(t, abs))
	res, err := s.ApplyStateWrite(StateWriteSpec{
		Op:           StateWriteValidation,
		Rel:          opinionValidationRel,
		ExpectedHash: hash,
		Validation:   model.ValidationValidated,
		Reason:       "经用户确认成立",
		Stamp:        mustStamp(t),
	})
	if err != nil || !res.Written {
		t.Fatalf("ApplyStateWrite(validation) 必须落盘：%v / %+v", err, res)
	}
	want := expectedAfter(before, "pending", "validated", "validate",
		oldStampLiteral, mustStamp(t).String(), "经用户确认成立")
	if got := string(mustBytes(t, abs)); got != want {
		t.Fatalf("第六形态未按契约落盘\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// 第七形态：未知形态一律报错，且文案里必须钉住「恰六种」。
	_, err = s.ApplyStateWrite(StateWriteSpec{Op: "validation_clear",
		Rel: opinionValidationRel, ExpectedHash: hash})
	if err == nil {
		t.Fatal("未知状态写形态必须报错（封闭六值）")
	}
	if !strings.Contains(err.Error(), "恰六种") {
		t.Fatalf("default 文案必须钉「恰六种」，实得：%v", err)
	}
}

// TestSetValidation_OneGuardedWriteBySource 是「单次守卫写 / 单次 persist」的源码级机械反证：
// SetValidation 体内恰一处 mutateGuarded（validation 覆盖 + updated_at 刷新 + 审计追加全在
// 同一候选内完成），若谁拆成两次守卫写本用例立刻变红。
func TestSetValidation_OneGuardedWriteBySource(t *testing.T) {
	src := mustBytes(t, "state_write.go")
	start := bytes.Index(src, []byte("func (s *Store) SetValidation("))
	if start < 0 {
		t.Fatal("未找到 SetValidation：第六种状态写形态必须在本文件内")
	}
	rest := src[start:]
	end := bytes.Index(rest, []byte("\n}\n"))
	if end < 0 {
		t.Fatal("SetValidation 函数体定界失败")
	}
	body := string(rest[:end])
	if n := strings.Count(body, "mutateGuarded("); n != 1 {
		t.Fatalf("SetValidation 内 mutateGuarded 调用数 = %d，必须恰 1（单次守卫写 / 单次 persist）", n)
	}
}

// TestSetValidation_UpdatedAtMissingDuplicateNonScalarZeroWrite：本形态三键**同成同败**。
// SetValidation 除覆盖 `validation` 外，还必然刷新既有 `updated_at`——因此 `updated_at`
// 与 `validation` 一样，是本次真正被改写的键。缺 `updated_at` 键 / 重复键 / 非标量都必须在
// 任何区间改写之前被拒（报错 + 全文零字节写入）：否则会落出「validation + 审计块写了、
// updated_at 没刷新」的半截产物（共享 refreshUpdatedAt 对缺键是「原样返回不新增」，那条
// 口径服务于历史写形态，不能让本形态借它把 updated_at 悄悄漏刷）。
func TestSetValidation_UpdatedAtMissingDuplicateNonScalarZeroWrite(t *testing.T) {
	cases := map[string]string{
		"缺 updated_at 键": strings.Replace(opinionValidationSample,
			"updated_at: '"+oldStampLiteral+"'\n", "", 1),
		"updated_at 重复键": strings.Replace(opinionValidationSample,
			"updated_at: '"+oldStampLiteral+"'\n",
			"updated_at: '"+oldStampLiteral+"'\nupdated_at: '2026-09-02T10:00:00+08:00'\n", 1),
		"updated_at 非标量": strings.Replace(opinionValidationSample,
			"updated_at: '"+oldStampLiteral+"'\n", "updated_at:\n  nested: true\n", 1),
	}
	for name, content := range cases {
		s, root := newVault(t)
		abs := writeSeed(t, root, opinionValidationRel, content)
		before := mustBytes(t, abs)
		f, err := s.Read(opinionValidationRel)
		if err != nil {
			// 读阶段失败同样是零写入（文件未变）；直接比对字节。
			if got := mustBytes(t, abs); !bytes.Equal(got, before) {
				t.Fatalf("%s：读失败也必须零改动", name)
			}
			continue
		}
		res, err := s.SetValidation(opinionValidationRel, f.Hash,
			model.ValidationValidated, "理由", mustStamp(t))
		if err == nil || res.Written {
			t.Fatalf("%s：必须拒写且零写入（三键同成同败）：%v / %+v", name, err, res)
		}
		if got := mustBytes(t, abs); !bytes.Equal(got, before) {
			t.Fatalf("%s：拒写时全文字节必须逐字不变", name)
		}
	}
}

// TestSetValidation_DisguisedOpinionNonOpinionIDZeroWrite：文件正文伪装成**完整五分区**、
// `validation` / `updated_at` 都合法，但 frontmatter `id` 不是 o- 前缀（合法知识卡 id k-* /
// 空 id / 畸形前缀）。OpinionOf 只做 Decode + 分区结构校验、**不校验 id 前缀**，因此这类文件
// 会通过 OpinionOf；SetValidation 必须显式校验 id（model.ParseOpinionID）并拒写、零字节改动，
// 杜绝「把非观点当观点改写」。这补上原 wrong-kind 用例（用知识卡分区、只证分区不匹配）的盲区。
func TestSetValidation_DisguisedOpinionNonOpinionIDZeroWrite(t *testing.T) {
	cases := map[string]string{
		"id 是合法知识卡前缀 k-": "id: k-20260901-lifecycle",
		"id 前缀畸形 x-":     "id: x-20260901-lifecycle",
		"id 为空":          "id:",
	}
	for name, idLine := range cases {
		s, root := newVault(t)
		content := strings.Replace(opinionValidationSample,
			"id: o-20260901-lifecycle", idLine, 1)
		abs := writeSeed(t, root, opinionValidationRel, content)
		before := mustBytes(t, abs)
		f, err := s.Read(opinionValidationRel)
		if err != nil {
			// 读阶段失败同样是零写入（文件未变）；直接比对字节。
			if got := mustBytes(t, abs); !bytes.Equal(got, before) {
				t.Fatalf("%s：读失败也必须零改动", name)
			}
			continue
		}
		res, err := s.SetValidation(opinionValidationRel, f.Hash,
			model.ValidationValidated, "理由", mustStamp(t))
		if err == nil || res.Written {
			t.Fatalf("%s：伪装成五分区但 id 非 o- 必须拒写且零写入：%v / %+v", name, err, res)
		}
		if got := mustBytes(t, abs); !bytes.Equal(got, before) {
			t.Fatalf("%s：拒写时全文字节必须逐字不变", name)
		}
	}
}
