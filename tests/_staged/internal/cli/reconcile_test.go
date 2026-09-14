// `eg reconcile` 命令级用例（M4 · T-evergreen.s1_main_flow-158614-058 阶段 2）。
//
// 本文件只测**命令本体**：信封形态、`data.reconcile` 三键、退出码次序、dry-run 零写入、
// 非法参数零副作用、以及 `eg report --last` 的逐字复现。检查包（R1–R7）与两条修复桥
// 各自的用例在 `internal/reconcile/**` 与 `reconcile_repair_*_test.go`，此处一条不重复。
//
// 三条纪律（写在最前面，便于复核）：
//   - **mock 逐处标注**：全文件恰一处 mock（退出码 4 的 Git Runner 注入），
//     标注格式 `// mock（owner 2026-09-07 授权）：…`，写清它模拟的真实事实与不可稳定复现的原因。
//   - **B3 跳过不是 mock**：退 3 用「检查阶段观测到的 content_hash 与写前重算不一致」这条
//     真实 B3 语义造出来（在检查与修复之间真实改写文件），走的是产品代码的原路。
//   - `Root.Now` 注入固定时刻**不算 mock**：它是仓内既有先例（见 reviewed_test.go 的 stampAt），
//     目的只是让时间戳可复算，不替换任何被测行为。
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/txn"
)

// reconcileFixedNow 是本组用例统一使用的固定时刻（RFC3339）。
// 注入 Root.Now 属仓内既有先例（reviewed_test.go / apply_test.go 都这么做），**不是 mock**：
// 它只把「本机当前时间」这一不可复算的输入钉成常量，被测判定逻辑一格不改。
const reconcileFixedNow = "2026-12-01T10:00:00+08:00"

// runReconcileCLI 在 dir 上跑一次 `eg reconcile …`（恒带 --json 信封）。
// stdin 恒为已关闭状态：本命令没有任何确认点（退出码 6 不在它的码集里），
// 任何读 stdin 的行为都会当场红。
func runReconcileCLI(t *testing.T, r *Root, dir string, extra ...string) (int, string, string) {
	t.Helper()
	setGitIdentity(t)
	r.In = closedStdin{}
	if r.Now == nil {
		r.Now = func() time.Time { return stampAt(t, reconcileFixedNow) }
	}
	args := append([]string{"reconcile", "--vault", dir, "--json"}, extra...)
	return runCLI(t, r, args...)
}

// rcPorcelainCount 数 `git status --porcelain` 的非空行数（工作区脏度的机器可判形态）。
func rcPorcelainCount(t *testing.T, dir string) int {
	t.Helper()
	out := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain"))
	if out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

// rcRawAt 沿 JSON 对象路径取**原始字节**（保留键序，供键序断言与逐字比对使用）。
func rcRawAt(t *testing.T, raw []byte, path ...string) []byte {
	t.Helper()
	cur := raw
	for _, key := range path {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(cur, &obj); err != nil {
			t.Fatalf("路径 %v 上 %q 之前的节点不是 JSON 对象：%v\n%s", path, key, err, cur)
		}
		next, ok := obj[key]
		if !ok {
			t.Fatalf("路径 %v 缺键 %q：%s", path, key, cur)
		}
		cur = next
	}
	return cur
}

// rcOrderedKeys 按**出现次序**返回一个 JSON 对象的键（键序也是合同的一部分）。
func rcOrderedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if _, err := dec.Token(); err != nil { // 吃掉 '{'
		t.Fatalf("不是 JSON 对象：%v\n%s", err, raw)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("读键失败：%v", err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("读值失败：%v", err)
		}
	}
	return keys
}

// rcFinding 是 `data.reconcile.findings[]` 的四键投影（键名逐字取合同 §11）。
type rcFinding struct {
	Check    string   `json:"check"`
	Severity string   `json:"severity"`
	Targets  []string `json:"targets"`
	Detail   string   `json:"detail"`
}

// rcReconcileKeys 是 `data.reconcile` 的**恰三键**（合同 §11 / §12，键序即声明序）。
var rcReconcileKeys = []string{"ran", "commit", "findings"}

// rcErrorChecks 是合同 §12 里「有它即退 2」的 error 级检查名（四条，一条不多一条不少）。
var rcErrorChecks = map[string]bool{
	"duplicate_id":            true,
	"dangling_ref":            true,
	"relation_target_missing": true,
	"relation_prefix_invalid": true,
}

