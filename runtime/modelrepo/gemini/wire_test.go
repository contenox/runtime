package gemini_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/gemini"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capture struct {
	server *httptest.Server
	bodies []map[string]any
}

func newCapture(t *testing.T, responses ...string) *capture {
	t.Helper()

	c := &capture{}
	index := 0
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var body map[string]any
		if len(raw) > 0 {
			require.NoError(t, json.Unmarshal(raw, &body))
		}
		c.bodies = append(c.bodies, body)
		response := `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
		if index < len(responses) {
			response = responses[index]
		}
		index++
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write([]byte(response))
		require.NoError(t, err)
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *capture) body(t *testing.T, index int) map[string]any {
	t.Helper()
	require.Greater(t, len(c.bodies), index, "expected request %d", index)
	return c.bodies[index]
}

func chatProvider(t *testing.T, c *capture, model string, capabilities modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()

	capabilities.CanChat = true
	capabilities.CanPrompt = true
	capabilities.CanStream = true
	if capabilities.ThinkingTypes == nil {
		capabilities.ThinkingTypes = gemini.ThinkingDeclarationFor(model)
	}
	capabilities.CanThink = capabilities.ThinkingTypes != nil
	return gemini.NewGeminiProvider("test-key", model, []string{c.server.URL}, capabilities, c.server.Client(), nil)
}

func TestUnit_GeminiWire_SystemInstructionUsesTheDocumentedName(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	provider := chatProvider(t, c, "gemini-2.5-pro", modelrepo.CapabilityConfig{})
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	_, err = client.Chat(context.Background(), []modelrepo.Message{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hi"},
	})
	require.NoError(t, err)

	body := c.body(t, 0)
	system, ok := body["systemInstruction"].(map[string]any)
	require.True(t, ok, "request carries the documented systemInstruction member: %v", body)
	require.NotContains(t, body, "system_instruction")
	parts := system["parts"].([]any)
	require.Len(t, parts, 1)
	assert.Equal(t, "be terse", parts[0].(map[string]any)["text"])
}

func TestUnit_GeminiWire_StructuredOutputCarriesTheSchema(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	provider := chatProvider(t, c, "gemini-2.5-pro", modelrepo.CapabilityConfig{})
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
		"required":             []any{"name"},
		"additionalProperties": false,
	}
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{
			Mode:   modelrepo.ResponseFormatJSONSchema,
			Name:   "person",
			Schema: schema,
		}))
	require.NoError(t, err)

	config := c.body(t, 0)["generationConfig"].(map[string]any)
	assert.Equal(t, "application/json", config["responseMimeType"])
	assert.Equal(t, schema, config["responseJsonSchema"])

	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONObject}))
	require.NoError(t, err)
	config = c.body(t, 1)["generationConfig"].(map[string]any)
	assert.Equal(t, "application/json", config["responseMimeType"])
	assert.NotContains(t, config, "responseJsonSchema")
}

func TestUnit_GeminiWire_ToolSchemaIsSentWhole(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	provider := chatProvider(t, c, "gemini-2.5-pro", modelrepo.CapabilityConfig{})
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"paths": map[string]any{
				"type":  []any{"string", "array"},
				"items": map[string]any{"type": "string"},
			},
		},
		"required":             []any{"paths"},
		"additionalProperties": false,
	}
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTools(modelrepo.Tool{
			Type: "function",
			Function: &modelrepo.FunctionTool{
				Name:        "git.paths",
				Description: "List paths.",
				Parameters:  schema,
			},
		}))
	require.NoError(t, err)

	tools := c.body(t, 0)["tools"].([]any)
	require.Len(t, tools, 1)
	declarations := tools[0].(map[string]any)["functionDeclarations"].([]any)
	require.Len(t, declarations, 1)
	declaration := declarations[0].(map[string]any)
	assert.Equal(t, "git.paths", declaration["name"])
	assert.Equal(t, schema, declaration["parametersJsonSchema"])
	assert.NotContains(t, declaration, "parameters")
}

func TestUnit_GeminiWire_ThinkingLevelUsesTheVendorEnumAndIncludesThoughts(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	provider := chatProvider(t, c, "gemini-3.5-flash", modelrepo.CapabilityConfig{})
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("low"))
	require.NoError(t, err)

	config := c.body(t, 0)["generationConfig"].(map[string]any)
	thinking := config["thinkingConfig"].(map[string]any)
	assert.Equal(t, "LOW", thinking["thinkingLevel"])
	assert.Equal(t, true, thinking["includeThoughts"])
	assert.NotContains(t, thinking, "thinkingBudget")

	provider = chatProvider(t, c, "gemini-2.5-flash", modelrepo.CapabilityConfig{})
	client, err = provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("high"))
	require.NoError(t, err)

	config = c.body(t, 1)["generationConfig"].(map[string]any)
	thinking = config["thinkingConfig"].(map[string]any)
	assert.Equal(t, float64(24576), thinking["thinkingBudget"])
	assert.NotContains(t, thinking, "thinkingLevel")
}

func TestUnit_GeminiWire_UndocumentedThinkingControlIsRefused(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	provider := chatProvider(t, c, "gemini-unlisted-model", modelrepo.CapabilityConfig{
		ThinkingTypes: &modelrepo.ThinkingTypes{Enabled: true},
	})
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("medium"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publishes no thinking control")
	assert.Empty(t, c.bodies, "no request reaches the vendor")
}

func TestUnit_GeminiWire_ThoughtSignatureReplaysInsideItsOwnPart(t *testing.T) {
	t.Parallel()

	first := `{"candidates":[{"content":{"role":"model","parts":[` +
		`{"text":"let me check"},` +
		`{"functionCall":{"name":"get_weather","args":{"city":"Berlin"}},"thoughtSignature":"sig-1"},` +
		`{"functionCall":{"name":"get_time","args":{"tz":"CET"}}}]},"finishReason":"STOP"}]}`
	c := newCapture(t, first, `{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)

	provider := chatProvider(t, c, "gemini-3-pro", modelrepo.CapabilityConfig{})
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	result, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "weather?"}})
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 2)
	require.Equal(t, "sig-1", result.ToolCalls[0].ProviderMeta["thought_signature"])
	require.Nil(t, result.ToolCalls[1].ProviderMeta, "a parallel call is never given its sibling's signature")
	require.NotNil(t, result.Message.Continuation)

	_, err = client.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "weather?"},
		result.Message,
		{Role: "tool", ToolCallID: result.ToolCalls[0].ID, Content: `{"temp":21}`},
		{Role: "tool", ToolCallID: result.ToolCalls[1].ID, Content: `{"time":"12:00"}`},
	})
	require.NoError(t, err)

	contents := c.body(t, 1)["contents"].([]any)
	model := contents[1].(map[string]any)
	parts := model["parts"].([]any)
	require.Len(t, parts, 3)
	assert.Equal(t, "let me check", parts[0].(map[string]any)["text"])
	assert.Equal(t, "sig-1", parts[1].(map[string]any)["thoughtSignature"])
	assert.NotContains(t, parts[2], "thoughtSignature",
		"the vendor returns a signature on the first call of a parallel group only")
}

