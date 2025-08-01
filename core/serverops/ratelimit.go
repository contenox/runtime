package serverops

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/contenox/runtime-mvp/libs/libkv"
)

const bucketKey = "ratelimiter"

type RateLimiter struct {
	kvManager libkv.KVManager
}

func NewRateLimiter(kv libkv.KVManager) *RateLimiter {
	return &RateLimiter{kvManager: kv}
}

type Event struct {
	Time time.Time `json:"time"`
	Key  string    `json:"key"`
}

// Allow checks whether a request should be allowed based on approximate rate limiting.
// This implementation tracks recent events and approximates a sliding window.
// Under high concurrency, with multiple nodes it may allow more than `limit` requests.
// This implementation is lock-free.
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	op, err := r.kvManager.Executor(ctx)
	if err != nil {
		return false, err
	}

	now := time.Now().UTC()
	windowEdge := now.Add(-window)
	event := Event{Time: now, Key: key}

	b, err := json.Marshal(event)
	if err != nil {
		return false, err
	}

	// Add new event (atomic)
	if err := op.ListPush(ctx, bucketKey, b); err != nil {
		return false, err
	}

	// Get all events (it's ok that this isn't atomic with LPush)
	events, err := op.ListRange(ctx, bucketKey, 0, -1)
	if errors.Is(err, libkv.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	// Process events in reverse (newest first) for efficiency
	recentCount := 0
	firstExpired := -1
	for i := len(events) - 1; i >= 0; i-- {
		var ev Event
		if err := json.Unmarshal(events[i], &ev); err != nil {
			continue
		}

		if ev.Key != key {
			continue
		}

		if ev.Time.After(windowEdge) {
			recentCount++
			// Early exit if we've passed the limit
			if recentCount > limit {
				return false, nil
			}
		} else if firstExpired == -1 {
			firstExpired = i
		}
	}

	// trimming of expired events
	if firstExpired != -1 {
		if err := op.ListTrim(ctx, bucketKey, 0, int64(firstExpired)); err != nil {
			return false, err
		}
	}

	return true, nil
}