// rcAssertReconcileShape 断言一个 `reconcile` 对象恰三键、键序即合同序、findings 恒非 null。
// 返回解析出的 findings（供各用例继续断言 severity / check）。
func rcAssertReconcileShape(t *testing.T, raw []byte) []rcFinding {
	t.Helper()
	if got := rcOrderedKeys(t, raw); strings.Join(got, ",") != strings.Join(rcReconcileKeys, ",") {
		t.Fatalf("data.reconcile 键 = %v，合同规定恰三键且键序为 %v：%s", got, rcReconcileKeys, raw)
	}
	findingsRaw := rcRawAt(t, raw, "findings")
	if strings.TrimSpace(string(findingsRaw)) == "null" {
		t.Fatalf("findings 恒非 null（零条时也必须是 []）：%s", raw)
	}
	var fs []rcFinding
	if err := json.Unmarshal(findingsRaw, &fs); err != nil {
		t.Fatalf("findings 不是数组：%v\n%s", err, findingsRaw)
	}
	return fs
}

// rcDupVault 建一个**真实**含重复 id 的库：把既有卡逐字节复制成第二个文件。
// 这不是 mock —— 重复 id 是磁盘上的真事实，R4 会如实判 duplicate_id（error 级）；
// 同时这份未跟踪文件让工作区变脏，从而 R1 纳管必然产生恰一次提交。
func rcDupVault(t *testing.T) (dir, cardRel, dupRel string) {
	t.Helper()
	dir, cardRel, _ = reviewedVault(t)
	dupRel = "domains/ai-infra/knowledge/dup-copy.md"
	if err := os.WriteFile(absIn(dir, dupRel), mustRead(t, absIn(dir, cardRel)), 0o644); err != nil {
		t.Fatalf("写重复 id 卡失败：%v", err)
	}
	return dir, cardRel, dupRel
}

// —— ① 信封恰五键 + data.reconcile 恰三键 + findings 恒非 null ——

func TestReconcileEnvelopeFiveKeys(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		extra []string
		want  int
	}{
		{
			name:  "clean_vault_exit0",
			setup: func(t *testing.T) string { dir, _, _ := reviewedVault(t); return dir },
			want:  ExitOK,
		},
		{
			name:  "dry_run_exit0",
			setup: func(t *testing.T) string { dir, _, _ := reviewedVault(t); return dir },
			extra: []string{"--dry-run"},
			want:  ExitOK,
		},
		{
			name:  "duplicate_id_exit2",
			setup: func(t *testing.T) string { dir, _, _ := rcDupVault(t); return dir },
			want:  ExitValidation,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.setup(t)
			code, out, errOut := runReconcileCLI(t, newTestRoot(t, dir), dir, tc.extra...)
			if code != tc.want {
				t.Fatalf("退出码 = %d，期望 %d（%s / %s）", code, tc.want, out, errOut)
			}
			// 信封形态：恰五键（键集合比对，真源是 EnvelopeKeys()）。
			var env map[string]interface{}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatalf("--json 输出不可解析：%v（%q）", err, out)
			}
			assertEnvelopeKeys(t, env)
			// 键序也锁：ok → data → warnings → exit_code → status。
			if got := rcOrderedKeys(t, []byte(out)); strings.Join(got, ",") !=
				strings.Join(EnvelopeKeys(), ",") {
				t.Fatalf("信封键序 = %v，期望逐字 %v", got, EnvelopeKeys())
			}
			// data.reconcile 恰三键；data 里 reconcile 必须排在 report 之前（DataOrder）。
			if got := rcOrderedKeys(t, rcRawAt(t, []byte(out), "data")); len(got) == 0 ||
				got[0] != "reconcile" {
				t.Fatalf("data 首键 = %v，期望 reconcile 在最前（res.DataOrder）", got)
			}
			rcAssertReconcileShape(t, rcRawAt(t, []byte(out), "data", "reconcile"))
			// 报告体里的同名三键与 data 侧同源，逐字节相等（不许各算一遍）。
			a := rcRawAt(t, []byte(out), "data", "reconcile")
			b := rcRawAt(t, []byte(out), "data", "report", "reconcile")
			if string(a) != string(b) {
				t.Fatalf("data.reconcile 与 data.report.reconcile 必须同源逐字相等\n%s\n%s", a, b)
			}
		})
	}
}

// —— ② 只有 warning 级 finding → 退 0（合同 §12 的显式口径，绝不是 2）——

