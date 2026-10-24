package gemini

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/google/uuid"
)

type GeminiChatClient struct {
	geminiClient
}

func (c *GeminiChatClient) Chat(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "chat", "gemini", "model", c.modelName)
	defer end()

	req, err := buildGeminiRequest(c.modelName, messages, args, c.canThink)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	if req.GenerationConfig != nil {
		req.GenerationConfig.MaxOutputTokens = modelrepo.ClampMaxOutputTokensPtr(req.GenerationConfig.MaxOutputTokens, c.maxOutputTokens)
	}

	endpoint := fmt.Sprintf("/v1beta/models/%s:generateContent", c.modelName)
	var resp geminiGenerateContentResponse
	if err := c.sendRequest(ctx, endpoint, req, &resp); err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	if len(resp.Candidates) == 0 {
		reason := resp.PromptFeedback.BlockReason
		if reason == "" {
			reason = "unknown (check safety filters)"
		}
		err := fmt.Errorf("no candidates returned from Gemini for model %s: prompt blocked (%s)", c.modelName, reason)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	cand := resp.Candidates[0]
	if len(cand.Content.Parts) == 0 {
		reason := cand.FinishReason
		if reason == "" {
			reason = "unknown"
		}
		err := fmt.Errorf("empty candidate parts from Gemini for model %s: finish reason (%s)", c.modelName, reason)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	var (
		outText      string
		thinkingText string
		toolCalls    []modelrepo.ToolCall
	)
	for _, p := range cand.Content.Parts {
		switch {
		case p.Thought && p.Text != "":
			thinkingText += p.Text
		case p.Text != "":
			outText += p.Text
		case p.FunctionCall != nil:
			argsJSON, err := json.Marshal(p.FunctionCall.Args)
			if err != nil {
				continue
			}
			tc := modelrepo.ToolCall{
				ID:   uuid.NewString(),
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      p.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			}
			if p.ThoughtSignature != "" {
				tc.ProviderMeta = map[string]string{thoughtSignatureMetaKey: p.ThoughtSignature}
			}
			toolCalls = append(toolCalls, tc)
		}
	}

	if outText == "" && len(toolCalls) == 0 {
		if cand.FinishReason == "MALFORMED_FUNCTION_CALL" {
			err := fmt.Errorf("empty content from Gemini model %s: finish reason (%s)", c.modelName, cand.FinishReason)
			reportErr(err)
			return modelrepo.ChatResult{}, err
		}
		empty := modelrepo.ChatResult{
			Message: modelrepo.Message{
				Role:         "assistant",
				Content:      "",
				Thinking:     thinkingText,
				Continuation: responseContinuation(c.modelName, cand.Content.Parts),
			},
			Usage: resp.UsageMetadata.neutralUsage(),
		}
		reportChange("chat_completed_empty", empty)
		return empty, nil
	}

	result := modelrepo.ChatResult{
		Message: modelrepo.Message{
			Role:         "assistant",
			Content:      outText,
			Thinking:     thinkingText,
			Continuation: responseContinuation(c.modelName, cand.Content.Parts),
		},
		ToolCalls:    toolCalls,
		Usage:        resp.UsageMetadata.neutralUsage(),
		FinishReason: cand.FinishReason,
	}

	reportChange("chat_completed", result)
	return result, nil
}

var _ modelrepo.LLMChatClient = (*GeminiChatClient)(nil)
