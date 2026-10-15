// SiYuan - From thought to insight, with agents
// Copyright (c) 2020-present, b3log.org
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
	"github.com/siyuan-note/siyuan/kernel/av"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/model"
)

const (
	evergreenCallerHeader        = "X-Evergreen-Caller"
	evergreenAgentIDHeader       = "X-Evergreen-Agent-ID"
	evergreenRequestReasonHeader = "X-Evergreen-Request-Reason"
)

var evergreenRuntimeProvider = func(ctx context.Context) (*kernelEvergreen.Runtime, error) {
	return model.EvergreenRuntime(ctx)
}

var evergreenCredentials = contractHandler(apicontract.EvergreenCredentialsAPI,
	func(c *gin.Context, request apicontract.EvergreenCredentialsRequest) apicontract.Response[apicontract.EvergreenCredentialsEnvelope] {
		data, err := model.ManageEvergreenCredentials(c, request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenCredentialsEnvelope{
				Data:        apicontract.EvergreenCredentialsData{Agents: []apicontract.EvergreenCredentialInfo{}},
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		c.Header("Cache-Control", "no-store")
		return apicontract.SuccessDirectJSON(apicontract.EvergreenCredentialsEnvelope{OK: true, Data: data})
	})

var evergreenCapabilities = contractHandler(apicontract.EvergreenCapabilitiesAPI,
	func(c *gin.Context, _ apicontract.EmptyRequest) apicontract.Response[apicontract.EvergreenCapabilitiesEnvelope] {
		runtime, err := evergreenRuntimeProvider(c.Request.Context())
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenCapabilitiesEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenCapabilities](runtime.Capabilities())
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenCapabilitiesEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenCapabilitiesEnvelope{OK: true, Data: data})
	})

var evergreenSchemas = contractHandler(apicontract.EvergreenSchemasAPI,
	func(c *gin.Context, _ apicontract.EmptyRequest) apicontract.Response[apicontract.EvergreenSchemasEnvelope] {
		runtime, err := evergreenRuntimeProvider(c.Request.Context())
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenSchemasEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenSchemaCatalog](runtime.Schemas())
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenSchemasEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenSchemasEnvelope{OK: true, Data: data})
	})

var evergreenPlan = contractHandler(apicontract.EvergreenPlanAPI,
	func(c *gin.Context, request apicontract.EvergreenPlanRequest) apicontract.Response[apicontract.EvergreenPlanEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.PlanRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		planned, err := runtime.Plan(c.Request.Context(), principal, coreRequest)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenPlannedOperation](planned)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{OK: true, Data: data})
	})

var evergreenApply = contractHandler(apicontract.EvergreenApplyAPI,
	func(c *gin.Context, request apicontract.EvergreenApplyRequest) apicontract.Response[apicontract.EvergreenOperationEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.ApplyRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		record, err := model.ApplyEvergreenOperation(c.Request.Context(), runtime, principal, coreRequest)
		data, convertErr := evergreenConvert[apicontract.EvergreenOperationRecord](record)
		if convertErr != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Diagnostics: evergreenDiagnostics(convertErr),
			})
		}
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Data: data, Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{OK: true, Data: data})
	})

var evergreenOperation = contractHandler(apicontract.EvergreenOperationAPI,
	func(c *gin.Context, _ apicontract.EmptyRequest) apicontract.Response[apicontract.EvergreenOperationEnvelope] {
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		record, err := runtime.Operation(c.Request.Context(), c.Param("id"))
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenOperationRecord](record)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenOperationEnvelope{OK: true, Data: data})
	})

var evergreenRecover = contractHandler(apicontract.EvergreenRecoverAPI,
	func(c *gin.Context, _ apicontract.EmptyRequest) apicontract.Response[apicontract.EvergreenRecoverEnvelope] {
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenRecoverEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		if principal.Type != evergreencore.PrincipalUser {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenRecoverEnvelope{
				Diagnostics: []apicontract.EvergreenDiagnostic{{
					Code: evergreencore.CodeUnauthorized, Level: "error", Path: "principal",
					Message: "journal recovery requires an authenticated user principal",
				}},
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenRecoverEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		records, err := model.RecoverEvergreenOperations(c.Request.Context(), runtime)
		data, convertErr := evergreenConvert[[]apicontract.EvergreenOperationRecord](records)
		if convertErr != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenRecoverEnvelope{
				Diagnostics: evergreenDiagnostics(convertErr),
			})
		}
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenRecoverEnvelope{
				Data: data, Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenRecoverEnvelope{OK: true, Data: data})
	})

