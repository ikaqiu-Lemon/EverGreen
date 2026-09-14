package index_test

// T-…-066 的机器判据（其二）：陈旧检出 `W22 index_stale` 与「只报不阻断」。
//
// 判据来源：M5 索引架构合同 §5.2（三态判定表 + 关键裁决「stale 不允许先用旧结果再提示」）
// 与 §6.3（诊断码分配；W22 / W23 / W24 三者互斥，退出码影响一律为「无」）。
//
// 边界：本文件**不**测读命令怎么降级（属 T-…-067），只测「状态算得对、码给得对、
// 且这套判定在任何状态下都不会要求阻断读」。

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// currentOf 把一份快照当作「权威 Markdown 现态」喂给 Check（现态由调用方扫描得到，
// 索引层不读 Markdown）。
func currentOf(snap index.Snapshot) index.Current {
	return index.Current{Head: snap.Head, Files: snap.Files}
}

// TestStaleDetectedOnExternalEdit 是 §5.2 的核心场景：**外部编辑器改了 Markdown 但未提交**。
//
// 此时 HEAD 一个字节没动，只有 files_hash 变了 —— 只看 HEAD 的实现会永远认为自己干净
// （A-44 排除表选项 ③）。判据：Freshness = stale，Code = W22，Reason = files_changed，
// 且逐文件 diff 能准确指出是哪一个文件变了。
func TestStaleDetectedOnExternalEdit(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)

	if c := index.Check(dir, currentOf(snap)); !c.Fresh() || c.Code != "" {
		t.Fatalf("建库直后必须 fresh 且零诊断码，实得 %s / %q", c.Freshness, c.Code)
	}

	edited := sampleSnapshot()
	edited.Files[1].ContentHash = "sha256:edited-by-hand" // domains/ai/knowledge/k-alpha.md
	c := index.Check(dir, currentOf(edited))
	if !c.Stale() {
		t.Fatalf("外部编辑未提交时必须判 stale，实得 %s", c.Freshness)
	}
	if c.Code != index.CodeIndexStale {
		t.Fatalf("陈旧的诊断码 = %q，期望 %q", c.Code, index.CodeIndexStale)
	}
	if c.Reason != index.StaleReasonFilesChanged {
		t.Fatalf("陈旧子因 = %q，期望 %q（HEAD 没动，只有文件变了）",
			c.Reason, index.StaleReasonFilesChanged)
	}
	if !reflect.DeepEqual(c.Changes.Modified, []string{"domains/ai/knowledge/k-alpha.md"}) {
		t.Fatalf("逐文件 diff 没指准变更文件：%v", c.Changes)
	}
	if c.Indexed.Equal(c.Actual) {
		t.Fatal("判 stale 却报告两侧水位线相等：自相矛盾")
	}
	if !contains(c.Message, "eg index sync") {
		t.Fatalf("陈旧消息必须给出修复建议 eg index sync，实得：%s", c.Message)
	}
}

// TestStaleReasonHeadMoved 反证三个陈旧子因都真的可达（commit 后未 sync / 外部编辑 / 两者）。
func TestStaleReasonHeadMoved(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)

	moved := sampleSnapshot()
	moved.Head = "cafe" + moved.Head[4:]
	if c := index.Check(dir, currentOf(moved)); c.Reason != index.StaleReasonHeadMoved {
		t.Fatalf("只动 HEAD 的子因 = %q，期望 %q", c.Reason, index.StaleReasonHeadMoved)
	}

	both := sampleSnapshot()
	both.Head = "cafe" + both.Head[4:]
	both.Files[0].ContentHash = "sha256:zzzz"
	if c := index.Check(dir, currentOf(both)); c.Reason != index.StaleReasonHeadAndFilesChanged {
		t.Fatalf("两者都动的子因 = %q，期望 %q", c.Reason, index.StaleReasonHeadAndFilesChanged)
	}
}

// TestW22CodeEmitted 钉住码字面量与「W22 只属陈旧」这件事：
// W22 / W23 / W24 三个字面量互不相同，且 Check 在陈旧态下确实产出 W22。
func TestW22CodeEmitted(t *testing.T) {
	if index.CodeIndexStale != "W22" {
		t.Fatalf("陈旧码漂移：%q，合同 §6.3 分配的是 W22", index.CodeIndexStale)
	}
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)
	stale := sampleSnapshot()
	stale.Files[0].ContentHash = "sha256:zzzz"
	if c := index.Check(dir, currentOf(stale)); c.Code != "W22" {
		t.Fatalf("陈旧态没产出 W22，实得 %q", c.Code)
	}
	codes := map[string]bool{
		index.CodeIndexStale: true, index.CodeIndexMissing: true, index.CodeIndexCorrupt: true,
	}
	if len(codes) != 3 {
		t.Fatalf("W22 / W23 / W24 三码不互异：%v", codes)
	}
}

