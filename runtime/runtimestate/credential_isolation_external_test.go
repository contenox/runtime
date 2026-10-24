package runtimestate_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	libbus "github.com/contenox/runtime/libbus"
	libdb "github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libkvstore"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/stretchr/testify/require"
)

type recordedRequest struct {
	path          string
	authorization string
}

type recordingProvider struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func newRecordingProvider(t *testing.T, model string) *recordingProvider {
	t.Helper()
	p := &recordingProvider{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.requests = append(p.requests, recordedRequest{path: r.URL.Path, authorization: r.Header.Get("Authorization")})
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/health"):
			_, _ = w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/api/tags"):
			_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{{"name": model, "model": model}}})
		case strings.HasSuffix(r.URL.Path, "/api/show"):
			_, _ = w.Write([]byte(`{"capabilities":["completion"],"model_info":{"llama.context_length":8192}}`))
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"` + model + `","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		case strings.HasSuffix(r.URL.Path, "/models"):
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []map[string]any{{"id": model, "object": "model"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *recordingProvider) recorded() []recordedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedRequest(nil), p.requests...)
}

func (p *recordingProvider) authorizations() []string {
	var out []string
	for _, request := range p.recorded() {
		out = append(out, request.authorization)
	}
	return out
}

type failureTracker struct {
	mu       sync.Mutex
	failures []trackedFailure
}

type trackedFailure struct {
	operation string
	subject   string
	err       error
}

func (f *failureTracker) Start(_ context.Context, operation, subject string, _ ...any) (func(error), func(string, any), func()) {
	return func(err error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.failures = append(f.failures, trackedFailure{operation: operation, subject: subject, err: err})
		},
		func(string, any) {},
		func() {}
}

func (f *failureTracker) recorded() []trackedFailure {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]trackedFailure(nil), f.failures...)
}

type faultyKV struct {
	libkvstore.KVManager
	getErr error
	setErr error
}

func (f faultyKV) Executor(ctx context.Context) (libkvstore.KVExecutor, error) {
	exec, err := f.KVManager.Executor(ctx)
	if err != nil {
		return nil, err
	}
	return faultyExecutor{KVExecutor: exec, getErr: f.getErr, setErr: f.setErr}, nil
}

type faultyExecutor struct {
	libkvstore.KVExecutor
	getErr error
	setErr error
}

func (e faultyExecutor) Get(ctx context.Context, key libkvstore.Key) (json.RawMessage, error) {
	if e.getErr != nil {
		return nil, e.getErr
	}
	return e.KVExecutor.Get(ctx, key)
}

func (e faultyExecutor) SetWithTTL(ctx context.Context, key libkvstore.Key, value json.RawMessage, ttl time.Duration) error {
	if e.setErr != nil {
		return e.setErr
	}
	return e.KVExecutor.SetWithTTL(ctx, key, value, ttl)
}

type credentialFixture struct {
	ctx   context.Context
	db    libdb.DBManager
	store runtimetypes.Store
	kv    libkvstore.KVManager
}

func newCredentialFixture(t *testing.T) credentialFixture {
	t.Helper()
	ctx := context.Background()
	db, err := libdb.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "runtime.db"), runtimetypes.SchemaSQLite+libkvstore.SQLiteSchema)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return credentialFixture{ctx: ctx, db: db, store: runtimetypes.New(db.WithoutTransaction()), kv: libkvstore.NewSQLiteManager(db)}
}

func (f credentialFixture) addConnection(t *testing.T, backend *runtimetypes.Backend, record *runtimestate.ProviderConfig) {
	t.Helper()
	require.NoError(t, f.store.CreateBackend(f.ctx, backend))
	if record == nil {
		return
	}
	raw, err := json.Marshal(*record)
	require.NoError(t, err)
	require.NoError(t, f.store.SetKV(f.ctx, runtimestate.BackendCredentialKey(backend.Type, backend.ID), raw))
}

func (f credentialFixture) addLegacySharedRow(t *testing.T, adapterType, key string) {
	t.Helper()
	raw, err := json.Marshal(runtimestate.ProviderConfig{Type: adapterType, APIKey: key})
	require.NoError(t, err)
	require.NoError(t, f.store.SetKV(f.ctx, runtimestate.ProviderKeyPrefix+adapterType, raw))
}

