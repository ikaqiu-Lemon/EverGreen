package plan

// T-…-038 授权判据的用例（授权合同 §7.2 逐字点名的测试名 + 写权限矩阵的逐格反证）。
//
// 本文件的取证方式刻意分两层：
//   - **矩阵事实**（TestWritePermissionMatrix）只读 Matrix()，不跑校验链：
//     它锁的是「盘上取值与合同 §2 逐字相等」，任何一格翻转立刻红；
//   - **门闸行为**（其余用例）走真实临时 vault + Validate（+ Execute），
//     断言「拒绝时目标文件一个字节都没动」——不是断言诊断码而已。
//
// 两层缺一不可：只测矩阵会漏掉「表对了但没人查表」，只测行为会漏掉「查表了但表错了」。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 真实临时 vault 的取证工具 ——

// authVault 在磁盘上落一份最小可用库，返回 vault 根与 store。
//
// 用真实文件而不是内存 map，是因为本组用例的核心断言是**字节不变**：
// 内存 Env 根本没有「字节」可言，测不出「拒绝 = 不落盘」。
func authVault(t *testing.T, files map[string]string) (string, *store.Store) {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("写夹具失败：%v", err)
		}
	}
	return root, store.New(root)
}

// snapshot 记下库内每个文件的内容哈希（字节级取证的基线）。
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = store.ContentHash(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("快照失败：%v", err)
	}
	return out
}

// assertUnchanged 断言两份快照逐文件、逐字节相等。
func assertUnchanged(t *testing.T, before, after map[string]string, why string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("%s：文件数从 %d 变成 %d（拒绝的 op 不得增删文件）", why, len(before), len(after))
	}
	for rel, want := range before {
		got, ok := after[rel]
		if !ok {
			t.Fatalf("%s：文件 %s 消失了", why, rel)
		}
		if got != want {
			t.Fatalf("%s：文件 %s 字节被改动（%s → %s）", why, rel, want, got)
		}
	}
}

// authRun 在真实库上跑「校验 →（通过才）执行」的完整链路，与 runPlan 同序。
//
// userRequest 逐字对应命令行上的 `--user-request`：这是 P-U 的唯一佐证来源。
func authRun(t *testing.T, root string, st *store.Store, body string, userRequest bool) *Result {
	t.Helper()
	env, err := EnvFor(st, "ai-infra")
	if err != nil {
		t.Fatalf("EnvFor 失败：%v", err)
	}
	env.UserRequest = userRequest
	p, err := Parse([]byte(body))
	if err != nil {
		t.Fatalf("plan 解析失败：%v", err)
	}
	res := Validate(p, env)
	if res.Failed() {
		return res // 校验失败 = 零写入（与 CLI 的 runPlan 同一条短路）
	}
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	Execute(st, res, ExecOptions{
		Stamp: model.NewStamp(now), Date: model.NewDate(now),
		Base: p.Base, Index: &env.Index,
	})
	return res
}

// —— ① E6：Agent 自动路径改不了核心内容（矩阵 #12 的 🔴 子情形）——

// TestE6_AgentAppendCoreKnowledgeRejected 断言 append_card 写「知识内容」→ 退 2 + 字节不变。
func TestE6_AgentAppendCoreKnowledgeRejected(t *testing.T) {
	files := authFiles()
	root, st := authVault(t, files)
	before := snapshot(t, root)

	res := authRun(t, root, st, authPlan(t, files, fmt.Sprintf(
		`{"op":"append_card","card":%q,"sections":{%q:"Agent 想改核心结论。\n"}}`,
		authCardA, store.SecKnowledge)), false)

	if !res.Failed() {
		t.Fatal("Agent 自动路径写「知识内容」必须判失败（退 2）")
	}
	d, ok := find(res.Errors, E6)
	if !ok {
		t.Fatalf("必须是 E6（沿用既有编号，不新增），实得 %v", codes(res.Errors))
	}
	if len(res.Actions) != 0 {
		t.Fatalf("被拒的 op 不得展开成 action，实得 %d 条", len(res.Actions))
	}
	assertUnchanged(t, before, snapshot(t, root), "append_card 写「知识内容」被拒后")
	t.Logf("E6 message = %s", d.Message)
}

// —— ② E6：「用户补充」两条路径都写不了（矩阵 #15，授权也解不开）——

