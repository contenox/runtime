package inferencesetup_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/admission"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type schedulingModels struct {
	llmrepo.ModelRepo
	reports map[string]transport.SessionExecution
}

func (m schedulingModels) record(ctx context.Context, role string) error {
	report := m.reports[role]
	return modeldconn.RecordExecution(modeldconn.WithWorkerRole(ctx, role), modeldconn.ModelRef{Name: report.ModelName, Type: report.Backend, Digest: report.Digest}, &report)
}

func (m schedulingModels) Chat(ctx context.Context, request llmrepo.Request, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, llmrepo.Meta, error) {
	if err := m.record(ctx, request.WorkerRole); err != nil {
		return modelrepo.ChatResult{}, llmrepo.Meta{}, err
	}
	return m.ModelRepo.Chat(ctx, request, messages, args...)
}

type schedulingFactory struct {
	modelFactory
	models schedulingModels
}

type nextEditSchedulingCatalog struct{ proposalCatalog }

func (s nextEditSchedulingCatalog) Inventory(ctx context.Context) (transport.Inventory, error) {
	inventory, err := s.proposalCatalog.Inventory(ctx)
	if err != nil {
		return transport.Inventory{}, err
	}
	inventory.Backends[0].Operations = append(inventory.Backends[0].Operations, "next-edit")
	return inventory, nil
}

func (s nextEditSchedulingCatalog) ListModels(ctx context.Context) ([]transport.NodeModel, error) {
	models, err := s.proposalCatalog.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return append(models, transport.NodeModel{Name: "sweep-next-edit-1.5b", Type: "llama", Digest: "edit-digest"}), nil
}

func (f schedulingFactory) Models(context.Context) (llmrepo.ModelRepo, func(), error) {
	return f.models, func() {}, nil
}

func (f schedulingFactory) Complete(ctx context.Context, assignment inferencesetup.Assignment, prefix, suffix string) (string, error) {
	if err := f.models.record(ctx, assignment.Role); err != nil {
		return "", err
	}
	return f.modelFactory.Complete(ctx, assignment, prefix, suffix)
}

func (f schedulingFactory) NextEdit(ctx context.Context, assignment inferencesetup.Assignment, input modelcontract.NextEditInput) ([]modelcontract.TextEdit, error) {
	if err := f.models.record(ctx, assignment.Role); err != nil {
		return nil, err
	}
	return []modelcontract.TextEdit{{Start: input.Start, End: input.End, OldText: input.Current[input.Start:input.End], NewText: "right"}}, nil
}

func (schedulingFactory) NextEditExecutionFingerprint(context.Context, string) (string, error) {
	return "fixture-next-edit-execution", nil
}

