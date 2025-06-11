package chatservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/contenox/core/chat"
	"github.com/contenox/contenox/core/hooks"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/core/services/tokenizerservice"
	"github.com/contenox/contenox/core/taskengine"
	"github.com/contenox/contenox/libs/libdb"
	"github.com/google/uuid"
)

type Service interface {
	GetChatHistory(ctx context.Context, id string) ([]ChatMessage, error)
	Chat(ctx context.Context, subjectID string, message string, preferredModelNames ...string) (string, int, int, error)
	ListChats(ctx context.Context) ([]ChatSession, error)
	NewInstance(ctx context.Context, subject string, preferredModels ...string) (string, error)
	AddInstruction(ctx context.Context, id string, message string) error
	serverops.ServiceMeta
}

type service struct {
	// state      *runtimestate.State
	dbInstance libdb.DBManager
	tokenizer  tokenizerservice.Tokenizer
	manager    *chat.Manager
}

func New(
	dbInstance libdb.DBManager,
	tokenizer tokenizerservice.Tokenizer,
	manager *chat.Manager,
) Service {
	return &service{
		dbInstance: dbInstance,
		tokenizer:  tokenizer,
		manager:    manager,
	}
}

type ChatInstance struct {
	Messages []serverops.Message

	CreatedAt time.Time
}

type ChatSession struct {
	ChatID      string       `json:"id"`
	StartedAt   time.Time    `json:"startedAt"`
	BackendID   string       `json:"backendId"`
	LastMessage *ChatMessage `json:"lastMessage,omitempty"`
}

// NewInstance creates a new chat instance after verifying that the user is authorized to start a chat for the given model.
func (s *service) NewInstance(ctx context.Context, subject string, preferredModels ...string) (string, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return "", err
	}
	identity, err := serverops.GetIdentity(ctx)
	if err != nil {
		return "", err
	}

	idxID := uuid.New().String()
	err = store.New(tx).CreateMessageIndex(ctx, idxID, identity)
	if err != nil {
		return "", err
	}

	return idxID, nil
}

// AddInstruction adds a system instruction to an existing chat instance.
// This method requires admin panel permissions.
func (s *service) AddInstruction(ctx context.Context, id string, message string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}

	return s.manager.AddInstruction(ctx, tx, id, message)
}

func (s *service) Chat(ctx context.Context, subjectID string, message string, preferredModelNames ...string) (string, int, int, error) {
	now := time.Now().UTC()
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return "", 0, 0, err
	}

	chatHook := hooks.NewChatHook(s.dbInstance, s.manager)

	// Append user message
	status, result, dtype, err := chatHook.Exec(ctx, now, message, taskengine.DataTypeString, &taskengine.HookCall{
		Type: "append_user_input",
		Args: map[string]string{
			"subject_id": subjectID,
		},
	})
	if err != nil || status != taskengine.StatusSuccess {
		return "", 0, 0, fmt.Errorf("append_user_input failed: %w", err)
	}

	// Run model inference
	status, result, dtype, err = chatHook.Exec(ctx, now, result, dtype, &taskengine.HookCall{
		Type: "execute_chat_model",
		Args: map[string]string{
			"models": strings.Join(preferredModelNames, ", "),
		},
	})
	if err != nil || status != taskengine.StatusSuccess {
		return "", 0, 0, fmt.Errorf("execute_chat_model failed: %w", err)
	}

	// Persist messages
	status, result, _, err = chatHook.Exec(ctx, now, result, dtype, &taskengine.HookCall{
		Type: "persist_input_output",
		Args: map[string]string{
			"subject_id": subjectID,
		},
	})
	if err != nil || status != taskengine.StatusSuccess {
		return "", 0, 0, fmt.Errorf("persist_input_output failed: %w", err)
	}

	// Extract the final assistant message
	history, ok := result.(taskengine.ChatHistory)
	if !ok || len(history.Messages) == 0 {
		return "", 0, 0, fmt.Errorf("unexpected result from hooks")
	}

	lastMsg := history.Messages[len(history.Messages)-1]
	if lastMsg.Role != "assistant" {
		return "", 0, 0, fmt.Errorf("expected assistant message, got %q", lastMsg.Role)
	}

	return lastMsg.Content, history.InputTokens, history.OutputTokens, nil
}

// ChatMessage is the public representation of a message in a chat.
type ChatMessage struct {
	Role     string    `json:"role"`     // user/assistant/system
	Content  string    `json:"content"`  // message text
	SentAt   time.Time `json:"sentAt"`   // timestamp
	IsUser   bool      `json:"isUser"`   // derived from role
	IsLatest bool      `json:"isLatest"` // mark if last message
}

// GetChatHistory retrieves the chat history for a specific chat instance.
// It checks that the caller is authorized to view the chat instance.
func (s *service) GetChatHistory(ctx context.Context, id string) ([]ChatMessage, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	conversation, err := store.New(tx).ListMessages(ctx, id)
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

	var history []ChatMessage
	for i, msg := range messages {
		history = append(history, ChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
			SentAt:  conversation[i].AddedAt,
			IsUser:  msg.Role == "user",
		})
	}
	if len(history) > 0 {
		history[len(history)-1].IsLatest = true
	}
	return history, nil
}

// ListChats returns all chat sessions.
// This operation requires admin panel view permission.
func (s *service) ListChats(ctx context.Context) ([]ChatSession, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	userID, err := serverops.GetIdentity(ctx)
	if err != nil {
		return nil, err
	}
	subjects, err := store.New(tx).ListMessageIndices(ctx, userID)
	if err != nil {
		return nil, err
	}
	// TODO implement missing logic here
	var sessions []ChatSession
	for _, sub := range subjects {
		sessions = append(sessions, ChatSession{
			ChatID: sub,
		})
	}

	return sessions, nil
}

type ModelResult struct {
	Model      string
	TokenCount int
	MaxTokens  int // Max token length for the model.
}

func (s *service) GetServiceName() string {
	return "chatservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
