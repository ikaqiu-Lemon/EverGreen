package txn

// [S5] recover.go —— M6 崩溃恢复：两遍模型（合同 §4.1 / §4.1.1 / §4.1.2 / §6 矩阵）。
//
// 恢复是写命令启动屏障 S2 的**纯 txn 层内核**（CLI hook / 退出码映射不在本包，见 doc.go 依赖禁令）。
// 逐字落地合同的三层不变式：
//
//	① 全局只读分类先于任何写盘（§4.1.2）：
//	   先 Scan 全集，再对全部 OpenTxn 逐个跑 Pass A（只读）。只有
//	   「OpenTxn 恰一个 + 其 Pass A 判定全量可恢复 + 零 CorruptTxn」才允许进入 Pass B；
//	   多于一个 OpenTxn、任一 CorruptTxn、任一 B-R3 / 路径违规 / 前像不可用 ⇒
//	   在**任何回滚写发生之前**整体阻断：事务、residue、权威文件**全部零写**，
//	   返回携 E15 的类型化错误，不发 W26。严禁「先恢复一个再发现异常才报错」。
//
//	② 逐文件三分支（§4.1 / §4.1.1）：Pass A 对唯一 OpenTxn 的 files[] 全表——
//	   先复核路径（§4.3）、再校验前像本身（create:false 的 pre_bytes_ref 必须存在、
//	   是普通文件、size==pre_size、全文哈希==pre_hash；create:true 必须无 pre_bytes_ref），
//	   再重新读盘算 current_hash 分类：
//	     B-R1 current==pre_hash            → 无需写，Pass B 跳过；
//	     B-R2 current==target_hash         → 本事务写出的目标态，Pass B 还原前像；
//	                                         （create:true 则原子移入 quarantine/<index>，不物理删字节）
//	     B-R3 二者皆非（含 create:false 文件缺失 / 非普通文件、create:true 文件被改写）
//	                                       → post-crash 外部编辑冲突，整事务零写、E15、不发 W26。
//	   Pass A 严格零写盘（不 write/rename/unlink/truncate 任何文件，含 abort 与 quarantine 移动）。
//
//	③ Pass B 写盘（仅在 Pass A 判定全量可恢复时进入，§4.1）：
//	   B-R1 跳过、B-R2 幂等回滚前像（tmp+fsync(tmp)+rename+fsync(目录)）、
//	   create:true 原子移入 quarantine/<index>（fsync 源、目标两侧目录）；
//	   **全部文件处置完成后**才原子写 abort（MarkAbort），最后产**一条** W26。
//	   commit 标记在盘 ⇒ 已提交，不回滚、不发 W26（§6 P5~P8）；
//	   回滚中途再次崩溃（abort 未落盘）⇒ 仍判未闭合，下次重跑 Pass A+B，按「current==pre 则跳过」保证幂等。
//	   residue（无 intent.json）不阻断、不发 W26，**只在整体裁决通过之后**静默清理。
//
// §6 矩阵在本层的可观察投影：P1（intent 屏障完成前崩溃）⇒ residue（无 W26，退 0 层面 = Recover 无回滚）；
// P2/P3/P4（intent 已发布、commit 缺席，0/k/N 个权威 rename）⇒ 回滚到前像 + 一条 W26；
// P5~P8（commit 标记在盘）⇒ 不回滚、不发 W26；P9（S7 Git 失败）是 CLI/Git 层非崩溃路径，不属本包。
//
// 写入面（除此之外本包一个字节都不写）：校验通过的 files[].path（B-R2 回滚）、本事务目录
// .index/txn/<txn_id>/{quarantine/,abort}、整体裁决通过后 residue 目录的自有清理，
// 以及**本事务自有的确定性备料残留临时文件**（.eg-txn-<txn_id>-<index>.{stage,restore}）。
// 对权威 vault 路径的 os.Remove **只作用于本事务确定性推导出的临时名**（保留前缀天然不与 .md 重名，
// 见 commit.go stageTempName）——绝不通配、绝不删除任何权威字节；create 回滚仍用 rename 移入 quarantine
// （不物理删字节），residue 清理只作用于 .index/txn/ 自有目录。这修复了 P0-1：崩溃在备料后 / 部分 rename 后
// 遗留在权威目录里的 stage 临时文件，此前 Recover 无从得知、导致 P2/P3 并非完整收敛。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// CodeTxnRecovered 是「未闭合事务已回滚到前像」的留痕码（合同 §12 五码之一 W26，本包在 072 产出）。
// 只在 §6 矩阵 P2/P3/P4（commit 缺席 ⇒ 真实回滚）后产出，整事务一条；其余一律不发。
const CodeTxnRecovered = "W26"

