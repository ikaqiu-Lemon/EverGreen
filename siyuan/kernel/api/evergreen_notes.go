package api

import (
	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
)

var evergreenNotePlanCreate = contractHandler(apicontract.EvergreenNotePlanCreateAPI,
	func(c *gin.Context, request apicontract.EvergreenNoteCreateRequest) apicontract.Response[apicontract.EvergreenNoteCreateEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.NoteCreateRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenNoteCreateEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenNoteCreateEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenNoteCreateEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		planned, err := runtime.PlanNoteCreate(c.Request.Context(), principal, coreRequest)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenNoteCreateEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenPlannedNoteCreate](planned)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenNoteCreateEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenNoteCreateEnvelope{
			OK: true, Data: data,
		})
	})
