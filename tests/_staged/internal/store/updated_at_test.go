package store

// internal/store 的 `updated_at` 刷新口径单测（批次 C2 · I-…-009）。
//
// 判据（声明面，逐字）：
//   - `2026-10-10-m3-user-authorization-contract.md` §2.1 写权限矩阵**第 8 行**：
//     `updated_at | ✅ 由 CLI 在实际写入时更新 | ✅ 同左 | …两条路径无差别`；
//   - 同合同 §5.5 EG-CFM-06：`reviewed_at` 只由 `eg mark-reviewed` 写，「过目」不是对内容的
//     修改 → `SetReviewedAt` **不刷新** `updated_at`（否则刚过目就又变未过目）；
//   - 对账合同 §9：R6 失准标记「不自动重算 / 不自动清除」→ `SetStale` 同样不刷新。
//
// 本文件锁的是 store 层的**机制**四条（命令级闭环由
// tests/e2e/lifecycle/c2_updated_at_refresh.sh 用真实二进制锁）：
//  1. 给了时刻的状态写 / 正文替换 / 关系移除 → `updated_at` 整行覆盖成该时刻；
//  2. 覆盖是**整行**：键恒恰一行、frontmatter 不长出新键、其余行逐字不动；
//  3. **豁免是结构性的**：SetReviewedAt / SetStale 的签名里没有可传时刻的参数，
//     调用后 `updated_at` 逐字不变（本文件用「行内容不变」反证）；
//  4. **缺键不新增**：frontmatter 本来没有 `updated_at` 的产物（如原文样例）不会被
//     落盘层发明出这个键；被 B3 跳过的写（零写入）也不刷新。

