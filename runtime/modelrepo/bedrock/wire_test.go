package bedrock_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream/eventstreamapi"
	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/bedrock"
)

const (
	claudeSonnet45     = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	claudeOpus46       = "us.anthropic.claude-opus-4-6-v1"
	mistralLarge2402   = "mistral.mistral-large-2402-v1:0"
	mistralLarge2407   = "mistral.mistral-large-2407-v1:0"
	staticCredentials  = `{"access_key_id":"key","secret_access_key":"secret"}`
	eventStreamContent = "application/vnd.amazon.eventstream"
)

type wireTransport struct {
	mu       sync.Mutex
	host     string
	path     string
	body     []byte
	requests int
	handler  func(request *http.Request, body []byte) *http.Response
}

func (w *wireTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var body []byte
	if request.Body != nil {
		body, _ = io.ReadAll(request.Body)
	}
	w.mu.Lock()
	w.host = request.URL.Host
	w.path = request.URL.Path
	w.body = body
	w.requests++
	handler := w.handler
	w.mu.Unlock()
	if handler == nil {
		return jsonResponse(request, http.StatusOK, `{}`), nil
	}
	return handler(request, body), nil
}

func (w *wireTransport) request() (string, map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var decoded map[string]any
	if err := json.Unmarshal(w.body, &decoded); err != nil {
		return w.path, nil
	}
	return w.path, decoded
}

func (w *wireTransport) rawBody() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.body...)
}

func jsonResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func errorResponse(request *http.Request, status int, errorType, message string) *http.Response {
	response := jsonResponse(request, status, fmt.Sprintf(`{"message":%q}`, message))
	response.Header.Set("x-amzn-errortype", errorType)
	return response
}

func replyJSON(body string) func(*http.Request, []byte) *http.Response {
	return func(request *http.Request, _ []byte) *http.Response {
		return jsonResponse(request, http.StatusOK, body)
	}
}

func newWireProvider(model string, capabilities modelrepo.CapabilityConfig, transport *wireTransport) modelrepo.Provider {
	return bedrock.NewBedrockProvider("us-east-1", staticCredentials, model, capabilities, &http.Client{Transport: transport}, nil)
}

func converseResponse(content map[string]any, stopReason string, usage string) string {
	message := map[string]any{"role": "assistant", "content": []any{content}}
	payload := map[string]any{"output": map[string]any{"message": message}}
	if stopReason != "" {
		payload["stopReason"] = stopReason
	}
	if usage != "" {
		payload["usage"] = json.RawMessage(usage)
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func textBlock(text string) map[string]any { return map[string]any{"text": text} }

func chatOnce(t *testing.T, provider modelrepo.Provider, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	t.Helper()
	connection, err := provider.GetChatConnection(context.Background(), "bedrock:us-east-1")
	require.NoError(t, err)
	return connection.Chat(context.Background(), messages, args...)
}

func eventFrame(eventType string, payload any) []byte {
	return encodeFrame(eventstreamapi.EventMessageType, eventstreamapi.EventTypeHeader, eventType, payload)
}

func exceptionFrame(exceptionType string, payload any) []byte {
	return encodeFrame(eventstreamapi.ExceptionMessageType, eventstreamapi.ExceptionTypeHeader, exceptionType, payload)
}

func encodeFrame(messageType, typeHeader, typeValue string, payload any) []byte {
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	message := eventstream.Message{
		Headers: eventstream.Headers{
			{Name: eventstreamapi.MessageTypeHeader, Value: eventstream.StringValue(messageType)},
			{Name: typeHeader, Value: eventstream.StringValue(typeValue)},
			{Name: eventstreamapi.ContentTypeHeader, Value: eventstream.StringValue("application/json")},
		},
		Payload: encoded,
	}
	var buffer bytes.Buffer
	if err := eventstream.NewEncoder().Encode(&buffer, message); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func streamResponse(frames ...[]byte) func(*http.Request, []byte) *http.Response {
	body := bytes.Join(frames, nil)
	return func(request *http.Request, _ []byte) *http.Response {
		response := jsonResponse(request, http.StatusOK, string(body))
		response.Header.Set("Content-Type", eventStreamContent)
		return response
	}
}

func drainStream(t *testing.T, provider modelrepo.Provider, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.StreamResult, error) {
	t.Helper()
	connection, err := provider.GetStreamConnection(context.Background(), "bedrock:us-east-1")
	require.NoError(t, err)
	parcels, err := connection.Stream(context.Background(), messages, args...)
	require.NoError(t, err)
	assembler := modelrepo.NewStreamAssembler("bedrock", "test-model")
	var consumeErr error
	for parcel := range parcels {
		if err := assembler.Consume(parcel); err != nil && consumeErr == nil {
			consumeErr = err
		}
	}
	result, err := assembler.Result()
	if consumeErr != nil {
		require.Error(t, err, "an error parcel must make the assembled turn an error")
	}
	return result, err
}

func thinkArg(level string) modelrepo.ChatArgument { return modelrepo.WithThink(level) }

func declared(efforts ...string) modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat:       true,
		CanStream:     true,
		CanPrompt:     true,
		CanThink:      true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Adaptive: true, Disabled: true, EffortLevels: efforts},
	}
}

func budgeted() modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat:         true,
		CanStream:       true,
		CanPrompt:       true,
		CanThink:        true,
		MaxOutputTokens: 8192,
		ThinkingTypes:   &modelrepo.ThinkingTypes{Enabled: true},
	}
}

