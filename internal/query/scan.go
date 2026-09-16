package query

// M2 查询侧的**唯一扫描入口**（M2 查询合同 `2026-09-19-m2-query-contract.md` §1 / §2 / §3 / §5）。
//
// 一次全库 Markdown 遍历产出统一的卡 / 笔记条目模型 + 诚实诊断，
// `search`（T-…-021）、`card show`（T-…-022）、`rel` 正反向查询（T-…-023）、
// `context` 候选（T-…-025）一律复用它，**不各扫各的**。
//
// 只读：本文件只有 os.Stat / os.ReadDir / os.ReadFile 与一处目录树遍历，
// 没有任何写入、没有任何 git 调用，也没有模型调用与网络请求。
//
// 实现方式（M2 冻结口径）：**直接扫描 Markdown，不建索引、不引数据库、不做增量与并发**，
// 单次命令一次扫描，不设任何时间门槛（索引与检索引擎属 S4）。

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// CardEntry 是一张被扫描到的知识卡。
//
// 字段集是 search / card show / rel / context 四个下游的**公共集合**：一次扫描喂饱四者
// （合同 §9 开工前 checklist 第 1 / 2 项）。Raw 保留原始字节，供调用方算 content_hash
// 或取分区正文——扫描层自己**不改写任何字节**。
type CardEntry struct {
	ID         string
	Path       string // vault 内相对路径（/ 分隔）
	Domain     string
	Title      string
	Tags       []string
	Status     string
	Deprecated bool
	CreatedAt  string
	UpdatedAt  string
	// ReviewedAt 是可选键 reviewed_at 的**逐字原值**，缺省即空串（不回填默认值）。
	// 扫描层只把这个事实带出来；「未过目」的判定归 internal/query/filter 单文件（ADR-20）。
	ReviewedAt string
	// DeletedAt / DeletedReason 是删除维度两键的**逐字原值**，缺省即空串（不回填默认值）；
	// Deleted 是 `deleted_at != null` 这一判定的展开（口径见 markers.go 的 DeletedFromStamp）。
	// 与 status、与 reviewed_at 三者正交：扫描层只把三个事实并列带出，绝不由一个推另一个。
	DeletedAt     string
	DeletedReason string
	Deleted       bool
	Relations     []model.Relation
	Sources       []model.SourceRef
	// ReplacedByTarget 是 `replaced_by.target` 的**逐字原值**，缺省即空串（不回填默认值）。
	// 扫描层解码 frontmatter 时**一并带出**这个事实：对账域 R4 的 `dangling_ref`（E12）
	// 要判「失效卡的替代指针指向不存在的知识卡」，索引层要把它写进 `cards.replaced_by` 列，
	// 两处都读这一个字段 —— 绝不各自再解码一次 frontmatter（同一事实只一处抽取）。
	ReplacedByTarget string
	Raw              []byte
	Doc              *mdfile.Doc

	// Score / MatchedFields 由 Filter 填写（合同 §1.3 的匹配分与命中字段）。
	Score         int
	MatchedFields []string
}

// Body 返回正文全文（frontmatter 之后的原始字节）。
func (c CardEntry) Body() string {
	if c.Doc == nil || c.Doc.BodyFrom > len(c.Raw) {
		return ""
	}
	return string(c.Raw[c.Doc.BodyFrom:])
}

// NoteEntry 是一篇被扫描到的材料笔记（材料层产物，**不进知识收敛**）。
type NoteEntry struct {
	ID        string
	Path      string
	Domain    string
	Source    string
	Tags      []string
	CreatedAt string
	UpdatedAt string
	// ReviewedAt 同 CardEntry.ReviewedAt（四类产物同构：字段名与语义一致）。
	ReviewedAt string
	// DeletedAt / Deleted 同 CardEntry（四类产物同构，删除维度不按产物类型分叉）。
	DeletedAt string
	Deleted   bool
	Raw       []byte
	Doc       *mdfile.Doc
}

