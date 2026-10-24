package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	msgcodec "github.com/contenox/runtime/runtime/modelrepo/codec/messages"
	"github.com/contenox/runtime/runtime/reasoning"
)

const (
	defaultBaseURL      = "https://api.anthropic.com"
	anthropicAPIVersion = "2023-06-01"
)

type anthropicClient struct {
	baseURL         string
	apiKey          string
	modelName       string
	httpClient      *http.Client
	canThink        bool
	thinking        *modelrepo.ThinkingTypes
	maxOutputTokens int
	tracker         libtracker.ActivityTracker
}

func chatConfigFromArgs(args []modelrepo.ChatArgument) (*modelrepo.ChatConfig, error) {
	cfg := &modelrepo.ChatConfig{}
	for _, a := range args {
		a.Apply(cfg)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *anthropicClient) newRequest(ctx context.Context, path string, body []byte, stream bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+path, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	return req, nil
}

func (c *anthropicClient) post(ctx context.Context, path string, request any) ([]byte, error) {
	b, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	ctx, cancel := modelrepo.NonStreamingContext(ctx)
	defer cancel()
	resp, err := modelrepo.DoWithRetry(ctx, c.httpClient, func() (*http.Request, error) {
		return c.newRequest(ctx, path, b, false)
	})
	if err != nil {
		return nil, fmt.Errorf("anthropic: request failed for model %s: %w", c.modelName, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, anthropicAPIError(resp.StatusCode, body, c.modelName)
	}
	return body, nil
}

func anthropicAPIError(status int, body []byte, modelName string) error {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	code, message := "", strings.TrimSpace(string(body))
	if json.Unmarshal(body, &payload) == nil && payload.Error.Message != "" {
		code, message = payload.Error.Type, payload.Error.Message
	}
	err := fmt.Errorf("anthropic API error: %d - %s (model=%s)", status, strings.TrimSpace(string(body)), modelName)
	return modelrepo.ClassifyProviderError(err, status, code, message)
}

func (c *anthropicClient) openStream(ctx context.Context, path string, request any) (*http.Response, error) {
	b, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal stream request: %w", err)
	}
	req, err := c.newRequest(ctx, path, b, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: stream request failed for model %s: %w", c.modelName, err)
	}
	if resp.StatusCode != http.StatusOK {
		bd, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, anthropicAPIError(resp.StatusCode, bd, c.modelName)
	}
	return resp, nil
}

func (c *anthropicClient) applyAnthropicThinking(req *msgcodec.Request, cfg *modelrepo.ChatConfig) error {
	if req == nil || cfg == nil || cfg.Think == nil {
		return nil
	}
	level, ok, err := reasoning.NormalizeOptional(*cfg.Think)
	if err != nil || !ok || level == reasoning.Auto {
		return err
	}
	declared := c.thinking

	if level == reasoning.Off {
		if declared != nil && !declared.Disabled {
			return nil
		}
		req.Thinking = &msgcodec.ThinkingConfig{Type: "disabled"}
		return nil
	}

	setEffort(req, c.effectiveEffort(level))
	if declared == nil {
		budget, err := anthropicThinkingBudget(level, req.MaxTokens)
		if err != nil {
			return fmt.Errorf("anthropic: model %s thinking level %q: %w", c.modelName, level, err)
		}
		req.Thinking = &msgcodec.ThinkingConfig{Type: "enabled", BudgetTokens: budget}
		return nil
	}
	switch {
	case declared.Adaptive:
		req.Thinking = &msgcodec.ThinkingConfig{Type: "adaptive", Display: "summarized"}
	case declared.Enabled:
		budget, err := anthropicThinkingBudget(level, req.MaxTokens)
		if err != nil {
			return fmt.Errorf("anthropic: model %s thinking level %q: %w", c.modelName, level, err)
		}
		req.Thinking = &msgcodec.ThinkingConfig{Type: "enabled", BudgetTokens: budget}
	case declared.Controls():
	default:
		return fmt.Errorf("anthropic: model %s declares no thinking control for level %q", c.modelName, level)
	}
	return nil
}

func (c *anthropicClient) effectiveEffort(level string) string {
	requested := anthropicEffort(level)
	if requested == "" {
		return ""
	}
	declared := c.thinking
	if declared == nil || declared.SupportsEffort(requested) {
		return requested
	}
	for _, candidate := range effortDescending(requested) {
		if declared.SupportsEffort(candidate) {
			return candidate
		}
	}
	return ""
}

func effortDescending(level string) []string {
	order := []string{reasoning.Max, reasoning.XHigh, reasoning.High, reasoning.Medium, reasoning.Low}
	for i, candidate := range order {
		if candidate == level {
			return order[i:]
		}
	}
	return nil
}

func setEffort(req *msgcodec.Request, effort string) {
	if effort == "" {
		return
	}
	if req.OutputConfig == nil {
		req.OutputConfig = &msgcodec.OutputConfig{}
	}
	req.OutputConfig.Effort = effort
}

func anthropicThinkingBudget(level string, maxTokens int) (int, error) {
	budget := 0
	switch level {
	case reasoning.Minimal, reasoning.Low:
		budget = msgcodec.MinThinkingBudget
	case reasoning.Medium:
		budget = 2048
	case reasoning.High:
		budget = 4096
	case reasoning.XHigh, reasoning.Max:
		budget = 8192
	}
	if budget < msgcodec.MinThinkingBudget {
		return 0, fmt.Errorf("no thinking budget for level %q", level)
	}
	if maxTokens < msgcodec.MinThinkingBudget+1 {
		return 0, fmt.Errorf("max_tokens %d leaves no room for a %d-token budget below it",
			maxTokens, msgcodec.MinThinkingBudget)
	}
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	return budget, nil
}

func anthropicEffort(level string) string {
	switch level {
	case reasoning.Minimal:
		return reasoning.Low
	case reasoning.Low, reasoning.Medium, reasoning.High, reasoning.XHigh, reasoning.Max:
		return level
	default:
		return ""
	}
}

type anthropicChatClient struct{ anthropicClient }

func (c *anthropicChatClient) Chat(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "chat", "anthropic", "model", c.modelName)
	defer end()

	if err := modelrepo.RefuseAudioInput("anthropic", c.modelName, messages); err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	cfg, err := chatConfigFromArgs(args)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	req, nameMap, err := msgcodec.Build(messages, cfg)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	req.Model = c.modelName
	req.MaxTokens, _ = modelrepo.ClampMaxOutputTokens(req.MaxTokens, c.maxOutputTokens)
	if c.canThink {
		if err := c.applyAnthropicThinking(&req, cfg); err != nil {
			reportErr(err)
			return modelrepo.ChatResult{}, err
		}
	}
	c.applySamplingPolicy(&req)
	if err := checkForcedToolUseWithManualThinking(&req); err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	stripThinkingUnlessEnabled(&req)

	raw, err := c.post(ctx, "/v1/messages", req)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	res, err := msgcodec.DecodeResponse(raw, nameMap)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	reportChange("chat_completed", res)
	return res, nil
}

