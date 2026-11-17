package inferencesetup_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/contenox/runtime/internal/modeld/modelstore"
	"github.com/contenox/runtime/internal/modeld/owner"
	"github.com/contenox/runtime/internal/transport"
	transportgrpc "github.com/contenox/runtime/internal/transport/grpc"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/modelrepo/modeldconn"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type refusalHarness struct {
	ctx     context.Context
	db      libdbexec.DBManager
	state   *runtimestate.State
	service *inferencesetup.Service
	models  *verifyingModels
}

func newRefusalHarness(t *testing.T, catalog []map[string]any) *refusalHarness {
	t.Helper()
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "refusals.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": catalog}))
	}))
	t.Cleanup(server.Close)
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "hosted", Name: "hosted", Type: "openai", BaseURL: server.URL}))
	credential, err := json.Marshal(runtimestate.ProviderConfig{Type: "openai", APIKey: "test-key"})
	require.NoError(t, err)
	require.NoError(t, runtimetypes.New(db.WithoutTransaction()).SetKV(ctx, runtimestate.BackendCredentialKey("openai", "hosted"), credential))
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, state.RunProviderCycle(ctx, "openai"))
	models := &verifyingModels{}
	service, err := inferencesetup.New(db, state, bus, modelFactory{repo: models})
	require.NoError(t, err)
	return &refusalHarness{ctx: ctx, db: db, state: state, service: service, models: models}
}

type emptyOwnerInventory struct {
	*transport.MemoryService
	*modelstore.NodeAdmin
}

func (*emptyOwnerInventory) Inventory(context.Context) (transport.Inventory, error) {
	return transport.Inventory{Backends: []transport.BackendInventory{{Backend: "llama", State: "available", Operations: []string{"chat", "embedding"}}}}, nil
}

func (*emptyOwnerInventory) Describe(context.Context, transport.OpenSessionRequest) (transport.ModelInfo, error) {
	return transport.ModelInfo{}, errors.New("catalog must not load or plan a model")
}

func serveEmptyOwner(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CONTENOX_DATA_ROOT", root)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	ctx = modeldconn.WithDataRoot(ctx, root)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	holder, err := owner.Join(ctx, owner.Config{LeasePath: filepath.Join(root, "modeld.lease"), Endpoint: listener.Addr().String(), Backend: "shared", TTL: 30 * time.Second})
	require.NoError(t, err)
	require.True(t, holder.IsOwner())
	t.Cleanup(func() { require.NoError(t, holder.Release()) })
	service := &emptyOwnerInventory{MemoryService: transport.NewMemoryService(), NodeAdmin: modelstore.NewNodeAdmin(filepath.Join(root, "models"))}
	done := make(chan error, 1)
	go func() { done <- transportgrpc.Serve(ctx, listener, service, holder.InstanceID(), "shared") }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done); modeldconn.CloseEndpoint("catalog:" + root) })
}

func TestIntegration_Assign_RefusesAModelTheConnectionDoesNotServeBeforeFingerprinting(t *testing.T) {
	h := newRefusalHarness(t, []map[string]any{{"id": "chat-model", "capabilities": []string{"chat", "completion"}, "completion_mode": "instruct"}})
	for _, role := range []string{inferencesetup.RoleChat, inferencesetup.RoleAutocomplete, inferencesetup.RoleEmbedding} {
		_, err := h.service.Assign(h.ctx, inferencesetup.Assignment{Role: role, Provider: "openai", Model: "absent-model"}, inferencesetup.SourceUser)
		var missing *inferencesetup.ModelNotDiscoveredError
		require.ErrorAs(t, err, &missing, role)
		require.Equal(t, inferencesetup.ModelNotDiscoveredError{Connection: "openai", Model: "absent-model"}, *missing, "a hosted connection cannot download a model")
		require.NotContains(t, err.Error(), "fingerprint")
	}
}

func TestIntegration_Assign_RefusesAnAbsentOrFailedConnectionNamingIt(t *testing.T) {
	h := newRefusalHarness(t, []map[string]any{{"id": "chat-model", "capabilities": []string{"chat"}}})
	_, err := h.service.Assign(h.ctx, inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: "nonesuch", Model: "chat-model"}, inferencesetup.SourceUser)
	var unavailable *inferencesetup.ConnectionUnavailableError
	require.ErrorAs(t, err, &unavailable)
	require.Equal(t, "nonesuch", unavailable.Connection)
	require.Contains(t, unavailable.Reason, "no connection of that name")
}

func TestIntegration_Assign_RefusesAnAutocompleteModelWithoutACompletionContract(t *testing.T) {
	h := newRefusalHarness(t, []map[string]any{{"id": "undeclared", "capabilities": []string{"chat", "completion"}}})
	_, err := h.service.Assign(h.ctx, inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "undeclared"}, inferencesetup.SourceUser)
	var missing *inferencesetup.CompletionContractMissingError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, inferencesetup.CompletionContractMissingError{Connection: "openai", Model: "undeclared"}, *missing)
}

