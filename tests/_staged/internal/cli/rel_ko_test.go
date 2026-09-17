package cli

// T-…-006-B2c Phase 4 的 CLI 层机器判据：`eg rel` 的 **k/o 端点闭环**——读路径
// （eg rel <o-id> / --to <o-id>）与写路径（eg rel add|remove 的 from/target 四组合
// k→k / k→o / o→k / o→o）都必须落在既有的单一 runPlan 链上，不新建第二条写入链路。
//
// 判据来源：M2 查询与关系写入合同 §3（读：data/条目键序、正反向、--to、Q 系列、零副作用）、
// §4（写：复用现有写入链路、opposing 归一去重、退出码）；读路径拆分设计（关系端点宇宙
// = 知识卡 ∪ 观点，见 model.RelationEndpoint / query.RelView）。B2a/B2b 已在 query 层
// 钉死 o-id 焦点投影与降级等价，本支只钉 **CLI 接通后**的对外事实：全程走真实 CLI → vault →
// store → git（绝不伪造 DTO、不打桩写入）。
//
// 命名与既有读写用例族对齐：读路径用例以 RelQueryKO 开头（承接 rel_test.go），
// 写路径用例以 RelAddKO / RelRemoveKO 开头（承接 rel_add_test.go / rel_remove_test.go）。

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const (
	relKOKa      = "k-20270101-alpha"
	relKOKb      = "k-20270101-bravo"
	relKOOa      = "o-20270101-alpha"
	relKOOb      = "o-20270101-bravo"
	relKOMissing = "k-20270109-missing"
)

// relKOOpinionPath 是观点在 vault 内的落位路径（domains/<d>/opinions/<o-id>.md）。
func relKOOpinionPath(dir, domain, id string) string {
	return filepath.Join(dir, "domains", domain, "opinions", id+".md")
}

// relKOHostPath 按端点前缀取宿主文件路径（k- 知识卡 / o- 观点），供字节级取证。
func relKOHostPath(dir, id string) string {
	if strings.HasPrefix(id, "o-") {
		return relKOOpinionPath(dir, "ai-infra", id)
	}
	return relAddCardPath(dir, "ai-infra", id)
}

// relKORel 把宿主绝对路径折成 vault 内 slash 相对路径（authoritySnapshot 的键口径）。
func relKORel(t *testing.T, dir, abs string) string {
	t.Helper()
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		t.Fatalf("求相对路径失败：%v", err)
	}
	return filepath.ToSlash(rel)
}

// seedRelOpinion 写一条带（可选）relations[] 的最小合法观点（测试脚手架，非产品写路径）。
func seedRelOpinion(t *testing.T, root, domain, id, title, relations string) {
	t.Helper()
	fm := "---\nid: " + id + "\nstatus: active\ncreated_at: '2027-01-01'\n" +
		"updated_at: '2027-01-01T10:00:00+08:00'\ntitle: " + title + "\n" +
		"validation: pending\nsources: []\n"
	if relations != "" {
		fm += "relations:\n" + relations
	}
	fm += "---\n\n# " + title + "\n\n## 观点\n\n正文占位。\n"
	writeFileMk(t, relKOOpinionPath(root, domain, id), fm)
}

// —— 读路径语料（不需 git：读路径只读，全库 Markdown 扫描）——
//
// k-a --supports--> o-a（供 o-a 的反向观察点）；o-a --supports--> k-b、
// o-a --opposing--> k-missing（悬空，供 Q2）。o-b / k-b 无正向关系。
func relKOReadVault(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ConfigFileName),
		"version: 1\ndomains:\n  - ai-infra\ndefault_domain: ai-infra\n")
	seedRelCard(t, dir, "ai-infra", relKOKa, "A 卡",
		"  - type: supports\n    target: "+relKOOa+"\n    reason: A 卡支持观点 A\n")
	seedRelCard(t, dir, "ai-infra", relKOKb, "B 卡", "")
	seedRelOpinion(t, dir, "ai-infra", relKOOa, "观点A",
		"  - type: supports\n    target: "+relKOKb+"\n    reason: 观点 A 支持 B 卡\n"+
			"  - type: opposing\n    target: "+relKOMissing+"\n    reason: 指向不存在的卡\n")
	seedRelOpinion(t, dir, "ai-infra", relKOOb, "观点B", "")
	return dir
}

