package txn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// SupportedJournalVersion 是本实现能解释的唯一 journal 版本；其他版本 ⇒ 损坏事务 fail closed。
const SupportedJournalVersion = 1

// 事务日志的固定子路径（合同 §4）。
const (
	IntentFileName = "intent.json"
	intentTmpName  = "intent.json.tmp"
	CommitMarker   = "commit"
	AbortMarker    = "abort"
	PreDirName     = "pre"
	QuarantineDir  = "quarantine"

	// HashAlgoPrefix 与 internal/store.ContentHash 逐字一致，使 target_hash / pre_hash
	// 可与权威写口算出的哈希直接比对（本包不 import store，只复制该字面量约定）。
	HashAlgoPrefix = "sha256:"

	markerPerm = 0o644
)

// 默认保留策略（合同 §4：闭合事务保留 7 天或最近 20 个，先到者为准）。
const (
	defaultPruneMaxAge  = 7 * 24 * time.Hour
	defaultPruneMaxKeep = 20
)

// Intent 是 intent.json 的结构（合同 §4，字段只增不改）。
type Intent struct {
	TxnID          string        `json:"txn_id"`
	StartedAt      string        `json:"started_at"`
	Argv           []string      `json:"argv"`
	Files          []IntentFile  `json:"files"`
	Skipped        []SkippedFile `json:"skipped"`
	Git            GitIntent     `json:"git"`
	JournalVersion int           `json:"journal_version"`
}

// IntentFile 是 accepted write-set 的一项，同时携前像与目标态（§4.1 三分支判定的必要输入）。
type IntentFile struct {
	Path        string `json:"path"`
	PreHash     string `json:"pre_hash"`
	PreSize     int64  `json:"pre_size"`
	PreBytesRef string `json:"pre_bytes_ref,omitempty"`
	TargetHash  string `json:"target_hash"`
	TargetSize  int64  `json:"target_size"`
	Create      bool   `json:"create"`
	TargetOp    string `json:"target_op"`
}

// SkippedFile 是 S3 preflight 定盘的跳过集合项（不属原子域，只用于报告 / 审计）。
type SkippedFile struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// skipped[].kind 的**封闭两值**（合同 §4「沿用既有 2 值，不扩张」）。字面量与 internal/store.SkipReason
// 逐字一致（本包不 import store，只复制该字面量约定，见 doc.go 的依赖禁令）。
const (
	SkipKindFileChanged     = "file_changed"
	SkipKindUserBlockUnsafe = "user_block_unsafe"
)

// GitIntent 记录本事务是否期望在 S7 产出一次 Git 提交（Git 结果不改变 Markdown 生效结论）。
type GitIntent struct {
	ExpectCommit bool `json:"expect_commit"`
}

// FileSpec 是 WriteIntent 的输入：调用方给出路径、是否新建、前像字节与目标字节。
// 前像 / 目标哈希由本包统一计算，避免调用方口径漂移。
type FileSpec struct {
	Path        string
	Create      bool
	PreBytes    []byte // create:false 时为当前权威字节；create:true 时忽略
	TargetBytes []byte // 本事务将写入的目标态字节
	TargetOp    string
}

// IntentInput 是 WriteIntent 的完整输入。
type IntentInput struct {
	Argv         []string
	ExpectCommit bool
	Files        []FileSpec
	Skipped      []SkippedFile
	Now          func() time.Time
}

// HashBytes 返回与 store.ContentHash 同口径的内容哈希（sha256: 前缀 + hex）。
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return HashAlgoPrefix + hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------- 阻断错误（携 E15）

// PathViolationError 是 intent 内路径违规（绝对路径 / .. / 未规范化 / symlink 逃逸）的类型化错误。
type PathViolationError struct {
	Field  string // "files[].path" 或 "files[].pre_bytes_ref"
	Value  string
	Reason string
}

func (e *PathViolationError) Error() string {
	return fmt.Sprintf("intent 路径违规（%s=%q）：%s", e.Field, e.Value, e.Reason)
}
func (e *PathViolationError) Code() string { return CodePrecheckFailed }
func (e *PathViolationError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.Value, Message: e.Error()}}
}

// BlockedDiagnoseHint 是阻断态诊断里**唯一**的 CLI 出路指引（短版，进 E15 的总述条）。
//
// 为什么写在诊断里：合同 §18.4 要求「给全部问题 `txn_id` 列表，**便于人工处置**」。
// 只给列表不给下一步，Agent 与运维只能「重投 → 退 5」死循环；这条指引把下一步钉在
// 一条**真实存在**的只读命令上（`eg check` / `eg check --strict` 会逐条列出事务与原因，
// 并给出完整人工出路）。**不新增命令**（合同 §13.4 命令数恒 22）。
const BlockedDiagnoseHint = "定位：跑 eg check（或 eg check --strict）逐条查看全部问题事务、" +
	"不可解析原因与人工出路"

// BlockedRemedyHint 是阻断态的**人工出路**全文（长版，供只读体检命令与文档共用一份口径）。
//
// 措辞里的风险交代是必须的：删除一个未闭合事务目录会连带丢弃它的前像副本，
// 该事务已写出的字节此后**无法**再被自动回滚 —— 不说这一句，"清理即可" 就是误导。
const BlockedRemedyHint = "人工出路（不新增命令）：① 逐个查看 " + IndexDirName + "/" + TxnDirName +
	"/<txn_id>/intent.json，判断该事务动过哪些权威文件；② 先把整个 " + IndexDirName + "/" + TxnDirName +
	"/<txn_id>/ 目录备份出去，再删除多余的未闭合事务目录（未闭合事务只允许留 1 个）与损坏事务目录；" +
	"③ 之后任一写命令会自动恢复剩下那个未闭合事务并留痕 W26；" +
	"④ 风险：删除事务目录会一并丢弃它的前像副本，该事务已写出的字节将无法自动回滚，需自行用 git 还原"

// BlockedRemedyHintOneLine 是同一份人工出路的**一行短版**：只交代「处置对象 + 之后会自动恢复并留痕」。
//
// # 为什么这句话必须住在本包（架构边界）
//
// `.index/txn/<txn_id>/` 这个**目录形态**与「下一条写命令自动恢复未闭合事务并留痕 W26」这件
// **恢复语义**，都是 S5 事务包的实现事实：路径由本包的 IndexDirName / TxnDirName 拼出，
// 留痕码由本包的 CodeTxnRecovered 持有。命令层需要在 `--help` 里把这件事如实告诉用户，
// 但**不得自带**这些字面量 —— 否则同一份口径在 cli / txn 各存一份（改一处漏一处），
// 且事务实现细节被渗进 CLI 层（这正是 full 门禁 TestNoOutOfScopeImplementation 判越界的那格）。
// 因此短版口径由本包给出，命令层只引用（落点见 internal/cli/check_txn.go 的
// checkTxnRemedyHelpLine，`eg check --help` 那句人工出路即由它拼出）。
const BlockedRemedyHintOneLine = "备份后清理 " + IndexDirName + "/" + TxnDirName +
	"/<txn_id>/，之后任一写命令会自动恢复并留痕 " + CodeTxnRecovered

// CorruptTxnRef 是一项损坏事务的如实记录：事务号 + **不可解析原因**（合同 §18.4）。
//
// 原因串直接搬运 Scan 的 CorruptReason，不在这里重述、不折叠成一句「损坏事务」——
// 「是 JSON 截断、还是缺必需字段、还是 journal_version 不支持」正是人工处置唯一的入手点。
type CorruptTxnRef struct {
	TxnID  string
	Reason string
}

// ScanBlockedError 是「全集裁决判定为整体阻断」的类型化结论（§4.1.2）：
// 存在任一 CorruptTxn 或多于一个 OpenTxn ⇒ 在任何回滚写之前整体阻断，权威零写入。
//
// 披露面（C2 · I-…-025 按合同 §18.4 补齐）：损坏事务逐个给 `txn_id` + 不可解析原因，
// 未闭合事务逐个给 `txn_id`，总述给**全部**问题 `txn_id` 列表 + CLI 出路；
// `txn_id` 一律落 `Target`，`Path` 只放真实目录路径。
type ScanBlockedError struct {
	// Corrupt 是逐个损坏事务的事务号与不可解析原因（**唯一**真源，不另留一份纯 ID 列表：
	// 两份清单必然漂移，而漂移出来的那份恰好是报告用的那份）。
	Corrupt  []CorruptTxnRef
	OpenTxns []string
}

