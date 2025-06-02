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
	"github.com/google/uuid"
	"github.com/ollama/ollama/api"
)

type Manager struct {
	state     *runtimestate.State
	tokenizer tokenizerservice.Tokenizer
}

func New(
	state *runtimestate.State,
	tokenizer tokenizerservice.Tokenizer) *Manager {
	return &Manager{
		state:     state,
		tokenizer: tokenizer,
	}
}

// AddInstruction adds a system instruction to an existing chat instance.
// This method requires admin panel permissions.
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

func (m *Manager) Chat(ctx context.Context, tx libdb.Exec, beginTime time.Time, subjectID string, message string, preferredModelNames ...string) (string, int, error) {
	// TODO: check authorization for the chat instance.
	conversation, err := store.New(tx).ListMessages(ctx, subjectID)
	if err != nil {
		return "", 0, err
	}
	// Convert stored messages into the api.Message slice.
	var messages []serverops.Message
	for _, msg := range conversation {
		var parsedMsg serverops.Message
		if err := json.Unmarshal([]byte(msg.Payload), &parsedMsg); err != nil {
			return "", 0, fmt.Errorf("BUG: TODO: json.Unmarshal([]byte(msg.Data): now what? %w", err)
		}
		messages = append(messages, parsedMsg)
	}
	userMsg := serverops.Message{
		Role:    "user",
		Content: message,
	}
	messages = append(messages, userMsg)

	// Use chatExec to handle the chat logic
	responseMessage, contextLength, err := m.ChatExec(ctx, messages, preferredModelNames...)
	if err != nil {
		return "", contextLength, err
	}

	// Use appendMessages to handle message storage
	err = m.AppendMessages(ctx, tx, beginTime, subjectID, &userMsg, responseMessage)
	if err != nil {
		return "", contextLength, err
	}

	return responseMessage.Content, contextLength, nil
}

func (m *Manager) AppendMessages(ctx context.Context, tx libdb.Exec, beginTime time.Time, subjectID string, inputMessage *serverops.Message, responseMessage *serverops.Message) error {
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

func (m *Manager) ChatExec(ctx context.Context, messages []serverops.Message, preferredModelNames ...string) (*serverops.Message, int, error) {
	if len(messages) == 0 {
		return nil, 0, errors.New("no messages provided")
	}
	if messages[len(messages)-1].Role != "user" {
		return nil, 0, errors.New("last message must be from user")
	}
	contextLength, err := m.calculateContextSize(ctx, messages)
	if err != nil {
		return nil, contextLength, fmt.Errorf("could not estimate context size %w", err)
	}
	convertedMessage := make([]api.Message, len(messages))
	for i, m := range messages {
		convertedMessage[i] = api.Message{
			Role:    m.Role,
			Content: m.Content,
		}
	}
	chatClient, err := llmresolver.Chat(ctx, llmresolver.Request{
		ContextLength: contextLength,
		ModelNames:    preferredModelNames,
	}, modelprovider.ModelProviderAdapter(ctx, m.state.Get(ctx)), llmresolver.Randomly)
	if err != nil {
		return nil, contextLength, fmt.Errorf("failed to resolve backend %w", err)
	}
	responseMessage, err := chatClient.Chat(ctx, messages)
	if err != nil {
		return nil, contextLength, fmt.Errorf("failed to chat %w", err)
	}
	assistantMsgData := serverops.Message{
		Role:    responseMessage.Role,
		Content: responseMessage.Content,
	}

	return &assistantMsgData, contextLength, nil
}
func (m *Manager) calculateContextSize(ctx context.Context, messages []serverops.Message, baseModels ...string) (int, error) {
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
