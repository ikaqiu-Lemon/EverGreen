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
// S1 实现方式：**直接全库扫描 Markdown**，不依赖索引（`.index/` 属 S4）。

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"evergreen/internal/mdfile"
	"evergreen/internal/model"
)

// vault 布局常量（冻结合同 F1 的 [S1] 列）。口径与 internal/store/layout.go 同源；
// §13 的依赖方向不允许 query 依赖 store，故这里各自持有同一份字面量。
const (
	unprocessedFile = "unprocessed.md"
	dirSources      = "sources"
	dirDomains      = "domains"
	dirNotes        = "notes"
	dirKnowledge    = "knowledge"
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
// 显式不含 proposals / reviews 字段（S2），也不含 [已删除] / [未过目] / [材料支持不足]
// 标记（S2 / S3）——S1 输出里根本没有承载它们的字段。
type Context struct {
	Domain     string            `json:"domain"`
	Source     *SourceView       `json:"source"`
	Notes      []NoteView        `json:"notes"`
	Cards      []CardView        `json:"cards"`
	Candidates []Candidate       `json:"candidates"`
	Base       map[string]string `json:"base"`
	Warnings   []string          `json:"warnings"`
}

// scanned 是一次只读扫描的中间结果。
type scanned struct {
	rel   string
	raw   []byte
	title string
}

// Build 组装上下文。零写入、零 commit：只读盘。
func Build(req Request, hash Hasher) (*Context, error) {
	if hash == nil {
		return nil, ErrHasherRequired
	}
	ctx := &Context{
		Domain: req.Domain,
		Notes:  []NoteView{},
		Cards:  []CardView{},
		Base:   map[string]string{},
	}

	notes, err := scanNotes(req.Root, req.Domain)
	if err != nil {
		return nil, err
	}
	target, err := resolveTarget(req, notes)
	if err != nil {
		return nil, err
	}
	if target != nil {
		ctx.Source = target
	}

	// ① 材料层：本领域内属于该原文的已有笔记。
	for _, n := range notes {
		if target != nil && n.note.Source != model.SourceID(target.ID) {
			continue
		}
		ctx.Notes = append(ctx.Notes, NoteView{
			ID: string(n.note.ID), Path: n.rel, Source: string(n.note.Source),
		})
		ctx.Base[n.rel] = hash(n.raw)
	}

	// ② 收敛输入：同领域 active 卡。打分输入集合只含 kind='card' 且 status: active，
	//    上面的笔记集合在这一步之前已被排除（EG-NOTE-04）。
	cards, err := scanCards(req.Root, req.Domain)
	if err != nil {
		return nil, err
	}
	for _, c := range cards {
		if c.card.Status.Deprecated() {
			continue
		}
		ctx.Cards = append(ctx.Cards, CardView{
			ID: string(c.card.ID), Path: c.rel, Title: c.title, Tags: c.card.Tags,
		})
	}

	// ③ 候选相似卡：确定性打分。
	ctx.Candidates = candidates(targetTitle(ctx), cards)
	for _, cand := range ctx.Candidates {
		for _, c := range cards {
			if c.rel == cand.Path {
				ctx.Base[c.rel] = hash(c.raw)
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
	return ctx, nil
}

// targetTitle 返回打分用的目标标题（原文标题；缺失时退化为空串 → 无候选）。
func targetTitle(ctx *Context) string {
	if ctx.Source == nil {
		return ""
	}
	return ctx.Source.Title
}

type notesEntry struct {
	scanned
	note model.Note
}

type cardEntry struct {
	scanned
	card model.Card
}

// resolveTarget 解析加工对象：--source 直接找原文；--note 先定位笔记再回到它的原文。
func resolveTarget(req Request, notes []notesEntry) (*SourceView, error) {
	id := req.Source
	if id == "" {
		found := false
		for _, n := range notes {
			if string(n.note.ID) == req.Note {
				id, found = string(n.note.Source), true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w：材料笔记 %s（领域 %s）", ErrTargetNotFound, req.Note, req.Domain)
		}
	}
	view, err := readSource(req.Root, id)
	if err != nil {
		return nil, err
	}
	return view, nil
}

// readSource 只读取目标原文（`sources/` 不属于任何领域，所以不按领域过滤）。
func readSource(root, id string) (*SourceView, error) {
	var out *SourceView
	err := walkMarkdown(filepath.Join(root, dirSources), root, func(rel string, raw []byte) error {
		if out != nil {
			return nil
		}
		doc, src, err := mdfile.ParseSource(raw)
		if err != nil {
			return nil // 不可解析的文件不参与定位，由校验命令负责报告
		}
		if string(src.ID) != id {
			return nil
		}
		out = &SourceView{
			ID: string(src.ID), Path: rel, Title: src.Title, URL: src.URL,
			SavedAt: src.SavedAt.String(), Body: string(raw[doc.BodyFrom:]),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("%w：原文 %s", ErrTargetNotFound, id)
	}
	return out, nil
}

// scanNotes 扫描**本领域**的材料笔记（他域目录根本不进入遍历，EG-DOM-02）。
func scanNotes(root, domain string) ([]notesEntry, error) {
	var out []notesEntry
	dir := filepath.Join(root, dirDomains, domain, dirNotes)
	err := walkMarkdown(dir, root, func(rel string, raw []byte) error {
		var note model.Note
		doc, err := mdfile.Parse(raw)
		if err != nil {
			return nil
		}
		if err := doc.DecodeFM(&note); err != nil || note.ID == "" {
			return nil
		}
		out = append(out, notesEntry{scanned: scanned{rel: rel, raw: raw}, note: note})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

// scanCards 扫描**本领域**的知识卡。
func scanCards(root, domain string) ([]cardEntry, error) {
	var out []cardEntry
	dir := filepath.Join(root, dirDomains, domain, dirKnowledge)
	err := walkMarkdown(dir, root, func(rel string, raw []byte) error {
		var card model.Card
		doc, err := mdfile.Parse(raw)
		if err != nil {
			return nil
		}
		if err := doc.DecodeFM(&card); err != nil || card.ID == "" {
			return nil
		}
		out = append(out, cardEntry{
			scanned: scanned{rel: rel, raw: raw, title: cardTitle(doc, card)}, card: card,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

// cardTitle 取卡片标题：frontmatter 的 title（可选键，落在 Extra），缺失时退化为 ID。
func cardTitle(doc *mdfile.Doc, card model.Card) string {
	if v, ok := card.Extra["title"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	_ = doc
	return string(card.ID)
}

// walkMarkdown 只读遍历目录下的 Markdown。目录不存在不是错误（骨架允许缺项）。
func walkMarkdown(dir, root string, fn func(rel string, raw []byte) error) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".index" {
				return fs.SkipDir
			}
			return nil
		}
		if path.Ext(d.Name()) != ".md" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), raw)
	})
}

// candidates 对同领域 active 卡做**确定性**打分：
//
//	标题共同词 ×3 + 目标标题词命中卡 tags ×2
//
// 同分按 ID 升序，输出上限 CandidateLimit。无模型调用、无随机、无时间因素：
// 同一输入连续两次执行输出逐字相同（含顺序与得分）。
func candidates(title string, cards []cardEntry) []Candidate {
	out := []Candidate{}
	want := tokens(title)
	if len(want) == 0 {
		return out
	}
	for _, c := range cards {
		if c.card.Status.Deprecated() {
			continue
		}
		score, reasons := scoreCard(want, c)
		if score <= 0 {
			continue
		}
		out = append(out, Candidate{
			ID: string(c.card.ID), Path: c.rel, Title: c.title, Score: score, Reasons: reasons,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > CandidateLimit {
		out = out[:CandidateLimit]
	}
	return out
}

func scoreCard(want map[string]bool, c cardEntry) (int, []string) {
	score := 0
	var titleHits, tagHits []string
	for _, tok := range sortedKeys(want) {
		if tokens(c.title)[tok] {
			score += 3
			titleHits = append(titleHits, tok)
		}
		for _, tag := range c.card.Tags {
			if tokens(tag)[tok] {
				score += 2
				tagHits = append(tagHits, tag)
				break
			}
		}
	}
	var reasons []string
	if len(titleHits) > 0 {
		reasons = append(reasons, "标题共同词："+strings.Join(titleHits, "、"))
	}
	if len(tagHits) > 0 {
		reasons = append(reasons, "命中 tags："+strings.Join(tagHits, "、"))
	}
	return score, reasons
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
//   - ASCII 字母数字连续段折成小写作为一个词；
//   - 非 ASCII（如中文）按**相邻两字**切二元组，避免整串比对导致零召回。
//
// 只用于**只读**打分，不参与任何落盘字节。
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
