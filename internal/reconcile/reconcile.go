package reconcile

// 只读检查器入口骨架：输入 vault 快照 + Git 工作区状态，输出 []Finding + []RepairSpec。
//
// **本 task（T-…-049）不实现 R1–R7 任何一项检查**：Run 只负责「按注册顺序汇总 + 归一化 +
// 可复算排序」，注册表在本 task 内**恒为空**，因此 Run 恒返回空集合（非 nil）。
// R1 ~ R7 由 T-…-050 ~ T-…-056 各自落一个 Checker 并在自己的文件里注册。
//
// 三条边界在本文件同样成立：不写盘、不发提交、不起子进程。Git 只以**只读状态快照**
// 的形态进来（git.Status 是 `git status --porcelain` 的采样结果），本包不持有 *git.Repo，
// 因此结构上不可能调到 git 包的写方法。

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
)

// Input 是一次对账的**全部**输入（只读）。
//
// 本包不自己扫描 vault、不自己执行 Git：快照由调用方（CLI 层）在包外取好后传进来，
// 这样「检查」与「取数」分离，检查器本身是纯函数、可用内存构造的快照做表驱动测试。
type Input struct {
	// VaultRoot 是 vault 根路径，只用于把绝对路径折算成 vault 相对路径（不做任何 IO）。
	VaultRoot string
	// Scan 是 query.VaultScan 的产物（全量 Markdown 扫描底座，本包不另写扫描器）。
	Scan *query.ScanResult
	// Status 是 git 包只读状态面的采样快照；nil 语义由各检查项自行定义（本 task 不判定）。
	Status git.Status
	// Sources 是 `sources/` 分区的只读快照（ID + vault 相对路径两个字段，见 SourceFact）。
	//
	// 为什么单独一个字段：M2 的扫描底座只覆盖 knowledge / notes 两类对象（合同口径），
	// 原文分区不在其内；R4（T-…-052）的「悬空 source 引用」与「无派生笔记的原文」两条判定
	// 需要原文的落盘事实，而本包**不许另写扫描器**（合同 §17 第 7 条），所以由调用方在包外
	// 采样后传进来。**nil = 该分区未采样**：两条源侧判定整体跳过，绝不把「没采样」误报成
	// 「不存在」（诚实性口径同 query 侧「Q2 只在全库扫描面判定」）。
	Sources []SourceFact
	// Edits 是「最近一次改动某文件的 commit 的 verb」这一只读事实的采样快照
	// （逐路径，见 EditFact）。R2（T-…-051）的判定条件②有两条证据，其一是未提交改动
	// （已由 Status 承载），其二是**已提交的外部编辑**——它只能从 Git 历史读出来。
	//
	// 为什么同样由调用方采样后传进来：本包不持有 *git.Repo、不起子进程（合同 §1.2 /
	// §17），采样属命令层（`git log -1` 的只读用法）。**nil = 该事实未采样**：证据②
	// 整体不判，只用未提交改动这一条证据，绝不把「没采样」当成「外部编辑」
	// （诚实性口径同上面的 Sources）。
	Edits []EditFact
	// Renames 是「Git 历史上该文件发生过的 rename」这一只读事实的采样快照
	// （逐条，见 RenameFact）。R5（T-…-054）的判定条件② 要读「跨领域目录 rename +
	// 那次 rename 的 commit 是不是 `eg` 产生的」，它只能从 Git 历史读出来。
	//
	// 为什么同样由调用方采样后传进来：本包不持有 *git.Repo、不起子进程（合同 §1.2 /
	// §17），采样属命令层（`git log --follow --name-status` 的只读用法，封装在
	// `internal/git/log.go`）。**nil = 该事实未采样**：条件② 整体不判，只用条件①
	// 这一条判据，绝不把「没采样」当成「没移动过」或「移动过」
	// （诚实性口径同上面的 Sources / Edits）。
	Renames []RenameFact
	// Recaps 是主题综述分区的只读快照（逐篇，见 RecapFact）。R6（T-…-055）要判定
	// 「综述所引用的知识卡里有没有已经变了的」，需要综述的 `updated_at`、引用卡清单
	// 与失准标记两键的落盘事实。
	//
	// 为什么同样由调用方采样后传进来：M2 的扫描底座只覆盖 knowledge / notes 两类对象，
	// 综述分区不在其内，而本包**不许另写扫描器**（合同 §17 第 7 条）。
	// **nil = 该分区未采样**：R6 整体不判定，绝不把「没采样」当成「没有综述」或
	// 「引用集合为空」（诚实性口径同上面的 Sources / Edits / Renames）。
	Recaps []RecapFact
}

