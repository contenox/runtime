package vllm_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/vllm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type vllmStub struct {
	health      int
	openAPI     string
	models      string
	chat        []string
	embeddings  string
	completions []string
	status      map[string]int

	bodies []struct {
		path string
		body map[string]any
	}
}

func (s *vllmStub) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			var raw json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err == nil && len(raw) > 0 {
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Errorf("decode %s body: %v (%s)", r.URL.Path, err, raw)
					return
				}
			}
		}
		s.bodies = append(s.bodies, struct {
			path string
			body map[string]any
		}{r.URL.Path, body})

		if code, ok := s.status[r.URL.Path]; ok {
			w.WriteHeader(code)
			fmt.Fprint(w, `{"error":{"message":"scripted","type":"InternalServerError","code":500}}`)
			return
		}
		switch r.URL.Path {
		case "/health":
			code := s.health
			if code == 0 {
				code = http.StatusOK
			}
			w.WriteHeader(code)
		case "/openapi.json":
			if s.openAPI == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, s.openAPI)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, s.models)
		case "/v1/chat/completions":
			if streaming, _ := body["stream"].(bool); streaming {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, line := range s.chat {
					fmt.Fprintf(w, "data: %s\n\n", line)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if len(s.chat) > 0 {
				fmt.Fprint(w, s.chat[0])
				return
			}
			fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		case "/v1/embeddings":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, s.embeddings)
		case "/v1/completions":
			w.Header().Set("Content-Type", "application/json")
			if len(s.completions) > 0 {
				fmt.Fprint(w, s.completions[0])
				return
			}
			fmt.Fprint(w, `{"choices":[{"text":"x","finish_reason":"stop"}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *vllmStub) bodyFor(path string) map[string]any {
	for i := len(s.bodies) - 1; i >= 0; i-- {
		if s.bodies[i].path == path {
			return s.bodies[i].body
		}
	}
	return nil
}

func vllmProvider(t *testing.T, srv *httptest.Server, caps modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	return vllm.NewVLLMProvider("test-model", []string{srv.URL}, srv.Client(), caps, "", nil)
}

func vllmChat(t *testing.T, srv *httptest.Server, caps modelrepo.CapabilityConfig) modelrepo.LLMChatClient {
	t.Helper()
	client, err := vllmProvider(t, srv, caps).GetChatConnection(context.Background(), srv.URL)
	require.NoError(t, err)
	return client
}

func TestSystem_VLLMStream_FlatErrorFrameSurfaces(t *testing.T) {
	stub := &vllmStub{chat: []string{`{"object":"error","message":"matryoshka unsupported","type":"BadRequestError","param":null,"code":400}`}}
	srv := stub.start(t)

	stream, err := vllmProvider(t, srv, modelrepo.CapabilityConfig{CanStream: true}).GetStreamConnection(context.Background(), srv.URL)
	require.NoError(t, err)
	ch, err := stream.Stream(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	asm := modelrepo.NewStreamAssembler("vllm", "test-model")
	for parcel := range ch {
		_ = asm.Consume(parcel)
	}
	_, err = asm.Result()
	require.ErrorContains(t, err, "matryoshka unsupported")
}

func TestSystem_VLLMEmbed_ExecutesTheEmbeddingRoute(t *testing.T) {
	stub := &vllmStub{embeddings: `{"object":"list","data":[{"index":0,"object":"embedding","embedding":[0.5,-0.25]}],"usage":{"prompt_tokens":5,"total_tokens":5}}`}
	srv := stub.start(t)

	client, err := vllmProvider(t, srv, modelrepo.CapabilityConfig{CanEmbed: true}).GetEmbedConnection(context.Background(), srv.URL)
	require.NoError(t, err)

	vector, err := client.Embed(context.Background(), "hello")
	require.NoError(t, err)
	assert.Equal(t, []float64{0.5, -0.25}, vector)

	body := stub.bodyFor("/v1/embeddings")
	require.NotNil(t, body, "the embedding route was never called")
	assert.Equal(t, "test-model", body["model"])
	assert.Equal(t, "hello", body["input"])
	assert.Equal(t, "float", body["encoding_format"])
}

func TestSystem_VLLMEmbed_RefusesWithoutTheDeclaration(t *testing.T) {
	stub := &vllmStub{}
	srv := stub.start(t)

	_, err := vllmProvider(t, srv, modelrepo.CapabilityConfig{CanChat: true}).GetEmbedConnection(context.Background(), srv.URL)
	require.Error(t, err)
	assert.False(t, vllmProvider(t, srv, modelrepo.CapabilityConfig{CanChat: true}).CanEmbed())
}

func TestSystem_VLLMChat_SendsTheVendorsSamplingControls(t *testing.T) {
	stub := &vllmStub{}
	srv := stub.start(t)
	chat := vllmChat(t, srv, modelrepo.CapabilityConfig{CanChat: true})

	tool := modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "get_weather"}}
	parallel := false
	_, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithTool(tool),
		modelrepo.WithStopSequences("</s>", "USER:"),
		modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}),
		modelrepo.WithParallelToolCalls(parallel),
		modelrepo.WithResponseFormat(modelrepo.ResponseFormat{Mode: modelrepo.ResponseFormatJSONObject}),
	)
	require.NoError(t, err)

	body := stub.bodyFor("/v1/chat/completions")
	require.NotNil(t, body)
	assert.Equal(t, []any{"</s>", "USER:"}, body["stop"])
	assert.Equal(t, "required", body["tool_choice"])
	assert.Equal(t, false, body["parallel_tool_calls"])
	format, ok := body["response_format"].(map[string]any)
	require.True(t, ok, "response_format missing: %#v", body)
	assert.Equal(t, "json_object", format["type"])
}

func TestSystem_VLLMChat_ReportsTheCacheDimensions(t *testing.T) {
	stub := &vllmStub{chat: []string{`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_tokens_details":{"cached_tokens":64,"created_cache_tokens":32},"completion_tokens_details":{"reasoning_tokens":3}}}`}}
	srv := stub.start(t)

	res, err := vllmChat(t, srv, modelrepo.CapabilityConfig{CanChat: true}).Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)
	require.NotNil(t, res.Usage)
	assert.Equal(t, 64, res.Usage.CacheReadTokens)
	assert.Equal(t, 32, res.Usage.CacheWriteTokens)
	assert.Equal(t, 3, res.Usage.ThinkingTokens)
}

