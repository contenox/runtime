package libacp

import (
	"encoding/json"
	"fmt"
)

type EnvVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HttpHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type McpServerKind string

const (
	McpServerKindStdio McpServerKind = ""
	McpServerKindHTTP  McpServerKind = "http"
	McpServerKindSSE   McpServerKind = "sse"
	McpServerKindACP   McpServerKind = "acp"
)

type McpServer struct {
	Type     string          `json:"type,omitempty"`
	Name     string          `json:"name"`
	Command  string          `json:"command,omitempty"`
	Args     []string        `json:"args,omitempty"`
	Env      []EnvVariable   `json:"env,omitempty"`
	URL      string          `json:"url,omitempty"`
	Headers  []HttpHeader    `json:"headers,omitempty"`
	ServerID string          `json:"serverId,omitempty"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

func (m McpServer) Kind() McpServerKind {
	switch m.Type {
	case "http":
		return McpServerKindHTTP
	case "sse":
		return McpServerKindSSE
	case "acp":
		return McpServerKindACP
	default:
		return McpServerKindStdio
	}
}

type mcpServerHttpWire struct {
	Type    string          `json:"type,omitempty"`
	Name    string          `json:"name"`
	URL     string          `json:"url,omitempty"`
	Headers []HttpHeader    `json:"headers"`
	Meta    json.RawMessage `json:"_meta,omitempty"`
}

type mcpServerStdioWire struct {
	Name    string          `json:"name"`
	Command string          `json:"command,omitempty"`
	Args    []string        `json:"args"`
	Env     []EnvVariable   `json:"env"`
	Meta    json.RawMessage `json:"_meta,omitempty"`
}

type mcpServerACPWire struct {
	Type     string          `json:"type"`
	Name     string          `json:"name"`
	ServerID string          `json:"serverId"`
	Meta     json.RawMessage `json:"_meta,omitempty"`
}

func (m McpServer) MarshalJSON() ([]byte, error) {
	switch m.Kind() {
	case McpServerKindACP:
		return json.Marshal(mcpServerACPWire{Type: "acp", Name: m.Name, ServerID: m.ServerID, Meta: m.Meta})
	case McpServerKindHTTP, McpServerKindSSE:
		headers := m.Headers
		if headers == nil {
			headers = []HttpHeader{}
		}
		return json.Marshal(mcpServerHttpWire{
			Type:    m.Type,
			Name:    m.Name,
			URL:     m.URL,
			Headers: headers,
			Meta:    m.Meta,
		})
	default:
		args, env := m.Args, m.Env
		if args == nil {
			args = []string{}
		}
		if env == nil {
			env = []EnvVariable{}
		}
		return json.Marshal(mcpServerStdioWire{
			Name:    m.Name,
			Command: m.Command,
			Args:    args,
			Env:     env,
			Meta:    m.Meta,
		})
	}
}

func (m McpServer) Validate() error {
	switch m.Kind() {
	case McpServerKindACP:
		if m.ServerID == "" {
			return fmt.Errorf("libacp: acp mcp server %q missing serverId", m.Name)
		}
	case McpServerKindStdio:
		if m.Command == "" {
			return fmt.Errorf("libacp: stdio mcp server %q missing command", m.Name)
		}
	case McpServerKindHTTP, McpServerKindSSE:
		if m.URL == "" {
			return fmt.Errorf("libacp: %s mcp server %q missing url", m.Type, m.Name)
		}
	}
	return nil
}

type MessageMCPRequest struct {
	ServerID  string          `json:"serverId"`
	RequestID string          `json:"requestId"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	Meta      json.RawMessage `json:"_meta,omitempty"`
}

type MCPError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type MessageMCPResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *MCPError       `json:"error,omitempty"`
	Meta   json.RawMessage `json:"_meta,omitempty"`
}

func (r MessageMCPResponse) Validate() error {
	hasResult := r.Result != nil
	hasError := r.Error != nil
	if hasResult == hasError {
		return fmt.Errorf("libacp: mcp/message response must contain exactly one of result or error")
	}
	if hasError && r.Error.Message == "" {
		return fmt.Errorf("libacp: mcp/message error missing message")
	}
	return nil
}
