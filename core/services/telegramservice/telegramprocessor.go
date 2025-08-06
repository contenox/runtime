package telegramservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/chat"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/tasksrecipes"
	"github.com/contenox/runtime/execservice"
	"github.com/contenox/runtime/taskengine"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

type Processor struct {
	db          libdb.DBManager
	chatManager *chat.Manager
	envService  execservice.TasksEnvService
}

func NewProcessor(db libdb.DBManager, envService execservice.TasksEnvService, chatManager *chat.Manager) *Processor {
	return &Processor{db: db, envService: envService, chatManager: chatManager}
}

func (p *Processor) ProcessJob(ctx context.Context, job *store.Job) error {
	var payload jobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshalling payload: %w", err)
	}

	// Process the Telegram update
	if err := p.processUpdate(ctx, payload); err != nil {
		// Handle retries with backoff
		if job.RetryCount < 5 {
			return fmt.Errorf("processing failed (retry %d): %w", job.RetryCount+1, err)
		}
		return fmt.Errorf("abandoning after 5 retries: %w", err)
	}
	return nil
}

func (p *Processor) processUpdate(ctx context.Context, payload jobPayload) error {
	tx := p.db.WithoutTransaction()
	storeInstance := store.New(tx)

	// Ensure user exists
	if err := ensureUserExists(ctx, storeInstance, payload.UserID, payload.Update); err != nil {
		return fmt.Errorf("ensuring user exists: %w", err)
	}

	// Ensure message index exists
	if err := ensureMessageIndexExists(ctx, storeInstance, payload.UserID, payload.SubjectID); err != nil {
		return fmt.Errorf("ensuring message index exists: %w", err)
	}

	// Create bot instance
	bot, err := tgbotapi.NewBotAPI(payload.BotToken)
	if err != nil {
		return fmt.Errorf("creating bot API: %w", err)
	}

	// Get existing messages
	messages, err := p.chatManager.ListMessages(ctx, tx, payload.SubjectID)
	if err != nil {
		return fmt.Errorf("listing messages: %w", err)
	}

	// Append new user message
	userMessage := payload.Update.Message.Text
	messages, err = p.chatManager.AppendMessage(ctx, messages, time.Now().UTC(), userMessage, "user")
	if err != nil {
		return fmt.Errorf("appending user message: %w", err)
	}

	// Form chat history
	history := taskengine.ChatHistory{
		Messages: messages,
	}

	// Get chat chain definition
	chain, err := tasksrecipes.GetChainDefinition(ctx, tx, payload.ChainID)
	if err != nil {
		return fmt.Errorf("getting chain: %w", err)
	}

	// Execute processing chain
	result, _, _, err := p.envService.Execute(ctx, chain, history, taskengine.DataTypeChatHistory)
	if err != nil {
		return fmt.Errorf("executing chain: %w", err)
	}

	// Process result
	hist, ok := result.(taskengine.ChatHistory)
	if !ok || len(hist.Messages) == 0 {
		return errors.New("invalid chain result - expected chat history")
	}

	lastMsg := hist.Messages[len(hist.Messages)-1]
	if lastMsg.Role != "assistant" && lastMsg.Role != "system" {
		return fmt.Errorf("unexpected message role in response: %s", lastMsg.Role)
	}

	// Persist updated history
	if err := p.chatManager.PersistDiff(ctx, tx, payload.SubjectID, hist.Messages); err != nil {
		return fmt.Errorf("persisting chat history: %w", err)
	}

	// Send response back to Telegram
	msg := tgbotapi.NewMessage(payload.Update.Message.Chat.ID, lastMsg.Content)
	if _, err := bot.Send(msg); err != nil {
		return fmt.Errorf("sending telegram response: %w", err)
	}

	return nil
}

func ensureUserExists(ctx context.Context, storeInstance store.Store, userID string, update tgbotapi.Update) error {
	_, err := storeInstance.GetUserBySubject(ctx, userID)
	if err == nil {
		return nil // User already exists
	}

	if !errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("checking user existence: %w", err)
	}

	// Create new user from Telegram update data
	user := &store.User{
		ID:           userID,
		FriendlyName: update.SentFrom().UserName,
		Subject:      userID,
		Salt:         uuid.NewString(),
		Email:        fmt.Sprintf("%s@telegram.contnox.com", userID),
	}

	if err := storeInstance.CreateUser(ctx, user); err != nil {
		return fmt.Errorf("creating user: %w", err)
	}
	return nil
}

func ensureMessageIndexExists(ctx context.Context, storeInstance store.Store, userID, subjectID string) error {
	// Check if message index already exists
	idxs, err := storeInstance.ListMessageIndices(ctx, userID)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return fmt.Errorf("listing message indices: %w", err)
	}

	// If index exists, nothing to do
	if slices.Contains(idxs, subjectID) {
		return nil
	}

	// Create new message index
	if err := storeInstance.CreateMessageIndex(ctx, subjectID, userID); err != nil {
		return fmt.Errorf("creating message index: %w", err)
	}
	return nil
}
