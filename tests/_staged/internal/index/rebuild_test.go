package index_test

// T-…-065 的机器判据（其四）：可恢复重建 + 零写权威。
//
// 判据来源：M5 索引架构合同 §4.3（不兼容 = 重建，永不迁移）、§4.4（rebuild 与 fresh build
// 在确定性口径下等价）、§8.1（`eg index build` 三分支语义）、P-1（Markdown 是唯一权威来源）。
//
// 「零写权威」在这里用**全库字节快照前后逐字比对**反证，而不是靠代码审查：
// 索引是派生物这件事，只有当「重建全程一个权威字节都没变」被机器证明时才算成立。

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
)

// TestRebuildEqualsFreshBuild 反证重建与全新构建**逻辑等价**（合同 §4.4）。
//
// 三种起点各测一遍：健康库、损坏库、缺失。三者重建后必须收敛到同一个 Digest ——
// 「旧库长什么样」不影响重建结果，因为重建根本不读旧库。
func TestRebuildEqualsFreshBuild(t *testing.T) {
	fresh := buildFixture(t, sampleSnapshot())
	want, err := index.Digest(fresh)
	if err != nil {
		t.Fatalf("Digest(fresh) 失败：%v", err)
	}

	cases := []struct {
		name    string
		prepare func(t *testing.T) string
	}{
		{
			name: "从健康库重建",
			prepare: func(t *testing.T) string {
				return buildFixture(t, sampleSnapshot())
			},
		},
		{
			name: "从版本不匹配的库重建",
			prepare: func(t *testing.T) string {
				dir := buildFixture(t, sampleSnapshot())
				execFixture(t, dir, `UPDATE `+index.TableIndexMeta+
					` SET value = '999' WHERE key = ?`, index.MetaSchemaVersion)
				return dir
			},
		},
		{
			name: "从截断的坏库重建",
			prepare: func(t *testing.T) string {
				dir := buildFixture(t, sampleSnapshot())
				if err := os.Truncate(filepath.Join(dir, index.DBFileName), 40); err != nil {
					t.Fatalf("截断失败：%v", err)
				}
				return dir
			},
		},
		{
			name: "从内容不同的旧库重建",
			prepare: func(t *testing.T) string {
				other := sampleSnapshot()
				other.Cards = other.Cards[:1]
				other.Relations = nil
				other.Files = other.Files[:1]
				dir := filepath.Join(t.TempDir(), index.DirName)
				if _, err := index.Build(dir, other, fixedOptions()); err != nil {
					t.Fatalf("建旧库失败：%v", err)
				}
				return dir
			},
		},
		{
			name: "索引缺失时重建",
			prepare: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), index.DirName)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := c.prepare(t)
			if _, err := index.Rebuild(dir, sampleSnapshot(), fixedOptions()); err != nil {
				t.Fatalf("Rebuild 失败：%v", err)
			}
			if diag := index.Inspect(dir); !diag.Usable() {
				t.Fatalf("重建后应 healthy，实际 = %s / %s", diag.Health, diag.Message)
			}
			got, err := index.Digest(dir)
			if err != nil {
				t.Fatalf("Digest 失败：%v", err)
			}
			if got != want {
				t.Fatalf("重建结果与 fresh build 不等价：\n实际 = %s\n期望 = %s", got, want)
			}
		})
	}
}

// TestRebuildRemovesPollution 反证重建会清掉整个 `.index/`（含外部混入的污染文件）：
// 重建的语义是「丢弃旧目录」，不是「就地覆盖 eg.db」。
func TestRebuildRemovesPollution(t *testing.T) {
	dir := buildFixture(t, sampleSnapshot())
	junk := filepath.Join(dir, "leftover.tmp")
	if err := os.WriteFile(junk, []byte("垃圾"), 0o644); err != nil {
		t.Fatalf("写污染文件失败：%v", err)
	}
	if diag := index.Inspect(dir); diag.Reason != index.ReasonUnexpectedFile {
		t.Fatalf("前置条件不成立：污染文件应先被判 unexpected_file，实际 = %s", diag.Reason)
	}
	if _, err := index.Rebuild(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("Rebuild 失败：%v", err)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Fatalf("重建后污染文件应已被清除，实际 stat = %v", err)
	}
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("重建后应 healthy，实际 = %s / %s", diag.Health, diag.Message)
	}
}

// TestRebuildNeverTouchesMarkdown 反证**零写权威**：构建与重建全程只动 `.index/`，
// vault 里其它文件的字节与集合逐字不变（P-1 / 合同 §13 禁令）。
//
// 断言口径刻意做成「全库快照 + 逐字比对」而不是抽查几个文件：只要有任何一次回写权威，
// 这条断言就当场失败 —— 这是 index 包最重要的一条守门线。
func TestRebuildNeverTouchesMarkdown(t *testing.T) {
	vault := t.TempDir()
	writeVaultFile(t, vault, "domains/ai/knowledge/k-alpha.md",
		"---\nid: k-alpha\nstatus: active\n---\n\n# Attention\n\nbody one\n")
	writeVaultFile(t, vault, "domains/ai/knowledge/k-beta.md",
		"---\nid: k-beta\nstatus: active\n---\n\n# 分词与索引\n\n这是正文 attention 机制\n")
	writeVaultFile(t, vault, "README.md", "# vault\n")

	before := snapshotTree(t, vault)

	dir := index.DirPath(vault)
	if _, err := index.Build(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("Build 失败：%v", err)
	}
	if _, err := index.Rebuild(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("Rebuild 失败：%v", err)
	}
	// 顺带把「损坏 → 可恢复重建」这一支也走一遍：它同样不得碰权威。
	if err := os.Truncate(filepath.Join(dir, index.DBFileName), 30); err != nil {
		t.Fatalf("截断失败：%v", err)
	}
	if _, action, _, err := index.EnsureBuilt(dir, sampleSnapshot(), fixedOptions()); err != nil {
		t.Fatalf("EnsureBuilt 失败：%v（action = %s）", err, action)
	}

	after := snapshotTree(t, vault)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("权威文件被改动了：\n之前 = %v\n之后 = %v", keysOf(before), keysOf(after))
	}
	// 反向自检：`.index/` 确实建出来了（否则「没改权威」是因为什么都没干）。
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("索引应已建好且 healthy，实际 = %s / %s", diag.Health, diag.Message)
	}
}

