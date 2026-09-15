package cli

// T-…-024 的 CLI 层机器判据：`eg rel add` 的写入链路、`relate` commit（恰一次）、
// `opposing` 归一去重、校验失败零写入、B3 / B4。
//
// 判据来源：M2 查询与关系写入合同 §4（参数 / 复用现有写入链路 / opposing / 报告与 commit /
// 退出码）；`rel remove` 的占位已由 T-…-044 接管（见 rel_remove_test.go）；ChangePlan 合同 §3.6 / §4.5.1（E2 / E3 / E5 /
// W2 / W8）；技术方案 §9 的 B1–B4。测试名统一以 `RelAdd` 开头，与 verify.test 的
// `-run 'RelAdd|NormalizeVerb'` 对齐。
//
// 全部用例走**真实** store 写口与**真实** git 仓：不打桩写入、不打桩校验，
// 唯一注入的是「时间」与「Git Runner」（B4 提交失败分支）。

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

const (
	relAddCardA = "k-20260901-a"
	relAddCardB = "k-20260902-b"
	relAddCardC = "k-20260903-c"
)

// relAddVault 建一个**真实 git 仓**的 vault，并 seed 三张无关系的干净卡。
func relAddVault(t *testing.T) string {
	t.Helper()
	dir := captureVault(t) // eg init：真实 git 仓 + evergreen.yml（default_domain=ai-infra）
	seedRelCard(t, dir, "ai-infra", relAddCardA, "A 卡", "")
	seedRelCard(t, dir, "ai-infra", relAddCardB, "B 卡", "")
	seedRelCard(t, dir, "ai-infra", relAddCardC, "C 卡", "")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "seed: rel add 语料")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

// runRelAddCLI 跑一次 `eg rel add … --json`，返回退出码、信封与 stderr。
func runRelAddCLI(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return captureAt(t) }
	return runRelAddWith(t, r, dir, args...)
}

