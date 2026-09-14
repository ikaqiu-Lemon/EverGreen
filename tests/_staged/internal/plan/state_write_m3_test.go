package plan

// M3 状态维度落盘接线的用例（T-…-040）：deprecate / restore / set_replaced_by 从
// 「校验通过但不落盘」升级为真实落盘之后，三条判定边界必须仍然成立：
//
//   - E10 是 error → 退 2、零展开、**目标文件字节逐字不变**；
//   - W12 是 warning → 照常写入、退 0，且报告 warnings[] 里留得下 W12；
//   - W11 幂等 → 该 op 零 action、零写入、**不产生空 commit**（Written 为空）。
//
// 落盘一律经 store 的导出状态写入入口（ApplyStateWrite），本包不直呼三个 setter。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const relAttention = "domains/ai-infra/knowledge/k-20260901-attention.md"

// TestE10_ReplacedByDeletedTarget：替代目标已被逻辑删除 → 退 2 且目标文件字节不变。
//
// 「字节不变」用真实 store 复算：即使 error 路径将来被误接到 executor 上，
// 磁盘比对也会立刻抓住（校验期零展开只是必要条件，不是充分条件）。
func TestE10_ReplacedByDeletedTarget(t *testing.T) {
	files := m3Files()
	files["domains/ai-infra/knowledge/k-20260815-rnn.md"] = cardM3("k-20260815-rnn", "active",
		"\ndeleted_at: '2026-10-17T09:00:00+08:00'\ndeleted_reason: 与新卡重复")
	dir := t.TempDir()
	writeVault(t, dir, files)
	before := readVaultFile(t, dir, relAttention)

	res := m3Run(t, files, `{"op":"set_replaced_by","target":"k-20260901-attention",
 "initiator":"user","replaced_by":{"target":"k-20260815-rnn","reason":"新版综述"}}`)
	requireError(t, res, E10)
	if len(res.Actions) != 0 {
		t.Fatalf("E10 必须零展开（退 2、零写入），实得 %d 条 action", len(res.Actions))
	}
	if got := readVaultFile(t, dir, relAttention); got != before {
		t.Fatal("E10 时目标文件字节必须逐字不变")
	}
}

// TestW12_ReplacedByDeprecatedTarget：替代目标是 deprecated 且未删除 → 写入成功退 0，
// 且报告 warnings[] 含 W12。
func TestW12_ReplacedByDeprecatedTarget(t *testing.T) {
	files := m3Files()
	files["domains/ai-infra/knowledge/k-20260815-rnn.md"] = cardM3("k-20260815-rnn", "deprecated", "")
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)

	res := m3Run(t, files, `{"op":"set_replaced_by","target":"k-20260901-attention",
 "initiator":"user","replaced_by":{"target":"k-20260815-rnn","reason":"新版综述已覆盖本卡"}}`)
	if res.Failed() {
		t.Fatalf("W12 是 warning，不得拦截（退 0）：%v", codes(res.Errors))
	}
	requireWarning(t, res, W12)
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActSetReplacedBy {
		t.Fatalf("set_replaced_by 应展开恰一条替代指针 action：%+v", res.Actions)
	}
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Skipped) != 0 || len(out.Failures) != 0 {
		t.Fatalf("替代指针应写入成功：skipped=%+v failures=%+v", out.Skipped, out.Failures)
	}
	if len(out.Written) != 1 || out.Written[0] != relAttention {
		t.Fatalf("只应写主体卡这一份文件（单向存储）：%v", out.Written)
	}
	after := readVaultFile(t, dir, relAttention)
	if !strings.Contains(after,
		`replaced_by: {target: k-20260815-rnn, reason: "新版综述已覆盖本卡"}`) {
		t.Fatalf("replaced_by 未按合同形态落盘：\n%s", after)
	}
	if !strings.Contains(after, "我自己的理解：先看 QKV。") {
		t.Fatal("「用户补充」必须逐字保留（B2）")
	}
	// 单向存储：被指向的目标卡一个字节都不许动。
	if got := readVaultFile(t, dir, "domains/ai-infra/knowledge/k-20260815-rnn.md"); got !=
		files["domains/ai-infra/knowledge/k-20260815-rnn.md"] {
		t.Fatal("replaced_by 的目标卡不得被改写（单向存储）")
	}
}

