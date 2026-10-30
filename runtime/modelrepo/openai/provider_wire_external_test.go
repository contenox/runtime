package openai_test

import (
	"context"
	"encoding/json"

	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/openai"
	"github.com/stretchr/testify/require"
)

type captureServer struct {
	*httptest.Server
	requests []capturedRequest
	replies  []scriptedReply
}

type capturedRequest struct {
	Path   string
	Header http.Header
	Body   map[string]any
}

type scriptedReply struct {
	Status int
	Body   string
}

func newCaptureServer(t *testing.T, replies ...scriptedReply) *captureServer {
	t.Helper()
	cs := &captureServer{replies: replies}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		cs.requests = append(cs.requests, capturedRequest{Path: r.URL.Path, Header: r.Header.Clone(), Body: body})
		index := len(cs.requests) - 1
		if index >= len(cs.replies) {
			index = len(cs.replies) - 1
		}
		reply := cs.replies[index]
		w.Header().Set("Content-Type", "application/json")
		if reply.Status != 0 {
			w.WriteHeader(reply.Status)
		}
		_, _ = fmt.Fprint(w, reply.Body)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *captureServer) lastRequest(t *testing.T) capturedRequest {
	t.Helper()
	require.NotEmpty(t, cs.requests, "the provider sent no request")
	return cs.requests[len(cs.requests)-1]
}

func openAIChatProvider(t *testing.T, cs *captureServer, model string, capability modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	capability.CanChat = true
	capability.CanStream = true
	return openai.NewOpenAIProvider("test-key", model, []string{cs.URL}, capability, cs.Client(), libtracker.NoopTracker{})
}

func chatOnce(t *testing.T, provider modelrepo.Provider, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	t.Helper()
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	return client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, args...)
}

func streamParcels(t *testing.T, provider modelrepo.Provider, args ...modelrepo.ChatArgument) (modelrepo.StreamResult, error) {
	t.Helper()
	client, err := provider.GetStreamConnection(context.Background(), "")
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, args...)
	require.NoError(t, err)
	asm := modelrepo.NewStreamAssembler("openai", provider.ModelName())
	for parcel := range stream {
		if consumeErr := asm.Consume(parcel); consumeErr != nil {
			return modelrepo.StreamResult{}, consumeErr
		}
	}
	return asm.Result()
}

func TestUnit_WireOpenAI_ChatRefusalIsErrRefused(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":null,"refusal":"I cannot help with that."}}]}`})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	_, err := chatOnce(t, provider)
	require.ErrorIs(t, err, modelrepo.ErrRefused)
	require.Contains(t, err.Error(), "I cannot help with that.")
}

func TestUnit_WireOpenAI_ResponsesRefusalIsErrRefused(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that."}]}]}`})
	provider := openAIChatProvider(t, cs, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})

	_, err := chatOnce(t, provider)
	require.ErrorIs(t, err, modelrepo.ErrRefused)
	require.Contains(t, err.Error(), "I cannot help with that.")
}

func TestUnit_WireOpenAI_ResponsesStreamRefusalIsErrRefused(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: "data: {\"type\":\"response.refusal.delta\",\"delta\":\"I cannot \"}\n\ndata: {\"type\":\"response.refusal.delta\",\"delta\":\"help with that.\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"})
	provider := openAIChatProvider(t, cs, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})

	_, err := streamParcels(t, provider)
	require.ErrorIs(t, err, modelrepo.ErrRefused)
	require.Contains(t, err.Error(), "I cannot help with that.")
}

func TestUnit_WireOpenAI_ChatSendsStopSequences(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	_, err := chatOnce(t, provider, modelrepo.WithStopSequences("END", "###"))
	require.NoError(t, err)
	require.Equal(t, []any{"END", "###"}, cs.lastRequest(t).Body["stop"])
}

