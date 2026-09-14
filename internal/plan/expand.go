package plan

// op → store 展开的收口（合同 §3）与黑名单判定（合同 §7）。
//
// 展开结果是 Action 序列，**顺序严格等于 `ops[]` 的声明顺序**；实际落盘由 store 的
// 字节区间插入执行（本包不写盘）。error 非空时调用方必须零写入。

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
