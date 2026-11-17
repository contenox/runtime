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
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

func TestSystem_ModeldNativeFIMPreservesInsertionAndReusesPrefix(t *testing.T) {
	path := os.Getenv("CONTENOX_LLAMA_FIM_GGUF")
	if path == "" {
		t.Skip("CONTENOX_LLAMA_FIM_GGUF is not set")
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
	go func() { done <- transportgrpc.Serve(ctx, listener, service, "fim-owner", "llama") }()
	t.Cleanup(func() {
		modeldconn.CloseEndpoint(listener.Addr().String())
		cancel()
		require.NoError(t, <-done)
		require.NoError(t, service.Shutdown(context.Background()))
	})
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "modeld", BaseURL: listener.Addr().String()})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: filepath.Base(filepath.Dir(path)), Meta: map[string]string{"type": "llama"}, CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, CompletionMode: "fim", FIMFormat: "qwen2.5-coder"}})
	client, err := provider.GetAutocompleteConnection(ctx, listener.Addr().String())
	require.NoError(t, err)
	input := modelrepo.AutocompleteRequest{Prefix: "package mathutil\n\n// Add returns the sum of a and b.\nfunc Add(a, b int) int {\n\treturn ", Suffix: "\n}\n", MaxTokens: 16, Temperature: 0, TopP: 1, Seed: 0, SessionKey: "first-client:first-document"}
	first, err := client.Autocomplete(ctx, input)
	require.NoError(t, err)
	require.Equal(t, "a + b", strings.TrimSpace(first.Text))
	require.NotNil(t, first.Usage)
	require.Zero(t, first.Usage.CacheReadTokens)
	second, err := client.Autocomplete(ctx, input)
	require.NoError(t, err)
	require.Equal(t, first.Text, second.Text)
	require.Positive(t, second.Usage.CacheReadTokens)
	require.EqualValues(t, 1, counted.opens.Load())
	input.SessionKey = "second-client:first-document"
	third, err := client.Autocomplete(ctx, input)
	require.NoError(t, err)
	require.Equal(t, first.Text, third.Text)
	require.Zero(t, third.Usage.CacheReadTokens)
	require.EqualValues(t, 2, counted.opens.Load())
	cancelled, stop := context.WithCancel(ctx)
	stop()
	_, err = client.Autocomplete(cancelled, input)
	require.ErrorIs(t, err, context.Canceled)
	t.Logf("native FIM insertion %q; warm prefix reused %d tokens; other client reused %d", first.Text, second.Usage.CacheReadTokens, third.Usage.CacheReadTokens)
}
