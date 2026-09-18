package store

// 综述失准标记写口（SetStale）的单测（M4 · R6 落盘口；对账合同 §9；裁决 A-33 / A-34；
// T-evergreen.s1_main_flow-158614-055 阶段 1）。
//
// 每条断言对应一条合同硬约束，不是行为复述：
//   - §9 两键封闭：写入键集合恰 `{stale, stale_reason}`——第三个键、正文、status、
//     删除维度、替代指针、reviewed_at、updated_at 一格不许动；
//   - §9 取值封闭：`stale_reason` 恰三值，第四种取值一律拒写（零写入、零字节改动）；
//   - §1.3 第 3 行「一次守卫写」：两键落在**同一次** mutateGuarded 里（源码级机械反证：
//     SetStale 体内恰一处 mutateGuarded，且经 setFMScalarKeys 落多键）；
//   - B3：hash 不符即跳过且文件字节不变；ADR-11 四类同构：本层只认 rel，不按对象类型分叉。

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// staleReviewSample 是一份最小主题综述样例（矩阵 #33 的对象类）。
// 刻意带上 status / reviewed_at / 未知键 / 正文：它们都是「一格不许动」的反证面。
const staleReviewSample = `---
id: r-20260901-attention
title: 注意力机制主题综述
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
reviewed_at: "2026-09-02T10:00:00+08:00"
vendor_unknown_key: 未知字段逐字保留
---

## 综述

综述正文一个字节都不许动。
`

const staleRel = "domains/ai-infra/reviews/r-20260901-attention.md"

// seedStaleReview 铺一份综述并返回 store、绝对路径、当前 hash。
func seedStaleReview(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, root := newVault(t)
	abs := writeSeed(t, root, staleRel, staleReviewSample)
	f, err := s.Read(staleRel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return s, abs, f.Hash
}

// TestSetStale_WritesExactlyTwoKeysInOneGuardedWrite：两键在**一次**守卫写内落盘，
// 且写入键集合恰 `{stale, stale_reason}`（正文 / 其它 frontmatter 键逐字不变）。
func TestSetStale_WritesExactlyTwoKeysInOneGuardedWrite(t *testing.T) {
	for _, reason := range model.ValidStaleReasons() {
		s, abs, hash := seedStaleReview(t)
		before := mustBytes(t, abs)

		res, err := s.SetStale(staleRel, hash, reason)
		if err != nil {
			t.Fatalf("SetStale(%s): %v", reason, err)
		}
		if !res.Written {
			t.Fatalf("%s：hash 相符时必须落盘", reason)
		}
		after := mustBytes(t, abs)
		fm := fmOf(t, after)
		if !strings.Contains(fm, "stale: true\n") {
			t.Fatalf("%s：stale 必须逐字落成 YAML 布尔真：%s", reason, fm)
		}
		if !strings.Contains(fm, `stale_reason: '`+string(reason)+"'\n") {
			t.Fatalf("%s：stale_reason 必须逐字落成双引号标量：%s", reason, fm)
		}
		// 写入键集合恰两格：只许新增两行，不重排、不新增第三个键。
		if got, want := len(strings.Split(fm, "\n")), len(strings.Split(fmOf(t, before), "\n"))+2; got != want {
			t.Fatalf("%s：只允许新增 stale / stale_reason 两行，行数 %d ≠ %d", reason, got, want)
		}
		if diff := addedFMKeys(t, before, after); len(diff) != 2 ||
			!(diff[0] == model.FMKeyStale && diff[1] == model.FMKeyStaleReason) {
			t.Fatalf("%s：写入键集合必须恰 {%s, %s}，实得 %v",
				reason, model.FMKeyStale, model.FMKeyStaleReason, diff)
		}
		// 正交维度一格不动（§9：不自动重算、不自动改状态）。
		if bodyOf(t, after) != bodyOf(t, before) {
			t.Fatalf("%s：综述正文必须逐字保留", reason)
		}
		for _, keep := range []string{"status: active\n",
			`reviewed_at: "2026-09-02T10:00:00+08:00"` + "\n",
			"updated_at: '2026-09-01T10:00:00+08:00'\n",
			"vendor_unknown_key: 未知字段逐字保留\n"} {
			if !strings.Contains(fm, keep) {
				t.Fatalf("%s：失准标记不得改写既有键 %q：%s", reason, keep, fm)
			}
		}
		for _, forbidden := range []string{"deleted_at:", "deleted_reason:", "replaced_by:"} {
			if strings.Contains(fm, forbidden) {
				t.Fatalf("%s：失准标记与删除 / 替代维度正交，不得引入 %s：%s", reason, forbidden, fm)
			}
		}
		assertNoTmp(t, filepath.Dir(abs))
	}
}

// addedFMKeys 返回 after 相对 before **新增**的顶层键名（按出现顺序）。
func addedFMKeys(t *testing.T, before, after []byte) []string {
	t.Helper()
	old := map[string]bool{}
	for _, k := range fmTopKeys(fmOf(t, before)) {
		old[k] = true
	}
	var added []string
	for _, k := range fmTopKeys(fmOf(t, after)) {
		if !old[k] {
			added = append(added, k)
		}
	}
	return added
}

func fmTopKeys(fm string) []string {
	var keys []string
	for _, line := range strings.Split(fm, "\n") {
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") {
			continue
		}
		if i := strings.Index(line, ":"); i > 0 {
			keys = append(keys, line[:i])
		}
	}
	return keys
}

