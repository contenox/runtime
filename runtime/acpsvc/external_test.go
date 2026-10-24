package acpsvc

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	libacp "github.com/contenox/runtime/libacp"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func buildStubAgentBin(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "acp-stub-agent")
	cmd := exec.Command("go", "build", "-o", binPath, "github.com/contenox/runtime/libacp/cmd/acp-stub-agent")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build acp-stub-agent: %v\n%s", err, out)
	}
	return binPath
}

func registerStubAgentInDB(t *testing.T, db libdb.DBManager, name string, env map[string]string) string {
	t.Helper()
	bin := buildStubAgentBin(t)
	svc := agentregistryservice.New(db)
	agent := &runtimetypes.Agent{Name: name, Enabled: true}
	require.NoError(t, agent.SetExternalACPConfig(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
		Command:   bin,
		Env:       env,
	}))
	require.NoError(t, svc.Create(context.Background(), agent))
	return name
}

func registerStubAgent(t *testing.T, h *loopbackHarness, name string) string {
	t.Helper()
	return registerStubAgentInDB(t, h.tr.deps.DB, name, nil)
}

func metaAgent(t *testing.T, meta json.RawMessage) string {
	t.Helper()
	require.NotEmpty(t, meta, "expected _meta with contenox.agent")
	return parseAgentMeta(meta)
}

func TestLoopback_ExternalAgent_NewSessionAndPromptRelays(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgent(t, h, "claude-stub")

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.SessionID)
	require.Equal(t, agentName, metaAgent(t, newResp.Meta),
		"session/new response _meta must echo contenox.agent")
	require.Len(t, newResp.ConfigOptions, 1,
		"a non-advertising external agent surfaces no chain-engine selects — only contenox's own HITL policy select")
	require.Equal(t, configIDHITLPolicy, newResp.ConfigOptions[0].ID,
		"contenox's HITL policy select is the sole config option of a modes-and-config-less external session")

	promptResp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("hello from beam")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason)

	updates := h.lc.drain(t, 2)
	byKind := make(map[libacp.SessionUpdateKind]libacp.SessionNotification, len(updates))
	for _, u := range updates {
		byKind[u.Update.SessionUpdate] = u
	}
	chunk, ok := byKind[libacp.SessionUpdateAgentMessageChunk]
	require.True(t, ok, "the downstream agent's message chunk must be relayed upstream")
	require.Equal(t, newResp.SessionID, chunk.SessionID,
		"a relayed downstream update must be remapped onto the upstream session id")
	require.NotNil(t, chunk.Update.Content)
	require.Equal(t, "ack", chunk.Update.Content.Text)
	require.Contains(t, byKind, libacp.SessionUpdateSessionInfo,
		"an external turn still pushes the post-turn session_info_update")
}

