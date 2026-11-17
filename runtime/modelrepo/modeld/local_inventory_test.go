package modeld_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

type catalogInventory struct {
	*transport.MemoryService
	*modelstore.NodeAdmin
	backend transport.BackendInventory
}

func (s *catalogInventory) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{Backends: []transport.BackendInventory{s.backend}}, nil
}

func (*catalogInventory) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{}, errors.New("catalog must not load or plan a model")
}

func TestUnit_LocalCatalogUsesOwnerCapabilitiesWithoutLoadingModels(t *testing.T) {
	for _, available := range []bool{true, false} {
		t.Run(map[bool]string{true: "available", false: "missing-backend"}[available], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			root := t.TempDir()
			ctx = modeldconn.WithDataRoot(ctx, root)
			for _, name := range []string{"qwen3-embedding-0.6b", "gemma4-e4b"} {
				dir := filepath.Join(root, "models", name)
				require.NoError(t, os.MkdirAll(dir, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("weights"), 0o644))
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			holder, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), Endpoint: listener.Addr().String(), Backend: "shared", TTL: 30 * time.Second})
			require.NoError(t, err)
			require.True(t, holder.IsOwner())
			t.Cleanup(func() { require.NoError(t, holder.Release()) })
			backend := transport.BackendInventory{Backend: "llama", State: "available", Operations: []string{"chat", "embedding", "vision"}}
			if !available {
				backend.State = "unavailable"
				backend.Error = "required runtime unavailable"
			}
			service := &catalogInventory{MemoryService: transport.NewMemoryService(), NodeAdmin: modelstore.NewNodeAdmin(filepath.Join(root, "models")), backend: backend}
			done := make(chan error, 1)
			go func() { done <- transportgrpc.Serve(ctx, listener, service, holder.InstanceID(), "shared") }()
			t.Cleanup(func() { cancel(); require.NoError(t, <-done); modeldconn.CloseEndpoint("catalog:" + root) })
			catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "modeld", BaseURL: "local"})
			require.NoError(t, err)
			models, err := catalog.ListModels(ctx)
			require.NoError(t, err)
			require.Len(t, models, 2)
			for _, model := range models {
				if model.Name == "qwen3-embedding-0.6b" {
					require.Equal(t, available, model.CanEmbed)
					require.False(t, model.CanChat)
				} else {
					require.Equal(t, available, model.CanChat)
					require.Equal(t, available, model.CanVision)
					require.False(t, model.CanEmbed)
				}
				require.False(t, model.CanAudio)
				require.Equal(t, backend.State, model.Meta["backend_state"])
				require.Equal(t, backend.Error, model.Meta["backend_error"])
			}
		})
	}
}
