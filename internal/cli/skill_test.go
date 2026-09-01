package cli

// SKILL.md 的机器判据（T-evergreen.s1_main_flow-158614-017 Acceptance）。
//
// 本文件是 T-…-017 的 verify.test（`go test ./internal/cli/... -run Skill`）落点：
// SKILL.md 是「语义面的唯一操作规程」，它写错等于 M1 的「零介入」跑不出来，因此
// 逐条断言的是**文档内容与两份冻结合同的一致性**，而不是措辞好不好看：
//
//  ①  七种处理关系对照表恰 7 行，枚举名集合与 ChangePlan 合同 §2.1 **逐字相等**；
//  ②  coverage_gaps 受控枚举七值集合与合同 §3.2.1 **逐字相等**，且两句底线明文在场；
//  ③  §8.2 的 13 条边界 bullet 逐条可定位（B-01 ~ B-13）+ #dead 全部废弃项在场；
//  ④  第 0 步与「抓取失败 / 正文为空」失败分支的明文可定位；
//  ⑤  base / content_hash 规则与 W6 后果明文可定位；
//  ⑥  退出码 2/3/4 各有处置指引；
//  ⑦  不出现 S2+ 能力承诺（命中处必须带「S1 不可用」标注）；
//  ⑧  两份样例存在，① 带非空 coverage_gaps、② 不带该字段；
//  ⑨  内嵌副本 == 源文件字节（`eg init` 的落盘同源，见 TestInitWritesSkillMD）。
//
// 合同路径是**只读**引用（teamwork 仓），断言两边集合相等即可反证「文档照抄早期草稿」。

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"evergreen/internal/plan"
	"evergreen/skill"
)

const (
	skillSrc         = "../../skill/SKILL.md"
	changePlanSpec   = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-08-changeplan-contract.md"
	egCLISpec        = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-01-eg-cli-contract.md"
	techDesignSpec   = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-evergreen-s1-tech-design.md"
	skillRelationRow = `^\| ` + "`" + `([a-z_]+)` + "`" + ` \|`
)

func skillText(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(skillSrc)
	if err != nil {
		t.Fatalf("读不到 %s：%v", skillSrc, err)
	}
	return string(raw)
}

// readSpec 读 teamwork 侧的只读合同文本（与 initconfig_test.go 的 mustRead 区分：那个返回字节）。
func readSpec(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到 %s：%v", path, err)
	}
	return string(raw)
}

// sortedUnique 归一化成有序去重切片，供集合逐字比对。
func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// TestSkillEmbeddedCopyMatchesSource：内嵌副本与源文件逐字相同（`eg init` 写出的就是它）。
func TestSkillEmbeddedCopyMatchesSource(t *testing.T) {
	raw, err := os.ReadFile(skillSrc)
	if err != nil {
		t.Fatalf("读不到 %s：%v", skillSrc, err)
	}
	if string(skill.Content()) != string(raw) {
		t.Fatal("内嵌 SKILL.md 与 skill/SKILL.md 源文件字节不同：两处内容必须同源")
	}
	if len(raw) < 4096 {
		t.Fatalf("SKILL.md 仅 %d 字节：占位内容不构成操作规程", len(raw))
	}
	for _, stale := range []string{"# SKILL.md（占位）", "**当前为占位内容**", "十一条边界与禁止项\n由 `T-evergreen"} {
		if strings.Contains(string(raw), stale) {
			t.Fatalf("SKILL.md 仍是占位稿（命中 %q）：T-…-017 要求交付正式规程", stale)
		}
	}
}

