package backendservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
)

var ErrInvalidBackend = errors.New("invalid backend data")

type Service interface {
	Create(ctx context.Context, backend *store.Backend) error
	Get(ctx context.Context, id string) (*store.Backend, error)
	Update(ctx context.Context, backend *store.Backend) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]*store.Backend, error)
	GetServiceName() string
	GetServiceGroup() string
}

type service struct {
	dbInstance      libdb.DBManager
	securityEnabled bool
	jwtSecret       string
}

func New(db libdb.DBManager) Service {
	return &service{dbInstance: db}
}

func (s *service) Create(ctx context.Context, backend *store.Backend) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	if err := validate(backend); err != nil {
		return err
	}
	return store.New(tx).CreateBackend(ctx, backend)
}

func (s *service) Get(ctx context.Context, id string) (*store.Backend, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).GetBackend(ctx, id)
}

func (s *service) Update(ctx context.Context, backend *store.Backend) error {
	if err := validate(backend); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).UpdateBackend(ctx, backend)
}

func (s *service) Delete(ctx context.Context, id string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).DeleteBackend(ctx, id)
}

func (s *service) List(ctx context.Context) ([]*store.Backend, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListBackends(ctx)
}

func validate(backend *store.Backend) error {
	if backend.Name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidBackend)
	}
	if backend.BaseURL == "" {
		return fmt.Errorf("%w: baseURL is required", ErrInvalidBackend)
	}
	if backend.Type != "ollama" && backend.Type != "vllm" {
		return fmt.Errorf("%w: Type is required to be ollama or vllm", ErrInvalidBackend)
	}

	return nil
}

func (s *service) GetServiceName() string {
	return "backendservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
