package index_test

// T-…-072 批次 B2b1 的机器判据：`.index/` 与 M6 运行时保留条目（`run.lock` / `txn/`）共存。
//
// 判据来源：M6 合同 §16.5（R6 P0-2 三层处置）+ R8 §18.1（类型违规分层，不是无条件 fail
// closed）+ R9 §19.5（reserved 类型违规的原因已确定为 corrupt，码只能是 W24）。
//
// 本文件只覆盖**名册与只读判定**这一层，逐条对撞四件事：
//
//	① 合法共存    —— `run.lock`（普通文件）+ `txn/`（目录）在盘不算污染，索引仍 healthy；
//	② 语义不漂移  —— 只有合法 reserved 而 `eg.db` 不在 ⇒ 仍是「索引缺失」（W23），不是 corrupt；
//	③ 类型违规    —— 反类型两例 + symlink 两例，逐例判 corrupt + **既有** W24 +
//	                 `unexpected_file`，并由专用入口回传结构化事实；
//	④ 只报不改    —— 违规例下 Inspect 前后 `.index/` 全树（名字 / 类型 / 字节 / 链接目标）
//	                 逐字不变：不重建、不删除、不修复。
//
// 边界（本批次**不做**，各有归属）：删除面两条策略（`purgeNonReserved` / build 失败清理）
// 与 `.index` 根目录的 `os.RemoveAll` 禁令属同 task 后续批次；B 类命令取锁与 `E15` / 退出码 5
// 的进程出口属 T-…-074。因此本文件一个字都不碰 Build / Rebuild / CLI。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// TestIndexInspectAllowsM6RuntimeEntries 是本批次的唯一新增用例（合同点名的测试名）。
func TestIndexInspectAllowsM6RuntimeEntries(t *testing.T) {
	t.Run("名册恰两项且带期望类型", testReservedRosterIsExactlyTwoTyped)
	t.Run("名册返回副本", testReservedRosterReturnsCopy)
	t.Run("合法共存不算污染", testReservedLegalEntriesAreNotPollution)
	t.Run("未创建不算违规", testReservedAbsentIsNotViolation)
	t.Run("runtime-only 无库仍判索引缺失", testReservedRuntimeOnlyStaysMissing)
	t.Run("外部污染仍被判出", testReservedPollutionStillDetected)
	t.Run("类型违规判 corrupt 且只报不改", testReservedTypeViolations)
	t.Run("类型违规优先于索引缺失", testReservedTypeViolationBeatsMissing)
}

// —— ① 名册形态 ——

// testReservedRosterIsExactlyTwoTyped 钉住 RuntimeReservedEntries 恰两项、逐字带类型，
// 且与 AllowedFiles（恰 3 个 DB 文件）**互不相交** —— 两张表永不合并是 R6 的硬要求。
func testReservedRosterIsExactlyTwoTyped(t *testing.T) {
	got := index.RuntimeReservedEntries()
	want := []index.ReservedEntry{
		{Name: "run.lock", Kind: index.EntryRegular},
		{Name: "txn", Kind: index.EntryDir},
	}
	if len(got) != 2 {
		t.Fatalf("RuntimeReservedEntries() = %v，期望恰 2 项", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项 = %+v，期望 %+v（名字与期望类型都逐字钉死）", i+1, got[i], want[i])
		}
	}
	// 期望类型只可能是这两值：symlink / other / absent / unknown 只作为**实测**类型出现。
	for _, e := range got {
		if e.Kind != index.EntryRegular && e.Kind != index.EntryDir {
			t.Fatalf("%s 的期望类型 = %q，只允许 regular / dir", e.Name, e.Kind)
		}
	}
	// 与 DB 白名单不相交：混表会让「恒保留」与「可清扫」两条相反语义共用一张表。
	allowed := map[string]bool{}
	for _, f := range index.AllowedFiles() {
		allowed[f] = true
	}
	for _, e := range got {
		if allowed[e.Name] {
			t.Fatalf("%s 同时出现在 AllowedFiles() 与 RuntimeReservedEntries()：两张表必须正交", e.Name)
		}
	}
}

