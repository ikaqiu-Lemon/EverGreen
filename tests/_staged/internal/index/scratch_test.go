package index

// scratch_test.go：一次性派生目录（scratch）的机器反证（T-…-068 纠正轮）。
//
// 这里要证的**不是**「NewScratch 能建目录」这种平凡事实，而是三条**安全性质**：
//
//	① 调用方无从指定删除目标 —— 本包任何导出函数都不会把自己的 string 形参喂给递归删除；
//	② prefix 不是路径 —— 含路径分隔符时直接失败，且不留下任何目录（无法目录穿越）；
//	③ 一次性副本确实会被清理 —— 哪怕里面是一棵非空的多层目录树（真实副本就是这样）。
//
// ① 是本轮纠正的核心：旧的 RemoveScratch(dir) 让「删的只可能是派生物」变成注释承诺，
// 而 U-02 的派生物例外正是靠这条性质成立的。现在它由 AST 反证钉住。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// —— ① 调用方无从指定删除目标（AST 级反证）——

// TestDeletionTargetTakingAPIsAreExactlyTheIndexDirPair 把「哪些导出函数会递归删除
// 调用方传进来的路径」这件事**封闭成空集**（M5 时它是二元的：`Build` 与 `Rebuild`）。
//
// 为什么曾经是那两个：它们是 `.index/` 目录本身的生命周期 API（`Rebuild` 的语义逐字
// 就是「先删掉整个 dir 再全量构建」），其安全性由 U-02 的**正面反证③**兜住 ——
// 本包非测试源里不出现任何权威产物根字面量、也不做路径上溯。
//
// 为什么现在是空集（T-…-072 批次 B2b2）：M6 把运行时协作面（`run.lock` / `txn/`）
// 搬进了同一个目录，`os.RemoveAll(.index)` 会连同互斥锁的 inode 与崩溃恢复所需的
// 事务日志一起抹掉，那条老实现在 M6 语境下**是错的**。两条删除策略因此被改写成
// 逐项、按判据的形态，且都收进了包内私有函数：
//
//	Rebuild    → purgeNonReserved      凭名册删（只留 run.lock / txn），
//	                                   递归面只够得到「某个具名的非保留子条目」；
//	Build 失败 → cleanupBuildAttempt   凭因果删（只回收本次调用的产物），全程非递归。
//
// 调用方连这两个函数的存在都感知不到，更无从把一个路径喂进去 ——「调用方能指定递归
// 删除目标」这个危险面在本包已彻底消失。白名单清空不是放宽，是历史上最强的一次收紧。
//
// 本用例真正防的是**集合增长**：任何**新增**的「让调用方指定递归删除目标」的导出 API
// 都会让它转红。历史上被删掉的 `RemoveScratch(dir string)`、以及本轮被替换掉的
// `Rebuild` 老实现，都是这种形态 —— 它们一旦回来，这里立刻判红，
// 而不是靠注释承诺「调用方必须只传临时目录」。
func TestDeletionTargetTakingAPIsAreExactlyTheIndexDirPair(t *testing.T) {
	// 封闭白名单：**空集**。本包不再有任何导出函数把自己的路径形参交给递归删除。
	allowed := map[string]bool{}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读不到包目录：%v", err)
	}
	var checked int
	got := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("解析 %s 失败：%v", name, perr)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !fn.Name.IsExported() {
				continue
			}
			checked++
			// 收集该导出函数的全部 string 形参名。
			params := map[string]bool{}
			for _, field := range fn.Type.Params.List {
				id, isIdent := field.Type.(*ast.Ident)
				if !isIdent || id.Name != "string" {
					continue
				}
				for _, n := range field.Names {
					if n.Name != "_" {
						params[n.Name] = true
					}
				}
			}
			if len(params) == 0 {
				continue
			}
			// 在函数体里找「递归删除(形参)」：命中即说明调用方能指定删除目标。
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall || !isRecursiveRemove(call) || len(call.Args) != 1 {
					return true
				}
				for _, ref := range identsIn(call.Args[0]) {
					if params[ref] {
						got[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("一个导出函数都没扫到：本用例失去意义（AST 遍历写错了）")
	}
	for name := range got {
		if !allowed[name] {
			t.Fatalf("导出函数 %s 让调用方指定了递归删除目标 —— 这是**新增**的危险面："+
				"U-02 派生物例外会退化为注释承诺。一次性目录请改用 NewScratch（由本包自建 dir 并封装清理）", name)
		}
	}
	// 反向也要成立：白名单不得因为函数被改名/删除而空挂（否则本用例会静默失效）。
	for name := range allowed {
		if !got[name] {
			t.Fatalf("白名单里的 %s 已不再是「删调用方传入的 dir」形态："+
				"请同步收窄白名单，别让本用例留下空挂条目", name)
		}
	}
}

// TestNewScratchNeverDeletesItsParameter 单点反证 scratch API 自身：
// `NewScratch` 的形参（prefix）**不会**被交给递归删除 —— 它的删除目标只能来自
// 函数体内 `os.MkdirTemp` 的返回值，因此调用方无从指定删除什么。
func TestNewScratchNeverDeletesItsParameter(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "scratch.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 scratch.go 失败：%v", err)
	}
	var fn *ast.FuncDecl
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "NewScratch" {
			fn = d
		}
	}
	if fn == nil {
		t.Fatal("scratch.go 里找不到 NewScratch")
	}
	// ① 形参恰一个，且是 string（prefix）——没有任何「路径」参数位。
	if n := fn.Type.Params.NumFields(); n != 1 {
		t.Fatalf("NewScratch 形参组 = %d，期望恰 1（只收目录名前缀，不收路径）", n)
	}
	params := map[string]bool{}
	for _, field := range fn.Type.Params.List {
		for _, nm := range field.Names {
			params[nm.Name] = true
		}
	}
	// ② 函数体里的递归删除，参数**不是**形参。
	var sawRemove bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isRecursiveRemove(call) || len(call.Args) != 1 {
			return true
		}
		sawRemove = true
		for _, ref := range identsIn(call.Args[0]) {
			if params[ref] {
				t.Fatalf("NewScratch 把形参 %q 交给了递归删除：调用方能指定删除目标", ref)
			}
		}
		return true
	})
	if !sawRemove {
		t.Fatal("NewScratch 里没有递归删除：cleanup 不可能真的清理临时副本")
	}
	// ③ 删除目标必须来自 os.MkdirTemp —— 结构性地「只可能是本包刚造的目录」。
	var sawMkdirTemp bool
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel {
			if pkg, isID := sel.X.(*ast.Ident); isID && pkg.Name == "os" && sel.Sel.Name == "MkdirTemp" {
				sawMkdirTemp = true
			}
		}
		return true
	})
	if !sawMkdirTemp {
		t.Fatal("NewScratch 未用 os.MkdirTemp 自建目录：删除目标的来源不再可证")
	}
}

