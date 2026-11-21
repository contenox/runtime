package serverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type registryOwner struct {
	*transport.MemoryService
	*modelstore.NodeAdmin
}

func (*registryOwner) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{Backends: []transport.BackendInventory{{Backend: "llama", State: "available", Operations: []string{"chat"}}}}, nil
}

func TestIntegration_RegistryDownloadUsesModeldAndPreservesConfiguredDefaults(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", t.TempDir())
	ctx, cancel := context.WithTimeout(modeldconn.WithDataRoot(t.Context(), root), 30*time.Second)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	lease, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), Endpoint: listener.Addr().String(), Backend: "shared", TTL: time.Minute})
	require.NoError(t, err)
	defer lease.Release()
	defer modeldconn.CloseEndpoint("catalog:" + root)
	defer modeldconn.CloseEndpoint("acquisition:" + root)
	admin := modelstore.NewNodeAdmin(modelstore.Dir(root, ""))
	go func() {
		_ = transportgrpc.Serve(ctx, listener, &registryOwner{transport.NewMemoryService(), admin}, lease.InstanceID(), "shared")
	}()

	model := "GGUF" + strings.Repeat("w", 8188)
	var transfers atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/weights" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(model)))
		if transfers.Add(1) == 1 {
			_, _ = io.WriteString(w, model[:len(model)/2])
			return
		}
		_, _ = io.WriteString(w, model)
	}))
	defer source.Close()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "runtime.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer db.Close()
	bus := libbus.NewInMem()
	defer bus.Close()
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	mux := http.NewServeMux()
	_, err = New(ctx, mux, "registry-test", "local", nil, Dependencies{DB: db, PubSub: bus, State: state})
	require.NoError(t, err)
	api := httptest.NewServer(mux)
	defer api.Close()
	call := func(method, path string, payload any, wantStatus int) []byte {
		t.Helper()
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(ctx, method, api.URL+path, bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		res, err := api.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, wantStatus, res.StatusCode, string(out))
		return out
	}
	entry := runtimetypes.ModelRegistryEntry{Name: "test-weights", SourceURL: source.URL + "/unused", SizeBytes: int64(len(model))}
	out := call(http.MethodPost, "/model-registry", entry, http.StatusCreated)
	require.NoError(t, json.Unmarshal(out, &entry))
	entryPath := "/model-registry/" + entry.ID
	require.Contains(t, string(call(http.MethodGet, entryPath, nil, http.StatusOK)), entry.SourceURL)
	entry.SourceURL = source.URL + "/weights"
	call(http.MethodPut, entryPath, entry, http.StatusOK)
	require.Contains(t, string(call(http.MethodGet, "/model-registry", nil, http.StatusOK)), entry.SourceURL)
	pull := func(name string, status int) string {
		return strings.TrimSpace(string(call(http.MethodPost, "/model-registry/download", map[string]string{"name": name}, status)))
	}
	pull("absent", http.StatusNotFound)
	pull(entry.Name, http.StatusUnprocessableEntity)
	_, err = modelstore.Resolve(modelstore.Dir(root, ""), entry.Name, "llama", "")
	require.ErrorIs(t, err, modelstore.ErrModelNotFound)
	store := runtimetypes.New(db.WithoutTransaction())
	require.Empty(t, clikv.Read(ctx, store, "default-provider"))
	require.Empty(t, clikv.Read(ctx, store, "default-model"))
	require.Equal(t, `"downloaded"`, pull(entry.Name, http.StatusOK))
	path, err := modelstore.Resolve(modelstore.Dir(root, ""), entry.Name, "llama", "")
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, model, string(data))
	backends, err := store.ListAllBackends(ctx)
	require.NoError(t, err)
	require.Len(t, backends, 1)
	require.Equal(t, "local", backends[0].BaseURL)
	require.Contains(t, state.Get(ctx)[backends[0].ID].Models, entry.Name)
	require.Equal(t, runtimestate.ConnectionSelector(backends[0].ID), clikv.Read(ctx, store, "default-provider"))
	require.Equal(t, entry.Name, clikv.Read(ctx, store, "default-model"))
	// Desktop inventory also includes models after their registry entry is removed.
	// Removing a selected model must leave both its bytes and assignment intact.
	modelPath := "/local-ai/models/" + entry.Name
	call(http.MethodDelete, modelPath, nil, http.StatusConflict)
	require.FileExists(t, path)
	require.NoError(t, clikv.SetString(ctx, store, "default-provider", "ollama"))
	require.NoError(t, clikv.SetString(ctx, store, "default-model", "chosen"))
	require.Equal(t, `"already downloaded"`, pull(entry.Name, http.StatusOK))
	require.EqualValues(t, 2, transfers.Load())
	require.Equal(t, "ollama", clikv.Read(ctx, store, "default-provider"))
	require.Equal(t, "chosen", clikv.Read(ctx, store, "default-model"))
	call(http.MethodDelete, entryPath, nil, http.StatusOK)
	pull(entry.Name, http.StatusNotFound)
	var inventory []struct {
		Name      string
		Installed bool
		Bytes     int64
	}
	require.NoError(t, json.Unmarshal(call(http.MethodGet, "/local-ai/models", nil, http.StatusOK), &inventory))
	require.Contains(t, inventory, struct {
		Name      string
		Installed bool
		Bytes     int64
	}{entry.Name, true, int64(len(model))})
	call(http.MethodDelete, modelPath, nil, http.StatusOK)
	require.NoFileExists(t, path)
	require.Equal(t, "chosen", clikv.Read(ctx, store, "default-model"))
}
