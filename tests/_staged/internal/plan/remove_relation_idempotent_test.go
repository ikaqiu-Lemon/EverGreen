package plan

// T-…-044 的 plan 层判据：`eg rel remove` 合成的那份 plan **重跑幂等**。
//
// 为什么幂等必须是 warning 而不是 error（提案与状态合同 §9.3「重跑同一 plan 幂等」）：
// 第一次跑删掉记录，第二次跑同一份 plan 就必然「未命中」——判 error 会让重跑失败，
// 把幂等变成陷阱。因此第二次是 **W10** + 零 action + 零写入（因而上层零新增 commit）。
//
// 本文件不新增 op、不新增诊断码、不新增写入链路：跑的是既有 Validate → Execute 同一条链。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// relRemovePlanBody 造一份与 `eg rel remove` **同形**的 plan：
// verb=relate + 单条 remove_relation op + initiator=user，base 覆盖全库。
func relRemovePlanBody(t *testing.T, files map[string]string, from, relType, target string) string {
	t.Helper()
	var base []string
	for rel, content := range files {
		base = append(base, fmt.Sprintf("%q:%q", rel, store.ContentHash([]byte(content))))
	}
	return fmt.Sprintf(`{"plan_version":1,"verb":"relate","domain":"ai-infra",`+
		`"reason":"该限定已不成立","requirement_ids":[],"base":{%s},"ops":[`+
		`{"op":"remove_relation","from":%q,"type":%q,"target":%q,`+
		`"reason":"该限定已不成立","initiator":"user"}]}`,
		strings.Join(base, ","), from, relType, target)
}

// TestRemoveRelation_Idempotent：同一份 plan 连跑两次——第一次真删，第二次 W10 幂等。
//
// 「零新增 commit」在本层的可观察形式是 **ex.Written 为空**：CLI 的 runPlan 只在有写入时
// 才提交，没有写入就不会产生空 commit（commit 计数的端到端取证在 test/e2e/m3_rel_remove.sh）。
func TestRemoveRelation_Idempotent(t *testing.T) {
	files := m3Files()
	root, st := authVault(t, files)
	rel := "domains/ai-infra/knowledge/k-20260901-attention.md"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	body := relRemovePlanBody(t, files, "k-20260901-attention", "limits", "k-20260815-rnn")

	// ① 第一次：命中 → 恰一条 remove_relation action，真的落盘。
	first := authRun(t, root, st, body, true)
	if first.Failed() {
		t.Fatalf("第一次执行不得失败：%v", codes(first.Errors))
	}
	if len(first.Actions) != 1 || first.Actions[0].Kind != ActRemoveRelation {
		t.Fatalf("第一次应展开恰一条移除 action：%+v", first.Actions)
	}
	if _, ok := find(first.Warnings, W10); ok {
		t.Fatal("第一次是命中，不该出现 W10")
	}
	afterFirst := mustRead(t, abs)
	if strings.Contains(afterFirst, "target: k-20260815-rnn") {
		t.Fatalf("第一次必须物理移除匹配的全部记录：\n%s", afterFirst)
	}

	// ② 第二次：同一份 plan（base 仍是首次执行前的哈希）→ W10 幂等、零 action、零写入。
	second := authRun(t, root, st, body, true)
	if second.Failed() {
		t.Fatalf("重跑必须退 0（幂等），实得 errors=%v", codes(second.Errors))
	}
	d, ok := find(second.Warnings, W10)
	if !ok {
		t.Fatalf("重跑必须记 W10，实得 warnings=%v", codes(second.Warnings))
	}
	if d.Level != LevelWarning {
		t.Fatalf("W10 必须是 warning（不拦截、不改退出码），实得 %q", d.Level)
	}
	if len(second.Actions) != 0 {
		t.Fatalf("重跑必须零 action（零写入、不产生空 commit），实得 %d 条", len(second.Actions))
	}
	if got := mustRead(t, abs); got != afterFirst {
		t.Fatalf("重跑必须字节不变：\n%s", got)
	}
	// 第三次同理（幂等不是「只第二次成立」）。
	third := authRun(t, root, st, body, true)
	if third.Failed() || len(third.Actions) != 0 {
		t.Fatalf("第三次仍须退 0 + 零 action：errors=%v actions=%+v", codes(third.Errors), third.Actions)
	}
	if got := mustRead(t, abs); got != afterFirst {
		t.Fatal("第三次仍须字节不变")
	}
}

// mustRead 读一个文件的全文（字节级取证）。
func mustRead(t *testing.T, abs string) string {
	t.Helper()
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", abs, err)
	}
	return string(raw)
}
