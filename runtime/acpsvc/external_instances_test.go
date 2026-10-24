package acpsvc

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agenthost"
	"github.com/contenox/runtime/runtime/agentinstance"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type instancesFixture struct {
	t       *testing.T
	db      libdb.DBManager
	mgr     agentinstance.Manager
	factory libacp.AgentFactory
}

func newInstancesFixture(t *testing.T) *instancesFixture {
	t.Helper()
	return newInstancesFixtureWith(t, func(db libdb.DBManager) agentinstance.Manager {
		return agentinstance.New(agentregistryservice.New(db))
	})
}

func newInstancesFixtureWith(t *testing.T, build func(libdb.DBManager) agentinstance.Manager) *instancesFixture {
	t.Helper()
	db, err := libdb.NewSQLiteDBManager(context.Background(), filepath.Join(t.TempDir(), "instances.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	bus := libbus.NewInMem()
	mgr := build(db)
	factory := New(Deps{
		Engine:        &enginesvc.Engine{Bus: bus},
		DB:            db,
		ChainRegistry: &ChainRegistry{defaultChain: &taskengine.TaskChainDefinition{}},
		WorkspaceID:   "loopback-ws",
		SessionRouter: NewSessionRouter(),
		Instances:     mgr,
	})
	t.Cleanup(func() {
		_ = mgr.Close()
		_ = db.Close()
	})
	return &instancesFixture{t: t, db: db, mgr: mgr, factory: factory}
}

type instConn struct {
	t          *testing.T
	tr         *Transport
	client     *libacp.ClientSideConnection
	lc         *loopbackClient
	cancel     context.CancelFunc
	agentDone  chan error
	clientDone chan error
	dropped    bool
}

func (f *instancesFixture) connect() *instConn {
	f.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	agentR, clientW := io.Pipe()
	clientR, agentW := io.Pipe()
	agentSide := &wirePipe{r: agentR, w: agentW}
	clientSide := &wirePipe{r: clientR, w: clientW}

	var tr *Transport
	agentConn := libacp.NewAgentSideConnection(agentSide, func(c *libacp.AgentSideConnection) libacp.Agent {
		a := f.factory(c)
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

	ic := &instConn{t: f.t, tr: tr, client: clientConn, lc: lc, cancel: cancel, agentDone: agentDone, clientDone: clientDone}
	f.t.Cleanup(ic.drop)
	return ic
}

func (ic *instConn) drop() {
	if ic.dropped {
		return
	}
	ic.dropped = true
	ic.cancel()
	select {
	case <-ic.agentDone:
	case <-time.After(2 * time.Second):
		ic.t.Error("agent connection did not shut down after drop")
	}
	select {
	case <-ic.clientDone:
	case <-time.After(2 * time.Second):
		ic.t.Error("client connection did not shut down after drop")
	}
}

func (ic *instConn) externalDriver(sid libacp.SessionID) *externalDriver {
	return externalDriverOf(ic.t, ic.tr, sid)
}

func externalDriverOf(t *testing.T, tr *Transport, sid libacp.SessionID) *externalDriver {
	t.Helper()
	tr.sessionMu.Lock()
	entry := tr.sessions[sid]
	tr.sessionMu.Unlock()
	require.NotNil(t, entry, "session %q not open on this connection", sid)
	ed, ok := entry.driver.(*externalDriver)
	require.True(t, ok, "session %q must be backed by an external driver", sid)
	return ed
}

func extInstanceID(ed *externalDriver) string {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	return ed.instanceID
}

func extHandle(ed *externalDriver) *agenthost.Handle {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	return ed.handle
}

func liveInstances(t *testing.T, mgr agentinstance.Manager) int {
	t.Helper()
	entries, err := mgr.List(context.Background())
	require.NoError(t, err)
	n := 0
	for _, e := range entries {
		n += len(e.Instances)
	}
	return n
}

func TestLoopback_ExternalInstance_SurvivesConnectionDrop(t *testing.T) {
	f := newInstancesFixture(t)
	agentName := registerStubAgentInDB(t, f.db, "claude-stub-survive", nil)
	cwd := t.TempDir()
	ctx := context.Background()

	c1 := f.connect()
	_, err := c1.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := c1.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)

	ed := c1.externalDriver(newResp.SessionID)
	instanceID := extInstanceID(ed)
	require.NotEmpty(t, instanceID, "a Manager-backed external session records its instance id")
	require.Nil(t, extHandle(ed), "the driver must NOT own the process on the Manager path (the instance does)")

	st, err := f.mgr.Get(instanceID)
	require.NoError(t, err)
	require.Equal(t, agentinstance.StateRunning, st.State)

	promptResp, err := c1.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("hello")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason)

	c1.drop()

	require.Never(t, func() bool {
		st, gerr := f.mgr.Get(instanceID)
		return gerr != nil || st.State != agentinstance.StateRunning
	}, 500*time.Millisecond, 50*time.Millisecond, "the instance must survive the connection drop")
	require.Equal(t, 1, liveInstances(t, f.mgr))

	c2 := f.connect()
	_, err = c2.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	_, err = c2.client.LoadSession(ctx, libacp.LoadSessionRequest{
		SessionID: newResp.SessionID,
		Cwd:       cwd,
	})
	require.NoError(t, err)
	promptResp2, err := c2.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("again")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp2.StopReason,
		"the surviving instance must answer a prompt on the re-attached connection")
}

func TestLoopback_ExternalInstance_SessionLoadReattachesSameInstance(t *testing.T) {
	f := newInstancesFixture(t)
	agentName := registerStubAgentInDB(t, f.db, "claude-stub-reattach", nil)
	cwd := t.TempDir()
	ctx := context.Background()

	c1 := f.connect()
	_, err := c1.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := c1.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	instanceID := extInstanceID(c1.externalDriver(newResp.SessionID))
	require.NotEmpty(t, instanceID)

	_, err = c1.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("first")},
	})
	require.NoError(t, err)
	c1.drop()

	c2 := f.connect()
	_, err = c2.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	_, err = c2.client.LoadSession(ctx, libacp.LoadSessionRequest{
		SessionID: newResp.SessionID,
		Cwd:       cwd,
	})
	require.NoError(t, err)
	promptResp, err := c2.client.Prompt(ctx, libacp.PromptRequest{
		SessionID: newResp.SessionID,
		Prompt:    []libacp.ContentBlock{libacp.NewTextContent("second")},
	})
	require.NoError(t, err)
	require.Equal(t, libacp.StopReasonEndTurn, promptResp.StopReason)

	ed2 := c2.externalDriver(newResp.SessionID)
	require.Equal(t, instanceID, extInstanceID(ed2),
		"session/load must re-attach to the SAME instance, not spawn a fresh one")
	require.Nil(t, extHandle(ed2), "a re-attached driver does not own the process")
	require.Equal(t, 1, liveInstances(t, f.mgr), "re-attach must NOT spawn a second instance")
}

