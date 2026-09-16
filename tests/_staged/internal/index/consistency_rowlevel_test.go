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

// —— T-005-D 补的完整性空白：孤儿 / 残留派生行（权威 Markdown 根本不存在）——
//
// C 批（观点增量收敛）在负控里登记过一个洞：人为在 `cards` 里留下一行「权威 Markdown
// 不存在、`files` 表也没有该 path」的孤儿（观点文件删掉后残留的形态），此时水位线
// (head, files_hash) 仍与现态一致、`card_count` 与实际行数也一致，于是 `index.Check`
// 判 healthy/fresh —— 读路径一旦按索引出候选集，就会拿一个磁盘上根本不存在的卡去回权威。
//
// 洞的成因不在「有没有比对」，而在**作用域**：checkRowLevel 的作用域 stablePaths 由
// **权威投影**构造，孤儿行的 path 天生不在其中，于是它在收窄那一步被静默滤掉。修法是在
// 作用域收窄**之前**先做一次集合层核对（不新增诊断码、不新增第 11 个 reason）：
//
//	① `cards` 里 path 不在 `files` 表的行     ⇒ 权威里没有这个文件 ⇒ 孤儿；
//	② `cards_fts` 里 id 不在 `cards` 表的行   ⇒ 检索行残留（删卡后 FTS 没跟着删）。
//
// 两者都统一收在既有 W24 / row_level_divergence 之下。
//
// 为什么孤儿判据用 `files` 表而不是权威投影：fresh 候选态的前提就是 files_hash 判等，
// 即 `files` 表与磁盘权威文件集合逐项相等。因此「path 不在 files」= 「磁盘上没有这个
// 文件」，是一个**不依赖作用域**的硬事实；而「path 在 files、内容却变了」属陈旧域
// （见下面 TestCheckQuickPathRewriteStaysStaleNotCorrupt），一格都不许被误判成损坏。
func TestCheckDetectsOrphanDerivedRows(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, dir string)
	}{
		{
			// 「cards 有孤儿且 files 无该路径」：card_count 同步跟上，否则先撞
			// watermark_self_contradiction（那是**另一类**损坏，会把本判据的红染成假红）。
			name: "cards 残留权威不存在的孤儿行（files 表无该 path）",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir, `INSERT INTO `+index.TableCards+
					` (id, path, domain, title, status, deprecated, deleted, replaced_by,
					   content_hash, mtime_unix, kind, validation)
					  VALUES (?, ?, ?, ?, ?, 0, 0, '', ?, ?, ?, ?)`,
					"o-orphan", "domains/ai/opinions/o-orphan.md", "ai", "已被删除的观点",
					"active", "sha256:orphan", 1_600_000_900,
					index.CardKindOpinion, index.ValidationPending)
				execFixture(t, dir, `UPDATE `+index.TableIndexMeta+
					` SET value = (SELECT count(*) FROM `+index.TableCards+`) WHERE key = ?`,
					index.MetaCardCount)
			},
		},
		{
			// FTS 相关残留：检索行还在，`cards` 里已无此 id。card_count 不受影响
			// （cards 行数没变），水位线也不动 —— 结构自检全过，只有集合核对能抓到。
			name: "cards_fts 残留检索行（cards 表无此 id）",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir, `INSERT INTO `+index.TableCardsFTS+
					` (id, title, body, bigram_text, kind, validation) VALUES (?, ?, ?, ?, ?, ?)`,
					"o-residual", "残留检索行", "正文残留", " zz zz ",
					index.CardKindOpinion, index.ValidationValidated)
			},
		},
		{
			// 孤儿 + 残留同时存在（真实世界里「删文件后派生表没同步」的完整形态）。
			name: "cards 孤儿与 cards_fts 残留并存",
			tamper: func(t *testing.T, dir string) {
				execFixture(t, dir, `INSERT INTO `+index.TableCards+
					` (id, path, domain, title, status, deprecated, deleted, replaced_by,
					   content_hash, mtime_unix, kind, validation)
					  VALUES (?, ?, ?, ?, ?, 0, 0, '', ?, ?, ?, ?)`,
					"k-orphan", "domains/ai/knowledge/k-orphan.md", "ai", "已被删除的卡",
					"active", "sha256:orphan2", 1_600_000_950,
					index.CardKindKnowledge, "")
				execFixture(t, dir, `INSERT INTO `+index.TableCardsFTS+
					` (id, title, body, bigram_text, kind, validation) VALUES (?, ?, ?, ?, ?, ?)`,
					"k-orphan", "已被删除的卡", "正文残留", " zz zz ",
					index.CardKindKnowledge, "")
				execFixture(t, dir, `UPDATE `+index.TableIndexMeta+
					` SET value = (SELECT count(*) FROM `+index.TableCards+`) WHERE key = ?`,
					index.MetaCardCount)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := buildFixture(t, sampleSnapshot())
			if c := index.Check(dir, currentWithCards(sampleSnapshot())); !c.Fresh() || c.Code != "" {
				t.Fatalf("篡改前应 fresh + 零码，实得 %s / %q", c.Freshness, c.Code)
			}
			tc.tamper(t, dir)

			// 自证「结构自检全过」：孤儿 / 残留不是 Inspect 那四类结构损坏，
			// 只有行级核对能抓到 —— 否则本用例证明的就不是它要证的那件事。
			if d := index.Inspect(dir); d.Health != index.HealthHealthy {
				t.Fatalf("前置：孤儿 / 残留必须结构自检全过（Inspect=healthy），"+
					"实得 %s / %s（%s）", d.Health, d.Reason, d.Message)
			}

			c := index.Check(dir, currentWithCards(sampleSnapshot()))
			if !c.Unusable() {
				t.Fatalf("权威不存在的孤儿 / 残留行必须判 unusable，实得 %s（%s）",
					c.Freshness, c.Message)
			}
			if c.Code != index.CodeIndexCorrupt {
				t.Fatalf("码应为既有 %s（不新增诊断码），实得 %q", index.CodeIndexCorrupt, c.Code)
			}
			if c.Reason != index.ReasonRowLevelDivergence {
				t.Fatalf("子因应为既有 %s（不新增第 11 个 reason），实得 %q（%s）",
					index.ReasonRowLevelDivergence, c.Reason, c.Message)
			}
			if c.Diagnosis.Health != index.HealthCorrupt || c.Diagnosis.Usable() {
				t.Fatalf("体检结论应降为 corrupt/不可用，实得 %s / usable=%v",
					c.Diagnosis.Health, c.Diagnosis.Usable())
			}
		})
	}
}