// OpinionEntry 是一条被扫描到的观点（schema v2 的第三类领域产物，落在
// `domains/<d>/opinions/o-*.md`）。
//
// 字段集是**对账域**与**派生索引投影**（T-…-066-B）两个消费者的公共集合：前者只需
// ID / 路径 / 领域三个定位事实与三个引用承载面（`relations[]` / `sources[]` /
// `replaced_by.target`）；后者要把观点折成与知识卡同口径的 index.Card 行，因此还需要
// 标题 / 状态 / 失效·删除位 / `validation` 论证进度键，以及原始字节与解析文档（供算
// content_hash 与取正文全文）。检索面的 tags / 计分字段仍不在这里——观点默认不进
// `eg search` 结果集（收窄检索属 T-…-067），先加进来只会得到无人消费的字段。
type OpinionEntry struct {
	ID     string
	Path   string // vault 内相对路径（/ 分隔）
	Domain string
	// Title 取 frontmatter 的 title（可选键，落在 Extra），缺失时退化为 ID——
	// 与 CardEntry.Title 走**同一个** entryTitle，写入侧与读路径投影口径不分叉。
	Title string
	// Tags / CreatedAt / UpdatedAt 是**检索面**（T-…-006-A）需要的三项：kind=opinion|all 的
	// `eg search` 把观点折成与知识卡同构的候选，Filter 用 tags 打分、SortEntries 用
	// updated_at / created_at 做第 ②③ 级全序键。缺省即空 / 空串（不回填默认值），口径与
	// CardEntry 的同名字段逐字相同（同一 entryTags / Stamp.String / Date.String）。
	Tags      []string
	CreatedAt string
	UpdatedAt string
	// Status 是 `status` 的逐字原值；Deprecated 是 `status.Deprecated()` 的展开
	// （与知识卡同口径：状态是一个维度，删除是另一个正交维度）。
	Status     string
	Deprecated bool
	// Deleted 是 `deleted_at != null` 的展开（口径同 CardEntry：DeletedFromStamp）。
	Deleted bool
	// Validation 是 `validation` 论证进度键的逐字原值（观点独有；派生索引的判别行需要它，
	// 且 schema v2 的 CHECK 要求 opinion 行 validation ∈ {pending,validated,rejected}）。
	Validation string
	// Relations 是 `relations[]` 的逐字原值。观点是关系的**持有方**，
	// 而 target 的落盘类型仍是知识卡 ID（model.Relation.Target 是 CardID）：
	// 「观点支持 / 限制 / 反对某张卡」写在观点这一侧，卡侧不写回。
	Relations []model.Relation
	// Sources 是 `sources[]` 的逐字原值（材料层引用：原文 + 来源笔记）。
	Sources []model.SourceRef
	// ReplacedByTarget 是 `replaced_by.target` 的逐字原值（缺省即空串，不回填默认值），
	// 口径与 CardEntry.ReplacedByTarget 逐字相同（同一字段键、同一目标类型）。
	ReplacedByTarget string
	// Raw 保留原始字节（供调用方算 content_hash）；Doc 供取分区正文——扫描层不改写字节。
	Raw []byte
	Doc *mdfile.Doc
}

// Body 返回正文全文（frontmatter 之后的原始字节），口径与 CardEntry.Body 逐字相同。
func (o OpinionEntry) Body() string {
	if o.Doc == nil || o.Doc.BodyFrom > len(o.Raw) {
		return ""
	}
	return string(o.Raw[o.Doc.BodyFrom:])
}

// ScanOptions 是一次扫描的输入。
type ScanOptions struct {
	// Domains 限定扫描的领域；**空 = 全库**（rel 反向查询必须走全库）。
	Domains []string
	// IncludeNotes 决定是否一并扫描 domains/<d>/notes/。
	IncludeNotes bool
	// IncludeDeprecated 登记「失效卡是否进结果」这一维度，合同 §1.5 把它定死为 true：
	// **失效卡同等可见，M2 不提供隐藏失效卡的开关**。因此 VaultScan 不读取本字段——
	// 零值 ScanOptions{} 与 IncludeDeprecated=true 行为完全相同，字段只作为 S2 引入
	// 隐藏开关时的唯一挂点，避免那时再改结构体形状。
	IncludeDeprecated bool
}

