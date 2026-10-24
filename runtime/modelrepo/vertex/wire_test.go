package vertex_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/gemini"
	"github.com/contenox/runtime/runtime/modelrepo/vertex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capture struct {
	server  *httptest.Server
	headers []http.Header
	bodies  []map[string]any
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
		c.headers = append(c.headers, r.Header.Clone())
		c.bodies = append(c.bodies, body)
		response := `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
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

func chatClient(t *testing.T, c *capture, model string, capabilities modelrepo.CapabilityConfig) modelrepo.LLMChatClient {
	t.Helper()

	capabilities.CanChat = true
	if capabilities.ThinkingTypes == nil {
		capabilities.ThinkingTypes = gemini.ThinkingDeclarationFor(model)
	}
	capabilities.CanThink = capabilities.ThinkingTypes != nil
	provider := vertex.NewVertexProvider("google", model, []string{c.server.URL + "/v1"}, capabilities, "express-key", c.server.Client(), nil)
	client, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	return client
}

func TestUnit_VertexWire_ExpressModeUsesTheAPIKeyHeader(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	client := chatClient(t, c, "gemini-2.5-pro", modelrepo.CapabilityConfig{})

	_, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Len(t, c.headers, 1)
	assert.Equal(t, "express-key", c.headers[0].Get("X-Goog-Api-Key"))
	assert.Empty(t, c.headers[0].Get("Authorization"))
	assert.Empty(t, c.headers[0].Get("x-goog-user-project"))
}

func TestUnit_VertexWire_QuotaProjectFollowsTheCredentialKind(t *testing.T) {
	t.Parallel()

	const userCredential = `{"type":"authorized_user","client_id":"id","client_secret":"secret","refresh_token":"token"}`
	const serviceAccount = `{"type":"service_account","project_id":"sa-project","private_key_id":"k","private_key":"-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n","client_email":"sa@example.test"}`
	const baseURL = "https://us-central1-aiplatform.googleapis.com/v1/projects/billing-project/locations/us-central1"

	project, err := vertex.QuotaProjectFor(context.Background(), userCredential, baseURL)
	require.NoError(t, err)
	assert.Equal(t, "billing-project", project)

	project, err = vertex.QuotaProjectFor(context.Background(), serviceAccount, baseURL)
	require.NoError(t, err)
	assert.Empty(t, project, "a service account is billed to its own project")

	project, err = vertex.QuotaProjectFor(context.Background(), "", baseURL)
	require.NoError(t, err)
	assert.Empty(t, project, "an ambient credential names its quota project in the ADC setup")

	project, err = vertex.QuotaProjectFor(context.Background(), "express-key", baseURL)
	require.NoError(t, err)
	assert.Empty(t, project, "express mode carries no quota project")

	bearer := func(context.Context) (string, error) { return "token", nil }

	req, err := http.NewRequest(http.MethodPost, baseURL, nil)
	require.NoError(t, err)
	require.NoError(t, vertex.ApplyVertexAuth(context.Background(), req, "", "billing-project", bearer))
	assert.Equal(t, "Bearer token", req.Header.Get("Authorization"))
	assert.Equal(t, "billing-project", req.Header.Get("x-goog-user-project"))

	req, err = http.NewRequest(http.MethodPost, baseURL, nil)
	require.NoError(t, err)
	require.NoError(t, vertex.ApplyVertexAuth(context.Background(), req, "", "", bearer))
	assert.Empty(t, req.Header.Get("x-goog-user-project"))

	req, err = http.NewRequest(http.MethodPost, baseURL, nil)
	require.NoError(t, err)
	require.NoError(t, vertex.ApplyVertexAuth(context.Background(), req, "express-key", "billing-project", bearer))
	assert.Equal(t, "express-key", req.Header.Get("X-Goog-Api-Key"))
	assert.Empty(t, req.Header.Get("Authorization"))
	assert.Empty(t, req.Header.Get("x-goog-user-project"))
}

func TestUnit_VertexWire_SystemInstructionUsesTheDocumentedName(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	client := chatClient(t, c, "gemini-2.5-pro", modelrepo.CapabilityConfig{})

	_, err := client.Chat(context.Background(), []modelrepo.Message{
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

func TestUnit_VertexWire_StructuredOutputAndToolSchemaUseJSONSchema(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	client := chatClient(t, c, "gemini-2.5-pro", modelrepo.CapabilityConfig{})

	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"paths": map[string]any{"type": []any{"string", "array"}, "items": map[string]any{"type": "string"}}},
		"required":             []any{"paths"},
		"additionalProperties": false,
	}
	_, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONSchema, Schema: schema}),
		modelrepo.WithTools(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{
			Name: "git.paths", Description: "List paths.", Parameters: schema,
		}}))
	require.NoError(t, err)

	config := c.body(t, 0)["generationConfig"].(map[string]any)
	assert.Equal(t, "application/json", config["responseMimeType"])
	assert.Equal(t, schema, config["responseJsonSchema"])

	declarations := c.body(t, 0)["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)
	require.Len(t, declarations, 1)
	declaration := declarations[0].(map[string]any)
	assert.Equal(t, schema, declaration["parametersJsonSchema"])
	assert.NotContains(t, declaration, "parameters")
}

func TestUnit_VertexWire_ThinkingControlAndIncludeThoughts(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	client := chatClient(t, c, "gemini-3.5-flash", modelrepo.CapabilityConfig{})
	_, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("medium"))
	require.NoError(t, err)

	thinking := c.body(t, 0)["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
	assert.Equal(t, "MEDIUM", thinking["thinkingLevel"])
	assert.Equal(t, true, thinking["includeThoughts"])
	assert.NotContains(t, thinking, "thinkingBudget")

	client = chatClient(t, c, "gemini-2.5-pro", modelrepo.CapabilityConfig{})
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithThink("xhigh"))
	require.NoError(t, err)
	thinking = c.body(t, 1)["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
	assert.Equal(t, float64(32768), thinking["thinkingBudget"],
		"the deepest documented budget, never the dynamic -1 that thinks less than high")
	assert.NotContains(t, thinking, "thinkingLevel")
}

func TestUnit_VertexWire_ArgonThinkingAndToolCalls(t *testing.T) {
	t.Parallel()
	c := newCapture(t, `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Berlin"}},"thoughtSignature":"argon-signature"}]},"finishReason":"STOP"}]}`)
	client := chatClient(t, c, "gemini-4-argon", modelrepo.CapabilityConfig{})
	result, err := client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "weather?"}},
		modelrepo.WithThink("medium"), modelrepo.WithTools(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{
			Name: "get_weather", Parameters: map[string]any{"type": "object"},
		}}))
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "argon-signature", result.ToolCalls[0].ProviderMeta["thought_signature"])
	thinking := c.body(t, 0)["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
	require.Equal(t, "MEDIUM", thinking["thinkingLevel"])
	require.NotContains(t, thinking, "thinkingBudget")
	require.Contains(t, c.body(t, 0), "tools")
}

