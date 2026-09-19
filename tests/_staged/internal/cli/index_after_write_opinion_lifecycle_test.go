package cli

// [S4-consumer] index_after_write_opinion_lifecycle_test.go —— T-007 批次 A3：
// `eg opinion validate|reject` 走完事务后，**写后索引同步（S8）必须把观点行一并收敛**。
//
// 文件名以 `index` 起头是**位置锁**要求：命令层里只有 index* 文件可以 import 索引包
// （cmd/eg/arch_test.go 的 TestStage4IndexPackageBoundary ⑤）。本文件直连 .index/eg.db 做
// SQL 对账，复用 index_opinion_projection_test.go 的只读脚手架（opnOpenDB / opnValidationByID /
// opnFTSRowsByID / idxVaultWithOpinions…），不另造第二套口径。
//
// 被钉的缺陷：index_after_write.go 的 indexDeltaFor 目前只认知识卡路径
// （isCardRel 只接受 domains/<d>/knowledge/<id>.md），观点写后不进 delta。于是一次
// `eg opinion validate` 提交后，HEAD 前移、索引「看起来 fresh」，但派生行里那条观点的
// validation 仍停在旧态 —— cards / cards_fts 的 validation 投影与权威新态漂移。
//
// 判据（健康索引写后仍健康且**逐格**与权威一致）：
//
//	① 写命令退 0、事务提交成功（前置：lifecycle 已接线）；
//	② eg index status 仍 healthy / fresh / use_index=true（不 W24、不陈旧）；
//	③ cards.validation[被验证观点] == 权威新态（validated），cards_fts 同行相等；
//	④ 未被触及的另外两条观点行 validation 逐字不动（写后同步不越界改无关行）。
//
// 另有一条守卫：缺索引（.index/eg.db 不存在）时写后同步**静默跳过、不自动建库**。

import (
	"os"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— ① 健康索引：观点 validate 写后，索引仍健康且 validation 投影随权威收敛 ——

func TestOpinionLifecycleSyncsHealthyOpinionIndex(t *testing.T) {
	dir := idxVaultWithOpinions(t)
	if code, _, errOut := runIndexCLI(t, dir, "build"); code != ExitOK {
		t.Fatalf("build 退出码非 0：%s", errOut)
	}
	// 前置自证：build 后三条观点 validation 各就各位，被测行确是 pending。
	pre := opnValidationByID(t, dir)
	if pre[opnPendingID] != string(index.ValidationPending) {
		t.Fatalf("前置：%s 索引 validation = %q，期望 pending", opnPendingID, pre[opnPendingID])
	}

	// 在健康索引之上，把 pending 观点 validate 到 validated（合法边，经真实 CLI 事务）。
	code, _, errOut := runOpinionCLI(t, dir, SubOpinionValidate, opnPendingID,
		"--reason", "写后索引同步判据：把 pending 收敛为 validated", "--user-request")
	if code != ExitOK {
		t.Fatalf("opinion validate 退出码 = %d，期望 0（lifecycle 已接线）：%s", code, errOut)
	}

	// ① 权威新态确为 validated（磁盘 frontmatter）。
	rel := store.OpinionRel(opnDomain, opnPendingID)
	if got := opinionValidationOf(t, dir, rel); got != model.ValidationValidated {
		t.Fatalf("权威 validation = %q，期望 validated（前置失败无从谈索引）", got)
	}

	// ② 索引仍 healthy / fresh / use_index=true（写后同步收敛，不 W24、不陈旧）。
	scode, sout, serr := runIndexCLI(t, dir, "status")
	if scode != ExitOK {
		t.Fatalf("status 退出码 = %d：%s", scode, serr)
	}
	if h := idxHealth(t, sout); h != string(index.HealthHealthy) {
		t.Fatalf("写后索引 health = %q，期望 healthy", h)
	}
	if f := idxString(t, sout, "freshness"); f != string(index.FreshnessFresh) {
		t.Fatalf("写后索引 freshness = %q，期望 fresh", f)
	}
	if u := idxString(t, sout, "use_index"); u != "true" {
		t.Fatalf("写后索引 use_index = %q，期望 true", u)
	}

	// ③ cards.validation[被验证观点] == validated，cards_fts 同行相等。
	post := opnValidationByID(t, dir)
	if post[opnPendingID] != string(index.ValidationValidated) {
		t.Fatalf("写后 cards.validation[%s] = %q，期望 validated（indexDeltaFor 漏收观点行）",
			opnPendingID, post[opnPendingID])
	}
	fts := opnFTSRowsByID(t, dir)
	if fts[opnPendingID].validation != string(index.ValidationValidated) {
		t.Fatalf("写后 cards_fts.validation[%s] = %q，期望 validated",
			opnPendingID, fts[opnPendingID].validation)
	}

	// ④ 未被触及的另外两条观点行逐字不动（写后同步只收敛受影响行，不越界）。
	if post[opnValidatedID] != pre[opnValidatedID] {
		t.Fatalf("无关观点 %s 的 validation 被写后同步改动：%q → %q",
			opnValidatedID, pre[opnValidatedID], post[opnValidatedID])
	}
	if post[opnRejectedID] != pre[opnRejectedID] {
		t.Fatalf("无关观点 %s 的 validation 被写后同步改动：%q → %q",
			opnRejectedID, pre[opnRejectedID], post[opnRejectedID])
	}
}

// —— ② 缺索引：观点 validate 写后**不自动建库**（静默跳过）——

func TestOpinionLifecycleDoesNotBuildMissingIndex(t *testing.T) {
	dir, _, opinionRel := opinionVault(t)
	// 前置：opinionVault 未建索引，eg.db 不应存在。
	if _, err := os.Stat(index.DBPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("前置：语料不应带索引库，os.Stat(eg.db) err = %v", err)
	}

	code, _, errOut := runOpinionCLI(t, dir, SubOpinionValidate, applyOpinionID,
		"--reason", "缺索引写后不建库", "--user-request")
	if code != ExitOK {
		t.Fatalf("opinion validate 退出码 = %d，期望 0：%s", code, errOut)
	}
	// 权威已写（validated）。
	if got := opinionValidationOf(t, dir, opinionRel); got != model.ValidationValidated {
		t.Fatalf("权威 validation = %q，期望 validated", got)
	}
	// 缺索引静默跳过：绝不自动建库。
	if _, err := os.Stat(index.DBPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("写后不得自动建索引库，os.Stat(eg.db) err = %v", err)
	}
}
