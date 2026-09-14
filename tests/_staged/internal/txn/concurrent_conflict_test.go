package txn

// concurrent_conflict_test.go —— T-…-073 判据 10：并发冲突四项聚焦反证（合同 §8 / A-54 / A-58，需 -race）。
//
// 本文件是**测试**，不改 internal/txn 的锁 / 日志 / 提交实现（那三者归 071 / 072）。
// 它只把 071 的 run.lock 互斥与 W28 / E16 留痕、072 的原子提交（tmp+rename，无撕裂）当作既有
// 原语来驱动，验证 T-073 的两条并发行为线：
//   1. 后到写者行为封闭：要么等待并留痕 W28、要么携 E16（锁不可用）且零写入；同一时刻只有一个持锁者。
//   2. 同一文件上不出现两个 txn_id 交叉写；并发读采样任一时刻只见「完整前像」或「完整目标态」，绝无半写。
//
// R7（合同 §17）：本 task **不**启用 CLI 退出码 5、**不**断言真实进程退 5，也**不**断言恰 1；
// 退出码 5 的端到端语义由 074 的 ExitCodeFor 单点映射、075 收口。这里只在库内断言
// 「携 E16 的 LockTimeoutError（errors.Is(err, ErrLockUnavailable) 成立）+ W28 留痕 + 权威零写入」。

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// —— 判据 10-1：锁等待期如实留痕 W28 —— 持锁期间的后到者超时失败，其诊断里 ≥1 条 W28 + 恰 1 条 E16。
func TestW28EmittedOnLockWait(t *testing.T) {
	vault := newVault(t)

	l1, err := Acquire(vault, LockOptions{Timeout: time.Second, Argv: []string{"eg", "holder"}})
	if err != nil {
		t.Fatalf("首个 Acquire 应成功，得 %v", err)
	}

	// 后到者：持锁期间用较小超时取锁 —— 必然退避重试若干次（每次留痕 W28）后超时，返回携 E16 的 LockTimeoutError。
	_, err2 := Acquire(vault, LockOptions{Timeout: 80 * time.Millisecond, Argv: []string{"eg", "latecomer"}})
	if err2 == nil {
		t.Fatalf("持锁期间的后到者 Acquire 必须失败，却成功了（互斥被破坏）")
	}
	if !errors.Is(err2, ErrLockUnavailable) {
		t.Fatalf("后到者超时应可被 errors.Is(ErrLockUnavailable) 穿透，得 %v", err2)
	}
	var te *LockTimeoutError
	if !errors.As(err2, &te) {
		t.Fatalf("期望 *LockTimeoutError，得 %T：%v", err2, err2)
	}

	var w28, e16 int
	for _, d := range te.Diagnostics() {
		switch d.Code {
		case CodeLockWaitRetry:
			w28++
		case CodeLockTimeout:
			e16++
		}
	}
	if w28 < 1 {
		t.Fatalf("等待期应至少留痕 1 条 W28，得 %d（diags=%+v）", w28, te.Diagnostics())
	}
	if e16 != 1 {
		t.Fatalf("收尾应恰有 1 条 E16，得 %d", e16)
	}

	// 权威零写入：超时路径绝不删锁、不改锁正文，锁文件仍在且 l1 仍可正常释放。
	if _, statErr := os.Lstat(LockPath(vault)); statErr != nil {
		t.Fatalf("超时不得删除锁文件：%v", statErr)
	}
	if err := l1.Release(); err != nil {
		t.Fatalf("持锁者释放应成功，得 %v", err)
	}
}

