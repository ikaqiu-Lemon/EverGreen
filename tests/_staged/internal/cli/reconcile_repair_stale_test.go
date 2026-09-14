package cli

// R6 修复桥的机器判据（M4 · T-evergreen.s1_main_flow-158614-055 阶段 3）。
//
// 三组用例（名字是 deliverables 逐字要求，供 M-004 完成判据 8 复算）：
//   ① TestR6StaleKeysRecapOnly          —— 写入键集合**恰** `{stale, stale_reason}` 的 diff 级
//      反证，外加**综述专属**的两路反证：整库只有那一篇综述带这两键（知识卡 / 材料笔记 /
//      材料 / 提案上出现即用例失败），且把非综述对象塞进修复意向时**整批拒绝、零写入**；
//   ② TestR6IdempotentZeroWrite         —— 幂等：已是 `stale: true` 且理由相同 → 零 op、
//      零写入、零 commit、字节全等，而 `recap_stale` finding **仍产出**；
//   ③ TestR6B3HashStaleSkipsWithoutWrite —— B3 不豁免：对账观测到的 hash 已过期 →
//      进 `skipped[kind=file_changed]`、目标字节零变化、零 commit，finding 仍在（带注记）。
//
// 另有两组自守用例（不在 deliverables 名单里，但钉住本阶段的硬约束）：
//   ④ TestR6BridgeWriteFaceDiscipline    —— 源码级：本桥零 `internal/store` import、
//      零 `SetStatus` / `deleted_at` / `replaced_by` / `reviewed_at` 字样、经 `internal/plan`
//      落盘、A-34 下不带 `initiator`、本 task 零命令注册；
//   ⑤ TestR6RepairJoinsSingleReconcileCommit —— A-35：R1 纳管 + R6 修复合并为**恰一次** commit。
//
// 全组事实一律回读**真实文件字节**与 `git log`，不看实现自报。

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/git"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/plan"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// 本组用例的固定时刻与固定 ID（全程确定性，绝不取本机当前时间）。
const (
	r6RepairAt = "2026-11-30T10:00:00+08:00"
	// r6RecapAt 是综述的 `updated_at`：与卡的初始 `updated_at` **逐字相等** ——
	// 于是造数完成时 R6 恒不命中（合同 §9 的判据是「晚于」，相等不算），
	// 「事后更新引用卡」这一步才是唯一的命中来源。
	r6RecapAt = "2026-09-01T10:00:00+08:00"
	// r6CardBumpAt 是「事后更新引用卡」后的卡 `updated_at`（严格晚于综述）。
	r6CardBumpAt = "2026-09-02T09:00:00+08:00"
	r6RecapID    = "r-20260901-attention"
	r6Domain     = "ai-infra"
)

// r6StaleHash 是注入的**过期观测 hash**（B3 前置比对必然不通过）。
const r6StaleHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// r6RecapRel 是综述的 vault 相对路径（`reviews/` 分区，S2 的既有位置）。
func r6RecapRel() string { return "domains/" + r6Domain + "/reviews/" + r6RecapID + ".md" }

// r6RecapFixture 是一篇主题综述的落盘字节（S2 的既有形态；`stale` 两键此刻零占用）。
const r6RecapFixture = `---
id: ` + r6RecapID + `
title: 注意力机制主题综述
created_at: '2026-09-01'
updated_at: '` + r6RecapAt + `'
source_cards:
  - ` + applyCardID + `
---

## 综述

注意力是一种加权求和；本段正文一个字节都不许动。

## 依据

- 取材自知识卡 ` + applyCardID + `
`

// r6Root 构造带固定时钟的 Root。
func r6Root(t *testing.T, dir string) *Root {
	t.Helper()
	setGitIdentity(t)
	r := newTestRoot(t, dir)
	r.Now = func() time.Time { return stampAt(t, r6RepairAt) }
	r.In = closedStdin{}
	return r
}

