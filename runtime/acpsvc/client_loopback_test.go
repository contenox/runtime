package acpsvc

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/approvalflow"
	"github.com/contenox/runtime/runtime/chatservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/hitlservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type loopbackClient struct {
	libacp.UnimplementedClient

	updates chan libacp.SessionNotification

	mu    sync.Mutex
	files map[string]string

	permMu   sync.Mutex
	permReqs []libacp.RequestPermissionRequest
	permResp libacp.RequestPermissionResponse
}

func newLoopbackClient() *loopbackClient {
	return &loopbackClient{
		updates: make(chan libacp.SessionNotification, 256),
		files:   make(map[string]string),
		permResp: libacp.RequestPermissionResponse{
			Outcome: libacp.RequestPermissionOutcome{
				Outcome:  libacp.PermissionOutcomeSelected,
				OptionID: approvalflow.OptionAllow,
			},
		},
	}
}

func (c *loopbackClient) SessionUpdate(_ context.Context, n libacp.SessionNotification) error {
	c.updates <- n
	return nil
}

func (c *loopbackClient) RequestPermission(_ context.Context, req libacp.RequestPermissionRequest) (libacp.RequestPermissionResponse, error) {
	c.permMu.Lock()
	c.permReqs = append(c.permReqs, req)
	resp := c.permResp
	c.permMu.Unlock()
	return resp, nil
}

func (c *loopbackClient) setPermissionResponse(resp libacp.RequestPermissionResponse) {
	c.permMu.Lock()
	c.permResp = resp
	c.permMu.Unlock()
}

func (c *loopbackClient) lastPermissionRequest() (libacp.RequestPermissionRequest, bool) {
	c.permMu.Lock()
	defer c.permMu.Unlock()
	if len(c.permReqs) == 0 {
		return libacp.RequestPermissionRequest{}, false
	}
	return c.permReqs[len(c.permReqs)-1], true
}

func (c *loopbackClient) ReadTextFile(_ context.Context, req libacp.ReadTextFileRequest) (libacp.ReadTextFileResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	content, ok := c.files[req.Path]
	if !ok {
		return libacp.ReadTextFileResponse{}, &libacp.Error{Code: libacp.ErrResourceNotFound, Message: "no such file: " + req.Path}
	}
	return libacp.ReadTextFileResponse{Content: content}, nil
}

func (c *loopbackClient) WriteTextFile(_ context.Context, req libacp.WriteTextFileRequest) (libacp.WriteTextFileResponse, error) {
	c.mu.Lock()
	c.files[req.Path] = req.Content
	c.mu.Unlock()
	return libacp.WriteTextFileResponse{}, nil
}

func (c *loopbackClient) drain(t *testing.T, n int) []libacp.SessionNotification {
	t.Helper()
	got := make([]libacp.SessionNotification, 0, n)
	deadline := time.After(5 * time.Second)
	for len(got) < n {
		select {
		case note := <-c.updates:
			got = append(got, note)
		case <-deadline:
			t.Fatalf("timed out waiting for %d session/update notifications (got %d: %+v)", n, len(got), got)
		}
	}
	return got
}

type loopbackAgent struct {
	promptFunc func(ctx context.Context, req agentservice.PromptRequest) (*agentservice.PromptResponse, error)
}

func (a *loopbackAgent) Capabilities(context.Context) (*agentservice.AgentCapabilities, error) {
	return nil, nil
}
func (a *loopbackAgent) SessionNew(context.Context, string) (string, error) { return "", nil }
func (a *loopbackAgent) SessionList(context.Context) ([]*agentservice.SessionInfo, error) {
	return nil, nil
}
func (a *loopbackAgent) SessionLoad(context.Context, string) (string, []taskengine.Message, error) {
	return "", nil, nil
}
func (a *loopbackAgent) SessionResume(context.Context, string) (string, error) { return "", nil }
func (a *loopbackAgent) SessionDelete(context.Context, string) error           { return nil }
func (a *loopbackAgent) SessionEnsureDefault(context.Context) (string, error)  { return "", nil }
func (a *loopbackAgent) Prompt(ctx context.Context, req agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
	return a.promptFunc(ctx, req)
}

var _ agentservice.Agent = (*loopbackAgent)(nil)

type loopbackHarness struct {
	t      *testing.T
	tr     *Transport
	client *libacp.ClientSideConnection
	lc     *loopbackClient
	bus    libbus.Messenger
	router *SessionRouter
}

