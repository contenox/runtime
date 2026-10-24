package vllm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

type vLLMPromptClient struct {
	vLLMClient
}

type vLLMChatClient struct {
	vLLMClient
}

type vLLMClient struct {
	baseURL         string
	httpClient      *http.Client
	modelName       string
	maxTokens       int
	maxOutputTokens int
	canThink        bool
	canAudio        bool
	apiKey          string
	tracker         libtracker.ActivityTracker
}

type chatRequest struct {
	Model              string           `json:"model"`
	Messages           []any            `json:"messages"`
	Temperature        *float64         `json:"temperature,omitempty"`
	MaxTokens          *int             `json:"max_tokens,omitempty"`
	TopP               *float64         `json:"top_p,omitempty"`
	Seed               *int             `json:"seed,omitempty"`
	Stream             bool             `json:"stream,omitempty"`
	StreamOptions      *streamOptions   `json:"stream_options,omitempty"`
	Tools              []modelrepo.Tool `json:"tools,omitempty"`
	ToolChoice         any              `json:"tool_choice,omitempty"`
	ParallelToolCalls  *bool            `json:"parallel_tool_calls,omitempty"`
	Stop               []string         `json:"stop,omitempty"`
	ResponseFormat     *responseFormat  `json:"response_format,omitempty"`
	SessionID          string           `json:"session_id,omitempty"`
	ReasoningEffort    string           `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs map[string]any   `json:"chat_template_kwargs,omitempty"`
}

type responseFormat struct {
	Type       string                  `json:"type"`
	JSONSchema *responseFormatJSONName `json:"json_schema,omitempty"`
}

type responseFormatJSONName struct {
	Name   string `json:"name"`
	Schema any    `json:"schema"`
	Strict bool   `json:"strict,omitempty"`
}

func wireResponseFormat(format *modelrepo.ResponseFormat) *responseFormat {
	if format == nil || format.Mode == modelrepo.ResponseFormatText {
		return nil
	}
	wire := &responseFormat{Type: string(format.Mode)}
	if format.Mode == modelrepo.ResponseFormatJSONSchema {
		wire.JSONSchema = &responseFormatJSONName{Name: format.Name, Schema: format.Schema, Strict: format.Strict}
	}
	return wire
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int          `json:"created"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens       int `json:"cached_tokens"`
		CreatedCacheTokens int `json:"created_cache_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u chatUsage) Usage() *modelrepo.TokenUsage {
	return &modelrepo.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		ThinkingTokens:   u.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      u.TotalTokens,
		CacheReadTokens:  u.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: u.PromptTokensDetails.CreatedCacheTokens,
	}
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Reasoning        string         `json:"reasoning,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
}

func (m chatMessage) Thinking() string {
	if m.Reasoning != "" {
		return m.Reasoning
	}
	return m.ReasoningContent
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Index    *int   `json:"index,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func convertChatToolCalls(toolCalls []chatToolCall, nameMap map[string]string) []modelrepo.ToolCall {
	out := make([]modelrepo.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		name := tc.Function.Name
		if orig, ok := nameMap[name]; ok && orig != "" {
			name = orig
		}
		out = append(out, modelrepo.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return out
}

func (c *vLLMClient) sendRequest(ctx context.Context, endpoint string, request interface{}, response interface{}) error {
	url := c.baseURL + endpoint
	reqBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	tracker := c.tracker
	reportErr, reportChange, end := tracker.Start(
		ctx,
		"http_request",
		"vllm",
		"model", c.modelName,
		"endpoint", endpoint,
		"base_url", c.baseURL,
	)
	defer end()

	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	resp, err := modelrepo.DoWithRetry(ctx, c.httpClient, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if c.apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.apiKey)
		}
		return req, nil
	})
	if err != nil {
		err = fmt.Errorf("HTTP request failed for model %s: %w", c.modelName, err)
		reportErr(err)
		return err
	}
	defer resp.Body.Close()

	reportChange("http_response", map[string]any{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
	})

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		err = fmt.Errorf("vLLM API returned non-200 status: %d, body: %s for model %s", resp.StatusCode, string(bodyBytes), c.modelName)
		err = modelrepo.ClassifyProviderError(err, resp.StatusCode, "", string(bodyBytes))
		reportErr(err)
		return err
	}

	if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
		err = fmt.Errorf("failed to decode response for model %s: %w", c.modelName, err)
		reportErr(err)
		return err
	}

	reportChange("request_completed", nil)
	return nil
}

type vllmWireMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	ToolCalls  []vllmWireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

type vllmWireToolCall struct {
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type"`
	Function vllmWireToolFunction `json:"function"`
}

type vllmWireToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type vllmContentMessage struct {
	Role    string            `json:"role"`
	Content []vllmContentPart `json:"content"`
}

type vllmContentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ImageURL   *vllmImageURL   `json:"image_url,omitempty"`
	InputAudio *vllmInputAudio `json:"input_audio,omitempty"`
}

type vllmImageURL struct {
	URL string `json:"url"`
}

type vllmInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

var audioFormats = map[string]string{
	"audio/wav":  "wav",
	"audio/mpeg": "mp3",
	"audio/mp4":  "mp4",
	"audio/ogg":  "ogg",
	"audio/flac": "flac",
}

