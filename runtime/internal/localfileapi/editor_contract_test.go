package localfileapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/eventlog"
	"github.com/contenox/runtime/runtime/internal/localfileapi"
	"github.com/contenox/runtime/runtime/localfileservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspaceevents"
	"github.com/stretchr/testify/require"
)

type editorFixture struct {
	t      *testing.T
	server *httptest.Server
	events workspaceevents.Service
	root   string
}

func newEditorFixture(t *testing.T) editorFixture {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "editor.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	root, err := vfs.ResolveRoot(t.TempDir())
	require.NoError(t, err)
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)
	bus := libbus.NewInMem()
	t.Cleanup(func() { _ = bus.Close() })
	events := workspaceevents.New("ws-editor", eventlog.NewService(db, nil, libtracker.NoopTracker{}), bus, factory, libtracker.NoopTracker{})
	mux := http.NewServeMux()
	require.NoError(t, localfileapi.AddWorkspaceRoutes(mux, factory, nil, localfileapi.WithEvents(events)))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return editorFixture{t: t, server: server, events: events, root: root}
}

func (f editorFixture) file(rel, body string) {
	abs := filepath.Join(f.root, filepath.FromSlash(rel))
	require.NoError(f.t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(f.t, os.WriteFile(abs, []byte(body), 0o644))
}

func (f editorFixture) disk(rel string) string {
	raw, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(rel)))
	require.NoError(f.t, err)
	return string(raw)
}