func textOut(content string, stopReason string) string {
	return converseResponse(textBlock(content), stopReason, "")
}

func additionalFields(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	fields, ok := body["additionalModelRequestFields"].(map[string]any)
	require.True(t, ok, "request must carry additionalModelRequestFields: %v", body)
	return fields
}

func inferenceConfig(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	config, ok := body["inferenceConfig"].(map[string]any)
	require.True(t, ok, "request must carry inferenceConfig: %v", body)
	return config
}

func toolSpecs(t *testing.T, body map[string]any) []any {
	t.Helper()
	config, ok := body["toolConfig"].(map[string]any)
	require.True(t, ok, "request must carry toolConfig: %v", body)
	tools, ok := config["tools"].([]any)
	require.True(t, ok, "toolConfig must carry a tool list: %v", config)
	return tools
}

func withTemperature(value float64) modelrepo.ChatArgument {
	return modelrepo.WithTemperature(value)
}

func withTopP(value float64) modelrepo.ChatArgument {
	return modelrepo.WithTopP(value)
}

func toolSpec(name, description string, parameters any) modelrepo.Tool {
	return modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: name, Description: description, Parameters: parameters}}
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}
}

func cacheContract(fields []string, ttls []string) modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat:     true,
		CanStream:   true,
		PromptCache: &modelrepo.PromptCacheContract{Fields: fields, TTLs: ttls},
	}
}

func TestUnit_BedrockConverseRequest_RolesSystemToolsAndInference(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("on it", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true, CanStream: true}, transport)
	result, err := chatOnce(t, provider,
		[]modelrepo.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "list /tmp"},
			{Role: "assistant", ToolCalls: []modelrepo.ToolCall{tc("t1", "fs.list", `{"path":"/tmp"}`)}},
			{Role: "tool", ToolCallID: "t1", Content: `{"files":["a"]}`},
		},
		modelrepo.WithMaxTokens(256),
		modelrepo.WithTools(toolSpec("fs.list", "list files", objectSchema())),
	)
	require.NoError(t, err)
	require.Equal(t, "on it", result.Message.Content)
	require.Equal(t, "end_turn", result.FinishReason)

	path, body := transport.request()
	require.Equal(t, "/model/"+claudeSonnet45+"/converse", path)
	system := body["system"].([]any)
	require.Len(t, system, 1)
	require.Equal(t, "be terse", system[0].(map[string]any)["text"])
	config := inferenceConfig(t, body)
	require.Equal(t, float64(256), config["maxTokens"])

	specs := toolSpecs(t, body)
	require.Len(t, specs, 1)
	require.Equal(t, "fs_list", specs[0].(map[string]any)["toolSpec"].(map[string]any)["name"])

	messages := body["messages"].([]any)
	require.Len(t, messages, 3)
	require.Equal(t, "user", messages[0].(map[string]any)["role"])
	require.Equal(t, "assistant", messages[1].(map[string]any)["role"])
	toolUse := messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)["toolUse"].(map[string]any)
	require.Equal(t, "t1", toolUse["toolUseId"])
	require.Equal(t, "fs_list", toolUse["name"])
	require.Equal(t, "/tmp", toolUse["input"].(map[string]any)["path"])
	require.Equal(t, "user", messages[2].(map[string]any)["role"])
	toolResult := messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)["toolResult"].(map[string]any)
	require.Equal(t, "t1", toolResult["toolUseId"])
	require.Equal(t, `{"files":["a"]}`, toolResult["content"].([]any)[0].(map[string]any)["text"])
}

func TestUnit_BedrockConverseResponse_DecodesTextToolUseAndReverseMapsNames(t *testing.T) {
	response := converseResponse(map[string]any{"toolUse": map[string]any{
		"toolUseId": "t9",
		"name":      "fs_list",
		"input":     map[string]any{"path": "/x"},
	}}, "tool_use", "")
	transport := &wireTransport{handler: replyJSON(response)}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	result, err := chatOnce(t, provider,
		[]modelrepo.Message{{Role: "user", Content: "list /x"}},
		modelrepo.WithTools(toolSpec("fs.list", "list files", objectSchema())),
	)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "t9", result.ToolCalls[0].ID)
	require.Equal(t, "function", result.ToolCalls[0].Type)
	require.Equal(t, "fs.list", result.ToolCalls[0].Function.Name)
	var arguments map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ToolCalls[0].Function.Arguments), &arguments))
	require.Equal(t, "/x", arguments["path"])
	require.Equal(t, "tool_use", result.FinishReason)
}

