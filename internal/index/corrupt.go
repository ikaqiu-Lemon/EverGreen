package index

// 损坏检测（合同 §6.1 总纲 + §6.3 诊断码分配表）。
//
// **只报不改**：本文件不删库、不建库、不改一个字节 —— 处置（重建）由 rebuild.go 承担，
// 降级（回落全量 Markdown 扫描）由读路径承担（T-…-067）。因此 Inspect 一律以**只读**
// 方式打开库（`mode=ro`）。
//
// 一条最高约束在这里落地：**索引不可用时必须降级为全量扫描而非报错退出**。
// 所以 Inspect 的返回值里没有 error 表达「索引坏了」这件事 —— 坏了是一个**诊断**
// （W23 / W24），不是一个失败。调用方据此产出 warning 并继续，不得据此退非 0。
//
// M5 检测的四类损坏（Task Scope 逐条）：
//
//	① IndexSchemaVersion 不匹配   → schema_version_mismatch
//	② 文件截断 / 非法头           → truncated_file
//	③ PRAGMA integrity_check 非 ok → integrity_check_failed
//	④ 水位线自指矛盾              → watermark_self_contradiction
//
// 另有三类「同族」不可用形态一并如实分因，避免它们被静默归到上面四类里：
// `.index/` 混入非法文件（unexpected_file）、schema 不完整（schema_incomplete）、
// 打不开 / 元数据不可解析（open_failed）。
//
// **不做**：陈旧判定（`W22 index_stale`，需要与现态比对水位线）属 T-…-066。

import (
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 诊断码（合同 §6.3；M5 新增恰 5 条，本包只产出其中两条 —— W22 属 066、W25 / Q5 属读路径）。
const (
	// CodeIndexMissing 是 `.index/` 或 `.index/eg.db` 不存在。
	CodeIndexMissing = "W23"
	// CodeIndexCorrupt 是索引存在但不可用（打不开 / integrity 非 ok / 版本不匹配 /
	// card_count 不自洽 / 目录混入非法文件 / 文件截断）。
	CodeIndexCorrupt = "W24"
)

// Health 是本 task 能判定的索引健康度（**恰 3 值**）。
//
// `stale`（索引可用但与现态不一致）**不在本枚举里**：它需要拿现态水位线做比对，
// 属 T-…-066 的增量与陈旧判定，本 task 一格都不提前实现。
type Health string

const (
	// HealthHealthy 索引在位、结构完整、自洽（就本 task 的判定面而言）。
	HealthHealthy Health = "healthy"
	// HealthMissing 索引整体缺失（首次使用 / 被 rm -rf 掉）。
	HealthMissing Health = "missing"
	// HealthCorrupt 索引在位但不可用。
	HealthCorrupt Health = "corrupt"
)

// 损坏原因（封闭集合；机器可读，用于 e2e 逐字断言与 status 输出）。
const (
	ReasonNone                       = ""
	ReasonIndexDirMissing            = "index_dir_missing"
	ReasonDBFileMissing              = "db_file_missing"
	ReasonUnexpectedFile             = "unexpected_file"
	ReasonTruncatedFile              = "truncated_file"
	ReasonOpenFailed                 = "open_failed"
	ReasonIntegrityCheckFailed       = "integrity_check_failed"
	ReasonSchemaIncomplete           = "schema_incomplete"
	ReasonSchemaVersionMismatch      = "schema_version_mismatch"
	ReasonWatermarkSelfContradiction = "watermark_self_contradiction"
)

// Reasons 返回封闭的原因集合（不含 ReasonNone；供测试与下游断言）。
func Reasons() []string {
	return []string{
		ReasonIndexDirMissing, ReasonDBFileMissing, ReasonUnexpectedFile,
		ReasonTruncatedFile, ReasonOpenFailed, ReasonIntegrityCheckFailed,
		ReasonSchemaIncomplete, ReasonSchemaVersionMismatch,
		ReasonWatermarkSelfContradiction,
	}
}

// Diagnosis 是一次只读体检的结论。
type Diagnosis struct {
	Health Health
	// Code 是诊断码：healthy 时为空串，missing 时 W23，corrupt 时 W24。
	Code string
	// Reason 是机器可读子因（封闭集合，见 Reasons）。
	Reason string
	// Message 是人类可读说明（与 Reason 同源同事实，不引入新事实）。
	Message string
	// Meta 是读得到时的元数据快照；读不到即零值（判定不依赖它是否为零值）。
	Meta Meta
	// MetaReadable 说明 Meta 是否真的读出来了（避免把零值误读成「card_count = 0」）。
	MetaReadable bool
}

// Usable 报告本次结论下索引**能不能用于查询**。
// 只有 healthy 才可用；missing / corrupt 一律不可用 —— 不可用时读路径必须降级为全量扫描。
func (d Diagnosis) Usable() bool { return d.Health == HealthHealthy }

// sqliteMagic 是 SQLite 数据库文件头的 16 字节魔数（含结尾 NUL）。
var sqliteMagic = []byte("SQLite format 3\x00")

// headerLen 是 SQLite 文件头长度：不足 100 字节的文件一定是截断的。
const headerLen = 100

// Inspect 只读体检 dir（= vault/.index）。它**永不返回 error**：
// 索引不可用是一个诊断，不是一次失败（合同 §6.1）。
func Inspect(dir string) Diagnosis {
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return missing(ReasonIndexDirMissing, "索引目录 %s 不存在", dir)
		}
		return corrupt(ReasonOpenFailed, "索引目录 %s 不可访问：%v", dir, err)
	}
	// M6 运行时保留条目（`run.lock` / `txn/`，reserved.go）的**类型**先于一切判定核对：
	// 它们与索引共处一室，但类型一旦不对（锁变目录 / 日志变文件 / 任一项是 symlink），
	// 这个目录就已经不是一个可被安全使用的 `.index/` —— 此时「库在不在」已经不是重点，
	// 因此判定必须排在 DB 存在性之前。反过来，**类型合法**的保留条目不参与任何判定：
	// 只有它们在盘而库不在时，语义仍然是「索引缺失」（W23），不是「索引损坏」。
	if v, bad := InspectRuntimeReserved(dir); bad {
		return corrupt(ReasonUnexpectedFile,
			"索引目录 %s 下的 M6 运行时保留条目类型非法：%s；"+
				"本层只报不改（不重建、不删除任何条目），请人工处理后再跑 eg index",
			dir, v.String())
	}
	dbPath := filepath.Join(dir, DBFileName)
	st, err := os.Stat(dbPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return missing(ReasonDBFileMissing, "索引文件 %s 不存在", dbPath)
	case err != nil:
		return corrupt(ReasonOpenFailed, "索引文件 %s 不可访问：%v", dbPath, err)
	case st.IsDir():
		return corrupt(ReasonOpenFailed, "索引文件 %s 是目录", dbPath)
	}
	if extra := unexpectedFiles(dir); len(extra) > 0 {
		return corrupt(ReasonUnexpectedFile,
			"索引目录混入非法文件 %s（允许集合恰 %s，另加 M6 运行时保留条目 %s）；建议 eg index rebuild",
			strings.Join(extra, ", "), strings.Join(AllowedFiles(), ", "),
			strings.Join(reservedNames(), ", "))
	}
	if why, ok := truncated(dbPath, st.Size()); !ok {
		return corrupt(ReasonTruncatedFile, "索引文件截断或文件头非法：%s", why)
	}
	return inspectOpen(dbPath)
}

