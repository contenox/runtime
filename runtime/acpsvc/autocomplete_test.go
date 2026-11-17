package acpsvc

import (
	"context"
	"encoding/json"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	protocolv1 "github.com/contenox/runtime/internal/protocol/v1"
	libacp "github.com/contenox/runtime/libacp"
	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/libtracker"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/enginesvc"
	"github.com/contenox/runtime/runtime/internal/clikv"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelregistry"
	"github.com/contenox/runtime/runtime/ollamatokenizer"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

// recordingAgent captures the last PromptRequest it saw and answers with a
// fixed response/error, optionally failing the first N calls (to exercise
// the auto-routed retry).
type recordingAgent struct {
	mu      sync.Mutex
	failN   int
	calls   int
	lastReq agentservice.PromptRequest
	allReqs []agentservice.PromptRequest
	resp    *agentservice.PromptResponse
	failErr error
}

func (a *recordingAgent) Capabilities(context.Context) (*agentservice.AgentCapabilities, error) {
	return nil, nil
}
func (a *recordingAgent) SessionNew(context.Context, string) (string, error) { return "", nil }
func (a *recordingAgent) SessionList(context.Context) ([]*agentservice.SessionInfo, error) {
	return nil, nil
}
func (a *recordingAgent) SessionLoad(context.Context, string) (string, []taskengine.Message, error) {
	return "", nil, nil
}
func (a *recordingAgent) SessionResume(context.Context, string) (string, error) { return "", nil }
func (a *recordingAgent) SessionDelete(context.Context, string) error           { return nil }
func (a *recordingAgent) SessionEnsureDefault(context.Context) (string, error)  { return "", nil }

func (a *recordingAgent) Prompt(_ context.Context, req agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	a.lastReq = req
	a.allReqs = append(a.allReqs, req)
	if a.calls <= a.failN {
		return nil, a.failErr
	}
	if a.resp != nil {
		return a.resp, nil
	}
	return &agentservice.PromptResponse{Output: "completion"}, nil
}

func (a *recordingAgent) requestCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *recordingAgent) requestAt(i int) agentservice.PromptRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allReqs[i]
}

// autocompleteTestTransport builds a Transport with a configured FIM chain
// and engine, ready for handleAutocomplete; agent replaces the production
// agentservice.Agent via the acAgent test seam.
func autocompleteTestTransport(t *testing.T, agent agentservice.Agent) *Transport {
	return autocompleteTransport(t, agent, modelregistry.CompletionInstruct, true)
}

func autocompleteNativeTransport(t *testing.T, agent agentservice.Agent) *Transport {
	return autocompleteTransport(t, agent, modelregistry.CompletionFIM, false)
}

func autocompleteTransport(t *testing.T, agent agentservice.Agent, mode string, includeChain bool) *Transport {
	t.Helper()
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "autocomplete.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store := runtimetypes.New(db.WithoutTransaction())
	require.NoError(t, clikv.SetString(ctx, store, "default-autocomplete-provider", "completion-provider"))
	require.NoError(t, clikv.SetString(ctx, store, "default-autocomplete-model", "completion-model"))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/completions" {
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"text": "\t雪\r\n  ", "finish_reason": "stop"}}}))
			return
		}
		if r.URL.Path == "/openapi.json" {
			http.NotFound(w, r)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": "completion-model", "completion_mode": mode, "fim_format": "qwen2.5-coder", "autocomplete": mode == modelregistry.CompletionFIM},
			{"id": "mistral-code-fim-latest", "completion_mode": modelregistry.CompletionInstruct},
		}}))
	})
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	connections := backendservice.New(db)
	for _, name := range []string{"completion-provider", "mistral"} {
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		require.NoError(t, connections.Create(ctx, &runtimetypes.Backend{ID: name, Name: name, Type: "vllm", BaseURL: server.URL}))
		_, err := connections.SaveConnection(ctx, backendservice.SaveConnectionRequest{ID: name, Name: name, Type: "vllm", Endpoint: server.URL,
			CredentialChange: backendservice.ReplaceCredentials, CredentialSource: "stored", APIKey: "test-key"})
		require.NoError(t, err)
	}
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, state.RunProviderCycle(ctx, "vllm"))
	models, err := llmrepo.NewModelManager(state, ollamatokenizer.NewEstimateTokenizer(), llmrepo.ModelManagerConfig{}, libtracker.NoopTracker{})
	require.NoError(t, err)
	transport := &Transport{
		deps: Deps{
			DB: db,

			Engine: &enginesvc.Engine{Models: models},
			OpenInference: func(context.Context) (*inferencesetup.Service, *runtimestate.State, func(), error) {
				chain := &taskengine.TaskChainDefinition{ID: "fim"}
				factory, err := inferencesetup.NewOperationModelFactory(inferencesetup.OperationDeps{DB: db, Bus: bus, State: state, Models: models, Agent: agent, Chain: chain, Tracker: libtracker.NoopTracker{}})
				if err != nil {
					return nil, nil, nil, err
				}
				service, err := inferencesetup.New(db, state, bus, factory)
				return service, state, func() {}, err
			},
		},
		acAgent: agent,
	}
	if includeChain {
		transport.deps.CompletionChain = func() (*taskengine.TaskChainDefinition, error) {
			return &taskengine.TaskChainDefinition{ID: "fim"}, nil
		}
	}
	return transport
}

