package cli

// T-…-010 的 CLI 侧验收：eg context 的白名单输出与 base。
// 用例名以 Context 开头，可用 `go test ./internal/cli/... -run Context` 单独跑。

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
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
		"default_domain_fallback", "domain", "knowledge_candidates", "notes",
		"opinion_candidates", "proposals", "source"}
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

// TestContextRenderSameFacts —— 文本模式的**知识候选**卡行数 == --json 的 candidates 长度，
// 逐行 ID 顺序相同；文本里出现「得分」与理由前缀；且文本不引入 JSON 里没有的候选 ID。
// （contextCandidateVault 只含知识卡、无观点，故此处只钉知识候选块；观点候选块另有专门用例。）
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
		case strings.HasPrefix(line, knowledgeCandidatePrefix):
			if !strings.Contains(line, "得分 ") {
				t.Fatalf("知识候选行必须打印得分：%q", line)
			}
			rest := strings.TrimPrefix(line, knowledgeCandidatePrefix)
			gotIDs = append(gotIDs, strings.SplitN(rest, "　", 2)[0])
		case strings.HasPrefix(line, candidateReasonPrefix):
			reasonLines++
			if strings.TrimSpace(strings.TrimPrefix(line, candidateReasonPrefix)) == "" {
				t.Fatalf("理由行不得为空：%q", line)
			}
		}
	}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("文本模式的知识候选行与 --json 不同源：\n文本 %v\nJSON %v", gotIDs, wantIDs)
	}
	if reasonLines < len(wantIDs) {
		t.Fatalf("每张候选卡至少一条理由行，实际理由行 %d / 候选 %d", reasonLines, len(wantIDs))
	}
	// 文本里不得出现 JSON 中没有的候选 ID（例如零命中的 k-20260901-z）。
	if strings.Contains(out, knowledgeCandidatePrefix+"k-20260901-z") {
		t.Fatalf("零命中的卡不得出现在候选区：\n%s", out)
	}
	// 总数行仍在，且与逐张打印的条数一致（人读模式两处事实必须自洽）。
	if !strings.Contains(out, "知识候选 "+itoaCLI(len(wantIDs))+" 张") {
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

// ================= T-…-006 阶段 6E：CLI 侧双候选 + candidates 兼容（D-3）+ I1 =================
//
// 判据来源：schema v2 设计 §5.3 + 决策 D-3、T-…-006 Acceptance「eg context --json 同时输出
// candidates / knowledge_candidates / opinion_candidates；candidates ≡ knowledge_candidates；
// 恰一条 I1 info 提示 candidates 已弃用」。本组用例只钉 CLI 出口事实：三候选字段并存且都是
// 数组、legacy alias 逐字等价、文本区分 Knowledge/Opinion 候选且与 JSON 同源同事实、恰一条
// I1 info（码 / 级别 / path / 消息 / 文本透出）、四种索引状态业务输出等价且对 .index 零副作用。

// writeContextOpinion 往 ai-infra 领域写一条可控标题 / 状态 / validation 的观点（schema v2）。
func writeContextOpinion(t *testing.T, dir, id, title, status, validation string) {
	t.Helper()
	body := "---\nid: " + id + "\nstatus: " + status + "\n" +
		"created_at: '2026-09-01'\nupdated_at: '2026-09-01T10:00:00+08:00'\n" +
		"title: " + title + "\nvalidation: " + validation + "\nsources: []\n" +
		"---\n\n# " + title + "\n\n## 观点\n\n正文占位。\n"
	writeVaultFile(t, dir, "domains/ai-infra/opinions/"+id+".md", body)
}

// contextDualVault 在 contextCandidateVault（含知识候选卡）基础上补三条命中观点，
// 覆盖 pending/validated/rejected 三态：证明知识与观点两路候选并存、互不串味。
func contextDualVault(t *testing.T) (string, string) {
	t.Helper()
	dir, id := contextCandidateVault(t)
	writeContextOpinion(t, dir, "o-20260901-p", "注意力机制", "active", "pending")
	writeContextOpinion(t, dir, "o-20260901-v", "注意力入门", "active", "validated")
	writeContextOpinion(t, dir, "o-20260901-r", "机制入门", "active", "rejected")
	return dir, id
}

// candListJSON 取 --json 的某个候选字段（保持数组原序）并解出 ID/得分/标题/理由。
func candListJSON(t *testing.T, env Envelope, key string) []struct {
	ID      string   `json:"id"`
	Score   int      `json:"score"`
	Title   string   `json:"title"`
	Reasons []string `json:"reasons"`
} {
	t.Helper()
	raw, err := json.Marshal(env.Data[key])
	if err != nil {
		t.Fatalf("%s 不可序列化：%v", key, err)
	}
	var out []struct {
		ID      string   `json:"id"`
		Score   int      `json:"score"`
		Title   string   `json:"title"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s 不可解析：%v\n%s", key, err, raw)
	}
	return out
}

// —— ① --json 三候选字段并存、都是数组（非 null）、candidates ≡ knowledge_candidates ——

func TestContextDualCandidatesJSON(t *testing.T) {
	dir, id := contextDualVault(t)
	code, env, errOut := runContextCLI(t, dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("eg context 退出码 = %d：%s", code, errOut)
	}
	// 三个键都在，且都是数组（空也必须是 []，不是 null / 缺键）。
	for _, k := range []string{"candidates", "knowledge_candidates", "opinion_candidates"} {
		if _, ok := env.Data[k].([]interface{}); !ok {
			t.Fatalf("data.%s 必须是数组，实得 %#v", k, env.Data[k])
		}
	}
	// legacy alias 逐字等价：candidates 的 JSON 必须与 knowledge_candidates 逐字相等。
	cj, err := json.Marshal(env.Data["candidates"])
	if err != nil {
		t.Fatal(err)
	}
	kj, err := json.Marshal(env.Data["knowledge_candidates"])
	if err != nil {
		t.Fatal(err)
	}
	if string(cj) != string(kj) {
		t.Fatalf("candidates 必须逐字等于 knowledge_candidates：\ncandidates=%s\nknowledge=%s", cj, kj)
	}
	// 知识候选保持旧语义：命中 k-a / k-b（得分降序），零命中的 k-z 不在。
	var kIDs []string
	for _, c := range candListJSON(t, env, "knowledge_candidates") {
		kIDs = append(kIDs, c.ID)
	}
	if strings.Join(kIDs, ",") != "k-20260901-a,k-20260901-b" {
		t.Fatalf("knowledge_candidates = %v，期望 [k-20260901-a k-20260901-b]", kIDs)
	}
	// 观点候选：三态都召回，按同一确定性全序（p 得分 12 在前，r/v 同分 9 按 ID 升序）。
	var oIDs []string
	for _, c := range candListJSON(t, env, "opinion_candidates") {
		if c.Score <= 0 || len(c.Reasons) == 0 {
			t.Fatalf("观点候选必须有正得分与非空理由：%+v", c)
		}
		oIDs = append(oIDs, c.ID)
	}
	if strings.Join(oIDs, ",") != "o-20260901-p,o-20260901-r,o-20260901-v" {
		t.Fatalf("opinion_candidates = %v，期望 [o-20260901-p o-20260901-r o-20260901-v]", oIDs)
	}
}

// —— ② 恰一条 I1 info：码 / 级别 / path / 消息 + 文本透出，且不因 Q1/默认领域回退而重复 ——

func TestContextCandidatesDeprecationI1(t *testing.T) {
	dir, id := contextDualVault(t)
	// 补一张坏卡制造 Q1 + Q3，反证 I1 与 Q 系列独立、恰一条、不被带偏成 warning。
	writeVaultFile(t, dir, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")

	code, env, errOut := runContextCLI(t, dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("I1/Q 类诊断不得改变退出码，实际 %d：%s", code, errOut)
	}
	var i1 []Diagnostic
	for _, w := range env.Warnings {
		if w.Code == "I1" {
			i1 = append(i1, w)
		}
	}
	if len(i1) != 1 {
		t.Fatalf("必须恰一条 I1，实得 %d 条：%+v", len(i1), env.Warnings)
	}
	d := i1[0]
	if d.Level != LevelInfo {
		t.Fatalf("I1 必须是 info 级，实得 %s", d.Level)
	}
	if d.Path != "candidates" {
		t.Fatalf("I1 的 path 应为 candidates，实得 %q", d.Path)
	}
	if !strings.Contains(d.Message, "candidates") || !strings.Contains(d.Message, "已弃用") ||
		!strings.Contains(d.Message, "knowledge_candidates") {
		t.Fatalf("I1 消息必须明确「candidates 已弃用，改读 knowledge_candidates」：%q", d.Message)
	}
	// 同源同事实：纯文本输出里也必须透出这条 I1（码 + 消息关键词）。
	_, text, stderr := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--source", id)
	whole := text + stderr
	if !strings.Contains(whole, "I1") || !strings.Contains(whole, "已弃用") {
		t.Fatalf("纯文本未透出 I1（同源同事实）：\n%s", whole)
	}
}

// —— ②′ CLI 反证：I1 与全部 query 诊断都**来自 ctx.Diagnostics**，CLI 只原样透出、保留 level ——
//
// 判据来源：T-…-006 Scope「弃用 I1 由 query context 的 diagnostics 产出」+「CLI 遍历
// ctx.Diagnostics 时必须保留 d.Level，不能把所有诊断硬编码为 warning」。做法：以
// query.Build 的 ctx.Diagnostics 为**唯一事实源**（oracle），逐字比对 CLI --json warnings[]
// 里所有**带码**诊断（fallback / notes 是无码 info，天然被过滤）。若 CLI 另造一份 I1，
// oracle 只有一条、CLI 就会多一条 → 序列不等 → 当场红；若 CLI 把 info 硬编码成 warning，
// level 位不等 → 当场红。因此这条用例同时锁死「不另造」与「保留 level」两件事。

// diagSig 把一条诊断摊成 `code|level|path|message`，用于逐字序列比对。
func diagSig(code, level, path, message string) string {
	return code + "|" + level + "|" + path + "|" + message
}

// codedWarningSigs 取 CLI --json 信封里**带码**诊断（即 query 域诊断）的签名序列（保序）。
func codedWarningSigs(env Envelope) []string {
	var out []string
	for _, w := range env.Warnings {
		if w.Code == "" {
			continue // fallback（默认领域回退）/ notes 都是无码 info，不属 query 诊断
		}
		out = append(out, diagSig(w.Code, w.Level, w.Path, w.Message))
	}
	return out
}

// queryDiagSigs 直接调 query.Build 取 ctx.Diagnostics 的签名序列（CLI 的唯一事实源）。
func queryDiagSigs(t *testing.T, dir, domain, source string) []string {
	t.Helper()
	ctx, err := query.Build(query.Request{Root: dir, Domain: domain, Source: source}, store.ContentHash)
	if err != nil {
		t.Fatalf("query.Build 失败：%v", err)
	}
	var out []string
	for _, d := range ctx.Diagnostics {
		out = append(out, diagSig(d.Code, d.Level, d.Path, d.Message))
	}
	return out
}

func TestContextDiagnosticsAreQueryPassThrough(t *testing.T) {
	// 两个场景：干净 vault（只应有 I1 info）与坏卡 vault（I1 info + Q1/Q3 warning）。
	// 两者都必须与 query.Build 的 ctx.Diagnostics 逐字相等，且各自恰一条 I1（info）。
	scenarios := []struct {
		name  string
		setup func(t *testing.T) (string, string)
	}{
		{"clean", contextDualVault},
		{"with-Q", func(t *testing.T) (string, string) {
			dir, id := contextDualVault(t)
			writeVaultFile(t, dir, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
			return dir, id
		}},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			dir, id := s.setup(t)

			code, env, errOut := runContextCLI(t, dir, "--source", id)
			if code != ExitOK {
				t.Fatalf("退出码 = %d：%s", code, errOut)
			}
			// CLI 带码诊断序列必须与 query.Build 的 ctx.Diagnostics 逐字相等
			//（同码、同 level、同 path、同 message、同顺序）。
			want := queryDiagSigs(t, dir, "ai-infra", id)
			got := codedWarningSigs(env)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("CLI 诊断未原样透出 query.Diagnostics（可能另造 I1 或改写 level）：\nCLI  %v\nquery %v", got, want)
			}
			// 反向锁死 level 保留：oracle 里的 I1 必须是 info，且恰一条——若 CLI 硬编码
			// 成 warning，上面的逐字比对已红；这里再单独钉「恰一条 info I1」。
			var i1Info int
			for _, sig := range want {
				if strings.HasPrefix(sig, "I1|"+LevelInfo+"|") {
					i1Info++
				}
			}
			if i1Info != 1 {
				t.Fatalf("%s：query 事实源里应恰一条 info 级 I1，实得 %d（%v）", s.name, i1Info, want)
			}
		})
	}
}

// —— ③ 文本区分 Knowledge / Opinion 候选，与 JSON 同序同事实，不把 candidates 再渲染一遍 ——

func TestContextTextDistinguishesKnowledgeOpinion(t *testing.T) {
	dir, id := contextDualVault(t)
	_, env, _ := runContextCLI(t, dir, "--source", id)
	var wantK, wantO []string
	for _, c := range candListJSON(t, env, "knowledge_candidates") {
		wantK = append(wantK, c.ID)
	}
	for _, c := range candListJSON(t, env, "opinion_candidates") {
		wantO = append(wantO, c.ID)
	}
	if len(wantK) == 0 || len(wantO) == 0 {
		t.Fatalf("用例前提被打破：知识候选 %v / 观点候选 %v", wantK, wantO)
	}

	code, out, errOut := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("人读模式退出码 = %d：%s", code, errOut)
	}
	var gotK, gotO []string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, knowledgeCandidatePrefix):
			if !strings.Contains(line, "得分 ") {
				t.Fatalf("知识候选行必须打印得分：%q", line)
			}
			rest := strings.TrimPrefix(line, knowledgeCandidatePrefix)
			gotK = append(gotK, strings.SplitN(rest, "　", 2)[0])
		case strings.HasPrefix(line, opinionCandidatePrefix):
			if !strings.Contains(line, "得分 ") {
				t.Fatalf("观点候选行必须打印得分：%q", line)
			}
			rest := strings.TrimPrefix(line, opinionCandidatePrefix)
			gotO = append(gotO, strings.SplitN(rest, "　", 2)[0])
		}
	}
	if strings.Join(gotK, ",") != strings.Join(wantK, ",") {
		t.Fatalf("文本知识候选与 JSON 不同序：文本 %v / JSON %v", gotK, wantK)
	}
	if strings.Join(gotO, ",") != strings.Join(wantO, ",") {
		t.Fatalf("文本观点候选与 JSON 不同序：文本 %v / JSON %v", gotO, wantO)
	}
	// 兼容 candidates 不得再单独渲染一遍：候选 ID 行总数恰为知识 + 观点，不翻倍。
	if got := len(gotK) + len(gotO); got != len(wantK)+len(wantO) {
		t.Fatalf("候选行总数 = %d，期望 %d（candidates 不得再渲染一遍）", got, len(wantK)+len(wantO))
	}
	// 总数行事实自洽：知识候选与观点候选两处计数都要出现。
	if !strings.Contains(out, "知识候选 "+itoaCLI(len(wantK))+" 张") {
		t.Fatalf("缺知识候选总数行：\n%s", out)
	}
	if !strings.Contains(out, "观点候选 "+itoaCLI(len(wantO))+" 条") {
		t.Fatalf("缺观点候选总数行：\n%s", out)
	}
}

// —— ④ 四种索引状态业务输出等价 + 对 .index 零副作用（context 是权威 scan，不接后端）——
//
// 现有架构判定：internal/query/context.go 的 Build 直接走 VaultScan（权威扫描 Markdown），
// **不接 SelectBackend**——且 context 含 notes，而索引不表达 notes。故本用例如实钉：
// 无论索引 healthy/missing/stale/corrupt，context 的 JSON 与文本输出逐字等价，
// 且 context 从不读写 `.index/`（对索引零副作用）。

// 索引布局字面量（冻结合同：`.index/` 目录与 `.index/eg.db` 库文件）。命令层测试文件
// 不得 import internal/index（arch 门禁 §1.1：只有 index* / bench* 前缀文件可消费索引包），
// 故这里按冻结布局直写路径——本用例只验证 context 对索引「零读写」，不依赖索引包 API。
const (
	indexDirName    = ".index"
	indexDBFileName = "eg.db"
)

// walDataFrameCount 按 SQLite WAL 文件格式推算 `.index/eg.db-wal` 里已写入的数据帧条数：
// 头 32 字节是 WAL header（第 8–11 字节大端为 page_size），其后每帧 = 24 字节帧头 + page_size。
// 文件缺失或仅有 header（≤32 字节）即 0 帧。build / rebuild / sync 收尾都会
// `wal_checkpoint(TRUNCATE)` 把 WAL 截空，故健康库常态是 0 帧；context 从不打开索引，
// 因此任何数据帧的出现或增长都必然意味着有人碰了索引——这条计数就是抓手。
func walDataFrameCount(t *testing.T, dir string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, indexDirName, indexDBFileName+"-wal"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("读 WAL 失败：%v", err)
	}
	if len(raw) <= 32 {
		return 0 // 只有 header（或空）：零数据帧
	}
	pageSize := int(binary.BigEndian.Uint32(raw[8:12]))
	if pageSize <= 0 {
		t.Fatalf("WAL header 的 page_size 非法：%d", pageSize)
	}
	return (len(raw) - 32) / (24 + pageSize)
}

// contextOutputs 跑一次 context，返回 JSON 与文本两份输出（供跨索引状态逐字比对）。
func contextOutputs(t *testing.T, dir, id string) (string, string) {
	t.Helper()
	_, jsonOut, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--json", "--source", id)
	_, text, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--source", id)
	return jsonOut, text
}

// TestContextIgnoresIndexStateAndLeavesItUntouched —— context 走权威 Markdown scan、
// **不接索引后端**，故四种索引状态下业务输出逐字等价；且 context 对索引 / vault / git /
// 事务日志 / WAL 一律零副作用。
//
// 本轮强化两处：
//  1. **stale 是真陈旧**：不再原字节重写（按 content_hash 终判可能仍 fresh），而是向一张
//     **非候选**卡（k-20260901-z，零命中、不进 candidates/base，其正文不进 context 输出）
//     追加真实字节；并用现有 `eg index status` helper **逐态**证明四个标签为真
//     （missing / healthy-fresh / stale(--strict) / corrupt）。
//  2. **零副作用用强 helper 取证**：非 .git 全树内容 hash+mtime（vaultSnapshot）、权威
//     Markdown 字节+mtime（authorityMDStats）、git status 与 rev-list count、事务集合
//     （txnIDsOn）、`.index/` 全子树字节（indexTreeSnapshot）与 WAL 数据帧计数，逐态
//     在 context 前后逐字比对，任何出现 / 增长 / 改写都会被抓住——不再复制第二套弱 hash。
func TestContextIgnoresIndexStateAndLeavesItUntouched(t *testing.T) {
	// 基线：无索引状态下的业务输出（后续四态都必须逐字复现它）。
	dir, id := contextDualVault(t)
	baseJSON, baseText := contextOutputs(t, dir, id)

	// nonCandidateCard 是零命中卡：它在 cards[] 里只露 id/path/title/tags（正文不出），
	// 既不进 candidates 也不进 base，因此改它的**正文字节**能让索引真陈旧、却不动 context 输出。
	const nonCandidateCard = "domains/ai-infra/knowledge/k-20260901-z.md"

	// proveLabel 用现有 `eg index status` helper 逐态证明四个索引标签**为真**。
	proveLabel := func(t *testing.T, d, state string) {
		switch state {
		case "missing":
			_, out, _ := runIndexCLI(t, d, "status")
			if h := idxHealth(t, out); h != "missing" {
				t.Fatalf("missing 态 health = %q，期望 missing", h)
			}
		case "healthy":
			_, out, _ := runIndexCLI(t, d, "status")
			if h := idxHealth(t, out); h != "healthy" {
				t.Fatalf("healthy 态 health = %q，期望 healthy", h)
			}
			if f := idxFreshness(t, out); f != "fresh" {
				t.Fatalf("healthy 态 freshness = %q，期望 fresh", f)
			}
		case "stale":
			// 用 --strict 走全量 content_hash 重算：这才是「真陈旧」的终判
			//（默认快路径只看 size/mtime，可能被等长改写骗过）。
			_, out, _ := runIndexCLI(t, d, "status", "--strict")
			if h := idxHealth(t, out); h != "healthy" {
				t.Fatalf("stale 态 health = %q，期望 healthy（陈旧不是损坏）", h)
			}
			if f := idxFreshness(t, out); f != "stale" {
				t.Fatalf("stale 态 --strict freshness = %q，期望 stale（content_hash 终判）", f)
			}
		case "corrupt":
			_, out, _ := runIndexCLI(t, d, "status")
			if h := idxHealth(t, out); h != "corrupt" {
				t.Fatalf("corrupt 态 health = %q，期望 corrupt", h)
			}
		}
	}

	states := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{"missing", func(t *testing.T, dir string) {}},
		{"healthy", func(t *testing.T, dir string) {
			if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
				t.Fatalf("建索引失败：%s", errOut)
			}
		}},
		{"stale", func(t *testing.T, dir string) {
			if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
				t.Fatalf("建索引失败：%s", errOut)
			}
			// 真陈旧：向非候选卡追加真实字节（content_hash 必变，--strict 一定判 stale）。
			p := filepath.Join(dir, filepath.FromSlash(nonCandidateCard))
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, append(raw, []byte("\n补一句不影响 context 的正文。\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"corrupt", func(t *testing.T, dir string) {
			if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
				t.Fatalf("建索引失败：%s", errOut)
			}
			idxCorruptDB(t, dir) // 真实非法字节，非打桩
		}},
	}
	for _, s := range states {
		t.Run(s.name, func(t *testing.T) {
			d, srcID := contextDualVault(t)
			s.setup(t, d)

			// 先逐态证明索引标签为真（status 只读；其读侧副产物在 before 快照前落定）。
			proveLabel(t, d, s.name)

			// 强快照基线（context 之前）：全树字节 / 权威 Markdown / git / 事务 / 索引子树 / WAL。
			vaultBefore := vaultSnapshot(t, d)
			mdBefore := authorityMDStats(t, d)
			gitStatusBefore := strings.TrimSpace(gitOut(t, d, "status", "--porcelain"))
			commitsBefore := strings.TrimSpace(gitOut(t, d, "rev-list", "--count", "HEAD"))
			txnBefore := txnIDsOn(t, d)
			idxBefore := indexTreeSnapshot(t, d)
			walBefore := walDataFrameCount(t, d)

			// 业务输出：四态都必须逐字复现无索引基线。
			gotJSON, gotText := contextOutputs(t, d, srcID)
			if gotJSON != baseJSON {
				t.Fatalf("索引状态 %s 下 JSON 输出与基线不同：\n%s\n----\n%s", s.name, gotJSON, baseJSON)
			}
			if gotText != baseText {
				t.Fatalf("索引状态 %s 下文本输出与基线不同", s.name)
			}

			// —— 零副作用（context 之后逐字复核）——
			// ① 非 .git 全树内容 hash + mtime（含 .index 子树、权威 Markdown、收件区……）。
			if after := vaultSnapshot(t, d); after != vaultBefore {
				t.Fatalf("索引状态 %s：context 改动了全树（内容 hash / mtime）", s.name)
			}
			// ② 权威 Markdown 字节 + mtime 逐条不变（新增 / 改写 / 删除 / 触碰均报出）。
			assertAuthorityMDUnchanged(t, d, mdBefore, "索引状态 "+s.name+"：context")
			// ③ git：工作区状态与提交数都不动（零 commit、零暂存）。
			if after := strings.TrimSpace(gitOut(t, d, "status", "--porcelain")); after != gitStatusBefore {
				t.Fatalf("索引状态 %s：context 改动了 git 工作区：\n前=%q\n后=%q", s.name, gitStatusBefore, after)
			}
			if after := strings.TrimSpace(gitOut(t, d, "rev-list", "--count", "HEAD")); after != commitsBefore {
				t.Fatalf("索引状态 %s：context 产生了 commit（%s → %s）", s.name, commitsBefore, after)
			}
			// ④ 事务集合前后相等：context 绝不开事务。
			if after := txnIDsOn(t, d); strings.Join(after, ",") != strings.Join(txnBefore, ",") {
				t.Fatalf("索引状态 %s：context 动了事务日志集合：\n前=%v\n后=%v", s.name, txnBefore, after)
			}
			// ⑤ `.index/` 全子树字节不变（run.lock 除外）：catch 索引库 / WAL / shm 的任何改写。
			if diffs := diffSnapshots(idxBefore, indexTreeSnapshot(t, d)); len(diffs) != 0 {
				t.Fatalf("索引状态 %s：context 动了 .index 子树：%v", s.name, diffs)
			}
			// ⑥ WAL 无数据帧、且不因 context 出现 / 增长（context 从不打开索引）。
			if walAfter := walDataFrameCount(t, d); walAfter != 0 || walAfter != walBefore {
				t.Fatalf("索引状态 %s：WAL 数据帧异常（前=%d 后=%d，期望恒 0）", s.name, walBefore, walAfter)
			}
		})
	}
}
