package cli

// `eg rel add` 的写路径（M2 查询与关系写入合同 `2026-09-19-m2-query-contract.md` §4；
// ChangePlan 合同 §3.6 / §4.5.1；技术方案 §6、§7.1、§9；T-…-024）。
//
// 本文件只做**一件事**：把三个位置参数 + `--reason` 合成一份**内存** ChangePlan
// （`verb: relate` + 单条 `add_relation` op，不落临时文件），然后交给 apply 与本命令
// **共用**的 runPlan 编排：
//
//	plan.Validate（E1–E6 / W1–W8 / I1）→ plan.Execute（executor → internal/store 写口）
//	→ report → internal/git 一次 `relate` commit
//
// **归属声明**：`add_relation` op 的实现归属唯一属 **T-…-014**（`internal/plan/validate_rel.go`
// + `internal/plan/executor.go` + `internal/store/relation.go`），本文件只复用既有实现——
// 不重新实现、不新增 op 类型、不改其校验语义。`opposing` 的字典序单向归一与同对去重、
// W2 / W8 与 E2 / E3 / E5 全部由既有校验产出，本文件只如实透出到报告与 `--json`。
//
// **禁止绕过 ChangePlan 直接调 store**（合同 §4.2）：本文件不 import `internal/store`，
// 写盘只可能发生在 runPlan 里的 executor 之后。反证由 cli_test.go 的 P1-2 用例承担。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
)

// relAddArgCount 是 `eg rel add` 的位置参数个数：<from> <type> <to>。
const relAddArgCount = 3

// relPlanJSON 是 `--json` 里回带的 plan（合同 §4.2 的样例结构，键序即字段声明序），
// 便于 Agent 原样复投给 `eg apply`。
type relPlanJSON struct {
	PlanVersion    int                 `json:"plan_version"`
	Verb           string              `json:"verb"`
	Domain         string              `json:"domain"`
	Reason         string              `json:"reason"`
	RequirementIDs []string            `json:"requirement_ids"`
	Convergence    []interface{}       `json:"convergence"`
	Base           map[string]string   `json:"base"`
	Ops            []map[string]string `json:"ops"`
}

// runRelAdd 实现 eg rel add <from> <type> <to> --reason <text> [--domain <d>]。
func (r *Root) runRelAdd(inv *Invocation) (*Result, error) {
	from, relType, to := inv.Args[0], inv.Args[1], inv.Args[2]
	reason := inv.String("reason")

	p, jsonPlan, err := r.buildRelationPlan(inv, from, relType, to, reason)
	if err != nil {
		return nil, err
	}

	res, runErr := runPlan(r, inv, p)
	if res != nil {
		res.Data["plan"] = jsonPlan
		res.Summary = append([]string{fmt.Sprintf(
			"rel add：%s --%s--> %s（verb=relate，单条 add_relation op，走 ChangePlan → plan → store → 一次 commit）",
			from, relType, to)}, res.Summary...)
	}
	return res, runErr
}

// buildRelationPlan 组装那份内存 ChangePlan：8 个顶层键齐备、ops 恰一条 add_relation。
//
// base 由 CLI 自己算（`store.ContentHash` 唯一口径，见 apply.go 的 planBase）：
// `rel add` 是单命令闭环，不要求用户先跑 `eg context`；B3 不因此放宽——
// 写前重算不一致仍然跳过并进 `skipped[]`（合同 §4.2）。该专用 `relate` 单关系
// plan 的空 `convergence[]` 是有意形态；plan 校验不得为此伪造三维度或产生 W5。
func (r *Root) buildRelationPlan(inv *Invocation, from, relType, to, reason string) (
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
			Index:       0,
			Name:        plan.OpAddRelation,
			From:        from,
			Type:        relType,
			Target:      to,
			Reason:      reason,
			ReasonGiven: inv.Set("reason"),
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
			"op": plan.OpAddRelation, "from": from, "type": relType,
			"target": to, "reason": reason,
		}},
	}
	return p, jsonPlan, nil
}

// relAddDomain 决定 plan.domain（合同 §4.1）：
// `--domain` 优先 → 其次 from 端点**所在目录**的领域（EG-DOM-01，避免误报 W1）
// → 最后 `default_domain`；三者皆无 → 退 1（CLI 绝不自选领域，EG-DOM-03）。
func relAddDomain(inv *Invocation, fromPath string) (string, error) {
	if d := strings.TrimSpace(inv.String("domain")); d != "" {
		if !inv.Config.HasDomain(d) {
			return "", &UsageError{Msg: fmt.Sprintf(
				"领域 %s 未登记在 %s 的 domains 里：请先 eg config set domains <d1,d2>（eg 绝不自选领域）",
				d, ConfigFileName)}
		}
		return d, nil
	}
	if d := domainOfPath(fromPath); d != "" {
		return d, nil
	}
	if d := strings.TrimSpace(inv.Config.DefaultDomain); d != "" {
		return d, nil
	}
	return "", &UsageError{Msg: fmt.Sprintf(
		"无法确定落位领域：未给 --domain、from 端点不在任何领域目录下且 %s 未配置 default_domain"+
			"（CLI 绝不自选领域）", ConfigFileName)}
}

// validateRelAddArgs 做**参数形态**校验（合同 §4.1 的逐参数分级，一律退 1、零写入）：
// 位置参数个数、`type` 的封闭四值、`--reason` 必填。
//
// 分级取舍（§4.1 与 §4.5 的消歧，记在 T-…-024 的 Activity Log）：
//   - `type` 出四值 → 退 1（§4.1「集合外一律拒绝，退 1」，§4.5 同款列举）；
//   - **完全未给** `--reason` → 退 1（§4.5「缺 --reason」属参数非法）；
//     给了但为空串或等于关系名本身 → 不拦截，由既有 W2 照写并进报告（§4.1）；
//   - k/o 端点 ID 不可解析 / 不存在 / `s-` 等非 k/o 前缀写进 target → 交给 plan 校验产出 E2 / E3（退 2）。
func validateRelAddArgs(inv *Invocation) error {
	if err := rejectReadOnlyVisibilityFlag(inv); err != nil {
		return err
	}
	if len(inv.Args) != relAddArgCount {
		return &UsageError{Msg: fmt.Sprintf(
			"eg rel add 需要恰三个位置参数 <from> <type> <to>，实际 %d 个：%v",
			len(inv.Args), inv.Args)}
	}
	if _, err := model.ParseRelationType(inv.Args[1]); err != nil {
		return &UsageError{Msg: fmt.Sprintf(
			"type=%q 不在论证关系封闭四值内：合法取值恰为 %s（冻结合同 F4）",
			inv.Args[1], relationTypeNames())}
	}
	if !inv.Set("reason") {
		return &UsageError{Msg: "eg rel add 缺必填参数 --reason <text>：关系必须写明理由（EG-CVG-04）"}
	}
	return nil
}

// relationTypeNames 渲染论证关系四值（顺序取 model 的封闭枚举声明序）。
func relationTypeNames() string {
	names := make([]string, 0, len(model.ValidRelationTypes()))
	for _, t := range model.ValidRelationTypes() {
		names = append(names, string(t))
	}
	return strings.Join(names, " | ")
}
