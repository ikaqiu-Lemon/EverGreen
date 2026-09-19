package plan

// opinion_validation_authorization_test.go —— T-007 批 7B：观点 `validation` 写入的**授权硬化机器证据**。
//
// 本文件只钉一件事：**在 Agent 自动路径（P-A）与 plan / apply 这条载体上，`validation`
// 是一个"写不进去"的字段**——它的流转唯一归 CLI 的用户显式路径（`eg opinion validate|reject`，
// 见 opinion_cmd.go / SetValidation），plan 侧任何写 `validation` 的尝试都被封闭面拦死，
// 且**不能靠 plan 内容自证**（N-1）：哪怕 plan 写了 `initiator: user` 且命令行带 `--user-request`
// （满 P-U 佐证），create_opinion 预盖章与 append_opinion 改判 `validation` 依旧零写入退错。
//
// 三条封闭面（本批补的机器证据，逐条对应 7B 的三项验收）：
//   - create_opinion 仅允许 `validation: pending`（缺键=默认 pending / 显式 pending=冗余 info），
//     `validated` / `rejected` / 枚举外一律 E2、零写入（§4.5 / §6.3）；
//   - append_opinion 一旦带 `validation` 键即 E6、零写入（追加论据与改判结论是两件事，§6.3）；
//   - 写权限矩阵**根本没有** `validation` 这一格（ObjectOpinion 只登记五个分区行），
//     故任何走 matrixGate 的写 `validation` 尝试都命中"查不到即拒绝"；且主链路 op 封闭集
//     里**不存在**任何独立的 validation 写 op（没有 set_validation / validate_opinion 之类），
//     Agent 没有任何可调用的载体去改 `validation`。
//
// 与既有用例的分工（不重复造）：knowledge_opinion_v2_test.go 的
// TestCreateOpinionWritesValidationPending / TestCreateOpinionRejectsPrevalidated 已钉 P-A 侧
// create_opinion 的 pending 与预盖章 E2；本文件补的是**两条路径同拒**（N-1 反自证）、
// append_opinion 的 validation E6、以及矩阵 / op 封闭集这两处**结构性缺格**的机器证据。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// puRun 跑一次**满 P-U 佐证**的校验：命令行 UserRequest=true（op 侧还须带 `initiator: user`
// 才真正判成 P-U，见 PathOf）。用于反自证：证明验证写入的封闭**与授权无关**——即便给足
// 用户显式佐证，plan 载体上的 validation 写入仍被拦死。
func puRun(t *testing.T, files map[string]string, ops string) *Result {
	t.Helper()
	env := vault(t, files)
	env.UserRequest = true
	p, err := Parse([]byte(v2Plan(t, files, ops)))
	if err != nil {
		t.Fatalf("plan 解析失败：%v", err)
	}
	return Validate(p, env)
}

// settledOpinionFiles 造一份"库里已有一条 validated 观点"的场地，供 append_opinion 改判反证用。
func settledOpinionFiles(t *testing.T, id string) map[string]string {
	t.Helper()
	files := v2Files()
	files[store.OpinionRel("ai-infra", id)] =
		opinionFile(id, "缩放注意力不适合超长序列", model.ValidationValidated)
	return files
}

// assertNoWriteExpansion 断言一次失败校验**零展开**（Actions 为空），且在真实实盘上执行后**既有文件
// 字节一字不变**。与 assertZeroWrite 的区别：不带"目标未被创建"判据——append_opinion 的目标观点本
// 就存在，用"未被创建"会误伤，这里只钉"既有字节零改写"（含目标观点保持 validated 前像）。
func assertNoWriteExpansion(t *testing.T, files map[string]string, res *Result) {
	t.Helper()
	if len(res.Actions) != 0 {
		t.Fatalf("失败校验必须零展开，实得 %d 条 action", len(res.Actions))
	}
	dir := t.TempDir()
	writeVault(t, dir, files)
	before := map[string]string{}
	for r := range files {
		before[r] = readVaultFile(t, dir, r)
	}
	Execute(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)})
	for r, want := range before {
		if got := readVaultFile(t, dir, r); got != want {
			t.Fatalf("零写入被破坏：%s 字节发生变化", r)
		}
	}
}

