// S4 索引符号（`.index/` / `FTS5` / `sqlite`）越界判据的**行为判据**实现与双侧反证
// （system_assurance 批次 A · I-evergreen.system_assurance-158614-001）。
//
// # 为什么需要本文件
//
// `TestNoOutOfScopeImplementation`（m2_acceptance_test.go）原先只有一种放宽手法：
// **按路径前缀具名扩列落地面**（m5Landed / txnDotIndexLanded）。测试树外置把
// `test/perf/queryset`（被 `internal/cli/bench.go` import 的固定查询集数据包）归位到
// `internal/query/queryset` 之后，它的关键词字面量 `"sqlite"` 落进了扫描面。
// 「再具名豁免一个文件」仍然是 allowlist —— 用名字换绿，判据本身没变强。
//
// 本文件改成**对实际依赖与实际出现形态的判定**，与文件名、包名、路径一律无关：
//
//	一个包若其 import 闭包**只含无副作用能力的标准库**（不能碰文件系统、不能开数据库、
//	不能起进程、不能 embed、不引用本仓任何 internal 包），则它在行为上**不可能**是
//	`.index/` 目录、FTS5 虚拟表或 SQLite 驱动的实现；此时若符号又**只以字符串字面量的
//	内容**出现（不是标识符 / 选择器 / 类型 / 调用 / 注释），它就是**数据**，不是实现。
//
// 两条判据都是可机器复算的事实：
//
//   - 依赖事实：`go/parser`（ImportsOnly）读该包目录下所有非测试源的 import 集合；
//   - 形态事实：`go/scanner` 在命中行上逐 token 判定，符号只许出现在 `token.STRING` 里。
//
// # 一格都不放宽的部分
//
//   - **`.index/` 是路径 token**：只要命中行含 `.index/`，本判据**恒判否**。
//     索引目录的落地面仍然只有 m5Landed + internal/txn/（路径面不接受行为豁免）。
//   - **整个包不得出现 `.index/`**：纯数据包也不许给实现喂索引路径。
//   - 标识符 / 选择器形态（`sqlite.Open`、`func sqliteBackend()`）恒判否。
//   - import path 字面量（`"modernc.org/sqlite"`）虽是 STRING，但会被依赖判据先否掉。
//
// # 双侧反证
//
// `TestIndexSymbolJudgeIsBehavioralNotAllowlist` 对判据本身做双侧反证：
// 一侧构造 8 组合成包，逐组断言「该接受的接受、该拒绝的拒绝」（改依赖、改形态、
// 夹带索引路径任一项，判据立刻翻红）；另一侧在**真仓**上跑 —— 真实索引实现
// （`internal/index/`）的每一处命中必须被拒绝，真实纯数据包的命中必须被接受。
package e2e

