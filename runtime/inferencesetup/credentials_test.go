package inferencesetup_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func TestIntegration_ProviderCredentials_ImportPreserveReplaceAndValidateWithoutDiscovery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(root, "credentials.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	service, err := inferencesetup.New(db, state, bus, modelFactory{repo: &verifyingModels{}})
	require.NoError(t, err)
	file := filepath.Join(root, "dedicated-account.json")
	credential := `{"access_key_id":"test-access-id","secret_access_key":"test-secret-key","session_token":"test-session-token"}`
	require.NoError(t, os.WriteFile(file, []byte(credential), 0600))
	request := inferencesetup.ProviderRequest{Name: "Bedrock test", Provider: "bedrock", Endpoint: "http://127.0.0.1:1", CredentialSource: "import", CredentialFile: file}
	saved, err := service.SaveProvider(ctx, request)
	require.NoError(t, err)
	require.Empty(t, saved.DiscoveryError)
	store := runtimetypes.New(db.WithoutTransaction())
	var credentials runtimestate.ProviderConfig
	require.NoError(t, store.GetKV(ctx, runtimestate.BackendCredentialKey("bedrock", saved.ID), &credentials))
	require.Equal(t, "stored", credentials.CredentialSource)
	require.JSONEq(t, credential, credentials.APIKey)
	configuration, err := service.Configuration(ctx)
	require.NoError(t, err)
	require.Equal(t, "not-observed", configuration.Providers[0].DiscoveryState)
	require.Equal(t, "unverified", configuration.Providers[0].AuthorizationState)
	raw, err := json.Marshal(configuration)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "test-secret-key")
	require.NotContains(t, string(raw), "test-session-token")
	request.ID, request.CredentialSource, request.CredentialFile = saved.ID, "preserve", ""
	request.Name = "Renamed account"
	_, err = service.SaveProvider(ctx, request)
	require.NoError(t, err)
	var preserved runtimestate.ProviderConfig
	require.NoError(t, store.GetKV(ctx, runtimestate.BackendCredentialKey("bedrock", saved.ID), &preserved))
	require.Equal(t, credentials, preserved)
	before, err := service.Configuration(ctx)
	require.NoError(t, err)
	request.CredentialSource, request.APIKey = "stored", `{"secret_access_key":"invalid-secret"}`
	_, err = service.SaveProvider(ctx, request)
	require.ErrorContains(t, err, "access_key_id")
	require.NotContains(t, err.Error(), "invalid-secret")
	after, err := service.Configuration(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	request.CredentialSource, request.APIKey = "ambient", ""
	_, err = service.SaveProvider(ctx, request)
	require.NoError(t, err)
	after, err = service.Configuration(ctx)
	require.NoError(t, err)
	require.Equal(t, "ambient", after.Providers[0].CredentialSource)
	require.NotEqual(t, before.Providers[0].Revision, after.Providers[0].Revision)
	request.CredentialSource = "none"
	_, err = service.SaveProvider(ctx, request)
	require.ErrorContains(t, err, "required credential source")
}
