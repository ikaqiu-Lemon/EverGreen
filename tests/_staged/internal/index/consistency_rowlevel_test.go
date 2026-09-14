package index_test

// I-evergreen.system_assurance-158614-024 的**先红**证据：在 watermark（head + files_hash）
// 与 `card_count` 都保持一致的**合法 SQLite** 上，对派生表做行级撒谎——
//
//	① 删除 cards_fts 行（cards 仍在册、card_count 不变）；
//	② 篡改 cards_fts 文本（title / body / bigram_text）；
//	③ 双向翻转 cards.deleted（假删 / 假活）；
//	④ 双向翻转 cards.deprecated（假失效 / 假在用）；
//	⑤ 等行数「幽灵/替换」：把某行 id 或 content_hash 原地改成权威里不存在的值（行数守恒）。
//
// 期望：`Check` 拿到**权威卡投影**（Current.Cards）后，逐行核对 cards / cards_fts 与权威
// Markdown 投影，任一不一致统一判 corrupt + W24（既有码，封闭子因 row_level_divergence）。
//
// 修复前（本文件即先红证据）：`index.Check` 只比水位线与 card_count，上述五类撒谎全部
// 报 healthy + fresh、零 W2x —— 每个子测试的 `wantCorrupt` 断言都会红。

import (
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// currentWithCards 把快照当作「权威 Markdown 现态」喂给 Check，并**带上权威卡投影**
// （Current.Cards）——这正是 cli.indexCurrent / query.probeIndex 在真实读路径上做的事。
func currentWithCards(snap index.Snapshot) index.Current {
	return index.Current{Head: snap.Head, Files: snap.Files, Cards: snap.Cards}
}

// rowidOf 取 cards 表里某张卡的 rowid（cards 与 cards_fts 共用同一 rowid，build.go 写入口径）。
func rowidOf(t *testing.T, dir, id string) int64 {
	t.Helper()
	db := openFixture(t, dir, true)
	var rowid int64
	if err := db.QueryRow(`SELECT rowid FROM `+index.TableCards+` WHERE id = ?`, id).
		Scan(&rowid); err != nil {
		t.Fatalf("取 %s 的 rowid 失败：%v", id, err)
	}
	return rowid
}

// TestCheckDetectsRowLevelLies 是 I-…-024 的先红总用例：五类行级撒谎逐个先红后绿。
func TestCheckDetectsRowLevelLies(t *testing.T) {
	// 基线自证：健康库 + 权威卡投影必须 fresh 且零码（否则后面的红全是假红）。
	base := buildFixture(t, sampleSnapshot())
	if c := index.Check(base, currentWithCards(sampleSnapshot())); !c.Fresh() || c.Code != "" {
		t.Fatalf("健康库 + 权威投影必须 fresh + 零码，实得 %s / %q（%s）",
			c.Freshness, c.Code, c.Message)
	}

	cases := []struct {
		name   string
		tamper func(t *testing.T, dir string)
	}{
		{
			name: "删除 cards_fts 行（card_count 不变）",
			tamper: func(t *testing.T, dir string) {
				rid := rowidOf(t, dir, "k-alpha")
				execFixture(t, dir, `DELETE FROM `+index.TableCardsFTS+` WHERE rowid = ?`, rid)
			},
		},
		{
			name: "篡改 cards_fts.body 文本",
			tamper: func(t *testing.T, dir string) {
				rid := rowidOf(t, dir, "k-alpha")
				execFixture(t, dir,
					`UPDATE `+index.TableCardsFTS+` SET body = ? WHERE rowid = ?`,
					"这是被外部进程改写过的正文", rid)
			},
		},
		{
			name: "篡改 cards_fts.title 文本",
			tamper: func(t *testing.T, dir string) {
				rid := rowidOf(t, dir, "k-alpha")
				execFixture(t, dir,
					`UPDATE `+index.TableCardsFTS+` SET title = ? WHERE rowid = ?`,
					"标题被篡改", rid)
			},
		},
		{
			name: "篡改 cards_fts.bigram_text 文本",
			tamper: func(t *testing.T, dir string) {
				rid := rowidOf(t, dir, "k-beta")
				execFixture(t, dir,
					`UPDATE `+index.TableCardsFTS+` SET bigram_text = ? WHERE rowid = ?`,
					" zz zz ", rid)
			},
		},
		{
			name: "翻转 cards.deleted：把未删卡假删",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET deleted = 1 WHERE id = ?`, "k-alpha")
			},
		},
		{
			name: "翻转 cards.deleted：把已删卡假活",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET deleted = 0 WHERE id = ?`, "k-gamma")
			},
		},
		{
			name: "翻转 cards.deprecated：把在用卡假失效",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET deprecated = 1 WHERE id = ?`, "k-beta")
			},
		},
		{
			name: "翻转 cards.deprecated：把失效卡假在用",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET deprecated = 0 WHERE id = ?`, "k-alpha")
			},
		},
		{
			name: "等行数幽灵：content_hash 原地改成权威里没有的值",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET content_hash = ? WHERE id = ?`,
					"sha256:ghost", "k-alpha")
			},
		},
		{
			name: "等行数替换：把卡 id 原地改成权威里没有的幽灵 id",
			tamper: func(t *testing.T, dir string) {
				// cards 与 cards_fts 同步改，制造「行数守恒但 id 集合对权威撒谎」的替换态。
				rid := rowidOf(t, dir, "k-alpha")
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET id = ? WHERE rowid = ?`, "k-ghost", rid)
				execFixture(t, dir,
					`UPDATE `+index.TableCardsFTS+` SET id = ? WHERE rowid = ?`, "k-ghost", rid)
			},
		},
		{
			name: "篡改 cards.replaced_by：指针原地改成权威里没有的值",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET replaced_by = ? WHERE id = ?`,
					"k-ghost", "k-alpha")
			},
		},
		{
			name: "篡改 cards.title：与权威 frontmatter 不一致",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir,
					`UPDATE `+index.TableCards+` SET title = ? WHERE id = ?`,
					"标题撒谎", "k-alpha")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildFixture(t, sampleSnapshot())
			// 篡改前自证 fresh：确保红不是被脏基线诬告的。
			if c := index.Check(dir, currentWithCards(sampleSnapshot())); !c.Fresh() {
				t.Fatalf("篡改前应 fresh，实得 %s", c.Freshness)
			}
			tc.tamper(t, dir)

			c := index.Check(dir, currentWithCards(sampleSnapshot()))
			// 水位线仍一致（篡改没碰权威 Markdown，也没碰 files/card_count）——
			// 这正是本 issue 的要害：老实现据此报 fresh。
			if !c.Unusable() {
				t.Fatalf("行级撒谎必须判 unusable，实得 %s（%s）", c.Freshness, c.Message)
			}
			if c.Code != index.CodeIndexCorrupt {
				t.Fatalf("行级撒谎的码应为 %s，实得 %q", index.CodeIndexCorrupt, c.Code)
			}
			if c.Reason != index.ReasonRowLevelDivergence {
				t.Fatalf("行级撒谎的子因应为 %s，实得 %q（%s）",
					index.ReasonRowLevelDivergence, c.Reason, c.Message)
			}
			if c.Diagnosis.Health != index.HealthCorrupt || c.Diagnosis.Usable() {
				t.Fatalf("行级撒谎必须把体检结论降为 corrupt/不可用，实得 %s / usable=%v",
					c.Diagnosis.Health, c.Diagnosis.Usable())
			}
		})
	}
}

// TestCheckRowLevelSkippedWithoutAuthoritativeCards 钉住语义边界：
// 调用方**不提供**权威投影（Current.Cards == nil）时，Check 不做行级核对（走老口径），
// 以免把「没提供权威」误当成「索引撒谎」。这不是放行撒谎——真实读路径恒提供 Cards。
func TestCheckRowLevelSkippedWithoutAuthoritativeCards(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	execFixture(t, dir, `UPDATE `+index.TableCards+` SET deleted = 1 WHERE id = ?`, "k-alpha")

	// 不带 Cards：只比水位线 ⇒ 仍 fresh（历史口径，向后兼容）。
	if c := index.Check(dir, currentOf(sampleSnapshot())); !c.Fresh() {
		t.Fatalf("未提供权威投影时应走老口径判 fresh，实得 %s（%s）", c.Freshness, c.Message)
	}
	// 带 Cards：立即检出撒谎。
	if c := index.Check(dir, currentWithCards(sampleSnapshot())); !c.Unusable() {
		t.Fatalf("提供权威投影后应检出撒谎判 unusable，实得 %s", c.Freshness)
	}
}
