package cli

// `eg proposal approve` **执行前重算**顺序的机器判据
// （提案合同 §3「S-② 的硬要求」逐字：「先批准后重算」是本触发的**唯一**实现顺序，
// 不得省略重算直接执行；§5.3 第 8 / 9a / 9b 步；T-evergreen.s1_main_flow-158614-035）。
//
// 用例名逐字采用 task verify 的约定形态：TestApprove_RecomputesImpactBeforeExecute。
//
// 判法：给 approveProposal 注入**带调用序号的** hooks，断言
//
//	重算（recomputeImpact）的调用序号 < 任一写盘动作（markApproved / supersede）的调用序号，
//
// 并额外断言「重算恰被调一次」「写盘至多一次」。顺序颠倒或省略重算，用例立刻红。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/proposal"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

const (
	apProposalID = proposal.ID("p-20261017-001")
	apCard       = "k-20260901-attention"
	apDomain     = "ai-infra"
)

// apToday 是改判时新提案的日期段：由 CLI 传入（internal/proposal 不读系统时钟）。
func apToday(t *testing.T) model.Date {
	t.Helper()
	d, err := model.ParseDate("2026-10-17")
	if err != nil {
		t.Fatalf("解析日期：%v", err)
	}
	return d
}

// apVault 落一份最小语料：一张卡 + 一份 pending 提案（提案的 impact 就写成该卡的真实影响面）。
func apVault(t *testing.T) (*store.Store, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("建目录：%v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("写夹具 %s：%v", rel, err)
		}
	}
	write("domains/"+apDomain+"/knowledge/"+apCard+".md", fmt.Sprintf(`---
id: %s
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
---

## 知识内容

正文占位。

## 解释与依据

依据占位。

## 条件与边界

边界占位。

## 用户补充

## 理解自检

- 自检问题占位？
`, apCard))
	raw, err := proposal.RenderTemplate(proposal.Template{
		ID: apProposalID, Title: "逻辑删除一张过时卡", CreatedAt: "2026-10-17",
		Targets: []string{apCard},
		Impact:  proposal.Impact{ExitsDefaultView: []string{apCard}},
	})
	if err != nil {
		t.Fatalf("RenderTemplate：%v", err)
	}
	write(proposal.Rel(apProposalID), string(raw))
	return store.New(root), root
}

// apTrace 记录四步链路里每个注入点的调用序号。
type apTrace struct {
	seq        int
	recomputed int
	approved   int
	superseded int
	recalls    int
	writes     int
}

// hooks 返回带序号记录的 hooks；real 为真时复用生产实现（落盘真实发生）。
func (tr *apTrace) hooks(real bool) approveHooks {
	def := defaultApproveHooks()
	return approveHooks{
		recomputeImpact: func(s *store.Store, targets []string) (proposal.Impact, error) {
			tr.seq++
			tr.recomputed = tr.seq
			tr.recalls++
			return def.recomputeImpact(s, targets)
		},
		markApproved: func(spec proposal.ApproveSpec) (store.Result, error) {
			tr.seq++
			tr.approved = tr.seq
			tr.writes++
			if !real {
				return store.Result{Path: proposal.Rel(spec.Original)}, nil
			}
			return def.markApproved(spec)
		},
		supersede: func(spec proposal.SupersedeSpec) (proposal.SupersedeResult, error) {
			tr.seq++
			tr.superseded = tr.seq
			tr.writes++
			if !real {
				return proposal.SupersedeResult{Trigger: spec.Trigger}, nil
			}
			return def.supersede(spec)
		},
	}
}