// r6Vault 造一个「一篇原文 + 一篇笔记 + 一张卡 + 一篇引用该卡的综述」的干净库。
//
// 综述由 fixture 直接落盘并提交（综述生成属 S2/S3 的生成面，本 task 不实现生成器，
// 也不新增任何命令）；提交后工作区干净，因此后续断言里的 commit 增量只可能来自对账。
func r6Vault(t *testing.T) (dir, recapRel, cardRel, noteRel string) {
	t.Helper()
	dir = applyVault(t)
	noteRel, cardRel = applyNoteAndCard(t, dir)
	recapRel = r6RecapRel()
	if err := os.MkdirAll(filepath.Dir(absIn(dir, recapRel)), 0o755); err != nil {
		t.Fatalf("建 reviews/ 目录失败：%v", err)
	}
	if err := os.WriteFile(absIn(dir, recapRel), []byte(r6RecapFixture), 0o644); err != nil {
		t.Fatalf("落综述 fixture 失败：%v", err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "test(fixture): 落一篇主题综述")
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置条件：造数后工作区必须干净，得到 %q", got)
	}
	raw := string(mustRead(t, absIn(dir, recapRel)))
	for _, key := range reconcile.RecapStaleKeys() {
		if fmKeyLineOf(t, raw, key) != "" {
			t.Fatalf("前置不成立：新落的综述不该带 %s（两键由本 task 首次写入）", key)
		}
	}
	// 造数完成时 R6 **恒不命中**：卡与综述的 updated_at 逐字相等。
	if targets, _ := r6Check(t, dir); len(targets) != 0 {
		t.Fatalf("前置不成立：更新引用卡之前 R6 不该命中，实得 %+v", targets)
	}
	return dir, recapRel, cardRel, noteRel
}

// r6BumpCard 模拟**事后更新引用卡**（用户在编辑器里改内容并顺手更新 `updated_at`）：
// 只替换 `updated_at` 那一行的值，然后提交（工作区保持干净）。
func r6BumpCard(t *testing.T, dir, cardRel, at string) {
	t.Helper()
	path := absIn(dir, cardRel)
	raw := string(mustRead(t, path))
	old := fmKeyLineOf(t, raw, "updated_at")
	if old == "" {
		t.Fatalf("%s 没有 updated_at 行，无法模拟事后更新", cardRel)
	}
	next := strings.Replace(raw, old, "updated_at: '"+at+"'", 1)
	if next == raw {
		t.Fatal("updated_at 行替换失败")
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		t.Fatalf("更新引用卡失败：%v", err)
	}
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "test(fixture): 事后更新引用卡")
}

// r6Check 采一次真实的只读事实（全库扫描 + 综述分区采样），跑一次只读检查。
func r6Check(t *testing.T, dir string) ([]reconcile.RecapTarget, reconcile.Result) {
	t.Helper()
	scan, err := query.VaultScan(dir, query.ScanOptions{IncludeNotes: true})
	if err != nil {
		t.Fatalf("扫描 vault 失败：%v", err)
	}
	sample, serr := SampleRecaps(dir)
	if serr != nil {
		t.Fatalf("采样综述失败：%v", serr)
	}
	if sample.Recaps == nil {
		t.Fatal("采样结果为 nil：nil 语义是「未采样即不判」，采样成功时必须是非 nil 切片")
	}
	in := reconcile.R6ScanOf(dir, scan, sample.Recaps)
	return reconcile.RecapTargets(in), reconcile.Run(in)
}

// r6RepairInput 组装一次 R6 修复的入参 —— **逐字没有** `UserRequest` 那一格（A-34）。
func r6RepairInput(dir string, targets []reconcile.RecapTarget,
	res reconcile.Result) StaleRepairInput {
	return StaleRepairInput{
		VaultRoot: dir, Targets: targets, Repairs: res.Repairs, Findings: res.Findings,
		Reason:         "对账 R6：为引用卡已变化的主题综述写失准标记两键",
		RequirementIDs: []string{"EG-EDIT-05"},
	}
}

// r6StaleFindings 数一批 findings 里的 `recap_stale` 条数。
func r6StaleFindings(fs []reconcile.Finding) []reconcile.Finding {
	var out []reconcile.Finding
	for _, f := range fs {
		if f.Check == reconcile.CheckRecapStale {
			out = append(out, f)
		}
	}
	return out
}

