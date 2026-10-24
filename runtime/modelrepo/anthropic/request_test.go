package anthropic_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/anthropic"
	msgcodec "github.com/contenox/runtime/runtime/modelrepo/codec/messages"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const anthropicVersionHeader = "2023-06-01"

func chatProvider(t *testing.T, server *httptest.Server, capability modelrepo.CapabilityConfig) modelrepo.Provider {
	t.Helper()
	capability.CanChat = true
	return anthropic.NewAnthropicProvider("secret-key", "claude-sonnet-4-5", []string{server.URL}, capability, server.Client(), nil)
}

func TestUnit_AnthropicChat_RequestShapeAndResponse(t *testing.T) {
	var gotPath, gotAPIKey, gotVersion string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &gotBody))
		_, _ = w.Write([]byte(`{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"hi there"}]}`))
	}))
	defer srv.Close()

	p := chatProvider(t, srv, modelrepo.CapabilityConfig{})
	chat, err := p.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	res, err := chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
	require.NoError(t, err)

	require.Equal(t, "/v1/messages", gotPath)
	require.Equal(t, "secret-key", gotAPIKey, "direct Anthropic must auth via x-api-key")
	require.Equal(t, anthropicVersionHeader, gotVersion, "direct Anthropic must send the documented anthropic-version header")
	require.Equal(t, "claude-sonnet-4-5", gotBody["model"], "direct Anthropic puts model in the body")
	require.Nil(t, gotBody["anthropic_version"], "anthropic_version is a Vertex-only body field")
	require.Equal(t, "hi there", res.Message.Content)
}

func TestUnit_AnthropicChat_OmitsThinkingWhenCapabilityIsFalse(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)
	body := chatBody(t, c, chatProvider(t, c.server, modelrepo.CapabilityConfig{}), modelrepo.WithThink("high"))

	require.NotContains(t, body, "thinking", "provider with CanThink=false must not send Anthropic thinking controls")
	require.NotContains(t, body, "output_config", "provider with CanThink=false must not send Anthropic effort controls")
}

func TestUnit_Anthropic_TypedSentinels(t *testing.T) {
	t.Run("prompt too long", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 250000 tokens > 200000 maximum"}}`))
		}))
		defer srv.Close()

		_, err := sendOneChat(t, srv.URL, srv.Client())
		require.ErrorIs(t, err, modelrepo.ErrContextLengthExceeded)
	})

	t.Run("429 rate limit", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After-Ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your rate limit"}}`))
		}))
		defer srv.Close()

		_, err := sendOneChat(t, srv.URL, srv.Client())
		require.ErrorIs(t, err, modelrepo.ErrRateLimited)
	})

	t.Run("529 overloaded", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After-Ms", "1")
			w.WriteHeader(529)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
		}))
		defer srv.Close()

		_, err := sendOneChat(t, srv.URL, srv.Client())
		require.ErrorIs(t, err, modelrepo.ErrRateLimited)
	})

	t.Run("402 billing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"billing_error","message":"Your credit balance is too low"}}`))
		}))
		defer srv.Close()

		_, err := sendOneChat(t, srv.URL, srv.Client())
		require.ErrorIs(t, err, modelrepo.ErrPaymentRequired)
		assert.Contains(t, err.Error(), "billing_error", "the vendor's own error type reaches the caller")
	})

	t.Run("401 authentication_error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"},"request_id":"req_011CfxhBG3cPJ5Pv9D3eBxkE"}`))
		}))
		defer srv.Close()

		_, err := sendOneChat(t, srv.URL, srv.Client())
		require.ErrorIs(t, err, modelrepo.ErrModelAccessDenied)
		assert.Contains(t, err.Error(), "authentication_error")
	})

	t.Run("404 not_found_error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: claude-nope-1"},"request_id":"req_011CfxhCQJWcXPWYoVVKPMt8"}`))
		}))
		defer srv.Close()

		_, err := sendOneChat(t, srv.URL, srv.Client())
		require.ErrorIs(t, err, modelrepo.ErrModelNotFoundOnBackend)
		assert.Contains(t, err.Error(), "not_found_error")
	})

	t.Run("413 request too large", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum allowed number of bytes"}}`))
		}))
		defer srv.Close()

		_, err := sendOneChat(t, srv.URL, srv.Client())
		require.ErrorIs(t, err, modelrepo.ErrRequestTooLarge)
	})
}

