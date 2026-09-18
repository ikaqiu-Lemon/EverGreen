package query

// `eg context` 的白名单上下文组装（技术方案 §8 第 2 步、§4.5 的 base、§4.2；T-…-010）。
//
// 只读：本文件**只有 os.ReadFile / WalkDir**，没有任何写入、没有任何 git 调用，
// 也不做任何模型调用与网络请求——候选相似卡的打分必须是确定性算法（同输入同输出）。
//
// 白名单（EG-EXT-01）四项：① 目标原文正文；② 该原文在本领域**已有的材料笔记**；
// ③ 同领域 `active` 知识卡；④ 候选相似卡（供 Agent 做收敛判断）。
//
// EG-NOTE-04：材料笔记是**材料层**产物，不参与知识收敛——只出现在 Notes（材料层字段），
// 打分输入集合在 candidates() 之前就把 `kind='note'` 过滤掉，笔记永不进候选相似卡。
// EG-KNW-02 / EG-CHK-03 / EG-DOM-02：只输出 `status: active` 的卡，且**只扫描本领域目录**，
// 他域产物在扫描阶段就进不来。
//
// S1 实现方式：**直接扫描 Markdown**，不依赖索引目录（索引属 S4）。
// M2（T-…-020）起，本文件的遍历与解析改为复用 scan.go 的 VaultScan / walkMarkdown 同一实现，
// 不可解析文件不再静默跳过，一律产出 Q1 诊断并透出到 Context.Diagnostics。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
)

// vault 布局常量（冻结合同 F1 的 [S1] 列）。口径与 store 包的 layout.go 同源；
// §13 的依赖方向不允许 query 依赖 store 包，故这里各自持有同一份字面量。
const (
	unprocessedFile = "unprocessed.md"
	dirSources      = "sources"
	dirDomains      = "domains"
	dirNotes        = "notes"
	dirKnowledge    = "knowledge"
	// dirOpinions 是 schema v2 新增的观点分区（`domains/<d>/opinions/`）。
	// `eg init` 的骨架里没有它：一个领域的第一条观点才会创建该目录，
	// 因此遍历侧「目录缺失不是错误」这一口径对它同样成立。
	dirOpinions = "opinions"
)

// CandidateLimit 是候选相似卡的输出上限（S1 不做分页 / 四级排序 / 截断标记，那是 S4）。
const CandidateLimit = 10

var (
	// ErrTargetNotFound 目标对象（原文 / 笔记）在库中不存在 → 上层退 2。
	ErrTargetNotFound = errors.New("目标对象不存在")
	// ErrHasherRequired 未注入 content_hash 计算口径。
	//
	// B3 的比对依据只有一份实现（store.ContentHash）：query 不另起一套算法，
	// 而是由调用方把 store 的口径注入进来，保证 context 交出的 hash 与 apply 写前重算逐字一致。
	ErrHasherRequired = errors.New("缺 content_hash 计算口径（须注入 store.ContentHash）")
)

// Hasher 计算 content_hash。唯一实现是 store.ContentHash（B3 同源口径）。
type Hasher func(content []byte) string

// Request 是一次 eg context 的输入。Source / Note 二选一。
type Request struct {
	Root   string
	Domain string
	Source string
	Note   string
}

// SourceView 是目标原文（材料层）。Body 是正文全文（frontmatter 之后的原始字节）。
type SourceView struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	SavedAt string `json:"saved_at"`
	Body    string `json:"body"`
}

// NoteView 是一篇材料笔记（材料层字段，**不进收敛输入**）。
type NoteView struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

