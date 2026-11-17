package supervisor_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestUnit_SharedOwner_ReplacementRequiresConfirmedWorkerStop(t *testing.T) {
	fixture := sharedOwner(t)
	client := connectOwner(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := filepath.Join(fixture.root, "models", "fixture", "model.gguf")
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	session, err := client.OpenSession(ctx, roleRequest(fixture, "primary"))
	require.NoError(t, err)
	manifest := transport.PushManifest{Name: "fixture", Type: "llama", Format: transport.PushFormatFile}
	_, err = client.PushModel(ctx, manifest, bytes.NewBufferString("replacement"))
	require.ErrorIs(t, err, transport.ErrModelBusy)
	require.NoError(t, session.Close())
	_, err = client.PushModel(ctx, manifest, bytes.NewBufferString("replacement"))
	require.ErrorIs(t, err, transport.ErrModelBusy)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, unchanged)
	status, err := client.Status(ctx)
	require.NoError(t, err)
	require.Len(t, status.Workers, 1)
	require.NoError(t, client.UnloadModel(ctx, transport.UnloadModelRequest{
		Fence: transport.Fence{OwnerInstanceID: fixture.holder.InstanceID()}, WorkerInstance: status.Workers[0].Instance,
	}))
	_, err = client.PushModel(ctx, manifest, bytes.NewBufferString("replacement"))
	require.NoError(t, err)
	installed, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "replacement", string(installed))
}
