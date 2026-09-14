package plan

// atomic_finalizer_test.go — T-072 批次 B2a：`ExecuteAtomicFinal` 的**预演收尾钩子**聚焦测试。
//
// 钩子存在的唯一理由是：一次 apply 里除了 `ops[]` 展开出的权威写，还有一类「与本次写入
// 同生共死」的收尾回写（CLI 侧的提案 `execution` 回写）。它必须落进**同一个** overlay，
// 从而进同一份 accepted write-set、同一份 intent、同一次原子提交。若它漏到预演之后，
// 那次回写就会直落实盘、既不在 intent 里也不受崩溃恢复保护 —— 即事务外偷改权威 Markdown。
//
// 本文件因此逐条钉住：钩子的写入进 write-set；钩子期间实盘仍零变化；钩子恰被调用一次；
// 预演失败时钩子**不**被调用；钩子自己造成的失败同样导致整事务放弃（write-set 不导出）。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// finalizerProposal 是钩子写入的目标：一份最小提案文件（走 store 的真实提案写口）。
const finalizerProposalRel = "proposals/p-20261020-777.md"

const finalizerProposalBody = `---
id: p-20261020-777
title: 收尾钩子写入的提案
status: draft
created_at: "2026-10-20"
targets: []
---

# 收尾钩子写入的提案
`

// TestExecuteAtomicFinalizerJoinsSameWriteSet：钩子的权威写与 ops[] 的权威写在**同一份**
// accepted write-set 里，且钩子执行期间实盘依然一个字节没动。
func TestExecuteAtomicFinalizerJoinsSameWriteSet(t *testing.T) {
	files, ids := atomicCards(1)
	res := m3Run(t, files, `{"op":"append_card","card":"`+ids[0]+`",
"sections":{"解释与依据":"钩子同事务反证。"}}`)
	if res.Failed() {
		t.Fatalf("plan 不应 error：%v", codes(res.Errors))
	}

	dir := t.TempDir()
	writeVault(t, dir, files)
	s := store.New(dir)

	calls := 0
	ar, err := ExecuteAtomicFinal(s, res, ExecOptions{Stamp: mustStamp(t)},
		func(fs *store.Store, ex *ExecResult) {
			calls++
			if !fs.InAtomic() {
				t.Fatal("钩子必须在 overlay 仍然开着的时候被调用，否则它的写会直落实盘")
			}
			if _, werr := fs.ApplyProposalCreate(finalizerProposalRel,
				[]byte(finalizerProposalBody)); werr != nil {
				t.Fatalf("钩子内的权威写失败：%v", werr)
			}
		})
	if err != nil {
		t.Fatalf("ExecuteAtomicFinal: %v", err)
	}
	if calls != 1 {
		t.Fatalf("钩子被调用 %d 次，期望恰 1 次", calls)
	}
	if !ar.Complete {
		t.Fatalf("无失败时必须 Complete：%+v", ar.Exec.Failures)
	}

	// ① 钩子写入的路径在 accepted write-set 里，且被标成新建。
	var got *store.AtomicFileSpec
	for i := range ar.WriteSet {
		if ar.WriteSet[i].Path == finalizerProposalRel {
			got = &ar.WriteSet[i]
		}
	}
	if got == nil {
		t.Fatalf("钩子写入没进 accepted write-set：%+v", ar.WriteSet)
	}
	if !got.IsNew || got.PreBytes != nil {
		t.Fatalf("新建文件的前像必须为空：IsNew=%v preBytes=%q", got.IsNew, got.PreBytes)
	}
	if string(got.TargetBytes) != finalizerProposalBody {
		t.Fatalf("write-set 里的目标字节被改写了：\n%q", got.TargetBytes)
	}

	// ② ops[] 的那一笔仍在（钩子不顶替、不覆盖既有条目）。
	if len(ar.WriteSet) != 2 {
		t.Fatalf("write-set 应恰 2 项（ops[] 一项 + 钩子一项），实得 %d：%+v",
			len(ar.WriteSet), ar.WriteSet)
	}

	// ③ 预演零实盘写：钩子的文件此刻**不在盘上**。
	if _, serr := os.Stat(filepath.Join(dir, filepath.FromSlash(finalizerProposalRel))); !os.IsNotExist(serr) {
		t.Fatalf("钩子的写入落到了实盘：预演必须零实盘写（stat err=%v）", serr)
	}
	for rel, content := range files {
		if readVaultFile(t, dir, rel) != content {
			t.Fatalf("预演期间实盘被改动：%s", rel)
		}
	}
}