// ScanResult 是一次扫描的产物。
//
// 计数守恒（合同 §5.3）：
// ScannedFiles == len(Cards)+len(Notes)+len(Opinions)+SkippedFiles，
// 这是「没有第三条静默路径」的机器判据。
type ScanResult struct {
	Cards []CardEntry
	Notes []NoteEntry
	// Opinions 是观点分区的条目（schema v2）。
	//
	// 为什么**不设开关**、恒随扫描带出：观点是 vault 里真实存在的落盘对象，
	// 对账域要在它上面判关系与结构一致性；若做成可选采样，「没采样」与「不存在」
	// 在下游就不可区分 —— 最直接的后果是「只被观点引用的知识卡」会被误判成零关系孤儿。
	// 它自成一个切片，不与 Cards 合流：检索面读 Cards，因此按 kind 收窄检索
	// （归读路径批次）与本字段互不干扰。
	Opinions     []OpinionEntry
	Diagnostics  []Diagnostic
	ScannedFiles int
	SkippedFiles int
}

// HasQ 报告是否存在任何 Q 类诊断（调用方据此决定人类可读输出要不要打「结果不完整」）。
func (r *ScanResult) HasQ() bool { return len(r.Diagnostics) > 0 }

// VaultScan 一次遍历 vault，产出卡 / 笔记条目 + Q 系列诊断。
//
// **禁止静默跳过**：任何读不动 / 解析不了的 .md 都记一条 Q1 并计入 SkippedFiles，
// 其余合法文件照常返回（一个坏文件不丢全部结果）。
func VaultScan(root string, opt ScanOptions) (*ScanResult, error) {
	res := &ScanResult{Cards: []CardEntry{}, Notes: []NoteEntry{},
		Opinions: []OpinionEntry{}, Diagnostics: []Diagnostic{}}
	domains, err := resolveDomains(root, opt.Domains)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		if err := scanCardDir(root, d, res); err != nil {
			return nil, err
		}
		if err := scanOpinionDir(root, d, res); err != nil {
			return nil, err
		}
		if opt.IncludeNotes {
			if err := scanNoteDir(root, d, res); err != nil {
				return nil, err
			}
		}
	}
	sort.SliceStable(res.Cards, func(i, j int) bool { return res.Cards[i].Path < res.Cards[j].Path })
	sort.SliceStable(res.Notes, func(i, j int) bool { return res.Notes[i].Path < res.Notes[j].Path })
	sort.SliceStable(res.Opinions, func(i, j int) bool {
		return res.Opinions[i].Path < res.Opinions[j].Path
	})
	res.Diagnostics = append(res.Diagnostics, duplicateIDDiagnostics(res.Cards)...)
	if len(opt.Domains) == 0 {
		// 悬空引用只在**全库**扫描面上判定：受限扫描面看不见他域的卡，
		// 在那里判 Q2 会把「没扫到」误报成「不存在」（合同 §5.1 的 Q2 以全库为准）。
		res.Diagnostics = append(res.Diagnostics, danglingDiagnostics(res.Cards)...)
	}
	res.Diagnostics = finalizeDiagnostics(res.Diagnostics)
	return res, nil
}

