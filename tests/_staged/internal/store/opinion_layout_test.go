package store_test

// Opinion 的目录布局与扫描纳管（Schema v2 契约 §3.1）。
//
// 两条判据：
//  1. `OpinionRel` 是 o-id → `domains/<d>/opinions/<o-id>.md` 的唯一拼装点；
//  2. `ScanIDs` 是**类型无关**的：新增一个 ID 前缀不需要改扫描器。
//     第 2 条是本任务里唯一「验证而非新增」的断言——若实际不成立，说明扫描器
//     暗含了前缀白名单，必须一并修，而不是把断言删掉。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

func TestOpinionRelPath(t *testing.T) {
	got := store.OpinionRel("agent", "o-20260915-harness-boundary-cost")
	want := "domains/agent/opinions/o-20260915-harness-boundary-cost.md"
	if got != want {
		t.Fatalf("OpinionRel 应为 %q，实际 %q", want, got)
	}
	if store.DirOpinions != "opinions" {
		t.Fatalf("DirOpinions 应为 \"opinions\"，实际 %q", store.DirOpinions)
	}
	// 领域反推对 opinions 路径同样成立（领域由目录唯一决定）。
	if d := store.DomainOf(got); d != "agent" {
		t.Fatalf("DomainOf(%q) 应为 agent，实际 %q", got, d)
	}
}

func TestOpinionKindAndSectionsExposedToPlanLayer(t *testing.T) {
	// plan 层不得直连 mdfile（§13 依赖方向），Opinion 的分区口径必须由 store 转发。
	if store.KindOpinion == "" {
		t.Fatal("store 必须透出 KindOpinion")
	}
	want := []string{"观点", "论据与推理", "条件与反例", "待验证", "用户补充"}
	got := store.OpinionSections()
	if len(got) != len(want) {
		t.Fatalf("store.OpinionSections() 应有 %d 段，实际 %d：%v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("store.OpinionSections() 第 %d 段应为 %q，实际 %q", i, want[i], got[i])
		}
	}
	if store.RequiredSection(store.KindOpinion) != store.SecOpinionClaim {
		t.Fatalf("store.RequiredSection(KindOpinion) 应为 SecOpinionClaim，实际 %q",
			store.RequiredSection(store.KindOpinion))
	}
}

func TestScanIDsIsPrefixAgnostic(t *testing.T) {
	dir := t.TempDir()
	// 造一个只含 Opinion 的最小 vault：若扫描器暗含前缀白名单，这里会扫不到。
	rel := store.OpinionRel("agent", "o-20260915-scan-probe")
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	body := "---\nid: o-20260915-scan-probe\n---\n\n## 观点\n\n扫描探针。\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}

	s := store.New(dir)
	idx, err := s.ScanIDs()
	if err != nil {
		t.Fatalf("ScanIDs 失败：%v", err)
	}
	got, ok := idx.ByID["o-20260915-scan-probe"]
	if !ok {
		t.Fatalf("ScanIDs 未纳管 o- 前缀产物；索引内容：%v", idx.ByID)
	}
	if got != rel {
		t.Fatalf("ScanIDs 记录的路径应为 %q，实际 %q", rel, got)
	}
	if resolved, err := idx.Resolve("o-20260915-scan-probe"); err != nil || resolved != rel {
		t.Fatalf("Resolve 应返回 %q，实际 (%q, %v)", rel, resolved, err)
	}
}
