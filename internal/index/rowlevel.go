package index

// 行级权威一致性核对（I-evergreen.system_assurance-158614-024）。
//
// # 这一层补的洞
//
// Inspect（corrupt.go）只体检索引**自身**是否自洽：库能不能打开、integrity_check、
// 表集合、schema 版本、`cards` 行数 == `index_meta.card_count`、水位线 `(head, files_hash)`。
// 这些全过之后，一个**结构完全合法**的 SQLite 仍可能对权威撒谎 —— 外部进程把某行
// `cards_fts` 删掉、把 `cards.deleted` 从 0 改成 1、把某行 `content_hash` 原地改成一个
// 权威里不存在的值：行数守恒、水位线不动、`card_count` 不失配，于是 Check 一路判 fresh。
// 未来读路径一旦窄化召回（不再逐个回权威 Markdown 复算），这类「行级撒谎」就会让
// `search` / `card show` / `rel` 拿着索引里的假事实出结果。
//
// 本文件把这道洞补上：拿到调用方**已解析好**的权威 Markdown 卡投影（Current.Cards），
// 逐行核对 `cards` / `cards_fts` 与权威是否逐列相等。任一不一致统一收在既有的
// `W24 index_corrupt` 之下（封闭子因 row_level_divergence，不新增诊断码）。
//
// # 三条边界（与合同 §13 一致）
//
//   - **不反向依赖**：本文件只 import 标准库与本包，绝不 import query / store / git。
//     权威投影从**外部**（cli.indexCurrent / query.probeIndex）灌进来，索引层不自己
//     读 Markdown、不自己算 content_hash（那两处口径分别属 store.B3 与 mdfile 解析）。
//   - **不另造第二套解析 / hash**：`cards_fts.bigram_text` 的期望值一律走 build.go 里
//     写入侧那**同一个** BigramText(title+"\n"+body)；`content_hash` 直接取投影里
//     调用方算好的值，本文件一个字节都不重算。
//   - **只读**：以 `mode=ro` 打开库，零 DDL、零写入 —— 与 Inspect / status 的只读合同一致。
//
// # 只在 fresh 候选态上核对（次序刻意）
//
// 行级核对只在水位线判定为「与现态一致」之后才做（见 consistency.go 的 Check）：
// 水位线一旦不一致，索引本就该判 stale 并走降级，此时权威 Markdown 已经变了，拿它的
// 卡投影去对一份**旧**索引必然「不一致」—— 那是陈旧，不是损坏，报 W24 会误导。
//
// # 作用域 = 「文件自 build 以来字节确未变」的卡（关键裁决）
//
// 水位线「与现态一致」有两条来路：`--strict` 全量重算 content_hash 后判等（此时文件字节
// 确未变），或默认**快路径**按 `(size, mtime)` 命中 QuickUnchanged 而沿用 files 表旧
// content_hash（合同 §5.1）。快路径对**等长原地改写 + 还原 mtime** 这类改动天生不敏感
// ——文件内容其实变了，但快路径仍判「未变」。若不加区分地拿**新解析**的权威投影去对
// 一份**按旧内容**建的索引，这类改动会被误报成 W24 行级撒谎，而它本质是陈旧（W22，
// 归 `--strict` / `eg index sync`）。因此行级核对**只作用于**「files 表 content_hash 与
// 权威投影 content_hash 相等」的那批卡：唯有此时才能断言「文件字节没变，行内容却对权威
// 撒谎」＝真损坏。content_hash 不等的卡属陈旧域，排除在行级核对之外（不误报、不漏报：
// 陈旧由水位线在 `--strict` 下自然抓到）。I-…-024 的全部篡改场景都不动权威 Markdown，
// 故其 content_hash 恒与 files 基线相等，一律落在作用域内、逐个被抓。

import (
	"fmt"
	"sort"
)