func runRelAddWith(t *testing.T, r *Root, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	if r.Now == nil {
		r.Now = func() time.Time { return captureAt(t) }
	}
	code, out, errOut := runCLI(t, r,
		append([]string{"--vault", dir, "--json", "rel", "add"}, args...)...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

func relAddCardPath(dir, domain, id string) string {
	return filepath.Join(dir, "domains", domain, "knowledge", id+".md")
}

// relAddWarningCodes 收集信封 warnings[] 的 code 集合。
func relAddWarningCodes(env Envelope) string {
	var codes []string
	for _, w := range env.Warnings {
		codes = append(codes, w.Code+":"+w.Message)
	}
	return strings.Join(codes, "\n")
}

// —— ① 恰一次 relate commit（M2 完成判据 5）——

func TestRelAddProducesSingleRelateCommit(t *testing.T) {
	dir := relAddVault(t)
	before := gitLogCount(t, dir)

	code, env, errOut := runRelAddCLI(t, dir, relAddCardA, "supports", relAddCardB,
		"--reason", "A 的结论支持 B 的结论")
	if code != ExitOK {
		t.Fatalf("rel add 退出码 = %d，期望 0：%s", code, errOut)
	}
	if got := gitLogCount(t, dir) - before; got != 1 {
		t.Fatalf("一次 rel add 必须恰产生一次 commit，实际 +%d", got)
	}
	subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
	if !strings.HasPrefix(subject, "relate(ai-infra): ") {
		t.Fatalf("commit 主题必须以 relate(<domain>): 开头，实际 %q", subject)
	}
	if strings.HasPrefix(subject, "process(") {
		t.Fatalf("verb 退化成了 process：%q", subject)
	}
	if strings.Contains(relAddWarningCodes(env), "已退化为 process") {
		t.Fatalf("warnings[] 不得含 verb 退化提示：%s", relAddWarningCodes(env))
	}
	if strings.Contains(relAddWarningCodes(env), "W5:") ||
		strings.Contains(relAddWarningCodes(env), "convergence[] 缺条目") {
		t.Fatalf("专用 rel add 计划不应因空 convergence[] 产生 W5：%s", relAddWarningCodes(env))
	}
	// 报告体：关系条数与 commit sha 如实可核对（合同 §4.4）。
	rep := applyReport(t, env)
	if len(rep.Relations.Knowledge) != 1 {
		t.Fatalf("report.relations.knowledge 应恰一条，实际 %d", len(rep.Relations.Knowledge))
	}
	k := rep.Relations.Knowledge[0]
	if k.From != relAddCardA || k.Type != "supports" || k.Target != relAddCardB {
		t.Fatalf("报告里的关系事实不符：%+v", k)
	}
	if rep.Git.Commit == nil || !strings.HasPrefix(
		strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD")), *rep.Git.Commit) {
		t.Fatalf("report.git.commit 必须是本次 sha，实际 %v", rep.Git.Commit)
	}
	// --json 回带组装出的 plan，供 Agent 复投（合同 §4.2）。
	// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：`eg rel add` 组装的 plan
	// 版本号随**当前**版本走（契约 §4.1 把 PlanVersion 提到 2，v1 只是兼容期仍被接受）。
	// 期望值取自 plan.PlanVersion 而不是再写死一个数字：写死 2 只会让下一次版本变更
	// 重演今天这次修改，而「CLI 组装的 plan 必须是当前版本」这条判据本身一格未放宽。
	raw, _ := json.Marshal(env.Data["plan"])
	for _, want := range []string{`"verb":"relate"`, `"op":"add_relation"`,
		fmt.Sprintf(`"plan_version":%d`, plan.PlanVersion), `"target":"` + relAddCardB + `"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("data.plan 缺 %s：%s", want, raw)
		}
	}
	if n := strings.Count(string(raw), `"op":`); n != 1 {
		t.Fatalf("组装出的 plan 必须恰含一个 op，实际 %d 个：%s", n, raw)
	}
}

// —— ② 关系确实落盘，读路径能读回 ——

func TestRelAddWritesRelation(t *testing.T) {
	dir := relAddVault(t)
	if code, _, errOut := runRelAddCLI(t, dir, relAddCardA, "supports", relAddCardB,
		"--reason", "A 的结论支持 B 的结论"); code != ExitOK {
		t.Fatalf("rel add 退出码 = %d：%s", code, errOut)
	}

	cardA := string(mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA)))
	for _, want := range []string{"relations:", "- type: 'supports'",
		"target: '" + relAddCardB + "'", "reason: 'A 的结论支持 B 的结论'"} {
		if !strings.Contains(cardA, want) {
			t.Fatalf("A 卡 frontmatter 缺 %q：\n%s", want, cardA)
		}
	}
	if n := strings.Count(cardA, "- type:"); n != 1 {
		t.Fatalf("A 卡 relations[] 应恰多一条，实际 %d 条", n)
	}
	// B1：只追加——正文分区逐字保留。
	if !strings.Contains(cardA, "## 知识内容\n\n正文占位。\n") {
		t.Fatalf("用户正文必须逐字保留：\n%s", cardA)
	}
	// 读路径（T-…-023）能读回：A 的正向一条、B 的反向一条。
	_, envA, _ := runRelJSON(t, dir, relAddCardA)
	dataA, _ := envA["data"].(map[string]interface{})
	if got := relEdgeSigs(t, dataA, "relations_out", "target"); len(got) != 1 ||
		got[0] != "supports("+relAddCardB+")" {
		t.Fatalf("eg rel A 的正向应恰读回新写入的一条，实际 %v", got)
	}
	_, envB, _ := runRelJSON(t, dir, relAddCardB)
	dataB, _ := envB["data"].(map[string]interface{})
	if got := relEdgeSigs(t, dataB, "relations_in", "from"); len(got) != 1 ||
		got[0] != "supports("+relAddCardA+")" {
		t.Fatalf("eg rel B 的反向应出现 A，实际 %v", got)
	}
}

// —— ③ opposing 字典序归一 + 同对幂等（EG-CVG-05 / W8）——

func TestRelAddOpposingNormalizedAndIdempotent(t *testing.T) {
	dir := relAddVault(t)
	// 先写非字典序方向：B opposing A（"k-20260901-a" < "k-20260902-b"）。
	code, env, errOut := runRelAddCLI(t, dir, relAddCardB, "opposing", relAddCardA,
		"--reason", "两张卡的结论互斥")
	if code != ExitOK {
		t.Fatalf("rel add opposing 退出码 = %d：%s", code, errOut)
	}
	if !strings.Contains(relAddWarningCodes(env), "W8") {
		t.Fatalf("方向被规范化必须记 W8：%s", relAddWarningCodes(env))
	}
	cardA := string(mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA)))
	cardB := string(mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardB)))
	if !strings.Contains(cardA, "type: 'opposing'") {
		t.Fatalf("opposing 必须写在字典序在前的一端（A 卡）：\n%s", cardA)
	}
	if strings.Contains(cardB, "type: 'opposing'") {
		t.Fatalf("字典序在后的一端不得留 opposing 记录（单向存储）：\n%s", cardB)
	}
	if total := strings.Count(cardA, "type: 'opposing'") + strings.Count(cardB, "type: 'opposing'"); total != 1 {
		t.Fatalf("全库 opposing 记录必须恰一条，实际 %d 条", total)
	}

	// 再写反向的同一对：幂等 —— 不新增条目、不产生空 commit。
	logBefore := gitLogCount(t, dir)
	code, env, errOut = runRelAddCLI(t, dir, relAddCardA, "opposing", relAddCardB,
		"--reason", "两张卡的结论互斥")
	if code != ExitOK {
		t.Fatalf("同对 opposing 重复写入应退 0，实际 %d：%s", code, errOut)
	}
	if !strings.Contains(relAddWarningCodes(env), "W8") ||
		!strings.Contains(relAddWarningCodes(env), "幂等跳过") {
		t.Fatalf("同对已存在必须记 W8「幂等跳过」：%s", relAddWarningCodes(env))
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("无实际改动不得产生空 commit：%d → %d", logBefore, got)
	}
	after := string(mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA)))
	if strings.Count(after, "type: 'opposing'") != 1 {
		t.Fatalf("同对 opposing 不得产生第二条：\n%s", after)
	}
	if s := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); s != "" {
		t.Fatalf("幂等路径不得留下工作区改动：%q", s)
	}
}

// —— ④ 重复写同一条普通关系：幂等、无空 commit ——

func TestRelAddIsIdempotentForSameRelation(t *testing.T) {
	dir := relAddVault(t)
	const reason = "同一句"
	if code, _, errOut := runRelAddCLI(t, dir, relAddCardA, "supports", relAddCardB,
		"--reason", reason); code != ExitOK {
		t.Fatalf("首次 rel add 退出码 = %d：%s", code, errOut)
	}
	logBefore := gitLogCount(t, dir)
	if code, _, errOut := runRelAddCLI(t, dir, relAddCardA, "supports", relAddCardB,
		"--reason", reason); code != ExitOK {
		t.Fatalf("第二次 rel add 退出码 = %d：%s", code, errOut)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("重复写同一条关系不得产生空 commit：%d → %d", logBefore, got)
	}
	cardA := string(mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA)))
	if n := strings.Count(cardA, "- type: 'supports'"); n != 1 {
		t.Fatalf("relations[] 条数必须不变，实际 %d 条", n)
	}
}

// —— ⑤ 校验失败：零写入、零 commit ——

func TestRelAddValidationFailureIsZeroWrite(t *testing.T) {
	dir := relAddVault(t)
	logBefore := gitLogCount(t, dir)
	authBefore := authoritySnapshot(t, dir)

	// E3：原文 ID 写进 target（两组关系互串的硬拦）。
	code, env, _ := runRelAddCLI(t, dir, relAddCardA, "supports", "s-20260901-x", "--reason", "r")
	if code != ExitValidation {
		t.Fatalf("target 写成 s- 应退 2，实际 %d", code)
	}
	raw, _ := json.Marshal(env.Data["errors"])
	if !strings.Contains(string(raw), "E3") {
		t.Fatalf("errors[] 应含 E3：%s", raw)
	}
	// E2：目标卡不存在于全库。
	code, env, _ = runRelAddCLI(t, dir, relAddCardA, "supports", "k-20260909-missing", "--reason", "r")
	if code != ExitValidation {
		t.Fatalf("目标卡不存在应退 2（E2 校验失败），实际 %d", code)
	}
	raw, _ = json.Marshal(env.Data["errors"])
	if !strings.Contains(string(raw), "E2") {
		t.Fatalf("errors[] 应含 E2：%s", raw)
	}
	// type 出四值 / 缺 --reason / 位置参数个数不对：参数非法，退 1（合同 §4.1 / §4.5）。
	for _, c := range []struct {
		args []string
		what string
	}{
		{[]string{relAddCardA, "frobnicate", relAddCardB, "--reason", "x"}, "type 出四值"},
		{[]string{relAddCardA, "supports", relAddCardB}, "缺 --reason"},
		{[]string{relAddCardA, "supports"}, "位置参数不足"},
	} {
		// 人读模式：参数非法必须在 stderr 给出可定位提示（合同 §4.5）。
		setGitIdentity(t)
		code, _, errOut := runCLI(t, newTestRoot(t, dir),
			append([]string{"--vault", dir, "rel", "add"}, c.args...)...)
		if code != ExitUsage {
			t.Fatalf("%s 应退 1，实际 %d（%s）", c.what, code, errOut)
		}
		if strings.TrimSpace(errOut) == "" {
			t.Fatalf("%s 必须在 stderr 给出可定位提示", c.what)
		}
	}

	if s := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); s != "" {
		t.Fatalf("校验失败必须零写入，工作区却变了：%q", s)
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("校验失败不得产生 commit：%d → %d", logBefore, got)
	}
	// M6 起按合同 §2 的临界区口径逐条读「零写入」：校验在**锁内**发生（先取锁、后校验），
	// 因此退 2 的路径必然留下运行时锁 `.index/run.lock` —— 那是互斥痕迹，不是权威写入。
	// 权威 Markdown 必须逐字节不变；运行时目录里除锁之外一无所有（无事务日志 / intent /
	// commit / abort，也无任何索引库产物）。
	assertAuthorityUnchanged(t, dir, authBefore, "rel add 校验失败")
	assertRuntimeOnlyLock(t, dir, "rel add 校验失败")
	assertNoNewTxn(t, dir, nil, "rel add 校验失败")
}

// TestRelAddEmptyReasonIsW2NotBlocked —— 给了 `--reason ""`：不拦截，W2 照写并进报告（§4.1）。
func TestRelAddEmptyReasonIsW2NotBlocked(t *testing.T) {
	dir := relAddVault(t)
	code, env, errOut := runRelAddCLI(t, dir, relAddCardA, "supports", relAddCardB, "--reason", "")
	if code != ExitOK {
		t.Fatalf("空 reason 应照写并退 0，实际 %d：%s", code, errOut)
	}
	if !strings.Contains(relAddWarningCodes(env), "W2") {
		t.Fatalf("空 reason 必须记 W2：%s", relAddWarningCodes(env))
	}
	cardA := string(mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA)))
	if !strings.Contains(cardA, "- type: 'supports'") {
		t.Fatalf("W2 只提示不拦截，关系仍须写入：\n%s", cardA)
	}
}

// —— ⑥ B3 / B4：新写入路径同样受约束 ——

func TestRelAddRespectsB3AndB4(t *testing.T) {
	// B3：base 与磁盘不一致 → 跳过该文件并进报告，退 3；绝不覆盖、不强写。
	t.Run("B3_content_hash_mismatch_skips", func(t *testing.T) {
		dir := relAddVault(t)
		r := newTestRoot(t, dir)
		r.Now = func() time.Time { return captureAt(t) }
		setGitIdentity(t)
		inv := relAddInvocation(t, r, dir)
		p, _, err := r.buildRelationPlan(inv, relAddCardA, "supports", relAddCardB, "理由")
		if err != nil {
			t.Fatalf("组装 plan 失败：%v", err)
		}
		// 模拟「文件自 eg context 读取以来被改过」：base 记的是旧 hash。
		for k := range p.Base {
			p.Base[k] = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
		}
		beforeBytes := mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA))
		logBefore := gitLogCount(t, dir)

		res, runErr := runPlan(r, inv, p)
		if got := ExitCodeFor(runErr); got != ExitPartialWrite {
			t.Fatalf("content_hash 不匹配应退 3，实际 %d（%v）", got, runErr)
		}
		rep := applyReport(t, Envelope{Data: res.Data})
		if len(rep.Skipped) != 1 || rep.Skipped[0].Cause == "" {
			t.Fatalf("跳过项必须显式上报：%+v", rep.Skipped)
		}
		if !strings.Contains(strings.Join([]string{rep.Skipped[0].Kind, rep.Skipped[0].Cause}, " "),
			"content_hash") && !strings.Contains(rep.Skipped[0].Kind, "file_changed") {
			t.Fatalf("跳过项的 kind / cause 不符 B3 口径：%+v", rep.Skipped[0])
		}
		if got := mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA)); string(got) != string(beforeBytes) {
			t.Fatal("B3：被跳过的文件一个字节都不许改")
		}
		if got := gitLogCount(t, dir); got != logBefore {
			t.Fatalf("零实际写入不得产生 commit：%d → %d", logBefore, got)
		}
	})

	// B4：commit 失败 → 退 4，磁盘保留写入后的状态，不做任何破坏性还原。
	t.Run("B4_commit_failure_keeps_disk_state", func(t *testing.T) {
		dir := relAddVault(t)
		r := newTestRoot(t, dir)
		r.NewRepo = failingCommitRepo()
		code, env, errOut := runRelAddWith(t, r, dir, relAddCardA, "supports", relAddCardB,
			"--reason", "commit 失败也不回滚")
		if code != ExitCommitFailed {
			t.Fatalf("commit 失败应退 4，实际 %d：%s", code, errOut)
		}
		cardA := string(mustRead(t, relAddCardPath(dir, "ai-infra", relAddCardA)))
		if !strings.Contains(cardA, "- type: 'supports'") {
			t.Fatalf("B4：已写入的字节必须保留（不得回滚）：\n%s", cardA)
		}
		if s := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); s == "" {
			t.Fatal("B4：提交失败后工作区应保留改动（未被还原）")
		}
		if !strings.Contains(relAddWarningCodes(env)+errOut, "未做任何还原") {
			t.Fatalf("B4：报告必须写明磁盘保留现状、未做还原：%s / %s",
				relAddWarningCodes(env), errOut)
		}
	})
}

// relAddInvocation 造一个与 CLI 分发等价的 Invocation（供直接调用 runPlan 的 B3 用例）。
func relAddInvocation(t *testing.T, r *Root, dir string) *Invocation {
	t.Helper()
	cmd := r.Lookup("rel")
	fs := flag.NewFlagSet("eg rel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cmd.Flags(fs)
	if err := fs.Parse([]string{"--reason", "理由"}); err != nil {
		t.Fatalf("解析 flag 失败：%v", err)
	}
	cfg, err := loadConfigReadOnly(dir)
	if err != nil {
		t.Fatalf("读配置失败：%v", err)
	}
	return &Invocation{
		Cmd: cmd, Sub: "add", Args: []string{relAddCardA, "supports", relAddCardB},
		Flags: fs, VaultRoot: dir, VaultFlag: dir, Config: cfg,
		Out: io.Discard, Err: io.Discard,
	}
}

// —— ⑦ rel remove 的 M2 占位已被 M3 接管：命令层实现在场、占位字面量零残留 ——

// TestRelRemoveTakeoverReplacesPlaceholder：接管的**结构性**判据（行为判据在 rel_remove_test.go）。
//
// 三条都是等号或存在性，不是弱化的「或」：
//  1. S1/M2 冻结的七个 op 名单（plan.OpNames()）**仍不含** remove_relation——合同 §3 一个不加；
//  2. 它**必须**在 M3 新增的 8 个 op 里（plan.M3OpNames() / IsM3Op），即 plan 层已实装；
//  3. **命令层已接管**：`remove_relation` 的实现形态必须出现在 internal/cli 的非测试源里，
//     且只允许出现在 rel_remove.go（具名收窄，防第二处写入链路），同时占位字面量零残留。
func TestRelRemoveTakeoverReplacesPlaceholder(t *testing.T) {
	for _, name := range plan.OpNames() {
		if name == "remove_relation" {
			t.Fatal("remove_relation 不得进入 S1/M2 冻结的七个 op 名单（合同 §3 一个不加）")
		}
	}
	if !plan.IsM3Op("remove_relation") {
		t.Fatal("remove_relation 必须属 M3 新增 8 个 op（提案合同 §8.1 第 8 行）")
	}
	var m3Registered bool
	for _, name := range plan.M3OpNames() {
		if name == "remove_relation" {
			m3Registered = true
		}
	}
	if !m3Registered {
		t.Fatalf("plan.M3OpNames() 必须含 remove_relation，实得 %v", plan.M3OpNames())
	}

	var impl int
	for _, h := range grepNonTestSources(t, ".", "OpRemoveRelation") {
		if strings.HasPrefix(h, "rel_remove.go:") {
			impl++
			continue
		}
		if strings.Contains(h, "//") {
			continue // 注释里的阶段说明不算第二处实现
		}
		t.Fatalf("remove_relation 的命令层实现只允许落在 rel_remove.go，命中：%s", h)
	}
	if impl == 0 {
		t.Fatal("rel_remove.go 必须真实合成 plan.OpRemoveRelation（M2 占位由 T-…-044 接管）")
	}
	// 占位字面量零残留（判据 16）。逐字比对的针按运行期拼接，避免本文件自身成为命中。
	needle := "M3/S2 " + "未实现"
	if hits := grepNonTestSources(t, ".", needle); len(hits) != 0 {
		t.Fatalf("internal/cli 非测试源仍残留占位文案：%v", hits)
	}
}

// grepNonTestSources 在目录内的非测试 .go 文件里找一个字面量，返回「文件:行内容」。
func grepNonTestSources(t *testing.T, dir, needle string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读目录 %s 失败：%v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, needle) {
				out = append(out, name+": "+strings.TrimSpace(line))
			}
		}
	}
	return out
}
