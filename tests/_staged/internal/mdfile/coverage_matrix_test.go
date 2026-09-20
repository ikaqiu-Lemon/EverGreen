package mdfile

// coverage_matrix_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-4
// 「覆盖矩阵确定性渲染 + 窄 parser round-trip」的**先红**判据
// （Schema v2 契约 §4.2.3 提炼覆盖 / §5.1 Note 渲染）。
//
// 覆盖矩阵是「提取结果」里紧随 Knowledge / Opinion 之后的一张表：行顺序 == extraction_coverage
// 数组序，source_refs / outputs 顺序 == 输入序。机器锚点使用**独立版本化协议 eg:nc:1**（不复用
// T12-3 的 eg:nr:1）：可见矩阵是给人看的、机器锚点是真源，二者必须能确定性重渲染逐字一致。
//
// 本文件锁死：
//   - render→parse 精确逆、render→parse→render 字节稳定；
//   - 行 / 来源 / 产出顺序原样保留（不排序 / 不去重 / 不重排）；
//   - summary / reason / module 里的 Markdown 特殊字符与 CR/LF 做**确定性单行转义**，不破表；
//     机器锚点保留原始字节，parser 逐字读回；
//   - 三种 disposition 的可见处置渲染：outputs 反引号 ID、note_only「Note-only：…」、missing「缺漏：…」；
//   - writer/parser fail closed：空矩阵、未知 disposition、outputs/note_only/missing 的形态违规、
//     未知锚点版本、非法 base64、未知字段、重复 JSON key、缺锚点、重复锚点、可见表头 / 单元格被篡改、
//     矩阵后多余可见内容——一律 error，绝不静默猜。

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

// 锚点协议字面量（与 internal/mdfile 的 eg:nc:1 协议一致；测试侧独立持有一份，
// 一旦协议前缀漂移，golden / 篡改用例会当场变红）。
const (
	ncOpenLit  = "<!-- eg:nc:1 "
	ncCloseLit = " -->"
)

func rc(module string, refs []string, summary, disp string, outputs []string, reason string) ReviewCoverage {
	return ReviewCoverage{Module: module, SourceRefs: refs, Summary: summary,
		Disposition: disp, Outputs: outputs, Reason: reason}
}

// ncLegal 是一组合法覆盖：一条 outputs（多 ref / 多 output）、一条 note_only。
func ncLegal() []ReviewCoverage {
	return []ReviewCoverage{
		rc("材料方法", []string{"L1-L2", "L3-L4"}, "方法三步", "outputs", []string{"k-a", "o-b"}, ""),
		rc("延伸讨论", []string{"L5-L6"}, "仅记录", "note_only", nil, "暂不产出卡"),
	}
}