func TestReconcileExitZeroOnWarningOnly(t *testing.T) {
	dir, _, _ := reviewedVault(t)
	beforeCommits := gitLogCount(t, dir)
	code, out, errOut := runReconcileCLI(t, newTestRoot(t, dir), dir)
	if code != ExitOK {
		t.Fatalf("只有 warning 级 finding 时退出码 = %d，合同 §12 规定退 0（%s / %s）", code, out, errOut)
	}
	fs := rcAssertReconcileShape(t, rcRawAt(t, []byte(out), "data", "reconcile"))
	if len(fs) == 0 {
		t.Fatal("前置不成立：本用例需要至少一条 warning 级 finding（否则退 0 是空判）")
	}
	var warnings int
	for _, f := range fs {
		switch f.Severity {
		case "error":
			t.Fatalf("语料里出现 error 级 finding %q，本用例的前置（只有 warning）不成立", f.Check)
		case "warning":
			warnings++
		}
	}
	if warnings == 0 {
		t.Fatalf("findings 里没有 warning 级条目：%v", fs)
	}
	// 工作区本来就干净 → R1 零改动即零提交（退 0 不代表必须有提交）。
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("干净库对账后 commit 数 = %d，期望不变（%d）", got, beforeCommits)
	}
	if raw := rcRawAt(t, []byte(out), "data", "reconcile", "commit"); strings.TrimSpace(string(raw)) != "null" {
		t.Fatalf("零改动时 reconcile.commit 必须为 null，实得 %s", raw)
	}
}

// —— ③ 有 error 级 finding：先完成 R1 纳管 commit（恰 +1）再退 2 ——

func TestReconcileExitTwoAfterR1Commit(t *testing.T) {
	dir, _, dupRel := rcDupVault(t)
	before := gitLogCount(t, dir)
	code, out, errOut := runReconcileCLI(t, newTestRoot(t, dir), dir)

	// 三格缺一不可：退出码 == 2 且 commit 数恰 +1 且 data.reconcile.commit 非 null。
	if code != ExitValidation {
		t.Fatalf("有 error 级 finding 时退出码 = %d，期望 2（%s / %s）", code, out, errOut)
	}
	if got := gitLogCount(t, dir); got != before+1 {
		t.Fatalf("commit 数 %d → %d，期望恰 +1（合同 §12：先完成 R1 纳管 commit 再退 2）", before, got)
	}
	shaRaw := rcRawAt(t, []byte(out), "data", "reconcile", "commit")
	if strings.TrimSpace(string(shaRaw)) == "null" {
		t.Fatal("data.reconcile.commit 为 null：退 2 之前的纳管提交没被如实登记")
	}
	var sha string
	if err := json.Unmarshal(shaRaw, &sha); err != nil || len(sha) < 7 {
		t.Fatalf("reconcile.commit 不是一个 sha：%s（%v）", shaRaw, err)
	}
	if !strings.Contains(gitOut(t, dir, "log", "-1", "--format=%H"), sha) {
		t.Fatalf("reconcile.commit=%s 不是 HEAD：登记的提交必须是真实存在的那一条", sha)
	}
	// error 级 finding 必须落在合同 §12 的四条结构性检查里（严重级的真源恒是检查包）。
	fs := rcAssertReconcileShape(t, rcRawAt(t, []byte(out), "data", "reconcile"))
	var hit []string
	for _, f := range fs {
		if f.Severity == "error" {
			if !rcErrorChecks[f.Check] {
				t.Fatalf("出现不在合同 §12 四条里的 error 级检查名 %q", f.Check)
			}
			hit = append(hit, f.Check)
		}
	}
	if len(hit) == 0 {
		t.Fatalf("语料应产出 duplicate_id（error 级），实得 findings=%v", fs)
	}
	// 纳管的对象就是那份让工作区变脏的重复文件：提交后它不再出现在 porcelain 里。
	if strings.Contains(gitOut(t, dir, "status", "--porcelain"), dupRel) {
		t.Fatalf("重复文件仍未纳管进提交：%s", gitOut(t, dir, "status", "--porcelain"))
	}
}

// —— ④ 有写入被 B3 拦下 → 退 3，且 data 里能看到被跳过的事实 ——

