package workspaceevents_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	ctx     context.Context
	service workspaceevents.Service
	bus     *libbus.InMem
	rootA   string
	rootB   string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "events.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rootA := t.TempDir()
	rootB := t.TempDir()
	factory, err := vfs.NewFactory(rootA, rootB)
	require.NoError(t, err)
	bus := libbus.NewInMem()
	t.Cleanup(func() { _ = bus.Close() })
	service := workspaceevents.New("ws-test", eventlog.NewService(db, nil, libtracker.NoopTracker{}), bus, factory, libtracker.NoopTracker{})
	return fixture{ctx: ctx, service: service, bus: bus, rootA: rootA, rootB: rootB}
}

func editor(eventType, subject string) workspaceevents.Input {
	return workspaceevents.Input{Type: eventType, Source: workspaceevents.SourceEditor, Subject: subject}
}

func TestUnit_WorkspaceEvents_AppendThenReplayByCursor(t *testing.T) {
	f := newFixture(t)
	first, err := f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorOpened, "src/main.go"))
	require.NoError(t, err)
	second, err := f.service.Append(f.ctx, f.rootA, workspaceevents.Input{
		Type: workspaceevents.TypeEditorSaved, Source: workspaceevents.SourceEditor,
		Subject: filepath.Join(f.rootA, "src", "main.go"), Data: json.RawMessage(`{"etag":"abc"}`),
	})
	require.NoError(t, err)
	require.Greater(t, second.NID, first.NID)
	require.Equal(t, "src/main.go", second.Subject)
	require.JSONEq(t, `{"etag":"abc"}`, string(second.Data))

	all, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, workspaceevents.SourceEditor, all[0].Source)

	tail, err := f.service.Since(f.ctx, f.rootA, first.NID, 0)
	require.NoError(t, err)
	require.Len(t, tail, 1)
	require.Equal(t, second.NID, tail[0].NID)

	latest, err := f.service.Latest(f.ctx, f.rootA)
	require.NoError(t, err)
	require.Equal(t, second.NID, latest)
}

func TestUnit_WorkspaceEvents_RootsAreIsolatedStreams(t *testing.T) {
	f := newFixture(t)
	_, err := f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorOpened, "a.txt"))
	require.NoError(t, err)
	_, err = f.service.Append(f.ctx, f.rootB, editor(workspaceevents.TypeEditorOpened, "b.txt"))
	require.NoError(t, err)

	inA, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Len(t, inA, 1)
	require.Equal(t, "a.txt", inA[0].Subject)

	inB, err := f.service.Since(f.ctx, f.rootB, 0, 0)
	require.NoError(t, err)
	require.Len(t, inB, 1)
	require.Equal(t, "b.txt", inB[0].Subject)

	sub := filepath.Join(f.rootA, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	granted, err := f.service.GrantedRoot(sub)
	require.NoError(t, err)
	fromSub, err := f.service.Since(f.ctx, granted, 0, 0)
	require.NoError(t, err)
	require.Len(t, fromSub, 1)

	_, err = f.service.GrantedRoot(filepath.Dir(f.rootA))
	require.ErrorIs(t, err, workspaceevents.ErrRootNotGranted)
}

