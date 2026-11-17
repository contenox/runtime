package inferencesetup_test

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/clikv"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type proposalCatalog struct {
	*transport.MemoryService
	*modelstore.NodeAdmin
}

func (s proposalCatalog) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{Backends: []transport.BackendInventory{{Backend: "llama", State: "available", Operations: []string{"chat", "autocomplete", "embedding"}}}}, nil
}

func (s proposalCatalog) ListModels(context.Context) ([]transport.NodeModel, error) {
	return []transport.NodeModel{
		{Name: "qwen3.5-2b", Type: "llama", Digest: "chat-digest"},
		{Name: "qwen3-embedding-0.6b", Type: "llama", Digest: "embedding-digest"},
	}, nil
}

func (s proposalCatalog) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{EffectiveContext: 8192, SupportsEmbedding: true}, nil
}

type proposedModels struct {
	llmrepo.ModelRepo
	check         func(context.Context, string)
	omitExecution bool
}

func (m proposedModels) Chat(ctx context.Context, req llmrepo.Request, msgs []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, llmrepo.Meta, error) {
	m.check(ctx, req.WorkerRole)
	if !m.omitExecution {
		if err := proposalExecution(ctx, req.WorkerRole, req.ModelNames[0]); err != nil {
			return modelrepo.ChatResult{}, llmrepo.Meta{}, err
		}
	}
	return m.ModelRepo.Chat(ctx, req, msgs, args...)
}

func (m proposedModels) Embed(ctx context.Context, req llmrepo.EmbedRequest, value string) ([]float64, llmrepo.Meta, error) {
	m.check(ctx, inferencesetup.RoleEmbedding)
	if !m.omitExecution {
		if err := proposalExecution(ctx, inferencesetup.RoleEmbedding, "qwen3-embedding-0.6b"); err != nil {
			return nil, llmrepo.Meta{}, err
		}
	}
	return m.ModelRepo.Embed(ctx, req, value)
}

type proposalFactory struct {
	modelFactory
	check         func(context.Context, string)
	omitExecution bool
}

func (f *proposalFactory) Models(context.Context) (llmrepo.ModelRepo, func(), error) {
	return proposedModels{ModelRepo: f.repo, check: f.check, omitExecution: f.omitExecution}, func() {}, nil
}

func (f *proposalFactory) Complete(ctx context.Context, assignment inferencesetup.Assignment, prefix, suffix string) (string, error) {
	f.check(ctx, assignment.Role)
	if !f.omitExecution {
		if err := proposalExecution(ctx, assignment.Role, assignment.Model); err != nil {
			return "", err
		}
	}
	return f.modelFactory.Complete(ctx, assignment, prefix, suffix)
}

func proposalExecution(ctx context.Context, role, name string) error {
	options, _ := modeldconn.ProposedWorkerOptions(ctx, role)
	digest := "chat-digest"
	if role == inferencesetup.RoleEmbedding {
		digest = "embedding-digest"
	}
	model := modeldconn.ModelRef{Name: name, Type: "llama", Digest: digest}
	return modeldconn.RecordExecution(modeldconn.WithWorkerRole(ctx, role), model, &transport.SessionExecution{
		Backend: model.Type, ModelName: model.Name, Digest: model.Digest,
		Requested: transport.Config{Target: options.Target.Normalize()},
		Resolved:  transport.Config{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, NumCtx: 8192},
	})
}

