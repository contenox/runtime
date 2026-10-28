package providerservice_test

import (
	"context"
	"fmt"
	"testing"

	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/providerservice"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

func testSQLiteDB(t *testing.T) libdb.DBManager {
	t.Helper()
	db, err := libdb.NewSQLiteDBManager(context.Background(), t.TempDir()+"/test.db", runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func TestUnit_ProviderService_ConfiguresOpenAIWithEnvReference(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")
	t.Setenv("OPENAI_API_KEY", "sk-env")

	status, err := svc.Configure(context.Background(), "openai", providerservice.ConfigureProviderRequest{
		APIKeyEnv:    "OPENAI_API_KEY",
		DefaultModel: "gpt-5-mini",
		Upsert:       true,
		SetDefault:   true,
	})
	require.NoError(t, err)
	require.True(t, status.Configured)
	require.Equal(t, "env", status.SecretSource)
	require.True(t, status.SecretPresent)
	require.Equal(t, "OPENAI_API_KEY", status.APIKeyEnv)
	require.NotContains(t, fmt.Sprintf("%+v", status), "sk-env")

	store := runtimetypes.New(db.WithoutTransaction())
	var cfg runtimestate.ProviderConfig
	require.NoError(t, store.GetKV(context.Background(), runtimestate.OpenaiKey, &cfg))
	require.Empty(t, cfg.APIKey)
	require.Equal(t, "OPENAI_API_KEY", cfg.APIKeyEnv)

	backend, err := store.GetBackendByName(context.Background(), "openai")
	require.NoError(t, err)
	require.Equal(t, "https://api.openai.com/v1", backend.BaseURL)
	require.Equal(t, "openai", clikv.Read(context.Background(), store, "default-provider"))
	require.Equal(t, "gpt-5-mini", clikv.Read(context.Background(), store, "default-model"))
	require.Equal(t, "chain-oracle-default.json", clikv.Read(context.Background(), store, "default-oracle-chain"))
	require.Equal(t, "hitl-policy-oracle.json", clikv.Read(context.Background(), store, "default-oracle-policy"))
}

func TestUnit_ProviderService_RequiresSecretForOpenAI(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	_, err := svc.Configure(context.Background(), "openai", providerservice.ConfigureProviderRequest{Upsert: true})
	require.Error(t, err)
	require.ErrorIs(t, err, providerservice.ErrInvalidProvider)
}

func TestUnit_ProviderService_ConfiguresLocalOllamaWithoutSecret(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	status, err := svc.Configure(context.Background(), "ollama", providerservice.ConfigureProviderRequest{
		Upsert:     true,
		SetDefault: true,
	})
	require.NoError(t, err)
	require.True(t, status.Configured)
	require.Equal(t, "none", status.SecretSource)
	require.Equal(t, "http://127.0.0.1:11434", status.BaseURL)
}

func TestUnit_ProviderService_ListsSupportedProviderCapabilities(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	providers, err := svc.ListSupportedProviders(context.Background())
	require.NoError(t, err)

	byProvider := map[string]providerservice.ProviderCapability{}
	for _, provider := range providers {
		byProvider[provider.Provider] = provider
	}
	require.Contains(t, byProvider, "openai")
	require.Contains(t, byProvider, "anthropic")
	require.Contains(t, byProvider, "mistral")
	require.Contains(t, byProvider, "gemini")
	require.Contains(t, byProvider, "vertex-google")
	require.NotContains(t, byProvider, "vertex-anthropic")
	require.NotContains(t, byProvider, "vertex-meta")
	require.NotContains(t, byProvider, "vertex-mistralai")
	require.True(t, byProvider["openai"].RequiresSecretConfig)
	require.True(t, byProvider["vertex-google"].RequiresBaseURL)
}

func TestUnit_ProviderService_ConfigureWritesTheConnectionCredential(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	status, err := svc.Configure(context.Background(), "openai", providerservice.ConfigureProviderRequest{
		APIKey: "sk-test",
		Upsert: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, status.BackendID)

	store := runtimetypes.New(db.WithoutTransaction())
	var record runtimestate.ProviderConfig
	require.NoError(t, store.GetKV(context.Background(),
		runtimestate.BackendCredentialKey("openai", status.BackendID), &record))
	require.Equal(t, runtimestate.CredentialStored, record.CredentialSource)
	require.Equal(t, "sk-test", record.APIKey)
}

func TestUnit_ProviderService_SupportedProvidersIncludeDeepSeekAndCodex(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	caps, err := svc.ListSupportedProviders(context.Background())
	require.NoError(t, err)
	byType := map[string]providerservice.ProviderCapability{}
	for _, capability := range caps {
		byType[capability.Provider] = capability
	}

	deepseek, ok := byType["deepseek"]
	require.True(t, ok, "deepseek must be offered")
	require.Equal(t, "https://api.deepseek.com", deepseek.DefaultBaseURL)
	require.True(t, deepseek.RequiresSecretConfig)
	require.Equal(t, "DEEPSEEK_API_KEY", deepseek.RecommendedAPIKeyEnv)

	codex, ok := byType["openai-codex"]
	require.True(t, ok, "the ChatGPT subscription must be offered")
	require.True(t, codex.RequiresLogin)
	require.False(t, codex.RequiresSecretConfig)
	require.Equal(t, "https://chatgpt.com/backend-api/codex", codex.DefaultBaseURL)
}

func TestUnit_ProviderService_ConfiguresCodexWithFixedEndpointAndLoginState(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	status, err := svc.Configure(context.Background(), "openai-codex", providerservice.ConfigureProviderRequest{
		Upsert:     true,
		SetDefault: false,
	})
	require.NoError(t, err)
	require.True(t, status.Configured)
	require.Equal(t, "https://chatgpt.com/backend-api/codex", status.BaseURL)
	require.Equal(t, "login_required", status.AuthState)
	require.True(t, status.LoginRequired)
	require.False(t, status.SecretConfigured)

	backend, err := runtimetypes.New(db.WithoutTransaction()).GetBackendByName(context.Background(), "openai-codex")
	require.NoError(t, err)
	require.Equal(t, "https://chatgpt.com/backend-api/codex", backend.BaseURL)
}

func TestUnit_ProviderService_CodexRefusesAPIKey(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	_, err := svc.Configure(context.Background(), "openai-codex", providerservice.ConfigureProviderRequest{
		APIKey: "sk-not-used",
		Upsert: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "device code")
}

func TestUnit_ProviderService_LoginStatusReportsNotConfigured(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	status, err := svc.LoginStatus(context.Background(), "openai-codex")
	require.NoError(t, err)
	require.Equal(t, "not_configured", status.State)
}

func TestUnit_ProviderService_StartLoginRefusesAPIKeyProviders(t *testing.T) {
	db := testSQLiteDB(t)
	svc := providerservice.New(db, "workspace")

	_, err := svc.StartLogin(context.Background(), "openai")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not use a subscription sign-in")
}
