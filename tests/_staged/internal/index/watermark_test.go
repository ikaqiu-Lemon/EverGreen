package index_test

// T-…-066 的机器判据（其一）：水位线口径与三向 diff。
//
// 判据来源：M5 索引架构合同 §5.1（A-44：水位线 = Git HEAD + 每文件 content_hash，
// mtime / size 只作快路径，**绝不**单独作为「未变更」的最终结论）。
//
// 本文件只测 index 包的纯函数与只读路径：不写权威 Markdown、不跑 git、不接 CLI。

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// TestWatermarkFromHeadAndHashes 反证水位线的**两半**都真的参与取值，且与索引记录的一致。
//
//	① 建库后 `index_meta` 里的水位线 == 由 (head, files) 现算的水位线；
//	② files 乱序不影响取值（聚合恒按 path 升序）；
//	③ 只改 head ⇒ 水位线变；只改任一文件的 content_hash ⇒ 水位线变。
func TestWatermarkFromHeadAndHashes(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)

	indexed, err := index.ReadWatermark(dir)
	if err != nil {
		t.Fatalf("ReadWatermark 失败：%v", err)
	}
	actual := index.WatermarkFrom(snap.Head, snap.Files)
	if !indexed.Equal(actual) {
		t.Fatalf("建库直后水位线就不相等：索引 %v，现态 %v", indexed, actual)
	}

	shuffled := sampleSnapshot()
	reverseFiles(shuffled.Files)
	if got := index.WatermarkFrom(snap.Head, shuffled.Files); !got.Equal(actual) {
		t.Fatalf("水位线依赖输入次序：乱序得 %v，期望 %v", got, actual)
	}

	if got := index.WatermarkFrom(snap.Head+"0", snap.Files); got.Equal(actual) {
		t.Fatal("换了 head 水位线却没变：head 没有真正参与取值")
	}

	edited := sampleSnapshot()
	edited.Files[0].ContentHash = "sha256:zzzz"
	if got := index.WatermarkFrom(snap.Head, edited.Files); got.Equal(actual) {
		t.Fatal("改了某个文件的 content_hash 水位线却没变：files_hash 没有真正参与取值")
	}
}

// TestMTimeAndSizeNeverSoleAuthority 是 A-44 排除表的机器形态（风险 R-23）：
//
//	① `(path, size, mtime)` 完全一致但内容不同（git checkout / 同秒两次写）⇒
//	   快路径命中，但**最终判定**必须是 Modified；
//	② content_hash 相同而 mtime / size 不同 ⇒ 判定必须是 Unchanged（时间戳不是权威）。
func TestMTimeAndSizeNeverSoleAuthority(t *testing.T) {
	indexed := []index.File{{Path: "a.md", ContentHash: "sha256:old", Size: 100, MTimeUnix: 7}}
	sameStat := []index.File{{Path: "a.md", ContentHash: "sha256:new", Size: 100, MTimeUnix: 7}}

	if !index.QuickUnchanged(indexed[0], sameStat[0]) {
		t.Fatal("(path,size,mtime) 三者一致时快路径应命中（快路径只表示可省一次 hash 计算）")
	}
	cs := index.DiffFiles(indexed, sameStat)
	if !reflect.DeepEqual(cs.Modified, []string{"a.md"}) || len(cs.Unchanged) != 0 {
		t.Fatalf("同 stat 异内容必须判 Modified，实得 %v", cs)
	}

	newStat := []index.File{{Path: "a.md", ContentHash: "sha256:old", Size: 999, MTimeUnix: 8}}
	if index.QuickUnchanged(indexed[0], newStat[0]) {
		t.Fatal("size / mtime 变了快路径不该命中")
	}
	cs = index.DiffFiles(indexed, newStat)
	if !reflect.DeepEqual(cs.Unchanged, []string{"a.md"}) || cs.Total() != 0 {
		t.Fatalf("hash 相同即未变更（mtime / size 不是权威），实得 %v", cs)
	}
}