func TestUnit_WireOpenAI_ChatSendsToolChoiceAndParallelCalls(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}}]}`})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	tool := modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "get weather", Parameters: map[string]any{"type": "object"}}}
	_, err := chatOnce(t, provider, modelrepo.WithTool(tool),
		modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "get weather"}),
		modelrepo.WithParallelToolCalls(false),
	)
	require.NoError(t, err)
	body := cs.lastRequest(t).Body
	require.Equal(t, false, body["parallel_tool_calls"])
	require.Equal(t, map[string]any{"type": "function", "function": map[string]any{"name": "get_weather"}}, body["tool_choice"])

	cs2 := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`})
	provider2 := openAIChatProvider(t, cs2, "gpt-4o", modelrepo.CapabilityConfig{})
	_, err = chatOnce(t, provider2, modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceNone}))
	require.NoError(t, err)
	if got, ok := cs2.lastRequest(t).Body["tool_choice"]; ok {
		require.Failf(t, "tool_choice sent without tools", "got %v", got)
	}

	cs3 := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`})
	provider3 := openAIChatProvider(t, cs3, "gpt-4o", modelrepo.CapabilityConfig{})
	_, err = chatOnce(t, provider3, modelrepo.WithTool(tool), modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceNone}))
	require.NoError(t, err)
	require.Equal(t, "none", cs3.lastRequest(t).Body["tool_choice"])
}

func TestUnit_WireOpenAI_ChatSendsResponseFormat(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"{}"}}]}`})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	_, err := chatOnce(t, provider, modelrepo.WithResponseFormat(modelrepo.ResponseFormat{
		Mode:   modelrepo.ResponseFormatJSONSchema,
		Name:   "answer",
		Schema: map[string]any{"type": "object"},
		Strict: true,
	}))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "answer",
			"schema": map[string]any{"type": "object"},
			"strict": true,
		},
	}, cs.lastRequest(t).Body["response_format"])
}

func TestUnit_WireOpenAI_ResponsesSendsTextFormat(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"{}"}]}]}`})
	provider := openAIChatProvider(t, cs, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})

	_, err := chatOnce(t, provider, modelrepo.WithResponseFormat(modelrepo.ResponseFormat{
		Mode:   modelrepo.ResponseFormatJSONSchema,
		Name:   "answer",
		Schema: map[string]any{"type": "object"},
		Strict: true,
	}))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"format": map[string]any{
			"type":   "json_schema",
			"name":   "answer",
			"schema": map[string]any{"type": "object"},
			"strict": true,
		},
	}, cs.lastRequest(t).Body["text"])
}

func TestUnit_WireOpenAI_ResponsesSendsToolChoiceAndParallelCalls(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`})
	provider := openAIChatProvider(t, cs, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})

	tool := modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "get weather", Parameters: map[string]any{"type": "object"}}}
	_, err := chatOnce(t, provider, modelrepo.WithTool(tool),
		modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "get weather"}),
		modelrepo.WithParallelToolCalls(false),
	)
	require.NoError(t, err)
	body := cs.lastRequest(t).Body
	require.Equal(t, "/responses", cs.lastRequest(t).Path)
	require.Equal(t, false, body["parallel_tool_calls"])
	require.Equal(t, map[string]any{"type": "function", "name": "get_weather"}, body["tool_choice"])
}

func TestUnit_WireOpenAI_ResponsesIncompleteIsATerminal(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n"})
	provider := openAIChatProvider(t, cs, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})

	result, err := streamParcels(t, provider)
	require.NoError(t, err)
	require.Equal(t, "partial", result.Content)
	require.Equal(t, "max_output_tokens", result.FinishReason)
	require.NotNil(t, result.Usage)
	require.Equal(t, 15, result.Usage.TotalTokens)
}

func TestUnit_WireOpenAI_ChatStreamTruncationIsReported(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"half\"}}]}\n\n"})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	_, err := streamParcels(t, provider)
	require.ErrorIs(t, err, modelrepo.ErrStreamTruncated)
}

