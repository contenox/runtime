package llmrepo

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	libmodelprovider "github.com/contenox/modelprovider"
	"github.com/contenox/modelprovider/llmresolver"

	"github.com/contenox/runtime-mvp/core/ollamatokenizer"
	"github.com/contenox/runtime-mvp/core/runtimestate"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/libs/libdb"
)

type ModelRepo interface {
	GetDefaultSystemProvider(ctx context.Context) (libmodelprovider.Provider, error)
	GetTokenizer(ctx context.Context) (ollamatokenizer.Tokenizer, error)
	GetRuntime(ctx context.Context) llmresolver.ProviderFromRuntimeState
	GetAvailableProviders(ctx context.Context) ([]libmodelprovider.Provider, error)
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

func NewExecRepo(ctx context.Context, config *serverops.Config, dbInstance libdb.DBManager, runtime *runtimestate.State, tokenizer ollamatokenizer.Tokenizer) (ModelRepo, error) {
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
		tokenizer:  tokenizer,
		embed:      false,
		prompt:     true,
	}, com(ctx)
}

type modelManager struct {
	pool       *store.Pool
	model      *store.Model
	dbInstance libdb.DBManager
	runtime    *runtimestate.State
	tokenizer  ollamatokenizer.Tokenizer
	embed      bool
	prompt     bool
}

// GetRuntime implements Embedder.
func (e *modelManager) GetRuntime(ctx context.Context) llmresolver.ProviderFromRuntimeState {
	provider, err := e.GetDefaultSystemProvider(ctx)

	return func(ctx context.Context, backendTypes ...string) ([]libmodelprovider.Provider, error) {
		if err != nil {
			return nil, err
		}

		return []libmodelprovider.Provider{provider}, nil
	}
}

func (e *modelManager) GetDefaultSystemProvider(ctx context.Context) (libmodelprovider.Provider, error) {
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
	provider := libmodelprovider.NewOllamaModelProvider(e.model.Model, results, http.DefaultClient,
		libmodelprovider.WithEmbed(e.embed),
		libmodelprovider.WithPrompt(e.prompt))
	return provider, nil
}

func (e *modelManager) GetTokenizer(ctx context.Context) (ollamatokenizer.Tokenizer, error) {
	if e.tokenizer == nil {
		return nil, errors.New("tokenizer not initialized")
	}
	return e.tokenizer, nil
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

func (e *modelManager) GetAvailableProviders(ctx context.Context) ([]libmodelprovider.Provider, error) {
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

	var providers []libmodelprovider.Provider
	for _, backend := range backends {
		provider := libmodelprovider.NewOllamaModelProvider(
			e.model.Model,
			[]string{backend.BaseURL},
			http.DefaultClient,
			libmodelprovider.WithEmbed(e.embed),
			libmodelprovider.WithPrompt(e.prompt),
		)
		providers = append(providers, provider)
	}
	return providers, nil
}