func sendOneChat(t *testing.T, url string, client *http.Client) (modelrepo.ChatResult, error) {
	t.Helper()
	p := anthropic.NewAnthropicProvider("secret-key", "claude-haiku-4-5", []string{url},
		modelrepo.CapabilityConfig{CanChat: true}, client, nil)
	chat, err := p.GetChatConnection(context.Background(), "")
	require.NoError(t, err)
	return chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}})
}

func TestUnit_AnthropicCache_WirePlacementAndUsage(t *testing.T) {
	c := newCapture(t, 200, `{"role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":900,"cache_creation_input_tokens":90}}`)

	provider := anthropic.NewAnthropicProvider("k", "claude-haiku-4-5", []string{c.server.URL},
		modelrepo.CapabilityConfig{CanChat: true}, c.server.Client(), nil)
	chat, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	res, err := chat.Chat(context.Background(),
		[]modelrepo.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
		},
		modelrepo.WithTools(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.read"}}),
		modelrepo.WithCacheHints(modelrepo.CacheHints{StableSystem: true, StableTools: true}),
	)
	require.NoError(t, err)

	wire := c.body(t)
	tools := wire["tools"].([]any)
	lastTool := tools[len(tools)-1].(map[string]any)
	cc, ok := lastTool["cache_control"].(map[string]any)
	require.True(t, ok, "last tool must carry cache_control: %v", wire)
	require.Equal(t, "ephemeral", cc["type"])

	system := wire["system"].([]any)
	require.Len(t, system, 1)
	sysBlock := system[0].(map[string]any)
	require.Equal(t, "be terse", sysBlock["text"], "system text must be unchanged")
	_, ok = sysBlock["cache_control"].(map[string]any)
	require.True(t, ok, "system block must carry cache_control")

	raw, err := json.Marshal(wire)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(raw), `"cache_control"`))

	require.NotNil(t, res.Usage)
	require.Equal(t, 1000, res.Usage.PromptTokens)
	require.Equal(t, 900, res.Usage.CacheReadTokens)
	require.Equal(t, 90, res.Usage.CacheWriteTokens)
	require.Equal(t, 4, res.Usage.CompletionTokens)
}

func TestUnit_AnthropicCache_HintlessRequestUnchanged(t *testing.T) {
	c := newCapture(t, 200, endTurnResponse)

	provider := anthropic.NewAnthropicProvider("k", "claude-haiku-4-5", []string{c.server.URL},
		modelrepo.CapabilityConfig{CanChat: true}, c.server.Client(), nil)
	chat, err := provider.GetChatConnection(context.Background(), "")
	require.NoError(t, err)

	res, err := chat.Chat(context.Background(),
		[]modelrepo.Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "hi"},
		},
		modelrepo.WithTools(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.read"}}),
	)
	require.NoError(t, err)

	wire := c.body(t)
	raw, err := json.Marshal(wire)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "cache_control")
	require.Equal(t, "be terse", wire["system"], "hint-less system stays the plain string form")
	require.Nil(t, res.Usage, "no usage in the response means none on the result")
}

type imageWireBody struct {
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Source *struct {
				Type      string `json:"type"`
				MediaType string `json:"media_type"`
				Data      string `json:"data"`
			} `json:"source"`
		} `json:"content"`
	} `json:"messages"`
}

