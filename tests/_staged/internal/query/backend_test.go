package query

// [S4] 读路径接入索引与降级的机器判据（M5 · T-evergreen.s1_main_flow-158614-067）。
//
// 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
//
//	§1.1 P-2（Markdown 是唯一权威来源）、§5.2（索引不可用 / 陈旧一律降级为全量扫描）、
//	§6.1（索引问题不阻断读、不改退出码）、§6.2 / §6.3（W22 / W23 / W24 与 Q5 的分配与同现）、
//	§6.4（data 键集合不扩张：降级事实只经诊断承载）、§7.4（索引只出候选集）。
//
// 三条纪律：
//   - **零 mock**：坏索引一律往 `.index/eg.db` 写真实非法字节，陈旧一律真改权威文件；
//     后端选择与降级全走产品代码，没有一处打桩。
//   - **等价性用逐字比对反证**：不是「看起来一样」，而是索引在位与索引删除两次调用的
//     结果结构体逐字 DeepEqual（诊断只允许多出降级那两条）。
//   - **本文件不碰 T-…-068**：不断言排序键、截断、分页、反查性能与任何时间门槛。
//
// 本文件是**包内**测试（package query）：它要断言的正是「取数走了哪条后端」这件
// 不进 data 的内部事实（合同 §6.4 不许它进输出），因此只能从包内读 res.backend。

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// bkFixedNow 是本文件的固定时钟：`built_at_unix` 可复算，用例全程确定性。
var bkFixedNow = time.Date(2026, 12, 29, 10, 0, 0, 0, time.UTC)

// bkDeps 是本文件注入的 A-44 水位线口径，与 internal/cli 的 readIndexDeps **同源**：
// content_hash 走 M1 写口的 `store.ContentHash`（B3），head 取空串（用例 vault 非 git 仓，
// 与 cli 的 indexHead 在非 git 仓下的取值逐字相同）。
//
// 不注入 = 证不出新鲜度 = 恒走扫描（见 ReasonFreshnessUnverifiable），那样本文件
// 「健康索引必须走索引后端」的判据会变成空断言，因此三条读路径一律经 bkXxx 包装注入。
var bkDeps = IndexDeps{Hash: store.ContentHash, Head: func() string { return "" }}

// bkSearch / bkShow / bkRel 是注入口径后的三条读路径（生产 CLI 的等价调用形态）。
func bkSearch(root string, req SearchRequest) (*SearchResult, error) {
	req.Index = bkDeps
	return Search(root, req)
}

func bkShow(root string, id model.CardID, opts ...VisibilityPolicy) (*CardShowResult, error) {
	return ShowCardWith(root, id, bkDeps, opts...)
}

func bkRel(root string, req RelRequest) (*RelResult, error) {
	req.Index = bkDeps
	return RelView(root, req)
}

// bkWrite 在 vault 内写一个文件（测试脚手架，非产品写路径）。
func bkWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

// bkCard 造一张最小合法知识卡（tags / relations 逐字拼入 frontmatter）。
func bkCard(id, title, status, updated, tags, relations string) string {
	fm := "---\nid: " + id + "\nstatus: " + status +
		"\ncreated_at: '2026-12-01'\nupdated_at: '" + updated +
		"'\ntitle: " + title + "\nsources: []\n"
	if tags != "" {
		fm += "tags:\n" + tags
	}
	if relations != "" {
		fm += "relations:\n" + relations
	}
	return fm + "---\n\n# " + title + "\n\n## 知识内容\n\n正文占位：注意力机制。\n"
}

// bkVault 造一个**覆盖面完整**的语料：正常卡、失效卡、带 reason 的正反向关系、
// 悬空引用（Q2）、不可解析文件（Q1）、多标签与不同 updated_at（喂 search 的过滤维度）。
//
// 焦点卡 attention 同时有正向边（→ rnn）与反向来源（ops → attention，带 reason）：
// 索引后端的「按 dst_id 反查来源卡 + 回权威取逐字 reason」因此被真实覆盖。
func bkVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	k := "domains/ai-infra/knowledge/"
	bkWrite(t, root, k+"k-20261201-attention.md", bkCard("k-20261201-attention", "注意力",
		"active", "2026-12-05T10:00:00+08:00", "  - transformer\n  - 核心\n",
		"  - type: derives\n    target: k-20261201-rnn\n    reason: 从循环网络演进\n"+
			"  - type: limits\n    target: k-20261201-missing\n    reason: 指向不存在的卡\n"))
	bkWrite(t, root, k+"k-20261201-rnn.md", bkCard("k-20261201-rnn", "循环网络",
		"deprecated", "2026-12-03T10:00:00+08:00", "  - rnn\n", ""))
	bkWrite(t, root, k+"k-20261201-ops.md", bkCard("k-20261201-ops", "算子",
		"active", "2026-12-07T10:00:00+08:00", "  - 核心\n",
		"  - type: supports\n    target: k-20261201-attention\n    reason: 算子支撑注意力\n"))
	bkWrite(t, root, "domains/ai-infra/knowledge/broken.md", "---\n- 1\n---\n\n# 坏卡\n")
	bkWrite(t, root, "domains/mlsys/knowledge/k-20261201-sched.md", bkCard("k-20261201-sched",
		"调度", "active", "2026-12-09T10:00:00+08:00", "  - 核心\n", ""))
	return root
}

