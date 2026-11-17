package codecompletion_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type refusingNative struct{}

func (refusingNative) Autocomplete(context.Context, llmrepo.Request, modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, llmrepo.Meta, error) {
	return modelrepo.AutocompleteResult{}, llmrepo.Meta{}, errors.New("native autocomplete must not run for an instructed request")
}

type recordingNative struct {
	request modelrepo.AutocompleteRequest
	route   llmrepo.Request
	ctx     context.Context
	err     error
}

func (r *recordingNative) Autocomplete(ctx context.Context, route llmrepo.Request, request modelrepo.AutocompleteRequest) (modelrepo.AutocompleteResult, llmrepo.Meta, error) {
	r.ctx, r.route, r.request = ctx, route, request
	return modelrepo.AutocompleteResult{Text: "\treturn a + b\n", FinishReason: "stop"}, llmrepo.Meta{}, r.err
}

func TestUnit_NativeCompletionBypassesClassificationAndPreservesSource(t *testing.T) {
	agent := &recordingPrompter{err: errors.New("chat must not run")}
	native := &recordingNative{}
	service, err := codecompletion.New(agent, &taskengine.TaskChainDefinition{ID: "instructed"}, native)
	require.NoError(t, err)
	request := codecompletion.Request{Mode: "fim", Provider: "connection:selected", Model: "same-name", Prefix: "func add(a,b int) int {\n", Suffix: "}\n", MaxTokens: 64}
	text, err := service.CompleteLatest(t.Context(), codecompletion.Editor{ClientID: "client", DocumentID: "file"}, request)
	require.NoError(t, err)
	require.Equal(t, "\treturn a + b\n", text)
	require.Zero(t, agent.calls)
	require.Equal(t, request.Prefix, native.request.Prefix)
	require.Equal(t, request.Suffix, native.request.Suffix)
	require.Equal(t, []string{request.Provider}, native.route.ProviderTypes)
	require.Equal(t, []string{request.Model}, native.route.ModelNames)
	require.Equal(t, request.MaxTokens, native.request.MaxTokens)
	native.err = modelrepo.ErrAutocompleteUnsupported
	_, err = service.Complete(t.Context(), request)
	require.ErrorIs(t, err, modelrepo.ErrAutocompleteUnsupported)
	require.Zero(t, agent.calls)
	request.Mode = ""
	_, err = service.Complete(t.Context(), request)
	require.ErrorContains(t, err, "explicit")
}
