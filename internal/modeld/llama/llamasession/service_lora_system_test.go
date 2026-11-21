//go:build llamanode

package llamasession

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/archiveutil"
	"github.com/contenox/runtime/internal/modeld/llama"
	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/slot"
	"github.com/contenox/runtime/internal/transport"
)

func TestSystem_LlamaService_AdapterRequestChangesGeneration(t *testing.T) {
	model, adapter := *loraModel, *loraAdapter
	if model == "" || adapter == "" {
		t.Skip("supply -lora-model and -lora-adapter")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	svc := &llama.Service{}
	cfg := llama.Config{NumCtx: 128, NumBatch: 32, NumThreads: 2, DisableBOS: true}
	stable, suffix := "Look in thy", " glass"

	base := serviceDecode(t, ctx, svc, transport.OpenSessionRequest{
		Type: "llama", Path: model, Config: cfg,
	}, stable, suffix)
	variant := serviceDecode(t, ctx, svc, transport.OpenSessionRequest{
		Type: "llama", Path: model, Config: cfg,
		Adapters: []transport.AdapterSpec{{Name: "style", Path: adapter, Scale: 1}},
	}, stable, suffix)

	t.Logf("base=%q", base)
	t.Logf("variant=%q", variant)
	if strings.TrimSpace(variant) == "" {
		t.Fatal("variant produced empty output")
	}
	if base == variant {
		t.Fatal("adapter on the OpenSessionRequest did not change generation through the Service")
	}
	// A second, negligible contribution must preserve the effect of the first.
	stacked := serviceDecode(t, ctx, svc, transport.OpenSessionRequest{
		Type: "llama", Path: model, Config: cfg,
		Adapters: []transport.AdapterSpec{{Name: "style", Path: adapter, Scale: 1}, {Name: "weak", Path: adapter, Scale: 1e-8}},
	}, stable, suffix)
	if stacked != variant {
		t.Fatalf("lost the first adapter in a stack: %q", stacked)
	}
	t.Run("named catalog variant", func(t *testing.T) {
		source := t.TempDir()
		for name, path := range map[string]string{"model.gguf": model, modelstore.LoRAFile: adapter} {
			in, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			out, err := os.Create(filepath.Join(source, name))
			if err != nil {
				in.Close()
				t.Fatal(err)
			}
			_, err = io.Copy(out, in)
			in.Close()
			closeErr := out.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
		}
		store := t.TempDir()
		admin := modelstore.NewAdmin(store)
		reader, writer := io.Pipe()
		go func() { writer.CloseWithError(archiveutil.WriteTar(ctx, source, writer)) }()
		defer reader.Close()
		if _, err := admin.ReceiveModel(ctx, modelstore.PushManifest{Name: "shakespeare", Type: "llama", Format: modelstore.PushFormatTar}, reader); err != nil {
			t.Fatal(err)
		}
		files, err := admin.Inventory()
		if err != nil || len(files) != 1 || !files[0].LoRA {
			t.Fatalf("catalog: %+v, %v", files, err)
		}
		owner := slot.New(svc, slot.WithBackend("llama"), slot.WithModelsDir(store), slot.WithDataRoot(t.TempDir()))
		defer owner.UnloadModel(ctx, transport.UnloadModelRequest{})
		got := serviceDecode(t, ctx, owner, transport.OpenSessionRequest{ModelName: "shakespeare", Type: "llama", Config: cfg}, stable, suffix)
		if got != variant {
			t.Fatalf("selecting the variant by name produced %q, want %q", got, variant)
		}
	})
}

func serviceDecode(t *testing.T, ctx context.Context, svc transport.Service, req transport.OpenSessionRequest, stable, suffix string) string {
	t.Helper()
	sess, err := svc.OpenSession(ctx, req)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer func() { _ = sess.Close() }()
	out, err := decodeContinuation(ctx, sess, stable, suffix, 12)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}