// r6FilesBearingStaleKeys 全库回读：返回 frontmatter 里出现失准标记两键之一的文件
// （相对路径升序）。综述专属的**唯一**判据就是它 —— 不看实现自报，只看落盘字节。
func r6FilesBearingStaleKeys(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		raw := string(mustRead(t, path))
		if !strings.HasPrefix(strings.TrimSpace(raw), "---") {
			return nil
		}
		for _, key := range reconcile.RecapStaleKeys() {
			if fmKeyLineOf(t, raw, key) == "" {
				continue
			}
			rel, rerr := filepath.Rel(dir, path)
			if rerr != nil {
				return rerr
			}
			out = append(out, filepath.ToSlash(rel))
			break
		}
		return nil
	})
	if err != nil {
		t.Fatalf("全库回读失败：%v", err)
	}
	sort.Strings(out)
	return out
}

// —— ① 写入键集合恰两键 + 两键为综述专属 ——

func TestR6StaleKeysRecapOnly(t *testing.T) {
	t.Run("命中综述：新增与整行变化的顶层键恰两键，正文与其余键逐字不变", func(t *testing.T) {
		dir, recapRel, cardRel, noteRel := r6Vault(t)
		r6BumpCard(t, dir, cardRel, r6CardBumpAt)

		before := string(mustRead(t, absIn(dir, recapRel)))
		cardBefore := string(mustRead(t, absIn(dir, cardRel)))
		noteBefore := string(mustRead(t, absIn(dir, noteRel)))
		beforeCommits := gitLogCount(t, dir)

		targets, res := r6Check(t, dir)
		if len(targets) != 1 || targets[0].Path != recapRel || targets[0].ID != r6RecapID {
			t.Fatalf("R6 应恰命中那一篇综述，实得 %+v", targets)
		}
		if targets[0].Reason != model.StaleReasonUpdated {
			t.Fatalf("理由 = %q，本行造数应命中 %q", targets[0].Reason, model.StaleReasonUpdated)
		}
		out, err := r6Root(t, dir).RepairRecapStale(r6RepairInput(dir, targets, res))
		if err != nil {
			t.Fatalf("R6 修复失败：%v（%+v）", err, out)
		}
		if out.Ops != 1 || len(out.Skipped) != 0 || len(out.Failures) != 0 {
			t.Fatalf("应恰 1 个 op、零跳过零失败：%+v", out)
		}
		if !reflect.DeepEqual(out.Written, []string{recapRel}) {
			t.Fatalf("写入路径 = %v，期望恰 [%s]", out.Written, recapRel)
		}
		if !reflect.DeepEqual(out.Staled, []string{r6RecapID}) {
			t.Fatalf("落盘成功的综述 = %v，期望恰 [%s]", out.Staled, r6RecapID)
		}

		// 1) 待写键集合的真源只有一处，且恒恰两个键。
		want := reconcile.RecapStaleKeys()
		if !reflect.DeepEqual(out.Keys, want) || len(want) != reconcile.RecapStaleKeyCount ||
			reconcile.RecapStaleKeyCount != 2 {
			t.Fatalf("待写键真源 = %v / 回执 = %v，两者应同源且恰 2 个键", want, out.Keys)
		}
		if !reflect.DeepEqual(want, []string{model.FMKeyStale, model.FMKeyStaleReason}) {
			t.Fatalf("两键 = %v，期望恰 {%s, %s}", want, model.FMKeyStale, model.FMKeyStaleReason)
		}

		after := string(mustRead(t, absIn(dir, recapRel)))
		// 2) 新增的顶层键**恰那两个**。
		got := addedFMKeys(t, before, after)
		sort.Strings(got)
		wantSorted := append([]string{}, want...)
		sort.Strings(wantSorted)
		if !reflect.DeepEqual(got, wantSorted) {
			t.Fatalf("新增顶层键 = %v，期望恰 %v", got, wantSorted)
		}
		// 3) 整行发生变化的键**也恰那两个**（其余键的整行逐字不变）。
		if changed := changedFMLines(t, before, after); !reflect.DeepEqual(changed, wantSorted) {
			t.Fatalf("整行变化的键 = %v，期望恰 %v（三维不牵连）", changed, wantSorted)
		}
		// 4) 两键的取值：`stale` 是 YAML 布尔真，理由落在封闭三值内且恰一行。
		if line := fmKeyLineOf(t, after, model.FMKeyStale); line != model.FMKeyStale+": true" {
			t.Fatalf("%s 行 = %q，期望 %q", model.FMKeyStale, line, model.FMKeyStale+": true")
		}
		reasonLine := fmKeyLineOf(t, after, model.FMKeyStaleReason)
		var hitReason bool
		for _, r := range model.ValidStaleReasons() {
			if strings.Contains(reasonLine, string(r)) {
				hitReason = true
			}
		}
		if !hitReason {
			t.Fatalf("%s 行 = %q，取值必须落在封闭三值 %v 内",
				model.FMKeyStaleReason, reasonLine, model.ValidStaleReasons())
		}
		if !strings.Contains(reasonLine, string(model.StaleReasonUpdated)) {
			t.Fatalf("%s 行 = %q，本行造数应写 %q",
				model.FMKeyStaleReason, reasonLine, model.StaleReasonUpdated)
		}
		for _, key := range want {
			if n := strings.Count(after, "\n"+key+":"); n != 1 {
				t.Fatalf("%s 出现 %d 行，期望恰 1 行（覆盖而不是累积）", key, n)
			}
		}
		// 5) 正文逐字节不变 —— **未重算综述**（合同 §9 第一条「不自动」）。
		if fmBody(t, after) != fmBody(t, before) {
			t.Fatal("综述正文发生变化：R6 只写两个 frontmatter 键，绝不重算综述")
		}
		// 6) 状态维度 / 删除维度 / 替代指针维度 / 过目维度一格不写。
		for _, key := range []string{"status", "updated_at", "created_at", "title"} {
			if fmKeyLineOf(t, after, key) != fmKeyLineOf(t, before, key) {
				t.Fatalf("%s 行发生变化：失准维度与它正交", key)
			}
		}
		for _, key := range fmKeys(t, after) {
			if strings.HasPrefix(key, "deleted") || key == "replaced_by" ||
				key == model.FMKeyReviewedAt {
				t.Fatalf("修复长出了 %s：删除 / 替代指针 / 过目维度一格不写", key)
			}
		}
		// 7) **综述专属**：整库带这两键的文件恰那一篇综述（卡 / 笔记 / 材料 / 提案零命中）。
		if bearers := r6FilesBearingStaleKeys(t, dir); !reflect.DeepEqual(bearers,
			[]string{recapRel}) {
			t.Fatalf("带失准两键的文件 = %v，期望恰 [%s]：两键是综述专属", bearers, recapRel)
		}
		if got := string(mustRead(t, absIn(dir, cardRel))); got != cardBefore {
			t.Fatalf("%s 的字节发生变化：R6 只写命中综述", cardRel)
		}
		if got := string(mustRead(t, absIn(dir, noteRel))); got != noteBefore {
			t.Fatalf("%s 的字节发生变化：R6 只写命中综述", noteRel)
		}
		// 8) 本桥自己零提交（提交归纳管写口的恰一次 commit）。
		if out.Commits != 0 {
			t.Fatalf("修复桥的提交条数 = %d，恒应为 0（A-35）", out.Commits)
		}
		if got := gitLogCount(t, dir); got != beforeCommits {
			t.Fatalf("commit 数 %d → %d：修复桥不得自己提交", beforeCommits, got)
		}
	})

	// 非综述对象一律进不了这个写入面：知识卡 / 材料笔记 / 材料（原文）/ 提案四类
	// 逐个反证「整批拒绝、零写入、零 commit」，且落盘上一个字节不变。
	t.Run("非综述对象：整批拒绝、零写入", func(t *testing.T) {
		cases := []struct {
			name string
			id   string
		}{
			{"知识卡", applyCardID},
			{"材料笔记", applyNoteID},
			{"材料（原文）", applySourceID},
			{"提案", "p-20260901-demo"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dir, recapRel, cardRel, _ := r6Vault(t)
				r6BumpCard(t, dir, cardRel, r6CardBumpAt)
				targets, res := r6Check(t, dir)
				if len(targets) != 1 {
					t.Fatalf("前置不成立：应恰命中 1 篇综述，实得 %d", len(targets))
				}
				before := string(mustRead(t, absIn(dir, recapRel)))
				beforeCommits := gitLogCount(t, dir)

				// 把命中对象换成非综述 ID（意向侧同步造一条同源的 spec）。
				bad := r6RepairInput(dir, targets, res)
				tg := targets[0]
				tg.ID = c.id
				tg.Path = "domains/" + r6Domain + "/knowledge/" + c.id + ".md"
				bad.Targets = []reconcile.RecapTarget{tg}
				spec, serr := reconcile.NewRepairSpec(reconcile.CheckRecapStale, tg.Path,
					reconcile.RecapStaleKeys(), "对账 R6：造一条非综述意向")
				if serr != nil {
					t.Fatalf("造意向失败：%v", serr)
				}
				bad.Repairs = []reconcile.RepairSpec{spec}
				out, err := r6Root(t, dir).RepairRecapStale(bad)
				if err == nil {
					t.Fatalf("%s 被接受了：两键是综述专属，必须整批拒绝（%+v）", c.name, out)
				}
				if out.Ops != 0 || len(out.Written) != 0 || out.Commits != 0 {
					t.Fatalf("拒绝时必须零 op、零写入、零提交：%+v", out)
				}
				if got := string(mustRead(t, absIn(dir, recapRel))); got != before {
					t.Fatal("拒绝时综述字节发生了变化")
				}
				if got := gitLogCount(t, dir); got != beforeCommits {
					t.Fatalf("拒绝时产生了 commit：%d → %d", beforeCommits, got)
				}
				if bearers := r6FilesBearingStaleKeys(t, dir); len(bearers) != 0 {
					t.Fatalf("拒绝后仍有文件带失准两键：%v", bearers)
				}
			})
		}
	})

	// 待写键不封闭的意向：**整批拒绝、零写入**（宁可一篇不标，也不写第三个键）。
	t.Run("键集合不封闭：整批拒绝、零写入", func(t *testing.T) {
		cases := []struct {
			name string
			keys []string
		}{
			{"多出第三个键", append(reconcile.RecapStaleKeys(), "status")},
			{"少一个键", reconcile.RecapStaleKeys()[:1]},
			{"顺序错位", []string{model.FMKeyStaleReason, model.FMKeyStale}},
			{"混进过目维度", []string{model.FMKeyStale, model.FMKeyReviewedAt}},
			{"空键集合", []string{}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				dir, recapRel, cardRel, _ := r6Vault(t)
				r6BumpCard(t, dir, cardRel, r6CardBumpAt)
				targets, res := r6Check(t, dir)
				before := string(mustRead(t, absIn(dir, recapRel)))
				beforeCommits := gitLogCount(t, dir)

				bad := r6RepairInput(dir, targets, res)
				bad.Repairs = append([]reconcile.RepairSpec{}, res.Repairs...)
				for i := range bad.Repairs {
					if bad.Repairs[i].Check == reconcile.CheckRecapStale {
						bad.Repairs[i].Keys = c.keys
					}
				}
				out, err := r6Root(t, dir).RepairRecapStale(bad)
				if err == nil {
					t.Fatalf("待写键 %v 被接受了（%+v）", c.keys, out)
				}
				if out.Ops != 0 || len(out.Written) != 0 || out.Commits != 0 {
					t.Fatalf("拒绝时必须零 op、零写入、零提交：%+v", out)
				}
				if got := string(mustRead(t, absIn(dir, recapRel))); got != before {
					t.Fatal("拒绝时综述字节发生了变化")
				}
				if got := gitLogCount(t, dir); got != beforeCommits {
					t.Fatalf("拒绝时产生了 commit：%d → %d", beforeCommits, got)
				}
			})
		}
	})
}

