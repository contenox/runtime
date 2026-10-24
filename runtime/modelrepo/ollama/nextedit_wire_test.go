package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/ollama"
	"github.com/stretchr/testify/require"
)

func TestUnit_OllamaNextEditUsesRawGenerationAndProfileParser(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/generate", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		_, _ = w.Write([]byte(`{"response":"new\n","done":true,"done_reason":"stop","prompt_eval_count":20,"eval_count":2}`))
	}))
	defer server.Close()
	provider := ollama.NewOllamaProvider("explicit-model", []string{server.URL}, nil, modelrepo.CapabilityConfig{CanNextEdit: true, NextEditFormat: "sweep-v1"}, "", nil)
	require.True(t, provider.CanNextEdit())
	client, err := provider.GetNextEditConnection(context.Background(), server.URL)
	require.NoError(t, err)
	result, err := client.NextEdit(context.Background(), modelrepo.NextEditRequest{Input: modelcontract.NextEditInput{Path: "main.go", Before: "old\n", Current: "old\n", Cursor: 4, Start: 0, End: 4}, MaxTokens: 64, TopP: 1, SessionKey: "session"})
	require.NoError(t, err)
	require.Equal(t, []modelcontract.TextEdit{{Start: 0, End: 3, OldText: "old", NewText: "new"}}, result.Edits)
	require.Equal(t, "explicit-model", request["model"])
	require.Equal(t, true, request["raw"])
}

func TestUnit_OllamaNextEditRejectsTruncatedOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"response":"new","done":true,"done_reason":"length"}`))
	}))
	defer server.Close()
	provider := ollama.NewOllamaProvider("model", []string{server.URL}, nil, modelrepo.CapabilityConfig{CanNextEdit: true, NextEditFormat: "sweep-v1"}, "", nil)
	client, err := provider.GetNextEditConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.NextEdit(context.Background(), modelrepo.NextEditRequest{Input: modelcontract.NextEditInput{Path: "main.go", Before: "old\n", Current: "old\n", Cursor: 4, Start: 0, End: 4}, MaxTokens: 64, TopP: 1, SessionKey: "session"})
	require.ErrorContains(t, err, "complete")
}