// bkBuildIndex 用权威扫描结果建一次全量索引（与 internal/cli 的 indexSnapshotWith 同口径：
// 扫描面 = 知识卡、`content_hash` 走 store 的 B3 同源实现、笔记不进索引）。
//
// 这里刻意**不**调命令层：query 不依赖 cli（§13 依赖方向），但索引的物理形态必须是真库。
func bkBuildIndex(t *testing.T, root string) {
	t.Helper()
	scan, err := VaultScan(root, ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	snap := index.Snapshot{}
	for _, c := range scan.Cards {
		st, err := os.Stat(filepath.Join(root, filepath.FromSlash(c.Path)))
		if err != nil {
			t.Fatalf("stat %s：%v", c.Path, err)
		}
		hash := store.ContentHash(c.Raw)
		snap.Cards = append(snap.Cards, index.Card{
			ID: c.ID, Path: c.Path, Domain: c.Domain, Title: c.Title, Status: c.Status,
			Deprecated: c.Deprecated, Deleted: c.Deleted, Body: c.Body(),
			ContentHash: hash, MTimeUnix: st.ModTime().Unix(),
		})
		snap.Files = append(snap.Files, index.File{
			Path: c.Path, ContentHash: hash, Size: st.Size(), MTimeUnix: st.ModTime().Unix(),
		})
		for _, rel := range c.Relations {
			snap.Relations = append(snap.Relations, index.Relation{
				SrcID: c.ID, Verb: string(rel.Type), DstID: string(rel.Target), SrcPath: c.Path,
			})
		}
	}
	if _, err := index.Build(index.DirPath(root), snap,
		index.Options{Now: func() time.Time { return bkFixedNow }}); err != nil {
		t.Fatalf("index.Build：%v", err)
	}
}

// bkHealthyVault 造语料并建索引，返回一个**探测为 fresh** 的 vault。
func bkHealthyVault(t *testing.T) string {
	t.Helper()
	root := bkVault(t)
	bkBuildIndex(t, root)
	if got := SelectBackend(root, cardNeed("k-20261201-attention", bkDeps)); !got.UseIndex() {
		t.Fatalf("刚建好的索引应判健康并走索引后端，实际 kind=%s reason=%s message=%s",
			got.Kind, got.Reason, got.Message)
	}
	return root
}

// bkDropIndex 删掉整个 `.index/`（index_missing → W23）。
func bkDropIndex(t *testing.T, root string) {
	t.Helper()
	if err := os.RemoveAll(index.DirPath(root)); err != nil {
		t.Fatalf("删 .index/ 失败：%v", err)
	}
}

// bkCorruptIndex 往 `.index/eg.db` 写真实非法字节（index_corrupt → W24）。
func bkCorruptIndex(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(index.DBPath(root), []byte("这不是 SQLite 文件"), 0o644); err != nil {
		t.Fatalf("写坏索引失败：%v", err)
	}
}

// bkMakeStale 真改一张权威卡的字节并把 mtime 推后（index_stale → W22）。
func bkMakeStale(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash("domains/ai-infra/knowledge/k-20261201-rnn.md"))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读卡失败：%v", err)
	}
	if err := os.WriteFile(p, append(raw, []byte("\n补一段正文：门控。\n")...), 0o644); err != nil {
		t.Fatalf("改卡失败：%v", err)
	}
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatalf("改 mtime 失败：%v", err)
	}
}

// bkCodes 取诊断码序列。
func bkCodes(diags []Diagnostic) []string {
	out := []string{}
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

// bkCount 数某个码出现的条数。
func bkCount(diags []Diagnostic, code string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

// bkReadAll 在一个 vault 上跑三条读路径（同一批参数，供两种索引状态对照）。
func bkReadAll(t *testing.T, root string) (*SearchResult, *CardShowResult, *RelResult) {
	t.Helper()
	sr, err := bkSearch(root, SearchRequest{Query: "注意力 循环 算子 调度"})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	cv, err := bkShow(root, model.CardID("k-20261201-attention"))
	if err != nil {
		t.Fatalf("ShowCard：%v", err)
	}
	rv, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-attention")})
	if err != nil {
		t.Fatalf("RelView：%v", err)
	}
	return sr, cv, rv
}

// —— ① 后端选择单点 ——

// TestBackendSelectionSinglePoint —— 判据 9：选择逻辑只有 SelectBackend 一处，
// 且它在**五种状态**下的结论确定：
//
//	fresh                  → 索引后端、无码、不降级
//	missing                → 扫描后端 + W23、降级
//	corrupt                → 扫描后端 + W24、降级
//	stale                  → 扫描后端 + W22、降级
//	查询不可由索引表达      → 扫描后端、**无码、不降级**（索引没坏也没旧 ⇒ 不产 Q5）
func TestBackendSelectionSinglePoint(t *testing.T) {
	root := bkHealthyVault(t)

	// fresh：三条读路径的 Need 都走索引。
	for _, need := range []Need{searchNeed(bkDeps), cardNeed("k-20261201-attention", bkDeps), relNeed("k-20261201-attention", bkDeps)} {
		b := SelectBackend(root, need)
		if b.Kind != BackendIndex || b.Code != "" || b.Degraded() {
			t.Fatalf("%s 在健康索引下应走索引后端且无降级码，实际 kind=%s code=%q reason=%q",
				need.Path, b.Kind, b.Code, b.Reason)
		}
		if b.Freshness != index.FreshnessFresh {
			t.Fatalf("%s 的新鲜度应为 fresh，实际 %s", need.Path, b.Freshness)
		}
	}

	// 查询不可由索引表达（含材料笔记面）：走扫描，但**不是**降级。
	nb := SelectBackend(root, notesNeed("unreviewed", bkDeps))
	if nb.Kind != BackendScan || nb.Reason != ReasonQueryNotExpressible {
		t.Fatalf("笔记面应走扫描后端且原因为 %s，实际 kind=%s reason=%s",
			ReasonQueryNotExpressible, nb.Kind, nb.Reason)
	}
	if nb.Code != "" || nb.Degraded() {
		t.Fatalf("不可表达≠降级：不得带诊断码，实际 code=%q", nb.Code)
	}

	// missing / corrupt / stale：逐个真状态比对码与原因（码取自 internal/index 常量）。
	cases := []struct {
		name   string
		break_ func(*testing.T, string)
		code   string
		reason string
	}{
		{"missing", bkDropIndex, index.CodeIndexMissing, ReasonIndexUnusable},
		{"corrupt", bkCorruptIndex, index.CodeIndexCorrupt, ReasonIndexUnusable},
		{"stale", bkMakeStale, index.CodeIndexStale, ReasonIndexStale},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := bkHealthyVault(t)
			c.break_(t, r)
			b := SelectBackend(r, cardNeed("k-20261201-attention", bkDeps))
			if b.Kind != BackendScan {
				t.Fatalf("%s 必须降级为扫描后端，实际 %s", c.name, b.Kind)
			}
			if b.Code != c.code || b.Reason != c.reason {
				t.Fatalf("%s 的码 / 原因 = %q / %q，期望 %q / %q",
					c.name, b.Code, b.Reason, c.code, c.reason)
			}
			if !b.Degraded() || b.Message == "" {
				t.Fatalf("%s 必须判为降级且带人类可读说明，实际 degraded=%v message=%q",
					c.name, b.Degraded(), b.Message)
			}
		})
	}
}