func (f editorFixture) do(method, path string, body any, header map[string]string) (int, http.Header, map[string]any) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.server.URL+path, reader)
	require.NoError(f.t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(f.t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, resp.Header, out
}

func (f editorFixture) saves() []workspaceevents.Event {
	all, err := f.events.Since(context.Background(), f.root, 0, 0)
	require.NoError(f.t, err)
	out := []workspaceevents.Event{}
	for _, e := range all {
		if e.Type == workspaceevents.TypeEditorSaved {
			out = append(out, e)
		}
	}
	return out
}

func TestUnit_EditorContract_ReadCarriesTheVersionTheAgentGuardUses(t *testing.T) {
	f := newEditorFixture(t)
	f.file("main.go", "package main\n")
	status, header, body := f.do("GET", "/files/content?path=main.go", nil, nil)
	require.Equal(t, http.StatusOK, status)
	want := localfileservice.ETag([]byte("package main\n"))
	require.Equal(t, want, body["etag"])
	require.Equal(t, strconv.Quote(want), header.Get("ETag"))
	require.Equal(t, want, body["metadata"].(map[string]any)["etag"])
}

func TestUnit_EditorContract_StaleSaveIsRefusedWithTheCurrentVersion(t *testing.T) {
	f := newEditorFixture(t)
	f.file("notes.md", "v1\n")
	_, _, read := f.do("GET", "/files/content?path=notes.md", nil, nil)
	base := read["etag"].(string)

	status, header, saved := f.do("PUT", "/files", map[string]string{"path": "notes.md", "content": "v2 from the editor\n"}, map[string]string{"If-Match": strconv.Quote(base)})
	require.Equal(t, http.StatusOK, status, "%v", saved)
	v2 := localfileservice.ETag([]byte("v2 from the editor\n"))
	require.Equal(t, v2, saved["etag"])
	require.Equal(t, strconv.Quote(v2), header.Get("ETag"))

	f.file("notes.md", "v3 from the agent\n")
	status, header, refused := f.do("PUT", "/files", map[string]string{"path": "notes.md", "content": "stale editor buffer\n"}, map[string]string{"If-Match": v2})
	require.Equal(t, http.StatusConflict, status)
	errBody := refused["error"].(map[string]any)
	v3 := localfileservice.ETag([]byte("v3 from the agent\n"))
	require.Equal(t, "file_changed", errBody["code"])
	require.Equal(t, v3, errBody["currentEtag"])
	require.Equal(t, "notes.md", errBody["path"])
	require.Equal(t, strconv.Quote(v3), header.Get("ETag"))
	require.Equal(t, "v3 from the agent\n", f.disk("notes.md"), "a refused save leaves the disk alone")

	require.NoError(t, os.Remove(filepath.Join(f.root, "notes.md")))
	status, _, gone := f.do("PUT", "/files", map[string]string{"path": "notes.md", "content": "x"}, map[string]string{"If-Match": v3})
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, "", gone["error"].(map[string]any)["currentEtag"], "a deleted file is a conflict too")

	status, _, blind := f.do("PUT", "/files", map[string]string{"path": "notes.md", "content": "blind\n"}, nil)
	require.Equal(t, http.StatusPreconditionRequired, status, "a save that names no version is refused")
	require.Equal(t, "precondition_required", blind["error"].(map[string]any)["code"])

	status, _, gone = f.do("PUT", "/files", map[string]string{"path": "notes.md", "content": "any\n"}, map[string]string{"If-Match": "*"})
	require.Equal(t, http.StatusConflict, status, "* replaces whatever is there, and nothing is there")
	require.Equal(t, "", gone["error"].(map[string]any)["currentEtag"])
	_, statErr := os.Stat(filepath.Join(f.root, "notes.md"))
	require.True(t, os.IsNotExist(statErr), "refused saves create nothing")

	status, _, _ = f.do("POST", "/files", map[string]string{"path": "notes.md", "content": "recreated\n"}, nil)
	require.Equal(t, http.StatusCreated, status, "a deleted file comes back through create")
	status, _, _ = f.do("PUT", "/files", map[string]string{"path": "notes.md", "content": "replaced\n"}, map[string]string{"If-Match": "*"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "replaced\n", f.disk("notes.md"))

	saves := f.saves()
	require.Len(t, saves, 3, "only the accepted saves are events")
	require.Equal(t, "notes.md", saves[0].Subject)
	var data map[string]any
	require.NoError(t, json.Unmarshal(saves[0].Data, &data))
	require.Equal(t, v2, data["etag"])
}

func TestUnit_EditorContract_ReplacePreviewIsWhatApplyWrites(t *testing.T) {
	f := newEditorFixture(t)
	f.file("a.go", "func OldName() {}\n")
	f.file("pkg/b.go", "OldName()\nOldName()\n")
	f.file("docs/c.md", "OldName\n")
	f.file("node_modules/x/d.go", "OldName\n")
	f.file(".contenox/agents.toml", "OldName\n")
	require.NoError(t, vfs.SetControlPlaneDenied(filepath.Join(f.root, ".contenox")))
	t.Cleanup(func() { _ = vfs.SetControlPlaneDenied() })

	status, _, preview := f.do("POST", "/workspace/replace", map[string]any{"pattern": "OldName", "replacement": "NewName", "caseSensitive": true, "include": []string{"*.go"}}, nil)
	require.Equal(t, http.StatusOK, status, "%v", preview)
	id := preview["id"].(string)
	require.NotEmpty(t, id)
	require.EqualValues(t, 3, preview["totalMatches"])
	files := preview["files"].([]any)
	require.Len(t, files, 2)
	first := files[0].(map[string]any)
	require.Equal(t, "a.go", first["path"])
	require.Equal(t, localfileservice.ETag([]byte("func OldName() {}\n")), first["etag"])
	hunk := first["hunks"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"func OldName() {}"}, hunk["before"])
	require.Equal(t, []any{"func NewName() {}"}, hunk["after"])

	f.file("pkg/b.go", "OldName()\nchanged by someone else\n")
	status, _, applied := f.do("POST", "/workspace/replace/"+id+"/apply", map[string]any{}, nil)
	require.Equal(t, http.StatusOK, status, "%v", applied)
	require.Equal(t, []any{map[string]any{"path": "a.go", "etag": localfileservice.ETag([]byte("func NewName() {}\n")), "matches": float64(1)}}, applied["applied"])
	require.Equal(t, []any{map[string]any{"path": "pkg/b.go", "currentEtag": localfileservice.ETag([]byte("OldName()\nchanged by someone else\n"))}}, applied["conflicts"])
	require.Equal(t, "func NewName() {}\n", f.disk("a.go"))
	require.Equal(t, "OldName()\nchanged by someone else\n", f.disk("pkg/b.go"))
	require.Equal(t, "OldName\n", f.disk("node_modules/x/d.go"))
	require.Equal(t, "OldName\n", f.disk(".contenox/agents.toml"))
	require.Equal(t, "OldName\n", f.disk("docs/c.md"))

	saves := f.saves()
	require.Len(t, saves, 1)
	var data map[string]any
	require.NoError(t, json.Unmarshal(saves[0].Data, &data))
	require.Equal(t, id, data["replace"])

	status, _, _ = f.do("POST", "/workspace/replace/"+id+"/apply", map[string]any{}, nil)
	require.Equal(t, http.StatusNotFound, status, "a preview is applied once")
}

