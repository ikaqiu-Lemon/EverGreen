package query

// [T-006-B2c Phase 3] query/index 关系**读路径**把 Knowledge ∪ Opinion 两类持有方的
// relations[] 都纳入：同一读 API 按 k/o 端点查正向与反向，四组合
//
//	k→k / k→o / o→k / o→o
//
// 都可读；尤其 `opinion show` 的反向段里 `*→o` 的**真实边**必须出现（B2a 的
// 「写模型忠实语料下反向恒空」边界已被 Phase 2 写路径打破：写路径现在允许任何产物以
// 观点为 target）。
//
// 本支只钉**读路径投影**，不接任何 CLI、不改写模型：
//   - card show / rel：正向读焦点卡 relations[]（可跨 k/o 端点），反向在 Knowledge ∪
//     Opinion 全库反查「谁指向了焦点卡」——因此 o→k 的反向边必须现身；
//   - opinion show：正向读观点 relations[]（o→k / o→o），反向在全库反查 `*→o`；
//   - 可见性（对端 deprecated 默认隐藏并计 Q4、已删除对端任何 flag 都隐藏）、悬空
//     （目标不在 Knowledge ∪ Opinion 存在宇宙 → MissingTargets + Q2）、确定性排序、
//     现有 JSON 键集合一律不变；
//   - 四后端矩阵（healthy / missing / stale / corrupt）：除降级诊断（W22/W23/W24 + Q5）
//     外业务结果逐字等价；
//   - 读路径对文件 / Git / 索引零副作用。
//
// 语料 korVault 是**自包含**的干净四组合语料（不叠加 bkVault，避免无关诊断噪声干扰精确
// 断言），但索引与降级脚手架复用 backend_test / index_backed_opinion_test 的既有实现
// （bkoBuildIndexWithOpinions / bkDropIndex / bkCorruptIndex / bkShow / bkRel / osShow /
// bkDeps），绝不伪造 DTO 掩盖链路。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 语料 ID（k-20270101-* / o-20270101-*，与 bkVault 的 20261201 语料绝不撞号）——
const (
	korAlphaID = "k-20270101-alpha" // 关系起点卡：k→k / k→o / k→o(悬空)
	korBetaID  = "k-20270101-beta"  // 反向目标卡：被 k / o 两类持有方指向
	korNoiseID = "k-20270101-noise" // 与本支断言无关的卡：仅供 korMakeStale 触发陈旧

	korPoriID = "o-20270101-pori" // 观点：o→k / o→o；且是 k-alpha 的 k→o 目标（*→o 反向证据）
	korQuonID = "o-20270101-quon" // 反向目标观点：被 o-pori 以 o→o 指向
	korDepID  = "o-20270101-dep"  // deprecated 观点：o→k，默认隐藏 + 计 Q4
	korDeadID = "o-20270101-dead" // 已删除观点：o→k，任何 flag 都隐藏

	korGhostID = "o-20270101-ghost" // 悬空目标：库中不存在（k-alpha 的 opposing 指向它）
)

// 逐字 reason（用于反证反向边的 reason 取自**权威源文件**，而非索引 relations 表 stub）。
const (
	korRAlphaBeta = "阿尔法支撑贝塔"
	korRAlphaPori = "阿尔法支撑波里"
	korRAlphaGho  = "阿尔法反对幽灵"
	korRPoriBeta  = "波里支撑贝塔"
	korRPoriQuon  = "波里限制库恩"
	korRDepBeta   = "废弃观点支撑贝塔"
	korRDeadBeta  = "已删观点支撑贝塔"
)

// korOpinion 造一条最小合法观点（frontmatter 键序符合 model.Opinion）。
// status ∈ {active, deprecated}；deletedAt 非空即置删除维度；relations 逐字拼入。
func korOpinion(id, title, status, validation, deletedAt, relations string) string {
	fm := "---\nid: " + id +
		"\nstatus: " + status +
		"\ncreated_at: '2027-01-01'" +
		"\nupdated_at: '2027-01-02T10:00:00+08:00'" +
		"\ntitle: " + title +
		"\nvalidation: " + validation +
		"\nsources: []\n"
	if deletedAt != "" {
		fm += "deleted_at: '" + deletedAt + "'\ndeleted_reason: 结论被推翻\n"
	}
	if relations != "" {
		fm += "relations:\n" + relations
	}
	return fm + "---\n\n# " + title + "\n\n## 观点\n\n主张一句。\n"
}

