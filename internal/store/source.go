package store

// `add_source` 的落盘：原文文件 + 收件区条目（技术方案 §4.4 / §7.5；T-…-009）。
//
// 口径：
//   - 原文落 `sources/<s-id>.md`（vault 根下，**不属于任何领域**）；收件区队列文件
//     `unprocessed.md` 在 **vault 根**，与 `sources/` 并列（冻结合同 F1；技术方案 §7.5
//     「产物」行把它写在 sources/ 下属方案笔误，本仓一律按 F1 实现）。
//   - 原文 frontmatter：`id` / `url` / `title` / `saved_at`（§4.4 四键）+ 本工具附加的
//     `reasons`（收录理由列表，命中判重时**追加**一条）与可选 `tags`。
//     正文收录后**不因任何加工改写**——本包没有任何改写原文正文的入口。
//   - 判重键只在这里定义一份：`URLKey`（URL 规范化后精确匹配）与 `TitleKey`（标题精确匹配）。
//   - 收件区条目键为 `source_id`，同 ID 已在队列中即视为已登记（幂等，不产生第二条）。
//
// 写路径同 §16.3：按模板拼字节，不做「结构体 → 序列化」；落盘一律经 CreateFile /
// WriteGuarded，或（收件区这种无固定分区的队列文件）经 mdfile 的字节区间插入 + 原子替换。

