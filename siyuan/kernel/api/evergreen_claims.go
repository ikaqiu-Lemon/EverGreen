package api

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
)

var evergreenClaimGet = contractHandler(apicontract.EvergreenClaimGetAPI,
	func(c *gin.Context, request apicontract.EvergreenClaimGetRequest) apicontract.Response[apicontract.EvergreenClaimEnvelope] {
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenClaimEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		var snapshot evergreencore.ClaimSnapshot
		switch {
		case request.LogicalID != "" && request.DocumentID == "":
			snapshot, err = runtime.InspectClaim(c.Request.Context(), evergreencore.LogicalID(request.LogicalID))
		case request.DocumentID != "" && request.LogicalID == "":
			snapshot, err = runtime.InspectClaimByDocumentID(c.Request.Context(), request.DocumentID)
		default:
			err = fmt.Errorf("claims/get requires exactly one of logical_id or document_id")
		}
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenClaimEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		data, err := evergreenConvert[apicontract.EvergreenClaimSnapshot](snapshot)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenClaimEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenClaimEnvelope{OK: true, Data: &data})
	})

var evergreenClaimPlanEdit = contractHandler(apicontract.EvergreenClaimPlanEditAPI,
	func(c *gin.Context, request apicontract.EvergreenClaimEditRequest) apicontract.Response[apicontract.EvergreenPlanEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.ClaimEditRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		return evergreenClaimPlanResponse(c, func(ctx context.Context, runtime *kernelEvergreen.Runtime, principal evergreencore.Principal) (evergreencore.PlannedOperation, error) {
			return runtime.PlanClaimEdit(ctx, principal, coreRequest)
		})
	})

var evergreenClaimPlanCreate = contractHandler(apicontract.EvergreenClaimPlanCreateAPI,
	func(c *gin.Context, request apicontract.EvergreenClaimCreateRequest) apicontract.Response[apicontract.EvergreenPlanEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.ClaimCreateRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		return evergreenClaimPlanResponse(c, func(ctx context.Context, runtime *kernelEvergreen.Runtime, principal evergreencore.Principal) (evergreencore.PlannedOperation, error) {
			return runtime.PlanClaimCreate(ctx, principal, coreRequest)
		})
	})

var evergreenClaimPlanBulk = contractHandler(apicontract.EvergreenClaimPlanBulkAPI,
	func(c *gin.Context, request apicontract.EvergreenBulkClaimRequest) apicontract.Response[apicontract.EvergreenPlanEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.BulkClaimRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		return evergreenClaimPlanResponse(c, func(ctx context.Context, runtime *kernelEvergreen.Runtime, principal evergreencore.Principal) (evergreencore.PlannedOperation, error) {
			return runtime.PlanBulkClaims(ctx, principal, coreRequest)
		})
	})

var evergreenClaimBulkCapabilities = contractHandler(apicontract.EvergreenClaimBulkCapabilitiesAPI,
	func(c *gin.Context, request apicontract.EvergreenBulkCapabilitiesRequest) apicontract.Response[apicontract.EvergreenBulkCapabilitiesEnvelope] {
		rows, err := evergreenConvert[[]evergreencore.AVRowRef](request.Rows)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenBulkCapabilitiesEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenBulkCapabilitiesEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenBulkCapabilitiesEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		capabilities, err := runtime.BulkCapabilities(c.Request.Context(), principal, rows)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenBulkCapabilitiesEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		data, err := evergreenConvert[apicontract.EvergreenBulkCapabilities](capabilities)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenBulkCapabilitiesEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenBulkCapabilitiesEnvelope{OK: true, Data: &data})
	})

func evergreenClaimPlanResponse(c *gin.Context, plan func(context.Context, *kernelEvergreen.Runtime, evergreencore.Principal) (evergreencore.PlannedOperation, error)) apicontract.Response[apicontract.EvergreenPlanEnvelope] {
	principal, err := evergreenPrincipal(c)
	if err != nil {
		return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{Diagnostics: evergreenDiagnostics(err)})
	}
	runtime, err := evergreenRuntimeForRequest(c)
	if err != nil {
		return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{Diagnostics: evergreenDiagnostics(err)})
	}
	planned, err := plan(c.Request.Context(), runtime, principal)
	if err != nil {
		return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{Diagnostics: evergreenDiagnostics(err)})
	}
	data, err := evergreenConvert[apicontract.EvergreenPlannedOperation](planned)
	if err != nil {
		return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{Diagnostics: evergreenDiagnostics(err)})
	}
	return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{OK: true, Data: data})
}
