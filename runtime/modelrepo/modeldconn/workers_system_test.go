package modeldconn

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSystem_WorkersNativeMixedBackendLifecycle(t *testing.T) {
	binary := os.Getenv("CONTENOX_TEST_MODELD_BIN")
	if binary == "" {
		t.Skip("set CONTENOX_TEST_MODELD_BIN to a native dual-backend modeld launcher")
	}
	t.Setenv("CONTENOX_MODELD_BIN", binary)
	root := t.TempDir()
	a, b := NewWorkers(context.Background(), root), NewWorkers(context.Background(), root)
	t.Cleanup(func() { require.NoError(t, a.Close()); require.NoError(t, b.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var targets []ModeldTarget
	var releases []func() error
	for i, spec := range []WorkerSpec{
		{Role: "primary", Model: "primary", Backend: "llama", Memory: "50%", Headroom: "5%", ComputeReserve: "auto"},
		{Role: "autocomplete", Model: "autocomplete", Backend: "llama", Memory: "25%", MinHotContext: "0"},
		{Role: "embedding", Model: "embedding", Backend: "openvino", Memory: "10%"},
		{Role: "alternative", Model: "alternative", Backend: "openvino", Memory: "25%"},
	} {
		manager := a
		if i >= 2 {
			manager = b
		}
		target, release, err := manager.Acquire(ctx, spec)
		require.NoError(t, err)
		for _, prior := range targets {
			require.NotEqual(t, prior.Endpoint, target.Endpoint)
			require.NotEqual(t, prior.Instance, target.Instance)
		}
		client, err := Endpoint(ctx, target.BackendID, target.Endpoint)
		require.NoError(t, err)
		require.Equal(t, spec.Backend, client.Backend)
		targets = append(targets, target)
		releases = append(releases, release)
	}
	for _, release := range releases {
		require.NoError(t, release())
	}
	require.NoError(t, a.Close())
	for _, target := range targets[2:] {
		client, err := Endpoint(ctx, target.BackendID, target.Endpoint)
		require.NoError(t, err)
		_, err = client.Health(ctx)
		require.NoError(t, err)
	}
	require.Len(t, b.Snapshot(), 2)
	for _, target := range targets[:2] {
		stoppedCtx, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, err := Endpoint(stoppedCtx, target.BackendID, target.Endpoint)
		stop()
		require.Error(t, err)
	}
}
