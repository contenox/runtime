package inferencesetup_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/contenox/runtime/libbus"
	"github.com/contenox/runtime/libdbexec"
	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/backendservice"
	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/inferencesetup"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/runtimestate"
	"github.com/contenox/runtime/runtime/runtimetypes"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type modelFactory struct {
	repo llmrepo.ModelRepo
}

func (f modelFactory) Models(context.Context) (llmrepo.ModelRepo, func(), error) {
	return f.repo, func() {}, nil
}

func (f modelFactory) CompletionFingerprint(context.Context) (string, error) {
	return "fixture-completion-chain", nil
}

func (f modelFactory) ExecutionFingerprint(_ context.Context, mode, format string) (string, error) {
	return "fixture-execution:" + mode + ":" + format, nil
}

func (f modelFactory) NextEdit(ctx context.Context, assignment inferencesetup.Assignment, input modelcontract.NextEditInput) ([]modelcontract.TextEdit, error) {
	models, ok := f.repo.(*verifyingModels)
	if !ok {
		return nil, errors.New("next-edit fixture requires verifying models")
	}
	service, err := codecompletion.NewNextEdit(models)
	if err != nil {
		return nil, err
	}
	format := "sweep-v1"
	profile, err := modelcontract.ResolveNextEdit(format)
	if err != nil {
		return nil, err
	}
	return service.Predict(ctx, codecompletion.NextEditRequest{Provider: assignment.Provider, Model: assignment.Model, Format: format, Input: input, MaxTokens: profile.MaxTokens})
}

func (f modelFactory) NextEditExecutionFingerprint(_ context.Context, format string) (string, error) {
	return "fixture-next-edit:" + format, nil
}

type changedCompletionFactory struct{ modelFactory }

func (f changedCompletionFactory) CompletionFingerprint(context.Context) (string, error) {
	return "revised-completion-chain", nil
}

func (f modelFactory) Complete(ctx context.Context, assignment inferencesetup.Assignment, prefix, suffix string) (string, error) {
	models, ok := f.repo.(*verifyingModels)
	if !ok {
		return "", errors.New("completion fixture requires verifying models")
	}
	service, err := codecompletion.New(models, &taskengine.TaskChainDefinition{ID: "completion"}, models)
	if err != nil {
		return "", err
	}
	return service.Complete(ctx, codecompletion.Request{Mode: "instruct", Provider: assignment.Provider, Model: assignment.Model, Prefix: prefix, Suffix: suffix, MaxTokens: 64, TemplateVars: map[string]string{}})
}

type verifyingModels struct {
	failRole          string
	constantEmbedding bool
	failInput         string
}

func (m *verifyingModels) Prompt(_ context.Context, req agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
	if m.failRole == inferencesetup.RoleAutocomplete || req.Chain == nil || req.TemplateVars["provider"] == "" || req.TemplateVars["model"] == "" {
		return nil, errors.New("autocomplete failed")
	}
	return &agentservice.PromptResponse{Output: "42"}, nil
}

func (m *verifyingModels) Tokenize(context.Context, string, string) ([]int, error) {
	return nil, errors.New("not used")
}

func (m *verifyingModels) CountTokens(context.Context, string, string) (int, error) {
	return 0, errors.New("not used")
}

func (m *verifyingModels) PromptExecute(context.Context, llmrepo.Request, string, float32, string, ...modelrepo.PromptArgument) (string, llmrepo.Meta, error) {
	return "", llmrepo.Meta{}, errors.New("generic prompt must not verify code completion")
}

func (m *verifyingModels) Chat(_ context.Context, req llmrepo.Request, messages []modelrepo.Message, _ ...modelrepo.ChatArgument) (modelrepo.ChatResult, llmrepo.Meta, error) {
	if m.failRole == req.WorkerRole || req.WorkerRole != inferencesetup.RoleChat && req.WorkerRole != inferencesetup.RoleRecovery {
		return modelrepo.ChatResult{}, llmrepo.Meta{}, errors.New("chat failed")
	}
	if len(messages[0].Images) != 0 || len(messages[0].Audio) != 0 {
		answer, err := mediaAnswer(messages[0], m.failInput)
		return modelrepo.ChatResult{Message: modelrepo.Message{Role: "assistant", Content: answer}}, llmrepo.Meta{}, err
	}
	return toolProbeResult(messages), llmrepo.Meta{}, nil
}

