package evergreencore

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LogicalLocation struct {
	LogicalID  LogicalID `json:"logical_id"`
	EntityType string    `json:"entity_type"`
	DocumentID string    `json:"document_id"`
	BlockID    string    `json:"block_id,omitempty"`
	Path       string    `json:"path"`
}

type LogicalIndex struct {
	Locations map[LogicalID]LogicalLocation
}

func ScanFS(filesystem fs.FS, registry *Registry) (*LogicalIndex, error) {
	index := &LogicalIndex{Locations: map[LogicalID]LogicalLocation{}}
	err := fs.WalkDir(filesystem, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".sy") {
			return nil
		}
		raw, err := fs.ReadFile(filesystem, path)
		if err != nil {
			return err
		}
		document, err := DecodeSY(raw, registry)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if document.ReadOnly && (document.Envelope == nil ||
			!onlyKindUnavailable(document.Diagnostics)) {
			return fmt.Errorf("%s: %w", path, &DiagnosticError{Diagnostics: document.Diagnostics})
		}
		if document.Envelope == nil {
			return nil
		}
		root, err := decodeRawObject(raw)
		if err != nil {
			return err
		}
		var documentID string
		if err = decodeRequired(root, "ID", &documentID); err != nil {
			return err
		}
		if err = addLogicalLocation(index, LogicalLocation{
			LogicalID:  document.Envelope.Entity.LogicalID,
			EntityType: string(document.Envelope.Entity.EntityType),
			DocumentID: documentID,
			Path:       filepath.ToSlash(path),
		}); err != nil {
			return err
		}
		if childrenRaw, ok := root["Children"]; ok {
			var children []json.RawMessage
			if err = json.Unmarshal(childrenRaw, &children); err != nil {
				return err
			}
			if err = scanBlocks(index, children, documentID, filepath.ToSlash(path), registry); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func onlyKindUnavailable(diagnostics []Diagnostic) bool {
	if len(diagnostics) == 0 {
		return false
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != CodeClaimKindUnavailable {
			return false
		}
	}
	return true
}

func addLogicalLocation(index *LogicalIndex, location LogicalLocation) error {
	if existing, ok := index.Locations[location.LogicalID]; ok {
		return validationError(
			CodeDuplicateID,
			"logical_id",
			fmt.Sprintf("logical ID %q is present at both %s and %s", location.LogicalID, existing.Path, location.Path),
		)
	}
	index.Locations[location.LogicalID] = location
	return nil
}

func scanBlocks(
	index *LogicalIndex,
	nodes []json.RawMessage,
	documentID string,
	path string,
	registry *Registry,
) error {
	for _, raw := range nodes {
		node, err := decodeRawObject(raw)
		if err != nil {
			return err
		}
		var blockID string
		if value, ok := node["ID"]; ok {
			if err = json.Unmarshal(value, &blockID); err != nil {
				return err
			}
		}
		if envelopeRaw, ok := node[evergreenJSONKey]; ok {
			envelope, decodeErr := UnmarshalBlockEnvelope(envelopeRaw)
			if decodeErr != nil {
				return decodeErr
			}
			if validateErr := ValidateBlock(envelope, registry); validateErr != nil &&
				!HasDiagnostic(validateErr, CodeClaimKindUnavailable) {
				return validateErr
			}
			switch envelope.Role {
			case "note_segment", "agent_annotation":
				err = addLogicalLocation(index, LogicalLocation{
					LogicalID:  envelope.Segment.SegmentID,
					EntityType: "segment",
					DocumentID: documentID,
					BlockID:    blockID,
					Path:       path,
				})
			case "candidate":
				err = addLogicalLocation(index, LogicalLocation{
					LogicalID:  envelope.Candidate.CandidateID,
					EntityType: "candidate",
					DocumentID: documentID,
					BlockID:    blockID,
					Path:       path,
				})
			}
			if err != nil {
				return err
			}
		}
		if childrenRaw, ok := node["Children"]; ok {
			var children []json.RawMessage
			if err = json.Unmarshal(childrenRaw, &children); err != nil {
				return err
			}
			if err = scanBlocks(index, children, documentID, path, registry); err != nil {
				return err
			}
		}
	}
	return nil
}

type PrincipalType string

const (
	PrincipalUser  PrincipalType = "user"
	PrincipalAgent PrincipalType = "agent"
)

type Principal struct {
	Type          PrincipalType `json:"type"`
	ID            string        `json:"id"`
	AuthSource    string        `json:"auth_source,omitempty"`
	RequestReason string        `json:"request_reason,omitempty"`
}

type BaseRef struct {
	LogicalID    LogicalID `json:"logical_id"`
	SemanticHash string    `json:"semantic_hash,omitempty"`
	Expected     string    `json:"expected,omitempty"`
}

type PlannedWrite struct {
	LogicalID LogicalID `json:"logical_id"`
	Before    string    `json:"before,omitempty"`
	After     string    `json:"after"`
}

type ChangePlan struct {
	OperationID string         `json:"operation_id"`
	Protocol    string         `json:"protocol"`
	Principal   Principal      `json:"principal"`
	Command     string         `json:"command"`
	Base        []BaseRef      `json:"base"`
	Writes      []PlannedWrite `json:"writes"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
}

func ValidateChangePlan(plan ChangePlan) error {
	if strings.TrimSpace(plan.OperationID) == "" {
		return validationError(CodeInvalidPlan, "operation_id", "operation ID is required")
	}
	if plan.Protocol != ChangePlanProtocol {
		return validationError(CodeInvalidPlan, "protocol", fmt.Sprintf(
			"protocol %q is not supported; expected %q", plan.Protocol, ChangePlanProtocol))
	}
	if plan.Principal.Type != PrincipalUser && plan.Principal.Type != PrincipalAgent {
		return validationError(CodeInvalidPlan, "principal.type", fmt.Sprintf(
			"principal type %q is not supported", plan.Principal.Type))
	}
	if strings.TrimSpace(plan.Principal.ID) == "" {
		return validationError(CodeInvalidPlan, "principal.id", "principal ID is required")
	}
	if strings.TrimSpace(plan.Command) == "" {
		return validationError(CodeInvalidPlan, "command", "command is required")
	}
	baseByID := map[LogicalID]BaseRef{}
	for index, base := range plan.Base {
		if base.LogicalID == "" {
			return validationError(CodeInvalidPlan, fmt.Sprintf("base[%d].logical_id", index), "logical ID is required")
		}
		if _, exists := baseByID[base.LogicalID]; exists {
			return validationError(CodeDuplicateID, fmt.Sprintf("base[%d].logical_id", index), "duplicate base logical ID")
		}
		if base.Expected == "" && !strings.HasPrefix(base.SemanticHash, "sha256:") {
			return validationError(CodeInvalidPlan, fmt.Sprintf("base[%d].semantic_hash", index), "sha256 semantic hash is required")
		}
		if base.Expected != "" && base.Expected != "absent" {
			return validationError(CodeInvalidPlan, fmt.Sprintf("base[%d].expected", index), "expected must be absent")
		}
		baseByID[base.LogicalID] = base
	}
	seenWrites := map[LogicalID]struct{}{}
	for index, write := range plan.Writes {
		path := fmt.Sprintf("writes[%d]", index)
		if write.LogicalID == "" {
			return validationError(CodeInvalidPlan, path+".logical_id", "logical ID is required")
		}
		if _, exists := seenWrites[write.LogicalID]; exists {
			return validationError(CodeDuplicateID, path+".logical_id", "duplicate planned write")
		}
		seenWrites[write.LogicalID] = struct{}{}
		base, exists := baseByID[write.LogicalID]
		if !exists {
			return validationError(CodeBaseMismatch, path+".before", "planned write has no base entry")
		}
		switch {
		case base.Expected == "absent" && write.Before != "":
			return validationError(CodeBaseMismatch, path+".before", "create write must not have a before hash")
		case base.Expected != "absent" && base.SemanticHash != write.Before:
			return validationError(CodeBaseMismatch, path+".before", fmt.Sprintf(
				"write before hash %q does not match base %q", write.Before, base.SemanticHash))
		}
		if !strings.HasPrefix(write.After, "sha256:") {
			return validationError(CodeInvalidPlan, path+".after", "sha256 after hash is required")
		}
	}
	return nil
}

type Repository interface {
	Load(context.Context, LogicalID) ([]byte, error)
	List(context.Context) ([]LogicalID, error)
}

type TransactionHost interface {
	Apply(context.Context, ChangePlan) error
}

type CommitSink interface {
	Commit(context.Context, ChangePlan) (string, error)
}

type IndexSink interface {
	Invalidate(context.Context, []LogicalID) error
}

type Authorizer interface {
	Authorize(context.Context, Principal, string, []LogicalID) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewOperationID() string
	NewLogicalID(EntityType) LogicalID
	NewEdgeID() EdgeID
}

func SortedLogicalIDs(index *LogicalIndex) []LogicalID {
	if index == nil {
		return nil
	}
	ids := make([]LogicalID, 0, len(index.Locations))
	for id := range index.Locations {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
