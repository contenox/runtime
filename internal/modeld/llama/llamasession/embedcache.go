package llamasession

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/contenox/runtime/internal/modeld/llama"
)

type embedModelKey struct {
	path   string
	config string
}

type cachedEmbedModel[T interface{ Close() }] struct {
	model T
	mu    sync.Mutex
}

type embedModelCache[T interface{ Close() }] struct {
	lifecycle sync.RWMutex
	mu        sync.Mutex
	models    map[embedModelKey]*cachedEmbedModel[T]
}

func (c *embedModelCache[T]) acquire(modelPath string, cfg llama.Config, load func() (T, error)) (*cachedEmbedModel[T], func(), error) {
	config, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("llama embed: cache config: %w", err)
	}
	key := embedModelKey{path: modelPath, config: string(config)}
	c.lifecycle.RLock()
	c.mu.Lock()
	em := c.models[key]
	if em == nil {
		model, err := load()
		if err != nil {
			c.mu.Unlock()
			c.lifecycle.RUnlock()
			return nil, nil, err
		}
		em = &cachedEmbedModel[T]{model: model}
		if c.models == nil {
			c.models = make(map[embedModelKey]*cachedEmbedModel[T])
		}
		c.models[key] = em
	}
	c.mu.Unlock()
	em.mu.Lock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			em.mu.Unlock()
			c.lifecycle.RUnlock()
		})
	}
	return em, release, nil
}

func (c *embedModelCache[T]) close() error {
	c.lifecycle.Lock()
	defer c.lifecycle.Unlock()
	for key, em := range c.models {
		em.model.Close()
		delete(c.models, key)
	}
	return nil
}