// TestDiffFilesThreeWay 钉住三向 diff 的四类归属与升序，并反证**重命名**的表达形态
// （旧路径 Removed + 新路径 Added，不引入第三种变更类型 / 不做 rename 启发式）。
func TestDiffFilesThreeWay(t *testing.T) {
	indexed := []index.File{
		{Path: "b.md", ContentHash: "h-b"},
		{Path: "a.md", ContentHash: "h-a"},
		{Path: "old/name.md", ContentHash: "h-r"},
	}
	current := []index.File{
		{Path: "new/name.md", ContentHash: "h-r"}, // 重命名后的同一份内容
		{Path: "a.md", ContentHash: "h-a"},        // 未变
		{Path: "b.md", ContentHash: "h-b2"},       // 修改
		{Path: "c.md", ContentHash: "h-c"},        // 新增
	}
	cs := index.DiffFiles(indexed, current)
	if !reflect.DeepEqual(cs.Added, []string{"c.md", "new/name.md"}) {
		t.Fatalf("Added = %v，期望 [c.md new/name.md]（升序）", cs.Added)
	}
	if !reflect.DeepEqual(cs.Modified, []string{"b.md"}) {
		t.Fatalf("Modified = %v，期望 [b.md]", cs.Modified)
	}
	if !reflect.DeepEqual(cs.Removed, []string{"old/name.md"}) {
		t.Fatalf("Removed = %v，期望 [old/name.md]", cs.Removed)
	}
	if !reflect.DeepEqual(cs.Unchanged, []string{"a.md"}) {
		t.Fatalf("Unchanged = %v，期望 [a.md]", cs.Unchanged)
	}
	if !reflect.DeepEqual(cs.Affected(), []string{"b.md", "c.md", "new/name.md"}) {
		t.Fatalf("Affected() = %v（应为 Added ∪ Modified 升序）", cs.Affected())
	}
	if cs.Empty() || cs.Total() != 4 {
		t.Fatalf("Empty()=%v Total()=%d，期望 false / 4", cs.Empty(), cs.Total())
	}
	if !index.DiffFiles(indexed, indexed).Empty() {
		t.Fatal("同一清单自比对必须零变更")
	}
}

// TestDiffFilesUnknownHashIsNeverUnchanged 反证保守兜底：调用方没算 hash（空串）时，
// 绝不允许把「不知道」静默读成「没变」。
func TestDiffFilesUnknownHashIsNeverUnchanged(t *testing.T) {
	indexed := []index.File{{Path: "a.md", ContentHash: "h-a"}}
	current := []index.File{{Path: "a.md", ContentHash: ""}}
	if cs := index.DiffFiles(indexed, current); !reflect.DeepEqual(cs.Modified, []string{"a.md"}) {
		t.Fatalf("content_hash 为空必须判 Modified，实得 %v", cs)
	}
}

// TestReadFilesRoundTrip 反证 `files` 表可被只读回放（快路径的前置），且按 path 升序。
func TestReadFilesRoundTrip(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)
	got, err := index.ReadFiles(dir)
	if err != nil {
		t.Fatalf("ReadFiles 失败：%v", err)
	}
	if len(got) != len(snap.Files) {
		t.Fatalf("ReadFiles 返回 %d 行，期望 %d 行", len(got), len(snap.Files))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Path >= got[i].Path {
			t.Fatalf("ReadFiles 不是 path 升序：%v", got)
		}
	}
	want := map[string]string{}
	for _, f := range snap.Files {
		want[f.Path] = f.ContentHash
	}
	for _, f := range got {
		if want[f.Path] != f.ContentHash {
			t.Fatalf("%s 的 content_hash = %q，期望 %q", f.Path, f.ContentHash, want[f.Path])
		}
	}
	if got := index.WatermarkFrom(snap.Head, got); !got.Equal(index.WatermarkFrom(snap.Head, snap.Files)) {
		t.Fatal("由 files 表回放算出的水位线与由现态算出的不等")
	}
}

// TestWatermarkStringHumanReadable 保证诊断消息里的水位线是**可读且不误导**的：
// 空 head（非 git 仓）显式标注，不能显示成一段空白让人以为是 40 位零。
func TestWatermarkStringHumanReadable(t *testing.T) {
	s := index.Watermark{Head: "", FilesHash: strings.Repeat("f", 64)}.String()
	if !contains(s, "(空)") || !contains(s, "files_hash=") {
		t.Fatalf("水位线人读形态可疑：%q", s)
	}
}
