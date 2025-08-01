package serverops

import (
	"context"
	"errors"
	"fmt"

	libdb "github.com/contenox/dbexec"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/google/uuid"
)

func InitEmbedPool(ctx context.Context, config *Config, tx libdb.Exec, created bool) (*store.Pool, error) {
	pool, err := store.New(tx).GetPool(ctx, EmbedPoolID)
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = store.New(tx).CreatePool(ctx, &store.Pool{
			ID:          EmbedPoolID,
			Name:        EmbedPoolName,
			PurposeType: "Internal Embeddings",
		})
		if err != nil {
			return nil, err
		}
		return InitEmbedPool(ctx, config, tx, true)
	}
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func InitTasksPool(ctx context.Context, config *Config, tx libdb.Exec, created bool) (*store.Pool, error) {
	pool, err := store.New(tx).GetPool(ctx, TasksPoolID)
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = store.New(tx).CreatePool(ctx, &store.Pool{
			ID:          TasksPoolID,
			Name:        TasksPoolName,
			PurposeType: "Internal Tasks",
		})
		if err != nil {
			return nil, err
		}
		return InitTasksPool(ctx, config, tx, true)
	}
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func InitEmbedModel(ctx context.Context, config *Config, tx libdb.Exec, created bool) (*store.Model, error) {
	tenantID, err := uuid.Parse(TenantID)
	if err != nil {
		return nil, err
	}
	modelID := uuid.NewSHA1(tenantID, []byte(config.EmbedModel))
	storeInstance := store.New(tx)

	model, err := storeInstance.GetModelByName(ctx, config.EmbedModel)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return nil, fmt.Errorf("get model: %w", err)
	}
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = storeInstance.AppendModel(ctx, &store.Model{
			Model: config.EmbedModel,
			ID:    modelID.String(),
		})
		if err != nil {
			return nil, err
		}
		return InitEmbedModel(ctx, config, tx, true)
	}
	return model, nil
}

func InitTasksModel(ctx context.Context, config *Config, tx libdb.Exec, created bool) (*store.Model, error) {
	tenantID, err := uuid.Parse(TenantID)
	if err != nil {
		return nil, err
	}
	modelID := uuid.NewSHA1(tenantID, []byte(config.TasksModel))
	storeInstance := store.New(tx)

	model, err := storeInstance.GetModelByName(ctx, config.TasksModel)
	if err != nil && !errors.Is(err, libdb.ErrNotFound) {
		return nil, fmt.Errorf("get model: %w", err)
	}
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = storeInstance.AppendModel(ctx, &store.Model{
			Model: config.TasksModel,
			ID:    modelID.String(),
		})
		if err != nil {
			return nil, err
		}
		return InitTasksModel(ctx, config, tx, true)
	}
	return model, nil
}

func AssignModelToPool(ctx context.Context, _ *Config, tx libdb.Exec, model *store.Model, pool *store.Pool) error {
	storeInstance := store.New(tx)

	models, err := storeInstance.ListModelsForPool(ctx, pool.ID)
	if err != nil {
		return err
	}
	for _, presentModel := range models {
		if presentModel.ID == model.ID {
			return nil
		}
	}
	if err := storeInstance.AssignModelToPool(ctx, pool.ID, model.ID); err != nil {
		return err
	}
	return nil
}

func InitCredentials(ctx context.Context, config *Config, tx libdb.Exec) error {
	storeInstance := store.New(tx)
	passwordHash, salt, err := NewPasswordHash(config.WorkerUserPassword, config.SigningKey)
	if err != nil {
		return err
	}
	entries, err := storeInstance.GetAccessEntriesByIdentity(ctx, config.WorkerUserAccountID)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return nil
	}

	err = storeInstance.CreateUser(ctx, &store.User{
		Email:          config.WorkerUserEmail,
		ID:             config.WorkerUserAccountID,
		Subject:        config.WorkerUserAccountID,
		FriendlyName:   "Internal Worker Account",
		HashedPassword: passwordHash,
		Salt:           salt,
	})
	if err != nil {
		return err
	}
	err = storeInstance.CreateAccessEntry(ctx, &store.AccessEntry{
		ID:           config.WorkerUserAccountID + "1",
		Identity:     config.WorkerUserAccountID,
		Resource:     DefaultServerGroup, // TODO: reduce privilege
		ResourceType: DefaultServerGroup,
		Permission:   store.PermissionManage,
	})
	if err != nil {
		return err
	}
	err = storeInstance.CreateAccessEntry(ctx, &store.AccessEntry{
		ID:           config.WorkerUserAccountID + "2",
		Identity:     config.WorkerUserAccountID,
		Resource:     store.ResourceTypeFiles,
		ResourceType: store.ResourceTypeSystem,
		Permission:   store.PermissionView,
	})
	if err != nil {
		return err
	}
	return nil
}
