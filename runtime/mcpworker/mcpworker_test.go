package mcpworker_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/mcpworker"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type minimalStore struct {
	runtimetypes.Store
}

func (m *minimalStore) ListMCPServers(_ context.Context, _ *time.Time, _ int) ([]*runtimetypes.MCPServer, error) {
	return nil, nil
}

func (m *minimalStore) GetKV(_ context.Context, _ string, _ interface{}) error {
	return errors.New("no kv")
}

func (m *minimalStore) SetKV(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}

func newTestManager(ctx context.Context, t *testing.T, bus *libbus.InMem) *mcpworker.Manager {
	t.Helper()
	mgr, err := mcpworker.New(ctx, &minimalStore{}, bus, libtracker.NoopTracker{})
	require.NoError(t, err)
	return mgr
}

func fakeMCPServer(name string) *runtimetypes.MCPServer {
	return &runtimetypes.MCPServer{
		ID:        "00000000-0000-0000-0000-000000000001",
		Name:      name,
		Transport: "sse",
		URL:       "http://127.0.0.1:19999/sse",
	}
}

func TestUnit_MCPWorker_StartStop(t *testing.T) {
	ctx := t.Context()
	bus := libbus.NewInMem()
	mgr := newTestManager(ctx, t, bus)

	srv := fakeMCPServer("unit-test-server")

	_, err := bus.Request(ctx, mcpworker.SubjectExecute(srv.Name), []byte(`{}`))
	require.ErrorIs(t, err, libbus.ErrRequestTimeout, "expected no handler before StartWorker")

	err = mgr.StartWorker(ctx, srv)
	require.NoError(t, err)

	assert.Contains(t, mgr.ActiveWorkers(), srv.Name)

	payload, _ := json.Marshal(mcpworker.MCPToolRequest{Tool: "ping", Args: nil})
	replyData, err := bus.Request(ctx, mcpworker.SubjectExecute(srv.Name), payload)
	require.NoError(t, err, "handler should be reachable even if MCP connect fails")
	_, decodeErr := mcpworker.DecodeToolReply(replyData)
	assert.Error(t, decodeErr, "tool call should fail because no real MCP server is running")

	replyData, err = bus.Request(ctx, mcpworker.SubjectListTools(srv.Name), nil)
	require.NoError(t, err)
	_, _ = mcpworker.DecodeListToolsReply(replyData)

	mgr.StopWorker(ctx, srv.Name)
	assert.NotContains(t, mgr.ActiveWorkers(), srv.Name)

	require.Eventually(t, func() bool {
		_, err := bus.Request(ctx, mcpworker.SubjectExecute(srv.Name), []byte(`{}`))
		return errors.Is(err, libbus.ErrRequestTimeout)
	}, 2*time.Second, 10*time.Millisecond, "handler should be gone after StopWorker")
}

func TestUnit_MCPWorker_WatchCreated(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	bus := libbus.NewInMem()
	mgr := newTestManager(ctx, t, bus)
	require.NoError(t, mgr.WatchEvents(ctx))

	srv := fakeMCPServer("watch-created-server")

	data, err := json.Marshal(srv)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(ctx, mcpworker.SubjectCreated, data))

	require.Eventually(t, func() bool {
		return contains(mgr.ActiveWorkers(), srv.Name)
	}, 2*time.Second, 50*time.Millisecond, "worker should start after created event")
}

func TestUnit_MCPWorker_WatchDeleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	bus := libbus.NewInMem()
	mgr := newTestManager(ctx, t, bus)
	require.NoError(t, mgr.WatchEvents(ctx))

	srv := fakeMCPServer("watch-deleted-server")

	require.NoError(t, mgr.StartWorker(ctx, srv))
	require.Contains(t, mgr.ActiveWorkers(), srv.Name)

	data, err := json.Marshal(mcpworker.MCPDeletedEvent{Name: srv.Name})
	require.NoError(t, err)
	require.NoError(t, bus.Publish(ctx, mcpworker.SubjectDeleted, data))

	require.Eventually(t, func() bool {
		return !contains(mgr.ActiveWorkers(), srv.Name)
	}, 2*time.Second, 50*time.Millisecond, "worker should stop after deleted event")

	_, err = bus.Request(ctx, mcpworker.SubjectExecute(srv.Name), []byte(`{}`))
	require.ErrorIs(t, err, libbus.ErrRequestTimeout)
}

