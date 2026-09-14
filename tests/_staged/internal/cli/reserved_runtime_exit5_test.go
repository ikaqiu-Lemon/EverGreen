package cli

// reserved_runtime_exit5_test.go —— M6 · T-…-075 批次 C1b-runtime：**运行时写路径**上的三类
// 反证（合同 §9 / §16.2 / §16.3 / §17.1）。
//
// 与既有测试的分工：
//   - index_reserved_read_test.go 钉的是 reserved 类型违规的**只读侧**（C 类降级、绝不退 5）；
//   - recover_barrier_exit5_test.go（C1a）钉的是**崩溃恢复五形态**在 `eg apply` 端到端退 5；
//   - 本文件补齐**写路径侧**尚缺的三条端到端具名反证：
//       ① reserved 类型违规在 **A 类写命令（eg apply）与 B 类 index 维护命令**上一律
//          fail closed 退 5 + E15，且本次请求零权威写入；
//       ② B 类 index 维护命令在 `run.lock` 锁忙时退 5 + E16（锁不可用共用一码），
//          且在有限墙钟内返回、索引与权威零写；
//       ③ S3 在**持锁之后**才重新发现候选并重校验：锁外旧快照一律作废 —— 取锁瞬间落地的
//          并发改动必须被锁内重读看见，据此重算 accepted write-set（本用例里整文件跳过、
//          零 accepted ⇒ 不开事务、不覆盖别人刚落的字节）。
//
// 全部用真实 run.lock / 事务日志 / git 仓 / store 写口，零打桩；语料与取证辅助沿用
// index_lock_test.go、index_reserved_read_test.go、recover_hook_test.go 的那几套，不另造口径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// —— ① reserved 类型违规：A / B 写路径一律退 5 + E15、零权威写 ——

// TestIndexReservedTypeViolationExits5 逐条语料 × 两类写路径对撞
// 「运行时保留条目类型违规 ⇒ 写前 fail closed（E15）退 5」。
//
// 这是 R6 五支反证里唯一落在**写路径**的一支（只读侧由 index_reserved_read_test.go 承接），
// 也是「坏掉的派生物绝不能让权威被写」这条最高约束在写侧的机器形态：
//
//	A 类（eg apply）   ——  run.lock 违规 ⇒ S1 Acquire 的 O_NOFOLLOW / 类型判定 fail closed；
//	                       txn 违规    ⇒ S1 之后 S2 恢复扫描的 parent-symlink / 类型判定 fail closed。
//	B 类（index 维护）——  同样两道屏障，维护那一格一次都不许跑。
//
// 两类都必须：退出码恰 5、errors[] 携 E15、权威 Markdown 逐字节不变。哪一道屏障先拦下
// 属实现细节，不钉；要保的不变量是「屏障没过就绝不写权威」。
func TestIndexReservedTypeViolationExits5(t *testing.T) {
	for _, c := range rsvCases() {
		t.Run(c.name, func(t *testing.T) {
			// —— B 类：三条 index 维护命令 ——
			t.Run("B类-index维护", func(t *testing.T) {
				dir := idxVault(t)
				if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
					t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
				}
				rsvApply(t, dir, c)
				authority := idxAuthoritySnapshot(t, dir)

				for _, sub := range idxMaintenanceSubs() {
					code, out, errOut := runIndexCLI(t, dir, sub)
					if code != ExitPrecheckOrLock {
						t.Fatalf("eg index %s：reserved 类型违规必须 fail closed 退 %d，实得 %d：%s",
							sub, ExitPrecheckOrLock, code, errOut)
					}
					diags := errorDiagsOf(t, idxEnvelope(t, out))
					if !diagsHaveCode(diags, txn.CodePrecheckFailed) {
						t.Fatalf("eg index %s：reserved 类型违规必须携 %s，实得 %+v",
							sub, txn.CodePrecheckFailed, diags)
					}
				}
				idxAssertAuthorityUnchanged(t, dir, authority)
			})

			// —— A 类：eg apply 写命令 ——
			t.Run("A类-eg-apply", func(t *testing.T) {
				dir := idxVault(t)
				// 先建一个健康索引，让 run.lock / txn 都以合法形态在盘，随后再摆违规现场。
				if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
					t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
				}
				rsvApply(t, dir, c)

				authority := idxAuthoritySnapshot(t, dir)
				headBefore := gitOut(t, dir, "rev-parse", "HEAD")
				logBefore := gitLogCount(t, dir)

				code, env, errOut := runApplyPlan(t, dir,
					cardPlan("k-20260901-reserved-viol", ""))
				if code != ExitPrecheckOrLock {
					t.Fatalf("eg apply：reserved 类型违规必须 fail closed 退 %d，实得 %d：%s",
						ExitPrecheckOrLock, code, errOut)
				}
				diags := errorDiagsOf(t, env)
				if !diagsHaveCode(diags, txn.CodePrecheckFailed) {
					t.Fatalf("eg apply：reserved 类型违规必须携 %s，实得 %+v",
						txn.CodePrecheckFailed, diags)
				}
				// 本次请求零权威写入、HEAD 不动、commit 数不变。
				idxAssertAuthorityUnchanged(t, dir, authority)
				if got := gitOut(t, dir, "rev-parse", "HEAD"); got != headBefore {
					t.Fatalf("reserved 类型违规不得移动 HEAD：%q → %q", headBefore, got)
				}
				if got := gitLogCount(t, dir); got != logBefore {
					t.Fatalf("reserved 类型违规不得产生 commit：%d → %d", logBefore, got)
				}
			})
		})
	}
}

