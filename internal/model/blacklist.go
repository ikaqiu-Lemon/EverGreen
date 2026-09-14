package model

// 废弃字段黑名单（「不得复活的已废弃设计」）。
//
// **判定口径：按 YAML / JSON 字段路径精确匹配，禁止关键词扫描。**
// 关键词扫描会把 `relations[].type`、`sources[].rel` 这类合法且必需的用法一并误伤，
// 直接把主链路打死；因此本包只暴露**字段路径列表**，新增废弃字段必须显式登记进列表。

// DeprecatedFieldPaths 是按字段路径判定的废弃字段黑名单。
//
// 覆盖：知识卡 / 材料笔记 frontmatter **顶层**的 domain、type、candidate、source_check，
// 以及任何观点倾向（lean）字段。领域由目录唯一决定（EG-DOM-01），故 domain 不进产物。
var DeprecatedFieldPaths = []string{
	// 知识卡 frontmatter 顶层
	"card.domain",
	"card.type",
	"card.candidate",
	"card.source_check",
	"card.lean",
	"card.stance",
	"card.tendency",
	// 材料笔记 frontmatter 顶层
	"note.domain",
	"note.type",
	"note.candidate",
	"note.source_check",
	"note.lean",
	"note.stance",
	"note.tendency",
	// 原文 frontmatter 顶层
	"source.domain",
	"source.type",
	"source.lean",
}

// AllowedFieldPaths 是**白名单**：与黑名单同名但合法且必需的字段路径（技术方案 §4.1 白名单表）。
// 任何黑名单判定都必须绕开这些路径——它们不是「顶层 domain / type」，语义完全不同。
var AllowedFieldPaths = []string{
	"relations[].type",          // 论证关系类型（冻结合同 F4）
	"sources[].rel",             // 材料关系类型（冻结合同 F4）
	"plan.domain",               // ChangePlan 的本次加工领域
	"--domain",                  // CLI flag
	"unprocessed.target_domain", // 收件区条目的目标领域
	"target_domain",             // 同上（条目内键名形态）
	"proposal.type",             // 提案类型（S2）
}

// IsDeprecatedFieldPath 报告字段路径是否命中黑名单。
// 白名单优先：命中白名单的路径**永不**被判为废弃。
func IsDeprecatedFieldPath(path string) bool {
	for _, w := range AllowedFieldPaths {
		if path == w {
			return false
		}
	}
	for _, b := range DeprecatedFieldPaths {
		if path == b {
			return true
		}
	}
	return false
}

// IsAllowedFieldPath 报告字段路径是否在白名单内。
func IsAllowedFieldPath(path string) bool {
	for _, w := range AllowedFieldPaths {
		if path == w {
			return true
		}
	}
	return false
}
