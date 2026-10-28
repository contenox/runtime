package acpsvc

import (
	"io"
	"sync/atomic"
	"testing"
	"time"

	libacp "github.com/contenox/runtime/libacp"
	"github.com/stretchr/testify/require"
)

type pipeRWC struct {
	io.Reader
	io.Writer
	close func() error
}

func (p pipeRWC) Close() error { return p.close() }

func TestUnit_TransportReleasesRoutingAndTerminalsWhenConnCloses(t *testing.T) {
	router := NewSessionRouter()
	var tr *Transport
	factory := New(Deps{SessionRouter: router})

	agentR, clientW := io.Pipe()
	_, agentW := io.Pipe()
	rwc := pipeRWC{Reader: agentR, Writer: agentW, close: func() error {
		_ = agentR.Close()
		return agentW.Close()
	}}
	conn := libacp.NewAgentSideConnection(rwc, func(c *libacp.AgentSideConnection) libacp.Agent {
		a := factory(c)
		tr = a.(*Transport)
		return a
	})
	require.NotNil(t, tr)

	tr.sessionMu.Lock()
	tr.sessions["acp-1"] = &sessionEntry{InternalSessionID: "internal-1"}
	tr.bindContenoxSession("internal-1", "acp-1")
	tr.sessionMu.Unlock()
	var unsubscribed atomic.Bool
	tr.termSubMu.Lock()
	tr.termSubs["acp-1"] = func() { unsubscribed.Store(true) }
	tr.termSubMu.Unlock()

	other := &Transport{}
	router.bind("internal-2", other)

	got, ok := router.transportFor("internal-1")
	require.True(t, ok)
	require.Same(t, tr, got)

	done := make(chan struct{})
	go func() {
		_ = conn.Run(t.Context())
		close(done)
	}()
	_ = clientW.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connection did not stop after the peer closed")
	}

	require.Eventually(t, func() bool {
		_, bound := router.transportFor("internal-1")
		return !bound && unsubscribed.Load()
	}, 5*time.Second, 10*time.Millisecond, "a dropped connection must release its router bindings and terminal subscriptions")

	got, ok = router.transportFor("internal-2")
	require.True(t, ok, "bindings held by other transports must survive")
	require.Same(t, other, got)
	require.Error(t, tr.connCtx.Err())
}

func TestUnit_TransportConnCloseKeepsNewerHolderBinding(t *testing.T) {
	router := NewSessionRouter()
	old := &Transport{deps: Deps{SessionRouter: router}, sessions: map[libacp.SessionID]*sessionEntry{"acp-1": {InternalSessionID: "internal-1"}}, contenoxToACPID: map[string]libacp.SessionID{"internal-1": "acp-1"}}
	router.bind("internal-1", old)
	newer := &Transport{}
	router.bind("internal-1", newer)

	old.releaseSessionRouting()

	got, ok := router.transportFor("internal-1")
	require.True(t, ok, "a reconnected transport's binding must not be dropped by the dead one")
	require.Same(t, newer, got)
}