// —— ② B 类 index 维护命令锁忙：退 5 + E16、有限墙钟返回、零写 ——

// TestBIndexLockBusyExits5WithE16：另有进程真实握住 flock 时，三条 B 类维护命令必须在
// 极小的等待上限内非零返回，退出码恰 5、诊断携 E16（锁不可用），且连锁正文都不写、
// 权威零改动、零 commit。
//
// 这是「退出码 5 一码两成因」在 E16 侧、B 类触发面上的端到端反证：C1a 已从 E15 侧钉过五形态，
// 本支补 E16 侧的 index 维护路径（A 类写命令的锁忙 E16 由 exit5_test.go 的类型化映射覆盖）。
func TestBIndexLockBusyExits5WithE16(t *testing.T) {
	dir := idxVault(t)
	authority := idxAuthoritySnapshot(t, dir)
	if code, _, errOut := runIndexCLI(t, dir, IndexSubBuild); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	commitsBefore := gitLogCount(t, dir)

	// 极小等待上限：修好后应在百毫秒量级返回；没修好则无论多小都不会返回。
	t.Setenv(txn.LockTimeoutEnv, "80")

	// 外部持锁者：真实握住 flock（OFD 语义，同进程另开一次 open 照样冲突）。
	held, err := txn.Acquire(dir, txn.LockOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("外部持锁失败，用例前提不成立：%v", err)
	}
	defer func() { _ = held.Release() }()

	before := idxRuntimeSnapshot(t, dir)
	for _, sub := range idxMaintenanceSubs() {
		started := time.Now()
		code, out := idxRunAsync(t, dir, sub, 20*time.Second)
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			t.Fatalf("eg index %s：等待上限 80ms，实际耗时 %s —— 锁等待没有走真实墙钟",
				sub, elapsed)
		}
		if code != ExitPrecheckOrLock {
			t.Fatalf("eg index %s：锁忙（E16）必须退 %d，实得 %d：%s",
				sub, ExitPrecheckOrLock, code, out)
		}
		diags := errorDiagsOf(t, idxEnvelope(t, out))
		if !diagsHaveCode(diags, txn.CodeLockTimeout) {
			t.Fatalf("eg index %s：锁忙必须携 %s，实得 %+v", sub, txn.CodeLockTimeout, diags)
		}
		// 连锁都没拿到 ⇒ 连锁正文都不许写（不豁免 run.lock）。
		idxAssertRuntimeUnchanged(t, before, idxRuntimeSnapshot(t, dir), false,
			"eg index "+sub+" 撞上锁忙")
	}

	idxAssertAuthorityUnchanged(t, dir, authority)
	if n := gitLogCount(t, dir); n != commitsBefore {
		t.Fatalf("commit 数 %d → %d：索引维护恒 0 次提交", commitsBefore, n)
	}
}