func TestSystem_VLLMChat_NormalizesATrailingSlashBaseURL(t *testing.T) {
	stub := &vllmStub{}
	srv := stub.start(t)

	client, err := vllm.NewVLLMChatClient(context.Background(), srv.URL+"/", "test-model", 4096, 2048, srv.Client(), "", false, false, nil)
	require.NoError(t, err)
	_, err = client.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.NotNil(t, stub.bodyFor("/v1/chat/completions"), "the doubled path was requested: %#v", stub.bodies)
}

func TestSystem_VLLMChat_SendsTheSessionIdentity(t *testing.T) {
	stub := &vllmStub{}
	srv := stub.start(t)
	chat := vllmChat(t, srv, modelrepo.CapabilityConfig{CanChat: true})

	_, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
		modelrepo.WithCacheHints(modelrepo.CacheHints{SessionKey: "session-7"}))
	require.NoError(t, err)

	body := stub.bodyFor("/v1/chat/completions")
	require.NotNil(t, body)
	assert.Equal(t, "session-7", body["session_id"])
}

func TestSystem_VLLMChat_SendsTheVendorsReasoningLiteral(t *testing.T) {
	stub := &vllmStub{}
	srv := stub.start(t)
	chat := vllmChat(t, srv, modelrepo.CapabilityConfig{CanChat: true, CanThink: true})

	_, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("max"))
	require.NoError(t, err)
	assert.Equal(t, "max", stub.bodyFor("/v1/chat/completions")["reasoning_effort"])

	_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}}, modelrepo.WithThink("xhigh"))
	require.NoError(t, err)
	assert.Equal(t, "xhigh", stub.bodyFor("/v1/chat/completions")["reasoning_effort"])
}

