package hooks

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/taskengine"
)

// EchoHook is a simple hook that echoes back the input arguments.
type EchoHook struct{}

// NewEchoHook creates a new instance of EchoHook.
func NewEchoHook() taskengine.HookRepo {
	return &EchoHook{}
}

// Exec handles execution by echoing the input arguments.
func (e *EchoHook) Exec(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	switch dataType {
	case taskengine.DataTypeString:
		if inputStr, ok := input.(string); ok {
			return taskengine.StatusSuccess, inputStr, taskengine.DataTypeString, transition, nil
		}
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("invalid string input")
	case taskengine.DataTypeChatHistory:
		if chatHist, ok := input.(taskengine.ChatHistory); ok {
			lastMsg := chatHist.Messages[len(chatHist.Messages)-1]
			return taskengine.StatusSuccess, lastMsg.Content, taskengine.DataTypeString, lastMsg.Content, nil
		}
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("invalid chat history input")
	default:
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("unsupported data type: %v", dataType)
	}
}

// Supports returns the hook types supported by this hook.
func (e *EchoHook) Supports(ctx context.Context) ([]string, error) {
	return []string{"echo"}, nil
}

// Get returns the function corresponding to the hook name.
func (e *EchoHook) Get(name string) (func(context.Context, time.Time, any, taskengine.DataType, string, *taskengine.HookCall) (int, any, taskengine.DataType, string, error), error) {
	if name != "echo" {
		return nil, fmt.Errorf("unsupported hook type: %s", name)
	}
	return e.Exec, nil
}

var _ taskengine.HookRepo = (*EchoHook)(nil)
