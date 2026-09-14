package model

// `stale` / `stale_reason` 两个键名常量与封闭三值的单测
// （对账合同 §9；裁决 A-33 / A-34；T-evergreen.s1_main_flow-158614-055 阶段 1）。
//
// 三条判据，都是合同硬约束而非行为复述：
//   - **键名字面量只许一处**：internal/ 全库（除本文件与常量声明处）不得再手写这两个字符串，
//     否则「唯一写入路径」这条判据会因为某处手写字符串而失真；
//   - **取值封闭恰三值**：第四种取值一律解析失败（本层不猜、不归一化、不代入默认理由）；
//   - **顺序即判定**：ValidStaleReasons() 的声明顺序就是合同 §9「多因并存取第一个命中值」
//     的那个顺序，调用方不得重排（本层给顺序，判定归 R6 检查器）。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestStaleKeysAndClosedReasons：两个键名常量逐字正确 + 封闭三值逐字与顺序正确。
func TestStaleKeysAndClosedReasons(t *testing.T) {
	if FMKeyStale != "stale" {
		t.Fatalf("FMKeyStale = %q，合同 §9 逐字为 stale", FMKeyStale)
	}
	if FMKeyStaleReason != "stale_reason" {
		t.Fatalf("FMKeyStaleReason = %q，合同 §9 逐字为 stale_reason", FMKeyStaleReason)
	}
	want := []StaleReason{"引用卡已更新", "引用卡已逻辑删除", "引用卡已失效"}
	got := ValidStaleReasons()
	if len(got) != 3 {
		t.Fatalf("stale_reason 必须恰三值，实得 %d：%v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个取值 = %q，期望 %q（顺序即合同 §9 首个命中顺序，不得重排）",
				i+1, got[i], want[i])
		}
	}
	if StaleReasonUpdated != want[0] || StaleReasonDeleted != want[1] || StaleReasonDeprecated != want[2] {
		t.Fatalf("三个具名常量与合同取值不一致：%q / %q / %q",
			StaleReasonUpdated, StaleReasonDeleted, StaleReasonDeprecated)
	}
}

// TestParseStaleReason_RejectsFourthValue：第四种取值（含空、近义词、带空白、英文别名）
// 一律报错且返回空值——本层绝不归一化、绝不代入默认理由。
func TestParseStaleReason_RejectsFourthValue(t *testing.T) {
	for _, ok := range ValidStaleReasons() {
		got, err := ParseStaleReason(string(ok))
		if err != nil || got != ok {
			t.Fatalf("合法取值 %q 必须解析成功：%q / %v", ok, got, err)
		}
		if !ok.Valid() {
			t.Fatalf("%q 必须 Valid", ok)
		}
	}
	for _, bad := range []string{"", " ", "引用卡已变更", "引用卡已更新 ", " 引用卡已更新",
		"引用卡已更新\n", "stale", "card_updated", "引用卡已失效。"} {
		got, err := ParseStaleReason(bad)
		if err == nil {
			t.Fatalf("非法取值 %q 必须解析失败（封闭三值）", bad)
		}
		if got != "" {
			t.Fatalf("解析失败时必须返回空值，不得代入默认理由，实得 %q", got)
		}
		if StaleReason(bad).Valid() {
			t.Fatalf("%q 不得判为 Valid", bad)
		}
	}
}