func TestLoopback_ExternalAgent_AcceptsMentionResourceLink(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgent(t, h, "claude-stub-mention")

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	promptResp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt: []libacp.ContentBlock{
			libacp.NewTextContent("review"),
			{Type: string(libacp.ContentKindResourceLink), Name: "main.go", URI: "main.go"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason,
		"an external turn carrying an @-mention resource_link must complete normally")

	updates := h.lc.drain(t, 2)
	var acked bool
	for _, u := range updates {
		if u.Update.SessionUpdate == libacp.SessionUpdateAgentMessageChunk && u.Update.Content != nil {
			require.Equal(t, "ack", u.Update.Content.Text)
			acked = true
		}
	}
	require.True(t, acked, "the downstream agent must complete the turn (no capability rejection)")

	listResp, err := h.client.ListSessions(ctx, libacp.ListSessionsRequest{})
	require.NoError(t, err)
	var found *libacp.SessionInfo
	for i := range listResp.Sessions {
		if listResp.Sessions[i].SessionID == newResp.SessionID {
			found = &listResp.Sessions[i]
			break
		}
	}
	require.NotNil(t, found, "the external session must appear in session/list")
	require.Contains(t, found.Title, "main.go",
		"the @-mention's resource_link reference must be persisted in the external session's history")
}

func TestLoopback_ExternalAgent_UnknownAgentRejected(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	_, err = h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON("no-such-agent"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown")

	h.tr.sessionMu.Lock()
	n := len(h.tr.sessions)
	h.tr.sessionMu.Unlock()
	require.Zero(t, n, "a rejected external agent must not create a session")
}

func TestLoopback_ExternalAgent_DisabledAgentRejected(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	const agentName = "claude-stub-disabled"
	svc := agentregistryservice.New(h.tr.deps.DB)
	agent := &runtimetypes.Agent{Name: agentName, Enabled: false}
	require.NoError(t, agent.SetExternalACPConfig(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
		Command:   "/bin/true",
	}))
	require.NoError(t, svc.Create(ctx, agent))

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	_, err = h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled")
	require.Contains(t, err.Error(), "contenox agent enable",
		"the ACP-level error must name the remedy, matching fleetservice's dispatch-path wording")

	h.tr.sessionMu.Lock()
	n := len(h.tr.sessions)
	h.tr.sessionMu.Unlock()
	require.Zero(t, n, "a refused agent must not create a session")
}

func TestLoopback_ExternalAgent_NoMetaKeyIsNative(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	require.Empty(t, newResp.Meta, "a native session/new carries no contenox.agent _meta")
	require.NotEmpty(t, newResp.ConfigOptions, "a native session still advertises chain config options")

	h.tr.sessionMu.Lock()
	entry := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	require.NotNil(t, entry)
	require.IsType(t, &nativeDriver{}, entry.driver, "a native session must be backed by a native driver")
}

func TestLoopback_ExternalAgent_CloseTearsDownProcess(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgent(t, h, "claude-stub-close")

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	h.tr.sessionMu.Lock()
	entry := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	require.NotNil(t, entry)
	ext, ok := entry.driver.(*externalDriver)
	require.True(t, ok, "an external session must be backed by an external driver")
	ext.mu.Lock()
	handle := ext.handle
	ext.mu.Unlock()
	require.NotNil(t, handle, "an external session must hold a live downstream handle")

	_, err = h.client.CloseSession(ctx, libacp.CloseSessionRequest{SessionID: newResp.SessionID})
	require.NoError(t, err)

	select {
	case <-handle.Conn.Closed():
	case <-time.After(5 * time.Second):
		t.Fatal("downstream connection (spawned process) was not torn down on session/close")
	}
}

func TestLoopback_ExternalAgent_PersistsHistoryForListing(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgent(t, h, "claude-stub-hist")

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	const firstPrompt = "summarize the repo layout"
	_, err = h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent(firstPrompt)},
	})
	require.NoError(t, err)

	listResp, err := h.client.ListSessions(ctx, libacp.ListSessionsRequest{})
	require.NoError(t, err)

	var found *libacp.SessionInfo
	for i := range listResp.Sessions {
		if listResp.Sessions[i].SessionID == newResp.SessionID {
			found = &listResp.Sessions[i]
			break
		}
	}
	require.NotNil(t, found, "the external session must appear in session/list")
	require.Equal(t, firstPrompt, found.Title,
		"session/list title must derive from the first user prompt")
	require.Equal(t, agentName, metaAgent(t, found.Meta),
		"session/list entry must carry contenox.agent attribution in _meta")
}

func TestE2E_Wire_ExternalAgent_CommandMenuAfterNewSessionResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "wire-external.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()

	agentName := registerStubAgentInDB(t, db, "claude-stub-wire",
		map[string]string{"ACP_STUB_ADVERTISE_COMMANDS": "1"})

	agentR, clientW := io.Pipe()
	clientR, agentW := io.Pipe()
	agentSide := &wirePipe{r: agentR, w: agentW}
	clientSide := &wirePipe{r: clientR, w: clientW}

	factory := New(Deps{
		Engine:      &enginesvc.Engine{},
		DB:          db,
		WorkspaceID: "wire-external-ws",
	})
	conn := libacp.NewAgentSideConnection(agentSide, func(c *libacp.AgentSideConnection) libacp.Agent {
		return factory(c)
	})
	runDone := make(chan error, 1)
	go func() { runDone <- conn.Run(ctx) }()
	defer func() {
		_ = clientSide.Close()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Error("connection did not shut down")
		}
	}()

	client := &wireClient{t: t, rw: clientSide}

	resp, _ := client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, notes := client.call(libacp.MethodSessionNew, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Nil(t, resp.Error)
	require.Empty(t, notes,
		"the downstream agent's available_commands_update must NOT precede the external session/new result "+
			"(a client drops updates for a session id it has not yet learned)")
	var newResp libacp.NewSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &newResp))
	require.NotEmpty(t, newResp.SessionID)
	require.Equal(t, agentName, parseAgentMeta(newResp.Meta),
		"external session/new result must echo the contenox.agent attribution")

	after := client.drainNotifications(1)
	require.Equal(t, libacp.MethodSessionUpdate, after[0].Method)
	var cmdNote libacp.SessionNotification
	require.NoError(t, json.Unmarshal(after[0].Params, &cmdNote))
	require.Equal(t, libacp.SessionUpdateAvailableCommands, cmdNote.Update.SessionUpdate,
		"the first notification after the external session/new result must be the relayed downstream command menu")
	require.Equal(t, newResp.SessionID, cmdNote.SessionID,
		"the relayed menu must be remapped onto the upstream session id")
	require.NotEmpty(t, cmdNote.Update.AvailableCommands,
		"the relayed menu must carry the downstream agent's advertised commands")
}

