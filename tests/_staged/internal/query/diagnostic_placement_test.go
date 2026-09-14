package query

// [M4 / T-…-061 最终审查①] A-39 的**机器反证**：Q 码集合 CodeQ1..CodeQ4 必须**全部**定义在
// internal/query/diagnostic.go，且 Q4 的组装逻辑（withDeprecatedHiddenDiagnostic / newQ4）
// 与前三条同处一个文件；card.go / relation.go 只准调用，不准各自持有 Q 码常量或 Q4 组装分支。
//
// 归位前的实现把 CodeQ4 与 withDeprecatedHiddenDiagnostic 落在 card.go，违反 A-39
// 「CodeQ1..CodeQ4 都定义在 internal/query/diagnostic.go」的裁决；本用例把该裁决钉成可判据。

import (
	"os"
	"strings"
	"testing"
)

// readQuerySource 读取 internal/query 下的源文件（测试工作目录即包目录）。
func readQuerySource(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读源文件 %s：%v", name, err)
	}
	return string(raw)
}

// TestA39QCodesAllDefinedInDiagnostic —— A-39 机器反证：
//
//	① 四条 Q 码常量定义（`CodeQ1 =` … `CodeQ4 =`）逐条出现在 diagnostic.go；
//	② card.go / relation.go 不得再出现任何 `CodeQ? =` 常量定义（只准引用）；
//	③ Q4 组装逻辑（func withDeprecatedHiddenDiagnostic / func newQ4）定义在 diagnostic.go，
//	   不在 card.go；
//	④ 运行期行为不变：Q1–Q3 语义与四码取值仍为 "Q1".."Q4"。
func TestA39QCodesAllDefinedInDiagnostic(t *testing.T) {
	diag := readQuerySource(t, "diagnostic.go")
	card := readQuerySource(t, "card.go")
	rel := readQuerySource(t, "relation.go")

	// ① 四条常量定义都在 diagnostic.go。needle 用 `CodeQn =` 定义式（带等号），
	// 避免命中注释里的引用（注释里写的是 `CodeQ4` 不带 ` =`）。
	for _, code := range []string{"CodeQ1", "CodeQ2", "CodeQ3", "CodeQ4"} {
		def := code + " ="
		if !strings.Contains(diag, def) {
			t.Fatalf("A-39：%s 的常量定义必须在 internal/query/diagnostic.go（找不到 %q）", code, def)
		}
	}

	// ② card.go / relation.go 不得再各自定义 Q 码常量。
	for _, code := range []string{"CodeQ1", "CodeQ2", "CodeQ3", "CodeQ4"} {
		def := code + " ="
		if strings.Contains(card, def) {
			t.Fatalf("A-39：card.go 不得定义 %s（应归位到 diagnostic.go 只做调用）", code)
		}
		if strings.Contains(rel, def) {
			t.Fatalf("A-39：relation.go 不得定义 %s（应归位到 diagnostic.go 只做调用）", code)
		}
	}

	// ③ Q4 组装逻辑定义在 diagnostic.go，不在 card.go。
	for _, fn := range []string{"func withDeprecatedHiddenDiagnostic", "func newQ4"} {
		if !strings.Contains(diag, fn) {
			t.Fatalf("A-39：Q4 组装逻辑 %q 必须定义在 diagnostic.go", fn)
		}
		if strings.Contains(card, fn) {
			t.Fatalf("A-39：card.go 不得定义 %q（应归位到 diagnostic.go）", fn)
		}
	}

	// ④ 运行期取值不变。
	if CodeQ1 != "Q1" || CodeQ2 != "Q2" || CodeQ3 != "Q3" || CodeQ4 != "Q4" {
		t.Fatalf("Q 码取值必须保持 Q1..Q4：%q %q %q %q", CodeQ1, CodeQ2, CodeQ3, CodeQ4)
	}

	// ⑤ 归位说明留痕：diagnostic.go 的口径注释已把「恰三条」放宽为「S3 起恰四条」，
	// 且强调 Q1–Q3 语义不变。用分片 needle 校验，避免命中静态反证 grep 的字面量。
	if !strings.Contains(diag, "恰四条") {
		t.Fatalf("diagnostic.go 应载明「S3 起恰四条」的 A-39 放宽口径")
	}
}
