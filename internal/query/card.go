package query

// `eg card show` 的单卡视图组装（M2 查询合同 `2026-09-19-m2-query-contract.md` §2；T-…-022）。
//
// 只读：本文件只经 VaultScan 读字节，不写文件、不调 git、不调模型、不发网络请求。
//
// **复用 T-…-020 的扫描底座**：定位目标卡与反向关系都走全库 VaultScan
// （`walkMarkdown` 是全包唯一遍历入口），正向 / 反向排序走 SortEdges（§3.3），
// 诊断走 Q1–Q3（§5）。不建索引、不引数据库（均属 S4）。

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"evergreen/internal/mdfile"
	"evergreen/internal/model"
)

// ErrCardNotFound 是 `card show` 的**卡不存在**（合同 §2.1 / §2.3 → 退 1，零副作用）。
var ErrCardNotFound = errors.New("知识卡不存在")

// ErrInvalidCardID 是 `<k-id>` 形态非法（合同 §2.1 → 退 1）。
var ErrInvalidCardID = errors.New("知识卡 ID 形态非法")

// MarkerDeprecated 是 M2 唯一可能出现的显著标记（合同 §1.5 / §2.2）：失效卡标记。
// 其余显著标记（逻辑删除 / 未过目 / 材料支持不足）属 S2 / S3，M2 **不输出**，
// 本包也不定义它们的字面量。
const MarkerDeprecated = "[失效]"

// Sections 是五分区正文的**有序**映射（合同 §2.2：键序固定为五分区声明序 F5）。
//
// 键集合恒定：缺分区**键仍在、值为空串**，绝不丢键；值是该分区的原始文本
// （逐字取字节，只裁掉首尾空白，不做 Markdown 再渲染）。
type Sections struct {
	vals map[string]string
}

// NewSections 按五分区声明序归一：未给出的分区补空串。
func NewSections(vals map[string]string) Sections {
	out := Sections{vals: map[string]string{}}
	for _, name := range mdfile.CardSections() {
		out.vals[name] = vals[name]
	}
	return out
}

// Keys 返回固定键序（= mdfile.CardSections()，F5 声明序）。
func (s Sections) Keys() []string { return mdfile.CardSections() }

// Get 取某分区正文（缺分区为空串）。
func (s Sections) Get(name string) string { return s.vals[name] }

// Missing 报告该分区是否缺失（值为空串）：人类可读渲染据此标注「（本分区缺失）」。
// 分区缺失**不属于** Q 系列（文件本身可解析），因此不计入 skipped_files（合同 §2.2）。
func (s Sections) Missing(name string) bool { return s.vals[name] == "" }

// MarshalJSON 按固定键序输出（不能退化成 encoding/json 的字典序）。
func (s Sections) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteString("{")
	for i, name := range s.Keys() {
		if i > 0 {
			b.WriteString(",")
		}
		key, err := jsonString(name)
		if err != nil {
			return nil, err
		}
		val, err := jsonString(s.vals[name])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteString(":")
		b.Write(val)
	}
	b.WriteString("}")
	return []byte(b.String()), nil
}