func TestSystem_VLLMChat_SendsAudioInputParts(t *testing.T) {
	stub := &vllmStub{}
	srv := stub.start(t)
	chat := vllmChat(t, srv, modelrepo.CapabilityConfig{CanChat: true, CanAudio: true})

	wav := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x41, 0x56, 0x45}
	_, err := chat.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "transcribe", Audio: []modelrepo.AudioPart{{Data: wav, MimeType: "audio/wav"}}},
	})
	require.NoError(t, err)

	raw, err := json.Marshal(stub.bodyFor("/v1/chat/completions")["messages"])
	require.NoError(t, err)
	var messages []struct {
		Content []struct {
			Type       string `json:"type"`
			InputAudio *struct {
				Data   string `json:"data"`
				Format string `json:"format"`
			} `json:"input_audio"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(raw, &messages))
	require.Len(t, messages, 1)

	var audio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	}
	for _, part := range messages[0].Content {
		if part.Type == "input_audio" {
			audio = part.InputAudio
		}
	}
	require.NotNil(t, audio, "no input_audio part on the wire: %s", raw)
	assert.Equal(t, base64.StdEncoding.EncodeToString(wav), audio.Data)
	assert.Equal(t, "wav", audio.Format)
}

func TestSystem_VLLMChat_RefusesAudioWithoutTheDeclaration(t *testing.T) {
	stub := &vllmStub{}
	srv := stub.start(t)
	chat := vllmChat(t, srv, modelrepo.CapabilityConfig{CanChat: true})

	_, err := chat.Chat(context.Background(), []modelrepo.Message{
		{Role: "user", Content: "transcribe", Audio: []modelrepo.AudioPart{{Data: []byte("RIFF"), MimeType: "audio/wav"}}},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, modelrepo.ErrAudioNotSupported), "error = %v, want ErrAudioNotSupported", err)
}

func TestSystem_VLLMCatalog_DerivesCapabilitiesFromTheServedTask(t *testing.T) {
	stub := &vllmStub{
		openAPI:    `{"paths":{"/v1/embeddings":{},"/health":{}}}`,
		models:     `{"data":[{"id":"bge-m3","object":"model","max_model_len":8192}]}`,
		embeddings: `{"data":[{"index":0,"embedding":[1]}]}`,
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "vllm", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)

	model := models[0]
	assert.Equal(t, "bge-m3", model.Name)
	assert.Equal(t, 8192, model.ContextLength)
	assert.True(t, model.CanEmbed, "the mounted pooling route must declare the embedding capability")
	assert.False(t, model.CanChat, "a deployment that mounted no generation route cannot chat")
	assert.False(t, model.CanPrompt)
	assert.False(t, model.CanStream)

	provider := catalog.ProviderFor(model)
	_, err = provider.GetEmbedConnection(context.Background(), srv.URL)
	assert.NoError(t, err)
	_, err = provider.GetChatConnection(context.Background(), srv.URL)
	assert.Error(t, err, "a model the deployment cannot chat with must not offer a chat connection")
}

func TestSystem_VLLMCatalog_DerivesTheGenerationRoutes(t *testing.T) {
	stub := &vllmStub{
		openAPI: `{"paths":{"/v1/chat/completions":{},"/v1/completions":{}}}`,
		models:  `{"data":[{"id":"qwen3","object":"model","max_model_len":32768}]}`,
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "vllm", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.True(t, models[0].CanChat)
	assert.True(t, models[0].CanPrompt)
	assert.True(t, models[0].CanStream)
	assert.False(t, models[0].CanEmbed)
}

func TestSystem_VLLMCatalog_ReportsADeadEngine(t *testing.T) {
	stub := &vllmStub{
		health: http.StatusServiceUnavailable,
		models: `{"data":[{"id":"qwen3","object":"model","max_model_len":32768}]}`,
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "vllm", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	require.NoError(t, err)
	_, err = catalog.ListModels(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

func TestSystem_VLLMCatalog_ListsModelsWhenTheServerMountsNoHealthRoute(t *testing.T) {
	stub := &vllmStub{
		health: http.StatusNotFound,
		models: `{"data":[{"id":"qwen3","object":"model","max_model_len":32768}]}`,
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "vllm", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "qwen3", models[0].Name)
	assert.True(t, models[0].CanChat)
}

func TestSystem_VLLMCatalog_ReadsOnlyWhatTheVendorPublishes(t *testing.T) {
	stub := &vllmStub{
		openAPI: `{"paths":{"/v1/chat/completions":{}}}`,
		models:  `{"data":[{"id":"qwen3","object":"model","created":1,"owned_by":"vllm","max_model_len":32768}]}`,
	}
	srv := stub.start(t)

	catalog, err := modelrepo.NewCatalogProvider(modelrepo.BackendSpec{Type: "vllm", BaseURL: srv.URL}, modelrepo.WithCatalogHTTPClient(srv.Client()))
	require.NoError(t, err)
	models, err := catalog.ListModels(context.Background())
	require.NoError(t, err)
	require.Len(t, models, 1)

	model := models[0]
	assert.Equal(t, 32768, model.ContextLength)
	assert.False(t, model.CanAutocomplete, "no completion mode or FIM format was published")
	assert.Empty(t, model.FIMFormat)
	assert.False(t, model.CanNextEdit, "no next-edit profile was published")
	assert.Empty(t, model.NextEditFormat)
}
