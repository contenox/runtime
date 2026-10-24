package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

const ollamaMaxStreamBuffer = 8 * 1024 * 1024

type ollamaHTTPClient struct {
	baseURL    *url.URL
	httpClient *http.Client
	apiKey     string
}

func newOllamaHTTPClient(baseURL, apiKey string, httpClient *http.Client) (*ollamaHTTPClient, error) {
	if httpClient == nil {
		httpClient = modelrepo.SharedHTTPClient
	}
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("invalid ollama base URL %q: %w", baseURL, err)
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/api")
	if u.Path == "/" {
		u.Path = ""
	}
	return &ollamaHTTPClient{
		baseURL:    u,
		httpClient: httpClient,
		apiKey:     strings.TrimSpace(apiKey),
	}, nil
}

func (c *ollamaHTTPClient) endpointURL(endpoint string) string {
	endpoint = "/" + strings.TrimLeft(endpoint, "/")
	return c.baseURL.JoinPath("/api" + endpoint).String()
}

func (c *ollamaHTTPClient) newRequest(ctx context.Context, method, endpoint string, body io.Reader, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.endpointURL(endpoint), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

func (c *ollamaHTTPClient) do(ctx context.Context, method, endpoint string, request any, response any) error {
	var body io.Reader
	if request != nil {
		b, err := json.Marshal(request)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}

	req, err := c.newRequest(ctx, method, endpoint, body, "application/json")
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if err := ollamaAPIError(resp.StatusCode, raw); err != nil {
		return err
	}
	if len(raw) > 0 && response != nil {
		return json.Unmarshal(raw, response)
	}
	return nil
}

func (c *ollamaHTTPClient) stream(ctx context.Context, method, endpoint string, request any, onChunk func([]byte) error) error {
	var body io.Reader
	if request != nil {
		b, err := json.Marshal(request)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}

	req, err := c.newRequest(ctx, method, endpoint, body, "application/x-ndjson")
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		raw, _ := io.ReadAll(resp.Body)
		return ollamaAPIError(resp.StatusCode, raw)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), ollamaMaxStreamBuffer)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := ollamaStreamError(line); err != nil {
			return err
		}
		if err := onChunk(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func ollamaStreamError(line []byte) error {
	var frame struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &frame); err != nil || len(frame.Error) == 0 {
		return nil
	}
	raw := strings.TrimSpace(string(frame.Error))
	if raw == "" || raw == "null" {
		return nil
	}
	var message string
	if err := json.Unmarshal(frame.Error, &message); err != nil {
		message = raw
	}
	if message == "" {
		message = raw
	}
	return modelrepo.ClassifyProviderError(fmt.Errorf("ollama API returned an error: %s", message), 0, "", message)
}

func (c *ollamaHTTPClient) Chat(ctx context.Context, req *ChatRequest, fn func(ChatResponse) error) error {
	return c.stream(ctx, http.MethodPost, "/chat", req, func(line []byte) error {
		var resp ChatResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			return err
		}
		return fn(resp)
	})
}

func (c *ollamaHTTPClient) Generate(ctx context.Context, req *GenerateRequest, fn func(GenerateResponse) error) error {
	return c.stream(ctx, http.MethodPost, "/generate", req, func(line []byte) error {
		var resp GenerateResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			return err
		}
		return fn(resp)
	})
}