// RecoverOutcome 是一次 Recover 的整体结果类别。
type RecoverOutcome int

const (
	// RecoverNoop 无未闭合事务需回滚（可能顺带清理了 residue），不产 W26。
	RecoverNoop RecoverOutcome = iota
	// RecoverRolledBack 恰一未闭合事务已两遍恢复回滚并写 abort，产一条 W26。
	RecoverRolledBack
)

// RecoverResult 是 Recover 成功（含无操作）时的回执。
type RecoverResult struct {
	Outcome        RecoverOutcome
	TxnID          string // 仅 RolledBack 时非空
	Diagnostics    []Diag // RolledBack 时含恰一条 W26；无操作为空
	ResidueCleaned []string
	// Restored 是本次恢复**真的改动过字节**的权威文件（vault 内相对路径，升序去重）。
	//
	// 只含 Pass B 的 B-R2 两支（回写前像 actRestorePre / 移入隔离区 actQuarantine）；
	// B-R1（current 本就等于前像，零写入）不进这份清单 —— 它没有让任何调用方的观测过期。
	//
	// 谁需要它（I-…-022）：调用方在锁外采过一次前像、锁内才过恢复屏障；恢复一旦回滚了
	// 其中某个文件，那份采样就只对这些**具体路径**过期。把清单如实交出去，调用方才能
	// 只重采被恢复动过的那几个，而不是把「文件变了」这条判据整体作废。
	Restored []string
}

// RecoverConflictError 是 Pass A 判出 post-crash 外部编辑冲突（B-R3）的 fail closed 错误（携 E15）。
// 整事务零写入、保持未闭合、不写 abort、不发 W26；报告列全所有冲突项及三个哈希供人工处置。
type RecoverConflictError struct {
	TxnID     string
	Conflicts []ConflictFile
}

// ConflictFile 是一项 B-R3 冲突的如实记录（三哈希直供报告）。
type ConflictFile struct {
	Path        string
	PreHash     string
	TargetHash  string
	CurrentHash string // 文件缺失 / 非普通文件时为空串或占位说明
	Reason      string
}

func (e *RecoverConflictError) Error() string {
	return fmt.Sprintf("事务 %s 恢复被阻断：%d 个文件在崩溃后被外部编辑（current 既非前像也非目标态），整事务零写入",
		e.TxnID, len(e.Conflicts))
}
func (e *RecoverConflictError) Code() string { return CodePrecheckFailed }
func (e *RecoverConflictError) Diagnostics() []Diag {
	out := make([]Diag, 0, len(e.Conflicts))
	for _, c := range e.Conflicts {
		out = append(out, Diag{Code: CodePrecheckFailed, Level: LevelError, Path: c.Path,
			Message: fmt.Sprintf("post-crash 外部编辑冲突：%s（pre=%s target=%s current=%s）",
				c.Reason, c.PreHash, c.TargetHash, c.CurrentHash)})
	}
	return out
}

// PreimageUnavailableError 是 Pass A 前像可用性校验失败（§4.1.1）的 fail closed 错误（携 E15）。
// 前像缺失 / 非普通文件 / size 不符 / 哈希不符 ⇒ 绝不进入 Pass B（否则会把错误字节写回权威 Markdown）。
type PreimageUnavailableError struct {
	TxnID  string
	Path   string
	Ref    string
	Reason string
}

func (e *PreimageUnavailableError) Error() string {
	return fmt.Sprintf("事务 %s 前像不可用（path=%s ref=%s）：%s，恢复零写入 fail closed",
		e.TxnID, e.Path, e.Ref, e.Reason)
}
func (e *PreimageUnavailableError) Code() string { return CodePrecheckFailed }
func (e *PreimageUnavailableError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.Path, Message: e.Error()}}
}

// RecoverReadError 是 Pass A 重算权威文件 current_hash 时读盘失败（非 ENOENT）的写前阻断错误（携 E15）。
// 这是「无法安全判定当前态」的 fail closed：绝不进入 Pass B，整事务零写入，供 074 映射退 5（P1 评审项）。
type RecoverReadError struct {
	TxnID string
	Path  string
	Err   error
}

