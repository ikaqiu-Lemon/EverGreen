package index_test

// T-…-065 的机器判据（其二）：全量构建的**确定性**与如实交代。
//
// 判据来源：M5 索引架构合同 §4.4（确定性口径：除 built_at_unix / files.indexed_at_unix
// 外逐字等价）、§5.1（水位线 = (head, files_hash)，files_hash 由每文件 content_hash 按
// path 升序聚合）、§2.6（bigram 补路的写入侧口径）。
//
// 一条最高约束贯穿全文件：**Markdown 是唯一权威来源，`.index/` 恒为可重建派生**。
// 因此本文件所有断言都只看 `.index/` 里的产物，而构建输入是一个中性快照 —— index 包
// 既不解析 Markdown，也不碰权威文件读写口。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// fixedNow 让 built_at_unix 完全可控：确定性断言不能依赖挂钟。
var fixedNow = time.Unix(1_700_000_000, 0).UTC()

// fixedOptions 返回注入了固定时钟的构建选项。
func fixedOptions() index.Options {
	return index.Options{Now: func() time.Time { return fixedNow }}
}

// sampleSnapshot 是一份**刻意不按序**的输入快照（中英文混排 + 关系边 + 文件水位线）。
//
// 输入乱序是有意的：确定性必须由 index 包自己的排序保证，而不是碰巧因为调用方按序喂进来。
func sampleSnapshot() index.Snapshot {
	return index.Snapshot{
		Head: strings.Repeat("ab", 20), // 40 位十六进制样式的 HEAD
		Cards: []index.Card{
			{
				ID: "k-beta", Path: "domains/ai/knowledge/k-beta.md", Domain: "ai",
				Title: "分词与索引", Status: "active", Body: "这是正文 attention 机制",
				ContentHash: "sha256:bbbb", MTimeUnix: 1_600_000_100,
			},
			{
				ID: "k-alpha", Path: "domains/ai/knowledge/k-alpha.md", Domain: "ai",
				Title: "Attention", Status: "deprecated", Deprecated: true,
				ReplacedBy: "k-beta", Body: "body one\nsecond line",
				ContentHash: "sha256:aaaa", MTimeUnix: 1_600_000_000,
			},
			{
				ID: "k-gamma", Path: "domains/ops/knowledge/k-gamma.md", Domain: "ops",
				Title: "运维", Status: "active", Deleted: true,
				Body:        "已删除卡片仍然入索引，删除语义由读路径过滤",
				ContentHash: "sha256:cccc", MTimeUnix: 1_600_000_200,
			},
		},
		Relations: []index.Relation{
			{SrcID: "k-beta", Verb: "refines", DstID: "k-alpha", SrcPath: "domains/ai/knowledge/k-beta.md"},
			{SrcID: "k-alpha", Verb: "supports", DstID: "k-gamma", SrcPath: "domains/ai/knowledge/k-alpha.md"},
			{SrcID: "k-alpha", Verb: "refines", DstID: "k-beta", SrcPath: "domains/ai/knowledge/k-alpha.md"},
		},
		Files: []index.File{
			{Path: "domains/ops/knowledge/k-gamma.md", ContentHash: "sha256:cccc", Size: 300, MTimeUnix: 1_600_000_200},
			{Path: "domains/ai/knowledge/k-alpha.md", ContentHash: "sha256:aaaa", Size: 100, MTimeUnix: 1_600_000_000},
			{Path: "domains/ai/knowledge/k-beta.md", ContentHash: "sha256:bbbb", Size: 200, MTimeUnix: 1_600_000_100},
		},
	}
}

// buildFixture 在临时目录里建一份索引，返回 `.index/` 路径。
func buildFixture(t *testing.T, snap index.Snapshot) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), index.DirName)
	if _, err := index.Build(dir, snap, fixedOptions()); err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	return dir
}

