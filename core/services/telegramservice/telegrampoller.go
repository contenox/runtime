package telegramservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
)

type Poller struct {
	db libdb.DBManager
}

func NewPoller(db libdb.DBManager) *Poller {
	return &Poller{db: db}
}

func (p *Poller) Tick(ctx context.Context) error {
	frontends, err := store.New(p.db.WithoutTransaction()).ListTelegramFrontends(ctx)
	if err != nil {
		return fmt.Errorf("listing telegram frontends: %w", err)
	}
	errs := []string{}
	for _, fe := range frontends {
		if err := p.processFrontend(ctx, fe); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, ","))
	}
	return nil
}

func (p *Poller) processFrontend(ctx context.Context, fe *store.TelegramFrontend) error {
	storeInstance := store.New(p.db.WithoutTransaction())

	bot, err := tgbotapi.NewBotAPI(fe.BotToken)
	if err != nil {
		return fmt.Errorf("creating bot API: %w", err)
	}

	updates, err := bot.GetUpdates(tgbotapi.NewUpdate(fe.LastOffset))
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}

	jobs := make([]*store.Job, 0, len(updates))
	for _, update := range updates {
		if update.Message == nil {
			continue
		}

		job, err := createJob(fe, update)
		if err != nil {
			return fmt.Errorf("creating job: %w", err)
		}
		jobs = append(jobs, job)
	}

	if err := storeInstance.AppendJobs(ctx, jobs...); err != nil {
		return fmt.Errorf("appending jobs: %w", err)
	}

	// Update offset
	newOffset := updates[len(updates)-1].UpdateID + 1
	fe.LastOffset = newOffset

	return storeInstance.UpdateTelegramFrontend(ctx, fe)
}

func createJob(fe *store.TelegramFrontend, update tgbotapi.Update) (*store.Job, error) {
	payload := jobPayload{
		BotToken:  fe.BotToken,
		Update:    update,
		ChainID:   fe.ChatChain,
		Frontend:  fe.ID,
		UserID:    fmt.Sprint(update.SentFrom().ID),
		SubjectID: fmt.Sprint(update.FromChat().ID) + fmt.Sprint(update.SentFrom().ID),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &store.Job{
		ID:        uuid.NewString(),
		TaskType:  "telegram-message",
		CreatedAt: time.Now().UTC(),
		Payload:   payloadBytes,
	}, nil
}

type jobPayload struct {
	BotToken  string
	Update    tgbotapi.Update
	ChainID   string
	Frontend  string
	UserID    string
	SubjectID string
}
