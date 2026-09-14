package cli

// [M3] internal/cli/markers_test.go：两个新标记与双标记顺序在**命令输出层**的机器判据
// （提案与状态合同 §6.2；T-evergreen.s1_main_flow-158614-043）。
//
//   - TestMarkers_DeletedAndUnreviewed：`[已删除]` / `[未过目]` 两个标记在显式查看里出现，
//     JSON 的 deleted / unreviewed 两个布尔与文本标记同源同事实；默认检索不返回已删除项。
//   - TestMarkers_Order：`deprecated` + 已删除 + 未过目的卡，显式查看输出**逐字**以
//     `[失效][已删除][未过目]` 开头（字符串前缀比对，不是集合比对），且两次执行逐字相同。

import (
	"path/filepath"
	"strings"
	"testing"
)

// markerVaultCLI 造四象限语料：过目维度由 `reviewed_at` 单独控制（缺省 = 从未过目）。
func markerVaultCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n")
	// ① active + 未删除 + 已过目：默认检索里唯一应出现的卡。
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260901-live.md"),
		"---\nid: k-20260901-live\nstatus: active\ncreated_at: '2026-09-01'\n"+
			"updated_at: '2026-09-01T10:00:00+08:00'\n"+
			"reviewed_at: '2026-09-01T10:00:00+08:00'\ntitle: 在册的卡 注意力\nsources: []\n"+
			"---\n\n## 知识内容\n\n正文占位 注意力。\n")
	// ② active + 已删除 + 已过目：第三象限，只出现在显式查看里，标 [已删除]。
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260903-del.md"),
		"---\nid: k-20260903-del\nstatus: active\ncreated_at: '2026-09-03'\n"+
			"updated_at: '2026-09-03T10:00:00+08:00'\n"+
			"reviewed_at: '2026-09-03T10:00:00+08:00'\n"+
			"deleted_at: '2026-09-05T10:00:00+08:00'\ndeleted_reason: 用户判断这条已过时\n"+
			"title: 已删除的卡 注意力\nsources: []\n---\n\n## 知识内容\n\n正文占位 注意力。\n")
	// ③ active + 未删除 + **缺 reviewed_at**：从未过目，标 [未过目]。
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260902-new.md"),
		"---\nid: k-20260902-new\nstatus: active\ncreated_at: '2026-09-02'\n"+
			"updated_at: '2026-09-02T10:00:00+08:00'\ntitle: 没过目的卡 注意力\nsources: []\n"+
			"---\n\n## 知识内容\n\n正文占位 注意力。\n")
	// ④ deprecated + 已删除 + 缺 reviewed_at：三个维度同时命中，双标记 + 过目标记。
	writeFileMk(t, filepath.Join(dir, "domains", "ai-infra", "knowledge", "k-20260904-both.md"),
		"---\nid: k-20260904-both\nstatus: deprecated\ncreated_at: '2026-09-04'\n"+
			"updated_at: '2026-09-04T10:00:00+08:00'\n"+
			"deleted_at: '2026-09-06T10:00:00+08:00'\ndeleted_reason: 失效后又被删除\n"+
			"title: 双标记的卡 注意力\nsources: []\n---\n\n## 知识内容\n\n正文占位 注意力。\n")
	return dir
}

// TestMarkers_DeletedAndUnreviewed —— 两个新标记各自独立成立（三维正交），
// JSON 布尔字段与文本标记一一对应，且默认检索不返回已删除项。
func TestMarkers_DeletedAndUnreviewed(t *testing.T) {
	dir := markerVaultCLI(t)

	// —— [已删除]：显式查看可见并标记，deleted == true、unreviewed == false ——
	code, env, _ := runCardShowJSON(t, dir, "k-20260903-del")
	if code != ExitOK {
		t.Fatalf("已删除卡显式查看退出码 = %d，期望 0（可显式查看）", code)
	}
	data, _ := env["data"].(map[string]interface{})
	if data["deleted"] != true || data["unreviewed"] != false {
		t.Fatalf("已删除但已过目：deleted / unreviewed = %v / %v，期望 true / false",
			data["deleted"], data["unreviewed"])
	}
	if !markerListEquals(data["markers"], []string{MarkerDeleted}) {
		t.Fatalf("markers = %v，期望 [%s]", data["markers"], MarkerDeleted)
	}
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260903-del")
	if line := titleLineOf(t, out, "k-20260903-del"); !strings.HasPrefix(line, MarkerDeleted) {
		t.Fatalf("文本模式标题行应以 %s 开头：%q", MarkerDeleted, line)
	}

	// —— [未过目]：缺 reviewed_at 即命中，且不连带删除维度 ——
	_, env2, _ := runCardShowJSON(t, dir, "k-20260902-new")
	data2, _ := env2["data"].(map[string]interface{})
	if data2["unreviewed"] != true || data2["deleted"] != false {
		t.Fatalf("缺 reviewed_at 且未删除：unreviewed / deleted = %v / %v，期望 true / false",
			data2["unreviewed"], data2["deleted"])
	}
	if !markerListEquals(data2["markers"], []string{MarkerUnreviewed}) {
		t.Fatalf("markers = %v，期望 [%s]", data2["markers"], MarkerUnreviewed)
	}
	_, out2, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260902-new")
	if line := titleLineOf(t, out2, "k-20260902-new"); !strings.HasPrefix(line, MarkerUnreviewed) {
		t.Fatalf("文本模式标题行应以 %s 开头：%q", MarkerUnreviewed, line)
	}

	// —— 默认检索不返回已删除项；显式开关才带回并标 [已删除] ——
	code, searchEnv, _ := runSearchJSON(t, dir, "注意力")
	if code != ExitOK {
		t.Fatalf("search 退出码 = %d，期望 0", code)
	}
	got := markerHitIDs(t, searchEnv)
	for _, deleted := range []string{"k-20260903-del", "k-20260904-both"} {
		if got[deleted] {
			t.Fatalf("默认检索返回了已删除项 %s（§5.1 默认检索列 🔴）", deleted)
		}
	}
	if !got["k-20260901-live"] || !got["k-20260902-new"] {
		t.Fatalf("默认检索漏了未删除的卡：%v", got)
	}
	code, inclEnv, _ := runSearchJSON(t, dir, "注意力", "--"+SearchIncludeDeletedFlag)
	if code != ExitOK {
		t.Fatalf("search --%s 退出码 = %d，期望 0", SearchIncludeDeletedFlag, code)
	}
	if incl := markerHitIDs(t, inclEnv); !incl["k-20260903-del"] || !incl["k-20260904-both"] {
		t.Fatalf("--%s 应把已删除项带回，实际 %v", SearchIncludeDeletedFlag, incl)
	}
	_, inclText, _ := runCLI(t, r, "--vault", dir, "search", "注意力",
		"--"+SearchIncludeDeletedFlag)
	if !strings.Contains(inclText, MarkerDeleted+"k-20260903-del") {
		t.Fatalf("显式带回的已删除项应标 %s：%q", MarkerDeleted, inclText)
	}
	// 检索是排序路径：ADR-20 的受限信号不得渗进来（入口只有 eg unreviewed）。
	if strings.Contains(inclText, MarkerUnreviewed) {
		t.Fatalf("检索输出不得携带 %s（ADR-20：该信号只出现在筛选条件里）：%q",
			MarkerUnreviewed, inclText)
	}
}

