package query

// [S4 消费面] 含观点语料下**索引后端**返回的 ScanResult 必须与扫描后端同构同值
// （Schema v2 · T-…-066-B 的证据补强批）。
//
// 为什么需要这一支：T-005-B 的 cli 端到端合同证的是「观点是否真的进了 cards / cards_fts /
// files / relations」（写入侧 + 库形态），但**读路径拿到的 ScanResult 是否还自洽**是另一件事。
// 索引后端把观点行挡在 `res.Cards` 之外（默认 search / card / rel 不泄漏观点），
// 同时 `statCardFiles` 的扫描面已经包含 `opinions/`，于是 `ScannedFiles` 里**含**观点文件。
// 这两件事一起意味着：若观点没有被如实放进 `res.Opinions`，`ScanResult` 自己声明的守恒式
//
//	ScannedFiles == len(Cards) + len(Notes) + len(Opinions) + SkippedFiles
//
// 在含观点库上就会不成立，而且 `ScannedFiles` 会与扫描后端分叉 —— 那是「有一条静默路径」
// 的机器信号（scan.go 的 ScanResult 注释把这条守恒式定义为「没有第三条静默路径」的判据）。
//
// 纪律：**不许靠缩小 ScannedFiles 凑绿**。因此本文件同时钉三件互相牵制的事：
//   - 计数守恒式成立；
//   - 两条后端的 ScannedFiles / SkippedFiles **逐字同值**（谁缩谁红）；
//   - ScannedFiles 等于**磁盘上真实存在**的 knowledge ∪ opinions 文件数（含解析不动的那个），
//     即它必须是一个有下界的正数，不能退化成「少扫即绿」。
//
// 文件名以 `index_backed` 起头是**位置锁**要求：取数层只有 backend* / index_backed* /
// degrade* 三个前缀的文件允许 import 索引包（cmd/eg 的 TestStage4IndexPackageBoundary
// 逐文件校验，且**含测试文件**）。

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

// —— 观点语料（validation 三态齐全；令牌是知识卡语料里绝不出现的唯一串）——
const (
	bkoPendingID   = "o-20261201-cost"
	bkoValidatedID = "o-20261201-longseq"
	bkoRejectedID  = "o-20261201-shortseq"

	bkoPendingToken   = "zylphionqk"
	bkoValidatedToken = "qwopraxvtd"
	bkoRejectedToken  = "mnbvcxlkju"
)

// bkoOpinion 造一条最小合法观点（frontmatter 逐字符合 model.Opinion 的键表）。
//
// relations 指向 bkVault 里**真实存在**的知识卡，避免悬空引用（Q2）混进诊断面干扰判据。
func bkoOpinion(id, title, validation, token, relVerb, relTarget string) string {
	return "---\nid: " + id +
		"\nstatus: active" +
		"\ncreated_at: '2026-12-01'" +
		"\nupdated_at: '2026-12-08T10:00:00+08:00'" +
		"\ntitle: " + title +
		"\nvalidation: " + validation +
		"\nsources: []" +
		"\nrelations:\n  - type: " + relVerb + "\n    target: " + relTarget +
		"\n    reason: 与该卡结论的论证关系" +
		"\n---\n\n# " + title +
		"\n\n## 观点\n\n主张一句 " + token + "。\n"
}

// bkoSeed 描述一条要落盘的观点。
type bkoSeed struct {
	id, title, validation, token, relVerb, relTarget string
}

func bkoSeeds() []bkoSeed {
	return []bkoSeed{
		{bkoPendingID, "缩放注意力的经济性存疑", string(index.ValidationPending),
			bkoPendingToken, "supports", "k-20261201-attention"},
		{bkoValidatedID, "长序列下注意力不经济", string(index.ValidationValidated),
			bkoValidatedToken, "limits", "k-20261201-rnn"},
		{bkoRejectedID, "注意力对短序列无益的判断不成立", string(index.ValidationRejected),
			bkoRejectedToken, "opposing", "k-20261201-attention"},
	}
}

