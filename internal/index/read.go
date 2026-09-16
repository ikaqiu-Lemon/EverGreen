package index

// 读路径取数 API（M5 索引架构合同 §7.4 / §8.4；T-…-067）。
//
// 本文件是**只读**的：`mode=ro` 打开，零 DDL、零写入、零 PRAGMA 变更。它只把
// `cards` / `relations` 两张表如实读回成 build.go 已定义的中性 DTO，**不做任何解释**
// （不判可见性、不打分、不排序成业务序、不产诊断码）——那些都归查询层的既有单点。
//
// 为什么读 API 落在 index 包而不是查询层自己开库：
//   - SQLite 的驱动名、DSN、只读模式、表名与列序是 index 包的**私有实现细节**
//     （schema.go 是建表 DDL 的唯一落点）。查询层若自己 `sql.Open`，就出现了第二处
//     索引物理布局知识，schema 一改两处漂移。
//   - 合同 §13 的依赖方向是 `query → index`（S4 读路径），不是反向：index 仍然
//     不解析 Markdown、不算 hash、不碰权威文件。
//
// **不含正文**：`cards_fts` 的 `body` 列不在本文件的返回值里。读路径要正文（五分区、
// 匹配打分）时一律回权威 Markdown 逐字取字节 —— 索引里的正文副本只服务 FTS 召回，
// 绝不作为「正文是什么」的答案（合同 §1.1 P-2：Markdown 是唯一权威来源）。

import (
	"database/sql"
)

// ReadCards 读回 `cards` 表全部行，按 `id` 升序（与 build.go 的写入序同口径，
// 因此同一个库两次读回逐字相同）。
//
// 返回的 Card 里 `Body` 恒为空串（见本文件顶部「不含正文」），其余列逐字带出：
// 调用方据此判可见性（`deprecated` / `deleted`）、做 `replaced_by` 反查、拿到 `path`
// 之后再决定要不要回 Markdown 解析那一个文件。
//
// `kind` / `validation`（Schema v2）也在带出之列：读路径要按分型收窄（只看知识 / 只看
// 观点）、要按论证进度过滤，靠的就是这两列。它们**必须**在这里读回来 ——
// 增量更新用本包读回的现态行重写派生表（incremental.go），漏读一列就等于把未受影响
// 的行悄悄清空成非法值，这条链路由行级核对与「增量 == 全量」两条判据同时钉住。
func ReadCards(dir string) ([]Card, error) {
	db, err := openDB(dbPathIn(dir), true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id, path, domain, title, status, deprecated, deleted,
  replaced_by, content_hash, mtime_unix, kind, validation FROM ` + TableCards + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Card{}
	for rows.Next() {
		var (
			c                 Card
			deprecated, delet int
		)
		if err := rows.Scan(&c.ID, &c.Path, &c.Domain, &c.Title, &c.Status,
			&deprecated, &delet, &c.ReplacedBy, &c.ContentHash, &c.MTimeUnix,
			&c.Kind, &c.Validation); err != nil {
			return nil, err
		}
		c.Deprecated = deprecated != 0
		c.Deleted = delet != 0
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadRelations 读回 `relations` 表全部正向边，按 `(src_id, verb, dst_id)` 升序。
//
// 表里**只有正向边**（合同 §4.1）：反向视图由调用方按 `dst_id` 反查得到，
// 本函数不补对称条目、不去重合并（`opposing` 单向存储口径 EG-CVG-05 不因索引而变）。
//
// `Reason` 不在返回值里（`relations` 表没有这一列）：关系理由的权威载体是卡片
// frontmatter，调用方拿到 `SrcPath` 后回 Markdown 取逐字原值。
func ReadRelations(dir string) ([]Relation, error) {
	db, err := openDB(dbPathIn(dir), true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	return readRelationsFrom(db)
}

func readRelationsFrom(db *sql.DB) ([]Relation, error) {
	rows, err := db.Query(`SELECT src_id, verb, dst_id, src_path, line FROM ` +
		TableRelations + ` ORDER BY src_id, verb, dst_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Relation{}
	for rows.Next() {
		var r Relation
		if err := rows.Scan(&r.SrcID, &r.Verb, &r.DstID, &r.SrcPath, &r.Line); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CountByKind 按 `cards.kind` **现算**分型行数，返回 kind → 行数。
//
// 三条刻意的取舍：
//
//   - **现算，不落第七个 meta 键**：`index_meta` 的键集合是封闭的六键（schema.go 的
//     MetaKeys 是唯一真源）。分型计数是一个可以由派生表在 O(行数) 内精确导出的量，
//     一旦把它也存进 meta，就多出一处必须与 `cards` 保持同步的冗余事实 —— 而任何一次
//     漏同步（增量重写、外力篡改）都会让摘要里的数字与库里的行对不上，且这种谎连
//     行级核对都抓不到（它只比对 cards / cards_fts 与权威，不比对 meta 里的派生计数）。
//   - **返回全部出现过的 kind，而不是只返回封闭二值**：库里若真出现了第三种 kind
//     （CHECK 约束被外力绕过），调用方能看见它、并据此判定异常；预先按 CardKinds()
//     过滤会把这类事实吞掉。同时对**没有任何行**的合法分型显式补 0（见下），
//     使「分型计数之和 == cards 总行数」在任何语料上都成立。
//   - **只读**：`mode=ro` 打开，与本文件其余读 API 同口径。
//
// 打不开库（缺失 / 截断 / 无权限）时返回 error：调用方**必须**据此把这一格从输出里
// 拿掉，而不是拿 0 冒充「库里确实没有这种卡」—— 那两件事对用户完全不同。
func CountByKind(dir string) (map[string]int, error) {
	db, err := openDB(dbPathIn(dir), true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT kind, count(*) FROM ` + TableCards + ` GROUP BY kind ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	// 合法分型先各补一个 0：空语料 / 只有一种卡的语料下，缺席的那一格是「确有 0 行」
	// 这个可读事实，与「读不到」不同（后者已由上面的 error 表达）。
	out := make(map[string]int, len(CardKinds())+1)
	for _, k := range CardKinds() {
		out[k] = 0
	}
	for rows.Next() {
		var (
			kind string
			n    int
		)
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[kind] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
