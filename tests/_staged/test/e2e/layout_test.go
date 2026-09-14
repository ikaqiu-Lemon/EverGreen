package e2e

// 迁移布局解析（system_assurance · T-…-003）。
//
// 历史事实：M1–M6 期的 shell 端到端脚本住在 `test/e2e/m<N>_*.sh`。全系统测试体系把它们
// 按系统能力搬到 `tests/e2e/<capability>/*.sh`，把阶段聚合器搬到
// `tests/archive/history/stage-acceptance/`。本文件让**元测试**（断言「脚本在场 / op 有落点 /
// 用例名不许消失」的那几支）从**权威迁移表** `tests/manifest/migration.tsv` 解析新路径，
// 而不是在 Go 侧再抄一份硬编码映射 —— 表和盘任一侧漂移都会立刻红。
//
// 注意：这里只解析**路径**，不改动任何断言口径。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

var (
	migOnce   sync.Once
	migMap    map[string]string // old_path -> new_path（decision ∈ {keep, archive}）
	migOrigin map[string]string // old_path -> stage_origin
	migErr    error
)

// loadMigration 读取 <root>/tests/manifest/migration.tsv。
func loadMigration(root string) (map[string]string, error) {
	migOnce.Do(func() {
		raw, err := os.ReadFile(filepath.Join(root, "tests", "manifest", "migration.tsv"))
		if err != nil {
			migErr = err
			return
		}
		m := map[string]string{}
		origin := map[string]string{}
		lines := strings.Split(string(raw), "\n")
		for i, ln := range lines {
			if i == 0 || strings.TrimSpace(ln) == "" {
				continue
			}
			f := strings.Split(ln, "\t")
			if len(f) < 3 {
				continue
			}
			old, neu, decision := f[0], f[1], f[2]
			if len(f) >= 8 {
				origin[old] = f[7]
			}
			if decision == "keep" || decision == "archive" {
				// 同一旧路径可能既有 merge 行又有 archive 行；只登记落盘的那一行。
				if _, dup := m[old]; !dup || decision == "keep" {
					m[old] = neu
				}
			}
		}
		migMap = m
		migOrigin = origin
	})
	return migMap, migErr
}

// migratedRel 把一个历史相对路径（如 test/e2e/m2_search.sh）解析成迁移后的相对路径。
func migratedRel(t *testing.T, root, oldRel string) string {
	t.Helper()
	m, err := loadMigration(root)
	if err != nil {
		t.Fatalf("读取 tests/manifest/migration.tsv：%v", err)
	}
	neu, ok := m[oldRel]
	if !ok {
		t.Fatalf("迁移表没有登记历史资产 %q：迁移表与盘面漂移", oldRel)
	}
	return neu
}

// migratedAbs 返回迁移后文件的绝对路径。
func migratedAbs(t *testing.T, root, oldRel string) string {
	t.Helper()
	return filepath.Join(root, migratedRel(t, root, oldRel))
}

// historicalStageScripts 返回某个历史阶段（m1…m6）在 test/e2e 下的 shell 脚本**历史文件名**，
// 并顺带校验它们迁移后仍在盘。用于「某阶段脚本确实在场」这类反证的事实基础。
func historicalStageScripts(t *testing.T, root, stage string) []string {
	t.Helper()
	m, err := loadMigration(root)
	if err != nil {
		t.Fatalf("读取迁移表：%v", err)
	}
	var out []string
	for old, neu := range m {
		if !strings.HasPrefix(old, "test/e2e/") || !strings.HasSuffix(old, ".sh") {
			continue
		}
		if migOrigin[old] != stage {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, neu)); err != nil {
			t.Fatalf("阶段 %s 的历史脚本 %s 迁移后不在盘（%s）：%v", stage, old, neu, err)
		}
		out = append(out, filepath.Base(old))
	}
	sort.Strings(out)
	return out
}