// Result 是一次对账的只读产物：findings + 修复意向。两个切片恒非 nil（空时为空集合）。
type Result struct {
	// Findings 已按 (severity, check, targets[0]) 排好序，error 在前，可逐字复算。
	Findings []Finding
	// Repairs 是修复意向，按 (check 表格行序, path) 排序，同样可复算。
	Repairs []RepairSpec
}

// RepairSpec 只描述「该怎么修」：目标路径 + 待写键 + 原因。
//
// 它**不是**写动作：本结构体只有数据字段（无函数字段、无 io 句柄、无 store / plan 句柄），
// 也没有任何 Apply / Write / Commit 方法 —— 反证见 check_test.go 的
// TestRepairSpecIsDescriptionOnly。真正的写入由 CLI 层把它编排成内存 ChangePlan、
// 经 plan 的完整校验链交 store 落盘（写权限矩阵与写前内容比对一格不豁免）。
type RepairSpec struct {
	// Check 是触发本意向的 check（十三值封闭枚举之一）。
	Check string
	// Path 是待修对象的 vault 相对路径。
	Path string
	// Keys 是待写的 frontmatter 键名（去重 + 升序 + 非 nil）；本包只列键名，不含值的落盘。
	Keys []string
	// Reason 是人类可读原因（非空），与对应 finding 的 detail 同源同事实。
	Reason string
}

// NewRepairSpec 构造一条修复意向：check 必须在封闭枚举内，path / reason 非空，
// Keys 归一化为去重升序的非 nil 集合且至少一个键（没有待写键就不该产生修复意向）。
func NewRepairSpec(check, path string, keys []string, reason string) (RepairSpec, error) {
	if !IsKnownCheck(check) {
		return RepairSpec{}, fmt.Errorf("未知 check %q：check 是恰 %d 值的封闭枚举（合同 §3）",
			check, CheckCount)
	}
	if strings.TrimSpace(path) == "" {
		return RepairSpec{}, fmt.Errorf("check %q 的修复意向缺 path", check)
	}
	norm := NormalizeTargets(keys)
	if len(norm) == 0 {
		return RepairSpec{}, fmt.Errorf("check %q 的修复意向缺待写键", check)
	}
	if strings.TrimSpace(reason) == "" {
		return RepairSpec{}, fmt.Errorf("check %q 的修复意向缺 reason", check)
	}
	return RepairSpec{Check: check, Path: path, Keys: norm, Reason: reason}, nil
}

// Validate 逐字段反证修复意向的完整性（形态与 NewRepairSpec 的准入条件一致）。
func (r RepairSpec) Validate() error {
	_, err := NewRepairSpec(r.Check, r.Path, r.Keys, r.Reason)
	return err
}

// SortRepairs 就地排序修复意向：排序键 = (check 表格行序, path, keys 全序列)。
func SortRepairs(rs []RepairSpec) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if ra, rb := checkRank(a.Check), checkRank(b.Check); ra != rb {
			return ra < rb
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return strings.Join(a.Keys, "\x00") < strings.Join(b.Keys, "\x00")
	})
}

// Checker 是单个 R 检查项的签名：**纯函数**，只读输入、只产描述性输出。
//
// 返回值里的 finding 由各检查项用 NewFinding 构造（severity 与诊断码因此恒取自真源表）。
type Checker func(Input) ([]Finding, []RepairSpec)

// checkers 是检查项注册表。
//
// **本 task 恒为空**：T-…-049 只交付骨架，R1–R7 一项未实现。下游 task 各自追加自己的
// 检查项（一个 task 一项，越界注册会被该 task 自己的 Acceptance 判红）。
var checkers []Checker

// Run 跑完已注册的检查项，汇总成可复算的 Result。
//
// 零副作用：不写盘、不提交、不起子进程、不改入参。空结果是**空集合而不是 nil**，
// 与报告侧「findings 恒非 null」的口径同源。
func Run(in Input) Result {
	findings := make([]Finding, 0)
	repairs := make([]RepairSpec, 0)
	for _, c := range checkers {
		if c == nil {
			continue
		}
		fs, rs := c(in)
		findings = append(findings, fs...)
		repairs = append(repairs, rs...)
	}
	SortFindings(findings)
	SortRepairs(repairs)
	return Result{Findings: findings, Repairs: repairs}
}

// HasError 报告本次结果里是否存在 error 级 finding（退出码语义的唯一判据来源，
// 具体退出码由 CLI 层决定，本包不涉及进程退出）。
func (r Result) HasError() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// CheckSet 返回本次结果中出现过的 check 集合（表格行序，去重），供报告与命令层做子集比对。
func (r Result) CheckSet() []string {
	seen := make(map[string]bool, CheckCount)
	for _, f := range r.Findings {
		seen[f.Check] = true
	}
	out := make([]string, 0, CheckCount)
	for _, s := range checkTable {
		if seen[s.Check] {
			out = append(out, s.Check)
		}
	}
	return out
}
