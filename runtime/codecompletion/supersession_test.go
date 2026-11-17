package codecompletion_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/contenox/runtime/runtime/agentservice"
	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/taskengine"
	"github.com/stretchr/testify/require"
)

type pendingCompletion struct {
	ctx    context.Context
	finish chan string
}

type phasedPrompter struct{ started chan pendingCompletion }

func (p phasedPrompter) Prompt(ctx context.Context, _ agentservice.PromptRequest) (*agentservice.PromptResponse, error) {
	work := pendingCompletion{ctx: ctx, finish: make(chan string, 1)}
	select {
	case p.started <- work:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case text := <-work.finish:
		return &agentservice.PromptResponse{Output: text}, nil
	case <-time.After(5 * time.Second):
		return nil, errors.New("test completion timed out")
	}
}

func TestUnit_CompletionSupersessionOwnsWholeChainAndRejectsLateSuccess(t *testing.T) {
	agent := phasedPrompter{started: make(chan pendingCompletion, 3)}
	service, err := codecompletion.New(agent, &taskengine.TaskChainDefinition{ID: "classification-and-generation"}, refusingNative{})
	require.NoError(t, err)
	request := codecompletion.Request{Mode: "instruct", Provider: "connection:first", Model: "same", Prefix: "return ", MaxTokens: 32, TemplateVars: map[string]string{}}
	editor := codecompletion.Editor{ClientID: "first-ide", DocumentID: "file:///same.go"}
	type result struct {
		text string
		err  error
	}
	start := func(editor codecompletion.Editor, request codecompletion.Request) <-chan result {
		done := make(chan result, 1)
		go func() { text, err := service.CompleteLatest(t.Context(), editor, request); done <- result{text, err} }()
		return done
	}
	old := start(editor, request)
	oldChain := <-agent.started
	other := start(codecompletion.Editor{ClientID: "second-ide", DocumentID: editor.DocumentID}, request)
	otherChain := <-agent.started
	request.Provider = "connection:second"
	latest := start(editor, request)
	latestChain := <-agent.started
	require.ErrorIs(t, context.Cause(oldChain.ctx), codecompletion.ErrSuperseded)
	require.NoError(t, otherChain.ctx.Err())
	oldChain.finish <- "stale insertion"
	stale := <-old
	require.ErrorIs(t, stale.err, codecompletion.ErrSuperseded)
	require.Empty(t, stale.text)
	latestChain.finish <- "42"
	current := <-latest
	require.NoError(t, current.err)
	require.Equal(t, "42", current.text)
	otherChain.finish <- "independent"
	independent := <-other
	require.NoError(t, independent.err)
	require.Equal(t, "independent", independent.text)
}

func TestUnit_CancelledCompletionCannotReturnLateInsertion(t *testing.T) {
	agent := phasedPrompter{started: make(chan pendingCompletion, 1)}
	service, err := codecompletion.New(agent, &taskengine.TaskChainDefinition{ID: "completion"}, refusingNative{})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		text, err := service.CompleteLatest(ctx, codecompletion.Editor{ClientID: "client", DocumentID: "file"}, codecompletion.Request{Mode: "instruct", Provider: "provider", Model: "model", Prefix: "return ", MaxTokens: 32, TemplateVars: map[string]string{}})
		if text != "" {
			done <- errors.New("cancelled completion produced an insertion")
			return
		}
		done <- err
	}()
	work := <-agent.started
	cancel()
	work.finish <- "stale"
	require.ErrorIs(t, <-done, context.Canceled)
}
