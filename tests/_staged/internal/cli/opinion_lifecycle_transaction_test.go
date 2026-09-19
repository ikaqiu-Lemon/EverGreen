package cli

// opinion_lifecycle_transaction_test.go —— T-007 批次 A3：`eg opinion validate|reject`
// （含 `validate --reopen`）从 NotWired 骨架接通**真实**观点验证生命周期状态机与 A 类事务写链。
//
// 被测对象是 opinion_cmd.go 的命令侧编排：把「授权齐备即 NotWired」的骨架，换成与
// mark-reviewed / undelete **同一把锁、同一套时序**的直写事务（S1 取锁 → S2 恢复 →
// S3 锁内重读 → S4 预演 → S5 intent → S6 提交 → S7 Git → S8 写后索引同步 → S9 释放锁）。
//
// 本文件钉这几件在**磁盘与产物上可观察**的事实（一条判定逻辑都不重测 model 状态机自身，
// 那归 internal/model；这里只考命令层有没有如实把它接进事务）：
//
//   - 五条合法边逐条经真实 CLI 成功：pending→validated / pending→rejected /
//     validated→rejected / validated→pending / rejected→pending，终态 validation
//     与权威字节一致，report 交付 txn_id / link / commit，审计块交代 from→to→action；
//   - 四格非法边（三自环 + rejected→validated）逐格退 2、零权威写、零 commit、无新事务；
//   - 成功一次恰一个事务、intent 只含目标一个文件、恰一次 Git commit，且 commit 的 name-only
//     只含目标文件；
//   - action 由 model.ValidationTransition(from,to) 复算，写进审计块（CLI 不自报）：reject 在
//     validated 上算出 reject、在 pending 上也算 reject —— 命令名相同、action 由权威 from 决定；
//   - S2 崩溃恢复早于 S3 任何业务读；S6 提交期 I/O 失败是退 3 的主动放弃（零权威写）；
//     S7 Git 失败退 4、保留目标态、不二次写、仍走完 S8；锁被占住有限阻断退 E16、零权威写。
//
// 观测手段沿用 recover_hook_test.go 的 txnOrderHook 与那一组磁盘取证辅助，不另造第二套口径；
// 语料一律走 opinionVault（真 plan 经 eg apply 落盘），目标观点 ID 恒为 applyOpinionID。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— 辅助 ——

// runOpinionLifecycleWith 用调用方给定的 Root 跑一次 `eg opinion validate|reject … --json`
// （用于注入 NewRepo 等接缝并钉死业务时钟），解析 --json 信封。
func runOpinionLifecycleWith(t *testing.T, r *Root, dir, at string, args ...string) (int, Envelope, string) {
	t.Helper()
	setGitIdentity(t)
	r.Now = func() time.Time { return stampAt(t, at) }
	r.In = closedStdin{}
	full := append([]string{"opinion"}, args...)
	full = append(full, "--vault", dir, "--json")
	code, out, errOut := runCLI(t, r, full...)
	var env Envelope
	if strings.TrimSpace(out) != "" {
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("--json 输出不可解析：%v\n%s", err, out)
		}
	}
	return code, env, out + errOut
}

