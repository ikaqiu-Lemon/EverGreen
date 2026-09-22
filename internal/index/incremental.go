package index

// 增量更新与收敛（合同 §5.3 `eg index sync` 收敛语义 + §4.4 确定性等价口径）。
//
// 本文件是**写路径可调用的窄 API**：
//
//	Apply(dir, Delta, opt)   ——「我刚落盘了这些文件」：按受影响文件集合增量更新索引
//	Sync(dir, Snapshot, opt) ——「以现态为准把索引推到 fresh」：三向 diff 后增量收敛
//
// 五条硬语义（逐条对应 Task 的 Acceptance）：
//
//	① **Markdown 是唯一权威源**：本文件不读、不写、不 stat 任何权威 Markdown —— 一切
//	   现态事实都由调用方扫描后作为中性 DTO 喂进来（合同 §13 禁令第一条）。
//	② **权威优先，索引可弃**：Apply 永不因为索引问题而让写命令失败。索引缺失 / 不可用时
//	   Apply 返回 ActionSkipped + 如实诊断（**不**建库、**不**改库、**不**回滚 Markdown）；
//	   索引写入自身报错时返回 error，调用方的合同义务是「降级为 W22 并继续退 0」。
//	③ **未变更文件零读写**：增量的输入只含受影响文件；未变更文件的 Markdown 一个字节不读，
//	   `files.indexed_at_unix` 原值不动（fileRow 按行携带索引时间就是为了这条）。
//	④ **与全量重建逐字等价**：增量不另写一套 SQL —— 它把「库内保留行 + 本次受影响行」合并成
//	   完整行集合后，交给 build.go 里那**同一个** writeAll 按确定序重写派生行空间。
//	   因此等价性不依赖两套代码保持同步（TestIncrementalEqualsFullRebuild）。
//	⑤ **幂等**：同一 Delta 连续应用两次，第二次是 no-op（水位线与内容都不变）。
//
// 关于「增量」的诚实口径（不许含糊）：**增量的是权威源读取与解析**（唯一昂贵项），
// 派生行空间在一个事务内按确定序整体重排。这样做的理由是 rowid 必须恒等于「确定序下的
// 序号」（build.go 的确定性口径），一旦卡集合或关系边集合发生增删，rowid 就会整体位移；
// 与其在 SQL 里手写一套「位移重排」（不可验证、易漂移），不如复用唯一写入口。
// 派生行重写的性能门槛评估属 T-…-068，本 task 不提前优化。
//
// 明确不做：不注册任何 CLI 子命令（`eg index sync` 的命令面属本 task 的阶段 B）、
// 不接读路径、不产 `Q5`、不做排序 / 分页 / bench、不引入任何 M6 能力（锁 / 事务日志 / 退出码 5）。

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
)

// SyncAction 是一次增量 / 收敛实际做了什么（机器可读，进 `eg index sync` 的 data.action）。
//
// 它与 rebuild.go 的 Action 是**两个不同命令面**的封闭集合：Action 描述
// `eg index build|rebuild`（恰 4 值），SyncAction 描述 `eg index sync` 与写后同步（恰 5 值）。
// 字面量上的重叠（built / rebuilt / noop）是语义相同所致，不是同一个枚举。
type SyncAction string

const (
	// ActionSynced 索引可用，本次按受影响文件集合增量更新并推进水位线。
	ActionSynced SyncAction = "synced"
	// ActionSyncNoop 现态与索引一致（水位线相等且零变更）：一个字节都没写。
	ActionSyncNoop SyncAction = "noop"
	// ActionSyncBuilt 索引原本缺失：退化为全量构建（如实交代，不静默）。
	ActionSyncBuilt SyncAction = "built"
	// ActionSyncRebuilt 索引原本不可用：退化为整库重建（如实交代，不静默）。
	ActionSyncRebuilt SyncAction = "rebuilt"
	// ActionSkipped 索引缺失 / 不可用，且本次**不允许**退化为全量（写后同步只有受影响
	// 文件、没有全量现态，凭它建库会造出一个残缺索引）：本次不动索引，等 sync / rebuild。
	ActionSkipped SyncAction = "skipped"
)

// SyncActions 返回封闭的动作集合（恰 5 值）。
func SyncActions() []string {
	return []string{string(ActionSynced), string(ActionSyncNoop), string(ActionSyncBuilt),
		string(ActionSyncRebuilt), string(ActionSkipped)}
}