// TestMarkers_Order —— 双标记顺序：`deprecated` + 已删除 + 未过目的卡，显式查看输出
// **逐字**以 `[失效][已删除][未过目]` 开头（字符串前缀比对，非集合比对）。
func TestMarkers_Order(t *testing.T) {
	dir := markerVaultCLI(t)
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260904-both")

	// 逐字前缀：顺序本身就是判据，写成字面量而不是拼集合。
	// 比对对象是卡的标题行（信封首行是退出状态，不属于卡视图）。
	const wantPrefix = "[失效][已删除][未过目]"
	line := titleLineOf(t, out, "k-20260904-both")
	if !strings.HasPrefix(line, wantPrefix) {
		t.Fatalf("显式查看的标题行未以 %q 逐字开头：%q", wantPrefix, line)
	}
	// 三个常量拼出来的顺序必须与那个字面量一致（防止有人只改常量不改顺序）。
	if got := MarkerDeprecated + MarkerDeleted + MarkerUnreviewed; got != wantPrefix {
		t.Fatalf("标记常量拼接 = %q，期望 %q", got, wantPrefix)
	}
	// JSON 侧 markers 也是同一顺序，且三个布尔字段全 true。
	_, env, _ := runCardShowJSON(t, dir, "k-20260904-both")
	data, _ := env["data"].(map[string]interface{})
	if !markerListEquals(data["markers"],
		[]string{MarkerDeprecated, MarkerDeleted, MarkerUnreviewed}) {
		t.Fatalf("markers = %v，期望三标记按固定顺序", data["markers"])
	}
	if data["deprecated"] != true || data["deleted"] != true || data["unreviewed"] != true {
		t.Fatalf("三个布尔字段应全 true：%v / %v / %v",
			data["deprecated"], data["deleted"], data["unreviewed"])
	}
	// 确定性：同一语料两次执行输出逐字相同（M2 冻结口径）。
	_, again, _ := runCLI(t, r, "--vault", dir, "card", "show", "k-20260904-both")
	if again != out {
		t.Fatalf("同一语料两次执行输出不同：\n%q\n%q", out, again)
	}
}

// titleLineOf 取出人类可读输出里那张卡的标题行（标记前缀就在这一行行首）。
func titleLineOf(t *testing.T, out string, id string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, id+"  ") {
			return line
		}
	}
	t.Fatalf("输出里找不到 %s 的标题行：%q", id, out)
	return ""
}

// markerListEquals 比对 --json 里的 markers 数组与期望列表（含顺序）。
func markerListEquals(got interface{}, want []string) bool {
	list, ok := got.([]interface{})
	if !ok || len(list) != len(want) {
		return false
	}
	for i, w := range want {
		if list[i] != w {
			return false
		}
	}
	return true
}

// markerHitIDs 折出 hits[].id 集合（本组用例只关心「在不在」，不关心顺序）。
func markerHitIDs(t *testing.T, env map[string]interface{}) map[string]bool {
	t.Helper()
	data, _ := env["data"].(map[string]interface{})
	hits, _ := data["hits"].([]interface{})
	out := map[string]bool{}
	for _, h := range hits {
		m, _ := h.(map[string]interface{})
		id, _ := m["id"].(string)
		out[id] = true
	}
	return out
}
