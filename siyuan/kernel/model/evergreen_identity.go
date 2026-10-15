package model

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	evergreencore "github.com/ikaqiu-Lemon/EverGreen/pkg/evergreencore"
	"github.com/siyuan-note/siyuan/kernel/apicontract"
	"github.com/siyuan-note/siyuan/kernel/internal/credentialperm"
	"github.com/siyuan-note/siyuan/kernel/util"
)

const evergreenIdentityKey = "evergreen.verified-identity"
const evergreenAgentTokenPrefix = "eg-agent-v1."
const evergreenCredentialDir = "evergreen-credentials"

var evergreenCredentialsLock sync.Mutex

type evergreenIdentity struct {
	principal evergreencore.Principal
}

type nativeTransactionIdentity struct {
	principal evergreencore.Principal
}

// BindEvergreenTransactionIdentity 只把当前认证链中的人工身份绑定到事务，重放须重新绑定。
// 普通 API token 继续使用原生普通文档权限，不能由 caller header 获得受管正文编辑权限。
func BindEvergreenTransactionIdentity(c *gin.Context, tx *Transaction) {
	if tx == nil {
		return
	}
	tx.nativeIdentity = nil
	principal, err := AuthenticatedEvergreenPrincipal(c)
	if err != nil || principal.Type != evergreencore.PrincipalUser || !IsAdminRoleContext(c) {
		return
	}
	switch principal.AuthSource {
	case "workspace-session", "workspace-oidc-session", "workspace-basic-auth", "trusted-loopback":
		tx.nativeIdentity = &nativeTransactionIdentity{principal: principal}
	}
}

type evergreenAgentCredential struct {
	AgentID   string `json:"agent_id"`
	Digest    string `json:"digest"`
	CreatedAt string `json:"created_at"`
	Revoked   bool   `json:"revoked,omitempty"`
}

type evergreenCredentialStore struct {
	Version int                        `json:"version"`
	Agents  []evergreenAgentCredential `json:"agents"`
}

func evergreenUnauthorized(message string) error {
	return &evergreencore.DiagnosticError{Diagnostics: []evergreencore.Diagnostic{{
		Code: evergreencore.CodeUnauthorized, Level: "error", Path: "principal", Message: message,
	}}}
}

func recordEvergreenUserAuthentication(c *gin.Context, source string) {
	c.Set(evergreenIdentityKey, evergreenIdentity{principal: evergreencore.Principal{
		Type: evergreencore.PrincipalUser, ID: "workspace-owner", AuthSource: source,
	}})
}

// RecordJWTAuthentication 只接收已验证 token，插件身份保持 Agent，发布服务身份保持只读。
func RecordJWTAuthentication(c *gin.Context, token *jwt.Token) {
	if token == nil || !token.Valid {
		return
	}
	claims := GetTokenClaims(token)
	audience, err := claims.GetAudience()
	subject, subjectErr := claims.GetSubject()
	if err != nil || subjectErr != nil || subject == "" {
		return
	}
	for _, value := range audience {
		if value == kernelPluginAudience {
			c.Set(evergreenIdentityKey, evergreenIdentity{principal: evergreencore.Principal{
				Type: evergreencore.PrincipalAgent, ID: subject, AuthSource: "kernel-plugin-jwt",
			}})
			return
		}
	}
}

// AuthenticatedEvergreenPrincipal 返回认证链记录的身份，header 只能核对身份及提供请求理由。
func AuthenticatedEvergreenPrincipal(c *gin.Context) (evergreencore.Principal, error) {
	value, exists := c.Get(evergreenIdentityKey)
	identity, ok := value.(evergreenIdentity)
	if !exists || !ok || identity.principal.ID == "" {
		return evergreencore.Principal{}, evergreenUnauthorized("verified Evergreen identity is required")
	}
	principal := identity.principal
	if strings.ContainsAny(principal.ID, "\r\n\x00") || len(principal.ID) > 256 {
		return evergreencore.Principal{}, evergreenUnauthorized("invalid authenticated identity")
	}
	caller := strings.TrimSpace(c.GetHeader("X-Evergreen-Caller"))
	if caller != "" && caller != "ui" && caller != "cli" && caller != "agent" {
		return evergreencore.Principal{}, evergreenUnauthorized("unsupported Evergreen caller")
	}
	if principal.Type == evergreencore.PrincipalUser && (caller == "agent" || c.GetHeader("X-Evergreen-Agent-ID") != "") {
		return evergreencore.Principal{}, evergreenUnauthorized("user credentials cannot authenticate an agent identity")
	}
	if principal.Type == evergreencore.PrincipalAgent {
		if id := strings.TrimSpace(c.GetHeader("X-Evergreen-Agent-ID")); id != "" && id != principal.ID {
			return evergreencore.Principal{}, evergreenUnauthorized("agent identity does not match its credential")
		}
	}
	principal.RequestReason = strings.TrimSpace(c.GetHeader("X-Evergreen-Request-Reason"))
	if strings.ContainsAny(principal.RequestReason, "\r\n\x00") || len(principal.RequestReason) > 4096 {
		return evergreencore.Principal{}, evergreenUnauthorized("invalid Evergreen request reason")
	}
	if principal.Type == evergreencore.PrincipalAgent && principal.RequestReason == "" {
		return evergreencore.Principal{}, evergreenUnauthorized("agent request reason is required")
	}
	return principal, nil
}