// CorruptIDs 取损坏事务号（升序即 Scan 的目录序），供计数与列表渲染共用。
func (e *ScanBlockedError) CorruptIDs() []string {
	out := make([]string, 0, len(e.Corrupt))
	for _, c := range e.Corrupt {
		out = append(out, c.TxnID)
	}
	return out
}

func (e *ScanBlockedError) Error() string {
	return fmt.Sprintf("事务扫描整体阻断：%d 个损坏事务、%d 个未闭合事务（协议上限 1）；%s；%s",
		len(e.Corrupt), len(e.OpenTxns), e.idListText(), BlockedDiagnoseHint)
}

// idListText 拼「全部问题 txn_id 列表」（合同 §18.4 逐字要求的那份清单）。
func (e *ScanBlockedError) idListText() string {
	parts := make([]string, 0, 2)
	if len(e.Corrupt) > 0 {
		parts = append(parts, "损坏事务=["+strings.Join(e.CorruptIDs(), " ")+"]")
	}
	if len(e.OpenTxns) > 0 {
		parts = append(parts, "未闭合事务=["+strings.Join(e.OpenTxns, " ")+"]")
	}
	if len(parts) == 0 {
		return "问题事务列表=[]"
	}
	return strings.Join(parts, "；")
}

func (e *ScanBlockedError) Code() string { return CodePrecheckFailed }
func (e *ScanBlockedError) Diagnostics() []Diag {
	out := make([]Diag, 0, len(e.Corrupt)+len(e.OpenTxns)+1)
	for _, c := range e.Corrupt {
		out = append(out, Diag{Code: CodePrecheckFailed, Level: LevelError,
			Path: TxnDirRel(c.TxnID), Target: c.TxnID,
			Message: fmt.Sprintf("损坏事务 %s 不可解析原因：%s；目录 %s 原样保留（不删除、不改写），"+
				"权威文件零写入 fail closed", c.TxnID, corruptReasonText(c.Reason), TxnDirRel(c.TxnID))})
	}
	for _, id := range e.OpenTxns {
		out = append(out, Diag{Code: CodePrecheckFailed, Level: LevelError,
			Path: TxnDirRel(id), Target: id,
			Message: fmt.Sprintf("未闭合事务 %s（intent 已发布、commit / abort 均缺席）："+
				"本次整体阻断不做任何恢复，目录 %s 原样保留", id, TxnDirRel(id))})
	}
	out = append(out, Diag{Code: CodePrecheckFailed, Level: LevelError, Message: e.Error()})
	return out
}

// corruptReasonText 兜底：原因串缺失时如实说「未记录」，绝不编一个原因。
func corruptReasonText(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "intent.json 在盘但不可解析（Scan 未记录更细原因）"
	}
	return reason
}

// TxnDirRel 返回某个事务目录相对 vault 根的路径（`path` 位只放这种真实路径）。
func TxnDirRel(txnID string) string {
	return IndexDirName + "/" + TxnDirName + "/" + txnID
}

// blockedFromEntries 是「阻断结论」的**唯一**构造点：Blocked() 与 pruneBlockers 都从这里取，
// 两个入口只在「多少个 Open 才算阻断」这一个阈值上不同，其余口径（含原因搬运）不许有第二份。
func blockedFromEntries(entries []TxnEntry, openLimit int) *ScanBlockedError {
	var corrupt []CorruptTxnRef
	var open []string
	for _, e := range entries {
		switch e.State {
		case StateCorrupt:
			corrupt = append(corrupt, CorruptTxnRef{TxnID: e.TxnID, Reason: e.CorruptReason})
		case StateOpen:
			open = append(open, e.TxnID)
		}
	}
	if len(corrupt) > 0 || len(open) > openLimit {
		return &ScanBlockedError{Corrupt: corrupt, OpenTxns: open}
	}
	return nil
}

// ---------------------------------------------------------------- 路径校验（§4.3）

// ValidateIntentPaths 逐项校验 intent 的路径字段（合同 §4.3），任一违规返回 *PathViolationError。
// **先校验后写、写的就是被校验的对象**：WriteIntent 与恢复层都必须先过本入口，
// 严禁「校验一个路径却写另一个路径」。
func ValidateIntentPaths(vaultRoot, txnID string, intent *Intent) error {
	if intent == nil {
		return &PathViolationError{Field: "intent", Reason: "intent 为空"}
	}
	txnDir := TxnDirPath(vaultRoot, txnID)
	for i := range intent.Files {
		f := intent.Files[i]
		if err := validateVaultRelPath("files[].path", f.Path, vaultRoot); err != nil {
			return err
		}
		if f.Create {
			// 新建项无前像：必须缺席 pre_bytes_ref，否则日志自相矛盾。
			if f.PreBytesRef != "" || f.PreHash != "" {
				return &PathViolationError{Field: "files[].pre_bytes_ref", Value: f.PreBytesRef,
					Reason: "create:true 却带前像引用 / 前像哈希（日志自相矛盾）"}
			}
			continue
		}
		if f.PreBytesRef == "" {
			return &PathViolationError{Field: "files[].pre_bytes_ref", Value: "",
				Reason: "create:false 却缺前像引用（日志自相矛盾）"}
		}
		if err := validateTxnRelPath("files[].pre_bytes_ref", f.PreBytesRef, txnDir); err != nil {
			return err
		}
	}
	return nil
}

// validateVaultRelPath 校验「相对 vault 根的规范化相对路径」，含 symlink 逃逸判定。
func validateVaultRelPath(field, p, vaultRoot string) error {
	if err := lexicalRelClean(field, p); err != nil {
		return err
	}
	if err := ensureWithin(vaultRoot, p); err != nil {
		return &PathViolationError{Field: field, Value: p, Reason: err.Error()}
	}
	return nil
}

// validateTxnRelPath 校验「相对本事务目录的规范化相对路径」，且必须形如 pre/<n>。
func validateTxnRelPath(field, p, txnDir string) error {
	if err := lexicalRelClean(field, p); err != nil {
		return err
	}
	if !strings.HasPrefix(p, PreDirName+"/") {
		return &PathViolationError{Field: field, Value: p, Reason: "pre_bytes_ref 必须位于本事务 pre/ 之内"}
	}
	if err := ensureWithin(txnDir, p); err != nil {
		return &PathViolationError{Field: field, Value: p, Reason: err.Error()}
	}
	return nil
}

// lexicalRelClean 做纯词法校验：非空、非绝对、以 / 分隔、无 . / .. 分量、Clean 后与原串逐字相等。
func lexicalRelClean(field, p string) error {
	if p == "" {
		return &PathViolationError{Field: field, Value: p, Reason: "空路径"}
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return &PathViolationError{Field: field, Value: p, Reason: "不得为绝对路径"}
	}
	if strings.Contains(p, "\\") || (len(p) >= 2 && p[1] == ':') {
		return &PathViolationError{Field: field, Value: p, Reason: "不得含 Windows 盘符 / 反斜杠"}
	}
	// 用 slash 语义判定分量，避免平台差异；要求 path.Clean 后逐字相等。
	if cleaned := filepath.ToSlash(filepath.Clean(p)); cleaned != p {
		return &PathViolationError{Field: field, Value: p, Reason: "未规范化（Clean 后与原串不等）"}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "." || seg == ".." || seg == "" {
			return &PathViolationError{Field: field, Value: p, Reason: "含 . / .. / 空分量"}
		}
	}
	return nil
}

// ensureWithin 校验 rel 解析（含 symlink）后仍位于 baseDir 真实路径之内。
// 叶子可能尚不存在（create:true 的目标 / 尚未写的 pre 副本），因此对「最长已存在祖先」
// 做 EvalSymlinks，并要求其真实路径以 baseDir 真实路径为前缀。
func ensureWithin(baseDir, rel string) error {
	baseReal, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		// baseDir 尚不存在时退回其绝对路径（尚无 symlink 可逃逸）。
		baseReal, err = filepath.Abs(baseDir)
		if err != nil {
			return fmt.Errorf("无法解析基准目录 %s：%v", baseDir, err)
		}
	}
	target := filepath.Join(baseReal, filepath.FromSlash(rel))
	real := resolveLongestExisting(target)
	if !pathHasPrefix(real, baseReal) {
		return fmt.Errorf("经 symlink 逃逸出 %s", baseDir)
	}
	return nil
}