// TestExecuteAtomicFinalizerSkippedOnFailure：预演本体已有普通写失败时，钩子**不被调用**。
//
// 理由：失败即整事务放弃，收尾回写无意义；更要紧的是不能让钩子在一个注定要放弃的事务里
// 再制造新的写意图（那会让「零权威写」这条判据变得依赖钩子的实现细节）。
func TestExecuteAtomicFinalizerSkippedOnFailure(t *testing.T) {
	files, ids := atomicCards(1)
	dir := t.TempDir()
	writeVault(t, dir, files)
	okRel := "domains/ai-infra/knowledge/" + ids[0] + ".md"
	okHash := store.ContentHash([]byte(files[okRel]))

	res := &Result{Verb: "process", Domain: "ai-infra", Actions: []Action{
		{Kind: ActCardAppend, OpIndex: 0, ID: ids[0], Path: okRel, ExpectedHash: okHash,
			Sections: []SectionWrite{{Section: "解释与依据", Payload: []byte("- 会 stage\n")}}},
		{Kind: ActCardAppend, OpIndex: 1, ID: "k-20260901-missing",
			Path:     "domains/ai-infra/knowledge/k-20260901-missing.md",
			Sections: []SectionWrite{{Section: "解释与依据", Payload: []byte("- 读失败\n")}}},
	}}
	called := false
	ar, err := ExecuteAtomicFinal(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)},
		func(*store.Store, *ExecResult) { called = true })
	if err != nil {
		t.Fatalf("ExecuteAtomicFinal: %v", err)
	}
	if called {
		t.Fatal("预演已失败仍调用了收尾钩子：失败即整事务放弃，收尾不该发生")
	}
	if ar.Complete || ar.WriteSet != nil {
		t.Fatalf("失败时 Complete 必须为 false 且不导出 write-set：complete=%v ws=%+v",
			ar.Complete, ar.WriteSet)
	}
}

// TestExecuteAtomicFinalizerFailureAbortsWholeTxn：钩子自己造成普通写失败时，
// 整事务同样放弃（Complete=false、write-set 不导出）—— 钩子不是特权路径。
func TestExecuteAtomicFinalizerFailureAbortsWholeTxn(t *testing.T) {
	files, ids := atomicCards(1)
	res := m3Run(t, files, `{"op":"append_card","card":"`+ids[0]+`",
"sections":{"解释与依据":"钩子失败反证。"}}`)
	if res.Failed() {
		t.Fatalf("plan 不应 error：%v", codes(res.Errors))
	}
	dir := t.TempDir()
	writeVault(t, dir, files)

	ar, err := ExecuteAtomicFinal(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)},
		func(fs *store.Store, ex *ExecResult) {
			// 读一个不存在的卡去追加 —— 与 ops[] 的普通写失败同一条记账路径。
			_, werr := fs.ApplyCardAppend(store.CardAppendSpec{
				Rel:      "domains/ai-infra/knowledge/k-20260901-nowhere.md",
				Sections: []store.SectionAppend{{Section: "解释与依据", Payload: []byte("- x\n")}},
			})
			if werr == nil {
				t.Fatal("前置不成立：对不存在的卡追加本应失败")
			}
			ex.Failures = append(ex.Failures, Diagnostic{
				Code: Unnumbered, Level: LevelWarning, OpIndex: 0,
				Path: "ops", Message: "钩子写入失败：" + werr.Error(),
			})
		})
	if err != nil {
		t.Fatalf("ExecuteAtomicFinal: %v", err)
	}
	if ar.Complete {
		t.Fatal("钩子造成失败后 Complete 必须为 false")
	}
	if ar.WriteSet != nil {
		t.Fatalf("钩子失败后不得导出 write-set：%+v", ar.WriteSet)
	}
	// 实盘依旧零变化（钩子失败不改变「预演不落盘」这条）。
	for rel, content := range files {
		if readVaultFile(t, dir, rel) != content {
			t.Fatalf("预演期间实盘被改动：%s", rel)
		}
	}
}