// TestBuildFullDeterministic 反证「同一快照的两次独立全量构建在 Digest 口径下逐字等价」，
// 且乱序输入不影响结果（合同 §4.4）。
func TestBuildFullDeterministic(t *testing.T) {
	dirA := buildFixture(t, sampleSnapshot())

	// 第二次故意把输入的次序整体反过来喂：等价性必须来自包内排序，而非输入次序。
	snapB := sampleSnapshot()
	reverseCards(snapB.Cards)
	reverseRelations(snapB.Relations)
	reverseFiles(snapB.Files)
	dirB := buildFixture(t, snapB)

	digestA, err := index.Digest(dirA)
	if err != nil {
		t.Fatalf("Digest(A) 失败：%v", err)
	}
	digestB, err := index.Digest(dirB)
	if err != nil {
		t.Fatalf("Digest(B) 失败：%v", err)
	}
	if digestA != digestB {
		t.Fatalf("两次全量构建的逻辑内容不等价：\nA = %s\nB = %s", digestA, digestB)
	}

	// 水位线本身也必须确定：files_hash 只由 (path, content_hash) 决定。
	metaA, err := index.ReadMeta(dirA)
	if err != nil {
		t.Fatalf("ReadMeta(A) 失败：%v", err)
	}
	metaB, err := index.ReadMeta(dirB)
	if err != nil {
		t.Fatalf("ReadMeta(B) 失败：%v", err)
	}
	if metaA.FilesHash != metaB.FilesHash {
		t.Fatalf("files_hash 不确定：A = %s，B = %s", metaA.FilesHash, metaB.FilesHash)
	}
	if metaA.CardCount != 3 || metaA.Head != strings.Repeat("ab", 20) {
		t.Fatalf("水位线内容不对：card_count = %d，head = %q", metaA.CardCount, metaA.Head)
	}
}

// TestBuildTwiceByteIdentical 在**固定时钟**下要求两次构建的 `eg.db` 主库文件字节级相同。
//
// 为什么敢要求字节级：唯一两处天然非确定的列（built_at_unix / files.indexed_at_unix）已被
// 固定时钟钉死，剩下的插入次序与 rowid 都是显式确定的。字节级相等是比 Digest 更强的证据，
// 它顺带排除了「页内布局依赖 map 遍历序」这类隐性不确定源。
func TestBuildTwiceByteIdentical(t *testing.T) {
	dirA := buildFixture(t, sampleSnapshot())
	dirB := buildFixture(t, sampleSnapshot())

	a, err := os.ReadFile(filepath.Join(dirA, index.DBFileName))
	if err != nil {
		t.Fatalf("读 A 库失败：%v", err)
	}
	b, err := os.ReadFile(filepath.Join(dirB, index.DBFileName))
	if err != nil {
		t.Fatalf("读 B 库失败：%v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("两次构建的库大小不同：%d vs %d 字节", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("两次构建的库在第 %d 字节起不同（固定时钟下应字节级相同）", i)
		}
	}
}

// TestBuildEmptyVaultOK 反证空库是**合法状态**而不是错误：零卡片同样要产出可用索引与
// 明确的水位线（空集合的 files_hash 是空串的哈希，不是空串）。
func TestBuildEmptyVaultOK(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)
	res, err := index.Build(dir, index.Snapshot{Head: ""}, fixedOptions())
	if err != nil {
		t.Fatalf("空 vault 构建应成功，实际失败：%v", err)
	}
	if res.CardCount != 0 || res.RelationCount != 0 || res.FileCount != 0 {
		t.Fatalf("空 vault 的回执应全 0，实际 = %+v", res)
	}
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("空 vault 的索引应判 healthy，实际 = %s / %s", diag.Health, diag.Reason)
	}
	meta, err := index.ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta 失败：%v", err)
	}
	if meta.CardCount != 0 {
		t.Fatalf("card_count = %d，期望 0", meta.CardCount)
	}
	if meta.FilesHash == "" || len(meta.FilesHash) != 64 {
		t.Fatalf("空集合也必须有明确 files_hash（64 位十六进制），实际 = %q", meta.FilesHash)
	}
	if meta.Head != "" {
		t.Fatalf("非 git 仓的 head 应为空串，实际 = %q", meta.Head)
	}
}

// TestBuildRefusesExistingIndex 反证 Build 不覆盖既有库：覆盖语义只属 Rebuild（显式优于隐式）。
func TestBuildRefusesExistingIndex(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	before, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}
	if _, err := index.Build(dir, index.Snapshot{}, fixedOptions()); !errors.Is(err, index.ErrIndexExists) {
		t.Fatalf("在既有库上 Build 应返回 ErrIndexExists，实际 = %v", err)
	}
	after, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}
	if before != after {
		t.Fatalf("被拒绝的 Build 竟改动了既有库：%s → %s", before, after)
	}
}

