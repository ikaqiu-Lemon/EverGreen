package index_test

// T-…-066 的机器判据（其三）：增量更新与收敛。
//
// 判据来源：M5 索引架构合同 §5.3（`eg index sync` 收敛语义：三向 diff / 幂等 / 与全量
// 等价 / 删除即消行 / 不可用时退化为 full build 且如实说明）+ §4.4（确定性等价口径）。
//
// 边界：本文件只测 index 包的窄 API（Apply / Sync）。**不**接 CLI、**不**接读路径、
// **不**碰写命令 —— 那些属本 task 的阶段 B 与 T-…-067。

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// laterNow 是「第二次写索引」的时钟：与建库时刻区分，才能反证
// 「未变更文件的 indexed_at_unix 原值不动」。
var laterNow = time.Unix(1_700_009_999, 0).UTC()

// laterOptions 返回注入了较晚固定时钟的选项。
func laterOptions() index.Options {
	return index.Options{Now: func() time.Time { return laterNow }}
}

const (
	pathAlpha = "domains/ai/knowledge/k-alpha.md"
	pathBeta  = "domains/ai/knowledge/k-beta.md"
	pathGamma = "domains/ops/knowledge/k-gamma.md"
	pathDelta = "domains/ai/knowledge/k-delta.md"
)

// editedSnapshot 是 sampleSnapshot 之后的**现态**：一次改动同时覆盖三类变更 ——
//
//	修改：k-alpha（正文与 content_hash 都变）
//	新增：k-delta（新文件 + 一条出边）
//	删除：k-gamma（文件消失，其卡片行 / 关系行都该跟着消失）
//
// 未变更：k-beta（它的索引行与 indexed_at_unix 都必须原样保留）。
func editedSnapshot() index.Snapshot {
	snap := sampleSnapshot()
	for i := range snap.Cards {
		if snap.Cards[i].Path == pathAlpha {
			snap.Cards[i].Body = "body one\nsecond line\n外部编辑器加的一段中文"
			snap.Cards[i].ContentHash = "sha256:aaaa-v2"
			snap.Cards[i].Title = "Attention（改）"
		}
	}
	snap.Cards = append(snap.Cards, index.Card{
		ID: "k-delta", Path: pathDelta, Domain: "ai", Title: "新卡",
		Status: "active", Body: "新卡正文 retrieval augmented",
		ContentHash: "sha256:dddd", MTimeUnix: 1_600_000_300,
		Kind: index.CardKindKnowledge, Validation: "",
	})
	// 删掉 k-gamma 的卡片行与它作为源的关系行（它的文件已不存在）。
	snap.Cards = dropCardsByPath(snap.Cards, pathGamma)
	snap.Relations = dropRelationsBySrcPath(snap.Relations, pathGamma)
	snap.Relations = append(snap.Relations,
		index.Relation{SrcID: "k-delta", Verb: "refines", DstID: "k-beta", SrcPath: pathDelta})

	files := []index.File{}
	for _, f := range snap.Files {
		switch f.Path {
		case pathGamma: // 删除
			continue
		case pathAlpha: // 修改
			f.ContentHash, f.Size, f.MTimeUnix = "sha256:aaaa-v2", 180, 1_600_009_000
		}
		files = append(files, f)
	}
	files = append(files, index.File{
		Path: pathDelta, ContentHash: "sha256:dddd", Size: 120, MTimeUnix: 1_600_000_300})
	snap.Files = files
	return snap
}

// renamedSnapshot 是「k-beta 被重命名」后的现态：卡片 ID 不变，路径换值。
// 重命名在索引口径下 = 旧路径消失 + 新路径出现（不做 rename 启发式）。
func renamedSnapshot() index.Snapshot {
	const moved = "domains/ai/knowledge/renamed-k-beta.md"
	snap := sampleSnapshot()
	for i := range snap.Cards {
		if snap.Cards[i].Path == pathBeta {
			snap.Cards[i].Path = moved
		}
	}
	for i := range snap.Relations {
		if snap.Relations[i].SrcPath == pathBeta {
			snap.Relations[i].SrcPath = moved
		}
	}
	for i := range snap.Files {
		if snap.Files[i].Path == pathBeta {
			snap.Files[i].Path = moved
		}
	}
	return snap
}