// bkoVaultWithOpinions 在 bkVault（3 张可解析卡 + 1 个坏卡 + 另一域 1 张卡）之上
// 落三条真实观点，返回 vault 根。
//
// 沿用 bkVault 的**带坏卡**语料是有意的：`broken.md` 让 SkippedFiles > 0，
// 守恒式因此不是「0 + 0」的平凡等式，而要求四项计数同时对上。
func bkoVaultWithOpinions(t *testing.T) string {
	t.Helper()
	root := bkVault(t)
	for _, s := range bkoSeeds() {
		bkWrite(t, root, store.OpinionRel("ai-infra", s.id),
			bkoOpinion(s.id, s.title, s.validation, s.token, s.relVerb, s.relTarget))
	}
	return root
}

// bkoBuildIndexWithOpinions 建一次**含观点**的全量索引。
//
// 口径与产品写入侧（internal/cli 的 indexSnapshotWith）逐格对称：知识卡 kind=knowledge、
// validation 空；观点 kind=opinion、validation 取 frontmatter 真值；两类都进
// cards / files / relations。这里不能调命令层（§13：query 不依赖 cli），但扫描结果取自
// **产品唯一的扫描底座** VaultScan（含它的 OpinionEntryFrom 映射），因此不是伪造扫描结果。
func bkoBuildIndexWithOpinions(t *testing.T, root string) {
	t.Helper()
	scan, err := VaultScan(root, ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if len(scan.Opinions) == 0 {
		t.Fatal("前置：扫描底座必须已收录观点，否则本支判据失去意义")
	}
	snap := index.Snapshot{}
	add := func(id, path, domain, title, status string, deprecated, deleted bool,
		body string, raw []byte, kind, validation, replacedBy string, rels []struct {
			verb, target string
		}) {
		st, serr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if serr != nil {
			t.Fatalf("stat %s：%v", path, serr)
		}
		hash := store.ContentHash(raw)
		snap.Cards = append(snap.Cards, index.Card{
			ID: id, Path: path, Domain: domain, Title: title, Status: status,
			Deprecated: deprecated, Deleted: deleted, Body: body,
			ContentHash: hash, MTimeUnix: st.ModTime().Unix(),
			Kind: kind, Validation: validation, ReplacedBy: replacedBy,
		})
		snap.Files = append(snap.Files, index.File{
			Path: path, ContentHash: hash, Size: st.Size(), MTimeUnix: st.ModTime().Unix(),
		})
		for _, r := range rels {
			snap.Relations = append(snap.Relations, index.Relation{
				SrcID: id, Verb: r.verb, DstID: r.target, SrcPath: path,
			})
		}
	}
	for _, c := range scan.Cards {
		var rels []struct{ verb, target string }
		for _, r := range c.Relations {
			rels = append(rels, struct{ verb, target string }{string(r.Type), string(r.Target)})
		}
		add(c.ID, c.Path, c.Domain, c.Title, c.Status, c.Deprecated, c.Deleted,
			c.Body(), c.Raw, string(index.CardKindKnowledge), "", c.ReplacedByTarget, rels)
	}
	for _, o := range scan.Opinions {
		var rels []struct{ verb, target string }
		for _, r := range o.Relations {
			rels = append(rels, struct{ verb, target string }{string(r.Type), string(r.Target)})
		}
		add(o.ID, o.Path, o.Domain, o.Title, o.Status, o.Deprecated, o.Deleted,
			o.Body(), o.Raw, string(index.CardKindOpinion), o.Validation, o.ReplacedByTarget, rels)
	}
	if _, err := index.Build(index.DirPath(root), snap,
		index.Options{Now: func() time.Time { return bkFixedNow }}); err != nil {
		t.Fatalf("index.Build：%v", err)
	}
}

// bkoDiskCount 数磁盘上某个领域子目录下的 .md 个数。
func bkoDiskCount(t *testing.T, root, domain, sub string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "domains", domain, sub, "*.md"))
	if err != nil {
		t.Fatalf("glob 失败：%v", err)
	}
	return len(matches)
}