// isRecursiveRemove 判断一个调用是否是**递归删除**（整棵子树消失的那一类调用）。
//
// 覆盖两种形态，缺一不可：
//
//	os 包的整树删除                —— 标准库形态（选择器名**分片拼接**，避免本测试文件
//	                                 自己成为 U-02 词法门禁的命中项）；
//	本包私有的 removeTree(root)    —— B2c2 之后 rebuild.go 用它替代标准库整树删除
//	                                 （见 rebuild.go 文件头）。只认标准库形态会让门禁
//	                                 在实现换了个名字之后静默失效。
func isRecursiveRemove(call *ast.CallExpr) bool {
	if id, ok := call.Fun.(*ast.Ident); ok {
		return id.Name == "removeTree"
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "os" {
		return false
	}
	return sel.Sel.Name == "Remove"+"All"
}

// identsIn 收集表达式里出现的全部标识符名（覆盖 dir、filepath.Join(dir, x) 这类构造）。
func identsIn(expr ast.Expr) []string {
	var out []string
	ast.Inspect(expr, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			out = append(out, id.Name)
		}
		return true
	})
	return out
}

// —— ② prefix 不是路径：无法目录穿越 ——

// TestNewScratchPrefixIsNotAPath 断言含路径分隔符的 prefix 一律失败，
// 且失败时**不返回** cleanup（拿不到一个指向别处的删除动作）。
func TestNewScratchPrefixIsNotAPath(t *testing.T) {
	for _, bad := range []string{
		"a" + string(os.PathSeparator) + "b",
		string(os.PathSeparator) + "tmp",
		"x" + string(os.PathSeparator),
	} {
		dir, cleanup, err := NewScratch(bad)
		if err == nil {
			if cleanup != nil {
				_ = cleanup()
			}
			t.Fatalf("prefix %q 含路径分隔符却成功了：prefix 被当成路径用，存在穿越面", bad)
		}
		if dir != "" {
			t.Fatalf("prefix %q 失败却仍返回 dir=%q", bad, dir)
		}
		if cleanup != nil {
			t.Fatalf("prefix %q 失败却仍返回了 cleanup：调用方可能拿到指向别处的删除动作", bad)
		}
	}
}