func newLoopbackHarness(t *testing.T) *loopbackHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "loopback.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)

	agentR, clientW := io.Pipe()
	clientR, agentW := io.Pipe()
	agentSide := &wirePipe{r: agentR, w: agentW}
	clientSide := &wirePipe{r: clientR, w: clientW}

	bus := libbus.NewSQLiteWithOptions(db.WithoutTransaction(), libbus.SQLiteBusOptions{
		EventPoll:   5 * time.Millisecond,
		RequestPoll: 5 * time.Millisecond,
	})
	router := NewSessionRouter()
	factory := New(Deps{
		Engine:        &enginesvc.Engine{Bus: bus},
		DB:            db,
		ChainRegistry: &ChainRegistry{defaultChain: &taskengine.TaskChainDefinition{}},
		WorkspaceID:   "loopback-ws",
		SessionRouter: router,
	})

	var tr *Transport
	agentConn := libacp.NewAgentSideConnection(agentSide, func(c *libacp.AgentSideConnection) libacp.Agent {
		a := factory(c)
		tr = a.(*Transport)
		return a
	})

	lc := newLoopbackClient()
	clientConn := libacp.NewClientSideConnection(clientSide, func(*libacp.ClientSideConnection) libacp.Client {
		return lc
	})

	agentDone := make(chan error, 1)
	clientDone := make(chan error, 1)
	go func() { agentDone <- agentConn.Run(ctx) }()
	go func() { clientDone <- clientConn.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case <-agentDone:
		case <-time.After(2 * time.Second):
			t.Error("agent connection did not shut down")
		}
		select {
		case <-clientDone:
		case <-time.After(2 * time.Second):
			t.Error("client connection did not shut down")
		}
		require.NoError(t, bus.Close())
		require.NoError(t, db.Close())
	})

	return &loopbackHarness{t: t, tr: tr, client: clientConn, lc: lc, bus: bus, router: router}
}

func (h *loopbackHarness) swapAgent(sid libacp.SessionID, a agentservice.Agent) {
	h.tr.sessionMu.Lock()
	h.tr.sessions[sid].driver.(*nativeDriver).agent = a
	h.tr.sessionMu.Unlock()
}

func TestLoopback_InitializeAdvertisesSpecCapabilities(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	resp, err := h.client.Initialize(ctx, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientInfo:      &libacp.Implementation{Name: "loopback-test", Version: "0"},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.ProtocolVersion, resp.ProtocolVersion)
	require.NotNil(t, resp.AgentCapabilities.SessionCapabilities.List)
	require.NotNil(t, resp.AgentCapabilities.SessionCapabilities.Resume)
	require.NotNil(t, resp.AgentCapabilities.SessionCapabilities.Close)
	require.NotNil(t, resp.AgentCapabilities.SessionCapabilities.Delete)
	require.Nil(t, resp.AgentCapabilities.SessionCapabilities.AdditionalDirectories,
		"acpsvc never reads NewSessionRequest.AdditionalDirectories (session.go), so advertising support would be dishonest")
}

func TestUnit_Initialize_DoesNotAdvertiseAdditionalDirectories(t *testing.T) {
	tr := &Transport{deps: Deps{Engine: &enginesvc.Engine{}}}
	resp, err := tr.Initialize(context.Background(), libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	require.Nil(t, resp.AgentCapabilities.SessionCapabilities.AdditionalDirectories)
}

func TestLoopback_Prompt_StreamsUpdatesThroughRealClient(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	require.NotEmpty(t, newResp.SessionID)
	h.lc.drain(t, 1)

	fake := &loopbackAgent{}
	fake.promptFunc = func(ctx context.Context, req agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
		reqID, _ := ctx.Value(libtracker.ContextKeyRequestID).(string)
		require.NotEmpty(t, reqID, "acpsvc stamps a request id onto the prompt ctx before calling the agent (prompt.go)")
		subject := taskengine.TaskEventRequestSubject(reqID)
		publish := func(ev taskengine.TaskEvent) {
			raw, mErr := json.Marshal(ev)
			require.NoError(t, mErr)
			require.NoError(t, h.bus.Publish(ctx, subject, raw))
		}
		publish(taskengine.TaskEvent{Kind: taskengine.TaskEventStepChunk, TaskHandler: string(taskengine.HandleChatCompletion), Content: "Hello from the real client stack."})
		publish(taskengine.TaskEvent{Kind: taskengine.TaskEventToolCallPending, ToolName: "local_fs.read_file", ApprovalID: "call-1", ApprovalArgs: map[string]any{"path": "/tmp/x.txt"}})
		publish(taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "local_fs.read_file", ApprovalID: "call-1", Content: `"file contents"`})
		publish(taskengine.TaskEvent{Kind: taskengine.TaskEventTokenUsage, TokenUsed: 12, TokenSize: 4096})
		return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
	}
	h.swapAgent(newResp.SessionID, fake)

	promptResp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("hi")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason)

	updates := h.lc.drain(t, 5)
	byKind := make(map[libacp.SessionUpdateKind]libacp.SessionUpdate, len(updates))
	for _, u := range updates {
		byKind[u.Update.SessionUpdate] = u.Update
	}

	require.Contains(t, byKind, libacp.SessionUpdateSessionInfo, "session/prompt always appends a session_info_update")

	chunk, ok := byKind[libacp.SessionUpdateAgentMessageChunk]
	require.True(t, ok)
	require.NotNil(t, chunk.Content)
	require.Equal(t, "Hello from the real client stack.", chunk.Content.Text)

	pending, ok := byKind[libacp.SessionUpdateToolCall]
	require.True(t, ok, "the first notification for a tool call must be create-shaped, not update-shaped")
	require.Equal(t, "call-1", pending.ToolCallID)
	require.Equal(t, libacp.ToolCallStatusPending, pending.Status)

	completed, ok := byKind[libacp.SessionUpdateToolCallUpdate]
	require.True(t, ok)
	require.Equal(t, "call-1", completed.ToolCallID)
	require.Equal(t, libacp.ToolCallStatusCompleted, completed.Status)

	usage, ok := byKind[libacp.SessionUpdateUsageUpdate]
	require.True(t, ok)
	require.Equal(t, 12, usage.Used)
	require.Equal(t, 4096, usage.Size)
}

