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
// O(全量 build)，已有性能门槛兜底。
//
// 版本沿革：
//
//	1  M5 首版（cards 十列，cards_fts 四列）。
//	2  Schema v2 knowledge / opinion 分型：`cards` 与 `cards_fts` 各加 `kind` 与
//	   `validation` 两列（设计 §7 决策记录 D-4 —— 加判别列，不分表）。v1 旧库遇到 v2
//	   二进制即判 schema_version_mismatch 并整库重建：这正是「不写迁移脚本」的兑现方式。
const IndexSchemaVersion = 2

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

// —— 产物分型：`cards.kind` 与 `cards.validation` 的取值域（Schema v2 §7 / D-4）——
//
// 判别列的存在理由：知识卡与观点共用 `cards` / `cards_fts` 两张表（**不**分表），
// 排序全序、分页、`relations` 跨类型 join 因此只需要一份实现，检索面收窄退化为
// 一个 `WHERE kind = ?`。表数量保持恰 6 张。
//
// 与 `SkippedKinds()` 同一处理：字面量在本包**逐字写死**而不是 import `internal/model`
// 的枚举 —— `Card` 是中性 DTO（字段全是标量，不引本仓任何结构体），且 §13 依赖禁令要求
// 索引层不长出对上游包的依赖。两边字面量若有分叉，调用方把观点喂进来的第一刻就会被
// 下面的 CHECK 当场拒绝（不会静默漂移），判据见 tests/…/index/schema_v2_kind_test.go。
const (
	// CardKindKnowledge 是知识卡（`domains/<域>/knowledge/k-*.md`）：validation 恒为空串。
	CardKindKnowledge = "knowledge"
	// CardKindOpinion 是观点（`domains/<域>/opinions/o-*.md`）：validation 必为封闭三值之一。
	CardKindOpinion = "opinion"
)

// CardKinds 返回封闭的产物分型集合（恰 2 个；第三值属独立里程碑变更）。
func CardKinds() []string { return []string{CardKindKnowledge, CardKindOpinion} }

// 观点的论证进度（Schema v2 §6.1，封闭三值；与 `internal/model` 的 Validation 枚举
// 逐字同值同序）。它与 `status` 是两个**正交**维度：rejected 的观点仍可以是 active
// （「已确认不成立」本身是知识资产），因此索引层不做任何 status ↔ validation 的推导。
const (
	ValidationPending   = "pending"
	ValidationValidated = "validated"
	ValidationRejected  = "rejected"
)

// CardValidations 返回封闭的论证进度集合（恰 3 个，次序与状态机推进方向一致）。
func CardValidations() []string {
	return []string{ValidationPending, ValidationValidated, ValidationRejected}
}

// validCardKind / validCardValidation 是**唯一**的取值域判定（写入口与 DDL 共用同一名单）。
func validCardKind(kind string) bool {
	for _, k := range CardKinds() {
		if kind == k {
			return true
		}
	}
	return false
}

// validCardValidation 判 (kind, validation) 这一**组合**是否合法：
// knowledge 恒空串、opinion 恒封闭三值之一（空串亦非法）。
//
// 交叉判定写成一个函数而不是两条独立规则：validation 的合法性离开 kind 无从谈起，
// 拆成两处必然出现「各自都通过、组合却自相矛盾」的行。
func validCardValidation(kind, validation string) bool {
	switch kind {
	case CardKindKnowledge:
		return validation == ""
	case CardKindOpinion:
		for _, v := range CardValidations() {
			if validation == v {
				return true
			}
		}
	}
	return false
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
  mtime_unix   INTEGER NOT NULL,
  kind         TEXT NOT NULL,
  validation   TEXT NOT NULL,
  CHECK (kind IN (` + quotedList(CardKinds()) + `)),
  CHECK ((kind = '` + CardKindKnowledge + `' AND validation = '')
      OR (kind = '` + CardKindOpinion + `' AND validation IN (` +
			quotedList(CardValidations()) + `)))
)`,
		// 两条 CHECK 分开写而不是合成一条：第一条单独钉住 kind 取值域，
		// 即便将来 validation 的规则调整，「kind 恰两值」这一格也不会跟着松。
		// 约束落在**库上**而不只是 Go 侧：绕过 writeAll 的任何写入（外部进程、将来的
		// 第二条写路径）都不得留下 kind 缺失或 (kind, validation) 自相矛盾的行。
		//
		// 新列一律**追加在尾部**：既有列的序号是显式 rowid 插入与各处 SELECT 列序的
		// 共同前提，插队会让「同一快照两次构建逐字等价」以最难查的方式变红。
		//
		// `replaced_by` 反向查询由本索引 + 一次反查实现，**不新增第 7 张表**（合同 §8.4）。
		`CREATE INDEX cards_replaced_by_idx ON ` + TableCards + `(replaced_by)`,
		`CREATE INDEX cards_path_idx ON ` + TableCards + `(path)`,
		// 按分型收窄检索面（`WHERE kind = ?`，设计 §7 理由 (c)）的支撑索引：
		// 没有它，「只搜观点 / 只搜知识」会退化成全表扫。
		`CREATE INDEX cards_kind_idx ON ` + TableCards + `(kind)`,
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
func quotedSkippedKinds() string { return quotedList(SkippedKinds()) }

// quotedList 把一个封闭取值域折成 `'a', 'b'` 形态的 SQL 字面量清单。
//
// 所有 CHECK 约束的取值域都经这里生成：DDL 里**不允许**出现第二份手抄名单，
// 否则「Go 常量」与「库上约束」会各自演化，而两者不一致时最先撞上的是用户的库。
// 取值域成员本身是本包内的常量（不含单引号），因此不做转义。
func quotedList(vals []string) string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, "'"+v+"'")
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
// 六列固定（Schema v2）：`id UNINDEXED` / `title` / `body` / `bigram_text` /
// `kind UNINDEXED` / `validation UNINDEXED`。
//
// 判别两列**必须 UNINDEXED**：一旦进倒排，裸 `MATCH 'opinion'` 会命中每一条观点、
// `MATCH 'pending'` 会命中每一条待验证观点 —— 检索结果被判别值污染，而且是那种
// 「看着像相关命中」的污染。按分型收窄检索面靠 `WHERE kind = ?`（UNINDEXED 列照样可
// 比较、可取回），不靠把判别值塞进全文索引。三档形态都带这两列：降级档位不得因为
// 「反正是退化路径」而少一列，否则 D2 下的读路径拿不到分型，只能重新回权威文件。
func ftsDDL(mode string) string {
	switch mode {
	case TokenizerTrigram:
		return fmt.Sprintf(`CREATE VIRTUAL TABLE %s USING fts5(
  id UNINDEXED, title, body, bigram_text, kind UNINDEXED, validation UNINDEXED,
  tokenize='trigram'
)`, TableCardsFTS)
	case TokenizerUnicode61Bigram:
		return fmt.Sprintf(`CREATE VIRTUAL TABLE %s USING fts5(
  id UNINDEXED, title, body, bigram_text, kind UNINDEXED, validation UNINDEXED,
  tokenize='unicode61'
)`, TableCardsFTS)
	default:
		return fmt.Sprintf(`CREATE TABLE %s (
  id          TEXT NOT NULL,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  bigram_text TEXT NOT NULL,
  kind        TEXT NOT NULL,
  validation  TEXT NOT NULL
)`, TableCardsFTS)
	}
}