func TestUnit_BedrockConverseRequest_ImageBlocksAndUnknownMime(t *testing.T) {
	cases := []struct {
		mime string
		want string
	}{
		{"image/png", "png"},
		{"image/jpeg", "jpeg"},
		{"image/jpg", "jpeg"},
		{"image/gif", "gif"},
		{"image/webp", "webp"},
	}
	for _, c := range cases {
		t.Run(c.mime, func(t *testing.T) {
			transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
			provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true, CanVision: true}, transport)
			_, err := chatOnce(t, provider, []modelrepo.Message{{
				Role:    "user",
				Content: "describe this",
				Images:  []modelrepo.ImagePart{{Data: []byte{1, 2, 3}, MimeType: c.mime}},
			}})
			require.NoError(t, err)
			_, body := transport.request()
			content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
			require.Len(t, content, 2)
			require.Equal(t, "describe this", content[0].(map[string]any)["text"])
			image := content[1].(map[string]any)["image"].(map[string]any)
			require.Equal(t, c.want, image["format"])
			require.Equal(t, "AQID", image["source"].(map[string]any)["bytes"])
		})
	}

	t.Run("unknown mime is skipped rather than sent as an invalid format", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{
			Role:    "user",
			Content: "hi",
			Images:  []modelrepo.ImagePart{{Data: []byte{1}, MimeType: "image/tiff"}},
		}})
		require.NoError(t, err)
		_, body := transport.request()
		content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
		require.Len(t, content, 1)
		require.Equal(t, "hi", content[0].(map[string]any)["text"])
	})

	t.Run("image-only message with an unknown mime produces no message", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{
			Role:   "user",
			Images: []modelrepo.ImagePart{{Data: []byte{1}, MimeType: "application/pdf"}},
		}})
		require.NoError(t, err)
		_, body := transport.request()
		require.Empty(t, body["messages"])
	})
}

func TestUnit_BedrockConverseRequest_ClampsMaxTokensToDeclaredCeiling(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true, MaxOutputTokens: 4096}, transport)
	_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithMaxTokens(9000))
	require.NoError(t, err)
	_, body := transport.request()
	require.Equal(t, float64(4096), inferenceConfig(t, body)["maxTokens"])
}

func TestUnit_BedrockConverseRequest_ToolHistoryDegradesWithoutTools(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider("deepseek.v3.2", modelrepo.CapabilityConfig{CanChat: true}, transport)
	_, err := chatOnce(t, provider, []modelrepo.Message{
		{Role: "user", Content: "list /tmp"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{tc("t1", "fs.list", `{"path":"/tmp"}`)}},
		{Role: "tool", ToolCallID: "t1", Content: `{"files":["a"]}`},
		{Role: "user", Content: "summarise what happened"},
	})
	require.NoError(t, err)
	_, body := transport.request()
	require.NotContains(t, body, "toolConfig")
	messages := body["messages"].([]any)
	require.Len(t, messages, 3)
	for _, message := range messages {
		for _, block := range message.(map[string]any)["content"].([]any) {
			require.NotContains(t, block, "toolUse")
			require.NotContains(t, block, "toolResult")
		}
	}
	require.Contains(t, messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"], "fs.list")
	require.Contains(t, messages[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"], `{"path":"/tmp"}`)
	require.Contains(t, messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)["text"], "t1")
	require.Contains(t, messages[2].(map[string]any)["content"].([]any)[0].(map[string]any)["text"], `{"files":["a"]}`)
}

func tc(id, name, args string) modelrepo.ToolCall {
	call := modelrepo.ToolCall{ID: id, Type: "function"}
	call.Function.Name = name
	call.Function.Arguments = args
	return call
}

func TestUnit_BedrockConverseRequest_ToolNamesAreInjective(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	long := strings.Repeat("a", 100)
	collidesWithLong := strings.Repeat("a", 100) + "different"
	_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTools(
			toolSpec("fs.list", "dot", objectSchema()),
			toolSpec("fs_list", "underscore", objectSchema()),
			toolSpec(long, "long", objectSchema()),
			toolSpec(collidesWithLong, "long variant", objectSchema()),
		),
	)
	require.NoError(t, err)
	_, body := transport.request()
	specs := toolSpecs(t, body)
	require.Len(t, specs, 4)
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.(map[string]any)["toolSpec"].(map[string]any)["name"].(string))
	}
	require.Equal(t, "fs_list", names[0])
	require.NotEqual(t, names[0], names[1], "sanitised names must not collide")
	require.Len(t, names[2], 64)
	require.Len(t, names[3], 64)
	require.NotEqual(t, names[2], names[3], "names truncated to the vendor's maximum must stay distinct")
	seen := map[string]bool{}
	for _, name := range names {
		require.LessOrEqual(t, len(name), 64)
		require.Regexp(t, `^[a-zA-Z0-9_-]+$`, name)
		require.False(t, seen[name], "wire tool names must be unique")
		seen[name] = true
	}
}

func TestUnit_BedrockConverseRequest_ToolWithoutDescriptionOmitsDescription(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTools(
			toolSpec("fs.list", "list files", objectSchema()),
			toolSpec("fs.read", "", objectSchema()),
			toolSpec("fs.write", "   ", objectSchema()),
		),
	)
	require.NoError(t, err)
	_, body := transport.request()
	specs := toolSpecs(t, body)
	require.Equal(t, "list files", specs[0].(map[string]any)["toolSpec"].(map[string]any)["description"])
	require.NotContains(t, specs[1].(map[string]any)["toolSpec"], "description")
	require.NotContains(t, specs[2].(map[string]any)["toolSpec"], "description")
}

