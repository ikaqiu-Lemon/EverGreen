package plan

// 授权判定的**唯一入口**（授权合同 `2026-10-10-m3-user-authorization-contract.md`
// §1 两条路径与反伪造条款 N-1、§2 写权限矩阵、§5 授权与 B1–B4 的关系）。
//
// 本文件回答且只回答两个问题：
//
//	① 这条 op 走的是哪条路径？        → PathOf（M3 完成判据 8 / 9 的唯一判定入口）
//	② 这条路径能不能写这一格？        → (*validator).matrixGate（查 §2 的 43 行矩阵）
//
// **其他包不得另写一套**：路径判定散成两处，N-1 的反伪造条款就会从某一处漏掉。
//
// 与 W7 的分工（必须分清，否则会把两件事混成一件）：
//   - **W7 管「缺 initiator 的分级」**：五个状态类 op 缺 `initiator=user` → error；
//     另三个 → warning（A-15 窄口径 N-6）。它回答「这条 op 的授权表达完整吗」。
//   - **矩阵管「该路径能不能写这一格」**：查 §2 的两列取值，🔴 → E6、整条 op 不执行。
//     它回答「就算表达完整，这条路径也未必有权写这个字段」。
//
// 两者**不矛盾**且都要发声。典型例子：`mark_reviewed`（矩阵 #7）与 `remove_relation`
// （矩阵 #11）缺 `initiator=user` 时 W7 只是 **warning**，但它们的 P-A 格是 🔴，
// 因此照样被 **E6 拦住退 2**；而 `replace_block`（矩阵 #17）两格都是 ✅，
// P-A 下只留 W7 warning、照常写入。
//
// **授权不是豁免**（合同 §5）：本文件只放宽「谁可以发起哪个动作」，
// 对「写入时怎么做」一字不改——B1 只追加、B2 逐字保留用户块、B3 先读盘逐文件比对
// `content_hash`、B4 失败不回滚，全部沿用既有实现，matrixGate 不碰其中任何一条。

