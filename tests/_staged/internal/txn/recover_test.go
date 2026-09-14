package txn

// [S5] recover_test.go —— T-…-072 批次 A：两遍崩溃恢复的聚焦反证（合同 §4.1 / §4.1.1 / §4.1.2 / §6）。
// 覆盖：P1~P9 矩阵、W26 分布、越界零触碰、幂等、abort-last、回滚中崩溃可重入、
// 整事务原子性（末项冲突零写）、坏前像 fail closed、路径逃逸、多 Open + Corrupt 整体阻断。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setRecoverFailpoint(t *testing.T, fn func(step string, index int) error) {
	t.Helper()
	prev := recoverFailpoint
	recoverFailpoint = fn
	t.Cleanup(func() { recoverFailpoint = prev })
}

// hasW26 判断恢复回执里是否含恰一条 W26。
func hasW26(res *RecoverResult) bool {
	n := 0
	for _, d := range res.Diagnostics {
		if d.Code == CodeTxnRecovered {
			n++
		}
	}
	return n == 1
}

func assertE15(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望携 E15 的 fail closed 错误，得 nil")
	}
	var c coded
	if !errors.As(err, &c) || c.Code() != CodePrecheckFailed {
		t.Fatalf("期望携 E15，得 %T/%v", err, err)
	}
}