// TestSkillRelationTableMatchesContract：七行处理关系对照表 + 与合同 §2.1 的集合逐字相等。
func TestSkillRelationTableMatchesContract(t *testing.T) {
	want := []string{
		"independent_new", "same_semantics", "non_core_supplement", "core_change",
		"conflict_coexist", "uncertain", "deprecated",
	}
	doc := skillText(t)

	// ① SKILL.md 的表格恰 7 行，且每行都给出 op 组合。
	rowRE := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| ([^|]+)\\| ([^|]+)\\|\\s*$")
	var got []string
	for _, m := range rowRE.FindAllStringSubmatch(doc, -1) {
		for _, w := range want {
			if m[1] == w {
				got = append(got, m[1])
				if strings.TrimSpace(m[3]) == "" {
					t.Fatalf("relation 行 %s 未给出 op 组合", m[1])
				}
			}
		}
	}
	if len(got) != 7 {
		t.Fatalf("处理关系对照表应恰 7 行，实际定位到 %d 行：%v", len(got), got)
	}
	if strings.Join(sortedUnique(got), ",") != strings.Join(sortedUnique(want), ",") {
		t.Fatalf("七值集合不符：got=%v want=%v", sortedUnique(got), sortedUnique(want))
	}

	// ② 与 ChangePlan 合同 §2.1 的枚举名集合逐字相等（两份文件 grep 结果集合相等）。
	spec := readSpec(t, changePlanSpec)
	enumRE := regexp.MustCompile("`(independent_new|same_semantics|non_core_supplement|core_change|conflict_coexist|uncertain|deprecated)`")
	var specNames []string
	for _, m := range enumRE.FindAllStringSubmatch(spec, -1) {
		specNames = append(specNames, m[1])
	}
	if strings.Join(sortedUnique(specNames), ",") != strings.Join(sortedUnique(want), ",") {
		t.Fatalf("合同 §2.1 的枚举集合 = %v，与 SKILL.md 不一致", sortedUnique(specNames))
	}

	// ③ deprecated 行必须明文「S1/M1 不可用」+「只由用户提出（S2）」。
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| `deprecated`") {
			if !strings.Contains(line, "S1/M1 不可用") || !strings.Contains(line, "只由用户提出（S2）") {
				t.Fatalf("deprecated 行缺少「S1/M1 不可用 / 只由用户提出（S2）」标注：%s", line)
			}
			return
		}
	}
	t.Fatal("未找到 deprecated 行")
}

// TestSkillCoverageGapsMatchesContract：coverage_gaps 七值与合同集合相等 + 两句底线明文。
func TestSkillCoverageGapsMatchesContract(t *testing.T) {
	doc := skillText(t)
	for _, v := range plan.CoverageGaps() {
		if !strings.Contains(doc, "`"+v+"`") {
			t.Fatalf("SKILL.md 缺 coverage_gaps 受控枚举值 %s", v)
		}
	}
	spec := readSpec(t, changePlanSpec)
	gapRE := regexp.MustCompile("`(core_claim|key_evidence|counterexample|boundary|method|conclusion|limitation)`")
	collect := func(s string) []string {
		var out []string
		for _, m := range gapRE.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}
		return sortedUnique(out)
	}
	if strings.Join(collect(doc), ",") != strings.Join(collect(spec), ",") {
		t.Fatalf("coverage_gaps 集合不等：SKILL.md=%v 合同=%v", collect(doc), collect(spec))
	}
	if strings.Join(collect(doc), ",") != strings.Join(sortedUnique(plan.CoverageGaps()), ",") {
		t.Fatalf("coverage_gaps 集合与实现 plan.CoverageGaps() 不等：%v", collect(doc))
	}
	for _, must := range []string{
		"原文未表达的要点写进 `coverage_gaps`",
		"不得编造原文未出现的要点",
		"无缺失时不写该字段",
	} {
		if !strings.Contains(doc, must) {
			t.Fatalf("SKILL.md 缺明文：%s", must)
		}
	}
}

// TestSkillBoundaryBullets：§8.2 的 13 条边界 bullet 逐条可定位 + #dead 全部废弃项在场。
//
// 口径登记：技术方案在 §2.1 / §8.2 caption / §14 EG-AGT-02 三处称「十一条边界」，
// 而 §8.2 code block 实际是 13 条 bullet 且本身无编号，属技术方案内部口径差异（只读、不改方案）。
// 本测试以「13 条 bullet 逐条可定位」为判据，不以编号一致为判据。
func TestSkillBoundaryBullets(t *testing.T) {
	doc := skillText(t)
	bullets := []struct {
		id    string
		marks []string
	}{
		{"B-01", []string{"不得绕过 `eg apply` 直接改文件", "git commit"}},
		{"B-02", []string{"一份 plan 不得写两个 domain"}},
		{"B-03", []string{"只追加三分区", "不改「知识内容」", "「用户补充」任何时候永不写"}},
		{"B-04", []string{"新建卡（`create_card`）五分区都可写"}},
		{"B-05", []string{"禁止替换或删除已有普通块"}},
		{"B-06", []string{"重新加工必须逐字保留用户块"}},
		{"B-07", []string{"禁止生成状态类 op"}},
		{"B-08", []string{"禁止把 `active` 说成「已确认」「已入库」"}},
		{"B-09", []string{"禁止读改其他领域的产物"}},
		{"B-10", []string{"禁止把多跳推导结论沉淀成新卡"}},
		{"B-11", []string{"无法判断领域时落 `default_domain` 且不追问"}},
		{"B-12", []string{"不确定是否同一知识单元就拆两张卡", "逐卡记录三维度比较结论"}},
		{"B-13", []string{"不虚构材料来源与依据", "留空"}},
	}
	if len(bullets) != 13 {
		t.Fatalf("边界清单应为 13 条，实际 %d 条", len(bullets))
	}
	for _, b := range bullets {
		if !strings.Contains(doc, "**"+b.id+" ") {
			t.Fatalf("SKILL.md 缺边界条目 %s", b.id)
		}
		for _, m := range b.marks {
			if !strings.Contains(doc, m) {
				t.Fatalf("边界条目 %s 缺要点明文：%s", b.id, m)
			}
		}
	}
	// #dead 的全部废弃项。
	for _, dead := range []string{
		"`candidate` 字段", "独立未决问题实体", "`open/` 目录", "`source_check`",
		"观点倾向 / `lean` 字段", "物理（永久）删除", "迁移提案", "跨领域能力",
	} {
		if !strings.Contains(doc, dead) {
			t.Fatalf("SKILL.md 缺已废弃设计项：%s", dead)
		}
	}
	// E3 的两个方向都要写死。
	if !strings.Contains(doc, "不得把 `s-` 写进 `relations`") ||
		!strings.Contains(doc, "不得把 `k-` 写进 `sources`") {
		t.Fatal("SKILL.md 缺 E3 双向禁止明文")
	}
}