func TestIntegration_Roles_ProtectedSingleRoleChecksPreservedNativeExecution(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		yield      bool
		shared     bool
		cpu        bool
		threads    int
		serialized bool
		accept     bool
	}{
		{name: "cooperative", yield: true, threads: 2, accept: true},
		{name: "shared-worker", yield: true, shared: true, threads: 1},
		{name: "non-yielding-gpu", threads: 2},
		{name: "independent-cpu", cpu: true, threads: 2, accept: true},
		{name: "threads-exhausted", cpu: true, threads: 6},
		{name: "serialized-reviewed", threads: 2, serialized: true, accept: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "settings.db"), runtimetypes.SchemaSQLite)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			bus := libbus.NewSQLite(db.WithoutTransaction())
			t.Cleanup(func() { require.NoError(t, bus.Close()) })
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			serverCtx, stop := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() {
				done <- transportgrpc.Serve(serverCtx, listener, proposalCatalog{transport.NewMemoryService(), modelstore.NewNodeAdmin(t.TempDir())}, "scheduling-owner", "shared")
			}()
			t.Cleanup(func() { stop(); require.NoError(t, <-done) })
			state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
			require.NoError(t, err)
			backend := &runtimetypes.Backend{ID: "scheduling", Name: "Scheduling", Type: "modeld", BaseURL: listener.Addr().String()}
			require.NoError(t, backendservice.New(db).Create(ctx, backend))
			require.NoError(t, state.RunProviderCycle(ctx, backend.ID))
			reports := map[string]transport.SessionExecution{}
			routes := []inferencesetup.RouteProposal{}
			for _, role := range []string{inferencesetup.RoleAutocomplete, inferencesetup.RoleChat} {
				threads, target, identity := 1, transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "gpu"}, role
				if role == inferencesetup.RoleChat {
					threads = scenario.threads
					if scenario.cpu {
						target = transport.ExecutionTarget{Mode: transport.PlacementCPU}
					}
					if scenario.shared {
						identity = inferencesetup.RoleAutocomplete
					}
				}
				pools := []admission.PoolAllocation{{PoolID: "host", Bytes: 100, AvailableBytes: 10000, LimitBytes: 10000}}
				if target.Mode == transport.PlacementAccelerator {
					pools = append(pools, admission.PoolAllocation{PoolID: "gpu", Bytes: 100, AvailableBytes: 10000, LimitBytes: 10000})
				}
				config := transport.Config{Target: target, NumThreads: threads, NumCtx: 8192}
				reports[role] = transport.SessionExecution{OwnerEpoch: "owner", WorkerIdentity: identity, ModelName: "qwen3.5-2b", Digest: "chat-digest", Backend: "llama", Requested: config, Resolved: config,
					Capacity: transport.ModelInfo{Allocation: &transport.NativeAllocation{Resources: admission.Allocation{Identity: identity, CanYield: scenario.yield, Threads: threads, ThreadLimit: 6, Pools: pools}}}}
				routes = append(routes, inferencesetup.RouteProposal{Assignment: inferencesetup.Assignment{Role: role, Provider: runtimestate.ConnectionSelector(backend.ID), Model: "qwen3.5-2b"}, SettingsChange: inferencesetup.ReplaceSettings,
					Settings: modeldconn.WorkerOptions{Target: target, Backend: "llama", Context: 8192, Threads: threads}})
			}
			models := &verifyingModels{}
			setup, err := inferencesetup.New(db, state, bus, schedulingFactory{modelFactory: modelFactory{repo: models}, models: schedulingModels{ModelRepo: models, reports: reports}})
			require.NoError(t, err)
			routes, err = setup.Review(ctx, routes)
			require.NoError(t, err)
			_, err = setup.Apply(ctx, routes, inferencesetup.SourcePlanner)
			require.NoError(t, err)
			planned := mustRoles(t, setup, ctx)
			require.Equal(t, inferencesetup.SourcePlanner, roleByID(t, planned, inferencesetup.RoleAutocomplete).Source)
			require.Equal(t, inferencesetup.SourcePlanner, roleByID(t, planned, inferencesetup.RoleChat).Source)
			proposal := routes[0]
			proposal.RoleRevision, proposal.Fingerprint = "", ""
			proposal.Settings.SchedulingMode = "protected"
			if scenario.serialized {
				proposal.Settings.SchedulingMode = "serialized"
			}
			reviewed, err := setup.Review(ctx, []inferencesetup.RouteProposal{proposal})
			require.NoError(t, err)
			_, err = setup.Apply(ctx, reviewed, inferencesetup.SourceUser)
			if scenario.accept {
				require.NoError(t, err)
				chosen := mustRoles(t, setup, ctx)
				require.Equal(t, inferencesetup.SourceUser, roleByID(t, chosen, inferencesetup.RoleAutocomplete).Source, "a user apply over a planner-set role flips its source")
				require.Equal(t, inferencesetup.SourcePlanner, roleByID(t, chosen, inferencesetup.RoleChat).Source)
			} else {
				require.ErrorContains(t, err, "completion protection")
			}
			options, err := modelworkers.ReadAssignedOptions(ctx, runtimetypes.New(db.WithoutTransaction()), inferencesetup.RoleAutocomplete)
			require.NoError(t, err)
			if scenario.accept {
				require.Equal(t, proposal.Settings, options)
			} else {
				require.Equal(t, routes[0].Settings, options)
			}
		})
	}
}