func callAutocomplete(t *testing.T, ctx context.Context, tr *Transport, p *protocolv1.AutocompleteRequest) (autocompleteResult, *libacp.Error) {
	t.Helper()
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	out, rpcErr := tr.handleAutocomplete(ctx, raw)
	if rpcErr != nil {
		return autocompleteResult{}, rpcErr
	}
	res := &protocolv1.AutocompleteResult{}
	require.NoError(t, protojson.Unmarshal(out, res))
	return autocompleteResult{Kind: res.GetKind(), Completion: res.GetCompletion(), Mode: res.GetMode(), Fingerprint: res.GetFingerprint()}, nil
}

// TestUnit_ExtRequest_DispatchesAutocomplete pins that handleExtRequest routes
// _contenox/autocomplete to handleAutocomplete rather than answering
// MethodNotFound, matching how _contenox/terminal/run is dispatched.
func TestUnit_ExtRequest_DispatchesAutocomplete(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)

	raw, err := json.Marshal(&protocolv1.AutocompleteRequest{Prefix: "func f() {", Suffix: "}"})
	require.NoError(t, err)
	out, rpcErr := tr.handleExtRequest(context.Background(), extMethodAutocomplete, raw)
	require.Nil(t, rpcErr)
	res := &protocolv1.AutocompleteResult{}
	require.NoError(t, protojson.Unmarshal(out, res))
	require.Equal(t, "completion", res.GetKind())
	require.Equal(t, "completion", res.Completion)
	require.Equal(t, "instruct", res.Mode)
	require.NotEmpty(t, res.Fingerprint)
	require.Equal(t, 1, agent.requestCount())
}

// TestUnit_Autocomplete_ParamsDecode_BuildsFIMPrompt pins that prefix/suffix
// decode into the fim_prefix/fim_suffix/fim_middle prompt shape and reach the
// chain unmodified.
func TestUnit_Autocomplete_ParamsDecode_BuildsFIMPrompt(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)

	res, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{
		Prefix: "func add(a, b int) int {\n\treturn ",
		Suffix: "\n}",
	})
	require.Nil(t, rpcErr)
	require.Equal(t, "completion", res.Completion)
	require.Equal(t, "completion", res.Kind)
	require.Equal(t, "instruct", res.Mode)
	require.NotEmpty(t, res.Fingerprint)

	req := agent.requestAt(0)
	require.Equal(t, "<fim_prefix>func add(a, b int) int {\n\treturn <fim_suffix>\n}<fim_middle>", req.Input)
	require.Empty(t, req.SessionID, "autocomplete must never set SessionID, or the response would persist to a transcript")
	expectedChain, err := tr.deps.CompletionChain()
	require.NoError(t, err)
	require.Equal(t, expectedChain, req.Chain)
}