// TestEnsureBuiltActions 钉住 `eg index build` 的三分支语义（合同 §8.1）：
//
//	缺失 → built（全量构建）
//	healthy → noop（**一个字节都不写**）
//	损坏 → repaired（可恢复重建，且诊断留痕）
func TestEnsureBuiltActions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), index.DirName)

	// ① 缺失 → built
	res, action, diag, err := index.EnsureBuilt(dir, sampleSnapshot(), fixedOptions())
	if err != nil {
		t.Fatalf("首次 EnsureBuilt 失败：%v", err)
	}
	if action != index.ActionBuilt {
		t.Fatalf("缺失时 action 应为 built，实际 = %s", action)
	}
	if diag.Health != index.HealthMissing || diag.Code != index.CodeIndexMissing {
		t.Fatalf("应留痕 W23 missing，实际 = %s / %s", diag.Health, diag.Code)
	}
	if res == nil || res.CardCount != 3 {
		t.Fatalf("回执不对：%+v", res)
	}

	// ② healthy → noop：字节级不变（幂等，不做无谓重建）
	beforeBytes, err := os.ReadFile(filepath.Join(dir, index.DBFileName))
	if err != nil {
		t.Fatalf("读库失败：%v", err)
	}
	res2, action2, diag2, err := index.EnsureBuilt(dir, sampleSnapshot(), fixedOptions())
	if err != nil {
		t.Fatalf("第二次 EnsureBuilt 失败：%v", err)
	}
	if action2 != index.ActionNoop || res2 != nil {
		t.Fatalf("healthy 时应 noop 且无回执，实际 action = %s，res = %+v", action2, res2)
	}
	if !diag2.Usable() {
		t.Fatalf("第二次的诊断应为 healthy，实际 = %s", diag2.Health)
	}
	afterBytes, err := os.ReadFile(filepath.Join(dir, index.DBFileName))
	if err != nil {
		t.Fatalf("读库失败：%v", err)
	}
	if !reflect.DeepEqual(beforeBytes, afterBytes) {
		t.Fatalf("noop 竟改写了库文件（build 在 healthy 索引上必须零写）")
	}

	// ③ 损坏 → repaired：坏索引不是死局，且必须留痕 W24
	execFixture(t, dir, `DELETE FROM `+index.TableCards+` WHERE id = ?`, "k-beta")
	res3, action3, diag3, err := index.EnsureBuilt(dir, sampleSnapshot(), fixedOptions())
	if err != nil {
		t.Fatalf("损坏后 EnsureBuilt 失败：%v", err)
	}
	if action3 != index.ActionRepaired {
		t.Fatalf("损坏时 action 应为 repaired，实际 = %s", action3)
	}
	if diag3.Code != index.CodeIndexCorrupt || diag3.Reason != index.ReasonWatermarkSelfContradiction {
		t.Fatalf("应留痕 W24/watermark_self_contradiction，实际 = %s / %s", diag3.Code, diag3.Reason)
	}
	if res3 == nil || res3.CardCount != 3 {
		t.Fatalf("修复后回执不对：%+v", res3)
	}
	if diag := index.Inspect(dir); !diag.Usable() {
		t.Fatalf("修复后应 healthy，实际 = %s / %s", diag.Health, diag.Message)
	}
	// 修复结果同样必须与 fresh build 等价。
	fresh := buildFixture(t, sampleSnapshot())
	want, err := index.Digest(fresh)
	if err != nil {
		t.Fatalf("Digest(fresh) 失败：%v", err)
	}
	got, err := index.Digest(dir)
	if err != nil {
		t.Fatalf("Digest 失败：%v", err)
	}
	if got != want {
		t.Fatalf("修复结果与 fresh build 不等价：%s vs %s", got, want)
	}
}

// TestActionsClosed 钉住动作集合恰 4 值（进 --json 的 data.action，下游按此断言）。
func TestActionsClosed(t *testing.T) {
	want := []string{"built", "noop", "repaired", "rebuilt"}
	if got := index.Actions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Actions() = %v，期望 %v", got, want)
	}
}

// —— 测试脚手架 ——

// writeVaultFile 在 vault 里写一个权威文件（脚手架：模拟真实 Markdown 库）。
func writeVaultFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

// snapshotTree 把 vault 里**除 `.index/` 之外**的全部文件抓成 path → 内容 的快照。
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		if info.IsDir() {
			if rel == index.DirName {
				return filepath.SkipDir // 派生目录本就该变，不进快照
			}
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("抓取 vault 快照失败：%v", err)
	}
	return out
}

// keysOf 返回快照的文件名清单（升序），只用于失败时的可读输出。
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
