package supervisor_test

import (
	"context"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/stretchr/testify/require"
)

func TestUnit_SharedOwner_RemoteRuntimeCloseReleasesOnlyItsClient(t *testing.T) {
	fixture := sharedOwner(t)
	clients := make([]*modeldconn.Workers, 2)
	for i := range clients {
		var err error
		clients[i], err = modeldconn.NewConfiguredWorkers(t.Context(), t.TempDir(), func(context.Context, string) (modeldconn.WorkerOptions, error) {
			return modeldconn.WorkerOptions{}, nil
		})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, clients[i].Close()) })
	}
	target := modeldconn.ModeldTarget{BackendID: "same-remote-connection", Endpoint: fixture.address}
	ref := modeldconn.ModelRef{Name: "fixture", Type: "llama"}
	first, err := modeldconn.OpenSessionTarget(modeldconn.WithWorkers(t.Context(), clients[0]), target, ref, transport.Config{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	type result struct {
		session transport.Session
		err     error
	}
	opened := make(chan result, 1)
	go func() {
		session, err := modeldconn.OpenSessionTarget(modeldconn.WithWorkers(t.Context(), clients[1]), target, ref, transport.Config{})
		opened <- result{session: session, err: err}
	}()
	observer := connectOwner(t, fixture)
	require.Eventually(t, func() bool {
		snapshot, err := observer.Admission(t.Context())
		return err == nil && len(snapshot.Workers) == 1 && snapshot.Workers[0].QueuedForeground == 1
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, clients[0].Close())
	select {
	case result := <-opened:
		require.NoError(t, result.err)
		require.False(t, result.session.ExplainContext().Closed)
		require.NoError(t, result.session.Close())
	case <-time.After(5 * time.Second):
		t.Fatal("another runtime's queued work did not survive client closure")
	}
	status, err := observer.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, status.Workers, 1)
	require.NotEqual(t, "stopped", status.Workers[0].State)
	_, err = modeldconn.OpenSessionTarget(modeldconn.WithWorkers(t.Context(), clients[0]), target, ref, transport.Config{})
	require.ErrorContains(t, err, "closed")
}
