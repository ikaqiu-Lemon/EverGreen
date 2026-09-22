package plan

import (
	"fmt"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/store"
)

type CandidateDraft = store.CandidateDraft
type CandidateDraftSection = store.CandidateDraftSection
type CandidateCoverage = store.CandidateCoverage

func candidateDraftKnownKeys() []string {
	return []string{"key", "kind", "title", "source_refs", "rel", "reason", "tags", "sections"}
}

func candidateDraftSectionKnownKeys() []string { return []string{"name", "body"} }

func candidateCoverageKnownKeys() []string {
	return []string{"module", "source_refs", "summary", "disposition", "candidates", "reason"}
}

func parseCandidateDrafts(opIndex int, value interface{}) ([]CandidateDraft, []Diagnostic) {
	items, ok := asList(value)
	if !ok {
		return nil, []Diagnostic{errorAt(E5, opIndex, opPath(opIndex, "candidate_drafts"),
			"candidate_drafts 必须是有序对象数组")}
	}
	known := set(candidateDraftKnownKeys())
	var out []CandidateDraft
	var diags []Diagnostic
	for i, item := range items {
		path := candidateDraftPath(opIndex, i, "")
		m, ok := asMap(item)
		if !ok {
			diags = append(diags, errorAt(E5, opIndex, path,
				"candidate_drafts 的每一项必须是对象（恰 %v）", candidateDraftKnownKeys()))
			continue
		}
		draft := CandidateDraft{}
		draft.Key, _ = asString(m["key"])
		kind, _ := asString(m["kind"])
		draft.Kind = mdfile.CandidateKind(kind)
		draft.Title, _ = asString(m["title"])
		draft.Rel, _ = asString(m["rel"])
		draft.Reason, _ = asString(m["reason"])
		if refs, exists := m["source_refs"]; exists {
			parsed, ds := parseStringArrayStrict(opIndex, path+".source_refs", refs)
			draft.SourceRefs = parsed
			diags = append(diags, ds...)
		}
		if tags, exists := m["tags"]; exists {
			parsed, ds := parseStringArrayStrict(opIndex, path+".tags", tags)
			draft.Tags = parsed
			diags = append(diags, ds...)
		} else {
			draft.Tags = []string{}
		}
		if sections, exists := m["sections"]; exists {
			parsed, ds := parseCandidateDraftSections(opIndex, i, sections)
			draft.Sections = parsed
			diags = append(diags, ds...)
		}
		for key := range m {
			if known[key] {
				continue
			}
			diags = append(diags, infoAt(opIndex, path+"."+key,
				"未知附加字段已原样忽略（candidate_drafts 字段表恰 %v）",
				candidateDraftKnownKeys()))
		}
		out = append(out, draft)
	}
	return out, diags
}

func parseCandidateDraftSections(opIndex, draftIndex int,
	value interface{}) ([]CandidateDraftSection, []Diagnostic) {
	path := candidateDraftPath(opIndex, draftIndex, "sections")
	items, ok := asList(value)
	if !ok {
		return nil, []Diagnostic{errorAt(E5, opIndex, path,
			"candidate_drafts[].sections 必须是有序对象数组")}
	}
	known := set(candidateDraftSectionKnownKeys())
	var out []CandidateDraftSection
	var diags []Diagnostic
	for i, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		m, ok := asMap(item)
		if !ok {
			diags = append(diags, errorAt(E5, opIndex, itemPath,
				"candidate_drafts[].sections 的每一项必须是对象（恰 %v）",
				candidateDraftSectionKnownKeys()))
			continue
		}
		section := CandidateDraftSection{}
		section.Name, _ = asString(m["name"])
		if body, exists := m["body"]; exists {
			s, _ := asString(body)
			section.Body = []byte(s)
		}
		for key := range m {
			if known[key] {
				continue
			}
			diags = append(diags, infoAt(opIndex, itemPath+"."+key,
				"未知附加字段已原样忽略（candidate section 字段表恰 %v）",
				candidateDraftSectionKnownKeys()))
		}
		out = append(out, section)
	}
	return out, diags
}

