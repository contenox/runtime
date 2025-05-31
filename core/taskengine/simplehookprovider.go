package taskengine

import (
	"context"
	"fmt"
)

type SimpleHookRepo struct {
	hooks map[string]HookRepo
}

func NewSimpleHookProvider(hooks map[string]HookRepo) *SimpleHookRepo {
	return &SimpleHookRepo{
		hooks: hooks,
	}
}

func (m *SimpleHookRepo) Exec(ctx context.Context, args *HookCall) (int, any, error) {
	if hook, ok := m.hooks[args.Type]; ok {
		return hook.Exec(ctx, args)
	}
	return StatusUnknownHookProvider, nil, fmt.Errorf("unknown hook type: %s", args.Type)
}
