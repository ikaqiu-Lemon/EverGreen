package cli

// T-…-015 的验收用例：eg apply 的编排、部分成功与退出码 0/2/3/4。
// 用例名以 Apply 开头，可用 `go test ./internal/cli/... -run Apply` 单独跑。
//
// 全部用例都走真实的 store 写口与真实 git 仓：不打桩写入、不打桩校验，
// 唯一被注入的是「时间」与「Git Runner」（提交失败分支）——保证确定性且零网络。

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/report"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

const (
	applySourceID = "s-20260901-demo"
	applyNoteID   = "n-20260901-demo"
	applyCardID   = "k-20260901-attention"
	applyCard2ID  = "k-20260901-context"
)

// applyVault 建一个含一篇原文的干净 vault（原文经 eg capture 真实收录）。
func applyVault(t *testing.T) string {
	t.Helper()
	dir := captureVault(t)
	captureOnce(t, dir, "--title", "demo", "--url", "https://example.com/demo",
		"--reason", "冒烟", "--captured-at", "2026-09-01T10:00:00+08:00")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：工作区必须干净，得到 %q", got)
	}
	return dir
}

// runApplyPlan 用 stdin 投一份 plan，返回退出码、--json 信封与 stderr。
func runApplyPlan(t *testing.T, dir, plan string, extra ...string) (int, Envelope, string) {
	t.Helper()
	return runApplyPlanWith(t, newTestRoot(t, dir), dir, plan, extra...)
}

