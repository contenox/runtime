package agentidentityapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentidentity"
	"github.com/contenox/runtime/runtime/internal/agentidentityapi"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "identity.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mux := http.NewServeMux()
	agentidentityapi.AddRoutes(mux, runtimetypes.New(db.WithoutTransaction()), agentidentity.KeySource{SSHDir: t.TempDir()})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func call[T any](t *testing.T, method, url string, body any) (int, T) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequest(method, url, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out T
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestUnit_AgentIdentityAPI_GenerateSaveCheck(t *testing.T) {
	srv := newServer(t)

	status, state := call[agentidentityapi.State](t, http.MethodGet, srv.URL+"/agent-identity", nil)
	require.Equal(t, http.StatusOK, status)
	require.False(t, state.Identity.Active())
	require.True(t, state.Identity.CreditMe)
	require.Empty(t, state.Keys)

	status, key := call[agentidentity.Key](t, http.MethodPost, srv.URL+"/agent-identity/keys", agentidentityapi.GenerateRequest{Comment: "agent@example.com"})
	require.Equal(t, http.StatusCreated, status)
	require.True(t, key.Usable)

	status, _ = call[map[string]any](t, http.MethodPost, srv.URL+"/agent-identity/keys", agentidentityapi.GenerateRequest{})
	require.Equal(t, http.StatusConflict, status, "a second generate never overwrites the agent's key")

	status, _ = call[map[string]any](t, http.MethodPut, srv.URL+"/agent-identity", agentidentity.Identity{Name: "Agent", Sign: true})
	require.Equal(t, http.StatusUnprocessableEntity, status)

	want := agentidentity.Identity{Name: "Contenox Agent", Email: "agent@example.com", Key: key.Ref, Sign: true, CreditMe: false}
	status, state = call[agentidentityapi.State](t, http.MethodPut, srv.URL+"/agent-identity", want)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, want, state.Identity)
	require.Len(t, state.Keys, 1)

	status, result := call[agentidentityapi.CheckResult](t, http.MethodPost, srv.URL+"/agent-identity/check", agentidentityapi.CheckRequest{})
	require.Equal(t, http.StatusOK, status)
	require.True(t, result.Signing.OK, result.Signing.Message)
	require.Equal(t, key.Fingerprint, result.Signing.Fingerprint)
	require.Nil(t, result.Host, "no host was asked for, so none is contacted")
}