// Delta 是一次增量更新的完整输入：**受影响文件集合**上的现态行 + 已消失的路径。
//
// 由写命令在「Markdown 成功落盘（必要时 commit 成功）之后」构造：
//
//	新增 / 修改：把该文件解析出的现态行放进 Cards / Relations / Files（Skipped 如有）；
//	删除       ：把路径放进 Removed；
//	重命名     ：旧路径进 Removed，新路径按「新增」处理（卡片 ID 不变，cards.path 换值）。
//
// 未出现在本结构里的路径 = 未变更：它们的索引行原样保留，`indexed_at_unix` 一并保留。
type Delta struct {
	// Head 是本次落盘后的 Git HEAD（非 git 仓空串）；水位线的另一半来自 Files。
	Head string
	// Cards / Relations / Files / Skipped 只包含**受影响路径**上的现态行（允许乱序）。
	Cards     []Card
	Relations []Relation
	Files     []File
	Skipped   []SkippedFile
	// Removed 是已不存在的 vault 内相对路径（删除、或重命名的旧路径）。
	Removed []string
	// Blocks is the complete current Note sidecar projection. It stays outside
	// the SQLite transaction and is independently rebuildable.
	Blocks []BlockDocument
}

// Empty 报告 Delta 是否不含任何受影响路径（此时 Apply 只可能推进 head，或直接 no-op）。
func (d Delta) Empty() bool {
	return len(d.Files) == 0 && len(d.Cards) == 0 && len(d.Relations) == 0 &&
		len(d.Skipped) == 0 && len(d.Removed) == 0
}