// —— ② 三条读路径在健康索引下走索引后端 ——

// TestSearchUsesIndexWhenHealthy —— 判据 9：`eg search` 健康索引下取数走**索引后端**，
// 且结果里一条 Q5 都没有（不是降级）。
func TestSearchUsesIndexWhenHealthy(t *testing.T) {
	root := bkHealthyVault(t)
	res, err := bkSearch(root, SearchRequest{Query: "注意力"})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	if res.backend.Kind != BackendIndex {
		t.Fatalf("search 应走索引后端，实际 %s（%s）", res.backend.Kind, res.backend.Message)
	}
	if bkCount(res.Diagnostics, CodeQ5) != 0 {
		t.Fatalf("健康索引下不得产 Q5，实际诊断 %v", bkCodes(res.Diagnostics))
	}
	if res.Total == 0 {
		t.Fatalf("语料含「注意力」卡，命中数不应为 0")
	}
}

// TestCardShowUsesIndexWhenHealthy —— 同上，`eg card show`。
func TestCardShowUsesIndexWhenHealthy(t *testing.T) {
	root := bkHealthyVault(t)
	view, err := bkShow(root, model.CardID("k-20261201-attention"))
	if err != nil {
		t.Fatalf("ShowCard：%v", err)
	}
	if view.backend.Kind != BackendIndex {
		t.Fatalf("card show 应走索引后端，实际 %s（%s）", view.backend.Kind, view.backend.Message)
	}
	if bkCount(view.Diagnostics, CodeQ5) != 0 {
		t.Fatalf("健康索引下不得产 Q5，实际诊断 %v", bkCodes(view.Diagnostics))
	}
	// 权威字段必须真的从 Markdown 解析而来（索引里没有正文 / tags / 时间戳）。
	if view.Card.Sections.Get("知识内容") == "" || len(view.Card.Tags) == 0 ||
		view.Card.UpdatedAt == "" {
		t.Fatalf("焦点卡必须回权威解析：知识内容分区=%q tags=%v updated_at=%q",
			view.Card.Sections.Get("知识内容"), view.Card.Tags, view.Card.UpdatedAt)
	}
}

// TestRelUsesIndexWhenHealthy —— 同上，`eg rel`；并逐字反证反向边的 reason
// 是从**权威源文件**取的（索引 relations 表不存 reason）。
func TestRelUsesIndexWhenHealthy(t *testing.T) {
	root := bkHealthyVault(t)
	res, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-attention")})
	if err != nil {
		t.Fatalf("RelView：%v", err)
	}
	if res.backend.Kind != BackendIndex {
		t.Fatalf("rel 应走索引后端，实际 %s（%s）", res.backend.Kind, res.backend.Message)
	}
	if bkCount(res.Diagnostics, CodeQ5) != 0 {
		t.Fatalf("健康索引下不得产 Q5，实际诊断 %v", bkCodes(res.Diagnostics))
	}
	if len(res.Data.RelationsIn) != 1 {
		t.Fatalf("反向来源应恰一条（ops → attention），实际 %d 条", len(res.Data.RelationsIn))
	}
	if got := res.Data.RelationsIn[0].Reason; got != "算子支撑注意力" {
		t.Fatalf("反向边 reason 必须是权威逐字原值，实际 %q", got)
	}
}

// —— ③ 等价性：索引后端与扫描后端逐字相同 ——

