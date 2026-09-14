package cli

// T-…-045 的 CLI 层机器判据：`eg edit` 是 M3 完成判据「**用户显式命令可改核心内容**」的
// 唯一命令载体（授权合同 §9 A-13 + §2 矩阵 #12 / #15 + §3 X1「需确认 = 否」+ §6 U-03）。
//
// 它与 T-…-038 的 `TestE6_AgentAppendCoreKnowledgeRejected`（Agent 自动路径改核心内容 → 退 2）
// 构成同一条判据的正负两半：这里只证「用户显式路径真的能改、且只改该改的那一段」。
//
// 全部用例走**真实** vault + 真实 Root.Run + 真实 git 仓：断言退出码、磁盘字节与 commit 计数，
// 唯一注入的是时间。

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// runEditCLI 跑一次 `eg edit … --json`，返回退出码、信封与 stderr。
func runEditCLI(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return captureAt(t) }
	code, out, errOut := runCLI(t, r,
		append([]string{"--vault", dir, "--json", "edit"}, args...)...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

// editSectionBody 取文件里某分区的正文（半开区间 Body..End，含尾随空行）。
func editSectionBody(t *testing.T, body, section string) string {
	t.Helper()
	doc, err := mdfile.Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	span, ok := doc.Section(section)
	if !ok {
		t.Fatalf("分区「%s」不存在：\n%s", section, body)
	}
	return body[span.Body:span.End]
}

// —— ① 判据 8 的正例：用户显式路径可改「知识内容」 ——

// TestEdit_UserExplicitCoreKnowledgeAccepted 断言：`eg edit … --user-request` 改写
// 「知识内容」→ 退 0、**内容逐字生效**（字符串相等断言）、恰产生 1 次 commit。
func TestEdit_UserExplicitCoreKnowledgeAccepted(t *testing.T) {
	dir := applyVault(t)
	cardRel := mustCardRel(t, dir)
	abs := filepath.Join(dir, filepath.FromSlash(cardRel))
	before := string(mustRead(t, abs))
	logBefore := gitLogCount(t, dir)

	const payload = "用户显式改写的新结论。\n\n第二段：带空行也必须逐字保留。\n"
	code, env, errOut := runEditCLI(t, dir, "--target", applyCardID,
		"--section", mdfile.SecKnowledge, "--content", payload, "--"+UserRequestFlag)
	if code != ExitOK {
		t.Fatalf("用户显式改核心内容应退 0，实际 %d（%s）\n%v", code, errOut, envMessages(env))
	}

	after := string(mustRead(t, abs))
	// 内容逐字生效：分区正文恰等于「一个空行 + 载荷 + 一个空行」的分区骨架形态，
	// 载荷本身一个字节不动（空行属分区骨架，新建产物也是这两个字节）。
	if got, want := editSectionBody(t, after, mdfile.SecKnowledge), "\n"+payload+"\n"; got != want {
		t.Fatalf("「知识内容」正文 = %q，期望 %q", got, want)
	}
	if after == before {
		t.Fatal("成功编辑必须改动字节（Git diff 可见）")
	}
	// 恰一次 commit，且工作区干净（写入与提交同一步，不留未提交改动）。
	if got := gitLogCount(t, dir); got != logBefore+1 {
		t.Fatalf("一次 eg edit 应恰产生一次 commit：%d → %d", logBefore, got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("写入必须一并提交，工作区应干净，实得 %q", got)
	}
	if subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s")); !strings.HasPrefix(subject, "process(") {
		t.Fatalf("commit 主题应以 process( 开头（沿用既有 verb，不新增动词），实得 %q", subject)
	}
	// 报告结构复用：编辑计入既有 cards.updated / links（不新增报告键）。
	rep := applyReport(t, env)
	if len(rep.Skipped) != 0 {
		t.Fatalf("成功编辑不应有 skipped[]：%+v", rep.Skipped)
	}
	if len(rep.Cards.Updated) == 0 {
		t.Fatalf("编辑结果必须计入报告 cards.updated：%+v", rep.Cards)
	}
}

// —— ② 反伪造 N-1：缺命令行 --user-request → 退 2 + 零写入 ——

// TestEdit_RequiresUserRequest 断言：`eg edit` 自己在 plan 里写 `initiator: user`
// **不能自证**授权——缺命令行佐证时落 P-A，被矩阵 #12 拦下退 2、零写入、零 commit。
func TestEdit_RequiresUserRequest(t *testing.T) {
	dir := applyVault(t)
	cardRel := mustCardRel(t, dir)
	abs := filepath.Join(dir, filepath.FromSlash(cardRel))
	before := string(mustRead(t, abs))
	logBefore := gitLogCount(t, dir)

	code, env, errOut := runEditCLI(t, dir, "--target", applyCardID,
		"--section", mdfile.SecKnowledge, "--content", "偷偷改核心内容。\n")
	if code != ExitValidation {
		t.Fatalf("缺 --user-request 必须退 2（而非用法错误 1），实际 %d（%s）", code, errOut)
	}
	if !hasCode(env, "E6") {
		t.Fatalf("必须是 E6（不新增诊断码）：%v", envMessages(env))
	}
	if got := string(mustRead(t, abs)); got != before {
		t.Fatal("被拒的命令不得改动目标文件的任何字节")
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("零写入必须零 commit：%d → %d", logBefore, got)
	}
	// 补上命令行佐证后同一次编辑成立：拦的是**缺佐证**，不是拒绝这个动作。
	code2, env2, errOut2 := runEditCLI(t, dir, "--target", applyCardID,
		"--section", mdfile.SecKnowledge, "--content", "偷偷改核心内容。\n", "--"+UserRequestFlag)
	if code2 != ExitOK {
		t.Fatalf("带 --user-request 后必须退 0，实际 %d（%s）\n%v", code2, errOut2, envMessages(env2))
	}
}

// —— ③ U-03：「用户补充」永不由 CLI 写入（表驱动 2 行）——

// TestEdit_UserSectionNeverWritten 断言：`--section 用户补充` 在带 / 不带
// `--user-request` 两种形态下**均**退 2 + E6 + 文件字节不变（矩阵 #15 / #25 两路径同 🔴）。
func TestEdit_UserSectionNeverWritten(t *testing.T) {
	for _, c := range []struct {
		name  string
		extra []string
	}{
		{"不带 --user-request（落 P-A）", nil},
		{"带 --user-request（P-U 也不行）", []string{"--" + UserRequestFlag}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := applyVault(t)
			cardRel := mustCardRel(t, dir)
			abs := filepath.Join(dir, filepath.FromSlash(cardRel))
			before := string(mustRead(t, abs))
			logBefore := gitLogCount(t, dir)

			args := append([]string{"--target", applyCardID,
				"--section", mdfile.SecUserAppend, "--content", "越界写用户补充。\n"}, c.extra...)
			code, env, errOut := runEditCLI(t, dir, args...)
			if code != ExitValidation {
				t.Fatalf("「用户补充」在 %s 也必须退 2，实退 %d（%s）", c.name, code, errOut)
			}
			if !hasCode(env, "E6") {
				t.Fatalf("必须是 E6（不新增编号）：%v", envMessages(env))
			}
			if got := string(mustRead(t, abs)); got != before {
				t.Fatal("被拒后目标文件字节必须逐字不变（B2 / U-03）")
			}
			if got := gitLogCount(t, dir); got != logBefore {
				t.Fatalf("零写入必须零 commit：%d → %d", logBefore, got)
			}
		})
	}
}

// —— ④ 三个正交维度：编辑正文不牵连 status / 删除维度 / 过目维度 ——

// TestEdit_DoesNotTouchReviewedAt 断言：`eg edit` 成功后 `reviewed_at` 逐字不变
// （`reviewed_at` 只由 `eg mark-reviewed` 写，A-16 / 矩阵 #7），且既没被新增也没被刷新；
// 同时反证 `status` 与删除维度一格未动。
func TestEdit_DoesNotTouchReviewedAt(t *testing.T) {
	dir := applyVault(t)
	cardRel := mustCardRel(t, dir)
	abs := filepath.Join(dir, filepath.FromSlash(cardRel))

	// ① 未过目的卡：编辑后**不得**凭空长出 reviewed_at（编辑正文 ≠ 用户过目了它）。
	if strings.Contains(string(mustRead(t, abs)), "reviewed_at") {
		t.Fatal("前置条件：新建的卡不应带 reviewed_at")
	}
	code, env, errOut := runEditCLI(t, dir, "--target", applyCardID,
		"--section", mdfile.SecKnowledge, "--content", "第一次改写。\n", "--"+UserRequestFlag)
	if code != ExitOK {
		t.Fatalf("编辑应退 0，实际 %d（%s）\n%v", code, errOut, envMessages(env))
	}
	if strings.Contains(string(mustRead(t, abs)), "reviewed_at") {
		t.Fatalf("编辑正文不得写 reviewed_at：\n%s", mustRead(t, abs))
	}

	// ② 已过目的卡：先用唯一写入路径 mark-reviewed 落一个 reviewed_at，再编辑正文。
	if code, _, errOut := runStateCLI(t, dir, "mark-reviewed", "--target", applyCardID); code != ExitOK {
		t.Fatalf("前置 mark-reviewed 应退 0，实际 %d（%s）", code, errOut)
	}
	marked := string(mustRead(t, abs))
	reviewedBefore := fmValue(t, marked, "reviewed_at")
	if reviewedBefore == "" {
		t.Fatalf("前置条件：mark-reviewed 必须落下 reviewed_at：\n%s", marked)
	}
	statusBefore := fmValue(t, marked, "status")
	updatedBefore := fmValue(t, marked, "updated_at")

	code2, env2, errOut2 := runEditCLI(t, dir, "--target", applyCardID,
		"--section", mdfile.SecKnowledge, "--content", "第二次改写。\n", "--"+UserRequestFlag)
	if code2 != ExitOK {
		t.Fatalf("编辑应退 0，实际 %d（%s）\n%v", code2, errOut2, envMessages(env2))
	}
	after := string(mustRead(t, abs))
	if got := fmValue(t, after, "reviewed_at"); got != reviewedBefore {
		t.Fatalf("reviewed_at 必须逐字不变：%q → %q（过目维度与内容维度正交）", reviewedBefore, got)
	}
	if got := fmValue(t, after, "status"); got != statusBefore {
		t.Fatalf("status 必须逐字不变：%q → %q", statusBefore, got)
	}
	if got := fmValue(t, after, "updated_at"); got != updatedBefore {
		t.Fatalf("updated_at 不由本命令刷新（B1 只追加不改写既有键）：%q → %q", updatedBefore, got)
	}
	if strings.Contains(after, "deleted_at") || strings.Contains(after, "deleted_reason") {
		t.Fatalf("编辑正文不得引入删除维度：\n%s", after)
	}
	if countSub(after, "reviewed_at:") != 1 {
		t.Fatalf("reviewed_at 只能有一处，实得 %d 处：\n%s", countSub(after, "reviewed_at:"), after)
	}
	// 内容确实换了（证明上面的「没变」不是因为整条命令没生效）。
	if got, want := editSectionBody(t, after, mdfile.SecKnowledge), "\n第二次改写。\n\n"; got != want {
		t.Fatalf("「知识内容」正文 = %q，期望 %q", got, want)
	}
}

// —— ⑤ 参数分级：缺必填退 1、零写入；命令不收确认参数 ——

// TestEdit_UsageErrors 断言参数形态错误一律退 1（零写入），且 `eg edit` 不认 `--confirm`
// （§3 X1「需确认 = 否」，退出码 6 的白名单只有 proposal approve 与 delete 两条）。
func TestEdit_UsageErrors(t *testing.T) {
	dir := applyVault(t)
	cardRel := mustCardRel(t, dir)
	abs := filepath.Join(dir, filepath.FromSlash(cardRel))
	before := string(mustRead(t, abs))

	for _, c := range []struct {
		name string
		args []string
	}{
		{"缺 --target", []string{"--section", mdfile.SecKnowledge, "--content", "x\n"}},
		{"缺 --section", []string{"--target", applyCardID, "--content", "x\n"}},
		{"缺 --content", []string{"--target", applyCardID, "--section", mdfile.SecKnowledge}},
		{"content 为空", []string{"--target", applyCardID,
			"--section", mdfile.SecKnowledge, "--content", "   "}},
		{"多余位置参数", []string{"k-x", "--target", applyCardID,
			"--section", mdfile.SecKnowledge, "--content", "x\n"}},
		{"不认 --confirm", []string{"--target", applyCardID,
			"--section", mdfile.SecKnowledge, "--content", "x\n", "--confirm"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, _, _ := runEditCLI(t, dir, append(c.args, "--"+UserRequestFlag)...)
			if code != ExitUsage {
				t.Fatalf("%s 应退 1，实际 %d", c.name, code)
			}
			if got := string(mustRead(t, abs)); got != before {
				t.Fatal("参数非法必须零写入")
			}
		})
	}
}

// TestEdit_ContentFromFile 断言 `--content` 的文件口径：值是既存普通文件时按字节读入，
// 内容逐字生效（与字面文本口径同一条落盘路径）。
func TestEdit_ContentFromFile(t *testing.T) {
	dir := applyVault(t)
	cardRel := mustCardRel(t, dir)
	abs := filepath.Join(dir, filepath.FromSlash(cardRel))

	const payload = "来自文件的新正文。\n\n  缩进行也必须逐字保留。\n"
	src := filepath.Join(t.TempDir(), "new-body.md")
	writeFileMk(t, src, payload)

	code, env, errOut := runEditCLI(t, dir, "--target", applyCardID,
		"--section", mdfile.SecRationale, "--content", src, "--"+UserRequestFlag)
	if code != ExitOK {
		t.Fatalf("文件口径应退 0，实际 %d（%s）\n%v", code, errOut, envMessages(env))
	}
	after := string(mustRead(t, abs))
	if got, want := editSectionBody(t, after, mdfile.SecRationale), "\n"+payload+"\n"; got != want {
		t.Fatalf("「解释与依据」正文 = %q，期望 %q", got, want)
	}
	// 其它分区不受牵连。
	if !strings.Contains(after, "## "+mdfile.SecKnowledge) || !strings.Contains(after, "## "+mdfile.SecSelfCheck) {
		t.Fatalf("其它分区必须逐字保留：\n%s", after)
	}
}
