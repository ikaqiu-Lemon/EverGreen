package plan

// atomic_test.go — T-072 批次 B1：plan 原子预演入口 ExecuteAtomic 的聚焦测试。
//
// 覆盖：N=1/2/5 写、同文件多 op 合并（B3 正确基线）、新建/既有前像、预演零实盘写、
// 导出字节原样、失败账本（不把部分 staged 误当成功）、skip 不进入 accepted set，
// 以及与 Execute 的账本对拍。全程不触 S5 提交层、不接 CLI / index。

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

var atomicSlugs = []string{"alpha", "bravo", "charlie", "delta", "echo"}

// atomicCards 造 n 张 active 卡（各自独立），返回库与卡 ID。
func atomicCards(n int) (map[string]string, []string) {
	files := map[string]string{}
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := "k-20260901-" + atomicSlugs[i]
		rel := fmt.Sprintf("domains/ai-infra/knowledge/%s.md", id)
		files[rel] = cardM3(id, "active", "")
		ids = append(ids, id)
	}
	return files, ids
}

// execRealAndDry 在两份完全相同的实盘库上分别跑 Execute（直落实盘）与 ExecuteAtomic
// （只落 overlay），并断言预演零实盘写；返回两者结果与实盘目录供对拍。
func execRealAndDry(t *testing.T, files map[string]string, ops string) (*ExecResult, *AtomicResult, string) {
	t.Helper()
	res := m3Run(t, files, ops)
	if res.Failed() {
		t.Fatalf("plan 不应 error：%v", codes(res.Errors))
	}
	opt := ExecOptions{Stamp: mustStamp(t)}

	realDir := t.TempDir()
	writeVault(t, realDir, files)
	out := Execute(store.New(realDir), res, opt)

	dryDir := t.TempDir()
	writeVault(t, dryDir, files)
	ar, err := ExecuteAtomic(store.New(dryDir), res, opt)
	if err != nil {
		t.Fatalf("ExecuteAtomic: %v", err)
	}
	for rel, content := range files {
		if got := readVaultFile(t, dryDir, rel); got != content {
			t.Fatalf("预演期间实盘被改动：%s", rel)
		}
	}
	return out, ar, realDir
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// assertLedgerParity 断言预演账本与实盘执行账本逐项对齐。
func assertLedgerParity(t *testing.T, real *ExecResult, dry *ExecResult) {
	t.Helper()
	if got, want := sortedCopy(dry.Written), sortedCopy(real.Written); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Written 账本不一致：dry=%v real=%v", got, want)
	}
	if len(dry.Skipped) != len(real.Skipped) {
		t.Fatalf("Skipped 数不一致：dry=%d real=%d", len(dry.Skipped), len(real.Skipped))
	}
	if len(dry.Failures) != len(real.Failures) {
		t.Fatalf("Failures 数不一致：dry=%d real=%d", len(dry.Failures), len(real.Failures))
	}
}

// TestExecuteAtomicNDistinctWrites：N=1/2/5 张独立卡各追加一次，导出 N 条 write-set，
// 逐条目标字节与实盘落地结果逐字一致，且账本与 Execute 对拍。
func TestExecuteAtomicNDistinctWrites(t *testing.T) {
	for _, n := range []int{1, 2, 5} {
		files, ids := atomicCards(n)
		var ops []string
		for _, id := range ids {
			ops = append(ops, fmt.Sprintf(
				`{"op":"append_card","card":%q,"sections":{"解释与依据":"- 追加 %s\n"}}`, id, id))
		}
		out, ar, realDir := execRealAndDry(t, files, strings.Join(ops, ","))
		if !ar.Complete {
			t.Fatalf("N=%d 无失败应 Complete", n)
		}
		if len(ar.WriteSet) != n {
			t.Fatalf("N=%d 应导出 %d 条 write-set，got %d", n, n, len(ar.WriteSet))
		}
		for _, spec := range ar.WriteSet {
			if spec.IsNew {
				t.Fatalf("N=%d 既有卡不应标记为新建：%s", n, spec.Path)
			}
			if got := readVaultFile(t, realDir, spec.Path); string(spec.TargetBytes) != got {
				t.Fatalf("N=%d %s 预演目标与实盘落地不一致", n, spec.Path)
			}
			if spec.TargetHash != store.ContentHash(spec.TargetBytes) {
				t.Fatalf("N=%d %s 目标 hash 与字节不符", n, spec.Path)
			}
		}
		assertLedgerParity(t, out, ar.Exec)
	}
}

