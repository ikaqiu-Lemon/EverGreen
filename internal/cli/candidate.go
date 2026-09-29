package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const (
	subCandidateShow  = "show"
	subCandidateApply = "apply"
)

func candidateCommand() *Command {
	return &Command{
		Name:        "candidate",
		Display:     "candidate show|apply",
		Summary:     "导出或原子应用 Note candidate 的完整用户审阅状态",
		Owner:       "T-evergreen.candidate_review_workflow-158614-002",
		Subs:        []string{subCandidateShow, subCandidateApply},
		SubRequired: true,
		Usage: `eg candidate show --note <n-id> [--json]
eg candidate apply --note <n-id> --file <review.json|-> --user-request [--strict] [--json]

参数：
  --note <n-id>        是；包含未物化 candidate 的 Note
  --file <path|->      apply 必填；完整 review spec，- 从标准输入读取
  --user-request       apply 必填；用户显式确认并发起
  --strict             apply 可选；保留统一写命令强校验参数面

show 只读，返回 schema_version=1 的完整候选与覆盖状态。apply 把 spec 当成完整目标状态，
可一次新增、删除、重命名、重排、改类型、改 logical_slug/source_refs/payload/coverage。
apply 要求 note_path 与 note_hash 精确匹配、现有候选全未物化，只替换「提取结果」中的候选
审阅管理区；其余 Note 字节保持不变。写入走 journal v1 与一次 Git commit。
退出码：0 成功 / 幂等 no-op | 1 参数非法 | 2 授权、spec 或结构校验失败（零写入） |
        3 原子提交已整体回滚 | 4 Git 提交失败（Markdown 已生效并保留） |
        5 写前安全复核失败（E15）/ run.lock 不可用（E16），两者均零权威写入
`,
		Flags: func(fs *flagSet) {
			fs.String("note", "", "包含 candidate 的 Note ID")
			fs.String("file", "", "完整 candidate review spec 文件；- 表示标准输入")
			registerStrictFlag(fs)
		},
		Validate: validateCandidateArgs,
	}
}

func validateCandidateArgs(inv *Invocation) error {
	if err := noPositionalArgs(inv); err != nil {
		return err
	}
	note := strings.TrimSpace(inv.String("note"))
	if note == "" {
		return &UsageError{Msg: "eg candidate 缺必填参数 --note <n-id>"}
	}
	if _, err := model.ParseNoteID(note); err != nil {
		return &UsageError{Msg: fmt.Sprintf("eg candidate 的 --note 非法：%v", err)}
	}
	switch inv.Sub {
	case subCandidateShow:
		if inv.Set("file") {
			return &UsageError{Msg: "eg candidate show 不接受 --file"}
		}
		if inv.Set(StrictFlag) {
			return &UsageError{Msg: "eg candidate show 是只读命令，不接受 --strict"}
		}
	case subCandidateApply:
		if strings.TrimSpace(inv.String("file")) == "" {
			return &UsageError{Msg: "eg candidate apply 缺必填参数 --file <review.json|->"}
		}
	}
	return nil
}

type candidateReviewSection struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type candidateReviewCandidate struct {
	Key         string                   `json:"key"`
	Kind        store.CandidateKind      `json:"kind"`
	LogicalSlug string                   `json:"logical_slug"`
	Title       string                   `json:"title"`
	SourceRefs  []string                 `json:"source_refs"`
	Rel         string                   `json:"rel"`
	Reason      string                   `json:"reason"`
	Tags        []string                 `json:"tags"`
	Sections    []candidateReviewSection `json:"sections"`
}

type candidateReviewCoverage struct {
	Module      string   `json:"module"`
	SourceRefs  []string `json:"source_refs"`
	Summary     string   `json:"summary"`
	Disposition string   `json:"disposition"`
	Candidates  []string `json:"candidates"`
	Reason      string   `json:"reason"`
}

