package evergreencore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type LogicalLocation struct {
	LogicalID   LogicalID    `json:"logical_id"`
	EntityType  string       `json:"entity_type"`
	DocumentID  string       `json:"document_id"`
	BlockID     string       `json:"block_id,omitempty"`
	Path        string       `json:"path"`
	ReadOnly    bool         `json:"read_only,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type LogicalIndex struct {
	Locations map[LogicalID]LogicalLocation

	documents  map[LogicalID][]byte
	generation string
}

func ScanFS(filesystem fs.FS, registry *Registry) (*LogicalIndex, error) {
	return scanFS(filesystem, registry, false)
}

type scanDocumentJob struct {
	position int
	path     string
}

type scanDocumentResult struct {
	documentID LogicalID
	locations  []LogicalLocation
	raw        []byte
	err        error
}

func scanFS(filesystem fs.FS, registry *Registry, captureDocuments bool) (*LogicalIndex, error) {
	index := &LogicalIndex{Locations: map[LogicalID]LogicalLocation{}}
	if captureDocuments {
		index.documents = map[LogicalID][]byte{}
	}
	generation := sha256.New()
	var paths []string
	err := fs.WalkDir(filesystem, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(path), ".sy") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	results := make([]scanDocumentResult, len(paths))
	if len(paths) > 0 {
		workerCount := runtime.GOMAXPROCS(0)
		if workerCount > 16 {
			workerCount = 16
		}
		if workerCount > len(paths) {
			workerCount = len(paths)
		}
		jobs := make(chan scanDocumentJob)
		var workers sync.WaitGroup
		workers.Add(workerCount)
		for worker := 0; worker < workerCount; worker++ {
			go func() {
				defer workers.Done()
				for job := range jobs {
					raw, readErr := fs.ReadFile(filesystem, job.path)
					if readErr != nil {
						results[job.position].err = readErr
						continue
					}
					result := scanDocument(job.path, raw, registry)
					if captureDocuments {
						result.raw = raw
					}
					results[job.position] = result
				}
			}()
		}

		for position, path := range paths {
			jobs <- scanDocumentJob{position: position, path: path}
		}
		close(jobs)
		workers.Wait()
	}

	for position, result := range results {
		if result.err != nil {
			return nil, result.err
		}
		if captureDocuments {
			_, _ = generation.Write([]byte(filepath.ToSlash(paths[position])))
			_, _ = generation.Write([]byte{0})
			_, _ = generation.Write(result.raw)
			_, _ = generation.Write([]byte{0})
		}
		for _, location := range result.locations {
			if err = addLogicalLocation(index, location); err != nil {
				return nil, err
			}
		}
		if captureDocuments && result.documentID != "" {
			index.documents[result.documentID] = append([]byte(nil), result.raw...)
		}
	}
	if captureDocuments {
		index.generation = "sha256:" + hex.EncodeToString(generation.Sum(nil))
	}
	return index, nil
}

func scanDocument(path string, raw []byte, registry *Registry) scanDocumentResult {
	result := scanDocumentResult{}
	local := &LogicalIndex{Locations: map[LogicalID]LogicalLocation{}}
	add := func(location LogicalLocation) error {
		if err := addLogicalLocation(local, location); err != nil {
			return err
		}
		result.locations = append(result.locations, location)
		return nil
	}

	document, err := DecodeSY(raw, registry)
	if err != nil {
		result.err = fmt.Errorf("%s: %w", path, err)
		return result
	}
	if document.ReadOnly && !readOnlyDiagnosticsSupported(document.Diagnostics) {
		result.err = fmt.Errorf("%s: %w", path, &DiagnosticError{Diagnostics: document.Diagnostics})
		return result
	}
	identity, identityErr := inspectDocumentIdentity(raw)
	if document.Envelope == nil {
		if !document.ReadOnly || !hasDiagnosticCode(document.Diagnostics, CodeSchemaTooNew) {
			return result
		}
		if identityErr != nil {
			result.err = fmt.Errorf("%s: %w", path, identityErr)
			return result
		}
		result.documentID = identity.LogicalID
		result.err = add(LogicalLocation{
			LogicalID:   identity.LogicalID,
			EntityType:  string(identity.EntityType),
			DocumentID:  identity.DocumentID,
			Path:        filepath.ToSlash(path),
			ReadOnly:    true,
			Diagnostics: append([]Diagnostic(nil), document.Diagnostics...),
		})
		return result
	}
	if identityErr != nil {
		result.err = fmt.Errorf("%s: %w", path, identityErr)
		return result
	}
	root, err := decodeRawObject(raw)
	if err != nil {
		result.err = err
		return result
	}
	result.documentID = document.Envelope.Entity.LogicalID
	if err = add(LogicalLocation{
		LogicalID:   document.Envelope.Entity.LogicalID,
		EntityType:  string(document.Envelope.Entity.EntityType),
		DocumentID:  identity.DocumentID,
		Path:        filepath.ToSlash(path),
		ReadOnly:    document.ReadOnly,
		Diagnostics: append([]Diagnostic(nil), document.Diagnostics...),
	}); err != nil {
		result.err = err
		return result
	}
	if childrenRaw, ok := root["Children"]; ok {
		var children []json.RawMessage
		if err = json.Unmarshal(childrenRaw, &children); err != nil {
			result.err = err
			return result
		}
		result.err = scanBlocks(add, children, identity.DocumentID, filepath.ToSlash(path), registry)
	}
	return result
}

func readOnlyDiagnosticsSupported(diagnostics []Diagnostic) bool {
	if len(diagnostics) == 0 {
		return false
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != CodeSchemaTooNew && diagnostic.Code != CodeClaimKindUnavailable {
			return false
		}
	}
	return true
}

func hasDiagnosticCode(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
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
	add func(LogicalLocation) error,
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
			spec, specErr := envelopeSpec(envelopeRaw)
			if specErr != nil {
				return specErr
			}
			major, majorErr := schemaMajor(spec)
			if majorErr != nil {
				return validationError(CodeInvalidBlock, "Evergreen.spec", majorErr.Error())
			}
			currentMajor, _ := schemaMajor(BlockSpec)
			if major <= currentMajor {
				envelope, decodeErr := UnmarshalBlockEnvelope(envelopeRaw)
				if decodeErr != nil {
					return decodeErr
				}
				if validateErr := ValidateBlock(envelope, registry); validateErr != nil {
					if !HasDiagnostic(validateErr, CodeClaimKindUnavailable) &&
						!HasDiagnostic(validateErr, CodeSchemaTooNew) {
						return validateErr
					}
				}
				switch envelope.Role {
				case "note_segment", "agent_annotation":
					err = add(LogicalLocation{
						LogicalID:  envelope.Segment.SegmentID,
						EntityType: "segment",
						DocumentID: documentID,
						BlockID:    blockID,
						Path:       path,
					})
				case "candidate":
					err = add(LogicalLocation{
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
		}
		if childrenRaw, ok := node["Children"]; ok {
			var children []json.RawMessage
			if err = json.Unmarshal(childrenRaw, &children); err != nil {
				return err
			}
			if err = scanBlocks(add, children, documentID, path, registry); err != nil {
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
	Delete    bool      `json:"delete,omitempty"`
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
		if write.Delete {
			if base.Expected == "absent" || write.After != "absent" || plan.Command != NativeEditCommand ||
				plan.Principal.Type != PrincipalUser {
				return validationError(CodeInvalidPlan, path+".after", "native delete requires an existing base and absent result")
			}
		} else if !strings.HasPrefix(write.After, "sha256:") {
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