// TestIndexAndScanResultsIdentical —— M-005 判据 8 的核心：同一语料上
// 「索引在位」与「索引删除」两次读，三条读路径的结果**逐字相等**；
// 诊断集合只允许多出降级那两条（W23 + Q5），其余一条不差。
func TestIndexAndScanResultsIdentical(t *testing.T) {
	root := bkHealthyVault(t)
	idxSearch, idxCard, idxRel := bkReadAll(t, root)
	if idxSearch.backend.Kind != BackendIndex {
		t.Fatalf("前置：第一轮必须走索引后端，实际 %s", idxSearch.backend.Kind)
	}
	bkDropIndex(t, root)
	scanSearch, scanCard, scanRel := bkReadAll(t, root)
	if scanSearch.backend.Kind != BackendScan {
		t.Fatalf("前置：第二轮必须走扫描后端，实际 %s", scanSearch.backend.Kind)
	}

	if !reflect.DeepEqual(idxSearch.Hits, scanSearch.Hits) {
		t.Fatalf("search hits 不等价：\n索引 %+v\n扫描 %+v", idxSearch.Hits, scanSearch.Hits)
	}
	if idxSearch.Total != scanSearch.Total ||
		idxSearch.ScannedFiles != scanSearch.ScannedFiles ||
		idxSearch.SkippedFiles != scanSearch.SkippedFiles {
		t.Fatalf("search 计数不等价：索引 %d/%d/%d，扫描 %d/%d/%d",
			idxSearch.Total, idxSearch.ScannedFiles, idxSearch.SkippedFiles,
			scanSearch.Total, scanSearch.ScannedFiles, scanSearch.SkippedFiles)
	}
	if !reflect.DeepEqual(idxCard.Card, scanCard.Card) {
		t.Fatalf("card show 的 card 不等价：\n索引 %+v\n扫描 %+v", idxCard.Card, scanCard.Card)
	}
	if !reflect.DeepEqual(idxCard.MissingTargets, scanCard.MissingTargets) ||
		idxCard.ScannedFiles != scanCard.ScannedFiles ||
		idxCard.SkippedFiles != scanCard.SkippedFiles {
		t.Fatalf("card show 的附属事实不等价：%+v vs %+v", idxCard, scanCard)
	}
	if !reflect.DeepEqual(idxRel.Data, scanRel.Data) {
		t.Fatalf("rel data 不等价：\n索引 %+v\n扫描 %+v", idxRel.Data, scanRel.Data)
	}
	if !reflect.DeepEqual(idxRel.MissingTargets, scanRel.MissingTargets) {
		t.Fatalf("rel missing_targets 不等价：%v vs %v", idxRel.MissingTargets, scanRel.MissingTargets)
	}

	// 诊断：扫描轮 == 索引轮 + 恰两条降级留痕（W23 + Q5），其余逐字相同。
	for _, c := range []struct {
		name      string
		idx, scan []Diagnostic
	}{
		{"search", idxSearch.Diagnostics, scanSearch.Diagnostics},
		{"card show", idxCard.Diagnostics, scanCard.Diagnostics},
		{"rel", idxRel.Diagnostics, scanRel.Diagnostics},
	} {
		if len(c.scan) != len(c.idx)+2 {
			t.Fatalf("%s 的诊断条数：索引轮 %v，扫描轮 %v，应恰多两条（W23 + Q5）",
				c.name, bkCodes(c.idx), bkCodes(c.scan))
		}
		var kept []Diagnostic
		for _, d := range c.scan {
			if d.Code == CodeQ5 || d.Code == index.CodeIndexMissing {
				continue
			}
			kept = append(kept, d)
		}
		if !reflect.DeepEqual(kept, c.idx) && !(len(kept) == 0 && len(c.idx) == 0) {
			t.Fatalf("%s 去掉降级两条后诊断必须逐字相同：\n索引 %+v\n扫描 %+v", c.name, c.idx, kept)
		}
	}
}

