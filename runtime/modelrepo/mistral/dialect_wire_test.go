package mistral_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/mistral"
	"github.com/stretchr/testify/require"
)

func mistralProvider(t *testing.T, server *httptest.Server, caps modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "mistral", BaseURL: server.URL, APIKey: "secret"})
	require.NoError(t, err)
	return catalog.ProviderFor(modelrepo.ObservedModel{Name: "mistral-medium-3-5", CapabilityConfig: caps})
}

func mistralChatServer(t *testing.T, body *map[string]any, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(body))
		_, _ = w.Write([]byte(response))
	}))
}

func mistralChatResponse() string {
	return `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
}

func TestUnit_MistralChatUsesDocumentedFieldNames(t *testing.T) {
	var body map[string]any
	server := mistralChatServer(t, &body, mistralChatResponse())
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanThink: true})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithMaxTokens(64), modelrepo.WithSeed(7), modelrepo.WithTemperature(0.5))
	require.NoError(t, err)
	require.Equal(t, float64(64), body["max_tokens"])
	require.NotContains(t, body, "max_completion_tokens")
	require.Equal(t, float64(7), body["random_seed"])
	require.NotContains(t, body, "seed")
	require.NotContains(t, body, "stream_options")
	require.NotContains(t, body, "reasoning_effort")
}

func TestUnit_MistralChatSendsDocumentedReasoningEffort(t *testing.T) {
	var body map[string]any
	server := mistralChatServer(t, &body, mistralChatResponse())
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanThink: true})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("medium"))
	require.NoError(t, err)
	require.Equal(t, "high", body["reasoning_effort"])

	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("off"))
	require.NoError(t, err)
	require.Equal(t, "none", body["reasoning_effort"])
}

func TestUnit_MistralReasoningEffortHonoursDeclaredLevels(t *testing.T) {
	var body map[string]any
	server := mistralChatServer(t, &body, mistralChatResponse())
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{
		CanChat: true, CanStream: true, CanThink: true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true, Disabled: true, EffortLevels: []string{"high"}},
	})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("high"))
	require.NoError(t, err)
	require.Equal(t, "high", body["reasoning_effort"])

	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("low"))
	require.ErrorContains(t, err, "low")
}

func TestUnit_MistralReasoningOffHonoursUndeclarableDisable(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(mistralChatResponse()))
	}))
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{
		CanChat: true, CanStream: true, CanThink: true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true, EffortLevels: []string{"high"}},
	})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("off"))
	require.ErrorContains(t, err, "off")
	require.False(t, called, "a model whose declaration rejects being turned off must not receive a disable control")
}

func TestUnit_MistralStreamingRequestOmitsStreamOptions(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanThink: true})
	client, err := provider.GetStreamConnection(context.Background(), server.URL)
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	for range stream {
	}
	require.Equal(t, true, body["stream"])
	require.NotContains(t, body, "stream_options")
	require.NotContains(t, body, "max_completion_tokens")
}

func TestUnit_MistralDecodesThinkingChunkContent(t *testing.T) {
	var body map[string]any
	server := mistralChatServer(t, &body, `{"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"thinking","thinking":[{"type":"text","text":"weighing it"}]},{"type":"text","text":"the answer"}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`)
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true, CanThink: true})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	result, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("high"))
	require.NoError(t, err)
	require.Equal(t, "the answer", result.Message.Content)
	require.Equal(t, "weighing it", result.Message.Thinking)
}

func TestUnit_MistralToolWithoutSchemaCarriesParameters(t *testing.T) {
	var body map[string]any
	server := mistralChatServer(t, &body, mistralChatResponse())
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTool(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "get_date"}}))
	require.NoError(t, err)
	tools, ok := body["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	function := tools[0].(map[string]any)["function"].(map[string]any)
	parameters, ok := function["parameters"].(map[string]any)
	require.True(t, ok, "tools[].function.parameters is required by Mistral")
	require.Equal(t, "object", parameters["type"])
}

func TestUnit_MistralRefusesTemperatureAboveDocumentedMaximum(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(mistralChatResponse()))
	}))
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithTemperature(1.8))
	require.ErrorContains(t, err, "at most 1.5")
	require.False(t, called, "an out-of-range temperature must be refused before the wire")
}

func TestUnit_MistralValidationEnvelopeNamesTheRejectedMember(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"loc":["body","max_tokens"],"msg":"Extra inputs are not permitted","type":"value_error"}]}`))
	}))
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithMaxTokens(16))
	require.ErrorContains(t, err, "body.max_tokens")
	require.ErrorContains(t, err, "Extra inputs are not permitted")
	require.ErrorContains(t, err, "mistral")
}

func mistralStreamServer(t *testing.T, body *map[string]any, frames ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(body))
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
}

func TestUnit_MistralUndeclaredThinkingSendsNoReasoningEffort(t *testing.T) {
	for _, level := range []string{"high", "low", "off"} {
		var chatBody map[string]any
		chatServer := mistralChatServer(t, &chatBody, mistralChatResponse())
		chat, err := mistralProvider(t, chatServer, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}).GetChatConnection(context.Background(), chatServer.URL)
		require.NoError(t, err)
		_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink(level))
		require.NoError(t, err, level)
		require.NotContains(t, chatBody, "reasoning_effort", level)
		chatServer.Close()

		var streamBody map[string]any
		streamServer := mistralStreamServer(t, &streamBody, `{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`)
		stream, err := mistralProvider(t, streamServer, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}).GetStreamConnection(context.Background(), streamServer.URL)
		require.NoError(t, err)
		parcels, err := stream.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink(level))
		require.NoError(t, err, level)
		for range parcels {
		}
		require.Equal(t, true, streamBody["stream"], level)
		require.NotContains(t, streamBody, "reasoning_effort", level)
		streamServer.Close()
	}
}

func TestUnit_MistralStreamedErrorFrameFailsTheTurn(t *testing.T) {
	var body map[string]any
	server := mistralStreamServer(t, &body,
		`{"choices":[{"index":0,"delta":{"content":"par"}}]}`,
		`{"error":{"message":"injected mid-stream error","type":"server_error","code":500}}`)
	defer server.Close()
	provider := mistralProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	client, err := provider.GetStreamConnection(context.Background(), server.URL)
	require.NoError(t, err)
	parcels, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assembly := modelrepo.NewStreamAssembler("mistral", "mistral-medium-3-5")
	var failure error
	for parcel := range parcels {
		if err := assembly.Consume(parcel); err != nil {
			failure = err
		}
	}
	require.ErrorContains(t, failure, "injected mid-stream error")
	_, err = assembly.Result()
	require.ErrorContains(t, err, "injected mid-stream error")
}
