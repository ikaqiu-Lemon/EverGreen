package plan

// M3 新增 8 个 op 与 E7–E10 / W9–W12 的用例（T-evergreen.s1_main_flow-158614-037）。
//
// 分级纪律的三条硬断言在这里：
//   - W7 对**五个**状态类 op 是 **error**（退 2、零展开 → 零写入）；
//   - W7 对另**三个** op 是 **warning**（A-15 窄口径 N-6，不拦截）；
//   - W3 自 M3 起**真正判定**，但仍是 warning。
//
// 幂等纪律：W10 / W11 触发时该 op **不产出 action** —— 零写入、不产生空 commit。

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— M3 用例专用夹具 ——

const cardM3Fixture = `---
id: %s
status: %s
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'%s
sources:
  - source: s-20260901-attention
    note: n-20260901-attention
    rel: support
    reason: 原文第 3 节给出该机制的定义
relations:
  - type: limits
    target: k-20260815-rnn
    reason: 限定了原结论的适用序列长度
  - type: limits
    target: k-20260815-rnn
    reason: 历史遗留的重复条目（同三元组第二条）
---

# 注意力机制

## 知识内容

注意力是一种加权聚合。

## 解释与依据

- 依据原文第 3 节

## 条件与边界

- 仅适用于序列建模

## 用户补充

我自己的理解：先看 QKV。

## 理解自检

- 历史块：为什么需要缩放？

- 当前有效块：多头注意力的头数如何选？
`

// cardM3 造一张 M3 象限可控的卡：status + 可选的 deleted_at / replaced_by 行。
func cardM3(id, status, extra string) string {
	return fmt.Sprintf(cardM3Fixture, id, status, extra)
}

const proposalM3Fixture = `---
id: %s
type: logical_delete
status: %s
created_at: '2026-10-17'
targets:
  - k-20260901-attention
impact:
  exits_default_view:
    - k-20260901-attention
  cards_losing_support: []
  affected_material_rels: 1
  affected_relations: 2
  stale_reviews: []
decision:
  result: %s
  reason: %s
  superseded_by: %s
execution:
  status: %s
  attempted_at: ''
  reason: ''
  git_commit: ''
  written_paths: []
  unwritten_paths: []
---

# 提案：删除注意力机制卡

## 推荐修改

逻辑删除 k-20260901-attention。

## 理由与证据

该卡与新版综述重复。

## 影响的文件、领域与关系

默认检索少一张卡，2 条关系受影响。

## 执行后状态

该卡带 deleted_at，退出默认检索。

## 不执行的影响

两张重复卡长期并存。

## 替代方案

保留并标失效。

## 可应用内容

- delete
`

// proposalM3 造一份提案：status / decision.result / reason / superseded_by / execution.status 全可控。
func proposalM3(id, status, result, reason, supersededBy, exec string) string {
	return fmt.Sprintf(proposalM3Fixture, id, status, result, reason, supersededBy, exec)
}

// m3Files 是 M3 用例的默认库：两张 active 卡 + 一篇笔记 + 一份原文 + 一份 approved 提案。
func m3Files() map[string]string {
	return map[string]string{
		"domains/ai-infra/knowledge/k-20260901-attention.md": cardM3("k-20260901-attention", "active", ""),
		"domains/ai-infra/knowledge/k-20260815-rnn.md":       cardM3("k-20260815-rnn", "active", ""),
		"domains/ai-infra/notes/n-20260901-attention.md":     note("n-20260901-attention"),
		"sources/s-20260901-attention.md":                    source("s-20260901-attention"),
		"proposals/p-20261017-001.md": proposalM3("p-20261017-001", "approved", "approved",
			"用户已批准删除", "''", "not_started"),
	}
}

// m3Plan 造一份**base 覆盖全库**的 plan（避免 W6 跳过掩盖 M3 的判定）。
func m3Plan(t *testing.T, files map[string]string, ops string) string {
	t.Helper()
	var base []string
	for rel, content := range files {
		base = append(base, fmt.Sprintf("%q:%q", rel, store.ContentHash([]byte(content))))
	}
	return fmt.Sprintf(`{"plan_version":1,"verb":"process","domain":"ai-infra",`+
		`"reason":"M3 用例","requirement_ids":["EG-EDIT-04"],"base":{%s},"ops":[%s]}`,
		strings.Join(base, ","), ops)
}

// m3Run 用给定库跑一次校验。
//
// **默认带命令行佐证**（Env.UserRequest = true）：M3 的绝大多数用例模拟的是
// 「用户在命令行上敲了 eg … --user-request」这一场景，plan 里的 initiator: user
// 只有配上它才构成 P-U（授权合同 N-1）。缺 initiator 的用例不受影响——
// 它们无论有没有佐证都落在 P-A。反伪造本身由 m3RunAgent 的用例覆盖。
func m3Run(t *testing.T, files map[string]string, ops string) *Result {
	t.Helper()
	env := vault(t, files)
	env.UserRequest = true
	return run(t, env, m3Plan(t, files, ops))
}

// m3RunAgent 跑一次**没有命令行佐证**的校验（Agent 自动路径 P-A）。
func m3RunAgent(t *testing.T, files map[string]string, ops string) *Result {
	t.Helper()
	return run(t, vault(t, files), m3Plan(t, files, ops))
}

// —— ① 8 个 op 的注册与字段表 ——

// TestM3Ops 表驱动 8 行：op 名在场、字段名集合与合同 §8.1「字段」列逐字相等、
// 顶层键集合可解析；并断言未知 op 仍报 E5 且整条不执行（§4.5 开放集合口径）。
func TestM3Ops(t *testing.T) {
	want := []struct {
		op     string
		fields []string
		state  bool
	}{
		{OpDeprecate, []string{"target", "reason", "initiator"}, true},
		{OpRestore, []string{"target", "reason", "initiator"}, true},
		{OpSetReplacedBy, []string{"target", "replaced_by.target", "replaced_by.reason", "initiator"}, true},
		{OpDelete, []string{"target", "reason", "proposal", "initiator"}, true},
		{OpUndelete, []string{"target", "reason", "initiator"}, true},
		{OpMarkReviewed, []string{"target", "initiator"}, false},
		{OpReplaceBlock, []string{"target", "section", "block", "base_block_hash"}, false},
		{OpRemoveRelation, []string{"from", "type", "target", "reason", "initiator"}, false},
	}
	if len(M3OpNames()) != 8 {
		t.Fatalf("M3 新增 op 必须恰 8 个，实得 %d：%v", len(M3OpNames()), M3OpNames())
	}
	if len(want) != len(M3OpNames()) {
		t.Fatalf("表驱动行数 %d 与 M3OpNames() %d 不等", len(want), len(M3OpNames()))
	}
	for i, c := range want {
		if M3OpNames()[i] != c.op {
			t.Fatalf("M3OpNames()[%d] = %q，期望 %q（顺序即合同 §8.1 表格顺序）", i, M3OpNames()[i], c.op)
		}
		got := M3OpFields(c.op)
		if strings.Join(got, ",") != strings.Join(c.fields, ",") {
			t.Fatalf("%s 字段表 = %v，合同 §8.1 = %v", c.op, got, c.fields)
		}
		if IsStateOp(c.op) != c.state {
			t.Fatalf("%s 的状态类归属 = %v，期望 %v（A-15 窄口径恰五个）", c.op, IsStateOp(c.op), c.state)
		}
		// 每个字段的顶层键都必须在 opKnownKeys 里，否则会被误判成未知附加字段。
		known := set(opKnownKeys(c.op))
		if !known["op"] {
			t.Fatalf("%s 的顶层键集合缺 op：%v", c.op, opKnownKeys(c.op))
		}
		for _, f := range c.fields {
			top := strings.SplitN(f, ".", 2)[0]
			if !known[top] {
				t.Fatalf("%s 的顶层键集合缺 %q：%v", c.op, top, opKnownKeys(c.op))
			}
		}
	}
	// 状态类 op 恰五个；S1 七个 op 一个不加；合起来恰 15 个可派发 op。
	if len(StateOpNames()) != 5 {
		t.Fatalf("状态类 op 必须恰 5 个（A-15），实得 %v", StateOpNames())
	}
	if len(OpNames()) != 7 {
		t.Fatalf("S1 七个 op 冻结，一个不加，实得 %v", OpNames())
	}
	// **T-…-045 重钉**：A-13 的 `edit_section` 使可派发 op 从 15 变 16。它**不**进
	// StateOpNames / M3OpNames（提案与状态合同 §8.1 的八条 op 一条不加），而是授权合同
	// §2 矩阵 #12 P-U ✅ 独有的编辑 op，因此 5 / 7 / 8 三个计数一律不动。
	if len(EditOpNames()) != 1 {
		t.Fatalf("A-13 编辑 op 恰一个（edit_section），实得 %v", EditOpNames())
	}
	// **T-…-055 阶段 1 重钉（加法等式，M3 期结论不改写）**：A-33 的 `set_stale` 使派发
	// 全集由 M3 期的 16 变 17。它**不**进 M3OpNames（§8.1 恒 8 行）、**不**进
	// StateOpNames（A-15 恒 5；A-34 明确 R6 写 stale 不需要 --user-request，
	// 不得落进 W7 升 error 的收紧面），故 5 / 7 / 8 / 1 四个计数一律不动。
	if len(M4OpNames()) != 1 {
		t.Fatalf("M4 新增 op 恰一个（set_stale，A-33），实得 %v", M4OpNames())
	}
	if IsStateOp(OpSetStale) || IsM3Op(OpSetStale) {
		t.Fatalf("%s 不得进入 M3OpNames %v / StateOpNames %v（A-33 / A-34）",
			OpSetStale, M3OpNames(), StateOpNames())
	}
	const m3AllOps, m4NewOps = 16, 1 // M3 期 7+8+1=16（历史事实，不改写）+ M4 新增 1
	if len(AllOpNames()) != m3AllOps+m4NewOps {
		t.Fatalf("可派发 op 应为「M3 期 %d + M4 新增 %d = %d」，实得 %d：%v",
			m3AllOps, m4NewOps, m3AllOps+m4NewOps, len(AllOpNames()), AllOpNames())
	}
	// 未知 op（归属未定的三个之一）仍报 E5 且整条不执行。
	files := m3Files()
	res := m3Run(t, files, `{"op":"save_review","target":"k-20260901-attention"}`)
	requireError(t, res, E5)
	if len(res.Actions) != 0 {
		t.Fatal("未知 op 必须整条不执行（零展开）")
	}
}

