package acpsvc

import (
	"context"
	"path/filepath"
	"testing"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func registerChainAgentInDB(t *testing.T, db libdb.DBManager, name string) string {
	t.Helper()
	svc := agentregistryservice.New(db)
	agent := &runtimetypes.Agent{Name: name, Enabled: true}
	require.NoError(t, agent.SetChainConfig(runtimetypes.ChainConfig{
		Path:    filepath.Join(t.TempDir(), "agent-chat-fixture.json"),
		ChainID: "agent-chat-fixture",
	}))
	require.NoError(t, svc.Create(context.Background(), agent))
	return name
}

func TestLoopback_ChainAgent_ChatPathOpensAndPrompts(t *testing.T) {
	stub := buildStubAgentBin(t)
	f := newInstancesFixtureWith(t, func(db libdb.DBManager) agentinstance.Manager {
		return agentinstance.New(agentregistryservice.New(db), agentinstance.WithSelfExecutable(stub))
	})
	agentName := registerChainAgentInDB(t, f.db, "chain-chat-fixture")

	conn := f.connect()
	ctx := context.Background()
	_, err := conn.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := conn.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err, "a chain agent offered in the picker must be usable in chat")
	require.Equal(t, agentName, metaAgent(t, newResp.Meta))

	promptResp, err := conn.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("ping")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason)
}

func TestLoopback_ChainAgent_ConnCtxPathRefusesHonestly(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	require.Nil(t, h.tr.deps.Instances, "this harness is the nil-Instances path by construction")

	agentName := registerChainAgentInDB(t, h.tr.deps.DB, "chain-stdio-fixture")

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	_, err = h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "chain agent")
	require.Contains(t, err.Error(), "contenox serve",
		"the refusal must name where a chain unit CAN run, not just that this cannot run it")

	h.tr.sessionMu.Lock()
	n := len(h.tr.sessions)
	h.tr.sessionMu.Unlock()
	require.Zero(t, n, "a refused agent must not create a session")
}
