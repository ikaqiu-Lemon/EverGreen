package plan

// M4 期新增 op `set_stale` 的校验 + 落盘接线用例
// （对账合同 §9 / R6；裁决 **A-33**（新增 op，op 全集 16 + 1 = 17）/ **A-34**
// （不需要 `--user-request`，矩阵不新增行、只净放开 #33 的 P-A 一格）；
// T-evergreen.s1_main_flow-158614-055 阶段 1）。
//
// 判据面（每条都是合同硬约束，不是行为复述）：
//   - **P-A 可写**：Agent 自动路径（无命令行佐证）零 error、恰一条 ActSetStale action，
//     落盘后 frontmatter 新增键集合恰 `{stale, stale_reason}`；
//   - **P-U 仍 🔴**：伪造成 P-U（initiator=user + 命令行佐证）被矩阵 #33 判 E6、零展开、
//     目标文件字节不变——A-34 只放开 P-A 一格，本 task 不放宽 P-U（只加严不放宽）；
//   - **综述专属**：`target` 是 `k-` / `n-` / `s-` / `p-` 一律 E2、零展开；
//   - **取值封闭**：`reason` 缺失或第四种取值 → E5、零展开、零写入；
//   - **B3 不豁免**：base 未覆盖目标 → W6 + skipped[]（封闭两值 file_changed /
//     content_hash_mismatch），零写入零字节改动，不自造第三种 kind；
//   - **写口唯一**：落盘只经 store.ApplyStateWrite（executor 里不直呼 SetStale，
//     该护栏由 internal/store 的 TestStateWritePortIsUniqueToStore 机械反证）。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const recapM4Fixture = `---
id: %s
title: 注意力机制主题综述
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
source_cards:
  - k-20260901-attention
vendor_unknown_key: 未知字段逐字保留%s
---

## 综述