func toVLLMRequestMessages(messages []modelrepo.Message, origToSanitized map[string]string) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		if len(m.Images) > 0 || len(m.Audio) > 0 {
			parts := make([]vllmContentPart, 0, len(m.Images)+len(m.Audio)+1)
			if m.Content != "" {
				parts = append(parts, vllmContentPart{Type: "text", Text: m.Content})
			}
			for _, img := range m.Images {
				parts = append(parts, vllmContentPart{
					Type:     "image_url",
					ImageURL: &vllmImageURL{URL: vllmImageDataURI(img.MimeType, img.Data)},
				})
			}
			for _, audio := range m.Audio {
				parts = append(parts, vllmContentPart{
					Type: "input_audio",
					InputAudio: &vllmInputAudio{
						Data:   base64.StdEncoding.EncodeToString(audio.Data),
						Format: audioFormats[audio.MimeType],
					},
				})
			}
			out = append(out, vllmContentMessage{Role: m.Role, Content: parts})
			continue
		}
		wm := vllmWireMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			name := tc.Function.Name
			if san, ok := origToSanitized[name]; ok && san != "" {
				name = san
			} else if s := sanitizeToolName(name); s != "" {
				name = s
			}
			wm.ToolCalls = append(wm.ToolCalls, vllmWireToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: vllmWireToolFunction{
					Name:      name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		out = append(out, wm)
	}
	return out
}

func vllmImageDataURI(mimeType string, data []byte) string {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func buildChatRequest(modelName string, messages []modelrepo.Message, args []modelrepo.ChatArgument, canThink ...bool) (chatRequest, map[string]string) {
	config := &modelrepo.ChatConfig{}
	for _, arg := range args {
		arg.Apply(config)
	}

	return buildChatRequestFromConfig(modelName, messages, config, canThink...)
}

func buildChatRequestFromConfig(modelName string, messages []modelrepo.Message, config *modelrepo.ChatConfig, canThink ...bool) (chatRequest, map[string]string) {
	tools, nameMap, origToSanitized := sanitizeVLLMTools(config.Tools)
	req := chatRequest{
		Model:             modelName,
		Messages:          toVLLMRequestMessages(messages, origToSanitized),
		Temperature:       config.Temperature,
		MaxTokens:         config.MaxTokens,
		TopP:              config.TopP,
		Seed:              config.Seed,
		Stream:            false,
		Tools:             tools,
		ParallelToolCalls: config.ParallelToolCalls,
		Stop:              config.StopSequences,
		ResponseFormat:    wireResponseFormat(config.Format),
		ToolChoice:        wireToolChoice(config.ToolChoice, origToSanitized),
	}
	if config.CacheHints != nil {
		req.SessionID = config.CacheHints.SessionKey
	}

	if vllmThinkingAllowed(canThink) {
		if effort, ok := vllmReasoningEffort(config.Think); ok {
			req.ReasoningEffort = effort
			req.ChatTemplateKwargs = map[string]any{"enable_thinking": effort != "none"}
		}
	}

	return req, nameMap
}

func wireToolChoice(choice *modelrepo.ToolChoice, sanitized map[string]string) any {
	if choice == nil {
		return nil
	}
	if choice.Tool == "" {
		return string(choice.Mode)
	}
	name := sanitized[choice.Tool]
	if name == "" {
		name = sanitizeToolName(choice.Tool)
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": name}}
}

func sanitizeVLLMTools(in []modelrepo.Tool) ([]modelrepo.Tool, map[string]string, map[string]string) {
	if len(in) == 0 {
		return nil, nil, nil
	}
	nameMap := make(map[string]string, len(in))
	origToSanitized := make(map[string]string, len(in))
	seen := map[string]int{}
	out := make([]modelrepo.Tool, 0, len(in))
	for i, t := range in {
		if strings.ToLower(t.Type) != "function" || t.Function == nil {
			out = append(out, t)
			continue
		}
		orig := t.Function.Name
		name := sanitizeToolName(orig)
		if name == "" {
			name = fmt.Sprintf("tool_%d", i)
		}
		name = uniquifyToolName(seen, name)
		nameMap[name] = orig
		origToSanitized[orig] = name
		fn := *t.Function
		fn.Name = name
		out = append(out, modelrepo.Tool{Type: t.Type, Function: &fn})
	}
	return out, nameMap, origToSanitized
}

func sanitizeToolName(in string) string {
	if in == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range in {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_-")
}

func uniquifyToolName(seen map[string]int, name string) string {
	if _, ok := seen[name]; !ok {
		seen[name] = 1
		return name
	}
	i := seen[name]
	for {
		candidate := fmt.Sprintf("%s_%d", name, i)
		if _, ok := seen[candidate]; !ok {
			seen[name] = i + 1
			seen[candidate] = 1
			return candidate
		}
		i++
	}
}

func (c *vLLMClient) clampChatRequest(req *chatRequest) {
	if req == nil {
		return
	}
	req.MaxTokens = modelrepo.ClampMaxOutputTokensPtr(req.MaxTokens, c.maxOutputTokens)
}

func vllmThinkingAllowed(canThink []bool) bool {
	return len(canThink) == 0 || canThink[0]
}

func vllmReasoningEffort(think *string) (string, bool) {
	level, ok, err := reasoning.NormalizeOptional(valueOfStringPtr(think))
	if err != nil || !ok || level == reasoning.Auto {
		return "", false
	}
	switch level {
	case reasoning.Off:
		return "none", true
	case reasoning.Minimal, reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max:
		return level, true
	default:
		return "", false
	}
}

func (c *vLLMClient) checkAudio(messages []modelrepo.Message) error {
	if !c.canAudio {
		return modelrepo.RefuseAudioInput("vllm", c.modelName, messages)
	}
	return modelrepo.ValidateAudioParts("vllm", c.modelName, messages)
}

func normalizeBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func valueOfStringPtr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
