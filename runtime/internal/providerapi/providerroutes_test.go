package providerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/providerservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	t     *testing.T
	ctx   context.Context
	db    libdb.DBManager
	store runtimetypes.Store
	srv   *httptest.Server
}

func newFixture(t *testing.T, withState bool) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "providerapi.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	var opts []providerservice.Option
	if withState {
		bus := libbus.NewSQLite(db.WithoutTransaction())
		t.Cleanup(func() { _ = bus.Close() })
		state, err := runtimestate.New(ctx, db, bus)
		require.NoError(t, err)
		opts = append(opts, providerservice.WithRuntimeState(state))
	}
	mux := http.NewServeMux()
	AddProviderRoutes(mux, providerservice.New(db, "workspace", opts...))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fixture{t: t, ctx: ctx, db: db, store: runtimetypes.New(db.WithoutTransaction()), srv: srv}
}

func (f *fixture) do(method, path string, body any) (int, []byte) {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(f.t, err)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, reader)
	require.NoError(f.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(f.t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(f.t, err)
	return resp.StatusCode, raw
}

func (f *fixture) connection(method, path string, body any, wantStatus int) providerservice.Connection {
	f.t.Helper()
	status, raw := f.do(method, path, body)
	require.Equal(f.t, wantStatus, status, string(raw))
	var conn providerservice.Connection
	require.NoError(f.t, json.Unmarshal(raw, &conn), string(raw))
	return conn
}

func (f *fixture) list() []providerservice.Connection {
	f.t.Helper()
	status, raw := f.do(http.MethodGet, "/providers/connections", nil)
	require.Equal(f.t, http.StatusOK, status, string(raw))
	var conns []providerservice.Connection
	require.NoError(f.t, json.Unmarshal(raw, &conns))
	return conns
}

func TestUnit_ProviderAPI_ConnectionsAutoNameAndNeverLeakSecrets(t *testing.T) {
	f := newFixture(t, false)

	require.Empty(t, f.list(), "no connections before any is created")

	first := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-first-secret"}, http.StatusCreated)
	second := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-second-secret"}, http.StatusCreated)
	named := f.connection("POST", "/providers/openai/connections", map[string]any{"name": "work", "apiKeyEnv": "WORK_OPENAI_KEY"}, http.StatusCreated)
	third := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-third-secret"}, http.StatusCreated)

	require.Equal(t, "openai", first.Name)
	require.Equal(t, "openai-2", second.Name)
	require.Equal(t, "work", named.Name)
	require.Equal(t, "openai-3", third.Name)
	require.NotEqual(t, first.ID, second.ID)

	require.Equal(t, "openai", first.ProviderType)
	require.Equal(t, "https://api.openai.com/v1", first.BaseURL)
	require.Equal(t, providerservice.ConfigConfigured, first.ConfigState)
	require.Equal(t, providerservice.AuthNotRequired, first.AuthState)
	require.Equal(t, providerservice.HealthUnknown, first.Health, "no runtime state is attached, so health is unknown")
	require.True(t, first.SecretConfigured)
	require.Equal(t, "literal", first.SecretSource)

	require.Equal(t, "env", named.SecretSource)
	require.Equal(t, "WORK_OPENAI_KEY", named.APIKeyEnv)
	require.False(t, named.SecretPresent, "the referenced variable is not set in this process")
	require.Equal(t, providerservice.ConfigInvalid, named.ConfigState, "an env reference whose variable is unset is an invalid configuration")
	require.Equal(t, "misconfigured", named.ErrorCode)

	status, raw := f.do(http.MethodGet, "/providers/connections", nil)
	require.Equal(t, http.StatusOK, status)
	for _, secret := range []string{"sk-first-secret", "sk-second-secret", "sk-third-secret"} {
		require.NotContains(t, string(raw), secret)
	}
	require.Len(t, f.list(), 4)

	for _, path := range []string{"/providers/openai/config", "/providers/configs", "/providers/openai/status"} {
		_, raw := f.do(http.MethodGet, path, nil)
		require.NotContains(t, string(raw), "sk-first-secret", path)
	}
}

func TestUnit_ProviderAPI_ConnectionsReusedNameIsAConflict(t *testing.T) {
	f := newFixture(t, false)
	f.connection("POST", "/providers/openai/connections", map[string]any{"name": "dup", "apiKey": "k"}, http.StatusCreated)
	status, raw := f.do("POST", "/providers/anthropic/connections", map[string]any{"name": "dup", "apiKey": "k"})
	require.Equal(t, http.StatusConflict, status, string(raw))
}

func TestUnit_ProviderAPI_ConnectionsValidateLikeConfigure(t *testing.T) {
	f := newFixture(t, false)
	cases := []struct {
		name     string
		path     string
		body     any
		wantCode int
	}{
		{"missing key", "/providers/openai/connections", map[string]any{}, http.StatusUnprocessableEntity},
		{"key and env", "/providers/openai/connections", map[string]any{"apiKey": "a", "apiKeyEnv": "B"}, http.StatusUnprocessableEntity},
		{"login provider rejects key", "/providers/openai-codex/connections", map[string]any{"apiKey": "a"}, http.StatusUnprocessableEntity},
		{"required base url", "/providers/vllm/connections", map[string]any{}, http.StatusUnprocessableEntity},
		{"unknown provider", "/providers/nope/connections", map[string]any{}, http.StatusUnprocessableEntity},
		{"bad name", "/providers/ollama/connections", map[string]any{"name": "connection:x"}, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := f.do("POST", tc.path, tc.body)
			require.Equal(t, tc.wantCode, status, string(raw))
		})
	}
	require.Empty(t, f.list(), "no rejected request may leave a backend behind")

	// A body-less create is fine for providers that need nothing.
	status, raw := f.do("POST", "/providers/ollama/connections", nil)
	require.Equal(t, http.StatusCreated, status, string(raw))
}

