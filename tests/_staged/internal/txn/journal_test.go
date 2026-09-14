package txn

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteIntentPublishesAtomically：WriteIntent 成功后 intent.json 已发布（无 .tmp 残留），
// pre/<n> 备料齐全、哈希正确；create:true 项无 pre 副本、无前像引用。
func TestWriteIntentPublishesAtomically(t *testing.T) {
	vault := newVault(t)
	id, err := AllocateTxnID(vault)
	if err != nil {
		t.Fatal(err)
	}
	in := IntentInput{
		Argv:         []string{"eg", "edit"},
		ExpectCommit: true,
		Files: []FileSpec{
			{Path: "notes/a.md", Create: false, PreBytes: []byte("old-a"), TargetBytes: []byte("new-a"), TargetOp: "replace"},
			{Path: "notes/b.md", Create: true, TargetBytes: []byte("brand-b"), TargetOp: "create"},
		},
	}
	intent, err := WriteIntent(vault, id, in)
	if err != nil {
		t.Fatalf("WriteIntent 失败：%v", err)
	}
	dir := TxnDirPath(vault, id)

	// intent.json 已发布且可解析。
	if _, err := os.Stat(filepath.Join(dir, IntentFileName)); err != nil {
		t.Fatalf("intent.json 应已发布：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, intentTmpName)); !os.IsNotExist(err) {
		t.Fatalf("intent.json.tmp 不应残留：%v", err)
	}
	if intent.TxnID != id || intent.JournalVersion != SupportedJournalVersion || len(intent.Files) != 2 {
		t.Fatalf("intent 内容异常：%+v", intent)
	}

	// files[0]（replace）：pre/0 落盘且哈希匹配。
	if got := string(mustReadFile(t, filepath.Join(dir, PreDirName, "0"))); got != "old-a" {
		t.Fatalf("pre/0 内容应为 old-a，得 %q", got)
	}
	if intent.Files[0].PreHash != HashBytes([]byte("old-a")) {
		t.Fatalf("files[0].pre_hash 不匹配")
	}
	if intent.Files[0].PreBytesRef != PreDirName+"/0" {
		t.Fatalf("files[0].pre_bytes_ref 应为 pre/0，得 %q", intent.Files[0].PreBytesRef)
	}
	// files[1]（create）：无 pre 副本、无前像引用。
	if _, err := os.Stat(filepath.Join(dir, PreDirName, "1")); !os.IsNotExist(err) {
		t.Fatalf("create:true 不应有 pre/1：%v", err)
	}
	if !intent.Files[1].Create || intent.Files[1].PreBytesRef != "" || intent.Files[1].PreHash != "" {
		t.Fatalf("files[1] 形态异常：%+v", intent.Files[1])
	}
}

// TestIntentWrittenBeforeAnyAuthoritativeWrite：txn 包只写 .index/txn 内，
// 绝不触碰权威文件；成功返回即表示屏障（intent.json + pre）已就绪。
func TestIntentWrittenBeforeAnyAuthoritativeWrite(t *testing.T) {
	vault := newVault(t)
	authoritative := filepath.Join(vault, "notes", "a.md")
	writeRaw(t, authoritative, []byte("AUTH-OLD"))
	before := string(mustReadFile(t, authoritative))

	id, err := AllocateTxnID(vault)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteIntent(vault, id, IntentInput{
		Files: []FileSpec{{Path: "notes/a.md", Create: false, PreBytes: []byte("AUTH-OLD"), TargetBytes: []byte("AUTH-NEW"), TargetOp: "replace"}},
	}); err != nil {
		t.Fatalf("WriteIntent 失败：%v", err)
	}

	if got := string(mustReadFile(t, authoritative)); got != before {
		t.Fatalf("txn 包不得写权威文件：%q → %q", before, got)
	}
	dir := TxnDirPath(vault, id)
	if _, err := os.Stat(filepath.Join(dir, IntentFileName)); err != nil {
		t.Fatalf("屏障后 intent.json 应就绪：%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, PreDirName, "0")); err != nil {
		t.Fatalf("屏障后 pre/0 应就绪：%v", err)
	}
}

// TestPreImageHashesComplete：多个 create:false 项的 pre/<n> 内容与 pre_hash 逐一齐全。
func TestPreImageHashesComplete(t *testing.T) {
	vault := newVault(t)
	id, err := AllocateTxnID(vault)
	if err != nil {
		t.Fatal(err)
	}
	pres := [][]byte{[]byte("p0"), []byte("pre-one"), []byte("third-pre-image")}
	files := make([]FileSpec, len(pres))
	for i, p := range pres {
		files[i] = FileSpec{Path: "d/f" + string(rune('0'+i)) + ".md", Create: false, PreBytes: p, TargetBytes: []byte("t"), TargetOp: "replace"}
	}
	intent, err := WriteIntent(vault, id, IntentInput{Files: files})
	if err != nil {
		t.Fatal(err)
	}
	dir := TxnDirPath(vault, id)
	for i, p := range pres {
		name := filepath.Join(dir, PreDirName, string(rune('0'+i)))
		if got := string(mustReadFile(t, name)); got != string(p) {
			t.Fatalf("pre/%d 应为 %q，得 %q", i, p, got)
		}
		if intent.Files[i].PreHash != HashBytes(p) {
			t.Fatalf("files[%d].pre_hash 不匹配", i)
		}
		if intent.Files[i].PreSize != int64(len(p)) {
			t.Fatalf("files[%d].pre_size 不匹配", i)
		}
	}
}

// TestIntentTargetHashAndCreateRecorded：目标哈希 / 大小 / create 标志与前像引用都如实记录。
func TestIntentTargetHashAndCreateRecorded(t *testing.T) {
	vault := newVault(t)
	id, err := AllocateTxnID(vault)
	if err != nil {
		t.Fatal(err)
	}
	pre := []byte("was-here")
	tgt0 := []byte("replaced-content")
	tgt1 := []byte("created-content")
	intent, err := WriteIntent(vault, id, IntentInput{Files: []FileSpec{
		{Path: "x.md", Create: false, PreBytes: pre, TargetBytes: tgt0, TargetOp: "replace"},
		{Path: "y.md", Create: true, TargetBytes: tgt1, TargetOp: "create"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	f0, f1 := intent.Files[0], intent.Files[1]
	if f0.TargetHash != HashBytes(tgt0) || f0.TargetSize != int64(len(tgt0)) {
		t.Fatalf("files[0] target 记录错误：%+v", f0)
	}
	if f0.PreHash != HashBytes(pre) || f0.Create || f0.PreBytesRef != PreDirName+"/0" {
		t.Fatalf("files[0] pre 记录错误：%+v", f0)
	}
	if f1.TargetHash != HashBytes(tgt1) || !f1.Create || f1.PreHash != "" || f1.PreBytesRef != "" {
		t.Fatalf("files[1] create 记录错误：%+v", f1)
	}
}

// TestCommitMarkerAtomic：MarkCommit 后 commit 为零字节普通文件、无 commit.tmp 残留，Scan 归类 Committed。
func TestCommitMarkerAtomic(t *testing.T) {
	vault := newVault(t)
	id := makeOpen(t, vault)
	if err := MarkCommit(vault, id); err != nil {
		t.Fatal(err)
	}
	dir := TxnDirPath(vault, id)
	fi, err := os.Stat(filepath.Join(dir, CommitMarker))
	if err != nil || fi.Size() != 0 {
		t.Fatalf("commit 应为零字节文件：fi=%v err=%v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(dir, CommitMarker+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("commit.tmp 不应残留：%v", err)
	}
	res, err := Scan(vault)
	if err != nil {
		t.Fatal(err)
	}
	if !hasState(res, id, StateCommitted) {
		t.Fatalf("%s 应归类 Committed", id)
	}
}

// TestAbortMarkerRollsBack：MarkAbort 后 Scan 归类 Aborted。
func TestAbortMarkerRollsBack(t *testing.T) {
	vault := newVault(t)
	id := makeOpen(t, vault)
	if err := MarkAbort(vault, id); err != nil {
		t.Fatal(err)
	}
	res, err := Scan(vault)
	if err != nil {
		t.Fatal(err)
	}
	if !hasState(res, id, StateAborted) {
		t.Fatalf("%s 应归类 Aborted", id)
	}
}

// TestJournalRetentionPolicy：保留策略按「最近 N 个」与「7 天」两条独立门限清理闭合事务。
func TestJournalRetentionPolicy(t *testing.T) {
	t.Run("keep_recent_n", func(t *testing.T) {
		vault := newVault(t)
		var ids []string
		for i := 0; i < 5; i++ {
			ids = append(ids, makeCommitted(t, vault))
		}
		removed, err := Prune(vault, PruneOptions{MaxKeep: 2, MaxAge: 1000 * time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if len(removed) != 3 {
			t.Fatalf("应移除最旧 3 个，得 %d：%v", len(removed), removed)
		}
		res, _ := Scan(vault)
		for _, keep := range ids[3:] {
			if !existsEntry(res, keep) {
				t.Fatalf("最近的 %s 应保留", keep)
			}
		}
		for _, gone := range ids[:3] {
			if existsEntry(res, gone) {
				t.Fatalf("最旧的 %s 应被移除", gone)
			}
		}
	})

	t.Run("drop_by_age", func(t *testing.T) {
		vault := newVault(t)
		idOld := makeCommitted(t, vault)
		idNew := makeCommitted(t, vault)
		old := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(filepath.Join(TxnDirPath(vault, idOld), CommitMarker), old, old); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		removed, err := Prune(vault, PruneOptions{MaxAge: 24 * time.Hour, MaxKeep: 100, Now: func() time.Time { return now }})
		if err != nil {
			t.Fatal(err)
		}
		if !contains(removed, idOld) {
			t.Fatalf("超龄的 %s 应被移除，removed=%v", idOld, removed)
		}
		if contains(removed, idNew) {
			t.Fatalf("新鲜的 %s 不应被移除", idNew)
		}
	})
}

// TestPreIntentResidueIsNotATransaction：只有 pre/ 而无 intent.json 的目录是 residue，
// 不阻断、可被 Prune 静默清理。
func TestPreIntentResidueIsNotATransaction(t *testing.T) {
	vault := newVault(t)
	id, dir := mkTxnDir(t, vault, 7)
	writeRaw(t, filepath.Join(dir, PreDirName, "0"), []byte("orphan-pre"))

	res, err := Scan(vault)
	if err != nil {
		t.Fatal(err)
	}
	if !hasState(res, id, StateResidue) {
		t.Fatalf("%s 应归类 Residue", id)
	}
	if len(res.Open()) != 0 || len(res.Corrupt()) != 0 {
		t.Fatalf("residue 不应计入 Open/Corrupt：open=%d corrupt=%d", len(res.Open()), len(res.Corrupt()))
	}
	if res.Blocked() != nil {
		t.Fatalf("residue 不应阻断：%v", res.Blocked())
	}
	removed, err := Prune(vault, PruneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(removed, id) {
		t.Fatalf("residue %s 应被清理，removed=%v", id, removed)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("residue 目录应被删除：%v", err)
	}
}

// TestCorruptIntentScanFailsClosed：intent.json 在盘但不可解析 / 版本不支持时归类 Corrupt，
// Scan 零写、Blocked 携 E15，Prune 阻断且不删任何目录。
func TestCorruptIntentScanFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		body func(id string) []byte
	}{
		{"truncated_json", func(id string) []byte { return []byte(`{"txn_id":"` + id) }},
		{"illegal_json", func(string) []byte { return []byte("this is not json") }},
		{"unsupported_version", func(id string) []byte {
			return []byte(`{"txn_id":"` + id + `","journal_version":2,"files":[]}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			id, dir := mkTxnDir(t, vault, 3)
			intentPath := filepath.Join(dir, IntentFileName)
			writeRaw(t, intentPath, tc.body(id))
			before := snapshotTree(t, TxnRootPath(vault))

			res, err := Scan(vault)
			if err != nil {
				t.Fatal(err)
			}
			if !hasState(res, id, StateCorrupt) {
				t.Fatalf("%s 应归类 Corrupt", id)
			}
			blocked := res.Blocked()
			if blocked == nil {
				t.Fatal("存在 Corrupt 时 Blocked() 应非 nil")
			}
			var sb *ScanBlockedError
			if !errors.As(blocked, &sb) || sb.Code() != CodePrecheckFailed {
				t.Fatalf("Blocked 应为携 E15 的 *ScanBlockedError，得 %T", blocked)
			}
			if after := snapshotTree(t, TxnRootPath(vault)); after != before {
				t.Fatal("Scan 必须零写盘（树快照不应变化）")
			}

			_, perr := Prune(vault, PruneOptions{})
			if !errors.As(perr, &sb) {
				t.Fatalf("Prune 遇 Corrupt 应返回 *ScanBlockedError，得 %v", perr)
			}
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("Prune 阻断时不应删除任何目录：%v", err)
			}
		})
	}
}

// TestValidateIntentPathsRejectsEscapes：绝对路径 / .. / 未规范化 / 越界前像引用 / symlink 逃逸
// 全部拒为 *PathViolationError，且不落任何字节。
func TestValidateIntentPathsRejectsEscapes(t *testing.T) {
	id := FormatTxnID(9)
	base := func(files []IntentFile) *Intent {
		return &Intent{TxnID: id, Files: files, JournalVersion: SupportedJournalVersion}
	}
	cases := []struct {
		name   string
		intent *Intent
	}{
		{"absolute", base([]IntentFile{{Path: "/etc/passwd", Create: true}})},
		{"dotdot", base([]IntentFile{{Path: "../outside.md", Create: true}})},
		{"unnormalized", base([]IntentFile{{Path: "a/./b.md", Create: true}})},
		{"pre_ref_escape", base([]IntentFile{{Path: "n.md", Create: false, PreHash: "sha256:x", PreBytesRef: "../x"}})},
		{"create_with_preref", base([]IntentFile{{Path: "n.md", Create: true, PreBytesRef: "pre/0"}})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			err := ValidateIntentPaths(vault, id, tc.intent)
			var pe *PathViolationError
			if !errors.As(err, &pe) {
				t.Fatalf("应为 *PathViolationError，得 %T：%v", err, err)
			}
			if _, statErr := os.Stat(TxnDirPath(vault, id)); !os.IsNotExist(statErr) {
				t.Fatalf("校验必须零写（不应创建事务目录）：%v", statErr)
			}
		})
	}

	t.Run("symlink_escape", func(t *testing.T) {
		vault := newVault(t)
		external := t.TempDir()
		if err := os.Symlink(external, filepath.Join(vault, "link")); err != nil {
			t.Fatal(err)
		}
		err := ValidateIntentPaths(vault, id, base([]IntentFile{{Path: "link/x.md", Create: true}}))
		var pe *PathViolationError
		if !errors.As(err, &pe) {
			t.Fatalf("symlink 逃逸应为 *PathViolationError，得 %T：%v", err, err)
		}
	})
}

// TestScanReportsAllEntriesReadOnly：Scan 全量列举（不提前 break），三分类计数准确且全程零写盘。
func TestScanReportsAllEntriesReadOnly(t *testing.T) {
	vault := newVault(t)
	makeOpen(t, vault)
	makeOpen(t, vault)
	_, cdir := mkTxnDir(t, vault, 100)
	writeRaw(t, filepath.Join(cdir, IntentFileName), []byte("broken"))
	mkTxnDir(t, vault, 101) // residue（无 intent.json）

	before := snapshotTree(t, TxnRootPath(vault))
	res, err := Scan(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open()) != 2 {
		t.Fatalf("应有 2 个 Open，得 %d", len(res.Open()))
	}
	if len(res.Corrupt()) != 1 {
		t.Fatalf("应有 1 个 Corrupt，得 %d", len(res.Corrupt()))
	}
	if len(res.Residue()) != 1 {
		t.Fatalf("应有 1 个 Residue，得 %d", len(res.Residue()))
	}
	if after := snapshotTree(t, TxnRootPath(vault)); after != before {
		t.Fatal("Scan 必须零写盘")
	}
}

// TestScanRejectsMalformedEntries（P0）：txn/ 下非目录 / symlink / 非法名目录都必须进入 Corrupt。
func TestScanRejectsMalformedEntries(t *testing.T) {
	vault := newVault(t)
	root := TxnRootPath(vault)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRaw(t, filepath.Join(root, FormatTxnID(5)), []byte("regular-file-with-txn-name"))
	if err := os.Symlink(t.TempDir(), filepath.Join(root, FormatTxnID(6))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "notatxn"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Scan(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Corrupt()) != 3 {
		t.Fatalf("三个畸形条目都应 Corrupt，得 %d：%+v", len(res.Corrupt()), res.Corrupt())
	}
	if res.Blocked() == nil {
		t.Fatal("存在 Corrupt 应整体阻断")
	}
}

// TestPruneBlocksOnCorruptOrOpen（P0）：只要有任一 Corrupt 或任一 Open，Prune 必须先阻断，
// 绝不先删 residue。
func TestPruneBlocksOnCorruptOrOpen(t *testing.T) {
	t.Run("corrupt_plus_residue", func(t *testing.T) {
		vault := newVault(t)
		_, cdir := mkTxnDir(t, vault, 200)
		writeRaw(t, filepath.Join(cdir, IntentFileName), []byte("broken"))
		_, rdir := mkTxnDir(t, vault, 201) // residue

		_, err := Prune(vault, PruneOptions{})
		var sb *ScanBlockedError
		if !errors.As(err, &sb) {
			t.Fatalf("应阻断为 *ScanBlockedError，得 %v", err)
		}
		if _, statErr := os.Stat(rdir); statErr != nil {
			t.Fatalf("阻断时 residue 不应被删：%v", statErr)
		}
	})

	t.Run("open_plus_residue", func(t *testing.T) {
		vault := newVault(t)
		makeOpen(t, vault)
		_, rdir := mkTxnDir(t, vault, 202) // residue

		_, err := Prune(vault, PruneOptions{})
		var sb *ScanBlockedError
		if !errors.As(err, &sb) {
			t.Fatalf("应阻断为 *ScanBlockedError，得 %v", err)
		}
		if _, statErr := os.Stat(rdir); statErr != nil {
			t.Fatalf("阻断时 residue 不应被删：%v", statErr)
		}
	})
}

// TestMarkersRejectTraversal（P0）：非法 txnID 拒写；marker.tmp 预置 symlink 时不得跟随写外部。
func TestMarkersRejectTraversal(t *testing.T) {
	t.Run("invalid_txn_id", func(t *testing.T) {
		vault := newVault(t)
		err := MarkCommit(vault, "../evil")
		var pe *PathViolationError
		if !errors.As(err, &pe) {
			t.Fatalf("非法 txnID 应拒为 *PathViolationError，得 %T：%v", err, err)
		}
	})

	t.Run("symlink_marker_tmp", func(t *testing.T) {
		vault := newVault(t)
		id, err := AllocateTxnID(vault)
		if err != nil {
			t.Fatal(err)
		}
		dir := TxnDirPath(vault, id)
		external := filepath.Join(t.TempDir(), "victim")
		const secret = "KEEP-ME"
		writeRaw(t, external, []byte(secret))
		if err := os.Symlink(external, filepath.Join(dir, CommitMarker+".tmp")); err != nil {
			t.Fatal(err)
		}
		err = MarkCommit(vault, id)
		var pe *PathViolationError
		if !errors.As(err, &pe) {
			t.Fatalf("marker.tmp 为 symlink 应拒为 *PathViolationError，得 %T：%v", err, err)
		}
		if got := string(mustReadFile(t, external)); got != secret {
			t.Fatalf("symlink 目标被改写：%q → %q", secret, got)
		}
	})
}

// ---- 小工具 ----

func hasState(res ScanResult, id string, st TxnState) bool {
	for _, e := range res.Entries {
		if e.TxnID == id {
			return e.State == st
		}
	}
	return false
}

func existsEntry(res ScanResult, id string) bool {
	for _, e := range res.Entries {
		if e.TxnID == id {
			return true
		}
	}
	return false
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
