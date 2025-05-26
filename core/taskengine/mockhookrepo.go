package taskengine

import (
	"context"
	"fmt"
)

// MockHookRepo is a mock implementation of the HookProvider interface.
type MockHookRepo struct {
	Calls       []HookCall
	ResponseMap map[string]any
}

// NewMockHookProvider returns a new instance of MockHookProvider.
func NewMockHookProvider() *MockHookRepo {
	return &MockHookRepo{
		ResponseMap: make(map[string]any),
	}
}

// Exec simulates execution of a hook call.
func (m *MockHookRepo) Exec(ctx context.Context, args *HookCall) (int, any, error) {
	// Record call
	m.Calls = append(m.Calls, *args)

	// Simulate response
	if resp, ok := m.ResponseMap[args.Type]; ok {
		return StatusSuccess, resp, nil
	}

	// Default behavior
	return StatusSuccess, fmt.Sprintf("mock response for hook %s", args.Type), nil
}
