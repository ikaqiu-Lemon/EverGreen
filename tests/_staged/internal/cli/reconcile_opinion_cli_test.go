package cli

// [S3] reconcile_opinion_cli_test.go —— T-004-C2：`eg reconcile` / `eg check` 两条命令
// 对 schema v2 观点的**端到端**判据，以及 R2 / R6 修复与观点**共存**的边界。
//
// # 文件名前缀不是随手取的
//
// 本文件 import 了对账包，而 TestStage3ReconcilePackageBoundary 的**文件级位置锁**
// 规定 `internal/cli/` 下只有 `reconcile*` / `check*` 前缀的文件可以消费它 ——
// 这是「对账不作为写命令前置」（合同 §0.1 第 3 条）的机器形态。故前缀必须为 `reconcile`。
//
// # 与 C1 的分工（一条也不重复）
//
// C1（`internal/reconcile/opinion_reconcile_test.go`）考的是**判定层**：扫描面折条目、
// R3 关系诊断、R4 结构索引与 E12 覆盖面。本文件一条判定逻辑都不重测，只考**命令层**：
// 取数面到底把观点采进来了没有、只读命令的只读性、以及修复路径会不会碰观点字节。
//
// # 契约边界（T-004-C2 逐条不得越线）
//
//   - 观点带来的新判定面**只属于** R3 / R4。R2 仍只针对知识卡 / 材料笔记的 `reviewed_at`，
//     R6 仍只针对综述的 `stale`。本文件用 TestOpinionNeverEntersR2R6RepairSurface 把这条
//     边界钉成机器判据：不给观点发明 `reviewed_at`、不发明 `stale`、不产新 RepairSpec。
//   - `eg check` 的检查面恒 7 个 check，观点不改变这个数。
//
// # 为什么必须走命令入口，而不是在判定层再补几支
//
// 「命令层有没有把观点采进来」是**取数面**的事实，判定层测不到：C1 的用例都是自己拼
// `query.ScanResult` 喂给 `reconcile.Run`，天然绕过了 `sampleReconcileInput` /
// `sampleCheckInput` 这两处真实采样。假如命令层另有一份忽略 opinions 的采样实现，
// C1 全绿而命令层照旧漏判 —— 只有从命令入口投真库才能钉住这一格。
//
// # 一条反证纪律（防止「绿」是空的）
//
// 「合法观点零误报」这类判据**单独看是可以被空实现骗过的**：命令层要是根本没采观点，
// 它同样零 finding。因此本文件恒把两支成对写：
// TestReconcileCLILegalOpinionVaultAllGreen（合法 → 零误报）必须与
// TestOpinionDanglingRelationSurfacedByBothCommands（悬空 → 必被点名）**同时**成立，
// 后者一红就说明前者的绿是空的。
//
// 语料一律是磁盘真事实：观点由真 plan 经 `eg apply` 落盘，关系异常由**外部编辑**真实写进
// frontmatter（模拟用户绕过 CLI 直接改文件，这正是 M4 要纳管的那件事）。全文件零 mock。

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/reconcile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 语料：一个含合法观点的真库 ——

// opinionVault 造一个**合法**的库：一篇材料笔记 + 两张知识卡 + 一条卡间关系 + 一个观点，
// 全部经真 plan 走 `eg apply` 落盘，因此工作区恒干净、每份字节都是产品代码写出来的。
//
// 返回观点的 vault 相对路径，供各用例做「字节不变」与「被点名」的对照。
func opinionVault(t *testing.T) (dir, cardRel, opinionRel string) {
	t.Helper()
	dir = applyVault(t)
	applyNoteAndCard(t, dir)
	if code, _, errOut := runApplyPlan(t, dir, cardPlan(applyCard2ID, "")); code != ExitOK {
		t.Fatalf("落第二张卡退出码 = %d：%s", code, errOut)
	}
	cardRel = store.CardRel("ai-infra", applyCardID)
	code, _, errOut := runApplyPlan(t, dir,
		opinionAndRelationPlan(applyCardID, applyCard2ID, hashOf(t, dir, cardRel)))
	if code != ExitOK {
		t.Fatalf("落观点退出码 = %d：%s", code, errOut)
	}
	opinionRel = store.OpinionRel("ai-infra", applyOpinionID)
	// 前置①：观点确实落在 `domains/*/opinions/` 下（后面所有断言都以它在盘为前提）。
	if _, err := os.Stat(absIn(dir, opinionRel)); err != nil {
		t.Fatalf("前置不成立：观点未落盘（%s）：%v", opinionRel, err)
	}
	// 前置②：工作区干净 —— 否则 R1 的未提交改动会混进后面的 finding 断言。
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("前置不成立：工作区应干净，实得 %q", got)
	}
	return dir, cardRel, opinionRel
}

