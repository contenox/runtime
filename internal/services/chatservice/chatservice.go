package chatservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/js402/CATE/internal/serverops"
	"github.com/js402/CATE/internal/serverops/messagerepo"
	"github.com/js402/CATE/internal/serverops/state"
	"github.com/js402/CATE/internal/serverops/store"
	"github.com/ollama/ollama/api"
)

type Service struct {
	state    *state.State
	msgStore messagerepo.Store
}

func New(state *state.State, msgStore messagerepo.Store) *Service {
	return &Service{state: state,
		msgStore: msgStore,
	}
}

type ChatInstance struct {
	Model    string
	Messages []api.Message

	CreatedAt time.Time
	mu        sync.Mutex
}

type ChatSession struct {
	ChatID      string       `json:"id"`
	StartedAt   time.Time    `json:"startedAt"`
	Model       string       `json:"model"`
	BackendID   string       `json:"backendId"`
	LastMessage *ChatMessage `json:"lastMessage,omitempty"`
}

// NewInstance creates a new chat instance after verifying that the user is authorized to start a chat for the given model.
func (s *Service) NewInstance(ctx context.Context, subject, selectedModel string) (uuid.UUID, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return uuid.Nil, err
	}

	_, err := state.FindModel(ctx, s.state, selectedModel)
	if err != nil {
		return uuid.Nil, err
	}

	chatSubjectID := uuid.New()
	now := time.Now().UTC()

	err = s.msgStore.Save(ctx, messagerepo.Message{
		ID:          uuid.New().String(),
		MessageID:   "0",
		Data:        `{"role": "system", "content": "{}"}`,
		Source:      "chatservice",
		SpecVersion: "v1",
		Type:        "chat_message",
		Subject:     chatSubjectID.String(),
		Time:        now,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return chatSubjectID, nil
}

// AddInstruction adds a system instruction to an existing chat instance.
// This method requires admin panel permissions.
func (s *Service) AddInstruction(ctx context.Context, id uuid.UUID, message string) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	err := s.msgStore.Save(ctx, messagerepo.Message{
		ID:          uuid.New().String(),
		MessageID:   "0",
		Data:        fmt.Sprintf(`{"role": "system", "content": "%s"}`, message),
		Source:      "chatservice",
		SpecVersion: "v1",
		Type:        "chat_message",
		Subject:     id.String(),
		Time:        time.Now().UTC(),
	})
	return err
}

func (s *Service) AddMessage(ctx context.Context, id uuid.UUID, message string) error {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionManage); err != nil {
		return err
	}
	err := s.msgStore.Save(ctx, messagerepo.Message{
		ID:          uuid.New().String(),
		MessageID:   "0",
		Data:        fmt.Sprintf(`{"role": "user", "content": "%s"}`, message),
		Source:      "chatservice",
		SpecVersion: "v1",
		Type:        "chat_message",
		Subject:     id.String(),
		Time:        time.Now().UTC(),
	})
	return err
}

func (s *Service) Chat(ctx context.Context, subjectID uuid.UUID, model, message string) (string, error) {
	// Save the user's message.
	if err := s.AddMessage(ctx, subjectID, message); err != nil {
		return "", err
	}

	backendInstance, err := state.FindModel(ctx, s.state, model)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(backendInstance.Backend.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid backend URL: %v", err)
	}

	// Retrieve all messages for this chat from the persistent store.
	msgs, _, _, err := s.msgStore.Search(ctx, subjectID.String(), nil, nil, "", "", 0, 10000, "", "")
	if err != nil {
		return "", err
	}

	// Convert stored messages into the api.Message slice.
	var apiMessages []api.Message
	for _, msg := range msgs {
		var parsedMsg api.Message
		if err := json.Unmarshal([]byte(msg.Data), &parsedMsg); err != nil {
			// Optionally log or skip malformed messages.
			continue
		}
		apiMessages = append(apiMessages, parsedMsg)
	}

	client := api.NewClient(u, http.DefaultClient)
	var finalMessage api.Message
	stream := false

	// Perform the chat request with the full conversation.
	err = client.Chat(ctx, &api.ChatRequest{
		Model:    model,
		Messages: apiMessages,
		Stream:   &stream,
	}, func(cr api.ChatResponse) error {
		if cr.Done {
			finalMessage = cr.Message
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	// Save the assistant's reply into the persistent store.
	assistantData, err := json.Marshal(finalMessage)
	if err != nil {
		return "", err
	}
	err = s.msgStore.Save(ctx, messagerepo.Message{
		ID:          uuid.New().String(),
		MessageID:   "0",
		Data:        string(assistantData),
		Source:      "chatservice",
		SpecVersion: "v1",
		Type:        "chat_message",
		Subject:     subjectID.String(),
		Time:        time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}

	return finalMessage.Content, nil
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
func (s *Service) GetChatHistory(ctx context.Context, id uuid.UUID) ([]ChatMessage, error) {
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionView); err != nil {
		return nil, err
	}

	msgs, _, _, err := s.msgStore.Search(ctx, id.String(), nil, nil, "", "", 0, 10000, "", "")
	if err != nil {
		return nil, err
	}

	var history []ChatMessage
	for _, msg := range msgs {
		var parsedMsg api.Message
		if err := json.Unmarshal([]byte(msg.Data), &parsedMsg); err != nil {
			continue // Skip messages that cannot be parsed.
		}
		history = append(history, ChatMessage{
			Role:    parsedMsg.Role,
			Content: parsedMsg.Content,
			SentAt:  msg.Time,
			IsUser:  parsedMsg.Role == "user",
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
	if err := serverops.CheckServiceAuthorization(ctx, s, store.PermissionView); err != nil {
		return nil, err
	}

	// Retrieve messages related to chat sessions.
	msgs, _, _, err := s.msgStore.Search(ctx, "", nil, nil, "chatservice", "chat_message", 0, 10000, "", "")
	if err != nil {
		return nil, err
	}

	// Group messages by their Subject (chat session id).
	sessionsMap := make(map[string][]messagerepo.Message)
	for _, msg := range msgs {
		sessionsMap[msg.Subject] = append(sessionsMap[msg.Subject], msg)
	}

	var sessions []ChatSession
	for subject, messages := range sessionsMap {
		// Sort messages by time.
		sort.Slice(messages, func(i, j int) bool {
			return messages[i].Time.Before(messages[j].Time)
		})
		// TODO Retrieve a model value.
		model := "TODO "
		var lastMsg *ChatMessage
		if len(messages) > 0 {
			last := messages[len(messages)-1]
			var parsedMsg api.Message
			if err := json.Unmarshal([]byte(last.Data), &parsedMsg); err == nil {
				lastMsg = &ChatMessage{
					Role:     parsedMsg.Role,
					Content:  parsedMsg.Content,
					SentAt:   last.Time,
					IsUser:   parsedMsg.Role == "user",
					IsLatest: true,
				}
			}
		}

		sessions = append(sessions, ChatSession{
			ChatID:      subject,
			StartedAt:   messages[0].Time,
			Model:       model,
			LastMessage: lastMsg,
		})
	}

	return sessions, nil
}

func (s *Service) GetServiceName() string {
	return "chatservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