var evergreenDoctor = contractHandler(apicontract.EvergreenDoctorAPI,
	func(c *gin.Context, request apicontract.EvergreenDoctorRequest) apicontract.Response[apicontract.EvergreenDoctorEnvelope] {
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenDoctorEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		if request.RebuildIndex && principal.Type != evergreencore.PrincipalUser {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenDoctorEnvelope{
				Diagnostics: []apicontract.EvergreenDiagnostic{{
					Code: evergreencore.CodeUnauthorized, Level: "error", Path: "principal",
					Message: "derived-index rebuild requires an authenticated user principal",
				}},
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenDoctorEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		report, doctorErr := model.DoctorEvergreenWorkspace(c.Request.Context(), runtime, request.RebuildIndex)
		raw, marshalErr := json.Marshal(report)
		if marshalErr != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenDoctorEnvelope{
				Diagnostics: evergreenDiagnostics(marshalErr),
			})
		}
		data, marshalErr := apicontract.EncodedJSONValue(raw)
		if marshalErr != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenDoctorEnvelope{
				Diagnostics: evergreenDiagnostics(marshalErr),
			})
		}
		if doctorErr != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenDoctorEnvelope{
				Data: data, Diagnostics: evergreenDiagnostics(doctorErr),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenDoctorEnvelope{OK: true, Data: data})
	})

var evergreenReviewGet = contractHandler(apicontract.EvergreenReviewGetAPI,
	func(c *gin.Context, request apicontract.EvergreenReviewGetRequest) apicontract.Response[apicontract.EvergreenReviewEnvelope] {
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		var snapshot evergreencore.ReviewSnapshot
		switch {
		case request.NoteID != "" && request.DocumentID == "":
			snapshot, err = runtime.InspectReview(
				c.Request.Context(), evergreencore.LogicalID(request.NoteID))
		case request.NoteID == "" && request.DocumentID != "":
			snapshot, err = runtime.InspectReviewByDocumentID(
				c.Request.Context(), request.DocumentID)
		default:
			err = fmt.Errorf("review/get requires exactly one of note_id or document_id")
		}
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenReviewSnapshot](snapshot)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewEnvelope{
			OK: true, Data: &data,
		})
	})

var evergreenReviewPlan = contractHandler(apicontract.EvergreenReviewPlanAPI,
	func(c *gin.Context, request apicontract.EvergreenReviewPlanRequest) apicontract.Response[apicontract.EvergreenReviewPlanEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.ReviewPlanRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		planned, err := runtime.PlanReview(
			c.Request.Context(), principal, coreRequest)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenPlannedReview](planned)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
			OK: true, Data: &data,
		})
	})

var evergreenReviewPlanEdit = contractHandler(apicontract.EvergreenReviewPlanEditAPI,
	func(c *gin.Context, request apicontract.EvergreenReviewEditPlanRequest) apicontract.Response[apicontract.EvergreenReviewPlanEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.ReviewEditPlanRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		planned, err := runtime.PlanReviewEdit(
			c.Request.Context(), principal, coreRequest)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenPlannedReview](planned)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenReviewPlanEnvelope{
			OK: true, Data: &data,
		})
	})

var evergreenReviewPlanMaterialize = contractHandler(apicontract.EvergreenReviewPlanMaterializeAPI,
	func(c *gin.Context, request apicontract.EvergreenReviewMaterializeRequest) apicontract.Response[apicontract.EvergreenMaterializePlanEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.ReviewMaterializeRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenMaterializePlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenMaterializePlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenMaterializePlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		planned, err := runtime.PlanReviewMaterialization(
			c.Request.Context(), principal, coreRequest)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenMaterializePlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenPlannedMaterialization](planned)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenMaterializePlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenMaterializePlanEnvelope{
			OK: true, Data: &data,
		})
	})

var evergreenAVDescribe = contractHandler(apicontract.EvergreenAVDescribeAPI,
	func(c *gin.Context, _ apicontract.EmptyRequest) apicontract.Response[apicontract.EvergreenAVDescribeEnvelope] {
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVDescribeEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		schema, err := runtime.DescribeAV(c.Request.Context(), c.Param("id"))
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVDescribeEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenAVViewSchema](schema)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVDescribeEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenAVDescribeEnvelope{OK: true, Data: data})
	})

var evergreenAVQuery = contractHandler(apicontract.EvergreenAVQueryAPI,
	func(c *gin.Context, request apicontract.EvergreenAVQueryRequest) apicontract.Response[apicontract.EvergreenAVPageEnvelope] {
		query, err := evergreenConvert[evergreencore.AVQuery](request.Query)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVPageEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVPageEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		page, err := runtime.QueryAV(c.Request.Context(), request.ViewID, query)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVPageEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenAVPage](page)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVPageEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenAVPageEnvelope{OK: true, Data: data})
	})