func TestUnit_WorkspaceEvents_RejectsWhatDoesNotBelong(t *testing.T) {
	f := newFixture(t)
	_, err := f.service.Append(f.ctx, f.rootA, editor("editor.exploded", "a.txt"))
	require.ErrorIs(t, err, workspaceevents.ErrUnknownType)

	_, err = f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeFSModified, "a.txt"))
	require.ErrorIs(t, err, workspaceevents.ErrSourceMismatch)

	_, err = f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorOpened, "../outside.txt"))
	require.ErrorIs(t, err, workspaceevents.ErrInvalidSubject)

	_, err = f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorOpened, ""))
	require.ErrorIs(t, err, workspaceevents.ErrInvalidSubject)

	withData := editor(workspaceevents.TypeEditorSaved, "a.txt")
	withData.Data = json.RawMessage(`["not","an","object"]`)
	_, err = f.service.Append(f.ctx, f.rootA, withData)
	require.ErrorIs(t, err, workspaceevents.ErrInvalidData)

	withData.Data = json.RawMessage(`{"blob":"` + strings.Repeat("x", workspaceevents.MaxDataBytes) + `"}`)
	_, err = f.service.Append(f.ctx, f.rootA, withData)
	require.ErrorIs(t, err, workspaceevents.ErrDataTooLarge)

	none, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestUnit_WorkspaceEvents_SelectionIsCoalesced(t *testing.T) {
	f := newFixture(t)
	first, err := f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorSelection, "a.txt"))
	require.NoError(t, err)
	again, err := f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorSelection, "a.txt"))
	require.NoError(t, err)
	require.Equal(t, first.NID, again.NID)
	other, err := f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorSelection, "b.txt"))
	require.NoError(t, err)
	require.NotEqual(t, first.NID, other.NID)

	all, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Len(t, all, 2)
}

func TestUnit_WorkspaceEvents_AppendAnnouncesOnTheBus(t *testing.T) {
	f := newFixture(t)
	ch := make(chan []byte, 4)
	sub, err := f.service.Subscribe(f.ctx, f.rootA, ch)
	require.NoError(t, err)
	defer sub.Unsubscribe()

	appended, err := f.service.Append(f.ctx, f.rootA, editor(workspaceevents.TypeEditorOpened, "a.txt"))
	require.NoError(t, err)
	_, err = f.service.Append(f.ctx, f.rootB, editor(workspaceevents.TypeEditorOpened, "b.txt"))
	require.NoError(t, err)

	select {
	case raw := <-ch:
		var got workspaceevents.Event
		require.NoError(t, json.Unmarshal(raw, &got))
		require.Equal(t, appended.NID, got.NID)
		require.Equal(t, "a.txt", got.Subject)
	case <-time.After(3 * time.Second):
		t.Fatal("no announcement arrived")
	}
	select {
	case raw := <-ch:
		t.Fatalf("another root's event leaked onto this stream: %s", raw)
	case <-time.After(150 * time.Millisecond):
	}
}

type recordingSink struct{ seen []taskengine.TaskEventKind }

func (r *recordingSink) Wants(taskengine.TaskEventKind) bool { return true }

func (r *recordingSink) PublishTaskEvent(_ context.Context, ev taskengine.TaskEvent) error {
	r.seen = append(r.seen, ev.Kind)
	return nil
}

func TestUnit_TaskEventBridge_RecordsWritesAndCommands(t *testing.T) {
	f := newFixture(t)
	inner := &recordingSink{}
	bridge := workspaceevents.NewTaskEventBridge(inner, f.service, func(context.Context) (string, string) {
		return "acp-session-1", f.rootA
	})

	wrote := taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "local_fs.write_file", ApprovalID: "call-1",
		ApprovalArgs: map[string]any{"path": "notes/todo.md"}}
	require.NoError(t, bridge.PublishTaskEvent(f.ctx, wrote))

	ran := taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "local_shell.local_shell", ApprovalID: "call-2",
		ApprovalArgs: map[string]any{"command": "go", "args": []any{"test", "./..."}},
		Content:      `{"exit_code":1,"stdout":"FAIL"}`}
	require.NoError(t, bridge.PublishTaskEvent(f.ctx, ran))

	read := taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "local_fs.read_file", ApprovalID: "call-3",
		ApprovalArgs: map[string]any{"path": "notes/todo.md"}}
	require.NoError(t, bridge.PublishTaskEvent(f.ctx, read))

	failed := taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "local_fs.write_file", ApprovalID: "call-4",
		ApprovalArgs: map[string]any{"path": "notes/never.md"}, Error: "denied"}
	require.NoError(t, bridge.PublishTaskEvent(f.ctx, failed))

	outside := taskengine.TaskEvent{Kind: taskengine.TaskEventToolCall, ToolName: "local_fs.write_file", ApprovalID: "call-5",
		ApprovalArgs: map[string]any{"path": filepath.Join(f.rootB, "elsewhere.md")}}
	require.NoError(t, bridge.PublishTaskEvent(f.ctx, outside))

	require.Len(t, inner.seen, 5)

	events, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Len(t, events, 2)

	require.Equal(t, workspaceevents.TypeToolWrote, events[0].Type)
	require.Equal(t, "notes/todo.md", events[0].Subject)
	var wroteData map[string]any
	require.NoError(t, json.Unmarshal(events[0].Data, &wroteData))
	require.Equal(t, "acp-session-1", wroteData["sessionId"])
	require.Equal(t, "call-1", wroteData["toolCallId"])

	require.Equal(t, workspaceevents.TypeToolRan, events[1].Type)
	require.Equal(t, "go", events[1].Subject, "only the program name: a command line can carry secrets")
	var ranData map[string]any
	require.NoError(t, json.Unmarshal(events[1].Data, &ranData))
	require.EqualValues(t, 1, ranData["exitCode"])
}

