package grpc_test

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/stretchr/testify/require"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type isolatedService struct {
	*transport.MemoryService
	mu         sync.Mutex
	namespaces []string
}

func (s *isolatedService) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	s.mu.Lock()
	s.namespaces = append(s.namespaces, req.Config.SessionNamespace)
	s.mu.Unlock()
	return s.MemoryService.OpenSession(ctx, req)
}

func TestUnit_GRPC_SessionsBelongToTheirClientConnection(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	service := &isolatedService{MemoryService: transport.NewMemoryService(transport.WithOwnerFence("owner"))}
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, lis, service, "owner", "test") }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	connect := func() *grpclib.ClientConn {
		client, err := grpclib.NewClient(lis.Addr().String(), grpclib.WithTransportCredentials(insecure.NewCredentials()), grpclib.WithDefaultCallOptions(grpclib.CallContentSubtype("contenoxtransportjson")))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		return client
	}
	first, second := connect(), connect()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-contenox-owner-instance", "owner")
	invoke := func(client *grpclib.ClientConn, method string, in, out any) error {
		return client.Invoke(ctx, "/contenox.transport.v1.Compute/"+method, in, out)
	}
	var firstSession, secondSession struct{ Handle string }
	request := map[string]any{"owner_instance_id": "owner", "model_name": "test", "config": transport.Config{NumCtx: 128, SessionNamespace: "same-client-supplied-name"}}
	require.NoError(t, invoke(first, "OpenSession", request, &firstSession))
	require.NoError(t, invoke(second, "OpenSession", request, &secondSession))
	for _, method := range []string{"ExplainContext", "Snapshot", "CloseSession"} {
		err := invoke(second, method, map[string]any{"handle": firstSession.Handle}, &map[string]any{})
		require.Error(t, err, method)
	}
	require.NoError(t, invoke(first, "ExplainContext", map[string]any{"handle": firstSession.Handle}, &map[string]any{}))
	require.NoError(t, invoke(first, "CloseSession", map[string]any{"handle": firstSession.Handle}, &map[string]any{}))
	require.NoError(t, invoke(second, "ExplainContext", map[string]any{"handle": secondSession.Handle}, &map[string]any{}))
	service.mu.Lock()
	require.Len(t, service.namespaces, 2)
	require.NotEqual(t, service.namespaces[0], service.namespaces[1])
	service.mu.Unlock()
}
