package modeld_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

type rejectingArtifact struct {
	*transport.MemoryService
	requests chan transport.OpenSessionRequest
}

func TestUnit_ModeldSessionKeySurvivesRepeatedRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	requests := make(chan transport.OpenSessionRequest, 1)
	done := make(chan error, 1)
	go func() {
		done <- transportgrpc.Serve(ctx, listener, rejectingArtifact{transport.NewMemoryService(), requests}, "owner", "shared")
	}()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done); modeldconn.CloseEndpoint(listener.Addr().String()) })
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "modeld", BaseURL: listener.Addr().String()})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "operator-model", Meta: map[string]string{"type": "llama"}, CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true}})
	client, err := provider.GetChatConnection(ctx, listener.Addr().String())
	require.NoError(t, err)
	namespace := func(key string) string {
		t.Helper()
		_, err := client.Chat(ctx, []modelrepo.Message{{Role: "user", Content: "test"}}, modelrepo.WithCacheHints(modelrepo.CacheHints{SessionKey: key}))
		require.ErrorIs(t, err, transport.ErrDigestMismatch)
		return (<-requests).Config.SessionNamespace
	}
	first := namespace("opaque-conversation-key")
	require.True(t, strings.HasSuffix(first, "/opaque-conversation-key"))
	require.Equal(t, first, namespace("opaque-conversation-key"))
	require.NotEqual(t, first, namespace("another-conversation-key"))
	unkeyed := namespace("")
	require.NotEmpty(t, unkeyed)
	require.NotEqual(t, first, unkeyed)
	require.NotEqual(t, unkeyed, namespace(""))
}

func (s rejectingArtifact) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	select {
	case s.requests <- req:
		return nil, transport.ErrDigestMismatch
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestUnit_ModeldObservedArtifactAndProposedPlacementReachSharedOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	requests := make(chan transport.OpenSessionRequest, 1)
	done := make(chan error, 1)
	go func() {
		done <- transportgrpc.Serve(ctx, listener, rejectingArtifact{transport.NewMemoryService(), requests}, "owner", "shared")
	}()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done); modeldconn.CloseEndpoint(listener.Addr().String()) })
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "modeld", BaseURL: listener.Addr().String()})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "operator-model", Digest: "reviewed-digest", Meta: map[string]string{"type": "llama"}, CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true}})
	client, err := provider.GetChatConnection(ctx, listener.Addr().String())
	require.NoError(t, err)
	proposed, err := modeldconn.WithWorkerOptions(ctx, "primary", modeldconn.WorkerOptions{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, Memory: "2GiB"})
	require.NoError(t, err)
	_, err = client.Chat(proposed, []modelrepo.Message{{Role: "user", Content: "test"}})
	require.ErrorIs(t, err, transport.ErrDigestMismatch)
	req := <-requests
	require.Equal(t, "operator-model", req.ModelName)
	require.Equal(t, "reviewed-digest", req.Digest)
	require.Equal(t, "llama", req.Type)
	require.NotNil(t, req.Config.Execution)
	require.Equal(t, transport.PlacementCPU, req.Config.Execution.Target.Mode)
	require.Equal(t, "2GiB", req.Config.Execution.Memory)
}
