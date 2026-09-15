package cli

// [S6] apply_opinion_transaction_test.go —— T-004-B：一个同时含 `create_opinion` 与
// `add_relation` 的真实 plan，走完整 `eg apply` 写链后的事务边界判据。
//
// 判据来源：Teamwork T-…-004 Acceptance 第 1 条逐字——
// 「一个包含 `create_opinion` + `add_relation` 的 plan 执行后：`o-*` 文件落盘、
//   relations 双向可见、恰一次 commit（verb 取自 plan.verb）」。
//
// # 为什么这支用例必须走 CLI 而不是 plan 层
//
// 「恰一次 commit」与「verb 取自 plan.verb」都不是 plan 层能观察到的事实：
// 前者是 S6 原子提交 + S7 Git 的**联合**结论（一笔事务、一条 commit、Markdown 与 Git
// 的先后由 commit marker 分界），后者住在 `internal/cli/recover_hook.go` 的 S7
// （`repo.Commit` 取 `pres.Verb`）。只有从命令入口投一份真 plan，才能同时钉住
// 「两类实体（观点 / 知识卡）落在同一笔事务里」这条原子域边界。
//
// 本文件不扩张 reconcile / check 的判定面（那是 T-004-C），只考事务与执行边界。

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

const applyOpinionID = "o-20261017-scaling"

// opinionAndRelationPlan 造本用例的 plan：**一份** plan 里既新建观点、又加一条论证关系。
//
// verb 刻意取 `relate` 而不是默认的 `process`：`process` 恰好是「verb 缺失 / 未知」时的
// 退化值（§4.5），拿它做判据无法区分「取自 plan.verb」与「根本没读 plan.verb」。
//
// `base` 必须带上 from 端卡的现态哈希：`add_relation` 改的是既有卡，B3 写前复核
// （base 未覆盖 ⇒ 跳过该文件、退 3）与实体种类无关，这里如实提供凭据，
// 才能让本用例考的是事务边界而不是 B3。
func opinionAndRelationPlan(from, target, fromHash string) string {
	return `{"plan_version":2,"verb":"relate","domain":"ai-infra",
"reason":"把观点与两张卡的论证关系一次落盘","requirement_ids":["EG-AGT-03"],
"base":{"` + from + `":"` + fromHash + `"},
"ops":[
{"op":"create_opinion","opinion_id":"` + applyOpinionID + `",
 "title":"缩放注意力不适合超长序列",
 "sources":[{"source":"` + applySourceID + `","note":"` + applyNoteID + `",
 "rel":"support","reason":"原文第 5 节的复杂度分析"}],
 "sections":{"观点":"超长序列下该机制不经济。\n","论据与推理":"复杂度随长度平方增长。\n"}},
{"op":"add_relation","from":"` + from + `","type":"supports","target":"` + target + `",
 "reason":"前者是后者的机制基础"}]}`
}