// TestExecuteAtomicSameCardMergesOneSpec：同一张卡上 append_card + add_material_rel
// 两个写 op，合并为一个 FileSpec（B3 用 staged 最新字节做基线，第二个 op 不自撞），
// 最终目标累计两处变更。
func TestExecuteAtomicSameCardMergesOneSpec(t *testing.T) {
	files := m3Files()
	ops := `{"op":"append_card","card":"k-20260901-attention","sections":{"条件与边界":"- 追加边界\n"}},` +
		`{"op":"add_material_rel","card":"k-20260901-attention","source":"s-20260901-attention",` +
		`"note":"n-20260901-attention","rel":"support","reason":"原文实测二次挂载"}`
	out, ar, realDir := execRealAndDry(t, files, ops)
	if !ar.Complete {
		t.Fatalf("两个合法写应 Complete；failures=%+v", ar.Exec.Failures)
	}
	rel := "domains/ai-infra/knowledge/k-20260901-attention.md"
	var got *store.AtomicFileSpec
	for i := range ar.WriteSet {
		if ar.WriteSet[i].Path == rel {
			got = &ar.WriteSet[i]
		}
	}
	if got == nil {
		t.Fatalf("write-set 缺目标卡 %s：%+v", rel, ar.WriteSet)
	}
	// 同路径多 op 恰合并为一个 FileSpec。
	count := 0
	for _, s := range ar.WriteSet {
		if s.Path == rel {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("同一路径应合并为一个 FileSpec，got %d", count)
	}
	if !strings.Contains(string(got.TargetBytes), "- 追加边界") {
		t.Fatalf("最终目标应含 append_card 的追加")
	}
	if !strings.Contains(string(got.TargetBytes), "原文实测二次挂载") {
		t.Fatalf("最终目标应含 add_material_rel 的追加")
	}
	// 与实盘顺序执行结果逐字一致（证明 overlay 基线正确）。
	if real := readVaultFile(t, realDir, rel); string(got.TargetBytes) != real {
		t.Fatalf("同文件多 op 预演目标与实盘落地不一致")
	}
	assertLedgerParity(t, out, ar.Exec)
}

// TestExecuteAtomicCreatePreimage：新建卡在 write-set 里 IsNew=true 且前像为空；
// 既有卡追加 IsNew=false 且前像等于首次实盘快照。
func TestExecuteAtomicCreatePreimage(t *testing.T) {
	files := m3Files()
	rel := "domains/ai-infra/knowledge/k-20260901-attention.md"
	preHash := store.ContentHash([]byte(files[rel]))

	ops := `{"op":"create_card","card_id":"k-20260902-flash","title":"FlashAttention",` +
		`"sources":[{"source":"s-20260901-attention","note":"n-20260901-attention","rel":"support","reason":"原文实测"}],` +
		`"sections":{"知识内容":"分块计算\n"}},` +
		`{"op":"append_card","card":"k-20260901-attention","sections":{"解释与依据":"- 既有卡追加\n"}}`
	_, ar, _ := execRealAndDry(t, files, ops)
	if !ar.Complete {
		t.Fatalf("新建+追加应 Complete；failures=%+v", ar.Exec.Failures)
	}
	byPath := map[string]store.AtomicFileSpec{}
	for _, s := range ar.WriteSet {
		byPath[s.Path] = s
	}
	newSpec, ok := byPath["domains/ai-infra/knowledge/k-20260902-flash.md"]
	if !ok {
		t.Fatalf("write-set 缺新建卡：%+v", ar.WriteSet)
	}
	if !newSpec.IsNew || newSpec.PreBytes != nil || newSpec.PreHash != "" {
		t.Fatalf("新建卡应 IsNew=true 且前像为空，got %+v", newSpec)
	}
	oldSpec, ok := byPath[rel]
	if !ok {
		t.Fatalf("write-set 缺既有卡：%+v", ar.WriteSet)
	}
	if oldSpec.IsNew || string(oldSpec.PreBytes) != files[rel] || oldSpec.PreHash != preHash {
		t.Fatalf("既有卡前像应等于首次实盘快照，got IsNew=%v preHash=%s", oldSpec.IsNew, oldSpec.PreHash)
	}
}

// TestExecuteAtomicFailureGatesWriteSet：普通写失败（缺文件的 CardAppend 读失败）→
// Complete=false 且 WriteSet=nil，绝不把前序 op 的部分 staged 误当成功；账本准确。
func TestExecuteAtomicFailureGatesWriteSet(t *testing.T) {
	files, ids := atomicCards(2)
	dir := t.TempDir()
	writeVault(t, dir, files)
	okRel := fmt.Sprintf("domains/ai-infra/knowledge/%s.md", ids[0])
	okHash := store.ContentHash([]byte(files[okRel]))

	// 直接构造 Actions：op0 合法追加（会 stage），op1 指向不存在的文件（读失败 → 普通失败）。
	res := &Result{Verb: "process", Domain: "ai-infra", Actions: []Action{
		{Kind: ActCardAppend, OpIndex: 0, ID: ids[0], Path: okRel, ExpectedHash: okHash,
			Sections: []SectionWrite{{Section: "解释与依据", Payload: []byte("- 会 stage\n")}}},
		{Kind: ActCardAppend, OpIndex: 1, ID: "k-20260901-missing",
			Path:     "domains/ai-infra/knowledge/k-20260901-missing.md",
			Sections: []SectionWrite{{Section: "解释与依据", Payload: []byte("- 读失败\n")}}},
	}}
	ar, err := ExecuteAtomic(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)})
	if err != nil {
		t.Fatalf("ExecuteAtomic: %v", err)
	}
	if len(ar.Exec.Failures) != 1 {
		t.Fatalf("应恰一条 Failure，got %+v", ar.Exec.Failures)
	}
	if ar.Complete {
		t.Fatalf("有普通写失败时 Complete 必须为 false")
	}
	if ar.WriteSet != nil {
		t.Fatalf("失败时不得导出部分 staged 集合，got %+v", ar.WriteSet)
	}
	// 账本仍准确：op0 记为已写。
	if len(ar.Exec.Written) != 1 || ar.Exec.Written[0] != okRel {
		t.Fatalf("失败账本仍应如实记录 op0 已写：%+v", ar.Exec.Written)
	}
	// 实盘零变化。
	if got := readVaultFile(t, dir, okRel); got != files[okRel] {
		t.Fatalf("预演期间实盘不应变化")
	}
}