// inspectOpen 打开库后的四步判定：integrity_check → 表集合 → schema 版本 → 水位线自洽。
func inspectOpen(dbPath string) Diagnosis {
	db, err := openDB(dbPath, true)
	if err != nil {
		return corrupt(ReasonOpenFailed, "索引库打不开：%v", err)
	}
	defer func() { _ = db.Close() }()

	var check string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil {
		// PRAGMA 自己报错（典型是 SQLITE_CORRUPT：database disk image is malformed）
		// 同样是 integrity 判定失败，不另起一类。
		return corrupt(ReasonIntegrityCheckFailed, "PRAGMA integrity_check 执行失败：%v", err)
	}
	if check != "ok" {
		return corrupt(ReasonIntegrityCheckFailed, "PRAGMA integrity_check = %q（期望 ok）", check)
	}

	tables, err := tableSet(db)
	if err != nil {
		return corrupt(ReasonOpenFailed, "读取表集合失败：%v", err)
	}
	if missingTables := missingFrom(tables, TableNames()); len(missingTables) > 0 {
		return corrupt(ReasonSchemaIncomplete, "缺表 %s（表集合恰 %d 张）",
			strings.Join(missingTables, ", "), len(TableNames()))
	}

	meta, err := readMetaFrom(db)
	if err != nil {
		return corrupt(ReasonSchemaIncomplete, "index_meta 不可用：%v", err)
	}
	if meta.SchemaVersion != IndexSchemaVersion {
		d := corrupt(ReasonSchemaVersionMismatch,
			"index_meta.schema_version = %d，本二进制的 IndexSchemaVersion = %d；"+
				"不兼容一律整库重建（永不迁移），请跑 eg index rebuild",
			meta.SchemaVersion, IndexSchemaVersion)
		d.Meta, d.MetaReadable = meta, true
		return d
	}

	var cards int
	if err := db.QueryRow(`SELECT count(*) FROM ` + TableCards).Scan(&cards); err != nil {
		return corrupt(ReasonOpenFailed, "统计 %s 行数失败：%v", TableCards, err)
	}
	if cards != meta.CardCount {
		d := corrupt(ReasonWatermarkSelfContradiction,
			"水位线自指矛盾：index_meta.card_count = %d，但 %s 实际 %d 行",
			meta.CardCount, TableCards, cards)
		d.Meta, d.MetaReadable = meta, true
		return d
	}
	return Diagnosis{
		Health: HealthHealthy, Code: "", Reason: ReasonNone,
		Message: fmt.Sprintf("索引在位且自洽：schema_version=%d，card_count=%d，tokenizer_mode=%s",
			meta.SchemaVersion, meta.CardCount, meta.TokenizerMode),
		Meta: meta, MetaReadable: true,
	}
}

