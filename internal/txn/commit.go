package txn

// [S5] commit.go —— M6 多文件原子提交（T-…-072，合同 §4 / §5.1）。
//
// 语义（合同 §4「安全序」+ §5.1「四级可见性」）：
//   - accepted write-set（S3 preflight 定盘）内 N 个权威文件**要么全部生效、要么全部不生效**；
//   - **首个权威 rename 必须晚于 intent 发布屏障完成**——本函数在写任何权威文件前，先复核
//     该事务已是 StateOpen（intent.json 已由 rename 一次性发布并可解析、commit/abort 皆缺席），
//     因此「先 rename 权威文件、后补日志」这条路径在实现上不存在（TestIntentBarrierBeforeFirstAuthoritativeRename 反证）；
//   - 落盘序逐字定死为「**两阶段**」：① 为每个文件写临时文件 → 逐个 fsync → 目录 fsync；
//     ② 再逐个原子 rename → 目录 fsync；最后写 commit 标记。第一阶段全部 fsync 完成之前
//     不发生任何权威 rename，故任意观察者永不看到撕裂（半写字节）文件（合同 §5.1 L1）；
//   - **提交幂等**：已 committed 直接返回；崩溃在「全部 rename 后、commit 前」（P4）时重跑
//     以相同目标字节重放（rename 幂等）后补写 commit；
//   - **提交不改内容字节**：写入的就是调用方给的 target 字节（与 M1~M4 的字节级写口径一致，
//     含行尾与末尾换行），本函数不构造、不改写任何一个字节；写前复核 HashBytes(target)==intent.target_hash，
//     防止提交的字节与已发布 intent 漂移（否则 072 恢复的 target 分类会张冠李戴）；
//   - **失败路径逐字定死**：保持事务未闭合 → 走 recover.go 的两遍恢复回滚全部前像（B-R2）/
//     把新建文件移入 quarantine（create:true）→ 全部回滚完成后**才**原子写 abort。
//     **严禁先写 abort 再回滚**（先写 abort 会让下一次启动把它当已闭合事务而跳过剩余恢复，
//     永久固化半应用态）。
//
// 写入面：校验通过的 files[].path（权威 vault 文件）+ 本事务目录 .index/txn/<txn_id>/ 之内
// （commit 标记；失败回滚时经 recover.go 触碰 abort / quarantine）。除此之外一个字节都不写。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// CommitFile 是提交写集合的一项：vault 相对路径（与已发布 intent 的 files[].path 对应）+ 目标字节。
type CommitFile struct {
	Path        string
	TargetBytes []byte
}

// CommitInput 是一次提交的完整写集合（必须与已发布 intent 的 files[] 逐项对应）。
type CommitInput struct {
	Files []CommitFile
}

// CommitResult 是一次提交的回执。
type CommitResult struct {
	TxnID string
	// Committed 为 true 表示 commit 标记已在盘（含幂等重入）。
	Committed bool
	// RolledBack 为 true 表示提交失败已按两遍恢复回滚并写 abort。
	RolledBack bool
	// FilesWritten 是本次实际发生权威 rename 的文件数（幂等重入可能为 0）。
	FilesWritten int
}

// CommitInputError 是「提交写集合与已发布 intent 不一致」的 fail closed 错误（携 E15）。
// 提交前复核：写集合的 path 集合与目标哈希必须逐项等于 intent；不一致即拒绝，权威零写入。
type CommitInputError struct {
	TxnID  string
	Reason string
}

func (e *CommitInputError) Error() string {
	return fmt.Sprintf("提交写集合与事务 %s 的已发布 intent 不一致：%s", e.TxnID, e.Reason)
}
func (e *CommitInputError) Code() string { return CodePrecheckFailed }
func (e *CommitInputError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Message: e.Error()}}
}

// CommitStateError 是「事务不处于可提交状态」的错误（已 aborted / corrupt / residue / 不存在）。
type CommitStateError struct {
	TxnID  string
	Reason string
}

func (e *CommitStateError) Error() string {
	return fmt.Sprintf("事务 %s 不可提交：%s", e.TxnID, e.Reason)
}

