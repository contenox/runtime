package inferencesetup_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type refusingPrompter struct{}

func (refusingPrompter) Prompt(context.Context, agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
	return nil, errors.New("the instructed chain is not part of a native completion contract")
}

type operationFixture struct {
	factory  *inferencesetup.OperationModelFactory
	service  *inferencesetup.Service
	provider string
	db       libdbexec.DBManager
}

func newOperationFixture(t *testing.T) operationFixture {
	t.Helper()
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = fmt.Fprint(w, `{"models":[{"name":"deepseek-flash","model":"deepseek-flash"}]}`)
		case "/api/show":
			_, _ = fmt.Fprint(w, `{"capabilities":["completion","insert","tools"],"model_info":{"llama.context_length":8192}}`)
		case "/api/generate":
			_, _ = fmt.Fprint(w, `{"model":"deepseek-flash","response":"result","done":true,"done_reason":"stop"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "setup.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	runtime, err := enginesvc.Build(ctx, db, enginesvc.Config{State: state, Bus: bus, SkipBackendCycle: true, Tracker: libtracker.NoopTracker{}})
	require.NoError(t, err)
	t.Cleanup(runtime.Stop)
	factory, err := inferencesetup.NewOperationModelFactory(inferencesetup.OperationDeps{
		DB: db, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}, Models: runtime.Models,
		Agent: refusingPrompter{}, Chain: &taskengine.TaskChainDefinition{ID: "completion"},
	})
	require.NoError(t, err)
	service, err := inferencesetup.New(db, state, bus, factory)
	require.NoError(t, err)
	connection, err := service.ConnectProvider(ctx, inferencesetup.ProviderRequest{Name: "local", Provider: "ollama", Endpoint: server.URL})
	require.NoError(t, err)
	return operationFixture{factory: factory, service: service, provider: runtimestate.ConnectionSelector(connection.ID), db: db}
}

func TestSystem_OperationModelFactory_ServesCompletionForAnInsertModel(t *testing.T) {
	f := newOperationFixture(t)
	text, err := f.factory.Complete(context.Background(), inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: f.provider, Model: "deepseek-flash"}, "def f():", "    return 1")
	require.NoError(t, err)
	require.Equal(t, "result", text)
}

func TestSystem_OperationModelFactory_AssignAllVerifiesAndPersistsAutocomplete(t *testing.T) {
	f := newOperationFixture(t)
	ctx := context.Background()
	assignment := inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: f.provider, Model: "deepseek-flash"}
	verifications, err := f.service.AssignAll(ctx, []inferencesetup.Assignment{assignment}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Len(t, verifications, 1)
	roles, err := f.service.Roles(ctx)
	require.NoError(t, err)
	var found bool
	for _, role := range roles {
		if role.Role == inferencesetup.RoleAutocomplete {
			found = true
			require.Equal(t, "deepseek-flash", role.Model)
			require.NotNil(t, role.Verification, "the persisted autocomplete assignment carries its verification: %+v", role)
		}
	}
	require.True(t, found)
}

func TestUnit_OperationModelFactory_RefusesAMissingDependencyByName(t *testing.T) {
	_, err := inferencesetup.NewOperationModelFactory(inferencesetup.OperationDeps{})
	require.Error(t, err)
	for _, name := range []string{"database", "bus", "runtime state", "tracker", "model repository", "completion agent", "completion chain"} {
		require.Contains(t, err.Error(), name)
	}
}
