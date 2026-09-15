package plan

// op → store 展开的收口（合同 §3）与黑名单判定（合同 §7）。
//
// 展开结果是 Action 序列，**顺序严格等于 `ops[]` 的声明顺序**；实际落盘由 store 的
// 字节区间插入执行（本包不写盘）。error 非空时调用方必须零写入。
//
// 本文件同时是 Schema v2 **兼容别名的唯一规范化点**（契约 §4.4）：`create_card` /
// `append_card` 在此改名为规范名，`validate.go` 与 `executor.go` 因此看不到别名。

import (
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// nowFn 是本包唯一的时间来源（测试可替换，保证展开是确定性的）。
var nowFn = time.Now

// Targets 返回本次将被写入的文件清单（去重、保序），供 `--dry-run` 输出与报告 links[]。
func (r *Result) Targets() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range r.Actions {
		if a.Skip || a.Path == "" || seen[a.Path] {
			continue
		}
		switch a.Kind {
		case ActSourceReuse, ActNoteReuse:
			continue // 复用形态零写入，不进写入清单。
		}
		seen[a.Path] = true
		out = append(out, a.Path)
	}
	return out
}

// SkipActions 返回被跳过的 action（W6：base 未覆盖 → 整文件跳过）。
func (r *Result) SkipActions() []Action {
	var out []Action
	for _, a := range r.Actions {
		if a.Skip {
			out = append(out, a)
		}
	}
	return out
}

// Diagnostics 返回全部诊断（error 在前，与 CLI 渲染顺序一致）。
func (r *Result) Diagnostics() []Diagnostic {
	out := make([]Diagnostic, 0, len(r.Errors)+len(r.Warnings))
	out = append(out, r.Errors...)
	return append(out, r.Warnings...)
}

// classifyExtra 判定一个**未知附加字段**的分级（合同 §7）。
//
// 黑名单一律按 **YAML / JSON 字段路径**匹配，**严禁关键词扫描**——
// `relations[].type`、`sources[].rel`、`plan.domain`、`target_domain` 都是合法且必需的用法，
// 关键词扫描会把它们误判成废弃设计。命中黑名单 → W4（该字段原样忽略，其余照写）；
// 其余未知字段 → I1（原样忽略 + 进报告，前向兼容）。
func classifyExtra(opIndex int, prefix, path, key string) Diagnostic {
	fieldPath := prefix + "." + key
	switch {
	case model.IsAllowedFieldPath(fieldPath) || model.IsAllowedFieldPath(key):
		return infoAt(opIndex, path, "字段 %q 属白名单用法，本 op 未消费，已原样忽略", fieldPath)
	case model.IsDeprecatedFieldPath(fieldPath):
		return warnAt(W4, opIndex, path,
			"字段路径 %q 命中废弃字段黑名单：该字段原样忽略，其余照写（S5 起才严格化）", fieldPath)
	default:
		return infoAt(opIndex, path, "未知附加字段 %q 已原样忽略（前向兼容：字段与 op 只增不改）", fieldPath)
	}
}

// normalizeAliases 把兼容别名改写成规范名（契约 §4.4 的**唯一**改写点）。
//
// 调用点在 Parse 的末尾（schema.go），即「解析完成、校验开始之前」的规范化阶段，
// 因此 validate 与 executor 只会见到 `create_knowledge` /
// `append_knowledge`；两处各写一份 `case "create_card"` 是被契约明令禁止的
// ——分支一旦重复，日后只改一处就会让同一份 plan 在校验期与执行期作用于不同实体。
//
// 改写只动 `Op.Name`，**不动任何字段字节**：别名与规范名的字段表逐字相同
// （见 opKnownKeys 的合并 case），因此「同一语义的 plan 用两种写法执行，
// 落盘结果字节等价」这条验收标准由「除名字外什么都没变」直接保证，
// 而不是由两条各自维护的执行路径巧合地保证。
//
// 每条改写产出一条 `I1` 迁移提示（不新增 info 编号：本包恰有 I1 一个）。
func normalizeAliases(p *ChangePlan) {
	aliases := OpAliases()
	for _, op := range p.Ops {
		canonical, ok := aliases[op.Name]
		if !ok {
			continue
		}
		alias := op.Name
		op.Name = canonical
		p.Diags = append(p.Diags, infoAt(op.Index, opPath(op.Index, "op"),
			"op %q 是 %q 的兼容别名，已按规范名执行：落盘结果与直接写 %q 字节等价；"+
				"兼容期结束后 %q 将按未知 op 拒绝（契约 §4.4）",
			alias, canonical, canonical, alias))
	}
}

// AliasedOpNames 返回本次 plan 里用到的兼容别名（按 ops 声明顺序，去重）。
//
// 供报告与用例复算「哪些 op 走了兼容路径」。它读的是**改写前**的名字，
// 因此必须在 normalizeAliases 之前调用；Parse 之后调用恒返回空——
// 这正是「validate 与 executor 看不到别名」的一个可观测证据。
func AliasedOpNames(p *ChangePlan) []string {
	aliases := OpAliases()
	seen := map[string]bool{}
	var out []string
	for _, op := range p.Ops {
		if _, ok := aliases[op.Name]; !ok || seen[op.Name] {
			continue
		}
		seen[op.Name] = true
		out = append(out, op.Name)
	}
	return out
}