// CardDetail 是单卡视图的 data 载荷（合同 §2.2 键表，**结构体字段序即键序**）。
type CardDetail struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Domain       string            `json:"domain"`
	Status       string            `json:"status"`
	Deprecated   bool              `json:"deprecated"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
	Path         string            `json:"path"`
	Tags         []string          `json:"tags"`
	Markers      []string          `json:"markers"`
	Sections     Sections          `json:"sections"`
	Sources      []model.SourceRef `json:"sources"`
	RelationsOut []RelationEdge    `json:"relations_out"`
	RelationsIn  []RelationEdge    `json:"relations_in"`
}

// CardDataKeys 是 `card show` 的 data 键次序（合同 §2.2 键表次序）。
func CardDataKeys() []string {
	return []string{"id", "title", "domain", "status", "deprecated", "created_at",
		"updated_at", "path", "tags", "markers", "sections", "sources",
		"relations_out", "relations_in"}
}

// CardShowResult 是一次单卡视图查询的产物。
//
// 命名说明：函数叫 ShowCard、结果叫 CardShowResult，是因为包内 CardView 这个名字
// 早在 M1 就被 eg context 的「同领域 active 卡」视图结构体占用（context.go），
// 不改 M1 已对外的类型名。
//
// MissingTargets 是 relations_out[] 里**目标不存在**的对端 ID（升序去重）：
// 条目本身照常展示（合同 §2.2 不允许给元素加第六个键），人类可读渲染据此标注
// 「目标不存在」，同时 Q2 诊断进 warnings[]。
type CardShowResult struct {
	Card           CardDetail
	MissingTargets []string
	Diagnostics    []Diagnostic
	ScannedFiles   int
	SkippedFiles   int
}

// ShowCard 组装单卡视图：全库扫描定位卡 → 五分区正文 → sources[] → 正向 / 反向关系。
//
// 定位走**全库扫描**（卡 ID 全库唯一，故不限定领域，合同 §2.1）。
// 同一 ID 出现在两处 → 取路径字典序最小者并由扫描层记一条 Q1（如实说明重复），
// **不静默择一**。失效卡照常展示并置 markers = ["[失效]"]（合同 §2.2）。
func ShowCard(root string, id model.CardID) (*CardShowResult, error) {
	if !id.Valid() {
		return nil, fmt.Errorf("%w：%q 不是 k-YYYYMMDD-slug 形态的知识卡 ID", ErrInvalidCardID, string(id))
	}
	scan, err := VaultScan(root, ScanOptions{})
	if err != nil {
		return nil, err
	}
	// scan.Cards 已按 path 升序稳定排序，故首个同 ID 命中即路径字典序最小者。
	var target *CardEntry
	for i := range scan.Cards {
		if scan.Cards[i].ID == string(id) {
			target = &scan.Cards[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w：%s 在库中不存在（已全库扫描 %d 个 .md）",
			ErrCardNotFound, string(id), scan.ScannedFiles)
	}

	out := RelationsOut(*target)
	res := &CardShowResult{
		Card: CardDetail{
			ID: target.ID, Title: target.Title, Domain: target.Domain,
			Status: target.Status, Deprecated: target.Deprecated,
			CreatedAt: target.CreatedAt, UpdatedAt: target.UpdatedAt, Path: target.Path,
			Tags: stringsOrEmpty(target.Tags), Markers: cardMarkers(*target),
			Sections: cardSections(*target), Sources: sourcesOrEmpty(target.Sources),
			RelationsOut: out, RelationsIn: RelationsIn(scan.Cards, string(id)),
		},
		MissingTargets: missingTargets(scan.Cards, out),
		Diagnostics:    scan.Diagnostics,
		ScannedFiles:   scan.ScannedFiles,
		SkippedFiles:   scan.SkippedFiles,
	}
	return res, nil
}

// cardMarkers 给出显著标记：M2 唯一可能取值是 [MarkerDeprecated]，其余为空数组（合同 §2.2）。
func cardMarkers(c CardEntry) []string {
	if c.Deprecated {
		return []string{MarkerDeprecated}
	}
	return []string{}
}

// cardSections 取五分区正文：复用 mdfile 的分区索引，**不重写解析**。
// 缺分区置空串（键集合恒定）；分区缺失不是 Q 类（文件本身可解析）。
func cardSections(c CardEntry) Sections {
	vals := map[string]string{}
	if c.Doc == nil {
		return NewSections(vals)
	}
	for _, name := range mdfile.CardSections() {
		span, ok := c.Doc.Section(name)
		if !ok {
			continue
		}
		if span.Body > len(c.Raw) || span.End > len(c.Raw) || span.Body > span.End {
			continue
		}
		vals[name] = strings.TrimSpace(string(c.Raw[span.Body:span.End]))
	}
	return NewSections(vals)
}

// missingTargets 收集正向关系里目标卡不存在的对端 ID（升序去重，确定性）。
func missingTargets(cards []CardEntry, out []RelationEdge) []string {
	known := map[string]bool{}
	for _, c := range cards {
		known[c.ID] = true
	}
	seen := map[string]bool{}
	miss := []string{}
	for _, e := range out {
		if e.Target == "" || known[e.Target] || seen[e.Target] {
			continue
		}
		seen[e.Target] = true
		miss = append(miss, e.Target)
	}
	sort.Strings(miss)
	return miss
}

// sourcesOrEmpty 把 nil 归一成空数组（合同 §2.2：无值时是 `[]` 而不是 `null`）。
func sourcesOrEmpty(in []model.SourceRef) []model.SourceRef {
	if in == nil {
		return []model.SourceRef{}
	}
	return in
}
