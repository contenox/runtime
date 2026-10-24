package libkvstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("key not found")
	ErrInvalidValue     = errors.New("invalid value for operation")
	ErrKeyExists        = errors.New("key already exists")
	ErrTxFailed         = errors.New("transaction failed")
	ErrTxConflict       = errors.New("transaction conflict")
	ErrLockUnavailable  = errors.New("lock unavailable")
	ErrConnectionFailed = errors.New("connection failed")
)

type Key = string

type KeyValue struct {
	Key   string
	Value json.RawMessage
	TTL   time.Time
}

type KVManager interface {
	Executor(ctx context.Context) (KVExecutor, error)
	Close() error
}

type KVExecutor interface {
	Get(ctx context.Context, key Key) (json.RawMessage, error)
	Set(ctx context.Context, key Key, value json.RawMessage) error
	SetWithTTL(ctx context.Context, key Key, value json.RawMessage, ttl time.Duration) error
	Delete(ctx context.Context, key Key) error
	Exists(ctx context.Context, key Key) (bool, error)
	Keys(ctx context.Context, pattern string) ([]Key, error)

	ListPush(ctx context.Context, key Key, value json.RawMessage) error
	ListRange(ctx context.Context, key Key, start, stop int64) ([]json.RawMessage, error)
	ListTrim(ctx context.Context, key Key, start, stop int64) error
	ListLength(ctx context.Context, key Key) (int64, error)
	ListRPop(ctx context.Context, key Key) (json.RawMessage, error)

	SetAdd(ctx context.Context, key Key, member json.RawMessage) error
	SetMembers(ctx context.Context, key Key) ([]json.RawMessage, error)
	SetRemove(ctx context.Context, key Key, member json.RawMessage) error
}
