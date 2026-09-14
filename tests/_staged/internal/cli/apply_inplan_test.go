package cli

// T-…-018 端到端验收中发现并就地修复的两处实现问题的回归用例。
//
// 两条都直接来自 ChangePlan 合同 §2.1「七种处理关系 → op 组合」表：
//
//	① `non_core_supplement` 行要求同一份 plan 里 `append_card` + `add_material_rel`
//	   都落在同一张卡上。修复前第二个 op 仍拿 plan.base 里的旧 content_hash 去比对，
//	   而该文件刚被前一个 op 合法改写，于是自撞 B3、被判 file_changed 跳过——
//	   照合同写的 plan 永远跑不完。修复后同一 plan 内的前序写入用写后真值比对，
//	   B3 对**外部**改动的拦截强度不变（TestApplyPartialSkipsFileChanged 仍然通过）。
//
//	② `independent_new` 行要求 `create_card` + `add_material_rel`。四要素逐字相同的
//	   材料关系在卡上早就是幂等去重的（store.ApplyMaterialRel），但报告仍登记两条，
//	   与磁盘不一致。修复后报告同样只登记一条——「报告如实」是 M1 的完成判据之一。

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyTwoOpsOnSameCardShareInPlanHash：同一 plan 内两个 op 写同一张卡不自撞 B3。
func TestApplyTwoOpsOnSameCardShareInPlanHash(t *testing.T) {
	dir := applyVault(t)
	_, cardRel := applyNoteAndCard(t, dir)
	cardHash := hashOf(t, dir, cardRel)

	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"合同 §2.1 non_core_supplement 行的 op 组合",
"convergence":[{"card":"` + applyCardID + `","relation":"non_core_supplement",
 "core_knowledge":"same","conditions":"different","reuse_purpose":"same","note":"补了一条成立条件"}],
"base":{"` + applyCardID + `":"` + cardHash + `"},
"ops":[{"op":"append_card","card":"` + applyCardID + `","sections":{"条件与边界":"- 追加的一条边界\n"}},
{"op":"add_material_rel","card":"` + applyCardID + `","source":"` + applySourceID + `",
 "note":"` + applyNoteID + `","rel":"support","reason":"第二篇材料为该卡补了成立条件"}]}`
	code, env, _ := runApplyPlan(t, dir, plan)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：同一 plan 内的前序写入不算「外部改动」", code)
	}
	rep := applyReport(t, env)
	if len(rep.Skipped) != 0 {
		t.Fatalf("不应有任何跳过：%+v", rep.Skipped)
	}
	card := string(mustRead(t, filepath.Join(dir, filepath.FromSlash(cardRel))))
	if !strings.Contains(card, "追加的一条边界") {
		t.Fatal("append_card 未落盘")
	}
	if !strings.Contains(card, "第二篇材料为该卡补了成立条件") {
		t.Fatal("add_material_rel 未落盘（被前一个 op 的写入误判为文件已变化）")
	}
	if len(rep.Relations.Material) != 1 {
		t.Fatalf("报告材料关系 = %d 条，期望 1：%+v", len(rep.Relations.Material), rep.Relations.Material)
	}
}

// TestApplyIdenticalMaterialRelReportedOnce：四要素逐字相同的材料关系，卡上一条、报告也只一条。
func TestApplyIdenticalMaterialRelReportedOnce(t *testing.T) {
	dir := applyVault(t)
	applyNoteAndCard(t, dir) // 先落地笔记（材料关系的 note 必须全库可解析）
	const newCard = "k-20260901-bitter"
	plan := `{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"合同 §2.1 independent_new 行的 op 组合",
"base":{},
"ops":[{"op":"create_card","card_id":"` + newCard + `","title":"新建卡",
 "sources":[{"source":"` + applySourceID + `","note":"` + applyNoteID + `","rel":"support","reason":"原文四个案例是直接依据"}],
 "sections":{"知识内容":"结论正文。\n"}},
{"op":"add_material_rel","card":"` + newCard + `","source":"` + applySourceID + `",
 "note":"` + applyNoteID + `","rel":"support","reason":"原文四个案例是直接依据"}]}`
	code, env, errOut := runApplyPlan(t, dir, plan)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s", code, errOut)
	}
	rep := applyReport(t, env)
	if len(rep.Relations.Material) != 1 {
		t.Fatalf("报告材料关系 = %d 条，期望 1（与卡上条目数一致）：%+v",
			len(rep.Relations.Material), rep.Relations.Material)
	}
	card := string(mustRead(t, filepath.Join(dir, filepath.FromSlash("domains/ai-infra/knowledge/"+newCard+".md"))))
	if n := strings.Count(card, "  - source:"); n != 1 {
		t.Fatalf("卡 sources[] 条目数 = %d，期望 1（幂等去重）", n)
	}
}
