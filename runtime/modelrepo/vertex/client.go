package vertex

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
	"github.com/contenox/runtime/runtime/modelrepo/gemini"
)

type vertexClient struct {
	baseURL         string
	publisher       string
	modelName       string
	contextLength   int
	maxOutputTokens int
	credJSON        string
	apiKey          string
	httpClient      *http.Client
	canThink        bool
	tracker         libtracker.ActivityTracker
	tokenFn         func(context.Context) (string, error)
	quotaProject    string
}

func (c *vertexClient) endpoint(method string) string {
	return strings.TrimRight(c.baseURL, "/") +
		"/publishers/" + c.publisher +
		"/models/" + c.modelName +
		":" + method
}

func (c *vertexClient) bearer(ctx context.Context) (string, error) {
	tokenFn := c.tokenFn
	if tokenFn == nil {
		tokenFn = func(ctx context.Context) (string, error) {
			return BearerTokenWithCreds(ctx, c.credJSON)
		}
	}
	return tokenFn(ctx)
}

func (c *vertexClient) authHeaders(ctx context.Context, req *http.Request) error {
	return ApplyVertexAuth(ctx, req, c.apiKey, c.quotaProject, c.bearer)
}

func (c *vertexClient) postJSON(ctx context.Context, endpoint string, request any) ([]byte, error) {
	reportErr, reportChange, end := c.tracker.Start(
		ctx,
		"http_request",
		"vertex",
		"model", c.modelName,
		"publisher", c.publisher,
		"endpoint", endpoint,
	)
	defer end()

	var payload []byte
	if request != nil {
		b, err := json.Marshal(request)
		if err != nil {
			err = fmt.Errorf("failed to marshal request: %w", err)
			reportErr(err)
			return nil, err
		}
		payload = b
	}

	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	resp, err := modelrepo.DoWithRetry(ctx, c.httpClient, func() (*http.Request, error) {
		var reqBody io.Reader
		if payload != nil {
			reqBody = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if err := c.authHeaders(ctx, req); err != nil {
			return nil, err
		}
		return req, nil
	})
	if err != nil {
		err = fmt.Errorf("HTTP request failed for model %s: %w", c.modelName, err)
		reportErr(err)
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	reportChange("http_response", map[string]any{
		"status_code": resp.StatusCode,
		"headers":     resp.Header,
	})

	if resp.StatusCode != http.StatusOK {
		var eresp vertexErrorResponse
		if jsonErr := json.Unmarshal(body, &eresp); jsonErr == nil && eresp.Error.Message != "" {
			err = fmt.Errorf("vertex API error: %d %s - %s (model=%s url=%s)",
				resp.StatusCode, eresp.Error.Status, eresp.Error.Message, c.modelName, endpoint)
			err = modelrepo.ClassifyProviderError(err, resp.StatusCode, eresp.Error.Status, eresp.Error.Message)
			reportErr(err)
			return nil, err
		}
		err = fmt.Errorf("vertex API error: %d - %s (model=%s url=%s)", resp.StatusCode, string(body), c.modelName, endpoint)
		err = modelrepo.ClassifyProviderError(err, resp.StatusCode, "", string(body))
		reportErr(err)
		return nil, err
	}

	reportChange("request_completed", nil)
	return body, nil
}

func (c *vertexClient) sendRequest(ctx context.Context, endpoint string, request interface{}, response interface{}) error {
	body, err := c.postJSON(ctx, endpoint, request)
	if err != nil {
		return err
	}
	if response != nil {
		if err := json.Unmarshal(body, response); err != nil {
			return fmt.Errorf("failed to decode response for model %s: %w", c.modelName, err)
		}
	}
	return nil
}

func buildVertexRequest(modelName string, messages []modelrepo.Message, args []modelrepo.ChatArgument, canThink ...bool) (vertexRequest, error) {
	if err := modelrepo.ValidateAudioParts("vertex", modelName, messages); err != nil {
		return vertexRequest{}, err
	}

	cfg := &modelrepo.ChatConfig{}
	for _, a := range args {
		a.Apply(cfg)
	}

	var systemInstruction *vertexContent
	filtered := make([]modelrepo.Message, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			if m.Content != "" {
				systemInstruction = &vertexContent{
					Parts: []vertexPart{{Text: m.Content}},
				}
			}
			continue
		}
		filtered = append(filtered, m)
	}

	var tools []vertexToolRequest
	if len(cfg.Tools) > 0 {
		decls := make([]vertexFunctionDeclaration, 0, len(cfg.Tools))
		for _, t := range cfg.Tools {
			if t.Type != "function" || t.Function == nil {
				continue
			}
			schema, err := vertexParameterSchema(t.Function.Parameters)
			if err != nil {
				return vertexRequest{}, err
			}
			decls = append(decls, vertexFunctionDeclaration{
				Name:                 t.Function.Name,
				Description:          t.Function.Description,
				ParametersJsonSchema: schema,
			})
		}
		if len(decls) > 0 {
			tools = append(tools, vertexToolRequest{FunctionDeclarations: decls})
		}
	}

	contents := convertToVertexContents(filtered, modelName)
	contents = trimTrailingModelTurn(contents)
	if len(contents) == 0 {
		return vertexRequest{}, fmt.Errorf("vertex: refusing to send empty contents for model %s after filtering %d message(s); provide at least one non-empty user/model/tool message, tool call, or tool response", modelName, len(messages))
	}

	req := vertexRequest{
		SystemInstruction: systemInstruction,
		Contents:          contents,
		GenerationConfig:  &vertexGenerationConfig{},
		Tools:             tools,
	}
	req.GenerationConfig.Temperature = cfg.Temperature
	req.GenerationConfig.TopP = cfg.TopP
	req.GenerationConfig.MaxOutputTokens = cfg.MaxTokens
	req.GenerationConfig.Seed = cfg.Seed
	applyVertexResponseFormat(req.GenerationConfig, cfg.Format)

	if vertexThinkingAllowed(canThink) {
		control, err := gemini.ThinkingControlFor(modelName, cfg.Think)
		if err != nil {
			return vertexRequest{}, err
		}
		if control != nil {
			req.GenerationConfig.ThinkingConfig = &vertexThinkingConfig{
				ThinkingBudget:  control.Budget,
				ThinkingLevel:   control.Level,
				IncludeThoughts: control.IncludeThoughts,
			}
		}
	}

	return req, nil
}

func applyVertexResponseFormat(config *vertexGenerationConfig, format *modelrepo.ResponseFormat) {
	if format == nil || format.Mode == modelrepo.ResponseFormatText {
		return
	}
	config.ResponseMimeType = "application/json"
	if format.Mode == modelrepo.ResponseFormatJSONSchema {
		config.ResponseJsonSchema = format.Schema
	}
}

func vertexParameterSchema(parameters any) (json.RawMessage, error) {
	if parameters == nil {
		return nil, nil
	}
	raw, err := json.Marshal(parameters)
	if err != nil {
		return nil, fmt.Errorf("vertex: encode tool parameter schema: %w", err)
	}
	return raw, nil
}

func vertexThinkingAllowed(canThink []bool) bool {
	return len(canThink) == 0 || canThink[0]
}

func convertToVertexContents(messages []modelrepo.Message, modelName string) []vertexContent {
	out := make([]vertexContent, 0, len(messages))
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
			if len(out) > 0 && out[len(out)-1].Role == role {
				out[len(out)-1].Parts = mergeVertexParts(out[len(out)-1].Parts, stored)
			} else {
				out = append(out, vertexContent{Role: role, Parts: stored})
			}
			continue
		}

		parts := make([]vertexPart, 0)

		if m.Content != "" && m.Role != "tool" {
			parts = append(parts, vertexPart{Text: m.Content})
		}

		for _, img := range m.Images {
			parts = append(parts, vertexPart{
				InlineData: &vertexInlineData{
					MimeType: img.MimeType,
					Data:     img.Data,
				},
			})
		}

		for _, a := range m.Audio {
			parts = append(parts, vertexPart{
				InlineData: &vertexInlineData{
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
				part := vertexPart{
					FunctionCall: &vertexFunctionCall{Name: tc.Function.Name, Args: args},
				}
				if sig := tc.ProviderMeta[thoughtSignatureMetaKey]; sig != "" {
					part.ThoughtSignature = sig
				} else {
					part.ThoughtSignature = skipThoughtSignatureValidator
				}
				parts = append(parts, part)
			}
		}

		if m.Role == "tool" {
			fnName := "tool_response"
			if m.ToolCallID != "" {
				if n, ok := toolCallNameByID[m.ToolCallID]; ok {
					fnName = n
				}
			}
			respMap := vertexToolResponseMap(m.Content)
			parts = append(parts, vertexPart{
				FunctionResponse: &vertexFunctionResponse{Name: fnName, Response: respMap},
			})
		}

		if len(parts) == 0 {
			continue
		}

		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Parts = mergeVertexParts(out[len(out)-1].Parts, parts)
		} else {
			out = append(out, vertexContent{Role: role, Parts: parts})
		}
	}

	return out
}