// Paths 返回本次受影响的全部路径（含 Removed），升序去重 —— 「只碰受影响文件」的可断言形态。
func (d Delta) Paths() []string {
	set := map[string]bool{}
	for _, f := range d.Files {
		set[f.Path] = true
	}
	for _, c := range d.Cards {
		set[c.Path] = true
	}
	for _, r := range d.Relations {
		set[r.SrcPath] = true
	}
	for _, s := range d.Skipped {
		set[s.Path] = true
	}
	for _, p := range d.Removed {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// SyncResult 是一次增量 / 收敛的回执。
type SyncResult struct {
	Action SyncAction
	// Degraded 说明本次是否从「增量」退化为「全量构建 / 整库重建 / 放弃」。
	// 退化恒伴随 Diagnosis 里的分因，**不得**静默（合同 §5.3 最后一条）。
	Degraded bool
	// Diagnosis 是动手前的只读体检结论。
	Diagnosis Diagnosis
	// Changes 是本次处理的三向 diff（Apply 由 Delta 推出，Sync 由现态清单 diff 得出）。
	Changes ChangeSet
	// Before / After 是水位线的前后值（noop 时两者相等）。
	Before Watermark
	After  Watermark
	// 收敛后的行数（noop / skipped 时为库内现值 / 零值）。
	CardCount     int
	RelationCount int
	FileCount     int
	SkippedCount  int
	BlockCount    int
	BlockAction   string
	BlockChanges  BlockChanges
	// 与 Result 同型的如实交代：按主键只收一份时被丢弃的重复行条数。
	DroppedDuplicateCards     int
	DroppedDuplicateRelations int
}

// Apply 按受影响文件集合增量更新 `dir`（= vault/.index）。
//
// 三种走向（都不写权威 Markdown、都不失败于「索引不好」这件事本身）：
//
//	索引 healthy 且现态与索引已一致  → ActionSyncNoop，零写入
//	索引 healthy 且有变更            → ActionSynced，单事务内重写派生行 + 推进水位线
//	索引 missing / corrupt           → ActionSkipped + Degraded + 诊断（不建库、不改库）
//
// 返回 error 只表示**索引写入本身**失败（磁盘满、库被并发改坏等）。调用方（写命令）
// 的合同义务：Markdown 已成功落盘，因此**不得**回滚、**不得**改变退出码，只把它降级成
// 一条 `W22` 提示用户后续 `eg index sync`。
func Apply(dir string, d Delta, opt Options) (*SyncResult, error) {
	diag := Inspect(dir)
	if !diag.Usable() {
		// 只有受影响文件、没有全量现态：凭 Delta 建库会造出一个「只有几张卡」的残缺索引，
		// 那比没有索引更危险（它会被 status 判成 healthy）。因此如实跳过。
		return &SyncResult{
			Action: ActionSkipped, Degraded: true, Diagnosis: diag,
			Changes: ChangeSet{Added: pathsOfNames(d.Files), Removed: sortedCopy(d.Removed)},
		}, nil
	}
	res, err := applyDelta(dir, d, diag, opt)
	if err != nil {
		return nil, err
	}
	return applyResultBlocks(dir, d.Blocks, res)
}

// Sync 以**全量现态快照**把索引收敛到 fresh（`eg index sync` 的语义底座，合同 §5.3）。
//
// 走向：
//
//	索引 missing  → 退化为全量构建（ActionSyncBuilt + Degraded，如实说明）
//	索引 corrupt  → 退化为整库重建（ActionSyncRebuilt + Degraded，如实说明）
//	索引 healthy  → 三向 diff：零变更且 head 一致即 noop；否则只重建受影响文件对应的行
//
// 幂等：连续两次 Sync 第二次恒为 ActionSyncNoop。
// 等价：Sync 后的索引与 fresh full build 在 Digest 口径下逐字等价。
func Sync(dir string, snap Snapshot, opt Options) (*SyncResult, error) {
	diag := Inspect(dir)
	switch diag.Health {
	case HealthMissing:
		res, err := Build(dir, snap, opt)
		return degradedResult(ActionSyncBuilt, diag, res, snap, err)
	case HealthCorrupt:
		res, err := Rebuild(dir, snap, opt)
		return degradedResult(ActionSyncRebuilt, diag, res, snap, err)
	}

	indexedFiles, err := ReadFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("读 %s 表失败：%w", TableFiles, err)
	}
	cs := DiffFiles(indexedFiles, snap.Files)
	before := WatermarkOf(diag.Meta)
	after := WatermarkFrom(snap.Head, snap.Files)
	if cs.Empty() && before.Equal(after) {
		return syncResultBlocks(
			dir, snap.Blocks, noopResult(diag, cs, before, len(indexedFiles)))
	}
	res, err := applyDelta(dir, deltaFromSnapshot(snap, cs), diag, opt)
	if err != nil {
		return nil, err
	}
	return syncResultBlocks(dir, snap.Blocks, res)
}

// deltaFromSnapshot 从全量现态快照里切出「只含受影响路径」的 Delta（Sync 的中间形态）。
//
// 这一步是 §5.3「只重建受影响文件对应的行」的落点：未变更路径的行不进 Delta，
// 因此它们在 applyDelta 里走「库内保留」分支，不会被重新派生。
func deltaFromSnapshot(snap Snapshot, cs ChangeSet) Delta {
	affected := map[string]bool{}
	for _, p := range cs.Affected() {
		affected[p] = true
	}
	d := Delta{
		Head: snap.Head, Removed: append([]string(nil), cs.Removed...),
		Blocks: append([]BlockDocument(nil), snap.Blocks...),
	}
	for _, c := range snap.Cards {
		if affected[c.Path] {
			d.Cards = append(d.Cards, c)
		}
	}
	for _, r := range snap.Relations {
		if affected[r.SrcPath] {
			d.Relations = append(d.Relations, r)
		}
	}
	for _, f := range snap.Files {
		if affected[f.Path] {
			d.Files = append(d.Files, f)
		}
	}
	for _, s := range snap.Skipped {
		if affected[s.Path] {
			d.Skipped = append(d.Skipped, s)
		}
	}
	return d
}

// applyDelta 是增量的唯一实现：单事务内「保留行 + 受影响行 → writeAll 重写」。
//
// 步骤（全程只碰 `.index/`）：
//  1. 只读回库内全部派生行（cards / cards_fts 的 body / relations / files / skipped）；
//  2. 按受影响路径集合剔除待重建的行（Removed 与新增 / 修改的路径都在其中）；
//  3. 与 Delta 的现态行合并，得到完整行集合；
//  4. 若合并结果与库内**完全一致**且水位线不动 → 回滚事务，报 noop（零写入、幂等）；
//  5. 否则清空派生表后用 writeAll 按确定序重写，并在同一事务更新 index_meta 的
//     head / files_hash / card_count（水位线与内容同事务提交，绝不分两步）。
func applyDelta(dir string, d Delta, diag Diagnosis, opt Options) (*SyncResult, error) {
	dbPath := dbPathIn(dir)
	db, err := openDB(dbPath, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	for _, p := range pragmas() {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("%s 失败：%w", p, err)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	rolledBack := false
	defer func() {
		if !rolledBack {
			_ = tx.Rollback() // Commit 之后的 Rollback 是 no-op；失败路径靠它兜底
		}
	}()

	cur, err := readStateTx(tx)
	if err != nil {
		return nil, err
	}

	affected := map[string]bool{}
	for _, p := range d.Paths() {
		affected[p] = true
	}

	// —— 合并：未受影响的行原样留下（含 indexed_at_unix），受影响的行取 Delta 的现态 ——
	keptFiles := make([]fileRow, 0, len(cur.files))
	for _, f := range cur.files {
		if !affected[f.Path] {
			keptFiles = append(keptFiles, f)
		}
	}
	built := opt.now().UTC().Unix()
	files := append(keptFiles, stampFiles(sortedFiles(d.Files), built)...)
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	cards := make([]Card, 0, len(cur.cards)+len(d.Cards))
	for _, c := range cur.cards {
		if !affected[c.Path] {
			cards = append(cards, c)
		}
	}
	cards = append(cards, d.Cards...)

	rels := make([]Relation, 0, len(cur.relations)+len(d.Relations))
	for _, r := range cur.relations {
		if !affected[r.SrcPath] {
			rels = append(rels, r)
		}
	}
	rels = append(rels, d.Relations...)

	skipped := make([]SkippedFile, 0, len(cur.skipped)+len(d.Skipped))
	for _, s := range cur.skipped {
		if !affected[s.Path] {
			skipped = append(skipped, s)
		}
	}
	skipped = append(skipped, d.Skipped...)

	cards, dupCards := dedupCards(sortedCards(cards))
	rels, dupRels := dedupRelations(sortedRelations(rels))
	skipped = sortedSkipped(skipped)
	for _, s := range skipped {
		if !validSkippedKind(s.Kind) {
			return nil, fmt.Errorf("skipped.kind = %q 不在封闭取值域 %v 内", s.Kind, SkippedKinds())
		}
	}

	before := WatermarkOf(cur.meta)
	after := Watermark{Head: d.Head, FilesHash: FilesHash(fileRowsToFiles(files))}
	cs := DiffFiles(fileRowsToFiles(cur.files), fileRowsToFiles(files))

	// 幂等与「零变更零写入」：内容与水位线都没动就一个字节都不写。
	if before.Equal(after) && cs.Empty() && len(cards) == len(cur.cards) &&
		len(rels) == len(cur.relations) && len(skipped) == len(cur.skipped) {
		rolledBack = true
		if err := tx.Rollback(); err != nil {
			return nil, err
		}
		res := noopResult(diag, cs, before, len(cur.files))
		res.CardCount, res.RelationCount = len(cur.cards), len(cur.relations)
		res.SkippedCount = len(cur.skipped)
		return res, nil
	}

	meta := Meta{
		SchemaVersion: IndexSchemaVersion,
		Head:          d.Head,
		FilesHash:     after.FilesHash,
		// tokenizer_mode 是**建库时的单点判定**（合同 §2.6）：增量绝不重新探测，原值沿用。
		TokenizerMode: cur.meta.TokenizerMode,
		BuiltAtUnix:   built,
		CardCount:     len(cards),
	}
	if err := clearDerived(tx); err != nil {
		return nil, err
	}
	if err := writeAll(tx, meta, cards, rels, files, skipped); err != nil {
		return nil, err
	}
	rolledBack = true
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// 与 build 同口径：把 WAL 回灌主库，保证后续只读打开不依赖 WAL 残帧（失败不算失败）。
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = err
	}

	return &SyncResult{
		Action: ActionSynced, Diagnosis: diag, Changes: cs,
		Before: before, After: after,
		CardCount: len(cards), RelationCount: len(rels),
		FileCount: len(files), SkippedCount: len(skipped),
		DroppedDuplicateCards: dupCards, DroppedDuplicateRelations: dupRels,
	}, nil
}

// clearDerived 清空六张表（派生数据 + 水位线），为 writeAll 的确定序重写让位。
//
// 只在**事务内**执行：中途失败即整体回滚，库不会停在「清空了但没写回」的半态。
// 表集合恒取 TableNames()（单一真源）；FTS5 虚表的 `DELETE FROM` 会连带清掉影子表内容。
func clearDerived(tx *sql.Tx) error {
	for _, table := range TableNames() {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return fmt.Errorf("清空 %s 失败：%w", table, err)
		}
	}
	return nil
}

// dbState 是库内派生行的内存镜像（增量合并的「保留行」来源）。
type dbState struct {
	meta      Meta
	cards     []Card
	relations []Relation
	files     []fileRow
	skipped   []SkippedFile
}

// readStateTx 在事务内读回全部派生行。
//
// 卡片正文（Body）只存在于 `cards_fts`，因此从那里读回；`bigram_text` **不读** ——
// 它由 writeAll 用 BigramText(title+"\n"+body) 确定性派生，读回来反而会多一份可漂移的真源。
//
// **无损往返合同**：除上述确定性派生列之外，`cards` 的每一个语义列都必须在这里读回来。
// 理由在 applyDelta 的形态里：它把「库内现态 − 受影响路径」当作**保留行**，与 Delta 里的
// 现态行合并后**整体重写**六张表。任何漏读的列，都会在重写时被写成零值 ——
// 未受影响的行明明没人碰过，却在增量之后丢了字段。加列时这里是必改点之一
// （另外三处：writeAll 的两条 INSERT、exportRows 的等价口径、rowlevel 的逐列核对）。
func readStateTx(tx *sql.Tx) (dbState, error) {
	var st dbState
	meta, err := readMetaTx(tx)
	if err != nil {
		return st, err
	}
	st.meta = meta

	bodies := map[string]string{}
	rows, err := tx.Query(`SELECT id, body FROM ` + TableCardsFTS)
	if err != nil {
		return st, fmt.Errorf("读 %s 失败：%w", TableCardsFTS, err)
	}
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			_ = rows.Close()
			return st, err
		}
		bodies[id] = body
	}
	if err := closeRows(rows); err != nil {
		return st, err
	}

	rows, err = tx.Query(`SELECT id, path, domain, title, status, deprecated, deleted,
		replaced_by, content_hash, mtime_unix, kind, validation FROM ` + TableCards +
		` ORDER BY rowid`)
	if err != nil {
		return st, fmt.Errorf("读 %s 失败：%w", TableCards, err)
	}
	for rows.Next() {
		var c Card
		var dep, del int
		if err := rows.Scan(&c.ID, &c.Path, &c.Domain, &c.Title, &c.Status,
			&dep, &del, &c.ReplacedBy, &c.ContentHash, &c.MTimeUnix,
			&c.Kind, &c.Validation); err != nil {
			_ = rows.Close()
			return st, err
		}
		c.Deprecated, c.Deleted = dep != 0, del != 0
		c.Body = bodies[c.ID]
		st.cards = append(st.cards, c)
	}
	if err := closeRows(rows); err != nil {
		return st, err
	}

	rows, err = tx.Query(`SELECT src_id, verb, dst_id, src_path, line FROM ` +
		TableRelations + ` ORDER BY rowid`)
	if err != nil {
		return st, fmt.Errorf("读 %s 失败：%w", TableRelations, err)
	}
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.SrcID, &r.Verb, &r.DstID, &r.SrcPath, &r.Line); err != nil {
			_ = rows.Close()
			return st, err
		}
		st.relations = append(st.relations, r)
	}
	if err := closeRows(rows); err != nil {
		return st, err
	}

	rows, err = tx.Query(filesQuery)
	if err != nil {
		return st, fmt.Errorf("读 %s 失败：%w", TableFiles, err)
	}
	st.files, err = scanFileRows(rows)
	if err != nil {
		return st, err
	}

	rows, err = tx.Query(`SELECT path, kind FROM ` + TableSkipped + ` ORDER BY path`)
	if err != nil {
		return st, fmt.Errorf("读 %s 失败：%w", TableSkipped, err)
	}
	for rows.Next() {
		var s SkippedFile
		if err := rows.Scan(&s.Path, &s.Kind); err != nil {
			_ = rows.Close()
			return st, err
		}
		st.skipped = append(st.skipped, s)
	}
	if err := closeRows(rows); err != nil {
		return st, err
	}
	return st, nil
}

