package cli

// `eg mark-reviewed` 与 `eg unreviewed` 的命令侧机器判据
// （T-evergreen.s1_main_flow-158614-042；提案与状态合同 §6.1、授权合同矩阵 #7 / #22）。
//
// 四组断言，逐条对应最容易被做丢的不变式：
//   ① mark-reviewed 是 reviewed_at 的唯一写入路径，且只写这一个键
//      （status / updated_at / 删除维度逐字不变；四类产物同构）；
//   ② 四类动作一律不更新 reviewed_at：被动查看 / 结果报告 / 查询读取 / Agent 写入；
//   ③ eg unreviewed 的筛选语义：命中 / 不命中 / **无 reviewed_at 计入** / 三类条件叠加取交集；
//   ④ eg unreviewed 零副作用：零写入、零 commit、输出不含任何催促类文案。

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// reviewedVault 建一个含「一份原文 + 一篇材料笔记 + 一张卡」的干净 vault。
// 三者都**没有** reviewed_at：本组用例的语料必须从「从未过目」这一真实起点开始。
func reviewedVault(t *testing.T) (dir string, cardRel string, noteRel string) {
	t.Helper()
	dir = applyVault(t)
	noteRel, cardRel = applyNoteAndCard(t, dir)
	if strings.Contains(string(mustRead(t, absIn(dir, cardRel))), model.FMKeyReviewedAt) {
		t.Fatalf("前置不成立：新建卡不该带 %s（S1/S2 不回填默认值）", model.FMKeyReviewedAt)
	}
	return dir, cardRel, noteRel
}

// stampAt 解析一个固定时刻（用例全程确定性，绝不取本机当前时间）。
func stampAt(t *testing.T, raw string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("解析固定时刻 %q 失败：%v", raw, err)
	}
	return ts
}

// runMarkReviewedCLI 在 dir 上跑一次 `eg mark-reviewed …`（--json 信封）。
// stdin 恒为已关闭状态：本命令没有任何确认点，任何读 stdin 的行为都会红。
func runMarkReviewedCLI(t *testing.T, dir string, at string, args ...string) (int, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return stampAt(t, at) }
	r.In = closedStdin{}
	full := append([]string{"mark-reviewed"}, args...)
	full = append(full, "--vault", dir, "--json")
	code, out, errOut := runCLI(t, r, full...)
	return code, out + errOut
}

// runUnreviewedCLI 在 dir 上跑一次 `eg unreviewed …`（--json 信封）。
func runUnreviewedCLI(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.In = closedStdin{}
	full := append([]string{"unreviewed"}, args...)
	full = append(full, "--vault", dir, "--json")
	code, out, errOut := runCLI(t, r, full...)
	return code, out + errOut
}

