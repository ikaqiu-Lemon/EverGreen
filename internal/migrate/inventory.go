package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/mdfile"
	"github.com/ikaqiu-Lemon/EverGreen/internal/segment"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

const ManifestSpec = "evergreen.migration-manifest/v1"

type File struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Size int    `json:"size"`
}

type Quarantine struct {
	Code   string `json:"code"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Entity struct {
	LogicalID  core.LogicalID `json:"logical_id"`
	Type       string         `json:"entity_type"`
	Kind       string         `json:"claim_kind,omitempty"`
	Source     string         `json:"source_path"`
	SourceHash string         `json:"source_hash"`
	DocumentID string         `json:"target_document_id"`
	Path       string         `json:"target_path"`
	Hash       string         `json:"expected_semantic_hash"`
	Unknown    []string       `json:"unknown_fields,omitempty"`
}

type Manifest struct {
	Spec       string                          `json:"spec"`
	TargetSpec core.SchemaRef                  `json:"target_spec"`
	Revision   string                          `json:"source_vault_revision"`
	SourceHash string                          `json:"source_tree_hash"`
	Hash       string                          `json:"manifest_hash"`
	Files      []File                          `json:"files"`
	Entities   []Entity                        `json:"entities"`
	Reviews    []segment.LegacyParityInventory `json:"reviews"`
	Counts     map[string]int                  `json:"counts"`
	Quarantine []Quarantine                    `json:"quarantine"`
	Legacy     []LegacyRecord                  `json:"legacy_inventory"`
	Assets     []Asset                         `json:"assets"`
	Edges      []EdgeMapping                   `json:"edges"`
}

type EdgeMapping struct {
	Owner core.LogicalID `json:"owner"`
	Edge  core.TypedEdge `json:"edge"`
}

type LegacyRecord struct {
	LogicalID  core.LogicalID  `json:"logical_id"`
	Type       string          `json:"type"`
	Path       string          `json:"path"`
	Metadata   map[string]any  `json:"metadata"`
	BodyHash   string          `json:"body_hash"`
	Review     json.RawMessage `json:"review,omitempty"`
	Candidates json.RawMessage `json:"candidates,omitempty"`
	Coverage   json.RawMessage `json:"coverage,omitempty"`
}

type Prepared struct {
	Manifest Manifest
	Images   map[string][]byte
}

type legacyEntity struct {
	path string
	raw  []byte
	doc  *mdfile.Doc
	meta map[string]any
	id   core.LogicalID
	kind string
}

// Inventory 扫描源文件且零写入；Git revision 与工作树 digest 分别绑定，避免遗漏用户修改。
func Inventory(root string) (Prepared, error) {
	result := Prepared{
		Manifest: Manifest{Spec: ManifestSpec, TargetSpec: core.DocumentSpec,
			Counts: map[string]int{}, Files: []File{}, Entities: []Entity{},
			Reviews: []segment.LegacyParityInventory{}, Quarantine: []Quarantine{}},
		Images: map[string][]byte{},
	}
	revision, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return result, fmt.Errorf("source vault must have an immutable Git revision: %w", err)
	}
	result.Manifest.Revision = strings.TrimSpace(string(revision))
	legacy := map[core.LogicalID]legacyEntity{}
	workspaces := map[core.LogicalID]legacyEntity{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source symlink is not an authority file: %s", rel)
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		result.Manifest.Files = append(result.Manifest.Files, File{Path: rel, Hash: hashBytes(raw), Size: len(raw)})
		if filepath.Ext(path) != ".md" {
			return nil
		}
		doc, parseErr := mdfile.Parse(raw)
		if parseErr != nil {
			return parseErr
		}
		meta := map[string]any{}
		if parseErr = doc.DecodeFM(&meta); parseErr != nil {
			return parseErr
		}
		id := core.LogicalID(text(meta["id"]))
		if id == "" {
			return nil
		}
		item := legacyEntity{path: rel, raw: raw, doc: doc, meta: meta, id: id}
		switch {
		case strings.HasPrefix(string(id), "ns-"):
			item.kind = "workspace"
		case strings.HasPrefix(string(id), "s-"):
			item.kind = "source"
		case strings.HasPrefix(string(id), "n-"):
			item.kind = "note"
		case strings.HasPrefix(string(id), "k-"):
			item.kind = "knowledge"
		case strings.HasPrefix(string(id), "o-"):
			item.kind = "opinion"
		default:
			result.quarantine("EG_MIGRATION_ENTITY_UNSUPPORTED", rel, "unsupported legacy entity "+string(id))
			return nil
		}
		if _, exists := legacy[id]; exists {
			return fmt.Errorf("EG_DUPLICATE_ID: %s", id)
		}
		legacy[id] = item
		record := LegacyRecord{LogicalID: id, Type: item.kind, Path: rel, Metadata: meta, BodyHash: hashBytes(raw[doc.BodyFrom:])}
		if item.kind == "workspace" {
			candidates, candidateErr := mdfile.ParseCandidates(raw)
			if candidateErr != nil {
				return candidateErr
			}
			coverage, coverageErr := mdfile.ParseCandidateCoverageState(raw)
			if coverageErr != nil {
				return coverageErr
			}
			record.Candidates, _ = json.Marshal(candidates)
			record.Coverage, _ = json.Marshal(coverage)
			result.Manifest.Counts["legacy_candidates"] += len(candidates)
			result.Manifest.Counts["legacy_coverage"] += len(coverage.Draft) + len(coverage.Final)
			if blocks, found, blockErr := mdfile.ParseNoteBlockManifest(raw); blockErr != nil {
				return blockErr
			} else if found {
				record.Review, _ = json.Marshal(blocks)
				result.Manifest.Counts["legacy_segments"] += len(blocks.Blocks)
			}
		}
		if item.kind == "note" {
			if body, exists := doc.Section(mdfile.SecNoteBody); exists {
				if review, reviewErr := mdfile.ParseReviewNote(raw[body.Body:body.End]); reviewErr == nil {
					record.Review, _ = json.Marshal(review)
					result.Manifest.Counts["legacy_segments"] += len(review.Blocks)
				}
			}
		}
		result.Manifest.Legacy = append(result.Manifest.Legacy, record)
		result.Manifest.Counts[item.kind]++
		if item.kind == "workspace" {
			noteID := core.LogicalID(text(meta["note"]))
			if _, exists := workspaces[noteID]; exists {
				return fmt.Errorf("EG_DUPLICATE_ID: multiple workspaces for %s", noteID)
			}
			workspaces[noteID] = item
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	fileJSON, _ := json.Marshal(result.Manifest.Files)
	result.Manifest.SourceHash = hashBytes(fileJSON)
	ids := make([]core.LogicalID, 0, len(legacy))
	for id := range legacy {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	documents := map[core.LogicalID][]byte{}
	noteRefs := map[core.LogicalID][]core.LogicalID{}
	for _, id := range ids {
		item := legacy[id]
		if item.kind == "workspace" || item.kind == "knowledge" || item.kind == "opinion" {
			continue
		}
		envelope := baseEnvelope(item)
		if item.kind == "note" {
			if workspace, exists := workspaces[id]; exists {
				imported, importErr := segment.ImportLegacyWorkspace(item.raw, workspace.raw)
				if importErr != nil {
					result.quarantine("EG_MIGRATION_REVIEW_UNMAPPABLE", item.path, importErr.Error())
					continue
				}
				result.Manifest.Reviews = append(result.Manifest.Reviews, imported.Inventory)
				result.Manifest.Counts["segments"] += len(imported.Inventory.Segments)
				result.Manifest.Counts["candidates"] += len(imported.Inventory.Candidates)
				result.Manifest.Counts["coverage"] += imported.Inventory.CoverageCount
				decoded, _ := core.DecodeSY(imported.SY, nil)
				decoded.Envelope.Extra = envelope.Extra
				documents[id], err = core.EncodeSY(imported.SY, decoded.Envelope, nil)
				if err != nil {
					return result, err
				}
				for _, s := range imported.Inventory.Segments {
					noteRefs[id] = append(noteRefs[id], s.SegmentID)
				}
				continue
			}
		}
		body, plainErr := mdfile.PlainExport(item.raw[item.doc.BodyFrom:])
		if plainErr != nil {
			return result, plainErr
		}
		documents[id], err = core.NewMarkdownDocument(id, title(item), body, envelope, nil)
		if err != nil {
			return result, err
		}
		if item.kind == "note" {
			documents[id], noteRefs[id], err = wrapWholeNote(documents[id], id, core.LogicalID(text(item.meta["source"])))
			if err != nil {
				return result, err
			}
			result.Manifest.Counts["segments"]++
			result.Manifest.Counts["coverage"]++
		}
	}
	for _, id := range ids {
		item := legacy[id]
		if item.kind != "knowledge" && item.kind != "opinion" {
			continue
		}
		envelope := baseEnvelope(item)
		envelope.Entity.EntityType = core.EntityClaim
		envelope.Entity.Schema = "evergreen.claim/v1"
		kindSchema := core.KnowledgeKindSchema
		data := json.RawMessage(`{}`)
		if item.kind == "opinion" {
			kindSchema = core.OpinionKindSchema
			data, _ = json.Marshal(map[string]any{"validation": map[string]any{"status": item.meta["validation"]}})
		}
		envelope.Claim = &core.Claim{ClaimKind: core.ClaimKind(item.kind), KindSchema: kindSchema,
			Status: text(item.meta["status"]), Tags: stringsList(item.meta["tags"]), KindData: data}
		for index, source := range objectList(item.meta["sources"]) {
			noteID, sourceID := core.LogicalID(text(source["note"])), core.LogicalID(text(source["source"]))
			refs := noteRefs[noteID]
			if len(refs) == 0 || documents[sourceID] == nil || text(source["reason"]) == "" || text(source["rel"]) == "" {
				result.quarantine("EG_MIGRATION_EDGE_CONTEXT", item.path, fmt.Sprintf("sources[%d] has missing target, reason, type or Note context", index))
				continue
			}
			// 旧材料关系只给 Note 级上下文，保守映射为完整 Note 范围，显式记录 scope。
			provenance := core.Provenance{SourceID: sourceID, NoteID: noteID,
				SegmentRefs: append([]core.LogicalID(nil), refs...), Reason: text(source["reason"])}
			envelope.Provenance = append(envelope.Provenance, provenance)
			edge := legacyEdge(id, "material", index, source, core.MaterialSchema, sourceID, text(source["rel"]))
			edge.Target.EntityType = core.EntitySource
			edge.Context = core.EdgeContext{NoteID: noteID, SegmentRefs: refs,
				Data: core.RawObject{"legacy_context_scope": json.RawMessage(`"whole_note"`)}}
			envelope.Relations.Outgoing = append(envelope.Relations.Outgoing, edge)
		}
		for index, relation := range objectList(item.meta["relations"]) {
			target := core.LogicalID(text(relation["target"]))
			if _, exists := legacy[target]; !exists || text(relation["reason"]) == "" {
				result.quarantine("EG_MIGRATION_EDGE_CONTEXT", item.path, fmt.Sprintf("relations[%d] missing target or reason", index))
				continue
			}
			edge := legacyEdge(id, "argument", index, relation, core.ArgumentSchema, target, text(relation["type"]))
			owner := id
			if edge.Type == "opposing" && owner > target {
				owner = target
				edge.Target.LogicalID = id
			}
			if owner == id {
				envelope.Relations.Outgoing = append(envelope.Relations.Outgoing, edge)
			} else {
				// 跨 owner 重排在第二遍统一完成。
				result.Images["pending/"+string(owner)+"/"+string(edge.ID)], _ = json.Marshal(edge)
			}
		}
		if replacement, ok := item.meta["replaced_by"].(map[string]any); ok {
			target := core.LogicalID(text(replacement["target"]))
			if _, exists := legacy[target]; !exists || text(replacement["reason"]) == "" {
				result.quarantine("EG_MIGRATION_EDGE_CONTEXT", item.path, "replacement has missing target or reason")
			} else {
				envelope.Relations.Outgoing = append(envelope.Relations.Outgoing,
					legacyEdge(id, "replacement", 0, replacement, core.ReplacementSchema, target, "replaced_by"))
			}
		}
		body, plainErr := mdfile.PlainExport(item.raw[item.doc.BodyFrom:])
		if plainErr != nil {
			return result, plainErr
		}
		documents[id], err = core.NewMarkdownDocument(id, title(item), body, envelope, nil)
		if err != nil {
			result.quarantine("EG_MIGRATION_CLAIM_INVALID", item.path, err.Error())
			delete(documents, id)
		}
	}
	pending := []string{}
	for key := range result.Images {
		if strings.HasPrefix(key, "pending/") {
			pending = append(pending, key)
		}
	}
	sort.Strings(pending)
	for _, key := range pending {
		raw := result.Images[key]
		owner := core.LogicalID(strings.Split(key, "/")[1])
		delete(result.Images, key)
		if documents[owner] == nil {
			result.quarantine("EG_MIGRATION_EDGE_CONTEXT", string(owner), "canonical edge owner did not migrate")
			continue
		}
		document, decodeErr := core.DecodeSY(documents[owner], nil)
		if decodeErr != nil {
			return result, decodeErr
		}
		var edge core.TypedEdge
		if err = json.Unmarshal(raw, &edge); err != nil {
			return result, err
		}
		document.Envelope.Relations.Outgoing = append(document.Envelope.Relations.Outgoing, edge)
		documents[owner], err = core.EncodeSY(documents[owner], document.Envelope, nil)
		if err != nil {
			return result, err
		}
	}
	for _, id := range ids {
		raw, exists := documents[id]
		if !exists {
			continue
		}
		item := legacy[id]
		decoded, decodeErr := core.DecodeSY(raw, nil)
		if decodeErr != nil {
			return result, decodeErr
		}
		var node struct{ ID string }
		_ = json.Unmarshal(raw, &node)
		targetPath := "20000101000000-egvault/" + node.ID + ".sy"
		hash, _ := core.SemanticHash(raw)
		result.Images[targetPath] = raw
		keys := []string{}
		for key := range item.meta {
			if !knownLegacyKey(key) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		result.Manifest.Entities = append(result.Manifest.Entities, Entity{
			LogicalID: id, Type: string(decoded.Envelope.Entity.EntityType), Kind: claimKind(decoded),
			Source: item.path, SourceHash: hashBytes(item.raw), DocumentID: node.ID, Path: targetPath, Hash: hash, Unknown: keys,
		})
		result.Manifest.Counts["edges"] += len(decoded.Envelope.Relations.Outgoing)
	}
	if err = result.migrateAssets(root); err != nil {
		return result, err
	}
	seenEdges := map[core.EdgeID]bool{}
	for _, entity := range result.Manifest.Entities {
		document, err := core.DecodeSY(result.Images[entity.Path], nil)
		if err != nil {
			return result, err
		}
		for _, edge := range document.Envelope.Relations.Outgoing {
			if seenEdges[edge.ID] {
				return result, fmt.Errorf("EG_DUPLICATE_ID: edge %s", edge.ID)
			}
			seenEdges[edge.ID] = true
			result.Manifest.Edges = append(result.Manifest.Edges, EdgeMapping{Owner: entity.LogicalID, Edge: edge})
		}
	}
	result.Manifest.Hash = manifestHash(result.Manifest)
	return result, nil
}

func baseEnvelope(item legacyEntity) *core.DocumentEnvelope {
	extra := map[string]any{}
	for key, value := range item.meta {
		switch key {
		case "id", "status", "tags", "validation", "sources", "relations", "replaced_by":
			continue
		}
		extra[key] = value
	}
	domain := ""
	parts := strings.Split(item.path, "/")
	if len(parts) > 2 && parts[0] == "domains" {
		domain = parts[1]
	}
	extra["domain"] = domain
	raw, _ := json.Marshal(extra)
	return &core.DocumentEnvelope{Spec: core.DocumentSpec,
		Entity: core.Entity{LogicalID: item.id, EntityType: core.EntityType(item.kind),
			Schema: core.SchemaRef("evergreen." + item.kind + "/v1"), SemanticRevision: 1},
		Relations: core.Relations{Outgoing: []core.TypedEdge{}},
		Extra:     core.RawObject{"legacy_metadata": raw}}
}

func wrapWholeNote(raw []byte, id, source core.LogicalID) ([]byte, []core.LogicalID, error) {
	var node map[string]json.RawMessage
	_ = json.Unmarshal(raw, &node)
	segmentID := core.LogicalID("seg-" + strings.TrimPrefix(string(id), "n-") + "-whole")
	var children []json.RawMessage
	_ = json.Unmarshal(node["Children"], &children)
	container := map[string]any{"ID": core.StablePhysicalID(string(segmentID)), "Type": "NodeSuperBlock",
		"Children": core.SuperBlockChildren(children)}
	containerRaw, _ := json.Marshal(container)
	hash, _ := core.NormalizedSegmentHash(containerRaw)
	block, err := core.MarshalBlockEnvelope(&core.BlockEnvelope{Spec: core.BlockSpec, Role: "note_segment",
		Segment: &core.SegmentMetadata{SegmentID: segmentID, NormalizedHash: hash,
			SourceRef: core.SourceLocator{SourceID: source, Locator: core.Locator{Kind: "legacy-note-context", Value: string(id)}}}})
	if err != nil {
		return nil, nil, err
	}
	container["Evergreen"] = block
	containerRaw, _ = json.Marshal(container)
	node["Children"], _ = json.Marshal([]json.RawMessage{containerRaw})
	document, _ := core.DecodeSY(raw, nil)
	document.Envelope.Review = &core.NoteReview{Spec: core.NoteReviewSpec,
		Coverage: []core.CoverageModule{{ModuleID: core.LogicalID("coverage-" + string(id)), Disposition: "note_only",
			SegmentRefs: []core.LogicalID{segmentID}, Reason: "Legacy Note has no Candidate review workspace."}}}
	base, _ := json.Marshal(node)
	out, err := core.EncodeSY(base, document.Envelope, nil)
	return out, []core.LogicalID{segmentID}, err
}

func legacyEdge(owner core.LogicalID, family string, index int, metadata map[string]any, schema core.SchemaRef, target core.LogicalID, kind string) core.TypedEdge {
	seed, _ := json.Marshal(metadata)
	return core.TypedEdge{
		ID:     core.EdgeID("edge-" + strings.TrimPrefix(core.StablePhysicalID(fmt.Sprintf("%s/%s/%d/%s", owner, family, index, seed)), "20000101000000-")),
		Schema: schema, Target: core.EntityRef{LogicalID: target, EntityType: core.EntityClaim},
		Type: kind, Reason: text(metadata["reason"]),
	}
}

func (p *Prepared) quarantine(code, path, reason string) {
	p.Manifest.Quarantine = append(p.Manifest.Quarantine, Quarantine{Code: code, Path: path, Reason: reason})
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func manifestHash(manifest Manifest) string {
	manifest.Hash = ""
	raw, _ := json.Marshal(manifest)
	return hashBytes(raw)
}

func text(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func stringsList(value any) []string {
	result := []string{}
	if items, ok := value.([]any); ok {
		for _, item := range items {
			result = append(result, text(item))
		}
	}
	return result
}

func objectList(value any) []map[string]any {
	result := []map[string]any{}
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if object, ok := item.(map[string]any); ok {
				result = append(result, object)
			}
		}
	}
	return result
}

func title(item legacyEntity) string {
	if value := text(item.meta["title"]); value != "" {
		return value
	}
	return string(item.id)
}

func claimKind(document *core.SYDocument) string {
	if document.Envelope.Claim != nil {
		return string(document.Envelope.Claim.ClaimKind)
	}
	return ""
}

func knownLegacyKey(key string) bool {
	return strings.Contains("|id|source|title|url|saved_at|status|tags|created_at|updated_at|validation|sources|relations|replaced_by|deleted_at|deleted_reason|reviewed_at|", "|"+key+"|")
}

// Repository 的查询只从 .sy 读取，manifest 不作为运行时索引。
func Repository(root string) (*core.FilesystemAuthority, error) {
	return core.NewFilesystemAuthority(core.FilesystemAuthorityOptions{Root: root, Registry: core.ApplicationRegistry()})
}

func Export(root string) (core.CanonicalArchive, error) {
	repository, err := core.NewSYReadRepository(root, core.ApplicationRegistry())
	if err != nil {
		return core.CanonicalArchive{}, err
	}
	return core.CanonicalExport(context.Background(), repository, core.ApplicationRegistry())
}
