package hooks

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/taskengine"
)

// MockHookRepo is a mock implementation of the HookProvider interface.
type MockHookRepo struct {
	Calls       []taskengine.HookCall
	ResponseMap map[string]any
}

// NewMockHookRegistry returns a new instance of MockHookProvider.
func NewMockHookRegistry() *MockHookRepo {
	return &MockHookRepo{
		ResponseMap: make(map[string]any),
	}
}

// Exec simulates execution of a hook call.
func (m *MockHookRepo) Exec(ctx context.Context, _ time.Time, _ any, _ taskengine.DataType, _ string, args *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	// Record call
	m.Calls = append(m.Calls, *args)

	// Simulate response
	if resp, ok := m.ResponseMap[args.Type]; ok {
		return taskengine.StatusSuccess, resp, taskengine.DataTypeAny, fmt.Sprintf("mock response for hook %s", args.Type), nil
	}

	// Default behavior
	return taskengine.StatusSuccess, fmt.Sprintf("mock response for hook %s", args.Type), taskengine.DataTypeAny, fmt.Sprintf("mock response for hook %s", args.Type), nil
}

func (m *MockHookRepo) Supports(ctx context.Context) ([]string, error) {
	return []string{"mock"}, nil
}

var _ taskengine.HookRegistry = (*MockHookRepo)(nil)
