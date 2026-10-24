package vllm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/vllm"
	"github.com/stretchr/testify/require"
)

func TestUnit_VLLMNextEditUsesRawCompletionAndValidatesEdits(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		_, _ = w.Write([]byte(`{"choices":[{"text":"new\n","finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	provider := vllm.NewVLLMProvider("explicit-model", []string{server.URL}, nil, modelrepo.CapabilityConfig{CanNextEdit: true, NextEditFormat: "sweep-v1"}, "secret", nil)
	require.True(t, provider.CanNextEdit())
	client, err := provider.GetNextEditConnection(context.Background(), server.URL)
	require.NoError(t, err)
	result, err := client.NextEdit(context.Background(), modelrepo.NextEditRequest{Input: modelcontract.NextEditInput{Path: "main.go", Before: "old\n", Current: "old\n", Cursor: 4, Start: 0, End: 4}, MaxTokens: 64, TopP: 1, SessionKey: "session"})
	require.NoError(t, err)
	require.Equal(t, []modelcontract.TextEdit{{Start: 0, End: 3, OldText: "old", NewText: "new"}}, result.Edits)
	require.Equal(t, "explicit-model", request["model"])
}

func TestUnit_VLLMCatalogRequiresExplicitNextEditProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"same-model","next_edit":true,"next_edit_format":"sweep-v2"},{"id":"sweep-v1","next_edit":false}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "vllm", BaseURL: server.URL})
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.True(t, catalog.ProviderFor(models[0]).CanNextEdit())
	require.Equal(t, "sweep-v2", models[0].NextEditFormat)
	require.False(t, catalog.ProviderFor(models[1]).CanNextEdit())
}
