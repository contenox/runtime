package codecompletion_test

import (
	"context"
	"errors"
	"testing"

	"github.com/contenox/runtime/runtime/codecompletion"
	"github.com/contenox/runtime/runtime/llmrepo"
	"github.com/contenox/runtime/runtime/modelrepo"
	"github.com/stretchr/testify/require"
)

type recordingPromptExecutor struct {
	route       llmrepo.Request
	system      string
	temperature float32
	prompt      string
	maxTokens   int
	calls       int
	err         error
}

func (r *recordingPromptExecutor) PromptExecute(_ context.Context, route llmrepo.Request, system string, temperature float32, prompt string, args ...modelrepo.PromptArgument) (string, llmrepo.Meta, error) {
	r.route, r.system, r.temperature, r.prompt, r.calls = route, system, temperature, prompt, r.calls+1
	config, err := modelrepo.PromptConfigFromArgs(args...)
	if err != nil {
		return "", llmrepo.Meta{}, err
	}
	if config.MaxTokens != nil {
		r.maxTokens = *config.MaxTokens
	}
	if r.err != nil {
		return "", llmrepo.Meta{}, r.err
	}
	return "```go\nreturn 42\n```\n", llmrepo.Meta{}, nil
}

func TestUnit_PromptCompletionUsesBoundedExplicitRouteWithoutFallback(t *testing.T) {
	prompt := &recordingPromptExecutor{}
	service, err := codecompletion.NewPromptOnly(prompt, refusingNative{})
	require.NoError(t, err)
	request := codecompletion.Request{Mode: "prompt", Provider: "selected-provider", Model: "prompt-model", Prefix: "func answer() int {\n", Suffix: "\n}\n", MaxTokens: 17}
	text, err := service.Complete(t.Context(), request)
	require.NoError(t, err)
	require.Equal(t, "return 42", text)
	require.Equal(t, []string{request.Provider}, prompt.route.ProviderTypes)
	require.Equal(t, []string{request.Model}, prompt.route.ModelNames)
	require.Equal(t, 17, prompt.maxTokens)
	require.Equal(t, float32(0), prompt.temperature)
	require.Contains(t, prompt.system, "Return only the code to insert")
	require.Contains(t, prompt.prompt, request.Prefix)
	require.Contains(t, prompt.prompt, request.Suffix)

	terminal := errors.New("prompt endpoint failed")
	prompt.err = terminal
	_, err = service.Complete(t.Context(), request)
	require.ErrorIs(t, err, terminal)
	require.Equal(t, 2, prompt.calls)
}

func TestUnit_PromptCompletionHasAnIndependentExecutionFingerprint(t *testing.T) {
	service, err := codecompletion.NewPromptOnly(&recordingPromptExecutor{}, refusingNative{})
	require.NoError(t, err)
	prompt, err := service.ExecutionFingerprint("prompt", "")
	require.NoError(t, err)
	require.NotEmpty(t, prompt)
	fim, err := service.ExecutionFingerprint("fim", "qwen2.5-coder")
	require.NoError(t, err)
	require.NotEqual(t, fim, prompt)
	_, err = service.ExecutionFingerprint("prompt", "qwen2.5-coder")
	require.ErrorContains(t, err, "cannot select")
}