// TestE6_UserSectionNeverWritten 断言 Agent 路径与用户显式路径**均**退 2。
func TestE6_UserSectionNeverWritten(t *testing.T) {
	for _, c := range []struct {
		name        string
		initiator   string
		userRequest bool
	}{
		{"Agent 自动路径（无 --user-request）", "", false},
		{"用户显式路径（initiator: user + --user-request）", `,"initiator":"user"`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := authFiles()
			root, st := authVault(t, files)
			before := snapshot(t, root)

			res := authRun(t, root, st, authPlan(t, files, fmt.Sprintf(
				`{"op":"append_card","card":%q,"sections":{%q:"我自己写的话。\n"}%s}`,
				authCardA, store.SecUserAppend, c.initiator)), c.userRequest)

			if !res.Failed() {
				t.Fatal("「用户补充」在任何路径都不得写入（B2 / 矩阵 #15 两格均 🔴）")
			}
			if _, ok := find(res.Errors, E6); !ok {
				t.Fatalf("必须是 E6，实得 %v", codes(res.Errors))
			}
			assertUnchanged(t, before, snapshot(t, root), "写「用户补充」被拒后")
		})
	}
}

// —— ③ W7：状态类 op 缺 initiator=user → error、退 2、零写入 ——

// TestW7_StatusOpWithoutUserInitiatorIsError 逐个状态类 op 取证（零 Action）。
func TestW7_StatusOpWithoutUserInitiatorIsError(t *testing.T) {
	ops := map[string]string{
		OpDeprecate:     fmt.Sprintf(`{"op":"deprecate","target":%q,"reason":"已被新卡取代"}`, authCardA),
		OpRestore:       fmt.Sprintf(`{"op":"restore","target":%q,"reason":"重新成立"}`, authCardA),
		OpSetReplacedBy: fmt.Sprintf(`{"op":"set_replaced_by","target":%q,"replaced_by":{"target":%q,"reason":"新版"}}`, authCardA, authCardB),
		OpDelete:        fmt.Sprintf(`{"op":"delete","target":%q,"reason":"重复","proposal":"p-20261017-001"}`, authCardA),
		OpUndelete:      fmt.Sprintf(`{"op":"undelete","target":%q,"reason":"误删"}`, authCardA),
	}
	if len(ops) != len(StateOpNames()) {
		t.Fatalf("状态类 op 恰 %d 个，用例覆盖了 %d 个", len(StateOpNames()), len(ops))
	}
	for _, name := range StateOpNames() {
		body, ok := ops[name]
		if !ok {
			t.Fatalf("状态类 op %s 未被用例覆盖", name)
		}
		t.Run(name, func(t *testing.T) {
			files := authFiles()
			root, st := authVault(t, files)
			before := snapshot(t, root)

			res := authRun(t, root, st, authPlan(t, files, body), false)

			d, ok := find(res.Errors, W7)
			if !ok {
				t.Fatalf("%s 缺 initiator=user 必须判 W7 error，实得 %v", name, codes(res.Errors))
			}
			if d.Level != LevelError {
				t.Fatalf("%s 的 W7 自 M3 起是 error，实得 %q", name, d.Level)
			}
			if len(res.Actions) != 0 {
				t.Fatalf("%s 必须零展开，实得 %d 条 action", name, len(res.Actions))
			}
			assertUnchanged(t, before, snapshot(t, root), name+" 被 W7 拦下后")
		})
	}

	// —— T-…-044 追加覆盖：`remove_relation` 缺 initiator=user 同样退 2 + 字节不变 ——
	//
	// **分工说明（不是把它改判成状态类 op）**：A-15 窄口径下 StateOpNames() 恰五个、不含
	// `remove_relation`，它的 W7 只是 warning；真正拦住 Agent 自动路径的是写权限矩阵
	// #11「删关系」的 P-A 🔴 → **E6**，且 E6 先于 W7 类 warning 生效。
	// 对外可观察结果与状态类 op 逐字一致：退 2（校验失败）+ 目标文件字节不变，
	// 因此这一行放在本用例里取证——它回答的是同一个问题「缺用户显式授权能不能删关系」。
	t.Run(OpRemoveRelation, func(t *testing.T) {
		files := m3Files()
		root, st := authVault(t, files)
		before := snapshot(t, root)

		res := authRun(t, root, st, m3Plan(t, files,
			`{"op":"remove_relation","from":"k-20260901-attention","type":"limits",
 "target":"k-20260815-rnn","reason":"Agent 自己想删"}`), false)

		if !res.Failed() {
			t.Fatal("Agent 自动路径删关系必须判失败（矩阵 #11 的 P-A 🔴 → 退 2）")
		}
		if _, ok := find(res.Errors, E6); !ok {
			t.Fatalf("必须是 E6（沿用既有编号，不新增），实得 %v", codes(res.Errors))
		}
		if IsStateOp(OpRemoveRelation) {
			t.Fatal("remove_relation 不得被塞进 A-15 窄口径的五个状态类 op")
		}
		if len(res.Actions) != 0 {
			t.Fatalf("被拒的 op 不得展开成 action，实得 %d 条", len(res.Actions))
		}
		assertUnchanged(t, before, snapshot(t, root), "Agent 路径删关系被拒后")
	})
}

