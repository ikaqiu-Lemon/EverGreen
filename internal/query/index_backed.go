package query

// [S4] 索引后端：**候选集召回 + 按需解析**（M5 索引架构合同 §5.1 / §7.4；T-…-067）。
//
// 本文件只做两件事，且都不产生新的业务口径：
//
//	① 探测（probeIndex）：索引现在能不能用、是不是落后于权威 —— 结论一律来自
//	   internal/index 的既有单点（Inspect / ReadFiles / QuickUnchanged），
//	   本文件不写第二套健康判定，也不写 W22/W23/W24 的字面量；
//	② 取数（indexVault）：用索引给出的**候选集**决定「该解析哪些权威文件」，
//	   把结果折成与 VaultScan **同构**的 ScanResult 交给下游。
//
// 下游一个函数都不用改口径：可见性过滤（VisibleEndpoints）、`[失效]` 标记（markers.go）、
// 正反向关系（RelationsOut / RelationsIn / SortEdges）、悬空与重复诊断
// （danglingDiagnostics / duplicateIDDiagnostics）、Q1–Q4 组装（diagnostic.go）
// **一律复用既有实现**，本文件不重新实现其中任何一个（风险 R-25）。
//
// 计数单源（I-…-002「计数双源」教训）：`ScannedFiles` / `SkippedFiles` 的口径与扫描
// 后端**同一个定义** —— ScannedFiles = 本次扫描面上的 `.md` 文件总数、
// SkippedFiles = 其中解析不了的个数，且守恒式 `Scanned == len(Cards)+len(Notes)+Skipped`
// 在两条后端上都成立（TestIndexBackendCountsConserved）。索引侧**不自造**第二套计数：
// 索引的 `files` 表只用来判「有没有变」，绝不用来回答「扫了几个文件」。
//
// 陈旧判定（A-44，逐字遵守，无本地变体）：水位线 = `(head, files_hash)`，
// `files_hash` 由**每文件 content_hash** 聚合；`(size, mtime)` 只作**快路径过滤**
// （命中 ⇒ 允许沿用索引里的 content_hash），三者任一不一致就**回权威重算 content_hash
// 再判定** —— 因此「摸了一下 mtime 但内容没变」必须仍判新鲜，`mtime` 不作最终结论。
//
// 判定实现**不在本文件**：三态结论一律来自 `index.Check`，与 `eg index status`（默认
// 快路径）**同一个函数**，故「什么算陈旧」在读路径与体检命令两处不可能分叉。本文件只
// 负责把「权威现态」按同一口径组装出来（currentWatermarkInput）。
//
// `content_hash` 与 `head` 这两处口径分别属 `internal/store`（B3）与 `internal/git`，
// 查询层依施工索引 §13 一个都不能 import ⇒ 由命令层**注入**（backend.go 的 IndexDeps）。
// 没注入就证不出新鲜度，**证不出就不用索引**（走扫描，且不算降级：见
// ReasonFreshnessUnverifiable）。宁可多扫一遍，绝不拿证不出新鲜的索引出结果。

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/index"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// errIndexBehindAuthority 是「索引落后于权威」的哨兵：indexVault 在按需解析时认出
// 「权威库有、索引 files 表没有」的卡即 wrap 它，调用方（degrade.go 的 loadVault）
// 据此把本次读整体退回扫描后端并留痕 W22（陈旧），**不**把它变成命令失败。
var errIndexBehindAuthority = errors.New("索引落后于权威 Markdown")

// indexProbe 是一次读路径索引探测的结果（只读，零副作用）。
type indexProbe struct {
	// dir 是 `.index/` 绝对路径；root 是 vault 根。
	dir  string
	root string
	// diag 是 internal/index 的只读体检结论（healthy / missing / corrupt）。
	diag index.Diagnosis
	// present 是**现态**扫描面上的 .md 文件（stat 走查，不读字节），按 path 升序。
	present []index.File
	// indexed 是索引 `files` 表的记录（按 path 索引）。它与 `cards` 表**不是同一个集合**：
	// 构建侧的快照来自扫描结果，故同 ID 重复的两份文件都在 `files` 里，`cards` 因主键
	// 只留一份 —— 「present 里有、files 里没有」才是「索引没见过这个文件」。
	indexed map[string]index.File
	// con 是 `index.Check` 给出的完整一致性结论（仅在新鲜度**判得出来**时有意义，
	// 即 diag.Usable() 且 !unverifiable）。stale / staleReason / staleDetail 是它的
	// 三格投影，供 SelectBackend 直接取用 —— 不是第二套判定。
	con         index.Consistency
	stale       bool
	staleReason string
	staleDetail string
	// unverifiable 为真 = 本次读**证不出**索引的新鲜度（未注入 A-44 口径，或权威
	// Markdown 读不动）；unverifiableWhy 是逐字原因。此时 con 为零值。
	unverifiable    bool
	unverifiableWhy string
}

