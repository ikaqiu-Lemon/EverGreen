package index

// Schema 与版本常量的**唯一**落点（M5 索引架构合同 §3.2 / §3.3 / §4.1 / §4.2 / §4.3）。
//
// 建表 DDL 一律集中在本文件：其它文件只允许调用这里的函数，不得自己拼 `CREATE`。
// 表集合、`index_meta` 键集合、`skipped.kind` 取值域三者都是**封闭集合**，
// 由 schema_test.go 逐字反证；改动集合大小属独立里程碑变更，不是本 task 的自由度。

import (
	"fmt"
	"path/filepath"
	"strings"
)

// IndexSchemaVersion 是索引 Schema 的版本常量（合同 §4.3）。
//
// 语义**唯一**：`index_meta.schema_version != IndexSchemaVersion` ⇒ 索引判为不可用
// （`W24 index_corrupt` 家族的 `schema_version_mismatch` 子因），处置 = **整库重建**。
// **永不写迁移脚本**：迁移会引入「旧库半迁移」这一不可验证态，而重建的代价是
// O(全量 build)，已有性能门槛兜底。M5 首版取 1。
const IndexSchemaVersion = 1

// 目录与文件布局（合同 §3.2 封闭清单）。
const (
	// DirName 是派生索引目录名：整目录 gitignore，可随时 rm -rf。
	DirName = ".index"
	// DBFileName 是唯一权威索引文件（含全部表与水位线）。
	DBFileName = "eg.db"
	// walSuffix / shmSuffix 是 journal_mode=WAL 的必然副产物，不算污染。
	walSuffix = "-wal"
	shmSuffix = "-shm"
)

// DirPath 返回 vault 的 `.index/` 绝对路径。
func DirPath(vaultRoot string) string { return filepath.Join(vaultRoot, DirName) }

// DBPath 返回 vault 的 `.index/eg.db` 绝对路径。
func DBPath(vaultRoot string) string { return filepath.Join(DirPath(vaultRoot), DBFileName) }

// AllowedFiles 是 `.index/` 下**允许存在**的文件名集合（恰 3 个，合同 §3.2）。
// 出现集合外的文件视为外部污染，按 `W24 index_corrupt` 处理（只报不改）。
func AllowedFiles() []string {
	return []string{DBFileName, DBFileName + walSuffix, DBFileName + shmSuffix}
}

// 表名常量（恰 6 张，合同 §4.1，本 task 不得增删）。
const (
	TableIndexMeta = "index_meta"
	TableCards     = "cards"
	TableCardsFTS  = "cards_fts"
	TableRelations = "relations"
	TableFiles     = "files"
	TableSkipped   = "skipped"
)

// TableNames 返回封闭的表集合（恰 6 个，次序即建表次序）。
//
// 机器反证：`SELECT name FROM sqlite_master WHERE type IN ('table','view')` 过滤掉
// FTS5 影子表（`cards_fts_*`）后必须与本函数返回值**逐字相等**（TestSchemaTablesClosed）。
func TableNames() []string {
	return []string{TableIndexMeta, TableCards, TableCardsFTS, TableRelations, TableFiles, TableSkipped}
}

// FTSShadowPrefix 是 FTS5 影子表的名字前缀（比对表集合时按此前缀过滤）。
const FTSShadowPrefix = TableCardsFTS + "_"

// index_meta 的键常量（恰 6 键，合同 §4.2，禁止新增第 7 个键）。
const (
	MetaSchemaVersion = "schema_version"
	MetaHead          = "head"
	MetaFilesHash     = "files_hash"
	MetaTokenizerMode = "tokenizer_mode"
	MetaBuiltAtUnix   = "built_at_unix"
	MetaCardCount     = "card_count"
)

// MetaKeys 返回封闭的键集合（恰 6 个，次序即写入次序）。
func MetaKeys() []string {
	return []string{MetaSchemaVersion, MetaHead, MetaFilesHash,
		MetaTokenizerMode, MetaBuiltAtUnix, MetaCardCount}
}

// 分词档位（合同 §2.6，取值域封闭；档位选择是**单点判定**，见 build.go 的 detectTokenizer）。
const (
	// TokenizerTrigram 是主路 D0：FTS5 `tokenize='trigram'`，覆盖 ASCII 全部 + 中文 ≥ 3 字符；
	// 1 ~ 2 字中文由同表补路列 `bigram_text`（D0b，默认恒开）覆盖。
	TokenizerTrigram = "trigram"
	// TokenizerUnicode61Bigram 是降级第 1 档 D1：`trigram` 建表失败时整表退到 `unicode61`，
	// 中文靠 `bigram_text` 列命中。
	TokenizerUnicode61Bigram = "unicode61_bigram"
	// TokenizerLikeScan 是降级第 2 档 D2：FTS5 整体不可用，`cards_fts` 退化为普通表，
	// 检索侧走 `LIKE '%kw%'` + 内存打分（读路径实现属 T-…-067，本包只如实落档位）。
	TokenizerLikeScan = "like_scan"
)

// TokenizerModes 返回封闭的档位集合（恰 3 个；D0b 是 D0 的同表补路，不单列一档，
// D3「放弃索引回落全量扫描」不落库 —— 那时库根本不可用）。
func TokenizerModes() []string {
	return []string{TokenizerTrigram, TokenizerUnicode61Bigram, TokenizerLikeScan}
}

