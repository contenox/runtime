package codecompletion_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelcontract"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

type editExecutor func(context.Context, llmrepo.Request, modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error)

func (f editExecutor) NextEdit(ctx context.Context, route llmrepo.Request, request modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error) {
	return f(ctx, route, request)
}

func editRequest() codecompletion.NextEditRequest {
	return codecompletion.NextEditRequest{Provider: "connection:chosen", Model: "same-name", Format: "sweep-v1", MaxTokens: 512, Input: modelcontract.NextEditInput{Path: "a.go", Before: "var a = 1", Current: "var b = 1", Cursor: 5, End: 9}}
}

func TestUnit_NextEditSupersedesEntirePrediction(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	service, err := codecompletion.NewNextEdit(editExecutor(func(ctx context.Context, route llmrepo.Request, input modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error) {
		require.Equal(t, []string{"connection:chosen"}, route.ProviderTypes)
		require.Equal(t, "next-edit", route.WorkerRole)
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
		}
		return modelrepo.NextEditResult{Edits: []modelcontract.TextEdit{{Start: 8, End: 9, OldText: "1", NewText: "2"}}, FinishReason: "stop"}, llmrepo.Meta{}, nil
	}))
	require.NoError(t, err)
	editor := codecompletion.Editor{ClientID: "client", DocumentID: "doc"}
	type prediction struct {
		edits []modelcontract.TextEdit
		err   error
	}
	first := make(chan prediction, 1)
	go func() {
		edits, err := service.PredictLatest(t.Context(), editor, editRequest())
		first <- prediction{edits: edits, err: err}
	}()
	<-started
	edits, err := service.PredictLatest(t.Context(), editor, editRequest())
	require.NoError(t, err)
	require.Len(t, edits, 1)
	previous := <-first
	require.ErrorIs(t, previous.err, codecompletion.ErrSuperseded)
	require.Empty(t, previous.edits)
}

func TestUnit_NextEditBoundsCurrentFileAndRejectsOutsideReplacement(t *testing.T) {
	text := strings.Repeat("// café remains intact\r\n", 1000)
	for _, format := range []string{"sweep-v1", "sweep-v2", "zeta-2.1"} {
		t.Run(format, func(t *testing.T) {
			request := editRequest()
			request.Format = format
			request.Input = modelcontract.NextEditInput{Path: "long.go", Before: text, Current: text, Cursor: len(text) / 2, End: len(text)}
			service, err := codecompletion.NewNextEdit(editExecutor(func(_ context.Context, _ llmrepo.Request, selected modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error) {
				require.Positive(t, selected.Input.Start)
				require.Less(t, selected.Input.End, len(text))
				require.NoError(t, selected.Input.Validate())
				prepared, err := modelcontract.PrepareNextEdit(format, selected.Input)
				require.NoError(t, err)
				require.Less(t, len(prepared.Render()), 16000)
				return modelrepo.NextEditResult{FinishReason: "stop", Edits: []modelcontract.TextEdit{{Start: 0, End: 0, NewText: "outside"}}}, llmrepo.Meta{}, nil
			}))
			require.NoError(t, err)
			edits, err := service.Predict(t.Context(), request)
			require.ErrorContains(t, err, "outside its region")
			require.Empty(t, edits)
		})
	}
}

func TestUnit_NextEditRejectsOversizedCursorLineBeforeInference(t *testing.T) {
	request := editRequest()
	request.Input.Current = strings.Repeat("x", 10000)
	request.Input.Before = request.Input.Current
	request.Input.Cursor, request.Input.End = 5000, 10000
	service, err := codecompletion.NewNextEdit(editExecutor(func(context.Context, llmrepo.Request, modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error) {
		t.Fatal("oversized source must not reach inference")
		return modelrepo.NextEditResult{}, llmrepo.Meta{}, nil
	}))
	require.NoError(t, err)
	_, err = service.Predict(t.Context(), request)
	require.ErrorContains(t, err, "cursor line")
}

func TestUnit_NextEditRejectsStaleAndTruncatedProviderResults(t *testing.T) {
	for _, result := range []modelrepo.NextEditResult{
		{FinishReason: "length"},
		{FinishReason: "stop", Edits: []modelcontract.TextEdit{{Start: 8, End: 9, OldText: "stale", NewText: "2"}}},
		{FinishReason: "stop", Edits: []modelcontract.TextEdit{{Start: 0, End: 99, OldText: "", NewText: "2"}}},
	} {
		service, err := codecompletion.NewNextEdit(editExecutor(func(context.Context, llmrepo.Request, modelrepo.NextEditRequest) (modelrepo.NextEditResult, llmrepo.Meta, error) {
			return result, llmrepo.Meta{}, nil
		}))
		require.NoError(t, err)
		edits, err := service.Predict(t.Context(), editRequest())
		require.Error(t, err)
		require.Empty(t, edits)
	}
}

func TestUnit_NativeFingerprintDoesNotDependOnInstructedChain(t *testing.T) {
	service, err := codecompletion.NewNative(&recordingNative{})
	require.NoError(t, err)
	fingerprint, err := service.ExecutionFingerprint("fim", "qwen2.5-coder")
	require.NoError(t, err)
	require.NotEmpty(t, fingerprint)
	_, err = service.ExecutionFingerprint("instruct", "")
	require.ErrorContains(t, err, "unconfigured")
	require.NoError(t, codecompletion.Contract().Validate())
}
