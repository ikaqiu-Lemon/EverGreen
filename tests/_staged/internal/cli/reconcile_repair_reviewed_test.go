package cli

// R2 修复桥的机器判据（M4 · T-evergreen.s1_main_flow-158614-051）。
//
// 五组用例（前三个名字是 deliverables 逐字要求，供 M-004 完成判据复算）：
//   ① TestR2OnlyReviewedAtWritten           —— **只写一键**的 diff 级反证：知识卡与材料笔记
//      两类目标，写后**新增的 frontmatter 行恰一行** `reviewed_at`，其余每一个键的整行
//      与正文全文逐字节不变；同一次修复不碰第二个文件；
//   ② TestR2B3HashStaleSkipsWithoutWrite    —— B3 不豁免：对账观测到的 hash 已过期 →
//      进 `skipped[kind=file_changed]`、目标字节零变化，且对应 finding **仍在**（带注记）；
//   ③ TestR2WrittenKeySetClosed             —— 写入键集合封闭：修复前后 frontmatter 键集合
//      的差集恰 `{reviewed_at}`；op 名恰既有 `mark_reviewed`、`AllOpNames` 恰
//      「M3 期 16 + M4 新增 1（`set_stale`，属 R6）= 17」；
//   ④ TestR2RepairIdempotentZeroWrite       —— 幂等：补齐后再跑一次 → 零 op、零写入、零提交；
//   ⑤ TestR2RepairJoinsSingleReconcileCommit —— 恰一次 commit：R1 有外部编辑 + R2 有修复时，
//      整轮对账的 `git log` 相对之前**恰 +1**（不是 +2）。
//
// 全组只经**内存 ChangePlan → 计划层 → 落盘层**这一条链路（本桥自己不 import 落盘层，
// 判据见 task 的 grep），事实一律回读真实文件字节与 `git log`，不看实现自报。

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
)

// r2RepairAt 是本组用例的固定对账时刻（全程确定性，绝不取本机当前时间）。
const r2RepairAt = "2026-11-24T10:00:00+08:00"

// r2Root 构造带固定时钟的 Root（补写进 frontmatter 的时刻因此可逐字断言）。
func r2Root(t *testing.T, dir string) *Root {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return stampAt(t, r2RepairAt) }
	r.In = closedStdin{}
	return r
}

// r2EditFile 模拟**用户直接编辑**（不经 eg）：在正文末尾追加一行，只写盘不提交。
func r2EditFile(t *testing.T, dir, rel string) {
	t.Helper()
	path := absIn(dir, rel)
	raw := string(mustRead(t, path))
	if err := os.WriteFile(path, []byte(raw+"\n用户在编辑器里手写的一行（外部编辑）。\n"), 0o644); err != nil {
		t.Fatalf("模拟外部编辑失败：%v", err)
	}
}

// r2Scan 采一次真实的只读事实：全库扫描 + `git status --porcelain`，折成检查器输入。
func r2Scan(t *testing.T, dir string) reconcile.Input {
	t.Helper()
	scan, err := query.VaultScan(dir, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("扫描 vault 失败：%v", err)
	}
	status, err := git.New(dir).Status()
	if err != nil {
		t.Fatalf("读 Git 状态失败：%v", err)
	}
	return reconcile.R2ScanOf(dir, scan, status, nil)
}

// r2Check 跑一次只读检查，返回命中对象 + findings + repairs（三者同源同一次扫描）。
func r2Check(t *testing.T, dir string) ([]reconcile.ReviewedTarget, reconcile.Result) {
	t.Helper()
	in := r2Scan(t, dir)
	return reconcile.ReviewedTargets(in), reconcile.Run(in)
}

// r2RepairInput 组装一次修复的入参（授权佐证由**进程边界**给，不由 plan 自证）。
func r2RepairInput(dir string, targets []reconcile.ReviewedTarget,
	res reconcile.Result) ReviewedRepairInput {
	return ReviewedRepairInput{
		VaultRoot: dir, Targets: targets, Repairs: res.Repairs, Findings: res.Findings,
		UserRequest: true, Reason: "对账 R2：补齐外部编辑后的过目信号",
		RequirementIDs: []string{"EG-EDIT-05"},
	}
}

