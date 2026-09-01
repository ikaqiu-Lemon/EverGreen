package query

// `eg search` 的检索实现（M2 查询合同 `2026-09-19-m2-query-contract.md` §1；T-…-021）。
//
// 只读：本文件不写任何文件、不调 git、不调模型、不发网络请求——
// 匹配与排序全是确定性算法，同一语料两次执行输出逐字相同（含顺序、得分与诊断次序）。
//
// **复用 T-…-020 的扫描底座**：遍历走 VaultScan（walkMarkdown 是全包唯一遍历入口）、
// 打分走 Filter（§1.3）、排序走 SortEntries（§1.4 四级全序）、诊断走 Q1–Q3（§5）。
// 本文件不新建第二套遍历、不建索引、不引数据库与检索引擎（均属 S4）。

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidQuery 是 search 的**参数非法**族（合同 §1.1 / §1.6 → 退 1，零输出内容）：
// 空检索词、非法日期、`--since > --until` 一律 wrap 本哨兵，由 CLI 翻译成退出码 1。
var ErrInvalidQuery = errors.New("search 参数非法")

// SearchRequest 是一次检索的输入（合同 §1.1 参数表）。
//
// Domain 为空 = **全库**（合同 §1.1 的 `--domain` 默认值）；Tags 多值为 AND；
// Since / Until 是 `updated_at` 日期部分的闭区间。
type SearchRequest struct {
	Query  string
	Domain string
	Tags   []string
	Since  string
	Until  string
}

// SearchHit 是一条命中（合同 §1.2 键表，**结构体字段序即 JSON 键序**）。
type SearchHit struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Domain        string   `json:"domain"`
	Tags          []string `json:"tags"`
	Status        string   `json:"status"`
	Deprecated    bool     `json:"deprecated"`
	UpdatedAt     string   `json:"updated_at"`
	CreatedAt     string   `json:"created_at"`
	Path          string   `json:"path"`
	MatchedFields []string `json:"matched_fields"`
	Score         int      `json:"score"`
}

// SearchHitKeys 是 hits[] 元素键的合同次序（合同 §1.2 表行序），供逐字反证。
func SearchHitKeys() []string {
	return []string{"id", "title", "domain", "tags", "status", "deprecated",
		"updated_at", "created_at", "path", "matched_fields", "score"}
}

// SearchDataKeys 是 `search` 的 data 键次序（合同 §1.2 表行序）。
func SearchDataKeys() []string {
	return []string{"hits", "total", "scanned_files", "skipped_files"}
}

// SearchResult 是一次检索的产物。
//
// Total 恒等于 len(Hits)：**不截断、不分页**（分页属 S4）。
// 计数守恒（合同 §5.3）：ScannedFiles == 进入结果集的文件数 + SkippedFiles。
type SearchResult struct {
	Hits         []SearchHit
	Total        int
	ScannedFiles int
	SkippedFiles int
	Diagnostics  []Diagnostic
}

// ValidateSearchRequest 做合同 §1.1 的参数形态校验（全部 wrap ErrInvalidQuery → 退 1）。
func ValidateSearchRequest(req SearchRequest) error {
	if strings.TrimSpace(req.Query) == "" {
		return fmt.Errorf("%w：检索词为空或只含空白（零输出内容）", ErrInvalidQuery)
	}
	// 次序固定（先 --since 再 --until）：两个都非法时报的也必须是同一条，
	// 否则同一输入两次执行的错误文案会不同（确定性口径覆盖错误路径）。
	for _, f := range []struct{ name, value string }{
		{"--since", req.Since}, {"--until", req.Until},
	} {
		if f.value == "" {
			continue
		}
		if !validDate(f.value) {
			return fmt.Errorf("%w：%s=%q 不是 YYYY-MM-DD 形态的日期", ErrInvalidQuery, f.name, f.value)
		}
	}
	if req.Since != "" && req.Until != "" && req.Since > req.Until {
		return fmt.Errorf("%w：--since=%s 晚于 --until=%s（闭区间为空）",
			ErrInvalidQuery, req.Since, req.Until)
	}
	return nil
}

// Search 执行一次检索：扫描 → 过滤打分 → 四级全序排序 → 组装命中列表 + 诊断。
//
// 搜索面 = 知识卡（`domains/<d>/knowledge/**.md`）：材料笔记与原文**不进 hits[]**
// （合同 §1.1；因此 IncludeNotes 为 false，笔记既不扫也不计数）。
// 失效卡**同等可见**（合同 §1.5）：照常进结果集、参与同一套排序，不降权不后置，
// 也没有任何隐藏它们的开关。
func Search(root string, req SearchRequest) (*SearchResult, error) {
	if err := ValidateSearchRequest(req); err != nil {
		return nil, err
	}
	opt := ScanOptions{}
	if req.Domain != "" {
		opt.Domains = []string{req.Domain}
	}
	scan, err := VaultScan(root, opt)
	if err != nil {
		return nil, err
	}
	cards := Filter(scan.Cards, FilterSpec{
		Query: req.Query, Domain: req.Domain, Tags: req.Tags,
		Since: req.Since, Until: req.Until,
	})
	SortEntries(cards)

	hits := make([]SearchHit, 0, len(cards))
	for _, c := range cards {
		hits = append(hits, SearchHit{
			ID: c.ID, Title: c.Title, Domain: c.Domain, Tags: stringsOrEmpty(c.Tags),
			Status: c.Status, Deprecated: c.Deprecated,
			UpdatedAt: c.UpdatedAt, CreatedAt: c.CreatedAt, Path: c.Path,
			MatchedFields: stringsOrEmpty(c.MatchedFields), Score: c.Score,
		})
	}
	return &SearchResult{
		Hits: hits, Total: len(hits),
		ScannedFiles: scan.ScannedFiles, SkippedFiles: scan.SkippedFiles,
		Diagnostics: scan.Diagnostics,
	}, nil
}

// validDate 判 YYYY-MM-DD 形态（只做形态判定，不做日历合法性推断：
// 时间戳是 frontmatter 原值，比较也只做逐字字符串比较，见合同 §1.4）。
func validDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, r := range s {
		if i == 4 || i == 7 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// stringsOrEmpty 把 nil 切片归一成空数组：合同 §1.2 要求 `tags` / `matched_fields`
// 无值时是 `[]` 而**不是** `null`。
func stringsOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
