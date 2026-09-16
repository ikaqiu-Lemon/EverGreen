package cli

// `eg index status` 对**行级撒谎**的命令级判据（I-evergreen.system_assurance-158614-024）。
//
// 与其它两层的分工：
//   - internal/index（consistency_rowlevel_test.go）钉 `index.Check` 拿到权威投影后判 W24；
//   - internal/query（rowlevel_degrade_test.go）钉读路径 search/card/rel 的 W24+Q5 降级与等价；
//   - **本文件**钉命令层 `eg index status [--strict]`：在一份「库结构合法、水位线一致、
//     card_count 不失配、权威 Markdown 一字未改」的库上，把某行 `cards.deleted` 直接篡改成
//     撒谎值，`eg index status` 必须——
//       ① 默认路径与 --strict 都判 health=corrupt / freshness=unusable / reason=row_level_divergence；
//       ② 恒退 0（体检是诊断不是失败，合同 §6.1）、use_index=false、blocks_read=false；
//       ③ **只读**：权威 Markdown 字节不变、`.index/` Digest 不变、零 commit、工作区干净；
//       ④ `eg index rebuild` 后立刻恢复 healthy/fresh（索引是可重建派生）。
//
// 关键：本用例覆盖 cli.indexCurrent 把权威 `snap.Cards` 灌进 Check 这条线 —— 若命令层
// 无谓丢弃 Cards（或快路径把旧 content_hash 塞进 Cards 投影），本用例立刻红。

