package txn

// [S5] commit_test.go —— T-…-072 批次 A：多文件原子提交的聚焦反证（合同 §4 / §5.1）。
// 判据 6 的六支：全成或全不成、无撕裂、fsync 先于 rename、幂等、字节保真、intent 屏障先于首个权威 rename。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wsFile 描述一次事务写集合中的一个文件（测试构造用）。
type wsFile struct {
	path   string
	create bool
	pre    []byte // create:false 的前像字节；create:true 忽略
	target []byte
}

// makeOpenTxnWithSet 按 files 建出一个「intent 已发布、commit/abort 皆缺席」的 OpenTxn：
// 先为 create:false 项写前像权威文件，再 AllocateTxnID + WriteIntent（写 pre/<n> 与 intent.json）。
// 返回 txn_id。此后测试可手工把权威文件设成各崩溃点的磁盘态。
func makeOpenTxnWithSet(t *testing.T, vault string, files []wsFile) string {
	t.Helper()
	for _, f := range files {
		if !f.create {
			writeRaw(t, filepath.Join(vault, f.path), f.pre)
		}
	}
	id, err := AllocateTxnID(vault)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	specs := make([]FileSpec, 0, len(files))
	for _, f := range files {
		specs = append(specs, FileSpec{
			Path: f.path, Create: f.create, PreBytes: f.pre, TargetBytes: f.target,
			TargetOp: opFor(f.create),
		})
	}
	if _, err := WriteIntent(vault, id, IntentInput{Argv: []string{"eg", "apply"}, Files: specs}); err != nil {
		t.Fatalf("写 intent 失败：%v", err)
	}
	return id
}

func opFor(create bool) string {
	if create {
		return "create"
	}
	return "replace"
}

func commitInputFor(files []wsFile) CommitInput {
	in := CommitInput{}
	for _, f := range files {
		in.Files = append(in.Files, CommitFile{Path: f.path, TargetBytes: f.target})
	}
	return in
}

func readAuth(t *testing.T, vault, rel string) []byte {
	t.Helper()
	return mustReadFile(t, filepath.Join(vault, rel))
}

func authExists(t *testing.T, vault, rel string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(vault, rel))
	return err == nil
}

func setAuth(t *testing.T, vault, rel string, data []byte) {
	t.Helper()
	writeRaw(t, filepath.Join(vault, rel), data)
}

// markerPresent 只读判断某标记是否在盘。
func markerPresent(t *testing.T, vault, txnID, name string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(TxnDirPath(vault, txnID), name))
	return err == nil
}

func setCommitFailpoint(t *testing.T, fn func(step string, index int) error) {
	t.Helper()
	prev := commitFailpoint
	commitFailpoint = fn
	t.Cleanup(func() { commitFailpoint = prev })
}

func setCommitMarkerFailpoint(t *testing.T, fn func(vaultRoot, txnID string) error) {
	t.Helper()
	prev := commitMarkerFailpoint
	commitMarkerFailpoint = fn
	t.Cleanup(func() { commitMarkerFailpoint = prev })
}

