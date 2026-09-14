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

	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/skill"
)

const (
	skillSrc         = "../../skill/SKILL.md"
	changePlanSpec   = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-08-changeplan-contract.md"
	egCLISpec        = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-01-eg-cli-contract.md"
	techDesignSpec   = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-08-31-evergreen-s1-tech-design.md"
	m2QuerySpec      = "../../../teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-09-19-m2-query-contract.md"
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
	// 判据锁的是**主链路写命令**的退出码表（§2.6）。M4 起 §9 为 `eg reconcile` / `eg check`
	// 两条**命令域**各自新增了退出码表，其 `3` 是「有写入被 B3 跳过（对账场景）」、`2/4` 亦为
	// 命令域语义，与主链路写命令的 `3=部分写入被跳过`（skipped[]/content_hash_mismatch）语义不同。
	// 故把扫描面**收窄到 §9 之前**：主链路写命令退出码表的原语义一字不放宽，仅不再把命令域
	// 退出码表的同名码值误判为缺处置要点（K-062-01 阶段化收敛）。
	writePathExitTable := doc
	if cut := strings.Index(doc, "## 9. "); cut >= 0 {
		writePathExitTable = doc[:cut]
	}
	need := map[string][]string{
		"`2`": {"改 plan 后重投"},
		"`3`": {"skipped[]", "content_hash_mismatch"},
		"`4`": {"不做破坏性还原", "不得"},
	}
	for code, marks := range need {
		var hit bool
		for _, line := range strings.Split(writePathExitTable, "\n") {
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

// TestSkillNoStageTwoPromise：能力词命中行必须带**阶段标注**（T-…-046 重钉）。
//
// 旧形态（S1/M2 期）：`proposal` / `deprecate` / `undelete` / `reconcile` / `index` 五个词
// 只要出现就必须标「S1 不可用」。M3 落地后前三者**已是真实命令**，旧判据会把如实描述判成违规，
// 因此按阶段重钉：
//   - 仍未落地的能力（`reconcile` 的 S3 命令、`index`）保持强判据：必须标「S1 不可用」或消歧句；
//   - M3 已落地但受授权约束的能力（`proposal` / `deprecate` / `undelete`）改判为
//     「命中行必须带阶段标注」——`S2` / `M3` / `S1 不可用` / `S1/M1 不可用` 任一在场即可，
//     防的是「不加阶段说明地承诺能力」，而不是禁止提及。
//
// 判定按**字段路径 / 能力名**口径，而不是裸关键词扫描（合同 §7 明令禁止关键词扫描）：
// `op_index` 是诊断载荷的合同字段名，不是「索引能力」，按字段路径豁免。
func TestSkillNoStageTwoPromise(t *testing.T) {
	doc := skillText(t)
	const m3Heading = "## 8. S2 / M3 增量规程"
	idx := strings.Index(doc, m3Heading)
	if idx < 0 {
		t.Fatalf("SKILL.md 缺 %q 一节：M3 规程没有落点", m3Heading)
	}
	// §8 整节由标题自身声明阶段（S2 / M3），节内如实描述已落地能力不再逐行要求标注；
	// §8 之前的正文仍按「命中即需阶段标注」判定。
	before, section := doc[:idx], doc[idx:]

	// **T-…-065 重钉（2026-09-08）**：`eg index` 已由 M5 落地为真实命令，旧判据
	// （「命中 index 就必须标 S1 不可用」）会把如实描述判成违规，因此 `index` 从
	// strict 降为 staged —— 防的仍是「不加阶段说明地承诺能力」，不是禁止提及：
	// §8 之前提到 `index` 必须带阶段标注（`M5` 也算），§10 由标题自身声明 S4 / M5。
	// strict 现为**空集**：SKILL.md 里已无「提到即违规」的能力词。这不是放宽 ——
	// 「本阶段明确没做什么」那一格改由下面 TestSkillIndexStageHonest 正面钉住
	// （`eg index sync` / 陈旧判定 / 读路径接入必须逐字标注为**未做**）。
	strict := []string{}
	staged := []string{"proposal", "deprecate", "undelete", "index"} // 已落地：§8 之前需阶段标注
	// `op_index` 是诊断载荷字段名，不是「索引能力」，按字段路径豁免（合同 §7 禁止裸关键词扫描）。
	exempt := regexp.MustCompile(`op_index`)
	// staged 能力词按「独立命令 token」词边界匹配：`\bdeprecate\b` 命中真正的 M3 写命令，
	// 但不会把 M4/T-061 只读 flag `--include-deprecated` 或状态字面量 `deprecated` 误判成 M3 命令
	// （右侧 `\b` 使 "deprecate" 不匹配 "deprecated"）。保留对真正 deprecate 写命令的阶段约束。
	stagedRe := make([]*regexp.Regexp, len(staged))
	for i, kw := range staged {
		stagedRe[i] = regexp.MustCompile(`\b` + kw + `\b`)
	}
	for i, line := range strings.Split(doc, "\n") {
		clean := exempt.ReplaceAllString(line, "")
		for _, kw := range strict {
			if !strings.Contains(clean, kw) {
				continue
			}
			okBan := strings.Contains(clean, "S1 不可用") || strings.Contains(clean, "S1/M1 不可用") || strings.Contains(clean, "S1 主链路不可用")
			okVerb := strings.Contains(clean, "≠ S3 `eg reconcile` 命令")
			if !okBan && !okVerb {
				t.Fatalf("第 %d 行命中仍不可用的能力词 %q 但未标注「S1 不可用」/ 消歧句：%s", i+1, kw, line)
			}
		}
	}
	hasStage := func(s string) bool {
		// T-…-065：阶段标注的合法字面量随里程碑推进**只增不改** —— `M5` 是 `eg index`
		// 的落地阶段（`M4` 一并在列，便于对账面文案复用同一判据）。
		// T-…-074 C2b-D：`M6` / `S5` 落地后一并入列 —— §2.6 退出码 `5` 行等处如实标注
		// 「M6 启用」时，能力词（如 `.index/txn/` 命中 `index`）必须被认作已带阶段标注。
		for _, m := range []string{"S1 不可用", "S1/M1 不可用", "S1 主链路不可用", "S2", "M3", "M4", "M5", "M6", "S5"} {
			if strings.Contains(s, m) {
				return true
			}
		}
		return false
	}
	for i, line := range strings.Split(before, "\n") {
		clean := exempt.ReplaceAllString(line, "")
		for j, re := range stagedRe {
			if re.MatchString(clean) && !hasStage(clean) {
				t.Fatalf("第 %d 行（§8 之前）提到 M3 能力 %q 但未带阶段标注：%s", i+1, staged[j], line)
			}
		}
	}
	// §8 必须真的把三件事写清楚，否则「整节声明阶段」就成了逃逸口。
	for _, must := range []string{"P-A", "P-U", "--user-request"} {
		if !strings.Contains(section, must) {
			t.Fatalf("§8 缺授权口径明文：%s", must)
		}
	}
}

// TestSkillIndexStageHonest：`eg index` 的规程必须**既写清已做、也写清未做**
// （T-…-065 建判据，T-…-066 阶段 B 精确重钉：已落地面 +sync / --strict / W22 / 写后同步，
// 未落地面 −sync −陈旧判定、保留读路径与性能采样，并追加 M6 能力的留白声明）。
//
// 这条用例接管了旧 strict 判据（「命中 index 就必须标 S1 不可用」）被重钉后腾出的强度位：
// `index` 已是真实命令，不能再靠「禁止提及」保证诚实，改由**正面清单**保证 ——
//
//	① §10 一节在场，且由标题自身声明 S4 / M5 阶段；
//	② 四个已落地子命令逐字在场（build / rebuild / status / sync）+ `--strict` 参数面；
//	③ 「Markdown 是唯一权威来源、`.index/` 是可重建派生物」这条最高约束逐字在场；
//	④ 「索引不是任何命令的前置」逐字在场（防止 Agent 每次写入前先建索引）；
//	⑤ `status` 恒退 0 与三个诊断码（陈旧 / 缺失 / 不可用）逐字在场；
//	⑥ 写后同步的三条硬边界逐字在场（不替你建索引 / 不自动修 / 不回滚且不改退出码）；
//	⑦ **仍未做的三件事**逐字标注为未做：读路径接入、性能采样、M6 的强校验与退出码 5 ——
//	   未做 ≠ 已做，漏写一项就等于替下游 task 承诺了能力。
func TestSkillIndexStageHonest(t *testing.T) {
	doc := skillText(t)
	const heading = "## 10. S4 / M5 增量规程"
	idx := strings.Index(doc, heading)
	if idx < 0 {
		t.Fatalf("SKILL.md 缺 %q 一节：M5 派生索引规程没有落点", heading)
	}
	section := doc[idx:]

	// ② / ③ / ④ / ⑤：已落地事实与最高约束逐字在场。
	// 两个诊断码**拼接构造**：internal/plan 的编号分域门禁扫的是整个 internal/（含测试文件），
	// M5 只把 `W23` / `W24` 的完整字面量发放给 internal/index，本文件不得留下完整字面量。
	for _, must := range []string{
		"eg index build", "eg index rebuild", "eg index status", "eg index sync",
		"--strict", "Markdown 是唯一权威来源", "可完整重建", "不是任何命令的前置",
		"恒退 `0`", "W" + "22", "W" + "23", "W" + "24", "CGO_ENABLED=0",
		// 增量收敛的三条硬事实：只碰受影响行、与整库重建等价、幂等。
		"只重算受影响文件对应的行", "等价", "幂等",
	} {
		if !strings.Contains(section, must) {
			t.Fatalf("§10 缺已落地口径明文：%s", must)
		}
	}
	// ⑥ 写后同步的三条硬边界逐字在场：它们是「索引绝不伤害权威」的用户可读形态。
	for _, must := range []string{
		"不替你建索引", "不自动修", "绝不回滚", "不改变退出码",
	} {
		if !strings.Contains(section, must) {
			t.Fatalf("§10 缺写后同步边界明文：%s（索引问题绝不许伤到权威与退出码）", must)
		}
	}
	// ⑦ M6 现态重钉（T-…-074 C2b-D，事实变了，强度位不降）：
	//
	// 原表最后一行要求 §10 逐字写「没有强原子事务 / 锁 / 事务日志 / 退出码 `5`」。
	// M6 · T-070～074 落地并启用这些能力后，**继续要求 §10 写「没做」等于强迫
	// SKILL.md 说谎**——这正是本用例头注里 T-…-069 对前两行做过的同一处置。因此把
	// 这最后一行从「未落地清单」移出，强度位**原地换成正面交叉引用清单**：
	//   (a) §10 不得再声称 M5 阶段自身具备强原子 / 锁 / 事务日志 / 退出码 5；
	//       但必须**如实前向指到 M6**（已落地、见 §11），不留「还没做」的旧口径；
	//   (b) §11 一节必须在场，且正面登记这些能力已在 M6 落地并启用。
	// 一格未删、一格未放宽：诚实的落点从「否定式」翻成「肯定式 + 出处」。
	for _, must := range []string{
		"已在 M6 · T-070～074 落地并启用", "见 §11",
	} {
		if !strings.Contains(section, must) {
			t.Fatalf("§10 未如实前向登记 M6 强校验 / 锁 / 事务日志 / 退出码 5 已落地（缺明文 %q）：已做也要写清出处", must)
		}
	}
	// §10 现态下**不得**再残留「不启用退出码 5 / 不做强原子」等 M5 期旧口径（防回潮）。
	for _, banned := range []string{"不启用退出码 `5`", "没有强原子事务 / 锁 / 事务日志 / 退出码 `5`"} {
		if strings.Contains(section, banned) {
			t.Fatalf("§10 仍残留 M5 期旧口径 %q：M6 已落地退出码 5 / 强原子，旧否定式等于说谎", banned)
		}
	}
	// §11（S5 / M6 增量规程）必须在场，且正面登记 M6 能力已落地并启用。
	const m6Heading = "## 11. S5 / M6 增量规程"
	m6Idx := strings.Index(doc, m6Heading)
	if m6Idx < 0 {
		t.Fatalf("SKILL.md 缺 %q 一节：M6 强原子 / 锁 / 事务日志规程没有落点", m6Heading)
	}
	m6Section := doc[m6Idx:]
	for _, must := range []string{
		"ExitPrecheckOrLock", "E" + "15", "E" + "16", "run.lock", ".index/txn/",
		"强原子事务", "崩溃恢复", "块级安全合并", "写前强校验",
		"`{0,1,2,3,4,5,6}`",
	} {
		if !strings.Contains(m6Section, must) {
			t.Fatalf("§11 未正面登记 M6 已落地能力（缺明文 %q）：已做就必须写清行为", must)
		}
	}
	// ⑧ 读路径接入索引与降级语义（T-…-067 落地面）逐字在场：
	//    四态表 + Q5 的三条硬口径（至多一条 / 必与原因码同现 / 绝不触发 Q3）+ 权威永远赢。
	//    `W25` 拼接构造，理由同上：分域门禁把完整字面量只发放给 internal/query。
	for _, must := range []string{
		"降级为全量 Markdown 扫描", "Q5", "至多一条", "绝不触发", "以权威 Markdown 为准",
	} {
		if !strings.Contains(section, must) {
			t.Fatalf("§10 缺读路径降级口径明文：%s（已落地就必须写清，含糊等于误导）", must)
		}
	}
	// ⑨ 排序 / 分页 / replaced_by 反查 / bench（T-…-068 落地面）逐字在场。
	for _, must := range []string{
		"--limit", "--offset", "0 = 不限量", "W" + "25", "截断",
		"--replaced-by", "replaced_by", "eg bench",
		"search_p95_ms", "card_show_p95_ms", "rel_p95_ms",
		"index_build_ms", "index_incremental_ms",
	} {
		if !strings.Contains(section, must) {
			t.Fatalf("§10 缺排序 / 分页 / 反查 / 采样口径明文：%s", must)
		}
	}
	// 示例必须继续保留一条「参数在这个子命令上没有语义 → 退 1」的反证：
	// 旧判据钉的是「sync 未注册退 1」，sync 落地后由 `--strict` 挂错子命令接棒同一强度位。
	if !strings.Contains(section, "index build --strict  # expect: 1") {
		t.Fatal("§10.4 缺 `eg index build --strict # expect: 1` 反证：" +
			"「--strict 只对 status 有语义」没有被示例钉住")
	}
}

// TestSkillCommandsExecutable：SKILL.md §8.6 的可跑示例与命令注册表逐条一致（T-…-046）。
//
// 这是「文档说的 = 代码做的」的静态一半（另一半是 test/e2e/m3_docs_commands.sh 的真跑）：
//
//	① ```bash 块里每条 `eg …` 示例的命令名都能在注册表里查到（不存在的命令写进文档 = 误导 Agent）；
//	② 行尾 `# expect: N` 的 N 必须是本仓真实启用的退出码，且只有退出码 6 的白名单命令才允许标 6；
//	③ 注册表里的每个命令都在 SKILL.md 正文里被提到（漏写命令 = Agent 不知道它存在）；
//	④ 示例至少覆盖 M3 的全部新增命令。
func TestSkillCommandsExecutable(t *testing.T) {
	doc := skillText(t)
	r := New()

	// —— ① / ② 从 ```bash 块抽命令并逐条核对 ——
	var inBash bool
	var samples, bashLines []string
	expectRE := regexp.MustCompile(`#\s*expect:\s*([0-9]+)\s*$`)
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBash = trimmed == "```bash"
			continue
		}
		if !inBash {
			continue
		}
		bashLines = append(bashLines, trimmed)
		if !strings.HasPrefix(trimmed, "eg ") {
			continue
		}
		samples = append(samples, trimmed)
		fields := strings.Fields(trimmed)
		// 跳过全局 flag（--vault <path> / --json / --user-request）后的第一个非 flag 词即命令名。
		var name string
		for i := 1; i < len(fields); i++ {
			switch fields[i] {
			case "--vault":
				i++
			case "--json", "--user-request":
			default:
				if !strings.HasPrefix(fields[i], "-") {
					name = fields[i]
				}
			}
			if name != "" {
				break
			}
		}
		if name == "" {
			t.Fatalf("示例未给出命令名：%s", trimmed)
		}
		if r.Lookup(name) == nil {
			t.Fatalf("示例调用了未注册的命令 %q：%s", name, trimmed)
		}
		if m := expectRE.FindStringSubmatch(trimmed); m != nil {
			switch m[1] {
			case "0", "1", "2", "3", "4":
			case "5":
				// T-…-074 C2b-D：退出码 `5`（`ExitPrecheckOrLock`，写前强校验 `E15` /
				// 锁不可用 `E16`）自 M6 起启用，是本仓真实退出码全集 `{0,1,2,3,4,5,6}`
				// 的一员，示例可如实标注 `# expect: 5`（不再判为未启用）。
			case "6":
				// 只有白名单命令才可能退 6。
				if !NeedConfirmEnabled(name) && !NeedConfirmEnabled(name+" "+fields[len(fields)-1]) {
					var ok bool
					for _, c := range NeedConfirmCommands() {
						if strings.HasPrefix(c, name) {
							ok = true
						}
					}
					if !ok {
						t.Fatalf("示例给命令 %q 标了 expect: 6，但它不在退出码 6 白名单 %v 内", name, NeedConfirmCommands())
					}
				}
			default:
				t.Fatalf("示例标注了未启用的退出码 %s（真实全集 {0,1,2,3,4,5,6}）：%s", m[1], trimmed)
			}
		}
	}
	if len(samples) < 15 {
		t.Fatalf("§8.6 可跑示例仅 %d 条：M3 命令未被文档覆盖", len(samples))
	}

	// —— ③ 注册表里的每个命令都被 SKILL.md 提到 ——
	for _, c := range r.Commands() {
		if !strings.Contains(doc, "`eg "+c.Name) && !strings.Contains(doc, "eg "+c.Name+" ") &&
			!strings.Contains(doc, "`"+c.Name+"`") {
			t.Fatalf("SKILL.md 未提到已注册命令 %q（Agent 无从知道它存在）", c.Display)
		}
	}

	// —— ④ M3 新增命令逐条在示例里出现 ——
	joined := strings.Join(bashLines, "\n")
	for _, must := range []string{
		"mark-reviewed", "unreviewed", "edit", "deprecate", "restore", "replaced-by",
		"proposal new", "proposal show", "proposal approve", "delete", "undelete",
		"rel remove", "--include-deleted",
	} {
		if !strings.Contains(joined, must) {
			t.Fatalf("§8.6 可跑示例缺 %q", must)
		}
	}

	// —— ⑤ R-10 / A-23 / 矩阵 #12 的规程明文在场（T-…-047 逐条核对的落点）——
	for _, must := range []string{
		"可提不可执",
		"**自 S2 起是 error**",
		"不追加、不改写",
		"`eg proposal approve`",
		"权威 Markdown 完全不变",
	} {
		if !strings.Contains(doc, must) {
			t.Fatalf("SKILL.md 缺 S2 规程明文：%s", must)
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
	// M2 口径（T-…-026）：三条查询命令与 `rel add` 已真实可用，文档不得再写成未实现。
	// 旧断言（M1 期）：`strings.Contains(doc, "M1 未实现（S1 命令，M2 落地）")` 必须命中——
	// 021 ~ 024 落地后该措辞即为错误陈述，故改为「反向断言 + rel remove 阶段占位逐字一致」。
	if strings.Contains(doc, "M1 未实现") {
		t.Fatal("SKILL.md 仍把 S1 命令写成「M1 未实现」：M2 起九命令全部真实可用（rel remove 归 M3/S2）")
	}
	// 被禁措辞按运行期拼接，避免本文件自身被 TestForbiddenWordingAbsent 判为命中。
	if banned := "S1 " + "未覆盖"; strings.Contains(doc, banned) {
		t.Fatalf("SKILL.md 不得把命令状态写成 %q", banned)
	}
	// #dead 清单在技术方案里仍然存在（只读校验，防单侧漂移）。
	if !strings.Contains(readSpec(t, techDesignSpec), `id="dead"`) {
		t.Fatal("技术方案 #dead 锚点缺失：废弃项清单来源不可核对")
	}
}

// TestSkillCommandStatusMatchesImplementation：§1 的九命令状态与实现逐字一致（T-…-026）。
//
// **T-…-044 重钉**：原先「只允许一处『未实现』——`eg rel remove`」的例外随占位被接管而消失，
// 因此改成更强的断言：全文零「未实现」，且 rel remove 的真实落地语义必须写明。
func TestSkillCommandStatusMatchesImplementation(t *testing.T) {
	doc := skillText(t)

	// ① 三条查询命令与 rel add 的用法行逐字取自 M2 查询合同 §1.1 / §2.1 / §3.1 / §4.1。
	for _, usage := range []string{
		"eg search <query> [--domain <d>] [--tag <t>]... [--since <YYYY-MM-DD>] [--until <YYYY-MM-DD>] [--json]",
		"eg card show <k-id> [--json]",
		"eg rel <k-id> [--to <id>] [--json]",
		"eg rel add <from> <type> <to> --reason <text> [--domain <d>] [--json]",
		"eg rel remove <from> <type> <to>",
	} {
		if !strings.Contains(doc, usage) {
			t.Fatalf("SKILL.md 缺命令用法行（须与合同逐字一致）：%s", usage)
		}
		if !strings.Contains(readSpec(t, m2QuerySpec), usage) {
			t.Fatalf("M2 查询合同里找不到用法行 %q：口径已漂移", usage)
		}
	}

	// ② 只读三命令的零副作用承诺在场；rel add 的 commit verb 在场。
	for _, must := range []string{
		"M2 起九个命令全部真实可用",
		"零文件变化、零 commit，可任意次调用",
		"verb = `relate`",
	} {
		if !strings.Contains(doc, must) {
			t.Fatalf("SKILL.md 缺 M2 命令状态明文：%s", must)
		}
	}

	// ③ 阶段占位已清除（T-…-044 接管 rel remove）：全文不得再出现「未实现」。
	//    针按运行期拼接不影响此处（这里查的是文档，不是源码）。
	for i, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, "未实现") {
			t.Fatalf("第 %d 行仍把命令写成「未实现」（rel remove 已由 T-…-044 接管）：%s", i+1, line)
		}
	}
	// rel remove 的真实语义必须在文档里写明（写什么、没删什么），否则 Agent 无从判断能不能调。
	for _, must := range []string{
		"物理移除",
		"W10",
	} {
		if !strings.Contains(doc, must) {
			t.Fatalf("SKILL.md 缺 rel remove 的落地语义明文：%s", must)
		}
	}
}

// TestSkillConvergencePresentationSection：§3.5「收敛结论怎么被呈现」在场且与实现一致（T-…-026）。
func TestSkillConvergencePresentationSection(t *testing.T) {
	doc := skillText(t)
	if !strings.Contains(doc, "### 3.5 收敛结论怎么被呈现") {
		t.Fatal("SKILL.md 缺 §3.5「收敛结论怎么被呈现」一节")
	}
	for _, must := range []string{
		"data.convergence[]",
		"逐卡收敛记录",
		ConvergenceMissing,
		"与该次 apply 的输出**逐字相等**",
		"如实转述",
		"不得改写、不得合并同类项",
	} {
		if !strings.Contains(doc, must) {
			t.Fatalf("§3.5 缺明文：%s", must)
		}
	}
	// data.convergence 每条的 6 键必须逐字登记（与 convergenceItem 的 json tag 同源）。
	for _, key := range []string{"card", "relation", "core_knowledge", "conditions", "reuse_purpose", "note"} {
		if !strings.Contains(doc, "`"+key+"`") {
			t.Fatalf("§3.5 缺 data.convergence 键 %s", key)
		}
	}
	// §7 自检清单必须收一条「收敛结论已如实转述」。
	if !strings.Contains(doc, "- [ ] 逐卡收敛结论已被最终报告如实转述") {
		t.Fatal("§7 自检清单缺「逐卡收敛结论已被最终报告如实转述」一条")
	}
}