// Evergreen 命令入口沿用 workspace 管理员权限，同时只允许受限凭据以 Editor 角色进入同一 core。
func CheckEvergreenCommandRole(c *gin.Context) {
	value, _ := c.Get(evergreenIdentityKey)
	identity, ok := value.(evergreenIdentity)
	if ok && (identity.principal.Type == evergreencore.PrincipalUser && IsAdminRoleContext(c) ||
		identity.principal.Type == evergreencore.PrincipalAgent &&
			IsValidRole(GetGinContextRole(c), []Role{RoleAdministrator, RoleEditor})) {
		c.Next()
		return
	}
	c.AbortWithStatus(http.StatusForbidden)
}

// 路径和方法均由契约注册表核对，新增普通 API 不会继承 Agent 凭据权限。
func isEvergreenAgentRoute(c *gin.Context) bool {
	if !strings.HasPrefix(c.FullPath(), "/api/evergreen/v1/") ||
		c.FullPath() == "/api/evergreen/v1/credentials" {
		return false
	}
	for _, definition := range apicontract.Definitions() {
		if definition.Path != c.FullPath() {
			continue
		}
		for _, method := range definition.Methods {
			if method == c.Request.Method {
				return true
			}
		}
	}
	return false
}

func requestEvergreenAgentToken(c *gin.Context) (string, bool) {
	header := c.GetHeader("Authorization")
	for _, prefix := range []string{"Token ", "token ", "Bearer ", "bearer "} {
		if token, ok := strings.CutPrefix(header, prefix); ok && strings.HasPrefix(token, evergreenAgentTokenPrefix) {
			return token, true
		}
	}
	token := c.Query("token")
	return token, strings.HasPrefix(token, evergreenAgentTokenPrefix)
}

// checkEvergreenAgentCredential 在 JWT、本机放行和主 token 之前判定，拒绝后不再尝试其他身份。
func checkEvergreenAgentCredential(c *gin.Context) bool {
	token, present := requestEvergreenAgentToken(c)
	if !present {
		return false
	}
	if !isEvergreenAgentRoute(c) {
		c.AbortWithStatus(http.StatusForbidden)
		return true
	}
	if retry := util.AuthThrottleCheck(c.ClientIP()); retry > 0 {
		c.AbortWithStatus(http.StatusTooManyRequests)
		return true
	}
	evergreenCredentialsLock.Lock()
	store, err := readEvergreenCredentialStore()
	evergreenCredentialsLock.Unlock()
	hash := sha256.Sum256([]byte(token))
	id := ""
	if err == nil {
		for _, entry := range store.Agents {
			digest, _ := hex.DecodeString(entry.Digest)
			if subtle.ConstantTimeCompare(digest, hash[:]) == 1 && !entry.Revoked {
				id = entry.AgentID
			}
		}
	}
	if id == "" {
		util.AuthThrottleFail(c.ClientIP())
		c.AbortWithStatus(http.StatusUnauthorized)
		return true
	}
	util.AuthThrottleReset(c.ClientIP())
	c.Set(evergreenIdentityKey, evergreenIdentity{principal: evergreencore.Principal{
		Type: evergreencore.PrincipalAgent, ID: id, AuthSource: "evergreen-agent-credential/v1",
	}})
	c.Set(RoleContextKey, RoleEditor)
	c.Next()
	return true
}

func readEvergreenCredentialStore() (evergreenCredentialStore, error) {
	store := evergreenCredentialStore{Version: 1, Agents: []evergreenAgentCredential{}}
	if util.ConfDir == "" {
		return store, errors.New("credential configuration is unavailable")
	}
	confRoot, err := os.OpenRoot(util.ConfDir)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return store, errors.New("credential configuration is unavailable")
	}
	defer confRoot.Close()
	info, err := confRoot.Lstat(evergreenCredentialDir)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return store, errors.New("Evergreen credential directory is invalid")
	}
	directory, err := confRoot.Open(evergreenCredentialDir)
	if err != nil {
		return store, err
	}
	actual, statErr := directory.Stat()
	private := credentialperm.Valid(directory)
	directory.Close()
	if statErr != nil || !os.SameFile(info, actual) || !private {
		return store, errors.New("Evergreen credential directory permissions are invalid")
	}
	file, err := util.OpenPublishFile(util.ConfDir, evergreenCredentialDir+"/agents.json")
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return store, errors.New("cannot authenticate Evergreen credentials")
	}
	defer file.Close()
	if !credentialperm.Valid(file) {
		return store, errors.New("Evergreen credential permissions are invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, 128*1024+1))
	if err != nil || len(data) > 128*1024 || json.Unmarshal(data, &store) != nil || store.Version != 1 || len(store.Agents) > 256 {
		return store, errors.New("Evergreen credential store is invalid")
	}
	ids, digests := map[string]bool{}, map[string]bool{}
	for _, entry := range store.Agents {
		digest, decodeErr := hex.DecodeString(entry.Digest)
		if !validEvergreenAgentID(entry.AgentID) || decodeErr != nil || len(digest) != sha256.Size ||
			ids[entry.AgentID] || digests[entry.Digest] {
			return store, errors.New("Evergreen credential store is invalid")
		}
		ids[entry.AgentID], digests[entry.Digest] = true, true
	}
	return store, nil
}

func validEvergreenAgentID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, char := range id {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			char == '-' || char == '_' || char == '.') {
			return false
		}
	}
	return true
}

