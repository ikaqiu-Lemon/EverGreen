package cli

// T-…-010 的 CLI 侧验收：eg context 的白名单输出与 base。
// 用例名以 Context 开头，可用 `go test ./internal/cli/... -run Context` 单独跑。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeVaultFile 往 vault 里放一个测试用文件（测试脚手架，不走产品写路径）。
func writeVaultFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

// contextVault 建一个已收录 s-20260901-* 原文的 vault（复用 T-…-009 的收录路径）。
func contextVault(t *testing.T) (string, string) {
	t.Helper()
	dir := captureVault(t)
	env := captureOnce(t, dir,
		"--url", "https://example.com/attention", "--title", "注意力机制入门",
		"--reason", "为 context 用例准备原文")
	id, _ := env.Data["source_id"].(string)
	if id == "" {
		t.Fatalf("收录未返回 source_id：%+v", env.Data)
	}
	return dir, id
}

func runContextCLI(t *testing.T, dir string, args ...string) (int, Envelope, string) {
	t.Helper()
	code, out, errOut := runCLI(t, newTestRoot(t, dir),
		append([]string{"context", "--vault", dir, "--json"}, args...)...)
	var env Envelope
	if out != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

// —— ① data 键锁定 + 只读零副作用 ——

func TestContextDataKeysAndReadOnly(t *testing.T) {
	dir, id := contextVault(t)
	before := snapshot(t, dir)
	beforeLog := gitOut(t, dir, "log", "--oneline")

	code, env, errOut := runContextCLI(t, dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("eg context 退出码 = %d，期望 0：%s", code, errOut)
	}
	want := []string{"base", "candidates", "cards", "default_domain",
		"default_domain_fallback", "domain", "notes", "proposals", "source"}
	var got []string
	for k := range env.Data {
		got = append(got, k)
	}
	sortStrings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("data 键 = %v，期望 %v", got, want)
	}
	if env.Data["domain"] != "ai-infra" || env.Data["default_domain_fallback"] != true {
		t.Fatalf("领域回退信息不对：%+v", env.Data)
	}

	// 只读：文件树、git 工作区、git log 都不变。
	if after := snapshot(t, dir); after != before {
		t.Fatalf("eg context 必须零文件变化：\n前=%s\n后=%s", before, after)
	}
	if s := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); s != "" {
		t.Fatalf("eg context 后工作区必须干净：%q", s)
	}
	if gitOut(t, dir, "log", "--oneline") != beforeLog {
		t.Fatalf("eg context 必须零 commit")
	}
}

// —— ② 输出不含 S3 才有的字段与标记；S2 只允许提案「摘要」 ——
//
// M3（T-…-033）重钉：提案控制面在 S2 落地后，`data.proposals` 是**合同要求**的键
// （提案合同 §10.3：eg context 只给标题与 targets 摘要供去重）。因此这里由
// 「一律禁 proposal 字样」改钉为**更严的三条同时成立**：
//  1. reviews / reviewed 与三个 S3 标记仍一律禁（过目与「材料支持不足」不属 M3）；
//  2. 库里没有提案时，`data.proposals` 必须是**空**列表（不得凭空造提案）；
//  3. 提案正文七分区的 H2 标题一个都不许出现在输出里（正文永不进 context）。
func TestContextOmitsStageTwoFields(t *testing.T) {
	dir, id := contextVault(t)
	code, env, _ := runContextCLI(t, dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("退出码 = %d", code)
	}
	if ps, ok := env.Data["proposals"].([]interface{}); !ok || len(ps) != 0 {
		t.Fatalf("库里没有提案时 data.proposals 必须为空列表，实为 %#v", env.Data["proposals"])
	}
	_, out, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--json", "--source", id)
	banned := []string{
		"reviews", "reviewed",
		"[已删除]", "[未过目]", "[材料支持不足]",
	}
	// 提案正文的七个 H2 逐字禁出（与 internal/proposal 的分区表同一批字面量）。
	banned = append(banned, "推荐修改", "理由与证据", "影响的文件、领域与关系",
		"执行后状态", "不执行的影响", "替代方案", "可应用内容")
	for _, b := range banned {
		if strings.Contains(out, b) {
			t.Fatalf("输出不得含 %q：\n%s", b, out)
		}
	}
}