func TestUnit_ProviderAPI_ConnectionsShareBaseURLButKeepOwnCredentials(t *testing.T) {
	f := newFixture(t, false)
	a := f.connection("POST", "/providers/openai/connections", map[string]any{"name": "personal", "apiKey": "sk-personal"}, http.StatusCreated)
	b := f.connection("POST", "/providers/openai/connections", map[string]any{"name": "team", "apiKey": "sk-team"}, http.StatusCreated)
	require.Equal(t, a.BaseURL, b.BaseURL, "two accounts on the same API share a base URL")

	for _, tc := range []struct {
		id   string
		want string
	}{{a.ID, "sk-personal"}, {b.ID, "sk-team"}} {
		backend, err := f.store.GetBackend(f.ctx, tc.id)
		require.NoError(t, err)
		got, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, *backend)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "the runtime resolves each connection's own key")
	}

	// Rotating one connection's key leaves the other alone.
	f.connection("PATCH", "/providers/connections/"+a.ID, map[string]any{"apiKey": "sk-personal-2"}, http.StatusOK)
	backendA, _ := f.store.GetBackend(f.ctx, a.ID)
	backendB, _ := f.store.GetBackend(f.ctx, b.ID)
	gotA, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, *backendA)
	require.NoError(t, err)
	gotB, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, *backendB)
	require.NoError(t, err)
	require.Equal(t, "sk-personal-2", gotA)
	require.Equal(t, "sk-team", gotB)
}

func TestUnit_ProviderAPI_ConnectionCredentialFallsBackToLegacyPerTypeRow(t *testing.T) {
	f := newFixture(t, false)
	t.Setenv("LEGACY_MISTRAL_KEY", "from-env")

	// An install from before per-connection credentials: a backend and one
	// shared per-type row, nothing under the backend's own key.
	legacy, err := json.Marshal(runtimestate.ProviderConfig{Type: "mistral", APIKeyEnv: "LEGACY_MISTRAL_KEY"})
	require.NoError(t, err)
	require.NoError(t, f.store.SetKV(f.ctx, runtimestate.ProviderKeyPrefix+"mistral", legacy))
	require.NoError(t, f.store.CreateBackend(f.ctx, &runtimetypes.Backend{ID: "legacy-id", Name: "mistral", Type: "mistral", BaseURL: "https://api.mistral.ai/v1"}))

	conns := f.list()
	require.Len(t, conns, 1)
	require.Equal(t, "env", conns[0].SecretSource)
	require.Equal(t, "LEGACY_MISTRAL_KEY", conns[0].APIKeyEnv)
	require.True(t, conns[0].SecretConfigured)
	require.True(t, conns[0].SecretPresent)
	require.Equal(t, providerservice.ConfigConfigured, conns[0].ConfigState)

	// Writing the connection's own credential takes precedence from then on.
	updated := f.connection("PATCH", "/providers/connections/legacy-id", map[string]any{"apiKey": "sk-own"}, http.StatusOK)
	require.Equal(t, "literal", updated.SecretSource)
	var own runtimestate.ProviderConfig
	require.NoError(t, f.store.GetKV(f.ctx, runtimestate.BackendCredentialKey("mistral", "legacy-id"), &own))
	require.Equal(t, "sk-own", own.APIKey)
	var shared runtimestate.ProviderConfig
	require.NoError(t, f.store.GetKV(f.ctx, runtimestate.ProviderKeyPrefix+"mistral", &shared))
	require.Equal(t, "LEGACY_MISTRAL_KEY", shared.APIKeyEnv, "the legacy row is left for other consumers")
}

