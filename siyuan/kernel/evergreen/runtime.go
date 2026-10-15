// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package evergreen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/evergreen/writegate"
)

type RuntimeOptions struct {
	Root           string
	Holder         string
	Registry       *evergreencore.Registry
	Storage        evergreencore.DocumentStorage
	Authorizer     evergreencore.Authorizer
	Committer      evergreencore.OperationCommitter
	IndexSink      evergreencore.IndexSink
	Faults         evergreencore.FaultInjector
	InspectDerived func(context.Context, []evergreencore.AuthorityDocumentRef) (evergreencore.DerivedProjectionStatus, error)
	RebuildDerived func(context.Context, []evergreencore.AuthorityDocumentRef) error
}

var applicationKinds = evergreencore.ApplicationRegistry()

func installedKindRegistry() *evergreencore.Registry {
	return applicationKinds
}

type Runtime struct {
	root           string
	lease          *evergreencore.WorkspaceWriterLease
	service        *evergreencore.AuthorityService
	authority      *evergreencore.FilesystemAuthority
	storage        evergreencore.DocumentStorage
	avProvider     *evergreencore.ClaimsAVProvider
	registry       *evergreencore.Registry
	writeGate      *sync.Mutex
	inspectDerived func(context.Context, []evergreencore.AuthorityDocumentRef) (evergreencore.DerivedProjectionStatus, error)
	rebuildDerived func(context.Context, []evergreencore.AuthorityDocumentRef) error

	closeMu sync.Mutex
	closed  bool
}

func NewRuntime(ctx context.Context, options RuntimeOptions) (*Runtime, error) {
	if options.Registry == nil {
		options.Registry = installedKindRegistry()
	}
	if options.Authorizer == nil {
		options.Authorizer = CommandPolicy{}
	}
	if options.Committer == nil {
		options.Committer = lazyGitCommitter{root: options.Root}
	}
	if options.IndexSink == nil {
		options.IndexSink = IndexSink{Root: options.Root}
	}
	holder := options.Holder
	if holder == "" {
		holder = "siyuan-kernel"
	}
	gate, err := writegate.ForWorkspace(options.Root)
	if err != nil {
		return nil, err
	}
	gate.Lock()
	defer gate.Unlock()
	lease, err := evergreencore.AcquireKernelLease(options.Root, holder)
	if err != nil {
		return nil, err
	}
	authority, err := evergreencore.NewFilesystemAuthority(evergreencore.FilesystemAuthorityOptions{
		Root: options.Root, Registry: options.Registry, Committer: options.Committer,
		Storage: options.Storage, IndexSink: options.IndexSink, Faults: options.Faults,
	})
	if err != nil {
		_ = lease.Release()
		return nil, err
	}
	service := evergreencore.NewAuthorityService(authority, options.Authorizer, options.Registry)
	runtime := &Runtime{
		root: options.Root, lease: lease, service: service, registry: options.Registry,
		authority: authority, storage: options.Storage,
		avProvider: evergreencore.NewClaimsAVProvider(authority, service, options.Registry),
		writeGate:  gate, inspectDerived: options.InspectDerived, rebuildDerived: options.RebuildDerived,
	}
	_, recoverErr := runtime.service.Recover(ctx)
	if recoverErr != nil {
		_ = lease.Release()
		return nil, recoverErr
	}
	return runtime, nil
}

func (r *Runtime) Capabilities() evergreencore.Capabilities {
	return r.service.Capabilities()
}

func (r *Runtime) Schemas() evergreencore.SchemaCatalog {
	return r.service.Schemas()
}

func (r *Runtime) CanonicalExport(ctx context.Context) (evergreencore.CanonicalArchive, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.CanonicalArchive{}, err
	}
	var authority *evergreencore.SYReadRepository
	var err error
	if r.storage != nil {
		authority, err = evergreencore.NewSYReadRepositoryWithStorage(ctx, r.storage, r.registry)
	} else {
		authority, err = evergreencore.NewSYReadRepository(r.root, r.registry)
	}
	if err != nil {
		return evergreencore.CanonicalArchive{}, err
	}
	return evergreencore.CanonicalExport(ctx, authority, r.registry)
}

func (r *Runtime) InspectClaim(ctx context.Context, id evergreencore.LogicalID) (evergreencore.ClaimSnapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.ClaimSnapshot{}, err
	}
	return r.service.InspectClaim(ctx, id)
}

func (r *Runtime) InspectClaimByDocumentID(ctx context.Context, documentID string) (evergreencore.ClaimSnapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.ClaimSnapshot{}, err
	}
	ids, err := r.authority.List(ctx)
	if err != nil {
		return evergreencore.ClaimSnapshot{}, err
	}
	for _, id := range ids {
		location, err := r.authority.Resolve(ctx, id)
		if err != nil {
			return evergreencore.ClaimSnapshot{}, err
		}
		if location.DocumentID == documentID && location.EntityType == string(evergreencore.EntityClaim) {
			return r.service.InspectClaim(ctx, id)
		}
	}
	return evergreencore.ClaimSnapshot{}, fmt.Errorf("managed Claim document was not found")
}

