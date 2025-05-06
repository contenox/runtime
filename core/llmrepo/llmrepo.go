package llmrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/js402/cate/core/modelprovider"
	"github.com/js402/cate/core/runtimestate"
	"github.com/js402/cate/core/serverops"
	"github.com/js402/cate/core/serverops/store"
	"github.com/js402/cate/libs/libdb"
)

type ModelRepo interface {
	GetProvider(ctx context.Context) (modelprovider.Provider, error)
	GetRuntime(ctx context.Context) modelprovider.RuntimeState
}

func NewEmbedder(ctx context.Context, config *serverops.Config, dbInstance libdb.DBManager, runtime *runtimestate.State) (ModelRepo, error) {
	tx, com, r, err := dbInstance.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer r()

	pool, err := serverops.InitEmbedPool(ctx, config, tx, false)
	if err != nil {
		return nil, fmt.Errorf("init pool: %w", err)
	}
	model, err := serverops.InitEmbedModel(ctx, config, tx, false)
	if err != nil {
		return nil, fmt.Errorf("init model: %w", err)
	}
	err = serverops.AssignModelToPool(ctx, config, tx, model, pool)
	if err != nil {
		return nil, fmt.Errorf("assign model to pool: %w", err)
	}
	err = serverops.InitCredentials(ctx, config, tx)
	if err != nil {
		return nil, fmt.Errorf("init credentials: %w", err)
	}
	return &modelManager{
		pool:       pool,
		model:      model,
		dbInstance: dbInstance,
		runtime:    runtime,
		embed:      true,
		prompt:     false,
	}, com(ctx)
}

func NewTaskEngine(ctx context.Context, config *serverops.Config, dbInstance libdb.DBManager, runtime *runtimestate.State) (ModelRepo, error) {
	tx, com, r, err := dbInstance.WithTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer r()

	pool, err := serverops.InitEmbedPool(ctx, config, tx, false)
	if err != nil {
		return nil, fmt.Errorf("init pool: %w", err)
	}
	model, err := serverops.InitTasksModel(ctx, config, tx, false)
	if err != nil {
		return nil, fmt.Errorf("init model: %w", err)
	}
	err = serverops.AssignModelToPool(ctx, config, tx, model, pool)
	if err != nil {
		return nil, fmt.Errorf("assign model to pool: %w", err)
	}
	err = serverops.InitCredentials(ctx, config, tx)
	if err != nil {
		return nil, fmt.Errorf("init credentials: %w", err)
	}
	return &modelManager{
		pool:       pool,
		model:      model,
		dbInstance: dbInstance,
		runtime:    runtime,
		embed:      false,
		prompt:     true,
	}, com(ctx)
}

type modelManager struct {
	pool       *store.Pool
	model      *store.Model
	dbInstance libdb.DBManager
	runtime    *runtimestate.State
	embed      bool
	prompt     bool
}

// GetRuntime implements Embedder.
func (e *modelManager) GetRuntime(ctx context.Context) modelprovider.RuntimeState {
	return modelprovider.ModelProviderAdapter(ctx, e.runtime.Get(ctx))
}

func (e *modelManager) GetProvider(ctx context.Context) (modelprovider.Provider, error) {
	adapter := modelprovider.ModelProviderAdapter(ctx, e.runtime.Get(ctx))
	providers, err := adapter(ctx, "Ollama")
	if err != nil {
		return nil, fmt.Errorf("unexpected error: %v", err)
	}
	if len(providers) == 0 {
		return nil, errors.New("no providers found")
	}
	backends, err := store.New(e.dbInstance.WithoutTransaction()).ListBackendsForPool(ctx, e.pool.ID)
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
	provider := modelprovider.NewOllamaModelProvider(e.model.Model, results,
		modelprovider.WithEmbed(e.embed),
		modelprovider.WithPrompt(e.prompt))
	return provider, nil
}
