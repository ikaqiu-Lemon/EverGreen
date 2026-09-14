package cli

// `eg rel remove` 的写路径：M3 接管 M2 留下的唯一显式阶段占位（M2 查询合同 §7 的占位形态
// 到此终止；提案与状态合同 §8.1 第 8 行的 `remove_relation` op；授权合同 §2 写权限矩阵
// #11「删关系」；T-…-044）。
//
// 本文件只做**一件事**：把三个位置参数 + `--reason` 合成一份**内存** ChangePlan
// （`verb: relate` + 单条 `remove_relation` op，不落临时文件），然后交给 apply / rel add
// **共用**的 runPlan 编排：
//
//	plan.EnvFor → env.UserRequest → plan.Validate（E2 / E3 / E5 / W2 / W3 / W8 / W10 / 矩阵 #11）
//	→ plan.Execute（executor → internal/store 的 ApplyRemoveRelation 唯一写口）
//	→ report → internal/git 一次 `relate` commit
//
// **不新增写入链路、不新增 op 类型、不新增诊断码**：op 与其校验归 T-…-037
// （`internal/plan/validate_m3.go` 的 removeRelation + `internal/store/mutate.go` 的
// ApplyRemoveRelation），本文件既不 import `internal/store`、也不自己拼字节——
// 关系条目的字节剪除只有一处实现，复制第二份会让 A-24 的匹配口径两处各自漂移。
//
// **落盘语义按 owner 裁决执行，本层不拍板**（`docs/specs/2026-10-13-m3-prestart-adjudication.md`
// §7.2 A-24 逐字结论 = `物理移除`）：命中即从宿主卡 frontmatter 的 `relations[]` 里
// 删掉规范化 `(from,type,target)` 匹配的**全部**记录，**不留墓碑、不创建 RelationID**。
// 它与 §5.2「关系记录不被物理删除」不冲突：后者约束的是**逻辑删除卡**时不得级联清理
// 指向它的关系（U-01），而这里是用户点名删一条关系这个**独立动作**。
//
// **W10 幂等**：未命中任何既有关系时校验期就收口成 warning、不产出 action，
// 因此零写入、不产生空 commit、退出码不受影响（0）。理由是「重跑同一 plan 幂等」：
// 判 error 会让第二次重跑失败，把幂等变成陷阱。
//
// 阶段边界（越界一律不做）：不改 `rel add` 与 `rel` 读路径的既有行为与输出（M2 已冻结）；
// 不做关系的批量删除 / 撤销删除 / 历史回放；不实现反向 `replaced_by` 查询（S4）；不引索引。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

// runRelRemove 实现 eg rel remove <from> <type> <to> --reason <text>。
func (r *Root) runRelRemove(inv *Invocation) (*Result, error) {
	from, relType, to := inv.Args[0], inv.Args[1], inv.Args[2]
	reason := inv.String("reason")

	p, jsonPlan, err := r.buildRelationRemovePlan(inv, from, relType, to, reason)
	if err != nil {
		return nil, err
	}

	res, runErr := runPlan(r, inv, p)
	if res != nil {
		res.Data["plan"] = jsonPlan
		res.Summary = append([]string{fmt.Sprintf(
			"rel remove：%s --%s--> %s（verb=relate，单条 remove_relation op，initiator=user，"+
				"物理移除匹配记录；未命中记 W10 幂等 no-op，零写入零 commit）",
			from, relType, to)}, res.Summary...)
	}
	return res, runErr
}