func (r *Runtime) PlanClaimEdit(ctx context.Context, principal evergreencore.Principal, request evergreencore.ClaimEditRequest) (evergreencore.PlannedOperation, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedOperation{}, err
	}
	return r.service.PlanClaimEdit(ctx, principal, request)
}

func (r *Runtime) PlanClaimCreate(ctx context.Context, principal evergreencore.Principal, request evergreencore.ClaimCreateRequest) (evergreencore.PlannedOperation, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedOperation{}, err
	}
	return r.service.PlanClaimCreate(ctx, principal, request)
}

func (r *Runtime) PlanSourceCapture(
	ctx context.Context,
	principal evergreencore.Principal,
	request evergreencore.SourceCaptureRequest,
) (evergreencore.PlannedSourceCapture, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedSourceCapture{}, err
	}
	return r.service.PlanSourceCapture(ctx, principal, request)
}

func (r *Runtime) PlanNoteCreate(
	ctx context.Context,
	principal evergreencore.Principal,
	request evergreencore.NoteCreateRequest,
) (evergreencore.PlannedNoteCreate, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedNoteCreate{}, err
	}
	return r.service.PlanNoteCreate(ctx, principal, request)
}

func (r *Runtime) BulkCapabilities(ctx context.Context, principal evergreencore.Principal, rows []evergreencore.AVRowRef) (evergreencore.BulkClaimCapabilities, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.BulkClaimCapabilities{}, err
	}
	return r.service.BulkCapabilities(ctx, principal, rows)
}

func (r *Runtime) PlanBulkClaims(ctx context.Context, principal evergreencore.Principal, request evergreencore.BulkClaimRequest) (evergreencore.PlannedOperation, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedOperation{}, err
	}
	return r.service.PlanBulkClaims(ctx, principal, request)
}

func (r *Runtime) DescribeAV(ctx context.Context, viewID string) (evergreencore.AVViewSchema, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.AVViewSchema{}, err
	}
	return r.avProvider.Describe(ctx, viewID)
}

func (r *Runtime) QueryAV(
	ctx context.Context,
	viewID string,
	query evergreencore.AVQuery,
) (evergreencore.AVPage, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return evergreencore.AVPage{}, err
	}
	return r.avProvider.Query(ctx, viewID, query)
}

func (r *Runtime) GetAVCell(
	ctx context.Context,
	row evergreencore.AVRowRef,
	column evergreencore.AVColumnBinding,
) (evergreencore.AVCellValue, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return evergreencore.AVCellValue{}, err
	}
	return r.avProvider.GetCell(ctx, row, column)
}

func (r *Runtime) PlanAVPatch(
	ctx context.Context,
	principal evergreencore.Principal,
	patch evergreencore.AVPatch,
) (evergreencore.PlannedOperation, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedOperation{}, err
	}
	return r.avProvider.PlanPatch(ctx, principal, patch)
}

func (r *Runtime) RebuildAV(ctx context.Context) (evergreencore.AVProjectionSnapshot, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return evergreencore.AVProjectionSnapshot{}, err
	}
	return r.avProvider.Rebuild(ctx)
}

func (r *Runtime) InspectReview(
	ctx context.Context,
	noteID evergreencore.LogicalID,
) (evergreencore.ReviewSnapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.ReviewSnapshot{}, err
	}
	return r.service.InspectReview(ctx, noteID)
}

func (r *Runtime) InspectReviewByDocumentID(
	ctx context.Context,
	documentID string,
) (evergreencore.ReviewSnapshot, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.ReviewSnapshot{}, err
	}
	return r.service.InspectReviewByDocumentID(ctx, documentID)
}

func (r *Runtime) PlanReview(
	ctx context.Context,
	principal evergreencore.Principal,
	request evergreencore.ReviewPlanRequest,
) (evergreencore.PlannedReview, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedReview{}, err
	}
	return r.service.PlanReview(ctx, principal, request)
}

func (r *Runtime) PlanReviewEdit(
	ctx context.Context,
	principal evergreencore.Principal,
	request evergreencore.ReviewEditPlanRequest,
) (evergreencore.PlannedReview, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedReview{}, err
	}
	return r.service.PlanReviewEdit(ctx, principal, request)
}

func (r *Runtime) PlanReviewMaterialization(
	ctx context.Context,
	principal evergreencore.Principal,
	request evergreencore.ReviewMaterializeRequest,
) (evergreencore.PlannedMaterialization, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedMaterialization{}, err
	}
	return r.service.PlanReviewMaterialization(ctx, principal, request)
}