// —— ① 读：eg rel <o-id> —— data/条目键序不变、正向 = 观点自身 relations[]、
// 反向 = 全库反查、悬空产 Q2、文本可读 ——

func TestRelQueryKOOpinionFocusReadable(t *testing.T) {
	dir := relKOReadVault(t)

	// JSON data 键序与条目键序逐字沿用合同 §3.1（o-id 焦点不得让 schema 分叉）。
	code, out, errOut := runCLI(t, newTestRoot(t, dir), "--vault", dir, "--json", "rel", relKOOa)
	if code != ExitOK {
		t.Fatalf("eg rel <o-id> 退出码 = %d，期望 0（%s / %s）", code, out, errOut)
	}
	assertKeyOrder(t, out, strings.Index(out, `"data":`), []string{
		`"id":`, `"relations_out":`, `"relations_in":`, `"scanned_files":`, `"skipped_files":`})
	assertKeyOrder(t, out, strings.Index(out, `"relations_out":`),
		[]string{`"from":`, `"type":`, `"target":`, `"reason":`, `"path":`})

	_, env, _ := runRelJSON(t, dir, relKOOa)
	data, _ := env["data"].(map[string]interface{})
	if data["id"] != relKOOa {
		t.Fatalf("焦点 id = %v，期望 %s", data["id"], relKOOa)
	}
	// 正向 = 观点自身 relations[]：opposing 排在 supports 前（type 固定次序），from 恒为 o-a。
	if got := relEdgeSigs(t, data, "relations_out", "target"); strings.Join(got, ",") !=
		"opposing("+relKOMissing+"),supports("+relKOKb+")" {
		t.Fatalf("relations_out = %v，期望 [opposing(missing), supports(k-b)]", got)
	}
	fwd, _ := data["relations_out"].([]interface{})
	for _, e := range fwd {
		m, _ := e.(map[string]interface{})
		if m["from"] != relKOOa {
			t.Fatalf("正向 from = %v，应恒为焦点观点 ID %s", m["from"], relKOOa)
		}
	}
	// 反向 = 全库反查（知识卡也能反查到观点）：k-a --supports--> o-a。
	if got := relEdgeSigs(t, data, "relations_in", "from"); strings.Join(got, ",") !=
		"supports("+relKOKa+")" {
		t.Fatalf("relations_in = %v，期望 [supports(k-a)]", got)
	}
	// 悬空 opposing→missing 产恰一条 Q2，退出码仍 0（条目不消失）。
	warns, _ := env["warnings"].([]interface{})
	q2 := 0
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		if m["code"] == "Q2" && strings.Contains(fmt.Sprint(m["message"]), relKOMissing) {
			q2++
		}
	}
	if q2 != 1 {
		t.Fatalf("o-id 焦点的悬空 opposing 应恰一条 Q2，实际 warnings=%v", warns)
	}

	// 文本模式同源同事实：正反向都可读，悬空标注「目标不存在」。
	_, textOut, _ := runCLI(t, newTestRoot(t, dir), "--vault", dir, "rel", relKOOa)
	for _, want := range []string{
		"正向关系：" + relKOOa + " --opposing--> " + relKOMissing,
		"正向关系：" + relKOOa + " --supports--> " + relKOKb,
		"反向关系：" + relKOKa + " --supports--> " + relKOOa,
		"（目标不存在）",
	} {
		if !strings.Contains(textOut, want) {
			t.Fatalf("文本模式缺 %q：\n%s", want, textOut)
		}
	}
}

// —— ② 读：--to <o-id> 正反向过滤 + k-id 回归不变 ——