// buildRelationRemovePlan 组装那份内存 ChangePlan：ops 恰一条 remove_relation。
//
// 为什么这里可以把 `initiator: user` 与 UserRequest 一起置真（授权合同 N-1 反伪造条款）：
// N-1 约束的是「**Agent** 在 plan 文件里写 `initiator: user` 自证」——文件内容不能自证授权。
// 而用户在终端敲 `eg rel remove` 这一动作本身就发生在**进程边界**上，即命令行佐证本身，
// 与 `eg apply --user-request` 同源。风险归档：矩阵 #11「删关系」P-A 🔴 / P-U ✅，
// 属「用户显式且低风险」一档（与 deprecate / restore / mark-reviewed 同档，
// 关系可由 `eg rel add` 原样重建、原因由 Git 历史承载），因此**不**要求用户再手敲
// `--user-request`，也**不**需要 proposal approve / delete 那种高风险执行闸门。
// Agent 自动路径（不经本命令、直接投 plan）则拿不到这行佐证，被矩阵 #11 拦下退 2、零写入。
//
// base 由 CLI 自己算（`store.ContentHash` 唯一口径，见 apply.go 的 planBase）：
// B3 不因此放宽——写前重算不一致仍然跳过并进 `skipped[]`。
func (r *Root) buildRelationRemovePlan(inv *Invocation, from, relType, to, reason string) (
	*plan.ChangePlan, relPlanJSON, error) {
	base, paths, err := planBase(inv.VaultRoot, []string{from, to})
	if err != nil {
		return nil, relPlanJSON{}, err
	}
	// base 是锁外采样，临界区在 S2 之后按同一组 ID 重采（I-…-022）；B3 依旧逐字比对。
	inv.selfComputedBase([]string{from, to})

	domain, derr := relAddDomain(inv, paths[from])
	if derr != nil {
		return nil, relPlanJSON{}, derr
	}

	// 命令行佐证：本命令的存在本身即用户显式路径（见上方 N-1 说明）。
	inv.UserRequest = true

	p := &plan.ChangePlan{
		Version:        plan.PlanVersion,
		VersionRaw:     plan.PlanVersion,
		Verb:           string(model.VerbRelate),
		VerbGiven:      true,
		Domain:         domain,
		DomainGiven:    true,
		Reason:         reason,
		RequirementIDs: []string{},
		Base:           base,
		Ops: []*plan.Op{{
			Index:          0,
			Name:           plan.OpRemoveRelation,
			From:           from,
			Type:           relType,
			Target:         to,
			Reason:         reason,
			ReasonGiven:    inv.Set("reason"),
			Initiator:      plan.InitiatorUser,
			InitiatorGiven: true,
		}},
		Extra: map[string]interface{}{},
	}

	jsonPlan := relPlanJSON{
		PlanVersion:    plan.PlanVersion,
		Verb:           string(model.VerbRelate),
		Domain:         domain,
		Reason:         reason,
		RequirementIDs: []string{},
		Convergence:    []interface{}{},
		Base:           base,
		Ops: []map[string]string{{
			"op": plan.OpRemoveRelation, "from": from, "type": relType,
			"target": to, "reason": reason, "initiator": plan.InitiatorUser,
		}},
	}
	return p, jsonPlan, nil
}

// validateRelRemoveArgs 做**参数形态**校验（与 `rel add` 同一分级口径，一律退 1、零写入）：
// 位置参数个数、`type` 的封闭四值、`--reason` 必填。
//
// 分级取舍与 `rel add` 逐条对齐（同一子命令族不得两套口径）：
//   - `type` 出四值 → 退 1（冻结合同 F4）；
//   - **完全未给** `--reason` → 退 1（属参数非法）；给了但为空串或等于关系名本身 → 不拦截，
//     由既有 W2 照写并进报告；
//   - 卡 ID 不可解析 / 不存在 / `s-` 写进 target → 交给 plan 校验产出 E2 / E3（退 2）；
//   - 关系不存在**不是**参数错误，交给 W10 幂等（退 0）。
func validateRelRemoveArgs(inv *Invocation) error {
	if err := rejectReadOnlyVisibilityFlag(inv); err != nil {
		return err
	}
	if len(inv.Args) != relAddArgCount {
		return &UsageError{Msg: fmt.Sprintf(
			"eg rel remove 需要恰三个位置参数 <from> <type> <to>，实际 %d 个：%v",
			len(inv.Args), inv.Args)}
	}
	if _, err := model.ParseRelationType(inv.Args[1]); err != nil {
		return &UsageError{Msg: fmt.Sprintf(
			"type=%q 不在论证关系封闭四值内：合法取值恰为 %s（冻结合同 F4）",
			inv.Args[1], relationTypeNames())}
	}
	if !inv.Set("reason") {
		return &UsageError{Msg: "eg rel remove 缺必填参数 --reason <text>：" +
			"删关系必须写明理由（授权合同 §9 A-15 的同款口径）"}
	}
	return nil
}
