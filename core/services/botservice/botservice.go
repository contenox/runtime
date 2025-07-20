package botservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

var ErrInvalidBot = errors.New("invalid bot data")

type Service interface {
	serverops.ServiceMeta

	CreateBot(ctx context.Context, bot *store.Bot) error
	GetBot(ctx context.Context, id string) (*store.Bot, error)
	UpdateBot(ctx context.Context, bot *store.Bot) error
	DeleteBot(ctx context.Context, id string) error
	ListBots(ctx context.Context) ([]*store.Bot, error)
	ListBotsByUser(ctx context.Context, userID string) ([]*store.Bot, error)
}

type service struct {
	dbInstance libdb.DBManager
}

func New(db libdb.DBManager) Service {
	return &service{
		dbInstance: db,
	}
}

func (s *service) CreateBot(ctx context.Context, bot *store.Bot) error {
	if err := validate(bot); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).CreateBot(ctx, bot)
}

func (s *service) GetBot(ctx context.Context, id string) (*store.Bot, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).GetBot(ctx, id)
}

func (s *service) UpdateBot(ctx context.Context, bot *store.Bot) error {
	if err := validate(bot); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionEdit); err != nil {
		return err
	}
	return store.New(tx).UpdateBot(ctx, bot)
}

func (s *service) DeleteBot(ctx context.Context, id string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).DeleteBot(ctx, id)
}

func (s *service) ListBots(ctx context.Context) ([]*store.Bot, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListBots(ctx)
}

func (s *service) ListBotsByUser(ctx context.Context, userID string) ([]*store.Bot, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListBotsByUser(ctx, userID)
}

func validate(bot *store.Bot) error {
	if bot.Name == "" {
		return fmt.Errorf("%w: bot name is required", ErrInvalidBot)
	}
	if bot.BotType == "" {
		return fmt.Errorf("%w: bot type is required", ErrInvalidBot)
	}
	if bot.JobType == "" {
		return fmt.Errorf("%w: job type is required", ErrInvalidBot)
	}
	if bot.TaskChainID == "" {
		return fmt.Errorf("%w: task chain ID is required", ErrInvalidBot)
	}
	return nil
}

func (s *service) GetServiceName() string {
	return "botservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