// TestNewScratchDirIsFreshAndUnderTempRoot 断言拿到的是一个**本包新造的、空的**目录，
// 且位于系统临时根下 —— 它不可能是调用方已有的权威目录。
func TestNewScratchDirIsFreshAndUnderTempRoot(t *testing.T) {
	dir, cleanup, err := NewScratch("eg-scratch-fresh-")
	if err != nil {
		t.Fatalf("NewScratch 失败：%v", err)
	}
	defer func() { _ = cleanup() }()

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("dir %q 不是目录：err=%v", dir, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读 dir 失败：%v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("新建的一次性目录里有 %d 个条目，期望恰 0（必须是全新空目录）", len(entries))
	}
	// 位于系统临时根下：与任何 vault 根、任何权威目录都不相交。
	root, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("解析临时根失败：%v", err)
	}
	got, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("解析 dir 失败：%v", err)
	}
	if !strings.HasPrefix(got, root+string(os.PathSeparator)) {
		t.Fatalf("dir %q 不在系统临时根 %q 之下", got, root)
	}
	// 两次调用互不复用：各自拿到独立目录，cleanup 也各自独立。
	dir2, cleanup2, err := NewScratch("eg-scratch-fresh-")
	if err != nil {
		t.Fatalf("第二次 NewScratch 失败：%v", err)
	}
	defer func() { _ = cleanup2() }()
	if dir2 == dir {
		t.Fatalf("两次 NewScratch 返回同一个目录 %q：一次性目录必须互不复用", dir)
	}
}

// —— ③ 一次性副本确实会被清理（非空多层目录树）——

// TestNewScratchCleanupRemovesNonEmptyTree 反证「临时副本仍会被清理」：
// 在 scratch 里铺一棵非空多层目录树（真实的 bench vault 副本就是这个形状），
// cleanup 之后整棵树必须消失；再调一次 cleanup 必须幂等成功。
func TestNewScratchCleanupRemovesNonEmptyTree(t *testing.T) {
	dir, cleanup, err := NewScratch("eg-scratch-tree-")
	if err != nil {
		t.Fatalf("NewScratch 失败：%v", err)
	}

	// 多层嵌套 + 文件，模拟一份 vault 副本（含只读文件，确保清理不被文件模式挡住）。
	deep := filepath.Join(dir, "lvl1", "lvl2", "lvl3")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("建嵌套目录失败：%v", err)
	}
	for _, p := range []string{
		filepath.Join(dir, "top.md"),
		filepath.Join(dir, "lvl1", "mid.md"),
		filepath.Join(deep, "leaf.md"),
	} {
		if err := os.WriteFile(p, []byte("payload\n"), 0o644); err != nil {
			t.Fatalf("写 %s 失败：%v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(deep, "leaf.md")); err != nil {
		t.Fatalf("前置不成立，叶子文件没写进去：%v", err)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup 失败：%v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("cleanup 之后 dir %q 仍存在（err=%v）：一次性副本没被清理", dir, err)
	}

	// 幂等：目录已不存在时再清一次，不得报错。
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup 第二次调用应幂等成功，实际 err=%v", err)
	}
}

// TestNewScratchCleanupIsBoundToItsOwnDir 反证 cleanup 是**绑定**在它自己的目录上的：
// 两个 scratch 各自 cleanup，互不影响对方 —— 说明 closure 里的删除目标不可被外部改写。
func TestNewScratchCleanupIsBoundToItsOwnDir(t *testing.T) {
	dirA, cleanupA, err := NewScratch("eg-scratch-a-")
	if err != nil {
		t.Fatalf("NewScratch A 失败：%v", err)
	}
	dirB, cleanupB, err := NewScratch("eg-scratch-b-")
	if err != nil {
		t.Fatalf("NewScratch B 失败：%v", err)
	}
	defer func() { _ = cleanupB() }()

	if err := os.WriteFile(filepath.Join(dirB, "keep.md"), []byte("keep\n"), 0o644); err != nil {
		t.Fatalf("写 B 失败：%v", err)
	}

	if err := cleanupA(); err != nil {
		t.Fatalf("cleanupA 失败：%v", err)
	}
	if _, err := os.Stat(dirA); !os.IsNotExist(err) {
		t.Fatalf("A 应已被删除，实际仍存在")
	}
	// B 一格未动：cleanupA 只能删 A。
	if _, err := os.Stat(filepath.Join(dirB, "keep.md")); err != nil {
		t.Fatalf("cleanupA 波及了 B（B 的文件不见了）：%v", err)
	}
}