func runApplyPlanWith(t *testing.T, r *Root, dir, plan string, extra ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r.In = strings.NewReader(plan)
	r.Now = func() time.Time { return captureAt(t) }
	args := append([]string{"apply", "--vault", dir, "--json", "--plan", "-"}, extra...)
	code, out, errOut := runCLI(t, r, args...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

// applyReport 从信封里取回报告体。
func applyReport(t *testing.T, env Envelope) report.Report {
	t.Helper()
	raw, err := json.Marshal(env.Data["report"])
	if err != nil {
		t.Fatalf("report 不可序列化：%v", err)
	}
	var rep report.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("report 不可解析：%v\n%s", err, raw)
	}
	return rep
}

func applyReportKeys(t *testing.T, env Envelope) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(env.Data["report"])
	if err != nil {
		t.Fatalf("report 不可序列化：%v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("report 不可解析：%v", err)
	}
	out := map[string]bool{}
	for k := range generic {
		out[k] = true
	}
	return out
}

func gitLogCount(t *testing.T, dir string) int {
	t.Helper()
	out := strings.TrimSpace(gitOut(t, dir, "log", "--oneline"))
	if out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

func hashOf(t *testing.T, dir, rel string) string {
	t.Helper()
	return store.ContentHash(mustRead(t, filepath.Join(dir, filepath.FromSlash(rel))))
}

// —— plan 语料 ——

func notePlan() string {
	return `{"plan_version":1,"verb":"process","domain":"ai-infra",
"reason":"冒烟","requirement_ids":["EG-AGT-03"],
"ops":[{"op":"write_note","source":"` + applySourceID + `","note_id":"` + applyNoteID + `",
"title":"demo","sections":{"材料提炼":"提炼要点一。"}}]}`
}

func cardPlan(cardID string, extra string) string {
	return `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"建卡",
"requirement_ids":["EG-AGT-03"],
"ops":[{"op":"create_card","card_id":"` + cardID + `","title":"注意力机制"` + extra + `,
"sources":[{"source":"` + applySourceID + `","note":"` + applyNoteID + `","rel":"support","reason":"原文给出定义"}],
"sections":{"知识内容":"注意力是一种加权求和。"}}]}`
}

// applyNoteAndCard 先落一篇笔记与一张卡，返回二者的相对路径。
func applyNoteAndCard(t *testing.T, dir string) (string, string) {
	t.Helper()
	code, _, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitOK {
		t.Fatalf("落笔记退出码 = %d：%s", code, errOut)
	}
	code, _, errOut = runApplyPlan(t, dir, cardPlan(applyCardID, ""))
	if code != ExitOK {
		t.Fatalf("落卡退出码 = %d：%s", code, errOut)
	}
	return store.NoteRel("ai-infra", applyNoteID), store.CardRel("ai-infra", applyCardID)
}

// —— ① 成功路径：退 0、恰一条 commit、commit 文件集合 == links[] ——

func TestApplySuccessCommitEqualsLinks(t *testing.T) {
	dir := applyVault(t)
	before := gitLogCount(t, dir)

	code, env, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	if got := gitLogCount(t, dir); got != before+1 {
		t.Fatalf("commit 数 = %d，期望 %d（一次 apply = 一次 commit）", got, before+1)
	}
	subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
	if !strings.HasPrefix(subject, "process(ai-infra): ") {
		t.Fatalf("commit 主题 = %q，期望 <verb>(<domain>): … 形状", subject)
	}
	rep := applyReport(t, env)
	if len(rep.Links) == 0 {
		t.Fatal("links[] 为空：实际写入文件必须如实列出")
	}
	files := strings.Fields(gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD"))
	if strings.Join(sortedCopy(files), ",") != strings.Join(sortedCopy(rep.Links), ",") {
		t.Fatalf("干净工作区下 commit 文件集合 %v 应等于报告 links[] %v", files, rep.Links)
	}
	if rep.Git.Commit == nil || *rep.Git.Commit == "" {
		t.Fatal("git.commit 应为本次 commit 的 sha")
	}
	if rep.Note.ID != applyNoteID {
		t.Fatalf("note.id = %q，期望 %q", rep.Note.ID, applyNoteID)
	}
}

func sortedCopy(in []string) []string {
	out := append([]string{}, in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// —— ② 校验失败：退 2、零写入、无 commit、报告仍产出且含错误项 ——

func TestApplyValidationFailureIsZeroWrite(t *testing.T) {
	dir := applyVault(t)
	// 五份基线全部在**执行前**抓：夹具 applyVault 走真实 `eg capture`，而 M6 · T-…-072
	// 批次 C1 起 capture 自己就是一笔完整事务，`.index/txn/<id>/` 与 `.index/run.lock`
	// 都是夹具留下的**合法历史**。「零写入」因此只能按增量读：不是「运行时目录里空无一物」，
	// 而是「被测这一次一个字节都没添、没改」。
	before := gitLogCount(t, dir)
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")
	snapshot := treeSnapshot(t, dir)
	authBefore := authoritySnapshot(t, dir)
	txnsBefore := txnIDsOn(t, dir)
	indexBefore := indexTreeSnapshot(t, dir)
	lockBefore := lockIdentity(t, dir)

	// E2：关系 target 不可解析（悬空引用）。
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","ops":[
{"op":"add_relation","from":"k-20260901-nope","type":"supports","target":"k-20260901-nada","reason":"x"}]}`
	code, env, _ := runApplyPlan(t, dir, plan)
	if code != ExitValidation {
		t.Fatalf("退出码 = %d，期望 2（校验失败零写入）", code)
	}
	if got := gitLogCount(t, dir); got != before {
		t.Fatalf("commit 数 = %d，期望不变 %d", got, before)
	}
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("校验失败不得移动 HEAD：%q → %q", headBefore, got)
	}
	// 知识内容逐字节不变。
	assertAuthorityUnchanged(t, dir, authBefore, "校验失败")
	// 运行时目录逐维度反证：不开新事务、既有事务日志不变、`.index/` 下零新增产物，
	// 锁文件还是同一个 inode（合同 §2 先取锁后校验 ⇒ 允许、且仅允许它的**正文**被重写）。
	assertNoNewTxn(t, dir, txnsBefore, "校验失败")
	assertTxnTreeUnchanged(t, dir, indexBefore, "校验失败")
	assertIndexTreeUnchanged(t, dir, indexBefore, "校验失败")
	assertLockInodeStable(t, dir, lockBefore, "校验失败")
	// 兜底：全树（含 .git 之外的一切，`.eg/` 也在内）唯一允许的变化就是锁正文那一条。
	onlyLockBody := "改写 " + txn.IndexDirName + "/" + txn.LockFileName
	var unexpected []string
	for _, d := range diffSnapshots(snapshot, treeSnapshot(t, dir)) {
		if d != onlyLockBody {
			unexpected = append(unexpected, d)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("校验失败除运行时锁正文外必须零写入，实际多出：%s", strings.Join(unexpected, "；"))
	}
	if _, ok := env.Data["report"]; !ok {
		t.Fatal("退 2 时报告仍必须产出")
	}
	if env.Data["errors"] == nil {
		t.Fatal("data.errors[] 必须含错误项")
	}
}

// treeSnapshot / treeDiff 用于「零写入」的字节级反证。
func treeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = store.ContentHash(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("快照失败：%v", err)
	}
	return out
}

func treeDiff(t *testing.T, dir string, before map[string]string) string {
	t.Helper()
	after := treeSnapshot(t, dir)
	var diffs []string
	for rel, hash := range after {
		if old, ok := before[rel]; !ok {
			diffs = append(diffs, "新增 "+rel)
		} else if old != hash {
			diffs = append(diffs, "改写 "+rel)
		}
	}
	for rel := range before {
		if _, ok := after[rel]; !ok {
			diffs = append(diffs, "删除 "+rel)
		}
	}
	return strings.Join(sortedCopy(diffs), "；")
}

// —— ③ 部分成功：file_changed → 退 3，其余 op 照常写入并已提交 ——

func TestApplyPartialSkipsFileChanged(t *testing.T) {
	dir := applyVault(t)
	noteRel, cardRel := applyNoteAndCard(t, dir)
	oldHash := hashOf(t, dir, cardRel)

	// 外部改动该卡（模拟 eg context 之后被人手工编辑）。
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	raw := mustRead(t, cardAbs)
	edited := append(append([]byte{}, raw...), "\n用户手工补的一行。\n"...)
	if err := os.WriteFile(cardAbs, edited, 0o644); err != nil {
		t.Fatalf("外部改动失败：%v", err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "-c", "user.name=eg-test", "-c", "user.email=eg-test@example.com",
		"commit", "-q", "-m", "外部改动")
	before := gitLogCount(t, dir)
	cardBytes := mustRead(t, cardAbs)

	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"部分成功",
"base":{"` + applyCardID + `":"` + oldHash + `","` + applyNoteID + `":"` + hashOf(t, dir, noteRel) + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `","sections":{"解释与依据":"补一条依据。"}},
{"op":"add_open_question","note":"` + applyNoteID + `","question":"这条结论在小样本下成立吗？"}]}`
	code, env, _ := runApplyPlan(t, dir, plan)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（部分成功）", code)
	}
	rep := applyReport(t, env)
	if len(rep.Skipped) != 1 {
		t.Fatalf("skipped[] 长度 = %d，期望 1：%+v", len(rep.Skipped), rep.Skipped)
	}
	s := rep.Skipped[0]
	if s.Kind != string(store.SkipFileChanged) || s.Cause != "content_hash_mismatch" {
		t.Fatalf("skipped[0] = %+v，期望 kind=file_changed / cause=content_hash_mismatch", s)
	}
	if s.Detail == "" {
		t.Fatalf("detail 必须是人类可读文本：%q", s.Detail)
	}
	if got := store.ContentHash(mustRead(t, cardAbs)); got != store.ContentHash(cardBytes) {
		t.Fatal("被跳过的卡必须字节不变")
	}
	if !strings.Contains(string(mustRead(t, filepath.Join(dir, filepath.FromSlash(noteRel)))),
		"这条结论在小样本下成立吗？") {
		t.Fatal("其余 op 必须照常写入")
	}
	if got := gitLogCount(t, dir); got != before+1 {
		t.Fatalf("commit 数 = %d，期望 %d（已写内容保留并已提交）", got, before+1)
	}
	if rep.Git.Commit == nil {
		t.Fatal("部分成功仍然产生 commit，git.commit 不应为 null")
	}
}

// —— ④ 第二形态：用户块无法逐字保留 → user_block_unsafe ——

func TestApplyPartialSkipsUserBlockUnsafe(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))

	// 该卡出现两个「用户补充」分区：用户内容归属不可确定，写入路径必须拒绝并如实上报。
	raw := string(mustRead(t, cardAbs))
	raw += "\n## 用户补充\n\n我自己写的第二段，必须逐字保留。\n"
	if err := os.WriteFile(cardAbs, []byte(raw), 0o644); err != nil {
		t.Fatalf("构造用户块失败：%v", err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "-c", "user.name=eg-test", "-c", "user.email=eg-test@example.com",
		"commit", "-q", "-m", "用户补充")
	kept := mustRead(t, cardAbs)

	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"用户块",
"base":{"` + applyCardID + `":"` + hashOf(t, dir, cardRel) + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `","sections":{"条件与边界":"仅在长序列下成立。"}}]}`
	code, env, _ := runApplyPlan(t, dir, plan)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3", code)
	}
	rep := applyReport(t, env)
	if len(rep.Skipped) != 1 || rep.Skipped[0].Kind != string(store.SkipUserBlockUnsafe) ||
		rep.Skipped[0].Cause != "user_block_not_preserved" {
		t.Fatalf("skipped[] = %+v，期望 kind=user_block_unsafe / cause=user_block_not_preserved", rep.Skipped)
	}
	if store.ContentHash(mustRead(t, cardAbs)) != store.ContentHash(kept) {
		t.Fatal("跳过的文件字节必须不变")
	}
}

// —— ⑤ B4：commit 失败 → 退 4，已写文件保持写入后状态，无删除无还原 ——

func TestApplyCommitFailureKeepsWrittenBytes(t *testing.T) {
	dir := applyVault(t)
	before := gitLogCount(t, dir)

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, _ := runApplyPlanWith(t, r, dir, notePlan())
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4", code)
	}
	rep := applyReport(t, env)
	if rep.Git.Commit != nil {
		t.Fatalf("提交失败时 git.commit 必须为 null，实得 %v", *rep.Git.Commit)
	}
	if len(rep.Links) == 0 {
		t.Fatal("已写文件仍须如实列出在 links[]")
	}
	for _, rel := range rep.Links {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("已写文件 %s 不得被删除或还原：%v", rel, err)
		}
	}
	if got := gitLogCount(t, dir); got != before {
		t.Fatalf("提交失败不应产生 commit，log 数 = %d", got)
	}
	joined := strings.Join(warningMessages(env.Warnings), "\n")
	if !strings.Contains(joined, "未提交清单") || !strings.Contains(joined, "注入的提交失败") {
		t.Fatalf("报告必须含未提交清单与失败原因：%s", joined)
	}
}

// —— ⑥ 跨领域 W1：照常写入、进 warnings[]、退出码不变 ——

func TestApplyCrossDomainIsWarningNotError(t *testing.T) {
	dir := applyVault(t)
	applyNoteAndCard(t, dir)

	code, env, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, `,"domain":"product"`))
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（W1 不影响退出码）：%s", code, errOut)
	}
	rep := applyReport(t, env)
	found := false
	for _, w := range rep.Warnings {
		if w.Code == "W1" && w.OpIndex == 0 && strings.Contains(w.Path, "ops[0]") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings[] 必须含一条带 op 下标与字段路径的 W1：%+v", rep.Warnings)
	}
	rel := store.CardRel("product", applyCard2ID)
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("W1 目标必须照常写入：%v", err)
	}
	if len(rep.Links) == 0 {
		t.Fatal("非零写入")
	}
}

// —— ⑦ --dry-run：零写入、零 commit ——

func TestApplyDryRunWritesNothing(t *testing.T) {
	dir := applyVault(t)
	before := gitLogCount(t, dir)
	snapshot := treeSnapshot(t, dir)

	code, env, errOut := runApplyPlan(t, dir, notePlan(), "--dry-run")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	if diff := treeDiff(t, dir, snapshot); diff != "" {
		t.Fatalf("--dry-run 必须零写入，实际变化：%s", diff)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("--dry-run 后 git status 必须为空，得到 %q", got)
	}
	if got := gitLogCount(t, dir); got != before {
		t.Fatalf("--dry-run 不得产生 commit，log 数 = %d", got)
	}
	if env.Data["planned"] == nil {
		t.Fatal("--dry-run 必须输出 planned[]（path / section / op_index）")
	}
	rep := applyReport(t, env)
	if rep.Git.Commit != nil {
		t.Fatal("--dry-run 的 git.commit 必须为空值")
	}
}

// —— ⑦-b I-…-002：--dry-run 的报告体计数与 planned[] / 正式执行同源 ——
//
// 缺陷形态：`applyDryRun()` 不填 `rep.Cards.Created`，却照打零卡说明 ——
// 同一次输出里 `planned[]` 已列出新卡分区，报告体却写「新建 0 张 / 本次未产生知识卡」，
// 正式 apply 则写「新建 1 张」。本用例把三处钉成同一个事实：
//
//	① 计划建卡时 dry-run 的 cards.created 非空且**不得**再打零卡说明；
//	② 同一份 plan 的 dry-run 与正式执行 cards.created / cards.updated 逐字相等；
//	③ 只补充既有卡（append_card）时两侧**同样**都打零卡说明 —— 不是把说明删掉，
//	   而是让它只在「确实没有新卡」时出现（EG-KNW-05 的零卡合法语义原样保留）；
//	④ 计数只读 pres.Actions，dry-run 的零写入零 commit 底线一格不动。
func TestApplyDryRunReportCountsMatchPlanned(t *testing.T) {
	dir := applyVault(t)
	if code, _, errOut := runApplyPlan(t, dir, notePlan()); code != ExitOK {
		t.Fatalf("落笔记退出码 = %d：%s", code, errOut)
	}
	plan := cardPlan(applyCardID, "")

	// ① dry-run：计划新建 1 张 → 计数非空、零卡说明消失，且仍零写入零 commit。
	before := gitLogCount(t, dir)
	snapshot := treeSnapshot(t, dir)
	code, dryEnv, errOut := runApplyPlan(t, dir, plan, "--dry-run")
	if code != ExitOK {
		t.Fatalf("--dry-run 退出码 = %d，期望 0：%s", code, errOut)
	}
	dryRep := applyReport(t, dryEnv)
	if len(dryRep.Cards.Created) != 1 || dryRep.Cards.Created[0] != applyCardID {
		t.Fatalf("dry-run 的 cards.created = %v，期望恰 [%s]（与 planned[] 同源）",
			dryRep.Cards.Created, applyCardID)
	}
	if lines := strings.Join(dryRep.Lines(), "\n"); strings.Contains(lines, report.NoCardNotice) {
		t.Fatalf("计划新建卡时 dry-run 不得再打零卡说明：\n%s", lines)
	}
	for _, w := range dryRep.Warnings {
		if strings.Contains(w.Message, report.NoCardNotice) {
			t.Fatalf("计划新建卡时 dry-run 的 warnings[] 不得再含零卡说明：%+v", w)
		}
	}
	if diff := treeDiff(t, dir, snapshot); diff != "" {
		t.Fatalf("--dry-run 必须零写入，实际变化：%s", diff)
	}
	if got := gitLogCount(t, dir); got != before {
		t.Fatalf("--dry-run 不得产生 commit，log 数 = %d（期望 %d）", got, before)
	}

	// ② 正式执行同一份 plan：两条源的卡账本逐字相等。
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitOK {
		t.Fatalf("正式执行退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	assertSameList(t, "cards.created", dryRep.Cards.Created, rep.Cards.Created)
	assertSameList(t, "cards.updated", dryRep.Cards.Updated, rep.Cards.Updated)

	// ③ 只补充既有卡：dry-run 与正式执行都记 updated、都保留零卡说明。
	// （append_card 必须带 plan.base 的真 content_hash，否则按 B3 判 file_changed 跳过。）
	cardRel := store.CardRel("ai-infra", applyCardID)
	appendPlan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"补充",
"base":{"` + applyCardID + `":"` + hashOf(t, dir, cardRel) + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `",
"sections":{"解释与依据":"dry-run 与正式执行必须数出同一张补充卡。"}}]}`
	code, dryEnv2, errOut := runApplyPlan(t, dir, appendPlan, "--dry-run")
	if code != ExitOK {
		t.Fatalf("补充卡 --dry-run 退出码 = %d：%s", code, errOut)
	}
	dryRep2 := applyReport(t, dryEnv2)
	if len(dryRep2.Skipped) != 0 {
		t.Fatalf("补充卡 dry-run 不应有跳过：%+v", dryRep2.Skipped)
	}
	if len(dryRep2.Cards.Created) != 0 ||
		len(dryRep2.Cards.Updated) != 1 || dryRep2.Cards.Updated[0] != applyCardID {
		t.Fatalf("补充卡 dry-run 的 cards = %+v，期望 created 空、updated 恰 [%s]",
			dryRep2.Cards, applyCardID)
	}
	if lines := strings.Join(dryRep2.Lines(), "\n"); !strings.Contains(lines, report.NoCardNotice) {
		t.Fatalf("确实没有新卡时零卡说明必须保留（EG-KNW-05）：\n%s", lines)
	}
	code, env2, errOut := runApplyPlan(t, dir, appendPlan)
	if code != ExitOK {
		t.Fatalf("补充卡正式执行退出码 = %d：%s", code, errOut)
	}
	rep2 := applyReport(t, env2)
	assertSameList(t, "补充卡 cards.created", dryRep2.Cards.Created, rep2.Cards.Created)
	assertSameList(t, "补充卡 cards.updated", dryRep2.Cards.Updated, rep2.Cards.Updated)
	if lines := strings.Join(rep2.Lines(), "\n"); !strings.Contains(lines, report.NoCardNotice) {
		t.Fatalf("正式执行侧同一形态也必须保留零卡说明：\n%s", lines)
	}
}