// freshnessUnknown 是「新鲜度未判定」的内部取值（空串）。
//
// 刻意**不**复用 index 的三态：Freshnesses() 是索引面的封闭集合，三值各自是一项事实
// 断言（跟得上 / 落后了 / 库不可用），而「本次读证不出来」不是关于索引的事实，是关于
// 本次调用的事实。硬塞进三态里会让某一个码变成含义模糊的兜底值。
// 它只出现在包内探测结论里，不进任何 CLI 输出。
const freshnessUnknown = index.Freshness("")

// freshness 把探测结论折成合同 §5.2 的三态（取值一律来自 internal/index 的封闭集合）。
func (p indexProbe) freshness() index.Freshness {
	switch {
	case !p.diag.Usable():
		return index.FreshnessUnusable
	case p.unverifiable:
		return freshnessUnknown // 未判定：既不宣称新鲜，也不诬告陈旧
	case p.stale:
		return index.FreshnessStale
	default:
		return index.FreshnessFresh
	}
}

// probeIndex 探测索引可用性与新鲜度。**永不返回 error**：索引异常是诊断不是失败
// （合同 §6.1），走查权威目录失败时也只保守判为「不可用于本次读」。
func probeIndex(root string, deps IndexDeps) indexProbe {
	p := indexProbe{root: root, dir: index.DirPath(root)}
	p.diag = index.Inspect(p.dir)
	present, err := statCardFiles(root)
	if err != nil {
		// 连扫描面都走查不动：这次读证不出索引新鲜（扫描后端会在读字节时把同一个错误如实上抛）。
		return p.unverifiableBecause(fmt.Sprintf("权威目录走查失败：%v", err))
	}
	p.present = present
	if !p.diag.Usable() {
		return p // 库不可用时无从比对新鲜度（Freshness 由 diag 决定为 unusable）。
	}
	indexed, err := index.ReadFiles(p.dir)
	if err != nil {
		return p.unverifiableBecause(fmt.Sprintf("索引 files 表读不出来：%v", err))
	}
	p.indexed = make(map[string]index.File, len(indexed))
	for _, f := range indexed {
		p.indexed[f.Path] = f
	}
	if !deps.complete() {
		return p.unverifiableBecause("本次调用未注入 A-44 水位线口径" +
			"（B3 content_hash 与 Git HEAD 由命令层注入，见 IndexDeps）")
	}
	files, err := currentWatermarkInput(root, deps, p.indexed, present)
	if err != nil {
		return p.unverifiableBecause(fmt.Sprintf("权威 Markdown 读不动，无法算现态水位线：%v", err))
	}
	// 权威**卡投影**：把现态扫描面上每个能解析的知识卡折成与 build 同口径的 index.Card
	// （含 Body / content_hash），交给 index.Check 做**行级**核对（I-…-024）。没有它，
	// Check 只能比水位线与表间自洽，会漏掉「删 cards_fts 行 / 翻转 deleted·deprecated /
	// 等行数替换 id·content_hash」这类**库结构合法但对权威撒谎**的行级损坏。
	cards, err := currentCardsInput(root, deps, present)
	if err != nil {
		return p.unverifiableBecause(fmt.Sprintf("权威 Markdown 读不动，无法算权威卡投影：%v", err))
	}
	// 三态结论**只从这一处来**：与 `eg index status`（默认快路径）同一个 index.Check。
	p.con = index.Check(p.dir, index.Current{Head: deps.Head(), Files: files, Cards: cards})
	if p.con.Unusable() {
		// 行级核对把一个「结构合法、水位线一致」的库判成了不可用（W24 /
		// row_level_divergence）：让探测结论整体以 con.Diagnosis 为准 —— freshness() 与
		// SelectBackend 都读 p.diag，据此把本次读整体降级为全量扫描并留痕 W24 + Q5
		// （合同 §5.2 / §6.3）。不在这里「半索引半扫描」地带病用索引。
		p.diag = p.con.Diagnosis
		return p
	}
	p.stale = p.con.Stale()
	p.staleReason, p.staleDetail = p.con.Reason, p.con.Message
	return p
}

