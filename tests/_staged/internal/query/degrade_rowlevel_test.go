package query

// [S4] I-evergreen.system_assurance-158614-024 在**读路径**上的机器判据：
// 索引「行级撒谎」（库结构合法、水位线一致、card_count 不失配，但 cards / cards_fts 的
// 行内容对权威 Markdown 撒谎）必须被 probeIndex 检出、整体降级为全量扫描，且：
//
//	① 三条读命令一律退 0（有结果、不报错）；
//	② 恰一条 W24 + 恰一条 Q5（既有降级留痕，不新增码）；
//	③ data 与「健康索引 / 重建后」逐字等价（回权威扫描 ⇒ 撒谎不影响答案）。
//
// 判据来源：M5 索引架构合同 §5.2（不可用一律降级）、§6.1（不阻断读、不改退出码）、
// §6.3（W24 与 Q5 同现）、§1.1 P-2（Markdown 是唯一权威来源）。
//
// 与 internal/index 的行级单测（consistency_rowlevel_test.go）分工：那边钉「index.Check
// 拿到权威投影后判 W24」，这边钉「读路径真的把权威投影喂进去了、并据此降级 + 等价」——
// 缺了 currentCardsInput 这一步，读路径只做 DB 表间自洽，本文件立刻红。

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// bkTamperIndexSQL 直接对 `.index/eg.db` 执行一条 SQL（测试脚手架，非产品写路径）。
//
// 驱动名 "sqlite" 由 internal/index 的空导入（modernc.org/sqlite）在进程内注册，
// 因此这里无须再引驱动包。刻意用**合法 SQLite 写操作**制造行级撒谎：库仍然结构合法、
// integrity_check 通过、水位线与 card_count 不变 —— 正是本 issue 要害的那种「结构无病、
// 内容撒谎」态。
func bkTamperIndexSQL(t *testing.T, root, query string, args ...interface{}) {
	t.Helper()
	db, err := sql.Open("sqlite", index.DBPath(root))
	if err != nil {
		t.Fatalf("打开索引库失败：%v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("篡改索引失败（%s）：%v", query, err)
	}
}

// TestRowLevelLieDegradesReadPath —— 读路径对索引行级撒谎的完整闭环：检出 → 降级 → 等价 → 重建恢复。
//
// 覆盖两类代表性撒谎（其余类型由 index 层单测穷举）：
//   - 翻转 cards.deleted：把在用卡假删（可见性列对权威撒谎，窄化召回后会漏卡）；
//   - 删除 cards_fts 行：FTS 召回行凭空少一条（未来 MATCH 召回会漏命中）。
func TestRowLevelLieDegradesReadPath(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(t *testing.T, root string)
	}{
		{
			name: "翻转 cards.deleted（在用卡假删）",
			tamper: func(t *testing.T, root string) {
				bkTamperIndexSQL(t, root,
					`UPDATE `+index.TableCards+` SET deleted = 1 WHERE id = ?`, "k-20261201-ops")
			},
		},
		{
			name: "删除 cards_fts 行（card_count 不变）",
			tamper: func(t *testing.T, root string) {
				// cards_fts.id 是 UNINDEXED 存储列，可直接按 id 过滤删除；
				// 删 fts 行不动 cards 表 ⇒ card_count / 水位线均不变，只有 FTS 集合对权威撒谎。
				bkTamperIndexSQL(t, root,
					`DELETE FROM `+index.TableCardsFTS+` WHERE id = ?`, "k-20261201-rnn")
			},
		},
		{
			name: "等行数幽灵：content_hash 原地改成权威没有的值",
			tamper: func(t *testing.T, root string) {
				bkTamperIndexSQL(t, root,
					`UPDATE `+index.TableCards+` SET content_hash = ? WHERE id = ?`,
					"sha256:ghost", "k-20261201-attention")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := bkHealthyVault(t)

			// 基线：健康索引下三条读路径的结果（等价对照物）。
			idxSearch, idxCard, idxRel := bkReadAll(t, root)
			if idxSearch.backend.Kind != BackendIndex {
				t.Fatalf("前置：健康索引应走索引后端，实际 %s", idxSearch.backend.Kind)
			}

			tc.tamper(t, root)

			// ① 检出：探测把「结构合法但行级撒谎」的库判为不可用（W24 / index_unusable）。
			b := SelectBackend(root, cardNeed("k-20261201-attention", bkDeps))
			if b.UseIndex() {
				t.Fatalf("行级撒谎必须降级，不得继续走索引后端（%s）", b.Message)
			}
			if b.Code != index.CodeIndexCorrupt || b.Reason != ReasonIndexUnusable {
				t.Fatalf("行级撒谎应判 W24 / %s，实际 code=%q reason=%q（%s）",
					ReasonIndexUnusable, b.Code, b.Reason, b.Message)
			}
			if b.Freshness != index.FreshnessUnusable {
				t.Fatalf("行级撒谎的新鲜度应为 unusable，实际 %s", b.Freshness)
			}

			// ② 降级留痕：三条读路径退 0、有结果、恰一条 W24 + 恰一条 Q5、走扫描后端。
			bkAssertDegraded(t, root, index.CodeIndexCorrupt)

			// ③ 等价：降级读（回权威扫描）与健康索引读逐字相同 —— 撒谎不影响答案。
			degSearch, degCard, degRel := bkReadAll(t, root)
			if !reflect.DeepEqual(degSearch.Hits, idxSearch.Hits) {
				t.Fatalf("search hits 降级前后不等价：\n健康 %+v\n降级 %+v",
					idxSearch.Hits, degSearch.Hits)
			}
			if !reflect.DeepEqual(degCard.Card, idxCard.Card) {
				t.Fatalf("card show 降级前后不等价：\n健康 %+v\n降级 %+v",
					idxCard.Card, degCard.Card)
			}
			if !reflect.DeepEqual(degRel.Data, idxRel.Data) {
				t.Fatalf("rel data 降级前后不等价：\n健康 %+v\n降级 %+v",
					idxRel.Data, degRel.Data)
			}

			// ④ 重建恢复：重建索引后恢复 healthy/fresh，读路径重新走索引后端。
			bkDropIndex(t, root)
			bkBuildIndex(t, root)
			if rb := SelectBackend(root, cardNeed("k-20261201-attention", bkDeps)); !rb.UseIndex() ||
				rb.Freshness != index.FreshnessFresh {
				t.Fatalf("重建后应恢复 fresh 并走索引后端，实际 kind=%s freshness=%s（%s）",
					rb.Kind, rb.Freshness, rb.Message)
			}
		})
	}
}

// TestRowLevelHealthyIndexStillUsesIndex —— 反向护栏：**未被篡改**的健康索引在提供了
// 权威卡投影后仍判 fresh、仍走索引后端、仍不产 Q5。防止行级核对把健康库误伤成降级。
func TestRowLevelHealthyIndexStillUsesIndex(t *testing.T) {
	root := bkHealthyVault(t)
	res, err := bkRel(root, RelRequest{ID: model.RelationEndpoint("k-20261201-attention")})
	if err != nil {
		t.Fatalf("RelView：%v", err)
	}
	if res.backend.Kind != BackendIndex {
		t.Fatalf("健康索引应走索引后端，实际 %s（%s）", res.backend.Kind, res.backend.Message)
	}
	if bkCount(res.Diagnostics, CodeQ5) != 0 {
		t.Fatalf("健康索引下不得产 Q5，实际诊断 %v", bkCodes(res.Diagnostics))
	}
}
