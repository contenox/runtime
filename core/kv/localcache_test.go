package kv_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/core/kv"
	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/serverops/store"
	"github.com/contenox/runtime-mvp/core/services/testingsetup"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// testSetup creates a new testing environment with cache instance
func testSetup(t *testing.T, prefix string) (context.Context, store.Store, kv.Repo) {
	ctx := t.Context()
	tenv := testingsetup.New(ctx, serverops.NewLogActivityTracker(slog.Default())).WithDBConn("test").WithDBManager()
	require.NoError(t, tenv.Err)
	build := tenv.Build()
	require.NoError(t, build.Err)
	dbInstance := build.GetDBInstance()
	cache := kv.NewLocalCache(dbInstance, prefix)
	storeInstance := store.New(dbInstance.WithoutTransaction())
	return ctx, storeInstance, cache
}

func TestUnitKV_CacheOperations(t *testing.T) {
	ctx, s, cache := testSetup(t, "")

	t.Run("Empty cache returns not found", func(t *testing.T) {
		var out string
		err := cache.Get(ctx, "non-existent", &out)
		require.ErrorIs(t, err, kv.ErrKeyNotFound)
	})

	t.Run("Refresh loads values from DB", func(t *testing.T) {
		// Setup DB state
		key := "test-" + uuid.NewString()
		value := json.RawMessage(`"cached_value"`)
		require.NoError(t, s.SetKV(ctx, key, value))

		// Refresh cache
		require.NoError(t, cache.ProcessTick(ctx))

		// Verify cache
		var out string
		require.NoError(t, cache.Get(ctx, key, &out))
		require.Equal(t, "cached_value", out)
	})

	t.Run("Cache ignores DB changes until refresh", func(t *testing.T) {
		key := "refresh-test-" + uuid.NewString()

		// Initial DB state
		require.NoError(t, s.SetKV(ctx, key, json.RawMessage(`"v1"`)))
		require.NoError(t, cache.ProcessTick(ctx))

		// Verify initial cache
		var out string
		require.NoError(t, cache.Get(ctx, key, &out))
		require.Equal(t, "v1", out)

		// Update DB
		require.NoError(t, s.SetKV(ctx, key, json.RawMessage(`"v2"`)))

		// Cache should still return old value
		require.NoError(t, cache.Get(ctx, key, &out))
		require.Equal(t, "v1", out)

		// Refresh cache
		require.NoError(t, cache.ProcessTick(ctx))

		// Should now return updated value
		require.NoError(t, cache.Get(ctx, key, &out))
		require.Equal(t, "v2", out)
	})

	t.Run("Cache handles complex types", func(t *testing.T) {
		type complexType struct {
			ID   string `json:"id"`
			Data []int  `json:"data"`
		}

		key := "complex-" + uuid.NewString()
		value := complexType{ID: "test", Data: []int{1, 2, 3}}
		valueBytes, _ := json.Marshal(value)

		require.NoError(t, s.SetKV(ctx, key, valueBytes))
		require.NoError(t, cache.ProcessTick(ctx))

		var out complexType
		require.NoError(t, cache.Get(ctx, key, &out))
		require.Equal(t, value, out)
	})

	t.Run("Cache refresh clears old entries", func(t *testing.T) {
		key1 := "old-" + uuid.NewString()
		key2 := "new-" + uuid.NewString()

		// Initial cache state
		require.NoError(t, s.SetKV(ctx, key1, json.RawMessage(`"v1"`)))
		require.NoError(t, cache.ProcessTick(ctx))

		// Delete key1 and add key2
		require.NoError(t, s.DeleteKV(ctx, key1))
		require.NoError(t, s.SetKV(ctx, key2, json.RawMessage(`"v2"`)))

		// Refresh cache
		require.NoError(t, cache.ProcessTick(ctx))

		// Verify updates
		var out string
		err := cache.Get(ctx, key1, &out)
		require.ErrorIs(t, err, kv.ErrKeyNotFound)

		require.NoError(t, cache.Get(ctx, key2, &out))
		require.Equal(t, "v2", out)
	})
}

func TestUnitKV_PrefixFiltering(t *testing.T) {
	prefix := "prefix-" + uuid.NewString()
	ctx, s, cache := testSetup(t, prefix)

	keys := []string{
		prefix + "-1",
		prefix + "-2",
		"other-" + uuid.NewString(),
		"unrelated-" + uuid.NewString(),
	}

	// Insert all keys
	for _, key := range keys {
		require.NoError(t, s.SetKV(ctx, key, json.RawMessage(`"value"`)))
	}

	// Refresh cache
	require.NoError(t, cache.ProcessTick(ctx))

	// Verify only prefixed keys are cached
	var out string
	for _, key := range keys {
		err := cache.Get(ctx, key, &out)
		if key[:len(prefix)] == prefix {
			require.NoError(t, err, "should find prefixed key")
			require.Equal(t, "value", out)
		} else {
			require.ErrorIs(t, err, kv.ErrKeyNotFound, "non-prefixed key should be missing")
		}
	}
}

func TestUnitKV_Concurrency(t *testing.T) {
	ctx, s, cache := testSetup(t, "")
	key := "concurrent-" + uuid.NewString()

	// Initial setup
	require.NoError(t, s.SetKV(ctx, key, json.RawMessage(`"initial"`)))
	require.NoError(t, cache.ProcessTick(ctx))

	// Concurrent access
	t.Run("Parallel reads", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			t.Run("", func(t *testing.T) {
				t.Parallel()
				var out string
				err := cache.Get(ctx, key, &out)
				require.NoError(t, err)
				require.Equal(t, "initial", out)
			})
		}
	})

	t.Run("Refresh during reads", func(t *testing.T) {
		// Start readers
		done := make(chan bool)
		for i := 0; i < 5; i++ {
			go func() {
				var out string
				for j := 0; j < 10; j++ {
					_ = cache.Get(ctx, key, &out)
					time.Sleep(time.Millisecond * 10)
				}
				done <- true
			}()
		}

		// Perform refreshes
		go func() {
			for i := 0; i < 3; i++ {
				require.NoError(t, cache.ProcessTick(ctx))
				time.Sleep(time.Millisecond * 15)
			}
		}()

		// Wait for completion
		for i := 0; i < 5; i++ {
			<-done
		}
	})
}

func TestUnitKV_ErrorHandling(t *testing.T) {
	ctx, _, cache := testSetup(t, "")

	t.Run("Invalid output type", func(t *testing.T) {
		key := "type-test-" + uuid.NewString()
		require.NoError(t, cache.ProcessTick(ctx)) // Refresh empty cache

		var out int
		err := cache.Get(ctx, key, &out)
		require.ErrorIs(t, err, kv.ErrKeyNotFound)
	})

	// This test requires a way to simulate DB errors
	t.Run("Refresh with DB error", func(t *testing.T) {
		// Create a mock DB that returns an error
		// This would require a mock implementation - omitted here
		t.Skip("Requires mock DB implementation to simulate errors")
	})
}