// testReservedRosterReturnsCopy 反证名册是**单点真源 + 返回副本**：
// 调用方改了自己拿到的那一份，下一次调用仍必须拿到原始名册。
func testReservedRosterReturnsCopy(t *testing.T) {
	first := index.RuntimeReservedEntries()
	first[0] = index.ReservedEntry{Name: "被改坏了", Kind: index.EntryDir}
	first = append(first, index.ReservedEntry{Name: "多出来的", Kind: index.EntryRegular})
	_ = first

	second := index.RuntimeReservedEntries()
	if len(second) != 2 || second[0].Name != "run.lock" || second[0].Kind != index.EntryRegular {
		t.Fatalf("名册被调用方改坏了：第二次调用得到 %+v", second)
	}
}

// —— ② 合法共存 ——

// testReservedLegalEntriesAreNotPollution：健康索引旁边放上合法的锁与日志全树，
// 索引仍 healthy —— 不判 unexpected_file、不判 corrupt、不触发任何重建动作。
func testReservedLegalEntriesAreNotPollution(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	writeLegalRuntimeEntries(t, dir)

	if v, bad := index.InspectRuntimeReserved(dir); bad {
		t.Fatalf("合法 reserved 不该报违规，实际 = %+v", v)
	}
	diag := index.Inspect(dir)
	if !diag.Usable() || diag.Health != index.HealthHealthy {
		t.Fatalf("合法 reserved 在盘时索引仍应 healthy，实际 = %s / %s / %s",
			diag.Health, diag.Reason, diag.Message)
	}
	if diag.Reason == index.ReasonUnexpectedFile {
		t.Fatal("run.lock / txn 被当成了外部污染：R6 要求它们是正交的保留条目")
	}
	if diag.Code != "" {
		t.Fatalf("healthy 时不得带诊断码，实际 = %q", diag.Code)
	}
	// 反向自检：锁与日志确实还在盘上（否则「不算污染」可能是因为它们压根没建出来）。
	assertLegalRuntimeEntriesOnDisk(t, dir)
}

// testReservedAbsentIsNotViolation：锁与日志按需创建，一项都没有时不算违规。
func testReservedAbsentIsNotViolation(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	if v, bad := index.InspectRuntimeReserved(dir); bad {
		t.Fatalf("reserved 未创建不该报违规，实际 = %+v", v)
	}
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("无 reserved 的健康索引仍应 healthy，实际 = %s / %s", diag.Health, diag.Message)
	}
}

// testReservedRuntimeOnlyStaysMissing 是本批次最容易被做坏的一格：
// `.index/` 里**只有**合法的 `run.lock` / `txn/` 而 `eg.db` 不在时，语义必须仍是
// **索引缺失**（W23 / db_file_missing），不得因为「目录里有东西」而升级成 corrupt。
//
// 为什么这条重要：写命令会先建出锁与日志再回写 Markdown，于是「从未建过索引的 vault」
// 从此永远存在一个 runtime-only 的 `.index/`。若这里判 corrupt，全仓读路径的
// W23 / Q5 分布会被一个与索引无关的目录整体改写。
func testReservedRuntimeOnlyStaysMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	writeLegalRuntimeEntries(t, dir)

	diag := index.Inspect(dir)
	if diag.Health != index.HealthMissing || diag.Code != index.CodeIndexMissing {
		t.Fatalf("runtime-only 应判 missing/%s，实际 = %s / %s（%s）",
			index.CodeIndexMissing, diag.Health, diag.Code, diag.Message)
	}
	if diag.Reason != index.ReasonDBFileMissing {
		t.Fatalf("子因应为 %s，实际 = %s", index.ReasonDBFileMissing, diag.Reason)
	}
	if diag.Usable() {
		t.Fatal("missing 不得判为可用")
	}
}

// testReservedPollutionStillDetected：放宽的只有 reserved 这两项，
// 外部污染（`leftover.tmp`）该判还判，且「混入非法文件」清单里**只**列污染项。
func testReservedPollutionStillDetected(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	writeLegalRuntimeEntries(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "leftover.tmp"), []byte("垃圾"), 0o644); err != nil {
		t.Fatalf("写污染文件失败：%v", err)
	}
	diag := index.Inspect(dir)
	assertCorrupt(t, diag, index.ReasonUnexpectedFile)
	if !strings.Contains(diag.Message, "混入非法文件 leftover.tmp（") {
		t.Fatalf("非法文件清单应恰为 leftover.tmp，实际消息 = %q", diag.Message)
	}
}

// —— ③ 类型违规 ——

