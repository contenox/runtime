package modelstore_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/contenox/runtime/internal/archiveutil"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelregistry"
)

func TestUnit_AcquisitionRejectsChangedModelBytesBeforeOwnerCommit(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "models")
	admin := modelstore.NewNodeAdmin(dir)
	if _, err := admin.ReceiveModel(t.Context(), transport.PushManifest{Name: "fim", Type: "llama", Format: transport.PushFormatFile}, strings.NewReader("previous")); err != nil {
		t.Fatal(err)
	}
	var repaired atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if repaired.Load() {
			fmt.Fprint(w, "reviewed weights")
			return
		}
		fmt.Fprint(w, "different weights")
	}))
	defer server.Close()
	digest := sha256.Sum256([]byte("reviewed weights"))
	registry := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{"fim": {Name: "fim", SourceURL: server.URL, SourceSHA256: hex.EncodeToString(digest[:])}}}
	options := modelstore.AutoPullOptions{DataRoot: root, Receive: admin.ReceiveModel}
	if err := modelstore.EnsureModelAvailable(t.Context(), registry, "fim", options); !errors.Is(err, modelstore.ErrDigestMismatch) {
		t.Fatalf("changed artifact accepted: %v", err)
	}
	assertModelBytes(t, dir, "fim", "model.gguf", "previous")
	assertNoStaging(t, dir)
	repaired.Store(true)
	if err := modelstore.EnsureModelAvailable(t.Context(), registry, "fim", options); err != nil {
		t.Fatal(err)
	}
	assertModelBytes(t, dir, "fim", "model.gguf", "reviewed weights")
	assertNoStaging(t, dir)
}

func TestUnit_AcquisitionRequiredAssetsPreservePreviousAndRetry(t *testing.T) {
	for _, failedAsset := range []string{"projector", "template"} {
		t.Run(failedAsset, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "models")
			admin := modelstore.NewNodeAdmin(dir)
			if _, err := admin.ReceiveModel(context.Background(), transport.PushManifest{Name: "vision", Type: "llama", Format: transport.PushFormatFile}, strings.NewReader("previous weights")); err != nil {
				t.Fatal(err)
			}
			var repaired atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/"+failedAsset && !repaired.Load() {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, "new "+strings.TrimPrefix(r.URL.Path, "/"))
			}))
			defer server.Close()
			registry := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{"vision": {Name: "vision", SourceURL: server.URL + "/weights", Vision: true, MMProjURL: server.URL + "/projector", ChatTemplateURL: server.URL + "/template", ToolProtocol: "llama:common_chat_tool_parser"}}}
			options := modelstore.AutoPullOptions{DataRoot: root, Receive: admin.ReceiveModel, ProgressOut: io.Discard}
			if err := modelstore.EnsureModelAvailable(context.Background(), registry, "vision", options); err == nil || !strings.Contains(err.Error(), failedAsset) {
				t.Fatalf("missing required %s: %v", failedAsset, err)
			}
			assertModelBytes(t, dir, "vision", "model.gguf", "previous weights")
			assertNoStaging(t, dir)
			repaired.Store(true)
			if err := modelstore.EnsureModelAvailable(context.Background(), registry, "vision", options); err != nil {
				t.Fatal(err)
			}
			for file, want := range map[string]string{"model.gguf": "new weights", "mmproj.gguf": "new projector", "chat_template.jinja": "new template"} {
				assertModelBytes(t, dir, "vision", file, want)
			}
			assertNoStaging(t, dir)
		})
	}
}

func TestUnit_AcquisitionCancellationAndOwnerRefusalPreservePrevious(t *testing.T) {
	for _, mode := range []string{"cancel-download", "cancel-receive", "resident"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "models")
			resident := false
			admin, err := modelstore.NewProtectedNodeAdmin(dir, func(string) error {
				if resident {
					return transport.ErrModelBusy
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := admin.ReceiveModel(context.Background(), transport.PushManifest{Name: "vision", Type: "llama", Format: transport.PushFormatFile}, strings.NewReader("previous")); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "artifact") }))
			defer server.Close()
			registry := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{"vision": {Name: "vision", Vision: true, SourceURL: server.URL, MMProjURL: server.URL}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			options := modelstore.AutoPullOptions{DataRoot: root, Receive: admin.ReceiveModel, ProgressOut: io.Discard}
			want := context.Canceled
			switch mode {
			case "cancel-download":
				options.Progress = func(modelstore.DownloadProgress) error { cancel(); return ctx.Err() }
			case "cancel-receive":
				options.Receive = func(ctx context.Context, manifest transport.PushManifest, r io.Reader) (transport.PushResult, error) {
					cancel()
					return admin.ReceiveModel(ctx, manifest, r)
				}
			case "resident":
				resident = true
				want = transport.ErrModelBusy
			}
			if err := modelstore.EnsureModelAvailable(ctx, registry, "vision", options); !errors.Is(err, want) {
				t.Fatalf("acquisition=%v want=%v", err, want)
			}
			assertModelBytes(t, dir, "vision", "model.gguf", "previous")
			assertNoStaging(t, dir)
		})
	}
}

