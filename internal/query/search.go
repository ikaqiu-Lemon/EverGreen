package query

// `eg search` 的检索实现（M2 查询合同 `2026-09-19-m2-query-contract.md` §1；T-…-021）。
//
// 只读：本文件不写任何文件、不调 git、不调模型、不发网络请求——
// 匹配与排序全是确定性算法，同一语料两次执行输出逐字相同（含顺序、得分与诊断次序）。
//
// **复用 T-…-020 的扫描底座**：打分走 Filter（§1.3）、排序走 SortEntries（§1.4 四级全序）、
// 诊断走 Q1–Q3（§5）。本文件不新建第二套遍历、不自己开库、不写第二套打分或排序。
//
// M5（T-…-067）起取数**经后端选择单点** SelectBackend + loadVault：索引健康时走索引后端
// （候选集按 searchNeed() 的依据**保守取全集**，逐个回权威 Markdown 解析，绝不窄化召回），
// 索引缺失 / 损坏 / 陈旧时确定性降级为全量扫描并留痕 W22|W23|W24 + Q5。
// 两条后端的 hits[] / total / scanned_files / skipped_files 逐字相等：**Markdown 恒为权威**。

import (
	"errors"
	"fmt"
	"strings"
	"time"
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
	// IncludeDeleted 是「可显式查看」这一列的显式开关（提案与状态合同 §5.1 第 3 / 4 行）：
	// **默认 false = 默认视图不返回已删除项**，置 true 才把它们带回结果并标 [已删除]。
	// 用开关而不是新命令：删除维度只是过滤条件，记录不动、排序算法不动。
	IncludeDeleted bool
	// Index 是 A-44 水位线判定所需的注入口径（S4，见 backend.go 的 IndexDeps）。
	// 零值合法：证不出新鲜度即走全量扫描（无诊断码、无 Q5），结果一字不差。
	Index IndexDeps
	// Page 是分页口径（S4 · T-…-068，合同 §8.2）。**零值 = 不限量**：M1–M4 的库内调用方
	// 因此一字不受影响；`--limit` 默认 50 只在命令层生效（见 page.go 文件头末段）。
	Page PageSpec
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
	// Deleted 是删除维度的判定值（合同 §6.2 的 `deleted: true`）。**追加在键表末尾**：
	// M2 §1.2 既有键的名字与次序一字不改，只多一个新维度的事实字段。
	// 过目维度的 `unreviewed` 刻意**不进** hits[]：ADR-20 只允许那个信号出现在筛选条件里
	// （入口是 eg unreviewed），检索这条排序路径不得携带它。
	Deleted bool `json:"deleted"`
}

// SearchHitKeys 是 hits[] 元素键的合同次序（合同 §1.2 表行序），供逐字反证。
func SearchHitKeys() []string {
	return []string{"id", "title", "domain", "tags", "status", "deprecated",
		"updated_at", "created_at", "path", "matched_fields", "score", "deleted"}
}

// SearchDataKeys 是 `search` 的 data 键次序（合同 §1.2 表行序）。
func SearchDataKeys() []string {
	return []string{"hits", "total", "scanned_files", "skipped_files"}
}