import (
	"fmt"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// Authorization 是**命令行侧**的授权佐证。
//
// 只有一个字段，且它的取值来源必须是命令行（`eg apply --user-request`）：
// plan 文件内容**不能**自证（N-1）。把它做成独立类型而不是裸 bool，
// 是为了让「谁在给授权佐证」在调用点一眼可读。
type Authorization struct {
	// UserRequest 为真表示发起进程在命令行上给出了 `--user-request`。
	UserRequest bool
}

// PathOf 判定一条 op 走的是哪条路径（合同 §1，**恰两条**）。
//
//	`initiator: user` **且** 命令行有 `--user-request` → P-U
//	其余一切情形（含缺 initiator、initiator 非 user、以及「只有 initiator: user
//	但没有命令行佐证」的伪造形态）                     → P-A
//
// 这是 M3 完成判据 8 / 9 的唯一判定入口。
func PathOf(op *Op, auth Authorization) Path {
	if op != nil && op.Initiator == InitiatorUser && auth.UserRequest {
		return PathUser
	}
	return PathAgent
}

// Forged 报告是否命中 N-1 的**反伪造**形态：plan 里写了 `initiator: user`，
// 但命令行没有 `--user-request` 佐证。
//
// 合同 §1 把 `initiator=user` 与 `--user-request` 并列为**两个**条件；
// 若允许 plan 内容自证，两个条件退化成一个，§16.1「Agent 自动路径改不了」
// 将失去代码层依据。命中者一律判 P-A（不是报错本身——W7 与矩阵各自照常发声）。
func Forged(op *Op, auth Authorization) bool {
	return op != nil && op.Initiator == InitiatorUser && !auth.UserRequest
}

// ForgeryNotice 是 W7 在反伪造分支上的逐字理由（N-1）。
// 单列成常量：报告文案与用例断言共用同一份字面量，不各写一遍。
const ForgeryNotice = "plan 内的 initiator: user 不构成授权：--user-request 必须由发起进程在命令行给出，" +
	"不能由 plan 文件内容自证（N-1）"

// auth 取本次校验的命令行佐证（只读，来自 Env）。
func (v *validator) auth() Authorization {
	return Authorization{UserRequest: v.env.UserRequest}
}

// pathOf 是 PathOf 在 validator 上的便捷形态。
func (v *validator) pathOf(op *Op) Path { return PathOf(op, v.auth()) }

// matrixGate 查 §2 写权限矩阵，判定「当前路径能不能写这一格」。
//
// 返回 false 表示**整条 op 不执行**：不产出 action，目标文件字节不变。
// 判定只看单元格里的符号（与 §2.8 的计数规则同一套读法）：
//
//	含 ✅            → 允许（#12 这类同时含 🔴 与 ✅ 的「条件解锁」格在此放行，
//	                   子情形的收窄由调用点自己做，见 coreKnowledgeGate）
//	含 🟡（且无 ✅）  → 允许，但登记一条**未编号 warning** 进报告（合同 §2 读法：
//	                   「允许但必须进报告提示」）。**不新增诊断编号。**
//	其余（🔴 / —）    → 登记 **E6**（沿用既有编号）并拒绝
//
// 查不到该格同样拒绝：矩阵是封闭 43 行，查不到说明调用点与合同 §2 脱节，
// 此时放行等于凭空发明一条豁免。
func (v *validator) matrixGate(op *Op, obj Object, field string) bool {
	row, ok := LookupRow(obj, field)
	if !ok {
		v.add(v.gateDiag(op, E6, field, fmt.Sprintf(
			"写权限矩阵查无「%s · %s」这一格：矩阵是封闭的 %d 行（合同 §2），"+
				"调用点与合同脱节时一律拒绝，整条 op 不执行、目标文件字节不变",
			obj, field, len(Matrix()))))
		return false
	}
	path := v.pathOf(op)
	cell := row.CellFor(path)
	switch {
	case cell.Has(VerdictAllow):
		return true
	case cell.Has(VerdictReport):
		d := Diagnostic{Code: Unnumbered, Level: LevelWarning, OpIndex: op.Index,
			Path: opPath(op.Index, field), Target: op.Target,
			Message: fmt.Sprintf("未编号 warning（写权限矩阵 %s 在 %s 路径为 %s）："+
				"允许写入但必须进报告提示；%s", row, path, VerdictReport, row.Note)}
		v.add(d)
		return true
	default:
		v.add(v.gateDiag(op, E6, field, fmt.Sprintf(
			"写权限矩阵 %s 在 %s 路径为 %s：整条 op 不执行，目标文件字节不变；%s",
			row, path, cell, row.Note)))
		return false
	}
}

// gateDiag 拼一条门闸诊断：字段路径落到 `ops[N].<矩阵字段>`，target 带上 op 目标，
// 使报告能逐格定位到「哪一行矩阵拦的、拦的是哪个对象」。
func (v *validator) gateDiag(op *Op, code, field, msg string) Diagnostic {
	d := errorAt(code, op.Index, opPath(op.Index, field), "%s", msg)
	d.Target = op.Target
	return d
}

// coreKnowledgeGate 落地矩阵 #12（条件解锁行之一）在 `append_knowledge` 上的子情形。
//
// #12 的 P-A 格同时含 🔴 与 ✅：🔴 对**已有卡**、✅ 对 `create_knowledge` **新建**。
// `append_knowledge` 的目标恒为已有卡，故 P-A 侧恒取 🔴 子情形 → E6、整条 op 不执行、
// 目标文件字节不变（这是 §16.1 M3 判据「Agent 自动路径改不了核心内容」的唯一落点）。
//
// P-U 侧的 ✅ 由 `eg edit` 承接（A-13），属 T-…-045，本阶段不实现；
// `append_knowledge` **不是** P-U 的载体，因此两条路径下它写「知识内容」都不放行——
// 与 sectionPayloads 的既有 E6 同结论，本函数只是把矩阵行号与两列取值点名进 message。
//
// 诊断里的 op 名一律取 `op.Name`：`create_card` / `append_card` 已在 expand 阶段
// 规范化（契约 §4.4），门闸看到的恒是规范名，别名只在那一步留一条 I1 迁移提示。
func (v *validator) coreKnowledgeGate(op *Op) bool {
	return v.claimSectionGate(op, 12, ObjectCard, store.SecKnowledge,
		"对已有卡是 🔴 子情形（✅ 子情形只属 "+OpCreateKnowledge+" 新建）",
		"P-U 的 ✅ 由 eg edit 承接（A-13，归 T-…-045），"+
			OpAppendKnowledge+" 不是用户显式路径的载体")
}

// opinionClaimGate 落地矩阵 #46（条件解锁行之二）在 `append_opinion` 上的子情形。
//
// 与 coreKnowledgeGate 同一条口径（契约 §3.3「同 `知识内容` 口径」）：观点的主张由
// 创建者一次写定，此后自动路径不得改写。理由不是「保护字节」，而是**改写主张不是补充
// 论据**——把「A 成立」改成「A 不成立」是换了一个观点，它该走 `create_opinion` 加一条
// `opposing` 关系（§6.4），而不是就地改掉那句话，让所有指向它的关系与 `validation`
// 状态悄悄换了对象。
func (v *validator) opinionClaimGate(op *Op) bool {
	return v.claimSectionGate(op, 46, ObjectOpinion, store.SecOpinionClaim,
		"对已有观点是 🔴 子情形（✅ 子情形只属 "+OpCreateOpinion+" 新建）",
		"P-U 的 ✅ 由 eg edit 承接（A-13，归 T-…-045），"+
			OpAppendOpinion+" 不是用户显式路径的载体")
}

// claimSectionGate 是两个条件解锁行（#12 / #46）共用的拒绝路径。
//
// 抽出来不是为了省行数，而是因为两行的判定**完全同构**：目标恒为已有实体，故 P-A 恒取
// 🔴 子情形；P-U 的 ✅ 另有载体。各写一份的唯一后果是某天只改了其中一处。
//
// obj 参与断言而非仅作注释：行号一旦被重排，`row.Object` 会与调用点声明的对象类不符，
// 此时 panic 比「继续拿错行去拼诊断」诚实——错行拼出的 message 会指向另一类实体。
func (v *validator) claimSectionGate(op *Op, rowNum int, obj Object, section, autoSub, userSub string) bool {
	row, ok := RowNum(rowNum)
	if !ok {
		panic(fmt.Sprintf("写权限矩阵缺第 %d 行：条件解锁行不得被删", rowNum))
	}
	if row.Object != obj || row.Field != SectionField(section) {
		panic(fmt.Sprintf("写权限矩阵第 %d 行是「%s · %s」，调用点声明的是「%s · %s」：行号被重排",
			rowNum, row.Object, row.Field, obj, SectionField(section)))
	}
	path := v.pathOf(op)
	sub := autoSub
	if path == PathUser {
		sub = userSub
	}
	v.add(v.gateDiag(op, E6, "sections."+section, fmt.Sprintf(
		"写权限矩阵 %s：%s 在 %s 路径不得写「%s」——%s；整条 op 不执行，目标文件字节不变",
		row, op.Name, path, section, sub)))
	return false
}

// editSectionGate 落地矩阵 #12 在 `edit_section`（`eg edit`，A-13）上的子情形，
// 是「**用户显式命令可改核心内容**」这条 M3 完成判据在代码层的唯一放行点。
//
// 三步，次序不可调：
//
//	① 「用户补充」→ 直接查矩阵 #15（两格同 🔴）→ E6。**与是否 P-U 无关**（U-03：
//	   CLI 任何路径、任何时候都不写它），因此放在授权判定**之前**：它不是授权问题。
//	② 非 P-U → E6。理由是 B1 那一行的原文：「替换与删除只在用户显式发起的命令里存在
//	   （S2 起）」——**渲染器不对自动路径提供替换能力**。#12 的 P-A 格虽同时含 🔴 与 ✅，
//	   但 ✅ 子情形只属 `create_card` 新建；`edit_section` 的目标恒为已有卡，故 P-A 侧
//	   恒取 🔴 子情形。缺命令行 `--user-request` 的伪造形态（N-1）在此被拦下，
//	   plan 内容自证不成立。
//	③ 分区必须在 EditableSections 白名单内，再逐格查矩阵（#12 / #13 / #14）——
//	   查表这一步不省略，任何一格被翻成 🔴 都会立刻在这里生效，而不是靠人记住。
//
// 返回 false 一律表示**整条 op 不执行**：不产出 action，目标文件字节不变。
func (v *validator) editSectionGate(op *Op, section string) bool {
	if section == store.SecUserAppend {
		// 矩阵 #15：两条路径同 🔴，E6 由 matrixGate 统一登记（不新增诊断码）。
		return v.matrixGate(op, ObjectCard, SectionField(store.SecUserAppend))
	}
	row, ok := RowNum(12)
	if !ok {
		panic("写权限矩阵缺第 12 行：合同 §2.1 的条件解锁行不得被删")
	}
	if path := v.pathOf(op); path != PathUser {
		why := fmt.Sprintf("%s 的分区替换只在用户显式路径（%s）成立：%s 侧取 🔴 子情形"+
			"（✅ 子情形只属 create_card 新建）", op.Name, PathUser, path)
		if Forged(op, v.auth()) {
			why = fmt.Sprintf("%s 的授权佐证不成立：%s", op.Name, ForgeryNotice)
		}
		v.add(v.gateDiag(op, E6, "section", fmt.Sprintf(
			"写权限矩阵 %s：%s；替换与删除只在用户显式发起的命令里存在（S2 起，B1 的内含）——"+
				"整条 op 不执行，目标文件字节不变", row, why)))
		return false
	}
	if !editableSection(section) {
		detail := fmt.Sprintf("允许替换的分区恰为 %v", EditableSections())
		if section == SelfCheckSection {
			detail = editSectionSelfCheckNotice
		}
		v.add(v.gateDiag(op, E6, "section", fmt.Sprintf(
			"%s 的 section=%q 不在可替换分区白名单内：%s；整条 op 不执行，目标文件字节不变",
			op.Name, section, detail)))
		return false
	}
	field, ok := cardSectionField(section)
	if !ok {
		v.add(v.gateDiag(op, E6, "section", fmt.Sprintf(
			"%s 的 section=%q 在写权限矩阵里没有对应行：查不到即拒绝（矩阵是封闭 %d 行）",
			op.Name, section, len(Matrix()))))
		return false
	}
	return v.matrixGate(op, ObjectCard, field)
}

// cardSectionField 把知识卡分区名映射到矩阵 Field 列（「理解自检」的追加块是 #16）。
// 返回 false 表示该分区不在矩阵的分区行里（如「用户补充」由 sectionPayloads 直接判 E6）。
//
// 「解释与依据」（#13）与「理解自检」（#16 / #17）已随 D-7 移出 v2 的 Knowledge 模板，
// 但两行**仍留在矩阵里**且本函数**仍映射它们**：存量 v1 卡片里这两个分区照旧存在，
// `replace_block` 对「理解自检」的口径收敛为**仅存量文件适用**（见 replace_block.go），
// 而 matrixGate 的「查不到即拒绝」会把存量文件的合法追加一并拦死。
// 换言之：模板决定「新建时写哪几个分区」，矩阵决定「谁有权写某个分区」——两者不是同一件事。
func cardSectionField(name string) (string, bool) {
	switch name {
	case store.SecKnowledge:
		return SectionField(store.SecKnowledge), true
	case store.SecRationale, store.SecBoundary:
		return SectionField(name), true
	case store.SecSelfCheck:
		return FieldSelfCheckAppend, true
	default:
		return "", false
	}
}

// autoSectionGate 对一条 op 请求写入的知识卡分区逐个查表（#13 / #14 / #16）。
//
// 「知识内容」（#12）由 coreKnowledgeGate 单独处理（条件解锁行有子情形），
// 「用户补充」（#15）由 sectionPayloads 直接判 E6（两条路径同为 🔴，与路径无关）。
// 本函数因此只覆盖剩下三个 ✅ 格：它们今天全放行，明天任何一格被翻成 🔴 都会立刻被拦。
//
// 遍历面取「v2 模板固定分区 ∪ v1 存量分区」：v2 模板收敛到三分区后，若只遍历模板，
// 存量分区名就再也走不到查表这一步——它们会被 sectionPayloads 当未知分区记 I1 忽略，
// 表面结果相同，但「矩阵拦的」与「模板里没有」是两个不同的事实，报告里必须能分辨。
func (v *validator) autoSectionGate(op *Op) bool {
	for _, name := range cardGateSections() {
		if _, wants := op.Sections[name]; !wants {
			continue
		}
		if name == store.SecKnowledge || name == store.SecUserAppend {
			continue
		}
		field, ok := cardSectionField(name)
		if !ok {
			continue
		}
		if !v.matrixGate(op, ObjectCard, field) {
			return false
		}
	}
	return true
}

// cardGateSections 是 autoSectionGate 的遍历面：v2 固定分区后接 v1 存量分区（去重保序）。
func cardGateSections() []string {
	out := append([]string{}, store.KnownSections(store.KindCard)...)
	seen := set(out)
	for _, name := range store.LegacyV1Sections(store.KindCard) {
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// opinionSectionGate 对一条 op 请求写入的观点分区逐个查表（#47 / #48 / #49）。
//
// 与 autoSectionGate 同构，分工也同构：「观点」（#46）由 opinionClaimGate 处理，
// 「用户补充」（#50）由 sectionPayloads 直接判 E6。剩下三格今天全是 ✅——
// 查表这一步不省略，任何一格被翻成 🔴 都会立刻在这里生效，而不是靠人记住。
//
// 为什么不与 autoSectionGate 合成一个「按 kind 查表」的函数：两者的对象类不同、
// 单独处理的分区不同、遍历面不同（观点没有 v1 存量分区，它是 v2 才有的实体）。
// 合成后函数体里会长出三个 `if kind == …`，而那正是本仓反复拒绝的形态。
func (v *validator) opinionSectionGate(op *Op) bool {
	for _, name := range store.KnownSections(store.KindOpinion) {
		if _, wants := op.Sections[name]; !wants {
			continue
		}
		if name == store.SecOpinionClaim || name == store.SecUserAppend {
			continue
		}
		if !v.matrixGate(op, ObjectOpinion, SectionField(name)) {
			return false
		}
	}
	return true
}