// TestSkillCallSequenceAndExitCodes：0–5 步调用顺序、第 0 步失败分支、退出码 2/3/4 处置。
func TestSkillCallSequenceAndExitCodes(t *testing.T) {
	doc := skillText(t)
	for _, must := range []string{
		"Agent 自行抓取并清洗正文（CLI 不做网络请求）",
		"抓取失败 / 正文为空 → 不调 `eg capture`，直接在报告写明原因与 URL，不落任何文件",
		"eg capture", "eg context", "eg apply --plan", "eg report --last",
		"`eg context` 返回的 `content_hash` 必须原样填入 `plan.base`",
		"W6",
		"唯一允许调用模型的环节",
		"唯一程序化写入通道",
	} {
		if !strings.Contains(doc, must) {
			t.Fatalf("SKILL.md 缺主链路明文：%s", must)
		}
	}
	// 退出码 2 / 3 / 4 各有处置指引（同一行内既出现码值也出现处置动作）。
	need := map[string][]string{
		"`2`": {"改 plan 后重投"},
		"`3`": {"skipped[]", "content_hash_mismatch"},
		"`4`": {"不做破坏性还原", "不得"},
	}
	for code, marks := range need {
		var hit bool
		for _, line := range strings.Split(doc, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "| "+code+" |") {
				continue
			}
			hit = true
			for _, m := range marks {
				if !strings.Contains(line, m) {
					t.Fatalf("退出码 %s 的处置指引缺要点 %q：%s", code, m, line)
				}
			}
		}
		if !hit {
			t.Fatalf("SKILL.md 退出码表缺 %s 行", code)
		}
	}
	// 三条收敛判据必须是可勾选清单（`- [ ]`），不是叙述段落。
	for _, must := range []string{
		"- [ ] **任一维度 `different` → 拆两张卡**（宁拆勿并）",
		"- [ ] **判不出",
	} {
		if !strings.Contains(doc, must) {
			t.Fatalf("SKILL.md 的收敛判据缺可勾选条目：%s", must)
		}
	}
	if n := strings.Count(doc, "- [ ] "); n < 10 {
		t.Fatalf("可勾选条目仅 %d 条，判据未写成可执行清单", n)
	}
}

// TestSkillNoStageTwoPromise：不出现 S2+ 能力承诺。
//
// 判定按**字段路径 / 能力名**口径，而不是裸关键词扫描（合同 §7 明令禁止关键词扫描）：
// `op_index` 是诊断载荷的合同字段名，不是「索引能力」，按字段路径豁免。
func TestSkillNoStageTwoPromise(t *testing.T) {
	doc := skillText(t)
	keywords := []string{"proposal", "deprecate", "undelete", "reconcile", "index"}
	exempt := regexp.MustCompile(`op_index`)
	for i, line := range strings.Split(doc, "\n") {
		clean := exempt.ReplaceAllString(line, "")
		for _, kw := range keywords {
			if !strings.Contains(clean, kw) {
				continue
			}
			okBan := strings.Contains(clean, "S1 不可用") || strings.Contains(clean, "S1/M1 不可用")
			okVerb := strings.Contains(clean, "≠ S3 `eg reconcile` 命令")
			if !okBan && !okVerb {
				t.Fatalf("第 %d 行命中 S2+ 能力词 %q 但未标注「S1 不可用」/ 消歧句：%s", i+1, kw, line)
			}
		}
	}
}

