package cli

// T-…-044 的 CLI 层机器判据：`eg rel remove` 接管 M2 占位后的真实行为。
//
// 判据来源：本 task 的 Acceptance（一次成功删除恰一次 `relate` commit / W10 幂等零 commit /
// 参数分级 / 重跑幂等）；owner 裁决 `docs/specs/2026-10-13-m3-prestart-adjudication.md`
// §7.2（A-24 = `物理移除`，5 条附加约束）；授权合同写权限矩阵 #11「删关系」P-A 🔴 / P-U ✅。
//
// 全部用例走**真实** store 写口与**真实** git 仓：不打桩写入、不打桩校验，
// 唯一注入的是「时间」（commit 时间戳可复算）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// relRemoveVault 建一个真实 git 仓的 vault：A 卡带三条正向关系
// （同三元组 limits 两条 → 验「移除全部匹配」；supports 一条 → 验「不牵连其他三元组」）。
func relRemoveVault(t *testing.T) string {
	t.Helper()
	dir := captureVault(t) // eg init：真实 git 仓 + evergreen.yml（default_domain=ai-infra）
	seedRelCard(t, dir, "ai-infra", relAddCardA, "A 卡",
		"  - type: limits\n    target: "+relAddCardB+"\n    reason: 该限定已不成立\n"+
			"  - type: limits\n    target: "+relAddCardB+"\n    reason: 历史遗留的重复条目\n"+
			"  - type: supports\n    target: "+relAddCardB+"\n    reason: 这条不该被牵连\n")
	seedRelCard(t, dir, "ai-infra", relAddCardB, "B 卡", "")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "seed: rel remove 语料")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

