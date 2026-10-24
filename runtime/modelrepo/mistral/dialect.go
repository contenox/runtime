package mistral

import (
	"fmt"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

const dialectName = "mistral"

const dialectDocURL = "https://docs.mistral.ai/api/endpoint/chat"

const maxTemperature = 1.5

const (
	reasoningEffortNone = "none"
	reasoningEffortLow  = "low"
	reasoningEffortHigh = "high"
	reasoningEffortMax  = "max"
)

type dialect struct{}

func (dialect) Name() string { return dialectName }

func (dialect) DocURL() string { return dialectDocURL }

func (dialect) OwnsReasoning() bool { return true }

func (d dialect) RewriteBody(body map[string]any, req modelrepo.DialectRequest) error {
	if _, ok := body["max_completion_tokens"]; ok {
		if _, declared := body["max_tokens"]; !declared {
			body["max_tokens"] = body["max_completion_tokens"]
		}
		delete(body, "max_completion_tokens")
	}
	if seed, ok := body["seed"]; ok {
		body["random_seed"] = seed
		delete(body, "seed")
	}
	if err := checkTemperature(body["temperature"]); err != nil {
		return err
	}
	delete(body, "stream_options")
	delete(body, "reasoning_effort")
	ensureToolParameters(body)

	effort, err := reasoningEffort(req)
	if err != nil {
		return err
	}
	if effort != "" {
		body["reasoning_effort"] = effort
	}
	return nil
}

func (dialect) ThinkingMember(modelrepo.DialectRequest) string { return "" }

func (dialect) WireAssistantMessage(content, thinking string) (modelrepo.WireMessage, error) {
	if thinking == "" {
		return nil, nil
	}
	chunks := []map[string]any{
		{"type": "thinking", "thinking": []map[string]any{{"type": "text", "text": thinking}}},
	}
	if content != "" {
		chunks = append(chunks, map[string]any{"type": "text", "text": content})
	}
	return modelrepo.WireMessage{"content": chunks}, nil
}

func checkTemperature(raw any) error {
	temperature, ok := raw.(float64)
	if !ok || temperature <= maxTemperature {
		return nil
	}
	return fmt.Errorf("mistral accepts a temperature of at most %v, got %v", maxTemperature, temperature)
}

func ensureToolParameters(body map[string]any) {
	tools, ok := body["tools"].([]any)
	if !ok {
		return
	}
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		if _, declared := function["parameters"]; !declared {
			function["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
	}
}

func reasoningEffort(req modelrepo.DialectRequest) (string, error) {
	level, err := req.ThinkLevel()
	if err != nil {
		return "", err
	}
	switch level {
	case "":
		return "", nil
	case reasoning.Off:
		if req.Thinking == nil || req.Thinking.Disabled {
			return reasoningEffortNone, nil
		}
		return "", &modelrepo.UnsupportedDialectLevelError{Dialect: dialectName, Level: level}
	}
	if req.Thinking == nil || len(req.Thinking.EffortLevels) == 0 {
		return reasoningEffortHigh, nil
	}
	effort := mistralEffortLiteral(level)
	if !req.AllowsEffort(effort) {
		return "", &modelrepo.UnsupportedDialectLevelError{Dialect: dialectName, Level: level}
	}
	return effort, nil
}

func mistralEffortLiteral(level string) string {
	switch level {
	case reasoning.Minimal, reasoning.Low:
		return reasoningEffortLow
	case reasoning.Max:
		return reasoningEffortMax
	default:
		return reasoningEffortHigh
	}
}

func init() {
	modelrepo.RegisterChatDialect(dialect{})
}
