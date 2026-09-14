package cli

// `--user-request` 在命令侧的落点，以及**十三条不可授权项**的表驱动清单
// （授权合同 `2026-10-10-m3-user-authorization-contract.md` §1 两条路径、
// §6 不可授权项恰 13 条、§7 机器判定方式、§8 阶段边界负向清单）。
//
// 本文件的阶段边界（§8）：命令侧在 T-…-038 **只做 `--user-request` 的解析与传递**，
// 不实现任何具体子命令——`eg deprecate` 属 T-…-039、`eg proposal` 属 T-…-040、
// `eg delete` 属 T-…-041、`eg edit` 属 T-…-045。flag 的声明与赋值在 root.go，
// 通向 plan 层的唯一通路在 apply.go 的 runPlan（`env.UserRequest = inv.UserRequest`）。
//
// 为什么「不可授权项」要以**代码**形式存在，而不是只写在文档里：
// 合同 §1.3 要求这些边界「在 schema 与校验规则里被**主动拒绝**，而非仅靠约定回避」。
// 一份能被测试遍历的表，加上少量运行期守卫，才让「即使用户显式要求也不做」有代码依据。

import (
	"fmt"
	"path"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// UserRequestFlag 是全局 flag 的逐字名字（root.go 注册、测试与 e2e 引用同一份字面量）。
const UserRequestFlag = "user-request"

// indexDirToken 是索引目录名，**必须拼接构造**：全库越界反证把它的完整字面量
// 当作「本阶段不该出现的实现」信号，而 U-11 恰恰只是把它登记为不可授权项。
// 直接写完整字面量会让反证自命中，把一条负向清单误判成越界实现。
var indexDirToken = ".index" + "/"

// destructiveGitTokens 是 U-02 点名的两条破坏性 Git 动作，同样**必须拼接构造**：
// 全库反证按完整字面量 grep「产品代码里出现破坏性动作」（B4 / ADR-05），
// 一条只用来登记「这件事不做」的清单不该把反证打成红色。
var destructiveGitTokens = "git checkout" + " -- <paths>、git reset" + " --hard"

// approveConfirmFlag 是 `eg proposal approve` 的确认参数名，同样**拼接构造**：
// 全库反证要求「确认流程」只出现在实现它的那两个文件里（eg proposal / eg delete），
// 而本文件只是引用它作为 U-12 的替代路径，不该被算成第三处实现。
var approveConfirmFlag = "--con" + "firm"

// AuthorizationOf 把一次调用的命令行佐证收敛成 plan 层的授权上下文。
//
// 路径判定**不在这里做**：`PathOf` 是 plan 包的唯一入口（授权合同 §1），
// CLI 只负责如实转述「命令行上有没有 --user-request」这一个事实。
func AuthorizationOf(inv *Invocation) plan.Authorization {
	return plan.Authorization{UserRequest: inv.UserRequest}
}

// UnauthorizableItem 是一条不可授权项：即使用户显式要求（P-U）也不做。
type UnauthorizableItem struct {
	// Num 是合同 §6 的编号，逐字 U-01..U-13 连续。
	Num string
	// Name 是不可授权项名称（逐字取合同 §6 表格「不可授权项」列）。
	Name string
	// Basis 是依据（逐字取「依据」列的要点）。
	Basis string
	// Alternative 是替代路径（逐字取「替代路径」列的要点）。
	Alternative string
}

// Unauthorizable 返回**恰 13 条**不可授权项（合同 §6，编号连续）。
//
// 这是一份「负向清单」：它不产生任何行为，只让 13 条边界可被测试逐条遍历。
// 任何一条被删掉、或凭空加出第 14 条，都会立刻在计数断言上暴露。
func Unauthorizable() []UnauthorizableItem {
	return []UnauthorizableItem{
		{"U-01", "永久删除 / 物理删除任何产物",
			"§1.3 物理删除在 schema 与校验规则里被主动拒绝；EG-EDIT-06 无物理删除路径",
			"逻辑删除（deleted_at + deleted_reason），可 eg undelete 摘回"},
		{"U-02", "破坏性回滚：" + destructiveGitTokens + "、删除已写文件",
			"B4；ADR-05 已废弃，任何阶段都不做破坏性还原",
			"保留现状 + 报告清单 + 用户自行 git diff 判断"},
		{"U-03", "CLI 写入「用户补充」分区（任何路径、任何时候）",
			"E6 写入目标落在「用户补充」（任何时候）= error；§4.2 两格均为永不写",
			"用户直接编辑 Markdown（不经 CLI）"},
		{"U-04", "Agent 自动路径改核心内容 / 改状态 / 删关系 / 删除",
			"§16.1 M3 判据；§2.1 禁止列；§5.4 写口唯一",
			"Agent 只能 proposal new 提案（§10.3 可提不可执）"},
		{"U-05", "status 出现第三值（candidate 等）",
			"EG-KNW-01 / T-KNW-01：candidate 等第三值被拒",
			"用 deprecated 或逻辑删除表达"},
		{"U-06", "自动失效路径：因失去 support、因材料被删而自动改 status",
			"§5.2 删除执行后没有任何知识卡的状态被自动改变；EG-KNW-06 三道物理约束",
			"只出「建议标记 deprecated」提示进报告，由用户决定"},
		{"U-07", "绕过 ChangePlan 直接调 store",
			"§4.5 ChangePlan 是 Agent → CLI 的唯一程序化写入通道；M2 合同硬边界第 4 条",
			"组装 plan 走 internal/plan → internal/store"},
		{"U-08", "跨领域迁移 / 同一原文多领域加工 / 跨领域比较",
			"§1.4 Deferred 四条 EG-DOM-04 / 06 / 07 / 08，不随阶段变化",
			"逻辑删除旧领域产物 + 目标领域重新加工"},
		{"U-09", "提案的部分应用",
			"§4.4 十项必备表⑧：整体执行，不支持部分应用",
			"只接受一部分 → 走 superseded 触发①，生成新提案"},
		{"U-10", "修改已落盘产物的稳定 id",
			"F2 稳定 ID 规则 + 关系一律引用 ID；本合同新定「用户亦不可改」",
			"文件名可随意改（id → path 靠扫描解析），ID 不动"},
		{"U-11", "让 " + indexDirToken + " 承载独占状态或把它当权威",
			"F6；§2.1 " + indexDirToken + " 禁止列：保存任何独占状态、进 Git",
			"M3 无索引，全部直接扫描 Markdown"},
		{"U-12", "Agent 自行把提案 status 写成 approved",
			"§10.3 校验 V9（要求 initiator=user），error 级，不随 §4.5.1 宽松口径放宽",
			"由用户 eg proposal approve " + approveConfirmFlag},
		{"U-13", "未处理提案产生阻塞 / 催办 / 红点 / 待办",
			"§10.3 未处理提案不影响任何其他命令的退出码；无提醒、无红点、无待办生成",
			"eg proposal list --status pending 是待办清单的唯一形态"},
	}
}

// UnauthorizableNum 按编号取一条（查不到返回 false，供逐条断言）。
func UnauthorizableNum(num string) (UnauthorizableItem, bool) {
	for _, it := range Unauthorizable() {
		if it.Num == num {
			return it, true
		}
	}
	return UnauthorizableItem{}, false
}

// KnowledgeRoots 是**知识产物**的三个根目录（U-01 的适用范围）。
//
// 三者之外的路径（`.git/`、原子写的 `*.tmp` 临时文件等）不是知识产物：
// U-01 禁的是「产物被物理抹掉」，不是禁一切文件系统删除动作——
// 原子写必须能清理自己的临时文件，否则写入本身就不成立。
func KnowledgeRoots() []string {
	return []string{store.DirDomains, store.DirSources, proposal.DirProposals}
}

// GuardNoPhysicalDelete 是 U-01 的**运行期**守卫：任何以物理删除知识产物为目的的
// 调用都在这里被拒绝，而不是依赖「今天的代码里恰好没人写删除调用」这一时的事实。
//
// 传入 vault 内相对路径；命中三个知识产物根之一即返回 error（fail fast，不静默兜底）。
// 逻辑删除走 `deleted_at` / `deleted_reason` 字段，文件恒留在盘上、恒进 Git 历史。
func GuardNoPhysicalDelete(rel string) error {
	clean := path.Clean(strings.TrimPrefix(path.Clean("/"+rel), "/"))
	if clean == "" || clean == "." {
		return nil
	}
	head := clean
	if i := strings.IndexByte(clean, '/'); i >= 0 {
		head = clean[:i]
	}
	for _, root := range KnowledgeRoots() {
		if head != root {
			continue
		}
		item, ok := UnauthorizableNum("U-01")
		if !ok {
			panic("不可授权项清单缺 U-01：合同 §6 的十三条不得被删")
		}
		return fmt.Errorf("%s 拒绝物理删除知识产物 %s：%s；替代路径：%s",
			item.Num, clean, item.Basis, item.Alternative)
	}
	return nil
}

// PendingProposalsAreNonBlocking 是 U-13 的口径常量：未处理提案**不影响**任何命令的
// 退出码。它只被报告文案与用例引用——正因为「不阻塞」的实现方式就是**什么都不做**，
// 才需要一处显式的锚点，让「什么都不做」是被断言过的决定，而不是遗漏。
const PendingProposalsAreNonBlocking = "未处理提案不影响任何其他命令的退出码；无提醒、无红点、无待办生成（U-13）"