// TestIndexBackendOpinionCountsConservedAndNoLeak —— 含观点语料下索引后端的 ScanResult：
//
//	① 后端确实是索引后端（否则本支退化成又测了一遍扫描后端，判据失效）；
//	② len(Cards) == 磁盘可解析知识卡数，len(Opinions) == 磁盘观点数；
//	③ 守恒式 ScannedFiles == len(Cards)+len(Notes)+len(Opinions)+SkippedFiles 成立；
//	④ ScannedFiles / SkippedFiles 与扫描后端（VaultScan）**逐字同值**，且 Cards / Opinions
//	   两项计数也同值 —— 谁缩小计数凑绿都会在这里红；
//	⑤ ScannedFiles 等于磁盘 knowledge ∪ opinions 的真实文件数（含解析不动的那个），
//	   即它有一个来自磁盘的正下界，不能靠少扫通过；
//	⑥ 默认 Search 的候选与结果里**一条 o-* 都没有**（本批不泄漏观点）。
func TestIndexBackendOpinionCountsConservedAndNoLeak(t *testing.T) {
	root := bkoVaultWithOpinions(t)
	bkoBuildIndexWithOpinions(t, root)

	need := searchNeed(bkDeps)
	b := SelectBackend(root, need)
	if !b.UseIndex() {
		t.Fatalf("① 前置：含观点的健康索引必须走索引后端，实际 kind=%s reason=%s message=%s",
			b.Kind, b.Reason, b.Message)
	}
	res, used, err := loadVault(root, ScanOptions{}, need, b)
	if err != nil {
		t.Fatalf("loadVault：%v", err)
	}
	if used.Kind != BackendIndex {
		t.Fatalf("① 实际取数后端 = %s，期望 %s（本支要测的就是索引后端）", used.Kind, BackendIndex)
	}

	// ② 两类产物的条数各自等于磁盘事实。
	wantOpn := bkoDiskCount(t, root, "ai-infra", "opinions")
	if wantOpn != len(bkoSeeds()) {
		t.Fatalf("前置：磁盘观点数 = %d，期望 %d", wantOpn, len(bkoSeeds()))
	}
	if len(res.Opinions) != wantOpn {
		t.Fatalf("② len(Opinions) = %d，期望 %d（观点必须如实进 res.Opinions）",
			len(res.Opinions), wantOpn)
	}
	// bkVault 的可解析知识卡：attention / rnn / ops / sched 共 4 张（broken.md 解析不动）。
	const wantCards = 4
	if len(res.Cards) != wantCards {
		t.Fatalf("② len(Cards) = %d，期望 %d（观点不得混进 Cards，知识卡不得漏）",
			len(res.Cards), wantCards)
	}

	// ③ 守恒式：ScanResult 自己声明的「没有第三条静默路径」判据。
	if got, want := res.ScannedFiles,
		len(res.Cards)+len(res.Notes)+len(res.Opinions)+res.SkippedFiles; got != want {
		t.Fatalf("③ 守恒式不成立：scanned=%d，cards=%d notes=%d opinions=%d skipped=%d（和 =%d）",
			got, len(res.Cards), len(res.Notes), len(res.Opinions), res.SkippedFiles, want)
	}

	// ④ 与扫描后端逐字同值（四项一起比，任一项被缩小都红）。
	scan, err := VaultScan(root, ScanOptions{})
	if err != nil {
		t.Fatalf("VaultScan：%v", err)
	}
	if res.ScannedFiles != scan.ScannedFiles || res.SkippedFiles != scan.SkippedFiles {
		t.Fatalf("④ 两条后端计数分叉：索引 scanned=%d skipped=%d，扫描 scanned=%d skipped=%d",
			res.ScannedFiles, res.SkippedFiles, scan.ScannedFiles, scan.SkippedFiles)
	}
	if len(res.Cards) != len(scan.Cards) || len(res.Opinions) != len(scan.Opinions) {
		t.Fatalf("④ 两条后端产物条数分叉：索引 cards=%d opinions=%d，扫描 cards=%d opinions=%d",
			len(res.Cards), len(res.Opinions), len(scan.Cards), len(scan.Opinions))
	}
	// 扫描后端自身也必须满足守恒式（否则上面的「同值」可能是两边一起错）。
	if got, want := scan.ScannedFiles,
		len(scan.Cards)+len(scan.Notes)+len(scan.Opinions)+scan.SkippedFiles; got != want {
		t.Fatalf("④ 扫描后端守恒式不成立：scanned=%d，和=%d", got, want)
	}

	// ⑤ ScannedFiles 有来自磁盘的正下界：knowledge ∪ opinions 的真实文件数。
	wantScanned := bkoDiskCount(t, root, "ai-infra", "knowledge") +
		bkoDiskCount(t, root, "mlsys", "knowledge") + wantOpn
	if wantScanned <= wantOpn {
		t.Fatalf("前置：磁盘文件数 %d 不合理（应显著大于观点数 %d）", wantScanned, wantOpn)
	}
	if res.ScannedFiles != wantScanned {
		t.Fatalf("⑤ ScannedFiles = %d，期望 %d（= 磁盘 knowledge ∪ opinions 的真实文件数，"+
			"含解析不动的那个；不许靠少扫凑绿）", res.ScannedFiles, wantScanned)
	}
	if res.SkippedFiles < 1 {
		t.Fatalf("⑤ SkippedFiles = %d，期望 ≥1（语料里有一个解析不动的坏卡，"+
			"守恒式必须在非平凡的跳过数上成立）", res.SkippedFiles)
	}

	// ⑥ res.Cards 与默认 Search 结果都不含观点。
	for _, c := range res.Cards {
		if strings.HasPrefix(c.ID, "o-") || strings.Contains(c.Path, "/opinions/") {
			t.Fatalf("⑥ res.Cards 泄漏观点：id=%s path=%s", c.ID, c.Path)
		}
	}
	// res.Opinions 侧则必须恰是那三条（证明②不是靠把观点丢进别处凑数）。
	var gotOpnIDs []string
	for _, o := range res.Opinions {
		gotOpnIDs = append(gotOpnIDs, o.ID)
	}
	sort.Strings(gotOpnIDs)
	wantOpnIDs := []string{bkoPendingID, bkoRejectedID, bkoValidatedID}
	sort.Strings(wantOpnIDs)
	if strings.Join(gotOpnIDs, ",") != strings.Join(wantOpnIDs, ",") {
		t.Fatalf("② res.Opinions 的 ID 集合 = %v，期望 %v", gotOpnIDs, wantOpnIDs)
	}

	// 默认 Search：既不能命中观点 ID，也不能被观点专属令牌召回。
	sr, err := bkSearch(root, SearchRequest{Query: "注意力 循环 算子 调度"})
	if err != nil {
		t.Fatalf("Search：%v", err)
	}
	if sr.backend.Kind != BackendIndex {
		t.Fatalf("⑥ 前置：Search 必须走索引后端，实际 %s", sr.backend.Kind)
	}
	if len(sr.Hits) == 0 {
		t.Fatal("⑥ 前置：默认 Search 应有知识卡命中，否则「不泄漏」是空断言")
	}
	for _, h := range sr.Hits {
		if strings.HasPrefix(h.ID, "o-") || strings.Contains(h.Path, "/opinions/") {
			t.Fatalf("⑥ 默认 Search 泄漏观点：id=%s path=%s", h.ID, h.Path)
		}
	}
	for _, tok := range []string{bkoPendingToken, bkoValidatedToken, bkoRejectedToken} {
		lr, lerr := bkSearch(root, SearchRequest{Query: tok})
		if lerr != nil {
			t.Fatalf("Search %q：%v", tok, lerr)
		}
		if len(lr.Hits) != 0 {
			t.Fatalf("⑥ 默认 Search 用观点专属令牌 %q 命中 %d 条，期望零命中", tok, len(lr.Hits))
		}
	}
}