func dropCardsByPath(in []index.Card, path string) []index.Card {
	out := in[:0:0]
	for _, c := range in {
		if c.Path != path {
			out = append(out, c)
		}
	}
	return out
}

func dropRelationsBySrcPath(in []index.Relation, path string) []index.Relation {
	out := in[:0:0]
	for _, r := range in {
		if r.SrcPath != path {
			out = append(out, r)
		}
	}
	return out
}

// deltaFor 模拟写命令的构造动作：从**现态快照**里切出受影响路径上的行，外加已消失的路径。
//
// 这正是阶段 B 里写命令要做的事（它们知道自己刚写了哪些文件），因此测试用同一形态喂进来。
func deltaFor(snap index.Snapshot, affected, removed []string) index.Delta {
	hit := map[string]bool{}
	for _, p := range affected {
		hit[p] = true
	}
	d := index.Delta{Head: snap.Head, Removed: removed}
	for _, c := range snap.Cards {
		if hit[c.Path] {
			d.Cards = append(d.Cards, c)
		}
	}
	for _, r := range snap.Relations {
		if hit[r.SrcPath] {
			d.Relations = append(d.Relations, r)
		}
	}
	for _, f := range snap.Files {
		if hit[f.Path] {
			d.Files = append(d.Files, f)
		}
	}
	for _, s := range snap.Skipped {
		if hit[s.Path] {
			d.Skipped = append(d.Skipped, s)
		}
	}
	return d
}

// digestOf 取索引的确定性摘要（等价比较的唯一口径）。
func digestOf(t *testing.T, dir string) string {
	t.Helper()
	got, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}
	return got
}

// freshDigestOf 在一个独立临时目录里对同一现态做**全量构建**，返回其摘要。
func freshDigestOf(t *testing.T, snap index.Snapshot) string {
	t.Helper()
	return digestOf(t, buildFixture(t, snap))
}

// countRows 在只读连接上数一条计数查询。
func countRows(t *testing.T, dir, query string, args ...interface{}) int {
	t.Helper()
	db := openFixture(t, dir, true)
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败（%s）：%v", query, err)
	}
	return n
}

// indexedAt 读某个文件行的 indexed_at_unix（不存在则返回 -1）。
func indexedAt(t *testing.T, dir, path string) int64 {
	t.Helper()
	db := openFixture(t, dir, true)
	defer func() { _ = db.Close() }()
	var at int64
	err := db.QueryRow(`SELECT indexed_at_unix FROM files WHERE path = ?`, path).Scan(&at)
	if err == sql.ErrNoRows {
		return -1
	}
	if err != nil {
		t.Fatalf("读 indexed_at_unix 失败：%v", err)
	}
	return at
}