func TestUnit_EditorContract_ReplaceAppliesAChosenSubset(t *testing.T) {
	f := newEditorFixture(t)
	f.file("one.txt", "x\n")
	f.file("two.txt", "x\n")
	_, _, preview := f.do("POST", "/workspace/replace", map[string]any{"pattern": "x", "replacement": "y"}, nil)
	id := preview["id"].(string)

	status, _, _ := f.do("POST", "/workspace/replace/"+id+"/apply", map[string]any{"paths": []string{"elsewhere.txt"}}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, status)

	status, _, applied := f.do("POST", "/workspace/replace/"+id+"/apply", map[string]any{"paths": []string{"two.txt"}}, nil)
	require.Equal(t, http.StatusOK, status, "a refused subset keeps the preview: %v", applied)
	require.Len(t, applied["applied"], 1)
	require.Equal(t, "x\n", f.disk("one.txt"))
	require.Equal(t, "y\n", f.disk("two.txt"))
}

func TestUnit_EditorContract_ReplaceRefusesBadQueries(t *testing.T) {
	f := newEditorFixture(t)
	f.file("a.txt", "x\n")
	for _, body := range []map[string]any{
		{"pattern": ""},
		{"pattern": "(", "regex": true},
		{"pattern": "x", "path": "../outside"},
		{"pattern": "x", "include": []string{"["}},
	} {
		status, _, _ := f.do("POST", "/workspace/replace", body, nil)
		require.Equal(t, http.StatusUnprocessableEntity, status, "%v", body)
	}
	status, _, _ := f.do("POST", "/workspace/replace?root="+t.TempDir(), map[string]any{"pattern": "x"}, nil)
	require.Equal(t, http.StatusUnprocessableEntity, status)
}

func TestUnit_EditorContract_AFileThatFailedForAnotherReasonStaysForARetry(t *testing.T) {
	f := newEditorFixture(t)
	f.file("ok.txt", "x\n")
	f.file("stuck.txt", "x\n")
	_, _, preview := f.do("POST", "/workspace/replace", map[string]any{"pattern": "x", "replacement": "y"}, nil)
	id := preview["id"].(string)

	stuck := filepath.Join(f.root, "stuck.txt")
	require.NoError(t, os.Remove(stuck))
	require.NoError(t, os.Mkdir(stuck, 0o755))
	status, _, applied := f.do("POST", "/workspace/replace/"+id+"/apply", map[string]any{}, nil)
	require.Equal(t, http.StatusOK, status, "%v", applied)
	require.Len(t, applied["applied"], 1)
	require.Empty(t, applied["conflicts"])
	failed := applied["failed"].([]any)
	require.Len(t, failed, 1)
	require.Equal(t, "stuck.txt", failed[0].(map[string]any)["path"])
	message := failed[0].(map[string]any)["message"].(string)
	require.NotEmpty(t, message)
	require.NotContains(t, message, f.root, "the reason names no path on disk")
	require.Equal(t, true, applied["retry"])

	require.NoError(t, os.Remove(stuck))
	f.file("stuck.txt", "x\n")
	status, _, retried := f.do("POST", "/workspace/replace/"+id+"/apply", map[string]any{}, nil)
	require.Equal(t, http.StatusOK, status, "%v", retried)
	require.Equal(t, []any{map[string]any{"path": "stuck.txt", "etag": localfileservice.ETag([]byte("y\n")), "matches": float64(1)}}, retried["applied"])
	require.Equal(t, false, retried["retry"])
	require.Equal(t, "y\n", f.disk("stuck.txt"))

	status, _, _ = f.do("POST", "/workspace/replace/"+id+"/apply", map[string]any{}, nil)
	require.Equal(t, http.StatusNotFound, status, "nothing is left to retry")
}
