package modelstore_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelregistry"
)

func TestRegisteredModelRejectsHTMLDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html>Please sign in</html>"))
	}))
	defer server.Close()
	root := t.TempDir()
	reg := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{
		"private-model": {ID: "user-entry", Name: "private-model", SourceURL: server.URL},
	}}
	err := modelstore.EnsureModelAvailable(t.Context(), reg, "private-model", modelstore.AutoPullOptions{
		DataRoot: root,
		Receive:  modelstore.NewNodeAdmin(filepath.Join(root, "models")).ReceiveModel,
	})
	if err == nil || !strings.Contains(err.Error(), "GGUF header") {
		t.Fatalf("expected an invalid GGUF download, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "models", "private-model")); !os.IsNotExist(err) {
		t.Fatalf("invalid model reached the inventory: %v", err)
	}
}

type mockRegistry struct {
	models map[string]*modelregistry.ModelDescriptor
}

func (m *mockRegistry) Resolve(ctx context.Context, name string) (*modelregistry.ModelDescriptor, error) {
	if d, ok := m.models[name]; ok {
		return d, nil
	}
	return nil, modelregistry.ErrNotFound
}

func (m *mockRegistry) List(ctx context.Context) ([]modelregistry.ModelDescriptor, error) {
	var out []modelregistry.ModelDescriptor
	for _, v := range m.models {
		out = append(out, *v)
	}
	return out, nil
}

func (m *mockRegistry) OptimalFor(ctx context.Context, name string) (string, error) {
	return "", modelregistry.ErrNotFound
}

func TestUnit_EnsureModelAvailable_AlreadyPresent(t *testing.T) {
	tempDataRoot := t.TempDir()
	modelsDir := filepath.Join(tempDataRoot, "models")
	modelDir := filepath.Join(modelsDir, "test-model")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.gguf"), []byte("dummy weights"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{}}
	err := modelstore.EnsureModelAvailable(context.Background(), reg, "test-model", modelstore.AutoPullOptions{
		DataRoot: tempDataRoot,
		Receive:  modelstore.NewNodeAdmin(filepath.Join(tempDataRoot, "models")).ReceiveModel,
	})
	if err != nil {
		t.Fatalf("expected nil error for existing model, got: %v", err)
	}
}

func TestUnit_EnsureModelAvailable_AutoPullsFromRegistry(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock gguf file content"))
	}))
	defer ts.Close()

	tempDataRoot := t.TempDir()
	reg := &mockRegistry{
		models: map[string]*modelregistry.ModelDescriptor{
			"qwen-test": {
				Name:         "qwen-test",
				SourceURL:    ts.URL,
				SizeBytes:    22,
				Curated:      true,
				ToolProtocol: "llama:common_chat_tool_parser",
			},
		},
	}

	var progressBuf bytes.Buffer
	var progress []modelstore.DownloadProgress
	err := modelstore.EnsureModelAvailable(context.Background(), reg, "qwen-test", modelstore.AutoPullOptions{
		DataRoot:    tempDataRoot,
		Receive:     modelstore.NewNodeAdmin(filepath.Join(tempDataRoot, "models")).ReceiveModel,
		ProgressOut: &progressBuf,
		Progress: func(value modelstore.DownloadProgress) error {
			progress = append(progress, value)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("expected successful auto-pull, got: %v", err)
	}
	destPath := filepath.Join(tempDataRoot, "models", "qwen-test", "model.gguf")
	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("expected downloaded model.gguf, read error: %v", err)
	}
	if string(data) != "mock gguf file content" {
		t.Fatalf("unexpected content: %q", string(data))
	}
	profilePath := filepath.Join(tempDataRoot, "models", "qwen-test", "contenox-llama.json")
	if _, err := os.Stat(profilePath); err != nil {
		t.Fatalf("expected profile JSON at %s: %v", profilePath, err)
	}
	if len(progress) == 0 ||
		progress[len(progress)-1].Downloaded != int64(len("mock gguf file content")) ||
		progress[len(progress)-1].Total != int64(len("mock gguf file content")) {
		t.Fatalf("unexpected download progress: %#v", progress)
	}
}

func TestUnit_DownloadProgressWriter_ReportsCallbackFailure(t *testing.T) {
	want := errors.New("progress receiver closed")
	writer := &modelstore.DownloadProgressWriter{Total: 3, Progress: func(modelstore.DownloadProgress) error { return want }}
	n, err := writer.Write([]byte("abc"))
	if n != 3 || !errors.Is(err, want) {
		t.Fatalf("Write() = %d, %v", n, err)
	}
}

func TestUnit_EnsureModelAvailable_NotFoundInRegistry(t *testing.T) {
	tempDataRoot := t.TempDir()
	reg := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{}}

	err := modelstore.EnsureModelAvailable(context.Background(), reg, "nonexistent-model", modelstore.AutoPullOptions{
		DataRoot: tempDataRoot,
		Receive:  modelstore.NewNodeAdmin(filepath.Join(tempDataRoot, "models")).ReceiveModel,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
	if !errors.Is(err, transport.ErrModelNotFound) && !errors.Is(err, modelstore.ErrModelNotFound) {
		t.Fatalf("expected modelstore.ErrModelNotFound, got: %v", err)
	}
}

func TestUnit_EnsureModelAvailable_ConcurrentDeduplication(t *testing.T) {
	var downloadCalls int
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		downloadCalls++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock concurrent gguf content"))
	}))
	defer ts.Close()

	tempDataRoot := t.TempDir()
	reg := &mockRegistry{
		models: map[string]*modelregistry.ModelDescriptor{
			"concurrent-model": {
				Name:      "concurrent-model",
				SourceURL: ts.URL,
				SizeBytes: 27,
			},
		},
	}

	var wg sync.WaitGroup
	errs := make([]error, 5)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = modelstore.EnsureModelAvailable(context.Background(), reg, "concurrent-model", modelstore.AutoPullOptions{
				DataRoot: tempDataRoot,
				Receive:  modelstore.NewNodeAdmin(filepath.Join(tempDataRoot, "models")).ReceiveModel,
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d got error: %v", i, err)
		}
	}

	mu.Lock()
	calls := downloadCalls
	mu.Unlock()

	if calls != 1 {
		t.Fatalf("expected exactly 1 download call for 5 concurrent requests, got %d", calls)
	}
}