// opAddRelations 用**外部编辑**给一份产物的 frontmatter 插入 `relations[]`。
//
// 为什么用外部编辑而不是 `eg rel add`：`create_opinion` 的落盘键序里没有 `relations`
// （见 internal/store/opinion.go 的 opinionContent），而本用例要考的恰恰是「用户绕过 CLI
// 直接改文件之后，对账能不能如实点名」。外部编辑是这条判据唯一的真实语料来源，
// check_test.go 的关系异常语料同口径。
//
// 插入点在 frontmatter 的结束分隔符之前，因此不动任何既有键的整行字节。
func opAddRelations(t *testing.T, dir, rel, typ, target string) {
	t.Helper()
	path := absIn(dir, rel)
	raw := string(mustRead(t, path))
	// 开头分隔符在文件首行（偏移 0，前面没有换行），因此结束分隔符是**首行之后**
	// 第一个 "\n---\n"：直接找 "\n---\n" 会命中它自己，不能拿它当开头。
	const head = "---\n"
	if !strings.HasPrefix(raw, head) {
		t.Fatalf("%s 不是带 frontmatter 的产物：\n%s", rel, raw)
	}
	j := strings.Index(raw[len(head):], "\n---\n")
	if j < 0 {
		t.Fatalf("%s 的 frontmatter 没有结束分隔符：\n%s", rel, raw)
	}
	end := len(head) + j + 1 // 指向结束分隔符那一行的行首
	block := "relations:\n  - type: " + typ + "\n    target: " + target +
		"\n    reason: 外部编辑写入的关系条目\n"
	if err := os.WriteFile(path, []byte(raw[:end]+block+raw[end:]), 0o644); err != nil {
		t.Fatalf("外部编辑 %s 失败：%v", rel, err)
	}
	// 前置自证：插入后 relations 确实落在 frontmatter 内（否则后面的「必被点名」是空考）。
	after := string(mustRead(t, path))
	if !strings.Contains(after[:strings.Index(after[len(head):], "\n---\n")+len(head)], "relations:") {
		t.Fatalf("relations 未插进 frontmatter：\n%s", after)
	}
}

// opVaultBytes 把整个 vault（除 `.git` 与 `.index` 运行时目录）的**逐文件字节**快照下来。
//
// 只读命令的「零写入」不能只看 `git status`：写进 `.gitignore` 覆盖范围的文件、或对既有文件
// 的原地改写 + 复原，都可能让 porcelain 看起来干净。逐字节全库比对才是机器可判的零写入。
func opVaultBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Clean(dir)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if info.IsDir() {
			// `.git` 会随 commit 变动；`.index` 是运行时目录（事务日志住这儿），
			// 两者都不是权威 Markdown，跳过它们才让本快照专考「权威字节」。
			if rel == ".git" || rel == ".index" {
				return filepath.SkipDir
			}
			return nil
		}
		out[rel] = string(mustRead(t, path))
		return nil
	})
	if err != nil {
		t.Fatalf("快照 vault 字节失败：%v", err)
	}
	return out
}

// opFindingsMentioning 过滤出 targets 或 detail 里点到 needle 的 finding。
func opFindingsMentioning(fs []rcFinding, needle string) []rcFinding {
	var out []rcFinding
	for _, f := range fs {
		hit := strings.Contains(f.Detail, needle)
		for _, tg := range f.Targets {
			if strings.Contains(tg, needle) {
				hit = true
			}
		}
		if hit {
			out = append(out, f)
		}
	}
	return out
}

// opErrorFindings 取 error 级 finding（「全绿」的机器判据落在这一档）。
func opErrorFindings(fs []rcFinding) []rcFinding {
	var out []rcFinding
	for _, f := range fs {
		if f.Severity == "error" {
			out = append(out, f)
		}
	}
	return out
}

// —— ① 判据 1：含合法观点的库跑 `eg reconcile` 全绿（零误报）——

