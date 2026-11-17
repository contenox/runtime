package modeld_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeld"
	"github.com/stretchr/testify/require"
)

type toolIdentityService struct {
	*transport.MemoryService
	suffixes chan transport.SuffixInput
}

func (s *toolIdentityService) ListModels(context.Context) ([]transport.NodeModel, error) {
	return []transport.NodeModel{{Name: "test", Type: "llama"}}, nil
}

func (s *toolIdentityService) RemoveModel(context.Context, string) error {
	return transport.ErrUnsupportedFeature
}

func (s *toolIdentityService) DiskStats(context.Context) (transport.NodeDiskStats, error) {
	return transport.NodeDiskStats{}, transport.ErrUnsupportedFeature
}

func (s *toolIdentityService) ReceiveModel(context.Context, transport.PushManifest, io.Reader) (transport.PushResult, error) {
	return transport.PushResult{}, transport.ErrUnsupportedFeature
}

func (s *toolIdentityService) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	session, err := s.MemoryService.OpenSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &toolIdentitySession{Session: session, suffixes: s.suffixes}, nil
}

type toolIdentitySession struct {
	transport.Session
	suffixes chan transport.SuffixInput
}

func (s *toolIdentitySession) PrefillSuffix(ctx context.Context, input transport.SuffixInput) (transport.SuffixStatus, error) {
	s.suffixes <- input
	return s.Session.PrefillSuffix(ctx, input)
}

func (s *toolIdentitySession) Decode(context.Context, transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	chunks := make(chan transport.StreamChunk, 2)
	var calls []transport.ToolCall
	for _, id := range []string{"", "provided-id", ""} {
		call := transport.ToolCall{ID: id, Type: "function"}
		call.Function.Name = "lookup"
		call.Function.Arguments = `{"key":"value"}`
		calls = append(calls, call)
	}
	chunks <- transport.StreamChunk{ToolCalls: calls}
	chunks <- transport.StreamChunk{FinishReason: "stop"}
	close(chunks)
	return chunks, nil
}

func TestUnit_ModeldAssignsMissingToolIdentitiesAndPreservesProvidedOnes(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	service := &toolIdentityService{MemoryService: transport.NewMemoryService(), suffixes: make(chan transport.SuffixInput, 2)}
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, listener, service, "owner", "llama") }()
	defer func() { cancel(); <-done }()
	provider := modeld.NewModeldProvider("test", []string{listener.Addr().String()}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}, libtracker.NoopTracker{})
	client, err := provider.GetChatConnection(ctx, listener.Addr().String())
	require.NoError(t, err)
	streamClient, err := provider.GetStreamConnection(ctx, listener.Addr().String())
	require.NoError(t, err)
	messages := []modelrepo.Message{{Role: "user", Content: "look up the value"}}
	parcels, err := streamClient.Stream(ctx, messages)
	require.NoError(t, err)
	assembler := modelrepo.NewStreamAssembler("modeld", "test")
	var streamedIDs []string
	for parcel := range parcels {
		require.NoError(t, assembler.Consume(parcel))
		if parcel.ToolCall != nil {
			streamedIDs = append(streamedIDs, parcel.ToolCall.ID)
		}
	}
	first, err := assembler.Result()
	require.NoError(t, err)
	<-service.suffixes
	require.Len(t, first.ToolCalls, 3)
	require.Equal(t, "provided-id", first.ToolCalls[1].ID)
	ids := make(map[string]bool)
	for index, call := range first.ToolCalls {
		require.NotEmpty(t, call.ID)
		require.Equal(t, streamedIDs[index], call.ID)
		require.False(t, ids[call.ID], "duplicate tool identity %q", call.ID)
		ids[call.ID] = true
	}
	messages = append(messages, modelrepo.Message{Role: "assistant", ToolCalls: first.ToolCalls})
	for _, call := range first.ToolCalls {
		messages = append(messages, modelrepo.Message{Role: "tool", ToolCallID: call.ID, Content: "found"})
	}
	second, err := client.Chat(ctx, messages)
	require.NoError(t, err)
	require.Len(t, second.ToolCalls, 3)
	require.Equal(t, "provided-id", second.ToolCalls[1].ID)
	for _, index := range []int{0, 2} {
		require.NotEmpty(t, second.ToolCalls[index].ID)
		require.False(t, ids[second.ToolCalls[index].ID], "generated identity was reused across turns")
	}
	suffix := <-service.suffixes
	for index, call := range first.ToolCalls {
		require.Equal(t, call.ID, suffix.Manifest.Segments[index+2].ToolCallID)
	}
}
