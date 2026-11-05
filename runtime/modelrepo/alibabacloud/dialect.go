package alibabacloud

import (
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/reasoning"
)

const dialectName = "alibaba-cloud"

const dialectDocURL = "https://www.alibabacloud.com/help/en/model-studio/compatibility-of-openai-with-dashscope"

const enableThinkingMember = "enable_thinking"

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
	delete(body, "prompt_cache_key")
	delete(body, "prompt_cache_retention")
	delete(body, "reasoning_effort")

	enabled, err := d.thinkingControl(req)
	if err != nil {
		return err
	}
	if enabled != nil {
		body[enableThinkingMember] = *enabled
	}
	return nil
}

func (dialect) ThinkingMember(modelrepo.DialectRequest) string { return "" }

func (dialect) WireAssistantMessage(string, string) (modelrepo.WireMessage, error) { return nil, nil }

func (dialect) thinkingControl(req modelrepo.DialectRequest) (*bool, error) {
	level, err := req.ThinkLevel()
	if err != nil {
		return nil, err
	}
	if level == "" {
		return nil, nil
	}
	if level == reasoning.Off {
		if req.Thinking != nil && !req.Thinking.Disabled {
			return nil, &modelrepo.UnsupportedDialectLevelError{Dialect: dialectName, Level: level}
		}
		return boolPtr(false), nil
	}
	if req.Thinking != nil && !req.Thinking.Enabled && !req.Thinking.Adaptive {
		return nil, &modelrepo.UnsupportedDialectLevelError{Dialect: dialectName, Level: level}
	}
	return boolPtr(true), nil
}

func boolPtr(value bool) *bool { return &value }

func init() {
	modelrepo.RegisterChatDialect(dialect{})
}