type candidateReviewSpec struct {
	SchemaVersion int                        `json:"schema_version"`
	Note          string                     `json:"note"`
	NotePath      string                     `json:"note_path"`
	NoteHash      string                     `json:"note_hash"`
	Candidates    []candidateReviewCandidate `json:"candidates"`
	Coverage      []candidateReviewCoverage  `json:"coverage"`
}

func (r *Root) runCandidate(inv *Invocation) (*Result, error) {
	if inv.Sub == subCandidateShow {
		return r.runCandidateShow(inv)
	}
	return r.runCandidateApply(inv)
}

func (r *Root) runCandidateShow(inv *Invocation) (*Result, error) {
	spec, err := loadCandidateReviewSpec(
		inv.VaultRoot, strings.TrimSpace(inv.String("note")))
	if err != nil {
		return nil, candidateReviewValidationError(
			strings.TrimSpace(inv.String("note")), "candidate show 失败", err)
	}
	res := &Result{
		Data: candidateReviewSpecData(spec),
		DataOrder: []string{
			"schema_version", "note", "note_path", "note_hash", "candidates", "coverage",
		},
		Summary: []string{fmt.Sprintf(
			"candidate show：Note %s 含 %d 个未物化 candidate、%d 条 coverage；只读",
			spec.Note, len(spec.Candidates), len(spec.Coverage))},
	}
	return res, nil
}

func loadCandidateReviewSpec(root, noteText string) (candidateReviewSpec, error) {
	noteID, err := model.ParseNoteID(noteText)
	if err != nil {
		return candidateReviewSpec{}, err
	}
	st := store.New(root)
	index, err := st.ScanIDs()
	if err != nil {
		return candidateReviewSpec{}, err
	}
	notePath, err := index.Resolve(noteText)
	if err != nil {
		return candidateReviewSpec{}, err
	}
	file, err := st.Read(notePath)
	if err != nil {
		return candidateReviewSpec{}, err
	}
	note, candidates, state, _, err := store.ParseMaterializationNote(file.Bytes)
	if err != nil {
		return candidateReviewSpec{}, err
	}
	if note.ID != noteID {
		return candidateReviewSpec{}, fmt.Errorf(
			"Note 路径与 frontmatter ID 不一致：%s != %s", noteID, note.ID)
	}
	if state.Finalized {
		return candidateReviewSpec{}, fmt.Errorf("Note 已写最终覆盖，不再是 candidate 审阅工作区")
	}
	spec := candidateReviewSpec{
		SchemaVersion: 1,
		Note:          noteText,
		NotePath:      notePath,
		NoteHash:      file.Hash,
		Candidates:    make([]candidateReviewCandidate, len(candidates)),
		Coverage:      reviewCoverageFromStore(state.Draft),
	}
	for i, candidate := range candidates {
		if candidate.Anchor.Output != "" {
			return candidateReviewSpec{}, fmt.Errorf(
				"candidate %s 已物化为 %s，不能导出可回投 review spec",
				candidate.Key, candidate.Anchor.Output)
		}
		draft, err := mdfile.CandidateDraftFromParsed(candidate)
		if err != nil {
			return candidateReviewSpec{}, err
		}
		spec.Candidates[i] = reviewCandidateFromDraft(draft)
	}
	return spec, nil
}

func reviewCandidateFromDraft(draft store.CandidateDraft) candidateReviewCandidate {
	sections := make([]candidateReviewSection, len(draft.Sections))
	for i, section := range draft.Sections {
		sections[i] = candidateReviewSection{Name: section.Name, Body: string(section.Body)}
	}
	return candidateReviewCandidate{
		Key: draft.Key, Kind: draft.Kind, LogicalSlug: draft.LogicalSlug,
		Title: draft.Title, SourceRefs: cloneReviewStrings(draft.SourceRefs),
		Rel: draft.Rel, Reason: draft.Reason, Tags: cloneReviewStrings(draft.Tags),
		Sections: sections,
	}
}

