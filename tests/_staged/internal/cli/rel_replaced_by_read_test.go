package cli

// [S4 · Phase6C] `eg rel --replaced-by` **读视图**的 CLI 合同（M5 查询合同 §8.4 + CLI 合同 §3）。
//
// rel_test.go 钉的是**论证关系**读视图的 data 键序 / 元素五键 / 双渲染同源；本文件把同一套
// CLI 合同判据落到**替代指针**视图（`--replaced-by`）上，且覆盖跨类型端点（宿主 / 目标可为
// 知识卡或观点）：
//   - JSON data 键序仍是 id / relations_out / relations_in / scanned_files / skipped_files（不因视图切换而改写）；
//   - 关系条目仍恰 from / type / target / reason / path 五键，`type` 逐字 `replaced_by`；
//   - 反向跨 k/o 两类宿主都出现，reason 取自权威逐字原值；
//   - 文本模式与 --json 同源同事实（正向 / 反向条目数一致，type 渲染为 replaced_by）。
//
// 语料用本文件的 seed 直接落 replaced_by frontmatter（读路径合同判据，与写链解耦：写链闭环由
// replaced_by_ko_cli_test.go 单独覆盖）。宿主保持 active，使反向在默认视图即可见——可见性正交
// 已由 query 层聚焦测试钉死，这里只验 CLI 序列化合同，不重复验可见性。

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// rbrSeedCardReplaced / rbrSeedOpinionReplaced 落一张带（可选）replaced_by 的最小合法产物。
func rbrSeedCardReplaced(t *testing.T, root, domain, id, title, target, reason string) {
	t.Helper()
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2027-03-01'\n" +
		"updated_at: '2027-03-02T10:00:00+08:00'\ntitle: " + title + "\nsources: []\n"
	if target != "" {
		fm += "replaced_by:\n  target: " + target + "\n  reason: " + reason + "\n"
	}
	fm += "---\n\n## 知识内容\n\n正文占位。\n"
	writeFileMk(t, filepath.Join(root, "domains", domain, "knowledge", id+".md"), fm)
}

func rbrSeedOpinionReplaced(t *testing.T, root, domain, id, title, validation, target, reason string) {
	t.Helper()
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2027-03-01'\n" +
		"updated_at: '2027-03-02T10:00:00+08:00'\ntitle: " + title +
		"\nvalidation: " + validation + "\nsources: []\n"
	if target != "" {
		fm += "replaced_by:\n  target: " + target + "\n  reason: " + reason + "\n"
	}
	fm += "---\n\n# " + title + "\n\n## 观点\n\n主张一句。\n"
	writeFileMk(t, filepath.Join(root, "domains", domain, "opinions", id+".md"), fm)
}

const (
	rbrNewK = "k-20270301-newk" // 目标知识卡（active）
	rbrOldK = "k-20270301-oldk" // k→k 宿主：replaced_by → rbrNewK
	rbrOldO = "o-20270301-oldo" // o→k 宿主：replaced_by → rbrNewK
	rbrRk   = "旧卡已被新卡取代"
	rbrRo   = "旧观点已被新卡取代"
)

// rbrVault 造替代指针读视图语料：一张目标新卡被一张旧卡（k→k）与一条旧观点（o→k）替代。
func rbrVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n")
	rbrSeedCardReplaced(t, dir, "ai-infra", rbrNewK, "新卡", "", "")
	rbrSeedCardReplaced(t, dir, "ai-infra", rbrOldK, "旧卡", rbrNewK, rbrRk)
	rbrSeedOpinionReplaced(t, dir, "ai-infra", rbrOldO, "旧观点", "rejected", rbrNewK, rbrRo)
	return dir
}