func TestUnit_VertexWire_ArgonStreamedToolCall(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/publishers/google/models/gemini-4-argon:streamGenerateContent", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		thinking := body["generationConfig"].(map[string]any)["thinkingConfig"].(map[string]any)
		require.Equal(t, "HIGH", thinking["thinkingLevel"])
		require.NotContains(t, thinking, "thinkingBudget")
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{\"name\":\"get_weather\",\"args\":{\"city\":\"Berlin\"}},\"thoughtSignature\":\"argon-stream-signature\"}]},\"finishReason\":\"STOP\"}]}\n\n")
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	provider := vertex.NewVertexProvider("google", "gemini-4-argon", []string{server.URL + "/v1"},
		modelrepo.CapabilityConfig{CanStream: true, CanChat: true, CanThink: true, ThinkingTypes: gemini.ThinkingDeclarationFor("gemini-4-argon")},
		"express-key", server.Client(), nil)
	client, err := provider.GetStreamConnection(context.Background(), "")
	require.NoError(t, err)
	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "weather?"}}, modelrepo.WithThink("high"))
	require.NoError(t, err)
	assembler := modelrepo.NewStreamAssembler("vertex", "gemini-4-argon")
	for parcel := range stream {
		require.NoError(t, assembler.Consume(parcel))
	}
	result, err := assembler.Result()
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "argon-stream-signature", result.ToolCalls[0].ProviderMeta["thought_signature"])
}

