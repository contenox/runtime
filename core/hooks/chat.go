package hooks

import (
	"context"
	"fmt"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime/taskengine"
	"github.com/google/uuid"
)

// Chat implements taskengine.HookRepo and manages chat-related hooks.
// It enables integration of chat-based logic like appending user input,
// invoking LLMs, and persisting chat messages.
type Chat struct {
	dbInstance  libdb.DBManager
	chatManager *chat.Manager
}

// Supports returns the list of hook types supported by this hook repository.
func (h *Chat) Supports(ctx context.Context) ([]string, error) {
	return []string{
		"convert_openai_to_history",
		"convert_history_to_openai",
	}, nil
}

// NewChatHook creates a new Chat hook repository instance.
func NewChatHook(dbInstance libdb.DBManager, chatManager *chat.Manager) taskengine.HookRepo {
	return &Chat{
		dbInstance:  dbInstance,
		chatManager: chatManager,
	}
}

var _ taskengine.HookRepo = (*Chat)(nil)

func (h *Chat) Get(name string) (func(context.Context, time.Time, any, taskengine.DataType, string, *taskengine.HookCall) (int, any, taskengine.DataType, string, error), error) {
	switch name {
	case "convert_openai_to_history":
		return h.AppendOpenAIChatToChathistory, nil
	case "convert_history_to_openai":
		return h.ConvertToOpenAIResponse, nil
	default:
		return nil, fmt.Errorf("unknown hook: %s", name)
	}
}

// Exec resolves and runs the hook function based on the provided hook call.
func (h *Chat) Exec(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if hookCall.Args == nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, "", fmt.Errorf("invalid hook call: missing type")
	}
	hookFunc, err := h.Get(hookCall.Name)
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, "", fmt.Errorf("failed to get hook function: %w", err)
	}

	return hookFunc(ctx, startTime, input, dataType, transition, hookCall)
}

func (h *Chat) AppendOpenAIChatToChathistory(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if dataType != taskengine.DataTypeOpenAIChat {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("expected OpenAI chat input")
	}

	openAIHistory, ok := input.(taskengine.OpenAIChatRequest)
	if !ok {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("append to chat got %s an invalid input type, expected OpenAIChatRequest", dataType.String())
	}
	history := taskengine.ChatHistory{
		Messages: []taskengine.Message{},
	}

	for _, oarm := range openAIHistory.Messages {
		history.Messages = append(history.Messages, taskengine.Message{
			Role:    oarm.Role,
			Content: oarm.Content,
		})
	}

	return taskengine.StatusSuccess, history, taskengine.DataTypeChatHistory, "appended", nil
}

func (h *Chat) ConvertToOpenAIResponse(
	ctx context.Context,
	startTime time.Time,
	input any,
	dataType taskengine.DataType,
	transition string,
	hookCall *taskengine.HookCall,
) (int, any, taskengine.DataType, string, error) {
	if dataType != taskengine.DataTypeChatHistory {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition,
			fmt.Errorf("ConvertToOpenAIResponse expected chat history, got %s", dataType.String())
	}

	history, ok := input.(taskengine.ChatHistory)
	if !ok {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition,
			fmt.Errorf("invalid input type: expected ChatHistory")
	}

	// Find the last assistant message
	var lastAsstMessage *taskengine.Message
	for i := len(history.Messages) - 1; i >= 0; i-- {
		if history.Messages[i].Role == "assistant" {
			lastAsstMessage = &history.Messages[i]
			break
		}
	}

	if lastAsstMessage == nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition,
			fmt.Errorf("no assistant message found in history")
	}

	// Build OpenAI response
	resp := taskengine.OpenAIChatResponse{
		ID:      "chatcmpl-" + uuid.New().String(), // TODO: Implement ID generation
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   history.Model,
		Choices: []taskengine.OpenAIChatResponseChoice{
			{
				Index: 0,
				Message: taskengine.OpenAIChatRequestMessage{
					Role:    lastAsstMessage.Role,
					Content: lastAsstMessage.Content,
				},
				FinishReason: "stop",
			},
		},
		Usage: taskengine.OpenAITokenUsage{
			PromptTokens:     history.InputTokens,
			CompletionTokens: history.OutputTokens,
			TotalTokens:      history.InputTokens + history.OutputTokens,
		},
		SystemFingerprint: "fp_" + uuid.New().String()[:8],
	}

	return taskengine.StatusSuccess, resp, taskengine.DataTypeOpenAIChatResponse,
		"converted history to OpenAI response", nil
}
