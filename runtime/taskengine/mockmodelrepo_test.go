package taskengine_test

import (
	"context"
	"errors"

	"github.com/contenox/runtime/runtime/llmrepo"
	libmodelprovider "github.com/contenox/runtime/runtime/modelrepo"
	"github.com/contenox/runtime/runtime/taskengine"
)

type captureTaskEventSink struct {
	events []taskengine.TaskEvent
}

func (s *captureTaskEventSink) PublishTaskEvent(ctx context.Context, event taskengine.TaskEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *captureTaskEventSink) Wants(taskengine.TaskEventKind) bool { return true }

type mockModelRepo struct {
	streamFunc func(ctx context.Context, req llmrepo.Request, messages []libmodelprovider.Message, opts ...libmodelprovider.ChatArgument) (<-chan *libmodelprovider.StreamParcel, llmrepo.Meta, error)
	chatFunc   func(ctx context.Context, req llmrepo.Request, messages []libmodelprovider.Message, opts ...libmodelprovider.ChatArgument) (libmodelprovider.ChatResult, llmrepo.Meta, error)
	promptFunc func(ctx context.Context, req llmrepo.Request, systeminstruction string, temperature float32, prompt string) (string, llmrepo.Meta, error)
}

func (m *mockModelRepo) Tokenize(ctx context.Context, modelName string, prompt string) ([]int, error) {
	return []int{1}, nil
}

func (m *mockModelRepo) CountTokens(ctx context.Context, modelName string, prompt string) (int, error) {
	return 1, nil
}

func (m *mockModelRepo) PromptExecute(ctx context.Context, req llmrepo.Request, systeminstruction string, temperature float32, prompt string, args ...libmodelprovider.PromptArgument) (string, llmrepo.Meta, error) {
	if m.promptFunc != nil {
		return m.promptFunc(ctx, req, systeminstruction, temperature, prompt)
	}
	return "", llmrepo.Meta{}, errors.New("PromptExecute should not be called")
}

func (m *mockModelRepo) Chat(ctx context.Context, req llmrepo.Request, messages []libmodelprovider.Message, opts ...libmodelprovider.ChatArgument) (libmodelprovider.ChatResult, llmrepo.Meta, error) {
	if m.chatFunc == nil {
		return libmodelprovider.ChatResult{}, llmrepo.Meta{}, errors.New("Chat should not be called")
	}
	return m.chatFunc(ctx, req, messages, opts...)
}

func (m *mockModelRepo) Embed(ctx context.Context, embedReq llmrepo.EmbedRequest, prompt string) ([]float64, llmrepo.Meta, error) {
	return nil, llmrepo.Meta{}, errors.New("Embed should not be called")
}

func (m *mockModelRepo) Stream(ctx context.Context, req llmrepo.Request, messages []libmodelprovider.Message, opts ...libmodelprovider.ChatArgument) (<-chan *libmodelprovider.StreamParcel, llmrepo.Meta, error) {
	if m.streamFunc == nil {
		return nil, llmrepo.Meta{}, errors.New("streamFunc not configured")
	}
	return m.streamFunc(ctx, req, messages, opts...)
}

func (m *mockModelRepo) Autocomplete(context.Context, llmrepo.Request, libmodelprovider.AutocompleteRequest) (libmodelprovider.AutocompleteResult, llmrepo.Meta, error) {
	return libmodelprovider.AutocompleteResult{}, llmrepo.Meta{}, errors.New("native autocomplete must not run in this instructed fixture")
}

func (m *mockModelRepo) NextEdit(context.Context, llmrepo.Request, libmodelprovider.NextEditRequest) (libmodelprovider.NextEditResult, llmrepo.Meta, error) {
	return libmodelprovider.NextEditResult{}, llmrepo.Meta{}, errors.New("next edit must not run in this instructed fixture")
}

var _ llmrepo.ModelRepo = (*mockModelRepo)(nil)
var _ taskengine.TaskEventSink = (*captureTaskEventSink)(nil)