// unexpectedFiles 返回 `.index/` 下不在允许集合内的条目名（升序）。
//
// 允许集合 = AllowedFiles()（恰 3 个 DB 文件）**并上** RuntimeReservedEntries() 的名字
// （`run.lock` / `txn`，reserved.go）。后者按**名字**排除，与类型无关：类型违规已由
// Inspect 在更前面用 InspectRuntimeReserved 单独判掉并给出结构化事实，这里再报一次
// 只会把「保留条目类型不对」和「外部污染」两件不同的事混成一条消息。
func unexpectedFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	allowed := reservedNameSet()
	for _, name := range AllowedFiles() {
		allowed[name] = true
	}
	var extra []string
	for _, e := range entries {
		if !allowed[e.Name()] {
			extra = append(extra, e.Name())
		}
	}
	sort.Strings(extra)
	return extra
}

// truncated 判定文件头合法性与「按页数应有的最小长度」。
//
// 只读前 100 字节即可判三件事：魔数、页大小、库页数。
// 页数 × 页大小 > 实际文件长度 ⇒ 文件被截断（这类损坏用 integrity_check 也能发现，
// 但先判它能给出**更准确的原因**，且不必先把坏文件交给驱动）。
func truncated(dbPath string, size int64) (string, bool) {
	if size < headerLen {
		return fmt.Sprintf("文件长度 %d 字节 < SQLite 文件头 %d 字节", size, headerLen), false
	}
	f, err := os.Open(dbPath)
	if err != nil {
		return fmt.Sprintf("打不开：%v", err), false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, headerLen)
	if _, err := f.Read(head); err != nil {
		return fmt.Sprintf("读文件头失败：%v", err), false
	}
	if !hasPrefix(head, sqliteMagic) {
		return "文件头魔数不是 SQLite format 3", false
	}
	pageSize := int64(binary.BigEndian.Uint16(head[16:18]))
	if pageSize == 1 {
		pageSize = 65536 // SQLite 用 1 表示 64KiB
	}
	pages := int64(binary.BigEndian.Uint32(head[28:32]))
	if pageSize <= 0 || pages <= 0 {
		// 页数为 0 的库由 integrity_check 兜底判定，这里不当成截断。
		return "", true
	}
	if want := pages * pageSize; size < want {
		return fmt.Sprintf("文件头声明 %d 页 × %d 字节 = %d 字节，实际只有 %d 字节",
			pages, pageSize, want, size), false
	}
	return "", true
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}

// tableSet 返回库里的表 / 视图名集合，**过滤掉** FTS5 影子表（`cards_fts_*`）。
func tableSet(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type IN ('table','view') ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	got := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if strings.HasPrefix(name, FTSShadowPrefix) || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		got[name] = true
	}
	return got, rows.Err()
}

// missingFrom 返回 want 中不在 got 里的名字（保持 want 的次序）。
func missingFrom(got map[string]bool, want []string) []string {
	var out []string
	for _, name := range want {
		if !got[name] {
			out = append(out, name)
		}
	}
	return out
}

func missing(reason, format string, args ...interface{}) Diagnosis {
	return Diagnosis{Health: HealthMissing, Code: CodeIndexMissing, Reason: reason,
		Message: fmt.Sprintf(format, args...)}
}

func corrupt(reason, format string, args ...interface{}) Diagnosis {
	return Diagnosis{Health: HealthCorrupt, Code: CodeIndexCorrupt, Reason: reason,
		Message: fmt.Sprintf(format, args...)}
}