// TestExecuteAtomicSkipDoesNotEnterWriteSet：B3 跳过不 stage、不进入 write-set，
// 且跳过不影响 Complete；成功 op 照常导出。
func TestExecuteAtomicSkipDoesNotEnterWriteSet(t *testing.T) {
	files, ids := atomicCards(2)
	dir := t.TempDir()
	writeVault(t, dir, files)
	okRel := fmt.Sprintf("domains/ai-infra/knowledge/%s.md", ids[0])
	skipRel := fmt.Sprintf("domains/ai-infra/knowledge/%s.md", ids[1])
	okHash := store.ContentHash([]byte(files[okRel]))

	res := &Result{Verb: "process", Domain: "ai-infra", Actions: []Action{
		{Kind: ActCardAppend, OpIndex: 0, ID: ids[0], Path: okRel, ExpectedHash: okHash,
			Sections: []SectionWrite{{Section: "解释与依据", Payload: []byte("- 会 stage\n")}}},
		// 错误的 ExpectedHash → B3 SkipFileChanged。
		{Kind: ActCardAppend, OpIndex: 1, ID: ids[1], Path: skipRel, ExpectedHash: "sha256:deadbeef",
			Sections: []SectionWrite{{Section: "解释与依据", Payload: []byte("- 应跳过\n")}}},
	}}
	ar, err := ExecuteAtomic(store.New(dir), res, ExecOptions{Stamp: mustStamp(t)})
	if err != nil {
		t.Fatalf("ExecuteAtomic: %v", err)
	}
	if len(ar.Exec.Skipped) != 1 {
		t.Fatalf("应恰一条 Skipped，got %+v", ar.Exec.Skipped)
	}
	if !ar.Complete {
		t.Fatalf("跳过不是失败，Complete 应为 true")
	}
	if len(ar.WriteSet) != 1 || ar.WriteSet[0].Path != okRel {
		t.Fatalf("跳过不进 write-set，成功 op 照常导出：%+v", ar.WriteSet)
	}
}
