package plan

// v2 `write_note` 的 **extraction_coverage 语义校验**（Schema v2 契约 §4.2.3）：
// 只加严 plan_version: 2 且 blocks[] 给出（BlocksGiven）的 write_note，逐字段判 E2（不新增码），
// 任一违规整条 op 零 action。与 source_coverage.go 同一分层原则：这里只回答「这样写允不允许」，
// 落盘渲染的字节形态由 store / mdfile 的覆盖矩阵协议负责。
//
// 本文件锁死的语义（契约 §4.2.3）：
//   - 存在性：该路径必须显式给出**非空** extraction_coverage[]；缺字段或空数组 → 整条 op 字段级 E2；
//   - module：TrimSpace 非空，且在本 Note 内按**原始字节**唯一（不 trim / 不归一化后比较）；
//   - source_refs：每项 TrimSpace 非空，且精确回指本次 role:source 块声明的 source_ref（悬空即拒）；
//     每段 role:source 的 source_ref 至少进入一个覆盖模块（漏覆盖钉回该 source 块的字段路径）；
//   - summary：TrimSpace 非空；
//   - disposition：封闭三值 outputs|note_only|missing——
//       · outputs：outputs 至少 1 个、每个是形态完整的 k-*/o-* 端点、reason 必须为空；
//       · note_only：outputs 必须为空、reason TrimSpace 非空；
//       · missing：outputs 必须为空、reason TrimSpace 非空，且**无条件** E2 阻止 apply（缺漏必须清零）；
//   - 与 output_cards 双向一致（不按次数）：每个 outputs 端点必须出现在 output_cards.card；
//     每张 output_cards.card 必须至少被一个 disposition=outputs 的模块引用；output_cards 中
//     空值 / 非法 / 非 k-*/o-* ID 一律 E2。
//
// **不短路**：把「写错了字」「引用悬空」「与产出卡对不上」分层报全，读的人一眼知道该改哪一层，
// 而不是修一处又冒一处。任一 E2 即返回 false，整条 write_note 零写入。

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
)

