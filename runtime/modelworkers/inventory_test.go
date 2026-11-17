package modelworkers_test

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type inventoryNode struct{ *transport.MemoryService }

func (inventoryNode) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{Backends: []transport.BackendInventory{{Backend: "remote-backend", State: "available"}}}, nil
}

func (inventoryNode) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{}, errors.New("inventory must not inspect a model")
}

func TestUnit_WorkerInventory_UsesSelectedRemoteWithoutLocalInstallation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := t.TempDir()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "settings.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		done <- transportgrpc.Serve(ctx, listener, inventoryNode{transport.NewMemoryService()}, "inventory-owner", "shared")
	}()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done); modeldconn.CloseEndpoint("inventory:remote") })
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "remote", Name: "Remote", Type: "modeld", BaseURL: listener.Addr().String()}))
	workers, err := modeldconn.NewConfiguredWorkers(ctx, root, func(context.Context, string) (modeldconn.WorkerOptions, error) {
		return modeldconn.WorkerOptions{}, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, workers.Close()) })
	service, err := modelworkers.New(db, workers)
	require.NoError(t, err)
	result, err := service.Inventory(ctx, modelworkers.InventoryRequest{Target: "connection", ConnectionID: "remote"})
	require.NoError(t, err)
	require.Equal(t, "remote-backend", result.Backends[0].Backend)
	_, err = service.Inventory(ctx, modelworkers.InventoryRequest{})
	require.ErrorContains(t, err, "requires a local or connection target")
}
