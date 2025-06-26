package hooks

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/contenox/runtime-mvp/core/taskengine"
)

// Mux implements a hook router that dispatches to sub-hooks based on command prefixes.
// It supports command parsing in the format: /<command> [arguments]
type Mux struct {
	hooks map[string]taskengine.HookRepo
}

// NewMux creates a new Mux hook router with registered sub-hooks.
func NewMux(hooks map[string]taskengine.HookRepo) taskengine.HookRepo {
	return &Mux{
		hooks: hooks,
	}
}

// Exec processes input by either routing to a sub-hook or returning unaltered input.
func (m *Mux) Exec(ctx context.Context, startTime time.Time, input any, dataType taskengine.DataType, transition string, hookCall *taskengine.HookCall) (int, any, taskengine.DataType, string, error) {
	var inputStr string
	var chatHist *taskengine.ChatHistory

	switch dataType {
	case taskengine.DataTypeString:
		s, ok := input.(string)
		if !ok {
			return taskengine.StatusError, nil, taskengine.DataTypeAny, "",
				fmt.Errorf("invalid string input: %T", input)
		}
		inputStr = s
	case taskengine.DataTypeChatHistory:
		ch, ok := input.(taskengine.ChatHistory)
		if !ok {
			return taskengine.StatusError, nil, taskengine.DataTypeAny, "",
				fmt.Errorf("invalid chat history input: %T", input)
		}
		if len(ch.Messages) == 0 {
			return taskengine.StatusError, nil, taskengine.DataTypeAny, "",
				fmt.Errorf("empty chat history")
		}
		if len(ch.Messages[len(ch.Messages)-1].Content) == 0 {
			return taskengine.StatusError, nil, taskengine.DataTypeAny, "",
				fmt.Errorf("empty message content")
		}
		chatHist = &ch
		inputStr = ch.Messages[len(ch.Messages)-1].Content
	default:
		return taskengine.StatusError, nil, taskengine.DataTypeAny, "",
			fmt.Errorf("unsupported data type: %v", dataType)
	}

	// Normalize input for command check
	trimmedInput := strings.TrimSpace(inputStr)
	if !strings.HasPrefix(trimmedInput, "/") {
		return taskengine.StatusSuccess, input, dataType, transition, nil
	}

	// Parse command
	parts := strings.SplitN(trimmedInput, " ", 2)
	command := strings.ToLower(strings.TrimPrefix(parts[0], "/"))
	if command == "" {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, "",
			fmt.Errorf("empty command")
	}

	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	hook, exists := m.hooks[command]
	if !exists {
		return taskengine.StatusError, nil, taskengine.DataTypeAny, "",
			fmt.Errorf("unregistered command: %s", command)
	}

	status, result, resultType, transition, err := hook.Exec(
		ctx, startTime, args, taskengine.DataTypeString, command, hookCall,
	)
	if status != taskengine.StatusSuccess {
		return status, nil, resultType, transition, err
	}

	if chatHist != nil {
		if resultStr, ok := result.(string); ok {
			// Append only assistant response (user message already exists)
			chatHist.Messages = append(chatHist.Messages, taskengine.Message{
				Role:    "system",
				Content: resultStr,
			})
			return taskengine.StatusSuccess, *chatHist, taskengine.DataTypeChatHistory, transition, nil
		}
		return taskengine.StatusError, nil, taskengine.DataTypeAny, transition,
			fmt.Errorf("hook result not string: %T", result)
	}

	return taskengine.StatusSuccess, result, resultType, transition, nil
}

// Supports returns the single hook type "command_router" that this router handles.
func (m *Mux) Supports(ctx context.Context) ([]string, error) {
	return []string{"command_router"}, nil
}

// Get returns the Exec function for the mux hook.
func (m *Mux) Get(name string) (func(context.Context, time.Time, any, taskengine.DataType, string, *taskengine.HookCall) (int, any, taskengine.DataType, string, error), error) {
	if name != "command_router" {
		return nil, fmt.Errorf("unsupported hook type: %s", name)
	}
	return m.Exec, nil
}

var _ taskengine.HookRepo = (*Mux)(nil)
