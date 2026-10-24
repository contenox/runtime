package vertex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

func TestUnit_VertexChatClient_Chat(t *testing.T) {
	t.Parallel()

	var got vertexRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "), "expected ADC bearer token")
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.True(t, strings.HasSuffix(r.URL.Path, ":generateContent"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(vertexResponse{
			Candidates: []struct {
				Content      vertexContent `json:"content"`
				FinishReason string        `json:"finishReason,omitempty"`
			}{
				{Content: vertexContent{
					Role:  "model",
					Parts: []vertexPart{{Text: "hello back"}},
				}},
			},
		})
	}))
	defer srv.Close()

	client := &vertexChatClient{
		vertexClient: vertexClient{
			baseURL:         srv.URL + "/v1/projects/test/locations/us-central1",
			publisher:       "google",
			modelName:       "gemini-flash-latest",
			maxOutputTokens: 512,
			httpClient: &http.Client{
				Transport: bearerInjectTransport{
					serverURL: srv.URL,
					token:     "fake-adc-token",
				},
			},
			tracker: libtracker.NoopTracker{},
			tokenFn: func(_ context.Context) (string, error) { return "fake-adc-token", nil },
		},
	}

	result, err := client.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "hello"},
	}, modelrepo.WithMaxTokens(1000))
	require.NoError(t, err)
	require.Equal(t, "hello back", result.Message.Content)
	require.Equal(t, "assistant", result.Message.Role)
	require.NotNil(t, got.GenerationConfig)
	require.NotNil(t, got.GenerationConfig.MaxOutputTokens)
	require.Equal(t, 512, *got.GenerationConfig.MaxOutputTokens)
}

