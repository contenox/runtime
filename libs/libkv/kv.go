package libkv

import (
	"context"
	"errors"
)

var (
	ErrNotFound = errors.New("key not found")

	ErrInvalidValue = errors.New("invalid value for operation")
	ErrKeyExists    = errors.New("key already exists")
)

// KVManager provides an interface to work with the key-value store.
type KVManager interface {
	Operation(ctx context.Context) (KVExec, error)
	// Close shuts down the underlying key-value store.
	Close() error
}

type KeyValue struct {
	Key   []byte
	Value []byte
}

// KVExec represents the basic key-value operations.
type KVExec interface {
	Get(ctx context.Context, key []byte) ([]byte, error)
	Set(ctx context.Context, keyvalue KeyValue) error
	Delete(ctx context.Context, key []byte) error
	Exists(ctx context.Context, key []byte) (bool, error)
	List(ctx context.Context) ([]string, error)
}
