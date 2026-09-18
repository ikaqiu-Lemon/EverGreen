package model_test

// 观点验证状态机（Schema v2 契约 §6.1）的机器判据。
//
// 本文件锁定四件事，每条都对应契约硬约束而非行为复述：
//  1. 3x3 网格恰**五合法 / 四非法**：两轴都是 ValidValidations() 的三值全集，
//     逐格调用 ValidationTransition，合法格数与非法格数都被钉死；
//  2. 每条合法边的 action 由 (from,to) 唯一决定（validate / reject / reopen），
//     调用方不得自报 action，也不得因目标态相同就误判 action；
//  3. 非法四格 = 三个自环（pending/validated/rejected 各对自身）+ 逆向跳变
//     rejected->validated（必须先 reopen 回 pending 再 validate）；
//  4. 封闭动作集恰三值，且第四值端点（起点或目标）不得参与迁移构造。

import (
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

type valEdge struct{ from, to model.Validation }

// legalEdges 是契约 §6.1 图逐边列出的**五**条合法迁移及其 action。
// 它是本文件唯一的「期望真源」：3x3 网格里凡不在此表的格子都必须是非法边。
var legalEdges = map[valEdge]model.ValidationAction{
	{model.ValidationPending, model.ValidationValidated}:  model.ValidationValidate,
	{model.ValidationPending, model.ValidationRejected}:   model.ValidationReject,
	{model.ValidationValidated, model.ValidationRejected}: model.ValidationReject,
	{model.ValidationValidated, model.ValidationPending}:  model.ValidationReopen,
	{model.ValidationRejected, model.ValidationPending}:   model.ValidationReopen,
}

// TestValidationMachineIsExactly3x3FiveLegalFourIllegal 遍历全 3x3 网格：
// 合法边必须成功且 action 与期望逐一相符；非法边必须报错且不返回 action。
func TestValidationMachineIsExactly3x3FiveLegalFourIllegal(t *testing.T) {
	all := model.ValidValidations()
	if len(all) != 3 {
		t.Fatalf("validation 必须恰三态（网格才是 3x3），实得 %d：%v", len(all), all)
	}
	legal, illegal := 0, 0
	for _, from := range all {
		for _, to := range all {
			act, err := model.ValidationTransition(from, to)
			if want, ok := legalEdges[valEdge{from, to}]; ok {
				if err != nil {
					t.Fatalf("合法边 %s -> %s 必须成功，得 err=%v", from, to, err)
				}
				if act != want {
					t.Fatalf("合法边 %s -> %s 的 action 应为 %q，实得 %q", from, to, want, act)
				}
				legal++
			} else {
				if err == nil {
					t.Fatalf("非法边 %s -> %s 必须报错，却成功返回 action=%q", from, to, act)
				}
				if act != "" {
					t.Fatalf("非法边 %s -> %s 报错时不得返回 action，实得 %q", from, to, act)
				}
				illegal++
			}
		}
	}
	if legal != 5 || illegal != 4 {
		t.Fatalf("3x3 网格必须恰 5 合法 / 4 非法，实得 %d / %d", legal, illegal)
	}
}

// TestValidationIllegalEdgesAreThreeSelfLoopsPlusReverseJump 逐格钉死非法四格的**身份**：
// 三个自环 + rejected->validated 逆向跳变；不允许「多一格少一格」的非法集合漂移。
func TestValidationIllegalEdgesAreThreeSelfLoopsPlusReverseJump(t *testing.T) {
	illegal := []valEdge{
		{model.ValidationPending, model.ValidationPending},     // 自环
		{model.ValidationValidated, model.ValidationValidated}, // 自环
		{model.ValidationRejected, model.ValidationRejected},   // 自环
		{model.ValidationRejected, model.ValidationValidated},  // 逆向跳变：须先 reopen 回 pending
	}
	for _, e := range illegal {
		if _, err := model.ValidationTransition(e.from, e.to); err == nil {
			t.Fatalf("非法边 %s -> %s 必须报错", e.from, e.to)
		}
	}
	// 反证：非法集合之外的四格（不含合法五边）不存在——上一测试已覆盖计数，
	// 这里再钉「合法五边确实各自合法」，与非法四格互补成完整 3x3。
	for e, want := range legalEdges {
		act, err := model.ValidationTransition(e.from, e.to)
		if err != nil || act != want {
			t.Fatalf("合法边 %s -> %s 应返回 %q 且无错，实得 act=%q err=%v", e.from, e.to, want, act, err)
		}
	}
}

// TestValidationActionSetClosedThreeValues：封闭动作集恰 validate/reject/reopen，顺序稳定。
func TestValidationActionSetClosedThreeValues(t *testing.T) {
	got := model.ValidationActions()
	want := []model.ValidationAction{
		model.ValidationValidate, model.ValidationReject, model.ValidationReopen,
	}
	if len(got) != 3 {
		t.Fatalf("action 集合必须恰三值，实得 %d：%v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("action 集合顺序应为 validate / reject / reopen，实得 %v", got)
		}
	}
	// 每条合法边解析出的 action 必须落在封闭集合内（不产生第四种动作）。
	closed := map[model.ValidationAction]bool{}
	for _, a := range got {
		closed[a] = true
	}
	for e := range legalEdges {
		act, err := model.ValidationTransition(e.from, e.to)
		if err != nil {
			t.Fatalf("合法边 %s -> %s 解析失败：%v", e.from, e.to, err)
		}
		if !closed[act] {
			t.Fatalf("合法边 %s -> %s 的 action %q 不在封闭集合内", e.from, e.to, act)
		}
	}
}

// TestValidationTransitionRejectsInvalidEndpoints：第四值端点不得参与迁移构造，
// 起点或目标非法都要报错（绝不接受「用第四值构造一条迁移」）。
func TestValidationTransitionRejectsInvalidEndpoints(t *testing.T) {
	if _, err := model.ValidationTransition(model.Validation("confirmed"), model.ValidationValidated); err == nil {
		t.Fatal("非法起点（第四值）必须报错")
	}
	if _, err := model.ValidationTransition(model.ValidationPending, model.Validation("maybe")); err == nil {
		t.Fatal("非法目标（第四值）必须报错")
	}
	if _, err := model.ValidationTransition(model.Validation(""), model.Validation("")); err == nil {
		t.Fatal("空端点必须报错")
	}
}