// resolveLongestExisting 对最长已存在的祖先做 EvalSymlinks，其余词法拼接。
func resolveLongestExisting(target string) string {
	cur := target
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{resolved}, reverse(tail)...)...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return filepath.Clean(target)
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func reverse(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

// pathHasPrefix 判断 p 是否等于 prefix 或位于其下（按路径分量，不做字符串前缀误判）。
func pathHasPrefix(p, prefix string) bool {
	p = filepath.Clean(p)
	prefix = filepath.Clean(prefix)
	if p == prefix {
		return true
	}
	return strings.HasPrefix(p, prefix+string(filepath.Separator))
}

// ---------------------------------------------------------------- WriteIntent（发布屏障，§4）

// IntentExistsError 是「事务目录里已存在 final intent.json」时 WriteIntent 的 fail closed 错误（携 E15）。
// intent.json 是恢复层唯一的判定输入，只能由本函数的 rename **发布一次**；若它已在盘（正常发布过、
// 或崩溃后残留的恢复证据），再次 WriteIntent **绝不静默覆盖**——覆盖会抹掉「已有权威写入」的唯一证据，
// 让 072 的恢复把一个可能已部分生效的事务误判为全新事务。
type IntentExistsError struct {
	Path   string
	Reason string
}

func (e *IntentExistsError) Error() string {
	return fmt.Sprintf("拒绝覆盖已存在的 intent.json（%s）：%s", e.Path, e.Reason)
}
func (e *IntentExistsError) Code() string { return CodePrecheckFailed }
func (e *IntentExistsError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Path: e.Path, Message: e.Error()}}
}

// MarkerConflictError 是 writeMarker 发布前两侧标记检查未通过时的 fail closed 错误（携 E15）。
// commit / abort 是 Markdown 事务互斥的终局标记，各自只能**单次**以合法普通零字节形态落盘：
//   - 对侧标记已在盘（任何形态）：再写同侧会主动制造「commit 与 abort 并存」的自相矛盾 Corrupt——拒绝；
//   - 同侧标记已在盘但**非普通文件 / 非零字节**：已损坏，rename 覆盖会抹掉这一损坏证据——拒绝；
//     （同侧合法普通零字节标记已在盘则是**幂等成功**，不落到本错误。）
//
// 落到本错误时 writeMarker **全程零写盘**：既不写 .tmp、也不 rename、绝不触碰任一已有标记的字节。
type MarkerConflictError struct {
	TxnID  string
	Marker string
	Reason string
}

func (e *MarkerConflictError) Error() string {
	return fmt.Sprintf("拒绝写事务 %s 的 %s 标记：%s", e.TxnID, e.Marker, e.Reason)
}
func (e *MarkerConflictError) Code() string { return CodePrecheckFailed }
func (e *MarkerConflictError) Diagnostics() []Diag {
	return []Diag{{Code: CodePrecheckFailed, Level: LevelError, Message: e.Error()}}
}

// writeIntentFailpoint 仅供**包内测试**注入（生产恒为 nil）。WriteIntent 在发布屏障的每个关键步骤后
// 以步骤名回调它；返回非 nil 即在该点**立即中止**（模拟崩溃 / 断电），既不吞错也不清理。
// 用途：反证「rename 之前任何点中止都不在盘上留下 final intent.json（Scan 判 residue）」，
// 以及「rename 之后返回错误不改变发布事实（Scan 判 Open，绝不误判 residue）」。
var writeIntentFailpoint func(step string) error

// 发布屏障的注入点名（稳定标识，供测试逐点枚举）。
const (
	fpBeforePre     = "before_pre"      // 写任何 pre/<n> 之前
	fpAfterPre      = "after_pre"       // 全部 pre/<n> 写完、Fsync(pre/) 之前
	fpAfterPreFsync = "after_pre_fsync" // Fsync(pre/) + Fsync(txn) 之后、写 tmp 之前
	fpAfterTmp      = "after_tmp"       // 写 + Fsync intent.json.tmp 之后、rename 之前
	fpBeforeRename  = "before_rename"   // rename(tmp→intent.json) 之前
	fpAfterRename   = "after_rename"    // rename 之后、最终 Fsync(txn) 之前（**发布已完成**）
)

func fireFailpoint(step string) error {
	if writeIntentFailpoint != nil {
		return writeIntentFailpoint(step)
	}
	return nil
}

// ensureIntentAbsent 在发布前确认事务目录里**尚无** final intent.json（openat + O_NOFOLLOW）。
// ENOENT ⇒ 正常首次发布；已存在（普通文件 / symlink / 其他）⇒ *IntentExistsError fail closed，
// 全程零写、绝不打开或截断已有 intent.json 的字节。
func ensureIntentAbsent(txnFd int, txnDir string) error {
	fd, err := sysOpenat(txnFd, IntentFileName, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil // 不存在 —— 唯一允许发布的情形
		}
		if errors.Is(err, syscall.ELOOP) {
			return &IntentExistsError{Path: filepath.Join(txnDir, IntentFileName), Reason: "已存在且是 symlink（拒绝覆盖 / 跟随）"}
		}
		return &IntentExistsError{Path: filepath.Join(txnDir, IntentFileName), Reason: "无法确认是否已存在：" + err.Error()}
	}
	_ = syscall.Close(fd)
	return &IntentExistsError{Path: filepath.Join(txnDir, IntentFileName), Reason: "已存在，拒绝二次发布 / 覆盖恢复证据"}
}