// opnReportLinks 取信封里 `data.report.links[]`（复用 undReportOf 的 data.report 口径）。
func opnReportLinks(t *testing.T, env Envelope) []string {
	t.Helper()
	raw, ok := undReportOf(t, env)["links"].([]interface{})
	if !ok {
		return nil
	}
	var out []string
	for _, one := range raw {
		if s, _ := one.(string); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// commitNameOnly 取 HEAD 这一笔 commit 改动的文件清单（--name-only，斜杠分隔的 vault 内相对路径）。
func commitNameOnly(t *testing.T, dir string) []string {
	t.Helper()
	raw := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	return strings.Fields(strings.TrimSpace(raw))
}

// commitSubject 取 HEAD 这一笔 commit 的**主题行**逐字原值（%s），即 `<verb>(<domain>): <subject>`
// 的整行。直接问 Git 权威，不看 report / intent 里的转述——用于钉死成功路径 S7 的 verb 位。
func commitSubject(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=format:%s"))
}

// opinionValidationOf 回读目标观点文件的权威 validation（经 store.OpinionOf 解析，不看索引）。
func opinionValidationOf(t *testing.T, dir, rel string) model.Validation {
	t.Helper()
	op, err := store.OpinionOf(mustRead(t, absIn(dir, rel)))
	if err != nil {
		t.Fatalf("解析观点 %s 失败：%v", rel, err)
	}
	return op.Validation
}

// setOpinionValidationVia 借真实 CLI 把观点推到指定前置态（用于把语料从默认 pending 迁到
// validated / rejected，再在其上考下一条边）。每一步都必须成功，否则语料前提不成立。
func setOpinionValidationVia(t *testing.T, dir, at, sub, oid string, reopen bool) {
	t.Helper()
	args := []string{sub, oid, "--reason", "前置迁移：把语料推到待考前置态", "--user-request"}
	if reopen {
		args = append(args, "--"+OpinionReopenFlag)
	}
	code, _, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, at, args...)
	if code != ExitOK {
		t.Fatalf("前置迁移 opinion %s %s 退出码 = %d：%s", sub, oid, code, out)
	}
}

// —— ① 五条合法边逐条经真实 CLI 成功；终态与权威一致、report 交付事实、审计块记 from→to→action ——

func TestOpinionLifecycleFiveLegalEdges(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	oid := applyOpinionID
	type edge struct {
		name    string
		prep    []model.Validation // 到达 from 的前置态（从默认 pending 出发的中间跳）
		sub     string
		reopen  bool
		wantTo  model.Validation
		wantAct model.ValidationAction
	}
	for _, e := range []edge{
		{"pending→validated", nil, SubOpinionValidate, false, model.ValidationValidated, model.ValidationValidate},
		{"pending→rejected", nil, SubOpinionReject, false, model.ValidationRejected, model.ValidationReject},
		{"validated→rejected", []model.Validation{model.ValidationValidated}, SubOpinionReject, false, model.ValidationRejected, model.ValidationReject},
		{"validated→pending", []model.Validation{model.ValidationValidated}, SubOpinionValidate, true, model.ValidationPending, model.ValidationReopen},
		{"rejected→pending", []model.Validation{model.ValidationRejected}, SubOpinionValidate, true, model.ValidationPending, model.ValidationReopen},
	} {
		t.Run(e.name, func(t *testing.T) {
			dir, _, opinionRel := opinionVault(t)

			// 前置：把语料从默认 pending 推到 e 的 from 态。
			for _, v := range e.prep {
				switch v {
				case model.ValidationValidated:
					setOpinionValidationVia(t, dir, at, SubOpinionValidate, oid, false)
				case model.ValidationRejected:
					setOpinionValidationVia(t, dir, at, SubOpinionReject, oid, false)
				}
			}
			from := opinionValidationOf(t, dir, opinionRel)

			// 前提自证：期望 action 确由状态机从 (from,wantTo) 复算得到（否则用例判据本身失真）。
			gotAct, terr := model.ValidationTransition(from, e.wantTo)
			if terr != nil || gotAct != e.wantAct {
				t.Fatalf("[%s] 前提失效：ValidationTransition(%s,%s) = (%q,%v)，期望 %q",
					e.name, from, e.wantTo, gotAct, terr, e.wantAct)
			}

			logBefore := gitLogCount(t, dir)
			base := txnIDsOn(t, dir)

			args := []string{e.sub, oid, "--reason", "本会话判定理由", "--user-request"}
			if e.reopen {
				args = append(args, "--"+OpinionReopenFlag)
			}
			code, env, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, at, args...)
			if code != ExitOK {
				t.Fatalf("[%s] 退出码 = %d，期望 0（合法边应成功写入）：%s", e.name, code, out)
			}

			// ① 终态权威 validation = 期望目标态。
			if got := opinionValidationOf(t, dir, opinionRel); got != e.wantTo {
				t.Fatalf("[%s] 终态 validation = %q，期望 %q", e.name, got, e.wantTo)
			}
			// ② 恰一个新事务，intent 只含目标一个文件，report.txn_id 与之对上。
			txnID := onlyNewTxn(t, dir, base, "一次合法 opinion lifecycle")
			if got := intentPathsOf(t, dir, txnID); strings.Join(got, ",") != opinionRel {
				t.Fatalf("[%s] intent.files[] = %v，期望恰含 %s", e.name, got, opinionRel)
			}
			if got := undReportTxnID(t, env); got != txnID {
				t.Fatalf("[%s] report.txn_id = %q，新增事务目录 = %q：必须对上", e.name, got, txnID)
			}
			// ③ 恰一次 Git commit；report.links 含目标；report.git.commit 非空。
			if n := gitLogCount(t, dir); n != logBefore+1 {
				t.Fatalf("[%s] commit 数 %d → %d，期望恰 +1", e.name, logBefore, n)
			}
			if !contains(opnReportLinks(t, env), opinionRel) {
				t.Fatalf("[%s] report.links 未含目标 %s：%v", e.name, opinionRel, opnReportLinks(t, env))
			}
			if g, _ := undReportOf(t, env)["git"].(map[string]interface{}); g == nil || g["commit"] == nil || g["commit"] == "" {
				t.Fatalf("[%s] 成功路径 report.git.commit 必须非空：%v", e.name, undReportOf(t, env)["git"])
			}
			// ④ commit 的 name-only 只含目标文件（写面不外溢）。
			if got := commitNameOnly(t, dir); strings.Join(got, ",") != opinionRel {
				t.Fatalf("[%s] commit name-only = %v，期望恰 %s", e.name, got, opinionRel)
			}
			// ⑤ action 由 (from,to) 复算并写进审计块（CLI 不自报）：审计块逐字交代 action 与 transition。
			afterRaw := string(mustRead(t, absIn(dir, opinionRel)))
			if !strings.Contains(afterRaw, "- action: "+string(e.wantAct)) {
				t.Fatalf("[%s] 审计块未记 action=%s：\n%s", e.name, e.wantAct, afterRaw)
			}
			if !strings.Contains(afterRaw, "- transition: "+string(from)+" -> "+string(e.wantTo)) {
				t.Fatalf("[%s] 审计块未记 transition=%s -> %s：\n%s", e.name, from, e.wantTo, afterRaw)
			}
		})
	}
}

