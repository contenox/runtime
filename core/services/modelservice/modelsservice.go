package modelservice

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
)

var (
	ErrInvalidModel = errors.New("invalid model data")
)

type Service struct {
	dbInstance              libdb.DBManager
	immutableEmbedModelName string
}

func New(db libdb.DBManager, config *serverops.Config) *Service {
	return &Service{
		dbInstance:              db,
		immutableEmbedModelName: config.EmbedModel,
	}
}

func (s *Service) Append(ctx context.Context, model *store.Model) error {
	if err := validate(model); err != nil {
		return err
	}
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return err
	}
	return store.New(tx).AppendModel(ctx, model)
}

func (s *Service) List(ctx context.Context) ([]*store.Model, error) {
	tx := s.dbInstance.WithoutTransaction()
	if err := serverops.CheckServiceAuthorization(ctx, store.New(tx), s, store.PermissionManage); err != nil {
		return nil, err
	}
	return store.New(tx).ListModels(ctx)
}

func (s *Service) Delete(ctx context.Context, modelName string) error {
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

func (s *Service) GetServiceName() string {
	return "modelservice"
}

func (s *Service) GetServiceGroup() string {
	return serverops.DefaultDefaultServiceGroup
}
