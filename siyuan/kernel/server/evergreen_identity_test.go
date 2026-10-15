package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/siyuan-note/siyuan/kernel/conf"
	"github.com/siyuan-note/siyuan/kernel/model"
)

// 使用生产 JWT middleware，验证 subject 与来源进入身份记录，角色和 header 不替代认证结果。
func TestEvergreenJWTAuthenticationIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldConf := model.Conf
	model.Conf = model.NewAppConf()
	model.Conf.Api = conf.NewAPI()
	model.Conf.AccessAuthCode = "password"
	t.Cleanup(func() { model.Conf = oldConf })
	model.InitJwtKey()
	token, err := model.CreatePluginJWT("verified-plugin")
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(jwtMiddleware)
	engine.POST("/api/evergreen/v1/probe", model.CheckAuth, func(c *gin.Context) {
		principal, err := model.AuthenticatedEvergreenPrincipal(c)
		if err != nil {
			c.Status(http.StatusForbidden)
			return
		}
		if principal.Type != "agent" || principal.ID != "verified-plugin" || principal.AuthSource != "kernel-plugin-jwt" {
			t.Fatalf("JWT did not bind authenticated subject: %+v", principal)
		}
		c.Status(http.StatusNoContent)
	})
	for _, caller := range []string{"", "ui", "cli", "agent"} {
		request := httptest.NewRequest(http.MethodPost, "/api/evergreen/v1/probe", nil)
		request.Header.Set(model.XAuthTokenKey, token)
		request.Header.Set("X-Evergreen-Caller", caller)
		request.Header.Set("X-Evergreen-Request-Reason", "inspect authenticated identity")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("valid plugin JWT rejected for caller %q: %d", caller, recorder.Code)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/evergreen/v1/probe", nil)
	request.Header.Set(model.XAuthTokenKey, token)
	request.Header.Set("Authorization", "Token eg-agent-v1.invalid")
	request.Header.Set("X-Evergreen-Request-Reason", "attempt authentication fallback")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("dedicated credential bypassed the registered-route restriction: %d", recorder.Code)
	}
}