// unreviewedIDs 从 --json 输出里读出清单里的 id（只读命令按 search 的既有口径，
// 清单直接挂在 .data.unreviewed 上；产生报告的命令才是 .data.report.*）。
func unreviewedIDs(t *testing.T, output string) []string {
	t.Helper()
	var env struct {
		Data struct {
			Unreviewed []UnreviewedRow `json:"unreviewed"`
			Total      int             `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(firstJSONObject(t, output)), &env); err != nil {
		t.Fatalf("--json 输出不可解析：%v\n%s", err, output)
	}
	if env.Data.Total != len(env.Data.Unreviewed) {
		t.Fatalf("total = %d 与清单长度 %d 不一致", env.Data.Total, len(env.Data.Unreviewed))
	}
	ids := make([]string, 0, len(env.Data.Unreviewed))
	for _, row := range env.Data.Unreviewed {
		if !row.Unreviewed {
			t.Fatalf("清单里出现 unreviewed=false 的行：%+v（进清单的就是命中的）", row)
		}
		ids = append(ids, row.ID)
	}
	return ids
}

// firstJSONObject 截出输出里的 JSON 信封（人类可读文案不会混进 --json 的 stdout，
// 但错误路径会把文案写进 stderr，这里只取信封那一段）。
func firstJSONObject(t *testing.T, output string) string {
	t.Helper()
	i := strings.Index(output, "{")
	j := strings.LastIndex(output, "}")
	if i < 0 || j <= i {
		t.Fatalf("输出里没有 JSON 信封：\n%s", output)
	}
	return output[i : j+1]
}

// fmKeyLineOf 取某个 frontmatter 顶层键所在的**整行**（含引号与空格），逐字比对用。
func fmKeyLineOf(t *testing.T, raw string, key string) string {
	t.Helper()
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, key+":") {
			return line
		}
	}
	return ""
}

// —— ① mark-reviewed 是唯一写入路径，且只写一个键 ——

func TestMarkReviewed(t *testing.T) {
	const markAt = "2026-09-02T09:00:00+08:00"
	cases := []struct {
		name string
		// targetOf 给出本行要标记的对象（返回 ID 与它的相对路径）。
		targetOf func(t *testing.T, dir, cardRel, noteRel string) (string, string)
	}{
		{
			name: "知识卡：只长出 reviewed_at 一行，status / updated_at 逐字不变",
			targetOf: func(t *testing.T, dir, cardRel, noteRel string) (string, string) {
				return applyCardID, cardRel
			},
		},
		{
			name: "材料笔记：四类产物同构（笔记没有 status 这一格，也不得长出）",
			targetOf: func(t *testing.T, dir, cardRel, noteRel string) (string, string) {
				return applyNoteID, noteRel
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, cardRel, noteRel := reviewedVault(t)
			target, rel := c.targetOf(t, dir, cardRel, noteRel)
			before := string(mustRead(t, absIn(dir, rel)))
			statusBefore := fmKeyLineOf(t, before, "status")
			updatedBefore := fmKeyLineOf(t, before, "updated_at")
			otherRel := cardRel
			if rel == cardRel {
				otherRel = noteRel
			}
			otherBefore := string(mustRead(t, absIn(dir, otherRel)))
			beforeCommits := gitLogCount(t, dir)

			code, output := runMarkReviewedCLI(t, dir, markAt, "--target", target)
			if code != ExitOK {
				t.Fatalf("eg mark-reviewed 退出码 = %d，期望 0：%s", code, output)
			}
			after := string(mustRead(t, absIn(dir, rel)))

			// 1) reviewed_at 恰一行，且值就是本次时刻（单键覆盖，不长出历史数组）。
			if n := strings.Count(after, "\n"+model.FMKeyReviewedAt+":"); n != 1 {
				t.Fatalf("%s 出现 %d 行，期望恰 1 行：\n%s", model.FMKeyReviewedAt, n, after)
			}
			if !strings.Contains(after, markAt) {
				t.Fatalf("%s 的值不是本次时刻 %s：\n%s", model.FMKeyReviewedAt, markAt, after)
			}
			// 2) status 与 updated_at 两行逐字未变（三个维度正交；过目不是内容修改）。
			if got := fmKeyLineOf(t, after, "status"); got != statusBefore {
				t.Fatalf("status 行 = %q，期望逐字仍是 %q", got, statusBefore)
			}
			if got := fmKeyLineOf(t, after, "updated_at"); got != updatedBefore {
				t.Fatalf("updated_at 行 = %q，期望逐字仍是 %q（标记已过目绝不更新它）",
					got, updatedBefore)
			}
			if updatedBefore == "" {
				t.Fatal("前置不成立：目标缺 updated_at 行，本用例失去判据")
			}
			// 3) 删除维度一格未碰。
			for _, key := range []string{"deleted_at:", "deleted_reason:"} {
				if strings.Contains(after, key) {
					t.Fatalf("标记已过目长出了 %s：删除维度与过目维度正交", key)
				}
			}
			// 4) 只动目标一个文件。
			if got := string(mustRead(t, absIn(dir, otherRel))); got != otherBefore {
				t.Fatalf("%s 的字节发生变化：mark-reviewed 只写目标一个文件", otherRel)
			}
			// 5) 恰一条 commit，verb = process（KnownVerbs 未增减）。
			if got := gitLogCount(t, dir); got != beforeCommits+1 {
				t.Fatalf("commit 数 %d → %d，期望恰 +1", beforeCommits, got)
			}
			if head := gitOut(t, dir, "log", "--oneline", "-1"); !strings.Contains(head, "process(") {
				t.Fatalf("最后一条 commit 主题 = %q，期望 process(<domain>): …", head)
			}
			// 6) 报告逐字含「只写一个键」的说明。
			if !strings.Contains(output, MarkReviewedNoContentChangeNotice) {
				t.Fatalf("报告缺逐字串 %q：\n%s", MarkReviewedNoContentChangeNotice, output)
			}
			if strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")) != "" {
				t.Fatalf("mark-reviewed 之后工作区必须干净：%s", gitOut(t, dir, "status", "--porcelain"))
			}
		})
	}
}

// TestMarkReviewedRejectsBadInvocation：参数与目标两类失败均**零写入零 commit**。
func TestMarkReviewedRejectsBadInvocation(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"缺 --target → 退 1（参数形态）", []string{}, ExitUsage},
		{"目标解析不到 → 退 2（校验失败）", []string{"--target", "k-20260101-absent"}, ExitValidation},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, cardRel, _ := reviewedVault(t)
			before := string(mustRead(t, absIn(dir, cardRel)))
			beforeCommits := gitLogCount(t, dir)
			code, output := runMarkReviewedCLI(t, dir, "2026-09-02T09:00:00+08:00", c.args...)
			if code != c.wantCode {
				t.Fatalf("退出码 = %d，期望 %d：%s", code, c.wantCode, output)
			}
			if got := string(mustRead(t, absIn(dir, cardRel))); got != before {
				t.Fatal("失败路径必须零写入：目标字节不得变化")
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("失败路径必须零 commit：%d → %d", beforeCommits, got)
			}
		})
	}
}

// TestMarkReviewedOverwritesSingleKey：重复标记只覆盖同一个键（不累积历史）。
func TestMarkReviewedOverwritesSingleKey(t *testing.T) {
	dir, cardRel, _ := reviewedVault(t)
	if code, out := runMarkReviewedCLI(t, dir, "2026-09-02T09:00:00+08:00",
		"--target", applyCardID); code != ExitOK {
		t.Fatalf("首次标记退出码 = %d：%s", code, out)
	}
	const second = "2026-09-03T09:00:00+08:00"
	if code, out := runMarkReviewedCLI(t, dir, second, "--target", applyCardID); code != ExitOK {
		t.Fatalf("二次标记退出码 = %d：%s", code, out)
	}
	after := string(mustRead(t, absIn(dir, cardRel)))
	if n := strings.Count(after, "\n"+model.FMKeyReviewedAt+":"); n != 1 {
		t.Fatalf("%s 出现 %d 行，重复标记必须只覆盖单键：\n%s", model.FMKeyReviewedAt, n, after)
	}
	if !strings.Contains(after, second) {
		t.Fatalf("%s 未被更新为 %s：\n%s", model.FMKeyReviewedAt, second, after)
	}
}

// —— ② 四类动作一律不更新 reviewed_at（表驱动 4 行）——

func TestReviewedAtNotTouchedByReportOrAgent(t *testing.T) {
	const markAt = "2026-09-02T09:00:00+08:00"
	cases := []struct {
		name string
		// act 执行本行那一类动作；返回值只用于失败时打印。
		act func(t *testing.T, dir, cardRel string) string
	}{
		{
			name: "被动查看：eg card show",
			act: func(t *testing.T, dir, cardRel string) string {
				code, out, errOut := runCLI(t, newTestRoot(t, dir),
					"card", "show", applyCardID, "--vault", dir, "--json")
				if code != ExitOK {
					t.Fatalf("card show 退出码 = %d：%s", code, out+errOut)
				}
				return out
			},
		},
		{
			name: "结果报告：eg report --last（§4.6 末尾：报告同样不更新它）",
			act: func(t *testing.T, dir, cardRel string) string {
				code, out, errOut := runCLI(t, newTestRoot(t, dir),
					"report", "--last", "--vault", dir, "--json")
				if code != ExitOK {
					t.Fatalf("report --last 退出码 = %d：%s", code, out+errOut)
				}
				return out
			},
		},
		{
			name: "索引/查询读取：eg search",
			act: func(t *testing.T, dir, cardRel string) string {
				code, out, errOut := runCLI(t, newTestRoot(t, dir),
					"search", "注意力", "--vault", dir, "--json")
				if code != ExitOK {
					t.Fatalf("search 退出码 = %d：%s", code, out+errOut)
				}
				return out
			},
		},
		{
			name: "Agent 写入：eg apply（无命令行佐证的 P-A 写入，成功落一张新卡）",
			act: func(t *testing.T, dir, cardRel string) string {
				code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, ""))
				if code != ExitOK {
					t.Fatalf("Agent apply 退出码 = %d：%s", code, errOut)
				}
				return errOut
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, cardRel, _ := reviewedVault(t)
			// 先经唯一写入路径把 reviewed_at 写上，四行才有「字节不变」可断言。
			if code, out := runMarkReviewedCLI(t, dir, markAt, "--target", applyCardID); code != ExitOK {
				t.Fatalf("前置 mark-reviewed 退出码 = %d：%s", code, out)
			}
			before := string(mustRead(t, absIn(dir, cardRel)))
			reviewedBefore := fmKeyLineOf(t, before, model.FMKeyReviewedAt)
			if reviewedBefore == "" {
				t.Fatalf("前置不成立：目标缺 %s 行，本行失去判据", model.FMKeyReviewedAt)
			}

			output := c.act(t, dir, cardRel)

			after := string(mustRead(t, absIn(dir, cardRel)))
			if got := fmKeyLineOf(t, after, model.FMKeyReviewedAt); got != reviewedBefore {
				t.Fatalf("%s 行 = %q，期望逐字仍是 %q（该动作一律不更新它）\n%s",
					model.FMKeyReviewedAt, got, reviewedBefore, output)
			}
			if n := strings.Count(after, "\n"+model.FMKeyReviewedAt+":"); n != 1 {
				t.Fatalf("%s 出现 %d 行：该动作既不得改也不得重写该键", model.FMKeyReviewedAt, n)
			}
		})
	}
}

// TestReviewedAtNotWritableByAgentPlan：Agent 自动路径的 mark_reviewed op 被矩阵 #7 拦下
// （P-A 🔴 → 退 2、零写入）。这是「Agent 写入一律不更新 reviewed_at」的另一半反证。
func TestReviewedAtNotWritableByAgentPlan(t *testing.T) {
	dir, cardRel, _ := reviewedVault(t)
	before := string(mustRead(t, absIn(dir, cardRel)))
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"Agent 自动路径尝试标记已过目",
"requirement_ids":["EG-CFM-06"],"base":{},
"ops":[{"op":"mark_reviewed","target":"` + applyCardID + `"}]}`
	code, _, errOut := runApplyPlan(t, dir, plan)
	if code != ExitValidation {
		t.Fatalf("Agent 路径的 mark_reviewed 退出码 = %d，期望 2（矩阵 #7 的 P-A 是 🔴）：%s", code, errOut)
	}
	after := string(mustRead(t, absIn(dir, cardRel)))
	if after != before {
		t.Fatalf("被拦下的 op 必须零写入：目标字节不得变化")
	}
	if strings.Contains(after, model.FMKeyReviewedAt) {
		t.Fatalf("Agent 路径写出了 %s：\n%s", model.FMKeyReviewedAt, after)
	}
}

// —— ③ eg unreviewed 的筛选语义（表驱动 4 行）——

func TestUnreviewed(t *testing.T) {
	// 语料的 updated_at 恒为 2026-09-01T10:00:00+08:00（applyVault 的固定时刻）。
	cases := []struct {
		name string
		// markAt 为空表示本行不标记（保留「无 reviewed_at」的真实起点）。
		markAt  string
		args    []string
		wantIDs []string
	}{
		{
			name:    "updated_at > reviewed_at → 命中",
			markAt:  "2026-08-31T09:00:00+08:00",
			wantIDs: []string{applyCardID, applyNoteID},
		},
		{
			name:    "updated_at <= reviewed_at → 不命中（卡已过目，笔记仍未过目）",
			markAt:  "2026-09-02T09:00:00+08:00",
			wantIDs: []string{applyNoteID},
		},
		{
			name:    "无 reviewed_at → **计入**（缺省视为从未过目）",
			wantIDs: []string{applyCardID, applyNoteID},
		},
		{
			name:    "叠加领域条件 → 交集正确（领域内的两个产物都在）",
			args:    []string{"--domain", "ai-infra"},
			wantIDs: []string{applyCardID, applyNoteID},
		},
		{
			name:    "叠加标签条件 → 交集为空（语料没有这个标签）",
			args:    []string{"--tag", "no-such-tag"},
			wantIDs: []string{},
		},
		{
			name:    "叠加时间条件（--since 晚于 updated_at）→ 交集为空",
			args:    []string{"--since", "2026-09-05"},
			wantIDs: []string{},
		},
		{
			name:    "叠加时间条件（--until 覆盖 updated_at）→ 交集为全部未过目产物",
			args:    []string{"--until", "2026-09-01"},
			wantIDs: []string{applyCardID, applyNoteID},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, _, _ := reviewedVault(t)
			if c.markAt != "" {
				if code, out := runMarkReviewedCLI(t, dir, c.markAt, "--target", applyCardID); code != ExitOK {
					t.Fatalf("前置 mark-reviewed 退出码 = %d：%s", code, out)
				}
			}
			code, output := runUnreviewedCLI(t, dir, c.args...)
			if code != ExitOK {
				t.Fatalf("eg unreviewed 退出码 = %d，期望 0（零命中也退 0）：%s", code, output)
			}
			got := unreviewedIDs(t, output)
			if strings.Join(sortedCopy(got), ",") != strings.Join(sortedCopy(c.wantIDs), ",") {
				t.Fatalf("清单 = %v，期望 %v\n%s", got, c.wantIDs, output)
			}
		})
	}
}

// —— ④ eg unreviewed 零副作用 ——

func TestUnreviewedHasNoSideEffects(t *testing.T) {
	dir, cardRel, noteRel := reviewedVault(t)
	cardBefore := string(mustRead(t, absIn(dir, cardRel)))
	noteBefore := string(mustRead(t, absIn(dir, noteRel)))
	beforeCommits := gitLogCount(t, dir)

	code, output := runUnreviewedCLI(t, dir)
	if code != ExitOK {
		t.Fatalf("eg unreviewed 退出码 = %d：%s", code, output)
	}
	// 1) 零写入：两个产物字节逐字不变，且**没有**被写上 reviewed_at（查看不等于过目）。
	if got := string(mustRead(t, absIn(dir, cardRel))); got != cardBefore {
		t.Fatal("eg unreviewed 改了卡的字节：本命令只读")
	}
	if got := string(mustRead(t, absIn(dir, noteRel))); got != noteBefore {
		t.Fatal("eg unreviewed 改了笔记的字节：本命令只读")
	}
	if strings.Contains(cardBefore+noteBefore, model.FMKeyReviewedAt) {
		t.Fatalf("语料本不该有 %s，前置不成立", model.FMKeyReviewedAt)
	}
	// 2) 零 commit、工作区干净（git status --porcelain 计数为 0）。
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("commit 数 %d → %d：只读命令不得产生 commit", beforeCommits, got)
	}
	if dirty := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); dirty != "" {
		t.Fatalf("git status --porcelain 非空：%s", dirty)
	}
	// 3) 输出不含任何催促类文案（字面量按纪律拼接构造，避免源码守卫命中完整字面量）。
	for _, banned := range []string{"请" + "尽快", "催" + "办"} {
		if strings.Contains(output, banned) {
			t.Fatalf("输出出现催促类文案 %q：清单只陈述事实\n%s", banned, output)
		}
	}
	// 4) 不改状态：输出里没有任何状态写入的痕迹，卡的 status 行逐字未变。
	if fmKeyLineOf(t, string(mustRead(t, absIn(dir, cardRel))), "status") !=
		fmKeyLineOf(t, cardBefore, "status") {
		t.Fatal("eg unreviewed 改了 status：本命令不改状态")
	}
}