// TestIndexBackendCountsConserved —— I-…-002「计数双源」的关闭判据在索引后端上同样成立：
// `ScannedFiles == len(Cards) + len(Notes) + SkippedFiles`，且计数与扫描后端逐字相同。
func TestIndexBackendCountsConserved(t *testing.T) {
	root := bkHealthyVault(t)
	need := cardNeed("k-20261201-attention", bkDeps)
	b := SelectBackend(root, need)
	res, _, err := loadVault(root, ScanOptions{}, need, b)
	if err != nil {
		t.Fatalf("loadVault：%v", err)
	}
	if res.ScannedFiles != len(res.Cards)+len(res.Notes)+res.SkippedFiles {
		t.Fatalf("守恒式不成立：scanned=%d cards=%d notes=%d skipped=%d",
			res.ScannedFiles, len(res.Cards), len(res.Notes), res.SkippedFiles)
	}
	scan, err := VaultScan(root, ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if res.ScannedFiles != scan.ScannedFiles || res.SkippedFiles != scan.SkippedFiles {
		t.Fatalf("两条后端的计数必须同源同值：索引 %d/%d，扫描 %d/%d",
			res.ScannedFiles, res.SkippedFiles, scan.ScannedFiles, scan.SkippedFiles)
	}
}

// TestIndexBackendNoStubLeaks —— 摘要条目（stub）绝不泄漏进结果：
// `search` 走 plan.all，每条命中的 tags / updated_at / created_at 都必须是权威原值。
func TestIndexBackendNoStubLeaks(t *testing.T) {
	root := bkHealthyVault(t)
	res, err := bkSearch(root, SearchRequest{Query: "注意力 循环 算子 调度"})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	if res.backend.Kind != BackendIndex || res.Total == 0 {
		t.Fatalf("前置不成立：backend=%s total=%d", res.backend.Kind, res.Total)
	}
	for _, h := range res.Hits {
		if h.UpdatedAt == "" || h.CreatedAt == "" {
			t.Fatalf("命中 %s 的时间戳为空：索引里没有这两格，必须回权威解析", h.ID)
		}
		if h.ID == "k-20261201-attention" && len(h.Tags) != 2 {
			t.Fatalf("命中 %s 的 tags = %v，期望权威原值两条", h.ID, h.Tags)
		}
	}
}

// TestBrokenFileDoesNotForceDegrade —— 语料里有**解析不了**的文件（Q1）时，
// 索引仍判健康：`files` 表天生不收这类文件（构建快照来自扫描结果），
// 把它当「索引缺了东西」会让一个坏文件永久废掉索引读路径。
func TestBrokenFileDoesNotForceDegrade(t *testing.T) {
	root := bkHealthyVault(t) // bkVault 里本就含 broken.md
	res, err := bkSearch(root, SearchRequest{Query: "注意力"})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	if res.backend.Kind != BackendIndex {
		t.Fatalf("含坏文件的语料仍应走索引后端，实际 %s（%s）", res.backend.Kind, res.backend.Message)
	}
	if res.SkippedFiles != 1 || bkCount(res.Diagnostics, CodeQ1) != 1 {
		t.Fatalf("坏文件必须如实记 Q1 并计入跳过：skipped=%d 诊断 %v",
			res.SkippedFiles, bkCodes(res.Diagnostics))
	}
}

// —— ④ 三形态降级 + 索引落后于权威 ——

// bkAssertDegraded 断言一次降级读的完整口径：不报错、有结果、恰一条原因码 + 恰一条 Q5、
// 且 Q5 紧跟原因码、Q3 恒末位（若有）。
func bkAssertDegraded(t *testing.T, root, wantCode string) {
	t.Helper()
	sr, cv, rv := bkReadAll(t, root)
	if sr.Total == 0 || cv.Card.ID == "" || rv.Data.ID == "" {
		t.Fatalf("降级不得丢结果：search total=%d card=%q rel=%q",
			sr.Total, cv.Card.ID, rv.Data.ID)
	}
	for _, c := range []struct {
		name    string
		kind    string
		diags   []Diagnostic
		backend Backend
	}{
		{"search", sr.backend.Kind, sr.Diagnostics, sr.backend},
		{"card show", cv.backend.Kind, cv.Diagnostics, cv.backend},
		{"rel", rv.backend.Kind, rv.Diagnostics, rv.backend},
	} {
		if c.kind != BackendScan {
			t.Fatalf("%s 必须降级为扫描后端，实际 %s", c.name, c.kind)
		}
		if n := bkCount(c.diags, wantCode); n != 1 {
			t.Fatalf("%s 应恰一条 %s，实际 %d 条（诊断 %v）", c.name, wantCode, n, bkCodes(c.diags))
		}
		if n := bkCount(c.diags, CodeQ5); n != 1 {
			t.Fatalf("%s 应恰一条 Q5，实际 %d 条（诊断 %v）", c.name, n, bkCodes(c.diags))
		}
		codes := bkCodes(c.diags)
		iCode, iQ5, iQ3 := -1, -1, -1
		for i, code := range codes {
			switch code {
			case wantCode:
				iCode = i
			case CodeQ5:
				iQ5 = i
			case CodeQ3:
				iQ3 = i
			}
		}
		if iQ5 != iCode+1 {
			t.Fatalf("%s 的 Q5 必须紧跟原因码（同现且成对），实际次序 %v", c.name, codes)
		}
		if iQ3 >= 0 && iQ3 != len(codes)-1 {
			t.Fatalf("%s 的 Q3 必须恒末位，实际次序 %v", c.name, codes)
		}
		for _, d := range c.diags {
			if d.Code == wantCode || d.Code == CodeQ5 {
				if d.Level != DiagLevel || d.Path != diagSummaryPath || d.Message == "" {
					t.Fatalf("%s 的 %s 口径不符：%+v", c.name, d.Code, d)
				}
			}
		}
	}
}

// TestMissingIndexDegradesNotFails —— `.index/` 整个不存在：照常出结果 + W23 + Q5。
func TestMissingIndexDegradesNotFails(t *testing.T) {
	root := bkHealthyVault(t)
	bkDropIndex(t, root)
	bkAssertDegraded(t, root, index.CodeIndexMissing)
}

// TestCorruptIndexDegradesNotFails —— `.index/eg.db` 是真实非法字节：照常出结果 + W24 + Q5。
func TestCorruptIndexDegradesNotFails(t *testing.T) {
	root := bkHealthyVault(t)
	bkCorruptIndex(t, root)
	bkAssertDegraded(t, root, index.CodeIndexCorrupt)
}

// TestStaleIndexDegradesNotFails —— 权威卡被外部改动：照常出结果 + W22 + Q5，
// 且结果反映**改后**的权威内容（降级读的是 Markdown，不是旧索引）。
func TestStaleIndexDegradesNotFails(t *testing.T) {
	root := bkHealthyVault(t)
	bkMakeStale(t, root)
	bkAssertDegraded(t, root, index.CodeIndexStale)
	view, err := bkShow(root, model.CardID("k-20261201-rnn"))
	if err != nil {
		t.Fatalf("ShowCard：%v", err)
	}
	found := false
	for _, name := range view.Card.Sections.Keys() {
		if contains(view.Card.Sections.Get(name), "门控") {
			found = true
		}
	}
	if !found {
		t.Fatalf("降级读必须反映改后的权威正文（应含「门控」）")
	}
}

// TestUnindexedCardDegradesToScan —— 索引建好之后**新增**一张卡：探测的快路径看不出来
// （旧文件的 size / mtime 都没变），按需解析时认出「权威有、索引 files 表没有」，
// 于是整体退回扫描 + W22 + Q5，并把新卡**如实纳入结果**（绝不漏）。
func TestUnindexedCardDegradesToScan(t *testing.T) {
	root := bkHealthyVault(t)
	bkWrite(t, root, "domains/ai-infra/knowledge/k-20261210-flash.md",
		bkCard("k-20261210-flash", "闪电注意力", "active", "2026-12-10T10:00:00+08:00", "  - 核心\n", ""))
	bkAssertDegraded(t, root, index.CodeIndexStale)
	res, err := bkSearch(root, SearchRequest{Query: "闪电注意力"})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	seen := false
	for _, h := range res.Hits {
		if h.ID == "k-20261210-flash" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("新增卡必须出现在结果里（降级读以权威为准），实际命中 %d 条", res.Total)
	}
}

// —— ⑤ Q5 的产出与不产出 ——

// TestQ5EmittedOnDegrade —— 降级必产 Q5，且**恰一条**、与原因码同现、绝不触发 Q3。
func TestQ5EmittedOnDegrade(t *testing.T) {
	root := bkHealthyVault(t)
	bkDropIndex(t, root)
	res, err := bkSearch(root, SearchRequest{Query: "调度"})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	if bkCount(res.Diagnostics, CodeQ5) != 1 {
		t.Fatalf("降级应恰一条 Q5，实际诊断 %v", bkCodes(res.Diagnostics))
	}
	if bkCount(res.Diagnostics, index.CodeIndexMissing) != 1 {
		t.Fatalf("Q5 必须与恰一条原因码同现，实际诊断 %v", bkCodes(res.Diagnostics))
	}
	// 语料里的 Q1（broken.md）会带出 Q3，但那与 Q5 无关：这里只反证 Q5 不**额外**造 Q3。
	// 用无 Q1 的干净语料再验一次「Q5 绝不触发 Q3」。
	clean := t.TempDir()
	bkWrite(t, clean, "domains/ai-infra/knowledge/k-20261201-solo.md",
		bkCard("k-20261201-solo", "独立卡", "active", "2026-12-01T10:00:00+08:00", "", ""))
	res2, err := bkSearch(clean, SearchRequest{Query: "独立"})
	if err != nil {
		t.Fatalf("Search（干净语料）：%v", err)
	}
	codes := bkCodes(res2.Diagnostics)
	if bkCount(res2.Diagnostics, CodeQ5) != 1 || bkCount(res2.Diagnostics, CodeQ3) != 0 {
		t.Fatalf("Q5 绝不触发 Q3：干净语料的降级诊断应只有 W23 + Q5，实际 %v", codes)
	}
}

// TestQ5NotEmittedOnHealthyIndex —— 健康索引下三条读路径**一条 Q5 都没有**；
// 「查询不可由索引表达」同样不产 Q5（它不是降级）。
func TestQ5NotEmittedOnHealthyIndex(t *testing.T) {
	root := bkHealthyVault(t)
	sr, cv, rv := bkReadAll(t, root)
	for _, c := range []struct {
		name  string
		diags []Diagnostic
	}{{"search", sr.Diagnostics}, {"card show", cv.Diagnostics}, {"rel", rv.Diagnostics}} {
		if n := bkCount(c.diags, CodeQ5); n != 0 {
			t.Fatalf("%s 在健康索引下不得产 Q5，实际 %d 条（诊断 %v）", c.name, n, bkCodes(c.diags))
		}
	}
	if got := degradeDiagnostics(SelectBackend(root, notesNeed("unreviewed", bkDeps))); got != nil {
		t.Fatalf("不可表达≠降级：不得产任何降级诊断，实际 %+v", got)
	}
}

// contains 是本文件的小工具（避免为一次子串判定引 strings 到断言里读起来更绕）。
func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (hay == needle || indexOfSub(hay, needle) >= 0)
}