func TestUnit_BedrockConverseRequest_ToolChoiceUnion(t *testing.T) {
	history := []modelrepo.Message{
		{Role: "user", Content: "list /tmp"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{tc("t1", "fs.list", `{"path":"/tmp"}`)}},
		{Role: "tool", ToolCallID: "t1", Content: `{"files":["a"]}`},
	}
	cases := []struct {
		name       string
		choice     modelrepo.ToolChoice
		wantChoice map[string]any
		wantTools  int
	}{
		{"auto", modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceAuto}, map[string]any{"auto": map[string]any{}}, 1},
		{"any", modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}, map[string]any{"any": map[string]any{}}, 1},
		{"named tool", modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "fs.list"}, map[string]any{"tool": map[string]any{"name": "fs_list"}}, 1},
		{"none", modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceNone}, nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
			provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
			_, err := chatOnce(t, provider, history,
				modelrepo.WithTools(toolSpec("fs.list", "list files", objectSchema())),
				modelrepo.WithToolChoice(c.choice),
			)
			require.NoError(t, err)
			_, body := transport.request()
			if c.wantChoice == nil {
				require.NotContains(t, body, "toolConfig")
				for _, message := range body["messages"].([]any) {
					for _, block := range message.(map[string]any)["content"].([]any) {
						require.NotContains(t, block, "toolUse")
						require.NotContains(t, block, "toolResult")
					}
				}
				return
			}
			tools := toolSpecs(t, body)
			require.Len(t, tools, c.wantTools)
			config := body["toolConfig"].(map[string]any)
			require.Equal(t, c.wantChoice, config["toolChoice"])
		})
	}
}

func TestUnit_BedrockConverseRequest_RefusesToolControlsTheVendorDoesNotDefine(t *testing.T) {
	cases := []struct {
		name    string
		args    []modelrepo.ChatArgument
		contain string
	}{
		{
			name: "forced tool that the request does not declare",
			args: []modelrepo.ChatArgument{
				modelrepo.WithTools(toolSpec("fs.list", "list", objectSchema())),
				modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired, Tool: "fs.read"}),
			},
			contain: "declares no such tool",
		},
		{
			name: "parallel tool calls disabled",
			args: []modelrepo.ChatArgument{
				modelrepo.WithTools(toolSpec("fs.list", "list", objectSchema())),
				modelrepo.WithParallelToolCalls(false),
			},
			contain: "cannot disable parallel tool calls",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
			provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
			_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, c.args...)
			require.ErrorContains(t, err, c.contain)
			require.Zero(t, transport.requests, "a refused request must not reach the vendor")
		})
	}
}

func TestUnit_BedrockConverseRequest_StopSequences(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("partial", "stop_sequence"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithStopSequences("SUCCESS", "FAILURE"))
	require.NoError(t, err)
	require.Equal(t, "stop_sequence", result.FinishReason)
	_, body := transport.request()
	config := inferenceConfig(t, body)
	require.Equal(t, []any{"SUCCESS", "FAILURE"}, config["stopSequences"])
	require.Equal(t, []any{"/stop_sequence"}, body["additionalModelResponseFieldPaths"])
}

func TestUnit_BedrockConverseRequest_ResponseFormat(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}}
	transport := &wireTransport{handler: replyJSON(textOut(`{"answer":"yes"}`, "end_turn"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONSchema, Name: "answer", Schema: schema}))
	require.NoError(t, err)
	require.Equal(t, `{"answer":"yes"}`, result.Message.Content)
	_, body := transport.request()
	textFormat := body["outputConfig"].(map[string]any)["textFormat"].(map[string]any)
	require.Equal(t, "json_schema", textFormat["type"])
	definition := textFormat["structure"].(map[string]any)["jsonSchema"].(map[string]any)
	require.Equal(t, "answer", definition["name"])
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(definition["schema"].(string)), &decoded))
	require.Equal(t, "object", decoded["type"])

	t.Run("a shape the vendor does not define is refused", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONObject}))
		require.ErrorContains(t, err, `cannot be constrained to "json_object"`)
		require.Zero(t, transport.requests)
	})

	t.Run("unconstrained text leaves the request untouched", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatText}))
		require.NoError(t, err)
		_, body := transport.request()
		require.NotContains(t, body, "outputConfig")
	})
}

func TestUnit_BedrockConverseRequest_RefusesContextOverflowControls(t *testing.T) {
	for _, c := range []struct {
		name string
		arg  modelrepo.ChatArgument
	}{
		{"shift", modelrepo.WithShift{}},
		{"truncate", withTruncate()},
	} {
		t.Run(c.name, func(t *testing.T) {
			transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
			provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
			_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, c.arg)
			require.ErrorContains(t, err, "Converse API defines no overflow control")
			require.Zero(t, transport.requests)
		})
	}
}

type truncateArgument struct{}

func (truncateArgument) Apply(config *modelrepo.ChatConfig) {
	enabled := true
	config.Truncate = &enabled
}

func withTruncate() modelrepo.ChatArgument { return truncateArgument{} }

func adaptiveOnlyCapabilities(efforts ...string) modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat:       true,
		CanStream:     true,
		CanThink:      true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Adaptive: true, Disabled: true, EffortLevels: efforts},
	}
}

func adaptiveBudgetCapabilities(efforts ...string) modelrepo.CapabilityConfig {
	return modelrepo.CapabilityConfig{
		CanChat:       true,
		CanStream:     true,
		CanThink:      true,
		ThinkingTypes: &modelrepo.ThinkingTypes{Adaptive: true, Enabled: true, Disabled: true, EffortLevels: efforts},
	}
}

func adaptiveCapabilities(efforts ...string) modelrepo.CapabilityConfig {
	return adaptiveOnlyCapabilities(efforts...)
}

