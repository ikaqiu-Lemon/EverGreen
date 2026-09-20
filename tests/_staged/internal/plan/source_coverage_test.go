package plan

// source_coverage_test.go —— T-evergreen.knowledge_opinion_split-158614-012 · T12-2A
// 「来源范围（source_ref）+ omissions + Source 快照」的**先红**判据
// （Schema v2 契约 §4.2 第 4/5 条 / §4.2.1）。
//
// 本批**只**加严 `plan_version: 2` 且 `blocks[]` 给出（BlocksGiven）的 write_note：
//   - 每个 role: source 块必须给出**非空**、格式严格的 source_ref；
//   - v2 blocks 必须显式给出 omissions[]（无删除也要传 []）；
//   - blocks[].source_ref 与 omissions[].source_ref 各自按 Source 顺序严格递增、不重叠，
//     两组之间也不得重复 / 交叉；两组区间的并集必须让 Source 的**每个非空正文行恰覆盖一次**，
//     空白行可不覆盖，只覆盖空白行的范围仍占用区间、参与重叠判定；
//   - 行号相对 frontmatter 之后的正文物理行（闭区间，从 1 起）；末尾换行不制造虚假尾行；
//     CRLF 的 \r 不算内容；越界 / 逆序 / 空洞 / 重复 / 交叉 / 缺 reason 一律**字段级 E2**，不新增码；
//   - Source 快照：既有 Source 经 resolve + Env.Read 读取一次；同一 plan 中先 add_source、
//     后 write_note 使用该 add_source 的 op.Body，但按它**落盘后**再取回的 SourceBody 布局
//     （前导空白物理行 + writer 补尾换行）计行号，而不是去读尚未落盘的 Env、也不是直接用 op.Body 原样。
//   - W21 仍是 warning，--strict 下不升级。
//
// 本文件只裁定来源覆盖，**不**做：结构资产扫描（图片 / 代码块 / 表格 …，属 T12-2B）、annotation /
// label 语义与渲染（T12-3）、机器锚点，以及 extraction_coverage 的语义校验 / 渲染（那是
// note_coverage.go 与 mdfile 覆盖矩阵协议的职责，T12-4 §4.2.3）。因此这里既不要求 role: agent 给
// annotation，也不比较 source 块正文是否逐字等于 Source 对应行；下面回填的最小合法 extraction_coverage
// 仅为让这些既有 T12-2A 判据在 v2 blocks 必填覆盖矩阵的前提下仍能构造合法 op，不代表本文件负责其语义。

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// covRefRe / covCardRe 从块 / 产出卡 JSON 里抽出 source_ref 与 card ID（供覆盖矩阵回填复用）。
var (
	covRefRe  = regexp.MustCompile(`"source_ref"\s*:\s*"([^"]*)"`)
	covCardRe = regexp.MustCompile(`"card"\s*:\s*"([^"]*)"`)
)

