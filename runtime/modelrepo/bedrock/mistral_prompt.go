package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/contenox/runtime/runtime/modelrepo"
)

type mistralInvokePromptClient struct{ bedrockClient }

type mistralInvokeRequest struct {
	Prompt      string  `json:"prompt"`
	MaxTokens   *int    `json:"max_tokens,omitempty"`
	Temperature float32 `json:"temperature"`
}

type mistralInvokeResponse struct {
	Outputs []struct {
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"outputs"`
}

var mistralTextCompletionModels = map[string]bool{
	"mistral.mistral-7b-instruct-v0:2":   true,
	"mistral.mixtral-8x7b-instruct-v0:1": true,
	"mistral.mistral-large-2402-v1:0":    true,
	"mistral.mistral-small-2402-v1:0":    true,
}

func bedrockUsesMistralTextCompletion(modelID string) bool {
	return mistralTextCompletionModels[strings.ToLower(bedrockBaseModelID(modelID))]
}

func (c *mistralInvokePromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, promptArgs ...modelrepo.PromptArgument) (string, *modelrepo.TokenUsage, error) {
	config, err := modelrepo.PromptConfigFromArgs(promptArgs...)
	if err != nil {
		return "", nil, err
	}
	maxTokens := config.MaxTokens
	if maxTokens != nil && c.model.maxOutputTokens > 0 && *maxTokens > c.model.maxOutputTokens {
		return "", nil, fmt.Errorf("prompt output limit exceeds the model's declared maximum of %d tokens", c.model.maxOutputTokens)
	}
	if maxTokens == nil && c.model.maxOutputTokens > 0 {
		maxTokens = aws.Int(c.model.maxOutputTokens)
	}
	formattedPrompt := "<s>[INST] " + prompt + " [/INST]"
	if strings.TrimSpace(systemInstruction) != "" {
		formattedPrompt = "<s>[INST] " + strings.TrimSpace(systemInstruction) + "\n\n" + prompt + " [/INST]"
	}
	body, err := json.Marshal(mistralInvokeRequest{Prompt: formattedPrompt, MaxTokens: maxTokens, Temperature: temperature})
	if err != nil {
		return "", nil, err
	}
	reportErr, reportChange, end := c.tracker.Start(ctx, "prompt", "bedrock", "model", c.model.name)
	defer end()
	response, err := c.api.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(c.model.name),
		Body:        body,
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
	})
	if err != nil {
		err = classifyBedrockError(fmt.Errorf("bedrock mistral invoke (model=%s): %w", c.model.name, err))
		reportErr(err)
		return "", nil, err
	}
	if response == nil || len(response.Body) == 0 {
		err := errors.New("bedrock mistral response returned an empty body")
		reportErr(err)
		return "", nil, err
	}
	var decoded mistralInvokeResponse
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		reportErr(err)
		return "", nil, fmt.Errorf("decode bedrock mistral response: %w", err)
	}
	if len(decoded.Outputs) != 1 {
		err := fmt.Errorf("bedrock mistral response returned %d outputs; expected one", len(decoded.Outputs))
		reportErr(err)
		return "", nil, err
	}
	if decoded.Outputs[0].Text == "" {
		err := errors.New("bedrock mistral response returned empty text")
		reportErr(err)
		return "", nil, err
	}
	result := decoded.Outputs[0].Text
	reportChange("prompt_completed", map[string]any{"model": c.model.name, "finish_reason": decoded.Outputs[0].StopReason})
	return result, nil, nil
}

var _ modelrepo.LLMPromptExecClient = (*mistralInvokePromptClient)(nil)