// TestIncrementalEqualsFullRebuild 是 §5.3「等价」这一条的机器形态：
// 「建库 → 增量应用一批增 / 改 / 删」的结果，与「直接对现态全量构建」在 Digest 口径下**逐字等价**。
//
// 顺带反证 rowid 的确定性：cards 与 cards_fts 的 rowid 必须逐行对齐，且恒等于 id 升序下的序号。
func TestIncrementalEqualsFullRebuild(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	now := editedSnapshot()

	res, err := index.Apply(dir, deltaFor(now, []string{pathAlpha, pathDelta}, []string{pathGamma}), laterOptions())
	if err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	if res.Action != index.ActionSynced {
		t.Fatalf("动作 = %q，期望 %q", res.Action, index.ActionSynced)
	}
	if res.Degraded {
		t.Fatal("healthy 索引上的增量不应报退化")
	}
	if !reflect.DeepEqual(res.Changes.Added, []string{pathDelta}) ||
		!reflect.DeepEqual(res.Changes.Modified, []string{pathAlpha}) ||
		!reflect.DeepEqual(res.Changes.Removed, []string{pathGamma}) {
		t.Fatalf("三向 diff 不符：%v", res.Changes)
	}

	if got, want := digestOf(t, dir), freshDigestOf(t, now); got != want {
		t.Fatalf("增量结果与全量重建不等价：\n增量 %s\n全量 %s", got, want)
	}
	if got, want := res.After, index.WatermarkFrom(now.Head, now.Files); !got.Equal(want) {
		t.Fatalf("增量后水位线 = %v，期望 %v", got, want)
	}

	// rowid 对齐：join 得到的行数必须等于卡片数，且 rowid 恒为 id 升序序号。
	if n := countRows(t, dir,
		`SELECT count(*) FROM cards c JOIN cards_fts f ON c.rowid = f.rowid AND c.id = f.id`); n != res.CardCount {
		t.Fatalf("cards 与 cards_fts 的 rowid 对齐行数 = %d，期望 %d", n, res.CardCount)
	}
	if n := countRows(t, dir,
		`SELECT count(*) FROM (SELECT rowid, row_number() OVER (ORDER BY id) AS want FROM cards)
		 WHERE rowid <> want`); n != 0 {
		t.Fatalf("有 %d 行的 rowid 不等于 id 升序序号：确定序被破坏", n)
	}
}

// TestIncrementalAffectedFilesOnly 是「只碰受影响文件」这条 Acceptance 的机器形态。
//
// 判据：未变更文件（k-beta）的 `files.indexed_at_unix` **原值不动**；
// 受影响文件（k-alpha / k-delta）的该列推进到本次时刻；被删文件的行整体消失。
// 顺带钉住 Delta.Paths() 恰为受影响路径集合（升序去重）——「只碰哪些文件」是可断言的。
func TestIncrementalAffectedFilesOnly(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	buildAt := indexedAt(t, dir, pathBeta)
	if buildAt != fixedNow.Unix() {
		t.Fatalf("建库时 indexed_at_unix = %d，期望 %d", buildAt, fixedNow.Unix())
	}

	now := editedSnapshot()
	d := deltaFor(now, []string{pathAlpha, pathDelta}, []string{pathGamma})
	if got := d.Paths(); !reflect.DeepEqual(got, []string{pathAlpha, pathDelta, pathGamma}) {
		t.Fatalf("Delta.Paths() = %v，期望恰三条受影响路径（升序）", got)
	}
	if _, err := index.Apply(dir, d, laterOptions()); err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}

	if got := indexedAt(t, dir, pathBeta); got != fixedNow.Unix() {
		t.Fatalf("未变更文件的 indexed_at_unix 被改成 %d（期望原值 %d）：违反「只碰受影响文件」",
			got, fixedNow.Unix())
	}
	for _, p := range []string{pathAlpha, pathDelta} {
		if got := indexedAt(t, dir, p); got != laterNow.Unix() {
			t.Fatalf("%s 的 indexed_at_unix = %d，期望推进到 %d", p, got, laterNow.Unix())
		}
	}
	if got := indexedAt(t, dir, pathGamma); got != -1 {
		t.Fatalf("被删文件仍留在 files 表（indexed_at_unix=%d）", got)
	}
}

