package libkeypool_test

import (
	"testing"
	"time"

	"github.com/contenox/runtime/libkeypool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnit_CircuitBreakerCountsToThresholdThenAdmitsATrial(t *testing.T) {
	cb, err := libkeypool.NewProviderCircuitBreaker(2, 5*time.Minute)
	require.NoError(t, err)
	now := time.Now()

	assert.True(t, cb.AllowAt("prov-1", now))

	assert.False(t, cb.MarkFailureAt("prov-1", now))
	assert.True(t, cb.AllowAt("prov-1", now))
	assert.True(t, cb.MarkFailureAt("prov-1", now))
	assert.False(t, cb.AllowAt("prov-1", now.Add(time.Minute)))
	assert.Equal(t, "Open", cb.State("prov-1", now.Add(time.Minute)))
	assert.True(t, cb.AllowAt("prov-1", now.Add(6*time.Minute)), "the cooldown admits one trial")

	cb.MarkSuccess("prov-1")
	assert.True(t, cb.AllowAt("prov-1", now))
	assert.Equal(t, "Closed", cb.State("prov-1", now))
}

func TestUnit_CircuitBreakerOpensImmediatelyOnATerminalFailure(t *testing.T) {
	cb, err := libkeypool.NewProviderCircuitBreaker(5, time.Minute)
	require.NoError(t, err)
	now := time.Now()

	cb.OpenAt("prov-1", now)
	assert.False(t, cb.AllowAt("prov-1", now))
	assert.True(t, cb.AllowAt("prov-1", now.Add(2*time.Minute)))
}

func TestUnit_CircuitBreakerRefusesANonPositivePolicy(t *testing.T) {
	_, err := libkeypool.NewProviderCircuitBreaker(0, time.Minute)
	require.Error(t, err)

	_, err = libkeypool.NewProviderCircuitBreaker(3, 0)
	require.Error(t, err)

	_, err = libkeypool.NewProviderCircuitBreaker(-1, -time.Second)
	require.Error(t, err)
}

func TestUnit_CircuitBreakerWithoutAConfigurationAdmitsEverything(t *testing.T) {
	var cb *libkeypool.ProviderCircuitBreaker
	now := time.Now()

	assert.True(t, cb.AllowAt("prov-1", now))
	assert.False(t, cb.MarkFailureAt("prov-1", now))
	cb.OpenAt("prov-1", now)
	assert.True(t, cb.AllowAt("prov-1", now), "a nil breaker holds no quarantine")
	assert.Equal(t, "Closed", cb.State("prov-1", now))
}
