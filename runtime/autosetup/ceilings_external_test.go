package autosetup_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/autosetup"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

const (
	fixtureGPUTotal  int64 = 10 << 30
	fixtureChatBytes int64 = 6 << 30
)

type ceilingOwner struct {
	*transport.MemoryService
	*modelstore.NodeAdmin
	mu       sync.Mutex
	ceilings map[string]float64
	refuse   error
}

func (o *ceilingOwner) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{
		HostThreads: 8,
		Pools: []transport.MemoryPool{
			{ID: "host", Kind: "host", TotalBytes: 32 << 30, AvailableBytes: 20 << 30, State: "available"},
			{ID: "gpu", Kind: "accelerator", TotalBytes: fixtureGPUTotal, AvailableBytes: 9 << 30, State: "available"},
		},
		Backends: []transport.BackendInventory{{
			Backend: "llama", State: "available", Operations: []string{"chat", "embedding"},
			Devices: []transport.DeviceInfo{
				{ID: "cpu", Type: "cpu", Name: "CPU", State: "available", PoolID: "host", Operations: []string{"chat"}},
				{ID: "GPU0", Type: "gpu", Description: "Fixture GPU", State: "available", PoolID: "gpu", Operations: []string{"chat"}, MemoryTotal: fixtureGPUTotal},
			},
		}},
	}, nil
}

func (*ceilingOwner) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{}, errors.New("a ceiling change must not load a model")
}

func (o *ceilingOwner) Admission(context.Context) (admission.Snapshot, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ceilings := map[string]float64{}
	for pool, share := range o.ceilings {
		ceilings[pool] = share
	}
	return admission.Snapshot{Resources: admission.Resources{Ceilings: ceilings}}, nil
}

func (*ceilingOwner) ConfigureAdmission(context.Context, admission.Config) error { return nil }

func (o *ceilingOwner) ConfigureCeilings(_ context.Context, ceilings map[string]float64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.refuse != nil {
		return o.refuse
	}
	o.ceilings = ceilings
	return nil
}

func (o *ceilingOwner) enforced() map[string]float64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.ceilings
}

type placedModels struct {
	llmrepo.ModelRepo
}

func (placedModels) Chat(ctx context.Context, req llmrepo.Request, messages []modelrepo.Message, _ ...modelrepo.ChatArgument) (modelrepo.ChatResult, llmrepo.Meta, error) {
	report := &transport.SessionExecution{
		ModelName: req.ModelNames[0], Backend: "llama", Digest: "fixture-digest",
		Requested: transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementAutomatic}},
		Resolved:  transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "GPU0"}, NumCtx: 8192},
		Capacity: transport.ModelInfo{Allocation: &transport.NativeAllocation{Resources: admission.Allocation{
			Pools: []admission.PoolAllocation{{PoolID: "gpu", Bytes: fixtureChatBytes, TotalBytes: fixtureGPUTotal}},
		}}},
	}
	if err := modeldconn.RecordExecution(ctx, modeldconn.ModelRef{Name: req.ModelNames[0], Type: "llama"}, report); err != nil {
		return modelrepo.ChatResult{}, llmrepo.Meta{}, err
	}
	last := messages[len(messages)-1]
	if last.Role == "tool" {
		return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant", Content: last.Content}}, llmrepo.Meta{}, nil
	}
	call := modelrepo.ToolCall{ID: "setup-call", Type: "function"}
	call.Function.Name, call.Function.Arguments = "contenox_ready", `{"status":"ok"}`
	return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant"}, ToolCalls: []modelrepo.ToolCall{call}}, llmrepo.Meta{}, nil
}

type placedFactory struct{}

func (placedFactory) Models(context.Context) (llmrepo.ModelRepo, func(), error) {
	return placedModels{}, func() {}, nil
}

type nativeFixture struct {
	ctx     context.Context
	owner   *ceilingOwner
	setup   *inferencesetup.Service
	service *autosetup.Service
	store   runtimetypes.Store
}

func newNativeFixture(t *testing.T) *nativeFixture {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", root)
	for _, name := range []string{"chat-model", "embed-model"} {
		dir := filepath.Join(root, "models", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("weights"), 0o644))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	ctx = modeldconn.WithDataRoot(ctx, root)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	holder, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), Endpoint: listener.Addr().String(), Backend: "shared", TTL: 30 * time.Second})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, holder.Release()) })
	fake := &ceilingOwner{MemoryService: transport.NewMemoryService(), NodeAdmin: modelstore.NewNodeAdmin(filepath.Join(root, "models"))}
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, listener, fake, holder.InstanceID(), "shared") }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
		for _, name := range []string{"catalog:" + root, "devices:native", "ceiling:native"} {
			modeldconn.CloseEndpoint(name)
		}
	})
	db, err := libdbexec.NewSQLiteDBManager(context.Background(), filepath.Join(t.TempDir(), "native.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "native", Name: "Local models", Type: "modeld", BaseURL: modeldconn.LocalSentinel}))
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, state.RunBackendCycle(ctx))
	setup, err := inferencesetup.New(db, state, bus, placedFactory{})
	require.NoError(t, err)
	service, err := autosetup.New(db, libtracker.NoopTracker{})
	require.NoError(t, err)
	return &nativeFixture{ctx: ctx, owner: fake, setup: setup, service: service, store: runtimetypes.New(db.WithoutTransaction())}
}

