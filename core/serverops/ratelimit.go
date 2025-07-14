package serverops

import (
	"context"
	"errors"
	"time"

	"github.com/contenox/runtime-mvp/libs/libkv"
)

type RateLimiter struct {
	kvManager libkv.KVManager
}

func NewRateLimiter(kv libkv.KVManager) *RateLimiter {
	return &RateLimiter{kvManager: kv}
}

// Allow checks whether a request should be allowed based on approximate rate limiting.
// This implementation uses a Redis-like list to track recent events and approximates
// a sliding window. Under high concurrency, it may allow more than `limit` requests
// in the `window` due to lack of atomicity. Suitable for brute-force protection.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	op, err := r.kvManager.Operation(ctx)
	if err != nil {
		return false, err
	}

	bucketKey := "ratelimit:" + key
	now := time.Now().UTC()
	windowEdge := now.Add(-window)

	if err := op.LPush(ctx, []byte(bucketKey), []byte(now.Format(time.RFC3339Nano))); err != nil {
		return false, err
	}

	if err := op.LTrim(ctx, []byte(bucketKey), 0, int64(limit)-1); err != nil {
		return false, err
	}

	events, err := op.LRange(ctx, []byte(bucketKey), 0, -1)
	if errors.Is(err, libkv.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	if len(events) > 0 {
		eventTime, err := time.Parse(time.RFC3339Nano, string(events[len(events)-1]))
		if err != nil {
			return false, err
		}
		if eventTime.After(windowEdge) {
			return false, nil
		}
	}
	return true, nil
}
