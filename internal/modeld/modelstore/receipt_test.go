package modelstore_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
)

func TestUnit_ModelReceiptComparesCompleteContents(t *testing.T) {
	admin := modelstore.NewAdmin(t.TempDir())
	manifest := modelstore.PushManifest{Name: "model", Type: "llama", Format: modelstore.PushFormatTar}
	for index, template := range []string{"first", "first", "changed"} {
		data := buildTestTar(t, map[string][]byte{"model.gguf": []byte("weights"), "chat_template.jinja": []byte(template)})
		manifest.Digest = digestOf(data)
		result, err := admin.ReceiveModel(context.Background(), manifest, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if result.AlreadyPresent != (index == 1) {
			t.Fatalf("receipt %d: %+v", index, result)
		}
	}
}

func TestUnit_ModelReceiptRejectsIncompleteReplacement(t *testing.T) {
	for _, name := range []string{"missing-entrypoint", "wrong-size", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			admin := modelstore.NewAdmin(dir)
			if _, err := admin.ReceiveModel(context.Background(), modelstore.PushManifest{Name: "model", Type: "llama", Format: modelstore.PushFormatFile}, bytes.NewBufferString("original")); err != nil {
				t.Fatal(err)
			}
			manifest := modelstore.PushManifest{Name: "model", Type: "llama", Format: modelstore.PushFormatTar}
			data := buildTestTar(t, map[string][]byte{"model.gguf": []byte("new")})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "missing-entrypoint":
				data = buildTestTar(t, map[string][]byte{"unrelated": []byte("new")})
			case "wrong-size":
				manifest.TotalBytes = int64(len(data)) + 1
			case "cancelled":
				cancel()
			}
			if _, err := admin.ReceiveModel(ctx, manifest, bytes.NewReader(data)); err == nil {
				t.Fatal("incomplete replacement succeeded")
			}
			got, err := os.ReadFile(filepath.Join(dir, "model", "model.gguf"))
			if err != nil || string(got) != "original" {
				t.Fatalf("original=%q error=%v", got, err)
			}
			assertNoStaging(t, dir)
		})
	}
}
