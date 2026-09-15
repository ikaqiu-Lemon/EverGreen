package store

// atomic_test.go — T-072 批次 B1：store 内存预演层的聚焦测试。
//
// 覆盖：零实盘写、同文件多 op 读到 overlay、新建/既有前像正确、write-set 按首次写入
// 顺序去重、导出字节原样、skip 不进入 write-set。全程不触 S5 提交层、不接 CLI / index。

import (
	"bytes"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

func appendEdit(section string, payload string) Edit {
	return Edit{Kind: mdfile.KindNote, Sections: []SectionAppend{{Section: section, Payload: []byte(payload)}}}
}

// TestAtomicZeroDiskWriteOnGuardedWrite：预演期间守卫写只落 overlay，实盘零变化。
func TestAtomicZeroDiskWriteOnGuardedWrite(t *testing.T) {
	s, root := newVault(t)
	abs := writeSeed(t, root, "notes/n.md", noteSample)
	before := mustBytes(t, abs)

	if err := s.BeginAtomic(); err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := s.WriteGuarded("notes/n.md", ContentHash(before), appendEdit(mdfile.SecNoteBody, "预演补充。\n"))
	if err != nil {
		t.Fatalf("guarded write: %v", err)
	}
	if !res.Written {
		t.Fatalf("预演写应记为 Written")
	}
	// 实盘必须原样。
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatalf("预演期间实盘被改动：want 原样，got 变化")
	}
	assertNoTmp(t, root+"/notes")

	ws := s.AtomicWriteSet()
	if len(ws) != 1 || ws[0].Path != "notes/n.md" {
		t.Fatalf("write-set 应恰含 notes/n.md，got %+v", ws)
	}
	if bytes.Equal(ws[0].TargetBytes, before) {
		t.Fatalf("staged 目标字节应包含追加内容")
	}
	if ws[0].IsNew {
		t.Fatalf("既有文件不应标记为新建")
	}
	if !bytes.Equal(ws[0].PreBytes, before) || ws[0].PreHash != ContentHash(before) {
		t.Fatalf("既有文件前像应等于首次实盘快照")
	}
	s.EndAtomic()
	// 退出后仍然零实盘变化。
	if got := mustBytes(t, abs); !bytes.Equal(got, before) {
		t.Fatalf("退出预演后实盘不应变化")
	}
}

// TestAtomicSameFileMultiOpReadsOverlay：同文件多 op，后续读取看到最新 staged 字节，
// 且合并为一个 FileSpec（目标为最终字节）。
func TestAtomicSameFileMultiOpReadsOverlay(t *testing.T) {
	s, root := newVault(t)
	writeSeed(t, root, "notes/n.md", noteSample)
	base, err := s.Read("notes/n.md")
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}

	if err := s.BeginAtomic(); err != nil {
		t.Fatalf("begin: %v", err)
	}
	r1, err := s.WriteGuarded("notes/n.md", base.Hash, appendEdit(mdfile.SecNoteBody, "第一段。\n"))
	if err != nil {
		t.Fatalf("op1: %v", err)
	}
	// 第二次读取必须看到第一次 staged 的最新字节（不是实盘旧字节）。
	mid, err := s.Read("notes/n.md")
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if mid.Hash != r1.Hash {
		t.Fatalf("同 plan 第二次读取应看到 staged 最新 hash：want %s got %s", r1.Hash, mid.Hash)
	}
	if !bytes.Contains(mid.Bytes, []byte("第一段。")) {
		t.Fatalf("staged 读取应含第一段追加")
	}
	// 第二个 op 以 staged hash 为基线（B3 正确基线），继续追加。
	r2, err := s.WriteGuarded("notes/n.md", mid.Hash, appendEdit(mdfile.SecNoteBody, "第二段。\n"))
	if err != nil {
		t.Fatalf("op2: %v", err)
	}

	ws := s.AtomicWriteSet()
	if len(ws) != 1 {
		t.Fatalf("同一路径多 op 应合并为一个 FileSpec，got %d", len(ws))
	}
	if ws[0].TargetHash != r2.Hash {
		t.Fatalf("FileSpec 目标应为最终 staged 字节")
	}
	if !bytes.Contains(ws[0].TargetBytes, []byte("第一段。")) ||
		!bytes.Contains(ws[0].TargetBytes, []byte("第二段。")) {
		t.Fatalf("最终目标应累计两段追加")
	}
}

// TestAtomicCreatePreimageIsNew：预演内新建，IsNew 为真、前像为空、实盘不落文件；
// 同一预演内二次新建同路径按「已存在」拒绝，新建事实（首次不存在）不翻转。
func TestAtomicCreatePreimageIsNew(t *testing.T) {
	s, _ := newVault(t)

	if err := s.BeginAtomic(); err != nil {
		t.Fatalf("begin: %v", err)
	}
	rel := "cards/k.md"
	if _, err := s.CreateFile(rel, mdfile.KindCard, []byte(cardTemplate)); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 实盘不应出现该文件。
	if _, err := s.Read(rel); err != nil {
		t.Fatalf("预演内新建后 Read 应从 overlay 读到：%v", err)
	}
	if exists, _ := s.Exists(rel); !exists {
		t.Fatalf("预演内应视为已存在（overlay）")
	}
	// 二次新建：overlay 视为已存在 → 拒绝。
	if _, err := s.CreateFile(rel, mdfile.KindCard, []byte(cardTemplate)); err == nil {
		t.Fatalf("二次新建同路径应被拒绝（已 staged）")
	}

	ws := s.AtomicWriteSet()
	if len(ws) != 1 || !ws[0].IsNew {
		t.Fatalf("新建应导出 IsNew=true 的单个 FileSpec，got %+v", ws)
	}
	if ws[0].PreBytes != nil || ws[0].PreHash != "" {
		t.Fatalf("新建的前像必须为空（首次不存在事实）")
	}
	s.EndAtomic()
	if exists, _ := s.Exists(rel); exists {
		t.Fatalf("退出预演后实盘不应出现新建文件（零实盘写）")
	}
}