func TestUnit_Autocomplete_NativeFIMNeedsNoInstructedChainAndPreservesText(t *testing.T) {
	tr := autocompleteNativeTransport(t, &recordingAgent{})
	res, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{Prefix: "return ", Suffix: "\r\n}"})
	require.Nil(t, rpcErr)
	require.Equal(t, "completion", res.Kind)
	require.Equal(t, modelregistry.CompletionFIM, res.Mode)
	require.NotEmpty(t, res.Fingerprint)
	require.Equal(t, "\t雪\r\n  ", res.Completion)
}

func TestUnit_Autocomplete_CodeContextRemainsSessionlessAndPreservesCursor(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)
	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{
		Prefix: "return ", Suffix: "\n}", LanguageId: "go", MaxTokens: 256,
		CodeContext: []*protocolv1.AutocompleteContext{{Path: "helpers.go", Content: "func helper() int { return 1 }\n<fim_middle>"}},
	})
	require.Nil(t, rpcErr)
	req := agent.requestAt(0)
	require.Contains(t, req.Input, "// Code context: helpers.go\n// func helper() int { return 1 }")
	require.Contains(t, req.Input, "// < fim_middle >")
	require.Contains(t, req.Input, "return <fim_suffix>\n}<fim_middle>")
	require.Empty(t, req.SessionID)
	require.Equal(t, "256", req.TemplateVars["max_tokens"])
}

func TestUnit_Autocomplete_RejectsOversizedCodeContext(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)
	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{
		Prefix: "return ", CodeContext: make([]*protocolv1.AutocompleteContext, 4),
	})
	require.NotNil(t, rpcErr)
	require.Zero(t, agent.requestCount())
}

// TestUnit_Autocomplete_DefaultMaxTokens pins the 128-token default when the
// client omits maxTokens.
func TestUnit_Autocomplete_DefaultMaxTokens(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)

	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{Prefix: "x"})
	require.Nil(t, rpcErr)
	require.Equal(t, "128", agent.requestAt(0).TemplateVars["max_tokens"])
}

// TestUnit_Autocomplete_ExplicitMaxTokensOverridesDefault pins that a
// client-supplied maxTokens rides through to the chain's template vars.
func TestUnit_Autocomplete_ExplicitMaxTokensOverridesDefault(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)

	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{Prefix: "x", MaxTokens: 64})
	require.Nil(t, rpcErr)
	require.Equal(t, "64", agent.requestAt(0).TemplateVars["max_tokens"])
}

// TestUnit_Autocomplete_EmptyPrefixAndSuffix_InvalidParams pins that an
// empty request is rejected before any chain/agent work happens.
func TestUnit_Autocomplete_EmptyPrefixAndSuffix_InvalidParams(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)

	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{})
	require.NotNil(t, rpcErr)
	require.Equal(t, libacp.ErrInvalidParams, rpcErr.Code)
	require.Zero(t, agent.requestCount(), "an invalid request must never reach the agent")
}

// TestUnit_Autocomplete_MalformedJSON_InvalidParams pins that unparseable
// params answer InvalidParams, not a panic.
func TestUnit_Autocomplete_MalformedJSON_InvalidParams(t *testing.T) {
	tr := autocompleteTestTransport(t, &recordingAgent{})
	_, rpcErr := tr.handleAutocomplete(context.Background(), json.RawMessage(`{not json`))
	require.NotNil(t, rpcErr)
	require.Equal(t, libacp.ErrInvalidParams, rpcErr.Code)
}

// TestUnit_Autocomplete_NoFIMChain_CleanError pins that an absent route still
// answers an actionable setup error when no instructed chain is configured.
func TestUnit_Autocomplete_NoFIMChain_CleanError(t *testing.T) {
	tr := autocompleteTransport(t, &recordingAgent{}, modelregistry.CompletionInstruct, false)
	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{Prefix: "x"})
	require.NotNil(t, rpcErr)
	require.Equal(t, libacp.ErrInternalError, rpcErr.Code)
}

