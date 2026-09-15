package cli

// T-…-016 的验收用例：报告在真实 apply 下的如实性，以及 eg report --last 的原样复现。
// 用例名以 Report 开头，可用 `go test ./internal/cli/... -run Report` 单独跑。
//
// internal/report 的用例只覆盖纯结构层（键集合 / 占位 / 渲染）；
// 这里覆盖必须由真实执行才能产生的事实：覆盖项缺失标注、工作区既有改动、
// 领域缺省落位、收件区条目未移出、以及最近一次报告的复现。

// **Schema v2 · T-…-003 夹具重钉（事实变了，判据形态不变）**：本文件里自动路径
// （`append_card` / `append_knowledge`）原先追加的是 Card 的 `解释与依据`。契约 D-7 把
// Knowledge 收敛为 `知识内容 / 条件与边界 / 用户补充` 三分区，`解释与依据` 自 v2 起
// 只作为**存量文件**的分区存在、且不在自动路径写白名单内，因此再拿它当写目标会让
// 整条 op 在校验期就被判「缺可写分区」而退 2 —— 那考的不再是本文件要考的事
// （B3 跳过 / 部分成功 / 事务放弃 / op 顺序 / 报告计数）。改用同为「只追加块」语义的
// v2 分区 `条件与边界`，本文件的判据一格未动。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// notePlanWith 生成一份 write_note plan，可注入额外字段（如 coverage_gaps / base）。
func notePlanWith(extraOp, extraTop string) string {
	return `{"plan_version":1,"verb":"process","domain":"ai-infra",
"reason":"报告用例","requirement_ids":["EG-EXT-02"]` + extraTop + `,
"ops":[{"op":"write_note","source":"` + applySourceID + `","note_id":"` + applyNoteID + `",
"title":"demo","sections":{"材料提炼":"提炼要点一。"}` + extraOp + `}]}`
}

func infoCount(diags []report.Diagnostic, needle string) int {
	n := 0
	for _, d := range diags {
		if d.Code == report.CodeI1 && strings.Contains(d.Message, needle) {
			n++
		}
	}
	return n
}

// —— ① 覆盖项缺失标注（EG-EXT-02）：声明 2 项 → 恰 2 条 I1；不声明 → 0 条 ——

func TestReportCoverageGapsArePassedThroughVerbatim(t *testing.T) {
	dir := applyVault(t)
	code, env, errOut := runApplyPlan(t, dir,
		notePlanWith(`,"coverage_gaps":["counterexample","limitation"]`, ""))
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if got := infoCount(rep.Warnings, "提炼覆盖项缺失"); got != 2 {
		t.Fatalf("I1 覆盖项条目 = %d 条，期望恰 2 条：%+v", got, rep.Warnings)
	}
	for _, d := range rep.Warnings {
		if strings.Contains(d.Message, "提炼覆盖项缺失") {
			if d.OpIndex != 0 || d.Path != "ops[0].coverage_gaps" {
				t.Fatalf("条目必须带 op 下标与字段路径：%+v", d)
			}
		}
	}
	raw, _ := json.Marshal(env.Data["report"])
	human := strings.Join(rep.Lines(), "\n")
	for _, gap := range []string{"counterexample", "limitation", "反例", "局限"} {
		if !strings.Contains(string(raw), gap) {
			t.Fatalf("--json 形态 grep 不到枚举值 %q", gap)
		}
		if !strings.Contains(human, gap) {
			t.Fatalf("人类可读形态 grep 不到枚举值 %q", gap)
		}
	}
	withKeys := applyReportKeys(t, env)

	// 无缺失用例：不得自行推断，报告全文不出现任何覆盖要点枚举名。
	dir2 := applyVault(t)
	code, env2, errOut := runApplyPlan(t, dir2, notePlanWith("", ""))
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep2 := applyReport(t, env2)
	if got := infoCount(rep2.Warnings, "提炼覆盖项缺失"); got != 0 {
		t.Fatalf("未声明 coverage_gaps 时该类条目必须为 0 条，实得 %d", got)
	}
	raw2, _ := json.Marshal(env2.Data["report"])
	text2 := string(raw2) + "\n" + strings.Join(rep2.Lines(), "\n")
	for _, gap := range []string{"counterexample", "limitation", "core_claim", "key_evidence"} {
		if strings.Contains(text2, gap) {
			t.Fatalf("报告不得编造覆盖要点 %q", gap)
		}
	}
	// 两个用例的报告体键集合必须一致（不新增 §4.6 之外的键）。
	if !sameKeys(withKeys, applyReportKeys(t, env2)) {
		t.Fatal("coverage_gaps 的有无不得改变报告体键集合")
	}
}