import (
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// judgeIndexSymbolRE 是 S4 索引面三个符号（与 m2_acceptance_test.go 的口径逐字一致）。
var judgeIndexSymbolRE = regexp.MustCompile(`\.index/|FTS5|sqlite`)

// judgeIndexPathToken 是**路径形态**的索引 token：它永远不接受行为豁免。
const judgeIndexPathToken = ".index/"

// pureComputationStdlib 是「无副作用能力」的标准库集合。
//
// 集合口径（不是白名单豁免，而是能力封闭性）：这些包**都不提供**文件系统写入、
// 数据库驱动、子进程、网络、embed、时钟与随机源。因此 import 闭包落在本集合内的包，
// 其可观察行为只有「纯计算 + 返回数据」一种。
//
// 刻意不在集合内（各自都会让「不可能是索引实现」这句话失效）：
// `os` / `io` / `io/fs` / `path` / `path/filepath` / `embed`（文件面）、
// `database/sql`（驱动面）、`os/exec` / `syscall`（进程面）、`net*`（网络面）、
// `time`（时钟）、`math/rand`（随机）、`unsafe`（越过类型系统）、
// 以及本仓任何 `evergreen/...` 包与任何第三方模块路径。
var pureComputationStdlib = map[string]bool{
	"bytes":        true,
	"errors":       true,
	"fmt":          true,
	"math":         true,
	"math/bits":    true,
	"sort":         true,
	"strconv":      true,
	"strings":      true,
	"unicode":      true,
	"unicode/utf8": true,
}

// symbolFormOnLine 判定：命中行上所有含索引符号的 token 是否**全部**是字符串字面量，
// 且符号确实出现在字面量的**内容**里（转义后）。
//
// 返回 (allLiteral, sawSymbolToken, reason)：
//   - allLiteral==false 时 reason 给出第一个反例 token 的形态；
//   - sawSymbolToken==false 表示这一行没有任何以 token 形式承载符号的位置
//     （例如符号只出现在跨行原始字符串的续行上），此时调用方不得据本判据放宽。
func symbolFormOnLine(absFile string, lineNo int) (allLiteral, sawSymbolToken bool, reason string) {
	src, err := os.ReadFile(absFile)
	if err != nil {
		return false, false, "read: " + err.Error()
	}
	fset := token.NewFileSet()
	f := fset.AddFile(absFile, fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(f, src, nil, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		text := lit
		if text == "" {
			text = tok.String()
		}
		start := fset.Position(pos).Line
		end := start + strings.Count(text, "\n")
		if lineNo < start || lineNo > end {
			continue
		}
		if !judgeIndexSymbolRE.MatchString(text) {
			continue
		}
		sawSymbolToken = true
		if tok != token.STRING {
			return false, sawSymbolToken, tok.String() + " token 承载符号：" + strings.TrimSpace(text)
		}
		unquoted, uerr := strconv.Unquote(lit)
		if uerr != nil {
			unquoted = lit
		}
		if !judgeIndexSymbolRE.MatchString(unquoted) {
			return false, sawSymbolToken, "符号不在字面量内容里：" + strings.TrimSpace(text)
		}
	}
	if s.ErrorCount > 0 {
		return false, sawSymbolToken, "scanner 报错"
	}
	return sawSymbolToken, sawSymbolToken, ""
}

// pkgCapability 读 relFile 所在**包目录**的全部非测试源，回答两个依赖事实：
//   - pure：import 闭包是否封闭在 pureComputationStdlib 内；
//   - indexPathFree：包内是否**完全没有** `.index/` 路径 token。
func pkgCapability(root, relFile string) (pure, indexPathFree bool, reason string) {
	dir := filepath.Join(root, filepath.Dir(relFile))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, false, "readdir: " + err.Error()
	}
	pure, indexPathFree = true, true
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		p := filepath.Join(dir, name)
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return false, false, "read: " + rerr.Error()
		}
		if strings.Contains(string(body), judgeIndexPathToken) {
			indexPathFree = false
			reason = name + " 含索引路径 token " + judgeIndexPathToken
		}
		af, perr := parser.ParseFile(fset, p, body, parser.ImportsOnly)
		if perr != nil {
			return false, indexPathFree, "parse: " + perr.Error()
		}
		for _, spec := range af.Imports {
			path, uerr := strconv.Unquote(spec.Path.Value)
			if uerr != nil {
				return false, indexPathFree, "import 字面量不可解析：" + spec.Path.Value
			}
			if !pureComputationStdlib[path] {
				pure = false
				if reason == "" {
					reason = name + " import 了有副作用能力的包：" + path
				}
			}
		}
	}
	return pure, indexPathFree, reason
}

