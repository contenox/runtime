package slot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/transport"
)

type mediaBackend struct {
	fakeBackend
	renders int
}

func (b *mediaBackend) RenderMedia(_ context.Context, req transport.MediaRequest) (transport.MediaResult, error) {
	b.renders++
	return transport.MediaResult{Execution: &transport.SessionExecution{Digest: req.Digest}}, nil
}

func TestRenderPinsTheArtifactOpenedByTheSlot(t *testing.T) {
	root := t.TempDir()
	model := filepath.Join(root, "video")
	if err := os.Mkdir(model, 0700); err != nil {
		t.Fatal(err)
	}
	graph := filepath.Join(model, "openvino_model.xml")
	if err := os.WriteFile(graph, []byte("verified graph"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := &mediaBackend{}
	svc := New(backend, WithModelsDir(root), WithBackend("openvino"))
	req := transport.MediaRequest{
		OpenSessionRequest: transport.OpenSessionRequest{ModelName: "video", Type: "openvino"},
		Settings:           transport.MediaSettings{Prompt: "lake", Width: 128, Height: 128, Frames: 9, FPS: 8, Steps: 2},
	}
	sample, err := svc.RenderMedia(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Digest = sample.Execution.Digest
	if req.Digest == "" {
		t.Fatal("verification returned an empty digest")
	}
	if err := os.WriteFile(graph, []byte("replacement graph"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", model} {
		req.Path = path
		if _, err := svc.RenderMedia(t.Context(), req); !errors.Is(err, transport.ErrDigestMismatch) {
			t.Fatalf("render with replacement graph, path %q: %v", path, err)
		}
	}
	if backend.renders != 1 {
		t.Fatalf("backend ran %d times, want only the verification", backend.renders)
	}
}