// —— ③ S3 持锁后重新发现候选：锁外旧快照作废 ——

// TestPrecheckRediscoversCandidatesUnderLock：S3 的读发生在**取锁之后**，锁外的旧快照一律作废。
//
// 造一次「取到锁的瞬间库被别人改掉」：若实现复用了锁外读到的 S0 快照来发现候选 / 复核
// content_hash，B3 校验会拿着过期哈希判定通过，把别人刚落的字节覆盖掉；只有持锁后
// **重新发现候选**（锁内 EnvFor + Validate）才会看见并发改动，据此把整文件按 file_changed
// 剔出 accepted write-set —— 于是零 accepted、不开事务、并发字节原样保留。
//
// 与 TestPlanTxnRereadsInsideLock 的分工：那支从「不覆盖别人字节」这一端立论，本支从
// 「候选集合在持锁后被重算」这一端立论，并显式断言 S3 重读节点晚于取锁节点、且注入的
// 并发改动确实发生在二者之间。
func TestPrecheckRediscoversCandidatesUnderLock(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardAbs := filepath.Join(dir, filepath.FromSlash(cardRel))
	staleHash := hashOf(t, dir, cardRel)
	base := txnIDsOn(t, dir)

	var mutatedAt int // 并发改动落地时已观测到的节点数（用于断言它发生在取锁之后、重读之前）
	var mutated bool
	rec := watchTxn(t, func(step, _ string) {
		if step != TxnStepAcquired || mutated {
			return
		}
		mutated = true
		mutatedAt = 1 // 此刻 names 里恰有 Acquired 这一格（extra 在 append 之后调用）
		raw := mustRead(t, cardAbs)
		if err := os.WriteFile(cardAbs, append(raw, []byte("\n<!-- 并发改动 -->\n")...), 0o644); err != nil {
			t.Fatalf("制造并发改动失败：%v", err)
		}
	})

	// base 里带上一次读到的 staleHash：S0 快照认为文件仍是这个内容。
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"S3 重新发现候选反证",
"base":{"` + applyCardID + `":"` + staleHash + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `","sections":{"解释与依据":"不该被写进去。"}}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitPartialWrite {
		t.Fatalf("退出码 = %d，期望 3（S3 重新发现候选应把已变化的文件整条剔除）：%s", code, errOut)
	}

	// 取锁与重读节点都在，且重读晚于取锁；并发改动确实落在二者之间。
	if rec.at(TxnStepAcquired) < 0 || rec.at(TxnStepReread) < 0 {
		t.Fatalf("必须观测到取锁与 S3 重读两个节点：%v", rec.names)
	}
	if rec.at(TxnStepReread) <= rec.at(TxnStepAcquired) {
		t.Fatalf("S3 重读必须晚于取锁（重新发现候选发生在临界区内）：%v", rec.names)
	}
	if !mutated || mutatedAt != 1 {
		t.Fatalf("并发改动没有在取锁之后、重读之前注入，本用例什么都没证明：mutated=%v at=%d",
			mutated, mutatedAt)
	}

	// 候选被重算：整文件按 file_changed 剔除、零 accepted ⇒ 不开事务。
	rep := applyReport(t, env)
	if len(rep.Skipped) != 1 || rep.Skipped[0].Kind != string(store.SkipFileChanged) {
		t.Fatalf("skipped[] = %+v，期望恰一条 file_changed（证明候选取自锁内重读的现态）", rep.Skipped)
	}
	assertNoNewTxn(t, dir, base, "全部候选被剔除")
	if rep.TxnID != "" {
		t.Fatalf("没开事务却填了 txn_id = %q", rep.TxnID)
	}

	// 并发字节原样保留、过期快照的写入没落盘。
	after := string(mustRead(t, cardAbs))
	if !strings.Contains(after, "并发改动") {
		t.Fatal("并发改动被覆盖了：持锁后重新发现候选的意义就是不覆盖别人刚落的字节")
	}
	if strings.Contains(after, "不该被写进去") {
		t.Fatal("拿 S0 过期快照写了盘：这正是 S3 重新发现候选要防的事")
	}
}