// TestCheckQuickPathRewriteStaysStaleNotCorrupt 钉住上面那条修复的**反向边界**：
// 真正的「内容被改写」（默认快路径按 (size, mtime) 沿用了 files 表旧 content_hash，
// 而权威投影带的是新内容的真 hash）必须**照旧**落在行级核对作用域之外 —— 那是陈旧
// （归 `--strict` / `eg index sync`），不是损坏。孤儿检测按 `files` 表**有没有这个 path**
// 判定，与「path 在册但内容变了」正交，因此这一格一格未松。
func TestCheckQuickPathRewriteStaysStaleNotCorrupt(t *testing.T) {
	snap := sampleSnapshot()
	dir := buildFixture(t, snap)

	// 权威投影：k-alpha 的正文 / 标题 / content_hash 全变了（等长原地改写 + 还原 mtime，
	// 于是快路径判「未变」，Files 仍沿用旧 hash ⇒ 水位线判等 ⇒ fresh 候选态）。
	rewritten := sampleSnapshot()
	for i := range rewritten.Cards {
		if rewritten.Cards[i].ID == "k-alpha" {
			rewritten.Cards[i].Title = "改写后的标题"
			rewritten.Cards[i].Body = "改写后的正文"
			rewritten.Cards[i].ContentHash = "sha256:aaaa-rewritten"
		}
	}
	cur := index.Current{Head: snap.Head, Files: snap.Files, Cards: rewritten.Cards}
	if c := index.Check(dir, cur); !c.Fresh() || c.Code != "" {
		t.Fatalf("快路径等长改写属陈旧域，不得误报成损坏：实得 %s / %q（%s）",
			c.Freshness, c.Code, c.Message)
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