// TestReconcileCLILegalOpinionVaultAllGreen：合法观点参与 R3 / R4 之后不产生任何
// error 级 finding，也没有任何 finding 点到那个观点。
//
// 与 TestOpinionDanglingRelationSurfacedByBothCommands 成对：那支证明命令层**确实**在看
// 观点，本支才有意义（否则「零误报」可以被「根本没采」冒充）。
func TestReconcileCLILegalOpinionVaultAllGreen(t *testing.T) {
	dir, _, opinionRel := opinionVault(t)

	code, out, errOut := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（合法观点不得让对账退非 0）：%s\n%s", code, errOut, out)
	}
	fs := rcAssertReconcileShape(t, rcRawAt(t, []byte(out), "data", "reconcile"))
	if got := opErrorFindings(fs); len(got) != 0 {
		t.Fatalf("合法库不得有 error 级 finding，实得 %d 条：%+v", len(got), got)
	}
	// 逐条反证：没有任何 finding 点到这个观点（路径或 ID 任一形态）。
	for _, needle := range []string{opinionRel, applyOpinionID} {
		if got := opFindingsMentioning(fs, needle); len(got) != 0 {
			t.Fatalf("合法观点被误报（点名 %q）：%+v", needle, got)
		}
	}
}

// —— ② 判据 2：`eg check` 仍恰 7 个 check，且零权威写入 / 零事务 / 零 commit ——

// TestCheckCLIOnOpinionVaultStaysSevenChecksReadOnly：观点入库**不改变** `eg check` 的
// 检查面基数，也不给它添任何写入面。
func TestCheckCLIOnOpinionVaultStaysSevenChecksReadOnly(t *testing.T) {
	dir, _, opinionRel := opinionVault(t)

	beforeBytes := opVaultBytes(t, dir)
	beforeCommits := gitLogCount(t, dir)
	beforeTxns := txnIDsOn(t, dir)

	code, out, errOut := runCheckCLI(t, dir)
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0（合法库结构面无 error）：%s\n%s", code, errOut, out)
	}

	// —— 检查面恒 7：常量、派生集合、信封三处同时复算 ——
	if CheckScopeCount != 7 {
		t.Fatalf("CheckScopeCount = %d，期望恰 7（观点不新增 check）", CheckScopeCount)
	}
	if CheckExcludedCount != 5 {
		t.Fatalf("CheckExcludedCount = %d，期望恰 5", CheckExcludedCount)
	}
	if got := chkStringSlice(t, out, "scope"); len(got) != 7 ||
		!reflect.DeepEqual(got, chkWantScope) {
		t.Fatalf("data.check.scope = %v，期望恰 7 个且逐字等于 %v", got, chkWantScope)
	}
	if got := chkStringSlice(t, out, "excluded"); !reflect.DeepEqual(got, chkWantExcluded) {
		t.Fatalf("data.check.excluded = %v，期望逐字等于 %v", got, chkWantExcluded)
	}

	// —— 只读性：权威字节、事务、commit 三格逐一不变 ——
	if got := opVaultBytes(t, dir); !reflect.DeepEqual(got, beforeBytes) {
		t.Fatalf("eg check 必须零写入：权威字节发生变化\n前 %v\n后 %v",
			opSortedKeys(beforeBytes), opSortedKeys(got))
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("eg check 恒 0 次提交：commit 数 %d → %d", beforeCommits, got)
	}
	if got := txnIDsOn(t, dir); !reflect.DeepEqual(sortedCopy(got), sortedCopy(beforeTxns)) {
		t.Fatalf("eg check 不得开事务：事务集合 %v → %v", beforeTxns, got)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("eg check 之后工作区应仍干净，实得 %q", got)
	}
	// 观点本身逐字节未被动过（只读命令连读都不该改 mtime 之外的任何东西）。
	if got := opVaultBytes(t, dir)[opinionRel]; got != beforeBytes[opinionRel] {
		t.Fatal("eg check 改写了观点字节")
	}
}

// opSetReplacedBy 用**外部编辑**给一份产物的 frontmatter 写入 `replaced_by: {target, reason}`。
//
// 为什么用外部编辑而不是 `eg replaced-by`：写命令会在落盘前校验 target 端点是否真实存在
// （指向缺失端点会被当场拒绝），而本用例恰恰要造「指向缺失端点」这条磁盘真事实来考对账的
// 存在性判定。插入点在 frontmatter 结束分隔符之前，不动任何既有键的整行字节。
func opSetReplacedBy(t *testing.T, dir, rel, target string) {
	t.Helper()
	path := absIn(dir, rel)
	raw := string(mustRead(t, path))
	const head = "---\n"
	if !strings.HasPrefix(raw, head) {
		t.Fatalf("%s 不是带 frontmatter 的产物：\n%s", rel, raw)
	}
	j := strings.Index(raw[len(head):], "\n---\n")
	if j < 0 {
		t.Fatalf("%s 的 frontmatter 没有结束分隔符：\n%s", rel, raw)
	}
	end := len(head) + j + 1
	block := "replaced_by:\n  target: " + target +
		"\n  reason: 外部编辑写入的替代指针\n"
	if err := os.WriteFile(path, []byte(raw[:end]+block+raw[end:]), 0o644); err != nil {
		t.Fatalf("外部编辑 %s 失败：%v", rel, err)
	}
}