func (c *anthropicClient) applySamplingPolicy(req *msgcodec.Request) {
	if AnthropicStripsSamplingParams(c.thinking) {
		req.Temperature = nil
		req.TopP = nil
		return
	}
	if req.Thinking == nil || req.Thinking.Type == "disabled" {
		return
	}
	req.Temperature = nil
	if req.TopP != nil && (*req.TopP < samplingTopPFloor || *req.TopP > 1) {
		req.TopP = nil
	}
}

const samplingTopPFloor = 0.95

func checkForcedToolUseWithManualThinking(req *msgcodec.Request) error {
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.ToolChoice == nil {
		return nil
	}
	if req.ToolChoice.Type == "any" || req.ToolChoice.Type == "tool" {
		return fmt.Errorf("anthropic: forced tool use (tool_choice %q) is incompatible with a caller-set thinking budget; request adaptive thinking or tool_choice auto", req.ToolChoice.Type)
	}
	return nil
}

func AnthropicStripsSamplingParams(declared *modelrepo.ThinkingTypes) bool {
	return declared != nil && declared.Adaptive && !declared.Enabled
}

func stripThinkingUnlessEnabled(req *msgcodec.Request) {
	if req.Thinking == nil || req.Thinking.Type == "disabled" {
		msgcodec.StripThinkingBlocks(req)
	}
}