func (e *RecoverReadError) Error() string {
	return fmt.Sprintf("事务 %s 恢复读盘失败（path=%s）：%v，写前阻断 fail closed", e.TxnID, e.Path, e.Err)
}
func (e *RecoverReadError) Unwrap() error { return e.Err }
func (e *RecoverReadError) Code() string  { return CodePrecheckFailed }
func (e *RecoverReadError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.Path, Message: e.Error()}}
}

// 编译期自证：新增阻断错误实现 coded 契约（E15 可被 074 的 ExitCodeFor 映射）。
var (
	_ coded = (*RecoverConflictError)(nil)
	_ coded = (*PreimageUnavailableError)(nil)
	_ coded = (*RecoverReadError)(nil)
)

// recoverFailpoint 仅供**包内测试**注入（生产恒 nil）。在 Pass B 的每个写盘步骤后以 (step, index)
// 回调，返回非 nil 即在该点立即中止（模拟回滚途中崩溃）。用于反证「回滚中崩溃 ⇒ abort 未落盘 ⇒
// 仍判未闭合 ⇒ 下次重跑并完成、结果幂等」。
var recoverFailpoint func(step string, index int) error

const (
	fpRecoverBeforeRestore    = "recover_before_restore"    // 还原第 index 个 B-R2 前像之前
	fpRecoverAfterRestore     = "recover_after_restore"     // 还原第 index 个 B-R2 前像之后
	fpRecoverBeforeQuarantine = "recover_before_quarantine" // 移入 quarantine 第 index 项之前
	fpRecoverBeforeAbort      = "recover_before_abort"      // 全部文件处置完成、写 abort 之前
)

func fireRecoverFailpoint(step string, index int) error {
	if recoverFailpoint != nil {
		return recoverFailpoint(step, index)
	}
	return nil
}

// Recover 执行合同 §4.1 的两遍崩溃恢复。返回成功回执或携 E15 的类型化 fail closed 错误。
//
// 时序：Scan 全集 → 全局裁决（Blocked 拦多 Open / 任一 Corrupt）→ 对唯一 OpenTxn 跑 Pass A（只读）
// → 仅全量可恢复才 Pass B 回滚 + MarkAbort + 一条 W26 → 整体裁决通过后静默清理 residue。
func Recover(vaultRoot string) (*RecoverResult, error) {
	res, err := Scan(vaultRoot)
	if err != nil {
		return nil, err
	}

	// 全局裁决先于任何写盘：多于一个 OpenTxn、或存在任一 CorruptTxn ⇒ 整体阻断，零写入、不发 W26。
	if berr := res.Blocked(); berr != nil {
		return nil, berr // *ScanBlockedError（携 E15）
	}

	result := &RecoverResult{Outcome: RecoverNoop}

	// Blocked()==nil ⇒ 至多一个 OpenTxn、零 CorruptTxn。
	opens := res.Open()
	if len(opens) == 1 {
		rr, rerr := rollbackAndAbort(vaultRoot, opens[0])
		if rerr != nil {
			return nil, rerr // Pass A fail closed（B-R3 / 前像不可用 / 路径违规），整事务零写入
		}
		result.Outcome = rr.Outcome
		result.TxnID = rr.TxnID
		result.Diagnostics = rr.Diagnostics
		result.Restored = rr.Restored // 逐字段搬运：漏一个字段就等于对外少交代一件已发生的事
	}

	// residue 清理**只能在整体裁决通过之后**进行（此刻已确认无 Corrupt、Open 已妥善处置）。
	cleaned, cerr := cleanResidues(vaultRoot, res.Residue())
	if cerr != nil {
		return result, cerr
	}
	result.ResidueCleaned = cleaned
	return result, nil
}

// rollbackAndAbort 对**单个** OpenTxn 跑 Pass A（只读判定）+ Pass B（写盘回滚）+ MarkAbort + W26。
// 供 Recover 与 commit.go 的失败回滚路径共用（commit 失败时保持事务未闭合、按恢复流程回滚，见 commit.go）。
// Pass A 任一冲突 / 前像不可用 / 路径违规 ⇒ 整事务零写入、不写 abort、返回携 E15 的类型化错误、不发 W26。
func rollbackAndAbort(vaultRoot string, entry TxnEntry) (*RecoverResult, error) {
	plan, perr := passAClassify(vaultRoot, entry)
	if perr != nil {
		return nil, perr
	}
	if err := passBRollback(vaultRoot, entry, plan); err != nil {
		return nil, err
	}
	return &RecoverResult{
		Outcome: RecoverRolledBack,
		TxnID:   entry.TxnID,
		Diagnostics: []Diag{{Code: CodeTxnRecovered, Level: LevelWarning, Path: entry.TxnID,
			Message: fmt.Sprintf("已恢复未闭合事务 %s：回滚全部权威文件到前像并写 abort", entry.TxnID)}},
		Restored: restoredPaths(plan),
	}, nil
}

