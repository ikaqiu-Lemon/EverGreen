// M2 验收的 Go 断言集（T-evergreen.s1_main_flow-158614-029）。
//
// 只覆盖「shell 不易表达」的三类断言，不重复子脚本已覆盖的行为：
//  1. 总控脚本 `m2_acceptance.sh` 确实覆盖判据 1 ~ 9（九个标记各恰一次）且引用的 9 个
//     子脚本文件全部存在——删任一子脚本或漏一个判据标记即失败；
//  2. M1 的历史用例名（e2e 顶层用例 / 子用例 + B1–B4 用例）仍存在于源码——用例名硬编码
//     在断言里，M1 用例被删改即失败（不回归的机器可判部分）；
//  3. S1 九命令均非占位（`Placeholder` 字段全 false 且 Handler 均已挂载），
//     越界符号在 `internal/` 与 `cmd/` 的非测试源里只以「登记形态」出现。
//
// 本文件不改任何产品实现，也不新造口径：判据原文见 `milestones/M-002-m2.md`。
package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/cli"
)

// repoRootT029 返回 evergreen 仓根（本文件位于 <root>/test/e2e/）。
func repoRootT029(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func readFileT029(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s：%v", path, err)
	}
	return string(raw)
}

// m2SubScripts 是总控脚本必须按固定顺序串起的 9 个 e2e 子脚本。
var m2SubScripts = []string{
	"m1_real_article.sh",
	"m2_search.sh",
	"m2_card_show.sh",
	"m2_rel_query.sh",
	"m2_rel_add.sh",
	"m2_context_polish.sh",
	"m2_convergence.sh",
	"m2_docs_commands.sh",
	"m2_ppe_replay.sh",
}

// TestM2AcceptanceScriptCoversAllGates：判据标记齐备 + 子脚本在场 + 固定顺序。
func TestM2AcceptanceScriptCoversAllGates(t *testing.T) {
	root := repoRootT029(t)
	// 迁移后（system_assurance · T-…-003）：M2 阶段聚合器作为**历史治理材料**归档到
	// tests/archive/history/stage-acceptance/，其判据标记与三值口径在归档件里逐字保全；
	// 9 个子脚本则按系统能力搬到 tests/e2e/<capability>/。两侧都由迁移表解析，任一侧漂移即红。
	script := migratedAbs(t, root, "test/e2e/m2_acceptance.sh")
	body := readFileT029(t, script)

	for i := 1; i <= 9; i++ {
		marker := "[判据 " + string(rune('0'+i)) + "]"
		if got := strings.Count(body, marker); got != 1 {
			t.Fatalf("m2_acceptance.sh 中 %q 出现 %d 次，应恰 1 次", marker, got)
		}
	}
	// 三值口径必须在脚本里成文，且不得二值化。
	for _, want := range []string{"PASS", "FAIL", "NOT-VERIFIED", "M2 结论：", "失败判据编号："} {
		if !strings.Contains(body, want) {
			t.Fatalf("m2_acceptance.sh 缺少 %q", want)
		}
	}
	// 9 个子脚本都存在，且在总控脚本里按固定顺序被调起。
	pos := -1
	for _, s := range m2SubScripts {
		if _, err := os.Stat(migratedAbs(t, root, "test/e2e/"+s)); err != nil {
			t.Fatalf("子脚本缺失 %s（迁移后路径 %s）：%v", s, migratedRel(t, root, "test/e2e/"+s), err)
		}
		at := strings.Index(body, "run_sub "+s)
		if at < 0 {
			t.Fatalf("m2_acceptance.sh 未调起子脚本 %s", s)
		}
		if at <= pos {
			t.Fatalf("子脚本 %s 的调起顺序与固定顺序不一致", s)
		}
		pos = at
	}
}

