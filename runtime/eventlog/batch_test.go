package eventlog_test

import (
	"context"
	"sync"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type countingBus struct {
	libbus.Messenger
	mu       sync.Mutex
	subjects []string
}

func (b *countingBus) Publish(_ context.Context, subject string, _ []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subjects = append(b.subjects, subject)
	return nil
}

type recordingTrigger struct {
	mu    sync.Mutex
	calls [][]*runtimetypes.Event
}

func (r *recordingTrigger) HandleEvent(_ context.Context, events ...*runtimetypes.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, events)
}

func TestUnit_EventService_AppendBatchWritesOnceAndWakesOnce(t *testing.T) {
	ctx := context.Background()
	db := setupDB(t)
	bus := &countingBus{}
	recorder := &recordingTrigger{}
	holder := eventlog.NewTriggerHolder()
	holder.Set(recorder)
	svc := eventlog.NewService(db, bus, nil, eventlog.WithTrigger(holder))

	batch := []*runtimetypes.Event{
		{WorkspaceID: testWS, Type: "fs.created", Subject: "a"},
		{WorkspaceID: testWS, Type: "fs.modified", Subject: "b"},
		{WorkspaceID: testWS, Type: "fs.created", Subject: "c"},
	}
	require.NoError(t, svc.AppendBatch(ctx, batch))
	require.True(t, holder.Drain(5*time.Second))

	require.Equal(t, batch[0].NID+1, batch[1].NID)
	require.Equal(t, batch[1].NID+1, batch[2].NID)
	stored, err := svc.ListEventsSince(ctx, testWS, 0, 10)
	require.NoError(t, err)
	require.Len(t, stored, 3)

	require.Equal(t, []string{"fs.created", "fs.modified"}, bus.subjects, "one publish per distinct type")
	require.Len(t, recorder.calls, 1, "the trigger hears the whole batch at once")
	require.Len(t, recorder.calls[0], 3)
}

func TestUnit_EventService_AnEmptyBatchIsANoOpAndANilEventIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := eventlog.NewService(setupDB(t), nil, nil)
	require.NoError(t, svc.AppendBatch(ctx, nil))
	require.ErrorIs(t, svc.AppendBatch(ctx, []*runtimetypes.Event{nil}), runtimetypes.ErrInvalidEventParameter)
}