// cardsCheckedColumns 是 `cards` 表逐行核对的列（合同 I-…-024 的最小集合）。
//
// 刻意**不含** `mtime_unix`：它是文件系统事实、不进权威语义（改一次 mtime 不改卡内容），
// 且 content_hash 已经承载「内容变没变」。核对面只盯**权威语义列**：撒谎一旦发生在
// 这几列（假删 / 假失效 / 幽灵 hash / 替换 id / 改标题指针），逐列比对必然抓到。
//
// `kind` / `validation`（Schema v2）在册：判别列上的撒谎（把观点伪装成知识卡、把
// pending 改成 validated）在权威 Markdown 面前同样是谎 —— 而且是会直接改变读路径
// 返回集与 `eg check` 结论的谎，比改标题更该被抓。
//
// 每加一列都必须同时在 cardColumn 里加一个 case：那个函数是 `switch` + `default: ""`
// 形态，漏加时期望值与实得值双双取空串、逐列比对恒相等 —— 核对面看着扩了，实际一格未扩。
// 判据见 TestCheckDetectsKindAndValidationLies（它要求诊断信息指名到列）。
var cardsCheckedColumns = []string{
	"id", "path", "domain", "title", "status",
	"deprecated", "deleted", "replaced_by", "content_hash",
	"kind", "validation",
}

// ftsCheckedColumns 是 `cards_fts` 表逐行核对的列（合同 I-…-024 的最小集合）。
var ftsCheckedColumns = []string{"id", "title", "body", "bigram_text", "kind", "validation"}

// ftsRow 是 `cards_fts` 一行里参与核对的六列（body / bigram_text 是 FTS 召回口径，
// 权威值分别是正文全文与 build.go 的 BigramText 补路串；kind / validation 是判别列，
// 权威值直接取权威投影 —— 它们在 FTS 侧是 UNINDEXED 存储列，只服务收窄与取回）。
type ftsRow struct {
	id, title, body, bigram, kind, validation string
}

// checkRowLevel 逐行核对 `cards` / `cards_fts` 与权威卡投影是否逐列相等。
//
// authoritative 是调用方**已解析好**的权威 Markdown 卡投影（未去重、可能乱序）：本函数
// 先把作用域收窄到「files 表 content_hash 与权威 content_hash 相等」的稳定卡（见文件头
// 「作用域」一节），再用与 build.go **同一套** sortedCards + dedupCards 归一，得到「若此刻
// 全量重建，库里应有的那批卡」，与磁盘上的 `cards` / `cards_fts` 逐行比对。
//
// 返回 (detail, diverged)：diverged=true 时 detail 是**第一处**不一致的逐字定位
// （哪张表、哪张卡、哪一列、期望什么 / 实得什么），供 status 与 e2e 逐条复算；
// diverged=false 表示两张派生表与权威投影（在作用域内）逐行相等。
func checkRowLevel(dir string, authoritative []Card) (string, bool) {
	// build 基线 = files 表的 content_hash（不受 cards / cards_fts 行级篡改影响：
	// 篡改 cards.content_hash 不动 files 表，故基线仍是可信的「文件字节指纹」）。
	baseline, err := ReadFiles(dir)
	if err != nil {
		return fmt.Sprintf("files 表读取失败：%v", err), true
	}
	baseHash := make(map[string]string, len(baseline))
	for _, f := range baseline {
		baseHash[f.Path] = f.ContentHash
	}
	// 作用域：只留「文件字节确未变」的卡（权威 content_hash == files 基线）。
	stablePaths := make(map[string]bool, len(authoritative))
	stable := make([]Card, 0, len(authoritative))
	for _, c := range authoritative {
		if h, ok := baseHash[c.Path]; ok && h == c.ContentHash {
			stablePaths[c.Path] = true
			stable = append(stable, c)
		}
	}
	want, _ := dedupCards(sortedCards(stable))

	// 磁盘 `cards` 同样收窄到作用域内（按 path 判定 —— path 是文件事实，不受 id /
	// content_hash 行级篡改影响；用它做「哪些行归本次核对」的稳定锚点）。
	gotCards, err := ReadCards(dir)
	if err != nil {
		return fmt.Sprintf("cards 表读取失败：%v", err), true
	}
	gotStable := make([]Card, 0, len(gotCards))
	for _, c := range gotCards {
		if stablePaths[c.Path] {
			gotStable = append(gotStable, c)
		}
	}
	if detail, ok := diffCardSets(want, gotStable); ok {
		return detail, true
	}

	// FTS 作用域 = 稳定卡的 id 并集（权威侧 + 索引侧）：并集才能同时抓到「权威在册但
	// 检索行被删 / 篡改」（权威侧 id）与「id 被原地替换成幽灵」（索引侧 id）两类撒谎。
	checkIDs := make(map[string]bool, len(want)+len(gotStable))
	for _, c := range want {
		checkIDs[c.ID] = true
	}
	for _, c := range gotStable {
		checkIDs[c.ID] = true
	}
	if detail, ok := diffCardsFTS(dir, want, checkIDs); ok {
		return detail, true
	}
	return "", false
}

