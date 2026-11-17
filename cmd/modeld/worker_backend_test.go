package main

import (
	"github.com/contenox/runtime/internal/modeld/capacity"
	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/libtracker"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUnit_WorkerBackendExplicitSelection(t *testing.T) {
	original := backends
	t.Cleanup(func() { backends = original })
	factory := func(capacity.Policy, libtracker.ActivityTracker) transport.Service { return unavailableBackend{} }
	backends = map[string]backendFactory{"llama": factory, "openvino": factory}
	t.Setenv("CONTENOX_MODELD_BACKEND", "llama")
	_, name, err := selectWorkerBackend("openvino", capacity.Policy{}, libtracker.NoopTracker{})
	require.NoError(t, err)
	require.Equal(t, "openvino", name)
	_, _, err = selectWorkerBackend("missing", capacity.Policy{}, libtracker.NoopTracker{})
	require.ErrorContains(t, err, "not compiled")
	t.Setenv("CONTENOX_MODELD_BACKEND", "missing")
	_, _, err = selectWorkerBackend("", capacity.Policy{}, libtracker.NoopTracker{})
	require.ErrorContains(t, err, "not compiled")
}