func TestUnit_AcquisitionRequiresProjectorSource(t *testing.T) {
	root := t.TempDir()
	registry := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{"vision": {Name: "vision", Vision: true, SourceURL: "http://invalid.local/model"}}}
	err := modelstore.EnsureModelAvailable(context.Background(), registry, "vision", modelstore.AutoPullOptions{DataRoot: root, Receive: modelstore.NewNodeAdmin(filepath.Join(root, "models")).ReceiveModel})
	if err == nil || !strings.Contains(err.Error(), "projector source") {
		t.Fatalf("missing projector source: %v", err)
	}
	assertNoStaging(t, filepath.Join(root, "models"))
}

func TestUnit_AcquisitionOpenVINOBundleNeverExposesPartialDownload(t *testing.T) {
	root := t.TempDir()
	var repaired atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fmt.Fprint(w, `{"siblings":[{"rfilename":"openvino_language_model.xml"},{"rfilename":"openvino_language_model.bin"},{"rfilename":"openvino_vision_embeddings_model.xml"},{"rfilename":"openvino_vision_embeddings_model.bin"}]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "openvino_vision_embeddings_model.bin") && !repaired.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "artifact")
	}))
	defer server.Close()
	t.Setenv("HF_ENDPOINT", server.URL)
	t.Setenv("HF_TOKEN", "")
	registry := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{"vision": {Name: "vision", Backend: "openvino", Vision: true, Repo: "owner/vision"}}}
	dir := filepath.Join(root, "models")
	options := modelstore.AutoPullOptions{DataRoot: root, Receive: modelstore.NewNodeAdmin(dir).ReceiveModel}
	if err := modelstore.EnsureModelAvailable(context.Background(), registry, "vision", options); err == nil {
		t.Fatal("partial IR bundle accepted")
	}
	if _, err := modelstore.Resolve(dir, "vision", "openvino", ""); !errors.Is(err, modelstore.ErrModelNotFound) {
		t.Fatalf("partial bundle visible: %v", err)
	}
	assertNoStaging(t, dir)
	repaired.Store(true)
	if err := modelstore.EnsureModelAvailable(context.Background(), registry, "vision", options); err != nil {
		t.Fatal(err)
	}
	assertModelBytes(t, dir, "vision", "openvino_vision_embeddings_model.bin", "artifact")
}

func TestUnit_AcquisitionMissingTokenizerCompanionPreservesInstalledModel(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "models")
	admin := modelstore.NewNodeAdmin(dir)
	previous := filepath.Join(t.TempDir(), "previous")
	if err := os.MkdirAll(filepath.Join(previous, "literal-source-tokenizer"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"openvino_language_model.xml":             "previous IR",
		"openvino_language_model.bin":             "previous weights",
		"contenox-literal-source-tokenizer.json":  "corrupt manifest",
		"literal-source-tokenizer/tokenizer.json": "corrupt tokenizer",
	} {
		if err := os.WriteFile(filepath.Join(previous, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	if err := archiveutil.WriteTar(t.Context(), previous, &archive); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ReceiveModel(t.Context(), transport.PushManifest{Name: "fim", Type: "openvino", Format: transport.PushFormatTar}, bytes.NewReader(archive.Bytes())); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fmt.Fprint(w, `{"siblings":[{"rfilename":"openvino_language_model.xml"},{"rfilename":"openvino_language_model.bin"}]}`)
			return
		}
		fmt.Fprint(w, "replacement artifact")
	}))
	defer server.Close()
	t.Setenv("HF_ENDPOINT", server.URL)
	t.Setenv("HF_TOKEN", "")
	registry := &mockRegistry{models: map[string]*modelregistry.ModelDescriptor{"fim": {
		Name: "fim", Backend: "openvino", Repo: "owner/fim", Revision: "pinned", TokenizerCompanionID: "missing-companion",
		Completion: modelregistry.CompletionProfile{Mode: modelregistry.CompletionFIM, FIMFormat: "starcoder"},
	}}}
	err := modelstore.EnsureModelAvailable(t.Context(), registry, "fim", modelstore.AutoPullOptions{DataRoot: root, Receive: admin.ReceiveModel, ProgressOut: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "not bundled") {
		t.Fatalf("missing tokenizer companion error=%v", err)
	}
	assertModelBytes(t, dir, "fim", "openvino_language_model.xml", "previous IR")
	assertModelBytes(t, dir, "fim", "openvino_language_model.bin", "previous weights")
	assertModelBytes(t, dir, "fim", "contenox-literal-source-tokenizer.json", "corrupt manifest")
	assertNoStaging(t, dir)
}

func TestSystem_AcquisitionEmbeddedStarCoderCompanion(t *testing.T) {
	sourceDir := os.Getenv("CONTENOX_STARCODER_FIM_SOURCE_ASSETS")
	if sourceDir == "" {
		t.Skip("CONTENOX_STARCODER_FIM_SOURCE_ASSETS is not set")
	}
	sourceFiles := map[string]string{
		"tokenizer.json":          "17fa145258b20c18287f1e3bd804e074cc13333f11984a2f5a2f11c5110437aa",
		"tokenizer_config.json":   "8149d8e6b21275ad2cc346885ad92c0e9b5aa3e28a78bb5d39b7febc7e52545d",
		"special_tokens_map.json": "0fc9ac706a35e6337b19d484bc8f866b6a0ee7ad2509b03f7305dc0838c82d2c",
	}
	sourceData := make(map[string][]byte, len(sourceFiles))
	for name, want := range sourceFiles {
		data, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			t.Fatalf("read pinned source tokenizer input %s: %v", name, err)
		}
		digest := sha256.Sum256(data)
		if got := hex.EncodeToString(digest[:]); got != want {
			t.Fatalf("pinned source tokenizer input %s sha256=%s want=%s", name, got, want)
		}
		sourceData[name] = data
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			fmt.Fprint(w, `{"siblings":[{"rfilename":"tokenizer.json"},{"rfilename":"tokenizer_config.json"},{"rfilename":"special_tokens_map.json"},{"rfilename":"openvino_language_model.xml"},{"rfilename":"openvino_language_model.bin"}]}`)
			return
		}
		for name, data := range sourceData {
			if strings.HasSuffix(r.URL.Path, "/"+name) {
				_, _ = w.Write(data)
				return
			}
		}
		fmt.Fprint(w, "pinned model IR artifact")
	}))
	defer server.Close()
	t.Setenv("HF_ENDPOINT", server.URL)
	t.Setenv("HF_TOKEN", "")
	registry := modelregistry.New(nil)
	desc, err := registry.Resolve(t.Context(), "starcoder2-7b-fim-ov")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	modelsDir := filepath.Join(root, "models")
	admin := modelstore.NewNodeAdmin(modelsDir)
	if err := modelstore.EnsureModelAvailable(t.Context(), registry, desc.Name, modelstore.AutoPullOptions{DataRoot: root, Receive: admin.ReceiveModel, ProgressOut: io.Discard}); err != nil {
		t.Fatal(err)
	}
	modelDir := filepath.Join(modelsDir, desc.Name)
	for _, name := range []string{
		"contenox-literal-source-tokenizer.json",
		"literal-source-tokenizer/tokenizer.json",
		"literal-source-tokenizer/tokenizer_config.json",
		"literal-source-tokenizer/openvino_tokenizer.xml",
		"literal-source-tokenizer/openvino_tokenizer.bin",
		"literal-source-tokenizer/LICENSE",
		"literal-source-tokenizer/PROVENANCE.json",
	} {
		if err := requireNonemptyFile(filepath.Join(modelDir, name)); err != nil {
			t.Fatalf("received companion file %s: %v", name, err)
		}
	}
	assertModelBytes(t, modelsDir, desc.Name, "openvino_language_model.xml", "pinned model IR artifact")
	assertNoStaging(t, modelsDir)
}

func requireNonemptyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("%s is not a nonempty regular file", filepath.Base(path))
	}
	return nil
}

func TestUnit_ProtectedStoreRecoversInterruptedReplacement(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, archiveutil.PreviousDirectoryPrefix+"vision")
	if err := os.Mkdir(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "model.gguf"), []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	admin, err := modelstore.NewProtectedNodeAdmin(dir, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	models, err := admin.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].Name != "vision" {
		t.Fatalf("recovered inventory=%+v error=%v", models, err)
	}
	assertModelBytes(t, dir, "vision", "model.gguf", "previous")
}

func assertModelBytes(t *testing.T, dir, model, file, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, model, file))
	if err != nil || string(data) != want {
		t.Fatalf("%s=%q error=%v want=%q", file, data, err, want)
	}
}

func assertNoStaging(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") || strings.HasPrefix(entry.Name(), archiveutil.PreviousDirectoryPrefix) {
			t.Fatalf("staging left behind: %s", entry.Name())
		}
	}
}