// TestRelReplacedByReverseJSONContract —— 反向视图（本端点取代了谁的逆向：谁取代了本端点）
// 的 JSON 合同：data 键序不变、元素恰五键、type 逐字 replaced_by、跨 k/o 两类宿主都现身且
// reason 权威。
func TestRelReplacedByReverseJSONContract(t *testing.T) {
	dir := rbrVault(t)
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "--vault", dir, "--json", "rel", rbrNewK, "--replaced-by")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（%s / %s）", code, out, errOut)
	}
	// data 键序与关系条目键序都不因视图切换而改写（合同 §3.1）。
	assertKeyOrder(t, out, strings.Index(out, `"data":`), []string{
		`"id":`, `"relations_out":`, `"relations_in":`, `"scanned_files":`, `"skipped_files":`})
	assertKeyOrder(t, out, strings.Index(out, `"relations_in":`),
		[]string{`"from":`, `"type":`, `"target":`, `"reason":`, `"path":`})

	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v", err)
	}
	assertEnvelopeKeys(t, env)
	data, _ := env["data"].(map[string]interface{})
	in, _ := data["relations_in"].([]interface{})
	// 反向两条：k→k 宿主 rbrOldK 与 o→k 宿主 rbrOldO，按 from 升序（k- < o-）。
	if len(in) != 2 {
		t.Fatalf("反向条目数 = %d，期望 2（跨 k/o 两类宿主）：%v", len(in), in)
	}
	wantFrom := []string{rbrOldK, rbrOldO}
	wantReason := map[string]string{rbrOldK: rbrRk, rbrOldO: rbrRo}
	for i, e := range in {
		m, _ := e.(map[string]interface{})
		if len(m) != 5 {
			t.Fatalf("反向条目键数 = %d，期望恰 5：%v", len(m), m)
		}
		if m["type"] != "replaced_by" {
			t.Fatalf("条目 type = %v，期望逐字 replaced_by", m["type"])
		}
		if m["target"] != rbrNewK {
			t.Fatalf("反向条目 target 应恒为焦点 %s，实际 %v", rbrNewK, m["target"])
		}
		if fmt.Sprint(m["from"]) != wantFrom[i] {
			t.Fatalf("反向第 %d 条 from = %v，期望 %s（按 from 升序）", i, m["from"], wantFrom[i])
		}
		if fmt.Sprint(m["reason"]) != wantReason[wantFrom[i]] {
			t.Fatalf("宿主 %s 的 reason = %v，期望权威原值 %q",
				wantFrom[i], m["reason"], wantReason[wantFrom[i]])
		}
	}
	// 正向为空：没人取代 rbrNewK。
	if outEdges, _ := data["relations_out"].([]interface{}); len(outEdges) != 0 {
		t.Fatalf("rbrNewK 无正向替代指针，relations_out 应为空：%v", outEdges)
	}
}

// TestRelReplacedByForwardJSONContract —— 正向视图（谁取代了本端点）：宿主焦点恰一条边，
// 元素五键、type replaced_by、target 指向新卡、reason 权威。
func TestRelReplacedByForwardJSONContract(t *testing.T) {
	dir := rbrVault(t)
	r := newTestRoot(t, dir)
	code, out, errOut := runCLI(t, r, "--vault", dir, "--json", "rel", rbrOldK, "--replaced-by")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（%s / %s）", code, out, errOut)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json 输出不是合法 JSON：%v", err)
	}
	data, _ := env["data"].(map[string]interface{})
	outEdges, _ := data["relations_out"].([]interface{})
	if len(outEdges) != 1 {
		t.Fatalf("正向条目数 = %d，期望恰 1（单向存储）：%v", len(outEdges), outEdges)
	}
	m, _ := outEdges[0].(map[string]interface{})
	if len(m) != 5 || m["type"] != "replaced_by" || m["from"] != rbrOldK ||
		m["target"] != rbrNewK || fmt.Sprint(m["reason"]) != rbrRk {
		t.Fatalf("正向条目不符合合同（五键 / replaced_by / from=oldk / target=newk / reason 权威）：%v", m)
	}
	// 宿主自己没有反向来源（没人取代它）。
	if in, _ := data["relations_in"].([]interface{}); len(in) != 0 {
		t.Fatalf("rbrOldK 无反向来源，relations_in 应为空：%v", in)
	}
}

// TestRelReplacedByTextRendering —— 文本模式与 --json 同源同事实：反向两条渲染为
// `--replaced_by-->` 且指向焦点，type 逐字 replaced_by（不因视图切换改渲染格式）。
func TestRelReplacedByTextRendering(t *testing.T) {
	dir := rbrVault(t)
	r := newTestRoot(t, dir)
	_, out, _ := runCLI(t, r, "--vault", dir, "rel", rbrNewK, "--replaced-by")
	for _, want := range []string{
		"反向关系：" + rbrOldK + " --replaced_by--> " + rbrNewK,
		"反向关系：" + rbrOldO + " --replaced_by--> " + rbrNewK,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("文本模式缺反向替代指针条目 %q：%s", want, out)
		}
	}
	// 与 data 同源：反向恰两条。
	if n := strings.Count(out, "反向关系："); n != 2 {
		t.Fatalf("文本反向行数 = %d，期望 2：%s", n, out)
	}
}
