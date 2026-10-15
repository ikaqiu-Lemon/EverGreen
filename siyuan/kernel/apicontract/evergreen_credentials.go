package apicontract

// 凭据管理要求已验证的 User、管理员角色及可写 workspace；Agent token 仅适用于已注册的 Evergreen 路由。
// create/revoke 要求 confirm=true；agent_id 不可重用。list 永不返回 digest/token，create 只返回一次 token。
// Authorization: Token eg-agent-v1.* 比 JWT、主 token 和本机放行优先判定；caller header 不改变已认证身份。
type EvergreenCredentialsRequest struct {
	Action  string `json:"action" api:"enum=list|create|revoke"`
	AgentID string `json:"agent_id,omitempty" api:"optional"`
	Confirm bool   `json:"confirm,omitempty" api:"optional"`
}

type EvergreenCredentialInfo struct {
	AgentID   string `json:"agent_id"`
	CreatedAt string `json:"created_at"`
	Revoked   bool   `json:"revoked"`
}

type EvergreenCredentialsData struct {
	Agents []EvergreenCredentialInfo `json:"agents" api:"nonnullable"`
	Token  string                    `json:"token,omitempty" api:"optional"`
}

type EvergreenCredentialsEnvelope struct {
	OK          bool                     `json:"ok"`
	Data        EvergreenCredentialsData `json:"data"`
	Diagnostics []EvergreenDiagnostic    `json:"diagnostics,omitempty" api:"optional,nonnullable"`
}