// resolveDomains 把 ScanOptions.Domains 归一成实际要遍历的领域目录名。
// 空 = 全库：按 domains/ 下的子目录名字典序枚举（目录不存在 → 空集合，不是错误）。
func resolveDomains(root string, want []string) ([]string, error) {
	if len(want) > 0 {
		out := append([]string{}, want...)
		sort.Strings(out)
		return out, nil
	}
	entries, err := os.ReadDir(filepath.Join(root, dirDomains))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// scanCardDir 扫描单个领域的 knowledge/。
func scanCardDir(root, domain string, res *ScanResult) error {
	dir := filepath.Join(root, dirDomains, domain, dirKnowledge)
	return walkMarkdown(dir, root, func(rel string, raw []byte) error {
		res.ScannedFiles++
		entry, bad, ok := CardEntryFrom(rel, domain, raw)
		if !ok {
			res.skip(bad) // 已记 Q1 并计入 SkippedFiles，继续遍历（禁止静默跳过）
			return nil
		}
		res.Cards = append(res.Cards, entry)
		return nil // 正常收录：无 Diagnostic
	})
}

// CardEntryFrom 把一个卡文件的原始字节折成 CardEntry，**是全包唯一的
// 「知识卡 Markdown → CardEntry」映射**。
//
// 两条读路径共用它，因此不可能在字段口径或 Q1 文案上漂移：
//   - 全量扫描底座 scanCardDir（M2 · T-…-020，本文件）；
//   - S4 索引后端的**按需解析**（M5 · T-…-067，index_backed.go）——索引只给出
//     「该读哪些文件」，字节到条目的解释仍然只有这一份实现。
//
// 返回值：ok=true 时第一个返回值有效、第二个为零值；ok=false 时第二个是该文件的
// **Q1** 诊断（三种成因逐字保留 M2 的文案：Parse 失败 / frontmatter 解码失败 / 缺 id），
// 调用方必须**同时**记录诊断与计入跳过计数（口径见 ScanResult.skip）。
func CardEntryFrom(rel, domain string, raw []byte) (CardEntry, Diagnostic, bool) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return CardEntry{}, newQ1(rel, "知识卡不可解析，已跳过：%v", err), false
	}
	var card model.Card
	if err := doc.DecodeFM(&card); err != nil {
		return CardEntry{}, newQ1(rel, "知识卡 frontmatter 解码失败，已跳过：%v", err), false
	}
	if card.ID == "" {
		return CardEntry{}, newQ1(rel, "知识卡缺 id，无法定位，已跳过"), false
	}
	return CardEntry{
		ID: string(card.ID), Path: rel, Domain: domain,
		Title: entryTitle(card.Extra, string(card.ID)), Tags: card.Tags,
		Status: string(card.Status), Deprecated: card.Status.Deprecated(),
		CreatedAt: card.CreatedAt.String(), UpdatedAt: card.UpdatedAt.String(),
		ReviewedAt: model.ReviewedAtText(card.ReviewedAt),
		DeletedAt:  stampText(card.DeletedAt), DeletedReason: card.DeletedReason,
		Deleted:   DeletedFromStamp(stampText(card.DeletedAt)),
		Relations: card.Relations, Sources: card.Sources,
		ReplacedByTarget: replacedByTargetOf(card.ReplacedBy), Raw: raw, Doc: doc,
	}, Diagnostic{}, true
}

// replacedByTargetOf 取 `replaced_by.target` 的逐字原值（未设置即空串）。
//
// 与 internal/reverse.go 的 replacedByOf 同一份落盘事实、同一个解码结果：这里在扫描解码
// 时顺手带出，让下游（对账 R4 / 索引构建）**共用**同一个字段而不各自再解码一次 frontmatter。
// 知识卡与观点共用本函数：`replaced_by` 是同一个字段键、同一个目标类型（知识卡 ID），
// 两类产物不该各写一份取值实现。
func replacedByTargetOf(rb *model.ReplacedBy) string {
	if rb == nil {
		return ""
	}
	return rb.Target
}

// scanOpinionDir 扫描单个领域的 opinions/（schema v2 的观点分区）。
func scanOpinionDir(root, domain string, res *ScanResult) error {
	dir := filepath.Join(root, dirDomains, domain, dirOpinions)
	return walkMarkdown(dir, root, func(rel string, raw []byte) error {
		res.ScannedFiles++
		entry, bad, ok := OpinionEntryFrom(rel, domain, raw)
		if !ok {
			res.skip(bad) // 已记 Q1 并计入 SkippedFiles，继续遍历（禁止静默跳过）
			return nil
		}
		res.Opinions = append(res.Opinions, entry)
		return nil // 正常收录：无 Diagnostic
	})
}

// OpinionEntryFrom 把一个观点文件的原始字节折成 OpinionEntry，**是全包唯一的
// 「观点 Markdown → OpinionEntry」映射**（口径同 CardEntryFrom：全量扫描与按需解析共用它）。
//
// 返回值语义与 CardEntryFrom 逐字一致：ok=false 时第二个返回值是该文件的 Q1 诊断，
// 调用方必须**同时**记录诊断与计入跳过计数（见 ScanResult.skip）。
func OpinionEntryFrom(rel, domain string, raw []byte) (OpinionEntry, Diagnostic, bool) {
	doc, err := mdfile.Parse(raw)
	if err != nil {
		return OpinionEntry{}, newQ1(rel, "观点不可解析，已跳过：%v", err), false
	}
	var op model.Opinion
	if err := doc.DecodeFM(&op); err != nil {
		return OpinionEntry{}, newQ1(rel, "观点 frontmatter 解码失败，已跳过：%v", err), false
	}
	if op.ID == "" {
		return OpinionEntry{}, newQ1(rel, "观点缺 id，无法定位，已跳过"), false
	}
	return OpinionEntry{
		ID: string(op.ID), Path: rel, Domain: domain,
		Title:      entryTitle(op.Extra, string(op.ID)),
		Tags:       op.Tags,
		CreatedAt:  op.CreatedAt.String(),
		UpdatedAt:  op.UpdatedAt.String(),
		Status:     string(op.Status),
		Deprecated: op.Status.Deprecated(),
		Deleted:    DeletedFromStamp(stampText(op.DeletedAt)),
		Validation: string(op.Validation),
		Relations:  op.Relations, Sources: op.Sources,
		ReplacedByTarget: replacedByTargetOf(op.ReplacedBy),
		Raw:              raw, Doc: doc,
	}, Diagnostic{}, true
}