// TestSkillSamplesAreValidPlans：两份样例可被解析与校验（e2e 另跑 `eg apply --dry-run`）。
func TestSkillSamplesAreValidPlans(t *testing.T) {
	samples := SkillSamples(t)
	if len(samples) != 2 {
		t.Fatalf("SKILL.md 应含恰 2 份 ChangePlan 样例，实际 %d 份", len(samples))
	}
	var withGaps, withoutGaps int
	for i, raw := range samples {
		var m struct {
			PlanVersion int                      `json:"plan_version"`
			Verb        string                   `json:"verb"`
			Domain      string                   `json:"domain"`
			Reason      string                   `json:"reason"`
			Base        map[string]string        `json:"base"`
			Ops         []map[string]interface{} `json:"ops"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("样例 %d 不是合法 JSON：%v", i+1, err)
		}
		if m.PlanVersion != 1 || m.Verb == "" || m.Domain == "" || m.Reason == "" || len(m.Ops) == 0 {
			t.Fatalf("样例 %d 顶层键不完整：%+v", i+1, m)
		}
		if _, err := plan.Parse([]byte(raw)); err != nil {
			t.Fatalf("样例 %d 无法被 plan.Parse 接受：%v", i+1, err)
		}
		for _, op := range m.Ops {
			if op["op"] != "write_note" {
				continue
			}
			if _, ok := op["coverage_gaps"]; ok {
				gaps, _ := op["coverage_gaps"].([]interface{})
				if len(gaps) == 0 {
					t.Fatalf("样例 %d 的 coverage_gaps 为空数组：无缺失时应直接不写该字段", i+1)
				}
				withGaps++
			} else {
				withoutGaps++
			}
		}
	}
	if withGaps < 1 || withoutGaps < 1 {
		t.Fatalf("两份样例应一份带非空 coverage_gaps、一份不带：带 %d 份、不带 %d 份", withGaps, withoutGaps)
	}
	// 样例覆盖 §3.2 表格的两种主路径：新建卡 + 复用已有卡（append_card 三分区）。
	joined := strings.Join(samples, "\n")
	for _, must := range []string{
		`"op": "create_card"`, `"op": "append_card"`, `"op": "add_material_rel"`,
		`"op": "add_open_question"`, `"relation": "non_core_supplement"`,
		`"解释与依据"`, `"条件与边界"`, `"理解自检"`,
	} {
		if !strings.Contains(joined, must) {
			t.Fatalf("两份样例合计缺 %s", must)
		}
	}
}

// SkillSamples 取 SKILL.md 里的 ChangePlan 样例；解析器落在 skill 包，
// 供 test/e2e 复用同一份实现（e2e 直接拿这两份样例跑 `eg apply`，保证「文档样例即用例」）。
func SkillSamples(t *testing.T) []string {
	t.Helper()
	return skill.Samples()
}

// TestSkillSpecCrossReferences：文档描述的命令 / 退出码集合与两份合同一致（防照抄早期草稿）。
func TestSkillSpecCrossReferences(t *testing.T) {
	doc := skillText(t)
	cli := readSpec(t, egCLISpec)
	for _, cmd := range []string{"init", "config get|set", "capture", "context", "apply", "search", "card show", "rel", "report --last"} {
		if !strings.Contains(doc, "`"+cmd+"`") {
			t.Fatalf("SKILL.md 未登记 S1 命令 %s", cmd)
		}
		if !strings.Contains(cli, cmd) {
			t.Fatalf("CLI 合同里找不到命令 %s：口径已漂移", cmd)
		}
	}
	if !strings.Contains(doc, "M1 未实现（S1 命令，M2 落地）") {
		t.Fatal("SKILL.md 缺 M1 占位措辞「M1 未实现（S1 命令，M2 落地）」")
	}
	// 被禁措辞按运行期拼接，避免本文件自身被 TestForbiddenWordingAbsent 判为命中。
	if banned := "S1 " + "未覆盖"; strings.Contains(doc, banned) {
		t.Fatalf("SKILL.md 不得把 M1 占位写成 %q", banned)
	}
	// #dead 清单在技术方案里仍然存在（只读校验，防单侧漂移）。
	if !strings.Contains(readSpec(t, techDesignSpec), `id="dead"`) {
		t.Fatal("技术方案 #dead 锚点缺失：废弃项清单来源不可核对")
	}
}
