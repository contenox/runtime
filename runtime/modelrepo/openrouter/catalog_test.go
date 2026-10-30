package openrouter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_CatalogProvider_ArchitectureInputModalitiesSetAudioAndVision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{
					"id": "google/gemini-4-argon",
					"context_length": 1000000,
					"architecture": {
						"modality": "text+image+audio->text",
						"input_modalities": ["text", "image", "audio"]
					},
					"top_provider": {"context_length": 1000000, "max_completion_tokens": 65536}
				},
				{
					"id": "meta/muse-spark-open",
					"context_length": 200000,
					"architecture": {
						"modality": "text+audio->text",
						"input_modalities": ["text", "audio"]
					},
					"top_provider": {"context_length": 200000, "max_completion_tokens": 8192}
				},
				{
					"id": "openai/gpt-text-only",
					"context_length": 128000,
					"architecture": {
						"modality": "text->text",
						"input_modalities": ["text"]
					},
					"top_provider": {"context_length": 128000, "max_completion_tokens": 8192}
				}
			]
		}`))
	}))
	defer server.Close()

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "openrouter", BaseURL: server.URL, APIKey: "test-key"})
	require.NoError(t, err)

	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	byName := map[string]modelrepo.ObservedModel{}
	for _, m := range models {
		byName[m.Name] = m
	}
	gemini := byName["google/gemini-4-argon"]
	require.True(t, gemini.CanAudio)
	require.True(t, gemini.CanVision)
	require.True(t, catalog.ProviderFor(gemini).CanAudio())

	audioOnly := byName["meta/muse-spark-open"]
	require.True(t, audioOnly.CanAudio)
	require.False(t, audioOnly.CanVision)

	require.False(t, byName["openai/gpt-text-only"].CanAudio)
	require.False(t, catalog.ProviderFor(byName["openai/gpt-text-only"]).CanAudio())
}
