package copilotauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type fakeGitHub struct {
	polls        atomic.Int32
	exchanges    atomic.Int32
	revoked      atomic.Bool
	tokenSeconds int64
	lastHeaders  atomic.Value
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/device/code", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, clientID, r.Form.Get("client_id"))
		_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "dev-1", "user_code": "ABCD-1234", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 1})
	})
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		if f.polls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "gho_test"})
	})
	mux.HandleFunc("GET /copilot_internal/v2/token", func(w http.ResponseWriter, r *http.Request) {
		f.exchanges.Add(1)
		f.lastHeaders.Store(r.Header.Clone())
		if f.revoked.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		require.Equal(t, "token gho_test", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "cop-token", "expires_at": time.Now().Unix() + f.tokenSeconds, "endpoints": map[string]string{"api": "https://api.individual.githubcopilot.com"}})
	})
	return mux
}

func setup(t *testing.T, allow bool) (*Service, *fakeGitHub, string) {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "copilot.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	store := runtimetypes.New(db.WithoutTransaction())
	backend := &runtimetypes.Backend{ID: "b1", Name: "copilot", BaseURL: BaseURL, Type: ProviderType}
	require.NoError(t, store.CreateBackend(ctx, backend))
	if allow {
		require.NoError(t, clikv.SetString(ctx, store, clikv.UnofficialSignInsKey, "true"))
	}
	fake := &fakeGitHub{tokenSeconds: 1800}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	svc := New(db)
	svc.githubURL, svc.apiURL = srv.URL, srv.URL
	return svc, fake, backend.ID
}

func TestUnit_CopilotAuth_RefusesWithoutOptIn(t *testing.T) {
	svc, fake, id := setup(t, false)
	err := svc.Login(context.Background(), id, func(modelauth.DeviceCode) error { return nil })
	require.ErrorIs(t, err, ErrNotAllowed)
	_, err = svc.Headers(context.Background(), id)
	require.ErrorIs(t, err, ErrNotAllowed)
	require.Zero(t, fake.polls.Load(), "nothing is sent to GitHub before the operator opts in")
}

func TestUnit_CopilotAuth_DeviceSignInAndHeaders(t *testing.T) {
	ctx := context.Background()
	svc, fake, id := setup(t, true)

	st, err := svc.Status(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "login_required", st.State)

	var shown modelauth.DeviceCode
	require.NoError(t, svc.Login(ctx, id, func(c modelauth.DeviceCode) error { shown = c; return nil }))
	require.Equal(t, "ABCD-1234", shown.Code)
	require.Equal(t, "https://github.com/login/device", shown.URL)
	require.EqualValues(t, 2, fake.polls.Load(), "a pending authorization is polled again")

	st, err = svc.Status(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "authenticated", st.State)

	h, err := svc.Headers(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Bearer cop-token", h.Get("Authorization"))
	require.Equal(t, integrationID, h.Get("Copilot-Integration-Id"))
	require.Equal(t, editorVersion, h.Get("Editor-Version"))
	require.EqualValues(t, 1, fake.exchanges.Load(), "a fresh Copilot token is reused")
	sent := fake.lastHeaders.Load().(http.Header)
	require.Equal(t, integrationID, sent.Get("Copilot-Integration-Id"))
}

func TestUnit_CopilotAuth_RenewsAndDropsRevokedSignIn(t *testing.T) {
	ctx := context.Background()
	svc, fake, id := setup(t, true)
	fake.tokenSeconds = 60
	require.NoError(t, svc.Login(ctx, id, func(modelauth.DeviceCode) error { return nil }))

	_, err := svc.Headers(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 2, fake.exchanges.Load(), "a Copilot token close to expiry is renewed")

	fake.revoked.Store(true)
	_, err = svc.Headers(ctx, id)
	require.ErrorIs(t, err, ErrLoginRequired)
	st, err := svc.Status(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "login_required", st.State, "a revoked GitHub token clears the sign-in")

	require.NoError(t, svc.Logout(ctx, id))
}