// —— ② 幂等 ——

func TestR6IdempotentZeroWrite(t *testing.T) {
	dir, recapRel, cardRel, _ := r6Vault(t)
	r6BumpCard(t, dir, cardRel, r6CardBumpAt)

	targets, res := r6Check(t, dir)
	if len(targets) != 1 {
		t.Fatalf("前置不成立：应恰命中 1 篇综述，实得 %d", len(targets))
	}
	first, err := r6Root(t, dir).RepairRecapStale(r6RepairInput(dir, targets, res))
	if err != nil {
		t.Fatalf("首轮修复失败：%v", err)
	}
	if first.Ops != 1 || len(first.Written) != 1 {
		t.Fatalf("首轮应恰 1 个 op / 1 处写入：%+v", first)
	}
	afterFirst := string(mustRead(t, absIn(dir, recapRel)))
	// 首轮写入后提交，使工作区干净：第二轮的「零新增 commit」因此只可能来自幂等本身。
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-m", "test(fixture): 落首轮失准标记")
	commits := gitLogCount(t, dir)

	// 第二轮：触发条件仍成立（卡确实比综述新）→ finding **仍产出**，
	// 但落盘上已是同一标记与同一理由 → 零 op、零写入、零 commit。
	targets2, res2 := r6Check(t, dir)
	if len(targets2) != 1 || !targets2[0].AlreadyMarked {
		t.Fatalf("第二轮应仍命中且 AlreadyMarked=true，实得 %+v", targets2)
	}
	if got := r6StaleFindings(res2.Findings); len(got) != 1 {
		t.Fatalf("幂等时 recap_stale finding 条数 = %d，期望恰 1（finding 仍产出）", len(got))
	}
	out, err := r6Root(t, dir).RepairRecapStale(r6RepairInput(dir, targets2, res2))
	if err != nil {
		t.Fatalf("幂等轮不该报错：%v", err)
	}
	if !out.Ran || out.Ops != 0 || len(out.Written) != 0 || len(out.Staled) != 0 ||
		out.Commits != 0 {
		t.Fatalf("幂等必须零 op、零写入、零提交（ran 仍为 true）：%+v", out)
	}
	if !reflect.DeepEqual(out.Idempotent, []string{r6RecapID}) {
		t.Fatalf("幂等清单 = %v，期望恰 [%s]", out.Idempotent, r6RecapID)
	}
	if got := r6StaleFindings(out.Findings); len(got) != 1 {
		t.Fatalf("幂等时回执里的 recap_stale finding 条数 = %d，期望恰 1", len(got))
	}
	if got := out.Reasons[r6RecapID]; got != string(model.StaleReasonUpdated) {
		t.Fatalf("幂等回执的理由 = %q，期望 %q", got, model.StaleReasonUpdated)
	}
	// 落盘字节全等 + 零新增 commit + 工作区仍干净（连空 commit 都不产生）。
	if got := string(mustRead(t, absIn(dir, recapRel))); got != afterFirst {
		t.Fatal("幂等轮写盘了（应零写入）")
	}
	if got := gitLogCount(t, dir); got != commits {
		t.Fatalf("幂等轮产生了 commit：%d → %d", commits, got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("幂等轮后工作区应干净：%q", got)
	}
	// 幂等轮也不清除既有标记（合同 §9 第二条「不自动」）。
	for _, key := range reconcile.RecapStaleKeys() {
		if fmKeyLineOf(t, string(mustRead(t, absIn(dir, recapRel))), key) == "" {
			t.Fatalf("幂等轮把 %s 清掉了：R6 不自动清除失准标记", key)
		}
	}
}

// —— ③ B3 不豁免 ——

func TestR6B3HashStaleSkipsWithoutWrite(t *testing.T) {
	cases := []struct {
		name string
		// keyOf 决定过期 hash 挂在 base 的哪个键上（id 与 rel 两种覆盖形态都要成立）。
		keyOf func(t reconcile.RecapTarget) string
	}{
		{"base 按对象 ID 覆盖", func(t reconcile.RecapTarget) string { return t.ID }},
		{"base 按相对路径覆盖", func(t reconcile.RecapTarget) string { return t.Path }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, recapRel, cardRel, _ := r6Vault(t)
			r6BumpCard(t, dir, cardRel, r6CardBumpAt)
			targets, res := r6Check(t, dir)
			if len(targets) != 1 {
				t.Fatalf("前置不成立：应恰命中 1 篇综述，实得 %d", len(targets))
			}
			before := string(mustRead(t, absIn(dir, recapRel)))
			beforeCommits := gitLogCount(t, dir)

			in := r6RepairInput(dir, targets, res)
			in.Base = map[string]string{c.keyOf(targets[0]): r6StaleHash}
			out, err := r6Root(t, dir).RepairRecapStale(in)
			if err != nil {
				t.Fatalf("B3 跳过不是错误路径：%v（%+v）", err, out)
			}
			// 1) 恰一条 skipped，kind 是既有封闭两值里的 file_changed（不新增第三种 kind）。
			if len(out.Skipped) != 1 {
				t.Fatalf("应恰 1 条 skipped：%+v", out.Skipped)
			}
			if got := out.Skipped[0].Kind; got != string(store.SkipFileChanged) {
				t.Fatalf("skipped[].kind = %q，期望 %q（不自造第三种 kind）",
					got, store.SkipFileChanged)
			}
			// 2) 零写入、零提交、字节全等 —— B3 不豁免。
			if len(out.Written) != 0 || len(out.Staled) != 0 || out.Commits != 0 {
				t.Fatalf("B3 跳过必须零写入零提交：%+v", out)
			}
			if got := string(mustRead(t, absIn(dir, recapRel))); got != before {
				t.Fatal("B3 跳过却写盘了")
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("B3 跳过必须零 commit：%d → %d", beforeCommits, got)
			}
			if bearers := r6FilesBearingStaleKeys(t, dir); len(bearers) != 0 {
				t.Fatalf("B3 跳过后仍有文件带失准两键：%v", bearers)
			}
			// 3) finding **仍在**，条数与分级不变，且 detail 注明已跳过。
			if len(out.Findings) != len(res.Findings) {
				t.Fatalf("finding 条数 %d → %d：跳过不得删条目",
					len(res.Findings), len(out.Findings))
			}
			var noted int
			for i, f := range out.Findings {
				if f.Check != reconcile.CheckRecapStale {
					continue
				}
				if f.Severity != res.Findings[i].Severity {
					t.Fatal("跳过不得改变 finding 分级")
				}
				if !reflect.DeepEqual(f.Targets, res.Findings[i].Targets) {
					t.Fatal("跳过不得改变 finding 的 targets")
				}
				if strings.Contains(f.Detail, reconcile.ReviewedSkipNotice) {
					noted++
				}
			}
			if noted != 1 {
				t.Fatalf("应恰 1 条 finding 带「已跳过」注记，实得 %d", noted)
			}
		})
	}
}