func TestLoopback_ExternalAgent_RelaysDownstreamCommandMenu(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-menu",
		map[string]string{"ACP_STUB_ADVERTISE_COMMANDS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	updates := h.lc.drain(t, 1)
	require.Equal(t, libacp.SessionUpdateAvailableCommands, updates[0].Update.SessionUpdate,
		"the downstream agent's command menu must be relayed to the upstream client")
	require.Equal(t, newResp.SessionID, updates[0].SessionID,
		"a relayed downstream menu must be remapped onto the upstream session id")
	names := make(map[string]bool, len(updates[0].Update.AvailableCommands))
	for _, c := range updates[0].Update.AvailableCommands {
		names[c.Name] = true
	}
	require.True(t, names["review"] && names["explain"],
		"the relayed menu must carry the stub's deterministic advertised commands")
}

func TestLoopback_ExternalAgent_SessionNewCarriesDownstreamConfigOptions(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-cfg",
		map[string]string{"ACP_STUB_ADVERTISE_CONFIG_OPTIONS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.ConfigOptions,
		"an external session/new response must carry the downstream agent's own advertised config options")
	verbosity := optionByID(t, newResp.ConfigOptions, "stub-verbosity")
	require.Equal(t, "select", verbosity.Type)
	require.Equal(t, "low", verbosity.CurrentValue,
		"the downstream agent's option value must be passed through as-is")
	require.True(t, configOptionHasValue(verbosity, "high"))

	h.tr.sessionMu.Lock()
	entry := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	opts := h.tr.sessionConfigOptions(ctx, entry)
	require.Len(t, opts, 2,
		"an external session advertises the downstream options plus contenox's own HITL policy select")
	require.Equal(t, "stub-verbosity", opts[0].ID, "the downstream agent's own option comes first")
	require.Equal(t, configIDHITLPolicy, opts[1].ID,
		"contenox's HITL policy select is appended after the downstream surface")
}

func TestLoopback_ExternalAgent_SetConfigOptionRoundTripsToDownstream(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-cfg-set",
		map[string]string{"ACP_STUB_ADVERTISE_CONFIG_OPTIONS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	setResp, err := h.client.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  "stub-verbosity",
		Value:     libacp.StringConfigValue("high"),
	})
	require.NoError(t, err)
	require.Equal(t, "high", optionByID(t, setResp.ConfigOptions, "stub-verbosity").CurrentValue,
		"the set_config_option response must carry the downstream agent's confirmed value")

	updates := h.lc.drain(t, 1)
	require.Equal(t, libacp.SessionUpdateConfigOption, updates[0].Update.SessionUpdate)
	require.Equal(t, newResp.SessionID, updates[0].SessionID,
		"a relayed downstream config_option_update must be remapped onto the upstream session id")
	require.Equal(t, "high", optionByID(t, updates[0].Update.ConfigOptions, "stub-verbosity").CurrentValue)

	_, err = h.client.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  "stub-verbosity",
		Value:     libacp.StringConfigValue("bogus"),
	})
	require.Error(t, err, "the downstream agent rejects an unknown value and the error surfaces upstream")
}

func TestLoopback_ExternalAgent_LazyRespawnPushesConfigOptions(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-cfg-respawn",
		map[string]string{"ACP_STUB_ADVERTISE_CONFIG_OPTIONS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	h.tr.sessionMu.Lock()
	ext := h.tr.sessions[newResp.SessionID].driver.(*externalDriver)
	h.tr.sessionMu.Unlock()
	require.NoError(t, ext.Close())

	promptResp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("hello again")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason)

	updates := h.lc.drain(t, 3)
	var restored *libacp.SessionNotification
	for i := range updates {
		if updates[i].Update.SessionUpdate == libacp.SessionUpdateConfigOption {
			restored = &updates[i]
			break
		}
	}
	require.NotNil(t, restored, "a lazy respawn must push a config_option_update to restore the pickers")
	require.Equal(t, newResp.SessionID, restored.SessionID)
	require.Equal(t, "low", optionByID(t, restored.Update.ConfigOptions, "stub-verbosity").CurrentValue)
}

