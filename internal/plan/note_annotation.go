package plan

// v2 审阅式 Note 的**批注校验层**（Schema v2 契约 §4.2 第 4 条 / §4.2.2 / D-10）：
// 只加严 plan_version: 2 且 blocks[] 给出的 write_note，逐字段判 E2（不新增码），整条 op 零写入。
//
// 本文件只判「字段该不该出现 / 批注 key 合不合法」，不碰 source_ref 覆盖（那在 source_coverage.go）、
// 不碰结构资产保真（note_fidelity.go）、不做渲染（渲染在 store.NoteReviewBytes → mdfile）。
// 三件事分层：字段形态是最外层的前置条件——字段放错了位置，谈覆盖与保真都没有意义，
// 因此本校验在 noteBlockWrites 里跑在覆盖 / 保真之前，一旦失败就零写入、不再往下走。
//
// # 为什么 role 决定字段用途（D-10）
//
// role 表达 provenance（这段字来自原文还是 Agent），annotation 表达教学意图（这条批注是导读还是
// 辨析）。二者正交：来源块只回指原文行段（source_ref），没有「教学意图」可言；Agent 批注是就近
// 推导，不对应某段原文，没有 source_ref 可言。把 annotation 放进 source 块、或把 source_ref 放进
// agent 块，都是把两个正交维度混成一谈——用字段级 E2 逼作者把话说清楚，而不是替他猜。

import (
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// noteAnnotationValidate 逐块校验 role 与字段的对应关系及批注词表（§4.2 第 4 条 / §4.2.2）。
//
// 返回 false 表示已登记至少一条字段级 E2、整条 write_note 零写入。**不短路**：逐块把每处
// 错误都钉到 ops[i].blocks[j].<字段>，让作者一次看清所有要改的字段，而不是改一个再撞下一个。
func (v *validator) noteAnnotationValidate(op *Op) bool {
	ok := true
	for i, b := range op.Blocks {
		switch b.Role {
		case NoteBlockSource:
			if b.Annotation != "" {
				v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "annotation"),
					"role: %s 的块不得带 annotation：annotation 是 Agent 批注的教学意图维度，"+
						"来源内容的整理只用 source_ref 回指原文行段（契约 §4.2 第 4 条 / D-10）",
					NoteBlockSource))
				ok = false
			}
			if b.Label != "" {
				v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "label"),
					"role: %s 的块不得带 label：label 只承载 Agent 扩展批注的人读标签，"+
						"来源块没有批注意图可言（契约 §4.2.2）", NoteBlockSource))
				ok = false
			}
		case NoteBlockAgent:
			if b.SourceRef != "" {
				v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "source_ref"),
					"role: %s 的块不得带 source_ref：source_ref 用于来源块回指原文行段，"+
						"Agent 批注是就近推导、不对应某段原文（契约 §4.2 第 4 条 / D-10）",
					NoteBlockAgent))
				ok = false
			}
			if !v.agentAnnotationValid(op, i, b) {
				ok = false
			}
		}
	}
	return ok
}

// agentAnnotationValid 校验单个 agent 块的 annotation/label（§4.2.2）：
//
//   - annotation 必须非空——每条批注都要声明教学意图；
//   - 内置七类 key：label（trim 后）必须为空——内置含义固定，不得用 label 覆盖；
//   - 扩展 key：必须匹配 ^[a-z][a-z0-9_-]{0,31}$，且 label（trim 后）非空——扩展 key 只是机器
//     标识，渲染时按 label 显示，缺 label 就没有可显示的标签。
//
// 词表 / 正则的判定来自 mdfile 的单一真源（BuiltinAnnotationLabel / ValidExtensionAnnotationKey），
// 与渲染侧 mdfile.ResolveAgentLabel 共用同一套规则——校验认下的组合，渲染必能解析出标签。
func (v *validator) agentAnnotationValid(op *Op, i int, b NoteBlock) bool {
	if b.Annotation == "" {
		v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "annotation"),
			"role: %s 的块必须给出非空 annotation：每条 Agent 批注都要声明教学意图"+
				"（内置七类 %v 之一，或匹配 ^[a-z][a-z0-9_-]{0,31}$ 的扩展 key）（契约 §4.2.2）",
			NoteBlockAgent, mdfile.BuiltinAnnotationKeys()))
		return false
	}
	if _, builtin := mdfile.BuiltinAnnotationLabel(b.Annotation); builtin {
		if strings.TrimSpace(b.Label) != "" {
			v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "label"),
				"内置批注 %q 的显示标签是固定的，不得用 label 覆盖：label 只用于扩展批注"+
					"（契约 §4.2.2：内置 label 不可覆盖固定含义）", b.Annotation))
			return false
		}
		return true
	}
	if !mdfile.ValidExtensionAnnotationKey(b.Annotation) {
		v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "annotation"),
			"annotation %q 既非内置七类 %v、也不匹配扩展 key 形态 ^[a-z][a-z0-9_-]{0,31}$"+
				"（首字符小写字母，其后 0..31 个小写字母 / 数字 / 下划线 / 连字符；契约 §4.2.2）",
			b.Annotation, mdfile.BuiltinAnnotationKeys()))
		return false
	}
	if strings.TrimSpace(b.Label) == "" {
		v.add(errorAt(E2, op.Index, blockPath(op.Index, i, "label"),
			"扩展批注 %q 必须给出 trim 后非空的 label 作为人读标签：扩展 key 只是机器标识，"+
				"渲染时按 label 显示（契约 §4.2.2）", b.Annotation))
		return false
	}
	return true
}