// —— ② 四格非法边逐格退 2、零权威写、零 commit、无新事务 ——

func TestOpinionLifecycleIllegalEdgesRejectedWithZeroWrite(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	oid := applyOpinionID
	type illegal struct {
		name   string
		prep   model.Validation
		sub    string
		reopen bool
	}
	for _, tc := range []illegal{
		// validated 自环：在 validated 上 validate（非 reopen）→ to=validated → 自环非法。
		{"validated→validated 自环", model.ValidationValidated, SubOpinionValidate, false},
		// rejected 自环：在 rejected 上 reject → to=rejected → 自环非法。
		{"rejected→rejected 自环", model.ValidationRejected, SubOpinionReject, false},
		// rejected→validated 逆跳：在 rejected 上 validate（非 reopen）→ to=validated → 逆跳非法。
		{"rejected→validated 逆跳", model.ValidationRejected, SubOpinionValidate, false},
		// pending 自环：在 pending 上 validate --reopen → to=pending → 自环非法。
		{"pending→pending 自环", model.ValidationPending, SubOpinionValidate, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, _ := opinionVault(t)
			switch tc.prep {
			case model.ValidationValidated:
				setOpinionValidationVia(t, dir, at, SubOpinionValidate, oid, false)
			case model.ValidationRejected:
				setOpinionValidationVia(t, dir, at, SubOpinionReject, oid, false)
			}

			before := authoritySnapshot(t, dir)
			logBefore := gitLogCount(t, dir)
			base := txnIDsOn(t, dir)

			args := []string{tc.sub, oid, "--reason", "试图走非法边", "--user-request"}
			if tc.reopen {
				args = append(args, "--"+OpinionReopenFlag)
			}
			code, env, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, at, args...)
			if code != ExitValidation {
				t.Fatalf("[%s] 退出码 = %d，期望 2（非法迁移属校验失败、零写入）：%s", tc.name, code, out)
			}
			// 措辞点名「非法 … 迁移」，绝不冒名成 NotWired 或授权失败。
			if !strings.Contains(out, "非法") {
				t.Fatalf("[%s] 非法迁移须逐字点名（含「非法」）：%s", tc.name, out)
			}
			if strings.Contains(out, "尚未挂载") {
				t.Fatalf("[%s] 状态机已挂载，绝不再报 NotWired：%s", tc.name, out)
			}
			// 零权威写、零 commit、无新事务；非法边在取号（S5）之前即被拒，报告无从谈起。
			assertAuthorityUnchanged(t, dir, before, "opinion 非法迁移")
			assertNoNewTxn(t, dir, base, "opinion 非法迁移")
			if n := gitLogCount(t, dir); n != logBefore {
				t.Fatalf("[%s] 非法迁移不得产生 commit：%d → %d", tc.name, logBefore, n)
			}
			// 连报告都产不出来：事实经 data.errors[] 交付，data.report 必须整个缺席
			// （这比「report.txn_id 为空」更强——根本没有产物，也就无从谈号）。
			if _, hasReport := env.Data["report"]; hasReport {
				t.Fatalf("[%s] 非法迁移在取号前即阻断，data.report 应整个缺席：%v", tc.name, env.Data)
			}
		})
	}
}

// —— ③ 成功 diff 只多 validation / updated_at / 一个审计块；多行 reason 保真 ——

