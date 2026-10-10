package cli

import (
	"context"
	"os"
	"os/user"
	"strings"

	"github.com/ikaqiu-Lemon/EverGreen/internal/client"
	"github.com/ikaqiu-Lemon/EverGreen/internal/migrate"
	"github.com/ikaqiu-Lemon/EverGreen/internal/model"
	"github.com/ikaqiu-Lemon/EverGreen/internal/query"
	core "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
)

func usesSYBackend(inv *Invocation) bool {
	return inv.String("backend") == "sy"
}

func (r *Root) runSYBackend(inv *Invocation) (*Result, error) {
	if inv.VaultFlag == "" {
		return nil, &UsageError{Msg: ".sy backend requires explicit --vault <data-dir>"}
	}
	if inv.Cmd.Name == "context" && ((inv.String("source") == "") == (inv.String("note") == "")) {
		return nil, &UsageError{Msg: "context requires exactly one of --source or --note"}
	}
	if inv.Cmd.Name == "materialize" && (inv.String("request") == "" || inv.Set("note") ||
		inv.Set("candidate") || inv.Set("all")) {
		return nil, &UsageError{Msg: ".sy materialize requires --request; legacy selection flags are not supported"}
	}
	if inv.Cmd.Name == "apply" && (inv.String("plan") == "" || inv.String("plan") == "-") {
		return nil, &UsageError{Msg: ".sy apply requires --plan <operation.json>"}
	}
	ctx := context.Background()
	var repository core.Repository
	var online *client.Client
	if strings.TrimSpace(os.Getenv(client.KernelURLEnv)) != "" {
		caller := client.CallerAgent
		if inv.UserRequest {
			caller = client.CallerCLI
		}
		var err error
		online, err = client.FromEnvironment(caller)
		if err != nil {
			return nil, syError(err)
		}
		if _, err = online.Capabilities(ctx); err != nil {
			return nil, syError(err)
		}
		archive, err := online.CanonicalExport(ctx)
		if err != nil {
			return nil, syError(err)
		}
		repository, err = core.NewArchiveRepository(archive)
		if err != nil {
			return nil, syError(err)
		}
	} else {
		var err error
		repository, err = core.NewSYReadRepository(inv.VaultFlag, core.ApplicationRegistry())
		if err != nil {
			return nil, syError(err)
		}
	}
	registry := core.ApplicationRegistry()
	switch inv.Cmd.Name {
	case "context":
		id := inv.String("note")
		if id == "" {
			id = inv.String("source")
		}
		data, err := query.ContextSY(ctx, repository, registry, core.LogicalID(id), inv.String("domain"))
		if err != nil {
			return nil, syError(err)
		}
		native, err := core.ContextWorkspace(ctx, repository, registry, core.LogicalID(id))
		if err != nil {
			return nil, syError(err)
		}
		return &Result{Data: map[string]interface{}{"context": native, "domain": data.Domain, "source": data.Source,
			"notes": data.Notes, "cards": data.Cards, "draft_candidates": data.DraftCandidates,
			"knowledge_candidates": data.KnowledgeCandidates, "opinion_candidates": data.OpinionCandidates,
			"base": data.Base}}, nil
	case "search":
		if len(inv.Args) != 1 {
			return nil, &UsageError{Msg: "search requires one query"}
		}
		page, err := pageSpecFrom(inv)
		if err != nil {
			return nil, err
		}
		result, err := query.SearchSY(ctx, repository, registry, query.SearchRequest{
			Query: inv.Args[0], Kind: query.SearchKind(inv.String("kind")), Domain: inv.String("domain"),
			Tags: captureTags(inv), Since: inv.String("since"), Until: inv.String("until"), Page: page,
			IncludeDeleted: inv.String("include-deleted") == "true",
		})
		if err != nil {
			return nil, syError(err)
		}
		return &Result{Data: map[string]interface{}{"hits": result.Hits, "total": result.Total,
			"scanned_files": result.ScannedFiles, "skipped_files": result.SkippedFiles},
			Warnings: queryDiagnostics(result.Diagnostics)}, nil
	case "card":
		if len(inv.Args) != 1 {
			return nil, &UsageError{Msg: "card show requires a logical Claim ID"}
		}
		items, err := core.QueryWorkspace(ctx, repository, registry, "")
		if err != nil {
			return nil, syError(err)
		}
		for _, item := range items {
			if item.ID == core.LogicalID(inv.Args[0]) && item.Envelope.Claim != nil {
				edges, err := core.RelationsWorkspace(ctx, repository, registry, item.ID)
				if err != nil {
					return nil, syError(err)
				}
				page, err := pageSpecFrom(inv)
				if err != nil {
					return nil, err
				}
				relations, err := query.RelSY(ctx, repository, registry, query.RelRequest{ID: model.RelationEndpoint(item.ID),
					Page: page, IncludeDeprecated: inv.String("include-deprecated") == "true"})
				if err != nil {
					return nil, syError(err)
				}
				body, err := core.MarkdownText(item.Body)
				if err != nil {
					return nil, syError(err)
				}
				detail, err := query.ClaimDetailSY(item, relations)
				if err != nil {
					return nil, syError(err)
				}
				markers, err := cardMarkerState(detail)
				if err != nil {
					return nil, err
				}
				card := query.WithUnreviewed(detail.Card, markers.Unreviewed)
				return &Result{Data: map[string]interface{}{"claim": item, "relations": edges, "id": item.ID,
					"title": item.Title, "body": string(body), "status": item.Envelope.Claim.Status, "tags": item.Envelope.Claim.Tags,
					"sources": query.SYSourceRefs(item.Envelope), "relations_out": relations.Data.RelationsOut,
					"relations_in": relations.Data.RelationsIn, "domain": card.Domain, "created_at": card.CreatedAt,
					"updated_at": card.UpdatedAt, "sections": card.Sections, "unknown_sections": card.UnknownSections,
					"deprecated": card.Deprecated, "deleted": card.Deleted, "unreviewed": card.Unreviewed, "markers": card.Markers},
					Warnings: queryDiagnostics(relations.Diagnostics)}, nil
			}
		}
		return nil, &UsageError{Msg: "Claim does not exist"}
	case "rel":
		if inv.Sub != "" {
			return nil, &UsageError{Msg: "use eg apply --backend sy for owner-side typed-edge writes"}
		}
		if len(inv.Args) != 1 {
			return nil, &UsageError{Msg: "rel requires a logical Claim ID"}
		}
		page, err := pageSpecFrom(inv)
		if err != nil {
			return nil, err
		}
		data, err := query.RelSY(ctx, repository, registry, query.RelRequest{ID: model.RelationEndpoint(inv.Args[0]),
			To: inv.String("to"), Page: page, IncludeDeprecated: inv.String("include-deprecated") == "true",
			ReplacedBy: inv.String(query.ReplacedByFlag) == "true"})
		if err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
		return &Result{Data: map[string]interface{}{"id": data.Data.ID, "relations_out": data.Data.RelationsOut,
			"relations_in": data.Data.RelationsIn, "total": data.Page.Total},
			Warnings: queryDiagnostics(data.Diagnostics)}, nil
	case "materialize":
		if !inv.UserRequest {
			return nil, syError(&core.DiagnosticError{Diagnostics: []core.Diagnostic{{
				Code: core.CodeUnauthorized, Level: "error", Path: "user_request", Message: "materialization requires a user request",
			}}})
		}
		var request core.ReviewMaterializeRequest
		if err := readJSON(inv.String("request"), &request); err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
		principal, err := syPrincipal(inv)
		if err != nil {
			return nil, syError(err)
		}
		var plan core.PlannedMaterialization
		if online != nil {
			plan, err = online.PlanReviewMaterialization(ctx, request)
		} else {
			host, hostErr := migrate.Repository(inv.VaultFlag)
			if hostErr != nil {
				return nil, syError(hostErr)
			}
			service := core.NewAuthorityService(host, core.CommandPolicy{}, registry)
			plan, err = service.PlanReviewMaterialization(ctx, principal, request)
		}
		if err != nil {
			return nil, syError(err)
		}
		if inv.String("dry-run") == "true" {
			return &Result{Data: map[string]interface{}{"plan": plan}}, nil
		}
		return r.applySYOperation(inv, online, plan.Operation)
	case "apply":
		var operation core.PlannedOperation
		if err := readJSON(inv.String("plan"), &operation); err != nil {
			return nil, &UsageError{Msg: err.Error()}
		}
		if inv.String("dry-run") == "true" {
			principal, err := syPrincipal(inv)
			if err != nil {
				return nil, syError(err)
			}
			request := core.PlanRequest{Protocol: operation.Plan.Protocol, OperationID: operation.Plan.OperationID,
				Command: operation.Plan.Command, Base: operation.Plan.Base}
			for _, image := range operation.Images {
				request.Writes = append(request.Writes, core.WriteInput{LogicalID: image.LogicalID, Path: image.Path, After: image.Bytes})
			}
			var planned core.PlannedOperation
			if online != nil {
				planned, err = online.Plan(ctx, request)
			} else {
				host, hostErr := migrate.Repository(inv.VaultFlag)
				if hostErr != nil {
					return nil, syError(hostErr)
				}
				planned, err = core.NewAuthorityService(host, core.CommandPolicy{}, registry).Plan(ctx, principal, request)
			}
			if err != nil {
				return nil, syError(err)
			}
			return &Result{Data: map[string]interface{}{"plan": planned}}, nil
		}
		return r.applySYOperation(inv, online, operation)
	}
	return nil, &UsageError{Msg: "this command does not support the .sy backend"}
}