type anthropicStreamClient struct{ anthropicClient }

func (c *anthropicStreamClient) Stream(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (<-chan *modelrepo.StreamParcel, error) {
	if err := modelrepo.RefuseAudioInput("anthropic", c.modelName, messages); err != nil {
		return nil, err
	}

	cfg, err := chatConfigFromArgs(args)
	if err != nil {
		return nil, err
	}
	req, nameMap, err := msgcodec.Build(messages, cfg)
	if err != nil {
		return nil, err
	}
	req.Model = c.modelName
	req.MaxTokens, _ = modelrepo.ClampMaxOutputTokens(req.MaxTokens, c.maxOutputTokens)
	if c.canThink {
		if err := c.applyAnthropicThinking(&req, cfg); err != nil {
			return nil, err
		}
	}
	c.applySamplingPolicy(&req)
	if err := checkForcedToolUseWithManualThinking(&req); err != nil {
		return nil, err
	}
	stripThinkingUnlessEnabled(&req)
	req.Stream = true
	dec := msgcodec.NewStreamDecoder(nameMap)

	reportErr, reportChange, end := c.tracker.Start(ctx, "stream", "anthropic", "model", c.modelName)
	resp, err := c.openStream(ctx, "/v1/messages", req)
	if err != nil {
		reportErr(err)
		end()
		return nil, err
	}

	parcels := make(chan *modelrepo.StreamParcel)
	go func() {
		defer close(parcels)
		defer resp.Body.Close()
		defer end()

		send := func(p *modelrepo.StreamParcel) bool {
			select {
			case parcels <- p:
				return true
			case <-ctx.Done():
				return false
			}
		}

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		var chunkCount int
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			ps, derr := dec.DecodeLine([]byte(payload))
			if derr != nil {
				derr = fmt.Errorf("anthropic stream failed for model %s: %w", c.modelName, derr)
				reportErr(derr)
				send(&modelrepo.StreamParcel{Error: derr})
				return
			}
			for _, p := range ps {
				chunkCount++
				if !send(p) {
					return
				}
			}
		}
		if err := sc.Err(); err != nil && err != io.EOF {
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: fmt.Errorf("anthropic: stream read: %w", err)})
			return
		}
		term := dec.Finish()
		if !send(term) {
			return
		}
		if term.Terminal != nil && term.Terminal.FinishReason == "refusal" {
			err := &modelrepo.RefusalError{Provider: "anthropic", Text: term.Terminal.Refusal}
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: err})
			return
		}
		reportChange("stream_completed", map[string]any{"chunk_count": chunkCount})
	}()
	return parcels, nil
}

type anthropicPromptClient struct{ anthropicClient }

func (c *anthropicPromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, promptArgs ...modelrepo.PromptArgument) (string, *modelrepo.TokenUsage, error) {
	promptConfig, err := modelrepo.PromptConfigFromArgs(promptArgs...)
	if err != nil {
		return "", nil, err
	}
	msgs := []modelrepo.Message{{Role: "user", Content: prompt}}
	if s := strings.TrimSpace(systemInstruction); s != "" {
		msgs = append([]modelrepo.Message{{Role: "system", Content: s}}, msgs...)
	}
	chat := &anthropicChatClient{anthropicClient: c.anthropicClient}
	var chatArgs []modelrepo.ChatArgument
	if !AnthropicStripsSamplingParams(c.thinking) {
		chatArgs = append(chatArgs, modelrepo.WithTemperature(float64(temperature)))
	}
	if promptConfig.MaxTokens != nil {
		chatArgs = append(chatArgs, modelrepo.WithMaxTokens(*promptConfig.MaxTokens))
	}
	res, err := chat.Chat(ctx, msgs, chatArgs...)
	if err != nil {
		return "", nil, err
	}
	return res.Message.Content, res.Usage, nil
}

var (
	_ modelrepo.LLMChatClient       = (*anthropicChatClient)(nil)
	_ modelrepo.LLMStreamClient     = (*anthropicStreamClient)(nil)
	_ modelrepo.LLMPromptExecClient = (*anthropicPromptClient)(nil)
)
