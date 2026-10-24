package localfileapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/runtime/internal/localfileapi"
	"github.com/contenox/runtime/runtime/vfs"
	"github.com/stretchr/testify/require"
)

type workspaceRootsResponse struct {
	Roots []struct {
		Path    string `json:"path"`
		Default bool   `json:"default"`
	} `json:"roots"`
}

func getWorkspaceRoots(t *testing.T, mux *http.ServeMux) (*http.Response, workspaceRootsResponse) {
	t.Helper()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/workspace/roots")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body workspaceRootsResponse
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	}
	return resp, body
}

func TestUnit_WorkspaceRootsRoutes_SingleRoot(t *testing.T) {
	root := t.TempDir()
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)

	mux := http.NewServeMux()
	localfileapi.AddWorkspaceRootsRoutes(mux, factory, nil)

	resp, body := getWorkspaceRoots(t, mux)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, body.Roots, 1)

	resolvedRoot, err := vfs.ResolveRoot(root)
	require.NoError(t, err)
	require.Equal(t, resolvedRoot, body.Roots[0].Path)
	require.True(t, body.Roots[0].Default, "the sole configured root must be marked default")
}

func TestUnit_WorkspaceRootsRoutes_MultiRoot(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	rootC := t.TempDir()
	factory, err := vfs.NewFactory(rootA, rootB, rootC)
	require.NoError(t, err)

	mux := http.NewServeMux()
	localfileapi.AddWorkspaceRootsRoutes(mux, factory, nil)

	resp, body := getWorkspaceRoots(t, mux)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, body.Roots, 3)

	resolvedA, err := vfs.ResolveRoot(rootA)
	require.NoError(t, err)

	defaults := 0
	paths := map[string]bool{}
	for _, r := range body.Roots {
		paths[r.Path] = true
		if r.Default {
			defaults++
			require.Equal(t, resolvedA, r.Path, "the default flag must mark the first configured root")
		}
	}
	require.Equal(t, 1, defaults, "exactly one root must be marked default")

	resolvedB, err := vfs.ResolveRoot(rootB)
	require.NoError(t, err)
	resolvedC, err := vfs.ResolveRoot(rootC)
	require.NoError(t, err)
	require.True(t, paths[resolvedB])
	require.True(t, paths[resolvedC])
}

func TestUnit_WorkspaceRootsRoutes_ExcludesControlPlanePath(t *testing.T) {
	root := t.TempDir()
	controlPlaneDir := t.TempDir()
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)

	mux := http.NewServeMux()
	localfileapi.AddWorkspaceRootsRoutes(mux, factory, nil)

	resp, body := getWorkspaceRoots(t, mux)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resolvedControlPlane, err := vfs.ResolveRoot(controlPlaneDir)
	require.NoError(t, err)
	for _, r := range body.Roots {
		require.NotEqual(t, resolvedControlPlane, r.Path, "a path never added to the allowlist must never appear as a root")
	}
}

func TestUnit_WorkspaceRootsRoutes_NilFactoryRegistersNothing(t *testing.T) {
	mux := http.NewServeMux()
	localfileapi.AddWorkspaceRootsRoutes(mux, nil, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/workspace/roots", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestUnit_WorkspaceRootsRoutes_PathsAreAbsolute(t *testing.T) {
	root := t.TempDir()
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)

	mux := http.NewServeMux()
	localfileapi.AddWorkspaceRootsRoutes(mux, factory, nil)

	_, body := getWorkspaceRoots(t, mux)
	require.Len(t, body.Roots, 1)
	require.True(t, filepath.IsAbs(body.Roots[0].Path))
	require.Equal(t, filepath.Clean(body.Roots[0].Path), body.Roots[0].Path)
}

func TestUnit_WorkspaceRootsRoutes_MethodAndPath(t *testing.T) {
	root := t.TempDir()
	factory, err := vfs.NewFactory(root)
	require.NoError(t, err)

	mux := http.NewServeMux()
	localfileapi.AddWorkspaceRootsRoutes(mux, factory, nil)

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/workspace/roots", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}