func (r *Runtime) DropAVDerived() {
	if r != nil && r.avProvider != nil {
		r.avProvider.DropDerived()
	}
}

func (r *Runtime) Plan(ctx context.Context, principal evergreencore.Principal, request evergreencore.PlanRequest) (evergreencore.PlannedOperation, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.PlannedOperation{}, err
	}
	return r.service.Plan(ctx, principal, request)
}

// ApplyNativeBatch 在同一写门禁内规划和提交，防止专用 API 插入原生写集的 CAS 验证之间。
// model 调用方须已持有 flushLock，索引回调不得再次获取该锁。
func (r *Runtime) ApplyNativeBatch(
	ctx context.Context, principal evergreencore.Principal, request evergreencore.PlanRequest,
	native []evergreencore.NativeDocumentWrite,
) (evergreencore.OperationRecord, error) {
	return r.ApplyNativeFiles(ctx, principal, request, native, nil)
}

// ApplyNativeFiles 将文档删除和辅助文件与受管正文绑定到同一次规划、CAS 和提交。
func (r *Runtime) ApplyNativeFiles(
	ctx context.Context, principal evergreencore.Principal, request evergreencore.PlanRequest,
	native []evergreencore.NativeDocumentWrite, files []evergreencore.NativeFileWrite,
) (evergreencore.OperationRecord, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return evergreencore.OperationRecord{}, err
	}
	planned, err := r.service.PlanNativeFiles(ctx, principal, request, native, files)
	if err != nil {
		return evergreencore.OperationRecord{}, err
	}
	type nativeExpected struct {
		after  []byte
		delete bool
	}
	expected := map[string]nativeExpected{}
	for _, write := range native {
		expected[write.Path] = nativeExpected{after: write.After, delete: write.Delete}
	}
	for _, write := range files {
		expected[write.Path] = nativeExpected{after: write.After, delete: write.Delete}
	}
	unmatched := make(map[string]bool, len(expected))
	for path := range expected {
		unmatched[path] = true
	}
	for _, image := range planned.Images {
		if image.Native == nil && image.Auxiliary == nil {
			continue
		}
		item, exists := expected[image.Path]
		if !exists || item.delete != image.Delete || !bytes.Equal(item.after, image.Bytes) {
			return evergreencore.OperationRecord{}, fmt.Errorf("native plan after-image changed for %s", image.Path)
		}
		delete(unmatched, image.Path)
	}
	if len(unmatched) != 0 {
		return evergreencore.OperationRecord{}, errors.New("native plan omitted an authenticated after-image")
	}
	record, err := r.service.Apply(ctx, principal, evergreencore.ApplyRequest{Operation: planned})
	// 部分落盘和派生刷新失败也必须让内存投影失效，后续依据 journal 恢复。
	r.avProvider.DropDerived()
	return record, err
}

func (r *Runtime) Apply(ctx context.Context, principal evergreencore.Principal, request evergreencore.ApplyRequest) (evergreencore.OperationRecord, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return evergreencore.OperationRecord{}, err
	}
	record, err := r.service.Apply(ctx, principal, request)
	if err == nil {
		r.avProvider.DropDerived()
	}
	return record, err
}

func (r *Runtime) Operation(ctx context.Context, operationID string) (evergreencore.OperationRecord, error) {
	if err := r.ensureOpen(); err != nil {
		return evergreencore.OperationRecord{}, err
	}
	return r.service.Operation(ctx, operationID)
}

func (r *Runtime) Recover(ctx context.Context) ([]evergreencore.OperationRecord, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return nil, err
	}
	records, err := r.service.Recover(ctx)
	if err == nil && len(records) > 0 {
		r.avProvider.DropDerived()
	}
	return records, err
}