func TestRelQueryKOToEndpointAndKIDRegression(t *testing.T) {
	dir := relKOReadVault(t)

	// eg rel k-a --to o-a：正向只留对端 == o-a 的一条 supports(o-a)，退 0。
	code, env, _ := runRelJSON(t, dir, relKOKa, "--to", relKOOa)
	if code != ExitOK {
		t.Fatalf("eg rel k-a --to o-a 退出码 = %d，期望 0", code)
	}
	data, _ := env["data"].(map[string]interface{})
	if got := relEdgeSigs(t, data, "relations_out", "target"); strings.Join(got, ",") !=
		"supports("+relKOOa+")" {
		t.Fatalf("--to o-a 过滤后 relations_out = %v，期望 [supports(o-a)]", got)
	}
	if inEdges, _ := data["relations_in"].([]interface{}); len(inEdges) != 0 {
		t.Fatalf("k-a 无反向关系，--to 过滤后仍应为空：%v", inEdges)
	}

	// k-id 回归：eg rel k-b 的反向恰含观点 o-a（o-a --supports--> k-b），行为与 k-only 时一致。
	_, envB, _ := runRelJSON(t, dir, relKOKb)
	dataB, _ := envB["data"].(map[string]interface{})
	if got := relEdgeSigs(t, dataB, "relations_in", "from"); strings.Join(got, ",") !=
		"supports("+relKOOa+")" {
		t.Fatalf("k-b 的反向 = %v，期望 [supports(o-a)]（k-id 回归）", got)
	}

	// --to 指向不存在的端点：结果为空 + 恰一条 Q2，仍退 0（可见性/退出码不变）。
	code, envM, _ := runRelJSON(t, dir, relKOOa, "--to", "o-20270109-none")
	if code != ExitOK {
		t.Fatalf("--to 指向不存在端点仍应退 0，实际 %d", code)
	}
	dataM, _ := envM["data"].(map[string]interface{})
	outM, _ := dataM["relations_out"].([]interface{})
	inM, _ := dataM["relations_in"].([]interface{})
	if len(outM) != 0 || len(inM) != 0 {
		t.Fatalf("--to 指向不存在端点时结果应为空：%v / %v", outM, inM)
	}
	warns, _ := envM["warnings"].([]interface{})
	hit := 0
	for _, w := range warns {
		m, _ := w.(map[string]interface{})
		if m["code"] == "Q2" && strings.Contains(fmt.Sprint(m["message"]), "--to") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("--to 不存在端点应恰一条 Q2，实际 warnings=%v", warns)
	}
}

// —— ③ 读：o-id / --to o-id 路径三维零副作用 ——

func TestRelQueryKOReadZeroSideEffect(t *testing.T) {
	dir := relKOReadVault(t)
	before := vaultSnapshot(t, dir)
	for _, args := range [][]string{
		{"rel", relKOOa},
		{"rel", relKOKa, "--to", relKOOa},
		{"rel", relKOOa, "--to", relKOKb},
	} {
		code, _, _ := runCLI(t, newTestRoot(t, dir), append([]string{"--vault", dir}, args...)...)
		if code != ExitOK && code != ExitUsage {
			t.Fatalf("eg %v 退出码 = %d：只读命令只可能 0 / 1", args, code)
		}
	}
	if after := vaultSnapshot(t, dir); after != before {
		t.Fatalf("读路径必须零副作用（字节 / mtime 不变）：\n%s\n%s", before, after)
	}
}

// —— 写路径语料（真实 git 仓）：两卡两观点，均无关系、工作区干净 ——
func relKOWriteVault(t *testing.T) string {
	t.Helper()
	dir := captureVault(t) // eg init：真实 git 仓 + evergreen.yml（default_domain=ai-infra）
	seedRelCard(t, dir, "ai-infra", relKOKa, "A 卡", "")
	seedRelCard(t, dir, "ai-infra", relKOKb, "B 卡", "")
	seedRelOpinion(t, dir, "ai-infra", relKOOa, "观点A", "")
	seedRelOpinion(t, dir, "ai-infra", relKOOb, "观点B", "")
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "seed: rel k/o 语料")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

