package ollama

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/google/uuid"
)

type OllamaChatClient struct {
	ollamaClient     *ollamaHTTPClient
	modelName        string
	backendURL       string
	maxOutputTokens  int
	contextLength    int
	thinking         *modelrepo.ThinkingTypes
	supportsThink    bool
	audioExtension   bool
	sessionExtension bool
	tracker          libtracker.ActivityTracker
}

func checkAudio(modelName string, audioExtension bool, messages []modelrepo.Message) error {
	if !audioExtension {
		return modelrepo.RefuseAudioInput("ollama", modelName, messages)
	}
	if err := modelrepo.ValidateAudioParts("ollama", modelName, messages); err != nil {
		return err
	}
	return modelrepo.ValidateAudioBytes("ollama", modelName, messages)
}

func toOllamaAudios(audios []modelrepo.AudioPart) []ImageData {
	if len(audios) == 0 {
		return nil
	}
	out := make([]ImageData, 0, len(audios))
	for _, audio := range audios {
		out = append(out, ImageData(audio.Data))
	}
	return out
}

func toOllamaImages(images []modelrepo.ImagePart) []ImageData {
	if len(images) == 0 {
		return nil
	}
	out := make([]ImageData, 0, len(images))
	for _, img := range images {
		out = append(out, ImageData(img.Data))
	}
	return out
}

func (c *OllamaChatClient) Chat(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (modelrepo.ChatResult, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "chat", "ollama", "model", c.modelName)
	defer end()

	if err := checkAudio(c.modelName, c.audioExtension, messages); err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	apiMessages := make([]Message, 0, len(messages))
	for _, msg := range messages {
		var apiToolCalls []ToolCall
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				argsStr := tc.Function.Arguments
				if argsStr == "" {
					argsStr = "{}"
				}
				var tcArgs ToolCallFunctionArguments
				_ = json.Unmarshal([]byte(argsStr), &tcArgs)
				apiToolCalls = append(apiToolCalls, ToolCall{
					ID: tc.ID,
					Function: ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tcArgs,
					},
				})
			}
		}
		apiMessages = append(apiMessages, Message{
			Role:       msg.Role,
			Content:    msg.Content,
			Images:     toOllamaImages(msg.Images),
			Audios:     toOllamaAudios(msg.Audio),
			ToolCalls:  apiToolCalls,
			ToolCallID: msg.ToolCallID,
		})
	}

	config := &modelrepo.ChatConfig{}
	for _, arg := range args {
		arg.Apply(config)
	}

	llamaOptions := buildOllamaOptions(config, c.maxOutputTokens, c.contextLength)
	var (
		think *ThinkValue
		err   error
	)
	if c.supportsThink {
		think, err = buildOllamaThink(config, c.thinking)
		if err != nil {
			reportErr(err)
			return modelrepo.ChatResult{}, err
		}
	}
	format, err := ollamaFormat(config.Format)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}
	stream := false

	apiTools, err := buildOllamaTools(config)
	if err != nil {
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	req := &ChatRequest{
		Model:     c.modelName,
		Messages:  apiMessages,
		Stream:    &stream,
		Format:    format,
		Think:     think,
		Options:   llamaOptions,
		Tools:     apiTools,
		KeepAlive: keepAlive(),
	}
	if config.Shift != nil {
		req.Shift = config.Shift
	}
	if config.Truncate != nil {
		req.Truncate = config.Truncate
	}
	req.Session = sessionOf(config, c.sessionExtension)

	var finalResponse ChatResponse

	err = c.ollamaClient.Chat(ctx, req, func(res ChatResponse) error {
		if res.Done {
			finalResponse = res
		}
		return nil
	})

	if err != nil {
		reportErr(err)
		wrapped := fmt.Errorf("ollama API chat request failed for model %s: %w", c.modelName, err)
		return modelrepo.ChatResult{}, modelrepo.ClassifyProviderError(wrapped, 0, "", err.Error())
	}

	if finalResponse.Message.Role == "" {
		err := fmt.Errorf("no response received from ollama for model %s", c.modelName)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	switch finalResponse.DoneReason {
	case "error":
		err := fmt.Errorf("ollama generation error for model %s: %s", c.modelName, finalResponse.Message.Content)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	case "length":
	case "", "stop":
	default:
		err := fmt.Errorf("unexpected completion reason %q for model %s", finalResponse.DoneReason, c.modelName)
		reportErr(err)
		return modelrepo.ChatResult{}, err
	}

	message := modelrepo.Message{
		Role:     finalResponse.Message.Role,
		Content:  finalResponse.Message.Content,
		Thinking: finalResponse.Message.Thinking,
	}

	var toolCalls []modelrepo.ToolCall
	for _, tc := range finalResponse.Message.ToolCalls {
		argsJSON := tc.Function.Arguments.String()

		id := tc.ID
		if id == "" {
			id = uuid.NewString()
		}

		toolCalls = append(toolCalls, modelrepo.ToolCall{
			ID:   id,
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      tc.Function.Name,
				Arguments: argsJSON,
			},
		})
	}

	result := modelrepo.ChatResult{
		Message:      message,
		ToolCalls:    toolCalls,
		Usage:        finalResponse.Metrics.Usage(),
		FinishReason: finalResponse.DoneReason,
	}

	reportChange("chat_completed", map[string]any{
		"content_length":   len(message.Content),
		"tool_calls_count": len(toolCalls),
		"done_reason":      finalResponse.DoneReason,
	})
	return result, nil
}

var _ modelrepo.LLMChatClient = (*OllamaChatClient)(nil)
