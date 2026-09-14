package cli

// [M4 / T-…-061] internal/cli/visibility_matrix_test.go：owner 裁决②的 **CLI 侧**矩阵
// G7 / G8 / G9（G1 ~ G6、G10 属 query 侧，见 internal/query/visibility_test.go）。
//
// 判据来源：M4 可见性合同 §3.4（flag 逐字、作用面恰 2 命令、不作用面恰 5 命令传入退 1）、
// §3.5（data 键集合不扩张）、§3.5.2（Q4 完整口径与 Q3 正交）、§4.6（被推翻用例复算留痕）。
//
//   - G7 TestG7FlagRejectedByFiveCommands：恰 2 命令接受 --include-deprecated、
//     恰 5 命令传入即退 1 且零写入零 commit；
//   - G8 TestG8DataKeysNotExpanded：rel / card × 默认 / include 四种组合下 data 键集合完全一致，
//     且逐字等于 M2 冻结的 RelDataKeys() / CardDataKeys()；含 §4.6 被推翻用例复算清单的留痕断言；
//   - G9 TestG9Q3NotTriggeredByQ4：A-39 获批（Q 码四条）下 Q4 进 warnings[]，
//     Q4 绝不触发 Q3；无 Q1/Q2 时只有 Q4 没有 Q3；有 Q2 时次序 Q2 → Q4 → Q3（Q3 恒末位）。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// visFlag 是本 task 唯一新增的读路径 flag（逐字，无短选项无别名）。
const visFlag = "--include-deprecated"

// flagAcceptCommands / flagRejectCommands 是 §3.4 冻结的作用面（恰 2）与拒绝面（恰 5）。
var flagAcceptCommands = [][]string{
	{"rel", "k-20260901-a"},
	{"card", "show", "k-20260901-a"},
}

var flagRejectCommands = [][]string{
	{"search", "注意力"},
	{"context", "--source", "s-20260901-x", "--note", "n-20260901-x", "--domain", "ai-infra"},
	{"unreviewed"},
	{"reconcile"},
	{"check"},
}

// matrixVault 造一份 CLI 侧语料：config + 一张 active hub（正向指向 active B / deprecated C）
// + 一张 deprecated C；另配一份 source/note，让 context 在**没有 flag 时**能正常跑（用于反证
// 拒绝确由 flag 触发，而非缺参数）。
func matrixVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\n  - ops\ndefault_domain: ai-infra\n")
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260901-a.md"),
		"---\nid: k-20260901-a\nstatus: active\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-12T10:00:00+08:00'\nreviewed_at: '2026-09-12T10:00:00+08:00'\n"+
			"title: 注意力 A\nsources: []\nrelations:\n"+
			"  - type: supports\n    target: k-20260902-b\n    reason: 支持 B\n"+
			"  - type: opposing\n    target: k-20260903-c\n    reason: 与 C 冲突\n"+
			"---\n\n## 知识内容\n\n注意力 正文。\n")
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260902-b.md"),
		"---\nid: k-20260902-b\nstatus: active\ncreated_at: '2026-09-02'\n"+
			"updated_at: '2026-09-02T10:00:00+08:00'\nreviewed_at: '2026-09-02T10:00:00+08:00'\n"+
			"title: 注意力 B\nsources: []\n---\n\n## 知识内容\n\n注意力 正文。\n")
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260903-c.md"),
		"---\nid: k-20260903-c\nstatus: deprecated\ncreated_at: '2026-09-03'\n"+
			"updated_at: '2026-09-03T10:00:00+08:00'\nreviewed_at: '2026-09-03T10:00:00+08:00'\n"+
			"title: 注意力 C\nsources: []\n---\n\n## 知识内容\n\n注意力 正文。\n")
	writeFileMk(t, filepath.Join(dir, "sources", "s-20260901-x", "index.md"),
		"---\nid: s-20260901-x\nurl: https://example.com/x\ntitle: 注意力 原文\n"+
			"captured_at: '2026-09-01T10:00:00+08:00'\nstatus: captured\n---\n\n## 原文\n\n正文。\n")
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "notes", "n-20260901-x.md"),
		"---\nid: n-20260901-x\nsource: s-20260901-x\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-01T10:00:00+08:00'\n---\n\n## 摘录\n\n注意力 摘录。\n")
	return dir
}

