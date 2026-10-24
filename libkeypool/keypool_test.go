package libkeypool_test

import (
	"testing"
	"time"

	"github.com/contenox/runtime/libkeypool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_KeyPoolStickyClientIsStableUnderAClockWeControl(t *testing.T) {
	breaker, err := libkeypool.NewProviderCircuitBreaker(3, 2*time.Minute)
	require.NoError(t, err)
	pool, err := libkeypool.NewKeyPool([]string{"sk-key-1", "sk-key-2", "sk-key-3"}, libkeypool.PolicyStickyClient, breaker)
	require.NoError(t, err)

	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	first := pool.SelectKey("user-alice", start)
	require.Contains(t, pool.Keys(), first)

	for _, later := range []time.Time{start.Add(time.Second), start.Add(time.Hour), start.Add(72 * time.Hour)} {
		assert.Equal(t, first, pool.SelectKey("user-alice", later),
			"the same identity keeps its key at every instant it is asked")
	}
	assert.Equal(t, first, pool.SelectKey("user-alice", start), "and asking out of order changes nothing")

	bob := pool.SelectKey("user-bob", start)
	assert.Contains(t, pool.Keys(), bob)
}

func TestUnit_KeyPoolFailoverOnQuarantine(t *testing.T) {
	breaker, err := libkeypool.NewProviderCircuitBreaker(3, 2*time.Minute)
	require.NoError(t, err)
	keys := []string{"sk-key-1", "sk-key-2"}
	pool, err := libkeypool.NewKeyPool(keys, libkeypool.PolicyStickyClient, breaker)
	require.NoError(t, err)

	now := time.Now()
	chosen := pool.SelectKey("user-alice", now)

	breaker.OpenAt(chosen, now)
	assert.False(t, breaker.AllowAt(chosen, now))

	spillover := pool.SelectKey("user-alice", now)
	assert.NotEqual(t, chosen, spillover, "quarantined key must be bypassed")
	assert.Contains(t, keys, spillover)

	assert.Equal(t, chosen, pool.SelectKey("user-alice", now.Add(3*time.Minute)),
		"once the cooldown passes the pinned key comes back, which is what keeps the cache warm")
	assert.Equal(t, spillover, pool.SelectKey("user-alice", now),
		"and asking again at the earlier instant bypasses it once more: the quarantine is measured against the time handed in, not a wall clock")
}

func TestUnit_KeyPoolRoundRobin(t *testing.T) {
	pool, err := libkeypool.NewKeyPool([]string{"sk-key-1", "sk-key-2"}, libkeypool.PolicyRoundRobin, nil)
	require.NoError(t, err)

	now := time.Now()
	first := pool.SelectKey("any", now)
	second := pool.SelectKey("any", now)
	assert.NotEqual(t, first, second, "round robin should alternate keys")
	assert.Equal(t, first, pool.SelectKey("any", now), "and come back around")
}

func TestUnit_KeyPoolRoundRobinWithoutAnIdentity(t *testing.T) {
	pool, err := libkeypool.NewKeyPool([]string{"sk-key-1", "sk-key-2"}, libkeypool.PolicyRoundRobin, nil)
	require.NoError(t, err)

	now := time.Now()
	assert.NotEqual(t, pool.SelectKey("", now), pool.SelectKey("", now))
}

func TestUnit_KeyPoolRefusesAConfigurationItCannotRotate(t *testing.T) {
	breaker, err := libkeypool.NewProviderCircuitBreaker(3, 2*time.Minute)
	require.NoError(t, err)

	_, err = libkeypool.NewKeyPool([]string{"sk-key-1"}, "cheapest", breaker)
	require.Error(t, err, "an unknown rotation policy selects nothing an operator could predict")
	assert.Contains(t, err.Error(), "cheapest")

	_, err = libkeypool.NewKeyPool([]string{"  ", ""}, libkeypool.PolicyStickyClient, breaker)
	require.Error(t, err, "a pool of blank keys holds no key")
}

func TestUnit_KeyPoolWithoutABreakerStillSelects(t *testing.T) {
	pool, err := libkeypool.NewKeyPool([]string{"sk-key-1", "sk-key-2"}, libkeypool.PolicyStickyClient, nil)
	require.NoError(t, err)

	now := time.Now()
	assert.Equal(t, pool.SelectKey("user-alice", now), pool.SelectKey("user-alice", now))
	assert.Contains(t, []string{"sk-key-1", "sk-key-2"}, pool.SelectKey("user-bob", now))
}

func TestUnit_KeyPoolNamesAKeyWhenEveryKeyIsQuarantined(t *testing.T) {
	breaker, err := libkeypool.NewProviderCircuitBreaker(3, time.Minute)
	require.NoError(t, err)
	keys := []string{"sk-key-1", "sk-key-2"}
	pool, err := libkeypool.NewKeyPool(keys, libkeypool.PolicyStickyClient, breaker)
	require.NoError(t, err)

	now := time.Now()
	for _, key := range keys {
		breaker.OpenAt(key, now)
	}
	assert.Contains(t, keys, pool.SelectKey("user-alice", now))
}

func TestUnit_KeyPoolReportsWhatItWasBuiltFrom(t *testing.T) {
	pool, err := libkeypool.NewKeyPool([]string{"sk-key-1", "sk-key-1", " sk-key-2 "}, libkeypool.PolicyStickyClient, nil)
	require.NoError(t, err)

	assert.Equal(t, []string{"sk-key-1", "sk-key-2"}, pool.Keys(), "cleaned, in the order declared")
	assert.Equal(t, libkeypool.PolicyStickyClient, pool.Policy())

	var absent *libkeypool.KeyPool
	assert.Empty(t, absent.SelectKey("user-alice", time.Now()))
	assert.Nil(t, absent.Keys())
}