import (
	"database/sql"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// idxTamperSQL 直连 `.index/eg.db` 执行一条写语句制造行级撒谎（测试脚手架，非产品写路径）。
//
// 收尾做一次 WAL 全量回灌：让篡改落在主库文件里，与真实世界的坏库形态一致，也确保随后
// `eg index status` 以独立只读连接打开时一定能看见它。驱动名 "sqlite" 由 internal/index
// 的空导入注册。
func idxTamperSQL(t *testing.T, dir, query string, args ...interface{}) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+index.DBPath(dir))
	if err != nil {
		t.Fatalf("打开索引库失败：%v", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("注入行级撒谎失败（%s）：%v", query, err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("WAL 回灌失败：%v", err)
	}
}

// TestIndexStatusDetectsOrphanDerivedRows —— T-005-D 补 C 批登记的完整性空白：
//
// 前提是一份**freshness 已经判 fresh 的候选态**库（水位线一致、card_count 不失配、
// 权威 Markdown 一字未改），但派生表里留着权威 Markdown **不存在**的行：
//
//	① `cards` 有孤儿行且 `files` 表无该 path（观点 / 卡文件被删后派生表没跟上）；
//	② `cards_fts` 有残留检索行（`cards` 里已无此 id）。
//
// 判据：`eg index status` 必须以**既有** W24 / row_level_divergence 判 corrupt，
// 不得 healthy；且仍恒退 0、只读、`eg index rebuild` 后立刻恢复 healthy/fresh。
//
// 与上面 TestIndexStatusDetectsRowLevelLie 的分工：那条钉「行在册但列内容撒谎」，
// 本条钉「行本身在权威里根本不存在」—— 后者过去被作用域收窄静默滤掉（C 批负控发现）。
func TestIndexStatusDetectsOrphanDerivedRows(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, dir string)
	}{
		{
			name: "cards 孤儿行（files 表无该 path）",
			tamper: func(t *testing.T, dir string) {
				idxTamperSQL(t, dir, `INSERT INTO `+index.TableCards+
					` (id, path, domain, title, status, deprecated, deleted, replaced_by,
					   content_hash, mtime_unix, kind, validation)
					  VALUES (?, ?, ?, ?, ?, 0, 0, '', ?, ?, ?, ?)`,
					"o-20260901-deleted", "domains/"+opnDomain+"/opinions/o-20260901-deleted.md",
					opnDomain, "文件已被删除的观点", "active",
					"sha256:orphan", 1_800_000_000,
					index.CardKindOpinion, index.ValidationPending)
				// card_count 同步跟上 cards 实际行数：否则先撞 watermark_self_contradiction，
				// 本用例要钉的那条（row_level_divergence）就永远走不到。
				idxTamperSQL(t, dir, `UPDATE `+index.TableIndexMeta+
					` SET value = (SELECT count(*) FROM `+index.TableCards+`) WHERE key = ?`,
					index.MetaCardCount)
			},
		},
		{
			name: "cards_fts 残留检索行（cards 表无此 id）",
			tamper: func(t *testing.T, dir string) {
				idxTamperSQL(t, dir, `INSERT INTO `+index.TableCardsFTS+
					` (id, title, body, bigram_text, kind, validation) VALUES (?, ?, ?, ?, ?, ?)`,
					"o-20260901-residual", "残留检索行", "残留正文", " zz zz ",
					index.CardKindOpinion, index.ValidationRejected)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := idxVaultWithOpinions(t)
			if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
				t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
			}
			if code, out, _ := runIndexCLI(t, dir, "status"); code != ExitOK ||
				idxHealth(t, out) != string(index.HealthHealthy) ||
				idxString(t, out, "freshness") != string(index.FreshnessFresh) {
				t.Fatalf("前置：刚建好的索引应 healthy/fresh，实得 health=%q freshness=%q",
					idxHealth(t, out), idxString(t, out, "freshness"))
			}
			tc.tamper(t, dir)

			authBefore := idxAuthoritySnapshot(t, dir)
			digestBefore, err := index.Digest(index.DirPath(dir))
			if err != nil {
				t.Fatalf("篡改后索引应仍可读出 Digest：%v", err)
			}
			beforeStatus := gitOut(t, dir, "status", "--porcelain")
			beforeCommits := gitOut(t, dir, "rev-list", "--count", "HEAD")

			for _, mode := range []struct {
				name  string
				extra []string
			}{{"默认路径", nil}, {"--strict", []string{"--strict"}}} {
				code, out, errOut := runIndexCLI(t, dir, "status", mode.extra...)
				if code != ExitOK {
					t.Fatalf("%s：status 必须恒退 0，实得 %d：%s", mode.name, code, errOut)
				}
				if h := idxHealth(t, out); h != string(index.HealthCorrupt) {
					t.Fatalf("%s：health = %q，期望 %q —— 权威里不存在的派生行是损坏，不是健康",
						mode.name, h, index.HealthCorrupt)
				}
				if f := idxString(t, out, "freshness"); f != string(index.FreshnessUnusable) {
					t.Fatalf("%s：freshness = %q，期望 %q", mode.name, f, index.FreshnessUnusable)
				}
				if r := idxString(t, out, "reason"); r != index.ReasonRowLevelDivergence {
					t.Fatalf("%s：reason = %q，期望既有 %q（不新增诊断码 / 第 11 个 reason）",
						mode.name, r, index.ReasonRowLevelDivergence)
				}
				if got := idxString(t, out, "use_index"); got != "false" {
					t.Fatalf("%s：use_index = %q，期望 false", mode.name, got)
				}
				if got := idxString(t, out, "blocks_read"); got != "false" {
					t.Fatalf("%s：blocks_read = %q，期望 false（索引任何状态都不阻断读）",
						mode.name, got)
				}
				if codes := idxWarnCodes(t, out); len(codes) != 1 || codes[0] != index.CodeIndexCorrupt {
					t.Fatalf("%s：warnings = %v，期望恰一条 %s", mode.name, codes, index.CodeIndexCorrupt)
				}
			}

			// 只读：两次 status 之后权威字节、索引 Digest、工作区、commit 数全不变。
			idxAssertAuthorityUnchanged(t, dir, authBefore)
			digestAfter, err := index.Digest(index.DirPath(dir))
			if err != nil {
				t.Fatalf("status 之后索引应仍可读：%v", err)
			}
			if digestBefore != digestAfter {
				t.Fatal("status 改动了索引：它必须只读")
			}
			if got := gitOut(t, dir, "status", "--porcelain"); got != beforeStatus {
				t.Fatalf("status 改动了工作区：前 %q → 后 %q", beforeStatus, got)
			}
			if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != beforeCommits {
				t.Fatalf("status 产生了 commit：前 %q → 后 %q", beforeCommits, got)
			}

			// 重建恢复：孤儿 / 残留行随整库重建一并消失。
			if code, _, errOut := runIndexCLI(t, dir, "rebuild"); code != ExitOK {
				t.Fatalf("rebuild 退出码 = %d：%s", code, errOut)
			}
			code, out, _ := runIndexCLI(t, dir, "status")
			if code != ExitOK || idxHealth(t, out) != string(index.HealthHealthy) ||
				idxString(t, out, "freshness") != string(index.FreshnessFresh) {
				t.Fatalf("rebuild 后应恢复 healthy/fresh，实得 health=%q freshness=%q",
					idxHealth(t, out), idxString(t, out, "freshness"))
			}
			if codes := idxWarnCodes(t, out); len(codes) != 0 {
				t.Fatalf("rebuild 后不得再有 warning 码，实得 %v", codes)
			}
		})
	}
}