func TestOpinionLifecycleDiffScopeAndMultilineReason(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	dir, _, opinionRel := opinionVault(t)
	oid := applyOpinionID

	beforeRaw := string(mustRead(t, absIn(dir, opinionRel)))
	statusBefore := fmKeyLineOf(t, beforeRaw, "status")
	updatedBefore := fmKeyLineOf(t, beforeRaw, "updated_at")
	if statusBefore == "" || updatedBefore == "" {
		t.Fatal("前置不成立：观点缺 status / updated_at 行")
	}

	const reason = "第一行：证据链已复核\n\n第三行：与 k- 卡结论一致"
	code, env, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, at,
		SubOpinionValidate, oid, "--reason", reason, "--user-request")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, out)
	}

	afterRaw := string(mustRead(t, absIn(dir, opinionRel)))
	// ① validation 覆盖为 validated（单行）。
	if got := opinionValidationOf(t, dir, opinionRel); got != model.ValidationValidated {
		t.Fatalf("validation = %q，期望 validated", got)
	}
	if n := strings.Count(afterRaw, "\n"+model.FMKeyValidation+":"); n != 1 {
		t.Fatalf("%s 出现 %d 行，期望恰 1 行（单键覆盖）", model.FMKeyValidation, n)
	}
	// ② updated_at 被刷新到本次时刻；status 逐字不变。
	if got := fmKeyLineOf(t, afterRaw, "updated_at"); got == updatedBefore {
		t.Fatalf("updated_at 未刷新：仍为 %q（validation 落盘要求同步刷新 updated_at）", got)
	}
	if got := fmKeyLineOf(t, afterRaw, "status"); got != statusBefore {
		t.Fatalf("status 行 = %q，期望逐字仍是 %q（validation 与 status 正交）", got, statusBefore)
	}
	// ③ 恰追加一个审计块，多行 reason 逐行 blockquote 保真（含中间空行）。
	if n := strings.Count(afterRaw, "### validation audit"); n != 1 {
		t.Fatalf("审计块出现 %d 次，期望恰 1 次", n)
	}
	if !strings.Contains(afterRaw, "> 第一行：证据链已复核") ||
		!strings.Contains(afterRaw, "> 第三行：与 k- 卡结论一致") {
		t.Fatalf("多行 reason 未逐行 blockquote 保真：\n%s", afterRaw)
	}
	if !strings.Contains(afterRaw, "\n>\n") {
		t.Fatalf("reason 中间空行未以裸 `>` 保真：\n%s", afterRaw)
	}
	// ④ report 交付 txn_id。
	if undReportTxnID(t, env) == "" {
		t.Fatal("成功路径 report.txn_id 不得为空")
	}
}

// —— ④ S2 崩溃恢复早于 S3 目标重读（观点直写事务同 mark-reviewed 口径）——

func TestOpinionLifecycleRecoverPrecedesReread(t *testing.T) {
	const crashAt = "2026-10-19T09:00:00+08:00"
	const runAt = "2026-10-21T09:00:00+08:00"

	dir, _, opinionRel := opinionVault(t)
	oid := applyOpinionID
	abs := absIn(dir, opinionRel)
	pre := mustRead(t, abs) // 前像：validation=pending，无审计块

	// 借真实命令生成「崩溃事务的目标态」字节（validation 已被改成 validated）。
	if code, _, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, crashAt,
		SubOpinionValidate, oid, "--reason", "崩溃事务写下的目标态", "--user-request"); code != ExitOK {
		t.Fatalf("前置写目标态退出码 = %d：%s", code, out)
	}
	target := mustRead(t, abs)
	if !strings.Contains(string(target), "validated") {
		t.Fatalf("夹具失效：目标态没有 validated：\n%s", target)
	}
	// 回滚工作区到前像，再挂一笔**未闭合**事务（intent 已发布、盘上是目标态、无 commit/abort）。
	if err := os.WriteFile(abs, pre, 0o644); err != nil {
		t.Fatalf("复位前像失败：%v", err)
	}
	stale, err := txn.AllocateTxnID(dir)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	now := func() time.Time { return stampAt(t, crashAt) }
	if _, err := txn.WriteIntent(dir, stale, txn.IntentInput{
		Argv: []string{"eg", "opinion", "validate"}, ExpectCommit: true, Now: now,
		Files: []txn.FileSpec{{
			Path: opinionRel, PreBytes: pre, TargetBytes: target, TargetOp: "update",
		}},
	}); err != nil {
		t.Fatalf("发布 intent 失败：%v", err)
	}
	if err := os.WriteFile(abs, target, 0o644); err != nil {
		t.Fatalf("写崩溃态失败：%v", err)
	}

	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atAcquire, atRecovered string
	watchTxn(t, func(step, _ string) {
		switch step {
		case TxnStepAcquired:
			atAcquire = string(mustRead(t, abs))
		case TxnStepRecovered:
			atRecovered = string(mustRead(t, abs))
		}
	})

	// 本次在**恢复后的前像（pending）**上做 reject → 合法边 pending→rejected。
	code, env, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, runAt,
		SubOpinionReject, oid, "--reason", "恢复后在前像上重新判定", "--user-request")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（恢复后照常写入）：%s", code, out)
	}
	if !strings.Contains(atAcquire, "validated") {
		t.Fatal("取锁瞬间盘上不是崩溃态：夹具没生效")
	}
	if atRecovered != string(pre) {
		t.Fatal("恢复回调时目标未回到前像：S2 没有真正回滚，或 S3 读早于 S2")
	}
	// 终态是「在恢复后的 pending 上」跑 reject 的结果，而非崩溃事务的 validated。
	if got := opinionValidationOf(t, dir, opinionRel); got != model.ValidationRejected {
		t.Fatalf("终态 validation = %q，期望 rejected（在恢复后的前像上判定）", got)
	}
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1", logBefore, n)
	}
	if !contains(undReportWarnCodes(t, env), txn.CodeTxnRecovered) {
		t.Fatalf("真实回滚必须在最终 report.warnings[] 留一条 %s：%v",
			txn.CodeTxnRecovered, undReportWarnCodes(t, env))
	}
	if !markerExists(dir, stale, txn.AbortMarker) {
		t.Fatal("被回滚的未闭合事务必须写下 abort 标记")
	}
	id2 := onlyNewTxn(t, dir, base, "恢复之后的一次真实 opinion reject")
	if id2 == stale {
		t.Fatalf("本次不得复用被回滚的事务号 %q", stale)
	}
}