func parseCandidateCoverage(opIndex int, value interface{}) ([]CandidateCoverage, []Diagnostic) {
	items, ok := asList(value)
	if !ok {
		return nil, []Diagnostic{errorAt(E5, opIndex, opPath(opIndex, "candidate_coverage"),
			"candidate_coverage 必须是有序对象数组")}
	}
	known := set(candidateCoverageKnownKeys())
	var out []CandidateCoverage
	var diags []Diagnostic
	for i, item := range items {
		path := candidateCoveragePath(opIndex, i, "")
		m, ok := asMap(item)
		if !ok {
			diags = append(diags, errorAt(E5, opIndex, path,
				"candidate_coverage 的每一项必须是对象（恰 %v）",
				candidateCoverageKnownKeys()))
			continue
		}
		c := CandidateCoverage{}
		c.Module, _ = asString(m["module"])
		c.Summary, _ = asString(m["summary"])
		c.Disposition, _ = asString(m["disposition"])
		c.Reason, _ = asString(m["reason"])
		if refs, exists := m["source_refs"]; exists {
			parsed, ds := parseStringArrayStrict(opIndex, path+".source_refs", refs)
			c.SourceRefs = parsed
			diags = append(diags, ds...)
		}
		if candidates, exists := m["candidates"]; exists {
			parsed, ds := parseStringArrayStrict(opIndex, path+".candidates", candidates)
			c.Candidates = parsed
			diags = append(diags, ds...)
		} else {
			c.Candidates = []string{}
		}
		for key := range m {
			if known[key] {
				continue
			}
			diags = append(diags, infoAt(opIndex, path+"."+key,
				"未知附加字段已原样忽略（candidate_coverage 字段表恰 %v）",
				candidateCoverageKnownKeys()))
		}
		out = append(out, c)
	}
	return out, diags
}

func candidateDraftPath(opIndex, i int, key string) string {
	base := fmt.Sprintf("ops[%d].candidate_drafts[%d]", opIndex, i)
	if key == "" {
		return base
	}
	return base + "." + key
}

func candidateCoveragePath(opIndex, i int, key string) string {
	base := fmt.Sprintf("ops[%d].candidate_coverage[%d]", opIndex, i)
	if key == "" {
		return base
	}
	return base + "." + key
}