// m1CaseNames：M1 的历史用例名（顶层 + 子用例）与 B1–B4 用例名，逐条硬编码。
// key = 用例名，value = 该名字必须出现的文件（相对仓根）。
var m1CaseNames = map[string]string{
	// M1 端到端顶层用例（test/e2e/m1_test.go）
	"TestM1RealArticleZeroIntervention":         "test/e2e/m1_test.go",
	"TestM1ErrorCasesRejectedWithZeroWrite":     "test/e2e/m1_test.go",
	"TestM1SafetyBaselines":                     "test/e2e/m1_test.go",
	"TestM1CoverageGapAbsentRerun":              "test/e2e/m1_test.go",
	"TestM1PlaceholderCommandsStayPlaceholders": "test/e2e/m1_test.go",
	// M1 端到端子用例
	"skill_sample1_dry_run":              "test/e2e/m1_test.go",
	"artifacts_complete":                 "test/e2e/m1_test.go",
	"report_matches_disk":                "test/e2e/m1_test.go",
	"coverage_gap_visible_in_both_forms": "test/e2e/m1_test.go",
	"git_chain_and_subject_format":       "test/e2e/m1_test.go",
	"idempotent_capture":                 "test/e2e/m1_test.go",
	"second_article_append_card":         "test/e2e/m1_test.go",
	"W1_cross_domain_is_warning":         "test/e2e/m1_test.go",
	"obsidian_parsable_artifacts":        "test/e2e/m1_test.go",
	"skipped_kind_closed_set":            "test/e2e/m1_test.go",
	// B1–B4（e2e 侧）
	"B1_append_only":                   "test/e2e/m1_test.go",
	"B2_user_block_preserved_verbatim": "test/e2e/m1_test.go",
	"B3_content_hash_mismatch_skip":    "test/e2e/m1_test.go",
	"B4_commit_failure_keeps_disk":     "test/e2e/m1_test.go",
	// B1–B4（单测侧）
	"TestB1NoDestructiveExports":                 "internal/store/store_test.go",
	"TestB1WriteFormsAreExactlyThree":            "internal/store/store_test.go",
	"TestGuardB2UserSectionsRoundTrip":           "internal/store/store_test.go",
	"TestGuardB2UnsafeUserBlockSkips":            "internal/store/store_test.go",
	"TestGuardB3FileChangedSkipsAndKeepsBytes":   "internal/store/store_test.go",
	"TestB4CommitFailureKeepsDiskState":          "internal/git/git_test.go",
	"TestB4RunnerFailureIsReportedNotRolledBack": "internal/git/git_test.go",
	// M2 新写入路径同样受 B1–B4 约束（T-…-024）
	"TestRelAddRespectsB3AndB4": "internal/cli/rel_add_test.go",
}

// TestM1CaseNamesStillPresent：M1 用例名一条不少（删任一即失败）。
func TestM1CaseNamesStillPresent(t *testing.T) {
	root := repoRootT029(t)
	cache := map[string]string{}
	for name, rel := range m1CaseNames {
		body, ok := cache[rel]
		if !ok {
			body = readFileT029(t, filepath.Join(root, rel))
			cache[rel] = body
		}
		if !strings.Contains(body, name) {
			t.Fatalf("M1 用例名 %q 已从 %s 消失：M1 回归证据被删改", name, rel)
		}
	}
	// M1 的 shell 端到端脚本必须仍在场且非空。
	st, err := os.Stat(migratedAbs(t, root, "test/e2e/m1_real_article.sh"))
	if err != nil || st.Size() == 0 {
		t.Fatalf("m1_real_article.sh 缺失或为空：%v", err)
	}
}

