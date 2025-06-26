package modelservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

var ErrInvalidModel = errors.New("invalid model data")

type service struct {
	dbInstance              libdb.DBManager
	immutableEmbedModelName string
}

type Service interface {
	serverops.ServiceMeta

	serverops.ServiceMeta

	Append(ctx context.Context, model *store.Model) error
	List(ctx context.Context) ([]*store.Model, error)
	Delete(ctx context.Context, modelName string) error
}

func New(db libdb.DBManager, config *serverops.Config) Service {
	return &service{
		dbInstance:              db,
		immutableEmbedModelName: config.EmbedModel,
	}
}

func (s *service) Append(ctx context.Context, model *store.Model) error {
	if err := validate(model); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).AppendModel(ctx, model)
}

func (s *service) List(ctx context.Context) ([]*store.Model, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	return store.New(tx).ListModels(ctx)
}

func (s *service) Delete(ctx context.Context, modelName string) error {
	tx := s.dbInstance.WithoutTransaction()
	if modelName == s.immutableEmbedModelName {
		return serverops.ErrImmutableModel
	}
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).DeleteModel(ctx, modelName)
}

func validate(model *store.Model) error {
	if model.Model == "" {
		return fmt.Errorf("%w: model name is required", ErrInvalidModel)
	}
	return nil
}

func (s *service) GetServiceName() string {
	return "modelservice"
}

func (s *service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
