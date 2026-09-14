package cli

// T-…-039 的 CLI 层机器判据：`eg deprecate` / `eg restore` / `eg replaced-by` 三条
// 用户显式状态命令（授权合同 §2 矩阵 #3 / #4、§9 A-15；状态合同 §5.1 / §5.3、§8.1）。
//
// 全部用例走**真实** store 写口与**真实** git 仓：不打桩写入、不打桩校验，
// 唯一注入的是「时间」。断言重心在「只改该改的那一个键」与「零写入零 commit」两侧，
// 因为退出码只说明判定结果，字节与 commit 计数才说明**真的没多做事**。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	stateCardOld = "k-20260901-old" // 被失效 / 被替代的主体卡
	stateCardNew = "k-20260902-new" // 替代它的新卡（`replaced-by` 的指向端）
)

// stateVault 建一个真实 git 仓的 vault，seed 两张卡：主体卡带一条关系 + 一条 sources 条目，
// 用来反证状态写入**不动**关系与依据（状态合同 §5.3「关系与依据全保留」）。
func stateVault(t *testing.T) string {
	t.Helper()
	dir := captureVault(t)
	writeFileMk(t, stateCardFile(dir, stateCardOld), stateCardBytes(stateCardOld, "旧卡", "active"))
	seedRelCard(t, dir, "ai-infra", stateCardNew, "新卡", "")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "seed: 状态命令语料")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

func stateCardFile(dir, id string) string {
	return filepath.Join(dir, "domains", "ai-infra", "knowledge", id+".md")
}

// stateCardBytes 构造一张带关系与 sources 条目的卡（frontmatter 手写：
// 这些字节是「状态写入前」的基准，必须由用例完全掌握）。
func stateCardBytes(id, title, status string) string {
	return "---\nid: " + id + "\nstatus: " + status + "\ncreated_at: '2026-09-01'\n" +
		"updated_at: '2026-09-12T10:00:00+08:00'\ntitle: " + title + "\n" +
		"sources:\n  - source: s-20260901-x\n    note: n-20260901-x\n    rel: support\n" +
		"    reason: 旧依据\n" +
		"relations:\n  - type: supports\n    target: " + stateCardNew + "\n    reason: 旧关系\n" +
		"---\n\n## 知识内容\n\n正文占位。\n"
}

// runStateCLI 跑一次状态命令（`--json`），返回退出码、信封与 stderr。
func runStateCLI(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return captureAt(t) }
	code, out, errOut := runCLI(t, r, append([]string{"--vault", dir, "--json"}, args...)...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

func readState(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件失败：%v", err)
	}
	return string(raw)
}

// fmValue 取 frontmatter 里某个顶层单键的值（只用于 status 这类标量键）。
func fmValue(t *testing.T, body, key string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, key+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, key+":"))
		}
	}
	t.Fatalf("frontmatter 缺键 %s：\n%s", key, body)
	return ""
}

func countSub(body, sub string) int { return strings.Count(body, sub) }

// —— ① deprecate 只覆盖 status 单键 ——

func TestDeprecate_WritesStatusOnly(t *testing.T) {
	dir := stateVault(t)
	path := stateCardFile(dir, stateCardOld)
	before := readState(t, path)

	code, _, errOut := runStateCLI(t, dir, "deprecate", "--target", stateCardOld,
		"--reason", "结论已被新证据推翻")
	if code != ExitOK {
		t.Fatalf("eg deprecate 退出码 = %d，期望 0：%s", code, errOut)
	}

	after := readState(t, path)
	if got := fmValue(t, after, "status"); got != "deprecated" {
		t.Fatalf("status = %q，期望 deprecated", got)
	}
	// deleted_at 不得出现：逻辑删除是另一条命令（T-…-041），状态失效与它无关。
	if strings.Contains(after, "deleted_at") {
		t.Fatalf("deprecate 不得写 deleted_at：\n%s", after)
	}
	// 关系与 sources[] 条目数逐条不变（状态合同 §5.3）。
	for _, sub := range []string{"target: " + stateCardNew, "reason: 旧关系",
		"source: s-20260901-x", "note: n-20260901-x", "reason: 旧依据"} {
		if countSub(before, sub) != countSub(after, sub) {
			t.Fatalf("状态写入改动了关系 / 依据条目 %q：%d → %d",
				sub, countSub(before, sub), countSub(after, sub))
		}
	}
	if countSub(before, "- type: supports") != countSub(after, "- type: supports") {
		t.Fatalf("relations[] 条目数变化：\n前\n%s\n后\n%s", before, after)
	}
	// 单键覆盖：status 仍只出现一次，不得堆出历史数组。
	if got := countSub(after, "status:"); got != 1 {
		t.Fatalf("status 键出现 %d 次，期望恰 1（反复覆盖单键，不留历史）", got)
	}
}

// —— ② --reason 必填：退 1 + 零写入 + 零 commit ——

func TestDeprecate_ReasonRequired(t *testing.T) {
	assertReasonRequired(t, "deprecate")
}

func TestRestore_ReasonRequired(t *testing.T) {
	assertReasonRequired(t, "restore")
}

