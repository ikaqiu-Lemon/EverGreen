package cli

// R1 纳管 commit 的**唯一写口**（对账合同 §1.3 第 1 行 / §4；A-30 verb / A-35 恰一次；
// M4 · T-evergreen.s1_main_flow-158614-050）。
//
// 本文件 ≠ eg reconcile 命令本体 —— 命令注册、信封与退出码归 T-…-058；本文件只提供
// 「在全部检查与修复写入完成之后、执行**恰一次** `git add -A` + 提交」这一个能力，
// 由那个命令在编排末尾调用一次。本文件不注册任何命令、不解析任何 flag。
//
// # 为什么写口在 CLI 层而不在检查器里（A-23 第 3 条的延伸）
//
// 对账检查器是纯只读的（零写盘、零提交、零子进程），Git 提交因此只能发生在 CLI 层：
// 与 `eg capture` / `eg init` / `eg apply` 同类 —— 用户发起、CLI 自带一次提交。
// 本文件直连 internal/git，**不得** import 知识数据写盘层（合同 §1.3 硬约束，
// grep 反证恒 0）：知识数据的修改（R2 的补齐、R6 的失准标记）仍走内存 ChangePlan →
// plan → 写盘层这条唯一写盘链，分属 T-…-051 / T-…-055，与本文件无关。
//
// # R1 纳管**不是**知识数据修改（A-23 窄例外一字不放宽）
//
// R1 纳管的对象是「用户自己已经改好的文件」：本文件一个字节都不改写、不碰任何
// frontmatter 键、不生成任何内容，只把工作区**既有事实**记进 Git 历史。
// 因此它不需要 ChangePlan、不需要写权限矩阵放行、不新增矩阵行；而 CLI 直写的窄例外
// （仅 `proposals/**`）也与本文件无关 —— 本文件根本不写文件。
//
// # 三条不可放宽的不变式（逐条有机器反证）
//
//  1. **恰 0 或 1 次提交**：一个 ReconcileTakeover 实例只允许纳管一次（第二次调用直接
//     报 ErrTakeoverAlreadyRan，不会产生第二条提交）；Commits() 恒 ≤ 1。
//  2. **零改动即零提交**：工作区与 HEAD 一致 → 不产生空提交，Commit 字段为 nil
//     （报告侧 reconcile.commit 因此可为 null，与 §11.2 的单值口径同源）。
//  3. **提交范围口径一字不变**：范围恒为 `git add -A`（M1 起的既有口径，R-13 / R-15 继承），
//     不得改成选择性暂存、不得改成强原子事务、不得改成两次提交。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// reconcileAddAllSemantics 逐字记录本写口的提交范围口径：`git add -A`。
//
// 它与 internal/git 的 Add 同一口径（那里就是 `git add -A` 的唯一执行点）：
// 本次纳管的改动与工作区既有改动**一并**进本次提交，不做工作区隔离式的选择性暂存。
const reconcileAddAllSemantics = "add -A"

// ReconcileTakeoverNotice 陈述「系统做了什么 / 没做什么」：只把既有改动记进历史，
// 不改写任何文件内容。措辞固定，便于用例与 e2e 逐字断言。
const ReconcileTakeoverNotice = "本次纳管只把工作区既有改动记进 Git 历史：" +
	"未改写任何文件内容、未写任何 frontmatter 键、未产生第二条提交"

// ReconcileZeroChangeNotice 是「零改动即零提交」的固定文案（工作区干净时的唯一交代）。
const ReconcileZeroChangeNotice = "工作区与 HEAD 一致且本次无修复写入：未产生提交（零改动即零提交）"

// ReconcileDryRunNotice 是 --dry-run（属 T-…-058）复用本写口时的固定文案：只报不写。
const ReconcileDryRunNotice = "dry-run：只报告未提交改动清单，零写入、零提交"

