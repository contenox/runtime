package openai

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type OpenAIPromptClient struct {
	openAIClient
}

func (c *OpenAIPromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, promptArgs ...modelrepo.PromptArgument) (string, *modelrepo.TokenUsage, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "prompt", c.providerType(), "model", c.modelName)
	defer end()
	promptConfig, err := modelrepo.PromptConfigFromArgs(promptArgs...)
	if err != nil {
		return "", nil, err
	}

	messages := []modelrepo.Message{
		{Role: "system", Content: systemInstruction},
		{Role: "user", Content: prompt},
	}

	var args []modelrepo.ChatArgument
	if !openAIShouldOmitSamplingParams(c.modelName, "") {
		args = append(args, modelrepo.WithTemperature(float64(temperature)))
	}
	if promptConfig.MaxTokens != nil {
		args = append(args, modelrepo.WithMaxTokens(*promptConfig.MaxTokens))
	}

	chatClient := &OpenAIChatClient{openAIClient: c.openAIClient}
	resp, err := chatClient.Chat(ctx, messages, args...)
	if err != nil {
		reportErr(err)
		return "", nil, fmt.Errorf("OpenAI prompt execution failed: %w", err)
	}

	reportChange("prompt_completed", map[string]any{
		"response_length": len(resp.Message.Content),
	})
	return resp.Message.Content, resp.Usage, nil
}

func (c *OpenAIPromptClient) Chat(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.Message, error) {
	chatClient := &OpenAIChatClient{openAIClient: c.openAIClient}
	resp, err := chatClient.Chat(ctx, messages, args...)
	if err != nil {
		return modelrepo.Message{}, fmt.Errorf("OpenAI chat execution failed: %w", err)
	}

	return resp.Message, nil
}

var _ modelrepo.LLMPromptExecClient = (*OpenAIPromptClient)(nil)
