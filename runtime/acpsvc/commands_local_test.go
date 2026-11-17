package acpsvc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestIntegration_LocalInferenceCommands(t *testing.T) {
	root := t.TempDir()
	ctx := modeldconn.WithDataRoot(t.Context(), root)
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "config.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	tr := &Transport{deps: Deps{DB: db}}
	wireTestInference(t, tr)
	require.Contains(t, commandNames(tr.acpCommands()), "local")
	for _, command := range tr.acpCommands() {
		if command.Name != "local" {
			continue
		}
		var meta libacp.CommandCompletionMeta
		require.NoError(t, json.Unmarshal(command.Meta, &meta))
		require.ElementsMatch(t, []string{"prepare", "models", "pull", "devices", "ceiling"}, meta.Completions[""].Values)
		require.Contains(t, meta.Completions["pull"].Values, "qwen3.5-2b")
	}
	for _, test := range []struct {
		command  string
		args     []string
		contains string
		failed   bool
	}{
		{"providers", nil, "No provider is connected", false},
		{"roles", nil, "embedding", false},
		{"local", []string{"models"}, "qwen3.5-2b", false},
		{"local", []string{"devices"}, "No local connection is saved", false},
		{"local", []string{"pull", "unknown-model"}, "is not a downloadable model", true},
		{"role", []string{"unknown", "auto"}, "unknown role", true},
		{"completion", []string{"missing", "model", "invalid"}, "unknown completion mode", true},
	} {
		t.Run(test.command+"/"+test.contains, func(t *testing.T) {
			out, err := tr.dispatchInferenceCommand(ctx, test.command, test.args)
			if test.failed {
				require.ErrorContains(t, err, test.contains)
				return
			}
			require.NoError(t, err)
			require.Contains(t, out, test.contains)
		})
	}
}

func wireTestInference(t *testing.T, tr *Transport) {
	t.Helper()
	bus := libbus.NewInMem()
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	tr.deps.OpenInference = func(ctx context.Context) (*inferencesetup.Service, *runtimestate.State, func(), error) {
		state, err := runtimestate.New(ctx, tr.deps.DB, bus, runtimestate.WithAutoDiscoverModels())
		if err != nil {
			return nil, nil, nil, err
		}
		service, err := inferencesetup.New(tr.deps.DB, state, bus, inferencesetup.RuntimeModelFactory{DB: tr.deps.DB, Bus: bus, State: state, Tracker: libtracker.NoopTracker{}})
		return service, state, func() {}, err
	}
}

func TestUnit_InferenceCommandsRequireSetupService(t *testing.T) {
	commands := (&Transport{}).acpCommands()
	for _, name := range []string{"providers", "roles", "role", "local", "completion"} {
		require.NotContains(t, commandNames(commands), name)
	}
}