// TestIncrementalAfterDeleteRemovesRows 是 §5.3「删除」这一条：源文件删除后，
// 对应的 cards / cards_fts / relations / files 四处行必须**同时**消失（三处一致 + 水位线）。
func TestIncrementalAfterDeleteRemovesRows(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	if n := countRows(t, dir, `SELECT count(*) FROM cards WHERE id = 'k-gamma'`); n != 1 {
		t.Fatalf("前置不成立：k-gamma 应在库里，实得 %d 行", n)
	}

	now := editedSnapshot()
	res, err := index.Apply(dir, deltaFor(now, []string{pathAlpha, pathDelta}, []string{pathGamma}), laterOptions())
	if err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	for _, q := range []struct {
		what  string
		query string
	}{
		{"cards", `SELECT count(*) FROM cards WHERE id = 'k-gamma'`},
		{"cards_fts", `SELECT count(*) FROM cards_fts WHERE id = 'k-gamma'`},
		{"cards.path", `SELECT count(*) FROM cards WHERE path = '` + pathGamma + `'`},
		{"relations", `SELECT count(*) FROM relations WHERE src_path = '` + pathGamma + `'`},
		{"files", `SELECT count(*) FROM files WHERE path = '` + pathGamma + `'`},
	} {
		if n := countRows(t, dir, q.query); n != 0 {
			t.Fatalf("删除后 %s 仍残留 %d 行", q.what, n)
		}
	}
	meta, err := index.ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta 失败：%v", err)
	}
	if meta.CardCount != res.CardCount || meta.CardCount != len(now.Cards) {
		t.Fatalf("card_count = %d，回执 %d，现态 %d：三者必须一致（否则水位线自指矛盾）",
			meta.CardCount, res.CardCount, len(now.Cards))
	}
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("增量后索引自洽性被破坏：%s / %s", diag.Health, diag.Message)
	}
}

// TestIncrementalIdempotent 是 §5.3「幂等」这一条：同一 Delta 连续应用两次，
// 第二次必须是 no-op —— 动作为 noop、Digest 不变，且**一个字节都没写**
// （用 built_at_unix 没被推进来反证：第二次传的是更晚的时钟）。
func TestIncrementalIdempotent(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	now := editedSnapshot()
	d := deltaFor(now, []string{pathAlpha, pathDelta}, []string{pathGamma})

	if _, err := index.Apply(dir, d, laterOptions()); err != nil {
		t.Fatalf("第一次 Apply 失败：%v", err)
	}
	first := digestOf(t, dir)
	metaFirst, err := index.ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta 失败：%v", err)
	}

	res, err := index.Apply(dir, d, index.Options{
		Now: func() time.Time { return laterNow.Add(3600 * time.Second) }})
	if err != nil {
		t.Fatalf("第二次 Apply 失败：%v", err)
	}
	if res.Action != index.ActionSyncNoop {
		t.Fatalf("第二次动作 = %q，期望 %q（幂等）", res.Action, index.ActionSyncNoop)
	}
	if !res.Before.Equal(res.After) {
		t.Fatalf("no-op 却推进了水位线：%v → %v", res.Before, res.After)
	}
	if got := digestOf(t, dir); got != first {
		t.Fatalf("第二次 Apply 改了内容：\n前 %s\n后 %s", first, got)
	}
	metaSecond, err := index.ReadMeta(dir)
	if err != nil {
		t.Fatalf("ReadMeta 失败：%v", err)
	}
	if metaSecond.BuiltAtUnix != metaFirst.BuiltAtUnix {
		t.Fatalf("no-op 却写了 built_at_unix：%d → %d（应零写入）",
			metaFirst.BuiltAtUnix, metaSecond.BuiltAtUnix)
	}
}

// TestIncrementalSkipsUnchanged 反证「未变更文件零读写」：
// 把一个**内容完全没变**的文件当作受影响项喂进来（写命令重写了同样的字节就是这个形态），
// 索引必须识别出水位线没动而整体 no-op，`indexed_at_unix` 一并保持原值。
func TestIncrementalSkipsUnchanged(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)
	before := digestOf(t, dir)

	res, err := index.Apply(dir, deltaFor(snap, []string{pathBeta}, nil), laterOptions())
	if err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	if res.Action != index.ActionSyncNoop {
		t.Fatalf("内容未变时动作 = %q，期望 %q", res.Action, index.ActionSyncNoop)
	}
	if !res.Changes.Empty() {
		t.Fatalf("内容未变却报出变更：%v", res.Changes)
	}
	if got := digestOf(t, dir); got != before {
		t.Fatal("内容未变却改了索引内容")
	}
	if got := indexedAt(t, dir, pathBeta); got != fixedNow.Unix() {
		t.Fatalf("未变更文件的 indexed_at_unix 被推进到 %d（应保持 %d）", got, fixedNow.Unix())
	}
}

