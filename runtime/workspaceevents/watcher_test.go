package workspaceevents_test

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

type watchFixture struct {
	ctx     context.Context
	db      libdb.DBManager
	factory *vfs.Factory
	service workspaceevents.Service
	root    string
	leases  string
}

func newWatchFixture(t *testing.T) watchFixture {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "watch.db"), runtimetypes.SchemaSQLite+"\n"+libkvstore.SQLiteSchema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	root, err := vfs.ResolveRoot(t.TempDir())
	require.NoError(t, err)
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)
	bus := libbus.NewInMem()
	t.Cleanup(func() { _ = bus.Close() })
	service := workspaceevents.New("ws-watch", eventlog.NewService(db, nil, libtracker.NoopTracker{}), bus, factory, libtracker.NoopTracker{})
	return watchFixture{ctx: ctx, db: db, factory: factory, service: service, root: root, leases: t.TempDir()}
}

func skipNames(names ...string) workspaceevents.SkipFunc {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(_, rel string, isDir bool) bool {
		return set[path.Base(rel)] && (isDir || path.Ext(rel) == ".log")
	}
}

func (f watchFixture) watcher(t *testing.T, owner string) *workspaceevents.Watcher {
	t.Helper()
	w := workspaceevents.NewWatcher(f.service, f.factory, workspaceevents.WatcherConfig{
		WorkspaceID: "ws-watch",
		Owner:       owner,
		LeaseDir:    f.leases,
		Skip:        skipNames("node_modules", ".git", "noise.log"),
		Transient:   func(rel string) bool { return strings.HasPrefix(path.Base(rel), ".tmp-") },
		Reconcile:   50 * time.Millisecond,
		LeaseTTL:    400 * time.Millisecond,
		Debounce:    60 * time.Millisecond,
		MaxDelay:    300 * time.Millisecond,
	})
	w.Start(f.ctx)
	t.Cleanup(w.Stop)
	return w
}

func (f watchFixture) waitWatching(t *testing.T, w *workspaceevents.Watcher, root string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, r := range w.Watching() {
			if r == root {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "the watcher never took %s", root)
}

func (f watchFixture) fsEvents(t *testing.T, root string) []workspaceevents.Event {
	t.Helper()
	all, err := f.service.Since(f.ctx, root, 0, 0)
	require.NoError(t, err)
	out := []workspaceevents.Event{}
	for _, e := range all {
		if e.Source == workspaceevents.SourceWatcher {
			out = append(out, e)
		}
	}
	return out
}

func (f watchFixture) waitFor(t *testing.T, root string, n int) []workspaceevents.Event {
	t.Helper()
	var got []workspaceevents.Event
	require.Eventually(t, func() bool {
		got = f.fsEvents(t, root)
		return len(got) >= n
	}, 5*time.Second, 20*time.Millisecond, "expected %d file events", n)
	time.Sleep(400 * time.Millisecond)
	return f.fsEvents(t, root)
}

func settle() { time.Sleep(400 * time.Millisecond) }

func kinds(events []workspaceevents.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type+" "+e.Subject)
	}
	return out
}

func write(t *testing.T, name, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(name), 0o755))
	require.NoError(t, os.WriteFile(name, []byte(body), 0o644))
}

func TestIntegration_Watcher_ReportsCreateModifyDelete(t *testing.T) {
	f := newWatchFixture(t)
	w := f.watcher(t, "one")
	f.waitWatching(t, w, f.root)

	file := filepath.Join(f.root, "src", "main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	settle()
	write(t, file, "package main\n")
	events := f.waitFor(t, f.root, 2)
	require.Equal(t, []string{"fs.created src", "fs.created src/main.go"}, kinds(events))
	var data map[string]any
	require.NoError(t, json.Unmarshal(events[1].Data, &data))
	require.EqualValues(t, len("package main\n"), data["size"])
	require.Equal(t, false, data["isDir"])

	write(t, file, "package main\n\nfunc main() {}\n")
	events = f.waitFor(t, f.root, 3)
	require.Equal(t, "fs.modified src/main.go", kinds(events)[2])

	require.NoError(t, os.Remove(file))
	events = f.waitFor(t, f.root, 4)
	require.Equal(t, "fs.deleted src/main.go", kinds(events)[3])
	require.Len(t, events, 4)
}

func TestIntegration_Watcher_MovesAndAtomicSaves(t *testing.T) {
	f := newWatchFixture(t)
	w := f.watcher(t, "one")
	f.waitWatching(t, w, f.root)

	original := filepath.Join(f.root, "notes.md")
	write(t, original, "first\n")
	f.waitFor(t, f.root, 1)

	moved := filepath.Join(f.root, "renamed.md")
	require.NoError(t, os.Rename(original, moved))
	events := f.waitFor(t, f.root, 2)
	require.Equal(t, "fs.moved renamed.md", kinds(events)[1])
	var data map[string]any
	require.NoError(t, json.Unmarshal(events[1].Data, &data))
	require.Equal(t, "notes.md", data["from"])

	temp := filepath.Join(f.root, ".renamed.md.tmp")
	write(t, temp, "second\n")
	require.NoError(t, os.Rename(temp, moved))
	events = f.waitFor(t, f.root, 3)
	require.Equal(t, []string{"fs.created notes.md", "fs.moved renamed.md", "fs.modified renamed.md"}, kinds(events))

	scratch := filepath.Join(f.root, "gone-soon.txt")
	write(t, scratch, "x")
	require.NoError(t, os.Remove(scratch))
	settle()
	require.Len(t, f.fsEvents(t, f.root), 3, "a file created and removed inside one window leaves no trace")
}

func TestIntegration_Watcher_SkipsIgnoredTreesAndFollowsNewFolders(t *testing.T) {
	f := newWatchFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, "node_modules", "pkg"), 0o755))
	w := f.watcher(t, "one")
	f.waitWatching(t, w, f.root)

	write(t, filepath.Join(f.root, "node_modules", "pkg", "index.js"), "x")
	write(t, filepath.Join(f.root, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(f.root, "noise.log"), "chatter")
	settle()
	require.Empty(t, f.fsEvents(t, f.root))

	fresh := filepath.Join(f.root, "pkg", "deep")
	require.NoError(t, os.MkdirAll(fresh, 0o755))
	f.waitFor(t, f.root, 1)
	settle()
	write(t, filepath.Join(fresh, "inside.go"), "package deep\n")
	events := f.waitFor(t, f.root, 3)
	require.Equal(t, []string{"fs.created pkg", "fs.created pkg/deep", "fs.created pkg/deep/inside.go"}, kinds(events))
}