// TestBuildReportsDroppedDuplicates 反证重复 ID / 重复关系边**只收一份但绝不静默**：
// 库里本来就可能有 duplicate_id / relation_duplicate（那是 `eg check` 的 finding），
// 索引不替库治病，但必须把丢弃条数如实回给调用方。
func TestBuildReportsDroppedDuplicates(t *testing.T) {
	snap := sampleSnapshot()
	snap.Cards = append(snap.Cards, index.Card{
		ID: "k-alpha", Path: "domains/dup/knowledge/k-alpha.md", Domain: "dup",
		Title: "重复 ID", Status: "active", ContentHash: "sha256:dddd",
	})
	snap.Relations = append(snap.Relations, index.Relation{
		SrcID: "k-alpha", Verb: "refines", DstID: "k-beta",
		SrcPath: "domains/ai/knowledge/k-alpha.md",
	})
	dir := filepath.Join(t.TempDir(), index.DirName)
	res, err := index.Build(dir, snap, fixedOptions())
	if err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	if res.DroppedDuplicateCards != 1 {
		t.Fatalf("重复卡片丢弃数 = %d，期望 1（必须如实交代）", res.DroppedDuplicateCards)
	}
	if res.DroppedDuplicateRelations != 1 {
		t.Fatalf("重复关系边丢弃数 = %d，期望 1", res.DroppedDuplicateRelations)
	}
	if res.CardCount != 3 {
		t.Fatalf("去重后卡片数 = %d，期望 3", res.CardCount)
	}
	// 留下的那份必须是**确定**的一份（同 ID 按 path 升序取第一）：
	db := openFixture(t, dir, true)
	var path string
	if err := db.QueryRow(`SELECT path FROM `+index.TableCards+` WHERE id = ?`, "k-alpha").Scan(&path); err != nil {
		t.Fatalf("查 k-alpha 失败：%v", err)
	}
	if path != "domains/ai/knowledge/k-alpha.md" {
		t.Fatalf("同 ID 去重留下的不是 path 最小的一份，实际 = %s", path)
	}
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("去重后索引应仍自洽，实际 = %s / %s", diag.Health, diag.Message)
	}
}

// TestBuildRejectsUnknownSkippedKind 反证 skipped.kind 取值域封闭在**写入侧**就拦住。
func TestBuildRejectsUnknownSkippedKind(t *testing.T) {
	snap := sampleSnapshot()
	snap.Skipped = []index.SkippedFile{{Path: "a.md", Kind: "brand_new_kind"}}
	dir := filepath.Join(t.TempDir(), index.DirName)
	if _, err := index.Build(dir, snap, fixedOptions()); err == nil {
		t.Fatalf("非法 skipped.kind 应导致构建失败")
	}
	// 失败不留半成品：`.index/` 必须已被清掉，下一次 build 从干净状态开始。
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("构建失败后应不留半成品目录，实际 stat = %v", err)
	}
}

// TestBuildOnlyAllowedFiles 反证 `.index/` 只出现允许集合内的文件（恰 3 个之内）：
// 派生目录的整洁性是「整目录 gitignore + 可随时 rm -rf」的前提。
func TestBuildOnlyAllowedFiles(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读 .index/ 失败：%v", err)
	}
	allowed := map[string]bool{}
	for _, name := range index.AllowedFiles() {
		allowed[name] = true
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Fatalf(".index/ 下出现子目录 %s（布局只允许 3 个文件）", e.Name())
		}
		if !allowed[e.Name()] {
			t.Fatalf(".index/ 下出现非法文件 %s（允许集合 = %v）", e.Name(), index.AllowedFiles())
		}
	}
}