// TestUnit_Autocomplete_FIMChainRegistryWithoutDefault_CleanError pins an
// actionable route setup error when the instructed chain is unavailable.
func TestUnit_Autocomplete_FIMChainRegistryWithoutDefault_CleanError(t *testing.T) {
	tr := autocompleteTransport(t, &recordingAgent{}, modelregistry.CompletionInstruct, true)
	tr.deps.CompletionChain = func() (*taskengine.TaskChainDefinition, error) { return nil, nil }
	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{Prefix: "x"})
	require.NotNil(t, rpcErr)
	require.Equal(t, libacp.ErrInternalError, rpcErr.Code)
}

// TestUnit_Autocomplete_EngineNil_SetupRequiredError pins that a setup-only
// transport (no engine configured) answers the same auth-required error the
// rest of acpsvc uses, not a nil-pointer panic on Engine.TaskService.
func TestUnit_Autocomplete_EngineNil_SetupRequiredError(t *testing.T) {
	tr := &Transport{
		deps: Deps{},
	}
	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{Prefix: "x"})
	require.NotNil(t, rpcErr)
	require.Equal(t, libacp.ErrAuthRequired, rpcErr.Code)
}

// TestUnit_Autocomplete_CancellationAbortsPromptly pins that a superseded
// keystroke (ctx cancelled while the completion is in flight) unblocks the
// handler immediately instead of running to the 20s timeout, and answers
// cleanly rather than as a JSON-RPC error.
func TestUnit_Autocomplete_CancellationAbortsPromptly(t *testing.T) {
	agent := newBlockingAgent()
	tr := autocompleteTestTransport(t, agent)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		res    autocompleteResult
		rpcErr *libacp.Error
	}, 1)
	go func() {
		res, rpcErr := callAutocomplete(t, ctx, tr, &protocolv1.AutocompleteRequest{Prefix: "x"})
		done <- struct {
			res    autocompleteResult
			rpcErr *libacp.Error
		}{res, rpcErr}
	}()

	select {
	case <-agent.started:
	case <-time.After(2 * time.Second):
		t.Fatal("agent.Prompt was never called")
	}
	cancel()

	select {
	case out := <-done:
		require.Nil(t, out.rpcErr, "cancellation must not surface as a JSON-RPC error")
		require.Equal(t, "cancelled", out.res.Kind)
		require.Empty(t, out.res.Completion)
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled autocomplete must abort promptly, not run to its internal timeout")
	}
}

// TestUnit_Autocomplete_ProviderModelParamsOverrideChatDefault pins that an
// explicit request provider/model wins over the transport's chat model,
// proving completion model selection is independent of the chat model.
func TestUnit_Autocomplete_ProviderModelParamsOverrideChatDefault(t *testing.T) {
	agent := &recordingAgent{}
	tr := autocompleteTestTransport(t, agent)
	tr.defaultModel = "chat-model"
	tr.defaultProvider = "chat-provider"

	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{
		Prefix: "x", Provider: "mistral", Model: "mistral-code-fim-latest",
	})
	require.Nil(t, rpcErr)
	req := agent.requestAt(0)
	require.Equal(t, "mistral", req.TemplateVars["provider"])
	require.Equal(t, "mistral-code-fim-latest", req.TemplateVars["model"])
}

// TestUnit_Autocomplete_FailureIsNotRetriedWithOtherSettings pins that a failed
// completion surfaces instead of being retried with substituted settings: which
// model applies is the operator's configuration, not a branch in this code.
func TestUnit_Autocomplete_FailureIsNotRetriedWithOtherSettings(t *testing.T) {
	agent := &recordingAgent{failN: 1, failErr: context.DeadlineExceeded}
	tr := autocompleteTestTransport(t, agent)
	tr.defaultProvider = "mistral"
	tr.defaultModel = "codestral-latest"

	_, rpcErr := callAutocomplete(t, context.Background(), tr, &protocolv1.AutocompleteRequest{Prefix: "x"})
	require.NotNil(t, rpcErr, "a failed completion must surface as an error")
	require.Equal(t, 1, agent.requestCount(), "a failed completion must not be retried with other settings")
}