func eqStrs(a, b []string) bool {
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

func eqCov(a, b ReviewCoverage) bool {
	return a.Module == b.Module && a.Summary == b.Summary &&
		a.Disposition == b.Disposition && a.Reason == b.Reason &&
		eqStrs(a.SourceRefs, b.SourceRefs) && eqStrs(a.Outputs, b.Outputs)
}

// ncAnchorLine 用给定 JSON 载荷手工造一条 eg:nc:1 锚点行（解码路径的 fail-closed 用例专用）。
func ncAnchorLine(payloadJSON string) string {
	return ncOpenLit + base64.RawURLEncoding.EncodeToString([]byte(payloadJSON)) + ncCloseLit
}

// —— round-trip ——

func TestCoverageMatrixRoundTripLegal(t *testing.T) {
	in := ncLegal()
	body, err := RenderCoverageMatrix(in)
	if err != nil {
		t.Fatalf("渲染合法覆盖矩阵出错：%v", err)
	}
	got, err := ParseCoverageMatrix(body)
	if err != nil {
		t.Fatalf("解析覆盖矩阵出错：%v\n%s", err, body)
	}
	if len(got) != len(in) {
		t.Fatalf("读回条目数 %d ≠ 输入 %d", len(got), len(in))
	}
	for i := range in {
		if !eqCov(got[i], in[i]) {
			t.Fatalf("第 %d 条读回不一致：\n got=%+v\nwant=%+v", i, got[i], in[i])
		}
	}
}

func TestCoverageMatrixRenderParseRenderStable(t *testing.T) {
	body1, err := RenderCoverageMatrix(ncLegal())
	if err != nil {
		t.Fatalf("首次渲染出错：%v", err)
	}
	covs, err := ParseCoverageMatrix(body1)
	if err != nil {
		t.Fatalf("解析出错：%v", err)
	}
	body2, err := RenderCoverageMatrix(covs)
	if err != nil {
		t.Fatalf("二次渲染出错：%v", err)
	}
	if !bytes.Equal(body1, body2) {
		t.Fatalf("render→parse→render 字节不稳定：\n--- 1 ---\n%s\n--- 2 ---\n%s", body1, body2)
	}
}

// —— 可见形态 golden（钉死表头 / 分隔行 / 行渲染，不含机器锚点部分）——

func TestCoverageMatrixVisibleGolden(t *testing.T) {
	body, err := RenderCoverageMatrix(ncLegal())
	if err != nil {
		t.Fatalf("渲染出错：%v", err)
	}
	want := "### 覆盖矩阵\n\n" +
		"| 模块 | 来源范围 | 语义模块 | 处置 |\n" +
		"| --- | --- | --- | --- |\n" +
		"| `材料方法` | `L1-L2` `L3-L4` | 方法三步 | `k-a` `o-b` |\n" +
		"| `延伸讨论` | `L5-L6` | 仅记录 | Note-only：暂不产出卡 |\n\n"
	first := bytes.Index(body, []byte(ncOpenLit))
	if first < 0 {
		t.Fatalf("渲染结果缺机器锚点：\n%s", body)
	}
	if got := string(body[:first]); got != want {
		t.Fatalf("可见矩阵形态与 golden 不一致：\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestCoverageMatrixMissingDispositionRenders(t *testing.T) {
	// missing 的**纯渲染**能力：显示「缺漏：<reason>」（plan 层另行 E2 阻止 apply，与渲染无关）。
	in := []ReviewCoverage{rc("待补", []string{"L1-L1"}, "还没提炼出产出", "missing", nil, "尚缺产出卡")}
	body, err := RenderCoverageMatrix(in)
	if err != nil {
		t.Fatalf("渲染 missing 覆盖出错：%v", err)
	}
	if !strings.Contains(string(body), "| 缺漏：尚缺产出卡 |") {
		t.Fatalf("missing 处置未渲染为「缺漏：…」：\n%s", body)
	}
	got, err := ParseCoverageMatrix(body)
	if err != nil {
		t.Fatalf("解析 missing 覆盖出错：%v", err)
	}
	if len(got) != 1 || !eqCov(got[0], in[0]) {
		t.Fatalf("missing 覆盖读回不一致：%+v", got)
	}
}

func TestCoverageMatrixOrderPreserved(t *testing.T) {
	// 模块按输入序（这里刻意给字典序倒置），parser 原样保留、不排序。
	in := []ReviewCoverage{
		rc("乙模块", []string{"L3-L4"}, "后段", "outputs", []string{"o-b"}, ""),
		rc("甲模块", []string{"L1-L2"}, "前段", "outputs", []string{"k-a"}, ""),
	}
	body, err := RenderCoverageMatrix(in)
	if err != nil {
		t.Fatalf("渲染出错：%v", err)
	}
	got, err := ParseCoverageMatrix(body)
	if err != nil {
		t.Fatalf("解析出错：%v", err)
	}
	if len(got) != 2 || got[0].Module != "乙模块" || got[1].Module != "甲模块" {
		t.Fatalf("模块顺序被重排：%+v", got)
	}
}

// —— 特殊字符：单行转义不破表，锚点逐字读回 ——

func TestCoverageMatrixSpecialCharsSingleLine(t *testing.T) {
	const summary = "含|管道\\反斜杠\r\n换行"
	const reason = "理由也带|与\\和\n多行"
	in := []ReviewCoverage{
		rc("材料方法", []string{"L1-L2"}, summary, "outputs", []string{"k-a"}, ""),
		rc("延伸讨论", []string{"L3-L4"}, "普通", "note_only", nil, reason),
	}
	body, err := RenderCoverageMatrix(in)
	if err != nil {
		t.Fatalf("渲染出错：%v", err)
	}
	// 可见部分不得出现裸 CR/LF 破坏单元格（除了行尾 \n 与锚点前的空行）：每条数据行必须是单行。
	first := bytes.Index(body, []byte(ncOpenLit))
	visible := body[:first]
	for _, line := range bytes.Split(visible, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("| `")) && bytes.Contains(line, []byte("\r")) {
			t.Fatalf("数据行含裸 CR，会破坏单行单元格：%q", line)
		}
	}
	// 锚点是真源：解析后 summary / reason 必须是**原始字节**逐字还原。
	got, err := ParseCoverageMatrix(body)
	if err != nil {
		t.Fatalf("解析出错：%v\n%s", err, body)
	}
	if got[0].Summary != summary {
		t.Fatalf("summary 未逐字还原：\n got=%q\nwant=%q", got[0].Summary, summary)
	}
	if got[1].Reason != reason {
		t.Fatalf("reason 未逐字还原：\n got=%q\nwant=%q", got[1].Reason, reason)
	}
	// render→parse→render 仍稳定。
	body2, err := RenderCoverageMatrix(got)
	if err != nil {
		t.Fatalf("二次渲染出错：%v", err)
	}
	if !bytes.Equal(body, body2) {
		t.Fatalf("特殊字符下 render→parse→render 不稳定")
	}
}

// —— writer fail closed ——

func TestCoverageRenderRejectsEmpty(t *testing.T) {
	if _, err := RenderCoverageMatrix(nil); err == nil {
		t.Fatal("空覆盖矩阵应拒绝渲染")
	}
}

func TestCoverageRenderRejectsEmptyModule(t *testing.T) {
	in := []ReviewCoverage{rc("  ", []string{"L1-L1"}, "x", "note_only", nil, "y")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("module 为空应拒绝渲染")
	}
}

func TestCoverageRenderRejectsUnknownDisposition(t *testing.T) {
	in := []ReviewCoverage{rc("m", []string{"L1-L1"}, "x", "archived", nil, "y")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("未知 disposition 应拒绝渲染")
	}
}

func TestCoverageRenderRejectsOutputsWithReason(t *testing.T) {
	in := []ReviewCoverage{rc("m", []string{"L1-L1"}, "x", "outputs", []string{"k-a"}, "多余理由")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("outputs 处置带 reason 应拒绝渲染")
	}
}

func TestCoverageRenderRejectsNoteOnlyWithoutReason(t *testing.T) {
	in := []ReviewCoverage{rc("m", []string{"L1-L1"}, "x", "note_only", nil, "  ")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("note_only 处置缺 reason 应拒绝渲染")
	}
}

func TestCoverageRenderRejectsOutputsWithoutOutputs(t *testing.T) {
	in := []ReviewCoverage{rc("m", []string{"L1-L1"}, "x", "outputs", nil, "")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("outputs 处置缺 outputs 应拒绝渲染")
	}
}

// —— parser fail closed（篡改可见部分）——

func ncBase(t *testing.T) []byte {
	t.Helper()
	body, err := RenderCoverageMatrix(ncLegal())
	if err != nil {
		t.Fatalf("构造基线矩阵出错：%v", err)
	}
	return body
}

func TestCoverageParseHeaderTampered(t *testing.T) {
	body := bytes.Replace(ncBase(t), []byte("| 模块 | 来源范围 | 语义模块 | 处置 |"),
		[]byte("| 模块 | 来源 | 语义模块 | 处置 |"), 1)
	if _, err := ParseCoverageMatrix(body); err == nil {
		t.Fatal("表头被篡改应 fail closed")
	}
}

func TestCoverageParseCellTampered(t *testing.T) {
	body := bytes.Replace(ncBase(t), []byte("方法三步"), []byte("方法四步"), 1)
	if _, err := ParseCoverageMatrix(body); err == nil {
		t.Fatal("可见单元格被篡改应 fail closed")
	}
}

func TestCoverageParseExtraTrailingContent(t *testing.T) {
	body := append(ncBase(t), []byte("多出来的一行\n")...)
	if _, err := ParseCoverageMatrix(body); err == nil {
		t.Fatal("矩阵后多余可见内容应 fail closed")
	}
}

func TestCoverageParseNoAnchorRejected(t *testing.T) {
	base := ncBase(t)
	first := bytes.Index(base, []byte(ncOpenLit))
	visibleOnly := base[:first] // 去掉全部机器锚点
	if _, err := ParseCoverageMatrix(visibleOnly); err == nil {
		t.Fatal("缺机器锚点应 fail closed")
	}
}

func TestCoverageParseMissingOneAnchor(t *testing.T) {
	base := ncBase(t)
	lines := bytes.Split(base, []byte("\n"))
	var kept [][]byte
	dropped := false
	for _, ln := range lines {
		if !dropped && bytes.HasPrefix(bytes.TrimSpace(ln), []byte(ncOpenLit)) {
			dropped = true // 删掉第一条锚点
			continue
		}
		kept = append(kept, ln)
	}
	if _, err := ParseCoverageMatrix(bytes.Join(kept, []byte("\n"))); err == nil {
		t.Fatal("缺一条锚点应 fail closed")
	}
}

func TestCoverageParseDuplicateAnchor(t *testing.T) {
	base := ncBase(t)
	lines := bytes.Split(base, []byte("\n"))
	var firstAnchor []byte
	for _, ln := range lines {
		if bytes.HasPrefix(bytes.TrimSpace(ln), []byte(ncOpenLit)) {
			firstAnchor = ln
			break
		}
	}
	if firstAnchor == nil {
		t.Fatal("基线里没有锚点，用例前提不成立")
	}
	dup := append(append([]byte{}, base...), append([]byte("\n"), firstAnchor...)...)
	if _, err := ParseCoverageMatrix(dup); err == nil {
		t.Fatal("重复锚点应 fail closed")
	}
}

// —— parser fail closed（篡改机器锚点载荷）——

func TestCoverageParseUnknownVersion(t *testing.T) {
	body := bytes.Replace(ncBase(t), []byte("<!-- eg:nc:1 "), []byte("<!-- eg:nc:2 "), 1)
	if _, err := ParseCoverageMatrix(body); err == nil {
		t.Fatal("未知锚点版本应 fail closed")
	}
}

func TestCoverageParseBadBase64(t *testing.T) {
	line := ncOpenLit + "@@@not-base64@@@" + ncCloseLit
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("非法 base64 载荷应 fail closed")
	}
}

func TestCoverageParseUnknownField(t *testing.T) {
	line := ncAnchorLine(`{"m":"x","s":["L1-L1"],"g":"y","d":"outputs","o":["k-a"],"z":1}`)
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("载荷含未知字段应 fail closed")
	}
}