func syPrincipal(inv *Invocation) (core.Principal, error) {
	if inv.UserRequest {
		current, err := user.Current()
		if err != nil {
			return core.Principal{}, err
		}
		return core.Principal{Type: core.PrincipalUser, ID: current.Username, AuthSource: "local-user"}, nil
	}
	id, reason := os.Getenv(client.AgentIDEnv), os.Getenv(client.RequestReasonEnv)
	principal := core.Principal{Type: core.PrincipalAgent, ID: id, RequestReason: reason, AuthSource: "local-agent"}
	if strings.TrimSpace(id) == "" || strings.TrimSpace(reason) == "" {
		return principal, &core.DiagnosticError{Diagnostics: []core.Diagnostic{{
			Code: core.CodeUnauthorized, Level: "error", Path: "principal", Message: "Agent requires EG_AGENT_ID and EG_REQUEST_REASON",
		}}}
	}
	return principal, nil
}

func syError(err error) error {
	diagnostics := []Diagnostic{}
	if typed, ok := err.(*core.DiagnosticError); ok {
		for _, diagnostic := range typed.Diagnostics {
			diagnostics = append(diagnostics, Diagnostic{Code: diagnostic.Code, Level: diagnostic.Level,
				Path: diagnostic.Path, Message: diagnostic.Message, OpIndex: NonOpDiagnostic})
		}
	}
	return &ValidationError{Msg: err.Error(), Diags: diagnostics}
}