func TestLoopback_Prompt_PushesDerivedTitleInSessionInfo(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	h.tr.sessionMu.Lock()
	internalID := h.tr.sessions[newResp.SessionID].InternalSessionID
	h.tr.sessionMu.Unlock()
	require.NotEmpty(t, internalID)

	const firstMessage = "   hey   how do you do?   "
	require.NoError(t, chatservice.NewManager("loopback-ws").PersistDiff(ctx, h.tr.deps.DB.WithoutTransaction(), internalID, []taskengine.Message{
		{Role: "user", Content: firstMessage, Timestamp: time.Now()},
		{Role: "assistant", Content: "very well, thank you", Timestamp: time.Now().Add(time.Second)},
	}))

	fake := &loopbackAgent{promptFunc: func(context.Context, agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
		return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
	}}
	h.swapAgent(newResp.SessionID, fake)

	_, err = h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("hi again")},
	})
	require.NoError(t, err)

	notes := h.lc.drain(t, 1)
	info := notes[0].Update
	require.Equal(t, libacp.SessionUpdateSessionInfo, info.SessionUpdate)
	require.NotEmpty(t, info.UpdatedAt, "the freshness ping must still be present")
	require.Equal(t, "hey how do you do?", info.Title,
		"session_info_update must carry the first user message (whitespace-collapsed) as Title, not the raw session id")
	require.NotEqual(t, string(newResp.SessionID), info.Title,
		"the derived title must not echo the session id, or the client treats it as absent")
}

func TestLoopback_CancelPrompt_ResolvesStopReasonCancelled(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	started := make(chan struct{})
	var startOnce sync.Once
	fake := &loopbackAgent{promptFunc: func(ctx context.Context, _ agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return &agentservice.PromptResponse{StopReason: agentservice.StopCancelled}, ctx.Err()
		case <-time.After(5 * time.Second):
			return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
		}
	}}
	h.swapAgent(newResp.SessionID, fake)

	type result struct {
		resp libacp.PromptResponse
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := h.client.Prompt(ctx, libacp.PromptRequest{
			SessionID: newResp.SessionID,
			Prompt:    []libacp.ContentBlock{libacp.NewTextContent("please cancel me")},
		})
		resultCh <- result{resp, err}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt did not reach the fake agent")
	}

	require.NoError(t, h.client.CancelPrompt(newResp.SessionID))

	select {
	case r := <-resultCh:
		require.NoError(t, r.err, "ACP spec: cancellation must not surface as a JSON-RPC error")
		require.Equal(t, libacp.StopReasonCancelled, r.resp.StopReason)
	case <-time.After(3 * time.Second):
		t.Fatal("prompt did not resolve after CancelPrompt")
	}
}