func TestUnit_ProviderAPI_PerTypeEndpointsStillDriveTheConnectionNamedAfterTheType(t *testing.T) {
	f := newFixture(t, false)

	status, raw := f.do("POST", "/providers/openai/configure", map[string]any{"apiKey": "sk-legacy", "upsert": true})
	require.Equal(t, http.StatusOK, status, string(raw))

	conns := f.list()
	require.Len(t, conns, 1)
	require.Equal(t, "openai", conns[0].Name)
	require.Equal(t, "literal", conns[0].SecretSource)

	status, raw = f.do("GET", "/providers/openai/status", nil)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, string(raw), conns[0].ID)

	next := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-other"}, http.StatusCreated)
	require.Equal(t, "openai-2", next.Name, "the configured connection keeps the plain name")

	status, raw = f.do("POST", "/providers/openai/configure", map[string]any{"apiKey": "sk-rotated", "upsert": true})
	require.Equal(t, http.StatusOK, status, string(raw))
	backend, err := f.store.GetBackendByName(f.ctx, "openai")
	require.NoError(t, err)
	require.Equal(t, conns[0].ID, backend.ID, "configure updates the named connection instead of adding another")
	got, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, *backend)
	require.NoError(t, err)
	require.Equal(t, "sk-rotated", got)
	other, err := f.store.GetBackendByName(f.ctx, "openai-2")
	require.NoError(t, err)
	gotOther, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, *other)
	require.NoError(t, err)
	require.Equal(t, "sk-other", gotOther)
}

func TestUnit_ProviderAPI_PatchConnection(t *testing.T) {
	f := newFixture(t, false)
	a := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-a"}, http.StatusCreated)
	b := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-b"}, http.StatusCreated)

	renamed := f.connection("PATCH", "/providers/connections/"+a.ID, map[string]any{"name": "personal", "baseUrl": "https://proxy.example/v1"}, http.StatusOK)
	require.Equal(t, "personal", renamed.Name)
	require.Equal(t, "https://proxy.example/v1", renamed.BaseURL)
	require.True(t, renamed.SecretConfigured, "untouched fields stay as they were")

	status, raw := f.do("PATCH", "/providers/connections/"+b.ID, map[string]any{"name": "personal"})
	require.Equal(t, http.StatusConflict, status, string(raw))

	switched := f.connection("PATCH", "/providers/connections/"+a.ID, map[string]any{"apiKeyEnv": "PATCH_KEY_ENV"}, http.StatusOK)
	require.Equal(t, "env", switched.SecretSource)
	var rec runtimestate.ProviderConfig
	require.NoError(t, f.store.GetKV(f.ctx, runtimestate.BackendCredentialKey("openai", a.ID), &rec))
	require.Empty(t, rec.APIKey, "switching to an env reference drops the literal key")
	require.Equal(t, "PATCH_KEY_ENV", rec.APIKeyEnv)

	cleared := f.connection("PATCH", "/providers/connections/"+a.ID, map[string]any{"clearSecret": true}, http.StatusOK)
	require.False(t, cleared.SecretConfigured)
	require.Equal(t, providerservice.ConfigMissing, cleared.ConfigState)
	require.Equal(t, "misconfigured", cleared.ErrorCode)
	err := f.store.GetKV(f.ctx, runtimestate.BackendCredentialKey("openai", a.ID), &rec)
	require.ErrorIs(t, err, libdb.ErrNotFound)

	for name, body := range map[string]any{
		"key and env":        map[string]any{"apiKey": "x", "apiKeyEnv": "Y"},
		"clear with new key": map[string]any{"clearSecret": true, "apiKey": "x"},
		"empty name":         map[string]any{"name": " "},
	} {
		status, raw := f.do("PATCH", "/providers/connections/"+a.ID, body)
		require.Equal(t, http.StatusUnprocessableEntity, status, name+": "+string(raw))
	}

	status, _ = f.do("PATCH", "/providers/connections/missing", map[string]any{"name": "x"})
	require.Equal(t, http.StatusNotFound, status)
}