func (c *ollamaHTTPClient) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	var resp EmbedResponse
	if err := c.do(ctx, http.MethodPost, "/embed", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *ollamaHTTPClient) Contenox(ctx context.Context) (*ContenoxResponse, error) {
	var resp ContenoxResponse
	if err := c.do(ctx, http.MethodGet, contenoxRoute, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *ollamaHTTPClient) List(ctx context.Context) (*ListResponse, error) {
	var resp ListResponse
	if err := c.do(ctx, http.MethodGet, "/tags", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *ollamaHTTPClient) Show(ctx context.Context, req *ShowRequest) (*ShowResponse, error) {
	var resp ShowResponse
	if err := c.do(ctx, http.MethodPost, "/show", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *ollamaHTTPClient) Delete(ctx context.Context, req *DeleteRequest) error {
	return c.do(ctx, http.MethodDelete, "/delete", req, nil)
}

func ollamaAPIError(status int, raw []byte) error {
	if status < http.StatusBadRequest {
		return nil
	}

	var body struct {
		Error     string `json:"error"`
		SigninURL string `json:"signin_url,omitempty"`
	}
	_ = json.Unmarshal(raw, &body)

	msg := strings.TrimSpace(body.Error)
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	if body.SigninURL != "" {
		msg = fmt.Sprintf("%s (signin: %s)", msg, body.SigninURL)
	}
	return modelrepo.ClassifyProviderError(fmt.Errorf("ollama API returned %d: %s", status, msg), status, "", msg)
}

func buildOllamaOptions(config *modelrepo.ChatConfig, maxOutputTokens, contextLength int) map[string]any {
	opts := make(map[string]any)
	if config.Temperature != nil {
		opts["temperature"] = *config.Temperature
	}
	if config.MaxTokens != nil {
		effective, _ := modelrepo.ClampMaxOutputTokens(*config.MaxTokens, maxOutputTokens)
		opts["num_predict"] = effective
	}
	if config.TopP != nil {
		opts["top_p"] = *config.TopP
	}
	if config.Seed != nil {
		opts["seed"] = *config.Seed
	}
	if contextLength > 0 {
		opts["num_ctx"] = contextLength
	}
	return opts
}

var ollamaLegacyThinkLevels = []string{reasoning.Low, reasoning.Medium, reasoning.High, reasoning.Max}

func ollamaThinkLevel(requested string, declared []string) string {
	candidates := declared
	if len(candidates) == 0 {
		candidates = ollamaLegacyThinkLevels
	}
	want := slices.Index(reasoning.Levels, requested)
	best, bestRank := "", -1
	lowest, lowestRank := "", len(reasoning.Levels)
	for _, candidate := range candidates {
		rank := slices.Index(reasoning.Levels, candidate)
		if rank < 0 {
			continue
		}
		if rank <= want && rank > bestRank {
			best, bestRank = candidate, rank
		}
		if rank < lowestRank {
			lowest, lowestRank = candidate, rank
		}
	}
	if best != "" {
		return best
	}
	return lowest
}

func buildOllamaThink(config *modelrepo.ChatConfig, declared *modelrepo.ThinkingTypes) (*ThinkValue, error) {
	if config == nil || config.Think == nil {
		return nil, nil
	}
	level, ok, err := reasoning.NormalizeOptional(*config.Think)
	if err != nil || !ok || level == reasoning.Auto {
		return nil, nil
	}
	if declared == nil {
		declared = &modelrepo.ThinkingTypes{}
	}
	if level == reasoning.Off {
		if declared.Controls() && !declared.Disabled {
			return nil, fmt.Errorf("ollama model declares no thinking-off control: %s", ollamaThinkingDeclaration(declared))
		}
		return &ThinkValue{Value: false}, nil
	}
	chosen := ollamaThinkLevel(level, declared.EffortLevels)
	if chosen == "" {
		if declared.Enabled {
			return &ThinkValue{Value: true}, nil
		}
		return nil, fmt.Errorf("ollama model declares no level for %q: %s", level, ollamaThinkingDeclaration(declared))
	}
	return &ThinkValue{Value: chosen}, nil
}

func ollamaThinkingDeclaration(declared *modelrepo.ThinkingTypes) string {
	if declared == nil {
		return "no thinking declaration"
	}
	return fmt.Sprintf("levels %v, enabled %t, disabled %t", declared.EffortLevels, declared.Enabled, declared.Disabled)
}

func ollamaFormat(format *modelrepo.ResponseFormat) (json.RawMessage, error) {
	if format == nil || format.Mode == modelrepo.ResponseFormatText {
		return nil, nil
	}
	switch format.Mode {
	case modelrepo.ResponseFormatJSONObject:
		return json.RawMessage(`"json"`), nil
	case modelrepo.ResponseFormatJSONSchema:
		schema, err := json.Marshal(format.Schema)
		if err != nil {
			return nil, fmt.Errorf("encode response schema: %w", err)
		}
		return schema, nil
	default:
		return nil, fmt.Errorf("ollama has no wire form for response format %q", format.Mode)
	}
}

func buildOllamaTools(config *modelrepo.ChatConfig) (Tools, error) {
	if len(config.Tools) == 0 {
		return nil, nil
	}

	apiTools := make(Tools, 0, len(config.Tools))
	for _, tool := range config.Tools {
		if tool.Type == "" || tool.Function == nil || tool.Function.Name == "" {
			continue
		}

		var params ToolFunctionParameters
		if tool.Function.Parameters != nil {
			raw, err := json.Marshal(tool.Function.Parameters)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool parameters for %s: %w", tool.Function.Name, err)
			}
			if err := json.Unmarshal(raw, &params); err != nil {
				return nil, fmt.Errorf("failed to unmarshal tool parameters for %s: %w", tool.Function.Name, err)
			}
		}

		apiTools = append(apiTools, Tool{
			Type: tool.Type,
			Function: ToolFunction{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  params,
			},
		})
	}
	return apiTools, nil
}