// TestIncrementalRenameIsRemoveAndAdd 反证重命名语义：旧路径进 Removed、新路径按新增处理，
// 结果与「直接对重命名后现态全量构建」等价；卡片 ID 不变而 cards.path / relations.src_path 换值。
func TestIncrementalRenameIsRemoveAndAdd(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	moved := renamedSnapshot()
	const newPath = "domains/ai/knowledge/renamed-k-beta.md"

	res, err := index.Apply(dir, deltaFor(moved, []string{newPath}, []string{pathBeta}), laterOptions())
	if err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	if res.Action != index.ActionSynced {
		t.Fatalf("动作 = %q，期望 %q", res.Action, index.ActionSynced)
	}
	if !reflect.DeepEqual(res.Changes.Added, []string{newPath}) ||
		!reflect.DeepEqual(res.Changes.Removed, []string{pathBeta}) {
		t.Fatalf("重命名应表达为「删除旧路径 + 新增新路径」，实得 %v", res.Changes)
	}
	if n := countRows(t, dir, `SELECT count(*) FROM cards WHERE path = ?`, pathBeta); n != 0 {
		t.Fatalf("旧路径的卡片行仍在（%d 行）", n)
	}
	if n := countRows(t, dir, `SELECT count(*) FROM cards WHERE id = 'k-beta' AND path = ?`, newPath); n != 1 {
		t.Fatalf("新路径下的 k-beta 行数 = %d，期望 1（ID 不变、路径换值）", n)
	}
	if n := countRows(t, dir, `SELECT count(*) FROM relations WHERE src_path = ?`, pathBeta); n != 0 {
		t.Fatalf("旧路径的关系行仍在（%d 行）", n)
	}
	if n := countRows(t, dir, `SELECT count(*) FROM files WHERE path = ?`, pathBeta); n != 0 {
		t.Fatalf("旧路径的文件行仍在（%d 行）", n)
	}
	if got, want := digestOf(t, dir), freshDigestOf(t, moved); got != want {
		t.Fatalf("重命名后的增量结果与全量重建不等价：\n增量 %s\n全量 %s", got, want)
	}
}

// TestSyncConvergesToClean 是 §5.3 的收敛主场景（判据 7）：
// 外部编辑 → Check 判 stale（W22）→ Sync → Check 判 fresh 且零 W22，且与全量重建等价；
// 再 Sync 一次恒为 no-op（幂等）。
func TestSyncConvergesToClean(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	now := editedSnapshot()

	if c := index.Check(dir, currentOf(now)); !c.Stale() || c.Code != index.CodeIndexStale {
		t.Fatalf("收敛前应为 stale + W22，实得 %s / %q", c.Freshness, c.Code)
	}

	res, err := index.Sync(dir, now, laterOptions())
	if err != nil {
		t.Fatalf("Sync 失败：%v", err)
	}
	if res.Action != index.ActionSynced || res.Degraded {
		t.Fatalf("healthy 索引上的 Sync 应是增量收敛，实得 %q（degraded=%v）", res.Action, res.Degraded)
	}
	c := index.Check(dir, currentOf(now))
	if !c.Fresh() {
		t.Fatalf("Sync 后仍非 fresh：%s / %s", c.Freshness, c.Message)
	}
	if c.Code != "" {
		t.Fatalf("Sync 后仍产出诊断码 %q，期望零 W22", c.Code)
	}
	if got, want := digestOf(t, dir), freshDigestOf(t, now); got != want {
		t.Fatalf("Sync 结果与全量重建不等价：\n收敛 %s\n全量 %s", got, want)
	}

	again, err := index.Sync(dir, now, index.Options{
		Now: func() time.Time { return laterNow.Add(7200 * time.Second) }})
	if err != nil {
		t.Fatalf("第二次 Sync 失败：%v", err)
	}
	if again.Action != index.ActionSyncNoop {
		t.Fatalf("第二次 Sync = %q，期望 %q（幂等）", again.Action, index.ActionSyncNoop)
	}
}

