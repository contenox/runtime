package modelstore_test

import (
	"bytes"
	"context"
	"io"
	"sync/atomic"
	"testing"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

func TestUnit_ModelStore_AcquisitionAndMutationExcludeEachOtherPerArtifact(t *testing.T) {
	ctx := context.Background()
	var resident atomic.Bool
	admin, err := modelstore.NewProtectedAdmin(t.TempDir(), func(name string) error {
		if name == "first" && resident.Load() {
			return transport.ErrModelBusy
		}
		return nil
	})
	require.NoError(t, err)
	manifest := modelstore.PushManifest{Name: "first", Type: "llama", Format: modelstore.PushFormatFile}
	first, err := admin.Retain("first")
	require.NoError(t, err)
	second, err := admin.Retain("first")
	require.NoError(t, err)
	_, err = admin.ReceiveModel(ctx, manifest, bytes.NewBufferString("original"))
	require.ErrorIs(t, err, transport.ErrModelBusy)
	first()
	require.ErrorIs(t, admin.RemoveModel(ctx, "first"), transport.ErrModelBusy)
	resident.Store(true)
	second()
	_, err = admin.ReceiveModel(ctx, manifest, bytes.NewBufferString("replacement"))
	require.ErrorIs(t, err, transport.ErrModelBusy)
	resident.Store(false)
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := admin.ReceiveModel(ctx, manifest, &announcingReader{reader: reader, started: started})
		done <- err
	}()
	<-started
	_, err = admin.Retain("first")
	require.ErrorIs(t, err, transport.ErrModelBusy)
	require.Error(t, admin.RemoveModel(ctx, "first"))
	unrelated, err := admin.Retain("second")
	require.NoError(t, err)
	unrelated()
	_, err = writer.Write([]byte("replacement"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, <-done)
	require.NoError(t, admin.RemoveModel(ctx, "first"))
}

type announcingReader struct {
	reader  io.Reader
	started chan struct{}
	once    atomic.Bool
}

func (r *announcingReader) Read(buf []byte) (int, error) {
	if r.once.CompareAndSwap(false, true) {
		close(r.started)
	}
	return r.reader.Read(buf)
}
