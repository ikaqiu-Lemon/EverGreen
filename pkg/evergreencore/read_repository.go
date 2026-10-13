package evergreencore

import (
	"context"
	"io/fs"
	"sort"
	"sync"
)

// SYReadRepository 是一次请求的只读扫描快照；计划与提交必须重新校验权威。
type SYReadRepository struct {
	locations  map[LogicalID]LogicalLocation
	documents  map[LogicalID][]byte
	generation string
	registry   *Registry

	relationsOnce     sync.Once
	relationsProvider *ClaimsAVProvider
}

func NewSYReadRepository(root string, registry *Registry) (*SYReadRepository, error) {
	return NewSYReadRepositoryWithStorage(context.Background(), plainDocumentStorage{root}, registry)
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
	raw, exists := r.documents[id]
	if !exists {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), raw...), nil
}

func (r *SYReadRepository) RepositoryGeneration(context.Context) (string, bool, error) {
	return r.generation, true, nil
}

func (r *SYReadRepository) ProjectRelations(ctx context.Context, id LogicalID) ([]AVEdgeValue, error) {
	r.relationsOnce.Do(func() {
		r.relationsProvider = NewClaimsAVProvider(r, nil, r.registry)
	})
	return r.relationsProvider.Relations(ctx, id)
}
