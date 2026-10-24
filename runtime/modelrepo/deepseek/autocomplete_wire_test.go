package deepseek_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/deepseek"
	"github.com/stretchr/testify/require"
)

func TestUnit_DeepSeekCatalogDoesNotInferFIMFromModelNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-flash"},{"id":"deepseek-v4-pro"}]}`))
	}))
	defer server.Close()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: server.URL})
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 2)
	for _, model := range models {
		require.True(t, model.CanChat)
		require.False(t, model.CanAutocomplete)
		require.Empty(t, model.FIMFormat)
		require.False(t, catalog.ProviderFor(model).CanNextEdit())
	}
}

func TestUnit_DeepSeekNativeFIMUsesBetaEndpointAndRawCompletionSchema(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/beta/completions", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		_, _ = w.Write([]byte(`{"choices":[{"text":"\treturn 1\n","finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_cache_hit_tokens":4}}`))
	}))
	defer server.Close()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: server.URL, APIKey: "secret"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "deepseek-v4-pro", CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanAutocomplete: true, FIMFormat: "deepseek-fim"}})
	client, err := provider.GetAutocompleteConnection(context.Background(), server.URL)
	require.NoError(t, err)
	result, err := client.Autocomplete(context.Background(), modelrepo.AutocompleteRequest{Prefix: "def f():\n", Suffix: "\n", MaxTokens: 32, TopP: 1, SessionKey: "session"})
	require.NoError(t, err)
	require.Equal(t, "\treturn 1\n", result.Text)
	require.Equal(t, "def f():\n", request["prompt"])
	require.Equal(t, "\n", request["suffix"])
	require.Equal(t, "deepseek-v4-pro", request["model"])
	require.Equal(t, false, request["stream"])
	require.Equal(t, 12, result.Usage.PromptTokens)
	require.Equal(t, 4, result.Usage.CacheReadTokens)
}

func TestUnit_DeepSeekFIMRejectsLimitsAboveDocumentedMaximum(t *testing.T) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: "https://example.test"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "deepseek-v4-pro", CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, FIMFormat: "deepseek-fim"}})
	client, err := provider.GetAutocompleteConnection(context.Background(), "https://example.test")
	require.NoError(t, err)
	_, err = client.Autocomplete(context.Background(), modelrepo.AutocompleteRequest{Prefix: "x", MaxTokens: 4097, TopP: 1, SessionKey: "session"})
	require.ErrorContains(t, err, "documented maximum")
}

func TestUnit_DeepSeekFIMRejectsWrongOperationContract(t *testing.T) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: "https://example.test"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "deepseek-v4-pro", CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, FIMFormat: "qwen2.5-coder"}})
	_, err = provider.GetAutocompleteConnection(context.Background(), "https://example.test")
	require.ErrorContains(t, err, "deepseek-fim operation contract")
}

func TestUnit_DeepSeekFIMHonorsCallerCancellation(t *testing.T) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: "https://example.test"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "deepseek-v4-pro", CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, FIMFormat: "deepseek-fim"}})
	client, err := provider.GetAutocompleteConnection(context.Background(), "https://example.test")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Autocomplete(ctx, modelrepo.AutocompleteRequest{Prefix: "x", MaxTokens: 1, TopP: 1, SessionKey: "session"})
	require.ErrorIs(t, err, context.Canceled)
}

func TestUnit_DeepSeekFIMFromTheVersionedBaseUsesTheRootBetaPath(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"choices":[{"text":"a + b","finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`))
	}))
	defer server.Close()
	base := server.URL + "/v1"
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: base, APIKey: "secret"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "deepseek-flash", CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanAutocomplete: true, FIMFormat: "deepseek-fim"}})
	client, err := provider.GetAutocompleteConnection(context.Background(), base)
	require.NoError(t, err)
	result, err := client.Autocomplete(context.Background(), modelrepo.AutocompleteRequest{Prefix: "def add(a, b):\n    return ", Suffix: "\n", MaxTokens: 8, TopP: 1, SessionKey: "session"})
	require.NoError(t, err)
	require.Equal(t, "a + b", result.Text)
	require.Equal(t, "/beta/completions", path, "the vendor serves FIM at the root beta base, not under /v1")
}