func writeEvergreenCredentialStore(store evergreenCredentialStore) error {
	data, err := json.Marshal(store)
	if err != nil {
		return err
	}
	confRoot, err := os.OpenRoot(util.ConfDir)
	if err != nil {
		return errors.New("credential configuration is unavailable")
	}
	defer confRoot.Close()
	err = confRoot.Mkdir(evergreenCredentialDir, 0700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err == nil {
		if err = credentialperm.Protect(filepath.Join(util.ConfDir, evergreenCredentialDir), true); err != nil {
			return err
		}
	}
	info, err := confRoot.Lstat(evergreenCredentialDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Evergreen credential directory is invalid")
	}
	root, err := confRoot.OpenRoot(evergreenCredentialDir)
	if err != nil {
		return err
	}
	defer root.Close()
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		return errors.New("Evergreen credential directory changed")
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	private := credentialperm.Valid(directory)
	directory.Close()
	if !private {
		return errors.New("Evergreen credential directory permissions are invalid")
	}
	tempBytes := make([]byte, 16)
	if _, err = rand.Read(tempBytes); err != nil {
		return err
	}
	name := ".agents-" + hex.EncodeToString(tempBytes)
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(name)
	if err = credentialperm.Protect(filepath.Join(util.ConfDir, evergreenCredentialDir, name), false); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = root.Rename(name, "agents.json"); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// ManageEvergreenCredentials 仅向已验证的 workspace User 开放；token 只在创建响应中返回一次。
func ManageEvergreenCredentials(c *gin.Context, request apicontract.EvergreenCredentialsRequest) (apicontract.EvergreenCredentialsData, error) {
	principal, err := AuthenticatedEvergreenPrincipal(c)
	if err != nil || principal.Type != evergreencore.PrincipalUser || !IsAdminRoleContext(c) {
		return apicontract.EvergreenCredentialsData{}, evergreenUnauthorized("credential management requires a verified user")
	}
	evergreenCredentialsLock.Lock()
	defer evergreenCredentialsLock.Unlock()
	store, err := readEvergreenCredentialStore()
	if err != nil {
		return apicontract.EvergreenCredentialsData{}, err
	}
	result := apicontract.EvergreenCredentialsData{Agents: []apicontract.EvergreenCredentialInfo{}}
	if request.Action == "list" {
		for _, entry := range store.Agents {
			result.Agents = append(result.Agents, apicontract.EvergreenCredentialInfo{
				AgentID: entry.AgentID, CreatedAt: entry.CreatedAt, Revoked: entry.Revoked,
			})
		}
		return result, nil
	}
	if !request.Confirm || !validEvergreenAgentID(request.AgentID) {
		return result, evergreenUnauthorized("credential mutation requires confirmation and a valid agent ID")
	}
	switch request.Action {
	case "create":
		for _, entry := range store.Agents {
			if entry.AgentID == request.AgentID {
				return result, errors.New("agent identity already exists; use a new identity")
			}
		}
		if len(store.Agents) >= 256 {
			return result, errors.New("Evergreen credential capacity exceeded")
		}
		random := make([]byte, 32)
		if _, err = rand.Read(random); err != nil {
			return result, err
		}
		result.Token = evergreenAgentTokenPrefix + hex.EncodeToString(random)
		digest := sha256.Sum256([]byte(result.Token))
		store.Agents = append(store.Agents, evergreenAgentCredential{
			AgentID: request.AgentID, Digest: hex.EncodeToString(digest[:]), CreatedAt: time.Now().UTC().Format(time.RFC3339),
		})
	case "revoke":
		found := false
		for i := range store.Agents {
			if store.Agents[i].AgentID == request.AgentID {
				store.Agents[i].Revoked, found = true, true
			}
		}
		if !found {
			return result, errors.New("agent identity does not exist")
		}
	default:
		return result, errors.New("unsupported credential action")
	}
	if err = writeEvergreenCredentialStore(store); err != nil {
		return apicontract.EvergreenCredentialsData{}, err
	}
	return result, nil
}