// diffCardSets 按 id 建集合逐列比对（纯函数，便于单测；不碰 DB）。
// 入参 want / got 均已由 checkRowLevel 收窄到「文件字节未变」的作用域内。
func diffCardSets(want, got []Card) (string, bool) {
	wantByID := make(map[string]Card, len(want))
	for _, c := range want {
		wantByID[c.ID] = c
	}
	gotByID := make(map[string]Card, len(got))
	for _, c := range got {
		if _, dup := gotByID[c.ID]; dup {
			return fmt.Sprintf("cards 表 id=%s 出现多行（权威每 id 恰一行）", c.ID), true
		}
		gotByID[c.ID] = c
	}
	// 幽灵 / 替换：库里有、权威没有。
	for _, id := range sortedCardIDs(gotByID) {
		if _, ok := wantByID[id]; !ok {
			return fmt.Sprintf("cards 表存在权威里没有的卡 id=%s（幽灵 / 替换行）", id), true
		}
	}
	// 缺失：权威有、库里没有。
	for _, id := range sortedCardIDs(wantByID) {
		if _, ok := gotByID[id]; !ok {
			return fmt.Sprintf("cards 表缺权威在册的卡 id=%s", id), true
		}
	}
	// 逐列比对（次序即 sortedCardIDs，定位稳定可复算）。
	for _, id := range sortedCardIDs(wantByID) {
		w, g := wantByID[id], gotByID[id]
		for _, col := range cardsCheckedColumns {
			wv, wok := cardColumn(w, col)
			gv, gok := cardColumn(g, col)
			if !wok || !gok {
				return fmt.Sprintf("cards 表核对列 %s 没有取值实现"+
					"（核对面配置错误：cardsCheckedColumns 与 cardColumn 不同步）", col), true
			}
			if wv != gv {
				return fmt.Sprintf("cards 表 id=%s 的 %s 与权威不符：期望 %q，实得 %q",
					id, col, wv, gv), true
			}
		}
	}
	return "", false
}

// diffCardsFTS 比对磁盘 `cards_fts` 与权威投影（逐列，ftsCheckedColumns）。
//
// body / bigram_text 的期望值直接由权威 Title/Body 派生：bigram_text 走 build.go 写入侧
// 那**同一个** BigramText（不另造第二套算法）。
//
// checkIDs 是本次行级核对的 FTS 作用域（稳定卡的 id 并集）：作用域外的 FTS 行——例如
// 内容已改（陈旧域）卡的检索行——一律跳过，不参与幽灵 / 缺失 / 逐列比对，避免把陈旧误报成
// W24。want 已由 checkRowLevel 收窄到作用域内，其 id 必属 checkIDs。
func diffCardsFTS(dir string, want []Card, checkIDs map[string]bool) (string, bool) {
	got, err := readFTSRows(dir)
	if err != nil {
		return fmt.Sprintf("cards_fts 表读取失败：%v", err), true
	}
	gotByID := make(map[string]ftsRow, len(got))
	for _, r := range got {
		if !checkIDs[r.id] {
			continue // 作用域外：不归本次行级核对
		}
		if _, dup := gotByID[r.id]; dup {
			return fmt.Sprintf("cards_fts 表 id=%s 出现多行（权威每 id 恰一行）", r.id), true
		}
		gotByID[r.id] = r
	}
	wantByID := make(map[string]ftsRow, len(want))
	for _, c := range want {
		wantByID[c.ID] = ftsRow{
			id: c.ID, title: c.Title, body: c.Body,
			bigram:     BigramText(c.Title + "\n" + c.Body),
			kind:       c.Kind,
			validation: c.Validation,
		}
	}
	for _, id := range sortedFTSIDs(gotByID) {
		if _, ok := wantByID[id]; !ok {
			return fmt.Sprintf("cards_fts 表存在权威里没有的行 id=%s（幽灵 / 替换）", id), true
		}
	}
	for _, id := range sortedFTSIDs(wantByID) {
		w, ok := gotByID[id]
		if !ok {
			return fmt.Sprintf("cards_fts 表缺权威在册卡 id=%s 的检索行（删行撒谎）", id), true
		}
		exp := wantByID[id]
		for _, col := range ftsCheckedColumns {
			ev, eok := ftsColumn(exp, col)
			gv, gok := ftsColumn(w, col)
			if !eok || !gok {
				return fmt.Sprintf("cards_fts 表核对列 %s 没有取值实现"+
					"（核对面配置错误：ftsCheckedColumns 与 ftsColumn 不同步）", col), true
			}
			if ev != gv {
				return fmt.Sprintf("cards_fts 表 id=%s 的 %s 与权威不符：期望 %q，实得 %q",
					id, col, ev, gv), true
			}
		}
	}
	return "", false
}

