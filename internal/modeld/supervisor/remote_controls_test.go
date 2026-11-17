package supervisor_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/modelworkers"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_RemoteControlsUseOnlySelectedOwner(t *testing.T) {
	a, b := sharedOwner(t), sharedOwner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, fixture := range []ownerFixture{a, b} {
		client := connectOwner(t, fixture)
		session, err := client.OpenSession(ctx, roleRequest(fixture, "primary"))
		require.NoError(t, err)
		require.NoError(t, session.Close())
	}
	local := t.TempDir()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(local, "settings.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	for id, fixture := range map[string]ownerFixture{"a": a, "b": b} {
		require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: id, Name: id, Type: "modeld", BaseURL: fixture.address}))
		t.Cleanup(func() { modeldconn.CloseEndpoint("controls:" + id) })
	}
	workers, err := modeldconn.NewConfiguredWorkers(ctx, local, func(context.Context, string) (modeldconn.WorkerOptions, error) {
		return modeldconn.WorkerOptions{}, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, workers.Close()) })
	service, err := modelworkers.New(db, workers)
	require.NoError(t, err)
	target := modelworkers.InventoryRequest{Target: "connection", ConnectionID: "a"}
	snapshot, err := service.ExecuteAt(ctx, target, modelworkers.Request{Action: "status"})
	require.NoError(t, err)
	require.Empty(t, snapshot.OwnerError)
	require.Len(t, snapshot.Workers, 1)
	instance := snapshot.Workers[0].Instance
	inspection, err := service.InspectAt(ctx, target, instance)
	require.NoError(t, err)
	require.Equal(t, instance, inspection.Status.OwnerInstanceID)
	_, err = service.InspectAt(ctx, modelworkers.InventoryRequest{Target: "connection", ConnectionID: "b"}, instance)
	require.Error(t, err)
	policy := snapshot.Admission
	policy.QueueCapacity++
	_, err = service.ExecuteAt(ctx, target, modelworkers.Request{Action: "admission", Policy: policy})
	require.NoError(t, err)
	other, err := service.ExecuteAt(ctx, modelworkers.InventoryRequest{Target: "connection", ConnectionID: "b"}, modelworkers.Request{Action: "status"})
	require.NoError(t, err)
	require.Equal(t, snapshot.Admission, other.Admission)
	stopped, err := service.ExecuteAt(ctx, target, modelworkers.Request{Action: "stop", Instance: instance})
	require.NoError(t, err)
	require.Empty(t, stopped.Workers)
	require.Len(t, b.pool.Snapshot(), 1)
	_, err = os.Stat(filepath.Join(local, "modeld.lease"))
	require.True(t, os.IsNotExist(err))
}

type remoteRegistry struct{ model modelregistry.ModelDescriptor }

func (r remoteRegistry) Resolve(context.Context, string) (*modelregistry.ModelDescriptor, error) {
	return &r.model, nil
}
func (r remoteRegistry) List(context.Context) ([]modelregistry.ModelDescriptor, error) {
	return []modelregistry.ModelDescriptor{r.model}, nil
}
func (r remoteRegistry) OptimalFor(context.Context, string) (string, error) { return r.model.Name, nil }

func TestUnit_RemoteAcquisitionCannotReuseOrReplaceLocalArtifact(t *testing.T) {
	remote := sharedOwner(t)
	local := t.TempDir()
	localModel := filepath.Join(local, "models", "fixture", "model.gguf")
	require.NoError(t, os.MkdirAll(filepath.Dir(localModel), 0o700))
	require.NoError(t, os.WriteFile(localModel, []byte("local-only"), 0o600))
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := fmt.Fprint(w, "remote-complete")
		require.NoError(t, err)
	}))
	t.Cleanup(source.Close)
	t.Cleanup(func() { modeldconn.CloseEndpoint("acquisition:" + remote.address) })
	registry := remoteRegistry{modelregistry.ModelDescriptor{Name: "fixture", SourceURL: source.URL}}
	err := modeldconn.AcquireModelAt(context.Background(), registry, "fixture", remote.address, modelstore.AutoPullOptions{
		DataRoot: local, ProgressOut: io.Discard, Progress: func(modelstore.DownloadProgress) error { return nil }, Tracker: libtracker.NoopTracker{},
	})
	require.NoError(t, err)
	got, err := os.ReadFile(localModel)
	require.NoError(t, err)
	require.Equal(t, "local-only", string(got))
	got, err = os.ReadFile(filepath.Join(remote.root, "models", "fixture", "model.gguf"))
	require.NoError(t, err)
	require.Equal(t, "remote-complete", string(got))
	staging, err := filepath.Glob(filepath.Join(local, ".remote-acquisition-*"))
	require.NoError(t, err)
	require.Empty(t, staging)
}