// assertRecomputeFirst 反证重算发生在**任何**写盘动作之前，且恰被调一次、写盘至多一次。
func (tr *apTrace) assertRecomputeFirst(t *testing.T) {
	t.Helper()
	if tr.recalls != 1 {
		t.Fatalf("执行前重算必须恰发生 1 次，实得 %d 次（不得省略、不得重复）", tr.recalls)
	}
	if tr.recomputed != 1 {
		t.Fatalf("重算的调用序号 = %d，必须是第 1 个动作（先重算，后落盘）", tr.recomputed)
	}
	if tr.writes > 1 {
		t.Fatalf("一次批准最多一个写盘分支，实得 %d 次", tr.writes)
	}
	for name, at := range map[string]int{"markApproved": tr.approved, "supersede": tr.superseded} {
		if at != 0 && at < tr.recomputed {
			t.Fatalf("写盘动作 %s 的序号 %d 早于重算 %d：违反「执行前必须重算」", name, at, tr.recomputed)
		}
	}
}

// TestApprove_RecomputesImpactBeforeExecute 钉死 §3 的唯一实现顺序：
// 两个分支（前提成立走 T1、前提变化走触发②）都必须**先重算再落盘**。
func TestApprove_RecomputesImpactBeforeExecute(t *testing.T) {
	// —— 分支 A：语料未变 → 重算与 impact 一致 → T1 批准，且重算在写盘之前 ——
	t.Run("前提成立仍必须先重算", func(t *testing.T) {
		st, root := apVault(t)
		tr := &apTrace{}
		out, err := approveProposal(st, approveRequest{
			ID: apProposalID, Reason: "影响面已核对", Today: apToday(t),
		}, tr.hooks(true))
		if err != nil {
			t.Fatalf("approveProposal：%v", err)
		}
		tr.assertRecomputeFirst(t)
		if !out.Executed || !out.Decision.Proceed || out.Decision.Edge != "T1" {
			t.Fatalf("前提成立时应走 T1 批准，实得 %+v", out.Decision)
		}
		if out.Superseded != nil {
			t.Fatalf("前提成立时不得走 superseded 分支")
		}
		if tr.approved == 0 || tr.superseded != 0 {
			t.Fatalf("落盘分支不对：markApproved 序号 %d，supersede 序号 %d", tr.approved, tr.superseded)
		}
		// 落盘事实：提案已批准，`execution` 一字未动（`approved` ≠ 已执行）。
		f, err := st.Read(proposal.Rel(apProposalID))
		if err != nil {
			t.Fatalf("读回提案：%v", err)
		}
		p, err := proposal.Parse(f.Bytes)
		if err != nil {
			t.Fatalf("解析提案：%v", err)
		}
		if p.P.Status != proposal.StatusApproved {
			t.Fatalf("提案 status = %s，期望 %s", p.P.Status, proposal.StatusApproved)
		}
		if p.P.Execution.Status != proposal.ExecNotStarted {
			t.Fatalf("批准不得回写 execution：实得 %s", p.P.Execution.Status)
		}
		// 知识数据零写入：删除本身仍走 ChangePlan 的 delete op（A-23 第 4 条）。
		card := filepath.Join(root, "domains", apDomain, "knowledge", apCard+".md")
		b, err := os.ReadFile(card)
		if err != nil {
			t.Fatalf("读卡：%v", err)
		}
		if strings.Contains(string(b), "deleted_at") {
			t.Fatalf("批准动作不得改知识数据（卡上出现了删除标记）")
		}
	})

	// —— 分支 B：批准前语料变了 → **不执行删除**，改走触发②，同样先重算再落盘 ——
	t.Run("前提变化时不执行删除", func(t *testing.T) {
		st, root := apVault(t)
		// 目标卡已被别的路径逻辑删除 → 它不再会「退出默认视图」，影响面与提案记录不一致。
		card := filepath.Join(root, "domains", apDomain, "knowledge", apCard+".md")
		b, err := os.ReadFile(card)
		if err != nil {
			t.Fatalf("读卡：%v", err)
		}
		changed := strings.Replace(string(b), "status: active\n",
			"status: superseded\ndeleted_at: '2026-10-16T09:00:00+08:00'\ndeleted_reason: 已被更新的卡取代\n", 1)
		if changed == string(b) {
			t.Fatalf("夹具没有改动卡的状态")
		}
		if err := os.WriteFile(card, []byte(changed), 0o644); err != nil {
			t.Fatalf("改夹具：%v", err)
		}
		tr := &apTrace{}
		out, err := approveProposal(st, approveRequest{
			ID: apProposalID, Reason: "执行前影响面已变化", Today: apToday(t),
			NewTitle: "前提变化：按当前影响面重新描述",
		}, tr.hooks(true))
		if err != nil {
			t.Fatalf("approveProposal：%v", err)
		}
		tr.assertRecomputeFirst(t)
		if out.Executed || out.Decision.Proceed {
			t.Fatalf("前提变化时**不得**继续执行")
		}
		if tr.superseded == 0 || tr.approved != 0 {
			t.Fatalf("落盘分支不对：supersede 序号 %d，markApproved 序号 %d", tr.superseded, tr.approved)
		}
		if out.Decision.Trigger != proposal.TriggerImpactChanged {
			t.Fatalf("触发种类 = %q，期望 %q", out.Decision.Trigger, proposal.TriggerImpactChanged)
		}
		if out.Superseded == nil || len(out.Superseded.Steps) != 3 {
			t.Fatalf("触发② 的三步动作必须齐备：%+v", out.Superseded)
		}
		// 报告逐字含「不执行删除」。
		joined := strings.Join(out.Lines, "\n")
		if !strings.Contains(joined, proposal.NotExecutedNotice) {
			t.Fatalf("报告必须逐字含「%s」，实得：\n%s", proposal.NotExecutedNotice, joined)
		}
		if !strings.Contains(joined, "影响面差异：") {
			t.Fatalf("报告必须说明原影响范围为何不再成立：\n%s", joined)
		}
		// 原提案标 superseded 并写 superseded_by；后继提案在库。
		f, err := st.Read(proposal.Rel(apProposalID))
		if err != nil {
			t.Fatalf("读回提案：%v", err)
		}
		p, err := proposal.Parse(f.Bytes)
		if err != nil {
			t.Fatalf("解析提案：%v", err)
		}
		if p.P.Status != proposal.StatusSuperseded {
			t.Fatalf("原提案 status = %s，期望 %s", p.P.Status, proposal.StatusSuperseded)
		}
		if p.P.Decision.SupersededBy != string(out.Superseded.NewID) {
			t.Fatalf("原提案 superseded_by = %q，期望 %s", p.P.Decision.SupersededBy, out.Superseded.NewID)
		}
		probe, err := proposal.ExistsProbe(st)
		if err != nil {
			t.Fatalf("ExistsProbe：%v", err)
		}
		if err := proposal.CheckSupersededChain(p.P, probe); err != nil {
			t.Fatalf("提案链应当成立：%v", err)
		}
	})

	// —— 分支 C：重算失败 → 直接返回、**零写盘**（不得跳过重算硬上）——
	t.Run("重算失败即零写盘", func(t *testing.T) {
		st, _ := apVault(t)
		tr := &apTrace{}
		h := tr.hooks(false)
		boom := fmt.Errorf("扫描失败")
		h.recomputeImpact = func(s *store.Store, targets []string) (proposal.Impact, error) {
			tr.seq++
			tr.recomputed = tr.seq
			tr.recalls++
			return proposal.Impact{}, boom
		}
		if _, err := approveProposal(st, approveRequest{ID: apProposalID, Today: apToday(t)}, h); err == nil {
			t.Fatalf("重算失败时必须返回错误")
		}
		if tr.writes != 0 {
			t.Fatalf("重算失败后不得写盘，实得 %d 次", tr.writes)
		}
		f, err := st.Read(proposal.Rel(apProposalID))
		if err != nil {
			t.Fatalf("读回提案：%v", err)
		}
		p, err := proposal.Parse(f.Bytes)
		if err != nil {
			t.Fatalf("解析提案：%v", err)
		}
		if p.P.Status != proposal.StatusPending {
			t.Fatalf("提案 status = %s，期望仍是 %s", p.P.Status, proposal.StatusPending)
		}
	})
}
