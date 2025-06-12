package chatservice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/chat"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
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
	dbInstance libdb.DBManager
	manager    *chat.Manager
	env        taskengine.EnvExecutor
}

func New(
	dbInstance libdb.DBManager,
	manager *chat.Manager,
	env taskengine.EnvExecutor,
) Service {
	return &service{
		dbInstance: dbInstance,
		manager:    manager,
		env:        env,
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
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return "", 0, 0, err
	}

	// Use raw string as input
	input := message

	// Build or load chain definition
	chain := buildChatChain(subjectID, preferredModelNames)

	// Run the chain using the environment executor
	result, err := s.env.ExecEnv(ctx, chain, input, taskengine.DataTypeString)
	if err != nil {
		return "", 0, 0, fmt.Errorf("chain execution failed: %w", err)
	}

	// Extract final assistant message
	hist, ok := result.(taskengine.ChatHistory)
	if !ok || len(hist.Messages) == 0 {
		return "", 0, 0, fmt.Errorf("unexpected result from chain")
	}

	lastMsg := hist.Messages[len(hist.Messages)-1]
	if lastMsg.Role != "assistant" && lastMsg.Role != "system" {
		return "", 0, 0, fmt.Errorf("expected assistant or system message, got %q", lastMsg.Role)
	}

	return lastMsg.Content, hist.InputTokens, hist.OutputTokens, nil
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

func buildChatChain(subjectID string, preferredModelNames []string) *taskengine.ChainDefinition {
	return &taskengine.ChainDefinition{
		ID:          "chat_chain",
		Description: "Standard chat processing pipeline with hooks",
		Tasks: []taskengine.ChainTask{
			{
				ID:          "append_user_input",
				Description: "Append user message to chat history",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "append_user_input",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Value: "_default", ID: "mux_input"},
					},
				},
			},
			{
				ID:          "mux_input",
				Description: "Check for commands like /echo using Mux",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "mux",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Value: "_default", ID: "execute_chat_model"},
						{
							Operator: "equals",
							Value:    "echo",
							ID:       "persist_input_output",
						},
					},
				},
			},
			{
				ID:              "execute_chat_model",
				Description:     "Run inference using selected LLM",
				Type:            taskengine.Hook,
				PreferredModels: preferredModelNames,
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Value: "_default", ID: "persist_input_output"},
					},
				},
				Hook: &taskengine.HookCall{
					Type: "execute_chat_model",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
			},
			{
				ID:          "persist_input_output",
				Description: "Persist the conversation",
				Type:        taskengine.Hook,
				Hook: &taskengine.HookCall{
					Type: "persist_input_output",
					Args: map[string]string{
						"subject_id": subjectID,
					},
				},
				Transition: taskengine.Transition{
					Next: []taskengine.ConditionalTransition{
						{Value: "_default", ID: "end"},
					},
				},
			},
		},
	}
}

func (s *service) GetServiceName() string {
	return "chatservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
