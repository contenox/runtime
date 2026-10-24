package ollama

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
)

type OllamaStreamClient struct {
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

func (c *OllamaStreamClient) Stream(ctx context.Context, messages []modelrepo.Message, args ...modelrepo.ChatArgument) (<-chan *modelrepo.StreamParcel, error) {
	reportErr, reportChange, end := c.tracker.Start(ctx, "stream", "ollama", "model", c.modelName)

	if err := checkAudio(c.modelName, c.audioExtension, messages); err != nil {
		reportErr(err)
		end()
		return nil, err
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

	stream := true
	var (
		think *ThinkValue
		err   error
	)
	if c.supportsThink {
		think, err = buildOllamaThink(config, c.thinking)
		if err != nil {
			reportErr(err)
			end()
			return nil, err
		}
	}
	format, err := ollamaFormat(config.Format)
	if err != nil {
		reportErr(err)
		end()
		return nil, err
	}
	apiTools, err := buildOllamaTools(config)
	if err != nil {
		reportErr(err)
		end()
		return nil, err
	}
	req := &ChatRequest{
		Model:     c.modelName,
		Messages:  apiMessages,
		Stream:    &stream,
		Format:    format,
		Think:     think,
		Options:   buildOllamaOptions(config, c.maxOutputTokens, c.contextLength),
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

	ch := make(chan *modelrepo.StreamParcel)
	go func() {
		defer close(ch)
		defer end()

		send := func(p *modelrepo.StreamParcel) bool {
			select {
			case ch <- p:
				return true
			case <-ctx.Done():
				return false
			}
		}

		var (
			chunkCount    int
			totalLen      int
			toolCallIndex int
			terminal      *modelrepo.StreamTerminal
		)
		err := c.ollamaClient.Chat(ctx, req, func(resp ChatResponse) error {
			if resp.Message.Content != "" {
				chunkCount++
				totalLen += len(resp.Message.Content)
				if !send(&modelrepo.StreamParcel{Data: resp.Message.Content}) {
					return ctx.Err()
				}
			}
			if resp.Message.Thinking != "" {
				if !send(&modelrepo.StreamParcel{Thinking: resp.Message.Thinking}) {
					return ctx.Err()
				}
			}
			for _, tc := range resp.Message.ToolCalls {
				argsJSON, err := json.Marshal(tc.Function.Arguments)
				if err != nil {
					continue
				}
				delta := &modelrepo.ToolCallDelta{
					Index:        toolCallIndex,
					ID:           tc.ID,
					Type:         "function",
					Name:         tc.Function.Name,
					ArgsFragment: string(argsJSON),
				}
				toolCallIndex++
				if !send(&modelrepo.StreamParcel{ToolCall: delta}) {
					return ctx.Err()
				}
			}
			if resp.Done {
				terminal = &modelrepo.StreamTerminal{
					FinishReason: resp.DoneReason,
					Terminated:   true,
					Usage:        resp.Metrics.Usage(),
				}
			}
			return nil
		})
		if err != nil {
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: fmt.Errorf("ollama stream request failed for model %s: %w", c.modelName, err)})
			return
		}
		if terminal == nil {
			err := fmt.Errorf("ollama stream for model %s ended without a done response", c.modelName)
			reportErr(err)
			send(&modelrepo.StreamParcel{Error: err})
			return
		}
		if !send(&modelrepo.StreamParcel{Terminal: terminal}) {
			return
		}

		reportChange("stream_completed", map[string]any{
			"chunk_count":  chunkCount,
			"total_length": totalLen,
		})
	}()

	return ch, nil
}

var _ modelrepo.LLMStreamClient = (*OllamaStreamClient)(nil)
