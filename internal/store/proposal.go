package store

// 提案控制面（`proposals/**`）的 **guarded 写口**（A-23 落地，T-evergreen.s1_main_flow-158614-035）。
//
// A-23 逐字：「仅限 `proposals/**` 提案控制面。**必须复用 guarded store，禁止裸写文件**。
// CLI 负责 Git 与报告。批准后的知识数据修改仍走 ChangePlan。Agent 可创建提案，但不可批准或执行。」
//
// 本文件是该例外在 store 侧的**唯一**落点，两个写口都把路径硬钉在提案目录下：
//   - ApplyProposalCreate  ：新建提案（不覆盖既有文件 + 写前字节自检 + 原子落盘）；
//   - ApplyProposalUpdate  ：改写既有提案（B3 content_hash 比对 → 写前 Parse→Render 自检 →
//     候选字节自检 → 用户分区逐字保留（B2）→ tmp + fsync + rename），复用 mutateGuarded，
//     与 `remove_relation` / `replace_block` **同一条**守卫链，不另开一条快车道。
//
// 为什么写口在 store 而不在 internal/proposal：A-23 第 2 条要求「复用 guarded store」，
// 而 store 是本仓唯一持有原子写原语的包（writeAtomic 不导出）。internal/proposal 只负责
// 拼字节与形态自检，落盘一律经这里 —— 因此提案包里没有任何裸写文件调用。
//
// 知识数据面（五分区权威 Markdown）**不适用**本例外：那条路径恒为 internal/plan → store，
// 批准后也不例外（提案合同 §8.5.1 表第 2 行）。

import (
	"errors"
	"fmt"
	"path"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// dirProposals 是提案目录名（vault 根，不属于任何领域；冻结合同 F1）。
//
// 与 internal/proposal 的 DirProposals、internal/query 的 dirProposals 同值：
// store 不得反向依赖 S2 的提案包（依赖方向由 cmd/eg 的 arch 用例钉死），
// 因此这里持有同一份目录名字面量，由 TestProposalWritePortRejectsOutsideDir 锁死口径。
const dirProposals = "proposals"

// kindProposal 是提案在 CreateFile 里的类型标记。
//
// 提案正文是**恰 7 个 H2** 的另一套结构（提案合同 §7.3），**不在** F5 冻结的
// 「知识卡五分区 / 材料笔记五分区」之内，因此不参与 mdfile 的分区白名单校验；
// 提案的分区形态由 internal/proposal 的 Parse / ValidateLayout 单独判死。
const kindProposal = mdfile.Kind("proposal")

// ErrNotProposalRel 表示写口拿到的相对路径不在提案目录下（A-23 第 1 条：仅限提案控制面）。
var ErrNotProposalRel = errors.New("提案写口只接受 " + dirProposals + "/ 下的路径（A-23 直写例外仅限提案控制面）")

// ErrProposalContentEmpty 表示候选字节为空（空文件不是合法提案，拒写）。
var ErrProposalContentEmpty = errors.New("提案候选字节为空，拒写")

// ProposalUpdateSpec 是一次提案改写的落盘输入。
//
// Content 是调用方**按字节拼好**的完整候选文件（沿用 mdfile 的 Doc/Span 半开区间 +
// 字节级替换产出，未知分区与未知 YAML 字段逐字保留）；本写口不做任何 YAML 序列化。
type ProposalUpdateSpec struct {
	Rel          string
	ExpectedHash string
	Content      []byte
}

// IsProposalRel 报告 vault 内相对路径是否落在提案目录下（只读判据，写口与调用方共用）。
func IsProposalRel(rel string) bool {
	parts := splitSlash(path.Clean(rel))
	return len(parts) >= 2 && parts[0] == dirProposals
}

// ApplyProposalCreate 新建一份提案文件（A-23 的两个写口之一）。
//
// 命名沿用 store 既有的 `Apply*` 领域写口（ApplyCard / ApplyNote / ApplyReplaceBlock …）：
// **B1 的三种写形态恰是 CreateFile / AppendToSection / WriteGuarded**，本函数不是第四种形态，
// 只是把提案控制面的路径约束叠在 CreateFile 之上（TestB1WriteFormsAreExactlyThree 锁死这一点）。
//
// 固定次序（与 CreateFile 同源）：路径必须落在提案目录下 → 不覆盖既有文件 →
// 写前字节自检（Parse→Render 不等即拒写）→「用户补充」必须为空（提案没有该分区，天然成立）
// → 原子落盘（tmp + fsync + rename）。
func (s *Store) ApplyProposalCreate(rel string, content []byte) (Result, error) {
	if !IsProposalRel(rel) {
		return Result{Path: rel}, fmt.Errorf("%w：%s", ErrNotProposalRel, rel)
	}
	if len(content) == 0 {
		return Result{Path: rel}, fmt.Errorf("%w：%s", ErrProposalContentEmpty, rel)
	}
	return s.CreateFile(rel, kindProposal, content)
}

// ApplyProposalUpdate 改写一份既有提案（A-23 的两个写口之一）。
//
// 全程复用 mutateGuarded：B3 content_hash 比对 → 原文字节自检 → 候选字节自检 →
// B2 用户分区逐字保留 → tmp + fsync + rename → 写后 YAML 只读复核。
// 不覆盖、不强写、不重试、不回滚；冲突返回 *SkipError（Reason=file_changed）。
func (s *Store) ApplyProposalUpdate(spec ProposalUpdateSpec) (Result, error) {
	if !IsProposalRel(spec.Rel) {
		return Result{Path: spec.Rel}, fmt.Errorf("%w：%s", ErrNotProposalRel, spec.Rel)
	}
	if len(spec.Content) == 0 {
		return Result{Path: spec.Rel}, fmt.Errorf("%w：%s", ErrProposalContentEmpty, spec.Rel)
	}
	return s.mutateGuarded(spec.Rel, spec.ExpectedHash,
		func(f File, doc *mdfile.Doc) ([]byte, error) { return spec.Content, nil })
}