// scanNoteDir 扫描单个领域的 notes/。
func scanNoteDir(root, domain string, res *ScanResult) error {
	dir := filepath.Join(root, dirDomains, domain, dirNotes)
	return walkMarkdown(dir, root, func(rel string, raw []byte) error {
		res.ScannedFiles++
		doc, err := mdfile.Parse(raw)
		if err != nil {
			res.skip(newQ1(rel, "材料笔记不可解析，已跳过：%v", err))
			return nil
		}
		var note model.Note
		if err := doc.DecodeFM(&note); err != nil {
			res.skip(newQ1(rel, "材料笔记 frontmatter 解码失败，已跳过：%v", err))
			return nil // 已记 Q1 并计入 SkippedFiles，继续遍历（禁止静默跳过）
		}
		if note.ID == "" {
			res.skip(newQ1(rel, "材料笔记缺 id，无法定位，已跳过"))
			return nil // 同上：Q1 已记录
		}
		res.Notes = append(res.Notes, NoteEntry{
			ID: string(note.ID), Path: rel, Domain: domain, Source: string(note.Source),
			Tags: note.Tags, CreatedAt: note.CreatedAt.String(),
			UpdatedAt:  note.UpdatedAt.String(),
			ReviewedAt: model.ReviewedAtText(note.ReviewedAt),
			DeletedAt:  stampText(note.DeletedAt),
			Deleted:    DeletedFromStamp(stampText(note.DeletedAt)),
			Raw:        raw, Doc: doc,
		})
		return nil // 正常收录：无 Diagnostic
	})
}

// skip 记一条 Q1 并计入 SkippedFiles（两件事永远一起发生，避免出现「记了不算」或「算了不记」）。
func (r *ScanResult) skip(d Diagnostic) {
	r.Diagnostics = append(r.Diagnostics, d)
	r.SkippedFiles++
}