// WriteIntent 按合同 §4 的 intent 发布屏障逐字落地地写出 intent.json：
//
//	① 事务目录已由 AllocateTxnID 以 O_EXCL 建出；这里建 pre/；
//	② 逐个写 pre/<n> 并**逐个 Fsync**（不允许只在末尾统一刷）；
//	③ Fsync(pre/ 目录) 与 Fsync(txn 目录)——此刻全部前像字节及目录项均已持久；
//	④ 写 intent.json.tmp（全量 JSON 一次写完）→ Fsync(intent.json.tmp)；
//	⑤ rename(tmp → intent.json) → Fsync(txn 目录)。
//
// **返回成功 = 屏障已完成**：调用方（072）只有在此之后才允许首个权威 rename。
// intent.json **只能**由 rename 发布，磁盘上永不出现半写的 intent.json（半写只在 .tmp）。
// 写入前强制过 ValidateIntentPaths（先校验后写）。
func WriteIntent(vaultRoot, txnID string, in IntentInput) (*Intent, error) {
	if !ValidTxnID(txnID) {
		return nil, &PathViolationError{Field: "txn_id", Value: txnID, Reason: "非法 txn_id 格式"}
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}
	txnDir := TxnDirPath(vaultRoot, txnID)
	// 安全解析 .index/txn/<txnID>/（parent-symlink fail closed，见 safedir.go）：事务目录必须
	// 已由 AllocateTxnID 以 O_EXCL 建出且是**真实目录**；.index / txn / <txnID> 任一是 symlink /
	// 非目录即 fail closed。后续 pre/ 创建与 intent 落盘全部相对已解析的目录 fd（*at 族）。
	txnDirFile, err := openRuntimeDir(vaultRoot, []string{IndexDirName, TxnDirName, txnID}, false)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("事务目录 %s 不存在（应由 AllocateTxnID 先建出）", txnDir)
		}
		return nil, err
	}
	defer txnDirFile.Close()
	txnFd := int(txnDirFile.Fd())

	// 防覆盖（fail closed）：intent.json 只能由本函数的 rename **发布一次**。若已在盘，说明该事务
	// 已发布过或崩溃后残留了恢复证据——拒绝二次写，绝不静默覆盖 072 恢复层唯一的判定输入。
	if err := ensureIntentAbsent(txnFd, txnDir); err != nil {
		return nil, err
	}

	// argv / skipped / files 一律用 make(…, 0, n) 起底：即使入参为 nil，也保证 JSON 落成非 nil 空数组
	// `[]` 而非 `null`（合同：这些字段是真数组；Scan 侧 validateIntentStructure 会拒绝 null / 非数组）。
	argv := make([]string, 0, len(in.Argv))
	argv = append(argv, in.Argv...)
	skipped := make([]SkippedFile, 0, len(in.Skipped))
	skipped = append(skipped, in.Skipped...)
	intent := &Intent{
		TxnID:          txnID,
		StartedAt:      now().UTC().Format(time.RFC3339),
		Argv:           argv,
		Skipped:        skipped,
		Git:            GitIntent{ExpectCommit: in.ExpectCommit},
		JournalVersion: SupportedJournalVersion,
	}
	intent.Files = make([]IntentFile, 0, len(in.Files))
	for i, spec := range in.Files {
		item := IntentFile{
			Path:       spec.Path,
			Create:     spec.Create,
			TargetHash: HashBytes(spec.TargetBytes),
			TargetSize: int64(len(spec.TargetBytes)),
			TargetOp:   spec.TargetOp,
		}
		if !spec.Create {
			item.PreHash = HashBytes(spec.PreBytes)
			item.PreSize = int64(len(spec.PreBytes))
			item.PreBytesRef = PreDirName + "/" + fmt.Sprintf("%d", i)
		}
		intent.Files = append(intent.Files, item)
	}

	// 先校验后写：路径违规直接 fail closed，绝不落任何字节。
	if err := ValidateIntentPaths(vaultRoot, txnID, intent); err != nil {
		return nil, err
	}

	// ② 备料：安全建 pre/（相对 txnFd，openat + O_NOFOLLOW 拒绝 symlink），逐个写 pre/<n> 并逐个 Fsync。
	preDirFile, err := openChildDir(txnFd, PreDirName, true)
	if err != nil {
		return nil, err
	}
	defer preDirFile.Close()
	preFd := int(preDirFile.Fd())
	if err := fireFailpoint(fpBeforePre); err != nil {
		return nil, err
	}
	for i, spec := range in.Files {
		if spec.Create {
			continue
		}
		if err := writeFileSyncedAt(preFd, fmt.Sprintf("%d", i), spec.PreBytes); err != nil { // 内含 Fsync(pre/<n>)
			return nil, fmt.Errorf("写前像副本 pre/%d 失败：%w", i, err)
		}
	}
	if err := fireFailpoint(fpAfterPre); err != nil {
		return nil, err
	}
	// ③ Fsync(pre/ 目录) + Fsync(txn 目录)：前像字节与目录项均已持久。
	if err := syscall.Fsync(preFd); err != nil {
		return nil, fmt.Errorf("Fsync pre/ 失败：%w", err)
	}
	if err := syscall.Fsync(txnFd); err != nil {
		return nil, fmt.Errorf("Fsync 事务目录失败：%w", err)
	}
	if err := fireFailpoint(fpAfterPreFsync); err != nil {
		return nil, err
	}

	// ④ 写 intent.json.tmp（全量 JSON 一次写完）→ Fsync(tmp)。
	body, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 intent 失败：%w", err)
	}
	if err := writeFileSyncedAt(txnFd, intentTmpName, body); err != nil { // 内含 Fsync(intent.json.tmp)
		return nil, fmt.Errorf("写 intent.json.tmp 失败：%w", err)
	}
	if err := fireFailpoint(fpAfterTmp); err != nil {
		return nil, err
	}
	// ⑤ renameat(tmp → intent.json) → Fsync(txn 目录)：一次性发布，永不留半写 intent.json。
	if err := fireFailpoint(fpBeforeRename); err != nil {
		return nil, err
	}
	if err := sysRenameat(txnFd, intentTmpName, txnFd, IntentFileName); err != nil {
		return nil, fmt.Errorf("发布 intent.json 失败：%w", err)
	}
	// **发布已完成**：intent.json 此刻已原子可见且完整。此后任何错误（含下面的目录 Fsync 失败、
	// 注入的 after_rename 失败）都**不改变**「已发布」这一事实——Scan 必须判其为 Open，绝不误判 residue。
	if err := fireFailpoint(fpAfterRename); err != nil {
		return nil, err
	}
	if err := syscall.Fsync(txnFd); err != nil {
		return nil, fmt.Errorf("Fsync 事务目录（发布后）失败：%w", err)
	}
	return intent, nil
}

// MarkCommit 原子落盘 commit 标记（Markdown 事务的唯一提交点）。
func MarkCommit(vaultRoot, txnID string) error { return writeMarker(vaultRoot, txnID, CommitMarker) }

// MarkAbort 原子落盘 abort 标记（回滚已全部完成后才允许写）。
func MarkAbort(vaultRoot, txnID string) error { return writeMarker(vaultRoot, txnID, AbortMarker) }

// writeMarker：**发布前先检查两侧标记**，再走 创建 tmp → Fsync(tmp) → renameat → Fsync(txn 目录)
// 四步落盘（合同 §4，commit / abort 互斥且各自单次）。先校验 txnID 拒绝路径穿越（`../` 等），
// 再安全解析 .index/txn/<txnID>/（parent-symlink fail closed），tmp 创建走相对目录 fd 的 no-follow 安全路径。
//
// 发布前的两侧裁决（全部**零写盘**，绝不 rename 覆盖任一已有标记）：
//   - 同侧标记已是合法普通零字节文件 ⇒ **幂等成功**（重复 MarkCommit/MarkAbort 无副作用，直接返回 nil）；
//   - 对侧标记存在（任何形态）⇒ 拒绝：再写同侧会主动制造「commit 与 abort 并存」的自相矛盾 Corrupt；
//   - 同侧标记存在但**非普通文件 / 非零字节** ⇒ 拒绝：rename 覆盖会抹掉这份损坏证据；
//   - 两侧皆缺席 ⇒ 唯一允许发布的情形。
//
// 发布之后仍由 Scan.markerStateAt 复核「普通零字节」形态：写入面与读取面共享同一条零字节合同，
// 二者任何一侧发现非零字节 / 非普通文件都判 Corrupt fail closed。
func writeMarker(vaultRoot, txnID, name string) error {
	if !ValidTxnID(txnID) {
		return &PathViolationError{Field: "txn_id", Value: txnID, Reason: "非法 txn_id，拒绝写标记（防路径穿越）"}
	}
	txnDirFile, err := openRuntimeDir(vaultRoot, []string{IndexDirName, TxnDirName, txnID}, false)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("事务目录 %s 不存在，拒绝写 %s 标记", TxnDirPath(vaultRoot, txnID), name)
		}
		return err
	}
	defer txnDirFile.Close()
	fd := int(txnDirFile.Fd())

	// 同侧 / 对侧标记名（commit ↔ abort 互斥）。
	other := AbortMarker
	if name == AbortMarker {
		other = CommitMarker
	}

	// 发布前读取两侧标记的在盘形态（相对 fd 的 O_NOFOLLOW 只读，零写盘）。
	selfPresent, selfOK := markerStateAt(fd, name) // ok = 普通零字节
	otherPresent, _ := markerStateAt(fd, other)    // 对侧任何在盘形态都构成互斥冲突

	// 对侧标记已存在（任何形态）：拒绝，绝不写第二个标记制造双标记 Corrupt。
	if otherPresent {
		return &MarkerConflictError{TxnID: txnID, Marker: name,
			Reason: fmt.Sprintf("对侧 %s 标记已存在，拒绝再写 %s（commit/abort 互斥，防双标记自相矛盾）", other, name)}
	}
	// 同侧标记已存在：合法零字节 ⇒ 幂等成功；否则（非普通 / 非零字节）⇒ 拒绝覆盖损坏证据。
	if selfPresent {
		if selfOK {
			return nil // 幂等：合法普通零字节同侧标记已在盘，重复调用无副作用、零写盘
		}
		return &MarkerConflictError{TxnID: txnID, Marker: name,
			Reason: fmt.Sprintf("同侧 %s 标记已存在但非普通零字节文件，拒绝 rename 覆盖（保留损坏证据）", name)}
	}

	// 两侧皆缺席：首次发布。零字节标记 + Fsync(tmp)，no-follow 安全创建。
	if err := writeFileSyncedAt(fd, name+".tmp", nil); err != nil {
		return fmt.Errorf("写 %s.tmp 失败：%w", name, err)
	}
	if err := sysRenameat(fd, name+".tmp", fd, name); err != nil {
		return fmt.Errorf("发布 %s 标记失败：%w", name, err)
	}
	if err := syscall.Fsync(fd); err != nil {
		return fmt.Errorf("Fsync 事务目录失败：%w", err)
	}
	return nil
}

