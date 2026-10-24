package contenoxcli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/localfileapi"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/contenox/runtime/runtime/workspacegrants"
	"github.com/stretchr/testify/require"
)

type rootsResponse struct {
	Roots []struct {
		Path    string `json:"path"`
		Default bool   `json:"default"`
	} `json:"roots"`
}

func (r rootsResponse) paths() []string {
	out := make([]string, 0, len(r.Roots))
	for _, e := range r.Roots {
		out = append(out, e.Path)
	}
	return out
}

func getRoots(t *testing.T, base string) rootsResponse {
	t.Helper()
	resp, err := http.Get(base + "/workspace/roots")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body rootsResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func TestSystem_WorkspaceGrants_LANStoryEndToEnd(t *testing.T) {
	ctx := context.Background()

	baseRoot := t.TempDir()
	grantRoot := t.TempDir()
	grantSub := filepath.Join(grantRoot, "project", "pkg")
	require.NoError(t, os.MkdirAll(grantSub, 0o755))

	dbPath := filepath.Join(t.TempDir(), "workspace-e2e.db")
	db, err := libdb.NewSQLiteDBManager(ctx, dbPath, runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	store := runtimetypes.New(db.WithoutTransaction())
	bus := libbus.NewSQLite(db.WithoutTransaction())
	defer func() { _ = bus.Close() }()

	factory, err := vfs.NewFactory(baseRoot)
	require.NoError(t, err)
	reloader := newWorkspaceRootReloader(factory, []string{baseRoot}, store)
	stop, err := startWorkspaceRootReloader(ctx, bus, reloader)
	require.NoError(t, err)
	defer stop()

	mux := http.NewServeMux()
	localfileapi.AddWorkspaceRootsRoutes(mux, factory, reloader.mutators(bus))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resolvedBase, _ := vfs.ResolveRoot(baseRoot)
	resolvedGrant, _ := vfs.ResolveRoot(grantRoot)
	resolvedSub, _ := vfs.ResolveRoot(grantSub)

	baseline := getRoots(t, srv.URL)
	require.Equal(t, []string{resolvedBase}, baseline.paths())
	require.True(t, baseline.Roots[0].Default)

	_, err = vfs.ResolveSessionCwd(factory, grantSub, "")
	require.Error(t, err, "a cwd under an ungranted root must be refused before the grant")

	body, _ := json.Marshal(map[string]string{"path": grantRoot})
	resp, err := http.Post(srv.URL+"/workspace/roots", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	afterAdd := getRoots(t, srv.URL)
	require.ElementsMatch(t, []string{resolvedBase, resolvedGrant}, afterAdd.paths())

	resolved, err := vfs.ResolveSessionCwd(factory, grantSub, "")
	require.NoError(t, err)
	require.Equal(t, resolvedSub, resolved)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/workspace/roots?path="+resolvedGrant, nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	afterDelete := getRoots(t, srv.URL)
	require.Equal(t, []string{resolvedBase}, afterDelete.paths())
	_, err = vfs.ResolveSessionCwd(factory, grantSub, "")
	require.Error(t, err, "after revoke, a cwd under the (former) root is refused again")

	grant2 := t.TempDir()
	resolvedGrant2, _ := vfs.ResolveRoot(grant2)
	cliBus := libbus.NewSQLite(db.WithoutTransaction())
	defer func() { _ = cliBus.Close() }()
	roots, err := workspacegrants.Add(ctx, store, grant2)
	require.NoError(t, err)
	require.NoError(t, workspacegrants.PublishChanged(ctx, cliBus, roots))

	require.Eventually(t, func() bool {
		_, ok := factory.Allows(grant2)
		return ok
	}, 10*time.Second, 50*time.Millisecond,
		"the cross-process doorbell never applied the grant to the live factory")

	final := getRoots(t, srv.URL)
	require.ElementsMatch(t, []string{resolvedBase, resolvedGrant2}, final.paths())
}