// CommitStateError 也是写前阻断（已 aborted / corrupt / residue / 标记发布失败后的裁决），
// 因此携 E15，供 074 的 ExitCodeFor 映射为退 5、零写入（P1 评审项）。
func (e *CommitStateError) Code() string { return CodePrecheckFailed }
func (e *CommitStateError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.TxnID, Message: e.Error()}}
}

// 编译期自证：新增阻断错误实现 coded 契约（E15 可被 074 的 ExitCodeFor 映射）。
var (
	_ coded = (*CommitInputError)(nil)
	_ coded = (*CommitStateError)(nil)
)

// commitFailpoint 仅供**包内测试**注入（生产恒 nil）。在关键落盘步骤后以 (step, index) 回调，
// 返回非 nil 即在该点立即中止（模拟崩溃 / 断电），既不吞错也不清理——用于逐点反证崩溃安全。
var commitFailpoint func(step string, index int) error

const (
	fpCommitAfterStage      = "commit_after_stage"       // 全部临时文件已写 + fsync、任何权威 rename 之前
	fpCommitBeforeRename    = "commit_before_rename"     // 第 index 个权威 rename 之前
	fpCommitAfterAllRenames = "commit_after_all_renames" // 全部权威 rename 之后、commit 标记之前
	fpCommitBeforeMarker    = "commit_before_marker"     // 写 commit 标记之前
)

func fireCommitFailpoint(step string, index int) error {
	if commitFailpoint != nil {
		return commitFailpoint(step, index)
	}
	return nil
}

// commitMarkerFailpoint 仅供**包内测试**注入（生产恒 nil），替身 MarkCommit 以复现 P0-2 的
// 「commit 标记发布返回错误」窗口。测试可让它：不发布并返回错误（⇒ 重读为 Open ⇒ 回滚+abort）、
// 先真发布再返回错误（⇒ 重读为 Committed ⇒ 如实报告已提交）、或先 abort 再返回错误（⇒ fail closed）。
var commitMarkerFailpoint func(vaultRoot, txnID string) error

func markCommit(vaultRoot, txnID string) error {
	if commitMarkerFailpoint != nil {
		return commitMarkerFailpoint(vaultRoot, txnID)
	}
	return MarkCommit(vaultRoot, txnID)
}

// 备料 / 恢复两个阶段的确定性临时名后缀（见 stageTempName）。
const (
	stagePhaseCommit  = "stage"   // Commit 备料阶段写在权威目录里的临时文件
	stagePhaseRestore = "restore" // Recover 的 B-R2 前像还原写在权威目录里的临时文件
)

// stageTempName 为事务 txnID 的第 index 个文件构造**确定性**临时名（可由 intent files 下标唯一推导）：
// ".eg-txn-<txnID>-<index>.<phase>"。同一 (txnID,index,phase) 恒定同名，因此崩溃后 Recover 能按
// intent 下标逐一推导并清理**本事务**的残留临时文件（P0-1），无需通配、绝不误删他人文件；保留前缀
// ".eg-txn-" 也保证它永不与权威 Markdown 文件重名（不会覆盖已有权威文件）。
func stageTempName(txnID string, index int, phase string) string {
	return ".eg-txn-" + txnID + "-" + strconv.Itoa(index) + "." + phase
}

