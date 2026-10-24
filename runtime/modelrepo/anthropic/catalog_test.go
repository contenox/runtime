package anthropic_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_CatalogProvider_VisionFromImageInputCapability(t *testing.T) {
	const body = `{
		"data": [
			{
				"id": "claude-opus-4-6",
				"created_at": "2026-02-04T00:00:00Z",
				"max_input_tokens": 200000,
				"max_tokens": 64000,
				"capabilities": {
					"image_input": {"supported": true},
					"thinking": {"supported": true}
				}
			},
			{
				"id": "claude-haiku-text-only",
				"created_at": "2026-02-04T00:00:00Z",
				"max_input_tokens": 100000,
				"max_tokens": 8000,
				"capabilities": {
					"image_input": {"supported": false}
				}
			},
			{
				"id": "claude-no-capabilities-object"
			}
		],
		"has_more": false,
		"last_id": "claude-no-capabilities-object"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Equal(t, "test-key", r.Header.Get("x-api-key"))
		require.Equal(t, anthropicVersionHeader, r.Header.Get("anthropic-version"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	catalog := newCatalog(t, server.URL, "test-key")
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 3)

	byName := map[string]modelrepo.ObservedModel{}
	for _, m := range models {
		byName[m.Name] = m
	}

	require.True(t, byName["claude-opus-4-6"].CanVision, "image_input.supported=true must set CanVision")
	require.False(t, byName["claude-haiku-text-only"].CanVision, "image_input.supported=false must leave CanVision unset")
	require.False(t, byName["claude-no-capabilities-object"].CanVision, "absent capabilities object must leave CanVision unset")

	require.True(t, catalog.ProviderFor(byName["claude-opus-4-6"]).CanVision())
	require.False(t, catalog.ProviderFor(byName["claude-haiku-text-only"]).CanVision())
}

func TestUnit_CatalogProvider_DocumentedListModelsShape(t *testing.T) {
	const page1 = `{
		"data": [
			{
				"id": "claude-opus-4-6",
				"type": "model",
				"display_name": "Claude Opus 4.6",
				"created_at": "2026-02-04T00:00:00Z",
				"max_input_tokens": 1000000,
				"max_tokens": 128000,
				"capabilities": {
					"image_input": {"supported": true},
					"effort": {
						"high": {"supported": true},
						"low": {"supported": true},
						"max": {"supported": true},
						"medium": {"supported": true},
						"supported": true,
						"xhigh": {"supported": true}
					},
					"structured_outputs": {"supported": true},
					"thinking": {
						"supported": false,
						"types": {
							"adaptive": {"supported": true},
							"enabled": {"supported": false}
						}
					}
				}
			}
		],
		"first_id": "claude-opus-4-6",
		"has_more": true,
		"last_id": "claude-opus-4-6"
	}`
	const page2 = `{
		"data": [
			{
				"id": "claude-haiku-4-5",
				"type": "model",
				"display_name": "Claude Haiku 4.5",
				"created_at": "2025-10-01T00:00:00Z",
				"max_input_tokens": 200000,
				"max_tokens": 64000,
				"capabilities": {
					"effort": {"supported": true, "high": {"supported": true}},
					"image_input": {"supported": true},
					"thinking": {"supported": false, "types": {"adaptive": {"supported": false}, "enabled": {"supported": false}}}
				}
			}
		],
		"first_id": "claude-haiku-4-5",
		"has_more": false,
		"last_id": "claude-haiku-4-5"
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "claude-opus-4-6" {
			_, _ = w.Write([]byte(page2))
			return
		}
		require.Empty(t, r.URL.Query().Get("after_id"))
		_, _ = w.Write([]byte(page1))
	}))
	defer server.Close()

	models, err := newCatalog(t, server.URL, "test-key").ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 2, "pagination must follow has_more/last_id")

	byName := map[string]modelrepo.ObservedModel{}
	for _, m := range models {
		byName[m.Name] = m
	}

	opus := byName["claude-opus-4-6"]
	require.Equal(t, 1000000, opus.ContextLength, "context window comes from max_input_tokens")
	require.Equal(t, 128000, opus.MaxOutputTokens, "output ceiling comes from max_tokens")
	require.True(t, opus.CanVision)
	require.True(t, opus.CanThink, "thinking.types.adaptive.supported must count as thinking support")
	require.True(t, opus.CanChat)
	require.True(t, opus.CanStream)

	haiku := byName["claude-haiku-4-5"]
	require.Equal(t, 200000, haiku.ContextLength)
	require.Equal(t, 64000, haiku.MaxOutputTokens)
	require.True(t, haiku.CanVision)
	require.True(t, haiku.CanThink, "effort.supported must count as thinking support")
}

