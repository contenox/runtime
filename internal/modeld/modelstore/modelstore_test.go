package modelstore_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
)

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestUnit_Dir_OverrideWinsOverDataRoot(t *testing.T) {
	if got := modelstore.Dir("/data", "/explicit"); got != "/explicit" {
		t.Fatalf("got %q, want /explicit", got)
	}
	want := filepath.Join("/data", modelstore.DefaultSubdir)
	if got := modelstore.Dir("/data", ""); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnit_Resolve_LlamaFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "qwen", "model.gguf"), []byte("weights"))

	path, err := modelstore.Resolve(dir, "qwen", "llama", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(dir, "qwen", "model.gguf"); path != want {
		t.Fatalf("got %q, want %q", path, want)
	}
}

func TestUnit_Resolve_LlamaNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := modelstore.Resolve(dir, "missing", "llama", "")
	if !errors.Is(err, modelstore.ErrModelNotFound) {
		t.Fatalf("got %v, want modelstore.ErrModelNotFound", err)
	}
}

func TestUnit_Resolve_LlamaDigestMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qwen", "model.gguf")
	writeFile(t, path, []byte("weights"))
	want, err := modelstore.FileDigest(path)
	if err != nil {
		t.Fatalf("FileDigest: %v", err)
	}

	got, err := modelstore.Resolve(dir, "qwen", "llama", want)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != path {
		t.Fatalf("got %q, want %q", got, path)
	}
}

func TestUnit_Resolve_LlamaDigestMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "qwen", "model.gguf"), []byte("weights"))

	_, err := modelstore.Resolve(dir, "qwen", "llama", "not-the-real-digest")
	if !errors.Is(err, modelstore.ErrDigestMismatch) {
		t.Fatalf("got %v, want modelstore.ErrDigestMismatch", err)
	}
}

func TestUnit_Resolve_OpenvinoFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "phi", "openvino_model.xml"), []byte("<ir/>"))

	path, err := modelstore.Resolve(dir, "phi", "openvino", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(dir, "phi"); path != want {
		t.Fatalf("got %q, want %q", path, want)
	}
}

func TestUnit_Resolve_OpenvinoLanguageModelEntrypoint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "phi", "openvino_language_model.xml"), []byte("<ir/>"))

	path, err := modelstore.Resolve(dir, "phi", "openvino", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(dir, "phi"); path != want {
		t.Fatalf("got %q, want %q", path, want)
	}
}

func TestUnit_Resolve_OpenvinoDigestMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "phi", "openvino_model.xml"), []byte("<ir/>"))

	_, err := modelstore.Resolve(dir, "phi", "openvino", "bogus")
	if !errors.Is(err, modelstore.ErrDigestMismatch) {
		t.Fatalf("got %v, want modelstore.ErrDigestMismatch", err)
	}
}

func TestUnit_Resolve_OpenvinoNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := modelstore.Resolve(dir, "missing", "openvino", "")
	if !errors.Is(err, modelstore.ErrModelNotFound) {
		t.Fatalf("got %v, want modelstore.ErrModelNotFound", err)
	}
}

func TestUnit_Resolve_UnsupportedType(t *testing.T) {
	dir := t.TempDir()
	_, err := modelstore.Resolve(dir, "anything", "ollama", "")
	if !errors.Is(err, modelstore.ErrUnsupportedType) {
		t.Fatalf("got %v, want modelstore.ErrUnsupportedType", err)
	}
}

func TestUnit_Resolve_EmptyName(t *testing.T) {
	dir := t.TempDir()
	_, err := modelstore.Resolve(dir, "", "llama", "")
	if !errors.Is(err, modelstore.ErrModelNotFound) {
		t.Fatalf("got %v, want modelstore.ErrModelNotFound", err)
	}
}

func TestUnit_ResolveMMProj_FoundNextToModel(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "vlm", "model.gguf")
	writeFile(t, modelPath, []byte("weights"))
	writeFile(t, filepath.Join(dir, "vlm", "mmproj.gguf"), []byte("projector"))

	if got, want := modelstore.ResolveMMProj(modelPath), filepath.Join(dir, "vlm", "mmproj.gguf"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnit_ResolveMMProj_AbsentForTextOnlyModel(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "llm", "model.gguf")
	writeFile(t, modelPath, []byte("weights"))

	if got := modelstore.ResolveMMProj(modelPath); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := modelstore.ResolveMMProj(""); got != "" {
		t.Fatalf("empty model path resolved %q, want empty", got)
	}
}

func TestUnit_FileDigest_Deterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	writeFile(t, path, []byte("hello"))

	d1, err := modelstore.FileDigest(path)
	if err != nil {
		t.Fatalf("FileDigest: %v", err)
	}
	d2, err := modelstore.FileDigest(path)
	if err != nil {
		t.Fatalf("FileDigest: %v", err)
	}
	if d1 != d2 {
		t.Fatalf("digest not deterministic: %q vs %q", d1, d2)
	}
	if len(d1) != 64 { // sha256 hex
		t.Fatalf("unexpected digest length %d: %q", len(d1), d1)
	}
}