// —— 判据 10-2：一个赢、其余等待（W28）或退出（E16），且任一时刻至多一个持锁者。
// R7：这里的「Exits5」指库内携 E16 的锁不可用错误，**不**是真实进程退 5。
func TestConcurrentWritersOneWinsOtherWaitsOrExits5(t *testing.T) {
	const workers = 6

	vault := newVault(t)

	var (
		start         = make(chan struct{})
		wg            sync.WaitGroup
		concurrent    int32 // 当前持锁者数，恒 ≤ 1
		maxConcurrent int32
		sawWait       int32 // 后到者等待成功（W28）
		sawTimeout    int32 // 后到者超时（E16）
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start // 同时起跑，制造真实竞争

			l, err := Acquire(vault, LockOptions{
				Timeout: 90 * time.Millisecond,
				Argv:    []string{"eg", "writer", strconv.Itoa(id)},
			})
			if err != nil {
				if errors.Is(err, ErrLockUnavailable) {
					atomic.AddInt32(&sawTimeout, 1)
					return // 后到者退出：零写入，行为封闭
				}
				t.Errorf("worker %d：非预期取锁错误 %v", id, err)
				return
			}

			// 赢家或等到锁的后到者：若曾等待，其取锁诊断里带 W28。
			for _, d := range l.Diagnostics() {
				if d.Code == CodeLockWaitRetry {
					atomic.AddInt32(&sawWait, 1)
					break
				}
			}

			cur := atomic.AddInt32(&concurrent, 1)
			if cur > 1 {
				t.Errorf("互斥被破坏：同一时刻 %d 个持锁者", cur)
			}
			for {
				m := atomic.LoadInt32(&maxConcurrent)
				if cur <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, cur) {
					break
				}
			}
			time.Sleep(35 * time.Millisecond) // 持锁一小段，逼后到者进入等待 / 超时
			atomic.AddInt32(&concurrent, -1)

			if err := l.Release(); err != nil {
				t.Errorf("worker %d：释放失败 %v", id, err)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if maxConcurrent > 1 {
		t.Fatalf("任一时刻至多一个持锁者，实测峰值 %d", maxConcurrent)
	}
	if sawWait == 0 && sawTimeout == 0 {
		t.Fatalf("%d 个写者同时起跑却未观测到任何竞争（既无 W28 等待也无 E16 超时）", workers)
	}
}

// —— 判据 10-3：同一文件上不出现两个 txn_id 交叉写 —— 两个写者各自在临界区内 alloc→intent→commit，
// 由 run.lock 串行化：txn_id 两枚不同且各自闭合（commit 标记在盘），并发读采样只见合法态。
func TestNoInterleavedTxnIDOnSameFile(t *testing.T) {
	vault := newVault(t)

	const rel = "note.md"
	pre := []byte("PRE\n")
	setAuth(t, vault, rel, pre)

	targets := [][]byte{[]byte("TARGET-A\n"), []byte("TARGET-B\n")}
	valid := map[string]bool{
		string(pre):        true,
		string(targets[0]): true,
		string(targets[1]): true,
	}

	// 并发读采样：任一时刻权威文件内容 ∈ {前像, 目标A, 目标B}，绝无半写 / 外来值。
	stop := make(chan struct{})
	var samplerBad atomic.Value
	var samplerWG sync.WaitGroup
	samplerWG.Add(1)
	go func() {
		defer samplerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(filepath.Join(vault, rel))
			if err != nil {
				continue // rename 原子替换，读到的永远是完整旧 / 新文件；偶发 ENOENT 忽略
			}
			if !valid[string(b)] {
				samplerBad.Store(fmt.Sprintf("采样到非法可见态：%q", b))
				return
			}
		}
	}()

	ids := make([]string, len(targets))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			err := WithLock(vault, LockOptions{Timeout: 5 * time.Second, Argv: []string{"eg", "edit", rel}}, func(_ *Lock) error {
				id, e := AllocateTxnID(vault)
				if e != nil {
					return e
				}
				cur, e := os.ReadFile(filepath.Join(vault, rel))
				if e != nil {
					return e
				}
				if _, e := WriteIntent(vault, id, IntentInput{
					Argv:  []string{"eg", "edit", rel},
					Files: []FileSpec{{Path: rel, PreBytes: cur, TargetBytes: targets[i], TargetOp: "replace"}},
				}); e != nil {
					return e
				}
				if _, e := Commit(vault, id, CommitInput{Files: []CommitFile{{Path: rel, TargetBytes: targets[i]}}}); e != nil {
					return e
				}
				ids[i] = id // 每个下标只由本 goroutine 写，无数据竞争
				return nil
			})
			if err != nil {
				t.Errorf("写者 %d 的临界区失败：%v", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(stop)
	samplerWG.Wait()

	if v := samplerBad.Load(); v != nil {
		t.Fatalf("%v", v)
	}

	// 两枚 txn_id 都非空、互不相同、序号不同 —— 串行分配，绝无交叉。
	if ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("期望两枚不同且非空的 txn_id，得 %v", ids)
	}
	s0, ok0 := ParseTxnID(ids[0])
	s1, ok1 := ParseTxnID(ids[1])
	if !ok0 || !ok1 || s0 == s1 {
		t.Fatalf("两枚 txn_id 的序号应可解析且不同，得 %q(%d,%v) / %q(%d,%v)", ids[0], s0, ok0, ids[1], s1, ok1)
	}

	// 两笔事务各自闭合：各自的 commit 标记在盘（没有一笔把另一笔的目录写脏）。
	for _, id := range ids {
		if !markerPresent(t, vault, id, CommitMarker) {
			t.Fatalf("事务 %s 应有 commit 标记（各自闭合、无交叉）", id)
		}
	}

	// 终态是「后提交者」的目标态之一，且是完整字节。
	got := readAuth(t, vault, rel)
	if string(got) != string(targets[0]) && string(got) != string(targets[1]) {
		t.Fatalf("终态应为某个目标态，得 %q", got)
	}
}

// —— 判据 10-4：并发读下永不出现半写文件 —— Commit 进行时持续读采样，任一样本恒为完整前像或完整目标态。
func TestConcurrentNoHalfWrittenFile(t *testing.T) {
	vault := newVault(t)

	const rel = "big.md"
	// 前像与目标态长度悬殊，一旦发生逐字节覆盖式半写，采样会立刻读到「非前像亦非目标」的中间态。
	pre := bytes.Repeat([]byte("P"), 4096)
	target := bytes.Repeat([]byte("T"), 9000)
	valid := map[string]bool{string(pre): true, string(target): true}

	files := []wsFile{{path: rel, pre: pre, target: target}}
	id := makeOpenTxnWithSet(t, vault, files)

	stop := make(chan struct{})
	var bad atomic.Value
	var samplerWG sync.WaitGroup
	samplerWG.Add(1)
	go func() {
		defer samplerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(filepath.Join(vault, rel))
			if err != nil {
				continue
			}
			if !valid[string(b)] {
				head := b
				if len(head) > 16 {
					head = head[:16]
				}
				bad.Store(fmt.Sprintf("采样到半写文件：len=%d head=%q", len(b), head))
				return
			}
		}
	}()

	time.Sleep(2 * time.Millisecond) // 让采样器先热身
	if _, err := Commit(vault, id, commitInputFor(files)); err != nil {
		close(stop)
		samplerWG.Wait()
		t.Fatalf("提交失败：%v", err)
	}
	close(stop)
	samplerWG.Wait()

	if v := bad.Load(); v != nil {
		t.Fatalf("%v", v)
	}
	if got := readAuth(t, vault, rel); string(got) != string(target) {
		t.Fatalf("终态应为完整目标态（len=%d），得 len=%d", len(target), len(got))
	}
}