// ---------------------------------------------------------------- Scan（全量只读三分类，§4.2）

// TxnState 是事务目录的分类。
type TxnState int

const (
	// StateOpen intent.json 可解析且 commit / abort 皆缺席——唯一需恢复的未闭合事务。
	StateOpen TxnState = iota
	// StateCorrupt intent.json 在盘但不可解析 / 缺必需字段 / 版本不支持——fail closed。
	StateCorrupt
	// StateResidue intent.json 缺席——pre-intent residue，可静默清理、不发 W26、不阻断。
	StateResidue
	// StateCommitted commit 标记在场——已提交，恢复层不回滚。
	StateCommitted
	// StateAborted abort 标记在场——已放弃且回滚已完成。
	StateAborted
)

// TxnEntry 是单个事务目录的分类结果。
type TxnEntry struct {
	TxnID         string
	Dir           string
	State         TxnState
	Intent        *Intent // 仅 Open / Committed / Aborted 且可解析时非 nil
	CorruptReason string  // 仅 Corrupt 时非空
}

// ScanResult 是 Scan 的全量结果（供 072 做全局裁决）。
type ScanResult struct {
	Entries []TxnEntry
}

// Scan 全量只读列出 .index/txn/ 下**所有**条目的分类结果（合同 §4.2 / R5 P0-3b）。
//
// 契约：
//   - **不提前 break**：遇到第一个 OpenTxn / CorruptTxn 也要走完全表；
//   - **全程零写盘**：不清理 residue、不写 abort、不动 quarantine——清理只能由调用方在
//     全局裁决通过之后另行调用 Prune；
//   - 判据是 lstat(intent.json) 的**明确 ENOENT**：唯有 intent.json 明确缺席才是 residue，
//     权限 / I/O 等其他错误、symlink、非普通文件一律 Corrupt（stat 成功但解析失败**绝不**降级为 residue）；
//   - txn/ 下的**非目录 / symlink / 非法 txn_id 目录名**都是扫描异常 ⇒ CorruptTxn，进入全量结果，
//     由调用方（072）据 Blocked() 整体 fail closed。
func Scan(vaultRoot string) (ScanResult, error) {
	root := TxnRootPath(vaultRoot)
	// 安全解析 .index/txn/（parent-symlink fail closed）：若 .index 或 txn 是 symlink / 非目录，
	// 直接 fail closed（绝不顺着它列举 / 后续删除 vault 外的目录），dirents 也从**已解析的 fd** 读取。
	rootFile, err := openRuntimeDir(vaultRoot, []string{IndexDirName, TxnDirName}, false)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return ScanResult{}, nil // 无事务日志目录即无事务
		}
		return ScanResult{}, err // .index / txn 是 symlink / 非目录 ⇒ *RuntimeDirError（携 E15）
	}
	defer rootFile.Close()
	rootFd := int(rootFile.Fd())
	dirents, err := rootFile.ReadDir(-1)
	if err != nil {
		return ScanResult{}, fmt.Errorf("读取事务日志目录 %s 失败：%w", root, err)
	}
	var res ScanResult
	for _, de := range dirents {
		name := de.Name()
		if name == SeqFileName || name == SeqFileName+".tmp" {
			continue // 全局计数器 / 其临时文件不是事务目录
		}
		dir := filepath.Join(root, name)
		// txn/ 下的每个非计数器条目都**必须**是一个合法命名的真实目录，否则是扫描异常。
		if de.Type()&os.ModeSymlink != 0 {
			res.Entries = append(res.Entries, TxnEntry{TxnID: name, Dir: dir, State: StateCorrupt,
				CorruptReason: "txn/ 下出现 symlink 条目（扫描异常，拒绝跟随）"})
			continue
		}
		if !de.IsDir() {
			res.Entries = append(res.Entries, TxnEntry{TxnID: name, Dir: dir, State: StateCorrupt,
				CorruptReason: "txn/ 下出现非目录条目（扫描异常）"})
			continue
		}
		if !ValidTxnID(name) {
			res.Entries = append(res.Entries, TxnEntry{TxnID: name, Dir: dir, State: StateCorrupt,
				CorruptReason: "非法 txn_id 目录名（不匹配 ^t[0-9a-f]{16}$）"})
			continue
		}
		res.Entries = append(res.Entries, classifyTxnDirAt(vaultRoot, rootFd, dir, name))
	}
	return res, nil
}