// TestCheckReplacedByTargetEndpointUniverseCLI：`eg check` / `eg reconcile --dry-run` 对
// `replaced_by.target` 的存在性判定面是**论证关系端点宇宙**（知识卡 ∪ 观点），真 CLI 端到端复核。
//
//   - target 指向库内**真实存在**的观点（`o-*`）→ 零 E12：两条命令都不因替代指针点名宿主卡
//     （旧实现只查 KindCard，会把它误报成悬空引用）。
//   - target 指向**形态合法但库内缺失**的观点 → 恰 1 条 error 级 `dangling_ref`（E12），
//     且 `eg check` 只读：零写入、恒 0 次提交、不开事务、工作区仍如外部编辑后的状态。
func TestCheckReplacedByTargetEndpointUniverseCLI(t *testing.T) {
	// ① 指向真实存在的观点端点：零 E12。
	t.Run("target 指向存在的观点端点 → 零 dangling_ref", func(t *testing.T) {
		dir, cardRel, _ := opinionVault(t)
		opSetReplacedBy(t, dir, cardRel, applyOpinionID) // applyOpinionID 是库内真实存在的观点

		code, out, errOut := runCheckCLI(t, dir)
		if code != ExitOK {
			t.Fatalf("replaced_by.target 指向存在观点，eg check 应退 0，实得 %d：%s\n%s", code, errOut, out)
		}
		for _, f := range opFindingsMentioning(chkFindings(t, out), applyCardID) {
			if f.Check == "dangling_ref" {
				t.Fatalf("指向存在观点端点不得报 dangling_ref：%+v", f)
			}
		}
		// eg reconcile 同一事实同样不得因替代指针报 E12。
		_, rout, _ := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")
		rfs := rcAssertReconcileShape(t, rcRawAt(t, []byte(rout), "data", "reconcile"))
		for _, f := range opFindingsMentioning(rfs, applyCardID) {
			if f.Check == "dangling_ref" {
				t.Fatalf("eg reconcile 也不得因指向存在观点报 dangling_ref：%+v", f)
			}
		}
	})

	// ② 指向形态合法但缺失的观点端点：恰 1 条 error 级 E12 + 只读零副作用。
	t.Run("target 指向缺失的观点端点 → E12 且 eg check 只读", func(t *testing.T) {
		const missOpinion = "o-20261231-absent"
		dir, cardRel, _ := opinionVault(t)
		opSetReplacedBy(t, dir, cardRel, missOpinion)

		beforeBytes := opVaultBytes(t, dir)
		beforeCommits := gitLogCount(t, dir)
		beforeTxns := txnIDsOn(t, dir)

		code, out, _ := runCheckCLI(t, dir)
		if code != ExitValidation {
			t.Fatalf("replaced_by.target 指向缺失观点，eg check 应退 2（存在 error 级 finding），实得 %d\n%s", code, out)
		}
		hit := opFindingsMentioning(chkFindings(t, out), applyCardID)
		var dangling []rcFinding
		for _, f := range hit {
			if f.Check == "dangling_ref" {
				dangling = append(dangling, f)
			}
		}
		if len(dangling) != 1 {
			t.Fatalf("指向缺失观点端点应恰 1 条 dangling_ref，实得 %d 条：%+v", len(dangling), hit)
		}
		f := dangling[0]
		if f.Severity != "error" {
			t.Fatalf("dangling_ref 应是 error 级，实得 %q", f.Severity)
		}
		want := []string{applyCardID, missOpinion}
		sort.Strings(want)
		if !reflect.DeepEqual(sortedCopy(f.Targets), want) {
			t.Fatalf("targets 期望 %v，实得 %v", want, f.Targets)
		}

		// —— 只读性：权威字节、commit、事务三格逐一不变（缺失目标不改变 eg check 的零副作用）——
		if got := opVaultBytes(t, dir); !reflect.DeepEqual(got, beforeBytes) {
			t.Fatalf("eg check 必须零写入：权威字节发生变化\n前 %v\n后 %v",
				opSortedKeys(beforeBytes), opSortedKeys(got))
		}
		if got := gitLogCount(t, dir); got != beforeCommits {
			t.Fatalf("eg check 恒 0 次提交：commit 数 %d → %d", beforeCommits, got)
		}
		if got := txnIDsOn(t, dir); !reflect.DeepEqual(sortedCopy(got), sortedCopy(beforeTxns)) {
			t.Fatalf("eg check 不得开事务：事务集合 %v → %v", beforeTxns, got)
		}
		// eg reconcile --dry-run 同一事实同样可见（不是只有 check 看得见）。
		_, rout, _ := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")
		rfs := rcAssertReconcileShape(t, rcRawAt(t, []byte(rout), "data", "reconcile"))
		var rdang int
		for _, rf := range opFindingsMentioning(rfs, applyCardID) {
			if rf.Check == "dangling_ref" {
				rdang++
			}
		}
		if rdang != 1 {
			t.Fatalf("eg reconcile 也应恰 1 条 dangling_ref 点名宿主卡，实得 %d：%+v", rdang, rfs)
		}
	})
}

