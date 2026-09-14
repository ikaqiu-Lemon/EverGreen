package index_test

// T-…-065 的机器判据（其三）：损坏检测「只报不改」+ 四类损坏逐条可判。
//
// 判据来源：M5 索引架构合同 §6.1（索引不可用时降级而非报错退出）、§6.3（诊断码分配：
// W23 index_missing / W24 index_corrupt）、§4.2（card_count 与实际行数不等即判损坏）、
// §4.3（schema_version 不匹配 ⇒ 整库重建，永不迁移）。
//
// 全文件贯穿一条最高约束：**索引不可用是一个诊断，不是一次失败**。所以 Inspect 的签名里
// 没有 error —— 断言也据此写成「必须给出 W23/W24 + 机器可读子因」，而不是「必须返回错误」。
//
// 本 task **不做**陈旧判定（W22 index_stale 需要与现态比对水位线，属 T-…-066）：
// 下面有一条断言专门反证「Health 枚举里没有 stale」，防止提前实现。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// TestInspectHealthyIndex 是判定面的正向锚点：刚建好的索引必须 healthy 且可用，
// 且不带任何诊断码。
func TestInspectHealthyIndex(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	diag := index.Inspect(dir)
	if !diag.Usable() || diag.Health != index.HealthHealthy {
		t.Fatalf("刚建好的索引应 healthy，实际 = %s / %s / %s", diag.Health, diag.Reason, diag.Message)
	}
	if diag.Code != "" || diag.Reason != index.ReasonNone {
		t.Fatalf("healthy 时不应带诊断码，实际 code = %q，reason = %q", diag.Code, diag.Reason)
	}
	if !diag.MetaReadable || diag.Meta.CardCount != 3 {
		t.Fatalf("healthy 诊断应带出可读元数据，实际 = %+v", diag)
	}
}