// TestW11_IdempotentStateOps：重复 deprecate / 重复 restore → 该 op 零写入、含 W11、
// 不产生空 commit（Written 为空 → 调用方无可提交内容）。
func TestW11_IdempotentStateOps(t *testing.T) {
	deprecated := m3Files()
	deprecated[relAttention] = cardM3("k-20260901-attention", "deprecated", "")
	cases := []struct {
		name  string
		files map[string]string
		ops   string
	}{
		{"重复 deprecate（目标已 deprecated）", deprecated,
			`{"op":"deprecate","target":"k-20260901-attention","reason":"再失效一次","initiator":"user"}`},
		{"重复 restore（目标已 active）", m3Files(),
			`{"op":"restore","target":"k-20260901-attention","reason":"再恢复一次","initiator":"user"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			s := store.New(dir)
			writeVault(t, dir, c.files)
			before := readVaultFile(t, dir, relAttention)

			res := m3Run(t, c.files, c.ops)
			if res.Failed() {
				t.Fatalf("W11 是 warning，不得影响退出码：%v", codes(res.Errors))
			}
			requireWarning(t, res, W11)
			if len(res.Actions) != 0 {
				t.Fatalf("W11 幂等必须零 action，实得 %d", len(res.Actions))
			}
			out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
			if len(out.Written) != 0 {
				t.Fatalf("W11 幂等必须零写入（不产生空 commit）：%v", out.Written)
			}
			if len(out.Skipped) != 0 || len(out.Failures) != 0 {
				t.Fatalf("幂等不是跳过、也不是失败：skipped=%+v failures=%+v",
					out.Skipped, out.Failures)
			}
			if got := readVaultFile(t, dir, relAttention); got != before {
				t.Fatal("幂等 no-op 时目标文件字节必须逐字不变")
			}
		})
	}
}

// TestSetStatus_WritesOnlyStatusKey：deprecate 真实落盘只改 status 一行。
//
// 与 W11 同源纪律的另一面：一旦确实要改，也只许改那一格——状态与删除是正交维度，
// 顺手动 deleted_* 或长出历史键都属越界（合同 F3）。
func TestSetStatus_WritesOnlyStatusKey(t *testing.T) {
	files := m3Files()
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)

	res := m3Run(t, files,
		`{"op":"deprecate","target":"k-20260901-attention","reason":"已被新版综述取代","initiator":"user"}`)
	if res.Failed() {
		t.Fatalf("合法 deprecate 不得判 error：%v", codes(res.Errors))
	}
	if len(res.Actions) != 1 || res.Actions[0].Kind != ActSetStatus {
		t.Fatalf("deprecate 应展开恰一条 status action：%+v", res.Actions)
	}
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Skipped) != 0 || len(out.Failures) != 0 {
		t.Fatalf("status 应写入成功：skipped=%+v failures=%+v", out.Skipped, out.Failures)
	}
	after := readVaultFile(t, dir, relAttention)
	// 只许差两行：status 那一格，以及跟随本次实际写入刷新的 updated_at（矩阵第 8 行 /
	// I-…-009）。deleted_* / reviewed_at / 历史键一格不许动。
	want := strings.Replace(files[relAttention], "status: active", "status: deprecated", 1)
	want = strings.Replace(want,
		"updated_at: '2026-09-01T10:00:00+08:00'",
		"updated_at: '"+mustStamp(t).String()+"'", 1)
	if after != want {
		t.Fatalf("落盘结果只应差 status 与 updated_at 两行：\n实得：\n%s", after)
	}
}

// TestStateOp_BaseNotCoveringIsSkipped：base 未覆盖 → W6 + skipped[]（cause 复用封闭两值），
// 由上层退 3；本包不新造第三种跳过机制。
func TestStateOp_BaseNotCoveringIsSkipped(t *testing.T) {
	files := m3Files()
	dir := t.TempDir()
	s := store.New(dir)
	writeVault(t, dir, files)
	before := readVaultFile(t, dir, relAttention)

	env := vault(t, files)
	env.UserRequest = true
	res := run(t, env, `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"base 未覆盖",
 "requirement_ids":["EG-EDIT-04"],"base":{},"ops":[
 {"op":"deprecate","target":"k-20260901-attention","reason":"已被取代","initiator":"user"}]}`)
	if res.Failed() {
		t.Fatalf("W6 是 warning，不得升 error：%v", codes(res.Errors))
	}
	requireWarning(t, res, W6)
	out := Execute(s, res, ExecOptions{Stamp: mustStamp(t)})
	if len(out.Skipped) != 1 {
		t.Fatalf("base 未覆盖应产出恰一条 skipped：%+v", out.Skipped)
	}
	if out.Skipped[0].Kind != store.SkipFileChanged ||
		out.Skipped[0].Cause != "content_hash_mismatch" {
		t.Fatalf("跳过命名必须复用封闭两值：%+v", out.Skipped[0])
	}
	if got := readVaultFile(t, dir, relAttention); got != before {
		t.Fatal("跳过时目标文件字节必须逐字不变")
	}
}