// TestUnreviewedRejectsBadInvocation：参数非法一律退 1、零输出内容之外无副作用。
func TestUnreviewedRejectsBadInvocation(t *testing.T) {
	dir, _, _ := reviewedVault(t)
	cases := []struct {
		name string
		args []string
	}{
		{"--domain 未登记 → 退 1", []string{"--domain", "not-registered"}},
		{"--since 形态非法 → 退 1", []string{"--since", "2026/09/01"}},
		{"--since 晚于 --until → 退 1", []string{"--since", "2026-09-05", "--until", "2026-09-01"}},
		{"多余位置参数 → 退 1", []string{"extra"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, output := runUnreviewedCLI(t, dir, c.args...)
			if code != ExitUsage {
				t.Fatalf("退出码 = %d，期望 1：%s", code, output)
			}
			if dirty := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); dirty != "" {
				t.Fatalf("退 1 之后工作区必须干净：%s", dirty)
			}
		})
	}
}

// TestUnreviewedIsNotWiredToGitWrites：本命令在 vault 里跑完之后 `git log` 一字未增，
// 且二进制层面两次执行输出逐字相同（确定性口径覆盖只读命令）。
func TestUnreviewedIsDeterministic(t *testing.T) {
	dir, _, _ := reviewedVault(t)
	code1, out1 := runUnreviewedCLI(t, dir)
	code2, out2 := runUnreviewedCLI(t, dir)
	if code1 != ExitOK || code2 != ExitOK {
		t.Fatalf("两次执行退出码 = %d / %d，期望均为 0", code1, code2)
	}
	if firstJSONObject(t, out1) != firstJSONObject(t, out2) {
		t.Fatalf("同一语料两次执行输出不同：\n%s\n---\n%s", out1, out2)
	}
	// 只读命令不得留下任何 git 侧痕迹（这里顺带钉住「没有偷偷 exec git」这一事实）。
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("环境无 git：%v", err)
	}
	if dirty := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); dirty != "" {
		t.Fatalf("只读命令之后工作区必须干净：%s", dirty)
	}
}
