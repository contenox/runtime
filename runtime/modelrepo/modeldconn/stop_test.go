package modeldconn

import (
	"context"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/stretchr/testify/require"
)

type stopWorker struct {
	*transport.MemoryService
	received chan transport.UnloadModelRequest
	reject   atomic.Bool
	workers  []transport.WorkerStatus
}

func (w *stopWorker) Status(context.Context) (transport.DaemonStatus, error) {
	if w.workers != nil {
		return transport.DaemonStatus{Workers: w.workers}, nil
	}
	return transport.DaemonStatus{Active: &transport.ActiveModel{ModelName: "resident", Generation: 42}}, nil
}

func TestUnit_StopModel_SharedOwnerTargetsIdleInstances(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(WithDataRoot(context.Background(), root), 10*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	o, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), TTL: time.Minute, Endpoint: listener.Addr().String(), Backend: "shared"})
	require.NoError(t, err)
	defer o.Release()
	w := &stopWorker{MemoryService: transport.NewMemoryService(), received: make(chan transport.UnloadModelRequest, 4), workers: []transport.WorkerStatus{
		{Spec: transport.WorkerSpec{Model: "ready"}, Instance: "primary"},
		{Spec: transport.WorkerSpec{Model: "busy"}, Instance: "busy-idle"},
		{Spec: transport.WorkerSpec{Model: "busy"}, Instance: "busy-active", References: 1},
		{Spec: transport.WorkerSpec{Model: "ready"}, Instance: "secondary"},
	}}
	go func() { _ = transportgrpc.Serve(ctx, listener, w, o.InstanceID(), "shared") }()
	require.ErrorContains(t, StopModel(ctx, "missing"), "not resident")
	require.ErrorIs(t, StopModel(ctx, "busy"), transport.ErrModelBusy)
	require.Empty(t, w.received)
	require.NoError(t, StopModel(ctx, "ready"))
	for _, instance := range []string{"primary", "secondary"} {
		req := <-w.received
		require.Equal(t, instance, req.WorkerInstance)
		require.Equal(t, o.InstanceID(), req.OwnerInstanceID)
	}
}
func (w *stopWorker) LoadModel(context.Context, transport.LoadModelRequest) (transport.ActiveModel, error) {
	return transport.ActiveModel{}, nil
}
func (w *stopWorker) UnloadModel(_ context.Context, req transport.UnloadModelRequest) error {
	w.received <- req
	if w.reject.Load() {
		return transport.ErrSlotGenerationStale
	}
	return nil
}

func TestUnit_StopModel_NameAndGenerationFence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	oldRoot := dataRoot
	SetDataRoot(root)
	defer SetDataRoot(oldRoot)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	o, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), TTL: time.Minute, Endpoint: listener.Addr().String(), Backend: "llama"})
	require.NoError(t, err)
	defer o.Release()
	w := &stopWorker{MemoryService: transport.NewMemoryService(), received: make(chan transport.UnloadModelRequest, 1)}
	w.reject.Store(true)
	go func() { _ = transportgrpc.Serve(ctx, listener, w, o.InstanceID(), "llama") }()
	require.ErrorContains(t, StopModel(ctx, "different"), "not resident")
	require.Empty(t, w.received)
	require.Error(t, StopModel(ctx, "resident"))
	req := <-w.received
	require.Equal(t, uint64(42), req.ExpectedGeneration)
	require.Equal(t, o.InstanceID(), req.OwnerInstanceID)
	w.reject.Store(false)
	require.NoError(t, StopModel(ctx, "resident"))
	require.Equal(t, uint64(42), (<-w.received).ExpectedGeneration)
}
