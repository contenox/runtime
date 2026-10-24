package modelrepo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/ollama"
	_ "github.com/contenox/runtime/runtime/modelrepo/openai"
	_ "github.com/contenox/runtime/runtime/modelrepo/vllm"
	"github.com/stretchr/testify/require"
)

func TestUnit_AutocompleteAdaptersUseRawCompletionAndPreserveWhitespace(t *testing.T) {
	for _, providerType := range []string{"ollama", "openai", "vllm"} {
		t.Run(providerType, func(t *testing.T) {
			var received map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
				require.Equal(t, "Bearer dedicated-test-key", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if providerType == "ollama" {
					require.Equal(t, "/api/generate", r.URL.Path)
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"response": "\treturn a+b\n", "done": true, "done_reason": "stop", "prompt_eval_count": 12, "eval_count": 4}))
				} else {
					require.Equal(t, "/v1/completions", r.URL.Path)
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"text": "\treturn a+b\n", "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 4, "total_tokens": 16}}))
				}
			}))
			defer server.Close()
			base := server.URL
			if providerType == "openai" {
				base += "/v1"
			}
			catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: providerType, BaseURL: base, APIKey: "dedicated-test-key"})
			require.NoError(t, err)
			model := modelrepo.ObservedModel{Name: "same-name", CapabilityConfig: modelrepo.CapabilityConfig{CanAutocomplete: true, CompletionMode: "fim", FIMFormat: "qwen2.5-coder"}}
			provider := catalog.ProviderFor(model)
			require.True(t, provider.CanAutocomplete())
			client, err := provider.GetAutocompleteConnection(context.Background(), base)
			require.NoError(t, err)
			result, err := client.Autocomplete(t.Context(), modelrepo.AutocompleteRequest{Prefix: "func add(a,b int) int {\n", Suffix: "}\n", MaxTokens: 32, TopP: 1, SessionKey: "opaque-document-key"})
			require.NoError(t, err)
			require.Equal(t, "\treturn a+b\n", result.Text)
			require.Equal(t, "<|fim_prefix|>func add(a,b int) int {\n<|fim_suffix|>}\n<|fim_middle|>", received["prompt"])
			require.NotContains(t, received, "messages")
			require.NotContains(t, received, "session_key")
			require.Equal(t, 12, result.Usage.PromptTokens)
			model.CanAutocomplete = false
			_, err = catalog.ProviderFor(model).GetAutocompleteConnection(t.Context(), base)
			require.ErrorIs(t, err, modelrepo.ErrAutocompleteUnsupported)
		})
	}
}
