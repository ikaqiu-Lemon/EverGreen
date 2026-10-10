package migrate

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

type Asset struct {
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	Hash       string `json:"hash"`
}

// migrateAssets 只复制已存在的本地 blob；远端引用保持原值，不触发网络读取。
func (p *Prepared) migrateAssets(root string) error {
	assets := map[string]Asset{}
	for _, file := range p.Manifest.Files {
		if !strings.HasPrefix(file.Path, "assets/") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(hashBytes(raw), "sha256:") + path.Ext(file.Path)
		target := "assets/" + name
		p.Images[target] = raw
		assets[file.Path] = Asset{SourcePath: file.Path, TargetPath: target, Hash: hashBytes(raw)}
	}
	for index := range p.Manifest.Entities {
		entity := &p.Manifest.Entities[index]
		raw := p.Images[entity.Path]
		var node any
		if err := json.Unmarshal(raw, &node); err != nil {
			return err
		}
		referenced := map[string]Asset{}
		var walk func(any)
		walk = func(value any) {
			switch object := value.(type) {
			case map[string]any:
				nodeType, _ := object["Type"].(string)
				if nodeType == "NodeLinkDest" || nodeType == "NodeTextMark" {
					field := "Data"
					if nodeType == "NodeTextMark" {
						field = "TextMarkAHref"
					}
					destination, _ := object[field].(string)
					parsed, err := url.Parse(destination)
					if err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.Path != "" {
						candidate := path.Clean(path.Join(path.Dir(entity.Source), parsed.Path))
						if strings.HasPrefix(parsed.Path, "assets/") {
							candidate = path.Clean(parsed.Path)
						}
						if asset, exists := assets[candidate]; exists {
							parsed.Path = asset.TargetPath
							object[field] = parsed.String()
							referenced[asset.SourcePath] = asset
						} else if strings.Contains(parsed.Path, "assets/") {
							p.quarantine("EG_MIGRATION_ASSET_MISSING", entity.Source, fmt.Sprintf("asset reference %q has no local blob", destination))
						}
					}
				}
				for key, child := range object {
					if key != "Evergreen" {
						walk(child)
					}
				}
			case []any:
				for _, child := range object {
					walk(child)
				}
			}
		}
		walk(node)
		decoded, err := core.DecodeSY(raw, nil)
		if err != nil {
			return err
		}
		if len(referenced) > 0 {
			bindings := []Asset{}
			for _, file := range p.Manifest.Files {
				if asset, exists := referenced[file.Path]; exists {
					bindings = append(bindings, asset)
				}
			}
			if decoded.Envelope.Extension == nil {
				decoded.Envelope.Extension = core.RawObject{}
			}
			decoded.Envelope.Extension["org.evergreen.assets/v1"], _ = json.Marshal(bindings)
		}
		changed, err := json.Marshal(node)
		if err != nil {
			return err
		}
		changed, err = core.EncodeSY(changed, decoded.Envelope, nil)
		if err != nil {
			return err
		}
		// 资产路径重写属于导入表示变化，重新捕获 hash，保持原有 stale 事实而不制造新 stale。
		if decoded.Envelope.Review != nil && len(referenced) > 0 {
			changed, err = refreshImportedHashes(changed)
			if err != nil {
				return err
			}
		}
		p.Images[entity.Path] = changed
		entity.Hash, err = core.SemanticHash(changed)
		if err != nil {
			return err
		}
	}
	for _, file := range p.Manifest.Files {
		if asset, exists := assets[file.Path]; exists {
			p.Manifest.Assets = append(p.Manifest.Assets, asset)
		}
	}
	p.Manifest.Counts["assets"] = len(p.Manifest.Assets)
	return nil
}

func refreshImportedHashes(raw []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	hashes := map[core.LogicalID]string{}
	var walk func([]json.RawMessage, bool) ([]json.RawMessage, error)
	walk = func(nodes []json.RawMessage, candidates bool) ([]json.RawMessage, error) {
		for index, raw := range nodes {
			var node map[string]json.RawMessage
			if err := json.Unmarshal(raw, &node); err != nil {
				return nil, err
			}
			if childrenRaw, exists := node["Children"]; exists {
				var children []json.RawMessage
				_ = json.Unmarshal(childrenRaw, &children)
				children, err := walk(children, candidates)
				if err != nil {
					return nil, err
				}
				node["Children"], _ = json.Marshal(children)
			}
			if blockRaw, exists := node["Evergreen"]; exists {
				block, err := core.UnmarshalBlockEnvelope(blockRaw)
				if err != nil {
					return nil, err
				}
				if block.Segment != nil && !candidates {
					base, _ := json.Marshal(node)
					block.Segment.NormalizedHash, err = core.NormalizedSegmentHash(base)
					hashes[block.Segment.SegmentID] = block.Segment.NormalizedHash
				}
				if block.Candidate != nil && candidates {
					block.Candidate.PayloadHash, err = core.CandidateSubtreeHash(node["Children"])
					for id := range block.Candidate.RefHashes {
						block.Candidate.RefHashes[id] = hashes[id]
					}
				}
				if err != nil {
					return nil, err
				}
				node["Evergreen"], err = core.MarshalBlockEnvelope(block)
				if err != nil {
					return nil, err
				}
			}
			nodes[index], _ = json.Marshal(node)
		}
		return nodes, nil
	}
	var children []json.RawMessage
	_ = json.Unmarshal(root["Children"], &children)
	var err error
	children, err = walk(children, false)
	if err == nil {
		children, err = walk(children, true)
	}
	if err != nil {
		return nil, err
	}
	root["Children"], _ = json.Marshal(children)
	return json.Marshal(root)
}
