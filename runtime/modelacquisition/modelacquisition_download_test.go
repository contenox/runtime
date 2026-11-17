package modelacquisition_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type artifactRegistry struct {
	models []modelregistry.ModelDescriptor
}

func (r artifactRegistry) Resolve(_ context.Context, name string) (*modelregistry.ModelDescriptor, error) {
	for _, model := range r.models {
		if model.Name == name {
			return &model, nil
		}
	}
	return nil, modelregistry.ErrNotFound
}

func (r artifactRegistry) List(context.Context) ([]modelregistry.ModelDescriptor, error) {
	return r.models, nil
}

func (r artifactRegistry) OptimalFor(context.Context, string) (string, error) {
	return "", modelregistry.ErrNotFound
}

type storeInstaller struct {
	admin *modelstore.NodeAdmin
}

func (i storeInstaller) Ensure(ctx context.Context, registry modelregistry.Registry, model string, options modelstore.AutoPullOptions) error {
	options.Receive = i.admin.ReceiveModel
	return modelstore.EnsureModelAvailable(ctx, registry, model, options)
}

func (i storeInstaller) EnsureAt(context.Context, modelregistry.Registry, string, string, modelstore.AutoPullOptions) error {
	return fmt.Errorf("remote acquisition is not part of this fixture")
}

type artifactFixture struct {
	root     string
	registry artifactRegistry
	release  chan struct{}
	service  *modelacquisition.Service
	setup    *setupRecorder
	state    *stateRecorder
}

func newArtifactFixture(t *testing.T) *artifactFixture {
	t.Helper()
	body := []byte(strings.Repeat("w", 4096))
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write(body[:len(body)/2])
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write(body[len(body)/2:])
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	modelsDir := modelstore.Dir(root, "")
	require.NoError(t, os.MkdirAll(modelsDir, 0o755))
	registry := artifactRegistry{models: []modelregistry.ModelDescriptor{
		{Name: "artifact-model", SourceURL: server.URL + "/model.gguf", SizeBytes: int64(len(body)), Curated: true},
		{Name: "other-model", SourceURL: server.URL + "/other.gguf", SizeBytes: 1, Curated: true},
	}}
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "acquisition.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	setup, state := &setupRecorder{}, &stateRecorder{}
	service, err := modelacquisition.New(db, setup, state, registry, storeInstaller{admin: modelstore.NewNodeAdmin(modelsDir)}, root, libtracker.NoopTracker{})
	require.NoError(t, err)
	return &artifactFixture{root: root, registry: registry, release: release, service: service, setup: setup, state: state}
}

func (f *artifactFixture) installed(t *testing.T, name string) bool {
	t.Helper()
	_, err := modelstore.Resolve(modelstore.Dir(f.root, ""), name, "llama", "")
	if err == nil {
		return true
	}
	require.ErrorIs(t, err, modelstore.ErrModelNotFound)
	return false
}

func (f *artifactFixture) leftovers(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(modelstore.Dir(f.root, ""))
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestIntegration_Pull_ReportsProgressAndRegistersOnlyAfterTheWholeBundleArrives(t *testing.T) {
	f := newArtifactFixture(t)
	close(f.release)
	var positions []modelacquisition.DownloadProgress
	require.NoError(t, f.service.Pull(context.Background(), "artifact-model", func(value modelacquisition.DownloadProgress) error {
		positions = append(positions, value)
		return nil
	}))
	require.NotEmpty(t, positions)
	last := positions[len(positions)-1]
	require.Equal(t, int64(4096), last.Total)
	require.Equal(t, last.Total, last.Downloaded)
	require.True(t, f.installed(t, "artifact-model"))
	require.Equal(t, "modeld", f.setup.request.Provider, "the local connection that serves the model is registered after the download")
}

func TestIntegration_Pull_StoppedMidDownloadInstallsAndRegistersNothingAndCanBeRetried(t *testing.T) {
	f := newArtifactFixture(t)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	halfway := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		var once bool
		result <- f.service.Pull(ctx, "artifact-model", func(value modelacquisition.DownloadProgress) error {
			if value.Downloaded > 0 && !once {
				once = true
				close(halfway)
			}
			return nil
		})
	}()
	select {
	case <-halfway:
	case <-time.After(20 * time.Second):
		t.Fatal("the download never started")
	}
	stop()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(20 * time.Second):
		t.Fatal("the stopped download did not return")
	}
	require.False(t, f.installed(t, "artifact-model"), "a stopped download installs no model")
	require.Empty(t, f.setup.request, "a stopped download registers no connection")
	require.Empty(t, f.state.provider)
	require.Empty(t, f.leftovers(t), "a stopped download leaves no staged files")

	close(f.release)
	require.NoError(t, f.service.Pull(context.Background(), "artifact-model", func(modelacquisition.DownloadProgress) error { return nil }))
	require.True(t, f.installed(t, "artifact-model"), "the retry installs the complete bundle")
	require.Equal(t, "modeld", f.setup.request.Provider)
}

func TestIntegration_Pull_RefusesAnUnknownNameListingTheCatalog(t *testing.T) {
	f := newArtifactFixture(t)
	close(f.release)
	err := f.service.Pull(context.Background(), "no-such-model", func(modelacquisition.DownloadProgress) error { return nil })
	var unknown *modelacquisition.UnknownModelError
	require.ErrorAs(t, err, &unknown)
	require.Equal(t, "no-such-model", unknown.Model)
	require.Equal(t, []string{"artifact-model", "other-model"}, unknown.Known)
	require.ErrorContains(t, err, "artifact-model, other-model")
	require.Empty(t, f.leftovers(t))
	require.Empty(t, f.setup.request)
}

func TestIntegration_Library_ReportsWhichCatalogModelsAreAlreadyOnThisComputer(t *testing.T) {
	f := newArtifactFixture(t)
	close(f.release)
	require.NoError(t, f.service.Pull(context.Background(), "artifact-model", func(modelacquisition.DownloadProgress) error { return nil }))
	library, err := f.service.Library(context.Background())
	require.NoError(t, err)
	require.Len(t, library, 2)
	byName := map[string]modelacquisition.LibraryEntry{}
	for _, entry := range library {
		byName[entry.Model.Name] = entry
	}
	require.True(t, byName["artifact-model"].Downloaded)
	require.False(t, byName["other-model"].Downloaded)
	require.Equal(t, inferencesetup.RolesServed(byName["artifact-model"].Model), byName["artifact-model"].Roles)
}
