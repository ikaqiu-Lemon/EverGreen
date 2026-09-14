package reconcile

// R1 —— Git 纳管的**只读检查**（对账合同 §4；M4 · T-…-050）。
//
// 唯一职责：把「外部编辑产生的未提交改动」检出为一条 git_uncommitted finding
// （W13 / warning，见 check.go 的唯一真源表），`targets[]` = 受影响的 vault 相对路径
// （去重 + 字典序升序，由 NormalizeTargets 归一化）。
//
// # 本文件**不做**的事（结构上做不到，不靠自律）
//
//   - 不写盘、不发提交、不起子进程：本文件不引 os 的写 API、不引子进程包，
//     也**不持有** *git.Repo —— Git 事实只以 git.Status（`git status --porcelain` 的
//     只读采样快照）的形态从 Input 进来，因此调不到 git 包的任何写方法。
//     纳管提交由 CLI 层唯一的写口承担（`reconcile_commit.go`，A-30 / A-35）。
//   - 不产出 RepairSpec：R1 的动作是 Git 纳管，**不是**知识数据修改，
//     故不经 ChangePlan、不写 frontmatter 一个键（A-23 窄例外一字不放宽）。
//     产 RepairSpec 的只有 R2（T-…-051）与 R6（T-…-055）。
//   - 不判定 R2 ~ R7 任何一项，不注册任何命令，不动报告体。
//
// # 与「恰一次 commit」的关系
//
// 本检查只回答「有没有未提交的外部编辑、是哪些路径」；「提交几次」是写口侧的事实：
// 零改动即零 commit、有改动恰一次 commit（合同 §4 / A-35）。两者的接缝是
// UncommittedTargets：命令层拿它当纳管前的路径清单，与 finding 的 targets 同源同序。

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
)

// R1 是本检查项的 R 编号（与 checkTable 内 CheckGitUncommitted 行的 R 列同值）。
const R1 = "R1"

// gitDirPrefix 是 Git 自身的元数据目录前缀：它不属工作区内容，
// `git status --porcelain` 正常也不会报它，此处只作防御性排除（不做任何 IO）。
const gitDirPrefix = ".git/"

// UncommittedTargets 返回 vault 范围内**未提交改动**的 vault 相对路径集合。
//
// 口径（合同 §4「`git status --porcelain` 在 vault/ 范围内的非空条目」）：
//   - 空路径条目丢弃（porcelain 解析残留）；
//   - 路径一律折算成 **vault 相对路径**并归一化成斜杠形态；
//   - 落在 vault 之外的条目（绝对路径不在 VaultRoot 下、或相对路径逃出 vault）丢弃 ——
//     对账只对本 vault 负责；
//   - `.git/**` 丢弃（Git 元数据不是 vault 内容）；
//   - 结果去重 + 字典序升序（NormalizeTargets），因此**可逐字复算**。
//
// 状态码（porcelain 的 XY）**不参与筛选**：未跟踪（`??`）、已暂存（`M `）、
// 工作区修改（` M`）、删除、重命名一律算「未提交的改动」——它们都会被纳管进同一次提交
// （`git add -A` 口径，M1 起一字不变，不得改成选择性 add）。
func UncommittedTargets(in Input) []string {
	out := make([]string, 0, len(in.Status.Changes))
	for _, c := range in.Status.Changes {
		rel, ok := vaultRel(in.VaultRoot, c.Path)
		if !ok {
			continue
		}
		out = append(out, rel)
	}
	return NormalizeTargets(out)
}

// vaultRel 把一条 porcelain 路径折算成 vault 相对路径（纯字符串运算，零 IO）。
//
// 第二个返回值为 false 表示该条目**不在 vault 范围内**（应被丢弃）。
func vaultRel(vaultRoot, path string) (string, bool) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", false
	}
	if filepath.IsAbs(p) {
		root := strings.TrimSpace(vaultRoot)
		if root == "" {
			return "", false
		}
		r, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
		if err != nil {
			return "", false
		}
		p = r
	}
	p = filepath.ToSlash(filepath.Clean(p))
	switch {
	case p == "." || p == "..":
		return "", false
	case strings.HasPrefix(p, "../"):
		return "", false // 逃出 vault 的条目不属本次对账范围
	case p == strings.TrimSuffix(gitDirPrefix, "/") || strings.HasPrefix(p, gitDirPrefix):
		return "", false // Git 元数据不是 vault 内容
	}
	return p, true
}

// r1Detail 渲染 finding 的 detail：非空中文单句 + 足以复算的事实
// （条目数 + 全部受影响路径，路径顺序与 targets 逐字相同）。
func r1Detail(targets []string) string {
	return fmt.Sprintf(
		"发现 %d 处未提交的外部编辑（%s 在 vault 范围内的非空条目）：%s；"+
			"本次对账把它们一并纳管进同一次提交（%s 口径，零改动时不产生空提交）",
		len(targets), statusCommand, strings.Join(targets, "、"), addAllSemantics)
}

// statusCommand / addAllSemantics 是两条**口径字面量**：前者是本检查的事实来源，
// 后者是写口侧的提交范围口径（M1 起一字不变，R-13 / R-15 继承，不得改成选择性 add）。
// 写在这里只为让 detail 逐字可复算 —— 本包既不执行前者也不执行后者。
const (
	statusCommand   = "git status --porcelain"
	addAllSemantics = "git add -A"
)

// checkR1GitUncommitted 是 R1 检查项本体：命中即恰一条 finding，零 RepairSpec。
//
// 纯函数：同一 Input 恒得同一输出；不改入参、不产生任何副作用。
// 工作区干净（无条目 / 条目全部落在 vault 之外）→ 返回**空集合**，
// 于是报告侧 git_uncommitted 条数为 0、写口侧零改动零提交（合同 §4 的「零改动即零 commit」）。
func checkR1GitUncommitted(in Input) ([]Finding, []RepairSpec) {
	targets := UncommittedTargets(in)
	if len(targets) == 0 {
		return nil, nil
	}
	f, err := NewFinding(CheckGitUncommitted, targets, r1Detail(targets))
	if err != nil {
		// 只有「未知 check / 空 detail」两种构造错误，两者在本文件都不可能发生
		// （check 取自封闭表常量、detail 由 r1Detail 恒产非空串）。防御性丢弃而不 panic：
		// 对账是只读检查，任何情况下都不该让进程死在检查器里。
		return nil, nil
	}
	return []Finding{f}, nil
}

// 注册：R1 是本包**第一个**落地的检查项（T-…-050）。
//
// 注册表 checkers 在 reconcile.go 内声明，一个 task 只在自己的文件里追加自己那一项 ——
// R2 ~ R7 分属 T-…-051 ~ T-…-056，本文件因此**恰追加一项**。
func init() { checkers = append(checkers, checkR1GitUncommitted) }

// R1StatusOf 是给命令层与用例的便利函数：把 git 包的只读状态快照直接折成 Input。
//
// 存在的理由：R1 只需要 Git 状态，不需要 vault 扫描结果（Scan 恒可为 nil），
// 让调用方不必为了跑 R1 先做一次全量扫描 —— 但**扫描底座仍复用 internal/query**，
// 本包不另写扫描器（合同 §0.1 第 1 条）。
func R1StatusOf(vaultRoot string, st git.Status) Input {
	return Input{VaultRoot: vaultRoot, Status: st}
}