// —— ④ 写：rel add 四组合 k→k / k→o / o→k / o→o 都走单一 runPlan 链 ——
//
// 每组：恰一次 relate commit；只改规范化后的 from 宿主（这里非 opposing，from 即宿主）；
// 另一端字节不变、全库其余文件字节不变；正反向都能读回。
func TestRelAddKOFourCombinations(t *testing.T) {
	for _, c := range []struct {
		name, from, target string
	}{
		{"k_to_k", relKOKa, relKOKb},
		{"k_to_o", relKOKa, relKOOa},
		{"o_to_k", relKOOa, relKOKa},
		{"o_to_o", relKOOa, relKOOb},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := relKOWriteVault(t)
			hostPath := relKOHostPath(dir, c.from)
			otherPath := relKOHostPath(dir, c.target)
			otherBefore := mustRead(t, otherPath)
			authBefore := authoritySnapshot(t, dir)
			logBefore := gitLogCount(t, dir)

			code, _, errOut := runRelAddCLI(t, dir, c.from, "supports", c.target,
				"--reason", "跨类型端点：结论互相支持")
			if code != ExitOK {
				t.Fatalf("rel add %s→%s 退出码 = %d：%s", c.from, c.target, code, errOut)
			}
			// 恰一次 relate commit（verb 不退化）。
			if got := gitLogCount(t, dir) - logBefore; got != 1 {
				t.Fatalf("一次 rel add 必须恰一次 commit，实际 +%d", got)
			}
			if subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s")); !strings.HasPrefix(
				subject, "relate(ai-infra): ") {
				t.Fatalf("commit 主题须以 relate(ai-infra): 开头，实得 %q", subject)
			}
			// 宿主（from 端）拿到新关系；另一端字节不变。
			host := string(mustRead(t, hostPath))
			for _, want := range []string{"- type: 'supports'", "target: '" + c.target + "'"} {
				if !strings.Contains(host, want) {
					t.Fatalf("from 宿主 %s 缺 %q：\n%s", c.from, want, host)
				}
			}
			if got := mustRead(t, otherPath); string(got) != string(otherBefore) {
				t.Fatalf("对端 %s 必须逐字节不变（关系单向写在 from 宿主）", c.target)
			}
			// 全库权威：只有 from 宿主一个文件变了。
			assertOnlyChanged(t, dir, authBefore, relKORel(t, dir, hostPath),
				"rel add "+c.name)
			// 正反向读回。
			_, envF, _ := runRelJSON(t, dir, c.from)
			dataF, _ := envF["data"].(map[string]interface{})
			if got := relEdgeSigs(t, dataF, "relations_out", "target"); strings.Join(got, ",") !=
				"supports("+c.target+")" {
				t.Fatalf("eg rel %s 正向应读回 supports(%s)，实际 %v", c.from, c.target, got)
			}
			_, envR, _ := runRelJSON(t, dir, c.target)
			dataR, _ := envR["data"].(map[string]interface{})
			if got := relEdgeSigs(t, dataR, "relations_in", "from"); strings.Join(got, ",") !=
				"supports("+c.from+")" {
				t.Fatalf("eg rel %s 反向应读回 supports(%s)，实际 %v", c.target, c.from, got)
			}
		})
	}
}