// —— ③ 判据 3：命令层透传 C1 的 R3 / R4 诊断（悬空 o-* 关系必被点名）——

// TestOpinionDanglingRelationSurfacedByBothCommands：观点 `relations[]` 指向库内查无此
// 对象的端点（缺失 `k-` / `o-` → E13）或形态非法的 target（非 k/o 端点 / 畸形 → E14）时，
// **两条命令**都必须如实点名、不静默。
//
// 端点合同（B2c）：论证关系端点宇宙 = 知识卡（`k-`）∪ 观点（`o-`）。因此「指向不存在的
// 观点」是**存在性缺失**（relation_target_missing / E13），不再是「前缀不合法」——
// 这正是本用例第 2 行要钉死的回归点：缺失 o-* 由 `eg check` 与 `eg reconcile` 都报
// relation_target_missing，而非 relation_prefix_invalid。
//
// 这支同时是「命令层确实把观点采进来了」的正面反证（见文件头的反证纪律）。
func TestOpinionDanglingRelationSurfacedByBothCommands(t *testing.T) {
	cases := []struct {
		name      string
		typ       string
		target    string
		wantCheck string
	}{
		// target 形态合法（`k-` + 8 位 + 非空 slug）但库里查无此卡 → 存在性缺失。
		{"target 指向不存在的知识卡 → relation_target_missing",
			"supports", "k-20261231-nonexistent", "relation_target_missing"},
		// target 形态合法（`o-` + 8 位 + 非空 slug）但库里查无此观点 → 同样是存在性缺失，
		// **不再**当作前缀不合法（观点是合法端点）。
		{"target 指向不存在的观点 → relation_target_missing（而非 prefix_invalid）",
			"supports", "o-20261231-other", "relation_target_missing"},
		// target 是非 k/o 端点（笔记 ID）→ 前缀 / 形态不合法，与存在性互斥。
		{"target 指向笔记 ID（非 k/o 端点）→ relation_prefix_invalid",
			"supports", "n-20261231-note", "relation_prefix_invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, _, opinionRel := opinionVault(t)
			opAddRelations(t, dir, opinionRel, c.typ, c.target)

			// —— eg check：结构面有 error → 恰退 2，零写入零提交 ——
			beforeCommits := gitLogCount(t, dir)
			code, out, _ := runCheckCLI(t, dir)
			if code != ExitValidation {
				t.Fatalf("eg check 退出码 = %d，期望 2（结构面存在 error 级 finding）\n%s", code, out)
			}
			if got := gitLogCount(t, dir); got != beforeCommits {
				t.Fatalf("eg check 恒 0 次提交：%d → %d", beforeCommits, got)
			}
			hit := opFindingsMentioning(chkFindings(t, out), applyOpinionID)
			if len(hit) == 0 {
				t.Fatalf("eg check 必须点名持有该关系的观点 %s，实得 findings = %v",
					applyOpinionID, chkNames(chkFindings(t, out)))
			}
			var got []string
			for _, f := range hit {
				got = append(got, f.Check)
				if f.Severity != "error" {
					t.Fatalf("%s 应是 error 级，实得 %q", f.Check, f.Severity)
				}
			}
			if !opContains(got, c.wantCheck) {
				t.Fatalf("eg check 应产出 %s，实得 %v", c.wantCheck, got)
			}
			// detail 必须如实称呼持有方是「观点」——C1 把写死的「知识卡 X」改成按类别取称呼，
			// 命令层原样透传，这里逐字复核那条修复没在命令层被吞掉。
			if !strings.Contains(hit[0].Detail, "观点") {
				t.Fatalf("detail 应如实称呼持有方为「观点」，实得：%s", hit[0].Detail)
			}

			// —— eg reconcile：同一事实必须同样可见（不是只有 check 看得见）——
			_, rout, _ := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")
			rfs := rcAssertReconcileShape(t, rcRawAt(t, []byte(rout), "data", "reconcile"))
			if len(opFindingsMentioning(rfs, applyOpinionID)) == 0 {
				t.Fatalf("eg reconcile 也必须点名观点 %s，实得 findings = %v",
					applyOpinionID, chkNames(rfs))
			}
		})
	}
}

