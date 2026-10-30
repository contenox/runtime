package providerservice

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/models/modelauth"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

// pendingAttempt registers a device-code sign-in that is still waiting for the
// user, the way StartLogin leaves it, without touching the network.
func pendingAttempt(t *testing.T, backendID string) (cancelled func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	loginMu.Lock()
	loginAttempts[backendID] = &loginAttempt{
		provider: ProviderTypeOpenAICodex,
		device:   deviceCode("https://example.test/device", "ABCD-1234"),
		doneCh:   make(chan struct{}),
		cancel:   cancel,
	}
	loginMu.Unlock()
	t.Cleanup(func() {
		cancel()
		loginMu.Lock()
		delete(loginAttempts, backendID)
		loginMu.Unlock()
	})
	return func() bool { return ctx.Err() != nil }
}

func attemptRegistered(backendID string) bool {
	loginMu.Lock()
	defer loginMu.Unlock()
	return loginAttempts[backendID] != nil
}

func TestUnit_CancelLogin_PendingAttemptIsAbandonedAndCredentialUntouched(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := New(db, "workspace")
	conn, err := svc.CreateConnection(ctx, ProviderTypeOpenAICodex, CreateConnectionRequest{})
	require.NoError(t, err)

	for name, cancel := range map[string]func() (*LoginStatus, error){
		"by connection": func() (*LoginStatus, error) { return svc.CancelConnectionLogin(ctx, conn.ID) },
		"by type": func() (*LoginStatus, error) {
			// the per-type wrapper addresses the connection named after the type
			return svc.CancelLogin(ctx, ProviderTypeOpenAICodex)
		},
	} {
		t.Run(name, func(t *testing.T) {
			cancelled := pendingAttempt(t, conn.ID)

			pending, err := svc.ConnectionLoginStatus(ctx, conn.ID)
			require.NoError(t, err)
			require.Equal(t, "login_pending", pending.State)
			require.Equal(t, "ABCD-1234", pending.Code)
			got, err := svc.GetConnection(ctx, conn.ID)
			require.NoError(t, err)
			require.Equal(t, AuthLoginPending, got.AuthState)

			status, err := cancel()
			require.NoError(t, err)
			require.Equal(t, "login_required", status.State, "no credential existed, so cancelling leaves the sign-in required")
			require.Empty(t, status.Code)
			require.True(t, cancelled(), "the in-flight sign-in is cancelled")
			require.False(t, attemptRegistered(conn.ID), "the attempt is forgotten")

			got, err = svc.GetConnection(ctx, conn.ID)
			require.NoError(t, err)
			require.Equal(t, AuthLoginRequired, got.AuthState)
		})
	}
}

func TestUnit_CancelLogin_DoesNotSignOutAnEstablishedSession(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	svc := New(db, "workspace")
	conn, err := svc.CreateConnection(ctx, ProviderTypeOpenAICodex, CreateConnectionRequest{})
	require.NoError(t, err)

	token, err := json.Marshal(map[string]any{
		"token":      map[string]any{"access_token": "at", "refresh_token": "rt", "expiry": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)},
		"generation": "g1",
	})
	require.NoError(t, err)
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, store.SetKV(ctx, "model-oauth:"+conn.ID, token))

	// A re-login attempt is in flight on top of a still-valid session.
	cancelled := pendingAttempt(t, conn.ID)
	status, err := svc.CancelConnectionLogin(ctx, conn.ID)
	require.NoError(t, err)
	require.Equal(t, "authenticated", status.State)
	require.True(t, cancelled())
	require.False(t, attemptRegistered(conn.ID))

	var kept json.RawMessage
	require.NoError(t, store.GetKV(ctx, "model-oauth:"+conn.ID, &kept))
	require.JSONEq(t, string(token), string(kept), "the stored credential is byte-for-byte what it was")

	// Logout, by contrast, cancels the attempt and wipes the session.
	cancelled = pendingAttempt(t, conn.ID)
	require.NoError(t, svc.LogoutConnection(ctx, conn.ID))
	require.True(t, cancelled())
	status, err = svc.ConnectionLoginStatus(ctx, conn.ID)
	require.NoError(t, err)
	require.Equal(t, "login_required", status.State)
}

func TestUnit_CancelLogin_RejectsProvidersWithoutSignIn(t *testing.T) {
	ctx := context.Background()
	svc := New(testDB(t), "workspace")
	_, err := svc.CancelLogin(ctx, ProviderTypeOpenAI)
	require.ErrorIs(t, err, ErrInvalidProvider)

	status, err := svc.CancelLogin(ctx, ProviderTypeOpenAICodex)
	require.NoError(t, err)
	require.Equal(t, "not_configured", status.State, "no connection yet, nothing to cancel")
}

func TestUnit_NextConnectionName(t *testing.T) {
	taken := map[string]struct{}{}
	require.Equal(t, "openai", nextConnectionName("openai", taken))
	taken["openai"] = struct{}{}
	require.Equal(t, "openai-2", nextConnectionName("openai", taken))
	taken["openai-3"] = struct{}{}
	require.Equal(t, "openai-2", nextConnectionName("openai", taken), "the first free number wins, gaps included")
	taken["openai-2"] = struct{}{}
	require.Equal(t, "openai-4", nextConnectionName("openai", taken))
	require.Equal(t, "anthropic", nextConnectionName("anthropic", taken))
}

func testDB(t *testing.T) libdb.DBManager {
	t.Helper()
	db, err := libdb.NewSQLiteDBManager(context.Background(), t.TempDir()+"/test.db", runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func deviceCode(url, code string) modelauth.DeviceCode {
	return modelauth.DeviceCode{URL: url, Code: code, ExpiresAt: time.Now().Add(10 * time.Minute)}
}
