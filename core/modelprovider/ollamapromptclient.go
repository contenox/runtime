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
		Stream: &stream, // Disable streaming to get a single response
	}

	var content string

	// Execute the Generate request and handle responses via the callback
	err := o.ollamaClient.Generate(ctx, req, func(gr api.GenerateResponse) error {
		content += gr.Response // Aggregate response content
		if gr.Done {
			println(gr.DoneReason)
			return nil
		}
		// if gr.Done && gr.DoneReason != "" {
		// 	generateErr = fmt.Errorf("generation error: %s", gr.DoneReason)
		// }
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("ollama generate API call failed: %w", err)
	}

	// Ensure content is not empty
	if content == "" {
		return "", fmt.Errorf("ollama generate returned empty content for model %s", o.modelName)
	}

	return content, nil
}

var _ serverops.LLMPromptClient = (*OllamaPromptClient)(nil)
