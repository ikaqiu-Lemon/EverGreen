package model

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	ginSessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
	"github.com/siyuan-note/siyuan/kernel/conf"
	"github.com/siyuan-note/siyuan/kernel/internal/credentialperm"
	"github.com/siyuan-note/siyuan/kernel/util"
)

func TestEvergreenAuthenticatedUserSources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldConf, oldWorkspace := Conf, util.WorkspaceDir
	Conf = NewAppConf()
	Conf.Api = conf.NewAPI()
	Conf.Api.Token = "master-token"
	Conf.AccessAuthCode = "workspace-password"
	util.WorkspaceDir = "identity-test-workspace"
	t.Cleanup(func() { Conf, util.WorkspaceDir = oldConf, oldWorkspace })
	engine := gin.New()
	engine.Use(ginSessions.Sessions("siyuan", cookie.NewStore([]byte("identity-test-cookie-key"))))
	engine.GET("/login", func(c *gin.Context) {
		session := util.GetSession(c)
		util.GetWorkspaceSession(session).AccessAuthCode = Conf.AccessAuthCode
		if err := session.Save(c); err != nil {
			t.Fatal(err)
		}
		c.Status(http.StatusNoContent)
	})
	engine.POST("/api/evergreen/v1/plan", CheckAuth, func(c *gin.Context) {
		principal, err := AuthenticatedEvergreenPrincipal(c)
		if err != nil {
			t.Fatal(err)
		}
		tx := &Transaction{}
		BindEvergreenTransactionIdentity(c, tx)
		expectedNative := principal.AuthSource != "api-token"
		if (tx.nativeIdentity != nil) != expectedNative {
			t.Errorf("native admission for verified source %q = %v", principal.AuthSource, tx.nativeIdentity != nil)
		}
		c.JSON(http.StatusOK, principal)
	})
	login := httptest.NewRecorder()
	engine.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:6806/login", nil))
	tests := []struct {
		name, source string
		setup        func(*http.Request)
	}{
		{"master token", "api-token", func(r *http.Request) { r.Header.Set("Authorization", "Token master-token") }},
		{"query token", "api-token", func(r *http.Request) { r.URL.RawQuery = "token=master-token" }},
		{"password session", "workspace-session", func(r *http.Request) {
			for _, value := range login.Result().Cookies() {
				r.AddCookie(value)
			}
		}},
		{"basic password", "workspace-basic-auth", func(r *http.Request) { r.SetBasicAuth(util.WorkspaceName, "workspace-password") }},
		{"trusted local", "trusted-loopback", func(r *http.Request) {
			Conf.AccessAuthCode = ""
			r.RemoteAddr = "127.0.0.1:32100"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			Conf.AccessAuthCode = "workspace-password"
			for _, caller := range []string{"ui", "cli", ""} {
				r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:6806/api/evergreen/v1/plan", nil)
				test.setup(r)
				r.Header.Set("X-Evergreen-Caller", caller)
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, r)
				var principal evergreencore.Principal
				if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &principal) != nil ||
					principal.Type != evergreencore.PrincipalUser || principal.ID != "workspace-owner" || principal.AuthSource != test.source {
					t.Fatalf("authentication source or stable user identity lost: status=%d body=%s", recorder.Code, recorder.Body)
				}
			}
		})
	}
}

func TestEvergreenNativeIdentityCannotSurviveTransportOrReplayCaller(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/transactions", nil)
	c.Set(RoleContextKey, RoleAdministrator)
	recordEvergreenUserAuthentication(c, "workspace-session")
	tx := &Transaction{}
	tx.MarkFromAPI()
	tx.MarkReplay()
	BindEvergreenTransactionIdentity(c, tx)
	if tx.nativeIdentity == nil {
		t.Fatal("verified human session lost native identity")
	}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	var transported Transaction
	if err = json.Unmarshal(raw, &transported); err != nil || transported.nativeIdentity != nil {
		t.Fatal("JSON transport retained native identity")
	}
	if err = json.Unmarshal([]byte(`{"nativeIdentity":{"principal":{"type":"user","id":"workspace-owner","auth_source":"workspace-session"}}}`), &transported); err != nil ||
		transported.nativeIdentity != nil {
		t.Fatal("JSON payload forged native identity")
	}
	for _, source := range []string{"api-token", "workspace-access-bypass", "unsupported-source"} {
		recordEvergreenUserAuthentication(c, source)
		c.Request.Header.Set("X-Evergreen-Caller", "ui")
		BindEvergreenTransactionIdentity(c, tx)
		if tx.nativeIdentity != nil {
			t.Fatalf("replay retained previous identity for %s", source)
		}
	}
	c.Set(evergreenIdentityKey, evergreenIdentity{principal: evergreencore.Principal{
		Type: evergreencore.PrincipalAgent, ID: "plugin", AuthSource: "kernel-plugin-jwt",
	}})
	c.Request.Header.Set("X-Evergreen-Request-Reason", "replay")
	BindEvergreenTransactionIdentity(c, tx)
	if tx.nativeIdentity != nil {
		t.Fatal("plugin identity received native user admission")
	}
	recordEvergreenUserAuthentication(c, "workspace-oidc-session")
	BindEvergreenTransactionIdentity(c, tx)
	if tx.nativeIdentity == nil {
		t.Fatal("verified OIDC identity was rejected")
	}
	c.Set(RoleContextKey, RoleReader)
	BindEvergreenTransactionIdentity(c, tx)
	if tx.nativeIdentity != nil {
		t.Fatal("read-only session retained native writer admission")
	}
}