// TestReconcileExitThreeOnB3Skip 用**真实 B3 语义**造退 3，不是 mock：
// 检查阶段（第 ①②步）观测到综述文件的 content_hash 之后、修复桥写入（第 ⑤步）之前，
// 该文件在磁盘上被真实改写一次，于是写前重算的 hash 与观测值不符，计划层如实判
// `skipped[kind=file_changed, cause=content_hash_mismatch]`。这正是 B3 要防的那件事
// （「自读取以来文件已变化」），产品代码走的是原路，没有任何行为被替换。
//
// 改写时机借 Root.Now 的**第一次调用**触发：Now 只在修复阶段被取用，因此它是「采样已完成、
// 写入尚未发生」这一时刻的可复算锚点；Now 本身返回固定时刻，仍是仓内既有的时间注入先例。
func TestReconcileExitThreeOnB3Skip(t *testing.T) {
	cases := []struct {
		name string
		// alsoErrorFinding 为真时再真实造一条 error 级 finding（dangling_ref / E12），
		// 用来在**命令级**锁死「3 优先于 2」这一格 —— 不只是纯函数表里那条。
		alsoErrorFinding bool
	}{
		{name: "b3_skip_only"},
		{name: "b3_skip_beats_error_finding", alsoErrorFinding: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, recapRel, cardRel, noteRel := r6Vault(t)
			r6BumpCard(t, dir, cardRel, r6CardBumpAt) // 真实制造 recap_stale：被引用的卡更新更晚
			if tc.alsoErrorFinding {
				// 真实制造 dangling_ref（E12，error 级）：把笔记 frontmatter 的 source
				// 改指到一个 vault 内**确实不存在**的原文 id。这不是 mock —— 悬空引用是磁盘上
				// 的真事实，R4 会如实判 error；它与综述那条 recap_stale 各自独立，互不干扰。
				path := absIn(dir, noteRel)
				raw := string(mustRead(t, path))
				old := fmKeyLineOf(t, raw, "source")
				if old == "" {
					t.Fatalf("前置不成立：%s 没有 source 行", noteRel)
				}
				next := strings.Replace(raw, old, "source: s-20260101-not-in-vault", 1)
				if next == raw {
					t.Fatal("source 行替换失败")
				}
				if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
					t.Fatalf("写悬空引用失败：%v", err)
				}
			}
			before := gitLogCount(t, dir)

			r := newTestRoot(t, dir)
			// 每一次 Now 调用都真实追加一行：修复桥的固定次序是
			// 「③ 算 base → ④ executePlanNoCommit(now)」（reconcile_repair_stale.go），
			// 因此**最后一次**改写必然晚于 R6 的 base 计算、早于真正的写入 ——
			// 无论前面 R2 是否也取过一次时间，B3 的比对窗口都被稳定命中。
			var nowCalls int
			r.Now = func() time.Time {
				nowCalls++
				raw := string(mustRead(t, absIn(dir, recapRel)))
				if err := os.WriteFile(absIn(dir, recapRel),
					[]byte(raw+"\n用户在对账中途又改了一次。\n"), 0o644); err != nil {
					t.Fatalf("在「base 已算完、写入未发生」的窗口内改写综述失败：%v", err)
				}
				return stampAt(t, reconcileFixedNow)
			}
			code, out, errOut := runReconcileCLI(t, r, dir)
			if nowCalls == 0 {
				t.Fatal("前置不成立：Now 未被调用，说明改写没有落在「采样后、写入前」这一时刻")
			}
			if code != ExitPartialWrite {
				t.Fatalf("有写入被 B3 拦下时退出码 = %d，期望 3（%s / %s）", code, out, errOut)
			}
			// data 里必须看得见「被跳过」这件事：报告 skipped[] 与 data.errors[] 两处都要有。
			var skipped []struct {
				Kind    string `json:"kind"`
				Target  string `json:"target"`
				Locator string `json:"locator"`
				Cause   string `json:"cause"`
				Detail  string `json:"detail"`
			}
			if err := json.Unmarshal(rcRawAt(t, []byte(out), "data", "report", "skipped"), &skipped); err != nil {
				t.Fatalf("report.skipped 不可解析：%v", err)
			}
			if len(skipped) != 1 {
				t.Fatalf("report.skipped 应恰 1 条，实得 %d 条：%v", len(skipped), skipped)
			}
			if skipped[0].Kind != "file_changed" || skipped[0].Cause != "content_hash_mismatch" {
				t.Fatalf("跳过条目的 kind / cause 必须逐字是 file_changed / content_hash_mismatch，实得 %+v",
					skipped[0])
			}
			if skipped[0].Locator != recapRel {
				t.Fatalf("跳过条目定位到 %q，期望 %q", skipped[0].Locator, recapRel)
			}
			errsRaw := string(rcRawAt(t, []byte(out), "data", "errors"))
			if !strings.Contains(errsRaw, "file_changed") || !strings.Contains(errsRaw, "content_hash_mismatch") {
				t.Fatalf("data.errors[] 必须逐字登记 kind / cause：%s", errsRaw)
			}
			// 退 3 不阻断写入路径：R1 纳管照常把那份被改写的文件记进历史（恰一次提交）。
			if got := gitLogCount(t, dir); got != before+1 {
				t.Fatalf("commit 数 %d → %d，期望恰 +1（退 3 时已完成的纳管必须保留）", before, got)
			}
			// finding 一条不减、一级不降：recap_stale 仍在（被拦下不等于问题消失）。
			fs := rcAssertReconcileShape(t, rcRawAt(t, []byte(out), "data", "reconcile"))
			var stale, hasError bool
			for _, f := range fs {
				if f.Check == "recap_stale" {
					stale = true
				}
				if f.Severity == "error" {
					hasError = true
				}
			}
			if !stale {
				t.Fatalf("写入被拦下后 recap_stale finding 必须仍在，实得 %v", fs)
			}
			// 第二格的前置必须成立：确实同时存在 error 级 finding，退出码却仍恰 3。
			if tc.alsoErrorFinding != hasError {
				t.Fatalf("前置不成立：期望 error 级 finding 存在=%v，实得 %v（findings=%v）",
					tc.alsoErrorFinding, hasError, fs)
			}
		})
	}
}

