package backendservice_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestUnit_Connection_CredentialFailureRollsBackEndpoint(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "connections.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	service := backendservice.New(db)
	request := backendservice.SaveConnectionRequest{Name: "account", Type: "openai", Endpoint: "http://original.invalid", CredentialChange: backendservice.ReplaceCredentials, APIKey: "original-test-key"}
	backend, err := service.SaveConnection(ctx, request)
	require.NoError(t, err)
	_, err = db.WithoutTransaction().ExecContext(ctx, `CREATE TRIGGER refuse_credential BEFORE UPDATE ON kv BEGIN SELECT RAISE(ABORT, 'credential write failed'); END`)
	require.NoError(t, err)
	request.ID, request.Endpoint, request.APIKey = backend.ID, "http://replacement.invalid", "replacement-test-key"
	_, err = service.SaveConnection(ctx, request)
	require.ErrorContains(t, err, "credential write failed")
	saved, err := service.Get(ctx, backend.ID)
	require.NoError(t, err)
	require.Equal(t, backend.BaseURL, saved.BaseURL)
	require.Equal(t, backend.Revision, saved.Revision)
	var credential runtimestate.ProviderConfig
	require.NoError(t, runtimetypes.New(db.WithoutTransaction()).GetKV(ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID), &credential))
	require.Equal(t, "original-test-key", credential.APIKey)
	request.ID, request.Name = "", "new-account"
	_, err = db.WithoutTransaction().ExecContext(ctx, `CREATE TRIGGER refuse_new_credential BEFORE INSERT ON kv BEGIN SELECT RAISE(ABORT, 'credential write failed'); END`)
	require.NoError(t, err)
	_, err = service.SaveConnection(ctx, request)
	require.ErrorContains(t, err, "credential write failed")
	_, err = runtimetypes.New(db.WithoutTransaction()).GetBackendByName(ctx, request.Name)
	require.ErrorIs(t, err, libdbexec.ErrNotFound)
}