// noteCandidateDraftBytes validates the draft-only extraction state and
// returns the exact payload to append to 提取结果.
func (v *validator) noteCandidateDraftBytes(op *Op) ([]byte, bool) {
	if !op.CandidateDraftsGiven && !op.CandidateCoverageGiven {
		return nil, true
	}
	ok := true
	if v.p.Version != PlanVersion || !op.BlocksGiven {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate_drafts"),
			"candidate_drafts/candidate_coverage 只允许用于 plan_version=%d 且 blocks[] 的 write_note",
			PlanVersion))
		ok = false
	}
	if len(op.CandidateDrafts) == 0 {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate_drafts"),
			"candidate_drafts 必须显式给出至少一个候选"))
		ok = false
	}
	if len(op.CandidateCoverage) == 0 {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate_coverage"),
			"candidate_coverage 必须显式给出至少一条草稿覆盖"))
		ok = false
	}
	if len(op.OutputCards) != 0 {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "output_cards"),
			"candidate_drafts 与 output_cards 互斥：草稿不得伪造尚未物化的 output"))
		ok = false
	}
	if len(op.ExtractionCoverage) != 0 {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "extraction_coverage"),
			"candidate_drafts 与非空 extraction_coverage 互斥：草稿覆盖与最终覆盖是两种状态"))
		ok = false
	}

	declaredRefs := map[string]bool{}
	for _, block := range op.Blocks {
		if block.Role == NoteBlockSource {
			declaredRefs[block.SourceRef] = true
		}
	}
	allKeys, currentKeys, existingOK := v.existingCandidateKeys(op)
	if !existingOK {
		ok = false
	}
	renderDrafts := make([]CandidateDraft, len(op.CandidateDrafts))
	for i, draft := range op.CandidateDrafts {
		path := candidateDraftPath(op.Index, i, "")
		if currentKeys[draft.Key] {
			v.add(errorAt(E2, op.Index, path+".key",
				"candidate key 在本次 candidate_drafts 内重复：%q", draft.Key))
			ok = false
		} else if allKeys[draft.Key] {
			v.add(errorAt(E2, op.Index, path+".key",
				"candidate key 已存在于目标 Note：%q", draft.Key))
			ok = false
		}
		currentKeys[draft.Key] = true
		allKeys[draft.Key] = true
		if draft.Output != "" {
			v.add(errorAt(E2, op.Index, path+".output",
				"candidate_drafts 的 output 必须为空：映射只能由 eg materialize 写入"))
			ok = false
		}
		for j, ref := range draft.SourceRefs {
			if !declaredRefs[ref] {
				v.add(errorAt(E2, op.Index,
					fmt.Sprintf("%s.source_refs[%d]", path, j),
					"candidate source_ref=%q 未精确回指本次 role:source 块", ref))
				ok = false
			}
		}
		renderDrafts[i] = draft
		renderDrafts[i].Sections = make([]CandidateDraftSection, len(draft.Sections))
		for j, section := range draft.Sections {
			renderDrafts[i].Sections[j] = CandidateDraftSection{
				Name: section.Name, Body: lineTerminated(section.Body),
			}
		}
		if _, err := mdfile.RenderCandidateDraft(renderDrafts[i]); err != nil {
			v.add(errorAt(E2, op.Index, path, "candidate draft 不成立：%v", err))
			ok = false
		}
	}

	coveredRefs := map[string]bool{}
	referencedCurrent := map[string]bool{}
	seenModules := map[string]bool{}
	for i, c := range op.CandidateCoverage {
		path := candidateCoveragePath(op.Index, i, "")
		if strings.TrimSpace(c.Module) == "" {
			v.add(errorAt(E2, op.Index, path+".module", "candidate_coverage module 为空"))
			ok = false
		} else if seenModules[c.Module] {
			v.add(errorAt(E2, op.Index, path+".module",
				"candidate_coverage module 原始字节重复：%q", c.Module))
			ok = false
		}
		seenModules[c.Module] = true
		if len(c.SourceRefs) == 0 {
			v.add(errorAt(E2, op.Index, path+".source_refs",
				"candidate_coverage source_refs 为空"))
			ok = false
		}
		for j, ref := range c.SourceRefs {
			if !declaredRefs[ref] {
				v.add(errorAt(E2, op.Index, fmt.Sprintf("%s.source_refs[%d]", path, j),
					"candidate_coverage source_ref=%q 未精确回指本次 role:source 块", ref))
				ok = false
			} else {
				coveredRefs[ref] = true
			}
		}
		if strings.TrimSpace(c.Summary) == "" {
			v.add(errorAt(E2, op.Index, path+".summary", "candidate_coverage summary 为空"))
			ok = false
		}
		switch c.Disposition {
		case mdfile.CandidateCoverageCandidate:
			if len(c.Candidates) == 0 {
				v.add(errorAt(E2, op.Index, path+".candidates",
					"disposition=candidate 必须引用至少一个 candidate key"))
				ok = false
			}
			if strings.TrimSpace(c.Reason) != "" {
				v.add(errorAt(E2, op.Index, path+".reason",
					"disposition=candidate 不得带 reason"))
				ok = false
			}
			for j, key := range c.Candidates {
				if !allKeys[key] {
					v.add(errorAt(E2, op.Index, fmt.Sprintf("%s.candidates[%d]", path, j),
						"candidate key 不存在于本次或目标 Note：%q", key))
					ok = false
				}
				if currentKeys[key] {
					referencedCurrent[key] = true
				}
			}
		case mdfile.CandidateCoverageNoteOnly, mdfile.CandidateCoverageUnresolved:
			if len(c.Candidates) != 0 {
				v.add(errorAt(E2, op.Index, path+".candidates",
					"disposition=%s 不得带 candidates", c.Disposition))
				ok = false
			}
			if strings.TrimSpace(c.Reason) == "" {
				v.add(errorAt(E2, op.Index, path+".reason",
					"disposition=%s 必须说明具体原因", c.Disposition))
				ok = false
			}
		default:
			v.add(errorAt(E2, op.Index, path+".disposition",
				"candidate_coverage disposition=%q 越界（封闭三值 candidate/note_only/unresolved）",
				c.Disposition))
			ok = false
		}
	}
	for ref := range declaredRefs {
		if !coveredRefs[ref] {
			v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate_coverage"),
				"role:source 的 source_ref=%q 未进入任何 candidate_coverage 模块", ref))
			ok = false
		}
	}
	for key := range currentKeys {
		if !referencedCurrent[key] {
			v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate_coverage"),
				"本次 candidate_drafts 的 key=%q 未被 disposition=candidate 的覆盖项引用", key))
			ok = false
		}
	}
	if !ok {
		return nil, false
	}
	body, err := store.CandidateDraftBytes(renderDrafts, op.CandidateCoverage)
	if err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "candidate_drafts"),
			"candidate 草稿无法渲染：%v", err))
		return nil, false
	}
	return body, true
}

func (v *validator) existingCandidateKeys(op *Op) (map[string]bool, map[string]bool, bool) {
	all := map[string]bool{}
	current := map[string]bool{}
	if op.NoteID == "" {
		return all, current, true
	}
	rel, exists := v.resolve(op.NoteID)
	if !exists {
		return all, current, true
	}
	raw, exists := v.readExisting(rel)
	if !exists {
		return all, current, true
	}
	candidates, err := mdfile.ParseCandidates(raw)
	if err != nil {
		v.add(errorAt(E2, op.Index, opPath(op.Index, "note_id"),
			"目标 Note 的既有 candidate 无法严格读回：%v", err))
		return all, current, false
	}
	for _, candidate := range candidates {
		all[candidate.Key] = true
	}
	return all, current, true
}