func TestUnit_GeminiWire_TextPartSignatureRoundTrips(t *testing.T) {
	t.Parallel()

	first := `{"candidates":[{"content":{"role":"model","parts":[{"text":"answer","thoughtSignature":"sig-text"}]},"finishReason":"STOP"}]}`
	c := newCapture(t, first, `{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)

	provider := chatProvider(t, c, "gemini-3-pro", modelrepo.CapabilityConfig{})
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	result, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	_, err = client.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "hi"},
		result.Message,
		{Role: "user", Content: "continue"},
	})
	require.NoError(t, err)

	parts := c.body(t, 1)["contents"].([]any)[1].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 1)
	assert.Equal(t, "answer", parts[0].(map[string]any)["text"])
	assert.Equal(t, "sig-text", parts[0].(map[string]any)["thoughtSignature"])
}

func TestUnit_GeminiWire_StreamedSignaturePartIsConsumedByItsCall(t *testing.T) {
	t.Parallel()

	frames := []string{
		`{"candidates":[{"content":{"parts":[{"thoughtSignature":"sig-pending"}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_time","args":{"tz":"CET"}}}]},"finishReason":"STOP"}]}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			_, err := w.Write([]byte("data: " + frame + "\n\n"))
			require.NoError(t, err)
		}
	}))
	defer server.Close()

	provider := chatProvider(t, &capture{server: server}, "gemini-3-pro", modelrepo.CapabilityConfig{})
	client, err := provider.GetStreamConnection(context.Background(), "")
	require.NoError(t, err)

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "time?"}})
	require.NoError(t, err)

	assembler := modelrepo.NewStreamAssembler("gemini", "gemini-3-pro")
	for parcel := range stream {
		require.NoError(t, assembler.Consume(parcel))
	}
	result, err := assembler.Result()
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "sig-pending", result.ToolCalls[0].ProviderMeta["thought_signature"])
	require.NotNil(t, result.Continuation)
}

