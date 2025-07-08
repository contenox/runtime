// Package chat provides chat session management, message persistence,
// and LLM invocation logic.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/runtimestate"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/services/tokenizerservice"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/google/uuid"
)

// Manager coordinates chat message management and LLM execution.
type Manager struct {
	state     *runtimestate.State
	settings  kv.Repo
	tokenizer tokenizerservice.Tokenizer
}

// New creates a new Manager for chat processing.
func New(
	state *runtimestate.State,
	tokenizer tokenizerservice.Tokenizer,
	settings kv.Repo,
) *Manager {
	return &Manager{
		state:     state,
		tokenizer: tokenizer,
		settings:  settings,
	}
}

// AddInstruction inserts a system message into an existing chat.
func (m *Manager) AddInstruction(ctx context.Context, tx libdb.Exec, id string, message string) error {
	msg := taskengine.Message{
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
func (m *Manager) AppendMessage(ctx context.Context, messages []taskengine.Message, message string, role string) ([]taskengine.Message, error) {
	userMsg := taskengine.Message{
		Role:    role,
		Content: message,
	}
	messages = append(messages, userMsg)

	return messages, nil
}

// ListMessages retrieves all stored messages for a given subject ID.
func (m *Manager) ListMessages(ctx context.Context, tx libdb.Exec, subjectID string) ([]taskengine.Message, error) {
	conversation, err := store.New(tx).ListMessages(ctx, subjectID)
	if err != nil {
		return nil, err
	}
	// Convert stored messages into the api.Message slice.
	var messages []taskengine.Message
	for _, msg := range conversation {
		var parsedMsg taskengine.Message
		if err := json.Unmarshal([]byte(msg.Payload), &parsedMsg); err != nil {
			return nil, fmt.Errorf("BUG: TODO: json.Unmarshal([]byte(msg.Data): now what? %w", err)
		}
		messages = append(messages, parsedMsg)
	}

	return messages, nil
}

// AppendMessages stores a user message and the assistant response to the database.
func (m *Manager) AppendMessages(ctx context.Context, tx libdb.Exec, beginTime time.Time, subjectID string, inputMessage *taskengine.Message, responseMessage *taskengine.Message) error {
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

// CalculateContextSize estimates the token count for the chat prompt history.
func (m *Manager) CalculateContextSize(ctx context.Context, messages []taskengine.Message, baseModels ...string) (int, error) {
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
