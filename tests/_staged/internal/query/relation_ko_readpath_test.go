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
	"crypto/sha256"
	"encoding/hex"
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

	// I-…-007：贝塔卡 / 波里观点各挂一个非固定分区的唯一令牌，用于四后端等价 + 零副作用
	// 负控里逐字反证 unknown_sections 的取数（索引后端也须回权威 Markdown 派生同一份内容）。
	korBetaUnknownTok = "zkorbetaunknowntok"
	korPoriUnknownTok = "zkorporiunknowntok"
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
	// I-…-007：贝塔（反向目标卡）额外挂一个非固定（v1 遗留）分区，让四后端等价与零副作用负控
	// 真实覆盖 unknown_sections 的取数——索引后端也必须回权威 Markdown 逐字派生同一份未知分区。
	bkWrite(t, root, k+"k-20270101-beta.md", bkCard(korBetaID, "贝塔", "active",
		"2027-01-02T10:00:00+08:00", "  - 核心\n", "")+
		"\n## 解释与依据\n\n贝塔遗留依据 "+korBetaUnknownTok+"。\n")
	bkWrite(t, root, k+"k-20270101-noise.md", bkCard(korNoiseID, "噪声", "active",
		"2027-01-02T10:00:00+08:00", "  - 核心\n", ""))
	bkWrite(t, root, store.OpinionRel("ai-infra", korPoriID),
		korOpinion(korPoriID, "波里", "active", "pending", "",
			korRel("supports", korBetaID, korRPoriBeta)+
				korRel("limits", korQuonID, korRPoriQuon))+
			"\n## 附录\n\n波里附录正文 "+korPoriUnknownTok+"。\n")
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

// korReasonToTarget 按 target 取 reason（正向段用：正向边 from 恒为焦点，对端在 target 位）。
func korReasonToTarget(edges []RelationEdge, target string) string {
	for _, e := range edges {
		if e.Target == target {
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
	rv, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korBetaID)})
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

// —— ②′ `rel` 焦点为观点端点（o-id）：正向读自身 relations[]、反向全库反查 `*→o` ——

// TestReadPathKORelOpinionFocus 钉 `RelView(o-id)` 这条核心 API：焦点端点是观点时，正向
// 读观点**自身** relations[]（o→k / o→o），反向在 Knowledge ∪ Opinion 全库反查谁指向了它
// （k→o / o→o 都如实现身）。这是 8b833a6 缺的第一处核心 API：焦点实体必须从
// Knowledge ∪ Opinion 联合定位，`RelView(o-id)` 的正反向因此都可用。
func TestReadPathKORelOpinionFocus(t *testing.T) {
	root := korVault(t)

	// rel o-pori：正向自身 relations[]。SortEdges 按关系类型 opposing→limits→supports→derives
	// 排序，故 limits(o-quon) 在 supports(k-beta) 之前；两条对端一个是观点(o→o)、一个是卡(o→k)。
	pori, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korPoriID)})
	if err != nil {
		t.Fatalf("rel pori：%v", err)
	}
	if pori.backend.UseIndex() {
		t.Fatalf("前置：无索引应走扫描后端，实际 %s", pori.backend.Kind)
	}
	if pori.Data.ID != korPoriID {
		t.Fatalf("rel data.id 必须回显焦点端点 %s，实际 %q", korPoriID, pori.Data.ID)
	}
	if got := targetsOf(pori.Data.RelationsOut); strings.Join(got, ",") != korQuonID+","+korBetaID {
		t.Fatalf("pori.relations_out = %v，期望 [o-quon(o→o,limits), k-beta(o→k,supports)]", got)
	}
	// 正向边 from 恒为焦点自身，reason 逐字取权威。
	for _, e := range pori.Data.RelationsOut {
		if e.From != korPoriID {
			t.Fatalf("正向边 from 必须恒为焦点 %s，实际 %s", korPoriID, e.From)
		}
	}
	if got := korReasonToTarget(pori.Data.RelationsOut, korBetaID); got != korRPoriBeta {
		t.Fatalf("o→k 正向边 reason 应为权威原值 %q，实际 %q", korRPoriBeta, got)
	}
	// 反向：k-alpha 以 k→o 指向 o-pori（*→o 真实边必须现身，reason 权威）。
	if got := korFroms(pori.Data.RelationsIn); strings.Join(got, ",") != korAlphaID {
		t.Fatalf("pori.relations_in = %v，期望 [k-alpha(k→o 反向真实边)]", got)
	}
	if got := korReasonOf(pori.Data.RelationsIn, korAlphaID); got != korRAlphaPori {
		t.Fatalf("k→o 反向边 reason 应为权威原值 %q，实际 %q", korRAlphaPori, got)
	}

	// rel o-quon：正向为空；反向出现 o→o 真实边（o-pori→o-quon）。
	quon, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korQuonID)})
	if err != nil {
		t.Fatalf("rel quon：%v", err)
	}
	if len(quon.Data.RelationsOut) != 0 {
		t.Fatalf("o-quon 无正向关系，relations_out 应为空，实际 %v", targetsOf(quon.Data.RelationsOut))
	}
	if got := korFroms(quon.Data.RelationsIn); strings.Join(got, ",") != korPoriID {
		t.Fatalf("quon.relations_in = %v，期望 [o-pori(o→o 反向真实边)]", got)
	}
	if got := korReasonOf(quon.Data.RelationsIn, korPoriID); got != korRPoriQuon {
		t.Fatalf("o→o 反向边 reason 应为权威原值 %q，实际 %q", korRPoriQuon, got)
	}
}