func TestUnit_GeminiCatalog_FollowsPagination(t *testing.T) {
	t.Parallel()

	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1beta/models" {
			_, err := w.Write([]byte(`{"name":"` + r.URL.Path + `","inputTokenLimit":1048576,"outputTokenLimit":65536,"thinking":true,"supportedGenerationMethods":["generateContent"]}`))
			require.NoError(t, err)
			return
		}
		tokens = append(tokens, r.URL.Query().Get("pageToken"))
		switch r.URL.Query().Get("pageToken") {
		case "":
			_, err := w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-pro"}],"nextPageToken":"page-2"}`))
			require.NoError(t, err)
		case "page-2":
			_, err := w.Write([]byte(`{"models":[{"name":"models/gemini-3-flash"}]}`))
			require.NoError(t, err)
		default:
			t.Errorf("unexpected page token")
		}
	}))
	defer server.Close()

	catalog, err := modelrepo.NewCatalogProvider(
		modelrepo.BackendSpec{Type: "gemini", BaseURL: server.URL, APIKey: "test-key"},
		modelrepo.WithCatalogHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"", "page-2"}, tokens)
	require.Len(t, models, 2)
	require.Equal(t, "gemini-2.5-pro", models[0].Name)
	require.Equal(t, "gemini-3-flash", models[1].Name)
}

func TestUnit_GeminiCatalog_ListsTheIDGenerationUses(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		var err error
		switch r.URL.Path {
		case "/v1beta/models":
			_, err = w.Write([]byte(`{"models":[{"name":"models/gemini-2.5-flash"}]}`))
		case "/v1beta/models/gemini-2.5-flash":
			_, err = w.Write([]byte(`{"name":"models/gemini-2.5-flash","baseModelId":"gemini-2.5-flash","inputTokenLimit":1048576,"supportedGenerationMethods":["generateContent"]}`))
		default:
			_, err = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))
		}
		require.NoError(t, err)
	}))
	defer server.Close()

	catalog, err := modelrepo.NewCatalogProvider(
		modelrepo.BackendSpec{Type: "gemini", BaseURL: server.URL, APIKey: "test-key"},
		modelrepo.WithCatalogHTTPClient(server.Client()),
	)
	require.NoError(t, err)

	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	require.Equal(t, "gemini-2.5-flash", models[0].Name)

	provider := catalog.ProviderFor(models[0])
	require.Equal(t, models[0].Name, provider.ModelName())
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.Contains(t, paths, "/v1beta/models/gemini-2.5-flash:generateContent")
}