func TestUnit_AnthropicImageInput_Serialization(t *testing.T) {
	rawImage := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x01, 0x02, 0x03}

	t.Run("user message with one image emits a base64 image block", func(t *testing.T) {
		messages := []modelrepo.Message{{
			Role:    "user",
			Content: "what is in this image?",
			Images:  []modelrepo.ImagePart{{Data: rawImage, MimeType: "image/png"}},
		}}

		req, _, err := msgcodec.Build(messages, nil)
		require.NoError(t, err)
		b, err := json.Marshal(req)
		require.NoError(t, err)

		var body imageWireBody
		require.NoError(t, json.Unmarshal(b, &body))
		require.Len(t, body.Messages, 1)
		require.Equal(t, "user", body.Messages[0].Role)

		blocks := body.Messages[0].Content
		require.Len(t, blocks, 2, "text block followed by exactly one image block")

		require.Equal(t, "text", blocks[0].Type)
		require.Equal(t, "what is in this image?", blocks[0].Text)
		require.Nil(t, blocks[0].Source, "text block must not carry a source object")

		require.Equal(t, "image", blocks[1].Type)
		require.NotNil(t, blocks[1].Source)
		require.Equal(t, "base64", blocks[1].Source.Type)
		require.Equal(t, "image/png", blocks[1].Source.MediaType)
		require.Equal(t, base64.StdEncoding.EncodeToString(rawImage), blocks[1].Source.Data,
			"data must be the base64 of the raw image bytes")
	})

	t.Run("text-only user message keeps its prior single-text-block shape", func(t *testing.T) {
		req, _, err := msgcodec.Build([]modelrepo.Message{{Role: "user", Content: "hello"}}, nil)
		require.NoError(t, err)
		b, err := json.Marshal(req)
		require.NoError(t, err)

		var body imageWireBody
		require.NoError(t, json.Unmarshal(b, &body))
		require.Len(t, body.Messages, 1)

		blocks := body.Messages[0].Content
		require.Len(t, blocks, 1, "text-only user message stays a single text block")
		require.Equal(t, "text", blocks[0].Type)
		require.Equal(t, "hello", blocks[0].Text)
		require.Nil(t, blocks[0].Source, "text-only block must omit the source object")
	})

	t.Run("media type the vendor does not accept is refused before the wire", func(t *testing.T) {
		messages := []modelrepo.Message{{
			Role:   "user",
			Images: []modelrepo.ImagePart{{Data: rawImage, MimeType: "image/tiff"}},
		}}

		_, _, err := msgcodec.Build(messages, nil)
		require.ErrorContains(t, err, "image/tiff")
	})
}

func TestUnit_AnthropicSampling_ThinkingOnForbidsTemperature(t *testing.T) {
	budgeted := &modelrepo.ThinkingTypes{Enabled: true, Disabled: true}

	t.Run("temperature is dropped while thinking is on", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-older", budgeted),
			modelrepo.WithThink("high"), modelrepo.WithTemperature(0.7))

		assert.NotContains(t, body, "temperature", "temperature is incompatible with thinking")
		assert.Equal(t, "enabled", thinkingOf(t, body)["type"])
	})

	t.Run("temperature is kept when thinking is off", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-older", budgeted),
			modelrepo.WithThink("off"), modelrepo.WithTemperature(0.7))

		assert.Equal(t, 0.7, body["temperature"])
	})

	t.Run("top_p survives only inside the documented band", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-older", budgeted),
			modelrepo.WithThink("high"), modelrepo.WithTopP(0.99))

		assert.Equal(t, 0.99, body["top_p"])

		c2 := newCapture(t, 200, endTurnResponse)
		body2 := chatBody(t, c2, declaredProvider(t, c2, "claude-older", budgeted),
			modelrepo.WithThink("high"), modelrepo.WithTopP(0.5))

		assert.NotContains(t, body2, "top_p")
	})
}

func TestUnit_AnthropicChat_ForcedToolUseRefusedWithManualThinking(t *testing.T) {
	budgeted := &modelrepo.ThinkingTypes{Enabled: true, Disabled: true}
	adaptive := &modelrepo.ThinkingTypes{Adaptive: true}

	t.Run("budget plus forced choice is refused before the wire", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		provider := declaredProvider(t, c, "claude-older", budgeted)
		chat, err := provider.GetChatConnection(context.Background(), "")
		require.NoError(t, err)

		_, err = chat.Chat(context.Background(), []modelrepo.Message{{Role: "user", Content: "hi"}},
			modelrepo.WithThink("high"),
			modelrepo.WithTool(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.read"}}),
			modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}))
		require.ErrorContains(t, err, "incompatible")
		assert.Empty(t, c.bodies, "a refused request must not reach the vendor")
	})

	t.Run("adaptive thinking accepts the same forced choice", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-newer", adaptive),
			modelrepo.WithThink("high"),
			modelrepo.WithTool(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.read"}}),
			modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}))

		assert.Equal(t, "adaptive", thinkingOf(t, body)["type"])
		assert.Equal(t, "any", body["tool_choice"].(map[string]any)["type"])
	})

	t.Run("forced choice without thinking is unchanged", func(t *testing.T) {
		c := newCapture(t, 200, endTurnResponse)
		body := chatBody(t, c, declaredProvider(t, c, "claude-older", budgeted),
			modelrepo.WithTool(modelrepo.Tool{Type: "function", Function: &modelrepo.FunctionTool{Name: "fs.read"}}),
			modelrepo.WithToolChoice(modelrepo.ToolChoice{Mode: modelrepo.ToolChoiceRequired}))

		assert.NotContains(t, body, "thinking")
		assert.Equal(t, "any", body["tool_choice"].(map[string]any)["type"])
	})
}