func TestIntegration_Roles_DisabledNextEditDoesNotBlockChangesAndEnableChecksProtection(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "next-edit-scheduling.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	serverCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		catalog := nextEditSchedulingCatalog{proposalCatalog{MemoryService: transport.NewMemoryService(), NodeAdmin: modelstore.NewNodeAdmin(t.TempDir())}}
		done <- transportgrpc.Serve(serverCtx, listener, catalog, "next-edit-scheduling-owner", "shared")
	}()
	t.Cleanup(func() { stop(); require.NoError(t, <-done) })
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	backend := &runtimetypes.Backend{ID: "next-edit-scheduling", Name: "Next Edit scheduling", Type: "modeld", BaseURL: listener.Addr().String()}
	require.NoError(t, backendservice.New(db).Create(ctx, backend))
	require.NoError(t, state.RunProviderCycle(ctx, backend.ID))
	selector := runtimestate.ConnectionSelector(backend.ID)
	shared := transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "gpu"}, NumThreads: 1, NumCtx: 8192, KVCacheType: "f16"}
	sharedAllocation := transport.ModelInfo{Allocation: &transport.NativeAllocation{Resources: admission.Allocation{Identity: "shared-worker", Threads: 1, ThreadLimit: 6, Pools: []admission.PoolAllocation{{PoolID: "host", Bytes: 100, AvailableBytes: 10000, LimitBytes: 10000}, {PoolID: "gpu", Bytes: 100, AvailableBytes: 10000, LimitBytes: 10000}}}}}
	reports := map[string]transport.SessionExecution{
		inferencesetup.RoleAutocomplete: {OwnerEpoch: "owner", WorkerIdentity: "shared-worker", ModelName: "qwen3.5-2b", Digest: "chat-digest", Backend: "llama", Requested: shared, Resolved: shared, Capacity: sharedAllocation},
		inferencesetup.RoleNextEdit:     {OwnerEpoch: "owner", WorkerIdentity: "shared-worker", ModelName: "sweep-next-edit-1.5b", Digest: "edit-digest", Backend: "llama", Requested: shared, Resolved: shared, Capacity: sharedAllocation},
	}
	models := &verifyingModels{}
	setup, err := inferencesetup.New(db, state, bus, schedulingFactory{modelFactory: modelFactory{repo: models}, models: schedulingModels{ModelRepo: models, reports: reports}})
	require.NoError(t, err)
	initial := []inferencesetup.RouteProposal{
		{Assignment: inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: selector, Model: "qwen3.5-2b"}, SettingsChange: inferencesetup.ReplaceSettings,
			Settings: modeldconn.WorkerOptions{Target: shared.Target, Backend: "llama", Context: 8192, Threads: 1, SchedulingMode: "serialized"}},
		{Assignment: inferencesetup.Assignment{Role: inferencesetup.RoleNextEdit, Provider: selector, Model: "sweep-next-edit-1.5b"}, SettingsChange: inferencesetup.ReplaceSettings,
			Settings: modeldconn.WorkerOptions{Target: shared.Target, Backend: "llama", Context: 8192, Threads: 1, SchedulingMode: "protected"}},
	}
	reviewed, err := setup.Review(ctx, initial)
	require.NoError(t, err)
	_, err = setup.Apply(ctx, reviewed, inferencesetup.SourceUser)
	require.NoError(t, err)
	roles, err := setup.Roles(ctx)
	require.NoError(t, err)
	nextEdit := roleByID(t, roles, inferencesetup.RoleNextEdit)
	require.False(t, nextEdit.Enabled)
	completion := initial[0]
	completion.Settings.SchedulingMode = "protected"
	reviewed, err = setup.Review(ctx, []inferencesetup.RouteProposal{completion})
	require.NoError(t, err)
	_, err = setup.Apply(ctx, reviewed, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.ErrorContains(t, setup.SetNextEditEnabled(ctx, true), "next-edit and Autocomplete cannot provide the reviewed completion protection")
	roles, err = setup.Roles(ctx)
	require.NoError(t, err)
	nextEdit = roleByID(t, roles, inferencesetup.RoleNextEdit)
	require.False(t, nextEdit.Enabled)
}
