package query

// [S4 · Phase6C] `eg rel --replaced-by` 的 **k/o 读路径四后端等价**（M5 索引架构合同 §8.4 +
// §5.2；T-…-068）。这是 index_backed_ko_readpath_test.go 里论证关系四后端矩阵
// （TestReadPathKOFourBackendEquivalence）在**替代指针视图**上的对应支：healthy / missing /
// stale / corrupt 四后端上，`--replaced-by` 的业务投影（正向「谁取代了本端点」、反向
// 「本端点取代了谁」，跨 k/o 宿主与目标）逐字等价，降级只体现在诊断码（W22/W23/W24 + Q5）。
//
// 为什么必须单列这一支：替代指针的**反向**来源不在 `relations` 表里（它不是论证关系），
// 索引后端要靠 `cards.replaced_by` 列（target）定位反向宿主、再回权威补齐 reason
// （index_backed.go 的 plan.replacedBy 分支）。因此「healthy 索引确实走索引后端且结果与扫描
// 后端逐字等价」这条判据，只有把 replaced_by 也建进索引快照（bkoBuildIndexWithOpinions 现
// 已随 CardEntry/OpinionEntry.ReplacedByTarget 建列，与生产 indexSnapshotWith 逐字对称）
// 才检验得到——否则反向查会因缺列而恒空，退化成一条假绿。
//
// 本文件属 `index_backed*` 前缀，架构边界（cmd/eg 的 TestStage4IndexPackageBoundary）据此
// 放行 import internal/index；语料 / 宿主投影脚手架复用 reverse_ko_test.go 的 rvWriteK /
// rvWriteO（写 replaced_by 宿主），破坏 / 降级脚手架复用 backend_test / index_backed_opinion
// （bkoBuildIndexWithOpinions / bkDropIndex / bkCorruptIndex / bkRel / korAssertDegradeCodes）。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// —— 语料 ID（20270201-*，与既有语料绝不撞号）——
const (
	rbTgtK  = "k-20270201-tgtk" // 目标知识卡（active）：被 k / o 两类宿主替代
	rbTgtO  = "o-20270201-tgto" // 目标观点（active）：被 k / o 两类宿主替代
	rbHkk   = "k-20270201-hkk"  // k→k 宿主（deprecated）→ rbTgtK
	rbHko   = "k-20270201-hko"  // k→o 宿主（deprecated）→ rbTgtO
	rbHok   = "o-20270201-hok"  // o→k 宿主（deprecated）→ rbTgtK
	rbHoo   = "o-20270201-hoo"  // o→o 宿主（deprecated）→ rbTgtO
	rbNoise = "k-20270201-noise"

	rbRhkk = "hkk 被 tgtk 取代"
	rbRhko = "hko 被 tgto 取代"
	rbRhok = "hok 被 tgtk 取代"
	rbRhoo = "hoo 被 tgto 取代"
)

// rbkoVault 造替代指针四组合语料（k→k / k→o / o→k / o→o 各一个失效宿主 + 两个 active 目标）。
//
//	rbTgtK 反向宿主：rbHkk(k→k)、rbHok(o→k)     —— 反向须跨 k/o 两类宿主
//	rbTgtO 反向宿主：rbHko(k→o)、rbHoo(o→o)
//	rbNoise 与断言无关，仅供 rbkoMakeStale 触发陈旧
func rbkoVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rvWriteK(t, root, rbTgtK, "目标卡", "active", "", "", false)
	rvWriteO(t, root, rbTgtO, "目标观点", "active", "validated", "", "", false)
	rvWriteK(t, root, rbHkk, "hkk", "deprecated", rbTgtK, rbRhkk, false)
	rvWriteK(t, root, rbHko, "hko", "deprecated", rbTgtO, rbRhko, false)
	rvWriteO(t, root, rbHok, "hok", "deprecated", "rejected", rbTgtK, rbRhok, false)
	rvWriteO(t, root, rbHoo, "hoo", "deprecated", "rejected", rbTgtO, rbRhoo, false)
	rvWriteK(t, root, rbNoise, "噪声", "active", "", "", false)
	return root
}

// rbkoMakeStale 真改一张**与断言无关**的卡（rbNoise）字节并推后 mtime，触发 index_stale。
func rbkoMakeStale(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash("domains/ai-infra/knowledge/"+rbNoise+".md"))
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 noise 卡失败：%v", err)
	}
	if err := os.WriteFile(p, append(raw, []byte("\n补一段无关正文。\n")...), 0o644); err != nil {
		t.Fatalf("改 noise 卡失败：%v", err)
	}
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatalf("改 mtime 失败：%v", err)
	}
}

// rbkoProjection 是一次替代指针读的业务投影（供跨后端逐字比对；不含降级诊断——那是 backend 的事）。
type rbkoProjection struct {
	RevTgtK string // rbTgtK 反向：from|reason 序列（放开 deprecated）
	RevTgtO string // rbTgtO 反向：from|reason 序列（放开 deprecated）
	FwdHkk  string // rbHkk 正向：target|reason
	FwdHoo  string // rbHoo 正向：target|reason
}

// rbSigIn / rbSigOut 把反向 / 正向段压成可逐格比对的签名。
func rbSigIn(edges []RelationEdge) string {
	parts := []string{}
	for _, e := range edges {
		parts = append(parts, e.From+"|"+e.Reason)
	}
	return strings.Join(parts, ",")
}