// TestSyncDegradesOnMissingIndex 是 §5.3 最后一条：索引缺失时 Sync **退化为全量构建**
// 并**如实说明**（Degraded + 诊断码 W23），退化结果本身仍与 fresh build 等价。
func TestSyncDegradesOnMissingIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)
	snap := sampleSnapshot()

	res, err := index.Sync(dir, snap, fixedOptions())
	if err != nil {
		t.Fatalf("Sync 失败：%v", err)
	}
	if res.Action != index.ActionSyncBuilt {
		t.Fatalf("动作 = %q，期望 %q", res.Action, index.ActionSyncBuilt)
	}
	if !res.Degraded {
		t.Fatal("退化必须留痕：Degraded 应为 true，不得静默")
	}
	if res.Diagnosis.Code != index.CodeIndexMissing {
		t.Fatalf("退化前的诊断码 = %q，期望 %q", res.Diagnosis.Code, index.CodeIndexMissing)
	}
	if got, want := digestOf(t, dir), freshDigestOf(t, snap); got != want {
		t.Fatal("退化构建的结果与 fresh build 不等价")
	}
	if c := index.Check(dir, currentOf(snap)); !c.Fresh() {
		t.Fatalf("退化构建后应为 fresh，实得 %s", c.Freshness)
	}
}

// TestSyncDegradesOnCorruptIndex 是同一条的另一支：索引不可用时退化为**整库重建**
// （W24 + Degraded），且重建后立刻 fresh。
func TestSyncDegradesOnCorruptIndex(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)
	if err := os.WriteFile(filepath.Join(dir, index.DBFileName),
		[]byte("这不是一个 SQLite 文件，只是被外部工具写坏了"), 0o644); err != nil {
		t.Fatalf("制造损坏失败：%v", err)
	}

	res, err := index.Sync(dir, snap, fixedOptions())
	if err != nil {
		t.Fatalf("Sync 失败：%v", err)
	}
	if res.Action != index.ActionSyncRebuilt || !res.Degraded {
		t.Fatalf("动作 = %q（degraded=%v），期望 %q + true",
			res.Action, res.Degraded, index.ActionSyncRebuilt)
	}
	if res.Diagnosis.Code != index.CodeIndexCorrupt {
		t.Fatalf("退化前的诊断码 = %q，期望 %q", res.Diagnosis.Code, index.CodeIndexCorrupt)
	}
	if c := index.Check(dir, currentOf(snap)); !c.Fresh() {
		t.Fatalf("重建后应为 fresh，实得 %s / %s", c.Freshness, c.Message)
	}
}

