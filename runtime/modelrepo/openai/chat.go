package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type OpenAIChatClient struct {
	openAIClient
}

const refusalFinishReason = "refusal"

type openAIChatCompletionResponse struct {
	Choices []openAIChatCompletionChoice `json:"choices"`
	Usage   *openAIChatCompletionUsage   `json:"usage"`
}

type openAIChatCompletionUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *openAIChatCompletionUsage) neutralUsage() *modelrepo.TokenUsage {
	if u == nil {
		return nil
	}
	total := u.TotalTokens
	if total == 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	return &modelrepo.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		ThinkingTokens:   u.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:      total,
		CacheReadTokens:  u.PromptTokensDetails.CachedTokens,
		CacheWriteTokens: u.PromptTokensDetails.CacheWriteTokens,
	}
}

type openAIChatCompletionChoice struct {
	Index        int                     `json:"index"`
	Message      openAIChatCompletionMsg `json:"message"`
	FinishReason string                  `json:"finish_reason"`
}

type openAIChatCompletionMsg struct {
	Role             string           `json:"role"`
	Content          openAIContent    `json:"content"`
	Refusal          string           `json:"refusal"`
	ReasoningContent string           `json:"reasoning_content"`
	ToolCalls        []openAIToolCall `json:"tool_calls"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIContent struct {
	text     string
	thinking string
}

func (c *openAIContent) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		c.text = text
		return nil
	}
	var chunks []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(data, &chunks); err != nil {
		return fmt.Errorf("decode assistant content: %w", err)
	}
	var answer, trace strings.Builder
	for _, chunk := range chunks {
		switch chunk.Type {
		case "text":
			answer.WriteString(chunk.Text)
		case "thinking":
			for _, inner := range chunk.Thinking {
				if inner.Type == "text" {
					trace.WriteString(inner.Text)
				}
			}
		}
	}
	c.text = answer.String()
	c.thinking = trace.String()
	return nil
}

func (c *OpenAIChatClient) Chat(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	if c.codex != nil {
		if err := modelrepo.RefuseAudioInput(c.codex.Type(), c.modelName, messages); err != nil {
			return modelrepo.ChatResult{}, err
		}
		ctx, cancel := modelrepo.NonStreamingContext(ctx)
		defer cancel()
		client := &OpenAIStreamClient{openAIClient: c.openAIClient}
		stream, err := client.Stream(ctx, messages, args...)
		if err != nil {
			return modelrepo.ChatResult{}, err
		}
		asm := modelrepo.NewStreamAssembler(c.codex.Type(), c.modelName)
		for parcel := range stream {
			if err := asm.Consume(parcel); err != nil {
				return modelrepo.ChatResult{}, err
			}
		}
		if err := ctx.Err(); err != nil {
			return modelrepo.ChatResult{}, err
		}
		r, err := asm.Result()
		if err != nil {
			return modelrepo.ChatResult{}, err
		}
		return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant", Content: r.Content, Thinking: r.Thinking, Continuation: r.Continuation}, ToolCalls: r.ToolCalls, Usage: r.Usage, FinishReason: r.FinishReason}, nil
	}
	reportErr, reportChange, end := c.tracker.Start(ctx, "chat", "openai", "model", c.modelName)
	defer end()

	usesResponses := openAIUsesResponsesEndpoint(c.modelName, chatArgsHaveTools(args))
	if !c.canAudio || usesResponses {
		if err := modelrepo.RefuseAudioInput("openai", c.modelName, messages); err != nil {
			reportErr(err)
			return modelrepo.ChatResult{}, err
		}
	}

	if usesResponses {
		req, nameMap, err := c.buildResponsesRequest(messages, args)
		if err != nil {
			return modelrepo.ChatResult{}, err
		}
		var response openAIResponse
		if err := c.sendRequest(ctx, "/responses", req, &response); err != nil {
			reportErr(err)
			return modelrepo.ChatResult{}, err
		}
		result, err := parseOpenAIResponsesResponseFromObject(nameMap, response)
		if err != nil {
			reportErr(err)
			return modelrepo.ChatResult{}, err
		}
		result.Message.Continuation = c.responseContinuation(&response)
		reportChange("chat_completed", result)
		return result, nil
	}

	req, nameMap, err := buildOpenAIRequestForDialect(c.dialect, c.modelName, messages, args, c.thinking, c.supportsThink)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	c.clampChatMaxOutputTokens(&req)
	body, err := c.chatRequestBody(req, args)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	var response openAIChatCompletionResponse

	if err := c.sendRequest(ctx, "/chat/completions", body, &response); err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	if len(response.Choices) == 0 {
		err := fmt.Errorf("no chat completion choices returned from %s for model %s", c.vendorLabel(), c.modelName)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	choice := response.Choices[0]
	if choice.Message.Refusal != "" || choice.FinishReason == refusalFinishReason {
		err := &modelrepo.RefusalError{Provider: c.providerType(), Code: choice.FinishReason, Text: choice.Message.Refusal}
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	thinking := choice.Message.ReasoningContent
	if thinking == "" {
		thinking = choice.Message.Content.thinking
	}
	if choice.Message.Content.text == "" && len(choice.Message.ToolCalls) == 0 && thinking == "" {
		err := fmt.Errorf("empty content from model %s despite normal completion. Finish reason: %s", c.modelName, choice.FinishReason)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	message := modelrepo.Message{
		Role:     choice.Message.Role,
		Content:  choice.Message.Content.text,
		Thinking: thinking,
	}

	var toolCalls []modelrepo.ToolCall
	for _, tc := range choice.Message.ToolCalls {
		name := tc.Function.Name
		if orig, ok := nameMap[name]; ok && orig != "" {
			name = orig
		}
		toolCalls = append(toolCalls, modelrepo.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	result := modelrepo.ChatResult{
		Message:      message,
		ToolCalls:    toolCalls,
		Usage:        response.Usage.neutralUsage(),
		FinishReason: choice.FinishReason,
	}
	reportChange("chat_completed", result)
	return result, nil
}

var _ modelrepo.LLMChatClient = (*OpenAIChatClient)(nil)
