package txn

import (
	"context"
	"errors"
	"sync"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

type OfflineSYRuntimeOptions struct {
	Root       string
	Holder     string
	Principal  evergreencore.Principal
	Authorizer evergreencore.Authorizer
	Registry   *evergreencore.Registry
	IndexSink  evergreencore.IndexSink
}

type OfflineSYRuntime struct {
	lease     *evergreencore.WorkspaceWriterLease
	service   *evergreencore.AuthorityService
	principal evergreencore.Principal

	mu     sync.Mutex
	closed bool
}

func OpenOfflineSYRuntime(ctx context.Context, options OfflineSYRuntimeOptions) (*OfflineSYRuntime, error) {
	if options.Authorizer == nil {
		return nil, errors.New("offline Evergreen runtime requires an authorizer")
	}
	lease, err := evergreencore.AcquireOfflineLease(options.Root, options.Holder)
	if err != nil {
		return nil, err
	}
	committer, err := evergreencore.NewLocalGitCommitter(options.Root)
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	authority, err := evergreencore.NewFilesystemAuthority(evergreencore.FilesystemAuthorityOptions{
		Root: options.Root, Registry: options.Registry, Committer: committer, IndexSink: options.IndexSink,
	})
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	service := evergreencore.NewAuthorityService(authority, options.Authorizer, options.Registry)
	if _, err = service.Recover(ctx); err != nil {
		_ = lease.Release()
		return nil, err
	}
	return &OfflineSYRuntime{lease: lease, service: service, principal: options.Principal}, nil
}

func (r *OfflineSYRuntime) Plan(ctx context.Context, request evergreencore.PlanRequest) (evergreencore.PlannedOperation, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedOperation{}, err
	}
	return r.service.Plan(ctx, r.principal, request)
}

func (r *OfflineSYRuntime) Apply(ctx context.Context, request evergreencore.ApplyRequest) (evergreencore.OperationRecord, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.OperationRecord{}, err
	}
	return r.service.Apply(ctx, r.principal, request)
}

func (r *OfflineSYRuntime) Recover(ctx context.Context) ([]evergreencore.OperationRecord, error) {
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	return r.service.Recover(ctx)
}

func (r *OfflineSYRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.lease.Release()
}

func (r *OfflineSYRuntime) ensureOpen() error {
	if r == nil {
		return errors.New("offline Evergreen runtime is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("offline Evergreen runtime is closed")
	}
	return nil
}

func RecoverOfflineWriterLease(root string) error {
	return evergreencore.RecoverStaleLease(root)
}