// TestConsistencyCodesMutuallyExclusive 反证「一次判定最多一条码」：
// Code 是**单值**字段，四种状态各自恰对应一个取值（fresh 为空串）。
func TestConsistencyCodesMutuallyExclusive(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)

	cases := []struct {
		name string
		set  func(t *testing.T) (string, index.Current)
		want string
	}{
		{"fresh", func(*testing.T) (string, index.Current) { return dir, currentOf(snap) }, ""},
		{"stale", func(*testing.T) (string, index.Current) {
			s := sampleSnapshot()
			s.Files[0].ContentHash = "sha256:zzzz"
			return dir, currentOf(s)
		}, index.CodeIndexStale},
		{"missing", func(t *testing.T) (string, index.Current) {
			return filepath.Join(t.TempDir(), index.DirName), currentOf(snap)
		}, index.CodeIndexMissing},
		{"corrupt", func(t *testing.T) (string, index.Current) {
			bad := filepath.Join(t.TempDir(), index.DirName)
			if err := os.MkdirAll(bad, 0o755); err != nil {
				t.Fatalf("建目录失败：%v", err)
			}
			if err := os.WriteFile(filepath.Join(bad, index.DBFileName),
				[]byte("not a sqlite file"), 0o644); err != nil {
				t.Fatalf("写坏库失败：%v", err)
			}
			return bad, currentOf(snap)
		}, index.CodeIndexCorrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, cur := tc.set(t)
			c := index.Check(d, cur)
			if c.Code != tc.want {
				t.Fatalf("%s 的码 = %q，期望 %q", tc.name, c.Code, tc.want)
			}
		})
	}
}

// TestStaleNeverBlocksRead 是 §6.1 总纲的机器形态：**索引的任何不健康状态都不阻断读**。
//
//	① BlocksRead() 在四种状态下恒 false；
//	② UseIndex() 只有 fresh 为 true（stale 与 unusable 在读结果上行为一致：都走扫描）；
//	③ Check 永不返回 error —— 它的签名里根本没有 error（缺失目录也照样给结论）。
func TestStaleNeverBlocksRead(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)
	stale := sampleSnapshot()
	stale.Files[0].ContentHash = "sha256:zzzz"

	freshC := index.Check(dir, currentOf(snap))
	staleC := index.Check(dir, currentOf(stale))
	missingC := index.Check(filepath.Join(t.TempDir(), index.DirName), currentOf(snap))

	for _, c := range []index.Consistency{freshC, staleC, missingC} {
		if c.BlocksRead() {
			t.Fatalf("%s 状态要求阻断读：违反合同 §6.1（索引不可用必须降级而非报错退出）", c.Freshness)
		}
	}
	if !freshC.UseIndex() {
		t.Fatal("fresh 必须允许用索引后端")
	}
	if staleC.UseIndex() || missingC.UseIndex() {
		t.Fatal("stale / unusable 都不得被判为「索引可信」（合同 §5.2 关键裁决）")
	}
	if !missingC.Unusable() || missingC.Freshness != index.FreshnessUnusable {
		t.Fatalf("缺失索引的三态结论 = %s，期望 unusable", missingC.Freshness)
	}
}

// TestFreshnessesClosed 钉住新鲜度集合恰 3 值（下游 status 输出按此断言）。
func TestFreshnessesClosed(t *testing.T) {
	want := []string{"fresh", "stale", "unusable"}
	if got := index.Freshnesses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Freshnesses() = %v，期望 %v", got, want)
	}
	// 与 Health 的分工：Health 里**没有** stale（那是 065 的封闭结论，不许被本 task 改）。
	if string(index.HealthHealthy) == "stale" || string(index.HealthCorrupt) == "stale" {
		t.Fatal("Health 枚举被污染出 stale：三态属 Freshness，Health 恒 3 值不含 stale")
	}
}

// TestStaleReasonsClosed 钉住陈旧子因恰 3 值，且与 065 的 9 个损坏子因**不重叠**
// （「坏」与「旧」必须可区分）。
func TestStaleReasonsClosed(t *testing.T) {
	want := []string{"head_moved", "files_changed", "head_and_files_changed"}
	if got := index.StaleReasons(); !reflect.DeepEqual(got, want) {
		t.Fatalf("StaleReasons() = %v，期望 %v", got, want)
	}
	corruptReasons := map[string]bool{}
	for _, r := range index.Reasons() {
		corruptReasons[r] = true
	}
	for _, r := range index.StaleReasons() {
		if corruptReasons[r] {
			t.Fatalf("陈旧子因 %q 与损坏子因集合重叠：两个集合语义域不同，不得合并", r)
		}
	}
}

// TestCheckIsReadOnly 反证 Check 全程只读：主库 `eg.db` 的字节与逻辑内容在前后逐字相同。
//
// 口径与 065 的 TestInspectIsReadOnly 一致 —— 比对**主库字节 + Digest**，而不是整目录：
// 只读打开一个 WAL 库时驱动可能落下 `eg.db-wal` / `eg.db-shm`，它们在合同 §2.3 A-43 的
// 允许集合内、且不含逻辑内容，不算「改写索引」。
func TestCheckIsReadOnly(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)
	dbPath := filepath.Join(dir, index.DBFileName)
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读库失败：%v", err)
	}
	digestBefore, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}

	stale := sampleSnapshot()
	stale.Files[0].ContentHash = "sha256:zzzz"
	_ = index.Check(dir, currentOf(snap))
	_ = index.Check(dir, currentOf(stale))

	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读库失败：%v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("Check 改写了主库字节（%d → %d）：状态查询必须只读", len(before), len(after))
	}
	digestAfter, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}
	if digestBefore != digestAfter {
		t.Fatalf("Check 改变了逻辑内容：%s → %s", digestBefore, digestAfter)
	}
}
