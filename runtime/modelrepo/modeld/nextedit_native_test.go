//go:build llamanode && llamacpp_direct

package modeld_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/llama"
	_ "github.com/contenox/runtime/internal/modeld/llama/llamasession"
	"github.com/contenox/runtime/internal/modeld/slot"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

func TestSystem_ModeldNativeNextEditPredictsRenameAndReusesPrefix(t *testing.T) {
	path := os.Getenv("CONTENOX_LLAMA_NEXT_EDIT_GGUF")
	if path == "" {
		t.Skip("CONTENOX_LLAMA_NEXT_EDIT_GGUF is not set")
	}
	require.Equal(t, "model.gguf", filepath.Base(path))
	t.Setenv("CONTENOX_LLAMA_GPU_LAYERS", "0")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	counted := &countedBackend{Service: llama.NewService()}
	service := slot.New(counted, slot.WithBackend("llama"), slot.WithModelsDir(filepath.Dir(filepath.Dir(path))), slot.WithIdleTTL(time.Minute))
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, listener, service, "next-edit-owner", "llama") }()
	t.Cleanup(func() {
		modeldconn.CloseEndpoint(listener.Addr().String())
		cancel()
		require.NoError(t, <-done)
		require.NoError(t, service.Shutdown(context.Background()))
	})
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "modeld", BaseURL: listener.Addr().String()})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: filepath.Base(filepath.Dir(path)), Meta: map[string]string{"type": "llama"}, CapabilityConfig: modelrepo.CapabilityConfig{CanNextEdit: true, NextEditFormat: "sweep-v1"}})
	client, err := provider.GetNextEditConnection(ctx, listener.Addr().String())
	require.NoError(t, err)
	before := "def add(a, b):\n    return a + b\n\nprint(add(1, 2))\n"
	current := strings.Replace(before, "def add", "def sum_values", 1)
	input := modelrepo.NextEditRequest{Input: modelcontract.NextEditInput{Path: "mathutil.py", Before: before, Current: current, Cursor: strings.Index(current, "print("), End: len(current)}, MaxTokens: 512, TopP: 1, SessionKey: "first-client:first-document"}
	first, err := client.NextEdit(ctx, input)
	require.NoError(t, err)
	require.NotEmpty(t, first.Edits)
	require.NoError(t, modelcontract.ValidateEdits(input.Input, first.Edits))
	updated := current
	for index := len(first.Edits) - 1; index >= 0; index-- {
		edit := first.Edits[index]
		updated = updated[:edit.Start] + edit.NewText + updated[edit.End:]
	}
	require.Contains(t, updated, "print(sum_values(1, 2))")
	require.Contains(t, updated, "def sum_values(a, b):")
	second, err := client.NextEdit(ctx, input)
	require.NoError(t, err)
	require.Equal(t, first.Edits, second.Edits)
	require.Positive(t, second.Usage.CacheReadTokens)
	require.EqualValues(t, 1, counted.opens.Load())
	t.Logf("native Next Edit result %q; warm prefix reused %d tokens", updated, second.Usage.CacheReadTokens)
}