// —— ⑤ S6 提交期 I/O 失败 ⇒ 主动放弃（退 3、零权威写）——

func TestOpinionLifecycleCommitRollbackIsPartialWrite(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	dir, _, opinionRel := opinionVault(t)
	oid := applyOpinionID
	before := authoritySnapshot(t, dir)
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var blocked string
	rec := watchTxn(t, func(step, txnID string) {
		if step != TxnStepIntent {
			return
		}
		blocked = filepath.Join(txn.TxnDirPath(dir, txnID), txn.CommitMarker+".tmp")
		if err := os.MkdirAll(filepath.Join(blocked, "occupied"), 0o755); err != nil {
			t.Fatalf("制造标记发布失败失败：%v", err)
		}
	})

	code, env, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, at,
		SubOpinionValidate, oid, "--reason", "提交期将被阻断", "--user-request")
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（提交失败已整体回滚，属主动放弃）：%s", code, out)
	}
	if blocked == "" {
		t.Fatal("没有触达 intent 节点：本用例什么都没证明")
	}
	_ = os.RemoveAll(blocked)

	assertAuthorityUnchanged(t, dir, before, "opinion 提交失败并回滚后")
	if got := opinionValidationOf(t, dir, opinionRel); got != model.ValidationPending {
		t.Fatalf("回滚后 validation 应仍是前像 pending，实得 %q", got)
	}
	id2 := onlyNewTxn(t, dir, base, "提交失败并回滚")
	if !markerExists(dir, id2, txn.AbortMarker) {
		t.Fatal("回滚完成后必须写下 abort 标记")
	}
	if markerExists(dir, id2, txn.CommitMarker) {
		t.Fatal("没提交成功却有 commit 标记")
	}
	if rec.at(TxnStepGit) >= 0 || rec.at(TxnStepIndexSync) >= 0 {
		t.Fatalf("提交回滚后不得跑 Git / S8，实际序列 %v", rec.names)
	}
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("提交失败不得产生 commit：%d → %d", logBefore, n)
	}
	if got := undReportTxnID(t, env); got != id2 {
		t.Fatalf("report.txn_id = %q，abort 目录 = %q：主动回滚的事务号必须交付且相等", got, id2)
	}
}

// —— ⑥ S7 Git 失败 ⇒ 退 4、保留目标态、不二次写、仍走完 S8 ——