// —— ④ 写口纪律（源码级自守） ——

func TestR6BridgeWriteFaceDiscipline(t *testing.T) {
	const bridge = "reconcile_repair_stale.go"
	raw, err := os.ReadFile(bridge)
	if err != nil {
		t.Fatalf("读本桥源码失败：%v", err)
	}
	src := string(raw)
	// 1) 零 `internal/store` 命中（task 的 verify.run 逐字 grep 同一判据）。
	if n := strings.Count(src, "internal/store"); n != 0 {
		t.Fatalf("%s 里 internal/store 命中 %d 次，必须恒 0（写口归属：只经 internal/plan）",
			bridge, n)
	}
	// 2) 四个越界字样零命中（状态 / 删除 / 替代指针 / 过目维度一格不写）。
	for _, word := range []string{"SetStatus", "deleted_at", "replaced_by", "reviewed_at"} {
		if strings.Contains(src, word) {
			t.Fatalf("%s 里出现 %q：失准维度与其余维度正交", bridge, word)
		}
	}
	// 3) 确实经计划层落盘（写入唯一通路）。
	if !strings.Contains(src, "internal/plan") {
		t.Fatalf("%s 未经 internal/plan：内存 ChangePlan 是唯一写入通路", bridge)
	}
	// 4) A-34：合成 plan 逐字不带 initiator（不需要 `--user-request`，仍走 ChangePlan）。
	if strings.Contains(src, "Initiator") {
		t.Fatalf("%s 里出现 Initiator：A-34 判定 R6 写两键不需要 --user-request", bridge)
	}
	if strings.Contains(src, "UserRequest: true") {
		t.Fatalf("%s 里给了命令行佐证：R6 恒走 P-A", bridge)
	}
	// 5) op 名与字段面：复用阶段 1 定稿的 `set_stale`，字段恰 target / reason 两格。
	if StaleRepairOp != plan.OpSetStale || plan.OpSetStale != "set_stale" {
		t.Fatalf("R6 用的 op = %q，A-33 裁决为新增 set_stale", StaleRepairOp)
	}
	if got := plan.M4OpFields(); !reflect.DeepEqual(got,
		[]string{"target", "reason"}) {
		t.Fatalf("set_stale 的字段表 = %v，期望恰 [target reason]", got)
	}
	// 6) 本 task 零命令注册（`eg reconcile` 属 T-…-058，`eg check` 属 T-…-059）。
	// 注册形态的字面量在此**拼接**而成：既有门禁按 `Name:[[:space:]]*"reconcile"` 全库 grep
	// 计数恒 0，本用例自己不得成为那条 grep 的命中项。
	for _, cmd := range []string{"reconcile", "check"} {
		if strings.Contains(src, "Name:"+" \""+cmd+"\"") {
			t.Fatalf("%s 注册了 %s 子命令：本 task 一律零命令注册", bridge, cmd)
		}
	}
	// 7) 失准理由的封闭三值真源只有一处，且恒三个。
	if got := reconcile.RecapStaleReasons(); len(got) != reconcile.RecapStaleReasonCount ||
		len(got) != len(model.ValidStaleReasons()) || len(got) != 3 {
		t.Fatalf("封闭三值 = %v，必须恒 3 个且与 model 同源", got)
	}
}

