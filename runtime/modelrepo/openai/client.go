package openai

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
)

type openAIClient struct {
	codex           *codexCatalog
	baseURL         string
	apiKey          string
	httpClient      *http.Client
	modelName       string
	maxTokens       int
	maxOutputTokens int
	tracker         libtracker.ActivityTracker
	supportsThink   bool
	thinking        *modelrepo.ThinkingTypes
	dialect         modelrepo.ChatDialect
}

func newDialectRequest(cfg *modelrepo.ChatConfig, thinking *modelrepo.ThinkingTypes, supportsThink bool) modelrepo.DialectRequest {
	req := modelrepo.DialectRequest{Thinking: thinking, Tools: len(cfg.Tools) > 0, CacheHints: cfg.CacheHints}
	if supportsThink {
		req.Think = cfg.Think
	}
	return req
}

func (c *openAIClient) vendorLabel() string {
	if c.dialect != nil {
		return c.dialect.Name()
	}
	return "OpenAI"
}

func (c *openAIClient) providerType() string {
	if c.codex != nil {
		return c.codex.Type()
	}
	return "openai"
}

type openAIChatRequest struct {
	Model                string               `json:"model"`
	Messages             []apiChatMessage     `json:"messages"`
	Temperature          *float64             `json:"temperature,omitempty"`
	MaxCompletionTokens  *int                 `json:"max_completion_tokens,omitempty"`
	TopP                 *float64             `json:"top_p,omitempty"`
	Seed                 *int                 `json:"seed,omitempty"`
	Stream               bool                 `json:"stream,omitempty"`
	StreamOptions        *openAIStreamOptions `json:"stream_options,omitempty"`
	Tools                []openAITool         `json:"tools,omitempty"`
	Stop                 []string             `json:"stop,omitempty"`
	ToolChoice           any                  `json:"tool_choice,omitempty"`
	ParallelToolCalls    *bool                `json:"parallel_tool_calls,omitempty"`
	ResponseFormat       any                  `json:"response_format,omitempty"`
	ReasoningEffort      string               `json:"reasoning_effort,omitempty"`
	PromptCacheKey       string               `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string               `json:"prompt_cache_retention,omitempty"`
}

type apiChatMessage struct {
	Role             string           `json:"role"`
	Content          any              `json:"content"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ToolCalls        []apiToolCallReq `json:"tool_calls,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

type apiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *apiImageURL `json:"image_url,omitempty"`
}

type apiImageURL struct {
	URL string `json:"url"`
}

type apiToolCallReq struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function openAIFunction2 `json:"function"`
}

type openAIFunction2 struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

func (c *openAIClient) chatRequestBody(req openAIChatRequest, args []modelrepo.ChatArgument) ([]byte, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	if c.dialect == nil {
		return body, nil
	}
	cfg := &modelrepo.ChatConfig{}
	for _, a := range args {
		if a == nil {
			continue
		}
		a.Apply(cfg)
	}
	return modelrepo.RewriteChatBody(c.dialect, body, newDialectRequest(cfg, c.thinking, c.supportsThink))
}

func (c *openAIClient) sendRequest(ctx context.Context, endpoint string, request any, response any) error {
	url := c.baseURL + endpoint

	tracker := c.tracker
	auth := "none"
	if c.apiKey != "" {
		auth = "bearer_set"
	}
	reportErr, reportChange, end := tracker.Start(
		ctx,
		"http_request",
		"openai",
		"model", c.modelName,
		"endpoint", endpoint,
		"base_url", c.baseURL,
		"auth", auth,
	)
	defer end()

	var body []byte
	switch encoded := request.(type) {
	case nil:
	case []byte:
		body = encoded
	default:
		var err error
		body, err = json.Marshal(request)
		if err != nil {
			err = fmt.Errorf("failed to marshal request: %w", err)
			reportErr(err)
			return err
		}
	}

	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()

	resp, err := modelrepo.DoWithRetry(ctx, c.httpClient, func() (*http.Request, error) {
		var reqBody io.Reader
		if body != nil {
			reqBody = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, "POST", url, reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
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
		code, message := modelrepo.ProviderErrorDetail(bodyBytes)
		err = fmt.Errorf("%s API returned non-200 status: %d for model %s: %s", c.vendorLabel(), resp.StatusCode, c.modelName, message)
		err = modelrepo.ClassifyProviderError(err, resp.StatusCode, code, message)
		reportErr(err)
		return err
	}

	if response != nil {
		if err := json.NewDecoder(resp.Body).Decode(response); err != nil {
			err = fmt.Errorf("failed to decode response for model %s: %w", c.modelName, err)
			reportErr(err)
			return err
		}
	}

	reportChange("request_completed", nil)
	return nil
}

func buildOpenAIRequest(modelName string, messages []modelrepo.Message, args []modelrepo.ChatArgument) (openAIChatRequest, map[string]string) {
	req, nameMap, err := buildOpenAIRequestForDialect(nil, modelName, messages, args, nil, true)
	if err != nil {
		return openAIChatRequest{}, nil
	}
	return req, nameMap
}

func (c *openAIClient) clampChatMaxOutputTokens(req *openAIChatRequest) {
	if req == nil {
		return
	}
	req.MaxCompletionTokens = modelrepo.ClampMaxOutputTokensPtr(req.MaxCompletionTokens, c.maxOutputTokens)
}

func (c *openAIClient) clampResponsesMaxOutputTokens(req *openAIResponsesRequest) {
	if req == nil {
		return
	}
	req.MaxOutputTokens = modelrepo.ClampMaxOutputTokensPtr(req.MaxOutputTokens, c.maxOutputTokens)
}

func buildOpenAIRequestWithCapabilities(modelName string, messages []modelrepo.Message, args []modelrepo.ChatArgument, supportsThink bool) (openAIChatRequest, map[string]string) {
	req, nameMap, err := buildOpenAIRequestForDialect(nil, modelName, messages, args, nil, supportsThink)
	if err != nil {
		return openAIChatRequest{}, nil
	}
	return req, nameMap
}

func chatArgsHaveTools(args []modelrepo.ChatArgument) bool {
	cfg := &modelrepo.ChatConfig{}
	for _, a := range args {
		if a == nil {
			continue
		}
		a.Apply(cfg)
	}
	return len(cfg.Tools) > 0
}

func buildOpenAIRequestForDialect(dialect modelrepo.ChatDialect, modelName string, messages []modelrepo.Message, args []modelrepo.ChatArgument, thinking *modelrepo.ThinkingTypes, supportsThink bool) (openAIChatRequest, map[string]string, error) {
	req := openAIChatRequest{
		Model: modelName,
	}

	cfg := &modelrepo.ChatConfig{}
	for _, a := range args {
		if a == nil {
			continue
		}
		a.Apply(cfg)
	}
	if err := cfg.Validate(); err != nil {
		return openAIChatRequest{}, nil, err
	}

	thinkingMember := ""
	if dialect != nil {
		thinkingMember = dialect.ThinkingMember(newDialectRequest(cfg, thinking, supportsThink))
	}

	if err := applyOpenAIChatControls(&req, modelName, cfg, thinking, supportsThink && !dialectOwnsReasoning(dialect)); err != nil {
		return openAIChatRequest{}, nil, err
	}

	nameMap := make(map[string]string)
	seen := map[string]int{}
	if len(cfg.Tools) > 0 {
		tools := make([]openAITool, 0, len(cfg.Tools))
		for i, t := range cfg.Tools {
			if strings.ToLower(t.Type) != "function" || t.Function == nil {
				continue
			}
			orig := t.Function.Name
			name := sanitizeToolName(orig)
			if name == "" {
				name = fmt.Sprintf("tool_%d", i)
			}
			name = uniquifyToolName(seen, name)
			nameMap[name] = orig
			tools = append(tools, openAITool{
				Type: "function",
				Function: openAIFunction{
					Name:        name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			})
		}
		if len(tools) > 0 {
			req.Tools = tools
		}
	}

	origToSanitized := make(map[string]string, len(nameMap))
	for san, orig := range nameMap {
		origToSanitized[orig] = san
	}
	req.ToolChoice = chatToolChoice(cfg.ToolChoice, origToSanitized)

	apiMsgs := make([]apiChatMessage, 0, len(messages))
	for _, msg := range messages {
		apiMsg := apiChatMessage{
			Role:       msg.Role,
			ToolCallID: msg.ToolCallID,
		}
		switch {
		case len(msg.Images) > 0:
			apiMsg.Content = openAIImageContent(msg)
		case msg.Content == "" && len(msg.ToolCalls) > 0:
			apiMsg.Content = nil
		default:
			apiMsg.Content = msg.Content
		}

		if thinkingMember != "" && msg.Role == "assistant" && msg.Thinking != "" {
			apiMsg.ReasoningContent = msg.Thinking
		}
		if dialect != nil && msg.Thinking != "" && len(msg.Images) == 0 {
			content, err := dialect.WireAssistantMessage(msg.Content, msg.Thinking)
			if err != nil {
				return openAIChatRequest{}, nil, fmt.Errorf("dialect %s: restate assistant turn: %w", dialect.Name(), err)
			}
			if content != nil {
				apiMsg.Content = content
			}
		}

		if len(msg.ToolCalls) > 0 {
			apiMsg.ToolCalls = make([]apiToolCallReq, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				name := tc.Function.Name
				if san, ok := origToSanitized[name]; ok {
					name = san
				} else {
					name = sanitizeToolName(name)
				}
				apiMsg.ToolCalls = append(apiMsg.ToolCalls, apiToolCallReq{
					ID:   tc.ID,
					Type: tc.Type,
					Function: openAIFunction2{
						Name:      name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}
		apiMsgs = append(apiMsgs, apiMsg)
	}
	req.Messages = apiMsgs

	return req, nameMap, nil
}

func dialectOwnsReasoning(dialect modelrepo.ChatDialect) bool {
	return dialect != nil && dialect.OwnsReasoning()
}

func openAIImageContent(msg modelrepo.Message) []apiContentPart {
	parts := make([]apiContentPart, 0, len(msg.Images)+1)
	if msg.Content != "" {
		parts = append(parts, apiContentPart{Type: "text", Text: msg.Content})
	}
	for _, img := range msg.Images {
		parts = append(parts, apiContentPart{
			Type:     "image_url",
			ImageURL: &apiImageURL{URL: imageDataURI(img.MimeType, img.Data)},
		})
	}
	return parts
}

func imageDataURI(mimeType string, data []byte) string {
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func openAIAPIBaseModelID(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return m
}

func sanitizeToolName(in string) string {
	if in == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range in {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	s = strings.Trim(s, "_-")
	return s
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