func TestOpinionLifecycleGitFailureKeepsTargetStateAndRunsS8(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	dir, _, opinionRel := opinionVault(t)
	oid := applyOpinionID
	base := txnIDsOn(t, dir)
	logBefore := gitLogCount(t, dir)

	var atGit map[string]string
	rec := watchTxn(t, func(step, _ string) {
		if step == TxnStepGit {
			atGit = authoritySnapshot(t, dir)
		}
	})

	r := newTestRoot(t, dir)
	r.NewRepo = failingCommitRepo()
	code, env, out := runOpinionLifecycleWith(t, r, dir, at,
		SubOpinionReject, oid, "--reason", "Git 将失败但权威已定盘", "--user-request")
	if code != ExitCommitFailed {
		t.Fatalf("退出码 = %d，期望 4：%s", code, out)
	}
	if atGit == nil {
		t.Fatal("没有触达 Git 节点：TxnStepGit 在失败支未触发")
	}

	// ① 权威已生效并保持目标态：validation=rejected（不回滚）。
	if got := opinionValidationOf(t, dir, opinionRel); got != model.ValidationRejected {
		t.Fatalf("Git 失败不得回滚权威，validation = %q，期望 rejected", got)
	}
	// ② Git 之后无第二次权威写。
	assertAuthorityUnchanged(t, dir, atGit, "opinion 的 Git 失败后")
	// ③ 失败支仍走完 S8，且顺序 committed < git < index_sync < releasing。
	assertStepOrder(t, rec, TxnStepCommitted, TxnStepGit, TxnStepIndexSync, TxnStepReleasing)
	// ④ 恰一个事务、commit 标记在盘、无 abort；无新 Git commit、report.git.commit 空。
	txnID := onlyNewTxn(t, dir, base, "Git 失败")
	if got := undReportTxnID(t, env); got != txnID {
		t.Fatalf("report.txn_id = %q 与新增事务目录 %q 对不上", got, txnID)
	}
	if !markerExists(dir, txnID, txn.CommitMarker) {
		t.Fatal("权威事务已提交，commit 标记必须在盘")
	}
	if markerExists(dir, txnID, txn.AbortMarker) {
		t.Fatal("Git 失败不是权威事务失败，不该有 abort 标记")
	}
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("Git 失败不得产生 commit：%d → %d", logBefore, n)
	}
	if g, _ := undReportOf(t, env)["git"].(map[string]interface{}); g == nil || g["commit"] != nil {
		t.Fatalf("Git 失败时 report.git.commit 必须为空：%v", undReportOf(t, env)["git"])
	}
}

// —— ⑦ 锁被占住：有限阻断退 E16、零权威写 ——

func TestOpinionLifecycleLockBusyReturnsE16WithoutHanging(t *testing.T) {
	dir, _, _ := opinionVault(t)
	oid := applyOpinionID
	before := authoritySnapshot(t, dir)
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	t.Setenv(txn.LockTimeoutEnv, "80")
	held, err := txn.Acquire(dir, txn.LockOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("外部持锁失败，用例前提不成立：%v", err)
	}
	defer func() { _ = held.Release() }()

	type runOut struct {
		code int
		env  Envelope
		out  string
	}
	done := make(chan runOut, 1)
	started := time.Now()
	go func() {
		code, env, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir,
			"2026-10-20T09:00:00+08:00", SubOpinionValidate, oid, "--reason", "撞锁", "--user-request")
		done <- runOut{code: code, env: env, out: out}
	}()
	var got runOut
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("锁被占用时 opinion validate 在 20s 内没有返回（等待上限 %s=80ms）", txn.LockTimeoutEnv)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("等待上限 80ms，实际耗时 %s：锁等待没走真实墙钟", elapsed)
	}
	if got.code == ExitOK {
		t.Fatalf("锁被占用必须阻断，实得退出码 0：%s", got.out)
	}
	diags := errorDiagsOf(t, got.env)
	if !diagsHaveCode(diags, txn.CodeLockTimeout) {
		t.Fatalf("锁等待超时必须携 %s，实得 %+v", txn.CodeLockTimeout, diags)
	}
	assertAuthorityUnchanged(t, dir, before, "opinion 撞上锁")
	assertNoNewTxn(t, dir, base, "opinion 撞上锁")
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("没拿到锁不得产生 commit：%d → %d", logBefore, n)
	}
}