// opContains 报告 list 是否含 want。
func opContains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// opinionOnlyPlan 造一份只新建观点（无关系）的 plan，供「关系指向存在 o-*」正面用例补第二个
// 落盘观点。sources 复用库内已有的原文 / 笔记，因此不引入任何额外的悬空引用。
func opinionOnlyPlan(opinionID string) string {
	return `{"plan_version":2,"verb":"relate","domain":"ai-infra",
"reason":"落第二个观点作为存在的关系端点","requirement_ids":["EG-AGT-03"],
"ops":[{"op":"create_opinion","opinion_id":"` + opinionID + `",
 "title":"第二个观点：作为存在的关系端点",
 "sources":[{"source":"` + applySourceID + `","note":"` + applyNoteID + `",
 "rel":"support","reason":"复用同一原文的另一处论据"}],
 "sections":{"观点":"这是用于端点存在性对照的第二个观点。\n","论据与推理":"复用同一原文的另一处论据。\n"}}]}`
}

// TestOpinionRelationToExistingOpinionAccepted：观点持有的关系指向**存在**的 `o-*` 端点时，
// `eg check` 与 `eg reconcile` 都不得报 relation_prefix_invalid / relation_target_missing，
// 也不产任何 error —— 论证关系端点宇宙含观点（B2c 端点合同）的命令层正面证据。
//
// 与 TestOpinionDanglingRelationSurfacedByBothCommands 的「缺失 o-*」分支成对：那支证明
// 缺失即 E13，本支证明存在即零诊断，两支一起把「o-* 是合法端点、但仍照判存在性」钉死。
func TestOpinionRelationToExistingOpinionAccepted(t *testing.T) {
	dir, _, opinionRel := opinionVault(t)

	// 落第二个观点，作为**存在**的关系端点（同一事务落盘、工作区随后仍干净）。
	const secondOpinion = "o-20261017-second"
	if code, _, errOut := runApplyPlan(t, dir, opinionOnlyPlan(secondOpinion)); code != ExitOK {
		t.Fatalf("落第二个观点退出码 = %d：%s", code, errOut)
	}
	if _, err := os.Stat(absIn(dir, store.OpinionRel("ai-infra", secondOpinion))); err != nil {
		t.Fatalf("前置不成立：第二个观点未落盘：%v", err)
	}
	// 外部编辑：让第一个观点 `supports` 第二个（存在的）观点端点。
	opAddRelations(t, dir, opinionRel, "supports", secondOpinion)

	beforeCommits := gitLogCount(t, dir)
	beforeBytes := opVaultBytes(t, dir)

	// —— eg check：指向存在 o-* 不构成结构 error → 退 0，且无关系两码点名该边 ——
	code, out, errOut := runCheckCLI(t, dir)
	if code != ExitOK {
		t.Fatalf("eg check 退出码 = %d，期望 0（指向存在 o-* 是合法端点）：%s\n%s", code, errOut, out)
	}
	for _, f := range chkFindings(t, out) {
		if f.Check == "relation_prefix_invalid" || f.Check == "relation_target_missing" {
			t.Fatalf("指向存在 o-* 不得报 %s：%+v", f.Check, f)
		}
	}

	// —— eg reconcile：同一事实同样零 error、无关系诊断点名该端点 ——
	rcode, rout, rerr := runReconcileCLI(t, newTestRoot(t, dir), dir, "--dry-run")
	if rcode != ExitOK {
		t.Fatalf("eg reconcile 退出码 = %d，期望 0：%s\n%s", rcode, rerr, rout)
	}
	rfs := rcAssertReconcileShape(t, rcRawAt(t, []byte(rout), "data", "reconcile"))
	if got := opErrorFindings(rfs); len(got) != 0 {
		t.Fatalf("指向存在 o-* 不得产 error 级 finding：%+v", got)
	}
	for _, f := range rfs {
		if f.Check == "relation_prefix_invalid" || f.Check == "relation_target_missing" {
			t.Fatalf("eg reconcile 不得对存在 o-* 报 %s：%+v", f.Check, f)
		}
	}

	// —— 只读性：两条命令都零写入、零提交（权威字节与 commit 数不变）——
	if got := opVaultBytes(t, dir); !reflect.DeepEqual(got, beforeBytes) {
		t.Fatalf("check / reconcile 必须零写入：权威字节发生变化")
	}
	if got := gitLogCount(t, dir); got != beforeCommits {
		t.Fatalf("只读命令恒 0 次提交：commit 数 %d → %d", beforeCommits, got)
	}
}