// korRel 拼一条 relations[] 元素（2 空格缩进，与 bkCard / bkoOpinion 同口径）。
func korRel(verb, target, reason string) string {
	return "  - type: " + verb + "\n    target: " + target + "\n    reason: " + reason + "\n"
}

// korVault 造四组合关系读路径语料（干净、自包含），返回 vault 根。
//
//	k-alpha  supports→k-beta(k→k) / supports→o-pori(k→o) / opposing→o-ghost(k→o 悬空)
//	k-beta   无关系（反向目标）
//	k-noise  无关系（陈旧触发用）
//	o-pori   supports→k-beta(o→k) / limits→o-quon(o→o)
//	o-quon   无关系（反向目标）
//	o-dep    supports→k-beta(o→k)，status=deprecated
//	o-dead   supports→k-beta(o→k)，deleted_at 有值
func korVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	k := "domains/ai-infra/knowledge/"
	bkWrite(t, root, k+"k-20270101-alpha.md", bkCard(korAlphaID, "阿尔法", "active",
		"2027-01-02T10:00:00+08:00", "  - 核心\n",
		korRel("supports", korBetaID, korRAlphaBeta)+
			korRel("supports", korPoriID, korRAlphaPori)+
			korRel("opposing", korGhostID, korRAlphaGho)))
	bkWrite(t, root, k+"k-20270101-beta.md", bkCard(korBetaID, "贝塔", "active",
		"2027-01-02T10:00:00+08:00", "  - 核心\n", ""))
	bkWrite(t, root, k+"k-20270101-noise.md", bkCard(korNoiseID, "噪声", "active",
		"2027-01-02T10:00:00+08:00", "  - 核心\n", ""))
	bkWrite(t, root, store.OpinionRel("ai-infra", korPoriID),
		korOpinion(korPoriID, "波里", "active", "pending", "",
			korRel("supports", korBetaID, korRPoriBeta)+
				korRel("limits", korQuonID, korRPoriQuon)))
	bkWrite(t, root, store.OpinionRel("ai-infra", korQuonID),
		korOpinion(korQuonID, "库恩", "active", "validated", "", ""))
	bkWrite(t, root, store.OpinionRel("ai-infra", korDepID),
		korOpinion(korDepID, "废弃观点", "deprecated", "pending", "",
			korRel("supports", korBetaID, korRDepBeta)))
	bkWrite(t, root, store.OpinionRel("ai-infra", korDeadID),
		korOpinion(korDeadID, "已删观点", "active", "rejected", "2027-01-03T10:00:00+08:00",
			korRel("supports", korBetaID, korRDeadBeta)))
	return root
}

// korMakeStale 真改一张**与断言无关**的卡（k-noise）的字节并推后 mtime，触发 index_stale。
func korMakeStale(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash("domains/ai-infra/knowledge/k-20270101-noise.md"))
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

// korFroms / korSig 抽取反向段的 from 序列与「type→peer」签名（断言用）。
func korFroms(edges []RelationEdge) []string {
	out := []string{}
	for _, e := range edges {
		out = append(out, e.From)
	}
	return out
}

func korReasonOf(edges []RelationEdge, from string) string {
	for _, e := range edges {
		if e.From == from {
			return e.Reason
		}
	}
	return ""
}

// —— ① 扫描后端：四组合正反向可读（无索引，走全量扫描底座）——

