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
	pool, err := initPool(ctx, config, dbInstance, false)
	if err != nil {
		return nil, err
	}
	model, err := initModel(ctx, config, dbInstance, false)
	if err != nil {
		return nil, err
	}
	err = assignModelToPool(ctx, config, dbInstance, model, pool)
	if err != nil {
		return nil, err
	}
	return &embedder{
		pool:       pool,
		model:      model,
		dbInstance: dbInstance,
		runtime:    runtime,
	}, nil
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
	poolID := e.pool.ID
	backends, err := store.New(e.dbInstance.WithoutTransaction()).ListBackendsForPool(ctx, poolID)
	if err != nil {
		return nil, err
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
		if _, ok := backendsConv[backend.BaseURL]; !ok {
			results = append(results, backend.BaseURL)
		}
	}
	if len(results) == 0 {
		return nil, errors.New("no backends found")
	}
	provider := modelprovider.NewOllamaModelProvider("embed", results, modelprovider.WithEmbed(true))

	return provider, nil
}

func initPool(ctx context.Context, config *serverops.Config, dbInstance libdb.DBManager, created bool) (*store.Pool, error) {
	pool, err := store.New(dbInstance.WithoutTransaction()).GetPool(ctx, serverops.EmbedPoolID)
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = store.New(dbInstance.WithoutTransaction()).CreatePool(ctx, &store.Pool{
			ID:          serverops.EmbedPoolID,
			Name:        serverops.EmbedPoolName,
			PurposeType: "Internal Embeddings",
		})
		if err != nil {
			return nil, err
		}
		return initPool(ctx, config, dbInstance, true)
	}
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func initModel(ctx context.Context, config *serverops.Config, dbInstance libdb.DBManager, created bool) (*store.Model, error) {
	tenantID, err := uuid.Parse(serverops.TenantID)
	if err != nil {
		return nil, err
	}
	modelID := uuid.NewSHA1(tenantID, []byte(config.EmbedModel))
	model, err := store.New(dbInstance.WithoutTransaction()).GetModel(ctx, modelID.String())
	if err != nil {
		return nil, err
	}
	if !created && errors.Is(err, libdb.ErrNotFound) {
		err = store.New(dbInstance.WithoutTransaction()).AppendModel(ctx, &store.Model{
			Model: config.EmbedModel,
			ID:    modelID.String(),
		})
		if err != nil {
			return nil, err
		}
		return initModel(ctx, config, dbInstance, true)
	}
	return model, nil
}

func assignModelToPool(ctx context.Context, _ *serverops.Config, dbInstance libdb.DBManager, model *store.Model, pool *store.Pool) error {
	models, err := store.New(dbInstance.WithoutTransaction()).ListModelsForPool(ctx, pool.ID)
	if err != nil {
		return err
	}
	for _, presentModel := range models {
		if presentModel.ID == model.ID {
			return nil
		}
	}
	if err := store.New(dbInstance.WithoutTransaction()).AssignModelToPool(ctx, model.ID, pool.ID); err != nil {
		return err
	}
	return nil
}
