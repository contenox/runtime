package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type geminiClient struct {
	apiKey          string
	modelName       string
	baseURL         string
	httpClient      *http.Client
	maxTokens       int
	maxOutputTokens int
	canThink        bool
	tracker         libtracker.ActivityTracker
}

type geminiGenerateContentRequest struct {
	SystemInstruction *geminiSystemInstruction `json:"systemInstruction,omitempty"`
	Contents          []geminiContent          `json:"contents"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
	Tools             []geminiToolRequest      `json:"tools,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature        *float64              `json:"temperature,omitempty"`
	TopP               *float64              `json:"topP,omitempty"`
	TopK               *int                  `json:"topK,omitempty"`
	CandidateCount     *int                  `json:"candidateCount,omitempty"`
	MaxOutputTokens    *int                  `json:"maxOutputTokens,omitempty"`
	StopSequences      []string              `json:"stopSequences,omitempty"`
	Seed               *int                  `json:"seed,omitempty"`
	ResponseMimeType   string                `json:"responseMimeType,omitempty"`
	ResponseJsonSchema any                   `json:"responseJsonSchema,omitempty"`
	ThinkingConfig     *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingBudget  *int   `json:"thinkingBudget,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
}

func (c *geminiClient) sendRequest(ctx context.Context, endpoint string, request interface{}, response interface{}) error {
	fullURL := fmt.Sprintf("%s%s", c.baseURL, endpoint)

	tracker := c.tracker
	reportErr, reportChange, end := tracker.Start(
		ctx,
		"http_request",
		"gemini",
		"model", c.modelName,
		"endpoint", endpoint,
		"base_url", c.baseURL,
	)
	defer end()

	var body []byte
	if request != nil {
		b, err := json.Marshal(request)
		if err != nil {
			err = fmt.Errorf("failed to marshal request: %w", err)
			reportErr(err)
			return err
		}
		body = b
	}

	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	resp, err := modelrepo.DoWithRetry(ctx, c.httpClient, func() (*http.Request, error) {
		var reqBody io.Reader
		if body != nil {
			reqBody = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fullURL, reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Goog-Api-Key", c.apiKey)
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
		var eresp struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		respBody, _ := io.ReadAll(resp.Body)
		if jsonErr := json.Unmarshal(respBody, &eresp); jsonErr == nil && eresp.Error.Message != "" {
			err = fmt.Errorf("gemini API error: %d %s - %s (model=%s url=%s)",
				resp.StatusCode, eresp.Error.Status, eresp.Error.Message, c.modelName, fullURL)
			err = modelrepo.ClassifyProviderError(err, resp.StatusCode, eresp.Error.Status, eresp.Error.Message)
			reportErr(err)
			return err
		}
		err = fmt.Errorf("gemini API error: %d - %s (model=%s url=%s)", resp.StatusCode, string(respBody), c.modelName, fullURL)
		err = modelrepo.ClassifyProviderError(err, resp.StatusCode, "", string(respBody))
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

func buildGeminiRequest(modelName string, messages []modelrepo.Message, args []modelrepo.ChatArgument, canThink ...bool) (geminiGenerateContentRequest, error) {
	if err := modelrepo.ValidateAudioParts("gemini", modelName, messages); err != nil {
		return geminiGenerateContentRequest{}, err
	}

	cfg := &modelrepo.ChatConfig{}
	for _, a := range args {
		a.Apply(cfg)
	}

	var systemInstruction *geminiSystemInstruction
	filtered := make([]modelrepo.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			if m.Content != "" {
				systemInstruction = &geminiSystemInstruction{
					Parts: []geminiPart{{Text: m.Content}},
				}
			}
			continue
		}
		filtered = append(filtered, m)
	}
	messages = filtered

	var tools []geminiToolRequest
	if len(cfg.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(cfg.Tools))
		for _, t := range cfg.Tools {
			if t.Type != "function" || t.Function == nil {
				continue
			}
			schema, err := geminiParameterSchema(t.Function.Parameters)
			if err != nil {
				return geminiGenerateContentRequest{}, err
			}
			decls = append(decls, geminiFunctionDeclaration{
				Name:                 t.Function.Name,
				Description:          t.Function.Description,
				ParametersJsonSchema: schema,
			})
		}
		if len(decls) > 0 {
			tools = append(tools, geminiToolRequest{
				FunctionDeclarations: decls,
			})
		}
	}

	contents := convertToGeminiMessages(messages, modelName)
	contents = trimTrailingModelTurn(contents)
	if len(contents) == 0 {
		return geminiGenerateContentRequest{}, fmt.Errorf("gemini: refusing to send empty contents for model %s after filtering %d message(s); provide at least one non-empty user/model/tool message, tool call, or tool response", modelName, len(messages))
	}

	req := geminiGenerateContentRequest{
		SystemInstruction: systemInstruction,
		Contents:          contents,
		GenerationConfig:  &geminiGenerationConfig{},
		Tools:             tools,
	}
	req.GenerationConfig.Temperature = cfg.Temperature
	req.GenerationConfig.TopP = cfg.TopP
	if cfg.MaxTokens != nil {
		req.GenerationConfig.MaxOutputTokens = cfg.MaxTokens
	}
	req.GenerationConfig.Seed = cfg.Seed
	applyGeminiResponseFormat(req.GenerationConfig, cfg.Format)

	if thinkingAllowed(canThink) {
		control, err := ThinkingControlFor(modelName, cfg.Think)
		if err != nil {
			return geminiGenerateContentRequest{}, err
		}
		if control != nil {
			req.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
				ThinkingBudget:  control.Budget,
				ThinkingLevel:   control.Level,
				IncludeThoughts: control.IncludeThoughts,
			}
		}
	}

	return req, nil
}

func thinkingAllowed(canThink []bool) bool {
	return len(canThink) == 0 || canThink[0]
}

func applyGeminiResponseFormat(config *geminiGenerationConfig, format *modelrepo.ResponseFormat) {
	if format == nil || format.Mode == modelrepo.ResponseFormatText {
		return
	}
	config.ResponseMimeType = "application/json"
	if format.Mode == modelrepo.ResponseFormatJSONSchema {
		config.ResponseJsonSchema = format.Schema
	}
}

func geminiParameterSchema(parameters any) (json.RawMessage, error) {
	if parameters == nil {
		return nil, nil
	}
	raw, err := json.Marshal(parameters)
	if err != nil {
		return nil, fmt.Errorf("gemini: encode tool parameter schema: %w", err)
	}
	return raw, nil
}

func convertToGeminiMessages(messages []modelrepo.Message, modelName string) []geminiContent {
	out := make([]geminiContent, 0, len(messages))

	toolCallNameByID := make(map[string]string)

	for _, m := range messages {
		if m.Role == "system" {
			continue
		}

		var role string
		switch m.Role {
		case "assistant", "model":
			role = "model"
		default:
			role = "user"
		}

		for _, tc := range m.ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				toolCallNameByID[tc.ID] = tc.Function.Name
			}
		}

		if stored, ok := replayContinuation(m.Continuation, modelName); ok {
			parts := stored
			if len(out) > 0 && out[len(out)-1].Role == role {
				out[len(out)-1].Parts = mergeGeminiParts(out[len(out)-1].Parts, parts)
			} else {
				out = append(out, geminiContent{Role: role, Parts: parts})
			}
			continue
		}

		parts := make([]geminiPart, 0)

		if m.Content != "" && m.Role != "tool" {
			parts = append(parts, geminiPart{Text: m.Content})
		}

		for _, img := range m.Images {
			parts = append(parts, geminiPart{
				InlineData: &geminiInlineData{
					MimeType: img.MimeType,
					Data:     img.Data,
				},
			})
		}

		for _, a := range m.Audio {
			parts = append(parts, geminiPart{
				InlineData: &geminiInlineData{
					MimeType: a.MimeType,
					Data:     a.Data,
				},
			})
		}

		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.Function.Name == "" {
					continue
				}

				var args map[string]any
				if tc.Function.Arguments != "" {
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
						args = map[string]any{}
					}
				} else {
					args = map[string]any{}
				}

				fc := &geminiFunctionCall{
					Name: tc.Function.Name,
					Args: args,
				}

				part := geminiPart{FunctionCall: fc}
				if sig := tc.ProviderMeta[thoughtSignatureMetaKey]; sig != "" {
					part.ThoughtSignature = sig
				} else {
					part.ThoughtSignature = skipThoughtSignatureValidator
				}

				parts = append(parts, part)
			}
		}

		if m.Role == "tool" {
			fnName := ""
			if m.ToolCallID != "" {
				if n, ok := toolCallNameByID[m.ToolCallID]; ok {
					fnName = n
				}
			}

			respMap := geminiToolResponseMap(m.Content)

			parts = append(parts, geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name:     fnName,
					Response: respMap,
				},
			})
		}

		if len(parts) == 0 {
			continue
		}

		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Parts = mergeGeminiParts(out[len(out)-1].Parts, parts)
		} else {
			out = append(out, geminiContent{
				Role:  role,
				Parts: parts,
			})
		}
	}

	return out
}

func mergeGeminiParts(existing, incoming []geminiPart) []geminiPart {
	responses := make([]geminiPart, 0, len(incoming))
	merged := make([]geminiPart, 0, len(existing)+len(incoming))
	keep := func(parts []geminiPart) {
		for _, p := range parts {
			if p.FunctionResponse != nil {
				responses = append(responses, p)
				continue
			}
			merged = append(merged, p)
		}
	}
	keep(existing)
	keep(incoming)
	return append(merged, responses...)
}

func trimTrailingModelTurn(contents []geminiContent) []geminiContent {
	last := len(contents) - 1
	for last >= 0 && contents[last].Role == "model" {
		last--
	}
	if last == len(contents)-1 {
		return contents
	}
	return contents[:last+1]
}

func geminiToolResponseMap(content string) map[string]interface{} {
	if content == "" {
		return map[string]interface{}{"content": ""}
	}

	var respData any
	if err := json.Unmarshal([]byte(content), &respData); err != nil {
		return map[string]interface{}{"content": content}
	}
	if geminiContainsSchemaReference(respData) {
		return map[string]interface{}{"content": content}
	}
	respMap, ok := respData.(map[string]any)
	if !ok {
		return map[string]interface{}{"content": respData}
	}
	return respMap
}

func geminiContainsSchemaReference(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			switch k {
			case "$schema", "$defs", "definitions":
				return true
			case "$ref":
				return true
			}
			if strings.HasPrefix(k, "$ref") {
				return true
			}
			if geminiContainsSchemaReference(val) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if geminiContainsSchemaReference(val) {
				return true
			}
		}
	}
	return false
}
