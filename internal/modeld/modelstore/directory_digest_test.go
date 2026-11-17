package modelstore_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/stretchr/testify/require"
)

func TestUnit_ModelBundleIdentityRejectsChangedWeightsAndTokenizer(t *testing.T) {
	for _, changed := range []string{"openvino_model.bin", "tokenizer.json", "nested/config.json"} {
		t.Run(changed, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "model")
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o700))
			for _, name := range []string{"openvino_model.xml", "openvino_model.bin", "tokenizer.json", "nested/config.json"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("original "+name), 0o600))
			}
			models, err := modelstore.NewAdmin(root).ListModels(context.Background())
			require.NoError(t, err)
			require.Len(t, models, 1)
			digest := models[0].Digest
			require.NotEmpty(t, digest)
			_, err = modelstore.Resolve(root, "model", "openvino", digest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, changed), []byte("replacement"), 0o600))
			_, err = modelstore.Resolve(root, "model", "openvino", digest)
			require.ErrorIs(t, err, modelstore.ErrDigestMismatch)
		})
	}
}

func TestUnit_ModelBundleIdentityRejectsChangedOrRemovedVisionProjector(t *testing.T) {
	for _, change := range []string{"replace", "remove"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "vision")
			require.NoError(t, os.MkdirAll(dir, 0o700))
			model := filepath.Join(dir, "model.gguf")
			projector := filepath.Join(dir, "mmproj.gguf")
			require.NoError(t, os.WriteFile(model, []byte("weights"), 0o600))
			textDigest, err := modelstore.FileDigest(model)
			require.NoError(t, err)
			plain, err := modelstore.ArtifactDigest(model, "llama")
			require.NoError(t, err)
			require.Equal(t, textDigest, plain)
			require.NoError(t, os.WriteFile(projector, []byte("projector"), 0o600))
			models, err := modelstore.NewAdmin(root).ListModels(t.Context())
			require.NoError(t, err)
			require.Len(t, models, 1)
			digest := models[0].Digest
			require.NotEqual(t, textDigest, digest)
			_, err = modelstore.Resolve(root, "vision", "llama", digest)
			require.NoError(t, err)
			_, err = modelstore.Resolve(root, "vision", "llama", textDigest)
			require.ErrorIs(t, err, modelstore.ErrDigestMismatch)
			if change == "replace" {
				require.NoError(t, os.WriteFile(projector, []byte("different projector"), 0o600))
			} else {
				require.NoError(t, os.Remove(projector))
			}
			_, err = modelstore.Resolve(root, "vision", "llama", digest)
			require.ErrorIs(t, err, modelstore.ErrDigestMismatch)
		})
	}
}
