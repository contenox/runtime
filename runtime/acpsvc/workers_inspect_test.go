package acpsvc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"net"
	"path/filepath"
	"testing"
	"time"

	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/stretchr/testify/require"
)

type inspectionWorker struct {
	*transport.MemoryService
	t             *testing.T
	capacityError bool
}

func (w *inspectionWorker) Status(context.Context) (transport.DaemonStatus, error) {
	return transport.DaemonStatus{State: transport.SlotReady, Active: &transport.ActiveModel{ModelName: "resident", Type: "llama"}}, nil
}
func (w *inspectionWorker) Describe(_ context.Context, request transport.OpenSessionRequest) (transport.ModelInfo, error) {
	require.Equal(w.t, "resident", request.ModelName)
	if w.capacityError {
		return transport.ModelInfo{}, errors.New("capacity unavailable")
	}
	return transport.ModelInfo{ModelMaxContext: 32768, EffectiveContext: 8192, Clamped: true, Reason: "memory budget"}, nil
}
func (w *inspectionWorker) LoadModel(context.Context, transport.LoadModelRequest) (transport.ActiveModel, error) {
	w.t.Error("inspection must not load a model")
	return transport.ActiveModel{}, errors.New("unexpected load")
}
func (w *inspectionWorker) UnloadModel(context.Context, transport.UnloadModelRequest) error {
	w.t.Error("inspection must not unload a model")
	return errors.New("unexpected unload")
}
func (w *inspectionWorker) OpenSession(context.Context, transport.OpenSessionRequest) (transport.Session, error) {
	w.t.Error("inspection must not open an inference session")
	return nil, errors.New("unexpected inference")
}

func TestUnit_WorkerInspect_ReadOnlyAndCapacityFailure(t *testing.T) {
	for _, capacityError := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		worker := &inspectionWorker{MemoryService: transport.NewMemoryService(), t: t, capacityError: capacityError}
		finished := make(chan error, 1)
		go func() { finished <- transportgrpc.Serve(ctx, listener, worker, "instance", "llama") }()
		inspection, err := modelworkers.InspectWorker(ctx, modeldconn.WorkerStatus{Instance: "instance", Endpoint: listener.Addr().String()})
		cancel()
		listener.Close()
		<-finished
		require.NoError(t, err)
		raw, rpcErr := marshalProtocol(workerInspectionWire(*inspection))
		require.Nil(t, rpcErr)
		result := &protocolv1.WorkerInspection{}
		require.NoError(t, protocolRequest.Unmarshal(raw, result))
		require.Equal(t, "resident", result.GetStatus().GetActive().GetModelName())
		if capacityError {
			require.Contains(t, result.GetCapacityError(), "capacity unavailable")
			require.Nil(t, result.GetCapacity())
		} else {
			require.NotNil(t, result.GetCapacity())
			require.Equal(t, int32(8192), result.GetCapacity().GetEffectiveContext())
		}
	}
}

func TestUnit_WorkerInspect_RejectsUnmanagedTargets(t *testing.T) {
	tr := &Transport{}
	for _, raw := range []string{`{}`, `{"instance":"unknown","endpoint":"http://arbitrary"}`} {
		_, err := tr.handleWorkerInspect(t.Context(), json.RawMessage(raw))
		require.NotNil(t, err)
	}
}

type workerWireFixture struct{ inspectionWorker }

func (workerWireFixture) Status(context.Context) (transport.DaemonStatus, error) {
	return transport.DaemonStatus{Workers: []transport.WorkerStatus{{Instance: "primary", Spec: transport.WorkerSpec{Model: "resident", Backend: "llama"}}}}, nil
}
func (workerWireFixture) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{HostThreads: 8, Pools: []transport.MemoryPool{{ID: "host", TotalBytes: 1 << 34}}}, nil
}
func (workerWireFixture) Admission(context.Context) (admission.Snapshot, error) {
	return admission.Snapshot{Policy: admission.DefaultConfig()}, nil
}
func (workerWireFixture) ConfigureAdmission(context.Context, admission.Config) error {
	return transport.ErrUnsupportedFeature
}

func TestUnit_WorkerStatusAndInventory_DeclaredACPResults(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(modeldconn.WithDataRoot(t.Context(), root), 10*time.Second)
	defer cancel()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "state.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	lease, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), Endpoint: listener.Addr().String(), Backend: "shared", TTL: time.Minute})
	require.NoError(t, err)
	defer lease.Release()
	defer listener.Close()
	go func() {
		_ = transportgrpc.Serve(ctx, listener, &workerWireFixture{inspectionWorker{MemoryService: transport.NewMemoryService(), t: t}}, lease.InstanceID(), "shared")
	}()
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "remote", Name: "Remote", Type: "modeld", BaseURL: listener.Addr().String()}))
	defer modeldconn.CloseEndpoint("inventory:remote")
	tr := &Transport{deps: Deps{DB: db}}
	raw, rpcErr := tr.handleExtRequest(ctx, "_contenox/workers/inventory", json.RawMessage(`{"target":"connection","connectionId":"remote"}`))
	require.Nil(t, rpcErr)
	inventory := &protocolv1.WorkerInventory{}
	require.NoError(t, protocolRequest.Unmarshal(raw, inventory))
	require.EqualValues(t, 8, inventory.GetHostThreads())
	require.EqualValues(t, 1<<34, inventory.GetPools()[0].GetTotalBytes())
	raw, rpcErr = tr.handleExtRequest(ctx, "_contenox/workers/status", json.RawMessage(`{}`))
	require.Nil(t, rpcErr)
	status := &protocolv1.WorkerSnapshot{}
	require.NoError(t, protocolRequest.Unmarshal(raw, status))
	require.Equal(t, "ready", status.GetOwnerState(), status.GetOwnerError())
	require.Len(t, status.GetWorkers(), 1)
	require.Equal(t, "resident", status.GetWorkers()[0].GetSpec().GetModel())
}