func TestUnit_BuildVertexRequest_MapsThinkingConfig(t *testing.T) {
	t.Parallel()
	msgs := []modelrepo.Message{{Role: "user", Content: "hi"}}

	req, err := buildVertexRequest("gemini-2.5-pro", msgs, []modelrepo.ChatArgument{modelrepo.WithThink("xhigh")})
	require.NoError(t, err)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	require.Equal(t, 32768, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	require.Equal(t, "", req.GenerationConfig.ThinkingConfig.ThinkingLevel)

	req, err = buildVertexRequest("gemini-2.5-pro", msgs, []modelrepo.ChatArgument{modelrepo.WithThink("off")})
	require.NoError(t, err)
	require.Nil(t, req.GenerationConfig.ThinkingConfig)

	req, err = buildVertexRequest("gemini-2.5-flash", msgs, []modelrepo.ChatArgument{modelrepo.WithThink("off")})
	require.NoError(t, err)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	require.Equal(t, 0, *req.GenerationConfig.ThinkingConfig.ThinkingBudget)

	req, err = buildVertexRequest("gemini-3-pro", msgs, []modelrepo.ChatArgument{modelrepo.WithThink("medium")})
	require.NoError(t, err)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.Nil(t, req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	require.Equal(t, "LOW", req.GenerationConfig.ThinkingConfig.ThinkingLevel)

	req, err = buildVertexRequest("gemini-3-flash", msgs, []modelrepo.ChatArgument{modelrepo.WithThink("minimal")})
	require.NoError(t, err)
	require.NotNil(t, req.GenerationConfig.ThinkingConfig)
	require.Equal(t, "MINIMAL", req.GenerationConfig.ThinkingConfig.ThinkingLevel)

	require.True(t, req.GenerationConfig.ThinkingConfig.IncludeThoughts)

	req, err = buildVertexRequest("gemini-3-pro", msgs, []modelrepo.ChatArgument{modelrepo.WithThink("auto")})
	require.NoError(t, err)
	require.Nil(t, req.GenerationConfig.ThinkingConfig)

	req, err = buildVertexRequest("gemini-2.5-pro", msgs, []modelrepo.ChatArgument{modelrepo.WithThink("high")}, false)
	require.NoError(t, err)
	require.Nil(t, req.GenerationConfig.ThinkingConfig, "provider with CanThink=false must omit Vertex thinking config")
}

func TestUnit_BuildVertexRequest_RejectsEmptyContents(t *testing.T) {
	t.Parallel()

	_, err := buildVertexRequest("gemini-3.1-pro-preview",
		[]modelrepo.Message{{Role: "system", Content: "system only"}},
		nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to send empty contents")
	require.Contains(t, err.Error(), "provide at least one non-empty")

	_, err = buildVertexRequest("gemini-3.1-pro-preview",
		[]modelrepo.Message{{Role: "user", Content: ""}},
		nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to send empty contents")
}

func TestUnit_BuildVertexRequest_TrimsTrailingModelTurn(t *testing.T) {
	t.Parallel()

	req, err := buildVertexRequest("gemini-3.1-pro-preview", []modelrepo.Message{
		{Role: "user", Content: "do the thing"},
		{Role: "assistant", Content: "I already answered this turn; continuing now."},
	}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, req.Contents)
	require.NotEqual(t, "model", req.Contents[len(req.Contents)-1].Role,
		"the request must not end with a model turn")
	require.Equal(t, "user", req.Contents[len(req.Contents)-1].Role)

	req, err = buildVertexRequest("gemini-3.1-pro-preview", []modelrepo.Message{
		{Role: "user", Content: "do the thing"},
		{Role: "assistant", Content: "first answer"},
		{Role: "assistant", Content: "second answer"},
	}, nil)
	require.NoError(t, err)
	last := req.Contents[len(req.Contents)-1]
	require.Equal(t, "user", last.Role)
}

func TestUnit_BuildVertexRequest_KeepsPairedToolCallBeforeItsResponse(t *testing.T) {
	t.Parallel()

	msgs := []modelrepo.Message{
		{Role: "user", Content: "inspect it"},
		{
			Role: "assistant",
			ToolCalls: []modelrepo.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "local_shell.run", Arguments: `{"cmd":"ls"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: `{"files":["a.go"]}`},
	}
	req, err := buildVertexRequest("gemini-3.1-pro-preview", msgs, nil)
	require.NoError(t, err)
	require.Len(t, req.Contents, 3)
	require.Equal(t, "user", req.Contents[len(req.Contents)-1].Role, "the tool result must stay the last content")
	require.NotNil(t, req.Contents[1].Parts[0].FunctionCall, "the functionCall content must survive the trim")
}

func TestUnit_BuildVertexRequest_TrimsUnpairedTrailingToolCall(t *testing.T) {
	t.Parallel()

	msgs := []modelrepo.Message{
		{Role: "user", Content: "inspect it"},
		{
			Role: "assistant",
			ToolCalls: []modelrepo.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "local_shell.run", Arguments: `{"cmd":"ls"}`},
			}},
		},
	}
	req, err := buildVertexRequest("gemini-3.1-pro-preview", msgs, nil)
	require.NoError(t, err)
	require.NotEmpty(t, req.Contents)
	require.Equal(t, "user", req.Contents[len(req.Contents)-1].Role,
		"the request must not end with a model turn")
}

func TestUnit_BuildVertexRequest_WrapsSchemaLikeToolResultAsText(t *testing.T) {
	t.Parallel()

	const schemaResult = `{"$defs":{"LogoutCapabilities":{"type":"object"}},"properties":{"logout":{"$ref":"#/$defs/LogoutCapabilities"}}}`
	msgs := []modelrepo.Message{
		{
			Role: "assistant",
			ToolCalls: []modelrepo.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "webtools.web_get", Arguments: `{"url":"https://example.test/schema.json"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: schemaResult},
	}

	req, err := buildVertexRequest("gemini-3.1-pro-preview", msgs, nil)
	require.NoError(t, err)
	require.Len(t, req.Contents, 2)
	resp := req.Contents[1].Parts[0].FunctionResponse.Response
	require.Equal(t, schemaResult, resp["content"])
	require.NotContains(t, resp, "$defs")
}

func TestUnit_BuildVertexRequest_ImageInputAddsInlineDataPart(t *testing.T) {
	t.Parallel()

	imgBytes := []byte("fake-jpeg-bytes")
	wantB64 := base64.StdEncoding.EncodeToString(imgBytes)

	msgs := []modelrepo.Message{
		{
			Role:    "user",
			Content: "describe this image",
			Images:  []modelrepo.ImagePart{{Data: imgBytes, MimeType: "image/jpeg"}},
		},
	}

	req, err := buildVertexRequest("gemini-flash-latest", msgs, nil)
	require.NoError(t, err)

	require.Len(t, req.Contents, 1)
	parts := req.Contents[0].Parts
	require.Len(t, parts, 2)
	require.Equal(t, "describe this image", parts[0].Text)
	require.NotNil(t, parts[1].InlineData)
	require.Equal(t, "image/jpeg", parts[1].InlineData.MimeType)
	require.Equal(t, imgBytes, parts[1].InlineData.Data)

	raw, err := json.Marshal(req)
	require.NoError(t, err)
	js := string(raw)
	require.Contains(t, js, `"inlineData":{`)
	require.Contains(t, js, `"mimeType":"image/jpeg"`)
	require.Contains(t, js, `"data":"`+wantB64+`"`)

	textReq, err := buildVertexRequest("gemini-flash-latest", []modelrepo.Message{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	require.Len(t, textReq.Contents, 1)
	require.Len(t, textReq.Contents[0].Parts, 1)
	require.Equal(t, "hi", textReq.Contents[0].Parts[0].Text)
	require.Nil(t, textReq.Contents[0].Parts[0].InlineData)
	textRaw, err := json.Marshal(textReq)
	require.NoError(t, err)
	require.NotContains(t, string(textRaw), "inlineData")
}

func TestUnit_BuildVertexRequest_KeepsNormalObjectToolResultStructured(t *testing.T) {
	t.Parallel()

	msgs := []modelrepo.Message{
		{
			Role: "assistant",
			ToolCalls: []modelrepo.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "webtools.web_get", Arguments: `{"url":"https://example.test/data.json"}`},
			}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: `{"status":"ok"}`},
	}

	req, err := buildVertexRequest("gemini-3.1-pro-preview", msgs, nil)
	require.NoError(t, err)
	require.Equal(t, "ok", req.Contents[1].Parts[0].FunctionResponse.Response["status"])
}

func TestUnit_BuildVertexRequest_KeepsOperatorTextBeforeTheFunctionResponse(t *testing.T) {
	t.Parallel()

	call := modelrepo.ToolCall{ID: "call-1", Type: "function"}
	call.Function.Name = "local_fs.edit_file"
	call.Function.Arguments = `{"path":"x.go"}`

	req, err := buildVertexRequest("gemini-3.1-pro-preview", []modelrepo.Message{
		{Role: "user", Content: "start"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{call}},
		{Role: "tool", ToolCallID: "call-1", Content: `{"error":"tool call was interrupted before a result was recorded"}`},
		{Role: "user", Content: "continue"},
	}, nil)
	require.NoError(t, err)

	require.Len(t, req.Contents, 3)
	for i := 1; i < len(req.Contents); i++ {
		require.NotEqual(t, req.Contents[i-1].Role, req.Contents[i].Role,
			"consecutive same-role contents fail the provider's alternation check")
	}

	last := req.Contents[len(req.Contents)-1]
	require.Equal(t, "user", last.Role)
	require.Len(t, last.Parts, 2)
	require.Equal(t, "continue", last.Parts[0].Text,
		"the operator's text must come before the function response")
	require.NotNil(t, last.Parts[1].FunctionResponse,
		"the function response must close the turn that carries it")
}

func TestUnit_BuildVertexRequest_KeepsParallelToolResponsesInOneTurn(t *testing.T) {
	t.Parallel()

	first := modelrepo.ToolCall{ID: "call-1", Type: "function"}
	first.Function.Name = "local_fs.read_file"
	first.Function.Arguments = `{"path":"a.go"}`
	second := modelrepo.ToolCall{ID: "call-2", Type: "function"}
	second.Function.Name = "local_shell.local_shell"
	second.Function.Arguments = `{"command":"ls"}`

	req, err := buildVertexRequest("gemini-3.1-pro-preview", []modelrepo.Message{
		{Role: "user", Content: "inspect both"},
		{Role: "assistant", ToolCalls: []modelrepo.ToolCall{first, second}},
		{Role: "tool", ToolCallID: "call-1", Content: `{"files":["a.go"]}`},
		{Role: "tool", ToolCallID: "call-2", Content: `{"exit_code":0}`},
	}, nil)
	require.NoError(t, err)

	require.Len(t, req.Contents, 3)
	require.Equal(t, "model", req.Contents[1].Role)
	responses := req.Contents[2]
	require.Equal(t, "user", responses.Role)
	require.Len(t, responses.Parts, 2)
	require.Equal(t, "local_fs.read_file", responses.Parts[0].FunctionResponse.Name)
	require.Equal(t, "local_shell.local_shell", responses.Parts[1].FunctionResponse.Name)
}
