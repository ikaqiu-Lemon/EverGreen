package api

import (
	"github.com/gin-gonic/gin"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
)

var evergreenWorkspaceExport = contractHandler(apicontract.EvergreenWorkspaceExportAPI,
	func(c *gin.Context, _ apicontract.EmptyRequest) apicontract.Response[apicontract.EvergreenExportEnvelope] {
		runtime, err := evergreenRuntimeForRequest(c)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenExportEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		archive, err := runtime.CanonicalExport(c.Request.Context())
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenExportEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		data, err := evergreenConvert[apicontract.EvergreenCanonicalArchive](archive)
		if err != nil {
			return apicontract.SuccessDirectJSON(apicontract.EvergreenExportEnvelope{Diagnostics: evergreenDiagnostics(err)})
		}
		return apicontract.SuccessDirectJSON(apicontract.EvergreenExportEnvelope{OK: true, Data: &data})
	})