func TestUnit_WireOpenAI_UsageCarriesCacheWrites(t *testing.T) {
	t.Parallel()
	cs := createChatUsageServer(t, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1200,"completion_tokens":9,"total_tokens":1209,"prompt_tokens_details":{"cached_tokens":1024,"cache_write_tokens":64}}}`)
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})
	result, err := chatOnce(t, provider)
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.Equal(t, 1024, result.Usage.CacheReadTokens)
	require.Equal(t, 64, result.Usage.CacheWriteTokens)

	cs2 := createChatUsageServer(t, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1200,"output_tokens":9,"total_tokens":1209,"input_tokens_details":{"cached_tokens":1024,"cache_write_tokens":64}}}`)
	provider2 := openAIChatProvider(t, cs2, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})
	result2, err := chatOnce(t, provider2)
	require.NoError(t, err)
	require.NotNil(t, result2.Usage)
	require.Equal(t, 1024, result2.Usage.CacheReadTokens)
	require.Equal(t, 64, result2.Usage.CacheWriteTokens)
}

func TestUnit_WireOpenAI_StreamUsageCarriesCacheWrites(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":1200,\"completion_tokens\":9,\"total_tokens\":1209,\"prompt_tokens_details\":{\"cached_tokens\":1024,\"cache_write_tokens\":64}}}\n\ndata: [DONE]\n\n"})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	result, err := streamParcels(t, provider)
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.Equal(t, 64, result.Usage.CacheWriteTokens)
}

func TestUnit_WireOpenAI_ResponsesReplaysEncryptedReasoning(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t,
		scriptedReply{Body: `{"status":"completed","output":[{"type":"reasoning","id":"rs_1","encrypted_content":"OPAQUE"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first"}]}]}`},
		scriptedReply{Body: `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second"}]}]}`},
	)
	provider := openAIChatProvider(t, cs, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})

	first, err := chatOnce(t, provider)
	require.NoError(t, err)
	require.Equal(t, []any{"reasoning.encrypted_content"}, cs.lastRequest(t).Body["include"])
	require.NotNil(t, first.Message.Continuation, "the reasoning items must be kept for the next turn")

	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "first", Continuation: first.Message.Continuation},
		{Role: "user", Content: "again"},
	})
	require.NoError(t, err)
	replay := cs.lastRequest(t).Body["input"].([]any)
	require.Equal(t, map[string]any{"type": "reasoning", "id": "rs_1", "encrypted_content": "OPAQUE"}, replay[1])
}