func TestIntegration_Roles_ProposedSettingsCommitOnlyAfterVerification(t *testing.T) {
	for _, outcome := range []string{"success", "missing-execution", "failed-retrieval", "cancel", "role-aba", "settings-aba", "credential", "review-role", "review-credential", "resolved-settings", "resolved-placement"} {
		t.Run(outcome, func(t *testing.T) {
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
				done <- transportgrpc.Serve(serverCtx, listener, proposalCatalog{transport.NewMemoryService(), modelstore.NewNodeAdmin(t.TempDir())}, "proposal-owner", "shared")
			}()
			t.Cleanup(func() { stop(); require.NoError(t, <-done) })
			state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
			require.NoError(t, err)
			backend := &runtimetypes.Backend{ID: "local-connection", Name: "Local", Type: "modeld", BaseURL: listener.Addr().String()}
			require.NoError(t, backendservice.New(db).Create(ctx, backend))
			require.NoError(t, state.RunProviderCycle(ctx, backend.ID))
			models := &verifyingModels{}
			factory := &proposalFactory{modelFactory: modelFactory{repo: models}, check: func(context.Context, string) {}}
			setup, err := inferencesetup.New(db, state, bus, factory)
			require.NoError(t, err)
			store := runtimetypes.New(db.WithoutTransaction())
			old := modeldconn.WorkerOptions{Target: transport.ExecutionTarget{Mode: transport.PlacementAutomatic}, Memory: "2GiB"}
			next := modeldconn.WorkerOptions{Target: transport.ExecutionTarget{Mode: transport.PlacementCPU}, Memory: "3GiB"}
			if outcome == "resolved-settings" {
				next.Context = 16384
			}
			if outcome == "resolved-placement" {
				next.Target = transport.ExecutionTarget{Mode: transport.PlacementAccelerator, DeviceID: "pci:01:00.0"}
			}
			save := func(role string, options modeldconn.WorkerOptions) {
				raw, err := json.Marshal(options)
				require.NoError(t, err)
				require.NoError(t, clikv.SetString(ctx, store, "inference.modeld."+role, string(raw)))
			}
			var routes []inferencesetup.Assignment
			var proposals []inferencesetup.RouteProposal
			for _, role := range []string{"primary", "alternative", "autocomplete", "embedding"} {
				model := "qwen3.5-2b"
				if role == "embedding" {
					model = "qwen3-embedding-0.6b"
				}
				route := inferencesetup.Assignment{Role: role, Provider: runtimestate.ConnectionSelector(backend.ID), Model: model}
				routes = append(routes, route)
				proposals = append(proposals, inferencesetup.RouteProposal{Assignment: route, SettingsChange: inferencesetup.ReplaceSettings, Settings: next})
				save(role, old)
			}
			original, err := setup.AssignAll(ctx, routes, inferencesetup.SourceUser)
			require.NoError(t, err)
			for _, verification := range original {
				require.NotEmpty(t, verification.NativeExecutions)
			}
			factory.omitExecution = outcome == "missing-execution"
			verifyCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			checked := map[string]bool{}
			factory.check = func(request context.Context, role string) {
				options, proposed := modeldconn.ProposedWorkerOptions(request, role)
				require.True(t, proposed)
				require.Equal(t, next, options)
				live, err := modelworkers.ReadOptions(ctx, store, role)
				require.NoError(t, err)
				require.Equal(t, old, live)
				if checked[role] {
					return
				}
				checked[role] = true
				if role != "primary" {
					return
				}
				switch outcome {
				case "failed-retrieval":
					models.failRole = "embedding"
				case "cancel":
					cancel()
				case "role-aba":
					require.NoError(t, clikv.SetString(ctx, store, "default-model", "another-model"))
					require.NoError(t, clikv.SetString(ctx, store, "default-model", routes[0].Model))
				case "settings-aba":
					save(role, next)
					save(role, old)
				case "credential":
					require.NoError(t, store.SetKV(ctx, runtimestate.BackendCredentialKey("modeld", backend.ID), []byte(`{"type":"modeld","apiKey":"edited"}`)))
				}
			}
			proposals, err = setup.Review(ctx, proposals)
			require.NoError(t, err)
			if outcome == "review-role" {
				require.NoError(t, clikv.SetString(ctx, store, "default-model", "concurrent-edit"))
				require.NoError(t, clikv.SetString(ctx, store, "default-model", routes[0].Model))
			}
			if outcome == "review-credential" {
				require.NoError(t, store.SetKV(ctx, runtimestate.BackendCredentialKey("modeld", backend.ID), []byte(`{"type":"modeld","apiKey":"edited-before-verification"}`)))
			}
			verified, err := setup.Apply(verifyCtx, proposals, inferencesetup.SourceUser)
			if outcome == "resolved-settings" || outcome == "resolved-placement" {
				var revision *inferencesetup.ProposalRevision
				require.ErrorAs(t, err, &revision)
				require.Len(t, revision.Proposals, len(proposals))
				require.NotEqual(t, proposals[0].Fingerprint, revision.Proposals[0].Fingerprint)
				require.Equal(t, 8192, revision.Proposals[0].Settings.Context)
				require.Equal(t, transport.PlacementCPU, revision.Proposals[0].Settings.Target.Mode)
				require.Equal(t, proposals[0].RoleRevision, revision.Proposals[0].RoleRevision)
				require.Equal(t, proposals[1:], revision.Proposals[1:])
			}
			if outcome == "review-role" || outcome == "review-credential" {
				require.ErrorIs(t, err, inferencesetup.ErrConfigurationChanged)
				require.Empty(t, checked)
			}
			if outcome == "success" {
				require.NoError(t, err)
				require.Len(t, checked, len(routes))
				original = verified
			} else {
				require.Error(t, err)
				if outcome == "role-aba" || outcome == "settings-aba" || outcome == "credential" {
					require.ErrorIs(t, err, inferencesetup.ErrConfigurationChanged)
				}
			}
			for index, route := range routes {
				options, err := modelworkers.ReadOptions(ctx, store, route.Role)
				require.NoError(t, err)
				if outcome == "success" {
					require.Equal(t, next, options)
				} else {
					require.Equal(t, old, options)
				}
				var evidence inferencesetup.Verification
				require.NoError(t, store.GetKV(ctx, "inference.setup.verification."+route.Role, &evidence))
				require.Equal(t, original[index], evidence)
			}
			if outcome == "success" {
				restarted, err := inferencesetup.New(db, state, bus, factory)
				require.NoError(t, err)
				ready, err := restarted.Ready(ctx)
				require.NoError(t, err)
				require.True(t, ready)
			}
		})
	}
}
