package vertex

import (
	"context"
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type vertexPromptClient struct {
	vertexClient
}

func (c *vertexPromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, promptArgs ...modelrepo.PromptArgument) (string, *modelrepo.TokenUsage, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "prompt", "vertex", "model", c.modelName)
	defer end()
	promptConfig, err := modelrepo.PromptConfigFromArgs(promptArgs...)
	if err != nil {
		return "", nil, err
	}

	messages := []modelrepo.Message{
		{Role: "user", Content: prompt},
	}
	if s := strings.TrimSpace(systemInstruction); s != "" {
		messages = append([]modelrepo.Message{{Role: "system", Content: s}}, messages...)
	}

	chat := &vertexChatClient{vertexClient: c.vertexClient}
	chatArgs := []modelrepo.ChatArgument{modelrepo.WithTemperature(float64(temperature))}
	if promptConfig.MaxTokens != nil {
		chatArgs = append(chatArgs, modelrepo.WithMaxTokens(*promptConfig.MaxTokens))
	}
	resp, err := chat.Chat(ctx, messages, chatArgs...)
	if err != nil {
		reportErr(err)
		return "", nil, fmt.Errorf("vertex prompt execution failed: %w", err)
	}

	reportChange("prompt_completed", map[string]any{
		"response_length": len(resp.Message.Content),
	})
	return resp.Message.Content, resp.Usage, nil
}

var _ modelrepo.LLMPromptExecClient = (*vertexPromptClient)(nil)