// Commit 对事务 txnID 的 accepted write-set 做多文件原子提交。
//
// 调用前置：WriteIntent 已返回成功（intent 发布屏障已完成）。Commit 会**复核**该事务当前
// 是 StateOpen 才开始写任何权威文件——这就是「首个权威 rename 晚于 intent 发布屏障」的实现保证。
//
// 成功 ⇒ 返回 Committed=true（commit 标记在盘）；失败 ⇒ 已按两遍恢复回滚并写 abort，返回原始错误。
func Commit(vaultRoot, txnID string, in CommitInput) (*CommitResult, error) {
	if !ValidTxnID(txnID) {
		return nil, &PathViolationError{Field: "txn_id", Value: txnID, Reason: "非法 txn_id 格式"}
	}
	entry, err := classifyOne(vaultRoot, txnID)
	if err != nil {
		return nil, err
	}
	switch entry.State {
	case StateCommitted:
		// 幂等：commit 标记已在盘，重复提交无副作用。
		return &CommitResult{TxnID: txnID, Committed: true}, nil
	case StateAborted:
		return nil, &CommitStateError{TxnID: txnID, Reason: "abort 标记已在盘，不能再提交"}
	case StateCorrupt:
		return nil, &CommitStateError{TxnID: txnID, Reason: "事务日志损坏：" + entry.CorruptReason}
	case StateResidue:
		return nil, &CommitStateError{TxnID: txnID, Reason: "intent.json 缺席（发布屏障未完成），不能提交"}
	case StateOpen:
		// 唯一允许提交的形态：继续。
	default:
		return nil, &CommitStateError{TxnID: txnID, Reason: "未知事务状态"}
	}

	intent := entry.Intent
	// 写集合与已发布 intent 的逐项一致性复核（path 集合 + 目标哈希 + 目标大小）。
	targets, err := matchCommitInput(txnID, intent, in)
	if err != nil {
		return nil, err
	}

	// ---- 第一阶段：为每个文件写临时文件并逐个 fsync；再 fsync 每个目标目录 ----
	staged := make([]stagedFile, 0, len(intent.Files))
	stageErr := func() error {
		for i, f := range intent.Files {
			abs := authoritativeAbs(vaultRoot, f.Path)
			tmp, dir, serr := stageTemp(txnID, i, stagePhaseCommit, abs, targets[f.Path])
			if serr != nil {
				return serr
			}
			staged = append(staged, stagedFile{abs: abs, tmp: tmp, dir: dir, index: i})
		}
		for _, dir := range uniqueDirs(staged) {
			if serr := fsyncDirPath(dir); serr != nil {
				return serr
			}
		}
		return fireCommitFailpoint(fpCommitAfterStage, -1)
	}()
	if stageErr != nil {
		cleanupTemps(staged)
		return commitRollback(vaultRoot, txnID, stageErr)
	}

	// ---- 第二阶段：逐个原子 rename（首个 rename 即本事务第一个权威落盘）；再 fsync 目录 ----
	renamed := 0
	pubErr := func() error {
		for i, s := range staged {
			if ferr := fireCommitFailpoint(fpCommitBeforeRename, i); ferr != nil {
				return ferr
			}
			if rerr := os.Rename(s.tmp, s.abs); rerr != nil {
				return rerr
			}
			renamed++
		}
		for _, dir := range uniqueDirs(staged) {
			if serr := fsyncDirPath(dir); serr != nil {
				return serr
			}
		}
		return fireCommitFailpoint(fpCommitAfterAllRenames, -1)
	}()
	if pubErr != nil {
		// 清理尚未 rename 的临时文件（已 rename 的由回滚按前像 / quarantine 处置）。
		cleanupTemps(staged[renamed:])
		return commitRollback(vaultRoot, txnID, pubErr)
	}

	if ferr := fireCommitFailpoint(fpCommitBeforeMarker, -1); ferr != nil {
		// 全部权威文件已生效，但 commit 标记尚未落盘（P4 崩溃）：保持未闭合，交由下一次恢复。
		return nil, ferr
	}
	if merr := markCommit(vaultRoot, txnID); merr != nil {
		// P0-2：全部权威 rename 已完成、但 commit 标记发布**返回错误**（非崩溃）。绝不允许留下
		// 「目标态在盘 + 无合法 commit」。重读状态整体裁决后收敛。
		return commitMarkerFailure(vaultRoot, txnID, renamed, merr)
	}
	return &CommitResult{TxnID: txnID, Committed: true, FilesWritten: renamed}, nil
}