import (
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// refreshStampLiteral 是本文件注入的写入时刻：远晚于所有样例的 `updated_at`，
// 因此「刷新过」与「没刷新」在字节上不可能撞值。
const refreshStampLiteral = "2027-04-20T09:30:00+08:00"

func refreshStamp(t *testing.T) model.Stamp {
	t.Helper()
	at, err := model.ParseStamp(refreshStampLiteral)
	if err != nil {
		t.Fatalf("解析注入时刻 %q：%v", refreshStampLiteral, err)
	}
	return at
}

// wantUpdatedAtLine 是刷新后应当出现的**整行**（引号风格与建卡时逐字一致：单引号标量）。
func wantUpdatedAtLine() string {
	return "updated_at: '" + refreshStampLiteral + "'\n"
}

// assertRefreshed 断言刷新已发生且是整行覆盖：恰一行、值为注入时刻、键集合不变。
func assertRefreshed(t *testing.T, before, after []byte, changedKeys ...string) {
	t.Helper()
	fm := fmOf(t, after)
	if !strings.Contains(fm, wantUpdatedAtLine()) {
		t.Fatalf("updated_at 必须被刷新成 %s：\n%s", refreshStampLiteral, fm)
	}
	if n := strings.Count(fm, "updated_at:"); n != 1 {
		t.Fatalf("updated_at 必须恰一行（整行覆盖，不长出重复键）：实得 %d\n%s", n, fm)
	}
	b := strings.Split(fmOf(t, before), "\n")
	a := strings.Split(fm, "\n")
	if len(a) != len(b) {
		t.Fatalf("frontmatter 行数必须不变（刷新是整行覆盖，不新增键）：%d → %d", len(b), len(a))
	}
	allowed := append([]string{"updated_at:"}, changedKeys...)
	for i := range b {
		if b[i] == a[i] {
			continue
		}
		ok := false
		for _, key := range allowed {
			if strings.HasPrefix(b[i], key) {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("第 %d 行不该变：%q → %q", i+1, b[i], a[i])
		}
	}
	// 正文（frontmatter 之后）逐字不变：刷新只碰 frontmatter 的一行。
	if got, want := bodyOf(t, after), bodyOf(t, before); got != want {
		t.Fatalf("正文必须逐字不变：\n得到 %q\n期望 %q", got, want)
	}
}

// TestSetStatusRefreshesUpdatedAt：状态流转是一次实际写入 → 刷新 `updated_at`（矩阵第 8 行）。
func TestSetStatusRefreshesUpdatedAt(t *testing.T) {
	s, _, abs := seedStateCard(t)
	rel := "cards/k-20260901-state.md"
	before := mustBytes(t, abs)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	res, err := s.SetStatus(rel, f.Hash, model.StatusDeprecated, refreshStamp(t))
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if !res.Written {
		t.Fatal("hash 相符时必须落盘")
	}
	after := mustBytes(t, abs)
	if !strings.Contains(fmOf(t, after), "status: 'deprecated'\n") {
		t.Fatalf("status 未被覆盖：%s", fmOf(t, after))
	}
	// 恰两行会变：status 与 updated_at；别的键（含未知字段）逐字不动。
	assertRefreshed(t, before, after, "status:")
	if strings.Contains(fmOf(t, after), "reviewed_at") {
		t.Fatal("状态写绝不写 reviewed_at（第三个正交维度）")
	}
}

// TestSetReplacedByRefreshesUpdatedAt：写替代指针同样是实际写入 → 刷新。
func TestSetReplacedByRefreshesUpdatedAt(t *testing.T) {
	s, root, abs := seedStateCard(t)
	targetRel := "cards/k-20260902-target.md"
	targetAbs := writeSeed(t, root, targetRel,
		strings.Replace(stateCardSample, "k-20260901-state", "k-20260902-target", 1))
	targetBefore := mustBytes(t, targetAbs)
	rel := "cards/k-20260901-state.md"
	before := mustBytes(t, abs)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetReplacedBy(rel, f.Hash, model.RelationEndpoint("k-20260902-target"),
		"口径已更新，见新卡", refreshStamp(t)); err != nil {
		t.Fatalf("SetReplacedBy: %v", err)
	}
	after := mustBytes(t, abs)
	fm := fmOf(t, after)
	if !strings.Contains(fm, wantUpdatedAtLine()) {
		t.Fatalf("updated_at 必须被刷新：%s", fm)
	}
	if n := strings.Count(fm, "updated_at:"); n != 1 {
		t.Fatalf("updated_at 必须恰一行：实得 %d\n%s", n, fm)
	}
	// replaced_by 是新键（样例没有它）→ 行数 +1；除它与 updated_at 外一行都不许变。
	if got, want := bodyOf(t, after), bodyOf(t, before); got != want {
		t.Fatal("正文必须逐字不变")
	}
	// 单向存储：被指向的目标卡一个字节都不动（含它自己的 updated_at）。
	if string(mustBytes(t, targetAbs)) != string(targetBefore) {
		t.Fatal("replaced_by 是单向的：目标卡不得被打开改写，其 updated_at 更不许被刷新")
	}
}

// TestSetReviewedAtNeverRefreshesUpdatedAt：过目**不是**内容修改 → 逐字不刷新
// （§5.5 EG-CFM-06；这条同时是「修复不许过度」的护栏）。
func TestSetReviewedAtNeverRefreshesUpdatedAt(t *testing.T) {
	s, _, abs := seedStateCard(t)
	rel := "cards/k-20260901-state.md"
	before := fmOf(t, mustBytes(t, abs))
	if !strings.Contains(before, "updated_at:") {
		t.Fatal("前置不成立：样例缺 updated_at，本用例失去判据")
	}
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetReviewedAt(rel, f.Hash, refreshStamp(t)); err != nil {
		t.Fatalf("SetReviewedAt: %v", err)
	}
	fm := fmOf(t, mustBytes(t, abs))
	if strings.Contains(fm, wantUpdatedAtLine()) {
		t.Fatalf("mark-reviewed 绝不刷新 updated_at（否则刚过目就又变未过目）：%s", fm)
	}
	for _, line := range strings.Split(before, "\n") {
		if strings.HasPrefix(line, "updated_at:") && !strings.Contains(fm, line) {
			t.Fatalf("updated_at 行必须逐字不变：期望仍是 %q\n%s", line, fm)
		}
	}
	if !strings.Contains(fm, model.FMKeyReviewedAt+":") {
		t.Fatalf("reviewed_at 必须落盘：%s", fm)
	}
}

// TestSetStaleNeverRefreshesUpdatedAt：R6 失准标记不改内容 → 不刷新（对账合同 §9）。
func TestSetStaleNeverRefreshesUpdatedAt(t *testing.T) {
	s, _, abs := seedStateCard(t)
	rel := "cards/k-20260901-state.md"
	beforeFM := fmOf(t, mustBytes(t, abs))
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetStale(rel, f.Hash, model.ValidStaleReasons()[0]); err != nil {
		t.Fatalf("SetStale: %v", err)
	}
	fm := fmOf(t, mustBytes(t, abs))
	if strings.Contains(fm, wantUpdatedAtLine()) {
		t.Fatalf("失准标记不得刷新 updated_at（§9：不自动重算）：%s", fm)
	}
	for _, line := range strings.Split(beforeFM, "\n") {
		if strings.HasPrefix(line, "updated_at:") && !strings.Contains(fm, line) {
			t.Fatalf("updated_at 行必须逐字不变：期望仍是 %q\n%s", line, fm)
		}
	}
}

// TestReplaceSectionRefreshesUpdatedAt：正文被整段替换 → 必须刷新
// （否则「过目 → 改内容」的卡永远进不了 `eg unreviewed`，即 I-…-009 的现场）。
func TestReplaceSectionRefreshesUpdatedAt(t *testing.T) {
	s, _, abs := seedStateCard(t)
	rel := "cards/k-20260901-state.md"
	before := mustBytes(t, abs)
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	res, err := s.ApplyReplaceSection(ReplaceSectionSpec{
		Rel: rel, ExpectedHash: f.Hash, ID: "k-20260901-state",
		Section: "知识内容", Content: []byte("替换后的新正文。\n"), Stamp: refreshStamp(t),
	})
	if err != nil {
		t.Fatalf("ApplyReplaceSection: %v", err)
	}
	if !res.Written {
		t.Fatal("hash 相符时必须落盘")
	}
	fm := fmOf(t, mustBytes(t, abs))
	if !strings.Contains(fm, wantUpdatedAtLine()) {
		t.Fatalf("正文替换必须刷新 updated_at：%s", fm)
	}
	if n := strings.Count(fm, "updated_at:"); n != 1 {
		t.Fatalf("updated_at 必须恰一行：实得 %d\n%s", n, fm)
	}
	// frontmatter 除 updated_at 一行外逐行不变（正文变化不在 fmOf 的范围内）。
	b := strings.Split(fmOf(t, before), "\n")
	a := strings.Split(fm, "\n")
	if len(a) != len(b) {
		t.Fatalf("frontmatter 行数必须不变：%d → %d", len(b), len(a))
	}
	for i := range b {
		if b[i] != a[i] && !strings.HasPrefix(b[i], "updated_at:") {
			t.Fatalf("第 %d 行不该变：%q → %q", i+1, b[i], a[i])
		}
	}
}

// TestZeroDeltaWriteDoesNotRefreshUpdatedAt：走完写路径但**候选字节与盘上逐字相同**
// （典型现场：replace_block 把当前有效块原样写回、remove_relation 幂等 no-op）→ 不刷新。
//
// 判据方向与 I-…-009 相反的那一半：矩阵第 8 行说的是「**实际写入**时更新」，内容一字未变
// 就没有「实际写入」。若这里也刷新，卡片会在内容零变化的情况下重新跌进 `eg unreviewed`
// —— 把 I-…-009 的假阴性换成假阳性，复核闭环照样不可信。
// 这里直接钉住判据函数本身（唯一入口 refreshIfChanged），正反两侧同时钉：
// 零 delta 必须原样返回；有 delta 必须刷新。集成侧的反证见
// tests/e2e/ops-diagnostics/ops_diagnostics.sh（A-15 的 replace_block 原样写回后全库字节不变）。
func TestZeroDeltaWriteDoesNotRefreshUpdatedAt(t *testing.T) {
	raw := []byte(stateCardSample)
	got, err := refreshIfChanged(raw, raw, refreshStamp(t))
	if err != nil {
		t.Fatalf("零 delta 不该报错：%v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("零 delta 写入必须逐字返回原字节（含 updated_at）：\n%s", got)
	}
	if strings.Contains(fmOf(t, got), wantUpdatedAtLine()) {
		t.Fatal("内容一字未变却刷新了 updated_at：会让该卡假阳性地重回未过目清单")
	}
	// 反面：只要候选字节确有变化，同一个入口必须刷新（否则就退化回 I-…-009 的现场）。
	changed := append(append([]byte{}, raw...), []byte("\n补一行正文。\n")...)
	got2, err := refreshIfChanged(raw, changed, refreshStamp(t))
	if err != nil {
		t.Fatalf("有 delta 刷新不该报错：%v", err)
	}
	if !strings.Contains(fmOf(t, got2), wantUpdatedAtLine()) {
		t.Fatalf("有 delta 必须刷新 updated_at：%s", fmOf(t, got2))
	}
	if n := strings.Count(fmOf(t, got2), "updated_at:"); n != 1 {
		t.Fatalf("刷新后必须恰一行 updated_at：实得 %d", n)
	}
}

// TestSkippedWriteDoesNotRefreshUpdatedAt：B3 跳过 = 零写入 → **连时间戳也不许动**
// （「刷新只跟随实际写入」的负半边）。
func TestSkippedWriteDoesNotRefreshUpdatedAt(t *testing.T) {
	s, _, abs := seedStateCard(t)
	rel := "cards/k-20260901-state.md"
	before := mustBytes(t, abs)
	res, err := s.SetStatus(rel,
		"sha256:2222222222222222222222222222222222222222222222222222222222222222",
		model.StatusDeprecated, refreshStamp(t))
	if _, ok := AsSkip(err); !ok {
		t.Fatalf("hash 不符必须跳过（B3），实得 %v", err)
	}
	if res.Written {
		t.Fatal("跳过时不得落盘")
	}
	if string(mustBytes(t, abs)) != string(before) {
		t.Fatal("跳过时文件字节必须逐字不变（含 updated_at）")
	}
}

// TestRefreshNeverInventsKey：frontmatter 本来没有 `updated_at` 的产物不会被落盘层
// 发明出这个键（规则「有则整行覆盖，无则不新增」）。
func TestRefreshNeverInventsKey(t *testing.T) {
	s, root := newVault(t)
	rel := "sources/s-20260901-del.md"
	abs := writeSeed(t, root, rel, delSourceSample)
	if strings.Contains(fmOf(t, mustBytes(t, abs)), "updated_at") {
		t.Fatal("前置不成立：原文样例本不该有 updated_at，本用例失去判据")
	}
	f, err := s.Read(rel)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := s.SetDeleted(rel, f.Hash, mustStamp(t), "用户要求删除", refreshStamp(t)); err != nil {
		t.Fatalf("SetDeleted: %v", err)
	}
	fm := fmOf(t, mustBytes(t, abs))
	if strings.Contains(fm, "updated_at") {
		t.Fatalf("缺键时绝不新增 updated_at（落盘层不发明键）：%s", fm)
	}
	if !strings.Contains(fm, "deleted_at") || !strings.Contains(fm, "deleted_reason") {
		t.Fatalf("删除维度两键必须照常落盘：%s", fm)
	}
}