// SkippedKinds 是 `skipped.kind` 的取值域：与 M2 既有的**恰 2 值**逐字相同
// （合同 §4.1 第 6 行）。索引层**不得**新增第 3 值。
//
// 两个字面量与 M1 的 store 侧回执同源同字面（`file_changed` / `user_block_unsafe`）；
// 这里逐字写死而不是 import store —— §13 禁令第一条：index 不得依赖 store 包。
// M5 的全量构建**没有写入面**，因此本表在 build 后恒 0 行：它的填充方来自写路径
// （M6 的强原子写 / T-…-066 的 sync 跳过登记），本 task 只把形态与取值域钉住。
func SkippedKinds() []string {
	return []string{"file_changed", "user_block_unsafe"}
}

// pragmas 是运行期 PRAGMA 口径（合同 §3.3，`T-…-065` 硬消费）。
//
//   - `journal_mode=WAL`：读不阻塞写；M5 只有单进程写。
//   - `synchronous=NORMAL`：**有意选择** —— 索引是可重建派生，不需要 FULL 的耐久度，
//     崩溃后重建即可；权威数据的耐久性由 Markdown + Git 承担，强原子写属 M-006。
//   - `foreign_keys=ON`：引用完整性由 SQLite 自检。
//   - `user_version` **不使用**：版本走显式表 `index_meta.schema_version`（§4.2）。
func pragmas() []string {
	return []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
}

// baseDDL 返回除 FTS5 虚表以外的建表语句（次序即 TableNames 的次序）。
//
// 说明：`cards` / `relations` 不声明名为 rowid 的列 —— 确定性由**显式 rowid 插入**
// 保证（build.go 按确定序把 rowid 从 1 递增写入），而不是靠 SQLite 自动分配。
func baseDDL() []string {
	return []string{
		`CREATE TABLE ` + TableIndexMeta + ` (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
)`,
		`CREATE TABLE ` + TableCards + ` (
  id           TEXT PRIMARY KEY,
  path         TEXT NOT NULL,
  domain       TEXT NOT NULL,
  title        TEXT NOT NULL,
  status       TEXT NOT NULL,
  deprecated   INTEGER NOT NULL,
  deleted      INTEGER NOT NULL,
  replaced_by  TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  mtime_unix   INTEGER NOT NULL
)`,
		// `replaced_by` 反向查询由本索引 + 一次反查实现，**不新增第 7 张表**（合同 §8.4）。
		`CREATE INDEX cards_replaced_by_idx ON ` + TableCards + `(replaced_by)`,
		`CREATE INDEX cards_path_idx ON ` + TableCards + `(path)`,
		`CREATE TABLE ` + TableRelations + ` (
  src_id   TEXT NOT NULL,
  verb     TEXT NOT NULL,
  dst_id   TEXT NOT NULL,
  src_path TEXT NOT NULL,
  line     INTEGER NOT NULL DEFAULT 0,
  UNIQUE(src_id, verb, dst_id)
)`,
		`CREATE INDEX relations_dst_idx ON ` + TableRelations + `(dst_id)`,
		`CREATE TABLE ` + TableFiles + ` (
  path            TEXT PRIMARY KEY,
  content_hash    TEXT NOT NULL,
  size            INTEGER NOT NULL,
  mtime_unix      INTEGER NOT NULL,
  indexed_at_unix INTEGER NOT NULL
)`,
		`CREATE TABLE ` + TableSkipped + ` (
  path TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN (` + quotedSkippedKinds() + `))
)`,
	}
}

// quotedSkippedKinds 把封闭的 2 值折成 SQL 字面量清单（单一真源，不抄第二份名单）。
func quotedSkippedKinds() string {
	kinds := SkippedKinds()
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, "'"+k+"'")
	}
	return strings.Join(out, ", ")
}

// ftsDDL 返回 `cards_fts` 的建表语句。
//
// 三档形态（合同 §2.6）：
//
//	trigram          → FTS5 虚表，tokenize='trigram'（主路 D0 + 同表补路 D0b）
//	unicode61_bigram → FTS5 虚表，tokenize='unicode61'（降级 D1，中文靠 bigram_text 列）
//	like_scan        → 普通表（降级 D2，FTS5 不可用；表集合仍恰 6 张）
//
// 四列固定：`id UNINDEXED` / `title` / `body` / `bigram_text`。
func ftsDDL(mode string) string {
	switch mode {
	case TokenizerTrigram:
		return fmt.Sprintf(`CREATE VIRTUAL TABLE %s USING fts5(
  id UNINDEXED, title, body, bigram_text, tokenize='trigram'
)`, TableCardsFTS)
	case TokenizerUnicode61Bigram:
		return fmt.Sprintf(`CREATE VIRTUAL TABLE %s USING fts5(
  id UNINDEXED, title, body, bigram_text, tokenize='unicode61'
)`, TableCardsFTS)
	default:
		return fmt.Sprintf(`CREATE TABLE %s (
  id          TEXT NOT NULL,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  bigram_text TEXT NOT NULL
)`, TableCardsFTS)
	}
}