// TestFilesHashWatermark 钉住水位线聚合口径（合同 §5.1）：
//
//	① 次序无关（按 path 升序聚合）；
//	② content_hash 变了 files_hash 必须变；
//	③ mtime / size 变了 files_hash **不得**变 —— 它们只是快路径，绝不作为最终结论。
func TestFilesHashWatermark(t *testing.T) {
	base := sampleSnapshot().Files
	shuffled := append([]index.File(nil), base...)
	reverseFiles(shuffled)
	if index.FilesHash(base) != index.FilesHash(shuffled) {
		t.Fatalf("files_hash 依赖了输入次序")
	}

	touched := append([]index.File(nil), base...)
	touched[0].MTimeUnix += 999
	touched[0].Size += 4096
	if index.FilesHash(touched) != index.FilesHash(base) {
		t.Fatalf("mtime/size 改动不应影响 files_hash（它们只是快路径过滤）")
	}

	changed := append([]index.File(nil), base...)
	changed[0].ContentHash = "sha256:zzzz"
	if index.FilesHash(changed) == index.FilesHash(base) {
		t.Fatalf("content_hash 改动必须改变 files_hash")
	}

	// 增删文件同样必须改变水位线。
	fewer := base[1:]
	if index.FilesHash(fewer) == index.FilesHash(base) {
		t.Fatalf("文件集合变化必须改变 files_hash")
	}
	if got := index.FilesHash(nil); len(got) != 64 {
		t.Fatalf("空集合的 files_hash 应为 64 位十六进制，实际 = %q", got)
	}
}

// TestBigramTextCJK 钉住 D0b 补路的写入侧口径：CJK 连续串切成相邻 bigram，
// 非 CJK 词整体保留（ASCII 由主路 trigram 负责，不在这里拆字母），
// 且非空结果两端各补一个空格（让首尾 bigram 与中间 bigram 同形可查）。
func TestBigramTextCJK(t *testing.T) {
	cases := []struct{ in, want string }{
		{"分词与索引", " 分词 词与 与索 索引 "},
		{"中", " 中 "},
		{"hello world", " hello world "},
		{"注意力 attention 机制", " 注意 意力 attention 机制 "},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := index.BigramText(c.in); got != c.want {
			t.Fatalf("BigramText(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestBigramColumnQueryable 反证 1 ~ 2 字中文经补路列**可被捞回**（合同 §2.6 D0b 的存在理由）。
//
// 这里同时钉住一个 T-…-067 必须知道的实测事实：主路 D0 下整表 tokenizer 是 `trigram`，
// 所以查询串也会被 trigram 切分 ——「分词」只有 2 字符，裸查 `MATCH "分词"` 恒 `hits=0`；
// 而 `bigram_text` 里 bigram 之间由空格分隔，故「bigram + 一个空格」正好构成 3 字符，
// `MATCH "分词 "` 命中。写入侧预切的价值就在这里，读路径必须按此形态构造查询串。
// 读路径实现属 T-…-067，本 task 只钉住列内容与可命中形态。
func TestBigramColumnQueryable(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	db := openFixture(t, dir, true)

	countMatch := func(query string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM `+index.TableCardsFTS+
			` WHERE `+index.TableCardsFTS+` MATCH ?`, query).Scan(&n); err != nil {
			t.Fatalf("MATCH %q 失败：%v", query, err)
		}
		return n
	}
	if got := countMatch(`bigram_text:"分词 "`); got == 0 {
		t.Fatalf("2 字中文「分词」经 bigram_text 补路（bigram + 空格形态）应命中，实际 0 行")
	}
	// 首尾 bigram 同形可查（这正是两端补空格的理由）：「索引」在预切串末尾。
	if got := countMatch(`bigram_text:"索引 "`); got == 0 {
		t.Fatalf("末尾 bigram「索引」应与中间 bigram 同形可命中，实际 0 行")
	}
	if got := countMatch(`bigram_text:"分词"`); got != 0 {
		t.Fatalf("裸 2 字符查询在 trigram 主路下应恒 0 命中（这正是补路存在的理由），实际 = %d", got)
	}
	// 补路列不得污染正文列的语义：正文里没有的 bigram 不能凭空命中。
	if got := countMatch(`bigram_text:"量子 "`); got != 0 {
		t.Fatalf("语料里不存在的 bigram 竟命中 %d 行", got)
	}
}

// —— 测试脚手架：原地反转，用于制造乱序输入 ——

func reverseCards(in []index.Card) {
	for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
		in[i], in[j] = in[j], in[i]
	}
}

func reverseRelations(in []index.Relation) {
	for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
		in[i], in[j] = in[j], in[i]
	}
}

func reverseFiles(in []index.File) {
	for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
		in[i], in[j] = in[j], in[i]
	}
}