func sameKeys(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// —— ② 工作区既有改动：一条 I1 info + 路径清单；干净工作区不输出 ——

func TestReportExistingWorktreeChangesAreDisclosed(t *testing.T) {
	dir := applyVault(t)
	stray := filepath.Join(dir, "draft-note.md")
	if err := os.WriteFile(stray, []byte("与本次写入无关的既有改动。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, env, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if got := infoCount(rep.Warnings, "工作区既有改动"); got != 1 {
		t.Fatalf("既有改动说明 = %d 条，期望恰 1 条：%+v", got, rep.Warnings)
	}
	joined := strings.Join(rep.Lines(), "\n")
	if !strings.Contains(joined, "draft-note.md") {
		t.Fatalf("说明必须列出既有改动路径：%s", joined)
	}
	files := strings.Fields(gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD"))
	found := false
	for _, f := range files {
		if strings.Contains(f, "draft-note.md") {
			found = true
		}
	}
	if !found {
		t.Fatalf("按 git add -A 口径，既有改动应一并进本次 commit：%v", files)
	}
	if len(files) <= len(rep.Links) {
		t.Fatal("此时 commit 文件集合应真包含 links[]")
	}

	// 干净工作区：不得输出该条目。
	clean := applyVault(t)
	_, env2, _ := runApplyPlan(t, clean, notePlan())
	if got := infoCount(applyReport(t, env2).Warnings, "工作区既有改动"); got != 0 {
		t.Fatalf("干净工作区不得输出既有改动说明，实得 %d 条", got)
	}
}

// —— ③ 领域缺省落位（EG-DOM-03）——

func TestReportDefaultDomainFallbackIsDisclosed(t *testing.T) {
	dir := applyVault(t)
	plan := `{"plan_version":1,"verb":"process","reason":"缺省落位",
"ops":[{"op":"write_note","source":"` + applySourceID + `","note_id":"` + applyNoteID + `",
"title":"demo","sections":{"材料提炼":"提炼要点一。"}}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if !rep.DefaultDomainFallback.Used || rep.DefaultDomainFallback.Reason == "" {
		t.Fatalf("default_domain 回落必须显式上报：%+v", rep.DefaultDomainFallback)
	}
	if !strings.Contains(strings.Join(rep.Lines(), "\n"), "领域缺省落位") {
		t.Fatal("人类可读形态必须出现回落说明")
	}
}

// —— ④ 收件区条目未移出（EG-SRC-02）——

func TestReportInboxEntryNotDetachedIsDisclosed(t *testing.T) {
	dir := applyVault(t)
	// base 里给 unprocessed.md 一个过期 hash → 条目移出被跳过，但笔记照常落盘。
	plan := notePlanWith("", `,"base":{"`+store.UnprocessedFile+`":"`+
		store.ContentHash([]byte("过期内容"))+`"}`)
	code, env, _ := runApplyPlan(t, dir, plan)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（条目未移出属部分成功）", code)
	}
	rep := applyReport(t, env)
	found := false
	for _, s := range rep.Skipped {
		if strings.Contains(s.Detail, "收件区条目未移出") &&
			strings.Contains(s.Detail, applySourceID) && s.Locator == store.UnprocessedFile {
			found = true
		}
	}
	if !found {
		t.Fatalf("必须显式列出 source_id 与原因：%+v", rep.Skipped)
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(dir, store.UnprocessedFile))), applySourceID) {
		t.Fatal("条目未移出时应仍留在收件区（如实上报，不做补偿）")
	}
}

// —— ⑤ high_impact[] 四类各一例 ——