func (r *Runtime) Doctor(ctx context.Context, rebuildIndex bool) (evergreencore.WorkspaceDoctorReport, error) {
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	if err := r.ensureOpen(); err != nil {
		return evergreencore.WorkspaceDoctorReport{}, err
	}
	if err := r.authority.CheckBackgroundWrite(ctx); err != nil {
		return evergreencore.WorkspaceDoctorReport{}, err
	}
	repository, err := r.readRepository(ctx)
	if err != nil {
		return evergreencore.WorkspaceDoctorReport{}, err
	}
	before, err := evergreencore.InspectWorkspaceDoctor(ctx, repository, r.registry)
	if err != nil {
		return evergreencore.WorkspaceDoctorReport{}, err
	}
	report := evergreencore.WorkspaceDoctorReport{
		Spec: evergreencore.WorkspaceDoctorSpec, RebuildIndex: rebuildIndex,
		Parity: true, Before: before, After: before,
	}
	refs := make([]evergreencore.AuthorityDocumentRef, 0, len(before.Entities))
	for _, entity := range before.Entities {
		location, resolveErr := r.authority.Resolve(ctx, entity.LogicalID)
		if resolveErr != nil {
			return report, resolveErr
		}
		refs = append(refs, evergreencore.AuthorityDocumentRef{
			LogicalID: entity.LogicalID, DocumentID: location.DocumentID, Path: location.Path,
		})
	}
	if r.inspectDerived != nil {
		report.DerivedBefore, err = r.inspectDerived(ctx, refs)
		if err != nil {
			return report, err
		}
		report.DerivedAfter = report.DerivedBefore
	}
	if !rebuildIndex {
		if report.DerivedBefore.Available && !report.DerivedBefore.Healthy {
			return report, derivedStaleError("host projection differs from the authority-derived document and block sets")
		}
		return report, nil
	}
	if r.rebuildDerived == nil || r.inspectDerived == nil {
		return report, derivedStaleError("the SiYuan host did not provide derived-index inspect and rebuild ports")
	}
	if err = r.rebuildDerived(ctx, refs); err != nil {
		return report, err
	}
	report.DerivedAfter, err = r.inspectDerived(ctx, refs)
	if err != nil {
		return report, err
	}
	if !report.DerivedAfter.Healthy {
		return report, derivedStaleError("host projection remains inconsistent after rebuild")
	}
	r.avProvider.DropDerived()
	repository, err = r.readRepository(ctx)
	if err != nil {
		return report, err
	}
	report.After, err = evergreencore.InspectWorkspaceDoctor(ctx, repository, r.registry)
	if err != nil {
		return report, err
	}
	report.Parity = report.Before.Digest == report.After.Digest
	if !report.Parity {
		return report, &evergreencore.DiagnosticError{Diagnostics: []evergreencore.Diagnostic{{
			Code: evergreencore.CodeDerivedStale, Level: "error", Path: "derived",
			Message: "derived rebuild changed the authority-derived entity, edge, review, or query sets",
		}}}
	}
	return report, nil
}

func derivedStaleError(message string) error {
	return &evergreencore.DiagnosticError{Diagnostics: []evergreencore.Diagnostic{{
		Code: evergreencore.CodeDerivedStale, Level: "error", Path: "derived", Message: message,
	}}}
}

func (r *Runtime) readRepository(ctx context.Context) (*evergreencore.SYReadRepository, error) {
	if r.storage != nil {
		return evergreencore.NewSYReadRepositoryWithStorage(ctx, r.storage, r.registry)
	}
	return evergreencore.NewSYReadRepository(r.root, r.registry)
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.writeGate.Lock()
	defer r.writeGate.Unlock()
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.lease.Release()
}

func (r *Runtime) ensureOpen() error {
	if r == nil {
		return errors.New("Evergreen kernel runtime is nil")
	}
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closed {
		return errors.New("Evergreen kernel runtime is closed")
	}
	return nil
}

// CommandPolicy is the kernel-side capability matrix. Clients never submit a
// capability list; the authenticated principal and command determine access.
type CommandPolicy = evergreencore.CommandPolicy

type lazyGitCommitter struct {
	root string
}

func (c lazyGitCommitter) Head(ctx context.Context) (string, error) {
	committer, err := evergreencore.NewLocalGitCommitter(c.root)
	if err != nil {
		return "", nil
	}
	return committer.Head(ctx)
}

func (c lazyGitCommitter) Commit(ctx context.Context, request evergreencore.CommitRequest) (string, error) {
	committer, err := evergreencore.NewLocalGitCommitter(c.root)
	if err != nil {
		return "", err
	}
	return committer.Commit(ctx, request)
}

var defaultRuntimeState struct {
	sync.Mutex
	root    string
	runtime *Runtime
}

func DefaultRuntime(ctx context.Context, options RuntimeOptions) (*Runtime, error) {
	defaultRuntimeState.Lock()
	defer defaultRuntimeState.Unlock()
	if defaultRuntimeState.runtime != nil {
		if defaultRuntimeState.root != options.Root {
			return nil, fmt.Errorf("Evergreen runtime already initialized for %s", defaultRuntimeState.root)
		}
		return defaultRuntimeState.runtime, nil
	}
	runtime, err := NewRuntime(ctx, options)
	if err != nil {
		return nil, err
	}
	defaultRuntimeState.root = options.Root
	defaultRuntimeState.runtime = runtime
	return runtime, nil
}

func CloseDefaultRuntime() error {
	defaultRuntimeState.Lock()
	defer defaultRuntimeState.Unlock()
	if defaultRuntimeState.runtime == nil {
		return nil
	}
	err := defaultRuntimeState.runtime.Close()
	defaultRuntimeState.root = ""
	defaultRuntimeState.runtime = nil
	return err
}
