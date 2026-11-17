package slot_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/slot"
	"github.com/contenox/runtime/internal/transport"
	"github.com/stretchr/testify/require"
)

type drainingBackend struct {
	transport.Service
	drained chan struct{}
	stop    chan struct{}
}

func (b drainingBackend) OpenSession(ctx context.Context, req transport.OpenSessionRequest) (transport.Session, error) {
	session, err := b.Service.OpenSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &drainingSession{Session: session, drained: b.drained, stop: b.stop}, nil
}

type drainingSession struct {
	transport.Session
	drained chan struct{}
	stop    chan struct{}
}

func (s *drainingSession) Decode(ctx context.Context, _ transport.DecodeConfig) (<-chan transport.StreamChunk, error) {
	chunks := make(chan transport.StreamChunk)
	go func() {
		defer close(chunks)
		<-ctx.Done()
		for range 32 {
			select {
			case chunks <- transport.StreamChunk{Error: ctx.Err()}:
			case <-s.stop:
				return
			}
		}
		close(s.drained)
		<-s.stop
	}()
	return chunks, nil
}

func TestUnit_Slot_CancelledDecodeKeepsSlotBusyUntilNativeDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stop, drained := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(stop) }) }
	t.Cleanup(finish)
	service := slot.New(drainingBackend{Service: transport.NewMemoryService(), drained: drained, stop: stop})
	session, err := service.OpenSession(ctx, transport.OpenSessionRequest{ModelName: "model", Path: "fixture.gguf", Config: transport.Config{NumCtx: 256}})
	require.NoError(t, err)
	decodeCtx, cancelDecode := context.WithCancel(ctx)
	chunks, err := session.Decode(decodeCtx, transport.DecodeConfig{})
	require.NoError(t, err)
	cancelDecode()
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("cancelled output was abandoned before native completion")
	}
	status, err := service.Status(ctx)
	require.NoError(t, err)
	require.Equal(t, transport.SlotBusy, status.State)
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("slot released before native completion: %v", err)
	default:
	}
	finish()
	for range chunks {
	}
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestUnit_Slot_DecodeCancellationReleasesBusyWhenOutputNotDrained(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stop, drained := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(stop) }) }
	defer finish()
	service := slot.New(drainingBackend{Service: transport.NewMemoryService(), drained: drained, stop: stop})
	_, err := service.LoadModel(ctx, transport.LoadModelRequest{ModelName: "model", Path: "fixture.gguf", Config: transport.Config{NumCtx: 256}})
	require.NoError(t, err)
	session, err := service.OpenSession(ctx, transport.OpenSessionRequest{ModelName: "model", Path: "fixture.gguf", Config: transport.Config{NumCtx: 256}})
	require.NoError(t, err)
	defer func() { finish(); require.NoError(t, session.Close()) }()
	decodeCtx, cancelDecode := context.WithCancel(ctx)
	_, err = session.Decode(decodeCtx, transport.DecodeConfig{})
	require.NoError(t, err)
	cancelDecode()
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	finish()
	require.Eventually(t, func() bool {
		status, err := service.Status(ctx)
		if err != nil {
			t.Error(err)
			return false
		}
		return status.State == transport.SlotReady
	}, 2*time.Second, 10*time.Millisecond)
}
