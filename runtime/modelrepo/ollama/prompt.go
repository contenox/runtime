package ollama

import (
	"context"
	"fmt"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type OllamaPromptClient struct {
	ollamaClient    *ollamaHTTPClient
	modelName       string
	backendURL      string
	maxOutputTokens int
	contextLength   int
	thinking        *modelrepo.ThinkingTypes
	supportsThink   bool
	tracker         libtracker.ActivityTracker
}

func (o *OllamaPromptClient) Prompt(ctx context.Context, systemInstruction string, temperature float32, prompt string, promptArgs ...modelrepo.PromptArgument) (string, *modelrepo.TokenUsage, error) {
	reportErr, reportChange, end := o.tracker.Start(ctx, "prompt", "ollama", "model", o.modelName)
	defer end()
	promptConfig, err := modelrepo.PromptConfigFromArgs(promptArgs...)
	if err != nil {
		return "", nil, err
	}

	stream := false
	config := &modelrepo.ChatConfig{}
	modelrepo.WithTemperature(float64(temperature)).Apply(config)
	if promptConfig.MaxTokens != nil {
		modelrepo.WithMaxTokens(*promptConfig.MaxTokens).Apply(config)
	}
	var think *ThinkValue
	if o.supportsThink {
		think, err = buildOllamaThink(config, o.thinking)
		if err != nil {
			return "", nil, err
		}
	}
	req := &GenerateRequest{
		Model:     o.modelName,
		Prompt:    prompt,
		System:    systemInstruction,
		Stream:    &stream,
		Options:   buildOllamaOptions(config, o.maxOutputTokens, o.contextLength),
		Think:     think,
		KeepAlive: keepAlive(),
	}

	var (
		content       string
		finalResponse GenerateResponse
	)

	err = o.ollamaClient.Generate(ctx, req, func(gr GenerateResponse) error {
		content += gr.Response
		if gr.Done {
			finalResponse = gr
		}
		return nil
	})
	if err != nil {
		reportErr(err)
		return "", nil, fmt.Errorf("ollama generate API call failed for model %s: %w", o.modelName, err)
	}

	if !finalResponse.Done {
		err := fmt.Errorf("no completion received from ollama for model %s", o.modelName)
		reportErr(err)
		return "", nil, err
	}

	switch finalResponse.DoneReason {
	case "error":
		err := fmt.Errorf("ollama generation error for model %s: %s", o.modelName, content)
		reportErr(err)
		return "", nil, err
	case "length":
		err := fmt.Errorf("token limit reached for model %s (partial response: %q)", o.modelName, content)
		reportErr(err)
		return "", nil, err
	case "stop":
		if content == "" {
			err := fmt.Errorf("empty content from model %s despite normal completion", o.modelName)
			reportErr(err)
			return "", nil, err
		}
	default:
		err := fmt.Errorf("unexpected completion reason %q for model %s", finalResponse.DoneReason, o.modelName)
		reportErr(err)
		return "", nil, err
	}

	reportChange("prompt_completed", map[string]any{
		"content_length": len(content),
		"done_reason":    finalResponse.DoneReason,
	})
	return content, finalResponse.Metrics.Usage(), nil
}

var _ modelrepo.LLMPromptExecClient = (*OllamaPromptClient)(nil)
