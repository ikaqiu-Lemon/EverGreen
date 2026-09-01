package query

// `eg rel` 读路径的正向 / 反向关系视图（M2 查询合同
// `2026-09-19-m2-query-contract.md` §3；T-…-023）。
//
// 只读：本文件只经 VaultScan 读字节，不写文件、不调 git、不调模型、不发网络请求。
//
// **复用 T-…-020 的扫描底座**：一次全库 VaultScan（`Domains: nil`）同时喂正向与反向
// （`walkMarkdown` 是全包唯一遍历入口），正向走 RelationsOut、反向走 RelationsIn、
// 排序走 SortEdges（§3.3），诊断走 Q1–Q3（§5）。
// **不建索引、不引 SQLite / FTS5**（均属 S4）；不做多跳展开（S2）、不做截断分页（S4）。

import (
	"fmt"

	"evergreen/internal/model"
)

// RelRequest 是一次关系查询的输入（合同 §3.1 参数表）。
//
// To 对应 `--to <id>`：只保留**对端** == 该 ID 的条目（正向比 target、反向比 from）；
// 空 = 不过滤。
type RelRequest struct {
	ID model.CardID
	To string
}

// RelData 是 `eg rel` 的 data 载荷（合同 §3.1 键表，**结构体字段序即键序**）。
type RelData struct {
	ID           string         `json:"id"`
	RelationsOut []RelationEdge `json:"relations_out"`
	RelationsIn  []RelationEdge `json:"relations_in"`
	ScannedFiles int            `json:"scanned_files"`
	SkippedFiles int            `json:"skipped_files"`
}

// RelDataKeys 是 `eg rel` 的 data 键次序（合同 §3.1）。
func RelDataKeys() []string {
	return []string{"id", "relations_out", "relations_in", "scanned_files", "skipped_files"}
}

// RelEdgeKeys 是关系条目的键次序（合同 §3.1：元素键恰五项，正反向同构）。
func RelEdgeKeys() []string {
	return []string{"from", "type", "target", "reason", "path"}
}

// RelResult 是一次关系查询的产物。
//
// MissingTargets 是正向条目里**目标不存在**的对端 ID（升序去重）：条目本身照常输出
// （合同 §3.1 不允许给元素加第六个键），人类可读渲染据此标注「目标不存在」，
// 同时 Q2 诊断进 warnings[]。
type RelResult struct {
	Data           RelData
	MissingTargets []string
	Diagnostics    []Diagnostic
}

// RelView 组装关系视图：全库扫描 → 正向读本卡 frontmatter → 反向反查「谁指向了我」。
//
// 正向 `relations_out[]`：只读本卡 frontmatter 的 `relations[]`，`from` 恒为本卡 ID。
// 反向 `relations_in[]`：**全库 Markdown 反向扫描**，`from` 为写下该条关系的卡。
// `opposing` **单向存储**（EG-CVG-05）：库里只有一条记录，读路径**不补对称条目、不去重合并**。
func RelView(root string, req RelRequest) (*RelResult, error) {
	if !req.ID.Valid() {
		return nil, fmt.Errorf("%w：%q 不是 k-YYYYMMDD-slug 形态的知识卡 ID",
			ErrInvalidCardID, string(req.ID))
	}
	if req.To != "" && !model.CardID(req.To).Valid() {
		return nil, fmt.Errorf("%w：--to=%q 不是 k-YYYYMMDD-slug 形态的知识卡 ID",
			ErrInvalidCardID, req.To)
	}
	scan, err := VaultScan(root, ScanOptions{})
	if err != nil {
		return nil, err
	}
	// scan.Cards 已按 path 升序稳定排序：同 ID 重复时首个命中即路径字典序最小者
	// （重复本身由扫描层记 Q1，不静默择一）。
	var target *CardEntry
	for i := range scan.Cards {
		if scan.Cards[i].ID == string(req.ID) {
			target = &scan.Cards[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("%w：%s 在库中不存在（已全库扫描 %d 个 .md）",
			ErrCardNotFound, string(req.ID), scan.ScannedFiles)
	}

	out := RelationsOut(*target)
	in := RelationsIn(scan.Cards, string(req.ID))
	diags := scan.Diagnostics
	if req.To != "" {
		out = filterEdges(out, func(e RelationEdge) bool { return e.Target == req.To })
		in = filterEdges(in, func(e RelationEdge) bool { return e.From == req.To })
		if !hasCard(scan.Cards, req.To) {
			// 合同 §3.1：`--to` 指向不存在的 ID → **结果为空** + 一条 Q2（如实说明，不静默返回空）。
			// 置空是逐字判据：即使本卡有一条悬空 relations[] 恰好指向这个不存在的 ID，
			// 也不能因此让 relations_out[] 非空——「对端卡不存在」的口径优于「条目照常输出」。
			out, in = []RelationEdge{}, []RelationEdge{}
			diags = finalizeDiagnostics(append(append([]Diagnostic{}, scan.Diagnostics...),
				newQ2(target.Path, "--to 指定的对端卡 %s 在库中不存在：正反向结果均为空", req.To)))
		}
	}
	return &RelResult{
		Data: RelData{
			ID: string(req.ID), RelationsOut: out, RelationsIn: in,
			ScannedFiles: scan.ScannedFiles, SkippedFiles: scan.SkippedFiles,
		},
		MissingTargets: missingTargets(scan.Cards, out),
		Diagnostics:    diags,
	}, nil
}

// filterEdges 按谓词过滤并保序（排序在 RelationsOut / RelationsIn 里已完成）。
func filterEdges(edges []RelationEdge, keep func(RelationEdge) bool) []RelationEdge {
	out := []RelationEdge{}
	for _, e := range edges {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// hasCard 报告库中是否存在该 ID 的卡。
// 线性判定：M2 没有索引（索引属 S4），一次命令一次扫描，不设性能门槛。
func hasCard(cards []CardEntry, id string) bool {
	for _, c := range cards {
		if c.ID == id {
			return true
		}
	}
	return false
}