func TestReportHighImpactCoversFourS1Classes(t *testing.T) {
	dir := applyVault(t)
	if code, _, errOut := runApplyPlan(t, dir, notePlan()); code != ExitOK {
		t.Fatalf("落笔记退出码 = %d：%s", code, errOut)
	}
	// 先建一张对手卡，供 opposing / 论证关系指向。
	if code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, "")); code != ExitOK {
		t.Fatalf("落对手卡退出码 = %d：%s", code, errOut)
	}
	base := `"` + applyCard2ID + `":"` +
		hashOf(t, dir, store.CardRel("ai-infra", applyCard2ID)) + `"`
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"四类显著变更",
"requirement_ids":["EG-KNW-04"],"base":{` + base + `},
"ops":[{"op":"create_card","card_id":"` + applyCardID + `","title":"注意力机制",
"sources":[{"source":"` + applySourceID + `","note":"` + applyNoteID + `","rel":"support","reason":"原文给出定义"}],
"sections":{"知识内容":"注意力是一种加权求和。"}},
{"op":"append_card","card":"` + applyCard2ID + `","sections":{"条件与边界":"补一条非核心说明。"}},
{"op":"add_relation","from":"` + applyCardID + `","type":"opposing","target":"` + applyCard2ID + `","reason":"两者结论冲突"},
{"op":"add_relation","from":"` + applyCardID + `","type":"supports","target":"` + applyCard2ID + `","reason":"另一角度佐证"}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	kinds := map[string]string{}
	for _, h := range rep.HighImpact {
		kinds[h.Kind] = h.Target
	}
	for _, want := range []string{"new_core_card", "non_core_supplement", "opposing", "relation"} {
		target, ok := kinds[want]
		if !ok {
			t.Fatalf("high_impact[] 缺 kind=%s：%+v", want, rep.HighImpact)
		}
		if target == "" {
			t.Fatalf("kind=%s 的 target 必须可定位", want)
		}
	}
}

// —— ⑥ eg report --last 原样复现最近一次 apply 的报告 ——

func TestReportLastReplaysLastApplyVerbatim(t *testing.T) {
	dir := applyVault(t)
	_, env, _ := runApplyPlan(t, dir, notePlan())
	applyBody, err := json.Marshal(env.Data["report"])
	if err != nil {
		t.Fatal(err)
	}
	before := gitLogCount(t, dir)
	snapshot := treeSnapshot(t, dir)

	code, out, errOut := runCLI(t, newTestRoot(t, dir), "report", "--last", "--vault", dir, "--json")
	if code != ExitOK {
		t.Fatalf("eg report --last 退出码 = %d：%s", code, errOut)
	}
	var replay Envelope
	if err := json.Unmarshal([]byte(out), &replay); err != nil {
		t.Fatalf("--json 输出不可解析：%v", err)
	}
	replayBody, err := json.Marshal(replay.Data["report"])
	if err != nil {
		t.Fatal(err)
	}
	if string(replayBody) != string(applyBody) {
		t.Fatalf("report --last 必须与该次 apply 的报告逐字一致\napply ：%s\nreport：%s",
			applyBody, replayBody)
	}
	if got := gitLogCount(t, dir); got != before {
		t.Fatalf("只读命令不得产生 commit，log 数 = %d", got)
	}
	if diff := treeDiff(t, dir, snapshot); diff != "" {
		t.Fatalf("只读命令必须零文件变化，实际：%s", diff)
	}
}

// 无历史报告时给出可操作提示并退 1（不编造空报告）。
//
// I-…-018 取材面搬家 + 加严：`applyVault` 里已经跑过一次 `eg capture`，而 capture 自本轮起
// **也落**最近一次报告（合同 §1.9 覆盖 apply / capture），所以「本 vault 从未产出报告」这一
// 前提必须由**只 init 过**的 vault 来承担（captureVault）。判据同时加严两条：
// 提示不得预设用户上一步做了什么（不许只把人引向 apply），且必须同时给出 capture 这一半入口。
func TestReportLastWithoutHistoryExitsOne(t *testing.T) {
	dir := captureVault(t)
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(LastReportFile))); !os.IsNotExist(err) {
		t.Fatalf("前置条件：只 init 过的 vault 不该有 %s（err=%v）", LastReportFile, err)
	}
	code, _, errOut := runCLI(t, newTestRoot(t, dir), "report", "--last", "--vault", dir)
	if code != ExitUsage {
		t.Fatalf("退出码 = %d，期望 1", code)
	}
	if !strings.Contains(errOut, "eg apply") {
		t.Fatalf("提示必须给出下一步动作：%s", errOut)
	}
	if !strings.Contains(errOut, "eg capture") {
		t.Fatalf("提示必须覆盖 capture 这一半入口（合同 §1.9）：%s", errOut)
	}
	if strings.Contains(errOut, "先执行一次 eg apply") {
		t.Fatalf("提示不得预设用户上一步应当 apply：%s", errOut)
	}
}

