package api

import (
	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
)

var evergreenSourcePlanCapture = contractHandler(apicontract.EvergreenSourcePlanCaptureAPI,
	func(c *gin.Context, request apicontract.EvergreenSourceCaptureRequest) apicontract.Response[apicontract.EvergreenSourceCaptureEnvelope] {
		coreRequest, err := evergreenConvert[evergreencore.SourceCaptureRequest](request)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenSourceCaptureEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		principal, err := evergreenPrincipal(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenSourceCaptureEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenSourceCaptureEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		planned, err := runtime.PlanSourceCapture(c.Request.Context(), principal, coreRequest)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenSourceCaptureEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		data, err := evergreenConvert[apicontract.EvergreenPlannedSourceCapture](planned)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenSourceCaptureEnvelope{
				Diagnostics: evergreenDiagnostics(err),
			})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenSourceCaptureEnvelope{
			OK: true, Data: data,
		})
	})
