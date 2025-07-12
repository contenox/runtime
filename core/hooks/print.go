package hooks

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/runtime-mvp/core/serverops"
	"github.com/contenox/runtime-mvp/core/taskengine"
)

// Print implements a simple hook that returns predefined messages
type Print struct {
	tracker serverops.ActivityTracker
}

// NewPrint creates a new Print instance
func NewPrint(tracker serverops.ActivityTracker) *Print {
	if tracker == nil {
		tracker = serverops.NoopTracker{}
	}
	return &Print{tracker: tracker}
}

func (h *Print) Exec(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	_, _, end := h.tracker.Start(ctx, "exec", "print_hook")
	defer end()
	message, ok := hookCall.Args["message"]
	if !ok {
		return taskengine.StatusError, nil, dataType, "", fmt.Errorf("missing 'message' argument in print hook")
	}
	switch dataType {
	case taskengine.DataTypeString:
		if _, ok := input.(string); ok {
			return taskengine.StatusSuccess, message, taskengine.DataTypeString, "print", nil
		}
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("invalid string input")
	case taskengine.DataTypeChatHistory:
		if chatHist, ok := input.(taskengine.ChatHistory); ok {
			chatHist.Messages = append(chatHist.Messages, taskengine.Message{
				Role:    "system",
				Content: message,
			})
			return taskengine.StatusSuccess, chatHist, taskengine.DataTypeChatHistory, "print", nil
		}
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition, fmt.Errorf("invalid chat history input")
	}
	return taskengine.StatusSuccess, message, taskengine.DataTypeString, "print", nil
}

func (h *Print) Supports(ctx context.Context) ([]string, error) {
	return []string{"print"}, nil
}

var _ taskengine.HookRepo = (*Print)(nil)