func TestE2E_Wire_ExternalAgent_ConfigOptionUpdateAfterNewSessionResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "wire-external-cfg.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()

	agentName := registerStubAgentInDB(t, db, "claude-stub-cfg-wire",
		map[string]string{"ACP_STUB_CONFIG_OPTIONS_AFTER_NEW": "1"})

	agentR, clientW := io.Pipe()
	clientR, agentW := io.Pipe()
	agentSide := &wirePipe{r: agentR, w: agentW}
	clientSide := &wirePipe{r: clientR, w: clientW}

	factory := New(Deps{
		Engine:      &enginesvc.Engine{},
		DB:          db,
		WorkspaceID: "wire-external-cfg-ws",
	})
	conn := libacp.NewAgentSideConnection(agentSide, func(c *libacp.AgentSideConnection) libacp.Agent {
		return factory(c)
	})
	runDone := make(chan error, 1)
	go func() { runDone <- conn.Run(ctx) }()
	defer func() {
		_ = clientSide.Close()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Error("connection did not shut down")
		}
	}()

	client := &wireClient{t: t, rw: clientSide}

	resp, _ := client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, notes := client.call(libacp.MethodSessionNew, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Nil(t, resp.Error)
	require.Empty(t, notes,
		"the downstream agent's config_option_update must NOT precede the external session/new result "+
			"(a client drops updates for a session id it has not yet learned)")
	var newResp libacp.NewSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &newResp))
	require.NotEmpty(t, newResp.SessionID)

	after := client.drainNotifications(1)
	require.Equal(t, libacp.MethodSessionUpdate, after[0].Method)
	var cfgNote libacp.SessionNotification
	require.NoError(t, json.Unmarshal(after[0].Params, &cfgNote))
	require.Equal(t, libacp.SessionUpdateConfigOption, cfgNote.Update.SessionUpdate,
		"the first notification after the external session/new result must be the relayed downstream config options")
	require.Equal(t, newResp.SessionID, cfgNote.SessionID,
		"the relayed config options must be remapped onto the upstream session id")
	require.NotEmpty(t, cfgNote.Update.ConfigOptions,
		"the relayed config_option_update must carry the downstream agent's advertised options")
}

type wireExternalConn struct {
	client   *wireClient
	shutdown func()
}

func dialWireTransport(ctx context.Context, t *testing.T, db libdb.DBManager, workspaceID string) *wireExternalConn {
	t.Helper()
	agentR, clientW := io.Pipe()
	clientR, agentW := io.Pipe()
	agentSide := &wirePipe{r: agentR, w: agentW}
	clientSide := &wirePipe{r: clientR, w: clientW}

	factory := New(Deps{
		Engine:      &enginesvc.Engine{},
		DB:          db,
		WorkspaceID: workspaceID,
	})
	conn := libacp.NewAgentSideConnection(agentSide, func(c *libacp.AgentSideConnection) libacp.Agent {
		return factory(c)
	})
	runDone := make(chan error, 1)
	go func() { runDone <- conn.Run(ctx) }()

	var once sync.Once
	shutdown := func() {
		once.Do(func() {
			_ = clientSide.Close()
			select {
			case <-runDone:
			case <-time.After(3 * time.Second):
				t.Error("connection did not shut down")
			}
		})
	}
	t.Cleanup(shutdown)
	return &wireExternalConn{client: &wireClient{t: t, rw: clientSide}, shutdown: shutdown}
}

func drainForCommandMenu(t *testing.T, c *wireClient) libacp.SessionNotification {
	t.Helper()
	for i := 0; i < 8; i++ {
		note := c.drainNotifications(1)[0]
		require.Equal(t, libacp.MethodSessionUpdate, note.Method)
		var sn libacp.SessionNotification
		require.NoError(t, json.Unmarshal(note.Params, &sn))
		if sn.Update.SessionUpdate == libacp.SessionUpdateAvailableCommands {
			return sn
		}
	}
	t.Fatal("no available_commands_update arrived after the response")
	return libacp.SessionNotification{}
}

