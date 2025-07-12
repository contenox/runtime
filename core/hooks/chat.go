package hooks

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libdb"
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
		"append_user_message",
		"convert_openai_to_history",
		"append_system_message",
		"persist_messages",
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
	case "append_user_message":
		return h.AppendUserInputToChathistory, nil
	case "convert_openai_to_history":
		return h.AppendOpenAIChatToChathistory, nil
	case "append_system_message":
		return h.AppendInstructionToChathistory, nil
	case "convert_history_to_openai":
		return h.ConvertToOpenAIResponse, nil
	case "persist_messages":
		return h.PersistMessages, nil
	default:
		return nil, fmt.Errorf("unknown hook: %s", name)
	}
}

// Exec resolves and runs the hook function based on the provided hook call.
func (h *Chat) Exec(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if hookCall.Args == nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, "", fmt.Errorf("invalid hook call: missing type")
	}
	hookFunc, err := h.Get(hookCall.Type)
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, "", fmt.Errorf("failed to get hook function: %w", err)
	}

	return hookFunc(ctx, startTime, input, dataType, transition, hookCall)
}

// AppendUserInputToChathistory appends a user message to the current chat history. The subject_id must already exist.
func (h *Chat) AppendUserInputToChathistory(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if dataType != taskengine.DataTypeString {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("expected string input")
	}

	inputStr, ok := input.(string)
	if !ok {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("append to chat got an invalid input type")
	}
	if inputStr == "" {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("empty input")
	}

	// Get subject ID from hook args
	subjectID, ok := hookCall.Args["subject_id"]
	if !ok || subjectID == "" {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("missing subject_id")
	}

	// Get chat history from DB
	tx := h.dbInstance.WithoutTransaction()
	messages, err := h.chatManager.ListMessages(ctx, tx, subjectID)
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("failed to load history: %w", err)
	}

	// Append new message
	updatedMessages, err := h.chatManager.AppendMessage(ctx, messages, inputStr, "user")
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("failed to append message: %w", err)
	}

	history := taskengine.ChatHistory{
		Messages: updatedMessages,
	}

	return taskengine.StatusSuccess, history, taskengine.DataTypeChatHistory, inputStr, nil
}

func (h *Chat) AppendOpenAIChatToChathistory(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if dataType != taskengine.DataTypeOpenAIChat {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("expected OpenAI chat input")
	}

	openAIHistory, ok := input.(taskengine.OpenAIChatRequest)
	if !ok {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("append to chat got an invalid input type")
	}
	history := taskengine.ChatHistory{
		Messages: []taskengine.Message{},
	}

	for _, oarm := range openAIHistory.Messages {
		history.Messages = append(history.Messages, taskengine.Message(oarm))
	}

	return taskengine.StatusSuccess, history, taskengine.DataTypeChatHistory, "appended", nil
}

// AppendInstructionToChathistory appends a system message to the current chat history. The subject_id must already exist.
func (h *Chat) AppendInstructionToChathistory(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if dataType != taskengine.DataTypeString {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("expected string input")
	}

	inputStr, ok := input.(string)
	if !ok {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("append to chat got an invalid input type")
	}
	if inputStr == "" {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("empty input")
	}

	// Get subject ID from hook args
	subjectID, ok := hookCall.Args["subject_id"]
	if !ok || subjectID == "" {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("missing subject_id")
	}

	// Get chat history from DB
	tx := h.dbInstance.WithoutTransaction()

	// Append new message
	err := h.chatManager.AddInstruction(ctx, tx, subjectID, inputStr)
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("failed to append message: %w", err)
	}
	messages, err := h.chatManager.ListMessages(ctx, tx, subjectID)
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("failed to load history: %w", err)
	}

	history := taskengine.ChatHistory{
		Messages: messages,
	}

	return taskengine.StatusSuccess, history, taskengine.DataTypeChatHistory, inputStr, nil
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
			fmt.Errorf("expected chat history, got %v", dataType)
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

// PersistMessages saves the most recent user and assistant messages to the database.
func (h *Chat) PersistMessages(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	if dataType != taskengine.DataTypeChatHistory {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition,
			fmt.Errorf("expected chat history, got %v", dataType)
	}

	history, ok := input.(taskengine.ChatHistory)
	messages := history.Messages
	if !ok || len(messages) < 2 {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("invalid chat history")
	}

	// Get subject ID from args
	subjectID, ok := hookCall.Args["subject_id"]
	if !ok || subjectID == "" {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("missing subject_id")
	}

	// Get transaction from DB
	tx := h.dbInstance.WithoutTransaction()

	// Save messages
	err := h.chatManager.AppendMessages(ctx, tx, startTime, subjectID,
		&messages[len(messages)-2], // User message
		&messages[len(messages)-1], // Assistant message
	)
	if err != nil {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("persist failed: %w", err)
	}
	history.Messages = messages
	return taskengine.StatusSuccess, history, taskengine.DataTypeChatHistory, messages[len(messages)-1].Content, nil
}
