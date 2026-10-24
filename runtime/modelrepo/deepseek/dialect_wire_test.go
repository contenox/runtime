package deepseek_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/deepseek"
	"github.com/stretchr/testify/require"
)

func deepseekProvider(t *testing.T, server *httptest.Server, caps modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "deepseek", BaseURL: server.URL, APIKey: "secret"})
	require.NoError(t, err)
	return catalog.ProviderFor(modelrepo.ObservedModel{Name: "deepseek-v4-pro", CapabilityConfig: caps})
}

func deepseekChatServer(t *testing.T, body *map[string]any, called *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(body))
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
	}))
}

func deepseekThinkingCaps() modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat: true, CanStream: true, CanThink: true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true, Disabled: true, EffortLevels: []string{"high", "max"}},
	}
}

func deepseekTool() modelrepo.Tool {
	return modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "get_date"}}
}

func TestUnit_DeepSeekRequiredToolChoiceIsRefusedWhileThinkingByDefault(t *testing.T) {
	var body map[string]any
	called := false
	server := deepseekChatServer(t, &body, &called)
	defer server.Close()
	provider := deepseekProvider(t, server, deepseekThinkingCaps())
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTool(deepseekTool()), modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}))
	require.ErrorContains(t, err, "required tool choice while thinking is enabled")
	require.False(t, called, "a request the vendor rejects must be refused before the wire")
}

func TestUnit_DeepSeekNamedToolChoiceIsRefusedWhileThinkingByDefault(t *testing.T) {
	var body map[string]any
	called := false
	server := deepseekChatServer(t, &body, &called)
	defer server.Close()
	provider := deepseekProvider(t, server, deepseekThinkingCaps())
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTool(deepseekTool()), modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "get_date"}))
	require.ErrorContains(t, err, "named tool choice while thinking is enabled")
	require.False(t, called, "a request the vendor rejects must be refused before the wire")
}

func TestUnit_DeepSeekRequiredToolChoiceIsRefusedAtANamedLevel(t *testing.T) {
	var body map[string]any
	called := false
	server := deepseekChatServer(t, &body, &called)
	defer server.Close()
	provider := deepseekProvider(t, server, deepseekThinkingCaps())
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("high"), modelrepo.WithTool(deepseekTool()), modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}))
	require.ErrorContains(t, err, "required tool choice while thinking is enabled")
	require.False(t, called)
}

func TestUnit_DeepSeekRequiredToolChoiceIsAcceptedWhenThinkingIsOff(t *testing.T) {
	var body map[string]any
	called := false
	server := deepseekChatServer(t, &body, &called)
	defer server.Close()
	provider := deepseekProvider(t, server, deepseekThinkingCaps())
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("off"), modelrepo.WithTool(deepseekTool()), modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}))
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "required", body["tool_choice"])
	thinking, ok := body["thinking"].(map[string]any)
	require.True(t, ok, "the off level is sent as the vendor's own disable toggle")
	require.Equal(t, "disabled", thinking["type"])
}

func TestUnit_DeepSeekNamedToolChoiceIsAcceptedWhenThinkingIsOff(t *testing.T) {
	var body map[string]any
	called := false
	server := deepseekChatServer(t, &body, &called)
	defer server.Close()
	provider := deepseekProvider(t, server, deepseekThinkingCaps())
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("off"), modelrepo.WithTool(deepseekTool()), modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "get_date"}))
	require.NoError(t, err)
	require.True(t, called)
	choice, ok := body["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", choice["type"])
}

func TestUnit_DeepSeekRequiredToolChoiceIsAcceptedOnAModelThatCannotThink(t *testing.T) {
	var body map[string]any
	called := false
	server := deepseekChatServer(t, &body, &called)
	defer server.Close()
	provider := deepseekProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	client, err := provider.GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTool(deepseekTool()), modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "get_date"}))
	require.NoError(t, err)
	require.True(t, called)
	require.NotContains(t, body, "thinking")
	_, ok := body["tool_choice"].(map[string]any)
	require.True(t, ok)
}

func deepseekStreamServer(t *testing.T, body *map[string]any, frames ...string) *httptest.Server {
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

func TestUnit_DeepSeekUndeclaredThinkingSendsNoThinkingControl(t *testing.T) {
	for _, level := range []string{"high", "low", "off"} {
		var chatBody map[string]any
		called := false
		chatServer := deepseekChatServer(t, &chatBody, &called)
		chat, err := deepseekProvider(t, chatServer, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}).GetChatConnection(context.Background(), chatServer.URL)
		require.NoError(t, err)
		_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink(level))
		require.NoError(t, err, level)
		require.True(t, called, level)
		require.NotContains(t, chatBody, "thinking", level)
		require.NotContains(t, chatBody, "reasoning_effort", level)
		chatServer.Close()

		var streamBody map[string]any
		streamServer := deepseekStreamServer(t, &streamBody, `{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`)
		stream, err := deepseekProvider(t, streamServer, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}).GetStreamConnection(context.Background(), streamServer.URL)
		require.NoError(t, err)
		parcels, err := stream.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink(level))
		require.NoError(t, err, level)
		for range parcels {
		}
		require.Equal(t, true, streamBody["stream"], level)
		require.NotContains(t, streamBody, "thinking", level)
		require.NotContains(t, streamBody, "reasoning_effort", level)
		streamServer.Close()
	}
}

func TestUnit_DeepSeekStreamedErrorFrameFailsTheTurn(t *testing.T) {
	var body map[string]any
	server := deepseekStreamServer(t, &body,
		`{"choices":[{"index":0,"delta":{"content":"par"}}]}`,
		`{"error":{"message":"injected mid-stream error","type":"server_error","code":500}}`)
	defer server.Close()
	provider := deepseekProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	client, err := provider.GetStreamConnection(context.Background(), server.URL)
	require.NoError(t, err)
	parcels, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	assembly := modelrepo.NewStreamAssembler("deepseek", "deepseek-v4-pro")
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