func reviewCoverageFromStore(items []store.CandidateCoverage) []candidateReviewCoverage {
	out := make([]candidateReviewCoverage, len(items))
	for i, item := range items {
		out[i] = candidateReviewCoverage{
			Module: item.Module, SourceRefs: cloneReviewStrings(item.SourceRefs),
			Summary: item.Summary, Disposition: item.Disposition,
			Candidates: cloneReviewStrings(item.Candidates), Reason: item.Reason,
		}
	}
	return out
}

func cloneReviewStrings(items []string) []string {
	out := make([]string, len(items))
	copy(out, items)
	return out
}

func candidateReviewSpecData(spec candidateReviewSpec) map[string]interface{} {
	return map[string]interface{}{
		"schema_version": spec.SchemaVersion,
		"note":           spec.Note,
		"note_path":      spec.NotePath,
		"note_hash":      spec.NoteHash,
		"candidates":     spec.Candidates,
		"coverage":       spec.Coverage,
	}
}

func (r *Root) runCandidateApply(inv *Invocation) (*Result, error) {
	noteText := strings.TrimSpace(inv.String("note"))
	if !inv.UserRequest {
		return nil, &ValidationError{
			Msg: "eg candidate apply 属用户显式写路径：请加 --user-request",
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: "--" + UserRequestFlag,
				OpIndex: NonOpDiagnostic, Target: noteText,
				Message: "candidate review apply 缺少用户显式发起佐证（--user-request）",
			}},
		}
	}
	raw, err := readCandidateReviewFile(r.In, strings.TrimSpace(inv.String("file")))
	if err != nil {
		return nil, candidateReviewValidationError(noteText, "读取 review spec 失败", err)
	}
	spec, err := decodeCandidateReviewSpec(raw)
	if err != nil {
		return nil, candidateReviewValidationError(noteText, "review spec 非法", err)
	}
	if spec.Note != noteText {
		return nil, candidateReviewValidationError(noteText, "review spec 非法",
			fmt.Errorf("note=%q 与 --note=%q 不一致", spec.Note, noteText))
	}

	res := &Result{}
	out, err := r.candidateApplyCritical(inv, res, spec)
	if err != nil {
		var blocked *TxnBlockedError
		if errors.As(err, &blocked) {
			return res, err
		}
		return res, candidateReviewValidationError(noteText, "candidate apply 失败", err)
	}
	res.Data = map[string]interface{}{
		"note": noteText, "note_path": spec.NotePath,
		"candidates": len(spec.Candidates), "coverage": len(spec.Coverage),
		"txn_id": out.TxnID, "commit": candidateApplyCommitData(out.Commit),
	}
	res.DataOrder = []string{
		"note", "note_path", "candidates", "coverage", "txn_id", "commit",
	}
	switch {
	case out.RolledBack:
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：Note 一个字节都没有写入、"+
				"未产生 commit（原因：%v）", out.TxnID, out.RollbackErr)}
	case out.Blocked != nil:
		return res, out.Blocked
	case out.GitErr != nil:
		return res, &CommitFailedError{
			Msg: "Git 提交失败：candidate review 已原子生效并保留，未做任何还原（B4）",
			Err: out.GitErr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit",
				OpIndex: NonOpDiagnostic, Message: commitFailureMessage(out.GitErr),
			}},
		}
	case !out.Changed:
		res.Summary = []string{fmt.Sprintf(
			"candidate apply：Note %s 已与 review spec 一致，幂等 no-op；零写入、零 commit",
			noteText)}
	default:
		res.Summary = []string{fmt.Sprintf(
			"candidate apply：Note %s 已原子替换为 %d 个 candidate、%d 条 coverage",
			noteText, len(spec.Candidates), len(spec.Coverage))}
	}
	return res, nil
}

func readCandidateReviewFile(in io.Reader, path string) ([]byte, error) {
	if path == "-" {
		if in == nil {
			return nil, fmt.Errorf("标准输入不可用")
		}
		return io.ReadAll(in)
	}
	return os.ReadFile(path)
}

