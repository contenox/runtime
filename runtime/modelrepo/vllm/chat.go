package vllm

import (
	"context"
	"fmt"
	"net/http"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type VLLMChatClient struct {
	vLLMClient
}

func NewVLLMChatClient(ctx context.Context, baseURL, modelName string, contextLength, maxOutputTokens int, httpClient *http.Client, apiKey string, canThink, canAudio bool, tracker libtracker.ActivityTracker) (modelrepo.LLMChatClient, error) {
	if httpClient == nil {
		httpClient = modelrepo.SharedHTTPClient
	}
	if tracker == nil {
		tracker = libtracker.NoopTracker{}
	}
	client := &VLLMChatClient{
		vLLMClient: vLLMClient{
			baseURL:         normalizeBaseURL(baseURL),
			httpClient:      httpClient,
			modelName:       modelName,
			maxOutputTokens: maxOutputTokens,
			canThink:        canThink,
			canAudio:        canAudio,
			apiKey:          apiKey,
			tracker:         tracker,
		},
	}

	client.maxTokens = min(contextLength, 2048)
	return client, nil
}

func (c *VLLMChatClient) Chat(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "chat", "vllm", "model", c.modelName)
	defer end()

	if err := c.checkAudio(messages); err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	request, nameMap := buildChatRequest(c.modelName, messages, args, c.canThink)
	c.clampChatRequest(&request)

	var response chatResponse

	if err := c.sendRequest(ctx, "/v1/chat/completions", request, &response); err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	if len(response.Choices) == 0 {
		err := fmt.Errorf("no completion choices returned")
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	choice := response.Choices[0]

	message := modelrepo.Message{
		Role:     choice.Message.Role,
		Content:  choice.Message.Content,
		Thinking: choice.Message.Thinking(),
	}

	toolCalls := convertChatToolCalls(choice.Message.ToolCalls, nameMap)

	result := modelrepo.ChatResult{
		Message:   message,
		ToolCalls: toolCalls,
		Usage:     response.Usage.Usage(),
	}

	result.FinishReason = choice.FinishReason
	switch choice.FinishReason {
	case "stop", "tool_calls", "length":
		reportChange("chat_completed", map[string]any{
			"finish_reason":    choice.FinishReason,
			"content_length":   len(message.Content),
			"thinking_length":  len(message.Thinking),
			"tool_calls_count": len(toolCalls),
		})
		return result, nil
	case "content_filter":
		err := fmt.Errorf("content filtered")
		reportErr(err)
		return modelrepo.ChatResult{}, err
	default:
		err := fmt.Errorf("unexpected completion reason: %s", choice.FinishReason)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
}

var _ modelrepo.LLMChatClient = (*VLLMChatClient)(nil)
