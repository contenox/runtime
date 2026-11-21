package workflows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	db "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

func TestProposalLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	manager, err := db.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "workflow.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer manager.Close()
	s := &Service{DB: manager, WorkspaceID: "test", AuthorizeRead: func(context.Context, string) error { return nil }}
	file := filepath.Join(dir, "draft.txt")
	require.NoError(t, os.WriteFile(file, []byte("old"), 0600))
	d, err := s.Save(dir, Definition{Name: "edit", Source: `function run(input){ workflow.propose("draft.txt",workflow.read("draft.txt")+input.suffix);return "Review text" }`})
	require.NoError(t, err)
	start := func(id string) Run {
		r, e := s.Run(ctx, dir, Invocation{Name: d.Name, Revision: d.Revision, ID: id, Origin: "manual", Payload: []byte(`{"suffix":"!"}`)})
		require.NoError(t, e)
		return r
	}
	first := start("first")
	require.Equal(t, "pending", first.Status)
	bytes, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "old", string(bytes))
	_, err = s.Decide(ctx, dir, first.ID, "reject")
	require.NoError(t, err)
	require.Equal(t, "rejected", start("first").Status)
	stale := start("stale")
	require.NoError(t, os.WriteFile(file, []byte("new"), 0600))
	_, err = s.Decide(ctx, dir, stale.ID, "approve")
	require.ErrorIs(t, err, ErrConflict)
	second := start("second")
	reopened := &Service{DB: manager, WorkspaceID: "test"}
	for range 2 {
		r, e := reopened.Decide(ctx, dir, second.ID, "approve")
		require.NoError(t, e)
		require.Equal(t, "approved", r.Status)
	}
	bytes, err = os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "new!", string(bytes))
	_, err = s.Save(dir, Definition{Name: "edit", Source: "function run(){}"})
	require.ErrorIs(t, err, ErrConflict)
	_, err = s.Run(ctx, dir, Invocation{Name: d.Name, Revision: "wrong", ID: "third", Origin: "manual", Payload: []byte("{}")})
	require.ErrorIs(t, err, ErrConflict)
	events := runtimetypes.NewEventStore(manager)
	publish := func() int64 {
		row, err := workspaceevents.TopicRecord(s.scope(dir), "document.updated", "human", []byte("{}"), time.Now().UTC())
		require.NoError(t, err)
		require.NoError(t, events.AppendEvent(ctx, &row))
		return row.NID
	}
	d.Source = `function run(){workflow.requireLatestEvent();workflow.propose("draft.txt","later");return "Review"}`
	d, err = s.Save(dir, d)
	require.NoError(t, err)
	eventRun, err := s.Run(ctx, dir, Invocation{Name: d.Name, Revision: d.Revision, Origin: "document.updated", EventNID: publish(), Payload: []byte("{}")})
	require.NoError(t, err)
	require.Equal(t, "pending", eventRun.Status)
	publish()
	_, err = s.Decide(ctx, dir, eventRun.ID, "approve")
	require.ErrorIs(t, err, ErrConflict)
	_, err = reopened.Get(ctx, t.TempDir(), second.ID)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWorkflowAuthorizesReadsAndProposalSnapshots(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "private.txt"), []byte("private contents"), 0600))
	for _, source := range []string{
		"return workflow.read('private.txt')",
		"workflow.propose('private.txt', 'replacement'); return 'review'",
	} {
		var checked []string
		s := Service{AuthorizeRead: func(ctx context.Context, path string) error {
			checked = append(checked, path)
			return errors.New("policy denied")
		}}
		var run Run
		err := s.execute(t.Context(), dir, Definition{Name: "edit", Source: "function run(){" + source + "}"}, []byte("{}"), &run)
		require.ErrorContains(t, err, "policy denied")
		require.Equal(t, []string{"private.txt"}, checked)
		require.Empty(t, run.Summary)
		require.Empty(t, run.Changes)
	}
}

func TestProposalFailureLeavesEarlierDocumentIntactAndRunRejectable(t *testing.T) {
	ctx, dir := t.Context(), t.TempDir()
	manager, err := db.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "workflow.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer manager.Close()
	s := Service{DB: manager, WorkspaceID: "test", AuthorizeRead: func(context.Context, string) error { return nil }}
	d, err := s.Save(dir, Definition{Name: "edit", Source: "function run(){ workflow.propose('first.txt','new'); workflow.propose('missing/later.txt','new'); return 'review' }"})
	require.NoError(t, err)
	first := filepath.Join(dir, "first.txt")
	require.NoError(t, os.WriteFile(first, []byte("old"), 0600))
	r, err := s.Run(ctx, dir, Invocation{Name: d.Name, Revision: d.Revision, Payload: []byte("{}")})
	require.NoError(t, err)
	require.Equal(t, "pending", r.Status)
	_, err = s.Decide(ctx, dir, r.ID, "approve")
	require.Error(t, err)
	data, err := os.ReadFile(first)
	require.NoError(t, err)
	require.Equal(t, "old", string(data))
	r, err = s.Decide(ctx, dir, r.ID, "reject")
	require.NoError(t, err)
	require.Equal(t, "rejected", r.Status)
}

func TestProposalRollsBackIfDestinationChangesAfterStaging(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()
	original := []byte{0, 255, 128}
	require.NoError(t, root.WriteFile("old.pdf", original, 0600))
	batch, err := stageProposal(root, []Change{
		{Path: "new.txt", After: "new"},
		{Path: "old.pdf", BeforeHash: digest(original), Binary: []byte("pdf")},
		{Path: "last.txt", After: "last"},
	}, false)
	require.NoError(t, err)
	defer batch.close()
	require.NoError(t, root.WriteFile("last.txt", []byte("independent edit"), 0600))
	require.ErrorIs(t, batch.apply(), ErrConflict)
	require.NoError(t, batch.undo())
	_, err = root.Stat("new.txt")
	require.ErrorIs(t, err, os.ErrNotExist)
	data, err := root.ReadFile("old.pdf")
	require.NoError(t, err)
	require.Equal(t, original, data)
	data, err = root.ReadFile("last.txt")
	require.NoError(t, err)
	require.Equal(t, "independent edit", string(data))
}
