package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

// ColdImport 只发布完整暂存 workspace；中断或校验失败时目标保持不存在。
func ColdImport(source, target string, manifest Manifest) error {
	if manifest.Spec != ManifestSpec || manifest.TargetSpec != core.DocumentSpec ||
		manifest.Hash != manifestHash(manifest) {
		return fmt.Errorf("EG_MIGRATION_MANIFEST: invalid manifest or checksum")
	}
	prepared, err := Inventory(source)
	if err != nil {
		return err
	}
	if prepared.Manifest.Hash != manifest.Hash {
		return fmt.Errorf("EG_MIGRATION_SOURCE_CHANGED: source does not match the frozen manifest")
	}
	if len(manifest.Quarantine) != 0 {
		return fmt.Errorf("EG_MIGRATION_QUARANTINE: %d unresolved records block import", len(manifest.Quarantine))
	}
	sourceReal, err := filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(targetAbs))
	if err != nil {
		return err
	}
	targetReal := filepath.Join(parent, filepath.Base(targetAbs))
	if parent == sourceReal {
		return fmt.Errorf("EG_MIGRATION_TARGET: target parent cannot be the source vault")
	}
	if rel, err := filepath.Rel(sourceReal, targetReal); err != nil || rel == "." ||
		(rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return fmt.Errorf("EG_MIGRATION_TARGET: target must be outside the source vault")
	}
	if _, err = os.Lstat(targetReal); err == nil {
		return fmt.Errorf("EG_MIGRATION_TARGET: target must not exist")
	} else if !os.IsNotExist(err) {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".evergreen-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	lease, err := core.AcquireOfflineLease(stage, "migration")
	if err != nil {
		return err
	}
	defer lease.Release()
	for path, raw := range prepared.Images {
		output := filepath.Join(stage, filepath.FromSlash(path))
		if err = os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
			return err
		}
		if err = writeSynced(output, raw); err != nil {
			return err
		}
	}
	// notebook metadata 只定义 UI 容器，不存放实体或关系副本。
	box := filepath.Join(stage, "20000101000000-egvault")
	if err = os.MkdirAll(box, 0o700); err != nil {
		return err
	}
	if err = writeSynced(filepath.Join(box, ".siyuan"), []byte(`{"name":"Evergreen","closed":false}`)); err != nil {
		return err
	}
	if _, err = core.ScanFS(os.DirFS(stage), core.ApplicationRegistry()); err != nil {
		return err
	}
	if _, err = Parity(source, stage, manifest); err != nil {
		return err
	}
	if err = lease.Release(); err != nil {
		return err
	}
	if err = syncDirectories(stage); err != nil {
		return err
	}
	if err = os.Rename(stage, targetReal); err != nil {
		return err
	}
	return syncDirectory(parent)
}

type ParityReport struct {
	ManifestHash string                    `json:"manifest_hash"`
	Entities     int                       `json:"entities"`
	Locations    int                       `json:"logical_locations"`
	Changes      []core.SemanticChange     `json:"changes"`
	Projection   core.AVProjectionSnapshot `json:"projection"`
}

func Parity(source, target string, manifest Manifest) (ParityReport, error) {
	result := ParityReport{ManifestHash: manifest.Hash}
	prepared, err := Inventory(source)
	if err != nil {
		return result, err
	}
	if prepared.Manifest.Hash != manifest.Hash {
		return result, fmt.Errorf("EG_MIGRATION_SOURCE_CHANGED: manifest no longer matches source")
	}
	if len(manifest.Quarantine) != 0 {
		return result, fmt.Errorf("EG_MIGRATION_QUARANTINE: unresolved records")
	}
	for _, asset := range manifest.Assets {
		raw, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(asset.TargetPath)))
		if err != nil || hashBytes(raw) != asset.Hash {
			return result, fmt.Errorf("EG_MIGRATION_ASSET_PARITY: missing or altered asset %s", asset.TargetPath)
		}
	}
	expected := core.CanonicalArchive{Spec: core.CanonicalExportSpec, Entities: []core.CanonicalEntity{}}
	for _, entity := range manifest.Entities {
		expected.Entities = append(expected.Entities, core.CanonicalEntity{LogicalID: entity.LogicalID,
			Type: core.EntityType(entity.Type), Hash: entity.Hash, Document: prepared.Images[entity.Path]})
	}
	actual, err := Export(target)
	if err != nil {
		return result, err
	}
	result.Changes, err = core.DiffCanonical(expected, actual)
	if err != nil {
		return result, err
	}
	if len(result.Changes) != 0 {
		return result, fmt.Errorf("EG_MIGRATION_PARITY: %d semantic differences", len(result.Changes))
	}
	index, err := core.ScanFS(os.DirFS(target), core.ApplicationRegistry())
	if err != nil {
		return result, err
	}
	result.Entities, result.Locations = len(actual.Entities), len(index.Locations)
	repository, err := Repository(target)
	if err != nil {
		return result, err
	}
	provider := core.NewClaimsAVProvider(repository, nil, core.ApplicationRegistry())
	result.Projection, err = provider.Rebuild(context.Background())
	if err != nil {
		return result, err
	}
	provider.DropDerived()
	rebuilt, err := provider.Rebuild(context.Background())
	if err != nil {
		return result, err
	}
	beforeJSON, _ := json.Marshal(result.Projection)
	afterJSON, _ := json.Marshal(rebuilt)
	if string(beforeJSON) != string(afterJSON) {
		return result, fmt.Errorf("EG_MIGRATION_REBUILD: derived projection differs after deletion")
	}
	return result, nil
}

func writeSynced(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectories(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDirectory(path)
		}
		return nil
	})
}