// restoredPaths 取 Pass B 计划里**真的写过盘**的权威路径（升序去重）。
//
// 判据只看 action：actRestorePre / actQuarantine 改了字节，actSkip 没改。分类不在这里
// 重算 —— 它是 Pass A 的裁决，本函数只如实汇报，避免出现第二套「算不算被恢复」的口径。
func restoredPaths(plans []filePlan) []string {
	seen := make(map[string]struct{}, len(plans))
	out := make([]string, 0, len(plans))
	for _, p := range plans {
		if p.action != actRestorePre && p.action != actQuarantine {
			continue
		}
		if _, dup := seen[p.file.Path]; dup {
			continue
		}
		seen[p.file.Path] = struct{}{}
		out = append(out, p.file.Path)
	}
	sort.Strings(out)
	return out
}

// rollbackAction 是 Pass A 对单个 file 的三分支判定结果。
type rollbackAction int

const (
	actSkip       rollbackAction = iota // B-R1：current==pre，无需写
	actRestorePre                       // B-R2：current==target，写回前像字节
	actQuarantine                       // B-R2 且 create:true：current==target，原子移入 quarantine
)

// filePlan 是 Pass A 对单个 file 的处置计划（Pass B 据此写盘，绝不重新判定）。
type filePlan struct {
	index    int
	file     IntentFile
	action   rollbackAction
	preBytes []byte // 仅 actRestorePre 非 nil
	// stageTempPath 是本事务该下标在权威目录里的 commit 备料残留临时文件（确定性推导，Pass A 只读求得）。
	// Pass B 无论 action 为何都会尝试清理它（ENOENT 安全）——修复 P0-1：崩溃后遗留在权威目录的 stage tmp。
	stageTempPath string
}

// passAClassify 是 Pass A：对单个 OpenTxn 的 files[] 全表做**只读**校验与三分支分类。
// 严格零写盘；任一路径违规 / 前像不可用 / B-R3 ⇒ 返回携 E15 的类型化错误（不提前 break，先收全冲突再报）。
func passAClassify(vaultRoot string, entry TxnEntry) ([]filePlan, error) {
	intent := entry.Intent
	if intent == nil {
		// 理论不可达（Open ⇒ intent 已解析）；防御性 fail closed。
		return nil, &RecoverConflictError{TxnID: entry.TxnID}
	}
	// ① 路径合法性复核（§4.3）——Scan 已在结构校验时过一遍，这里防御性重校验，先校验后用。
	if perr := ValidateIntentPaths(vaultRoot, entry.TxnID, intent); perr != nil {
		return nil, perr // *PathViolationError（携 E15）
	}

	txnDir := TxnDirPath(vaultRoot, entry.TxnID)
	plans := make([]filePlan, 0, len(intent.Files))
	var conflicts []ConflictFile

	for i := range intent.Files {
		f := intent.Files[i]
		authAbs := authoritativeAbs(vaultRoot, f.Path)
		// 本事务该下标的 commit 备料残留临时文件（确定性推导；只读求路径，不触盘）。
		stageTmp := filepath.Join(filepath.Dir(authAbs), stageTempName(entry.TxnID, i, stagePhaseCommit))

		// ② 前像可用性校验（§4.1.1）：create:false 必须有可信前像；create:true 必须无前像引用。
		var preBytes []byte
		if !f.Create {
			pb, perr := loadVerifiedPreimage(entry.TxnID, txnDir, f)
			if perr != nil {
				return nil, perr // 前像不可用即整体 fail closed（早返回，绝不进入 Pass B）
			}
			preBytes = pb
		}

		// ③ 重新读盘算 current_hash 并三分支分类。
		curHash, curExists, regular, rerr := currentFileHash(authAbs)
		if rerr != nil {
			return nil, &RecoverReadError{TxnID: entry.TxnID, Path: f.Path, Err: rerr} // 写前阻断（携 E15）
		}

		if f.Create {
			switch {
			case !curExists:
				plans = append(plans, filePlan{index: i, file: f, action: actSkip, stageTempPath: stageTmp}) // B-R1：本就不存在
			case regular && curHash == f.TargetHash:
				plans = append(plans, filePlan{index: i, file: f, action: actQuarantine, stageTempPath: stageTmp}) // B-R2：移入隔离区
			default:
				conflicts = append(conflicts, ConflictFile{Path: f.Path, PreHash: "", TargetHash: f.TargetHash,
					CurrentHash: currentHashDisplay(curHash, curExists, regular),
					Reason:      "新建文件在崩溃后被外部改写（current 非本事务目标态）"})
			}
			continue
		}

		switch {
		case regular && curHash == f.PreHash:
			plans = append(plans, filePlan{index: i, file: f, action: actSkip, stageTempPath: stageTmp}) // B-R1
		case regular && curHash == f.TargetHash:
			plans = append(plans, filePlan{index: i, file: f, action: actRestorePre, preBytes: preBytes, stageTempPath: stageTmp}) // B-R2
		default:
			conflicts = append(conflicts, ConflictFile{Path: f.Path, PreHash: f.PreHash, TargetHash: f.TargetHash,
				CurrentHash: currentHashDisplay(curHash, curExists, regular),
				Reason:      "current 既非前像也非目标态（post-crash 外部编辑 / 缺失 / 非普通文件）"})
		}
	}

	if len(conflicts) > 0 {
		return nil, &RecoverConflictError{TxnID: entry.TxnID, Conflicts: conflicts}
	}
	return plans, nil
}