func TestUnit_ProviderAPI_DeleteConnectionRemovesBackendAndCredential(t *testing.T) {
	f := newFixture(t, false)
	a := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-a"}, http.StatusCreated)
	b := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "sk-b"}, http.StatusCreated)

	status, raw := f.do("DELETE", "/providers/connections/"+a.ID, nil)
	require.Equal(t, http.StatusOK, status, string(raw))

	_, err := f.store.GetBackend(f.ctx, a.ID)
	require.ErrorIs(t, err, libdb.ErrNotFound)
	var rec runtimestate.ProviderConfig
	require.ErrorIs(t, f.store.GetKV(f.ctx, runtimestate.BackendCredentialKey("openai", a.ID), &rec), libdb.ErrNotFound)
	require.NoError(t, f.store.GetKV(f.ctx, runtimestate.BackendCredentialKey("openai", b.ID), &rec), "the sibling connection keeps its credential")

	status, _ = f.do("DELETE", "/providers/connections/"+a.ID, nil)
	require.Equal(t, http.StatusNotFound, status)

	// The other DELETE routes sharing the same path shape still work.
	require.NoError(t, f.store.SetKV(f.ctx, runtimestate.OpenaiKey, json.RawMessage(`{"Type":"openai","APIKey":"x"}`)))
	status, _ = f.do("DELETE", "/providers/openai/config", nil)
	require.Equal(t, http.StatusOK, status)
	status, _ = f.do("DELETE", "/providers/openai/bogus", nil)
	require.Equal(t, http.StatusNotFound, status)
}