// 两条调用序 / 调用次数的守卫错误（写口的硬失败，不是 warning）。
var (
	// ErrTakeoverAlreadyRan 表示同一次对账里第二次调用写口 —— 恰一次的反面。
	ErrTakeoverAlreadyRan = errors.New("纳管写口在同一次对账内只允许调用一次（A-35：恰一次提交）")
	// ErrTakeoverTooEarly 表示检查或修复写入还没跑完就来提交 —— 合同 §4 的调用序要求。
	ErrTakeoverTooEarly = errors.New("纳管提交必须在全部检查与修复写入完成之后执行（合同 §4）")
)

// ReconcileTakeoverInput 是一次纳管的全部输入（纯事实，不含任何策略开关）。
type ReconcileTakeoverInput struct {
	// Domain 是提交主题的域位；空串 → `-`（全局操作，与既有 commitDomain 同口径）。
	Domain string
	// Findings / Repairs 是本次对账的产出事实，只进提交主题与报告文案，不参与判定。
	// Repairs 恒由 R2 / R6 的编排侧（T-…-051 / T-…-055）填，本 task 的调用方填 0。
	Findings int
	Repairs  int
	// OurWrites 是本次由 eg 自己写入的仓库相对路径，只为把「工作区既有改动」
	// 如实区分出来（两者都会进本次提交，`git add -A` 口径不变）。
	OurWrites []string
	// ChecksDone / RepairsDone 是调用序的显式声明：两者必须都为 true，
	// 否则写口拒绝执行（ErrTakeoverTooEarly）。
	ChecksDone  bool
	RepairsDone bool
	// DryRun 为真 → 零写入零提交，只回报待纳管清单。
	DryRun bool
	// Reason / RequirementIDs 进提交正文（Reason: / Requirement: 两行，口径同 M1）。
	Reason         string
	RequirementIDs []string
}

// ReconcileTakeoverResult 是一次纳管的只读回执。
//
// Commit 是**单值**：`*string` 而不是 `[]string` —— 报告侧 reconcile.commit 是单值字段
// （§11.2 / A-35），两次提交在这里从类型上就无法表达。
type ReconcileTakeoverResult struct {
	// Ran 表示写口被执行过（含「跑了但零提交」这一支）。
	Ran bool
	// Commit 是本次纳管提交的 SHA；零改动 / dry-run 时为 nil（报告侧写 null）。
	Commit *string
	// Pending 是纳管前工作区的未提交路径（如实展示，顺序取自 git 的只读采样）。
	Pending []string
	// Existing 是其中**非本次写入**的既有改动（一并进本次提交，如实说明）。
	Existing []string
	// Files 是本次提交实际涉及的文件清单（提交后的只读回读）。
	Files []string
	// Warnings 是提交信息层面的告警（verb / Reason / Requirement 缺失等）与固定交代文案。
	Warnings []string
}

// ReconcileTakeover 是纳管写口的载体：**恰一次**这条不变式由实例状态承载，
// 而不是靠调用方自律 —— 一次对账构造一个实例，第二次调用直接失败。
type ReconcileTakeover struct {
	repo    *git.Repo
	ran     bool
	commits int
}

// NewReconcileTakeover 用 vault 的 Git 仓构造一个只能用一次的纳管写口。
func NewReconcileTakeover(repo *git.Repo) *ReconcileTakeover {
	return &ReconcileTakeover{repo: repo}
}

// newReconcileTakeover 走 Root 的可注入仓库工厂（提交失败分支因此可被用例覆盖）。
func (r *Root) newReconcileTakeover(root string) *ReconcileTakeover {
	return NewReconcileTakeover(r.repo(root))
}

// Ran 报告写口是否已被调用过（幂等判定与「恰一次」的读侧出口）。
func (t *ReconcileTakeover) Ran() bool { return t.ran }

// Commits 返回本写口实际产生的提交条数：**恒 0 或 1**。
func (t *ReconcileTakeover) Commits() int { return t.commits }