// —— 判据 7：崩溃恢复矩阵 P1~P9 逐行 —— 恢复后磁盘态与 W26 分布逐行相符。
func TestCrashRecoveryMatrix(t *testing.T) {
	// 一个 2 改写 + 1 新建的写集合。
	mkFiles := func() []wsFile {
		return []wsFile{
			{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
			{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
			{path: "n.md", create: true, target: []byte("N1\n")},
		}
	}

	// P1：intent 发布屏障完成前崩溃 ⇒ 只有 residue 目录、无 intent.json ⇒ 无回滚、无 W26、清理 residue。
	t.Run("P1_pre_intent_residue", func(t *testing.T) {
		vault := newVault(t)
		setAuth(t, vault, "a.md", []byte("A0\n"))
		id, _ := mkTxnDir(t, vault, 1) // 无 intent.json
		res, err := Recover(vault)
		if err != nil {
			t.Fatalf("P1 恢复应成功，得 %v", err)
		}
		if res.Outcome != RecoverNoop || hasW26(res) {
			t.Fatalf("P1 不应回滚、不发 W26，得 %+v", res)
		}
		if _, serr := os.Stat(TxnDirPath(vault, id)); !errors.Is(serr, os.ErrNotExist) {
			t.Fatalf("P1 residue 应被清理")
		}
		if string(readAuth(t, vault, "a.md")) != "A0\n" {
			t.Fatalf("P1 权威文件应零改动")
		}
	})

	// P2：intent 已发布、首个 rename 前崩溃 ⇒ 全部文件仍在前像/不存在 ⇒ 回滚（0 还原）+ W26。
	t.Run("P2_intent_written_before_first_rename", func(t *testing.T) {
		vault := newVault(t)
		files := mkFiles()
		id := makeOpenTxnWithSet(t, vault, files)
		// 崩溃点：无任何权威 rename（a/b 仍前像，n 不存在）。
		res, err := Recover(vault)
		if err != nil {
			t.Fatalf("P2 恢复失败：%v", err)
		}
		if res.Outcome != RecoverRolledBack || !hasW26(res) {
			t.Fatalf("P2 应回滚并发 W26，得 %+v", res)
		}
		assertMatrixRolledBack(t, vault, id, files)
	})

	// P3：部分 rename（a 已生效，b 未、n 已生效）⇒ 回滚已生效者到前像 / quarantine + W26。
	t.Run("P3_partial_rename", func(t *testing.T) {
		vault := newVault(t)
		files := mkFiles()
		id := makeOpenTxnWithSet(t, vault, files)
		setAuth(t, vault, "a.md", files[0].target) // 已 rename
		setAuth(t, vault, "n.md", files[2].target) // 新建已 rename
		// b.md 仍前像。
		res, err := Recover(vault)
		if err != nil {
			t.Fatalf("P3 恢复失败：%v", err)
		}
		if res.Outcome != RecoverRolledBack || !hasW26(res) {
			t.Fatalf("P3 应回滚并发 W26，得 %+v", res)
		}
		assertMatrixRolledBack(t, vault, id, files)
	})

	// P4：全部 rename 后、commit 标记前崩溃 ⇒ 全部回滚 + W26。
	t.Run("P4_all_renamed_before_commit", func(t *testing.T) {
		vault := newVault(t)
		files := mkFiles()
		id := makeOpenTxnWithSet(t, vault, files)
		setAuth(t, vault, "a.md", files[0].target)
		setAuth(t, vault, "b.md", files[1].target)
		setAuth(t, vault, "n.md", files[2].target)
		res, err := Recover(vault)
		if err != nil {
			t.Fatalf("P4 恢复失败：%v", err)
		}
		if res.Outcome != RecoverRolledBack || !hasW26(res) {
			t.Fatalf("P4 应回滚并发 W26，得 %+v", res)
		}
		assertMatrixRolledBack(t, vault, id, files)
	})

	// P5~P8：commit 标记在盘 ⇒ 保持已提交态、恢复不回滚、不发 W26。
	for _, name := range []string{"P5_after_commit_marker", "P6_during_git", "P7_before_lock_release", "P8_before_log_cleanup"} {
		t.Run(name, func(t *testing.T) {
			vault := newVault(t)
			files := mkFiles()
			id := makeOpenTxnWithSet(t, vault, files)
			setAuth(t, vault, "a.md", files[0].target)
			setAuth(t, vault, "b.md", files[1].target)
			setAuth(t, vault, "n.md", files[2].target)
			if err := MarkCommit(vault, id); err != nil {
				t.Fatalf("写 commit 标记失败：%v", err)
			}
			res, err := Recover(vault)
			if err != nil {
				t.Fatalf("%s 恢复失败：%v", name, err)
			}
			if res.Outcome != RecoverNoop || hasW26(res) {
				t.Fatalf("%s commit 在盘不应回滚 / 不发 W26，得 %+v", name, res)
			}
			// 已提交态保持不变。
			if string(readAuth(t, vault, "a.md")) != "A1\n" || string(readAuth(t, vault, "n.md")) != "N1\n" {
				t.Fatalf("%s 已提交态应保持", name)
			}
		})
	}

	// P9：S7 Git 提交失败是 CLI/Git 层非崩溃路径（不属 internal/txn）。txn 层可观察投影 = commit 已在盘、
	// 磁盘保留当前状态、恢复无操作、不发 W26。此处以该投影断言，退出码 4 归 plan/cli（本批次不接）。
	t.Run("P9_git_commit_failed_projection", func(t *testing.T) {
		vault := newVault(t)
		files := mkFiles()
		id := makeOpenTxnWithSet(t, vault, files)
		setAuth(t, vault, "a.md", files[0].target)
		setAuth(t, vault, "b.md", files[1].target)
		setAuth(t, vault, "n.md", files[2].target)
		if err := MarkCommit(vault, id); err != nil {
			t.Fatalf("写 commit 标记失败：%v", err)
		}
		res, err := Recover(vault)
		if err != nil {
			t.Fatalf("P9 投影恢复失败：%v", err)
		}
		if res.Outcome != RecoverNoop || hasW26(res) {
			t.Fatalf("P9 投影：commit 在盘应保留、不发 W26，得 %+v", res)
		}
	})
}

// assertMatrixRolledBack 断言一次成功回滚后的磁盘态：改写文件回前像、新建文件移入 quarantine 且原路径消失、
// abort 在盘、commit 不在盘。
func assertMatrixRolledBack(t *testing.T, vault, id string, files []wsFile) {
	t.Helper()
	for i, f := range files {
		if f.create {
			if authExists(t, vault, f.path) {
				t.Fatalf("新建文件 %s 回滚后原路径应消失", f.path)
			}
			q := filepath.Join(TxnDirPath(vault, id), QuarantineDir, itoa(i))
			// 仅当该新建文件曾被 rename（current==target）才会进 quarantine；未 rename 时无副本。
			if authWasCreated(f) {
				if _, serr := os.Stat(q); serr == nil {
					if string(mustReadFile(t, q)) != string(f.target) {
						t.Fatalf("quarantine 副本字节应等于目标态")
					}
				}
			}
			continue
		}
		if got := readAuth(t, vault, f.path); string(got) != string(f.pre) {
			t.Fatalf("%s 应回到前像 %q，得 %q", f.path, f.pre, got)
		}
	}
	if !markerPresent(t, vault, id, AbortMarker) {
		t.Fatalf("回滚完成后应写 abort")
	}
	if markerPresent(t, vault, id, CommitMarker) {
		t.Fatalf("回滚路径不应有 commit")
	}
}

// authWasCreated 仅用于测试可读性（新建文件在被 rename 后才有 quarantine 副本）。
func authWasCreated(f wsFile) bool { return f.create }

func itoa(i int) string { return string(rune('0' + i)) }

// —— W26 只在真实回滚时出现（P2/P3/P4），恰一条。
func TestRecoverEmitsW26(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, "a.md", files[0].target) // 已 rename ⇒ B-R2
	res, err := Recover(vault)
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if !hasW26(res) || res.TxnID != id {
		t.Fatalf("应发恰一条 W26 且指向 %s，得 %+v", id, res)
	}
}

// —— 恢复只碰事务内文件，绝不触碰事务之外的任何文件。
func TestRecoverNeverTouchesOutsideTxn(t *testing.T) {
	vault := newVault(t)
	setAuth(t, vault, "outside.md", []byte("UNTOUCHED\n"))
	files := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, "a.md", files[0].target)
	before := snapshotTree(t, filepath.Join(vault))
	_ = before
	outBefore := readAuth(t, vault, "outside.md")
	if _, err := Recover(vault); err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if string(readAuth(t, vault, "outside.md")) != string(outBefore) {
		t.Fatalf("事务外文件不应被触碰")
	}
	if string(readAuth(t, vault, "a.md")) != "A0\n" {
		t.Fatalf("事务内文件应回前像")
	}
	_ = id
}

