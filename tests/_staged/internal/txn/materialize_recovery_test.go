package txn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func materializeTxnSet(targets int) []wsFile {
	files := make([]wsFile, 0, targets+1)
	for i := 0; i < targets; i++ {
		dir, prefix := "knowledge", "k"
		if i%2 == 1 {
			dir, prefix = "opinions", "o"
		}
		rel := fmt.Sprintf("domains/ai-infra/%s/%s-20260922-%02d.md", dir, prefix, i)
		files = append(files, wsFile{
			path: rel, create: true,
			target: []byte(fmt.Sprintf("---\nid: %s-20260922-%02d\n---\n\nbody-%d\n",
				prefix, i, i)),
		})
	}
	files = append(files, wsFile{
		path:   "domains/ai-infra/notes/n-20260922-source.md",
		pre:    []byte("note-before\n"),
		target: []byte("note-after-with-output-mappings\n"),
	})
	return files
}

func assertMaterializePreimage(t *testing.T, vault string, files []wsFile) {
	t.Helper()
	for _, file := range files {
		if file.create {
			if authExists(t, vault, file.path) {
				t.Fatalf("新建目标回滚后仍存在：%s", file.path)
			}
			continue
		}
		if got := readAuth(t, vault, file.path); string(got) != string(file.pre) {
			t.Fatalf("%s 未回到前像：want=%q got=%q", file.path, file.pre, got)
		}
	}
}

func TestMaterializeIntentV1ForTwoAndNFiles(t *testing.T) {
	for _, targets := range []int{1, 4} {
		t.Run(fmt.Sprintf("%d_targets_plus_note", targets), func(t *testing.T) {
			vault := newVault(t)
			files := materializeTxnSet(targets)
			id := makeOpenTxnWithSet(t, vault, files)
			scan, err := Scan(vault)
			if err != nil {
				t.Fatal(err)
			}
			var intent *Intent
			for _, entry := range scan.Entries {
				if entry.TxnID == id {
					intent = entry.Intent
				}
			}
			if intent == nil || intent.JournalVersion != 1 ||
				len(intent.Files) != targets+1 {
				t.Fatalf("materialize intent 形态错误：%+v", intent)
			}
			for i, file := range files {
				if intent.Files[i].Path != file.path ||
					intent.Files[i].Create != file.create {
					t.Fatalf("intent.files[%d] 未保留 write-set：%+v", i, intent.Files[i])
				}
			}
		})
	}
}

func TestMaterializeCommitFailpointsConverge(t *testing.T) {
	cases := []struct {
		name string
		hit  func(step string, index int) bool
		open bool
	}{
		{name: "staged", hit: func(step string, _ int) bool {
			return step == fpCommitAfterStage
		}},
		{name: "partial_rename", hit: func(step string, index int) bool {
			return step == fpCommitBeforeRename && index == 2
		}},
		{name: "all_renamed", hit: func(step string, _ int) bool {
			return step == fpCommitAfterAllRenames
		}},
		{name: "before_marker_crash", open: true, hit: func(step string, _ int) bool {
			return step == fpCommitBeforeMarker
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			files := materializeTxnSet(4)
			id := makeOpenTxnWithSet(t, vault, files)
			boom := errors.New("materialize failpoint")
			setCommitFailpoint(t, func(step string, index int) error {
				if tc.hit(step, index) {
					return boom
				}
				return nil
			})
			result, err := Commit(vault, id, commitInputFor(files))
			if !errors.Is(err, boom) {
				t.Fatalf("应命中 failpoint：%v", err)
			}
			if tc.open {
				if result != nil {
					t.Fatalf("marker 前崩溃应保持 open：%+v", result)
				}
				setCommitFailpoint(t, nil)
				recovered, err := Recover(vault)
				if err != nil {
					t.Fatal(err)
				}
				if recovered.Outcome != RecoverRolledBack {
					t.Fatalf("首次恢复应回滚 open materialize：%+v", recovered)
				}
			} else if result == nil || !result.RolledBack {
				t.Fatalf("主动提交失败应完整回滚：%+v", result)
			}
			assertMaterializePreimage(t, vault, files)
			if !markerPresent(t, vault, id, AbortMarker) ||
				markerPresent(t, vault, id, CommitMarker) {
				t.Fatal("失败收敛后应只有 abort 标记")
			}
			if left := txnTmpsLeft(t, vault, id); len(left) != 0 {
				t.Fatalf("不得遗留 materialize 临时文件：%v", left)
			}
		})
	}
}

