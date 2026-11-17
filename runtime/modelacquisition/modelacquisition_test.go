package modelacquisition_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type setupRecorder struct {
	request        inferencesetup.ProviderRequest
	discoveryError string
}

func (s *setupRecorder) ConnectProvider(_ context.Context, request inferencesetup.ProviderRequest) (inferencesetup.ProviderResult, error) {
	s.request = request
	return inferencesetup.ProviderResult{ID: "saved-local", DiscoveryError: s.discoveryError}, nil
}

type stateRecorder struct {
	provider string
}

func (s *stateRecorder) RefreshProviderModels(_ context.Context, providers ...string) error {
	s.provider = providers[0]
	return nil
}

type registryStub struct{}

func (registryStub) Resolve(context.Context, string) (*modelregistry.ModelDescriptor, error) {
	return &modelregistry.ModelDescriptor{Name: "local-model"}, nil
}

func (registryStub) List(context.Context) ([]modelregistry.ModelDescriptor, error) {
	return []modelregistry.ModelDescriptor{{Name: "local-model"}}, nil
}

func (registryStub) OptimalFor(context.Context, string) (string, error) {
	return "local-model", nil
}

type installerRecorder struct {
	model    string
	endpoint string
}

func (i *installerRecorder) Ensure(_ context.Context, _ modelregistry.Registry, model string, options modelstore.AutoPullOptions) error {
	i.model = model
	return options.Progress(modelstore.DownloadProgress{Downloaded: 4, Total: 8})
}

func (i *installerRecorder) EnsureAt(ctx context.Context, registry modelregistry.Registry, model, endpoint string, options modelstore.AutoPullOptions) error {
	i.endpoint = endpoint
	return i.Ensure(ctx, registry, model, options)
}

func TestUnit_PullToRemoteRefreshesOnlySelectedEndpoint(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "acquisition.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	backends := backendservice.New(db)
	for _, backend := range []*runtimetypes.Backend{
		{ID: "local", Name: "local", Type: "modeld", BaseURL: "local"},
		{ID: "remote-a", Name: "remote-a", Type: "modeld", BaseURL: "192.0.2.1:9090"},
		{ID: "remote-b", Name: "remote-b", Type: "modeld", BaseURL: "192.0.2.2:9090"},
	} {
		require.NoError(t, backends.Create(ctx, backend))
	}
	state, installer, setup := &stateRecorder{}, &installerRecorder{}, &setupRecorder{}
	service, err := modelacquisition.New(db, setup, state, registryStub{}, installer, root, libtracker.NoopTracker{})
	require.NoError(t, err)
	require.NoError(t, service.PullTo(ctx, modelacquisition.Request{Target: "connection", ConnectionID: "remote-b", Model: "same-model"}, func(modelacquisition.DownloadProgress) error { return nil }))
	require.Equal(t, "192.0.2.2:9090", installer.endpoint)
	require.Equal(t, "same-model", installer.model)
	require.Equal(t, runtimestate.ConnectionSelector("remote-b"), state.provider)
	require.Empty(t, setup.request)
}

func TestUnit_PullRegistersAcquiresAndRefreshesLocalProvider(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "acquisition.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	setup := &setupRecorder{}
	state := &stateRecorder{}
	installer := &installerRecorder{}
	service, err := modelacquisition.New(db, setup, state, registryStub{}, installer, t.TempDir(), libtracker.NoopTracker{})
	require.NoError(t, err)
	var progress modelacquisition.DownloadProgress
	require.NoError(t, service.Pull(ctx, "local-model", func(value modelacquisition.DownloadProgress) error {
		progress = value
		return nil
	}))
	require.Equal(t, "modeld", setup.request.Provider)
	require.Equal(t, "local", setup.request.Endpoint)
	require.Equal(t, "local-model", installer.model)
	require.Equal(t, int64(4), progress.Downloaded)
	require.Empty(t, state.provider)
}

func TestUnit_PullDiscoveryFailureRetainsAcquisitionAndSavedConnection(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "acquisition.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	setup := &setupRecorder{discoveryError: "worker unavailable"}
	installer := &installerRecorder{}
	service, err := modelacquisition.New(db, setup, &stateRecorder{}, registryStub{}, installer, t.TempDir(), libtracker.NoopTracker{})
	require.NoError(t, err)
	err = service.Pull(ctx, "local-model", func(modelacquisition.DownloadProgress) error { return nil })
	require.ErrorContains(t, err, "model acquired and connection saved-local saved")
	require.ErrorContains(t, err, "discovery failed: worker unavailable")
	require.Equal(t, "local-model", installer.model)
	require.Equal(t, "local", setup.request.Endpoint)
}

func TestUnit_PullRefreshesOnlyConnectionsSharingLocalArtifacts(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "acquisition.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	backends := backendservice.New(db)
	local := &runtimetypes.Backend{Name: "local", Type: "modeld", BaseURL: "local"}
	require.NoError(t, backends.Create(ctx, local))
	require.NoError(t, backends.Create(ctx, &runtimetypes.Backend{Name: "unrelated remote", Type: "modeld", BaseURL: "192.0.2.1:9090"}))
	setup := &setupRecorder{}
	state := &stateRecorder{}
	service, err := modelacquisition.New(db, setup, state, registryStub{}, &installerRecorder{}, t.TempDir(), libtracker.NoopTracker{})
	require.NoError(t, err)
	require.NoError(t, service.Pull(ctx, "local-model", func(modelacquisition.DownloadProgress) error { return nil }))
	require.Equal(t, runtimestate.ConnectionSelector(local.ID), state.provider)
	require.Empty(t, setup.request.Provider)
}