func (f credentialFixture) newState(t *testing.T, options ...runtimestate.Option) *runtimestate.State {
	t.Helper()
	bus := libbus.NewSQLite(f.db.WithoutTransaction())
	t.Cleanup(func() { _ = bus.Close() })
	options = append([]runtimestate.Option{runtimestate.WithKVStore(f.kv), runtimestate.WithAutoDiscoverModels()}, options...)
	state, err := runtimestate.New(f.ctx, f.db, bus, options...)
	require.NoError(t, err)
	return state
}

func (f credentialFixture) cycle(t *testing.T, options ...runtimestate.Option) *runtimestate.State {
	t.Helper()
	state := f.newState(t, options...)
	require.NoError(t, state.RunBackendCycle(f.ctx))
	return state
}

func (f credentialFixture) kvStoreDump(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.db.WithoutTransaction().QueryContext(f.ctx, `SELECT key, value FROM kv_store`)
	require.NoError(t, err)
	defer rows.Close()
	dump := map[string]string{}
	for rows.Next() {
		var key, value string
		require.NoError(t, rows.Scan(&key, &value))
		dump[key] = value
	}
	require.NoError(t, rows.Err())
	return dump
}

func TestUnit_Discovery_ModelCacheHoldsNoCredential(t *testing.T) {
	const secret = "sk-discovery-secret"
	const variable = "CONTENOX_TEST_DISCOVERY_KEY"
	f := newCredentialFixture(t)
	provider := newRecordingProvider(t, "served-model")
	backend := &runtimetypes.Backend{ID: "conn-a", Name: "a", Type: "openai", BaseURL: provider.URL + "/v1"}
	f.addConnection(t, backend, &runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable})
	t.Setenv(variable, secret)

	state := f.cycle(t)

	require.Empty(t, state.Get(f.ctx)[backend.ID].Error)
	require.Equal(t, []recordedRequest{{path: "/v1/models", authorization: "Bearer " + secret}}, provider.recorded())
	dump := f.kvStoreDump(t)
	cached, ok := dump["prov:"+backend.ID]
	require.True(t, ok, "discovery caches the observed model list")
	require.Contains(t, cached, "served-model")
	for key, value := range dump {
		require.NotContains(t, value, secret, "kv_store row %s holds the credential", key)
		require.NotContains(t, value, "api_key", "kv_store row %s carries a credential field", key)
	}

	require.NoError(t, state.RunBackendCycle(f.ctx))
	require.Len(t, provider.recorded(), 1, "the cached model list is still served to the credential it was fetched with")
}

func TestUnit_Discovery_CacheDoesNotServeAnotherCredential(t *testing.T) {
	const variable = "CONTENOX_TEST_ROTATING_KEY"
	f := newCredentialFixture(t)
	provider := newRecordingProvider(t, "served-model")
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-a", Name: "a", Type: "openai", BaseURL: provider.URL + "/v1"},
		&runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable})

	t.Setenv(variable, "first-key")
	state := f.cycle(t)
	t.Setenv(variable, "second-key")
	require.NoError(t, state.RunBackendCycle(f.ctx))

	require.Equal(t, []string{"Bearer first-key", "Bearer second-key"}, provider.authorizations(),
		"a model list fetched with one credential is not served for another, so the variable is read again when a request is made")
}

func TestUnit_Discovery_DropsCacheRowCarryingCredential(t *testing.T) {
	const secret = "sk-legacy-secret"
	f := newCredentialFixture(t)
	provider := newRecordingProvider(t, "fresh-model")
	backend := &runtimetypes.Backend{ID: "conn-legacy", Name: "legacy", Type: "openai", BaseURL: provider.URL + "/v1"}
	f.addConnection(t, backend, &runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialStored, APIKey: secret})
	exec, err := f.kv.Executor(f.ctx)
	require.NoError(t, err)
	legacy, err := json.Marshal(map[string]any{"models": []map[string]any{{"Name": "stale-model"}}, "api_key": secret})
	require.NoError(t, err)
	require.NoError(t, exec.Set(f.ctx, "prov:"+backend.ID, legacy))

	state := f.cycle(t)

	require.Len(t, provider.recorded(), 1, "a cache row carrying a credential is never served")
	require.Equal(t, "fresh-model", state.Get(f.ctx)[backend.ID].PulledModels[0].Model)
	cached := f.kvStoreDump(t)["prov:"+backend.ID]
	require.NotContains(t, cached, secret)
	require.NotContains(t, cached, "api_key")
	require.NotContains(t, cached, "stale-model")
}