// classifyTxnDirAt 只读地分类一个（已确认名字合法的）事务目录，**全程相对已解析的 txn 根 fd**
// 行走：`<txn_id>` 目录用 openat + O_NOFOLLOW 打开（固定父 symlink 不跟随），intent.json 与
// commit/abort 标记都相对该目录 fd 以 O_NOFOLLOW 读取——读路径与写路径共享同一套 dirfd 安全语义，
// 绝不按路径重新解析（不给攻击者在 stat 与 read 之间调包父目录的窗口）。**零写盘。**
func classifyTxnDirAt(vaultRoot string, rootFd int, dir, name string) TxnEntry {
	e := TxnEntry{TxnID: name, Dir: dir}

	// 相对 txn 根 fd 打开 <txn_id>/（O_DIRECTORY|O_NOFOLLOW）：symlink / 非目录 ⇒ Corrupt fail closed，
	// 绝不顺着它读到 vault 外。（Scan 循环已按 dirent 过滤过 symlink，这里是 fd 级的权威复核。）
	childDir, cerr := openChildDir(rootFd, name, false)
	if cerr != nil {
		var rde *RuntimeDirError
		if errors.As(cerr, &rde) {
			e.State = StateCorrupt
			e.CorruptReason = "事务目录组件不安全：" + rde.Error()
			return e
		}
		if errors.Is(cerr, syscall.ENOENT) || errors.Is(cerr, os.ErrNotExist) {
			// 扫描与打开之间目录消失：无可恢复内容，按 residue（不阻断、可清理）。
			e.State = StateResidue
			return e
		}
		e.State = StateCorrupt
		e.CorruptReason = "无法打开事务目录：" + cerr.Error()
		return e
	}
	defer childDir.Close()
	txnFd := int(childDir.Fd())

	// intent.json：openat + O_NOFOLLOW 读取。判据是**明确 ENOENT** ⇒ residue；
	// symlink（ELOOP）/ 非普通文件 / 权限 / I/O / 读取失败一律 Corrupt（stat 成功但解析失败**绝不**降级为 residue）。
	intentFd, oerr := sysOpenat(txnFd, IntentFileName, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if oerr != nil {
		if errors.Is(oerr, syscall.ENOENT) {
			e.State = StateResidue
			return e
		}
		if errors.Is(oerr, syscall.ELOOP) {
			e.State = StateCorrupt
			e.CorruptReason = "intent.json 是 symlink（拒绝跟随）"
			return e
		}
		e.State = StateCorrupt
		e.CorruptReason = "intent.json 无法打开（权限 / I/O）：" + oerr.Error()
		return e
	}
	intentFile := os.NewFile(uintptr(intentFd), IntentFileName)
	info, serr := intentFile.Stat()
	if serr != nil {
		_ = intentFile.Close()
		e.State = StateCorrupt
		e.CorruptReason = "intent.json 无法 stat：" + serr.Error()
		return e
	}
	if !info.Mode().IsRegular() {
		_ = intentFile.Close()
		e.State = StateCorrupt
		e.CorruptReason = fmt.Sprintf("intent.json 不是普通文件（mode=%s）", info.Mode())
		return e
	}
	raw, rerr := io.ReadAll(intentFile)
	_ = intentFile.Close()
	if rerr != nil {
		e.State = StateCorrupt
		e.CorruptReason = "intent.json 无法读取：" + rerr.Error()
		return e
	}

	// **完整结构校验**（合同 §4 字段表 + §4.3 路径约束）：任一必需字段缺失 / 类型错误 / 取值违约 ⇒ Corrupt。
	it, verr := validateIntentStructure(vaultRoot, name, raw)
	if verr != nil {
		e.State = StateCorrupt
		e.CorruptReason = "intent.json 结构非法：" + verr.Error()
		return e
	}
	e.Intent = it

	// commit / abort 的存在性与形态（相对 txnFd 的 O_NOFOLLOW 读取）：任一非普通零字节文件、或两者同时在场 ⇒ Corrupt。
	commitPresent, commitOK := markerStateAt(txnFd, CommitMarker)
	abortPresent, abortOK := markerStateAt(txnFd, AbortMarker)
	if commitPresent && !commitOK {
		e.State = StateCorrupt
		e.CorruptReason = "commit 标记不是普通零字节文件"
		return e
	}
	if abortPresent && !abortOK {
		e.State = StateCorrupt
		e.CorruptReason = "abort 标记不是普通零字节文件"
		return e
	}
	if commitPresent && abortPresent {
		e.State = StateCorrupt
		e.CorruptReason = "commit 与 abort 标记同时存在（自相矛盾）"
		return e
	}
	if commitPresent {
		e.State = StateCommitted
		return e
	}
	if abortPresent {
		e.State = StateAborted
		return e
	}
	e.State = StateOpen
	return e
}

// sha256HashRe 是 target_hash / pre_hash 的形态：sha256: + 64 位小写十六进制（与 store.ContentHash 同口径）。
var sha256HashRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// rawIntent 用指针 / json.RawMessage 区分「键缺失」与「键在但零值合法」（如 expect_commit:false、
// target_size:0、create:false）——任何**必需键缺失**都必须判 Corrupt，绝不静默按零值继续。
type rawIntent struct {
	TxnID          *string          `json:"txn_id"`
	StartedAt      *string          `json:"started_at"`
	Argv           *json.RawMessage `json:"argv"`
	Files          *json.RawMessage `json:"files"`
	Skipped        *json.RawMessage `json:"skipped"`
	Git            *json.RawMessage `json:"git"`
	JournalVersion *int             `json:"journal_version"`
}

// rawFile 同样以指针区分缺键与零值，供逐项完整校验。
type rawFile struct {
	Path        *string `json:"path"`
	PreHash     *string `json:"pre_hash"`
	PreSize     *int64  `json:"pre_size"`
	PreBytesRef *string `json:"pre_bytes_ref"`
	TargetHash  *string `json:"target_hash"`
	TargetSize  *int64  `json:"target_size"`
	Create      *bool   `json:"create"`
	TargetOp    *string `json:"target_op"`
}

type rawGit struct {
	ExpectCommit *bool `json:"expect_commit"`
}

// validateIntentStructure 对 intent.json 做**完整结构校验**（合同 §4 字段表 + §4.3 路径约束），
// 是 072 三分支恢复的输入完整性闸门：任一必需字段缺失 / 类型错误 / 取值违约即返回 error
// （调用方据此判 Corrupt fail closed）。成功时返回规整的 *Intent。**只读、零写盘。**
func validateIntentStructure(vaultRoot, name string, raw []byte) (*Intent, error) {
	var ri rawIntent
	// json.Unmarshal 会拒绝尾随垃圾（顶层值之后的非空白内容），因此半写 / 拼接的 JSON 一律解析失败。
	if err := json.Unmarshal(raw, &ri); err != nil {
		return nil, fmt.Errorf("不可解析：%v", err)
	}
	if ri.JournalVersion == nil {
		return nil, errors.New("缺 journal_version")
	}
	if *ri.JournalVersion != SupportedJournalVersion {
		return nil, fmt.Errorf("journal_version=%d 不支持（仅支持 %d）", *ri.JournalVersion, SupportedJournalVersion)
	}
	if ri.TxnID == nil {
		return nil, errors.New("缺 txn_id")
	}
	if !ValidTxnID(*ri.TxnID) {
		return nil, fmt.Errorf("txn_id %q 不匹配 ^t[0-9a-f]{16}$", *ri.TxnID)
	}
	if *ri.TxnID != name {
		return nil, fmt.Errorf("intent.txn_id(%q) 与目录名(%q) 不一致", *ri.TxnID, name)
	}
	if ri.StartedAt == nil {
		return nil, errors.New("缺 started_at")
	}
	st, perr := time.Parse(time.RFC3339, *ri.StartedAt)
	if perr != nil {
		return nil, fmt.Errorf("started_at %q 不是 RFC3339：%v", *ri.StartedAt, perr)
	}
	// started_at 必须是 UTC（合同 §4：时间戳一律 UTC / Z）。RFC3339 允许 +08:00 之类的带偏移写法，
	// 这里显式拒非零时区偏移——跨机 / 跨时区读日志时才不会把同一时刻解释成不同挂钟时间。
	if _, offset := st.Zone(); offset != 0 {
		return nil, fmt.Errorf("started_at %q 必须为 UTC（零偏移 / Z 形式），得非零时区偏移", *ri.StartedAt)
	}
	argvRaw, aerr := requireJSONArray("argv", ri.Argv)
	if aerr != nil {
		return nil, aerr
	}
	var argv []string
	if err := json.Unmarshal(argvRaw, &argv); err != nil {
		return nil, fmt.Errorf("argv 不是字符串数组：%v", err)
	}
	skippedRaw, serr := requireJSONArray("skipped", ri.Skipped)
	if serr != nil {
		return nil, serr
	}
	var skipped []SkippedFile
	if err := json.Unmarshal(skippedRaw, &skipped); err != nil {
		return nil, fmt.Errorf("skipped 结构非法：%v", err)
	}
	for i := range skipped {
		if verr := validateSkipped(i, skipped[i]); verr != nil {
			return nil, verr
		}
	}
	if ri.Git == nil {
		return nil, errors.New("缺 git")
	}
	var rg rawGit
	if err := json.Unmarshal(*ri.Git, &rg); err != nil {
		return nil, fmt.Errorf("git 结构非法：%v", err)
	}
	if rg.ExpectCommit == nil {
		return nil, errors.New("缺 git.expect_commit")
	}
	filesRaw, ferr := requireJSONArray("files", ri.Files)
	if ferr != nil {
		return nil, ferr
	}
	var rawFiles []rawFile
	if err := json.Unmarshal(filesRaw, &rawFiles); err != nil {
		return nil, fmt.Errorf("files 不是数组：%v", err)
	}

	it := &Intent{
		TxnID:          *ri.TxnID,
		StartedAt:      *ri.StartedAt,
		Argv:           argv,
		Skipped:        skipped,
		Git:            GitIntent{ExpectCommit: *rg.ExpectCommit},
		JournalVersion: *ri.JournalVersion,
		Files:          make([]IntentFile, 0, len(rawFiles)),
	}
	seen := make(map[string]struct{}, len(rawFiles))
	for idx, rf := range rawFiles {
		f, ferr := validateRawFile(idx, rf)
		if ferr != nil {
			return nil, ferr
		}
		if _, dup := seen[f.Path]; dup {
			return nil, fmt.Errorf("files[%d].path 重复：%q", idx, f.Path)
		}
		seen[f.Path] = struct{}{}
		it.Files = append(it.Files, f)
	}
	// §4.3：files[].path 与 pre_bytes_ref 的规范化 / 防逃逸校验（先词法后 symlink）。
	if perr := ValidateIntentPaths(vaultRoot, name, it); perr != nil {
		return nil, fmt.Errorf("路径违规：%v", perr)
	}
	return it, nil
}

// requireJSONArray 断言某字段在盘、非 null、且是真 JSON 数组（不接受缺席 / null / 对象 / 标量）。
// 这是「argv/files/skipped 必须是真数组」合同的**强校验入口**：不依赖 *json.RawMessage 对 JSON null
// 的微妙折叠行为（可能收到 nil 指针，也可能收到字面量 "null"），改用字节前缀判定，两种形态都 fail closed。
func requireJSONArray(field string, rm *json.RawMessage) (json.RawMessage, error) {
	if rm == nil {
		return nil, fmt.Errorf("缺 %s（不得缺席或为 null）", field)
	}
	b := bytes.TrimSpace([]byte(*rm))
	if len(b) == 0 || string(b) == "null" {
		return nil, fmt.Errorf("%s 为 null（必须是数组 []）", field)
	}
	if b[0] != '[' {
		return nil, fmt.Errorf("%s 不是数组（必须是 JSON array）", field)
	}
	return json.RawMessage(b), nil
}

// validateSkipped 校验 skipped[] 单项（合同 §4）：path 必需且**词法规范化**（相对 vault、无 . / .. / 空分量、
// Clean 后逐字相等），kind 必需且限**封闭两值** file_changed / user_block_unsafe。skipped 仅用于报告 / 审计，
// 不做 symlink 逃逸解析（其指向的文件可能已被改动 / 删除），故用 lexicalRelClean 而非 validateVaultRelPath。
func validateSkipped(idx int, s SkippedFile) error {
	if s.Path == "" {
		return fmt.Errorf("skipped[%d].path 缺失", idx)
	}
	if err := lexicalRelClean(fmt.Sprintf("skipped[%d].path", idx), s.Path); err != nil {
		return err
	}
	switch s.Kind {
	case SkipKindFileChanged, SkipKindUserBlockUnsafe:
		return nil
	case "":
		return fmt.Errorf("skipped[%d].kind 缺失", idx)
	default:
		return fmt.Errorf("skipped[%d].kind %q 非法（仅 %s / %s）",
			idx, s.Kind, SkipKindFileChanged, SkipKindUserBlockUnsafe)
	}
}

// validateRawFile 校验单个 files[] 项的完整性（合同 §4 字段表）：
//
//   - 恒需 path / create / target_op（非空）/ target_hash（sha256:<64hex>）/ target_size（≥0）；
//
//   - create:true ⇒ **无前像内容**，但 pre_hash / pre_size 作为 JSON 合同字段**必须在场**且分别
//     为空串 / 0；唯一被省略的是 pre_bytes_ref（前像副本不存在，引用无意义，必须缺席）；
//
//   - create:false ⇒ **完整前像**：pre_hash 为 sha256、pre_size ≥0、pre_bytes_ref **逐项**严格
//     等于 pre/<本项索引>（不能只认 pre/ 前缀，否则多项可指向同一份错误前像）。
func validateRawFile(idx int, rf rawFile) (IntentFile, error) {
	where := func(field string) string { return fmt.Sprintf("files[%d].%s", idx, field) }
	if rf.Path == nil {
		return IntentFile{}, fmt.Errorf("%s 缺失", where("path"))
	}
	if rf.Create == nil {
		return IntentFile{}, fmt.Errorf("%s 缺失", where("create"))
	}
	if rf.TargetOp == nil || *rf.TargetOp == "" {
		return IntentFile{}, fmt.Errorf("%s 缺失或为空", where("target_op"))
	}
	if rf.TargetHash == nil {
		return IntentFile{}, fmt.Errorf("%s 缺失", where("target_hash"))
	}
	if !sha256HashRe.MatchString(*rf.TargetHash) {
		return IntentFile{}, fmt.Errorf("%s 不是 sha256:<64hex>：%q", where("target_hash"), *rf.TargetHash)
	}
	if rf.TargetSize == nil {
		return IntentFile{}, fmt.Errorf("%s 缺失", where("target_size"))
	}
	if *rf.TargetSize < 0 {
		return IntentFile{}, fmt.Errorf("%s 为负", where("target_size"))
	}
	f := IntentFile{
		Path:       *rf.Path,
		Create:     *rf.Create,
		TargetHash: *rf.TargetHash,
		TargetSize: *rf.TargetSize,
		TargetOp:   *rf.TargetOp,
	}
	if *rf.Create {
		// pre_hash 必须在场且为空串：作为「无前像」的显式合同承诺，而非靠字段缺席隐式表达。
		if rf.PreHash == nil {
			return IntentFile{}, fmt.Errorf("%s：create:true 仍必须在场（空串）", where("pre_hash"))
		}
		if *rf.PreHash != "" {
			return IntentFile{}, fmt.Errorf("%s：create:true 必须为空串，得 %q", where("pre_hash"), *rf.PreHash)
		}
		// pre_size 必须在场且为 0。
		if rf.PreSize == nil {
			return IntentFile{}, fmt.Errorf("%s：create:true 仍必须在场（0）", where("pre_size"))
		}
		if *rf.PreSize != 0 {
			return IntentFile{}, fmt.Errorf("%s：create:true 必须为 0，得 %d", where("pre_size"), *rf.PreSize)
		}
		// pre_bytes_ref 必须被省略（缺席）——前像副本不存在，任何引用都自相矛盾。
		if rf.PreBytesRef != nil {
			return IntentFile{}, fmt.Errorf("%s：create:true 必须省略（前像不存在，得 %q）", where("pre_bytes_ref"), *rf.PreBytesRef)
		}
		return f, nil
	}
	if rf.PreHash == nil || !sha256HashRe.MatchString(*rf.PreHash) {
		return IntentFile{}, fmt.Errorf("%s：create:false 需 sha256 前像哈希", where("pre_hash"))
	}
	if rf.PreSize == nil {
		return IntentFile{}, fmt.Errorf("%s 缺失", where("pre_size"))
	}
	if *rf.PreSize < 0 {
		return IntentFile{}, fmt.Errorf("%s 为负", where("pre_size"))
	}
	if rf.PreBytesRef == nil || *rf.PreBytesRef == "" {
		return IntentFile{}, fmt.Errorf("%s：create:false 缺前像引用", where("pre_bytes_ref"))
	}
	// create:false 的前像引用必须**逐项**严格等于 pre/<本项索引>：仅校验 pre/ 前缀会让多个 files
	// 项指向同一份错误前像（例如都写 pre/0），破坏前像与目标的一一对应，恢复期回滚将张冠李戴。
	wantRef := fmt.Sprintf("%s/%d", PreDirName, idx)
	if *rf.PreBytesRef != wantRef {
		return IntentFile{}, fmt.Errorf("%s：create:false 必须严格等于 %q，得 %q", where("pre_bytes_ref"), wantRef, *rf.PreBytesRef)
	}
	f.PreHash = *rf.PreHash
	f.PreSize = *rf.PreSize
	f.PreBytesRef = *rf.PreBytesRef
	return f, nil
}

// markerStateAt 相对已解析的事务目录 fd 只读判断标记文件（openat + O_NOFOLLOW|O_NONBLOCK）：
// present=是否在盘（含 symlink / 特殊文件 / 访问异常等非「明确缺席」形态），
// ok=在盘且是**普通文件且零字节**（合同要求 commit / abort 恒为零字节标记）。
//
// **缺席判据收紧（合同 §4.x）**：只有 ENOENT / ENOTDIR（父路径分量不是目录，等价于该名不可能存在）
// 才是真正「缺席」→ (false,false)。其余任何 open 错误——symlink 触发的 ELOOP、权限 EACCES、
// I/O 故障 EIO、名过长 ENAMETOOLONG 等——都不能证明标记缺席，一律判 present + !ok，
// 让上层把该事务视为 Corrupt fail closed，绝不因一次读失败就误判为「无 commit / 无 abort」。
//
// **零字节形态校验（合同 §4.x）**：标记必须是**零字节**普通文件；非普通文件、或普通但**非零字节**
// （被塞入内容 / 被当作数据文件误写）都判 present + !ok ⇒ 上层 Corrupt。写入面 writeMarker 也复用
// 本判据决定幂等 / 拒绝覆盖，读写两侧共享同一条零字节合同。
func markerStateAt(dirFd int, name string) (present, ok bool) {
	fd, err := sysOpenat(dirFd, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR) {
			return false, false // 唯一「明确缺席」判据
		}
		// ELOOP(symlink) / EACCES / EIO / ENAMETOOLONG / … ：在盘或状态不明 ⇒ present + !ok ⇒ Corrupt
		return true, false
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, serr := f.Stat()
	if serr != nil {
		return true, false
	}
	// 普通文件且零字节才算合法标记；非普通或非零字节 ⇒ present + !ok ⇒ Corrupt。
	return true, info.Mode().IsRegular() && info.Size() == 0
}

// Open 返回全部未闭合事务。
func (r ScanResult) Open() []TxnEntry { return r.filter(StateOpen) }

// Corrupt 返回全部损坏事务。
func (r ScanResult) Corrupt() []TxnEntry { return r.filter(StateCorrupt) }

// Residue 返回全部 pre-intent residue。
func (r ScanResult) Residue() []TxnEntry { return r.filter(StateResidue) }

func (r ScanResult) filter(s TxnState) []TxnEntry {
	var out []TxnEntry
	for _, e := range r.Entries {
		if e.State == s {
			out = append(out, e)
		}
	}
	return out
}

// Blocked 给出「全局裁决」的阻断结论（§4.1.2）：存在任一 CorruptTxn 或多于一个 OpenTxn ⇒
// 返回 *ScanBlockedError（携 E15），调用方据此在**任何回滚写之前**整体阻断、权威零写入。
// 返回 nil 表示可以进入 Pass B（恢复动作本身在 072）。
func (r ScanResult) Blocked() error {
	if berr := blockedFromEntries(r.Entries, 1); berr != nil {
		return berr
	}
	return nil
}

// ---------------------------------------------------------------- Prune（保留策略，§4）

// PruneOptions 覆盖默认保留策略（测试注入用）。
type PruneOptions struct {
	MaxAge  time.Duration // ≤0 取默认 7 天
	MaxKeep int           // ≤0 取默认 20
	Now     func() time.Time
}

// Prune 顺带清理已闭合事务与 residue，**绝不触碰** OpenTxn / CorruptTxn / 权威文件（合同 §4 / §4.2 / §4.1.2）。
//
// **阻断优先（§4.1.2 的零写不变式）**：只要扫描结果里存在**任一** CorruptTxn 或**任一**尚未
// 由恢复层闭合的 OpenTxn，Prune 在**删除任何 residue / closed 之前**直接返回 *ScanBlockedError，
// 一个字节都不删——否则「先删 residue 再发现 Corrupt」会让阻断路径的「全盘零写」变成假承诺。
// 因此 Prune 只在「无 Open、无 Corrupt」的干净结果上执行清理：
//
//   - residue（intent.json 缺席）：静默移除整目录（含遗留 .tmp），不发 W26；
//   - 闭合事务（commit / abort 在场）：保留 7 天或最近 20 个，先到者为准，其余移除。
//
// **不重置** seq。返回被移除的 txn_id 列表。
func Prune(vaultRoot string, opts PruneOptions) ([]string, error) {
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = defaultPruneMaxAge
	}
	maxKeep := opts.MaxKeep
	if maxKeep <= 0 {
		maxKeep = defaultPruneMaxKeep
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	res, err := Scan(vaultRoot)
	if err != nil {
		return nil, err
	}
	// 阻断优先：任何 Corrupt 或任何未闭合 Open 都必须先由恢复层（072）处理，
	// 在此之前 Prune 零写退出（连 residue 都不动，保全人工排查现场）。
	if blockers := pruneBlockers(res); blockers != nil {
		return nil, blockers
	}

	// 删除路径与读路径共享同一套 dirfd 安全语义：再次**安全解析** .index/txn/（parent-symlink
	// fail closed），此后所有删除都相对该 fd 用 unlinkat 行走，绝不按 e.Dir 路径做递归路径删除
	// （那会在扫描后被人换成 symlink 时顺着删到 vault 外）。
	rootFile, err := openRuntimeDir(vaultRoot, []string{IndexDirName, TxnDirName}, false)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist) {
			return nil, nil // 目录已不在，无可清理
		}
		return nil, err // .index / txn 变成 symlink / 非目录 ⇒ fail closed
	}
	defer rootFile.Close()
	rootFd := int(rootFile.Fd())

	var removed []string

	// residue：无条件静默移除（此刻已确认无 Open / Corrupt）。
	for _, e := range res.Residue() {
		if err := removeTxnEntryAt(rootFd, e.TxnID); err != nil {
			return removed, fmt.Errorf("清理 residue %s 失败：%w", e.TxnID, err)
		}
		removed = append(removed, e.TxnID)
	}

	// 闭合事务：先按 seq（= 目录名字典序）排序，再套 7 天 / 20 个。
	type settled struct {
		entry TxnEntry
		mtime time.Time
	}
	var closed []settled
	for _, e := range res.Entries {
		if e.State != StateCommitted && e.State != StateAborted {
			continue
		}
		marker := CommitMarker
		if e.State == StateAborted {
			marker = AbortMarker
		}
		mt := now()
		if t, ok := markerModTimeAt(rootFd, e.TxnID, marker); ok {
			mt = t
		}
		closed = append(closed, settled{entry: e, mtime: mt})
	}
	// 最近的排前面（按 txn_id 字典序 = seq 单调，等价于分配顺序）。
	sort.Slice(closed, func(i, j int) bool { return closed[i].entry.TxnID > closed[j].entry.TxnID })
	for rank, s := range closed {
		tooOld := now().Sub(s.mtime) > maxAge
		beyondKeep := rank >= maxKeep
		if tooOld || beyondKeep {
			if err := removeTxnEntryAt(rootFd, s.entry.TxnID); err != nil {
				return removed, fmt.Errorf("清理闭合事务 %s 失败：%w", s.entry.TxnID, err)
			}
			removed = append(removed, s.entry.TxnID)
		}
	}
	return removed, nil
}