func TestLoopback_ExternalInstance_DeleteStopsInstance(t *testing.T) {
	f := newInstancesFixture(t)
	agentName := registerStubAgentInDB(t, f.db, "claude-stub-delete", nil)
	cwd := t.TempDir()
	ctx := context.Background()

	c1 := f.connect()
	_, err := c1.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	newResp, err := c1.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.NoError(t, err)
	instanceID := extInstanceID(c1.externalDriver(newResp.SessionID))
	require.NotEmpty(t, instanceID)
	require.Equal(t, 1, liveInstances(t, f.mgr))

	_, err = c1.client.DeleteSession(ctx, libacp.DeleteSessionRequest{SessionID: newResp.SessionID})
	require.NoError(t, err)

	require.Equal(t, 0, liveInstances(t, f.mgr), "DeleteSession must stop the Manager instance")
	_, gerr := f.mgr.Get(instanceID)
	require.ErrorIs(t, gerr, agentinstance.ErrNotFound)
	_, aerr := f.mgr.Attach(ctx, instanceID, "any-session", newExternalBridge(c1.tr, "any-session", true))
	require.ErrorIs(t, aerr, agentinstance.ErrNotFound)
	_, oerr := f.mgr.OpenSession(ctx, instanceID, agentinstance.SessionSpec{Cwd: cwd})
	require.ErrorIs(t, oerr, agentinstance.ErrNotFound)
}

func TestLoopback_ExternalInstance_NilInstancesFallsBackToConnCtxSpawn(t *testing.T) {
	ctx := context.Background()

	h := newLoopbackHarness(t)
	require.Nil(t, h.tr.deps.Instances, "the default harness must wire no Manager")
	nilAgent := registerStubAgent(t, h, "claude-stub-nil")
	_, err := h.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	nilResp, err := h.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(nilAgent),
	})
	require.NoError(t, err)
	nilEd := externalDriverOf(t, h.tr, nilResp.SessionID)
	require.Empty(t, extInstanceID(nilEd), "no Manager => no instance id")
	require.NotNil(t, extHandle(nilEd), "no Manager => the driver owns the connCtx-bound subprocess")

	f := newInstancesFixture(t)
	mgrAgent := registerStubAgentInDB(t, f.db, "claude-stub-mgr", nil)
	c := f.connect()
	_, err = c.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)
	mgrResp, err := c.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(mgrAgent),
	})
	require.NoError(t, err)
	mgrEd := c.externalDriver(mgrResp.SessionID)
	require.NotEmpty(t, extInstanceID(mgrEd), "a Manager => the session records its instance id")
	require.Nil(t, extHandle(mgrEd), "a Manager => the driver does not own the process")
}

func TestLoopback_ExternalInstance_DisabledAgentRejected(t *testing.T) {
	f := newInstancesFixture(t)
	ctx := context.Background()

	const agentName = "claude-stub-disabled-mgr"
	svc := agentregistryservice.New(f.db)
	agent := &runtimetypes.Agent{Name: agentName, Enabled: false}
	require.NoError(t, agent.SetExternalACPConfig(runtimetypes.ExternalACPConfig{
		Transport: runtimetypes.ExternalACPTransportStdio,
		Command:   "/bin/true",
	}))
	require.NoError(t, svc.Create(ctx, agent))

	c := f.connect()
	_, err := c.client.Initialize(ctx, libacp.InitializeRequest{ProtocolVersion: libacp.ProtocolVersion})
	require.NoError(t, err)

	_, err = c.client.NewSession(ctx, libacp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []libacp.McpServer{},
		Meta:       agentMetaJSON(agentName),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "disabled")
	require.Equal(t, 0, liveInstances(t, f.mgr),
		"a refused agent must never reach agentinstance.Manager.Start")
}
