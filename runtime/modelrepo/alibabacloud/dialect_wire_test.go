package alibabacloud_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	_ "github.com/contenox/runtime/runtime/modelrepo/alibabacloud"
	"github.com/stretchr/testify/require"
)

func alibabaProvider(t *testing.T, server *httptest.Server, caps modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "alibaba-cloud", BaseURL: server.URL, APIKey: "secret"})
	require.NoError(t, err)
	return catalog.ProviderFor(modelrepo.ObservedModel{Name: "qwen3.8-max", CapabilityConfig: caps})
}

func alibabaChatServer(t *testing.T, body *map[string]any, called *bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(body))
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning_content":"thought"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`))
	}))
}

func hybridCaps() modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat: true, CanStream: true, CanThink: true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true, Disabled: true},
	}
}

func thinkingOnlyCaps() modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat: true, CanStream: true, CanThink: true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true},
	}
}

func TestUnit_AlibabaCloudSendsNoThinkingControlByDefault(t *testing.T) {
	var body map[string]any
	called := false
	server := alibabaChatServer(t, &body, &called)
	defer server.Close()
	client, err := alibabaProvider(t, server, hybridCaps()).GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.True(t, called)
	require.NotContains(t, body, "enable_thinking", "the vendor default stays in force until the operator picks a level")
	require.NotContains(t, body, "reasoning_effort")
	require.Equal(t, "qwen3.8-max", body["model"])
}

func TestUnit_AlibabaCloudTurnsThinkingOffWithTheVendorToggle(t *testing.T) {
	var body map[string]any
	called := false
	server := alibabaChatServer(t, &body, &called)
	defer server.Close()
	client, err := alibabaProvider(t, server, hybridCaps()).GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("off"))
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, false, body["enable_thinking"])
}

func TestUnit_AlibabaCloudTurnsThinkingOnForANamedLevel(t *testing.T) {
	for _, level := range []string{"low", "high", "max"} {
		var body map[string]any
		called := false
		server := alibabaChatServer(t, &body, &called)
		client, err := alibabaProvider(t, server, hybridCaps()).GetChatConnection(context.Background(), server.URL)
		require.NoError(t, err)
		_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink(level))
		require.NoError(t, err, level)
		require.True(t, called, level)
		require.Equal(t, true, body["enable_thinking"], level)
		require.NotContains(t, body, "reasoning_effort", level)
		server.Close()
	}
}

func TestUnit_AlibabaCloudRefusesToSwitchOffAThinkingOnlyModel(t *testing.T) {
	var body map[string]any
	called := false
	server := alibabaChatServer(t, &body, &called)
	defer server.Close()
	client, err := alibabaProvider(t, server, thinkingOnlyCaps()).GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("off"))
	var unsupported *modelrepo.UnsupportedDialectLevelError
	require.ErrorAs(t, err, &unsupported)
	require.False(t, called, "a level the model does not publish is refused before the wire")
}

func TestUnit_AlibabaCloudUndeclaredThinkingSendsNoThinkingControl(t *testing.T) {
	for _, level := range []string{"high", "off"} {
		var body map[string]any
		called := false
		server := alibabaChatServer(t, &body, &called)
		client, err := alibabaProvider(t, server, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}).GetChatConnection(context.Background(), server.URL)
		require.NoError(t, err)
		_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink(level))
		require.NoError(t, err, level)
		require.True(t, called, level)
		require.NotContains(t, body, "enable_thinking", level)
		server.Close()
	}
}

func TestUnit_AlibabaCloudRewritesTokenCapAndDropsOpenAIOnlyMembers(t *testing.T) {
	var body map[string]any
	called := false
	server := alibabaChatServer(t, &body, &called)
	defer server.Close()
	client, err := alibabaProvider(t, server, hybridCaps()).GetChatConnection(context.Background(), server.URL)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithMaxTokens(256))
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, float64(256), body["max_tokens"])
	require.NotContains(t, body, "max_completion_tokens")
	require.NotContains(t, body, "prompt_cache_key")
	require.NotContains(t, body, "prompt_cache_retention")
}

func TestUnit_AlibabaCloudStreamsReasoningContent(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/chat/completions", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + frame + "\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	client, err := alibabaProvider(t, server, hybridCaps()).GetStreamConnection(context.Background(), server.URL)
	require.NoError(t, err)
	parcels, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("high"))
	require.NoError(t, err)
	assembly := modelrepo.NewStreamAssembler("alibaba-cloud", "qwen3.8-max")
	for parcel := range parcels {
		require.NoError(t, assembly.Consume(parcel))
	}
	require.Equal(t, true, body["stream"])
	require.Equal(t, true, body["enable_thinking"])
	result, err := assembly.Result()
	require.NoError(t, err)
	require.Equal(t, "answer", result.Content)
	require.Equal(t, "think", result.Thinking)
}