func (m *verifyingModels) Embed(ctx context.Context, _ llmrepo.EmbedRequest, input string) ([]float64, llmrepo.Meta, error) {
	if m.failRole == inferencesetup.RoleEmbedding {
		return nil, llmrepo.Meta{}, errors.New("embedding failed")
	}
	if m.constantEmbedding {
		return []float64{1, 2, 3}, llmrepo.Meta{}, nil
	}
	if modelrepo.EmbeddingPurposeFromContext(ctx) == modelrepo.EmbeddingQuery {
		return []float64{1, 0.1, 0}, llmrepo.Meta{}, nil
	}
	if strings.Contains(input, "Banana") {
		return []float64{0, 1, 0}, llmrepo.Meta{}, nil
	}
	return []float64{1, 0, 0}, llmrepo.Meta{}, nil
}

func (m *verifyingModels) Stream(context.Context, llmrepo.Request, []modelrepo.Message, ...modelrepo.ChatArgument) (<-chan *modelrepo.StreamParcel, llmrepo.Meta, error) {
	return nil, llmrepo.Meta{}, errors.New("not used")
}

func TestIntegration_Roles_RequireCapabilityVerificationBeforeReadiness(t *testing.T) {
	ctx := context.Background()
	db, err := libdbexec.NewSQLiteDBManager(ctx, filepath.Join(t.TempDir(), "setup.db"), runtimetypes.SchemaSQLite)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	bus := libbus.NewSQLite(db.WithoutTransaction())
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": "chat-model", "capabilities": []string{"chat", "completion"}, "completion_mode": "instruct"},
			{"id": "fim-model", "capabilities": []string{"completion"}, "completion_mode": "fim", "fim_format": "qwen2.5-coder"},
			{"id": "edit-model", "next_edit": true, "next_edit_format": "sweep-v1"},
			{"id": "generic-chat", "capabilities": []string{"chat", "completion"}},
			{"id": "text-embedding-3-small", "capabilities": []string{"embedding"}},
		}}))
	}))
	defer server.Close()
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "provider-1", Name: "provider", Type: "openai", BaseURL: server.URL}))
	credential, err := json.Marshal(runtimestate.ProviderConfig{Type: "openai", APIKey: "test-key"})
	require.NoError(t, err)
	require.NoError(t, runtimetypes.New(db.WithoutTransaction()).SetKV(ctx, runtimestate.BackendCredentialKey("openai", "provider-1"), credential))
	require.NoError(t, backendservice.New(db).Create(ctx, &runtimetypes.Backend{ID: "provider-2", Name: "recovery-provider", Type: "vllm", BaseURL: server.URL}))
	state, err := runtimestate.New(ctx, db, bus, runtimestate.WithAutoDiscoverModels())
	require.NoError(t, err)
	require.NoError(t, state.RunProviderCycle(ctx, "openai"))
	require.NoError(t, state.RunProviderCycle(ctx, "vllm"))
	models := &verifyingModels{}
	service, err := inferencesetup.New(db, state, bus, modelFactory{repo: models})
	require.NoError(t, err)
	for model, facts := range map[string]runtimetypes.ModelFacts{
		"chat-model": {Registered: true, ContextLength: 8192, Capabilities: []string{"completion"}, CompletionMode: "instruct"},
		"fim-model":  {Registered: true, ContextLength: 8192, Capabilities: []string{"autocomplete"}, CompletionMode: "fim", FIMFormat: "qwen2.5-coder"},
		"edit-model": {Registered: true, ContextLength: 8192, Capabilities: []string{"next_edit"}, NextEditFormat: "sweep-v1"},
	} {
		require.NoError(t, service.RegisterModel(ctx, inferencesetup.RegisterModelRequest{ConnectionID: "provider-1", Model: model, Facts: facts}))
	}
	require.NoError(t, service.RegisterModel(ctx, inferencesetup.RegisterModelRequest{ConnectionID: "provider-2", Model: "chat-model", Facts: runtimetypes.ModelFacts{Registered: true, ContextLength: 8192, Capabilities: []string{"completion"}, CompletionMode: "instruct"}}))
	fimAssignment := inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "fim-model"}
	fimFingerprint, err := service.ConfigurationFingerprint(ctx, fimAssignment)
	require.NoError(t, err)
	changedFIM, err := inferencesetup.New(db, state, bus, changedCompletionFactory{modelFactory{repo: models}})
	require.NoError(t, err)
	changedFIMFingerprint, err := changedFIM.ConfigurationFingerprint(ctx, fimAssignment)
	require.NoError(t, err)
	require.Equal(t, fimFingerprint, changedFIMFingerprint, "native completion verification is independent of the instructed chain")

	ready, err := service.Ready(ctx)
	require.NoError(t, err)
	require.False(t, ready)
	_, err = service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "generic-chat"}, inferencesetup.SourceUser)
	require.Error(t, err)
	chat, err := service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleChat, Provider: "openai", Model: "chat-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Zero(t, chat.Dimension)
	recovery, err := service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleRecovery, Provider: "vllm", Model: "chat-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Equal(t, "vllm", recovery.Provider)
	autocomplete, err := service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "chat-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Zero(t, autocomplete.Dimension)
	nextEdit, err := service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleNextEdit, Provider: "openai", Model: "edit-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Equal(t, inferencesetup.RoleNextEdit, nextEdit.Role)
	require.False(t, roleByID(t, mustRoles(t, service, ctx), inferencesetup.RoleNextEdit).Enabled)
	require.NoError(t, service.SetNextEditEnabled(ctx, true))
	require.True(t, roleByID(t, mustRoles(t, service, ctx), inferencesetup.RoleNextEdit).Enabled)
	require.False(t, roleByID(t, mustRoles(t, service, ctx), inferencesetup.RoleEmbedding).Enabled, "workspace search is off until the operator turns it on")
	ready, err = service.Ready(ctx)
	require.NoError(t, err)
	require.True(t, ready, "an embedding role that is off does not block readiness")
	require.ErrorIs(t, service.SetRoleEnabled(ctx, inferencesetup.RoleEmbedding, true), inferencesetup.ErrSetupIncomplete, "turning workspace search on needs a verified embedding model")
	require.False(t, roleByID(t, mustRoles(t, service, ctx), inferencesetup.RoleEmbedding).Enabled)
	_, err = service.VerifiedRole(ctx, inferencesetup.RoleEmbedding)
	require.ErrorIs(t, err, inferencesetup.ErrRoleDisabled)
	require.ErrorIs(t, err, inferencesetup.ErrSetupIncomplete)
	require.ErrorContains(t, service.SetRoleEnabled(ctx, inferencesetup.RoleChat, false), "required")
	require.Equal(t, []string{inferencesetup.RoleNextEdit, inferencesetup.RoleEmbedding}, inferencesetup.OptionalRoles())
	embedding, err := service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleEmbedding, Provider: "openai", Model: "text-embedding-3-small"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Equal(t, 3, embedding.Dimension)
	require.NoError(t, service.SetRoleEnabled(ctx, inferencesetup.RoleEmbedding, true), "a verified embedding model lets workspace search turn on")
	require.True(t, roleByID(t, mustRoles(t, service, ctx), inferencesetup.RoleEmbedding).Enabled)
	ready, err = service.Ready(ctx)
	require.NoError(t, err)
	require.True(t, ready)
	restarted, err := inferencesetup.New(db, state, bus, modelFactory{repo: models})
	require.NoError(t, err)
	ready, err = restarted.Ready(ctx)
	require.NoError(t, err)
	require.True(t, ready)
	reviewed, err := service.Review(ctx, []inferencesetup.RouteProposal{{
		Assignment:     inferencesetup.Assignment{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "chat-model"},
		SettingsChange: inferencesetup.PreserveSettings,
	}})
	require.NoError(t, err)
	changed, err := inferencesetup.New(db, state, bus, changedCompletionFactory{modelFactory{repo: models}})
	require.NoError(t, err)
	changedRoles, err := changed.Roles(ctx)
	require.NoError(t, err)
	require.Equal(t, "needs-verification", roleByID(t, changedRoles, inferencesetup.RoleAutocomplete).State)
	for _, role := range []string{inferencesetup.RoleChat, inferencesetup.RoleRecovery, inferencesetup.RoleEmbedding} {
		require.Equal(t, "available", roleByID(t, changedRoles, role).State)
	}
	_, err = changed.Apply(ctx, reviewed, inferencesetup.SourceUser)
	require.ErrorIs(t, err, inferencesetup.ErrConfigurationChanged)
	ready, err = service.Ready(ctx)
	require.NoError(t, err)
	require.True(t, ready)
	require.NoError(t, service.RemoveProvider(ctx, "provider-2"))
	ready, err = service.Ready(ctx)
	require.NoError(t, err)
	require.False(t, ready)
	recovery, err = service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleRecovery, Provider: "openai", Model: "chat-model"}, inferencesetup.SourceUser)
	require.NoError(t, err)
	require.Equal(t, "openai", recovery.Provider)
	models.constantEmbedding = true
	_, err = service.Assign(ctx, inferencesetup.Assignment{Role: inferencesetup.RoleEmbedding, Provider: "openai", Model: "text-embedding-3-small"}, inferencesetup.SourceUser)
	require.ErrorContains(t, err, "failed retrieval")
	models.constantEmbedding = false

	models.failRole = inferencesetup.RoleEmbedding
	_, err = service.AssignAll(ctx, []inferencesetup.Assignment{
		{Role: inferencesetup.RoleAutocomplete, Provider: "openai", Model: "chat-model"},
		{Role: inferencesetup.RoleEmbedding, Provider: "openai", Model: "text-embedding-3-small"},
	}, inferencesetup.SourceUser)
	require.ErrorContains(t, err, "embedding failed")
	roles, err := service.Roles(ctx)
	require.NoError(t, err)
	require.Equal(t, "openai", roleByID(t, roles, inferencesetup.RoleChat).Provider)
	require.Equal(t, "available", roleByID(t, roles, inferencesetup.RoleEmbedding).State)
	require.NoError(t, service.RemoveProvider(ctx, "provider-1"))
	roles, err = service.Roles(ctx)
	require.NoError(t, err)
	require.Equal(t, "needs-attention", roleByID(t, roles, inferencesetup.RoleChat).State)
}