// —— 判据 6-1：多文件全成 —— 提交成功后全部权威文件为目标态、commit 标记在盘。
func TestMultiFileCommitAllOrNothing(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
		{path: "sub/c.md", create: true, target: []byte("C1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)
	res, err := Commit(vault, id, commitInputFor(files))
	if err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if !res.Committed || res.FilesWritten != 3 {
		t.Fatalf("期望 Committed 且写 3 文件，得 %+v", res)
	}
	for _, f := range files {
		if got := readAuth(t, vault, f.path); string(got) != string(f.target) {
			t.Fatalf("%s 应为目标态 %q，得 %q", f.path, f.target, got)
		}
	}
	if !markerPresent(t, vault, id, CommitMarker) {
		t.Fatalf("commit 标记应在盘")
	}
}

// —— 判据 6-2：无撕裂 —— 首个 rename 后崩溃点上，每个权威文件恒为「完整前像」或「完整目标态」，绝无半写。
func TestNoTornFileEverVisible(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1-longer-content\n")},
		{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)

	setCommitFailpoint(t, func(step string, index int) error {
		if step == fpCommitBeforeRename && index == 1 {
			// 此刻 a.md 已 rename 为完整目标态，b.md 仍是完整前像；两者都不是半写。
			if a := readAuth(t, vault, "a.md"); string(a) != string(files[0].target) {
				t.Fatalf("a.md 应为完整目标态，得 %q", a)
			}
			if b := readAuth(t, vault, "b.md"); string(b) != string(files[1].pre) {
				t.Fatalf("b.md 应为完整前像，得 %q", b)
			}
		}
		return nil
	})
	if _, err := Commit(vault, id, commitInputFor(files)); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
}

// —— 判据 6-3：fsync 先于 rename —— 备料阶段完成时，尚无任何权威 rename，且目标目录里有临时文件。
func TestFsyncBeforeRename(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)

	setCommitFailpoint(t, func(step string, index int) error {
		if step == fpCommitAfterStage {
			for _, f := range files {
				if got := readAuth(t, vault, f.path); string(got) != string(f.pre) {
					t.Fatalf("备料阶段 %s 不应发生权威 rename，仍应为前像，得 %q", f.path, got)
				}
			}
			ents, _ := os.ReadDir(vault)
			hasTmp := false
			for _, e := range ents {
				if strings.HasPrefix(e.Name(), ".eg-txn-") {
					hasTmp = true
				}
			}
			if !hasTmp {
				t.Fatalf("备料阶段应存在临时文件（tmp 已写 + fsync）")
			}
		}
		return nil
	})
	if _, err := Commit(vault, id, commitInputFor(files)); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
}

// —— 判据 6-4：提交幂等 —— 重复 Commit 结果不变，第二次零权威写入。
func TestCommitIsIdempotent(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
	id := makeOpenTxnWithSet(t, vault, files)
	if _, err := Commit(vault, id, commitInputFor(files)); err != nil {
		t.Fatalf("首次提交失败：%v", err)
	}
	res2, err := Commit(vault, id, commitInputFor(files))
	if err != nil {
		t.Fatalf("二次提交应幂等，得 %v", err)
	}
	if !res2.Committed || res2.FilesWritten != 0 {
		t.Fatalf("幂等二次提交应零写入，得 %+v", res2)
	}
	if got := readAuth(t, vault, "a.md"); string(got) != "A1\n" {
		t.Fatalf("内容应稳定为目标态，得 %q", got)
	}
}

// —— 判据 6-5：字节保真 —— 提交写入与调用方给的目标字节逐字节相同（含末尾换行 / 无换行）。
func TestAtomicCommitPreservesBytes(t *testing.T) {
	vault := newVault(t)
	target := []byte("line1\nline2\n\tindented\nno-trailing-newline")
	files := []wsFile{{path: "keep.md", pre: []byte("old"), target: target}}
	id := makeOpenTxnWithSet(t, vault, files)
	if _, err := Commit(vault, id, commitInputFor(files)); err != nil {
		t.Fatalf("提交失败：%v", err)
	}
	if got := readAuth(t, vault, "keep.md"); string(got) != string(target) {
		t.Fatalf("字节不保真：\n want %q\n got  %q", target, got)
	}
}

// —— 判据 6-6：intent 屏障先于首个权威 rename ——
//
//	(a) intent 未发布（residue）⇒ Commit 拒绝、权威零写入；
//	(b) 成功路径下，首个权威 rename 时刻 intent.json 已在盘且可解析、全部 pre/<n> 已存在。
func TestIntentBarrierBeforeFirstAuthoritativeRename(t *testing.T) {
	// (a) residue：无 intent.json ⇒ 不允许写任何权威文件。
	t.Run("refuse_without_published_intent", func(t *testing.T) {
		vault := newVault(t)
		setAuth(t, vault, "a.md", []byte("A0\n"))
		id, _ := mkTxnDir(t, vault, 1) // 仅目录，无 intent.json ⇒ residue
		files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
		_, err := Commit(vault, id, commitInputFor(files))
		if err == nil {
			t.Fatalf("intent 未发布时提交应被拒绝")
		}
		var se *CommitStateError
		if !errors.As(err, &se) {
			t.Fatalf("应为 CommitStateError，得 %T", err)
		}
		if got := readAuth(t, vault, "a.md"); string(got) != "A0\n" {
			t.Fatalf("拒绝路径下权威文件不应被改动，得 %q", got)
		}
	})

	// (b) 成功路径：首个 rename 前 intent 屏障已完成。
	t.Run("barrier_satisfied_before_first_rename", func(t *testing.T) {
		vault := newVault(t)
		files := []wsFile{
			{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
			{path: "b.md", create: true, target: []byte("B1\n")},
		}
		id := makeOpenTxnWithSet(t, vault, files)
		checked := false
		setCommitFailpoint(t, func(step string, index int) error {
			if step == fpCommitBeforeRename && index == 0 {
				checked = true
				// intent.json 已发布且可解析。
				res, err := Scan(vault)
				if err != nil {
					t.Fatalf("scan 失败：%v", err)
				}
				var e TxnEntry
				for _, x := range res.Entries {
					if x.TxnID == id {
						e = x
					}
				}
				if e.State != StateOpen || e.Intent == nil {
					t.Fatalf("首个权威 rename 前 intent 必须已发布为可解析 Open，得 state=%v", e.State)
				}
				// 全部 create:false 项的 pre/<n> 已存在。
				preAbs := filepath.Join(TxnDirPath(vault, id), "pre", "0")
				if _, serr := os.Stat(preAbs); serr != nil {
					t.Fatalf("首个权威 rename 前 pre/0 必须已存在：%v", serr)
				}
				// 尚无任何权威文件是目标态。
				if authExists(t, vault, "b.md") {
					t.Fatalf("首个权威 rename 前 create 文件不应存在")
				}
			}
			return nil
		})
		if _, err := Commit(vault, id, commitInputFor(files)); err != nil {
			t.Fatalf("提交失败：%v", err)
		}
		if !checked {
			t.Fatalf("未触达首个 rename 前的注入点")
		}
	})
}

// —— 写集合与已发布 intent 不一致 ⇒ fail closed（携 E15），权威零写入。
func TestCommitInputMismatchFailsClosed(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
	id := makeOpenTxnWithSet(t, vault, files)
	// 篡改目标字节，使其与 intent.target_hash 不符。
	bad := CommitInput{Files: []CommitFile{{Path: "a.md", TargetBytes: []byte("TAMPERED\n")}}}
	_, err := Commit(vault, id, bad)
	if err == nil {
		t.Fatalf("写集合不一致应被拒绝")
	}
	var c coded
	if !errors.As(err, &c) || c.Code() != CodePrecheckFailed {
		t.Fatalf("应携 E15，得 %T/%v", err, err)
	}
	if got := readAuth(t, vault, "a.md"); string(got) != "A0\n" {
		t.Fatalf("拒绝路径下权威零写入，得 %q", got)
	}
}

// —— 提交失败路径：备料后注入错误 ⇒ 保持未闭合→按恢复回滚→写 abort，权威回到前像。
func TestCommitFailureRollsBackToPreimageThenAbort(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)
	// 在渲染出第一个权威 rename 之后（index 1 之前）注入失败，制造 P3 式部分应用。
	setCommitFailpoint(t, func(step string, index int) error {
		if step == fpCommitBeforeRename && index == 1 {
			return errors.New("注入提交失败")
		}
		return nil
	})
	_, err := Commit(vault, id, commitInputFor(files))
	if err == nil {
		t.Fatalf("提交应失败")
	}
	// 回滚后两个文件都回到前像，abort 在盘、commit 不在盘。
	if got := readAuth(t, vault, "a.md"); string(got) != "A0\n" {
		t.Fatalf("a.md 应回到前像，得 %q", got)
	}
	if got := readAuth(t, vault, "b.md"); string(got) != "B0\n" {
		t.Fatalf("b.md 应保持前像，得 %q", got)
	}
	if !markerPresent(t, vault, id, AbortMarker) {
		t.Fatalf("回滚完成后应写 abort")
	}
	if markerPresent(t, vault, id, CommitMarker) {
		t.Fatalf("失败路径不应有 commit 标记")
	}
}

// —— P0-2：全部权威 rename 已完成、但 commit 标记发布**返回错误**（非崩溃）——
// 绝不留下「目标态在盘 + 无合法 commit」；重读状态整体裁决后收敛（Committed / Open / fail closed）。
func TestCommitMarkerPublishFailure(t *testing.T) {
	// (1) 标记未发布即返回错误 ⇒ 重读为 Open ⇒ 回滚全部前像 + 写 abort（不留目标态）。
	t.Run("unpublished_rolls_back_and_aborts", func(t *testing.T) {
		vault := newVault(t)
		files := []wsFile{
			{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
			{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
		}
		id := makeOpenTxnWithSet(t, vault, files)
		setCommitMarkerFailpoint(t, func(vaultRoot, txnID string) error {
			return errors.New("注入：标记未发布即失败")
		})
		res, err := Commit(vault, id, commitInputFor(files))
		if err == nil {
			t.Fatalf("标记发布失败应返回错误")
		}
		if res == nil || !res.RolledBack {
			t.Fatalf("标记未发布应回滚，得 %+v", res)
		}
		if got := readAuth(t, vault, "a.md"); string(got) != "A0\n" {
			t.Fatalf("a.md 应回前像，得 %q", got)
		}
		if got := readAuth(t, vault, "b.md"); string(got) != "B0\n" {
			t.Fatalf("b.md 应回前像，得 %q", got)
		}
		if !markerPresent(t, vault, id, AbortMarker) {
			t.Fatalf("回滚后应写 abort")
		}
		if markerPresent(t, vault, id, CommitMarker) {
			t.Fatalf("不应留下 commit 标记")
		}
	})

	// (2) 标记其实已落盘、只是调用返回错误 ⇒ 重读为 Committed ⇒ 如实报告已提交，目标态保留。
	t.Run("actually_published_reports_committed", func(t *testing.T) {
		vault := newVault(t)
		files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
		id := makeOpenTxnWithSet(t, vault, files)
		setCommitMarkerFailpoint(t, func(vaultRoot, txnID string) error {
			if err := MarkCommit(vaultRoot, txnID); err != nil { // 先真发布（模拟已 fsync 落盘）
				t.Fatalf("注入内 MarkCommit 失败：%v", err)
			}
			return errors.New("注入：标记已落盘但返回路径异常")
		})
		res, err := Commit(vault, id, commitInputFor(files))
		if err != nil {
			t.Fatalf("标记已在盘时应如实报告已提交，得 err=%v", err)
		}
		if !res.Committed {
			t.Fatalf("应报告已提交，得 %+v", res)
		}
		if got := readAuth(t, vault, "a.md"); string(got) != "A1\n" {
			t.Fatalf("已提交目标态应保留，得 %q", got)
		}
		if !markerPresent(t, vault, id, CommitMarker) {
			t.Fatalf("commit 标记应在盘")
		}
	})

	// (3) 标记发布失败且事务已被推进为 aborted ⇒ fail closed（携 E15 的 CommitStateError），绝不静默留半态。
	t.Run("aborted_fails_closed_e15", func(t *testing.T) {
		vault := newVault(t)
		files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
		id := makeOpenTxnWithSet(t, vault, files)
		setCommitMarkerFailpoint(t, func(vaultRoot, txnID string) error {
			if err := MarkAbort(vaultRoot, txnID); err != nil {
				t.Fatalf("注入内 MarkAbort 失败：%v", err)
			}
			return errors.New("注入：标记发布失败且事务已 aborted")
		})
		_, err := Commit(vault, id, commitInputFor(files))
		if err == nil {
			t.Fatalf("aborted 态应 fail closed")
		}
		var se *CommitStateError
		if !errors.As(err, &se) {
			t.Fatalf("应为 CommitStateError，得 %T", err)
		}
		var c coded
		if !errors.As(err, &c) || c.Code() != CodePrecheckFailed {
			t.Fatalf("应携 E15，得 %T/%v", err, err)
		}
	})
}

// —— P1：CommitStateError 是写前阻断，必须携 E15/Diagnostics（供 074 的 ExitCodeFor 映射）——类型断言。
func TestCommitStateErrorIsCodedE15(t *testing.T) {
	vault := newVault(t)
	setAuth(t, vault, "a.md", []byte("A0\n"))
	id, _ := mkTxnDir(t, vault, 1) // residue：无 intent.json ⇒ 不可提交
	files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
	_, err := Commit(vault, id, commitInputFor(files))
	var se *CommitStateError
	if !errors.As(err, &se) {
		t.Fatalf("应为 CommitStateError，得 %T", err)
	}
	var c coded
	if !errors.As(err, &c) || c.Code() != CodePrecheckFailed {
		t.Fatalf("CommitStateError 应携 E15，得 %T/%v", err, err)
	}
	if d := c.Diagnostics(); len(d) != 1 || d[0].Code != CodePrecheckFailed {
		t.Fatalf("应提供一条 E15 Diagnostics，得 %+v", d)
	}
	if got := readAuth(t, vault, "a.md"); string(got) != "A0\n" {
		t.Fatalf("fail closed 路径下权威零写入，得 %q", got)
	}
}