// —— ⑧ 自检非前置（EG-CHK-05）——

func TestApplySelfCheckIsNotAGate(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	if !strings.Contains(string(mustRead(t, filepath.Join(dir, filepath.FromSlash(cardRel)))),
		"理解自检") {
		t.Skip("卡模板未含理解自检分区")
	}
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"自检非前置",
"base":{"` + applyCardID + `":"` + hashOf(t, dir, cardRel) + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `","sections":{"理解自检":"Q: 这条结论的边界是什么？"}}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（自检未作答不阻塞写入与提交）：%s", code, errOut)
	}
	for _, w := range applyReport(t, env).Warnings {
		if strings.Contains(w.Message, "自检") || strings.Contains(w.Path, "自检") {
			t.Fatalf("报告不得出现任何与自检相关的条目：%+v", w)
		}
	}
}

// —— ⑨ op 顺序敏感：先 create_card 后 append_card 同一张卡 ——

func TestApplyExecutesOpsInDeclaredOrder(t *testing.T) {
	dir := applyVault(t)
	code, _, errOut := runApplyPlan(t, dir, notePlan())
	if code != ExitOK {
		t.Fatalf("落笔记退出码 = %d：%s", code, errOut)
	}
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"顺序",
"ops":[{"op":"create_card","card_id":"` + applyCardID + `","title":"注意力机制",
"sources":[{"source":"` + applySourceID + `","note":"` + applyNoteID + `","rel":"support","reason":"原文给出定义"}],
"sections":{"知识内容":"注意力是一种加权求和。"}},
{"op":"append_card","card":"` + applyCardID + `","sections":{"解释与依据":"后一条 op 追加的依据。"}}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	body := string(mustRead(t, filepath.Join(dir,
		filepath.FromSlash(store.CardRel("ai-infra", applyCardID)))))
	if !strings.Contains(body, "后一条 op 追加的依据。") {
		t.Fatal("append_card 必须在 create_card 之后生效（顺序 = ops[] 声明顺序）")
	}
	rep := applyReport(t, env)
	if len(rep.Cards.Created) != 1 || len(rep.Cards.Updated) != 1 {
		t.Fatalf("cards = %+v，期望新建 1 张 + 补充 1 张", rep.Cards)
	}
}

// —— ⑩ 空 ops[]：退 0、干净工作区下无 commit ——

func TestApplyEmptyOpsProducesNoCommit(t *testing.T) {
	dir := applyVault(t)
	before := gitLogCount(t, dir)
	code, env, errOut := runApplyPlan(t, dir,
		`{"plan_version":1,"verb":"process","domain":"ai-infra","ops":[]}`)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	if got := gitLogCount(t, dir); got != before {
		t.Fatalf("空 ops[] 不得产生 commit，log 数 = %d（期望 %d）", got, before)
	}
	rep := applyReport(t, env)
	if len(rep.Links) != 0 || rep.Git.Commit != nil {
		t.Fatalf("零写入报告：links[] 应为空、git.commit 应为 null，实得 %+v / %v", rep.Links, rep.Git.Commit)
	}
	if !strings.Contains(strings.Join(rep.Lines(), "\n"), report.NoCardNotice) {
		t.Fatal("零写入报告必须判定「已完成（零写入）」并说明未产生知识卡")
	}
}

// —— ⑪ 报告体键集合封闭 + 跳过命名封闭（grep 反证） ——

func TestApplyReportKeysAreClosed(t *testing.T) {
	dir := applyVault(t)
	_, env, _ := runApplyPlan(t, dir, notePlan())
	keys := applyReportKeys(t, env)
	for _, k := range report.RequiredKeys() {
		if !keys[k] {
			t.Fatalf("报告体缺 §4.6 S1 必填键 %q", k)
		}
	}
	for _, k := range report.ForbiddenKeys() {
		if keys[k] {
			t.Fatalf("报告体不得出现自创键 %q", k)
		}
	}
}

func TestApplySkippedNamingIsClosed(t *testing.T) {
	// 禁用词以拼接构造，避免本文件自身成为 grep 反证的命中项。
	banned := []string{
		"block_" + "conflict", "block_hash_" + "changed", "st" + "ale",
		"intent" + ".json", "reset " + "--hard", "fl" + "ock", ".eg" + ".lock",
	}
	for _, name := range []string{"apply.go", "report.go"} {
		raw := string(mustRead(t, name))
		for _, word := range banned {
			if strings.Contains(raw, word) {
				t.Fatalf("%s 出现禁用命名 / S5 才有的机制 %q", name, word)
			}
		}
	}
	if store.CauseFor(store.SkipFileChanged) == "" || store.CauseFor(store.SkipUserBlockUnsafe) == "" {
		t.Fatal("kind → cause 一一对应表必须完整")
	}
}

// —— 测试脚手架：注入一个只在 commit 步骤失败的 Git Runner ——

var errFakeCommit = errors.New("注入的提交失败")

// failingCommitRepo 返回一个「只有 commit 这一步失败」的仓构造器：
// add / status 等仍走真实 git，因此磁盘与索引状态是真的，B4 断言才有意义。
func failingCommitRepo() func(root string) *git.Repo {
	return func(root string) *git.Repo {
		return git.NewWithRunner(root, func(d string, args ...string) ([]byte, []byte, error) {
			if len(args) > 0 && args[0] == "commit" {
				return nil, []byte(errFakeCommit.Error()), errFakeCommit
			}
			return realGit(d, args...)
		})
	}
}

func realGit(dir string, args ...string) ([]byte, []byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
