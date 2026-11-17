package modeld

import (
	"context"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
)

// ModeldPromptClient executes plain prompts through the native session transport.
type ModeldPromptClient struct {
	artifact        modeldconn.ModelRef
	modelName       string
	backendID       string
	maxOutputTokens int
	supportsThink   bool
	tracker         libtracker.ActivityTracker
}

// Prompt executes a system instruction and user prompt through the native chat path.
func (c *ModeldPromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, promptArgs ...modelrepo.PromptArgument) (string, *modelrepo.TokenUsage, error) {
	promptConfig, err := modelrepo.PromptConfigFromArgs(promptArgs...)
	if err != nil {
		return "", nil, err
	}
	client := &ModeldChatClient{artifact: c.artifact, modelName: c.modelName, backendID: c.backendID, maxOutputTokens: c.maxOutputTokens, supportsThink: c.supportsThink, tracker: c.tracker}
	chatArgs := []modelrepo.ChatArgument{modelrepo.WithTemperature(float64(temperature))}
	if promptConfig.MaxTokens != nil {
		chatArgs = append(chatArgs, modelrepo.WithMaxTokens(*promptConfig.MaxTokens))
	}
	result, err := client.Chat(ctx, []modelrepo.Message{{Role: "system", Content: systemInstruction}, {Role: "user", Content: prompt}}, chatArgs...)
	return result.Message.Content, result.Usage, err
}

var _ modelrepo.LLMPromptExecClient = (*ModeldPromptClient)(nil)