// TestInspectMissingIndex 反证「缺失」与「损坏」是**两类**结论（W23 vs W24）：
// 首次使用 / 被 rm -rf 掉都属缺失，处置是构建而非修复。
func TestInspectMissingIndex(t *testing.T) {
	// ① `.index/` 整个不存在。
	root := t.TempDir()
	diag := index.Inspect(filepath.Join(root, index.DirName))
	if diag.Health != index.HealthMissing || diag.Code != index.CodeIndexMissing {
		t.Fatalf("缺目录应判 missing/W23，实际 = %s / %s", diag.Health, diag.Code)
	}
	if diag.Reason != index.ReasonIndexDirMissing {
		t.Fatalf("子因应为 index_dir_missing，实际 = %s", diag.Reason)
	}
	if diag.Usable() {
		t.Fatalf("missing 不得判为可用")
	}

	// ② 目录在但库文件不在（典型：用户只删了 eg.db）。
	dir := filepath.Join(t.TempDir(), index.DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	diag = index.Inspect(dir)
	if diag.Health != index.HealthMissing || diag.Reason != index.ReasonDBFileMissing {
		t.Fatalf("缺库文件应判 missing/db_file_missing，实际 = %s / %s", diag.Health, diag.Reason)
	}
	if diag.MetaReadable {
		t.Fatalf("读不到库时 MetaReadable 必须为 false（不能把零值当成 card_count=0）")
	}
}

// TestCorruptSchemaVersionMismatch 反证第 ① 类损坏：版本不匹配。
//
// 关键语义（合同 §4.3）：**永不迁移**，处置一律整库重建。所以这里不只断言判为 corrupt，
// 还断言人类可读说明里明确指向 rebuild —— 用户不该被留在「库坏了但不知道怎么办」的状态。
func TestCorruptSchemaVersionMismatch(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	execFixture(t, dir, `UPDATE `+index.TableIndexMeta+` SET value = '999' WHERE key = ?`,
		index.MetaSchemaVersion)

	diag := index.Inspect(dir)
	if diag.Health != index.HealthCorrupt || diag.Code != index.CodeIndexCorrupt {
		t.Fatalf("版本不匹配应判 corrupt/W24，实际 = %s / %s", diag.Health, diag.Code)
	}
	if diag.Reason != index.ReasonSchemaVersionMismatch {
		t.Fatalf("子因应为 schema_version_mismatch，实际 = %s（%s）", diag.Reason, diag.Message)
	}
	if diag.Usable() {
		t.Fatalf("版本不匹配的索引不得判为可用")
	}
	if !contains(diag.Message, "rebuild") {
		t.Fatalf("说明里应给出处置（rebuild），实际 = %q", diag.Message)
	}
	// 版本不匹配时元数据本身是读得出来的：诊断必须如实带出旧版本号，便于用户理解。
	if !diag.MetaReadable || diag.Meta.SchemaVersion != 999 {
		t.Fatalf("应如实带出旧 schema_version，实际 = %+v", diag)
	}
}

// TestCorruptTruncatedFile 反证第 ② 类损坏：文件截断 / 文件头非法。
//
// 两个子形态都要判住：长度不足文件头（100 字节），以及长度够但魔数不对
// （典型是被别的工具覆写、或半个下载文件被塞进来）。
func TestCorruptTruncatedFile(t *testing.T) {
	t.Run("短于文件头", func(t *testing.T) {
		dir := buildFixture(t, sampleSnapshot())
		dbPath := filepath.Join(dir, index.DBFileName)
		if err := os.Truncate(dbPath, 50); err != nil {
			t.Fatalf("截断失败：%v", err)
		}
		assertCorrupt(t, index.Inspect(dir), index.ReasonTruncatedFile)
	})

	t.Run("按页数应有长度不足", func(t *testing.T) {
		dir := buildFixture(t, sampleSnapshot())
		dbPath := filepath.Join(dir, index.DBFileName)
		st, err := os.Stat(dbPath)
		if err != nil {
			t.Fatalf("stat 失败：%v", err)
		}
		// 砍掉最后一页：文件头声明的页数与实际长度就矛盾了。
		if err := os.Truncate(dbPath, st.Size()-4096); err != nil {
			t.Fatalf("截断失败：%v", err)
		}
		assertCorrupt(t, index.Inspect(dir), index.ReasonTruncatedFile)
	})

	t.Run("魔数非法", func(t *testing.T) {
		dir := buildFixture(t, sampleSnapshot())
		dbPath := filepath.Join(dir, index.DBFileName)
		f, err := os.OpenFile(dbPath, os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("打开失败：%v", err)
		}
		if _, err := f.WriteAt([]byte("NOT A SQLITE DB\x00"), 0); err != nil {
			t.Fatalf("覆写文件头失败：%v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("关闭失败：%v", err)
		}
		assertCorrupt(t, index.Inspect(dir), index.ReasonTruncatedFile)
	})
}

// TestCorruptIntegrityCheckFails 反证第 ③ 类损坏：`PRAGMA integrity_check` 非 ok。
//
// 制造手法是把**中间某一页**整页填成垃圾（页 1 = 文件头保持合法，长度也不变），
// 因此前置的截断判定不会拦下它，必须由 integrity_check 这一层判出来 —— 这正是这条断言
// 存在的意义：四类损坏各有独立的判定面，不能互相顶替。
func TestCorruptIntegrityCheckFails(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	dbPath := filepath.Join(dir, index.DBFileName)

	// WAL / SHM 一并清掉：让被读到的一定是主库文件本身（否则页可能从 WAL 取到干净副本）。
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			t.Fatalf("删除 %s 失败：%v", suffix, err)
		}
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读库失败：%v", err)
	}
	const pageSize = 4096
	if len(data) < 3*pageSize {
		t.Fatalf("样本库只有 %d 字节，不足 3 页，无法制造页内损坏", len(data))
	}
	// 把第 2 页起的两页整页填成垃圾（避开页 1 的文件头，保持文件总长不变）。
	for i := pageSize; i < 3*pageSize; i++ {
		data[i] = 0xAA
	}
	if err := os.WriteFile(dbPath, data, 0o644); err != nil {
		t.Fatalf("写回失败：%v", err)
	}

	diag := index.Inspect(dir)
	if diag.Health != index.HealthCorrupt || diag.Code != index.CodeIndexCorrupt {
		t.Fatalf("页内损坏应判 corrupt/W24，实际 = %s / %s（%s）", diag.Health, diag.Code, diag.Message)
	}
	if diag.Reason != index.ReasonIntegrityCheckFailed {
		t.Fatalf("子因应为 integrity_check_failed，实际 = %s（%s）", diag.Reason, diag.Message)
	}
	if diag.Usable() {
		t.Fatalf("integrity_check 不过的索引不得判为可用")
	}
}