// —— ②′ 有提案时：context 只出 title + targets 摘要，正文一个字都不出 ——
//
// 这是 T-…-033「eg context 只给摘要」在 **CLI 出口**上的判定：
// JSON 的 data.proposals 与人读 summary 同源同事实，且两侧都拿不到正文。
func TestContextProposalSummaryOnly(t *testing.T) {
	dir, id := contextVault(t)
	const secret = "只出现在提案正文里的哨兵句"
	writeVaultFile(t, dir, "proposals/p-20260701-001.md", ""+
		"---\n"+
		"id: p-20260701-001\n"+
		"type: logical_delete\n"+
		"status: pending\n"+
		"created_at: '2026-07-01T10:00:00+08:00'\n"+
		"targets:\n  - k-20260601-001\n  - k-20260601-002\n"+
		"---\n\n"+
		"# 逻辑删除两张过时卡\n\n"+
		"## 推荐修改\n\n"+secret+"\n\n"+
		"## 理由与证据\n\n"+secret+"\n")

	code, env, errOut := runContextCLI(t, dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("退出码 = %d：%s", code, errOut)
	}
	ps, ok := env.Data["proposals"].([]interface{})
	if !ok || len(ps) != 1 {
		t.Fatalf("data.proposals 应恰 1 项，实为 %#v", env.Data["proposals"])
	}
	item, ok := ps[0].(map[string]interface{})
	if !ok {
		t.Fatalf("提案摘要不是对象：%#v", ps[0])
	}
	var keys []string
	for k := range item {
		keys = append(keys, k)
	}
	sortStrings(keys)
	if strings.Join(keys, ",") != "id,path,targets,title" {
		t.Fatalf("提案摘要键 = %v，期望恰 id,path,targets,title", keys)
	}
	if item["title"] != "逻辑删除两张过时卡" {
		t.Fatalf("摘要 title = %v", item["title"])
	}
	tg, _ := item["targets"].([]interface{})
	if len(tg) != 2 || tg[0] != "k-20260601-001" || tg[1] != "k-20260601-002" {
		t.Fatalf("摘要 targets = %#v", item["targets"])
	}

	// 人读模式：摘要行有 id/title/targets，没有正文。
	_, text, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--source", id)
	for _, wantLine := range []string{"p-20260701-001", "逻辑删除两张过时卡", "k-20260601-001"} {
		if !strings.Contains(text, wantLine) {
			t.Fatalf("人读输出缺 %q：\n%s", wantLine, text)
		}
	}
	_, jsonOut, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--json", "--source", id)
	for _, out := range []string{text, jsonOut} {
		if strings.Contains(out, secret) {
			t.Fatalf("提案正文泄漏进 context：\n%s", out)
		}
		for _, h2 := range []string{"推荐修改", "理由与证据"} {
			if strings.Contains(out, h2) {
				t.Fatalf("提案正文分区标题 %q 泄漏进 context：\n%s", h2, out)
			}
		}
	}
}

// —— ③ --domain 不在 evergreen.yml → 退 1、零输出内容、零写入 ——

func TestContextRejectsUnknownDomain(t *testing.T) {
	dir, id := contextVault(t)
	before := snapshot(t, dir)
	code, out, errOut := runCLI(t, newTestRoot(t, dir),
		"context", "--vault", dir, "--json", "--source", id, "--domain", "not-registered")
	if code != ExitUsage {
		t.Fatalf("未登记领域退出码 = %d，期望 1：%s", code, errOut)
	}
	var env Envelope
	if out != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("stdout 不可解析：%v\n%s", err, out)
		}
		// 参数非法：data 里只允许框架的 errors[]，不得出现任何上下文内容。
		for k := range env.Data {
			if k != "errors" {
				t.Fatalf("参数非法时不得输出内容，多出键 %q：%+v", k, env.Data)
			}
		}
	}
	if after := snapshot(t, dir); after != before {
		t.Fatalf("参数非法必须零写入")
	}
}