// —— ②″ `--to` 校验与存在性统一为 k/o 端点宇宙；补 To=o-id 的真实正 / 反向证据 ——

// TestReadPathKORelToEndpoint 钉 8b833a6 缺的第二处核心 API：`--to` 收窄到 k/o 端点，
// 存在性判定走**统一端点宇宙**（Knowledge ∪ Opinion）。逐项覆盖：
//   - To=o-id 命中真实正向边（k→o、o→o）；
//   - To=o-id 命中真实反向边（焦点为卡、对端为观点的 `o→k` 反向）；
//   - To 指向存在但无边的 o-id → 正反向皆空，且**不在无 --to 基线上新增 Q2**（端点存在，只是没边）；
//   - To 指向库中不存在的端点 → 正反向皆空 + 在基线上**恰新增一条 Q2**（对端不存在优于条目照常输出）。
//
// korVault 里 k-alpha→o-ghost 的悬空边恒产一条**全局** Q2（danglingDiagnostics 扫全库卡），
// 故这里以「无 --to 基线 Q2 计数」为参照断言 --to 是否新增 Q2，稳健于全局悬空噪声。
func TestReadPathKORelToEndpoint(t *testing.T) {
	root := korVault(t)

	// baseQ2 = 某焦点在**无 --to** 时的 Q2 计数（含全局悬空噪声）。
	baseQ2 := func(focus string) int {
		rv, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(focus)})
		if err != nil {
			t.Fatalf("baseline rel %s：%v", focus, err)
		}
		return countCode(rv.Diagnostics, CodeQ2)
	}

	// ① To=o-pori（k→o 正向）：k-alpha --to o-pori 只留正向 k-alpha→o-pori，反向为空；不新增 Q2。
	kToPori, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korAlphaID), To: korPoriID})
	if err != nil {
		t.Fatalf("rel k-alpha --to o-pori：%v", err)
	}
	if got := targetsOf(kToPori.Data.RelationsOut); strings.Join(got, ",") != korPoriID {
		t.Fatalf("k-alpha --to o-pori 正向 = %v，期望恰 [o-pori]（k→o 真实正向边）", got)
	}
	if got := korReasonToTarget(kToPori.Data.RelationsOut, korPoriID); got != korRAlphaPori {
		t.Fatalf("k→o 正向边 reason 应权威 %q，实际 %q", korRAlphaPori, got)
	}
	if len(kToPori.Data.RelationsIn) != 0 {
		t.Fatalf("k-alpha 无反向来源，--to o-pori 反向应为空，实际 %v", korFroms(kToPori.Data.RelationsIn))
	}
	if got, base := countCode(kToPori.Diagnostics, CodeQ2), baseQ2(korAlphaID); got != base {
		t.Fatalf("--to 指向存在的 o-pori 不应新增 Q2：期望 %d 实际 %d（%v）", base, got, codesOf(kToPori.Diagnostics))
	}

	// ② To=o-quon（o→o 正向）：o-pori --to o-quon 只留正向 o-pori→o-quon。
	poriToQuon, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korPoriID), To: korQuonID})
	if err != nil {
		t.Fatalf("rel o-pori --to o-quon：%v", err)
	}
	if got := targetsOf(poriToQuon.Data.RelationsOut); strings.Join(got, ",") != korQuonID {
		t.Fatalf("o-pori --to o-quon 正向 = %v，期望恰 [o-quon]（o→o 真实正向边）", got)
	}

	// ③ To=o-pori（反向命中）：k-beta --to o-pori 只留反向 o-pori→k-beta（对端是观点的 o→k 反向边）。
	betaToPori, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korBetaID), To: korPoriID})
	if err != nil {
		t.Fatalf("rel k-beta --to o-pori：%v", err)
	}
	if len(betaToPori.Data.RelationsOut) != 0 {
		t.Fatalf("k-beta 无正向关系，--to o-pori 正向应为空，实际 %v", targetsOf(betaToPori.Data.RelationsOut))
	}
	if got := korFroms(betaToPori.Data.RelationsIn); strings.Join(got, ",") != korPoriID {
		t.Fatalf("k-beta --to o-pori 反向 = %v，期望恰 [o-pori]（o→k 真实反向边）", got)
	}
	if got := korReasonOf(betaToPori.Data.RelationsIn, korPoriID); got != korRPoriBeta {
		t.Fatalf("反向边 reason 应权威 %q，实际 %q", korRPoriBeta, got)
	}

	// ④ To 指向存在但无此边的 o-id：k-beta --to o-quon → 正反向皆空，且**不新增 Q2**（o-quon 真实存在）。
	betaToQuon, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korBetaID), To: korQuonID})
	if err != nil {
		t.Fatalf("rel k-beta --to o-quon：%v", err)
	}
	if len(betaToQuon.Data.RelationsOut) != 0 || len(betaToQuon.Data.RelationsIn) != 0 {
		t.Fatalf("k-beta 与 o-quon 之间无边，正反向都应为空，实际 out=%v in=%v",
			targetsOf(betaToQuon.Data.RelationsOut), korFroms(betaToQuon.Data.RelationsIn))
	}
	if got, base := countCode(betaToQuon.Diagnostics, CodeQ2), baseQ2(korBetaID); got != base {
		t.Fatalf("--to 指向存在的 o-quon（只是无边）不应新增 Q2：期望 %d 实际 %d（%v）", base, got, codesOf(betaToQuon.Diagnostics))
	}

	// ⑤ To 指向库中不存在的 o-id：k-beta --to o-ghost → 正反向皆空 + 在基线上恰新增一条 Q2。
	// 统一端点宇宙里查不到 o-ghost，故按「对端不存在」置空并记这条 Q2（口径优于条目照常输出）。
	betaToGhost, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korBetaID), To: korGhostID})
	if err != nil {
		t.Fatalf("rel k-beta --to o-ghost：%v", err)
	}
	if len(betaToGhost.Data.RelationsOut) != 0 || len(betaToGhost.Data.RelationsIn) != 0 {
		t.Fatalf("--to 指向不存在的 o-ghost 应把正反向置空，实际 out=%v in=%v",
			targetsOf(betaToGhost.Data.RelationsOut), korFroms(betaToGhost.Data.RelationsIn))
	}
	if got, base := countCode(betaToGhost.Diagnostics, CodeQ2), baseQ2(korBetaID); got != base+1 {
		t.Fatalf("--to 指向不存在端点应恰新增一条 Q2：期望 %d 实际 %d（%v）", base+1, got, codesOf(betaToGhost.Diagnostics))
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
	if _, err := bkRel(root, RelRequest{ID: model.RelationEndpoint(korBetaID)}); err != nil {
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

// korSnapshot 把 vault 下全部文件折成「路径:大小:mtime纳秒:内容sha256」的确定性指纹串。
// 内容哈希使读前后比对不止看大小 / mtime，还逐字节比内容——覆盖 `.index/eg.db`（含内容与
// mtime）、Markdown 源文件与 `.git`（若存在），任何一格被读路径改动都会立刻被这串指纹揪出。
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
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(raw)
		lines = append(lines, filepath.ToSlash(rel)+":"+
			itoa(info.Size())+":"+itoa(info.ModTime().UnixNano())+":"+hex.EncodeToString(sum[:]))
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