// TestIndexStatusDetectsRowLevelLie —— 命令级行级撒谎检出 + 只读 + 重建恢复的完整闭环。
func TestIndexStatusDetectsRowLevelLie(t *testing.T) {
	dir := idxVault(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("前置 build 退出码 = %d：%s", code, errOut)
	}
	// 篡改前自证 healthy/fresh：确保后面的 corrupt 不是脏基线诬告的。
	if code, out, _ := runIndexCLI(t, dir, "status"); code != ExitOK ||
		idxHealth(t, out) != string(index.HealthHealthy) ||
		idxString(t, out, "freshness") != string(index.FreshnessFresh) {
		t.Fatalf("前置：刚建好的索引应 healthy/fresh，实得 health=%q freshness=%q",
			idxHealth(t, out), idxString(t, out, "freshness"))
	}

	// 行级撒谎：把一张在用卡的 deleted 从 0 翻成 1（不动权威 Markdown、files 表、card_count）。
	idxTamperSQL(t, dir, `UPDATE `+index.TableCards+` SET deleted = 1 WHERE id = ?`, applyCardID)

	// 篡改后立刻取只读基线（此刻才是「坏库」的稳定态）。
	authBefore := idxAuthoritySnapshot(t, dir)
	digestBefore, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("篡改后索引应仍可读出 Digest：%v", err)
	}
	beforeStatus := gitOut(t, dir, "status", "--porcelain")
	beforeCommits := gitOut(t, dir, "rev-list", "--count", "HEAD")

	// ① + ②：默认路径与 --strict 都必须检出行级撒谎，且恒退 0 / use_index=false / blocks_read=false。
	for _, tc := range []struct {
		name  string
		extra []string
	}{
		{"默认路径", nil},
		{"--strict", []string{"--strict"}},
	} {
		code, out, errOut := runIndexCLI(t, dir, "status", tc.extra...)
		if code != ExitOK {
			t.Fatalf("%s：status 必须退 0（体检是诊断不是失败），实得 %d：%s", tc.name, code, errOut)
		}
		if h := idxHealth(t, out); h != string(index.HealthCorrupt) {
			t.Fatalf("%s：health = %q，期望 %q（行级撒谎属损坏）", tc.name, h, index.HealthCorrupt)
		}
		if f := idxString(t, out, "freshness"); f != string(index.FreshnessUnusable) {
			t.Fatalf("%s：freshness = %q，期望 %q", tc.name, f, index.FreshnessUnusable)
		}
		if r := idxString(t, out, "reason"); r != index.ReasonRowLevelDivergence {
			t.Fatalf("%s：reason = %q，期望 %q", tc.name, r, index.ReasonRowLevelDivergence)
		}
		if got := idxString(t, out, "use_index"); got != "false" {
			t.Fatalf("%s：use_index = %q，期望 false（行级撒谎不可信，读侧必须走全量扫描）", tc.name, got)
		}
		if got := idxString(t, out, "blocks_read"); got != "false" {
			t.Fatalf("%s：blocks_read = %q，期望 false（索引任何状态都不阻断读）", tc.name, got)
		}
		if codes := idxWarnCodes(t, out); len(codes) != 1 || codes[0] != index.CodeIndexCorrupt {
			t.Fatalf("%s：warnings = %v，期望恰一条 %s", tc.name, codes, index.CodeIndexCorrupt)
		}
	}

	// ③ 只读：跑完两次 status 后，权威 Markdown 字节、索引 Digest、Git 状态、commit 数全不变。
	idxAssertAuthorityUnchanged(t, dir, authBefore)
	digestAfter, err := index.Digest(index.DirPath(dir))
	if err != nil {
		t.Fatalf("status 之后索引应仍可读：%v", err)
	}
	if digestBefore != digestAfter {
		t.Fatal("status 改动了索引：它必须只读（连修复行级撒谎都得走 eg index rebuild）")
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != beforeStatus {
		t.Fatalf("status 改动了工作区：前 %q → 后 %q（status 必须只读）", beforeStatus, got)
	}
	if got := gitOut(t, dir, "rev-list", "--count", "HEAD"); got != beforeCommits {
		t.Fatalf("status 产生了 commit：前 %q → 后 %q", beforeCommits, got)
	}

	// ④ 重建恢复：rebuild 从权威 Markdown 全量重建 ⇒ 立刻回到 healthy/fresh。
	if code, _, errOut := runIndexCLI(t, dir, "rebuild"); code != ExitOK {
		t.Fatalf("rebuild 退出码 = %d：%s", code, errOut)
	}
	code, out, _ := runIndexCLI(t, dir, "status")
	if code != ExitOK || idxHealth(t, out) != string(index.HealthHealthy) ||
		idxString(t, out, "freshness") != string(index.FreshnessFresh) {
		t.Fatalf("rebuild 后应恢复 healthy/fresh，实得 health=%q freshness=%q",
			idxHealth(t, out), idxString(t, out, "freshness"))
	}
	if codes := idxWarnCodes(t, out); len(codes) != 0 {
		t.Fatalf("rebuild 后不得再有 warning 码，实得 %v", codes)
	}
}