func TestIntegration_Assign_NamesACuratedLocalModelThatIsNotDownloaded(t *testing.T) {
	serveEmptyOwner(t)
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "local.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "native", Name: "Local models", Type: "modeld", BaseURL: modeldconn.LocalSentinel}))
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, state.RunBackendCycle(ctx))
	service, err := inferencesetup.New(db, state, bus, modelFactory{repo: &verifyingModels{}})
	require.NoError(t, err)
	_, err = service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "modeld", Model: "qwen2.5-coder-1.5b-fim"}, inferencesetup.SourceUser)
	var missing *inferencesetup.ModelNotDiscoveredError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, inferencesetup.ModelNotDiscoveredError{Connection: "modeld", Model: "qwen2.5-coder-1.5b-fim", Curated: true}, *missing, "a curated artifact the connection can download")
	_, err = service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: "modeld", Model: "not-a-curated-model"}, inferencesetup.SourceUser)
	require.ErrorAs(t, err, &missing)
	require.False(t, missing.Curated, "a name the catalog does not curate is not downloadable")
}

func TestIntegration_DeclareCompletionContract_MakesAnUndeclaredModelAssignable(t *testing.T) {
	h := newRefusalHarness(t, []map[string]any{{"id": "chat-model", "capabilities": []string{"chat", "completion"}}, {"id": "insert-model", "capabilities": []string{"completion"}}})
	assign := inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "chat-model"}
	_, err := h.service.Assign(h.ctx, assign, inferencesetup.SourceUser)
	require.ErrorAs(t, err, new(*inferencesetup.CompletionContractMissingError))
	require.NoError(t, h.service.DeclareCompletionContract(h.ctx, inferencesetup.CompletionDeclaration{Provider: "openai", Model: "chat-model", Mode: "instruct"}))
	_, err = h.service.Assign(h.ctx, assign, inferencesetup.SourceUser)
	require.NoError(t, err)

	require.ErrorContains(t, h.service.DeclareCompletionContract(h.ctx, inferencesetup.CompletionDeclaration{Provider: "openai", Model: "chat-model", Mode: "bogus"}), "instruct, prompt, fim")
	require.ErrorContains(t, h.service.DeclareCompletionContract(h.ctx, inferencesetup.CompletionDeclaration{Provider: "openai", Model: "chat-model", Mode: "instruct", FIMFormat: "qwen2.5-coder"}), "apply to fim alone")
}

func TestIntegration_DeclareCompletionContract_RegistersAFillInTheMiddleModel(t *testing.T) {
	h := newRefusalHarness(t, []map[string]any{{"id": "insert-model", "capabilities": []string{"completion"}}})
	declaration := inferencesetup.CompletionDeclaration{Provider: "openai", Model: "insert-model", Mode: "fim", FIMFormat: "qwen2.5-coder"}
	require.ErrorContains(t, h.service.DeclareCompletionContract(h.ctx, inferencesetup.CompletionDeclaration{Provider: "openai", Model: "insert-model", Mode: "fim", FIMFormat: "no-such-format"}), "qwen2.5-coder", "an unknown format names the declared formats")
	var needed *inferencesetup.ContextLengthRequiredError
	require.ErrorAs(t, h.service.DeclareCompletionContract(h.ctx, declaration), &needed, "the catalog reports no context length and none was stated")
	declaration.ContextLength = 8192
	require.NoError(t, h.service.DeclareCompletionContract(h.ctx, declaration))
	facts, err := runtimetypes.New(h.db.WithoutTransaction()).GetLLMProviderModelFacts(h.ctx, "hosted")
	require.NoError(t, err)
	require.Equal(t, "fim", facts["insert-model"].CompletionMode)
	require.Equal(t, "qwen2.5-coder", facts["insert-model"].FIMFormat)
	require.Equal(t, 8192, facts["insert-model"].ContextLength)
	require.Contains(t, facts["insert-model"].Capabilities, runtimetypes.CapabilityAutocomplete)
	require.Contains(t, facts["insert-model"].Capabilities, runtimetypes.CapabilityCompletion, "the observed capabilities are kept")
	_, err = h.service.Assign(h.ctx, inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "insert-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
}

func TestIntegration_DeclareCompletionContract_RefusesALocalModelNamingWhereItsContractLives(t *testing.T) {
	serveEmptyOwner(t)
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "local.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "native", Name: "Local models", Type: "modeld", BaseURL: modeldconn.LocalSentinel}))
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, state.RunBackendCycle(ctx))
	service, err := inferencesetup.New(db, state, bus, modelFactory{repo: &verifyingModels{}})
	require.NoError(t, err)
	err = service.DeclareCompletionContract(ctx, inferencesetup.CompletionDeclaration{Provider: "modeld", Model: "qwen2.5-coder-1.5b-fim", Mode: "fim", FIMFormat: "qwen2.5-coder", ContextLength: 8192})
	require.ErrorContains(t, err, "downloaded artifact")
}

func TestIntegration_DeclareCompletionContract_KeepsAVerifiedChatRoleOnTheSameModel(t *testing.T) {
	h := newRefusalHarness(t, []map[string]any{{"id": "chat-model", "capabilities": []string{"chat", "completion"}}})
	state := func(role string) string {
		roles, err := h.service.Roles(h.ctx)
		require.NoError(t, err)
		for _, status := range roles {
			if status.Role == role {
				return status.State
			}
		}
		t.Fatalf("role %s is not listed", role)
		return ""
	}
	_, err := h.service.Assign(h.ctx, inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: "openai", Model: "chat-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Equal(t, "available", state(inferencesetup.RoleChat))
	require.NoError(t, h.service.DeclareCompletionContract(h.ctx, inferencesetup.CompletionDeclaration{Provider: "openai", Model: "chat-model", Mode: "instruct"}))
	_, err = h.service.Assign(h.ctx, inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "chat-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Equal(t, "available", state(inferencesetup.RoleAutocomplete))
	require.Equal(t, "available", state(inferencesetup.RoleChat), "a completion contract changes nothing a chat turn uses")
}