// —— 恢复幂等：重复恢复结果不变。
func TestRecoverIdempotent(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "n.md", create: true, target: []byte("N1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, "a.md", files[0].target)
	setAuth(t, vault, "n.md", files[1].target)
	if _, err := Recover(vault); err != nil {
		t.Fatalf("首次恢复失败：%v", err)
	}
	snap1 := snapshotTree(t, vault)
	// 二次恢复：事务已 aborted，不再是 Open ⇒ 无操作、磁盘不变。
	res2, err := Recover(vault)
	if err != nil {
		t.Fatalf("二次恢复失败：%v", err)
	}
	if res2.Outcome != RecoverNoop {
		t.Fatalf("二次恢复应无操作，得 %+v", res2)
	}
	if snapshotTree(t, vault) != snap1 {
		t.Fatalf("二次恢复不应改变磁盘")
	}
	_ = id
}

// —— abort 只在全部回滚完成后才写：注入「写 abort 前」崩溃 ⇒ abort 未落盘、各文件已回前像。
func TestAbortMarkerWrittenOnlyAfterFullRollback(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, "a.md", files[0].target)
	setAuth(t, vault, "b.md", files[1].target)
	setRecoverFailpoint(t, func(step string, index int) error {
		if step == fpRecoverBeforeAbort {
			// 此刻两个文件都应已回前像，但 abort 尚未写。
			if string(readAuth(t, vault, "a.md")) != "A0\n" || string(readAuth(t, vault, "b.md")) != "B0\n" {
				t.Fatalf("写 abort 前所有文件字节必须已等于前像")
			}
			return errors.New("注入：写 abort 前崩溃")
		}
		return nil
	})
	_, err := Recover(vault)
	if err == nil {
		t.Fatalf("注入崩溃应返回错误")
	}
	if markerPresent(t, vault, id, AbortMarker) {
		t.Fatalf("回滚未完整闭合前不应有 abort")
	}
}