// —— ⑤ Git 提交失败 → 退 4，磁盘保留现状、不做任何还原（B4）——

func TestReconcileExitFourOnGitFailure(t *testing.T) {
	dir, cardRel, _ := reviewedVault(t)
	r2EditFile(t, dir, cardRel) // 真实制造「用户在编辑器里直接改过」：工作区脏，R1 必须纳管
	editedHash := hashOf(t, dir, cardRel)
	beforeCommits := gitLogCount(t, dir)
	beforePorcelain := rcPorcelainCount(t, dir)
	beforeTree := treeSnapshot(t, dir)

	r := newTestRoot(t, dir)
	// mock（owner 2026-09-07 授权）：注入一个「只有 git commit 这一步失败」的 Runner
	// （failingCommitRepo → git.NewWithRunner，add / status / log 等**全部走真实 git**）。
	// 它模拟的真实事实是「纳管提交在 Git 层失败」——真实环境下的成因是磁盘满、
	// 钩子拒绝、index.lock 争用、仓库权限被回收等外部条件，无法在单测里稳定、可复算地复现，
	// 也不能靠破坏测试库来制造（那会同时污染 add / status 的真实性，B4 断言就失去意义）。
	// 除 commit 这一步的返回值外，本用例的磁盘状态与 Git 索引状态全是真的。
	r.NewRepo = failingCommitRepo()

	code, out, errOut := runReconcileCLI(t, r, dir)
	if code != ExitCommitFailed {
		t.Fatalf("Git 提交失败时退出码 = %d，期望 4（%s / %s）", code, out, errOut)
	}
	if raw := rcRawAt(t, []byte(out), "data", "reconcile", "commit"); strings.TrimSpace(string(raw)) != "null" {
		t.Fatalf("提交失败时 reconcile.commit 必须为 null，实得 %s", raw)
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("提交失败后 commit 数 = %d，期望不变（%d）", got, beforeCommits)
	}
	// B4：磁盘保留现状、不做任何还原 —— 用户那次编辑的内容逐字节还在，工作区仍然脏。
	if got := hashOf(t, dir, cardRel); got != editedHash {
		t.Fatalf("提交失败后 %s 的内容被改写（%s → %s）：B4 规定不做任何还原", cardRel, editedHash, got)
	}
	if got := rcPorcelainCount(t, dir); got != beforePorcelain {
		t.Fatalf("提交失败后 porcelain 行数 = %d，期望不变（%d）：不许悄悄清理工作区", got, beforePorcelain)
	}
	// 允许两类**合法**变化，且仅这两类：
	//   · 最近一次报告 .eg/last-report.json（工程状态目录，已进本地忽略清单、不进提交）；
	//   · 运行时锁正文 .index/run.lock —— C5 起对账进入 M6 强事务、成功取锁即重写锁正文
	//     （合同 §3：纯诊断字段，不参与互斥），git 失败不回滚它属预期 runtime 痕迹。
	// 语料里两者都可能已存在（建库时跑过 eg apply），因此「新增」「改写」两种形态都放行。
	// 除此以外任何变化（txn 日志、索引 DB、权威 Markdown 或其它路径）一律判红 ——
	// 权威 Markdown 一个字节都不许动，也**不**放宽为忽略整个 .index/。
	lockRel := txn.IndexDirName + "/" + txn.LockFileName
	allowed := map[string]bool{
		"新增 " + LastReportFile: true,
		"改写 " + LastReportFile: true,
		"新增 " + lockRel:        true,
		"改写 " + lockRel:        true,
	}
	if diff := treeDiff(t, dir, beforeTree); diff != "" {
		for _, entry := range strings.Split(diff, "；") {
			if !allowed[entry] {
				t.Fatalf("提交失败后的文件变化 = %q，只允许 %s 与 %s 的新增/改写"+
					"（禁止 txn / 索引 DB / 权威 Markdown / 其它任何路径变化）", diff, LastReportFile, lockRel)
			}
		}
	}
	// run.lock 必须是**普通文件**（既非 symlink 也非目录 / 设备）：用 Lstat 不跟随链接。
	lockAbs := filepath.Join(dir, filepath.FromSlash(lockRel))
	if info, err := os.Lstat(lockAbs); err != nil {
		t.Fatalf("run.lock 状态读取失败：%v", err)
	} else if !info.Mode().IsRegular() {
		t.Fatalf("run.lock 不是普通文件：mode=%v", info.Mode())
	}
	// 未提交清单必须如实报出（B4 的可操作性要求）。
	if !strings.Contains(string(rcRawAt(t, []byte(out), "data", "errors")), cardRel) {
		t.Fatalf("data.errors[] 未报出未提交清单里的 %s：%s", cardRel,
			rcRawAt(t, []byte(out), "data", "errors"))
	}
}