// —— ④ V9 ≡ W7 前半：Agent 不能批准提案（U-12）——

// TestV9_AgentCannotApproveProposal 断言 Agent 路径改提案状态 → 退 2。
//
// 矩阵 #36「status → approved」的 P-A 格是 🔴，且 §2.9 把它**锁死**；
// 在 M3 的 ChangePlan 里，Agent 能碰到提案状态的唯一入口是 `delete` 引用提案，
// 因此本用例从两侧取证：① 矩阵 #36 / #38 的 P-A 恒为 🔴（锁定条款）；
// ② Agent 路径的 `delete`（提案已 approved）照样退 2，拿不到「已批准」的红利。
func TestV9_AgentCannotApproveProposal(t *testing.T) {
	row36, ok := RowNum(36)
	if !ok {
		t.Fatal("矩阵缺第 36 行")
	}
	if !row36.Auto.Has(VerdictDeny) || row36.Auto.Has(VerdictAllow) {
		t.Fatalf("#36 的 P-A 必须恒为 🔴（U-12 / §2.9 锁定），实得 %s", row36.Auto)
	}
	row38, ok := RowNum(38)
	if !ok {
		t.Fatal("矩阵缺第 38 行")
	}
	if !row38.Auto.Has(VerdictDeny) || row38.Auto.Has(VerdictAllow) {
		t.Fatalf("#38 decision.* 的 P-A 必须恒为 🔴，实得 %s", row38.Auto)
	}

	files := authFiles()
	root, st := authVault(t, files)
	before := snapshot(t, root)
	res := authRun(t, root, st, authPlan(t, files, fmt.Sprintf(
		`{"op":"delete","target":%q,"reason":"重复","proposal":"p-20261017-001","initiator":"user"}`,
		authCardA)), false) // 没有 --user-request：这就是 Agent 自动路径
	if !res.Failed() {
		t.Fatal("Agent 自动路径不得借已批准提案执行删除（U-12 / V9）")
	}
	if _, ok := find(res.Errors, W7); !ok {
		t.Fatalf("V9 ≡ W7 前半，不另起编号，实得 %v", codes(res.Errors))
	}
	assertUnchanged(t, before, snapshot(t, root), "Agent 路径的 delete 被拒后")
}

// —— ⑤ 矩阵门闸：P-A 下 6 行逐行退 2 ——

// matrixGateCases 是 #3 / #4 / #5 / #6 / #7 / #11 六行的最小 plan（矩阵行号 → op）。
func matrixGateCases() []struct {
	Row int
	Op  string
} {
	return []struct {
		Row int
		Op  string
	}{
		{3, fmt.Sprintf(`{"op":"deprecate","target":%q,"reason":"已被新卡取代","initiator":"user"}`, authCardA)},
		{4, fmt.Sprintf(`{"op":"set_replaced_by","target":%q,"initiator":"user",`+
			`"replaced_by":{"target":%q,"reason":"新版综述"}}`, authCardA, authCardB)},
		{5, fmt.Sprintf(`{"op":"delete","target":%q,"reason":"内容重复","initiator":"user",`+
			`"proposal":"p-20261017-001"}`, authCardA)},
		{6, fmt.Sprintf(`{"op":"undelete","target":%q,"reason":"误删","initiator":"user"}`, authCardD)},
		{7, fmt.Sprintf(`{"op":"mark_reviewed","target":%q,"initiator":"user"}`, authCardA)},
		{11, fmt.Sprintf(`{"op":"remove_relation","from":%q,"type":"limits","target":%q,`+
			`"reason":"该限定已不成立","initiator":"user"}`, authCardA, authCardB)},
	}
}