import (
	"bytes"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// ErrSourceIDRequired 缺原文 ID：ID 由上层按 (日期, 标题) 稳定生成后传入。
var ErrSourceIDRequired = errors.New("缺原文 ID")

// TrackingParams 是 URL 规范化时剔除的跟踪参数前缀 / 全名（判重口径的一部分，
// 集合封闭且只在这里定义一份）。
func TrackingParams() []string {
	return []string{"utm_", "spm", "fbclid", "gclid", "ref", "from", "source"}
}

// NormalizeURL 把 URL 归一成判重用的 `url_key`（EG-SRC-01）。
//
// 规范化是**确定性**的纯字节处理，不发起任何网络请求：
// scheme / host 的 ASCII 大写字母折叠、去默认端口、去 fragment、去跟踪参数、
// query 按键排序、去掉末尾 `/`（根路径除外）。不可解析的 URL 退化为去空白后的原串。
//
// 注意作用域：折叠只发生在 **URL 的 scheme / host**（协议规定大小写不敏感），
// 且只作为判重键使用——原文 frontmatter 里的 `url` 一律逐字落盘，不被规范化改写。
func NormalizeURL(raw string) string {
	s := trimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	u.Scheme = foldASCII(u.Scheme)
	u.Host = foldASCII(u.Host)
	if (u.Scheme == "http" && strings.HasSuffix(u.Host, ":80")) ||
		(u.Scheme == "https" && strings.HasSuffix(u.Host, ":443")) {
		u.Host = u.Host[:strings.LastIndex(u.Host, ":")]
	}
	u.Fragment = ""
	u.RawFragment = ""
	q := u.Query()
	for key := range q {
		if isTracking(key) {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode() // Encode 按键排序，天然确定性
	if len(u.Path) > 1 {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String()
}

// foldASCII 把 ASCII 大写字母折成小写（纯字节处理：非 ASCII 字节逐字不动，
// 不做 rune 迭代重建，非法 UTF-8 输入无损）。
func foldASCII(s string) string {
	src := []byte(s)
	out := make([]byte, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// trimSpace 去首尾空白（字节级；只用于判重键与空值判定，不参与任何落盘字节）。
func trimSpace(s string) string { return string(bytes.TrimSpace([]byte(s))) }

func isTracking(key string) bool {
	k := foldASCII(key)
	for _, p := range TrackingParams() {
		if k == p || (strings.HasSuffix(p, "_") && strings.HasPrefix(k, p)) {
			return true
		}
	}
	return false
}

// NormalizeTitle 把标题归一成判重用的 `title_key`：去首尾空白（**精确匹配**，
// 不做大小写折叠与全半角归一——那会把不同标题误判成同一篇）。
func NormalizeTitle(raw string) string { return trimSpace(raw) }

// sourceMeta 是原文 frontmatter 的只读投影：§4.4 四键 + 本工具附加的 reasons / tags。
// 未列出的键落进 model.Source.Extra 由 mdfile 原样保留，本结构体只用于**读**。
type sourceMeta struct {
	ID      string   `yaml:"id"`
	URL     string   `yaml:"url"`
	Title   string   `yaml:"title"`
	SavedAt string   `yaml:"saved_at"`
	Reasons []string `yaml:"reasons"`
	Tags    []string `yaml:"tags"`
}

// SourceInfo 是一篇已收录原文的判重视图。
type SourceInfo struct {
	ID       model.SourceID
	Rel      string
	URL      string
	Title    string
	URLKey   string
	TitleKey string
	Reasons  []string
	Hash     string
}

// ScanSources 扫描 `sources/` 下的原文（S1 全库扫描版，无索引）。
// 结果按相对路径升序，保证同输入同输出。
func (s *Store) ScanSources() ([]SourceInfo, error) {
	var out []SourceInfo
	err := s.walkMarkdown(DirSources, func(rel string, raw []byte) error {
		var meta sourceMeta
		if err := FrontmatterInto(raw, &meta); err != nil || meta.ID == "" {
			return nil // 不可解析的文件不参与判重，由校验命令负责报告
		}
		out = append(out, SourceInfo{
			ID:       model.SourceID(meta.ID),
			Rel:      rel,
			URL:      meta.URL,
			Title:    meta.Title,
			URLKey:   NormalizeURL(meta.URL),
			TitleKey: NormalizeTitle(meta.Title),
			Reasons:  meta.Reasons,
			Hash:     ContentHash(raw),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// DedupHit 是一次判重命中的结果：命中哪一篇、命中的是哪个键。
type DedupHit struct {
	Source SourceInfo
	Key    string // "url" | "title"
}

// FindDuplicate 按 §7.5 判重口径查已有原文：**URL 规范化后精确匹配，其次标题精确匹配**。
func FindDuplicate(sources []SourceInfo, rawURL, title string) (DedupHit, bool) {
	urlKey, titleKey := NormalizeURL(rawURL), NormalizeTitle(title)
	if urlKey != "" {
		for _, s := range sources {
			if s.URLKey != "" && s.URLKey == urlKey {
				return DedupHit{Source: s, Key: "url"}, true
			}
		}
	}
	if titleKey != "" {
		for _, s := range sources {
			if s.TitleKey != "" && s.TitleKey == titleKey {
				return DedupHit{Source: s, Key: "title"}, true
			}
		}
	}
	return DedupHit{}, false
}

// NoteInfo 是一篇材料笔记的定位视图（供「已有材料笔记 → 默认不重复加工」判定）。
type NoteInfo struct {
	ID     model.NoteID
	Rel    string
	Domain string
	Source model.SourceID
	Hash   string
}

// ScanNotes 扫描 `domains/<d>/notes/` 下的材料笔记（S1 全库扫描版）。
// 结果按相对路径升序。
func (s *Store) ScanNotes() ([]NoteInfo, error) {
	var out []NoteInfo
	err := s.walkMarkdown(DirDomains, func(rel string, raw []byte) error {
		if path.Base(path.Dir(rel)) != DirNotes {
			return nil
		}
		var note model.Note
		if err := FrontmatterInto(raw, &note); err != nil || note.ID == "" {
			return nil
		}
		out = append(out, NoteInfo{
			ID: note.ID, Rel: rel, Domain: DomainOf(rel),
			Source: note.Source, Hash: ContentHash(raw),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// NoteOf 返回该原文已有的材料笔记（按相对路径升序取第一条）。
func (s *Store) NoteOf(id model.SourceID) (NoteInfo, bool, error) {
	notes, err := s.ScanNotes()
	if err != nil {
		return NoteInfo{}, false, err
	}
	for _, n := range notes {
		if n.Source == id {
			return n, true, nil
		}
	}
	return NoteInfo{}, false, nil
}

// walkMarkdown 遍历 vault 内某个子目录下的 Markdown 文件（相对路径 + 原始字节）。
// 子目录不存在时不是错误（S1 允许骨架缺项，如尚未建过任何领域）。
func (s *Store) walkMarkdown(sub string, fn func(rel string, raw []byte) error) error {
	abs, err := s.Abs(sub)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), raw)
	})
}

// SourceSpec 是一次 add_source 的落盘输入。Body 逐字落盘（Agent 已清洗），
// 本包不解析 HTML、不清洗、不做任何网络请求。
type SourceSpec struct {
	Rel     string // 空 → SourceRel(ID)
	ID      model.SourceID
	URL     string
	Title   string
	Stamp   model.Stamp
	Tags    []string
	Reasons []string
	Body    []byte
	Inbox   InboxEntry
}

// SourceOutcome 是 add_source 的显式结果：原文本体 + 收件区条目登记情况。
// InboxSkip 非 nil 即「必须上报的跳过」，调用方不得忽略（§9 B3 同源口径）。
type SourceOutcome struct {
	Source    Result
	Inbox     Result
	InboxSkip *SkipError
}

// ApplySource 落盘一篇原文，并把待加工条目登记进收件区。
func (s *Store) ApplySource(spec SourceSpec) (SourceOutcome, error) {
	if spec.ID == "" {
		return SourceOutcome{}, ErrSourceIDRequired
	}
	rel := spec.Rel
	if rel == "" {
		rel = SourceRel(string(spec.ID))
	}
	out := SourceOutcome{}
	content, err := sourceContent(spec)
	if err != nil {
		return out, err
	}
	res, err := s.CreateFile(rel, mdfile.KindSource, content)
	out.Source = res
	if err != nil {
		return out, err
	}
	inbox, skip := s.AttachEntry(spec.Inbox, spec.ID)
	out.Inbox, out.InboxSkip = inbox, skip
	return out, nil
}

// sourceContent 拼装新建原文的完整字节：frontmatter + 正文全文。
// 原文没有固定分区（正文是任意字节），因此 document 只产出 frontmatter 外壳。
func sourceContent(spec SourceSpec) ([]byte, error) {
	var fm []byte
	for _, kv := range [][2]string{
		{"id", string(spec.ID)},
		{"url", spec.URL},
		{"title", spec.Title},
		{"saved_at", spec.Stamp.String()},
	} {
		if kv[1] == "" {
			continue
		}
		line, err := fmLine(kv[0], kv[1])
		if err != nil {
			return nil, err
		}
		fm = append(fm, line...)
	}
	reasons, err := fmSeq("reasons", spec.Reasons)
	if err != nil {
		return nil, err
	}
	fm = append(fm, reasons...)
	tags, err := fmSeq("tags", spec.Tags)
	if err != nil {
		return nil, err
	}
	fm = append(fm, tags...)

	out, err := document(mdfile.KindSource, fm, nil)
	if err != nil {
		return nil, err
	}
	out = append(out, spec.Body...)
	if len(spec.Body) > 0 && spec.Body[len(spec.Body)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}

// ApplySourceReason 向已有原文的 `reasons` 列表追加一条收录理由（判重命中路径）。
//
// 只追加、正文不覆盖（B1）：理由已存在时**不产生第二条**，返回 Written=false 的回执，
// 磁盘字节零变化。`reasons` 键缺失时新建块状序列。
func (s *Store) ApplySourceReason(rel, expectedHash, reason string) (Result, error) {
	if trimSpace(reason) == "" {
		return Result{Path: rel}, nil
	}
	f, err := s.Read(rel)
	if err != nil {
		return Result{Path: rel}, err
	}
	var meta sourceMeta
	if err := FrontmatterInto(f.Bytes, &meta); err != nil {
		return Result{Path: rel, Hash: f.Hash}, err
	}
	for _, existing := range meta.Reasons {
		if existing == reason {
			return Result{Path: rel, Hash: f.Hash,
				Detail: "该收录理由已在列表中：不产生第二条（幂等）"}, nil
		}
	}
	hash := expectedHash
	if hash == "" {
		hash = f.Hash
	}
	quotedReason, err := quoted(reason)
	if err != nil {
		return Result{Path: rel, Hash: f.Hash}, err
	}
	edit := Edit{Kind: mdfile.KindSource}
	if len(meta.Reasons) == 0 {
		items := make([]byte, 0, len(seqIndent)+len(quotedReason)+3)
		items = append(items, seqIndent...)
		items = append(items, '-', ' ')
		items = append(items, quotedReason...)
		items = append(items, '\n')
		edit.FMSeqNew = []FMSeqBlock{{Key: "reasons", Items: items}}
	} else {
		edit.FMSeqItems = []FMSeqAppend{{Key: "reasons", Item: append(quotedReason, '\n')}}
	}
	return s.WriteGuarded(rel, hash, edit)
}