// noteCoverageValidate 是 v2 blocks 的 extraction_coverage 语义闸门（§4.2.3）。
// 返回 false 表示已登记至少一条 E2、整条 op 零写入。只在 plan_version==PlanVersion 且 BlocksGiven
// 的路径调用（noteBlockWrites 内），兼容路径（v1 sections / v1 blocks / v2 sections）不波及。
func (v *validator) noteCoverageValidate(op *Op) bool {
	if !op.ExtractionCoverageGiven || len(op.ExtractionCoverage) == 0 {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "extraction_coverage"),
			"plan_version=%d 且 blocks[] 的 write_note 必须显式给出非空 extraction_coverage[]："+
				"审阅式提炼要如实交代每段来源去向（产出卡 / 仅记录 / 缺漏），"+
				"缺该字段或空数组都无法证明「本次提炼覆盖了所有来源」（契约 §4.2.3）", v.p.Version))
		return false
	}

	// 本次 role:source 块声明的 source_ref（精确回指用）与它们的字段路径（漏覆盖钉回块上）。
	declared := map[string]bool{}
	type srcRef struct{ ref, path string }
	var srcBlocks []srcRef
	for i, b := range op.Blocks {
		if b.Role != NoteBlockSource {
			continue
		}
		declared[b.SourceRef] = true
		srcBlocks = append(srcBlocks, srcRef{ref: b.SourceRef, path: blockPath(op.Index, i, "source_ref")})
	}

	ok := true

	// output_cards 形态：每张卡必须是形态完整的 k-*/o-* 端点。合法卡入集合供双向一致判定。
	cardValid := map[string]bool{}
	for i, c := range op.OutputCards {
		path := fmt.Sprintf("ops[%d].output_cards[%d].card", op.Index, i)
		if _, err := model.ParseRelationEndpoint(c.Card); err != nil {
			v.add(errorAt(E2, op.Index, path,
				"output_cards[%d].card=%q 不是形态完整的 k-*/o-* 关系端点：%v", i, c.Card, err))
			ok = false
			continue
		}
		cardValid[c.Card] = true
	}

	covered := map[string]bool{}        // 被任一模块回指的 source_ref
	cardReferenced := map[string]bool{} // 被 disposition=outputs 模块引用的产出卡
	seenModule := map[string]bool{}     // 已出现的 module 原始字节

	for i, c := range op.ExtractionCoverage {
		base := coveragePath(op.Index, i, "")

		// module：TrimSpace 非空 + 原始字节唯一。
		switch {
		case strings.TrimSpace(c.Module) == "":
			v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "module"),
				"extraction_coverage[%d].module 为空：每个语义模块必须有非空名字", i))
			ok = false
		case seenModule[c.Module]:
			v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "module"),
				"extraction_coverage[%d].module=%q 与前一模块原始字节重复：模块名在本 Note 内按原始字节唯一"+
					"（不 trim / 不归一化后再比）", i, c.Module))
			ok = false
		default:
			seenModule[c.Module] = true
		}

		// source_refs：非空 + 每项 TrimSpace 非空 + 精确回指本次声明。
		if len(c.SourceRefs) == 0 {
			v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "source_refs"),
				"extraction_coverage[%d].source_refs 为空：每个语义模块至少回指一段来源", i))
			ok = false
		} else {
			for j, ref := range c.SourceRefs {
				rp := fmt.Sprintf("%s.source_refs[%d]", base, j)
				if strings.TrimSpace(ref) == "" {
					v.add(errorAt(E2, op.Index, rp,
						"extraction_coverage[%d].source_refs[%d] 为空", i, j))
					ok = false
					continue
				}
				if !declared[ref] {
					v.add(errorAt(E2, op.Index, rp,
						"extraction_coverage[%d].source_refs[%d]=%q 未回指本次任何 role:source 块声明的"+
							" source_ref（悬空引用）", i, j, ref))
					ok = false
					continue
				}
				covered[ref] = true
			}
		}

		// summary：TrimSpace 非空。
		if strings.TrimSpace(c.Summary) == "" {
			v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "summary"),
				"extraction_coverage[%d].summary 为空：语义模块要有一句人读概述", i))
			ok = false
		}

		// disposition + outputs/reason 形态 + output_cards 一致。
		switch c.Disposition {
		case mdfile.CoverageDispOutputs:
			if len(c.Outputs) == 0 {
				v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "outputs"),
					"extraction_coverage[%d] 处置为 outputs 却无 outputs：产出去向必须至少给一张卡", i))
				ok = false
			}
			if strings.TrimSpace(c.Reason) != "" {
				v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "reason"),
					"extraction_coverage[%d] 处置为 outputs 不得带 reason：reason 只用于 note_only / missing", i))
				ok = false
			}
			for j, out := range c.Outputs {
				outPath := fmt.Sprintf("%s.outputs[%d]", base, j)
				if _, err := model.ParseRelationEndpoint(out); err != nil {
					v.add(errorAt(E2, op.Index, outPath,
						"extraction_coverage[%d].outputs[%d]=%q 不是形态完整的 k-*/o-* 端点：%v", i, j, out, err))
					ok = false
					continue
				}
				if !cardValid[out] {
					v.add(errorAt(E2, op.Index, outPath,
						"extraction_coverage[%d].outputs[%d]=%q 未出现在 output_cards：覆盖模块与产出卡"+
							"必须成员一致（coverage → cards 方向）", i, j, out))
					ok = false
					continue
				}
				cardReferenced[out] = true
			}
		case mdfile.CoverageDispNoteOnly, mdfile.CoverageDispMissing:
			if len(c.Outputs) != 0 {
				v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "outputs"),
					"extraction_coverage[%d] 处置为 %s 不得带 outputs：不产出卡的模块不应引用产出卡", i, c.Disposition))
				ok = false
			}
			if strings.TrimSpace(c.Reason) == "" {
				v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "reason"),
					"extraction_coverage[%d] 处置为 %s 必须给 TrimSpace 后非空的 reason：如实交代为何不产出卡", i, c.Disposition))
				ok = false
			}
			if c.Disposition == mdfile.CoverageDispMissing {
				v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "disposition"),
					"extraction_coverage[%d] 处置为 missing（表缺漏）：缺漏必须清零后本 write_note 才能落盘"+
						"——missing 无条件阻止 apply（契约 §4.2.3）", i))
				ok = false
			}
		default:
			v.add(errorAt(E2, op.Index, coveragePath(op.Index, i, "disposition"),
				"extraction_coverage[%d].disposition=%q 越界（封闭三值 %s / %s / %s）",
				i, c.Disposition, mdfile.CoverageDispOutputs, mdfile.CoverageDispNoteOnly, mdfile.CoverageDispMissing))
			ok = false
		}
	}

	// 每段整理进正文的来源都要被某个语义模块覆盖（漏覆盖钉回该 source 块的字段路径）。
	for _, sb := range srcBlocks {
		if !covered[sb.ref] {
			v.add(errorAt(E2, op.Index, sb.path,
				"role:source 块的 source_ref=%q 未进入任何覆盖模块：每段整理进正文的来源都要被某个"+
					"语义模块覆盖（契约 §4.2.3）", sb.ref))
			ok = false
		}
	}

	// output_cards → coverage 方向：每张产出卡至少被一个 disposition=outputs 的模块引用。
	for i, c := range op.OutputCards {
		if !cardValid[c.Card] {
			continue // 形态非法的卡已各自 E2，不再重复钉未引用。
		}
		if !cardReferenced[c.Card] {
			v.add(errorAt(E2, op.Index, fmt.Sprintf("ops[%d].output_cards[%d].card", op.Index, i),
				"output_cards[%d].card=%q 未被任何 disposition=outputs 的覆盖模块引用："+
					"产出卡与覆盖模块必须双向一致（cards → coverage 方向）", i, c.Card))
			ok = false
		}
	}

	return ok
}
