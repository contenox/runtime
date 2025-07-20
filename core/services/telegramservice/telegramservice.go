package telegramservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

var ErrInvalidTelegramFrontend = errors.New("invalid Telegram frontend data")

type service struct {
	dbInstance libdb.DBManager
}

type Service interface {
	serverops.ServiceMeta

	Create(ctx context.Context, frontend *store.TelegramFrontend) error
	Update(ctx context.Context, frontend *store.TelegramFrontend) error
	Get(ctx context.Context, id string) (*store.TelegramFrontend, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]*store.TelegramFrontend, error)
	ListByUser(ctx context.Context, userID string) ([]*store.TelegramFrontend, error)
}

func New(db libdb.DBManager) Service {
	return &service{dbInstance: db}
}

func (s *service) Create(ctx context.Context, frontend *store.TelegramFrontend) error {
	if err := validate(frontend); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	// if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
	// 	return err
	// }
	return store.New(tx).CreateTelegramFrontend(ctx, frontend)
}

func (s *service) Update(ctx context.Context, frontend *store.TelegramFrontend) error {
	if err := validate(frontend); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	// if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
	// 	return err
	// }
	return store.New(tx).UpdateTelegramFrontend(ctx, frontend)
}

func (s *service) Get(ctx context.Context, id string) (*store.TelegramFrontend, error) {
	tx := s.dbInstance.WithoutTransaction()
	// if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
	// 	return nil, err
	// }
	return store.New(tx).GetTelegramFrontend(ctx, id)
}

func (s *service) Delete(ctx context.Context, id string) error {
	tx := s.dbInstance.WithoutTransaction()
	// if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
	// 	return err
	// }
	return store.New(tx).DeleteTelegramFrontend(ctx, id)
}

func (s *service) List(ctx context.Context) ([]*store.TelegramFrontend, error) {
	tx := s.dbInstance.WithoutTransaction()
	// if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
	// 	return nil, err
	// }
	return store.New(tx).ListTelegramFrontends(ctx)
}

func (s *service) ListByUser(ctx context.Context, userID string) ([]*store.TelegramFrontend, error) {
	tx := s.dbInstance.WithoutTransaction()
	// if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
	// 	return nil, err
	// }
	return store.New(tx).ListTelegramFrontendsByUser(ctx, userID)
}

func validate(frontend *store.TelegramFrontend) error {
	if frontend.BotToken == "" {
		return fmt.Errorf("%w: bot token is required", ErrInvalidTelegramFrontend)
	}
	if frontend.UserID == "" {
		return fmt.Errorf("%w: user ID is required", ErrInvalidTelegramFrontend)
	}
	return nil
}

func (s *service) GetServiceName() string {
	return "telegramservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
