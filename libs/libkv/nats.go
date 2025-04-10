package libkv

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"
)

var _ KVManager = &natsKVManager{}

// natsKVManager is our NATS-backed implementation of KVManager.
type natsKVManager struct {
	nc     *nats.Conn
	js     nats.JetStreamContext
	bucket nats.KeyValue
}

// NewNatsKVManager creates a new KVManager backed by a NATS JetStream Key-Value store.
// 'url' is the NATS server address and 'bucketName' is the name of the KV bucket.
func NewNatsKVManager(url, bucketName string, create bool) (KVManager, error) {
	// Connect to NATS.
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, err
	}

	// Create a JetStream context.
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}

	var kv nats.KeyValue
	if create {
		// Try to create the bucket.
		kv, err = js.CreateKeyValue(&nats.KeyValueConfig{
			Bucket: bucketName,
		})
		if err != nil {
			// If the bucket already exists, open it.
			if errors.Is(err, nats.ErrBucketNotFound) {
				kv, err = js.KeyValue(bucketName)
				if err != nil {
					nc.Close()
					return nil, err
				}
			} else {
				nc.Close()
				return nil, err
			}
		}
	} else {
		kv, err = js.KeyValue(bucketName)
		if err != nil {
			nc.Close()
			return nil, err
		}
	}

	return &natsKVManager{
		nc:     nc,
		js:     js,
		bucket: kv,
	}, nil
}

// Operation returns a KVExec instance which is a thin wrapper around our NATS KV bucket.
func (m *natsKVManager) Operation(ctx context.Context) (KVExec, error) {
	return &natsKVExec{bucket: m.bucket}, nil
}

// Close shuts down the underlying NATS connection.
func (m *natsKVManager) Close() error {
	m.nc.Close()
	return nil
}

// natsKVExec implements the KVExec interface.
type natsKVExec struct {
	bucket nats.KeyValue
}

// Get fetches the value for a given key. If the key is not found, it returns ErrNotFound.
func (e *natsKVExec) Get(ctx context.Context, key []byte) ([]byte, error) {
	k := string(key)
	entry, err := e.bucket.Get(k)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return entry.Value(), nil
}

// Set stores the key/value pair. This implementation always overwrites existing values.
// If your application requires a create-only behavior, you might want to first check for existence.
func (e *natsKVExec) Set(ctx context.Context, keyvalue KeyValue) error {
	k := string(keyvalue.Key)
	// Optionally, check for existence first if you need to avoid overwrites:
	/*
		if _, err := e.bucket.Get(k); err == nil {
			return ErrKeyExists
		} else if !errors.Is(err, nats.ErrKeyNotFound) {
			return err
		}
	*/
	_, err := e.bucket.Put(k, keyvalue.Value)
	if err != nil {
		// Map any error related to value issues as needed.
		return ErrInvalidValue
	}
	return nil
}

// Delete removes the key from the store. Returns ErrNotFound if the key does not exist.
func (e *natsKVExec) Delete(ctx context.Context, key []byte) error {
	k := string(key)
	err := e.bucket.Delete(k)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// Exists checks whether a key exists in the store.
func (e *natsKVExec) Exists(ctx context.Context, key []byte) (bool, error) {
	k := string(key)
	_, err := e.bucket.Get(k)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// List returns all keys stored in the bucket.
func (e *natsKVExec) List(ctx context.Context) ([]string, error) {
	keys, err := e.bucket.Keys()
	if err != nil {
		return nil, err
	}
	return keys, nil
}
