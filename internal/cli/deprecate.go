package cli

// `eg deprecate` 的写路径（授权合同 `2026-10-10-m3-user-authorization-contract.md`
// §1 两条路径 / §2 写权限矩阵 #3；提案与状态合同 §8.1 的 `deprecate` op；T-…-039）。
//
// 本文件只做一件事：把 `--target` + `--reason` 合成一份**内存** ChangePlan
// （单条 `deprecate` op，不落临时文件），然后交给 apply / rel add 共用的 runPlan 编排：
//
//	plan.EnvFor → env.UserRequest → plan.Validate（W7 / W11 / 矩阵 #3）
//	→ plan.Execute（executor → internal/store 的 ApplyStateWrite 唯一写口）
//	→ report → internal/git 一次 commit
//
// **不新增任何写入链路**：状态落盘的唯一实现在 `internal/store/state_write.go`（前一层已落地），
// 本文件既不 import store、也不复制一份 frontmatter 改写逻辑——写口唯一护栏
// `TestStateWritePortIsUniqueToStore` 要求 `SetStatus` 的非测试命中全部落在 internal/store/。
//
// 阶段边界（越界一律不做）：不写 `deleted_at`（T-…-041 的 `eg delete`）、不产出
// `[已删除]` / `[未过目]` 标记（T-…-043）、不判依据是否充分（S3）、不生成提案（T-…-040）。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

// runDeprecate 实现 eg deprecate --target <id> --reason <text>。
func (r *Root) runDeprecate(inv *Invocation) (*Result, error) {
	return r.runStatusCommand(inv, plan.OpDeprecate, "deprecate（status → deprecated）")
}

// runStatusCommand 是 `eg deprecate` 与 `eg restore` 的共用编排。
//
// 两条命令的差别**只有 op 名**（落盘时写 `status: deprecated` 还是 `status: active`，
// 由 plan 层的 validate_m3 决定），因此这里合成的形态必须只有一处：
// 复制成两份会让「一次命令 = 单条 op = 一次 commit」在两处各自漂移。
func (r *Root) runStatusCommand(inv *Invocation, opName, label string) (*Result, error) {
	target, reason := strings.TrimSpace(inv.String("target")), inv.String("reason")

	p, err := r.buildStateOpPlan(inv, opName, target, reason, nil)
	if err != nil {
		return nil, err
	}

	res, runErr := runPlan(r, inv, p)
	if res != nil {
		res.Summary = append([]string{fmt.Sprintf(
			"%s：目标 %s（单条 %s op，initiator=user，走 ChangePlan → plan → store → 一次 commit）",
			label, target, opName)}, res.Summary...)
	}
	return res, runErr
}

// buildStateOpPlan 组装那份内存 ChangePlan：单条状态类 op，`initiator: user` 恒为真。
//
// 为什么这里可以直接把 `initiator: user` 与 UserRequest 一起置真（授权合同 N-1 反伪造条款）：
// N-1 约束的是「**Agent** 在 plan 文件里写 `initiator: user` 自证」——plan 文件内容不能自证。
// 而用户在终端敲 `eg deprecate` 这一动作本身就发生在**进程边界**上，即命令行佐证本身，
// 与 `eg apply --user-request` 同源。因此这三条命令不要求用户再手敲 `--user-request`：
// 矩阵 #3 / #4 的 P-A 是 🔴、P-U 是 ✅，唯有用户显式路径可写 status / replaced_by。
//
// replaced 非 nil 时（`eg replaced-by`）载荷落到 op 的 `replaced_by` 子对象，
// 其余情形 op 只携带 target + reason（合同 §8.1 的字段表）。
func (r *Root) buildStateOpPlan(inv *Invocation, opName, target, reason string,
	replaced *plan.ReplacedBy) (*plan.ChangePlan, error) {
	baseIDs := r.stateOpBaseIDs(target, replaced)
	base, paths, err := planBase(inv.VaultRoot, baseIDs)
	if err != nil {
		return nil, err
	}
	// 声明 base 来源：本次是 CLI 在锁外自算的采样，临界区会在 S2 恢复屏障之后按同一组 ID
	// 重采一次（I-…-022）。不声明就会让崩溃恢复后的第一条状态类写命令被自己的恢复判成 B3 冲突。
	inv.selfComputedBase(baseIDs)

	domain, derr := stateOpDomain(inv, paths[target])
	if derr != nil {
		return nil, derr
	}

	// 命令行佐证：`--user-request` 是 apply 那条 Agent 通道的显式声明，
	// 而本命令的存在本身即用户显式路径，故在进程边界上把佐证置真（见上方 N-1 说明）。
	inv.UserRequest = true

	op := &plan.Op{
		Index:          0,
		Name:           opName,
		Target:         target,
		Reason:         reason,
		ReasonGiven:    inv.Set("reason"),
		Initiator:      plan.InitiatorUser,
		InitiatorGiven: true,
		ReplacedBy:     replaced,
	}
	// verb 取 `process`：S1 冻结的六个提交动词里没有状态动词，且 `relate` 的窄例外
	// 只认单条 add_relation / remove_relation（M-13 / M-15），借它躲 W5 一律不成立。
	return &plan.ChangePlan{
		Version:        plan.PlanVersion,
		VersionRaw:     plan.PlanVersion,
		Verb:           string(model.VerbProcess),
		VerbGiven:      true,
		Domain:         domain,
		DomainGiven:    true,
		Reason:         reason,
		RequirementIDs: []string{},
		Base:           base,
		Ops:            []*plan.Op{op},
		Extra:          map[string]interface{}{},
	}, nil
}

