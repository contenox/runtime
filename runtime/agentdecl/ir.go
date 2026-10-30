package agentdecl

import (
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/runtimetypes"
)

type Dialect string

const (
	DialectClaudeCode  Dialect = "claude-code"
	DialectAntigravity Dialect = "antigravity"
	DialectCopilot     Dialect = "copilot"
	DialectOpenCode    Dialect = "opencode"
	DialectCursor      Dialect = "cursor"
)

type Source struct {
	Dialect  Dialect `json:"dialect"`
	SpecDate string  `json:"spec_date"`
	Path     string  `json:"path"`
	SHA256   string  `json:"sha256"`
}

type Model struct {
	Raw      string `json:"raw"`
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
}

type Tools struct {
	Allow   []string `json:"allow,omitempty"`
	Deny    []string `json:"deny,omitempty"`
	Inherit bool     `json:"inherit,omitempty"`
}

type Role string

const (
	RolePrimary Role = "primary"
	RoleMission Role = "mission"
	RoleBoth    Role = "both"
)

const RoleSubagent = RoleMission

type Posture string

const (
	PostureReadOnly  Posture = "read_only"
	PostureAskAlways Posture = "ask_always"
	PostureAutoEdit  Posture = "auto_edit"
	PostureUnsafe    Posture = "unsafe"
)

func ParsePosture(raw string) (Posture, error) {
	switch Posture(strings.ToLower(strings.TrimSpace(raw))) {
	case PostureReadOnly:
		return PostureReadOnly, nil
	case PostureAskAlways:
		return PostureAskAlways, nil
	case PostureAutoEdit:
		return PostureAutoEdit, nil
	}
	return "", fmt.Errorf("unknown posture %q: use %s, %s or %s", raw,
		PostureReadOnly, PostureAskAlways, PostureAutoEdit)
}

type Budgets struct {
	MaxTurns *int `json:"max_turns,omitempty"`
}

type Unmapped struct {
	Field  string `json:"field"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason"`
}

type DeclaredMCPServer struct {
	Declared   string            `json:"declared"`
	Transport  string            `json:"transport"`
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	URL        string            `json:"url,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	AuthEnvKey string            `json:"auth_env_key,omitempty"`
	AuthType   string            `json:"auth_type,omitempty"`
}

type DeclaredRemoteTool struct {
	Declared    string            `json:"declared"`
	EndpointURL string            `json:"endpoint_url"`
	SpecURL     string            `json:"spec_url,omitempty"`
	TimeoutMs   int               `json:"timeout_ms,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

type AgentIR struct {
	Source         Source               `json:"source"`
	Name           string               `json:"name"`
	Description    string               `json:"description"`
	SystemPrompt   string               `json:"system_prompt"`
	Model          Model                `json:"model"`
	Tools          Tools                `json:"tools"`
	MCPServers     []string             `json:"mcp_servers,omitempty"`
	DeclaredMCP    []DeclaredMCPServer  `json:"declared_mcp,omitempty"`
	DeclaredRemote []DeclaredRemoteTool `json:"declared_remote,omitempty"`
	Role           Role                 `json:"role"`
	Posture        Posture              `json:"posture"`
	Category       string               `json:"category,omitempty"`
	Icon           string               `json:"icon,omitempty"`
	Think          string               `json:"think,omitempty"`
	DefaultBranch  string               `json:"default_branch,omitempty"`
	Temperature    *float32             `json:"temperature,omitempty"`
	Budgets        Budgets              `json:"budgets"`
	Unmapped       []Unmapped           `json:"unmapped,omitempty"`
}

func (ir *AgentIR) DeclaredToolsetNames(agentID string) []string {
	names := make([]string, 0, len(ir.DeclaredMCP)+len(ir.DeclaredRemote))
	for _, srv := range ir.DeclaredMCP {
		names = append(names, runtimetypes.DeclaredToolName(agentID, srv.Declared))
	}
	for _, tool := range ir.DeclaredRemote {
		names = append(names, runtimetypes.DeclaredToolName(agentID, tool.Declared))
	}
	return names
}

func (ir *AgentIR) AddUnmapped(field, value, reason string) {
	ir.Unmapped = append(ir.Unmapped, Unmapped{Field: field, Value: value, Reason: reason})
}

func (ir *AgentIR) ScopedName(scopeWithDialect bool) string {
	if !scopeWithDialect {
		return ir.Name
	}
	return string(ir.Source.Dialect) + "-" + ir.Name
}