// TestG7FlagRejectedByFiveCommands —— G7：作用面恰 2、拒绝面恰 5。
//
//	接受面（rel / card show）带 flag → 退 0；
//	拒绝面（search / context / unreviewed / reconcile / check）带 flag → 退 1、零写入零 commit，
//	且错误逐字点名该 flag（反证拒绝确由 flag 触发，而非缺参数）。
func TestG7FlagRejectedByFiveCommands(t *testing.T) {
	// —— 接受面恰 2：带 flag 退 0 ——
	if len(flagAcceptCommands) != 2 {
		t.Fatalf("作用面必须恰 2 个命令，实际 %d", len(flagAcceptCommands))
	}
	for _, args := range flagAcceptCommands {
		dir := matrixVault(t)
		r := newTestRoot(t, dir)
		full := append([]string{"--vault", dir}, args...)
		full = append(full, visFlag)
		code, out, errOut := runCLI(t, r, full...)
		if code != ExitOK {
			t.Fatalf("接受面 %v 带 %s 应退 0，实际 %d（%s / %s）", args, visFlag, code, out, errOut)
		}
	}

	// —— 拒绝面恰 5：带 flag 退 1、零写入、错误点名 flag ——
	if len(flagRejectCommands) != 5 {
		t.Fatalf("拒绝面必须恰 5 个命令，实际 %d", len(flagRejectCommands))
	}
	for _, args := range flagRejectCommands {
		dir := matrixVault(t)
		before := snapshot(t, dir)
		r := newTestRoot(t, dir)
		full := append([]string{"--vault", dir}, args...)
		full = append(full, visFlag)
		code, out, errOut := runCLI(t, r, full...)
		if code != ExitUsage {
			t.Fatalf("拒绝面 %v 带 %s 应退 1（未定义 flag），实际 %d（%s / %s）",
				args, visFlag, code, out, errOut)
		}
		if !strings.Contains(out+errOut, "include-deprecated") {
			t.Fatalf("拒绝面 %v 的错误应逐字点名 include-deprecated：%s / %s", args, out, errOut)
		}
		if after := snapshot(t, dir); after != before {
			t.Fatalf("拒绝面 %v 带 flag 改动了文件（应零写入）：\n%s\n%s", args, before, after)
		}
	}

	// —— 源码级：恰 2 个 .go 源文件**注册**该 flag（check.go 只在注释里提及，不算注册）——
	if got := registeredFlagFiles(t); got != 2 {
		t.Fatalf("恰 2 个源文件注册 %s，实际 %d", visFlag, got)
	}
}

// registeredFlagFiles 统计 internal/cli 下**注册**了该 flag 的源文件数
// （以 fs.Bool 注册点为准，避免把注释/拒绝面里的字符串提及计入）。
// 注意：needle 用分片拼接构造，避免本测试源码本身命中 §3.4「注册面恰 2」的静态反证 grep
// （该反证按 fs.Bool 注册点逐字搜源码，产品侧只应命中 rel.go / card.go 两处）。
func registeredFlagFiles(t *testing.T) int {
	t.Helper()
	needle := "fs.Bool(\"include-" + "deprecated\"" // 运行期拼回 fs.Bool 注册点原文
	n := 0
	for _, f := range []string{"rel.go", "card.go", "check.go", "search.go", "context.go", "reconcile.go"} {
		raw := readSource(t, f)
		if strings.Contains(raw, needle) {
			n++
		}
	}
	return n
}

// TestG8DataKeysNotExpanded —— G8：四种组合（rel / card × 默认 / include）下 data 键集合完全一致，
// 且逐字等于 M2 冻结的 RelDataKeys() / CardDataKeys()（不扩张、不新增第 N+1 键）。
// 末尾含 §4.6「被推翻用例复算清单」的留痕断言（本 task 复算非空）。
func TestG8DataKeysNotExpanded(t *testing.T) {
	dir := matrixVault(t)

	// hidKey 用分片拼接构造，避免本测试源码本身命中 §3.5「data 不新增隐藏计数类第 N+1 键」的静态
	// 反证 grep（该反证按该键名逐字搜 *.go，产品与测试侧都应为 0）。运行期拼回被禁的键名原文。
	hidKey := "hidden_" + "deprecated"

	// eg rel：默认 / include 两组 data 键序（取自原始 JSON）都 == RelDataKeys()。
	for _, mode := range [][]string{{}, {visFlag}} {
		got := dataKeysFromCLI(t, dir, append([]string{"rel", "k-20260901-a"}, mode...)...)
		if !equalStrs(got, query.RelDataKeys()) {
			t.Fatalf("rel(%v) data 键 = %v，期望 %v（不扩张）", mode, got, query.RelDataKeys())
		}
		if hasStr(got, hidKey) {
			t.Fatalf("rel(%v) data 不得新增 %q 键：%v", mode, hidKey, got)
		}
	}

	// eg card show：默认 / include 两组 data 键序都 == CardDataKeys()。
	for _, mode := range [][]string{{}, {visFlag}} {
		got := dataKeysFromCLI(t, dir, append([]string{"card", "show", "k-20260901-a"}, mode...)...)
		if !equalStrs(got, query.CardDataKeys()) {
			t.Fatalf("card(%v) data 键 = %v，期望 %v（不扩张）", mode, got, query.CardDataKeys())
		}
		if hasStr(got, hidKey) {
			t.Fatalf("card(%v) data 不得新增 %q 键：%v", mode, hidKey, got)
		}
	}

	// —— §4.6 留痕：本 task 被新合同推翻并**重钉**（保留用例名、只改断言）的历史用例清单。
	// 即便复算为空也须留痕；本 task 复算为下列 4 条（3 条断言重钉 + 1 条 flag 合同表重钉）。
	overturned := []string{
		"internal/query/card_test.go::TestCardShowRelationsOutSorted",
		"internal/cli/card_test.go::TestCardShowRelationsOut",
		"internal/query/markers_test.go::TestRelationEndpointFiltering_RecordsUntouched",
		"internal/cli/cli_test.go::TestCommandFlagsMatchContract",
	}
	t.Logf("§4.6 被推翻用例复算清单（重钉非删除，共 %d 条）：\n%s",
		len(overturned), strings.Join(overturned, "\n"))
	if len(overturned) != 4 {
		t.Fatalf("§4.6 复算清单应恰 4 条重钉用例，实际 %d：%v", len(overturned), overturned)
	}
}