// stateOpBaseIDs 列出需要进 base 的 ID：主体卡恒在内；`replaced_by.target`
// 一并计入是因为 plan 校验要读它的 status / deleted_at 来判 E10 / W12。
func (r *Root) stateOpBaseIDs(target string, replaced *plan.ReplacedBy) []string {
	if replaced == nil || replaced.Target == "" {
		return []string{target}
	}
	return []string{target, replaced.Target}
}

// stateOpDomain 决定 plan.domain：目标卡**所在目录**的领域优先（EG-DOM-01，
// 领域由目录唯一决定，绝不从 frontmatter 猜）→ 其次 `default_domain`；
// 两者皆无 → 退 1（CLI 绝不自选领域，EG-DOM-03）。三条状态命令不收 `--domain`：
// 目标卡已落盘，其领域是既成事实，允许用户另指领域只会引入伪造空间。
func stateOpDomain(inv *Invocation, targetPath string) (string, error) {
	if d := domainOfPath(targetPath); d != "" {
		return d, nil
	}
	if d := strings.TrimSpace(inv.Config.DefaultDomain); d != "" {
		return d, nil
	}
	return "", &UsageError{Msg: fmt.Sprintf(
		"无法确定落位领域：目标卡不在任何领域目录下且 %s 未配置 default_domain"+
			"（CLI 绝不自选领域）", ConfigFileName)}
}

// requireStateTargetAndReason 做**参数形态**校验（缺必填一律退 1、零写入、零 commit）。
//
// `--reason` 必填是硬要求：状态类 op 的 W7 后半在 plan 层是 error，命令侧提前在退 1
// 这一级拦住，使「用法错误」与「校验失败（退 2）」两类失败不互相冒名。
func requireStateTargetAndReason(inv *Invocation, cmd string) error {
	if err := noPositionalArgs(inv); err != nil {
		return err
	}
	if strings.TrimSpace(inv.String("target")) == "" {
		// replaced-by 的宿主是跨类型端点（知识卡 k- 或观点 o-），文案区别于四条
		// card-only 状态命令（deprecate / restore / delete / undelete 仍是 <k-id>）。
		if cmd == "replaced-by" {
			return &UsageError{Msg: "eg replaced-by 缺必填参数 --target <k|o-id>：" +
				"替代指针必须点名替代宿主端点"}
		}
		return &UsageError{Msg: fmt.Sprintf(
			"eg %s 缺必填参数 --target <k-id>：状态写入必须点名目标卡", cmd)}
	}
	if !inv.Set("reason") || strings.TrimSpace(inv.String("reason")) == "" {
		return &UsageError{Msg: fmt.Sprintf(
			"eg %s 缺必填参数 --reason <text>：状态变更必须写明理由（授权合同 §9 A-15）", cmd)}
	}
	return nil
}
