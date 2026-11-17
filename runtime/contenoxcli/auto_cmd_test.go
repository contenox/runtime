package contenoxcli

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type autoTestWorker struct{ *transport.MemoryService }

func (w *autoTestWorker) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{EffectiveContext: 262144, HotContextTokens: 131072}, nil
}
func (w *autoTestWorker) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	req.Config.NumCtx = 262144
	s, err := w.MemoryService.OpenSession(ctx, req)
	return &autoTestSession{Session: s}, err
}

func (w *autoTestWorker) ListModels(context.Context) ([]transport.NodeModel, error) {
	return []transport.NodeModel{{Name: "setup-test", Type: "llama", ContextLength: 262144}}, nil
}

func (w *autoTestWorker) RemoveModel(context.Context, string) error {
	return transport.ErrUnsupportedFeature
}

func (w *autoTestWorker) DiskStats(context.Context) (transport.NodeDiskStats, error) {
	return transport.NodeDiskStats{}, transport.ErrUnsupportedFeature
}

func (w *autoTestWorker) ReceiveModel(context.Context, transport.PushManifest, io.Reader) (transport.PushResult, error) {
	return transport.PushResult{}, transport.ErrUnsupportedFeature
}

type autoTestSession struct {
	transport.Session
	text strings.Builder
}

func (s *autoTestSession) ExplainContext() transport.ContextReport {
	report := s.Session.ExplainContext()
	report.HotContextTokens = 131072
	return report
}

var autoTestUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func (s *autoTestSession) EnsurePrefix(ctx context.Context, prefix transport.PrefixInput) (transport.PrefixStatus, error) {
	s.text.WriteString(prefix.Text)
	return s.Session.EnsurePrefix(ctx, prefix)
}

func (s *autoTestSession) PrefillSuffix(ctx context.Context, suffix transport.SuffixInput) (transport.SuffixStatus, error) {
	s.text.WriteString(suffix.Text)
	return s.Session.PrefillSuffix(ctx, suffix)
}

func (s *autoTestSession) Decode(context.Context, transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	out := make(chan transport.StreamChunk, 1)
	defer close(out)
	if code := autoTestUUID.FindString(s.text.String()); code != "" {
		out <- transport.StreamChunk{Text: code, FinishReason: "stop"}
		return out, nil
	}
	call := transport.ToolCall{ID: "setup", Type: "function"}
	call.Function.Name = "contenox_ready"
	call.Function.Arguments = `{"status":"ok"}`
	out <- transport.StreamChunk{ToolCalls: []transport.ToolCall{call}, FinishReason: "tool_calls"}
	return out, nil
}

func TestSystem_Auto_VerifiesToolAndOpenedHotContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	root := t.TempDir()
	ctx = modeldconn.WithDataRoot(ctx, root)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	lease, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), TTL: time.Minute, Endpoint: listener.Addr().String(), Backend: "llama"})
	require.NoError(t, err)
	defer lease.Release()
	go func() {
		_ = transportgrpc.Serve(ctx, listener, &autoTestWorker{transport.NewMemoryService()}, lease.InstanceID(), "llama")
	}()
	hot, err := verifyAutoModel(ctx, io.Discard, modelregistry.ModelDescriptor{Name: "setup-test", Backend: "llama"}, 131072)
	require.NoError(t, err)
	require.Equal(t, 131072, hot)
}

func TestUnit_Auto_DefaultsKeepRemoteBackendAndReplaceCloudFallback(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	cmd := &cobra.Command{}
	cmd.Flags().String("db", path, "")
	db, err := OpenDBAt(ctx, path)
	require.NoError(t, err)
	svc := backendservice.New(db)
	require.NoError(t, svc.Create(ctx, &runtimetypes.Backend{ID: "remote", Name: "remote", Type: "modeld", BaseURL: "192.0.2.1:9000"}))
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, clikv.WriteConfig(ctx, store, "", "default-alt-provider", "openai"))
	require.NoError(t, db.Close())
	require.NoError(t, saveAutoDefaults(ctx, cmd, modelregistry.ModelDescriptor{Name: "selected"}))
	db, err = OpenDBAt(ctx, path)
	require.NoError(t, err)
	defer db.Close()
	store = runtimetypes.New(db.WithoutTransaction())
	local, err := store.GetBackendByName(ctx, "native-local")
	require.NoError(t, err)
	require.Equal(t, runtimestate.ConnectionSelector(local.ID), clikv.Read(ctx, store, "default-alt-provider"))
	require.Equal(t, runtimestate.ConnectionSelector(local.ID), clikv.Read(ctx, store, "default-provider"))
	require.Equal(t, "selected", clikv.Read(ctx, store, "default-model"))
	require.Empty(t, clikv.Read(ctx, store, "default-token-limit"),
		"auto selects a model; the window mask stays unset because the worker reports its own capacity")
	entries, err := backendservice.New(db).List(ctx, nil, 100)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	for _, entry := range entries {
		if entry.ID == "remote" {
			require.Equal(t, "192.0.2.1:9000", entry.BaseURL)
		}
	}
}

func TestUnit_Auto_PreviousSelectionDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.db")
	cmd := &cobra.Command{}
	cmd.Flags().String("db", path, "")
	_, found, err := previousAutoModel(context.Background(), cmd, "", "llama", 16<<30)
	require.NoError(t, err)
	require.False(t, found)
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err))
}

func TestSystem_Auto_NativeToolProbe(t *testing.T) {
	modelPath := os.Getenv("CONTENOX_AUTO_TEST_GGUF")
	if modelPath == "" {
		t.Skip("set CONTENOX_AUTO_TEST_GGUF and CONTENOX_MODELD_BIN for native inference")
	}
	testAutoNativeToolProbe(t, "llama", modelPath)
}

func TestSystem_Auto_OpenVINOToolProbe(t *testing.T) {
	modelPath := os.Getenv("CONTENOX_AUTO_TEST_OPENVINO")
	if modelPath == "" {
		t.Skip("set CONTENOX_AUTO_TEST_OPENVINO and CONTENOX_MODELD_BIN for native inference")
	}
	testAutoNativeToolProbe(t, "openvino", modelPath)
}

func testAutoNativeToolProbe(t *testing.T, backend, modelPath string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", root)
	t.Setenv("CONTENOX_MODELD_BACKEND", backend)
	defer modeldconn.StopAutoStarted()
	dir := filepath.Join(root, "models", "setup-native")
	if backend == "openvino" {
		require.NoError(t, os.MkdirAll(dir, 0700))
		entries, err := os.ReadDir(modelPath)
		require.NoError(t, err)
		for _, entry := range entries {
			if entry.Name() != "contenox-openvino.json" {
				testAutoCopyModelFile(t, filepath.Join(modelPath, entry.Name()), filepath.Join(dir, entry.Name()))
			}
		}
		require.NoError(t, modelstore.WriteModelProfile(backend, dir, "openvino:json_schema_tool_calls", "", ""))
	} else {
		require.NoError(t, os.MkdirAll(dir, 0700))
		require.NoError(t, os.Symlink(modelPath, filepath.Join(dir, "model.gguf")))
	}
	hot, err := verifyAutoModel(modeldconn.WithDataRoot(t.Context(), root), io.Discard, modelregistry.ModelDescriptor{Name: "setup-native", Backend: backend}, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, hot, 4096)
}

func testAutoCopyModelFile(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	require.NoError(t, err)
	defer func() { require.NoError(t, in.Close()) }()
	out, err := os.Create(destination)
	require.NoError(t, err)
	defer func() { require.NoError(t, out.Close()) }()
	_, err = io.Copy(out, in)
	require.NoError(t, err)
}