// TestCorruptWatermarkSelfContradiction 反证第 ④ 类损坏：水位线自指矛盾
// （`index_meta.card_count` ≠ `SELECT count(*) FROM cards`，合同 §4.2）。
//
// 这类损坏 SQLite 自己永远发现不了 —— 库结构完全合法、integrity_check 也是 ok，
// 只有**索引层的自检**能判出来。它是「水位线与索引内容同事务提交」这条设计的守门断言。
func TestCorruptWatermarkSelfContradiction(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	execFixture(t, dir, `DELETE FROM `+index.TableCards+` WHERE id = ?`, "k-alpha")

	diag := index.Inspect(dir)
	if diag.Health != index.HealthCorrupt || diag.Reason != index.ReasonWatermarkSelfContradiction {
		t.Fatalf("card_count 不自洽应判 corrupt/watermark_self_contradiction，实际 = %s / %s（%s）",
			diag.Health, diag.Reason, diag.Message)
	}
	if !diag.MetaReadable || diag.Meta.CardCount != 3 {
		t.Fatalf("应如实带出水位线声明值 3，实际 = %+v", diag.Meta)
	}
	if diag.Usable() {
		t.Fatalf("自指矛盾的索引不得判为可用")
	}
}

// TestCorruptUnexpectedFile 反证 `.index/` 混入集合外文件即判不可用：
// 派生目录必须保持「白名单恰 3 个文件」的整洁形态，否则「整目录可 rm -rf」不再安全。
func TestCorruptUnexpectedFile(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("外部工具塞进来的"), 0o644); err != nil {
		t.Fatalf("写污染文件失败：%v", err)
	}
	diag := index.Inspect(dir)
	assertCorrupt(t, diag, index.ReasonUnexpectedFile)
	if !contains(diag.Message, "notes.txt") {
		t.Fatalf("说明里应点名污染文件，实际 = %q", diag.Message)
	}
}

// TestCorruptSchemaIncomplete 反证缺表即判不可用（表集合恰 6 张是硬形态）。
func TestCorruptSchemaIncomplete(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	execFixture(t, dir, `DROP TABLE `+index.TableSkipped)
	diag := index.Inspect(dir)
	assertCorrupt(t, diag, index.ReasonSchemaIncomplete)
	if !contains(diag.Message, index.TableSkipped) {
		t.Fatalf("说明里应点名缺失的表，实际 = %q", diag.Message)
	}
}

// TestInspectIsReadOnly 反证「只报不改」：体检前后 `eg.db` 的字节必须逐字相同。
//
// 比对范围刻意限定在 `eg.db` 这一个权威索引文件：`-wal` / `-shm` 是 WAL 模式下由 SQLite
// 自行管理的运行期副产物（只读连接也可能触碰 `-shm` 的共享内存镜像），把它们纳入字节比对
// 只会引入与语义无关的抖动。真正要守住的是「体检不改索引内容」。
func TestInspectIsReadOnly(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	dbPath := filepath.Join(dir, index.DBFileName)

	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读库失败：%v", err)
	}
	digestBefore, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}
	for i := 0; i < 3; i++ {
		if diag := index.Inspect(dir); !diag.Usable() {
			t.Fatalf("第 %d 次体检竟判不可用：%s", i, diag.Message)
		}
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读库失败：%v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("体检改变了库大小：%d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("体检改写了库的第 %d 字节（Inspect 必须只读）", i)
		}
	}
	digestAfter, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}
	if digestBefore != digestAfter {
		t.Fatalf("体检改变了逻辑内容：%s → %s", digestBefore, digestAfter)
	}
}