// Takeover 执行恰一次纳管提交。
//
// 顺序：① 只读采样工作区状态 → ② 干净则零提交直接返回 → ③ dry-run 则零写入返回 →
// ④ 区分「本次写入 / 既有改动」→ ⑤ 一次 `git add -A` + 一次提交（verb = reconcile）。
// 提交失败 → 返回 *CommitFailedError（退出码 4 由命令层翻译），磁盘保留现状、不做任何还原（B4）。
func (t *ReconcileTakeover) Takeover(in ReconcileTakeoverInput) (ReconcileTakeoverResult, error) {
	res := ReconcileTakeoverResult{}
	if t.ran {
		return res, ErrTakeoverAlreadyRan
	}
	if !in.ChecksDone || !in.RepairsDone {
		return res, ErrTakeoverTooEarly
	}
	t.ran = true
	res.Ran = true

	st, err := t.repo.Status()
	if err != nil {
		return res, err
	}
	res.Pending = st.Paths()

	// ② 零改动即零提交：不产生空提交，Commit 保持 nil。
	if st.IsClean() {
		res.Warnings = append(res.Warnings, ReconcileZeroChangeNotice)
		return res, nil
	}
	// ③ dry-run：零写入零提交，清单照报。
	if in.DryRun {
		res.Warnings = append(res.Warnings, ReconcileDryRunNotice)
		return res, nil
	}
	// ④ 只为如实说明：既有改动同样会进本次提交（提交范围口径见 reconcileAddAllSemantics）。
	if sample, serr := t.repo.Sample(in.OurWrites); serr == nil {
		res.Existing = sample.Existing
		if len(sample.Existing) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"本次提交含 %d 处工作区既有改动（`git %s` 口径一并提交）：%s",
				len(sample.Existing), reconcileAddAllSemantics,
				strings.Join(sample.Existing, "、")))
		}
	}

	// ⑤ 恰一次提交：verb 取 A-30 裁决值 reconcile（该 verb 在 model 侧已属已知 verb，
	// KnownVerbs 恒 8，本 task 不增减 verb 个数）。
	info, cerr := t.repo.Commit(git.Message{
		Verb:           string(model.VerbReconcile),
		Domain:         commitDomain(in.Domain),
		Subject:        reconcileCommitSubject(len(res.Pending), in.Findings, in.Repairs),
		Reason:         reconcileCommitReason(in.Reason),
		RequirementIDs: in.RequirementIDs,
	})
	res.Warnings = append(res.Warnings, info.Warnings...)
	res.Warnings = append(res.Warnings, ReconcileTakeoverNotice)
	if cerr != nil {
		return res, &CommitFailedError{
			Msg: "Git 提交失败：工作区改动保持现状，未做任何还原（B4）；本次对账零提交",
			Err: cerr,
			Diags: []Diagnostic{{
				Code: E22, Level: LevelError, Path: "git.commit", OpIndex: NonOpDiagnostic,
				Message: commitFailureMessage(cerr),
			}},
		}
	}
	if info.Created {
		t.commits++
		sha := info.SHA
		res.Commit = &sha
		res.Files = info.Files
	}
	return res, nil
}

// reconcileCommitSubject 生成提交主题的事实部分（只复述已发生的事实，不加评价）。
func reconcileCommitSubject(pending, findings, repairs int) string {
	return fmt.Sprintf("纳管 %d 处未提交改动（对账 finding %d 条、修复写入 %d 处）",
		pending, findings, repairs)
}

// reconcileCommitReason 补默认原因（调用方未给时），保证提交正文的 Reason: 行不缺。
func reconcileCommitReason(reason string) string {
	if strings.TrimSpace(reason) != "" {
		return reason
	}
	return "对账纳管：把用户在编辑器里直接改过的文件记进 Git 历史（R1，恰一次提交）"
}