func TestUnit_BedrockConverseRequest_AdaptiveThinkingEffort(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeOpus46, adaptiveCapabilities("low", "medium", "high", "xhigh", "max"), transport)
	_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("high"))
	require.NoError(t, err)
	fields := additionalFields(t, transport.mustBody(t))
	require.Equal(t, map[string]any{"type": "adaptive"}, fields["thinking"])
	require.Equal(t, map[string]any{"effort": "high"}, fields["output_config"])

	cases := map[string]string{"minimal": "low", "low": "low", "medium": "medium", "xhigh": "xhigh", "max": "max"}
	for level, effort := range cases {
		t.Run(level, func(t *testing.T) {
			transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
			provider := newWireProvider(claudeOpus46, adaptiveCapabilities("low", "medium", "high", "xhigh", "max"), transport)
			_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg(level))
			require.NoError(t, err)
			fields := additionalFields(t, transport.mustBody(t))
			require.Equal(t, map[string]any{"effort": effort}, fields["output_config"])
		})
	}
}

func TestUnit_BedrockConverseRequest_ThinkingDisabledAndUnsupportedLevels(t *testing.T) {
	t.Run("off is sent as the vendor's disabled control", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeOpus46, adaptiveCapabilities("high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("off"))
		require.NoError(t, err)
		fields := additionalFields(t, transport.mustBody(t))
		require.Equal(t, map[string]any{"type": "disabled"}, fields["thinking"])
	})

	t.Run("a model that does not declare disabled refuses rather than sending it", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		capabilities := adaptiveCapabilities("high")
		capabilities.ThinkingTypes.Disabled = false
		provider := newWireProvider("us.anthropic.claude-fable-5-v1:0", capabilities, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("off"))
		require.ErrorContains(t, err, "do not include a disabled control")
		require.Zero(t, transport.requests)
	})

	t.Run("a level the declaration does not name is refused", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeOpus46, adaptiveCapabilities("low", "medium", "high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("xhigh"))
		require.ErrorContains(t, err, "does not declare the xhigh effort level")
		require.Zero(t, transport.requests)
	})

	t.Run("a model with no declared controls refuses a level instead of guessing one", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("meta.llama3-70b-instruct-v1:0", modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("high"))
		require.ErrorContains(t, err, "has no declared thinking controls")
		require.Zero(t, transport.requests)
	})

	t.Run("no declared control leaves the request untouched", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("meta.llama3-70b-instruct-v1:0", modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
		require.NoError(t, err)
		require.NotContains(t, transport.mustBody(t), "additionalModelRequestFields")
	})
}

func TestUnit_BedrockConverseRequest_ThinkingBudgetSitsUnderTheOutputCeiling(t *testing.T) {
	t.Run("the model's declared ceiling carries the budget", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, budgeted(), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("high"))
		require.NoError(t, err)
		body := transport.mustBody(t)
		require.Equal(t, map[string]any{"type": "enabled", "budget_tokens": float64(4096)}, additionalFields(t, body)["thinking"])
		require.Equal(t, float64(8192), inferenceConfig(t, body)["maxTokens"])
	})

	t.Run("a smaller caller ceiling clamps the budget instead of raising the ceiling", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, budgeted(), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			thinkArg("high"), modelrepo.WithMaxTokens(2048))
		require.NoError(t, err)
		body := transport.mustBody(t)
		require.Equal(t, map[string]any{"type": "enabled", "budget_tokens": float64(2047)}, additionalFields(t, body)["thinking"])
		require.Equal(t, float64(2048), inferenceConfig(t, body)["maxTokens"])
	})

	t.Run("no ceiling anywhere refuses with the declaration that is missing", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		undeclared := budgeted()
		undeclared.MaxOutputTokens = 0
		provider := newWireProvider(claudeSonnet45, undeclared, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, thinkArg("high"))
		require.ErrorContains(t, err, "needs an output ceiling")
		require.Zero(t, transport.requests)
	})

	t.Run("a ceiling below the smallest budget refuses", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, budgeted(), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			thinkArg("high"), modelrepo.WithMaxTokens(512))
		require.ErrorContains(t, err, "cannot carry a thinking budget")
		require.Zero(t, transport.requests)
	})
}

func TestUnit_BedrockConverseRequest_ForcedToolUseOnlyRefusedWithManualThinking(t *testing.T) {
	t.Run("manual budget refuses", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, budgeted(), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			thinkArg("high"),
			modelrepo.WithTools(toolSpec("fs.list", "list", objectSchema())),
			modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}),
		)
		require.ErrorContains(t, err, "cannot force tool use with a caller-set thinking budget")
		require.Zero(t, transport.requests)
	})

	t.Run("adaptive thinking sends both", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeOpus46, adaptiveCapabilities("high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			thinkArg("high"),
			modelrepo.WithTools(toolSpec("fs.list", "list", objectSchema())),
			modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}),
		)
		require.NoError(t, err)
		body := transport.mustBody(t)
		require.Equal(t, map[string]any{"any": map[string]any{}}, body["toolConfig"].(map[string]any)["toolChoice"])
		require.Equal(t, map[string]any{"type": "adaptive"}, additionalFields(t, body)["thinking"])
	})
}