// —— ⑧ S4 单文件原子域硬约束：accepted write-set 越出「恰 1 且恰目标 rel」即当场阻断 ——
//
// 直接注入 r.StateWrite，在真实写口把目标观点 stage 进 overlay 之后**再越界 stage 第二个无关
// 文件**（一张知识卡），把 accepted write-set 撑成 2 条。合同要求 S4 必须显式核验单文件原子域
// （len(ws)==1 且 ws[0].Path==目标 rel），任何偏离都 overlay 丢弃、零 txn / 零 Git / 零权威写、
// 返回阻断错误。本用例由**注入反证**锁死这一格：证明守卫是主动检查写集本身，而非事后靠 intent
// 恰含一个文件间接推断——若守卫缺席，被撑大的写集会一路走到取号 / 提交 / Git，用例即会看到新事务
// 与新 commit。
func TestOpinionLifecycleRejectsMultiFileWriteSet(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	dir, cardRel, opinionRel := opinionVault(t)
	oid := applyOpinionID

	before := authoritySnapshot(t, dir)
	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)

	r := newTestRoot(t, dir)
	var staged2 bool
	r.StateWrite = func(st *store.Store, spec store.StateWriteSpec) (store.Result, error) {
		// ① 目标观点走真实写口，正常 stage 进 overlay（accepted write-set 此刻恰 1）。
		res, err := st.ApplyStateWrite(spec)
		if err != nil {
			return res, err
		}
		// ② 越界：把一张与本次流转无关的知识卡也 stage 进**同一个** overlay（reviewed_at 单键），
		//    accepted write-set 因此变成 2 条 —— 恰是单文件原子域守卫必须当场拒绝的越界形态。
		cf, rerr := st.Read(cardRel)
		if rerr != nil {
			t.Fatalf("读卡 %s 失败：%v", cardRel, rerr)
		}
		if _, rerr := st.ApplyStateWrite(store.StateWriteSpec{
			Op: store.StateWriteReviewedAt, Rel: cardRel, ExpectedHash: cf.Hash,
			At: model.NewStamp(stampAt(t, at).Add(time.Second)),
		}); rerr != nil {
			t.Fatalf("越界 stage 第二文件失败：%v", rerr)
		}
		staged2 = true
		return res, nil
	}

	code, env, out := runOpinionLifecycleWith(t, r, dir, at,
		SubOpinionValidate, oid, "--reason", "写集将被撑成两条", "--user-request")

	if !staged2 {
		t.Fatal("注入未生效：没有触达 r.StateWrite，本用例什么都没证明")
	}
	// 越界写集必须当场阻断：绝不成功、逐字点名「单文件原子域」（区别于锁 / 非法边 / NotWired）。
	if code == ExitOK {
		t.Fatalf("写集越出单文件原子域必须阻断，实得退出码 0：%s", out)
	}
	if !strings.Contains(out, "单文件原子域") {
		t.Fatalf("阻断须逐字点名「单文件原子域」（证明确由该守卫触发）：%s", out)
	}
	// 零权威写：目标观点仍是前像 pending，越界的知识卡也一字未改。
	assertAuthorityUnchanged(t, dir, before, "opinion 写集越界后")
	if got := opinionValidationOf(t, dir, opinionRel); got != model.ValidationPending {
		t.Fatalf("阻断后目标 validation 应仍是前像 pending，实得 %q", got)
	}
	// 零事务、零 commit；守卫在 S5 取号之前拦下，连报告都产不出来。
	assertNoNewTxn(t, dir, base, "opinion 写集越界")
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("写集越界不得产生 commit：%d → %d", logBefore, n)
	}
	// data.report 必须整个缺席（比「report.txn_id 为空」更强——根本没有产物）：事实经 data.errors[]
	// 的阻断诊断交付。
	if _, hasReport := env.Data["report"]; hasReport {
		t.Fatalf("写集越界在取号前即阻断，data.report 应整个缺席：%v", env.Data)
	}
}

