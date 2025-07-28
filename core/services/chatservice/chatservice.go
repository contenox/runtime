package chatservice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/taskengine"
	"github.com/contenox/runtime-mvp/core/tasksrecipes"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/contenox/runtime-mvp/libs/libmodelprovider"
	"github.com/google/uuid"
)

type Service interface {
	GetChatHistory(ctx context.Context, id string) ([]ChatMessage, error)
	Chat(ctx context.Context, req ChatRequest) (string, int, int, []taskengine.CapturedStateUnit, error)
	ListChats(ctx context.Context) ([]ChatSession, error)
	NewInstance(ctx context.Context, subject string) (string, error)
	AddInstruction(ctx context.Context, id string, message string) error
	OpenAIChat
	serverops.ServiceMeta
}

type OpenAIChat interface {
	OpenAIChatCompletions(ctx context.Context, req taskengine.OpenAIChatRequest) (*taskengine.OpenAIChatResponse, error)
}

type service struct {
	dbInstance  libdb.DBManager
	chatManager *chat.Manager
	env         taskengine.EnvExecutor
}

func New(
	dbInstance libdb.DBManager,
	env taskengine.EnvExecutor,
	chatManager *chat.Manager,
) Service {
	return &service{
		dbInstance:  dbInstance,
		env:         env,
		chatManager: chatManager,
	}
}

type ChatInstance struct {
	Messages []libmodelprovider.Message

	CreatedAt time.Time
}

type ChatSession struct {
	ChatID      string       `json:"id"`
	StartedAt   time.Time    `json:"startedAt"`
	BackendID   string       `json:"backendId"`
	LastMessage *ChatMessage `json:"lastMessage,omitempty"`
}

// NewInstance creates a new chat instance after verifying that the user is authorized to start a chat.
func (s *service) NewInstance(ctx context.Context, subject string) (string, error) {
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
func (s *service) AddInstruction(ctx context.Context, subjectID string, message string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}

	// Build or load chain definition
	chain := tasksrecipes.BuildAppendInstruction(subjectID)

	// Run the chain using the environment executor
	_, _, err := s.env.ExecEnv(ctx, chain, message, taskengine.DataTypeString)
	if err != nil {
		return fmt.Errorf("chain execution failed: %w", err)
	}
	return nil
}

type ChatRequest struct {
	SubjectID           string
	Message             string
	PreferredModelNames []string
	Provider            string
}

func (s *service) Chat(ctx context.Context, req ChatRequest) (string, int, int, []taskengine.CapturedStateUnit, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return "", 0, 0, nil, err
	}
	messages, err := s.chatManager.ListMessages(ctx, tx, req.SubjectID)
	if err != nil {
		return "", 0, 0, nil, err
	}
	messages, err = s.chatManager.AppendMessage(ctx, messages, time.Now().UTC(), req.Message, "user")
	if err != nil {
		return "", 0, 0, nil, err
	}
	history := taskengine.ChatHistory{
		Messages: messages,
	}
	// Retrieve chain from store
	chain, err := tasksrecipes.GetChainDefinition(ctx, tx, tasksrecipes.StandardChatChainID)
	if err != nil {
		return "", 0, 0, nil, fmt.Errorf("failed to get chain: %w", err)
	}

	// Update chain parameters
	for i := range chain.Tasks {
		task := &chain.Tasks[i]
		if task.Hook == nil {
			continue
		}
		if task.Type == taskengine.ModelExecution && task.ExecuteConfig != nil {
			task.ExecuteConfig.Models = req.PreferredModelNames
			task.ExecuteConfig.Provider = req.Provider
		}
	}

	// Execute chain
	result, stackTrace, err := s.env.ExecEnv(ctx, chain, history, taskengine.DataTypeChatHistory)
	if err != nil {
		return "", 0, 0, stackTrace, fmt.Errorf("chain execution failed: %w", err)
	}
	// Process result
	hist, ok := result.(taskengine.ChatHistory)
	if !ok || len(hist.Messages) == 0 {
		return "", 0, 0, stackTrace, fmt.Errorf("unexpected result from chain")
	}

	lastMsg := hist.Messages[len(hist.Messages)-1]
	if lastMsg.Role != "assistant" && lastMsg.Role != "system" {
		return "", 0, 0, stackTrace, fmt.Errorf("expected assistant or system message, got %q", lastMsg.Role)
	}
	err = s.chatManager.PersistDiff(ctx, tx, req.SubjectID, hist.Messages)
	if err != nil {
		return "", 0, 0, stackTrace, fmt.Errorf("failed to persist chat history: %w", err)
	}

	return lastMsg.Content, hist.InputTokens, hist.OutputTokens, stackTrace, nil
}

// ChatMessage is the public representation of a message in a chat.
type ChatMessage struct {
	ID       string    `json:"id"`       // unique identifier
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
	var messages []libmodelprovider.Message
	for _, msg := range conversation {
		var parsedMsg libmodelprovider.Message
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

func (s *service) OpenAIChatCompletions(ctx context.Context, req taskengine.OpenAIChatRequest) (*taskengine.OpenAIChatResponse, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	provider := ""
	if strings.HasPrefix(req.Model, "openai") {
		provider = "openai"
	}
	if strings.HasPrefix(req.Model, "ollama") {
		provider = "ollama"
	}
	if strings.HasPrefix(req.Model, "gemini") {
		provider = "gemini"
	}
	if strings.HasPrefix(req.Model, "vllm") {
		provider = "vllm"
	}
	chain := tasksrecipes.BuildOpenAIChatChain(req.Model, provider)

	result, stackTrace, err := s.env.ExecEnv(ctx, chain, req, taskengine.DataTypeOpenAIChat)
	if err != nil {
		return nil, fmt.Errorf("chain execution failed: %w", err)
	}
	_ = stackTrace // TODO: log stack trace?
	if result == nil {
		return nil, fmt.Errorf("empty result from chain")
	}

	// Correct type assertion
	res, ok := result.(taskengine.OpenAIChatResponse)
	if !ok {
		return nil, fmt.Errorf("invalid result type: %T", result)
	}

	return &res, nil
}

func (s *service) GetServiceName() string {
	return "chatservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