func TestReadPathKOForwardReverseScanBackend(t *testing.T) {
	root := korVault(t)

	// card show k-alpha：正向 k→k / k→o 可读；k→o 悬空进 MissingTargets（且不误报 o-pori）。
	alpha, err := bkShow(root, model.CardID(korAlphaID))
	if err != nil {
		t.Fatalf("card show alpha：%v", err)
	}
	if alpha.backend.UseIndex() {
		t.Fatalf("前置：无索引应走扫描后端，实际 %s", alpha.backend.Kind)
	}
	if got := targetsOf(alpha.Card.RelationsOut); strings.Join(got, ",") !=
		korGhostID+","+korBetaID+","+korPoriID {
		t.Fatalf("alpha.relations_out = %v，期望 [o-ghost(k→o 悬空,opposing), k-beta(k→k,supports), o-pori(k→o,supports)]", got)
	}
	if strings.Join(alpha.MissingTargets, ",") != korGhostID {
		t.Fatalf("alpha.MissingTargets = %v，期望恰 [o-ghost]（有效 k→o 目标 o-pori 不得误报悬空）",
			alpha.MissingTargets)
	}
	if n := countCode(alpha.Diagnostics, CodeQ2); n != 1 {
		t.Fatalf("alpha 悬空 Q2 应恰一条（k-alpha→o-ghost），实际 %d：%v", n, codesOf(alpha.Diagnostics))
	}
	if len(alpha.Card.RelationsIn) != 0 {
		t.Fatalf("alpha 无人指向，relations_in 应为空，实际 %v", korFroms(alpha.Card.RelationsIn))
	}

	// card show k-beta：反向纳入 k→k(alpha) 与 o→k(pori)；deprecated(dep)/deleted(dead) 默认隐藏。
	beta, err := bkShow(root, model.CardID(korBetaID))
	if err != nil {
		t.Fatalf("card show beta：%v", err)
	}
	if got := korFroms(beta.Card.RelationsIn); strings.Join(got, ",") !=
		korAlphaID+","+korPoriID {
		t.Fatalf("beta.relations_in = %v，期望 [k-alpha(k→k), o-pori(o→k)]（o→k 反向边必须现身）", got)
	}
	if got := korReasonOf(beta.Card.RelationsIn, korPoriID); got != korRPoriBeta {
		t.Fatalf("o→k 反向边 reason 必须是权威逐字原值 %q，实际 %q", korRPoriBeta, got)
	}
	if beta.HiddenDeprecated != 1 {
		t.Fatalf("beta 反向应隐藏 1 个 deprecated 对端(o-dep)，实际 HiddenDeprecated=%d", beta.HiddenDeprecated)
	}

	// opinion show o-pori：正向 o→k / o→o；反向出现 k→o 真实边（k-alpha→o-pori）。
	pori, err := osShow(root, korPoriID)
	if err != nil {
		t.Fatalf("opinion show pori：%v", err)
	}
	if got := targetsOf(pori.Opinion.Supports.Forward); strings.Join(got, ",") != korBetaID {
		t.Fatalf("pori supports.forward = %v，期望 [k-beta(o→k)]", got)
	}
	if got := targetsOf(pori.Opinion.Limits.Forward); strings.Join(got, ",") != korQuonID {
		t.Fatalf("pori limits.forward = %v，期望 [o-quon(o→o)]", got)
	}
	if got := korFroms(pori.Opinion.Supports.Reverse); strings.Join(got, ",") != korAlphaID {
		t.Fatalf("pori supports.reverse = %v，期望 [k-alpha(*→o 真实边)]", got)
	}
	if got := korReasonOf(pori.Opinion.Supports.Reverse, korAlphaID); got != korRAlphaPori {
		t.Fatalf("*→o 反向边 reason 必须是权威逐字原值 %q，实际 %q", korRAlphaPori, got)
	}

	// opinion show o-quon：反向出现 o→o 真实边（o-pori→o-quon）。
	quon, err := osShow(root, korQuonID)
	if err != nil {
		t.Fatalf("opinion show quon：%v", err)
	}
	if got := korFroms(quon.Opinion.Limits.Reverse); strings.Join(got, ",") != korPoriID {
		t.Fatalf("quon limits.reverse = %v，期望 [o-pori(o→o 真实边)]", got)
	}

	// rel k-beta：与 card show 同一套反向事实（o→k 反向边同样现身、reason 权威）。
	rv, err := bkRel(root, RelRequest{ID: model.CardID(korBetaID)})
	if err != nil {
		t.Fatalf("rel beta：%v", err)
	}
	if got := korFroms(rv.Data.RelationsIn); strings.Join(got, ",") != korAlphaID+","+korPoriID {
		t.Fatalf("rel beta relations_in = %v，期望 [k-alpha, o-pori]", got)
	}
	if got := korReasonOf(rv.Data.RelationsIn, korPoriID); got != korRPoriBeta {
		t.Fatalf("rel o→k 反向边 reason 必须权威，实际 %q", got)
	}
}

