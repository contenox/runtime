package llmrepo

import (
	"context"
	"errors"
	"fmt"

	"github.com/contenox/contenox/core/modelprovider"
	"github.com/contenox/contenox/core/runtimestate"
	"github.com/contenox/contenox/core/serverops"
	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
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

func NewExecRepo(ctx context.Context, config *serverops.Config, dbInstance libdb.DBManager, runtime *runtimestate.State) (ModelRepo, error) {
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
	provider, err := e.GetProvider(ctx)

	return func(ctx context.Context, backendTypes ...string) ([]modelprovider.Provider, error) {
		if err != nil {
			return nil, err
		}

		return []modelprovider.Provider{provider}, nil
	}
}

func (e *modelManager) GetProvider(ctx context.Context) (modelprovider.Provider, error) {
	backends := map[string]store.Backend{}

	for _, v := range e.runtime.Get(ctx) {
		ok, err := e.backendIsInPool(ctx, v.Backend)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, lmr := range v.PulledModels {
			if lmr.Model == e.model.Model {
				backends[v.Backend.BaseURL] = v.Backend
			}
		}
	}
	var results []string
	for _, backend := range backends {
		results = append(results, backend.BaseURL)
	}
	if len(results) == 0 {
		return nil, errors.New("no backends found")
	}
	provider := modelprovider.NewOllamaModelProvider(e.model.Model, results,
		modelprovider.WithEmbed(e.embed),
		modelprovider.WithPrompt(e.prompt))
	return provider, nil
}

func (e *modelManager) backendIsInPool(ctx context.Context, backendToVerify store.Backend) (bool, error) {
	backendsConfiguredInPool, err := store.New(e.dbInstance.WithoutTransaction()).ListBackendsForPool(ctx, e.pool.ID)
	if err != nil {
		return false, fmt.Errorf("failed to list backends for pool %s: %w", e.pool.ID, err)
	}

	for _, poolBackend := range backendsConfiguredInPool {
		if poolBackend.BaseURL == backendToVerify.BaseURL {
			return true, nil
		}
	}
	return false, nil
}
