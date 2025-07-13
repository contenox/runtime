package libkv_test

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/contenox/runtime-mvp/libs/libkv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/valkey"
)

// SetupLocalValKeyInstance is provided by the user
func SetupLocalValKeyInstance(ctx context.Context) (string, testcontainers.Container, func(), error) {
	cleanup := func() {}

	container, err := valkey.Run(ctx, "docker.io/valkey/valkey:7.2.5")
	if err != nil {
		return "", nil, cleanup, err
	}

	cleanup = func() {
		timeout := time.Second
		err := container.Stop(ctx, &timeout)
		if err != nil {
			panic(err)
		}
	}

	conn, err := container.ConnectionString(ctx)
	if err != nil {
		return "", nil, cleanup, err
	}
	return conn, container, cleanup, nil
}

func TestValkeyCRUD(t *testing.T) {
	ctx := context.Background()

	connStr, _, cleanup, err := SetupLocalValKeyInstance(ctx)
	require.NoError(t, err)
	defer cleanup()
	// Parse connection string properly
	u, err := url.Parse(connStr)
	require.NoError(t, err)

	// Extract host:port
	addr := u.Host // e.g., "localhost:32769"
	cfg := libkv.Config{
		Addr:     addr,
		Password: "",
	}
	manager, err := libkv.NewManager(cfg, 10*time.Second)
	require.NoError(t, err)
	defer manager.Close()

	kv, err := manager.Operation(ctx)
	require.NoError(t, err)

	key := []byte("testkey")
	value := []byte("testvalue")

	// Test Set
	err = kv.Set(ctx, libkv.KeyValue{
		Key:   key,
		Value: value,
	})
	require.NoError(t, err)

	// Test Get
	retrieved, err := kv.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, retrieved)

	// Test Exists
	exists, err := kv.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)

	// Test Delete
	err = kv.Delete(ctx, key)
	require.NoError(t, err)

	// Test Get after Delete
	_, err = kv.Get(ctx, key)
	assert.ErrorIs(t, err, libkv.ErrNotFound)

	// Test Exists after Delete
	exists, err = kv.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestValkeyTTL(t *testing.T) {
	ctx := context.Background()

	connStr, _, cleanup, err := SetupLocalValKeyInstance(ctx)
	require.NoError(t, err)
	defer cleanup()
	// Parse connection string properly
	u, err := url.Parse(connStr)
	require.NoError(t, err)

	// Extract host:port
	addr := u.Host // e.g., "localhost:32769"
	cfg := libkv.Config{
		Addr:     addr,
		Password: "",
	}
	manager, err := libkv.NewManager(cfg, 10*time.Second)
	require.NoError(t, err)
	defer manager.Close()

	kv, err := manager.Operation(ctx)
	require.NoError(t, err)

	key := []byte("ttlkey")
	value := []byte("ttlvalue")

	// Set with TTL
	ttl := time.Now().Add(2 * time.Second)
	err = kv.Set(ctx, libkv.KeyValue{
		Key:   key,
		Value: value,
		TTL:   ttl,
	})
	require.NoError(t, err)

	// Wait for TTL to expire
	time.Sleep(3 * time.Second)

	// Test Get after TTL
	_, err = kv.Get(ctx, key)
	assert.ErrorIs(t, err, libkv.ErrNotFound)
}

func TestValkeyList(t *testing.T) {
	ctx := context.Background()

	connStr, _, cleanup, err := SetupLocalValKeyInstance(ctx)
	require.NoError(t, err)
	defer cleanup()
	// Parse connection string properly
	u, err := url.Parse(connStr)
	require.NoError(t, err)

	// Extract host:port
	addr := u.Host // e.g., "localhost:32769"
	cfg := libkv.Config{
		Addr:     addr,
		Password: "",
	}
	manager, err := libkv.NewManager(cfg, 10*time.Second)
	require.NoError(t, err)
	defer manager.Close()

	kv, err := manager.Operation(ctx)
	require.NoError(t, err)

	// Set multiple keys
	keys := [][]byte{
		[]byte("key1"),
		[]byte("key2"),
		[]byte("key3"),
	}
	for _, key := range keys {
		err := kv.Set(ctx, libkv.KeyValue{
			Key:   key,
			Value: []byte("value"),
		})
		require.NoError(t, err)
	}

	// List keys
	listed, err := kv.List(ctx)
	require.NoError(t, err)

	// Convert to map for easy comparison
	listedMap := make(map[string]bool)
	for _, k := range listed {
		listedMap[k] = true
	}

	for _, key := range keys {
		assert.True(t, listedMap[string(key)])
	}
}