// readFTSRows 只读读回 `cards_fts` 全部行的六个核对列（按 rowid 升序）。
func readFTSRows(dir string) ([]ftsRow, error) {
	db, err := openDB(dbPathIn(dir), true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	rows, err := db.Query(`SELECT id, title, body, bigram_text, kind, validation FROM ` +
		TableCardsFTS + ` ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ftsRow{}
	for rows.Next() {
		var r ftsRow
		if err := rows.Scan(&r.id, &r.title, &r.body, &r.bigram,
			&r.kind, &r.validation); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// cardColumn 取一张卡在某个核对列上的**规范字符串值**（布尔按 0/1，与 SQLite 存储口径一致）。
//
// 第二个返回值是「这一列有没有取值实现」。它存在的唯一目的是封掉一个假绿：本函数是
// `switch col` 形态，若有人往 cardsCheckedColumns 里加了列却忘了在这里加 case，
// 那么期望侧与实得侧会**双双**落到同一个兜底值上，逐列比对恒相等 —— 核对面看着扩大了，
// 实际一格未扩。返回哨兵字符串同样救不了（两侧哨兵也相等），所以必须把「无取值实现」
// 这件事本身当成一次核对失败上报，由调用方判成不一致。
func cardColumn(c Card, col string) (string, bool) {
	switch col {
	case "id":
		return c.ID, true
	case "path":
		return c.Path, true
	case "domain":
		return c.Domain, true
	case "title":
		return c.Title, true
	case "status":
		return c.Status, true
	case "deprecated":
		return fmt.Sprintf("%d", boolInt(c.Deprecated)), true
	case "deleted":
		return fmt.Sprintf("%d", boolInt(c.Deleted)), true
	case "replaced_by":
		return c.ReplacedBy, true
	case "content_hash":
		return c.ContentHash, true
	case "kind":
		return c.Kind, true
	case "validation":
		return c.Validation, true
	default:
		return "", false
	}
}

// ftsColumn 取一行 FTS 在某个核对列上的值（第二个返回值同 cardColumn：有无取值实现）。
func ftsColumn(r ftsRow, col string) (string, bool) {
	switch col {
	case "id":
		return r.id, true
	case "title":
		return r.title, true
	case "body":
		return r.body, true
	case "bigram_text":
		return r.bigram, true
	case "kind":
		return r.kind, true
	case "validation":
		return r.validation, true
	default:
		return "", false
	}
}

// sortedCardIDs / sortedFTSIDs 给出确定序的 id 列表（定位与复算稳定）。
func sortedCardIDs(m map[string]Card) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sortedFTSIDs(m map[string]ftsRow) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// rowLevelCorrupt 把一处行级不一致折成 corrupt 诊断（W24 / row_level_divergence）。
//
// Meta 沿用 Inspect 读到的元数据：库结构合法，摘要仍可如实透出（card_count / schema 等），
// 只是**行内容**对权威撒谎。Message 前缀点名这是「行级」损坏，与结构类损坏区分。
func rowLevelCorrupt(base Diagnosis, detail string) Diagnosis {
	msg := "索引对权威撒谎（行级不一致，库结构合法但派生表内容与权威 Markdown 不符）：" +
		detail + "；处置 = eg index rebuild（索引是可重建派生，重建后即恢复 healthy/fresh）"
	d := Diagnosis{
		Health:  HealthCorrupt,
		Code:    CodeIndexCorrupt,
		Reason:  ReasonRowLevelDivergence,
		Message: msg,
	}
	if base.MetaReadable {
		d.Meta, d.MetaReadable = base.Meta, true
	}
	return d
}