// CardView 是一张同领域 active 知识卡（收敛输入）。
type CardView struct {
	ID    string   `json:"id"`
	Path  string   `json:"path"`
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

// Candidate 是一张候选相似卡：确定性打分 + 命中理由（供 Agent 判断，不替它做决定）。
type Candidate struct {
	ID      string   `json:"id"`
	Path    string   `json:"path"`
	Title   string   `json:"title"`
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
}

// Context 是 eg context 的输出载荷。
//
// 不含 reviews 字段（S2 的综述面尚未落地），也不含任何显著标记字段（渲染面在
// internal/query/markers.go 与命令层，T-…-043 交付）——本结构里没有承载它们的字段。
//
// M3（T-…-033）新增 Proposals：**只有标题与 targets 的摘要**，供 Agent 做去重
// （「这条我是不是已经提过了」）。提案正文**不进上下文**：ProposalSummary 里
// 没有任何承载七个 H2 内容的字段，因此「只给摘要」是结构性的，不靠调用方自律。
type Context struct {
	Domain string      `json:"domain"`
	Source *SourceView `json:"source"`
	Notes  []NoteView  `json:"notes"`
	Cards  []CardView  `json:"cards"`
	// Candidates 是 D-3 的**过渡兼容字段**：内容 / 顺序逐项恒等于 KnowledgeCandidates
	// （同一底层切片，JSON 逐字相等）。0.7.x 起弃用，调用方应改读 knowledge_candidates；
	// 弃用提示（I1）在 CLI 出口产出，本结构只保证「三字段并存且都是数组（非 null）」。
	Candidates []Candidate `json:"candidates"`
	// KnowledgeCandidates 是**知识卡**候选：保持旧实现逐字语义（同一 candidates() 单点、
	// 同一三级全序、同一 CandidateLimit），不引入默认集合 / 打分 / base 行为回归。
	KnowledgeCandidates []Candidate `json:"knowledge_candidates"`
	// OpinionCandidates 是**观点**候选：复用与知识卡**同一个** candidates() 确定性评分 / 排序
	// 单点（不复制算法），输入是 scan.Opinions 经 opinionAsCandidate 投影出的同构候选集。
	// 仅同域、active、非 deleted 的观点可入（candidates() 内的失效 / 删除 / 零分过滤同时成立）；
	// validation（pending/validated/rejected）与候选评分正交，三态均可召回；观点绝不混入 Cards。
	OpinionCandidates []Candidate       `json:"opinion_candidates"`
	Proposals         []ProposalSummary `json:"proposals"`
	Base              map[string]string `json:"base"`
	Warnings          []string          `json:"warnings"`

	// Diagnostics 是本次组装的 Q 系列只读诊断（M2 合同 §5）。
	// 一律 warning，不影响退出码；CLI 层原样透出到 --json 的 warnings[] 与纯文本输出。
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Build 组装上下文。零写入、零 commit：只读盘。
//
// M2（T-…-020）起，材料笔记与知识卡的扫描统一走 scan.go 的 VaultScan：
// 遍历口径与 search / card show / rel 完全同源，不可解析文件产出 Q1 诊断而**不再静默跳过**。
func Build(req Request, hash Hasher) (*Context, error) {
	if hash == nil {
		return nil, ErrHasherRequired
	}
	ctx := &Context{
		Domain:              req.Domain,
		Notes:               []NoteView{},
		Cards:               []CardView{},
		Candidates:          []Candidate{},
		KnowledgeCandidates: []Candidate{},
		OpinionCandidates:   []Candidate{},
		Proposals:           []ProposalSummary{},
		Base:                map[string]string{},
		Diagnostics:         []Diagnostic{},
	}

	// 只扫本领域目录（EG-DOM-02：他域产物在扫描阶段就进不来）。
	scan, err := VaultScan(req.Root, ScanOptions{
		Domains: []string{req.Domain}, IncludeNotes: true,
	})
	if err != nil {
		return nil, err
	}
	notes, cards := scan.Notes, scan.Cards

	target, srcDiags, err := resolveTarget(req, notes)
	if err != nil {
		return nil, err
	}
	if target != nil {
		ctx.Source = target
	}

	// ① 材料层：本领域内属于该原文的已有笔记。
	for _, n := range notes {
		if target != nil && n.Source != target.ID {
			continue
		}
		ctx.Notes = append(ctx.Notes, NoteView{ID: n.ID, Path: n.Path, Source: n.Source})
		ctx.Base[n.Path] = hash(n.Raw)
	}

	// ② 收敛输入：同领域 active 卡。打分输入集合只含 kind='card' 且 status: active，
	//    上面的笔记集合在这一步之前已被排除（EG-NOTE-04）。
	for _, c := range cards {
		if c.Deprecated || c.Deleted {
			// 失效卡不参与收敛（S1 起如此）；已删除卡同样退出收敛与综述取材面
			// （提案与状态合同 §5.1 第 3 / 4 行的第二、四列）——两个维度各自独立成立。
			continue
		}
		ctx.Cards = append(ctx.Cards, CardView{
			ID: c.ID, Path: c.Path, Title: c.Title, Tags: c.Tags,
		})
	}

	// ③ 候选相似项：知识卡与观点各自走**同一个**确定性评分 / 排序单点 candidates()，
	//    不复制算法（观点先经 opinionAsCandidate 投影成同构候选集，再与知识卡同款打分 /
	//    三级全序 / CandidateLimit 截断）。两类各自独立截断，互不串味。
	ctx.KnowledgeCandidates = candidates(targetTitle(ctx), cards)
	// candidates 兼容字段（D-3）：指向同一底层切片，因此内容 / 顺序逐项恒等于
	// knowledge_candidates，JSON 也逐字相等；调用方读哪个都拿到同一份知识候选。
	ctx.Candidates = ctx.KnowledgeCandidates
	ctx.OpinionCandidates = candidates(targetTitle(ctx), opinionCandidates(scan.Opinions))

	// 被选中的知识 / 观点候选路径都进 base（B3 并发保护），content_hash 唯一注入
	// store.ContentHash（与 apply 写前重算逐字一致）。未命中 / 被排除项不进 base。
	for _, cand := range ctx.KnowledgeCandidates {
		for _, c := range cards {
			if c.Path == cand.Path {
				ctx.Base[c.Path] = hash(c.Raw)
			}
		}
	}
	for _, cand := range ctx.OpinionCandidates {
		for _, o := range scan.Opinions {
			if o.Path == cand.Path {
				ctx.Base[o.Path] = hash(o.Raw)
			}
		}
	}

	// ④ 收件区：write_note 成功后要移出条目，属「可能被本次加工修改」的文件。
	inbox := filepath.Join(req.Root, unprocessedFile)
	if raw, err := os.ReadFile(inbox); err == nil {
		ctx.Base[unprocessedFile] = hash(raw)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	// ⑤ 提案去重摘要（T-…-033，提案合同 §10.3）：**只有标题与 targets**。
	//    提案不在知识扫描面内（VaultScan 在遍历入口就跳过 `proposals/`），
	//    这里单独读一次提案目录，且**只取 H1 标题与 targets**——七个 H2 的正文
	//    一个字都不进上下文，也不进 Base（提案不是本次知识加工会改的文件）。
	proposals, propDiags, err := ProposalSummaries(req.Root)
	if err != nil {
		return nil, err
	}
	ctx.Proposals = proposals

	// ⑥ 诚实诊断：扫描期的 Q1、定位原文期的 Q1、提案摘要期的 Q1 汇总后重排
	//    （必要时补 Q3 汇总项）。Q 类一律 warning，**不影响退出码**——
	//    eg context 仍退 0（合同 §5、§6）。
	merged := append(dropQ3(scan.Diagnostics), srcDiags...)
	ctx.Diagnostics = finalizeDiagnostics(append(merged, propDiags...))
	return ctx, nil
}

// dropQ3 去掉已有的 Q3 汇总项：多批诊断合并后要按合并结果**重新汇总恰一条**，
// 否则会出现两条 Q3。
func dropQ3(diags []Diagnostic) []Diagnostic {
	out := make([]Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Code == CodeQ3 {
			continue
		}
		out = append(out, d)
	}
	return out
}

// targetTitle 返回打分用的目标标题（原文标题；缺失时退化为空串 → 无候选）。
func targetTitle(ctx *Context) string {
	if ctx.Source == nil {
		return ""
	}
	return ctx.Source.Title
}

// resolveTarget 解析加工对象：--source 直接找原文；--note 先定位笔记再回到它的原文。
// 第二个返回值是定位过程中产生的 Q1 诊断（原文目录里的不可解析文件）。
func resolveTarget(req Request, notes []NoteEntry) (*SourceView, []Diagnostic, error) {
	id := req.Source
	if id == "" {
		found := false
		for _, n := range notes {
			if n.ID == req.Note {
				id, found = n.Source, true
				break
			}
		}
		if !found {
			return nil, nil, fmt.Errorf("%w：材料笔记 %s（领域 %s）", ErrTargetNotFound, req.Note, req.Domain)
		}
	}
	view, diags, err := readSource(req.Root, id)
	if err != nil {
		return nil, diags, err
	}
	return view, diags, nil
}

// readSource 只读取目标原文（`sources/` 不属于任何领域，所以不按领域过滤）。
// 不可解析的原文**不再静默跳过**：记 Q1 并继续找下一个。
func readSource(root, id string) (*SourceView, []Diagnostic, error) {
	var out *SourceView
	var diags []Diagnostic
	err := walkMarkdown(filepath.Join(root, dirSources), root, func(rel string, raw []byte) error {
		if out != nil {
			// 已定位到目标：后续文件不再解析，因此也不产生 Diagnostic。
			return nil
		}
		doc, src, err := mdfile.ParseSource(raw)
		if err != nil {
			diags = append(diags, newQ1(rel, "原文不可解析，已跳过：%v", err))
			return nil // Q1 已记录，继续遍历（禁止静默跳过）
		}
		if string(src.ID) != id {
			return nil // 可解析但不是目标：正常情况，无 Diagnostic
		}
		out = &SourceView{
			ID: string(src.ID), Path: rel, Title: src.Title, URL: src.URL,
			SavedAt: src.SavedAt.String(), Body: string(raw[doc.BodyFrom:]),
		}
		return nil // 已收下目标原文：无 Diagnostic
	})
	if err != nil {
		return nil, diags, err
	}
	if out == nil {
		return nil, diags, fmt.Errorf("%w：原文 %s", ErrTargetNotFound, id)
	}
	return out, diags, nil
}

// candidates 对同领域 active 卡做**确定性**打分：
//
//	标题共同词 ×3 + 目标标题词命中卡 tags ×2
//
// 打分口径沿用 M1（T-…-010），**不推翻**；M2（T-…-025）只补两件事：
//
//  1. **全序**：得分降序 → 命中理由条数降序 → ID 升序。三键相同即同一张卡，
//     因此对任意输入顺序，输出顺序唯一确定（打乱输入不改变结果）。
//  2. **理由质量**：每条理由只描述**一个来源字段**（`tags` / `title`），
//     逐条写明「哪个词命中了卡的哪个字段」，理由列表按来源字段名升序、
//     字段内按命中词升序（见 reasonsFor）。
//
// 无模型调用、无随机、无时间因素、不读 map 迭代顺序：
// 同一输入连续两次执行输出逐字相同（含顺序、得分与理由）。
func candidates(title string, cards []CardEntry) []Candidate {
	out := []Candidate{}
	want := tokens(title)
	if len(want) == 0 {
		return out
	}
	for _, c := range cards {
		if c.Deprecated || c.Deleted {
			// 同 ② 的口径：已删除项不进候选（默认视图与取材面都不展示它）。
			continue
		}
		score, reasons := scoreCard(want, c)
		// 零理由的卡不进输出：推荐必须可解释（得分 > 0 与理由非空互为充要）。
		if score <= 0 || len(reasons) == 0 {
			continue
		}
		out = append(out, Candidate{
			ID: c.ID, Path: c.Path, Title: c.Title, Score: score, Reasons: reasons,
		})
	}
	sort.Slice(out, func(i, j int) bool { return lessCandidate(out[i], out[j]) })
	if len(out) > CandidateLimit {
		out = out[:CandidateLimit]
	}
	return out
}

// lessCandidate 是候选相似卡的**三级全序**比较键（T-…-025）：
// 得分降序 → 命中理由条数降序 → ID 升序。ID 在库内唯一（冻结合同 F3），
// 因此该序是全序：任意两张不同的卡都可比，排序结果与输入顺序无关。
func lessCandidate(a, b Candidate) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if len(a.Reasons) != len(b.Reasons) {
		return len(a.Reasons) > len(b.Reasons)
	}
	return a.ID < b.ID
}

// 命中理由的来源字段名（升序即理由列表的排列次序：tags < title）。
const (
	fieldTags  = "tags"
	fieldTitle = "title"
)

// scoreCard 计分并生成命中理由。
//
// 计分沿用 M1：目标标题的每个词命中卡标题 +3；命中卡的任一 tag +2（同一个词只计一次）。
// 遍历输入是 sortedKeys(want)（**已排序的切片**，不是 map 迭代），故结果与哈希顺序无关。
func scoreCard(want map[string]bool, c CardEntry) (int, []string) {
	score := 0
	cardTitleTokens := tokens(c.Title)
	var titleHits []string
	var tagHits []tagHit
	for _, tok := range sortedKeys(want) {
		if cardTitleTokens[tok] {
			score += 3
			titleHits = append(titleHits, tok)
		}
		for _, tag := range c.Tags {
			if tokens(tag)[tok] {
				score += 2
				tagHits = append(tagHits, tagHit{Word: tok, Tag: tag})
				break // 同一个词只计一次：多个 tag 命中同一词不重复加分
			}
		}
	}
	return score, reasonsFor(titleHits, tagHits)
}

// tagHit 是一次 tags 命中：哪个词（Word）命中了哪个 tag 值（Tag）。
type tagHit struct {
	Word string
	Tag  string
}

// reasonsFor 生成命中理由：**一条理由只描述一个来源字段**，不把两类来源合并成一句。
//
// 排列次序（确定性要求）：先按来源字段名升序（`tags` → `title`），
// 字段内按命中词升序；命中词由 sortedKeys 产出，本身已升序且去重。
// 文案保持人类可读，且逐条能回答「哪个词、命中了卡的哪个字段」。
func reasonsFor(titleHits []string, tagHits []tagHit) []string {
	reasons := []string{}
	if len(tagHits) > 0 {
		sort.Slice(tagHits, func(i, j int) bool {
			if tagHits[i].Word != tagHits[j].Word {
				return tagHits[i].Word < tagHits[j].Word
			}
			return tagHits[i].Tag < tagHits[j].Tag
		})
		items := make([]string, 0, len(tagHits))
		for _, h := range tagHits {
			items = append(items, fmt.Sprintf("%s（tags 值：%s）", h.Word, h.Tag))
		}
		reasons = append(reasons, "命中卡的 "+fieldTags+" 字段："+strings.Join(items, "、"))
	}
	if len(titleHits) > 0 {
		sort.Strings(titleHits)
		reasons = append(reasons, "命中卡的 "+fieldTitle+" 字段："+strings.Join(titleHits, "、"))
	}
	return reasons
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tokens 把标题切成确定性的词集合：
//   - 全角 ASCII（U+FF01–U+FF5E）先折半角，全角与半角写法命中同一个词（M2 合同 §1.3 归一口径）；
//   - ASCII 字母数字连续段折成小写作为一个词；
//   - 非 ASCII（如中文）按**相邻两字**切二元组，避免整串比对导致零召回。
//
// 只用于**只读**打分，不参与任何落盘字节。**全包唯一一套切词口径**：
// context 候选与 search 匹配分（合同 §1.3）共用它，不另起一套。
func tokens(s string) map[string]bool {
	out := map[string]bool{}
	runes := []rune(s)
	var ascii []rune
	flush := func() {
		if len(ascii) > 0 {
			out[strings.ToLower(string(ascii))] = true
			ascii = ascii[:0]
		}
	}
	var cjk []rune
	flushCJK := func() {
		for i := 0; i+1 < len(cjk); i++ {
			out[string(cjk[i:i+2])] = true
		}
		if len(cjk) == 1 {
			out[string(cjk)] = true
		}
		cjk = cjk[:0]
	}
	for _, r := range runes {
		r = foldWidth(r)
		switch {
		case r < 128 && (r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			flushCJK()
			ascii = append(ascii, r)
		case r < 128:
			flush()
			flushCJK()
		default:
			flush()
			cjk = append(cjk, r)
		}
	}
	flush()
	flushCJK()
	return out
}

// foldWidth 把全角 ASCII（U+FF01–U+FF5E）折成对应半角（M2 合同 §1.3 第 1 条），
// 其余码位原样返回。只读归一：不影响任何落盘字节。
func foldWidth(r rune) rune {
	if r >= 0xFF01 && r <= 0xFF5E {
		return r - 0xFEE0
	}
	return r
}