// commitMarkerFailure 处理「全部权威 rename 已完成、但 commit 标记发布返回错误」的窗口（P0-2）。
// 通过重读事务状态整体裁决，保证绝不静默留下「目标态在盘 + 无合法 commit」：
//   - StateCommitted：标记其实已在盘（错误发生在标记 fsync 落盘之后的返回路径）⇒ 如实报告已提交；
//   - StateOpen：标记确未发布 ⇒ 按提交失败序回滚全部前像 / quarantine 后写 abort（复用 commitRollback）；
//   - 其余（Corrupt/Aborted/Residue）：fail closed，返回携 E15 的 CommitStateError（供 074 映射退 5）。
func commitMarkerFailure(vaultRoot, txnID string, renamed int, cause error) (*CommitResult, error) {
	entry, cerr := classifyOne(vaultRoot, txnID)
	if cerr != nil {
		return nil, errors.Join(cause, cerr)
	}
	switch entry.State {
	case StateCommitted:
		// 标记已落盘：提交实际成功，返回错误只是标记调用的返回路径异常，如实报告已提交。
		return &CommitResult{TxnID: txnID, Committed: true, FilesWritten: renamed}, nil
	case StateOpen:
		// 标记未发布：事务仍未闭合，按失败序回滚（B-R2 还原前像 / create 移入 quarantine）后写 abort。
		return commitRollback(vaultRoot, txnID, cause)
	default:
		return nil, &CommitStateError{TxnID: txnID,
			Reason: fmt.Sprintf("commit 标记发布失败后事务处于 %s（原因：%v），fail closed", stateName(entry.State), cause)}
	}
}

// commitRollback 走 recover.go 的两遍恢复回滚**本事务**并写 abort（严禁先写 abort 再回滚）。
// 返回原始失败错误（回滚成功时保留 cause 供调用方判因；回滚自身失败则合并上报）。
func commitRollback(vaultRoot, txnID string, cause error) (*CommitResult, error) {
	entry, cerr := classifyOne(vaultRoot, txnID)
	if cerr != nil {
		return nil, errors.Join(cause, cerr)
	}
	if entry.State != StateOpen {
		// 已被其它路径闭合（例如并发恢复）：直接回传 cause。
		return &CommitResult{TxnID: txnID, RolledBack: entry.State == StateAborted}, cause
	}
	if _, rerr := rollbackAndAbort(vaultRoot, entry); rerr != nil {
		return nil, errors.Join(cause, rerr)
	}
	return &CommitResult{TxnID: txnID, RolledBack: true}, cause
}

// matchCommitInput 复核写集合与 intent 逐项一致，返回 path→target 字节映射。
func matchCommitInput(txnID string, intent *Intent, in CommitInput) (map[string][]byte, error) {
	if len(in.Files) != len(intent.Files) {
		return nil, &CommitInputError{TxnID: txnID,
			Reason: fmt.Sprintf("写集合 %d 项，intent 声明 %d 项", len(in.Files), len(intent.Files))}
	}
	byPath := make(map[string][]byte, len(in.Files))
	for _, cf := range in.Files {
		if _, dup := byPath[cf.Path]; dup {
			return nil, &CommitInputError{TxnID: txnID, Reason: "写集合含重复 path：" + cf.Path}
		}
		byPath[cf.Path] = cf.TargetBytes
	}
	for _, f := range intent.Files {
		data, ok := byPath[f.Path]
		if !ok {
			return nil, &CommitInputError{TxnID: txnID, Reason: "写集合缺少 intent 声明的 path：" + f.Path}
		}
		if int64(len(data)) != f.TargetSize {
			return nil, &CommitInputError{TxnID: txnID,
				Reason: fmt.Sprintf("%s 目标大小 %d 与 intent 声明 %d 不符", f.Path, len(data), f.TargetSize)}
		}
		if HashBytes(data) != f.TargetHash {
			return nil, &CommitInputError{TxnID: txnID, Reason: f.Path + " 目标哈希与 intent 声明不符"}
		}
	}
	return byPath, nil
}

// stagedFile 记录一个已备料文件：目标绝对路径、临时文件路径、目标所在目录、intent 下标。
type stagedFile struct {
	abs   string
	tmp   string
	dir   string
	index int
}