// —— ⑤ 写：跨类型 opposing 规范化（写在字典序较小端）+ 同对重复幂等 ——
func TestRelAddKOOpposingCrossKindNormalizedIdempotent(t *testing.T) {
	dir := relKOWriteVault(t)
	// 非字典序方向：o-a opposing k-a（"k-…" < "o-…"，规范化后宿主应落到 k-a）。
	code, env, errOut := runRelAddCLI(t, dir, relKOOa, "opposing", relKOKa,
		"--reason", "跨类型结论互斥")
	if code != ExitOK {
		t.Fatalf("跨类型 opposing 退出码 = %d：%s", code, errOut)
	}
	if !strings.Contains(relAddWarningCodes(env), "W8") {
		t.Fatalf("方向被规范化必须记 W8：%s", relAddWarningCodes(env))
	}
	cardKa := string(mustRead(t, relKOHostPath(dir, relKOKa)))
	opOa := string(mustRead(t, relKOHostPath(dir, relKOOa)))
	if !strings.Contains(cardKa, "type: 'opposing'") || !strings.Contains(cardKa, "target: '"+relKOOa+"'") {
		t.Fatalf("opposing 必须写在字典序在前的 k-a 端：\n%s", cardKa)
	}
	if strings.Contains(opOa, "type: 'opposing'") {
		t.Fatalf("字典序在后的 o-a 端不得留 opposing 记录（单向存储）：\n%s", opOa)
	}
	if total := strings.Count(cardKa, "type: 'opposing'") + strings.Count(opOa, "type: 'opposing'"); total != 1 {
		t.Fatalf("全库 opposing 记录必须恰一条，实际 %d 条", total)
	}

	// 同对反向重复：幂等——W8 幂等跳过、不新增条目、不产生空 commit。
	logBefore := gitLogCount(t, dir)
	code, env, errOut = runRelAddCLI(t, dir, relKOKa, "opposing", relKOOa,
		"--reason", "跨类型结论互斥")
	if code != ExitOK {
		t.Fatalf("同对 opposing 重复应退 0，实际 %d：%s", code, errOut)
	}
	if !strings.Contains(relAddWarningCodes(env), "W8") ||
		!strings.Contains(relAddWarningCodes(env), "幂等跳过") {
		t.Fatalf("同对已存在必须记 W8「幂等跳过」：%s", relAddWarningCodes(env))
	}
	if got := gitLogCount(t, dir); got != logBefore {
		t.Fatalf("无实际改动不得产生空 commit：%d → %d", logBefore, got)
	}
	if after := string(mustRead(t, relKOHostPath(dir, relKOKa))); strings.Count(after, "type: 'opposing'") != 1 {
		t.Fatalf("同对 opposing 不得产生第二条：\n%s", after)
	}
	if s := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); s != "" {
		t.Fatalf("幂等路径不得留下工作区改动：%q", s)
	}
}

// —— ⑥ 写：非法端点（s/n/r/p/畸形/不存在/自环）按既有 E/exit 语义、权威字节零变化 ——
func TestRelAddKOInvalidEndpointsZeroAuthorityChange(t *testing.T) {
	// 退 2（校验失败，锁内发生）：权威 Markdown 逐字节不变、零 commit。
	for _, c := range []struct {
		name, from, target string
	}{
		{"target_source_s", relKOOa, "s-20270101-x"},     // E3：s- 写进 target
		{"target_note_n", relKOOa, "n-20270101-x"},       // E2：非论证端点
		{"target_review_r", relKOOa, "r-20270101-x"},     // E2：非论证端点
		{"target_proposal_p", relKOOa, "p-20270101-x"},   // E2：非论证端点
		{"target_malformed", relKOOa, "o-not valid"},     // E2：端点形态非法
		{"target_missing_o", relKOOa, "o-20270109-none"}, // E2：全库不存在
		{"self_loop_o", relKOOa, relKOOa},                // E5：自环
		{"from_missing_o", "o-20270109-none", relKOKa},   // E2：from 不存在
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := relKOWriteVault(t)
			authBefore := authoritySnapshot(t, dir)
			logBefore := gitLogCount(t, dir)
			code, _, errOut := runRelAddCLI(t, dir, c.from, "supports", c.target, "--reason", "非法端点")
			if code != ExitValidation {
				t.Fatalf("%s 应退 2（校验失败），实际 %d：%s", c.name, code, errOut)
			}
			assertAuthorityUnchanged(t, dir, authBefore, "rel add "+c.name)
			if got := gitLogCount(t, dir); got != logBefore {
				t.Fatalf("%s 校验失败不得产生 commit：%d → %d", c.name, logBefore, got)
			}
		})
	}

	// 退 1（参数形态非法）：type 出四值 / 缺 --reason —— 零写入零 commit。
	for _, c := range []struct {
		name string
		args []string
	}{
		{"type_out_of_set", []string{relKOOa, "frobnicate", relKOKa, "--reason", "x"}},
		{"missing_reason", []string{relKOOa, "supports", relKOKa}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := relKOWriteVault(t)
			treeBefore := vaultSnapshot(t, dir)
			logBefore := gitLogCount(t, dir)
			setGitIdentity(t)
			code, _, errOut := runCLI(t, newTestRoot(t, dir),
				append([]string{"--vault", dir, "rel", "add"}, c.args...)...)
			if code != ExitUsage {
				t.Fatalf("%s 应退 1，实际 %d：%s", c.name, code, errOut)
			}
			if got := vaultSnapshot(t, dir); got != treeBefore {
				t.Fatalf("%s 必须零写入（字节 / mtime 不变）", c.name)
			}
			if got := gitLogCount(t, dir); got != logBefore {
				t.Fatalf("%s 不得产生 commit：%d → %d", c.name, logBefore, got)
			}
		})
	}
}