var evergreenAVCreateView = contractHandler(apicontract.EvergreenAVCreateViewAPI,
	func(c *gin.Context, request apicontract.EvergreenAVCreateViewRequest) apicontract.Response[apicontract.EvergreenAVCreatedViewEnvelope] {
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCreatedViewEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		if len(evergreenRequestNotebookScope(c)) != 0 {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCreatedViewEnvelope{
				Diagnostics: evergreenDiagnostics(evergreenScopeError(evergreencore.CodeInvalidPlan,
					"scoped Evergreen AV creation requires an explicit notebook-backed view command")),
			})
		}
		if av.IsAttributeViewExist(request.ID) {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCreatedViewEnvelope{
				Diagnostics: evergreenDiagnostics(fmt.Errorf("attribute view %q already exists", request.ID)),
			})
		}
		schema, err := runtime.DescribeAV(c.Request.Context(), request.Preset)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCreatedViewEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		attrView := av.NewSourceBackedAttributeView(request.ID, schema)
		if err = av.SaveAttributeView(attrView); err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCreatedViewEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCreatedViewEnvelope{
			OK: true,
			Data: apicontract.EvergreenAVCreatedView{
				ID: request.ID, ViewID: attrView.Views[0].ID, Preset: request.Preset,
			},
		})
	})

var evergreenAVCell = contractHandler(apicontract.EvergreenAVCellAPI,
	func(c *gin.Context, request apicontract.EvergreenAVCellRequest) apicontract.Response[apicontract.EvergreenAVCellEnvelope] {
		row, err := evergreenConvert[evergreencore.AVRowRef](request.Row)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCellEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		column, err := evergreenConvert[evergreencore.AVColumnBinding](request.Column)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCellEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCellEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		cell, err := runtime.GetAVCell(c.Request.Context(), row, column)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCellEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenAVCellValue](cell)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCellEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenAVCellEnvelope{OK: true, Data: data})
	})

var evergreenAVPlanPatch = contractHandler(apicontract.EvergreenAVPlanPatchAPI,
	func(c *gin.Context, request apicontract.EvergreenAVPatch) apicontract.Response[apicontract.EvergreenPlanEnvelope] {
		patch, err := evergreenConvert[evergreencore.AVPatch](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		planned, err := runtime.PlanAVPatch(c.Request.Context(), principal, patch)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenPlannedOperation](planned)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenPlanEnvelope{OK: true, Data: data})
	})

var evergreenAVRebuild = contractHandler(apicontract.EvergreenAVRebuildAPI,
	func(c *gin.Context, _ apicontract.EmptyRequest) apicontract.Response[apicontract.EvergreenAVSnapshotEnvelope] {
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVSnapshotEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		if principal.Type != evergreencore.PrincipalUser {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVSnapshotEnvelope{
				Diagnostics: []apicontract.EvergreenDiagnostic{{
					Code: evergreencore.CodeUnauthorized, Level: "error", Path: "principal",
					Message: "AV index rebuild requires an authenticated user principal",
				}},
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVSnapshotEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		snapshot, err := runtime.RebuildAV(c.Request.Context())
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVSnapshotEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenAVProjectionSnapshot](snapshot)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenAVSnapshotEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenAVSnapshotEnvelope{OK: true, Data: data})
	})

func evergreenPrincipal(c *gin.Context) (evergreencore.Principal, error) {
	return model.AuthenticatedEvergreenPrincipal(c)
}

func evergreenDiagnostics(err error) []apicontract.EvergreenDiagnostic {
	var diagnosticErr *evergreencore.DiagnosticError
	if errors.As(err, &diagnosticErr) && len(diagnosticErr.Diagnostics) > 0 {
		diagnostics, convertErr := evergreenConvert[[]apicontract.EvergreenDiagnostic](diagnosticErr.Diagnostics)
		if convertErr == nil {
			return diagnostics
		}
	}
	return []apicontract.EvergreenDiagnostic{{
		Code: evergreencore.CodeTransactionBlocked, Level: "error", Path: "runtime", Message: err.Error(),
	}}
}

func evergreenConvert[To, From any](from From) (To, error) {
	var to To
	data, err := json.Marshal(from)
	if err != nil {
		return to, err
	}
	if err = json.Unmarshal(data, &to); err != nil {
		return to, err
	}
	return to, nil
}