// filesQuery 是 `files` 表的唯一读回语句（含 indexed_at_unix，按 path 升序）。
const filesQuery = `SELECT path, content_hash, size, mtime_unix, indexed_at_unix FROM ` +
	TableFiles + ` ORDER BY path`

// readFileRowsFrom 以只读连接读回 `files` 表全部行。
func readFileRowsFrom(db *sql.DB) ([]fileRow, error) {
	rows, err := db.Query(filesQuery)
	if err != nil {
		return nil, fmt.Errorf("读 %s 失败：%w", TableFiles, err)
	}
	return scanFileRows(rows)
}

// scanFileRows 把 files 查询结果扫成 fileRow 切片（并负责关闭 rows）。
func scanFileRows(rows *sql.Rows) ([]fileRow, error) {
	var out []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.Path, &f.ContentHash, &f.Size, &f.MTimeUnix, &f.IndexedAtUnix); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, f)
	}
	return out, closeRows(rows)
}

// readMetaTx 在事务内读回六键元数据（键集合封闭校验与 readMetaFrom 同口径）。
func readMetaTx(tx *sql.Tx) (Meta, error) {
	rows, err := tx.Query(`SELECT key, value FROM ` + TableIndexMeta)
	if err != nil {
		return Meta{}, err
	}
	got := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return Meta{}, err
		}
		got[k] = v
	}
	if err := closeRows(rows); err != nil {
		return Meta{}, err
	}
	return metaFromKV(got)
}

