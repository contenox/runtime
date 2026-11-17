package modeld_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/slot"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

type countedBackend struct {
	transport.Service
	opens  atomic.Int64
	closes atomic.Int64
}

func (s *countedBackend) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	session, err := s.Service.OpenSession(ctx, req)
	if err != nil {
		return nil, err
	}
	s.opens.Add(1)
	return &countedSession{Session: session, backend: s}, nil
}

type countedSession struct {
	transport.Session
	backend *countedBackend
}

func (s *countedSession) Close() error {
	s.backend.closes.Add(1)
	return s.Session.Close()
}

func TestUnit_ModeldChatRetainsCacheAndIsolatesOtherConversations(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "test"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test", "model.gguf"), []byte("fixture"), 0600))
	for _, reused := range checkChatResidency(t, transport.NewMemoryService(), dir, "test") {
		require.Positive(t, reused)
	}
}

func checkChatResidency(t *testing.T, backend transport.Service, modelsDir, model string) []int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	counted := &countedBackend{Service: backend}
	service := slot.New(counted, slot.WithBackend("llama"), slot.WithModelsDir(modelsDir), slot.WithIdleTTL(time.Minute))
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, listener, service, "cache-owner", "llama") }()
	t.Cleanup(func() {
		modeldconn.CloseEndpoint(listener.Addr().String())
		cancel()
		require.NoError(t, <-done)
		require.NoError(t, service.Shutdown(context.Background()))
	})
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "modeld", BaseURL: listener.Addr().String()})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: model, Meta: map[string]string{"type": "llama"}, CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true}})
	client, err := provider.GetChatConnection(ctx, listener.Addr().String())
	require.NoError(t, err)
	turn := func(key, question string) int {
		t.Helper()
		result, err := client.Chat(ctx, []modelrepo.Message{{Role: "system", Content: "You are a concise coding assistant. Answer the user with a short reply."}, {Role: "user", Content: question}}, modelrepo.WithCacheHints(modelrepo.CacheHints{SessionKey: key}), modelrepo.WithMaxTokens(2), modelrepo.WithThink("off"))
		require.NoError(t, err)
		require.NotNil(t, result.Usage)
		return result.Usage.CacheReadTokens
	}
	require.Zero(t, turn("first-conversation", "Hello"))
	firstReuse := turn("first-conversation", "Hello again")
	require.EqualValues(t, 1, counted.opens.Load())
	require.Zero(t, counted.closes.Load())
	require.Zero(t, turn("second-conversation", "Hello"))
	secondReuse := turn("second-conversation", "Hello again")
	require.EqualValues(t, 2, counted.opens.Load())
	require.EqualValues(t, 1, counted.closes.Load())
	modeldconn.CloseEndpoint(listener.Addr().String())
	require.Zero(t, turn("second-conversation", "Hello from another client"))
	require.EqualValues(t, 3, counted.opens.Load())
	require.EqualValues(t, 2, counted.closes.Load())
	return []int{firstReuse, secondReuse}
}
