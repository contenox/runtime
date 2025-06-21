// Package chat provides chat session management, message persistence,
// and LLM invocation logic.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/llmresolver"
	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/tokenizerservice"
	"github.com/contenox/contenox/libs/libdb"
	"github.com/contenox/contenox/libs/libroutine"
	"github.com/google/uuid"
	"github.com/ollama/ollama/api"
)

// Manager coordinates chat message management and LLM execution.
type Manager struct {
	state     *runtimestate.State
	tokenizer tokenizerservice.Tokenizer
}

// New creates a new Manager for chat processing.
func New(
	state *runtimestate.State,
	tokenizer tokenizerservice.Tokenizer,
) *Manager {
	return &Manager{
		state:     state,
		tokenizer: tokenizer,
	}
}

// AddInstruction inserts a system message into an existing chat.
func (m *Manager) AddInstruction(ctx context.Context, tx libdb.Exec, id string, message string) error {
	msg := serverops.Message{
		Role:    "system",
		Content: message,
	}
	payload, err := json.Marshal(&msg)
	if err != nil {
		return err
	}
	err = store.New(tx).AppendMessages(ctx, &store.Message{
		ID:      uuid.NewString(),
		IDX:     id,
		Payload: payload,
	})
	if err != nil {
		return err
	}

	return nil
}

// AppendMessage appends a message to an existing message slice.
func (m *Manager) AppendMessage(ctx context.Context, messages []serverops.Message, message string, role string) ([]serverops.Message, error) {
	userMsg := serverops.Message{
		Role:    role,
		Content: message,
	}
	messages = append(messages, userMsg)

	return messages, nil
}

// ListMessages retrieves all stored messages for a given subject ID.
func (m *Manager) ListMessages(ctx context.Context, tx libdb.Exec, subjectID string) ([]serverops.Message, error) {
	conversation, err := store.New(tx).ListMessages(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	// Convert stored messages into the api.Message slice.
	var messages []serverops.Message
	for _, msg := range conversation {
		var parsedMsg serverops.Message
		if err := json.Unmarshal([]byte(msg.Payload), &parsedMsg); err != nil {
			return nil, fmt.Errorf("BUG: TODO: json.Unmarshal([]byte(msg.Data): now what? %w", err)
		}
		messages = append(messages, parsedMsg)
	}

	return messages, nil
}

// AppendMessages stores a user message and the assistant response to the database.
func (m *Manager) AppendMessages(ctx context.Context, tx libdb.Exec, beginTime time.Time, subjectID string, inputMessage *serverops.Message, responseMessage *serverops.Message) error {
	if beginTime.IsZero() {
		return fmt.Errorf("beginTime cannot be zero")
	}
	if subjectID == "" {
		return fmt.Errorf("subjectID cannot be empty")
	}
	if inputMessage == nil {
		return fmt.Errorf("inputMessage cannot be nil")
	}
	if responseMessage == nil {
		return fmt.Errorf("responseMessage cannot be nil")
	}
	payload, err := json.Marshal(inputMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal user message %w", err)
	}

	jsonData, err := json.Marshal(responseMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal assistant message data: %w", err)
	}

	err = store.New(tx).AppendMessages(ctx,
		&store.Message{
			ID:      uuid.NewString(),
			IDX:     subjectID,
			Payload: payload,
			AddedAt: beginTime,
		},
		&store.Message{
			ID:      uuid.New().String(),
			IDX:     subjectID,
			Payload: jsonData,
			AddedAt: time.Now().UTC(),
		})
	if err != nil {
		return err
	}

	return nil
}

// ChatExec runs the chat history through a selected LLM and returns the assistant's response.
// Validates that the last message is from the user and uses the preferred model names.
//
// Returns:
//   - Assistant response message
//   - Number of input tokens
//   - Number of output tokens
func (m *Manager) ChatExec(ctx context.Context, messages []serverops.Message, contextLength int, preferredModelNames ...string) (*serverops.Message, int, int, string, error) {
	if len(messages) == 0 {
		return nil, 0, 0, "", errors.New("no messages provided")
	}
	if messages[len(messages)-1].Role != "user" && messages[len(messages)-1].Role != "system" {
		return nil, 0, 0, "", fmt.Errorf("last message must be from user or system was %v", messages[len(messages)-1].Role)
	}
	inputtokens := 0
	convertedMessage := make([]api.Message, len(messages))
	for i, msg := range messages {
		convertedMessage[i] = api.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
		var err2 error
		retryFunc := func(ctx context.Context) error {
			inputtokens, err2 = m.tokenizer.CountTokens(ctx, "phi-3", msg.Content)
			if err2 != nil {
				fmt.Printf("Retrying token count due to error: %v\n", err2)
			}
			return err2
		}
		err := libroutine.NewRoutine(6, time.Second*10).ExecuteWithRetry(ctx, time.Second, 10, retryFunc)
		if err != nil {
			return nil, 0, 0, "", fmt.Errorf("failed to count tokens %w %w", err, err2)
		}

	}
	chatClient, model, err := llmresolver.Chat(ctx, llmresolver.Request{
		ContextLength: contextLength,
		ModelNames:    preferredModelNames,
	}, modelprovider.ModelProviderAdapter(ctx, m.state.Get(ctx)), llmresolver.Randomly)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to resolve backend %w", err)
	}
	responseMessage, err := chatClient.Chat(ctx, messages)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to chat %w", err)
	}
	outputtokens, err := m.tokenizer.CountTokens(ctx, "phi-3", responseMessage.Content)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to count tokens %w", err)
	}
	assistantMsgData := serverops.Message{
		Role:    responseMessage.Role,
		Content: responseMessage.Content,
	}
	return &assistantMsgData, inputtokens, outputtokens, model, nil
}

// CalculateContextSize estimates the token count for the chat prompt history.
func (m *Manager) CalculateContextSize(ctx context.Context, messages []serverops.Message, baseModels ...string) (int, error) {
	var prompt string
	for _, m := range messages {
		if m.Role == "user" {
			prompt = prompt + "\n" + m.Content
		}
	}
	var selectedModel string
	for _, model := range baseModels {
		optimal, err := m.tokenizer.OptimalModel(ctx, model)
		if err != nil {
			return 0, fmt.Errorf("BUG: failed to get optimal model for %q: %w", model, err)
		}
		// TODO: For now, pick the first valid one.
		selectedModel = optimal
		break
	}
	// If no base models were provided, use a fallback.
	if selectedModel == "" {
		selectedModel = "tiny"
	}
	count := 0
	for start := 0; start < len(prompt); start += tokenizerMaxPromptBytes {
		end := min(start+tokenizerMaxPromptBytes, len(prompt))
		chunk := prompt[start:end]
		tokens, err := m.tokenizer.CountTokens(ctx, selectedModel, chunk)
		if err != nil {
			return 0, fmt.Errorf("failed to estimate context size at chunk [%d:%d]: %w", start, end, err)
		}
		count += tokens
	}

	return count, nil
}

const tokenizerMaxPromptBytes = 16 * 1024 // 16 KiB