// testReservedTypeViolations 逐例对撞四种类型违规，并在每一例上同时钉住「只报不改」。
func testReservedTypeViolations(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, dir string)
		want    index.ReservedViolation
	}{
		{
			name: "run.lock 是目录",
			prepare: func(t *testing.T, dir string) {
				if err := os.MkdirAll(filepath.Join(dir, "run.lock"), 0o755); err != nil {
					t.Fatalf("造违规锁失败：%v", err)
				}
				mkTxnTree(t, dir)
			},
			want: index.ReservedViolation{
				Name: "run.lock", Want: index.EntryRegular, Got: index.EntryDir, Symlink: false,
			},
		},
		{
			name: "txn 是普通文件",
			prepare: func(t *testing.T, dir string) {
				mkLockFile(t, dir)
				if err := os.WriteFile(filepath.Join(dir, "txn"), []byte("不是目录"), 0o644); err != nil {
					t.Fatalf("造违规日志失败：%v", err)
				}
			},
			want: index.ReservedViolation{
				Name: "txn", Want: index.EntryDir, Got: index.EntryRegular, Symlink: false,
			},
		},
		{
			// symlink 指向**合法**的普通文件同样违规：否则「锁文件 inode 不得被替换」
			// 会退化成「链接没变」，还多出一条往目录外写的通道。
			name: "run.lock 是 symlink（指向合法普通文件）",
			prepare: func(t *testing.T, dir string) {
				target := filepath.Join(filepath.Dir(dir), "outside-lock")
				if err := os.WriteFile(target, nil, 0o644); err != nil {
					t.Fatalf("造链接目标失败：%v", err)
				}
				mustSymlink(t, target, filepath.Join(dir, "run.lock"))
				mkTxnTree(t, dir)
			},
			want: index.ReservedViolation{
				Name: "run.lock", Want: index.EntryRegular, Got: index.EntrySymlink, Symlink: true,
			},
		},
		{
			name: "txn 是 symlink（指向合法目录）",
			prepare: func(t *testing.T, dir string) {
				mkLockFile(t, dir)
				target := filepath.Join(filepath.Dir(dir), "outside-txn")
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatalf("造链接目标失败：%v", err)
				}
				mustSymlink(t, target, filepath.Join(dir, "txn"))
			},
			want: index.ReservedViolation{
				Name: "txn", Want: index.EntryDir, Got: index.EntrySymlink, Symlink: true,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := buildFixture(t, sampleSnapshot())
			c.prepare(t, dir)
			before := reservedTreeSnapshot(t, dir)

			// 专用入口回传**结构化**事实（哪一项 / 期望类型 / 实测类型 / 是否 symlink）。
			got, bad := index.InspectRuntimeReserved(dir)
			if !bad {
				t.Fatal("类型违规应被判出，实际报告无违规")
			}
			if got != c.want {
				t.Fatalf("违规事实 = %+v，期望 %+v", got, c.want)
			}
			if !strings.Contains(got.String(), c.want.Name) {
				t.Fatalf("人类可读说明应点名违规项，实际 = %q", got.String())
			}

			// 判定面：corrupt + **既有** W24 + `unexpected_file`（零新增诊断码）。
			diag := index.Inspect(dir)
			assertCorrupt(t, diag, index.ReasonUnexpectedFile)
			if !strings.Contains(diag.Message, c.want.Name) {
				t.Fatalf("诊断消息应点名违规项 %s，实际 = %q", c.want.Name, diag.Message)
			}

			// 只报不改：多跑几次体检，`.index/` 全树逐字不变（不重建、不删除、不修复）。
			for i := 0; i < 3; i++ {
				_ = index.Inspect(dir)
				_, _ = index.InspectRuntimeReserved(dir)
			}
			assertTreeUnchanged(t, before, reservedTreeSnapshot(t, dir))
		})
	}
}