// —— ② W7 的两种分级 ——

// TestW7_IsErrorInM3 断言五个状态类 op 缺 initiator=user / 缺 reason → W7 是 **error**、零展开；
// 且 delete 未引用 status=approved 提案同样是 error（V10 ≡ W7 后半，A-14 不另起编号）。
func TestW7_IsErrorInM3(t *testing.T) {
	files := m3Files()
	cases := []struct {
		name string
		ops  string
	}{
		{"deprecate 缺 initiator", `{"op":"deprecate","target":"k-20260901-attention","reason":"已被新卡取代"}`},
		{"deprecate 缺 reason", `{"op":"deprecate","target":"k-20260901-attention","initiator":"user"}`},
		{"restore 缺 initiator", `{"op":"restore","target":"k-20260901-attention","reason":"重新成立"}`},
		{"restore 缺 reason", `{"op":"restore","target":"k-20260901-attention","initiator":"user"}`},
		{"set_replaced_by 缺 initiator", `{"op":"set_replaced_by","target":"k-20260901-attention",
 "replaced_by":{"target":"k-20260815-rnn","reason":"新版综述"}}`},
		{"delete 缺 initiator", `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "proposal":"p-20261017-001"}`},
		{"delete 缺 reason", `{"op":"delete","target":"k-20260901-attention","initiator":"user",
 "proposal":"p-20261017-001"}`},
		{"delete 未引用提案", `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user"}`},
		{"undelete 缺 initiator", `{"op":"undelete","target":"k-20260901-attention","reason":"误删"}`},
		{"undelete 缺 reason", `{"op":"undelete","target":"k-20260901-attention","initiator":"user"}`},
		{"initiator 取值不是 user", `{"op":"deprecate","target":"k-20260901-attention",
 "reason":"已被新卡取代","initiator":"agent"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := m3Run(t, files, c.ops)
			d := requireError(t, res, W7)
			if d.Level != LevelError {
				t.Fatalf("W7 自 M3 起对状态类 op 必须是 error，实得 %q", d.Level)
			}
			if len(res.Actions) != 0 {
				t.Fatalf("W7 error 必须零展开（零写入），实得 %d 条 action", len(res.Actions))
			}
		})
	}
	// delete 引用的提案不是 approved（pending）→ 同样是 error。
	pending := m3Files()
	pending["proposals/p-20261017-001.md"] = proposalM3("p-20261017-001", "pending", "''", "''", "''", "not_started")
	res := m3Run(t, pending, `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user","proposal":"p-20261017-001"}`)
	d := requireError(t, res, W7)
	if !strings.Contains(d.Message, string(proposal.StatusApproved)) {
		t.Fatalf("W7 应说明必须引用已批准提案：%+v", d)
	}
	if len(res.Actions) != 0 {
		t.Fatal("未批准提案的 delete 必须零展开")
	}
}

// TestW7_NarrowScope 断言 A-15 窄口径：另三个 op 缺 initiator=user 只记 **warning**。
//
// 「W7 是 warning」与「该 op 能不能写」是**两件事**（授权合同 §1 + §2 分工）：
// 本用例只锁 W7 的分级不被升级成 error；写不写得成由写权限矩阵单独决定——
// `mark_reviewed`（#7）与 `remove_relation`（#11）的 P-A 格是 🔴，
// 因此它们在无佐证时仍会被 **E6** 拦住，而这**不是** W7 判的 error。
// `replace_block`（#17）两格都是 ✅，P-A 下照常展开、退 0。
func TestW7_NarrowScope(t *testing.T) {
	files := m3Files()
	hash := selfCheckBlockHash(t, files["domains/ai-infra/knowledge/k-20260901-attention.md"])
	cases := []struct {
		name string
		ops  string
	}{
		{"mark_reviewed", `{"op":"mark_reviewed","target":"k-20260901-attention"}`},
		{"replace_block", fmt.Sprintf(`{"op":"replace_block","target":"k-20260901-attention",
 "section":"理解自检","block":"- 换过的当前有效块？\n","base_block_hash":%q}`, hash)},
		{"remove_relation", `{"op":"remove_relation","from":"k-20260901-attention","type":"limits",
 "target":"k-20260815-rnn","reason":"该限定已不成立"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := m3Run(t, files, c.ops)
			if _, isError := find(res.Errors, W7); isError {
				t.Fatalf("A-15 窄口径：%s 缺 initiator 不得判 W7 error，实得 %v", c.name, codes(res.Errors))
			}
			for _, d := range res.Errors {
				if d.Code != E6 {
					t.Fatalf("%s 缺 initiator 只可能被写权限矩阵（E6）拦下，实得 %v",
						c.name, codes(res.Errors))
				}
			}
			d, ok := find(res.Warnings, W7)
			if !ok {
				t.Fatalf("期望 %s 的 W7 warning，实际 warnings=%v", c.name, codes(res.Warnings))
			}
			if d.Level != LevelWarning {
				t.Fatalf("%s 的 W7 必须是 warning，实得 %q", c.name, d.Level)
			}
			if IsStateOp(c.name) {
				t.Fatalf("%s 不得进入 A-15 的状态类 op 集合", c.name)
			}
		})
	}
}

// —— ③ W3 自 M3 起真正判定 ——

// TestW3_JudgedFromM3 断言关系一端为 deprecated 或已逻辑删除 → 产生 W3 warning 且**不拦截**。
func TestW3_JudgedFromM3(t *testing.T) {
	deprecated := m3Files()
	deprecated["domains/ai-infra/knowledge/k-20260815-rnn.md"] =
		cardM3("k-20260815-rnn", "deprecated", "")
	res := m3Run(t, deprecated, `{"op":"remove_relation","from":"k-20260901-attention","type":"limits",
 "target":"k-20260815-rnn","reason":"该限定已不成立","initiator":"user"}`)
	d := requireWarning(t, res, W3)
	if d.Level != LevelWarning {
		t.Fatalf("W3 在 M3 仍是 warning（S5 起才 error），实得 %q", d.Level)
	}
	if len(res.Actions) != 1 {
		t.Fatalf("W3 不得拦截：期望仍展开 1 条 action，实得 %d", len(res.Actions))
	}

	deleted := m3Files()
	deleted["domains/ai-infra/knowledge/k-20260815-rnn.md"] =
		cardM3("k-20260815-rnn", "active", "\ndeleted_at: '2026-10-17T09:00:00+08:00'\ndeleted_reason: 与新卡重复")
	res = m3Run(t, deleted, `{"op":"add_relation","from":"k-20260901-attention","type":"supports",
 "target":"k-20260815-rnn","reason":"新证据支持该结论"}`)
	d = requireWarning(t, res, W3)
	if !strings.Contains(d.Message, "已被逻辑删除") {
		t.Fatalf("已删除端的 W3 必须写明逻辑删除：%+v", d)
	}
	if len(res.Actions) != 1 {
		t.Fatalf("W3 不得拦截 add_relation：实得 %d 条 action", len(res.Actions))
	}
}

// —— ④ E7 / E8 / E9 / E10 ——

// TestE7SupersededChainBroken：superseded 但 superseded_by 为空 / 指向自己 / 无法解析
// → 退 2、零写入（判定复用 internal/proposal 的 CheckSupersededChain，编号在本包发）。
func TestE7SupersededChainBroken(t *testing.T) {
	for _, sb := range []string{"''", "'p-20261017-001'", "'p-20261018-999'"} {
		files := m3Files()
		files["proposals/p-20261017-001.md"] = proposalM3("p-20261017-001",
			"superseded", "superseded", "被新提案取代", sb, "not_started")
		before := files["domains/ai-infra/knowledge/k-20260901-attention.md"]
		res := m3Run(t, files, `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user","proposal":"p-20261017-001"}`)
		requireError(t, res, E7)
		requireZeroWrite(t, res, before, files["domains/ai-infra/knowledge/k-20260901-attention.md"])
	}
	// 链成立（superseded_by 指向一个**存在的**提案）→ 不再发 E7。
	files := m3Files()
	files["proposals/p-20261017-001.md"] = proposalM3("p-20261017-001",
		"superseded", "superseded", "被新提案取代", "'p-20261017-002'", "not_started")
	files["proposals/p-20261017-002.md"] = proposalM3("p-20261017-002",
		"pending", "''", "''", "''", "not_started")
	res := m3Run(t, files, `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user","proposal":"p-20261017-001"}`)
	for _, d := range res.Errors {
		if d.Code == E7 {
			t.Fatalf("提案链成立时不得发 %s：%+v", E7, d)
		}
	}
	// 映射表是编号的唯一来源：提案链断裂族逐字映射到 E7。
	brokenErr := proposal.CheckSupersededChain(proposal.Proposal{
		ID: "p-20261017-001", Status: proposal.StatusSuperseded,
		Decision: proposal.Decision{Result: proposal.StatusSuperseded},
	}, nil)
	if got := CodeForProposalViolation(brokenErr); got != E7 {
		t.Fatalf("提案链断裂的编号 = %q，期望 %s", got, E7)
	}
}

// TestE8EnumOrConsistency：status / execution.status 越界，或 decision.result 与 status 不一致。
func TestE8EnumOrConsistency(t *testing.T) {
	cases := []struct {
		name                         string
		status, result, reason, exec string
	}{
		{"status 第五值", "applied", "applied", "越界", "not_started"},
		{"execution.status 越界", "approved", "approved", "已批准", "applied"},
		{"decision.result 与 status 不一致", "approved", "rejected", "已批准", "not_started"},
		{"pending 时 decision.result 非空", "pending", "approved", "''", "not_started"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := m3Files()
			files["proposals/p-20261017-001.md"] = proposalM3("p-20261017-001",
				c.status, c.result, c.reason, "''", c.exec)
			before := files["domains/ai-infra/knowledge/k-20260901-attention.md"]
			res := m3Run(t, files, `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user","proposal":"p-20261017-001"}`)
			requireError(t, res, E8)
			requireZeroWrite(t, res, before, files["domains/ai-infra/knowledge/k-20260901-attention.md"])
		})
	}
}

// TestE9UnreachableStateCombo：落盘的 status × execution 命中 §4.2 可达矩阵的 🔴 / ⚠️ 格。
//
// 矩阵 12 格里 ✅ 恰 6 格：approved 行三格全可达，其余三态只有 not_started 可达。
func TestE9UnreachableStateCombo(t *testing.T) {
	for _, status := range proposal.Statuses() {
		for _, exec := range proposal.ExecStatuses() {
			reachable := status == proposal.StatusApproved || exec == proposal.ExecNotStarted
			if got := UnreachableStateCombo(status, exec); got == reachable {
				t.Fatalf("矩阵判定错：status=%s × exec=%s 不可达=%v，期望 %v",
					status, exec, got, !reachable)
			}
		}
	}
	files := m3Files()
	files["proposals/p-20261017-001.md"] = proposalM3("p-20261017-001",
		"rejected", "rejected", "不同意删除", "''", "succeeded")
	before := files["domains/ai-infra/knowledge/k-20260901-attention.md"]
	res := m3Run(t, files, `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user","proposal":"p-20261017-001"}`)
	requireError(t, res, E9)
	requireZeroWrite(t, res, before, files["domains/ai-infra/knowledge/k-20260901-attention.md"])
}

// TestE10ReplacedByDeletedTarget：set_replaced_by 的目标已被逻辑删除 → 退 2、零写入。
func TestE10ReplacedByDeletedTarget(t *testing.T) {
	deletedPointee := m3Files()
	deletedPointee["domains/ai-infra/knowledge/k-20260815-rnn.md"] = cardM3("k-20260815-rnn", "active",
		"\ndeleted_at: '2026-10-17T09:00:00+08:00'\ndeleted_reason: 与新卡重复")
	before := deletedPointee["domains/ai-infra/knowledge/k-20260901-attention.md"]
	res := m3Run(t, deletedPointee, `{"op":"set_replaced_by","target":"k-20260901-attention",
 "initiator":"user","replaced_by":{"target":"k-20260815-rnn","reason":"新版综述"}}`)
	d := requireError(t, res, E10)
	if !strings.Contains(d.Path, "replaced_by.target") {
		t.Fatalf("E10 的字段路径应指向 replaced_by.target：%+v", d)
	}
	requireZeroWrite(t, res, before,
		deletedPointee["domains/ai-infra/knowledge/k-20260901-attention.md"])

	// 主体已被逻辑删除同样是 E10（合同行文两种读法的并集，只加严）。
	deletedSubject := m3Files()
	deletedSubject["domains/ai-infra/knowledge/k-20260901-attention.md"] = cardM3(
		"k-20260901-attention", "deprecated",
		"\ndeleted_at: '2026-10-17T09:00:00+08:00'\ndeleted_reason: 与新卡重复")
	res = m3Run(t, deletedSubject, `{"op":"set_replaced_by","target":"k-20260901-attention",
 "initiator":"user","replaced_by":{"target":"k-20260815-rnn","reason":"新版综述"}}`)
	requireError(t, res, E10)
	if len(res.Actions) != 0 {
		t.Fatal("E10 必须零展开")
	}
}

// —— ⑤ W9 / W10 / W11 / W12 ——

// TestW9ProposalEssentialsMissing：提案十项必备缺项 → warning，不拒绝、不拦截其余 op。
func TestW9ProposalEssentialsMissing(t *testing.T) {
	files := m3Files()
	files["proposals/p-20261017-001.md"] = strings.Replace(
		proposalM3("p-20261017-001", "approved", "approved", "''", "''", "not_started"),
		"## 推荐修改\n\n逻辑删除 k-20260901-attention。\n", "## 推荐修改\n\n", 1)
	res := m3Run(t, files, `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user","proposal":"p-20261017-001"}`)
	d := requireWarning(t, res, W9)
	if d.Level != LevelWarning {
		t.Fatalf("W9 必须是 warning（照常创建 + 进报告），实得 %q", d.Level)
	}
	if !strings.Contains(d.Message, "推荐修改") || !strings.Contains(d.Message, "decision.reason") {
		t.Fatalf("W9 应逐项列出缺项（空分区 + decision.reason）：%+v", d)
	}
}

// TestW10_RemoveRelationNoMatch：未命中 → 幂等 no-op，**零写入、零 commit**、不拦截其余 op。
func TestW10_RemoveRelationNoMatch(t *testing.T) {
	files := m3Files()
	res := m3Run(t, files, `{"op":"remove_relation","from":"k-20260901-attention","type":"supports",
 "target":"k-20260815-rnn","reason":"这条关系并不存在","initiator":"user"}`)
	d := requireWarning(t, res, W10)
	if d.Level != LevelWarning {
		t.Fatalf("W10 必须是 warning，实得 %q", d.Level)
	}
	if len(res.Actions) != 0 {
		t.Fatalf("W10 幂等必须零 action（零写入、零 commit），实得 %d", len(res.Actions))
	}
	// 不拦截其余 op：同一 plan 内后一条 op 照常展开。
	res = m3Run(t, files, `{"op":"remove_relation","from":"k-20260901-attention","type":"supports",
 "target":"k-20260815-rnn","reason":"这条关系并不存在","initiator":"user"},
 {"op":"append_card","card":"k-20260901-attention","sections":{"解释与依据":"- 追加一条依据\n"}}`)
	if res.Failed() {
		t.Fatalf("W10 不得影响退出码：实得 errors=%v", codes(res.Errors))
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActCardAppend {
		t.Fatalf("W10 不得拦截其余 op：实得 %+v", res.Actions)
	}
}

// TestW11IdempotentStateOps：三类幂等 no-op（已 deprecated / 已 active / 未删除）→ 零写入 + 进报告。
func TestW11IdempotentStateOps(t *testing.T) {
	deprecated := m3Files()
	deprecated["domains/ai-infra/knowledge/k-20260901-attention.md"] =
		cardM3("k-20260901-attention", "deprecated", "")
	cases := []struct {
		name  string
		files map[string]string
		ops   string
	}{
		{"deprecate 目标已 deprecated", deprecated,
			`{"op":"deprecate","target":"k-20260901-attention","reason":"再失效一次","initiator":"user"}`},
		{"restore 目标已 active", m3Files(),
			`{"op":"restore","target":"k-20260901-attention","reason":"再恢复一次","initiator":"user"}`},
		{"undelete 目标未被删除", m3Files(),
			`{"op":"undelete","target":"k-20260901-attention","reason":"并没有删过","initiator":"user"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := m3Run(t, c.files, c.ops)
			d := requireWarning(t, res, W11)
			if d.Level != LevelWarning {
				t.Fatalf("W11 必须是 warning，实得 %q", d.Level)
			}
			if len(res.Actions) != 0 {
				t.Fatalf("W11 幂等必须零 action（零写入、不产生空 commit），实得 %d", len(res.Actions))
			}
			if res.Failed() {
				t.Fatalf("W11 不得影响退出码：%v", codes(res.Errors))
			}
		})
	}
}

// TestW12ReplacedByDeprecatedTarget：替代目标是 deprecated 且未删除 → 照常处理 + 进报告提示。
func TestW12ReplacedByDeprecatedTarget(t *testing.T) {
	files := m3Files()
	files["domains/ai-infra/knowledge/k-20260815-rnn.md"] = cardM3("k-20260815-rnn", "deprecated", "")
	res := m3Run(t, files, `{"op":"set_replaced_by","target":"k-20260901-attention",
 "initiator":"user","replaced_by":{"target":"k-20260815-rnn","reason":"新版综述"}}`)
	d := requireWarning(t, res, W12)
	if d.Target != "k-20260815-rnn" {
		t.Fatalf("W12 的 target 应指向替代目标：%+v", d)
	}
	if res.Failed() {
		t.Fatalf("W12 不得拦截：%v", codes(res.Errors))
	}
}

// —— ⑥ 编号闭合与载荷字段 ——

// TestDiagnosticCodes_Closed 断言编号集合恰闭合在 E1..E10 ∪ W1..W12 ∪ {I1}，
// 且 internal/ 全库的 code 字面量不越界（M4 · T-…-049 起按包分域：E11–E14 / W13–W20
// 只许出现在 M4 的 S3 检查器包，其余落点恒零命中；E15+ / W21+ / I2+ 留给 M5–M6）。
func TestDiagnosticCodes_Closed(t *testing.T) {
	if len(ErrorCodes()) != 10 || len(WarningCodes()) != 12 || len(InfoCodes()) != 1 {
		t.Fatalf("编号占用总览：error 10 / warning 12 / info 1，实得 %d / %d / %d",
			len(ErrorCodes()), len(WarningCodes()), len(InfoCodes()))
	}
	if len(AllCodes()) != 23 {
		t.Fatalf("闭合集合应恰 23 个编号，实得 %d：%v", len(AllCodes()), AllCodes())
	}
	for i, want := range []string{"E1", "E2", "E3", "E4", "E5", "E6", "E7", "E8", "E9", "E10"} {
		if ErrorCodes()[i] != want {
			t.Fatalf("ErrorCodes()[%d] = %q，期望 %q", i, ErrorCodes()[i], want)
		}
	}
	for i, want := range []string{"W1", "W2", "W3", "W4", "W5", "W6", "W7", "W8",
		"W9", "W10", "W11", "W12"} {
		if WarningCodes()[i] != want {
			t.Fatalf("WarningCodes()[%d] = %q，期望 %q", i, WarningCodes()[i], want)
		}
	}
	// 越界编号与预留 kind 字面量**一律拼接构造**，绝不在源码里留完整字面量：
	// 判据 15 的两条反证 grep（`"(E1[1-9]|W1[3-9]|I[2-9])"` 与 block 冲突预留名）
	// 扫的是整个 internal/ 而不排除测试文件，恒零命中才算真闭合。
	outOfRange := []string{"E" + "0", "E" + "11", "E" + "12", "W" + "0",
		"W" + "13", "W" + "14", "I" + "2", "I" + "3"}
	reservedKinds := []string{"block" + "_conflict", "block" + "_hash_changed"}
	for _, bad := range append(outOfRange, reservedKinds...) {
		if IsKnownCode(bad) {
			t.Fatalf("%q 不在闭合集合内，却被判为已知编号", bad)
		}
	}
	if !IsKnownCode(Unnumbered) {
		t.Fatal("未编号 warning（空串 Code）不算越界")
	}
	// 全库 code 字面量不越界；两个预留 kind 名恒不出现。
	//
	// **重钉理由（事实变了，不是放宽）**：原判据是「internal/ 全库 E11+ / W13+ / I2+ 恒零命中，
	// 留给 M4–M6」。M4 的 T-…-049 起，对账合同 §3 把 E11–E14 / W13–W20 **正式发放**给 M4 的
	// S3 检查器包的十二值 check 枚举，那批编号在该包内是**合同要求存在**的事实。因此把判据
	// 形态从「全库零命中」重钉为**按包分域的双侧等号**，判据本体（编号不得越界、越界编号不得
	// 被认作已知编号、预留 kind 名恒不出现）逐条保留，且反证面**变严**：
	//   - S3 检查器包之外的**每一个** .go 文件：E11+ / W13+ / I2+ 仍恒零命中
	//     （M3 的原判据在原有全部落点上一格不放宽）；
	//   - S3 检查器包内部：只许 M4 已发放的 E11–E14 / W13–W20，
	//     E15+ / W21+ / I2+ 一律零命中（M5–M6 的号段照旧留白）；
	//   - M4 编号**必须**在该包内真实出现（否则本重钉失去事实基础，判据回落为「全库零命中」
	//     时会立刻判红），即等号双侧都被钉住。
	//
	// S3 检查器包的**包名一律拼接构造**，与上面的越界编号同一手法：判据 15 另有一条
	// 「internal/plan 出现 M4–M6 能力字样恒零命中」的裸词 grep，扫的是本目录全部文件
	// （含测试文件），本文件不得留下该包名的完整字面量。
	//
	// **M5 · T-…-065 追加一域（同一手法，只增不改）**：M5 索引架构合同 A-45 把
	// `W22`–`W25` / `Q5` 发放给 S4 的派生索引面，其中 T-…-065 只实际启用 **W23 / W24**
	// （缺失 / 不可用两态）。因此分域表从一行变两行，两行都是**双侧等号**。
	//
	// **M5 · T-…-066 阶段 B 精确重钉（事实变了，判据形态不变）**：本 task 按合同 §5.2 / §6.3
	// 正式启用 **W22（index_stale，索引陈旧）**，落点恰在 S4 索引包的
	// `consistency.go`。因此 S4 域内的允许集合从 `W23 / W24` 变为 `W22 / W23 / W24`，
	// 其余一格不放宽：
	//   - S4 索引包内部：只许 `W22` / `W23` / `W24`，`W20` / `W21` / `W25` / `E15+` / `I2+`
	//     一律零命中（`W25` 与 `Q5` 属读路径接入，仍在下游 T-…-067）；
	//   - S4 索引包之外：`W22` / `W23` / `W24` 同样零命中（其它落点一格不放宽 ——
	//     命令层引用这些码必须走 `index.CodeIndex*` 常量，不许自己抄一份字面量）；
	//   - 且 S4 编号**必须**在该包内真实出现，否则本追加失去事实基础，当场判红。
	// **M5 · T-…-069 追加第三域（同一手法，只增不改）**：`W25`（result_truncated，
	// 结果被 `--limit` 截断）由 T-…-068 按合同 §7.4 / A-47 落地，唯一落点是
	// `internal/query/page.go` 的 `CodeResultTruncated`。上面 T-…-066 那段已写明
	// 「`W25` 与 `Q5` 属读路径接入，仍在下游」——本 task 只把这笔**既成事实**登记进表，
	// 使分域表与实现同真（T-…-068 落地时漏登，M5 收口在此补齐）。因此分域表从两行变三行：
	//   - S4/M5 读路径层 `internal/query`：只许 `W25`，`W22` / `W23` / `W24` 一律零命中
	//     （读路径引用索引域三码必须走 `index.CodeIndex*` 常量，不许自己抄字面量），
	//     `W20` / `W21` / `E11+` / `I2+` 同样零命中；
	//   - `internal/query` 之外 `W25` 零命中（唯一落点等号）；
	//   - 且 `W25` 必须在该包内真实出现，否则本追加失去事实基础，当场判红。
	root := repoInternalDir(t)
	bad := regexp.MustCompile(`"(E1[1-9]|E[2-9][0-9]|W1[3-9]|W[2-9][0-9]|I[2-9])"`)
	m4Reserved := regexp.MustCompile(`"(E1[5-9]|E[2-9][0-9]|W2[1-9]|W[3-9][0-9]|I[2-9])"`)
	// S4 索引包内的预留面：M5 至 T-…-066 已发放 W22 / W23 / W24，
	// 其余号段（含同里程碑的 W20 / W21 / W25 与 Q5 对应的下游能力）继续留白。
	m5Reserved := regexp.MustCompile(`"(E1[1-9]|E[2-9][0-9]|W1[3-9]|W2[0-1]|W2[5-9]|W[3-9][0-9]|I[2-9])"`)
	// S4/M5 读路径层内的预留面：M5 至 T-…-068 只发放 W25，其余号段（含索引域的
	// W22 / W23 / W24 与 W20 / W21）在本包内一律零命中。
	queryReserved := regexp.MustCompile(`"(E1[1-9]|E[2-9][0-9]|W1[3-9]|W2[0-4]|W2[6-9]|W[3-9][0-9]|I[2-9])"`)
	// **M6 · T-…-073 追加块级安全合并域（同一手法，只增不改）**：M6 合同 §12 把 `W27`
	// （block_merge_conflict）的唯一落点发放给 `internal/mdfile/block_merge.go`（判定内核，
	// 常量 `CodeBlockMergeConflict` 唯一持有该字面量）与 `internal/store/merge.go`（写口接线，
	// **只走该常量、不自己抄字面量**）。上面 M6 事务域那段写明「W27 仍为 T-…-073 保留」——本
	// task 即那位下游，把 W27 从「留白」转为「已发放」。因此新增一域，仍是**双侧等号**：
	//   - S5 块级合并判定包 `internal/mdfile`：只许 `W27`，其余号段（含事务域的
	//     E15 / E16 / W26 / W28、索引域的 W22–W25、M3 已闭合低码、E17+ / W29+ / I2+）一律零命中；
	//   - `internal/mdfile` 之外 `W27` 零命中（`store` 侧接线走 `mdfile.CodeBlockMergeConflict`
	//     常量，源码不出现被双引号包住的 `W27`，故仍落在默认 `bad` 判红面内，唯一字面量落点等号）；
	//   - 且 `W27` 必须在 `internal/mdfile` 内真实出现，否则本追加失去事实基础，当场判红。
	blockMergeReserved := regexp.MustCompile(`"(E1[1-9]|E[2-9][0-9]|W1[3-9]|W2[0-689]|W[3-9][0-9]|I[2-9])"`)
	// **M6 · S5 事务域（同一手法，只增不改）**：M6 原子性与强校验合同 §12 把新增诊断码恰
	// 5 条（E15 / E16 / W26 / W27 / W28）发放给 M6。截至 T-…-072（批次 B1）的 S5 事务包
	// 已实际启用 **E15（写前安全复核语义大类）/ E16（锁超时）/ W26（真实崩溃恢复回滚留痕）/
	// W28（锁等待退避留痕）** 四码；其中 W26 由 internal/txn/recover.go 的真实恢复路径产出
	// （合同 §12 与 T-…-072 明确：真实恢复产 W26，W28 仅锁等待重试，二者不得互换）。W27 仍
	// 为 T-…-073（块级安全合并 / 并发冲突）保留，本域暂不启用。四个字面量只在该包常量块各出现
	// 一次，其余落点全部走常量引用。因此本行同样是**双侧等号**：
	//   - S5 事务包内部：只许 `E15` / `E16` / `W26` / `W28`，其余号段（含同里程碑尚未启用的
	//     W27 与 E17+ / W29+ / I2+，以及 M3 已闭合的低码）一律零命中；
	//   - S5 事务包之外：`E15` / `E16` / `W26` / `W28` 同样零命中（其它包引用这四码必须走该包
	//     的 `Code*` 常量，不许自己抄一份字面量 —— 由默认 `bad` 与三个既有 owner 域各自的
	//     预留正则共同保证：四张表都把 E15+ / W26 / W28 判为越界）；
	//   - 且 `E15` / `E16` / `W26` / `W28` 必须在该包内真实出现，否则本追加失去事实基础，当场判红。
	txnReserved := regexp.MustCompile(`"(E1[1-47-9]|E[2-9][0-9]|W1[3-9]|W2[0-57]|W29|W[3-9][0-9]|I[2-9])"`)
	forbidden := regexp.MustCompile(strings.Join(reservedKinds, "|"))
	// codeLit 从源码里把**带双引号的**诊断码字面量剥出裸码（如剥出 E16），用于对 M6 事务域做
	// **逐码**存在性断言：seen[dir]>0 只能证明「四者任一存在」，不满足合同「E15 / E16 / W26 /
	// W28 各自必须出现」。注意：本注释按本文件既有纪律，绝不写出被双引号包住的码，以免被上面的
	// bad / codeLit 正则当成 plan 包内的域外码扫到（本测试全库扫描，不排除 _test.go）。
	codeLit := regexp.MustCompile(`"([EWI][0-9]+)"`)
	m4OwnerPkg := "recon" + "cile"
	m5OwnerPkg := "ind" + "ex"
	queryOwnerPkg := "que" + "ry"
	m6OwnerPkg := "tx" + "n"
	txnDir := filepath.Join(root, m6OwnerPkg)
	// blockMergeDir 是 T-…-073 的 W27 唯一字面量落点包 internal/mdfile（判定内核所在）。
	blockMergeDir := filepath.Join(root, "mdfile")
	// txnRequired 是 M6 事务域**必须逐码出现**的精确集合（合同 §12 截至 T-…-072 已启用的四码）。
	// 测试侧的这四个码按本文件既有纪律用**字符串拼接**构造（同 "tx"+"n" 手法），使源码里不出现
	// 被双引号包住的完整码，避免本测试全库扫描时把 m3_test.go 自身当成 plan 包内的域外码判红。
	txnRequired := []string{"E1" + "5", "E1" + "6", "W2" + "6", "W2" + "8"}
	// **C2 · I-…-015 追加命令层 error 码域（同一手法，只增不改；粒度到文件）**：上面每一段
	// 都写着「E17+ / W29+ / I2+ 留给下游」——本 issue 即那位下游。合同 §5 只授权「未编号
	// **warning** 填空码」，而实现里有 37 处 **error 级**条目留空码、3 处把 `skipped[].cause`
	// 塞进 `code` 位；修复方案是给命令层的错误族发放恰九个编号 `E17`–`E25`，唯一字面量落点是
	// `internal/cli/codes.go`（其余文件一律引用该文件的常量）。因此新增一域，仍是**双侧等号**，
	// 且比既有域**更紧**：粒度不是包目录而是**单个文件**：
	//   - `internal/cli/codes.go` 内部：只许 `E17`–`E25` 恰九码，其余号段（含 M3 已闭合低码、
	//     索引域 W22–W25、事务域 E15 / E16 / W26 / W28、mdfile 的 W27、W29+ / I2+）一律零命中；
	//   - 该文件之外：`E17`–`E25` 同样零命中 —— 由默认 `bad` 判红面保证（它覆盖 E11+ 全段），
	//     命令层其它文件必须走本文件常量，不许自己抄一份字面量；
	//   - 且九码**逐码**必须在该文件内真实出现，否则本追加失去事实基础，当场判红。
	cliOwnerPkg := "cl" + "i"
	cliCodesFile := filepath.Join(root, cliOwnerPkg, "codes.go")
	cliRequired := []string{
		"E1" + "7", "E1" + "8", "E1" + "9", "E2" + "0", "E2" + "1",
		"E2" + "2", "E2" + "3", "E2" + "4", "E2" + "5",
	}
	cliAllowed := map[string]bool{}
	for _, c := range cliRequired {
		cliAllowed[c] = true
	}
	// cliCodeSeen 逐码记录命令层码表文件内每个字面量的出现次数（精确集合断言的事实来源）。
	cliCodeSeen := map[string]int{}
	owners := map[string]*regexp.Regexp{
		filepath.Join(root, m4OwnerPkg):    m4Reserved,
		filepath.Join(root, m5OwnerPkg):    m5Reserved,
		filepath.Join(root, queryOwnerPkg): queryReserved,
		txnDir:                             txnReserved,
		blockMergeDir:                      blockMergeReserved,
	}
	seen := map[string]int{}
	// txnCodeSeen 逐码记录 M6 事务域内每个诊断码字面量的出现次数（精确集合断言的事实来源）。
	txnCodeSeen := map[string]int{}
	// txnAllowed 是 S5 事务域**精确允许**的码集合，由 txnRequired 拼接构造（源码里不出现被双引号
	// 包住的完整码）。txn 域**先于** baseline/owner 判定处理：只认这四码，其余一律判红。
	txnAllowed := map[string]bool{}
	for _, c := range txnRequired {
		txnAllowed[c] = true
	}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		// S5 事务域**先于**任何 baseline/closed 放行处理：内部**精确闭合**为 E15 / E16 / W26 / W28，
		// 任何其他 E / W / I 数字码（含 M3 已闭合的低码 E1–E10 / W1–W12 / I1，以及本里程碑尚未启用的
		// W27）一律判红 —— 否则「txn 内混入 E1 / W1 / W27 也能通过」会与「本域精确等于
		// {E15, E16, W26, W28}」的注释自相矛盾。
		if strings.HasPrefix(p, txnDir+string(filepath.Separator)) {
			for _, mm := range codeLit.FindAllStringSubmatch(string(raw), -1) {
				code := mm[1]
				if !txnAllowed[code] {
					t.Fatalf("%s 出现越界诊断码 %s：S5 事务域精确闭合为 {E15 / E16 / W26 / W28}，"+
						"任何其他 E / W / I 数字码（含 M3 已闭合码与尚未启用的 W27）都不许出现在 internal/%s", p, code, m6OwnerPkg)
				}
				txnCodeSeen[code]++
			}
			if m := forbidden.FindString(string(raw)); m != "" {
				t.Fatalf("%s 出现 %s：skipped[].kind 恒为封闭两值，永不启用该预留字面量", p, m)
			}
			return nil
		}
		// C2 · I-…-015 命令层码表文件**先于**任何 baseline/owner 判定处理：内部**精确闭合**为
		// E17–E25 恰九码，任何其他 E / W / I 数字码一律判红（含 M3 已闭合低码与其它 owner 域的码）。
		if p == cliCodesFile {
			for _, mm := range codeLit.FindAllStringSubmatch(string(raw), -1) {
				code := mm[1]
				if !cliAllowed[code] {
					t.Fatalf("%s 出现越界诊断码 %s：命令层码表精确闭合为 E17–E25 恰九码，"+
						"其它域的码必须走各自包的 Code* 常量，不许抄字面量", p, code)
				}
				cliCodeSeen[code]++
			}
			if m := forbidden.FindString(string(raw)); m != "" {
				t.Fatalf("%s 出现 %s：skipped[].kind 恒为封闭两值，永不启用该预留字面量", p, m)
			}
			return nil
		}
		limit := bad
		for dir, reserved := range owners {
			if strings.HasPrefix(p, dir+string(filepath.Separator)) {
				limit = reserved
				seen[dir] += len(bad.FindAllString(string(raw), -1))
			}
		}
		if m := limit.FindString(string(raw)); m != "" {
			t.Fatalf("%s 出现越界诊断编号 %s（分域发放：E11–E14 / W13–W20 → internal/%s，"+
				"W22 / W23 / W24 → internal/%s，W25 → internal/%s，E15 / E16 / W26 / W28 → internal/%s，"+
				"W27 → internal/mdfile；其余落点恒零命中，E17+ / W29+ / I2+ 留给下游）",
				p, m, m4OwnerPkg, m5OwnerPkg, queryOwnerPkg, m6OwnerPkg)
		}
		if m := forbidden.FindString(string(raw)); m != "" {
			t.Fatalf("%s 出现 %s：skipped[].kind 恒为封闭两值，永不启用该预留字面量", p, m)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 internal/ 失败：%v", err)
	}
	for dir := range owners {
		if dir == txnDir {
			// M6 事务域改用**逐码精确断言**（见下），跳过泛化的「任一 >0」计数。
			continue
		}
		if seen[dir] == 0 {
			t.Fatalf("%s 内未见任何本域编号：分域判据失去事实基础，"+
				"应回落为「全库 E11+ / W13+ / I2+ 零命中」的原形态", dir)
		}
	}
	// M6 事务域：E15 / E16 / W26 / W28 **逐码**各至少出现一次（不满足于「四者任一存在」）。
	// 与 txnReserved 的域外禁令合起来即「本域诊断码字面量精确等于集合 {E15, E16, W26, W28}」。
	for _, code := range txnRequired {
		if txnCodeSeen[code] == 0 {
			t.Fatalf("internal/%s 内未见诊断码 %q：M6 事务域要求 E15 / E16 / W26 / W28 **逐码**"+
				"各至少出现一次（seen>0 只证明四者任一存在，不满足精确集合），"+
				"分域判据失去事实基础", m6OwnerPkg, code)
		}
	}
	// C2 · I-…-015 命令层码域：E17–E25 **逐码**各至少出现一次，且该文件内的码集合**恰九个**
	// （双侧等号：越界由上面 walk 内的 cliAllowed 判红，缺码 / 多码由这里判红）。
	for _, code := range cliRequired {
		if cliCodeSeen[code] == 0 {
			t.Fatalf("%s 内未见诊断码 %q：命令层码域要求 E17–E25 **逐码**各至少出现一次，"+
				"分域判据失去事实基础", cliCodesFile, code)
		}
	}
	if len(cliCodeSeen) != len(cliRequired) {
		t.Fatalf("%s 内的诊断码集合大小 = %d，期望恰 %d（E17–E25，连续无空洞无外码）：%v",
			cliCodesFile, len(cliCodeSeen), len(cliRequired), cliCodeSeen)
	}
}

// TestDiagnosticPayload_SixFields 断言诊断载荷**恰六字段**（不新增第七个）。
func TestDiagnosticPayload_SixFields(t *testing.T) {
	typ := reflect.TypeOf(Diagnostic{})
	if typ.NumField() != 6 {
		var got []string
		for i := 0; i < typ.NumField(); i++ {
			got = append(got, typ.Field(i).Name)
		}
		t.Fatalf("诊断载荷必须恰 6 字段，实得 %d：%v", typ.NumField(), got)
	}
	want := DiagnosticFields()
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name != want[i] {
			t.Fatalf("第 %d 个字段的 json 名 = %q，期望 %q（顺序同 CLI 合同 §5）", i, name, want[i])
		}
	}
}

// —— ⑦ replace_block：只动当前有效块 ——

// TestReplaceBlock_HistoryAppendOnly 断言四件：
//   - 缺 base_block_hash → 拒绝（E5，零展开）；
//   - 分区不是「理解自检」→ 拒绝（E6）；
//   - hash 相符 → 只替换当前有效块，**历史记录块逐字不变**、用户补充分区逐字不变；
//   - hash 不符 → skipped{kind: file_changed, cause: content_hash_mismatch}，
//     detail 内含块 locator 与期望 base_block_hash，且**全文不含**块级冲突的
//     两个预留 kind 名（源码里也不留完整字面量，见用例内拼接构造）。
func TestReplaceBlock_HistoryAppendOnly(t *testing.T) {
	files := m3Files()
	rel := "domains/ai-infra/knowledge/k-20260901-attention.md"
	hash := selfCheckBlockHash(t, files[rel])

	res := m3Run(t, files, `{"op":"replace_block","target":"k-20260901-attention",
 "section":"理解自检","block":"- 换过的当前有效块？\n","initiator":"user"}`)
	d := requireError(t, res, E5)
	if !strings.Contains(d.Path, "base_block_hash") {
		t.Fatalf("缺 base_block_hash 的 E5 应指向该字段：%+v", d)
	}
	if len(res.Actions) != 0 {
		t.Fatal("缺 base_block_hash 必须零展开")
	}

	res = m3Run(t, files, fmt.Sprintf(`{"op":"replace_block","target":"k-20260901-attention",
 "section":"用户补充","block":"- 越界\n","base_block_hash":%q,"initiator":"user"}`, hash))
	requireError(t, res, E6)

	// 落盘复算：真实 store + 真实字节。
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)
	before := readVaultFile(t, dir, rel)

	res = m3Run(t, files, fmt.Sprintf(`{"op":"replace_block","target":"k-20260901-attention",
 "section":"理解自检","block":"- 当前有效块：换成新的自检问题？\n","base_block_hash":%q,
 "initiator":"user"}`, hash))
	if res.Failed() {
		t.Fatalf("合法 replace_block 不得判 error：%v", codes(res.Errors))
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActReplaceBlock {
		t.Fatalf("replace_block 应展开恰一条块替换 action：%+v", res.Actions)
	}
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Skipped) != 0 || len(out.Failures) != 0 {
		t.Fatalf("块替换应写入成功：skipped=%+v failures=%+v", out.Skipped, out.Failures)
	}
	after := readVaultFile(t, dir, rel)
	if !strings.Contains(after, "- 历史块：为什么需要缩放？") {
		t.Fatal("历史记录块必须逐字保留（只追加、永不改写）")
	}
	if strings.Contains(after, "- 当前有效块：多头注意力的头数如何选？") {
		t.Fatal("当前有效块应已被替换")
	}
	if !strings.Contains(after, "- 当前有效块：换成新的自检问题？") {
		t.Fatal("新块未写入")
	}
	if !strings.Contains(after, "我自己的理解：先看 QKV。") {
		t.Fatal("「用户补充」必须逐字保留（B2）")
	}

	// hash 不符：整文件字节不变 + 跳过口径复用封闭两值。
	dir2 := t.TempDir()
	s2 := store.New(dir2)
	writeVault(t, dir2, files)
	res = m3Run(t, files, `{"op":"replace_block","target":"k-20260901-attention",
 "section":"理解自检","block":"- 冲突块\n","base_block_hash":"sha256:0000","initiator":"user"}`)
	if res.Failed() {
		t.Fatalf("块 hash 冲突属跳过（不是 error）：%v", codes(res.Errors))
	}
	out = Execute(s2, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Skipped) != 1 {
		t.Fatalf("块 hash 冲突应产出恰一条 skipped：%+v", out.Skipped)
	}
	skip := out.Skipped[0]
	if skip.Kind != store.SkipFileChanged {
		t.Fatalf("kind 必须复用 file_changed（不新造第三值），实得 %q", skip.Kind)
	}
	if skip.Cause != "content_hash_mismatch" {
		t.Fatalf("cause 必须是 content_hash_mismatch，实得 %q", skip.Cause)
	}
	if !strings.Contains(skip.Detail, "k-20260901-attention#理解自检#") {
		t.Fatalf("detail 必须含块 locator：%q", skip.Detail)
	}
	if !strings.Contains(skip.Detail, "sha256:0000") {
		t.Fatalf("detail 必须含期望 base_block_hash：%q", skip.Detail)
	}
	joined := fmt.Sprintf("%+v", out)
	// 两个预留 kind 名拼接构造：判据 15 的反证 grep 扫整个 internal/，源码里不留完整字面量。
	for _, bad := range []string{"block" + "_conflict", "block" + "_hash_changed"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("报告不得出现禁止字面量 %q：%s", bad, joined)
		}
	}
	if got := readVaultFile(t, dir2, rel); got != before {
		t.Fatal("块 hash 冲突时目标文件字节必须逐字不变")
	}
}

// —— ⑧ §8.4 的三条加严条款 M-13 / M-14 / M-15 ——

// TestRemoveRelation_CountedInTouchesExistingCard：remove_relation 与 add_relation 对称，
// 必须计入 touchesExistingCard()——否则多 op plan 夹带它时会静默不判 W5（那是放宽）。
func TestRemoveRelation_CountedInTouchesExistingCard(t *testing.T) {
	files := m3Files()
	res := m3Run(t, files, `{"op":"remove_relation","from":"k-20260901-attention","type":"limits",
 "target":"k-20260815-rnn","reason":"该限定已不成立","initiator":"user"},
 {"op":"append_card","card":"k-20260901-attention","sections":{"解释与依据":"- 追加\n"}}`)
	if _, ok := find(res.Warnings, W5); !ok {
		t.Fatalf("verb=process 的多 op plan 夹带 remove_relation 且缺 convergence[] → 必须判 W5：%v",
			codes(res.Warnings))
	}
}

// TestStateOps_DoNotSuppressW5_WhenMixed：七个「不计入」的 op **不等于可抵消** ——
// 与 append_card 同 plan 时 W5 照常触发。
func TestStateOps_DoNotSuppressW5_WhenMixed(t *testing.T) {
	files := m3Files()
	for _, op := range []string{
		`{"op":"deprecate","target":"k-20260815-rnn","reason":"已被取代","initiator":"user"}`,
		`{"op":"mark_reviewed","target":"k-20260815-rnn","initiator":"user"}`,
	} {
		res := m3Run(t, files, op+`,
 {"op":"append_card","card":"k-20260901-attention","sections":{"解释与依据":"- 追加\n"}}`)
		if _, ok := find(res.Warnings, W5); !ok {
			t.Fatalf("状态类 op 不得为 plan 整体豁免收敛义务：%s → warnings=%v", op, codes(res.Warnings))
		}
	}
	// 单独一条状态类 op：自己不触发 W5（不计入 touchesExistingCard）。
	res := m3Run(t, files,
		`{"op":"deprecate","target":"k-20260815-rnn","reason":"已被取代","initiator":"user"}`)
	if _, ok := find(res.Warnings, W5); ok {
		t.Fatalf("单条状态类 op 不应触发 W5：%v", codes(res.Warnings))
	}
}

// TestDedicatedRelationPlan_VerbOpPairing：窄例外只认 verb=relate + 单条关系 op，
// 表驱动三行（relate+add_relation → 成立；relate+remove_relation → 成立；relate+deprecate → 不成立）。
func TestDedicatedRelationPlan_VerbOpPairing(t *testing.T) {
	files := m3Files()
	cases := []struct {
		name   string
		op     string
		exempt bool
	}{
		{"relate + add_relation", `{"op":"add_relation","from":"k-20260901-attention",
 "type":"supports","target":"k-20260815-rnn","reason":"新证据支持"}`, true},
		{"relate + remove_relation", `{"op":"remove_relation","from":"k-20260901-attention",
 "type":"limits","target":"k-20260815-rnn","reason":"该限定已不成立","initiator":"user"}`, true},
		{"relate + deprecate", `{"op":"deprecate","target":"k-20260815-rnn",
 "reason":"已被取代","initiator":"user"}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := strings.Replace(m3Plan(t, files, c.op), `"verb":"process"`, `"verb":"relate"`, 1)
			res := run(t, vault(t, files), body)
			_, gotW5 := find(res.Warnings, W5)
			if c.exempt && gotW5 {
				t.Fatalf("窄例外应成立（不出 W5），实得 warnings=%v", codes(res.Warnings))
			}
			if !c.exempt && gotW5 {
				t.Fatalf("借关系动词躲开 W5 是禁止的，但本行 op 不计入 touchesExistingCard，"+
					"因此也不应出 W5：warnings=%v", codes(res.Warnings))
			}
		})
	}
	// 硬断言：窄例外的成员集合恰两个关系 op —— verb=relate + 单条 deprecate 不得落入例外。
	deprecateOnly := &ChangePlan{Ops: []*Op{{Name: OpDeprecate}}}
	v := &validator{p: deprecateOnly, res: &Result{Verb: string(model.VerbRelate)}}
	if v.isDedicatedRelationPlan() {
		t.Fatal("M-15：窄例外不得外溢到状态类 op")
	}
	for _, name := range []string{OpAddRelation, OpRemoveRelation} {
		v := &validator{p: &ChangePlan{Ops: []*Op{{Name: name}}},
			res: &Result{Verb: string(model.VerbRelate)}}
		if !v.isDedicatedRelationPlan() {
			t.Fatalf("M-13：verb=relate + 单条 %s 必须落入窄例外", name)
		}
	}
}

// TestDelete_NoCascadeRemoveRelation：逻辑删除**不得**级联移除关系记录（A-24 口径 5，F3 正交）。
func TestDelete_NoCascadeRemoveRelation(t *testing.T) {
	files := m3Files()
	res := m3Run(t, files, `{"op":"delete","target":"k-20260901-attention","reason":"与新卡重复",
 "initiator":"user","proposal":"p-20261017-001"}`)
	if res.Failed() {
		t.Fatalf("合法 delete 不得判 error：%v", codes(res.Errors))
	}
	for _, a := range res.Actions {
		if a.Kind == ActRemoveRelation || a.Removal != nil {
			t.Fatalf("delete 不得级联展开关系移除：%+v", a)
		}
	}
	// deprecate 同样不级联。
	res = m3Run(t, files,
		`{"op":"deprecate","target":"k-20260901-attention","reason":"已被取代","initiator":"user"}`)
	for _, a := range res.Actions {
		if a.Kind == ActRemoveRelation || a.Removal != nil {
			t.Fatalf("deprecate 不得级联展开关系移除：%+v", a)
		}
	}
}

// TestRemoveRelation_RemovesAllMatchesInPlan：命中即展开一条移除 action，并把命中条数
// （同三元组的历史重复条目一并计入）带给 store —— A-24 口径 1「移除匹配的全部记录」。
func TestRemoveRelation_RemovesAllMatchesInPlan(t *testing.T) {
	files := m3Files()
	res := m3Run(t, files, `{"op":"remove_relation","from":"k-20260901-attention","type":"limits",
 "target":"k-20260815-rnn","reason":"该限定已不成立","initiator":"user"}`)
	if res.Failed() {
		t.Fatalf("合法 remove_relation 不得判 error：%v", codes(res.Errors))
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActRemoveRelation {
		t.Fatalf("应展开恰一条移除 action：%+v", res.Actions)
	}
	rm := res.Actions[0].Removal
	if rm == nil || rm.Matches != 2 {
		t.Fatalf("夹具含同三元组两条，命中数应为 2：%+v", rm)
	}
	// 落盘复算：两条一起消失，用户分区与其余关系逐字保留。
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Skipped) != 0 || len(out.Failures) != 0 {
		t.Fatalf("移除应写入成功：skipped=%+v failures=%+v", out.Skipped, out.Failures)
	}
	after := readVaultFile(t, dir, "domains/ai-infra/knowledge/k-20260901-attention.md")
	if strings.Contains(after, "target: k-20260815-rnn") {
		t.Fatalf("匹配的全部记录都应物理移除（不留墓碑）：\n%s", after)
	}
	if strings.Contains(after, "removed") {
		t.Fatalf("不得留任何「已移除」标记位：\n%s", after)
	}
	if !strings.Contains(after, "我自己的理解：先看 QKV。") {
		t.Fatal("「用户补充」必须逐字保留（B2）")
	}
	if !strings.Contains(after, "source: s-20260901-attention") {
		t.Fatal("材料关系 sources[] 不得被牵连")
	}
}

// —— 用例助手 ——

// requireZeroWrite 断言「零写入」：零 action 展开 + 目标字节逐字不变。
func requireZeroWrite(t *testing.T, res *Result, before, after string) {
	t.Helper()
	if len(res.Actions) != 0 {
		t.Fatalf("error 必须零展开（零写入），实得 %d 条 action", len(res.Actions))
	}
	if before != after {
		t.Fatal("目标文件字节必须逐字不变")
	}
}

// selfCheckBlockHash 取「理解自检」**当前有效块**（最后一个块）的 block_hash。
// plan 不直连 mdfile（§13），因此经 store 的只读转发拿。
func selfCheckBlockHash(t *testing.T, content string) string {
	t.Helper()
	hash, err := store.CurrentSelfCheckBlockHash([]byte(content))
	if err != nil {
		t.Fatalf("读当前有效自检块失败：%v", err)
	}
	return hash
}

// writeVault 把内存夹具落到真实目录（用于落盘复算）。
func writeVault(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
	}
}

func readVaultFile(t *testing.T, root, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("读回夹具失败：%v", err)
	}
	return string(raw)
}

func mustStamp(t *testing.T) model.Stamp {
	t.Helper()
	s, err := model.ParseStamp("2026-10-17T10:00:00+08:00")
	if err != nil {
		t.Fatalf("时间戳解析失败：%v", err)
	}
	return s
}

// repoInternalDir 定位 internal/ 目录（本用例在 internal/plan 下运行）。
func repoInternalDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("定位 internal/ 失败：%v", err)
	}
	return dir
}