func TestIntegration_Watcher_AFolderMovedInReportsWhatItHolds(t *testing.T) {
	f := newWatchFixture(t)
	w := f.watcher(t, "one")
	f.waitWatching(t, w, f.root)

	outside := filepath.Join(filepath.Dir(f.root), filepath.Base(f.root)+"-staging")
	write(t, filepath.Join(outside, "lib", "a.go"), "package lib\n")
	write(t, filepath.Join(outside, "lib", "node_modules", "x.js"), "x")
	write(t, filepath.Join(outside, "README.md"), "hi\n")
	t.Cleanup(func() { _ = os.RemoveAll(outside) })

	require.NoError(t, os.Rename(outside, filepath.Join(f.root, "vendored")))
	events := f.waitFor(t, f.root, 4)
	got := kinds(events)
	require.Len(t, got, 4, "%v", got)
	require.Equal(t, "fs.created vendored", got[0])
	require.ElementsMatch(t, []string{"fs.created vendored/lib", "fs.created vendored/lib/a.go", "fs.created vendored/README.md"}, got[1:])

	write(t, filepath.Join(f.root, "vendored", "lib", "b.go"), "package lib\n")
	events = f.waitFor(t, f.root, 5)
	require.Equal(t, "fs.created vendored/lib/b.go", kinds(events)[4], "the moved-in folder is watched")
}

func TestIntegration_Watcher_TransientFilesNeverShow(t *testing.T) {
	f := newWatchFixture(t)
	w := f.watcher(t, "one")
	f.waitWatching(t, w, f.root)

	target := filepath.Join(f.root, "config.json")
	temp := filepath.Join(f.root, ".tmp-123")
	write(t, temp, "{}")
	settle()
	require.Empty(t, f.fsEvents(t, f.root), "a temp file alone is not news")

	require.NoError(t, os.Rename(temp, target))
	events := f.waitFor(t, f.root, 1)
	require.Equal(t, []string{"fs.modified config.json"}, kinds(events), "a temp renamed over a path is a save of that path")

	write(t, temp, "{\"a\":1}")
	settle()
	require.NoError(t, os.Remove(temp))
	settle()
	require.Len(t, f.fsEvents(t, f.root), 1, "a discarded temp leaves no trace")
}

func TestIntegration_Watcher_PicksUpARootGrantedLater(t *testing.T) {
	f := newWatchFixture(t)
	w := f.watcher(t, "one")
	f.waitWatching(t, w, f.root)

	later, err := vfs.ResolveRoot(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, f.factory.SetRoots([]string{f.root, later}))
	f.waitWatching(t, w, later)

	write(t, filepath.Join(later, "hello.txt"), "hi")
	events := f.waitFor(t, later, 1)
	require.Equal(t, []string{"fs.created hello.txt"}, kinds(events))
	require.Empty(t, f.fsEvents(t, f.root))

	require.NoError(t, f.factory.SetRoots([]string{f.root}))
	require.Eventually(t, func() bool { return len(w.Watching()) == 1 }, 5*time.Second, 20*time.Millisecond)
}

func TestIntegration_Watcher_OneWriterPerRootAndTakeover(t *testing.T) {
	f := newWatchFixture(t)
	first := f.watcher(t, "first")
	f.waitWatching(t, first, f.root)
	second := f.watcher(t, "second")
	time.Sleep(300 * time.Millisecond)
	require.Empty(t, second.Watching(), "the second runtime must not watch a root the first holds")

	write(t, filepath.Join(f.root, "once.txt"), "1")
	events := f.waitFor(t, f.root, 1)
	require.Equal(t, []string{"fs.created once.txt"}, kinds(events), "one change, one event")

	first.Stop()
	f.waitWatching(t, second, f.root)
	write(t, filepath.Join(f.root, "after.txt"), "2")
	events = f.waitFor(t, f.root, 2)
	require.Equal(t, []string{"fs.created once.txt", "fs.created after.txt"}, kinds(events))
}