func TestE2E_Wire_ExternalAgent_ReloadRestoresMenuAndConfigOptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "wire-external-reload.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	const ws = "wire-external-reload-ws"
	cwd := t.TempDir()
	agentName := registerStubAgentInDB(t, db, "claude-stub-reload", map[string]string{
		"ACP_STUB_ADVERTISE_COMMANDS":       "1",
		"ACP_STUB_ADVERTISE_CONFIG_OPTIONS": "1",
	})

	c1 := dialWireTransport(ctx, t, db, ws)

	resp, _ := c1.client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, notes := c1.client.call(libacp.MethodSessionNew, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Nil(t, resp.Error)
	require.Empty(t, notes, "no update may precede the external session/new result")
	var newResp libacp.NewSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &newResp))
	require.NotEmpty(t, newResp.SessionID)
	require.NotEmpty(t, newResp.ConfigOptions,
		"the external session/new response must carry the downstream config options")

	menu := drainForCommandMenu(t, c1.client)
	require.Equal(t, newResp.SessionID, menu.SessionID)

	resp, _ = c1.client.call(libacp.MethodSessionPrompt, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("hello over the wire")},
	})
	require.Nil(t, resp.Error)

	c1.shutdown()

	c2 := dialWireTransport(ctx, t, db, ws)

	resp, _ = c2.client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, notes = c2.client.call(libacp.MethodSessionLoad, libacp.LoadSessionRequest{
		SessionID:  newResp.SessionID,
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
	})
	require.Nil(t, resp.Error)

	var loadResp libacp.LoadSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &loadResp))
	require.NotEmpty(t, loadResp.ConfigOptions,
		"session/load must restore the downstream config options from persistence, no prompt required")
	require.Equal(t, "stub-verbosity", optionByID(t, loadResp.ConfigOptions, "stub-verbosity").ID)

	for _, n := range notes {
		require.Equal(t, libacp.MethodSessionUpdate, n.Method, "only history replay precedes the load result")
		var sn libacp.SessionNotification
		require.NoError(t, json.Unmarshal(n.Params, &sn))
		require.NotEqual(t, libacp.SessionUpdateAvailableCommands, sn.Update.SessionUpdate,
			"the downstream command menu must NOT precede the load result")
	}

	reloaded := drainForCommandMenu(t, c2.client)
	require.Equal(t, newResp.SessionID, reloaded.SessionID,
		"the re-emitted menu must be remapped onto the upstream session id")
	names := make(map[string]bool, len(reloaded.Update.AvailableCommands))
	for _, cmd := range reloaded.Update.AvailableCommands {
		names[cmd.Name] = true
	}
	require.True(t, names["review"] && names["explain"],
		"the re-emitted menu must carry the downstream agent's advertised commands")
}

func TestLoopback_NativeSession_LoadUnaffectedByReloadPath(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	cwd := t.TempDir()
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: cwd, McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.ConfigOptions, "a native session/new advertises the chain config options")
	h.lc.drain(t, 1)

	loadResp, err := h.client.LoadSession(ctx, libacp.LoadSessionRequest{
		SessionID:  newResp.SessionID,
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	require.NotEmpty(t, loadResp.ConfigOptions,
		"a native session/load must still carry the chain config options (the external reload path must not intercept it)")

	got := h.lc.drain(t, 1)
	require.Equal(t, libacp.SessionUpdateAvailableCommands, got[0].Update.SessionUpdate,
		"a native session/load still emits the contenox slash-command menu")
	require.Equal(t, newResp.SessionID, got[0].SessionID)

	h.tr.sessionMu.Lock()
	entry := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	require.NotNil(t, entry)
	require.IsType(t, &nativeDriver{}, entry.driver, "a native session/load stays backed by a native driver")
}

func TestLoopback_ExternalAgent_SlashPromptPassesThroughVerbatim(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgent(t, h, "claude-stub-passthrough")

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	promptResp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("/help now")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason,
		"a slash prompt on an external session ends via the downstream turn, not a contenox command")

	updates := h.lc.drain(t, 2)
	var acked bool
	for _, u := range updates {
		if u.Update.SessionUpdate == libacp.SessionUpdateAgentMessageChunk && u.Update.Content != nil {
			text := u.Update.Content.Text
			require.Equal(t, "ack", text,
				"the downstream stub's reply must be relayed verbatim, not a contenox command response")
			require.NotContains(t, text, "Available commands",
				"contenox must NOT intercept /help on an external session")
			require.NotContains(t, text, "unknown command",
				"contenox must NOT reject a slash prompt on an external session")
			acked = true
		}
	}
	require.True(t, acked, "the downstream agent's ack must be relayed upstream")
}

func TestLoopback_ExternalAgent_SessionNewCarriesSyntheticModeOption(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-modes",
		map[string]string{"ACP_STUB_ADVERTISE_MODES": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.ConfigOptions,
		"an external session/new response must surface the downstream agent's modes as the synthetic mode select")
	require.Equal(t, AgentModeConfigOptionID, newResp.ConfigOptions[0].ID,
		"the synthetic mode select must lead the config-option set (mode first)")
	mode := optionByID(t, newResp.ConfigOptions, AgentModeConfigOptionID)
	require.Equal(t, "select", mode.Type)
	require.Equal(t, "Mode", mode.Name)
	require.Equal(t, "code", mode.CurrentValue,
		"the synthetic option's currentValue mirrors the downstream currentModeId")
	require.True(t, configOptionHasValue(mode, "code"))
	require.True(t, configOptionHasValue(mode, "ask"),
		"each downstream availableMode must be a selectable value")

	h.tr.sessionMu.Lock()
	entry := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	opts := h.tr.sessionConfigOptions(ctx, entry)
	require.Len(t, opts, 2,
		"a modes-only downstream agent surfaces the synthetic mode select plus contenox's own HITL policy select")
	require.Equal(t, AgentModeConfigOptionID, opts[0].ID, "the synthetic mode select leads")
	require.Equal(t, configIDHITLPolicy, opts[len(opts)-1].ID,
		"contenox's HITL policy select is appended last, after the downstream surface")
}