// fmKeys 取一份 Markdown 的 frontmatter **顶层键集合**（升序）。
func fmKeys(t *testing.T, raw string) []string {
	t.Helper()
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		t.Fatalf("不是带 frontmatter 的产物：\n%s", raw)
	}
	var keys []string
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || strings.TrimSpace(l) == "" {
			continue // 嵌套项与空行不是顶层键
		}
		if i := strings.Index(l, ":"); i > 0 {
			keys = append(keys, l[:i])
		}
	}
	sort.Strings(keys)
	return keys
}

// fmBody 取正文全文（frontmatter 之后的全部字节）。
func fmBody(t *testing.T, raw string) string {
	t.Helper()
	i := strings.Index(raw, "\n---")
	if i < 0 {
		t.Fatalf("不是带 frontmatter 的产物：\n%s", raw)
	}
	rest := raw[i+len("\n---"):]
	if j := strings.Index(rest, "\n"); j >= 0 {
		return rest[j+1:]
	}
	return rest
}

// addedFMKeys 返回「修复后新增的顶层键」（before → after 的差集）。
func addedFMKeys(t *testing.T, before, after string) []string {
	t.Helper()
	old := map[string]bool{}
	for _, k := range fmKeys(t, before) {
		old[k] = true
	}
	var out []string
	for _, k := range fmKeys(t, after) {
		if !old[k] {
			out = append(out, k)
		}
	}
	return out
}

