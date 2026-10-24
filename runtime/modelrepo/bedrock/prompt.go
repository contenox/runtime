package bedrock

import (
	"context"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type bedrockPromptClient struct{ bedrockClient }

func (c *bedrockPromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, promptArgs ...modelrepo.PromptArgument) (string, *modelrepo.TokenUsage, error) {
	promptConfig, err := modelrepo.PromptConfigFromArgs(promptArgs...)
	if err != nil {
		return "", nil, err
	}
	msgs := []modelrepo.Message{{Role: "user", Content: prompt}}
	if s := strings.TrimSpace(systemInstruction); s != "" {
		msgs = append([]modelrepo.Message{{Role: "system", Content: s}}, msgs...)
	}
	chat := &bedrockChatClient{c.bedrockClient}
	chatArgs := []modelrepo.ChatArgument{modelrepo.WithTemperature(float64(temperature))}
	if promptConfig.MaxTokens != nil {
		chatArgs = append(chatArgs, modelrepo.WithMaxTokens(*promptConfig.MaxTokens))
	}
	res, err := chat.Chat(ctx, msgs, chatArgs...)
	if err != nil {
		return "", nil, err
	}
	return res.Message.Content, res.Usage, nil
}

var _ modelrepo.LLMPromptExecClient = (*bedrockPromptClient)(nil)