// covUniqMatches 按出现序抽取正则第一捕获组、去重（不排序、不改写字节）。
func covUniqMatches(re *regexp.Regexp, s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		if v := m[1]; !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// covMatrixFor 依据 blocks 声明的 source_ref 与（可选）output_cards 造一份**语义合法**的
// extraction_coverage 片段（形如 `,"extraction_coverage":[...]`，可直接拼进 op）。
//
// 契约 §4.2 第 6 条 / §4.2.3：plan_version:2 且 blocks[] 的 write_note 必须显式给出非空覆盖矩阵。
// 这些既有 T12-2A/2B/3 判据只考来源覆盖 / 保真 / 批注，本身不预设覆盖矩阵内容，故此处回填一份
// 恰好合法的最小矩阵：无产出卡 → 单个 note_only 模块覆盖全部 ref（outputs 空、reason 非空）；
// 有产出卡 → 单个 outputs 模块覆盖全部 ref 且引用全部卡（与 output_cards 成员双向一致）。
// 无任何 source_ref 时返回空串——那类 op 会在更早的来源 / 块校验处失败，走不到覆盖闸门。
func covMatrixFor(blocksJoined string, cards []string) string {
	refs := covUniqMatches(covRefRe, blocksJoined)
	if len(refs) == 0 {
		return ""
	}
	var cardIDs []string
	for _, c := range cards {
		cardIDs = append(cardIDs, covUniqMatches(covCardRe, c)...)
	}
	var mod string
	if len(cardIDs) == 0 {
		mod = ncMod("整段整理", refs, "覆盖全部来源范围，仅整理归纳", "note_only", nil, "整段仅忠实整理，无独立复用产物。")
	} else {
		mod = ncMod("整段整理", refs, "覆盖全部来源范围并产出卡片", "outputs", cardIDs, "")
	}
	return `,"extraction_coverage":[` + mod + `]`
}

// —— 夹具与小工具 ——

const covSrcID = "s-20260901-cov"

// covSource 造一份 Source 文件：body 逐字落在 frontmatter 之后（不含前导空行，
// 由调用方自行决定正文的物理行布局）。
func covSource(id, body string) string {
	return "---\nid: " + id + "\nurl: https://example.com/x\ntitle: 覆盖夹具\n" +
		"saved_at: '2026-09-01T10:00:00+08:00'\n---\n" + body
}

func covSourceFile(body string) map[string]string {
	return map[string]string{"sources/" + covSrcID + ".md": covSource(covSrcID, body)}
}

// covSB 造一个带 source_ref 的 source 块（本批不涉及 heading / annotation）。
func covSB(ref, body string) string {
	return fmt.Sprintf(`{"role":"source","source_ref":%q,"body":%q}`, ref, body)
}

// covSBNoRef 造一个**缺 source_ref** 的 source 块。
func covSBNoRef(body string) string {
	return fmt.Sprintf(`{"role":"source","body":%q}`, body)
}

// covAB 造一个 agent 块（本批不要求 annotation）。
func covAB(body string) string {
	return fmt.Sprintf(`{"role":"agent","body":%q}`, body)
}

// covOm 造一条 omission（source_ref + reason）。
func covOm(ref, reason string) string {
	return fmt.Sprintf(`{"source_ref":%q,"reason":%q}`, ref, reason)
}

// covNote 造一条 v2 write_note op。withOm=false 表示**根本不给 omissions 字段**
// （用来考「v2 blocks 必须显式给 omissions」）。
func covNote(noteID string, blocks []string, omissions []string, withOm bool) string {
	joined := strings.Join(blocks, ",")
	op := `{"op":"write_note","source":"` + covSrcID + `","note_id":"` + noteID +
		`","blocks":[` + joined + `]`
	if withOm {
		op += `,"omissions":[` + strings.Join(omissions, ",") + `]`
	}
	op += covMatrixFor(joined, nil)
	return op + `}`
}

// covValidate 用既有 Source（body 决定物理行布局）跑一次 v2 校验。
func covValidate(t *testing.T, body, ops string) *Result {
	t.Helper()
	files := covSourceFile(body)
	return run(t, vault(t, files), v2Plan(t, files, ops))
}

// covInline 跑一份「同一 plan 先 add_source、后 write_note」的 v2 校验：
// write_note 必须用 add_source 的 op.Body 精确字节做覆盖校验（Env 里此刻还没有该文件）。
func covInline(t *testing.T, addBody string, blocks []string, omissions []string, withOm bool) *Result {
	t.Helper()
	files := map[string]string{}
	add := `{"op":"add_source","source_id":"s-20260901-inline","url":"https://example.com/i",` +
		`"title":"内联原文","saved_at":"2026-09-01T10:00:00+08:00","body":"` + addBody + `"}`
	joined := strings.Join(blocks, ",")
	note := `{"op":"write_note","source":"s-20260901-inline","note_id":"n-20261017-inl","blocks":[` +
		joined + `]`
	if withOm {
		note += `,"omissions":[` + strings.Join(omissions, ",") + `]`
	}
	note += covMatrixFor(joined, nil)
	note += `}`
	return run(t, vault(t, files), v2Plan(t, files, add+","+note))
}

// covV1 跑一份 plan_version: 1 的 sections{} write_note（回归：兼容路径不得被新校验波及）。
func covV1(t *testing.T, body, ops string) *Result {
	t.Helper()
	files := covSourceFile(body)
	plan := fmt.Sprintf(`{"plan_version":%d,"verb":"process","domain":"ai-infra",`+
		`"reason":"v1 回归","requirement_ids":["EG-KNW-04"],"base":{},"ops":[%s]}`,
		PlanVersionV1, ops)
	return run(t, vault(t, files), plan)
}

// requireErrorAt 断言存在一条 code@path 的 error（钉死字段级路径，而不是只断言 Failed）。
func requireErrorAt(t *testing.T, res *Result, code, path string) Diagnostic {
	t.Helper()
	if !res.Failed() {
		t.Fatalf("期望 %s@%s，实际零 error（warnings=%v）", code, path, codes(res.Warnings))
	}
	for _, d := range res.Errors {
		if d.Code == code && d.Path == path {
			return d
		}
	}
	t.Fatalf("期望 %s@%s，实际 errors=%v", code, path, diagPaths(res.Errors))
	return Diagnostic{}
}

// requireNoError 断言合法用例零 error。
func requireNoError(t *testing.T, res *Result) {
	t.Helper()
	if res.Failed() {
		t.Fatalf("合法用例不应产出 error，实得 %v", diagPaths(res.Errors))
	}
}

// 常用正文：四个非空正文行（无空白行），L1..L4。
const covBody4 = "甲行\n乙行\n丙行\n丁行\n"

// —— 合法基线 ——

// TestSourceRefExistingSourceLegal —— 既有 Source：两个 source 块 + 空 omissions 覆盖全部非空行。
func TestSourceRefExistingSourceLegal(t *testing.T) {
	res := covValidate(t, covBody4, covNote("n-20261017-ok",
		[]string{covSB("L1-L2", "抄前两行。"), covSB("L3-L4", "抄后两行。")},
		nil, true))
	requireNoError(t, res)
	if len(res.Actions) == 0 {
		t.Fatal("合法 v2 write_note 应展开出写入 action")
	}
}

// TestSourceRefSamePlanAddSourceLegal —— 同一 plan 先 add_source、后 write_note：
// 覆盖校验用 add_source 的 op.Body，但按**落盘后**布局计行号 —— body `甲行\n乙行\n` 落盘后
// SourceBody 为「(空行)\n甲行\n乙行\n」：L1 空白可不覆盖，甲/乙 分别是 L2 / L3。
func TestSourceRefSamePlanAddSourceLegal(t *testing.T) {
	res := covInline(t, "甲行\\n乙行\\n",
		[]string{covSB("L2-L2", "抄第一行。"), covSB("L3-L3", "抄第二行。")}, nil, true)
	requireNoError(t, res)
}

// TestSourceRefSamePlanAddSourceHole —— 同 plan add_source，但留下非空行空洞 → E2。
// 证明快照真的取自 op.Body 的落盘后布局（否则读不到正文就无从判空洞）：只覆盖 L2、漏掉非空的 L3。
func TestSourceRefSamePlanAddSourceHole(t *testing.T) {
	res := covInline(t, "甲行\\n乙行\\n",
		[]string{covSB("L2-L2", "只抄第一行。")}, nil, true)
	requireErrorAt(t, res, E2, "ops[1].blocks")
}

// TestSamePlanSnapshotMatchesPersistedBody —— 同-plan add_source 的快照必须与它**落盘后**
// 再取回的 SourceBody 行号 / 字节布局一致（§4.2.1）：否则「当次算 L1、落盘后重处理算 L2」漂移。
//
// 三段一链，外加一条字节级等价断言：
//
//	① 同一 plan 先 add_source、后 write_note（source_ref 按落盘后布局 L2 / L3 给），当次 Validate 通过；
//	② Execute 真正把该 add_source 落盘成一份 Source 文件；
//	③ 用**相同**的 source_ref 对已落盘 Source 起一条新 write_note，仍然通过；
//	④ 直接断言 store.PersistedSourceBody(op.Body) 与 store.SourceBody(落盘文件) 字节等价 ——
//	   把「快照布局」与「真实落盘布局」钉死在同一份字节上，任一侧漂移都当场变红。
func TestSamePlanSnapshotMatchesPersistedBody(t *testing.T) {
	const sid = "s-20260901-inline"
	// 相同的 L2/L3 refs 在两种正文尾态下都应通过：钉死 appendSourceBody 的两条分支
	// （body 以 \n 收尾 → 不补；不以 \n 收尾 → 补一个行尾 \n），二者落盘后布局同为
	// "\n甲行\n乙行\n"（L1 空白 / L2 甲 / L3 乙），故同一 refs 通用。
	cases := []struct {
		name    string // 子用例名
		escaped string // 内联 plan JSON 里的 add_source.body（已转义）
		body    string // 真实 ApplySource 用的原始正文字节
	}{
		{"body已有尾换行", "甲行\\n乙行\\n", "甲行\n乙行\n"},
		{"body无尾换行", "甲行\\n乙行", "甲行\n乙行"},
	}
	blocks := []string{covSB("L2-L2", "抄甲。"), covSB("L3-L3", "抄乙。")}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// ① 同 plan：add_source + write_note，当次按落盘后布局 L2/L3 给，通过。
			requireNoError(t, covInline(t, tc.escaped, blocks, nil, true))

			// ② 落盘 add_source，读回真实 Source 字节。
			dir := t.TempDir()
			st := store.New(dir)
			if _, err := st.ApplySource(store.SourceSpec{ID: sid, URL: "https://example.com/i",
				Title: "内联原文", Stamp: mustStamp(t), Body: []byte(tc.body)}); err != nil {
				t.Fatalf("落盘 add_source 失败：%v", err)
			}
			rel := store.SourceRel(sid)
			raw, err := st.Read(rel)
			if err != nil {
				t.Fatalf("读回落盘原文失败：%v", err)
			}
			persisted, err := store.SourceBody(raw.Bytes)
			if err != nil {
				t.Fatalf("解析落盘正文失败：%v", err)
			}

			// ④ 字节级等价：同-plan 快照布局 == 真实落盘 SourceBody（用 bytes.Equal，不经 string）。
			if snap := store.PersistedSourceBody([]byte(tc.body)); !bytes.Equal(snap, persisted) {
				t.Fatalf("同-plan 快照与落盘 SourceBody 字节不等价：\n快照=%q\n落盘=%q", snap, persisted)
			}

			// ③ 用相同 source_ref 对已落盘 Source 起新 write_note，仍通过。
			files := map[string]string{rel: string(raw.Bytes)}
			resPersisted := run(t, vault(t, files), v2Plan(t, files,
				`{"op":"write_note","source":"`+sid+`","note_id":"n-20261017-persist","blocks":[`+
					strings.Join(blocks, ",")+`],"omissions":[]`+covMatrixFor(strings.Join(blocks, ","), nil)+`}`))
			requireNoError(t, resPersisted)
		})
	}
}