// —— 回滚中途崩溃可重入：还原完 a 后崩溃 ⇒ 事务仍未闭合 ⇒ 重跑完成、结果幂等。
func TestCrashDuringRollbackResumesRollback(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, "a.md", files[0].target)
	setAuth(t, vault, "b.md", files[1].target)

	// 第一次：还原完第 0 个文件后崩溃（abort 未写）。
	setRecoverFailpoint(t, func(step string, index int) error {
		if step == fpRecoverAfterRestore && index == 0 {
			return errors.New("注入：回滚途中崩溃")
		}
		return nil
	})
	if _, err := Recover(vault); err == nil {
		t.Fatalf("首次恢复应因注入而失败")
	}
	if markerPresent(t, vault, id, AbortMarker) {
		t.Fatalf("回滚中崩溃后不应有 abort（事务仍未闭合）")
	}
	// 第二次：无注入，重跑并完成。
	setRecoverFailpoint(t, nil)
	res, err := Recover(vault)
	if err != nil {
		t.Fatalf("重入恢复失败：%v", err)
	}
	if res.Outcome != RecoverRolledBack || !hasW26(res) {
		t.Fatalf("重入应完成回滚并发 W26，得 %+v", res)
	}
	if string(readAuth(t, vault, "a.md")) != "A0\n" || string(readAuth(t, vault, "b.md")) != "B0\n" {
		t.Fatalf("重入后两个文件都应回前像")
	}
	if !markerPresent(t, vault, id, AbortMarker) {
		t.Fatalf("重入完成后应写 abort")
	}
}

// —— 整事务原子性 + 末项冲突零写：唯一冲突项置于 files[] 最后一位 ⇒ 前面的 B-R2 一个都不写。
func TestRecoverConflictAtLastFileRollsBackNothing(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
		{path: "c.md", pre: []byte("C0\n"), target: []byte("C1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)
	// a、b 处于目标态（本应 B-R2），c 被外部编辑成第三种字节（B-R3，置于末位）。
	setAuth(t, vault, "a.md", files[0].target)
	setAuth(t, vault, "b.md", files[1].target)
	setAuth(t, vault, "c.md", []byte("C-EXTERNAL-EDIT\n"))

	snapBefore := snapshotTree(t, vault)
	_, err := Recover(vault)
	assertE15(t, err)
	var ce *RecoverConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("应为 RecoverConflictError，得 %T", err)
	}
	// 前面的 B-R2 未被提前回滚：a、b 仍是目标态（磁盘逐字节不变）。
	if string(readAuth(t, vault, "a.md")) != string(files[0].target) {
		t.Fatalf("末项冲突时 a.md 不应被提前回滚")
	}
	if string(readAuth(t, vault, "b.md")) != string(files[1].target) {
		t.Fatalf("末项冲突时 b.md 不应被提前回滚")
	}
	if markerPresent(t, vault, id, AbortMarker) || markerPresent(t, vault, id, CommitMarker) {
		t.Fatalf("整事务零写：不应有 abort / commit")
	}
	if snapshotTree(t, vault) != snapBefore {
		t.Fatalf("整事务零写：磁盘应逐字节不变")
	}
}

// —— 整事务原子性：Pass A 判出任一冲突则事务内任何文件都不被写（冲突项在中间位置）。
func TestRecoverIsAllOrNothingAcrossFiles(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{
		{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
		{path: "mid.md", pre: []byte("M0\n"), target: []byte("M1\n")},
		{path: "z.md", pre: []byte("Z0\n"), target: []byte("Z1\n")},
	}
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, "a.md", files[0].target)
	setAuth(t, vault, "mid.md", []byte("MID-CONFLICT\n")) // B-R3 在中间
	setAuth(t, vault, "z.md", files[2].target)
	snapBefore := snapshotTree(t, vault)
	_, err := Recover(vault)
	assertE15(t, err)
	if snapshotTree(t, vault) != snapBefore {
		t.Fatalf("任一冲突 ⇒ 整事务零写，磁盘应逐字节不变")
	}
	if markerPresent(t, vault, id, AbortMarker) {
		t.Fatalf("冲突时不应写 abort")
	}
}

// —— 新建文件回滚移入 quarantine（字节完整、原路径消失、绝不物理删除）。
func TestCreateRollbackMovesToQuarantine(t *testing.T) {
	vault := newVault(t)
	files := []wsFile{{path: "new.md", create: true, target: []byte("NEW-CONTENT\n")}}
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, "new.md", files[0].target) // 新建已 rename
	res, err := Recover(vault)
	if err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	if res.Outcome != RecoverRolledBack || !hasW26(res) {
		t.Fatalf("应回滚并发 W26，得 %+v", res)
	}
	if authExists(t, vault, "new.md") {
		t.Fatalf("新建文件原路径应消失")
	}
	q := filepath.Join(TxnDirPath(vault, id), QuarantineDir, "0")
	if string(mustReadFile(t, q)) != "NEW-CONTENT\n" {
		t.Fatalf("quarantine 副本字节应与新建目标态逐字节相同")
	}
}