func TestLoopback_ServerCancel_AbortsEngineCtxAndResolvesCancelled(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	started := make(chan struct{})
	var startOnce sync.Once
	engineCtxCancelled := make(chan struct{})
	fake := &loopbackAgent{promptFunc: func(ctx context.Context, _ agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			close(engineCtxCancelled)
			return &agentservice.PromptResponse{StopReason: agentservice.StopCancelled}, ctx.Err()
		case <-time.After(5 * time.Second):
			return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
		}
	}}
	h.swapAgent(newResp.SessionID, fake)

	type result struct {
		resp libacp.PromptResponse
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		resp, err := h.client.Prompt(ctx, libacp.PromptRequest{
			SessionID: newResp.SessionID,
			Prompt:    []libacp.ContentBlock{libacp.NewTextContent("start a slow turn")},
		})
		resultCh <- result{resp, err}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt did not reach the fake agent")
	}

	require.NoError(t, h.client.CancelSession(libacp.CancelNotification{SessionID: newResp.SessionID}))

	select {
	case <-engineCtxCancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("engine ctx was not cancelled after session/cancel — the running turn kept executing")
	}

	select {
	case r := <-resultCh:
		require.NoError(t, r.err, "ACP spec: cancellation must not surface as a JSON-RPC error")
		require.Equal(t, libacp.StopReasonCancelled, r.resp.StopReason)
	case <-time.After(3 * time.Second):
		t.Fatal("prompt did not resolve promptly after session/cancel")
	}
}

func TestUnit_Cancel_NoInflightTurnIsCleanNoOp(t *testing.T) {
	tr := &Transport{promptCancels: make(map[libacp.SessionID]*inflightPrompt)}
	require.False(t, tr.cancelInflightPrompt("no-such-session"),
		"cancelling a session with no in-flight turn must report it cancelled nothing")
	require.NoError(t, tr.Cancel(context.Background(), libacp.CancelNotification{SessionID: "no-such-session"}),
		"session/cancel with no in-flight turn is a clean no-op, never an error")
}

func TestUnit_PromptCancel_RegisterSupersedeUnregister(t *testing.T) {
	tr := &Transport{promptCancels: make(map[libacp.SessionID]*inflightPrompt)}
	const sid = libacp.SessionID("sess-x")

	firstCancelled := false
	reg1 := tr.registerPromptCancel(sid, func() { firstCancelled = true })

	secondCancelled := false
	reg2 := tr.registerPromptCancel(sid, func() { secondCancelled = true })
	require.True(t, firstCancelled, "a superseding registration must cancel the stale turn")

	tr.unregisterPromptCancel(sid, reg1)
	require.True(t, tr.cancelInflightPrompt(sid), "the current (second) turn must still be registered")
	require.True(t, secondCancelled)

	tr.unregisterPromptCancel(sid, reg2)
	require.False(t, tr.cancelInflightPrompt(sid), "after the current turn unregisters, nothing remains")
}

func TestLoopback_Prompt_PermissionRoundTripThroughRealClient(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	var approvalErr error
	var allowed bool
	fake := &loopbackAgent{promptFunc: func(ctx context.Context, req agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
		approveCtx := context.WithValue(ctx, runtimetypes.SessionIDContextKey, req.SessionID)
		allowed, approvalErr = h.tr.AskApproval(approveCtx, hitlservice.ApprovalRequest{
			ToolCallID: "call-perm-1",
			ToolName:   "local_shell.exec",
			Args:       map[string]any{"command": "rm -rf /tmp/x"},
		})
		return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
	}}
	h.swapAgent(newResp.SessionID, fake)

	h.lc.setPermissionResponse(libacp.RequestPermissionResponse{
		Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: approvalflow.OptionAllow},
	})
	promptResp, err := h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("do the thing")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason)
	require.NoError(t, approvalErr)
	require.True(t, allowed, "client answered allow_once; AskApproval must resolve true")

	req, ok := h.lc.lastPermissionRequest()
	require.True(t, ok, "the real client must have received session/request_permission")
	require.Equal(t, newResp.SessionID, req.SessionID)
	require.Equal(t, "call-perm-1", req.ToolCall.ToolCallID)

	h.lc.setPermissionResponse(libacp.RequestPermissionResponse{
		Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: approvalflow.OptionDeny},
	})
	_, err = h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("do it again")},
	})
	require.NoError(t, err)
	require.NoError(t, approvalErr)
	require.False(t, allowed, "client answered reject; AskApproval must resolve false")
}