func TestEvergreenCredentialStoreBoundaries(t *testing.T) {
	oldConf, oldConfDir := Conf, util.ConfDir
	Conf = NewAppConf()
	util.ConfDir = t.TempDir()
	t.Cleanup(func() { Conf, util.ConfDir = oldConf, oldConfDir })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/evergreen/v1/credentials", nil)
	c.Set(RoleContextKey, RoleAdministrator)
	recordEvergreenUserAuthentication(c, "workspace-session")
	create := func(id string) (apicontract.EvergreenCredentialsData, error) {
		return ManageEvergreenCredentials(c, apicontract.EvergreenCredentialsRequest{Action: "create", AgentID: id, Confirm: true})
	}
	first, err := create("agent-one")
	if err != nil || first.Token == "" {
		t.Fatal("credential setup failed")
	}
	path := filepath.Join(util.ConfDir, evergreenCredentialDir, "agents.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("identity cannot be reused", func(t *testing.T) {
		if _, err := create("agent-one"); err == nil {
			t.Fatal("duplicate agent identity accepted")
		}
		revoked, err := ManageEvergreenCredentials(c, apicontract.EvergreenCredentialsRequest{
			Action: "revoke", AgentID: "agent-one", Confirm: true,
		})
		if err != nil || revoked.Token != "" {
			t.Fatal("revocation failed or leaked credential")
		}
		if _, err := create("agent-one"); err == nil {
			t.Fatal("revoked identity was reused")
		}
		if err := os.WriteFile(path, before, 0600); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("corruption is preserved", func(t *testing.T) {
		corrupt := []byte(`{"version":2,"agents":[]}`)
		if err := os.WriteFile(path, corrupt, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := create("agent-two"); err == nil {
			t.Fatal("corrupted credential store accepted")
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, corrupt) {
			t.Fatal("corrupted credential store was overwritten")
		}
		if err := os.WriteFile(path, before, 0600); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unsafe permissions are rejected", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, before, 0644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Chmod(path, 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := create("agent-two"); err == nil {
			t.Fatal("world-readable credential store accepted")
		}
		if err := credentialperm.Protect(path, false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unsafe directory permissions are rejected", func(t *testing.T) {
		dir := filepath.Dir(path)
		if runtime.GOOS == "windows" {
			other := filepath.Join(util.ConfDir, "unprotected")
			if err := os.Mkdir(other, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, filepath.Join(other, "agents.json")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(other, dir); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Chmod(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := readEvergreenCredentialStore(); err == nil {
			t.Fatal("credential authentication accepted a replaceable directory")
		}
		if _, err := create("agent-two"); err == nil {
			t.Fatal("credential management accepted a replaceable directory")
		}
		if err := credentialperm.Protect(dir, true); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("symlink store is rejected", func(t *testing.T) {
		external := filepath.Join(t.TempDir(), "external.json")
		if err := os.WriteFile(external, before, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, path); err != nil {
			t.Fatal(err)
		}
		if _, err := create("agent-two"); err == nil {
			t.Fatal("linked credential store accepted")
		}
		got, _ := os.ReadFile(external)
		if !bytes.Equal(got, before) {
			t.Fatal("linked credential target was modified")
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, before, 0600); err != nil {
			t.Fatal(err)
		}
		if err := credentialperm.Protect(path, false); err != nil {
			t.Fatal(err)
		}
	})
}
