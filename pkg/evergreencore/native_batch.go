package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

// NativePhysicalDocument 是宿主写集中的普通文档标识，不建立 Evergreen 逻辑实体。
type NativePhysicalDocument struct {
	DocumentID        string `json:"document_id"`
	BeforeContentHash string `json:"before_content_hash,omitempty"`
}

// NativeDocumentWrite 只能通过可信宿主入口规划，Before 是编辑前读取的完整原始明文。
type NativeDocumentWrite struct {
	Path   string
	Before []byte
	After  []byte
	Create bool
	Delete bool
}

type nativePhysicalReader interface {
	LoadPhysicalDocument(context.Context, string) ([]byte, error)
}

var nativeDocumentIDPattern = regexp.MustCompile(`^[0-9]{14}-[a-z0-9]{7}$`)

func nativePhysicalRef(documentID string) LogicalID {
	return LogicalID("siyuan-physical:" + documentID)
}

// PlanNativeBatch 把普通文档与受管正文纳入同一 journal；普通路径不能从通用 Plan 请求获得授权。
func (s *AuthorityService) PlanNativeBatch(
	ctx context.Context, principal Principal, request PlanRequest, native []NativeDocumentWrite,
) (PlannedOperation, error) {
	if len(native) == 0 {
		return s.PlanNativeEdit(ctx, principal, request)
	}
	if principal.Type != PrincipalUser || (request.Command != "" && request.Command != NativeEditCommand) {
		return PlannedOperation{}, validationError(CodeUnauthorized, "principal", "native batch requires a verified user")
	}
	reader, ok := s.host.(nativePhysicalReader)
	if !ok {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "host", "host cannot read physical documents")
	}
	request.Command = NativeEditCommand
	var operation PlannedOperation
	var err error
	if len(request.Writes) > 0 {
		operation, err = s.PlanNativeEdit(ctx, principal, request)
		if err != nil {
			return PlannedOperation{}, err
		}
	} else {
		if len(request.Base) != 0 {
			return PlannedOperation{}, validationError(CodeInvalidPlan, "base", "base has no managed write")
		}
		operation = PlannedOperation{nativeEditor: true, Plan: ChangePlan{
			OperationID: request.OperationID, Protocol: request.Protocol, Command: NativeEditCommand, Principal: principal,
		}}
	}
	for _, write := range native {
		if err = validateAuthorityPath(write.Path); err != nil {
			return PlannedOperation{}, validationError(CodeInvalidPlan, "path", err.Error())
		}
		raw := write.After
		if write.Delete {
			if write.Create || len(write.After) != 0 {
				return PlannedOperation{}, validationError(CodeInvalidPlan, "delete", "delete cannot create or contain after bytes")
			}
			raw = write.Before
		}
		document, err := DecodeSY(raw, s.registry)
		if err != nil {
			return PlannedOperation{}, err
		}
		root, err := decodeRawObject(raw)
		if err != nil {
			return PlannedOperation{}, err
		}
		var id string
		_ = json.Unmarshal(root["ID"], &id)
		ref := nativePhysicalRef(id)
		afterHash := "absent"
		if !write.Delete {
			afterHash, err = SemanticHash(write.After)
			if err != nil {
				return PlannedOperation{}, err
			}
		}
		image := AfterImage{
			LogicalID: ref, Path: write.Path, Bytes: append([]byte(nil), write.After...), Create: write.Create,
			SemanticHash: afterHash, ContentHash: contentHash(write.After),
			Native: &NativePhysicalDocument{DocumentID: id}, Delete: write.Delete,
		}
		base := BaseRef{LogicalID: ref}
		plannedWrite := PlannedWrite{LogicalID: ref, After: afterHash, Delete: write.Delete}
		current, loadErr := reader.LoadPhysicalDocument(ctx, write.Path)
		if write.Create {
			if len(write.Before) != 0 || !errors.Is(loadErr, fs.ErrNotExist) {
				return PlannedOperation{}, validationError(CodeBaseMismatch, write.Path, "native create requires an absent path")
			}
			base.Expected = "absent"
			if err = s.host.CheckCreate(ctx, image); err != nil {
				return PlannedOperation{}, err
			}
		} else {
			if loadErr != nil {
				return PlannedOperation{}, loadErr
			}
			image.Native.BeforeContentHash = contentHash(write.Before)
			if contentHash(current) != image.Native.BeforeContentHash {
				return PlannedOperation{}, validationError(CodeBaseMismatch, write.Path, "native base bytes changed")
			}
			base.SemanticHash, err = SemanticHash(current)
			if err != nil {
				return PlannedOperation{}, err
			}
			plannedWrite.Before = base.SemanticHash
		}
		if err = validateNativePhysicalImage(operation, image, document); err != nil {
			return PlannedOperation{}, err
		}
		if err = authorizeNativePhysicalChange(image, current); err != nil {
			return PlannedOperation{}, err
		}
		operation.Images = append(operation.Images, image)
		operation.Plan.Base = append(operation.Plan.Base, base)
		operation.Plan.Writes = append(operation.Plan.Writes, plannedWrite)
	}
	sort.Slice(operation.Images, func(i, j int) bool { return operation.Images[i].LogicalID < operation.Images[j].LogicalID })
	sort.Slice(operation.Plan.Base, func(i, j int) bool { return operation.Plan.Base[i].LogicalID < operation.Plan.Base[j].LogicalID })
	sort.Slice(operation.Plan.Writes, func(i, j int) bool { return operation.Plan.Writes[i].LogicalID < operation.Plan.Writes[j].LogicalID })
	operation.PlanHash = ComputePlanHash(operation.Plan, operation.Images)
	if err = ValidatePlannedOperation(operation, s.registry); err != nil {
		return PlannedOperation{}, err
	}
	before := make([][]byte, len(operation.Images))
	for index, image := range operation.Images {
		if image.Create {
			continue
		}
		if image.Native != nil {
			before[index], err = reader.LoadPhysicalDocument(ctx, image.Path)
		} else {
			before[index], err = s.host.Load(ctx, image.LogicalID)
		}
		if err != nil {
			return PlannedOperation{}, err
		}
	}
	if err = s.validateOperation(ctx, operation, before); err != nil {
		return PlannedOperation{}, err
	}
	return operation, nil
}

