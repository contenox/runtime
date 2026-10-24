package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/openai"
	"github.com/stretchr/testify/require"
)

func TestUnit_OpenAIPrompt_ForwardsOutputTokenBound(t *testing.T) {
	var received openaiChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"return 42"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	provider := openai.NewOpenAIProvider("", "gpt-4o-mini", []string{server.URL + "/v1"}, modelrepo.CapabilityConfig{CanPrompt: true, MaxOutputTokens: 64}, server.Client(), nil)
	client, err := provider.GetPromptConnection(context.Background(), "")
	require.NoError(t, err)
	text, _, err := client.Prompt(context.Background(), "Return code only", 0, "complete", modelrepo.WithPromptMaxTokens(7))
	require.NoError(t, err)
	require.Equal(t, "return 42", text)
	require.NotNil(t, received.MaxCompletionTokens)
	require.Equal(t, 7, *received.MaxCompletionTokens)
}

type openaiChatRequest struct {
	MaxCompletionTokens *int `json:"max_completion_tokens"`
}
