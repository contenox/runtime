package contenoxcli

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type gatewayNativeService struct {
	*transport.MemoryService
	transport.NodeAdmin
}

func (s *gatewayNativeService) ListModels(context.Context) ([]transport.NodeModel, error) {
	return []transport.NodeModel{{Name: "native-test", Type: "llama"}}, nil
}

func (s *gatewayNativeService) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{ModelMaxContext: 32768, EffectiveContext: 4096, ChatTemplateSupportsThinking: true}, nil
}

func (s *gatewayNativeService) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{
		HostThreads: 8,
		Pools:       []transport.MemoryPool{{ID: "host", Kind: "host", State: "available", TotalBytes: 16 << 30, AvailableBytes: 12 << 30}},
		Backends: []transport.BackendInventory{{
			Backend: "llama", State: "available", Operations: []string{"chat"},
			Devices: []transport.DeviceInfo{{ID: "cpu", Type: "cpu", State: "available", IdentityState: "stable", PoolID: "host", Operations: []string{"chat"}}},
		}},
	}, nil
}

func (s *gatewayNativeService) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	req.Config.NumCtx = 4096
	sess, err := s.MemoryService.OpenSession(ctx, req)
	return &gatewayNativeSession{Session: sess}, err
}

type gatewayNativeSession struct {
	transport.Session
}

func (s *gatewayNativeSession) Decode(context.Context, transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	out := make(chan transport.StreamChunk, 2)
	out <- transport.StreamChunk{Thinking: "check"}
	out <- transport.StreamChunk{Text: "native answer", FinishReason: "stop", Usage: &transport.TokenUsage{PromptTokens: 20, CompletionTokens: 8, ThinkingTokens: 3}}
	close(out)
	return out, nil
}

func TestUnit_Modeld_RuntimeGatewayChat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	service := &gatewayNativeService{MemoryService: transport.NewMemoryService()}
	go func() { _ = transportgrpc.Serve(ctx, listener, service, "native-owner", "llama") }()
	db, err := OpenDBAt(ctx, filepath.Join(t.TempDir(), "native.db"))
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "native-worker", Name: "native", Type: "modeld", BaseURL: listener.Addr().String()}))
	rt, err := enginesvc.Build(ctx, db, enginesvc.Config{DefaultModel: "native-test", DefaultProvider: "modeld"})
	require.NoError(t, err)
	defer rt.Stop()
	states := rt.State.Get(ctx)
	require.Empty(t, states["native-worker"].Error)
	require.Len(t, states["native-worker"].PulledModels, 1)
	require.Equal(t, 4096, states["native-worker"].PulledModels[0].ContextLength)
	require.False(t, states["native-worker"].PulledModels[0].CanVision)
	requestCtx := llmrepo.WithUsageSession(ctx, "native-session")
	result, _, err := rt.Models.Chat(requestCtx, llmrepo.Request{ModelNames: []string{"native-test"}, ProviderTypes: []string{"modeld"}}, []modelrepo.Message{{Role: "user", Content: "hello"}}, modelrepo.WithMaxTokens(32))
	require.NoError(t, err)
	require.Equal(t, "native answer", result.Message.Content)
	require.Equal(t, "check", result.Message.Thinking)
	require.NotNil(t, result.Usage)
	require.Equal(t, 3, result.Usage.ThinkingTokens)
	require.Equal(t, 20, result.Usage.PromptTokens)
	require.Equal(t, 8, result.Usage.CompletionTokens)
}
