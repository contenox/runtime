package deepseek_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_CatalogProvider_InputModalitiesSetAudioAndVision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "deepseek-flash",
					"context_window": 1000000,
					"max_output_tokens": 65536,
					"input_modalities": ["text", "image"]
				},
				{
					"id": "omni-audio",
					"context_window": 128000,
					"max_output_tokens": 8192,
					"input_modalities": ["text", "audio"]
				},
				{
					"id": "deepseek-v4-pro",
					"context_window": 128000,
					"input_modalities": ["text"]
				}
			]
		}`))
	}))
	defer server.Close()

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: server.URL, APIKey: "test-key"})
	require.NoError(t, err)

	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	byName := map[string]modelrepo.ObservedModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	require.True(t, byName["deepseek-flash"].CanVision)
	require.False(t, byName["deepseek-flash"].CanAudio)
	require.True(t, byName["omni-audio"].CanAudio)
	require.False(t, byName["omni-audio"].CanVision)
	require.False(t, byName["deepseek-v4-pro"].CanAudio)
	require.True(t, catalog.ProviderFor(byName["omni-audio"]).CanAudio())
}