func TestUnit_Discovery_ReportsACacheFailureInsteadOfDroppingIt(t *testing.T) {
	cases := []struct {
		name      string
		faulty    func(libkvstore.KVManager, error) faultyKV
		operation string
	}{
		{"write", func(kv libkvstore.KVManager, err error) faultyKV { return faultyKV{KVManager: kv, setErr: err} }, "write"},
		{"read", func(kv libkvstore.KVManager, err error) faultyKV { return faultyKV{KVManager: kv, getErr: err} }, "read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			injected := errors.New("the cache store refused the " + tc.name)
			f := newCredentialFixture(t)
			provider := newRecordingProvider(t, "served-model")
			backend := &runtimetypes.Backend{ID: "conn-a", Name: "a", Type: "openai", BaseURL: provider.URL + "/v1"}
			f.addConnection(t, backend, &runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialStored, APIKey: "sk-test"})
			tracker := &failureTracker{}

			state := f.cycle(t, runtimestate.WithKVStore(tc.faulty(f.kv, injected)), runtimestate.WithTracker(tracker))

			require.Empty(t, state.Get(f.ctx)[backend.ID].Error, "a cache failure does not fail discovery")
			require.Len(t, state.Get(f.ctx)[backend.ID].PulledModels, 1)
			failures := tracker.recorded()
			require.Len(t, failures, 1, "the failed cache %s is reported", tc.name)
			require.Equal(t, tc.operation, failures[0].operation)
			require.Equal(t, "model_cache", failures[0].subject)
			require.ErrorIs(t, failures[0].err, injected)
		})
	}
}

func TestUnit_Credential_ASharedRowIsAdoptedOnceByItsTypeAndNeverBorrowedAfter(t *testing.T) {
	const secret = "sk-another-connections-key"
	f := newCredentialFixture(t)
	openaiKeyless := newRecordingProvider(t, "gpt-served")
	vllmKeyless := newRecordingProvider(t, "vllm-served")
	ollamaWithoutRecord := newRecordingProvider(t, "ollama-served")
	ollamaAddedLater := newRecordingProvider(t, "ollama-served")
	f.addLegacySharedRow(t, "openai", secret)
	f.addLegacySharedRow(t, "ollama", secret)
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-openai", Name: "b", Type: "openai", BaseURL: openaiKeyless.URL + "/v1"},
		&runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialNone})
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-vllm", Name: "v", Type: "vllm", BaseURL: vllmKeyless.URL},
		&runtimestate.ProviderConfig{Type: "vllm", CredentialSource: runtimestate.CredentialNone})
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-ollama", Name: "o", Type: "ollama", BaseURL: ollamaWithoutRecord.URL}, nil)

	state := f.newState(t)
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-ollama-later", Name: "o2", Type: "ollama", BaseURL: ollamaAddedLater.URL}, nil)
	require.NoError(t, state.RunBackendCycle(f.ctx))

	snapshot := state.Get(f.ctx)
	for _, id := range []string{"conn-openai", "conn-vllm", "conn-ollama", "conn-ollama-later"} {
		require.Empty(t, snapshot[id].Error, "%s", id)
	}
	for name, provider := range map[string]*recordingProvider{"openai with its own record": openaiKeyless, "vllm": vllmKeyless, "ollama saved after the adoption": ollamaAddedLater} {
		require.NotEmpty(t, provider.recorded(), name)
		for _, request := range provider.recorded() {
			require.Empty(t, request.authorization, "%s request %s carried a credential that is not this connection's", name, request.path)
		}
	}
	require.NotEmpty(t, ollamaWithoutRecord.recorded())
	for _, request := range ollamaWithoutRecord.recorded() {
		require.Equal(t, "Bearer "+secret, request.authorization, "the record-less ollama connection adopted the shared row it resolved to before the upgrade")
	}
	var adopted runtimestate.ProviderConfig
	require.NoError(t, f.store.GetKV(f.ctx, runtimestate.BackendCredentialKey("ollama", "conn-ollama"), &adopted))
	require.Equal(t, runtimestate.ProviderConfig{Type: "ollama", CredentialSource: runtimestate.CredentialStored, APIKey: secret}, adopted)
	dump := f.kvStoreDump(t)
	for _, adapterType := range []string{"openai", "ollama"} {
		_, shared := dump[runtimestate.ProviderKeyPrefix+adapterType]
		require.False(t, shared, "the shared %s row is gone after the adoption", adapterType)
	}
}

