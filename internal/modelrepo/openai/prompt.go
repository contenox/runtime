package openai

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/internal/modelrepo"
)

type OpenAIPromptClient struct {
	openAIClient
}

func (c *OpenAIPromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string) (string, error) {
	// Convert to chat format for consistency
	messages := []modelrepo.Message{
		{Role: "system", Content: systemInstruction},
		{Role: "user", Content: prompt},
	}

	// Use the chat client to handle the prompt
	tempArg := modelrepo.WithTemperature(float64(temperature))

	response, err := c.Chat(ctx, messages, tempArg)
	if err != nil {
		return "", fmt.Errorf("OpenAI prompt execution failed: %w", err)
	}

	return response.Content, nil
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