// stageTemp 在目标同目录内以**确定性名**（stageTempName）写临时文件并 fsync（同文件系统，供后续原子 rename）。
// 目标父目录不存在则创建（create:true 的目标可能落在尚不存在的子目录下）。
// 先清掉本事务同名残留（只可能是本 txn 上一次崩溃遗留的临时文件），再以 O_EXCL 独占创建：
// 这样既保证崩溃后确定性可清理（P0-1），又保证绝不覆盖任何已有权威文件（保留前缀天然不与 .md 重名）。
func stageTemp(txnID string, index int, phase, abs string, data []byte) (tmp, dir string, err error) {
	dir = filepath.Dir(abs)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	name := filepath.Join(dir, stageTempName(txnID, index, phase))
	if rmErr := os.Remove(name); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return "", "", rmErr
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", "", err
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", "", werr
	}
	if serr := f.Sync(); serr != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", "", serr
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(name)
		return "", "", cerr
	}
	// 与 M1~M4 store 落盘一致的权威文件权限（消解 umask 影响）。
	if cerr := os.Chmod(name, 0o644); cerr != nil {
		_ = os.Remove(name)
		return "", "", cerr
	}
	return name, dir, nil
}

// cleanupTemps 尽力删除尚未 rename 的临时文件（失败清理，不影响正确性）。
func cleanupTemps(staged []stagedFile) {
	for _, s := range staged {
		_ = os.Remove(s.tmp)
	}
}

// uniqueDirs 返回 staged 里去重后的目标目录（保持首次出现次序）。
func uniqueDirs(staged []stagedFile) []string {
	seen := make(map[string]bool, len(staged))
	var out []string
	for _, s := range staged {
		if !seen[s.dir] {
			seen[s.dir] = true
			out = append(out, s.dir)
		}
	}
	return out
}

// writeAuthoritative 原子写单个权威文件（tmp + fsync + rename + 目录 fsync）。
// 供 recover.go 的 B-R2 前像回滚复用——临时文件使用本事务该下标的确定性 restore 名，
// 崩溃在 write 与 rename 之间时可由下次恢复的 remove-then-create 幂等收拾（不留孤儿）。
func writeAuthoritative(txnID string, index int, abs string, data []byte) error {
	tmp, dir, err := stageTemp(txnID, index, stagePhaseRestore, abs, data)
	if err != nil {
		return err
	}
	if rerr := os.Rename(tmp, abs); rerr != nil {
		_ = os.Remove(tmp)
		return rerr
	}
	return fsyncDirPath(dir)
}

// stateName 给出 TxnState 的人类可读名（仅用于诊断消息）。
func stateName(s TxnState) string {
	switch s {
	case StateOpen:
		return "open"
	case StateCommitted:
		return "committed"
	case StateAborted:
		return "aborted"
	case StateCorrupt:
		return "corrupt"
	case StateResidue:
		return "residue"
	default:
		return "unknown"
	}
}

// authoritativeAbs 把 vault 相对路径拼成绝对路径（路径合法性由 ValidateIntentPaths 保证）。
func authoritativeAbs(vaultRoot, rel string) string {
	return filepath.Join(vaultRoot, filepath.FromSlash(rel))
}

// fsyncDirPath 打开目录并 Fsync（使 rename / 创建的目录项落盘）。
func fsyncDirPath(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if serr := d.Sync(); serr != nil {
		_ = d.Close()
		return serr
	}
	return d.Close()
}

// classifyOne 只读复核单个事务目录的分类（复用 Scan 的全量分类，取其中一项）。
// 不存在 ⇒ StateResidue（无可提交内容）。
func classifyOne(vaultRoot, txnID string) (TxnEntry, error) {
	res, err := Scan(vaultRoot)
	if err != nil {
		return TxnEntry{}, err
	}
	for _, e := range res.Entries {
		if e.TxnID == txnID {
			return e, nil
		}
	}
	return TxnEntry{TxnID: txnID, Dir: TxnDirPath(vaultRoot, txnID), State: StateResidue}, nil
}