func TestUnit_BedrockConverseRequest_SamplingParametersFollowTheThinkingMode(t *testing.T) {
	t.Run("thinking drops temperature and keeps top_p in its band", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeOpus46, adaptiveBudgetCapabilities("high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			thinkArg("high"), withTemperature(0.7), withTopP(0.98))
		require.NoError(t, err)
		config := inferenceConfig(t, transport.mustBody(t))
		require.NotContains(t, config, "temperature")
		require.Equal(t, float64(0.98), config["topP"])
	})

	t.Run("thinking drops a top_p outside the documented band", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeOpus46, adaptiveBudgetCapabilities("high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			thinkArg("high"), withTemperature(0.7), withTopP(0.5))
		require.NoError(t, err)
		require.NotContains(t, transport.mustBody(t), "inferenceConfig")
	})

	t.Run("a model that accepts only adaptive thinking rejects sampling on every request", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("us.anthropic.claude-sonnet-5-v1:0", adaptiveOnlyCapabilities("high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			withTemperature(0.7), withTopP(0.9))
		require.NoError(t, err)
		require.NotContains(t, transport.mustBody(t), "inferenceConfig")
	})

	t.Run("a request that does not reason carries both", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("meta.llama3-70b-instruct-v1:0", modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
			withTemperature(0.7), withTopP(0.9))
		require.NoError(t, err)
		config := inferenceConfig(t, transport.mustBody(t))
		require.Equal(t, float64(0.7), config["temperature"])
		require.Equal(t, float64(0.9), config["topP"])
	})

	t.Run("temperature is bounded by the vendor's documented range", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("meta.llama3-70b-instruct-v1:0", modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}}, withTemperature(1.5))
		require.ErrorContains(t, err, "accepts a temperature between 0 and 1")
		require.Zero(t, transport.requests)
	})
}

func (w *wireTransport) mustBody(t *testing.T) map[string]any {
	t.Helper()
	_, body := w.request()
	require.NotNil(t, body, "request body must be JSON: %s", string(w.rawBody()))
	return body
}

func TestUnit_BedrockConverseResponse_ReasoningSignatureRoundTrips(t *testing.T) {
	response := converseResponse(map[string]any{}, "end_turn", "")
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(response), &payload))
	message := payload["output"].(map[string]any)["message"].(map[string]any)
	message["content"] = []any{
		map[string]any{"reasoningContent": map[string]any{"reasoningText": map[string]any{"text": "thinking it through", "signature": "sig-1"}}},
		map[string]any{"reasoningContent": map[string]any{"redactedContent": "cmVkYWN0ZWQ="}},
		textBlock("the answer"),
	}
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)

	transport := &wireTransport{handler: replyJSON(string(encoded))}
	provider := newWireProvider(claudeSonnet45, adaptiveOnlyCapabilities("high"), transport)
	result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "why"}})
	require.NoError(t, err)
	require.Equal(t, "the answer", result.Message.Content)
	require.Equal(t, "thinking it through", result.Message.Thinking)
	require.NotNil(t, result.Message.Continuation, "the signed reasoning must round-trip on the next turn")
	require.Equal(t, "bedrock", result.Message.Continuation.Provider)
	require.Equal(t, claudeSonnet45, result.Message.Continuation.Model)

	transport = &wireTransport{handler: replyJSON(textOut("done", "end_turn"))}
	provider = newWireProvider(claudeSonnet45, adaptiveOnlyCapabilities("high"), transport)
	_, err = chatOnce(t, provider, []modelrepo.Message{
		{Role: "user", Content: "why"},
		{Role: "assistant", Content: "the answer", Thinking: "thinking it through", Continuation: result.Message.Continuation},
		{Role: "user", Content: "go on"},
	})
	require.NoError(t, err)
	_, body := transport.request()
	assistant := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
	require.Len(t, assistant, 3)
	reasoning := assistant[0].(map[string]any)["reasoningContent"].(map[string]any)["reasoningText"].(map[string]any)
	require.Equal(t, "thinking it through", reasoning["text"])
	require.Equal(t, "sig-1", reasoning["signature"])
	require.Equal(t, "cmVkYWN0ZWQ=", assistant[1].(map[string]any)["reasoningContent"].(map[string]any)["redactedContent"])
	require.Equal(t, "the answer", assistant[2].(map[string]any)["text"])
}

func TestUnit_BedrockConverseResponse_ReasoningIsReplayedOnlyToItsOwnModel(t *testing.T) {
	continuation := &modelrepo.Continuation{
		Provider: "bedrock",
		Model:    claudeSonnet45,
		Items:    json.RawMessage(`{"reasoning":[{"text":"why","signature":"sig"}]}`),
	}

	t.Run("another model refuses the replay", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("us.anthropic.claude-opus-4-6-v1", adaptiveOnlyCapabilities("high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{
			{Role: "user", Content: "why"},
			{Role: "assistant", Content: "answer", Continuation: continuation},
		})
		require.ErrorContains(t, err, "reasoning for model")
		require.Zero(t, transport.requests)
	})

	t.Run("an inference profile replays to its base model", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider("anthropic.claude-sonnet-4-5-20250929-v1:0", adaptiveOnlyCapabilities("high"), transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{
			{Role: "user", Content: "why"},
			{Role: "assistant", Content: "answer", Continuation: continuation},
			{Role: "user", Content: "go on"},
		})
		require.NoError(t, err)
		_, body := transport.request()
		assistant := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
		require.Contains(t, assistant[0], "reasoningContent")
	})

	t.Run("another provider's opaque items are not replayed", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, adaptiveOnlyCapabilities("high"), transport)
		foreign := &modelrepo.Continuation{Provider: "gemini", Model: claudeSonnet45, Items: json.RawMessage(`{"thoughtSignature":"x"}`)}
		_, err := chatOnce(t, provider, []modelrepo.Message{
			{Role: "user", Content: "why"},
			{Role: "assistant", Content: "answer", Continuation: foreign},
			{Role: "user", Content: "go on"},
		})
		require.NoError(t, err)
		_, body := transport.request()
		assistant := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
		require.Len(t, assistant, 1, "only this adapter's own reasoning blocks are replayed")
	})
}