// runRelRemoveCLI 跑一次 `eg rel remove … --json`，返回退出码、信封与 stderr。
func runRelRemoveCLI(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return captureAt(t) }
	code, out, errOut := runCLI(t, r,
		append([]string{"--vault", dir, "--json", "rel", "remove"}, args...)...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

// TestRelRemove_Commit：一次成功删除 = 一次 `relate` commit，落盘按 A-24 物理移除。
func TestRelRemove_Commit(t *testing.T) {
	dir := relRemoveVault(t)
	abs := relAddCardPath(dir, "ai-infra", relAddCardA)
	before := readFileString(t, abs)
	logBefore := gitLogCount(t, dir)

	code, env, errOut := runRelRemoveCLI(t, dir, relAddCardA, "limits", relAddCardB,
		"--reason", "该限定已不成立")
	if code != ExitOK {
		t.Fatalf("成功删除应退 0，实际 %d（%s）", code, errOut)
	}
	// ① 恰一次 commit，且主题以 relate( 开头（verb 与 rel add 同族，不退化成 process）。
	if got := gitLogCount(t, dir); got != logBefore+1 {
		t.Fatalf("一次成功删除应恰产生一次 commit：%d → %d", logBefore, got)
	}
	subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
	if !strings.HasPrefix(subject, "relate(") {
		t.Fatalf("commit 主题应以 relate( 开头，实得 %q", subject)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("写入必须一并提交，工作区应干净，实得 %q", got)
	}

	// ② 落盘语义（A-24 `物理移除`）：匹配三元组的两条一起消失、不留墓碑、其余逐字保留。
	after := readFileString(t, abs)
	if after == before {
		t.Fatal("物理移除必须改动字节（Git diff 可见）")
	}
	if strings.Contains(after, "该限定已不成立") || strings.Contains(after, "历史遗留的重复条目") {
		t.Fatalf("匹配的**全部**记录都应物理移除：\n%s", after)
	}
	if !strings.Contains(after, "这条不该被牵连") {
		t.Fatalf("其他三元组不得被牵连：\n%s", after)
	}
	for _, ban := range []string{"removed_at", "relation_id", "已移除"} {
		if strings.Contains(after, ban) {
			t.Fatalf("不得留墓碑 / 新主键（命中 %q）：\n%s", ban, after)
		}
	}
	if !strings.Contains(after, "## 知识内容") || !strings.Contains(after, "正文占位。") {
		t.Fatalf("正文必须逐字保留：\n%s", after)
	}
	// ③ 信封：plan 回带的 op 恰一条 remove_relation + initiator=user（Agent 可原样复投 apply）。
	if env.Data == nil {
		t.Fatal("--json 必须回带 data")
	}
	planRaw, _ := json.Marshal(env.Data["plan"])
	raw := string(planRaw)
	for _, must := range []string{`"op":"remove_relation"`, `"initiator":"user"`, `"verb":"relate"`} {
		if !strings.Contains(raw, must) {
			t.Fatalf("data.plan 缺 %s：%s", must, raw)
		}
	}

	// ④ 重跑同一命令：W10 幂等——退 0、零新增 commit、字节不变。
	code2, env2, errOut2 := runRelRemoveCLI(t, dir, relAddCardA, "limits", relAddCardB,
		"--reason", "该限定已不成立")
	if code2 != ExitOK {
		t.Fatalf("重跑必须退 0（幂等），实际 %d（%s）", code2, errOut2)
	}
	if got := gitLogCount(t, dir); got != logBefore+1 {
		t.Fatalf("未命中不得产生空 commit：commit 数应仍为 %d，实得 %d", logBefore+1, got)
	}
	if got := readFileString(t, abs); got != after {
		t.Fatal("重跑必须零写入（字节不变）")
	}
	if !strings.Contains(relAddWarningCodes(env2), "W10") {
		t.Fatalf("重跑必须记 W10，实得 %s", relAddWarningCodes(env2))
	}
}

// TestRelRemove_UsageErrors：参数形态错误一律退 1、零写入零 commit（与 rel add 同一分级）。
func TestRelRemove_UsageErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		args []string
	}{
		{"位置参数少一个", []string{relAddCardA, "limits"}},
		{"type 出封闭四值", []string{relAddCardA, "removes", relAddCardB, "--reason", "x"}},
		{"完全未给 --reason", []string{relAddCardA, "limits", relAddCardB}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := relRemoveVault(t)
			logBefore := gitLogCount(t, dir)
			treeBefore := vaultSnapshot(t, dir)

			code, _, errOut := runRelRemoveCLI(t, dir, c.args...)
			if code != ExitUsage {
				t.Fatalf("参数非法应退 1，实际 %d（%s）", code, errOut)
			}
			if got := gitLogCount(t, dir); got != logBefore {
				t.Fatalf("参数错误必须零 commit：%d → %d", logBefore, got)
			}
			if got := vaultSnapshot(t, dir); got != treeBefore {
				t.Fatal("参数错误必须零写入（.md 字节不变）")
			}
		})
	}
}

// TestRelRemove_ValidationFailureIsZeroWrite：端点不存在 → E2 退 2、零写入零 commit。
func TestRelRemove_ValidationFailureIsZeroWrite(t *testing.T) {
	dir := relRemoveVault(t)
	logBefore := gitLogCount(t, dir)
	authBefore := authoritySnapshot(t, dir)

	code, env, errOut := runRelRemoveCLI(t, dir, relAddCardA, "limits", "k-20260909-missing",
		"--reason", "对端不存在")
	if code != ExitValidation {
		t.Fatalf("校验失败应退 2，实际 %d（%s）", code, errOut)
	}
	errRaw, _ := json.Marshal(env.Data["errors"])
	if !strings.Contains(string(errRaw), `"E2"`) {
		t.Fatalf("对端不存在应判 E2，实得 %s", errRaw)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("校验失败必须零 commit：%d → %d", logBefore, got)
	}
	// 同 rel add：退 2 的路径允许留下运行时锁（合同 §2 先取锁后校验），但权威 Markdown
	// 逐字节不变，且运行时目录里除锁之外没有事务日志 / intent / 标记 / 索引库产物。
	assertAuthorityUnchanged(t, dir, authBefore, "rel remove 校验失败")
	assertRuntimeOnlyLock(t, dir, "rel remove 校验失败")
	assertNoNewTxn(t, dir, nil, "rel remove 校验失败")
}

// readFileString 读全文（字节级取证）。
func readFileString(t *testing.T, abs string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(abs))
	if err != nil {
		t.Fatalf("读 %s 失败：%v", abs, err)
	}
	return string(raw)
}