// —— ⑤ 恰一次 commit（R1 纳管 + R6 修复合并） ——

func TestR6RepairJoinsSingleReconcileCommit(t *testing.T) {
	dir, recapRel, cardRel, _ := r6Vault(t)
	r6BumpCard(t, dir, cardRel, r6CardBumpAt)
	// 再造一处**未提交**的外部编辑：它构成 R1 的纳管面，与 R6 的写入同进一条 commit。
	r2EditFile(t, dir, cardRel)
	beforeCommits := gitLogCount(t, dir)

	targets, res := r6Check(t, dir)
	if len(targets) != 1 {
		t.Fatalf("前置不成立：应恰命中 1 篇综述，实得 %d", len(targets))
	}
	out, err := r6Root(t, dir).RepairRecapStale(r6RepairInput(dir, targets, res))
	if err != nil {
		t.Fatalf("R6 修复失败：%v", err)
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("修复阶段不得提交：%d → %d", beforeCommits, got)
	}

	tk := NewReconcileTakeover(git.New(dir))
	tres, terr := tk.Takeover(ReconcileTakeoverInput{
		Domain: r6Domain, Findings: len(res.Findings), Repairs: len(res.Repairs),
		OurWrites: out.Written, ChecksDone: true, RepairsDone: true,
		Reason: "对账：R1 纳管 + R6 失准标记（恰一次 commit）", RequirementIDs: []string{"EG-EDIT-05"},
	})
	if terr != nil {
		t.Fatalf("纳管失败：%v", terr)
	}
	if tk.Commits() != 1 || tres.Commit == nil || *tres.Commit == "" {
		t.Fatalf("整轮对账应恰产生 1 条 commit：%+v", tres)
	}
	if got := gitLogCount(t, dir); got != beforeCommits+1 {
		t.Fatalf("commit 数 %d → %d，应恰 +1（R1 + R6 合并，不是 +2）", beforeCommits, got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("对账后工作区应干净：%q", got)
	}
	files := gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD")
	for _, rel := range []string{recapRel, cardRel} {
		if !strings.Contains(files, rel) {
			t.Fatalf("本次 commit 未含 %s：\n%s", rel, files)
		}
	}
	if diff := gitOut(t, dir, "show", "HEAD"); !strings.Contains(diff, model.FMKeyStale) ||
		!strings.Contains(diff, model.FMKeyStaleReason) {
		t.Fatal("本次 commit 的 diff 里没有那两个写入的键")
	}
}