// unverifiableBecause 把探测结论标成「新鲜度证不出来」并附逐字原因。
//
// 刻意**不**标成陈旧：陈旧是对索引的一项事实断言（「它落后了」），而这里的事实是
// 「本次读没法证明它跟得上」——两者不是同一件事，混用会让 W22 变成一个含义模糊的码。
func (p indexProbe) unverifiableBecause(why string) indexProbe {
	p.unverifiable, p.unverifiableWhy = true, why
	return p
}

// currentWatermarkInput 组装 `index.Check` 需要的**权威现态**（合同 §5.1 / A-44）。
//
// 口径与 `eg index status`（默认快路径）**逐格相同**，因此「什么算陈旧」在读路径与
// 体检命令两处不可能分叉：
//
//	① 只有**解析得动的知识卡**进水位线：解析不了的文件在 `eg index status` 那边也不在
//	   `scan.Cards` 里（Q1 已经如实登记过），它们不算陈旧、也不算新鲜；
//	② `(path, size, mtime)` 与 `files` 表逐格一致 ⇒ 沿用索引里的 `content_hash`
//	   （A-44 允许且指定的快路径，省掉一次全库读盘）；
//	③ 三者任一不一致 ⇒ **回权威重算** `content_hash` 再判定 —— A-44 逐字禁止
//	   「以 mtime 作最终结论」，因此「摸了一下 mtime 但内容没变」必须仍判 fresh。
//
// `content_hash` 由注入的 B3 口径（store.ContentHash）计算：查询层不自造第二套 hash。
// 返回 error 只有一种情形：**权威 Markdown 读不动**（磁盘 / 权限）——那与索引无关，
// 调用方据此判为「新鲜度证不出来」并走扫描，由扫描后端把同一个错误如实上抛。
func currentWatermarkInput(root string, deps IndexDeps, indexed map[string]index.File,
	present []index.File) ([]index.File, error) {
	files := make([]index.File, 0, len(present))
	for _, cur := range present {
		if prev, ok := indexed[cur.Path]; ok && index.QuickUnchanged(prev, cur) {
			files = append(files, index.File{Path: cur.Path, ContentHash: prev.ContentHash,
				Size: cur.Size, MTimeUnix: cur.MTimeUnix})
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cur.Path)))
		if err != nil {
			return nil, err
		}
		if !parseableAt(cur.Path, raw) {
			continue // 解析不动 ⇒ 不进水位线（与 status 侧的扫描收录口径一致，按分区分流解析器）
		}
		files = append(files, index.File{Path: cur.Path, ContentHash: deps.Hash(raw),
			Size: cur.Size, MTimeUnix: cur.MTimeUnix})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// currentCardsInput 组装 `index.Check` 行级核对所需的**权威卡投影**（I-…-024）。
//
// 与 build / status 侧的快照**同口径**：每个能解析的知识卡折成 index.Card，字段取自
// 全包唯一的 CardEntryFrom（因此 title / status / deprecated / deleted / replaced_by /
// Body 与扫描后端逐字一致），content_hash 走注入的 B3 口径（deps.Hash，与写口同源）——
// 查询层不自造第二套解析，也不自造第二套 hash。
//
// 为什么必须逐个解析（哪怕水位线快路径判「未变」）：行级核对要比的是 `cards` /
// `cards_fts` 的**每一列**是否对权威撒谎，而 deprecated / deleted / title 这些列只有解析
// frontmatter 才拿得到 —— 水位线的 (size, mtime) 快路径给不出它们。这一次全解析是
// I-…-024 为「读路径不被索引行级撒谎骗到」付出的正确性代价；窄化召回的性能形态属 T-…-068。
//
// 解析不动的文件不进投影（与扫描后端的 Q1 / skipped 口径一致：它们本就不该在 cards 表里）。
// 返回 error 只有一种情形：**权威 Markdown 读不动**（磁盘 / 权限）——调用方据此判为
// 「新鲜度证不出来」并走扫描，由扫描后端把同一个错误如实上抛。
func currentCardsInput(root string, deps IndexDeps, present []index.File) ([]index.Card, error) {
	cards := make([]index.Card, 0, len(present))
	for _, cur := range present {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(cur.Path)))
		if err != nil {
			return nil, err
		}
		// 按分区分流解析器（与写入侧快照逐字对称）：观点走 OpinionEntryFrom 折出
		// kind=opinion、validation 取真值的投影；知识卡走 CardEntryFrom、kind=knowledge、
		// validation 恒空。两侧都用全包唯一的映射与注入的 B3 hash，绝不自造第二套。
		if isOpinionPath(cur.Path) {
			entry, _, ok := OpinionEntryFrom(cur.Path, domainOfPath(cur.Path), raw)
			if !ok {
				continue // 解析不动 ⇒ 不进权威投影（与 cards 表的收录口径一致）
			}
			cards = append(cards, index.Card{
				ID: entry.ID, Path: entry.Path, Domain: entry.Domain, Title: entry.Title,
				Status: entry.Status, Deprecated: entry.Deprecated, Deleted: entry.Deleted,
				ReplacedBy: entry.ReplacedByTarget, Body: entry.Body(),
				ContentHash: deps.Hash(raw), MTimeUnix: cur.MTimeUnix,
				Kind: index.CardKindOpinion, Validation: entry.Validation,
			})
			continue
		}
		entry, _, ok := CardEntryFrom(cur.Path, domainOfPath(cur.Path), raw)
		if !ok {
			continue // 解析不动 ⇒ 不进权威投影（与 cards 表的收录口径一致）
		}
		cards = append(cards, index.Card{
			ID: entry.ID, Path: entry.Path, Domain: entry.Domain, Title: entry.Title,
			Status: entry.Status, Deprecated: entry.Deprecated, Deleted: entry.Deleted,
			ReplacedBy: entry.ReplacedByTarget, Body: entry.Body(),
			ContentHash: deps.Hash(raw), MTimeUnix: cur.MTimeUnix,
			// 分型显式给值（Schema v2）：知识卡面逐张 knowledge、validation 恒空。行级核对会把
			// 这两列也逐列对账，因此这里**必须**与写入侧同口径 —— 留空会让每一行都被判成
			// 「判别列对权威撒谎」。
			Kind: index.CardKindKnowledge, Validation: "",
		})
	}
	return cards, nil
}

