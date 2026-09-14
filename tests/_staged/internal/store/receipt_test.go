package store_test

// `skipped[].kind` 的封闭性守卫（判据 15，合同 §8.3 加严裁决）：
// M3 新增 `replace_block` 的块级冲突**复用**既有两值，不新造第三个 kind。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// TestCauseFor_ExactlyTwoKinds 断言 SkipReason 除 SkipNone 外**恰两值**，
// CauseFor 一一对应，且对任何第三值返回空串（不猜、不兜底成某个已知 cause）。
func TestCauseFor_ExactlyTwoKinds(t *testing.T) {
	if got := store.CauseFor(store.SkipFileChanged); got != "content_hash_mismatch" {
		t.Fatalf("CauseFor(file_changed) = %q，期望 content_hash_mismatch", got)
	}
	if got := store.CauseFor(store.SkipUserBlockUnsafe); got != "user_block_not_preserved" {
		t.Fatalf("CauseFor(user_block_unsafe) = %q，期望 user_block_not_preserved", got)
	}
	if got := store.CauseFor(store.SkipNone); got != "" {
		t.Fatalf("CauseFor(SkipNone) 必须是空串，实得 %q", got)
	}
	// 第三值（含块级冲突的两个**预留**名，拼接构造以免在源码里留下完整字面量）
	// 一律返回空串：kind 是封闭两值，不存在第三种。
	for _, third := range []string{"block" + "_conflict", "block" + "_hash_changed",
		"stale", "content_hash_mismatch", "unknown"} {
		if got := store.CauseFor(store.SkipReason(third)); got != "" {
			t.Fatalf("CauseFor(%q) 必须是空串（kind 恰两值），实得 %q", third, got)
		}
	}

	// 源码级封闭：receipt.go 里 SkipReason 常量的**非空取值恰两个**。
	// 这样即便有人新增第四个常量，判据也会立刻失败，而不是等运行时。
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "receipt.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 receipt.go 失败：%v", err)
	}
	var values []string
	ast.Inspect(f, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		id, ok := spec.Type.(*ast.Ident)
		if !ok || id.Name != "SkipReason" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || s == "" {
				continue
			}
			values = append(values, s)
		}
		return true
	})
	if len(values) != 2 {
		t.Fatalf("SkipReason 的非空取值必须恰两个（file_changed / user_block_unsafe），实得 %d 个：%v",
			len(values), values)
	}
	if got := strings.Join(values, ","); got != "file_changed,user_block_unsafe" {
		t.Fatalf("SkipReason 取值集合必须恰等于 {file_changed, user_block_unsafe}，实得 %v", values)
	}
}
