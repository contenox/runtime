package alibabacloud_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/alibabacloud"
	"github.com/stretchr/testify/require"
)

func TestUnit_AlibabaCloudCatalogListsChatModelsFromTheCompatibleModeEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/compatible-mode/v1/models", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"qwen3.8-max","object":"model","created":1781439068,"ownedBy":"system"},
			{"id":"qwen3.5-plus","object":"model"},
			{"id":"qwen3-coder-plus","object":"model"},
			{"id":"qwen-plus","object":"model"},
			{"id":"qwen3.7-max-preview","object":"model"},
			{"id":"text-embedding-v4","object":"model"},
			{"id":"qwen-tts-latest","object":"model"}
		]}`))
	}))
	defer server.Close()

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "alibaba-cloud", BaseURL: server.URL + "/compatible-mode/v1/", APIKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, "alibaba-cloud", catalog.Type())

	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	byName := map[string]modelrepo.ObservedModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	require.Len(t, byName, 7)

	max := byName["qwen3.8-max"]
	require.True(t, max.CanChat)
	require.True(t, max.CanStream)
	require.True(t, max.CanThink)
	require.True(t, max.ThinkingTypes.Enabled)
	require.True(t, max.ThinkingTypes.Disabled, "qwen3.x flagships are hybrid: thinking can be switched off")
	require.True(t, max.CanVision)
	require.Equal(t, "alibaba-cloud", max.Dialect)
	require.Equal(t, "qwen3.8-max", max.Meta[modelrepo.ObservedDisplayNameMetaKey])

	coder := byName["qwen3-coder-plus"]
	require.True(t, coder.CanChat)
	require.False(t, coder.CanThink)
	require.False(t, coder.CanVision)

	plus := byName["qwen-plus"]
	require.True(t, plus.CanThink)
	require.True(t, plus.ThinkingTypes.Disabled)

	preview := byName["qwen3.7-max-preview"]
	require.True(t, preview.CanThink)
	require.False(t, preview.ThinkingTypes.Disabled, "thinking-only models cannot switch thinking off")

	embedding := byName["text-embedding-v4"]
	require.False(t, embedding.CanChat)
	require.True(t, embedding.CanEmbed)
	require.False(t, byName["qwen-tts-latest"].CanChat)

	provider := catalog.ProviderFor(max)
	require.Equal(t, "alibaba-cloud", provider.GetType())
	require.False(t, provider.CanAutocomplete())
	require.False(t, provider.CanNextEdit())
	require.True(t, provider.CanChat())
}

func TestUnit_AlibabaCloudCatalogDefaultsToTheInternationalEndpoint(t *testing.T) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "alibaba-cloud", APIKey: "test-key"})
	require.NoError(t, err)
	provider := catalog.ProviderFor(modelrepo.ObservedModel{Name: "qwen3.5-plus", CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true}})
	require.Equal(t, "alibaba-cloud", provider.GetType())
}

func TestUnit_AlibabaCloudCatalogSurfacesAnInvalidKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error","param":null,"code":"invalid_api_key"},"request_id":"r"}`))
	}))
	defer server.Close()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "alibaba-cloud", BaseURL: server.URL, APIKey: "bad"})
	require.NoError(t, err)
	_, err = catalog.ListModels(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "Incorrect API key")
}