// statCardFiles 走查**索引对象扫描面**上的全部 `.md`（只 stat 不读字节），按 path 升序。
//
// 扫描面 = 知识卡（`domains/<d>/knowledge/**`）**并集**观点（`domains/<d>/opinions/**`）：
// 与 VaultScan 的对象面逐字相同（两类都进 cards / cards_fts / files / relations，
// schema v2·T-…-066-B）。三处遍历共用 resolveDomains + walkMarkdownPaths 这一处实现，
// 因此「哪些文件算在内」不可能在写入侧快照与读路径投影之间分叉——这正是含观点索引仍判
// healthy 的前提：若读路径的 `present` 少了观点，files 表里的 o-* 行在水位线核对时就成了
// 「有行无现态文件」，含观点的库每次读都会被误判成陈旧而降级。
func statCardFiles(root string) ([]index.File, error) {
	domains, err := resolveDomains(root, nil)
	if err != nil {
		return nil, err
	}
	out := []index.File{}
	statInto := func(dir string) error {
		return walkMarkdownPaths(dir, root, func(rel, full string) error {
			st, err := os.Stat(full)
			if err != nil {
				return err
			}
			out = append(out, index.File{
				Path: rel, Size: st.Size(), MTimeUnix: st.ModTime().Unix(),
			})
			return nil
		})
	}
	for _, d := range domains {
		if err := statInto(filepath.Join(root, dirDomains, d, dirKnowledge)); err != nil {
			return nil, err
		}
		if err := statInto(filepath.Join(root, dirDomains, d, dirOpinions)); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// parsePlan 是「这次索引读要回权威解析哪些文件」的完整计划（由 Need 派生，见 planFor）。
type parsePlan struct {
	// focus 是焦点卡 ID（`card show` / `rel`）；search 无焦点即空串。
	focus string
	// all 为真 = 候选集取**全集**并逐个解析（search：需要 tags / updated_at / 正文原值）。
	all bool
	// domains 限定扫描面（空 = 全库），口径与 ScanOptions.Domains 逐字相同。
	domains []string
}

// planFor 把 Need 与本次扫描面折成解析计划：**唯一**的 Need → plan 映射。
func planFor(need Need, opt ScanOptions) parsePlan {
	return parsePlan{focus: need.Focus, all: need.FullCardFields, domains: opt.Domains}
}

// indexVault 用索引后端组装一份与 VaultScan **同构**的 ScanResult。
//
// 候选集与「必须回权威解析的文件集」按 plan 决定，两种形态：
//
//	① plan.all（`eg search`）：候选集 = 扫描面上的**全部** `.md`，逐个回权威解析。
//	   保守到底、零窄化 ⇒ 结果集与全量扫描**构造性等价**（同一批文件、同一个
//	   CardEntryFrom），假阴性在原理上不可能出现。索引在这条路径上提供的事实是
//	   「在册卡集合」与「哪些文件索引里没有」，不提供也不假装提供 tags / updated_at。
//	② plan.focus（`eg card show` / `eg rel`）：只解析
//
//	   {焦点实体自身（卡或观点）} ∪ {relations.dst_id = focus 的反向来源（卡或观点）} ∪ {索引未收录的文件}
//
// 三段的必要性与充分性（等价性证明，逐条对应下游消费者）：
//
//	① 焦点实体：五分区正文 / tags / 时间戳 / sources[] / 正向 relations[] 全部只在它自己的
//	   文件里 ⇒ 解析它一个文件即可，且必须解析（索引不存正文与 reason）。焦点是知识卡走 need、
//	   是观点走 needOpinion（`RelView(o-id)` 的正向边取观点自身 relations[] 的逐字 reason）；
//	② 反向来源（卡或观点）：`relations` 表存**全部**正向边（含观点持有的 `o-* → k-*`），按
//	   `dst_id` 反查即得「谁指向了 focus」的精确集合（无假阴性）；每条边的 `reason` 回源文件取
//	   逐字原值 ⇒ 必须解析这几个文件——来源是知识卡走 need，是观点走 needOpinion（因索引
//	   relations 表不存 reason，观点 stub 的 relBySrc 反查会漏 reason，必须回权威）；
//	③ 索引未收录的文件：`files` / `cards` 只收**解析得动**的卡（构建侧的快照就是扫描结果，
//	   见 internal/cli 的 indexSnapshotWith），故「现态走查到但索引 cards 里没有」的文件恰是
//	   「解析不了的（Q1 / skipped）」「同 ID 重复被主键收掉的第二份」与「索引建好之后**新增**
//	   的卡」三类 ⇒ 解析它们才能如实复现 Q1 与 SkippedFiles 计数、「同 ID 重复」诊断
//	   （按路径字典序取小者的定位口径），以及把新增卡**如实纳入结果**而不是漏掉。
//
// 其余卡只需要 `id / path / domain / title / status / deprecated / deleted` 与**正向边**
// 这几项事实（可见性过滤、悬空判定、反向来源定位都只用到它们），全部在索引里，
// 因此以**摘要条目**（stub）进入 Cards：不读它们的字节，也不假装读过。
//
// 摘要条目的边界（写死在类型里而不是靠自律）：stub 的 Raw / Doc 恒为 nil、
// Tags / Sources 恒为空、时间戳恒为空串 —— 任何需要这些字段的读路径都不可能只拿 stub
// 就产出结果：`card show` / `rel` 只对**焦点卡与反向来源（卡或观点）**取这些字段（都已解析），
// 而 `eg search` 走 plan.all，一条 stub 都不会留到结果里（TestIndexBackendNoStubLeaks）。
func indexVault(root string, p indexProbe, plan parsePlan) (*ScanResult, error) {
	cards, err := index.ReadCards(p.dir)
	if err != nil {
		return nil, err
	}
	rels, err := index.ReadRelations(p.dir)
	if err != nil {
		return nil, err
	}
	// 扫描面收窄到 plan.domains（口径与 VaultScan 的 ScanOptions.Domains 逐字相同：
	// 领域是**目录事实**，`domains/<d>/knowledge/**` 前缀命中即在面内）。
	present := filterByDomains(p.present, plan.domains)

	// ① 摘要条目：每张索引在册（且在扫描面内）的卡一条，正向边由 relations 表按 src_id 归并。
	relBySrc := map[string][]model.Relation{}
	for _, r := range rels {
		relBySrc[r.SrcID] = append(relBySrc[r.SrcID], model.Relation{
			Type: model.RelationType(r.Verb), Target: model.RelationEndpoint(r.DstID),
		})
	}
	indexedPaths := make(map[string]bool, len(cards))
	// opinionPaths 是索引在册的**观点**行路径集合。默认读路径（`eg search` / `card show` /
	// `rel`）只投知识卡：观点不进 res.Cards，业务 search 因此不泄漏 o-*（按 kind 收窄的
	// 检索语义与 opinion 检索属 T-…-067，本批不提前实现）。但它们的路径仍要登记进
	// indexedPaths —— 否则下面「present 里有、cards 里没有」的新增判定会把观点文件当成
	// 未收录的知识卡去解析，既做无用功又可能把观点误当卡带进结果。
	opinionPaths := make(map[string]bool)
	entries := make([]CardEntry, 0, len(cards))
	// opinions 是从索引在册的观点行折回的 OpinionEntry 摘要（口径同知识卡 stub：
	// 只带 relations 表里已有的定位事实与正向边，不读观点字节）。它们**不进** res.Cards
	// （默认读路径不泄漏观点），但必须进 res.Opinions —— 否则 ScanResult 的守恒式
	// `ScannedFiles == len(Cards)+len(Notes)+len(Opinions)+SkippedFiles` 在含观点库上不成立，
	// 且与扫描后端（VaultScan 把观点计入 ScannedFiles 又放进 Opinions）的计数分叉。
	opinions := make([]OpinionEntry, 0)
	for _, c := range cards {
		if !inDomains(c.Path, plan.domains) {
			continue
		}
		indexedPaths[c.Path] = true
		if c.Kind == index.CardKindOpinion {
			opinionPaths[c.Path] = true
			opinions = append(opinions, OpinionEntry{
				ID: c.ID, Path: c.Path, Domain: c.Domain, Title: c.Title,
				Status: c.Status, Deprecated: c.Deprecated, Deleted: c.Deleted,
				Validation: c.Validation, ReplacedByTarget: c.ReplacedBy,
				Relations: relBySrc[c.ID],
			})
			continue // 观点不进默认读结果集（不泄漏）；与 VaultScan 把观点放在 res.Opinions 对称
		}
		entries = append(entries, CardEntry{
			ID: c.ID, Path: c.Path, Domain: c.Domain, Title: c.Title,
			Status: c.Status, Deprecated: c.Deprecated, Deleted: c.Deleted,
			Relations: relBySrc[c.ID],
		})
	}

	// ② 必须解析的权威文件：索引未收录者（Q1 / 重复 / 新增）+ 焦点卡 + 反向来源卡；
	//    plan.all 时是扫描面上的全部文件。
	res := &ScanResult{Cards: []CardEntry{}, Notes: []NoteEntry{}, Diagnostics: []Diagnostic{}}
	res.ScannedFiles = len(present)
	parsed := map[string]CardEntry{}
	// unparsable 是本次已判 Q1 的文件：它们**不是**索引的问题（`files` 表天生不收
	// 解析不了的文件），因此既不重复解析，也绝不升级成「索引落后于权威」。
	unparsable := map[string]bool{}
	for _, f := range present {
		if indexedPaths[f.Path] {
			continue
		}
		entry, bad, ok := parseCardAt(root, f.Path)
		if !ok {
			res.skip(bad) // Q1 与 SkippedFiles 同时发生（口径同扫描后端）
			unparsable[f.Path] = true
			continue
		}
		if _, known := p.indexed[f.Path]; !known {
			// 解析得动、却连 `files` 表都没有它 ⇒ 索引建好之后新增的卡 ⇒ 索引确实陈旧。
			// 不在这里「顺手带上」：读路径的降级语义是**整体**退回扫描（合同 §5.2），
			// 半索引半扫描的混合态既说不清也没法复算。
			return nil, fmt.Errorf("%w：%s 在权威库里存在且可解析，索引 files 表里没有",
				errIndexBehindAuthority, f.Path)
		}
		parsed[f.Path] = entry
		entries = append(entries, entry)
	}
	need := map[string]bool{}
	// needOpinion 承接**反向来源是观点**的路径（plan.focus：card show / rel）：schema v2 写路径
	// 允许 `o-* → k-*`，故焦点卡的反向来源可能是观点文件。索引 relations 表**不存 reason**
	// （只有 src_id/verb/dst_id/src_path/line），若只拿观点 stub 的 relBySrc 反查，o→k 反向边的
	// reason 会是空串，与扫描后端（回权威取逐字 reason）分叉。因此这些观点必须回权威解析，
	// 与 plan.all 的观点解析共用 parsedOpinions（下面的覆盖循环据此把 stub 换成权威条目）。
	needOpinion := map[string]bool{}
	// parsedOpinions 承接 plan.all（`eg search`）下**回权威解析**的观点全条目。
	// 默认读路径（plan.focus：card show / rel）不投观点，因此这个 map 恒空、观点保持摘要态；
	// 只有 search 需要观点的 tags / created_at / updated_at / 正文来打分与四级全序（kind
	// 收窄属 T-…-006-A），此时**必须**读观点字节 —— 摘要 stub 的这些字段是空的，用它
	// 直接检索会与扫描后端分叉（打分 0、时间戳空导致排序错位）。
	parsedOpinions := map[string]OpinionEntry{}
	if plan.all {
		for _, f := range present {
			if unparsable[f.Path] {
				continue // 已记 Q1（跳过）：口径与扫描后端一致
			}
			if opinionPaths[f.Path] {
				// 观点行：回权威解析成与 VaultScan 逐字同构的 OpinionEntry（同一 OpinionEntryFrom）。
				oe, _, ok := parseOpinionAt(root, f.Path)
				if !ok {
					// 索引说这里有一条可解析的观点，现在解析不了 ⇒ 索引与权威不一致：
					// 整体退回扫描后端（degrade.go 的 loadVault 留痕 W22 + Q5），不半索引半扫描。
					return nil, fmt.Errorf("索引记录的观点文件 %s 现在解析不了", f.Path)
				}
				parsedOpinions[f.Path] = oe
				continue
			}
			need[f.Path] = true
		}
	}
	if plan.focus != "" {
		for _, e := range entries {
			if e.ID == plan.focus {
				need[e.Path] = true
			}
		}
		// 焦点本身也可能是**观点**（`RelView(o-id)`）：其自身 relations[] 的逐字 reason 只在
		// 观点文件里，索引 relations 表不存 reason，故焦点观点必须回权威解析（走 needOpinion，
		// 下面的覆盖循环把 stub 换成权威条目，o→k / o→o 的正向 reason 便与扫描后端逐字一致）。
		for _, o := range opinions {
			if o.ID == plan.focus {
				needOpinion[o.Path] = true
			}
		}
		for _, r := range rels {
			if r.DstID != plan.focus || !inDomains(r.SrcPath, plan.domains) {
				continue
			}
			// 反向来源既可能是知识卡也可能是观点（schema v2 允许 `o-* → k-*`）：
			// 观点来源回权威解析取逐字 reason（索引 relations 不存 reason），知识卡来源照旧。
			if opinionPaths[r.SrcPath] {
				needOpinion[r.SrcPath] = true
			} else {
				need[r.SrcPath] = true
			}
		}
	}
	for rel := range need {
		if _, done := parsed[rel]; done {
			continue
		}
		entry, _, ok := parseCardAt(root, rel)
		if !ok {
			// 索引说这里有一张可解析的卡，现在解析不了 ⇒ 索引与权威已经不一致。
			// 这不是「静默跳过」：调用方（degrade.go 的 loadVault）会据此把本次读整体
			// 退回扫描后端，由权威 Markdown 给出唯一答案，并如实留痕 W22 + Q5。
			return nil, fmt.Errorf("索引记录的卡文件 %s 现在解析不了", rel)
		}
		parsed[rel] = entry
	}
	// plan.focus 下反向来源是观点的，回权威解析补齐逐字 reason（与 plan.all 共用 parsedOpinions；
	// 下面的覆盖循环据此把观点 stub 换成权威条目，o→k 反向边的 reason 便与扫描后端逐字一致）。
	for rel := range needOpinion {
		if _, done := parsedOpinions[rel]; done {
			continue
		}
		oe, _, ok := parseOpinionAt(root, rel)
		if !ok {
			// 索引说这里有一份可解析的观点，现在解析不了 ⇒ 索引与权威不一致：
			// 整体退回扫描后端（degrade.go 的 loadVault 留痕 W22 + Q5），不半索引半扫描。
			return nil, fmt.Errorf("索引记录的观点文件 %s 现在解析不了", rel)
		}
		parsedOpinions[rel] = oe
	}
	// 用解析结果覆盖同路径的摘要条目：同一路径只留一条，且优先留「真读过字节」的那条。
	for i := range entries {
		if full, ok := parsed[entries[i].Path]; ok {
			entries[i] = full
		}
	}

	// ③ 归一化：与 VaultScan 逐字同序（按 path 升序稳定排序），再走同一套诊断组装。
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	res.Cards = entries
	// 观点摘要同样按 path 升序（与 VaultScan 的 res.Opinions 排序口径逐字相同）；它们不参与
	// 卡面的重复 / 悬空诊断（那两条只在 Cards 上判），仅用于把守恒式与计数对齐扫描后端。
	// plan.all（search）下用回权威解析的全条目覆盖同路径摘要：观点候选因此带齐 tags /
	// 时间戳 / 正文，kind 收窄检索与扫描后端逐字一致（同一路径只留一条，优先「真读过字节」）。
	for i := range opinions {
		if full, ok := parsedOpinions[opinions[i].Path]; ok {
			opinions[i] = full
		}
	}
	sort.SliceStable(opinions, func(i, j int) bool { return opinions[i].Path < opinions[j].Path })
	res.Opinions = opinions
	res.Diagnostics = append(res.Diagnostics, duplicateIDDiagnostics(res.Cards)...)
	if len(plan.domains) == 0 {
		// 悬空引用（Q2）只在**全库**面上判定：与 VaultScan 逐字同一条件
		// （受限扫描面看不见他域的卡，在那里判 Q2 会把「没扫到」误报成「不存在」）。
		res.Diagnostics = append(res.Diagnostics, danglingDiagnostics(res.Cards, res.Opinions)...)
	}
	res.Diagnostics = finalizeDiagnostics(res.Diagnostics)
	return res, nil
}

// filterByDomains 把走查到的文件收窄到指定领域（空 = 全库，原样返回）。
func filterByDomains(files []index.File, domains []string) []index.File {
	if len(domains) == 0 {
		return files
	}
	out := make([]index.File, 0, len(files))
	for _, f := range files {
		if inDomains(f.Path, domains) {
			out = append(out, f)
		}
	}
	return out
}

// inDomains 判一个 vault 内相对路径是否落在指定领域内（空集合 = 全库，恒真）。
func inDomains(rel string, domains []string) bool {
	if len(domains) == 0 {
		return true
	}
	d := domainOfPath(rel)
	for _, want := range domains {
		if d == want {
			return true
		}
	}
	return false
}

// parseCardAt 读一个卡文件并折成 CardEntry（领域名由 vault 内相对路径推出）。
//
// 字节 → 条目的映射复用 CardEntryFrom（全包唯一实现），因此 Q1 文案与字段口径
// 与扫描后端逐字相同。读不动文件时也走 Q1（与 walkMarkdown 的错误面不同：那里
// 读失败是整次扫描的 error，这里是单文件降级，故如实记 Q1 而不是中断整次读）。
func parseCardAt(root, rel string) (CardEntry, Diagnostic, bool) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return CardEntry{}, newQ1(rel, "知识卡不可解析，已跳过：%v", err), false
	}
	return CardEntryFrom(rel, domainOfPath(rel), raw)
}