// —— ② 可见性：deprecated 默认隐藏（Q4）/ --include-deprecated 放开；deleted 恒隐藏 ——

func TestReadPathKOReverseVisibilityDeprecatedDeleted(t *testing.T) {
	root := korVault(t)

	// 默认：o-dep(deprecated) 与 o-dead(deleted) 都不在 beta 反向里；隐藏 1 个 deprecated ⇒ Q4。
	beta, err := bkShow(root, model.CardID(korBetaID))
	if err != nil {
		t.Fatalf("card show beta：%v", err)
	}
	for _, from := range korFroms(beta.Card.RelationsIn) {
		if from == korDepID || from == korDeadID {
			t.Fatalf("默认视图不应出现 deprecated/deleted 对端，实际 relations_in from = %v",
				korFroms(beta.Card.RelationsIn))
		}
	}
	if countCode(beta.Diagnostics, CodeQ4) != 1 {
		t.Fatalf("beta 反向隐藏 1 个 deprecated 对端应产恰一条 Q4，实际 %v", codesOf(beta.Diagnostics))
	}

	// --include-deprecated：o-dep 现身（带权威 reason），o-dead 仍隐藏；DeprecatedPeers=[o-dep]，无 Q4。
	beta2, err := bkShow(root, model.CardID(korBetaID), VisibilityPolicy{IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("card show beta（含 deprecated）：%v", err)
	}
	if got := korFroms(beta2.Card.RelationsIn); strings.Join(got, ",") !=
		korAlphaID+","+korDepID+","+korPoriID {
		t.Fatalf("含 deprecated 时 beta.relations_in = %v，期望 [k-alpha, o-dep, o-pori]", got)
	}
	if got := korReasonOf(beta2.Card.RelationsIn, korDepID); got != korRDepBeta {
		t.Fatalf("放开后 o-dep 反向边 reason 必须权威 %q，实际 %q", korRDepBeta, got)
	}
	if strings.Join(beta2.DeprecatedPeers, ",") != korDepID {
		t.Fatalf("DeprecatedPeers = %v，期望 [o-dep]", beta2.DeprecatedPeers)
	}
	if hasCode(beta2.Diagnostics, CodeQ4) {
		t.Fatalf("显式放开 deprecated 后不应有 Q4，实际 %v", codesOf(beta2.Diagnostics))
	}
	// o-dead(deleted) 任何 flag 都不出现。
	for _, from := range korFroms(beta2.Card.RelationsIn) {
		if from == korDeadID {
			t.Fatalf("已删除对端任何 flag 都必须隐藏，实际出现 %s", korDeadID)
		}
	}
}

// —— ③ 四后端矩阵：healthy / missing / stale / corrupt 业务结果逐字等价 ——

// korProjection 是一次读的业务投影（供跨后端逐字比对；不含降级诊断——那是 backend 的事）。
type korProjection struct {
	CardJSON    string
	CardMissing string
	OpnJSON     string
	OpnMissing  string
	RelInFroms  string
	RelInReason string
}

func korProject(t *testing.T, root string, wantIndex bool) korProjection {
	t.Helper()
	beta, err := bkShow(root, model.CardID(korBetaID))
	if err != nil {
		t.Fatalf("card show beta：%v", err)
	}
	pori, err := osShow(root, korPoriID)
	if err != nil {
		t.Fatalf("opinion show pori：%v", err)
	}
	rv, err := bkRel(root, RelRequest{ID: model.CardID(korBetaID)})
	if err != nil {
		t.Fatalf("rel beta：%v", err)
	}
	if got := beta.backend.UseIndex(); got != wantIndex {
		t.Fatalf("card show 后端与预期不符：useIndex=%v want=%v（%s）",
			got, wantIndex, beta.backend.Message)
	}
	if got := pori.backend.UseIndex(); got != wantIndex {
		t.Fatalf("opinion show 后端与预期不符：useIndex=%v want=%v", got, wantIndex)
	}
	if got := rv.backend.UseIndex(); got != wantIndex {
		t.Fatalf("rel 后端与预期不符：useIndex=%v want=%v", got, wantIndex)
	}
	cb, _ := json.Marshal(beta.Card)
	ob, _ := json.Marshal(pori.Opinion)
	return korProjection{
		CardJSON:    string(cb),
		CardMissing: strings.Join(beta.MissingTargets, ","),
		OpnJSON:     string(ob),
		OpnMissing:  strings.Join(pori.MissingTargets, ","),
		RelInFroms:  strings.Join(korFroms(rv.Data.RelationsIn), ","),
		RelInReason: korReasonOf(rv.Data.RelationsIn, korPoriID),
	}
}

func TestReadPathKOFourBackendEquivalence(t *testing.T) {
	// 基线：healthy 索引（含观点）。
	root := korVault(t)
	bkoBuildIndexWithOpinions(t, root)
	healthy := korProject(t, root, true)

	// missing / corrupt / stale：各自在**新建的 healthy vault** 上破坏，再取投影（应降级为扫描）。
	for _, c := range []struct {
		name   string
		break_ func(*testing.T, string)
	}{
		{"missing", bkDropIndex},
		{"corrupt", bkCorruptIndex},
		{"stale", korMakeStale},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := korVault(t)
			bkoBuildIndexWithOpinions(t, r)
			c.break_(t, r)
			got := korProject(t, r, false)
			if got != healthy {
				t.Fatalf("%s 降级后业务投影与 healthy 不等价：\nhealthy=%+v\n%s=%+v",
					c.name, healthy, c.name, got)
			}
		})
	}
}