func TestUnit_BedrockConverseResponse_StopReasonHandling(t *testing.T) {
	cases := []struct {
		name     string
		reason   string
		content  map[string]any
		refused  string
		overflow bool
		contains string
		finish   string
	}{
		{name: "end_turn", reason: "end_turn", content: textBlock("answer"), finish: "end_turn"},
		{name: "max_tokens", reason: "max_tokens", content: textBlock("half an ans"), finish: "max_tokens"},
		{name: "stop_sequence", reason: "stop_sequence", content: textBlock("answer"), finish: "stop_sequence"},
		{name: "guardrail_intervened", reason: "guardrail_intervened", content: textBlock("blocked by guardrail"), refused: "guardrail_intervened"},
		{name: "content_filtered", reason: "content_filtered", content: textBlock("filtered"), refused: "content_filtered"},
		{name: "model_context_window_exceeded", reason: "model_context_window_exceeded", content: textBlock(""), overflow: true},
		{name: "malformed_model_output", reason: "malformed_model_output", content: textBlock(""), contains: "malformed_model_output"},
		{name: "malformed_tool_use", reason: "malformed_tool_use", content: textBlock(""), contains: "malformed_tool_use"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			transport := &wireTransport{handler: replyJSON(converseResponse(c.content, c.reason, ""))}
			provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
			result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
			switch {
			case c.refused != "":
				require.ErrorIs(t, err, modelrepo.ErrRefused)
				var refusal *modelrepo.RefusalError
				require.ErrorAs(t, err, &refusal)
				require.Equal(t, "bedrock", refusal.Provider)
				require.Equal(t, c.refused, refusal.Code)
				require.Equal(t, strings.TrimSpace(c.content["text"].(string)), refusal.Text)
			case c.overflow:
				require.ErrorIs(t, err, modelrepo.ErrContextLengthExceeded)
			case c.contains != "":
				require.ErrorContains(t, err, c.contains)
			default:
				require.NoError(t, err)
				require.Equal(t, c.finish, result.FinishReason)
			}
		})
	}

	t.Run("an empty turn that reports why it stopped is a completed turn", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(converseResponse(textBlock(""), "end_turn", ""))}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
		require.NoError(t, err)
		require.Empty(t, result.Message.Content)
		require.Equal(t, "end_turn", result.FinishReason)
	})

	t.Run("an empty turn with no stop reason is an error rather than an empty answer", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(converseResponse(textBlock(""), "", ""))}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
		require.ErrorContains(t, err, "no stop reason")
	})
}

func TestUnit_BedrockConverseResponse_UsageNormalization(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(converseResponse(textBlock("hi"), "end_turn",
		`{"inputTokens":7,"outputTokens":11,"totalTokens":18,"cacheReadInputTokens":100,"cacheWriteInputTokens":30}`))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.Equal(t, 137, result.Usage.PromptTokens)
	require.Equal(t, 11, result.Usage.CompletionTokens)
	require.Equal(t, 148, result.Usage.TotalTokens)
	require.Equal(t, 100, result.Usage.CacheReadTokens)
	require.Equal(t, 30, result.Usage.CacheWriteTokens)

	t.Run("an unreported usage stays nil", func(t *testing.T) {
		transport := &wireTransport{handler: replyJSON(textOut("hi", "end_turn"))}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		result, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
		require.NoError(t, err)
		require.Nil(t, result.Usage)
	})
}

func TestUnit_BedrockConverseError_ClassifiesTheDocumentedErrorList(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		errorType string
		message   string
		sentinel  error
	}{
		{"throttling", http.StatusTooManyRequests, "ThrottlingException", "Too many requests", modelrepo.ErrRateLimited},
		{"model not ready", http.StatusTooManyRequests, "ModelNotReadyException", "Model is not ready", modelrepo.ErrRateLimited},
		{"context length", http.StatusBadRequest, "ValidationException", "Input is too long for requested model.", modelrepo.ErrContextLengthExceeded},
		{"resource not found", http.StatusNotFound, "ResourceNotFoundException", "Model not found", modelrepo.ErrModelNotFoundOnBackend},
		{"access denied", http.StatusForbidden, "AccessDeniedException", "Not authorized", modelrepo.ErrModelAccessDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			transport := &wireTransport{handler: func(request *http.Request, _ []byte) *http.Response {
				return errorResponse(request, c.status, c.errorType, c.message)
			}}
			provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
			_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
			require.ErrorIs(t, err, c.sentinel)
			require.ErrorContains(t, err, c.message)
			require.ErrorContains(t, err, c.errorType)
		})
	}

	t.Run("an unclassified error keeps the vendor's diagnostic", func(t *testing.T) {
		transport := &wireTransport{handler: func(request *http.Request, _ []byte) *http.Response {
			return errorResponse(request, http.StatusInternalServerError, "InternalServerException", "internal failure")
		}}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
		require.ErrorContains(t, err, "internal failure")
		require.NotErrorIs(t, err, modelrepo.ErrRateLimited)
		require.NotErrorIs(t, err, modelrepo.ErrContextLengthExceeded)
	})

	t.Run("a non-context validation error is not an overflow", func(t *testing.T) {
		transport := &wireTransport{handler: func(request *http.Request, _ []byte) *http.Response {
			return errorResponse(request, http.StatusBadRequest, "ValidationException", "The provided model identifier is invalid.")
		}}
		provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
		_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
		require.NotErrorIs(t, err, modelrepo.ErrContextLengthExceeded)
		require.NotErrorIs(t, err, modelrepo.ErrRateLimited)
		require.ErrorContains(t, err, "The provided model identifier is invalid.")
	})
}