// entryTitle 取标题：frontmatter 的 title（可选键，落在 Extra），缺失时退化为 ID。
func entryTitle(extra map[string]interface{}, fallback string) string {
	if v, ok := extra["title"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return fallback
}

// duplicateIDDiagnostics 报同一稳定 ID 出现在多个文件的情况（合同 §2.2）：
// 记 Q1 但**不跳过**（两份都照常返回，由调用方按路径字典序取小者）。
func duplicateIDDiagnostics(cards []CardEntry) []Diagnostic {
	seen := map[string]string{}
	var out []Diagnostic
	for _, c := range cards {
		if first, ok := seen[c.ID]; ok {
			out = append(out, newQ1(c.Path, "知识卡 ID %s 重复：同 ID 已出现在 %s，两份均照实输出", c.ID, first))
			continue
		}
		seen[c.ID] = c.Path
	}
	return out
}

// danglingDiagnostics 报悬空引用（Q2）：relations[].target 在全库中不存在。
// 条目本身**不隐藏**，只是附一条诊断（合同 §5.1）。
func danglingDiagnostics(cards []CardEntry) []Diagnostic {
	known := map[string]bool{}
	for _, c := range cards {
		known[c.ID] = true
	}
	var out []Diagnostic
	for _, c := range cards {
		for _, rel := range c.Relations {
			target := string(rel.Target)
			if target == "" || known[target] {
				continue
			}
			out = append(out, newQ2(c.Path,
				"悬空引用：relations[] 指向的目标卡 %s 在库中不存在（关系 type=%s，from=%s）",
				target, rel.Type, c.ID))
		}
	}
	return out
}

// —— 提案控制面：不进知识扫描面，只出「标题 + targets」摘要（T-…-033）——
//
// 依据：提案合同 §7.1（`proposals/` 一项一文件、不属于任何领域）、§10.3
// （**S2 靠目录过滤实现**，S4 起改由索引 kind 列过滤，**口径不变**）、§6.2 末段
// （提案不参与标记与检索）。
//
// 两个面严格分开，不能混：
//   - **知识扫描面**（`eg search` / `eg card show` / `eg rel` / 综述取材）：
//     由 skipDirName 在**遍历入口** SkipDir，提案文件根本不会被读取、解析，
//     也就绝不会计入 ScannedFiles / SkippedFiles，更不会变成 Q1 噪声。
//   - **去重摘要面**（`eg context`）：由 ProposalSummaries 单独读 `proposals/`，
//     **只交出标题与 targets，不交正文**——七个 H2 的内容一个字都不进上下文。

// dirProposals 是提案目录名（vault 根）。口径与 store / internal/proposal 两侧的
// 布局常量同源；§13 的依赖方向不允许 query 依赖它们，故这里各自持有同一份字面量。
const dirProposals = "proposals"

// skipDirName 报告遍历知识扫描面时是否整棵 SkipDir 该目录。
//
// 三项整棵 SkipDir：`.git`（版本库自身）、`.index`（S4 的派生索引目录，S2 不建不读）、
// `proposals`（提案控制面）。这是**唯一**一处「知识扫描面排除表」，
// search / card show / rel / context 共用；此处只判目录名，**不读任何索引**。
func skipDirName(name string) bool {
	return name == ".git" || name == ".index" || name == dirProposals // 三者一律整棵 SkipDir
}

// IsProposalPath 报告 vault 内相对路径是否属提案控制面（供调用方做越界反证）。
func IsProposalPath(rel string) bool {
	parts := strings.Split(path.Clean(rel), "/")
	return len(parts) >= 2 && parts[0] == dirProposals
}

// ProposalSummary 是一条提案摘要：**只有标题与 targets**。
//
// 刻意不含正文字段：没有 Body、没有分区、没有 impact / decision / execution。
// 结构体形状本身就是「只给摘要」的机器判据——上下文里没有承载提案正文的字段。
type ProposalSummary struct {
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	Title   string   `json:"title"`
	Targets []string `json:"targets"`
}

// proposalFM 是提案 frontmatter 里**摘要用得到的两键**（提案合同 §7.2 的 id / targets）。
// §13 的依赖方向不允许 query 依赖 internal/proposal，故这里只声明摘要必需的最小字段；
// 提案的完整 schema 只有 internal/proposal 一份定义，两侧都只做只读解析、不做 YAML 回写。
type proposalFM struct {
	ID      string   `yaml:"id"`
	Targets []string `yaml:"targets"`
}

// ProposalSummaries 读 `proposals/` 下的提案，产出去重摘要 + Q1 诊断。
//
// 只读：os.ReadDir + os.ReadFile。目录不存在不是错误（S1 / M3 都允许 `proposals/` 缺席，
// 提案合同 §7.1「S1 是否需存在：可以不存在」）。
// 排序按 vault 内相对路径升序，结果与文件系统返回序无关（可复算）。
//
// **不计入任何扫描计数**：ScanResult 的 ScannedFiles / SkippedFiles 与本函数无关，
// 因此「提案不进知识检索」与「scanned_files 不含提案」两件事互不干扰。
func ProposalSummaries(root string) ([]ProposalSummary, []Diagnostic, error) {
	out := []ProposalSummary{}
	diags := []Diagnostic{}
	dir := filepath.Join(root, dirProposals)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, diags, nil
	}
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".md" {
			// 子目录与非 .md 不在提案面内：与知识扫描面同口径，不记 Diagnostic。
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		rel := path.Join(dirProposals, name)
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, err
		}
		doc, err := mdfile.Parse(raw)
		if err != nil {
			diags = append(diags, newQ1(rel, "提案不可解析，已跳过：%v", err))
			continue // Q1 已记录，继续遍历（禁止静默跳过）
		}
		var fm proposalFM
		if err := doc.DecodeFM(&fm); err != nil {
			diags = append(diags, newQ1(rel, "提案 frontmatter 解码失败，已跳过：%v", err))
			continue
		}
		if fm.ID == "" {
			diags = append(diags, newQ1(rel, "提案缺 id，无法定位，已跳过"))
			continue
		}
		out = append(out, ProposalSummary{
			ID: fm.ID, Path: rel, Title: proposalTitle(raw, doc, fm.ID), Targets: fm.Targets,
		})
	}
	return out, diags, nil
}