// TestMatrixGate_AgentDeniedRowsRejected 断言六行在 P-A 下逐行退 2 + 零写入。
//
// 六条 plan 都写了 `initiator: user`——正因为**没给命令行佐证**，它们全部落回 P-A
// （N-1 反伪造）：这同时也是「plan 内容不能自证授权」的行为级取证。
func TestMatrixGate_AgentDeniedRowsRejected(t *testing.T) {
	for _, c := range matrixGateCases() {
		row, ok := RowNum(c.Row)
		if !ok {
			t.Fatalf("矩阵缺第 %d 行", c.Row)
		}
		t.Run(fmt.Sprintf("#%d", c.Row), func(t *testing.T) {
			if !row.Auto.Has(VerdictDeny) || row.Auto.Has(VerdictAllow) {
				t.Fatalf("#%d 的 P-A 应为纯 🔴，实得 %s", c.Row, row.Auto)
			}
			files := authFiles()
			root, st := authVault(t, files)
			before := snapshot(t, root)

			res := authRun(t, root, st, authPlan(t, files, c.Op), false)

			if !res.Failed() {
				t.Fatalf("矩阵 #%d 在 P-A 下必须拒绝，实得通过", c.Row)
			}
			if len(res.Actions) != 0 {
				t.Fatalf("矩阵 #%d 被拒后不得展开 action，实得 %d 条", c.Row, len(res.Actions))
			}
			assertUnchanged(t, before, snapshot(t, root), fmt.Sprintf("矩阵 #%d 被拒后", c.Row))
		})
	}
}

// TestMatrixGate_UserPathUnlocks 断言同样六行在 P-U 下**不因授权**报 error。
//
// 「不因授权」是刻意的窄断言：这六个 op 的落盘语义分别归 T-…-039 / 041 / 042，
// 本阶段它们通过校验链后只是如实上报「未落盘」，因此这里不断言写入结果，
// 只断言**没有任何授权类 error**（W7 / E6）挡在前面。
func TestMatrixGate_UserPathUnlocks(t *testing.T) {
	for _, c := range matrixGateCases() {
		t.Run(fmt.Sprintf("#%d", c.Row), func(t *testing.T) {
			files := authFiles()
			root, st := authVault(t, files)

			res := authRun(t, root, st, authPlan(t, files, c.Op), true)

			for _, d := range res.Errors {
				if d.Code == W7 || d.Code == E6 {
					t.Fatalf("矩阵 #%d 在 P-U 下不得因授权报错：%s %s", c.Row, d.Code, d.Message)
				}
			}
		})
	}
}

// —— ⑥ 路径判定与反伪造的直接取证 ——

// TestPathOfAndForgery 锁 PathOf / Forged 的真值表（授权合同 §1，恰两条路径）。
func TestPathOfAndForgery(t *testing.T) {
	cases := []struct {
		initiator   string
		userRequest bool
		want        Path
		forged      bool
	}{
		{InitiatorUser, true, PathUser, false},
		{InitiatorUser, false, PathAgent, true},
		{"agent", true, PathAgent, false},
		{"", false, PathAgent, false},
		{"", true, PathAgent, false},
	}
	for _, c := range cases {
		op := &Op{Initiator: c.initiator, InitiatorGiven: c.initiator != ""}
		auth := Authorization{UserRequest: c.userRequest}
		if got := PathOf(op, auth); got != c.want {
			t.Fatalf("PathOf(initiator=%q, --user-request=%v) = %s，期望 %s",
				c.initiator, c.userRequest, got, c.want)
		}
		if got := Forged(op, auth); got != c.forged {
			t.Fatalf("Forged(initiator=%q, --user-request=%v) = %v，期望 %v",
				c.initiator, c.userRequest, got, c.forged)
		}
	}
	if len(Paths()) != 2 {
		t.Fatalf("写入路径恰两条（合同 §1），实得 %d：%v", len(Paths()), Paths())
	}
}

// —— 夹具 ——

const (
	authCardA = "k-20260901-attention"
	authCardB = "k-20260815-rnn"
	authCardD = "k-20260903-gone"
)

// authFiles 是本组用例的真实库夹具：在 M3 默认库之上再加一张**已逻辑删除**的卡，
// 好让矩阵 #6「清空 deleted_at / deleted_reason」有一个真实的 undelete 目标
// （否则 undelete 会先撞上 W11 幂等，测不到门闸本身）。
func authFiles() map[string]string {
	files := m3Files()
	files["domains/ai-infra/knowledge/"+authCardD+".md"] = cardM3(authCardD, "active",
		"\ndeleted_at: '2026-10-16T09:00:00+08:00'\ndeleted_reason: 与新卡重复")
	return files
}

// authPlan 造一份 base 覆盖全库的 plan（避免 W6 跳过把授权判定掩盖过去）。
func authPlan(t *testing.T, files map[string]string, ops string) string {
	t.Helper()
	var base []string
	for rel, content := range files {
		base = append(base, fmt.Sprintf("%q:%q", rel, store.ContentHash([]byte(content))))
	}
	return fmt.Sprintf(`{"plan_version":1,"verb":"process","domain":"ai-infra",`+
		`"reason":"T-…-038 授权用例","requirement_ids":["EG-EDIT-04"],"base":{%s},"ops":[%s]}`,
		strings.Join(base, ","), ops)
}