func TestLoopback_ExternalAgent_SetModeOptionRoundTripsToDownstream(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-modes-set",
		map[string]string{"ACP_STUB_ADVERTISE_MODES": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	setResp, err := h.client.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  AgentModeConfigOptionID,
		Value:     libacp.StringConfigValue("ask"),
	})
	require.NoError(t, err)
	require.Equal(t, "ask", optionByID(t, setResp.ConfigOptions, AgentModeConfigOptionID).CurrentValue,
		"the set_config_option response must carry the downstream agent's confirmed mode")

	updates := h.lc.drain(t, 1)
	require.Equal(t, libacp.SessionUpdateConfigOption, updates[0].Update.SessionUpdate)
	require.Equal(t, newResp.SessionID, updates[0].SessionID,
		"a relayed downstream mode change must be remapped onto the upstream session id")
	require.Equal(t, "ask", optionByID(t, updates[0].Update.ConfigOptions, AgentModeConfigOptionID).CurrentValue)
}

func TestLoopback_ExternalAgent_CurrentModeUpdateRelaysAsConfigOption(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-modes-relay",
		map[string]string{"ACP_STUB_ADVERTISE_MODES": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	_, err = h.client.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  AgentModeConfigOptionID,
		Value:     libacp.StringConfigValue("ask"),
	})
	require.NoError(t, err)

	updates := h.lc.drain(t, 1)
	require.Equal(t, libacp.SessionUpdateConfigOption, updates[0].Update.SessionUpdate,
		"a downstream current_mode_update must surface as a config_option_update, not a raw mode update")
	require.Empty(t, updates[0].Update.CurrentModeID,
		"the translated update carries no raw currentModeId field — the mode rides the synthetic option")
	require.Equal(t, newResp.SessionID, updates[0].SessionID,
		"the translated update must be remapped onto the upstream session id")
	mode := optionByID(t, updates[0].Update.ConfigOptions, AgentModeConfigOptionID)
	require.Equal(t, "ask", mode.CurrentValue,
		"the translated config_option_update must carry the refreshed synthetic mode value")
	require.True(t, configOptionHasValue(mode, "code") && configOptionHasValue(mode, "ask"),
		"the refreshed synthetic option must still list every downstream mode")
}

func TestE2E_Wire_ExternalAgent_ReloadRestoresModePicker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "wire-external-modes-reload.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	const ws = "wire-external-modes-reload-ws"
	cwd := t.TempDir()
	agentName := registerStubAgentInDB(t, db, "claude-stub-modes-reload",
		map[string]string{"ACP_STUB_ADVERTISE_MODES": "1"})

	c1 := dialWireTransport(ctx, t, db, ws)
	resp, _ := c1.client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, notes := c1.client.call(libacp.MethodSessionNew, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Nil(t, resp.Error)
	require.Empty(t, notes, "no update may precede the external session/new result")
	var newResp libacp.NewSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &newResp))
	require.NotEmpty(t, newResp.SessionID)
	require.Equal(t, AgentModeConfigOptionID, optionByID(t, newResp.ConfigOptions, AgentModeConfigOptionID).ID,
		"the external session/new response must carry the synthetic mode picker")

	c1.shutdown()

	c2 := dialWireTransport(ctx, t, db, ws)
	resp, _ = c2.client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, _ = c2.client.call(libacp.MethodSessionLoad, libacp.LoadSessionRequest{
		SessionID:  newResp.SessionID,
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
	})
	require.Nil(t, resp.Error)

	var loadResp libacp.LoadSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &loadResp))
	require.NotEmpty(t, loadResp.ConfigOptions,
		"session/load must restore the synthetic mode picker from persistence, no prompt required")
	mode := optionByID(t, loadResp.ConfigOptions, AgentModeConfigOptionID)
	require.Equal(t, "select", mode.Type)
	require.Equal(t, "code", mode.CurrentValue,
		"the restored mode picker must carry the persisted current mode")
	require.True(t, configOptionHasValue(mode, "code") && configOptionHasValue(mode, "ask"),
		"the restored mode picker must still list every downstream mode")
}

func TestLoopback_NativeSession_NoSyntheticModeOption(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.ConfigOptions, "a native session still advertises chain config options")
	for _, o := range newResp.ConfigOptions {
		require.NotEqual(t, AgentModeConfigOptionID, o.ID,
			"the synthetic downstream-mode option is external-only; a native session must never carry it")
	}
}