// —— pre-intent residue 经 Recover 静默清理：不发 W26、不阻断、可清理（Scan+Prune 面见 journal_test）。
func TestRecoverCleansPreIntentResidue(t *testing.T) {
	vault := newVault(t)
	id, dir := mkTxnDir(t, vault, 1)
	// 含空 pre/ 但无 intent.json。
	if err := os.MkdirAll(filepath.Join(dir, PreDirName), 0o755); err != nil {
		t.Fatalf("建 pre/ 失败：%v", err)
	}
	res, err := Recover(vault)
	if err != nil {
		t.Fatalf("residue 恢复应成功，得 %v", err)
	}
	if hasW26(res) || res.Outcome != RecoverNoop {
		t.Fatalf("residue 不应回滚 / 不发 W26，得 %+v", res)
	}
	if _, serr := os.Stat(TxnDirPath(vault, id)); !errors.Is(serr, os.ErrNotExist) {
		t.Fatalf("residue 应被清理")
	}
}

// —— 损坏 intent（不可解析）⇒ fail closed（E15），目录原样保留、权威零写入、不发 W26。
func TestCorruptIntentFailsClosed(t *testing.T) {
	vault := newVault(t)
	setAuth(t, vault, "a.md", []byte("A0\n"))
	_, dir := mkTxnDir(t, vault, 1)
	// 写半个 JSON（不可解析）。
	writeRaw(t, filepath.Join(dir, IntentFileName), []byte(`{"txn_id":`))
	before := snapshotTree(t, vault)
	_, err := Recover(vault)
	assertE15(t, err)
	if snapshotTree(t, vault) != before {
		t.Fatalf("损坏 intent ⇒ 权威 + 事务目录应逐字节原样保留")
	}
}

// —— 路径逃逸三例（绝对路径 / .. / 事务外 pre_bytes_ref）⇒ Pass A 即 fail closed（E15）、零写入。
func TestRecoverRejectsEscapingPaths(t *testing.T) {
	cases := map[string]func(id, dir string) []byte{
		"absolute_path": func(id, dir string) []byte {
			return rawIntentJSON(id, `{"path":"/etc/passwd","pre_hash":"","pre_size":0,"target_hash":"`+HashBytes([]byte("x"))+`","target_size":1,"create":true,"target_op":"create"}`)
		},
		"dotdot_path": func(id, dir string) []byte {
			return rawIntentJSON(id, `{"path":"../../outside.md","pre_hash":"","pre_size":0,"target_hash":"`+HashBytes([]byte("x"))+`","target_size":1,"create":true,"target_op":"create"}`)
		},
		"pre_ref_outside_txn": func(id, dir string) []byte {
			// create:false 但 pre_bytes_ref 指向其它目录（非 pre/<idx>）。
			return rawIntentJSON(id, `{"path":"a.md","pre_hash":"`+HashBytes([]byte("A0\n"))+`","pre_size":3,"pre_bytes_ref":"../evil","target_hash":"`+HashBytes([]byte("A1\n"))+`","target_size":3,"create":false,"target_op":"replace"}`)
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			vault := newVault(t)
			setAuth(t, vault, "a.md", []byte("A0\n"))
			id, dir := mkTxnDir(t, vault, 1)
			writeRaw(t, filepath.Join(dir, IntentFileName), mk(id, dir))
			before := snapshotTree(t, vault)
			_, err := Recover(vault)
			assertE15(t, err)
			if snapshotTree(t, vault) != before {
				t.Fatalf("%s：越界 ⇒ 整体零写入，磁盘应不变", name)
			}
			if markerPresent(t, vault, id, AbortMarker) {
				t.Fatalf("%s：不应写 abort", name)
			}
		})
	}
}

