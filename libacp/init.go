package libacp

import "encoding/json"

type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type FileSystemCapabilities struct {
	ReadTextFile  bool            `json:"readTextFile,omitempty"`
	WriteTextFile bool            `json:"writeTextFile,omitempty"`
	Meta          json.RawMessage `json:"_meta,omitempty"`
}

type AuthCapabilities struct {
	Terminal bool `json:"terminal,omitempty"`
}

type ClientSessionCapabilities struct {
	ConfigOptions *SessionConfigOptionsCapabilities `json:"configOptions,omitempty"`
	Meta          json.RawMessage                   `json:"_meta,omitempty"`
}

type SessionConfigOptionsCapabilities struct {
	Boolean *struct{}       `json:"boolean,omitempty"`
	Meta    json.RawMessage `json:"_meta,omitempty"`
}

type ClientCapabilities struct {
	FS       FileSystemCapabilities     `json:"fs,omitempty"`
	Terminal bool                       `json:"terminal,omitempty"`
	Session  *ClientSessionCapabilities `json:"session,omitempty"`
	Auth     AuthCapabilities           `json:"auth,omitempty"`
	Meta     json.RawMessage            `json:"_meta,omitempty"`
}

func (c ClientCapabilities) SupportsBooleanConfigOptions() bool {
	return c.Session != nil && c.Session.ConfigOptions != nil && c.Session.ConfigOptions.Boolean != nil
}

type PromptCapabilities struct {
	Image           bool            `json:"image,omitempty"`
	Audio           bool            `json:"audio,omitempty"`
	EmbeddedContext bool            `json:"embeddedContext,omitempty"`
	Meta            json.RawMessage `json:"_meta,omitempty"`
}

type McpCapabilities struct {
	HTTP bool            `json:"http,omitempty"`
	SSE  bool            `json:"sse,omitempty"`
	ACP  bool            `json:"acp,omitempty"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type SessionCapabilities struct {
	List                  *struct{}       `json:"list,omitempty"`
	Resume                *struct{}       `json:"resume,omitempty"`
	Close                 *struct{}       `json:"close,omitempty"`
	Delete                *struct{}       `json:"delete,omitempty"`
	AdditionalDirectories *struct{}       `json:"additionalDirectories,omitempty"`
	Meta                  json.RawMessage `json:"_meta,omitempty"`
}

type AgentAuthCapabilities struct {
	Logout *LogoutCapabilities `json:"logout,omitempty"`
	Meta   json.RawMessage     `json:"_meta,omitempty"`
}

type LogoutCapabilities struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type AgentCapabilities struct {
	LoadSession         bool                  `json:"loadSession,omitempty"`
	PromptCapabilities  PromptCapabilities    `json:"promptCapabilities,omitempty"`
	McpCapabilities     McpCapabilities       `json:"mcpCapabilities,omitempty"`
	SessionCapabilities SessionCapabilities   `json:"sessionCapabilities,omitempty"`
	Auth                AgentAuthCapabilities `json:"auth,omitempty"`
	Meta                json.RawMessage       `json:"_meta,omitempty"`
}

type AuthMethod struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Vars        []AuthEnvVar      `json:"vars,omitempty"`
	Link        string            `json:"link,omitempty"`
	Meta        json.RawMessage   `json:"_meta,omitempty"`
}

const (
	AuthMethodTypeTerminal = "terminal"
	AuthMethodTypeEnvVar   = "env_var"
)

type AuthEnvVar struct {
	Name     string          `json:"name"`
	Label    string          `json:"label,omitempty"`
	Secret   *bool           `json:"secret,omitempty"`
	Optional bool            `json:"optional,omitempty"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
	Meta               json.RawMessage    `json:"_meta,omitempty"`
}

type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities,omitempty"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
	AuthMethods       []AuthMethod      `json:"authMethods,omitempty"`
	Meta              json.RawMessage   `json:"_meta,omitempty"`
}

type AuthenticateRequest struct {
	MethodID string          `json:"methodId"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

type AuthenticateResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type LogoutRequest struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type LogoutResponse struct {
	Meta json.RawMessage `json:"_meta,omitempty"`
}
