package poolservice

import (
	"context"
	"errors"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
	"github.com/google/uuid"
)

var (
	ErrInvalidPool = errors.New("invalid pool data")
	ErrNotFound    = libdb.ErrNotFound
)

type service struct {
	dbInstance libdb.DBManager
}

func New(db libdb.DBManager) Service {
	return &service{dbInstance: db}
}

type Service interface {
	Create(ctx context.Context, pool *store.Pool) error
	GetByID(ctx context.Context, id string) (*store.Pool, error)
	GetByName(ctx context.Context, name string) (*store.Pool, error)
	Update(ctx context.Context, pool *store.Pool) error
	Delete(ctx context.Context, id string) error
	ListAll(ctx context.Context) ([]*store.Pool, error)
	ListByPurpose(ctx context.Context, purpose string) ([]*store.Pool, error)
	AssignBackend(ctx context.Context, poolID, backendID string) error
	RemoveBackend(ctx context.Context, poolID, backendID string) error
	ListBackends(ctx context.Context, poolID string) ([]*store.Backend, error)
	ListPoolsForBackend(ctx context.Context, backendID string) ([]*store.Pool, error)
	AssignModel(ctx context.Context, poolID, modelID string) error
	RemoveModel(ctx context.Context, poolID, modelID string) error
	ListModels(ctx context.Context, poolID string) ([]*store.Model, error)
	ListPoolsForModel(ctx context.Context, modelID string) ([]*store.Pool, error)
	serverops.ServiceMeta
}

func (s *service) Create(ctx context.Context, pool *store.Pool) error {
	pool.ID = uuid.New().String()
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).CreatePool(ctx, pool)
}

func (s *service) GetByID(ctx context.Context, id string) (*store.Pool, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).GetPool(ctx, id)
}

func (s *service) GetByName(ctx context.Context, name string) (*store.Pool, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).GetPoolByName(ctx, name)
}

func (s *service) Update(ctx context.Context, pool *store.Pool) error {
	if pool.ID == serverops.EmbedPoolID {
		return serverops.ErrImmutablePool
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).UpdatePool(ctx, pool)
}

func (s *service) Delete(ctx context.Context, id string) error {
	if id == serverops.EmbedPoolID {
		return serverops.ErrImmutablePool
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).DeletePool(ctx, id)
}

func (s *service) ListAll(ctx context.Context) ([]*store.Pool, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListPools(ctx)
}

func (s *service) ListByPurpose(ctx context.Context, purpose string) ([]*store.Pool, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListPoolsByPurpose(ctx, purpose)
}

func (s *service) AssignBackend(ctx context.Context, poolID, backendID string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).AssignBackendToPool(ctx, poolID, backendID)
}

func (s *service) RemoveBackend(ctx context.Context, poolID, backendID string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).RemoveBackendFromPool(ctx, poolID, backendID)
}

func (s *service) ListBackends(ctx context.Context, poolID string) ([]*store.Backend, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListBackendsForPool(ctx, poolID)
}

func (s *service) ListPoolsForBackend(ctx context.Context, backendID string) ([]*store.Pool, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListPoolsForBackend(ctx, backendID)
}

func (s *service) AssignModel(ctx context.Context, poolID, modelID string) error {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).AssignModelToPool(ctx, poolID, modelID)
}

func (s *service) RemoveModel(ctx context.Context, poolID, modelID string) error {
	if poolID == serverops.EmbedPoolID {
		return serverops.ErrImmutablePool
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).RemoveModelFromPool(ctx, poolID, modelID)
}

func (s *service) ListModels(ctx context.Context, poolID string) ([]*store.Model, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListModelsForPool(ctx, poolID)
}

func (s *service) ListPoolsForModel(ctx context.Context, modelID string) ([]*store.Pool, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionView); err != nil {
		return nil, err
	}
	return store.New(tx).ListPoolsForModel(ctx, modelID)
}

func (s *service) GetServiceName() string {
	return "poolservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