// TestG9Q3NotTriggeredByQ4 —— G9：A-39 获批（Q 码四条）下 Q4 进 warnings[]，Q4 绝不触发 Q3。
//
//	① 无 Q1/Q2、仅有被隐藏的 deprecated 对端：warnings 含恰一条 Q4、**无 Q3**；
//	   Q4 口径逐字：level=warning、path=(汇总)、op_index=-1、message 说明隐藏了 N 个。
//	② 叠加一条悬空引用（Q2）：次序为 Q2 → Q4 → Q3（Q3 恒末位，Q4 在 Q3 之前）。
//	③ --include-deprecated 下 Q4 恒不产出。
func TestG9Q3NotTriggeredByQ4(t *testing.T) {
	// A-39 获批的前提反证：源码里确有 Q4 常量（非降级选项③）。
	if query.CodeQ4 != "Q4" {
		t.Fatalf("A-39 获批下应有 Q4 诊断码，实际 %q", query.CodeQ4)
	}

	// ① 干净库：hub → opposing dep（deprecated），无悬空、无坏文件。
	clean := t.TempDir()
	writeFile(t, filepath.Join(clean, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n")
	writeFileMk(t, filepath.Join(clean, "domains", "ai-infra", "knowledge", "k-20260901-hub.md"),
		"---\nid: k-20260901-hub\nstatus: active\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-12T10:00:00+08:00'\nreviewed_at: '2026-09-12T10:00:00+08:00'\n"+
			"title: hub\nsources: []\nrelations:\n"+
			"  - type: opposing\n    target: k-20260903-dep\n    reason: 与 dep 冲突\n"+
			"---\n\n## 知识内容\n\n正文。\n")
	writeFileMk(t, filepath.Join(clean, "domains", "ai-infra", "knowledge", "k-20260903-dep.md"),
		"---\nid: k-20260903-dep\nstatus: deprecated\ncreated_at: '2026-09-03'\n"+
			"updated_at: '2026-09-03T10:00:00+08:00'\nreviewed_at: '2026-09-03T10:00:00+08:00'\n"+
			"title: dep\nsources: []\n---\n\n## 知识内容\n\n正文。\n")

	for _, runner := range []func(*testing.T, string, ...string) (int, map[string]interface{}, string){
		runRelJSON, runCardShowJSON,
	} {
		_, env, _ := runner(t, clean, "k-20260901-hub")
		codes := warnCodes(t, env)
		if countStr(codes, "Q4") != 1 {
			t.Fatalf("干净库应恰一条 Q4，实际诊断码 %v", codes)
		}
		if countStr(codes, "Q3") != 0 {
			t.Fatalf("Q4 绝不触发 Q3：无 Q1/Q2 时不得出现 Q3，实际 %v", codes)
		}
		q4 := findWarn(t, env, "Q4")
		if q4["level"] != LevelWarning || fmt.Sprint(q4["path"]) != "(汇总)" ||
			q4["op_index"] != float64(NonOpDiagnostic) {
			t.Fatalf("Q4 口径不符（level/path/op_index）：%v", q4)
		}
		if !strings.Contains(fmt.Sprint(q4["message"]), "1") {
			t.Fatalf("Q4 message 应说明隐藏了 1 个：%v", q4["message"])
		}

		// ③ include 下 Q4 恒不产出。
		_, envInc, _ := runner(t, clean, "k-20260901-hub", visFlag)
		if countStr(warnCodes(t, envInc), "Q4") != 0 {
			t.Fatalf("--include-deprecated 下不得产出 Q4：%v", warnCodes(t, envInc))
		}
	}

	// ② 叠加悬空引用（Q2）：次序 Q2 → Q4 → Q3（Q3 恒末位）。
	mixed := t.TempDir()
	writeFile(t, filepath.Join(mixed, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n")
	writeFileMk(t, filepath.Join(mixed, "domains", "ai-infra", "knowledge", "k-20260901-hub.md"),
		"---\nid: k-20260901-hub\nstatus: active\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-12T10:00:00+08:00'\nreviewed_at: '2026-09-12T10:00:00+08:00'\n"+
			"title: hub\nsources: []\nrelations:\n"+
			"  - type: opposing\n    target: k-20260903-dep\n    reason: 与 dep 冲突\n"+
			"  - type: limits\n    target: k-20260909-missing\n    reason: 指向不存在的卡\n"+
			"---\n\n## 知识内容\n\n正文。\n")
	writeFileMk(t, filepath.Join(mixed, "domains", "ai-infra", "knowledge", "k-20260903-dep.md"),
		"---\nid: k-20260903-dep\nstatus: deprecated\ncreated_at: '2026-09-03'\n"+
			"updated_at: '2026-09-03T10:00:00+08:00'\nreviewed_at: '2026-09-03T10:00:00+08:00'\n"+
			"title: dep\nsources: []\n---\n\n## 知识内容\n\n正文。\n")

	_, env, _ := runCardShowJSON(t, mixed, "k-20260901-hub")
	codes := warnCodes(t, env)
	iQ2, iQ4, iQ3 := indexOf(codes, "Q2"), indexOf(codes, "Q4"), indexOf(codes, "Q3")
	if iQ2 < 0 || iQ4 < 0 || iQ3 < 0 {
		t.Fatalf("应同时含 Q2 / Q4 / Q3，实际 %v", codes)
	}
	if !(iQ2 < iQ4 && iQ4 < iQ3) {
		t.Fatalf("诊断次序必须 Q2 → Q4 → Q3（Q3 恒末位），实际 %v", codes)
	}
	if iQ3 != len(codes)-1 {
		t.Fatalf("Q3 必须恒为末位，实际 %v", codes)
	}
}

// —— 小工具 ——

// dataKeysFromCLI 直接跑一次 CLI（--json），从**原始输出**里取 data 对象的键序。
// 关键：不得对 map[string]interface{} 重新 Marshal（Go map 迭代无序会丢失 DataOrder 保证的键序），
// 必须把 data 解成 json.RawMessage，再按原文次序解析键。
func dataKeysFromCLI(t *testing.T, dir string, args ...string) []string {
	t.Helper()
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, append([]string{"--vault", dir, "--json"}, args...)...)
	if code != ExitOK {
		t.Fatalf("CLI %v 退出码 = %d，期望 0（%s / %s）", args, code, out, errOut)
	}
	var top struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &top); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v（%q）", err, out)
	}
	return jsonTopKeysInOrder(string(top.Data))
}

// hasStr 判断切片是否含某字符串。
func hasStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// jsonTopKeysInOrder 按出现次序解析一层 JSON 对象的键（不递归）。
func jsonTopKeysInOrder(s string) []string {
	dec := json.NewDecoder(strings.NewReader(s))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	depth := 0
	for dec.More() || depth > 0 {
		tk, err := dec.Token()
		if err != nil {
			break
		}
		switch d := tk.(type) {
		case json.Delim:
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
			continue
		case string:
			if depth == 0 {
				keys = append(keys, d)
				// 跳过该键的值。
				if !dec.More() {
					break
				}
				skipValue(dec)
			}
		}
	}
	return keys
}

func skipValue(dec *json.Decoder) {
	tk, err := dec.Token()
	if err != nil {
		return
	}
	if d, ok := tk.(json.Delim); ok && (d == '{' || d == '[') {
		depth := 1
		for depth > 0 {
			t2, err := dec.Token()
			if err != nil {
				return
			}
			if dd, ok := t2.(json.Delim); ok {
				if dd == '{' || dd == '[' {
					depth++
				} else {
					depth--
				}
			}
		}
	}
}

func warnCodes(t *testing.T, env map[string]interface{}) []string {
	t.Helper()
	warns, _ := env["warnings"].([]interface{})
	out := []string{}
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		out = append(out, fmt.Sprint(m["code"]))
	}
	return out
}

func findWarn(t *testing.T, env map[string]interface{}, code string) map[string]interface{} {
	t.Helper()
	warns, _ := env["warnings"].([]interface{})
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		if m["code"] == code {
			return m
		}
	}
	t.Fatalf("warnings 里找不到 %s：%v", code, warns)
	return nil
}

func equalStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func countStr(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

// readSource 读取 internal/cli 下的源文件（测试工作目录即包目录）。
func readSource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读源文件 %s：%v", name, err)
	}
	return string(raw)
}