func TestUnit_ApprovalObserver_RecordsTheDecision(t *testing.T) {
	f := newFixture(t)
	observe := workspaceevents.ApprovalObserver(f.service, func(_ context.Context, internalID string) (string, string) {
		switch internalID {
		case "internal-7":
			return "acp-session-7", f.rootA
		case "":
			return "", f.rootB
		}
		return "", ""
	})
	observe(f.ctx, runtimetypes.HITLApproval{ID: "approval-1", ToolsName: "local_shell", ToolName: "local_shell",
		SessionID: "internal-7", ToolCallID: "call-9"}, false)
	observe(f.ctx, runtimetypes.HITLApproval{ID: "approval-2", ToolsName: "local_fs", ToolName: "write_file"}, true)
	observe(f.ctx, runtimetypes.HITLApproval{ID: "approval-3", ToolsName: "local_fs", ToolName: "write_file", SessionID: "gone"}, true)

	events, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, workspaceevents.TypeApprovalDecided, events[0].Type)
	require.Equal(t, "approval-1", events[0].Subject)
	var data map[string]any
	require.NoError(t, json.Unmarshal(events[0].Data, &data))
	require.Equal(t, "denied", data["decision"])
	require.Equal(t, "local_shell.local_shell", data["tool"])
	require.Equal(t, "acp-session-7", data["sessionId"])
	require.Equal(t, "call-9", data["toolCallId"])

	sessionless, err := f.service.Since(f.ctx, f.rootB, 0, 0)
	require.NoError(t, err)
	require.Len(t, sessionless, 1)
	require.Equal(t, "approval-2", sessionless[0].Subject)
	require.NoError(t, json.Unmarshal(sessionless[0].Data, &data))
	require.Equal(t, "approved", data["decision"])
}

func TestUnit_TaskEventBridge_NeverStoresACommandLine(t *testing.T) {
	f := newFixture(t)
	bridge := workspaceevents.NewTaskEventBridge(&recordingSink{}, f.service, func(context.Context) (string, string) {
		return "", f.rootA
	})
	for i, command := range []map[string]any{
		{"command": "curl", "args": []any{"-H", "Authorization: Bearer sk-live-123", "https://api.example.com"}},
		{"command": "/usr/bin/env TOKEN=sk-live-123 deploy"},
		{"command": `C:\Tools\gh.exe auth login --with-token sk-live-123`},
	} {
		require.NoError(t, bridge.PublishTaskEvent(f.ctx, taskengine.TaskEvent{
			Kind: taskengine.TaskEventToolCall, ToolName: "local_shell.local_shell", ApprovalID: fmt.Sprintf("call-%d", i),
			ApprovalArgs: command, Content: `{"exit_code":0}`,
		}))
	}
	events, err := f.service.Since(f.ctx, f.rootA, 0, 0)
	require.NoError(t, err)
	subjects := []string{}
	for _, e := range events {
		subjects = append(subjects, e.Subject)
		require.NotContains(t, string(e.Data), "sk-live")
	}
	require.Equal(t, []string{"curl", "env", "gh.exe"}, subjects)
}
