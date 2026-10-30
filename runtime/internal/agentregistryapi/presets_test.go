package agentregistryapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentregistryservice"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func presetTestServer(t *testing.T) (*http.ServeMux, string) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "presets.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	svc := agentregistryservice.New(db)
	mux := http.NewServeMux()
	AddAgentRegistryRoutes(mux, svc)
	AddAgentPresetRoutes(mux, svc, dir)
	return mux, dir
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestUnit_AgentPresetInstallRegistersAgent(t *testing.T) {
	mux, dir := presetTestServer(t)

	list := doJSON(t, mux, http.MethodGet, "/agent-presets", "")
	require.Equal(t, http.StatusOK, list.Code)
	var presets []PresetStatus
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &presets))
	require.NotEmpty(t, presets)
	for _, preset := range presets {
		require.False(t, preset.Installed)
	}

	install := doJSON(t, mux, http.MethodPost, "/agent-presets/reviewer", "")
	require.Equal(t, http.StatusCreated, install.Code, install.Body.String())
	require.FileExists(t, filepath.Join(dir, "agent-reviewer.json"))

	agents := doJSON(t, mux, http.MethodGet, "/agents", "")
	require.Equal(t, http.StatusOK, agents.Code)
	require.Contains(t, agents.Body.String(), `"reviewer"`)

	again := doJSON(t, mux, http.MethodPost, "/agent-presets/reviewer", "")
	require.Equal(t, http.StatusOK, again.Code)
	require.Contains(t, again.Body.String(), `"installed":false`)

	forced := doJSON(t, mux, http.MethodPost, "/agent-presets/reviewer?force=true", "")
	require.Equal(t, http.StatusCreated, forced.Code)
}

func TestUnit_AgentWriteEndpointsCreateUpdateDelete(t *testing.T) {
	mux, _ := presetTestServer(t)
	chainPath := filepath.Join(t.TempDir(), "agent-custom.json")

	createBody, err := json.Marshal(map[string]any{
		"name": "custom", "kind": "chain", "enabled": true,
		"configJson": map[string]any{"path": chainPath},
	})
	require.NoError(t, err)
	create := doJSON(t, mux, http.MethodPost, "/agents", string(createBody))
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())
	var created runtimetypes.Agent
	require.NoError(t, json.Unmarshal(create.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)
	require.Equal(t, "custom", created.Name)

	updateBody, err := json.Marshal(map[string]any{
		"name": "custom", "kind": "chain", "enabled": false,
		"configJson": map[string]any{"path": chainPath},
	})
	require.NoError(t, err)
	update := doJSON(t, mux, http.MethodPut, "/agents/"+created.ID, string(updateBody))
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())
	require.Contains(t, update.Body.String(), `"enabled":false`)

	del := doJSON(t, mux, http.MethodDelete, "/agents/"+created.ID, "")
	require.Equal(t, http.StatusOK, del.Code)
	_, err = os.Stat(filepath.Join(t.TempDir(), "unused"))
	require.Error(t, err)

	missing := doJSON(t, mux, http.MethodGet, "/agents/"+created.ID, "")
	require.NotEqual(t, http.StatusOK, missing.Code)
}