func TestUnit_AnthropicCatalog_RegisteredAndChatCapable(t *testing.T) {
	cp, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "anthropic", APIKey: "k"})
	require.NoError(t, err, "anthropic must be registered in the catalog registry")
	require.Equal(t, "anthropic", cp.Type())

	prov := cp.ProviderFor(modelrepo.ObservedModel{
		Name:             "claude-sonnet-4-5",
		CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanPrompt: true},
	})
	require.Equal(t, "anthropic", prov.GetType())
	require.True(t, prov.CanChat())
	require.False(t, prov.CanEmbed())
}

func TestUnit_AnthropicCatalog_DetectsThinkingFromCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Empty(t, r.URL.RawQuery)
		require.Equal(t, "secret-key", r.Header.Get("x-api-key"))
		require.Equal(t, anthropicVersionHeader, r.Header.Get("anthropic-version"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [{
				"id": "claude-sonnet-4-5",
				"created_at": "2026-02-19T00:00:00Z",
				"max_input_tokens": 200000,
				"capabilities": {
					"thinking": {"supported": true, "types": {"enabled": {"supported": true}, "adaptive": {"supported": false}}},
					"effort": {"supported": false}
				}
			}]
		}`))
	}))
	defer server.Close()

	catalog := newCatalog(t, server.URL, "secret-key")
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "claude-sonnet-4-5", models[0].Name)
	require.Equal(t, 200000, models[0].ContextLength)
	require.True(t, models[0].CanThink)
	require.True(t, catalog.ProviderFor(models[0]).CanThink())
}

func TestUnit_AnthropicCatalog_DoesNotInferThinkingWhenCapabilitiesAreMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Empty(t, r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-4-5","type":"model","max_tokens":64000}]}`))
	}))
	defer server.Close()

	models, err := newCatalog(t, server.URL, "").ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "claude-sonnet-4-5", models[0].Name)
	require.Equal(t, 64000, models[0].MaxOutputTokens)
	require.False(t, models[0].CanThink, "missing capability metadata must not fall back to model-name inference")
}

func TestUnit_AnthropicCatalog_PaginatesListModels(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			_, _ = w.Write([]byte(`{
				"data": [{"id":"claude-fable-5","created_at":"2026-01-01T00:00:00Z","max_input_tokens":200000}],
				"has_more": true,
				"last_id": "claude-fable-5"
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"data": [{"id":"claude-sonnet-4-6","created_at":"2026-01-01T00:00:00Z","max_input_tokens":200000}],
			"has_more": false,
			"last_id": "claude-sonnet-4-6"
		}`))
	}))
	defer server.Close()

	models, err := newCatalog(t, server.URL, "key").ListModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, callCount, "must have made 2 requests for 2 pages")
	require.Len(t, models, 2)
	names := []string{models[0].Name, models[1].Name}
	require.Contains(t, names, "claude-fable-5")
	require.Contains(t, names, "claude-sonnet-4-6")
}

func newCatalog(t *testing.T, baseURL, apiKey string) modelrepo.CatalogProvider {
	t.Helper()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{
		Type:    "anthropic",
		BaseURL: baseURL,
		APIKey:  apiKey,
	})
	require.NoError(t, err)
	return catalog
}