func indexOfSub(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestQ5CodeLivesInDiagnosticFile —— Q5 的**归位纪律**：它是 Q 系列的第五个成员，
// 因此常量必须与 Q1–Q4 同处 diagnostic.go，backend.go / degrade.go / index_backed.go
// 只允许调用、不允许自持一套码（A-39 的机器反证在 S4 上的等价延续）。
//
// 本用例只**新增**约束，不改 diagnostic_placement_test.go 的既有四码断言。
func TestQ5CodeLivesInDiagnosticFile(t *testing.T) {
	diag := readSourceFile(t, "diagnostic.go")
	if !strings.Contains(diag, "CodeQ5 =") {
		t.Fatalf("CodeQ5 必须定义在 internal/query/diagnostic.go")
	}
	if !strings.Contains(diag, "恰五条") {
		t.Fatalf("diagnostic.go 应载明「S4 起恰五条」的口径（A-45 选项 ①）")
	}
	for _, name := range []string{"backend.go", "degrade.go", "index_backed.go"} {
		if src := readSourceFile(t, name); strings.Contains(src, "CodeQ5 =") {
			t.Fatalf("%s 不得自持 Q5 常量定义，只能调用 diagnostic.go 的 CodeQ5", name)
		}
	}
	if CodeQ5 != "Q5" {
		t.Fatalf("CodeQ5 取值必须为 \"Q5\"，实际 %q", CodeQ5)
	}
	// Q1–Q4 取值一字不变（S4 只多一个成员，不改既有语义）。
	if CodeQ1 != "Q1" || CodeQ2 != "Q2" || CodeQ3 != "Q3" || CodeQ4 != "Q4" {
		t.Fatalf("S4 不得改动 Q1–Q4 的取值")
	}
}

func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", name, err)
	}
	return string(raw)
}

// —— ⑥ 陈旧判定的口径（A-44）：mtime 只作快路径，content_hash 才是结论 ——

// bkTouch 只改文件的 mtime（**字节一字不动**，size 也不变）。
//
// 这是真实世界里极常见的一幕：`git checkout` / 编辑器保存 / 备份工具回写 / rsync
// 都会碰 mtime 而不改内容。若把它判成陈旧，用户会在一个完全同步的库上永久看到 W22 + Q5，
// 「降级留痕」就贬值成噪音。
func bkTouch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	later := time.Now().Add(3 * time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatalf("改 mtime 失败：%v", err)
	}
}

