package botservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
	"github.com/google/uuid"
)

type BotPoller struct {
	db      libdb.DBManager
	service Service
}

func NewBotPoller(db libdb.DBManager, service Service) *BotPoller {
	return &BotPoller{db: db, service: service}
}

func (p *BotPoller) Tick(ctx context.Context) error {
	bots, err := p.service.ListBots(ctx)
	if err != nil {
		return fmt.Errorf("listing bots: %w", err)
	}

	errs := []string{}
	for _, bot := range bots {
		if err := p.processBot(ctx, bot); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, ","))
	}
	return nil
}

func (p *BotPoller) processBot(ctx context.Context, bot *store.Bot) error {
	storeInstance := store.New(p.db.WithoutTransaction())

	// Get fetcher for bot type
	fetcher, err := getBotFetcher(bot.BotType)
	if err != nil {
		return fmt.Errorf("getting bot fetcher: %w", err)
	}

	// Fetch new updates
	updates, newState, err := fetcher.FetchUpdates(ctx, bot.State)
	if err != nil {
		return fmt.Errorf("fetching updates: %w", err)
	}
	if len(updates) == 0 {
		return nil
	}

	// Create jobs for updates
	jobs := make([]*store.Job, 0, len(updates))
	for _, update := range updates {
		job, err := createBotJob(bot, update)
		if err != nil {
			return fmt.Errorf("creating job: %w", err)
		}
		jobs = append(jobs, job)
	}

	// Save jobs to store
	if err := storeInstance.AppendJobs(ctx, jobs...); err != nil {
		return fmt.Errorf("appending jobs: %w", err)
	}

	// Update bot state
	bot.State = newState
	return p.service.UpdateBot(ctx, bot)
}

func createBotJob(bot *store.Bot, update interface{}) (*store.Job, error) {
	payload := botJobPayload{
		BotID:   bot.ID,
		Update:  update,
		ChainID: bot.TaskChainID,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &store.Job{
		ID:        uuid.NewString(),
		TaskType:  bot.JobType,
		CreatedAt: time.Now().UTC(),
		Payload:   payloadBytes,
	}, nil
}

type botJobPayload struct {
	BotID   string      `json:"botId"`
	Update  interface{} `json:"update"`
	ChainID string      `json:"chainId"`
}

// BotFetcher interface for different bot types
type BotFetcher interface {
	FetchUpdates(ctx context.Context, state []byte) (updates []interface{}, newState []byte, err error)
}

// BotFetcher registry
var botFetchers = make(map[string]BotFetcher)

func RegisterBotFetcher(botType string, fetcher BotFetcher) {
	botFetchers[botType] = fetcher
}

func getBotFetcher(botType string) (BotFetcher, error) {
	fetcher, ok := botFetchers[botType]
	if !ok {
		return nil, fmt.Errorf("no fetcher registered for bot type: %s", botType)
	}
	return fetcher, nil
}