func TestUnit_WireOpenAI_ToolCallingRoutesToResponses(t *testing.T) {
	t.Parallel()
	tool := modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "get_weather", Parameters: map[string]any{"type": "object"}}}

	withTools := newCaptureServer(t, scriptedReply{Body: `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`})
	provider := openAIChatProvider(t, withTools, "gpt-6-astra", modelrepo.CapabilityConfig{CanThink: true})
	_, err := chatOnce(t, provider, modelrepo.WithTool(tool))
	require.NoError(t, err)
	require.Equal(t, "/responses", withTools.lastRequest(t).Path)

	chatOnly := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`})
	provider = openAIChatProvider(t, chatOnly, "o1-mini", modelrepo.CapabilityConfig{CanThink: true})
	_, err = chatOnce(t, provider)
	require.NoError(t, err)
	require.Equal(t, "/chat/completions", chatOnly.lastRequest(t).Path)

	responsesOnly := newCaptureServer(t, scriptedReply{Body: `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`})
	provider = openAIChatProvider(t, responsesOnly, "gpt-5.6-cyber", modelrepo.CapabilityConfig{CanThink: true})
	_, err = chatOnce(t, provider)
	require.NoError(t, err)
	require.Equal(t, "/responses", responsesOnly.lastRequest(t).Path)
}

func TestUnit_WireOpenAI_StreamRequestErrorNamesTheVendorCode(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Status: http.StatusTooManyRequests, Body: `{"error":{"message":"Rate limit reached for gpt-4o","type":"requests","code":"rate_limit_exceeded"}}`})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	client, err := provider.GetStreamConnection(context.Background(), "")
	require.NoError(t, err)
	_, err = client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.Error(t, err)
	require.ErrorIs(t, err, modelrepo.ErrRateLimited)
	require.Contains(t, err.Error(), "Rate limit reached for gpt-4o")
	require.NotContains(t, err.Error(), `"error"`)
}

func TestUnit_WireOpenAI_CatalogDeclaresDocumentedFacts(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-6-astra","object":"model","created":1,"owned_by":"openai"}]}`)
	}))
	defer server.Close()

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "openai", BaseURL: server.URL, APIKey: "k"})
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.True(t, models[0].CanVision, "gpt-6-astra documents image input")
	require.Equal(t, 128000, models[0].MaxOutputTokens)

	provider := catalog.ProviderFor(models[0])
	require.True(t, provider.CanVision(), "the observed vision declaration reaches the provider")

	calls := 0
	clampServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, float64(128000), body["max_output_tokens"], "the documented ceiling must clamp the request")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer clampServer.Close()

	clamped := openai.NewOpenAIProvider("k", "gpt-6-astra", []string{clampServer.URL}, models[0].CapabilityConfig, clampServer.Client(), libtracker.NoopTracker{})
	client, err := clamped.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithMaxTokens(500000))
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestUnit_WireOpenAI_UndocumentedReasoningEffortIsRefused(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`})
	provider := openAIChatProvider(t, cs, "gpt-5.1", modelrepo.CapabilityConfig{CanThink: true})

	_, err := chatOnce(t, provider, modelrepo.WithThink("xhigh"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "xhigh")
	require.Empty(t, cs.requests, "the request must not reach the wire")

	_, err = chatOnce(t, provider, modelrepo.WithThink("max"))
	require.ErrorContains(t, err, "documented")
}

func createChatUsageServer(t *testing.T, body string) *captureServer {
	t.Helper()
	return newCaptureServer(t, scriptedReply{Body: body})
}

func TestUnit_WireOpenAI_CatalogDeclaresDocumentedReasoning(t *testing.T) {
	t.Parallel()
	var responses []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/models" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"gpt-5.5"},{"id":"gpt-4o"}]}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		responses = append(responses, body)
		_, _ = fmt.Fprint(w, `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	t.Cleanup(server.Close)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "openai", BaseURL: server.URL, APIKey: "test-key"},
		modelrepo.WithCatalogHTTPClient(server.Client()))
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 2)

	require.True(t, models[0].CanThink)
	require.NotNil(t, models[0].ThinkingTypes)
	require.True(t, models[0].ThinkingTypes.Disabled)
	require.Equal(t, []string{"low", "medium", "high", "xhigh"}, models[0].ThinkingTypes.EffortLevels)
	require.False(t, models[1].CanThink, "gpt-4o's page documents no reasoning")
	require.Nil(t, models[1].ThinkingTypes)

	_, err = chatOnce(t, catalog.ProviderFor(models[0]), modelrepo.WithThink("low"))
	require.NoError(t, err)
	require.Len(t, responses, 1)
	require.Equal(t, "low", responses[0]["reasoning"].(map[string]any)["effort"])
}

func TestUnit_WireOpenAI_StreamedErrorFrameFailsTheTurn(t *testing.T) {
	t.Parallel()
	cs := newCaptureServer(t, scriptedReply{Body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"par\"}}]}\n\n" +
		"data: {\"error\":{\"message\":\"injected mid-stream error\",\"type\":\"server_error\",\"code\":500}}\n\n" +
		"data: [DONE]\n\n"})
	provider := openAIChatProvider(t, cs, "gpt-4o", modelrepo.CapabilityConfig{})

	result, err := streamParcels(t, provider)
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected mid-stream error")
	require.Empty(t, result.Content)
}
