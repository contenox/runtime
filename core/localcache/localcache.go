package localcache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/contenox/contenox/core/serverops/store"
	"github.com/contenox/contenox/libs/libdb"
)

// ErrKeyNotFound is returned when a key is not found in the cache.
var ErrKeyNotFound = errors.New("key not found")

type data struct {
	Value []byte
	Added time.Time
}

type Config struct {
	mu         sync.RWMutex
	cache      map[string]data
	dbInstance libdb.DBManager
	prefix     string
}

func NewRuntimeConfig(dbInstance libdb.DBManager, prefix string) *Config {
	if prefix == "*" {
		prefix = ""
	}
	return &Config{
		cache:      make(map[string]data),
		dbInstance: dbInstance,
		prefix:     prefix,
	}
}

// Get returns the cached value directly without checking DB.
// Assumes the cache is always warm and up-to-date (via ProcessTick).
func (r *Config) Get(ctx context.Context, key string, out any) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cached, ok := r.cache[key]
	if !ok {
		return ErrKeyNotFound
	}

	return json.Unmarshal(cached.Value, out)
}

// ProcessTick fully replaces the cache by fetching all KV pairs from the DB.
func (r *Config) ProcessTick(ctx context.Context) error {
	storeInstance := store.New(r.dbInstance.WithoutTransaction())
	var kvPairs []*store.KV
	var err error
	if r.prefix != "" {
		kvPairs, err = storeInstance.ListKVPrefix(ctx, r.prefix)
	} else {
		kvPairs, err = storeInstance.ListKV(ctx)
	}
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.cache = make(map[string]data)
	now := time.Now().UTC()
	for _, kv := range kvPairs {
		r.cache[kv.Key] = data{
			Value: kv.Value,
			Added: now,
		}
	}

	return nil
}