// —— Source 快照：读取语义与「一次读取、缓存复用」——

// TestSourceUnreadableExistingIsE2 —— 既有 Source 在索引里、但 Env.Read 读失败：
// v2 blocks 的覆盖校验无从进行，判 E2@ops[0].source 且 write_note 零展开。
func TestSourceUnreadableExistingIsE2(t *testing.T) {
	files := covSourceFile(covBody4)
	env := vault(t, files) // 索引由 frontmatter 建好，resolve 能定位
	rel := "sources/" + covSrcID + ".md"
	inner := env.Read
	env.Read = func(r string) ([]byte, error) {
		if r == rel {
			return nil, os.ErrNotExist // 模拟既有 Source 读失败（索引在、字节读不到）
		}
		return inner(r)
	}
	ops := covNote("n-20261017-unread", []string{covSB("L1-L4", "整段抄。")}, nil, true)
	res := run(t, env, v2Plan(t, files, ops))
	requireErrorAt(t, res, E2, "ops[0].source")
	if len(res.Actions) != 0 {
		t.Fatalf("取不到 Source 正文时 write_note 必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// TestSourceFrontmatterUnterminatedIsE2 —— 既有 Source 的 frontmatter 未闭合：
// store.SourceBody（mdfile.Parse）解析失败，判 E2@ops[0].source 且 write_note 零展开。
//
// 索引手工登记（vault 会因 frontmatter 不可解析而跳过该文件，那样只能考到「不存在于全库」，
// 考不到「取到了字节却解析不出正文」这条路径），Read 直接回一份缺闭合分隔行的字节。
func TestSourceFrontmatterUnterminatedIsE2(t *testing.T) {
	rel := "sources/" + covSrcID + ".md"
	raw := "---\nid: " + covSrcID + "\ntitle: 未闭合\n正文没有结束的三横线分隔行。\n"
	env := Env{
		Index:         store.Index{ByID: map[string]string{covSrcID: rel}},
		DefaultDomain: "ai-infra",
		Read: func(r string) ([]byte, error) {
			if r == rel {
				return []byte(raw), nil
			}
			return nil, os.ErrNotExist
		},
	}
	ops := covNote("n-20261017-unterm", []string{covSB("L1-L4", "整段抄。")}, nil, true)
	plan := fmt.Sprintf(`{"plan_version":%d,"verb":"process","domain":"ai-infra",`+
		`"reason":"未闭合 frontmatter","requirement_ids":["EG-KNW-04"],"base":{},"ops":[%s]}`,
		PlanVersion, ops)
	res := run(t, env, plan)
	requireErrorAt(t, res, E2, "ops[0].source")
	if len(res.Actions) != 0 {
		t.Fatalf("解析不出 Source 正文时 write_note 必须零展开，实得 %d 条 action", len(res.Actions))
	}
}

// TestSourceSnapshotReadOncePerValidate —— 同一份既有 Source 被两条合法 write_note 引用：
// resolve + Env.Read + SourceBody 只在首次读一次，第二条命中缓存 —— Env.Read 对该 Source
// 总计恰 1 次（契约 §4.2.1「一次读取、缓存复用」）。
func TestSourceSnapshotReadOncePerValidate(t *testing.T) {
	files := covSourceFile(covBody4)
	env := vault(t, files)
	rel := "sources/" + covSrcID + ".md"
	reads := 0
	inner := env.Read
	env.Read = func(r string) ([]byte, error) {
		if r == rel {
			reads++
		}
		return inner(r)
	}
	// 两条不同 note、引用同一既有 Source，各自覆盖全部非空行、omissions=[]。
	note1 := covNote("n-20261017-rc1", []string{covSB("L1-L4", "整段抄。")}, nil, true)
	note2 := covNote("n-20261017-rc2", []string{covSB("L1-L4", "再抄一遍。")}, nil, true)
	res := run(t, env, v2Plan(t, files, note1+","+note2))
	requireNoError(t, res)
	if reads != 1 {
		t.Fatalf("同一既有 Source 被两条 write_note 引用，Env.Read 应恰 1 次，实得 %d 次", reads)
	}
}

// —— omissions 必给 ——

// TestWriteNoteV2RequiresOmissions —— v2 blocks 不给 omissions 字段 → E2@ops[0].omissions。
func TestWriteNoteV2RequiresOmissions(t *testing.T) {
	res := covValidate(t, covBody4, covNote("n-20261017-noom",
		[]string{covSB("L1-L4", "整段抄。")}, nil, false))
	requireErrorAt(t, res, E2, "ops[0].omissions")
}

// —— source 块必给 ref ——

// TestSourceBlockRequiresSourceRef —— source 块缺 source_ref → E2@blocks[0].source_ref。
func TestSourceBlockRequiresSourceRef(t *testing.T) {
	res := covValidate(t, covBody4, covNote("n-20261017-noref",
		[]string{covSBNoRef("没有范围。")}, nil, true))
	requireErrorAt(t, res, E2, "ops[0].blocks[0].source_ref")
}

// —— 格式 / 逆序 / 越界 ——

// TestSourceRefFormatRangeErrors —— 单个 ref 的格式 / 逆序 / 越界都落到该 ref 的字段路径。
func TestSourceRefFormatRangeErrors(t *testing.T) {
	cases := []struct {
		name string
		ref  string
	}{
		{"格式非法-无L", "1-2"},
		{"格式非法-分隔", "L1toL2"},
		{"格式非法-前导零", "L01-L2"},
		{"逆序-start大于end", "L3-L2"},
		{"越界-end超过物理行数", "L1-L5"}, // covBody4 只有 4 行
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := covValidate(t, covBody4, covNote("n-20261017-fmt",
				[]string{covSB(c.ref, "抄。")}, nil, true))
			requireErrorAt(t, res, E2, "ops[0].blocks[0].source_ref")
		})
	}
}

// —— source refs 逆序 / 重叠 ——

// TestSourceRefsMonotoneOverlap —— source 块的 refs 必须按 Source 顺序严格递增、不重叠。
func TestSourceRefsMonotoneOverlap(t *testing.T) {
	cases := []struct {
		name   string
		blocks []string
	}{
		{"逆序", []string{covSB("L2-L2", "后。"), covSB("L1-L1", "前。")}},
		{"重叠", []string{covSB("L1-L2", "前。"), covSB("L2-L3", "后。")}},
		{"重复", []string{covSB("L1-L2", "前。"), covSB("L1-L2", "又一遍。")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := covValidate(t, covBody4, covNote("n-20261017-mono", c.blocks, nil, true))
			// 违规落在**后一个**区间的 source_ref 上（blocks[1]）。
			requireErrorAt(t, res, E2, "ops[0].blocks[1].source_ref")
		})
	}
}

// —— omissions 缺 ref / 空 reason / 逆序 / 重叠 ——

// TestOmissionErrors —— omissions 每项必须 source_ref + trim 后非空 reason，且组内严格递增不重叠。
func TestOmissionErrors(t *testing.T) {
	// 一个覆盖 L1 的 source 块，保证问题只出在 omissions 上。
	src := []string{covSB("L1-L1", "抄第一行。")}
	t.Run("缺ref", func(t *testing.T) {
		res := covValidate(t, covBody4, covNote("n-20261017-om1", src,
			[]string{`{"reason":"页脚噪声"}`}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[0].source_ref")
	})
	t.Run("空reason", func(t *testing.T) {
		res := covValidate(t, covBody4, covNote("n-20261017-om2", src,
			[]string{covOm("L2-L2", "   ")}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[0].reason")
	})
	t.Run("逆序", func(t *testing.T) {
		res := covValidate(t, covBody4, covNote("n-20261017-om3", src,
			[]string{covOm("L4-L4", "噪声甲"), covOm("L2-L2", "噪声乙")}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[1].source_ref")
	})
	t.Run("重叠", func(t *testing.T) {
		res := covValidate(t, covBody4, covNote("n-20261017-om4", src,
			[]string{covOm("L2-L3", "噪声甲"), covOm("L3-L4", "噪声乙")}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[1].source_ref")
	})
	// omission 的 source_ref 与 source 块同一套形态判定：格式 / 逆序（start>end）/ 越界
	// 都必须逐项钉到该 omission 的 source_ref 字段路径（而不是只报个 Failed）。
	t.Run("ref格式非法", func(t *testing.T) {
		res := covValidate(t, covBody4, covNote("n-20261017-om5", src,
			[]string{covOm("L1toL2", "页眉噪声")}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[0].source_ref")
	})
	t.Run("ref-start大于end", func(t *testing.T) {
		res := covValidate(t, covBody4, covNote("n-20261017-om6", src,
			[]string{covOm("L3-L2", "页脚噪声")}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[0].source_ref")
	})
	t.Run("ref越界", func(t *testing.T) {
		// covBody4 只有 4 行，L2-L5 越过物理尾行。
		res := covValidate(t, covBody4, covNote("n-20261017-om7", src,
			[]string{covOm("L2-L5", "越界噪声")}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[0].source_ref")
	})
}

// —— 跨两组重叠 ——

// TestCrossGroupOverlap —— source 区间与 omission 区间不得交叉 / 重复。
func TestCrossGroupOverlap(t *testing.T) {
	res := covValidate(t, covBody4, covNote("n-20261017-cross",
		[]string{covSB("L1-L2", "抄前两行。")},
		[]string{covOm("L2-L2", "与来源块交叉")}, true))
	// 交叉落在后出现（start 较大）的那个区间上 —— 此处是 omissions[0]。
	requireErrorAt(t, res, E2, "ops[0].omissions[0].source_ref")
}

// —— 非空行空洞 ——

// TestNonEmptyLineHole —— 并集未覆盖某个非空正文行 → E2@ops[0].blocks。
func TestNonEmptyLineHole(t *testing.T) {
	res := covValidate(t, covBody4, covNote("n-20261017-hole",
		[]string{covSB("L1-L1", "抄第一行。"), covSB("L2-L2", "抄第二行。")},
		nil, true)) // L3 / L4 非空却无人覆盖
	requireErrorAt(t, res, E2, "ops[0].blocks")
}

// —— 非法 UTF-8 非空行：不丢、不误判（确认性回归）——

// TestNonUTF8LineIsNonEmpty —— 含非法 UTF-8 字节的非空正文行：既不能被误判为空白而漏覆盖，
// 也不能在计数中丢失。
//
// 这是一条**确认性回归**，不是先红判据：bodyPhysicalLines 走 []byte 只是为守「正文只读字节」
// 纪律、少一次整段正文分配；换成旧的 strings.Split(string(body), "\n") + strings.TrimSpace
// 其实也会给出同样结果 —— Go 的 []byte→string→[]byte 往返并不篡改字节，非法 UTF-8 原样保留，
// bytes.TrimSpace 虽按 Unicode 空白定义裁边，但 \xff 之类非法字节并不是空白、不会被裁掉。因此本
// 用例在新旧两种实现下都绿，它守的是「非法字节行按普通非空行处理」这条口径不被将来某次改动破坏。
func TestNonUTF8LineIsNonEmpty(t *testing.T) {
	// L1 甲行 / L2 三个非法 UTF-8 字节（非空白）/ L3 丙行。
	body := "甲行\n\xff\xfe\xfd\n丙行\n"
	t.Run("漏覆盖该行即空洞", func(t *testing.T) {
		// 只覆盖 L1、L3、跳过 L2：若把非法字节行误判为空白就不会报，实际必须报非空行空洞。
		res := covValidate(t, body, covNote("n-20261017-u8a",
			[]string{covSB("L1-L1", "抄甲。"), covSB("L3-L3", "抄丙。")}, nil, true))
		requireErrorAt(t, res, E2, "ops[0].blocks")
	})
	t.Run("覆盖该行即合法", func(t *testing.T) {
		// L1-L3 连续覆盖三行（含非法字节行）：行数恰 3、无越界、无空洞。
		res := covValidate(t, body, covNote("n-20261017-u8b",
			[]string{covSB("L1-L3", "整段抄（含乱码行）。")}, nil, true))
		requireNoError(t, res)
	})
}

// —— 仅空白行未覆盖可通过 + 空白行范围仍参与重叠 ——

// TestBlankLineCoverage —— 空白行可不覆盖；但只覆盖空白行的范围仍占用区间、参与重叠判定。
func TestBlankLineCoverage(t *testing.T) {
	// body: L1 非空 / L2 空白 / L3 非空。
	const body = "甲行\n\n乙行\n"
	t.Run("空白行未覆盖-合法", func(t *testing.T) {
		res := covValidate(t, body, covNote("n-20261017-blank1",
			[]string{covSB("L1-L1", "抄甲。"), covSB("L3-L3", "抄乙。")}, nil, true))
		requireNoError(t, res)
	})
	t.Run("空白行范围参与重叠", func(t *testing.T) {
		// source L1-L2（含空白行 L2）与 omission L2-L2（空白行）交叉 → E2。
		res := covValidate(t, body, covNote("n-20261017-blank2",
			[]string{covSB("L1-L2", "抄甲+空行。"), covSB("L3-L3", "抄乙。")},
			[]string{covOm("L2-L2", "空行噪声")}, true))
		requireErrorAt(t, res, E2, "ops[0].omissions[0].source_ref")
	})
}

// —— CRLF + 末尾换行边界 ——

// TestCRLFAndTrailingNewlineBoundary —— CRLF 的 \r 不算内容；末尾换行不制造虚假尾行。
func TestCRLFAndTrailingNewlineBoundary(t *testing.T) {
	t.Run("CRLF末尾有换行-L1L2合法", func(t *testing.T) {
		res := covValidate(t, "行1\r\n行2\r\n", covNote("n-20261017-crlf1",
			[]string{covSB("L1-L2", "抄两行。")}, nil, true))
		requireNoError(t, res)
	})
	t.Run("CRLF无末尾换行-不制造尾行", func(t *testing.T) {
		// body 末尾无换行：物理行仍是 2 行（行1 / 行2），末尾不生 L3。
		res := covValidate(t, "行1\r\n行2", covNote("n-20261017-crlf2",
			[]string{covSB("L1-L2", "抄两行。")}, nil, true))
		requireNoError(t, res)
	})
	t.Run("越界-越过物理尾行", func(t *testing.T) {
		res := covValidate(t, "行1\r\n行2\r\n", covNote("n-20261017-crlf3",
			[]string{covSB("L1-L3", "多抄一行。")}, nil, true))
		requireErrorAt(t, res, E2, "ops[0].blocks[0].source_ref")
	})
	// 空白口径锁定：空白行 = bytes.TrimSpace(line) 为空。纯尾随 CR（含多个）整行被 TrimSpace
	// 裁空，属空白、可不覆盖；只要行里有真实文字，尾随 CR 不改变其「非空、必须覆盖」的性质。
	// body：L1「甲行」非空 / L2「\r\r」纯 CR 空白 / L3「乙行」非空。
	t.Run("纯CR行是空白可不覆盖", func(t *testing.T) {
		// 只覆盖 L1、L3，跳过纯 CR 的 L2：空白行可不覆盖 → 合法。
		res := covValidate(t, "甲行\n\r\r\n乙行\n", covNote("n-20261017-crlf4",
			[]string{covSB("L1-L1", "抄甲。"), covSB("L3-L3", "抄乙。")}, nil, true))
		requireNoError(t, res)
	})
	t.Run("含真实文字行必须覆盖", func(t *testing.T) {
		// 只覆盖 L1：纯 CR 的 L2 可不覆盖，但含真实文字的 L3「乙行」漏覆盖即空洞。
		res := covValidate(t, "甲行\n\r\r\n乙行\n", covNote("n-20261017-crlf5",
			[]string{covSB("L1-L1", "只抄甲。")}, nil, true))
		requireErrorAt(t, res, E2, "ops[0].blocks")
	})
}

// —— v1 / v2 sections 兼容路径不变 ——

// TestV1SectionsUnchanged —— plan_version: 1 的 sections{} write_note 不被新校验波及。
func TestV1SectionsUnchanged(t *testing.T) {
	res := covV1(t, covBody4,
		`{"op":"write_note","source":"`+covSrcID+`","note_id":"n-20261017-v1",
 "sections":{"材料提炼":"整理正文。\n"}}`)
	requireNoError(t, res)
	// 不得因新校验凭空冒出 source_ref / omissions 相关的 E2。
	for _, d := range res.Errors {
		if strings.Contains(d.Path, "source_ref") ||
			strings.HasSuffix(d.Path, ".omissions") || strings.HasPrefix(d.Path, "ops[0].blocks") {
			t.Fatalf("v1 兼容路径不应产出来源覆盖类 E2：%s@%s", d.Code, d.Path)
		}
	}
}

// TestV2SectionsCompatUnchanged —— plan_version: 2 但仍用 sections{}（非 blocks[]）：
// 走兼容映射，不触发 source_ref / omissions 校验。
func TestV2SectionsCompatUnchanged(t *testing.T) {
	files := covSourceFile(covBody4)
	res := run(t, vault(t, files), v2Plan(t, files,
		`{"op":"write_note","source":"`+covSrcID+`","note_id":"n-20261017-v2sec",
 "sections":{"材料提炼":"整理正文。\n"}}`))
	requireNoError(t, res)
	for _, d := range res.Errors {
		if strings.Contains(d.Path, "source_ref") || strings.HasSuffix(d.Path, ".omissions") {
			t.Fatalf("v2 sections 兼容路径不应产出来源覆盖类 E2：%s@%s", d.Code, d.Path)
		}
	}
}

// —— W21 仍是 warning ——

// covAnchorBody 造一份恰 6 个 H2、无空白行的正文（L1..L12，全非空）。
const covAnchorBody = "## 甲\n甲文\n## 乙\n乙文\n## 丙\n丙文\n## 丁\n丁文\n## 戊\n戊文\n## 己\n己文\n"

// TestW21StaysWarningWithCoverage —— 覆盖完整但 source 块数显著少于章节数时仍报 W21（warning），
// --strict 下不升级为 error、照常写入。
func TestW21StaysWarningWithCoverage(t *testing.T) {
	// 2 个 source 块覆盖 L1..L12 全部非空行；6 个 H2 → 阈值 ceil(6/2)=3 → 2<3 → W21。
	ops := covNote("n-20261017-w21",
		[]string{covSB("L1-L6", "抄前半。"), covSB("L7-L12", "抄后半。")}, nil, true)
	res := covValidate(t, covAnchorBody, ops)
	requireWarning(t, res, W21)
	if res.Failed() {
		t.Fatalf("W21 是 warning，不得拦截：errors=%v", diagPaths(res.Errors))
	}
	pc := Precheck(res, true)
	if pc.Failed {
		t.Fatalf("--strict 下 W21 不得把写拦下：升级清单 %v", codes(pc.Upgraded))
	}
	if _, ok := find(pc.Upgraded, W21); ok {
		t.Fatalf("W21 竟被 --strict 升级：%v", codes(pc.Upgraded))
	}
}