// passBRollback 是 Pass B：按 Pass A 的计划逐项写盘回滚，全部完成后才原子写 abort。
// 仅在 Pass A 判定「全量可恢复」后被调用；本函数不重新判定、不改变分类。
func passBRollback(vaultRoot string, entry TxnEntry, plans []filePlan) error {
	txnDir := TxnDirPath(vaultRoot, entry.TxnID)
	for _, p := range plans {
		authAbs := authoritativeAbs(vaultRoot, p.file.Path)
		switch p.action {
		case actSkip:
			// B-R1：零权威写入（但仍可能有备料残留 tmp 待清，见下）。
		case actRestorePre:
			if ferr := fireRecoverFailpoint(fpRecoverBeforeRestore, p.index); ferr != nil {
				return ferr
			}
			if werr := writeAuthoritative(entry.TxnID, p.index, authAbs, p.preBytes); werr != nil {
				return werr
			}
			if ferr := fireRecoverFailpoint(fpRecoverAfterRestore, p.index); ferr != nil {
				return ferr
			}
		case actQuarantine:
			if ferr := fireRecoverFailpoint(fpRecoverBeforeQuarantine, p.index); ferr != nil {
				return ferr
			}
			if qerr := quarantineCreated(txnDir, authAbs, p.index); qerr != nil {
				return qerr
			}
		}
		// P0-1：清理本事务该下标遗留在权威目录里的 commit 备料临时文件（确定性名，ENOENT 安全）。
		// 成功 rename 的文件其临时名已被消费；崩溃遗留者在此静默收敛，恢复后不留任何本事务 tmp。
		if rmErr := removeStageTemp(p.stageTempPath); rmErr != nil {
			return rmErr
		}
	}
	if ferr := fireRecoverFailpoint(fpRecoverBeforeAbort, -1); ferr != nil {
		return ferr // 回滚已完成但 abort 未落盘：保持未闭合，下次重跑（幂等）
	}
	return MarkAbort(vaultRoot, entry.TxnID)
}

// removeStageTemp 删除本事务确定性推导出的备料残留临时文件并 fsync 其目录（使删除项落盘）。
// 仅作用于单个确定性路径（绝不通配、绝不删权威字节）；文件不存在即视作已收敛（ENOENT 安全）。
func removeStageTemp(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return fsyncDirPath(filepath.Dir(path))
}

