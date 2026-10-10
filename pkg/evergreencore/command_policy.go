package evergreencore

import (
	"context"
	"strings"
)

// CommandPolicy 在 UI、在线与离线 host 间共享命令授权；字段能力由 Plan/Apply 继续校验。
type CommandPolicy struct{}

func (CommandPolicy) Authorize(_ context.Context, principal Principal, command string, _ []LogicalID) error {
	if strings.TrimSpace(principal.ID) == "" {
		return validationError(CodeUnauthorized, "principal.id", "authenticated principal ID is required")
	}
	if principal.Type != PrincipalUser && principal.Type != PrincipalAgent {
		return validationError(CodeUnauthorized, "principal.type", "unsupported principal type")
	}
	if principal.Type == PrincipalAgent &&
		(strings.TrimSpace(principal.AuthSource) == "" || strings.TrimSpace(principal.RequestReason) == "") {
		return validationError(CodeUnauthorized, "principal", "agent requires authorization source and request reason")
	}
	switch command {
	case "claim.update", "claim.edge.update", "note.review.update", "note.review.materialize":
		return nil
	default:
		return validationError(CodeUnauthorized, "command", "principal is not authorized for this command")
	}
}

// ArchiveRepository 用于传输返回值的只读查询；不接受任何写入，也不持久化第二份权威。
type ArchiveRepository struct {
	archive CanonicalArchive
}

func NewArchiveRepository(archive CanonicalArchive) (*ArchiveRepository, error) {
	if archive.Spec != CanonicalExportSpec {
		return nil, validationError(CodeProtocolUnsupported, "spec", "unsupported archive protocol")
	}
	if _, err := archiveEntities(archive); err != nil {
		return nil, err
	}
	return &ArchiveRepository{archive: archive}, nil
}

func (r *ArchiveRepository) List(_ context.Context) ([]LogicalID, error) {
	result := make([]LogicalID, 0, len(r.archive.Entities))
	for _, entity := range r.archive.Entities {
		result = append(result, entity.LogicalID)
	}
	return result, nil
}

func (r *ArchiveRepository) Load(_ context.Context, id LogicalID) ([]byte, error) {
	for _, entity := range r.archive.Entities {
		if entity.LogicalID == id {
			return append([]byte(nil), entity.Document...), nil
		}
	}
	return nil, validationError(CodeInvalidEnvelope, "logical_id", "entity not found")
}