func roleByID(t *testing.T, roles []inferencesetup.RoleStatus, id string) inferencesetup.RoleStatus {
	t.Helper()
	for _, role := range roles {
		if role.Role == id {
			return role
		}
	}
	t.Fatalf("role %q not found", id)
	return inferencesetup.RoleStatus{}
}

func mustRoles(t *testing.T, service *inferencesetup.Service, ctx context.Context) []inferencesetup.RoleStatus {
	t.Helper()
	roles, err := service.Roles(ctx)
	require.NoError(t, err)
	return roles
}

func (m *verifyingModels) Autocomplete(context.Context, llmrepo.Request, modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, llmrepo.Meta, error) {
	return modelrepo.AutocompleteResult{}, llmrepo.Meta{}, errors.New("native autocomplete must not run in this instructed fixture")
}

func (m *verifyingModels) NextEdit(_ context.Context, req llmrepo.Request, request modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error) {
	if m.failRole == inferencesetup.RoleNextEdit || req.WorkerRole != inferencesetup.RoleNextEdit {
		return modelrepo.NextEditResult{}, llmrepo.Meta{}, errors.New("native next edit failed")
	}
	input := request.Input
	return modelrepo.NextEditResult{FinishReason: "stop", Edits: []modelcontract.TextEdit{{Start: input.Start, End: input.End, OldText: input.Current[input.Start:input.End], NewText: "right"}}}, llmrepo.Meta{}, nil
}
