package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

func runStorageV3CLI(t *testing.T, r *Root, args ...string) (int, Envelope, string) {
	t.Helper()
	code, out, errOut := runCLI(t, r, args...)
	var env Envelope
	if out != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("JSON 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, errOut
}

func TestMaterializeCommandRealKnowledgeOpinionClosure(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	before := mustRead(t, notePath)
	beforeCandidates, err := mdfile.ParseCandidates(before)
	if err != nil {
		t.Fatal(err)
	}
	commitsBefore := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD"))

	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 9, 22, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	code, env, errOut := runStorageV3CLI(t, root,
		"materialize", "--vault", dir, "--json", "--note",
		"n-20260922-txn-materialize", "--all", "--user-request")
	if code != ExitOK {
		t.Fatalf("materialize 退出码=%d：%s %+v", code, errOut, env)
	}
	items, ok := env.Data["materialized_candidates"].([]interface{})
	if !ok || len(items) != 2 || env.Data["finalized"] != true {
		t.Fatalf("materialize 回执不完整：%+v", env.Data)
	}
	kPath := filepath.Join(dir, "domains/ai-infra/knowledge/k-20260922-txn-knowledge.md")
	oPath := filepath.Join(dir, "domains/ai-infra/opinions/o-20260922-txn-opinion.md")
	if got := mustRead(t, kPath); !bytes.Contains(got, []byte("## 知识内容\n\n事务知识。\n")) {
		t.Fatalf("Knowledge 未逐字承接 candidate payload：\n%s", got)
	}
	if got := mustRead(t, oPath); !bytes.Contains(got, []byte("validation: 'pending'")) ||
		!bytes.Contains(got, []byte("## 观点\n\n事务观点。\n")) {
		t.Fatalf("Opinion 未以 pending 逐字承接 candidate payload：\n%s", got)
	}
	after := mustRead(t, notePath)
	afterCandidates, err := mdfile.ParseCandidates(after)
	if err != nil {
		t.Fatal(err)
	}
	for i := range beforeCandidates {
		if !bytes.Equal(beforeCandidates[i].Raw(before), afterCandidates[i].Raw(after)) {
			t.Fatalf("candidate %s 的 H3/payload 被物化改写", beforeCandidates[i].Key)
		}
	}
	if commitsAfter := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD")); commitsAfter == commitsBefore {
		t.Fatal("首次 materialize 必须产生 Git commit")
	}

	root.Now = func() time.Time {
		return time.Date(2026, 9, 30, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	beforeReplay := gitOut(t, dir, "rev-list", "--count", "HEAD")
	code, replay, errOut := runStorageV3CLI(t, root,
		"materialize", "--vault", dir, "--json", "--note",
		"n-20260922-txn-materialize", "--all", "--user-request")
	if code != ExitOK {
		t.Fatalf("跨日重跑失败：%d %s %+v", code, errOut, replay)
	}
	if replay.Data["txn_id"] != "" || replay.Data["commit"] != nil {
		t.Fatalf("跨日重跑必须是零 txn / 零 commit no-op：%+v", replay.Data)
	}
	if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != beforeReplay {
		t.Fatal("跨日重跑产生了空 commit")
	}
}

func TestMaterializeCommandAuthorizationAndSelection(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	before := mustRead(t, filepath.Join(dir, filepath.FromSlash(noteRel)))
	cases := [][]string{
		{"materialize", "--vault", dir, "--json", "--note",
			"n-20260922-txn-materialize", "--all"},
		{"materialize", "--vault", dir, "--json", "--note",
			"n-20260922-txn-materialize", "--all", "--candidate", "cand-k", "--user-request"},
		{"materialize", "--vault", dir, "--json", "--note",
			"bad", "--all", "--user-request"},
	}
	want := []int{ExitValidation, ExitUsage, ExitUsage}
	for i, args := range cases {
		code, _, _ := runStorageV3CLI(t, newTestRoot(t, dir), args...)
		if code != want[i] {
			t.Fatalf("case %d exit=%d want=%d", i, code, want[i])
		}
		if got := mustRead(t, filepath.Join(dir, filepath.FromSlash(noteRel))); !bytes.Equal(got, before) {
			t.Fatalf("case %d 拒绝后改写了 Note", i)
		}
	}
}

func TestExportPlainWritesOutsideVaultAndPreservesVisibleContent(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	beforeVault := snapshot(t, dir)
	beforeLog := gitOut(t, dir, "log", "--oneline")
	out := filepath.Join(t.TempDir(), "plain")
	code, env, errOut := runStorageV3CLI(t, newTestRoot(t, dir),
		"export", "--vault", dir, "--json", "--plain", "--output", out)
	if code != ExitOK {
		t.Fatalf("export 退出码=%d：%s %+v", code, errOut, env)
	}
	exported := mustRead(t, filepath.Join(out, filepath.FromSlash(noteRel)))
	for _, forbidden := range []string{
		"<!-- eg:nr:", "<!-- eg:cd:", "<!-- eg:cc:", "<!-- eg:nc:",
		".eg-candidate", "#cand-",
	} {
		if bytes.Contains(exported, []byte(forbidden)) {
			t.Fatalf("plain export 残留 %q：\n%s", forbidden, exported)
		}
	}
	for _, visible := range []string{
		"知识来源。", "观点来源。", "### Txn Knowledge",
		"#### 知识内容", "事务知识。", "### Txn Opinion", "事务观点。",
	} {
		if !bytes.Contains(exported, []byte(visible)) {
			t.Fatalf("plain export 丢失可见内容 %q：\n%s", visible, exported)
		}
	}
	if after := snapshot(t, dir); after != beforeVault {
		t.Fatalf("export 改动了 vault：\n前=%s\n后=%s", beforeVault, after)
	}
	if got := gitOut(t, dir, "log", "--oneline"); got != beforeLog {
		t.Fatal("export 产生了 Git commit")
	}
	if _, err := os.Stat(filepath.Join(out, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("plain export 只导出知识数据，不应复制运行规程 SKILL.md")
	}
}

func TestExportPlainExcludesNoteSegmentationWorkspaces(t *testing.T) {
	dir, noteRel, workspaceRel := materializeWorkspaceFixture(t)
	out := filepath.Join(t.TempDir(), "plain")
	code, _, errOut := runStorageV3CLI(t, newTestRoot(t, dir),
		"export", "--vault", dir, "--json", "--plain", "--output", out)
	if code != ExitOK {
		t.Fatalf("export 失败：%d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(noteRel))); err != nil {
		t.Fatalf("pure Note 应被导出：%v", err)
	}
	if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(workspaceRel))); !os.IsNotExist(err) {
		t.Fatalf("note-segments 默认不得进入 plain export：%v", err)
	}
}

func TestExportPlainRejectsUnsafeOutputs(t *testing.T) {
	dir, _, _ := initVault(t, "--domain", "ai-infra")
	nonempty := filepath.Join(t.TempDir(), "nonempty")
	if err := os.MkdirAll(nonempty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonempty, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		filepath.Join(dir, "plain"), nonempty, link,
	} {
		code, _, _ := runStorageV3CLI(t, newTestRoot(t, dir),
			"export", "--vault", dir, "--json", "--plain", "--output", output)
		if code != ExitUsage {
			t.Fatalf("不安全输出 %s 应退 1，实得 %d", output, code)
		}
	}
}

func TestCandidateShowApplyCRUDAndMaterializeLogicalSlug(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	before := mustRead(t, notePath)
	beforeDoc, err := mdfile.Parse(before)
	if err != nil {
		t.Fatal(err)
	}
	beforeBody, _ := beforeDoc.Section(mdfile.SecNoteBody)
	beforeOpen, _ := beforeDoc.Section(mdfile.SecOpenQuest)
	beforeUser, _ := beforeDoc.Section(mdfile.SecUserAppend)
	commitsBefore := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD"))

	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 9, 29, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	code, shown, errOut := runStorageV3CLI(t, root,
		"candidate", "show", "--vault", dir, "--json",
		"--note", "n-20260922-txn-materialize")
	if code != ExitOK {
		t.Fatalf("candidate show 退出码=%d：%s %+v", code, errOut, shown)
	}
	if shown.Data["schema_version"] != float64(1) && shown.Data["schema_version"] != 1 {
		t.Fatalf("candidate show schema_version 错误：%+v", shown.Data)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD")); got != commitsBefore {
		t.Fatal("candidate show 不得产生 commit")
	}

	shownRaw, err := json.Marshal(shown.Data)
	if err != nil {
		t.Fatal(err)
	}
	var spec candidateReviewSpec
	if err := json.Unmarshal(shownRaw, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Candidates = []candidateReviewCandidate{
		{
			Key: "cand-renamed", Kind: store.CandidateKindKnowledge,
			LogicalSlug: "agent-development-boundaries", Title: "用户校准后的边界",
			SourceRefs: []string{"L1-L1", "L2-L2"}, Rel: "support",
			Reason: "用户扩大了来源范围", Tags: []string{"reviewed"},
			Sections: []candidateReviewSection{
				{Name: mdfile.SecKnowledge, Body: "用户确认后的完整知识。\n"},
				{Name: mdfile.SecBoundary, Body: "仅适用于已审阅范围。\n"},
			},
		},
		{
			Key: "cand-added", Kind: store.CandidateKindOpinion,
			LogicalSlug: "reviewed-claim", Title: "新增观点",
			SourceRefs: []string{"L2-L2"}, Rel: "context",
			Reason: "用户新增判断", Tags: []string{},
			Sections: []candidateReviewSection{
				{Name: mdfile.SecOpinionClaim, Body: "用户新增的可反驳主张。\n"},
				{Name: mdfile.SecToVerify, Body: "需要后续验证。\n"},
			},
		},
	}
	spec.Coverage = []candidateReviewCoverage{
		{
			Module: "m-reviewed", SourceRefs: []string{"L1-L1", "L2-L2"},
			Summary: "用户确认后的完整覆盖", Disposition: store.CandidateCoverageCandidate,
			Candidates: []string{"cand-renamed", "cand-added"}, Reason: "",
		},
	}
	reviewPath := filepath.Join(t.TempDir(), "review.json")
	raw, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewPath, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	code, applied, errOut := runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitOK {
		t.Fatalf("candidate apply 退出码=%d：%s %+v", code, errOut, applied)
	}
	if applied.Data["txn_id"] == "" || applied.Data["commit"] == nil {
		t.Fatalf("candidate apply 缺事务或 commit 回执：%+v", applied.Data)
	}
	after := mustRead(t, notePath)
	candidates, err := mdfile.ParseCandidates(after)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Key != "cand-renamed" ||
		candidates[0].Kind != mdfile.CandidateKindKnowledge ||
		candidates[0].LogicalSlug != "agent-development-boundaries" ||
		candidates[1].Key != "cand-added" {
		t.Fatalf("candidate CRUD 结果错误：%+v", candidates)
	}
	afterDoc, err := mdfile.Parse(after)
	if err != nil {
		t.Fatal(err)
	}
	afterBody, _ := afterDoc.Section(mdfile.SecNoteBody)
	afterOpen, _ := afterDoc.Section(mdfile.SecOpenQuest)
	afterUser, _ := afterDoc.Section(mdfile.SecUserAppend)
	for _, pair := range [][2][]byte{
		{before[beforeBody.Start:beforeBody.End], after[afterBody.Start:afterBody.End]},
		{before[beforeOpen.Start:beforeOpen.End], after[afterOpen.Start:afterOpen.End]},
		{before[beforeUser.Start:beforeUser.End], after[afterUser.Start:afterUser.End]},
	} {
		if !bytes.Equal(pair[0], pair[1]) {
			t.Fatal("candidate apply 改写了提取结果以外的 Note 分区")
		}
	}
	index, err := store.New(dir).ScanIDs()
	if err != nil {
		t.Fatal(err)
	}
	for id := range index.ByID {
		if strings.HasPrefix(id, "k-") || strings.HasPrefix(id, "o-") {
			t.Fatalf("candidate apply 不得提前物化产物：%s", id)
		}
	}

	code, fresh, errOut := runStorageV3CLI(t, root,
		"candidate", "show", "--vault", dir, "--json", "--note", spec.Note)
	if code != ExitOK {
		t.Fatalf("第二次 candidate show 失败：%d %s", code, errOut)
	}
	freshRaw, _ := json.Marshal(fresh.Data)
	if err := os.WriteFile(reviewPath, freshRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	commitsBeforeNoop := gitOut(t, dir, "rev-list", "--count", "HEAD")
	code, noop, errOut := runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitOK || noop.Data["txn_id"] != "" || noop.Data["commit"] != nil {
		t.Fatalf("相同 review spec 应 no-op：code=%d err=%s data=%+v", code, errOut, noop.Data)
	}
	if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != commitsBeforeNoop {
		t.Fatal("candidate apply no-op 产生了空 commit")
	}

	code, materialized, errOut := runStorageV3CLI(t, root,
		"materialize", "--vault", dir, "--json", "--note", spec.Note,
		"--all", "--user-request")
	if code != ExitOK {
		t.Fatalf("审阅后 materialize 失败：%d %s %+v", code, errOut, materialized)
	}
	items := materialized.Data["materialized_candidates"].([]interface{})
	first := items[0].(map[string]interface{})
	if first["output"] != "k-20260929-agent-development-boundaries" {
		t.Fatalf("materialize 未使用 logical_slug：%+v", first)
	}

	materializedNote := mustRead(t, notePath)
	spec.NoteHash = store.ContentHash(materializedNote)
	raw, _ = json.Marshal(spec)
	if err := os.WriteFile(reviewPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitValidation || !bytes.Equal(materializedNote, mustRead(t, notePath)) {
		t.Fatalf("已物化 candidate 应拒绝 review apply 且零写入，实际 code=%d", code)
	}
}

func TestCandidateWorkspaceApplyAndExplicitRebase(t *testing.T) {
	dir, noteRel, workspaceRel := materializeCanonicalWorkspaceFixture(t)
	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	workspacePath := filepath.Join(dir, filepath.FromSlash(workspaceRel))
	noteBefore := mustRead(t, notePath)
	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 9, 29, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}

	code, shown, errOut := runStorageV3CLI(t, root,
		"candidate", "show", "--vault", dir, "--json",
		"--note", "n-20260922-workspace")
	if code != ExitOK {
		t.Fatalf("workspace show 失败：%d %s %+v", code, errOut, shown)
	}
	if shown.Data["schema_version"] != float64(2) ||
		shown.Data["workspace"] != "ns-20260922-workspace" ||
		shown.Data["stale"] != false {
		t.Fatalf("workspace show schema/freshness 错误：%+v", shown.Data)
	}
	shownRaw, _ := json.Marshal(shown.Data)
	var spec candidateReviewSpec
	if err := json.Unmarshal(shownRaw, &spec); err != nil {
		t.Fatal(err)
	}
	spec.Candidates[0].Sections[0].Body = "用户优化后的工作区知识。\n"
	reviewPath := filepath.Join(t.TempDir(), "workspace-review.json")
	reviewRaw, _ := json.Marshal(spec)
	if err := os.WriteFile(reviewPath, reviewRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitOK {
		t.Fatalf("workspace apply 失败：%d %s", code, errOut)
	}
	if got := mustRead(t, notePath); !bytes.Equal(got, noteBefore) {
		t.Fatal("candidate apply 改写了 n-*")
	}
	workspaceAfter := mustRead(t, workspacePath)
	if !bytes.Contains(workspaceAfter, []byte("用户优化后的工作区知识。")) ||
		!bytes.Contains(workspaceAfter, []byte("用户保留文字。")) {
		t.Fatalf("candidate apply 未保留/应用 ns-* 用户内容：\n%s", workspaceAfter)
	}
	manifestBefore, found, err := mdfile.ParseNoteBlockManifest(workspaceAfter)
	if err != nil || !found {
		t.Fatalf("canonical workspace 缺旧 block manifest：found=%v err=%v", found, err)
	}

	changedNote := bytes.Replace(noteBefore,
		[]byte("知识来源。\n\n> **[Agent 补充]** 观点批注。"),
		[]byte("修订后的知识来源。\n\n> **[Agent 补充]** 观点批注。\n\n"+
			"> **[Agent 强调]** 新增判断。"), 1)
	if err := os.WriteFile(notePath, changedNote, 0o644); err != nil {
		t.Fatal(err)
	}
	code, staleView, errOut := runStorageV3CLI(t, root,
		"candidate", "show", "--vault", dir, "--json", "--note", spec.Note)
	if code != ExitOK || staleView.Data["stale"] != true {
		t.Fatalf("stale workspace 必须仍可 show：code=%d err=%s data=%+v",
			code, errOut, staleView.Data)
	}
	staleRaw, _ := json.Marshal(staleView.Data)
	if err := os.WriteFile(reviewPath, staleRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitValidation {
		t.Fatalf("stale workspace 无 --rebase 应退 2，实得 %d", code)
	}
	if got := mustRead(t, workspacePath); !bytes.Equal(got, workspaceAfter) {
		t.Fatal("stale 拒绝路径改写了 ns-*")
	}

	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--rebase", "--user-request")
	if code != ExitValidation {
		t.Fatalf("缺新 note_blocks 的 rebase 应退 2，实得 %d", code)
	}
	if got := mustRead(t, workspacePath); !bytes.Equal(got, workspaceAfter) {
		t.Fatal("缺新 note_blocks 的 rebase 改写了 ns-*")
	}

	var rebaseSpec candidateReviewSpec
	if err := json.Unmarshal(staleRaw, &rebaseSpec); err != nil {
		t.Fatal(err)
	}
	rebaseSpec.NoteBlocks = []candidateReviewNoteBlock{
		{
			Role: store.NoteBlockSource, Body: "知识来源。",
			SourceRef: "L1-L1",
		},
		{
			Role: store.NoteBlockAgent, Body: "观点批注。",
			Annotation: "supplement",
		},
		{
			Role: store.NoteBlockAgent, Body: "新增判断。",
			Annotation: "emphasis",
		},
	}
	rebaseSpec.Omissions = []candidateReviewOmission{}
	rebaseRaw, _ := json.Marshal(rebaseSpec)
	if err := os.WriteFile(reviewPath, rebaseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--rebase", "--user-request")
	if code != ExitValidation {
		t.Fatalf("不能重渲染当前正文的 note_blocks 应退 2，实得 %d", code)
	}
	if got := mustRead(t, workspacePath); !bytes.Equal(got, workspaceAfter) {
		t.Fatal("非法 note_blocks 的 rebase 改写了 ns-*")
	}

	rebaseSpec.NoteBlocks[0].Body = "修订后的知识来源。"
	rebaseRaw, _ = json.Marshal(rebaseSpec)
	if err := os.WriteFile(reviewPath, rebaseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--rebase", "--user-request")
	if code != ExitValidation {
		t.Fatalf("新 B3 未进入 coverage 的 rebase 应退 2，实得 %d", code)
	}
	if got := mustRead(t, workspacePath); !bytes.Equal(got, workspaceAfter) {
		t.Fatal("coverage 有缺口的 rebase 改写了 ns-*")
	}

	rebaseSpec.Coverage = append(rebaseSpec.Coverage, candidateReviewCoverage{
		Module: "m-new", NoteRefs: []string{"B3"}, Summary: "新增判断",
		Disposition: store.CandidateCoverageNoteOnly,
		Reason:      "新增批注只保留在 Note",
	})
	rebaseRaw, _ = json.Marshal(rebaseSpec)
	if err := os.WriteFile(reviewPath, rebaseRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--rebase", "--user-request")
	if code != ExitOK {
		t.Fatalf("显式 rebase 失败：%d %s", code, errOut)
	}
	rebased := mustRead(t, workspacePath)
	_, workspace, err := mdfile.ParseNoteSegmentation(rebased)
	if err != nil {
		t.Fatal(err)
	}
	if workspace.NoteHash != store.ContentHash(changedNote) ||
		!bytes.Contains(rebased, []byte("用户保留文字。")) {
		t.Fatalf("rebase 未推进 hash 或破坏用户补充：%+v\n%s", workspace, rebased)
	}
	manifestAfter, found, err := mdfile.ParseNoteBlockManifest(rebased)
	if err != nil || !found || len(manifestAfter.Blocks) != 3 {
		t.Fatalf("rebase 未重建 block manifest：found=%v err=%v %+v",
			found, err, manifestAfter)
	}
	if manifestAfter.Blocks[0].ContentHash ==
		manifestBefore.Blocks[0].ContentHash {
		t.Fatal("正文变化后 B1 content_hash 未变化，仍在复用旧 manifest")
	}
	code, freshView, errOut := runStorageV3CLI(t, root,
		"candidate", "show", "--vault", dir, "--json", "--note", spec.Note)
	if code != ExitOK || freshView.Data["stale"] != false {
		t.Fatalf("完整重划分后 workspace 应 fresh：code=%d err=%s data=%+v",
			code, errOut, freshView.Data)
	}
}

func TestCandidateMigrateSplitsLegacyNoteAtomically(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	before := mustRead(t, notePath)
	beforeDoc, err := mdfile.Parse(before)
	if err != nil {
		t.Fatal(err)
	}
	bodyBefore, _ := beforeDoc.Section(mdfile.SecNoteBody)
	reviewBefore, err := mdfile.ParseReviewNote(
		before[bodyBefore.Body:bodyBefore.End])
	if err != nil {
		t.Fatal(err)
	}
	wantPlain, err := mdfile.RenderPlainReviewNote(
		reviewBefore.Blocks, reviewBefore.Omissions)
	if err != nil {
		t.Fatal(err)
	}
	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 10, 4, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	code, migrated, errOut := runStorageV3CLI(t, root,
		"candidate", "migrate", "--vault", dir, "--json",
		"--note", "n-20260922-txn-materialize", "--user-request")
	if code != ExitOK {
		t.Fatalf("candidate migrate 失败：%d %s %+v", code, errOut, migrated)
	}
	workspaceRel := "domains/ai-infra/note-segments/ns-20260922-txn-materialize.md"
	after := mustRead(t, notePath)
	afterDoc, _, err := mdfile.ParseNote(after)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := afterDoc.Section(mdfile.SecExtraction); ok {
		t.Fatalf("迁移后的 n-* 仍含提取结果：\n%s", after)
	}
	bodyAfter, _ := afterDoc.Section(mdfile.SecNoteBody)
	wantFramed := append([]byte("\n"), wantPlain...)
	wantFramed = append(wantFramed, '\n')
	if !bytes.Equal(after[bodyAfter.Body:bodyAfter.End], wantFramed) ||
		mdfile.ContainsEvergreenMachineAnchors(after) {
		t.Fatalf("迁移后的 n-* 未成为 anchorless 可见正文：\n%s", after)
	}
	workspaceRaw := mustRead(t, filepath.Join(dir, filepath.FromSlash(workspaceRel)))
	_, workspace, err := mdfile.ParseNoteSegmentation(workspaceRaw)
	if err != nil {
		t.Fatalf("迁移后的 ns-* 不可解析：%v\n%s", err, workspaceRaw)
	}
	if workspace.NoteHash != store.ContentHash(after) ||
		!bytes.Contains(workspaceRaw, []byte("<!-- eg:nb:1 ")) ||
		!bytes.Contains(workspaceRaw, []byte("<!-- eg:cd:2 ")) ||
		!bytes.Contains(workspaceRaw, []byte("<!-- eg:cc:2 ")) {
		t.Fatalf("迁移后的关联/hash/协议不成立：%+v\n%s", workspace, workspaceRaw)
	}

	noteBeforeReplay := mustRead(t, notePath)
	workspaceBeforeReplay := mustRead(t,
		filepath.Join(dir, filepath.FromSlash(workspaceRel)))
	commitsBeforeReplay := gitOut(t, dir, "rev-list", "--count", "HEAD")
	code, replay, errOut := runStorageV3CLI(t, root,
		"candidate", "migrate", "--vault", dir, "--json",
		"--note", "n-20260922-txn-materialize", "--user-request")
	if code != ExitOK || replay.Data["txn_id"] != "" {
		t.Fatalf("重复 migrate 应 no-op：%d %s %+v", code, errOut, replay.Data)
	}
	if !bytes.Equal(mustRead(t, notePath), noteBeforeReplay) ||
		!bytes.Equal(mustRead(t,
			filepath.Join(dir, filepath.FromSlash(workspaceRel))), workspaceBeforeReplay) {
		t.Fatal("重复 migrate 改写了 n-* 或 ns-*")
	}
	if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != commitsBeforeReplay {
		t.Fatal("重复 migrate 产生了空 commit")
	}
}

func TestCandidateMigrateNormalizesExistingWorkspaceAtomically(t *testing.T) {
	dir, noteRel, workspaceRel := materializeWorkspaceFixture(t)
	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	workspacePath := filepath.Join(dir, filepath.FromSlash(workspaceRel))
	noteBefore := mustRead(t, notePath)
	workspaceBefore := mustRead(t, workspacePath)
	candidatesBefore, err := mdfile.ParseCandidates(workspaceBefore)
	if err != nil {
		t.Fatal(err)
	}
	workspaceDocBefore, err := mdfile.Parse(workspaceBefore)
	if err != nil {
		t.Fatal(err)
	}
	userBefore, _ := workspaceDocBefore.Section(mdfile.SecUserAppend)

	root := newTestRoot(t, dir)
	root.Now = func() time.Time {
		return time.Date(2026, 10, 5, 12, 0, 0, 0,
			time.FixedZone("CST", 8*60*60))
	}
	code, migrated, errOut := runStorageV3CLI(t, root,
		"candidate", "migrate", "--vault", dir, "--json",
		"--note", "n-20260922-workspace", "--user-request")
	if code != ExitOK {
		t.Fatalf("existing workspace migrate 失败：%d %s %+v",
			code, errOut, migrated)
	}
	noteAfter := mustRead(t, notePath)
	workspaceAfter := mustRead(t, workspacePath)
	if bytes.Equal(noteAfter, noteBefore) ||
		mdfile.ContainsEvergreenMachineAnchors(noteAfter) {
		t.Fatalf("已有 n/ns 迁移未移除 n-* 机器锚点：\n%s", noteAfter)
	}
	manifest, found, err := mdfile.ParseNoteBlockManifest(workspaceAfter)
	if err != nil || !found || len(manifest.Blocks) != 2 {
		t.Fatalf("已有 n/ns 迁移未写块清单：found=%v err=%v %+v\n%s",
			found, err, manifest, workspaceAfter)
	}
	candidatesAfter, err := mdfile.ParseCandidates(workspaceAfter)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidatesAfter) != len(candidatesBefore) {
		t.Fatalf("candidate 数量变化：%d -> %d",
			len(candidatesBefore), len(candidatesAfter))
	}
	for i := range candidatesBefore {
		if !bytes.Equal(candidatesBefore[i].Raw(workspaceBefore),
			candidatesAfter[i].Raw(workspaceAfter)) {
			t.Fatalf("candidate %s 字节被迁移改写", candidatesBefore[i].Key)
		}
	}
	workspaceDocAfter, _, err := mdfile.ParseNoteSegmentation(workspaceAfter)
	if err != nil {
		t.Fatal(err)
	}
	userAfter, _ := workspaceDocAfter.Section(mdfile.SecUserAppend)
	if !bytes.Equal(
		workspaceBefore[userBefore.Body:userBefore.End],
		workspaceAfter[userAfter.Body:userAfter.End]) {
		t.Fatal("已有 n/ns 迁移改写了 workspace 用户补充")
	}
	_, segmentation, _, _, refs, err := store.ParseMaterializationWorkspace(
		noteAfter, workspaceAfter)
	if err != nil || segmentation.NoteHash != store.ContentHash(noteAfter) ||
		len(refs) != 2 || !refs["B1"] || !refs["B2"] {
		t.Fatalf("规范化后的 workspace 不可物化：err=%v segmentation=%+v refs=%v",
			err, segmentation, refs)
	}

	headBeforeReplay := gitOut(t, dir, "rev-parse", "HEAD")
	code, replay, errOut := runStorageV3CLI(t, root,
		"candidate", "migrate", "--vault", dir, "--json",
		"--note", "n-20260922-workspace", "--user-request")
	if code != ExitOK || replay.Data["txn_id"] != "" {
		t.Fatalf("规范化重复 migrate 应 no-op：%d %s %+v",
			code, errOut, replay.Data)
	}
	if got := gitOut(t, dir, "rev-parse", "HEAD"); got != headBeforeReplay {
		t.Fatal("规范化重复 migrate 产生空 commit")
	}
}

func TestCandidateApplyRejectsAuthorizationStaleAndInvalidSpec(t *testing.T) {
	dir, noteRel := materializeTxnFixture(t)
	notePath := filepath.Join(dir, filepath.FromSlash(noteRel))
	root := newTestRoot(t, dir)
	code, shown, errOut := runStorageV3CLI(t, root,
		"candidate", "show", "--vault", dir, "--json",
		"--note", "n-20260922-txn-materialize")
	if code != ExitOK {
		t.Fatalf("candidate show 失败：%d %s", code, errOut)
	}
	raw, _ := json.Marshal(shown.Data)
	var spec candidateReviewSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	reviewPath := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(reviewPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, notePath)
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath)
	if code != ExitValidation || !bytes.Equal(before, mustRead(t, notePath)) {
		t.Fatalf("缺 --user-request 应退 2 且零写入，实际 code=%d", code)
	}

	stale := append(append([]byte(nil), before...), []byte("\n")...)
	if err := os.WriteFile(notePath, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitValidation || !bytes.Equal(stale, mustRead(t, notePath)) {
		t.Fatalf("stale note_hash 应退 2 且零写入，实际 code=%d", code)
	}

	if err := os.WriteFile(notePath, before, 0o644); err != nil {
		t.Fatal(err)
	}
	spec.Candidates[0].LogicalSlug = "Bad_Slug"
	invalidRaw, _ := json.Marshal(spec)
	if err := os.WriteFile(reviewPath, invalidRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitValidation || !bytes.Equal(before, mustRead(t, notePath)) {
		t.Fatalf("非法 logical_slug 应退 2 且零写入，实际 code=%d", code)
	}

	spec.Candidates[0].LogicalSlug = "valid-slug"
	spec.Coverage = spec.Coverage[:1]
	invalidRaw, _ = json.Marshal(spec)
	if err := os.WriteFile(reviewPath, invalidRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ = runStorageV3CLI(t, root,
		"candidate", "apply", "--vault", dir, "--json",
		"--note", spec.Note, "--file", reviewPath, "--user-request")
	if code != ExitValidation || !bytes.Equal(before, mustRead(t, notePath)) {
		t.Fatalf("coverage 缺口应退 2 且零写入，实际 code=%d", code)
	}
}