// TestAtomicWriteSetBytesVerbatim：预演导出的目标字节，与非预演直落实盘的结果逐字相同。
func TestAtomicWriteSetBytesVerbatim(t *testing.T) {
	// 预演路径。
	sA, rootA := newVault(t)
	writeSeed(t, rootA, "notes/n.md", noteSample)
	baseA, _ := sA.Read("notes/n.md")
	if err := sA.BeginAtomic(); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := sA.WriteGuarded("notes/n.md", baseA.Hash, appendEdit(mdfile.SecNoteBody, "要点。\n")); err != nil {
		t.Fatalf("dry write: %v", err)
	}
	ws := sA.AtomicWriteSet()
	sA.EndAtomic()

	// 非预演路径：同一 seed 直落实盘。
	sB, rootB := newVault(t)
	absB := writeSeed(t, rootB, "notes/n.md", noteSample)
	baseB, _ := sB.Read("notes/n.md")
	if _, err := sB.WriteGuarded("notes/n.md", baseB.Hash, appendEdit(mdfile.SecNoteBody, "要点。\n")); err != nil {
		t.Fatalf("real write: %v", err)
	}
	realBytes := mustBytes(t, absB)

	if len(ws) != 1 {
		t.Fatalf("want 1 spec, got %d", len(ws))
	}
	if !bytes.Equal(ws[0].TargetBytes, realBytes) {
		t.Fatalf("预演目标字节与实盘落地结果不一致")
	}
	if ws[0].TargetHash != ContentHash(realBytes) {
		t.Fatalf("目标 hash 与实盘落地结果不一致")
	}
}

// TestAtomicOrderByFirstWrite：write-set 顺序按首次写入路径顺序，去重后每路径一条。
func TestAtomicOrderByFirstWrite(t *testing.T) {
	s, root := newVault(t)
	writeSeed(t, root, "notes/a.md", noteSample)
	writeSeed(t, root, "notes/b.md", noteSample)
	ha, _ := s.Read("notes/a.md")
	hb, _ := s.Read("notes/b.md")

	if err := s.BeginAtomic(); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// 首次写入顺序：b, a；随后再写一次 b（不改变首次顺序，也不新增条目）。
	if _, err := s.WriteGuarded("notes/b.md", hb.Hash, appendEdit(mdfile.SecNoteBody, "b1。\n")); err != nil {
		t.Fatalf("b1: %v", err)
	}
	if _, err := s.WriteGuarded("notes/a.md", ha.Hash, appendEdit(mdfile.SecNoteBody, "a1。\n")); err != nil {
		t.Fatalf("a1: %v", err)
	}
	nb, _ := s.Read("notes/b.md")
	if _, err := s.WriteGuarded("notes/b.md", nb.Hash, appendEdit(mdfile.SecNoteBody, "b2。\n")); err != nil {
		t.Fatalf("b2: %v", err)
	}

	ws := s.AtomicWriteSet()
	if len(ws) != 2 {
		t.Fatalf("两条路径应去重为两条 FileSpec，got %d", len(ws))
	}
	if ws[0].Path != "notes/b.md" || ws[1].Path != "notes/a.md" {
		t.Fatalf("顺序应按首次写入 [b, a]，got [%s, %s]", ws[0].Path, ws[1].Path)
	}
}

// TestAtomicSkipDoesNotStage：守卫跳过（B3 hash 不符）不 stage、不进入 write-set，
// 且只读触达不落入 accepted set。
func TestAtomicSkipDoesNotStage(t *testing.T) {
	s, root := newVault(t)
	writeSeed(t, root, "notes/n.md", noteSample)

	if err := s.BeginAtomic(); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// 传入错误的 expectedHash → SkipFileChanged，不写。
	_, err := s.WriteGuarded("notes/n.md", "sha256:deadbeef", appendEdit(mdfile.SecNoteBody, "不应写入。\n"))
	if _, ok := AsSkip(err); !ok {
		t.Fatalf("hash 不符应返回 SkipError，got %v", err)
	}
	if ws := s.AtomicWriteSet(); len(ws) != 0 {
		t.Fatalf("跳过不应进入 write-set，got %+v", ws)
	}
}

// TestAtomicNestedBeginRejected：预演不支持嵌套。
func TestAtomicNestedBeginRejected(t *testing.T) {
	s, _ := newVault(t)
	if err := s.BeginAtomic(); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.BeginAtomic(); err == nil {
		t.Fatalf("嵌套 BeginAtomic 应报错")
	}
	s.EndAtomic()
	if s.InAtomic() {
		t.Fatalf("EndAtomic 后应退出预演模式")
	}
}