// changedFMLines 返回「整行发生变化的顶层键」（键集合相同但值变了的那些）。
func changedFMLines(t *testing.T, before, after string) []string {
	t.Helper()
	var out []string
	for _, k := range fmKeys(t, after) {
		if fmKeyLineOf(t, before, k) != fmKeyLineOf(t, after, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// —— ① 只写一键（diff 级反证） ——

func TestR2OnlyReviewedAtWritten(t *testing.T) {
	cases := []struct {
		name string
		// relOf 选出本行要外部编辑并补齐的目标（另一个文件充当「一字不动」的对照）。
		relOf func(cardRel, noteRel string) (string, string)
	}{
		{"知识卡：只长出 reviewed_at 一行，其余键与正文逐字不变",
			func(cardRel, noteRel string) (string, string) { return cardRel, noteRel }},
		{"材料笔记：四类产物同构，同样只长出那一个键",
			func(cardRel, noteRel string) (string, string) { return noteRel, cardRel }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, cardRel, noteRel := reviewedVault(t)
			rel, otherRel := c.relOf(cardRel, noteRel)
			r2EditFile(t, dir, rel)

			before := string(mustRead(t, absIn(dir, rel)))
			otherBefore := string(mustRead(t, absIn(dir, otherRel)))
			beforeCommits := gitLogCount(t, dir)

			targets, res := r2Check(t, dir)
			if len(targets) != 1 || targets[0].Path != rel {
				t.Fatalf("R2 应恰命中被外部编辑的 %s，实得 %+v", rel, targets)
			}
			out, err := r2Root(t, dir).RepairReviewed(r2RepairInput(dir, targets, res))
			if err != nil {
				t.Fatalf("R2 修复失败：%v（%+v）", err, out)
			}
			if out.Ops != 1 || len(out.Skipped) != 0 || len(out.Failures) != 0 {
				t.Fatalf("应恰 1 个 op、零跳过零失败：%+v", out)
			}
			if !reflect.DeepEqual(out.Written, []string{rel}) {
				t.Fatalf("写入路径 = %v，期望恰 [%s]", out.Written, rel)
			}
			if !reflect.DeepEqual(out.Keys, []string{model.FMKeyReviewedAt}) {
				t.Fatalf("写入键集合 = %v，期望恰 [%s]", out.Keys, model.FMKeyReviewedAt)
			}

			after := string(mustRead(t, absIn(dir, rel)))
			// 1) 新增的顶层键**恰一个**，就是那一个封闭键。
			if got := addedFMKeys(t, before, after); !reflect.DeepEqual(got,
				[]string{model.FMKeyReviewedAt}) {
				t.Fatalf("新增顶层键 = %v，期望恰 [%s]", got, model.FMKeyReviewedAt)
			}
			// 2) 整行发生变化的键**也恰那一个**（其余键的整行逐字不变）。
			if got := changedFMLines(t, before, after); !reflect.DeepEqual(got,
				[]string{model.FMKeyReviewedAt}) {
				t.Fatalf("整行变化的键 = %v，期望恰 [%s]（三维不牵连）", got, model.FMKeyReviewedAt)
			}
			// 3) 值就是本次对账时刻，且恰一行（覆盖而不是累积）。
			if !strings.Contains(after, r2RepairAt) {
				t.Fatalf("%s 的值不是本次对账时刻 %s：\n%s", model.FMKeyReviewedAt, r2RepairAt, after)
			}
			if n := strings.Count(after, "\n"+model.FMKeyReviewedAt+":"); n != 1 {
				t.Fatalf("%s 出现 %d 行，期望恰 1 行", model.FMKeyReviewedAt, n)
			}
			if out.Stamp != r2RepairAt {
				t.Fatalf("回执时刻 = %q，期望 %q（与落盘值同源）", out.Stamp, r2RepairAt)
			}
			// 4) 正文（含用户手写的那一行）逐字节不变。
			if fmBody(t, after) != fmBody(t, before) {
				t.Fatal("正文发生变化：R2 只补一个 frontmatter 键，绝不碰用户的字节")
			}
			if !strings.Contains(after, "用户在编辑器里手写的一行") {
				t.Fatal("用户外部编辑的内容被改写了")
			}
			// 5) 状态维度 / 删除维度 / 替代指针维度一格不写。
			for _, key := range []string{"status", "updated_at", "created_at"} {
				if fmKeyLineOf(t, after, key) != fmKeyLineOf(t, before, key) {
					t.Fatalf("%s 行发生变化：过目维度与它正交", key)
				}
			}
			for _, key := range fmKeys(t, after) {
				if strings.HasPrefix(key, "deleted") || key == "replaced_by" {
					t.Fatalf("修复长出了 %s：删除维度与替代指针维度一格不写", key)
				}
			}
			// 6) 只动目标一个文件；本桥自己零提交（提交归纳管写口的恰一次 commit）。
			if got := string(mustRead(t, absIn(dir, otherRel))); got != otherBefore {
				t.Fatalf("%s 的字节发生变化：R2 只写命中对象", otherRel)
			}
			if out.Commits != 0 {
				t.Fatalf("修复桥的提交条数 = %d，恒应为 0（A-35）", out.Commits)
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("commit 数 %d → %d：修复桥不得自己提交", beforeCommits, got)
			}
		})
	}
}

// TestR2RepairRequiresUserRequest：授权佐证缺失（P-A 列）→ 矩阵判 error、**零写入**。
//
// A-34 的反证：`initiator=user` 只是 plan 内容的自证，进程边界那一半缺了照样写不进去。
func TestR2RepairRequiresUserRequest(t *testing.T) {
	dir, cardRel, _ := reviewedVault(t)
	r2EditFile(t, dir, cardRel)
	before := string(mustRead(t, absIn(dir, cardRel)))
	beforeCommits := gitLogCount(t, dir)

	targets, res := r2Check(t, dir)
	in := r2RepairInput(dir, targets, res)
	in.UserRequest = false
	out, err := r2Root(t, dir).RepairReviewed(in)
	if err == nil {
		t.Fatalf("缺授权佐证却成功了：%+v", out)
	}
	if len(out.Written) != 0 || len(out.Failures) == 0 {
		t.Fatalf("缺授权佐证必须零写入且如实上报：%+v", out)
	}
	if got := string(mustRead(t, absIn(dir, cardRel))); got != before {
		t.Fatal("校验失败路径必须零写入：目标字节不得变化")
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("校验失败路径必须零 commit：%d → %d", beforeCommits, got)
	}
}

// —— ② B3 不豁免 ——

func TestR2B3HashStaleSkipsWithoutWrite(t *testing.T) {
	cases := []struct {
		name string
		// baseOf 给出进 plan 的 base（模拟「对账观测时刻的 hash」）。
		baseOf func(id string) map[string]string
	}{
		{"观测到的 hash 已过期（检查与修复之间用户又改了一次）",
			func(id string) map[string]string {
				return map[string]string{id: "sha256:0000000000000000000000000000000000000000000000000000000000000000"}
			}},
		{"base 未覆盖该文件（无法确认未变化）",
			func(id string) map[string]string {
				return map[string]string{"k-20260101-absent": "sha256:deadbeef"}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, cardRel, _ := reviewedVault(t)
			r2EditFile(t, dir, cardRel)
			before := string(mustRead(t, absIn(dir, cardRel)))
			beforeCommits := gitLogCount(t, dir)

			targets, res := r2Check(t, dir)
			if len(targets) != 1 {
				t.Fatalf("前置不成立：R2 应恰命中 1 个对象，实得 %d", len(targets))
			}
			in := r2RepairInput(dir, targets, res)
			in.Base = c.baseOf(targets[0].ID)
			out, err := r2Root(t, dir).RepairReviewed(in)
			if err != nil {
				t.Fatalf("B3 跳过不是错误路径（应正常返回并如实交代）：%v", err)
			}
			// 1) 恰一条跳过，kind 是封闭两值里的 file_changed（不新增第三种 kind）。
			if len(out.Skipped) != 1 {
				t.Fatalf("应恰 1 条跳过，实得 %d：%+v", len(out.Skipped), out.Skipped)
			}
			if out.Skipped[0].Kind != "file_changed" {
				t.Fatalf("跳过 kind = %q，期望 file_changed", out.Skipped[0].Kind)
			}
			if strings.TrimSpace(out.Skipped[0].Cause) == "" {
				t.Fatal("跳过必须带 cause（为什么没写）")
			}
			// 2) 零写入：目标字节逐字不变、无 commit。
			if len(out.Written) != 0 || len(out.Reviewed) != 0 {
				t.Fatalf("B3 跳过必须零写入：%+v", out)
			}
			if got := string(mustRead(t, absIn(dir, cardRel))); got != before {
				t.Fatal("B3 跳过却改了字节")
			}
			if strings.Contains(string(mustRead(t, absIn(dir, cardRel))), model.FMKeyReviewedAt) {
				t.Fatal("B3 跳过却补写了过目信号")
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("B3 跳过必须零 commit：%d → %d", beforeCommits, got)
			}
			// 3) finding **仍在**，且 detail 注明已跳过（不删条目、不降级）。
			if len(out.Findings) != len(res.Findings) {
				t.Fatalf("finding 条数 %d → %d：跳过不得删条目", len(res.Findings), len(out.Findings))
			}
			var noted int
			for i, f := range out.Findings {
				if f.Check != reconcile.CheckReviewedAtMissing {
					continue
				}
				if f.Severity != res.Findings[i].Severity {
					t.Fatal("跳过不得改变 finding 分级")
				}
				if !reflect.DeepEqual(f.Targets, res.Findings[i].Targets) {
					t.Fatal("跳过不得改变 finding 的 targets")
				}
				if strings.Contains(f.Detail, reconcile.ReviewedSkipNotice) {
					noted++
				}
			}
			if noted != 1 {
				t.Fatalf("应恰 1 条 finding 带「已跳过」注记，实得 %d", noted)
			}
		})
	}
}

// —— ③ 写入键集合封闭 ——

func TestR2WrittenKeySetClosed(t *testing.T) {
	dir, cardRel, noteRel := reviewedVault(t)
	// 两类对象同时被外部编辑：一次修复覆盖两个文件，键集合封闭对两者同时成立。
	r2EditFile(t, dir, cardRel)
	r2EditFile(t, dir, noteRel)
	beforeCard := string(mustRead(t, absIn(dir, cardRel)))
	beforeNote := string(mustRead(t, absIn(dir, noteRel)))

	targets, res := r2Check(t, dir)
	if len(targets) != 2 {
		t.Fatalf("两类对象各应命中一次，实得 %+v", targets)
	}
	out, err := r2Root(t, dir).RepairReviewed(r2RepairInput(dir, targets, res))
	if err != nil {
		t.Fatalf("R2 修复失败：%v", err)
	}
	if out.Ops != 2 || len(out.Written) != 2 {
		t.Fatalf("应恰 2 个 op / 2 处写入：%+v", out)
	}

	for _, pair := range [][2]string{{cardRel, beforeCard}, {noteRel, beforeNote}} {
		rel, before := pair[0], pair[1]
		after := string(mustRead(t, absIn(dir, rel)))
		diff := addedFMKeys(t, before, after)
		if !reflect.DeepEqual(diff, []string{model.FMKeyReviewedAt}) {
			t.Fatalf("%s 的键集合差集 = %v，期望恰 [%s]", rel, diff, model.FMKeyReviewedAt)
		}
		if got := changedFMLines(t, before, after); !reflect.DeepEqual(got,
			[]string{model.FMKeyReviewedAt}) {
			t.Fatalf("%s 整行变化的键 = %v，期望恰 [%s]", rel, got, model.FMKeyReviewedAt)
		}
	}
	// 键集合的**真源**只有一处，且恒恰一个键。
	if got := reconcile.ReviewedKeys(); !reflect.DeepEqual(got, out.Keys) ||
		len(got) != reconcile.ReviewedKeyCount || reconcile.ReviewedKeyCount != 1 {
		t.Fatalf("待写键真源 = %v / 回执 = %v，两者应同源且恰 1 个键", got, out.Keys)
	}
	// op 名与 op 全集：**R2 侧**复用既有 `mark_reviewed`，一格也不新增 op（A-33）。
	if ReviewedRepairOp != "mark_reviewed" {
		t.Fatalf("R2 用的 op = %q，A-33 裁决为复用既有 mark_reviewed", ReviewedRepairOp)
	}
	// op 全集按**加法等式**钉死，不写死单个数字：
	//   M3 期 16（历史事实，M3 结论不改写）+ M4 新增 1（R6 的 set_stale，A-33）
	//   + Schema v2 新增 2（Opinion 的 create_opinion / append_opinion，契约 §4.4）= 19。
	//
	// **Schema v2 · T-…-003 重钉（事实变了，判据形态不变）**：等式右侧多了一项，
	// 而不是把 17 改成 19 —— 加法等式的价值就在于「每一项都能说出自己是哪个里程碑加的」。
	// 本体（「R2 不新增 op」）一格未放宽：新增的两个 op 属 Knowledge/Opinion 写口，
	// 与本文件（R2 修复桥）无关，故下方同时反证「R2 的 op 既不是 M4 新增的那个，
	// 也不是 v2 新增的两个」。注意两个 Knowledge 兼容别名（create_card / append_card）
	// **不计入**：它们在 expand 阶段就被改写成规范名，不是可派发 op。
	const m3AllOps, m4NewOps, v2NewOps = 16, 1, 2
	if n := len(plan.AllOpNames()); n != m3AllOps+m4NewOps+v2NewOps {
		t.Fatalf("可派发 op 总数 = %d，期望「M3 期 %d + M4 新增 %d + Schema v2 新增 %d = %d」",
			n, m3AllOps, m4NewOps, v2NewOps, m3AllOps+m4NewOps+v2NewOps)
	}
	for _, op := range []string{plan.OpCreateOpinion, plan.OpAppendOpinion} {
		if ReviewedRepairOp == op {
			t.Fatalf("R2 修复桥不得改用 Schema v2 新增的 %s（那是 Opinion 的写口）", op)
		}
	}
	// 加严一格：M4 新增的 op 恰一个，且其名字逐字为 `set_stale`（R6 专属，A-33）。
	// 若日后有人往 M4 面偷偷再加 op，或把新增 op 混成 R2 用的那个，这里立即红。
	if got := plan.M4OpNames(); len(got) != m4NewOps || got[0] != plan.OpSetStale ||
		plan.OpSetStale != "set_stale" {
		t.Fatalf("M4 新增 op 应恰 %d 个且逐字为 set_stale，实得 %v", m4NewOps, got)
	}
	if ReviewedRepairOp == plan.OpSetStale {
		t.Fatalf("R2 修复桥不得改用 M4 新增的 %s（那是 R6 的写口）", plan.OpSetStale)
	}
	// 待写键不封闭的意向：**整批拒绝、零写入**（宁可一个不写，也不写第二个键）。
	bad := r2RepairInput(dir, targets, res)
	bad.Repairs = append([]reconcile.RepairSpec{}, res.Repairs...)
	bad.Repairs[0].Keys = []string{"reviewed_at", "status"}
	if _, err := r2Root(t, dir).RepairReviewed(bad); err == nil {
		t.Fatal("待写键不封闭却被接受了")
	}
}

// —— ④ 幂等 ——

func TestR2RepairIdempotentZeroWrite(t *testing.T) {
	dir, cardRel, _ := reviewedVault(t)
	r2EditFile(t, dir, cardRel)
	targets, res := r2Check(t, dir)
	if _, err := r2Root(t, dir).RepairReviewed(r2RepairInput(dir, targets, res)); err != nil {
		t.Fatalf("首轮修复失败：%v", err)
	}
	afterFirst := string(mustRead(t, absIn(dir, cardRel)))
	commits := gitLogCount(t, dir)

	// 第二轮：三条判定里的第 3 条已不成立（过目信号不再缺失 / 不再滞后）→ 零命中。
	targets2, res2 := r2Check(t, dir)
	if len(targets2) != 0 {
		t.Fatalf("补齐后不应再命中 R2，实得 %+v", targets2)
	}
	for _, f := range res2.Findings {
		if f.Check == reconcile.CheckReviewedAtMissing {
			t.Fatalf("补齐后仍报 %s：%+v", reconcile.CheckReviewedAtMissing, f)
		}
	}
	out, err := r2Root(t, dir).RepairReviewed(r2RepairInput(dir, targets2, res2))
	if err != nil {
		t.Fatalf("零命中的修复不该报错：%v", err)
	}
	if !out.Ran || out.Ops != 0 || len(out.Written) != 0 || out.Commits != 0 {
		t.Fatalf("零命中必须零 op、零写入、零提交（ran 仍为 true）：%+v", out)
	}
	if got := string(mustRead(t, absIn(dir, cardRel))); got != afterFirst {
		t.Fatal("第二轮修复写盘了（应幂等）")
	}
	if got := gitLogCount(t, dir); got != commits {
		t.Fatalf("第二轮修复产生了 commit：%d → %d", commits, got)
	}
}

// —— ⑤ 恰一次 commit（R1 + R2 合并） ——

func TestR2RepairJoinsSingleReconcileCommit(t *testing.T) {
	dir, cardRel, _ := reviewedVault(t)
	r2EditFile(t, dir, cardRel) // 同一个外部编辑同时构成 R1 的未提交改动与 R2 的命中
	beforeCommits := gitLogCount(t, dir)

	targets, res := r2Check(t, dir)
	if len(targets) != 1 {
		t.Fatalf("前置不成立：R2 应恰命中 1 个对象，实得 %d", len(targets))
	}
	r := r2Root(t, dir)
	out, err := r.RepairReviewed(r2RepairInput(dir, targets, res))
	if err != nil {
		t.Fatalf("R2 修复失败：%v", err)
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("修复阶段不得提交：%d → %d", beforeCommits, got)
	}

	// 纳管写口在编排末尾产生**恰一次** commit，R1 的外部编辑与 R2 的补写同进这一条。
	tk := NewReconcileTakeover(git.New(dir))
	tres, terr := tk.Takeover(ReconcileTakeoverInput{
		Domain: "ai-infra", Findings: len(res.Findings), Repairs: len(res.Repairs),
		OurWrites: out.Written, ChecksDone: true, RepairsDone: true,
		Reason: "对账：R1 纳管 + R2 补齐（恰一次 commit）", RequirementIDs: []string{"EG-EDIT-05"},
	})
	if terr != nil {
		t.Fatalf("纳管失败：%v", terr)
	}
	if tk.Commits() != 1 || tres.Commit == nil || *tres.Commit == "" {
		t.Fatalf("整轮对账应恰产生 1 条 commit：%+v", tres)
	}
	if got := gitLogCount(t, dir); got != beforeCommits+1 {
		t.Fatalf("commit 数 %d → %d，应恰 +1（R1 + R2 合并，不是 +2）", beforeCommits, got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("对账后工作区应干净：%q", got)
	}
	subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
	if !strings.HasPrefix(subject, "reconcile(") {
		t.Fatalf("commit 主题 = %q，期望 reconcile(<domain>): …（A-30）", subject)
	}
	// 补写的那一个键与用户的外部编辑同进这一条 commit。
	files := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	if !strings.Contains(files, cardRel) {
		t.Fatalf("本次 commit 未含 %s：\n%s", cardRel, files)
	}
	if !strings.Contains(gitOut(t, dir, "show", "HEAD"), model.FMKeyReviewedAt) {
		t.Fatal("本次 commit 的 diff 里没有那一个补写的键")
	}
}
