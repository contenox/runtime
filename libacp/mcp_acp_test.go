package libacp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/contenox/runtime/libacp"
	"github.com/stretchr/testify/require"
)

type mcpClient struct {
	libacp.UnimplementedClient
	request libacp.MessageMCPRequest
}

func (c *mcpClient) MessageMCP(_ context.Context, request libacp.MessageMCPRequest) (libacp.MessageMCPResponse, error) {
	c.request = request
	return libacp.MessageMCPResponse{Result: json.RawMessage(`{"tools":[{"name":"lookup"}]}`)}, nil
}

func TestUnit_MCPACPServer_WireAndValidation(t *testing.T) {
	server := libacp.McpServer{Type: "acp", Name: "theia", ServerID: "theia-mcp:theia"}
	require.NoError(t, server.Validate())
	require.Equal(t, libacp.McpServerKindACP, server.Kind())

	raw, err := json.Marshal(server)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"acp","name":"theia","serverId":"theia-mcp:theia"}`, string(raw))

	server.ServerID = ""
	require.Error(t, server.Validate())
}

func TestUnit_MCPMessageResponse_RequiresOneOutcome(t *testing.T) {
	require.NoError(t, (libacp.MessageMCPResponse{Result: json.RawMessage(`null`)}).Validate())
	require.NoError(t, (libacp.MessageMCPResponse{Error: &libacp.MCPError{Code: -32601, Message: "missing"}}).Validate())
	require.Error(t, (libacp.MessageMCPResponse{}).Validate())
	require.Error(t, (libacp.MessageMCPResponse{Result: json.RawMessage(`{}`), Error: &libacp.MCPError{Message: "both"}}).Validate())
}

func TestUnit_MCPMessage_RoundTripsAcrossACPConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &mcpClient{}
	agentConn, _, cleanup := wireUpTestConnections(t, ctx, &testAgent{}, client)
	defer cleanup()

	request := libacp.MessageMCPRequest{
		ServerID:  "theia-mcp:docs",
		RequestID: "request-1",
		Method:    "tools/list",
		Params:    json.RawMessage(`{}`),
	}
	response, err := agentConn.MessageMCP(ctx, request)
	require.NoError(t, err)
	require.Equal(t, request, client.request)
	require.JSONEq(t, `{"tools":[{"name":"lookup"}]}`, string(response.Result))
}