func rbSigOut(edges []RelationEdge) string {
	parts := []string{}
	for _, e := range edges {
		parts = append(parts, e.Target+"|"+e.Reason)
	}
	return strings.Join(parts, ",")
}

// rbkoProject 取四条 `--replaced-by` 读命令的业务投影，并逐条断言后端与预期一致
// （healthy → 索引后端，破坏 → 扫描后端）。返回投影与四条读命令各自的诊断。
func rbkoProject(t *testing.T, root string, wantIndex bool) (rbkoProjection, [][]Diagnostic) {
	t.Helper()
	// 反向须放开 deprecated：四个宿主都是 deprecated，默认视图会被既有可见性策略隐藏。
	revK, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(rbTgtK),
		ReplacedBy: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("rel %s --replaced-by：%v", rbTgtK, err)
	}
	revO, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(rbTgtO),
		ReplacedBy: true, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("rel %s --replaced-by：%v", rbTgtO, err)
	}
	// 正向宿主为 deprecated，但正向边的对端是 active 目标，默认视图即可见（可见性筛的是对端）。
	fwdK, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(rbHkk), ReplacedBy: true})
	if err != nil {
		t.Fatalf("rel %s --replaced-by：%v", rbHkk, err)
	}
	fwdO, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(rbHoo), ReplacedBy: true})
	if err != nil {
		t.Fatalf("rel %s --replaced-by：%v", rbHoo, err)
	}
	for _, r := range []struct {
		label string
		res   *RelResult
	}{{"rel tgtk", revK}, {"rel tgto", revO}, {"rel hkk", fwdK}, {"rel hoo", fwdO}} {
		if got := r.res.backend.UseIndex(); got != wantIndex {
			t.Fatalf("%s 后端与预期不符：useIndex=%v want=%v（%s）",
				r.label, got, wantIndex, r.res.backend.Message)
		}
	}
	return rbkoProjection{
		RevTgtK: rbSigIn(revK.Data.RelationsIn),
		RevTgtO: rbSigIn(revO.Data.RelationsIn),
		FwdHkk:  rbSigOut(fwdK.Data.RelationsOut),
		FwdHoo:  rbSigOut(fwdO.Data.RelationsOut),
	}, [][]Diagnostic{revK.Diagnostics, revO.Diagnostics, fwdK.Diagnostics, fwdO.Diagnostics}
}

// TestReplacedByKOFourBackendEquivalence —— healthy / missing / stale / corrupt 四后端上，
// `--replaced-by` 的 k/o 正反读投影逐字等价，降级只体现在诊断码（W22/W23/W24 + Q5）。
func TestReplacedByKOFourBackendEquivalence(t *testing.T) {
	labels := []string{"rel tgtk", "rel tgto", "rel hkk", "rel hoo"}

	// 基线：healthy 索引（含观点、含 replaced_by 列）。四条读命令都走索引后端，W22/W23/W24/Q5 全 0。
	root := rbkoVault(t)
	bkoBuildIndexWithOpinions(t, root)
	healthy, hdiags := rbkoProject(t, root, true)
	for i, d := range hdiags {
		korAssertDegradeCodes(t, "healthy/"+labels[i], d, "")
	}
	// 反向确实跨 k/o 两类宿主、reason 取自权威逐字原值（否则四后端等价会退化成空绿）。
	if healthy.RevTgtK != rbHkk+"|"+rbRhkk+","+rbHok+"|"+rbRhok {
		t.Fatalf("tgtk 反向投影 = %q，期望 k/o 两类宿主各一条且 reason 权威", healthy.RevTgtK)
	}
	if healthy.RevTgtO != rbHko+"|"+rbRhko+","+rbHoo+"|"+rbRhoo {
		t.Fatalf("tgto 反向投影 = %q，期望 k/o 两类宿主各一条且 reason 权威", healthy.RevTgtO)
	}
	if healthy.FwdHkk != rbTgtK+"|"+rbRhkk || healthy.FwdHoo != rbTgtO+"|"+rbRhoo {
		t.Fatalf("正向投影 = (%q,%q)，期望各恰一条且 reason 权威", healthy.FwdHkk, healthy.FwdHoo)
	}

	// missing / corrupt / stale：各自在新建 healthy vault 上破坏，再取投影（应降级为扫描）。
	// 逐项断言降级码，业务投影与 healthy 逐字等价。
	for _, c := range []struct {
		name   string
		wantW  string
		break_ func(*testing.T, string)
	}{
		{"missing", index.CodeIndexMissing, bkDropIndex},
		{"corrupt", index.CodeIndexCorrupt, bkCorruptIndex},
		{"stale", index.CodeIndexStale, rbkoMakeStale},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := rbkoVault(t)
			bkoBuildIndexWithOpinions(t, r)
			c.break_(t, r)
			got, gdiags := rbkoProject(t, r, false)
			for i, d := range gdiags {
				korAssertDegradeCodes(t, c.name+"/"+labels[i], d, c.wantW)
			}
			if got != healthy {
				t.Fatalf("%s 降级后 --replaced-by 业务投影与 healthy 不等价：\nhealthy=%+v\n%s=%+v",
					c.name, healthy, c.name, got)
			}
		})
	}
}
