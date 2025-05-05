package llmembed

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
)

type Embedder interface {
	GetProvider(ctx context.Context) (modelprovider.Provider, error)
	GetRuntime(ctx context.Context) modelprovider.RuntimeState
}

func New(ctx context.Context, config *serverops.Config, dbInstance libdb.DBManager, runtime *runtimestate.State) (Embedder, error) {
	tx, com, r, err := dbInstance.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer r()

	pool, err := initPool(ctx, config, tx, false)
	if err != nil {
		return nil, fmt.Errorf("init pool: %w", err)
	}
	model, err := initModel(ctx, config, tx, false)
	if err != nil {
		return nil, fmt.Errorf("init model: %w", err)
	}
	err = assignModelToPool(ctx, config, tx, model, pool)
	if err != nil {
		return nil, fmt.Errorf("assign model to pool: %w", err)
	}
	err = initCredentials(ctx, config, tx)
	if err != nil {
		return nil, fmt.Errorf("init credentials: %w", err)
	}
	return &embedder{
		pool:       pool,
		model:      model,
		dbInstance: dbInstance,
		runtime:    runtime,
	}, com(ctx)
}

type embedder struct {
	pool       *store.Pool
	model      *store.Model
	dbInstance libdb.DBManager
	runtime    *runtimestate.State
}

// GetRuntime implements Embedder.
func (e *embedder) GetRuntime(ctx context.Context) modelprovider.RuntimeState {
	return modelprovider.ModelProviderAdapter(ctx, e.runtime.Get(ctx))
}

func (e *embedder) GetProvider(ctx context.Context) (modelprovider.Provider, error) {
	adapter := modelprovider.ModelProviderAdapter(ctx, e.runtime.Get(ctx))
	providers, err := adapter(ctx, "Ollama")
	if err != nil {
		return nil, fmt.Errorf("unexpected error: %v", err)
	}
	if len(providers) == 0 {
		return nil, errors.New("no providers found")
	}
	backends, err := store.New(e.dbInstance.WithoutTransaction()).ListBackendsForPool(ctx, serverops.EmbedPoolID)
	if err != nil {
		return nil, fmt.Errorf("failed to list backends: %v", err)
	}
	backendsConv := map[string]struct{}{}
	for _, provider := range providers {
		backendsInRuntime := provider.GetBackendIDs()
		for _, backend := range backendsInRuntime {
			backendsConv[backend] = struct{}{}
		}
	}
	var results []string
	for _, backend := range backends {
		if _, ok := backendsConv[backend.BaseURL]; ok {
			results = append(results, backend.BaseURL)
		}
	}
	if len(results) == 0 {
		return nil, errors.New("no backends found")
	}
	provider := modelprovider.NewOllamaModelProvider(e.model.Model, results, modelprovider.WithEmbed(true))

	return provider, nil
}

func initPool(ctx context.Context, config *serverops.Config, tx libdb.Exec, created bool) (*store.Pool, error) {
	pool, err := store.New(tx).GetPool(ctx, serverops.EmbedPoolID)
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = store.New(tx).CreatePool(ctx, &store.Pool{
			ID:          serverops.EmbedPoolID,
			Name:        serverops.EmbedPoolName,
			PurposeType: "Internal Embeddings",
		})
		if err != nil {
			return nil, err
		}
		return initPool(ctx, config, tx, true)
	}
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func initModel(ctx context.Context, config *serverops.Config, tx libdb.Exec, created bool) (*store.Model, error) {
	tenantID, err := uuid.Parse(serverops.TenantID)
	if err != nil {
		return nil, err
	}
	modelID := uuid.NewSHA1(tenantID, []byte(config.EmbedModel))
	storeInstance := store.New(tx)

	model, err := storeInstance.GetModel(ctx, modelID.String())
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
		return initModel(ctx, config, tx, true)
	}
	return model, nil
}

func assignModelToPool(ctx context.Context, _ *serverops.Config, tx libdb.Exec, model *store.Model, pool *store.Pool) error {
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

func initCredentials(ctx context.Context, config *serverops.Config, tx libdb.Exec) error {
	storeInstance := store.New(tx)
	passwordHash, salt, err := serverops.NewPasswordHash(config.WorkerUserPassword, config.SigningKey)
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
		Resource:     serverops.DefaultServerGroup, // TODO: reduce privilege
		ResourceType: serverops.DefaultServerGroup,
		Permission:   store.PermissionManage,
	})
	if err != nil {
		return err
	}
	err = storeInstance.CreateAccessEntry(ctx, &store.AccessEntry{
		ID:           config.WorkerUserAccountID + "2",
		Identity:     config.WorkerUserAccountID,
		Resource:     "files",
		ResourceType: "files",
		Permission:   store.PermissionView,
	})
	if err != nil {
		return err
	}
	return nil
}
