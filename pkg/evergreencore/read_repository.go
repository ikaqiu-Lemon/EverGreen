package evergreencore

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// SYReadRepository 是一次请求的只读扫描快照；计划与提交必须重新校验权威。
type SYReadRepository struct {
	root      string
	locations map[LogicalID]LogicalLocation
}

func NewSYReadRepository(root string, registry *Registry) (*SYReadRepository, error) {
	index, err := ScanFS(os.DirFS(root), registry)
	if err != nil {
		return nil, err
	}
	return &SYReadRepository{root: root, locations: index.Locations}, nil
}

func (r *SYReadRepository) List(_ context.Context) ([]LogicalID, error) {
	ids := []LogicalID{}
	for id, location := range r.locations {
		if location.EntityType != "segment" && location.EntityType != "candidate" {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (r *SYReadRepository) Load(_ context.Context, id LogicalID) ([]byte, error) {
	location, exists := r.locations[id]
	if !exists {
		return nil, fs.ErrNotExist
	}
	return os.ReadFile(filepath.Join(r.root, filepath.FromSlash(location.Path)))
}
