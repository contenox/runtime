package modelstore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
)

func TestUnit_ModelNamesCannotEscapeStore(t *testing.T) {
	root := t.TempDir()
	storeDir := filepath.Join(root, "models")
	outside := filepath.Join(root, "outside", "model.gguf")
	writeFile(t, outside, []byte("keep"))
	admin := modelstore.NewAdmin(storeDir)
	for _, name := range []string{"..", ".", "../outside", "sub/../../outside", `..\outside`, "/outside", `C:\outside`} {
		t.Run(name, func(t *testing.T) {
			if _, err := modelstore.Resolve(storeDir, name, "llama", ""); err == nil {
				t.Fatal("resolved unsafe name")
			}
			if err := admin.RemoveModel(context.Background(), name); err == nil {
				t.Fatal("removed unsafe name")
			}
			if _, err := admin.ReceiveModel(context.Background(), modelstore.PushManifest{Name: name, Type: "llama", Format: modelstore.PushFormatFile}, strings.NewReader("replace")); err == nil {
				t.Fatal("received unsafe name")
			}
			if err := modelstore.EnsureModelAvailable(context.Background(), nil, name, modelstore.AutoPullOptions{DataRoot: root}); err == nil {
				t.Fatal("pulled unsafe name")
			}
		})
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatalf("outside file changed: %q, %v", data, err)
	}
}