// TestApplyOpinionAndRelationCommitOnceWithPlanVerb —— Acceptance 第 1 条的四格一次性钉住：
// ① `o-*` 落盘（且 validation 是新建默认值 pending）；② relations 正反双向可见；
// ③ 整个 plan 恰一次 commit、恰一个事务且 commit 标记在盘；④ commit 的 verb 取自 plan.verb。
func TestApplyOpinionAndRelationCommitOnceWithPlanVerb(t *testing.T) {
	dir := applyVault(t)
	applyNoteAndCard(t, dir) // 落一篇笔记 + 第一张卡（关系的 from 端）
	if code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, "")); code != ExitOK {
		t.Fatalf("落第二张卡退出码 = %d：%s", code, errOut)
	}

	cardRel := store.CardRel("ai-infra", applyCardID)
	before := gitLogCount(t, dir)
	txnsBefore := txnIDsOn(t, dir)

	code, env, errOut := runApplyPlan(t, dir,
		opinionAndRelationPlan(applyCardID, applyCard2ID, hashOf(t, dir, cardRel)))
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}

	// —— ③ 恰一次 commit：整个 plan（观点 + 关系）是**一笔**事务、**一条** commit。——
	if got := gitLogCount(t, dir); got != before+1 {
		t.Fatalf("commit 数 = %d，期望 %d：一份 plan 恰一次 commit（不因两类实体分成两次）",
			got, before+1)
	}
	txnID := onlyNewTxn(t, dir, txnsBefore, "含观点与关系的 plan")
	if !markerExists(dir, txnID, txn.CommitMarker) {
		t.Fatalf("事务 %s 的 commit 标记必须在盘（标记落盘前 Markdown 不算生效）", txnID)
	}
	if markerExists(dir, txnID, txn.AbortMarker) {
		t.Fatalf("成功路径不得留下 abort 标记（事务 %s）", txnID)
	}

	// —— ④ verb 取自 plan.verb：主题形状 `<verb>(<domain>): …`。——
	subject := strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
	if !strings.HasPrefix(subject, string(model.VerbRelate)+"(ai-infra): ") {
		t.Fatalf("commit 主题 = %q，期望以 %q 开头（verb 取自 plan.verb，不是默认的 process）",
			subject, string(model.VerbRelate)+"(ai-infra): ")
	}

	// —— ① `o-*` 落盘：文件在观点目录、frontmatter 的 validation 为 pending。——
	opinionRel := store.OpinionRel("ai-infra", applyOpinionID)
	raw := mustRead(t, filepath.Join(dir, filepath.FromSlash(opinionRel)))
	var fm struct {
		ID         string `yaml:"id"`
		Validation string `yaml:"validation"`
	}
	if err := store.FrontmatterInto(raw, &fm); err != nil {
		t.Fatalf("落盘观点的 frontmatter 必须可解析：%v\n%s", err, raw)
	}
	if fm.ID != applyOpinionID || fm.Validation != string(model.ValidationPending) {
		t.Fatalf("观点落盘结果 id=%q validation=%q，期望 %q / %q",
			fm.ID, fm.Validation, applyOpinionID, model.ValidationPending)
	}

	// 观点与知识卡必须在**同一条** commit 里：这才是「同一个原子域」的可观察形态。
	committed := strings.Fields(gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD"))
	inCommit := map[string]bool{}
	for _, f := range committed {
		inCommit[f] = true
	}
	for _, rel := range []string{opinionRel, cardRel} {
		if !inCommit[rel] {
			t.Fatalf("%s 应出现在本次 commit 的文件集合里，实得 %v", rel, committed)
		}
	}
	rep := applyReport(t, env)
	if strings.Join(sortedCopy(committed), ",") != strings.Join(sortedCopy(rep.Links), ",") {
		t.Fatalf("干净工作区下 commit 文件集合 %v 应等于报告 links[] %v", committed, rep.Links)
	}

	// —— ② relations 双向可见：from 端读正向、target 端读反向（`eg rel` 只读路径）。——
	_, outEnv, _ := runRelJSON(t, dir, applyCardID)
	outData, _ := outEnv["data"].(map[string]interface{})
	if got := relEdgeSigs(t, outData, "relations_out", "target"); !hasSig(got, "supports("+applyCard2ID+")") {
		t.Fatalf("%s 的正向关系应含 supports(%s)，实得 %v", applyCardID, applyCard2ID, got)
	}
	_, inEnv, _ := runRelJSON(t, dir, applyCard2ID)
	inData, _ := inEnv["data"].(map[string]interface{})
	if got := relEdgeSigs(t, inData, "relations_in", "from"); !hasSig(got, "supports("+applyCardID+")") {
		t.Fatalf("%s 的反向关系应含 supports(%s)，实得 %v", applyCard2ID, applyCardID, got)
	}
}

// hasSig 判断关系签名清单里是否含某条边（判据是「含」而非「等于」：夹具卡自带的
// 材料关系不属本用例考察面，把它们钉进判据只会让无关变更变红）。
func hasSig(sigs []string, want string) bool {
	for _, s := range sigs {
		if s == want {
			return true
		}
	}
	return false
}
