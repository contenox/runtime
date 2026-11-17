package contenoxcli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

func TestUnit_NativeList_OfflineInventory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", root)
	dir := filepath.Join(modelstore.Dir(root, ""), "installed")
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("weights"), 0600))
	cmd := newNativeListCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "installed")
	require.Contains(t, out.String(), "llama")
	_, err := os.Stat(filepath.Join(root, "modeld.lease"))
	require.True(t, os.IsNotExist(err))
}

func TestUnit_NativeVerbs_Dispatch(t *testing.T) {
	for _, name := range []string{"pull", "list", "ls", "show", "ps", "stop"} {
		require.True(t, reservedSubcommands[name], name)
		cmd, _, err := rootCmd.Find([]string{name})
		require.NoError(t, err)
		require.NotEqual(t, rootCmd, cmd, name)
	}
}

type sharedInspectionWorker struct {
	*transport.MemoryService
	inspections chan transport.OpenSessionRequest
}

func (w *sharedInspectionWorker) Describe(_ context.Context, req transport.OpenSessionRequest) (transport.ModelInfo, error) {
	w.inspections <- req
	return transport.ModelInfo{RuntimeName: "shared-inspection", EffectiveContext: 4096}, nil
}

func (*sharedInspectionWorker) LoadModel(context.Context, transport.LoadModelRequest) (transport.ActiveModel, error) {
	return transport.ActiveModel{}, transport.ErrUnsupportedFeature
}

func (*sharedInspectionWorker) UnloadModel(context.Context, transport.UnloadModelRequest) error {
	return transport.ErrUnsupportedFeature
}

func (w *sharedInspectionWorker) Status(context.Context) (transport.DaemonStatus, error) {
	return transport.DaemonStatus{Workers: []transport.WorkerStatus{
		{Spec: transport.WorkerSpec{Model: "installed", Backend: "llama"}, Instance: "test-instance", State: "ready"},
	}}, nil
}

func TestUnit_NativeShowAndPS_SharedOwner(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", root)
	ctx, cancel := context.WithTimeout(modeldconn.WithDataRoot(t.Context(), root), 10*time.Second)
	defer cancel()
	dir := filepath.Join(modelstore.Dir(root, ""), "installed")
	require.NoError(t, os.MkdirAll(dir, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("weights"), 0600))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	lease, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), TTL: time.Minute, Endpoint: listener.Addr().String(), Backend: "shared"})
	require.NoError(t, err)
	defer lease.Release()
	worker := &sharedInspectionWorker{MemoryService: transport.NewMemoryService(), inspections: make(chan transport.OpenSessionRequest, 1)}
	go func() { _ = transportgrpc.Serve(ctx, listener, worker, lease.InstanceID(), "shared") }()

	var out bytes.Buffer
	show := newNativeShowCmd()
	show.SetArgs([]string{"installed"})
	show.SetOut(&out)
	require.NoError(t, show.ExecuteContext(ctx))
	require.Contains(t, out.String(), "shared-inspection")
	request := <-worker.inspections
	require.Equal(t, lease.InstanceID(), request.OwnerInstanceID)
	require.NotNil(t, request.Config.Execution)
	require.Equal(t, "inspection", request.Config.Execution.Role)
	out.Reset()
	ps := newNativePSCmd()
	ps.SetArgs([]string{})
	ps.SetOut(&out)
	require.NoError(t, ps.ExecuteContext(ctx))
	for _, value := range []string{"installed", "llama", "ready", "test-instance"} {
		require.Contains(t, out.String(), value)
	}
}