func TestLoopback_SessionRouter_RoutesToOwningTransport(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	h.tr.sessionMu.Lock()
	internalID := h.tr.sessions[newResp.SessionID].InternalSessionID
	h.tr.sessionMu.Unlock()
	require.NotEmpty(t, internalID)

	h.lc.setPermissionResponse(libacp.RequestPermissionResponse{
		Outcome: libacp.RequestPermissionOutcome{Outcome: libacp.PermissionOutcomeSelected, OptionID: approvalflow.OptionAllow},
	})
	approveCtx := context.WithValue(ctx, runtimetypes.SessionIDContextKey, internalID)
	allowed, err := h.router.AskApproval(approveCtx, hitlservice.ApprovalRequest{
		ToolCallID: "router-call-1",
		ToolName:   "local_fs.write_file",
		Args:       map[string]any{"path": "/tmp/loopback-router/x.txt"},
	})
	require.NoError(t, err)
	require.True(t, allowed, "router must bridge to the owning transport and resolve the client's allow")
	req, ok := h.lc.lastPermissionRequest()
	require.True(t, ok, "the real client must have received session/request_permission")
	require.Equal(t, newResp.SessionID, req.SessionID)
	require.Equal(t, "router-call-1", req.ToolCall.ToolCallID)

	_, err = h.router.AskApproval(
		context.WithValue(ctx, runtimetypes.SessionIDContextKey, "no-such-session"),
		hitlservice.ApprovalRequest{ToolCallID: "router-call-2", ToolName: "local_fs.write_file"},
	)
	require.ErrorIs(t, err, ErrNoBoundSession)

	_, err = h.client.CloseSession(ctx, libacp.CloseSessionRequest{SessionID: newResp.SessionID})
	require.NoError(t, err)
	_, err = h.router.AskApproval(approveCtx, hitlservice.ApprovalRequest{ToolCallID: "router-call-3", ToolName: "local_fs.write_file"})
	require.ErrorIs(t, err, ErrNoBoundSession, "a closed session must no longer route")
}

func TestLoopback_Prompt_FSReadWriteThroughRealClient(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{
		ProtocolVersion: libacp.ProtocolVersion,
		ClientCapabilities: libacp.ClientCapabilities{
			FS: libacp.FileSystemCapabilities{ReadTextFile: true, WriteTextFile: true},
		},
	})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	h.lc.mu.Lock()
	h.lc.files["/tmp/loopback-fs/note.txt"] = "hello from the client"
	h.lc.mu.Unlock()

	fio := NewACPFileIO(func() *Transport { return h.tr })
	var readBack []byte
	fake := &loopbackAgent{promptFunc: func(ctx context.Context, req agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
		approveCtx := context.WithValue(ctx, runtimetypes.SessionIDContextKey, req.SessionID)
		var readErr, writeErr error
		readBack, readErr = fio.ReadFile(approveCtx, "/tmp/loopback-fs/note.txt")
		require.NoError(t, readErr)
		writeErr = fio.WriteFile(approveCtx, "/tmp/loopback-fs/written.txt", []byte("hello from the agent"))
		require.NoError(t, writeErr)
		return &agentservice.PromptResponse{StopReason: agentservice.StopEndTurn}, nil
	}}
	h.swapAgent(newResp.SessionID, fake)

	_, err = h.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("read and write")},
	})
	require.NoError(t, err)
	require.Equal(t, "hello from the client", string(readBack))

	h.lc.mu.Lock()
	written := h.lc.files["/tmp/loopback-fs/written.txt"]
	h.lc.mu.Unlock()
	require.Equal(t, "hello from the agent", written)
}

func TestLoopback_SetSessionConfigOption_RoundTripThroughRealClient(t *testing.T) {
	h := newLoopbackHarness(t)
	ctx := context.Background()

	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{Cwd: t.TempDir(), McpServers: []libacp.McpServer{}})
	require.NoError(t, err)
	h.lc.drain(t, 1)

	thinkOption := func(options []libacp.SessionConfigOption) libacp.SessionConfigOption {
		t.Helper()
		for _, o := range options {
			if o.ID == configIDThink {
				return o
			}
		}
		t.Fatalf("think config option missing from %#v", options)
		return libacp.SessionConfigOption{}
	}
	require.Equal(t, "high", thinkOption(newResp.ConfigOptions).CurrentValue, "session/new's default think level")

	setResp, err := h.client.SetSessionConfigOption(ctx, libacp.SetSessionConfigOptionRequest{
		SessionID: newResp.SessionID,
		ConfigID:  configIDThink,
		Value:     libacp.StringConfigValue("xhigh"),
	})
	require.NoError(t, err)
	require.Equal(t, "xhigh", thinkOption(setResp.ConfigOptions).CurrentValue)

	h.tr.sessionMu.Lock()
	sess := h.tr.sessions[newResp.SessionID]
	h.tr.sessionMu.Unlock()
	require.Equal(t, "xhigh", sess.think(), "the change must durably apply to the session's live state")
}