// TestS1NineCommandsAreImplemented：命令均非占位且均已挂载 Handler（判据 1 的机器可判部分）。
//
// 总数自 T-…-039 起是 **12**：S1 九命令 + M3 三条用户显式状态命令
// （`deprecate` / `restore` / `replaced-by`）。数字变了是**事实变了**，不是放宽判据——
// 「全部非占位 + 全部已挂载」这两条逐条不变。
func TestS1NineCommandsAreImplemented(t *testing.T) {
	cmds := cli.New().Commands()
	// T-…-040 起总数是 **13**：再加 S2 提案子系统的 `eg proposal`（五子命令一条命令）。
	// T-…-041 起总数是 **14**：再加 M3 逻辑删除 `eg delete`。数字变了是事实变了，
	// 「全部非占位 + 全部已挂载」两条判据逐条不变。
	// T-…-041 收尾起总数是 **15**：再加 M3 清空删除标记 `eg undelete`。
	// T-…-042 起总数是 **17**：再加 M3 的 `eg mark-reviewed`（reviewed_at 唯一写入路径）
	// 与 `eg unreviewed`（只读筛选）。数字变了是事实变了，两条判据逐条不变。
	// T-…-045 起总数是 **18**：再加 M3 的 `eg edit`（A-13，矩阵 #12 P-U ✅ 的唯一命令载体）。
	// 数字变了是事实变了，「全部非占位 + 全部已挂载」两条判据逐条不变。
	// M4 T-…-058 新增 `reconcile` 一条：**18 → 19**（加法等式 M3 期 18 + M4 新增 1 = 19，
	// 逐项复算见 internal/cli/cli_test.go 的 TestCommandCountNineteen）。M3 侧的加数
	// 与上面每一行说明逐字保留；本处只按实测重钉总数，两条判据一字未放宽。
	// M4 T-…-059 再新增 `check` 一条：**19 → 20**（M4 收口值；加法等式 M3 期 18 + M4 期 2 = 20，
	// 逐项复算见 internal/cli/cli_test.go 的 TestCommandCountTwenty）。M3 / M4 上游每一行说明
	// 逐字保留；本处只按实测重钉总数，「全部非占位 + 全部已挂载」两条判据一字未放宽。
	// M5 T-…-065 再新增 `index` 一条：**20 → 21**（M5 **过程值**，非里程碑收口值；
	// 终值 22 由 T-…-068 的 `eg bench` 补齐，见 M5 索引架构合同 §8.1。加法等式
	// 逐项复算见 internal/cli/cli_test.go 的 TestCommandCountTwentyOne）。M3 / M4 上游
	// 每一行说明逐字保留；本处只按实测重钉总数，两条判据一字未放宽。
	// M5 T-…-068 再新增 `bench` 一条：**21 → 22**（M5 **收口值**，即上一行预告的终值；
	// 加法等式 M3 期 18 + M4 期 2 + M5 期 2（index / bench）= 22，逐项复算见
	// internal/cli/cli_test.go 的 wantCommandCount = 22）。T-…-069 按实测把总数从过程值
	// 21 重钉到收口值 22 —— 数字变了是**事实变了**（T-…-068 已真实注册 `eg bench`），
	// 「全部非占位 + 全部已挂载」这两条判据依旧逐字不变、一格未放宽。
	if len(cmds) != 22 {
		t.Fatalf("命令数应为 22（S1 九命令 + M3 状态三命令 + S2 proposal + M3 delete / undelete + "+
			"mark-reviewed / unreviewed + edit + M4 reconcile / check + M5 index / bench），实际 %d", len(cmds))
	}
	for _, c := range cmds {
		if c.Placeholder {
			t.Fatalf("命令 %q 仍被标为占位（M2 判据 1 要求九命令全部具备真实实现）", c.Display)
		}
		if c.Handler == nil {
			t.Fatalf("命令 %q 未挂载实现（Handler == nil）", c.Display)
		}
	}
	// **T-…-044 重钉**：`rel remove` 的子命令级阶段占位已被接管，因此判据从「占位文案逐字冻结」
	// 反转为「占位零残留 + 子命令在册」——事实变了，覆盖面没减。
	var rel *cli.Command
	for _, c := range cmds {
		if c.Name == "rel" {
			rel = c
		}
	}
	if rel == nil {
		t.Fatal("命令注册表缺 rel")
	}
	var hasRemove bool
	for _, sub := range rel.Subs {
		if sub == "remove" {
			hasRemove = true
		}
	}
	if !hasRemove {
		t.Fatalf("rel 的子命令必须含 remove（真实写路径），实得 %v", rel.Subs)
	}
	if strings.Contains(rel.Usage, "未实现") {
		t.Fatalf("rel 的 --help 不得再宣告阶段未实现：%q", rel.Usage)
	}
}