// assertReasonRequired 断言缺 `--reason` 是**用法错误**（退 1），不是校验失败（退 2）：
// 两者混用会让「参数没给对」与「库的状态不允许」在报告里彼此冒名。
func assertReasonRequired(t *testing.T, cmd string) {
	t.Helper()
	dir := stateVault(t)
	path := stateCardFile(dir, stateCardOld)
	before := readState(t, path)
	logBefore := gitLogCount(t, dir)

	code, _, _ := runStateCLI(t, dir, cmd, "--target", stateCardOld)
	if code != ExitUsage {
		t.Fatalf("eg %s 缺 --reason 退出码 = %d，期望 %d", cmd, code, ExitUsage)
	}
	if got := readState(t, path); got != before {
		t.Fatalf("缺 --reason 必须零写入，文件已变：\n%s", got)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("缺 --reason 必须零 commit：%d → %d", logBefore, got)
	}
	if s := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); s != "" {
		t.Fatalf("缺 --reason 必须零工作区变化，得到 %q", s)
	}
}

// —— ③ restore 不以「存在有效 support」为前提 ——

func TestRestore_NoSupportPrecondition(t *testing.T) {
	dir := stateVault(t)
	// 一张 deprecated 且 **sources[] 为空**（无任何有效依据）的卡。
	noSupport := "k-20260903-nosupport"
	writeFileMk(t, stateCardFile(dir, noSupport),
		"---\nid: "+noSupport+"\nstatus: deprecated\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-12T10:00:00+08:00'\ntitle: 无依据卡\nsources: []\n"+
			"---\n\n## 知识内容\n\n正文占位。\n")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "seed: 无依据的失效卡")

	code, env, errOut := runStateCLI(t, dir, "restore", "--target", noSupport,
		"--reason", "用户判断该结论仍然成立")
	if code != ExitOK {
		t.Fatalf("无 support 的 restore 退出码 = %d，期望 0（系统不拦截）：%s", code, errOut)
	}
	if got := fmValue(t, readState(t, stateCardFile(dir, noSupport)), "status"); got != "active" {
		t.Fatalf("status = %q，期望 active", got)
	}
	// 「材料支持不足」属 S3，本阶段不得出现在任何输出里。
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("信封不可序列化：%v", err)
	}
	if strings.Contains(string(raw), "材料支持不足") {
		t.Fatalf("restore 输出不得含「材料支持不足」（该标记归 S3）：%s", raw)
	}
}

// —— ④ replaced-by 单向：被指向的新卡字节不变 ——

func TestSetReplacedBy_NoReverseWrite(t *testing.T) {
	dir := stateVault(t)
	oldPath, newPath := stateCardFile(dir, stateCardOld), stateCardFile(dir, stateCardNew)
	newBefore := readState(t, newPath)

	code, _, errOut := runStateCLI(t, dir, "replaced-by", "--target", stateCardOld,
		"--to", stateCardNew, "--reason", "新卡给出了更完整的结论")
	if code != ExitOK {
		t.Fatalf("eg replaced-by 退出码 = %d，期望 0：%s", code, errOut)
	}
	oldAfter := readState(t, oldPath)
	if !strings.Contains(oldAfter, "replaced_by:") ||
		!strings.Contains(oldAfter, "target: "+stateCardNew) {
		t.Fatalf("失效卡上应写出 replaced_by：\n%s", oldAfter)
	}
	// 反向写入会造出第二份可漂移的记录：被指向卡必须**逐字节**不变。
	if got := readState(t, newPath); got != newBefore {
		t.Fatalf("被指向的新卡字节被改动：\n前\n%s\n后\n%s", newBefore, got)
	}
}

// —— ⑤ 失效卡可继续追加 support：报告有事实、状态不动 ——

func TestDeprecatedCardAcceptsNewSupport(t *testing.T) {
	dir := stateVault(t)
	if code, _, errOut := runStateCLI(t, dir, "deprecate", "--target", stateCardOld,
		"--reason", "结论过期"); code != ExitOK {
		t.Fatalf("前置 deprecate 退出码 = %d：%s", code, errOut)
	}

	code, env, errOut := runStateCLI(t, dir, "rel", "add", stateCardNew, "supports",
		stateCardOld, "--reason", "新卡的证据仍支持旧卡的部分结论")
	if code != ExitOK {
		t.Fatalf("对 deprecated 卡追加 support 退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if len(rep.DeprecatedNewSupport) == 0 {
		t.Fatal("report.deprecated_new_support[] 必须非空：失效卡出现了新支持材料")
	}
	if got := rep.DeprecatedNewSupport[0].Card; got != stateCardOld {
		t.Fatalf("deprecated_new_support[0].card = %q，期望 %q", got, stateCardOld)
	}
	// 状态一律不动：不自动恢复、不产生恢复建议（U-06 / EG-KNW-06）。
	if got := fmValue(t, readState(t, stateCardFile(dir, stateCardOld)), "status"); got != "deprecated" {
		t.Fatalf("追加 support 后 status = %q，期望仍为 deprecated", got)
	}
}
