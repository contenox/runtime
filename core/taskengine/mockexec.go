package taskengine

import (
	"context"

	"github.com/contenox/contenox/core/llmresolver"
)

// MockTaskExecutor is a mock implementation of taskengine.TaskExecutor.
type MockTaskExecutor struct {
	MockOutput       any
	MockRawResponse  string
	MockError        error
	CalledWithTask   *ChainTask
	CalledWithPrompt string

	// Add a function to dynamically return errors
	ErrorSequence []error // simulate multiple error responses
	callIndex     int
}

// TaskExec is the mock implementation of the TaskExec method.
func (m *MockTaskExecutor) TaskExec(ctx context.Context, resolver llmresolver.Policy, currentTask *ChainTask, input any) (any, string, error) {
	m.CalledWithTask = currentTask
	m.CalledWithPrompt, _ = input.(string)

	var err error
	if m.callIndex < len(m.ErrorSequence) {
		err = m.ErrorSequence[m.callIndex]
		m.callIndex++
	} else {
		err = m.MockError
	}

	return m.MockOutput, m.MockRawResponse, err
}