// TestSetStale_OneGuardedWriteBySource 是「一次守卫写」的**源码级**机械反证：
// SetStale 体内恰一处 mutateGuarded，且多键经 setFMScalarKeys 落地——
// 若谁把它改成「先写 stale 再写 stale_reason」的两次守卫写，本用例立刻变红。
func TestSetStale_OneGuardedWriteBySource(t *testing.T) {
	src, err := os.ReadFile("state_write.go")
	if err != nil {
		t.Fatalf("读 state_write.go：%v", err)
	}
	start := bytes.Index(src, []byte("func (s *Store) SetStale("))
	if start < 0 {
		t.Fatal("未找到 SetStale：第五种状态写形态必须在本文件内")
	}
	rest := src[start:]
	end := bytes.Index(rest, []byte("\n}\n"))
	if end < 0 {
		t.Fatal("SetStale 函数体定界失败")
	}
	body := string(rest[:end])
	if n := strings.Count(body, "mutateGuarded("); n != 1 {
		t.Fatalf("SetStale 内 mutateGuarded 调用数 = %d，必须恰 1（两键一次守卫写，不是两次写）", n)
	}
	if !strings.Contains(body, "setFMScalarKeys(") {
		t.Fatal("两键必须经既有 setFMScalarKeys 一次落成，不许自造第二套多键写路径")
	}
}

// TestSetStale_ReasonClosedThreeValues：封闭三值之外（含空、第四种取值、英文别名）
// 一律拒写 ErrStaleReasonClosed，且**零写入、零字节改动**（本层绝不代入默认理由）。
func TestSetStale_ReasonClosedThreeValues(t *testing.T) {
	if got := len(model.ValidStaleReasons()); got != 3 {
		t.Fatalf("stale_reason 合法取值必须恰 3 个，实得 %d：%v", got, model.ValidStaleReasons())
	}
	bad := []model.StaleReason{"", "引用卡已变更", "stale", "引用卡已更新 ", "引用卡已逻辑删除。"}
	for _, reason := range bad {
		s, abs, hash := seedStaleReview(t)
		before := mustBytes(t, abs)
		res, err := s.SetStale(staleRel, hash, reason)
		if err == nil {
			t.Fatalf("reason=%q 不在封闭三值内，必须拒写", reason)
		}
		if res.Written {
			t.Fatalf("reason=%q 拒写时不得落盘：%+v", reason, res)
		}
		if got := mustBytes(t, abs); !bytes.Equal(got, before) {
			t.Fatalf("reason=%q 拒写时文件字节必须逐字不变", reason)
		}
	}
}