func mergeVertexParts(existing, incoming []vertexPart) []vertexPart {
	responses := make([]vertexPart, 0, len(incoming))
	merged := make([]vertexPart, 0, len(existing)+len(incoming))
	keep := func(parts []vertexPart) {
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

func trimTrailingModelTurn(contents []vertexContent) []vertexContent {
	last := len(contents) - 1
	for last >= 0 && contents[last].Role == "model" {
		last--
	}
	if last == len(contents)-1 {
		return contents
	}
	return contents[:last+1]
}

func vertexToolResponseMap(content string) map[string]interface{} {
	if content == "" {
		return map[string]interface{}{"content": ""}
	}

	var respData any
	if err := json.Unmarshal([]byte(content), &respData); err != nil {
		return map[string]interface{}{"content": content}
	}
	if vertexContainsSchemaReference(respData) {
		return map[string]interface{}{"content": content}
	}
	respMap, ok := respData.(map[string]any)
	if !ok {
		return map[string]interface{}{"content": respData}
	}
	return respMap
}

func vertexContainsSchemaReference(v any) bool {
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
			if vertexContainsSchemaReference(val) {
				return true
			}
		}
	case []any:
		for _, val := range x {
			if vertexContainsSchemaReference(val) {
				return true
			}
		}
	}
	return false
}