// proposalTitle 取提案标题：正文首个 H1（`# …`）。
//
// 提案 frontmatter **没有** title 键（合同 §7.2 键集合恰 8 个），因此标题的权威载体是
// 正文 H1（F6：Markdown 是权威）。缺 H1 时退化为 ID —— 与卡片标题缺失时的退化口径一致。
// 只读首个 H1 一行，**不碰任何 H2 分区**：正文内容不会因此泄漏进上下文。
func proposalTitle(raw []byte, doc *mdfile.Doc, fallback string) string {
	end := len(raw)
	if len(doc.Sections) > 0 {
		end = doc.Sections[0].Start
	}
	for at := doc.BodyFrom; at < end; {
		stop := end
		if i := bytes.IndexByte(raw[at:end], '\n'); i >= 0 {
			stop = at + i
		}
		line := raw[at:stop]
		if bytes.HasPrefix(line, []byte("# ")) {
			return string(bytes.TrimRight(line[len("# "):], " \t\r"))
		}
		if stop >= end {
			break
		}
		at = stop + 1
	}
	return fallback
}

// FilterSpec 是查询过滤条件（合同 §1.1 的参数表）。零值表示「不过滤」。
type FilterSpec struct {
	Query  string   // 关键词串；空串 = 不按关键词过滤（全部保留、匹配分为 0）
	Domain string   // 领域；空 = 不限
	Tags   []string // 多值为 AND，逐字相等比较
	Since  string   // updated_at 日期部分 >= Since（YYYY-MM-DD，闭区间）
	Until  string   // updated_at 日期部分 <= Until（闭区间）
}

// Filter 按合同 §1.1 过滤、按 §1.3 打匹配分，并填 MatchedFields。
//
// 确定性：只依赖输入卡与条件本身，无时间因素、无随机、无语料相关权重（如 IDF），
// 同一输入两次调用输出逐字相同。
func Filter(cards []CardEntry, spec FilterSpec) []CardEntry {
	want := tokens(spec.Query)
	out := []CardEntry{}
	for _, c := range cards {
		if spec.Domain != "" && c.Domain != spec.Domain {
			continue
		}
		if !hasAllTags(c.Tags, spec.Tags) {
			continue
		}
		day := dateOf(c.UpdatedAt)
		if spec.Since != "" && (day == "" || day < spec.Since) {
			continue
		}
		if spec.Until != "" && (day == "" || day > spec.Until) {
			continue
		}
		hit := c
		hit.Score, hit.MatchedFields = scoreEntry(want, c)
		if len(want) > 0 && hit.Score == 0 {
			continue // 关键词一个都没命中：不进结果集（合同 §1.3 第 3 条）
		}
		out = append(out, hit)
	}
	return out
}

// scoreEntry 按合同 §1.3 计分：每个查询词命中 title +3、命中 tags +2、命中正文 +1，
// 每个词对每个字段至多计一次；命中字段按 title / tags / body 固定次序去重输出。
func scoreEntry(want map[string]bool, c CardEntry) (int, []string) {
	if len(want) == 0 {
		return 0, []string{}
	}
	title := tokens(c.Title)
	body := tokens(c.Body())
	tagToks := make([]map[string]bool, 0, len(c.Tags))
	for _, tag := range c.Tags {
		tagToks = append(tagToks, tokens(tag))
	}
	score := 0
	var inTitle, inTags, inBody bool
	for _, tok := range sortedKeys(want) {
		if title[tok] {
			score += 3
			inTitle = true
		}
		for _, tt := range tagToks {
			if tt[tok] {
				score += 2
				inTags = true
				break
			}
		}
		if body[tok] {
			score++
			inBody = true
		}
	}
	fields := []string{}
	for _, f := range []struct {
		name string
		hit  bool
	}{{"title", inTitle}, {"tags", inTags}, {"body", inBody}} {
		if f.hit {
			fields = append(fields, f.name)
		}
	}
	return score, fields
}

// hasAllTags 判定 AND 语义的标签过滤（逐字相等）。
func hasAllTags(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, t := range have {
		set[t] = true
	}
	for _, t := range want {
		if !set[t] {
			return false
		}
	}
	return true
}

// dateOf 取时间戳的日期部分（前 10 字节）。非法 / 空值返回空串。
func dateOf(stamp string) string {
	if len(stamp) < 10 {
		return ""
	}
	return stamp[:10]
}

