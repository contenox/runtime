package workspaceevents

import (
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeInfo struct {
	name string
	dir  bool
	size int64
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return time.Unix(1_800_000_000, 0) }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

type batch struct {
	seq    int
	paths  map[string]*pendingPath
	exists map[string]os.FileInfo
}

func newBatch() *batch {
	return &batch{paths: map[string]*pendingPath{}, exists: map[string]os.FileInfo{}}
}

func (b *batch) entry(path string) *pendingPath {
	p, ok := b.paths[path]
	if !ok {
		b.seq++
		p = &pendingPath{path: path, first: b.seq}
		b.paths[path] = p
	}
	b.seq++
	return p
}

func (b *batch) created(path string, dir bool) *batch {
	p := b.entry(path)
	p.created = true
	if p.createSeq == 0 {
		p.createSeq = b.seq
	}
	p.isDir = p.isDir || dir
	return b
}

func (b *batch) wrote(path string) *batch   { b.entry(path).wrote = true; return b }
func (b *batch) removed(path string) *batch { b.entry(path).removed = true; return b }
func (b *batch) renamed(path string) *batch {
	p := b.entry(path)
	p.renamed = true
	p.renameSeq = b.seq
	return b
}
func (b *batch) dirGone(path string) *batch { b.entry(path).isDir = true; return b }

func (b *batch) onDisk(path string, dir bool, size int64) *batch {
	b.exists[path] = fakeInfo{name: path[strings.LastIndex(path, "/")+1:], dir: dir, size: size}
	return b
}

func (b *batch) resolve() batchResult {
	paths := make([]*pendingPath, 0, len(b.paths))
	for _, p := range b.paths {
		paths = append(paths, p)
	}
	for i := 1; i < len(paths); i++ {
		for j := i; j > 0 && paths[j].first < paths[j-1].first; j-- {
			paths[j], paths[j-1] = paths[j-1], paths[j]
		}
	}
	transient := func(p string) bool { return strings.Contains(p, "/.tmp-") }
	relative := func(p string) (string, bool) { return strings.TrimPrefix(p, "/r/"), true }
	return resolveBatch(paths, b.exists, transient, relative)
}

func kindsOf(res batchResult) []string {
	out := []string{}
	for _, e := range res.events {
		out = append(out, e.in.Type+" "+e.in.Subject)
	}
	return out
}

func dataOf(t *testing.T, e emitted) map[string]any {
	t.Helper()
	var data map[string]any
	require.NoError(t, json.Unmarshal(e.in.Data, &data))
	return data
}

func TestUnit_ResolveBatch_CreateWriteAndDelete(t *testing.T) {
	res := newBatch().created("/r/a.go", false).wrote("/r/a.go").onDisk("/r/a.go", false, 12).resolve()
	require.Equal(t, []string{"fs.created /r/a.go"}, kindsOf(res), "a create and its first write are one event")
	require.EqualValues(t, 12, dataOf(t, res.events[0])["size"])

	res = newBatch().wrote("/r/a.go").onDisk("/r/a.go", false, 20).resolve()
	require.Equal(t, []string{"fs.modified /r/a.go"}, kindsOf(res))

	res = newBatch().wrote("/r/src").onDisk("/r/src", true, 0).resolve()
	require.Empty(t, kindsOf(res), "a folder's own write notification says nothing its children do not")

	res = newBatch().removed("/r/a.go").resolve()
	require.Equal(t, []string{"fs.deleted /r/a.go"}, kindsOf(res))
	require.Equal(t, map[string]any{"isDir": false}, dataOf(t, res.events[0]))

	res = newBatch().created("/r/gone.txt", false).removed("/r/gone.txt").resolve()
	require.Empty(t, kindsOf(res), "created and removed in one window leaves no trace")
}

func TestUnit_ResolveBatch_RenamesBecomeMoves(t *testing.T) {
	res := newBatch().renamed("/r/notes.md").created("/r/renamed.md", false).onDisk("/r/renamed.md", false, 6).resolve()
	require.Equal(t, []string{"fs.moved /r/renamed.md"}, kindsOf(res))
	require.Equal(t, "notes.md", dataOf(t, res.events[0])["from"])

	res = newBatch().created("/r/after.md", false).renamed("/r/before.md").onDisk("/r/after.md", false, 1).resolve()
	require.Equal(t, []string{"fs.created /r/after.md", "fs.deleted /r/before.md"}, kindsOf(res), "a create that came before the rename is not its target")

	res = newBatch().renamed("/r/pkg").dirGone("/r/pkg").created("/r/lib", true).onDisk("/r/lib", true, 0).resolve()
	require.Equal(t, []string{"fs.moved /r/lib"}, kindsOf(res))
	require.Equal(t, []string{"/r/pkg"}, res.forget, "the old folder's watches are dropped")
}

func TestUnit_ResolveBatch_AtomicSavesAndTransientFiles(t *testing.T) {
	res := newBatch().
		created("/r/.renamed.md.swp", false).wrote("/r/.renamed.md.swp").renamed("/r/.renamed.md.swp").
		created("/r/renamed.md", false).onDisk("/r/renamed.md", false, 7).resolve()
	require.Equal(t, []string{"fs.modified /r/renamed.md"}, kindsOf(res), "a file written and renamed over a path in one window is a save of that path")

	res = newBatch().renamed("/r/.tmp-123").created("/r/config.json", false).onDisk("/r/config.json", false, 2).resolve()
	require.Equal(t, []string{"fs.modified /r/config.json"}, kindsOf(res), "a transient temp renamed over a path is a save, even across windows")

	res = newBatch().created("/r/.tmp-9", false).wrote("/r/.tmp-9").onDisk("/r/.tmp-9", false, 3).resolve()
	require.Empty(t, kindsOf(res), "a transient file alone is not news")

	res = newBatch().removed("/r/.tmp-9").resolve()
	require.Empty(t, kindsOf(res))
}

func TestUnit_ResolveBatch_KeepsOrderAndForgetsVanishedFolders(t *testing.T) {
	res := newBatch().created("/r/b", true).created("/r/a.go", false).onDisk("/r/b", true, 0).onDisk("/r/a.go", false, 1).resolve()
	require.Equal(t, []string{"fs.created /r/b", "fs.created /r/a.go"}, kindsOf(res), "events keep the order the changes happened in")
	require.Equal(t, true, dataOf(t, res.events[0])["isDir"])

	res = newBatch().created("/r/tmpdir", true).resolve()
	require.Empty(t, kindsOf(res))
	require.Equal(t, []string{"/r/tmpdir"}, res.forget)

	res = newBatch().dirGone("/r/old").removed("/r/old").resolve()
	require.Equal(t, []string{"fs.deleted /r/old"}, kindsOf(res))
	require.Equal(t, true, dataOf(t, res.events[0])["isDir"])
	require.Equal(t, []string{"/r/old"}, res.forget)
}
