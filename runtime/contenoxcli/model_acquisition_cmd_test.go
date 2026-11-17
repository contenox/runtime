package contenoxcli

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/stretchr/testify/require"
)

type pullSetup struct{}

func (pullSetup) ConnectProvider(context.Context, inferencesetup.ProviderRequest) (inferencesetup.ProviderResult, error) {
	return inferencesetup.ProviderResult{ID: "local"}, nil
}

type pullState struct{}

func (pullState) RefreshProviderModels(context.Context, ...string) error { return nil }

type pullRegistry struct{}

func (pullRegistry) Resolve(_ context.Context, name string) (*modelregistry.ModelDescriptor, error) {
	if name != "local-model" {
		return nil, modelregistry.ErrNotFound
	}
	return &modelregistry.ModelDescriptor{Name: name}, nil
}

func (pullRegistry) List(context.Context) ([]modelregistry.ModelDescriptor, error) {
	return []modelregistry.ModelDescriptor{{Name: "local-model"}}, nil
}

func (pullRegistry) OptimalFor(context.Context, string) (string, error) { return "local-model", nil }

type pullInstaller struct{}

func (pullInstaller) Ensure(_ context.Context, _ modelregistry.Registry, _ string, options modelstore.AutoPullOptions) error {
	return options.Progress(modelstore.DownloadProgress{Downloaded: 4, Total: 8})
}

func (pullInstaller) EnsureAt(ctx context.Context, registry modelregistry.Registry, model, _ string, options modelstore.AutoPullOptions) error {
	return pullInstaller{}.Ensure(ctx, registry, model, options)
}

func TestUnit_ModelRegistryListCmd_ListsTheCuratedCatalogAndRendersTheDeclaredHelp(t *testing.T) {
	require.Equal(t, modelacquisition.Contract.Operations[0].Description, modelRegistryListCmd.Long)
	cmd := testCobraCmd()
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	require.NoError(t, runModelRegistryList(cmd, nil))
	require.Contains(t, out.String(), "NAME")
	require.Contains(t, out.String(), "qwen3.5-2b")
}

func TestUnit_ModelPullCmd_RefusesAnUnknownModelNamingTheCatalogCommand(t *testing.T) {
	require.Equal(t, modelacquisition.Contract.Operations[1].Description, modelPullCmd.Long)
	cmd := testCobraCmd()
	cmd.SetContext(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	require.NoError(t, cmd.Root().PersistentFlags().Set("db", filepath.Join(t.TempDir(), "pull.db")))
	err := modelPullCmd.RunE(cmd, []string{"no-such-model"})
	require.ErrorContains(t, err, `"no-such-model" is not a downloadable model`)
	require.ErrorContains(t, err, "contenox-runtime model registry-list")
}

func TestUnit_PullModel_ReportsProgressAndWhereTheModelIsAvailable(t *testing.T) {
	root := t.TempDir()
	service, err := modelacquisition.New(openAcquisitionDB(t), pullSetup{}, pullState{}, pullRegistry{}, pullInstaller{}, root, libtracker.NoopTracker{})
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, pullModel(context.Background(), service, "local-model", root, &out))
	require.Contains(t, out.String(), "Downloading local-model: 4 B / 8 B")
	require.Contains(t, out.String(), "Model local-model available in "+modelstore.Dir(root, ""))
	err = pullModel(context.Background(), service, "other", root, &out)
	require.ErrorContains(t, err, "contenox-runtime model registry-list")
}

func openAcquisitionDB(t *testing.T) libdb.DBManager {
	t.Helper()
	db, err := OpenDBAt(context.Background(), filepath.Join(t.TempDir(), "acquisition.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}