func (f *nativeFixture) assignVerifiedChat(t *testing.T) {
	t.Helper()
	_, err := f.setup.Assign(f.ctx, inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: "modeld", Model: "chat-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
}

func (f *nativeFixture) assignUnverifiedEmbedding(t *testing.T) {
	t.Helper()
	require.NoError(t, clikv.SetString(f.ctx, f.store, "default-embedding-provider", "modeld"))
	require.NoError(t, clikv.SetString(f.ctx, f.store, "default-embedding-model", "embed-model"))
	require.NoError(t, clikv.SetString(f.ctx, f.store, "inference.assignment.embedding", "assigned"))
}

func TestIntegration_ConfigureCeiling_LoweringNeverRequiresVerifiedRolesAndNamesThem(t *testing.T) {
	f := newNativeFixture(t)
	f.assignUnverifiedEmbedding(t)
	result, err := f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "gpu", Share: 0.5})
	require.NoError(t, err, "a role without a verified allocation does not block lowering")
	require.Equal(t, map[string]float64{"gpu": 0.5}, f.owner.enforced())
	require.Equal(t, "Fixture GPU", result.PoolLabel)
	require.Equal(t, fixtureGPUTotal/2, result.LimitBytes)
	require.Empty(t, result.Unfit)
	require.Len(t, result.Unchecked, 1)
	require.Equal(t, "embedding", result.Unchecked[0].Role)
	require.Equal(t, "embed-model", result.Unchecked[0].Model)
	require.Contains(t, result.Ceilings, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "gpu", Share: 0.5})
}

func TestIntegration_ConfigureCeiling_ReportsTheVerifiedRolesThatNoLongerFit(t *testing.T) {
	f := newNativeFixture(t)
	f.assignVerifiedChat(t)
	fits, err := f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "gpu", Share: 0.9})
	require.NoError(t, err)
	require.Empty(t, fits.Unfit, "a role under the limit is not reported")
	require.Equal(t, fixtureChatBytes, fits.HeldBytes)

	lowered, err := f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "gpu", Share: 0.4})
	require.NoError(t, err, "the owner accepts the limit and the result names what no longer fits")
	require.Equal(t, map[string]float64{"gpu": 0.4}, f.owner.enforced())
	require.Len(t, lowered.Unfit, 1)
	require.Equal(t, "primary", lowered.Unfit[0].Role)
	require.Equal(t, "chat-model", lowered.Unfit[0].Model)
	require.Equal(t, fixtureChatBytes, lowered.Unfit[0].Bytes)
	require.Less(t, lowered.LimitBytes, lowered.HeldBytes)
}

func TestIntegration_ConfigureCeiling_RefusesAnUnknownPoolAndAnOwnerThatKeepsLoadedModels(t *testing.T) {
	f := newNativeFixture(t)
	_, err := f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "nonesuch", Share: 0.5})
	require.ErrorContains(t, err, "no available memory pool nonesuch")
	_, err = f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "missing", PoolID: "gpu", Share: 0.5})
	require.ErrorContains(t, err, "not a native connection")
	_, err = f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "gpu", Share: 1.5})
	require.Error(t, err)

	f.owner.refuse = admission.ErrCapacity
	_, err = f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "gpu", Share: 0.2})
	require.ErrorIs(t, err, admission.ErrCapacity)
	require.ErrorContains(t, err, "already hold more of Fixture GPU than 20% allows")
	require.Empty(t, f.owner.enforced(), "a refused change leaves the previous ceiling")
}

func TestIntegration_Devices_ListsPoolsWithTheirDevicesAndCeilings(t *testing.T) {
	f := newNativeFixture(t)
	_, err := f.service.ConfigureCeiling(f.ctx, f.setup, autosetup.PoolCeiling{ConnectionID: "native", PoolID: "gpu", Share: 0.75})
	require.NoError(t, err)
	hosts, err := f.service.Devices(f.ctx, f.setup)
	require.NoError(t, err)
	require.Len(t, hosts, 1)
	require.Equal(t, "Local models", hosts[0].Connection)
	require.Empty(t, hosts[0].Error)
	require.Equal(t, 8, hosts[0].HostThreads)
	pools := map[string]autosetup.PoolView{}
	for _, pool := range hosts[0].Pools {
		pools[pool.ID] = pool
	}
	require.Equal(t, "system memory", pools["host"].Label)
	require.False(t, pools["host"].Bounded)
	require.Equal(t, "Fixture GPU", pools["gpu"].Label)
	require.True(t, pools["gpu"].Bounded)
	require.Equal(t, 0.75, pools["gpu"].Share)
	require.Equal(t, fixtureGPUTotal, pools["gpu"].TotalBytes)
	require.Len(t, pools["gpu"].Devices, 1)
	require.Equal(t, "GPU0", pools["gpu"].Devices[0].ID)
}
