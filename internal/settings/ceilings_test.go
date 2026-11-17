package settings_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/contenox/runtime/internal/settings"
)

func TestUnit_DeviceCeilingValidation(t *testing.T) {
	for _, share := range []float64{0, -0.1, 1.01, math.NaN(), math.Inf(1)} {
		require.Error(t, settings.ValidateDeviceCeiling("host", share), share)
	}
	for _, pool := range []string{"", " host", "gpu/0", "-gpu", "gpu 0", string(make([]byte, 129))} {
		require.Error(t, settings.ValidateDeviceCeiling(pool, 0.5), pool)
	}
	for _, pool := range []string{"host", "GPU-8f1c:0", "pci_0000.01.00.0"} {
		require.NoError(t, settings.ValidateDeviceCeiling(pool, 0.5), pool)
	}
	require.NoError(t, settings.ValidateDeviceCeiling("host", 1))
	ceilings, err := settings.ParseDeviceCeilings("")
	require.NoError(t, err)
	require.Empty(t, ceilings)
	ceilings, err = settings.ParseDeviceCeilings(`{"host":0.5,"gpu:0":0.6}`)
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"host": 0.5, "gpu:0": 0.6}, ceilings)
	_, err = settings.ParseDeviceCeilings(`{"host":0}`)
	require.Error(t, err)
	_, err = settings.ParseDeviceCeilings(`{"gpu 0":0.5}`)
	require.Error(t, err)
	_, err = settings.ParseDeviceCeilings(`[0.5]`)
	require.Error(t, err)
	spec, ok := settings.Lookup(settings.DeviceCeilings)
	require.True(t, ok)
	require.Equal(t, settings.DeviceCeilings, spec.Key)
}