func TestUnit_VertexWire_EmbeddingCarriesTheDocumentedTaskType(t *testing.T) {
	t.Parallel()

	c := newCapture(t,
		`{"predictions":[{"embeddings":{"values":[0.1,0.2]}}]}`,
		`{"predictions":[{"embeddings":{"values":[0.1,0.2]}}]}`,
	)
	provider := vertex.NewVertexProvider("google", "gemini-embedding-001", []string{c.server.URL + "/v1"},
		modelrepo.CapabilityConfig{CanEmbed: true}, "express-key", c.server.Client(), nil)
	client, err := provider.GetEmbedConnection(context.Background(), "")
	require.NoError(t, err)

	_, err = client.Embed(modelrepo.WithEmbeddingPurpose(context.Background(), modelrepo.EmbeddingDocument), "a passage")
	require.NoError(t, err)
	instance := c.body(t, 0)["instances"].([]any)[0].(map[string]any)
	assert.Equal(t, "RETRIEVAL_DOCUMENT", instance["task_type"])

	_, err = client.Embed(modelrepo.WithEmbeddingPurpose(context.Background(), modelrepo.EmbeddingQuery), "a query")
	require.NoError(t, err)
	instance = c.body(t, 1)["instances"].([]any)[0].(map[string]any)
	assert.Equal(t, "RETRIEVAL_QUERY", instance["task_type"])
}

func TestUnit_VertexWire_ThoughtSignatureReplaysInsideItsOwnPart(t *testing.T) {
	t.Parallel()

	first := `{"candidates":[{"content":{"role":"model","parts":[` +
		`{"text":"let me check"},` +
		`{"functionCall":{"name":"get_weather","args":{"city":"Berlin"}},"thoughtSignature":"sig-1"},` +
		`{"functionCall":{"name":"get_time","args":{"tz":"CET"}}}]},"finishReason":"STOP"}]}`
	c := newCapture(t, first, `{"candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)

	client := chatClient(t, c, "gemini-3-pro", modelrepo.CapabilityConfig{})
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

	parts := c.body(t, 1)["contents"].([]any)[1].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 3)
	assert.Equal(t, "let me check", parts[0].(map[string]any)["text"])
	assert.Equal(t, "sig-1", parts[1].(map[string]any)["thoughtSignature"])
	assert.NotContains(t, parts[2], "thoughtSignature",
		"the vendor returns a signature on the first call of a parallel group only")
}

func TestUnit_VertexWire_StreamedSignatureAndThoughtOnlyTurn(t *testing.T) {
	t.Parallel()

	c := newCapture(t)
	provider := vertex.NewVertexProvider("google", "gemini-3-pro", []string{c.server.URL + "/v1"},
		modelrepo.CapabilityConfig{CanStream: true, CanChat: true, CanThink: true, ThinkingTypes: gemini.ThinkingDeclarationFor("gemini-3-pro")},
		"express-key", c.server.Client(), nil)
	client, err := provider.GetStreamConnection(context.Background(), "")
	require.NoError(t, err)

	body := []string{
		`{"candidates":[{"content":{"parts":[{"text":"thinking","thought":true}]}}]}`,
		`{"candidates":[{"content":{"parts":[{"thoughtSignature":"sig-only"}]},"finishReason":"STOP"}]}`,
	}
	response := ""
	for _, chunk := range body {
		response += "data: " + chunk + "\n\n"
	}
	streamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, writeErr := w.Write([]byte(response))
		require.NoError(t, writeErr)
	}))
	defer streamServer.Close()

	provider = vertex.NewVertexProvider("google", "gemini-3-pro", []string{streamServer.URL + "/v1"},
		modelrepo.CapabilityConfig{CanStream: true, CanChat: true, CanThink: true, ThinkingTypes: gemini.ThinkingDeclarationFor("gemini-3-pro")},
		"express-key", streamServer.Client(), nil)
	client, err = provider.GetStreamConnection(context.Background(), "")
	require.NoError(t, err)

	stream, err := client.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	assembler := modelrepo.NewStreamAssembler("vertex", "gemini-3-pro")
	for parcel := range stream {
		require.NoError(t, assembler.Consume(parcel), "a thought/signature-only turn is a legal turn")
	}
	result, err := assembler.Result()
	require.NoError(t, err)
	assert.Equal(t, "thinking", result.Thinking)
	assert.Equal(t, "STOP", result.FinishReason)
	require.NotNil(t, result.Continuation, "the streamed parts are kept for replay")
}