// testReservedTypeViolationBeatsMissing：库不在 + 保留条目类型违规 ⇒ 判 corrupt。
//
// 判定次序在这里被钉死：类型违规先于「库在不在」。理由是此时这个目录已经不是一个可被安全
// 使用的 `.index/`，「缺个库」不再是重点；而反过来（合法 reserved + 无库）必须仍是 missing，
// 由 testReservedRuntimeOnlyStaysMissing 从另一侧钉住。
func testReservedTypeViolationBeatsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)
	if err := os.MkdirAll(filepath.Join(dir, "run.lock"), 0o755); err != nil {
		t.Fatalf("造违规锁失败：%v", err)
	}
	diag := index.Inspect(dir)
	assertCorrupt(t, diag, index.ReasonUnexpectedFile)
	if diag.Code != index.CodeIndexCorrupt {
		t.Fatalf("码应为 %s（R9 §19.5：原因已确定为 corrupt），实际 = %s",
			index.CodeIndexCorrupt, diag.Code)
	}
}

// —— 测试脚手架 ——

// writeLegalRuntimeEntries 在 dir 下造一份**合法**的 M6 运行时现场：
// `run.lock`（普通文件）+ `txn/`（含 `seq` 与一个未闭合事务目录：`intent.json` + `pre/`）。
func writeLegalRuntimeEntries(t *testing.T, dir string) {
	t.Helper()
	mkLockFile(t, dir)
	mkTxnTree(t, dir)
}

func mkLockFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "run.lock"), []byte("pid=1\n"), 0o644); err != nil {
		t.Fatalf("造锁文件失败：%v", err)
	}
}

func mkTxnTree(t *testing.T, dir string) {
	t.Helper()
	txnDir := filepath.Join(dir, "txn")
	open := filepath.Join(txnDir, "t0000000000000001")
	if err := os.MkdirAll(filepath.Join(open, "pre"), 0o755); err != nil {
		t.Fatalf("造事务目录失败：%v", err)
	}
	for path, body := range map[string]string{
		filepath.Join(txnDir, "seq"):       "1\n",
		filepath.Join(open, "intent.json"): `{"journal_version":1,"files":[]}`,
		filepath.Join(open, "pre", "1"):    "前像字节",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("造事务日志 %s 失败：%v", path, err)
		}
	}
}

// assertLegalRuntimeEntriesOnDisk 反向自检：锁是普通文件、日志是目录，两者都真的在盘上。
func assertLegalRuntimeEntriesOnDisk(t *testing.T, dir string) {
	t.Helper()
	lock, err := os.Lstat(filepath.Join(dir, "run.lock"))
	if err != nil || !lock.Mode().IsRegular() {
		t.Fatalf("run.lock 应是在盘的普通文件，实际 err = %v", err)
	}
	txn, err := os.Lstat(filepath.Join(dir, "txn"))
	if err != nil || !txn.IsDir() {
		t.Fatalf("txn 应是在盘的目录，实际 err = %v", err)
	}
}

// mustSymlink 建一条符号链接，失败即判红。
//
// 本项目的验收环境恒为 Linux，符号链接是**必备**能力而非可选特性：造链接失败说明环境
// 本身不满足前置条件，而 reserved 条目的 symlink 违规是 M6 的核心判据之一，
// 一旦跳过就等于这条判据在该次运行里凭空消失。因此这里 fail closed，不给 skip 留口子。
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("造符号链接 %s -> %s 失败（Linux 验收环境必须支持 symlink）：%v", link, target, err)
	}
}

// reservedTreeSnapshot 把 `.index/` 全树抓成 相对路径 → 类型与内容 的快照。
//
// 快照口径刻意覆盖到**类型**与**链接目标**：只比字节的话，「目录被换成同名空文件」
// 这类改动会从眼皮底下溜过去。
func reservedTreeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		// Walk 用 Lstat 取 info，因此链接不会被跟随。
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, rerr := os.Readlink(p)
			if rerr != nil {
				return rerr
			}
			out[filepath.ToSlash(rel)] = "symlink:" + target
			return nil
		case info.IsDir():
			out[filepath.ToSlash(rel)] = "dir"
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = "file:" + string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("抓取 %s 快照失败：%v", dir, err)
	}
	return out
}

// assertTreeUnchanged 逐项比对两份快照（多一项 / 少一项 / 变一个字节都当场红）。
func assertTreeUnchanged(t *testing.T, before, after map[string]string) {
	t.Helper()
	for rel, want := range before {
		got, ok := after[rel]
		if !ok {
			t.Fatalf("体检删掉了 %s：本层只报不改", rel)
		}
		if got != want {
			t.Fatalf("体检改动了 %s：%q → %q", rel, want, got)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Fatalf("体检新造了 %s：本层只报不改（不重建）", rel)
		}
	}
}
