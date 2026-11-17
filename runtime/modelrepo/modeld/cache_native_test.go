//go:build llamanode && llamacpp_direct

package modeld_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/internal/modeld/llama"
	_ "github.com/contenox/runtime/internal/modeld/llama/llamasession"
	"github.com/stretchr/testify/require"
)

func TestSystem_ModeldChatRetainsNativeModel(t *testing.T) {
	model := os.Getenv("CONTENOX_LLAMA_TINY_GGUF")
	if model == "" {
		t.Skip("CONTENOX_LLAMA_TINY_GGUF is not set")
	}
	require.Equal(t, "model.gguf", filepath.Base(model))
	_, err := os.Stat(model)
	require.NoError(t, err)
	t.Setenv("CONTENOX_LLAMA_GPU_LAYERS", "0")
	reused := checkChatResidency(t, llama.NewService(), filepath.Dir(filepath.Dir(model)), filepath.Base(filepath.Dir(model)))
	t.Logf("sequential turns retain the model; backend-reported prefix reuse: %v", reused)
}