// —— ④ 目标不存在 → 退 2 ——

func TestContextMissingTargetExitsTwo(t *testing.T) {
	dir, _ := contextVault(t)
	code, _, errOut := runContextCLI(t, dir, "--source", "s-19700101-nope")
	if code != ExitValidation {
		t.Fatalf("对象不存在退出码 = %d，期望 2：%s", code, errOut)
	}
}

// —— ⑤ base 直接可填进 plan.base：键为路径、值带 sha256: 前缀，且含收件区 ——

func TestContextBaseIsPlanReady(t *testing.T) {
	dir, id := contextVault(t)
	_, env, _ := runContextCLI(t, dir, "--source", id)
	base, ok := env.Data["base"].(map[string]interface{})
	if !ok || len(base) == 0 {
		t.Fatalf("base 缺失或为空：%+v", env.Data["base"])
	}
	for k, v := range base {
		s, _ := v.(string)
		if !strings.HasPrefix(s, "sha256:") {
			t.Fatalf("base[%s] = %v，期望 sha256: 前缀", k, v)
		}
	}
	if _, ok := base["unprocessed.md"]; !ok {
		t.Fatalf("base 缺 unprocessed.md：%+v", base)
	}
}

// —— ⑥ 输出确定性：连续两次逐字相同 ——

