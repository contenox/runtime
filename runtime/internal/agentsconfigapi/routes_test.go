package agentsconfigapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/runtime/agentdecl"
	"github.com/contenox/runtime/runtime/internal/agentsconfigapi"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	server  *httptest.Server
	home    string
	roots   []string
	applied []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{home: t.TempDir()}
	shipped, err := os.ReadFile(filepath.Join("..", "..", "agentdecl", "agents.toml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(f.home, agentdecl.ConfigFilename), shipped, 0o644))
	mux := http.NewServeMux()
	agentsconfigapi.AddRoutes(mux, agentsconfigapi.Deps{
		Home:  f.home,
		Roots: func() []string { return f.roots },
		Applied: func(_ context.Context, scope, root string) {
			f.applied = append(f.applied, scope+":"+root)
		},
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fixture) call(t *testing.T, method, path string, body any, headers ...string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequest(method, f.server.URL+path, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUnit_AgentsConfigAPI_EnvelopeCrudKeepsTheFileAndAppliesEachChange(t *testing.T) {
	f := newFixture(t)
	status, doc := f.call(t, http.MethodGet, "/agents-config", nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, doc["exists"])
	before := doc["raw"].(string)

	status, item := f.call(t, http.MethodPost, "/agents-config/envelopes", map[string]any{
		"name":  "mine",
		"value": map[string]any{"extends": "read_only", "compute": map[string]any{"max_tool_calls": 12}},
	})
	require.Equal(t, http.StatusCreated, status, item)
	require.Equal(t, true, item["inLayer"])

	disk, err := os.ReadFile(filepath.Join(f.home, agentdecl.ConfigFilename))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(disk), before), "everything that was in the file is still there")
	require.Contains(t, string(disk), "max_tool_calls = 12")
	require.Equal(t, []string{"home:"}, f.applied)

	status, _ = f.call(t, http.MethodPost, "/agents-config/envelopes", map[string]any{"name": "mine", "value": map[string]any{}})
	require.Equal(t, http.StatusConflict, status)
	status, _ = f.call(t, http.MethodPut, "/agents-config/envelopes/ghost", map[string]any{"value": map[string]any{}})
	require.Equal(t, http.StatusNotFound, status)

	status, item = f.call(t, http.MethodPut, "/agents-config/envelopes/mine", map[string]any{"value": map[string]any{"extends": "read_only", "description": "changed"}})
	require.Equal(t, http.StatusOK, status, item)
	require.Equal(t, "changed", item["effective"].(map[string]any)["description"])

	status, _ = f.call(t, http.MethodDelete, "/agents-config/envelopes/mine", nil)
	require.Equal(t, http.StatusOK, status)
	status, _ = f.call(t, http.MethodGet, "/agents-config/envelopes/mine", nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Len(t, f.applied, 3, "only changes that wrote something are applied")

	_, _ = f.call(t, http.MethodDelete, "/agents-config/envelopes/mine", nil)
	require.Len(t, f.applied, 3, "a refused request applies nothing")
}

func TestUnit_AgentsConfigAPI_BreakingAndStaleChangesAreRefusedWithoutWriting(t *testing.T) {
	f := newFixture(t)
	_, doc := f.call(t, http.MethodGet, "/agents-config", nil)
	revision := doc["revision"].(string)

	status, body := f.call(t, http.MethodPost, "/agents-config/envelopes", map[string]any{"name": "bad", "value": map[string]any{"extends": "missing"}})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	require.Contains(t, body["error"].(map[string]any)["message"], "nothing was written")
	status, _ = f.call(t, http.MethodPost, "/agents-config/envelopes", map[string]any{"name": "has space", "value": map[string]any{}})
	require.Equal(t, http.StatusUnprocessableEntity, status)

	_, doc = f.call(t, http.MethodGet, "/agents-config", nil)
	require.Equal(t, revision, doc["revision"])
	require.Empty(t, f.applied)

	status, _ = f.call(t, http.MethodPost, "/agents-config/envelopes", map[string]any{"name": "late", "value": map[string]any{"extends": "read_only"}}, "If-Match", `"`+strings.Repeat("0", 64)+`"`)
	require.Equal(t, http.StatusConflict, status)
	status, _ = f.call(t, http.MethodPost, "/agents-config/envelopes", map[string]any{"name": "late", "value": map[string]any{"extends": "read_only"}, "revision": revision})
	require.Equal(t, http.StatusCreated, status)
}

func TestUnit_AgentsConfigAPI_WorkspaceLayersNeedAGrantedFolderAndStaySeparate(t *testing.T) {
	f := newFixture(t)
	folder := t.TempDir()

	status, _ := f.call(t, http.MethodGet, "/agents-config?scope=workspace", nil)
	require.Equal(t, http.StatusBadRequest, status)
	status, body := f.call(t, http.MethodGet, "/agents-config?scope=workspace&workspace="+url.QueryEscape(folder), nil)
	require.Equal(t, http.StatusUnprocessableEntity, status)
	require.Contains(t, body["error"].(map[string]any)["message"], "granted workspace")
	status, _ = f.call(t, http.MethodGet, "/agents-config?scope=other", nil)
	require.Equal(t, http.StatusBadRequest, status)

	f.roots = []string{folder}
	query := "?scope=workspace&workspace=" + url.QueryEscape(folder)
	status, item := f.call(t, http.MethodPost, "/agents-config/envelopes"+query, map[string]any{"name": "local", "value": map[string]any{"extends": "strict"}})
	require.Equal(t, http.StatusCreated, status, item)
	require.Equal(t, "workspace", item["scope"])

	layer, err := os.ReadFile(filepath.Join(folder, ".contenox", agentdecl.ConfigFilename))
	require.NoError(t, err)
	require.Contains(t, string(layer), "[envelopes.local]")
	home, err := os.ReadFile(filepath.Join(f.home, agentdecl.ConfigFilename))
	require.NoError(t, err)
	require.NotContains(t, string(home), "[envelopes.local]", "the global file is not touched")
	require.Equal(t, []string{"workspace:" + folder}, f.applied)

	status, _ = f.call(t, http.MethodGet, "/agents-config/envelopes/local", nil)
	require.Equal(t, http.StatusNotFound, status, "the global view does not see it")
	status, inherited := f.call(t, http.MethodGet, "/agents-config/envelopes/strict"+query, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, false, inherited["inLayer"], "strict comes from the global layer, not this folder's file")
}

func TestUnit_AgentsConfigAPI_SectionsKindsAndWholeFile(t *testing.T) {
	f := newFixture(t)
	status, _ := f.call(t, http.MethodGet, "/agents-config/nothing", nil)
	require.Equal(t, http.StatusNotFound, status)
	status, _ = f.call(t, http.MethodPut, "/agents-config/envelopes", map[string]any{"value": map[string]any{}})
	require.Equal(t, http.StatusBadRequest, status, "a list section is changed one item at a time")
	status, _ = f.call(t, http.MethodPost, "/agents-config/chain", map[string]any{"name": "x", "value": map[string]any{}})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = f.call(t, http.MethodGet, "/agents-config/chain/x", nil)
	require.Equal(t, http.StatusNotFound, status)

	status, section := f.call(t, http.MethodPut, "/agents-config/chain", map[string]any{"value": map[string]any{"main_rounds": 21}})
	require.Equal(t, http.StatusOK, status, section)
	require.EqualValues(t, 21, section["effective"].(map[string]any)["main_rounds"])
	status, _ = f.call(t, http.MethodDelete, "/agents-config/chain", nil)
	require.Equal(t, http.StatusOK, status)
	status, _ = f.call(t, http.MethodDelete, "/agents-config/chain", nil)
	require.Equal(t, http.StatusNotFound, status)

	status, listing := f.call(t, http.MethodGet, "/agents-config/tools", nil)
	require.Equal(t, http.StatusOK, status)
	require.NotEmpty(t, listing["items"])

	status, _ = f.call(t, http.MethodPut, "/agents-config", map[string]any{"raw": "[envelopes.x\nbroken"})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	status, doc := f.call(t, http.MethodPut, "/agents-config", map[string]any{"raw": "# only a comment\n"})
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "# only a comment\n", doc["raw"])
}

func TestUnit_AgentsConfigAPI_PatchMergesInsteadOfReplacing(t *testing.T) {
	f := newFixture(t)
	status, item := f.call(t, http.MethodPatch, "/agents-config/envelopes/strict", map[string]any{
		"value": map[string]any{"description": "Patched.", "compute": map[string]any{"max_tool_calls": 11}},
	})
	require.Equal(t, http.StatusOK, status, item)
	effective := item["effective"].(map[string]any)
	require.Equal(t, "Patched.", effective["description"])
	require.Equal(t, "ask_always", effective["extends"], "keys the patch did not name are kept")
	require.EqualValues(t, 11, effective["compute"].(map[string]any)["max_tool_calls"])
	require.EqualValues(t, 1000000, effective["compute"].(map[string]any)["max_tokens"])
	require.Equal(t, []string{"home:"}, f.applied)

	status, _ = f.call(t, http.MethodPatch, "/agents-config/envelopes/ghost", map[string]any{"value": map[string]any{"description": "x"}})
	require.Equal(t, http.StatusNotFound, status)
	status, _ = f.call(t, http.MethodPatch, "/agents-config/envelopes/strict", map[string]any{"value": map[string]any{"extends": "missing"}})
	require.Equal(t, http.StatusUnprocessableEntity, status, "a patch that breaks the config is refused like any other change")
	status, _ = f.call(t, http.MethodPatch, "/agents-config/envelopes/strict", map[string]any{"value": "text"})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	require.Len(t, f.applied, 1)
}

func TestUnit_AgentsConfigAPI_PatchAcceptsAPlainMergePatchBody(t *testing.T) {
	f := newFixture(t)
	req, err := http.NewRequest(http.MethodPatch, f.server.URL+"/agents-config/envelopes/strict", strings.NewReader(`{"description":"Raw patch.","network":null}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/merge-patch+json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var item map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&item))
	require.Equal(t, "Raw patch.", item["effective"].(map[string]any)["description"])
	require.NotContains(t, item["layer"].(map[string]any), "network", "null removes the key from the file")

	stale, err := http.NewRequest(http.MethodPatch, f.server.URL+"/agents-config/envelopes/strict", strings.NewReader(`{"description":"Late."}`))
	require.NoError(t, err)
	stale.Header.Set("Content-Type", "application/merge-patch+json")
	stale.Header.Set("If-Match", `"`+strings.Repeat("0", 64)+`"`)
	resp2, err := http.DefaultClient.Do(stale)
	require.NoError(t, err)
	resp2.Body.Close()
	require.Equal(t, http.StatusConflict, resp2.StatusCode)
}

func TestUnit_AgentsConfigAPI_PatchOfASingletonCreatesTheSectionInAWorkspaceLayer(t *testing.T) {
	f := newFixture(t)
	folder := t.TempDir()
	f.roots = []string{folder}
	query := "?scope=workspace&workspace=" + url.QueryEscape(folder)

	status, section := f.call(t, http.MethodPatch, "/agents-config/chain"+query, map[string]any{"value": map[string]any{"main_rounds": 5}})
	require.Equal(t, http.StatusOK, status, section)
	require.Equal(t, true, section["inLayer"])
	require.EqualValues(t, 5, section["effective"].(map[string]any)["main_rounds"])
	require.EqualValues(t, 8, section["effective"].(map[string]any)["recovery_rounds"], "the rest is inherited")

	status, _ = f.call(t, http.MethodPatch, "/agents-config/envelopes"+query, map[string]any{"value": map[string]any{}})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = f.call(t, http.MethodPatch, "/agents-config/chain/x"+query, map[string]any{"value": map[string]any{}})
	require.Equal(t, http.StatusNotFound, status)
}