// —— ⑥ --dry-run：porcelain 行数与 log 行数均不变、ran==true、commit==null ——

func TestReconcileDryRunZeroWrite(t *testing.T) {
	// 语料刻意选「有事可做」的那种：R2 该补 reviewed_at、R6 该写失准标记、R1 该纳管。
	dir, recapRel, cardRel, _ := r6Vault(t)
	_ = recapRel
	r6BumpCard(t, dir, cardRel, r6CardBumpAt)
	r2EditFile(t, dir, cardRel)
	beforeCommits := gitLogCount(t, dir)
	beforePorcelain := rcPorcelainCount(t, dir)
	beforeStatus := gitOut(t, dir, "status", "--porcelain")
	beforeTree := treeSnapshot(t, dir)

	code, out, errOut := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")
	if code != ExitOK {
		t.Fatalf("dry-run 退出码 = %d，期望 0（%s / %s）", code, out, errOut)
	}
	if got := rcPorcelainCount(t, dir); got != beforePorcelain {
		t.Fatalf("dry-run 后 porcelain 行数 = %d，期望不变（%d）", got, beforePorcelain)
	}
	if got := gitOut(t, dir, "status", "--porcelain"); got != beforeStatus {
		t.Fatalf("dry-run 后 porcelain 内容变了：\n前：%s\n后：%s", beforeStatus, got)
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("dry-run 后 log 行数 = %d，期望不变（%d）", got, beforeCommits)
	}
	if diff := treeDiff(t, dir, beforeTree); diff != "" {
		t.Fatalf("dry-run 必须一个字节都不写（含最近一次报告），实际变化：%s", diff)
	}
	rc := rcRawAt(t, []byte(out), "data", "reconcile")
	rcAssertReconcileShape(t, rc)
	var ran bool
	if err := json.Unmarshal(rcRawAt(t, rc, "ran"), &ran); err != nil || !ran {
		t.Fatalf("dry-run 的 reconcile.ran 必须为 true（检查照常跑），实得 %s（%v）",
			rcRawAt(t, rc, "ran"), err)
	}
	if raw := rcRawAt(t, rc, "commit"); strings.TrimSpace(string(raw)) != "null" {
		t.Fatalf("dry-run 的 reconcile.commit 必须为 null，实得 %s", raw)
	}
}

// —— ⑦ 范围收窄类参数一律非法：退 1 且零写入零 commit ——

func TestReconcileRejectsTargetAndDomainFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "target", args: []string{"--target", "k-x"}},
		{name: "domain", args: []string{"--domain", "d"}},
		{name: "include_deprecated", args: []string{"--include-deprecated"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, _ := reviewedVault(t)
			beforeCommits := gitLogCount(t, dir)
			beforeStatus := gitOut(t, dir, "status", "--porcelain")
			beforeTree := treeSnapshot(t, dir)

			code, out, errOut := runReconcileCLI(t, newTestRoot(t, dir), dir, tc.args...)
			if code != ExitUsage {
				t.Fatalf("%v 应按参数非法退 1，实得 %d（%s / %s）", tc.args, code, out, errOut)
			}
			// 全库对账不接受任何范围收窄参数：未声明即由参数解析当场判非法（收窄属 S4）。
			if !strings.Contains(out+errOut, strings.TrimPrefix(tc.args[0], "--")) {
				t.Fatalf("错误信息未点名非法参数 %q：%s / %s", tc.args[0], out, errOut)
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("参数非法路径产生了 commit：%d → %d", beforeCommits, got)
			}
			if got := gitOut(t, dir, "status", "--porcelain"); got != beforeStatus {
				t.Fatalf("参数非法路径改动了工作区：\n前：%s\n后：%s", beforeStatus, got)
			}
			if diff := treeDiff(t, dir, beforeTree); diff != "" {
				t.Fatalf("参数非法必须零写入，实际变化：%s", diff)
			}
		})
	}
}

// —— ⑧ 退出码次序纯函数的表驱动锁：4 → 3 → 2 → 0 ——

func TestReconcileExitCodePrecedence(t *testing.T) {
	cases := []struct {
		name  string
		facts reconcileFacts
		want  int
		why   string
	}{
		{
			name: "clean", facts: reconcileFacts{}, want: ExitOK,
			why: "无失败、无跳过、无 error finding → 0",
		},
		{
			name: "error_finding_only", facts: reconcileFacts{ErrorFindings: 1}, want: ExitValidation,
			why: "只有 error 级 finding → 2（纳管已在更早一步完成）",
		},
		{
			name: "repair_rejected_only", facts: reconcileFacts{RepairRejected: 1}, want: ExitValidation,
			why: "修复被结构性拒绝与 error finding 同归 2，不新增第六个码",
		},
		{
			name: "skip_only", facts: reconcileFacts{SkippedWrites: 1}, want: ExitPartialWrite,
			why: "只有 B3 跳过 → 3",
		},
		{
			// 施工卡点名必须有的一格：同时存在 error 级 finding 与 B3 跳过 → 恰 3。
			name:  "error_finding_and_b3_skip",
			facts: reconcileFacts{ErrorFindings: 3, SkippedWrites: 1},
			want:  ExitPartialWrite,
			why:   "3 优先于 2：「有事实没落盘」是更强的行动信号",
		},
		{
			name:  "repair_rejected_and_b3_skip",
			facts: reconcileFacts{RepairRejected: 2, SkippedWrites: 5},
			want:  ExitPartialWrite,
			why:   "3 同样优先于「修复被拒」这一路 2",
		},
		{
			// 施工卡点名必须有的另一格：同时 Git 提交失败与 error finding → 恰 4。
			name:  "commit_failed_and_error_finding",
			facts: reconcileFacts{CommitFailed: true, ErrorFindings: 7},
			want:  ExitCommitFailed,
			why:   "4 最高：提交失败意味着本次一条历史都没落，先报它",
		},
		{
			name:  "commit_failed_beats_everything",
			facts: reconcileFacts{CommitFailed: true, SkippedWrites: 9, ErrorFindings: 9, RepairRejected: 9},
			want:  ExitCommitFailed,
			why:   "四格同时成立时仍恰 4（次序锁死，不做任何组合特判）",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reconcileExitCode(tc.facts); got != tc.want {
				t.Fatalf("reconcileExitCode(%+v) = %d，期望 %d（%s）", tc.facts, got, tc.want, tc.why)
			}
		})
	}
}

// —— ⑨ 本命令从不产出 5 / 6（三格合判）——

