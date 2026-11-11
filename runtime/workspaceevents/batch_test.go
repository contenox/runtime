package workspaceevents_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

func watcher(eventType, subject string) workspaceevents.Input {
	return workspaceevents.Input{Type: eventType, Source: workspaceevents.SourceWatcher, Subject: subject}
}

func nextAnnounce(t *testing.T, ch <-chan []byte) workspaceevents.Event {
	t.Helper()
	select {
	case raw := <-ch:
		var event workspaceevents.Event
		require.NoError(t, json.Unmarshal(raw, &event))
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("no announce arrived")
		return workspaceevents.Event{}
	}
}

func TestUnit_AppendBatch_WritesTheValidEventsAndAnnouncesOnce(t *testing.T) {
	f := newFixture(t)
	ch := make(chan []byte, 16)
	sub, err := f.service.Subscribe(f.ctx, f.rootA, ch)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	got, err := f.service.AppendBatch(f.ctx, f.rootA, []workspaceevents.Input{
		watcher(workspaceevents.TypeFSCreated, "a.go"),
		watcher("fs.exploded", "b.go"),
		watcher(workspaceevents.TypeFSModified, "c.go"),
		watcher(workspaceevents.TypeFSDeleted, "d.go"),
	})
	require.ErrorIs(t, err, workspaceevents.ErrUnknownType)
	require.Len(t, got, 3)
	require.Equal(t, []string{"a.go", "c.go", "d.go"}, []string{got[0].Subject, got[1].Subject, got[2].Subject})
	require.Equal(t, got[0].NID+1, got[1].NID)
	require.Equal(t, got[1].NID+1, got[2].NID)

	stored, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Len(t, stored, 3)

	sentinel, err := f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorOpened, "e.go"))
	require.NoError(t, err)
	require.Equal(t, got[2].NID, nextAnnounce(t, ch).NID, "the batch wakes subscribers with its last event")
	require.Equal(t, sentinel.NID, nextAnnounce(t, ch).NID, "and only once")
}

func TestUnit_AppendBatch_WithNothingValidWritesNothing(t *testing.T) {
	f := newFixture(t)
	got, err := f.service.AppendBatch(f.ctx, f.rootA, []workspaceevents.Input{watcher("fs.exploded", "b.go")})
	require.ErrorIs(t, err, workspaceevents.ErrUnknownType)
	require.Empty(t, got)
	stored, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Empty(t, stored)

	_, err = f.service.AppendBatch(f.ctx, t.TempDir(), []workspaceevents.Input{watcher(workspaceevents.TypeFSCreated, "a.go")})
	require.ErrorIs(t, err, workspaceevents.ErrRootNotGranted)
}
