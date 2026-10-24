package libkeypool

import (
	"fmt"
	"time"

	"github.com/contenox/runtime/libroutine"
)

type ProviderCircuitBreaker struct {
	pool *libroutine.Pool
}

func NewProviderCircuitBreaker(threshold int, cooldown time.Duration) (*ProviderCircuitBreaker, error) {
	if threshold <= 0 {
		return nil, fmt.Errorf("keypool: circuit breaker threshold %d must be positive", threshold)
	}
	if cooldown <= 0 {
		return nil, fmt.Errorf("keypool: circuit breaker cooldown %s must be positive", cooldown)
	}
	return &ProviderCircuitBreaker{pool: libroutine.NewPool(threshold, cooldown)}, nil
}

func (cb *ProviderCircuitBreaker) AllowAt(identity string, now time.Time) bool {
	if cb == nil {
		return true
	}
	return cb.pool.AllowAt(identity, now)
}

func (cb *ProviderCircuitBreaker) MarkSuccess(identity string) {
	if cb == nil {
		return
	}
	cb.pool.MarkSuccess(identity)
}

func (cb *ProviderCircuitBreaker) MarkFailureAt(identity string, now time.Time) bool {
	if cb == nil || identity == "" {
		return false
	}
	r := cb.pool.Get(identity)
	r.MarkFailureAt(now)
	return r.GetState() == libroutine.Open
}

func (cb *ProviderCircuitBreaker) OpenAt(identity string, now time.Time) {
	if cb == nil || identity == "" {
		return
	}
	cb.pool.Get(identity).ForceOpenAt(now)
}

func (cb *ProviderCircuitBreaker) State(identity string, now time.Time) string {
	if cb == nil || identity == "" {
		return libroutine.Closed.String()
	}
	r := cb.pool.Get(identity)
	r.AllowAt(now)
	return r.GetState().String()
}