func TestReconcileNeverUsesExitFiveOrSix(t *testing.T) {
	// 第一格：退出码纯函数的**全枚举**（四个事实位，2×2×2×2 = 16 组），值域恰 {0,2,3,4}。
	allowed := map[int]bool{ExitOK: true, ExitValidation: true, ExitPartialWrite: true, ExitCommitFailed: true}
	seen := map[int]bool{}
	for _, failed := range []bool{false, true} {
		for _, skipped := range []int{0, 1} {
			for _, errs := range []int{0, 1} {
				for _, rejected := range []int{0, 1} {
					f := reconcileFacts{CommitFailed: failed, SkippedWrites: skipped,
						ErrorFindings: errs, RepairRejected: rejected}
					got := reconcileExitCode(f)
					if !allowed[got] {
						t.Fatalf("reconcileExitCode(%+v) = %d，超出本命令码集 {0,2,3,4}", f, got)
					}
					// 退出码 5 在本仓刻意**不定义常量**（exitcode.go：「不存在的符号是最硬的护栏」），
					// 因此这里只能用字面量 5 反证；6 有常量，用常量比对。
					if got == 5 || got == ExitNeedConfirm {
						t.Fatalf("reconcileExitCode(%+v) 产出 5 / 6：本命令不使用这两个码", f)
					}
					seen[got] = true
				}
			}
		}
	}
	if len(seen) != 4 {
		t.Fatalf("全枚举只覆盖到 %v，四个码应各至少出现一次", seen)
	}

	// 第二格：退出码 5 自 M6（T-074）起已全局启用，但 reconcile 仍不使用它
	//（reconcile 是只读体检命令，永不退 5；启用位为真不改变本命令的码集合）。
	if !ExitCode5Enabled() {
		t.Fatal("ExitCode5Enabled() 自 M6 起恒 true；reconcile 自身仍不产 5")
	}

	// 第三格：需确认命令白名单里没有 reconcile（它没有任何确认点 → 不可能退 6）。
	for _, c := range NeedConfirmCommands() {
		if c == "reconcile" || strings.HasPrefix(c, "reconcile ") {
			t.Fatalf("NeedConfirmCommands() 里出现 %q：reconcile 无确认点，不得进白名单（实得 %v）",
				c, NeedConfirmCommands())
		}
	}
	if NeedConfirmEnabled("reconcile") {
		t.Fatal("NeedConfirmEnabled(\"reconcile\") 必须为 false")
	}
}

// —— ⑩ eg report --last 逐字复现本次 reconcile 三键 ——

func TestReconcileReportLastRoundTrip(t *testing.T) {
	dir, _, dupRel := rcDupVault(t)
	_ = dupRel
	code, out, errOut := runReconcileCLI(t, newTestRoot(t, dir), dir)
	if code != ExitValidation {
		t.Fatalf("对账退出码 = %d，期望 2（%s / %s）", code, out, errOut)
	}
	live := rcRawAt(t, []byte(out), "data", "reconcile")
	rcAssertReconcileShape(t, live)

	beforeCommits := gitLogCount(t, dir)
	beforeTree := treeSnapshot(t, dir)
	rcode, rout, rerr := runCLI(t, newTestRoot(t, dir), "report", "--last", "--vault", dir, "--json")
	if rcode != ExitOK {
		t.Fatalf("eg report --last 退出码 = %d（%s / %s）", rcode, rout, rerr)
	}
	replay := rcRawAt(t, []byte(rout), "data", "report", "reconcile")

	// 取值逐字节相等 + 键序逐字相等：复现的是同一批事实，不是重算一遍。
	if string(replay) != string(live) {
		t.Fatalf("report --last 未逐字复现本次 reconcile 三键\n本次：%s\n复现：%s", live, replay)
	}
	if a, b := rcOrderedKeys(t, live), rcOrderedKeys(t, replay); strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("键序不一致：本次 %v，复现 %v", a, b)
	}
	if got := rcOrderedKeys(t, replay); strings.Join(got, ",") != strings.Join(rcReconcileKeys, ",") {
		t.Fatalf("复现的键序 = %v，期望逐字 %v", got, rcReconcileKeys)
	}
	// findings 条数与 check 名集合也比对一遍（防「只对了三个键名」的假绿）。
	liveFs := rcAssertReconcileShape(t, live)
	replayFs := rcAssertReconcileShape(t, replay)
	if len(liveFs) != len(replayFs) || len(liveFs) == 0 {
		t.Fatalf("findings 条数 %d → %d（且不得为 0）", len(liveFs), len(replayFs))
	}
	var a, b []string
	for i := range liveFs {
		a = append(a, liveFs[i].Check+"/"+liveFs[i].Severity)
		b = append(b, replayFs[i].Check+"/"+replayFs[i].Severity)
	}
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("finding 的 check/severity 集合不一致：%v vs %v", a, b)
	}
	// 只读命令零副作用（顺带反证 report --last 不会重跑对账）。
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("eg report --last 产生了 commit：%d → %d", beforeCommits, got)
	}
	if diff := treeDiff(t, dir, beforeTree); diff != "" {
		t.Fatalf("eg report --last 必须零文件变化，实际：%s", diff)
	}
}
