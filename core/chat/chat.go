// Package chat provides chat session management, message persistence,
// and LLM invocation logic.
package chat

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/runtimestate"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/services/tokenizerservice"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/libs/libdb"
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
func (m *Manager) AddInstruction(ctx context.Context, tx libdb.Exec, id string, sendAt time.Time, message string) error {
	msg := taskengine.Message{
		Role:      "system",
		Content:   message,
		Timestamp: sendAt,
	}
	payload, err := json.Marshal(&msg)
	if err != nil {
		return err
	}
	messageID := msg.ID
	if messageID == "" {
		messageID = generateMessageID(id, &msg)
	}

	err = store.New(tx).AppendMessages(ctx, &store.Message{
		ID:      messageID,
		IDX:     id,
		Payload: payload,
		AddedAt: sendAt,
	})
	return err
}

// AppendMessage appends a message to an existing message slice.
func (m *Manager) AppendMessage(ctx context.Context, messages []taskengine.Message, sendAt time.Time, message string, role string) ([]taskengine.Message, error) {
	userMsg := taskengine.Message{
		Role:      role,
		Content:   message,
		Timestamp: sendAt,
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

	var messages []taskengine.Message
	for _, msg := range conversation {
		var parsedMsg taskengine.Message
		if err := json.Unmarshal([]byte(msg.Payload), &parsedMsg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal message: %w", err)
		}
		messages = append(messages, parsedMsg)
	}

	return messages, nil
}

// AppendMessages stores a user message and the assistant response to the database.
func (m *Manager) AppendMessages(ctx context.Context, tx libdb.Exec, subjectID string, inputMessage *taskengine.Message, responseMessage *taskengine.Message) error {
	if inputMessage.Timestamp.IsZero() {
		inputMessage.Timestamp = time.Now().UTC()
	}
	if responseMessage.Timestamp.IsZero() {
		responseMessage.Timestamp = time.Now().UTC()
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
	inputPayload, err := json.Marshal(inputMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal user message %w", err)
	}

	responsePayload, err := json.Marshal(responseMessage)
	if err != nil {
		return fmt.Errorf("failed to marshal assistant message data: %w", err)
	}

	inputID := inputMessage.ID
	if inputID == "" {
		inputID = generateMessageID(subjectID, inputMessage)
	}
	responseID := responseMessage.ID
	if responseID == "" {
		responseID = generateMessageID(subjectID, responseMessage)
	}

	return store.New(tx).AppendMessages(ctx,
		&store.Message{
			ID:      inputID,
			IDX:     subjectID,
			Payload: inputPayload,
			AddedAt: inputMessage.Timestamp,
		},
		&store.Message{
			ID:      responseID,
			IDX:     subjectID,
			Payload: responsePayload,
			AddedAt: responseMessage.Timestamp,
		})
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

func (m *Manager) PersistDiff(ctx context.Context, tx libdb.Exec, subjectID string, hist []taskengine.Message) error {
	if len(hist) == 0 {
		return nil
	}

	conversation, err := store.New(tx).ListMessages(ctx, subjectID)
	if err != nil {
		return err
	}

	// Create set of existing message IDs
	existingIDs := make(map[string]bool)
	for _, msg := range conversation {
		existingIDs[msg.ID] = true
	}

	var messages []*store.Message
	for _, msg := range hist {
		payload, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("failed to marshal message: %w", err)
		}
		if msg.ID == "" {
			msg.ID = generateMessageID(subjectID, &msg)
		}
		messageID := msg.ID

		if existingIDs[messageID] {
			continue
		}
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now().UTC()
		}
		messages = append(messages, &store.Message{
			ID:      messageID,
			IDX:     subjectID,
			Payload: payload,
			AddedAt: msg.Timestamp,
		})
	}

	if len(messages) > 0 {
		return store.New(tx).AppendMessages(ctx, messages...)
	}
	return nil
}

const tokenizerMaxPromptBytes = 16 * 1024 // 16 KiB

// Helper function for consistent message ID generation
func generateMessageID(subjectID string, msg *taskengine.Message) string {
	h := sha1.New()
	h.Write([]byte(subjectID))
	h.Write([]byte(msg.Content))
	h.Write([]byte(msg.Role))
	h.Write([]byte(msg.Timestamp.Format(time.RFC3339))) // Time-Clock drift issue

	return fmt.Sprintf("%x", h.Sum(nil))
}