// —— ⑦ 写：rel remove 的 k/o 四组合物理移除 + 未命中 W10 幂等零 commit ——
func TestRelRemoveKOFourCombinations(t *testing.T) {
	for _, c := range []struct {
		name, from, target string
	}{
		{"k_to_k", relKOKa, relKOKb},
		{"k_to_o", relKOKa, relKOOa},
		{"o_to_k", relKOOa, relKOKa},
		{"o_to_o", relKOOa, relKOOb},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := relKOWriteVault(t)
			// 先经真实 CLI 写入一条待删关系（复用同一 runPlan 链）。
			if code, _, errOut := runRelAddCLI(t, dir, c.from, "supports", c.target,
				"--reason", "待会删掉"); code != ExitOK {
				t.Fatalf("前置 rel add 退出码 = %d：%s", code, errOut)
			}
			hostPath := relKOHostPath(dir, c.from)
			otherPath := relKOHostPath(dir, c.target)
			otherBefore := mustRead(t, otherPath)
			logBefore := gitLogCount(t, dir)

			code, _, errOut := runRelRemoveCLI(t, dir, c.from, "supports", c.target, "--reason", "撤销该关系")
			if code != ExitOK {
				t.Fatalf("rel remove %s→%s 退出码 = %d：%s", c.from, c.target, code, errOut)
			}
			// 恰一次 relate commit。
			if got := gitLogCount(t, dir) - logBefore; got != 1 {
				t.Fatalf("一次成功删除应恰一次 commit，实际 +%d", got)
			}
			if subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s")); !strings.HasPrefix(
				subject, "relate(") {
				t.Fatalf("commit 主题须以 relate( 开头，实得 %q", subject)
			}
			// 宿主物理移除该关系；对端字节不变。
			if host := string(mustRead(t, hostPath)); strings.Contains(host, "target: '"+c.target+"'") {
				t.Fatalf("from 宿主应物理移除 supports→%s：\n%s", c.target, host)
			}
			if got := mustRead(t, otherPath); string(got) != string(otherBefore) {
				t.Fatalf("对端 %s 必须逐字节不变", c.target)
			}
			// 重跑：W10 幂等——退 0、零新增 commit、字节不变。
			hostAfter := mustRead(t, hostPath)
			logAfter := gitLogCount(t, dir)
			code2, env2, errOut2 := runRelRemoveCLI(t, dir, c.from, "supports", c.target, "--reason", "撤销该关系")
			if code2 != ExitOK {
				t.Fatalf("重跑应退 0（幂等），实际 %d：%s", code2, errOut2)
			}
			if got := gitLogCount(t, dir); got != logAfter {
				t.Fatalf("未命中不得产生空 commit：%d → %d", logAfter, got)
			}
			if got := mustRead(t, hostPath); string(got) != string(hostAfter) {
				t.Fatalf("重跑必须零写入（字节不变）")
			}
			if !strings.Contains(relAddWarningCodes(env2), "W10") {
				t.Fatalf("重跑必须记 W10，实得 %s", relAddWarningCodes(env2))
			}
		})
	}
}