func TestContextOutputIsByteIdentical(t *testing.T) {
	dir, id := contextVault(t)
	_, first, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--json", "--source", id)
	_, second, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--json", "--source", id)
	if first != second {
		t.Fatalf("两次输出不同：\n%s\n----\n%s", first, second)
	}
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// —— ⑦ T-…-020：不可解析文件必须诚实上报（M-002 风险 R-1 的 CLI 侧关闭判据）——

// TestContextReportsUnparsableCardAsQ1 断言坏卡产生 Q1 + Q3，且只读语义与退出码不变。
func TestContextReportsUnparsableCardAsQ1(t *testing.T) {
	dir, id := contextVault(t)
	writeVaultFile(t, dir, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")

	code, env, errOut := runContextCLI(t, dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("Q 类诊断不得改变退出码，实际 %d：%s", code, errOut)
	}
	var q1, q3 int
	for _, w := range env.Warnings {
		switch w.Code {
		case "Q1":
			q1++
			if w.Path != "domains/ai-infra/knowledge/broken.md" || w.Message == "" {
				t.Errorf("Q1 必须带文件路径与人类可读原因：%+v", w)
			}
			if w.Level != LevelWarning {
				t.Errorf("Q 系列一律 warning，实际 %s", w.Level)
			}
		case "Q3":
			q3++
		}
	}
	if q1 != 1 || q3 != 1 {
		t.Fatalf("期望恰 1 条 Q1 + 1 条 Q3，实际 Q1=%d Q3=%d（%+v）", q1, q3, env.Warnings)
	}

	// 同源同事实：纯文本输出里必须出现同一条路径，不能只在 --json 里说。
	_, stdout, stderr := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--source", id)
	text := stdout + stderr
	if !strings.Contains(text, "broken.md") || !strings.Contains(text, "Q1") {
		t.Errorf("纯文本输出未透出 Q1 诊断（同源同事实）：\n%s", text)
	}
}

// —— ⑧ T-…-025：候选相似卡的人读渲染必须与 --json 同源同事实 ——

// contextCandidateVault 在 contextVault 的基础上补三张可控卡：
// 目标原文标题「注意力机制入门」，两张命中（其中一张同时命中 tags）、一张零命中。
func contextCandidateVault(t *testing.T) (string, string) {
	t.Helper()
	dir, id := contextVault(t)
	card := func(cid, title, tags string) string {
		fm := "---\nid: " + cid + "\ntitle: " + title + "\nstatus: active\n" +
			"created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n"
		if tags != "" {
			fm += "tags:\n  - " + tags + "\n"
		}
		return fm + "---\n\n## 定义\n\n正文占位。\n"
	}
	writeVaultFile(t, dir, "domains/ai-infra/knowledge/k-20260901-a.md",
		card("k-20260901-a", "注意力机制", ""))
	writeVaultFile(t, dir, "domains/ai-infra/knowledge/k-20260901-b.md",
		card("k-20260901-b", "机制入", "注意"))
	writeVaultFile(t, dir, "domains/ai-infra/knowledge/k-20260901-z.md",
		card("k-20260901-z", "磁盘调度", ""))
	return dir, id
}

// candidateIDsFromJSON 取 --json 的 data.candidates[].id（保持数组原序）。
func candidateIDsFromJSON(t *testing.T, env Envelope) []string {
	t.Helper()
	raw, err := json.Marshal(env.Data["candidates"])
	if err != nil {
		t.Fatalf("candidates 不可序列化：%v", err)
	}
	var cands []struct {
		ID      string   `json:"id"`
		Score   int      `json:"score"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(raw, &cands); err != nil {
		t.Fatalf("candidates 不可解析：%v\n%s", err, raw)
	}
	out := []string{}
	for _, c := range cands {
		if len(c.Reasons) == 0 {
			t.Fatalf("被推荐的卡必须带命中理由：%+v", c)
		}
		out = append(out, c.ID)
	}
	return out
}

// TestContextRenderSameFacts —— 文本模式的候选卡行数 == --json 的 candidates 长度，
// 逐行 ID 顺序相同；文本里出现「得分」与理由前缀；且文本不引入 JSON 里没有的候选 ID。
func TestContextRenderSameFacts(t *testing.T) {
	dir, id := contextCandidateVault(t)

	_, env, _ := runContextCLI(t, dir, "--source", id)
	wantIDs := candidateIDsFromJSON(t, env)
	if len(wantIDs) < 2 {
		t.Fatalf("用例前提被打破：应至少两张候选卡，实际 %v", wantIDs)
	}

	code, out, errOut := runCLI(t, newTestRoot(t, dir),
		"context", "--vault", dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("人读模式退出码 = %d：%s", code, errOut)
	}
	var gotIDs []string
	reasonLines := 0
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, candidateLinePrefix):
			if !strings.Contains(line, "得分 ") {
				t.Fatalf("候选卡行必须打印得分：%q", line)
			}
			rest := strings.TrimPrefix(line, candidateLinePrefix)
			gotIDs = append(gotIDs, strings.SplitN(rest, "　", 2)[0])
		case strings.HasPrefix(line, candidateReasonPrefix):
			reasonLines++
			if strings.TrimSpace(strings.TrimPrefix(line, candidateReasonPrefix)) == "" {
				t.Fatalf("理由行不得为空：%q", line)
			}
		}
	}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("文本模式的候选卡行与 --json 不同源：\n文本 %v\nJSON %v", gotIDs, wantIDs)
	}
	if reasonLines < len(wantIDs) {
		t.Fatalf("每张候选卡至少一条理由行，实际理由行 %d / 候选 %d", reasonLines, len(wantIDs))
	}
	// 文本里不得出现 JSON 中没有的候选 ID（例如零命中的 k-20260901-z）。
	if strings.Contains(out, candidateLinePrefix+"k-20260901-z") {
		t.Fatalf("零命中的卡不得出现在候选区：\n%s", out)
	}
	// 总数行仍在，且与逐张打印的条数一致（人读模式两处事实必须自洽）。
	if !strings.Contains(out, "候选相似卡 "+itoaCLI(len(wantIDs))+" 张") {
		t.Fatalf("总数行与逐张打印条数不一致：\n%s", out)
	}
}

// TestContextCandidateRenderIsStable —— 人读模式连续两次输出逐字相等（确定性）。
func TestContextCandidateRenderIsStable(t *testing.T) {
	dir, id := contextCandidateVault(t)
	_, first, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--source", id)
	_, second, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--source", id)
	if first != second {
		t.Fatalf("人读模式两次输出不同：\n%s\n----\n%s", first, second)
	}
	if !strings.Contains(first, candidateReasonPrefix) {
		t.Fatalf("人读模式必须打印命中理由：\n%s", first)
	}
}

func itoaCLI(n int) string {
	return strconv.Itoa(n)
}