// markerModTimeAt 相对已解析的 txn 根 fd 读取 <txn_id>/<marker> 的 mtime（O_NOFOLLOW，只读）。
// 父组件不安全 / 缺失 / 非普通文件均返回 ok=false，调用方回落到 now()。
func markerModTimeAt(rootFd int, txnName, marker string) (time.Time, bool) {
	childDir, err := openChildDir(rootFd, txnName, false)
	if err != nil {
		return time.Time{}, false
	}
	defer childDir.Close()
	fd, oerr := sysOpenat(int(childDir.Fd()), marker, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if oerr != nil {
		return time.Time{}, false
	}
	f := os.NewFile(uintptr(fd), marker)
	defer f.Close()
	info, serr := f.Stat()
	if serr != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// removeTxnEntryAt 相对已解析的 txn 根 fd 安全删除单个事务目录整棵子树（unlinkat 行走）。
func removeTxnEntryAt(rootFd int, name string) error { return removeAllAt(rootFd, name) }

// removeAllAt 相对 parentFd 递归删除名为 name 的项（先按普通文件 unlink，是目录则 O_NOFOLLOW 打开、
// 递归删子项、再 rmdir）。全程 fd-relative + O_NOFOLLOW：即便 name 在删除窗口内被换成 symlink，
// unlinkat 只摘除该链接本身、绝不跟随删除链接目标。ENOENT 视为已删除（幂等）。
func removeAllAt(parentFd int, name string) error {
	// 先按普通文件 / symlink 尝试 unlink（对目录会返回 EISDIR/EPERM）。
	if err := sysUnlinkat(parentFd, name, 0); err == nil || errors.Is(err, syscall.ENOENT) {
		return nil
	} else if !errors.Is(err, syscall.EISDIR) && !errors.Is(err, syscall.EPERM) {
		return err
	}
	// 是目录：O_NOFOLLOW 打开后递归清空，再 rmdir。
	dirFd, oerr := sysOpenat(parentFd, name,
		syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if oerr != nil {
		if errors.Is(oerr, syscall.ENOENT) {
			return nil
		}
		return oerr
	}
	d := os.NewFile(uintptr(dirFd), name)
	names, rerr := d.Readdirnames(-1)
	if rerr != nil {
		_ = d.Close()
		return rerr
	}
	for _, child := range names {
		if cerr := removeAllAt(int(d.Fd()), child); cerr != nil {
			_ = d.Close()
			return cerr
		}
	}
	_ = d.Close()
	if err := sysUnlinkat(parentFd, name, atRemoveDir); err != nil && !errors.Is(err, syscall.ENOENT) {
		return err
	}
	return nil
}

// pruneBlockers 返回「使 Prune 必须零写退出」的阻断错误（存在任一 Corrupt 或任一 Open）；
// 干净结果返回 nil。
func pruneBlockers(res ScanResult) error {
	// Prune 的阈值比 Blocked 更严（**任一** Open 就阻断，而不是 >1）：保留策略一旦在有未闭合
	// 事务时动手，删掉的可能正是下一次恢复要用的那份前像。阈值差异只体现在这个参数上，
	// 披露口径（含损坏原因搬运）与 Blocked 共用同一构造点。
	if berr := blockedFromEntries(res.Entries, 0); berr != nil {
		return berr
	}
	return nil
}

// ---------------------------------------------------------------- 共享落盘原语
//
// 安全落盘原语已收敛到 safedir.go 的 writeFileSyncedAt（相对已解析目录 fd，openat + O_NOFOLLOW）
// 与 syscall.Fsync；本文件不再保留任何按路径解析的写原语，避免绕过 parent-symlink 防线。