func TestUnit_BedrockProvider_UsesTheRegionItsBackendNames(t *testing.T) {
	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{
		Type:    "bedrock",
		BaseURL: "https://bedrock-runtime.eu-west-3.amazonaws.com",
	})
	require.NoError(t, err)
	require.Equal(t, "bedrock", catalog.Type())
	provider := catalog.ProviderFor(modelrepo.ObservedModel{
		Name:             claudeSonnet45,
		CapabilityConfig: modelrepo.CapabilityConfig{CanChat: true},
	})
	require.Equal(t, "bedrock:eu-west-3", provider.GetBackendIDs()[0])

	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	reached := bedrockRegionProvider("eu-west-3", claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	_, err = chatOnce(t, reached, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	transport.mu.Lock()
	host := transport.host
	transport.mu.Unlock()
	require.Equal(t, "bedrock-runtime.eu-west-3.amazonaws.com", host)
}

func TestUnit_BedrockProvider_CapabilitiesComeFromTheDeclaration(t *testing.T) {
	provider := newWireProvider("anthropic.claude-3-7-sonnet-20250219-v1:0", modelrepo.CapabilityConfig{CanChat: true}, &wireTransport{})
	require.False(t, provider.CanThink(), "a model name alone must not declare thinking")
	require.False(t, provider.CanEmbed())
	require.False(t, provider.CanAudio())
	require.False(t, provider.CanAutocomplete())
	require.False(t, provider.CanNextEdit())

	declared := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true, CanThink: true}, &wireTransport{})
	require.True(t, declared.CanThink())

	_, err := declared.GetEmbedConnection(context.Background(), "bedrock:us-east-1")
	require.ErrorContains(t, err, "does not support embeddings")
	_, err = declared.GetAutocompleteConnection(context.Background(), "bedrock:us-east-1")
	require.ErrorIs(t, err, modelrepo.ErrAutocompleteUnsupported)
	_, err = declared.GetNextEditConnection(context.Background(), "bedrock:us-east-1")
	require.ErrorIs(t, err, modelrepo.ErrNextEditUnsupported)
}

func TestUnit_BedrockConverseRequest_RefusesAudioInput(t *testing.T) {
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	_, err := chatOnce(t, provider, []modelrepo.Message{{
		Role:  "user",
		Audio: []modelrepo.AudioPart{{Data: []byte{1}, MimeType: "audio/wav"}},
	}})
	require.ErrorIs(t, err, modelrepo.ErrAudioNotSupported)
	require.Zero(t, transport.requests)
}

func bedrockRegionProvider(region, model string, capabilities modelrepo.CapabilityConfig, transport *wireTransport) modelrepo.Provider {
	return bedrock.NewBedrockProvider(region, staticCredentials, model, capabilities, &http.Client{Transport: transport}, nil)
}

func TestUnit_BedrockConverseRequest_SanitizedToolNames(t *testing.T) {
	longest := strings.Repeat("a", 64)
	transport := &wireTransport{handler: replyJSON(textOut("ok", "end_turn"))}
	provider := newWireProvider(claudeSonnet45, modelrepo.CapabilityConfig{CanChat: true}, transport)
	_, err := chatOnce(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTools(
			toolSpec("", "empty", objectSchema()),
			toolSpec("-abc-", "trimmed dashes", objectSchema()),
			toolSpec("_abc_", "trimmed underscores", objectSchema()),
			toolSpec(".", "punctuation only", objectSchema()),
			toolSpec("__.-.__", "punctuation only variant", objectSchema()),
			toolSpec(longest+"_", "trimmed to the maximum", objectSchema()),
		),
	)
	require.NoError(t, err)
	specs := toolSpecs(t, transport.mustBody(t))
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.(map[string]any)["toolSpec"].(map[string]any)["name"].(string))
	}
	require.Equal(t, []string{"tool", "abc", "abc_2", "tool_2", "tool_3", longest}, names)
}

func TestUnit_BedrockConverseStream_MetadataCarriesCacheUsage(t *testing.T) {
	provider := streamTextProvider(t, [][]byte{
		eventFrame("contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": "hi"}}),
		eventFrame("messageStop", map[string]any{"stopReason": "end_turn"}),
		eventFrame("metadata", map[string]any{"usage": map[string]any{
			"inputTokens": 5, "outputTokens": 3, "cacheReadInputTokens": 60, "cacheWriteInputTokens": 15,
		}}),
	}, modelrepo.CapabilityConfig{CanChat: true, CanStream: true})
	result, err := drainStream(t, provider, []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.Equal(t, 80, result.Usage.PromptTokens)
	require.Equal(t, 3, result.Usage.CompletionTokens)
	require.Equal(t, 60, result.Usage.CacheReadTokens)
	require.Equal(t, 15, result.Usage.CacheWriteTokens)
}