// TestApplySkipsUnusableIndex 是「权威优先、索引可弃」的机器形态：
// 写命令只有受影响文件、没有全量现态，凭 Delta 建库会造出一个残缺却被判 healthy 的索引。
// 因此 Apply 在索引缺失 / 不可用时**不建库、不改库、不报错**，只如实回 skipped + 诊断。
func TestApplySkipsUnusableIndex(t *testing.T) {
	snap := sampleSnapshot()
	d := deltaFor(snap, []string{pathBeta}, nil)

	missing := filepath.Join(t.TempDir(), index.DirName)
	res, err := index.Apply(missing, d, laterOptions())
	if err != nil {
		t.Fatalf("Apply 不得因索引缺失而失败（写已落盘）：%v", err)
	}
	if res.Action != index.ActionSkipped || !res.Degraded {
		t.Fatalf("动作 = %q（degraded=%v），期望 %q + true",
			res.Action, res.Degraded, index.ActionSkipped)
	}
	if res.Diagnosis.Code != index.CodeIndexMissing {
		t.Fatalf("诊断码 = %q，期望 %q", res.Diagnosis.Code, index.CodeIndexMissing)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Fatal("Apply 在索引缺失时擅自建了目录：残缺索引比没有索引更危险")
	}

	corrupt := buildFixture(t, snap)
	dbPath := filepath.Join(corrupt, index.DBFileName)
	if err := os.WriteFile(dbPath, []byte("坏库"), 0o644); err != nil {
		t.Fatalf("制造损坏失败：%v", err)
	}
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读坏库失败：%v", err)
	}
	res, err = index.Apply(corrupt, d, laterOptions())
	if err != nil {
		t.Fatalf("Apply 不得因索引损坏而失败：%v", err)
	}
	if res.Action != index.ActionSkipped || res.Diagnosis.Code != index.CodeIndexCorrupt {
		t.Fatalf("坏索引上的回执 = %q / %q，期望 skipped / W24", res.Action, res.Diagnosis.Code)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("读坏库失败：%v", err)
	}
	if string(before) != string(after) {
		t.Fatal("Apply 改了不可用索引的字节：应只报不改")
	}
}

// TestSyncActionsClosed 钉住 sync 动作集合恰 5 值，并反证它**没有**污染 065 的 Actions()
// （`eg index build|rebuild` 的动作集合恒 4 值）。
func TestSyncActionsClosed(t *testing.T) {
	want := []string{"synced", "noop", "built", "rebuilt", "skipped"}
	if got := index.SyncActions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SyncActions() = %v，期望 %v", got, want)
	}
	if got := index.Actions(); len(got) != 4 {
		t.Fatalf("Actions() 被本 task 改成 %d 值：build / rebuild 的动作集合应恒 4 值", len(got))
	}
}

// TestIncrementalNeverTouchesMarkdown 反证 P-1：增量与收敛全程**零写权威** ——
// vault 里除 `.index/` 之外的字节在 Apply / Sync 前后逐字相同。
func TestIncrementalNeverTouchesMarkdown(t *testing.T) {
	root := t.TempDir()
	writeVaultFile(t, root, pathAlpha, "---\nid: k-alpha\n---\nbody one\nsecond line\n")
	writeVaultFile(t, root, pathBeta, "---\nid: k-beta\n---\n这是正文 attention 机制\n")
	writeVaultFile(t, root, pathGamma, "---\nid: k-gamma\n---\n运维\n")
	writeVaultFile(t, root, "sources/s-1.md", "外部来源，不该被索引改动\n")

	dir := index.DirPath(root)
	if _, err := index.Build(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	before := snapshotTree(t, root)

	now := editedSnapshot()
	writeVaultFile(t, root, pathDelta, "---\nid: k-delta\n---\n新卡正文 retrieval augmented\n")
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(pathGamma))); err != nil {
		t.Fatalf("删文件失败：%v", err)
	}
	// 这两步是**测试脚手架**在模拟外部改动；改完后重抓基线，用来反证索引自己一个字节都不写。
	before = snapshotTree(t, root)

	if _, err := index.Apply(dir, deltaFor(now, []string{pathAlpha, pathDelta}, []string{pathGamma}),
		laterOptions()); err != nil {
		t.Fatalf("Apply 失败：%v", err)
	}
	if _, err := index.Sync(dir, now, laterOptions()); err != nil {
		t.Fatalf("Sync 失败：%v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("索引写了权威 Markdown：\n前 %v\n后 %v", keysOf(before), keysOf(after))
	}
}
