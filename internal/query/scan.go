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
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"evergreen/internal/mdfile"
	"evergreen/internal/model"
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
	Relations  []model.Relation
	Sources    []model.SourceRef
	Raw        []byte
	Doc        *mdfile.Doc

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
	Raw       []byte
	Doc       *mdfile.Doc
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
// 计数守恒（合同 §5.3）：ScannedFiles == len(Cards)+len(Notes)+SkippedFiles，
// 这是「没有第三条静默路径」的机器判据。
type ScanResult struct {
	Cards        []CardEntry
	Notes        []NoteEntry
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
	res := &ScanResult{Cards: []CardEntry{}, Notes: []NoteEntry{}, Diagnostics: []Diagnostic{}}
	domains, err := resolveDomains(root, opt.Domains)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		if err := scanCardDir(root, d, res); err != nil {
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
		doc, err := mdfile.Parse(raw)
		if err != nil {
			res.skip(newQ1(rel, "知识卡不可解析，已跳过：%v", err))
			return nil
		}
		var card model.Card
		if err := doc.DecodeFM(&card); err != nil {
			res.skip(newQ1(rel, "知识卡 frontmatter 解码失败，已跳过：%v", err))
			return nil // 已记 Q1 并计入 SkippedFiles，继续遍历（禁止静默跳过）
		}
		if card.ID == "" {
			res.skip(newQ1(rel, "知识卡缺 id，无法定位，已跳过"))
			return nil // 同上：Q1 已记录
		}
		res.Cards = append(res.Cards, CardEntry{
			ID: string(card.ID), Path: rel, Domain: domain,
			Title: entryTitle(card.Extra, string(card.ID)), Tags: card.Tags,
			Status: string(card.Status), Deprecated: card.Status.Deprecated(),
			CreatedAt: card.CreatedAt.String(), UpdatedAt: card.UpdatedAt.String(),
			Relations: card.Relations, Sources: card.Sources, Raw: raw, Doc: doc,
		})
		return nil // 正常收录：无 Diagnostic
	})
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
			UpdatedAt: note.UpdatedAt.String(), Raw: raw, Doc: doc,
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

// relationTypeOrder 是合同 §3.3 的固定次序：opposing → limits → supports → derives
// （opposing 最影响判断故首位；该次序不随字母序变化）。
var relationTypeOrder = map[string]int{
	string(model.RelationOpposing): 0,
	string(model.RelationLimits):   1,
	string(model.RelationSupports): 2,
	string(model.RelationDerives):  3,
}

// SortEdges 就地做合同 §3.3 的两级排序：type 固定次序 → 对端 ID 升序。
// peerOf 给出「对端」：正向比 target、反向比 from。同 type 同对端时按 path 兜底，
// 保证全序（重复条目照实输出、不折叠）。
func SortEdges(edges []RelationEdge, peerOf func(RelationEdge) string) {
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		oa, ok := relationTypeOrder[a.Type]
		if !ok {
			oa = len(relationTypeOrder)
		}
		ob, ok := relationTypeOrder[b.Type]
		if !ok {
			ob = len(relationTypeOrder)
		}
		if oa != ob {
			return oa < ob
		}
		if peerOf(a) != peerOf(b) {
			return peerOf(a) < peerOf(b)
		}
		return a.Path < b.Path
	})
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
func walkMarkdown(dir, root string, fn func(rel string, raw []byte) error) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		// 目录缺失时没有任何文件可扫，因此也没有可记的 Diagnostic（不是「静默跳过文件」）。
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
			// 目录本身不是查询对象：不进结果、不记 Diagnostic（Q1 只针对 .md 文件）。
			return nil
		}
		if path.Ext(d.Name()) != ".md" {
			// 非 .md 不在查询面内：同样不记 Diagnostic（Q1 只针对 .md 文件）。
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
