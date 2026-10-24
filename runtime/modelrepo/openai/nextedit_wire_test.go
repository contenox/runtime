package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/openai"
	"github.com/stretchr/testify/require"
)

func TestUnit_OpenAINativeNextEditUsesRawCompletionAndAppliesProfile(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/completions", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		_, _ = w.Write([]byte(`{"choices":[{"text":"new\n","finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":2,"total_tokens":22}}`))
	}))
	defer server.Close()
	provider := openai.NewOpenAIProvider("secret", "explicit-model", []string{server.URL + "/v1"}, modelrepo.CapabilityConfig{CanNextEdit: true, NextEditFormat: "sweep-v1"}, nil, nil)
	require.True(t, provider.CanNextEdit())
	client, err := provider.GetNextEditConnection(context.Background(), server.URL+"/v1")
	require.NoError(t, err)
	result, err := client.NextEdit(context.Background(), modelrepo.NextEditRequest{Input: nextEditInput(), MaxTokens: 64, TopP: 1, SessionKey: "session"})
	require.NoError(t, err)
	require.Equal(t, []modelcontract.TextEdit{{Start: 0, End: 3, OldText: "old", NewText: "new"}}, result.Edits)
	require.Equal(t, "explicit-model", request["model"])
	require.Equal(t, "stop", result.FinishReason)
	require.Equal(t, 20, result.Usage.PromptTokens)
}

func TestUnit_OpenAINativeNextEditRejectsTruncatedOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"text":"new","finish_reason":"length"}]}`))
	}))
	defer server.Close()
	provider := openai.NewOpenAIProvider("", "model", []string{server.URL}, modelrepo.CapabilityConfig{CanNextEdit: true, NextEditFormat: "sweep-v1"}, nil, nil)
	client, err := provider.GetNextEditConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.NextEdit(context.Background(), modelrepo.NextEditRequest{Input: nextEditInput(), MaxTokens: 64, TopP: 1, SessionKey: "session"})
	require.ErrorContains(t, err, "complete")
}

func TestUnit_OpenAICatalogRequiresExplicitNextEditProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"same-model","next_edit":true,"next_edit_format":"sweep-v2"},{"id":"other-model"}]}`))
	}))
	defer server.Close()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "openai", BaseURL: server.URL})
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 2)
	require.Empty(t, models[0].NextEditFormat, "next_edit_format is not a field the vendor defines")
	require.False(t, models[0].CanNextEdit)
	require.False(t, catalog.ProviderFor(models[0]).CanNextEdit(), "an undeclared model cannot next-edit")

	declared := models[0]
	declared.CapabilityConfig = modelrepo.CapabilityConfig{CanNextEdit: true, NextEditFormat: "sweep-v1"}
	require.True(t, catalog.ProviderFor(declared).CanNextEdit(), "the operator's declaration grants the capability")
}

func nextEditInput() modelcontract.NextEditInput {
	return modelcontract.NextEditInput{Path: "main.go", Before: "old\n", Current: "old\n", Cursor: 4, Start: 0, End: 4}
}
