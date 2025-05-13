package chatservice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/js402/cate/core/llmresolver"
	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/core/services/tokenizerservice"
	"github.com/js402/cate/libs/libdb"
	"github.com/ollama/ollama/api"
)

const tokenizerMaxPromptBytes = 16 * 1024 // 16 KiB

type Service struct {
	state      *runtimestate.State
	dbInstance libdb.DBManager
	tokenizer  tokenizerservice.Tokenizer
}

func New(
	state *runtimestate.State,
	dbInstance libdb.DBManager,
	tokenizer tokenizerservice.Tokenizer) *Service {
	return &Service{
		state:      state,
		dbInstance: dbInstance,
		tokenizer:  tokenizer,
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
func (s *Service) NewInstance(ctx context.Context, subject string, preferredModels ...string) (string, error) {
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
func (s *Service) AddInstruction(ctx context.Context, id string, message string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	// TODO: check authorization for the chat instance.
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

func (s *Service) Chat(ctx context.Context, subjectID string, message string, preferredModelNames ...string) (string, error) {
	now := time.Now().UTC()
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return "", err
	}
	// TODO: check authorization for the chat instance.
	conversation, err := store.New(tx).ListMessages(ctx, subjectID)
	if err != nil {
		return "", err
	}
	// Convert stored messages into the api.Message slice.
	var messages []serverops.Message
	for _, msg := range conversation {
		var parsedMsg serverops.Message
		if err := json.Unmarshal([]byte(msg.Payload), &parsedMsg); err != nil {
			return "", fmt.Errorf("BUG: TODO: json.Unmarshal([]byte(msg.Data): now what? %w", err)
		}
		messages = append(messages, parsedMsg)
	}
	msg := serverops.Message{
		Role:    "user",
		Content: message,
	}
	messages = append(messages, msg)
	contextLength, err := s.CalculateContextSize(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("could not estimate context size %w", err)
	}
	convertedMessage := make([]api.Message, len(messages))
	for i, m := range messages {
		convertedMessage[i] = api.Message{
			Role:    m.Role,
			Content: m.Content,
		}
	}
	chatClient, err := llmresolver.ResolveChat(ctx, llmresolver.ResolveRequest{
		ContextLength: contextLength,
		ModelNames:    preferredModelNames,
	}, modelprovider.ModelProviderAdapter(ctx, s.state.Get(ctx)), llmresolver.ResolveRandomly)
	if err != nil {
		return "", fmt.Errorf("failed to resolve backend %w", err)
	}
	responseMessage, err := chatClient.Chat(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("failed to chat %w", err)
	}
	assistantMsgData := serverops.Message{
		Role:    responseMessage.Role,
		Content: responseMessage.Content,
	}
	jsonData, err := json.Marshal(assistantMsgData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal assistant message data: %w", err)
	}
	payload, err := json.Marshal(&msg)
	if err != nil {
		return "", fmt.Errorf("failed to marshal user message %w", err)
	}
	err = store.New(tx).AppendMessages(ctx,
		&store.Message{
			ID:      uuid.NewString(),
			IDX:     subjectID,
			Payload: payload,
			AddedAt: now,
		},
		&store.Message{
			ID:      uuid.New().String(),
			IDX:     subjectID,
			Payload: jsonData,
			AddedAt: time.Now().UTC(),
		})
	if err != nil {
		return "", err
	}

	return responseMessage.Content, nil
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
func (s *Service) GetChatHistory(ctx context.Context, id string) ([]ChatMessage, error) {
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
func (s *Service) ListChats(ctx context.Context) ([]ChatSession, error) {
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

func (s *Service) CalculateContextSize(ctx context.Context, messages []serverops.Message, baseModels ...string) (int, error) {
	var prompt string
	for _, m := range messages {
		if m.Role == "user" {
			prompt = prompt + "\n" + m.Content
		}
	}
	var selectedModel string
	for _, model := range baseModels {
		optimal, err := s.tokenizer.OptimalModel(ctx, model)
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
		tokens, err := s.tokenizer.CountTokens(ctx, selectedModel, chunk)
		if err != nil {
			return 0, fmt.Errorf("failed to estimate context size at chunk [%d:%d]: %w", start, end, err)
		}
		count += tokens
	}

	return count, nil
}

func (s *Service) GetServiceName() string {
	return "chatservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
