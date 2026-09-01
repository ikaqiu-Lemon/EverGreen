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

	"evergreen/internal/cli"
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
	e2e := filepath.Join(root, "test", "e2e")
	script := filepath.Join(e2e, "m2_acceptance.sh")
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
		if _, err := os.Stat(filepath.Join(e2e, s)); err != nil {
			t.Fatalf("子脚本缺失 %s：%v", s, err)
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
	st, err := os.Stat(filepath.Join(root, "test", "e2e", "m1_real_article.sh"))
	if err != nil || st.Size() == 0 {
		t.Fatalf("m1_real_article.sh 缺失或为空：%v", err)
	}
}

// TestS1NineCommandsAreImplemented：九命令均非占位且均已挂载 Handler（判据 1 的机器可判部分）。
func TestS1NineCommandsAreImplemented(t *testing.T) {
	cmds := cli.New().Commands()
	if len(cmds) != 9 {
		t.Fatalf("S1 命令数应为 9，实际 %d", len(cmds))
	}
	for _, c := range cmds {
		if c.Placeholder {
			t.Fatalf("命令 %q 仍被标为占位（M2 判据 1 要求九命令全部具备真实实现）", c.Display)
		}
		if c.Handler == nil {
			t.Fatalf("命令 %q 未挂载实现（Handler == nil）", c.Display)
		}
	}
	// `rel remove` 的 M3/S2 占位是**子命令级**的，且文案逐字冻结。
	if !strings.Contains(cli.RemovePlaceholderNotice, "M3/S2 未实现") {
		t.Fatalf("rel remove 占位文案不再是 M3/S2 口径：%q", cli.RemovePlaceholderNotice)
	}
}

// TestNoOutOfScopeImplementation：越界符号只以「登记形态」出现在非测试源里。
//
// 登记形态恰三类：① 注释行（阶段说明）；② 命中行自身带阶段标注（如 `--help` 文案里的
// 「不创建 …/.index/（S4）」）；③ 两处冻结的代码字面量——
// `internal/plan/schema.go` 的 `s2OpNames()`（S2+ op 清单，正是「不实现」的反证）与
// `internal/git/repo.go` 的 `GitignoreContent`（`.index/` 属 S4 目标态目录，S1 只预留排除）。
// 其余任何代码命中即判越界；命中文件本身还必须带阶段标注。
func TestNoOutOfScopeImplementation(t *testing.T) {
	root := repoRootT029(t)
	pat := regexp.MustCompile(`remove_relation|\.index/|FTS5|sqlite`)
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