综述正文一个字节都不许动。
`

const relRecap = "domains/ai-infra/reviews/r-20260901-attention.md"

func recapM4(id, extra string) string { return fmt.Sprintf(recapM4Fixture, id, extra) }

// m4Files 在 M3 那套库上加一份主题综述（矩阵 #33 的对象类）。
func m4Files() map[string]string {
	files := m3Files()
	files[relRecap] = recapM4("r-20260901-attention", "")
	return files
}

const staleOpUpdated = `{"op":"set_stale","target":"r-20260901-attention","reason":"引用卡已更新"}`

// TestSetStale_AgentPathWritesExactlyTwoKeys：A-34 —— Agent 自动路径（**无** `--user-request`）
// 即可写，落盘只多 `stale` / `stale_reason` 两行，其余字节逐字不变。
func TestSetStale_AgentPathWritesExactlyTwoKeys(t *testing.T) {
	files := m4Files()
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)

	res := m3RunAgent(t, files, staleOpUpdated)
	if res.Failed() {
		t.Fatalf("A-34：R6 写 stale 不需要 --user-request，P-A 必须零 error，实得 %v",
			codes(res.Errors))
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActSetStale {
		t.Fatalf("set_stale 应展开恰一条失准标记 action：%+v", res.Actions)
	}
	if got := res.Actions[0].Stale; got == nil || got.Reason != model.StaleReasonUpdated {
		t.Fatalf("action 载荷未带上封闭三值之一：%+v", res.Actions[0].Stale)
	}
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Skipped) != 0 || len(out.Failures) != 0 {
		t.Fatalf("失准标记应写入成功：skipped=%+v failures=%+v", out.Skipped, out.Failures)
	}
	if len(out.Written) != 1 || out.Written[0] != relRecap {
		t.Fatalf("只应写这一份综述（不连带重算、不连带写卡）：%v", out.Written)
	}
	if len(out.RecapsStaled) != 1 || out.RecapsStaled[0] != "r-20260901-attention" {
		t.Fatalf("执行结果必须登记被标记的综述：%+v", out.RecapsStaled)
	}
	after := readVaultFile(t, dir, relRecap)
	want := recapM4("r-20260901-attention",
		"\nstale: true\nstale_reason: '引用卡已更新'")
	if after != want {
		t.Fatalf("落盘结果只应多 stale / stale_reason 两行：\n实得：\n%s", after)
	}
	// 引用卡一个字节不动（失准标记只写综述那一份文件）。
	if got := readVaultFile(t, dir, relAttention); got != files[relAttention] {
		t.Fatal("失准标记不得改写被引用的知识卡")
	}
}

// TestSetStale_UserPathStillDenied：#33 的 P-U 一格**未动**（仍 🔴）——
// 即使伪造成用户路径（initiator=user + 命令行佐证）也被 E6 拦下、零展开、字节不变。
//
// 这条是「只加严不放宽」的机械反证：A-34 授权的是 P-A 那一格，不是整行放开。
func TestSetStale_UserPathStillDenied(t *testing.T) {
	files := m4Files()
	dir := t.TempDir()
	writeVault(t, dir, files)
	before := readVaultFile(t, dir, relRecap)

	res := m3Run(t, files,
		`{"op":"set_stale","target":"r-20260901-attention","reason":"引用卡已更新","initiator":"user"}`)
	requireError(t, res, E6)
	if len(res.Actions) != 0 {
		t.Fatalf("P-U 侧 🔴 必须零展开，实得 %d 条 action", len(res.Actions))
	}
	if got := readVaultFile(t, dir, relRecap); got != before {
		t.Fatal("E6 时目标文件字节必须逐字不变")
	}
}

// TestSetStale_TargetMustBeRecap：两键是综述专属——卡 / 笔记 / 原文 / 提案一律 E2、零展开。
func TestSetStale_TargetMustBeRecap(t *testing.T) {
	files := m4Files()
	for _, id := range []string{"k-20260901-attention", "n-20260901-attention",
		"s-20260901-attention", "p-20261017-001"} {
		res := m3RunAgent(t, files,
			fmt.Sprintf(`{"op":"set_stale","target":%q,"reason":"引用卡已更新"}`, id))
		requireError(t, res, E2)
		if len(res.Actions) != 0 {
			t.Fatalf("target=%s 必须零展开，实得 %+v", id, res.Actions)
		}
	}
	// 缺 target 同样 E2（落点无法定位）。
	res := m3RunAgent(t, files, `{"op":"set_stale","reason":"引用卡已更新"}`)
	requireError(t, res, E2)
}

// TestSetStale_ReasonClosedThreeValues：三值逐个可写；缺失与第四种取值 → E5、零展开、零写入。
func TestSetStale_ReasonClosedThreeValues(t *testing.T) {
	files := m4Files()
	for _, reason := range model.ValidStaleReasons() {
		res := m3RunAgent(t, files,
			fmt.Sprintf(`{"op":"set_stale","target":"r-20260901-attention","reason":%q}`, reason))
		if res.Failed() {
			t.Fatalf("合法 reason=%s 不得判 error：%v", reason, codes(res.Errors))
		}
		if len(res.Actions) != 1 {
			t.Fatalf("reason=%s 应展开恰一条 action：%+v", reason, res.Actions)
		}
	}
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)
	before := readVaultFile(t, dir, relRecap)
	for _, bad := range []string{`"引用卡已变更"`, `"stale"`, `""`} {
		res := m3RunAgent(t, files,
			`{"op":"set_stale","target":"r-20260901-attention","reason":`+bad+`}`)
		requireError(t, res, E5)
		if len(res.Actions) != 0 {
			t.Fatalf("reason=%s 必须零展开（本层不代入默认理由）：%+v", bad, res.Actions)
		}
		out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
		if len(out.Written) != 0 {
			t.Fatalf("reason=%s 必须零写入：%v", bad, out.Written)
		}
	}
	// 缺 reason 字段同样 E5。
	requireError(t, m3RunAgent(t, files,
		`{"op":"set_stale","target":"r-20260901-attention"}`), E5)
	if got := readVaultFile(t, dir, relRecap); got != before {
		t.Fatal("取值越界时目标文件字节必须逐字不变")
	}
}

// TestSetStale_BaseNotCoveringIsSkipped：**B3 不豁免**——base 未覆盖目标 → W6 + 恰一条
// skipped[]（复用封闭两值），零写入零字节改动，不自造第三种 kind。
func TestSetStale_BaseNotCoveringIsSkipped(t *testing.T) {
	files := m4Files()
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)
	before := readVaultFile(t, dir, relRecap)

	res := run(t, vault(t, files), `{"plan_version":1,"verb":"process","domain":"ai-infra",
 "reason":"base 未覆盖","requirement_ids":["EG-EDIT-04"],"base":{},"ops":[`+staleOpUpdated+`]}`)
	if res.Failed() {
		t.Fatalf("W6 是 warning，不得升 error：%v", codes(res.Errors))
	}
	requireWarning(t, res, W6)
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Written) != 0 {
		t.Fatalf("B3 跳过必须零写入：%v", out.Written)
	}
	if len(out.Skipped) != 1 {
		t.Fatalf("应产出恰一条 skipped：%+v", out.Skipped)
	}
	if out.Skipped[0].Kind != store.SkipFileChanged ||
		out.Skipped[0].Cause != "content_hash_mismatch" {
		t.Fatalf("跳过命名必须复用封闭两值（不自造第三种 kind）：%+v", out.Skipped[0])
	}
	if got := readVaultFile(t, dir, relRecap); got != before {
		t.Fatal("B3 跳过时目标文件字节必须逐字不变")
	}
}

// TestSetStale_StaleHashSkipsWithoutWrite：base 覆盖但文件在校验后又变了（hash 过期）——
// 由 store 返回 *SkipError{file_changed} 经 record 落进 skipped[]，零写入零字节改动。
func TestSetStale_StaleHashSkipsWithoutWrite(t *testing.T) {
	files := m4Files()
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)

	res := m3RunAgent(t, files, staleOpUpdated)
	if res.Failed() {
		t.Fatalf("校验期不应报错：%v", codes(res.Errors))
	}
	// 校验之后、执行之前，磁盘上的综述被别人改了：ExpectedHash 随即过期。
	drifted := recapM4("r-20260901-attention", "\nvendor_second_key: 磁盘已漂移")
	writeVault(t, dir, map[string]string{relRecap: drifted})

	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Written) != 0 {
		t.Fatalf("hash 过期必须零写入：%v", out.Written)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].Kind != store.SkipFileChanged {
		t.Fatalf("hash 过期必须落进 skipped[]（file_changed）：%+v", out.Skipped)
	}
	if len(out.Failures) != 0 {
		t.Fatalf("hash 过期不是失败：%+v", out.Failures)
	}
	if got := readVaultFile(t, dir, relRecap); got != drifted {
		t.Fatal("hash 过期时磁盘字节必须逐字保持漂移后的样子（零字节改动）")
	}
}

// TestSetStale_ExpectedHashAlwaysCarried：B3 不豁免的**结构性**判据——
// 展开出的 action 一定带 ExpectedHash（base 覆盖时非空），执行体不得绕开 e.expect(a)。
func TestSetStale_ExpectedHashAlwaysCarried(t *testing.T) {
	files := m4Files()
	res := m3RunAgent(t, files, staleOpUpdated)
	if len(res.Actions) != 1 {
		t.Fatalf("应展开恰一条 action：%+v", res.Actions)
	}
	a := res.Actions[0]
	if a.ExpectedHash == "" {
		t.Fatal("base 覆盖目标时 ExpectedHash 必须带下去（B3 不豁免）")
	}
	if a.ExpectedHash != store.ContentHash([]byte(files[relRecap])) {
		t.Fatalf("ExpectedHash 必须等于 base 里登记的 content_hash：%q", a.ExpectedHash)
	}
	if a.Path != relRecap {
		t.Fatalf("action 落点应为综述文件：%q", a.Path)
	}
}

// TestSetStale_WrittenKeySetIsExactlyTwo：写入键集合恰 `{stale, stale_reason}`——
// status / 删除维度 / 替代指针 / reviewed_at / updated_at 一格不许动。
func TestSetStale_WrittenKeySetIsExactlyTwo(t *testing.T) {
	files := m4Files()
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)

	res := m3RunAgent(t, files, staleOpUpdated)
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Written) != 1 {
		t.Fatalf("应恰写一份文件：%v / skipped=%+v", out.Written, out.Skipped)
	}
	after := readVaultFile(t, dir, relRecap)
	for _, forbidden := range []string{"status:", "deleted_at:", "deleted_reason:",
		"replaced_by:", model.FMKeyReviewedAt + ":"} {
		if strings.Contains(after, forbidden) {
			t.Fatalf("失准标记不得引入 %s：\n%s", forbidden, after)
		}
	}
	if !strings.Contains(after, "updated_at: '2026-09-01T10:00:00+08:00'\n") {
		t.Fatalf("updated_at 必须逐字不变（§9：不自动重算综述）：\n%s", after)
	}
	if !strings.Contains(after, "综述正文一个字节都不许动。\n") {
		t.Fatalf("正文必须逐字保留：\n%s", after)
	}
	if n := strings.Count(after, model.FMKeyStale+":"); n != 1 {
		t.Fatalf("stale 只许一行，实得 %d", n)
	}
}