func TestCoverageParseDuplicateKey(t *testing.T) {
	line := ncAnchorLine(`{"m":"x","m":"y","s":["L1-L1"],"g":"z","d":"note_only","r":"w"}`)
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("载荷含重复 JSON key 应 fail closed")
	}
}

func TestCoverageParseUnknownDisposition(t *testing.T) {
	line := ncAnchorLine(`{"m":"x","s":["L1-L1"],"g":"y","d":"weird"}`)
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("载荷 disposition 越界应 fail closed")
	}
}

func TestCoverageParseFormViolationInAnchor(t *testing.T) {
	// outputs 处置但载荷缺 outputs → 形态违规，解码即 fail closed。
	line := ncAnchorLine(`{"m":"x","s":["L1-L1"],"g":"y","d":"outputs"}`)
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("outputs 处置缺 outputs 的锚点应 fail closed")
	}
}

// ============================================================================
// T12-4 独立审查补漏（§4.2.3 / §5.1）：
//   - module 也走**统一安全 code-span 渲染**：同时含 | \ ` CR/LF 的 module 必须单行不破表、
//     代码跨度自洽，锚点仍逐字读回；
//   - writer/parser 对 module 原始字节重复 fail closed；同 ref / 同 output 重复仍合法；
//   - validateReviewCoverage 补 summary / source_refs 每项 / outputs 每项 TrimSpace 非空；
//   - parser 对「第二个顶层 JSON 值 / 尾随 token / 非对象顶层」显式 fail closed（不靠肉眼判断实现）。
// ============================================================================