func TestUnit_MCPWorker_IdempotentStart(t *testing.T) {
	ctx := t.Context()
	bus := libbus.NewInMem()
	mgr := newTestManager(ctx, t, bus)

	srv := fakeMCPServer("idempotent-server")

	require.NoError(t, mgr.StartWorker(ctx, srv))
	require.NoError(t, mgr.StartWorker(ctx, srv))

	workers := mgr.ActiveWorkers()
	count := 0
	for _, w := range workers {
		if w == srv.Name {
			count++
		}
	}
	assert.Equal(t, 1, count, "should only have one worker per name")
}

func TestUnit_MCPWorker_StopNonExistentWorker(t *testing.T) {
	ctx := t.Context()
	bus := libbus.NewInMem()
	mgr := newTestManager(ctx, t, bus)

	mgr.StopWorker(ctx, "ghost-server")
	assert.NotContains(t, mgr.ActiveWorkers(), "ghost-server")
}

func TestUnit_MCPWorker_ListToolsFailureCooldownSkipsImmediateRetry(t *testing.T) {
	ctx := t.Context()
	bus := libbus.NewInMem()
	mgr := newTestManager(ctx, t, bus)
	srv := fakeMCPServer("cooldown-server")
	require.NoError(t, mgr.StartWorker(ctx, srv))

	replyData, err := bus.Request(ctx, mcpworker.SubjectListTools(srv.Name), nil)
	require.NoError(t, err)
	_, firstErr := mcpworker.DecodeListToolsReply(replyData)
	require.Error(t, firstErr)
	require.NotContains(t, firstErr.Error(), "cooling down")

	replyData, err = bus.Request(ctx, mcpworker.SubjectListTools(srv.Name), nil)
	require.NoError(t, err)
	_, secondErr := mcpworker.DecodeListToolsReply(replyData)
	require.Error(t, secondErr)
	require.Contains(t, secondErr.Error(), "list-tools skipped")
	require.Contains(t, secondErr.Error(), "cooling down")
	require.Contains(t, secondErr.Error(), "previous failure")
	require.True(t, strings.Contains(secondErr.Error(), firstErr.Error()) || strings.Contains(secondErr.Error(), srv.Name))
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestUnit_MCPWorker_BootLoadsFromDB(t *testing.T) {
	ctx := t.Context()
	bus := libbus.NewInMem()

	preloaded := []*runtimetypes.MCPServer{
		fakeMCPServer("boot-server-a"),
		fakeMCPServer("boot-server-b"),
		fakeMCPServer("acp-stale"),
	}
	preloaded[1].ID = "00000000-0000-0000-0000-000000000002"
	preloaded[2].ID = "00000000-0000-0000-0000-000000000003"

	store := &preloadedStore{servers: preloaded}
	mgr, err := mcpworker.New(ctx, store, bus, libtracker.NoopTracker{})
	require.NoError(t, err)

	workers := mgr.ActiveWorkers()
	assert.Contains(t, workers, "boot-server-a")
	assert.Contains(t, workers, "boot-server-b")
	assert.NotContains(t, workers, "acp-stale")
}

type preloadedStore struct {
	runtimetypes.Store
	servers []*runtimetypes.MCPServer
}

func (s *preloadedStore) ListMCPServers(_ context.Context, _ *time.Time, _ int) ([]*runtimetypes.MCPServer, error) {
	return s.servers, nil
}

func TestUnit_MCPWorker_DecodeToolReply_ErrorPropagation(t *testing.T) {
	data, err := json.Marshal(mcpworker.MCPToolReply{Error: "tool execution failed: file not found"})
	require.NoError(t, err)

	_, decodeErr := mcpworker.DecodeToolReply(data)
	require.Error(t, decodeErr)
	assert.Contains(t, decodeErr.Error(), "file not found")
}

func TestUnit_MCPWorker_DecodeToolReply_Success(t *testing.T) {
	data, err := json.Marshal(mcpworker.MCPToolReply{Result: "hello world"})
	require.NoError(t, err)

	result, decodeErr := mcpworker.DecodeToolReply(data)
	require.NoError(t, decodeErr)
	assert.Equal(t, "hello world", result)
}

func TestUnit_MCPWorker_DecodeListToolsReply_ErrorPropagation(t *testing.T) {
	data, err := json.Marshal(mcpworker.MCPToolListReply{Error: "connection refused"})
	require.NoError(t, err)

	_, decodeErr := mcpworker.DecodeListToolsReply(data)
	require.Error(t, decodeErr)
	assert.Contains(t, decodeErr.Error(), "connection refused")
}

var _ interface {
	ListMCPServers(context.Context, *time.Time, int) ([]*runtimetypes.MCPServer, error)
} = (*minimalStore)(nil)

var _ error = errors.New("")
