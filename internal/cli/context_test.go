package cli

// T-…-010 的 CLI 侧验收：eg context 的白名单输出与 base。
// 用例名以 Context 开头，可用 `go test ./internal/cli/... -run Context` 单独跑。

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		"default_domain_fallback", "domain", "notes", "source"}
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

// —— ② 输出不含 S2/S3 才有的字段与标记 ——

func TestContextOmitsStageTwoFields(t *testing.T) {
	dir, id := contextVault(t)
	code, _, _ := runContextCLI(t, dir, "--source", id)
	if code != ExitOK {
		t.Fatalf("退出码 = %d", code)
	}
	_, out, _ := runCLI(t, newTestRoot(t, dir), "context", "--vault", dir, "--json", "--source", id)
	for _, banned := range []string{
		"proposals", "proposal", "reviews", "reviewed",
		"[已删除]", "[未过目]", "[材料支持不足]",
	} {
		if strings.Contains(out, banned) {
			t.Fatalf("输出不得含 %q（S2/S3 才有）：\n%s", banned, out)
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