// —— ④ 判据 4：R2 / R6 修复与观点共存（观点字节不变、不被遗漏或误写）——

// TestReconcileRepairKeepsOpinionBytesIntact：同一个库里既有观点、又有触发 R2 的外部编辑时，
// 走**完整** `eg reconcile`（非 dry-run）之后：
//
//	① 观点字节逐字节不变；② 本次写入路径恰是被编辑的那张卡；
//	③ 恰一次 commit（R1 纳管与 R2 补写合并，不因观点在库里多出一次）；
//	④ commit 的文件集合**不含**观点路径。
func TestReconcileRepairKeepsOpinionBytesIntact(t *testing.T) {
	dir, cardRel, opinionRel := opinionVault(t)

	opinionBefore := string(mustRead(t, absIn(dir, opinionRel)))
	// 外部编辑那张卡：同一个编辑同时构成 R1 的未提交改动与 R2 的命中。
	r2EditFile(t, dir, cardRel)
	beforeCommits := gitLogCount(t, dir)

	r := r2Root(t, dir)
	code, out, errOut := runReconcileCLI(t, r, dir, "--user-request")
	if code != ExitOK {
		t.Fatalf("退出码 = %d，期望 0：%s\n%s", code, errOut, out)
	}

	// —— ① 观点字节逐字节不变 ——
	if got := string(mustRead(t, absIn(dir, opinionRel))); got != opinionBefore {
		t.Fatalf("R2 修复不得碰观点字节\n前：%q\n后：%q", opinionBefore, got)
	}
	// —— ③ 恰一次 commit ——
	if got := gitLogCount(t, dir); got != beforeCommits+1 {
		t.Fatalf("commit 数 %d → %d，应恰 +1（R1 + R2 合并成一条）", beforeCommits, got)
	}
	// —— ④ commit 文件集合不含观点 ——
	files := strings.Fields(gitOut(t, dir, "show", "--name-only", "--pretty=format:", "HEAD"))
	for _, f := range files {
		if f == opinionRel {
			t.Fatalf("本次 commit 不该含观点路径 %s（它没被改动）：%v", opinionRel, files)
		}
	}
	if !opContains(files, cardRel) {
		t.Fatalf("被外部编辑的卡 %s 必须进本次 commit：%v", cardRel, files)
	}
	// —— ② 观点没有长出 reviewed_at（R2 的待写键封闭在知识卡 / 笔记两类上）——
	if strings.Contains(opinionBefore, model.FMKeyReviewedAt) {
		t.Fatalf("前置不成立：新建观点不该带 %s", model.FMKeyReviewedAt)
	}
	after := string(mustRead(t, absIn(dir, opinionRel)))
	if strings.Contains(after, model.FMKeyReviewedAt) {
		t.Fatalf("不得给观点发明 %s（R2 只针对知识卡 / 材料笔记）：\n%s",
			model.FMKeyReviewedAt, after)
	}
	if got := strings.TrimSpace(gitOut(t, dir, "status", "--porcelain")); got != "" {
		t.Fatalf("对账后工作区应干净，实得 %q", got)
	}
}

// TestOpinionNeverEntersR2R6RepairSurface：契约边界的机器判据 ——
// 即使观点被外部编辑（在 R2 眼里「未提交改动」这条证据同样成立），
// R2 的命中对象与 RepairSpec 里**永不**出现观点，R6 也不给观点发明 `stale`。
//
// 这一支不走命令层而直接问判定层：命令层的修复桥是拿 `ReviewedTargets` /
// `Repairs` 当入参的，源头不含观点，桥自然碰不到它 —— 把判据钉在源头比钉在桥上更强。
func TestOpinionNeverEntersR2R6RepairSurface(t *testing.T) {
	dir, _, opinionRel := opinionVault(t)
	// 连观点一起外部编辑：把「R2 证据① 未提交改动」这条对观点也造足。
	r2EditFile(t, dir, opinionRel)

	targets, res := r2Check(t, dir)
	for _, tg := range targets {
		if tg.Path == opinionRel || strings.HasPrefix(tg.ID, "o-") {
			t.Fatalf("R2 命中对象不得含观点（%+v）：reviewed_at 只属知识卡 / 材料笔记", tg)
		}
	}
	for _, sp := range res.Repairs {
		if sp.Path == opinionRel {
			t.Fatalf("RepairSpec 不得指向观点：%+v", sp)
		}
		// 待写键集合恒封闭：观点在库里也不会让它长出第二个键。
		if sp.Check == reconcile.CheckReviewedAtMissing &&
			!reflect.DeepEqual(sp.Keys, reconcile.ReviewedKeys()) {
			t.Fatalf("R2 待写键集合应恒 %v，实得 %v", reconcile.ReviewedKeys(), sp.Keys)
		}
	}
	// R6 的 `stale` 只写综述：观点字节里不得出现这个键。
	if got := string(mustRead(t, absIn(dir, opinionRel))); strings.Contains(got, "stale:") {
		t.Fatalf("不得给观点发明 stale（R6 只针对综述）：\n%s", got)
	}
}