// —— ① append_opinion 带 validation 键 → E6、零写入；P-A / P-U 两条路径同拒（N-1 反自证）——
//
// append_opinion 的目标恒为**已有**观点，其 validation 的真源在盘上、只能由用户显式路径流转。
// 追加论据（论据与推理 / 条件与反例 / 待验证）与"改判验证结论"是两件事：前者放行、后者一律 E6。
// 关键机器证据是**两条路径同拒**——把 initiator=user + --user-request 都给足（满 P-U），
// validation 改判依旧 E6、依旧零写入：验证写入不吃"授权"这一套，plan 内容无从自证（N-1）。
func TestAppendOpinionRejectsValidationWriteBothPaths(t *testing.T) {
	const oid = "o-20260901-longseq"
	// 追加一条合法论据分区（确保能越过"缺 sections"这道更早的门），同时非法地带上 validation。
	op := fmt.Sprintf(`{"op":"append_opinion","opinion":%q,"validation":%q,`+
		`"sections":{"论据与推理":"再补一条论据，但不该借机改判结论。\n"}}`,
		oid, model.ValidationRejected)

	cases := []struct {
		name string
		run  func(t *testing.T, files map[string]string) *Result
	}{
		{"P-A 自动路径", func(t *testing.T, files map[string]string) *Result { return v2Run(t, files, op) }},
		{"P-U 满佐证（反自证）", func(t *testing.T, files map[string]string) *Result {
			// op 侧补 initiator=user，命令行侧 UserRequest=true —— 满 P-U。
			pu := fmt.Sprintf(`{"op":"append_opinion","opinion":%q,"initiator":"user","validation":%q,`+
				`"sections":{"论据与推理":"再补一条论据，但不该借机改判结论。\n"}}`,
				oid, model.ValidationRejected)
			return puRun(t, files, pu)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := settledOpinionFiles(t, oid)
			res := c.run(t, files)
			d := requireError(t, res, E6)
			if !strings.Contains(d.Path, "validation") {
				t.Fatalf("E6 诊断必须指向 validation 字段，实得 %q", d.Path)
			}
			// 零写入：不展开任何 action，且盘上既有目标观点字节一字不变（validated 保持前像）。
			// append 的目标本就存在，故不能用 assertZeroWrite 的「未被创建」判据。
			assertNoWriteExpansion(t, files, res)
		})
	}
}

// —— ② create_opinion 仅允许 pending；validated / rejected / 枚举外一律 E2、零写入；两路径同拒 ——
//
// 创建即盖章 = 绕过用户授权（§4.5）。本用例把"越权取值"扩到**满 P-U 佐证**下同样 E2：
// 证明 create_opinion 的 pending-only 也不吃授权那一套（N-1），且顺带钉死缺键 / 显式 pending 放行。
func TestCreateOpinionValidationClosedToPendingBothPaths(t *testing.T) {
	mkOp := func(withInitiator bool, validationKV string) string {
		init := ""
		if withInitiator {
			init = `"initiator":"user",`
		}
		return fmt.Sprintf(`{"op":"create_opinion","opinion_id":"o-20261017-pin",`+
			`%s%s"title":"一条待验证的主张",`+
			`"sources":[{"source":"s-20260901-attention","note":"n-20260901-attention",`+
			`"rel":"support","reason":"原文第 5 节"}],`+
			`"sections":{"观点":"一句主张。\n"}}`, init, validationKV)
	}
	for _, bad := range []model.Validation{model.ValidationValidated, model.ValidationRejected} {
		kv := fmt.Sprintf(`"validation":%q,`, bad)
		t.Run("P-A/"+string(bad), func(t *testing.T) {
			files := v2Files()
			res := v2Run(t, files, mkOp(false, kv))
			d := requireError(t, res, E2)
			if !strings.Contains(d.Path, "validation") {
				t.Fatalf("E2 诊断必须指向 validation 字段，实得 %q", d.Path)
			}
			assertZeroWrite(t, files, res, store.OpinionRel("ai-infra", "o-20261017-pin"))
		})
		t.Run("P-U 满佐证（反自证）/"+string(bad), func(t *testing.T) {
			files := v2Files()
			res := puRun(t, files, mkOp(true, kv))
			d := requireError(t, res, E2)
			if !strings.Contains(d.Path, "validation") {
				t.Fatalf("E2 诊断必须指向 validation 字段（满 P-U 也不放行），实得 %q", d.Path)
			}
			assertZeroWrite(t, files, res, store.OpinionRel("ai-infra", "o-20261017-pin"))
		})
	}
	// 显式枚举外取值同样 E2（不是"只挡 validated/rejected"，是"只放行 pending"）。
	t.Run("P-A/枚举外", func(t *testing.T) {
		files := v2Files()
		res := v2Run(t, files, mkOp(false, `"validation":"approved",`))
		d := requireError(t, res, E2)
		if !strings.Contains(d.Path, "validation") {
			t.Fatalf("枚举外取值必须 E2 指向 validation，实得 %q", d.Path)
		}
		assertZeroWrite(t, files, res, store.OpinionRel("ai-infra", "o-20261017-pin"))
	})
	// 缺键（默认 pending）与显式 pending（冗余）都放行：证明封闭面只挡"非 pending"，不误伤合法建卡。
	for _, ok := range []struct {
		name string
		kv   string
	}{{"缺键默认pending", ""}, {"显式pending", `"validation":"pending",`}} {
		t.Run("放行/"+ok.name, func(t *testing.T) {
			files := v2Files()
			res := v2Run(t, files, mkOp(false, ok.kv))
			if res.Failed() {
				t.Fatalf("合法 create_opinion（%s）不应失败：errors=%v", ok.name, codes(res.Errors))
			}
		})
	}
}

