package hooks

import (
	"context"
	"fmt"
	"time"

	"github.com/contenox/contenox/core/taskengine"
)

type SimpleHookRepo struct {
	hooks map[string]taskengine.HookRepo
}

func NewSimpleHookProvider(hooks map[string]taskengine.HookRepo) *SimpleHookRepo {
	return &SimpleHookRepo{
		hooks: hooks,
	}
}

func (m *SimpleHookRepo) Exec(ctx context.Context, startingTime time.Time, input any, dataType taskengine.DataType, args *taskengine.HookCall) (int, any, taskengine.DataType, error) {
	if hook, ok := m.hooks[args.Type]; ok {
		return hook.Exec(ctx, startingTime, input, dataType, args)
	}
	return taskengine.StatusUnknownHookProvider, nil, taskengine.DataTypeAny, fmt.Errorf("unknown hook type: %s", args.Type)
}

func (m *SimpleHookRepo) Supports(ctx context.Context) ([]string, error) {
	supported := make([]string, 0, len(m.hooks))
	for k := range m.hooks {
		supported = append(supported, k)
	}
	return supported, nil
}

var _ taskengine.HookRepo = (*SimpleHookRepo)(nil)