func validateNativePhysicalImage(operation PlannedOperation, image AfterImage, document *SYDocument) error {
	if !operation.nativeEditor || operation.Plan.Command != NativeEditCommand || operation.Plan.Principal.Type != PrincipalUser {
		return validationError(CodeUnauthorized, "native", "physical document writes require trusted native admission")
	}
	identity := image.Native
	if identity == nil || !nativeDocumentIDPattern.MatchString(identity.DocumentID) ||
		image.LogicalID != nativePhysicalRef(identity.DocumentID) ||
		path.Base(image.Path) != identity.DocumentID+".sy" ||
		(image.Create && identity.BeforeContentHash != "") ||
		(!image.Create && !strings.HasPrefix(identity.BeforeContentHash, "sha256:")) {
		return validationError(CodeInvalidPlan, "native", "physical identity, path or base checksum is invalid")
	}
	if image.Delete {
		if image.Create || len(image.Bytes) != 0 || image.SemanticHash != "absent" {
			return validationError(CodeInvalidPlan, "delete", "physical delete must have an absent after-image")
		}
		// 删除前的身份及受管元数据必须在写锁内使用完整 before-image 再次认证。
		return nil
	}
	root, err := decodeRawObject(image.Bytes)
	if err != nil {
		return err
	}
	var id string
	_ = json.Unmarshal(root["ID"], &id)
	if id != identity.DocumentID || document.ReadOnly || document.Envelope != nil {
		return validationError(CodeUnauthorized, "native", "ordinary write cannot contain managed document metadata")
	}
	node, err := parseReviewNode(image.Bytes)
	if err != nil {
		return err
	}
	if node.containsManagedEnvelope() {
		return validationError(CodeUnauthorized, "native", "ordinary write cannot contain managed block metadata")
	}
	return validateNativeNodeIdentity(node)
}

func authorizeNativePhysicalChange(image AfterImage, before []byte) error {
	if image.Create {
		if len(before) != 0 {
			return validationError(CodeBaseMismatch, image.Path, "native create requires an absent before-image")
		}
		return nil
	}
	if contentHash(before) != image.Native.BeforeContentHash {
		return validationError(CodeBaseMismatch, image.Path, "native before-image checksum mismatch")
	}
	previous, err := parseReviewNode(before)
	if err != nil {
		return err
	}
	if previous.containsManagedEnvelope() {
		return validationError(CodeUnauthorized, image.Path, "ordinary write cannot remove existing managed metadata")
	}
	if err = validateNativeNodeIdentity(previous); err != nil {
		return err
	}
	if image.Delete {
		root, err := decodeRawObject(before)
		if err != nil {
			return err
		}
		var id string
		_ = json.Unmarshal(root["ID"], &id)
		if id != image.Native.DocumentID {
			return validationError(CodeBaseMismatch, image.Path, "deleted document physical identity mismatch")
		}
		return nil
	}
	a, err := decodeRawObject(before)
	if err != nil {
		return err
	}
	b, err := decodeRawObject(image.Bytes)
	if err != nil {
		return err
	}
	for _, object := range []RawObject{a, b} {
		delete(object, "Children")
		delete(object, "Properties")
	}
	if !semanticValuesEqual(a, b) {
		return validationError(CodeUnauthorized, image.Path, "native edit must preserve physical identity and unknown document fields")
	}
	return nil
}

// AuthorityDocumentRef 供宿主在提交后重建普通及受管文档的派生数据。
type AuthorityDocumentRef struct {
	LogicalID  LogicalID
	DocumentID string
	Path       string
	Delete     bool
	Auxiliary  string
}

type documentIndexSink interface {
	InvalidateDocuments(context.Context, []AuthorityDocumentRef) error
}