// TestNoOutOfScopeImplementation：越界符号只以「登记形态」出现在非测试源里。
//
// 登记形态恰三类：① 注释行（阶段说明）；② 命中行自身带阶段标注（如 `--help` 文案里的
// 「不创建 …/.index/（S4）」）；③ 两处冻结的代码字面量——
// `internal/plan/schema.go` 的 `s2OpNames()`（归属未定的 op 清单，正是「不实现」的反证）与
// `internal/git/repo.go` 的 `GitignoreContent`（`.index/` 属 S4 目标态目录，S1 只预留排除）。
// 其余任何代码命中即判越界；命中文件本身还必须带阶段标注。
//
// **M3 重钉（T-…-037，事实变了，不是放宽）**：`remove_relation` 自本 task 起是**在场能力**
// （提案合同 §8.1 第 8 行 + owner 裁决 A-24），因此它从「越界符号」改判为**受限落地面**：
// 只允许出现在 `internal/plan/` / `internal/store/`（T-…-037 的 code_paths）与
// **T-…-044 具名追加**的 `internal/cli/rel_remove.go` / `internal/cli/rel.go` 两个文件，
// 出现在其余任何位置（cli 其余文件 / query / report / git / mdfile / model / rules / cmd）仍判越界。M4–M6 的 `.index/` / FTS5 / sqlite
// 三个符号的口径**一字不改**，仍只许登记形态。
func TestNoOutOfScopeImplementation(t *testing.T) {
	root := repoRootT029(t)
	pat := regexp.MustCompile(`remove_relation|\.index/|FTS5|sqlite`)
	// S4 索引面的三个符号单独一支：它们的落地面与 remove_relation 不同（见 m5Landed）。
	indexSymbolRE := regexp.MustCompile(`\.index/|FTS5|sqlite`)
	// m3Landed 是 remove_relation 唯一允许的实现落地面（前缀比对，等号由目录名给出）。
	//
	// **T-…-044 重钉（具名收窄，不是整体放宽）**：命令侧接管落地后，落地面新增**两个具名文件**
	// `internal/cli/rel_remove.go`（合成 op 的唯一命令层实现）与 `internal/cli/rel.go`
	// （只在 --help 文案与分发里出现该 op 名）。除这两个文件外，internal/cli 其余文件
	// 以及 query / report / git / mdfile / model / rules / cmd 各包仍判越界。
	m3Landed := []string{"internal/plan/", "internal/store/",
		"internal/cli/rel_remove.go", "internal/cli/rel.go"}
	// m5Landed 是 `.index/` / FTS5 / sqlite 三个符号唯一允许的实现落地面（M5 · T-…-065）。
	//
	// **重钉理由（事实变了，不是放宽）**：这三个符号原判「只许登记形态」，依据是「S4 索引面
	// 本仓任何阶段都不落地」——那是 M1–M4 时期的事实。T-…-064 冻结 M5 索引架构合同、
	// T-…-065 落地派生索引包之后，它们在 S4 落地面里是**合同要求存在**的实现。
	// 收窄手法与 T-…-044 的 `remove_relation` 逐字相同：落地面只有**一个包 + 一个具名文件**，
	// 出现在其余任何位置（query / report / plan / store / git / cli 其余文件 / cmd）仍判越界 ——
	// 这正是「索引不作为任何命令的前置」「读路径本阶段不接索引」两条边界的机器形态。
	//
	// **T-…-066 阶段 B 具名扩列（同一手法，不是整体放宽）**：本 task 按 Task `code_paths`
	// 新增**两个具名文件** —— `internal/cli/index_sync.go`（`eg index sync` 的唯一实现）与
	// `internal/cli/index_after_write.go`（六条写命令共用的写后同步 helper 的唯一落点）。
	// 除这三个具名文件 + `internal/index/` 一个包之外，其余任何位置出现这三个符号仍判越界：
	// 「读路径不接索引」（`search` / `card` / `rel` 各文件零命中）这条边界的机器形态一格未松。
	// **T-…-069 具名扩列一项（同一手法，不是整体放宽）**：新增第四个具名文件
	// `internal/cli/bench.go` —— `eg bench`（T-…-068，合同 §7.6 / A-46）的唯一实现。
	// 它命中的是 `BenchAuthorityNotice` 常量里那句**只读声明**（「权威 Markdown 与 .index/
	// 一个字节都不写」），即符号出现在此处恰恰是在向用户声明索引只读边界，属合同要求存在
	// 的实现形态。除这四个具名文件 + `internal/index/` 一个包之外，其余任何位置出现这三个
	// 符号仍判越界：「读路径不接索引」（`search.go` / `card.go` / `relation.go` 各文件对这三个
	// 符号零命中）这条边界的机器形态一格未松。
	m5Landed := []string{"internal/index/", "internal/cli/index.go",
		"internal/cli/index_sync.go", "internal/cli/index_after_write.go",
		"internal/cli/bench.go"}
	// fts5SqliteRE 单独识别 `FTS5` / `sqlite` 两个 **SQLite 专属**符号。
	// 它们的落地面**恒等于** m5Landed，绝不随 M6 放宽 —— 见 txnDotIndexLanded 的说明。
	fts5SqliteRE := regexp.MustCompile(`FTS5|sqlite`)
	// txnDotIndexLanded 是 **仅 `.index/` 一个符号** 在 M6 的额外落地面（M6 · T-…-071）。
	//
	// **重钉理由（事实变了，不是放宽）**：`.index/` 原本只许登记形态 + M5 索引落地面。
	// M6 的 T-…-070 冻结事务合同、T-…-071 落地 S5 事务包之后，事务日志与锁在
	// `.index/txn/`、`.index/run.lock` 下落盘，因此 `.index/` 这**一个**符号在 internal/txn/
	// 内是合同要求存在的实现形态。收窄手法与 T-…-065 的三符号逐字相同：额外落地面只有
	// **一个包**（internal/txn/），出现在其余任何位置仍判越界。
	//
	// **关键边界（绝不放宽）**：只有 `.index/` 一个符号获得这块额外落地面；
	// `FTS5` / `sqlite` **不在其内** —— S5 事务包是纯 Go 标准库实现、零 SQLite，
	// 那两个符号若出现在 txn（或除 m5Landed 外任何位置）仍判越界。因此下面的放宽条件
	// 显式要求「命中行含 `.index/` 且**不含** FTS5 / sqlite」，双保险。
	txnDotIndexLanded := []string{"internal/txn/"}
	stage := regexp.MustCompile(`S2|S3|S4|M3|M5|M6`)
	total := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			body := readFileT029(t, p)
			rel, _ := filepath.Rel(root, p)
			for i, line := range strings.Split(body, "\n") {
				if !pat.MatchString(line) {
					continue
				}
				total++
				if strings.Contains(line, "remove_relation") {
					landed := false
					for _, prefix := range m3Landed {
						if strings.HasPrefix(filepath.ToSlash(rel), prefix) {
							landed = true
						}
					}
					if landed {
						continue
					}
				}
				// S4 三符号（`.index/` / FTS5 / sqlite）在 M5 落地面内是实现形态，不判越界。
				if indexSymbolRE.MatchString(line) {
					landed := false
					for _, prefix := range m5Landed {
						if strings.HasPrefix(filepath.ToSlash(rel), prefix) {
							landed = true
						}
					}
					// M6 · T-…-071：**仅 `.index/`**（且命中行不含 FTS5 / sqlite）额外允许落在
					// S5 事务包 internal/txn/；FTS5 / sqlite 一格不放宽，txn 零 SQLite。
					if !landed && strings.Contains(line, ".index/") &&
						!fts5SqliteRE.MatchString(line) {
						for _, prefix := range txnDotIndexLanded {
							if strings.HasPrefix(filepath.ToSlash(rel), prefix) {
								landed = true
							}
						}
					}
					// system_assurance 批次 A · I-…-001：**行为判据**，不是具名豁免。
					//
					// 这里不再按文件名 / 路径放宽任何一格。判据只问两个可复算的事实
					// （实现见 index_symbol_judge_test.go，双侧反证见
					// `TestIndexSymbolJudgeIsBehavioralNotAllowlist`）：
					//   ① **依赖事实**：命中所在包的 import 闭包是否封闭在「无副作用能力」
					//      的标准库集合内 —— 不能碰文件系统、不能开数据库、不能起进程、
					//      不能 embed、不引用本仓任何 internal 包。这样的包在行为上
					//      **不可能**是 `.index/` / FTS5 / SQLite 的实现。
					//   ② **形态事实**：符号是否只作为字符串**字面量内容**出现（`go/scanner`
					//      逐 token 判定）；标识符 / 选择器 / 调用 / 注释一律判否。
					// 另加两条不放宽的硬边界：命中行含 `.index/` 恒判否（路径面只认落地面）；
					// 所在包内出现 `.index/` 恒判否（数据包不许给实现喂索引路径）。
					if !landed {
						if ok, _ := provablyNotIndexImplementation(root, filepath.ToSlash(rel), i+1, line); ok {
							landed = true
						}
					}
					if landed {
						continue
					}
				}
				switch {
				case strings.Contains(line, "//"):
				case stage.MatchString(line):
				case strings.Contains(line, `"replace_block"`), strings.Contains(line, `"mark_reviewed"`):
				case strings.Contains(line, `GitignoreContent = ".index/`):
				default:
					t.Fatalf("%s:%d 越界符号以实现形态出现：%s", rel, i+1, strings.TrimSpace(line))
				}
				if !stage.MatchString(body) {
					t.Fatalf("%s:%d 命中越界符号但文件无阶段标注", rel, i+1)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s：%v", dir, err)
		}
	}
	if total == 0 {
		t.Fatal("越界符号零命中：阶段登记注释疑被删除（应保留 M3/S2 与 S4 的登记）")
	}
}