// TestStaleKeyLiteralsAppearOnce：两个键名的**字面量**在 internal/ 非测试代码里
// 只允许出现在**具名语义所有者**的常量声明处（照 FMKeyReviewedAt 体例的加严面）。
//
// **M5 · T-…-069 纠正轮的阶段化重钉（判据加严，不放宽）**
//
// 原判据：`"stale"` 在 internal/ 非测试代码里**恰 1 次**，且只许在
// internal/model/frontmatter.go —— 这在 M3 期是对的，因为那时全仓只有一个 `stale` 概念。
// M5 落地 `internal/index` 后出现了**第二个、语义无关**的同形词：
//
//	internal/model/frontmatter.go  FMKeyStale      知识卡 frontmatter 键名（R6「综述可能失准」标记）
//	internal/index/consistency.go  FreshnessStale  索引新鲜度令牌（合同 §5.2 三态之一）
//
// 两者只是**恰好都写作 `stale`**：一个是入盘数据的键名（改名要迁移数据、要历史兼容），
// 一个是索引域的对外字符串（改名只影响 `eg index status` 的一格）。曾经为了满足「恰 1 次」
// 把后者写成 `Freshness(model.FMKeyStale)`，那是**跨领域假共享**（耦合，不是复用）：
// 任何一侧改名都会静默污染另一侧。owner 裁决解除该耦合，两侧各自独立声明。
//
// 因此本判据从「单一所有者恰 1 次」重钉为「**封闭的两个具名所有者，逐个恰 1 次**」：
//
//	① 每个字面量在 internal/ 非测试代码里的**总出现次数**恰等于其所有者条数（`stale` = 2、`stale_reason` = 1）；
//	② 逐个所有者文件**各自恰 1 次**（不允许某个所有者写 2 次而另一个写 0 次来凑总数）；
//	③ 所有者名单**之外**的任何文件里出现即判红（这一格与原判据完全等强）；
//	④ 每个所有者那一行必须是**该具名常量的声明**（`FMKeyStale` / `FreshnessStale` 在场），
//	   杜绝「随便某处写个 "stale" 也算所有者」。
//
// 一句话：所有者名单是**封闭枚举 + 精确计数**，不是「允许出现任意多处」。
func TestStaleKeyLiteralsAppearOnce(t *testing.T) {
	// 字面量拼接构造：本用例自身不给出完整字符串，否则扫描会命中测试代码之外的假阳性口径。
	staleLit, reasonLit := `"`+"sta"+`le"`, `"`+"stale_rea"+`son"`
	// 封闭的所有者表：字面量 → （所有者文件相对 internal/ 的路径 → 必须同现的具名常量）。
	owners := map[string]map[string]string{
		staleLit: {
			"model/frontmatter.go": "FMKeyStale",     // frontmatter 键名语义
			"index/consistency.go": "FreshnessStale", // 索引新鲜度令牌语义
		},
		reasonLit: {
			"model/frontmatter.go": "FMKeyStaleReason",
		},
	}
	root := ".."
	total := map[string]int{}
	perFile := map[string]map[string]int{staleLit: {}, reasonLit: {}}
	declLine := map[string]map[string]string{staleLit: {}, reasonLit: {}}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.ToSlash(path), "../"))
		for lit := range owners {
			n := strings.Count(string(raw), lit)
			if n == 0 {
				continue
			}
			total[lit] += n
			perFile[lit][rel] += n
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, lit) {
					declLine[lit][rel] = line
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/：%v", err)
	}
	for lit, table := range owners {
		// ① 总数恰等于所有者条数。
		if total[lit] != len(table) {
			t.Fatalf("键名字面量 %s 在 internal/ 非测试代码里共出现 %d 次，必须恰 %d 次"+
				"（封闭所有者：%v）", lit, total[lit], len(table), keysOf(table))
		}
		// ②③ 逐个所有者恰 1 次；名单外零命中。
		for rel, n := range perFile[lit] {
			if _, ok := table[rel]; !ok {
				t.Fatalf("键名字面量 %s 出现在所有者名单之外的 %s（%d 次）："+
					"要用请引用具名常量，不要手写字符串", lit, rel, n)
			}
			if n != 1 {
				t.Fatalf("所有者 %s 里 %s 出现 %d 次，必须恰 1 次（声明处唯一）", rel, lit, n)
			}
		}
		for rel, constName := range table {
			if perFile[lit][rel] != 1 {
				t.Fatalf("所有者 %s 必须恰有 1 处 %s 声明，实得 %d 次："+
					"名单登记了就必须真在场（否则名单造假）", rel, lit, perFile[lit][rel])
			}
			// ④ 那一行必须是该具名常量的声明。
			if !strings.Contains(declLine[lit][rel], constName) {
				t.Fatalf("%s 里 %s 所在行未声明具名常量 %s：%q",
					rel, lit, constName, declLine[lit][rel])
			}
		}
	}
}

// keysOf 只为报错信息给出确定序的所有者清单。
func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