// —— ⑧ 写：rel remove 与 add 对称的非法端点表 —— 权威字节/mtime 零变化、零 commit ——
//
// 与 TestRelAddKOInvalidEndpointsZeroAuthorityChange 对称：from/target 两侧的
// s-/n-/r-/p-/畸形与「合法但不存在的 k/o」都在 plan 校验期被拦下（E2 / E3），
// 退 2、零权威写、零 commit（既有 runtime lock 语义不算权威写：authoritySnapshot
// 已 SkipDir .git/.index/state，锁文件不落权威快照）。
//
// 自环（from == target）单列：rel remove 与 rel add **不对称**且这是既有行为，本用例如实锁定。
// add 的自环是纯静态 plan 级约束（internal/plan/validate_rel.go 的 E5，两端可解析但取值组合
// 不成立），而 remove 没有这条静态守卫——自环关系根本无从写入，故删它必然「未命中」，
// 落到既有 W10 幂等 no-op：退 0、零写入、零 commit。本 phase 不触碰 plan，故锁既有语义，
// 不臆造一个 remove 侧并不存在的 E5。
func TestRelRemoveKOInvalidEndpointsZeroAuthorityChange(t *testing.T) {
	// 退 2（校验失败，锁内发生）：权威 Markdown 逐字节不变、零 commit。
	for _, c := range []struct {
		name, from, target string
	}{
		{"target_source_s", relKOOa, "s-20270101-x"},     // E3：s- 写进 target
		{"target_note_n", relKOOa, "n-20270101-x"},       // E2：非论证端点
		{"target_review_r", relKOOa, "r-20270101-x"},     // E2：非论证端点
		{"target_proposal_p", relKOOa, "p-20270101-x"},   // E2：非论证端点
		{"target_malformed", relKOOa, "o-not valid"},     // E2：端点形态非法
		{"target_missing_o", relKOOa, "o-20270109-none"}, // E2：全库不存在
		{"from_source_s", "s-20270101-x", relKOKa},       // E2：s- 无法解析为论证端点
		{"from_note_n", "n-20270101-x", relKOKa},         // E2：非论证端点
		{"from_review_r", "r-20270101-x", relKOKa},       // E2：非论证端点
		{"from_proposal_p", "p-20270101-x", relKOKa},     // E2：非论证端点
		{"from_malformed", "o-not valid", relKOKa},       // E2：端点形态非法
		{"from_missing_o", "o-20270109-none", relKOKa},   // E2：from 全库不存在
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := relKOWriteVault(t)
			authBefore := authoritySnapshot(t, dir)
			logBefore := gitLogCount(t, dir)
			code, _, errOut := runRelRemoveCLI(t, dir, c.from, "supports", c.target, "--reason", "非法端点")
			if code != ExitValidation {
				t.Fatalf("%s 应退 2（校验失败），实际 %d：%s", c.name, code, errOut)
			}
			assertAuthorityUnchanged(t, dir, authBefore, "rel remove "+c.name)
			if got := gitLogCount(t, dir); got != logBefore {
				t.Fatalf("%s 校验失败不得产生 commit：%d → %d", c.name, logBefore, got)
			}
		})
	}

	// 自环单列：既有行为是 W10 幂等 no-op（退 0、零权威写、零 commit），非 E5。
	t.Run("self_loop_o_is_W10_noop", func(t *testing.T) {
		dir := relKOWriteVault(t)
		authBefore := authoritySnapshot(t, dir)
		logBefore := gitLogCount(t, dir)
		code, env, errOut := runRelRemoveCLI(t, dir, relKOOa, "supports", relKOOa, "--reason", "自环删")
		if code != ExitOK {
			t.Fatalf("自环 remove 既有行为应退 0（W10 幂等），实际 %d：%s", code, errOut)
		}
		if !strings.Contains(relAddWarningCodes(env), "W10") {
			t.Fatalf("自环 remove 应记 W10 幂等 no-op，实得 %s", relAddWarningCodes(env))
		}
		assertAuthorityUnchanged(t, dir, authBefore, "rel remove self_loop_o")
		if got := gitLogCount(t, dir); got != logBefore {
			t.Fatalf("自环 W10 no-op 不得产生 commit：%d → %d", logBefore, got)
		}
	})
}

// assertOnlyChanged 断言相对权威基线**恰一个** vault 内相对路径发生变化（其余逐字节不变）。
func assertOnlyChanged(t *testing.T, dir string, before map[string]string, wantRel, what string) {
	t.Helper()
	after := authoritySnapshot(t, dir)
	var changed []string
	for rel, h := range after {
		if old, ok := before[rel]; !ok || old != h {
			changed = append(changed, rel)
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			changed = append(changed, rel+"(删除)")
		}
	}
	if len(changed) != 1 || changed[0] != wantRel {
		t.Fatalf("%s：只应改动 %s，实际变化 %v", what, wantRel, changed)
	}
}