func TestLoopback_ExternalAgent_SessionNewCarriesSyntheticModelOption(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-models",
		map[string]string{"ACP_STUB_ADVERTISE_MODELS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.ConfigOptions,
		"an external session/new response must surface the downstream agent's models as the synthetic model select")
	require.Equal(t, AgentModelConfigOptionID, newResp.ConfigOptions[0].ID,
		"the synthetic model select must lead the config-option set (no modes here, so model is first)")
	model := optionByID(t, newResp.ConfigOptions, AgentModelConfigOptionID)
	require.Equal(t, "select", model.Type)
	require.Equal(t, "Model", model.Name)
	require.Equal(t, "stub-model-fast", model.CurrentValue,
		"the synthetic option's currentValue mirrors the downstream currentModelId")
	require.True(t, configOptionHasValue(model, "stub-model-fast"))
	require.True(t, configOptionHasValue(model, "stub-model-smart"),
		"each downstream availableModel must be a selectable value")

	h.tr.sessionMu.Lock()
	entry := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	opts := h.tr.sessionConfigOptions(ctx, entry)
	require.Len(t, opts, 2,
		"a models-only downstream agent surfaces the synthetic model select plus contenox's own HITL policy select")
	require.Equal(t, AgentModelConfigOptionID, opts[0].ID, "the synthetic model select leads")
	require.Equal(t, configIDHITLPolicy, opts[len(opts)-1].ID,
		"contenox's HITL policy select is appended last, after the downstream surface")
}

func TestLoopback_ExternalAgent_SessionNewCarriesModeAndModelInOrder(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-modes-models",
		map[string]string{"ACP_STUB_ADVERTISE_MODES": "1", "ACP_STUB_ADVERTISE_MODELS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	require.Len(t, newResp.ConfigOptions, 3,
		"a modes+models downstream agent surfaces the synthetic mode select, the synthetic model select, and contenox's HITL policy select")
	require.Equal(t, AgentModeConfigOptionID, newResp.ConfigOptions[0].ID,
		"the synthetic mode select leads")
	require.Equal(t, AgentModelConfigOptionID, newResp.ConfigOptions[1].ID,
		"the synthetic model select follows the mode select")
	require.Equal(t, configIDHITLPolicy, newResp.ConfigOptions[2].ID,
		"contenox's HITL policy select is last")
	require.Equal(t, "code", optionByID(t, newResp.ConfigOptions, AgentModeConfigOptionID).CurrentValue)
	require.Equal(t, "stub-model-fast", optionByID(t, newResp.ConfigOptions, AgentModelConfigOptionID).CurrentValue)
}

func TestLoopback_ExternalAgent_SetModelOptionRoundTripsToDownstream(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-models-set",
		map[string]string{"ACP_STUB_ADVERTISE_MODELS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	setResp, err := h.client.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  AgentModelConfigOptionID,
		Value:     libacp.StringConfigValue("stub-model-smart"),
	})
	require.NoError(t, err)
	require.Equal(t, "stub-model-smart", optionByID(t, setResp.ConfigOptions, AgentModelConfigOptionID).CurrentValue,
		"the set_config_option response must carry the downstream agent's confirmed model")
	require.True(t, configOptionHasValue(optionByID(t, setResp.ConfigOptions, AgentModelConfigOptionID), "stub-model-fast"),
		"the refreshed synthetic option must still list every downstream model")
}

func TestE2E_Wire_ExternalAgent_ReloadRestoresModelPicker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "wire-external-models-reload.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	const ws = "wire-external-models-reload-ws"
	cwd := t.TempDir()
	agentName := registerStubAgentInDB(t, db, "claude-stub-models-reload",
		map[string]string{"ACP_STUB_ADVERTISE_MODELS": "1"})

	c1 := dialWireTransport(ctx, t, db, ws)
	resp, _ := c1.client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, notes := c1.client.call(libacp.MethodSessionNew, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Nil(t, resp.Error)
	require.Empty(t, notes, "no update may precede the external session/new result")
	var newResp libacp.NewSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &newResp))
	require.NotEmpty(t, newResp.SessionID)
	require.Equal(t, AgentModelConfigOptionID, optionByID(t, newResp.ConfigOptions, AgentModelConfigOptionID).ID,
		"the external session/new response must carry the synthetic model picker")

	c1.shutdown()

	c2 := dialWireTransport(ctx, t, db, ws)
	resp, _ = c2.client.call(libacp.MethodInitialize, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "wiretest", Version: "0"},
	})
	require.Nil(t, resp.Error)

	resp, _ = c2.client.call(libacp.MethodSessionLoad, libacp.LoadSessionRequest{
		SessionID:  newResp.SessionID,
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
	})
	require.Nil(t, resp.Error)

	var loadResp libacp.LoadSessionResponse
	require.NoError(t, json.Unmarshal(resp.Result, &loadResp))
	require.NotEmpty(t, loadResp.ConfigOptions,
		"session/load must restore the synthetic model picker from persistence, no prompt required")
	model := optionByID(t, loadResp.ConfigOptions, AgentModelConfigOptionID)
	require.Equal(t, "select", model.Type)
	require.Equal(t, "stub-model-fast", model.CurrentValue,
		"the restored model picker must carry the persisted current model")
	require.True(t, configOptionHasValue(model, "stub-model-fast") && configOptionHasValue(model, "stub-model-smart"),
		"the restored model picker must still list every downstream model")
}