// closeRows 统一收敛「先看迭代错误再关闭」的样板。
func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

// fileRowsToFiles 去掉 indexed_at_unix 这一列（水位线口径里它不参与，合同 §4.4）。
func fileRowsToFiles(in []fileRow) []File {
	out := make([]File, 0, len(in))
	for _, r := range in {
		out = append(out, r.File)
	}
	return out
}

// dbPathIn 返回 `.index/` 目录下的库文件路径（避免各处重复拼接）。
func dbPathIn(dir string) string { return filepath.Join(dir, DBFileName) }

// noopResult 组装「零变更零写入」的回执。
func noopResult(diag Diagnosis, cs ChangeSet, w Watermark, fileCount int) *SyncResult {
	return &SyncResult{
		Action: ActionSyncNoop, Diagnosis: diag, Changes: cs,
		Before: w, After: w,
		CardCount: diag.Meta.CardCount, FileCount: fileCount,
	}
}

// degradedResult 组装「退化为全量构建 / 整库重建」的回执（Degraded 恒 true，诊断随行）。
func degradedResult(action SyncAction, diag Diagnosis, res *Result,
	snap Snapshot, err error) (*SyncResult, error) {
	if err != nil {
		return nil, err
	}
	return &SyncResult{
		Action: action, Degraded: true, Diagnosis: diag,
		Changes:   ChangeSet{Added: pathsOfNames(snap.Files)},
		Before:    Watermark{},
		After:     WatermarkOf(res.Meta),
		CardCount: res.CardCount, RelationCount: res.RelationCount,
		FileCount: res.FileCount, SkippedCount: res.SkippedCount,
		BlockCount: res.BlockCount, BlockAction: res.BlockAction,

		DroppedDuplicateCards:     res.DroppedDuplicateCards,
		DroppedDuplicateRelations: res.DroppedDuplicateRelations,
	}, nil
}

func syncResultBlocks(dir string, blocks []BlockDocument,
	res *SyncResult,
) (*SyncResult, error) {
	synced, err := SyncBlocks(dir, blocks)
	if err != nil {
		return nil, err
	}
	res.BlockCount = synced.Count
	res.BlockAction = synced.Action
	res.BlockChanges = synced.Changes
	if res.Action == ActionSyncNoop && synced.Action != BlockActionNoop {
		res.Action = ActionSynced
	}
	return res, nil
}

func applyResultBlocks(dir string, blocks []BlockDocument,
	res *SyncResult,
) (*SyncResult, error) {
	synced, err := ApplyBlockDocuments(dir, blocks)
	if err != nil {
		return nil, err
	}
	res.BlockCount = synced.Count
	res.BlockAction = synced.Action
	res.BlockChanges = synced.Changes
	if res.Action == ActionSyncNoop && synced.Action != BlockActionNoop {
		res.Action = ActionSynced
	}
	return res, nil
}

// pathsOfNames 取一批文件的路径（升序）。
func pathsOfNames(files []File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

// sortedCopy 返回升序副本（不改调用方切片）。
func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
