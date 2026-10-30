package chatservice

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/messagestore"
	"github.com/contenox/runtime/runtime/taskengine"
)

type Manager struct {
	workspaceID string
}

func NewManager(workspaceID string) *Manager {
	return &Manager{workspaceID: workspaceID}
}

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
	return messagestore.New(tx, m.workspaceID).AppendMessages(ctx, &messagestore.Message{
		ID:      messageID,
		IDX:     id,
		Payload: payload,
		AddedAt: sendAt,
	})
}

func (m *Manager) AppendMessage(_ context.Context, messages []taskengine.Message, sendAt time.Time, message string, role string) ([]taskengine.Message, error) {
	messages = append(messages, taskengine.Message{
		Role:      role,
		Content:   message,
		Timestamp: sendAt,
	})
	return messages, nil
}

func (m *Manager) ListMessages(ctx context.Context, tx libdb.Exec, subjectID string) ([]taskengine.Message, error) {
	conversation, err := messagestore.New(tx, m.workspaceID).ListMessages(ctx, subjectID)
	if err != nil {
		return nil, err
	}

	var messages []taskengine.Message
	for _, msg := range conversation {
		var parsedMsg taskengine.Message
		if err := json.Unmarshal(msg.Payload, &parsedMsg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal message: %w", err)
		}
		messages = append(messages, parsedMsg)
	}
	return messages, nil
}

func (m *Manager) PersistDiff(ctx context.Context, tx libdb.Exec, subjectID string, hist []taskengine.Message) error {
	if len(hist) == 0 {
		return nil
	}

	conversation, err := messagestore.New(tx, m.workspaceID).ListMessages(ctx, subjectID)
	if err != nil {
		return err
	}

	existingIDs := make(map[string]bool)
	var lastAdded time.Time
	for _, msg := range conversation {
		existingIDs[msg.ID] = true
		if msg.AddedAt.After(lastAdded) {
			lastAdded = msg.AddedAt
		}
	}

	var newMessages []*messagestore.Message
	for _, msg := range hist {
		if msg.ID == "" {
			msg.ID = generateMessageID(subjectID, &msg)
		}
		if existingIDs[msg.ID] {
			continue
		}
		existingIDs[msg.ID] = true
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now().UTC()
		}
		// History is read back ordered by (added_at, seq, id) and the store
		// assigns seq in append order, so messages sharing a timestamp keep the
		// order they were written in without touching their timestamps. A
		// message stamped before what is already stored would still sort ahead
		// of it by added_at, so clamp it to the stream's latest timestamp (a
		// tie, resolved by seq) instead of letting it jump the queue.
		if msg.Timestamp.Before(lastAdded) {
			msg.Timestamp = lastAdded
		}
		lastAdded = msg.Timestamp
		payload, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("failed to marshal message: %w", err)
		}
		newMessages = append(newMessages, &messagestore.Message{
			ID:      msg.ID,
			IDX:     subjectID,
			Payload: payload,
			AddedAt: msg.Timestamp,
		})
	}

	if len(newMessages) > 0 {
		return messagestore.New(tx, m.workspaceID).AppendMessages(ctx, newMessages...)
	}
	return nil
}

func (m *Manager) DeleteSession(ctx context.Context, tx libdb.Exec, sessionID string, identity string) error {
	store := messagestore.New(tx, m.workspaceID)
	if err := store.DeleteMessageIndex(ctx, sessionID, identity); err != nil {
		return fmt.Errorf("failed to delete session index: %w", err)
	}
	return nil
}

func (m *Manager) ClearSession(ctx context.Context, tx libdb.Exec, sessionID string) error {
	if err := messagestore.New(tx, m.workspaceID).DeleteMessages(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to clear session messages: %w", err)
	}
	return nil
}

func (m *Manager) RenameSession(ctx context.Context, tx libdb.Exec, sessionID string, name string) error {
	return messagestore.New(tx, m.workspaceID).RenameSession(ctx, sessionID, name)
}

func generateMessageID(subjectID string, msg *taskengine.Message) string {
	h := sha1.New()
	h.Write([]byte(subjectID))
	h.Write([]byte(msg.Role))
	h.Write([]byte(msg.ToolCallID))
	h.Write([]byte(msg.Timestamp.Format(time.RFC3339Nano)))
	h.Write([]byte(msg.Content))
	return fmt.Sprintf("%x", h.Sum(nil))
}
