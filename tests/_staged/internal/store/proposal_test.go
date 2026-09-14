package store

// 提案控制面写口的机器判据（A-23 的直写例外落地，T-evergreen.s1_main_flow-158614-035）。
//
// A-23 逐字：「仅限 `proposals/**` 提案控制面。**必须复用 guarded store，禁止裸写文件**。」
// 因此本文件钉三件事：
//   - 路径**只**接受提案目录下的相对路径（知识数据面一律拒）；
//   - 改写走 mutateGuarded：content_hash 不匹配即拒（B3），且不留 tmp 残留；
//   - 新建不覆盖既有文件，写前字节自检不过即拒（形态坏的候选字节永不落盘）。
//
// B1 的「写形态恰三种」由 TestB1WriteFormsAreExactlyThree 独立锁死：本文件的两个入口
// 是 `Apply*` 领域写口（叠路径约束 + 复用 CreateFile / mutateGuarded），不是第四种写形态。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// proposalSample 是一份**恰合规**的提案（8 个顶层键 + 7 个 H2）；
// store 不解提案语义，这里只要求它 Parse→Render 逐字可复原。
const proposalSample = `---
id: p-20261017-001
type: logical_delete
status: pending
created_at: '2026-10-17'
targets:
  - k-20260901-attention
impact:
  exits_default_view:
    - k-20260901-attention
  cards_losing_support: []
  affected_material_rels: 0
  affected_relations: 0
  stale_reviews: []
decision:
  result:
  reason:
  superseded_by:
execution:
  status: 'not_started'
  attempted_at:
  reason:
  git_commit:
  written_paths: []
  unwritten_paths: []
---

# 逻辑删除一张过时卡

## 推荐修改

## 理由与证据

## 影响的文件、领域与关系

## 执行后状态

## 不执行的影响

## 替代方案

## 可应用内容
`

// TestProposalWritePortRejectsOutsideDir：两个写口都只认 `proposals/**`（A-23 第 1 条）。
func TestProposalWritePortRejectsOutsideDir(t *testing.T) {
	s, root := newVault(t)
	for _, rel := range []string{
		"domains/ai-infra/knowledge/k-20260901-attention.md",
		"sources/s-20260901-a.md",
		"proposals.md",
		"proposal/p-20261017-001.md",
	} {
		if IsProposalRel(rel) {
			t.Fatalf("%s 不该被判成提案控制面路径", rel)
		}
		if _, err := s.ApplyProposalCreate(rel, []byte(proposalSample)); err == nil {
			t.Fatalf("新建写口必须拒绝非提案路径：%s", rel)
		}
		if _, err := s.ApplyProposalUpdate(ProposalUpdateSpec{Rel: rel, Content: []byte(proposalSample)}); err == nil {
			t.Fatalf("改写写口必须拒绝非提案路径：%s", rel)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			t.Fatalf("被拒的路径 %s 不得被创建", rel)
		}
	}
	if !IsProposalRel("proposals/p-20261017-001.md") {
		t.Fatalf("提案目录下的路径必须被判成提案控制面")
	}
	// 空候选字节一律拒（空文件不是提案）。
	if _, err := s.ApplyProposalCreate("proposals/p-20261017-001.md", nil); err == nil {
		t.Fatalf("空候选字节必须被拒")
	}
}

// TestProposalWritePortGuarded：新建不覆盖、改写按 content_hash 守卫、坏字节拒写、无 tmp 残留。
func TestProposalWritePortGuarded(t *testing.T) {
	s, root := newVault(t)
	rel := "proposals/p-20261017-001.md"
	res, err := s.ApplyProposalCreate(rel, []byte(proposalSample))
	if err != nil {
		t.Fatalf("新建提案：%v", err)
	}
	if res.Hash == "" {
		t.Fatalf("回执必须带 content_hash")
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if got := string(mustBytes(t, abs)); got != proposalSample {
		t.Fatalf("落盘字节与候选字节不逐字相同")
	}
	// 不覆盖既有文件。
	if _, err := s.ApplyProposalCreate(rel, []byte(proposalSample)); err == nil {
		t.Fatalf("新建写口不得覆盖既有提案")
	}
	// 形态坏的候选字节：写前自检拒写，原文不变。
	if _, err := s.ApplyProposalUpdate(ProposalUpdateSpec{
		Rel: rel, ExpectedHash: res.Hash, Content: []byte(malformedSample),
	}); err == nil {
		t.Fatalf("Parse→Render 不等的候选字节必须被拒")
	}
	if got := string(mustBytes(t, abs)); got != proposalSample {
		t.Fatalf("拒写后原文必须保持现状")
	}
	// content_hash 不匹配（B3）：拒写。
	if _, err := s.ApplyProposalUpdate(ProposalUpdateSpec{
		Rel: rel, ExpectedHash: "sha256:0000", Content: []byte(proposalSample),
	}); err == nil {
		t.Fatalf("content_hash 不匹配时必须拒写")
	}
	// 正常改写：status 改成 approved，其余字节逐字不变。
	updated := strings.Replace(proposalSample, "status: pending\n", "status: approved\n", 1)
	if updated == proposalSample {
		t.Fatalf("夹具未改动 status")
	}
	out, err := s.ApplyProposalUpdate(ProposalUpdateSpec{
		Rel: rel, ExpectedHash: res.Hash, Content: []byte(updated),
	})
	if err != nil {
		t.Fatalf("改写提案：%v", err)
	}
	if out.Hash == res.Hash {
		t.Fatalf("改写后 content_hash 必须变化")
	}
	if got := string(mustBytes(t, abs)); got != updated {
		t.Fatalf("落盘字节与候选字节不逐字相同")
	}
	assertNoTmp(t, filepath.Dir(abs))
}
