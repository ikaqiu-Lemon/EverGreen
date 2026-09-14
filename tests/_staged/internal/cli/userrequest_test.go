package cli

// T-…-038 命令侧的验收：`--user-request` 全局 flag 的**必要性**，以及
// 十三条不可授权项里由 task 点名的六条机器反证（U-01 / U-02 / U-03 / U-05 / U-12 / U-13）。
//
// 全部用例走真实 vault + 真实 Root.Run：断言的是**退出码**与**磁盘字节**，
// 不是内部函数的返回值——授权是端到端行为，只在内部断言等于没断言。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
)

// —— ① `--user-request` 是 P-U 的必要条件（反伪造 N-1）——

// TestUserRequestFlagRequired 断言：plan 内写 `initiator: user` 但命令行**无**
// `--user-request` → 退 2；补上 flag 后同一份 plan 不再因授权失败。
func TestUserRequestFlagRequired(t *testing.T) {
	dir := applyVault(t)
	cardRel := mustCardRel(t, dir)
	before := mustRead(t, filepath.Join(dir, filepath.FromSlash(cardRel)))

	// deprecate 是五个状态类 op 之一：缺授权 → W7 error → 退 2、零写入。
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"用户要求失效",
"base":{},"ops":[{"op":"deprecate","target":"` + applyCardID + `",
"reason":"已被新卡取代","initiator":"user"}]}`

	code, _, errOut := runApplyPlan(t, dir, plan)
	if code != ExitValidation {
		t.Fatalf("plan 内的 initiator: user 不得自证授权（N-1），退出码 = %d，期望 2：%s", code, errOut)
	}
	after := mustRead(t, filepath.Join(dir, filepath.FromSlash(cardRel)))
	if string(after) != string(before) {
		t.Fatal("被拒的 plan 不得改动目标文件的任何字节")
	}

	// 同一份 plan + 命令行佐证：不再因授权失败（落盘语义归 T-…-039，这里只看授权）。
	code, env, errOut := runApplyPlan(t, dir, plan, "--"+UserRequestFlag)
	if code == ExitValidation {
		t.Fatalf("带 --user-request 后不得再因授权失败：%s\n%v", errOut, envMessages(env))
	}
}

// TestUserRequestFlagIsGlobal 断言 `--user-request` 是**全局** flag：
// 写在子命令之前与之后等价，且不会被当成未知 flag 拒掉。
func TestUserRequestFlagIsGlobal(t *testing.T) {
	dir := applyVault(t)
	r := newTestRoot(t, dir)
	for _, args := range [][]string{
		{"--" + UserRequestFlag, "config", "get", "default_domain", "--vault", dir, "--json"},
		{"config", "get", "default_domain", "--vault", dir, "--json", "--" + UserRequestFlag},
	} {
		code, _, errOut := runCLI(t, r, args...)
		if code != ExitOK {
			t.Fatalf("%v 应退 0（全局 flag 在子命令前后等价），实退 %d：%s", args, code, errOut)
		}
	}
	if !strings.Contains(r.Usage(), "--"+UserRequestFlag) {
		t.Fatal("--help 的「全局 flag：」块必须说明 --user-request")
	}
}

// —— ② 十三条不可授权项的清单本体 ——

// TestUnauthorizableExactlyThirteen 断言编号 U-01..U-13 连续、恰 13 条、无重号。
func TestUnauthorizableExactlyThirteen(t *testing.T) {
	items := Unauthorizable()
	if len(items) != 13 {
		t.Fatalf("不可授权项恰 13 条（合同 §6），实得 %d", len(items))
	}
	seen := map[string]bool{}
	for i, it := range items {
		want := "U-" + twoDigits(i+1)
		if it.Num != want {
			t.Fatalf("第 %d 条编号 = %q，期望 %q（必须连续）", i+1, it.Num, want)
		}
		if seen[it.Num] {
			t.Fatalf("编号 %s 重复", it.Num)
		}
		seen[it.Num] = true
		if it.Name == "" || it.Basis == "" || it.Alternative == "" {
			t.Fatalf("%s 的名称 / 依据 / 替代路径不得为空：%+v", it.Num, it)
		}
		if _, ok := UnauthorizableNum(it.Num); !ok {
			t.Fatalf("UnauthorizableNum(%s) 查不到", it.Num)
		}
	}
}

// —— ③ U-01：知识产物永不物理删除 ——

// TestU01_NoPhysicalDeleteOfKnowledgeArtifacts 双向取证：
// 运行期守卫拒绝三个知识产物根下的一切物理删除；且 CLI 层不存在删除知识产物的代码路径。
func TestU01_NoPhysicalDeleteOfKnowledgeArtifacts(t *testing.T) {
	if n := len(KnowledgeRoots()); n != 3 {
		t.Fatalf("知识产物根恰 3 个（domains / sources / proposals），实得 %d：%v", n, KnowledgeRoots())
	}
	for _, rel := range []string{
		"domains/ai-infra/knowledge/k-20260901-attention.md",
		"domains/ai-infra/notes/n-20260901-demo.md",
		"sources/s-20260901-demo.md",
		"proposals/p-20261020-001.md",
	} {
		err := GuardNoPhysicalDelete(rel)
		if err == nil {
			t.Fatalf("GuardNoPhysicalDelete(%q) 必须拒绝（U-01）", rel)
		}
		if !strings.Contains(err.Error(), "U-01") {
			t.Fatalf("拒绝理由必须点名 U-01：%v", err)
		}
	}
	// 非知识产物（原子写的临时文件、工程文件）不在 U-01 的适用范围内：
	// 禁的是「产物被抹掉」，不是禁一切文件系统删除——否则原子写本身就不成立。
	for _, rel := range []string{"evergreen.yml", "SKILL.md", "unprocessed.md", ".git/index"} {
		if err := GuardNoPhysicalDelete(rel); err != nil {
			t.Fatalf("%q 不是知识产物，不该被 U-01 拦：%v", rel, err)
		}
	}

	// 代码路径反证：CLI 层的 os.Remove 只许出现在原子写的临时文件清理里。
	hits := grepNonTest(t, "internal/cli", regexp.MustCompile(`os\.Remove\b`))
	for _, h := range hits {
		if !strings.HasSuffix(h.file, "ymlwrite.go") {
			t.Fatalf("CLI 层出现疑似删除知识产物的代码路径：%s:%d %s", h.file, h.line, h.text)
		}
	}
}

// —— ④ U-02：无破坏性回滚 ——

// TestU02_NoDestructiveRollback 断言产品代码里零出现破坏性动作（B4 / ADR-05）。
//
// **T-…-065 重钉（2026-09-08）**：M5 的 `.index/` 是**可重建派生物**，「重建 = 先整目录删除
// 再全量构建」是它的正确语义（架构合同 §4.3「不兼容 = 重建，永不迁移」）。把这条删除也判成
// U-02 会把「派生物重建」和「回滚权威产物」混为一谈，因此按**最小面**开一个带正面判据的例外：
//
//	① 例外**只**覆盖 `internal/index/` 一个包，其它任何包命中即判红（口径一字未放宽）；
//	② 例外只认参数逐字是索引目录形参 `dir` 的调用 —— 拿别的路径去删一律不豁免；
//	③ 例外配一条**正面反证**：`internal/index/` 的非测试源里不得出现任何权威产物根
//	   （`domains` / `sources` / `proposals` / `reviews`）字面量，也不得出现 `..`
//	   —— 于是「那个 dir 只可能是 `.index/`」由文件内容结构性保证，而不是靠注释承诺。
func TestU02_NoDestructiveRollback(t *testing.T) {
	// 被禁字面量**拼接构造**：直接写完整字面量会让本用例自己成为命中项。
	pat := regexp.MustCompile(strings.Join([]string{
		"checkout" + " --", "reset" + " --hard", "Remove" + "All",
	}, "|"))
	// ② 唯一被豁免的调用形态：删的就是索引目录形参本身。
	derivedOnly := regexp.MustCompile(`os\.Remove` + `All\(dir\)`)
	for _, dir := range []string{"internal", "cmd"} {
		var bad []codeHit
		for _, h := range grepNonTest(t, dir, pat) {
			// ① 包级白名单恰一个：internal/index（派生索引，删掉零信息损失）。
			if strings.HasPrefix(h.file, "internal/index/") && derivedOnly.MatchString(h.text) {
				continue
			}
			bad = append(bad, h)
		}
		if len(bad) > 0 {
			t.Fatalf("%s 出现破坏性回滚动作（U-02）：%+v", dir, bad)
		}
	}
	// ③ 正面反证：索引包不认识任何权威产物根，也不做路径上溯 ——
	// 因此上面豁免掉的 `dir` 在语义上不可能指向权威文件。
	authority := regexp.MustCompile(`"domains"|"sources"|"proposals"|"reviews"|"\.\."`)
	if hits := grepNonTest(t, "internal/index", authority); len(hits) > 0 {
		t.Fatalf("internal/index 出现权威产物根 / 路径上溯字面量，U-02 的派生物例外不再成立：%+v", hits)
	}
}

// —— ⑤ U-03：CLI 永不写「用户补充」（两条路径均 E6）——

// TestU03_UserSectionRejectedOnBothPaths 端到端取证：带不带 --user-request 都退 2。
func TestU03_UserSectionRejectedOnBothPaths(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra []string
		ops   string
	}{
		{"Agent 自动路径", nil,
			`{"op":"append_card","card":"` + applyCardID + `","sections":{"用户补充":"越界。\n"}}`},
		{"用户显式路径", []string{"--" + UserRequestFlag},
			`{"op":"append_card","card":"` + applyCardID + `","initiator":"user",
"sections":{"用户补充":"越界。\n"}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := applyVault(t)
			cardRel := mustCardRel(t, dir)
			before := mustRead(t, filepath.Join(dir, filepath.FromSlash(cardRel)))

			plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"越界写用户补充",
"base":{},"ops":[` + c.ops + `]}`
			code, env, errOut := runApplyPlan(t, dir, plan, c.extra...)
			if code != ExitValidation {
				t.Fatalf("「用户补充」在 %s 也必须退 2，实退 %d：%s", c.name, code, errOut)
			}
			if !hasCode(env, "E6") {
				t.Fatalf("必须是 E6（不新增编号）：%v", envMessages(env))
			}
			after := mustRead(t, filepath.Join(dir, filepath.FromSlash(cardRel)))
			if string(after) != string(before) {
				t.Fatal("被拒后目标文件字节必须不变（B2）")
			}
		})
	}
}

// —— ⑥ U-13：未处理提案不影响退出码 ——

// TestPendingProposalDoesNotAffectExitCode 断言存在 pending 提案时 `eg apply` 仍退 0。
func TestPendingProposalDoesNotAffectExitCode(t *testing.T) {
	dir := applyVault(t)
	prel := pendingProposalFixture(t, dir)
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(prel))); err != nil {
		t.Fatalf("前置条件：pending 提案必须在盘上：%v", err)
	}

	code, env, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitOK {
		t.Fatalf("未处理提案不得影响退出码（U-13），实退 %d：%s", code, errOut)
	}
	// 不阻塞、不催办：报告里不得出现待办 / 红点式的提示。
	raw := strings.Join(envMessages(env), "\n")
	for _, banned := range []string{"待办", "红点", "催办", "请先处理"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("报告里出现催办字样 %q（U-13 明令无提醒、无红点、无待办）", banned)
		}
	}
	if PendingProposalsAreNonBlocking == "" {
		t.Fatal("U-13 的口径常量不得为空")
	}
}

// —— 用例专用小工具 ——

// mustCardRel 先落一篇笔记与一张卡，返回卡的相对路径（授权用例的共同前置）。
func mustCardRel(t *testing.T, dir string) string {
	t.Helper()
	_, cardRel := applyNoteAndCard(t, dir)
	if cardRel == "" {
		t.Fatal("前置语料缺失：卡未落盘")
	}
	return cardRel
}

// pendingProposalFixture 落一份 **pending** 提案（提案子命令属 T-…-040，这里直接用
// internal/proposal 的生产模板渲染，不手搓夹具字符串）。
func pendingProposalFixture(t *testing.T, dir string) string {
	t.Helper()
	raw, err := proposal.RenderTemplate(proposal.Template{
		ID: proposal.ID("p-20261020-002"), Title: "逻辑删除一张过时卡", CreatedAt: "2026-10-20",
		Targets: []string{applyCardID},
		Impact:  proposal.Impact{ExitsDefaultView: []string{applyCardID}},
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	rel := proposal.Rel(proposal.ID("p-20261020-002"))
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("建 proposals 目录：%v", err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatalf("写提案：%v", err)
	}
	return rel
}

// twoDigits 把 1..13 渲染成两位（U-01..U-13 的编号形态）。
func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// hasCode 报告信封里是否出现某个诊断码（errors 与 warnings 都算）。
func hasCode(env Envelope, code string) bool {
	for _, m := range envMessages(env) {
		if strings.Contains(m, code) {
			return true
		}
	}
	return false
}

// envMessages 抽出信封里全部诊断的「码 + 文案」（只读，供反证 grep）。
func envMessages(env Envelope) []string {
	var out []string
	for _, d := range env.Warnings {
		out = append(out, d.Code+" "+d.Message)
	}
	raw, err := json.Marshal(env.Data)
	if err == nil {
		out = append(out, string(raw))
	}
	return out
}

// codeHit 是一次源码 grep 命中。
type codeHit struct {
	file string
	line int
	text string
}

// grepNonTest 在仓库某个目录下按正则搜非测试 Go 文件（反证用；不依赖外部 grep）。
func grepNonTest(t *testing.T, dir string, pat *regexp.Regexp) []codeHit {
	t.Helper()
	root := repoRootForGuard(t)
	var hits []codeHit
	err := filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		for i, line := range strings.Split(string(raw), "\n") {
			if pat.MatchString(line) {
				hits = append(hits, codeHit{file: filepath.ToSlash(rel), line: i + 1,
					text: strings.TrimSpace(line)})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s：%v", dir, err)
	}
	return hits
}

// repoRootForGuard 从当前包目录（internal/cli）上溯到仓库根。
func repoRootForGuard(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd：%v", err)
	}
	return filepath.Dir(filepath.Dir(wd))
}
