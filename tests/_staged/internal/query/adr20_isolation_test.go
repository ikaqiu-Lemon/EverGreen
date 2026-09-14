package query_test

// ADR-20 的**文件级隔离**机器判据（提案与状态合同 §6.1 ADR-20 硬约束行）。
//
// 「`updated_at > reviewed_at` 只允许出现在筛选条件里」这句话，只有变成**依赖关系事实**
// 才检得住：判定全部关在 internal/query/filter 单包单文件里，于是「谁用了这个信号」
// 等价于「谁的依赖闭包里有 github.com/ikaqiu-Lemon/EverGreen/internal/query/filter」，可用 go list -deps 反证。
//
// CI 依赖方向门禁属 M6，本用例只钉 M3 该保证的**事实**。

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// filterPkg 是被隔离的包路径（判定的唯一落点）。
const filterPkg = "github.com/ikaqiu-Lemon/EverGreen/internal/query/filter"

// forbiddenImporters 是 ADR-20 点名「不得导入 filter」的四个包。
//
// 合同按**目标形态**点名 internal/query/rank、internal/query/relations、
// internal/rules/converge、internal/query/review 四个包；当前代码里排序 / 关系正反查 /
// 综述取材还合并在 internal/query，收敛判定合并在 internal/rules，所以这里同时列出
// 「合同名」与「当前宿主」：包不存在时退到宿主断言，判据不会因为拆包与否而失效。
var forbiddenImporters = []struct {
	contract string // 合同点名的包路径（可能尚未拆出）
	host     string // 该逻辑当前所在的包路径（一定存在）
}{
	{"github.com/ikaqiu-Lemon/EverGreen/internal/query/rank", "github.com/ikaqiu-Lemon/EverGreen/internal/query"},
	{"github.com/ikaqiu-Lemon/EverGreen/internal/query/relations", "github.com/ikaqiu-Lemon/EverGreen/internal/query"},
	{"github.com/ikaqiu-Lemon/EverGreen/internal/query/review", "github.com/ikaqiu-Lemon/EverGreen/internal/query"},
	{"github.com/ikaqiu-Lemon/EverGreen/internal/rules/converge", "github.com/ikaqiu-Lemon/EverGreen/internal/rules"},
}

// TestADR20_FilterIsolation 三段断言：
//
//	① 四个包（或其当前宿主）的依赖闭包**不含** query/filter；
//	② filter 包自身不反向依赖 internal/query（隔离是双向的，否则闭包判据会被绕过）；
//	③ 包内非测试源文件恰是**具名白名单**两个：unreviewed.go（受限信号的唯一判定落点）
//	   与 visibility.go（§5.1 四象限真值表，只吃 status 与删除维度，不碰受限信号）；
//	   并逐字反证 reviewed_at 相关判定不曾散落到 visibility.go。
func TestADR20_FilterIsolation(t *testing.T) {
	root := repoRoot(t)
	// ① 依赖闭包反证。
	for _, p := range forbiddenImporters {
		pkg := p.contract
		if !pkgExists(t, root, pkg) {
			pkg = p.host
		}
		deps := goListDeps(t, root, pkg)
		if deps[filterPkg] {
			t.Fatalf("%s 的依赖闭包含 %s：ADR-20 要求该信号只出现在筛选条件里，"+
				"排序 / 关系分析 / 收敛判定 / 综述取材一律不得依赖它", pkg, filterPkg)
		}
	}
	// ② 反向依赖：filter 不得导入 internal/query（也不得导入 cli）。
	filterDeps := goListDeps(t, root, filterPkg)
	for _, banned := range []string{"github.com/ikaqiu-Lemon/EverGreen/internal/query", "github.com/ikaqiu-Lemon/EverGreen/internal/cli"} {
		if filterDeps[banned] {
			t.Fatalf("%s 反向依赖了 %s：隔离必须是单向的一个叶子包", filterPkg, banned)
		}
	}
	// ③ 判定只有一个落点文件。
	entries, err := os.ReadDir(filepath.Join(root, "internal", "query", "filter"))
	if err != nil {
		t.Fatalf("读 internal/query/filter 目录失败：%v", err)
	}
	var srcs []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			srcs = append(srcs, e.Name())
		}
	}
	sort.Strings(srcs)
	// 具名白名单（不做整体放宽）：新增文件必须在这里逐个具名登记，理由写在本用例注释里。
	wantSrcs := []string{"unreviewed.go", "visibility.go"}
	if !reflect.DeepEqual(srcs, wantSrcs) {
		t.Fatalf("internal/query/filter 的非测试源文件 = %v，期望恰 %v", srcs, wantSrcs)
	}
	// ④ 受限信号的判定仍然只在 unreviewed.go：visibility.go 不得出现 reviewed_at 这个键名。
	vis, err := os.ReadFile(filepath.Join(root, "internal", "query", "filter", "visibility.go"))
	if err != nil {
		t.Fatalf("读 visibility.go 失败：%v", err)
	}
	// 拼接构造键名，避免本断言自身在源码扫描类守卫里制造字面量命中。
	key := "reviewed" + "_at"
	if strings.Contains(string(vis), key) {
		t.Fatalf("visibility.go 出现 %s：ADR-20 要求该信号的判定只在 unreviewed.go 一个文件里", key)
	}
}

// repoRoot 定位仓库根（本文件在 internal/query 下，上溯两级）。
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

// pkgExists 判某个包路径当前是否存在（合同点名的包可能尚未从宿主里拆出）。
func pkgExists(t *testing.T, root string, pkg string) bool {
	t.Helper()
	cmd := exec.Command("go", "list", pkg)
	cmd.Dir = root
	return cmd.Run() == nil
}

// goListDeps 返回 pkg 的依赖闭包（只留本仓包，标准库无关）。
func goListDeps(t *testing.T, root string, pkg string) map[string]bool {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s 失败：%v", pkg, err)
	}
	deps := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "github.com/ikaqiu-Lemon/EverGreen/") {
			deps[line] = true
		}
	}
	return deps
}
