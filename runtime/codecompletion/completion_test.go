package codecompletion_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

func TestUnit_CompletionFingerprintTracksLoadedChainAcrossRestart(t *testing.T) {
	agent := &recordingPrompter{output: "42"}
	chain := &taskengine.TaskChainDefinition{ID: "completion", Tasks: []taskengine.TaskDefinition{{
		ID: "insertion", Handler: taskengine.HandleChatCompletion, SystemInstruction: "Insert only the missing expression.",
	}}}
	service, err := codecompletion.New(agent, chain, refusingNative{})
	require.NoError(t, err)
	before, err := service.Fingerprint()
	require.NoError(t, err)
	require.NotEmpty(t, before)
	raw, err := json.MarshalIndent(chain, "", "  ")
	require.NoError(t, err)
	var reloaded taskengine.TaskChainDefinition
	require.NoError(t, json.Unmarshal(raw, &reloaded))
	restarted, err := codecompletion.New(agent, &reloaded, refusingNative{})
	require.NoError(t, err)
	same, err := restarted.Fingerprint()
	require.NoError(t, err)
	require.Equal(t, before, same)
	reloaded.Tasks[0].SystemInstruction = "Generate a whole function."
	changed, err := restarted.Fingerprint()
	require.NoError(t, err)
	require.NotEqual(t, before, changed)
}

type recordingPrompter struct {
	request agentservice.PromptRequest
	ctx     context.Context
	calls   int
	output  any
	err     error
}

func (p *recordingPrompter) Prompt(ctx context.Context, request agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
	p.ctx = ctx
	p.request, p.calls = request, p.calls+1
	if p.err != nil {
		return nil, p.err
	}
	return &agentservice.PromptResponse{Output: p.output}, nil
}

func TestUnit_CompletionCacheIdentityIsStableAndScopedToTheEditor(t *testing.T) {
	agent := &recordingPrompter{output: "42"}
	service, err := codecompletion.New(agent, &taskengine.TaskChainDefinition{ID: "classification-and-generation"}, refusingNative{})
	require.NoError(t, err)
	request := codecompletion.Request{Mode: "instruct", Provider: "connection:first", Model: "same", Prefix: "return ", MaxTokens: 32, TemplateVars: map[string]string{}}
	editor := codecompletion.Editor{ClientID: "first-ide", DocumentID: "file:///same.go"}
	ctx := llmrepo.WithSessionKey(t.Context(), "unrelated-chat-key")
	complete := func(editor codecompletion.Editor) string {
		t.Helper()
		_, err := service.CompleteLatest(ctx, editor, request)
		require.NoError(t, err)
		require.Empty(t, agent.request.SessionID)
		key := llmrepo.SessionKeyFromContext(agent.ctx)
		require.NotEmpty(t, key)
		require.NotEqual(t, "unrelated-chat-key", key)
		require.NotContains(t, key, editor.ClientID)
		require.NotContains(t, key, editor.DocumentID)
		return key
	}
	first := complete(editor)
	request.Prefix = "return 4"
	require.Equal(t, first, complete(editor))
	require.NotEqual(t, first, complete(codecompletion.Editor{ClientID: "second-ide", DocumentID: editor.DocumentID}))
	require.NotEqual(t, first, complete(codecompletion.Editor{ClientID: editor.ClientID, DocumentID: "file:///other.go"}))
	_, err = service.Complete(ctx, request)
	require.NoError(t, err)
	oneShot := llmrepo.SessionKeyFromContext(agent.ctx)
	require.NotEmpty(t, oneShot)
	require.NotEqual(t, "unrelated-chat-key", oneShot)
	require.NotEqual(t, first, oneShot)
	_, err = service.Complete(ctx, request)
	require.NoError(t, err)
	require.NotEqual(t, oneShot, llmrepo.SessionKeyFromContext(agent.ctx))
}

func TestUnit_CompletionRunsTheConfiguredChainWithAnExplicitRoute(t *testing.T) {
	agent := &recordingPrompter{output: "42\n"}
	chain := &taskengine.TaskChainDefinition{ID: "production-completion"}
	service, err := codecompletion.New(agent, chain, refusingNative{})
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"provider": "chat-provider", "model": "chat-model", "think": "off"}
	result, err := service.Complete(context.Background(), codecompletion.Request{Mode: "instruct", Provider: "chosen-provider", Model: "chosen-model", Prefix: "return ", Suffix: " }", MaxTokens: 128, TemplateVars: vars})
	if err != nil || result != "42" {
		t.Fatalf("completion %q: %v", result, err)
	}
	if agent.request.Chain != chain || agent.request.SessionID != "" {
		t.Fatal("completion changed the chain or created a session")
	}
	if agent.request.Input != "<fim_prefix>return <fim_suffix> }<fim_middle>" {
		t.Fatalf("cursor changed: %q", agent.request.Input)
	}
	if agent.request.TemplateVars["model"] != "chosen-model" || agent.request.TemplateVars["provider"] != "chosen-provider" || agent.request.TemplateVars["max_tokens"] != "128" {
		t.Fatal("explicit route was not used")
	}
	if vars["model"] != "chat-model" {
		t.Fatal("completion mutated caller configuration")
	}
}

func TestUnit_CompletionRejectsMissingRouteAndNeverRetriesFailures(t *testing.T) {
	agent := &recordingPrompter{err: errors.New("native load failed")}
	service, err := codecompletion.New(agent, &taskengine.TaskChainDefinition{ID: "completion"}, refusingNative{})
	if err != nil {
		t.Fatal(err)
	}
	request := codecompletion.Request{Mode: "instruct", Prefix: "return ", MaxTokens: 128, TemplateVars: map[string]string{"model": "chat-fallback", "provider": "chat-provider"}}
	if _, err := service.Complete(context.Background(), request); err == nil || agent.calls != 0 {
		t.Fatal("missing route used chat configuration")
	}
	request.Model, request.Provider = "completion-model", "completion-provider"
	if _, err := service.Complete(context.Background(), request); !errors.Is(err, agent.err) || agent.calls != 1 {
		t.Fatal("native failure was swallowed or retried")
	}
}

func TestUnit_CompletionRejectsEmptyVisibleOutput(t *testing.T) {
	agent := &recordingPrompter{output: "\n\t"}
	service, err := codecompletion.New(agent, &taskengine.TaskChainDefinition{ID: "completion"}, refusingNative{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Complete(context.Background(), codecompletion.Request{Mode: "instruct", Provider: "provider", Model: "model", Prefix: "return ", MaxTokens: 128, TemplateVars: map[string]string{}})
	if err == nil {
		t.Fatal("empty visible output was accepted")
	}
}