// —— ③ 结构性缺格：写权限矩阵没有 validation 这一格，matrixGate「查不到即拒绝」——
//
// ObjectOpinion 在矩阵里只登记五个**分区**行（观点 / 论据与推理 / 条件与反例 / 待验证 / 用户补充），
// **没有** `validation` 这一格。因此任何走 matrixGate 的写 `validation` 尝试都命中 LookupRow
// 未命中 → E6「查不到即拒绝」。本用例把"矩阵里根本没有 validation 字段"钉成机器可复算的事实：
// 遍历全 50 行，断言没有任何一行的 Field 是 `validation`；并逐字确认 LookupRow(ObjectOpinion,
// validation) 未命中。这是"Agent 无载体改 validation"在授权矩阵层的结构性根因。
func TestValidationIsNotAnAgentWritableMatrixField(t *testing.T) {
	// (a) 全矩阵无 validation 字段格（任何对象类都不得给 validation 开写权限格）。
	for _, row := range Matrix() {
		if row.Field == model.FMKeyValidation {
			t.Fatalf("写权限矩阵不得存在 validation 字段格（#%d %s）：validation 只能由用户显式路径流转，"+
				"给它开矩阵格等于把 Agent 写 validation 变成「查表放行」", row.Num, row.Object)
		}
	}
	// (b) LookupRow 对 validation 逐字未命中 —— matrixGate 因此走"查不到即拒绝"。
	if row, ok := LookupRow(ObjectOpinion, model.FMKeyValidation); ok {
		t.Fatalf("LookupRow(ObjectOpinion, %q) 竟命中 #%d：validation 必须是矩阵里查不到的字段",
			model.FMKeyValidation, row.Num)
	}
	// (c) ObjectOpinion 的五行必须全是分区行（SectionField(...)），无任何 frontmatter 字段行泄漏写权限。
	opinionRows := 0
	for _, row := range Matrix() {
		if row.Object != ObjectOpinion {
			continue
		}
		opinionRows++
		if !strings.HasPrefix(row.Field, "分区「") {
			t.Fatalf("ObjectOpinion #%d 的字段 %q 不是分区行：观点在矩阵里只应登记分区，"+
				"frontmatter 字段（尤其 validation）不得进矩阵", row.Num, row.Field)
		}
	}
	if opinionRows != 5 {
		t.Fatalf("ObjectOpinion 应恰 5 个分区行（§3.3），实得 %d", opinionRows)
	}
}

// —— ④ op 封闭集：不存在任何 Agent 可调用的独立 validation 写 op ——
//
// 主链路 op 恰九个（OpNames），别名只有 create_card / append_card（OpAliases）。这九个里能触碰
// validation 的只有 create_opinion（pending-only，见 ②），没有任何"set_validation / validate_opinion /
// reopen / reject_opinion"这类独立的验证写 op。本用例把这条封闭性钉成机器证据：遍历 OpNames，
// 断言不含任何验证写 op 名；断言观点写 op 恰 create_opinion + append_opinion 两个；断言别名表
// 不引入验证写口；并顺带确认那几个"未定义语义"的 S2 op 也没有偷偷塞进一个 validation 写 op。
func TestNoAgentCallableValidationChangePlanOp(t *testing.T) {
	// 封闭黑名单：任何以下名字若出现在可派发的 op 集合里，都意味着 Agent 有了独立改 validation 的载体。
	forbidden := map[string]bool{
		"set_validation": true, "validate_opinion": true, "validate": true,
		"reject_opinion": true, "reopen_opinion": true, "reopen": true,
		"opinion_validate": true, "opinion_reject": true, "set_opinion_validation": true,
	}
	names := OpNames()
	if len(names) != 9 {
		t.Fatalf("主链路 op 应恰九个（§4.4），实得 %d：%v", len(names), names)
	}
	opinionWriteOps := 0
	for _, n := range names {
		if forbidden[n] {
			t.Fatalf("主链路 op 集合出现验证写 op %q：Agent 不得有任何独立改 validation 的载体（§6.3）", n)
		}
		if n == OpCreateOpinion || n == OpAppendOpinion {
			opinionWriteOps++
		}
	}
	if opinionWriteOps != 2 {
		t.Fatalf("观点写 op 应恰 create_opinion + append_opinion 两个，实得 %d", opinionWriteOps)
	}
	// 别名表只把 create_card/append_card 归一到 Knowledge 规范名，不得引入任何验证写口。
	for alias, canon := range OpAliases() {
		if forbidden[alias] || forbidden[canon] {
			t.Fatalf("别名 %q→%q 触及验证写 op 黑名单", alias, canon)
		}
	}
	// 连"未定义语义、一律按未知 op 拒绝"的 S2 名单也不得藏一个 validation 写 op
	//（即便被拒，出现在这里也是危险信号）。
	for _, n := range s2OpNames() {
		if forbidden[n] {
			t.Fatalf("S2 未定义 op 名单出现验证写 op %q：不得为验证写预留任何 op 名", n)
		}
	}
}
