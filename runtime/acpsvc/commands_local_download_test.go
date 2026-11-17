package acpsvc

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelacquisition"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type artifactCatalog struct {
	models []modelregistry.ModelDescriptor
}

func (c artifactCatalog) Resolve(_ context.Context, name string) (*modelregistry.ModelDescriptor, error) {
	for _, model := range c.models {
		if model.Name == name {
			return &model, nil
		}
	}
	return nil, modelregistry.ErrNotFound
}

func (c artifactCatalog) List(context.Context) ([]modelregistry.ModelDescriptor, error) {
	return c.models, nil
}

func (artifactCatalog) OptimalFor(context.Context, string) (string, error) {
	return "", modelregistry.ErrNotFound
}

type storeInstaller struct{ admin *modelstore.NodeAdmin }

func (i storeInstaller) Ensure(ctx context.Context, registry modelregistry.Registry, model string, options modelstore.AutoPullOptions) error {
	options.Receive = i.admin.ReceiveModel
	return modelstore.EnsureModelAvailable(ctx, registry, model, options)
}

func (i storeInstaller) EnsureAt(context.Context, modelregistry.Registry, string, string, modelstore.AutoPullOptions) error {
	return errors.New("remote acquisition is not part of this fixture")
}

type acquiringFactory struct {
	models    llmrepo.ModelRepo
	registry  modelregistry.Registry
	installer modelacquisition.Installer
}

func (f acquiringFactory) Models(context.Context) (llmrepo.ModelRepo, func(), error) {
	return f.models, func() {}, nil
}

func (f acquiringFactory) Acquisition() (modelregistry.Registry, modelacquisition.Installer) {
	return f.registry, f.installer
}

type downloadOwner struct {
	*transport.MemoryService
	*modelstore.NodeAdmin
}

func (*downloadOwner) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{
		HostThreads: 8,
		Pools: []transport.MemoryPool{
			{ID: "host", Kind: "host", TotalBytes: 32 << 30, AvailableBytes: 20 << 30, State: "available"},
			{ID: "gpu", Kind: "accelerator", TotalBytes: 10 << 30, AvailableBytes: 9 << 30, State: "available"},
		},
		Backends: []transport.BackendInventory{{
			Backend: "llama", State: "available", Operations: []string{"chat", "embedding"},
			Devices: []transport.DeviceInfo{
				{ID: "cpu", Type: "cpu", Name: "CPU", State: "available", PoolID: "host", Operations: []string{"chat"}},
				{ID: "GPU0", Type: "gpu", Description: "Fixture GPU", State: "available", PoolID: "gpu", Operations: []string{"chat"}, MemoryTotal: 10 << 30},
			},
		}},
	}, nil
}

func TestIntegration_StopCancelsALocalPullLeavingNothingRegisteredAndARetrySucceeds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", root)
	ctx, cancel := context.WithTimeout(modeldconn.WithDataRoot(t.Context(), root), 15*time.Second)
	defer cancel()
	admin := modelstore.NewNodeAdmin(modelstore.Dir(root, ""))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	holder, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), Endpoint: listener.Addr().String(), Backend: "shared", TTL: time.Minute})
	require.NoError(t, err)
	defer holder.Release()
	defer listener.Close()
	defer modeldconn.CloseEndpoint("catalog:" + root)
	go func() {
		_ = transportgrpc.Serve(ctx, listener, &downloadOwner{transport.NewMemoryService(), admin}, holder.InstanceID(), "shared")
	}()
	release, started := make(chan struct{}), make(chan struct{})
	var first sync.Once
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "8192")
		_, _ = w.Write([]byte(strings.Repeat("w", 4096)))
		w.(http.Flusher).Flush()
		first.Do(func() { close(started) })
		select {
		case <-release:
			_, _ = w.Write([]byte(strings.Repeat("w", 4096)))
		case <-r.Context().Done():
		}
	}))
	defer source.Close()
	var unblock sync.Once
	unblockDownload := func() { unblock.Do(func() { close(release) }) }
	defer unblockDownload()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "runtime.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()
	bus := libbus.NewInMem()
	defer bus.Close()
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	registry := artifactCatalog{models: []modelregistry.ModelDescriptor{{Name: "artifact-model", SourceURL: source.URL + "/model.gguf", SizeBytes: 8192, Curated: true, AutoEligible: true, License: "apache-2.0", AgenticTier: modelregistry.AgenticTierAgentic}}}
	setup, err := inferencesetup.New(db, state, bus, acquiringFactory{registry: registry, installer: storeInstaller{admin}})
	require.NoError(t, err)
	tr := &Transport{deps: Deps{DB: db, OpenInference: func(context.Context) (*inferencesetup.Service, *runtimestate.State, func(), error) {
		return setup, state, func() {}, nil
	}}}
	type reply struct {
		response libacp.PromptResponse
		err      error
	}
	answered := make(chan reply, 1)
	go func() {
		r, err := tr.dispatchCommand(ctx, "pull-session", &sessionEntry{}, "local", "pull artifact-model")
		answered <- reply{r, err}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, tr.Cancel(ctx, libacp.CancelNotification{SessionID: "pull-session"}))
	select {
	case r := <-answered:
		require.NoError(t, r.err)
		require.Equal(t, libacp.StopReasonCancelled, r.response.StopReason)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err = modelstore.Resolve(modelstore.Dir(root, ""), "artifact-model", "llama", "")
	require.ErrorIs(t, err, modelstore.ErrModelNotFound)
	saved, err := runtimetypes.New(db.WithoutTransaction()).ListAllBackends(ctx)
	require.NoError(t, err)
	require.Empty(t, saved)
	unblockDownload()
	out, err := tr.dispatchInferenceCommand(ctx, "local", []string{"pull", "artifact-model"})
	require.NoError(t, err)
	require.Contains(t, out, "Downloaded artifact-model.")
	_, err = modelstore.Resolve(modelstore.Dir(root, ""), "artifact-model", "llama", "")
	require.NoError(t, err)
}