// TestSetStale_HashMismatchSkipsWithoutWrite：B3——hash 不符即跳过，
// 跳过是 *SkipError{file_changed}，且文件字节不变（B3 不豁免，无第三种跳过机制）。
func TestSetStale_HashMismatchSkipsWithoutWrite(t *testing.T) {
	s, abs, _ := seedStaleReview(t)
	before := mustBytes(t, abs)

	res, err := s.SetStale(staleRel, "deadbeef", model.StaleReasonUpdated)
	if err == nil {
		t.Fatal("B3：hash 不符必须报跳过，不得静默写入")
	}
	skip, ok := AsSkip(err)
	if !ok {
		t.Fatalf("B3 跳过必须是 *SkipError，实得 %T：%v", err, err)
	}
	if skip.Reason != SkipFileChanged || CauseFor(skip.Reason) != "content_hash_mismatch" {
		t.Fatalf("跳过命名必须复用封闭两值，实得 %+v", skip)
	}
	if res.Written {
		t.Fatalf("B3 跳过时不得落盘：%+v", res)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatal("B3 跳过时文件字节必须逐字不变（零写入、零字节改动）")
	}
}

// TestSetStale_RewriteSameValueIsByteIdentical：同值重写只覆盖那两行、不累积历史键，
// 字节逐字相同（合同 §9 明文不自动清除 stale，本层也不提供反向形态）。
func TestSetStale_RewriteSameValueIsByteIdentical(t *testing.T) {
	s, abs, hash := seedStaleReview(t)
	if _, err := s.SetStale(staleRel, hash, model.StaleReasonDeleted); err != nil {
		t.Fatalf("首次 SetStale: %v", err)
	}
	first := mustBytes(t, abs)
	f, err := s.Read(staleRel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetStale(staleRel, f.Hash, model.StaleReasonDeleted); err != nil {
		t.Fatalf("同值重写: %v", err)
	}
	if got := mustBytes(t, abs); !bytes.Equal(got, first) {
		t.Fatalf("同值重写必须字节逐字相同（单键覆盖，不累积历史）：\n%s", got)
	}
	if n := strings.Count(fmOf(t, first), "stale_reason:"); n != 1 {
		t.Fatalf("stale_reason 只许有一行，实得 %d 行", n)
	}
}

// TestApplyStateWriteStale_IsTheFifthShape：唯一对外入口能表达第五形态，
// 且未知形态写不出来（default 报错文案在 validation 第六形态引入后钉「恰六种」；
// stale 仍是历史上的第五形态，本结论不改写）。
func TestApplyStateWriteStale_IsTheFifthShape(t *testing.T) {
	s, abs, hash := seedStaleReview(t)
	res, err := s.ApplyStateWrite(StateWriteSpec{Op: StateWriteStale, Rel: staleRel,
		ExpectedHash: hash, StaleReason: model.StaleReasonDeprecated})
	if err != nil || !res.Written {
		t.Fatalf("ApplyStateWrite(stale) 必须落盘：%v / %+v", err, res)
	}
	fm := fmOf(t, mustBytes(t, abs))
	if !strings.Contains(fm, "stale: true\n") ||
		!strings.Contains(fm, `stale_reason: '`+string(model.StaleReasonDeprecated)+"'\n") {
		t.Fatalf("第五形态未按合同落盘：%s", fm)
	}
	// 未知形态：一律报错，且文案里必须钉住「恰六种」（第六形态 validation 在 T-007 引入后，
	// 状态写形态总数从五升到六；stale 仍是历史上的第五形态，本结论不改写）。
	_, err = s.ApplyStateWrite(StateWriteSpec{Op: "stale_clear", Rel: staleRel, ExpectedHash: hash})
	if err == nil {
		t.Fatal("未知状态写形态必须报错（封闭六值）")
	}
	if !strings.Contains(err.Error(), "恰六种") {
		t.Fatalf("default 文案必须钉「恰六种」，实得：%v", err)
	}
}