// SearchResult 是一次检索的产物。
//
// Total 是**分页之前**的命中总数（S4 · T-…-068，合同 §8.2）：不分页时它等于 len(Hits)，
// 分页时它仍是库里的总条数 —— 「这一页给你看几条」绝不改写「一共命中几条」。
// 计数守恒（合同 §5.3）：ScannedFiles == 进入结果集的文件数 + SkippedFiles。
type SearchResult struct {
	Hits         []SearchHit
	Total        int
	ScannedFiles int
	SkippedFiles int
	Diagnostics  []Diagnostic
	// Page 是本次分页的事实（S4 · T-…-068）：**不进 data**（合同 §8.3 键集合不扩张），
	// 只服务渲染与用例的可判定性。不分页时 Truncated 恒 false。
	Page Page
	// backend 是本次取数实际走的后端（M5 · T-…-067）。**不进 data**：
	// 合同 §6.4 定死 `search` 的 data 键集合不扩张，降级事实经 warnings[] 承载
	// （W22|W23|W24 + Q5）。这一格只服务测试与排障的可判定性。
	backend Backend
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
		if !ValidDay(f.value) {
			return fmt.Errorf("%w：%s=%q 不是 YYYY-MM-DD 形态的日期，"+
				"或是日历上不存在的日期（月份须 01–12、日须在该年该月的实际天数内，闰年按公历真实规则判定）",
				ErrInvalidQuery, f.name, f.value)
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
	// 分页参数与检索参数分属两族错误：前者 wrap ErrInvalidPage、后者 wrap ErrInvalidQuery。
	// 两族**都**归到退 1（参数非法）：只读命令退出码闭集为 {0,1}（合同 §8.2 / A-47，
	// 经 I-…-008 改判），但哨兵不合并 —— 诊断文案要说清是分页越界还是检索条件非法。
	if err := req.Page.Validate(); err != nil {
		return nil, err
	}
	opt := ScanOptions{}
	if req.Domain != "" {
		opt.Domains = []string{req.Domain}
	}
	// 取数：后端选择走**唯一单点**，降级走**唯一落点**（backend.go / degrade.go）。
	need := searchNeed(req.Index)
	scan, backend, err := loadVault(root, opt, need, SelectBackend(root, need))
	if err != nil {
		return nil, err
	}
	cards := Filter(scan.Cards, FilterSpec{
		Query: req.Query, Domain: req.Domain, Tags: req.Tags,
		Since: req.Since, Until: req.Until,
	})
	// 删除维度的过滤在**打分之后、排序之前**：M2 冻结的排序算法与结果集顺序一字不动，
	// 已删除项只是整条退出默认视图（合同 §5.1 默认检索列 🔴），文件与关系记录不动。
	if !req.IncludeDeleted {
		cards = dropDeleted(cards)
	}
	SortEntries(cards)

	hits := make([]SearchHit, 0, len(cards))
	for _, c := range cards {
		hits = append(hits, SearchHit{
			ID: c.ID, Title: c.Title, Domain: c.Domain, Tags: stringsOrEmpty(c.Tags),
			Status: c.Status, Deprecated: c.Deprecated,
			UpdatedAt: c.UpdatedAt, CreatedAt: c.CreatedAt, Path: c.Path,
			MatchedFields: stringsOrEmpty(c.MatchedFields), Score: c.Score,
			Deleted: c.Deleted,
		})
	}
	// 分页施加在**排序之后**（合同 §8.2）：切片区间 [offset, offset+limit) 因此与
	// 四级全序一一对应，逐页拼接无重无漏。total 取分页前的总数。
	page, pg := ApplyPage(hits, req.Page)
	diags := withIndexDegradedDiagnostics(scan.Diagnostics, degradeDiagnostics(backend))
	return &SearchResult{
		Hits: page, Total: pg.Total,
		ScannedFiles: scan.ScannedFiles, SkippedFiles: scan.SkippedFiles,
		Diagnostics: withTruncationDiagnostic(diags, pg.Truncated, pg, "命中"),
		Page:        pg,
		backend:     backend,
	}, nil
}

// dropDeleted 剔除已删除的卡（保序，不重排）。
//
// 口径来自 internal/query/filter 的四象限真值表「默认检索」列：已删除项（无论 status）
// 一律退出默认视图。这里不导入那个包——internal/query 是排序 / 关系分析 / 综述取材的
// 当前宿主，依赖闭包必须与 ADR-20 的隔离事实兼容；两侧一致由 query 侧的表驱动用例反证。
func dropDeleted(cards []CardEntry) []CardEntry {
	out := make([]CardEntry, 0, len(cards))
	for _, c := range cards {
		if c.Deleted {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ValidDay 判一个 `--since` / `--until` 取值是否是**真实存在的公历日期**。
//
// 两道关卡，缺一不可（I-…-011）：
//  1. **形态**：必须恰好 `YYYY-MM-DD` —— 十位、两个连字符在第 5 / 8 位、其余全为数字。
//     `2026-9-1`（缺前导零）、`20260901`、`2026/09/01`、带时刻的 `2026-09-01T00:00:00`
//     一律不收：形态收窄到唯一一种，逐字字符串比较（合同 §1.4）才成立。
//  2. **日历有效性**：形态过关后还要真的落在日历上。原实现只做第 1 关，于是
//     `--since 2026-13-45` 会被当成合法过滤条件参与字符串比较，得到 `exit 0 + hits=[]`，
//     调用方据此得出「库里没有符合条件的卡」的**错误结论**，全程无 error 无 warning；
//     而 `2026-02-30` 用在 `--since` 与 `--until` 上结果方向相反，结果集不可解释。
//     脚本 / Agent 自行拼日期时月与日越界是最常见的 off-by-one 产物，必须当场拒收。
//
// 实现取 `time.Parse(time.DateOnly, …)`：Go 标准库对 `2006-01-02` 布局**不做**归一化
// 回绕（不会把 `2026-02-30` 悄悄读成 3 月 2 日），并按公历真实闰年规则（4 年闰、
// 百年不闰、400 年再闰）判定 2 月 29 日，故 `2024-02-29` / `2000-02-29` 合法，
// `2025-02-29` / `2100-02-29` 非法。
//
// 仍**不**做的事：不推断语义合理性（`1900-01-01`、`2999-12-31` 都是真实日期，照收），
// 也不改变比较方式 —— 过关后依旧是与 frontmatter 原值的逐字字符串比较。
//
// 导出供命令层复用（`eg unreviewed` 的同族日期参数走同一实现，避免两处各写一份而漂移）。
func ValidDay(s string) bool {
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
	// 形态已收窄到 YYYY-MM-DD，此处只剩「日历上是否存在」这一问。
	_, err := time.Parse(time.DateOnly, s)
	return err == nil
}

// stringsOrEmpty 把 nil 切片归一成空数组：合同 §1.2 要求 `tags` / `matched_fields`
// 无值时是 `[]` 而**不是** `null`。
func stringsOrEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