func TestMaterializeCrashStatesRecoverTwoPass(t *testing.T) {
	cases := []struct {
		name  string
		crash func(t *testing.T, vault, id string, files []wsFile)
	}{
		{name: "staged_only", crash: func(t *testing.T, vault, id string, files []wsFile) {
			for i, file := range files {
				placeStageTemp(t, vault, id, i, file.path, file.target)
			}
		}},
		{name: "partial_rename", crash: func(t *testing.T, vault, _ string, files []wsFile) {
			for _, file := range files[:2] {
				setAuth(t, vault, file.path, file.target)
			}
		}},
		{name: "all_renamed_without_marker", crash: func(t *testing.T, vault, _ string, files []wsFile) {
			for _, file := range files {
				setAuth(t, vault, file.path, file.target)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := newVault(t)
			files := materializeTxnSet(4)
			id := makeOpenTxnWithSet(t, vault, files)
			tc.crash(t, vault, id, files)
			result, err := Recover(vault)
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != RecoverRolledBack || !hasW26(result) {
				t.Fatalf("materialize 崩溃恢复应留 W26：%+v", result)
			}
			assertMaterializePreimage(t, vault, files)
			if left := txnTmpsLeft(t, vault, id); len(left) != 0 {
				t.Fatalf("恢复后不得遗留 materialize 临时文件：%v", left)
			}
		})
	}
}

func TestMaterializeRecoveryInterruptionResumes(t *testing.T) {
	vault := newVault(t)
	files := materializeTxnSet(4)
	id := makeOpenTxnWithSet(t, vault, files)
	for _, file := range files {
		setAuth(t, vault, file.path, file.target)
	}
	boom := errors.New("rollback interrupted")
	setRecoverFailpoint(t, func(step string, index int) error {
		if step == fpRecoverBeforeQuarantine && index == 1 {
			return boom
		}
		return nil
	})
	if _, err := Recover(vault); !errors.Is(err, boom) {
		t.Fatalf("应在第二个新建目标回滚前中断：%v", err)
	}
	if markerPresent(t, vault, id, AbortMarker) {
		t.Fatal("回滚未完成前不得写 abort")
	}
	setRecoverFailpoint(t, nil)
	result, err := Recover(vault)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != RecoverRolledBack {
		t.Fatalf("重入恢复未闭合：%+v", result)
	}
	assertMaterializePreimage(t, vault, files)
}

func TestMaterializePostCrashExternalEditFailsClosed(t *testing.T) {
	vault := newVault(t)
	files := materializeTxnSet(2)
	id := makeOpenTxnWithSet(t, vault, files)
	setAuth(t, vault, files[0].path, files[0].target)
	note := files[len(files)-1]
	external := []byte("post-crash external note edit\n")
	setAuth(t, vault, note.path, external)
	before := snapshotTree(t, vault)
	_, err := Recover(vault)
	var conflict *RecoverConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("应因 Note 外部编辑 E15 fail closed：%T %v", err, err)
	}
	if snapshotTree(t, vault) != before {
		t.Fatal("B-R3 必须整事务零写入")
	}
	if markerPresent(t, vault, id, AbortMarker) {
		t.Fatal("冲突事务不得写 abort")
	}
	if got := readAuth(t, vault, note.path); string(got) != string(external) {
		t.Fatal("恢复覆盖了 post-crash 外部编辑")
	}
}

func TestMaterializeWriteIntentFailpointsKeepAuthorityUntouched(t *testing.T) {
	for _, point := range []string{
		fpBeforePre, fpAfterPre, fpAfterPreFsync,
		fpAfterTmp, fpBeforeRename, fpAfterRename,
	} {
		t.Run(point, func(t *testing.T) {
			vault := newVault(t)
			files := materializeTxnSet(1)
			for _, file := range files {
				if !file.create {
					setAuth(t, vault, file.path, file.pre)
				}
			}
			id, err := AllocateTxnID(vault)
			if err != nil {
				t.Fatal(err)
			}
			specs := make([]FileSpec, 0, len(files))
			for _, file := range files {
				specs = append(specs, FileSpec{
					Path: file.path, Create: file.create, PreBytes: file.pre,
					TargetBytes: file.target, TargetOp: opFor(file.create),
				})
			}
			boom := errors.New("intent failpoint")
			setFailpoint(t, func(step string) error {
				if step == point {
					return boom
				}
				return nil
			})
			if _, err := WriteIntent(vault, id, IntentInput{
				Argv: []string{"eg", "materialize"}, Files: specs,
			}); !errors.Is(err, boom) {
				t.Fatalf("应命中 %s：%v", point, err)
			}
			assertMaterializePreimage(t, vault, files)
			if authExists(t, vault, files[0].path) {
				t.Fatal("intent 屏障阶段不得创建 materialize 目标")
			}
			if point == fpAfterRename {
				setFailpoint(t, nil)
				if _, err := Recover(vault); err != nil {
					t.Fatal(err)
				}
				if !markerPresent(t, vault, id, AbortMarker) {
					t.Fatal("已发布 intent 应由恢复闭合为 abort")
				}
			} else {
				if _, err := os.Stat(filepath.Join(TxnDirPath(vault, id), IntentFileName)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rename 前不得发布 intent.json：%v", err)
				}
			}
		})
	}
}