func decodeCandidateReviewSpec(raw []byte) (candidateReviewSpec, error) {
	if err := rejectDuplicateReviewJSONKeys(raw); err != nil {
		return candidateReviewSpec{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var spec candidateReviewSpec
	if err := dec.Decode(&spec); err != nil {
		return candidateReviewSpec{}, err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return candidateReviewSpec{}, fmt.Errorf("review spec 必须恰含一个 JSON 对象")
	}
	if spec.SchemaVersion != 1 {
		return candidateReviewSpec{}, fmt.Errorf(
			"schema_version=%d，期望 1", spec.SchemaVersion)
	}
	if spec.Candidates == nil || spec.Coverage == nil {
		return candidateReviewSpec{}, fmt.Errorf("candidates/coverage 必须是数组")
	}
	for i, candidate := range spec.Candidates {
		if candidate.SourceRefs == nil || candidate.Tags == nil || candidate.Sections == nil {
			return candidateReviewSpec{}, fmt.Errorf(
				"candidates[%d] 的 source_refs/tags/sections 必须是数组", i)
		}
	}
	for i, item := range spec.Coverage {
		if item.SourceRefs == nil || item.Candidates == nil {
			return candidateReviewSpec{}, fmt.Errorf(
				"coverage[%d] 的 source_refs/candidates 必须是数组", i)
		}
	}
	return spec, nil
}

func rejectDuplicateReviewJSONKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("review spec 对象键不是字符串")
				}
				if seen[key] {
					return fmt.Errorf("review spec 含重复键 %q", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		default:
			return fmt.Errorf("review spec JSON 分隔符非法：%q", delim)
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("review spec 必须恰含一个 JSON 值")
	}
	return nil
}

func reviewDrafts(spec candidateReviewSpec) []store.CandidateDraft {
	out := make([]store.CandidateDraft, len(spec.Candidates))
	for i, candidate := range spec.Candidates {
		sections := make([]store.CandidateDraftSection, len(candidate.Sections))
		for j, section := range candidate.Sections {
			sections[j] = store.CandidateDraftSection{
				Name: section.Name, Body: []byte(section.Body),
			}
		}
		out[i] = store.CandidateDraft{
			Key: candidate.Key, Kind: candidate.Kind,
			LogicalSlug: candidate.LogicalSlug, Title: candidate.Title,
			SourceRefs: cloneReviewStrings(candidate.SourceRefs),
			Rel:        candidate.Rel, Reason: candidate.Reason,
			Tags: cloneReviewStrings(candidate.Tags), Sections: sections,
		}
	}
	return out
}

func reviewCoverage(spec candidateReviewSpec) []store.CandidateCoverage {
	out := make([]store.CandidateCoverage, len(spec.Coverage))
	for i, item := range spec.Coverage {
		out[i] = store.CandidateCoverage{
			Module: item.Module, SourceRefs: cloneReviewStrings(item.SourceRefs),
			Summary: item.Summary, Disposition: item.Disposition,
			Candidates: cloneReviewStrings(item.Candidates), Reason: item.Reason,
		}
	}
	return out
}

type candidateApplyOutcome struct {
	TxnID       string
	Commit      git.CommitInfo
	Changed     bool
	RolledBack  bool
	RollbackErr error
	Blocked     error
	GitErr      error
}

func (r *Root) candidateApplyCritical(
	inv *Invocation,
	res *Result,
	spec candidateReviewSpec,
) (*candidateApplyOutcome, error) {
	sess, err := r.enterTxnCritical(inv, resultWarnSink{res}, txnCriticalOpts{
		ZeroWrite:     "本次 candidate review 零写入",
		ReleaseNotice: "本次 candidate review 与提交不受影响",
	})
	if err != nil {
		return nil, err
	}
	defer sess.release()

	st := store.New(inv.VaultRoot)
	index, err := st.ScanIDs()
	if err != nil {
		return nil, err
	}
	notePath, err := index.Resolve(spec.Note)
	if err != nil {
		return nil, err
	}
	if notePath != spec.NotePath {
		return nil, fmt.Errorf(
			"note_path 已变化：spec=%s，磁盘=%s", spec.NotePath, notePath)
	}
	file, err := st.Read(notePath)
	if err != nil {
		return nil, err
	}
	if file.Hash != spec.NoteHash {
		return nil, fmt.Errorf(
			"note_hash 已变化：spec=%s，磁盘=%s", spec.NoteHash, file.Hash)
	}
	note, current, state, sourceRefs, err := store.ParseMaterializationNote(file.Bytes)
	if err != nil {
		return nil, err
	}
	if string(note.ID) != spec.Note {
		return nil, fmt.Errorf(
			"Note 路径与 frontmatter ID 不一致：%s != %s", spec.Note, note.ID)
	}
	if state.Finalized {
		return nil, fmt.Errorf("Note 已写最终覆盖，不得应用 review spec")
	}
	for _, candidate := range current {
		if candidate.Anchor.Output != "" {
			return nil, fmt.Errorf(
				"candidate %s 已物化为 %s，不得应用 review spec",
				candidate.Key, candidate.Anchor.Output)
		}
	}
	drafts := reviewDrafts(spec)
	coverage := reviewCoverage(spec)
	if err := store.ValidateCandidateReview(drafts, coverage, sourceRefs); err != nil {
		return nil, err
	}
	target, err := mdfile.ReplaceCandidateDraftState(file.Bytes, drafts, coverage)
	if err != nil {
		return nil, err
	}
	if _, _, _, _, err := store.ParseMaterializationNote(target); err != nil {
		return nil, fmt.Errorf("review spec 应用后 Note 自检失败：%w", err)
	}
	fireTxnStep(TxnStepExecuted, "")

	out := &candidateApplyOutcome{}
	if bytes.Equal(target, file.Bytes) {
		return out, nil
	}
	out.Changed = true
	ws := []store.AtomicFileSpec{{
		Path: notePath, TargetBytes: target, TargetHash: store.ContentHash(target),
		IsNew: false, PreBytes: file.Bytes, PreHash: file.Hash,
	}}
	txnID, err := sess.openTxn(
		inv, intentFilesOf(ws), nil, r.Now, func(id string) { out.TxnID = id })
	if err != nil {
		if txnID == "" {
			return nil, err
		}
		out.Blocked = err
		return out, nil
	}
	commitResult, commitErr := sess.commitWriteSet(txnID, ws)
	switch {
	case commitErr != nil && commitResult != nil && commitResult.RolledBack:
		out.RolledBack = true
		out.RollbackErr = commitErr
		return out, nil
	case commitErr != nil:
		out.Blocked = blockedError(fmt.Sprintf(
			"事务 %s 的 candidate review 提交失败且未能收敛为已回滚状态，需人工处置",
			txnID), commitErr)
		return out, nil
	}
	fireTxnStep(TxnStepCommitted, txnID)

	repo := r.repo(inv.VaultRoot)
	noteExistingChangesInResult(res, repo, []string{notePath})
	info, gitErr := repo.Commit(git.Message{
		Verb: string(model.VerbProcess), Domain: store.DomainOf(notePath),
		Subject: "审阅 candidates " + spec.Note,
		Reason:  "用户显式应用完整 candidate review spec",
	})
	res.Warnings = append(res.Warnings, gitWarnings(info)...)
	out.Commit = info
	out.GitErr = gitErr
	fireTxnStep(TxnStepGit, txnID)

	r.syncResultIndex(res, inv, inv.VaultRoot, []string{notePath})
	fireTxnStep(TxnStepIndexSync, txnID)
	return out, nil
}

func candidateApplyCommitData(info git.CommitInfo) interface{} {
	if !info.Created && info.SHA == "" {
		return nil
	}
	return commitInfoData(info)
}

func candidateReviewValidationError(target, prefix string, err error) error {
	msg := fmt.Sprintf("%s：%v", prefix, err)
	return &ValidationError{Msg: msg, Diags: []Diagnostic{{
		Code: E20, Level: LevelError, Path: target,
		OpIndex: NonOpDiagnostic, Target: target, Message: msg,
	}}}
}
