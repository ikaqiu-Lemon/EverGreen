package evergreencore

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	NativeFileAttributeView = "native-av"
	NativeFileAVCache       = "native-av-cache"
	NativeFileNotebookConf  = "notebook-conf"
	NativeFileAsset         = "asset"
)

// NativeAuxiliaryFile 绑定可信宿主登记的辅助文件类型与完整原始字节。
type NativeAuxiliaryFile struct {
	Kind              string `json:"kind"`
	BeforeContentHash string `json:"before_content_hash,omitempty"`
}

type NativeFileWrite struct {
	Path   string
	Kind   string
	Before []byte
	After  []byte
	Create bool
	Delete bool
}

type nativeFileReader interface {
	LoadPhysicalFile(context.Context, string) ([]byte, error)
}

func nativeFileRef(p string) LogicalID {
	return LogicalID("siyuan-file:" + p)
}

// PlanNativeFiles 将文档与辅助写集绑定到同一计划，通用 JSON 请求不能取得此授权。
func (s *AuthorityService) PlanNativeFiles(
	ctx context.Context, principal Principal, request PlanRequest, documents []NativeDocumentWrite, files []NativeFileWrite,
) (PlannedOperation, error) {
	if len(files) == 0 {
		return s.PlanNativeBatch(ctx, principal, request, documents)
	}
	if principal.Type != PrincipalUser || (request.Command != "" && request.Command != NativeEditCommand) {
		return PlannedOperation{}, validationError(CodeUnauthorized, "principal", "native files require a verified user")
	}
	reader, ok := s.host.(nativeFileReader)
	if !ok {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "host", "host cannot read auxiliary files")
	}
	operation := PlannedOperation{nativeEditor: true, Plan: ChangePlan{
		OperationID: request.OperationID, Protocol: request.Protocol, Command: NativeEditCommand, Principal: principal,
	}}
	var err error
	if len(request.Writes) > 0 || len(documents) > 0 {
		operation, err = s.PlanNativeBatch(ctx, principal, request, documents)
		if err != nil {
			return PlannedOperation{}, err
		}
	} else if len(request.Base) != 0 {
		return PlannedOperation{}, validationError(CodeInvalidPlan, "base", "base has no managed write")
	}
	for _, file := range files {
		ref := nativeFileRef(file.Path)
		image := AfterImage{
			LogicalID: ref, Path: file.Path, Create: file.Create, Delete: file.Delete,
			Bytes: append([]byte(nil), file.After...), ContentHash: contentHash(file.After),
			SemanticHash: contentHash(file.After), Auxiliary: &NativeAuxiliaryFile{Kind: file.Kind},
		}
		if !file.Create {
			image.Auxiliary.BeforeContentHash = contentHash(file.Before)
		}
		if file.Delete {
			image.SemanticHash = "absent"
		}
		if err = validateNativeFileImage(operation, image); err != nil {
			return PlannedOperation{}, err
		}
		current, loadErr := reader.LoadPhysicalFile(ctx, file.Path)
		base := BaseRef{LogicalID: ref}
		write := PlannedWrite{LogicalID: ref, After: image.SemanticHash, Delete: image.Delete}
		if image.Create {
			if len(file.Before) != 0 || !errors.Is(loadErr, fs.ErrNotExist) {
				return PlannedOperation{}, validationError(CodeBaseMismatch, file.Path, "auxiliary create requires an absent path")
			}
			base.Expected = "absent"
			if err = s.host.CheckCreate(ctx, image); err != nil {
				return PlannedOperation{}, err
			}
		} else {
			if loadErr != nil {
				return PlannedOperation{}, loadErr
			}
			if contentHash(current) != image.Auxiliary.BeforeContentHash {
				return PlannedOperation{}, validationError(CodeBaseMismatch, file.Path, "auxiliary base bytes changed")
			}
			base.SemanticHash = contentHash(current)
			write.Before = base.SemanticHash
		}
		if err = authorizeNativeFileChange(image, current); err != nil {
			return PlannedOperation{}, err
		}
		operation.Images = append(operation.Images, image)
		operation.Plan.Base = append(operation.Plan.Base, base)
		operation.Plan.Writes = append(operation.Plan.Writes, write)
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
		switch {
		case image.Auxiliary != nil:
			before[index], err = reader.LoadPhysicalFile(ctx, image.Path)
		case image.Native != nil:
			before[index], err = s.host.(nativePhysicalReader).LoadPhysicalDocument(ctx, image.Path)
		default:
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

func validateNativeFileImage(operation PlannedOperation, image AfterImage) error {
	if !operation.nativeEditor || operation.Plan.Command != NativeEditCommand || operation.Plan.Principal.Type != PrincipalUser {
		return validationError(CodeUnauthorized, "auxiliary", "auxiliary files require trusted native admission")
	}
	if image.Auxiliary == nil || image.Native != nil || image.LogicalID != nativeFileRef(image.Path) ||
		(image.Create && image.Auxiliary.BeforeContentHash != "") ||
		(!image.Create && !strings.HasPrefix(image.Auxiliary.BeforeContentHash, "sha256:")) {
		return validationError(CodeInvalidPlan, image.Path, "invalid auxiliary identity or base checksum")
	}
	kind, err := nativeAuxiliaryPathKind(image.Path)
	if err != nil || kind != image.Auxiliary.Kind {
		return validationError(CodeInvalidPlan, image.Path, "auxiliary path does not match its registered kind")
	}
	if image.Delete && (image.Create || len(image.Bytes) != 0) {
		return validationError(CodeInvalidPlan, image.Path, "auxiliary delete cannot create or contain after bytes")
	}
	return nil
}

func authorizeNativeFileChange(image AfterImage, before []byte) error {
	if image.Create {
		if len(before) != 0 {
			return validationError(CodeBaseMismatch, image.Path, "auxiliary create requires an absent before-image")
		}
	} else if contentHash(before) != image.Auxiliary.BeforeContentHash {
		return validationError(CodeBaseMismatch, image.Path, "auxiliary before-image checksum mismatch")
	}
	switch image.Auxiliary.Kind {
	case NativeFileAttributeView:
		objects := make([]RawObject, 0, 2)
		for _, raw := range [][]byte{before, image.Bytes} {
			if raw == nil {
				continue
			}
			object, err := decodeRawObject(raw)
			if err != nil {
				return err
			}
			var id string
			_ = json.Unmarshal(object["id"], &id)
			if id+".json" != path.Base(image.Path) || (object["provider"] != nil && string(object["provider"]) != "null") {
				return validationError(CodeUnauthorized, image.Path, "native auxiliary write cannot mutate a managed AV")
			}
			objects = append(objects, object)
		}
		if len(objects) == 2 {
			for _, object := range objects {
				for _, key := range []string{"name", "keyValues", "keyIDs", "views", "newItemTemplates",
					"defaultTemplateID", "cardCoverPositions"} {
					delete(object, key)
				}
			}
			if !semanticValuesEqual(objects[0], objects[1]) {
				return validationError(CodeUnauthorized, image.Path, "native AV edits must preserve identity, spec and unknown fields")
			}
		}
	case NativeFileNotebookConf:
		if image.Create || image.Delete {
			return validationError(CodeUnauthorized, image.Path, "native edit cannot create or remove notebook configuration")
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
			for _, key := range []string{"name", "icon", "sort"} {
				delete(object, key)
			}
		}
		if !semanticValuesEqual(a, b) {
			return validationError(CodeUnauthorized, image.Path, "native edit must preserve notebook encryption and unknown configuration")
		}
	}
	return nil
}

// ValidateTransactionFilePath 供存储和 Git 适配器校验文档及限定类型的辅助路径。
func ValidateTransactionFilePath(p string) error {
	if err := validateAuthorityPath(p); err == nil {
		return nil
	}
	_, err := nativeAuxiliaryPathKind(p)
	return err
}

func nativeAuxiliaryPathKind(p string) (string, error) {
	if !fs.ValidPath(p) || strings.Contains(p, "\\") || strings.TrimSpace(p) != p {
		return "", errors.New("transaction path must be normalized and relative")
	}
	parts := strings.Split(p, "/")
	if len(parts) >= 3 && nativeDocumentIDPattern.MatchString(parts[0]) &&
		strings.Join(parts[1:], "/") == ".siyuan/conf.json" {
		return NativeFileNotebookConf, nil
	}
	relative := p
	if len(parts) > 1 && nativeDocumentIDPattern.MatchString(parts[0]) {
		relative = strings.Join(parts[1:], "/")
	}
	if strings.HasPrefix(relative, "storage/av/") && strings.Count(relative, "/") == 2 {
		name := path.Base(relative)
		if nativeDocumentIDPattern.MatchString(strings.TrimSuffix(name, ".json")) && strings.HasSuffix(name, ".json") {
			return NativeFileAttributeView, nil
		}
		if name == "blocks.msgpack" || name == "relations.msgpack" {
			return NativeFileAVCache, nil
		}
	}
	for index, part := range parts {
		if part == "assets" && index < len(parts)-1 {
			for _, component := range parts {
				if strings.HasPrefix(component, ".") {
					return "", errors.New("asset path cannot contain private runtime components")
				}
			}
			if !strings.HasSuffix(strings.ToLower(p), ".sy") &&
				(index == 0 || nativeDocumentIDPattern.MatchString(parts[0])) {
				return NativeFileAsset, nil
			}
		}
	}
	return "", errors.New("transaction path is not a supported auxiliary file")
}

func imageSemanticHash(image AfterImage, raw []byte) (string, error) {
	if image.Auxiliary != nil {
		return contentHash(raw), nil
	}
	return SemanticHash(raw)
}