// TestMTimeOnlyTouchStaysFresh —— A-44 逐字：`mtime` **不得**作为「已变更」的最终结论。
//
// 只碰 mtime（内容与 HEAD 都没变）⇒ 快路径未命中 ⇒ 必须回权威重算 `content_hash`
// ⇒ 结论仍是 fresh ⇒ 走索引后端、零 W22、零 Q5，且结果与触碰前逐字相同。
func TestMTimeOnlyTouchStaysFresh(t *testing.T) {
	root := bkHealthyVault(t)
	before, err := bkSearch(root, SearchRequest{Query: "注意力 循环 算子 调度"})
	if err != nil {
		t.Fatalf("Search（触碰前）：%v", err)
	}

	bkTouch(t, root, "domains/ai-infra/knowledge/k-20261201-rnn.md")

	b := SelectBackend(root, cardNeed("k-20261201-attention", bkDeps))
	if !b.UseIndex() || b.Code != "" || b.Degraded() {
		t.Fatalf("只改 mtime 不算变更（A-44）：应仍走索引且无码，实际 kind=%s code=%q reason=%q\n%s",
			b.Kind, b.Code, b.Reason, b.Message)
	}
	if b.Freshness != index.FreshnessFresh {
		t.Fatalf("只改 mtime 后新鲜度应仍为 fresh，实际 %q", b.Freshness)
	}

	after, err := bkSearch(root, SearchRequest{Query: "注意力 循环 算子 调度"})
	if err != nil {
		t.Fatalf("Search（触碰后）：%v", err)
	}
	if bkCount(after.Diagnostics, CodeQ5) != 0 ||
		bkCount(after.Diagnostics, index.CodeIndexStale) != 0 {
		t.Fatalf("只改 mtime 不得产 W22 / Q5，实际诊断 %v", bkCodes(after.Diagnostics))
	}
	if !reflect.DeepEqual(before.Hits, after.Hits) || before.Total != after.Total {
		t.Fatalf("只改 mtime 不得改变结果：前 %d 条，后 %d 条", before.Total, after.Total)
	}
}

// TestContentChangeStaleEvenWhenMTimeKept —— 反向判据：**内容变了就必须判陈旧**，
// 哪怕改完把 mtime 原样还原（备份 / 同步工具的常见行为）。
//
// 这里 size 随内容一起变，故快路径未命中 ⇒ 回权威重算 hash ⇒ 水位线不一致 ⇒ W22 + Q5。
// 两条用例合起来钉住 A-44 的完整语义：mtime 变化不代表变了，mtime 不变也不代表没变。
func TestContentChangeStaleEvenWhenMTimeKept(t *testing.T) {
	root := bkHealthyVault(t)
	rel := "domains/ai-infra/knowledge/k-20261201-rnn.md"
	p := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat：%v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读卡：%v", err)
	}
	if err := os.WriteFile(p, append(raw, []byte("\n补一段正文：门控。\n")...), 0o644); err != nil {
		t.Fatalf("改卡：%v", err)
	}
	// mtime 还原成改动前的值：判定不能依赖它。
	if err := os.Chtimes(p, st.ModTime(), st.ModTime()); err != nil {
		t.Fatalf("还原 mtime：%v", err)
	}

	b := SelectBackend(root, cardNeed("k-20261201-attention", bkDeps))
	if b.UseIndex() || b.Code != index.CodeIndexStale || b.Reason != ReasonIndexStale {
		t.Fatalf("内容变了必须判陈旧并降级，实际 kind=%s code=%q reason=%q\n%s",
			b.Kind, b.Code, b.Reason, b.Message)
	}
	bkAssertDegraded(t, root, index.CodeIndexStale)
}

// TestFreshnessDecidedByIndexCheck —— 判定**单点**：读路径的三态结论与 `index.Check`
// （`eg index status` 默认快路径用的同一个函数）在同一现态下逐格一致。
//
// 这条用例的价值不在「两个 bool 相等」，而在于它会在**任何人**把读路径的新鲜度判定
// 复制成第二套实现时立刻失败 —— 那正是 A-44 与 I-…-002 都在防的那类缺陷。
func TestFreshnessDecidedByIndexCheck(t *testing.T) {
	for _, c := range []struct {
		name  string
		mutil func(*testing.T, string)
	}{
		{"fresh", func(*testing.T, string) {}},
		{"mtime-only", func(t *testing.T, r string) {
			bkTouch(t, r, "domains/ai-infra/knowledge/k-20261201-rnn.md")
		}},
		{"content-changed", bkMakeStale},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := bkHealthyVault(t)
			c.mutil(t, root)

			p := probeIndex(root, bkDeps)
			if p.unverifiable {
				t.Fatalf("已注入口径不应判为「证不出新鲜度」：%s", p.unverifiableWhy)
			}
			files, err := currentWatermarkInput(root, bkDeps, p.indexed, p.present)
			if err != nil {
				t.Fatalf("组装现态水位线：%v", err)
			}
			want := index.Check(index.DirPath(root),
				index.Current{Head: bkDeps.Head(), Files: files})
			if p.freshness() != want.Freshness {
				t.Fatalf("读路径三态 %q 与 index.Check 的 %q 不一致（判定必须同一处）",
					p.freshness(), want.Freshness)
			}
			b := SelectBackend(root, cardNeed("k-20261201-attention", bkDeps))
			if b.UseIndex() != want.UseIndex() {
				t.Fatalf("后端选择与 index.Check 的可信性判断不一致：useIndex=%v want=%v",
					b.UseIndex(), want.UseIndex())
			}
		})
	}
}