// parseOpinionAt 读一个观点文件并折成 OpinionEntry（领域名由 vault 内相对路径推出）。
//
// 字节 → 条目的映射复用全包唯一的 OpinionEntryFrom，因此 tags / 时间戳 / 正文 / Q1 文案
// 与扫描后端逐字相同。**所有** `eg search` 都走 plan.all（与 --kind 无关），因此每次 search
// 都会在此回权威解析观点字节；kind 收窄发生在下游（candidatesForKind）——默认 kind=knowledge
// 只是不把解析出的观点投进 hits，而不是不解析。card show / rel 走 plan.focus，不解析观点，
// 不会走到这里。读不动 / 解析不动即 ok=false，调用方据此整体降级为扫描后端。
func parseOpinionAt(root, rel string) (OpinionEntry, Diagnostic, bool) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return OpinionEntry{}, newQ1(rel, "观点不可解析，已跳过：%v", err), false
	}
	return OpinionEntryFrom(rel, domainOfPath(rel), raw)
}

// domainOfPath 从 `domains/<d>/knowledge/<file>.md` 取领域名（取不到即空串）。
// 口径与扫描后端一致：领域是**目录事实**，不从 frontmatter 猜。
func domainOfPath(rel string) string {
	parts := strings.Split(path.Clean(rel), "/")
	if len(parts) >= 2 && parts[0] == dirDomains {
		return parts[1]
	}
	return ""
}

// isOpinionPath 判一个 vault 内相对路径是否落在观点分区 `domains/<d>/opinions/**`。
// 领域产物的类型是**目录事实**（与 domainOfPath 同源口径），不从 frontmatter 的 id 前缀猜——
// 读路径据此决定该走 CardEntryFrom 还是 OpinionEntryFrom，与写入侧快照的分流逐字对称。
func isOpinionPath(rel string) bool {
	parts := strings.Split(path.Clean(rel), "/")
	return len(parts) >= 3 && parts[0] == dirDomains && parts[2] == dirOpinions
}

// parseableAt 判一个现态文件在其分区口径下能否解析成领域对象（知识卡或观点）。
// 与写入侧快照的收录口径逐字一致：解析不动的文件既不进水位线也不进权威投影
// （它们本就不该在 cards / files 表里，扫描后端对它们记 Q1、计入 skipped）。
func parseableAt(rel string, raw []byte) bool {
	if isOpinionPath(rel) {
		_, _, ok := OpinionEntryFrom(rel, domainOfPath(rel), raw)
		return ok
	}
	_, _, ok := CardEntryFrom(rel, domainOfPath(rel), raw)
	return ok
}