// TestHealthEnumHasNoStale 反证**没有提前实现** T-…-066 的陈旧判定：
// 本 task 的 Health 只有 healthy / missing / corrupt 三值，`stale` 一格都不占。
func TestHealthEnumHasNoStale(t *testing.T) {
	for _, h := range []index.Health{index.HealthHealthy, index.HealthMissing, index.HealthCorrupt} {
		if string(h) == "stale" {
			t.Fatalf("Health 里出现了 stale：陈旧判定属 T-…-066，本 task 不得提前实现")
		}
	}
	// 诊断码同理：W22 index_stale 归 066，本包只产 W23 / W24。
	if index.CodeIndexMissing != "W23" || index.CodeIndexCorrupt != "W24" {
		t.Fatalf("诊断码漂移：missing = %q，corrupt = %q（合同 §6.3 分配 W23 / W24）",
			index.CodeIndexMissing, index.CodeIndexCorrupt)
	}
}

// TestReasonsClosed 钉住损坏子因集合封闭（9 个，机器可读，供 status 与 e2e 逐字断言）。
func TestReasonsClosed(t *testing.T) {
	got := index.Reasons()
	want := []string{
		"index_dir_missing", "db_file_missing", "unexpected_file", "truncated_file",
		"open_failed", "integrity_check_failed", "schema_incomplete",
		"schema_version_mismatch", "watermark_self_contradiction",
	}
	if !equalSet(got, want) {
		t.Fatalf("损坏子因集合不封闭：\n实际 = %v\n期望 = %v", got, want)
	}
	if index.ReasonNone != "" {
		t.Fatalf("healthy 的子因必须是空串，实际 = %q", index.ReasonNone)
	}
}

// —— 测试脚手架 ——

// assertCorrupt 断言一次体检判为 corrupt/W24 且子因逐字相等。
func assertCorrupt(t *testing.T, diag index.Diagnosis, reason string) {
	t.Helper()
	if diag.Health != index.HealthCorrupt {
		t.Fatalf("应判 corrupt，实际 = %s（%s）", diag.Health, diag.Message)
	}
	if diag.Code != index.CodeIndexCorrupt {
		t.Fatalf("诊断码应为 %s，实际 = %s", index.CodeIndexCorrupt, diag.Code)
	}
	if diag.Reason != reason {
		t.Fatalf("子因应为 %s，实际 = %s（%s）", reason, diag.Reason, diag.Message)
	}
	if diag.Usable() {
		t.Fatalf("corrupt 不得判为可用")
	}
	if diag.Message == "" {
		t.Fatalf("必须给出人类可读说明")
	}
}

// execFixture 直连索引库执行一条写语句，用于制造损坏（测试脚手架：产品代码永不这么改库）。
//
// 收尾做一次 WAL 全量回灌：让注入的损坏落在主库文件里，与真实世界的坏库形态一致。
func execFixture(t *testing.T, dir, query string, args ...interface{}) {
	t.Helper()
	db := openFixture(t, dir, false)
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("注入语句失败（%s）：%v", query, err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("WAL 回灌失败：%v", err)
	}
}

// contains 是 strings.Contains 的薄封装（避免在断言处引入额外 import 噪音）。
func contains(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	n, m := len(haystack), len(needle)
	for i := 0; i+m <= n; i++ {
		if haystack[i:i+m] == needle {
			return i
		}
	}
	return -1
}