// loadVerifiedPreimage 读取并校验 create:false 项的前像副本（§4.1.1）：ref 必须严格等于 pre/<index>、
// 目标必须是普通文件（非目录 / 非 symlink）、size==pre_size、全文哈希==pre_hash。**只读**。
func loadVerifiedPreimage(txnID, txnDir string, f IntentFile) ([]byte, error) {
	ref := f.PreBytesRef
	if ref == "" {
		return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref,
			Reason: "create:false 缺前像引用"}
	}
	abs := filepath.Join(txnDir, filepath.FromSlash(ref))
	// Lstat 判 symlink / 目录 / 特殊文件（不跟随）。
	li, lerr := os.Lstat(abs)
	if lerr != nil {
		if errors.Is(lerr, os.ErrNotExist) {
			return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref, Reason: "前像副本缺失"}
		}
		return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref, Reason: "前像副本无法 lstat：" + lerr.Error()}
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref, Reason: "前像副本是 symlink（拒绝跟随）"}
	}
	if !li.Mode().IsRegular() {
		return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref,
			Reason: fmt.Sprintf("前像副本不是普通文件（mode=%s）", li.Mode())}
	}
	if li.Size() != f.PreSize {
		return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref,
			Reason: fmt.Sprintf("前像副本 size=%d 与 intent 声明 pre_size=%d 不符", li.Size(), f.PreSize)}
	}
	data, rerr := os.ReadFile(abs)
	if rerr != nil {
		return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref, Reason: "前像副本读取失败：" + rerr.Error()}
	}
	if HashBytes(data) != f.PreHash {
		return nil, &PreimageUnavailableError{TxnID: txnID, Path: f.Path, Ref: ref, Reason: "前像副本内容哈希与 intent 声明 pre_hash 不符"}
	}
	return data, nil
}

// currentFileHash 只读重算权威文件的 current_hash。
// 返回 (hash, exists, regular, err)：ENOENT ⇒ exists=false；非普通文件（symlink/dir/设备）⇒ regular=false。
func currentFileHash(abs string) (hash string, exists, regular bool, err error) {
	li, lerr := os.Lstat(abs)
	if lerr != nil {
		if errors.Is(lerr, os.ErrNotExist) {
			return "", false, false, nil
		}
		return "", false, false, lerr
	}
	if li.Mode()&os.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return "", true, false, nil // 存在但非普通文件 ⇒ 交由调用方判 B-R3
	}
	data, rerr := os.ReadFile(abs)
	if rerr != nil {
		return "", true, true, rerr
	}
	return HashBytes(data), true, true, nil
}

// currentHashDisplay 为报告拼一个人类可读的 current 态说明。
func currentHashDisplay(hash string, exists, regular bool) string {
	switch {
	case !exists:
		return "<缺失>"
	case !regular:
		return "<非普通文件>"
	default:
		return hash
	}
}

// quarantineCreated 把 create:true 新建文件原子移入 .index/txn/<txn_id>/quarantine/<index>，
// fsync 源目录与隔离区目录两侧（字节完整保留、可人工取回，**绝不物理删除**）。
func quarantineCreated(txnDir, authAbs string, index int) error {
	qDir := filepath.Join(txnDir, QuarantineDir)
	if err := os.MkdirAll(qDir, runtimeDirMode); err != nil {
		return fmt.Errorf("创建隔离区失败：%w", err)
	}
	dst := filepath.Join(qDir, fmt.Sprintf("%d", index))
	if err := os.Rename(authAbs, dst); err != nil {
		return fmt.Errorf("移入隔离区失败（%s → %s）：%w", authAbs, dst, err)
	}
	// fsync 两侧目录，使 rename 的目录项落盘。
	if err := fsyncDirPath(qDir); err != nil {
		return err
	}
	return fsyncDirPath(filepath.Dir(authAbs))
}

// cleanResidues 在整体裁决通过后静默清理 pre-intent residue（无 intent.json 的孤儿目录）。
// 只作用于 .index/txn/ 自有目录（相对已解析根 fd 的 unlinkat 行走，见 journal.go 的 removeTxnEntryAt），
// 权威文件零触碰、不发 W26。
func cleanResidues(vaultRoot string, residues []TxnEntry) ([]string, error) {
	if len(residues) == 0 {
		return nil, nil
	}
	rootFile, err := openRuntimeDir(vaultRoot, []string{IndexDirName, TxnDirName}, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer rootFile.Close()
	rootFd := int(rootFile.Fd())
	var cleaned []string
	for _, e := range residues {
		if rerr := removeTxnEntryAt(rootFd, e.TxnID); rerr != nil {
			return cleaned, fmt.Errorf("清理 residue %s 失败：%w", e.TxnID, rerr)
		}
		cleaned = append(cleaned, e.TxnID)
	}
	return cleaned, nil
}