// SortEntries 就地做合同 §1.4 的**四级全序**排序：
//
//	① 匹配分降序 → ② updated_at 倒序 → ③ created_at 倒序 → ④ id 升序
//
// 第 ④ 级保证全序（id 全库唯一），因此结果与输入顺序无关、与文件系统返回序无关：
// 同一输入连续两次执行输出逐字相同。
func SortEntries(cards []CardEntry) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.UpdatedAt != b.UpdatedAt {
			return a.UpdatedAt > b.UpdatedAt
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt > b.CreatedAt
		}
		return a.ID < b.ID
	})
}

// RelationEdge 是一条论证关系（正向 / 反向同构，合同 §3.1 的五键）。
type RelationEdge struct {
	From   string `json:"from"`
	Type   string `json:"type"`
	Target string `json:"target"`
	Reason string `json:"reason"`
	Path   string `json:"path"`
}

// RelationsOut 返回本卡 frontmatter relations[] 的正向关系（合同 §3.2），已排序。
func RelationsOut(card CardEntry) []RelationEdge {
	out := []RelationEdge{}
	for _, rel := range card.Relations {
		out = append(out, RelationEdge{From: card.ID, Type: string(rel.Type),
			Target: string(rel.Target), Reason: rel.Reason, Path: card.Path})
	}
	SortEdges(out, func(e RelationEdge) string { return e.Target })
	return out
}

// RelationsIn 全库反向扫描出指向 id 的关系（合同 §3.2），已排序。
// **不走任何索引**：入参就是 VaultScan 的全库结果。
func RelationsIn(cards []CardEntry, id string) []RelationEdge {
	out := []RelationEdge{}
	for _, c := range cards {
		if c.ID == id {
			continue
		}
		for _, rel := range c.Relations {
			if string(rel.Target) != id {
				continue
			}
			out = append(out, RelationEdge{From: c.ID, Type: string(rel.Type),
				Target: string(rel.Target), Reason: rel.Reason, Path: c.Path})
		}
	}
	SortEdges(out, func(e RelationEdge) string { return e.From })
	return out
}

// walkMarkdown 只读遍历目录下的 Markdown。**本包唯一一处目录树遍历实现**：
// context.go 与 scan.go 共用它，不存在第二份遍历实现。
//
// 遍历口径（与 M1 逐字一致，不得放宽）：跳过 .git 与索引目录、只收 .md、
// 目录不存在不是错误（骨架允许缺项）。
// M3（T-…-033）追加一项**只加严**的过滤：提案目录整棵跳过，见 skipDirName。
func walkMarkdown(dir, root string, fn func(rel string, raw []byte) error) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		// 目录缺失时没有任何文件可扫，因此也没有可记的 Diagnostic（不是「静默跳过文件」）。
		return nil
	}
	return walkMarkdownPaths(dir, root, func(rel, full string) error {
		raw, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		return fn(rel, raw)
	})
}

// walkMarkdownPaths 是**全包唯一**的目录树遍历实现（M5 · T-…-067 抽出）。
//
// 抽出理由：S4 的索引后端需要一次**只 stat 不读字节**的走查（拿到「本次读的扫描面上
// 有哪些 .md」以及每个文件的 size / mtime），而 M2 的 walkMarkdown 恒读字节。
// 若各写一个 WalkDir，遍历口径（跳 `.git` / `.index` / `proposals`、只收 `.md`、
// 目录缺失不是错误）就会有第二份实现并随时间漂移 —— 因此这里把「遍历」与「读字节」
// 分层：walkMarkdown = 本函数 + os.ReadFile，索引后端 = 本函数 + os.Stat。
// 遍历口径仍然只有这一处，一格未放宽。
func walkMarkdownPaths(dir, root string, fn func(rel, full string) error) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		// 目录缺失时没有任何文件可扫，因此也没有可记的 Diagnostic（不是「静默跳过文件」）。
		return nil
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirName(d.Name()) {
				return fs.SkipDir
			}
			// 目录本身不是查询对象：不进结果、不记 Diagnostic（Q1 只针对 .md 文件）。
			return nil
		}
		if path.Ext(d.Name()) != ".md" {
			// 非 .md 不在查询面内：同样不记 Diagnostic（Q1 只针对 .md 文件）。
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), p)
	})
}