// provablyNotIndexImplementation 是替代具名豁免的**行为判据**：
// 命中行是否可证「不是 S4 索引实现」。三条判据全过才判是，任一条不过照旧判越界。
func provablyNotIndexImplementation(root, rel string, lineNo int, line string) (bool, string) {
	if strings.Contains(line, judgeIndexPathToken) {
		return false, "命中行含路径 token " + judgeIndexPathToken + "：路径面不接受行为判据"
	}
	allLiteral, sawSymbol, reason := symbolFormOnLine(filepath.Join(root, rel), lineNo)
	if !sawSymbol {
		return false, "该行无 token 形态的符号承载点"
	}
	if !allLiteral {
		return false, "出现形态不是字符串字面量：" + reason
	}
	pure, indexPathFree, why := pkgCapability(root, rel)
	if !pure {
		return false, "包依赖闭包具备副作用能力：" + why
	}
	if !indexPathFree {
		return false, "包内出现索引路径 token：" + why
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// 双侧反证
// ---------------------------------------------------------------------------

// judgeFixture 是一组合成包：写入 tmp 后对指定行跑判据，断言判定与期望一致。
type judgeFixture struct {
	name   string
	rel    string // 相对 root 的文件路径
	body   string
	hit    string // 命中行必须包含的子串（用来定位行号，不是判据本身）
	accept bool   // 期望判据是否放宽
	why    string // 期望被拒时，reason 必须包含的关键字
}

func TestIndexSymbolJudgeIsBehavioralNotAllowlist(t *testing.T) {
	// —— 一侧：合成包的正反例（依赖 / 形态 / 索引路径三个维度各自单变量翻转）——
	fixtures := []judgeFixture{
		{
			name: "纯计算数据包·字面量 → 接受",
			rel:  "internal/synth/dataonly/data.go",
			body: "// [S4] synth\npackage dataonly\n\nimport \"strconv\"\n\n" +
				"func Keywords() []string { return []string{\"latency\", \"sqlite\"} }\n\n" +
				"func N(i int) string { return strconv.Itoa(i) }\n",
			hit:    "\"sqlite\"",
			accept: true,
		},
		{
			name: "同一份数据 + import database/sql → 拒绝（依赖）",
			rel:  "internal/synth/withsql/data.go",
			body: "// [S4] synth\npackage withsql\n\nimport \"database/sql\"\n\n" +
				"var DB *sql.DB\n\nfunc Keywords() []string { return []string{\"sqlite\"} }\n",
			hit:    "\"sqlite\"",
			accept: false,
			why:    "database/sql",
		},
		{
			name: "同一份数据 + import os → 拒绝（依赖·文件面）",
			rel:  "internal/synth/withos/data.go",
			body: "// [S4] synth\npackage withos\n\nimport \"os\"\n\n" +
				"func Dump(p string) error { return os.WriteFile(p, nil, 0o644) }\n\n" +
				"func Keywords() []string { return []string{\"sqlite\"} }\n",
			hit:    "\"sqlite\"",
			accept: false,
			why:    "os",
		},
		{
			name: "同一份数据 + import 本仓 internal 包 → 拒绝（依赖·仓内）",
			rel:  "internal/synth/withinternal/data.go",
			body: "// [S4] synth\npackage withinternal\n\nimport \"github.com/ikaqiu-Lemon/EverGreen/internal/model\"\n\n" +
				"var _ = model.Kind(0)\n\nfunc Keywords() []string { return []string{\"sqlite\"} }\n",
			hit:    "\"sqlite\"",
			accept: false,
			why:    "github.com/ikaqiu-Lemon/EverGreen/internal/model",
		},
		{
			name: "第三方驱动 import + 选择器调用 → 拒绝（依赖 + 形态）",
			rel:  "internal/synth/driver/open.go",
			body: "// [S4] synth\npackage driver\n\nimport (\n\t\"database/sql\"\n\n\t_ \"modernc.org/sqlite\"\n)\n\n" +
				"func Open(dsn string) (*sql.DB, error) { return sql.Open(\"sqlite\", dsn) }\n",
			hit:    "sql.Open(\"sqlite\", dsn)",
			accept: false,
			why:    "副作用能力",
		},
		{
			name: "纯计算包但符号是标识符 → 拒绝（形态）",
			rel:  "internal/synth/ident/backend.go",
			body: "// [S4] synth\npackage ident\n\nimport \"strings\"\n\n" +
				"func sqliteBackend(s string) string { return strings.ToUpper(s) }\n\n" +
				"var _ = sqliteBackend\n",
			hit:    "func sqliteBackend",
			accept: false,
			why:    "字符串字面量",
		},
		{
			name:   "纯计算包但符号在注释里 → 拒绝（形态·注释不是数据）",
			rel:    "internal/synth/comment/note.go",
			body:   "// [S4] synth\npackage comment\n\n// 这里用 FTS5 建虚拟表\nconst X = 1\n",
			hit:    "用 FTS5 建虚拟表",
			accept: false,
			why:    "字符串字面量",
		},
		{
			name: "纯计算包但包内夹带 .index/ 路径 → 拒绝（索引路径不接受）",
			rel:  "internal/synth/withpath/data.go",
			body: "// [S4] synth\npackage withpath\n\n" +
				"const Dir = \".index/\"\n\nfunc Keywords() []string { return []string{\"sqlite\"} }\n",
			hit:    "\"sqlite\"",
			accept: false,
			why:    judgeIndexPathToken,
		},
		{
			name:   "命中行本身是 .index/ 路径 → 拒绝（路径面恒不放宽）",
			rel:    "internal/synth/pathline/data.go",
			body:   "// [S4] synth\npackage pathline\n\nfunc Dir() string { return \".index/\" }\n",
			hit:    "\".index/\"",
			accept: false,
			why:    "路径面",
		},
	}

	tmp := t.TempDir()
	for _, fx := range fixtures {
		abs := filepath.Join(tmp, fx.rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("%s：mkdir：%v", fx.name, err)
		}
		if err := os.WriteFile(abs, []byte(fx.body), 0o644); err != nil {
			t.Fatalf("%s：write：%v", fx.name, err)
		}
		lineNo, line := 0, ""
		for i, l := range strings.Split(fx.body, "\n") {
			if strings.Contains(l, fx.hit) {
				lineNo, line = i+1, l
				break
			}
		}
		if lineNo == 0 {
			t.Fatalf("%s：fixture 自身不含命中串 %q（fixture 写错了）", fx.name, fx.hit)
		}
		if !judgeIndexSymbolRE.MatchString(line) {
			t.Fatalf("%s：命中行不含索引符号，反证无意义：%s", fx.name, line)
		}
		got, reason := provablyNotIndexImplementation(tmp, fx.rel, lineNo, line)
		if got != fx.accept {
			t.Fatalf("%s：判据 = %v，期望 %v（reason=%q）", fx.name, got, fx.accept, reason)
		}
		if !fx.accept && !strings.Contains(reason, fx.why) {
			t.Fatalf("%s：拒绝理由 %q 未包含期望关键字 %q", fx.name, reason, fx.why)
		}
	}

	// —— 另一侧：真仓反证。真实索引实现的每处命中必须被拒绝。——
	root := repoRootT029(t)
	implHits, implAccepted := 0, []string{}
	dataHits := []string{}
	acceptedPkgs := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			body, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			for i, line := range strings.Split(string(body), "\n") {
				if !judgeIndexSymbolRE.MatchString(line) {
					continue
				}
				ok, _ := provablyNotIndexImplementation(root, rel, i+1, line)
				if strings.HasPrefix(rel, "internal/index/") {
					implHits++
					if ok {
						implAccepted = append(implAccepted,
							rel+":"+strconv.Itoa(i+1)+" "+strings.TrimSpace(line))
					}
				}
				if ok {
					dataHits = append(dataHits, rel+":"+strconv.Itoa(i+1))
					acceptedPkgs[filepath.Dir(rel)] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s：%v", dir, err)
		}
	}
	if implHits == 0 {
		t.Fatal("internal/index/ 对索引符号零命中：真实索引实现疑被删除，负向反证失效")
	}
	if len(implAccepted) > 0 {
		t.Fatalf("判据错误放宽了真实索引实现的 %d 处命中：%s",
			len(implAccepted), strings.Join(implAccepted, " | "))
	}
	// 被接受的每个包必须**独立**满足两条依赖事实（与文件名无关的复算）。
	for pkg := range acceptedPkgs {
		pure, free, why := pkgCapability(root, pkg+"/x.go")
		if !pure || !free {
			t.Fatalf("被判据接受的包 %s 复算失败：pure=%v indexPathFree=%v（%s）", pkg, pure, free, why)
		}
	}
	if len(dataHits) == 0 {
		t.Log("提示：本仓当前没有任何纯数据包命中索引符号；判据仍按上面的合成反证生效")
	}
}
