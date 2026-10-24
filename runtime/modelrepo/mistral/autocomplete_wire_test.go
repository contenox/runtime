package mistral_test

import (
	"context"
	"encoding/json"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/mistral"
	"github.com/stretchr/testify/require"
)

func TestUnit_MistralCatalogDeclaresOnlyAdvertisedFIM(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"codestral-latest","max_context_length":32768,"capabilities":{"completion_chat":true,"completion_fim":true}},{"id":"mistral-small","capabilities":{"completion_chat":true,"completion_fim":false}}]}`))
	}))
	defer server.Close()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "mistral", BaseURL: server.URL, APIKey: "secret"})
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 2)
	require.True(t, models[0].CanAutocomplete)
	require.Equal(t, "mistral-fim", models[0].FIMFormat)
	require.Equal(t, runtimetypes.CompletionModeFIM, models[0].CompletionMode, "a native FIM declaration names the fim completion operation")
	require.False(t, models[1].CanAutocomplete)
	require.Empty(t, models[1].FIMFormat)
	require.Empty(t, models[1].CompletionMode)
	provider := catalog.ProviderFor(models[0])
	require.Equal(t, "mistral", provider.GetType())
	require.True(t, provider.CanChat())
	require.False(t, provider.CanPrompt())
	require.False(t, provider.CanNextEdit())
	_, err = provider.GetNextEditConnection(context.Background(), server.URL)
	require.ErrorIs(t, err, modelrepo.ErrNextEditUnsupported)
}

func TestUnit_MistralNativeFIMUsesProviderEndpointAndSchema(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/fim/completions", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"\treturn 1\n"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`))
	}))
	defer server.Close()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "mistral", BaseURL: server.URL + "/v1", APIKey: "secret"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "codestral-latest", CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true, CanAutocomplete: true, FIMFormat: "mistral-fim"}})
	client, err := provider.GetAutocompleteConnection(context.Background(), server.URL+"/v1")
	require.NoError(t, err)
	result, err := client.Autocomplete(context.Background(), modelrepo.AutocompleteRequest{Prefix: "def f():\n", Suffix: "\n", MaxTokens: 32, TopP: 1, SessionKey: "session"})
	require.NoError(t, err)
	require.Equal(t, "\treturn 1\n", result.Text)
	require.Equal(t, "def f():\n", request["prompt"])
	require.Equal(t, "\n", request["suffix"])
	require.Equal(t, "codestral-latest", request["model"])
	require.Equal(t, float64(32), request["max_tokens"])
	require.Equal(t, 8, result.Usage.PromptTokens)
}

func TestUnit_MistralFIMRejectsWrongOperationContract(t *testing.T) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "mistral", BaseURL: "https://example.test/v1"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "codestral-latest", CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, FIMFormat: "qwen2.5-coder"}})
	_, err = provider.GetAutocompleteConnection(context.Background(), "https://example.test/v1")
	require.ErrorContains(t, err, "mistral-fim operation contract")
}

func TestUnit_MistralFIMHonorsCallerCancellation(t *testing.T) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "mistral", BaseURL: "https://example.test/v1"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "codestral-latest", CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, FIMFormat: "mistral-fim"}})
	client, err := provider.GetAutocompleteConnection(context.Background(), "https://example.test/v1")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Autocomplete(ctx, modelrepo.AutocompleteRequest{Prefix: "x", MaxTokens: 1, TopP: 1, SessionKey: "session"})
	require.ErrorIs(t, err, context.Canceled)
}