func TestUnit_ProviderAPI_CodexConnectionAuthAndLoginCancel(t *testing.T) {
	f := newFixture(t, false)
	conn := f.connection("POST", "/providers/openai-codex/connections", nil, http.StatusCreated)
	require.Equal(t, providerservice.AuthLoginRequired, conn.AuthState)
	require.Equal(t, "login_required", conn.ErrorCode)
	require.Equal(t, providerservice.ConfigConfigured, conn.ConfigState)

	var login providerservice.LoginStatus
	status, raw := f.do("POST", "/providers/connections/"+conn.ID+"/login/cancel", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	require.NoError(t, json.Unmarshal(raw, &login))
	require.Equal(t, "login_required", login.State, "cancelling with nothing in flight is a no-op")
	require.Equal(t, conn.ID, login.ConnectionID)

	// Establish a credential the way a finished sign-in leaves it.
	token, err := json.Marshal(map[string]any{
		"token":      map[string]any{"access_token": "at", "refresh_token": "rt", "expiry": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		"account":    "acct",
		"generation": "g1",
	})
	require.NoError(t, err)
	require.NoError(t, f.store.SetKV(f.ctx, "model-oauth:"+conn.ID, token))

	for _, path := range []string{"/providers/connections/" + conn.ID + "/login/cancel", "/providers/openai-codex/login/cancel"} {
		status, raw = f.do("POST", path, nil)
		require.Equal(t, http.StatusOK, status, string(raw))
		require.NoError(t, json.Unmarshal(raw, &login))
		require.Equal(t, "authenticated", login.State, path+": cancel must not sign an established session out")
	}
	require.Equal(t, providerservice.AuthAuthenticated, f.list()[0].AuthState)

	status, raw = f.do("GET", "/providers/connections/"+conn.ID+"/login", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	require.Contains(t, string(raw), `"authenticated"`)

	// Logout is the call that does remove the credential.
	status, raw = f.do("DELETE", "/providers/connections/"+conn.ID+"/login", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	require.Equal(t, providerservice.AuthLoginRequired, f.list()[0].AuthState)

	// Deleting the connection removes its OAuth record too.
	require.NoError(t, f.store.SetKV(f.ctx, "model-oauth:"+conn.ID, token))
	status, _ = f.do("DELETE", "/providers/connections/"+conn.ID, nil)
	require.Equal(t, http.StatusOK, status)
	var leftover json.RawMessage
	require.ErrorIs(t, f.store.GetKV(f.ctx, "model-oauth:"+conn.ID, &leftover), libdb.ErrNotFound)

	// Key-based providers have no sign-in to cancel.
	keyed := f.connection("POST", "/providers/openai/connections", map[string]any{"apiKey": "k"}, http.StatusCreated)
	status, _ = f.do("POST", "/providers/connections/"+keyed.ID+"/login/cancel", nil)
	require.Equal(t, http.StatusUnprocessableEntity, status)
}

func TestUnit_ProviderAPI_ListIncludesNonProviderBackends(t *testing.T) {
	f := newFixture(t, false)
	require.NoError(t, f.store.CreateBackend(f.ctx, &runtimetypes.Backend{ID: "raw", Name: "raw-modeld", Type: "modeld", BaseURL: "http://127.0.0.1:1"}))
	conns := f.list()
	require.Len(t, conns, 1)
	require.Equal(t, "modeld", conns[0].ProviderType)
	require.Equal(t, providerservice.ConfigConfigured, conns[0].ConfigState)
	require.Equal(t, providerservice.AuthNotRequired, conns[0].AuthState)
}

func openAIModelsServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-a","object":"model"},{"id":"gpt-b","object":"model"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestIntegration_ProviderAPI_CheckConnectionReportsHealthAndErrorCodes(t *testing.T) {
	f := newFixture(t, true)

	ok := openAIModelsServer(t, http.StatusOK)
	denied := openAIModelsServer(t, http.StatusUnauthorized)
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()

	good := f.connection("POST", "/providers/openai/connections", map[string]any{"name": "good", "apiKey": "k", "baseUrl": ok.URL}, http.StatusCreated)
	bad := f.connection("POST", "/providers/openai/connections", map[string]any{"name": "bad", "apiKey": "k", "baseUrl": denied.URL}, http.StatusCreated)
	away := f.connection("POST", "/providers/openai/connections", map[string]any{"name": "away", "apiKey": "k", "baseUrl": goneURL}, http.StatusCreated)

	got := f.connection("POST", "/providers/connections/"+good.ID+"/check", nil, http.StatusOK)
	require.Equal(t, providerservice.HealthAvailable, got.Health, got.Error)
	require.Equal(t, 2, got.ModelCount)
	require.Empty(t, got.ErrorCode)

	got = f.connection("POST", "/providers/connections/"+bad.ID+"/check", nil, http.StatusOK)
	require.Equal(t, providerservice.HealthUnavailable, got.Health)
	require.Equal(t, "unauthorized", got.ErrorCode, got.Error)
	require.NotEmpty(t, got.Error)

	got = f.connection("POST", "/providers/connections/"+away.ID+"/check", nil, http.StatusOK)
	require.Equal(t, providerservice.HealthUnavailable, got.Health)
	require.Equal(t, "unreachable", got.ErrorCode, got.Error)

	// A key that is not set in the process is reported without any network call.
	unset := f.connection("POST", "/providers/openai/connections", map[string]any{"name": "unset", "apiKeyEnv": "CONNECTION_TEST_UNSET_KEY", "baseUrl": ok.URL}, http.StatusCreated)
	got = f.connection("POST", "/providers/connections/"+unset.ID+"/check", nil, http.StatusOK)
	require.Equal(t, providerservice.HealthUnavailable, got.Health)
	require.Equal(t, "misconfigured", got.ErrorCode)

	byID := map[string]providerservice.Connection{}
	for _, c := range f.list() {
		byID[c.ID] = c
	}
	require.Equal(t, providerservice.HealthAvailable, byID[good.ID].Health)
	require.Equal(t, "unauthorized", byID[bad.ID].ErrorCode)

	status, _ := f.do("POST", "/providers/connections/missing/check", nil)
	require.Equal(t, http.StatusNotFound, status)

}