// —— ④ 读路径对文件 / Git / 索引零副作用 ——

func TestReadPathKOZeroSideEffects(t *testing.T) {
	root := korVault(t)
	before := korSnapshot(t, root)

	// 扫描后端：跑遍四组合读路径。
	if _, err := bkShow(root, model.CardID(korAlphaID)); err != nil {
		t.Fatalf("card show alpha：%v", err)
	}
	if _, err := bkShow(root, model.CardID(korBetaID)); err != nil {
		t.Fatalf("card show beta：%v", err)
	}
	if _, err := osShow(root, korPoriID); err != nil {
		t.Fatalf("opinion show pori：%v", err)
	}
	if _, err := osShow(root, korQuonID); err != nil {
		t.Fatalf("opinion show quon：%v", err)
	}
	if _, err := bkRel(root, RelRequest{ID: model.CardID(korBetaID)}); err != nil {
		t.Fatalf("rel beta：%v", err)
	}

	after := korSnapshot(t, root)
	if before != after {
		t.Fatalf("读路径改动了文件树（零副作用被破坏）：\nbefore=%s\nafter =%s", before, after)
	}
	// 读路径既不建索引，也不建 Git。
	if _, err := os.Stat(filepath.Join(root, ".index")); !os.IsNotExist(err) {
		t.Fatalf("读路径不得创建 .index（err=%v）", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatalf("读路径不得创建 .git（err=%v）", err)
	}
}

// korSnapshot 把 vault 下全部文件折成「路径:大小:mtime」的确定性指纹串。
func korSnapshot(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		lines = append(lines, filepath.ToSlash(rel)+":"+
			itoa(info.Size())+":"+itoa(info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		t.Fatalf("快照 vault 失败：%v", err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