// —— 前像不可信五例 ⇒ Pass A 即 fail closed（E15）、绝不进入 Pass B、无 abort。
func TestRecoverRejectsCorruptPreimage(t *testing.T) {
	// 构造：create:false 单文件事务，authoritative 处于目标态（本应 B-R2），但破坏 pre/0。
	build := func(t *testing.T) (vault, id string) {
		vault = newVault(t)
		files := []wsFile{{path: "a.md", pre: []byte("A0-preimage\n"), target: []byte("A1\n")}}
		id = makeOpenTxnWithSet(t, vault, files)
		setAuth(t, vault, "a.md", files[0].target) // current==target ⇒ 若前像可信本会 B-R2
		return
	}
	preAbs := func(vault, id string) string { return filepath.Join(TxnDirPath(vault, id), PreDirName, "0") }

	t.Run("missing", func(t *testing.T) {
		vault, id := build(t)
		if err := os.Remove(preAbs(vault, id)); err != nil {
			t.Fatalf("删前像失败：%v", err)
		}
		runPreimageFailClosed(t, vault, id)
	})
	t.Run("is_dir", func(t *testing.T) {
		vault, id := build(t)
		p := preAbs(vault, id)
		_ = os.Remove(p)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatalf("造目录失败：%v", err)
		}
		runPreimageFailClosed(t, vault, id)
	})
	t.Run("is_symlink", func(t *testing.T) {
		vault, id := build(t)
		p := preAbs(vault, id)
		_ = os.Remove(p)
		// 指向本事务目录内的 intent.json：路径校验通过（未逃逸），由前像 Lstat 的 symlink 判据拒绝。
		if err := os.Symlink(filepath.Join(TxnDirPath(vault, id), IntentFileName), p); err != nil {
			t.Fatalf("造 symlink 失败：%v", err)
		}
		runPreimageFailClosed(t, vault, id)
	})
	t.Run("truncated_size", func(t *testing.T) {
		vault, id := build(t)
		writeRaw(t, preAbs(vault, id), []byte("A0")) // size 与 pre_size 不符
		runPreimageFailClosed(t, vault, id)
	})
	t.Run("tampered_hash", func(t *testing.T) {
		vault, id := build(t)
		// size 相同但内容被篡改（同 11 字节）。
		writeRaw(t, preAbs(vault, id), []byte("XX-preimage\n"[:len("A0-preimage\n")]))
		runPreimageFailClosed(t, vault, id)
	})
}

func runPreimageFailClosed(t *testing.T, vault, id string) {
	t.Helper()
	before := snapshotTree(t, vault)
	_, err := Recover(vault)
	assertE15(t, err)
	var pe *PreimageUnavailableError
	if !errors.As(err, &pe) {
		t.Fatalf("应为 PreimageUnavailableError，得 %T", err)
	}
	// 权威文件保持目标态未被回滚（未进入 Pass B），无 abort。
	if string(readAuth(t, vault, "a.md")) != "A1\n" {
		t.Fatalf("前像不可信时不应写回权威文件")
	}
	if markerPresent(t, vault, id, AbortMarker) {
		t.Fatalf("前像不可信时不应写 abort")
	}
	if snapshotTree(t, vault) != before {
		t.Fatalf("前像不可信 ⇒ 整体零写入")
	}
}

