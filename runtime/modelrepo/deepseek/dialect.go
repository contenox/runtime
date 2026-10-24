package deepseek

import (
	"fmt"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

const dialectName = "deepseek"

const dialectDocURL = "https://api-docs.deepseek.com/api/create-chat-completion"

const thinkingMember = "reasoning_content"

const (
	thinkingTypeDisabled = "disabled"
	thinkingTypeEnabled  = "enabled"
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
	delete(body, "seed")
	delete(body, "prompt_cache_key")

	if req.CacheHints != nil && req.CacheHints.SessionKey != "" {
		body["user_id"] = req.CacheHints.SessionKey
	}

	thinking, effort, err := d.thinkingControl(req)
	if err != nil {
		return err
	}
	if thinking != nil {
		body["thinking"] = thinking
	}
	delete(body, "reasoning_effort")
	if effort != "" {
		body["reasoning_effort"] = effort
	}
	if err := checkStructuredOutput(body["response_format"]); err != nil {
		return err
	}
	return checkToolChoice(body["tool_choice"], thinkingActive(req, thinking))
}

func (dialect) ThinkingMember(req modelrepo.DialectRequest) string {
	if !req.Tools {
		return ""
	}
	return thinkingMember
}

func (dialect) WireAssistantMessage(string, string) (modelrepo.WireMessage, error) { return nil, nil }

func (d dialect) thinkingControl(req modelrepo.DialectRequest) (map[string]any, string, error) {
	level, err := req.ThinkLevel()
	if err != nil {
		return nil, "", err
	}
	switch level {
	case "":
		return nil, "", nil
	case reasoning.Off:
		if req.Thinking != nil && !req.Thinking.Disabled {
			return nil, "", &modelrepo.UnsupportedDialectLevelError{Dialect: dialectName, Level: level}
		}
		return map[string]any{"type": thinkingTypeDisabled}, "", nil
	}
	effort := deepseekEffortLiteral(level)
	if !req.AllowsEffort(effort) {
		return nil, "", &modelrepo.UnsupportedDialectLevelError{Dialect: dialectName, Level: level}
	}
	return map[string]any{"type": thinkingTypeEnabled}, effort, nil
}

func deepseekEffortLiteral(level string) string {
	switch level {
	case reasoning.Minimal, reasoning.Low:
		return reasoning.Low
	case reasoning.Max:
		return reasoning.Max
	default:
		return reasoning.High
	}
}

func thinkingActive(req modelrepo.DialectRequest, thinking map[string]any) map[string]any {
	if thinking != nil {
		return thinking
	}
	if req.Thinking.Controls() {
		return map[string]any{"type": thinkingTypeEnabled}
	}
	return nil
}

func checkStructuredOutput(raw any) error {
	format, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	if kind, _ := format["type"].(string); kind == "json_schema" {
		return fmt.Errorf("deepseek declares no json_schema response format; use json_object")
	}
	return nil
}

func checkToolChoice(raw any, thinking map[string]any) error {
	if raw == nil || thinking == nil {
		return nil
	}
	if kind, _ := thinking["type"].(string); kind != thinkingTypeEnabled {
		return nil
	}
	switch choice := raw.(type) {
	case string:
		if choice == string(modelrepo.ToolChoiceRequired) {
			return fmt.Errorf("deepseek rejects the required tool choice while thinking is enabled; turn thinking off for this request")
		}
	case map[string]any:
		return fmt.Errorf("deepseek rejects a named tool choice while thinking is enabled; turn thinking off for this request")
	}
	return nil
}

func init() {
	modelrepo.RegisterChatDialect(dialect{})
}
