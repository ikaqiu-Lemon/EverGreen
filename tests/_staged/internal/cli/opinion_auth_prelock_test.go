package cli

// opinion_auth_prelock_test.go —— T-007 批 7B：`eg opinion validate|reject`（含 `validate --reopen`）
// 对**真实存在**的观点缺 `--user-request` 时，授权门（E19）必须**先于取 run.lock** 触发。
//
// 这一条是 7B 授权硬化的机器证据 #1：授权拒绝要在**拿锁之前**完成，权威字节、事务、Git 三处零变化。
// 光断言「缺 --user-request 退 2/E19」还不够——那无法区分「取锁前拒」与「取锁后才拒」。本用例用一个
// **外部持锁 + 长等待上限**的夹具把时序钉死：
//
//   - 先由外部占住 run.lock，并把锁等待上限（EG_LOCK_TIMEOUT_MS）设成一个**很长**的值；
//   - 再在缺 --user-request（但 --reason 齐备，已越过更早的 reason 门）下跑命令；
//   - 若授权门当真先于取锁：命令**根本不碰锁**，near-instant 返回 E19（退 2），耗时远小于等待上限；
//   - 若授权门被错接到取锁之后：命令会在锁上阻塞到上限，最终吐的是 E16（CodeLockTimeout、退 5），
//     而不是 E19——用例即会因「拿到 E16 / 或耗时逼近等待上限」而红。
//
// 于是「拿到 E19 且耗时远小于等待上限、且不含 E16」三者合起来，唯一自洽的解释就是：授权门先于取锁。
// 语料一律走 opinionVault（真观点落盘，ID 恒为 applyOpinionID）；三个子命令逐个考。

import (
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

func TestOpinionLifecycleMissingUserRequestDeniedBeforeLock(t *testing.T) {
	const at = "2026-10-20T09:00:00+08:00"
	type sub struct {
		name   string
		sub    string
		reopen bool
	}
	for _, sc := range []sub{
		{"validate", SubOpinionValidate, false},
		{"reject", SubOpinionReject, false},
		{"validate --reopen", SubOpinionValidate, true},
	} {
		t.Run(sc.name, func(t *testing.T) {
			dir, _, _ := opinionVault(t)
			oid := applyOpinionID

			before := authoritySnapshot(t, dir)
			logBefore := gitLogCount(t, dir)
			base := txnIDsOn(t, dir)

			// 把等待上限设成很长（8s）：若授权门错接到取锁之后，命令会阻塞逼近这个上限；
			// 授权门若先于取锁，则命令根本不等锁，耗时远小于它。
			t.Setenv(txn.LockTimeoutEnv, "8000")
			held, err := txn.Acquire(dir, txn.LockOptions{Timeout: 2 * time.Second})
			if err != nil {
				t.Fatalf("外部持锁失败，用例前提不成立：%v", err)
			}
			defer func() { _ = held.Release() }()

			// 缺 --user-request，但 --reason 齐备（越过更早的 reason 门，直抵授权门）。
			args := []string{sc.sub, oid, "--reason", "缺用户显式佐证：应在取锁前被 E19 拒"}
			if sc.reopen {
				args = append(args, "--"+OpinionReopenFlag)
			}

			type runOut struct {
				code int
				env  Envelope
				out  string
			}
			done := make(chan runOut, 1)
			started := time.Now()
			go func() {
				code, env, out := runOpinionLifecycleWith(t, newTestRoot(t, dir), dir, at, args...)
				done <- runOut{code: code, env: env, out: out}
			}()
			var got runOut
			select {
			case got = <-done:
			case <-time.After(30 * time.Second):
				t.Fatalf("[%s] 缺 --user-request 的命令 30s 内未返回：授权门疑似被错接到取锁之后并卡在锁上", sc.name)
			}
			elapsed := time.Since(started)

			// ① 退 2 / E19：授权门以校验失败拒绝。
			if got.code != ExitValidation {
				t.Fatalf("[%s] 退出码 = %d，期望 2（缺 --user-request 属授权失败）：%s", sc.name, got.code, got.out)
			}
			diags := errorDiagsOf(t, got.env)
			if !diagsHaveCode(diags, E19) {
				t.Fatalf("[%s] 授权失败必须携 E19，实得 %+v", sc.name, diags)
			}
			// ② 绝不是 E16：一旦出现锁超时诊断，说明命令确实去取了锁（授权门在取锁之后，契约违背）。
			if diagsHaveCode(diags, txn.CodeLockTimeout) {
				t.Fatalf("[%s] 出现 %s（锁超时）：授权门被错接到取锁之后，命令去等了锁：%+v",
					sc.name, txn.CodeLockTimeout, diags)
			}
			// ③ 耗时远小于等待上限（8s）：命令根本没有在锁上阻塞——授权门先于取锁的时序证据。
			if elapsed > 4*time.Second {
				t.Fatalf("[%s] 耗时 %s 逼近锁等待上限 8s：授权门疑似在取锁之后才拒（应先于取锁、near-instant 返回）",
					sc.name, elapsed)
			}

			// ④ 权威字节 / 事务 / Git 三处零变化：授权门在取号（S5）乃至取锁之前即出局。
			assertAuthorityUnchanged(t, dir, before, "opinion 缺 --user-request 授权前置拒绝")
			assertNoNewTxn(t, dir, base, "opinion 缺 --user-request 授权前置拒绝")
			if n := gitLogCount(t, dir); n != logBefore {
				t.Fatalf("[%s] 授权前置拒绝不得产生 commit：%d → %d", sc.name, logBefore, n)
			}
			// 连报告都产不出来：授权门在取号前阻断，data.report 整个缺席。
			if _, hasReport := got.env.Data["report"]; hasReport {
				t.Fatalf("[%s] 授权前置拒绝在取号前即阻断，data.report 应整个缺席：%v", sc.name, got.env.Data)
			}
		})
	}
}