func TestUnit_ExecutionProviders_ChatRequestsSendOnlyTheConnectionsOwnCredential(t *testing.T) {
	f := newCredentialFixture(t)
	keyed := newRecordingProvider(t, "gpt-served")
	keyless := newRecordingProvider(t, "gpt-served")
	vllm := newRecordingProvider(t, "vllm-served")
	f.addLegacySharedRow(t, "openai", "sk-legacy-shared")
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-a", Name: "a", Type: "openai", BaseURL: keyed.URL + "/v1"},
		&runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialStored, APIKey: "sk-a"})
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-b", Name: "b", Type: "openai", BaseURL: keyless.URL + "/v1"},
		&runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialNone})
	f.addConnection(t, &runtimetypes.Backend{ID: "conn-v", Name: "v", Type: "vllm", BaseURL: vllm.URL}, nil)
	state := f.cycle(t)
	executionProviders := state.ExecutionProviders(libtracker.NoopTracker{})

	for id, provider := range map[string]*recordingProvider{"conn-a": keyed, "conn-b": keyless, "conn-v": vllm} {
		candidates, err := executionProviders(f.ctx, runtimestate.ConnectionSelector(id))
		require.NoError(t, err)
		require.NotEmpty(t, candidates, id)
		client, err := candidates[0].GetChatConnection(f.ctx, candidates[0].GetBackendIDs()[0])
		require.NoError(t, err)
		_, err = client.Chat(f.ctx, []modelrepo.Message{{Role: "user", Content: "ping"}})
		require.NoError(t, err)
		var chats []string
		for _, request := range provider.recorded() {
			if strings.HasSuffix(request.path, "/chat/completions") {
				chats = append(chats, request.authorization)
			}
		}
		require.Len(t, chats, 1, id)
		if id == "conn-a" {
			require.Equal(t, "Bearer sk-a", chats[0])
		} else {
			require.Empty(t, strings.TrimSpace(strings.TrimPrefix(chats[0], "Bearer")), "%s: a keyless connection's chat request carries no credential, least of all the shared row's", id)
		}
	}
}

func TestUnit_Credential_ConnectionWithoutARecordIsRefusedWhenTheAdapterNeedsAKey(t *testing.T) {
	f := newCredentialFixture(t)
	provider := newRecordingProvider(t, "gpt-served")
	backend := &runtimetypes.Backend{ID: "conn-openai", Name: "b", Type: "openai", BaseURL: provider.URL + "/v1"}
	f.addConnection(t, backend, nil)

	state := f.cycle(t)

	require.Equal(t, "API key not configured", state.Get(f.ctx)[backend.ID].Error)
	require.Empty(t, provider.recorded(), "no request is sent with a credential the connection does not hold")
	_, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, *backend)
	require.ErrorIs(t, err, libdb.ErrNotFound)
}

func TestUnit_Credential_EnvironmentReferenceIsReadWhenTheRequestIsMade(t *testing.T) {
	const variable = "CONTENOX_TEST_REFERENCED_KEY"
	f := newCredentialFixture(t)
	backend := runtimetypes.Backend{ID: "conn-a", Name: "a", Type: "openai", BaseURL: "http://127.0.0.1:1/v1"}
	f.addConnection(t, &backend, &runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable})

	t.Setenv(variable, "first-value")
	first, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, backend)
	require.NoError(t, err)
	t.Setenv(variable, "second-value")
	second, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, backend)
	require.NoError(t, err)

	require.Equal(t, "first-value", first)
	require.Equal(t, "second-value", second)
}

func TestUnit_Credential_UnsetVariableIsRefusedNamingItAndNeverFallsBackToALiteral(t *testing.T) {
	const variable = "CONTENOX_TEST_UNSET_REFERENCE"
	records := map[string]runtimestate.ProviderConfig{
		"a literal beside the reference":                      {Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable, APIKey: "sk-stale-literal"},
		"a record written before sources were named":          {Type: "openai", APIKeyEnv: variable, APIKey: "sk-stale-literal"},
		"the reference alone":                                 {Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable},
		"a whitespace-only value counts as unset":             {Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable},
		"a literal beside the reference on a keyless adapter": {Type: "vllm", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable, APIKey: "sk-stale-literal"},
	}
	for name, record := range records {
		t.Run(name, func(t *testing.T) {
			f := newCredentialFixture(t)
			provider := newRecordingProvider(t, "served-model")
			backend := &runtimetypes.Backend{ID: "conn-a", Name: "a", Type: record.Type, BaseURL: provider.URL + "/v1"}
			f.addConnection(t, backend, &record)
			if strings.Contains(name, "whitespace") {
				t.Setenv(variable, "  ")
			}

			state := f.cycle(t)

			observed := state.Get(f.ctx)[backend.ID]
			require.Contains(t, observed.Error, variable, "the refusal names the variable the connection reads")
			require.Empty(t, provider.recorded(), "no request is sent, and none with the stale literal")
			_, err := runtimestate.LoadProviderAPIKey(f.ctx, f.db, *backend)
			var unset *runtimestate.EnvironmentUnsetError
			require.ErrorAs(t, err, &unset)
			require.Equal(t, variable, unset.Variable)
		})
	}
}

