package agentdecl_test

import (
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func parseDecl(t *testing.T, body string) (*agentdecl.AgentIR, error) {
	t.Helper()
	return agentdecl.ParseClaudeCode("agent.md", []byte(body), mustConfig(t))
}

const declHeader = `---
name: reviewer
description: Reviews things
tools: Read
`

func TestUnit_MCPServersListIsAGrant(t *testing.T) {
	t.Parallel()
	ir, err := parseDecl(t, declHeader+`mcpServers: [github, linear]
---
Body.
`)
	require.NoError(t, err)
	require.Equal(t, []string{"github", "linear"}, ir.MCPServers)
	require.Empty(t, ir.DeclaredMCP, "a name list defines nothing")
}

func TestUnit_MCPServersMapIsADefinition(t *testing.T) {
	t.Parallel()
	ir, err := parseDecl(t, declHeader+`mcpServers:
  filesystem:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/data"]
  linear:
    type: http
    url: https://mcp.linear.app/mcp
---
Body.
`)
	require.NoError(t, err)
	require.Empty(t, ir.MCPServers, "definitions are not also grants")
	require.Len(t, ir.DeclaredMCP, 2)

	require.Equal(t, "filesystem", ir.DeclaredMCP[0].Declared)
	require.Equal(t, "stdio", ir.DeclaredMCP[0].Transport, "a command implies stdio")
	require.Equal(t, []string{"-y", "@modelcontextprotocol/server-filesystem", "/data"}, ir.DeclaredMCP[0].Args)

	require.Equal(t, "linear", ir.DeclaredMCP[1].Declared)
	require.Equal(t, "http", ir.DeclaredMCP[1].Transport)
	require.Equal(t, "https://mcp.linear.app/mcp", ir.DeclaredMCP[1].URL)
}

func TestUnit_MCPServerRefusesALiteralCredential(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"authToken", "token", "password", "secret"} {
		t.Run(key, func(t *testing.T) {
			_, err := parseDecl(t, declHeader+`mcpServers:
  linear:
    url: https://mcp.linear.app/mcp
    `+key+`: sk-live-abc123
---
Body.
`)
			require.Error(t, err)
			require.Contains(t, err.Error(), "authEnvKey", "the refusal names the supported alternative")
			require.NotContains(t, err.Error(), "sk-live-abc123", "the refusal must not echo the credential")
		})
	}
}

func TestUnit_MCPServerRefusesAnIncompleteTransport(t *testing.T) {
	t.Parallel()
	_, err := parseDecl(t, declHeader+`mcpServers:
  broken:
    type: http
---
Body.
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no url")
}

func TestUnit_RemoteToolsAreParsed(t *testing.T) {
	t.Parallel()
	ir, err := parseDecl(t, declHeader+`remoteTools:
  billing:
    url: https://internal.example.com
    spec: https://internal.example.com/openapi.json
    timeoutMs: 8000
---
Body.
`)
	require.NoError(t, err)
	require.Len(t, ir.DeclaredRemote, 1)
	require.Equal(t, "billing", ir.DeclaredRemote[0].Declared)
	require.Equal(t, "https://internal.example.com", ir.DeclaredRemote[0].EndpointURL)
	require.Equal(t, "https://internal.example.com/openapi.json", ir.DeclaredRemote[0].SpecURL)
	require.Equal(t, 8000, ir.DeclaredRemote[0].TimeoutMs)
}

func TestUnit_RemoteToolNeedsSomewhereToCall(t *testing.T) {
	t.Parallel()
	_, err := parseDecl(t, declHeader+`remoteTools:
  billing:
    spec: https://internal.example.com/openapi.json
---
Body.
`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no url")
}

func TestUnit_DeclarationWithoutSourcesIsUnchanged(t *testing.T) {
	t.Parallel()
	ir, err := parseDecl(t, declHeader+`---
Body.
`)
	require.NoError(t, err)
	require.Empty(t, ir.MCPServers)
	require.Empty(t, ir.DeclaredMCP)
	require.Empty(t, ir.DeclaredRemote)
	require.Empty(t, ir.DeclaredToolsetNames("reviewer"))
}

func TestUnit_EmittedChainNamesDeclaredSources(t *testing.T) {
	t.Parallel()
	cfg := mustConfig(t)
	ir, err := agentdecl.ParseClaudeCode("reviewer.md", []byte(`---
name: reviewer
description: Reviews things
mcpServers:
  filesystem:
    command: npx
    args: ["-y", "server"]
remoteTools:
  billing:
    url: https://internal.example.com
---
Body.
`), cfg)
	require.NoError(t, err)

	chain, err := agentdecl.EmitChain(ir, cfg)
	require.NoError(t, err)

	var tools []string
	for _, task := range chain.Tasks {
		if task.ExecuteConfig != nil && len(task.ExecuteConfig.Tools) > 0 {
			tools = task.ExecuteConfig.Tools
			break
		}
	}
	agentID := ir.ScopedName(cfg.Naming.ScopeWithDialect)
	wantMCP := runtimetypes.DeclaredToolName(agentID, "filesystem")
	wantRemote := runtimetypes.DeclaredToolName(agentID, "billing")
	require.Contains(t, tools, wantMCP, "declared MCP server must be named exactly: %v", tools)
	require.Contains(t, tools, wantRemote, "declared remote tool must be named exactly: %v", tools)
	require.Contains(t, tools, "*", "omitting tools still inherits the built-ins")

	for _, name := range tools {
		require.False(t, strings.HasPrefix(name, "decl-") && name != wantMCP && name != wantRemote,
			"only this agent's own sources may appear: %q", name)
	}
}

func TestUnit_GrantedMCPServersReachTheChain(t *testing.T) {
	t.Parallel()
	cfg := mustConfig(t)
	ir, err := agentdecl.ParseClaudeCode("reviewer.md", []byte(`---
name: reviewer
description: Reviews things
tools: Read
mcpServers: [tavily]
---
Body.
`), cfg)
	require.NoError(t, err)

	chain, err := agentdecl.EmitChain(ir, cfg)
	require.NoError(t, err)
	for _, task := range chain.Tasks {
		if task.ExecuteConfig != nil && len(task.ExecuteConfig.Tools) > 0 {
			require.Contains(t, task.ExecuteConfig.Tools, "tavily")
			return
		}
	}
	t.Fatal("no task exposed any toolset")
}
