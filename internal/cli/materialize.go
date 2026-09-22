package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

var materializeCandidateKeyRE = regexp.MustCompile(
	`^cand-[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func materializeCommand() *Command {
	return &Command{
		Name:    "materialize",
		Display: "materialize",
		Summary: "将 Note candidate 确定性物化为 Knowledge / Opinion（用户显式写路径）",
		Owner:   "T-evergreen.block_boundary_materialization-158614-005",
		Usage: `eg materialize --note <n-id> (--candidate <cand-key> | --all) --user-request [--strict] [--json]

参数：
  --note <n-id>              是；包含 candidate 的 Note
  --candidate <cand-key>     与 --all 恰一；只物化一个 candidate
  --all                      与 --candidate 恰一；物化该 Note 的全部未物化 candidate
  --user-request             是；用户显式发起的命令行佐证
  --strict                   否；保留统一写命令强校验参数面

物化不调用模型、不访问网络，只校验并复制 Note 内已经存在的确定字节。目标文件与 Note
映射更新进入同一个 journal v1 write-set；跨日重跑复用 candidate output，完全一致时 no-op。
退出码：0 成功 / 幂等 no-op | 1 参数非法（零写入） | 2 授权或结构校验失败（零写入） |
        3 B3 冲突或事务已整体回滚（零 commit） |
        4 Git 提交失败（Markdown 已原子生效并保留） |
        5 写前安全复核失败（E15）/ run.lock 不可用（E16），两者均零权威写入
`,
		Flags: func(fs *flagSet) {
			fs.String("note", "", "包含 candidate 的 Note ID")
			fs.String("candidate", "", "只物化该 candidate key")
			fs.Bool("all", false, "物化该 Note 的全部 candidate")
			registerStrictFlag(fs)
		},
		Validate: validateMaterializeArgs,
	}
}

func validateMaterializeArgs(inv *Invocation) error {
	if err := noPositionalArgs(inv); err != nil {
		return err
	}
	note := strings.TrimSpace(inv.String("note"))
	if note == "" {
		return &UsageError{Msg: "eg materialize 缺必填参数 --note <n-id>"}
	}
	if _, err := model.ParseNoteID(note); err != nil {
		return &UsageError{Msg: fmt.Sprintf("eg materialize 的 --note 非法：%v", err)}
	}
	candidate := strings.TrimSpace(inv.String("candidate"))
	all, err := strconv.ParseBool(inv.String("all"))
	if err != nil {
		return &UsageError{Msg: "eg materialize 的 --all 必须是布尔值"}
	}
	if inv.Set("candidate") && candidate == "" {
		return &UsageError{Msg: "eg materialize 的 --candidate 不得为空"}
	}
	if candidate != "" && !materializeCandidateKeyRE.MatchString(candidate) {
		return &UsageError{Msg: fmt.Sprintf(
			"eg materialize 的 --candidate 非法：%q", candidate)}
	}
	if (candidate != "") == all {
		return &UsageError{Msg: "eg materialize 必须且只能给出 --candidate <cand-key> 或 --all"}
	}
	return nil
}

type materializeCandidateData struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Output  string `json:"output"`
	Path    string `json:"path"`
	Created bool   `json:"created"`
}

func (r *Root) runMaterialize(inv *Invocation) (*Result, error) {
	noteText := strings.TrimSpace(inv.String("note"))
	if !inv.UserRequest {
		return nil, &ValidationError{
			Msg: "eg materialize 属用户显式写路径：请加 --user-request",
			Diags: []Diagnostic{{
				Code: E19, Level: LevelError, Path: "--" + UserRequestFlag,
				OpIndex: NonOpDiagnostic, Target: noteText,
				Message: "candidate 物化缺少用户显式发起佐证（--user-request）",
			}},
		}
	}
	note, _ := model.ParseNoteID(noteText)
	now := r.now()
	req := plan.MaterializeRequest{
		Note: note, Candidate: strings.TrimSpace(inv.String("candidate")),
		All:  inv.String("all") == "true",
		Date: model.NewDate(now), Stamp: model.NewStamp(now),
	}
	prepared, err := prepareMaterializeRequest(inv.VaultRoot, req)
	if err != nil {
		return nil, materializeValidationError(noteText,
			"materialize 无法定位或读取 Note", err)
	}

	res := &Result{}
	out, err := r.materializeCritical(inv, res, prepared)
	if err != nil {
		var skip *store.SkipError
		if errors.As(err, &skip) {
			res.Data = materializeEmptyData(noteText)
			return res, &PartialWriteError{
				Msg: "candidate 物化因 B3 并发保护被跳过：本次零写入、零 commit",
				Diags: []Diagnostic{{
					Code: E24, Level: LevelError, Path: skip.Path,
					OpIndex: NonOpDiagnostic, Target: noteText,
					Message: skip.Error(),
				}},
			}
		}
		var blocked *TxnBlockedError
		if errors.As(err, &blocked) {
			return res, err
		}
		return res, materializeValidationError(noteText,
			"candidate 物化结构校验失败", err)
	}
	if out == nil || out.Materialized == nil {
		return res, fmt.Errorf("materialize 事务入口未返回结果")
	}

	res.Data = materializeResultData(out)
	res.DataOrder = []string{
		"note", "note_path", "materialized_candidates", "finalized", "txn_id", "commit",
	}
	switch {
	case out.RolledBack:
		return res, &PartialWriteError{Msg: fmt.Sprintf(
			"原子提交失败，事务 %s 已整体回滚：%d 个目标文件一个都没有写入、"+
				"磁盘保持事务开始前的字节、未产生 commit（原因：%v）",
			out.TxnID, len(out.Unwritten), out.RollbackErr)}
	case out.Blocked != nil:
		return res, out.Blocked
	case out.GitErr != nil:
		return res, &CommitFailedError{
			Msg: "Git 提交失败：物化后的 Markdown 已原子生效并保留，未做任何还原（B4）",
			Err: out.GitErr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit",
				OpIndex: NonOpDiagnostic, Message: commitFailureMessage(out.GitErr),
			}},
		}
	}
	if len(out.Materialized.WriteSet) == 0 {
		res.Summary = []string{fmt.Sprintf(
			"materialize：Note %s 的映射与目标内容一致，幂等 no-op；零写入、零 commit",
			noteText)}
		return res, nil
	}
	res.Summary = []string{fmt.Sprintf(
		"materialize：Note %s 处理 %d 个 candidate，写入 %d 个权威文件，finalized=%t",
		noteText, len(out.Materialized.Candidates),
		len(out.Materialized.WriteSet), out.Materialized.Finalized)}
	return res, nil
}

func materializeValidationError(target, prefix string, err error) error {
	msg := fmt.Sprintf("%s：%v", prefix, err)
	return &ValidationError{Msg: msg, Diags: []Diagnostic{{
		Code: E20, Level: LevelError, Path: target,
		OpIndex: NonOpDiagnostic, Target: target, Message: msg,
	}}}
}

func materializeEmptyData(note string) map[string]interface{} {
	return map[string]interface{}{
		"note": note, "note_path": "", "materialized_candidates": []materializeCandidateData{},
		"finalized": false, "txn_id": "", "commit": nil,
	}
}

func materializeResultData(out *materializeTxnOutcome) map[string]interface{} {
	items := make([]materializeCandidateData, 0, len(out.Materialized.Candidates))
	for _, item := range out.Materialized.Candidates {
		items = append(items, materializeCandidateData{
			Key: item.Key, Kind: string(item.Kind), Output: item.Output,
			Path: item.Path, Created: item.Created,
		})
	}
	var commit interface{}
	if out.Commit.Created || out.Commit.SHA != "" {
		commit = commitInfoData(out.Commit)
	}
	return map[string]interface{}{
		"note": string(out.Materialized.Note), "note_path": out.Materialized.NotePath,
		"materialized_candidates": items, "finalized": out.Materialized.Finalized,
		"txn_id": out.TxnID, "commit": commit,
	}
}
