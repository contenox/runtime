package modelprovider

import (
	"context"
	"fmt"

	"github.com/js402/cate/core/serverops"
	"github.com/ollama/ollama/api"
)

type OllamaPromptClient struct {
	ollamaClient *api.Client // The underlying Ollama API client
	modelName    string      // The specific model this client targets (e.g., "llama3:latest")
	backendURL   string      // backend URL
}

// Prompt implements serverops.LLMPromptClient.
func (o *OllamaPromptClient) Prompt(ctx context.Context, prompt string) (string, error) {
	stream := false
	req := &api.GenerateRequest{
		Model:  o.modelName,
		Prompt: prompt,
		System: "You are a task processing engine talking to other machines. Identify the goal of the task and return the direct answer without explanation to the given task.",
		Stream: &stream, // Disable streaming to get a single response
		Options: map[string]any{
			"temperature": 0.0,
		},
	}

	var (
		content       string
		finalResponse api.GenerateResponse
	)

	err := o.ollamaClient.Generate(ctx, req, func(gr api.GenerateResponse) error {
		content += gr.Response
		if gr.Done {
			finalResponse = gr
		}
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("ollama generate API call failed for model %s: %w", o.modelName, err)
	}

	if !finalResponse.Done {
		return "", fmt.Errorf("no completion received from Ollama for model %s", o.modelName)
	}

	switch finalResponse.DoneReason {
	case "error":
		return "", fmt.Errorf("ollama generation error for model %s: %s", o.modelName, content)
	case "length":
		return "", fmt.Errorf("token limit reached for model %s (partial response: %q)", o.modelName, content)
	case "stop":
		if content == "" {
			return "", fmt.Errorf("empty content from model %s despite normal completion", o.modelName)
		}
	default:
		return "", fmt.Errorf("unexpected completion reason %q for model %s", finalResponse.DoneReason, o.modelName)
	}

	return content, nil
}

var _ serverops.LLMPromptExecClient = (*OllamaPromptClient)(nil)
