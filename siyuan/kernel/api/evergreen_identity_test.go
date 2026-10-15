package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
	"github.com/siyuan-note/siyuan/kernel/conf"
	kernelEvergreen "github.com/siyuan-note/siyuan/kernel/evergreen"
	"github.com/siyuan-note/siyuan/kernel/internal/credentialperm"
	"github.com/siyuan-note/siyuan/kernel/model"
	"github.com/siyuan-note/siyuan/kernel/util"
)

// 使用完整路由和认证链验证身份，客户端正文及 caller header 均不能提升权限。
func TestAPIContractEvergreenCredentialIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldConf, oldConfDir := model.Conf, util.ConfDir
	model.Conf = model.NewAppConf()
	model.Conf.Api = conf.NewAPI()
	model.Conf.Api.Token = "user-master-token"
	model.Conf.AccessAuthCode = "workspace-password"
	util.ConfDir = filepath.Join(t.TempDir(), "conf")
	t.Cleanup(func() { model.Conf, util.ConfDir = oldConf, oldConfDir })
	token := "eg-agent-v1.test-credential"
	digest := sha256.Sum256([]byte(token))
	store := map[string]any{"version": 1, "agents": []any{map[string]any{
		"agent_id": "agent-bound", "digest": hex.EncodeToString(digest[:]), "created_at": "2026-10-11T00:00:00Z",
	}}}
	storeData, _ := json.Marshal(store)
	storeDir := filepath.Join(util.ConfDir, "evergreen-credentials")
	if err := os.MkdirAll(storeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "agents.json"), storeData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := credentialperm.Protect(storeDir, true); err != nil {
		t.Fatal(err)
	}
	if err := credentialperm.Protect(filepath.Join(storeDir, "agents.json"), false); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeEvergreenAPIFixture(t, root, "box/a.sy", "c-a", "before")
	git := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git failed: %v", err)
		}
		return output
	}
	git("init", "-q")
	git("config", "user.name", "Evergreen Test")
	git("config", "user.email", "evergreen-test@example.invalid")
	git("add", "--", "box/a.sy")
	git("commit", "-q", "-m", "fixture")
	before := readEvergreenAPIFile(t, root, "box/a.sy")
	base, _ := evergreencore.SemanticHash(before)
	tagDocument, err := evergreencore.DecodeSY(before, nil)
	if err != nil {
		t.Fatal(err)
	}
	tagDocument.Envelope.Claim.Tags = []string{"legal-tag"}
	tagAfter, err := evergreencore.EncodeSY(before, tagDocument.Envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	tagRequest := evergreencore.PlanRequest{
		Protocol: evergreencore.ChangePlanProtocol, OperationID: "op-identity-tags", Command: "claim.update",
		Base:   []evergreencore.BaseRef{{LogicalID: "c-a", SemanticHash: base}},
		Writes: []evergreencore.WriteInput{{LogicalID: "c-a", After: tagAfter}},
	}
	gitCommitter, err := evergreencore.NewLocalGitCommitter(root)
	if err != nil {
		t.Fatal(err)
	}
	committer := &apiRecordingCommitter{delegate: gitCommitter}
	rt, err := kernelEvergreen.NewRuntime(context.Background(), kernelEvergreen.RuntimeOptions{
		Root: root, Committer: committer,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldProvider := evergreenRuntimeProvider
	evergreenRuntimeProvider = func(context.Context) (*kernelEvergreen.Runtime, error) { return rt, nil }
	t.Cleanup(func() { evergreenRuntimeProvider = oldProvider; _ = rt.Close() })
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if jwtToken := model.ParseXAuthToken(c.Request); jwtToken != nil && jwtToken.Valid {
			claims := model.GetTokenClaims(jwtToken)
			c.Set(model.ClaimsContextKey, claims)
			c.Set(model.RoleContextKey, model.GetClaimRole(claims))
			model.RecordJWTAuthentication(c, jwtToken)
		}
		c.Next()
	})
	ServeAPI(engine)
	model.InitJwtKey()
	pluginJWT, err := model.CreatePluginJWT("test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"protocol": evergreencore.ChangePlanProtocol, "operation_id": "op-credential", "command": "claim.update",
		"base":      []evergreencore.BaseRef{{LogicalID: "c-a", SemanticHash: base}},
		"writes":    []evergreencore.WriteInput{{LogicalID: "c-a", After: mutateEvergreenAPIFixture(t, before, "attack")}},
		"principal": map[string]string{"type": "user", "id": "forged"},
	}
	for _, caller := range []string{"", "ui", "cli", "agent"} {
		t.Run("bound agent "+caller, func(t *testing.T) {
			headers := map[string]string{"Authorization": "Token " + token, model.XAuthTokenKey: pluginJWT,
				evergreenCallerHeader: caller, evergreenRequestReasonHeader: "attempt body mutation"}
			response := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
				"/api/evergreen/v1/plan", request, headers)
			if response.OK || len(response.Diagnostics) != 1 || response.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
				t.Fatalf("agent credential elevated via caller %q: %+v", caller, response)
			}
		})
	}
	t.Run("plugin JWT cannot self report user", func(t *testing.T) {
		response := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
			"/api/evergreen/v1/plan", request, map[string]string{model.XAuthTokenKey: pluginJWT,
				evergreenCallerHeader: "ui", evergreenRequestReasonHeader: "attempt plugin body mutation"})
		if response.OK || len(response.Diagnostics) != 1 || response.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
			t.Fatalf("plugin JWT elevated to user: %+v", response)
		}
	})
	t.Run("master token cannot invent agent identity", func(t *testing.T) {
		response := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
			"/api/evergreen/v1/plan", tagRequest, map[string]string{"Authorization": "Token user-master-token",
				evergreenCallerHeader: "agent", evergreenAgentIDHeader: "invented-agent", evergreenRequestReasonHeader: "attempt impersonation"})
		if response.OK || len(response.Diagnostics) != 1 || response.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
			t.Fatalf("user credential impersonated an agent: %+v", response)
		}
	})
	t.Run("agent token never grants global administrator", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/system/getWorkspaceInfo", nil)
		r.Header.Set("Authorization", "Token "+token)
		r.Header.Set(model.XAuthTokenKey, pluginJWT)
		engine.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("agent credential escaped Evergreen scope: status=%d", recorder.Code)
		}
	})
	t.Run("credential path is protected", func(t *testing.T) {
		if !util.IsForbiddenAbsPath(filepath.Join(storeDir, "agents.json")) {
			t.Fatal("generic file access can expose or replace agent credentials")
		}
	})
	if !bytes.Equal(before, readEvergreenAPIFile(t, root, "box/a.sy")) || committer.count != 0 {
		t.Fatal("identity attacks changed authority or Git")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(evergreencore.RuntimeDirName), "transactions", "op-credential")); !os.IsNotExist(err) {
		t.Fatalf("identity attack created a journal: %v", err)
	}
	t.Run("role alone does not authenticate principal", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/api/evergreen/v1/plan", nil)
		c.Set(model.RoleContextKey, model.RoleAdministrator)
		c.Set("evergreen.principal", evergreencore.Principal{Type: evergreencore.PrincipalUser, ID: "forged"})
		if _, err := evergreenPrincipal(c); err == nil {
			t.Fatal("role or unverified principal context authenticated a user")
		}
	})
	userHeaders := map[string]string{"Authorization": "Token user-master-token", evergreenCallerHeader: "ui"}
	t.Run("credential management requires confirmation", func(t *testing.T) {
		response := performEvergreenAPI[apicontract.EvergreenCredentialsData](t, engine, http.MethodPost,
			"/api/evergreen/v1/credentials", map[string]any{"action": "create", "agent_id": "new-agent"}, userHeaders)
		if response.OK || response.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
			t.Fatalf("unconfirmed credential creation = %+v", response)
		}
	})
	created := performEvergreenAPI[apicontract.EvergreenCredentialsData](t, engine, http.MethodPost,
		"/api/evergreen/v1/credentials", map[string]any{"action": "create", "agent_id": "new-agent", "confirm": true}, userHeaders)
	if !created.OK || !strings.HasPrefix(created.Data.Token, "eg-agent-v1.") {
		t.Fatal("confirmed user could not create a dedicated Agent credential")
	}
	storeData = readEvergreenAPIFile(t, util.ConfDir, "evergreen-credentials/agents.json")
	if bytes.Contains(storeData, []byte(created.Data.Token)) || bytes.Contains(storeData, []byte(token)) {
		t.Fatal("credential store persisted a plaintext token")
	}
	privateFile, err := os.Open(filepath.Join(storeDir, "agents.json"))
	if err != nil {
		t.Fatal(err)
	}
	private := credentialperm.Valid(privateFile)
	privateFile.Close()
	if !private {
		t.Fatal("credential file does not have private permissions")
	}
	list := performEvergreenAPI[apicontract.EvergreenCredentialsData](t, engine, http.MethodPost,
		"/api/evergreen/v1/credentials", map[string]string{"action": "list"}, userHeaders)
	if !list.OK || len(list.Data.Agents) != 2 || list.Data.Token != "" {
		t.Fatal("credential listing exposed a token or lost identities")
	}
	agentHeaders := map[string]string{"Authorization": "Token " + created.Data.Token,
		evergreenCallerHeader: "cli", evergreenRequestReasonHeader: "add a review tag"}
	exported := performEvergreenAPI[evergreencore.CanonicalArchive](t, engine, http.MethodPost,
		"/api/evergreen/v1/workspace/export", map[string]any{}, agentHeaders)
	if !exported.OK || len(exported.Data.Entities) != 1 {
		t.Fatal("dedicated Agent credential cannot use the online CLI read transport")
	}
	t.Run("agent ID cannot be replaced", func(t *testing.T) {
		headers := map[string]string{"Authorization": agentHeaders["Authorization"], evergreenCallerHeader: "ui",
			evergreenAgentIDHeader: "other-agent", evergreenRequestReasonHeader: "attempt identity replacement"}
		response := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
			"/api/evergreen/v1/plan", request, headers)
		if response.OK || response.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
			t.Fatal("caller replaced the credential identity")
		}
	})
	t.Run("agent cannot manage credentials or recover", func(t *testing.T) {
		for _, path := range []string{"/api/evergreen/v1/credentials", "/api/evergreen/v1/recover",
			"/api/evergreen/v1/av/rebuild", "/api/evergreen/v1/review/plan-materialize"} {
			r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"action":"list"}`))
			for key, value := range agentHeaders {
				r.Header.Set(key, value)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, r)
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("agent reached user-only %s: %d", path, recorder.Code)
			}
		}
	})
	document, err := evergreencore.DecodeSY(before, nil)
	if err != nil {
		t.Fatal(err)
	}
	document.Envelope.Claim.Tags = []string{"reviewed"}
	after, err := evergreencore.EncodeSY(before, document.Envelope, nil)
	if err != nil {
		t.Fatal(err)
	}
	legal := evergreencore.PlanRequest{
		Protocol: evergreencore.ChangePlanProtocol, OperationID: "op-agent-legal", Command: "claim.update",
		Base:   []evergreencore.BaseRef{{LogicalID: "c-a", SemanticHash: base}},
		Writes: []evergreencore.WriteInput{{LogicalID: "c-a", After: after}},
	}
	plan := performEvergreenAPI[evergreencore.PlannedOperation](t, engine, http.MethodPost,
		"/api/evergreen/v1/plan", legal, agentHeaders)
	if !plan.OK || plan.Data.Plan.Principal.Type != evergreencore.PrincipalAgent ||
		plan.Data.Plan.Principal.ID != "new-agent" ||
		plan.Data.Plan.Principal.AuthSource != "evergreen-agent-credential/v1" {
		t.Fatalf("legal agent plan principal = %+v, diagnostics=%+v", plan.Data.Plan.Principal, plan.Diagnostics)
	}
	impersonated := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost,
		"/api/evergreen/v1/apply", evergreencore.ApplyRequest{Operation: plan.Data}, userHeaders)
	if impersonated.OK || len(impersonated.Diagnostics) == 0 || impersonated.Diagnostics[0].Code != evergreencore.CodeUnauthorized {
		t.Fatal("another credential applied the Agent plan")
	}
	if !bytes.Equal(before, readEvergreenAPIFile(t, root, "box/a.sy")) || committer.count != 0 {
		t.Fatal("cross-credential apply changed authority or Git")
	}
	applied := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost,
		"/api/evergreen/v1/apply", evergreencore.ApplyRequest{Operation: plan.Data}, agentHeaders)
	retry := performEvergreenAPI[evergreencore.OperationRecord](t, engine, http.MethodPost,
		"/api/evergreen/v1/apply", evergreencore.ApplyRequest{Operation: plan.Data}, agentHeaders)
	if !applied.OK || !retry.OK || applied.Data.GitCommit != retry.Data.GitCommit || committer.count != 1 {
		t.Fatal("authenticated Agent write or idempotent retry failed")
	}
	if committer.requests[0].Principal.ID != "new-agent" || committer.requests[0].Principal.RequestReason != "add a review tag" {
		t.Fatal("Git audit lost the authenticated identity or reason")
	}
	trailers := git("show", "-s", "--format=%B", "HEAD")
	for _, trailer := range []string{"Evergreen-Operation-ID: op-agent-legal", "Evergreen-Principal: agent:new-agent",
		"Evergreen-Auth-Source: evergreen-agent-credential/v1", "Evergreen-Request-Reason: add a review tag"} {
		if !bytes.Contains(trailers, []byte(trailer)) {
			t.Fatalf("committed audit omitted %s", trailer)
		}
	}
	if strings.TrimSpace(string(git("rev-list", "--count", "HEAD"))) != "2" {
		t.Fatal("Agent retry produced a duplicate Git commit")
	}
	revoked := performEvergreenAPI[apicontract.EvergreenCredentialsData](t, engine, http.MethodPost,
		"/api/evergreen/v1/credentials", map[string]any{"action": "revoke", "agent_id": "new-agent", "confirm": true}, userHeaders)
	if !revoked.OK {
		t.Fatal("user could not revoke Agent credential")
	}
	r := httptest.NewRequest(http.MethodGet, "/api/evergreen/v1/capabilities", nil)
	r.RemoteAddr, r.Host = "127.0.0.1:32100", "127.0.0.1"
	model.Conf.AccessAuthCode = ""
	r.Header.Set("Authorization", agentHeaders["Authorization"])
	r.Header.Set(model.XAuthTokenKey, pluginJWT)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, r)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatal("revoked credential fell back to loopback or JWT authentication")
	}
}