// —— 多 OpenTxn / OpenTxn + CorruptTxn 并存 ⇒ 在任何回滚写之前整体阻断（E15），两事务都不闭合。
func TestMultipleOpenTxnFailsClosedBeforeWrites(t *testing.T) {
	t.Run("two_open", func(t *testing.T) {
		vault := newVault(t)
		f1 := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
		f2 := []wsFile{{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")}}
		id1 := makeOpenTxnWithSet(t, vault, f1)
		id2 := makeOpenTxnWithSet(t, vault, f2)
		setAuth(t, vault, "a.md", f1[0].target) // 本可回滚
		setAuth(t, vault, "b.md", f2[0].target) // 本可回滚
		before := snapshotTree(t, vault)
		_, err := Recover(vault)
		assertE15(t, err)
		var be *ScanBlockedError
		if !errors.As(err, &be) {
			t.Fatalf("应为 ScanBlockedError，得 %T", err)
		}
		if markerPresent(t, vault, id1, AbortMarker) || markerPresent(t, vault, id2, AbortMarker) {
			t.Fatalf("整体阻断：任一事务都不应被恢复")
		}
		if snapshotTree(t, vault) != before {
			t.Fatalf("整体阻断 ⇒ 零写入，磁盘不变")
		}
	})
	t.Run("open_plus_corrupt_plus_residue", func(t *testing.T) {
		vault := newVault(t)
		f1 := []wsFile{{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")}}
		id1 := makeOpenTxnWithSet(t, vault, f1)
		setAuth(t, vault, "a.md", f1[0].target)
		// CorruptTxn。
		_, cdir := mkTxnDir(t, vault, 100)
		writeRaw(t, filepath.Join(cdir, IntentFileName), []byte(`{"broken":`))
		// residue（不应被清理）。
		idRes, _ := mkTxnDir(t, vault, 200)
		before := snapshotTree(t, vault)
		_, err := Recover(vault)
		assertE15(t, err)
		if markerPresent(t, vault, id1, AbortMarker) {
			t.Fatalf("存在 Corrupt ⇒ 可回滚事务也不得恢复")
		}
		if _, serr := os.Stat(TxnDirPath(vault, idRes)); errors.Is(serr, os.ErrNotExist) {
			t.Fatalf("整体阻断时 residue 也不得被清理")
		}
		if snapshotTree(t, vault) != before {
			t.Fatalf("整体阻断 ⇒ 零写入，磁盘不变")
		}
	})
}

// rawIntentJSON 拼一个单文件 intent.json（测试构造用；version=1、含必需字段）。
func rawIntentJSON(txnID, fileObj string) []byte {
	return []byte(`{"txn_id":"` + txnID + `","started_at":"2027-02-01T00:00:00Z","argv":["eg","apply"],` +
		`"files":[` + fileObj + `],"skipped":[],"git":{"expect_commit":false},"journal_version":1}`)
}

// placeStageTemp 在文件 rel 的权威目录里放一个**本事务该下标的确定性 commit 备料临时文件**，
// 用于模拟「崩溃在备料后 / 部分 rename 后」在权威目录遗留的 stage tmp（P0-1）。
func placeStageTemp(t *testing.T, vault, id string, index int, rel string, data []byte) {
	t.Helper()
	name := stageTempName(id, index, stagePhaseCommit)
	writeRaw(t, filepath.Join(vault, filepath.Dir(rel), name), data)
}

// txnTmpsLeft 返回 vault 子树内仍残留的、属于本事务的临时文件（前缀 .eg-txn-<id>-）。
func txnTmpsLeft(t *testing.T, vault, id string) []string {
	t.Helper()
	prefix := ".eg-txn-" + id + "-"
	var found []string
	err := filepath.Walk(vault, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasPrefix(filepath.Base(p), prefix) {
			found = append(found, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 vault 失败：%v", err)
	}
	return found
}

// —— P0-1：崩溃后遗留在权威目录的 commit 备料临时文件必须被 Recover 一并清理 ——
// 此前临时名是随机后缀、Recover 无从推导，导致 P2/P3 回滚后仍残留 .eg-txn-*.tmp、并非完整收敛。
// 现临时名可由 intent 下标唯一推导，Pass A 只读求路径、Pass B 静默清理；恢复后不得残留任何本事务 tmp。
func TestRecoverCleansLeftoverStageTemps(t *testing.T) {
	// P2：全部文件仍是前像/不存在，但每个下标都遗留了 stage tmp（崩溃在全部 fsync 后、任何 rename 前）。
	t.Run("P2_all_staged_no_rename", func(t *testing.T) {
		vault := newVault(t)
		files := []wsFile{
			{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
			{path: "sub/b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
			{path: "n.md", create: true, target: []byte("N1\n")},
		}
		id := makeOpenTxnWithSet(t, vault, files)
		for i, f := range files {
			placeStageTemp(t, vault, id, i, f.path, f.target)
		}
		res, err := Recover(vault)
		if err != nil {
			t.Fatalf("P2 恢复失败：%v", err)
		}
		if res.Outcome != RecoverRolledBack || !hasW26(res) {
			t.Fatalf("P2 应回滚并发 W26，得 %+v", res)
		}
		assertMatrixRolledBack(t, vault, id, files)
		if left := txnTmpsLeft(t, vault, id); len(left) != 0 {
			t.Fatalf("P2 恢复后不得残留本事务 stage tmp，得 %v", left)
		}
	})

	// P3：a 已 rename（tmp 已被消费）、b 仍备料未 rename（残留 stage tmp）、n 新建已 rename。
	t.Run("P3_partial_rename_with_residual_temp", func(t *testing.T) {
		vault := newVault(t)
		files := []wsFile{
			{path: "a.md", pre: []byte("A0\n"), target: []byte("A1\n")},
			{path: "b.md", pre: []byte("B0\n"), target: []byte("B1\n")},
			{path: "n.md", create: true, target: []byte("N1\n")},
		}
		id := makeOpenTxnWithSet(t, vault, files)
		setAuth(t, vault, "a.md", files[0].target)               // 已 rename
		placeStageTemp(t, vault, id, 1, "b.md", files[1].target) // 仍备料未 rename
		setAuth(t, vault, "n.md", files[2].target)               // 新建已 rename
		res, err := Recover(vault)
		if err != nil {
			t.Fatalf("P3 恢复失败：%v", err)
		}
		if res.Outcome != RecoverRolledBack || !hasW26(res) {
			t.Fatalf("P3 应回滚并发 W26，得 %+v", res)
		}
		assertMatrixRolledBack(t, vault, id, files)
		if left := txnTmpsLeft(t, vault, id); len(left) != 0 {
			t.Fatalf("P3 恢复后不得残留本事务 stage tmp，得 %v", left)
		}
	})
}

// —— P1：Recover 的写前阻断错误必须携 E15/Diagnostics（供 074 映射）——类型断言（含读盘失败错误）。
func TestRecoverWriteBlockErrorsAreCodedE15(t *testing.T) {
	// RecoverReadError 的 coded 契约：Code=E15、Diagnostics 指向出错 path、Unwrap 可达底层原因。
	err := error(&RecoverReadError{TxnID: "t0000000000000001", Path: "a.md", Err: errors.New("boom")})
	var c coded
	if !errors.As(err, &c) || c.Code() != CodePrecheckFailed {
		t.Fatalf("RecoverReadError 应携 E15，得 %T/%v", err, err)
	}
	if d := c.Diagnostics(); len(d) != 1 || d[0].Code != CodePrecheckFailed || d[0].Path != "a.md" {
		t.Fatalf("应提供指向 path 的一条 E15 Diagnostics，得 %+v", d)
	}
	if !errors.Is(err, err.(*RecoverReadError).Err) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Unwrap 链应可达底层原因")
	}
}
