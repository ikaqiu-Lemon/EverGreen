package txn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestWriteIntentEmitsNonNilArrays：WriteIntent 对 nil 的 argv/files/skipped 也必须落成非 nil 空数组 `[]`，
// 绝不写 `null`；产出的 intent 必须能被自身 Scan 正常判为 Open（不因 null 数组被误判 Corrupt）。
func TestWriteIntentEmitsNonNilArrays(t *testing.T) {
	vault := newVault(t)
	id, err := AllocateTxnID(vault)
	if err != nil {
		t.Fatalf("分配 txn_id 失败：%v", err)
	}
	// 全 nil 输入（无 argv / 无 files / 无 skipped）。
	if _, err := WriteIntent(vault, id, IntentInput{}); err != nil {
		t.Fatalf("WriteIntent 空输入应成功：%v", err)
	}
	raw := mustReadFile(t, filepath.Join(TxnDirPath(vault, id), IntentFileName))
	var probe struct {
		Argv    *json.RawMessage `json:"argv"`
		Files   *json.RawMessage `json:"files"`
		Skipped *json.RawMessage `json:"skipped"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("解析 intent.json 失败：%v", err)
	}
	for name, rm := range map[string]*json.RawMessage{"argv": probe.Argv, "files": probe.Files, "skipped": probe.Skipped} {
		got := "<缺席>"
		if rm != nil {
			got = strings.TrimSpace(string(*rm))
		}
		if got != "[]" {
			t.Fatalf("%s 应序列化为非 nil 空数组 []，实得 %q", name, got)
		}
	}
	res, err := Scan(vault)
	if err != nil {
		t.Fatalf("Scan 失败：%v", err)
	}
	if !hasState(res, id, StateOpen) {
		t.Fatalf("空数组 intent 应判 Open，未命中（不得因 [] 误判 Corrupt）")
	}
}

// TestValidateIntentStructureRejectsNullAndBadArrays：表驱动断言 argv/files/skipped
// 必须是真数组（拒 null / 对象 / 标量），且 skipped[] 每项 path 必需且规范化、kind 限封闭两值。
func TestValidateIntentStructureRejectsNullAndBadArrays(t *testing.T) {
	vault := newVault(t)
	id := FormatTxnID(42)
	base := func() map[string]any {
		return map[string]any{
			"txn_id":          id,
			"started_at":      "2027-01-24T00:00:00Z",
			"argv":            []any{"eg", "edit"},
			"files":           []any{},
			"skipped":         []any{},
			"git":             map[string]any{"expect_commit": false},
			"journal_version": 1,
		}
	}
	marshal := func(m map[string]any) []byte {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal 失败：%v", err)
		}
		return b
	}

	// 基准必须合法（否则后续 mutate 断言无意义）。
	if _, err := validateIntentStructure(vault, id, marshal(base())); err != nil {
		t.Fatalf("基准 intent 应合法：%v", err)
	}

	bad := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"argv_null", func(m map[string]any) { m["argv"] = nil }},
		{"argv_object", func(m map[string]any) { m["argv"] = map[string]any{} }},
		{"argv_scalar", func(m map[string]any) { m["argv"] = "eg" }},
		{"files_null", func(m map[string]any) { m["files"] = nil }},
		{"files_object", func(m map[string]any) { m["files"] = map[string]any{} }},
		{"files_scalar", func(m map[string]any) { m["files"] = 3 }},
		{"skipped_null", func(m map[string]any) { m["skipped"] = nil }},
		{"skipped_object", func(m map[string]any) { m["skipped"] = map[string]any{} }},
		{"skipped_scalar", func(m map[string]any) { m["skipped"] = "x" }},
		{"skipped_missing_path", func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"kind": SkipKindFileChanged}}
		}},
		{"skipped_empty_path", func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"path": "", "kind": SkipKindFileChanged}}
		}},
		{"skipped_missing_kind", func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"path": "notes/a.md"}}
		}},
		{"skipped_bad_kind", func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"path": "notes/a.md", "kind": "weird"}}
		}},
		{"skipped_unnormalized_path", func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"path": "a/./b.md", "kind": SkipKindFileChanged}}
		}},
		{"skipped_absolute_path", func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"path": "/etc/passwd", "kind": SkipKindFileChanged}}
		}},
		{"skipped_dotdot_path", func(m map[string]any) {
			m["skipped"] = []any{map[string]any{"path": "../escape.md", "kind": SkipKindUserBlockUnsafe}}
		}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.mutate(m)
			if _, err := validateIntentStructure(vault, id, marshal(m)); err == nil {
				t.Fatalf("%s 应判结构非法，却通过校验", tc.name)
			}
		})
	}

	// 反例：skipped 的两个封闭 kind 值都必须被接受。
	for _, kind := range []string{SkipKindFileChanged, SkipKindUserBlockUnsafe} {
		t.Run("skipped_ok_"+kind, func(t *testing.T) {
			m := base()
			m["skipped"] = []any{map[string]any{"path": "notes/a.md", "kind": kind}}
			if _, err := validateIntentStructure(vault, id, marshal(m)); err != nil {
				t.Fatalf("合法 skipped kind=%s 应通过：%v", kind, err)
			}
		})
	}
}

// TestMarkerStateAtClassifiesOpenErrors：表驱动断言 markerStateAt 的缺席判据与零字节合同——
// 只有 ENOENT 才算「明确缺席」；合法零字节普通文件才 ok；普通但非零字节、symlink(ELOOP) /
// 目录 / FIFO 特殊文件 / 其他 open 错误（用超长名触发 ENAMETOOLONG，root 环境下也稳定）
// 一律 present + !ok，交由 classify 判 Corrupt。
func TestMarkerStateAtClassifiesOpenErrors(t *testing.T) {
	vault := newVault(t)
	dir := filepath.Join(vault, "txndir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	writeRaw(t, filepath.Join(dir, "reg"), nil)             // 合法零字节标记 ⇒ ok
	writeRaw(t, filepath.Join(dir, "nonzero"), []byte("x")) // 普通但非零字节 ⇒ present + !ok（Corrupt）
	if err := os.Symlink("reg", filepath.Join(dir, "lnk")); err != nil {
		t.Fatalf("建 symlink 失败：%v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("建子目录失败：%v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644); err != nil {
		t.Fatalf("建 FIFO 失败：%v", err)
	}

	df, err := os.Open(dir)
	if err != nil {
		t.Fatalf("打开目录失败：%v", err)
	}
	defer df.Close()
	fd := int(df.Fd())
	longName := strings.Repeat("a", 300) // > NAME_MAX(255) ⇒ ENAMETOOLONG，非 ENOENT

	cases := []struct {
		name        string
		target      string
		wantPresent bool
		wantOK      bool
	}{
		{"absent_enoent", "does-not-exist", false, false},
		{"regular_zero_byte", "reg", true, true},
		{"regular_nonzero_invalid", "nonzero", true, false},
		{"symlink_eloop", "lnk", true, false},
		{"directory_nonregular", "sub", true, false},
		{"fifo_special", "fifo", true, false},
		{"nametoolong_other_error", longName, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			present, ok := markerStateAt(fd, tc.target)
			if present != tc.wantPresent || ok != tc.wantOK {
				t.Fatalf("markerStateAt(%q)=(present=%v, ok=%v)，期望 (present=%v, ok=%v)",
					tc.target, present, ok, tc.wantPresent, tc.wantOK)
			}
		})
	}
}

// setFailpoint 安装一个包内 WriteIntent 发布屏障注入器并返回复位函数（这些测试不并行，串行安全）。
func setFailpoint(t *testing.T, fn func(step string) error) {
	t.Helper()
	prev := writeIntentFailpoint
	writeIntentFailpoint = fn
	t.Cleanup(func() { writeIntentFailpoint = prev })
}

// TestWriteIntentPublishBarrierFailpoints：逐点枚举发布屏障注入点，反证两条不变量——
//   - rename 之前任一点崩溃：盘上**绝不**出现 final intent.json，Scan 判 Residue；
//   - rename 之后崩溃：intent.json 已发布，即便 WriteIntent 返回错误，发布事实成立，Scan 判 Open（不误判 residue）。
func TestWriteIntentPublishBarrierFailpoints(t *testing.T) {
	replaceInput := func() IntentInput {
		return IntentInput{
			Argv: []string{"eg", "edit", "o.md"},
			Files: []FileSpec{
				{Path: "o.md", Create: false, PreBytes: []byte("old"), TargetBytes: []byte("new"), TargetOp: "replace"},
			},
		}
	}

	// —— rename 之前的注入点：中止 ⇒ 无 final intent.json ⇒ Residue ——
	preRename := []string{fpBeforePre, fpAfterPre, fpAfterPreFsync, fpAfterTmp, fpBeforeRename}
	for _, step := range preRename {
		t.Run("pre_rename_"+step, func(t *testing.T) {
			vault := newVault(t)
			id, err := AllocateTxnID(vault)
			if err != nil {
				t.Fatalf("分配 txn_id 失败：%v", err)
			}
			setFailpoint(t, func(s string) error {
				if s == step {
					return fmt.Errorf("注入崩溃于 %s", s)
				}
				return nil
			})
			if _, werr := WriteIntent(vault, id, replaceInput()); werr == nil {
				t.Fatalf("注入 %s 应使 WriteIntent 返回错误", step)
			}
			intentPath := filepath.Join(TxnDirPath(vault, id), IntentFileName)
			if _, statErr := os.Lstat(intentPath); !os.IsNotExist(statErr) {
				t.Fatalf("注入 %s：rename 前中止不得留下 final intent.json（Lstat=%v）", step, statErr)
			}
			res, err := Scan(vault)
			if err != nil {
				t.Fatalf("Scan 失败：%v", err)
			}
			if !hasState(res, id, StateResidue) {
				t.Fatalf("注入 %s：应判 Residue", step)
			}
		})
	}

	// —— rename 之后的注入点：intent.json 已发布 ⇒ Open（绝不 residue）——
	t.Run("post_rename_after_rename", func(t *testing.T) {
		vault := newVault(t)
		id, err := AllocateTxnID(vault)
		if err != nil {
			t.Fatalf("分配 txn_id 失败：%v", err)
		}
		setFailpoint(t, func(s string) error {
			if s == fpAfterRename {
				return fmt.Errorf("注入崩溃于 %s（发布后）", s)
			}
			return nil
		})
		if _, werr := WriteIntent(vault, id, replaceInput()); werr == nil {
			t.Fatalf("注入 after_rename 应使 WriteIntent 返回错误")
		}
		intentPath := filepath.Join(TxnDirPath(vault, id), IntentFileName)
		if _, statErr := os.Stat(intentPath); statErr != nil {
			t.Fatalf("注入 after_rename：intent.json 必须已发布在盘：%v", statErr)
		}
		res, err := Scan(vault)
		if err != nil {
			t.Fatalf("Scan 失败：%v", err)
		}
		if !hasState(res, id, StateOpen) {
			t.Fatalf("注入 after_rename：发布已完成，应判 Open，绝不 residue")
		}
	})
}

// TestWriteIntentRefusesOverwrite：intent.json 只能被 rename 发布一次。二次 WriteIntent、或事务目录里
// 已被植入同名普通文件 / symlink 时，必须 *IntentExistsError fail closed，且**绝不**改动已有字节。
func TestWriteIntentRefusesOverwrite(t *testing.T) {
	t.Run("republish_regular", func(t *testing.T) {
		vault := newVault(t)
		id := makeOpen(t, vault) // 已发布一份 create:true 的 intent.json
		intentPath := filepath.Join(TxnDirPath(vault, id), IntentFileName)
		before := mustReadFile(t, intentPath)

		_, err := WriteIntent(vault, id, IntentInput{
			Argv:  []string{"eg", "edit", "other.md"},
			Files: []FileSpec{{Path: "other.md", Create: true, TargetBytes: []byte("y"), TargetOp: "create"}},
		})
		var iee *IntentExistsError
		if !errors.As(err, &iee) {
			t.Fatalf("二次发布应为 *IntentExistsError，得 %T：%v", err, err)
		}
		if iee.Code() != CodePrecheckFailed {
			t.Fatalf("IntentExistsError 应携 %s，得 %s", CodePrecheckFailed, iee.Code())
		}
		if after := mustReadFile(t, intentPath); !bytes.Equal(before, after) {
			t.Fatalf("防覆盖失败：intent.json 字节被改动")
		}
	})

	t.Run("existing_symlink", func(t *testing.T) {
		vault := newVault(t)
		id, err := AllocateTxnID(vault)
		if err != nil {
			t.Fatalf("分配 txn_id 失败：%v", err)
		}
		// 在事务目录植入一个名为 intent.json 的 symlink（模拟被投毒 / 残留），指向 vault 外。
		if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), filepath.Join(TxnDirPath(vault, id), IntentFileName)); err != nil {
			t.Fatalf("建 symlink 失败：%v", err)
		}
		_, werr := WriteIntent(vault, id, IntentInput{
			Files: []FileSpec{{Path: "o.md", Create: true, TargetBytes: []byte("x"), TargetOp: "create"}},
		})
		var iee *IntentExistsError
		if !errors.As(werr, &iee) {
			t.Fatalf("已存在 symlink 时应拒为 *IntentExistsError，得 %T：%v", werr, werr)
		}
	})
}

// TestValidateIntentStructureRequiredFields：**完整必需字段**表驱动——从一份合法 intent 出发，
// 逐一击穿顶层字段、files 项字段、create 真假两支的前像合同、started_at UTC、pre_bytes_ref 逐项索引，
// 每个 bad 例都应判非法；good 例（单 create:false / 单 create:true / 双 create:false 索引正确）应通过。
func TestValidateIntentStructureRequiredFields(t *testing.T) {
	vault := newVault(t)
	id := FormatTxnID(77)
	hashA := "sha256:" + strings.Repeat("a", 64)
	hashB := "sha256:" + strings.Repeat("b", 64)

	// create:false 合法文件（索引 idx，pre_bytes_ref=pre/<idx>）。
	cfFile := func(idx int, path string) map[string]any {
		return map[string]any{
			"path": path, "create": false, "target_op": "replace",
			"target_hash": hashA, "target_size": 3,
			"pre_hash": hashB, "pre_size": 3, "pre_bytes_ref": fmt.Sprintf("pre/%d", idx),
		}
	}
	// create:true 合法文件（pre_hash="" / pre_size=0 在场，pre_bytes_ref 省略）。
	ctFile := func(path string) map[string]any {
		return map[string]any{
			"path": path, "create": true, "target_op": "create",
			"target_hash": hashA, "target_size": 1,
			"pre_hash": "", "pre_size": 0,
		}
	}
	base := func(files ...map[string]any) map[string]any {
		fs := make([]any, 0, len(files))
		for _, f := range files {
			fs = append(fs, f)
		}
		return map[string]any{
			"txn_id": id, "started_at": "2027-01-24T00:00:00Z",
			"argv": []any{"eg", "edit"}, "files": fs, "skipped": []any{},
			"git": map[string]any{"expect_commit": true}, "journal_version": 1,
		}
	}
	marshal := func(m map[string]any) []byte {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal 失败：%v", err)
		}
		return b
	}

	// —— good 例：三种合法形态都必须通过 ——
	good := []struct {
		name string
		m    map[string]any
	}{
		{"single_create_false", base(cfFile(0, "notes/a.md"))},
		{"single_create_true", base(ctFile("notes/new.md"))},
		{"two_create_false_correct_index", base(cfFile(0, "notes/a.md"), cfFile(1, "notes/b.md"))},
		{"mixed_create_true_and_false", base(ctFile("notes/new.md"), cfFile(1, "notes/b.md"))},
	}
	for _, tc := range good {
		t.Run("ok_"+tc.name, func(t *testing.T) {
			if _, err := validateIntentStructure(vault, id, marshal(tc.m)); err != nil {
				t.Fatalf("%s 应合法，却判非法：%v", tc.name, err)
			}
		})
	}

	// —— bad 例：每例都必须判非法 ——
	bad := []struct {
		name   string
		mutate func(map[string]any)
	}{
		// 顶层字段
		{"missing_txn_id", func(m map[string]any) { delete(m, "txn_id") }},
		{"txn_id_mismatch", func(m map[string]any) { m["txn_id"] = FormatTxnID(99) }},
		{"txn_id_bad_format", func(m map[string]any) { m["txn_id"] = "not-a-txn-id" }},
		{"missing_started_at", func(m map[string]any) { delete(m, "started_at") }},
		{"started_at_not_rfc3339", func(m map[string]any) { m["started_at"] = "2027/01/24 00:00" }},
		{"started_at_non_utc_offset", func(m map[string]any) { m["started_at"] = "2027-01-24T08:00:00+08:00" }},
		{"missing_git", func(m map[string]any) { delete(m, "git") }},
		{"missing_git_expect_commit", func(m map[string]any) { m["git"] = map[string]any{} }},
		{"missing_journal_version", func(m map[string]any) { delete(m, "journal_version") }},
		{"unsupported_journal_version", func(m map[string]any) { m["journal_version"] = 2 }},
		// files 项通用字段
		{"file_missing_path", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "path")
			m["files"] = []any{f}
		}},
		{"file_missing_create", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "create")
			m["files"] = []any{f}
		}},
		{"file_missing_target_op", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "target_op")
			m["files"] = []any{f}
		}},
		{"file_empty_target_op", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			f["target_op"] = ""
			m["files"] = []any{f}
		}},
		{"file_missing_target_hash", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "target_hash")
			m["files"] = []any{f}
		}},
		{"file_bad_target_hash", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			f["target_hash"] = "sha256:XYZ"
			m["files"] = []any{f}
		}},
		{"file_missing_target_size", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "target_size")
			m["files"] = []any{f}
		}},
		{"file_negative_target_size", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			f["target_size"] = -1
			m["files"] = []any{f}
		}},
		{"duplicate_path", func(m map[string]any) {
			m["files"] = []any{cfFile(0, "notes/a.md"), cfFile(1, "notes/a.md")}
		}},
		// create:true 前像合同
		{"create_true_missing_pre_hash", func(m map[string]any) {
			f := ctFile("notes/new.md")
			delete(f, "pre_hash")
			m["files"] = []any{f}
		}},
		{"create_true_nonempty_pre_hash", func(m map[string]any) {
			f := ctFile("notes/new.md")
			f["pre_hash"] = hashB
			m["files"] = []any{f}
		}},
		{"create_true_missing_pre_size", func(m map[string]any) {
			f := ctFile("notes/new.md")
			delete(f, "pre_size")
			m["files"] = []any{f}
		}},
		{"create_true_nonzero_pre_size", func(m map[string]any) {
			f := ctFile("notes/new.md")
			f["pre_size"] = 5
			m["files"] = []any{f}
		}},
		{"create_true_has_pre_bytes_ref", func(m map[string]any) {
			f := ctFile("notes/new.md")
			f["pre_bytes_ref"] = "pre/0"
			m["files"] = []any{f}
		}},
		// create:false 前像合同
		{"create_false_missing_pre_hash", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "pre_hash")
			m["files"] = []any{f}
		}},
		{"create_false_bad_pre_hash", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			f["pre_hash"] = "deadbeef"
			m["files"] = []any{f}
		}},
		{"create_false_missing_pre_size", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "pre_size")
			m["files"] = []any{f}
		}},
		{"create_false_negative_pre_size", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			f["pre_size"] = -1
			m["files"] = []any{f}
		}},
		{"create_false_missing_pre_bytes_ref", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			delete(f, "pre_bytes_ref")
			m["files"] = []any{f}
		}},
		{"create_false_pre_bytes_ref_prefix_only", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			f["pre_bytes_ref"] = "pre/"
			m["files"] = []any{f}
		}},
		{"create_false_pre_bytes_ref_wrong_index", func(m map[string]any) {
			f := cfFile(0, "notes/a.md")
			f["pre_bytes_ref"] = "pre/5"
			m["files"] = []any{f}
		}},
		{"create_false_second_file_reuses_pre0", func(m map[string]any) {
			f0 := cfFile(0, "notes/a.md")
			f1 := cfFile(1, "notes/b.md")
			f1["pre_bytes_ref"] = "pre/0" // 第二项复用了 pre/0（应为 pre/1）
			m["files"] = []any{f0, f1}
		}},
	}
	for _, tc := range bad {
		t.Run("bad_"+tc.name, func(t *testing.T) {
			m := base(cfFile(0, "notes/a.md"))
			tc.mutate(m)
			if _, err := validateIntentStructure(vault, id, marshal(m)); err == nil {
				t.Fatalf("%s 应判非法，却通过校验", tc.name)
			}
		})
	}
}
