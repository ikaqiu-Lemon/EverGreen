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