// —— ⑨ 命令层 B3：S3 锁内重读之后、S4 落盘之前目标被并发改写 ——
//
// 合同要求：opinion 生命周期的写口带 B3 hash 守卫（ExpectedHash 取自 S3 锁内重读的前像）。
// 若目标文件在 S3 读取之后、真实 ApplyStateWrite 之前被**另一写者**改了字节，写口必须以
// *SkipError{file_changed} 拒写；命令层据此走「预演期写失败 ⇒ overlay 丢弃」出口：退 3
// （ExitPartialWrite）、携 E21（既有 B3 对应诊断），且**保留并发写的新字节**（命令层零覆盖、
// 零还原他人写），零权威流转、零事务、零 Git。
//
// 注入手法：把并发写夹进 r.StateWrite 里——真实写口触达目标 rel 的**首个** overlay 访问之前，
// 先往盘上追加并发新字节，再委托真实 st.ApplyStateWrite。由于 overlay 首次 preimage 读的是实盘
// 当前字节（并发后的），其 hash 必然对不上 S3 抓的 ExpectedHash，B3 当场触发。这比事后断言
// 「没写成」更强：它证明守卫基准确实钉在 S3 前像、且并发字节被原样保留。
func TestOpinionLifecycleConcurrentTargetChangeIsPartialWriteKeepingBytesWithoutTxnOrGit(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	dir, _, opinionRel := opinionVault(t)
	oid := applyOpinionID

	logBefore := gitLogCount(t, dir)
	base := txnIDsOn(t, dir)
	opinionAbs := absIn(dir, opinionRel)

	// 并发新字节：模拟 S3 与 S4 之间另一写者对目标观点的追加写。
	concurrentTail := "\n<!-- concurrent writer appended between S3 read and S4 write -->\n"
	var wantConcurrent string

	r := newTestRoot(t, dir)
	var injected bool
	r.StateWrite = func(st *store.Store, spec store.StateWriteSpec) (store.Result, error) {
		if !injected && spec.Rel == opinionRel {
			// 真实写口的首个 overlay 访问之前抢先改盘：这一刻正是 S3 读完、S4 尚未落盘。
			cur := mustRead(t, opinionAbs)
			wantConcurrent = string(cur) + concurrentTail
			if err := os.WriteFile(opinionAbs, []byte(wantConcurrent), 0o644); err != nil {
				t.Fatalf("注入并发写失败：%v", err)
			}
			injected = true
		}
		return st.ApplyStateWrite(spec)
	}

	code, env, out := runOpinionLifecycleWith(t, r, dir, at,
		SubOpinionValidate, oid, "--reason", "S3 与 S4 之间目标被并发改写", "--user-request")

	if !injected {
		t.Fatal("注入未生效：没有触达 r.StateWrite（本用例什么都没证明）")
	}
	// ① 退 3（ExitPartialWrite）：并发改写触发 B3，命令层走预演期写失败出口、零生效。
	if code != ExitPartialWrite {
		t.Fatalf("S3→S4 间目标被并发改写必须退 %d，实得 %d：%s", ExitPartialWrite, code, out)
	}
	// 携 E21（既有 B3 对应写失败诊断）。
	diags := errorDiagsOf(t, env)
	if !diagsHaveCode(diags, E21) {
		t.Fatalf("并发写反证必须携 %s（写失败类诊断），实得 %+v", E21, diags)
	}
	// ② 并发新字节必须原样保留：命令层不得覆盖 / 还原他人的并发写（B3 的语义就是「不动盘、交人工」）。
	if got := string(mustRead(t, opinionAbs)); got != wantConcurrent {
		t.Fatalf("并发新字节必须原样保留（命令层零覆盖 / 零还原）\n期望：%q\n实得：%q", wantConcurrent, got)
	}
	// 目标 validation 一个权威字节都没生效：并发内容里绝不能出现本次要写的 validated 键值。
	if strings.Contains(string(mustRead(t, opinionAbs)), model.FMKeyValidation+": validated") {
		t.Fatalf("并发冲突后不得落地 validated：%s", opinionRel)
	}
	// ③ 零事务、零 Git：预演期即失败，取号（S5）之前就出局。
	assertNoNewTxn(t, dir, base, "opinion S3→S4 并发写冲突")
	if n := gitLogCount(t, dir); n != logBefore {
		t.Fatalf("并发写冲突不得产生 commit：%d → %d", logBefore, n)
	}
	// data.report 整个缺席（比「report.txn_id 为空」更强）：事实经 data.errors[] 的 E21 交付。
	if _, hasReport := env.Data["report"]; hasReport {
		t.Fatalf("并发写冲突在取号前即阻断，data.report 应整个缺席：%v", env.Data)
	}
}

// —— ⑩ 成功路径：HEAD 主题必须由 S7 的 verb=process 落成 `process(` 前缀 ——
//
// A3 的 S7 以 model.VerbProcess 提交，主题行格式恒为 `<verb>(<domain>): <subject>`。这里直接问
// Git 权威（git log -1 %s）而非 report/intent 里的转述，明确断言 HEAD 主题以 `process(` 开头——
// 把「状态类命令同口径用 process 动词」这条契约钉在真实 commit 主题上，杜绝日后改回其它动词而
// report 仍自报成功的漂移。
func TestOpinionLifecycleSuccessHeadSubjectStartsWithProcessVerb(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	dir, _, opinionRel := opinionVault(t)
	oid := applyOpinionID

	logBefore := gitLogCount(t, dir)
	r := newTestRoot(t, dir)
	code, _, out := runOpinionLifecycleWith(t, r, dir, at,
		SubOpinionValidate, oid, "--reason", "成功路径应由 process 动词落 commit", "--user-request")
	if code != ExitOK {
		t.Fatalf("合法边应成功，退出码 = %d：%s", code, out)
	}
	// 恰 +1 commit：确保读到的 HEAD 就是本次流转这一笔。
	if n := gitLogCount(t, dir); n != logBefore+1 {
		t.Fatalf("成功路径应恰 +1 commit：%d → %d", logBefore, n)
	}
	// 核心断言：HEAD 主题逐字以 `process(` 开头（S7 verb=process）。
	subj := commitSubject(t, dir)
	if !strings.HasPrefix(subj, "process(") {
		t.Fatalf("成功路径 HEAD 主题必须以 `process(` 开头（S7 verb=process），实得：%q", subj)
	}
	// 与 name-only 同口径钉住写面：这笔 process commit 恰改目标观点一个文件。
	if got := commitNameOnly(t, dir); strings.Join(got, ",") != opinionRel {
		t.Fatalf("成功路径 commit name-only 应恰含目标 %s，实得 %v", opinionRel, got)
	}
}