func TestLoopback_NativeSession_NoSyntheticModelOption(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.ConfigOptions, "a native session still advertises chain config options")
	for _, o := range newResp.ConfigOptions {
		require.NotEqual(t, AgentModelConfigOptionID, o.ID,
			"the synthetic downstream-model option is external-only; a native session must never carry it")
	}
}

func TestLoopback_ExternalAgent_HITLPolicyPickerRoundTripsNativelyAndPersists(t *testing.T) {
	h := newLoopbackHarness(t)
	h.tr.deps.KnownPolicies = []string{"strict", "dev"}
	h.tr.deps.HITLDefaultPolicyName = "strict"
	ctx := context.Background()
	agentName := registerStubAgentInDB(t, h.tr.deps.DB, "claude-stub-hitl",
		map[string]string{"ACP_STUB_ADVERTISE_CONFIG_OPTIONS": "1"})

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	require.Equal(t, "stub-verbosity", newResp.ConfigOptions[0].ID, "the downstream option comes first")
	require.Equal(t, configIDHITLPolicy, newResp.ConfigOptions[len(newResp.ConfigOptions)-1].ID,
		"contenox's HITL policy select is appended last, after the downstream surface")
	require.Equal(t, hitlPolicyDefaultValue,
		optionByID(t, newResp.ConfigOptions, configIDHITLPolicy).CurrentValue,
		"a fresh external session defaults to the sentinel policy")

	setResp, err := h.client.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  configIDHITLPolicy,
		Value:     libacp.StringConfigValue("dev"),
	})
	require.NoError(t, err)
	require.Equal(t, "dev", optionByID(t, setResp.ConfigOptions, configIDHITLPolicy).CurrentValue,
		"the HITL policy set is reflected in the external session's config options")
	require.Equal(t, "low", optionByID(t, setResp.ConfigOptions, "stub-verbosity").CurrentValue,
		"the downstream option is untouched — the HITL set never went downstream")

	h.tr.sessionMu.Lock()
	entry := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	require.Equal(t, "dev", entry.hitlPolicy(), "the selection is stored on the session")
	require.Equal(t, "dev", h.tr.resolveSessionHITLPolicy(entry),
		"the external session's HITL policy resolves to its own name for gating")

	store := runtimetypes.New(h.tr.deps.DB.WithoutTransaction())
	reloaded := &sessionEntry{HITLPolicy: hitlPolicyDefaultValue, driver: &nativeDriver{t: h.tr}}
	h.tr.markExternalIfPersisted(ctx, store, newResp.SessionID, reloaded)
	require.IsType(t, &externalDriver{}, reloaded.driver, "the reloaded entry is re-flagged external")
	require.Equal(t, "dev", reloaded.hitlPolicy(), "the per-session HITL policy survives a reload")
	reloadedOpts := h.tr.reloadedConfigOptions(ctx, store, newResp.SessionID, reloaded)
	require.Equal(t, configIDHITLPolicy, reloadedOpts[len(reloadedOpts)-1].ID,
		"the reloaded external session re-advertises the HITL policy picker after the downstream surface")
	require.Equal(t, "dev", optionByID(t, reloadedOpts, configIDHITLPolicy).CurrentValue,
		"the reloaded picker shows the previously-chosen value, not the sentinel default")
}

func TestLoopback_NativeSession_PolicySlashCommandStillWorks(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
	})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	promptResp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("/policy dev")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason,
		"a native /policy command resolves as an ended turn, not a downstream prompt")

	updates := h.lc.drain(t, 2)
	var confirmed bool
	for _, u := range updates {
		if u.Update.SessionUpdate == libacp.SessionUpdateAgentMessageChunk && u.Update.Content != nil {
			require.Contains(t, u.Update.Content.Text, "HITL policy set to dev",
				"the native /policy switch confirms inline")
			confirmed = true
		}
	}
	require.True(t, confirmed, "the /policy confirmation must reach the client")

	require.Equal(t, "dev", clikv.ReadHITLPolicy(ctx, runtimetypes.New(h.tr.deps.DB.WithoutTransaction())),
		"native /policy still writes the global cli.hitl-policy-name KV")
}