// TestFreshnessUnverifiableFallsBackToScan —— **未注入** A-44 口径时（如 M2 冻结签名的
// `ShowCard`）：证不出新鲜度 ⇒ 走扫描后端，但这**不是降级** ⇒ 无 W22/W23/W24、无 Q5，
// 且结果与注入后走索引的那次逐字相同（Markdown 恒为唯一权威来源）。
func TestFreshnessUnverifiableFallsBackToScan(t *testing.T) {
	root := bkHealthyVault(t)

	b := SelectBackend(root, cardNeed("k-20261201-attention", IndexDeps{}))
	if b.Kind != BackendScan || b.Reason != ReasonFreshnessUnverifiable {
		t.Fatalf("未注入口径应走扫描且原因为 %s，实际 kind=%s reason=%s",
			ReasonFreshnessUnverifiable, b.Kind, b.Reason)
	}
	if b.Code != "" || b.Degraded() || b.Freshness != freshnessUnknown {
		t.Fatalf("证不出新鲜度≠降级：不得带码、不得判降级、新鲜度应未判定，"+
			"实际 code=%q degraded=%v freshness=%q", b.Code, b.Degraded(), b.Freshness)
	}

	plain, err := ShowCard(root, model.CardID("k-20261201-attention"))
	if err != nil {
		t.Fatalf("ShowCard（M2 签名）：%v", err)
	}
	if plain.backend.Kind != BackendScan {
		t.Fatalf("M2 签名的 ShowCard 应走扫描后端，实际 %s", plain.backend.Kind)
	}
	if bkCount(plain.Diagnostics, CodeQ5) != 0 {
		t.Fatalf("未注入口径不得产 Q5，实际诊断 %v", bkCodes(plain.Diagnostics))
	}
	injected, err := bkShow(root, model.CardID("k-20261201-attention"))
	if err != nil {
		t.Fatalf("ShowCardWith（注入口径）：%v", err)
	}
	if injected.backend.Kind != BackendIndex {
		t.Fatalf("注入口径后应走索引后端，实际 %s（%s）",
			injected.backend.Kind, injected.backend.Message)
	}
	if !reflect.DeepEqual(plain.Card, injected.Card) ||
		!reflect.DeepEqual(plain.Diagnostics, injected.Diagnostics) {
		t.Fatalf("两条后端的单卡视图必须逐字相同：\n扫描 %+v\n索引 %+v",
			plain.Card, injected.Card)
	}
}

// —— ⑦ 输出契约不扩张（S4 只加诊断，不加键） ——

// TestRelDataKeysStillFive —— `eg rel` 的 data 键**仍恰五项**、次序一字不变。
//
// S4 的降级事实只经诊断（W22|W23|W24 + Q5）承载，绝不在 data 里加 `index` / `backend`
// 这类新键（合同 §6.4）。这条用例同时守住键**集合**与键**次序**：JSON 键序是 M2 冻结的
// 输出契约，agent 侧的解析与人类侧的 diff 都依赖它。
func TestRelDataKeysStillFive(t *testing.T) {
	want := []string{"id", "relations_out", "relations_in", "scanned_files", "skipped_files"}
	if !reflect.DeepEqual(RelDataKeys(), want) {
		t.Fatalf("rel 的 data 键必须恰五项且次序不变：实际 %v，期望 %v", RelDataKeys(), want)
	}

	// 两条后端上都不许扩张：健康索引与删掉索引各跑一次，键集合逐字相同。
	root := bkHealthyVault(t)
	idx, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-attention")})
	if err != nil {
		t.Fatalf("RelView（索引后端）：%v", err)
	}
	if idx.backend.Kind != BackendIndex {
		t.Fatalf("前置不成立：应走索引后端，实际 %s", idx.backend.Kind)
	}
	bkDropIndex(t, root)
	scan, err := bkRel(root, RelRequest{ID: model.CardID("k-20261201-attention")})
	if err != nil {
		t.Fatalf("RelView（扫描后端）：%v", err)
	}
	if scan.backend.Kind != BackendScan {
		t.Fatalf("前置不成立：应降级为扫描后端，实际 %s", scan.backend.Kind)
	}
	if !reflect.DeepEqual(idx.Data, scan.Data) {
		t.Fatalf("两条后端的 rel data 必须逐字相同：\n索引 %+v\n扫描 %+v", idx.Data, scan.Data)
	}
}

// TestCardShowDataKeysNotExpanded —— `eg card show` 的 data 键集合与次序**一字未动**。
//
// 判据是与 M3 冻结键表的**逐字**比对（不是「包含」也不是「至少」）：S4 一个键都没加。
func TestCardShowDataKeysNotExpanded(t *testing.T) {
	want := []string{"id", "title", "domain", "status", "deprecated", "created_at",
		"updated_at", "path", "tags", "markers", "sections", "sources",
		"relations_out", "relations_in", FieldDeleted, FieldUnreviewed}
	if !reflect.DeepEqual(CardDataKeys(), want) {
		t.Fatalf("card show 的 data 键集合 / 次序被改动了：实际 %v，期望 %v", CardDataKeys(), want)
	}
	for _, k := range CardDataKeys() {
		if k == "index" || k == "backend" || k == "degraded" {
			t.Fatalf("S4 不得把索引 / 后端事实塞进 data（合同 §6.4），实际键含 %q", k)
		}
	}
	// 两条后端上的单卡视图逐字相同（键集合之外，值也不许因后端而漂移）。
	root := bkHealthyVault(t)
	idx, err := bkShow(root, model.CardID("k-20261201-attention"))
	if err != nil {
		t.Fatalf("ShowCardWith（索引后端）：%v", err)
	}
	bkDropIndex(t, root)
	scan, err := bkShow(root, model.CardID("k-20261201-attention"))
	if err != nil {
		t.Fatalf("ShowCardWith（扫描后端）：%v", err)
	}
	if idx.backend.Kind != BackendIndex || scan.backend.Kind != BackendScan {
		t.Fatalf("前置不成立：索引轮 %s / 扫描轮 %s", idx.backend.Kind, scan.backend.Kind)
	}
	if !reflect.DeepEqual(idx.Card, scan.Card) {
		t.Fatalf("两条后端的单卡视图必须逐字相同：\n索引 %+v\n扫描 %+v", idx.Card, scan.Card)
	}
}