func TestUnit_Credential_EnvironmentReferenceOutranksALiteralBesideIt(t *testing.T) {
	const variable = "CONTENOX_TEST_OUTRANKING_KEY"
	for name, record := range map[string]runtimestate.ProviderConfig{
		"explicit source":    {Type: "openai", CredentialSource: runtimestate.CredentialEnvironment, APIKeyEnv: variable, APIKey: "sk-stale-literal"},
		"no recorded source": {Type: "openai", APIKeyEnv: variable, APIKey: "sk-stale-literal"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCredentialFixture(t)
			provider := newRecordingProvider(t, "served-model")
			backend := &runtimetypes.Backend{ID: "conn-a", Name: "a", Type: "openai", BaseURL: provider.URL + "/v1"}
			f.addConnection(t, backend, &record)
			t.Setenv(variable, "sk-from-environment")

			state := f.cycle(t)

			require.Empty(t, state.Get(f.ctx)[backend.ID].Error)
			require.Equal(t, []string{"Bearer sk-from-environment"}, provider.authorizations())
		})
	}
}

func TestUnit_Credential_StoredKeyIsTheProductsCredentialPath(t *testing.T) {
	f := newCredentialFixture(t)
	provider := newRecordingProvider(t, "served-model")
	backend := &runtimetypes.Backend{ID: "conn-a", Name: "a", Type: "openai", BaseURL: provider.URL + "/v1"}
	f.addConnection(t, backend, &runtimestate.ProviderConfig{Type: "openai", CredentialSource: runtimestate.CredentialStored, APIKey: "sk-operator-stored"})

	state := f.cycle(t)

	require.Empty(t, state.Get(f.ctx)[backend.ID].Error)
	require.Equal(t, []string{"Bearer sk-operator-stored"}, provider.authorizations())
}

func TestUnit_ProviderConfig_SourceAndCredential(t *testing.T) {
	const variable = "CONTENOX_TEST_RECORD_KEY"
	t.Setenv(variable, "from-environment")
	cases := []struct {
		name       string
		record     runtimestate.ProviderConfig
		backend    string
		source     string
		credential string
		failure    string
	}{
		{"stored", runtimestate.ProviderConfig{CredentialSource: "stored", APIKey: "literal"}, "openai", "stored", "literal", ""},
		{"stored without a key", runtimestate.ProviderConfig{CredentialSource: "stored"}, "openai", "stored", "", "holds none"},
		{"environment", runtimestate.ProviderConfig{CredentialSource: "environment", APIKeyEnv: variable}, "openai", "environment", "from-environment", ""},
		{"environment naming no variable", runtimestate.ProviderConfig{CredentialSource: "environment"}, "openai", "environment", "", "no variable"},
		{"unsourced literal is stored", runtimestate.ProviderConfig{APIKey: "literal"}, "openai", "stored", "literal", ""},
		{"unsourced reference is environment", runtimestate.ProviderConfig{APIKeyEnv: variable}, "openai", "environment", "from-environment", ""},
		{"unsourced cloud adapter is ambient", runtimestate.ProviderConfig{}, "vertex-google", "ambient", "", ""},
		{"unsourced openai is none", runtimestate.ProviderConfig{}, "openai", "none", "", ""},
		{"none ignores a literal", runtimestate.ProviderConfig{CredentialSource: "none", APIKey: "literal"}, "vllm", "none", "", ""},
		{"ambient ignores a literal", runtimestate.ProviderConfig{CredentialSource: "ambient", APIKey: "literal"}, "bedrock", "ambient", "", ""},
		{"unknown source", runtimestate.ProviderConfig{CredentialSource: "import", APIKey: "literal"}, "bedrock", "import", "", "unknown credential source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.source, tc.record.Source(tc.backend))
			credential, err := tc.record.Credential(tc.backend)
			if tc.failure != "" {
				require.ErrorContains(t, err, tc.failure)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.credential, credential)
		})
	}
}