// I-…-018：一次成功的 `eg capture` 之后 `report --last` 必须退 0 并复现 §4.6 报告体
// （声明面覆盖 apply / capture，过去 capture 这一半入口不落记录 → 照文档调用直接失败）。
func TestReportLastReplaysLastCapture(t *testing.T) {
	dir := captureVault(t)
	env := captureOnce(t, dir, "--title", "demo", "--url", "https://example.com/demo",
		"--reason", "冒烟", "--captured-at", "2026-09-01T10:00:00+08:00")
	if _, ok := env.Data["report"]; ok {
		t.Fatalf("capture 的信封 data 键集是封闭的（§1.3），不得多出 report：%v", env.Data)
	}
	code, out, errOut := runCLI(t, newTestRoot(t, dir), "report", "--last", "--vault", dir, "--json")
	if code != ExitOK {
		t.Fatalf("capture 后 eg report --last 退出码 = %d（期望 0）：%s", code, errOut)
	}
	var replay Envelope
	if err := json.Unmarshal([]byte(out), &replay); err != nil {
		t.Fatalf("--json 输出不可解析：%v", err)
	}
	raw, err := json.Marshal(replay.Data["report"])
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("报告体不可解析：%v", err)
	}
	for _, k := range report.RequiredKeys() {
		if _, ok := body[k]; !ok {
			t.Fatalf("capture 的报告体缺 §4.6 必填键 %q：%s", k, raw)
		}
	}
	for _, k := range report.ForbiddenKeys() {
		if _, ok := body[k]; ok {
			t.Fatalf("报告体出现禁键 %q：%s", k, raw)
		}
	}
	src, _ := body["source"].(map[string]interface{})
	if got, want := src["id"], env.Data["source_id"]; got != want {
		t.Fatalf("report.source.id = %v，期望与该次 capture 的 data.source_id %v 逐字相同", got, want)
	}
	if got, want := src["path"], env.Data["path"]; got != want {
		t.Fatalf("report.source.path = %v，期望与该次 capture 的 data.path %v 逐字相同", got, want)
	}
}

// —— ⑦ 报告产物不落在知识目录下（EG-AGT-04）——

func TestReportArtifactIsNotAKnowledgeFile(t *testing.T) {
	dir := applyVault(t)
	if code, _, errOut := runApplyPlan(t, dir, notePlan()); code != ExitOK {
		t.Fatalf("退出码 = %d：%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(LastReportFile))); err != nil {
		t.Fatalf("最近一次报告应落在 %s：%v", LastReportFile, err)
	}
	if strings.HasPrefix(LastReportFile, "domains/") || strings.HasPrefix(LastReportFile, "sources/") {
		t.Fatalf("报告不是知识产物，不得落在 domains/** 或 sources/** 下：%s", LastReportFile)
	}
	for _, sub := range []string{"domains", store.DirSources} {
		err := filepath.Walk(filepath.Join(dir, sub), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.Contains(info.Name(), "report") {
				return os.ErrExist
			}
			return nil
		})
		if err != nil {
			t.Fatalf("%s 下不得出现报告产物", sub)
		}
	}
	// 报告落盘后工作区仍应干净（状态目录已登记进本地忽略清单）。
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("报告落盘不得污染工作区，git status = %q", got)
	}
}

// —— ⑧ 退 4 时报告的必备事实（§4.6 + B4）——

func TestReportOnCommitFailureKeepsFacts(t *testing.T) {
	dir := applyVault(t)
	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, _ := runApplyPlanWith(t, r, dir, notePlan())
	if code != ExitCommitFailed || env.Status != StatusPartial {
		t.Fatalf("exit_code = %d / status = %q，期望 4 / partial", code, env.Status)
	}
	rep := applyReport(t, env)
	if rep.Git.Commit != nil {
		t.Fatal("git.commit 必须为 null")
	}
	if len(rep.Links) == 0 {
		t.Fatal("已写文件必须如实列出在 links[]")
	}
	joined := strings.Join(rep.Lines(), "\n")
	if !strings.Contains(joined, "未提交清单") || !strings.Contains(joined, "未做任何还原") {
		t.Fatalf("报告必须含未提交清单与「不回滚」说明：%s", joined)
	}
	for _, k := range report.RequiredKeys() {
		if !applyReportKeys(t, env)[k] {
			t.Fatalf("失败路径的报告同样必须齐备，缺 %q", k)
		}
	}
}