// —— ⑤ 帮助文本的覆盖面必须取自真源（C1 留下的漂移，本批修掉）——

// TestCheckHelpDanglingCoverageDerivedFromSource：`eg check` 的帮助文本里 E12 的覆盖面
// **必须与判定层的真源一致**。
//
// 为什么需要这一支：C1 把 E12 从四类扩到六类（追加 `opinion.sources[].note` 与
// `opinion.sources[].source`），但 `eg check` 的 Usage 里抄了一份写死的「恰四类」名单，
// 于是帮助里说四类、运行时 detail 说六类 —— 用户读到的是**假话**。check.go 自己的设计
// 原则写得很清楚：「抄名单会在检查表变动时静默失真」（排除集合就是从真源取补集的）。
// 本支把这条原则也施加到 E12 覆盖面上：数量与名单都从 `reconcile` 取，抄本不许再出现。
func TestCheckHelpDanglingCoverageDerivedFromSource(t *testing.T) {
	kinds := reconcile.DanglingRefKinds()
	if len(kinds) != reconcile.DanglingRefKindCount {
		t.Fatalf("DanglingRefKinds() 有 %d 项，DanglingRefKindCount = %d：两者必须同源",
			len(kinds), reconcile.DanglingRefKindCount)
	}
	// 名单不得有重复或空串（它要进用户可见的帮助文本）。
	seen := map[string]bool{}
	for _, k := range kinds {
		if strings.TrimSpace(k) == "" {
			t.Fatal("DanglingRefKinds() 不得含空串")
		}
		if seen[k] {
			t.Fatalf("DanglingRefKinds() 含重复项 %q", k)
		}
		seen[k] = true
	}

	usage := checkCommand().Usage
	// ① 六类逐条都在帮助里（漏一条就是漏说一种用户会遇到的 error）。
	for _, k := range kinds {
		if !strings.Contains(usage, k) {
			t.Fatalf("eg check 帮助文本缺 E12 覆盖类别 %q：\n%s", k, usage)
		}
	}
	// ② 写死的旧口径不许再出现。
	for _, stale := range []string{"恰四类", "恰 4 类"} {
		if strings.Contains(usage, stale) {
			t.Fatalf("帮助文本仍写着 %q（真源已是 %d 类）：\n%s",
				stale, reconcile.DanglingRefKindCount, usage)
		}
	}
	// ③ 类别数如实（用真源渲染，不是又抄一个数字）。
	if want := "恰 " + itoaSmall(reconcile.DanglingRefKindCount) + " 类"; !strings.Contains(usage, want) {
		t.Fatalf("帮助文本应含 %q：\n%s", want, usage)
	}
	// ④ replaced_by.target 的端点措辞必须是「端点（知识卡或观点）」这一新口径（真源已放宽到
	// 知识卡 ∪ 观点端点宇宙）；写死的旧「→知识卡」抄本不许再出现在用户可见的帮助里。
	if !strings.Contains(usage, "replaced_by.target→端点（知识卡或观点）") {
		t.Fatalf("eg check 帮助文本应含 replaced_by.target 的端点措辞（知识卡或观点）：\n%s", usage)
	}
	if strings.Contains(usage, "replaced_by.target→知识卡") {
		t.Fatalf("帮助文本仍写着旧口径 replaced_by.target→知识卡（真源已放宽到端点宇宙）：\n%s", usage)
	}
}

// itoaSmall 把一位数转成十进制串（只服务上面那条断言，不引 strconv 增加本文件依赖面）。
func itoaSmall(n int) string {
	if n < 0 || n > 9 {
		return ""
	}
	return string(rune('0' + n))
}

// opSortedKeys 取一个字节快照的路径列表（升序），仅供失败信息排查用。
func opSortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
