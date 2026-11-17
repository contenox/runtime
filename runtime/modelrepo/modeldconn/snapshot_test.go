package modeldconn

import (
	"testing"

	"github.com/contenox/runtime/internal/modeld/snapshotauth"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestUnit_SnapshotProofRequiresPersistedSessionAndLocalLease(t *testing.T) {
	root := t.TempDir()
	require.NotEmpty(t, snapshotauth.Open(root))
	const namespace, instance, endpoint = "persisted-session", "owner", "127.0.0.1:4321"
	ctx := WithPersistedNamespace(t.Context(), namespace)
	want, ok := metadata.FromOutgoingContext(transportgrpc.WithSnapshotProof(ctx, snapshotauth.Proof(root, namespace, instance)))
	require.True(t, ok)
	got, ok := metadata.FromOutgoingContext(snapshotContext(ctx, root, endpoint, instance, namespace))
	require.True(t, ok)
	require.Equal(t, want, got)

	require.Same(t, ctx, snapshotContext(ctx, "", endpoint, instance, namespace), "explicit loopback endpoint")
	require.Same(t, ctx, snapshotContext(ctx, root, "203.0.113.7:4321", instance, namespace), "nonlocal endpoint")
	require.Same(t, ctx, snapshotContext(ctx, root, endpoint, instance, "different-session"))
	anon := t.Context()
	require.Same(t, anon, snapshotContext(anon, root, endpoint, instance, namespace), "no persisted session")
}