// unescapedPipesInRow 数一行里**未被反斜杠转义**的裸管道（GFM 表格按裸管道切单元格）。
func unescapedPipesInRow(line string) int {
	n := 0
	for i := 0; i < len(line); i++ {
		if line[i] != '|' {
			continue
		}
		bs := 0
		for j := i - 1; j >= 0 && line[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 { // 前面反斜杠为偶数（含 0）→ 该管道未被转义。
			n++
		}
	}
	return n
}

// splitRowCells 按**未转义**管道切一行表格，去掉首尾空壳，返回单元格原文（含两侧空格）。
func splitRowCells(row string) []string {
	var cells []string
	var cur []byte
	for i := 0; i < len(row); i++ {
		if row[i] == '|' {
			bs := 0
			for j := i - 1; j >= 0 && row[j] == '\\'; j-- {
				bs++
			}
			if bs%2 == 0 {
				cells = append(cells, string(cur))
				cur = cur[:0]
				continue
			}
		}
		cur = append(cur, row[i])
	}
	cells = append(cells, string(cur))
	if len(cells) >= 2 {
		cells = cells[1 : len(cells)-1] // 去掉行首 `|` 前、行尾 `|` 后的空壳。
	}
	return cells
}

// wellFormedCodeSpan 判断一段（已 TrimSpace）文本是否是自洽的行内 code span：
// 以等长反引号围栏开头 / 结尾，且内部不含与围栏等长的反引号串（否则会提前闭合围栏）。
func wellFormedCodeSpan(cell string) bool {
	i := 0
	for i < len(cell) && cell[i] == '`' {
		i++
	}
	if i == 0 {
		return false // 必须以反引号围栏开头。
	}
	fence := cell[:i]
	j := len(cell)
	for j > 0 && cell[j-1] == '`' {
		j--
	}
	if len(cell)-j != len(fence) {
		return false // 首尾围栏长度必须一致。
	}
	return !strings.Contains(cell[i:j], fence)
}

// firstDataRow 从可见部分取第一条数据行（以 "| " 开头且非表头 / 分隔行）。
func firstDataRow(t *testing.T, body []byte) string {
	t.Helper()
	first := bytes.Index(body, []byte(ncOpenLit))
	if first < 0 {
		t.Fatalf("渲染结果缺机器锚点：\n%s", body)
	}
	for _, ln := range strings.Split(string(body[:first]), "\n") {
		if strings.HasPrefix(ln, "| ") && ln != coverageHeaderRowLit && ln != coverageSepRowLit {
			return ln
		}
	}
	t.Fatalf("没找到可见数据行：\n%s", body[:first])
	return ""
}

// 表头 / 分隔行字面量（测试侧独立持有一份，一旦渲染漂移当场变红）。
const (
	coverageHeaderRowLit = "| 模块 | 来源范围 | 语义模块 | 处置 |"
	coverageSepRowLit    = "| --- | --- | --- | --- |"
)

// —— 缺陷 1：module 统一安全 code-span 渲染 ——

func TestCoverageMatrixSpecialModuleSingleLineAndCodeSpan(t *testing.T) {
	const module = "含|管道`反引号\\反斜杠\r\n换行" // 同时含 | ` \ CR LF
	in := []ReviewCoverage{rc(module, []string{"L1-L2"}, "概述", "note_only", nil, "仅记录")}
	body, err := RenderCoverageMatrix(in)
	if err != nil {
		t.Fatalf("渲染含特殊字符 module 出错：%v", err)
	}
	// 锚点是真源：module 必须逐字还原。
	got, err := ParseCoverageMatrix(body)
	if err != nil {
		t.Fatalf("解析出错：%v\n%s", err, body)
	}
	if len(got) != 1 || got[0].Module != module {
		t.Fatalf("module 未逐字还原：\n got=%q\nwant=%q", got[0].Module, module)
	}
	row := firstDataRow(t, body)
	// 单行：数据行内不得含裸 CR / LF（换行必须被转义），否则一行被拆成多行破表。
	if strings.ContainsAny(row, "\r\n") {
		t.Fatalf("数据行含裸 CR/LF，破坏单行：%q", row)
	}
	// 恰 4 个单元格：module 里的 | 必须被转义，未转义管道恰 5 个。
	if p := unescapedPipesInRow(row); p != 5 {
		t.Fatalf("数据行未转义管道数=%d（应为 5 即 4 个单元格），module 的 | 未安全转义：%q", p, row)
	}
	cells := splitRowCells(row)
	if len(cells) != 4 {
		t.Fatalf("单元格数=%d（应 4）：%q", len(cells), row)
	}
	// module 单元格是自洽 code span：module 里的反引号不得提前闭合围栏。
	if !wellFormedCodeSpan(strings.TrimSpace(cells[0])) {
		t.Fatalf("module 单元格不是自洽 code span：%q", cells[0])
	}
	// render→parse→render 稳定。
	body2, err := RenderCoverageMatrix(got)
	if err != nil {
		t.Fatalf("二次渲染出错：%v", err)
	}
	if !bytes.Equal(body, body2) {
		t.Fatal("特殊 module 下 render→parse→render 不稳定")
	}
}

func TestCoverageMatrixSpecialRefAndOutputSingleLine(t *testing.T) {
	// source_refs / outputs 也共用安全 code-span helper：即便塞入会破表的字节，
	// 公共渲染器也不得吐出裸管道 / 拆行 / 破坏代码跨度的行（锚点仍逐字读回）。
	const ref = "L1|L2`x"
	const out = "k-a`|b"
	in := []ReviewCoverage{rc("模块", []string{ref}, "概述", "outputs", []string{out}, "")}
	body, err := RenderCoverageMatrix(in)
	if err != nil {
		t.Fatalf("渲染出错：%v", err)
	}
	row := firstDataRow(t, body)
	if strings.ContainsAny(row, "\r\n") {
		t.Fatalf("数据行含裸 CR/LF：%q", row)
	}
	if p := unescapedPipesInRow(row); p != 5 {
		t.Fatalf("ref/output 的 | 未安全转义，未转义管道数=%d（应 5）：%q", p, row)
	}
	cells := splitRowCells(row)
	if len(cells) != 4 {
		t.Fatalf("单元格数=%d（应 4）：%q", len(cells), row)
	}
	if !wellFormedCodeSpan(strings.TrimSpace(cells[1])) {
		t.Fatalf("source_refs 单元格不是自洽 code span：%q", cells[1])
	}
	if !wellFormedCodeSpan(strings.TrimSpace(cells[3])) {
		t.Fatalf("outputs 单元格不是自洽 code span：%q", cells[3])
	}
	got, err := ParseCoverageMatrix(body)
	if err != nil {
		t.Fatalf("解析出错：%v\n%s", err, body)
	}
	if len(got) != 1 || !eqStrs(got[0].SourceRefs, []string{ref}) || !eqStrs(got[0].Outputs, []string{out}) {
		t.Fatalf("ref/output 未逐字还原：%+v", got[0])
	}
}

func TestCoverageMatrixNormalContentGoldenUnchanged(t *testing.T) {
	// 负控：普通内容（无特殊字节）经统一 helper 渲染仍是**单反引号**包裹，与既有 golden 一字不差。
	body, err := RenderCoverageMatrix(ncLegal())
	if err != nil {
		t.Fatalf("渲染出错：%v", err)
	}
	want := "### 覆盖矩阵\n\n" +
		"| 模块 | 来源范围 | 语义模块 | 处置 |\n" +
		"| --- | --- | --- | --- |\n" +
		"| `材料方法` | `L1-L2` `L3-L4` | 方法三步 | `k-a` `o-b` |\n" +
		"| `延伸讨论` | `L5-L6` | 仅记录 | Note-only：暂不产出卡 |\n\n"
	first := bytes.Index(body, []byte(ncOpenLit))
	if got := string(body[:first]); got != want {
		t.Fatalf("统一 helper 改变了普通内容的可见形态：\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// —— 缺陷 2：writer / parser 对 module 原始字节重复 fail closed；同 ref/output 重复合法 ——

func TestCoverageRenderRejectsDuplicateModule(t *testing.T) {
	in := []ReviewCoverage{
		rc("同名", []string{"L1-L2"}, "s1", "note_only", nil, "r1"),
		rc("同名", []string{"L3-L4"}, "s2", "note_only", nil, "r2"),
	}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("module 原始字节重复应拒绝渲染（store 直调也不得落下违反 §4.2.3 的矩阵）")
	}
}

func TestCoverageParseRejectsDuplicateModule(t *testing.T) {
	// 用内部渲染件手工拼一份**字节自洽**（可见 ↔ 锚点一致）但 module 重复的矩阵：
	// parse 必须因重渲染时的 module 唯一性校验 fail closed，而不是靠字节不一致侥幸拒。
	a := rc("同名", []string{"L1-L2"}, "s1", "note_only", nil, "r1")
	b := rc("同名", []string{"L3-L4"}, "s2", "note_only", nil, "r2")
	var sb strings.Builder
	sb.WriteString(coverageHeading + "\n\n")
	sb.WriteString(coverageHeaderRow + "\n")
	sb.WriteString(coverageSepRow + "\n")
	sb.WriteString(coverageVisibleRow(a) + "\n")
	sb.WriteString(coverageVisibleRow(b) + "\n\n")
	sb.WriteString(encodeCoverageAnchor(a) + "\n")
	sb.WriteString(encodeCoverageAnchor(b) + "\n")
	if _, err := ParseCoverageMatrix([]byte(sb.String())); err == nil {
		t.Fatal("重复 module 的矩阵应 fail closed（parser 侧）")
	}
}

func TestCoverageRenderAllowsDuplicateRefsAndOutputs(t *testing.T) {
	in := []ReviewCoverage{
		rc("材料方法", []string{"L1-L2", "L1-L2"}, "重复来源合法", "outputs", []string{"k-a", "k-a"}, ""),
	}
	body, err := RenderCoverageMatrix(in)
	if err != nil {
		t.Fatalf("同 ref / 同 output 重复应合法，却被拒：%v", err)
	}
	got, err := ParseCoverageMatrix(body)
	if err != nil {
		t.Fatalf("解析出错：%v", err)
	}
	if len(got) != 1 || !eqStrs(got[0].SourceRefs, []string{"L1-L2", "L1-L2"}) ||
		!eqStrs(got[0].Outputs, []string{"k-a", "k-a"}) {
		t.Fatalf("重复 ref / output 未逐字保留：%+v", got[0])
	}
}

// —— 缺陷 2：validateReviewCoverage 补 summary / source_refs 每项 / outputs 每项非空 ——

func TestCoverageRenderRejectsEmptySummary(t *testing.T) {
	in := []ReviewCoverage{rc("m", []string{"L1-L1"}, "  ", "note_only", nil, "r")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("summary 空白应拒绝渲染")
	}
}

func TestCoverageRenderRejectsEmptySourceRefItem(t *testing.T) {
	in := []ReviewCoverage{rc("m", []string{"L1-L1", "  "}, "x", "note_only", nil, "r")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("source_refs 含空项应拒绝渲染")
	}
}

func TestCoverageRenderRejectsEmptyOutputItem(t *testing.T) {
	in := []ReviewCoverage{rc("m", []string{"L1-L1"}, "x", "outputs", []string{"k-a", "  "}, "")}
	if _, err := RenderCoverageMatrix(in); err == nil {
		t.Fatal("outputs 含空项应拒绝渲染")
	}
}

// —— 缺陷 3：parser 对第二顶层值 / 尾随 token / 非对象顶层 fail closed ——

func TestCoverageParseSecondTopLevelValue(t *testing.T) {
	// 两个顶层对象拼接：第二个顶层值必须被拒（不能只解第一个就放过）。
	line := ncAnchorLine(`{"m":"x","s":["L1-L1"],"g":"y","d":"note_only","r":"z"}` +
		`{"m":"y","s":["L1-L1"],"g":"y","d":"note_only","r":"z"}`)
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("载荷含第二个顶层 JSON 值应 fail closed")
	}
}

func TestCoverageParseTrailingToken(t *testing.T) {
	// 合法对象后尾随非空白 token。
	line := ncAnchorLine(`{"m":"x","s":["L1-L1"],"g":"y","d":"note_only","r":"z"}42`)
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("载荷尾随多余 token 应 fail closed")
	}
}

func TestCoverageParseNonObjectTopLevel(t *testing.T) {
	// 顶层不是对象（数组）应被拒。
	line := ncAnchorLine(`["m","s"]`)
	if _, err := ParseCoverageMatrix([]byte(line + "\n")); err == nil {
		t.Fatal("载荷顶层非对象（数组）应 fail closed")
	}
}
